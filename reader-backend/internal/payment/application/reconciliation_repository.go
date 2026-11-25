package application

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"reader-backend/internal/conversion"
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

const (
	paypalOrder         = `payment_method='paypal' AND external_ref_id IS NOT NULL AND external_ref_id NOT LIKE 'mock%'`
	payPalOrderWindow   = "interval '30 days'"
	recentPayPalOrder   = `status='Pending' AND checkout_id IS NOT NULL AND created_at > now()-` + payPalOrderWindow
	retainedPayPalOrder = `((status='Pending' AND checkout_id IS NULL)
		OR (status IN ('Success','Paid') AND order_type='whole_book' AND fulfilled_at IS NULL))`
	orderCheckAt = `COALESCE(reconciliation_at,created_at)`
	orderDue     = paypalOrder + ` AND ` + orderCheckAt + ` < now()-interval '5 minutes' AND `
)

func claimPayPalOrders(ctx context.Context) ([]orderJob, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// Qualify recent orders before sorting. An ordered LIMIT over the old
	// due-time index can walk the entire expired Pending backlog.
	rows, err := db.Pool(ctx).Query(ctx, `WITH recent_candidates AS MATERIALIZED (
			SELECT id, `+orderCheckAt+` AS due_at FROM recharge_orders
			WHERE `+orderDue+recentPayPalOrder+` AND (checkout_environment IS NULL OR EXISTS(
				SELECT 1 FROM payment_checkout_attempts a WHERE a.order_id=recharge_orders.id AND a.provider='paypal'
				AND a.status IN ('buyer_action','capturing') AND a.provider_order_id IS NOT NULL))
		), recent AS (
			SELECT o.id, c.due_at FROM recent_candidates c JOIN recharge_orders o ON o.id=c.id
			WHERE `+orderDue+recentPayPalOrder+` AND `+orderCheckAt+`=c.due_at
			ORDER BY c.due_at,o.id LIMIT 5 FOR UPDATE OF o SKIP LOCKED
		), retained AS (
			SELECT id, `+orderCheckAt+` AS due_at FROM recharge_orders
			WHERE `+orderDue+`(`+retainedPayPalOrder+` OR (`+agedLegacyBookRecovery("recharge_orders", "$1")+`))
			ORDER BY due_at,id LIMIT 5 FOR UPDATE SKIP LOCKED
		), batch AS (
			SELECT id FROM (SELECT id,due_at FROM recent UNION ALL SELECT id,due_at FROM retained) candidates
			ORDER BY due_at,id LIMIT 5
		)
		UPDATE recharge_orders o SET reconciliation_at=now(),reconciliation_attempts=o.reconciliation_attempts+1
		FROM batch WHERE o.id=batch.id RETURNING COALESCE((
			SELECT a.provider_order_id FROM payment_checkout_attempts a WHERE a.order_id=o.id AND a.provider='paypal'
			AND a.status IN ('buyer_action','capturing') AND a.provider_order_id IS NOT NULL
		),o.external_ref_id),o.reconciliation_attempts`, conversion.Environment())
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
	rows, err := db.Pool(ctx).Query(ctx, `WITH due AS (
		SELECT id FROM user_subscriptions WHERE status='CANCEL_PENDING' AND payment_method='paypal'
		AND payment_subscription_environment(user_subscriptions)=$1 AND COALESCE(cancel_next_attempt_at,updated_at)<=now()
		ORDER BY COALESCE(cancel_next_attempt_at,updated_at),id LIMIT 5 FOR UPDATE SKIP LOCKED
	)
	UPDATE user_subscriptions s SET cancel_next_attempt_at=now()+interval '5 minutes',cancel_attempts=s.cancel_attempts+1
	FROM due WHERE s.id=due.id RETURNING s.subscription_id,COALESCE(s.cancel_reason,''),s.cancel_attempts`, conversion.Environment())
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (paypalCancellation, error) {
		var task paypalCancellation
		err := row.Scan(&task.id, &task.reason, &task.attempt)
		return task, err
	})
}

const (
	paypalSubscription     = `payment_method='paypal' AND subscription_id NOT LIKE 'I-MOCK%'`
	continuousSubscription = `(status IN ('ACTIVE','SUSPENDED','CANCEL_PENDING') OR paypal_recovery_requested)`
	recentSubscription     = `NOT ` + continuousSubscription + ` AND paypal_status_changed_at > now()-interval '30 days'`
	subscriptionCheckAt    = `COALESCE(paypal_next_check_at,created_at)`
	subscriptionDue        = paypalSubscription + ` AND ` + subscriptionCheckAt + `<=now() AND `
)

