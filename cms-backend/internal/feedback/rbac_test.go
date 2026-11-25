package feedback

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

type stubFeedbackService struct{}

func (stubFeedbackService) List(context.Context, int, int, string, string) ([]Feedback, int, error) {
	return []Feedback{{ID: 1, Subject: "Help", Status: "pending"}}, 1, nil
}

func (stubFeedbackService) GetByID(context.Context, int64) (*Feedback, error) {
	return &Feedback{ID: 1, Subject: "Help", Status: "pending"}, nil
}

func (stubFeedbackService) Update(context.Context, int64, *UpdateFeedbackRequest) error {
	return nil
}

func TestFeedbackRoutesEnforceRBACPermissions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	prevCfg := config.AppConfig
	config.AppConfig = &config.Config{JWTSecret: "feedback-rbac-test-secret"}
	t.Cleanup(func() { config.AppConfig = prevCfg })

	principals := map[string]*auth.Principal{
		"dash-only": auth.NewPrincipal("dash-only", "dash", "Dash", 2, "Dash", "仅总览", false, auth.StatusActive, auth.StatusActive, []string{auth.PermDashboardRead}),
		"fb-reader": auth.NewPrincipal("fb-reader", "fr", "FR", 3, "Reader", "只读", false, auth.StatusActive, auth.StatusActive, []string{auth.PermSupportFeedbackRead}),
		"fb-writer": auth.NewPrincipal("fb-writer", "fw", "FW", 4, "Writer", "处理", false, auth.StatusActive, auth.StatusActive, []string{auth.PermSupportFeedbackWrite}),
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
	NewHandler(stubFeedbackService{}).RegisterRoutes(r.Group("/api/v1/admin"))

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

	if code := call("dash-only", http.MethodGet, "/feedback", ""); code != http.StatusForbidden {
		t.Fatalf("dash-only GET /feedback = %d, want 403", code)
	}
	if code := call("fb-reader", http.MethodGet, "/feedback", ""); code != http.StatusOK {
		t.Fatalf("fb-reader GET /feedback = %d, want 200", code)
	}
	if code := call("fb-reader", http.MethodGet, "/feedback/1", ""); code != http.StatusOK {
		t.Fatalf("fb-reader GET /feedback/1 = %d, want 200", code)
	}
	if code := call("fb-reader", http.MethodPut, "/feedback/1", `{"status":"replied","admin_reply":"ok"}`); code != http.StatusForbidden {
		t.Fatalf("fb-reader PUT /feedback/1 = %d, want 403", code)
	}
	if code := call("fb-writer", http.MethodPut, "/feedback/1", `{"status":"replied","admin_reply":"ok"}`); code != http.StatusOK {
		t.Fatalf("fb-writer PUT /feedback/1 = %d, want 200", code)
	}
}
