package application

import (
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"reader-backend/internal/payment"
	"reader-backend/internal/testdb"
	"reader-backend/internal/wallet"
)

func TestPurchaseIntentReservationAndFunding(t *testing.T) {
	t.Setenv("CONVERSION_ENVIRONMENT", "sandbox")
	ctx := t.Context()
	pool := testdb.Open(t)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO users(id) VALUES (742),(743);
		INSERT INTO novels(id,title,author,publish_status) VALUES (742,'Checkout Book','Author','published');
		SELECT assert_paypal_identity('purchase-mock','sandbox');`)
	seedTestProductAndSlot(t, pool, testCatalogSpec{
		SlotID: 742, TemplateID: 742, TemplateName: "Purchase Fixture", IsDefault: true, SlotIndex: 1,
		Code: "TEST_INTENT_742", ProductType: "whole_book", PriceCents: 999,
		Environment: "sandbox", PayPalAccountKey: "purchase-mock",
	})
	seedTestProductAndSlot(t, pool, testCatalogSpec{
		SlotID: 743, TemplateID: 742, TemplateName: "Purchase Fixture", IsDefault: true, SlotIndex: 2,
		Code: "TEST_INTENT_743", ProductType: "single", PriceCents: 699, Coins: 60, Bonus: 10,
		Environment: "sandbox", PayPalAccountKey: "purchase-mock",
	})

	repo := NewDBRepository(wallet.NewDBRepository())
	book := purchaseRequest{slotID: 742, novelID: 742, returnPath: "/content?novel_id=742"}
	type attemptResult struct {
		intent *PurchaseIntent
		err    error
	}
	results := make(chan attemptResult, 8)
	var group sync.WaitGroup
	for range cap(results) {
		group.Go(func() {
			request := book
			request.key = uuid.New()
			intent, err := repo.preparePurchase(ctx, 742, request, checkoutAttribution{})
			results <- attemptResult{intent, err}
		})
	}
	group.Wait()
	close(results)
	var bookIntent string
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		if bookIntent == "" {
			bookIntent = result.intent.ID
		}
		if result.intent.ID != bookIntent {
			t.Fatal("parallel book purchases received separate intents")
		}
	}

	coins := purchaseRequest{key: uuid.New(), slotID: 743}
	firstCoins, err := repo.preparePurchase(ctx, 742, coins, checkoutAttribution{})
	if err != nil {
		t.Fatal(err)
	}
	seedTestProductAndSlot(t, pool, testCatalogSpec{
		SlotID: 743, TemplateID: 742, TemplateName: "Purchase Fixture", IsDefault: true, SlotIndex: 2,
		Code: "TEST_INTENT_743_REPRICE", ProductType: "single", PriceCents: 799, Coins: 60, Bonus: 10,
		Environment: "sandbox", PayPalAccountKey: "purchase-mock",
	})
	replayedCoins, err := repo.preparePurchase(ctx, 742, coins, checkoutAttribution{})
	if err != nil || replayedCoins.ID != firstCoins.ID || replayedCoins.AmountMinor != 699 {
		t.Fatalf("original checkout snapshot was lost: %+v %v", replayedCoins, err)
	}
	conflict := coins
	conflict.slotID = 742
	if _, err := repo.preparePurchase(ctx, 742, conflict, checkoutAttribution{}); !errors.Is(err, ErrCheckoutKeyConflict) {
		t.Fatalf("different parameters reused the checkout key: %v", err)
	}
	coins.key = uuid.New()
	otherCoins, err := repo.preparePurchase(ctx, 742, coins, checkoutAttribution{})
	if err != nil || otherCoins.ID == firstCoins.ID || otherCoins.AmountMinor != 799 {
		t.Fatalf("explicit new coin purchase did not get a new intent: %+v %v", otherCoins, err)
	}

	account := payment.WebhookAccount{AccountKey: "purchase-mock", Environment: "sandbox"}
	const returnURL = "https://reader.example.invalid/recharge"
	first, err := repo.reservePayPalAttempt(ctx, 742, bookIntent, returnURL, returnURL, account)
	if err != nil || !first.send {
		t.Fatalf("first creation was not leased: %+v %v", first, err)
	}
	exec(`INSERT INTO payment_channel_accounts(provider,environment,account_key,credentials_ref,webhook_ref)
		VALUES ('stripe','sandbox','other-mock','test:credentials','test:webhook')`)
	_, err = pool.Exec(ctx, `INSERT INTO payment_checkout_attempts(id,order_id,provider,account_id,sales_mode,status,return_url,cancel_url)
		SELECT $1,$2,'stripe',id,'direct','creating',$3,$3 FROM payment_channel_accounts WHERE account_key='other-mock'`,
		uuid.New(), first.orderID, returnURL)
	var conflictErr *pgconn.PgError
	if !errors.As(err, &conflictErr) || conflictErr.ConstraintName != "idx_payment_checkout_active" {
		t.Fatalf("another channel was able to claim an active intent: %v", err)
	}
	exec(`INSERT INTO payment_checkout_attempts(id,order_id,provider,account_id,sales_mode,status,return_url,cancel_url)
		SELECT $1,o.id,'stripe',a.id,'direct','creating',$2,$2
		FROM recharge_orders o CROSS JOIN payment_channel_accounts a
		WHERE o.checkout_id=$3 AND a.provider='stripe' AND a.account_key='other-mock'`, uuid.New(), returnURL, otherCoins.ID)
	if _, err := repo.reservePayPalAttempt(ctx, 742, otherCoins.ID, returnURL, returnURL, account); !errors.Is(err, ErrCheckoutPending) {
		t.Fatalf("PayPal replaced an in-flight Stripe attempt: %v", err)
	}

	again, err := repo.reservePayPalAttempt(ctx, 742, bookIntent, returnURL+"?change=1", returnURL, account)
	if err != nil || again.send || again.id != first.id || again.returnURL != returnURL {
		t.Fatalf("repeat modified or resent provider attempt: %+v %v", again, err)
	}
	exec(`UPDATE payment_checkout_attempts SET next_create_at=now()-interval '1 second' WHERE id=$1`, first.id)
	claimed, err := claimCheckoutCreate(ctx, account)
	if err != nil || len(claimed) != 1 || claimed[0].id != first.id || claimed[0].returnURL != returnURL {
		t.Fatalf("worker did not recover the original request: %+v %v", claimed, err)
	}
	exec(`UPDATE payment_checkout_attempts SET retry_until=now()-interval '1 second',next_create_at=now()-interval '1 second' WHERE id=$1`, first.id)
	if claimed, err := claimCheckoutCreate(ctx, account); err != nil || len(claimed) != 0 {
		t.Fatalf("worker repeated a create after its window: %+v %v", claimed, err)
	}
	if retry, err := repo.reservePayPalAttempt(ctx, 742, bookIntent, returnURL, returnURL, account); err != nil || retry.send {
		t.Fatalf("review requirement allowed capture creation to be repeated: %+v %v", retry, err)
	}

	if err := repo.finishPayPalAttempt(ctx, first, "PP-FIRST", returnURL); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.purchaseByReference(ctx, 743, bookIntent, "PP-FIRST"); !errors.Is(err, ErrCheckoutUnavailable) {
		t.Fatalf("another user opened the purchase: %v", err)
	}
	if purchase, err := repo.purchaseByReference(ctx, 742, "", "PP-FIRST"); err != nil || purchase.ID != bookIntent {
		t.Fatalf("legacy PayPal return did not locate the intent: %+v %v", purchase, err)
	}
	if sent, err := repo.leaseCheckoutCapture(ctx, "PP-FIRST"); err != nil || !sent {
		t.Fatalf("capture lease=%t %v", sent, err)
	}
	if err := repo.recordCaptureOutcome(ctx, first.orderID, "PP-FIRST", captureDeclined); err != nil {
		t.Fatal(err)
	}
	if err := repo.recordCaptureOutcome(ctx, first.orderID, "PP-FIRST", captureUnconfirmed); err != nil {
		t.Fatal(err)
	}
	pending, err := repo.purchaseByReference(ctx, 742, bookIntent, "")
	if err != nil || pending.ConfirmationCode != captureDeclined || pending.ConfirmationMessage == "" {
		t.Fatalf("later unknown outcome hid the known decline: %+v %v", pending, err)
	}

	exec(`UPDATE payment_checkout_attempts SET capture_retry_until=now()-interval '1 second',next_capture_at=now()-interval '1 second'
		WHERE id=$1`, first.id)
	if sent, err := repo.leaseCheckoutCapture(ctx, "PP-FIRST"); err != nil || sent {
		t.Fatalf("expired capture lease=%t %v", sent, err)
	}

	if err := repo.closePayPalAttempt(ctx, "PP-FIRST"); err != nil {
		t.Fatal(err)
	}
	newAttempt, err := repo.reservePayPalAttempt(ctx, 742, bookIntent, returnURL, returnURL, account)
	if err != nil || !newAttempt.send || newAttempt.id == first.id {
		t.Fatalf("no replacement after verified close: %+v %v", newAttempt, err)
	}
	replacement, err := repo.purchaseByReference(ctx, 742, bookIntent, "")
	if err != nil || replacement.ConfirmationCode != "" {
		t.Fatalf("replacement inherited a prior capture outcome: %+v %v", replacement, err)
	}

	if err := repo.finishPayPalAttempt(ctx, newAttempt, "PP-SECOND", returnURL); err != nil {
		t.Fatal(err)
	}
	capture := func(order, charge string) *payment.OrderCapture {
		return &payment.OrderCapture{Account: account, OrderID: order, CustomID: bookIntent, CaptureID: charge,
			CurrencyCode: "USD", Status: "COMPLETED", GrossAmount: "9.99", FeeAmount: "0", NetAmount: "9.99", RawPayload: "{}"}
	}
	if result, err := repo.settleCheckout(ctx, "PP-SECOND", capture("PP-SECOND", "CAPTURE-SECOND")); err != nil || !result.settled {
		t.Fatalf("first fulfillment did not settle: %+v %v", result, err)
	}
	if result, err := repo.settleCheckout(ctx, "PP-FIRST", capture("PP-FIRST", "CAPTURE-FIRST")); err != nil || result.settled {
		t.Fatalf("late extra payment was not recorded independently: %+v %v", result, err)
	}
	if err := repo.CompletePayPalRefund(ctx, "REFUND-EXTRA", "CAPTURE-FIRST", 999, "USD"); err != nil {
		t.Fatal(err)
	}

	rows, err := pool.Query(ctx, `SELECT stage,outcome,reason FROM payment_checkout_decisions
		WHERE user_id=742 AND rule_version=1 ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	outcomes := map[string]int{}
	for rows.Next() {
		var stage, outcome, reason string
		if err := rows.Scan(&stage, &outcome, &reason); err != nil {
			t.Fatal(err)
		}
		outcomes[stage+"/"+outcome+"/"+reason]++
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]int{
		"purchase_intent/allow/resume_existing_intent":                   8,
		"purchase_intent/block/request_key_conflict":                     1,
		"payment_attempt/verify/payment_attempt_in_progress":             2,
		"payment_attempt/review/payment_attempt_requires_reconciliation": 1,
	} {
		if outcomes[key] != want {
			t.Fatalf("checkout outcome %s = %d, want %d", key, outcomes[key], want)
		}
	}

	var receipts, reviewNotes, grants, bonus int
	var status string
	err = pool.QueryRow(ctx, `SELECT o.status,
		(SELECT count(*) FROM third_party_payment_orders p WHERE p.order_id=o.id),
		(SELECT count(*) FROM third_party_payment_orders p WHERE p.order_id=o.id AND p.reconciliation_note IS NOT NULL),
		(SELECT count(*) FROM payment_fulfillment_funding f JOIN third_party_payment_orders p ON p.id=f.receipt_id
		  WHERE f.order_id=o.id AND p.capture_id='CAPTURE-SECOND' AND f.principal_minor=999),
		(SELECT COALESCE(MAX(bonus_coins), 0) FROM wallets WHERE user_id=o.user_id)
		FROM recharge_orders o WHERE o.checkout_id=$1`, bookIntent).Scan(&status, &receipts, &reviewNotes, &grants, &bonus)
	if err != nil || status != "Success" || receipts != 2 || reviewNotes != 1 || grants != 1 || bonus != 0 {
		t.Fatalf("extra refund affected the original grant: status=%s receipts=%d review=%d grants=%d bonus=%d err=%v",
			status, receipts, reviewNotes, grants, bonus, err)
	}
}

