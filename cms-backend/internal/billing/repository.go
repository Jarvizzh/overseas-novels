package billing

import (
	"context"
	"strconv"

	"github.com/jackc/pgx/v5"
	"star-novel-cms/internal/db"
)

type BillingRepository interface {
	ListLedgerMovements(context.Context, movementFilter) (movementPage, error)
	ListSettlementPayouts(context.Context, payoutFilter) (payoutPage, error)
	ListSettlementReportIssues(context.Context, reconciliationPage) (reportIssuePage, error)
	GetPaymentActivity(context.Context, reconciliationScope) (paymentActivity, error)

	BeginTx(ctx context.Context) (pgx.Tx, error)
	CountOrders(ctx context.Context, countQuery string, args []interface{}) (int, error)
	ListOrders(ctx context.Context, selectQuery string, args []interface{}) ([]Order, error)

	GetOrderForUpdateTx(ctx context.Context, tx pgx.Tx, orderID string) (*Order, error)
	UpdateOrderStatusTx(ctx context.Context, tx pgx.Tx, orderID, status string) error
	InsertAdminAuditLogTx(ctx context.Context, tx pgx.Tx, adminID, action, targetID, beforeData, afterData string) error

	GetThirdPartyPaymentDetails(ctx context.Context, orderID int64) (*ThirdPartyPaymentOrder, error)

	ListRechargeTemplates(ctx context.Context) ([]RechargeTemplate, error)
	ListVIPOfferPublications(ctx context.Context) ([]VIPOfferPublication, error)
	GetRechargeSlots(ctx context.Context, templateID int) ([]RechargeSlot, error)

	ClearDefaultTemplatesTx(ctx context.Context, tx pgx.Tx) error
	CreateRechargeTemplateTx(ctx context.Context, tx pgx.Tx, name string, isDefault bool) (int, error)
	CreateRechargeSlotTx(ctx context.Context, tx pgx.Tx, templateID, index int, slotType string, coins, bonus int, duration, cycle, vipName, vipDesc, price string, priceCents int) error

	UpdateRechargeTemplateTx(ctx context.Context, tx pgx.Tx, id int, name string, isDefault bool) (int64, error)
	SetDefaultTemplateOnlyTx(ctx context.Context, tx pgx.Tx, id int) (int64, error)
	DeleteRechargeSlotsTx(ctx context.Context, tx pgx.Tx, templateID int) error
}

type dbBillingRepository struct{}

func NewBillingRepository() BillingRepository {
	return &dbBillingRepository{}
}

func (r *dbBillingRepository) BeginTx(ctx context.Context) (pgx.Tx, error) {
	return db.DB.Begin(ctx)
}

func (r *dbBillingRepository) ListVIPOfferPublications(ctx context.Context) ([]VIPOfferPublication, error) {
	rows, err := db.DB.Query(ctx, `SELECT COALESCE(a.id,0),CASE WHEN a.id IS NULL THEN '' WHEN length(a.account_key)>4
		THEN '••••'||right(a.account_key,4) ELSE '••••' END,COALESCE(a.environment,''),
		v.slot_id,t.name,v.name,v.amount_minor,v.currency,v.cycle,
		COALESCE(p.plan_id,''),COALESCE(p.request_key::text,''),CASE
			WHEN a.id IS NULL THEN 'no_account'
			WHEN p.plan_id IS NOT NULL THEN 'ready'
			WHEN p.review_required THEN 'review'
			WHEN p.first_sent_at IS NOT NULL THEN 'publishing'
			ELSE 'queued'
		END
		FROM payment_vip_offer_versions v JOIN recharge_slots s ON s.vip_offer_version_id=v.slot_id
		JOIN recharge_templates t ON t.id=v.template_id
		LEFT JOIN payment_channel_accounts a ON a.provider='paypal'
		LEFT JOIN payment_paypal_vip_plans p ON p.slot_id=v.slot_id AND p.account_id=a.id
		ORDER BY t.name,v.slot_index,v.slot_id,a.environment,a.id`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (VIPOfferPublication, error) {
		var offer VIPOfferPublication
		err := row.Scan(&offer.AccountID, &offer.AccountLabel, &offer.Environment, &offer.VersionID, &offer.Template, &offer.Name,
			&offer.Amount, &offer.Currency, &offer.Cycle, &offer.PlanID, &offer.RequestKey, &offer.State)
		return offer, err
	})
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
			&o.PromotionLinkID, &o.NovelID, &o.CreatedAt, &o.UpdatedAt, &o.ReconciliationNote, &o.RefundStatus, &o.PaymentEnvironment,
		)
		if err != nil {
			return nil, err
		}
		orders = append(orders, o)
	}
	return orders, rows.Err()
}

