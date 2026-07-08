package auth

import (
	"context"
	"database/sql"

	"github.com/jackc/pgx/v5"
	"novel-backend/internal/db"
	"novel-backend/internal/model"
)

type UserRepository interface {
	BeginTx(ctx context.Context) (pgx.Tx, error)
	CreateGuestUser(ctx context.Context, tx pgx.Tx, nickname, device, ipAddress, utmSource, utmCampaign string) (*model.User, error)
	CreateStandardUser(ctx context.Context, tx pgx.Tx, email, passwordHash, nickname, device, ipAddress, utmSource, utmCampaign string) (*model.User, error)
	InitWallet(ctx context.Context, tx pgx.Tx, userID int64, bonusCoins int) error
	CreateTransactionRecord(ctx context.Context, tx pgx.Tx, txID string, userID int64, txType, bizType string, amount, chargedAmount, bonusAmount int, description string) error
	EmailExists(ctx context.Context, email string) (bool, error)
	GetUserByEmail(ctx context.Context, email string) (*model.User, string, error)
	GetUserByID(ctx context.Context, id int64) (*model.User, error)
}

type dbUserRepository struct{}

func NewUserRepository() UserRepository {
	return &dbUserRepository{}
}

func (r *dbUserRepository) BeginTx(ctx context.Context) (pgx.Tx, error) {
	return db.DB.Begin(ctx)
}

func (r *dbUserRepository) CreateGuestUser(ctx context.Context, tx pgx.Tx, nickname, device, ipAddress, utmSource, utmCampaign string) (*model.User, error) {
	userQuery := `
		INSERT INTO users (email, password_hash, nickname, avatar_url, status, device, ip_address, utm_source, utm_campaign)
		VALUES (NULL, NULL, $1, '/assets/default_avatar.png', 1, $2, $3, $4, $5)
		RETURNING id, nickname, avatar_url, status, created_at`

	var user model.User
	err := tx.QueryRow(ctx, userQuery, nickname, device, ipAddress, utmSource, utmCampaign).Scan(
		&user.ID, &user.Nickname, &user.AvatarURL, &user.Status, &user.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	user.Device = device
	user.IPAddress = ipAddress
	user.UTMSource = utmSource
	user.UTMCampaign = utmCampaign
	return &user, nil
}

func (r *dbUserRepository) CreateStandardUser(ctx context.Context, tx pgx.Tx, email, passwordHash, nickname, device, ipAddress, utmSource, utmCampaign string) (*model.User, error) {
	userQuery := `
		INSERT INTO users (email, password_hash, nickname, avatar_url, status, device, ip_address, utm_source, utm_campaign)
		VALUES ($1, $2, $3, '/assets/default_avatar.png', 1, $4, $5, $6, $7)
		RETURNING id, email, nickname, avatar_url, status, created_at`

	var user model.User
	var emailStr string
	err := tx.QueryRow(ctx, userQuery, email, passwordHash, nickname, device, ipAddress, utmSource, utmCampaign).Scan(
		&user.ID, &emailStr, &user.Nickname, &user.AvatarURL, &user.Status, &user.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	user.Email = &emailStr
	user.Device = device
	user.IPAddress = ipAddress
	user.UTMSource = utmSource
	user.UTMCampaign = utmCampaign
	return &user, nil
}

func (r *dbUserRepository) InitWallet(ctx context.Context, tx pgx.Tx, userID int64, bonusCoins int) error {
	walletQuery := `
		INSERT INTO wallets (user_id, charged_coins, bonus_coins, updated_at)
		VALUES ($1, 0, $2, CURRENT_TIMESTAMP)`
	_, err := tx.Exec(ctx, walletQuery, userID, bonusCoins)
	return err
}

func (r *dbUserRepository) CreateTransactionRecord(ctx context.Context, tx pgx.Tx, txID string, userID int64, txType, bizType string, amount, chargedAmount, bonusAmount int, description string) error {
	txLogQuery := `
		INSERT INTO transactions (id, user_id, type, biz_type, amount, charged_amount, bonus_amount, description)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`
	_, err := tx.Exec(ctx, txLogQuery, txID, userID, txType, bizType, amount, chargedAmount, bonusAmount, description)
	return err
}

func (r *dbUserRepository) EmailExists(ctx context.Context, email string) (bool, error) {
	var exists bool
	checkQuery := `SELECT EXISTS(SELECT 1 FROM users WHERE email = $1)`
	err := db.DB.QueryRow(ctx, checkQuery, email).Scan(&exists)
	return exists, err
}

func (r *dbUserRepository) GetUserByEmail(ctx context.Context, email string) (*model.User, string, error) {
	var user model.User
	var passwordHash string
	var emailVal sql.NullString

	query := `
		SELECT id, email, password_hash, nickname, avatar_url, status, created_at
		FROM users
		WHERE email = $1`
	err := db.DB.QueryRow(ctx, query, email).Scan(
		&user.ID, &emailVal, &passwordHash, &user.Nickname, &user.AvatarURL, &user.Status, &user.CreatedAt,
	)
	if err != nil {
		return nil, "", err
	}

	if emailVal.Valid {
		user.Email = &emailVal.String
	}
	return &user, passwordHash, nil
}

func (r *dbUserRepository) GetUserByID(ctx context.Context, id int64) (*model.User, error) {
	var user model.User
	var emailVal sql.NullString

	query := `
		SELECT id, email, nickname, avatar_url, status, created_at
		FROM users
		WHERE id = $1`
	err := db.DB.QueryRow(ctx, query, id).Scan(
		&user.ID, &emailVal, &user.Nickname, &user.AvatarURL, &user.Status, &user.CreatedAt,
	)
	if err != nil {
		return nil, err
	}

	if emailVal.Valid {
		user.Email = &emailVal.String
	}
	return &user, nil
}
