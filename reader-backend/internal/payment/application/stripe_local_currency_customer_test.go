package application

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"reader-backend/internal/payment"
	"reader-backend/internal/testdb"
	"reader-backend/internal/wallet"
)

type localCurrencyStripeFixture struct {
	account       payment.WebhookAccount
	mu            sync.Mutex
	singleCreates []payment.SingleCheckoutRequest
	vipCreates    []payment.StripeVIPCheckoutRequest
	customerCalls int32
	customerKeys  []string
	readSingleFn  func(context.Context, string, string, payment.SingleCheckoutOffer) (payment.SingleCheckoutProof, error)
	readVIPInvFn  func(context.Context, string, payment.StripeVIPOffer) (payment.StripeVIPInvoice, error)
	ledgerFn      func(context.Context, string) (payment.LedgerEvidence, error)
}

func (f *localCurrencyStripeFixture) Provider() string                     { return payment.ProviderStripe }
func (f *localCurrencyStripeFixture) AccountScope() payment.WebhookAccount { return f.account }

func (f *localCurrencyStripeFixture) CreateCustomer(_ context.Context, userID int64, _ string, idempotencyKey string) (string, error) {
	atomic.AddInt32(&f.customerCalls, 1)
	f.mu.Lock()
	f.customerKeys = append(f.customerKeys, idempotencyKey)
	f.mu.Unlock()
	return "cus_Concurrent01", nil
}

func (f *localCurrencyStripeFixture) CreateSingleCheckout(_ context.Context, req payment.SingleCheckoutRequest) (payment.SingleCheckoutProof, error) {
	f.mu.Lock()
	f.singleCreates = append(f.singleCreates, req)
	f.mu.Unlock()
	return payment.SingleCheckoutProof{
		ID:            "cs_" + req.AttemptID[:8],
		AttemptID:     req.AttemptID,
		URL:           "https://checkout.stripe.com/c/pay/cs_test",
		AwaitingBuyer: true,
	}, nil
}

func (f *localCurrencyStripeFixture) FindSingleCheckout(context.Context, string, time.Time, time.Time) (string, error) {
	return "", nil
}

func (f *localCurrencyStripeFixture) ReadSingleCheckout(ctx context.Context, id, attempt string, offer payment.SingleCheckoutOffer) (payment.SingleCheckoutProof, error) {
	return f.readSingleFn(ctx, id, attempt, offer)
}

func (f *localCurrencyStripeFixture) GetLedgerMovement(ctx context.Context, id string) (payment.LedgerEvidence, error) {
	return f.ledgerFn(ctx, id)
}

func (f *localCurrencyStripeFixture) CreateStripeVIPCheckout(_ context.Context, req payment.StripeVIPCheckoutRequest) (payment.StripeVIPCheckout, error) {
	f.mu.Lock()
	f.vipCreates = append(f.vipCreates, req)
	f.mu.Unlock()
	return payment.StripeVIPCheckout{
		ID:            "cs_vip_" + req.IntentID[:8],
		IntentID:      req.IntentID,
		URL:           "https://checkout.stripe.com/c/pay/cs_vip",
		AwaitingBuyer: true,
	}, nil
}

func (f *localCurrencyStripeFixture) FindStripeVIPCheckout(context.Context, string, time.Time, time.Time) (string, error) {
	return "", nil
}
func (f *localCurrencyStripeFixture) ReadStripeVIPCheckout(context.Context, string, string, string) (payment.StripeVIPCheckout, error) {
	return payment.StripeVIPCheckout{}, nil
}
func (f *localCurrencyStripeFixture) ReadStripeVIPSubscription(context.Context, string) (payment.StripeVIPSubscription, error) {
	return payment.StripeVIPSubscription{}, nil
}
func (f *localCurrencyStripeFixture) StripeVIPInvoiceRefs(context.Context, string, string) ([]string, string, error) {
	return nil, "", nil
}
func (f *localCurrencyStripeFixture) StripeInvoiceSubscription(context.Context, string) (string, error) {
	return "", nil
}
func (f *localCurrencyStripeFixture) ReadStripeVIPInvoice(ctx context.Context, id string, offer payment.StripeVIPOffer) (payment.StripeVIPInvoice, error) {
	return f.readVIPInvFn(ctx, id, offer)
}
func (f *localCurrencyStripeFixture) ScheduleStripeVIPCancellation(context.Context, string, string) (payment.StripeVIPSubscription, error) {
	return payment.StripeVIPSubscription{}, nil
}
func (f *localCurrencyStripeFixture) OpenStripeVIPPortal(context.Context, string, string, string) (string, error) {
	return "", nil
}

