package operations

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"conversion-backend/internal/delivery"
	"conversion-backend/internal/partner/sinan"
)

func TestHTTPHeaderPermissionGuard(t *testing.T) {
	handler := NewHandler(Repository{Environment: sinan.TestEnvironment}, "service-secret")

	t.Run("rejects missing or wrong bearer token", func(t *testing.T) {
		for _, auth := range []string{"", "Bearer wrong"} {
			req := httptest.NewRequest(http.MethodGet, "/internal/conversion/events?partner_report_state=invalid", nil)
			if auth != "" {
				req.Header.Set("Authorization", auth)
			}
			req.Header.Set("X-Operator-ID", "op-1")
			req.Header.Set("X-Operator-Access", "read")
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != http.StatusUnauthorized || !strings.Contains(res.Body.String(), "service_auth_required") {
				t.Fatalf("auth=%q: status=%d body=%s", auth, res.Code, res.Body.String())
			}
		}
	})

	t.Run("GET requires non-empty operator ID and read or write access", func(t *testing.T) {
		cases := []struct {
			name     string
			id       string
			role     string
			access   string
			wantCode int
			wantBody string
		}{
			{"missing operator id", "", "FinanceManager", "read", http.StatusForbidden, "operator_forbidden"},
			{"blank operator id", "   ", "FinanceManager", "read", http.StatusForbidden, "operator_forbidden"},
			{"missing access", "op-1", "FinanceManager", "", http.StatusForbidden, "operator_forbidden"},
			{"invalid access", "op-1", "FinanceManager", "admin", http.StatusForbidden, "operator_forbidden"},
			{"read access passes guard", "op-1", "CustomViewer", "read", http.StatusBadRequest, "invalid_filter"},
			{"write access passes guard", "op-1", "AdOptimizer", "write", http.StatusBadRequest, "invalid_filter"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				// Invalid query triggers 400 inside handler without touching DB pool, proving middleware passed.
				req := httptest.NewRequest(http.MethodGet, "/internal/conversion/events?partner_report_state=unknown_state", nil)
				req.Header.Set("Authorization", "Bearer service-secret")
				if tc.id != "" {
					req.Header.Set("X-Operator-ID", tc.id)
				}
				req.Header.Set("X-Operator-Role", tc.role)
				if tc.access != "" {
					req.Header.Set("X-Operator-Access", tc.access)
				}
				res := httptest.NewRecorder()
				handler.ServeHTTP(res, req)
				if res.Code != tc.wantCode || !strings.Contains(res.Body.String(), tc.wantBody) {
					t.Fatalf("status=%d body=%s, want %d %q", res.Code, res.Body.String(), tc.wantCode, tc.wantBody)
				}
			})
		}
	})

	t.Run("non-GET enforces write access and exact route permission", func(t *testing.T) {
		routes := []struct {
			method   string
			path     string
			wantPerm string
			wrong    string
		}{
			{http.MethodPost, "/internal/conversion/destinations", delivery.PermissionAdsPixelsWrite, delivery.PermissionAdsConversionsWrite},
			{http.MethodPatch, "/internal/conversion/destinations/1", delivery.PermissionAdsPixelsWrite, delivery.PermissionAdsConversionsWrite},
			{http.MethodPost, "/internal/conversion/integrations", delivery.PermissionAdsPixelsWrite, delivery.PermissionAdsConversionsWrite},
			{http.MethodPatch, "/internal/conversion/integrations/1", delivery.PermissionAdsPixelsWrite, delivery.PermissionAdsConversionsWrite},
			{http.MethodPut, "/internal/conversion/integrations/1/sinan-mock", delivery.PermissionAdsPixelsWrite, delivery.PermissionAdsConversionsWrite},
			{http.MethodPost, "/internal/conversion/events/1/deliveries", delivery.PermissionAdsConversionsWrite, delivery.PermissionAdsPixelsWrite},
			{http.MethodPost, "/internal/conversion/deliveries/1/retry", delivery.PermissionAdsConversionsWrite, delivery.PermissionAdsPixelsWrite},
			{http.MethodPost, "/internal/conversion/partner-reports/1/retry", delivery.PermissionAdsConversionsWrite, delivery.PermissionAdsPixelsWrite},
			{http.MethodPost, "/internal/conversion/integrations/1/payment-policy/preview", delivery.PermissionAdsConversionsWrite, delivery.PermissionAdsPixelsWrite},
			{http.MethodPost, "/internal/conversion/integrations/1/payment-policy/publish", delivery.PermissionAdsConversionsWrite, delivery.PermissionAdsPixelsWrite},
		}
		for _, rt := range routes {
			t.Run(rt.method+" "+rt.path, func(t *testing.T) {
				call := func(access, perm string) *httptest.ResponseRecorder {
					// Malformed JSON triggers 400 inside handler without touching DB pool, proving auth passed.
					req := httptest.NewRequest(rt.method, rt.path, strings.NewReader("{"))
					req.Header.Set("Authorization", "Bearer service-secret")
					req.Header.Set("X-Operator-ID", "op-1")
					req.Header.Set("X-Operator-Role", "CustomRole")
					if access != "" {
						req.Header.Set("X-Operator-Access", access)
					}
					if perm != "" {
						req.Header.Set("X-Operator-Permission", perm)
					}
					res := httptest.NewRecorder()
					handler.ServeHTTP(res, req)
					return res
				}
				if res := call("read", rt.wantPerm); res.Code != http.StatusForbidden {
					t.Fatalf("read access should be forbidden, got %d %s", res.Code, res.Body.String())
				}
				if res := call("write", ""); res.Code != http.StatusForbidden {
					t.Fatalf("missing permission should be forbidden, got %d %s", res.Code, res.Body.String())
				}
				if res := call("write", rt.wrong); res.Code != http.StatusForbidden {
					t.Fatalf("wrong permission %q should be forbidden, got %d %s", rt.wrong, res.Code, res.Body.String())
				}
				if res := call("write", rt.wantPerm); res.Code != http.StatusBadRequest {
					t.Fatalf("matching permission %q should pass guard to 400 validation, got %d %s", rt.wantPerm, res.Code, res.Body.String())
				}
			})
		}
	})

	t.Run("unknown non-GET route fails closed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/internal/conversion/destinations/1", nil)
		req.Header.Set("Authorization", "Bearer service-secret")
		req.Header.Set("X-Operator-ID", "op-1")
		req.Header.Set("X-Operator-Access", "write")
		req.Header.Set("X-Operator-Permission", delivery.PermissionAdsPixelsWrite)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusForbidden || !strings.Contains(res.Body.String(), "operator_forbidden") {
			t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
		}
	})
}

func TestOperatorFromRequest(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/internal/conversion/destinations", nil)
	req.Header.Set("X-Operator-ID", "admin-42")
	req.Header.Set("X-Operator-Role", "AdOptimizer")
	req.Header.Set("X-Operator-Permission", delivery.PermissionAdsPixelsWrite)

	got := operatorFrom(req)
	if got.ID != "admin-42" || got.Role != "AdOptimizer" || got.Permission != delivery.PermissionAdsPixelsWrite || got.RequireSuperAdmin {
		t.Fatalf("unexpected operator: %+v", got)
	}
}
