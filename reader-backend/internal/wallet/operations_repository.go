package wallet

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"reader-backend/internal/db"
)

type TaskStatus struct {
	Kind        string     `json:"kind"`
	Count       int64      `json:"count"`
	OldestAt    *time.Time `json:"oldest_at"`
	MaxAttempts int        `json:"max_attempts"`
}

// PaymentTaskStatus excludes payloads, credentials and customer data.
func PaymentTaskStatus(ctx context.Context) ([]TaskStatus, error) {
	rows, err := db.DB.Query(ctx, `
 SELECT 'inbox',count(*),min(created_at),COALESCE(max(attempts),0) FROM paypal_event_inbox WHERE processed_at IS NULL
 UNION ALL SELECT 'outbox',count(*),min(created_at),COALESCE(max(attempts),0) FROM payment_event_outbox WHERE state='pending' AND delivered_at IS NULL
 UNION ALL SELECT 'unattributed',count(*),min(created_at),COALESCE(max(attempts),0) FROM payment_event_outbox WHERE state='unattributed'
 UNION ALL SELECT 'cancel_pending',count(*),min(COALESCE(cancel_requested_at,updated_at)),COALESCE(max(cancel_attempts),0) FROM user_subscriptions WHERE status='CANCEL_PENDING'
 UNION ALL SELECT 'subscription_due',count(*),min(COALESCE(paypal_next_check_at,created_at)),COALESCE(max(paypal_check_attempts),0) FROM user_subscriptions WHERE payment_method='paypal' AND subscription_id NOT LIKE 'I-MOCK%' AND (status IN ('ACTIVE','SUSPENDED','CANCEL_PENDING') OR updated_at>now()-interval '30 days' OR paypal_recovery_requested) AND COALESCE(paypal_next_check_at,created_at)<=now()
 UNION ALL SELECT 'paid_unfulfilled',count(*),min(COALESCE(paid_at,created_at)),COALESCE(max(reconciliation_attempts),0) FROM recharge_orders WHERE status IN ('Success','Paid') AND fulfilled_at IS NULL
 UNION ALL SELECT 'order_pending',count(*),min(created_at),COALESCE(max(reconciliation_attempts),0) FROM recharge_orders WHERE status='Pending' AND payment_method='paypal'
 UNION ALL SELECT 'manual_review',count(*),min(created_at),COALESCE(max(reconciliation_attempts),0) FROM recharge_orders WHERE reconciliation_note IS NOT NULL AND status<>'Refunded'
 UNION ALL SELECT 'refund_shortfall',count(*),min(updated_at),0 FROM payment_refunds WHERE charged_coin_shortfall>0 OR bonus_coin_shortfall>0
 UNION ALL SELECT 'subscription_intent_uncertain',count(*),min(submitted_at),0 FROM paypal_subscription_intents WHERE submitted_at IS NOT NULL AND subscription_id IS NULL`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (TaskStatus, error) {
		var status TaskStatus
		err := row.Scan(&status.Kind, &status.Count, &status.OldestAt, &status.MaxAttempts)
		return status, err
	})
}

var ErrTaskNotRetryable = errors.New("task is absent, completed or currently leased")

// RetryPaymentTask only changes scheduling. It never changes receipts, payloads,
// balances or entitlements. A subscription cursor may only move backwards.
func RetryPaymentTask(ctx context.Context, kind, id string, from *time.Time) error {
	var query string
	args := []any{id}
	switch kind {
	case "inbox":
		query = `UPDATE paypal_event_inbox SET next_attempt_at=now() WHERE event_id=$1 AND processed_at IS NULL AND next_attempt_at<=now()`
	case "outbox":
		query = `UPDATE payment_event_outbox SET next_attempt_at=now() WHERE event_id=$1 AND state='pending' AND delivered_at IS NULL AND (lease_until IS NULL OR lease_until<=now())`
	case "order":
		orderID, err := strconv.ParseInt(id, 10, 64)
		if err != nil || orderID <= 0 {
			return errors.New("order retry requires a positive local order ID")
		}
		args[0] = orderID
		query = `UPDATE recharge_orders SET reconciliation_at=now()-interval '6 minutes' WHERE id=$1 AND payment_method='paypal' AND (status='Pending' OR (status IN ('Success','Paid') AND order_type='whole_book' AND fulfilled_at IS NULL)) AND COALESCE(reconciliation_at,created_at)<=now()-interval '5 minutes' AND external_ref_id IS NOT NULL AND external_ref_id NOT LIKE 'mock%' AND (status<>'Pending' OR created_at>now()-interval '30 days' OR checkout_id IS NULL)`
	case "cancel":
		query = `UPDATE user_subscriptions SET cancel_next_attempt_at=now() WHERE subscription_id=$1 AND payment_method='paypal' AND status='CANCEL_PENDING' AND COALESCE(cancel_next_attempt_at,updated_at)<=now()`
	case "subscription":
		if from == nil || from.IsZero() || from.After(time.Now()) {
			return errors.New("subscription retry requires a past RFC3339 cursor")
		}
		query = `UPDATE user_subscriptions SET paypal_next_check_at=now(),paypal_transaction_cursor=$2,paypal_recovery_requested=true WHERE subscription_id=$1 AND payment_method='paypal' AND subscription_id NOT LIKE 'I-MOCK%' AND $2::timestamptz<=COALESCE(paypal_transaction_cursor,now()) AND COALESCE(paypal_next_check_at,created_at)<=now()`
		args = append(args, *from)
	default:
		return fmt.Errorf("unknown task kind %q", kind)
	}
	result, err := db.DB.Exec(ctx, query, args...)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrTaskNotRetryable
	}
	return nil
}
