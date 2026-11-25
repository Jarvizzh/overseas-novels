package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"star-novel-cms/internal/config"
)

func setupTestJWTConfig(t *testing.T) {
	t.Helper()
	prev := config.AppConfig
	config.AppConfig = &config.Config{
		JWTSecret: "test-jwt-secret-for-auth-unit-tests-1234567890",
	}
	t.Cleanup(func() {
		config.AppConfig = prev
	})
}

func TestAuthMiddlewareZeroCacheRealTimeRevocation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupTestJWTConfig(t)

	var (
		mu       sync.RWMutex
		calls    atomic.Int32
		currentP = NewPrincipal("admin-1", "alice", "Alice", 2, "Leader", "负责人", false, StatusActive, StatusActive, []string{PermContentNovelsRead})
	)

	restore := SetPrincipalLoaderForTest(func(ctx context.Context, adminID string) (*Principal, error) {
		calls.Add(1)
		mu.RLock()
		defer mu.RUnlock()
		if currentP == nil || adminID != currentP.ID {
			return nil, ErrAccountInactive
		}
		if !currentP.IsActive() {
			return nil, ErrAccountInactive
		}
		return currentP, nil
	})
	t.Cleanup(restore)

	token, err := GenerateToken("admin-1", "OldStaleRole")
	if err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.GET("/novels", AuthMiddleware(), RequirePermission(PermContentNovelsRead), func(c *gin.Context) {
		p, ok := PrincipalFromContext(c)
		if !ok {
			c.Status(http.StatusInternalServerError)
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"role":       p.Role,
			"admin_role": c.GetString("admin_role"),
		})
	})

	// Request 1: active with permission -> 200 and live DB role code (not stale JWT role).
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/novels", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("req 1 status = %d, body = %s", w.Code, w.Body.String())
	}
	var body map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["role"] != "Leader" || body["admin_role"] != "Leader" {
		t.Fatalf("expected live DB role Leader, got %+v", body)
	}

	// Revoke permission immediately -> Request 2 with SAME JWT gets 403.
	mu.Lock()
	currentP = NewPrincipal("admin-1", "alice", "Alice", 2, "Leader", "负责人", false, StatusActive, StatusActive, []string{PermDashboardRead})
	mu.Unlock()

	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/novels", nil)
	req2.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(w2, req2)
	if w2.Code != http.StatusForbidden {
		t.Fatalf("req 2 status = %d, want 403, body = %s", w2.Code, w2.Body.String())
	}

	// Disable account immediately -> Request 3 with SAME JWT gets 401 account_inactive.
	mu.Lock()
	currentP = NewPrincipal("admin-1", "alice", "Alice", 2, "Leader", "负责人", false, StatusDisabled, StatusActive, []string{PermContentNovelsRead})
	mu.Unlock()

	w3 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodGet, "/novels", nil)
	req3.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(w3, req3)
	if w3.Code != http.StatusUnauthorized {
		t.Fatalf("req 3 status = %d, want 401, body = %s", w3.Code, w3.Body.String())
	}
	var errBody map[string]string
	_ = json.Unmarshal(w3.Body.Bytes(), &errBody)
	if errBody["code"] != "account_inactive" {
		t.Fatalf("expected code=account_inactive, got %+v", errBody)
	}

	if calls.Load() != 3 {
		t.Fatalf("expected 3 live lookups (zero cache), got %d", calls.Load())
	}
}

