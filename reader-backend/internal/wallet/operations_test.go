package wallet

import (
	"context"
	"errors"
	"testing"
	"time"

	"reader-backend/internal/testdb"
)

func TestTaskRecoveryFencesAndMonitoring(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO users(id) VALUES(1);
 INSERT INTO recharge_orders(id,user_id,external_ref_id,amount_cents,payment_method,status,created_at) VALUES(1,1,'OPS-ORDER',999,'paypal','Pending',now()-interval '1 hour');
 INSERT INTO paypal_event_inbox(event_id,event_type,amount_cents,currency,event) VALUES('OPS-INBOX','ignored',999,'USD','{"EventID":"OPS-INBOX"}');
 INSERT INTO payment_event_outbox(event_id,order_id,event_name,payload,state,next_attempt_at) VALUES('OPS-OUTBOX',1,'Purchase','{}','pending',now()+interval '1 hour'),('OPS-DELIVERED',1,'Purchase','{}','delivered',now()),('OPS-UNATTRIBUTED',1,'Purchase','{}','unattributed',now());
 INSERT INTO user_subscriptions(user_id,subscription_id,plan_id,status,cycle,price_cents,currency,payment_method,paypal_next_check_at,paypal_transaction_cursor) VALUES(1,'I-OPS','P-OPS','ACTIVE','month',999,'USD','paypal',now()-interval '1 minute',now()-interval '1 day')`)
	orders, err := claimPayPalOrders(ctx)
	if err != nil || len(orders) != 1 {
		t.Fatalf("claim orders: %v %v", orders, err)
	}
	if err := RetryPaymentTask(ctx, "order", "1", nil); !errors.Is(err, ErrTaskNotRetryable) {
		t.Fatalf("active order lease bypassed: %v", err)
	}
	if err := finishOrderReconciliation(ctx, orders[0], errors.New("channel unavailable")); err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE recharge_orders SET reconciliation_at=now()-interval '6 minutes' WHERE id=1`)
	newOrders, err := claimPayPalOrders(ctx)
	if err != nil || len(newOrders) != 1 {
		t.Fatal(err)
	}
	if err := finishOrderReconciliation(ctx, orders[0], nil); err != nil {
		t.Fatal(err)
	}
	var message string
	if err := pool.QueryRow(ctx, `SELECT reconciliation_error FROM recharge_orders WHERE id=1`).Scan(&message); err != nil || message != "channel unavailable" {
		t.Fatalf("stale worker overwrote result: %s %v", message, err)
	}
	if err := finishOrderReconciliation(ctx, newOrders[0], nil); err != nil {
		t.Fatal(err)
	}

	jobs, err := claimPayPalEvents(ctx)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("inbox claim: %+v %v", jobs, err)
	}
	if err := RetryPaymentTask(ctx, "inbox", "OPS-INBOX", nil); !errors.Is(err, ErrTaskNotRetryable) {
		t.Fatalf("active inbox lease bypassed: %v", err)
	}
	exec(`UPDATE paypal_event_inbox SET next_attempt_at=now()-interval '1 minute'`)
	newJobs, err := claimPayPalEvents(ctx)
	if err != nil || len(newJobs) != 1 {
		t.Fatal(err)
	}
	if err := finishInboxEvent(ctx, jobs[0], nil); err != nil {
		t.Fatal(err)
	}
	var processed bool
	if err := pool.QueryRow(ctx, `SELECT processed_at IS NOT NULL FROM paypal_event_inbox WHERE event_id='OPS-INBOX'`).Scan(&processed); err != nil || processed {
		t.Fatalf("stale inbox acknowledged: %t %v", processed, err)
	}
	if err := finishInboxEvent(ctx, newJobs[0], errors.New("receipt not found")); err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE paypal_event_inbox SET next_attempt_at=now()-interval '1 minute'`)
	if err := RetryPaymentTask(ctx, "inbox", "OPS-INBOX", nil); err != nil {
		t.Fatal(err)
	}
	if err := finishInboxEvent(ctx, newJobs[0], nil); err != nil {
		t.Fatal(err)
	}
	if err := RetryPaymentTask(ctx, "inbox", "OPS-INBOX", nil); !errors.Is(err, ErrTaskNotRetryable) {
		t.Fatalf("processed inbox retried: %v", err)
	}
	if err := RetryPaymentTask(ctx, "outbox", "OPS-OUTBOX", nil); !errors.Is(err, ErrUseConversionOps) {
		t.Fatalf("legacy outbox retry should direct operator to conversion-ops: %v", err)
	}
	var historicalUnchanged bool
	if err := pool.QueryRow(ctx, `SELECT next_attempt_at>now()+interval '50 minutes'
		FROM payment_event_outbox WHERE event_id='OPS-OUTBOX'`).Scan(&historicalUnchanged); err != nil || !historicalUnchanged {
		t.Fatalf("legacy row changed: %v", err)
	}

	from := time.Now().UTC().Add(-48 * time.Hour)
	if err := RetryPaymentTask(ctx, "subscription", "I-OPS", &from); err != nil {
		t.Fatal(err)
	}
	var cursor time.Time
	if err := pool.QueryRow(ctx, `SELECT paypal_transaction_cursor FROM user_subscriptions WHERE subscription_id='I-OPS'`).Scan(&cursor); err != nil || cursor.Sub(from).Abs() > time.Microsecond {
		t.Fatalf("cursor=%s %v", cursor, err)
	}
	forward := time.Now().Add(-time.Hour)
	if err := RetryPaymentTask(ctx, "subscription", "I-OPS", &forward); !errors.Is(err, ErrTaskNotRetryable) {
		t.Fatalf("cursor advanced: %v", err)
	}
	exec(`UPDATE user_subscriptions SET status='CANCEL_PENDING' WHERE subscription_id='I-OPS'`)
	var requested time.Time
	if err := pool.QueryRow(ctx, `SELECT cancel_requested_at FROM user_subscriptions WHERE subscription_id='I-OPS'`).Scan(&requested); err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE user_subscriptions SET updated_at=now()+interval '1 hour' WHERE subscription_id='I-OPS'`)
	status, err := PaymentTaskStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int64{}
	for _, item := range status {
		counts[item.Kind] = item.Count
		if item.Kind == "cancel_pending" && (item.OldestAt == nil || !item.OldestAt.Equal(requested)) {
			t.Fatalf("cancel age reset by update: %+v", item)
		}
	}
	if counts["conversion_ingress"] != 0 || counts["inbox"] != 0 || counts["legacy_unattributed"] != 1 || counts["cancel_pending"] != 1 || counts["order_pending"] != 1 {
		t.Fatalf("unexpected queue status: %+v", status)
	}
}

