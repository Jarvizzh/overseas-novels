package wallet

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"reader-backend/internal/db"
	"reader-backend/internal/payment"
)

type orderJob struct {
	id      string
	attempt int
}
type subscriptionJob struct {
	id            string
	cursor, lease time.Time
}

func claimPayPalOrders(ctx context.Context) ([]orderJob, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	rows, err := db.DB.Query(ctx, `
		WITH batch AS (
			SELECT id FROM recharge_orders WHERE payment_method='paypal' AND external_ref_id IS NOT NULL
			AND (status='Pending' OR (status IN ('Success','Paid') AND order_type='whole_book' AND fulfilled_at IS NULL))
			AND COALESCE(reconciliation_at,created_at) < now() - interval '5 minutes'
			AND (status <> 'Pending' OR created_at > now() - interval '30 days' OR checkout_id IS NULL)
			AND external_ref_id NOT LIKE 'mock%'
			ORDER BY COALESCE(reconciliation_at,created_at), id LIMIT 5 FOR UPDATE SKIP LOCKED
		)
		UPDATE recharge_orders o SET reconciliation_at=now(),reconciliation_attempts=o.reconciliation_attempts+1
		FROM batch WHERE o.id=batch.id RETURNING o.external_ref_id,o.reconciliation_attempts`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (orderJob, error) {
		var job orderJob
		err := row.Scan(&job.id, &job.attempt)
		return job, err
	})
}
func claimPayPalCancellations(ctx context.Context) ([]paypalCancellation, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
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

func claimPayPalSubscriptions(ctx context.Context) ([]subscriptionJob, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	rows, err := db.DB.Query(ctx, `WITH due AS (
		SELECT id FROM user_subscriptions WHERE payment_method='paypal'
		AND subscription_id NOT LIKE 'I-MOCK%'
		AND (status IN ('ACTIVE','SUSPENDED','CANCEL_PENDING') OR updated_at>now()-interval '30 days' OR paypal_recovery_requested)
		AND COALESCE(paypal_next_check_at,created_at)<=now()
		ORDER BY COALESCE(paypal_next_check_at,created_at),id LIMIT 5 FOR UPDATE SKIP LOCKED
	)
	UPDATE user_subscriptions s SET paypal_next_check_at=now()+interval '15 minutes',paypal_check_attempts=s.paypal_check_attempts+1
	FROM due WHERE s.id=due.id RETURNING s.subscription_id,
	COALESCE(s.paypal_transaction_cursor,GREATEST(s.created_at,now()-interval '30 days')),s.paypal_next_check_at`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (subscriptionJob, error) {
		var task subscriptionJob
		err := row.Scan(&task.id, &task.cursor, &task.lease)
		return task, err
	})
}

func finishOrderReconciliation(ctx context.Context, job orderJob, result error) error {
	var message *string
	if result != nil {
		text := result.Error()
		message = &text
	}
	_, err := db.DB.Exec(ctx, `UPDATE recharge_orders SET reconciliation_error=left($3,1000) WHERE external_ref_id=$1 AND reconciliation_attempts=$2`, job.id, job.attempt, message)
	return err
}
func finishCancellationRetry(ctx context.Context, task paypalCancellation, result error) error {
	_, err := db.DB.Exec(ctx, `UPDATE user_subscriptions SET cancel_last_error=left($3,1000),cancel_next_attempt_at=now()+interval '1 minute'*LEAST(cancel_attempts,30) WHERE subscription_id=$1 AND cancel_attempts=$2 AND status='CANCEL_PENDING'`, task.id, task.attempt, result.Error())
	return err
}
func finishSubscriptionReconciliation(ctx context.Context, job subscriptionJob, end time.Time, result error) error {
	if result != nil {
		_, err := db.DB.Exec(ctx, `UPDATE user_subscriptions SET paypal_check_error=left($3,1000) WHERE subscription_id=$1 AND paypal_next_check_at=$2`, job.id, job.lease, result.Error())
		return err
	}
	_, err := db.DB.Exec(ctx, `UPDATE user_subscriptions SET paypal_check_error=NULL,paypal_transaction_cursor=$2,paypal_recovery_requested=CASE WHEN $2<now()-interval '1 day' THEN paypal_recovery_requested ELSE false END,paypal_next_check_at=CASE WHEN $2<now()-interval '1 day' THEN now()+interval '30 seconds' ELSE now()+interval '1 hour' END WHERE subscription_id=$1 AND paypal_next_check_at=$3`, job.id, end, job.lease)
	return err
}

type inboxJob struct {
	event   payment.WebhookEventResult
	attempt int
}

func claimPayPalEvents(ctx context.Context) ([]inboxJob, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	rows, err := db.DB.Query(ctx, `WITH due AS (
		SELECT event_id FROM paypal_event_inbox WHERE processed_at IS NULL AND next_attempt_at<=now()
		ORDER BY next_attempt_at,event_id LIMIT 10 FOR UPDATE SKIP LOCKED
	)
	UPDATE paypal_event_inbox i SET attempts=i.attempts+1,next_attempt_at=now()+interval '10 minutes'
	FROM due WHERE i.event_id=due.event_id RETURNING i.event,i.attempts`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (inboxJob, error) {
		var job inboxJob
		err := row.Scan(&job.event, &job.attempt)
		return job, err
	})
}
func finishInboxEvent(ctx context.Context, job inboxJob, result error) error {
	if result != nil {
		_, err := db.DB.Exec(ctx, `UPDATE paypal_event_inbox SET last_error=left($3,1000),next_attempt_at=now()+interval '1 second'*LEAST(30*attempts,1800) WHERE event_id=$1 AND attempts=$2 AND processed_at IS NULL`, job.event.EventID, job.attempt, result.Error())
		return err
	}
	_, err := db.DB.Exec(ctx, `UPDATE paypal_event_inbox SET processed_at=now(),last_error=NULL WHERE event_id=$1 AND attempts=$2 AND processed_at IS NULL`, job.event.EventID, job.attempt)
	return err
}
