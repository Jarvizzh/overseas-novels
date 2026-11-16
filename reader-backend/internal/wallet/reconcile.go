package wallet

import (
	"context"
	"errors"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"reader-backend/internal/db"
	"reader-backend/internal/model"
	"reader-backend/internal/payment"
)

func (s *service) RunReconciliation(ctx context.Context) {
	var workers sync.WaitGroup
	for _, run := range []func(context.Context){
		s.reconcilePayPalEvents, s.reconcilePayPalOrders,
		s.reconcilePayPalCancellations, s.reconcilePayPalSubscriptions,
	} {
		workers.Go(func() {
			ticker := time.NewTicker(15 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					run(ctx)
				}
			}
		})
	}
	workers.Wait()
}

func (s *service) reconcilePayPalOrders(ctx context.Context) {
	rows, err := db.DB.Query(ctx, `
		WITH batch AS (
			SELECT id FROM recharge_orders WHERE payment_method='paypal' AND external_ref_id IS NOT NULL
			AND (status='Pending' OR (status IN ('Success','Paid') AND order_type='whole_book' AND fulfilled_at IS NULL))
			AND COALESCE(reconciliation_at,created_at) < now() - interval '5 minutes'
			AND (status <> 'Pending' OR created_at > now() - interval '30 days' OR checkout_id IS NULL)
			AND external_ref_id NOT LIKE 'mock%'
			ORDER BY COALESCE(reconciliation_at,created_at), id LIMIT 5 FOR UPDATE SKIP LOCKED
		)
		UPDATE recharge_orders o SET reconciliation_at=now()
		FROM batch WHERE o.id=batch.id RETURNING o.external_ref_id`)
	if err != nil {
		log.Printf("PayPal reconciliation batch: %v", err)
		return
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return
	}
	for _, id := range ids {
		requestCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
		_, _ = s.completePayPalOrder(requestCtx, 0, id, true)
		cancel()
	}
}

type paypalCancellation struct {
	id, reason string
	attempt    int
}

func claimPayPalCancellations(ctx context.Context) ([]paypalCancellation, error) {
	rows, err := db.DB.Query(ctx, `WITH due AS (
		SELECT id FROM user_subscriptions WHERE status='CANCEL_PENDING' AND payment_method='paypal'
		AND COALESCE(cancel_next_attempt_at,updated_at)<=now()
		ORDER BY COALESCE(cancel_next_attempt_at,updated_at),id LIMIT 5 FOR UPDATE SKIP LOCKED
	)
	UPDATE user_subscriptions s SET cancel_next_attempt_at=now()+interval '5 minutes',cancel_attempts=s.cancel_attempts+1
	FROM due WHERE s.id=due.id RETURNING s.subscription_id,COALESCE(s.cancel_reason,''),s.cancel_attempts`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (paypalCancellation, error) {
		var task paypalCancellation
		err := row.Scan(&task.id, &task.reason, &task.attempt)
		return task, err
	})
}

func (s *service) reconcilePayPalCancellations(ctx context.Context) {
	tasks, err := claimPayPalCancellations(ctx)
	if err != nil {
		return
	}
	for _, task := range tasks {
		req, cancel := context.WithTimeout(ctx, 40*time.Second)
		err := s.confirmPayPalCancellation(req, task.id, task.reason)
		cancel()
		if err == nil {
			continue
		}
		_, _ = db.DB.Exec(ctx, `UPDATE user_subscriptions SET cancel_last_error=left($3,1000),
			cancel_next_attempt_at=now()+interval '1 minute'*LEAST(cancel_attempts,30)
			WHERE subscription_id=$1 AND cancel_attempts=$2 AND status='CANCEL_PENDING'`, task.id, task.attempt, err.Error())
	}
}

