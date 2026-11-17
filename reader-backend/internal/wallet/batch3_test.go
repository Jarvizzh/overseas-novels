package wallet

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"reader-backend/internal/model"
	"reader-backend/internal/payment"
	"reader-backend/internal/testdb"
	"reader-backend/internal/tracking"
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
	tx, err := p.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE recharge_orders SET status='Success',paid_at=now(),fulfilled_at=now(),payment_events_enabled=true WHERE id=100`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := p.QueryRow(ctx, `SELECT count(*) FROM payment_event_outbox`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rolled back event persisted: %d %v", count, err)
	}
	exec(`UPDATE recharge_orders SET status='Success',paid_at=now(),fulfilled_at=now(),payment_events_enabled=true WHERE id=100`)
	exec(`UPDATE recharge_orders SET fb_lead_metadata='{"pixel_id":"different","fbc":"changed"}' WHERE id=100`)
	if err := p.QueryRow(ctx, `SELECT count(*) FROM payment_event_outbox`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("duplicate event: %d %v", count, err)
	}
	original := http.DefaultTransport
	defer func() { http.DefaultTransport = original }()
	var first tracking.FacebookEvent
	requests := 0
	http.DefaultTransport = reviewTransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host != "graph.facebook.com" {
			t.Fatalf("unexpected external request: %s", req.URL.Host)
		}
		var body tracking.FacebookCAPIRequest
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body.Data) != 1 || body.Data[0].UserData.Fbc != "original-click" {
			t.Fatal("lost immutable attribution")
		}
		requests++
		if requests == 1 {
			first = body.Data[0]
		} else if body.Data[0].EventID != first.EventID || body.Data[0].EventTime != first.EventTime {
			t.Fatal("retry changed event identity")
		}
		status, payload := 503, `{}`
		if requests > 1 {
			status, payload = 200, `{"events_received":1}`
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(payload)), Header: make(http.Header)}, nil
	})
	svc := &service{}
	svc.reconcilePaymentEvents(ctx)
	var delivered bool
	if err := p.QueryRow(ctx, `SELECT delivered_at IS NOT NULL FROM payment_event_outbox`).Scan(&delivered); err != nil || delivered {
		t.Fatal("failed delivery acknowledged", err)
	}
	exec(`UPDATE payment_event_outbox SET next_attempt_at=now()`)
	svc.reconcilePaymentEvents(ctx)
	if err := p.QueryRow(ctx, `SELECT delivered_at IS NOT NULL FROM payment_event_outbox`).Scan(&delivered); err != nil || !delivered || requests != 2 {
		t.Fatal("retry failed", err)
	}
	svc.reconcilePaymentEvents(ctx)
	if requests != 2 {
		t.Fatal("delivered event reclaimed")
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
	if _, err := r.RecordSubscriptionRenewalOrderTx(ctx, sub, "snapshot-sale", 999, "USD", at, at.AddDate(0, 1, 0), "ACTIVE", &model.ThirdPartyPaymentOrder{Status: "COMPLETED", RawPayload: "{}"}); err != nil {
		t.Fatal(err)
	}
	var click, pixel, campaign string
	if err := p.QueryRow(ctx, `SELECT fb_lead_metadata->>'fbc',fb_lead_metadata->>'pixel_id',utm_campaign FROM recharge_orders WHERE external_ref_id='snapshot-sale'`).Scan(&click, &pixel, &campaign); err != nil {
		t.Fatal(err)
	}
	if click != "original-click" || pixel != "original-pixel" || campaign != "original-campaign" {
		t.Fatalf("snapshot changed: %s %s %s", click, pixel, campaign)
	}
	var count int
	if err := p.QueryRow(ctx, `SELECT count(*) FROM payment_event_outbox`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("purchase and subscribe: %d %v", count, err)
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
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := p.QueryRow(ctx, `SELECT count(*) FROM payment_event_outbox`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("unfulfilled refund emitted payment: %d %v", count, err)
	}
}

func TestOutboxLeaseFencesCrashedWorkers(t *testing.T) {
	p := testdb.Open(t)
	ctx := context.Background()
	if _, err := p.Exec(ctx, `INSERT INTO users(id) VALUES(1);INSERT INTO recharge_orders(id,user_id,amount_cents,payment_method,status,fulfilled_at,paid_at,fb_lead_metadata,payment_events_enabled) VALUES(100,1,999,'paypal','Success',now(),now(),'{"pixel_id":"test"}',true)`); err != nil {
		t.Fatal(err)
	}
	first, err := claimPaymentEvents(ctx)
	if err != nil || len(first) != 1 {
		t.Fatal("first claim", err)
	}
	if next, err := claimPaymentEvents(ctx); err != nil || len(next) != 0 {
		t.Fatal("live lease claimed twice", err)
	}
	if _, err := p.Exec(ctx, `UPDATE payment_event_outbox SET lease_until=now()-interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	second, err := claimPaymentEvents(ctx)
	if err != nil || len(second) != 1 || first[0].lease == second[0].lease {
		t.Fatal("crashed lease not recovered", err)
	}
	if err := finishPaymentEvent(ctx, first[0], nil); err != nil {
		t.Fatal(err)
	}
	var delivered bool
	if err := p.QueryRow(ctx, `SELECT delivered_at IS NOT NULL FROM payment_event_outbox`).Scan(&delivered); err != nil || delivered {
		t.Fatal("stale worker acknowledged current lease", err)
	}
	if err := finishPaymentEvent(ctx, second[0], nil); err != nil {
		t.Fatal(err)
	}
	if err := p.QueryRow(ctx, `SELECT delivered_at IS NOT NULL FROM payment_event_outbox`).Scan(&delivered); err != nil || !delivered {
		t.Fatal("current worker could not acknowledge", err)
	}
}
