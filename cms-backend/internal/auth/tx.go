package auth

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"
)

// LockPrincipalTx locks the admin and its bound role with FOR SHARE OF a, r
// inside an existing transaction, verifies both are active, and loads effective permissions.
func LockPrincipalTx(ctx context.Context, tx pgx.Tx, adminID string) (*Principal, error) {
	adminID = strings.TrimSpace(adminID)
	if adminID == "" {
		return nil, ErrAccountInactive
	}
	return loadPrincipalFromQuerier(ctx, tx, adminID, true)
}

// RequirePermissionTx verifies inside tx (holding FOR SHARE OF a, r) that adminID
// belongs to an active admin with an active role and holds all requiredPerms
// (or is a super admin).
func RequirePermissionTx(ctx context.Context, tx pgx.Tx, adminID string, requiredPerms ...string) error {
	p, err := LockPrincipalTx(ctx, tx, adminID)
	if err != nil {
		return err
	}
	if !p.HasAllPermissions(requiredPerms...) {
		return ErrPermissionDenied
	}
	return nil
}

// RequireAnyPermissionTx verifies inside tx (holding FOR SHARE OF a, r) that adminID
// belongs to an active admin with an active role and holds at least one of requiredPerms
// (or is a super admin).
func RequireAnyPermissionTx(ctx context.Context, tx pgx.Tx, adminID string, requiredPerms ...string) error {
	p, err := LockPrincipalTx(ctx, tx, adminID)
	if err != nil {
		return err
	}
	if !p.HasAnyPermission(requiredPerms...) {
		return ErrPermissionDenied
	}
	return nil
}

// RequireSuperAdminTx verifies inside tx (holding FOR SHARE OF a, r) that adminID
// belongs to an active admin with an active super-admin role.
func RequireSuperAdminTx(ctx context.Context, tx pgx.Tx, adminID string) error {
	p, err := LockPrincipalTx(ctx, tx, adminID)
	if err != nil {
		return err
	}
	if !p.IsSuperAdmin {
		return ErrSuperAdminRequired
	}
	return nil
}
