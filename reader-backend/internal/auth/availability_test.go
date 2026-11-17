package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"reader-backend/internal/config"
	"reader-backend/internal/testdb"
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

func TestAccountInputsMatchStorageAcrossRegistrationAndBinding(t *testing.T) {
	pool := testdb.Open(t)
	config.AppConfig = &config.Config{JWTSecret: "test-account-fields"}
	handler := NewAuthHandler(NewAuthService(NewUserRepository()))
	router := gin.New()
	router.POST("/guest", handler.GuestLogin)
	router.POST("/register", handler.Register)
	router.POST("/bind/:user", func(c *gin.Context) {
		id, err := strconv.ParseInt(c.Param("user"), 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		c.Set("user_id", id)
	}, handler.BindEmail)
	nick := strings.Repeat("阅", 50)
	ua := strings.Repeat("阅", 256)
	longEmail := strings.Repeat("a", 55) + "@" + strings.Repeat("b", 49) + ".example"
	call := func(path string, body any, status int) AuthResponse {
		t.Helper()
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(encoded))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", ua)
		req.Header.Set("X-UTM-Source", strings.Repeat("x", 101))
		req.Header.Set("X-UTM-Campaign", strings.Repeat("y", 100))
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != status {
			t.Fatalf("%s: got %d want %d: %s", path, w.Code, status, w.Body)
		}
		var response AuthResponse
		if status == http.StatusOK {
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
		}
		return response
	}
	guest := call("/guest", nil, 200)
	var device, source, campaign string
	if err := pool.QueryRow(context.Background(), "SELECT device,utm_source,utm_campaign FROM users WHERE id=$1", guest.User.ID).Scan(&device, &source, &campaign); err != nil ||
		utf8.RuneCountInString(device) != 255 || source != "" || len(campaign) != 100 {
		t.Fatalf("bad stored diagnostic and attribution lengths: %d %d %d %v", utf8.RuneCountInString(device), len(source), len(campaign), err)
	}
	call("/register", RegisterRequest{Email: longEmail, Password: "Test-password", Nickname: nick + "阅"}, 400)
	created := call("/register", RegisterRequest{Email: longEmail, Password: "Test-password", Nickname: nick}, 200)
	if created.User.Email == nil || *created.User.Email != longEmail || created.User.Nickname != nick {
		t.Fatalf("valid account input was not preserved: %+v", created.User)
	}
	boundaryEmail := strings.Repeat("c", 60) + "@" + strings.Repeat("d", 63) + "." +
		strings.Repeat("e", 63) + "." + strings.Repeat("f", 59) + ".sample"
	boundary := call("/register", RegisterRequest{Email: boundaryEmail, Password: "Test-password"}, 200)
	if boundary.User.Email == nil || *boundary.User.Email != boundaryEmail {
		t.Fatalf("valid 255-character email did not round-trip: %+v", boundary.User.Email)
	}
	call("/register", RegisterRequest{Email: "c" + boundaryEmail, Password: "Test-password"}, 400)

	bind := "/bind/" + strconv.FormatInt(guest.User.ID, 10)
	call(bind, BindEmailRequest{Email: "guest+" + longEmail, Password: "Test-password", Nickname: strings.Repeat("🙂", 51)}, 400)
	bound := call(bind, BindEmailRequest{Email: "guest+" + longEmail, Password: "Test-password", Nickname: strings.Repeat("🙂", 50)}, 200)
	if bound.User.ID != guest.User.ID || utf8.RuneCountInString(bound.User.Nickname) != 50 {
		t.Fatalf("binding changed account identity or lost Unicode nickname: %+v", bound.User)
	}
}
