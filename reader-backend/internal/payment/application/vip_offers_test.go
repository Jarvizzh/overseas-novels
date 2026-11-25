package application

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"reader-backend/internal/payment"
	"reader-backend/internal/testdb"
	"reader-backend/internal/wallet"
)

type vipOfferAdapter struct {
	paypalCheckout
	account       payment.WebhookAccount
	planRequests  []payment.CreateSubscriptionPlanParam
	agreements    []payment.CreateSubscriptionParam
	losePlanReply bool
}

func (a *vipOfferAdapter) Provider() string { return payment.ProviderPayPal }

func (a *vipOfferAdapter) CheckoutAccount(context.Context) (payment.WebhookAccount, error) {
	return a.account, nil
}

func (a *vipOfferAdapter) WebhookConfigured(context.Context) bool { return true }

func (a *vipOfferAdapter) CreatePlan(_ context.Context, p payment.CreateSubscriptionPlanParam) (string, error) {
	a.planRequests = append(a.planRequests, p)
	if a.losePlanReply {
		return "", context.DeadlineExceeded
	}
	return fmt.Sprintf("P-%d", p.PriceCents), nil
}

func (a *vipOfferAdapter) CreateSubscription(_ context.Context, p payment.CreateSubscriptionParam) (*payment.SubscriptionResult, error) {
	a.agreements = append(a.agreements, p)
	return &payment.SubscriptionResult{SubscriptionID: fmt.Sprintf("I-FIXTURE-%d", p.UserID),
		ApproveURL: "https://www.sandbox.paypal.com/test"}, nil
}

func (a *vipOfferAdapter) VerifyVIPPlan(_ context.Context, id string, offer payment.CreateSubscriptionPlanParam) error {
	if offer.Account != a.account || offer.Currency != "USD" || offer.Cycle != payment.CycleMonth ||
		id != fmt.Sprintf("P-%d", offer.PriceCents) {
		return errors.New("Plan terms differ")
	}
	return nil
}

