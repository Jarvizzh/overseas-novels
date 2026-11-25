package billing

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"star-novel-cms/internal/auth"
	"star-novel-cms/internal/config"
)

type stubBillingService struct {
	BillingService
}

func (stubBillingService) ListOrders(context.Context, int, int, string, string, string, string, string, string, string, reconciliationScope) ([]Order, int, error) {
	return []Order{{ID: 101, UserID: "1", ExternalRefID: "ext_101", AmountCents: 999, PaymentAccountID: "12"}}, 1, nil
}

func (stubBillingService) ListRechargeTemplates(context.Context) ([]RechargeTemplate, error) {
	return []RechargeTemplate{{ID: 1, Name: "Live Default", Environment: "live", IsDefault: true}}, nil
}

func (stubBillingService) ListLedgerMovements(context.Context, movementFilter) (movementPage, error) {
	return movementPage{}, nil
}

func TestBillingRoutesEnforceRBACPermissions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	prevCfg := config.AppConfig
	config.AppConfig = &config.Config{JWTSecret: "billing-rbac-test-secret"}
	t.Cleanup(func() { config.AppConfig = prevCfg })

	principals := map[string]*auth.Principal{
		"dash-only":     auth.NewPrincipal("dash-only", "dash", "Dash", 2, "Dash", "仅总览", false, auth.StatusActive, auth.StatusActive, []string{auth.PermDashboardRead}),
		"orders-read":   auth.NewPrincipal("orders-read", "or", "OR", 3, "OrdersRead", "订单只读", false, auth.StatusActive, auth.StatusActive, []string{auth.PermPaymentsOrdersRead}),
		"orders-write":  auth.NewPrincipal("orders-write", "ow", "OW", 4, "OrdersWrite", "订单读写", false, auth.StatusActive, auth.StatusActive, []string{auth.PermPaymentsOrdersWrite}),
		"refunds-read":  auth.NewPrincipal("refunds-read", "rfr", "RFR", 12, "RefundsRead", "退款只读", false, auth.StatusActive, auth.StatusActive, []string{auth.PermPaymentsRefundsRead}),
		"refunds-write": auth.NewPrincipal("refunds-write", "rw", "RW", 5, "RefundsWrite", "退款读写", false, auth.StatusActive, auth.StatusActive, []string{auth.PermPaymentsRefundsWrite}),
		"risk-read":     auth.NewPrincipal("risk-read", "rr", "RR", 6, "RiskRead", "风控只读", false, auth.StatusActive, auth.StatusActive, []string{auth.PermPaymentsRiskRead}),
		"disputes-read": auth.NewPrincipal("disputes-read", "dr", "DR", 7, "DisputesRead", "争议只读", false, auth.StatusActive, auth.StatusActive, []string{auth.PermPaymentsDisputesRead}),
		"prod-write":    auth.NewPrincipal("prod-write", "pw", "PW", 8, "ProdWrite", "商品读写", false, auth.StatusActive, auth.StatusActive, []string{auth.PermPaymentsProductsWrite}),
		"prod-ch-write": auth.NewPrincipal("prod-ch-write", "pcw", "PCW", 9, "ProdChWrite", "商品渠道读写", false, auth.StatusActive, auth.StatusActive, []string{auth.PermPaymentsProductsWrite, auth.PermPaymentsChannelsWrite}),
		"tpl-read":      auth.NewPrincipal("tpl-read", "tr", "TR", 10, "TplRead", "模板只读", false, auth.StatusActive, auth.StatusActive, []string{auth.PermAdsTemplatesRead}),
		"recon-read":    auth.NewPrincipal("recon-read", "rcr", "RCR", 11, "ReconRead", "对账只读", false, auth.StatusActive, auth.StatusActive, []string{auth.PermPaymentsReconciliationRead}),
	}
	restore := auth.SetPrincipalLoaderForTest(func(_ context.Context, adminID string) (*auth.Principal, error) {
		p, ok := principals[adminID]
		if !ok {
			return nil, auth.ErrAccountInactive
		}
		return p, nil
	})
	t.Cleanup(restore)

	r := gin.New()
	NewHandler(stubBillingService{}).RegisterRoutes(r.Group("/api/v1/admin"))

	call := func(adminID, method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		tok, err := auth.GenerateToken(adminID, "Any")
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(method, "/api/v1/admin"+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+tok)
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	// Dashboard-only gets 403 across billing endpoints
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/orders"},
		{http.MethodGet, "/payments/orders"},
		{http.MethodGet, "/payments/accounts"},
		{http.MethodGet, "/payments/subscriptions"},
		{http.MethodGet, "/payments/channels"},
		{http.MethodGet, "/payments/refunds"},
		{http.MethodGet, "/payments/cases?view=risk"},
		{http.MethodGet, "/payments/cases/owners"},
		{http.MethodGet, "/payments/reconciliation/movements"},
		{http.MethodGet, "/products"},
		{http.MethodGet, "/recharge-templates"},
		{http.MethodGet, "/recharge-templates/product-options"},
	} {
		if w := call("dash-only", tc.method, tc.path, ""); w.Code != http.StatusForbidden {
			t.Fatalf("dash-only %s %s = %d, want 403", tc.method, tc.path, w.Code)
		}
	}

	// Orders read returns full fields without legacy role stripping
	if w := call("orders-read", http.MethodGet, "/orders", ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"external_ref_id":"ext_101"`) {
		t.Fatalf("orders-read GET /orders = %d: %s", w.Code, w.Body.String())
	}
	if w := call("orders-read", http.MethodGet, "/payments/orders/not-a-number/receipts", ""); w.Code != http.StatusBadRequest {
		t.Fatalf("orders-read GET /payments/orders/not-a-number/receipts = %d, want 400 (past authz)", w.Code)
	}

	// Refunds read can access order receipts for refund calculation, but NOT order list or payment details (CR-1)
	if w := call("refunds-read", http.MethodGet, "/payments/orders/not-a-number/receipts", ""); w.Code != http.StatusBadRequest {
		t.Fatalf("refunds-read GET /payments/orders/not-a-number/receipts = %d, want 400 (past authz)", w.Code)
	}
	if w := call("refunds-read", http.MethodGet, "/payments/orders", ""); w.Code != http.StatusForbidden {
		t.Fatalf("refunds-read GET /payments/orders = %d, want 403", w.Code)
	}
	if w := call("refunds-read", http.MethodGet, "/payments/orders/101/payment-details", ""); w.Code != http.StatusForbidden {
		t.Fatalf("refunds-read GET /payments/orders/101/payment-details = %d, want 403", w.Code)
	}
	if w := call("dash-only", http.MethodGet, "/payments/orders/101/receipts", ""); w.Code != http.StatusForbidden {
		t.Fatalf("dash-only GET /payments/orders/101/receipts = %d, want 403", w.Code)
	}

	// Unified refund permission: orders-write CANNOT refund, refunds-write CAN pass middleware (invalid ID -> 400)
	if w := call("orders-write", http.MethodPost, "/orders/101/refund", ""); w.Code != http.StatusForbidden {
		t.Fatalf("orders-write POST /orders/101/refund = %d, want 403", w.Code)
	}
	if w := call("orders-write", http.MethodPost, "/payments/refunds", `{}`); w.Code != http.StatusForbidden {
		t.Fatalf("orders-write POST /payments/refunds = %d, want 403", w.Code)
	}
	if w := call("refunds-write", http.MethodPost, "/orders/not-a-number/refund", ""); w.Code != http.StatusBadRequest {
		t.Fatalf("refunds-write POST /orders/not-a-number/refund = %d, want 400 (past authz)", w.Code)
	}
	if w := call("refunds-write", http.MethodPost, "/payments/refunds", `{}`); w.Code != http.StatusBadRequest {
		t.Fatalf("refunds-write POST /payments/refunds invalid body = %d, want 400 (past authz)", w.Code)
	}

	// Cases risk vs disputes split at handler level
	if w := call("risk-read", http.MethodGet, "/payments/cases?view=disputes", ""); w.Code != http.StatusForbidden {
		t.Fatalf("risk-read GET cases?view=disputes = %d, want 403", w.Code)
	}
	if w := call("disputes-read", http.MethodGet, "/payments/cases?view=risk", ""); w.Code != http.StatusForbidden {
		t.Fatalf("disputes-read GET cases?view=risk = %d, want 403", w.Code)
	}
	if w := call("disputes-read", http.MethodGet, "/payments/cases/decisions", ""); w.Code != http.StatusForbidden {
		t.Fatalf("disputes-read GET cases/decisions = %d, want 403", w.Code)
	}
	if w := call("risk-read", http.MethodGet, "/payments/cases/owners?role_scope=invalid", ""); w.Code != http.StatusBadRequest {
		t.Fatalf("risk-read GET cases/owners invalid role_scope = %d, want 400", w.Code)
	}

	// Product bindings require BOTH payments.products:write AND payments.channels:write
	for _, provider := range []string{"paypal", "stripe"} {
		if w := call("prod-write", http.MethodPut, "/products/1/bindings/"+provider, `{}`); w.Code != http.StatusForbidden {
			t.Fatalf("prod-write PUT bindings/%s = %d, want 403", provider, w.Code)
		}
		if w := call("prod-ch-write", http.MethodPut, "/products/1/bindings/"+provider, `{}`); w.Code != http.StatusBadRequest {
			t.Fatalf("prod-ch-write PUT bindings/%s invalid body = %d, want 400 (past authz)", provider, w.Code)
		}
	}

	// Product live publish/pause/retire require payments.channels:write as well
	for _, action := range []string{"publish", "pause", "retire"} {
		if w := call("prod-write", http.MethodPost, "/products/1/"+action, `{"environment":"live","reason":"test"}`); w.Code != http.StatusForbidden {
			t.Fatalf("prod-write live %s = %d, want 403: %s", action, w.Code, w.Body.String())
		}
		if w := call("prod-ch-write", http.MethodPost, "/products/1/"+action, `{"environment":"live"}`); w.Code != http.StatusBadRequest {
			t.Fatalf("prod-ch-write live %s missing reason = %d, want 400 (past authz): %s", action, w.Code, w.Body.String())
		}
	}

	// Templates read/write separation
	if w := call("tpl-read", http.MethodGet, "/recharge-templates", ""); w.Code != http.StatusOK {
		t.Fatalf("tpl-read GET /recharge-templates = %d, want 200", w.Code)
	}
	if w := call("tpl-read", http.MethodPost, "/recharge-templates", `{}`); w.Code != http.StatusForbidden {
		t.Fatalf("tpl-read POST /recharge-templates = %d, want 403", w.Code)
	}
	if w := call("tpl-read", http.MethodGet, "/products", ""); w.Code != http.StatusForbidden {
		t.Fatalf("tpl-read GET /products = %d, want 403", w.Code)
	}

	// Reconciliation read/write separation
	if w := call("recon-read", http.MethodGet, "/payments/reconciliation/movements", ""); w.Code != http.StatusOK {
		t.Fatalf("recon-read GET movements = %d, want 200", w.Code)
	}
	if w := call("recon-read", http.MethodPut, "/payments/reconciliation/accounts/1/automatic", `{"enabled":true,"reason":"x"}`); w.Code != http.StatusForbidden {
		t.Fatalf("recon-read PUT automatic = %d, want 403", w.Code)
	}
}

func TestProductForPrincipalMasksBindingsWithoutChannelsRead(t *testing.T) {
	item := ProductView{
		ID:            1,
		Code:          "COIN_100",
		Name:          "100 Coins",
		PayPalBinding: &ChannelBindingView{ID: 1, ExternalPriceRef: "P-1"},
		StripeBinding: &ChannelBindingView{ID: 2, ExternalPriceRef: "price_1"},
		AllBindings:   []ChannelBindingView{{ID: 1}, {ID: 2}},
	}
	prodOnly := auth.NewPrincipal("p1", "p1", "P1", 2, "ProdOnly", "商品", false, auth.StatusActive, auth.StatusActive, []string{auth.PermPaymentsProductsRead})
	masked := productForPrincipal(item, prodOnly)
	if masked.PayPalBinding != nil || masked.StripeBinding != nil || masked.AllBindings != nil {
		t.Fatalf("expected bindings stripped without payments.channels:read, got %+v", masked)
	}

	withChannels := auth.NewPrincipal("p2", "p2", "P2", 3, "Fin", "财务", false, auth.StatusActive, auth.StatusActive, []string{auth.PermPaymentsProductsRead, auth.PermPaymentsChannelsRead})
	visible := productForPrincipal(item, withChannels)
	if visible.PayPalBinding == nil || visible.StripeBinding == nil || len(visible.AllBindings) != 2 {
		t.Fatalf("expected bindings preserved with payments.channels:read, got %+v", visible)
	}
}

func TestCaseCanRejectSQLRequiresFinanceDisputesAndRefundsWrite(t *testing.T) {
	for _, perm := range []string{auth.PermPaymentsRefundsWrite, auth.PermPaymentsDisputesWrite} {
		want := "public.admin_has_permission(p.finance_id, '" + perm + "')"
		if !strings.Contains(caseCanRejectSQL, want) {
			t.Fatalf("caseCanRejectSQL missing %q: %s", want, caseCanRejectSQL)
		}
	}
}
