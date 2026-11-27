package billing

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"star-novel-cms/internal/db"
)

type mockStripeState struct {
	mu           sync.Mutex
	prodSeq      int
	priceSeq     int
	products     map[string]*mockStripeProduct
	prices       map[string]*mockStripePrice
	prodCreates  int
	prodUpdates  int
	priceCreates int
	priceIdems   []string
	accountCalls int
}

type mockStripeProduct struct {
	ID       string
	Active   bool
	Livemode bool
	TaxCode  string
}

type mockStripePrice struct {
	ID          string
	ProductID   string
	UnitAmount  int64
	Kind        string
	Interval    string
	TaxBehavior string
	Active      bool
	Livemode    bool
}

func newMockStripeState() *mockStripeState {
	return &mockStripeState{
		products: make(map[string]*mockStripeProduct),
		prices:   make(map[string]*mockStripePrice),
	}
}

func (s *mockStripeState) serveHTTP(w http.ResponseWriter, req *http.Request) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	live := strings.Contains(req.Header.Get("Authorization"), "_live_")
	switch {
	case req.URL.Path == "/v1/account":
		s.accountCalls++
		acctID := "acct_StripeSbx01"
		switch {
		case strings.Contains(req.Header.Get("Authorization"), "acct2"):
			acctID = "acct_StripeSbx02"
		case live:
			acctID = "acct_StripeLive01"
		}
		_, _ = fmt.Fprintf(w, `{"id":%q}`, acctID)
		return true

	case req.Method == http.MethodGet && req.URL.Path == "/v1/webhook_endpoints":
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"type":"permission_error","code":"rak_insufficient_permissions"}}`))
		return true

	case req.Method == http.MethodPost && req.URL.Path == "/v1/products":
		if err := req.ParseForm(); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return true
		}
		s.prodSeq++
		s.prodCreates++
		prefix := "prod_sbx_"
		if live {
			prefix = "prod_live_"
		}
		id := fmt.Sprintf("%s%d", prefix, s.prodSeq)
		s.products[id] = &mockStripeProduct{
			ID:       id,
			Active:   true,
			Livemode: live,
			TaxCode:  req.PostForm.Get("tax_code"),
		}
		_, _ = fmt.Fprintf(w, `{"id":%q,"active":true,"livemode":%v}`, id, live)
		return true

	case req.Method == http.MethodPost && strings.HasPrefix(req.URL.Path, "/v1/products/"):
		prodID := strings.TrimPrefix(req.URL.Path, "/v1/products/")
		prod, ok := s.products[prodID]
		if !ok || errCheck(req.ParseForm()) {
			w.WriteHeader(http.StatusNotFound)
			return true
		}
		s.prodUpdates++
		if tc := req.PostForm.Get("tax_code"); tc != "" {
			prod.TaxCode = tc
		}
		_, _ = fmt.Fprintf(w, `{"id":%q,"active":%v,"livemode":%v,"tax_code":%q}`, prod.ID, prod.Active, prod.Livemode, prod.TaxCode)
		return true

	case req.Method == http.MethodPost && req.URL.Path == "/v1/prices":
		if err := req.ParseForm(); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return true
		}
		s.priceSeq++
		s.priceCreates++
		s.priceIdems = append(s.priceIdems, req.Header.Get("Idempotency-Key"))
		prefix := "price_sbx_"
		if live {
			prefix = "price_live_"
		}
		id := fmt.Sprintf("%s%d", prefix, s.priceSeq)
		unitAmt, _ := strconv.ParseInt(req.PostForm.Get("unit_amount"), 10, 64)
		kind := "one_time"
		interval := req.PostForm.Get("recurring[interval]")
		if interval != "" {
			kind = "recurring"
		}
		tb := req.PostForm.Get("tax_behavior")
		if tb == "" {
			tb = "unspecified"
		}
		s.prices[id] = &mockStripePrice{
			ID:          id,
			ProductID:   req.PostForm.Get("product"),
			UnitAmount:  unitAmt,
			Kind:        kind,
			Interval:    interval,
			TaxBehavior: tb,
			Active:      true,
			Livemode:    live,
		}
		_, _ = fmt.Fprintf(w, `{"id":%q,"active":true,"livemode":%v}`, id, live)
		return true

	case req.Method == http.MethodGet && strings.HasPrefix(req.URL.Path, "/v1/prices/"):
		priceID := strings.TrimPrefix(req.URL.Path, "/v1/prices/")
		price, ok := s.prices[priceID]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return true
		}
		prod := s.products[price.ProductID]
		if prod == nil {
			w.WriteHeader(http.StatusNotFound)
			return true
		}
		recJSON := "null"
		if price.Kind == "recurring" {
			recJSON = fmt.Sprintf(`{"interval":%q,"interval_count":1}`, price.Interval)
		}
		_, _ = fmt.Fprintf(w,
			`{"id":%q,"currency":"usd","type":%q,"tax_behavior":%q,"unit_amount":%d,"active":%v,"livemode":%v,"recurring":%s,"product":{"id":%q,"active":%v,"livemode":%v,"tax_code":%q}}`,
			price.ID, price.Kind, price.TaxBehavior, price.UnitAmount, price.Active, price.Livemode, recJSON,
			prod.ID, prod.Active, prod.Livemode, prod.TaxCode,
		)
		return true
	}
	return false
}

func errCheck(err error) bool { return err != nil }

func setupChannelWithPublish(t *testing.T, r http.Handler, payload map[string]any, setPrimary bool) int64 {
	t.Helper()
	rec := doJSON(t, r, http.MethodPost, "/cms/payments/channels", "cfg-super", payload)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create channel failed (%d): %s", rec.Code, rec.Body.String())
	}
	var created struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	if v := doJSON(t, r, http.MethodPost, fmt.Sprintf("/cms/payments/channels/%d/validate", created.ID), "cfg-super", nil); v.Code != http.StatusOK {
		t.Fatalf("validate channel %d failed (%d): %s", created.ID, v.Code, v.Body.String())
	}
	if p := doJSON(t, r, http.MethodPost, fmt.Sprintf("/cms/payments/channels/%d/publish", created.ID), "cfg-super", map[string]any{"set_primary": setPrimary}); p.Code != http.StatusOK {
		t.Fatalf("publish channel %d failed (%d): %s", created.ID, p.Code, p.Body.String())
	}
	return created.ID
}

func createRegressionProduct(t *testing.T, r http.Handler, body map[string]any) int64 {
	t.Helper()
	rec := doJSON(t, r, http.MethodPost, "/cms/products", "cfg-finance", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create product failed (%d): %s", rec.Code, rec.Body.String())
	}
	var out struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return out.ID
}

func setupRegressionMockEnv(t *testing.T, customHandler func(http.ResponseWriter, *http.Request) bool) (*gin.Engine, context.Context, *mockStripeState) {
	t.Helper()
	r, ctx := setupChannelCatalogRouter(t)
	st := newMockStripeState()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if customHandler != nil && customHandler(w, req) {
			return
		}
		if st.serveHTTP(w, req) {
			return
		}
		switch {
		case req.URL.Path == "/v1/oauth2/token":
			_, _ = w.Write([]byte(`{"access_token":"tok_pp_reg"}`))
		case strings.HasPrefix(req.URL.Path, "/v1/notifications/webhooks/"):
			whID := strings.TrimPrefix(req.URL.Path, "/v1/notifications/webhooks/")
			_, _ = fmt.Fprintf(w, `{"id":%q,"url":"https://example.test/webhook"}`, whID)
		case req.URL.Path == "/v1/catalogs/products":
			_, _ = w.Write([]byte(`{"id":"PROD-PP-REG"}`))
		case req.URL.Path == "/v1/billing/plans":
			_, _ = w.Write([]byte(`{"id":"P-REG-1"}`))
		case strings.HasPrefix(req.URL.Path, "/v1/billing/plans/"):
			planID := strings.TrimPrefix(req.URL.Path, "/v1/billing/plans/")
			_, _ = fmt.Fprintf(w, `{"id":%q,"product_id":"PROD-PP-REG","status":"ACTIVE","billing_cycles":[{"tenure_type":"REGULAR","total_cycles":0,"frequency":{"interval_unit":"MONTH","interval_count":1},"pricing_scheme":{"fixed_price":{"value":"9.99","currency_code":"USD"}}}]}`, planID)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	origPPSbx, origPPLive, origST := paypalAPIBaseSandbox, paypalAPIBaseLive, stripeAPIBaseURL
	paypalAPIBaseSandbox, paypalAPIBaseLive, stripeAPIBaseURL = srv.URL, srv.URL, srv.URL
	t.Cleanup(func() {
		paypalAPIBaseSandbox, paypalAPIBaseLive, stripeAPIBaseURL = origPPSbx, origPPLive, origST
	})
	return r, ctx, st
}

// TC-CMS-01: Updating Stripe sales_mode / tax_code / tax_behavior selectively updates Product tax_code and creates a new Price revision without idempotency collision.
func TestStripeBinding_UpdateTaxCodeAndBehaviorReprovisionsPrice(t *testing.T) {
	r, ctx, st := setupRegressionMockEnv(t, nil)

	setupChannelWithPublish(t, r, map[string]any{
		"provider": "stripe", "environment": "sandbox", "display_name": "Stripe Sandbox",
		"account_key": "acct_StripeSbx01", "api_key": "sk_test_sbx_1234", "webhook_secret": "whsec_sbx_1234",
	}, true)
	setupChannelWithPublish(t, r, map[string]any{
		"provider": "stripe", "environment": "live", "display_name": "Stripe Live",
		"account_key": "acct_StripeLive01", "api_key": "sk_live_live_1234", "webhook_secret": "whsec_live_1234",
	}, true)

	prodID := createRegressionProduct(t, r, map[string]any{
		"code": "REG_TAX_COIN_999", "product_type": "single", "name": "Reg Coin 999",
		"price_cents": 999, "coins": 1000, "bonus": 50,
	})

	// Sandbox verification: direct -> MoR forces tax_code update & new inclusive price.
	sbxRec1 := doJSON(t, r, http.MethodPut, fmt.Sprintf("/cms/products/%d/bindings/stripe", prodID), "cfg-super", map[string]any{
		"environment": "sandbox", "sales_mode": "direct", "countries": []string{"US"}, "reason": "sbx direct",
	})
	if sbxRec1.Code != http.StatusOK {
		t.Fatalf("sandbox direct bind failed (%d): %s", sbxRec1.Code, sbxRec1.Body.String())
	}
	st.mu.Lock()
	st.products["prod_sbx_1"].TaxCode = "txcd_20030000"
	st.mu.Unlock()

	sbxRec2 := doJSON(t, r, http.MethodPut, fmt.Sprintf("/cms/products/%d/bindings/stripe", prodID), "cfg-super", map[string]any{
		"environment": "sandbox", "sales_mode": "merchant_of_record",
		"tax_behavior": "inclusive",
		"countries":    []string{"US"}, "reason": "sbx MoR upgrade",
	})
	if sbxRec2.Code != http.StatusOK || !strings.Contains(sbxRec2.Body.String(), `"external_price_ref":"price_sbx_2"`) {
		t.Fatalf("sandbox MoR upgrade expected price_sbx_2, got (%d): %s", sbxRec2.Code, sbxRec2.Body.String())
	}

	// Live 5-step sequence:
	liveBind := func(payload map[string]any) string {
		t.Helper()
		rec := doJSON(t, r, http.MethodPut, fmt.Sprintf("/cms/products/%d/bindings/stripe", prodID), "cfg-super", payload)
		if rec.Code != http.StatusOK {
			t.Fatalf("live bind %v failed (%d): %s", payload, rec.Code, rec.Body.String())
		}
		var resp struct {
			PriceRef string `json:"external_price_ref"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &resp)
		return resp.PriceRef
	}

	// Step 1: direct -> prod_live_2 (seq=2 after sbx) / price_live_3 (seq=3)
	p1 := liveBind(map[string]any{
		"environment": "live", "sales_mode": "direct", "countries": []string{"US"},
		"release_evidence": "ticket-1", "market_policy": "US-standard", "reason": "live direct",
	})

	// Step 2: direct -> merchant_of_record (default single tax_code txcd_10000000, exclusive) -> updates product tax_code + creates p2
	p2 := liveBind(map[string]any{
		"environment": "live", "sales_mode": "merchant_of_record", "tax_behavior": "exclusive",
		"countries": []string{"US"}, "release_evidence": "ticket-2", "market_policy": "US-MoR", "reason": "upgrade MoR",
	})
	if p2 == p1 {
		t.Fatalf("expected new exclusive price after direct->MoR, kept %s", p2)
	}

	// Step 3: switch tax_behavior exclusive -> inclusive -> creates p3 and updates validated_catalog
	p3 := liveBind(map[string]any{
		"environment": "live", "sales_mode": "merchant_of_record", "tax_behavior": "inclusive",
		"countries": []string{"US"}, "release_evidence": "ticket-3", "market_policy": "US-MoR", "reason": "switch inclusive",
	})
	if p3 == p2 {
		t.Fatalf("expected new inclusive price, kept %s", p3)
	}
	var vcPriceID, vcTaxBehavior, vcTaxCode string
	if err := db.DB.QueryRow(ctx, `
		SELECT validated_catalog->>'id', validated_catalog->>'tax_behavior', validated_catalog->'product'->>'tax_code'
		FROM product_channel_bindings WHERE product_id=$1 AND environment='live' AND provider='stripe'`, prodID).
		Scan(&vcPriceID, &vcTaxBehavior, &vcTaxCode); err != nil || vcPriceID != p3 || vcTaxBehavior != "inclusive" || vcTaxCode != "txcd_10000000" {
		t.Fatalf("step 3 validated_catalog mismatch: id=%s tb=%s tc=%s err=%v", vcPriceID, vcTaxBehavior, vcTaxCode, err)
	}

	// Step 4: switch tax_code -> txcd_10302003 (keep inclusive) -> updates Product tax_code & validated_catalog
	p4TaxOnly := liveBind(map[string]any{
		"environment": "live", "sales_mode": "merchant_of_record", "tax_code": "txcd_10302003", "tax_behavior": "inclusive",
		"countries": []string{"US"}, "release_evidence": "ticket-4", "market_policy": "US-MoR", "reason": "update tax_code",
	})
	if p4TaxOnly != p3 {
		t.Fatalf("tax_code-only change unexpectedly recreated price: %s -> %s", p3, p4TaxOnly)
	}
	if err := db.DB.QueryRow(ctx, `
		SELECT validated_catalog->'product'->>'tax_code'
		FROM product_channel_bindings WHERE product_id=$1 AND environment='live' AND provider='stripe'`, prodID).
		Scan(&vcTaxCode); err != nil || vcTaxCode != "txcd_10302003" {
		t.Fatalf("step 4 validated_catalog tax_code=%q err=%v", vcTaxCode, err)
	}

	// Step 5: switch back to exclusive -> creates a NEW price p5 (no 24h idempotency key collision back to p2)
	p5 := liveBind(map[string]any{
		"environment": "live", "sales_mode": "merchant_of_record", "tax_code": "txcd_10302003", "tax_behavior": "exclusive",
		"countries": []string{"US"}, "release_evidence": "ticket-5", "market_policy": "US-MoR", "reason": "back to exclusive",
	})
	if p5 == p2 || p5 == p3 {
		t.Fatalf("expected brand new exclusive price after switching back, got %s (p2=%s p3=%s)", p5, p2, p3)
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.prodUpdates < 3 {
		t.Fatalf("expected >=3 product tax_code updates (sbx+step2+step4), got %d", st.prodUpdates)
	}
	seen := map[string]bool{}
	for _, k := range st.priceIdems {
		if seen[k] {
			t.Fatalf("duplicate price idempotency key reused across revisions: %q", k)
		}
		seen[k] = true
	}
}

