package auth

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"star-novel-cms/internal/db"
)

type Admin struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"-"`
	Nickname     string    `json:"nickname"`
	Role         string    `json:"role"`
	Status       int16     `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
}

type AdminRepository interface {
	CreateAdmin(ctx context.Context, id, username, passwordHash, nickname, role string) (*Admin, error)
	GetAdminByUsername(ctx context.Context, username string) (*Admin, error)
	GetAdminByID(ctx context.Context, id string) (*Admin, error)
	ListAdmins(ctx context.Context) ([]Admin, error)
	UpdateAdmin(ctx context.Context, id, nickname, role, passwordHash string) error
	DeleteAdmin(ctx context.Context, id string) error
	AdminExists(ctx context.Context, id string) (bool, error)
}

type dbAdminRepository struct{}

func NewAdminRepository() AdminRepository {
	return &dbAdminRepository{}
}

func (r *dbAdminRepository) CreateAdmin(ctx context.Context, id, username, passwordHash, nickname, role string) (*Admin, error) {
	query := `
		INSERT INTO admins (id, username, password_hash, nickname, role, status)
		VALUES ($1, $2, $3, $4, $5, 1)
		RETURNING id, username, nickname, role, status, created_at`

	var a Admin
	err := db.DB.QueryRow(ctx, query, id, username, passwordHash, nickname, role).
		Scan(&a.ID, &a.Username, &a.Nickname, &a.Role, &a.Status, &a.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func (r *dbAdminRepository) GetAdminByUsername(ctx context.Context, username string) (*Admin, error) {
	query := `SELECT id, username, password_hash, nickname, role, status, created_at FROM admins WHERE username = $1`
	var a Admin
	err := db.DB.QueryRow(ctx, query, username).
		Scan(&a.ID, &a.Username, &a.PasswordHash, &a.Nickname, &a.Role, &a.Status, &a.CreatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &a, nil
}

func (r *dbAdminRepository) GetAdminByID(ctx context.Context, id string) (*Admin, error) {
	query := `SELECT id, username, password_hash, nickname, role, status, created_at FROM admins WHERE id = $1`
	var a Admin
	err := db.DB.QueryRow(ctx, query, id).
		Scan(&a.ID, &a.Username, &a.PasswordHash, &a.Nickname, &a.Role, &a.Status, &a.CreatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &a, nil
}

func (r *dbAdminRepository) ListAdmins(ctx context.Context) ([]Admin, error) {
	query := `SELECT id, username, nickname, role, status, created_at FROM admins ORDER BY created_at DESC`
	rows, err := db.DB.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var admins []Admin
	for rows.Next() {
		var a Admin
		err = rows.Scan(&a.ID, &a.Username, &a.Nickname, &a.Role, &a.Status, &a.CreatedAt)
		if err != nil {
			return nil, err
		}
		admins = append(admins, a)
	}
	return admins, nil
}

func (r *dbAdminRepository) UpdateAdmin(ctx context.Context, id, nickname, role, passwordHash string) error {
	var err error
	if passwordHash != "" {
		_, err = db.DB.Exec(ctx, "UPDATE admins SET nickname = $1, role = $2, password_hash = $3 WHERE id = $4", nickname, role, passwordHash, id)
	} else {
		_, err = db.DB.Exec(ctx, "UPDATE admins SET nickname = $1, role = $2 WHERE id = $3", nickname, role, id)
	}
	return err
}

func (r *dbAdminRepository) DeleteAdmin(ctx context.Context, id string) error {
	_, err := db.DB.Exec(ctx, "DELETE FROM admins WHERE id = $1", id)
	return err
}

func (r *dbAdminRepository) AdminExists(ctx context.Context, id string) (bool, error) {
	var exists bool
	err := db.DB.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM admins WHERE id = $1)", id).Scan(&exists)
	return exists, err
}
