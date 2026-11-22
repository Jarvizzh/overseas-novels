package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"reader-backend/internal/conversion"
	"reader-backend/internal/db"
	"reader-backend/internal/model"
	"reader-backend/internal/payment"
	"reader-backend/internal/payment/aftercare"
	"reader-backend/internal/payment/paypal"
)

var ErrCheckoutUnavailable = errors.New("checkout order or package unavailable")

type checkoutOrder struct {
	id               int64
	userID           int64
	checkoutID       string
	status           string
	orderType        string
	amount           int64
	currency         string
	coins            int
	bonus            int
	novelID          int64
	fulfilled        bool
	legacyBookProven bool
	managedAttempt   bool
	providerSettled  bool
	closedAttempt    bool
	paymentMethod    string
	deferred         bool
}

type checkoutAttribution = conversion.Attribution

type CheckoutResult struct {
	settled   bool
	Coins     int    `json:"coins_awarded"`
	OrderType string `json:"order_type"`
}

func (r *dbRepository) GetCheckoutTemplate(ctx context.Context, userID int64) (int, error) {
	var id int
	err := db.Pool(ctx).QueryRow(ctx, `
		SELECT COALESCE(
			(SELECT p.recharge_template_id FROM reader_promotion_for_user($1) p WHERE p.recharge_template_id IS NOT NULL),
			(SELECT id FROM recharge_templates ORDER BY is_default DESC NULLS LAST, id LIMIT 1), 0)`, userID).Scan(&id)
	return id, err
}

func (r *dbRepository) CreateCheckout(ctx context.Context, userID int64, slot *model.RechargeSlot, novelID int64, attribution checkoutAttribution) (string, error) {
	if slot == nil || slot.ID <= 0 {
		return "", ErrCheckoutUnavailable
	}
	prod, err := resolveProductForCheckoutTx(ctx, db.Pool(ctx), userID, slot.ProductID, slot.ID, currentPaymentLedgerEnvironment())
	if err != nil {
		return "", err
	}
	if prod.OrderType == "subscription" ||
		(slot.TemplateID > 0 && slot.TemplateID != prod.TemplateID) ||
		(slot.PriceCents > 0 && slot.PriceCents != prod.PriceCents) ||
		(slot.Type != "" && slot.Type != prod.OrderType) ||
		slot.Bonus != prod.Bonus ||
		(prod.OrderType != "whole_book" && slot.Coins != prod.Coins) {
		return "", ErrCheckoutUnavailable
	}
	key := uuid.NewString()
	var environment *string
	if prod.OrderType == "whole_book" {
		scope := conversion.Environment()
		environment = &scope
	}
	_, err = insertCheckout(ctx, db.Pool(ctx), key, userID, prod, novelID, attribution, environment, "")
	return key, err
}

type checkoutQueryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func insertCheckout(ctx context.Context, q checkoutQueryer, key string, userID int64, prod resolvedProduct, novelID int64, attribution checkoutAttribution, environment *string, returnPath string) (int64, error) {
	if prod.OrderType == "whole_book" {
		allowed, err := wholeBookProductAvailable(ctx, q, userID, prod.TemplateID, novelID, prod.NovelScopeType, prod.NovelScopeIDs)
		if err != nil {
			return 0, err
		}
		if !allowed {
			return 0, ErrCheckoutUnavailable
		}
	}
	attributionJSON, err := json.Marshal(attribution)
	if err != nil {
		return 0, err
	}
	ledgerEnv := currentPaymentLedgerEnvironment()
	var id int64
	err = q.QueryRow(ctx, `
		INSERT INTO recharge_orders (checkout_id, user_id, amount_cents, currency, coins, bonus_coins_credited,
			payment_method, status, order_type, recharge_slot_id, recharge_template_id, product_id, product_snapshot, novel_id,
			utm_source, utm_campaign, promotion_link_id, fb_lead_metadata, checkout_environment, checkout_return_path)
		SELECT $1, u.id, $2, 'USD', $3, $4, 'paypal', 'Pending', $5::varchar, NULLIF($6, 0), NULLIF($7, 0), $8, $9::jsonb, NULLIF($10, 0),
			u.utm_source, u.utm_campaign, p.id, $12::jsonb || jsonb_build_object('pixel_id',(SELECT fp.pixel_id FROM promotion_links l JOIN fb_pixels fp ON fp.id=l.fb_pixel_id WHERE l.id=p.id)),
			$13, $14
		FROM users u
		LEFT JOIN LATERAL (SELECT id,novel_id,recharge_template_id FROM reader_promotion_for_user(u.id)) p ON TRUE
		WHERE u.id = $11
		  AND ($6 = 0 OR $7 = COALESCE(p.recharge_template_id,(SELECT id FROM recharge_templates ORDER BY is_default DESC NULLS LAST,id LIMIT 1)))
		  AND ($6 = 0 OR EXISTS(SELECT 1 FROM recharge_slots s WHERE s.id = $6 AND s.template_id = $7 AND s.product_id = $8))
		  AND EXISTS (
		      SELECT 1 FROM products pr
		      JOIN product_publications pub ON pub.product_id = pr.id AND pub.environment = $15 AND pub.status = 'published'
		      WHERE pr.id = $8 AND pr.product_type = $5::varchar AND pr.price_cents = $2 AND pr.bonus = $4
		        AND ($5::varchar = 'whole_book' OR pr.coins = $3)
		        `+dualChannelSellableSQL("pr", "$15")+`
		  )
		RETURNING id`,
		key, prod.PriceCents, prod.Coins, prod.Bonus, prod.OrderType, prod.SlotID, prod.TemplateID,
		prod.ID, string(prod.SnapshotJSON), novelID, userID, attributionJSON, environment, returnPath, ledgerEnv).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrCheckoutUnavailable
	}
	return id, err
}

func (r *dbRepository) BindCheckout(ctx context.Context, checkoutID, orderID string) error {
	tx, err := db.Pool(ctx).Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if err := bindCheckoutTx(ctx, tx, checkoutID, orderID, false); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func bindCheckoutTx(ctx context.Context, tx pgx.Tx, checkoutID, orderID string, keepOriginalReference bool) error {
	var existing *string
	err := tx.QueryRow(ctx, "SELECT external_ref_id FROM recharge_orders WHERE checkout_id=$1 FOR UPDATE", checkoutID).Scan(&existing)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && existing != nil && *existing != orderID && !keepOriginalReference) {
		return ErrCheckoutUnavailable
	}
	if err != nil || existing != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "UPDATE recharge_orders SET external_ref_id=$2 WHERE checkout_id=$1", checkoutID, orderID); err != nil {
		return err
	}
	return recordCheckoutInitiatedTx(ctx, tx, checkoutID, orderID)
}

func recordCheckoutInitiatedTx(ctx context.Context, tx pgx.Tx, checkoutID, orderID string) error {

	_, err := tx.Exec(ctx, `
		INSERT INTO reader_conversion_outbox (business_key,event_name,order_id,intent_id,environment,occurred_at,snapshot)
		SELECT 'checkout:'||o.checkout_id||':InitiateCheckout','InitiateCheckout',o.id,o.checkout_id::text,$2,clock_timestamp(),
			jsonb_build_object('user_id',u.id::text,'email',u.email,'amount_cents',o.amount_cents,'currency',o.currency,
				'order_type',o.order_type,'promotion_id',o.promotion_link_id::text,'action_source','website',
				'attribution',COALESCE(o.fb_lead_metadata,'{}'),
				'attribution_state',CASE WHEN NULLIF(o.fb_lead_metadata->>'pixel_id','') IS NOT NULL THEN 'matched'
					WHEN o.promotion_link_id IS NOT NULL OR COALESCE(o.utm_source,'')<>'' OR COALESCE(o.utm_campaign,'')<>''
						OR COALESCE(o.fb_lead_metadata->>'fbc','')<>'' THEN 'missing_pixel' ELSE 'organic' END)
		FROM recharge_orders o JOIN users u ON u.id=o.user_id WHERE o.checkout_id=$1
		ON CONFLICT (business_key) DO NOTHING`, checkoutID, conversion.Environment())
	if err != nil {
		return err
	}
	return nil
}

