package auth

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"star-novel-cms/internal/db"
)

const superAdminMutationLockKey int64 = 2026111935

type Admin struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"-"`
	Nickname     string    `json:"nickname"`
	RoleID       int64     `json:"role_id"`
	Role         string    `json:"role"`
	RoleName     string    `json:"role_name"`
	IsSuperAdmin bool      `json:"is_super_admin"`
	Status       int16     `json:"status"`
	RoleStatus   int16     `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
}

type CreateAdminInput struct {
	ID           string
	Username     string
	PasswordHash string
	Nickname     string
	RoleID       int64
	RoleCode     string
	Status       int16
}

type UpdateAdminInput struct {
	Nickname     *string
	RoleID       *int64
	RoleCode     *string
	Status       *int16
	PasswordHash string
}

type AdminRepository interface {
	CreateAdminTx(ctx context.Context, actorID, actorIP string, input CreateAdminInput) (*Admin, error)
	GetAdminByUsername(ctx context.Context, username string) (*Admin, error)
	GetAdminByID(ctx context.Context, id string) (*Admin, error)
	GetPrincipalByID(ctx context.Context, id string) (*Principal, error)
	ListAdmins(ctx context.Context) ([]Admin, error)
	UpdateAdminTx(ctx context.Context, actorID, actorIP, targetID string, input UpdateAdminInput) (*Admin, error)
	DeleteAdminTx(ctx context.Context, actorID, actorIP, targetID string) error
	AdminExists(ctx context.Context, id string) (bool, error)
}

type dbAdminRepository struct{}

