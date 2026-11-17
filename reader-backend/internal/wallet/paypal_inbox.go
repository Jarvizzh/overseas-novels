package wallet

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
	"reader-backend/internal/db"
	"reader-backend/internal/payment"
)

var errRefundUnmatched = errors.New("refund is waiting for the original payment")
var ErrPaymentRefunded = errors.New("payment was refunded")

func lockPayPalCapture(ctx context.Context, tx pgx.Tx, captureID string) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('paypal-capture:' || $1,0))`, captureID)
	return err
}

func (r *dbRepository) enqueuePayPalEvent(ctx context.Context, event *payment.WebhookEventResult) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	tx, err := db.Pool(ctx).Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if event.EventType == "refund_completed" && event.CaptureID != "" {
		if err := lockPayPalCapture(ctx, tx, event.CaptureID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT save_paypal_refund_item($1,$2,$3,$4)`,
			event.ExternalRefID, event.CaptureID, event.AmountCents, event.Currency); err != nil {
			return err
		}
	}

	_, err = tx.Exec(ctx, `INSERT INTO paypal_event_inbox
		(event_id,event_type,capture_id,refund_id,amount_cents,currency,event)
		SELECT $1,$2,$3,$4,$5,$6,$7::jsonb
		WHERE $2::text='refund_completed' OR NOT EXISTS (SELECT 1 FROM paypal_processed_events WHERE id=$1)
		ON CONFLICT(event_id) DO NOTHING`, event.EventID, event.EventType, event.CaptureID, event.ExternalRefID,
		event.AmountCents, event.Currency, payload)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Applies a previously verified full refund before any coins or entitlement are granted.
func applyRefundBeforeFulfillment(ctx context.Context, tx pgx.Tx, orderID int64, captureID string, amount int64, currency string) (bool, error) {
	var refundID string
	var refunded int64
	err := tx.QueryRow(ctx, `SELECT COALESCE(max(refund_id),''),COALESCE(sum(amount_cents),0)
 FROM paypal_refund_items WHERE capture_id=$1 AND currency=$2`, captureID, currency).Scan(&refundID, &refunded)
	if err != nil {
		return false, err
	}
	if refunded < amount {
		return false, nil
	}
	if refunded != amount {
		return false, errors.New("PayPal refund total differs from original payment")
	}
	_, err = tx.Exec(ctx, `INSERT INTO payment_refunds (order_id,request_id,provider_refund_id,status)
		VALUES($1,gen_random_uuid(),$2,'Completed')`, orderID, refundID)
	if err != nil {
		return false, err
	}
	_, err = tx.Exec(ctx, `UPDATE recharge_orders SET status='Refunded', fulfilled_at=NULL,
		paid_at=COALESCE(paid_at,now()),updated_at=now() WHERE id=$1`, orderID)
	return err == nil, err
}

func (s *service) reconcilePayPalEvents(ctx context.Context) {
	events, err := claimPayPalEvents(ctx)
	if err != nil {
		log.Printf("task=inbox action=claim error=%v", err)
		return
	}

	for i := range events {
		if ctx.Err() != nil {
			return
		}
		job := events[i]
		event := &job.event
		requestCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
		err := s.applyPayPalEvent(requestCtx, s.paypalClient, event)
		cancel()
		if err != nil {
			log.Printf("task=inbox id=%s attempt=%d error=%v", event.EventID, job.attempt, err)
		}
		if saveErr := finishInboxEvent(ctx, job, err); saveErr != nil {
			log.Printf("task=inbox id=%s action=save error=%v", event.EventID, saveErr)
		}
	}
}