// TC-CMS-02: Channel bindings reject draft/disabled accounts and retired products BEFORE issuing external HTTP calls.
func TestBinding_RejectsDraftDisabledAccountAndRetiredProduct(t *testing.T) {
	httpCalls := 0
	r, ctx, _ := setupRegressionMockEnv(t, func(_ http.ResponseWriter, req *http.Request) bool {
		if req.URL.Path != "/v1/oauth2/token" && req.URL.Path != "/v1/account" {
			httpCalls++
		} else {
			httpCalls++
		}
		return false
	})

	// Create draft Stripe and disabled PayPal accounts.
	draftSt := doJSON(t, r, http.MethodPost, "/cms/payments/channels", "cfg-super", map[string]any{
		"provider": "stripe", "environment": "sandbox", "display_name": "Draft Stripe",
		"account_key": "acct_StripeSbx01", "api_key": "sk_test_draft_1234", "webhook_secret": "whsec_draft_1234",
	})
	var draftStObj struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(draftSt.Body.Bytes(), &draftStObj)

	disabledPPID := setupChannelWithPublish(t, r, map[string]any{
		"provider": "paypal", "environment": "sandbox", "display_name": "PP To Disable",
		"account_key": "pp_dis_1", "client_secret": "pp_sec_dis", "webhook_id": "WH_DIS_1",
	}, true)
	if dec := doJSON(t, r, http.MethodPost, fmt.Sprintf("/cms/payments/channels/%d/deactivate", disabledPPID), "cfg-super", nil); dec.Code != http.StatusOK {
		t.Fatalf("deactivate failed (%d): %s", dec.Code, dec.Body.String())
	}

	// Published primary accounts for verifying retired product guard:
	activePPID := setupChannelWithPublish(t, r, map[string]any{
		"provider": "paypal", "environment": "sandbox", "display_name": "PP Active",
		"account_key": "pp_act_1", "client_secret": "pp_sec_act", "webhook_id": "WH_ACT_1",
	}, true)
	activeStID := setupChannelWithPublish(t, r, map[string]any{
		"provider": "stripe", "environment": "sandbox", "display_name": "ST Active",
		"account_key": "acct_StripeSbx02", "api_key": "sk_test_act_acct2_1234", "webhook_secret": "whsec_act_1234",
	}, true)

	prodA := createRegressionProduct(t, r, map[string]any{
		"code": "REG_GUARD_A", "product_type": "single", "name": "Guard A", "price_cents": 499, "coins": 400,
	})
	prodB := createRegressionProduct(t, r, map[string]any{
		"code": "REG_GUARD_B", "product_type": "single", "name": "Guard B", "price_cents": 999, "coins": 900,
	})
	// Mark prodB as retired under live (so bind under live fails 409).
	if _, err := db.DB.Exec(ctx, `UPDATE product_publications SET status='retired' WHERE product_id=$1 AND environment='live'`, prodB); err != nil {
		t.Fatal(err)
	}
	// Also create published live accounts to confirm retired product stops before account check or HTTP.
	_ = setupChannelWithPublish(t, r, map[string]any{
		"provider": "paypal", "environment": "live", "display_name": "PP Live",
		"account_key": "pp_live_1", "client_secret": "pp_sec_live", "webhook_id": "WH_LIVE_1",
	}, true)
	_ = setupChannelWithPublish(t, r, map[string]any{
		"provider": "stripe", "environment": "live", "display_name": "ST Live",
		"account_key": "acct_StripeLive01", "api_key": "sk_live_act_1234", "webhook_secret": "whsec_act_live",
	}, true)

	httpCalls = 0
	// Draft Stripe & disabled PayPal -> 409 without external HTTP calls.
	if rec := doJSON(t, r, http.MethodPut, fmt.Sprintf("/cms/products/%d/bindings/stripe", prodA), "cfg-super", map[string]any{
		"environment": "sandbox", "account_id": draftStObj.ID, "sales_mode": "direct", "countries": []string{"US"}, "reason": "draft",
	}); rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 for draft Stripe account, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec := doJSON(t, r, http.MethodPut, fmt.Sprintf("/cms/products/%d/bindings/paypal", prodA), "cfg-super", map[string]any{
		"environment": "sandbox", "account_id": disabledPPID, "reason": "disabled",
	}); rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 for disabled PayPal account, got %d: %s", rec.Code, rec.Body.String())
	}

	// Retired product -> 409 on both Stripe & PayPal without HTTP calls.
	if rec := doJSON(t, r, http.MethodPut, fmt.Sprintf("/cms/products/%d/bindings/stripe", prodB), "cfg-super", map[string]any{
		"environment": "live", "sales_mode": "direct", "countries": []string{"US"},
		"release_evidence": "ev", "market_policy": "US", "reason": "retired",
	}); rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 for retired Stripe bind, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec := doJSON(t, r, http.MethodPut, fmt.Sprintf("/cms/products/%d/bindings/paypal", prodB), "cfg-super", map[string]any{
		"environment": "live", "reason": "retired",
	}); rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 for retired PayPal bind, got %d: %s", rec.Code, rec.Body.String())
	}
	_ = activePPID
	_ = activeStID
	if httpCalls != 0 {
		t.Fatalf("expected 0 external HTTP calls on 409 rejections, got %d", httpCalls)
	}
}

