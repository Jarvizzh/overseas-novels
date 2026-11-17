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
	rows, err := db.Pool(ctx).Query(ctx, `
 WITH order_attention AS (
   SELECT paid_unfulfilled, order_pending, manual_review,
          COALESCE(paid_at,created_at) AS payment_time, created_at, reconciliation_attempts
   FROM recharge_orders
   CROSS JOIN LATERAL (VALUES (
       status IN ('Success','Paid') AND fulfilled_at IS NULL,
       status='Pending' AND payment_method='paypal',
       reconciliation_note IS NOT NULL AND status<>'Refunded'
   )) AS flags(paid_unfulfilled,order_pending,manual_review)
   WHERE paid_unfulfilled OR order_pending OR manual_review
 ), order_totals AS (
   SELECT count(*) FILTER (WHERE paid_unfulfilled) AS paid_count,
          min(payment_time) FILTER (WHERE paid_unfulfilled) AS paid_oldest,
          max(reconciliation_attempts) FILTER (WHERE paid_unfulfilled) AS paid_attempts,
          count(*) FILTER (WHERE order_pending) AS pending_count,
          min(created_at) FILTER (WHERE order_pending) AS pending_oldest,
          max(reconciliation_attempts) FILTER (WHERE order_pending) AS pending_attempts,
          count(*) FILTER (WHERE manual_review) AS review_count,
          min(created_at) FILTER (WHERE manual_review) AS review_oldest,
          max(reconciliation_attempts) FILTER (WHERE manual_review) AS review_attempts
   FROM order_attention
 )
 SELECT 'inbox',count(*),min(created_at),COALESCE(max(attempts),0) FROM paypal_event_inbox WHERE processed_at IS NULL
 UNION ALL SELECT 'outbox',count(*),min(created_at),COALESCE(max(attempts),0) FROM payment_event_outbox WHERE state='pending' AND delivered_at IS NULL
 UNION ALL SELECT 'unattributed',count(*),min(created_at),COALESCE(max(attempts),0) FROM payment_event_outbox WHERE state='unattributed'
 UNION ALL SELECT 'cancel_pending',count(*),min(COALESCE(cancel_requested_at,updated_at)),COALESCE(max(cancel_attempts),0) FROM user_subscriptions WHERE status='CANCEL_PENDING'
 UNION ALL SELECT 'subscription_due',count(*),min(COALESCE(paypal_next_check_at,created_at)),COALESCE(max(paypal_check_attempts),0) FROM user_subscriptions WHERE payment_method='paypal' AND subscription_id NOT LIKE 'I-MOCK%' AND (status IN ('ACTIVE','SUSPENDED','CANCEL_PENDING') OR updated_at>now()-interval '30 days' OR paypal_recovery_requested) AND COALESCE(paypal_next_check_at,created_at)<=now()
 UNION ALL SELECT kind,task_count,oldest_at,COALESCE(max_attempts,0) FROM order_totals
 CROSS JOIN LATERAL (VALUES ('paid_unfulfilled',paid_count,paid_oldest,paid_attempts),
                            ('order_pending',pending_count,pending_oldest,pending_attempts),
                            ('manual_review',review_count,review_oldest,review_attempts))
                   AS summary(kind,task_count,oldest_at,max_attempts)
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
	result, err := db.Pool(ctx).Exec(ctx, query, args...)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrTaskNotRetryable
	}
	return nil
}
