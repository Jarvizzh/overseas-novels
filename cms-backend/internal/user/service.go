package user

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

var (
	ErrUserNotFound = errors.New("User not found")
)

type UserService interface {
	ListUsers(ctx context.Context, page, pageSize int, search, status string) ([]User, int, error)
	GetUserDetail(ctx context.Context, userID int64) (*UserDetail, error)
	ToggleUserStatus(ctx context.Context, userID int64, status int16, adminID string) (string, error)
	AdjustWallet(ctx context.Context, userID int64, amount int, isBonus bool, reason string, adminID string) error
}

type userService struct {
	repo UserRepository
}

func NewUserService(repo UserRepository) UserService {
	return &userService{
		repo: repo,
	}
}

func (s *userService) ListUsers(ctx context.Context, page, pageSize int, search, status string) ([]User, int, error) {
	offset := (page - 1) * pageSize

	var whereClauses []string
	var args []interface{}
	argCount := 1

	if search != "" {
		idVal, err := strconv.ParseInt(search, 10, 64)
		if err == nil {
			whereClauses = append(whereClauses, fmt.Sprintf("(u.email ILIKE $%d OR u.nickname ILIKE $%d OR u.id = $%d)", argCount, argCount+1, argCount+2))
			args = append(args, "%"+search+"%", "%"+search+"%", idVal)
			argCount += 3
		} else {
			whereClauses = append(whereClauses, fmt.Sprintf("(u.email ILIKE $%d OR u.nickname ILIKE $%d)", argCount, argCount+1))
			args = append(args, "%"+search+"%", "%"+search+"%")
			argCount += 2
		}
	}

	if status != "" {
		st, _ := strconv.Atoi(status)
		whereClauses = append(whereClauses, fmt.Sprintf("u.status = $%d", argCount))
		args = append(args, st)
		argCount++
	}

	whereSQL := ""
	if len(whereClauses) > 0 {
		whereSQL = "WHERE " + strings.Join(whereClauses, " AND ")
	}

	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM users u %s", whereSQL)
	total, err := s.repo.CountUsers(ctx, countQuery, args)
	if err != nil {
		return nil, 0, err
	}

	selectQuery := fmt.Sprintf(`
		SELECT 
			u.id, 
			COALESCE(u.email, 'Guest Account'), 
			COALESCE(u.nickname, ''), 
			COALESCE(u.avatar_url, ''), 
			u.status, 
			COALESCE(u.device, ''), 
			COALESCE(u.ip_address, ''), 
			COALESCE(u.utm_source, ''), 
			COALESCE(u.utm_campaign, ''),
			COALESCE((SELECT SUM(amount) FROM transactions WHERE user_id = u.id AND type = 'credit' AND biz_type = 'recharge'), 0) AS total_recharge,
			COALESCE((SELECT charged_coins + bonus_coins FROM wallets WHERE user_id = u.id), 0) AS balance,
			COALESCE((SELECT SUM(amount) FROM transactions WHERE user_id = u.id AND type = 'debit'), 0) AS total_spent,
			COALESCE((
				SELECT n.title 
				FROM bookshelves b 
				JOIN novels n ON b.novel_id = n.id 
				WHERE b.user_id = u.id 
				ORDER BY b.updated_at DESC 
				LIMIT 1
			), '') AS recently_read_book,
			u.created_at
		FROM users u
		%s
		ORDER BY u.created_at DESC
		LIMIT $%d OFFSET $%d`, whereSQL, argCount, argCount+1)

	args = append(args, pageSize, offset)
	users, err := s.repo.ListUsers(ctx, selectQuery, args)
	if err != nil {
		return nil, 0, err
	}

	return users, total, nil
}

func (s *userService) GetUserDetail(ctx context.Context, userID int64) (*UserDetail, error) {
	u, err := s.repo.GetUserBaseProfile(ctx, userID)
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, ErrUserNotFound
	}

	charged, bonus, err := s.repo.GetUserWalletCoins(ctx, userID)
	if err != nil {
		return nil, err
	}

	bookshelf, err := s.repo.GetBookshelfItems(ctx, userID)
	if err != nil {
		return nil, err
	}

	return &UserDetail{
		User:         *u,
		ChargedCoins: charged,
		BonusCoins:   bonus,
		Bookshelf:    bookshelf,
	}, nil
}

func (s *userService) ToggleUserStatus(ctx context.Context, userID int64, status int16, adminID string) (string, error) {
	tx, err := s.repo.BeginTx(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	affected, err := s.repo.UpdateUserStatusTx(ctx, tx, userID, status)
	if err != nil {
		return "", err
	}
	if affected == 0 {
		return "", ErrUserNotFound
	}

	afterJSON := fmt.Sprintf(`{"status": %d}`, status)
	_ = s.repo.InsertAdminAuditLogTx(ctx, tx, adminID, "ban_user", strconv.FormatInt(userID, 10), afterJSON)

	err = tx.Commit(ctx)
	if err != nil {
		return "", err
	}

	statusStr := "Normal"
	if status == 2 {
		statusStr = "Banned"
	}
	return statusStr, nil
}

func (s *userService) AdjustWallet(ctx context.Context, userID int64, amount int, isBonus bool, reason string, adminID string) error {
	tx, err := s.repo.BeginTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	exists, err := s.repo.CheckWalletExistsTx(ctx, tx, userID)
	if err != nil {
		return err
	}

	if !exists {
		err = s.repo.InsertWalletTx(ctx, tx, userID, amount, isBonus)
	} else {
		err = s.repo.UpdateWalletCoinsTx(ctx, tx, userID, amount, isBonus)
	}
	if err != nil {
		return err
	}

	txID := uuid.New().String()
	txType := "credit"
	if amount < 0 {
		txType = "debit"
	}

	chargedAmt := 0
	bonusAmt := 0
	absAmt := amount
	if amount < 0 {
		absAmt = -amount
	}

	if isBonus {
		bonusAmt = amount
	} else {
		chargedAmt = amount
	}

	desc := fmt.Sprintf("System Adjustment: %s (Reason: %s)", reason, reason)
	err = s.repo.InsertTransactionRecordTx(ctx, tx, txID, userID, txType, "system_grant", absAmt, chargedAmt, bonusAmt, desc)
	if err != nil {
		return err
	}

	afterJSON := fmt.Sprintf(`{"adjustment": %d, "is_bonus": %t, "reason": "%s"}`, amount, isBonus, reason)
	_ = s.repo.InsertAdminAuditLogTx(ctx, tx, adminID, "wallet_grant", strconv.FormatInt(userID, 10), afterJSON)

	return tx.Commit(ctx)
}