// TC-CMS-03: Primary channel switch is blocked with unready_published_products unless target account is pre-bound to all published SKUs.
func TestPublishChannel_PrimarySwitchGuardsPublishedProducts(t *testing.T) {
	r, ctx, _ := setupRegressionMockEnv(t, nil)

	_ = setupChannelWithPublish(t, r, map[string]any{
		"provider": "paypal", "environment": "sandbox", "display_name": "PP 1",
		"account_key": "pp_1", "client_secret": "pp_sec_1", "webhook_id": "WH_PP_1",
	}, true)
	st1 := setupChannelWithPublish(t, r, map[string]any{
		"provider": "stripe", "environment": "sandbox", "display_name": "Stripe 1",
		"account_key": "acct_StripeSbx01", "api_key": "sk_test_acct1_1234", "webhook_secret": "whsec_acct1_1234",
	}, true)

	prodA := createRegressionProduct(t, r, map[string]any{
		"code": "REG_SWITCH_A", "product_type": "single", "name": "Switch A", "price_cents": 499, "coins": 500,
	})
	if rec := doJSON(t, r, http.MethodPut, fmt.Sprintf("/cms/products/%d/bindings/paypal", prodA), "cfg-super", map[string]any{
		"environment": "sandbox", "reason": "bind pp",
	}); rec.Code != http.StatusOK {
		t.Fatalf("paypal bind failed (%d): %s", rec.Code, rec.Body.String())
	}
	if rec := doJSON(t, r, http.MethodPut, fmt.Sprintf("/cms/products/%d/bindings/stripe", prodA), "cfg-super", map[string]any{
		"environment": "sandbox", "account_id": st1, "sales_mode": "direct", "countries": []string{"US"}, "reason": "bind st1",
	}); rec.Code != http.StatusOK {
		t.Fatalf("stripe1 bind failed (%d): %s", rec.Code, rec.Body.String())
	}
	if rec := doJSON(t, r, http.MethodPost, fmt.Sprintf("/cms/products/%d/publish", prodA), "cfg-finance", map[string]any{
		"environment": "sandbox",
	}); rec.Code != http.StatusOK {
		t.Fatalf("publish prodA failed (%d): %s", rec.Code, rec.Body.String())
	}

	// Create + validate + publish (#2 without set_primary) so it is published & selectable for pre-binding.
	st2 := setupChannelWithPublish(t, r, map[string]any{
		"provider": "stripe", "environment": "sandbox", "display_name": "Stripe 2",
		"account_key": "acct_StripeSbx02", "api_key": "sk_test_acct2_1234", "webhook_secret": "whsec_acct2_1234",
	}, false)

	// Attempting to promote #2 to primary while prodA is unbound on #2 -> 409 with unready_published_products.
	switchFail := doJSON(t, r, http.MethodPost, fmt.Sprintf("/cms/payments/channels/%d/publish", st2), "cfg-super", map[string]any{
		"set_primary": true,
	})
	if switchFail.Code != http.StatusConflict || !strings.Contains(switchFail.Body.String(), `"unready_published_products"`) || !strings.Contains(switchFail.Body.String(), `"REG_SWITCH_A"`) {
		t.Fatalf("expected 409 listing unready REG_SWITCH_A, got (%d): %s", switchFail.Code, switchFail.Body.String())
	}
	var st1Primary bool
	if err := db.DB.QueryRow(ctx, `SELECT is_primary FROM payment_channel_accounts WHERE id=$1`, st1).Scan(&st1Primary); err != nil || !st1Primary {
		t.Fatalf("expected st1 to remain primary, got %v err=%v", st1Primary, err)
	}

	// Pre-bind prodA on #2, then promote #2 to primary -> 200 and DualChannelReady remains true.
	if rec := doJSON(t, r, http.MethodPut, fmt.Sprintf("/cms/products/%d/bindings/stripe", prodA), "cfg-super", map[string]any{
		"environment": "sandbox", "account_id": st2, "sales_mode": "direct", "countries": []string{"US"}, "reason": "prebind st2",
	}); rec.Code != http.StatusOK {
		t.Fatalf("prebind st2 failed (%d): %s", rec.Code, rec.Body.String())
	}
	if rec := doJSON(t, r, http.MethodPost, fmt.Sprintf("/cms/payments/channels/%d/publish", st2), "cfg-super", map[string]any{
		"set_primary": true,
	}); rec.Code != http.StatusOK {
		t.Fatalf("primary switch after prebind failed (%d): %s", rec.Code, rec.Body.String())
	}
	views, err := loadProductViews(ctx, prodA, "sandbox", "", "", "")
	if err != nil || len(views) != 1 || !views[0].DualChannelReady {
		t.Fatalf("expected DualChannelReady=true after primary switch, got %+v err=%v", views, err)
	}
}

