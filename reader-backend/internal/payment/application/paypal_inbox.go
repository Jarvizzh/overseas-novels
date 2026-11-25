package application

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
	"reader-backend/internal/db"
	"reader-backend/internal/payment"
	"reader-backend/internal/payment/aftercare"
	"reader-backend/internal/payment/paypal"
)

var errRefundUnmatched = errors.New("refund is waiting for the original payment")
var ErrPaymentRefunded = errors.New("payment was refunded")

const activePayPalInbox = `i.provider='paypal' AND (i.account_id IS NULL OR EXISTS (
	SELECT 1 FROM payment_channel_accounts a JOIN paypal_identity_lock p
		ON p.client_id=a.account_key AND p.environment=a.environment
	WHERE a.id=i.account_id
))`

func lockPayPalCapture(ctx context.Context, tx pgx.Tx, captureID string) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('paypal-capture:' || $1,0))`, captureID)
	return err
}

func (r *dbRepository) enqueuePayPalEvent(ctx context.Context, event *payment.WebhookEventResult, verifiedRegistration ...bool) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	tx, err := db.Pool(ctx).Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	var accountID int64
	registered := len(verifiedRegistration) != 0 && verifiedRegistration[0]
	err = tx.QueryRow(ctx, `SELECT a.id FROM payment_channel_accounts a WHERE a.provider='paypal' AND a.account_key=$1 AND a.environment=$2
		AND ($3 OR EXISTS(SELECT 1 FROM paypal_identity_lock l WHERE l.client_id=a.account_key AND l.environment=a.environment)) FOR SHARE OF a`,
		event.Account.AccountKey, event.Account.Environment, registered).Scan(&accountID)
	if errors.Is(err, pgx.ErrNoRows) {
		return errors.New("PayPal event verification account does not match the active account")
	}
	if err != nil {
		return err
	}
	hasRefundEvidence := event.EventType == "refund_completed" && event.CaptureID != ""
	if hasRefundEvidence || event.EventType == "payment_reversed" && event.CaptureID != "" {
		if err := lockPayPalCapture(ctx, tx, event.CaptureID); err != nil {
			return err
		}
	}

	inserted, err := tx.Exec(ctx, `INSERT INTO payment_event_inbox
		(provider,account_id,event_id,event_type,capture_id,refund_id,amount_cents,currency,event)
	SELECT 'paypal',$1,$2,$3,$4,$5,$6,$7,$8::jsonb
	WHERE NOT EXISTS (SELECT 1 FROM payment_event_inbox WHERE provider='paypal' AND account_id IS NULL AND event_id=$2)
	AND ($3::text='refund_completed' OR NOT EXISTS (SELECT 1 FROM paypal_processed_events WHERE id=$2))
	ON CONFLICT ON CONSTRAINT payment_event_identity DO NOTHING`, accountID, event.EventID, event.EventType,
		event.CaptureID, event.ExternalRefID, event.AmountCents, event.Currency, payload)
	if err != nil {
		return err
	}
	// Settlement sees the new event and refund evidence together. A replay leaves both intact.
	if hasRefundEvidence && inserted.RowsAffected() == 1 {
		var wrongScope bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM third_party_payment_orders WHERE payment_provider='paypal' AND capture_id=$1
			AND account_id IS NOT NULL AND account_id<>$2)`, event.CaptureID, accountID).Scan(&wrongScope); err != nil {
			return err
		}
		if wrongScope {
			return ErrCheckoutUnavailable
		}
		if _, err := tx.Exec(ctx, `SELECT save_paypal_refund_item($1,$2,$3,$4)`,
			event.ExternalRefID, event.CaptureID, event.AmountCents, event.Currency); err != nil {
			return err
		}
	}
	if inserted.RowsAffected() == 1 {
		fact, err := paypal.WebhookCase(event)
		if err != nil {
			return err
		}
		if fact != nil {
			if err := aftercare.RecordTx(ctx, tx, *fact); err != nil {
				return err
			}
		}
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
	client := s.primaryPayPalClient()
	if client == nil {
		return
	}
	account, err := client.CheckoutAccount(ctx)
	if err != nil || !paymentEnvironmentMatches(account) {
		return
	}
	events, err := claimPayPalEvents(ctx, account)
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
		err := s.applyPayPalEvent(requestCtx, client, event)
		cancel()
		if err != nil {
			log.Printf("task=inbox id=%s attempt=%d error=%v", event.EventID, job.attempt, err)
		}
		if saveErr := finishInboxEvent(ctx, job, err); saveErr != nil {
			log.Printf("task=inbox id=%s action=save error=%v", event.EventID, saveErr)
		}
	}
}
