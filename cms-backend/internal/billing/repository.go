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
	CreateRechargeTemplateTx(ctx context.Context, tx pgx.Tx, name, environment string, isDefault bool) (int, error)
	CreateRechargeSlotTx(ctx context.Context, tx pgx.Tx, templateID, index int, productID int64, badge string, isHighlighted bool, displayAlias string) error

	UpdateRechargeTemplateTx(ctx context.Context, tx pgx.Tx, id int, name, environment string, isDefault bool) (int64, error)
	SetDefaultTemplateOnlyTx(ctx context.Context, tx pgx.Tx, id int) (int64, error)
	SyncRechargeSlotsTx(ctx context.Context, tx pgx.Tx, templateID int, slots []RechargeSlot) error
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
			&o.PaymentProvider, &o.PaymentAccountID, &o.SubscriptionCycle,
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
		SELECT t.id, t.name, t.environment, t.is_default, t.created_at, t.updated_at,
		       COALESCE(p.cnt, 0),
		       COALESCE(p.names, '{}'::text[])
		FROM recharge_templates t
		LEFT JOIN LATERAL (
			SELECT count(*)::int AS cnt,
			       array_agg(COALESCE(NULLIF(pl.name, ''), '#' || pl.id::text) ORDER BY pl.id) AS names
			FROM promotion_links pl
			WHERE pl.recharge_template_id = t.id
		) p ON true
		ORDER BY t.id DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var templates []RechargeTemplate
	for rows.Next() {
		var t RechargeTemplate
		err := rows.Scan(&t.ID, &t.Name, &t.Environment, &t.IsDefault, &t.CreatedAt, &t.UpdatedAt, &t.LinkedPromotionCount, &t.LinkedPromotionNames)
		if err != nil {
			return nil, err
		}
		if t.LinkedPromotionNames == nil {
			t.LinkedPromotionNames = []string{}
		}
		templates = append(templates, t)
	}
	return templates, rows.Err()
}

func (r *dbBillingRepository) GetRechargeSlots(ctx context.Context, templateID int) ([]RechargeSlot, error) {
	rows, err := db.DB.Query(ctx, `
		SELECT s.id, s.template_id, s.slot_index, s.product_id, s.badge, s.is_highlighted, s.display_alias,
		       p.code, p.product_type,
		       CASE WHEN p.product_type = 'vip' THEN 'subscription' ELSE p.product_type END,
		       p.name, p.description, p.coins, p.bonus, COALESCE(p.vip_cycle, ''),
		       p.price_cents, p.novel_scope_type, p.novel_scope_ids,
		       COALESCE(ARRAY(
		           SELECT pub.environment FROM product_publications pub
		           WHERE pub.product_id = p.id AND pub.status = 'published'
		           ORDER BY pub.environment
		       ), '{}'::text[])
		FROM recharge_slots s
		JOIN products p ON p.id = s.product_id
		WHERE s.template_id = $1
		ORDER BY s.slot_index ASC, s.id ASC
	`, templateID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var slots []RechargeSlot
	for rows.Next() {
		var s RechargeSlot
		err := rows.Scan(
			&s.ID, &s.TemplateID, &s.SlotIndex, &s.ProductID, &s.Badge, &s.IsHighlighted, &s.DisplayAlias,
			&s.ProductCode, &s.ProductType, &s.Type,
			&s.Name, &s.Description, &s.Coins, &s.Bonus, &s.SubscriptionCycle,
			&s.PriceCents, &s.NovelScopeType, &s.NovelScopeIDs, &s.PublishedEnvs,
		)
		if err != nil {
			return nil, err
		}
		s.Price = formatUSDCents(s.PriceCents)
		if s.ProductType == productTypeVIP {
			s.VipDuration = s.SubscriptionCycle
			s.VipName = s.Name
			s.VipDesc = s.Description
		}
		if s.NovelScopeIDs == nil {
			s.NovelScopeIDs = []int64{}
		}
		slots = append(slots, s)
	}
	return slots, rows.Err()
}

func (r *dbBillingRepository) ClearDefaultTemplatesTx(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, "UPDATE recharge_templates SET is_default = FALSE")
	return err
}

