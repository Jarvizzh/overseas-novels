package application

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"reader-backend/internal/model"
	"reader-backend/internal/novel"
	"reader-backend/internal/payment"
	"reader-backend/internal/testdb"
	"reader-backend/internal/wallet"
)

func setupRegressionReaderFixture(t *testing.T) (*pgxpool.Pool, *service, *Handler) {
	t.Helper()
	t.Setenv("CONVERSION_ENVIRONMENT", "sandbox")
	pool := testdb.Open(t)
	ctx := t.Context()

	if _, err := pool.Exec(ctx, `
		INSERT INTO novels(id, title, author, publish_status) VALUES
		(701, 'Reg Novel 1', 'Author', 'published'),
		(702, 'Reg Novel 2', 'Author', 'published')
		ON CONFLICT (id) DO NOTHING;
		SELECT assert_paypal_identity('reg-paypal', 'sandbox');
		UPDATE payment_channel_accounts
		SET status='published', is_primary=true, new_orders_enabled=true, new_subscriptions_enabled=true
		WHERE account_key='reg-paypal';
		INSERT INTO payment_channel_accounts(provider, account_key, environment, credentials_ref, webhook_ref, display_name, status, is_primary, new_orders_enabled, new_subscriptions_enabled)
		VALUES ('stripe', 'acct_RegPrimary1', 'sandbox', 'db:encrypted', 'test', 'Reg Stripe 1', 'published', true, true, true)
		ON CONFLICT (provider, account_key, environment) DO UPDATE SET
			credentials_ref='db:encrypted', status='published', is_primary=true, new_orders_enabled=true, new_subscriptions_enabled=true;
		INSERT INTO system_domains(name, domain, status) VALUES('Reader Reg', 'reader.example.invalid', 1) ON CONFLICT DO NOTHING;
	`); err != nil {
		t.Fatal(err)
	}

	ppAccount := payment.WebhookAccount{AccountKey: "reg-paypal", Environment: "sandbox"}
	stAccount := payment.WebhookAccount{AccountKey: "acct_RegPrimary1", Environment: "sandbox"}
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
	return pool, svc, NewHandler(svc)
}

func testReaderRouter(h *Handler, uid int64) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	api := r.Group("/api/v1/wallet", func(c *gin.Context) {
		c.Set("user_id", uid)
		c.Next()
	})
	api.GET("/recharge/templates", h.GetRechargeTemplates)
	api.POST("/purchase-intents", h.PreparePurchase)
	return r
}

