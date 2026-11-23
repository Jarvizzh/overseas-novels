package delivery

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"conversion-backend/internal/paymentpolicy"
)

func testReaderSubscriptionRecovery(t *testing.T, ctx context.Context, owner, worker, api Service) {
	const environment = "reader-recovery"
	owner.Environment, worker.Environment, api.Environment = environment, environment, environment
	pool := owner.Pool
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`
		INSERT INTO users(id) VALUES (851),(852);
		INSERT INTO conversion.integrations(source,account_key,environment,contract_version,enabled,allowed_events)
		VALUES ('starnovel','reader','reader-recovery','v1',true,ARRAY['CompleteRegistration','InitiateCheckout','Purchase','Subscribe']);
		INSERT INTO conversion.destinations(environment,media,pixel_id,credential_id) VALUES ('reader-recovery','meta','541',1);
		INSERT INTO conversion.integration_destinations(integration_id,destination_id,environment,default_route)
		SELECT i.id,d.id,i.environment,false FROM conversion.integrations i
		JOIN conversion.destinations d ON d.environment=i.environment WHERE i.environment='reader-recovery';
	`)
	var integration int64
	if err := pool.QueryRow(ctx, "SELECT id FROM conversion.integrations WHERE environment=$1", environment).Scan(&integration); err != nil {
		t.Fatal(err)
	}
	actor := Operator{ID: "ops", Role: "Admin"}
	send, skip := int64(2), int64(1)
	if _, err := owner.PublishPaymentPolicy(ctx, actor, integration, paymentpolicy.Draft{
		App: paymentpolicy.ReaderApp, Reason: "verified renewal fixture",
		Operations: []paymentpolicy.Operation{{Type: "set_fallback", Kind: "subscription_renewal",
			Action: &paymentpolicy.Action{Mode: paymentpolicy.Cycle, SendCount: &send, SkipCount: &skip}}},
	}); err != nil {
		t.Fatal(err)
	}
	current := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Minute)
	const withURL = "https://example.invalid/reader"
	payment := func(id, user int, sub string, at time.Time, website string) {
		t.Helper()
		exec(`INSERT INTO recharge_orders(id,user_id,external_ref_id,amount_cents,payment_method,status,paid_at,fulfilled_at,
			order_type,subscription_id,fb_lead_metadata)
			VALUES($1,$2,'recovery-'||$1::bigint,999,'paypal','Success',$4,now(),'subscription',$3,
				jsonb_build_object('pixel_id','541','source_url',$5::text,'user_agent','fixture'));
		`, id, user, sub, at, website)
		exec(`INSERT INTO third_party_payment_orders(order_id,payment_provider,external_order_id,capture_id,gross_amount,status)
			VALUES($1,'paypal',$2,'recovery-'||$1::bigint,9.99,'VERIFIED')`, id, sub)
	}
	for id := 851; id <= 859; id++ {
		sub := "I-RECOVERY-" + strconv.Itoa(id)
		exec("INSERT INTO user_subscriptions(id,user_id,subscription_id,capi_subscribe_state) VALUES($1,851,$2,'historical')", id, sub)
		url := withURL
		if id != 853 && id != 856 {
			url = ""
		}
		payment(id, 851, sub, current, url)
		first := id == 857
		if first {
			exec("UPDATE user_subscriptions SET capi_subscribe_state='claimed',capi_subscribe_order_id=$1 WHERE id=$1", id)
		}
		exec(`SELECT record_reader_payment_conversion($1,$2,'paypal_subscription_ledger','reader-recovery',$3,false)`, id, current, first)
	}
	prior := func(id, user int) {
		payment(id+100, user, "I-RECOVERY-"+strconv.Itoa(id), current.AddDate(0, -1, 0), "")
	}
	consume := func(want int) {
		t.Helper()
		if n, err := worker.ConsumeReader(ctx, 10); err != nil || n != want {
			t.Fatalf("Worker normalized %d events, want %d: %v", n, want, err)
		}
	}
	exec("DELETE FROM reader_conversion_outbox WHERE order_id=854")
	exec(`UPDATE reader_conversion_outbox SET snapshot=snapshot-'amount_cents' WHERE order_id=858`)
	prior(851, 851) // Evidence exists by first consumption. A renewal needs no website URL.
	prior(853, 852) // A verified payment for a different customer cannot resolve the order.
	payment(1053, 851, "I-RECOVERY-853", current.AddDate(0, -1, 0), "")
	exec(`UPDATE recharge_orders SET capi_expectation='{"environment":"test"}' WHERE id=1053`)
	exec(`INSERT INTO conversion.payment_policy_decisions(environment,integration_id,app_scope,payment_key,scope_kind,
		promotion_key,original_destination_id,payment_kind,mode,outcome,reason,decided_at)
		SELECT 'reader-recovery',integration_id,'reader','856','default','',destination_id,'','all','selected','rule_no_match_send',now()
		FROM conversion.integration_destinations WHERE environment='reader-recovery'`)
	prior(856, 851) // An already frozen all-send decision keeps its original facts.
	consume(9)
	assertString(t, ctx, pool, `SELECT e.action_source||':'||e.payment_policy_state||':'||p.payment_kind
		FROM conversion.events e JOIN conversion.payment_policy_decisions p ON p.id=e.payment_policy_decision_id
		WHERE e.environment='reader-recovery' AND e.source_order_id='856'`, "website:decided:")
	assertString(t, ctx, pool, `SELECT string_agg(e.source_order_id||':'||d.state||':'||d.reason,',' ORDER BY e.source_order_id)
		FROM conversion.events e JOIN conversion.deliveries d ON e.id=d.event_id
		WHERE e.environment='reader-recovery' AND e.event_name='Purchase' AND e.source_order_id IN ('857','858')`,
		"857:failed:invalid_website_context,858:failed:missing_purchase_amount")

	var blocked, expired, skipAfterEvidence int64
	if err := pool.QueryRow(ctx, `
		SELECT max(d.id) FILTER(WHERE e.source_order_id='852'), max(d.id) FILTER(WHERE e.source_order_id='855'),
		       max(d.id) FILTER(WHERE e.source_order_id='859')
		FROM conversion.events e JOIN conversion.deliveries d ON d.event_id=e.id
		WHERE e.environment=$1 AND d.role='original' AND d.state='blocked' AND d.reason='rule_fact_missing'`, environment).
		Scan(&blocked, &expired, &skipAfterEvidence); err != nil {
		t.Fatal(err)
	}
	if result, err := api.RetryDelivery(ctx, actor, blocked, 0, "payment evidence still missing", false); err != nil ||
		result.State != "blocked" || result.Reason != "rule_fact_missing" || result.NextAttemptAt != nil {
		t.Fatalf("pending evidence outcome: %+v, %v", result, err)
	}

	prior(852, 851)
	for _, role := range []Service{worker, api} {
		assertString(t, ctx, role.Pool, `SELECT reader_verified_subscription_payment_kind('reader-recovery',852,851)`, "subscription_renewal")
		assertString(t, ctx, role.Pool, `SELECT (reader_verified_subscription_payment_kind('test',852,851) IS NULL
			AND reader_verified_subscription_payment_kind('reader-recovery',852,852) IS NULL
			AND reader_verified_subscription_payment_kind('reader-recovery',853,851) IS NULL)::text`, "true")
	}
	after := make(chan error, 2)
	var attempts sync.WaitGroup
	for range 2 {
		attempts.Go(func() {
			result, err := api.RetryDelivery(ctx, actor, blocked, 0, "verified prior PayPal payment", false)
			if err == nil && (result.State != "pending" || result.NextAttemptAt == nil) {
				err = errors.New("verified renewal did not reschedule")
			}
			after <- err
		})
	}
	attempts.Wait()
	won := 0
	for range 2 {
		if err := <-after; err == nil {
			won++
		} else if !errors.Is(err, ErrOperationConflict) {
			t.Fatal(err)
		}
	}
	if won != 1 {
		t.Fatalf("concurrent retry successes=%d", won)
	}
	exec("UPDATE conversion.deliveries SET next_attempt_at=now()+interval '1 hour' WHERE environment=$1 AND id<>$2 AND state='pending'",
		environment, blocked)
	job, err := worker.Claim(ctx, true, 5, time.Minute, time.Second, 0)
	if err != nil || job.ID != blocked {
		t.Fatalf("claim recovered renewal: %+v, %v", job, err)
	}
	payload, err := worker.Payload(ctx, job)
	if err != nil || payload.ActionSource == nil || *payload.ActionSource != "system_generated" || payload.SourceURL != nil ||
		payload.CredentialID == nil || payload.KeyVersion == nil {
		t.Fatalf("recovered renewal payload: %+v, %v", payload, err)
	}
	if err := ValidatePayload(payload); err != nil {
		t.Fatal(err)
	}
	if err := worker.StartSend(ctx, job, *payload.CredentialID, payload.Ciphertext, *payload.KeyVersion, time.Second); err != nil {
		t.Fatal(err)
	}
	httpStatus, received := 200, 1
	if err := worker.Complete(ctx, job, Result{Outcome: Accepted, HTTPStatus: &httpStatus, EventsReceived: &received}, time.Second); err != nil {
		t.Fatal(err)
	}

	prior(859, 851)
	if result, err := api.RetryDelivery(ctx, actor, skipAfterEvidence, 0, "verified renewal matched cycle skip", false); err != nil ||
		result.State != "skipped" || result.NextAttemptAt != nil {
		t.Fatalf("resolved renewal skip: %+v, %v", result, err)
	}
	assertString(t, ctx, pool, `SELECT state||':'||attempts FROM conversion.deliveries
		WHERE event_id=(SELECT id FROM conversion.events WHERE environment='reader-recovery' AND source_order_id='859')`, "skipped:0")

	repair := RepairRequest{OrderID: 854, EventName: "Purchase", Reason: "restore verified renewal", Evidence: "verified prior capture"}
	if result, err := api.RepairMissing(ctx, actor, repair); err != nil || result.Action != "restore_outbox" || !result.DryRun {
		t.Fatalf("preview renewal repair: %+v, %v", result, err)
	}
	prior(854, 851)
	repair.Apply = true
	if result, err := api.RepairMissing(ctx, actor, repair); err != nil || result.Action != "restore_outbox" {
		t.Fatalf("renewal repair: %+v, %v", result, err)
	}
	consume(1)
	consume(0)
	exec("UPDATE reader_conversion_outbox SET consumed_at=NULL WHERE order_id IN (851,852)")
	for range 2 {
		attempts.Go(func() {
			if _, err := worker.ConsumeReader(ctx, 1); err != nil {
				t.Error(err)
			}
		})
	}
	attempts.Wait()
	consume(0)

	exec("UPDATE conversion.deliveries SET deadline_at=now()-interval '1 minute' WHERE id=$1", expired)
	if _, err := worker.Sweep(ctx, 10, time.Second); err != nil {
		t.Fatal(err)
	}
	assertString(t, ctx, pool, `SELECT state||':'||reason FROM conversion.deliveries WHERE finalized_at IS NOT NULL
		AND event_id=(SELECT id FROM conversion.events WHERE environment='reader-recovery' AND source_order_id='855')`, "failed:rule_fact_missing")
	prior(855, 851)
	if _, err := api.RetryDelivery(ctx, actor, expired, 0, "verified but expired", false); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("expired retry: %v", err)
	}
	assertString(t, ctx, pool, `SELECT string_agg(payment_key||':'||payment_kind||':'||cycle_sequence||':'||outcome,',' ORDER BY payment_key)
		FROM conversion.payment_policy_decisions WHERE environment='reader-recovery' AND cycle_sequence IS NOT NULL`,
		"851:subscription_renewal:1:selected,852:subscription_renewal:2:selected,854:subscription_renewal:4:selected,859:subscription_renewal:3:skipped")
	assertString(t, ctx, pool, `SELECT count(*)::text
		FROM conversion.events e JOIN reader_conversion_repair_facts f ON f.business_key=e.source_event_id
		JOIN conversion.deliveries d ON d.event_id=e.id AND d.role='original'
		WHERE e.environment='reader-recovery' AND e.action_source='system_generated' AND e.source_url IS NULL
		    AND e.occurred_at=f.occurred_at AND e.received_at=f.created_at
		    AND d.deadline_at=f.created_at+interval '24 hours'
		    AND f.snapshot->>'action_source'='website' AND f.snapshot->'payment_kind'='null'::jsonb`, "4")
}
