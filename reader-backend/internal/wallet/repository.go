package wallet

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"reader-backend/internal/db"
	"reader-backend/internal/model"
	"reader-backend/internal/novel"
	"reader-backend/internal/payment"
)

var (
	ErrInsufficientBalance = errors.New("insufficient balance")
	ErrAlreadyUnlocked     = errors.New("chapter is already unlocked")
)

type Repository interface {
	CompletePayPalRefund(context.Context, string, string, int64, string) error
	enqueuePayPalEvent(context.Context, *payment.WebhookEventResult) error
	enqueuePayPalCancellation(context.Context, string, int64, string) error
	savePayPalBillingSnapshot(context.Context, *payment.SubscriptionDetails) error
	getPayPalBillingSnapshot(context.Context, string, time.Time, string, int64) (time.Time, error)
	GetWallet(ctx context.Context, userID int64) (*model.Wallet, error)
	GetTransactions(ctx context.Context, userID int64, limit, offset int) ([]model.Transaction, error)
	UnlockChapterTx(ctx context.Context, userID int64, novelID int64, chapterIndex int, price int, chapterTitle string) error
	AddCoins(ctx context.Context, userID int64, amount int, isBonus bool, bizType string, description string) error
	CheckIn(context.Context, int64, bool) (*CheckInState, error)
	GetUserEmail(ctx context.Context, userID int64) (string, error)
	GetRechargeTemplateByID(ctx context.Context, templateID int) (*model.RechargeTemplate, error)
	GetDefaultRechargeTemplate(ctx context.Context) (*model.RechargeTemplate, error)
	GetFirstRechargeTemplate(ctx context.Context) (*model.RechargeTemplate, error)
	GetRechargeSlots(ctx context.Context, templateID int) ([]model.RechargeSlot, error)
	GetRechargeSlotByID(ctx context.Context, slotID int) (*model.RechargeSlot, error)
	GetProviderPlan(ctx context.Context, provider string, slotID int) (*model.PaymentProviderPlan, error)
	SaveProviderPlan(ctx context.Context, plan *model.PaymentProviderPlan) error
	UpsertUserSubscription(ctx context.Context, sub *model.UserSubscription) error
	reserveSubscriptionIntent(context.Context, subscriptionIntent) (*subscriptionIntent, error)
	releaseSubscriptionIntent(context.Context, string)
	submitSubscriptionIntent(context.Context, string, string) error
	finishSubscriptionIntent(context.Context, *subscriptionIntent, *payment.SubscriptionResult) error
	GetActiveSubscriptionByUserID(ctx context.Context, userID int64) (*model.UserSubscription, error)
	GetSubscriptionByID(ctx context.Context, subID string) (*model.UserSubscription, error)
	UpdateUserSubscriptionStatus(ctx context.Context, subID string, status string) error
	RecordSubscriptionRenewalOrderTx(ctx context.Context, sub *model.UserSubscription, externalRefID string, amountCents int64, currency string, paidAt, periodEnd time.Time, providerStatus string, tpOrder *model.ThirdPartyPaymentOrder) (bool, error)
	GetCheckoutTemplate(context.Context, int64) (int, error)
	WholeBookAvailable(context.Context, int64, int, int64) (bool, error)
	CreateCheckout(context.Context, int64, *model.RechargeSlot, int64, checkoutAttribution) (string, error)
	BindCheckout(context.Context, string, string) error
	findCheckout(context.Context, string) (*checkoutOrder, error)
	flagCheckoutForReview(context.Context, int64, string) error
	settleCheckout(context.Context, string, *payment.PayPalCaptureResult) (*CheckoutResult, error)
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
	err = tx.QueryRow(txCtx, walletQuery, userID).Scan(&chargedCoins, &bonusCoins)
	if err != nil {
		return err
	}

	// 2. Check if already unlocked inside the locked transaction
	var exists bool
	checkQuery := `SELECT EXISTS(SELECT 1 FROM unlock_records WHERE user_id = $1 AND novel_id = $2 AND chapter_index = $3)`
	err = tx.QueryRow(txCtx, checkQuery, userID, novelID, chapterIndex).Scan(&exists)
	if err != nil {
		return err
	}
	if exists {
		return ErrAlreadyUnlocked
	}

	err = tx.QueryRow(txCtx, `SELECT EXISTS(SELECT 1 FROM user_subscriptions WHERE user_id=$1 AND `+novel.PaidSubscriptionPredicate+`)
		OR EXISTS(SELECT 1 FROM recharge_orders WHERE user_id=$1 AND novel_id=$2 AND `+novel.WholeBookEntitlementPredicate+`)`,
		userID, novelID).Scan(&exists)
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
		INSERT INTO unlock_records (user_id, novel_id, chapter_index)
		VALUES ($1, $2, $3)`
	_, err = tx.Exec(txCtx, insertUnlockQuery, userID, novelID, chapterIndex)
	if err != nil {
		return err
	}

	return tx.Commit(txCtx)
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
		WHERE user_id = $1 AND ` + novel.PaidSubscriptionPredicate + `
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
		cancelQuery = ", cancelled_at = COALESCE(cancelled_at, CURRENT_TIMESTAMP), next_billing_time = NULL, cancel_next_attempt_at=NULL, cancel_last_error=NULL"
	}
	query := fmt.Sprintf("UPDATE user_subscriptions SET status = $1, updated_at = CURRENT_TIMESTAMP %s WHERE subscription_id = $2 AND (status <> 'CANCELLED' OR $1='CANCELLED')  AND (status <> 'CANCEL_PENDING' OR $1 IN ('CANCELLED','EXPIRED'))", cancelQuery)
	_, err := db.DB.Exec(ctx, query, status, subID)
	return err
}

func (r *dbRepository) RecordSubscriptionRenewalOrderTx(
	ctx context.Context, sub *model.UserSubscription, externalRefID string, amountCents int64,
	currency string, paidAt, periodEnd time.Time, providerStatus string, tpOrder *model.ThirdPartyPaymentOrder,
) (bool, error) {
	tx, err := db.DB.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if err := lockPayPalCapture(ctx, tx, externalRefID); err != nil {
		return false, err
	}

	var confirmedEnd *time.Time
	if !periodEnd.IsZero() {
		confirmedEnd = &periodEnd
	}
	reportedRefund := tpOrder.Status == "REFUNDED"
	var orderID int64
	err = tx.QueryRow(ctx, `INSERT INTO recharge_orders
		(user_id, external_ref_id, amount_cents,currency,coins,bonus_coins_credited,payment_method,status,
		paid_at, order_type, subscription_id, recharge_slot_id, recharge_template_id, fulfilled_at, paid_period_start, paid_period_end,
		utm_source, utm_campaign, promotion_link_id, novel_id, reconciliation_note, paypal_refund_reported)
		SELECT u.id,$2,$3,$4,0,0,$5,'Success',$6,'subscription',$7,$8,
			(SELECT id FROM recharge_templates WHERE id=$9),CASE WHEN $10::timestamptz IS NOT NULL THEN now() END,CASE WHEN $10::timestamptz IS NOT NULL THEN $6::timestamptz END,$10,u.utm_source,u.utm_campaign,p.id,p.novel_id,
 CASE WHEN $11 THEN 'PayPal reports refunded sale; match refund ID'
 WHEN $10::timestamptz IS NULL THEN 'PayPal settled amount verified; billing period needs review' END,$11
		FROM users u LEFT JOIN LATERAL (
			SELECT id,novel_id FROM promotion_links WHERE utm_source=u.utm_source AND utm_campaign=u.utm_campaign
			AND u.utm_source <> '' AND u.utm_campaign <> '' ORDER BY id DESC LIMIT 1
		) p ON TRUE WHERE u.id=$1
		ON CONFLICT(external_ref_id) DO UPDATE SET
			paid_period_end=COALESCE(EXCLUDED.paid_period_end,recharge_orders.paid_period_end),
 paid_period_start=COALESCE(EXCLUDED.paid_period_start,recharge_orders.paid_period_start),
			fulfilled_at=COALESCE(recharge_orders.fulfilled_at,EXCLUDED.fulfilled_at),
			paypal_refund_reported=EXCLUDED.paypal_refund_reported,
			reconciliation_note=EXCLUDED.reconciliation_note
		WHERE ((recharge_orders.paid_period_end IS NULL AND EXCLUDED.paid_period_end IS NOT NULL
			AND NOT recharge_orders.paypal_refund_reported) OR EXCLUDED.paypal_refund_reported)
			AND recharge_orders.status IN ('Success','Paid') AND recharge_orders.user_id=EXCLUDED.user_id
			AND recharge_orders.subscription_id=EXCLUDED.subscription_id AND recharge_orders.payment_method=EXCLUDED.payment_method
			AND recharge_orders.amount_cents=EXCLUDED.amount_cents AND recharge_orders.currency=EXCLUDED.currency
		RETURNING id`,
		sub.UserID, externalRefID, amountCents, currency, sub.PaymentMethod, paidAt, sub.SubscriptionID, sub.SlotID, sub.TemplateID, confirmedEnd, reportedRefund).Scan(&orderID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO third_party_payment_orders
		(order_id,payment_provider,external_order_id,capture_id,payer_id,payer_email,payer_name,currency,gross_amount,net_amount,status,raw_payload)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9::bigint::numeric/100,$9::bigint::numeric/100,'VERIFIED',$10::jsonb)
 ON CONFLICT(payment_provider,external_order_id,capture_id) DO UPDATE SET status='VERIFIED',raw_payload=EXCLUDED.raw_payload
 WHERE third_party_payment_orders.order_id=EXCLUDED.order_id`,
		orderID, sub.PaymentMethod, sub.SubscriptionID, externalRefID, tpOrder.PayerID, tpOrder.PayerEmail, tpOrder.PayerName,
		currency, amountCents, tpOrder.RawPayload)
	if err != nil {
		return false, err
	}
	var priorPeriod bool
	err = tx.QueryRow(ctx, `SELECT paid_period_end IS NOT NULL FROM recharge_orders WHERE id=$1`, orderID).Scan(&priorPeriod)
	if err != nil {
		return false, err
	}
	refunded := false
	if !priorPeriod || (!reportedRefund && !periodEnd.IsZero()) {
		refunded, err = applyRefundBeforeFulfillment(ctx, tx, orderID, externalRefID, amountCents, currency)
	}
	if err != nil {
		return false, err
	}
	if refunded {
		return false, tx.Commit(ctx)
	}
	var firstPayment bool
	err = tx.QueryRow(ctx, `SELECT last_payment_time IS NULL FROM user_subscriptions WHERE subscription_id=$1 FOR UPDATE`,
		sub.SubscriptionID).Scan(&firstPayment)
	if err != nil {
		return false, err
	}

	if reportedRefund {
		_, err = tx.Exec(ctx, `UPDATE user_subscriptions SET current_period_end=(
			SELECT max(paid_period_end) FROM recharge_orders WHERE subscription_id=$1
			AND status IN ('Success','Paid','RefundPending') AND NOT paypal_refund_reported
		),updated_at=now()
		WHERE subscription_id=$1 AND current_period_end<=(
			SELECT paid_period_end FROM recharge_orders WHERE id=$2)`, sub.SubscriptionID, orderID)
		if err != nil {
			return false, err
		}
		return false, tx.Commit(ctx)
	}

	if periodEnd.IsZero() {
		return false, tx.Commit(ctx)
	}

	_, err = tx.Exec(ctx, `UPDATE user_subscriptions SET
		status=CASE WHEN status IN ('CANCELLED','CANCEL_PENDING') THEN status ELSE $4 END,
		last_payment_time=GREATEST(last_payment_time,$2), current_period_end=GREATEST(current_period_end,$3),
		next_billing_time=CASE WHEN status IN ('CANCELLED','CANCEL_PENDING') OR $4 <> 'ACTIVE' THEN NULL ELSE GREATEST(next_billing_time,$3) END,
		updated_at=now()
		WHERE subscription_id=$1`,
		sub.SubscriptionID, paidAt, periodEnd, providerStatus)
	if err != nil {
		return false, err
	}
	return firstPayment, tx.Commit(ctx)
}

