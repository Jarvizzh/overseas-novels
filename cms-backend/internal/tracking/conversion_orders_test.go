package tracking

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"

	"star-novel-cms/internal/auth"
	"star-novel-cms/internal/config"
)

func stubPrincipalLoader(t *testing.T, principals map[string]*auth.Principal) {
	t.Helper()
	restore := auth.SetPrincipalLoaderForTest(func(_ context.Context, adminID string) (*auth.Principal, error) {
		p, ok := principals[adminID]
		if !ok || p == nil || !p.IsActive() {
			return nil, auth.ErrAccountInactive
		}
		return p, nil
	})
	t.Cleanup(restore)
}

func TestConversionOrdersEnforcesPermissionsAndForwardsVerifiedHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	old := config.AppConfig
	config.AppConfig = &config.Config{JWTSecret: "tracking-reconciliation-test-secret"}
	t.Cleanup(func() { config.AppConfig = old })

	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/internal/conversion/orders/外部+1" || r.URL.Query().Get("integration_id") != "9" ||
			r.Header.Get("Authorization") != "Bearer private-fixture" ||
			r.Header.Get("X-Operator-ID") != "conv-reader" ||
			r.Header.Get("X-Operator-Role") != "FinanceManager" ||
			r.Header.Get("X-Operator-Access") != "read" ||
			r.Header.Get("X-Operator-Permission") != auth.PermAdsConversionsRead {
			t.Errorf("unexpected upstream query or headers: path=%s id=%q role=%q access=%q perm=%q",
				r.URL, r.Header.Get("X-Operator-ID"), r.Header.Get("X-Operator-Role"),
				r.Header.Get("X-Operator-Access"), r.Header.Get("X-Operator-Permission"))
		}
		_, _ = w.Write([]byte(`{"order_id":"外部+1","events":[]}`))
	}))
	defer upstream.Close()

	stubPrincipalLoader(t, map[string]*auth.Principal{
		"conv-reader": auth.NewPrincipal("conv-reader", "fin", "Fin", 5, "FinanceManager", "财务", false, auth.StatusActive, auth.StatusActive, []string{auth.PermAdsConversionsRead}),
		"disabled":    auth.NewPrincipal("disabled", "dis", "Dis", 5, "FinanceManager", "财务", false, auth.StatusDisabled, auth.StatusActive, []string{auth.PermAdsConversionsRead}),
		"pixels-only": auth.NewPrincipal("pixels-only", "px", "Px", 3, "AdOptimizer", "优化师", false, auth.StatusActive, auth.StatusActive, []string{auth.PermAdsPixelsRead}),
	})

	r := gin.New()
	if err := RegisterConversionOrders(r.Group("/api/v1/admin"), nil, upstream.URL, "private-fixture"); err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		adminID string
		want    int
	}{
		{"conv-reader", http.StatusOK},
		{"disabled", http.StatusUnauthorized},
		{"pixels-only", http.StatusForbidden},
	} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/conversion/orders/%E5%A4%96%E9%83%A8%2B1?integration_id=9", nil)
		token, err := auth.GenerateToken(tt.adminID, "Admin")
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		// Attempt to spoof operator headers from client; proxy must overwrite them.
		req.Header.Set("X-Operator-Role", "Admin")
		req.Header.Set("X-Operator-Access", "write")
		req.Header.Set("X-Operator-Permission", auth.PermAdsConversionsWrite)
		response := httptest.NewRecorder()
		r.ServeHTTP(response, req)
		if response.Code != tt.want {
			t.Errorf("adminID=%q status=%d, want %d: %s", tt.adminID, response.Code, tt.want, response.Body)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("upstream requests = %d, want 1", calls.Load())
	}
}