func TestVIPOfferPublicationAndVersionPinnedCheckout(t *testing.T) {
	t.Setenv("CONVERSION_ENVIRONMENT", "sandbox")
	p := testdb.Open(t)
	ctx := t.Context()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := p.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO users(id) VALUES(1),(2),(3);
		INSERT INTO recharge_templates(id,name,is_default) VALUES(1,'Default',true);
		SELECT assert_paypal_identity('vip-publish-test','sandbox');
		UPDATE payment_channel_accounts SET status='published',is_primary=true,new_orders_enabled=true,new_subscriptions_enabled=true WHERE account_key='vip-publish-test'`)
	saveOffer := func(id, price int) {
		t.Helper()
		exec(`DELETE FROM recharge_slots WHERE template_id=1`)
		var prodID int64
		if err := p.QueryRow(ctx, `
			INSERT INTO products(code,product_type,name,description,price_cents,currency,coins,bonus,vip_cycle,novel_scope_type)
			VALUES($1,'vip',$1,'Legacy VIP test',$2,'USD',0,0,'month','none')
			RETURNING id`, fmt.Sprintf("LEGACY_VIP_%d_%d", id, price), price).Scan(&prodID); err != nil {
			t.Fatal(err)
		}
		exec(`INSERT INTO recharge_slots(id,template_id,slot_index,product_id,type,price,price_cents,subscription_cycle)
			VALUES($1,1,1,$2,'subscription',$3::int::text,$3,'month')`, id, prodID, price)
		exec(`UPDATE recharge_slots s SET vip_offer_version_id=snapshot_vip_offer(s) WHERE s.id=$1`, id)
	}
	adapter := &vipOfferAdapter{account: payment.WebhookAccount{AccountKey: "vip-publish-test", Environment: "sandbox"}}
	repo := NewDBRepository(wallet.NewDBRepository())
	svc := NewService(repo, nil, payment.NewAdapters(adapter)).(*service)
	create := func(user int64, slot int) (*payment.SubscriptionResult, error) {
		return svc.CreateSubscription(ctx, user, payment.ProviderPayPal, slot, "", "", "", "", "", "", "", "", "", "")
	}
	saveOffer(11, 999)
	if _, err := create(1, 11); !errors.Is(err, ErrCheckoutUnavailable) {
		t.Fatalf("unpublished offer checkout: %v", err)
	}
	svc.reconcilePayPalVIPOffers(ctx)
	intent, err := repo.reserveSubscriptionIntent(ctx, subscriptionIntent{userID: 1, slotID: 11, templateID: 1, account: adapter.account})
	if err != nil || intent.planID != "P-999" {
		t.Fatalf("initial offer intent %+v: %v", intent, err)
	}
	repo.releaseSubscriptionIntent(ctx, intent.id)
	exec(`INSERT INTO user_subscriptions(user_id,subscription_id,plan_id,status,cycle,price_cents)
		VALUES(3,'I-OLD-AGREEMENT','P-999','CANCELLED','month',999)`)
	saveOffer(12, 1299)
	svc.reconcilePayPalVIPOffers(ctx)
	exec(`UPDATE payment_channel_accounts SET new_subscriptions_enabled=false WHERE account_key='vip-publish-test'`)
	if _, err := create(2, 12); !errors.Is(err, ErrCheckoutPaused) {
		t.Fatalf("paused account accepted a new VIP: %v", err)
	}
	first, err := create(1, 11)
	if err != nil || first.IntentID != intent.id {
		t.Fatalf("old intent did not resume after reprice: %+v %v", first, err)
	}
	exec(`UPDATE payment_channel_accounts SET new_subscriptions_enabled=true WHERE account_key='vip-publish-test'`)
	if _, err := create(2, 12); err != nil {
		t.Fatal(err)
	}
	history, err := repo.recentPayPalVIPPurchases(ctx, 1)
	if err != nil || len(history) != 1 || history[0].ID != intent.id || history[0].Provider != payment.ProviderPayPal || history[0].ApprovalURL == "" {
		t.Fatalf("reopen PayPal VIP through purchase history: %+v %v", history, err)
	}
	if _, err := repo.getVIPPurchase(ctx, 2, intent.id, ""); !errors.Is(err, ErrCheckoutUnavailable) {
		t.Fatalf("another account could read a PayPal VIP intent: %v", err)
	}
	if len(adapter.agreements) != 2 || adapter.agreements[0].PlanID != "P-999" ||
		adapter.agreements[0].PriceCents != 999 || adapter.agreements[1].PlanID != "P-1299" ||
		adapter.agreements[1].PriceCents != 1299 {
		t.Fatalf("checkout changed version: %+v", adapter.agreements)
	}
	for i, want := range []struct {
		id      string
		version int
		price   int
		plan    string
	}{
		{"I-FIXTURE-1", 11, 999, "P-999"},
		{"I-FIXTURE-2", 12, 1299, "P-1299"},
		{"I-OLD-AGREEMENT", 0, 999, "P-999"},
	} {
		var version int
		var price int
		var plan string
		if err := p.QueryRow(ctx, `SELECT COALESCE(vip_offer_version_id,0),price_cents,plan_id FROM user_subscriptions
			WHERE subscription_id=$1`, want.id).Scan(&version, &price, &plan); err != nil ||
			version != want.version || price != want.price || plan != want.plan {
			t.Fatalf("agreement %d version=%v price=%d plan=%s err=%v", i, version, price, plan, err)
		}
	}
	// Saving an unchanged offer reuses the bound Plan. Losing the response for a
	// different offer leaves it unavailable and never retries the channel write.
	saveOffer(13, 1299)
	svc.reconcilePayPalVIPOffers(ctx)
	if len(adapter.planRequests) != 2 {
		t.Fatalf("identical terms created another PayPal Plan: %+v", adapter.planRequests)
	}
	saveOffer(14, 1499)
	adapter.losePlanReply = true
	svc.reconcilePayPalVIPOffers(ctx)
	saveOffer(15, 1499)
	svc.reconcilePayPalVIPOffers(ctx)
	if len(adapter.planRequests) != 3 {
		t.Fatalf("unknown plan creation was retried: %+v", adapter.planRequests)
	}
	if _, err := create(3, 15); !errors.Is(err, ErrCheckoutUnavailable) {
		t.Fatalf("unconfirmed version allowed purchase: %v", err)
	}
	request := adapter.planRequests[2].RequestID
	if err := RecoverPayPalVIPPlan(ctx, adapter, request, "P-1299"); err == nil {
		t.Fatal("wrong price was accepted for Plan recovery")
	}
	if err := RecoverPayPalVIPPlan(ctx, adapter, request, "P-1499"); err != nil {
		t.Fatal(err)
	}
	if _, err := create(3, 15); err != nil || adapter.agreements[2].PlanID != "P-1499" {
		t.Fatalf("recovered Plan unavailable: %+v %v", adapter.agreements, err)
	}
}