func TestPermissionMiddlewaresConcurrentRaceSafety(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupTestJWTConfig(t)

	principals := map[string]*Principal{
		"super": NewPrincipal("super", "root", "Root", 1, "Admin", "管理员", true, StatusActive, StatusActive, nil),
		"fin":   NewPrincipal("fin", "fin", "Fin", 5, "FinanceManager", "财务", false, StatusActive, StatusActive, []string{PermPaymentsProductsWrite, PermPaymentsRefundsWrite}),
		"ro":    NewPrincipal("ro", "ro", "RO", 6, "Viewer", "只读", false, StatusActive, StatusActive, []string{PermDashboardRead}),
	}
	restore := SetPrincipalLoaderForTest(func(ctx context.Context, adminID string) (*Principal, error) {
		p, ok := principals[adminID]
		if !ok {
			return nil, ErrAccountInactive
		}
		return p, nil
	})
	t.Cleanup(restore)

	router := gin.New()
	router.GET("/all", AuthMiddleware(), RequireAllPermissions(PermPaymentsProductsWrite, PermPaymentsChannelsWrite), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	router.GET("/any", AuthMiddleware(), RequireAnyPermission(PermPaymentsRefundsWrite, PermAdsPixelsWrite), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	router.GET("/super", AuthMiddleware(), RequireSuperAdmin(), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	tokSuper, _ := GenerateToken("super", "Admin")
	tokFin, _ := GenerateToken("fin", "FinanceManager")
	tokRO, _ := GenerateToken("ro", "Viewer")

	cases := []struct {
		path  string
		token string
		want  int
	}{
		{"/all", tokSuper, http.StatusOK},
		{"/all", tokFin, http.StatusForbidden},
		{"/any", tokFin, http.StatusOK},
		{"/any", tokRO, http.StatusForbidden},
		{"/super", tokSuper, http.StatusOK},
		{"/super", tokFin, http.StatusForbidden},
	}

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		for _, tc := range cases {
			wg.Add(1)
			go func(tc struct {
				path  string
				token string
				want  int
			}) {
				defer wg.Done()
				w := httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodGet, tc.path, nil)
				req.Header.Set("Authorization", "Bearer "+tc.token)
				router.ServeHTTP(w, req)
				if w.Code != tc.want {
					t.Errorf("%s status = %d, want %d", tc.path, w.Code, tc.want)
				}
			}(tc)
		}
	}
	wg.Wait()
}

// --- Fake pgx.Tx to verify RequirePermissionTx SQL lock and permission logic ---

type fakeRow struct {
	scanFn func(dest ...any) error
}

func (r fakeRow) Scan(dest ...any) error {
	return r.scanFn(dest...)
}

type fakeRows struct {
	items []string
	idx   int
}

func (r *fakeRows) Close()                                       {}
func (r *fakeRows) Err() error                                   { return nil }
func (r *fakeRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *fakeRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *fakeRows) Next() bool {
	if r.idx >= len(r.items) {
		return false
	}
	r.idx++
	return true
}
func (r *fakeRows) Scan(dest ...any) error {
	*(dest[0].(*string)) = r.items[r.idx-1]
	return nil
}
func (r *fakeRows) Values() ([]any, error) { return nil, nil }
func (r *fakeRows) RawValues() [][]byte    { return nil }
func (r *fakeRows) Conn() *pgx.Conn        { return nil }

type fakeTx struct {
	pgx.Tx
	adminStatus  int16
	roleStatus   int16
	isSuperAdmin bool
	notFound     bool
	perms        []string
	lastQuerySQL string
}

func (tx *fakeTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	tx.lastQuerySQL = sql
	return fakeRow{scanFn: func(dest ...any) error {
		if tx.notFound {
			return pgx.ErrNoRows
		}
		*(dest[0].(*string)) = args[0].(string)
		*(dest[1].(*string)) = "user"
		*(dest[2].(*string)) = "Nick"
		*(dest[3].(*int16)) = tx.adminStatus
		*(dest[4].(*int64)) = 2
		*(dest[5].(*string)) = "FinanceManager"
		*(dest[6].(*string)) = "财务"
		*(dest[7].(*bool)) = tx.isSuperAdmin
		*(dest[8].(*int16)) = tx.roleStatus
		return nil
	}}
}

func (tx *fakeTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return &fakeRows{items: tx.perms}, nil
}

func TestRequirePermissionTx(t *testing.T) {
	ctx := context.Background()

	// Active non-super-admin with required perms -> nil, locks FOR SHARE OF a, r.
	tx := &fakeTx{
		adminStatus: StatusActive,
		roleStatus:  StatusActive,
		perms:       []string{PermPaymentsProductsWrite, PermPaymentsChannelsWrite},
	}
	if err := RequirePermissionTx(ctx, tx, "admin-1", PermPaymentsProductsWrite, PermPaymentsChannelsWrite); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if !strings.Contains(tx.lastQuerySQL, "FOR SHARE OF a, r") {
		t.Fatalf("expected FOR SHARE OF a, r lock in query, got: %s", tx.lastQuerySQL)
	}

	// Missing one of multiple required perms -> ErrPermissionDenied.
	txMissing := &fakeTx{
		adminStatus: StatusActive,
		roleStatus:  StatusActive,
		perms:       []string{PermPaymentsProductsWrite},
	}
	if err := RequirePermissionTx(ctx, txMissing, "admin-1", PermPaymentsProductsWrite, PermPaymentsChannelsWrite); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("expected ErrPermissionDenied, got %v", err)
	}

	// Disabled role -> ErrAccountInactive.
	txDisabledRole := &fakeTx{
		adminStatus: StatusActive,
		roleStatus:  StatusDisabled,
		perms:       []string{PermPaymentsProductsWrite},
	}
	if err := RequirePermissionTx(ctx, txDisabledRole, "admin-1", PermPaymentsProductsWrite); !errors.Is(err, ErrAccountInactive) {
		t.Fatalf("expected ErrAccountInactive, got %v", err)
	}

	// Super admin bypasses permission list and passes RequireSuperAdminTx.
	txSuper := &fakeTx{
		adminStatus:  StatusActive,
		roleStatus:   StatusActive,
		isSuperAdmin: true,
	}
	if err := RequirePermissionTx(ctx, txSuper, "super-1", PermPaymentsChannelsWrite); err != nil {
		t.Fatalf("super admin should pass RequirePermissionTx, got %v", err)
	}
	if err := RequireSuperAdminTx(ctx, txSuper, "super-1"); err != nil {
		t.Fatalf("super admin should pass RequireSuperAdminTx, got %v", err)
	}

	// Non-super-admin fails RequireSuperAdminTx with ErrSuperAdminRequired (which wraps ErrPermissionDenied).
	if err := RequireSuperAdminTx(ctx, tx, "admin-1"); !errors.Is(err, ErrSuperAdminRequired) || !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("expected ErrSuperAdminRequired wrapping ErrPermissionDenied, got %v", err)
	}
}
