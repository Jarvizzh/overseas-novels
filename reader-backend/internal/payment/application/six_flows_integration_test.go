package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"reader-backend/internal/model"
	"reader-backend/internal/novel"
	"reader-backend/internal/payment"
	"reader-backend/internal/payment/paypal"
	"reader-backend/internal/testdb"
	"reader-backend/internal/wallet"
)

type sixFlowPayPalAdapter struct {
	*vipOfferAdapter
	orders         map[string]string // orderID -> customID
	cancelledSubs  []string
	capturesByRef  map[string]*payment.OrderCapture
	captureErr     error
	subDetailsByID map[string]*payment.SubscriptionDetails
	subByRequest   map[string]string
	createFault    string
	cancelFault    func(string) error
	getSubFault    func(string) error
	beforeCreate   func()
	listTxCalls    int
	activateCalls  int
	subTxByID      map[string][]payment.SubscriptionTransaction
}

func (a *sixFlowPayPalAdapter) CreateOrder(_ context.Context, _ payment.WebhookAccount, _ int64, customID, _, _, _, _ string) (string, string, error) {
	if a.orders == nil {
		a.orders = make(map[string]string)
	}
	id := fmt.Sprintf("PP-ORD-%d", len(a.orders)+1)
	a.orders[id] = customID
	return id, "https://www.sandbox.paypal.com/checkoutnow?token=" + id, nil
}

func (a *sixFlowPayPalAdapter) CaptureOrder(_ context.Context, id string, account payment.WebhookAccount) (*payment.OrderCapture, error) {
	if a.captureErr != nil {
		return nil, a.captureErr
	}
	if capObj, ok := a.capturesByRef[id]; ok {
		return capObj, nil
	}
	return &payment.OrderCapture{
		OrderID: id, CaptureID: "PP-CAP-" + id, CustomID: a.orders[id], Account: account,
		CurrencyCode: "USD", GrossAmount: "9.99", FeeAmount: "0.49", NetAmount: "9.50", Status: "COMPLETED", RawPayload: "{}",
	}, nil
}

func (a *sixFlowPayPalAdapter) GetCapture(_ context.Context, id string) (*payment.OrderCapture, error) {
	if capObj, ok := a.capturesByRef[id]; ok {
		return capObj, nil
	}
	return &payment.OrderCapture{OrderID: id, CustomID: a.orders[id], Account: a.account, CurrencyCode: "USD", GrossAmount: "9.99"}, paypal.ErrOrderApproved
}

func (a *sixFlowPayPalAdapter) CanCloseSingleCheckout(context.Context, string, string, int64, string, payment.WebhookAccount) (bool, error) {
	return true, nil
}

func (a *sixFlowPayPalAdapter) CreateSubscription(_ context.Context, p payment.CreateSubscriptionParam) (*payment.SubscriptionResult, error) {
	a.agreements = append(a.agreements, p)
	if a.beforeCreate != nil {
		a.beforeCreate()
	}
	if a.createFault == "before" {
		a.createFault = ""
		return nil, context.DeadlineExceeded
	}
	id := a.subByRequest[p.RequestID]
	if id == "" {
		id = fmt.Sprintf("I-SIX-%d-%d", p.UserID, len(a.agreements))
		if a.subByRequest == nil {
			a.subByRequest = make(map[string]string)
		}
		a.subByRequest[p.RequestID] = id
		a.subDetailsByID[id] = &payment.SubscriptionDetails{SubscriptionID: id, PlanID: p.PlanID, CustomID: p.CustomID, Status: "APPROVAL_PENDING"}
	}
	if a.createFault == "after" {
		a.createFault = ""
		return nil, context.DeadlineExceeded
	}
	return &payment.SubscriptionResult{
		SubscriptionID: id,
		ApproveURL:     "https://www.sandbox.paypal.com/webapps/billing/subscriptions?ba_token=" + id,
	}, nil
}

func (a *sixFlowPayPalAdapter) GetSubscription(_ context.Context, id string) (*payment.SubscriptionDetails, error) {
	if a.getSubFault != nil {
		if err := a.getSubFault(id); err != nil {
			return nil, err
		}
	}
	if d, ok := a.subDetailsByID[id]; ok {
		return d, nil
	}
	return &payment.SubscriptionDetails{SubscriptionID: id, Status: "APPROVAL_PENDING"}, nil
}

func (a *sixFlowPayPalAdapter) ActivateSubscription(context.Context, string) error {
	a.activateCalls++
	return nil
}

func (a *sixFlowPayPalAdapter) CancelSubscription(_ context.Context, id, _ string) error {
	a.cancelledSubs = append(a.cancelledSubs, id)
	if a.cancelFault != nil {
		return a.cancelFault(id)
	}
	a.subDetailsByID[id].Status = "CANCELLED"
	return nil
}

