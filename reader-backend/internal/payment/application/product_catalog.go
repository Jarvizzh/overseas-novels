package application

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"reader-backend/internal/db"
	"reader-backend/internal/payment"
)

type resolvedProduct struct {
	ID             int64
	Code           string
	ProductType    string // 'single', 'whole_book', 'vip'
	OrderType      string // 'single', 'whole_book', 'subscription'
	Name           string
	Description    string
	PriceCents     int
	Currency       string
	Coins          int
	Bonus          int
	VipCycle       string
	NovelScopeType string
	NovelScopeIDs  []int64
	SlotID         int
	TemplateID     int
	Badge          string
	DisplayAlias   string
	SnapshotJSON   []byte
}

func buildProductSnapshotJSON(p resolvedProduct) []byte {
	payload := map[string]any{
		"product_id":       p.ID,
		"code":             p.Code,
		"product_type":     p.ProductType,
		"name":             p.Name,
		"description":      p.Description,
		"price_cents":      p.PriceCents,
		"currency":         p.Currency,
		"coins":            p.Coins,
		"bonus":            p.Bonus,
		"vip_cycle":        p.VipCycle,
		"novel_scope_type": p.NovelScopeType,
		"novel_scope_ids":  p.NovelScopeIDs,
		"slot_id":          p.SlotID,
		"badge":            p.Badge,
		"display_alias":    p.DisplayAlias,
		"snapshot_at":      time.Now().UTC().Format(time.RFC3339),
	}
	raw, _ := json.Marshal(payload)
	return raw
}

// resolveProductForCheckoutTx resolves an immutable published product that passes
// dual-channel readiness (PayPal AND Stripe) for the active environment.
func resolveProductForCheckoutTx(ctx context.Context, q checkoutQueryer, userID int64, productID int64, slotID int, ledgerEnv string) (resolvedProduct, error) {
	var p resolvedProduct
	if productID <= 0 && slotID <= 0 {
		return p, ErrCheckoutUnavailable
	}

	var err error
	if productID > 0 {
		err = q.QueryRow(ctx, `
			SELECT p.id, p.code, p.product_type, p.name, p.description, p.price_cents, p.currency,
			       p.coins, p.bonus, COALESCE(p.vip_cycle, ''), p.novel_scope_type, p.novel_scope_ids,
			       COALESCE(s.id, 0), COALESCE(s.template_id, 0), COALESCE(s.badge, ''), COALESCE(s.display_alias, '')
			FROM products p
			JOIN product_publications pub ON pub.product_id = p.id AND pub.environment = $2 AND pub.status = 'published'
			LEFT JOIN LATERAL (
				SELECT sl.id, sl.template_id, sl.badge, sl.display_alias
				FROM recharge_slots sl
				WHERE sl.product_id = p.id
				  AND (
				      ($3 > 0 AND sl.id = $3)
				   OR ($3 = 0 AND sl.template_id = COALESCE(
				       (SELECT rp.recharge_template_id FROM reader_promotion_for_user($4) rp WHERE rp.recharge_template_id IS NOT NULL),
				       (SELECT id FROM recharge_templates ORDER BY is_default DESC NULLS LAST, id LIMIT 1)
				   ))
				  )
				ORDER BY sl.id ASC LIMIT 1
			) s ON TRUE
			WHERE p.id = $1 AND ($3 = 0 OR s.id = $3)
			`+dualChannelSellablePredicate,
			productID, ledgerEnv, slotID, userID).
			Scan(
				&p.ID, &p.Code, &p.ProductType, &p.Name, &p.Description, &p.PriceCents, &p.Currency,
				&p.Coins, &p.Bonus, &p.VipCycle, &p.NovelScopeType, &p.NovelScopeIDs,
				&p.SlotID, &p.TemplateID, &p.Badge, &p.DisplayAlias,
			)
	} else {
		err = q.QueryRow(ctx, `
			SELECT p.id, p.code, p.product_type, p.name, p.description, p.price_cents, p.currency,
			       p.coins, p.bonus, COALESCE(p.vip_cycle, ''), p.novel_scope_type, p.novel_scope_ids,
			       s.id, s.template_id, s.badge, s.display_alias
			FROM recharge_slots s
			JOIN products p ON p.id = s.product_id
			JOIN product_publications pub ON pub.product_id = p.id AND pub.environment = $2 AND pub.status = 'published'
			WHERE s.id = $1
			`+dualChannelSellablePredicate,
			slotID, ledgerEnv).
			Scan(
				&p.ID, &p.Code, &p.ProductType, &p.Name, &p.Description, &p.PriceCents, &p.Currency,
				&p.Coins, &p.Bonus, &p.VipCycle, &p.NovelScopeType, &p.NovelScopeIDs,
				&p.SlotID, &p.TemplateID, &p.Badge, &p.DisplayAlias,
			)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return p, ErrCheckoutUnavailable
	}
	if err != nil {
		return p, err
	}
	if p.TemplateID == 0 {
		_ = q.QueryRow(ctx, `
			SELECT COALESCE(
				(SELECT rp.recharge_template_id FROM reader_promotion_for_user($1) rp WHERE rp.recharge_template_id IS NOT NULL),
				(SELECT id FROM recharge_templates ORDER BY is_default DESC NULLS LAST, id LIMIT 1), 0
			)`, userID).Scan(&p.TemplateID)
	}
	if p.ProductType == "vip" {
		p.OrderType = "subscription"
		p.Coins = 0
		p.Bonus = 0
	} else if p.ProductType == "whole_book" {
		p.OrderType = "whole_book"
		p.Coins = 0
		p.Bonus = 0
	} else {
		p.OrderType = "single"
	}
	if p.PriceCents <= 0 || p.Coins < 0 || p.Bonus < 0 {
		return p, ErrCheckoutUnavailable
	}
	if p.NovelScopeIDs == nil {
		p.NovelScopeIDs = []int64{}
	}
	p.SnapshotJSON = buildProductSnapshotJSON(p)
	return p, nil
}

