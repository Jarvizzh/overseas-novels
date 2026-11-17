package wallet

import (
	"context"
	"maps"
	"slices"
	"testing"
	"time"

	"reader-backend/internal/testdb"
)

func TestSubscriptionClaimsUseStableObservationWindow(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		INSERT INTO users(id) VALUES(1);
		INSERT INTO user_subscriptions(user_id, subscription_id, status, paypal_status_changed_at,
		    updated_at, cancelled_at, paypal_next_check_at, paypal_recovery_requested)
		SELECT 1, subscription, status, now()-age, now(), now()-age, now()-due, recovery
		FROM (VALUES
		    ('I-OLD-CANCELLED','CANCELLED',interval '31 days',interval '90 days',false),
		    ('I-OLD-EXPIRED','EXPIRED',interval '31 days',interval '80 days',false),
		    ('I-RECENT','CANCELLED',interval '29 days',interval '10 hours',false),
		    ('I-RECOVERY','EXPIRED',interval '31 days',interval '9 hours',true),
		    ('I-ACTIVE','ACTIVE',interval '90 days',interval '8 hours',false),
		    ('I-WAITING','ACTIVE',interval '1 day',interval '-1 hour',false),
		    ('I-MOCK-HIDDEN','ACTIVE',interval '1 day',interval '7 hours',false)
		) AS fixture(subscription,status,age,due,recovery)`); err != nil {
		t.Fatal(err)
	}
	repo := NewDBRepository()
	for id, status := range map[string]string{"I-OLD-CANCELLED": "CANCELLED", "I-OLD-EXPIRED": "EXPIRED"} {
		if err := repo.UpdateUserSubscriptionStatus(ctx, id, status); err != nil {
			t.Fatal(err)
		}
	}
	var stale int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM user_subscriptions
		WHERE subscription_id IN ('I-OLD-CANCELLED','I-OLD-EXPIRED') AND paypal_status_changed_at<now()-interval '30 days'`).Scan(&stale); err != nil || stale != 2 {
		t.Fatalf("same-status poll changed the observation window: %d, %v", stale, err)
	}
	if err := repo.UpdateUserSubscriptionStatus(ctx, "I-WAITING", "EXPIRED"); err != nil {
		t.Fatal(err)
	}
	var transitioned bool
	if err := pool.QueryRow(ctx, `SELECT paypal_status_changed_at>now()-interval '1 minute'
		FROM user_subscriptions WHERE subscription_id='I-WAITING'`).Scan(&transitioned); err != nil || !transitioned {
		t.Fatalf("state transition did not record its time: %t, %v", transitioned, err)
	}
	status, err := PaymentTaskStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var dueCount int64 = -1
	for _, item := range status {
		if item.Kind == "subscription_due" {
			dueCount = item.Count
		}
	}
	if dueCount != 3 {
		t.Fatalf("metrics and claim eligibility diverged: %d", dueCount)
	}

	jobResults := make(chan []subscriptionJob, 2)
	errors := make(chan error, 2)
	for range 2 {
		go func() {
			jobs, err := claimPayPalSubscriptions(ctx)
			jobResults <- jobs
			errors <- err
		}()
	}
	claimed := make(map[string]subscriptionJob)
	for range 2 {
		for _, job := range <-jobResults {
			if _, exists := claimed[job.id]; exists {
				t.Fatalf("duplicate subscription lease: %s", job.id)
			}
			claimed[job.id] = job
		}
		if err := <-errors; err != nil {
			t.Fatal(err)
		}
	}
	if ids := slices.Sorted(maps.Keys(claimed)); !slices.Equal(ids, []string{"I-ACTIVE", "I-RECENT", "I-RECOVERY"}) {
		t.Fatalf("claimed subscriptions: %v", ids)
	}
	if err := finishSubscriptionReconciliation(ctx, claimed["I-RECOVERY"], time.Now(), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE user_subscriptions SET paypal_next_check_at=now()-interval '1 second' WHERE subscription_id='I-RECOVERY'`); err != nil {
		t.Fatal(err)
	}
	if jobs, err := claimPayPalSubscriptions(ctx); err != nil || len(jobs) != 0 {
		t.Fatalf("finished recovery is still eligible: %v, %v", jobs, err)
	}
}

func TestPaymentMetricsTrackUnattributedExactlyAndCollectOnce(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	exec := func(sql string) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO users(id) VALUES(1);
		INSERT INTO recharge_orders(id,user_id,amount_cents,payment_method) VALUES(1,1,499,'paypal');
		INSERT INTO payment_event_outbox(event_id, order_id, event_name, payload, state, attempts, created_at)
		VALUES ('oldest',1,'Purchase','{}','unattributed',0,'2024-01-01Z'),
		       ('max-attempts',1,'Subscribe','{}','unattributed',4,'2024-02-01Z'),
		       ('other',1,'Purchase','{}','pending',2,'2024-03-01Z')`)
	assertMetrics := func(count int64, attempts int, oldest string) {
		t.Helper()
		status, err := PaymentTaskStatus(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range status {
			if item.Kind == "unattributed" {
				var date string
				if item.OldestAt != nil {
					date = item.OldestAt.UTC().Format(time.DateOnly)
				}
				if item.Count != count || item.MaxAttempts != attempts || date != oldest {
					t.Fatalf("unattributed metrics: %+v, date=%s", item, date)
				}
				return
			}
		}
		t.Fatal("missing unattributed metrics")
	}
	assertMetrics(2, 4, "2024-01-01")
	exec(`UPDATE payment_event_outbox SET state='unattributed' WHERE event_id='other';
		UPDATE payment_event_outbox SET attempts=1 WHERE event_id='max-attempts';`)
	assertMetrics(3, 2, "2024-01-01")
	exec(`DELETE FROM payment_event_outbox WHERE event_id='oldest'`)
	assertMetrics(2, 2, "2024-02-01")
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM payment_event_outbox`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	assertMetrics(2, 2, "2024-02-01")
	exec(`UPDATE recharge_orders SET status='Refunded' WHERE id=1`)
	assertMetrics(0, 0, "")

	type claimResult struct {
		claimed bool
		err     error
	}
	results := make(chan claimResult, 8)
	for range cap(results) {
		go func() {
			claimed, err := claimPaymentTaskMetrics(ctx)
			results <- claimResult{claimed, err}
		}()
	}
	winners := 0
	for range cap(results) {
		result := <-results
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.claimed {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("collected %d samples for one period", winners)
	}
	exec(`UPDATE payment_task_metrics_schedule SET next_collection_at=now()-interval '1 second'`)
	if claimed, err := claimPaymentTaskMetrics(ctx); err != nil || !claimed {
		t.Fatalf("failed to reclaim collection after expiry: %t %v", claimed, err)
	}
}
