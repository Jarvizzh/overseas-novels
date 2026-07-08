package user

import (
	"context"

	"github.com/jackc/pgx/v5"
	"star-novel-cms/internal/db"
)

type UserRepository interface {
	BeginTx(ctx context.Context) (pgx.Tx, error)
	CountUsers(ctx context.Context, countQuery string, args []interface{}) (int, error)
	ListUsers(ctx context.Context, selectQuery string, args []interface{}) ([]User, error)

	GetUserBaseProfile(ctx context.Context, userID int64) (*User, error)
	GetUserWalletCoins(ctx context.Context, userID int64) (int, int, error)
	GetBookshelfItems(ctx context.Context, userID int64) ([]UserBookshelfItem, error)

	UpdateUserStatusTx(ctx context.Context, tx pgx.Tx, userID int64, status int16) (int64, error)
	InsertAdminAuditLogTx(ctx context.Context, tx pgx.Tx, adminID, action, targetID, data string) error

	CheckWalletExistsTx(ctx context.Context, tx pgx.Tx, userID int64) (bool, error)
	InsertWalletTx(ctx context.Context, tx pgx.Tx, userID int64, amount int, isBonus bool) error
	UpdateWalletCoinsTx(ctx context.Context, tx pgx.Tx, userID int64, amount int, isBonus bool) error
	InsertTransactionRecordTx(ctx context.Context, tx pgx.Tx, id string, userID int64, txType, bizType string, amount, chargedAmt, bonusAmt int, desc string) error
}

type dbUserRepository struct{}

func NewUserRepository() UserRepository {
	return &dbUserRepository{}
}

func (r *dbUserRepository) BeginTx(ctx context.Context) (pgx.Tx, error) {
	return db.DB.Begin(ctx)
}

func (r *dbUserRepository) CountUsers(ctx context.Context, countQuery string, args []interface{}) (int, error) {
	var total int
	err := db.DB.QueryRow(ctx, countQuery, args...).Scan(&total)
	return total, err
}

