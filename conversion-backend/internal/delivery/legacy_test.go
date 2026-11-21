package delivery

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"conversion-backend/internal/credentials"
)

func testLegacyCutover(t *testing.T, ctx context.Context, s Service) {
	t.Helper()
	pool := s.Pool
	mustExec(t, ctx, pool, `
	    INSERT INTO users(id) VALUES (10);
		INSERT INTO fb_pixels(id,name,pixel_id,access_token)
		VALUES (20,'old Pixel','789','old-fixture-token'),(21,'invalid','not-numeric','invalid-fixture-token');
		INSERT INTO user_subscriptions(id,user_id,subscription_id,last_payment_time)
		VALUES (1,10,'I-CLAIM',NULL),(2,10,'I-HIST',now()),(3,10,'I-UNKNOWN',NULL);
		INSERT INTO recharge_orders(id,user_id,payment_method,status,amount_cents,order_type,subscription_id,fulfilled_at)
		VALUES (100,10,'paypal','Success',999,'subscription','I-CLAIM',now()),
		       (101,10,'paypal','Success',999,'single',NULL,now()),
		       (102,10,'paypal','Success',999,'single',NULL,now()),
		       (103,10,'paypal','Success',999,'single',NULL,now()),
		       (104,10,'paypal','Success',999,'single',NULL,now()),
		       (105,10,'paypal','Success',999,'single',NULL,now());
		INSERT INTO payment_event_outbox(event_id,order_id,event_name,payload,state,attempts,delivered_at)
		SELECT event_id,order_id,name,jsonb_build_object('event_id',event_id,'event_name',name,
		    'event_time',extract(epoch FROM CASE WHEN order_id=105 THEN now()-interval '8 days' ELSE now() END)::bigint,
		    'user_id','10','amount_cents',999,'currency','USD','attribution',jsonb_build_object('pixel_id',pixel)),state,attempts,
		    CASE WHEN state='delivered' THEN now()-interval '1 minute' END
		FROM (VALUES
		  ('subscription:I-CLAIM:Subscribe',100,'Subscribe','delivered',1,'789'),
		  ('payment:101:Purchase',101,'Purchase','pending',3,'789'),
		  ('payment:102:Purchase',102,'Purchase','pending',0,'789'),
		  ('payment:103:Purchase',103,'Purchase','pending',0,'789'),
		  ('payment:104:Purchase',104,'Purchase','unattributed',0,''),
		  ('payment:105:Purchase',105,'Purchase','pending',0,'789')
		) f(event_id,order_id,name,state,attempts,pixel)`)
	keys, err := credentials.Parse(`{"fixture":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="}`)
	if err != nil {
		t.Fatal(err)
	}
	for index, want := range []int{1, 0} {
		batch, err := s.ImportLegacyPixels(ctx, 0, 20, "fixture", keys.Encrypt)
		if err != nil || batch.Changed != want || batch.Skipped != 1 {
			t.Fatalf("Pixel import %d: %+v, %v", index, batch, err)
		}
	}
	var encrypted []byte
	if err := pool.QueryRow(ctx, `
		SELECT ciphertext FROM conversion.credentials c JOIN conversion.destinations d ON d.credential_id=c.id
		WHERE d.legacy_fb_pixel_id=20`).Scan(&encrypted); err != nil {
		t.Fatal(err)
	}
	plain, err := keys.Decrypt("fixture", encrypted)
	if err != nil || plain != "old-fixture-token" || bytes.Contains(encrypted, []byte(plain)) {
		t.Fatal("historical Pixel credential did not round-trip as ciphertext")
	}

	subscriptions, err := s.BackfillLegacySubscriptions(ctx, 0, 2)
	if err != nil || subscriptions.Changed != 2 || subscriptions.After != 2 {
		t.Fatalf("subscription backfill: %+v, %v", subscriptions, err)
	}
	subscriptions, err = s.BackfillLegacySubscriptions(ctx, subscriptions.After, 2)
	if err != nil || subscriptions.Changed != 0 || subscriptions.Skipped != 1 {
		t.Fatalf("subscription without evidence: %+v, %v", subscriptions, err)
	}
	assertString(t, ctx, pool, "SELECT capi_subscribe_state||':'||capi_subscribe_order_id FROM user_subscriptions WHERE id=1", "claimed:100")
	assertString(t, ctx, pool, "SELECT capi_subscribe_state FROM user_subscriptions WHERE id=2", "historical")
	assertString(t, ctx, pool, "SELECT capi_subscribe_state FROM user_subscriptions WHERE id=3", "unknown")

	evidence := map[string]LegacyEvidence{
		"payment:102:Purchase": {Environment: "test", Reference: "fixture registry"},
		"payment:105:Purchase": {Environment: "test", Reference: "fixture registry"},
	}
	mustExec(t, ctx, pool, "UPDATE payment_event_outbox SET lease_until=now()+interval '1 minute' WHERE event_id='payment:102:Purchase'")
	if _, err := s.ImportLegacyEvents(ctx, "", 3, evidence); err == nil {
		t.Fatal("legacy import proceeded while old lease was active")
	}
	assertString(t, ctx, pool, "SELECT count(*)::text FROM conversion.legacy_outbox_imports", "0")
	mustExec(t, ctx, pool, "UPDATE payment_event_outbox SET lease_until=NULL WHERE event_id='payment:102:Purchase'")

	first, err := s.ImportLegacyEvents(ctx, "", 3, evidence)
	if err != nil || first.Imported != 3 || first.After != "payment:103:Purchase" {
		t.Fatalf("first batch: %+v, %v", first, err)
	}
	second, err := s.ImportLegacyEvents(ctx, first.After, 10, evidence)
	if err != nil || second.Imported != 3 || second.Resolution["succeeded"] != 1 || second.Resolution["failed"] != 1 ||
		second.Resolution["blocked_without_target"] != 1 {
		t.Fatalf("second batch: %+v, %v", second, err)
	}
	replayed, err := s.ImportLegacyEvents(ctx, "", 20, evidence)
	if err != nil || replayed.Imported != 0 {
		t.Fatalf("replay: %+v, %v", replayed, err)
	}
	assertString(t, ctx, pool, "SELECT state||':'||attempts||':'||unknown_attempt_no FROM conversion.deliveries WHERE meta_event_id='payment:101:Purchase'", "blocked:3:3")
	assertString(t, ctx, pool, "SELECT state FROM conversion.deliveries WHERE meta_event_id='payment:102:Purchase'", "pending")
	assertString(t, ctx, pool, "SELECT state||':'||reason FROM conversion.deliveries WHERE meta_event_id='payment:103:Purchase'", "blocked:legacy_environment_unverified")
	assertString(t, ctx, pool, "SELECT state||':'||reason FROM conversion.deliveries WHERE meta_event_id='payment:105:Purchase'", "failed:deadline_exhausted")
	assertString(t, ctx, pool, `
		SELECT (d.finalized_at=m.legacy_delivered_at AND to_timestamp((o.payload->>'event_time')::bigint)=e.occurred_at
		        AND d.meta_event_id=m.legacy_event_id)::text
		FROM conversion.deliveries d JOIN conversion.events e ON e.id=d.event_id
		JOIN conversion.legacy_outbox_imports m ON m.event_id=e.id JOIN payment_event_outbox o ON o.event_id=m.legacy_event_id
		WHERE m.legacy_event_id='subscription:I-CLAIM:Subscribe'`, "true")
}

func testLegacyReview(t *testing.T, ctx context.Context, s Service) {
	t.Helper()
	for _, tt := range []struct{ key, state string }{
		{"payment:101:Purchase", "outcome_unknown"}, {"payment:103:Purchase", "pending"},
	} {
		for n := range 2 {
			out, err := s.ReviewLegacyEnvironment(ctx, Operator{ID: "ops"}, tt.key, "verified release evidence", "review prior deployment")
			if err != nil || out.Existing != (n == 1) {
				t.Fatalf("review %s replay=%d: %+v %v", tt.key, n, out, err)
			}
		}
		var actual string
		if err := s.Pool.QueryRow(ctx, `SELECT state FROM conversion.deliveries WHERE meta_event_id=$1`, tt.key).Scan(&actual); err != nil || actual != tt.state {
			t.Fatalf("review %s got %s want %s: %v", tt.key, actual, tt.state, err)
		}
	}
	assertString(t, ctx, s.Pool, "SELECT count(*)::text FROM conversion.operation_audit WHERE action='review_legacy_environment'", "2")
	if _, err := s.ReviewLegacyEnvironment(ctx, Operator{ID: "ops"}, "payment:103:Purchase", "different reference", "review prior deployment"); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("evidence overwritten: %v", err)
	}
}