func (a *sixFlowPayPalAdapter) ListSubscriptionTransactions(_ context.Context, id string, _, _ time.Time) ([]payment.SubscriptionTransaction, error) {
	a.listTxCalls++
	if a.subTxByID != nil {
		return a.subTxByID[id], nil
	}
	return nil, nil
}

type sixFlowStripeAdapter struct {
	account       payment.WebhookAccount
	sessionSeq    int
	vipSessionSeq int
	vipCreates    []payment.StripeVIPCheckoutRequest
	closedVIP     []string
	loseVIPReply  bool
	closeVIPProof func(payment.StripeVIPCheckout) payment.StripeVIPCheckout
	closeVIPErr   error
	vipByIntent   map[string]string
}

func (a *sixFlowStripeAdapter) Provider() string                     { return payment.ProviderStripe }
func (a *sixFlowStripeAdapter) AccountScope() payment.WebhookAccount { return a.account }

func (a *sixFlowStripeAdapter) CreateSingleCheckout(_ context.Context, req payment.SingleCheckoutRequest) (payment.SingleCheckoutProof, error) {
	a.sessionSeq++
	id := fmt.Sprintf("cs_single_%d", a.sessionSeq)
	return payment.SingleCheckoutProof{
		ID: id, AttemptID: req.AttemptID, AwaitingBuyer: true,
		URL: "https://checkout.stripe.com/c/pay/" + id,
	}, nil
}

func (a *sixFlowStripeAdapter) FindSingleCheckout(context.Context, string, time.Time, time.Time) (string, error) {
	return "", nil
}

func (a *sixFlowStripeAdapter) ReadSingleCheckout(_ context.Context, sessionID, attemptID string, _ payment.SingleCheckoutOffer) (payment.SingleCheckoutProof, error) {
	return payment.SingleCheckoutProof{ID: sessionID, AttemptID: attemptID, AwaitingBuyer: true}, nil
}

func (a *sixFlowStripeAdapter) CreateStripeVIPCheckout(_ context.Context, req payment.StripeVIPCheckoutRequest) (payment.StripeVIPCheckout, error) {
	a.vipCreates = append(a.vipCreates, req)
	id := a.vipByIntent[req.IntentID]
	if id == "" {
		a.vipSessionSeq++
		id = fmt.Sprintf("cs_vip_%d", a.vipSessionSeq)
		if a.vipByIntent == nil {
			a.vipByIntent = make(map[string]string)
		}
		a.vipByIntent[req.IntentID] = id
	}
	if a.loseVIPReply {
		return payment.StripeVIPCheckout{}, context.DeadlineExceeded
	}
	return payment.StripeVIPCheckout{
		ID: id, IntentID: req.IntentID, AwaitingBuyer: true,
		URL: "https://checkout.stripe.com/c/pay/" + id,
	}, nil
}

func (a *sixFlowStripeAdapter) FindStripeVIPCheckout(_ context.Context, intentID string, _, _ time.Time) (string, error) {
	return a.vipByIntent[intentID], nil
}

func (a *sixFlowStripeAdapter) ReadStripeVIPCheckout(_ context.Context, sessionID, intentID, _ string) (payment.StripeVIPCheckout, error) {
	return payment.StripeVIPCheckout{ID: sessionID, IntentID: intentID, AwaitingBuyer: true,
		URL: "https://checkout.stripe.com/c/pay/" + sessionID}, nil
}

func (a *sixFlowStripeAdapter) CloseStripeVIPCheckout(_ context.Context, sessionID, intentID, _ string) (payment.StripeVIPCheckout, error) {
	a.closedVIP = append(a.closedVIP, sessionID)
	if a.closeVIPErr != nil {
		return payment.StripeVIPCheckout{}, a.closeVIPErr
	}
	proof := payment.StripeVIPCheckout{ID: sessionID, IntentID: intentID, Expired: true}
	if a.closeVIPProof != nil {
		proof = a.closeVIPProof(proof)
	}
	return proof, nil
}

func (a *sixFlowStripeAdapter) ReadStripeVIPSubscription(_ context.Context, subID string) (payment.StripeVIPSubscription, error) {
	return payment.StripeVIPSubscription{ID: subID, Status: "active"}, nil
}

func (a *sixFlowStripeAdapter) StripeVIPInvoiceRefs(context.Context, string, string) ([]string, string, error) {
	return nil, "", nil
}

func (a *sixFlowStripeAdapter) StripeInvoiceSubscription(context.Context, string) (string, error) {
	return "", nil
}

func (a *sixFlowStripeAdapter) ReadStripeVIPInvoice(context.Context, string, payment.StripeVIPOffer) (payment.StripeVIPInvoice, error) {
	return payment.StripeVIPInvoice{}, nil
}

