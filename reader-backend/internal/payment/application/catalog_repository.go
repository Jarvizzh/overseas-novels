package application

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"reader-backend/internal/db"
	"reader-backend/internal/model"
)

const defaultLiveTemplateIDSQL = `(SELECT id FROM recharge_templates WHERE environment = 'live' ORDER BY is_default DESC NULLS LAST, id LIMIT 1)`

func formatUSDPrice(cents int) string {
	return fmt.Sprintf("$%d.%02d", cents/100, cents%100)
}

func (r *dbRepository) GetRechargeTemplateByID(ctx context.Context, templateID int) (*model.RechargeTemplate, error) {
	var t model.RechargeTemplate
	query := `
		SELECT id, name, is_default
		FROM recharge_templates
		WHERE id = $1
		LIMIT 1`
	err := db.Pool(ctx).QueryRow(ctx, query, templateID).Scan(&t.ID, &t.Name, &t.IsDefault)
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
		WHERE is_default = TRUE AND environment = 'live'
		LIMIT 1`
	err := db.Pool(ctx).QueryRow(ctx, query).Scan(&t.ID, &t.Name, &t.IsDefault)
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
		WHERE environment = 'live'
		ORDER BY id ASC
		LIMIT 1`
	err := db.Pool(ctx).QueryRow(ctx, query).Scan(&t.ID, &t.Name, &t.IsDefault)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &t, nil
}

const sellableCatalogSlotColumns = `
	SELECT s.id, s.template_id, s.slot_index, p.id, p.code, p.product_type,
	       CASE WHEN p.product_type = 'vip' THEN 'subscription' ELSE p.product_type END,
	       p.name, p.description, p.coins, p.bonus,
	       COALESCE(p.vip_cycle, ''), p.currency, p.price_cents, s.list_price_cents,
	       s.badge, s.is_highlighted, s.display_alias,
	       p.novel_scope_type, p.novel_scope_ids
	FROM recharge_slots s
	JOIN products p ON p.id = s.product_id
	JOIN product_publications pub ON pub.product_id = p.id AND pub.environment = $2 AND pub.status = 'published'
`

func dualChannelSellableSQL(productAlias, envParam string) string {
	return `
	AND EXISTS (
		SELECT 1 FROM product_channel_bindings bp
		JOIN payment_channel_accounts ap ON ap.id = bp.account_id
		WHERE bp.product_id = ` + productAlias + `.id AND bp.provider = 'paypal' AND bp.environment = ` + envParam + `
		  AND ap.provider = 'paypal' AND ap.environment = ` + envParam + ` AND ap.status = 'published' AND ap.is_primary = true
		  AND bp.enabled = true AND bp.validation_status = 'verified'
		  AND ((` + productAlias + `.product_type = 'vip' AND ap.new_subscriptions_enabled AND bp.external_price_ref <> '')
		    OR (` + productAlias + `.product_type <> 'vip' AND ap.new_orders_enabled))
	)
	AND EXISTS (
		SELECT 1 FROM product_channel_bindings bs
		JOIN payment_channel_accounts ast ON ast.id = bs.account_id
		WHERE bs.product_id = ` + productAlias + `.id AND bs.provider = 'stripe' AND bs.environment = ` + envParam + `
		  AND ast.provider = 'stripe' AND ast.environment = ` + envParam + ` AND ast.status = 'published' AND ast.is_primary = true
		  AND bs.enabled = true AND bs.validation_status = 'verified'
		  AND bs.external_product_ref <> '' AND bs.external_price_ref <> ''
		  AND (` + envParam + ` = 'sandbox' OR (bs.validated_catalog IS NOT NULL AND bs.release_evidence IS NOT NULL AND bs.market_policy IS NOT NULL))
		  AND ((` + productAlias + `.product_type = 'vip' AND ast.new_subscriptions_enabled)
		    OR (` + productAlias + `.product_type <> 'vip' AND ast.new_orders_enabled))
	)`
}

var dualChannelSellablePredicate = dualChannelSellableSQL("p", "$2")

