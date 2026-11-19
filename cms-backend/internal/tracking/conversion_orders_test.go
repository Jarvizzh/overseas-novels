package tracking

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"

	"star-novel-cms/internal/auth"
	"star-novel-cms/internal/config"
)

type orderAdminLookup struct {
	admin *auth.Admin
}

func (s orderAdminLookup) GetAdminByID(_ context.Context, _ string) (*auth.Admin, error) {
	return s.admin, nil
}

func TestConversionOrdersRechecksRoleAndForwardsVerifiedIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	old := config.AppConfig
	config.AppConfig = &config.Config{JWTSecret: "tracking-reconciliation-test-secret"}
	t.Cleanup(func() { config.AppConfig = old })

	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/internal/conversion/orders/外部+1" || r.URL.Query().Get("integration_id") != "9" ||
			r.Header.Get("Authorization") != "Bearer private-fixture" ||
			r.Header.Get("X-Operator-ID") != "fixture-admin" || r.Header.Get("X-Operator-Role") != "Finance" {
			t.Errorf("unexpected upstream query or verified identity: %s", r.URL)
		}
		_, _ = w.Write([]byte(`{"order_id":"外部+1","events":[]}`))
	}))
	defer upstream.Close()

	for _, tt := range []struct {
		role   string
		active int16
		want   int
	}{
		{"Finance", 1, http.StatusOK},
		{"Finance", 2, http.StatusForbidden},
		{"Editor", 1, http.StatusForbidden},
	} {
		r := gin.New()
		err := RegisterConversionOrders(r.Group("/api/v1/admin"), orderAdminLookup{
			admin: &auth.Admin{ID: "fixture-admin", Role: tt.role, Status: tt.active},
		}, upstream.URL, "private-fixture")
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/conversion/orders/%E5%A4%96%E9%83%A8%2B1?integration_id=9", nil)
		token, err := auth.GenerateToken("fixture-admin", "SuperAdmin")
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-Operator-Role", "SuperAdmin")
		response := httptest.NewRecorder()
		r.ServeHTTP(response, req)
		if response.Code != tt.want {
			t.Errorf("active=%d role=%q status=%d, want %d: %s", tt.active, tt.role, response.Code, tt.want, response.Body)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("upstream requests = %d, want 1", calls.Load())
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

	token, err := auth.GenerateToken("admin-1", "SuperAdmin")
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
			if err := RegisterConversionOrders(r.Group("/api/v1/admin"), orderAdminLookup{
				admin: &auth.Admin{ID: "admin-1", Role: "SuperAdmin", Status: 1},
			}, upstream.URL, "private-fixture"); err != nil {
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

	for _, tc := range []struct {
		role       string
		wantStatus int
	}{
		{"Finance", http.StatusOK},
		{"Editor", http.StatusForbidden},
	} {
		r := gin.New()
		if err := RegisterConversionOrders(r.Group("/api/v1/admin"), orderAdminLookup{
			admin: &auth.Admin{ID: "admin-1", Role: tc.role, Status: 1},
		}, upstream.URL, "private-fixture"); err != nil {
			t.Fatal(err)
		}
		tok, err := auth.GenerateToken("admin-1", tc.role)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/conversion/audit?subject_type=destination&subject_id=10", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		res := httptest.NewRecorder()
		r.ServeHTTP(res, req)
		if res.Code != tc.wantStatus {
			t.Fatalf("role=%s status=%d want=%d body=%s", tc.role, res.Code, tc.wantStatus, res.Body.String())
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("upstream calls=%d want=1", calls.Load())
	}
}