func (a *sixFlowStripeAdapter) ScheduleStripeVIPCancellation(_ context.Context, subID, _ string) (payment.StripeVIPSubscription, error) {
	return payment.StripeVIPSubscription{ID: subID, Status: "active", CancelAtPeriodEnd: true}, nil
}

func (a *sixFlowStripeAdapter) OpenStripeVIPPortal(context.Context, string, string, string) (string, error) {
	return "https://billing.stripe.com/session/mock", nil
}

func (a *sixFlowStripeAdapter) GetLedgerMovement(context.Context, string) (payment.LedgerEvidence, error) {
	return payment.LedgerEvidence{}, nil
}

func TestSixH5PurchaseFlowsEndToEnd(t *testing.T) {
	t.Setenv("CONVERSION_ENVIRONMENT", "sandbox")
	pool := testdb.Open(t)
	ctx := t.Context()

	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, q, args...); err != nil {
			t.Fatalf("exec %q: %v", q, err)
		}
	}
	exec(`INSERT INTO users(id, email) VALUES
		(601, 'coins@example.invalid'),
		(602, 'book@example.invalid'),
		(603, 'vip-pp@example.invalid'),
		(604, 'vip-st@example.invalid'),
		(605, 'prefulfill@example.invalid'),
		(606, 'shortfall@example.invalid');
		INSERT INTO novels(id, title, author, publish_status) VALUES
		(601, 'Book One', 'Author', 'published'),
		(602, 'Book Two', 'Author', 'published');
		SELECT assert_paypal_identity('sixflow-paypal', 'sandbox');
		INSERT INTO payment_channel_accounts(provider, account_key, environment, credentials_ref, webhook_ref, display_name, status, is_primary, new_orders_enabled, new_subscriptions_enabled)
		VALUES ('stripe', 'acct_SixFlow', 'sandbox', 'db:encrypted', 'test', 'SixFlow Stripe', 'published', true, true, true)
		ON CONFLICT (provider, account_key, environment) DO UPDATE SET credentials_ref='db:encrypted', status='published', is_primary=true, new_orders_enabled=true, new_subscriptions_enabled=true;`)
	// Ensure PayPal account is published/primary with both capabilities enabled
	exec(`UPDATE payment_channel_accounts SET status='published', is_primary=true, new_orders_enabled=true, new_subscriptions_enabled=true WHERE account_key='sixflow-paypal'`)

	// Seed 4 slots across default Template 600 (Coins, WholeBook All, WholeBook Specific, VIP Year, VIP Month)
	pCoins := seedTestProductAndSlot(t, pool, testCatalogSpec{
		SlotID: 601, TemplateID: 600, TemplateName: "SixFlow Standard", IsDefault: true, SlotIndex: 1,
		Code: "SIX_COINS_499", ProductType: "single", PriceCents: 499, Coins: 100, Bonus: 20,
		Badge: "Popular", DisplayAlias: "Starter Pack",
		Environment: "sandbox", PayPalAccountKey: "sixflow-paypal", StripeAccountKey: "acct_SixFlow",
	})
	_ = seedTestProductAndSlot(t, pool, testCatalogSpec{
		SlotID: 602, TemplateID: 600, TemplateName: "SixFlow Standard", IsDefault: true, SlotIndex: 2,
		Code: "SIX_BOOK_ALL_999", ProductType: "whole_book", PriceCents: 999,
		NovelScopeType: "all_published", Badge: "Full Access",
		Environment: "sandbox", PayPalAccountKey: "sixflow-paypal", StripeAccountKey: "acct_SixFlow",
	})
	_ = seedTestProductAndSlot(t, pool, testCatalogSpec{
		SlotID: 603, TemplateID: 600, TemplateName: "SixFlow Standard", IsDefault: true, SlotIndex: 3,
		Code: "SIX_BOOK_SPEC_899", ProductType: "whole_book", PriceCents: 899,
		NovelScopeType: "specific_novels", NovelScopeIDs: []int64{601},
		Environment: "sandbox", PayPalAccountKey: "sixflow-paypal", StripeAccountKey: "acct_SixFlow",
	})
	pVIPYear := seedTestProductAndSlot(t, pool, testCatalogSpec{
		SlotID: 604, TemplateID: 600, TemplateName: "SixFlow Standard", IsDefault: true, SlotIndex: 4,
		Code: "SIX_VIP_YEAR_9999", ProductType: "vip", VipCycle: "year", PriceCents: 9999,
		Badge: "Save 45%", IsHighlighted: true, DisplayAlias: "Annual VIP",
		Environment: "sandbox", PayPalAccountKey: "sixflow-paypal", PayPalPlanID: "P-SIX-YEAR",
		StripeAccountKey: "acct_SixFlow", StripeProductRef: "prod_VIP_YEAR", StripePriceRef: "price_VIP_YEAR",
	})
	_ = seedTestProductAndSlot(t, pool, testCatalogSpec{
		SlotID: 605, TemplateID: 600, TemplateName: "SixFlow Standard", IsDefault: true, SlotIndex: 5,
		Code: "SIX_VIP_MONTH_1499", ProductType: "vip", VipCycle: "month", PriceCents: 1499,
		Environment: "sandbox", PayPalAccountKey: "sixflow-paypal", PayPalPlanID: "P-SIX-MONTH",
		StripeAccountKey: "acct_SixFlow", StripeProductRef: "prod_VIP_MONTH", StripePriceRef: "price_VIP_MONTH",
	})

	ppAccount := payment.WebhookAccount{AccountKey: "sixflow-paypal", Environment: "sandbox"}
	stAccount := payment.WebhookAccount{AccountKey: "acct_SixFlow", Environment: "sandbox"}
	ppAdapter := &sixFlowPayPalAdapter{
		vipOfferAdapter: &vipOfferAdapter{account: ppAccount},
		capturesByRef:   make(map[string]*payment.OrderCapture),
		subDetailsByID:  make(map[string]*payment.SubscriptionDetails),
	}
	stAdapter := &sixFlowStripeAdapter{account: stAccount}

	adapters := payment.NewAdapters(ppAdapter)
	if err := adapters.RegisterAccount(stAccount, stAdapter, true); err != nil {
		t.Fatal(err)
	}
	repo := NewDBRepository(wallet.NewDBRepository())
	svc := NewService(repo, novel.NewDBRepository(), adapters).(*service)
	origin := "https://reader.example.invalid"

	// Configure runtime allowed origin for checkoutSite check
	exec(`INSERT INTO system_domains(name, domain, status) VALUES('Reader Example', 'reader.example.invalid', 1) ON CONFLICT DO NOTHING`)

	// Verify GetRechargeTemplates hydrates dual-channel slots, bonus=0 rules, VIP cycles, and novel scopes
	tpl601, err := svc.GetRechargeTemplates(ctx, 601, 601)
	if err != nil || len(tpl601.Slots) != 5 {
		t.Fatalf("template slots for novel 601: got %d (%+v), err=%v", len(tpl601.Slots), tpl601.Slots, err)
	}
	tpl602, err := svc.GetRechargeTemplates(ctx, 601, 602)
	if err != nil || len(tpl602.Slots) != 4 {
		// slot 603 (specific_novels=[601]) should be filtered out for novel 602!
		t.Fatalf("expected 4 slots for novel 602, got %d (%+v), err=%v", len(tpl602.Slots), tpl602.Slots, err)
	}
	methods, err := svc.GetPaymentMethods(ctx)
	if err != nil || !methods.PayPalSingle || !methods.PayPalVIP || !methods.StripeSingle || !methods.StripeVIP {
		t.Fatalf("unexpected methods: %+v %v", methods, err)
	}

	// Flow 1: Coins x PayPal
	t.Run("Flow1_Coins_PayPal", func(t *testing.T) {
		intent, err := svc.PreparePurchase(ctx, 601, 0, 601, 0, uuid.NewString(), "", checkoutAttribution{})
		if err != nil || intent.OrderType != "single" || intent.Coins != 120 || !slices.Contains(intent.AvailableChannels, payment.ProviderPayPal) || !slices.Contains(intent.AvailableChannels, payment.ProviderStripe) {
			t.Fatalf("Flow 1 prepare: %+v err=%v", intent, err)
		}
		started, err := svc.StartPayPalCheckout(ctx, 601, intent.ID, origin)
		if err != nil || started.Status != "awaiting_approval" || started.ExternalOrderID == "" {
			t.Fatalf("Flow 1 start paypal: %+v err=%v", started, err)
		}
		ppAdapter.capturesByRef[started.ExternalOrderID] = &payment.OrderCapture{
			OrderID: started.ExternalOrderID, CaptureID: "CAP-COINS-PP-1", CustomID: intent.ID, Account: ppAccount,
			CurrencyCode: "USD", GrossAmount: "4.99", FeeAmount: "0.30", NetAmount: "4.69", Status: "COMPLETED", RawPayload: "{}",
		}
		if _, err := svc.CapturePayPalPayment(ctx, 601, started.ExternalOrderID); err != nil {
			t.Fatalf("Flow 1 capture: %v", err)
		}
		w, err := wallet.NewDBRepository().GetWallet(ctx, 601)
		if err != nil || w.ChargedCoins != 100 || w.BonusCoins != 20 {
			t.Fatalf("Flow 1 wallet mismatch: %+v err=%v", w, err)
		}
		done, err := svc.GetPurchase(ctx, 601, intent.ID, "")
		if err != nil || done.Status != "completed" {
			t.Fatalf("Flow 1 final intent: %+v err=%v", done, err)
		}
	})

	// Flow 2: Coins x Stripe (happy path + pre-fulfillment refund + partial-to-full refund shortfall)
	t.Run("Flow2_Coins_Stripe", func(t *testing.T) {
		// Happy path via user's active template slot!
		intent, err := svc.PreparePurchase(ctx, 601, pCoins, 601, 0, uuid.NewString(), "", checkoutAttribution{})
		if err != nil || intent.ProductID != pCoins || intent.OrderType != "single" {
			t.Fatalf("Flow 2 prepare by product_id: %+v err=%v", intent, err)
		}
		started, err := svc.StartStripeCheckout(ctx, 601, intent.ID, origin)
		if err != nil || started.Provider != payment.ProviderStripe || started.Status != "awaiting_approval" {
			t.Fatalf("Flow 2 start stripe: %+v err=%v", started, err)
		}
		attempt, err := loadStripeAttemptByCheckoutID(ctx, pool, stAccount, intent.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err := repo.applyStripeProof(ctx, attempt, payment.SingleCheckoutProof{
			ID: attempt.SessionID, AttemptID: attempt.ID,
			Receipt: &payment.SingleChargeProof{
				ID: "ch_coins_ok", Country: "US", Currency: "USD", AmountMinor: 499, PaidAt: time.Now(), EligibleTerms: true, Evidence: json.RawMessage(`{}`),
			},
		}, nil); err != nil {
			t.Fatalf("Flow 2 settle stripe: %v", err)
		}
		w, _ := wallet.NewDBRepository().GetWallet(ctx, 601)
		if w.ChargedCoins != 200 || w.BonusCoins != 40 {
			t.Fatalf("Flow 2 cumulative wallet mismatch: %+v", w)
		}

		// Pre-fulfillment full refund on Stripe Coins -> closes Refunded & blocks coin credit
		prefIntent, err := svc.PreparePurchase(ctx, 605, 0, 601, 0, uuid.NewString(), "", checkoutAttribution{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.StartStripeCheckout(ctx, 605, prefIntent.ID, origin); err != nil {
			t.Fatal(err)
		}
		prefAttempt, err := loadStripeAttemptByCheckoutID(ctx, pool, stAccount, prefIntent.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err := repo.applyStripeProof(ctx, prefAttempt, payment.SingleCheckoutProof{
			ID: prefAttempt.SessionID, AttemptID: prefAttempt.ID,
			Receipt: &payment.SingleChargeProof{
				ID: "ch_coins_pref_ref", Country: "US", Currency: "USD", AmountMinor: 499, RefundedMinor: 499, PaidAt: time.Now(), EligibleTerms: true, Evidence: json.RawMessage(`{}`),
			},
		}, nil); err != nil {
			t.Fatalf("Flow 2 pre-fulfillment refund settle: %v", err)
		}
		if prefDone, err := svc.GetPurchase(ctx, 605, prefIntent.ID, ""); err != nil || prefDone.Status != "refunded" {
			t.Fatalf("expected pre-fulfillment refunded status, got %+v err=%v", prefDone, err)
		}
		if w605, _ := wallet.NewDBRepository().GetWallet(ctx, 605); w605 != nil {
			t.Fatalf("pre-fulfillment refund credited wallet: %+v", w605)
		}

		// Partial-to-full refund shortfall visibility in payment_entitlement_adjustments & PaymentTaskStatus
		sfIntent, err := svc.PreparePurchase(ctx, 606, 0, 601, 0, uuid.NewString(), "", checkoutAttribution{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.StartStripeCheckout(ctx, 606, sfIntent.ID, origin); err != nil {
			t.Fatal(err)
		}
		sfAttempt, _ := loadStripeAttemptByCheckoutID(ctx, pool, stAccount, sfIntent.ID)
		if err := repo.applyStripeProof(ctx, sfAttempt, payment.SingleCheckoutProof{
			ID: sfAttempt.SessionID, AttemptID: sfAttempt.ID,
			Receipt: &payment.SingleChargeProof{
				ID: "ch_coins_sf", Country: "US", Currency: "USD", AmountMinor: 499, PaidAt: time.Now(), EligibleTerms: true, Evidence: json.RawMessage(`{}`),
			},
		}, nil); err != nil {
			t.Fatal(err)
		}
		// Buyer spends 40 charged + 10 bonus coins before refund
		exec(`UPDATE wallets SET charged_coins=60, bonus_coins=10 WHERE user_id=606`)
		// Partial refund (200 cents) -> no coin clawback yet
		runRefundTx := func(refID string, minor int64) {
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err := applyStripeRefundTx(ctx, tx, stAccount, &payment.StripeRefundProof{
				ID: refID, ChargeID: "ch_coins_sf", Currency: "USD", Status: "succeeded", AmountMinor: minor,
			}); err != nil {
				_ = tx.Rollback(ctx)
				t.Fatal(err)
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
		}
		runRefundTx("re_sf_1", 200)
		if w606, _ := wallet.NewDBRepository().GetWallet(ctx, 606); w606.ChargedCoins != 60 || w606.BonusCoins != 10 {
			t.Fatalf("partial refund debited coins prematurely: %+v", w606)
		}
		// Remaining refund (299 cents -> full 499) -> claws back 60+10, records 40+10 shortfall
		runRefundTx("re_sf_2", 299)
		if w606, _ := wallet.NewDBRepository().GetWallet(ctx, 606); w606.ChargedCoins != 0 || w606.BonusCoins != 0 {
			t.Fatalf("full refund failed to claw back remaining coins: %+v", w606)
		}
		var cSF, bSF int
		if err := pool.QueryRow(ctx, `SELECT charged_coin_shortfall, bonus_coin_shortfall FROM payment_entitlement_adjustments WHERE order_id=$1`, sfAttempt.OrderID).Scan(&cSF, &bSF); err != nil || cSF != 40 || bSF != 10 {
			t.Fatalf("shortfall expected 40/10, got %d/%d err=%v", cSF, bSF, err)
		}
		tasks, err := PaymentTaskStatus(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var foundShortfallTask bool
		for _, task := range tasks {
			if task.Kind == "refund_shortfall" && task.Count >= 1 {
				foundShortfallTask = true
			}
		}
		if !foundShortfallTask {
			t.Fatalf("expected refund_shortfall in PaymentTaskStatus, got %+v", tasks)
		}
	})

	// Flow 3: WholeBook x PayPal (+ scope check specific_novels)
	t.Run("Flow3_WholeBook_PayPal", func(t *testing.T) {
		if _, err := svc.PreparePurchase(ctx, 602, 0, 603, 602, uuid.NewString(), "", checkoutAttribution{}); !errors.Is(err, ErrCheckoutUnavailable) {
			t.Fatalf("specific_novels [601] allowed novel 602: %v", err)
		}
		intent, err := svc.PreparePurchase(ctx, 602, 0, 603, 601, uuid.NewString(), "/content?novel_id=601&chapter_index=2", checkoutAttribution{})
		if err != nil || intent.OrderType != "whole_book" || intent.Coins != 0 || intent.NovelID != 601 {
			t.Fatalf("Flow 3 prepare: %+v err=%v", intent, err)
		}
		started, err := svc.StartPayPalCheckout(ctx, 602, intent.ID, origin)
		if err != nil || started.Status != "awaiting_approval" {
			t.Fatalf("Flow 3 start: %+v err=%v", started, err)
		}
		ppAdapter.capturesByRef[started.ExternalOrderID] = &payment.OrderCapture{
			OrderID: started.ExternalOrderID, CaptureID: "CAP-BOOK-PP-1", CustomID: intent.ID, Account: ppAccount,
			CurrencyCode: "USD", GrossAmount: "8.99", FeeAmount: "0.40", NetAmount: "8.59", Status: "COMPLETED", RawPayload: "{}",
		}
		if _, err := svc.CapturePayPalPayment(ctx, 602, started.ExternalOrderID); err != nil {
			t.Fatalf("Flow 3 capture: %v", err)
		}
		if owned, err := novel.NewDBRepository().HasWholeBook(ctx, 602, 601); err != nil || !owned {
			t.Fatalf("Flow 3 ownership=%v err=%v", owned, err)
		}
		if _, err := svc.PreparePurchase(ctx, 602, 0, 602, 601, uuid.NewString(), "", checkoutAttribution{}); !errors.Is(err, ErrCheckoutConflict) {
			t.Fatalf("expected duplicate book purchase blocked, got %v", err)
		}
	})

	// Flow 4: WholeBook x Stripe (with PayPal -> Stripe switch on all_published)
	t.Run("Flow4_WholeBook_Stripe_Switch", func(t *testing.T) {
		intent, err := svc.PreparePurchase(ctx, 602, 0, 602, 602, uuid.NewString(), "", checkoutAttribution{})
		if err != nil || intent.OrderType != "whole_book" || intent.Coins != 0 {
			t.Fatalf("Flow 4 prepare: %+v err=%v", intent, err)
		}
		if _, err := svc.StartPayPalCheckout(ctx, 602, intent.ID, origin); err != nil {
			t.Fatalf("Flow 4 start paypal: %v", err)
		}
		switched, err := svc.StartStripeCheckout(ctx, 602, intent.ID, origin)
		if err != nil || switched.Provider != payment.ProviderStripe || switched.Status != "awaiting_approval" {
			t.Fatalf("Flow 4 switch to stripe: %+v err=%v", switched, err)
		}
		attempt, err := loadStripeAttemptByCheckoutID(ctx, pool, stAccount, intent.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err := repo.applyStripeProof(ctx, attempt, payment.SingleCheckoutProof{
			ID: attempt.SessionID, AttemptID: attempt.ID,
			Receipt: &payment.SingleChargeProof{
				ID: "ch_book2_ok", Country: "US", Currency: "USD", AmountMinor: 999, PaidAt: time.Now(), EligibleTerms: true, Evidence: json.RawMessage(`{}`),
			},
		}, nil); err != nil {
			t.Fatalf("Flow 4 settle stripe: %v", err)
		}
		if owned, err := novel.NewDBRepository().HasWholeBook(ctx, 602, 602); err != nil || !owned {
			t.Fatalf("Flow 4 ownership=%v err=%v", owned, err)
		}
	})

	// Flow 5: VIP (year cycle!) x PayPal unified via PreparePurchase
	t.Run("Flow5_VIP_Year_PayPal", func(t *testing.T) {
		intent, err := svc.PreparePurchase(ctx, 603, pVIPYear, 604, 0, uuid.NewString(), "", checkoutAttribution{})
		if err != nil || intent.OrderType != "subscription" || intent.Cycle != "year" || intent.Badge != "Save 45%" || intent.DisplayAlias != "Annual VIP" {
			t.Fatalf("Flow 5 prepare: %+v err=%v", intent, err)
		}
		started, err := svc.StartPayPalCheckout(ctx, 603, intent.ID, origin)
		if err != nil || started.Provider != payment.ProviderPayPal || started.Status != "awaiting_approval" || started.ExternalOrderID == "" {
			t.Fatalf("Flow 5 start paypal: %+v err=%v", started, err)
		}
		sub, err := repo.GetSubscriptionByID(ctx, started.ExternalOrderID)
		if err != nil || sub == nil || sub.ProductID == nil || *sub.ProductID != pVIPYear || sub.Cycle != "year" {
			t.Fatalf("Flow 5 subscription row: %+v err=%v", sub, err)
		}
		paid := time.Now().UTC().Truncate(time.Second)
		end := regularPayPalPeriodEnd(sub, paid, paid)
		if !end.Equal(paid.AddDate(1, 0, 0)) {
			t.Fatalf("expected 1 year period end %v, got %v", paid.AddDate(1, 0, 0), end)
		}
		first, err := repo.RecordSubscriptionRenewalOrderTx(ctx, sub, "SALE-VIP-YEAR-1", 9999, "USD", paid, end, "ACTIVE", &model.ThirdPartyPaymentOrder{Status: "COMPLETED", RawPayload: "{}"})
		if err != nil || !first {
			t.Fatalf("Flow 5 renewal record: first=%v err=%v", first, err)
		}
		if active, err := novel.NewDBRepository().HasActiveSubscription(ctx, 603); err != nil || !active {
			t.Fatalf("Flow 5 active=%v err=%v", active, err)
		}
		if done, err := svc.GetPurchase(ctx, 603, intent.ID, ""); err != nil || done.Status != "completed" {
			t.Fatalf("Flow 5 final purchase status: %+v err=%v", done, err)
		}
		list, err := svc.ListRecentPurchases(ctx, 603)
		if err != nil || len(list) != 1 || list[0].ID != intent.ID {
			t.Fatalf("Flow 5 deduplicated recent purchases: %+v err=%v", list, err)
		}
	})

	// Flow 6: VIP x Stripe (with PayPal -> Stripe VIP switch before payment)
	t.Run("Flow6_VIP_Month_Stripe_Switch", func(t *testing.T) {
		intent, err := svc.PreparePurchase(ctx, 604, 0, 605, 0, uuid.NewString(), "", checkoutAttribution{})
		if err != nil || intent.OrderType != "subscription" || intent.Cycle != "month" {
			t.Fatalf("Flow 6 prepare: %+v err=%v", intent, err)
		}
		ppStarted, err := svc.StartPayPalCheckout(ctx, 604, intent.ID, origin)
		if err != nil || ppStarted.Provider != payment.ProviderPayPal {
			t.Fatalf("Flow 6 start paypal: %+v err=%v", ppStarted, err)
		}
		switched, err := svc.StartStripeCheckout(ctx, 604, intent.ID, origin)
		if err != nil || switched.Provider != payment.ProviderStripe || switched.Status != "awaiting_approval" {
			t.Fatalf("Flow 6 switch to stripe: %+v err=%v", switched, err)
		}
		if !slices.Contains(ppAdapter.cancelledSubs, ppStarted.ExternalOrderID) {
			t.Fatalf("PayPal VIP was not cancelled during switch: %+v", ppAdapter.cancelledSubs)
		}

		// Settle Stripe VIP subscription and verify unified VIP intent flips Completed
		var stIntentID string
		if err := pool.QueryRow(ctx, `SELECT stripe_intent_id::text FROM payment_vip_checkout_intents WHERE intent_id=$1`, intent.ID).Scan(&stIntentID); err != nil {
			t.Fatal(err)
		}
		v, err := findStripeVIPIntent(ctx, pool, stAccount, stIntentID)
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC()
		inv := payment.StripeVIPInvoice{
			ID: "in_six_vip", BillingReason: "subscription_create", Currency: "USD", Country: "US", Status: "paid",
			DueMinor: 1499, RemainingMinor: 0, StartsAt: now.Add(-time.Hour), EndsAt: now.AddDate(0, 1, 0),
			Subscription: payment.StripeVIPSubscription{ID: "sub_six_vip", CustomerID: "cus_six_vip", IntentID: v.ID, PriceID: v.Offer.PriceID, Status: "active"},
			Allocations: []payment.StripeVIPAllocation{{
				ID: "inpay_six", AmountMinor: 1499,
				Receipt: payment.SingleChargeProof{ID: "ch_six_vip", Currency: "USD", AmountMinor: 1499, PaidAt: now, EligibleTerms: true, Evidence: json.RawMessage(`{}`)},
			}},
		}
		if err := settleStripeVIPInvoice(ctx, v, inv, nil); err != nil {
			t.Fatalf("Flow 6 settle stripe vip: %v", err)
		}
		if active, err := novel.NewDBRepository().HasActiveSubscription(ctx, 604); err != nil || !active {
			t.Fatalf("Flow 6 active=%v err=%v", active, err)
		}
		if done, err := svc.GetPurchase(ctx, 604, intent.ID, ""); err != nil || done.Status != "completed" {
			t.Fatalf("Flow 6 final purchase status: %+v err=%v", done, err)
		}
	})

	t.Run("UnifiedVIPReviewRegressions", func(t *testing.T) {
		testUnifiedVIPReview(t, svc, pool, ppAdapter, stAdapter, origin)
	})

	t.Run("UnifiedVIPRereviewRegressions", func(t *testing.T) {
		testUnifiedVIPRereview(t, svc, pool, ppAdapter, stAdapter, origin)
	})

	// Dual-channel gate: disabling Stripe binding on pCoins excludes it from catalog & PreparePurchase
	t.Run("DualChannelReadyGate", func(t *testing.T) {
		exec(`UPDATE product_channel_bindings SET enabled=false WHERE product_id=$1 AND provider='stripe'`, pCoins)
		t.Cleanup(func() {
			exec(`UPDATE product_channel_bindings SET enabled=true WHERE product_id=$1 AND provider='stripe'`, pCoins)
		})
		if _, err := svc.PreparePurchase(ctx, 601, pCoins, 601, 0, uuid.NewString(), "", checkoutAttribution{}); !errors.Is(err, ErrCheckoutUnavailable) {
			t.Fatalf("expected ErrCheckoutUnavailable when Stripe binding disabled, got %v", err)
		}
		tplAfter, err := svc.GetRechargeTemplates(ctx, 601, 601)
		if err != nil || len(tplAfter.Slots) != 4 {
			t.Fatalf("expected disabled product removed from H5 template, got %d (%+v)", len(tplAfter.Slots), err)
		}
	})

	// Webhook token endpoint check
	t.Run("ChannelWebhookTokenRoute", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		router := gin.New()
		NewHandler(svc).RegisterRoutes(router.Group("/api/v1"))
		req := httptest.NewRequest(http.MethodPost, "/api/v1/wallet/webhook/channel/"+uuid.NewString(), strings.NewReader(`{}`))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable && rec.Code != http.StatusNotFound {
			t.Fatalf("unknown webhook token status=%d", rec.Code)
		}
	})
}

func loadStripeAttemptByCheckoutID(ctx context.Context, q checkoutQueryer, account payment.WebhookAccount, checkoutID string) (stripePurchaseAttempt, error) {
	var id string
	if err := q.QueryRow(ctx, `SELECT p.id::text FROM payment_checkout_attempts p
		JOIN recharge_orders o ON o.id=p.order_id
		WHERE o.checkout_id=$1::uuid AND p.provider='stripe' ORDER BY p.created_at DESC LIMIT 1`, checkoutID).Scan(&id); err != nil {
		return stripePurchaseAttempt{}, err
	}
	return loadStripeAttempt(ctx, q, account, id)
}
