package billing

import (
	"context"
	"strconv"

	"github.com/jackc/pgx/v5"
	"star-novel-cms/internal/db"
)

type BillingRepository interface {
	BeginTx(ctx context.Context) (pgx.Tx, error)
	CountOrders(ctx context.Context, countQuery string, args []interface{}) (int, error)
	ListOrders(ctx context.Context, selectQuery string, args []interface{}) ([]Order, error)

	GetOrderForUpdateTx(ctx context.Context, tx pgx.Tx, orderID string) (*Order, error)
	UpdateOrderStatusTx(ctx context.Context, tx pgx.Tx, orderID, status string) error
	DeductWalletBalanceTx(ctx context.Context, tx pgx.Tx, userID string, chargedAmt, bonusAmt int) error
	InsertTransactionRecordTx(ctx context.Context, tx pgx.Tx, id, userID, txType, bizType string, amount, chargedAmt, bonusAmt int, desc string) error
	InsertAdminAuditLogTx(ctx context.Context, tx pgx.Tx, adminID, action, targetID, beforeData, afterData string) error

	GetFirstPaidOrderIDByUserIDTx(ctx context.Context, tx pgx.Tx, userID string) (string, error)
	CreateOrderTx(ctx context.Context, tx pgx.Tx, o *Order) error
	UpsertWalletBalanceTx(ctx context.Context, tx pgx.Tx, userID string, chargedAmt, bonusAmt int) error

	ListRechargeTemplates(ctx context.Context) ([]RechargeTemplate, error)
	GetRechargeSlots(ctx context.Context, templateID int) ([]RechargeSlot, error)
	GetTemplateIsDefault(ctx context.Context, templateID int) (bool, error)

	ClearDefaultTemplatesTx(ctx context.Context, tx pgx.Tx) error
	CreateRechargeTemplateTx(ctx context.Context, tx pgx.Tx, name string, isDefault bool) (int, error)
	CreateRechargeSlotTx(ctx context.Context, tx pgx.Tx, templateID, index int, slotType string, coins, bonus int, duration, vipName, vipDesc, price string, priceCents int) error

	UpdateRechargeTemplateTx(ctx context.Context, tx pgx.Tx, id int, name string, isDefault bool) (int64, error)
	SetDefaultTemplateOnlyTx(ctx context.Context, tx pgx.Tx, id int) (int64, error)
	DeleteRechargeSlotsTx(ctx context.Context, tx pgx.Tx, templateID int) error

	DeleteRechargeTemplate(ctx context.Context, id int) (int64, error)
}

type dbBillingRepository struct{}

func NewBillingRepository() BillingRepository {
	return &dbBillingRepository{}
}

func (r *dbBillingRepository) BeginTx(ctx context.Context) (pgx.Tx, error) {
	return db.DB.Begin(ctx)
}

func (r *dbBillingRepository) CountOrders(ctx context.Context, countQuery string, args []interface{}) (int, error) {
	var total int
	err := db.DB.QueryRow(ctx, countQuery, args...).Scan(&total)
	return total, err
}

