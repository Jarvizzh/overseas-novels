package wallet

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"novel-backend/internal/db"
	"novel-backend/internal/model"
)

var (
	ErrInsufficientBalance = errors.New("insufficient balance")
	ErrAlreadyUnlocked     = errors.New("chapter is already unlocked")
)

type Repository interface {
	GetWallet(ctx context.Context, userID int64) (*model.Wallet, error)
	GetTransactions(ctx context.Context, userID int64, limit, offset int) ([]model.Transaction, error)
	UnlockChapterTx(ctx context.Context, userID int64, novelID int64, chapterIndex int, price int, chapterTitle string) error
	AddCoins(ctx context.Context, userID int64, amount int, isBonus bool, bizType string, description string) error
	HasCheckedInToday(ctx context.Context, userID int64) (bool, error)
	GetPriceCentsByCoins(ctx context.Context, coins int) (int, error)
	GetUserEmail(ctx context.Context, userID int64) (string, error)
	GetRechargeTemplateByID(ctx context.Context, templateID int) (*model.RechargeTemplate, error)
	GetDefaultRechargeTemplate(ctx context.Context) (*model.RechargeTemplate, error)
	GetFirstRechargeTemplate(ctx context.Context) (*model.RechargeTemplate, error)
	GetRechargeSlots(ctx context.Context, templateID int) ([]model.RechargeSlot, error)
	RecordRechargeOrderAndCreditCoinsTx(ctx context.Context, userID int64, externalRefID string, coinsAmount int, amountCents int64, currency string, paymentMethod string, fbLeadJSON string) error
	CreatePendingOrder(ctx context.Context, userID int64, externalRefID string, coinsAmount int, amountCents int64, currency string, paymentMethod string) error
}

type dbRepository struct{}

func NewDBRepository() Repository {
	return &dbRepository{}
}

