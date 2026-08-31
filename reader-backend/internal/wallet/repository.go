package wallet

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"math"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"reader-backend/internal/db"
	"reader-backend/internal/model"
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
	GetRechargeSlotByID(ctx context.Context, slotID int) (*model.RechargeSlot, error)
	GetProviderPlan(ctx context.Context, provider string, slotID int) (*model.PaymentProviderPlan, error)
	SaveProviderPlan(ctx context.Context, plan *model.PaymentProviderPlan) error
	CreateUserSubscriptionTx(ctx context.Context, tx pgx.Tx, sub *model.UserSubscription) error
	UpsertUserSubscription(ctx context.Context, sub *model.UserSubscription) error
	GetActiveSubscriptionByUserID(ctx context.Context, userID int64) (*model.UserSubscription, error)
	GetSubscriptionByID(ctx context.Context, subID string) (*model.UserSubscription, error)
	UpdateUserSubscriptionStatus(ctx context.Context, subID string, status string) error
	RecordSubscriptionOrderTx(ctx context.Context, userID int64, subID, externalRefID string, slotID int, amountCents int64, currency, paymentMethod, fbLeadJSON string, tpOrder *model.ThirdPartyPaymentOrder) error
	RecordSubscriptionRenewalOrderTx(ctx context.Context, sub *model.UserSubscription, externalRefID string, amountCents int64, currency string, tpOrder *model.ThirdPartyPaymentOrder) error
	RecordRechargeOrderAndCreditCoinsTx(ctx context.Context, userID int64, externalRefID string, coinsAmount int, amountCents int64, currency string, paymentMethod string, fbLeadJSON string, tpOrder *model.ThirdPartyPaymentOrder) error
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
	txCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	tx, err := db.DB.Begin(txCtx)
	if err != nil {
		return err
	}
	defer tx.Rollback(txCtx)

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
		SELECT id, template_id, slot_index, type, coins, bonus, COALESCE(vip_duration, ''), COALESCE(subscription_cycle, ''), COALESCE(vip_name, ''), COALESCE(vip_desc, ''), price, price_cents
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
		err := rows.Scan(&s.ID, &s.TemplateID, &s.SlotIndex, &s.Type, &s.Coins, &s.Bonus, &s.VipDuration, &s.SubscriptionCycle, &s.VipName, &s.VipDesc, &s.Price, &s.PriceCents)
		if err != nil {
			return nil, err
		}
		slots = append(slots, s)
	}
	return slots, nil
}

func (r *dbRepository) GetRechargeSlotByID(ctx context.Context, slotID int) (*model.RechargeSlot, error) {
	query := `
		SELECT id, template_id, slot_index, type, coins, bonus, COALESCE(vip_duration, ''), COALESCE(subscription_cycle, ''), COALESCE(vip_name, ''), COALESCE(vip_desc, ''), price, price_cents
		FROM recharge_slots
		WHERE id = $1`
	var s model.RechargeSlot
	err := db.DB.QueryRow(ctx, query, slotID).Scan(&s.ID, &s.TemplateID, &s.SlotIndex, &s.Type, &s.Coins, &s.Bonus, &s.VipDuration, &s.SubscriptionCycle, &s.VipName, &s.VipDesc, &s.Price, &s.PriceCents)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &s, nil
}