func (r *dbRepository) CompletePayPalRefund(ctx context.Context, refundID, captureID string, amount int64, currency string) error {
	tx, err := db.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))

	if captureID != "" {
		if err := lockPayPalCapture(ctx, tx, captureID); err != nil {
			return err
		}
	}

	var orderID int64
	var status, observedCapture string
	err = tx.QueryRow(ctx, `SELECT o.id,o.status,p.capture_id FROM recharge_orders o
		JOIN third_party_payment_orders p ON p.order_id=o.id AND p.payment_provider='paypal'
		LEFT JOIN payment_refunds r ON r.order_id=o.id
		WHERE o.payment_method='paypal' AND $3>0 AND o.amount_cents >= $3 AND o.currency=$4
		AND (($2<>'' AND p.capture_id=$2 AND p.status='VERIFIED') OR
			(r.provider_refund_id=$1 AND ($2='' OR p.capture_id=$2) AND p.status IN ('VERIFIED','COMPLETED')) OR
			($2<>'' AND p.capture_id=$2 AND o.status='RefundPending' AND p.status='COMPLETED'))
		ORDER BY p.id DESC LIMIT 1`, refundID, captureID, amount, currency).Scan(&orderID, &status, &observedCapture)
	if errors.Is(err, pgx.ErrNoRows) {
		return errRefundUnmatched
	}
	if err != nil {
		return err
	}
	if status == "Pending" {
		return errRefundUnmatched
	}
	_, err = tx.Exec(ctx, `SELECT settle_paypal_refund_item($1,$2,$3,$4,$5)`, orderID, refundID, observedCapture, amount, currency)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *dbRepository) enqueuePayPalCancellation(ctx context.Context, id string, userID int64, reason string) error {
	_, err := db.DB.Exec(ctx, `UPDATE user_subscriptions SET status='CANCEL_PENDING',
		cancel_reason=COALESCE(cancel_reason,NULLIF($3,'')),cancel_next_attempt_at=COALESCE(cancel_next_attempt_at,now()),updated_at=now()
		WHERE subscription_id=$1 AND user_id=$2 AND status NOT IN ('CANCELLED','EXPIRED')`, id, userID, reason)
	return err
}

func (r *dbRepository) WholeBookAvailable(ctx context.Context, userID int64, templateID int, novelID int64) (bool, error) {
	var allowed bool
	err := db.DB.QueryRow(ctx, `SELECT whole_book_offer_available($1,$2,$3)`, userID, templateID, novelID).Scan(&allowed)
	return allowed, err
}