func TestConversionPixelsVsConversionsPermissionSplit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	old := config.AppConfig
	config.AppConfig = &config.Config{JWTSecret: "tracking-split-secret"}
	t.Cleanup(func() { config.AppConfig = old })

	type captured struct {
		method, path, access, perm, role string
	}
	var last captured
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		last = captured{
			method: r.Method,
			path:   r.URL.Path,
			access: r.Header.Get("X-Operator-Access"),
			perm:   r.Header.Get("X-Operator-Permission"),
			role:   r.Header.Get("X-Operator-Role"),
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	stubPrincipalLoader(t, map[string]*auth.Principal{
		"pixels-writer": auth.NewPrincipal("pixels-writer", "pw", "PW", 10, "CustomPixels", "像素专员", false, auth.StatusActive, auth.StatusActive, []string{auth.PermAdsPixelsWrite}),
		"conv-writer":   auth.NewPrincipal("conv-writer", "cw", "CW", 11, "CustomConv", "回传专员", false, auth.StatusActive, auth.StatusActive, []string{auth.PermAdsConversionsWrite}),
		"conv-reader":   auth.NewPrincipal("conv-reader", "cr", "CR", 12, "CustomConvRead", "回传只读", false, auth.StatusActive, auth.StatusActive, []string{auth.PermAdsConversionsRead}),
		"dashboard":     auth.NewPrincipal("dashboard", "db", "DB", 13, "Dash", "仪表盘", false, auth.StatusActive, auth.StatusActive, []string{auth.PermDashboardRead}),
	})

	r := gin.New()
	if err := RegisterConversionOrders(r.Group("/api/v1/admin"), nil, upstream.URL, "private-fixture"); err != nil {
		t.Fatal(err)
	}

	call := func(adminID, method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		tok, err := auth.GenerateToken(adminID, "Custom")
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(method, "/api/v1/admin"+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+tok)
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		res := httptest.NewRecorder()
		r.ServeHTTP(res, req)
		return res
	}

	// Pixels writer can read/write destinations & integrations, forwarding ads.pixels:write
	if res := call("pixels-writer", http.MethodGet, "/conversion/integrations", ""); res.Code != http.StatusOK || last.access != "read" || last.perm != auth.PermAdsPixelsRead {
		t.Fatalf("pixels-writer GET integrations: code=%d last=%+v", res.Code, last)
	}
	if res := call("pixels-writer", http.MethodPost, "/conversion/destinations", `{}`); res.Code != http.StatusOK || last.access != "write" || last.perm != auth.PermAdsPixelsWrite || last.role != "CustomPixels" {
		t.Fatalf("pixels-writer POST destinations: code=%d last=%+v", res.Code, last)
	}
	if res := call("pixels-writer", http.MethodPatch, "/conversion/integrations/1", `{}`); res.Code != http.StatusOK || last.access != "write" || last.perm != auth.PermAdsPixelsWrite {
		t.Fatalf("pixels-writer PATCH integrations: code=%d last=%+v", res.Code, last)
	}
	// Pixels writer CANNOT read/write payment-policy, events, audit, retries, or promotion-options
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/conversion/events"},
		{http.MethodGet, "/conversion/audit"},
		{http.MethodGet, "/conversion/integrations/1/payment-policy"},
		{http.MethodGet, "/conversion/promotion-options"},
		{http.MethodPost, "/conversion/integrations/1/payment-policy/publish"},
		{http.MethodPost, "/conversion/deliveries/1/retry"},
	} {
		if res := call("pixels-writer", tc.method, tc.path, `{}`); res.Code != http.StatusForbidden {
			t.Fatalf("pixels-writer %s %s expected 403, got %d: %s", tc.method, tc.path, res.Code, res.Body.String())
		}
	}

	// Conversions writer can read destinations/integrations (with ads.conversions:read) and write payment-policy/retries (with ads.conversions:write)
	if res := call("conv-writer", http.MethodGet, "/conversion/destinations", ""); res.Code != http.StatusOK || last.access != "read" || last.perm != auth.PermAdsConversionsRead {
		t.Fatalf("conv-writer GET destinations: code=%d last=%+v", res.Code, last)
	}
	if res := call("conv-writer", http.MethodPost, "/conversion/integrations/1/payment-policy/publish", `{}`); res.Code != http.StatusOK || last.access != "write" || last.perm != auth.PermAdsConversionsWrite {
		t.Fatalf("conv-writer POST publish: code=%d last=%+v", res.Code, last)
	}
	if res := call("conv-writer", http.MethodPost, "/conversion/deliveries/5/retry", `{}`); res.Code != http.StatusOK || last.access != "write" || last.perm != auth.PermAdsConversionsWrite {
		t.Fatalf("conv-writer POST retry: code=%d last=%+v", res.Code, last)
	}
	// Conversions writer CANNOT write destinations/integrations
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/conversion/destinations"},
		{http.MethodPatch, "/conversion/destinations/1"},
		{http.MethodPost, "/conversion/integrations"},
		{http.MethodPatch, "/conversion/integrations/1"},
	} {
		if res := call("conv-writer", tc.method, tc.path, `{}`); res.Code != http.StatusForbidden {
			t.Fatalf("conv-writer %s %s expected 403, got %d", tc.method, tc.path, res.Code)
		}
	}

	// Conversions read-only cannot write payment-policy or retries
	if res := call("conv-reader", http.MethodPost, "/conversion/integrations/1/payment-policy/preview", `{}`); res.Code != http.StatusForbidden {
		t.Fatalf("conv-reader POST preview expected 403, got %d", res.Code)
	}
	// Dashboard-only gets 403 everywhere
	if res := call("dashboard", http.MethodGet, "/conversion/promotion-options", ""); res.Code != http.StatusForbidden {
		t.Fatalf("dashboard GET promotion-options expected 403, got %d", res.Code)
	}
}

