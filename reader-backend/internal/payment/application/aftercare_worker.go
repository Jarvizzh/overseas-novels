package application

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
	"reader-backend/internal/db"
	"reader-backend/internal/payment/aftercare"
)

const aftercareInterval = 15 * time.Second
const aftercareBatchSize = 5

type deferredPayment struct {
	ExternalID     string
	SubscriptionID string
	ReceiptID      string
	PaidAt         time.Time
}

func claimDeferredPayments(ctx context.Context) ([]deferredPayment, error) {
	rows, err := db.Pool(ctx).Query(ctx, `WITH due AS (
		SELECT h.order_id,h.receipt_id,COALESCE(p.external_order_id,o.external_ref_id,'') AS external_id,
			COALESCE(o.subscription_id,'') AS subscription_id,p.capture_id,o.paid_at
		FROM payment_deferred_fulfillment h JOIN recharge_orders o ON o.id=h.order_id
		JOIN third_party_payment_orders p ON p.id=h.receipt_id
		JOIN payment_channel_accounts a ON a.id=p.account_id AND a.provider='paypal'
		LEFT JOIN payment_cases c ON c.id=h.case_id
		WHERE h.resolved_at IS NULL AND o.status IN ('Paid','Success') AND o.fulfilled_at IS NULL
		  AND a.environment=$2 AND h.next_check_at<=now() AND (h.case_id IS NULL OR c.work_state='approved')
		ORDER BY h.next_check_at,h.order_id LIMIT $1 FOR UPDATE OF h SKIP LOCKED
	)
	UPDATE payment_deferred_fulfillment h SET next_check_at=now()+interval '5 minutes'
	FROM due WHERE h.order_id=due.order_id AND h.receipt_id=due.receipt_id
	RETURNING due.external_id,due.subscription_id,due.capture_id,due.paid_at`, aftercareBatchSize, currentPaymentLedgerEnvironment())
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (deferredPayment, error) {
		var item deferredPayment
		err := row.Scan(&item.ExternalID, &item.SubscriptionID, &item.ReceiptID, &item.PaidAt)
		return item, err
	})
}

func (s *service) reconcileAftercare(ctx context.Context) {
	repo := aftercare.NewRepository(db.Pool(ctx))
	if err := repo.Maintain(ctx); err != nil {
		log.Printf("task=aftercare action=maintain error=%v", err)
	}
	for range aftercareBatchSize {
		didWork, err := repo.ApplyNextLoss(ctx)
		if err != nil {
			log.Printf("task=aftercare action=loss error=%v", err)
			break
		}
		if !didWork {
			break
		}
	}
	items, err := claimDeferredPayments(ctx)
	if err != nil {
		log.Printf("task=aftercare action=claim error=%v", err)
		return
	}
	for _, item := range items {
		request, cancel := context.WithTimeout(ctx, 45*time.Second)
		err := s.retryDeferredPayment(request, item)
		cancel()
		if err != nil {
			log.Printf("task=aftercare action=verify_receipt receipt=%s error=%v", item.ReceiptID, err)
		}
	}
}

func (s *service) retryDeferredPayment(ctx context.Context, item deferredPayment) error {
	if item.SubscriptionID == "" {
		_, err := s.completePayPalOrder(ctx, 0, item.ExternalID, false)
		if errors.Is(err, ErrPaymentRefunded) {
			return nil
		}
		return err
	}
	sub, err := s.repo.GetSubscriptionByID(ctx, item.SubscriptionID)
	if err != nil {
		return err
	}
	if sub == nil {
		return ErrCheckoutUnavailable
	}
	client, err := s.payPalForSubscription(ctx, item.SubscriptionID)
	if err != nil || client == nil {
		return errors.Join(ErrCheckoutUnavailable, err)
	}
	details, err := client.GetSubscription(ctx, item.SubscriptionID)
	if err != nil {
		return err
	}
	if details.SubscriptionID != sub.SubscriptionID || details.PlanID != sub.PlanID {
		return ErrCheckoutUnavailable
	}
	through := minTime(time.Now().Add(time.Second), item.PaidAt.Add(24*time.Hour))
	transactions, err := client.ListSubscriptionTransactions(ctx, item.SubscriptionID, item.PaidAt.Add(-24*time.Hour), through)
	if err != nil {
		return err
	}
	for _, txn := range transactions {
		if txn.ID == item.ReceiptID {
			return s.recordPayPalSubscriptionTransaction(ctx, sub, details, txn)
		}
	}
	return errors.New("deferred sale is not visible in the PayPal ledger")
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