func seedReadyLiveCatalog(t *testing.T, r http.Handler, count int) []int64 {
	t.Helper()
	setupChannelWithPublish(t, r, map[string]any{
		"provider": "paypal", "environment": "live", "display_name": "PP Live",
		"account_key": "pp_live_reg", "client_secret": "pp_sec_live_reg", "webhook_id": "WH_LIVE_REG",
	}, true)
	setupChannelWithPublish(t, r, map[string]any{
		"provider": "stripe", "environment": "live", "display_name": "ST Live",
		"account_key": "acct_StripeLive01", "api_key": "sk_live_reg_1234", "webhook_secret": "whsec_live_reg_1234",
	}, true)

	ids := make([]int64, 0, count)
	for i := 0; i < count; i++ {
		pid := createRegressionProduct(t, r, map[string]any{
			"code": fmt.Sprintf("REG_LIVE_%d", i+1), "product_type": "single", "name": fmt.Sprintf("Live Pack %d", i+1),
			"price_cents": 499 + i*500, "coins": 500 + i*500,
		})
		if rec := doJSON(t, r, http.MethodPut, fmt.Sprintf("/cms/products/%d/bindings/paypal", pid), "cfg-super", map[string]any{
			"environment": "live", "reason": "seed live pp",
		}); rec.Code != http.StatusOK {
			t.Fatalf("pp bind live %d failed (%d): %s", pid, rec.Code, rec.Body.String())
		}
		if rec := doJSON(t, r, http.MethodPut, fmt.Sprintf("/cms/products/%d/bindings/stripe", pid), "cfg-super", map[string]any{
			"environment": "live", "sales_mode": "direct", "countries": []string{"US"},
			"release_evidence": "ev-live", "market_policy": "US-live", "reason": "seed live st",
		}); rec.Code != http.StatusOK {
			t.Fatalf("st bind live %d failed (%d): %s", pid, rec.Code, rec.Body.String())
		}
		if rec := doJSON(t, r, http.MethodPost, fmt.Sprintf("/cms/products/%d/publish", pid), "cfg-super", map[string]any{
			"environment": "live", "reason": "go live",
		}); rec.Code != http.StatusOK {
			t.Fatalf("publish live %d failed (%d): %s", pid, rec.Code, rec.Body.String())
		}
		ids = append(ids, pid)
	}
	return ids
}

// TC-CMS-04: Live pause/retire requires SuperAdmin, non-empty reason, and blocks dropping active default/promo templates below 4 published slots.
func TestPauseAndRetire_EnforceLiveSuperAdminReasonAndMinSlots(t *testing.T) {
	r, _, _ := setupRegressionMockEnv(t, nil)
	pids := seedReadyLiveCatalog(t, r, 4)

	tplRec := doJSON(t, r, http.MethodPost, "/cms/recharge-templates", "cfg-finance", map[string]any{
		"name": "Live Default 4-Slot", "environment": "live", "is_default": true,
		"slots": []map[string]any{
			{"slot_index": 1, "product_id": pids[0]},
			{"slot_index": 2, "product_id": pids[1]},
			{"slot_index": 3, "product_id": pids[2]},
			{"slot_index": 4, "product_id": pids[3]},
		},
	})
	if tplRec.Code != http.StatusOK {
		t.Fatalf("create live default template failed (%d): %s", tplRec.Code, tplRec.Body.String())
	}

	// Finance cannot pause live product -> 403
	if rec := doJSON(t, r, http.MethodPost, fmt.Sprintf("/cms/products/%d/pause", pids[0]), "cfg-finance", map[string]any{
		"environment": "live", "reason": "finance attempt",
	}); rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for finance live pause, got %d: %s", rec.Code, rec.Body.String())
	}

	// SuperAdmin retiring live product without reason -> 400
	if rec := doJSON(t, r, http.MethodPost, fmt.Sprintf("/cms/products/%d/retire", pids[0]), "cfg-super", map[string]any{
		"environment": "live", "reason": "   ",
	}); rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when live retire omits reason, got %d: %s", rec.Code, rec.Body.String())
	}

	// SuperAdmin retiring live product with reason while default template has 4 slots -> 409 affected_templates
	retRec := doJSON(t, r, http.MethodPost, fmt.Sprintf("/cms/products/%d/retire", pids[0]), "cfg-super", map[string]any{
		"environment": "live", "reason": "cleanup",
	})
	if retRec.Code != http.StatusConflict || !strings.Contains(retRec.Body.String(), `"affected_templates"`) || !strings.Contains(retRec.Body.String(), `"Live Default 4-Slot"`) {
		t.Fatalf("expected 409 affected_templates conflict, got (%d): %s", retRec.Code, retRec.Body.String())
	}
}

// TC-CMS-05: Saving a live template rejects sandbox-only SKUs and SKUs that lost primary channel readiness.
func TestTemplateSave_RejectsSandboxOnlyOrUnreadyProducts(t *testing.T) {
	r, ctx, _ := setupRegressionMockEnv(t, nil)
	livePids := seedReadyLiveCatalog(t, r, 4)

	// Setup sandbox channels and publish product X ONLY in sandbox (live stays draft).
	setupChannelWithPublish(t, r, map[string]any{
		"provider": "paypal", "environment": "sandbox", "display_name": "PP Sbx",
		"account_key": "pp_sbx_reg", "client_secret": "pp_sec_sbx", "webhook_id": "WH_SBX_REG",
	}, true)
	setupChannelWithPublish(t, r, map[string]any{
		"provider": "stripe", "environment": "sandbox", "display_name": "ST Sbx",
		"account_key": "acct_StripeSbx01", "api_key": "sk_test_sbx_reg_1234", "webhook_secret": "whsec_sbx_reg_1234",
	}, true)
	prodX := createRegressionProduct(t, r, map[string]any{
		"code": "REG_SBX_ONLY", "product_type": "single", "name": "Sandbox Only", "price_cents": 299, "coins": 300,
	})
	_ = doJSON(t, r, http.MethodPut, fmt.Sprintf("/cms/products/%d/bindings/paypal", prodX), "cfg-super", map[string]any{
		"environment": "sandbox", "reason": "sbx",
	})
	_ = doJSON(t, r, http.MethodPut, fmt.Sprintf("/cms/products/%d/bindings/stripe", prodX), "cfg-super", map[string]any{
		"environment": "sandbox", "sales_mode": "direct", "countries": []string{"US"}, "reason": "sbx",
	})
	if rec := doJSON(t, r, http.MethodPost, fmt.Sprintf("/cms/products/%d/publish", prodX), "cfg-finance", map[string]any{
		"environment": "sandbox",
	}); rec.Code != http.StatusOK {
		t.Fatalf("publish prodX sandbox failed (%d): %s", rec.Code, rec.Body.String())
	}

	// Saving live template referencing sandbox-only prodX -> 409
	if rec := doJSON(t, r, http.MethodPost, "/cms/recharge-templates", "cfg-finance", map[string]any{
		"name": "Bad Live Mixing Sbx", "environment": "live",
		"slots": []map[string]any{
			{"slot_index": 1, "product_id": prodX},
			{"slot_index": 2, "product_id": livePids[1]},
			{"slot_index": 3, "product_id": livePids[2]},
			{"slot_index": 4, "product_id": livePids[3]},
		},
	}); rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 for sandbox-only product in live template, got %d: %s", rec.Code, rec.Body.String())
	}

	// Disable live primary Stripe binding on livePids[0] -> live template save rejects with reason mentioning DualChannelReady/Stripe.
	if _, err := db.DB.Exec(ctx, `UPDATE product_channel_bindings SET enabled=false WHERE product_id=$1 AND environment='live' AND provider='stripe'`, livePids[0]); err != nil {
		t.Fatal(err)
	}
	unreadyRec := doJSON(t, r, http.MethodPost, "/cms/recharge-templates", "cfg-finance", map[string]any{
		"name": "Unready Live SKU", "environment": "live",
		"slots": []map[string]any{
			{"slot_index": 1, "product_id": livePids[0]},
			{"slot_index": 2, "product_id": livePids[1]},
			{"slot_index": 3, "product_id": livePids[2]},
			{"slot_index": 4, "product_id": livePids[3]},
		},
	})
	if unreadyRec.Code != http.StatusConflict || !strings.Contains(unreadyRec.Body.String(), "not ready on Stripe (live)") {
		t.Fatalf("expected 409 unready binding explanation, got (%d): %s", unreadyRec.Code, unreadyRec.Body.String())
	}
}