func TestConversionOrdersMapsUpstreamErrorCodes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	old := config.AppConfig
	config.AppConfig = &config.Config{JWTSecret: "tracking-error-map-secret"}
	t.Cleanup(func() { config.AppConfig = old })

	cases := []struct {
		name           string
		upstreamStatus int
		upstreamBody   string
		wantCode       string
		wantError      string
	}{
		{
			name:           "400 invalid_time",
			upstreamStatus: http.StatusBadRequest,
			upstreamBody:   `{"code":"invalid_time"}`,
			wantCode:       "invalid_time",
			wantError:      "时间范围格式错误，或开始时间晚于结束时间",
		},
		{
			name:           "400 invalid_filter",
			upstreamStatus: http.StatusBadRequest,
			upstreamBody:   `{"code":"invalid_filter"}`,
			wantCode:       "invalid_filter",
			wantError:      "筛选参数格式错误",
		},
		{
			name:           "400 payment policy request",
			upstreamStatus: http.StatusBadRequest,
			upstreamBody:   `{"code":"invalid_payment_policy_request","field":"publication_id"}`,
			wantCode:       "invalid_payment_policy_request",
			wantError:      "回传规则请求格式无效，请核对配置后重试",
		},
		{
			name:           "404 resource_not_found",
			upstreamStatus: http.StatusNotFound,
			upstreamBody:   `{"code":"resource_not_found"}`,
			wantCode:       "resource_not_found",
			wantError:      "未找到指定的事件、投递或目标记录",
		},
		{
			name:           "400 invalid_limit",
			upstreamStatus: http.StatusBadRequest,
			upstreamBody:   `{"code":"invalid_limit"}`,
			wantCode:       "invalid_limit",
			wantError:      "每页条数超出允许范围",
		},
		{
			name:           "409 state_conflict",
			upstreamStatus: http.StatusConflict,
			upstreamBody:   `{"code":"state_conflict"}`,
			wantCode:       "state_conflict",
			wantError:      "记录状态已变化，或存在重复 Pixel/接入账户与推广链绑定冲突，请刷新后核对",
		},
		{
			name:           "400 unknown code falls back safely",
			upstreamStatus: http.StatusBadRequest,
			upstreamBody:   `{"code":"internal_sql_secret","error":"raw sql leak"}`,
			wantCode:       "invalid_order_key",
			wantError:      "查询或操作参数格式错误",
		},
		{
			name:           "404 non-json body falls back safely",
			upstreamStatus: http.StatusNotFound,
			upstreamBody:   `not json`,
			wantCode:       "order_not_found",
			wantError:      "未找到指定记录",
		},
	}

	stubPrincipalLoader(t, map[string]*auth.Principal{
		"admin-1": auth.NewPrincipal("admin-1", "admin", "Admin", 1, "Admin", "管理员", true, auth.StatusActive, auth.StatusActive, nil),
	})

	token, err := auth.GenerateToken("admin-1", "Admin")
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.upstreamStatus)
				_, _ = w.Write([]byte(tc.upstreamBody))
			}))
			defer upstream.Close()

			r := gin.New()
			if err := RegisterConversionOrders(r.Group("/api/v1/admin"), nil, upstream.URL, "private-fixture"); err != nil {
				t.Fatal(err)
			}

			req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/conversion/events", nil)
			req.Header.Set("Authorization", "Bearer "+token)
			res := httptest.NewRecorder()
			r.ServeHTTP(res, req)

			if res.Code != tc.upstreamStatus {
				t.Fatalf("status=%d want=%d body=%s", res.Code, tc.upstreamStatus, res.Body.String())
			}
			var got conversionErrorEntry
			if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
				t.Fatalf("invalid json response %s: %v", res.Body.String(), err)
			}
			if got.Code != tc.wantCode || got.Error != tc.wantError {
				t.Fatalf("got=%+v want code=%q error=%q", got, tc.wantCode, tc.wantError)
			}
		})
	}
}

