package application

import (
	"errors"
	"testing"
	"time"

	"reader-backend/internal/novel"
	"reader-backend/internal/payment"
	"reader-backend/internal/testdb"
)

func TestStripeVIPInvoiceLifecycle(t *testing.T) {
	t.Setenv("CONVERSION_ENVIRONMENT", "sandbox")
	pool := testdb.Open(t)
	ctx := t.Context()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO users(id) VALUES(801),(802);
		INSERT INTO recharge_templates(id,name,is_default) VALUES(801,'VIP Stripe',true);
		INSERT INTO recharge_slots(id,template_id,slot_index,type,price,price_cents,coins,bonus,subscription_cycle)
		VALUES(801,801,1,'vip','10',1000,0,0,'month');
		UPDATE recharge_slots s SET vip_offer_version_id=snapshot_vip_offer(s) WHERE s.id=801;
		INSERT INTO payment_channel_accounts(provider,account_key,environment,credentials_ref,webhook_ref,new_subscriptions_enabled)
		VALUES('stripe','acct_StripeTest','sandbox','test','test',true);
		INSERT INTO payment_stripe_vip_offers(account_id,offer_version_id,product_ref,price_ref,sales_mode,allowed_countries,enabled)
		SELECT id,801,'prod_VIP','price_VIP','direct',ARRAY['US','CA'],true FROM payment_channel_accounts WHERE account_key='acct_StripeTest';`)
	account := payment.WebhookAccount{AccountKey: "acct_StripeTest", Environment: "sandbox"}
	req := stripeVIPRequest{userID: 801, slotID: 801, account: account, returnURL: "https://reader.example/recharge?payment=return",
		cancelURL: "https://reader.example/recharge?payment=cancel", attribution: checkoutAttribution{FBC: "fb.1.1740000000.fixture"}}
	v, err := reserveStripeVIP(ctx, req, true)
	if err != nil {
		t.Fatal(err)
	}
	if offers, err := stripeVIPSlots(ctx, account, 801); err != nil || len(offers) != 1 || offers[0] != 801 {
		t.Fatalf("visible offer: %v %v", offers, err)
	}
	if _, err := stripeVIPPurchase(ctx, 802, account, v.ID); !errors.Is(err, ErrCheckoutUnavailable) {
		t.Fatalf("second user found checkout: %v", err)
	}
	if _, err := (&dbRepository{}).reserveSubscriptionIntent(ctx, subscriptionIntent{userID: 801, slotID: 801}); !errors.Is(err, ErrSubscriptionConflict) {
		t.Fatalf("PayPal replaced an open Stripe VIP: %v", err)
	}
	due, err := claimStripeVIP(ctx, account, v.ID, true)
	if err != nil || len(due) != 1 || due[0].LookupOnly {
		t.Fatalf("Stripe VIP checkout claim: %+v %v", due, err)
	}
	if err := saveStripeVIPCheckout(ctx, *v, payment.StripeVIPCheckout{ID: "cs_VIP", IntentID: v.ID, URL: "https://checkout.stripe.com/vip"}); err != nil {
		t.Fatal(err)
	}
	if intent, err := stripeVIPPurchase(ctx, 801, account, v.ID); err != nil || intent.Status != "awaiting_approval" || intent.ApprovalURL == "" {
		t.Fatalf("return snapshot: %+v %v", intent, err)
	}
	proof := payment.StripeVIPSubscription{ID: "sub_VIP", CustomerID: "cus_VIP", IntentID: v.ID, PriceID: "price_VIP", Status: "active"}
	now := time.Now().UTC()
	start, end := now.Add(-time.Hour), now.AddDate(0, 1, 0).Add(-time.Hour)
	capture := func(id string, minor int64) payment.StripeVIPAllocation {
		return payment.StripeVIPAllocation{ID: "inpay_" + id, AmountMinor: minor, Receipt: payment.SingleChargeProof{
			ID: "ch_" + id, Currency: "USD", AmountMinor: minor, PaidAt: now, EligibleTerms: true, Evidence: []byte(`{}`),
		}}
	}
	first := payment.StripeVIPInvoice{ID: "in_First", Subscription: proof, BillingReason: "subscription_create", Currency: "USD", Country: "US",
		Status: "open", DueMinor: 1000, RemainingMinor: 600, StartsAt: start, EndsAt: end, Allocations: []payment.StripeVIPAllocation{capture("Part", 400)}}
	settle := func(invoice payment.StripeVIPInvoice) {
		t.Helper()
		if err := settleStripeVIPInvoice(ctx, *v, invoice, nil); err != nil {
			t.Fatal(err)
		}
	}
	access := func(want bool) {
		t.Helper()
		ok, err := novel.NewDBRepository().HasActiveSubscription(ctx, 801)
		if err != nil || ok != want {
			t.Fatalf("VIP access=%v error=%v want=%v", ok, err, want)
		}
	}
	settle(first)
	access(false)
	first.Status, first.RemainingMinor = "paid", 0
	first.Allocations = append(first.Allocations, capture("Remainder", 600))
	exec(`INSERT INTO admins(id,username,password_hash,role,status) VALUES('vip-risk-finance','vip-risk-finance','test','Finance',1);
		INSERT INTO payment_aftercare_policies(account_id,hold_fulfillment,reviewer_id,finance_id,review_seconds,refund_seconds,reminder_seconds)
		SELECT id,true,'vip-risk-finance','vip-risk-finance',120,240,3600 FROM payment_channel_accounts WHERE account_key='acct_StripeTest'`)
	for i := range first.Allocations {
		receipt := &first.Allocations[i].Receipt
		receipt.RiskReview = &payment.CaseFact{Provider: payment.ProviderStripe, Account: account, Kind: payment.CaseRiskReview,
			ExternalID: "prv_" + receipt.ID, SourceRef: receipt.ID, Source: "provider_api", Status: "open",
			Open: true, ObservedAt: now, ReceiptRefs: []string{receipt.ID}, Evidence: []byte(`{}`)}
	}
	settle(first)
	access(false)
	var holds int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM payment_deferred_fulfillment h JOIN payment_stripe_invoice_periods p
		ON p.order_id=h.order_id WHERE p.invoice_ref='in_First' AND h.resolved_at IS NULL AND h.case_id IS NOT NULL`).Scan(&holds); err != nil || holds != 2 {
		t.Fatalf("expected distinct Stripe risk holds, got %d: %v", holds, err)
	}
	exec(`UPDATE payment_cases SET work_state='approved' WHERE external_id='prv_ch_Part'`)
	settle(first)
	access(false)
	exec(`UPDATE payment_cases SET work_state='refund_due' WHERE external_id='prv_ch_Remainder'`)
	settle(first)
	access(false)
	exec(`UPDATE payment_cases SET work_state='approved' WHERE external_id='prv_ch_Remainder'`)
	errs := make(chan error, 2)
	for range 2 {
		go func() { errs <- settleStripeVIPInvoice(ctx, *v, first, nil) }()
	}
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	access(true)
	duplicate := first
	duplicate.ID, duplicate.Allocations = "in_Duplicate", []payment.StripeVIPAllocation{capture("Duplicate", 1000)}
	settle(duplicate)
	renewal := first
	renewal.ID, renewal.BillingReason, renewal.StartsAt, renewal.EndsAt = "in_Renewal", "subscription_cycle", end, end.AddDate(0, 1, 0)
	renewal.Subscription.Status = "past_due"
	renewal.Status, renewal.RemainingMinor, renewal.Allocations = "open", 1000, nil
	settle(renewal)
	access(true)
	renewal.Subscription.Status = "active"
	renewal.Status, renewal.RemainingMinor, renewal.Allocations = "paid", 0, []payment.StripeVIPAllocation{capture("Renewal", 1000)}
	settle(renewal)
	zero := renewal
	zero.ID, zero.DueMinor, zero.Allocations = "in_Zero", 0, nil
	zero.StartsAt, zero.EndsAt = renewal.EndsAt, renewal.EndsAt.AddDate(0, 1, 0)
	settle(zero)
	off := zero
	off.ID, off.DueMinor, off.ReviewReason = "in_OffStripe", 1000, "Stripe reports payment outside captured funds"
	settle(off)
	market := zero
	market.ID, market.DueMinor, market.Country, market.Allocations = "in_Unsupported", 1000, "GB", []payment.StripeVIPAllocation{capture("Unsupported", 1000)}
	settle(market)
	changed := market
	changed.ID, changed.Country, changed.Allocations = "in_Changed", "CA", []payment.StripeVIPAllocation{capture("Changed", 1000)}
	settle(changed)
	var changeReason string
	if err := pool.QueryRow(ctx, `SELECT review_reason FROM payment_stripe_invoice_periods WHERE invoice_ref='in_Changed'`).Scan(&changeReason); err != nil || changeReason != "Stripe billing country changed and needs verification" {
		t.Fatalf("billing country review: %q %v", changeReason, err)
	}
	var admitted, records, subscribe, kinds int
	if err := pool.QueryRow(ctx, `SELECT count(*) FILTER(WHERE admitted),count(*) FROM payment_stripe_invoice_periods`).Scan(&admitted, &records); err != nil || admitted != 2 || records != 7 {
		t.Fatalf("invoices=%d admitted=%d err=%v", records, admitted, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FILTER(WHERE event_name='Subscribe'),count(*) FILTER(WHERE snapshot->>'payment_kind' IN ('subscription_initial','subscription_renewal'))
		FROM reader_conversion_outbox`).Scan(&subscribe, &kinds); err != nil || subscribe != 1 || kinds != 3 {
		t.Fatalf("Stripe conversion subscribe=%d classified=%d error=%v", subscribe, kinds, err)
	}
	voided := zero
	voided.ID, voided.Status, voided.DueMinor, voided.RemainingMinor = "in_Voided", "void", 1000, 1000
	voided.StartsAt, voided.EndsAt = zero.EndsAt, zero.EndsAt.AddDate(0, 1, 0)
	settle(voided)
	reopenedInvoice := voided
	reopenedInvoice.ID, reopenedInvoice.Status, reopenedInvoice.RemainingMinor = "in_Replacement", "paid", 0
	reopenedInvoice.Allocations = []payment.StripeVIPAllocation{capture("Replacement", 1000)}
	settle(reopenedInvoice)
	if err := pool.QueryRow(ctx, `SELECT review_reason FROM payment_stripe_invoice_periods WHERE invoice_ref='in_Replacement'`).Scan(&changeReason); err != nil || changeReason != "Stripe VIP invoice revision needs verified original billing-period evidence" {
		t.Fatalf("unlinked invoice replacement: %q %v", changeReason, err)
	}
	reopenedInvoice.RevisesInvoiceID, reopenedInvoice.RevisionVerified = voided.ID, true
	settle(reopenedInvoice)
	settle(reopenedInvoice)
	var revisionVerified bool
	if err := pool.QueryRow(ctx, `SELECT admitted AND revision_verified FROM payment_stripe_invoice_periods WHERE invoice_ref='in_Replacement' AND revises_invoice_ref='in_Voided'`).Scan(&revisionVerified); err != nil || !revisionVerified {
		t.Fatalf("verified replacement not admitted: %v %v", revisionVerified, err)
	}
	for _, refund := range []payment.StripeRefundProof{
		{ID: "re_First", ChargeID: "ch_Part", AmountMinor: 400, Currency: "USD", Status: "succeeded"},
		{ID: "re_Second", ChargeID: "ch_Remainder", AmountMinor: 600, Currency: "USD", Status: "succeeded"},
	} {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := applyStripeRefundTx(ctx, tx, account, &refund); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatal(err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		access(refund.ID == "re_First")
	}
	owner, err := stripeVIPForOwner(ctx, account, 801, proof.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := queueStripeVIPCancellation(ctx, owner, "reader requested"); err != nil {
		t.Fatal(err)
	}
	if _, err := reserveStripeVIP(ctx, req, true); !errors.Is(err, ErrSubscriptionConflict) {
		t.Fatalf("opened while cancellation pending: %v", err)
	}
	jobs, err := claimStripeVIPCancellations(ctx, account)
	if err != nil || len(jobs) != 1 || !jobs[0].maySend || jobs[0].key == "" {
		t.Fatalf("cancel lease=%+v err=%v", jobs, err)
	}
	stale := proof
	stale.ObservedAt = time.Now().Add(-time.Minute)
	proof.CancelAtPeriodEnd = true
	if err := bindStripeVIP(ctx, owner, proof); err != nil {
		t.Fatal(err)
	}
	if err := bindStripeVIP(ctx, owner, stale); err != nil {
		t.Fatal(err)
	}
	var actual time.Time
	if err := pool.QueryRow(ctx, `SELECT current_period_end FROM user_subscriptions WHERE subscription_id='sub_VIP' AND status='CANCELLED'`).Scan(&actual); err != nil || actual.Sub(reopenedInvoice.EndsAt).Abs() > time.Millisecond {
		t.Fatalf("cancel preserved period end=%v error=%v", actual, err)
	}
	if reopened, err := reserveStripeVIP(ctx, req, true); err != nil || reopened.ID == v.ID {
		t.Fatalf("confirmed cancellation should allow immediate re-enrollment: %+v %v", reopened, err)
	}

	exec(`UPDATE payment_stripe_vip_offers SET sales_mode='merchant_of_record',tax_liability_account='acct_Liability'`)
	req.userID = 802
	managed, err := reserveStripeVIP(ctx, req, true)
	if err != nil {
		t.Fatal(err)
	}
	managedInvoice := first
	managedInvoice.ID = "in_Managed"
	managedInvoice.DueMinor = 1100
	managedInvoice.PrincipalMinor = 1000
	managedInvoice.Subscription = payment.StripeVIPSubscription{
		ID: "sub_Managed", CustomerID: "cus_Managed", IntentID: managed.ID, PriceID: managed.Offer.PriceID,
		Status: "active", ManagedPayments: true,
	}
	managedInvoice.Allocations = []payment.StripeVIPAllocation{capture("Managed", 1100)}
	admittedManaged := func(invoice payment.StripeVIPInvoice, expect bool) {
		t.Helper()
		if err := settleStripeVIPInvoice(ctx, *managed, invoice, nil); err != nil {
			t.Fatal(err)
		}
		var got bool
		if err := pool.QueryRow(ctx, `SELECT admitted FROM payment_stripe_invoice_periods WHERE invoice_ref=$1`, invoice.ID).Scan(&got); err != nil || got != expect {
			t.Fatalf("Managed Payments admitted=%v want=%v err=%v", got, expect, err)
		}
	}
	admittedManaged(managedInvoice, false)
	managedInvoice.TaxLiabilityAccount = "acct_Liability"
	managedInvoice.CustomerTaxStatus = "none"
	managedInvoice.TaxVerified = true
	differentCurrency := managedInvoice
	differentCurrency.ID = "in_ManagedFX"
	differentCurrency.Allocations = []payment.StripeVIPAllocation{capture("ManagedFX", 1100)}
	differentCurrency.Allocations[0].Receipt.PresentmentCurrency = "CAD"
	admittedManaged(differentCurrency, false)
	managedInvoice.Allocations[0].Receipt.PresentmentCurrency = "USD"
	admittedManaged(managedInvoice, true)
	assertManagedPrincipal := func() {
		t.Helper()
		var principal int64
		if err := pool.QueryRow(ctx, `SELECT f.principal_minor FROM payment_fulfillment_funding f
			JOIN payment_stripe_invoice_periods p ON p.order_id=f.order_id WHERE p.invoice_ref=$1`, managedInvoice.ID).Scan(&principal); err != nil || principal != 1000 {
			t.Fatalf("managed principal=%d error=%v", principal, err)
		}
	}
	assertManagedPrincipal()
	exec(`UPDATE payment_fulfillment_funding f SET principal_minor=NULL
		FROM payment_stripe_invoice_periods p WHERE p.order_id=f.order_id AND p.invoice_ref='in_Managed'`)
	admittedManaged(managedInvoice, true)
	assertManagedPrincipal()
	for _, refund := range []struct {
		id, state string
		amount    int64
	}{
		{"re_ManagedPrincipal", "Success", 1000},
		{"re_ManagedTax", "Refunded", 100},
	} {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		proof := payment.StripeRefundProof{ID: refund.id, ChargeID: "ch_Managed", Currency: "USD", Status: "succeeded", AmountMinor: refund.amount}
		if err := applyStripeRefundTx(ctx, tx, account, &proof); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatal(err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		var state string
		if err := pool.QueryRow(ctx, `SELECT status FROM recharge_orders WHERE external_ref_id=$1`, managedInvoice.ID).Scan(&state); err != nil || state != refund.state {
			t.Fatalf("managed refund %s: order=%q error=%v want=%q", refund.id, state, err, refund.state)
		}
	}

	exec(`UPDATE payment_stripe_market_reviews SET approved_at=now(),approval_reason='finance fixture'
		WHERE order_id=(SELECT order_id FROM payment_stripe_invoice_periods WHERE invoice_ref='in_Changed')`)
	settle(changed)
	var countryApproved bool
	if err := pool.QueryRow(ctx, `SELECT admitted FROM payment_stripe_invoice_periods WHERE invoice_ref='in_Changed'`).Scan(&countryApproved); err != nil || !countryApproved {
		t.Fatalf("reviewed invoice did not fulfill: %v %v", countryApproved, err)
	}

}
