package application

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
	"reader-backend/internal/db"
	"reader-backend/internal/payment"
)

type refundJob struct {
	id        string
	orderID   int64
	receiptID int64
	lease     time.Time
	request   payment.RefundRequest
}

func claimRefundCommands(ctx context.Context, account payment.WebhookAccount, scopeLegacy ...bool) ([]refundJob, error) {
	strictLegacy := len(scopeLegacy) != 0 && scopeLegacy[0]
	rows, err := db.Pool(ctx).Query(ctx, `WITH due AS (
		SELECT c.id,p.capture_id,CASE WHEN o.order_type='subscription' THEN 'sale' ELSE 'capture' END AS receipt_type,
			(NOT c.legacy_source AND c.first_sent_at IS NULL AND c.provider_refund_id IS NULL) AS send
		FROM payment_refund_commands c JOIN third_party_payment_orders p ON p.id=c.receipt_id
		JOIN recharge_orders o ON o.id=c.order_id
		LEFT JOIN payment_channel_accounts a ON a.id=p.account_id
		WHERE c.status IN ('pending','confirming') AND c.next_check_at<=now() AND c.environment=$2
		AND (c.claimed_until IS NULL OR c.claimed_until<=now())
		AND c.amount_minor IS NOT NULL AND p.payment_provider='paypal'
		AND ((a.account_key=$1 AND a.environment=$2) OR (p.account_id IS NULL AND (NOT $3 OR EXISTS (
			SELECT 1 FROM paypal_identity_lock l WHERE l.client_id=$1 AND l.environment=$2))))
		ORDER BY c.next_check_at,c.id LIMIT 5 FOR UPDATE OF c SKIP LOCKED
	)
	UPDATE payment_refund_commands c SET claimed_until=now()+interval '5 minutes',status='confirming',
		first_sent_at=CASE WHEN due.send THEN now() ELSE c.first_sent_at END,updated_at=now()
	FROM due WHERE c.id=due.id
	RETURNING c.id::text,c.order_id,c.receipt_id,c.claimed_until,due.capture_id,COALESCE(c.provider_refund_id,''),
		c.amount_minor,c.currency,due.receipt_type,due.send`, account.AccountKey, account.Environment, strictLegacy)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (refundJob, error) {
		job := refundJob{request: payment.RefundRequest{Account: account}}
		err := row.Scan(&job.id, &job.orderID, &job.receiptID, &job.lease, &job.request.ReceiptID, &job.request.RefundID,
			&job.request.AmountMinor, &job.request.Currency, &job.request.ReceiptType, &job.request.Send)
		job.request.RequestKey = job.id
		return job, err
	})
}