func loadAvailableProductChannels(ctx context.Context, productID int64, productType, ledgerEnv string) []string {
	if db.Pool(ctx) == nil || productID <= 0 {
		return []string{payment.ProviderPayPal, payment.ProviderStripe}
	}
	var ppOK, stOK bool
	_ = db.Pool(ctx).QueryRow(ctx, `
		SELECT
		  EXISTS (
		    SELECT 1 FROM product_channel_bindings b
		    JOIN payment_channel_accounts a ON a.id = b.account_id
		    WHERE b.product_id = $1 AND b.provider = 'paypal' AND b.environment = $2
		      AND a.provider = 'paypal' AND a.environment = $2 AND a.status = 'published' AND a.is_primary = true
		      AND b.enabled = true AND b.validation_status = 'verified'
		      AND (($3 = 'vip' AND a.new_subscriptions_enabled AND b.external_price_ref <> '')
		        OR ($3 <> 'vip' AND a.new_orders_enabled))
		  ),
		  EXISTS (
		    SELECT 1 FROM product_channel_bindings b
		    JOIN payment_channel_accounts a ON a.id = b.account_id
		    WHERE b.product_id = $1 AND b.provider = 'stripe' AND b.environment = $2
		      AND a.provider = 'stripe' AND a.environment = $2 AND a.status = 'published' AND a.is_primary = true
		      AND b.enabled = true AND b.validation_status = 'verified'
		      AND b.external_product_ref <> '' AND b.external_price_ref <> ''
		      AND ($2 = 'sandbox' OR (b.validated_catalog IS NOT NULL AND b.release_evidence IS NOT NULL AND b.market_policy IS NOT NULL))
		      AND (($3 = 'vip' AND a.new_subscriptions_enabled)
		        OR ($3 <> 'vip' AND a.new_orders_enabled))
		  )`, productID, ledgerEnv, productType).Scan(&ppOK, &stOK)
	channels := make([]string, 0, 2)
	if ppOK {
		channels = append(channels, payment.ProviderPayPal)
	}
	if stOK {
		channels = append(channels, payment.ProviderStripe)
	}
	return channels
}