func (r *dbBillingRepository) CreateRechargeTemplateTx(ctx context.Context, tx pgx.Tx, name, environment string, isDefault bool) (int, error) {
	var templateID int
	query := `
		INSERT INTO recharge_templates (name, environment, is_default)
		VALUES ($1, $2, $3)
		RETURNING id`
	err := tx.QueryRow(ctx, query, name, environment, isDefault).Scan(&templateID)
	return templateID, err
}

func (r *dbBillingRepository) CreateRechargeSlotTx(ctx context.Context, tx pgx.Tx, templateID, index int, productID int64, badge string, isHighlighted bool, displayAlias string) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO recharge_slots (template_id, slot_index, product_id, badge, is_highlighted, display_alias)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		templateID, index, productID, badge, isHighlighted, displayAlias)
	return err
}

func (r *dbBillingRepository) UpdateRechargeTemplateTx(ctx context.Context, tx pgx.Tx, id int, name, environment string, isDefault bool) (int64, error) {
	query := `
		UPDATE recharge_templates
		SET name = $1, environment = $2, is_default = $3, updated_at = CURRENT_TIMESTAMP
		WHERE id = $4`
	tag, err := tx.Exec(ctx, query, name, environment, isDefault, id)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (r *dbBillingRepository) SyncRechargeSlotsTx(ctx context.Context, tx pgx.Tx, templateID int, slots []RechargeSlot) error {
	rows, err := tx.Query(ctx, `SELECT id, product_id FROM recharge_slots WHERE template_id = $1 FOR UPDATE`, templateID)
	if err != nil {
		return err
	}
	existingByProd := make(map[int64]int)
	for rows.Next() {
		var slotID int
		var prodID int64
		if err := rows.Scan(&slotID, &prodID); err != nil {
			rows.Close()
			return err
		}
		existingByProd[prodID] = slotID
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	desiredByProd := make(map[int64]RechargeSlot, len(slots))
	for _, s := range slots {
		desiredByProd[s.ProductID] = s
	}

	var removeIDs []int
	var parkIDs []int
	var keepSlots []struct {
		id   int
		slot RechargeSlot
	}
	var addSlots []RechargeSlot

	for prodID, slotID := range existingByProd {
		if _, ok := desiredByProd[prodID]; !ok {
			removeIDs = append(removeIDs, slotID)
		}
	}
	for _, s := range slots {
		if slotID, ok := existingByProd[s.ProductID]; ok {
			parkIDs = append(parkIDs, slotID)
			keepSlots = append(keepSlots, struct {
				id   int
				slot RechargeSlot
			}{id: slotID, slot: s})
		} else {
			addSlots = append(addSlots, s)
		}
	}

	if len(removeIDs) > 0 {
		if _, err := tx.Exec(ctx, `DELETE FROM recharge_slots WHERE template_id = $1 AND id = ANY($2::int[])`, templateID, removeIDs); err != nil {
			return err
		}
	}
	// Temporarily negate retained slot_index values so reordering never collides with UNIQUE(template_id, slot_index).
	if len(parkIDs) > 0 {
		if _, err := tx.Exec(ctx, `UPDATE recharge_slots SET slot_index = -id WHERE template_id = $1 AND id = ANY($2::int[])`, templateID, parkIDs); err != nil {
			return err
		}
		for _, item := range keepSlots {
			if _, err := tx.Exec(ctx, `
				UPDATE recharge_slots
				SET slot_index = $2, badge = $3, is_highlighted = $4, display_alias = $5
				WHERE id = $1 AND template_id = $6`,
				item.id, item.slot.SlotIndex, item.slot.Badge, item.slot.IsHighlighted, item.slot.DisplayAlias, templateID); err != nil {
				return err
			}
		}
	}
	for _, s := range addSlots {
		if err := r.CreateRechargeSlotTx(ctx, tx, templateID, s.SlotIndex, s.ProductID, s.Badge, s.IsHighlighted, s.DisplayAlias); err != nil {
			return err
		}
	}
	return nil
}

func (r *dbBillingRepository) SetDefaultTemplateOnlyTx(ctx context.Context, tx pgx.Tx, id int) (int64, error) {
	tag, err := tx.Exec(ctx, "UPDATE recharge_templates SET is_default = TRUE, updated_at = CURRENT_TIMESTAMP WHERE id = $1", id)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
