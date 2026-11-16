package wallet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"reader-backend/internal/db"
	"reader-backend/internal/model"
	"reader-backend/internal/payment"
)

var ErrCheckoutUnavailable = errors.New("checkout order or package unavailable")

type checkoutOrder struct {
	id         int64
	userID     int64
	checkoutID string
	status     string
	orderType  string
	amount     int64
	currency   string
	coins      int
	bonus      int
	novelID    int64
	fulfilled  bool
}

type checkoutAttribution struct {
	FBP       string `json:"fbp,omitempty"`
	FBC       string `json:"fbc,omitempty"`
	IP        string `json:"ip_address,omitempty"`
	UserAgent string `json:"user_agent,omitempty"`
	SourceURL string `json:"source_url,omitempty"`
	Country   string `json:"country,omitempty"`
}

type CheckoutResult struct {
	settled   bool
	Coins     int    `json:"coins_awarded"`
	OrderType string `json:"order_type"`
}

func (r *dbRepository) GetCheckoutTemplate(ctx context.Context, userID int64) (int, error) {
	var id int
	err := db.DB.QueryRow(ctx, `
		SELECT COALESCE(
			(SELECT p.recharge_template_id FROM users u JOIN promotion_links p
			ON u.utm_source = p.utm_source AND u.utm_campaign = p.utm_campaign
			WHERE u.id = $1 AND u.utm_source <> '' AND u.utm_campaign <> ''
			AND p.recharge_template_id IS NOT NULL ORDER BY p.id DESC LIMIT 1),
			(SELECT id FROM recharge_templates ORDER BY is_default DESC NULLS LAST, id LIMIT 1), 0)`, userID).Scan(&id)
	return id, err
}

func (r *dbRepository) CreateCheckout(ctx context.Context, userID int64, slot *model.RechargeSlot, novelID int64, attribution checkoutAttribution) (string, error) {
	key := uuid.NewString()
	attributionJSON, err := json.Marshal(attribution)
	if err != nil {
		return "", err
	}
	var id int64
	err = db.DB.QueryRow(ctx, `
		INSERT INTO recharge_orders (checkout_id, user_id, amount_cents, currency, coins, bonus_coins_credited,
			payment_method, status, order_type, recharge_slot_id, recharge_template_id, novel_id, utm_source, utm_campaign, promotion_link_id,fb_lead_metadata)
		SELECT $1, u.id, $2, 'USD', $3, $4, 'paypal', 'Pending', $5::varchar, $6, $7, NULLIF($8, 0),
			u.utm_source, u.utm_campaign, p.id,$10::jsonb
		FROM users u
		LEFT JOIN LATERAL (SELECT id,novel_id,recharge_template_id FROM promotion_links
			WHERE utm_source = u.utm_source AND utm_campaign = u.utm_campaign
			AND u.utm_source <> '' AND u.utm_campaign <> '' AND recharge_template_id IS NOT NULL
			ORDER BY id DESC LIMIT 1) p ON TRUE
		WHERE u.id = $9
		AND $7 = COALESCE(p.recharge_template_id,(SELECT id FROM recharge_templates ORDER BY is_default DESC NULLS LAST,id LIMIT 1))
		AND ($5::varchar <> 'whole_book' OR ($8 > 0 AND (
			p.novel_id=$8 OR (p.id IS NULL AND EXISTS(SELECT 1 FROM recharge_templates WHERE id=$7 AND is_default)))))
		AND EXISTS(SELECT 1 FROM recharge_slots WHERE id=$6 AND template_id=$7 AND type=$5::varchar
			AND price_cents=$2 AND bonus=$4 AND ($5::varchar='whole_book' OR coins=$3))
		RETURNING id`,
		key, slot.PriceCents, slot.Coins, slot.Bonus, slot.Type, slot.ID, slot.TemplateID, novelID, userID, attributionJSON).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrCheckoutUnavailable
	}
	return key, err
}

