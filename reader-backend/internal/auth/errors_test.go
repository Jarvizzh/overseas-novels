package auth

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"reader-backend/internal/model"
)

type unavailableUserRepository struct {
	UserRepository
	cause error
}

func (r unavailableUserRepository) GetUserByID(context.Context, int64) (*model.User, error) {
	return nil, r.cause
}
func (r unavailableUserRepository) GetUserByEmail(context.Context, string) (*model.User, string, error) {
	return nil, "", r.cause
}

func TestAccountErrorsPreserveAvailabilityAndHideDiagnostics(t *testing.T) {
	cause := errors.New("SQLSTATE 08006 private database address")
	svc := NewAuthService(unavailableUserRepository{cause: cause})
	if _, err := svc.GetProfile(context.Background(), 1); !errors.Is(err, cause) {
		t.Fatalf("database error became missing account: %v", err)
	}
	if _, _, err := svc.Login(context.Background(), "a@example.invalid", "password"); !errors.Is(err, cause) {
		t.Fatalf("database error became wrong password: %v", err)
	}
	h := NewAuthHandler(svc)
	r := gin.New()
	r.GET("/profile", func(c *gin.Context) { c.Set("user_id", int64(1)); h.GetProfile(c) })
	r.POST("/login", h.Login)
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{{"GET", "/profile", "", 503}, {"POST", "/login", `{"email":"a@example.invalid","password":"password"}`, 500}} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body)))
		if w.Code != tc.status || strings.Contains(w.Body.String(), "SQLSTATE") || strings.Contains(w.Body.String(), "database address") {
			t.Fatalf("%s: %d %s", tc.path, w.Code, w.Body.String())
		}
	}
	missing := NewAuthService(unavailableUserRepository{cause: pgx.ErrNoRows})
	if _, err := missing.GetProfile(context.Background(), 1); !errors.Is(err, ErrUserNotFound) {
		t.Fatal(err)
	}
	if _, _, err := missing.Login(context.Background(), "a@example.invalid", "password"); !errors.Is(err, ErrInvalidAuth) {
		t.Fatal(err)
	}
}

func TestPasswordByteLimitPrecedesRepositoryWork(t *testing.T) {
	svc := NewAuthService(nil)
	for _, password := range []string{strings.Repeat("a", 73), strings.Repeat("中", 25), "short"} {
		if _, _, err := svc.Register(context.Background(), "a@example.invalid", password, "", "", "", "", "", "", "", "", "", "", ""); !errors.Is(err, ErrInvalidPassword) {
			t.Fatalf("register accepted invalid password: %v", err)
		}
		if _, _, err := svc.BindEmail(context.Background(), 1, "a@example.invalid", password, ""); !errors.Is(err, ErrInvalidPassword) {
			t.Fatalf("binding accepted invalid password: %v", err)
		}
	}
}
