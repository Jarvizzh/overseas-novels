package wallet

import (
	"context"
	"testing"
	"time"

	"reader-backend/internal/conversion"
	"reader-backend/internal/model"
	"reader-backend/internal/payment"
	"reader-backend/internal/testdb"
)

func TestTransactionalPaymentOutbox(t *testing.T) {
	p := testdb.Open(t)
	ctx := context.Background()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := p.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO users(id) VALUES(1);INSERT INTO fb_pixels(id,name,pixel_id,access_token) VALUES(1,'Test','pixel-test','test-secret');
 INSERT INTO recharge_orders(id,user_id,amount_cents,payment_method,status,fb_lead_metadata) VALUES(100,1,999,'paypal','Pending','{"pixel_id":"pixel-test","fbc":"original-click","source_url":"https://example.invalid"}')`)
	captureTime := time.Date(2026, 11, 19, 3, 4, 5, 0, time.UTC)
	for _, commit := range []bool{false, true} {
		tx, err := p.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, `UPDATE recharge_orders SET status='Success',paid_at=now(),fulfilled_at=now() WHERE id=100`); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatal(err)
		}
		if err := conversion.RecordPaymentTx(ctx, tx, 100, captureTime, "paypal_capture_create_time", false, false); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatal(err)
		}
		if !commit {
			if err := tx.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
			var count int
			if err := p.QueryRow(ctx, `SELECT count(*) FROM reader_conversion_outbox`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("rolled back event persisted: %d %v", count, err)
			}
			continue
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE recharge_orders SET fb_lead_metadata='{"pixel_id":"different","fbc":"changed"}' WHERE id=100`)
	exec(`SELECT record_reader_payment_conversion(100,now(),'replayed','production',false,false)`)
	var key, pixel string
	var occurredAt time.Time
	if err := p.QueryRow(ctx, `SELECT business_key,snapshot->'attribution'->>'pixel_id',occurred_at FROM reader_conversion_outbox`).
		Scan(&key, &pixel, &occurredAt); err != nil || key != "payment:100:Purchase" || pixel != "pixel-test" || !occurredAt.Equal(captureTime) {
		t.Fatalf("event snapshot = %s %s %v, %v", key, pixel, occurredAt, err)
	}
	var count int
	if err := p.QueryRow(ctx, `SELECT count(*) FROM reader_conversion_outbox`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("duplicate event: %d %v", count, err)
	}
}