func finishRefundCommand(ctx context.Context, job refundJob, result *payment.RefundResult, lookupErr error) error {
	tx, err := db.Pool(ctx).Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if err := lockPayPalCapture(ctx, tx, job.request.ReceiptID); err != nil {
		return err
	}
	var oldRefundID *string
	var legacy bool
	err = tx.QueryRow(ctx, `SELECT provider_refund_id,legacy_source FROM payment_refund_commands
		WHERE id=$1 AND claimed_until=$2 AND status='confirming' FOR UPDATE`, job.id, job.lease).Scan(&oldRefundID, &legacy)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}

	status, note := "review", "Refund result requires provider evidence"
	var nextCheck *time.Time
	refundID := oldRefundID
	if result != nil && lookupErr == nil {
		refundID = &result.ID
		switch result.Status {
		case "COMPLETED":
			if result.AmountMinor != job.request.AmountMinor || result.Currency != job.request.Currency {
				return errors.New("verified refund amount mismatch")
			}
			if err := settlePayPalReceiptRefundTx(ctx, tx, job.orderID, result.ID, job.request.ReceiptID, result.AmountMinor, result.Currency); err != nil {
				return err
			}
			status, note = "succeeded", ""
		case "FAILED", "DENIED", "CANCELED", "CANCELLED":
			if legacy {
				if _, err := tx.Exec(ctx, `UPDATE recharge_orders o SET status=r.prior_order_status,updated_at=now()
					FROM payment_refunds r WHERE r.order_id=o.id AND o.id=$1 AND r.request_id=$2
						AND o.status='RefundPending' AND r.status='Pending'`, job.orderID, job.id); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `UPDATE payment_refunds SET status='Failed',updated_at=now()
					WHERE order_id=$1 AND request_id=$2 AND status='Pending'`, job.orderID, job.id); err != nil {
					return err
				}
			}
			status, note = "failed", "PayPal confirmed that the refund failed or was cancelled"
		case "PENDING":
			status, note = "confirming", ""
			next := time.Now().Add(5 * time.Minute)
			nextCheck = &next
		}
	} else if job.request.Send || job.request.RefundID != "" {
		status, note = "confirming", "PayPal response is unavailable; checking the original refund"
		next := time.Now().Add(time.Minute)
		nextCheck = &next
	}
	_, err = tx.Exec(ctx, `UPDATE payment_refund_commands SET status=$2,provider_refund_id=$3,next_check_at=$4,
		claimed_until=NULL,result_note=NULLIF($5,''),updated_at=now() WHERE id=$1`, job.id, status, refundID, nextCheck, note)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *service) reconcileRefundCommands(ctx context.Context) {
	adapter, err := payment.Capability[payment.RefundProvider](s.adapters, payment.ProviderPayPal)
	if err != nil {
		return
	}
	account, err := adapter.CheckoutAccount(ctx)
	if err != nil {
		return
	}
	jobs, err := claimRefundCommands(ctx, account, true)
	if err != nil {
		log.Printf("task=refund_command action=claim error=%v", err)
		return
	}
	for _, job := range jobs {
		if ctx.Err() != nil {
			return
		}
		req, cancel := context.WithTimeout(ctx, 30*time.Second)
		if job.request.Send {
			if err := s.checkRefundDispute(req, job); err != nil {
				cancel()
				if saveErr := parkRefundBeforeSend(ctx, job, err); saveErr != nil {
					log.Printf("task=refund_command id=%s action=park error=%v", job.id, saveErr)
				}
				continue
			}
		}

		result, lookupErr := adapter.ResolveRefund(req, job.request)
		cancel()
		if err := finishRefundCommand(ctx, job, result, lookupErr); err != nil {
			log.Printf("task=refund_command id=%s action=save error=%v", job.id, err)
		}
	}
}

// Inbox facts request a lookup for an already linked refund. A notification
// cannot assign an unknown refund to a command based on the amount or receipt.
func schedulePayPalRefundEventLookup(ctx context.Context, event *payment.WebhookEventResult) error {
	if event.ExternalRefID == "" || !paymentEnvironmentMatches(event.Account) {
		return ErrCheckoutUnavailable
	}
	_, err := db.Pool(ctx).Exec(ctx, `UPDATE payment_refund_commands c
		SET status='confirming',next_check_at=now(),updated_at=now()
		FROM third_party_payment_orders p
		LEFT JOIN payment_channel_accounts a ON a.id=p.account_id AND a.provider='paypal'
		WHERE p.id=c.receipt_id AND p.payment_provider='paypal'
		AND c.provider_refund_id=$1 AND c.environment=$3
		AND c.status IN ('pending','confirming','review')
		AND (c.claimed_until IS NULL OR c.claimed_until<=now())
		AND ($4='' OR p.capture_id=$4)
		AND ((a.account_key=$2 AND a.environment=$3) OR (p.account_id IS NULL AND EXISTS (
			SELECT 1 FROM paypal_identity_lock l WHERE l.client_id=$2 AND l.environment=$3)))`,
		event.ExternalRefID, event.Account.AccountKey, event.Account.Environment, event.CaptureID)
	return err
}