func (r *dbRepository) GetProviderPlan(ctx context.Context, provider string, slotID int) (*model.PaymentProviderPlan, error) {
	query := `
		SELECT id, provider, slot_id, cycle, price_cents, currency, external_plan_id, status, COALESCE(raw_payload::text, '{}'), created_at, updated_at
		FROM payment_provider_plans
		WHERE provider = $1 AND slot_id = $2
		LIMIT 1`
	var p model.PaymentProviderPlan
	err := db.DB.QueryRow(ctx, query, provider, slotID).Scan(
		&p.ID, &p.Provider, &p.SlotID, &p.Cycle, &p.PriceCents, &p.Currency, &p.ExternalPlanID, &p.Status, &p.RawPayload, &p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &p, nil
}

func (r *dbRepository) SaveProviderPlan(ctx context.Context, plan *model.PaymentProviderPlan) error {
	query := `
		INSERT INTO payment_provider_plans (provider, slot_id, cycle, price_cents, currency, external_plan_id, status, raw_payload)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb)
		ON CONFLICT (provider, slot_id) DO UPDATE SET
			external_plan_id = EXCLUDED.external_plan_id,
			price_cents = EXCLUDED.price_cents,
			cycle = EXCLUDED.cycle,
			currency = EXCLUDED.currency,
			status = EXCLUDED.status,
			raw_payload = EXCLUDED.raw_payload,
			updated_at = CURRENT_TIMESTAMP
		RETURNING id`
	raw := plan.RawPayload
	if raw == "" {
		raw = "{}"
	}
	if plan.Currency == "" {
		plan.Currency = "USD"
	}
	if plan.Status == "" {
		plan.Status = "ACTIVE"
	}
	return db.DB.QueryRow(ctx, query, plan.Provider, plan.SlotID, plan.Cycle, plan.PriceCents, plan.Currency, plan.ExternalPlanID, plan.Status, raw).Scan(&plan.ID)
}

func (r *dbRepository) CreateUserSubscriptionTx(ctx context.Context, tx pgx.Tx, sub *model.UserSubscription) error {
	query := `
		INSERT INTO user_subscriptions (
			user_id, subscription_id, plan_id, slot_id, template_id, status,
			cycle, price_cents, currency, payment_method, current_period_start, current_period_end,
			next_billing_time, last_payment_time, raw_payload
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15::jsonb)
		ON CONFLICT (subscription_id) DO UPDATE SET
			status = EXCLUDED.status,
			current_period_start = EXCLUDED.current_period_start,
			current_period_end = EXCLUDED.current_period_end,
			next_billing_time = EXCLUDED.next_billing_time,
			last_payment_time = EXCLUDED.last_payment_time,
			raw_payload = EXCLUDED.raw_payload,
			updated_at = CURRENT_TIMESTAMP
		RETURNING id`
	raw := sub.RawPayload
	if raw == "" {
		raw = "{}"
	}
	return tx.QueryRow(ctx, query,
		sub.UserID, sub.SubscriptionID, sub.PlanID, sub.SlotID, sub.TemplateID, sub.Status,
		sub.Cycle, sub.PriceCents, sub.Currency, sub.PaymentMethod, sub.CurrentPeriodStart, sub.CurrentPeriodEnd,
		sub.NextBillingTime, sub.LastPaymentTime, raw,
	).Scan(&sub.ID)
}

func (r *dbRepository) UpsertUserSubscription(ctx context.Context, sub *model.UserSubscription) error {
	query := `
		INSERT INTO user_subscriptions (
			user_id, subscription_id, plan_id, slot_id, template_id, status,
			cycle, price_cents, currency, payment_method, current_period_start, current_period_end,
			next_billing_time, last_payment_time, raw_payload
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15::jsonb)
		ON CONFLICT (subscription_id) DO UPDATE SET
			status = EXCLUDED.status,
			current_period_start = EXCLUDED.current_period_start,
			current_period_end = EXCLUDED.current_period_end,
			next_billing_time = EXCLUDED.next_billing_time,
			last_payment_time = EXCLUDED.last_payment_time,
			raw_payload = EXCLUDED.raw_payload,
			updated_at = CURRENT_TIMESTAMP
		RETURNING id`
	raw := sub.RawPayload
	if raw == "" {
		raw = "{}"
	}
	return db.DB.QueryRow(ctx, query,
		sub.UserID, sub.SubscriptionID, sub.PlanID, sub.SlotID, sub.TemplateID, sub.Status,
		sub.Cycle, sub.PriceCents, sub.Currency, sub.PaymentMethod, sub.CurrentPeriodStart, sub.CurrentPeriodEnd,
		sub.NextBillingTime, sub.LastPaymentTime, raw,
	).Scan(&sub.ID)
}

func (r *dbRepository) GetActiveSubscriptionByUserID(ctx context.Context, userID int64) (*model.UserSubscription, error) {
	query := `
		SELECT id, user_id, subscription_id, COALESCE(plan_id, ''), COALESCE(slot_id, 0), COALESCE(template_id, 0),
		       status, cycle, price_cents, currency, payment_method, current_period_start, current_period_end,
		       next_billing_time, last_payment_time, cancelled_at, COALESCE(raw_payload::text, '{}'), created_at, updated_at
		FROM user_subscriptions
		WHERE user_id = $1 AND status = 'ACTIVE' AND (current_period_end IS NULL OR current_period_end > CURRENT_TIMESTAMP)
		ORDER BY id DESC
		LIMIT 1`
	var s model.UserSubscription
	err := db.DB.QueryRow(ctx, query, userID).Scan(
		&s.ID, &s.UserID, &s.SubscriptionID, &s.PlanID, &s.SlotID, &s.TemplateID,
		&s.Status, &s.Cycle, &s.PriceCents, &s.Currency, &s.PaymentMethod, &s.CurrentPeriodStart, &s.CurrentPeriodEnd,
		&s.NextBillingTime, &s.LastPaymentTime, &s.CancelledAt, &s.RawPayload, &s.CreatedAt, &s.UpdatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &s, nil
}

func (r *dbRepository) GetSubscriptionByID(ctx context.Context, subID string) (*model.UserSubscription, error) {
	query := `
		SELECT id, user_id, subscription_id, COALESCE(plan_id, ''), COALESCE(slot_id, 0), COALESCE(template_id, 0),
		       status, cycle, price_cents, currency, payment_method, current_period_start, current_period_end,
		       next_billing_time, last_payment_time, cancelled_at, COALESCE(raw_payload::text, '{}'), created_at, updated_at
		FROM user_subscriptions
		WHERE subscription_id = $1
		ORDER BY id DESC
		LIMIT 1`
	var s model.UserSubscription
	err := db.DB.QueryRow(ctx, query, subID).Scan(
		&s.ID, &s.UserID, &s.SubscriptionID, &s.PlanID, &s.SlotID, &s.TemplateID,
		&s.Status, &s.Cycle, &s.PriceCents, &s.Currency, &s.PaymentMethod, &s.CurrentPeriodStart, &s.CurrentPeriodEnd,
		&s.NextBillingTime, &s.LastPaymentTime, &s.CancelledAt, &s.RawPayload, &s.CreatedAt, &s.UpdatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &s, nil
}

func (r *dbRepository) UpdateUserSubscriptionStatus(ctx context.Context, subID string, status string) error {
	var cancelQuery string
	if status == "CANCELLED" {
		cancelQuery = ", cancelled_at = CURRENT_TIMESTAMP"
	}
	query := fmt.Sprintf("UPDATE user_subscriptions SET status = $1, updated_at = CURRENT_TIMESTAMP %s WHERE subscription_id = $2", cancelQuery)
	_, err := db.DB.Exec(ctx, query, status, subID)
	return err
}

func (r *dbRepository) RecordSubscriptionOrderTx(
	ctx context.Context,
	userID int64,
	subID, externalRefID string,
	slotID int,
	amountCents int64,
	currency, paymentMethod, fbLeadJSON string,
	tpOrder *model.ThirdPartyPaymentOrder,
) error {
	txCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	tx, err := db.DB.Begin(txCtx)
	if err != nil {
		return err
	}
	defer tx.Rollback(txCtx)

	var utmSource, utmCampaign sql.NullString
	_ = tx.QueryRow(ctx, "SELECT utm_source, utm_campaign FROM users WHERE id = $1", userID).Scan(&utmSource, &utmCampaign)

	var promoID, novelID sql.NullInt64
	if utmSource.Valid && utmSource.String != "" && utmCampaign.Valid && utmCampaign.String != "" {
		_ = tx.QueryRow(ctx, "SELECT id, novel_id FROM promotion_links WHERE utm_source = $1 AND utm_campaign = $2 LIMIT 1", utmSource.String, utmCampaign.String).Scan(&promoID, &novelID)
	}

	var fbLeadVal *string
	if fbLeadJSON != "" {
		fbLeadVal = &fbLeadJSON
	}
	var utmSrcVal, utmCampVal *string
	if utmSource.Valid && utmSource.String != "" {
		utmSrcVal = &utmSource.String
	}
	if utmCampaign.Valid && utmCampaign.String != "" {
		utmCampVal = &utmCampaign.String
	}

	var orderID int64
	checkQuery := `SELECT id FROM recharge_orders WHERE external_ref_id = $1`
	err = tx.QueryRow(ctx, checkQuery, externalRefID).Scan(&orderID)
	if err != nil {
		if err == pgx.ErrNoRows {
			insertOrderQuery := `
				INSERT INTO recharge_orders (
					user_id, external_ref_id, amount_cents, currency, coins,
					bonus_coins_credited, payment_method, status, utm_source, utm_campaign,
					fb_lead_metadata, paid_at, order_type, subscription_id, recharge_slot_id,
					promotion_link_id, novel_id
				)
				VALUES ($1, $2, $3, $4, 0, 0, $5, 'Success', $6, $7, $8, CURRENT_TIMESTAMP, 'subscription', $9, $10, $11, $12)
				RETURNING id`
			err = tx.QueryRow(ctx, insertOrderQuery, userID, externalRefID, amountCents, currency, paymentMethod, utmSrcVal, utmCampVal, fbLeadVal, subID, slotID, promoID, novelID).Scan(&orderID)
			if err != nil {
				return err
			}
		} else {
			return err
		}
	} else {
		_, err = tx.Exec(ctx, "UPDATE recharge_orders SET status = 'Success', paid_at = CURRENT_TIMESTAMP, subscription_id = $1, updated_at = CURRENT_TIMESTAMP WHERE id = $2", subID, orderID)
		if err != nil {
			return err
		}
	}

	if tpOrder != nil && orderID > 0 {
		insertTPQuery := `
			INSERT INTO third_party_payment_orders (
				order_id, payment_provider, external_order_id, capture_id,
				payer_id, payer_email, payer_name, payer_country,
				currency, gross_amount, fee_amount, net_amount,
				status, seller_protection_status, raw_payload
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15::jsonb)
			ON CONFLICT (payment_provider, external_order_id, capture_id) DO UPDATE SET
				status = EXCLUDED.status,
				fee_amount = EXCLUDED.fee_amount,
				net_amount = EXCLUDED.net_amount,
				raw_payload = EXCLUDED.raw_payload,
				updated_at = CURRENT_TIMESTAMP`
		rawPayload := tpOrder.RawPayload
		if rawPayload == "" {
			rawPayload = "{}"
		}
		_, _ = tx.Exec(ctx, insertTPQuery,
			orderID, tpOrder.PaymentProvider, tpOrder.ExternalOrderID, tpOrder.CaptureID,
			tpOrder.PayerID, tpOrder.PayerEmail, tpOrder.PayerName, tpOrder.PayerCountry,
			tpOrder.Currency, tpOrder.GrossAmount, tpOrder.FeeAmount, tpOrder.NetAmount,
			tpOrder.Status, tpOrder.SellerProtectionStatus, rawPayload,
		)
	}

	return tx.Commit(ctx)
}

func (r *dbRepository) RecordSubscriptionRenewalOrderTx(
	ctx context.Context,
	sub *model.UserSubscription,
	externalRefID string,
	amountCents int64,
	currency string,
	tpOrder *model.ThirdPartyPaymentOrder,
) error {
	txCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	tx, err := db.DB.Begin(txCtx)
	if err != nil {
		return err
	}
	defer tx.Rollback(txCtx)

	var exists bool
	_ = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM recharge_orders WHERE external_ref_id = $1)", externalRefID).Scan(&exists)
	if exists {
		return nil
	}

	var orderID int64
	insertOrderQuery := `
		INSERT INTO recharge_orders (
			user_id, external_ref_id, amount_cents, currency, coins,
			bonus_coins_credited, payment_method, status, paid_at,
			order_type, subscription_id, recharge_slot_id
		)
		VALUES ($1, $2, $3, $4, 0, 0, $5, 'Success', CURRENT_TIMESTAMP, 'subscription', $6, $7)
		RETURNING id`
	err = tx.QueryRow(ctx, insertOrderQuery, sub.UserID, externalRefID, amountCents, currency, sub.PaymentMethod, sub.SubscriptionID, sub.SlotID).Scan(&orderID)
	if err != nil {
		return err
	}

	now := time.Now()
	var newPeriodEnd time.Time
	switch sub.Cycle {
	case "day":
		newPeriodEnd = now.Add(24 * time.Hour)
	case "week":
		newPeriodEnd = now.Add(7 * 24 * time.Hour)
	case "month":
		newPeriodEnd = now.Add(30 * 24 * time.Hour)
	default:
		newPeriodEnd = now.Add(30 * 24 * time.Hour)
	}

	updateSubQuery := `
		UPDATE user_subscriptions
		SET status = 'ACTIVE', last_payment_time = $1, current_period_end = $2, next_billing_time = $2, updated_at = CURRENT_TIMESTAMP
		WHERE subscription_id = $3`
	_, err = tx.Exec(ctx, updateSubQuery, now, newPeriodEnd, sub.SubscriptionID)
	if err != nil {
		return err
	}

	if tpOrder != nil && orderID > 0 {
		insertTPQuery := `
			INSERT INTO third_party_payment_orders (
				order_id, payment_provider, external_order_id, capture_id,
				payer_id, payer_email, payer_name, payer_country,
				currency, gross_amount, fee_amount, net_amount,
				status, seller_protection_status, raw_payload
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15::jsonb)
			ON CONFLICT (payment_provider, external_order_id, capture_id) DO NOTHING`
		rawPayload := tpOrder.RawPayload
		if rawPayload == "" {
			rawPayload = "{}"
		}
		_, _ = tx.Exec(ctx, insertTPQuery,
			orderID, tpOrder.PaymentProvider, tpOrder.ExternalOrderID, tpOrder.CaptureID,
			tpOrder.PayerID, tpOrder.PayerEmail, tpOrder.PayerName, tpOrder.PayerCountry,
			tpOrder.Currency, tpOrder.GrossAmount, tpOrder.FeeAmount, tpOrder.NetAmount,
			tpOrder.Status, tpOrder.SellerProtectionStatus, rawPayload,
		)
	}

	return tx.Commit(ctx)
}


func (r *dbRepository) RecordRechargeOrderAndCreditCoinsTx(
	ctx context.Context,
	userID int64,
	externalRefID string,
	coinsAmount int,
	amountCents int64,
	currency string,
	paymentMethod string,
	fbLeadJSON string,
	tpOrder *model.ThirdPartyPaymentOrder,
) error {
	txCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	tx, err := db.DB.Begin(txCtx)
	if err != nil {
		return err
	}
	defer tx.Rollback(txCtx)

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
	var orderID int64
	var status string
	checkQuery := `SELECT id, status FROM recharge_orders WHERE external_ref_id = $1`
	err = tx.QueryRow(ctx, checkQuery, externalRefID).Scan(&orderID, &status)
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
				VALUES ($1, $2, $3, $4, $5, $6, $7, 'Success', $8, $9, $10, CURRENT_TIMESTAMP, $11, $12, $13)
				RETURNING id`
			err = tx.QueryRow(ctx, insertOrderQuery, userID, externalRefID, finalAmountCents, currency, slotCoins, slotBonus, paymentMethod, utmSrcVal, utmCampVal, fbLeadVal, slotType, promotionLinkID, novelID).Scan(&orderID)
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
			WHERE external_ref_id = $2
			RETURNING id`
		err = tx.QueryRow(ctx, updateOrderQuery, fbLeadVal, externalRefID).Scan(&orderID)
		if err != nil {
			return err
		}
	}

	// 3b. Record third-party payment transaction details
	if tpOrder != nil && orderID > 0 {
		insertTPQuery := `
			INSERT INTO third_party_payment_orders (
				order_id, payment_provider, external_order_id, capture_id,
				payer_id, payer_email, payer_name, payer_country,
				currency, gross_amount, fee_amount, net_amount,
				status, seller_protection_status, raw_payload
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15::jsonb)
			ON CONFLICT (payment_provider, external_order_id, capture_id) DO UPDATE SET
				status = EXCLUDED.status,
				fee_amount = EXCLUDED.fee_amount,
				net_amount = EXCLUDED.net_amount,
				raw_payload = EXCLUDED.raw_payload,
				updated_at = CURRENT_TIMESTAMP`
		rawPayload := tpOrder.RawPayload
		if rawPayload == "" {
			rawPayload = "{}"
		}
		_, err = tx.Exec(ctx, insertTPQuery,
			orderID, tpOrder.PaymentProvider, tpOrder.ExternalOrderID, tpOrder.CaptureID,
			tpOrder.PayerID, tpOrder.PayerEmail, tpOrder.PayerName, tpOrder.PayerCountry,
			tpOrder.Currency, tpOrder.GrossAmount, tpOrder.FeeAmount, tpOrder.NetAmount,
			tpOrder.Status, tpOrder.SellerProtectionStatus, rawPayload,
		)
		if err != nil {
			log.Printf("[Warning] Failed to insert third_party_payment_orders: %v", err)
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