func (r *dbUserRepository) ListUsers(ctx context.Context, selectQuery string, args []interface{}) ([]User, error) {
	rows, err := db.DB.Query(ctx, selectQuery, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var u User
		err := rows.Scan(
			&u.ID, &u.Email, &u.Nickname, &u.AvatarURL, &u.Status,
			&u.Device, &u.IPAddress, &u.UTMSource, &u.UTMCampaign,
			&u.TotalRecharge, &u.Balance, &u.TotalSpent, &u.RecentlyReadBook,
			&u.CreatedAt,
		)
		if err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, nil
}

func (r *dbUserRepository) GetUserBaseProfile(ctx context.Context, userID int64) (*User, error) {
	query := `
		SELECT 
			id, 
			COALESCE(email, 'Guest Account'), 
			COALESCE(nickname, ''), 
			COALESCE(avatar_url, ''), 
			status, 
			COALESCE(device, ''), 
			COALESCE(ip_address, ''), 
			COALESCE(utm_source, ''), 
			COALESCE(utm_campaign, ''),
			COALESCE((SELECT SUM(amount) FROM transactions WHERE user_id = users.id AND type = 'credit' AND biz_type = 'recharge'), 0),
			COALESCE((SELECT charged_coins + bonus_coins FROM wallets WHERE user_id = users.id), 0),
			COALESCE((SELECT SUM(amount) FROM transactions WHERE user_id = users.id AND type = 'debit'), 0),
			COALESCE((
				SELECT n.title 
				FROM bookshelves b 
				JOIN novels n ON b.novel_id = n.id 
				WHERE b.user_id = users.id 
				ORDER BY b.updated_at DESC 
				LIMIT 1
			), ''),
			created_at
		FROM users
		WHERE id = $1`

	var u User
	err := db.DB.QueryRow(ctx, query, userID).Scan(
		&u.ID, &u.Email, &u.Nickname, &u.AvatarURL, &u.Status,
		&u.Device, &u.IPAddress, &u.UTMSource, &u.UTMCampaign,
		&u.TotalRecharge, &u.Balance, &u.TotalSpent, &u.RecentlyReadBook,
		&u.CreatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &u, nil
}

func (r *dbUserRepository) GetUserWalletCoins(ctx context.Context, userID int64) (int, int, error) {
	var chargedCoins, bonusCoins int
	query := `SELECT COALESCE(charged_coins, 0), COALESCE(bonus_coins, 0) FROM wallets WHERE user_id = $1`
	err := db.DB.QueryRow(ctx, query, userID).Scan(&chargedCoins, &bonusCoins)
	if err != nil {
		if err == pgx.ErrNoRows {
			return 0, 0, nil
		}
		return 0, 0, err
	}
	return chargedCoins, bonusCoins, nil
}

func (r *dbUserRepository) GetBookshelfItems(ctx context.Context, userID int64) ([]UserBookshelfItem, error) {
	query := `
		SELECT b.novel_id, n.title, COALESCE(n.cover_url, ''), b.chapter_index, b.scroll_offset_percentage, b.in_shelf, b.updated_at
		FROM bookshelves b
		JOIN novels n ON b.novel_id = n.id
		WHERE b.user_id = $1
		ORDER BY b.updated_at DESC`

	rows, err := db.DB.Query(ctx, query, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	bookshelf := []UserBookshelfItem{}
	for rows.Next() {
		var bi UserBookshelfItem
		err := rows.Scan(
			&bi.NovelID, &bi.NovelTitle, &bi.CoverURL, &bi.ChapterIndex,
			&bi.ScrollOffsetPercentage, &bi.InShelf, &bi.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		bookshelf = append(bookshelf, bi)
	}
	return bookshelf, nil
}

func (r *dbUserRepository) UpdateUserStatusTx(ctx context.Context, tx pgx.Tx, userID int64, status int16) (int64, error) {
	query := `UPDATE users SET status = $1 WHERE id = $2`
	tag, err := tx.Exec(ctx, query, status, userID)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (r *dbUserRepository) InsertAdminAuditLogTx(ctx context.Context, tx pgx.Tx, adminID, action, targetID, data string) error {
	query := `
		INSERT INTO admin_audit_logs (admin_id, action, target_id, after_data)
		VALUES ($1, $2, $3, $4)`
	_, err := tx.Exec(ctx, query, adminID, action, targetID, data)
	return err
}

func (r *dbUserRepository) CheckWalletExistsTx(ctx context.Context, tx pgx.Tx, userID int64) (bool, error) {
	var exists bool
	err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM wallets WHERE user_id = $1)", userID).Scan(&exists)
	return exists, err
}

func (r *dbUserRepository) InsertWalletTx(ctx context.Context, tx pgx.Tx, userID int64, amount int, isBonus bool) error {
	var query string
	if isBonus {
		query = `INSERT INTO wallets (user_id, charged_coins, bonus_coins) VALUES ($1, 0, $2)`
	} else {
		query = `INSERT INTO wallets (user_id, charged_coins, bonus_coins) VALUES ($1, $2, 0)`
	}
	_, err := tx.Exec(ctx, query, userID, amount)
	return err
}

func (r *dbUserRepository) UpdateWalletCoinsTx(ctx context.Context, tx pgx.Tx, userID int64, amount int, isBonus bool) error {
	var query string
	if isBonus {
		query = `UPDATE wallets SET bonus_coins = bonus_coins + $1, updated_at = CURRENT_TIMESTAMP WHERE user_id = $2`
	} else {
		query = `UPDATE wallets SET charged_coins = charged_coins + $1, updated_at = CURRENT_TIMESTAMP WHERE user_id = $2`
	}
	_, err := tx.Exec(ctx, query, amount, userID)
	return err
}

func (r *dbUserRepository) InsertTransactionRecordTx(ctx context.Context, tx pgx.Tx, id string, userID int64, txType, bizType string, amount, chargedAmt, bonusAmt int, desc string) error {
	query := `
		INSERT INTO transactions (id, user_id, type, biz_type, amount, charged_amount, bonus_amount, description)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`
	_, err := tx.Exec(ctx, query, id, userID, txType, bizType, amount, chargedAmt, bonusAmt, desc)
	return err
}