func TestStripeSingleLocalCurrencyPendingSettlementRetryAndReview(t *testing.T) {
	t.Setenv("CONVERSION_ENVIRONMENT", "sandbox")
	pool := testdb.Open(t)
	ctx := t.Context()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO users(id, email) VALUES (701, 'u701@example.com');
		SELECT assert_paypal_identity('pp-local-test','sandbox');
		INSERT INTO payment_channel_accounts(provider,account_key,environment,credentials_ref,webhook_ref,status,is_primary,new_orders_enabled,adaptive_pricing_enabled)
		VALUES('stripe','acct_Local1','sandbox','test','test','published',true,true,true)`)
	seedTestProductAndSlot(t, pool, testCatalogSpec{
		SlotID: 701, TemplateID: 701, TemplateName: "LocalCoins", IsDefault: true, SlotIndex: 1,
		Code: "COINS_701", ProductType: "single", PriceCents: 999, Coins: 1000, Bonus: 100,
		Environment: "sandbox", PayPalAccountKey: "pp-local-test",
		StripeAccountKey: "acct_Local1", StripeProductRef: "prod_Coins701", StripePriceRef: "price_Coins701",
		AllowedCountries: []string{"US", "GB", "CA", "JP"},
	})

	account := payment.WebhookAccount{AccountKey: "acct_Local1", Environment: "sandbox"}
	repo := NewDBRepository(wallet.NewDBRepository())
	fixture := &localCurrencyStripeFixture{account: account}
	svc := &service{repo: repo, adapters: payment.NewAdapters(fixture)}

	// 1. Frozen attempt params remain stable across retries even if account toggles change.
	intent, err := repo.preparePurchase(ctx, 701, purchaseRequest{key: uuid.New(), slotID: 701}, checkoutAttribution{FBC: "fb.1.1740000000.local"})
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := repo.reserveStripePurchase(ctx, 701, intent.ID, "https://reader.example.invalid/ok", "https://reader.example.invalid/cancel", account)
	if err != nil {
		t.Fatal(err)
	}
	due, err := repo.claimStripeAttempts(ctx, account, attempt.ID, true)
	if err != nil || len(due) != 1 {
		t.Fatalf("claimStripeAttempts = %+v, %v", due, err)
	}
	if err := svc.syncStripeAttempt(ctx, fixture, due[0], ""); err != nil {
		t.Fatal(err)
	}
	if len(fixture.singleCreates) != 1 || fixture.singleCreates[0].Params == nil || !fixture.singleCreates[0].Params.AdaptivePricingEnabled {
		t.Fatalf("expected frozen adaptive pricing enabled on first write: %+v", fixture.singleCreates)
	}
	firstParams := *fixture.singleCreates[0].Params
	// Flip account toggles after first write and verify stored frozen params stay unchanged.
	exec(`UPDATE payment_channel_accounts SET adaptive_pricing_enabled=false, saved_payment_method_enabled=true, simplified_address_collection_enabled=true WHERE account_key='acct_Local1'`)
	reloaded, err := loadStripeAttempt(ctx, pool, account, attempt.ID)
	if err != nil || reloaded.FrozenParams == nil || *reloaded.FrozenParams != firstParams {
		t.Fatalf("frozen params drifted after account toggle change: got=%+v want=%+v err=%v", reloaded.FrozenParams, firstParams, err)
	}

	// 2. Local currency (JPY zero-decimal) with missing BalanceTransaction -> paid-pending-settlement, entitlement deferred.
	jpyAmt := int64(1500)
	var currentMovementID string
	var ledgerErr error
	var ledgerEntry payment.LedgerEvidence
	fixture.readSingleFn = func(_ context.Context, id, att string, _ payment.SingleCheckoutOffer) (payment.SingleCheckoutProof, error) {
		return payment.SingleCheckoutProof{
			ID:        id,
			AttemptID: att,
			Receipt: &payment.SingleChargeProof{
				ID:                     "ch_JPY1",
				IntentID:               "pi_JPY1",
				Currency:               payment.CurrencyUSD,
				CustomerID:             "cus_Concurrent01",
				Country:                "JP",
				AmountMinor:            999,
				PrincipalMinor:         999,
				PaidAt:                 time.Now().UTC(),
				EligibleTerms:          true,
				TaxVerified:            true,
				PresentmentCurrency:    "JPY",
				PresentmentAmountMinor: &jpyAmt,
				MovementID:             currentMovementID,
				Evidence:               json.RawMessage(`{"presentment":"JPY"}`),
			},
		}, nil
	}
	fixture.ledgerFn = func(_ context.Context, _ string) (payment.LedgerEvidence, error) {
		return ledgerEntry, ledgerErr
	}

	// Missing BalanceTransaction (MovementID == "")
	if err := svc.syncStripeAttempt(ctx, fixture, reloaded, "cs_"+attempt.ID[:8]); err != nil {
		t.Fatal(err)
	}
	var orderStatus, settlementState, presCurrency string
	var presAmount int64
	var fulfilled bool
	if err := pool.QueryRow(ctx, `SELECT o.status, o.fulfilled_at IS NOT NULL, t.settlement_state, COALESCE(r.presentment_currency,''), COALESCE(r.presentment_amount_minor,0)
		FROM recharge_orders o
		JOIN payment_stripe_checkout_terms t ON t.attempt_id=$1
		JOIN third_party_payment_orders r ON r.order_id=o.id
		WHERE o.id=$2`, attempt.ID, attempt.OrderID).Scan(&orderStatus, &fulfilled, &settlementState, &presCurrency, &presAmount); err != nil {
		t.Fatal(err)
	}
	if orderStatus != "Paid" || fulfilled || settlementState != payment.SettlementStatePending || presCurrency != "JPY" || presAmount != 1500 {
		t.Fatalf("pending settlement state mismatch: status=%q fulfilled=%v state=%q pres=%s/%d", orderStatus, fulfilled, settlementState, presCurrency, presAmount)
	}

	// 3. Transient fetch timeout keeps pending_settlement; disabling new_orders_enabled does NOT block retrying existing pending-settlement attempt!
	exec(`UPDATE payment_channel_accounts SET new_orders_enabled=false WHERE account_key='acct_Local1'`)
	exec(`UPDATE payment_checkout_attempts SET next_create_at=now()-interval '1 minute' WHERE id=$1`, attempt.ID)
	claimedPending, err := repo.claimStripeAttempts(ctx, account, "", false)
	if err != nil || len(claimedPending) != 1 || claimedPending[0].ID != attempt.ID {
		t.Fatalf("claimStripeAttempts must claim pending_settlement even when new entries disabled: %+v %v", claimedPending, err)
	}
	currentMovementID = "txn_JPY1"
	ledgerErr = errors.New("stripe timeout")
	if err := svc.syncStripeAttempt(ctx, fixture, claimedPending[0], ""); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT fulfilled_at IS NOT NULL FROM recharge_orders WHERE id=$1`, attempt.OrderID).Scan(&fulfilled); err != nil || fulfilled {
		t.Fatalf("order must stay unfulfilled on transient timeout: fulfilled=%v err=%v", fulfilled, err)
	}

	// 4. Retry succeeds with verified USD BalanceTransaction -> fulfills once and records settlement + CAPI in USD.
	ledgerErr = nil
	ledgerEntry = payment.LedgerEvidence{
		Provider:     payment.ProviderStripe,
		Account:      account,
		MovementKey:  "txn_JPY1",
		Source:       payment.LedgerStripeBalance,
		SourceRef:    "txn_JPY1",
		Payload:      json.RawMessage(`{"id":"txn_JPY1"}`),
		Kind:         payment.MovementPayment,
		ProviderCode: "charge",
		Currency:     payment.CurrencyUSD,
		ExchangeRate: "0.666",
		OccurredAt:   time.Now().UTC(),
		Amounts: map[payment.LedgerComponent]payment.LedgerMoney{
			payment.LedgerGross:       {Currency: payment.CurrencyUSD, Minor: 999},
			payment.LedgerFee:         {Currency: payment.CurrencyUSD, Minor: -59},
			payment.LedgerWithheldTax: {Currency: payment.CurrencyUSD, Minor: 0},
			payment.LedgerNet:         {Currency: payment.CurrencyUSD, Minor: 940},
		},
	}
	exec(`UPDATE payment_checkout_attempts SET next_create_at=now()-interval '1 minute' WHERE id=$1`, attempt.ID)
	claimedRetry, err := repo.claimStripeAttempts(ctx, account, "", false)
	if err != nil || len(claimedRetry) != 1 {
		t.Fatalf("expected pending settlement claim for retry: %+v %v", claimedRetry, err)
	}
	// Run twice to verify duplicate notification idempotency.
	for range 2 {
		if err := svc.syncStripeAttempt(ctx, fixture, claimedRetry[0], ""); err != nil {
			t.Fatal(err)
		}
	}
	var coins, bonus int
	var capiCount int
	var settledGross, settledNet int64
	var settledRate string
	if err := pool.QueryRow(ctx, `SELECT o.status, o.fulfilled_at IS NOT NULL, t.settlement_state, t.settlement_gross_minor, t.settlement_net_minor, t.settlement_exchange_rate,
		(SELECT COALESCE(charged_coins,0) FROM wallets WHERE user_id=701),
		(SELECT COALESCE(bonus_coins,0) FROM wallets WHERE user_id=701),
		(SELECT count(*) FROM reader_conversion_outbox WHERE event_name='Purchase' AND order_id=o.id)
		FROM recharge_orders o JOIN payment_stripe_checkout_terms t ON t.attempt_id=$1 WHERE o.id=$2`, attempt.ID, attempt.OrderID).
		Scan(&orderStatus, &fulfilled, &settlementState, &settledGross, &settledNet, &settledRate, &coins, &bonus, &capiCount); err != nil {
		t.Fatal(err)
	}
	if orderStatus != "Success" || !fulfilled || settlementState != payment.SettlementStateSettled ||
		settledGross != 999 || settledNet != 940 || settledRate != "0.666" ||
		coins != 1000 || bonus != 100 || capiCount != 1 {
		t.Fatalf("unexpected settled result: status=%q fulfilled=%v state=%q gross=%d net=%d rate=%q coins=%d/%d capi=%d",
			orderStatus, fulfilled, settlementState, settledGross, settledNet, settledRate, coins, bonus, capiCount)
	}

	// 5. Currency mismatch, amount mismatch, and retry-window exhaustion all route to review without fulfillment.
	exec(`UPDATE payment_channel_accounts SET new_orders_enabled=true WHERE account_key='acct_Local1'`)
	for _, tc := range []struct {
		name      string
		chargeID  string
		expireWin bool
		entry     payment.LedgerEvidence
		wantNote  string
	}{
		{
			name:     "currency mismatch",
			chargeID: "ch_BadCurr",
			entry: payment.LedgerEvidence{
				Provider: payment.ProviderStripe, Account: account, MovementKey: "txn_BadCurr",
				Source: payment.LedgerStripeBalance, SourceRef: "txn_BadCurr", Payload: json.RawMessage(`{}`),
				Kind: payment.MovementPayment, Currency: "GBP", OccurredAt: time.Now().UTC(),
				Amounts: map[payment.LedgerComponent]payment.LedgerMoney{payment.LedgerGross: {Currency: "GBP", Minor: 999}},
			},
			wantNote: stripeNoteSettlementCurrencyMismatch,
		},
		{
			name:     "amount mismatch",
			chargeID: "ch_BadAmt",
			entry: payment.LedgerEvidence{
				Provider: payment.ProviderStripe, Account: account, MovementKey: "txn_BadAmt",
				Source: payment.LedgerStripeBalance, SourceRef: "txn_BadAmt", Payload: json.RawMessage(`{}`),
				Kind: payment.MovementPayment, Currency: payment.CurrencyUSD, OccurredAt: time.Now().UTC(),
				Amounts: map[payment.LedgerComponent]payment.LedgerMoney{payment.LedgerGross: {Currency: payment.CurrencyUSD, Minor: 888}},
			},
			wantNote: stripeNoteSettlementAmountMismatch,
		},
		{
			name:      "retry window exhausted",
			chargeID:  "ch_Expired",
			expireWin: true,
			wantNote:  stripeNoteSettlementWindowExhausted,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			i2, err := repo.preparePurchase(ctx, 701, purchaseRequest{key: uuid.New(), slotID: 701}, checkoutAttribution{})
			if err != nil {
				t.Fatal(err)
			}
			a2, err := repo.reserveStripePurchase(ctx, 701, i2.ID, "https://reader.example.invalid/ok", "https://reader.example.invalid/cancel", account)
			if err != nil {
				t.Fatal(err)
			}
			if tc.expireWin {
				exec(`UPDATE payment_stripe_checkout_terms SET settlement_retry_until=now()-interval '1 minute' WHERE attempt_id=$1`, a2.ID)
			}
			gbpAmt := int64(799)
			p2 := payment.SingleCheckoutProof{
				ID: "cs_" + tc.chargeID, AttemptID: a2.ID,
				Receipt: &payment.SingleChargeProof{
					ID: tc.chargeID, IntentID: "pi_" + tc.chargeID, Currency: payment.CurrencyUSD, Country: "GB",
					AmountMinor: 999, PrincipalMinor: 999, PaidAt: time.Now().UTC(), EligibleTerms: true, TaxVerified: true,
					PresentmentCurrency: "GBP", PresentmentAmountMinor: &gbpAmt, Evidence: json.RawMessage(`{}`),
				},
			}
			var ver stripeSettlementVerification
			if tc.expireWin {
				ver = stripeSettlementVerification{Outcome: settlementOutcomePending}
			} else {
				ver = settlementVerificationFromMovement(p2.Receipt, &tc.entry)
			}
			if err := repo.applyStripeProofWithSettlement(ctx, *a2, p2, ver); err != nil {
				t.Fatal(err)
			}
			var gotState, gotNote string
			var gotFulfilled bool
			if err := pool.QueryRow(ctx, `SELECT o.fulfilled_at IS NOT NULL, COALESCE(o.reconciliation_note,''), t.settlement_state
				FROM recharge_orders o JOIN payment_stripe_checkout_terms t ON t.attempt_id=$1 WHERE o.id=$2`, a2.ID, a2.OrderID).
				Scan(&gotFulfilled, &gotNote, &gotState); err != nil {
				t.Fatal(err)
			}
			if gotFulfilled || gotState != payment.SettlementStateReview || gotNote != tc.wantNote {
				t.Fatalf("got fulfilled=%v state=%q note=%q, want false/review/%q", gotFulfilled, gotState, gotNote, tc.wantNote)
			}
		})
	}
}

