package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"star-novel-cms/internal/db"
)

const (
	StatusActive   int16 = 1
	StatusDisabled int16 = 2

	contextKeyPrincipal = "auth_principal"
)

// Principal represents the real-time authenticated administrator and effective permissions.
type Principal struct {
	ID            string   `json:"id"`
	Username      string   `json:"username"`
	Nickname      string   `json:"nickname"`
	RoleID        int64    `json:"role_id"`
	Role          string   `json:"role"`
	RoleName      string   `json:"role_name"`
	IsSuperAdmin  bool     `json:"is_super_admin"`
	Status        int16    `json:"-"`
	RoleStatus    int16    `json:"-"`
	Permissions   []string `json:"permissions"`
	permissionSet map[string]struct{}
}

// NewPrincipal constructs a Principal with normalized permissions and O(1) lookup set.
func NewPrincipal(
	id, username, nickname string,
	roleID int64,
	roleCode, roleName string,
	isSuperAdmin bool,
	status, roleStatus int16,
	permissions []string,
) *Principal {
	var normalized []string
	if isSuperAdmin {
		normalized = AllPermissionCodes()
	} else {
		normalized = normalizeKnownPermissions(permissions)
	}
	permSet := make(map[string]struct{}, len(normalized))
	for _, p := range normalized {
		permSet[p] = struct{}{}
	}
	return &Principal{
		ID:            id,
		Username:      username,
		Nickname:      nickname,
		RoleID:        roleID,
		Role:          roleCode,
		RoleName:      roleName,
		IsSuperAdmin:  isSuperAdmin,
		Status:        status,
		RoleStatus:    roleStatus,
		Permissions:   normalized,
		permissionSet: permSet,
	}
}

// IsActive reports whether both the admin account and its bound role are enabled.
func (p *Principal) IsActive() bool {
	return p != nil && p.Status == StatusActive && p.RoleStatus == StatusActive
}

// HasPermission reports whether the principal holds the given permission code.
// Super admins implicitly satisfy every permission.
func (p *Principal) HasPermission(code string) bool {
	if p == nil {
		return false
	}
	if p.IsSuperAdmin {
		return true
	}
	code = strings.TrimSpace(code)
	if code == "" {
		return false
	}
	if p.permissionSet != nil {
		_, ok := p.permissionSet[code]
		return ok
	}
	for _, perm := range p.Permissions {
		if perm == code {
			return true
		}
	}
	return false
}

// HasAllPermissions reports whether the principal holds every code in codes.
func (p *Principal) HasAllPermissions(codes ...string) bool {
	if p == nil {
		return false
	}
	if p.IsSuperAdmin {
		return true
	}
	if len(codes) == 0 {
		return true
	}
	for _, code := range codes {
		if !p.HasPermission(code) {
			return false
		}
	}
	return true
}

// HasAnyPermission reports whether the principal holds at least one code in codes.
func (p *Principal) HasAnyPermission(codes ...string) bool {
	if p == nil {
		return false
	}
	if p.IsSuperAdmin {
		return true
	}
	for _, code := range codes {
		if p.HasPermission(code) {
			return true
		}
	}
	return false
}

// PrincipalLoader fetches the current Principal from authoritative storage for each request.
type PrincipalLoader func(ctx context.Context, adminID string) (*Principal, error)

var (
	principalLoaderMu sync.RWMutex
	principalLoader   PrincipalLoader = LoadPrincipal
)

// SetPrincipalLoaderForTest replaces the loader used by AuthMiddleware() in unit tests
// and returns a restore function. Production code always uses the live DB LoadPrincipal.
func SetPrincipalLoaderForTest(loader PrincipalLoader) func() {
	principalLoaderMu.Lock()
	prev := principalLoader
	if loader == nil {
		principalLoader = LoadPrincipal
	} else {
		principalLoader = loader
	}
	principalLoaderMu.Unlock()
	return func() {
		principalLoaderMu.Lock()
		principalLoader = prev
		principalLoaderMu.Unlock()
	}
}

func currentPrincipalLoader() PrincipalLoader {
	principalLoaderMu.RLock()
	loader := principalLoader
	principalLoaderMu.RUnlock()
	return loader
}

// LoadPrincipal performs a direct, zero-cache primary-key lookup in PostgreSQL
// for the admin, its role, and its role_permissions.
func LoadPrincipal(ctx context.Context, adminID string) (*Principal, error) {
	adminID = strings.TrimSpace(adminID)
	if adminID == "" {
		return nil, ErrAccountInactive
	}
	if db.DB == nil {
		return nil, errors.New("database is not initialized")
	}
	return loadPrincipalFromQuerier(ctx, db.DB, adminID, false)
}

type principalQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