func (r *dbBillingRepository) ListOrders(ctx context.Context, selectQuery string, args []interface{}) ([]Order, error) {
	rows, err := db.DB.Query(ctx, selectQuery, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var orders []Order
	for rows.Next() {
		var o Order
		err := rows.Scan(
			&o.ID, &o.UserID, &o.ExternalRefID, &o.AmountCents, &o.Currency,
			&o.Coins, &o.BonusCoinsCredited, &o.PaymentMethod,
			&o.Status, &o.UtmSource, &o.UtmCampaign, &o.PaidAt, &o.OrderType,
			&o.PromotionLinkID, &o.NovelID, &o.CreatedAt, &o.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		orders = append(orders, o)
	}
	return orders, nil
}

func (r *dbBillingRepository) GetOrderForUpdateTx(ctx context.Context, tx pgx.Tx, orderID string) (*Order, error) {
	orderIDVal, err := strconv.ParseInt(orderID, 10, 64)
	if err != nil {
		return nil, err
	}

	query := `
		SELECT user_id, status, coins, bonus_coins_credited
		FROM recharge_orders
		WHERE id = $1 FOR UPDATE`
	var o Order
	o.ID = orderIDVal
	err = tx.QueryRow(ctx, query, orderIDVal).Scan(&o.UserID, &o.Status, &o.Coins, &o.BonusCoinsCredited)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &o, nil
}

func (r *dbBillingRepository) UpdateOrderStatusTx(ctx context.Context, tx pgx.Tx, orderID, status string) error {
	orderIDVal, err := strconv.ParseInt(orderID, 10, 64)
	if err != nil {
		return err
	}

	query := `
		UPDATE recharge_orders
		SET status = $1, updated_at = CURRENT_TIMESTAMP
		WHERE id = $2`
	_, err = tx.Exec(ctx, query, status, orderIDVal)
	return err
}

func (r *dbBillingRepository) DeductWalletBalanceTx(ctx context.Context, tx pgx.Tx, userID string, chargedAmt, bonusAmt int) error {
	query := `
		UPDATE wallets
		SET charged_coins = charged_coins - $1, bonus_coins = bonus_coins - $2, updated_at = CURRENT_TIMESTAMP
		WHERE user_id = $3`
	_, err := tx.Exec(ctx, query, chargedAmt, bonusAmt, userID)
	return err
}

func (r *dbBillingRepository) InsertTransactionRecordTx(ctx context.Context, tx pgx.Tx, id, userID, txType, bizType string, amount, chargedAmt, bonusAmt int, desc string) error {
	query := `
		INSERT INTO transactions (id, user_id, type, biz_type, amount, charged_amount, bonus_amount, description)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`
	_, err := tx.Exec(ctx, query, id, userID, txType, bizType, amount, chargedAmt, bonusAmt, desc)
	return err
}

func (r *dbBillingRepository) InsertAdminAuditLogTx(ctx context.Context, tx pgx.Tx, adminID, action, targetID, beforeData, afterData string) error {
	query := `
		INSERT INTO admin_audit_logs (admin_id, action, target_id, before_data, after_data)
		VALUES ($1, $2, $3, $4, $5)`
	_, err := tx.Exec(ctx, query, adminID, action, targetID, beforeData, afterData)
	return err
}

func (r *dbBillingRepository) GetFirstPaidOrderIDByUserIDTx(ctx context.Context, tx pgx.Tx, userID string) (string, error) {
	var orderIDVal int64
	err := tx.QueryRow(ctx, "SELECT id FROM recharge_orders WHERE user_id = $1 AND status = 'Success' LIMIT 1", userID).Scan(&orderIDVal)
	if err != nil {
		if err == pgx.ErrNoRows {
			return "", nil
		}
		return "", err
	}
	return strconv.FormatInt(orderIDVal, 10), nil
}

func (r *dbBillingRepository) CreateOrderTx(ctx context.Context, tx pgx.Tx, o *Order) error {
	query := `
		INSERT INTO recharge_orders (user_id, external_ref_id, amount_cents, currency, coins, bonus_coins_credited, payment_method, status, utm_source, utm_campaign, paid_at, order_type, promotion_link_id, novel_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		RETURNING id`
	err := tx.QueryRow(ctx, query, o.UserID, o.ExternalRefID, o.AmountCents, o.Currency, o.Coins, o.BonusCoinsCredited, o.PaymentMethod, o.Status, o.UtmSource, o.UtmCampaign, o.PaidAt, o.OrderType, o.PromotionLinkID, o.NovelID).Scan(&o.ID)
	return err
}

func (r *dbBillingRepository) UpsertWalletBalanceTx(ctx context.Context, tx pgx.Tx, userID string, chargedAmt, bonusAmt int) error {
	query := `
		INSERT INTO wallets (user_id, charged_coins, bonus_coins, updated_at)
		VALUES ($1, $2, $3, CURRENT_TIMESTAMP)
		ON CONFLICT (user_id) DO UPDATE
		SET charged_coins = wallets.charged_coins + EXCLUDED.charged_coins,
		    bonus_coins = wallets.bonus_coins + EXCLUDED.bonus_coins,
		    updated_at = CURRENT_TIMESTAMP`
	_, err := tx.Exec(ctx, query, userID, chargedAmt, bonusAmt)
	return err
}

func (r *dbBillingRepository) ListRechargeTemplates(ctx context.Context) ([]RechargeTemplate, error) {
	rows, err := db.DB.Query(ctx, `
		SELECT id, name, is_default, created_at, updated_at
		FROM recharge_templates
		ORDER BY id DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var templates []RechargeTemplate
	for rows.Next() {
		var t RechargeTemplate
		err := rows.Scan(&t.ID, &t.Name, &t.IsDefault, &t.CreatedAt, &t.UpdatedAt)
		if err != nil {
			return nil, err
		}
		templates = append(templates, t)
	}
	return templates, nil
}

func (r *dbBillingRepository) GetRechargeSlots(ctx context.Context, templateID int) ([]RechargeSlot, error) {
	rows, err := db.DB.Query(ctx, `
		SELECT id, template_id, slot_index, type, coins, bonus, COALESCE(vip_duration, ''), COALESCE(vip_name, ''), COALESCE(vip_desc, ''), price, price_cents
		FROM recharge_slots
		WHERE template_id = $1
		ORDER BY slot_index ASC
	`, templateID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var slots []RechargeSlot
	for rows.Next() {
		var s RechargeSlot
		err := rows.Scan(&s.ID, &s.TemplateID, &s.SlotIndex, &s.Type, &s.Coins, &s.Bonus, &s.VipDuration, &s.VipName, &s.VipDesc, &s.Price, &s.PriceCents)
		if err != nil {
			return nil, err
		}
		slots = append(slots, s)
	}
	return slots, nil
}

func (r *dbBillingRepository) GetTemplateIsDefault(ctx context.Context, templateID int) (bool, error) {
	var isDefault bool
	err := db.DB.QueryRow(ctx, "SELECT is_default FROM recharge_templates WHERE id = $1", templateID).Scan(&isDefault)
	return isDefault, err
}

func (r *dbBillingRepository) ClearDefaultTemplatesTx(ctx context.Context, tx pgx.Tx) error {
	_, _ = tx.Exec(ctx, "SELECT id FROM recharge_templates FOR UPDATE")
	_, err := tx.Exec(ctx, "UPDATE recharge_templates SET is_default = FALSE")
	return err
}

func (r *dbBillingRepository) CreateRechargeTemplateTx(ctx context.Context, tx pgx.Tx, name string, isDefault bool) (int, error) {
	var templateID int
	query := `
		INSERT INTO recharge_templates (name, is_default)
		VALUES ($1, $2)
		RETURNING id`
	err := tx.QueryRow(ctx, query, name, isDefault).Scan(&templateID)
	return templateID, err
}

func (r *dbBillingRepository) CreateRechargeSlotTx(ctx context.Context, tx pgx.Tx, templateID, index int, slotType string, coins, bonus int, duration, vipName, vipDesc, price string, priceCents int) error {
	query := `
		INSERT INTO recharge_slots (template_id, slot_index, type, coins, bonus, vip_duration, vip_name, vip_desc, price, price_cents)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`
	_, err := tx.Exec(ctx, query, templateID, index, slotType, coins, bonus, duration, vipName, vipDesc, price, priceCents)
	return err
}

func (r *dbBillingRepository) UpdateRechargeTemplateTx(ctx context.Context, tx pgx.Tx, id int, name string, isDefault bool) (int64, error) {
	query := `
		UPDATE recharge_templates
		SET name = $1, is_default = $2, updated_at = CURRENT_TIMESTAMP
		WHERE id = $3`
	tag, err := tx.Exec(ctx, query, name, isDefault, id)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (r *dbBillingRepository) DeleteRechargeSlotsTx(ctx context.Context, tx pgx.Tx, templateID int) error {
	_, err := tx.Exec(ctx, "DELETE FROM recharge_slots WHERE template_id = $1", templateID)
	return err
}

func (r *dbBillingRepository) SetDefaultTemplateOnlyTx(ctx context.Context, tx pgx.Tx, id int) (int64, error) {
	tag, err := tx.Exec(ctx, "UPDATE recharge_templates SET is_default = TRUE, updated_at = CURRENT_TIMESTAMP WHERE id = $1", id)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (r *dbBillingRepository) DeleteRechargeTemplate(ctx context.Context, id int) (int64, error) {
	tag, err := db.DB.Exec(ctx, "DELETE FROM recharge_templates WHERE id = $1", id)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