func TestConversionPolicyProxyForwardsPublicationAuditAndLargeDraft(t *testing.T) {
	gin.SetMode(gin.TestMode)
	old := config.AppConfig
	config.AppConfig = &config.Config{JWTSecret: "tracking-publication-test-secret"}
	t.Cleanup(func() { config.AppConfig = old })

	const publicationID = "0000000000000000000000000000a016"
	input := `{"app_id":"reader","expected_version":null,"publication_id":"` + publicationID + `","operations":[]}` +
		strings.Repeat(" ", 9<<10)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Operator-ID") != "fixture-admin" || r.Header.Get("X-Operator-Role") != "AdOptimizer" {
			t.Errorf("proxy did not forward verified operator: id=%q role=%q", r.Header.Get("X-Operator-ID"), r.Header.Get("X-Operator-Role"))
		}
		switch r.Method {
		case http.MethodGet:
			q := r.URL.Query()
			if r.URL.Path != "/internal/conversion/audit" || q.Get("publication_id") != publicationID ||
				q.Get("app_id") != "reader" || q.Get("promotion_id") != "13" || q.Get("subject_id") != "1" ||
				r.Header.Get("X-Operator-Access") != "read" || r.Header.Get("X-Operator-Permission") != auth.PermAdsConversionsRead {
				t.Errorf("unexpected audit query: %s", r.URL)
			}
			_, _ = w.Write([]byte(`{"publication_id":"` + publicationID + `","record":null,"current":{}}`))
		case http.MethodPost:
			body, err := io.ReadAll(r.Body)
			if err != nil || string(body) != input || r.URL.Path != "/internal/conversion/integrations/1/payment-policy/publish" ||
				r.Header.Get("X-Operator-Access") != "write" || r.Header.Get("X-Operator-Permission") != auth.PermAdsConversionsWrite {
				t.Errorf("unexpected publish request: %s %d bytes %v", r.URL, len(body), err)
			}
			_, _ = w.Write([]byte(`{"config_source":"own"}`))
		}
	}))
	defer upstream.Close()

	stubPrincipalLoader(t, map[string]*auth.Principal{
		"fixture-admin": auth.NewPrincipal("fixture-admin", "opt", "Opt", 3, "AdOptimizer", "优化师", false, auth.StatusActive, auth.StatusActive, []string{auth.PermAdsConversionsWrite}),
	})

	r := gin.New()
	if err := RegisterConversionOrders(r.Group("/api/v1/admin"), nil, upstream.URL, "private-fixture"); err != nil {
		t.Fatal(err)
	}
	token, err := auth.GenerateToken("fixture-admin", "AdOptimizer")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ method, path, body, want string }{
		{http.MethodGet, "/audit?subject_type=integration&subject_id=1&app_id=reader&promotion_id=13&publication_id=" + publicationID, "", publicationID},
		{http.MethodPost, "/integrations/1/payment-policy/publish", input, `"config_source":"own"`},
	} {
		req := httptest.NewRequest(test.method, "/api/v1/admin/conversion"+test.path, strings.NewReader(test.body))
		req.Header.Set("Authorization", "Bearer "+token)
		res := httptest.NewRecorder()
		r.ServeHTTP(res, req)
		if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), test.want) {
			t.Fatalf("%s %s: %d %s", test.method, test.path, res.Code, res.Body.String())
		}
	}
}

func TestConversionOrdersAuditProxy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	old := config.AppConfig
	config.AppConfig = &config.Config{JWTSecret: "tracking-audit-proxy-secret"}
	t.Cleanup(func() { config.AppConfig = old })

	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/internal/conversion/audit" ||
			r.URL.Query().Get("subject_type") != "destination" ||
			r.URL.Query().Get("subject_id") != "10" {
			t.Errorf("unexpected upstream request: %s", r.URL.String())
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"records":[],"has_more":false}`))
	}))
	defer upstream.Close()

	stubPrincipalLoader(t, map[string]*auth.Principal{
		"fin-1":    auth.NewPrincipal("fin-1", "fin", "Fin", 5, "FinanceManager", "财务", false, auth.StatusActive, auth.StatusActive, []string{auth.PermAdsConversionsRead}),
		"content1": auth.NewPrincipal("content1", "ops", "Ops", 4, "ContentOperations", "运营", false, auth.StatusActive, auth.StatusActive, []string{auth.PermContentNovelsWrite}),
	})

	r := gin.New()
	if err := RegisterConversionOrders(r.Group("/api/v1/admin"), nil, upstream.URL, "private-fixture"); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		adminID    string
		wantStatus int
	}{
		{"fin-1", http.StatusOK},
		{"content1", http.StatusForbidden},
	} {
		tok, err := auth.GenerateToken(tc.adminID, "Any")
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/conversion/audit?subject_type=destination&subject_id=10", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		res := httptest.NewRecorder()
		r.ServeHTTP(res, req)
		if res.Code != tc.wantStatus {
			t.Fatalf("admin=%s status=%d want=%d body=%s", tc.adminID, res.Code, tc.wantStatus, res.Body.String())
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("upstream calls=%d want=1", calls.Load())
	}
}
