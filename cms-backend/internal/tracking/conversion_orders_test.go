package tracking

import (
	"context"
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