func loadPrincipalFromQuerier(ctx context.Context, q principalQuerier, adminID string, forShare bool) (*Principal, error) {
	query := `
		SELECT a.id, a.username, COALESCE(a.nickname, ''), a.status,
		       r.id, r.code, r.name, r.is_super_admin, r.status
		FROM admins a
		JOIN roles r ON r.id = a.role_id
		WHERE a.id = $1`
	if forShare {
		query += ` FOR SHARE OF a, r`
	}

	var (
		id, username, nickname string
		adminStatus            int16
		roleID                 int64
		roleCode, roleName     string
		isSuperAdmin           bool
		roleStatus             int16
	)
	err := q.QueryRow(ctx, query, adminID).Scan(
		&id, &username, &nickname, &adminStatus,
		&roleID, &roleCode, &roleName, &isSuperAdmin, &roleStatus,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrAccountInactive
		}
		return nil, err
	}
	if adminStatus != StatusActive || roleStatus != StatusActive {
		return nil, ErrAccountInactive
	}

	var perms []string
	if !isSuperAdmin {
		rows, err := q.Query(ctx, `SELECT permission_code FROM role_permissions WHERE role_id = $1`, roleID)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var code string
			if err := rows.Scan(&code); err != nil {
				return nil, err
			}
			perms = append(perms, code)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}

	return NewPrincipal(id, username, nickname, roleID, roleCode, roleName, isSuperAdmin, adminStatus, roleStatus, perms), nil
}

// AuthMiddleware verifies the Bearer JWT and performs a real-time, zero-cache DB lookup
// on every request to load the active Principal and effective permissions.
func AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		authenticateWithLoader(c, currentPrincipalLoader())
	}
}

// AuthMiddlewareWithLoader is the explicit-dependency variant of AuthMiddleware.
func AuthMiddlewareWithLoader(loader PrincipalLoader) gin.HandlerFunc {
	if loader == nil {
		loader = LoadPrincipal
	}
	return func(c *gin.Context) {
		authenticateWithLoader(c, loader)
	}
}

func authenticateWithLoader(c *gin.Context, loader PrincipalLoader) {
	authHeader := c.GetHeader("Authorization")
	if authHeader == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Authorization header is required"})
		c.Abort()
		return
	}

	parts := strings.SplitN(authHeader, " ", 2)
	if !(len(parts) == 2 && parts[0] == "Bearer") {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Authorization header must be Bearer token"})
		c.Abort()
		return
	}

	claims, err := ParseToken(parts[1])
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid or expired token"})
		c.Abort()
		return
	}

	principal, err := loader(c.Request.Context(), claims.AdminID)
	if err != nil {
		if errors.Is(err, ErrAccountInactive) {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": ErrAccountInactive.Error(),
				"code":  "account_inactive",
			})
			c.Abort()
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Authentication lookup failed"})
		c.Abort()
		return
	}
	if principal == nil || !principal.IsActive() {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": ErrAccountInactive.Error(),
			"code":  "account_inactive",
		})
		c.Abort()
		return
	}

	c.Set(contextKeyPrincipal, principal)
	c.Set("admin_id", principal.ID)
	c.Set("admin_role", principal.Role)
	c.Next()
}

// PrincipalFromContext retrieves the authenticated Principal stored by AuthMiddleware.
func PrincipalFromContext(c *gin.Context) (*Principal, bool) {
	if c == nil {
		return nil, false
	}
	val, exists := c.Get(contextKeyPrincipal)
	if !exists {
		return nil, false
	}
	p, ok := val.(*Principal)
	return p, ok && p != nil
}

// RequirePermission requires the authenticated principal to hold the specified permission.
func RequirePermission(code string) gin.HandlerFunc {
	return RequireAllPermissions(code)
}

// RequireAllPermissions requires the authenticated principal to hold all specified permissions.
func RequireAllPermissions(codes ...string) gin.HandlerFunc {
	required := append([]string(nil), codes...)
	return func(c *gin.Context) {
		p, ok := PrincipalFromContext(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Authentication required"})
			c.Abort()
			return
		}
		if !p.HasAllPermissions(required...) {
			c.JSON(http.StatusForbidden, gin.H{
				"error": ErrPermissionDenied.Error(),
				"code":  "permission_denied",
			})
			c.Abort()
			return
		}
		c.Next()
	}
}

// RequireAnyPermission requires the authenticated principal to hold at least one of the specified permissions.
func RequireAnyPermission(codes ...string) gin.HandlerFunc {
	required := append([]string(nil), codes...)
	return func(c *gin.Context) {
		p, ok := PrincipalFromContext(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Authentication required"})
			c.Abort()
			return
		}
		if !p.HasAnyPermission(required...) {
			c.JSON(http.StatusForbidden, gin.H{
				"error": ErrPermissionDenied.Error(),
				"code":  "permission_denied",
			})
			c.Abort()
			return
		}
		c.Next()
	}
}

// RequireSuperAdmin strictly requires the authenticated principal to have IsSuperAdmin == true.
func RequireSuperAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		p, ok := PrincipalFromContext(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Authentication required"})
			c.Abort()
			return
		}
		if !p.IsSuperAdmin {
			c.JSON(http.StatusForbidden, gin.H{
				"error": ErrSuperAdminRequired.Error(),
				"code":  "super_admin_required",
			})
			c.Abort()
			return
		}
		c.Next()
	}
}