func scanHydratedSlot(row pgx.Row) (model.RechargeSlot, error) {
	var s model.RechargeSlot
	err := row.Scan(
		&s.ID, &s.TemplateID, &s.SlotIndex, &s.ProductID, &s.ProductCode, &s.ProductType, &s.Type,
		&s.Name, &s.Description, &s.Coins, &s.Bonus,
		&s.SubscriptionCycle, &s.Currency, &s.PriceCents, &s.ListPriceCents,
		&s.Badge, &s.IsHighlighted, &s.DisplayAlias,
		&s.NovelScopeType, &s.NovelScopeIDs,
	)
	if err != nil {
		return model.RechargeSlot{}, err
	}
	s.Price = formatUSDPrice(s.PriceCents)
	if s.ListPriceCents > s.PriceCents {
		s.ListPrice = formatUSDPrice(s.ListPriceCents)
	} else {
		s.ListPriceCents = 0
		s.ListPrice = ""
	}
	s.PayPalReady = true
	s.StripeReady = true
	if s.ProductType == "vip" {
		s.VipDuration = s.SubscriptionCycle
		s.VipName = s.Name
		s.VipDesc = s.Description
	} else if s.ProductType == "whole_book" {
		s.Coins = 0
		s.Bonus = 0
	}
	if s.NovelScopeIDs == nil {
		s.NovelScopeIDs = []int64{}
	}
	return s, nil
}

func (r *dbRepository) GetRechargeSlots(ctx context.Context, templateID int) ([]model.RechargeSlot, error) {
	query := sellableCatalogSlotColumns + ` WHERE s.template_id = $1 ` + dualChannelSellablePredicate + ` ORDER BY s.slot_index ASC, s.id ASC`
	rows, err := db.Pool(ctx).Query(ctx, query, templateID, currentPaymentLedgerEnvironment())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var slots []model.RechargeSlot
	for rows.Next() {
		s, err := scanHydratedSlot(rows)
		if err != nil {
			return nil, err
		}
		slots = append(slots, s)
	}
	return slots, rows.Err()
}

func (r *dbRepository) GetRechargeSlotByID(ctx context.Context, slotID int) (*model.RechargeSlot, error) {
	query := sellableCatalogSlotColumns + ` WHERE s.id = $1 ` + dualChannelSellablePredicate
	s, err := scanHydratedSlot(db.Pool(ctx).QueryRow(ctx, query, slotID, currentPaymentLedgerEnvironment()))
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &s, nil
}

func (r *dbRepository) WholeBookAvailable(ctx context.Context, userID int64, templateID int, novelID int64) (bool, error) {
	return wholeBookProductAvailable(ctx, db.Pool(ctx), userID, templateID, novelID, "promoted_or_default", nil)
}

func wholeBookProductAvailable(ctx context.Context, q checkoutQueryer, userID int64, templateID int, novelID int64, scopeType string, scopeIDs []int64) (bool, error) {
	if userID <= 0 || novelID <= 0 {
		return false, nil
	}
	var allowed bool
	err := q.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM users u
			LEFT JOIN LATERAL (SELECT id, novel_id, recharge_template_id FROM reader_promotion_for_user_env(u.id, $6)) promo ON TRUE
			WHERE u.id = $1
			  AND EXISTS (SELECT 1 FROM novels WHERE id = $3 AND status <> 'archived' AND publish_status = 'published')
			  AND ($2 = 0 OR $2 = COALESCE(promo.recharge_template_id, ` + defaultLiveTemplateIDSQL + `))
			  AND (
			      ($4 = 'all_published')
			   OR ($4 = 'promoted_novel' AND promo.novel_id = $3)
			   OR ($4 = 'specific_novels' AND $3 = ANY($5::bigint[]))
			   OR ($4 = 'promoted_or_default' AND (promo.novel_id = $3 OR (promo.id IS NULL AND EXISTS(SELECT 1 FROM recharge_templates WHERE id = $2 AND is_default AND environment = 'live'))))
			  )
		)`, userID, templateID, novelID, scopeType, scopeIDs, currentPaymentLedgerEnvironment()).Scan(&allowed)
	return allowed, err
}