func TestStripeVIPMultiAllocationLocalCurrencyPendingAndRetry(t *testing.T) {
	t.Setenv("CONVERSION_ENVIRONMENT", "sandbox")
	pool := testdb.Open(t)
	ctx := t.Context()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO users(id, email) VALUES (702, 'u702@example.com');
		SELECT assert_paypal_identity('pp-vip-local','sandbox');
		INSERT INTO payment_channel_accounts(provider,account_key,environment,credentials_ref,webhook_ref,status,is_primary,new_subscriptions_enabled)
		VALUES('stripe','acct_VIPLocal','sandbox','test','test','published',true,true)`)
	seedTestProductAndSlot(t, pool, testCatalogSpec{
		SlotID: 702, TemplateID: 702, TemplateName: "LocalVIP", IsDefault: true, SlotIndex: 1,
		Code: "VIP_702", ProductType: "vip", PriceCents: 999, VipCycle: "month",
		Environment: "sandbox", PayPalAccountKey: "pp-vip-local",
		StripeAccountKey: "acct_VIPLocal", StripeProductRef: "prod_VIP702", StripePriceRef: "price_VIP702",
		StripeSalesMode: payment.StripeSaleMoR, AllowedCountries: []string{"GB", "CA", "JP"},
	})

	account := payment.WebhookAccount{AccountKey: "acct_VIPLocal", Environment: "sandbox"}
	v, err := reserveStripeVIP(ctx, stripeVIPRequest{
		userID: 702, slotID: 702, returnURL: "https://reader.example.invalid/ok", cancelURL: "https://reader.example.invalid/cancel",
		account: account,
	}, true)
	if err != nil {
		t.Fatal(err)
	}

	cad1, cad2 := int64(800), int64(700)
	start := time.Now().UTC().Truncate(time.Second)
	end := start.Add(30 * 24 * time.Hour)
	proof := payment.StripeVIPInvoice{
		ID: "in_LocalVIP1", Country: "CA", Currency: payment.CurrencyUSD, BillingReason: "subscription_create",
		TaxLiabilityType: "account", TaxLiabilityAccount: "acct_StripeMoR01", AutomaticTaxStatus: "complete",
		CustomerTaxStatus: "none", TaxVerified: true,
		Subscription: payment.StripeVIPSubscription{
			ID: "sub_LocalVIP1", IntentID: v.ID, CustomerID: "cus_VIP702", PriceID: "price_VIP702",
			Status: "active", ManagedPayments: true, ObservedAt: time.Now().UTC(),
		},
		Status: "paid", DueMinor: 1099, RemainingMinor: 0, PrincipalMinor: 999,
		StartsAt: start, EndsAt: end,
		Allocations: []payment.StripeVIPAllocation{
			{
				ID: "inpay_A1", AmountMinor: 600,
				Receipt: payment.SingleChargeProof{
					ID: "ch_A1", IntentID: "pi_A1", Currency: payment.CurrencyUSD, CustomerID: "cus_VIP702",
					AmountMinor: 600, PaidAt: start, EligibleTerms: true,
					PresentmentCurrency: "CAD", PresentmentAmountMinor: &cad1, MovementID: "txn_A1",
					Evidence: json.RawMessage(`{"alloc":1}`),
				},
			},
			{
				ID: "inpay_A2", AmountMinor: 499,
				Receipt: payment.SingleChargeProof{
					ID: "ch_A2", IntentID: "pi_A2", Currency: payment.CurrencyUSD, CustomerID: "cus_VIP702",
					AmountMinor: 499, PaidAt: start.Add(time.Second), EligibleTerms: true,
					PresentmentCurrency: "CAD", PresentmentAmountMinor: &cad2, MovementID: "txn_A2",
					Evidence: json.RawMessage(`{"alloc":2}`),
				},
			},
		},
	}

	secondReady := false
	fixture := &localCurrencyStripeFixture{
		account: account,
		readVIPInvFn: func(_ context.Context, _ string, _ payment.StripeVIPOffer) (payment.StripeVIPInvoice, error) {
			return proof, nil
		},
		ledgerFn: func(_ context.Context, id string) (payment.LedgerEvidence, error) {
			if id == "txn_A1" {
				return payment.LedgerEvidence{
					Provider: payment.ProviderStripe, Account: account, MovementKey: "txn_A1",
					Source: payment.LedgerStripeBalance, SourceRef: "txn_A1", Payload: json.RawMessage(`{}`),
					Kind: payment.MovementPayment, Currency: payment.CurrencyUSD, ExchangeRate: "0.75",
					OccurredAt: start,
					Amounts: map[payment.LedgerComponent]payment.LedgerMoney{
						payment.LedgerGross:       {Currency: payment.CurrencyUSD, Minor: 600},
						payment.LedgerFee:         {Currency: payment.CurrencyUSD, Minor: -80},
						payment.LedgerWithheldTax: {Currency: payment.CurrencyUSD, Minor: -55},
						payment.LedgerNet:         {Currency: payment.CurrencyUSD, Minor: 520},
					},
				}, nil
			}
			if !secondReady {
				return payment.LedgerEvidence{}, errors.New("balance transaction pending")
			}
			return payment.LedgerEvidence{
				Provider: payment.ProviderStripe, Account: account, MovementKey: "txn_A2",
				Source: payment.LedgerStripeBalance, SourceRef: "txn_A2", Payload: json.RawMessage(`{}`),
				Kind: payment.MovementPayment, Currency: payment.CurrencyUSD, ExchangeRate: "0.7128",
				OccurredAt: start,
				Amounts: map[payment.LedgerComponent]payment.LedgerMoney{
					payment.LedgerGross:       {Currency: payment.CurrencyUSD, Minor: 499},
					payment.LedgerFee:         {Currency: payment.CurrencyUSD, Minor: -70},
					payment.LedgerWithheldTax: {Currency: payment.CurrencyUSD, Minor: -45},
					payment.LedgerNet:         {Currency: payment.CurrencyUSD, Minor: 429},
				},
			}, nil
		},
	}
	svc := &service{repo: NewDBRepository(wallet.NewDBRepository()), adapters: payment.NewAdapters(fixture)}

	// First pass: second allocation pending -> invoice stays pending_settlement, unfulfilled.
	if err := svc.syncStripeVIPInvoice(ctx, fixture, *v, "in_LocalVIP1", "sub_LocalVIP1"); err != nil {
		t.Fatal(err)
	}
	var admitted bool
	var invState string
	if err := pool.QueryRow(ctx, `SELECT admitted, settlement_state FROM payment_stripe_invoice_periods WHERE invoice_ref='in_LocalVIP1'`).
		Scan(&admitted, &invState); err != nil {
		t.Fatal(err)
	}
	if admitted || invState != payment.SettlementStatePending {
		t.Fatalf("expected pending_settlement and unadmitted, got admitted=%v state=%q", admitted, invState)
	}

	// Claim pending-settlement VIP invoice and retry once second BalanceTransaction is ready.
	exec(`UPDATE payment_stripe_invoice_periods SET settlement_next_check_at=now()-interval '1 minute' WHERE invoice_ref='in_LocalVIP1'`)
	pending, err := claimStripeVIPPendingSettlementInvoices(ctx, account)
	if err != nil || len(pending) != 1 || pending[0].invoiceRef != "in_LocalVIP1" {
		t.Fatalf("claimStripeVIPPendingSettlementInvoices = %+v, %v", pending, err)
	}
	secondReady = true
	if err := svc.syncStripeVIPInvoice(ctx, fixture, *v, pending[0].invoiceRef, pending[0].subscriptionRef); err != nil {
		t.Fatal(err)
	}
	var totalPrincipal int64
	var receiptCount int
	var customerVerified bool
	if err := pool.QueryRow(ctx, `SELECT p.admitted, p.settlement_state,
		(SELECT COALESCE(sum(principal_minor),0) FROM payment_fulfillment_funding WHERE order_id=p.order_id),
		(SELECT count(*) FROM third_party_payment_orders WHERE order_id=p.order_id AND presentment_currency='CAD'),
		(SELECT tax_evidence_verified FROM payment_stripe_customers WHERE user_id=702 AND stripe_customer_id='cus_VIP702')
		FROM payment_stripe_invoice_periods p WHERE p.invoice_ref='in_LocalVIP1'`).
		Scan(&admitted, &invState, &totalPrincipal, &receiptCount, &customerVerified); err != nil {
		t.Fatal(err)
	}
	if !admitted || invState != payment.SettlementStateSettled || totalPrincipal != 999 || receiptCount != 2 || !customerVerified {
		t.Fatalf("unexpected settled VIP invoice: admitted=%v state=%q principal=%d receipts=%d customerVerified=%v",
			admitted, invState, totalPrincipal, receiptCount, customerVerified)
	}
}

func TestStripeCustomerConcurrentIdempotencyAndAddressSimplification(t *testing.T) {
	t.Setenv("CONVERSION_ENVIRONMENT", "sandbox")
	pool := testdb.Open(t)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id, email) VALUES (703, 'u703@example.com');
		INSERT INTO payment_channel_accounts(provider,account_key,environment,credentials_ref,webhook_ref,status,is_primary,saved_payment_method_enabled,simplified_address_collection_enabled)
		VALUES('stripe','acct_Cust703','sandbox','test','test','published',true,true,true)`); err != nil {
		t.Fatal(err)
	}
	var accountID int64
	if err := pool.QueryRow(ctx, `SELECT id FROM payment_channel_accounts WHERE account_key='acct_Cust703'`).Scan(&accountID); err != nil {
		t.Fatal(err)
	}
	account := payment.WebhookAccount{AccountKey: "acct_Cust703", Environment: "sandbox"}
	fixture := &localCurrencyStripeFixture{account: account}

	// Concurrent customer creation converges on a single DB row and identical Stripe idempotency key.
	const workers = 8
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	ids := make(chan string, workers)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec, err := ensureStripeCustomer(ctx, fixture, 703, accountID, account)
			if err != nil {
				errs <- err
				return
			}
			ids <- rec.CustomerID
		}()
	}
	wg.Wait()
	close(errs)
	close(ids)
	for err := range errs {
		t.Fatalf("concurrent ensureStripeCustomer error: %v", err)
	}
	for id := range ids {
		if id != "cus_Concurrent01" {
			t.Fatalf("got customer ID %q, want cus_Concurrent01", id)
		}
	}
	wantKey := stripeCustomerIdempotencyKey(account, 703)
	for _, k := range fixture.customerKeys {
		if k != wantKey {
			t.Fatalf("idempotency key = %q, want %q", k, wantKey)
		}
	}
	var rowCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM payment_stripe_customers WHERE user_id=703 AND account_id=$1 AND environment='sandbox'`, accountID).Scan(&rowCount); err != nil || rowCount != 1 {
		t.Fatalf("expected 1 customer row, got %d err=%v", rowCount, err)
	}

	// Address collection stays "required" until billing country & tax evidence are verified.
	policy, err := loadStripeAccountCheckoutPolicy(ctx, pool, accountID)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := findStripeCustomer(ctx, pool, 703, accountID, "sandbox")
	if err != nil || rec == nil {
		t.Fatalf("findStripeCustomer = %+v, %v", rec, err)
	}
	before := buildFrozenCheckoutParams(payment.StripeSaleDirect, stripeCheckoutModeOneTime, policy, rec)
	if before.BillingAddressCollection != payment.BillingAddressCollectionRequired || !before.SavePaymentMethodEnabled || before.SetupFutureUsage != payment.SetupFutureUsageOnSession {
		t.Fatalf("before verification params = %+v", before)
	}

	// Once billing country & tax evidence are verified, address collection simplifies to "auto".
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := upsertStripeCustomerTx(ctx, tx, 703, accountID, "sandbox", "cus_Concurrent01", "GB", true); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	recAfter, err := findStripeCustomer(ctx, pool, 703, accountID, "sandbox")
	if err != nil || recAfter == nil {
		t.Fatal(err)
	}
	after := buildFrozenCheckoutParams(payment.StripeSaleDirect, stripeCheckoutModeOneTime, policy, recAfter)
	if after.BillingAddressCollection != payment.BillingAddressCollectionAuto {
		t.Fatalf("after verification BillingAddressCollection = %q, want auto", after.BillingAddressCollection)
	}
}
