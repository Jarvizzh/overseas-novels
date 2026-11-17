package auth

import (
	"context"
	"github.com/gin-gonic/gin"
	"net/http/httptest"
	"reader-backend/internal/config"
	"reader-backend/internal/testdb"
	"strings"
	"testing"
)

func TestAccountSessionAndBinding(t *testing.T) {
	p := testdb.Open(t)
	ctx := context.Background()
	gin.SetMode(gin.TestMode)
	config.AppConfig = &config.Config{JWTSecret: "test-session-secret"}
	svc := NewAuthService(NewUserRepository())
	user, _, err := svc.GuestLogin(ctx, "device", "127.0.0.1", "", "", "", "", "", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	registered, _, err := svc.Register(ctx, "registered@example.invalid", "password123", "", "device", "127.0.0.1", "", "", "", "", "", "", "", "")
	if err != nil || registered.Email == nil {
		t.Fatalf("standard registration: %v", err)
	}
	session := sessionService{}
	raw, err := session.create(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	id, err := session.refresh(ctx, raw)
	if err != nil || id != user.ID {
		t.Fatalf("refresh: %d %v", id, err)
	}
	var stored string
	if err := p.QueryRow(ctx, "SELECT token_hash FROM reader_sessions").Scan(&stored); err != nil || stored == raw {
		t.Fatal("raw refresh credential stored", err)
	}
	if _, _, err := svc.BindEmail(ctx, id, "guest@example.invalid", "password123", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.BindEmail(ctx, id, "other@example.invalid", "newpassword", ""); err != ErrAlreadyBound {
		t.Fatalf("registered account rebound: %v", err)
	}
	if _, err := session.refresh(ctx, raw); err == nil {
		t.Fatal("binding did not revoke old session")
	}
	raw, err = session.create(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	access, err := generateToken(id, sessionHash(raw))
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.GET("/protected", AuthMiddleware(), func(c *gin.Context) { c.Status(204) })
	checkAccess := func(want int) {
		t.Helper()
		req := httptest.NewRequest("GET", "/protected", nil)
		req.Header.Set("Authorization", "Bearer "+access)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("access status=%d want %d", w.Code, want)
		}
	}
	checkAccess(204)
	if err := session.logout(ctx, raw); err != nil {
		t.Fatal(err)
	}
	checkAccess(403)
	if _, err := session.refresh(ctx, raw); err == nil {
		t.Fatal("revoked credential accepted")
	}
	raw, err = session.create(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Exec(ctx, "UPDATE reader_sessions SET expires_at=now()-interval '1 second'"); err != nil {
		t.Fatal(err)
	}
	if _, err := session.refresh(ctx, raw); err == nil {
		t.Fatal("expired credential accepted")
	}
}
func TestUntrustedForwardedHeadersDoNotBypassLimiter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	if err := r.SetTrustedProxies(nil); err != nil {
		t.Fatal(err)
	}
	r.POST("/", GuestRateLimitMiddleware(0.001, 1), func(c *gin.Context) { c.Status(204) })
	for i, ip := range []string{"198.51.100.1", "198.51.100.2"} {
		req := httptest.NewRequest("POST", "/", strings.NewReader(""))
		req.RemoteAddr = "192.0.2.1:1000"
		req.Header.Set("X-Forwarded-For", ip)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		want := 204
		if i > 0 {
			want = 429
		}
		if w.Code != want {
			t.Fatalf("status=%d want %d", w.Code, want)
		}
	}
}
