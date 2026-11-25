package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

type stubAdminRepo struct {
	admins     map[string]*Admin
	principals map[string]*Principal
	createErr  error
	updateErr  error
	deleteErr  error
	lastCreate CreateAdminInput
	lastUpdate UpdateAdminInput
}

func (r *stubAdminRepo) CreateAdminTx(_ context.Context, _, _ string, input CreateAdminInput) (*Admin, error) {
	r.lastCreate = input
	if r.createErr != nil {
		return nil, r.createErr
	}
	a := &Admin{
		ID:        input.ID,
		Username:  input.Username,
		Nickname:  input.Nickname,
		RoleID:    input.RoleID,
		Role:      "Leader",
		RoleName:  "负责人",
		Status:    StatusActive,
		CreatedAt: time.Now(),
	}
	return a, nil
}

func (r *stubAdminRepo) GetAdminByUsername(_ context.Context, username string) (*Admin, error) {
	for _, a := range r.admins {
		if a.Username == username {
			return a, nil
		}
	}
	return nil, nil
}

func (r *stubAdminRepo) GetAdminByID(_ context.Context, id string) (*Admin, error) {
	return r.admins[id], nil
}

func (r *stubAdminRepo) GetPrincipalByID(_ context.Context, id string) (*Principal, error) {
	p, ok := r.principals[id]
	if !ok {
		return nil, ErrAccountInactive
	}
	return p, nil
}

func (r *stubAdminRepo) ListAdmins(_ context.Context) ([]Admin, error) {
	out := make([]Admin, 0, len(r.admins))
	for _, a := range r.admins {
		out = append(out, *a)
	}
	return out, nil
}

func (r *stubAdminRepo) UpdateAdminTx(_ context.Context, actorID, _, targetID string, input UpdateAdminInput) (*Admin, error) {
	r.lastUpdate = input
	if r.updateErr != nil {
		return nil, r.updateErr
	}
	if actorID == targetID && input.Status != nil && *input.Status != StatusActive {
		return nil, ErrSelfModifyForbidden
	}
	a := r.admins[targetID]
	if a == nil {
		return nil, ErrAdminNotFound
	}
	cp := *a
	if input.Nickname != nil {
		cp.Nickname = *input.Nickname
	}
	if input.Status != nil {
		cp.Status = *input.Status
	}
	return &cp, nil
}

func (r *stubAdminRepo) DeleteAdminTx(_ context.Context, actorID, _, targetID string) error {
	if actorID == targetID {
		return ErrSelfModifyForbidden
	}
	if r.deleteErr != nil {
		return r.deleteErr
	}
	return nil
}

func (r *stubAdminRepo) AdminExists(_ context.Context, id string) (bool, error) {
	_, ok := r.admins[id]
	return ok, nil
}

type stubRoleRepo struct {
	roles      []Role
	createErr  error
	updateErr  error
	deleteErr  error
	lastCreate CreateRoleInput
	lastUpdate UpdateRoleInput
}

func (r *stubRoleRepo) ListRoles(_ context.Context) ([]Role, error) {
	return r.roles, nil
}

func (r *stubRoleRepo) GetRoleByID(_ context.Context, id int64) (*Role, error) {
	for i := range r.roles {
		if r.roles[i].ID == id {
			cp := r.roles[i]
			return &cp, nil
		}
	}
	return nil, nil
}

func (r *stubRoleRepo) CreateRoleTx(_ context.Context, _, _ string, input CreateRoleInput) (*Role, error) {
	r.lastCreate = input
	if r.createErr != nil {
		return nil, r.createErr
	}
	return &Role{
		ID:          10,
		Code:        input.Code,
		Name:        input.Name,
		Description: input.Description,
		Status:      input.Status,
		Permissions: input.Permissions,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}, nil
}

func (r *stubRoleRepo) UpdateRoleTx(_ context.Context, _, _ string, id int64, input UpdateRoleInput) (*Role, error) {
	r.lastUpdate = input
	if r.updateErr != nil {
		return nil, r.updateErr
	}
	name := "Custom"
	if input.Name != nil {
		name = *input.Name
	}
	desc := ""
	if input.Description != nil {
		desc = *input.Description
	}
	perms := []string{PermDashboardRead}
	if input.Permissions != nil {
		perms = *input.Permissions
	}
	return &Role{
		ID:          id,
		Code:        "Custom",
		Name:        name,
		Description: desc,
		Status:      StatusActive,
		Permissions: perms,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}, nil
}

func (r *stubRoleRepo) DeleteRoleTx(_ context.Context, _, _ string, _ int64) error {
	return r.deleteErr
}

