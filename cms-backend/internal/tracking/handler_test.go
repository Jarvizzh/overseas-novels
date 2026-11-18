package tracking

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"star-novel-cms/internal/auth"
	"star-novel-cms/internal/config"
)

type stubTrackingService struct {
	called bool
}

func (s *stubTrackingService) GetLogs(_ context.Context, _, _ int, _, _, _ string) ([]*FacebookCAPILog, int, error) {
	s.called = true
	return []*FacebookCAPILog{}, 0, nil
}

func TestTrackingLogsRequiresSuperAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	prevCfg := config.AppConfig
	config.AppConfig = &config.Config{JWTSecret: "unit-test-secret"}
	t.Cleanup(func() { config.AppConfig = prevCfg })

	cases := []struct {
		name       string
		role       string
		wantStatus int
		wantCalled bool
	}{
		{name: "unauthenticated", role: "", wantStatus: http.StatusUnauthorized, wantCalled: false},
		{name: "media buyer forbidden", role: "MediaBuyer", wantStatus: http.StatusForbidden, wantCalled: false},
		{name: "editor forbidden", role: "Editor", wantStatus: http.StatusForbidden, wantCalled: false},
		{name: "finance forbidden", role: "Finance", wantStatus: http.StatusForbidden, wantCalled: false},
		{name: "superadmin allowed", role: "SuperAdmin", wantStatus: http.StatusOK, wantCalled: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := &stubTrackingService{}
			r := gin.New()
			NewHandler(svc).RegisterRoutes(r.Group("/api/v1/admin"))

			req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/tracking-logs", nil)
			if tc.role != "" {
				tok, err := auth.GenerateToken("admin-test", tc.role)
				if err != nil {
					t.Fatalf("generate token: %v", err)
				}
				req.Header.Set("Authorization", "Bearer "+tok)
			}

			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != tc.wantStatus {
				t.Fatalf("expected status %d, got %d: %s", tc.wantStatus, w.Code, w.Body.String())
			}
			if svc.called != tc.wantCalled {
				t.Fatalf("expected service called=%v, got %v", tc.wantCalled, svc.called)
			}
		})
	}
}
