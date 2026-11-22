package application

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reader-backend/internal/model"
	"reader-backend/internal/novel"
	"reader-backend/internal/payment/paypal"
	"reader-backend/internal/payment/paypal/credentialstore"
	"reader-backend/internal/testdb"
	"reader-backend/internal/wallet"
	"strings"
	"testing"
	"time"
)

type reviewTransport func(*http.Request) (*http.Response, error)

func (f reviewTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestSubscriptionRecoveryAndManagement(t *testing.T) {
	t.Setenv("CONVERSION_ENVIRONMENT", "sandbox")
	p := testdb.Open(t)
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := p.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO users(id,utm_source,utm_campaign) VALUES(1,'s','c'),(2,'',''); INSERT INTO novels(id,title,author) VALUES(1,'Book','Author'); INSERT INTO recharge_templates(id,name,is_default) VALUES(1,'Default',true); INSERT INTO recharge_slots(id,template_id,slot_index,type,price,price_cents) VALUES(1,1,1,'vip','$9.99',999)`)
	exec(`INSERT INTO user_subscriptions(user_id,subscription_id,plan_id,slot_id,template_id,status,cycle,price_cents,currency,payment_method) VALUES(1,'I-RECOVER','P-REVIEW',1,1,'ACTIVE','month',999,'USD','paypal')`)
	exec(`INSERT INTO system_configs(key,value) VALUES('paypal_client_id','review-client'),('paypal_secret_key','review-secret'),('paypal_mode','sandbox')`)
	exec(`SELECT assert_paypal_identity('review-client','sandbox');
		UPDATE user_subscriptions SET checkout_account_id=(SELECT id FROM payment_channel_accounts
		WHERE provider='paypal' AND account_key='review-client' AND environment='sandbox') WHERE subscription_id='I-RECOVER'`)
	repo := NewDBRepository(wallet.NewDBRepository())
	svc := &service{repo: repo, readingAccess: novel.NewDBRepository(), paypalClient: paypal.NewClient(credentialstore.Load)}
	paid := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	end := paid.AddDate(0, 1, 0)
	sub, err := repo.GetSubscriptionByID(ctx, "I-RECOVER")
	if err != nil {
		t.Fatal(err)
	}
	tp := &model.ThirdPartyPaymentOrder{Status: "COMPLETED", RawPayload: "{}"}
	if _, err := repo.RecordSubscriptionRenewalOrderTx(ctx, sub, "sale-review", 999, "USD", paid, end, "ACTIVE", tp); err != nil {
		t.Fatal(err)
	}

	original := http.DefaultTransport
	defer func() { http.DefaultTransport = original }()
	http.DefaultTransport = reviewTransport(func(req *http.Request) (*http.Response, error) {
		var body string
		switch {
		case req.URL.Path == "/v1/oauth2/token":
			body = `{"access_token":"review-only-token"}`
		case strings.HasSuffix(req.URL.Path, "/transactions"):
			raw, _ := json.Marshal(map[string]any{"transactions": []any{map[string]any{"id": "sale-review", "status": "COMPLETED", "time": paid, "amount_with_breakdown": map[string]any{"gross_amount": map[string]any{"value": "9.99", "currency_code": "USD"}}}}})
			body = string(raw)
		case req.URL.Path == "/v1/billing/subscriptions/I-RECOVER":
			raw, _ := json.Marshal(map[string]any{"id": "I-RECOVER", "plan_id": "P-REVIEW", "status": "CANCELLED", "start_time": paid, "billing_info": map[string]any{"next_billing_time": end, "last_payment": map[string]any{"time": paid, "amount": map[string]any{"value": "9.99", "currency_code": "USD"}}}})
			body = string(raw)
		default:
			t.Fatalf("unexpected request %s", req.URL.String())
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	if err := svc.reconcileSubscriptionWindow(ctx, "I-RECOVER", paid.Add(-time.Hour), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetSubscriptionByID(ctx, "I-RECOVER")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "CANCELLED" {
		t.Fatalf("unexpected status %s", got.Status)
	}
	if _, err := repo.reserveSubscriptionIntent(ctx, subscriptionIntent{userID: 1, slotID: 1, templateID: 1, priceCents: 999, cycle: "month"}); err != nil {
		t.Fatalf("cancelled subscription still blocks repurchase: %v", err)
	}

	exec(`INSERT INTO user_subscriptions(user_id,subscription_id,plan_id,slot_id,template_id,status,cycle,price_cents,currency,payment_method) VALUES(2,'I-PENDING','P-REVIEW',1,1,'PENDING','month',999,'USD','paypal')`)
	testdb.ScopeSubscriptions(t, p, "paypal", "sandbox", "I-PENDING")
	visible, err := repo.GetManagedSubscriptionByUserID(ctx, 2)
	if err != nil || visible == nil {
		t.Fatalf("unexpected status result %+v %v", visible, err)
	}
	if _, err := repo.reserveSubscriptionIntent(ctx, subscriptionIntent{userID: 2, slotID: 2, templateID: 1, priceCents: 999, cycle: "month"}); err != ErrSubscriptionConflict {
		t.Fatalf("expected existing pending agreement conflict: %v", err)
	}
}

func TestLongAttributionDoesNotBlockSettlement(t *testing.T) {
	p := testdb.Open(t)
	ctx := context.Background()
	long := strings.Repeat("c", 60)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := p.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO users(id,utm_source,utm_campaign) VALUES(1,'source',$1)`, long)
	exec(`INSERT INTO recharge_templates(id,name,is_default) VALUES(1,'Default',true); INSERT INTO recharge_slots(id,template_id,slot_index,type,coins,price,price_cents) VALUES(1,1,1,'single',100,'$9.99',999)`)
	r := NewDBRepository(wallet.NewDBRepository())
	slot, err := r.GetRechargeSlotByID(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.CreateCheckout(ctx, 1, slot, 0, checkoutAttribution{}); err != nil {
		t.Fatalf("checkout rejected accepted attribution: %v", err)
	}
	exec(`INSERT INTO user_subscriptions(user_id,subscription_id,plan_id,slot_id,template_id,status,cycle,price_cents,currency,payment_method) VALUES(1,'I-LONG-UTM','P-LONG',1,1,'PENDING','month',999,'USD','paypal')`)
	sub, err := r.GetSubscriptionByID(ctx, "I-LONG-UTM")
	if err != nil {
		t.Fatal(err)
	}
	paid := time.Now().UTC()
	if _, err := r.RecordSubscriptionRenewalOrderTx(ctx, sub, "sale-long-utm", 999, "USD", paid, paid.AddDate(0, 1, 0), "ACTIVE", &model.ThirdPartyPaymentOrder{Status: "COMPLETED", RawPayload: "{}"}); err != nil {
		t.Fatalf("settlement rejected accepted attribution: %v", err)
	}
}
