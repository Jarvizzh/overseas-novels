package wallet

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"reader-backend/internal/conversion"
	"reader-backend/internal/db"
	"reader-backend/internal/model"
	"reader-backend/internal/novel"
)

var (
	ErrInsufficientBalance = errors.New("insufficient balance")
	ErrAlreadyUnlocked     = errors.New("chapter is already unlocked")
)

type Repository interface {
	GetWallet(ctx context.Context, userID int64) (*model.Wallet, error)
	GetTransactions(ctx context.Context, userID int64, limit, offset int) ([]model.Transaction, error)
	getTransactionsAfter(ctx context.Context, userID int64, limit int, after *historyCursor) ([]model.Transaction, error)
	UnlockChapterTx(ctx context.Context, userID int64, novelID int64, chapterIndex, promoID int, quote ChapterQuote) error
	AddCoins(ctx context.Context, userID int64, amount int, isBonus bool, bizType string, description string) error
	CheckIn(context.Context, int64, bool) (*CheckInState, error)
	CreditRechargeTx(context.Context, pgx.Tx, int64, int, int, string) error
}

type dbRepository struct{}

func NewDBRepository() Repository {
	return &dbRepository{}
}

func (r *dbRepository) GetWallet(ctx context.Context, userID int64) (*model.Wallet, error) {
	query := `SELECT user_id, charged_coins, bonus_coins, updated_at FROM wallets WHERE user_id = $1`
	var w model.Wallet
	err := db.Pool(ctx).QueryRow(ctx, query, userID).Scan(&w.UserID, &w.ChargedCoins, &w.BonusCoins, &w.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &w, nil
}

// Treat missing historical timestamps as the oldest value, so every row has
// a stable position in both offset and cursor pagination.
const transactionDate = `COALESCE(created_at, TIMESTAMPTZ '0001-01-01 00:00:00+00')`
const transactionHistorySelect = `
		SELECT id, user_id, type, biz_type, amount, COALESCE(charged_amount,0), COALESCE(bonus_amount,0),
		       COALESCE(description,''), ` + transactionDate + `
		FROM transactions
		WHERE user_id = $1`
const transactionHistoryOrder = ` ORDER BY ` + transactionDate + ` DESC, id DESC LIMIT $2`

func (r *dbRepository) GetTransactions(ctx context.Context, userID int64, limit, offset int) ([]model.Transaction, error) {
	return queryTransactions(ctx, transactionHistorySelect+transactionHistoryOrder+` OFFSET $3`, userID, limit, offset)
}

func (r *dbRepository) getTransactionsAfter(ctx context.Context, userID int64, limit int, after *historyCursor) ([]model.Transaction, error) {
	if after == nil {
		return queryTransactions(ctx, transactionHistorySelect+transactionHistoryOrder, userID, limit)
	}
	return queryTransactions(ctx, transactionHistorySelect+` AND (`+transactionDate+`, id) < ($3::timestamptz, $4::varchar)`+transactionHistoryOrder,
		userID, limit, after.createdAt, after.id)
}

func queryTransactions(ctx context.Context, query string, args ...any) ([]model.Transaction, error) {
	rows, err := db.Pool(ctx).Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	txs := make([]model.Transaction, 0)
	for rows.Next() {
		var t model.Transaction
		err = rows.Scan(
			&t.ID, &t.UserID, &t.Type, &t.BizType, &t.Amount, &t.ChargedAmt, &t.BonusAmt, &t.Description, &t.CreatedAt,
		)
		if err != nil {
			return nil, err
		}
		txs = append(txs, t)
	}
	return txs, rows.Err()
}

func (r *dbRepository) UnlockChapterTx(ctx context.Context, userID int64, novelID int64, chapterIndex, promoID int, quote ChapterQuote) error {
	txCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	tx, err := db.Pool(txCtx).Begin(txCtx)
	if err != nil {
		return err
	}
	defer tx.Rollback(txCtx)

	// 1. Fetch wallet balance WITH ROW LOCK (FOR UPDATE) FIRST to serialize concurrent unlocks for the user
	walletQuery := `SELECT charged_coins, bonus_coins FROM wallets WHERE user_id = $1 FOR UPDATE`
	var chargedCoins, bonusCoins int
	err = tx.QueryRow(txCtx, walletQuery, userID).Scan(&chargedCoins, &bonusCoins)
	if err != nil {
		return err
	}

	var chapterID, chapterTitle string
	var version int64
	var wordCount int
	err = tx.QueryRow(txCtx, `
		SELECT c.id, c.title, c.word_count, n.content_version
		FROM novels n JOIN chapters c ON c.novel_id = n.id AND c.chapter_index = $2
		WHERE n.id = $1 AND n.status <> 'archived' AND n.publish_status = 'published'
		FOR SHARE OF n`, novelID, chapterIndex).Scan(&chapterID, &chapterTitle, &wordCount, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrPriceChanged
	}
	if err != nil {
		return err
	}
	if chapterID != quote.ID || version != quote.Version {
		return ErrPriceChanged
	}
	start, cost, err := novel.ReadChapterPricing(txCtx, tx, userID, novelID, promoID)
	if err != nil {
		return err
	}
	price := novel.CalculateChapterPrice(wordCount, cost)
	if chapterIndex < start-1 || cost == 0 || price != quote.Price {
		return ErrPriceChanged
	}

	var exists, unresolved bool
	err = tx.QueryRow(txCtx, `SELECT reader_has_chapter_unlock($1, $3),
		EXISTS(SELECT 1 FROM unlock_records WHERE user_id=$1 AND novel_id=$2 AND chapter_id IS NULL)`,
		userID, novelID, chapterID).Scan(&exists, &unresolved)
	if err != nil {
		return err
	}
	if exists {
		return ErrAlreadyUnlocked
	}
	if unresolved {
		return ErrPurchaseNeedsReview
	}

	err = tx.QueryRow(txCtx, `SELECT EXISTS(SELECT 1 FROM user_subscriptions WHERE user_id=$1 AND payment_subscription_environment(user_subscriptions)=$3 AND `+novel.PaidSubscriptionPredicate+`)
		OR EXISTS(SELECT 1 FROM recharge_orders WHERE user_id=$1 AND novel_id=$2 AND `+novel.WholeBookEntitlementPredicate+` AND `+novel.WholeBookEnvironmentPredicate("$3")+`)`,
		userID, novelID, conversion.Environment()).Scan(&exists)
	if err != nil {
		return err
	}
	if exists {
		return ErrAlreadyUnlocked
	}

	if chargedCoins+bonusCoins < price {
		return ErrInsufficientBalance
	}

	// 3. Deduct coins (Bonus first)
	deductBonus := int(math.Min(float64(bonusCoins), float64(price)))
	deductCharged := price - deductBonus

	updateWalletQuery := `
		UPDATE wallets 
		SET charged_coins = charged_coins - $1, bonus_coins = bonus_coins - $2, updated_at = CURRENT_TIMESTAMP 
		WHERE user_id = $3`
	_, err = tx.Exec(txCtx, updateWalletQuery, deductCharged, deductBonus, userID)
	if err != nil {
		return err
	}

	// 4. Create transaction log
	txID := uuid.New().String()
	insertTxQuery := `
		INSERT INTO transactions (id, user_id, type, biz_type, amount, charged_amount, bonus_amount, description)
		VALUES ($1, $2, 'debit', 'unlock', $3, $4, $5, $6)`
	desc := "Unlocked: " + chapterTitle
	_, err = tx.Exec(txCtx, insertTxQuery, txID, userID, price, deductCharged, deductBonus, desc)
	if err != nil {
		return err
	}

	// 5. Create unlock record
	insertUnlockQuery := `
		INSERT INTO unlock_records (user_id, novel_id, chapter_index, chapter_id, identity_evidence, identity_resolved_at, created_at)
		VALUES ($1, $2, $3, $4, 'stable chapter purchase', clock_timestamp(), clock_timestamp())`
	_, err = tx.Exec(txCtx, insertUnlockQuery, userID, novelID, chapterIndex, chapterID)
	if err != nil {
		return err
	}

	return tx.Commit(txCtx)
}

func (r *dbRepository) AddCoins(ctx context.Context, userID int64, amount int, isBonus bool, bizType string, description string) error {
	tx, err := db.Pool(ctx).Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// 1. Update wallet balance
	var updateQuery string
	if isBonus {
		updateQuery = `
			INSERT INTO wallets (user_id, charged_coins, bonus_coins, updated_at)
			VALUES ($1, 0, $2, CURRENT_TIMESTAMP)
			ON CONFLICT (user_id) DO UPDATE 
			SET bonus_coins = wallets.bonus_coins + EXCLUDED.bonus_coins, updated_at = CURRENT_TIMESTAMP`
	} else {
		updateQuery = `
			INSERT INTO wallets (user_id, charged_coins, bonus_coins, updated_at)
			VALUES ($1, $2, 0, CURRENT_TIMESTAMP)
			ON CONFLICT (user_id) DO UPDATE 
			SET charged_coins = wallets.charged_coins + EXCLUDED.charged_coins, updated_at = CURRENT_TIMESTAMP`
	}

	_, err = tx.Exec(ctx, updateQuery, userID, amount)
	if err != nil {
		return err
	}

	// 2. Create transaction record
	txID := uuid.New().String()
	insertTxQuery := `
		INSERT INTO transactions (id, user_id, type, biz_type, amount, charged_amount, bonus_amount, description)
		VALUES ($1, $2, 'credit', $3, $4, $5, $6, $7)`
	var chargedInc, bonusInc int
	if isBonus {
		bonusInc = amount
	} else {
		chargedInc = amount
	}
	_, err = tx.Exec(ctx, insertTxQuery, txID, userID, bizType, amount, chargedInc, bonusInc, description)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// CreditRechargeTx records both wallet balances and their ledger entry.
// The caller owns settlement deduplication and commits the transaction.
func (r *dbRepository) CreditRechargeTx(ctx context.Context, tx pgx.Tx, userID int64, chargedCoins, bonusCoins int, description string) error {
	_, err := tx.Exec(ctx, `INSERT INTO wallets(user_id, charged_coins, bonus_coins) VALUES ($1,$2,$3)
		ON CONFLICT (user_id) DO UPDATE SET charged_coins=wallets.charged_coins+$2,
		bonus_coins=wallets.bonus_coins+$3, updated_at=now()`, userID, chargedCoins, bonusCoins)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO transactions(id,user_id,type,biz_type,amount,charged_amount,bonus_amount,description)
		VALUES ($1,$2,'credit','recharge',$3,$4,$5,$6)`, uuid.NewString(), userID, chargedCoins+bonusCoins,
		chargedCoins, bonusCoins, description)
	return err
}