func (r *dbRepository) BindCheckout(ctx context.Context, checkoutID, orderID string) error {
	tag, err := db.DB.Exec(ctx, `UPDATE recharge_orders SET external_ref_id=$2
		WHERE checkout_id=$1 AND (external_ref_id IS NULL OR external_ref_id=$2)`, checkoutID, orderID)
	if err == nil && tag.RowsAffected() != 1 {
		return ErrCheckoutUnavailable
	}
	return err
}

const checkoutSelect = `SELECT id, user_id, COALESCE(checkout_id::text, ''), status, order_type,
	amount_cents, currency, coins, bonus_coins_credited, COALESCE(novel_id,0), fulfilled_at IS NOT NULL FROM recharge_orders
	WHERE external_ref_id=$1 AND payment_method='paypal' AND order_type IN ('single', 'whole_book')`

func (r *dbRepository) findCheckout(ctx context.Context, orderID string) (*checkoutOrder, error) {
	return scanCheckout(db.DB.QueryRow(ctx, checkoutSelect, orderID))
}

func scanCheckout(row pgx.Row) (*checkoutOrder, error) {
	var o checkoutOrder
	err := row.Scan(&o.id, &o.userID, &o.checkoutID, &o.status, &o.orderType, &o.amount, &o.currency, &o.coins, &o.bonus, &o.novelID, &o.fulfilled)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrCheckoutUnavailable
	}
	return &o, err
}