func TestSubscriptionKeepsCheckoutAttribution(t *testing.T) {
	p := testdb.Open(t)
	ctx := context.Background()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := p.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO novels(id,title,author) VALUES(1,'Book','Author');INSERT INTO fb_pixels(id,name,pixel_id,access_token) VALUES(1,'Original','original-pixel','secret');
 INSERT INTO promotion_links(id,novel_id,novel_title,generated_url,fb_pixel_id) VALUES(1,1,'Book','https://example.invalid',1);
 INSERT INTO users(id,promotion_link_id,utm_source,utm_campaign) VALUES(1,1,'original-source','original-campaign')`)
	r := &dbRepository{}
	intent, err := r.reserveSubscriptionIntent(ctx, subscriptionIntent{userID: 1, slotID: 1, templateID: 1, priceCents: 999, cycle: "month", attribution: checkoutAttribution{FBP: "original-browser", FBC: "original-click", IP: "192.0.2.1", UserAgent: "test-agent", SourceURL: "https://example.invalid"}})
	if err != nil {
		t.Fatal(err)
	}
	intent.planID = "P-SNAPSHOT"
	exec(`UPDATE users SET utm_campaign='changed' WHERE id=1;UPDATE fb_pixels SET pixel_id='changed-pixel' WHERE id=1`)
	if err := r.finishSubscriptionIntent(ctx, intent, &payment.SubscriptionResult{SubscriptionID: "I-SNAPSHOT", ApproveURL: "https://example.invalid/approve"}); err != nil {
		t.Fatal(err)
	}
	sub, err := r.GetSubscriptionByID(ctx, "I-SNAPSHOT")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	if err := r.finishSubscriptionIntent(ctx, intent, &payment.SubscriptionResult{SubscriptionID: "I-SNAPSHOT", ApproveURL: "https://example.invalid/approve"}); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	for _, ref := range []string{"snapshot-sale", "snapshot-renewal"} {
		go func() {
			_, err := r.RecordSubscriptionRenewalOrderTx(ctx, sub, ref, 999, "USD", at, at.AddDate(0, 1, 0), "ACTIVE",
				&model.ThirdPartyPaymentOrder{Status: "COMPLETED", RawPayload: "{}"})
			results <- err
		}()
	}
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}

	var click, pixel, campaign string
	if err := p.QueryRow(ctx, `SELECT fb_lead_metadata->>'fbc',fb_lead_metadata->>'pixel_id',utm_campaign FROM recharge_orders WHERE external_ref_id='snapshot-sale'`).Scan(&click, &pixel, &campaign); err != nil {
		t.Fatal(err)
	}
	if click != "original-click" || pixel != "original-pixel" || campaign != "original-campaign" {
		t.Fatalf("snapshot changed: %s %s %s", click, pixel, campaign)
	}
	var purchases, subscribes, checkouts int
	var owner int64
	if err := p.QueryRow(ctx, `SELECT count(*) FILTER (WHERE event_name='Purchase'), count(*) FILTER (WHERE event_name='Subscribe'),
		count(*) FILTER (WHERE event_name='InitiateCheckout'), max(order_id) FILTER (WHERE event_name='Subscribe')
		FROM reader_conversion_outbox`).Scan(&purchases, &subscribes, &checkouts, &owner); err != nil ||
		purchases != 2 || subscribes != 1 || checkouts != 1 {
		t.Fatalf("checkout/purchase/subscribe counts=%d/%d/%d, err=%v", checkouts, purchases, subscribes, err)
	}
	var claimedOrder int64
	if err := p.QueryRow(ctx, `SELECT capi_subscribe_order_id FROM user_subscriptions
		WHERE subscription_id='I-SNAPSHOT' AND capi_subscribe_state='claimed'`).Scan(&claimedOrder); err != nil || claimedOrder != owner {
		t.Fatalf("subscribe owner=%d, want %d, err=%v", claimedOrder, owner, err)
	}

}

func TestProviderPlansRequireBoundIdentity(t *testing.T) {
	p := testdb.Open(t)
	ctx := context.Background()
	if _, err := p.Exec(ctx, `INSERT INTO payment_provider_plans(provider,slot_id,cycle,price_cents,currency,external_plan_id) VALUES('paypal',1,'month',999,'USD','P-LEGACY');SELECT assert_paypal_identity('merchant-test','sandbox')`); err != nil {
		t.Fatal(err)
	}
	r := &dbRepository{}
	plan, err := r.GetProviderPlan(ctx, "paypal", 1)
	if err != nil || plan != nil {
		t.Fatalf("unverified legacy plan reused: %+v %v", plan, err)
	}
	if err := r.SaveProviderPlan(ctx, &model.PaymentProviderPlan{Provider: "paypal", SlotID: 1, Cycle: "month", PriceCents: 999, Currency: "USD", ExternalPlanID: "P-BOUND"}); err != nil {
		t.Fatal(err)
	}
	plan, err = r.GetProviderPlan(ctx, "paypal", 1)
	if err != nil || plan == nil || plan.ExternalPlanID != "P-BOUND" {
		t.Fatal("bound plan unavailable", err)
	}
}

func TestRefundBeforeFulfillmentLeavesNoPaymentEvent(t *testing.T) {
	p := testdb.Open(t)
	ctx := context.Background()
	tx, err := p.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `INSERT INTO users(id) VALUES(1);INSERT INTO recharge_orders(id,user_id,amount_cents,payment_method,status,fulfilled_at,paid_at,payment_events_enabled) VALUES(100,1,999,'paypal','Success',now(),now(),true);UPDATE recharge_orders SET status='Refunded',fulfilled_at=NULL WHERE id=100`); err != nil {
		t.Fatal(err)
	}
	if err := conversion.RecordPaymentTx(ctx, tx, 100, time.Time{}, "missing", false, false); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := p.QueryRow(ctx, `SELECT count(*) FROM reader_conversion_outbox WHERE event_name IN ('Purchase','Subscribe')`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("unfulfilled refund emitted payment: %d %v", count, err)
	}
}
