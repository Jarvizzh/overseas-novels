package application

import (
	"context"

	"github.com/jackc/pgx/v5"
	"reader-backend/internal/db"
	"reader-backend/internal/model"
)

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
		WHERE is_default = TRUE
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

func (r *dbRepository) GetRechargeSlots(ctx context.Context, templateID int) ([]model.RechargeSlot, error) {
	query := `
		SELECT id, template_id, slot_index, type, coins, bonus, COALESCE(vip_duration, ''), COALESCE(subscription_cycle, ''), COALESCE(vip_name, ''), COALESCE(vip_desc, ''), price, price_cents
		FROM recharge_slots
		WHERE template_id = $1
		  AND (type NOT IN ('vip','subscription') OR EXISTS (
			SELECT 1 FROM payment_paypal_vip_plans p JOIN payment_channel_accounts a ON a.id=p.account_id
			WHERE p.slot_id=recharge_slots.vip_offer_version_id AND p.plan_id IS NOT NULL AND a.environment=$2
		  ))
		ORDER BY slot_index ASC`
	rows, err := db.Pool(ctx).Query(ctx, query, templateID, currentPaymentLedgerEnvironment())
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
	return slots, rows.Err()
}

func (r *dbRepository) GetRechargeSlotByID(ctx context.Context, slotID int) (*model.RechargeSlot, error) {
	query := `
		SELECT id, template_id, slot_index, type, coins, bonus, COALESCE(vip_duration, ''), COALESCE(subscription_cycle, ''), COALESCE(vip_name, ''), COALESCE(vip_desc, ''), price, price_cents
		FROM recharge_slots
		WHERE id = $1`
	var s model.RechargeSlot
	err := db.Pool(ctx).QueryRow(ctx, query, slotID).Scan(&s.ID, &s.TemplateID, &s.SlotIndex, &s.Type, &s.Coins, &s.Bonus, &s.VipDuration, &s.SubscriptionCycle, &s.VipName, &s.VipDesc, &s.Price, &s.PriceCents)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &s, nil
}

func (r *dbRepository) WholeBookAvailable(ctx context.Context, userID int64, templateID int, novelID int64) (bool, error) {
	var allowed bool
	err := db.Pool(ctx).QueryRow(ctx, `SELECT whole_book_offer_available($1,$2,$3)`, userID, templateID, novelID).Scan(&allowed)
	return allowed, err
}
