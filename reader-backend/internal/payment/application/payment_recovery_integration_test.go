package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"reader-backend/internal/model"
	"reader-backend/internal/novel"
	"reader-backend/internal/payment"
	"reader-backend/internal/payment/stripe"
	"reader-backend/internal/testdb"
)

type paymentAccountFixture struct {
	*vipOfferAdapter
	orders      int
	switchError error
	canSwitch   bool
}

func (p *paymentAccountFixture) CreateOrder(_ context.Context, scope payment.WebhookAccount, _ int64, _, _, _, _, _ string) (string, string, error) {
	if scope != p.account {
		return "", "", errors.New("unexpected PayPal write scope")
	}
	p.orders++
	return "order-" + scope.AccountKey, "https://www.sandbox.paypal.com/checkoutnow", nil
}

func (p *paymentAccountFixture) CanCloseSingleCheckout(context.Context, string, string, int64, string, payment.WebhookAccount) (bool, error) {
	return p.canSwitch, p.switchError
}

func TestPayPalRejectsEnvironmentMismatchAndResumesOldAccount(t *testing.T) {
	t.Setenv("CONVERSION_ENVIRONMENT", "sandbox")
	pool := testdb.Open(t)
	ctx := t.Context()
	_, err := pool.Exec(ctx, `INSERT INTO users(id) VALUES(716);
		SELECT assert_paypal_identity('original','sandbox');
		UPDATE payment_channel_accounts SET status='published',is_primary=true,new_orders_enabled=true,new_subscriptions_enabled=true WHERE account_key='original';
		INSERT INTO payment_channel_accounts(provider,account_key,environment,credentials_ref,webhook_ref,status,new_orders_enabled)
		VALUES('paypal','next','sandbox','test','test','published',true),('paypal','live-merchant','live','test','test','published',true)`)
	if err != nil {
		t.Fatal(err)
	}
	recProdID := seedTestProductAndSlot(t, pool, testCatalogSpec{
		SlotID: 716, TemplateID: 716, TemplateName: "Payments", IsDefault: true, SlotIndex: 1,
		Code: "TEST_RECOVERY_716", ProductType: "single", PriceCents: 999, Coins: 50,
		Environment: "sandbox", PayPalAccountKey: "original",
	})
	if _, err := pool.Exec(ctx, `INSERT INTO product_channel_bindings(product_id,account_id,provider,environment,enabled,validation_status,validated_at)
		SELECT $1,id,'paypal','sandbox',true,'verified',now() FROM payment_channel_accounts WHERE account_key='next'`, recProdID); err != nil {
		t.Fatal(err)
	}
	makeAdapter := func(id, env string) *paymentAccountFixture {
		return &paymentAccountFixture{vipOfferAdapter: &vipOfferAdapter{account: payment.WebhookAccount{AccountKey: id, Environment: env}}}
	}
	live, original, next := makeAdapter("live-merchant", "live"), makeAdapter("original", "sandbox"), makeAdapter("next", "sandbox")
	repo := NewDBRepository(nil)
	createIntent := func() string {
		t.Helper()
		intent, err := repo.preparePurchase(ctx, 716, purchaseRequest{key: uuid.New(), slotID: 716}, checkoutAttribution{})
		if err != nil {
			t.Fatal(err)
		}
		return intent.ID
	}
	start := func(s *service, intent string) (string, error) {
		ref, _, err := s.startPayPalAttempt(ctx, 716, intent, "https://reader.example/recharge", "https://reader.example/recharge")
		return ref, err
	}
	first := createIntent()
	mismatched := NewService(repo, nil, payment.NewAdapters(live)).(*service)
	if methods, err := mismatched.GetPaymentMethods(ctx); err != nil || methods.PayPalSingle {
		t.Fatalf("mismatched method was offered: %+v %v", methods, err)
	}
	if _, err := start(mismatched, first); !errors.Is(err, ErrCheckoutPaused) || live.orders != 0 {
		t.Fatalf("mismatched request reached channel: %d %v", live.orders, err)
	}
	old := NewService(repo, nil, payment.NewAdapters(original)).(*service)
	if ref, err := start(old, first); ref != "order-original" || err != nil {
		t.Fatalf("original order: %q %v", ref, err)
	}
	adapters := payment.NewAdapters(original)
	if err := adapters.RegisterAccount(next.account, next, true); err != nil {
		t.Fatal(err)
	}
	rotated := NewService(repo, nil, adapters).(*service)
	if ref, err := start(rotated, first); ref != "order-original" || err != nil || original.orders != 1 || next.orders != 0 {
		t.Fatalf("old checkout changed account: %q %v writes: %d/%d", ref, err, original.orders, next.orders)
	}
	intent, err := rotated.GetPurchase(ctx, 716, first, "")
	if err != nil {
		t.Fatal(err)
	}
	original.switchError = context.DeadlineExceeded
	if err := rotated.closePayPalForCheckoutSwitch(ctx, 716, intent); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("switch timeout: %v", err)
	}
	if allowed, err := repo.leaseCheckoutCapture(ctx, "order-original"); err != nil || allowed {
		t.Fatalf("capture allowed during switch: %v %v", allowed, err)
	}
	if intent, err := rotated.GetPurchase(ctx, 716, first, ""); err != nil || intent.ConfirmationCode != "payment_switch_pending" {
		t.Fatalf("switch state: %+v %v", intent, err)
	}
	var staleToken uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT paypal_switch_token FROM payment_checkout_attempts
		WHERE provider_order_id='order-original'`).Scan(&staleToken); err != nil {
		t.Fatal(err)
	}
	if ref, err := start(rotated, first); ref != "order-original" || err != nil || original.orders != 1 {
		t.Fatalf("resuming original checkout: %s %v", ref, err)
	}
	original.switchError, original.canSwitch = nil, true
	if err := rotated.verifyPayPalCheckoutSwitch(ctx, original, original.account, intent, "order-original", staleToken); !errors.Is(err, ErrCheckoutPending) {
		t.Fatalf("stale proof closed the resumed attempt: %v", err)
	}
	if allowed, err := repo.leaseCheckoutCapture(ctx, "order-original"); err != nil || !allowed {
		t.Fatalf("capture not restored: %v %v", allowed, err)
	}
	if ref, err := start(rotated, createIntent()); ref != "order-next" || err != nil {
		t.Fatalf("new checkout did not use primary: %q %v", ref, err)
	}
}

type stripeLookupOnlyFixture struct {
	payment.StripeVIPAdapter
	writes, reads int
}

func (a *stripeLookupOnlyFixture) CreateStripeVIPCheckout(context.Context, payment.StripeVIPCheckoutRequest) (payment.StripeVIPCheckout, error) {
	a.writes++
	return payment.StripeVIPCheckout{}, &stripe.APIError{Disposition: stripe.LookupOnly}
}

func (a *stripeLookupOnlyFixture) FindStripeVIPCheckout(context.Context, string, time.Time, time.Time) (string, error) {
	a.reads++
	return "", nil
}

func TestStripeVIPLookupOnlyReplacementAndRefundBeforeFulfillment(t *testing.T) {
	t.Setenv("CONVERSION_ENVIRONMENT", "sandbox")
	pool := testdb.Open(t)
	ctx := t.Context()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO users(id) VALUES(817);
		INSERT INTO payment_channel_accounts(provider,account_key,environment,credentials_ref,webhook_ref,status,is_primary,new_subscriptions_enabled)
		VALUES('stripe','acct_Audit','sandbox','test','test','published',true,true),('stripe','acct_New','sandbox','test','test','draft',false,false)`)
	seedTestProductAndSlot(t, pool, testCatalogSpec{
		SlotID: 817, TemplateID: 817, TemplateName: "Stripe VIP", IsDefault: true, SlotIndex: 1,
		Code: "TEST_STRIPE_VIP_817", ProductType: "vip", VipCycle: "month", PriceCents: 1000,
		Environment: "sandbox", StripeAccountKey: "acct_Audit", StripeProductRef: "prod_VIP", StripePriceRef: "price_VIP",
	})
	account := payment.WebhookAccount{AccountKey: "acct_Audit", Environment: "sandbox"}
	intent, err := reserveStripeVIP(ctx, stripeVIPRequest{userID: 817, slotID: 817, account: account,
		returnURL: "https://reader.example/recharge", cancelURL: "https://reader.example/recharge"}, true)
	if err != nil {
		t.Fatal(err)
	}
	primary := payment.WebhookAccount{AccountKey: "acct_New", Environment: "sandbox"}
	resumed, err := reserveStripeVIP(ctx, stripeVIPRequest{userID: 817, slotID: 817, account: primary}, false)
	if err != nil || resumed.ID != intent.ID || resumed.Account != account {
		t.Fatalf("original VIP attempt was not resumed after rotation: %+v %v", resumed, err)
	}
	claim := func() stripeVIPIntent {
		t.Helper()
		jobs, err := claimStripeVIP(ctx, account, intent.ID, true)
		if err != nil || len(jobs) != 1 {
			t.Fatalf("lease: %v %v", jobs, err)
		}
		return jobs[0]
	}
	svc, adapter := &service{}, &stripeLookupOnlyFixture{}
	if err := svc.syncStripeVIP(ctx, adapter, claim(), ""); err == nil {
		t.Fatal("expected uncertain Stripe write result")
	}
	exec(`UPDATE payment_stripe_subscription_intents SET next_check_at=now() WHERE id=$1`, intent.ID)
	if err := svc.syncStripeVIP(ctx, adapter, claim(), ""); err != nil || adapter.writes != 1 || adapter.reads != 1 {
		t.Fatalf("lookup-only write/reads: %d/%d %v", adapter.writes, adapter.reads, err)
	}
	now := time.Now().UTC()
	proof := payment.StripeVIPInvoice{ID: "in_Original", BillingReason: "subscription_create", Currency: "USD", Country: "US", Status: "void",
		DueMinor: 1000, RemainingMinor: 1000, StartsAt: now.Add(-time.Hour), EndsAt: now.AddDate(0, 1, 0),
		Subscription: payment.StripeVIPSubscription{ID: "sub_Proof", CustomerID: "cus_Proof", IntentID: intent.ID, PriceID: intent.Offer.PriceID, Status: "active"}}
	settle := func() {
		t.Helper()
		if err := settleStripeVIPInvoice(ctx, *intent, proof, nil); err != nil {
			t.Fatal(err)
		}
	}
	settle()
	allocate := func(id string) []payment.StripeVIPAllocation {
		return []payment.StripeVIPAllocation{{ID: "inpay_" + id, AmountMinor: 1000, Receipt: payment.SingleChargeProof{
			ID: "ch_" + id, Currency: "USD", AmountMinor: 1000, PaidAt: now, EligibleTerms: true, Evidence: []byte("{}")}}}
	}
	proof.ID, proof.Status, proof.RemainingMinor = "in_Replacement", "paid", 0
	proof.RevisesInvoiceID, proof.RevisionVerified, proof.Allocations = "in_Original", true, allocate("Replacement")
	settle()
	if result, err := stripeVIPPurchase(ctx, 817, account, intent.ID); err != nil || result.Status != "completed" {
		t.Fatalf("replacement projection: %+v %v", result, err)
	}
	proof.ID, proof.BillingReason, proof.Country = "in_Blocked", "subscription_cycle", "GB"
	proof.StartsAt, proof.EndsAt = proof.EndsAt, proof.EndsAt.AddDate(0, 1, 0)
	proof.RevisesInvoiceID, proof.RevisionVerified, proof.Allocations = "", false, allocate("Blocked")
	settle()
	for range 2 {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := applyStripeRefundTx(ctx, tx, account, &payment.StripeRefundProof{ID: "re_Blocked", ChargeID: "ch_Blocked", AmountMinor: 1000, Currency: "USD", Status: "succeeded"}); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatal(err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	settle()
	var status string
	var fulfilled bool
	if err := pool.QueryRow(ctx, `SELECT status,fulfilled_at IS NOT NULL FROM recharge_orders WHERE external_ref_id='in_Blocked'`).Scan(&status, &fulfilled); err != nil || status != "Refunded" || fulfilled {
		t.Fatalf("unfulfilled full refund: %s %v %v", status, fulfilled, err)
	}
}

type renewalFailureFixture struct {
	*vipOfferAdapter
	details payment.SubscriptionDetails
}

func (a *renewalFailureFixture) GetSubscription(context.Context, string) (*payment.SubscriptionDetails, error) {
	return &a.details, nil
}

func TestVIPEnvironmentScopeAndPayPalRenewalFailure(t *testing.T) {
	t.Setenv("CONVERSION_ENVIRONMENT", "production")
	pool := testdb.Open(t)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id) VALUES(718);
		INSERT INTO user_subscriptions(user_id,subscription_id,plan_id,status,payment_method,last_payment_time,current_period_end)
		VALUES (718,'I-SANDBOX-VIP','P-SANDBOX','ACTIVE','paypal',now()-interval '1 day',now()+interval '20 days'),
			(718,'I-UNKNOWN-VIP','P-UNKNOWN','ACTIVE','paypal',now()-interval '1 day',now()+interval '20 days')`); err != nil {
		t.Fatal(err)
	}
	testdb.ScopeSubscriptions(t, pool, "paypal", "sandbox", "I-SANDBOX-VIP")
	repo := NewDBRepository(nil)
	reading := novel.NewDBRepository()
	if access, err := reading.HasActiveSubscription(ctx, 718); err != nil || access {
		t.Fatalf("sandbox or unknown agreement granted live VIP: %v %v", access, err)
	}
	if managed, err := repo.ListManagedSubscriptions(ctx, 718); err != nil || len(managed) != 0 {
		t.Fatalf("sandbox or unknown agreement showed in live management: %v %v", managed, err)
	}
	if _, err := repo.reserveSubscriptionIntent(ctx, subscriptionIntent{userID: 718, slotID: 1, templateID: 1, cycle: "month", priceCents: 999}); err != nil {
		t.Fatalf("sandbox or unknown agreement occupied live checkout: %v", err)
	}

	t.Setenv("CONVERSION_ENVIRONMENT", "sandbox")
	account := payment.WebhookAccount{AccountKey: "fixture-paypal-sandbox", Environment: "sandbox"}
	adapter := &renewalFailureFixture{vipOfferAdapter: &vipOfferAdapter{account: account}, details: payment.SubscriptionDetails{
		SubscriptionID: "I-SANDBOX-VIP", PlanID: "P-SANDBOX", Status: "SUSPENDED",
	}}
	svc := NewService(repo, reading, payment.NewAdapters(adapter)).(*service)
	event := &payment.WebhookEventResult{EventType: "subscription_payment_failed", Account: account,
		SubscriptionID: "I-SANDBOX-VIP", PaidAt: time.Now().Add(-time.Hour)}
	for range 2 {
		if err := svc.applyPayPalEvent(ctx, adapter, event); err != nil {
			t.Fatal(err)
		}
	}
	managed, err := repo.ListManagedSubscriptions(ctx, 718)
	if err != nil || len(managed) != 1 || managed[0].RenewalFailedAt == nil || managed[0].Status != "SUSPENDED" {
		t.Fatalf("failure projection: %+v %v", managed, err)
	}
	if access, err := reading.HasActiveSubscription(ctx, 718); err != nil || !access {
		t.Fatalf("renewal failure revoked existing paid period: %v %v", access, err)
	}
	sub, err := repo.GetSubscriptionByID(ctx, "I-SANDBOX-VIP")
	if err != nil || sub == nil {
		t.Fatalf("subscription: %+v %v", sub, err)
	}
	at := event.PaidAt.Add(30 * time.Minute)
	if _, err := repo.RecordSubscriptionRenewalOrderTx(ctx, sub, "sale-recovered", 999, "USD", at, at.AddDate(0, 1, 0), "ACTIVE",
		&model.ThirdPartyPaymentOrder{RawPayload: "{}"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.applyPayPalEvent(ctx, adapter, event); err != nil {
		t.Fatal(err)
	}
	managed, err = repo.ListManagedSubscriptions(ctx, 718)
	if err != nil || len(managed) != 1 || managed[0].RenewalFailedAt != nil {
		t.Fatalf("old failure overrode verified renewal: %+v %v", managed, err)
	}
}
