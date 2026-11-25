package user

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

type stubUserService struct{}

func (stubUserService) ListUsers(context.Context, int, int, string, string) ([]User, int, error) {
	return []User{{ID: 1, Nickname: "reader-1", Status: 1}}, 1, nil
}

func (stubUserService) GetUserDetail(context.Context, int64) (*UserDetail, error) {
	return &UserDetail{User: User{ID: 1, Nickname: "reader-1", Status: 1}}, nil
}

func (stubUserService) ToggleUserStatus(context.Context, int64, int16, string) (string, error) {
	return "banned", nil
}

func (stubUserService) AdjustWallet(context.Context, int64, int, bool, string, string) error {
	return nil
}

func TestUserRoutesEnforceRBACPermissions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	prevCfg := config.AppConfig
	config.AppConfig = &config.Config{JWTSecret: "user-rbac-test-secret"}
	t.Cleanup(func() { config.AppConfig = prevCfg })

	principals := map[string]*auth.Principal{
		"dash-only":   auth.NewPrincipal("dash-only", "dash", "Dash", 2, "Dash", "仅总览", false, auth.StatusActive, auth.StatusActive, []string{auth.PermDashboardRead}),
		"user-reader": auth.NewPrincipal("user-reader", "ur", "UR", 3, "FinanceManager", "财务", false, auth.StatusActive, auth.StatusActive, []string{auth.PermRevenueUsersRead}),
		"user-writer": auth.NewPrincipal("user-writer", "uw", "UW", 4, "ContentOperations", "内容运营", false, auth.StatusActive, auth.StatusActive, []string{auth.PermRevenueUsersWrite}),
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
	NewHandler(stubUserService{}).RegisterRoutes(r.Group("/api/v1/admin"))

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

	// dashboard:read MUST NOT access user list or details
	if code := call("dash-only", http.MethodGet, "/users", ""); code != http.StatusForbidden {
		t.Fatalf("dash-only GET /users = %d, want 403", code)
	}
	if code := call("dash-only", http.MethodGet, "/users/1", ""); code != http.StatusForbidden {
		t.Fatalf("dash-only GET /users/1 = %d, want 403", code)
	}

	// revenue.users:read can read, cannot write
	if code := call("user-reader", http.MethodGet, "/users", ""); code != http.StatusOK {
		t.Fatalf("user-reader GET /users = %d, want 200", code)
	}
	if code := call("user-reader", http.MethodGet, "/users/1", ""); code != http.StatusOK {
		t.Fatalf("user-reader GET /users/1 = %d, want 200", code)
	}
	if code := call("user-reader", http.MethodPut, "/users/1/status", `{"status":2}`); code != http.StatusForbidden {
		t.Fatalf("user-reader PUT /users/1/status = %d, want 403", code)
	}
	if code := call("user-reader", http.MethodPost, "/users/1/adjust-wallet", `{"amount":100,"reason":"test"}`); code != http.StatusForbidden {
		t.Fatalf("user-reader POST /users/1/adjust-wallet = %d, want 403", code)
	}

	// revenue.users:write can read and write
	if code := call("user-writer", http.MethodGet, "/users/1", ""); code != http.StatusOK {
		t.Fatalf("user-writer GET /users/1 = %d, want 200", code)
	}
	if code := call("user-writer", http.MethodPut, "/users/1/status", `{"status":2}`); code != http.StatusOK {
		t.Fatalf("user-writer PUT /users/1/status = %d, want 200", code)
	}
	if code := call("user-writer", http.MethodPost, "/users/1/adjust-wallet", `{"amount":100,"reason":"test"}`); code != http.StatusOK {
		t.Fatalf("user-writer POST /users/1/adjust-wallet = %d, want 200", code)
	}
}