// TC-RDR-01: First-touch attribution remains locked for existing users, and spoofed X-Recharge-Template-ID / X-Promo-ID headers cannot switch templates.
func TestTemplateResolution_FirstTouchLockedAndIgnoresTemplateHeader(t *testing.T) {
	pool, svc, h := setupRegressionReaderFixture(t)
	ctx := t.Context()

	// Seed default T1 (701) and promo T2 (702).
	_ = seedTestProductAndSlot(t, pool, testCatalogSpec{
		SlotID: 7011, TemplateID: 701, TemplateName: "Default T1", IsDefault: true, SlotIndex: 1,
		Code: "RDR_T1_COIN", ProductType: "single", PriceCents: 499, Coins: 500, DisplayAlias: "T1 Starter",
		Environment: "sandbox", PayPalAccountKey: "reg-paypal", StripeAccountKey: "acct_RegPrimary1",
	})
	_ = seedTestProductAndSlot(t, pool, testCatalogSpec{
		SlotID: 7021, TemplateID: 702, TemplateName: "Promo T2", IsDefault: false, SlotIndex: 1,
		Code: "RDR_T2_COIN", ProductType: "single", PriceCents: 999, Coins: 1200, DisplayAlias: "T2 Promo",
		Environment: "sandbox", PayPalAccountKey: "reg-paypal", StripeAccountKey: "acct_RegPrimary1",
	})

	if _, err := pool.Exec(ctx, `
		INSERT INTO promotion_links(id, name, novel_id, novel_title, generated_url, utm_source, utm_campaign, recharge_template_id)
		VALUES (702, 'Promo P2', 701, 'Reg Novel 1', 'https://reader.example.invalid/detail?novel_id=701', 'fb', 'camp_p2', 702);
		INSERT INTO users(id, email, promotion_link_id, promotion_resolved) VALUES
		(701, 'organic-u1@example.invalid', NULL, true),
		(702, 'promo-u2@example.invalid', 702, true);
	`); err != nil {
		t.Fatal(err)
	}

	// U1 (organic, first-touch resolved) sends spoofed X-Promo-ID & X-Recharge-Template-ID headers -> gets T1 only.
	rU1 := testReaderRouter(h, 701)
	reqT := httptest.NewRequest(http.MethodGet, "/api/v1/wallet/recharge/templates", nil)
	reqT.Header.Set("X-Promo-ID", "702")
	reqT.Header.Set("X-Recharge-Template-ID", "702")
	recT := httptest.NewRecorder()
	rU1.ServeHTTP(recT, reqT)
	if recT.Code != http.StatusOK {
		t.Fatalf("U1 GetRechargeTemplates failed (%d): %s", recT.Code, recT.Body.String())
	}
	var gotT1 model.RechargeTemplate
	if err := json.Unmarshal(recT.Body.Bytes(), &gotT1); err != nil {
		t.Fatal(err)
	}
	if gotT1.ID != 701 || len(gotT1.Slots) != 1 || gotT1.Slots[0].ID != 7011 {
		t.Fatalf("expected U1 locked to T1(slot 7011), got %+v", gotT1)
	}

	// U1 cannot check out with T2's slot 7021 even with forged X-Recharge-Template-ID.
	if _, err := svc.PreparePurchase(ctx, 701, 0, 7021, 0, uuid.NewString(), "", checkoutAttribution{}); !errors.Is(err, ErrCheckoutUnavailable) {
		t.Fatalf("expected ErrCheckoutUnavailable for cross-template slot 7021 on U1, got %v", err)
	}
	var u1Promo *int
	if err := pool.QueryRow(ctx, `SELECT promotion_link_id FROM users WHERE id=701`).Scan(&u1Promo); err != nil || u1Promo != nil {
		t.Fatalf("U1 promotion_link_id mutated unexpectedly: %v err=%v", u1Promo, err)
	}

	// U2 (acquired via P2 -> bound on first touch) gets T2 and can check out with slot 7021.
	rU2 := testReaderRouter(h, 702)
	recU2 := httptest.NewRecorder()
	rU2.ServeHTTP(recU2, httptest.NewRequest(http.MethodGet, "/api/v1/wallet/recharge/templates", nil))
	var gotT2 model.RechargeTemplate
	if err := json.Unmarshal(recU2.Body.Bytes(), &gotT2); recU2.Code != http.StatusOK || err != nil || gotT2.ID != 702 || len(gotT2.Slots) != 1 || gotT2.Slots[0].ID != 7021 {
		t.Fatalf("expected U2 bound to T2(slot 7021), code=%d got=%+v err=%v", recU2.Code, gotT2, err)
	}
	if intent, err := svc.PreparePurchase(ctx, 702, 0, 7021, 0, uuid.NewString(), "", checkoutAttribution{}); err != nil || intent.SlotID != 7021 {
		t.Fatalf("expected U2 PreparePurchase on T2 slot 7021 to succeed, intent=%+v err=%v", intent, err)
	}
}