func TestSubscriptionAndCancellationResultsRemainDurable(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO users(id) VALUES(1); INSERT INTO user_subscriptions(user_id,subscription_id,status,cycle,price_cents,payment_method,paypal_next_check_at,paypal_transaction_cursor,paypal_recovery_requested,updated_at) VALUES(1,'I-RECOVER','CANCELLED','month',999,'paypal',now()-interval '1 minute',now()-interval '2 days',true,now()-interval '60 days')`)
	jobs, err := claimPayPalSubscriptions(ctx)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("explicit recovery of old agreement: %v %v", jobs, err)
	}
	if err := finishSubscriptionReconciliation(ctx, jobs[0], time.Now(), errors.New("provider timeout")); err != nil {
		t.Fatal(err)
	}
	var cursor time.Time
	var message string
	if err := pool.QueryRow(ctx, `SELECT paypal_transaction_cursor,paypal_check_error FROM user_subscriptions WHERE subscription_id='I-RECOVER'`).Scan(&cursor, &message); err != nil || !cursor.Equal(jobs[0].cursor) || message != "provider timeout" {
		t.Fatalf("failed check lost cursor/error: %s %s %v", cursor, message, err)
	}
	exec(`UPDATE user_subscriptions SET paypal_next_check_at=now()-interval '1 minute' WHERE subscription_id='I-RECOVER'`)
	newJobs, err := claimPayPalSubscriptions(ctx)
	if err != nil || len(newJobs) != 1 {
		t.Fatal(err)
	}
	if err := finishSubscriptionReconciliation(ctx, jobs[0], time.Now(), nil); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT paypal_check_error FROM user_subscriptions WHERE subscription_id='I-RECOVER'`).Scan(&message); err != nil || message != "provider timeout" {
		t.Fatalf("stale check overwrote error: %s %v", message, err)
	}
	if err := finishSubscriptionReconciliation(ctx, newJobs[0], time.Now(), nil); err != nil {
		t.Fatal(err)
	}
	var recovery bool
	if err := pool.QueryRow(ctx, `SELECT paypal_recovery_requested FROM user_subscriptions WHERE subscription_id='I-RECOVER'`).Scan(&recovery); err != nil || recovery {
		t.Fatalf("caught up recovery remained active: %t %v", recovery, err)
	}
	exec(`UPDATE user_subscriptions SET status='CANCEL_PENDING',cancel_next_attempt_at=now() WHERE subscription_id='I-RECOVER'`)
	tasks, err := claimPayPalCancellations(ctx)
	if err != nil || len(tasks) != 1 {
		t.Fatal(err)
	}
	if err := finishCancellationRetry(ctx, tasks[0], errors.New("cancel provider timeout")); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT cancel_last_error FROM user_subscriptions WHERE subscription_id='I-RECOVER'`).Scan(&message); err != nil || message != "cancel provider timeout" {
		t.Fatalf("cancel error lost: %s %v", message, err)
	}
}
