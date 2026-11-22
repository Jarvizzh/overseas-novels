package application

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"reader-backend/internal/payment"
	"reader-backend/internal/testdb"
	"reader-backend/internal/wallet"
)

type payPalSwitchAdapter struct {
	*vipOfferAdapter
	verify func(context.Context, string, string, int64, string, payment.WebhookAccount) (bool, error)
}

func (a *payPalSwitchAdapter) CanCloseSingleCheckout(ctx context.Context, id, intent string, amount int64, currency string, account payment.WebhookAccount) (bool, error) {
	return a.verify(ctx, id, intent, amount, currency, account)
}

func TestStripeSandboxCheckoutSettlementAndRefund(t *testing.T) {
	t.Setenv("CONVERSION_ENVIRONMENT", "sandbox")
	pool := testdb.Open(t)
	ctx := t.Context()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO users(id) VALUES(523),(524);
		INSERT INTO novels(id,title,author,publish_status) VALUES (523,'Stripe book','Author','published');
		INSERT INTO recharge_templates(id,name,is_default) VALUES(523,'Stripe',true);
		INSERT INTO recharge_slots(id,template_id,slot_index,type,price,price_cents,coins,bonus)
		VALUES(523,523,1,'whole_book','9.99',999,0,5);
		SELECT assert_paypal_identity('stripe-test-paypal','sandbox');
		INSERT INTO payment_channel_accounts(provider,account_key,environment,credentials_ref,webhook_ref)
		VALUES('stripe','acct_Sandbox','sandbox','test','test');
		INSERT INTO payment_stripe_one_time_offers(account_id,slot_id,product_ref,price_ref,amount_minor,allowed_countries,enabled)
		SELECT id,523,'prod_Book','price_Book',999,ARRAY['US'],true FROM payment_channel_accounts WHERE account_key='acct_Sandbox';`)
	repo := NewDBRepository(wallet.NewDBRepository())
	account := payment.WebhookAccount{AccountKey: "acct_Sandbox", Environment: "sandbox"}
	url := "https://reader.example.invalid/recharge"
	intent, err := repo.preparePurchase(ctx, 523, purchaseRequest{key: uuid.New(), slotID: 523, novelID: 523}, checkoutAttribution{FBC: "fb.1.1740000000.fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.reserveStripePurchase(ctx, 524, intent.ID, url, url, account); !errors.Is(err, ErrCheckoutUnavailable) {
		t.Fatalf("another user reserved this purchase: %v", err)
	}
	const payPalReference = "paypal-before-stripe"
	payPalAccount := payment.WebhookAccount{AccountKey: "stripe-test-paypal", Environment: "sandbox"}
	paypalAttempt, err := repo.reservePayPalAttempt(ctx, 523, intent.ID, url, url, payPalAccount)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.finishPayPalAttempt(ctx, paypalAttempt, payPalReference, url); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.reserveStripePurchase(ctx, 523, intent.ID, url, url, account); !errors.Is(err, ErrCheckoutPending) {
		t.Fatalf("Stripe replaced an in-flight PayPal attempt: %v", err)
	}
	adapter := &payPalSwitchAdapter{vipOfferAdapter: &vipOfferAdapter{account: payPalAccount}}
	adapter.verify = func(ctx context.Context, id, reference string, amount int64, currency string, account payment.WebhookAccount) (bool, error) {
		if id != payPalReference || reference != intent.ID || amount != 999 || currency != "USD" || account != payPalAccount {
			t.Fatalf("unexpected PayPal switch evidence scope: %s %s %d %s %+v", id, reference, amount, currency, account)
		}
		if allowed, err := repo.leaseCheckoutCapture(ctx, id); err != nil || allowed {
			t.Fatalf("capture possible while verifying switch: %v %v", allowed, err)
		}
		return true, nil
	}
	service := &service{repo: repo, paypalClient: adapter, adapters: payment.NewAdapters(adapter)}
	if err := service.closePayPalForCheckoutSwitch(ctx, 524, intent); !errors.Is(err, ErrCheckoutPending) {
		t.Fatalf("wrong owner closed checkout: %v", err)
	}
	if err := service.closePayPalForCheckoutSwitch(ctx, 523, intent); err != nil {
		t.Fatal(err)
	}
	pinned, err := repo.reserveStripePurchase(ctx, 523, intent.ID, url, url, account)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.reservePayPalAttempt(ctx, 523, intent.ID, url, url, payment.WebhookAccount{AccountKey: "stripe-test-paypal", Environment: "sandbox"}); !errors.Is(err, ErrCheckoutPending) {
		t.Fatalf("PayPal replaced an in-flight Stripe attempt: %v", err)
	}
	if paused, err := repo.claimStripeAttempts(ctx, account, pinned.ID, false); err != nil || len(paused) != 0 {
		t.Fatalf("disabled new Stripe checkout was submitted: %+v %v", paused, err)
	}
	due, err := repo.claimStripeAttempts(ctx, account, pinned.ID, true)
	if err != nil || len(due) != 1 || !due[0].MayCreate {
		t.Fatalf("initial Stripe lease: %+v %v", due, err)
	}
	exec(`UPDATE payment_stripe_one_time_offers SET price_ref='price_Changed',enabled=false`)
	resumed, err := repo.reserveStripePurchase(ctx, 523, intent.ID, url+"?new=1", url, account)
	if err != nil || resumed.ID != pinned.ID || resumed.Offer.PriceID != "price_Book" || resumed.ReturnURL != url {
		t.Fatalf("Stripe snapshot was changed: %+v %v", resumed, err)
	}
	notice := payment.SingleCheckoutNotice{EventID: "evt_Paid", EventType: "checkout.session.completed", SessionID: "cs_Paid", AttemptID: pinned.ID, Account: account}
	for range 2 {
		if err := queueStripeNotice(ctx, &notice, []byte(`{}`)); err != nil {
			t.Fatal(err)
		}
	}
	jobs, err := claimStripeNotices(ctx, account)
	if err != nil || len(jobs) != 1 || jobs[0].attemptID != pinned.ID {
		t.Fatalf("Stripe inbox replay: %+v %v", jobs, err)
	}
	capture := &payment.SingleChargeProof{ID: "ch_Paid", IntentID: "pi_Paid", Country: "US", Currency: "USD", AmountMinor: 999,
		PaidAt: time.Now(), EligibleTerms: true, Evidence: json.RawMessage(`{"fixture":true}`)}
	proof := payment.SingleCheckoutProof{ID: "cs_Paid", AttemptID: pinned.ID, Receipt: capture}
	for range 2 {
		if err := repo.applyStripeProof(ctx, *pinned, proof, nil); err != nil {
			t.Fatal(err)
		}
	}
	status, err := repo.purchaseByReference(ctx, 523, intent.ID, "")
	if err != nil || status.Provider != "stripe" || status.Status != "completed" {
		t.Fatalf("Stripe return: %+v %v", status, err)
	}
	late := &payment.OrderCapture{OrderID: payPalReference, CaptureID: "payPalLateCapture", CustomID: intent.ID,
		Account: payPalAccount,
		Status:  "COMPLETED", CurrencyCode: "USD", GrossAmount: "9.99", RawPayload: "{}"}
	if _, err := repo.findCheckout(ctx, late.OrderID); err != nil {
		t.Fatalf("historical PayPal attempt after Stripe fulfillment: %v", err)
	}
	for range 2 {
		if _, err := repo.settleCheckout(ctx, late.OrderID, late); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.CompletePayPalRefund(ctx, "paypal-late-refund", late.CaptureID, 999, "USD"); err != nil {
		t.Fatal(err)
	}
	var extra int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM third_party_payment_orders WHERE order_id=$1 AND payment_provider='paypal'
		AND capture_id=$2 AND reconciliation_note IS NOT NULL`, pinned.OrderID, late.CaptureID).Scan(&extra); err != nil || extra != 1 {
		t.Fatalf("late PayPal receipts=%d error=%v", extra, err)
	}
	if status, err = repo.purchaseByReference(ctx, 523, intent.ID, late.OrderID); err != nil || status.Status != "completed" {
		t.Fatalf("PayPal refund changed Stripe-funded access: %+v %v", status, err)
	}
	var debit, funding, outbox int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM transactions WHERE user_id=523 AND biz_type='recharge'),
		(SELECT count(*) FROM payment_fulfillment_funding WHERE order_id=$1),
		(SELECT count(*) FROM reader_conversion_outbox WHERE order_id=$1 AND event_name='Purchase')`, pinned.OrderID).Scan(&debit, &funding, &outbox); err != nil || debit != 1 || funding != 1 || outbox != 1 {
		t.Fatalf("replayed fulfillment ledger=%d funding=%d outbox=%d err=%v", debit, funding, outbox, err)
	}
	for range 2 {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := applyStripeRefundTx(ctx, tx, account, &payment.StripeRefundProof{ID: "re_Full", ChargeID: "ch_Paid", Currency: "USD", Status: "succeeded", AmountMinor: 999}); err != nil {
			tx.Rollback(ctx)
			t.Fatal(err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	status, err = repo.purchaseByReference(ctx, 523, intent.ID, "")
	if err != nil || status.Status != "refunded" {
		t.Fatalf("refunded Stripe return: %+v %v", status, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM transactions WHERE user_id=523 AND biz_type='refund'`).Scan(&debit); err != nil || debit != 1 {
		t.Fatalf("replayed refund changed wallet twice: %d %v", debit, err)
	}
}