// TC-RDR-02: In-flight Pending VIP intents succeed after in-place cosmetic template updates (preserving recharge_slots.id).
func TestVIPCheckout_ContinuesAfterTemplateCosmeticEdit(t *testing.T) {
	pool, svc, _ := setupRegressionReaderFixture(t)
	ctx := t.Context()

	_ = seedTestProductAndSlot(t, pool, testCatalogSpec{
		SlotID: 711, TemplateID: 710, TemplateName: "VIP Cos", IsDefault: true, SlotIndex: 1,
		Code: "RDR_VIP_COS_1", ProductType: "vip", VipCycle: "month", PriceCents: 1499,
		Environment: "sandbox", PayPalAccountKey: "reg-paypal", PayPalPlanID: "P-COS-1",
		StripeAccountKey: "acct_RegPrimary1", StripeProductRef: "prod_cos1", StripePriceRef: "price_cos1",
	})
	_ = seedTestProductAndSlot(t, pool, testCatalogSpec{
		SlotID: 712, TemplateID: 710, TemplateName: "VIP Cos", IsDefault: true, SlotIndex: 2,
		Code: "RDR_COIN_COS_2", ProductType: "single", PriceCents: 499, Coins: 500,
		Environment: "sandbox", PayPalAccountKey: "reg-paypal", StripeAccountKey: "acct_RegPrimary1",
	})
	if _, err := pool.Exec(ctx, `INSERT INTO users(id, email) VALUES (711, 'cos-pp@example.invalid'), (712, 'cos-st@example.invalid')`); err != nil {
		t.Fatal(err)
	}

	ppIntent, err := svc.PreparePurchase(ctx, 711, 0, 711, 0, uuid.NewString(), "", checkoutAttribution{})
	if err != nil {
		t.Fatal(err)
	}
	stIntent, err := svc.PreparePurchase(ctx, 712, 0, 711, 0, uuid.NewString(), "", checkoutAttribution{})
	if err != nil {
		t.Fatal(err)
	}

	// Simulate in-place CMS template update preserving slot_id=711 while altering template name, badge, and swapping slot_index.
	if _, err := pool.Exec(ctx, `
		UPDATE recharge_templates SET name='VIP Cos Edited', updated_at=now() WHERE id=710;
		UPDATE recharge_slots SET slot_index=-id WHERE template_id=710;
		UPDATE recharge_slots SET slot_index=2, badge='BEST VALUE', is_highlighted=true WHERE id=711;
		UPDATE recharge_slots SET slot_index=1 WHERE id=712;
	`); err != nil {
		t.Fatal(err)
	}

	origin := "https://reader.example.invalid"
	if resPP, err := svc.StartPayPalCheckout(ctx, 711, ppIntent.ID, origin); err != nil || resPP.Status != "awaiting_approval" || resPP.ApprovalURL == "" {
		t.Fatalf("PayPal VIP start failed after cosmetic edit: res=%+v err=%v", resPP, err)
	}
	if resST, err := svc.StartStripeCheckout(ctx, 712, stIntent.ID, origin); err != nil || resST.Status != "awaiting_approval" || resST.ApprovalURL == "" {
		t.Fatalf("Stripe VIP start failed after cosmetic edit: res=%+v err=%v", resST, err)
	}
}

