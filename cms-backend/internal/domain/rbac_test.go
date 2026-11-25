package domain

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

type stubDomainService struct{}

func (stubDomainService) ListDomains(context.Context) ([]*SystemDomain, error) {
	return []*SystemDomain{{ID: 1, Name: "Main", Domain: "read.example.com", Type: "main", Status: 1, IsDefault: true}}, nil
}

func (stubDomainService) CreateDomain(context.Context, *CreateDomainRequest) (*SystemDomain, error) {
	return &SystemDomain{ID: 2, Name: "Sub", Domain: "sub.example.com", Type: "sub", Status: 1}, nil
}

func (stubDomainService) UpdateDomainStatus(context.Context, int, int16) error { return nil }
func (stubDomainService) SetDefaultDomain(context.Context, int) error          { return nil }
func (stubDomainService) DeleteDomain(context.Context, int) error              { return nil }

func TestDomainRoutesEnforceRBACPermissions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	prevCfg := config.AppConfig
	config.AppConfig = &config.Config{JWTSecret: "domain-rbac-test-secret"}
	t.Cleanup(func() { config.AppConfig = prevCfg })

	principals := map[string]*auth.Principal{
		"dash-only":     auth.NewPrincipal("dash-only", "dash", "Dash", 2, "Dash", "仅总览", false, auth.StatusActive, auth.StatusActive, []string{auth.PermDashboardRead}),
		"domain-reader": auth.NewPrincipal("domain-reader", "dr", "DR", 3, "Leader", "负责人", false, auth.StatusActive, auth.StatusActive, []string{auth.PermSystemDomainsRead}),
		"domain-writer": auth.NewPrincipal("domain-writer", "dw", "DW", 4, "Admin", "管理员", false, auth.StatusActive, auth.StatusActive, []string{auth.PermSystemDomainsWrite}),
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
	NewHandler(stubDomainService{}).RegisterRoutes(r.Group("/api/v1/admin"))

	call := func(adminID, method, path, body string) int {
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
		return w.Code
	}

	if code := call("dash-only", http.MethodGet, "/domains", ""); code != http.StatusForbidden {
		t.Fatalf("dash-only GET /domains = %d, want 403", code)
	}
	if code := call("domain-reader", http.MethodGet, "/domains", ""); code != http.StatusOK {
		t.Fatalf("domain-reader GET /domains = %d, want 200", code)
	}
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodPost, "/domains", `{"name":"Sub","domain":"sub.example.com","type":"sub"}`},
		{http.MethodPut, "/domains/1/status", `{"status":2}`},
		{http.MethodPost, "/domains/1/set-default", ``},
		{http.MethodDelete, "/domains/1", ``},
	} {
		if code := call("domain-reader", tc.method, tc.path, tc.body); code != http.StatusForbidden {
			t.Fatalf("domain-reader %s %s = %d, want 403", tc.method, tc.path, code)
		}
		if code := call("domain-writer", tc.method, tc.path, tc.body); code != http.StatusOK {
			t.Fatalf("domain-writer %s %s = %d, want 200", tc.method, tc.path, code)
		}
	}
}