func (r *dbRepository) GetWallet(ctx context.Context, userID int64) (*model.Wallet, error) {
	query := `SELECT user_id, charged_coins, bonus_coins, updated_at FROM wallets WHERE user_id = $1`
	var w model.Wallet
	err := db.DB.QueryRow(ctx, query, userID).Scan(&w.UserID, &w.ChargedCoins, &w.BonusCoins, &w.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &w, nil
}

func (r *dbRepository) GetTransactions(ctx context.Context, userID int64, limit, offset int) ([]model.Transaction, error) {
	query := `
		SELECT id, user_id, type, biz_type, amount, charged_amount, bonus_amount, description, created_at
		FROM transactions
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3`
	rows, err := db.DB.Query(ctx, query, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var txs []model.Transaction
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
	return txs, nil
}

func (r *dbRepository) UnlockChapterTx(ctx context.Context, userID int64, novelID int64, chapterIndex int, price int, chapterTitle string) error {
	tx, err := db.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// 1. Fetch wallet balance WITH ROW LOCK (FOR UPDATE) FIRST to serialize concurrent unlocks for the user
	walletQuery := `SELECT charged_coins, bonus_coins FROM wallets WHERE user_id = $1 FOR UPDATE`
	var chargedCoins, bonusCoins int
	err = tx.QueryRow(ctx, walletQuery, userID).Scan(&chargedCoins, &bonusCoins)
	if err != nil {
		return err
	}

	// 2. Check if already unlocked inside the locked transaction
	var exists bool
	checkQuery := `SELECT EXISTS(SELECT 1 FROM unlock_records WHERE user_id = $1 AND novel_id = $2 AND chapter_index = $3)`
	err = tx.QueryRow(ctx, checkQuery, userID, novelID, chapterIndex).Scan(&exists)
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
	_, err = tx.Exec(ctx, updateWalletQuery, deductCharged, deductBonus, userID)
	if err != nil {
		return err
	}

	// 4. Create transaction log
	txID := uuid.New().String()
	insertTxQuery := `
		INSERT INTO transactions (id, user_id, type, biz_type, amount, charged_amount, bonus_amount, description)
		VALUES ($1, $2, 'debit', 'unlock', $3, $4, $5, $6)`
	desc := "Unlocked: " + chapterTitle
	_, err = tx.Exec(ctx, insertTxQuery, txID, userID, price, deductCharged, deductBonus, desc)
	if err != nil {
		return err
	}

	// 5. Create unlock record
	insertUnlockQuery := `
		INSERT INTO unlock_records (user_id, novel_id, chapter_index)
		VALUES ($1, $2, $3)`
	_, err = tx.Exec(ctx, insertUnlockQuery, userID, novelID, chapterIndex)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func (r *dbRepository) AddCoins(ctx context.Context, userID int64, amount int, isBonus bool, bizType string, description string) error {
	tx, err := db.DB.Begin(ctx)
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

func (r *dbRepository) HasCheckedInToday(ctx context.Context, userID int64) (bool, error) {
	checkQuery := `
		SELECT EXISTS(
			SELECT 1 FROM transactions 
			WHERE user_id = $1 AND biz_type = 'checkin' AND created_at::date = CURRENT_DATE
		)`
	var checkedInToday bool
	err := db.DB.QueryRow(ctx, checkQuery, userID).Scan(&checkedInToday)
	return checkedInToday, err
}

func (r *dbRepository) GetPriceCentsByCoins(ctx context.Context, coins int) (int, error) {
	var priceCents int
	query := "SELECT price_cents FROM recharge_slots WHERE (coins + bonus) = $1 OR coins = $1 LIMIT 1"
	err := db.DB.QueryRow(ctx, query, coins).Scan(&priceCents)
	if err != nil {
		if err == pgx.ErrNoRows {
			return 0, nil
		}
		return 0, err
	}
	return priceCents, nil
}

func (r *dbRepository) GetUserEmail(ctx context.Context, userID int64) (string, error) {
	var email sql.NullString
	query := "SELECT email FROM users WHERE id = $1"
	err := db.DB.QueryRow(ctx, query, userID).Scan(&email)
	if err != nil {
		if err == pgx.ErrNoRows {
			return "", nil
		}
		return "", err
	}
	return email.String, nil
}

func (r *dbRepository) GetRechargeTemplateByID(ctx context.Context, templateID int) (*model.RechargeTemplate, error) {
	var t model.RechargeTemplate
	query := `
		SELECT id, name, is_default
		FROM recharge_templates
		WHERE id = $1
		LIMIT 1`
	err := db.DB.QueryRow(ctx, query, templateID).Scan(&t.ID, &t.Name, &t.IsDefault)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &t, nil
}

func (r *dbRepository) GetDefaultRechargeTemplate(ctx context.Context) (*model.RechargeTemplate, error) {
	var t model.RechargeTemplate
	query := `
		SELECT id, name, is_default
		FROM recharge_templates
		WHERE is_default = TRUE
		LIMIT 1`
	err := db.DB.QueryRow(ctx, query).Scan(&t.ID, &t.Name, &t.IsDefault)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &t, nil
}

func (r *dbRepository) GetFirstRechargeTemplate(ctx context.Context) (*model.RechargeTemplate, error) {
	var t model.RechargeTemplate
	query := `
		SELECT id, name, is_default
		FROM recharge_templates
		ORDER BY id ASC
		LIMIT 1`
	err := db.DB.QueryRow(ctx, query).Scan(&t.ID, &t.Name, &t.IsDefault)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &t, nil
}

func (r *dbRepository) GetRechargeSlots(ctx context.Context, templateID int) ([]model.RechargeSlot, error) {
	query := `
		SELECT id, template_id, slot_index, type, coins, bonus, COALESCE(vip_duration, ''), COALESCE(vip_name, ''), COALESCE(vip_desc, ''), price, price_cents
		FROM recharge_slots
		WHERE template_id = $1
		ORDER BY slot_index ASC`
	rows, err := db.DB.Query(ctx, query, templateID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var slots []model.RechargeSlot
	for rows.Next() {
		var s model.RechargeSlot
		err := rows.Scan(&s.ID, &s.TemplateID, &s.SlotIndex, &s.Type, &s.Coins, &s.Bonus, &s.VipDuration, &s.VipName, &s.VipDesc, &s.Price, &s.PriceCents)
		if err != nil {
			return nil, err
		}
		slots = append(slots, s)
	}
	return slots, nil
}

func (r *dbRepository) RecordRechargeOrderAndCreditCoinsTx(ctx context.Context, userID int64, externalRefID string, coinsAmount int, amountCents int64, currency string, paymentMethod string, fbLeadJSON string) error {
	tx, err := db.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// 1. Fetch UTM parameters from users table
	var utmSource, utmCampaign sql.NullString
	userQuery := `SELECT utm_source, utm_campaign FROM users WHERE id = $1`
	err = tx.QueryRow(ctx, userQuery, userID).Scan(&utmSource, &utmCampaign)
	if err != nil && err != pgx.ErrNoRows {
		return err
	}

	// 2. Fetch slot information to distinguish charged coins vs bonus coins
	var slotCoins, slotBonus, slotPriceCents int
	var slotType string
	slotQuery := `SELECT coins, bonus, price_cents, type FROM recharge_slots WHERE (coins + bonus) = $1 OR coins = $1 LIMIT 1`
	err = tx.QueryRow(ctx, slotQuery, coinsAmount).Scan(&slotCoins, &slotBonus, &slotPriceCents, &slotType)
	if err != nil {
		if err == pgx.ErrNoRows {
			// Fallback: treat all as charged coins
			slotCoins = coinsAmount
			slotBonus = 0
			slotPriceCents = int(amountCents)
			slotType = "single"
		} else {
			return err
		}
	}

	// 2b. Fetch promotion link and novel details from UTM parameters
	var promotionLinkID, novelID sql.NullInt64
	if utmSource.Valid && utmSource.String != "" && utmCampaign.Valid && utmCampaign.String != "" {
		promoQuery := `SELECT id, novel_id FROM promotion_links WHERE utm_source = $1 AND utm_campaign = $2 LIMIT 1`
		err = tx.QueryRow(ctx, promoQuery, utmSource.String, utmCampaign.String).Scan(&promotionLinkID, &novelID)
		if err != nil && err != pgx.ErrNoRows {
			return err
		}
	}

	// If slotPriceCents is 0 and amountCents is passed, use amountCents
	finalAmountCents := slotPriceCents
	if finalAmountCents == 0 && amountCents > 0 {
		finalAmountCents = int(amountCents)
	}

	// 3. Check if this order (externalRefID) is already processed
	var status string
	checkQuery := `SELECT status FROM recharge_orders WHERE external_ref_id = $1`
	err = tx.QueryRow(ctx, checkQuery, externalRefID).Scan(&status)
	if err != nil {
		if err == pgx.ErrNoRows {
			// Order doesn't exist at all, we insert a new Success order
			var utmSrcVal, utmCampVal *string
			if utmSource.Valid && utmSource.String != "" {
				utmSrcVal = &utmSource.String
			}
			if utmCampaign.Valid && utmCampaign.String != "" {
				utmCampVal = &utmCampaign.String
			}
			var fbLeadVal *string
			if fbLeadJSON != "" {
				fbLeadVal = &fbLeadJSON
			}

			insertOrderQuery := `
				INSERT INTO recharge_orders (
					user_id, external_ref_id, amount_cents, currency, coins, 
					bonus_coins_credited, payment_method, status, utm_source, utm_campaign, 
					fb_lead_metadata, paid_at, order_type, promotion_link_id, novel_id
				)
				VALUES ($1, $2, $3, $4, $5, $6, $7, 'Success', $8, $9, $10, CURRENT_TIMESTAMP, $11, $12, $13)`
			_, err = tx.Exec(ctx, insertOrderQuery, userID, externalRefID, finalAmountCents, currency, slotCoins, slotBonus, paymentMethod, utmSrcVal, utmCampVal, fbLeadVal, slotType, promotionLinkID, novelID)
			if err != nil {
				return err
			}
		} else {
			return err
		}
	} else {
		// Order already exists
		if status == "Success" || status == "Paid" {
			// Already completed, skip (idempotency)
			return nil
		}
		// If it is Pending, we update it to Success and credit coins
		var fbLeadVal *string
		if fbLeadJSON != "" {
			fbLeadVal = &fbLeadJSON
		}
		updateOrderQuery := `
			UPDATE recharge_orders
			SET status = 'Success', paid_at = CURRENT_TIMESTAMP, fb_lead_metadata = COALESCE($1, fb_lead_metadata), updated_at = CURRENT_TIMESTAMP
			WHERE external_ref_id = $2`
		_, err = tx.Exec(ctx, updateOrderQuery, fbLeadVal, externalRefID)
		if err != nil {
			return err
		}
	}

	// 4. Update wallet balance
	updateQuery := `
		INSERT INTO wallets (user_id, charged_coins, bonus_coins, updated_at)
		VALUES ($1, $2, $3, CURRENT_TIMESTAMP)
		ON CONFLICT (user_id) DO UPDATE 
		SET charged_coins = wallets.charged_coins + EXCLUDED.charged_coins, 
		    bonus_coins = wallets.bonus_coins + EXCLUDED.bonus_coins, 
		    updated_at = CURRENT_TIMESTAMP`
	_, err = tx.Exec(ctx, updateQuery, userID, slotCoins, slotBonus)
	if err != nil {
		return err
	}

	// 5. Create transaction record
	txID := uuid.New().String()
	insertTxQuery := `
		INSERT INTO transactions (id, user_id, type, biz_type, amount, charged_amount, bonus_amount, description)
		VALUES ($1, $2, 'credit', 'recharge', $3, $4, $5, $6)`
	desc := paymentMethod + " Recharge (+" + strconv.Itoa(coinsAmount) + " Coins)"
	_, err = tx.Exec(ctx, insertTxQuery, txID, userID, coinsAmount, slotCoins, slotBonus, desc)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func (r *dbRepository) CreatePendingOrder(ctx context.Context, userID int64, externalRefID string, coinsAmount int, amountCents int64, currency string, paymentMethod string) error {
	// 1. Fetch UTM parameters from users table
	var utmSource, utmCampaign sql.NullString
	userQuery := `SELECT utm_source, utm_campaign FROM users WHERE id = $1`
	err := db.DB.QueryRow(ctx, userQuery, userID).Scan(&utmSource, &utmCampaign)
	if err != nil && err != pgx.ErrNoRows {
		return err
	}

	// 2. Fetch slot information to distinguish charged coins vs bonus coins and slot type
	var slotCoins, slotBonus, slotPriceCents int
	var slotType string
	slotQuery := `SELECT coins, bonus, price_cents, type FROM recharge_slots WHERE (coins + bonus) = $1 OR coins = $1 LIMIT 1`
	err = db.DB.QueryRow(ctx, slotQuery, coinsAmount).Scan(&slotCoins, &slotBonus, &slotPriceCents, &slotType)
	if err != nil {
		if err == pgx.ErrNoRows {
			slotCoins = coinsAmount
			slotBonus = 0
			slotPriceCents = int(amountCents)
			slotType = "single"
		} else {
			return err
		}
	}

	// 3. Fetch promotion link and novel details from UTM parameters
	var promotionLinkID, novelID sql.NullInt64
	if utmSource.Valid && utmSource.String != "" && utmCampaign.Valid && utmCampaign.String != "" {
		promoQuery := `SELECT id, novel_id FROM promotion_links WHERE utm_source = $1 AND utm_campaign = $2 LIMIT 1`
		err = db.DB.QueryRow(ctx, promoQuery, utmSource.String, utmCampaign.String).Scan(&promotionLinkID, &novelID)
		if err != nil && err != pgx.ErrNoRows {
			return err
		}
	}

	finalAmountCents := slotPriceCents
	if finalAmountCents == 0 && amountCents > 0 {
		finalAmountCents = int(amountCents)
	}

	var utmSrcVal, utmCampVal *string
	if utmSource.Valid && utmSource.String != "" {
		utmSrcVal = &utmSource.String
	}
	if utmCampaign.Valid && utmCampaign.String != "" {
		utmCampVal = &utmCampaign.String
	}

	insertOrderQuery := `
		INSERT INTO recharge_orders (
			user_id, external_ref_id, amount_cents, currency, coins, 
			bonus_coins_credited, payment_method, status, utm_source, utm_campaign, 
			order_type, promotion_link_id, novel_id
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'Pending', $8, $9, $10, $11, $12)
		ON CONFLICT (external_ref_id) DO NOTHING`
	_, err = db.DB.Exec(ctx, insertOrderQuery, userID, externalRefID, finalAmountCents, currency, slotCoins, slotBonus, paymentMethod, utmSrcVal, utmCampVal, slotType, promotionLinkID, novelID)
	return err
}