// TC-RDR-03: slot_id=0 is blocked across single, whole_book, and vip (both service & HTTP), leaving zero orphan rows, while valid template slots succeed.
func TestPreparePurchase_BlocksSlotZeroAndRequiresActiveTemplateSlot(t *testing.T) {
	pool, svc, h := setupRegressionReaderFixture(t)
	ctx := t.Context()

	pInCoin := seedTestProductAndSlot(t, pool, testCatalogSpec{
		SlotID: 7211, TemplateID: 720, TemplateName: "Active T720", IsDefault: true, SlotIndex: 1,
		Code: "RDR_IN_COIN", ProductType: "single", PriceCents: 499, Coins: 500,
		Environment: "sandbox", PayPalAccountKey: "reg-paypal", StripeAccountKey: "acct_RegPrimary1",
	})
	pInBook := seedTestProductAndSlot(t, pool, testCatalogSpec{
		SlotID: 7212, TemplateID: 720, TemplateName: "Active T720", IsDefault: true, SlotIndex: 2,
		Code: "RDR_IN_BOOK", ProductType: "whole_book", PriceCents: 999, NovelScopeType: "all_published",
		Environment: "sandbox", PayPalAccountKey: "reg-paypal", StripeAccountKey: "acct_RegPrimary1",
	})
	pInVIP := seedTestProductAndSlot(t, pool, testCatalogSpec{
		SlotID: 7213, TemplateID: 720, TemplateName: "Active T720", IsDefault: true, SlotIndex: 3,
		Code: "RDR_IN_VIP", ProductType: "vip", VipCycle: "month", PriceCents: 1499,
		Environment: "sandbox", PayPalAccountKey: "reg-paypal", PayPalPlanID: "P-IN-VIP",
		StripeAccountKey: "acct_RegPrimary1", StripeProductRef: "prod_invip", StripePriceRef: "price_invip",
	})

	// Seed published dual-channel SKUs outside user's active template (placed on non-default T729).
	pOutCoin := seedTestProductAndSlot(t, pool, testCatalogSpec{
		SlotID: 7291, TemplateID: 729, TemplateName: "Other T729", IsDefault: false, SlotIndex: 1,
		Code: "RDR_OUT_COIN", ProductType: "single", PriceCents: 799, Coins: 800,
		Environment: "sandbox", PayPalAccountKey: "reg-paypal", StripeAccountKey: "acct_RegPrimary1",
	})
	pOutBook := seedTestProductAndSlot(t, pool, testCatalogSpec{
		SlotID: 7292, TemplateID: 729, TemplateName: "Other T729", IsDefault: false, SlotIndex: 2,
		Code: "RDR_OUT_BOOK", ProductType: "whole_book", PriceCents: 1299, NovelScopeType: "all_published",
		Environment: "sandbox", PayPalAccountKey: "reg-paypal", StripeAccountKey: "acct_RegPrimary1",
	})
	pOutVIP := seedTestProductAndSlot(t, pool, testCatalogSpec{
		SlotID: 7293, TemplateID: 729, TemplateName: "Other T729", IsDefault: false, SlotIndex: 3,
		Code: "RDR_OUT_VIP", ProductType: "vip", VipCycle: "year", PriceCents: 8999,
		Environment: "sandbox", PayPalAccountKey: "reg-paypal", PayPalPlanID: "P-OUT-VIP",
		StripeAccountKey: "acct_RegPrimary1", StripeProductRef: "prod_outvip", StripePriceRef: "price_outvip",
	})

	if _, err := pool.Exec(ctx, `INSERT INTO users(id, email) VALUES (721, 'slotzero@example.invalid'), (722, 'valid-vip@example.invalid')`); err != nil {
		t.Fatal(err)
	}

	router := testReaderRouter(h, 721)
	for _, tc := range []struct {
		name      string
		productID int64
		novelID   int64
	}{
		{"in_single", pInCoin, 0},
		{"in_whole_book", pInBook, 701},
		{"in_vip", pInVIP, 0},
		{"out_single", pOutCoin, 0},
		{"out_whole_book", pOutBook, 701},
		{"out_vip", pOutVIP, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := svc.PreparePurchase(ctx, 721, tc.productID, 0, tc.novelID, uuid.NewString(), "", checkoutAttribution{}); !errors.Is(err, ErrCheckoutUnavailable) {
				t.Fatalf("expected ErrCheckoutUnavailable for slot_id=0 (%s), got %v", tc.name, err)
			}
			body := fmt.Sprintf(`{"product_id":%d,"slot_id":0,"novel_id":%d,"request_key":%q}`, tc.productID, tc.novelID, uuid.NewString())
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/v1/wallet/purchase-intents", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected HTTP 400 for slot_id=0 (%s), got %d: %s", tc.name, rec.Code, rec.Body.String())
			}
		})
	}

	var orphanOrders, orphanVIP int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM recharge_orders WHERE user_id=721`).Scan(&orphanOrders); err != nil || orphanOrders != 0 {
		t.Fatalf("unexpected recharge_orders left for user 721: %d err=%v", orphanOrders, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM payment_vip_checkout_intents WHERE user_id=721`).Scan(&orphanVIP); err != nil || orphanVIP != 0 {
		t.Fatalf("unexpected payment_vip_checkout_intents left for user 721: %d err=%v", orphanVIP, err)
	}

	// Cross-product mismatch on valid slot must be rejected.
	if _, err := svc.PreparePurchase(ctx, 721, pInBook, 7211, 701, uuid.NewString(), "", checkoutAttribution{}); !errors.Is(err, ErrCheckoutUnavailable) {
		t.Fatalf("expected ErrCheckoutUnavailable on slot/product mismatch, got %v", err)
	}

	// Valid active template slots succeed across single, whole_book, and vip.
	if intent, err := svc.PreparePurchase(ctx, 721, pInCoin, 7211, 0, uuid.NewString(), "", checkoutAttribution{}); err != nil || intent.OrderType != "single" {
		t.Fatalf("valid single failed: %+v err=%v", intent, err)
	}
	if intent, err := svc.PreparePurchase(ctx, 721, pInBook, 7212, 701, uuid.NewString(), "", checkoutAttribution{}); err != nil || intent.OrderType != "whole_book" {
		t.Fatalf("valid whole_book failed: %+v err=%v", intent, err)
	}
	if intent, err := svc.PreparePurchase(ctx, 722, pInVIP, 7213, 0, uuid.NewString(), "", checkoutAttribution{}); err != nil || intent.OrderType != "subscription" {
		t.Fatalf("valid vip failed: %+v err=%v", intent, err)
	}
}