const checkoutSelect = `SELECT id, user_id, COALESCE(checkout_id::text, ''), status, order_type,
	amount_cents, currency, coins, bonus_coins_credited, COALESCE(novel_id,0), fulfilled_at IS NOT NULL, EXISTS(SELECT 1 FROM legacy_whole_book_proofs b
 WHERE b.order_id=recharge_orders.id AND b.novel_id=recharge_orders.novel_id),
	EXISTS (SELECT 1 FROM payment_checkout_attempts a WHERE a.order_id=recharge_orders.id AND a.provider='paypal' AND a.provider_order_id=$1),
	EXISTS (SELECT 1 FROM payment_checkout_attempts a WHERE a.order_id=recharge_orders.id AND a.provider='paypal' AND a.provider_order_id=$1 AND a.status='settled'),
		EXISTS (SELECT 1 FROM payment_checkout_attempts a WHERE a.order_id=recharge_orders.id AND a.provider='paypal' AND a.provider_order_id=$1 AND a.status='closed'),
		payment_method,
 EXISTS(SELECT 1 FROM payment_deferred_fulfillment d WHERE d.order_id=recharge_orders.id AND d.resolved_at IS NULL)
	FROM recharge_orders
	WHERE id=COALESCE((SELECT order_id FROM payment_checkout_attempts WHERE provider='paypal' AND provider_order_id=$1),
		(SELECT id FROM recharge_orders WHERE external_ref_id=$1))
	AND order_type IN ('single', 'whole_book')
	AND (payment_method='paypal' OR EXISTS (SELECT 1 FROM payment_checkout_attempts p
		WHERE p.order_id=recharge_orders.id AND p.provider='paypal' AND p.provider_order_id=$1))`

func (r *dbRepository) findCheckout(ctx context.Context, orderID string) (*checkoutOrder, error) {
	return scanCheckout(db.Pool(ctx).QueryRow(ctx, checkoutSelect, orderID))
}

func scanCheckout(row pgx.Row) (*checkoutOrder, error) {
	var o checkoutOrder
	err := row.Scan(&o.id, &o.userID, &o.checkoutID, &o.status, &o.orderType, &o.amount, &o.currency, &o.coins, &o.bonus, &o.novelID, &o.fulfilled, &o.legacyBookProven, &o.managedAttempt, &o.providerSettled, &o.closedAttempt, &o.paymentMethod, &o.deferred)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrCheckoutUnavailable
	}
	return &o, err
}