// TC-CMS-06: Deleting a recharge template referenced by promotion_links returns 409 and preserves the DB FK; succeeds after unlink.
func TestDeleteTemplate_BlockedWhenReferencedByPromotionLink(t *testing.T) {
	r, ctx, _ := setupRegressionMockEnv(t, nil)
	pids := seedReadyLiveCatalog(t, r, 4)

	tplRec := doJSON(t, r, http.MethodPost, "/cms/recharge-templates", "cfg-finance", map[string]any{
		"name": "Promo Ref Template", "environment": "live", "is_default": false,
		"slots": []map[string]any{
			{"slot_index": 1, "product_id": pids[0]},
			{"slot_index": 2, "product_id": pids[1]},
			{"slot_index": 3, "product_id": pids[2]},
			{"slot_index": 4, "product_id": pids[3]},
		},
	})
	if tplRec.Code != http.StatusOK {
		t.Fatalf("create template failed (%d): %s", tplRec.Code, tplRec.Body.String())
	}
	var created struct {
		ID int `json:"id"`
	}
	_ = json.Unmarshal(tplRec.Body.Bytes(), &created)

	var promoID int
	if err := db.DB.QueryRow(ctx, `
		WITH n AS (INSERT INTO novels(title, author) VALUES('Promo Ref Novel', 'Promo Ref Author') RETURNING id)
		INSERT INTO promotion_links(name, novel_id, novel_title, generated_url, recharge_template_id)
		SELECT 'Camp-1', id, 'Promo Ref Novel', 'https://example.test/detail?novel_id='||id, $1 FROM n RETURNING id`, created.ID).Scan(&promoID); err != nil {
		t.Fatal(err)
	}

	if rec := doJSON(t, r, http.MethodDelete, fmt.Sprintf("/cms/recharge-templates/%d", created.ID), "cfg-finance", nil); rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 when deleting promo-referenced template, got %d: %s", rec.Code, rec.Body.String())
	}
	var linkedTpl int
	if err := db.DB.QueryRow(ctx, `SELECT recharge_template_id FROM promotion_links WHERE id=$1`, promoID).Scan(&linkedTpl); err != nil || linkedTpl != created.ID {
		t.Fatalf("promotion_links.recharge_template_id mutated unexpectedly: %d err=%v", linkedTpl, err)
	}

	if _, err := db.DB.Exec(ctx, `UPDATE promotion_links SET recharge_template_id = NULL WHERE id = $1`, promoID); err != nil {
		t.Fatal(err)
	}
	if rec := doJSON(t, r, http.MethodDelete, fmt.Sprintf("/cms/recharge-templates/%d", created.ID), "cfg-finance", nil); rec.Code != http.StatusOK {
		t.Fatalf("expected 200 delete after unlink, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TC-CMS-07: Template updates preserve recharge_slots.id for retained SKUs when reordering/editing badges, and block unsetting the sole default template.
func TestUpdateTemplate_PreservesSlotIDsAndDefaultInvariant(t *testing.T) {
	r, ctx, _ := setupRegressionMockEnv(t, nil)
	pids := seedReadyLiveCatalog(t, r, 4)

	tplRec := doJSON(t, r, http.MethodPost, "/cms/recharge-templates", "cfg-finance", map[string]any{
		"name": "Sole Default", "environment": "live", "is_default": true,
		"slots": []map[string]any{
			{"slot_index": 1, "product_id": pids[0], "badge": "FIRST"},
			{"slot_index": 2, "product_id": pids[1], "badge": "SECOND"},
			{"slot_index": 3, "product_id": pids[2]},
			{"slot_index": 4, "product_id": pids[3]},
		},
	})
	if tplRec.Code != http.StatusOK {
		t.Fatalf("create default template failed (%d): %s", tplRec.Code, tplRec.Body.String())
	}
	var tplID int
	_ = json.Unmarshal(tplRec.Body.Bytes(), &tplID)
	// Extract id from JSON response
	var created struct {
		ID int `json:"id"`
	}
	_ = json.Unmarshal(tplRec.Body.Bytes(), &created)
	tplID = created.ID

	loadSlotIDsByProd := func() map[int64]int {
		rows, err := db.DB.Query(ctx, `SELECT product_id, id FROM recharge_slots WHERE template_id=$1`, tplID)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		out := make(map[int64]int)
		for rows.Next() {
			var pid int64
			var sid int
			if err := rows.Scan(&pid, &sid); err != nil {
				t.Fatal(err)
			}
			out[pid] = sid
		}
		return out
	}
	before := loadSlotIDsByProd()

	// Swap slots 1 and 2 + update badge/name
	if rec := doJSON(t, r, http.MethodPut, fmt.Sprintf("/cms/recharge-templates/%d", tplID), "cfg-finance", map[string]any{
		"name": "Sole Default Renamed", "environment": "live", "is_default": true,
		"slots": []map[string]any{
			{"slot_index": 1, "product_id": pids[1], "badge": "PROMOTED"},
			{"slot_index": 2, "product_id": pids[0], "badge": "SWAPPED"},
			{"slot_index": 3, "product_id": pids[2]},
			{"slot_index": 4, "product_id": pids[3]},
		},
	}); rec.Code != http.StatusOK {
		t.Fatalf("template update failed (%d): %s", rec.Code, rec.Body.String())
	}
	after := loadSlotIDsByProd()
	for _, pid := range pids {
		if before[pid] == 0 || before[pid] != after[pid] {
			t.Fatalf("slot.id changed for product %d: before=%d after=%d", pid, before[pid], after[pid])
		}
	}

	// Attempt to unset is_default on sole default template -> 409 and stays default
	if rec := doJSON(t, r, http.MethodPut, fmt.Sprintf("/cms/recharge-templates/%d", tplID), "cfg-finance", map[string]any{
		"name": "Sole Default Renamed", "environment": "live", "is_default": false,
		"slots": []map[string]any{
			{"slot_index": 1, "product_id": pids[1]},
			{"slot_index": 2, "product_id": pids[0]},
			{"slot_index": 3, "product_id": pids[2]},
			{"slot_index": 4, "product_id": pids[3]},
		},
	}); rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 when unsetting sole default template, got %d: %s", rec.Code, rec.Body.String())
	}
	var stillDefault bool
	if err := db.DB.QueryRow(ctx, `SELECT is_default FROM recharge_templates WHERE id=$1`, tplID).Scan(&stillDefault); err != nil || !stillDefault {
		t.Fatalf("expected template %d to remain default, got %v err=%v", tplID, stillDefault, err)
	}
}

// TC-CMS-08: Stripe inactive ref reprovisioning & PayPal VIP deterministic 4xx vs uncertain 504 handling (+ F-12 legacy route 404 verification).
func TestStripeArchiveRecoveryAndPayPalVIP4xxRetry(t *testing.T) {
	var stArchiveMu sync.Mutex
	archiveFirstPrice := false
	ppPlanAttempts := 0
	var r *gin.Engine
	var ctx context.Context
	var st *mockStripeState

	r, ctx, st = setupRegressionMockEnv(t, func(w http.ResponseWriter, req *http.Request) bool {
		stArchiveMu.Lock()
		arch := archiveFirstPrice
		stArchiveMu.Unlock()
		if req.Method == http.MethodGet && req.URL.Path == "/v1/prices/price_sbx_1" && arch {
			st.mu.Lock()
			price := st.prices["price_sbx_1"]
			prod := st.products[price.ProductID]
			st.mu.Unlock()
			_, _ = fmt.Fprintf(w,
				`{"id":%q,"currency":"usd","type":"one_time","tax_behavior":"unspecified","unit_amount":999,"active":false,"livemode":false,"product":{"id":%q,"active":true,"livemode":false,"tax_code":""}}`,
				price.ID, prod.ID)
			return true
		}
		if req.Method == http.MethodPost && req.URL.Path == "/v1/billing/plans" {
			stArchiveMu.Lock()
			ppPlanAttempts++
			attempt := ppPlanAttempts
			stArchiveMu.Unlock()
			switch attempt {
			case 1:
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"name":"INVALID_REQUEST","message":"description invalid"}`))
				return true
			case 2:
				_, _ = w.Write([]byte(`{"id":"P-RECOVERED-400"}`))
				return true
			default:
				w.WriteHeader(http.StatusGatewayTimeout)
				_, _ = w.Write([]byte(`{"error":"gateway timeout"}`))
				return true
			}
		}
		return false
	})

	setupChannelWithPublish(t, r, map[string]any{
		"provider": "stripe", "environment": "sandbox", "display_name": "ST Rec",
		"account_key": "acct_StripeSbx01", "api_key": "sk_test_rec_1234", "webhook_secret": "whsec_rec_1234",
	}, true)
	setupChannelWithPublish(t, r, map[string]any{
		"provider": "paypal", "environment": "sandbox", "display_name": "PP Rec",
		"account_key": "pp_rec", "client_secret": "pp_sec_rec", "webhook_id": "WH_REC",
	}, true)

	// 1. Stripe archived Price recovery via reprovision=true
	coinID := createRegressionProduct(t, r, map[string]any{
		"code": "REG_REC_COIN", "product_type": "single", "name": "Rec Coin", "price_cents": 999, "coins": 1000,
	})
	if rec := doJSON(t, r, http.MethodPut, fmt.Sprintf("/cms/products/%d/bindings/stripe", coinID), "cfg-super", map[string]any{
		"environment": "sandbox", "sales_mode": "direct", "countries": []string{"US"}, "reason": "initial",
	}); rec.Code != http.StatusOK {
		t.Fatalf("initial stripe bind failed (%d): %s", rec.Code, rec.Body.String())
	}

	stArchiveMu.Lock()
	archiveFirstPrice = true
	stArchiveMu.Unlock()

	if rec := doJSON(t, r, http.MethodPut, fmt.Sprintf("/cms/products/%d/bindings/stripe", coinID), "cfg-super", map[string]any{
		"environment": "sandbox", "sales_mode": "direct", "countries": []string{"US"}, "reason": "no reprovision",
	}); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 on inactive price without reprovision, got %d: %s", rec.Code, rec.Body.String())
	}
	reprovRec := doJSON(t, r, http.MethodPut, fmt.Sprintf("/cms/products/%d/bindings/stripe", coinID), "cfg-super", map[string]any{
		"environment": "sandbox", "sales_mode": "direct", "countries": []string{"US"}, "reprovision": true, "reason": "reprovision archived",
	})
	if reprovRec.Code != http.StatusOK || !strings.Contains(reprovRec.Body.String(), `"external_price_ref":"price_sbx_2"`) {
		t.Fatalf("expected reprovisioned price_sbx_2, got (%d): %s", reprovRec.Code, reprovRec.Body.String())
	}
	var reprovAudits int
	if err := db.DB.QueryRow(ctx, `SELECT count(*) FROM admin_audit_logs WHERE action='reprovision_stripe_refs' AND target_id=$1`, strconv.FormatInt(coinID, 10)).Scan(&reprovAudits); err != nil || reprovAudits != 1 {
		t.Fatalf("expected reprovision_stripe_refs audit, count=%d err=%v", reprovAudits, err)
	}

	// 2. PayPal VIP deterministic 400 leaves review_required=false so retrying without manual Plan ID succeeds; 504 locks with review_required=true.
	vip1 := createRegressionProduct(t, r, map[string]any{
		"code": "REG_PP_VIP_400", "product_type": "vip", "name": "VIP Month 400", "price_cents": 999, "vip_cycle": "month",
	})
	fail400 := doJSON(t, r, http.MethodPut, fmt.Sprintf("/cms/products/%d/bindings/paypal", vip1), "cfg-super", map[string]any{
		"environment": "sandbox", "reason": "first attempt hits 400",
	})
	if fail400.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 on PayPal 400, got %d: %s", fail400.Code, fail400.Body.String())
	}
	var revReq bool
	if err := db.DB.QueryRow(ctx, `SELECT review_required FROM product_channel_bindings WHERE product_id=$1 AND provider='paypal'`, vip1).Scan(&revReq); err != nil || revReq {
		t.Fatalf("expected review_required=false after PayPal 400, got %v err=%v", revReq, err)
	}
	retryOK := doJSON(t, r, http.MethodPut, fmt.Sprintf("/cms/products/%d/bindings/paypal", vip1), "cfg-super", map[string]any{
		"environment": "sandbox", "reason": "retry auto create",
	})
	if retryOK.Code != http.StatusOK || !strings.Contains(retryOK.Body.String(), `"external_price_ref":"P-RECOVERED-400"`) {
		t.Fatalf("expected retry after 400 to auto-create P-RECOVERED-400, got (%d): %s", retryOK.Code, retryOK.Body.String())
	}

	// 3. Another VIP hitting 504 sets review_required=true and blocks subsequent blind retry with 409.
	vip2 := createRegressionProduct(t, r, map[string]any{
		"code": "REG_PP_VIP_504", "product_type": "vip", "name": "VIP Month 504", "price_cents": 999, "vip_cycle": "month",
	})
	fail504 := doJSON(t, r, http.MethodPut, fmt.Sprintf("/cms/products/%d/bindings/paypal", vip2), "cfg-super", map[string]any{
		"environment": "sandbox", "reason": "hits 504",
	})
	if fail504.Code != http.StatusUnprocessableEntity || !strings.Contains(fail504.Body.String(), `"validation_status":"review"`) {
		t.Fatalf("expected 422 validation_status=review after 504, got (%d): %s", fail504.Code, fail504.Body.String())
	}
	blindRetry := doJSON(t, r, http.MethodPut, fmt.Sprintf("/cms/products/%d/bindings/paypal", vip2), "cfg-super", map[string]any{
		"environment": "sandbox", "reason": "blind retry should block",
	})
	if blindRetry.Code != http.StatusConflict {
		t.Fatalf("expected 409 conflict when blind-retrying after uncertain 504, got %d: %s", blindRetry.Code, blindRetry.Body.String())
	}

	// F-12 regression check: legacy Offer routes are removed and return 404.
	for _, legacyPath := range []string{
		"/cms/payments/channels/vip-offers",
		"/cms/payments/channels/1/stripe-one-time",
		"/cms/payments/channels/1/stripe-vip",
	} {
		if rec := doJSON(t, r, http.MethodGet, legacyPath, "cfg-super", nil); rec.Code != http.StatusNotFound {
			t.Fatalf("expected legacy route %s to return 404, got %d", legacyPath, rec.Code)
		}
	}
}

// TC-CMS-09: CopyProduct inherits source bonus when omitted, and overrides to 0 only when explicitly passed.
func TestCopyProduct_PreservesBonusUnlessExplicitlyZero(t *testing.T) {
	r, ctx, _ := setupRegressionMockEnv(t, nil)
	srcID := createRegressionProduct(t, r, map[string]any{
		"code": "REG_SRC_BONUS", "product_type": "single", "name": "Source Bonus",
		"price_cents": 999, "coins": 1000, "bonus": 200,
	})

	copyOmit := doJSON(t, r, http.MethodPost, fmt.Sprintf("/cms/products/%d/copy", srcID), "cfg-finance", map[string]any{
		"code": "REG_COPY_INHERIT", "name": "Copy Inherit",
	})
	if copyOmit.Code != http.StatusCreated {
		t.Fatalf("copy omit bonus failed (%d): %s", copyOmit.Code, copyOmit.Body.String())
	}
	var inheritResp struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(copyOmit.Body.Bytes(), &inheritResp)
	var bonusA int
	if err := db.DB.QueryRow(ctx, `SELECT bonus FROM products WHERE id=$1`, inheritResp.ID).Scan(&bonusA); err != nil || bonusA != 200 {
		t.Fatalf("expected inherited bonus=200, got %d err=%v", bonusA, err)
	}

	copyZero := doJSON(t, r, http.MethodPost, fmt.Sprintf("/cms/products/%d/copy", srcID), "cfg-finance", map[string]any{
		"code": "REG_COPY_ZERO", "name": "Copy Zero", "bonus": 0,
	})
	if copyZero.Code != http.StatusCreated {
		t.Fatalf("copy explicit zero bonus failed (%d): %s", copyZero.Code, copyZero.Body.String())
	}
	var zeroResp struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(copyZero.Body.Bytes(), &zeroResp)
	var bonusB int
	if err := db.DB.QueryRow(ctx, `SELECT bonus FROM products WHERE id=$1`, zeroResp.ID).Scan(&bonusB); err != nil || bonusB != 0 {
		t.Fatalf("expected explicit zero bonus=0, got %d err=%v", bonusB, err)
	}
}

func seedReadySandboxCatalog(t *testing.T, r http.Handler, count int) []int64 {
	t.Helper()
	setupChannelWithPublish(t, r, map[string]any{
		"provider": "paypal", "environment": "sandbox", "display_name": "PP Sbx",
		"account_key": "pp_sbx_iso", "client_secret": "pp_sec_sbx_iso", "webhook_id": "WH_SBX_ISO",
	}, true)
	setupChannelWithPublish(t, r, map[string]any{
		"provider": "stripe", "environment": "sandbox", "display_name": "ST Sbx",
		"account_key": "acct_StripeSbx01", "api_key": "sk_test_sbx_iso_1234", "webhook_secret": "whsec_sbx_iso_1234",
	}, true)
	ids := make([]int64, 0, count)
	for i := 0; i < count; i++ {
		pid := createRegressionProduct(t, r, map[string]any{
			"code": fmt.Sprintf("REG_SBX_ISO_%d", i+1), "product_type": "single", "name": fmt.Sprintf("Sbx Pack %d", i+1),
			"price_cents": 299 + i*100, "coins": 300 + i*100,
		})
		_ = doJSON(t, r, http.MethodPut, fmt.Sprintf("/cms/products/%d/bindings/paypal", pid), "cfg-super", map[string]any{
			"environment": "sandbox", "reason": "seed sbx pp",
		})
		_ = doJSON(t, r, http.MethodPut, fmt.Sprintf("/cms/products/%d/bindings/stripe", pid), "cfg-super", map[string]any{
			"environment": "sandbox", "sales_mode": "direct", "countries": []string{"US"}, "reason": "seed sbx st",
		})
		if rec := doJSON(t, r, http.MethodPost, fmt.Sprintf("/cms/products/%d/publish", pid), "cfg-finance", map[string]any{
			"environment": "sandbox",
		}); rec.Code != http.StatusOK {
			t.Fatalf("publish sandbox %d failed (%d): %s", pid, rec.Code, rec.Body.String())
		}
		ids = append(ids, pid)
	}
	return ids
}

// TC-CMS-10 (R-1): Only live templates can be default or promotion-linked; sandbox templates cannot become default and active live templates cannot be demoted to sandbox.
func TestTemplateEnvironmentIsolation_DefaultAndPromotionGuards(t *testing.T) {
	r, ctx, _ := setupRegressionMockEnv(t, nil)
	livePids := seedReadyLiveCatalog(t, r, 4)
	sbxPids := seedReadySandboxCatalog(t, r, 4)

	sbxSlots := []map[string]any{
		{"slot_index": 1, "product_id": sbxPids[0]},
		{"slot_index": 2, "product_id": sbxPids[1]},
		{"slot_index": 3, "product_id": sbxPids[2]},
		{"slot_index": 4, "product_id": sbxPids[3]},
	}
	liveSlots := []map[string]any{
		{"slot_index": 1, "product_id": livePids[0]},
		{"slot_index": 2, "product_id": livePids[1]},
		{"slot_index": 3, "product_id": livePids[2]},
		{"slot_index": 4, "product_id": livePids[3]},
	}

	// 1. Creating a sandbox template with is_default=true -> 409 Conflict.
	if rec := doJSON(t, r, http.MethodPost, "/cms/recharge-templates", "cfg-finance", map[string]any{
		"name": "Bad Sandbox Default", "environment": "sandbox", "is_default": true, "slots": sbxSlots,
	}); rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 when creating sandbox template as default, got %d: %s", rec.Code, rec.Body.String())
	}

	// Non-default sandbox template creates fine, but PUT is_default=true or POST set-default -> 409 Conflict.
	sbxCreate := doJSON(t, r, http.MethodPost, "/cms/recharge-templates", "cfg-finance", map[string]any{
		"name": "Sandbox Preview", "environment": "sandbox", "is_default": false, "slots": sbxSlots,
	})
	if sbxCreate.Code != http.StatusOK {
		t.Fatalf("create non-default sandbox template failed (%d): %s", sbxCreate.Code, sbxCreate.Body.String())
	}
	var sbxCreated struct {
		ID int `json:"id"`
	}
	_ = json.Unmarshal(sbxCreate.Body.Bytes(), &sbxCreated)
	if rec := doJSON(t, r, http.MethodPut, fmt.Sprintf("/cms/recharge-templates/%d", sbxCreated.ID), "cfg-finance", map[string]any{
		"name": "Sandbox Preview", "environment": "sandbox", "is_default": true, "slots": sbxSlots,
	}); rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 when updating sandbox template to default, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec := doJSON(t, r, http.MethodPost, fmt.Sprintf("/cms/recharge-templates/%d/set-default", sbxCreated.ID), "cfg-finance", nil); rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 when set-default on sandbox template, got %d: %s", rec.Code, rec.Body.String())
	}

	// 2. Switching a default live template or promo-linked live template to sandbox -> 409 Conflict.
	liveDefCreate := doJSON(t, r, http.MethodPost, "/cms/recharge-templates", "cfg-finance", map[string]any{
		"name": "Live Default", "environment": "live", "is_default": true, "slots": liveSlots,
	})
	var liveDef struct {
		ID int `json:"id"`
	}
	_ = json.Unmarshal(liveDefCreate.Body.Bytes(), &liveDef)
	if rec := doJSON(t, r, http.MethodPut, fmt.Sprintf("/cms/recharge-templates/%d", liveDef.ID), "cfg-finance", map[string]any{
		"name": "Live Default", "environment": "sandbox", "is_default": false, "slots": sbxSlots,
	}); rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 when demoting default live template to sandbox, got %d: %s", rec.Code, rec.Body.String())
	}

	livePromoCreate := doJSON(t, r, http.MethodPost, "/cms/recharge-templates", "cfg-finance", map[string]any{
		"name": "Live Promo Template", "environment": "live", "is_default": false, "slots": liveSlots,
	})
	var livePromo struct {
		ID int `json:"id"`
	}
	_ = json.Unmarshal(livePromoCreate.Body.Bytes(), &livePromo)
	if _, err := db.DB.Exec(ctx, `
		WITH n AS (INSERT INTO novels(title, author) VALUES('Env Novel', 'Author') RETURNING id)
		INSERT INTO promotion_links(name, novel_id, novel_title, generated_url, recharge_template_id)
		SELECT 'Env-Link', id, 'Env Novel', 'https://example.test/detail?novel_id='||id, $1 FROM n`, livePromo.ID); err != nil {
		t.Fatal(err)
	}
	if rec := doJSON(t, r, http.MethodPut, fmt.Sprintf("/cms/recharge-templates/%d", livePromo.ID), "cfg-finance", map[string]any{
		"name": "Live Promo Template", "environment": "sandbox", "is_default": false, "slots": sbxSlots,
	}); rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 when switching promo-linked live template to sandbox, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TC-CMS-11 (R-2): Retrying with reprovision=true after a mid-flight Stripe price creation timeout recovers cleanly.
func TestStripeReprovision_IdempotentRetryAfterPriceCreateFailure(t *testing.T) {
	var failNextPriceMu sync.Mutex
	archiveOld := false
	failNextPrice := false
	var r *gin.Engine
	var ctx context.Context
	var st *mockStripeState

	r, ctx, st = setupRegressionMockEnv(t, func(w http.ResponseWriter, req *http.Request) bool {
		failNextPriceMu.Lock()
		arch := archiveOld
		failPrice := failNextPrice
		if failPrice && req.Method == http.MethodPost && req.URL.Path == "/v1/prices" {
			failNextPrice = false
			failNextPriceMu.Unlock()
			w.WriteHeader(http.StatusGatewayTimeout)
			_, _ = w.Write([]byte(`{"error":{"message":"stripe timeout during reprovision"}}`))
			return true
		}
		failNextPriceMu.Unlock()

		if arch && req.Method == http.MethodGet && req.URL.Path == "/v1/prices/price_sbx_1" {
			st.mu.Lock()
			price := st.prices["price_sbx_1"]
			prod := st.products[price.ProductID]
			st.mu.Unlock()
			_, _ = fmt.Fprintf(w,
				`{"id":%q,"currency":"usd","type":"one_time","tax_behavior":"unspecified","unit_amount":999,"active":false,"livemode":false,"product":{"id":%q,"active":true,"livemode":false,"tax_code":""}}`,
				price.ID, prod.ID)
			return true
		}
		return false
	})

	setupChannelWithPublish(t, r, map[string]any{
		"provider": "stripe", "environment": "sandbox", "display_name": "ST Reprov",
		"account_key": "acct_StripeSbx01", "api_key": "sk_test_reprov_1234", "webhook_secret": "whsec_reprov_1234",
	}, true)

	prodID := createRegressionProduct(t, r, map[string]any{
		"code": "REG_REPROV_RETRY", "product_type": "single", "name": "Reprov Retry", "price_cents": 999, "coins": 1000,
	})
	if rec := doJSON(t, r, http.MethodPut, fmt.Sprintf("/cms/products/%d/bindings/stripe", prodID), "cfg-super", map[string]any{
		"environment": "sandbox", "sales_mode": "direct", "countries": []string{"US"}, "reason": "initial",
	}); rec.Code != http.StatusOK {
		t.Fatalf("initial stripe bind failed (%d): %s", rec.Code, rec.Body.String())
	}

	failNextPriceMu.Lock()
	archiveOld = true
	failNextPrice = true
	failNextPriceMu.Unlock()

	// First reprovision wipes price_sbx_1 then hits 504 on POST /v1/prices -> persists failed state with empty price_ref.
	firstAttempt := doJSON(t, r, http.MethodPut, fmt.Sprintf("/cms/products/%d/bindings/stripe", prodID), "cfg-super", map[string]any{
		"environment": "sandbox", "sales_mode": "direct", "countries": []string{"US"}, "reprovision": true, "reason": "first try fails",
	})
	if firstAttempt.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 on failed reprovision price create, got %d: %s", firstAttempt.Code, firstAttempt.Body.String())
	}
	var savedProd, savedPrice, valStatus string
	if err := db.DB.QueryRow(ctx, `SELECT external_product_ref, external_price_ref, validation_status FROM product_channel_bindings WHERE product_id=$1 AND provider='stripe'`, prodID).
		Scan(&savedProd, &savedPrice, &valStatus); err != nil || savedProd != "prod_sbx_1" || savedPrice != "" || valStatus != "failed" {
		t.Fatalf("unexpected partial state: prod=%q price=%q status=%q err=%v", savedProd, savedPrice, valStatus, err)
	}

	// Retrying with the exact same reprovision=true payload recovers without "reprovision requires an existing Stripe Product/Price reference".
	retryAttempt := doJSON(t, r, http.MethodPut, fmt.Sprintf("/cms/products/%d/bindings/stripe", prodID), "cfg-super", map[string]any{
		"environment": "sandbox", "sales_mode": "direct", "countries": []string{"US"}, "reprovision": true, "reason": "retry recovery",
	})
	if retryAttempt.Code != http.StatusOK || !strings.Contains(retryAttempt.Body.String(), `"external_price_ref":"price_sbx_2"`) {
		t.Fatalf("expected idempotent retry to recover price_sbx_2, got (%d): %s", retryAttempt.Code, retryAttempt.Body.String())
	}
}

// TC-CMS-12 (R-3): Concurrent promotion_links FK violation (SQLSTATE 23503) on template delete maps to 409 Conflict instead of 500.
func TestDeleteTemplate_ConcurrentFKViolationMaps409(t *testing.T) {
	r, ctx, _ := setupRegressionMockEnv(t, nil)
	pids := seedReadyLiveCatalog(t, r, 4)

	tplRec := doJSON(t, r, http.MethodPost, "/cms/recharge-templates", "cfg-finance", map[string]any{
		"name": "Race Delete Target", "environment": "live", "is_default": false,
		"slots": []map[string]any{
			{"slot_index": 1, "product_id": pids[0]},
			{"slot_index": 2, "product_id": pids[1]},
			{"slot_index": 3, "product_id": pids[2]},
			{"slot_index": 4, "product_id": pids[3]},
		},
	})
	if tplRec.Code != http.StatusOK {
		t.Fatalf("create template failed (%d): %s", tplRec.Code, tplRec.Body.String())
	}
	var target struct {
		ID int `json:"id"`
	}
	_ = json.Unmarshal(tplRec.Body.Bytes(), &target)

	var novelID int
	if err := db.DB.QueryRow(ctx, `INSERT INTO novels(title, author) VALUES('Race Novel', 'Author') RETURNING id`).Scan(&novelID); err != nil {
		t.Fatal(err)
	}

	// tx1 inserts an uncommitted promotion_link referencing target.ID so Delete's FOR SHARE snapshot sees 0 rows,
	// forcing DELETE FROM recharge_templates to block on tx1 and then fail with SQLSTATE 23503 once tx1 commits.
	tx1, err := db.DB.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx1.Rollback(context.WithoutCancel(ctx))
	if _, err := tx1.Exec(ctx, `
		INSERT INTO promotion_links(name, novel_id, novel_title, generated_url, recharge_template_id)
		VALUES ('Concurrent Promo', $1, 'Race Novel', 'https://example.test/detail', $2)`, novelID, target.ID); err != nil {
		t.Fatal(err)
	}

	deleteRecCh := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		deleteRecCh <- doJSON(t, r, http.MethodDelete, fmt.Sprintf("/cms/recharge-templates/%d", target.ID), "cfg-finance", nil)
	}()

	time.Sleep(80 * time.Millisecond)
	if err := tx1.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	rec := <-deleteRecCh
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict on concurrent FK violation (23503), got %d: %s", rec.Code, rec.Body.String())
	}
	var stillExists bool
	if err := db.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM recharge_templates WHERE id=$1)`, target.ID).Scan(&stillExists); err != nil || !stillExists {
		t.Fatalf("template %d should still exist after 409: %v err=%v", target.ID, stillExists, err)
	}
}