// TC-RDR-04: Switching the primary Stripe account immediately excludes products not verified-bound on the new primary.
func TestCatalog_PrimarySwitchImmediatelyHidesUnboundAccountProducts(t *testing.T) {
	pool, svc, _ := setupRegressionReaderFixture(t)
	ctx := t.Context()

	_ = seedTestProductAndSlot(t, pool, testCatalogSpec{
		SlotID: 731, TemplateID: 730, TemplateName: "Switch T730", IsDefault: true, SlotIndex: 1,
		Code: "RDR_SWITCH_A", ProductType: "single", PriceCents: 499, Coins: 500,
		Environment: "sandbox", PayPalAccountKey: "reg-paypal", StripeAccountKey: "acct_RegPrimary1",
	})
	if _, err := pool.Exec(ctx, `INSERT INTO users(id, email) VALUES (731, 'switch-rdr@example.invalid')`); err != nil {
		t.Fatal(err)
	}

	beforeTpl, err := svc.GetRechargeTemplates(ctx, 731, 0)
	if err != nil || len(beforeTpl.Slots) != 1 || beforeTpl.Slots[0].ID != 731 {
		t.Fatalf("expected slot 731 visible before primary switch, got %+v err=%v", beforeTpl, err)
	}

	// Atomically promote acct_RegPrimary2 (published, primary, NO binding for RDR_SWITCH_A)
	if _, err := pool.Exec(ctx, `
		UPDATE payment_channel_accounts SET is_primary=false WHERE provider='stripe' AND environment='sandbox';
		INSERT INTO payment_channel_accounts(provider, account_key, environment, credentials_ref, webhook_ref, display_name, status, is_primary, new_orders_enabled, new_subscriptions_enabled)
		VALUES ('stripe', 'acct_RegPrimary2', 'sandbox', 'db:encrypted', 'test', 'Reg Stripe 2', 'published', true, true, true);
	`); err != nil {
		t.Fatal(err)
	}

	afterTpl, err := svc.GetRechargeTemplates(ctx, 731, 0)
	if err != nil || len(afterTpl.Slots) != 0 {
		t.Fatalf("expected slot 731 hidden immediately after primary switch, got %+v err=%v", afterTpl, err)
	}
	if _, err := svc.PreparePurchase(ctx, 731, 0, 731, 0, uuid.NewString(), "", checkoutAttribution{}); !errors.Is(err, ErrCheckoutUnavailable) {
		t.Fatalf("expected ErrCheckoutUnavailable for unbound new primary account, got %v", err)
	}
}

