package delivery

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"conversion-backend/internal/paymentpolicy"

	"github.com/jackc/pgx/v5"
)

func testReaderPaymentPolicy(t *testing.T, ctx context.Context, base Service) {
	t.Helper()
	s := Service{Store{Pool: base.Pool, Environment: "reader-policy"}}
	pool := s.Pool
	mustExec(t, ctx, pool, `
		INSERT INTO admins(id,username,password_hash,role) VALUES ('policy-ops','policy-ops','fixture','Admin');
		INSERT INTO users(id) VALUES (81);
		INSERT INTO conversion.integrations(source,account_key,environment,contract_version,enabled,allowed_events)
		VALUES ('starnovel','reader','reader-policy','v1',true,ARRAY['CompleteRegistration','InitiateCheckout','Purchase','Subscribe']);
		INSERT INTO conversion.destinations(environment,media,pixel_id,credential_id)
		VALUES ('reader-policy','meta','151',1),('reader-policy','meta','152',1),('reader-policy','meta','153',1);
		INSERT INTO conversion.integration_destinations(integration_id,destination_id,environment,default_route)
		SELECT i.id,x.id,i.environment,x.pixel_id='152'
		FROM conversion.integrations i JOIN conversion.destinations x ON x.environment=i.environment
		WHERE i.environment='reader-policy';
	`)
	var integration, original, manual int64
	if err := pool.QueryRow(ctx, `
		SELECT i.id,min(x.id),max(x.id)
		FROM conversion.integrations i JOIN conversion.destinations x ON x.environment=i.environment
		WHERE i.environment='reader-policy' GROUP BY i.id`).Scan(&integration, &original, &manual); err != nil {
		t.Fatal(err)
	}
	actor := Operator{ID: "policy-ops", Role: "Admin"}
	send, skip := int64(1), int64(1)
	policy, err := s.PublishPaymentPolicy(ctx, actor, integration, paymentpolicy.Draft{
		App: paymentpolicy.ReaderApp, Reason: "fixture cycle",
		Operations: []paymentpolicy.Operation{{Type: "set_fallback",
			Action: &paymentpolicy.Action{Mode: paymentpolicy.Cycle, SendCount: &send, SkipCount: &skip}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	settle := func(id int64, subscription bool, promotion int) {
		t.Helper()
		kind := "single"
		var sub *string
		if subscription {
			kind = "subscription"
			value := "I-POLICY-" + strconv.FormatInt(id, 10)
			sub = &value
		}
		exec(`INSERT INTO recharge_orders(id,user_id,amount_cents,payment_method,status,fulfilled_at,order_type,
			subscription_id,fb_lead_metadata,promotion_link_id)
			VALUES($1,81,999,'paypal','Success',clock_timestamp(),$2,$3,
			    '{"pixel_id":"151","source_url":"https://example.invalid/checkout","user_agent":"fixture"}',NULLIF($4,0))`, id, kind, sub, promotion)
		exec(`SELECT record_reader_payment_conversion($1,clock_timestamp()-interval '1 second',
			'paypal_capture_create_time','reader-policy',$2,false)`, id, subscription)
	}
	consume := func(want int) {
		t.Helper()
		if got, err := s.ConsumeReader(ctx, 10); err != nil || got != want {
			t.Fatalf("normalized %d, want %d: %v", got, want, err)
		}
	}
	pair := func(want string, order int64) {
		t.Helper()
		var got string
		err := pool.QueryRow(ctx, `
			SELECT count(*)||':'||count(DISTINCT e.payment_policy_decision_id)||':'||min(d.state)||':'||max(d.state)
			FROM conversion.events e JOIN conversion.deliveries d ON d.event_id=e.id
			WHERE e.environment='reader-policy' AND e.source_order_id=$1::bigint::text`, order).Scan(&got)
		if got != want || err != nil {
			t.Fatalf("payment %d delivers %s, want %s: %v", order, got, want, err)
		}
	}

	settle(801, true, 0)
	var group sync.WaitGroup
	for range 2 {
		group.Go(func() {
			if err := s.consumeReaderMessage(ctx); err != nil {
				t.Error(err)
			}
		})
	}
	group.Wait()
	pair("4:1:pending:pending", 801)
	assertString(t, ctx, pool, "SELECT cycle_sequence::text FROM conversion.payment_policy_decisions WHERE environment='reader-policy' AND payment_key='801'", "1")

	// Freeze the first result while the token spend path is blocked.
	exec("UPDATE conversion.deliveries SET next_attempt_at=now()+interval '1 hour' WHERE environment='reader-policy'")
	exec(`UPDATE conversion.deliveries d SET next_attempt_at=now() FROM conversion.events e
		WHERE d.event_id=e.id AND e.environment='reader-policy' AND e.source_order_id='801'
		  AND e.event_name='Purchase' AND d.role='original'`)
	exec("UPDATE conversion.destinations SET credential_id=NULL WHERE id=$1", original)
	job, err := s.Claim(ctx, true, 10, time.Minute, time.Second, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Complete(ctx, job, Result{Outcome: NotSent, Block: true, Reason: "destination_or_credential_unavailable"}, time.Second); err != nil {
		t.Fatal(err)
	}
	exec("UPDATE conversion.destinations SET credential_id=1 WHERE id=$1", original)
	if _, err := s.RetryDelivery(ctx, actor, job.ID, job.Attempt, "credential recovered", false); err != nil {
		t.Fatal(err)
	}
	pair("4:1:pending:pending", 801)
	exec("UPDATE conversion.deliveries SET next_attempt_at=now()+interval '1 hour' WHERE environment='reader-policy'")

	exec("UPDATE conversion.destinations SET enabled=false WHERE id=$1", original)
	settle(802, true, 0)
	consume(2)
	pair("4:0:blocked:blocked", 802)
	var deliveryID, eventID int64
	if err := pool.QueryRow(ctx, `
		SELECT d.id,e.id FROM conversion.events e JOIN conversion.deliveries d ON d.event_id=e.id
		WHERE e.environment='reader-policy' AND e.source_order_id='802' AND e.event_name='Purchase' AND d.role='original'`).
		Scan(&deliveryID, &eventID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendTarget(ctx, actor, eventID, manual, "pending append"); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("append to pending payment: %v", err)
	}
	if _, err := s.RetryDelivery(ctx, actor, deliveryID, 0, "original disabled", false); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("retry without original authorization: %v", err)
	}
	assertString(t, ctx, pool, "SELECT last_sequence::text FROM conversion.payment_policy_counters WHERE environment='reader-policy'", "1")
	exec("UPDATE conversion.destinations SET enabled=true WHERE id=$1", original)
	if _, err := s.RetryDelivery(ctx, actor, deliveryID, 0, "original enabled", false); err != nil {
		t.Fatal(err)
	}
	pair("4:1:skipped:skipped", 802)
	assertString(t, ctx, pool, "SELECT cycle_sequence::text FROM conversion.payment_policy_decisions WHERE environment='reader-policy' AND payment_key='802'", "2")
	if _, err := s.AppendTarget(ctx, actor, eventID, manual, "skipped append"); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("append to skipped payment: %v", err)
	}
	if _, err := s.RetryDelivery(ctx, actor, deliveryID, 0, "skipped retry", false); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("retry skipped payment: %v", err)
	}

	// Even an inconsistent pending delivery must not be claimed or enter sending.
	exec("UPDATE conversion.deliveries SET state='pending',finalized_at=NULL WHERE id=$1", deliveryID)
	if _, err := s.Claim(ctx, true, 10, time.Minute, time.Second, 0); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("claim bypassed skipped decision: %v", err)
	}
	var lease Lease
	lease.ID, lease.Attempt = deliveryID, 1
	if err := pool.QueryRow(ctx, `
		UPDATE conversion.deliveries SET attempts=1,lease_token=gen_random_uuid(),lease_until=now()+interval '1 minute'
		WHERE id=$1 RETURNING lease_token::text`, deliveryID).Scan(&lease.Token); err != nil {
		t.Fatal(err)
	}
	exec("INSERT INTO conversion.delivery_attempts(delivery_id,attempt_no,lease_token) VALUES($1,1,$2::uuid)", deliveryID, lease.Token)
	payload, err := s.Payload(ctx, lease)
	if err != nil || payload.CredentialID == nil {
		t.Fatalf("payload fixture: %+v, %v", payload, err)
	}
	if err := s.StartSend(ctx, lease, *payload.CredentialID, payload.Ciphertext, *payload.KeyVersion, time.Second); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("start send bypassed skipped decision: %v", err)
	}
	exec("UPDATE conversion.deliveries SET state='skipped',finalized_at=now(),lease_token=NULL,lease_until=NULL WHERE id=$1", deliveryID)
	exec("DELETE FROM conversion.delivery_attempts WHERE delivery_id=$1", deliveryID)
	exec("DELETE FROM conversion.deliveries WHERE id=$1", deliveryID)
	skippedRepair := RepairRequest{OrderID: 802, EventName: "Purchase", Apply: true, Reason: "repair skipped target", Evidence: "verified original"}
	if result, err := s.RepairMissing(ctx, actor, skippedRepair); err != nil || result.Existing || result.Action != "restore_original" {
		t.Fatalf("skipped target repair: %+v, %v", result, err)
	}
	pair("4:1:skipped:skipped", 802)

	// Missing binding is diagnosed before sampling. Repair uses the rule
	// decision once the original is authorized again.
	exec("DELETE FROM conversion.integration_destinations WHERE integration_id=$1 AND destination_id=$2", integration, original)
	settle(803, false, 0)
	consume(1)
	pair("1:0:blocked:blocked", 803)
	exec("INSERT INTO conversion.integration_destinations VALUES($1,$2,'reader-policy',false)", integration, original)
	repair := RepairRequest{OrderID: 803, EventName: "Purchase", Apply: true, Reason: "fixture repair", Evidence: "verified original"}
	if result, err := s.RepairMissing(ctx, actor, repair); err != nil || result.Existing || result.Action != "restore_original" {
		t.Fatalf("route repair: %+v, %v", result, err)
	}
	pair("2:1:pending:pending", 803)
	assertString(t, ctx, pool, "SELECT cycle_sequence::text FROM conversion.payment_policy_decisions WHERE environment='reader-policy' AND payment_key='803'", "3")

	settle(804, false, 0)
	exec("DELETE FROM reader_conversion_outbox WHERE order_id=804")
	exec(`INSERT INTO conversion.events (integration_id,environment,source_event_type,source_event_id,
		    event_name,source_order_id,attribution_state,received_at)
		VALUES ($1,'reader-policy','Subscribe','older-subscribe','Subscribe','804','matched',now())`, integration)
	repair.OrderID = 804
	if result, err := s.RepairMissing(ctx, actor, repair); err != nil || result.Action != "restore_outbox" {
		t.Fatalf("historical sibling repair: %+v, %v", result, err)
	}
	consume(1)
	assertString(t, ctx, pool, "SELECT payment_policy_state||':'||reason FROM conversion.events WHERE environment='reader-policy' AND source_event_id='payment:804:Purchase'", "historical:historical_payment")
	assertString(t, ctx, pool, "SELECT last_sequence::text FROM conversion.payment_policy_counters WHERE environment='reader-policy'", "3")

	// All of these orders settle before coverage verification, so their
	// immutable snapshots lack the first-payment fact.
	exec(`
		INSERT INTO novels(id,title,author) VALUES(81,'Policy book','Author');
		INSERT INTO promotion_links(id,novel_id,novel_title,generated_url,created_at)
		SELECT id,81,'Policy book','https://example.invalid/'||id,now()-interval '1 day'
		FROM generate_series(81,85) id;
	`)
	exec("UPDATE conversion.destinations SET enabled=false WHERE id=$1", original)
	settle(805, false, 81)
	consume(1)
	settle(806, false, 81)
	settle(807, false, 82)
	settle(808, false, 83)
	settle(809, false, 84)
	settle(810, false, 85)
	exec("DELETE FROM reader_conversion_outbox WHERE order_id=808")
	exec(`UPDATE reader_conversion_outbox SET snapshot=jsonb_set(snapshot,'{user_id}','"82"') WHERE order_id=809`)
	exec(`INSERT INTO reader_payment_promotion_coverage
		SELECT 'reader-policy',id,now(),'fixture evidence' FROM generate_series(81,85) id`)
	for link := 81; link <= 85; link++ {
		if _, err := s.PublishPaymentPolicy(ctx, actor, integration, paymentpolicy.Draft{
			App: paymentpolicy.ReaderApp, Promotion: strconv.Itoa(link), ExpectedInherited: &policy.OwnVersion.ID,
			Range: paymentpolicy.FirstPaid, Reason: "first-only fixture",
			Operations: []paymentpolicy.Operation{{Type: "set_fallback", Action: &paymentpolicy.Action{Mode: paymentpolicy.All}}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	exec("DELETE FROM reader_payment_promotion_coverage WHERE environment='reader-policy' AND promotion_link_id=85")
	exec("UPDATE conversion.destinations SET enabled=true WHERE id=$1", original)
	if err := pool.QueryRow(ctx, `
		SELECT d.id FROM conversion.deliveries d JOIN conversion.events e ON e.id=d.event_id
		WHERE e.environment='reader-policy' AND e.source_order_id='805' AND d.role='original'`).Scan(&deliveryID); err != nil {
		t.Fatal(err)
	}
	if result, err := s.RetryDelivery(ctx, actor, deliveryID, 0, "route recovered after verifying coverage", false); err != nil || result.NextAttemptAt == nil {
		t.Fatalf("verified first-payment retry: %+v, %v", result, err)
	}
	pair("2:1:pending:pending", 805)
	repair.OrderID = 808
	if result, err := s.RepairMissing(ctx, actor, repair); err != nil || result.Action != "restore_outbox" || result.Existing {
		t.Fatalf("verified first-payment repair: %+v, %v", result, err)
	}
	assertString(t, ctx, pool, "SELECT outcome FROM conversion.payment_policy_decisions WHERE environment='reader-policy' AND payment_key='808'", "selected")
	exec("DELETE FROM reader_payment_promotion_coverage WHERE environment='reader-policy' AND promotion_link_id=83")
	consume(5)
	pair("2:1:skipped:skipped", 806)
	assertString(t, ctx, pool, "SELECT reason FROM conversion.payment_policy_decisions WHERE environment='reader-policy' AND payment_key='806'", "rule_not_first_payment")
	pair("2:1:pending:pending", 807)
	pair("2:1:pending:pending", 808)
	pair("2:0:blocked:blocked", 809)
	pair("2:0:blocked:blocked", 810)
	assertString(t, ctx, pool, `SELECT count(*)::text FROM conversion.events WHERE environment='reader-policy'
		AND source_order_id IN ('809','810') AND reason='rule_fact_missing'`, "2")
	assertString(t, ctx, pool, `SELECT count(*)::text FROM reader_conversion_repair_facts WHERE order_id BETWEEN 805 AND 810
		AND snapshot->'first_paid_on_promotion'='null'::jsonb`, "6")

	assertString(t, ctx, pool, "SELECT policy_version_id::text FROM conversion.payment_policy_decisions WHERE environment='reader-policy' AND payment_key='801'",
		strconv.FormatInt(policy.OwnVersion.ID, 10))

	exec(`INSERT INTO promotion_links(id,novel_id,novel_title,generated_url)
		VALUES(86,81,'Policy book','https://example.invalid/86');
		INSERT INTO user_subscriptions(id,user_id,subscription_id,capi_subscribe_state) VALUES(86,81,'I-ORDERED','eligible');`)
	sendTwo := int64(2)
	if _, err := s.PublishPaymentPolicy(ctx, actor, integration, paymentpolicy.Draft{
		App: paymentpolicy.ReaderApp, Promotion: "86", ExpectedInherited: &policy.OwnVersion.ID, Reason: "subscription type fixture",
		Operations: []paymentpolicy.Operation{
			{Type: "set_fallback", Kind: "subscription_initial", Action: &paymentpolicy.Action{Mode: paymentpolicy.All}},
			{Type: "set_fallback", Kind: "subscription_renewal", Action: &paymentpolicy.Action{Mode: paymentpolicy.Cycle, SendCount: &sendTwo, SkipCount: &skip}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for _, payment := range []struct {
		id  int64
		ago time.Duration
	}{
		{811, 4 * time.Minute}, {813, time.Minute}, {812, 3 * time.Minute}, {814, time.Minute}, {812, 3 * time.Minute},
	} {
		paidAt := now.Add(-payment.ago)
		exec(`INSERT INTO recharge_orders(id,user_id,external_ref_id,amount_cents,payment_method,status,paid_at,fulfilled_at,order_type,
			subscription_id,fb_lead_metadata,promotion_link_id)
			VALUES($1,81,'capture-'||$1::bigint,999,'paypal','Success',$2,clock_timestamp(),'subscription','I-ORDERED',
				'{"pixel_id":"151","source_url":"https://example.invalid/checkout","user_agent":"fixture"}',86)
			ON CONFLICT (id) DO NOTHING`, payment.id, paidAt)
		exec(`INSERT INTO third_party_payment_orders(order_id,payment_provider,external_order_id,capture_id,gross_amount,status)
			VALUES($1,'paypal','I-ORDERED','capture-'||$1::bigint,9.99,'VERIFIED')
			ON CONFLICT DO NOTHING`, payment.id)
		first := payment.id == 811
		if first {
			exec("UPDATE user_subscriptions SET capi_subscribe_state='claimed',capi_subscribe_order_id=$1 WHERE subscription_id='I-ORDERED'", payment.id)
		}
		exec(`SELECT record_reader_payment_conversion($1,$2,'paypal_subscription_ledger','reader-policy',$3,false)`, payment.id, paidAt, first)
	}
	consume(5)
	pair("4:1:pending:pending", 811)
	pair("2:1:pending:pending", 813)
	pair("2:1:pending:pending", 812)
	pair("2:1:skipped:skipped", 814)
	assertString(t, ctx, pool, `SELECT string_agg(payment_key||':'||payment_kind||':'||COALESCE(cycle_sequence::text,'all'),',' ORDER BY payment_key)
		FROM conversion.payment_policy_decisions WHERE environment='reader-policy' AND promotion_key='86'`,
		"811:subscription_initial:all,812:subscription_renewal:2,813:subscription_renewal:1,814:subscription_renewal:3")

	// Parallel normalization of the same cycle must allocate one unique,
	// gap-free sequence number per payment, regardless of sibling ordering.
	for id := int64(820); id <= 831; id++ {
		settle(id, true, 0)
	}
	for range 3 {
		group.Go(func() {
			if _, err := s.ConsumeReader(ctx, 32); err != nil {
				t.Error(err)
			}
		})
	}
	group.Wait()
	assertString(t, ctx, pool, `SELECT (count(*)=12 AND count(DISTINCT cycle_sequence)=12
        AND max(cycle_sequence)-min(cycle_sequence)=11 AND count(*) FILTER (WHERE outcome='selected')=6)::text
        FROM conversion.payment_policy_decisions WHERE environment='reader-policy' AND payment_key BETWEEN '820' AND '831'`, "true")
	for id := int64(820); id <= 831; id++ {
		var deliveries, decisions int
		err := pool.QueryRow(ctx, `SELECT count(*),count(DISTINCT e.payment_policy_decision_id)
            FROM conversion.events e JOIN conversion.deliveries d ON d.event_id=e.id
            WHERE e.environment='reader-policy' AND e.source_order_id=$1`, strconv.FormatInt(id, 10)).
			Scan(&deliveries, &decisions)
		if err != nil || deliveries != 4 || decisions != 1 {
			t.Fatalf("parallel payment %d: deliveries=%d decisions=%d error=%v", id, deliveries, decisions, err)
		}
	}
}