func NewAdminRepository() AdminRepository {
	return &dbAdminRepository{}
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func writeAdminAuditLog(ctx context.Context, tx pgx.Tx, actorID, action, targetID string, beforeData, afterData any, ipAddress string) error {
	var beforeJSON, afterJSON *string
	if beforeData != nil {
		b, err := json.Marshal(beforeData)
		if err != nil {
			return err
		}
		s := string(b)
		beforeJSON = &s
	}
	if afterData != nil {
		b, err := json.Marshal(afterData)
		if err != nil {
			return err
		}
		s := string(b)
		afterJSON = &s
	}
	var ipPtr *string
	if ip := strings.TrimSpace(ipAddress); ip != "" {
		ipPtr = &ip
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO admin_audit_logs (admin_id, action, target_id, before_data, after_data, ip_address)
		VALUES ($1, $2, $3, $4::jsonb, $5::jsonb, $6)
	`, actorID, action, targetID, beforeJSON, afterJSON, ipPtr)
	return err
}

func adminAuditSnapshot(a *Admin) map[string]any {
	if a == nil {
		return nil
	}
	return map[string]any{
		"id":             a.ID,
		"username":       a.Username,
		"nickname":       a.Nickname,
		"role_id":        a.RoleID,
		"role":           a.Role,
		"role_name":      a.RoleName,
		"is_super_admin": a.IsSuperAdmin,
		"status":         a.Status,
	}
}

func resolveRoleForShareTx(ctx context.Context, tx pgx.Tx, roleID int64, roleCode string) (*Role, error) {
	var r Role
	var err error
	if roleID > 0 {
		err = tx.QueryRow(ctx, `
			SELECT id, code, name, description, is_system, is_super_admin, status, created_at, updated_at
			FROM roles
			WHERE id = $1
			FOR SHARE
		`, roleID).Scan(&r.ID, &r.Code, &r.Name, &r.Description, &r.IsSystem, &r.IsSuperAdmin, &r.Status, &r.CreatedAt, &r.UpdatedAt)
	} else {
		roleCode = strings.TrimSpace(roleCode)
		if roleCode == "" {
			return nil, ErrRoleNotFound
		}
		err = tx.QueryRow(ctx, `
			SELECT id, code, name, description, is_system, is_super_admin, status, created_at, updated_at
			FROM roles
			WHERE code = $1
			FOR SHARE
		`, roleCode).Scan(&r.ID, &r.Code, &r.Name, &r.Description, &r.IsSystem, &r.IsSuperAdmin, &r.Status, &r.CreatedAt, &r.UpdatedAt)
	}
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrRoleNotFound
		}
		return nil, err
	}
	return &r, nil
}

func countActiveSuperAdminsForUpdateTx(ctx context.Context, tx pgx.Tx) (int, error) {
	rows, err := tx.Query(ctx, `
		SELECT a.id
		FROM admins a
		JOIN roles r ON r.id = a.role_id
		WHERE r.is_super_admin = TRUE
		  AND a.status = 1
		  AND r.status = 1
		FOR UPDATE OF a
	`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return 0, err
		}
		count++
	}
	return count, rows.Err()
}

func (r *dbAdminRepository) CreateAdminTx(ctx context.Context, actorID, actorIP string, input CreateAdminInput) (*Admin, error) {
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

	role, err := resolveRoleForShareTx(ctx, tx, input.RoleID, input.RoleCode)
	if err != nil {
		return nil, err
	}
	if role.Status != StatusActive {
		return nil, ErrRoleInactive
	}

	status := input.Status
	if status == 0 {
		status = StatusActive
	}
	var a Admin
	err = tx.QueryRow(ctx, `
		INSERT INTO admins (id, username, password_hash, nickname, role, role_id, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, username, COALESCE(nickname, ''), role_id, role, status, created_at
	`, input.ID, input.Username, input.PasswordHash, input.Nickname, role.Code, role.ID, status).Scan(
		&a.ID, &a.Username, &a.Nickname, &a.RoleID, &a.Role, &a.Status, &a.CreatedAt,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrUsernameExists
		}
		return nil, err
	}
	a.RoleName = role.Name
	a.IsSuperAdmin = role.IsSuperAdmin
	a.RoleStatus = role.Status

	if err := writeAdminAuditLog(ctx, tx, actorID, "create_admin", a.ID, nil, adminAuditSnapshot(&a), actorIP); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &a, nil
}

func (r *dbAdminRepository) GetAdminByUsername(ctx context.Context, username string) (*Admin, error) {
	query := `
		SELECT a.id, a.username, a.password_hash, COALESCE(a.nickname, ''),
		       a.role_id, r.code, r.name, r.is_super_admin, a.status, r.status, a.created_at
		FROM admins a
		JOIN roles r ON r.id = a.role_id
		WHERE a.username = $1`
	var a Admin
	err := db.DB.QueryRow(ctx, query, username).Scan(
		&a.ID, &a.Username, &a.PasswordHash, &a.Nickname,
		&a.RoleID, &a.Role, &a.RoleName, &a.IsSuperAdmin, &a.Status, &a.RoleStatus, &a.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &a, nil
}

func (r *dbAdminRepository) GetAdminByID(ctx context.Context, id string) (*Admin, error) {
	query := `
		SELECT a.id, a.username, a.password_hash, COALESCE(a.nickname, ''),
		       a.role_id, r.code, r.name, r.is_super_admin, a.status, r.status, a.created_at
		FROM admins a
		JOIN roles r ON r.id = a.role_id
		WHERE a.id = $1`
	var a Admin
	err := db.DB.QueryRow(ctx, query, id).Scan(
		&a.ID, &a.Username, &a.PasswordHash, &a.Nickname,
		&a.RoleID, &a.Role, &a.RoleName, &a.IsSuperAdmin, &a.Status, &a.RoleStatus, &a.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &a, nil
}

func (r *dbAdminRepository) GetPrincipalByID(ctx context.Context, id string) (*Principal, error) {
	return LoadPrincipal(ctx, id)
}

func (r *dbAdminRepository) ListAdmins(ctx context.Context) ([]Admin, error) {
	query := `
		SELECT a.id, a.username, COALESCE(a.nickname, ''),
		       a.role_id, r.code, r.name, r.is_super_admin, a.status, r.status, a.created_at
		FROM admins a
		JOIN roles r ON r.id = a.role_id
		ORDER BY a.created_at DESC, a.id ASC`
	rows, err := db.DB.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	admins := make([]Admin, 0)
	for rows.Next() {
		var a Admin
		if err := rows.Scan(
			&a.ID, &a.Username, &a.Nickname,
			&a.RoleID, &a.Role, &a.RoleName, &a.IsSuperAdmin, &a.Status, &a.RoleStatus, &a.CreatedAt,
		); err != nil {
			return nil, err
		}
		admins = append(admins, a)
	}
	return admins, rows.Err()
}

func (r *dbAdminRepository) UpdateAdminTx(ctx context.Context, actorID, actorIP, targetID string, input UpdateAdminInput) (*Admin, error) {
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

	var before Admin
	err = tx.QueryRow(ctx, `
		SELECT a.id, a.username, COALESCE(a.nickname, ''),
		       a.role_id, r.code, r.name, r.is_super_admin, a.status, r.status, a.created_at
		FROM admins a
		JOIN roles r ON r.id = a.role_id
		WHERE a.id = $1
		FOR UPDATE OF a
	`, targetID).Scan(
		&before.ID, &before.Username, &before.Nickname,
		&before.RoleID, &before.Role, &before.RoleName, &before.IsSuperAdmin, &before.Status, &before.RoleStatus, &before.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrAdminNotFound
		}
		return nil, err
	}

	newNickname := before.Nickname
	if input.Nickname != nil {
		newNickname = strings.TrimSpace(*input.Nickname)
	}
	newStatus := before.Status
	if input.Status != nil {
		newStatus = *input.Status
	}

	newRole := &Role{
		ID:           before.RoleID,
		Code:         before.Role,
		Name:         before.RoleName,
		IsSuperAdmin: before.IsSuperAdmin,
		Status:       before.RoleStatus,
	}
	roleChanged := false
	if input.RoleID != nil && *input.RoleID > 0 {
		roleChanged = *input.RoleID != before.RoleID
		resolved, err := resolveRoleForShareTx(ctx, tx, *input.RoleID, "")
		if err != nil {
			return nil, err
		}
		newRole = resolved
	} else if input.RoleCode != nil && strings.TrimSpace(*input.RoleCode) != "" {
		resolved, err := resolveRoleForShareTx(ctx, tx, 0, *input.RoleCode)
		if err != nil {
			return nil, err
		}
		roleChanged = resolved.ID != before.RoleID
		newRole = resolved
	}
	if roleChanged && newRole.Status != StatusActive {
		return nil, ErrRoleInactive
	}

	// Self-protection: never allow disabling or demoting the current logged-in admin.
	if actorID == targetID {
		if newStatus != StatusActive {
			return nil, ErrSelfModifyForbidden
		}
		if before.IsSuperAdmin && !newRole.IsSuperAdmin {
			return nil, ErrSelfModifyForbidden
		}
	}

	// Last active super admin protection.
	wasActiveSuper := before.Status == StatusActive && before.IsSuperAdmin && before.RoleStatus == StatusActive
	willBeActiveSuper := newStatus == StatusActive && newRole.IsSuperAdmin && newRole.Status == StatusActive
	if wasActiveSuper && !willBeActiveSuper {
		activeSupers, err := countActiveSuperAdminsForUpdateTx(ctx, tx)
		if err != nil {
			return nil, err
		}
		if activeSupers <= 1 {
			return nil, ErrLastSuperAdmin
		}
	}

	if input.PasswordHash != "" {
		_, err = tx.Exec(ctx, `
			UPDATE admins
			SET nickname = $1, role = $2, role_id = $3, status = $4, password_hash = $5
			WHERE id = $6
		`, newNickname, newRole.Code, newRole.ID, newStatus, input.PasswordHash, targetID)
	} else {
		_, err = tx.Exec(ctx, `
			UPDATE admins
			SET nickname = $1, role = $2, role_id = $3, status = $4
			WHERE id = $5
		`, newNickname, newRole.Code, newRole.ID, newStatus, targetID)
	}
	if err != nil {
		return nil, err
	}

	after := Admin{
		ID:           before.ID,
		Username:     before.Username,
		Nickname:     newNickname,
		RoleID:       newRole.ID,
		Role:         newRole.Code,
		RoleName:     newRole.Name,
		IsSuperAdmin: newRole.IsSuperAdmin,
		Status:       newStatus,
		RoleStatus:   newRole.Status,
		CreatedAt:    before.CreatedAt,
	}
	afterSnap := adminAuditSnapshot(&after)
	if input.PasswordHash != "" {
		afterSnap["password_updated"] = true
	}

	if err := writeAdminAuditLog(ctx, tx, actorID, "update_admin", targetID, adminAuditSnapshot(&before), afterSnap, actorIP); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &after, nil
}

func (r *dbAdminRepository) DeleteAdminTx(ctx context.Context, actorID, actorIP, targetID string) error {
	if actorID == targetID {
		return ErrSelfModifyForbidden
	}

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

	var before Admin
	err = tx.QueryRow(ctx, `
		SELECT a.id, a.username, COALESCE(a.nickname, ''),
		       a.role_id, r.code, r.name, r.is_super_admin, a.status, r.status, a.created_at
		FROM admins a
		JOIN roles r ON r.id = a.role_id
		WHERE a.id = $1
		FOR UPDATE OF a
	`, targetID).Scan(
		&before.ID, &before.Username, &before.Nickname,
		&before.RoleID, &before.Role, &before.RoleName, &before.IsSuperAdmin, &before.Status, &before.RoleStatus, &before.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrAdminNotFound
		}
		return err
	}

	if before.Status == StatusActive && before.IsSuperAdmin && before.RoleStatus == StatusActive {
		activeSupers, err := countActiveSuperAdminsForUpdateTx(ctx, tx)
		if err != nil {
			return err
		}
		if activeSupers <= 1 {
			return ErrLastSuperAdmin
		}
	}

	if _, err := tx.Exec(ctx, `DELETE FROM admins WHERE id = $1`, targetID); err != nil {
		return err
	}
	if err := writeAdminAuditLog(ctx, tx, actorID, "delete_admin", targetID, adminAuditSnapshot(&before), nil, actorIP); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *dbAdminRepository) AdminExists(ctx context.Context, id string) (bool, error) {
	var exists bool
	err := db.DB.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM admins WHERE id = $1)", id).Scan(&exists)
	return exists, err
}