func (r *dbRepository) settleCheckout(ctx context.Context, orderID string, capture *payment.OrderCapture) (*CheckoutResult, error) {
	tx, err := db.Pool(ctx).Begin(ctx)
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
	if o.orderType == "whole_book" && (o.novelID <= 0 || (o.checkoutID == "" && !o.legacyBookProven)) {
		return nil, ErrCheckoutUnavailable
	}
	result := &CheckoutResult{Coins: o.coins + o.bonus, OrderType: o.orderType}
	historicalBook := o.needsHistoricalBookVerification()
	if (o.status == "Success" || o.status == "Paid") && !historicalBook && !o.managedAttempt {
		return result, nil
	}
	extra := o.managedAttempt && (o.closedAttempt || o.paymentMethod != payment.ProviderPayPal || o.fulfilled || o.status == "Refunded")
	if (o.status != "Pending" && !o.deferred && !historicalBook && !extra) || !o.matchesPayPalOrder(capture, orderID) ||
		(capture.Status != "COMPLETED" && capture.Status != "REFUNDED") {
		return nil, ErrCheckoutUnavailable
	}
	cents := o.amount
	if historicalBook && !extra {
		if !paymentEnvironmentMatches(capture.Account) {
			return nil, ErrCheckoutUnavailable
		}
		tag, err := tx.Exec(ctx, `UPDATE third_party_payment_orders receipt SET status='VERIFIED',raw_payload=$5::jsonb,
			account_id=account.id,object_type='capture'
			FROM payment_channel_accounts account
			WHERE account.provider='paypal' AND account.account_key=$6 AND account.environment=$7
			AND (receipt.account_id IS NULL OR receipt.account_id=account.id)
			AND order_id=$1 AND receipt.payment_provider='paypal' AND receipt.capture_id=$2
			AND receipt.gross_amount*100=$3 AND receipt.currency=$4 AND receipt.status IN ('COMPLETED','VERIFIED')`,
			o.id, capture.CaptureID, cents, o.currency, capture.RawPayload, capture.Account.AccountKey, capture.Account.Environment)
		if err != nil {
			return nil, err
		}
		if tag.RowsAffected() != 1 {
			return nil, ErrCheckoutUnavailable
		}
		var refundedCents int64
		err = tx.QueryRow(ctx, `SELECT COALESCE(sum(amount_cents),0) FROM paypal_refund_items
 WHERE capture_id=$1 AND currency=$2`, capture.CaptureID, o.currency).Scan(&refundedCents)
		if err != nil {
			return nil, err
		}
		if refundedCents >= cents {
			if err := tx.Commit(ctx); err != nil {
				return nil, err
			}
			return nil, errRefundUnmatched
		}

		if capture.Status != "COMPLETED" {
			return nil, errRefundUnmatched
		}
		_, err = tx.Exec(ctx, `UPDATE recharge_orders SET fulfilled_at=now(),reconciliation_note=NULL,updated_at=now(),
            capi_expectation=COALESCE(capi_expectation,jsonb_build_object('events','[]'::jsonb,'reason','historical_entitlement_recovery','environment',$2::text)) WHERE id=$1`, o.id, conversion.Environment())
		if err != nil {
			return nil, err
		}
		return result, tx.Commit(ctx)
	}

	receiptID, err := recordCheckoutReceipt(ctx, tx, o, capture, extra)
	if err != nil {
		return nil, err
	}
	if capture.Account.AccountKey != "" {
		fact, err := paypal.CaptureRisk(capture, time.Now())
		if err != nil {
			return nil, err
		}
		if fact != nil {
			if err := aftercare.RecordTx(ctx, tx, *fact); err != nil {
				return nil, err
			}
		}
	}

	if extra {
		return result, tx.Commit(ctx)
	}
	// A previously held order cannot be funded by a second capture.
	var otherReceipt bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM payment_deferred_fulfillment WHERE order_id=$1 AND receipt_id<>$2)`,
		o.id, receiptID).Scan(&otherReceipt); err != nil {
		return nil, err
	}
	if otherReceipt {
		if _, err := tx.Exec(ctx, `UPDATE third_party_payment_orders SET reconciliation_note='Extra capture while fulfillment is deferred' WHERE id=$1`, receiptID); err != nil {
			return nil, err
		}
		result.Coins = 0
		return result, tx.Commit(ctx)
	}
	refunded, err := applyRefundBeforeFulfillment(ctx, tx, o.id, capture.CaptureID, cents, o.currency)
	if err != nil {
		return nil, err
	}
	if refunded {
		if err := conversion.RecordPaymentTx(ctx, tx, o.id, capture.OccurredAt, "paypal_capture_create_time", false, false); err != nil {
			return nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return nil, ErrPaymentRefunded
	}
	if capture.Status == "REFUNDED" {
		return nil, errRefundUnmatched
	}

	hold, err := aftercare.EvaluateFulfillment(ctx, tx, o.id, receiptID)
	if err != nil {
		return nil, err
	}
	if hold.Deferred {
		_, err = tx.Exec(ctx, `UPDATE recharge_orders SET status='Paid',paid_at=COALESCE(paid_at,$2,now()),updated_at=now()
			WHERE id=$1`, o.id, verifiedPaidAt(capture.OccurredAt))
		if err != nil {
			return nil, err
		}
		return &CheckoutResult{OrderType: o.orderType}, tx.Commit(ctx)
	}
	if err := aftercare.FinishFulfillment(ctx, tx, o.id); err != nil {
		return nil, err
	}

	if err := recordFundingTx(ctx, tx, o.id, receiptID, o.amount, o.currency); err != nil {
		return nil, err
	}
	if result.Coins > 0 {
		if err := r.fulfillment.CreditRechargeTx(ctx, tx, o.userID, o.coins, o.bonus,
			fmt.Sprintf("PayPal recharge order #%d", o.id)); err != nil {
			return nil, err
		}
	}
	_, err = tx.Exec(ctx, `UPDATE recharge_orders SET status='Success', paid_at=COALESCE(paid_at,now()), fulfilled_at=clock_timestamp(),
		reconciliation_note=NULL, updated_at=now() WHERE id=$1`, o.id)
	if err != nil {
		return nil, err
	}
	if err := conversion.RecordPaymentTx(ctx, tx, o.id, capture.OccurredAt, "paypal_capture_create_time", false, false); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	result.settled = true
	return result, nil
}

func recordFundingTx(ctx context.Context, tx pgx.Tx, orderID, receiptID, amount int64, currency string) error {
	_, err := tx.Exec(ctx, `INSERT INTO payment_fulfillment_funding(order_id,receipt_id,principal_minor,currency)
		VALUES($1,$2,$3,$4) ON CONFLICT(order_id,receipt_id) DO NOTHING`, orderID, receiptID, amount, currency)
	return err
}

func recordCheckoutReceipt(ctx context.Context, tx pgx.Tx, o *checkoutOrder, capture *payment.OrderCapture, extra bool) (int64, error) {
	var accountID *int64
	if o.managedAttempt {
		err := tx.QueryRow(ctx, `UPDATE payment_checkout_attempts p SET status=CASE WHEN p.status='closed' THEN 'closed' ELSE 'settled' END
			FROM payment_channel_accounts a WHERE p.order_id=$1 AND p.provider='paypal' AND p.provider_order_id=$2
				AND p.account_id=a.id AND a.account_key=$3 AND a.environment=$4 RETURNING a.id`,
			o.id, capture.OrderID, capture.Account.AccountKey, capture.Account.Environment).Scan(&accountID)
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrCheckoutUnavailable
		}
		if err != nil {
			return 0, err
		}
	}
	var note *string
	if extra {
		reason := "Payment arrived after the order's funding outcome and requires review"
		note = &reason
	}
	var id int64
	err := tx.QueryRow(ctx, `INSERT INTO third_party_payment_orders
		(order_id,payment_provider,external_order_id,capture_id,payer_id,payer_email,payer_name,payer_country,
		currency,gross_amount,fee_amount,net_amount,status,seller_protection_status,raw_payload,account_id,object_type,reconciliation_note)
		VALUES ($1,'paypal',$2,$3,$4,$5,$6,$7,$8,$9::text::numeric,NULLIF($10::text,'')::numeric,NULLIF($11::text,'')::numeric,'VERIFIED',$12,$13::jsonb,$14,'capture',$15)
		ON CONFLICT (payment_provider,external_order_id,capture_id) DO UPDATE SET status='VERIFIED'
			WHERE third_party_payment_orders.order_id=EXCLUDED.order_id AND
			      third_party_payment_orders.account_id IS NOT DISTINCT FROM EXCLUDED.account_id
		RETURNING id`,
		o.id, capture.OrderID, capture.CaptureID, capture.PayerID, capture.PayerEmail, capture.PayerName, capture.PayerCountry,
		o.currency, capture.GrossAmount, capture.FeeAmount, capture.NetAmount, capture.SellerProtectionStatus, capture.RawPayload, accountID, note).Scan(&id)
	return id, err
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
	return o.paymentMethod != payment.ProviderStripe && !o.deferred && o.orderType == "whole_book" && !o.fulfilled && (o.status == "Success" || o.status == "Paid")
}

func (o *checkoutOrder) matchesPayPalOrder(capture *payment.OrderCapture, orderID string) bool {
	if capture == nil || capture.OrderID != orderID || !o.matchesCapture(capture.CustomID) || capture.CurrencyCode != o.currency {
		return false
	}
	if o.orderType == "whole_book" && (o.novelID <= 0 || (o.checkoutID == "" && !o.legacyBookProven)) {
		return false
	}
	cents, err := payment.ParseCents(capture.GrossAmount)
	return err == nil && cents == o.amount
}

func (r *dbRepository) flagCheckoutForReview(ctx context.Context, id int64, reason string) error {
	_, err := db.Pool(ctx).Exec(ctx, `UPDATE recharge_orders SET reconciliation_note=$2 WHERE id=$1
		AND (status='Pending' OR (order_type='whole_book' AND status IN ('Success','Paid') AND (fulfilled_at IS NULL OR checkout_id IS NULL)))`, id, reason)
	return err
}

func verifiedPaidAt(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