func (r *dbRepository) settleCheckout(ctx context.Context, orderID string, capture *payment.PayPalCaptureResult) (*CheckoutResult, error) {
	tx, err := db.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if capture.CaptureID == "" {
		return nil, ErrCheckoutUnavailable
	}
	if err := lockPayPalCapture(ctx, tx, capture.CaptureID); err != nil {
		return nil, err
	}

	o, err := scanCheckout(tx.QueryRow(ctx, checkoutSelect+" FOR UPDATE", orderID))
	if err != nil {
		return nil, err
	}
	result := &CheckoutResult{Coins: o.coins + o.bonus, OrderType: o.orderType}
	historicalBook := o.needsHistoricalBookVerification()
	if (o.status == "Success" || o.status == "Paid") && !historicalBook {
		return result, nil
	}
	if (o.status != "Pending" && !historicalBook) || !o.matchesPayPalOrder(capture, orderID) ||
		(capture.Status != "COMPLETED" && capture.Status != "REFUNDED") {
		return nil, ErrCheckoutUnavailable
	}
	cents := o.amount
	if historicalBook {
		tag, err := tx.Exec(ctx, `UPDATE third_party_payment_orders SET status='VERIFIED',raw_payload=$5::jsonb
			WHERE order_id=$1 AND payment_provider='paypal' AND capture_id=$2
			AND gross_amount*100=$3 AND currency=$4 AND status IN ('COMPLETED','VERIFIED')`,
			o.id, capture.CaptureID, cents, o.currency, capture.RawPayload)
		if err != nil {
			return nil, err
		}
		if tag.RowsAffected() != 1 {
			return nil, ErrCheckoutUnavailable
		}
		var refundID string
		err = tx.QueryRow(ctx, `SELECT refund_id FROM paypal_event_inbox WHERE event_type='refund_completed'
			AND capture_id=$1 AND amount_cents=$2 AND currency=$3 LIMIT 1`, capture.CaptureID, cents, o.currency).Scan(&refundID)
		if err == nil {
			// Publish the verified receipt so the normal refund path can reverse historical coins.
			if err := tx.Commit(ctx); err != nil {
				return nil, err
			}
			return nil, errRefundUnmatched
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		if capture.Status != "COMPLETED" {
			return nil, errRefundUnmatched
		}
		_, err = tx.Exec(ctx, `UPDATE recharge_orders SET fulfilled_at=now(),reconciliation_note=NULL,updated_at=now() WHERE id=$1`, o.id)
		if err != nil {
			return nil, err
		}
		return result, tx.Commit(ctx)
	}

	// The partial unique index prevents the same verified capture funding multiple orders.
	_, err = tx.Exec(ctx, `INSERT INTO third_party_payment_orders
		(order_id,payment_provider,external_order_id,capture_id,payer_id,payer_email,payer_name,payer_country,
		currency,gross_amount,fee_amount,net_amount,status,seller_protection_status,raw_payload)
		VALUES ($1,'paypal',$2,$3,$4,$5,$6,$7,$8,$9::text::numeric,$10::text::numeric,$11::text::numeric,'VERIFIED',$12,$13::jsonb)`,
		o.id, orderID, capture.CaptureID, capture.PayerID, capture.PayerEmail, capture.PayerName, capture.PayerCountry,
		o.currency, capture.GrossAmount, capture.FeeAmount, capture.NetAmount, capture.SellerProtectionStatus, capture.RawPayload)
	if err != nil {
		return nil, err
	}
	refunded, err := applyRefundBeforeFulfillment(ctx, tx, o.id, capture.CaptureID, cents, o.currency)
	if err != nil {
		return nil, err
	}
	if refunded {
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return nil, ErrPaymentRefunded
	}
	if capture.Status == "REFUNDED" {
		return nil, errRefundUnmatched
	}

	if result.Coins > 0 {
		_, err = tx.Exec(ctx, `INSERT INTO wallets(user_id, charged_coins, bonus_coins) VALUES ($1,$2,$3)
			ON CONFLICT (user_id) DO UPDATE SET charged_coins=wallets.charged_coins+$2,
			bonus_coins=wallets.bonus_coins+$3, updated_at=now()`, o.userID, o.coins, o.bonus)
		if err != nil {
			return nil, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO transactions(id,user_id,type,biz_type,amount,charged_amount,bonus_amount,description)
			VALUES ($1,$2,'credit','recharge',$3,$4,$5,$6)`, uuid.NewString(), o.userID, result.Coins, o.coins, o.bonus,
			fmt.Sprintf("PayPal recharge order #%d", o.id))
		if err != nil {
			return nil, err
		}
	}
	_, err = tx.Exec(ctx, `UPDATE recharge_orders SET status='Success', paid_at=now(), fulfilled_at=now(), updated_at=now() WHERE id=$1`, o.id)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	result.settled = true
	return result, nil
}

func (o *checkoutOrder) matchesCapture(customID string) bool {
	if o.checkoutID != "" {
		return customID == o.checkoutID
	}
	if o.coins+o.bonus <= 0 {
		return false
	}
	return customID == fmt.Sprintf("%d:%d", o.userID, o.coins+o.bonus) ||
		customID == fmt.Sprintf("%d:%d", o.userID, o.coins)
}

func (o *checkoutOrder) needsHistoricalBookVerification() bool {
	return o.orderType == "whole_book" && !o.fulfilled && (o.status == "Success" || o.status == "Paid")
}

func (o *checkoutOrder) matchesPayPalOrder(capture *payment.PayPalCaptureResult, orderID string) bool {
	if capture == nil || capture.OrderID != orderID || !o.matchesCapture(capture.CustomID) || capture.CurrencyCode != o.currency {
		return false
	}
	if o.orderType == "whole_book" && o.novelID <= 0 {
		return false
	}
	cents, err := payment.ParseCents(capture.GrossAmount)
	return err == nil && cents == o.amount
}

func (r *dbRepository) flagCheckoutForReview(ctx context.Context, id int64, reason string) error {
	_, err := db.DB.Exec(ctx, `UPDATE recharge_orders SET reconciliation_note=$2 WHERE id=$1
		AND (status='Pending' OR (order_type='whole_book' AND fulfilled_at IS NULL AND status IN ('Success','Paid')))`, id, reason)
	return err
}