// TC-RDR-05 (R-1): Even if a dirty sandbox row with is_default=true is injected into DB, live catalog/checkout ignores it and selects the live default template.
func TestReaderDefaultTemplateIgnoresSandboxDefaultRow(t *testing.T) {
	pool, svc, _ := setupRegressionReaderFixture(t)
	ctx := t.Context()

	// Seed live channels and a valid live default template (ID=740, slot=7401) + live whole_book slot (7402).
	if _, err := pool.Exec(ctx, `
		INSERT INTO payment_channel_accounts(provider, account_key, environment, credentials_ref, webhook_ref, display_name, status, is_primary, new_orders_enabled, new_subscriptions_enabled)
		VALUES
		('paypal', 'reg-paypal-live', 'live', 'env:pp', 'env:pp', 'Reg PayPal Live', 'published', true, true, true),
		('stripe', 'acct_RegLive1', 'live', 'db:encrypted', 'test', 'Reg Stripe Live', 'published', true, true, true);
		INSERT INTO users(id, email) VALUES (741, 'live-isolation@example.invalid');
	`); err != nil {
		t.Fatal(err)
	}

	t.Setenv("CONVERSION_ENVIRONMENT", "production")
	pLive := seedTestProductAndSlot(t, pool, testCatalogSpec{
		SlotID: 7401, TemplateID: 740, TemplateName: "Live Official Default", IsDefault: true, SlotIndex: 1,
		Code: "RDR_LIVE_DEF_COIN", ProductType: "single", PriceCents: 499, Coins: 500,
		Environment: "live", PayPalAccountKey: "reg-paypal-live", StripeAccountKey: "acct_RegLive1",
	})
	_ = seedTestProductAndSlot(t, pool, testCatalogSpec{
		SlotID: 7402, TemplateID: 740, TemplateName: "Live Official Default", IsDefault: true, SlotIndex: 2,
		Code: "RDR_LIVE_DEF_BOOK", ProductType: "whole_book", PriceCents: 999, NovelScopeType: "all_published",
		Environment: "live", PayPalAccountKey: "reg-paypal-live", StripeAccountKey: "acct_RegLive1",
	})

	// Inject a dirty sandbox template with LOWER ID=739 and is_default=true.
	pSbxDirty := seedTestProductAndSlot(t, pool, testCatalogSpec{
		SlotID: 7391, TemplateID: 739, TemplateName: "Dirty Sandbox Default", IsDefault: true, SlotIndex: 1,
		Code: "RDR_SBX_DIRTY_COIN", ProductType: "single", PriceCents: 199, Coins: 200,
		Environment: "sandbox", PayPalAccountKey: "reg-paypal", StripeAccountKey: "acct_RegPrimary1",
	})
	if _, err := pool.Exec(ctx, `UPDATE recharge_templates SET environment='sandbox', is_default=true WHERE id=739`); err != nil {
		t.Fatal(err)
	}

	// Reader catalog in live mode must still resolve template 740, NOT 739.
	tpl, err := svc.GetRechargeTemplates(ctx, 741, 701)
	if err != nil {
		t.Fatalf("GetRechargeTemplates failed: %v", err)
	}
	if tpl.ID != 740 || len(tpl.Slots) != 2 || tpl.Slots[0].ID != 7401 {
		t.Fatalf("expected live template 740 with live slots, got id=%d slots=%+v", tpl.ID, tpl.Slots)
	}

	// Checkout with live slot 7401 succeeds; dirty sandbox slot 7391 is blocked.
	if intent, err := svc.PreparePurchase(ctx, 741, pLive, 7401, 0, uuid.NewString(), "", checkoutAttribution{}); err != nil || intent.SlotID != 7401 {
		t.Fatalf("expected live PreparePurchase on slot 7401 to succeed, intent=%+v err=%v", intent, err)
	}
	if _, err := svc.PreparePurchase(ctx, 741, pSbxDirty, 7391, 0, uuid.NewString(), "", checkoutAttribution{}); !errors.Is(err, ErrCheckoutUnavailable) {
		t.Fatalf("expected ErrCheckoutUnavailable for dirty sandbox slot 7391, got %v", err)
	}

	// whole_book_offer_available DB function also rejects template 739 and accepts template 740.
	var okLive, okSbx bool
	if err := pool.QueryRow(ctx, `SELECT whole_book_offer_available(741, 740, 701), whole_book_offer_available(741, 739, 701)`).
		Scan(&okLive, &okSbx); err != nil || !okLive || okSbx {
		t.Fatalf("whole_book_offer_available live=%v sbx=%v err=%v", okLive, okSbx, err)
	}
}