func claimPayPalSubscriptions(ctx context.Context) ([]subscriptionJob, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	rows, err := db.Pool(ctx).Query(ctx, `WITH continuous AS (
		SELECT id, `+subscriptionCheckAt+` AS due_at FROM user_subscriptions
		WHERE payment_subscription_environment(user_subscriptions)=$1 AND `+subscriptionDue+continuousSubscription+`
		ORDER BY due_at,id LIMIT 5 FOR UPDATE SKIP LOCKED
	), recent AS (
		SELECT id, `+subscriptionCheckAt+` AS due_at FROM user_subscriptions
		WHERE payment_subscription_environment(user_subscriptions)=$1 AND `+subscriptionDue+recentSubscription+`
		ORDER BY due_at,id LIMIT 5 FOR UPDATE SKIP LOCKED
	), due AS (
		SELECT id FROM (SELECT * FROM continuous UNION ALL SELECT * FROM recent) candidates
		ORDER BY due_at,id LIMIT 5
	)
	UPDATE user_subscriptions s SET paypal_next_check_at=now()+interval '15 minutes',paypal_check_attempts=s.paypal_check_attempts+1
	FROM due WHERE s.id=due.id RETURNING s.subscription_id,
	COALESCE(s.paypal_transaction_cursor,GREATEST(s.created_at,now()-interval '30 days')),s.paypal_next_check_at`, conversion.Environment())
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
	_, err := db.Pool(ctx).Exec(ctx, `UPDATE recharge_orders SET reconciliation_error=left($3,1000)
		WHERE reconciliation_attempts=$2 AND (external_ref_id=$1 OR id=(
			SELECT order_id FROM payment_checkout_attempts WHERE provider='paypal' AND provider_order_id=$1))`, job.id, job.attempt, message)
	return err
}
func finishCancellationRetry(ctx context.Context, task paypalCancellation, result error) error {
	_, err := db.Pool(ctx).Exec(ctx, `UPDATE user_subscriptions SET cancel_last_error=left($3,1000),cancel_next_attempt_at=now()+interval '1 minute'*LEAST(cancel_attempts,30) WHERE subscription_id=$1 AND cancel_attempts=$2 AND status='CANCEL_PENDING'`, task.id, task.attempt, result.Error())
	return err
}
func finishSubscriptionReconciliation(ctx context.Context, job subscriptionJob, end time.Time, result error) error {
	if result != nil {
		_, err := db.Pool(ctx).Exec(ctx, `UPDATE user_subscriptions SET paypal_check_error=left($3,1000) WHERE subscription_id=$1 AND paypal_next_check_at=$2`, job.id, job.lease, result.Error())
		return err
	}
	_, err := db.Pool(ctx).Exec(ctx, `UPDATE user_subscriptions SET paypal_check_error=NULL,paypal_transaction_cursor=$2,paypal_recovery_requested=CASE WHEN $2<now()-interval '1 day' THEN paypal_recovery_requested ELSE false END,paypal_next_check_at=CASE WHEN $2<now()-interval '1 day' THEN now()+interval '30 seconds' ELSE now()+interval '1 hour' END WHERE subscription_id=$1 AND paypal_next_check_at=$3`, job.id, end, job.lease)
	return err
}

type inboxJob struct {
	id      int64
	event   payment.WebhookEventResult
	attempt int
}

func claimPayPalEvents(ctx context.Context, accounts ...payment.WebhookAccount) ([]inboxJob, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	scope, args := activePayPalInbox, []any(nil)
	if len(accounts) != 0 {
		scope = `i.provider='paypal' AND (EXISTS(SELECT 1 FROM payment_channel_accounts a WHERE a.id=i.account_id
			AND a.provider='paypal' AND a.account_key=$1 AND a.environment=$2) OR (i.account_id IS NULL AND EXISTS(
			SELECT 1 FROM paypal_identity_lock l WHERE l.client_id=$1 AND l.environment=$2)))`
		args = []any{accounts[0].AccountKey, accounts[0].Environment}
	}
	rows, err := db.Pool(ctx).Query(ctx, `WITH due AS (
		SELECT i.id FROM payment_event_inbox i WHERE `+scope+`
		AND i.processed_at IS NULL AND i.next_attempt_at<=now()
		ORDER BY i.next_attempt_at,i.id LIMIT 10 FOR UPDATE SKIP LOCKED
	)
	UPDATE payment_event_inbox i SET attempts=i.attempts+1,next_attempt_at=now()+interval '10 minutes'
	FROM due LEFT JOIN payment_event_inbox original ON original.id=due.id
 LEFT JOIN payment_channel_accounts a ON a.id=original.account_id
 WHERE i.id=due.id RETURNING i.id,i.event,i.attempts,COALESCE(a.account_key,''),COALESCE(a.environment,'')`, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (inboxJob, error) {
		var job inboxJob
		err := row.Scan(&job.id, &job.event, &job.attempt, &job.event.Account.AccountKey, &job.event.Account.Environment)
		return job, err
	})
}
func finishInboxEvent(ctx context.Context, job inboxJob, result error) error {
	if result != nil {
		_, err := db.Pool(ctx).Exec(ctx, `UPDATE payment_event_inbox SET last_error=left($3,1000),next_attempt_at=now()+interval '1 second'*LEAST(30*attempts,1800) WHERE id=$1 AND attempts=$2 AND processed_at IS NULL`, job.id, job.attempt, result.Error())
		return err
	}
	_, err := db.Pool(ctx).Exec(ctx, `UPDATE payment_event_inbox SET processed_at=now(),last_error=NULL WHERE id=$1 AND attempts=$2 AND processed_at IS NULL`, job.id, job.attempt)
	return err
}
