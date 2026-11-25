package auth

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"star-novel-cms/internal/db"
)

type Role struct {
	ID           int64     `json:"id"`
	Code         string    `json:"code"`
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	IsSystem     bool      `json:"is_system"`
	IsSuperAdmin bool      `json:"is_super_admin"`
	Status       int16     `json:"status"`
	AdminCount   int       `json:"admin_count"`
	Permissions  []string  `json:"permissions"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type CreateRoleInput struct {
	Code        string
	Name        string
	Description string
	Status      int16
	Permissions []string
}

type UpdateRoleInput struct {
	Name        *string
	Description *string
	Status      *int16
	Permissions *[]string
}

type RoleRepository interface {
	ListRoles(ctx context.Context) ([]Role, error)
	GetRoleByID(ctx context.Context, id int64) (*Role, error)
	CreateRoleTx(ctx context.Context, actorID, actorIP string, input CreateRoleInput) (*Role, error)
	UpdateRoleTx(ctx context.Context, actorID, actorIP string, id int64, input UpdateRoleInput) (*Role, error)
	DeleteRoleTx(ctx context.Context, actorID, actorIP string, id int64) error
}

type dbRoleRepository struct{}

func NewRoleRepository() RoleRepository {
	return &dbRoleRepository{}
}

func roleAuditSnapshot(r *Role) map[string]any {
	if r == nil {
		return nil
	}
	return map[string]any{
		"id":             r.ID,
		"code":           r.Code,
		"name":           r.Name,
		"description":    r.Description,
		"is_system":      r.IsSystem,
		"is_super_admin": r.IsSuperAdmin,
		"status":         r.Status,
		"permissions":    r.Permissions,
	}
}

func loadRolePermissionsTx(ctx context.Context, q principalQuerier, roleID int64) ([]string, error) {
	rows, err := q.Query(ctx, `SELECT permission_code FROM role_permissions WHERE role_id = $1`, roleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	perms := make([]string, 0)
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
	return normalizeKnownPermissions(perms), nil
}

func (r *dbRoleRepository) ListRoles(ctx context.Context) ([]Role, error) {
	rows, err := db.DB.Query(ctx, `
		SELECT r.id, r.code, r.name, r.description, r.is_system, r.is_super_admin, r.status,
		       COUNT(a.id)::int AS admin_count, r.created_at, r.updated_at
		FROM roles r
		LEFT JOIN admins a ON a.role_id = r.id
		GROUP BY r.id
		ORDER BY r.is_super_admin DESC, r.is_system DESC, r.id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	roles := make([]Role, 0)
	roleIdx := make(map[int64]int)
	for rows.Next() {
		var item Role
		if err := rows.Scan(
			&item.ID, &item.Code, &item.Name, &item.Description,
			&item.IsSystem, &item.IsSuperAdmin, &item.Status,
			&item.AdminCount, &item.CreatedAt, &item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		if item.IsSuperAdmin {
			item.Permissions = AllPermissionCodes()
		} else {
			item.Permissions = make([]string, 0)
		}
		roleIdx[item.ID] = len(roles)
		roles = append(roles, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	permRows, err := db.DB.Query(ctx, `SELECT role_id, permission_code FROM role_permissions`)
	if err != nil {
		return nil, err
	}
	defer permRows.Close()

	rawPerms := make(map[int64][]string)
	for permRows.Next() {
		var roleID int64
		var code string
		if err := permRows.Scan(&roleID, &code); err != nil {
			return nil, err
		}
		rawPerms[roleID] = append(rawPerms[roleID], code)
	}
	if err := permRows.Err(); err != nil {
		return nil, err
	}

	for roleID, idx := range roleIdx {
		if roles[idx].IsSuperAdmin {
			continue
		}
		roles[idx].Permissions = normalizeKnownPermissions(rawPerms[roleID])
	}
	return roles, nil
}

func (r *dbRoleRepository) GetRoleByID(ctx context.Context, id int64) (*Role, error) {
	var item Role
	err := db.DB.QueryRow(ctx, `
		SELECT r.id, r.code, r.name, r.description, r.is_system, r.is_super_admin, r.status,
		       (SELECT COUNT(*)::int FROM admins a WHERE a.role_id = r.id),
		       r.created_at, r.updated_at
		FROM roles r
		WHERE r.id = $1
	`, id).Scan(
		&item.ID, &item.Code, &item.Name, &item.Description,
		&item.IsSystem, &item.IsSuperAdmin, &item.Status,
		&item.AdminCount, &item.CreatedAt, &item.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if item.IsSuperAdmin {
		item.Permissions = AllPermissionCodes()
	} else {
		perms, err := loadRolePermissionsTx(ctx, db.DB, item.ID)
		if err != nil {
			return nil, err
		}
		item.Permissions = perms
	}
	return &item, nil
}

func (r *dbRoleRepository) CreateRoleTx(ctx context.Context, actorID, actorIP string, input CreateRoleInput) (*Role, error) {
	tx, err := db.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, superAdminMutationLockKey); err != nil {
		return nil, err
	}
	if err := RequireSuperAdminTx(ctx, tx, actorID); err != nil {
		return nil, err
	}

	var created Role
	err = tx.QueryRow(ctx, `
		INSERT INTO roles (code, name, description, is_system, is_super_admin, status)
		VALUES ($1, $2, $3, FALSE, FALSE, $4)
		RETURNING id, code, name, description, is_system, is_super_admin, status, created_at, updated_at
	`, input.Code, input.Name, input.Description, input.Status).Scan(
		&created.ID, &created.Code, &created.Name, &created.Description,
		&created.IsSystem, &created.IsSuperAdmin, &created.Status,
		&created.CreatedAt, &created.UpdatedAt,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrRoleCodeExists
		}
		return nil, err
	}

	for _, code := range input.Permissions {
		if _, err := tx.Exec(ctx, `
			INSERT INTO role_permissions (role_id, permission_code)
			VALUES ($1, $2)
			ON CONFLICT (role_id, permission_code) DO NOTHING
		`, created.ID, code); err != nil {
			return nil, err
		}
	}
	created.Permissions = append([]string{}, input.Permissions...)
	if created.Permissions == nil {
		created.Permissions = make([]string, 0)
	}

	if err := writeAdminAuditLog(ctx, tx, actorID, "create_role", strconv.FormatInt(created.ID, 10), nil, roleAuditSnapshot(&created), actorIP); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &created, nil
}

func (r *dbRoleRepository) UpdateRoleTx(ctx context.Context, actorID, actorIP string, id int64, input UpdateRoleInput) (*Role, error) {
	tx, err := db.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, superAdminMutationLockKey); err != nil {
		return nil, err
	}
	if err := RequireSuperAdminTx(ctx, tx, actorID); err != nil {
		return nil, err
	}

	var before Role
	err = tx.QueryRow(ctx, `
		SELECT id, code, name, description, is_system, is_super_admin, status, created_at, updated_at
		FROM roles
		WHERE id = $1
		FOR UPDATE
	`, id).Scan(
		&before.ID, &before.Code, &before.Name, &before.Description,
		&before.IsSystem, &before.IsSuperAdmin, &before.Status,
		&before.CreatedAt, &before.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrRoleNotFound
		}
		return nil, err
	}
	if before.IsSuperAdmin {
		return nil, ErrSystemRoleImmutable
	}

	beforePerms, err := loadRolePermissionsTx(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	before.Permissions = beforePerms

	newName := before.Name
	if input.Name != nil {
		newName = *input.Name
	}
	newDesc := before.Description
	if input.Description != nil {
		newDesc = *input.Description
	}
	newStatus := before.Status
	if input.Status != nil {
		newStatus = *input.Status
	}

	var updated Role
	err = tx.QueryRow(ctx, `
		UPDATE roles
		SET name = $1, description = $2, status = $3, updated_at = CURRENT_TIMESTAMP
		WHERE id = $4
		RETURNING id, code, name, description, is_system, is_super_admin, status, created_at, updated_at
	`, newName, newDesc, newStatus, id).Scan(
		&updated.ID, &updated.Code, &updated.Name, &updated.Description,
		&updated.IsSystem, &updated.IsSuperAdmin, &updated.Status,
		&updated.CreatedAt, &updated.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	if input.Permissions != nil {
		if _, err := tx.Exec(ctx, `DELETE FROM role_permissions WHERE role_id = $1`, id); err != nil {
			return nil, err
		}
		for _, code := range *input.Permissions {
			if _, err := tx.Exec(ctx, `
				INSERT INTO role_permissions (role_id, permission_code)
				VALUES ($1, $2)
				ON CONFLICT (role_id, permission_code) DO NOTHING
			`, id, code); err != nil {
				return nil, err
			}
		}
		updated.Permissions = append([]string{}, (*input.Permissions)...)
	} else {
		updated.Permissions = append([]string{}, beforePerms...)
	}
	if updated.Permissions == nil {
		updated.Permissions = make([]string, 0)
	}
	if err := tx.QueryRow(ctx, `SELECT COUNT(*)::int FROM admins WHERE role_id = $1`, id).Scan(&updated.AdminCount); err != nil {
		return nil, err
	}

	if err := writeAdminAuditLog(ctx, tx, actorID, "update_role", strconv.FormatInt(id, 10), roleAuditSnapshot(&before), roleAuditSnapshot(&updated), actorIP); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &updated, nil
}

func (r *dbRoleRepository) DeleteRoleTx(ctx context.Context, actorID, actorIP string, id int64) error {
	tx, err := db.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, superAdminMutationLockKey); err != nil {
		return err
	}
	if err := RequireSuperAdminTx(ctx, tx, actorID); err != nil {
		return err
	}

	var before Role
	err = tx.QueryRow(ctx, `
		SELECT id, code, name, description, is_system, is_super_admin, status, created_at, updated_at
		FROM roles
		WHERE id = $1
		FOR UPDATE
	`, id).Scan(
		&before.ID, &before.Code, &before.Name, &before.Description,
		&before.IsSystem, &before.IsSuperAdmin, &before.Status,
		&before.CreatedAt, &before.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrRoleNotFound
		}
		return err
	}
	if before.IsSystem || before.IsSuperAdmin {
		return ErrSystemRoleDeleteForbidden
	}

	var boundAdmins int
	if err := tx.QueryRow(ctx, `SELECT COUNT(*)::int FROM admins WHERE role_id = $1`, id).Scan(&boundAdmins); err != nil {
		return err
	}
	if boundAdmins > 0 {
		return ErrRoleInUse
	}

	beforePerms, err := loadRolePermissionsTx(ctx, tx, id)
	if err != nil {
		return err
	}
	before.Permissions = beforePerms

	if _, err := tx.Exec(ctx, `DELETE FROM roles WHERE id = $1`, id); err != nil {
		return err
	}
	if err := writeAdminAuditLog(ctx, tx, actorID, "delete_role", strconv.FormatInt(id, 10), roleAuditSnapshot(&before), nil, actorIP); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