// Sandbox promotion links work with sandbox templates in sandbox mode and never leak into live production mode.
func TestSandboxPromotionLinkIsolatedFromLiveAndActiveInSandbox(t *testing.T) {
	pool, svc, _ := setupRegressionReaderFixture(t)
	ctx := t.Context()

	if _, err := pool.Exec(ctx, `
		INSERT INTO payment_channel_accounts(provider, account_key, environment, credentials_ref, webhook_ref, display_name, status, is_primary, new_orders_enabled, new_subscriptions_enabled)
		VALUES
		('paypal', 'reg-paypal-live-iso', 'live', 'env:pp', 'env:pp', 'Reg PayPal Live', 'published', true, true, true),
		('stripe', 'acct_RegLiveIso1', 'live', 'db:encrypted', 'test', 'Reg Stripe Live', 'published', true, true, true);
	`); err != nil {
		t.Fatal(err)
	}

	_ = seedTestProductAndSlot(t, pool, testCatalogSpec{
		SlotID: 7501, TemplateID: 750, TemplateName: "Live Default Promo Iso", IsDefault: true, SlotIndex: 1,
		Code: "RDR_LIVE_PROMO_ISO", ProductType: "single", PriceCents: 499, Coins: 500,
		Environment: "live", PayPalAccountKey: "reg-paypal-live-iso", StripeAccountKey: "acct_RegLiveIso1",
	})
	pSbx := seedTestProductAndSlot(t, pool, testCatalogSpec{
		SlotID: 7511, TemplateID: 751, TemplateName: "Sandbox Promo Template", IsDefault: false, SlotIndex: 1,
		Code: "RDR_SBX_PROMO_ISO", ProductType: "single", PriceCents: 199, Coins: 200,
		Environment: "sandbox", PayPalAccountKey: "reg-paypal", StripeAccountKey: "acct_RegPrimary1",
	})
	if _, err := pool.Exec(ctx, `
		UPDATE recharge_templates SET environment='sandbox' WHERE id=751;
		INSERT INTO promotion_links(id, name, environment, novel_id, novel_title, utm_source, utm_campaign, generated_url, recharge_template_id)
		VALUES (7510, 'Sandbox Test Promo', 'sandbox', 701, 'Reg Novel', 'fb', 'sbx_camp', 'https://example.test/detail?novel_id=701', 751);
		INSERT INTO users(id, email, promotion_link_id, promotion_resolved)
		VALUES (751, 'sbx-promo-user@example.invalid', 7510, true);
	`); err != nil {
		t.Fatal(err)
	}

	// 1. In sandbox mode: resolves sandbox promotion link & sandbox template 751 and allows checkout.
	t.Setenv("CONVERSION_ENVIRONMENT", "sandbox")
	var resolvedSbx *int
	if err := pool.QueryRow(ctx, `SELECT resolve_reader_promotion_for_env(7510, '', '', 'sandbox')`).Scan(&resolvedSbx); err != nil || resolvedSbx == nil || *resolvedSbx != 7510 {
		t.Fatalf("expected sandbox promotion 7510 to resolve in sandbox, got %v err=%v", resolvedSbx, err)
	}
	sbxTpl, err := svc.GetRechargeTemplates(ctx, 751, 701)
	if err != nil || sbxTpl.ID != 751 || len(sbxTpl.Slots) != 1 || sbxTpl.Slots[0].ID != 7511 {
		t.Fatalf("expected sandbox template 751 with slot 7511 in sandbox mode, got %+v err=%v", sbxTpl, err)
	}
	if intent, err := svc.PreparePurchase(ctx, 751, pSbx, 7511, 0, uuid.NewString(), "", checkoutAttribution{}); err != nil || intent.SlotID != 7511 {
		t.Fatalf("expected sandbox PreparePurchase on slot 7511 to succeed, intent=%+v err=%v", intent, err)
	}

	// 2. In live (production) mode: sandbox promotion 7510 is ignored and reader falls back to live default template 750.
	t.Setenv("CONVERSION_ENVIRONMENT", "production")
	var resolvedLive *int
	if err := pool.QueryRow(ctx, `SELECT resolve_reader_promotion_for_env(7510, '', '', 'live')`).Scan(&resolvedLive); err != nil || resolvedLive != nil {
		t.Fatalf("expected sandbox promotion 7510 NOT to resolve in live, got %v err=%v", resolvedLive, err)
	}
	liveTpl, err := svc.GetRechargeTemplates(ctx, 751, 701)
	if err != nil || liveTpl.ID != 750 || len(liveTpl.Slots) != 1 || liveTpl.Slots[0].ID != 7501 {
		t.Fatalf("expected live fallback template 750 with slot 7501 in production mode, got %+v err=%v", liveTpl, err)
	}
	if _, err := svc.PreparePurchase(ctx, 751, pSbx, 7511, 0, uuid.NewString(), "", checkoutAttribution{}); !errors.Is(err, ErrCheckoutUnavailable) {
		t.Fatalf("expected sandbox slot 7511 blocked in live mode, got %v", err)
	}

	// 3. Fail-closed behavior: empty, NULL, or unknown env must NOT resolve sandbox promotion links.
	for _, envExpr := range []string{`''`, `NULL::text`, `'staging'`} {
		var resolved *int
		if err := pool.QueryRow(ctx, `SELECT resolve_reader_promotion_for_env(7510, '', '', `+envExpr+`)`).Scan(&resolved); err != nil || resolved != nil {
			t.Fatalf("expected sandbox promotion 7510 fail-closed for env=%s, got %v err=%v", envExpr, resolved, err)
		}
		var userPromoCount int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM reader_promotion_for_user_env(751, `+envExpr+`)`).Scan(&userPromoCount); err != nil || userPromoCount != 0 {
			t.Fatalf("expected reader_promotion_for_user_env fail-closed for env=%s, got %d err=%v", envExpr, userPromoCount, err)
		}
	}

	// 4. UTM fallback in sandbox prefers unique sandbox match over a live link sharing the same UTM.
	if _, err := pool.Exec(ctx, `
		INSERT INTO promotion_links(id, name, environment, novel_id, novel_title, chapter_index, utm_source, utm_campaign, generated_url)
		VALUES (7520, 'Shared UTM Live', 'live', 701, 'Book 701', 1, 'fb', 'shared_campaign', 'https://example.test/live'),
		       (7521, 'Shared UTM Sandbox', 'sandbox', 701, 'Book 701', 1, 'fb', 'shared_campaign', 'https://example.test/sbx')
	`); err != nil {
		t.Fatal(err)
	}
	var utmSbx, utmLive, utmEmpty *int
	if err := pool.QueryRow(ctx, `SELECT
		resolve_reader_promotion_for_env(0, 'fb', 'shared_campaign', 'sandbox'),
		resolve_reader_promotion_for_env(0, 'fb', 'shared_campaign', 'live'),
		resolve_reader_promotion_for_env(0, 'fb', 'shared_campaign', '')`).Scan(&utmSbx, &utmLive, &utmEmpty); err != nil {
		t.Fatal(err)
	}
	if utmSbx == nil || *utmSbx != 7521 || utmLive == nil || *utmLive != 7520 || utmEmpty == nil || *utmEmpty != 7520 {
		t.Fatalf("unexpected UTM fallback resolution: sandbox=%v live=%v empty=%v", utmSbx, utmLive, utmEmpty)
	}
}