func TestAuthAndRoleEndpointsAuthorizationAndValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupTestJWTConfig(t)

	hash, err := bcrypt.GenerateFromPassword([]byte("ValidPass#2026"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}

	superP := NewPrincipal("super-1", "root", "管理员", 1, "Admin", "管理员", true, StatusActive, StatusActive, nil)
	acctReaderP := NewPrincipal("reader-1", "auditor", "审计", 6, "Auditor", "审计", false, StatusActive, StatusActive, []string{PermSystemAccountsRead})
	roleReaderP := NewPrincipal("rolereader-1", "roleviewer", "角色只读", 7, "RoleViewer", "角色只读", false, StatusActive, StatusActive, []string{PermSystemRolesRead})

	adminRepo := &stubAdminRepo{
		admins: map[string]*Admin{
			"super-1": {
				ID: "super-1", Username: "root", PasswordHash: string(hash), Nickname: "管理员",
				RoleID: 1, Role: "Admin", RoleName: "管理员", IsSuperAdmin: true, Status: StatusActive, RoleStatus: StatusActive,
			},
			"disabled-1": {
				ID: "disabled-1", Username: "inactive", PasswordHash: string(hash), Nickname: "禁用",
				RoleID: 2, Role: "Leader", RoleName: "负责人", Status: StatusDisabled, RoleStatus: StatusActive,
			},
		},
		principals: map[string]*Principal{
			"super-1":      superP,
			"reader-1":     acctReaderP,
			"rolereader-1": roleReaderP,
		},
	}
	roleRepo := &stubRoleRepo{
		roles: []Role{
			{ID: 1, Code: "Admin", Name: "管理员", IsSystem: true, IsSuperAdmin: true, Status: StatusActive, Permissions: AllPermissionCodes()},
		},
	}

	restore := SetPrincipalLoaderForTest(func(_ context.Context, adminID string) (*Principal, error) {
		p, ok := adminRepo.principals[adminID]
		if !ok {
			return nil, ErrAccountInactive
		}
		return p, nil
	})
	t.Cleanup(restore)

	router := gin.New()
	api := router.Group("/api/v1/admin")
	NewAuthHandler(NewAdminService(adminRepo)).RegisterRoutes(api)
	NewRoleHandler(NewRoleService(roleRepo)).RegisterRoutes(api)

	tokSuper, _ := GenerateToken("super-1", "Admin")
	tokAcctReader, _ := GenerateToken("reader-1", "Auditor")
	tokRoleReader, _ := GenerateToken("rolereader-1", "RoleViewer")

	doJSON := func(method, path, token string, payload any) *httptest.ResponseRecorder {
		var body []byte
		if payload != nil {
			body, _ = json.Marshal(payload)
		}
		req := httptest.NewRequest(method, "/api/v1/admin"+path, bytes.NewReader(body))
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}

	// 1. Unauthenticated /auth/register MUST return 401.
	if w := doJSON(http.MethodPost, "/auth/register", "", map[string]any{
		"username": "eviluser", "password": "Password123!", "nickname": "Evil", "role_id": 1,
	}); w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated /auth/register status = %d, want 401", w.Code)
	}

	// 2. Non-super-admin cannot create admins or roles (403).
	if w := doJSON(http.MethodPost, "/auth/admins", tokAcctReader, map[string]any{
		"username": "newuser", "password": "Password123!", "nickname": "New", "role_id": 2,
	}); w.Code != http.StatusForbidden {
		t.Fatalf("non-super POST /auth/admins status = %d, want 403", w.Code)
	}
	if w := doJSON(http.MethodPost, "/auth/roles", tokRoleReader, map[string]any{
		"code": "CustomRole", "name": "自定义", "permissions": []string{PermDashboardRead},
	}); w.Code != http.StatusForbidden {
		t.Fatalf("non-super POST /auth/roles status = %d, want 403", w.Code)
	}

	// 3. Login returns full Principal payload; disabled account returns 403.
	wLogin := doJSON(http.MethodPost, "/auth/login", "", map[string]any{
		"username": "root", "password": "ValidPass#2026",
	})
	if wLogin.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", wLogin.Code, wLogin.Body.String())
	}
	var loginResp struct {
		Token string    `json:"token"`
		Admin Principal `json:"admin"`
	}
	if err := json.Unmarshal(wLogin.Body.Bytes(), &loginResp); err != nil {
		t.Fatal(err)
	}
	if loginResp.Token == "" || !loginResp.Admin.IsSuperAdmin || loginResp.Admin.RoleID != 1 || len(loginResp.Admin.Permissions) != 39 {
		t.Fatalf("unexpected login response: %+v", loginResp)
	}
	if w := doJSON(http.MethodPost, "/auth/login", "", map[string]any{
		"username": "inactive", "password": "ValidPass#2026",
	}); w.Code != http.StatusForbidden {
		t.Fatalf("disabled login status = %d, want 403", w.Code)
	}

	// 4. GET /auth/me returns full live Principal.
	wMe := doJSON(http.MethodGet, "/auth/me", tokAcctReader, nil)
	if wMe.Code != http.StatusOK {
		t.Fatalf("GET /auth/me status = %d", wMe.Code)
	}
	var meResp Principal
	if err := json.Unmarshal(wMe.Body.Bytes(), &meResp); err != nil {
		t.Fatal(err)
	}
	if meResp.Role != "Auditor" || meResp.RoleName != "审计" || meResp.IsSuperAdmin {
		t.Fatalf("unexpected /auth/me response: %+v", meResp)
	}

	// 5. GET /auth/permissions and GET /auth/roles allow either system.roles:read or system.accounts:read.
	if w := doJSON(http.MethodGet, "/auth/permissions", tokAcctReader, nil); w.Code != http.StatusOK {
		t.Fatalf("GET /auth/permissions status = %d, want 200", w.Code)
	}
	if w := doJSON(http.MethodGet, "/auth/roles", tokAcctReader, nil); w.Code != http.StatusOK {
		t.Fatalf("GET /auth/roles status = %d, want 200", w.Code)
	}
	// GET /auth/admins requires system.accounts:read (role-only reader gets 403).
	if w := doJSON(http.MethodGet, "/auth/admins", tokRoleReader, nil); w.Code != http.StatusForbidden {
		t.Fatalf("GET /auth/admins for role-only reader status = %d, want 403", w.Code)
	}

	// 6. Creating non-super-admin role with super_admin_only permission is rejected (400).
	if w := doJSON(http.MethodPost, "/auth/roles", tokSuper, map[string]any{
		"code":        "BadRole",
		"name":        "越权角色",
		"permissions": []string{PermDashboardRead, PermSystemAccountsWrite},
	}); w.Code != http.StatusBadRequest {
		t.Fatalf("super_admin_only permission on custom role status = %d, want 400, body = %s", w.Code, w.Body.String())
	}

	// 7. Creating custom role with :write auto-adds :read.
	wCreateRole := doJSON(http.MethodPost, "/auth/roles", tokSuper, map[string]any{
		"code":        "EditorPlus",
		"name":        "高级编辑",
		"permissions": []string{PermContentNovelsWrite},
	})
	if wCreateRole.Code != http.StatusCreated {
		t.Fatalf("POST /auth/roles status = %d, body = %s", wCreateRole.Code, wCreateRole.Body.String())
	}
	if len(roleRepo.lastCreate.Permissions) != 2 || roleRepo.lastCreate.Permissions[0] != PermContentNovelsRead || roleRepo.lastCreate.Permissions[1] != PermContentNovelsWrite {
		t.Fatalf("write->read normalization failed: %v", roleRepo.lastCreate.Permissions)
	}

	// 8. Partial PUT /auth/roles/:id omitting permissions preserves them (input.Permissions == nil);
	// explicit empty [] clears them (input.Permissions != nil && len == 0).
	if w := doJSON(http.MethodPut, "/auth/roles/10", tokSuper, map[string]any{
		"status": StatusDisabled,
	}); w.Code != http.StatusOK {
		t.Fatalf("partial role status update status = %d, body = %s", w.Code, w.Body.String())
	}
	if roleRepo.lastUpdate.Permissions != nil {
		t.Fatalf("omitted permissions should stay nil to preserve existing permissions, got %v", *roleRepo.lastUpdate.Permissions)
	}
	if w := doJSON(http.MethodPut, "/auth/roles/10", tokSuper, map[string]any{
		"permissions": []string{},
	}); w.Code != http.StatusOK {
		t.Fatalf("explicit empty permissions update status = %d, body = %s", w.Code, w.Body.String())
	}
	if roleRepo.lastUpdate.Permissions == nil || len(*roleRepo.lastUpdate.Permissions) != 0 {
		t.Fatalf("explicit empty permissions should pass non-nil empty slice, got %v", roleRepo.lastUpdate.Permissions)
	}

	// 9. Updating super admin role returns 400; deleting system role returns 400; deleting in-use role returns 409.
	roleRepo.updateErr = ErrSystemRoleImmutable
	if w := doJSON(http.MethodPut, "/auth/roles/1", tokSuper, map[string]any{
		"name": "管理员", "permissions": []string{PermDashboardRead},
	}); w.Code != http.StatusBadRequest {
		t.Fatalf("updating super admin role status = %d, want 400", w.Code)
	}
	roleRepo.updateErr = nil

	roleRepo.deleteErr = ErrSystemRoleDeleteForbidden
	if w := doJSON(http.MethodDelete, "/auth/roles/2", tokSuper, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("deleting system role status = %d, want 400", w.Code)
	}
	roleRepo.deleteErr = ErrRoleInUse
	if w := doJSON(http.MethodDelete, "/auth/roles/10", tokSuper, nil); w.Code != http.StatusConflict {
		t.Fatalf("deleting in-use role status = %d, want 409", w.Code)
	}
	roleRepo.deleteErr = nil

	// 9. Self-delete and self-disable are forbidden (400).
	if w := doJSON(http.MethodDelete, "/auth/admins/super-1", tokSuper, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("self-delete status = %d, want 400", w.Code)
	}
	disabled := StatusDisabled
	if w := doJSON(http.MethodPut, "/auth/admins/super-1", tokSuper, map[string]any{
		"status": disabled,
	}); w.Code != http.StatusBadRequest {
		t.Fatalf("self-disable status = %d, want 400", w.Code)
	}

	// 10. Deleting last active super admin returns 400.
	adminRepo.deleteErr = ErrLastSuperAdmin
	if w := doJSON(http.MethodDelete, "/auth/admins/other-super", tokSuper, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("last super admin delete status = %d, want 400", w.Code)
	}
}