func TestVIPReopenKeepsPaidPeriodAndHonorsOtherChannel(t *testing.T) {
	t.Setenv("CONVERSION_ENVIRONMENT", "sandbox")
	pool := testdb.Open(t)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `
		INSERT INTO users(id) VALUES(745);
		INSERT INTO user_subscriptions(user_id,subscription_id,plan_id,status,payment_method,current_period_end,last_payment_time)
		VALUES (745,'I-PAID-CANCELLED','P-ORIGINAL','CANCELLED','paypal',now()+interval '10 days',now()-interval '3 days'),
		       (745,'STRIPE-ACTIVE-MOCK','P-OTHER','ACTIVE','stripe',NULL,NULL);
		SELECT assert_paypal_identity('vip-checkout-test','sandbox');`); err != nil {
		t.Fatal(err)
	}
	seedTestProductAndSlot(t, pool, testCatalogSpec{
		SlotID: 7, TemplateID: 1, TemplateName: "Default", IsDefault: true, SlotIndex: 1,
		Code: "TEST_VIP_745", ProductType: "vip", VipCycle: "month", PriceCents: 999,
		Environment: "sandbox", PayPalAccountKey: "vip-checkout-test", PayPalPlanID: "P-NEW",
	})
	repo := NewDBRepository(wallet.NewDBRepository())
	testdb.ScopeSubscriptions(t, pool, "paypal", "sandbox", "I-PAID-CANCELLED")
	testdb.ScopeSubscriptions(t, pool, "stripe", "sandbox", "STRIPE-ACTIVE-MOCK")
	proposed := subscriptionIntent{userID: 745, slotID: 7, templateID: 1, priceCents: 999, cycle: "month",
		account:   payment.WebhookAccount{AccountKey: "vip-checkout-test", Environment: "sandbox"},
		returnURL: "https://reader.example.invalid/recharge", returnPath: "/content?novel_id=745"}
	if _, err := repo.reserveSubscriptionIntent(ctx, proposed); !errors.Is(err, ErrSubscriptionConflict) {
		t.Fatalf("new VIP ignored the other channel's renewing agreement: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE user_subscriptions SET status='UNKNOWN' WHERE subscription_id='STRIPE-ACTIVE-MOCK'`); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.reserveSubscriptionIntent(ctx, proposed); !errors.Is(err, ErrSubscriptionConflict) {
		t.Fatalf("unknown agreement did not reserve VIP checkout: %v", err)
	}

	if _, err := pool.Exec(ctx, `UPDATE user_subscriptions SET status='CANCELLED' WHERE subscription_id='STRIPE-ACTIVE-MOCK'`); err != nil {
		t.Fatal(err)
	}
	intent, err := repo.reserveSubscriptionIntent(ctx, proposed)
	if err != nil || intent.id == "" {
		t.Fatalf("cancelled but paid agreement blocked reopening: %+v %v", intent, err)
	}
	if _, err := repo.reserveSubscriptionIntent(ctx, proposed); !errors.Is(err, ErrSubscriptionPending) {
		t.Fatalf("in-flight subscription was not reused: %v", err)
	}
	var blocked, allowed, verify int
	if err := pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE outcome='block'),count(*) FILTER (WHERE outcome='allow'),
		count(*) FILTER (WHERE outcome='verify') FROM payment_checkout_decisions WHERE user_id=745 AND stage='subscription_intent'
		AND rule_version=1 AND provider='paypal'`).Scan(&blocked, &allowed, &verify); err != nil ||
		blocked != 2 || allowed != 1 || verify != 1 {
		t.Fatalf("VIP decision trail: blocked=%d allowed=%d verify=%d err=%v", blocked, allowed, verify, err)
	}

	if intent.planID != "P-NEW" {
		t.Fatalf("unbound VIP plan: %+v", intent)
	}
	if err := repo.finishSubscriptionIntent(ctx, intent, &payment.SubscriptionResult{SubscriptionID: "I-NEW-VIP", ApproveURL: "https://www.sandbox.paypal.com/test"}); err != nil {
		t.Fatal(err)
	}
	current, err := repo.getVIPPurchase(ctx, 745, intent.id, "I-NEW-VIP")
	if err != nil || current.Status != "confirming" || current.ReturnPath != proposed.returnPath {
		t.Fatalf("new unpaid subscription return: %+v %v", current, err)
	}
	old, err := repo.getVIPPurchase(ctx, 745, "", "I-PAID-CANCELLED")
	if err != nil || old.Status != "completed" {
		t.Fatalf("old paid VIP was affected: %+v %v", old, err)
	}
	for i, want := range []bool{true, false} {
		claimed, err := repo.claimVIPActivation(ctx, "I-NEW-VIP")
		if err != nil || claimed != want {
			t.Fatalf("activation %d: claimed=%t err=%v", i, claimed, err)
		}
	}
}

func TestRiskReviewDefersPayPalCheckoutUntilApproved(t *testing.T) {
	t.Setenv("CONVERSION_ENVIRONMENT", "sandbox")
	ctx := t.Context()
	pool := testdb.Open(t)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO users(id) VALUES(746);
		SELECT assert_paypal_identity('held-payment','sandbox');`)
	seedTestProductAndSlot(t, pool, testCatalogSpec{
		SlotID: 746, TemplateID: 746, TemplateName: "Held payment", IsDefault: true, SlotIndex: 1,
		Code: "TEST_HELD_746", ProductType: "single", PriceCents: 999, Coins: 20, Bonus: 4,
		Environment: "sandbox", PayPalAccountKey: "held-payment",
	})
	repo := NewDBRepository(wallet.NewDBRepository())
	intent, err := repo.preparePurchase(ctx, 746, purchaseRequest{key: uuid.New(), slotID: 746}, checkoutAttribution{})
	if err != nil {
		t.Fatal(err)
	}
	scope := payment.WebhookAccount{AccountKey: "held-payment", Environment: "sandbox"}
	attempt, err := repo.reservePayPalAttempt(ctx, 746, intent.ID, "", "", scope)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.finishPayPalAttempt(ctx, attempt, "ORDER-HELD", "https://www.sandbox.paypal.com"); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO payment_cases(account_id,kind,external_id,receipt_refs,provider_status,is_open,observed_at,work_state)
		SELECT id,'risk_review','REVIEW-HELD',ARRAY['CAPTURE-HELD'],'open',true,now(),'pending'
		FROM payment_channel_accounts WHERE account_key='held-payment'`)
	capture := &payment.OrderCapture{Account: scope, OrderID: "ORDER-HELD", CaptureID: "CAPTURE-HELD", CustomID: intent.ID,
		GrossAmount: "9.99", CurrencyCode: "USD", Status: "COMPLETED", RawPayload: "{}"}
	result, err := repo.settleCheckout(ctx, capture.OrderID, capture)
	if err != nil || result.Coins != 0 || result.settled {
		t.Fatalf("held result=%+v err=%v", result, err)
	}
	var status string
	var fulfilled bool
	var value int
	if err := pool.QueryRow(ctx, `SELECT status,fulfilled_at IS NOT NULL,(SELECT count(*) FROM transactions WHERE user_id=746)
		FROM recharge_orders WHERE checkout_id=$1`, intent.ID).Scan(&status, &fulfilled, &value); err != nil || status != "Paid" || fulfilled || value != 0 {
		t.Fatalf("held status=%s fulfilled=%t ledger=%d err=%v", status, fulfilled, value, err)
	}
	exec(`UPDATE payment_cases SET work_state='approved' WHERE external_id='REVIEW-HELD'`)
	result, err = repo.settleCheckout(ctx, capture.OrderID, capture)
	if err != nil || !result.settled || result.Coins != 24 {
		t.Fatalf("approved result=%+v err=%v", result, err)
	}
	if _, err := repo.settleCheckout(ctx, capture.OrderID, capture); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM transactions WHERE user_id=746 AND biz_type='recharge'`).Scan(&value); err != nil || value != 1 {
		t.Fatalf("duplicate fulfillment: ledger=%d err=%v", value, err)
	}
}