func (r *dbBillingRepository) GetOrderForUpdateTx(ctx context.Context, tx pgx.Tx, orderID string) (*Order, error) {
	orderIDVal, err := strconv.ParseInt(orderID, 10, 64)
	if err != nil {
		return nil, err
	}

	query := `
		SELECT user_id, status, coins, bonus_coins_credited, amount_cents, currency, payment_method, order_type, COALESCE(external_ref_id,'')
		FROM recharge_orders
		WHERE id = $1 FOR UPDATE`
	var o Order
	o.ID = orderIDVal
	err = tx.QueryRow(ctx, query, orderIDVal).Scan(&o.UserID, &o.Status, &o.Coins, &o.BonusCoinsCredited, &o.AmountCents, &o.Currency, &o.PaymentMethod, &o.OrderType, &o.ExternalRefID)
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

func (r *dbBillingRepository) InsertAdminAuditLogTx(ctx context.Context, tx pgx.Tx, adminID, action, targetID, beforeData, afterData string) error {
	var adminIDVal *string
	if adminID != "" {
		adminIDVal = &adminID
	}
	query := `
		INSERT INTO admin_audit_logs (admin_id, action, target_id, before_data, after_data)
		VALUES ($1, $2, $3, $4, $5)`
	_, err := tx.Exec(ctx, query, adminIDVal, action, targetID, beforeData, afterData)
	return err
}

func (r *dbBillingRepository) GetThirdPartyPaymentDetails(ctx context.Context, orderID int64) (*ThirdPartyPaymentOrder, error) {
	query := `
		SELECT p.id, p.order_id, p.payment_provider, COALESCE(p.external_order_id, ''), COALESCE(p.capture_id, ''),
		       COALESCE(p.payer_id, ''), COALESCE(p.payer_email, ''), COALESCE(p.payer_name, ''), COALESCE(p.payer_country, ''),
		       p.currency, p.gross_amount, COALESCE(finance.fee,p.fee_amount), COALESCE(finance.net,p.net_amount), p.status,
		       COALESCE(p.seller_protection_status, ''), COALESCE(p.raw_payload::text, '{}'),
		       p.created_at, p.updated_at
		FROM third_party_payment_orders p
		LEFT JOIN LATERAL (
			SELECT -max(value.amount_minor) FILTER (WHERE value.component='fee')::numeric/100 AS fee,
			        max(value.amount_minor) FILTER (WHERE value.component='net')::numeric/100 AS net
			FROM payment_ledger_receipt_matches link
			JOIN payment_ledger_movements m ON m.id=link.movement_id AND m.kind='payment'
			JOIN payment_ledger_amounts gross ON gross.movement_id=m.id AND gross.component='gross'
				AND gross.currency=p.currency AND gross.amount_minor=p.gross_amount*100
			JOIN payment_ledger_amounts value ON value.movement_id=m.id AND value.currency=p.currency
			WHERE p.payment_provider='paypal' AND p.currency='USD' AND link.receipt_id=p.id
			HAVING count(DISTINCT m.id)=1
		) finance ON true
		WHERE p.order_id = $1
		ORDER BY EXISTS(SELECT 1 FROM payment_fulfillment_funding f WHERE f.order_id=p.order_id AND f.receipt_id=p.id) DESC,p.id DESC
		LIMIT 1`
	var t ThirdPartyPaymentOrder
	err := db.DB.QueryRow(ctx, query, orderID).Scan(
		&t.ID, &t.OrderID, &t.PaymentProvider, &t.ExternalOrderID, &t.CaptureID,
		&t.PayerID, &t.PayerEmail, &t.PayerName, &t.PayerCountry, &t.Currency,
		&t.GrossAmount, &t.FeeAmount, &t.NetAmount, &t.Status,
		&t.SellerProtectionStatus, &t.RawPayload,
		&t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &t, nil
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
	return templates, rows.Err()
}

func (r *dbBillingRepository) GetRechargeSlots(ctx context.Context, templateID int) ([]RechargeSlot, error) {
	rows, err := db.DB.Query(ctx, `
		SELECT id, template_id, slot_index, type, coins, bonus, COALESCE(vip_duration, ''), COALESCE(subscription_cycle, ''), COALESCE(vip_name, ''), COALESCE(vip_desc, ''), price, price_cents
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
		err := rows.Scan(&s.ID, &s.TemplateID, &s.SlotIndex, &s.Type, &s.Coins, &s.Bonus, &s.VipDuration, &s.SubscriptionCycle, &s.VipName, &s.VipDesc, &s.Price, &s.PriceCents)
		if err != nil {
			return nil, err
		}
		slots = append(slots, s)
	}
	return slots, rows.Err()
}

func (r *dbBillingRepository) ClearDefaultTemplatesTx(ctx context.Context, tx pgx.Tx) error {
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

func (r *dbBillingRepository) CreateRechargeSlotTx(ctx context.Context, tx pgx.Tx, templateID, index int, slotType string, coins, bonus int, duration, cycle, vipName, vipDesc, price string, priceCents int) error {
	query := `
		INSERT INTO recharge_slots (template_id, slot_index, type, coins, bonus, vip_duration, subscription_cycle, vip_name, vip_desc, price, price_cents)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING id`
	var slotID int
	err := tx.QueryRow(ctx, query, templateID, index, slotType, coins, bonus, duration, cycle, vipName, vipDesc, price, priceCents).Scan(&slotID)
	if err != nil || (slotType != "vip" && slotType != "subscription") {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE recharge_slots s SET vip_offer_version_id=snapshot_vip_offer(s) WHERE s.id=$1`, slotID)
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