func (s *service) confirmPayPalCancellation(ctx context.Context, id, reason string) error {
	details, err := s.paypalClient.GetSubscription(ctx, id)
	if err != nil {
		return err
	}
	if details.SubscriptionID != id {
		return errors.New("unexpected subscription ID")
	}
	switch details.Status {
	case "CANCELLED", "EXPIRED":
		return s.repo.UpdateUserSubscriptionStatus(ctx, id, details.Status)
	default:
		if err := s.paypalClient.CancelSubscription(ctx, id, reason); err != nil {
			return err
		}
		return s.repo.UpdateUserSubscriptionStatus(ctx, id, "CANCELLED")
	}
}

func (s *service) reconcilePayPalSubscriptions(ctx context.Context) {
	rows, err := db.DB.Query(ctx, `WITH due AS (
		SELECT id FROM user_subscriptions WHERE payment_method='paypal'
		AND subscription_id NOT LIKE 'I-MOCK%'
		AND (status IN ('ACTIVE','SUSPENDED','CANCEL_PENDING') OR updated_at>now()-interval '30 days')
		AND COALESCE(paypal_next_check_at,created_at)<=now()
		ORDER BY COALESCE(paypal_next_check_at,created_at),id LIMIT 5 FOR UPDATE SKIP LOCKED
	)
	UPDATE user_subscriptions s SET paypal_next_check_at=now()+interval '15 minutes'
	FROM due WHERE s.id=due.id RETURNING s.subscription_id,
	COALESCE(s.paypal_transaction_cursor,GREATEST(s.created_at,now()-interval '30 days')),s.paypal_next_check_at`)
	if err != nil {
		return
	}
	type job struct {
		id            string
		cursor, lease time.Time
	}
	jobs, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (job, error) {
		var task job
		err := row.Scan(&task.id, &task.cursor, &task.lease)
		return task, err
	})
	if err != nil {
		return
	}
	for _, job := range jobs {
		req, cancel := context.WithTimeout(ctx, time.Minute)
		now := time.Now().UTC().Truncate(time.Second)
		end := job.cursor.AddDate(0, 0, 6).Truncate(time.Second)
		if end.After(now) {
			end = now
		}
		err := s.reconcileSubscriptionWindow(req, job.id, job.cursor.Add(-24*time.Hour), end)
		cancel()
		if err != nil {
			log.Printf("PayPal subscription reconciliation deferred: %v", err)
			continue
		}
		_, _ = db.DB.Exec(ctx, `UPDATE user_subscriptions SET paypal_transaction_cursor=$2,
			paypal_next_check_at=CASE WHEN $2 < now()-interval '1 day'
				THEN now()+interval '30 seconds' ELSE now()+interval '1 hour' END
			WHERE subscription_id=$1 AND paypal_next_check_at=$3`, job.id, end, job.lease)
	}
}

func (s *service) reconcileSubscriptionWindow(ctx context.Context, id string, from, to time.Time) error {
	sub, err := s.repo.GetSubscriptionByID(ctx, id)
	if err != nil {
		return err
	}
	if sub == nil || strings.TrimSpace(sub.PlanID) == "" {
		return ErrCheckoutUnavailable
	}
	details, err := s.paypalClient.GetSubscription(ctx, id)
	if err != nil {
		return err
	}
	if details.SubscriptionID != id || details.PlanID != sub.PlanID {
		return ErrCheckoutUnavailable
	}
	if details.Status == "APPROVAL_PENDING" || details.Status == "APPROVED" {
		return nil
	}
	return s.importPayPalSubscriptionTransactions(ctx, sub, details, from, to)
}

func (s *service) importPayPalSubscriptionTransactions(ctx context.Context,
	sub *model.UserSubscription, details *payment.SubscriptionDetails, from, to time.Time) error {

	transactions, err := s.paypalClient.ListSubscriptionTransactions(ctx, sub.SubscriptionID, from, to)
	if err != nil {
		return err
	}
	for _, transaction := range transactions {
		if err := s.recordPayPalSubscriptionTransaction(ctx, sub, details, transaction); err != nil {
			return err
		}
	}
	return nil
}
