package application

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"reader-backend/internal/conversion"
	"reader-backend/internal/db"
	"reader-backend/internal/model"
	"reader-backend/internal/novel"
	"reader-backend/internal/payment/aftercare"
)

func (r *dbRepository) GetProviderPlan(ctx context.Context, provider string, slotID int) (*model.PaymentProviderPlan, error) {
	query := `
		SELECT id, provider, slot_id, cycle, price_cents, currency, external_plan_id, status, COALESCE(raw_payload::text, '{}'), created_at, updated_at
		FROM payment_provider_plans
		WHERE provider = $1 AND slot_id = $2 AND status='ACTIVE'
 AND ($1<>'paypal' OR EXISTS(SELECT 1 FROM paypal_identity_lock i WHERE i.client_id=payment_provider_plans.identity_client_id AND i.environment=payment_provider_plans.identity_environment))
		LIMIT 1`
	var p model.PaymentProviderPlan
	err := db.Pool(ctx).QueryRow(ctx, query, provider, slotID).Scan(
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
	return db.Pool(ctx).QueryRow(ctx, query,
		sub.UserID, sub.SubscriptionID, sub.PlanID, sub.SlotID, sub.TemplateID, sub.Status,
		sub.Cycle, sub.PriceCents, sub.Currency, sub.PaymentMethod, sub.CurrentPeriodStart, sub.CurrentPeriodEnd,
		sub.NextBillingTime, sub.LastPaymentTime, raw,
	).Scan(&sub.ID)
}

func (r *dbRepository) GetManagedSubscriptionByUserID(ctx context.Context, userID int64) (*model.UserSubscription, error) {
	subscriptions, err := r.ListManagedSubscriptions(ctx, userID)
	if err != nil || len(subscriptions) == 0 {
		return nil, err
	}
	return &subscriptions[0], nil
}
func (r *dbRepository) ListManagedSubscriptions(ctx context.Context, userID int64) ([]model.UserSubscription, error) {
	query := `
		SELECT id, user_id, subscription_id, COALESCE(plan_id, ''), COALESCE(product_id, 0), COALESCE(slot_id, 0), COALESCE(template_id, 0),
		       status, cycle, price_cents, currency, payment_method, current_period_start, current_period_end,
		       next_billing_time, last_payment_time, cancelled_at, COALESCE(raw_payload::text, '{}'), created_at, updated_at,
		       COALESCE((SELECT CASE v.sales_mode WHEN 'direct' THEN 'stripe_portal' ELSE 'stripe_link' END
		        FROM payment_stripe_subscription_intents v WHERE v.id=user_subscriptions.checkout_intent_id AND v.checkout_environment=$2),''),paypal_renewal_failed_at
		FROM user_subscriptions
		WHERE user_id = $1 AND payment_subscription_environment(user_subscriptions)=$2 AND (status IN ('PENDING','ACTIVE','SUSPENDED','CANCEL_PENDING') OR (` + novel.PaidSubscriptionPredicate + `))
 AND subscription_id NOT LIKE 'I-MOCK%'
		ORDER BY id DESC`
	rows, err := db.Pool(ctx).Query(ctx, query, userID, conversion.Environment())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	subscriptions := make([]model.UserSubscription, 0)
	for rows.Next() {
		row := rows

		var s model.UserSubscription
		err := row.Scan(
			&s.ID, &s.UserID, &s.SubscriptionID, &s.PlanID, &s.ProductID, &s.SlotID, &s.TemplateID,
			&s.Status, &s.Cycle, &s.PriceCents, &s.Currency, &s.PaymentMethod, &s.CurrentPeriodStart, &s.CurrentPeriodEnd,
			&s.NextBillingTime, &s.LastPaymentTime, &s.CancelledAt, &s.RawPayload, &s.CreatedAt, &s.UpdatedAt, &s.Management, &s.RenewalFailedAt,
		)
		if err != nil {
			return nil, err
		}
		subscriptions = append(subscriptions, s)
	}
	return subscriptions, rows.Err()
}

func (r *dbRepository) GetSubscriptionByID(ctx context.Context, subID string) (*model.UserSubscription, error) {
	query := `
		SELECT id, user_id, subscription_id, COALESCE(plan_id, ''), COALESCE(product_id, 0), COALESCE(slot_id, 0), COALESCE(template_id, 0),
		       status, cycle, price_cents, currency, payment_method, current_period_start, current_period_end,
		       next_billing_time, last_payment_time, cancelled_at, COALESCE(raw_payload::text, '{}'), created_at, updated_at,COALESCE(payment_subscription_environment(user_subscriptions),'')
		FROM user_subscriptions
		WHERE subscription_id = $1
		ORDER BY id DESC
		LIMIT 1`
	var s model.UserSubscription
	err := db.Pool(ctx).QueryRow(ctx, query, subID).Scan(
		&s.ID, &s.UserID, &s.SubscriptionID, &s.PlanID, &s.ProductID, &s.SlotID, &s.TemplateID,
		&s.Status, &s.Cycle, &s.PriceCents, &s.Currency, &s.PaymentMethod, &s.CurrentPeriodStart, &s.CurrentPeriodEnd,
		&s.NextBillingTime, &s.LastPaymentTime, &s.CancelledAt, &s.RawPayload, &s.CreatedAt, &s.UpdatedAt, &s.Environment,
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
	_, err := db.Pool(ctx).Exec(ctx, `UPDATE user_subscriptions SET status=$1,updated_at=now(),
		cancelled_at=CASE WHEN $1='CANCELLED' THEN COALESCE(cancelled_at,now()) ELSE cancelled_at END,
		next_billing_time=CASE WHEN $1='CANCELLED' THEN NULL ELSE next_billing_time END,
		cancel_next_attempt_at=CASE WHEN $1='CANCELLED' THEN NULL ELSE cancel_next_attempt_at END,
		cancel_last_error=CASE WHEN $1='CANCELLED' THEN NULL ELSE cancel_last_error END
		WHERE subscription_id=$2 AND (status<>'CANCELLED' OR $1='CANCELLED')
		AND (status<>'CANCEL_PENDING' OR $1 IN ('CANCELLED','EXPIRED'))
		AND (status<>$1 OR ($1='CANCELLED' AND (cancelled_at IS NULL OR next_billing_time IS NOT NULL
		     OR cancel_next_attempt_at IS NOT NULL OR cancel_last_error IS NOT NULL)))`, status, subID)
	return err
}

func (r *dbRepository) RecordSubscriptionRenewalOrderTx(
	ctx context.Context, sub *model.UserSubscription, externalRefID string, amountCents int64,
	currency string, paidAt, periodEnd time.Time, providerStatus string, tpOrder *model.ThirdPartyPaymentOrder,
) (bool, error) {
	tx, err := db.Pool(ctx).Begin(ctx)
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
		paid_at, order_type, subscription_id, recharge_slot_id, recharge_template_id, product_id, product_snapshot, paid_period_start, paid_period_end,
		utm_source, utm_campaign, promotion_link_id, novel_id, reconciliation_note, paypal_refund_reported,fb_lead_metadata,checkout_environment)
		SELECT u.id,$2,$3,$4,0,0,$5,'Success',$6,'subscription',$7::text,$8,
			(SELECT id FROM recharge_templates WHERE id=$9),
			(SELECT product_id FROM user_subscriptions WHERE subscription_id=$7::text),
			(SELECT product_snapshot FROM user_subscriptions WHERE subscription_id=$7::text),
			CASE WHEN $10::timestamptz IS NOT NULL THEN $6::timestamptz END,$10,COALESCE((SELECT attribution->>'utm_source' FROM user_subscriptions WHERE subscription_id=$7::text),u.utm_source),COALESCE((SELECT attribution->>'utm_campaign' FROM user_subscriptions WHERE subscription_id=$7::text),u.utm_campaign),p.id,p.novel_id,
 CASE WHEN $11 THEN 'PayPal reports refunded sale; match refund ID'
 WHEN $10::timestamptz IS NULL AND $3::int < $14::int THEN 'PayPal settled amount is below the agreement price; billing period needs review'
 WHEN $10::timestamptz IS NULL THEN 'PayPal settled amount verified; billing period needs review' END,$11,(SELECT attribution FROM user_subscriptions WHERE subscription_id=$7::text) || jsonb_build_object('payer_email',$12::text),NULLIF($13,'')
		FROM users u LEFT JOIN LATERAL (
			SELECT id,novel_id FROM reader_promotion_for_user_env(u.id, CASE WHEN $13 IN ('sandbox','test') THEN 'sandbox' ELSE 'live' END)
		) p ON TRUE WHERE u.id=$1
		ON CONFLICT(external_ref_id) DO UPDATE SET
			paid_period_end=COALESCE(EXCLUDED.paid_period_end,recharge_orders.paid_period_end),
 paid_period_start=COALESCE(EXCLUDED.paid_period_start,recharge_orders.paid_period_start),
			paypal_refund_reported=EXCLUDED.paypal_refund_reported,
			reconciliation_note=EXCLUDED.reconciliation_note
		WHERE ((recharge_orders.paid_period_end IS NULL AND EXCLUDED.paid_period_end IS NOT NULL
			AND NOT recharge_orders.paypal_refund_reported) OR EXCLUDED.paypal_refund_reported
            OR (recharge_orders.fulfilled_at IS NULL AND EXISTS(SELECT 1 FROM payment_deferred_fulfillment h
                WHERE h.order_id=recharge_orders.id AND h.resolved_at IS NULL)))
			AND recharge_orders.status IN ('Success','Paid') AND recharge_orders.user_id=EXCLUDED.user_id
			AND recharge_orders.subscription_id=EXCLUDED.subscription_id AND recharge_orders.payment_method=EXCLUDED.payment_method
			AND recharge_orders.amount_cents=EXCLUDED.amount_cents AND recharge_orders.currency=EXCLUDED.currency
		RETURNING id`,
		sub.UserID, externalRefID, amountCents, currency, sub.PaymentMethod, paidAt, sub.SubscriptionID, sub.SlotID, sub.TemplateID, confirmedEnd, reportedRefund, tpOrder.PayerEmail, sub.Environment, sub.PriceCents).Scan(&orderID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var receiptID int64
	err = tx.QueryRow(ctx, `INSERT INTO third_party_payment_orders
		(order_id,payment_provider,external_order_id,capture_id,payer_id,payer_email,payer_name,currency,gross_amount,fee_amount,net_amount,status,raw_payload,account_id,object_type)
		SELECT $1,$2,s.subscription_id,$4,$5,$6,$7::text,$8,$9::bigint::numeric/100,NULL,NULL,'VERIFIED',$10::jsonb,s.checkout_account_id,'sale'
		FROM user_subscriptions s WHERE s.subscription_id=$3
		ON CONFLICT(payment_provider,external_order_id,capture_id) DO UPDATE SET status='VERIFIED',raw_payload=EXCLUDED.raw_payload
		WHERE third_party_payment_orders.order_id=EXCLUDED.order_id
			AND third_party_payment_orders.account_id IS NOT DISTINCT FROM EXCLUDED.account_id
		RETURNING id`,
		orderID, sub.PaymentMethod, sub.SubscriptionID, externalRefID, tpOrder.PayerID, tpOrder.PayerEmail, tpOrder.PayerName,
		currency, amountCents, tpOrder.RawPayload).Scan(&receiptID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, errors.New("PayPal subscription capture is linked to another order")
	}
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
		if err := conversion.RecordPaymentTx(ctx, tx, orderID, paidAt, "paypal_subscription_ledger", false, false); err != nil {
			return false, err
		}
		return false, tx.Commit(ctx)
	}
	if !reportedRefund && !periodEnd.IsZero() {
		hold, err := aftercare.EvaluateFulfillment(ctx, tx, orderID, receiptID)
		if err != nil {
			return false, err
		}
		if hold.Deferred {
			return false, tx.Commit(ctx)
		}
		if err := aftercare.FinishFulfillment(ctx, tx, orderID); err != nil {
			return false, err
		}
	}

	var previousPayment *time.Time
	var subscribeState string
	err = tx.QueryRow(ctx, `SELECT last_payment_time, capi_subscribe_state FROM user_subscriptions WHERE subscription_id=$1 FOR UPDATE`,
		sub.SubscriptionID).Scan(&previousPayment, &subscribeState)
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
		paypal_renewal_failed_at=CASE WHEN paypal_renewal_failed_at<=$2 THEN NULL ELSE paypal_renewal_failed_at END,
		last_payment_time=GREATEST(last_payment_time,$2), current_period_end=GREATEST(current_period_end,$3),
		next_billing_time=CASE WHEN status IN ('CANCELLED','CANCEL_PENDING') OR $4 <> 'ACTIVE' THEN NULL ELSE GREATEST(next_billing_time,$3) END,
		updated_at=now()
		WHERE subscription_id=$1`,
		sub.SubscriptionID, paidAt, periodEnd, providerStatus)
	if err != nil {
		return false, err
	}
	if err := recordFundingTx(ctx, tx, orderID, receiptID, amountCents, currency); err != nil {
		return false, err
	}
	firstConversion := subscribeState == "eligible"
	if firstConversion {
		if _, err := tx.Exec(ctx, `UPDATE user_subscriptions SET capi_subscribe_state='claimed',
			capi_subscribe_order_id=$2 WHERE subscription_id=$1 AND capi_subscribe_state='eligible'`, sub.SubscriptionID, orderID); err != nil {
			return false, err
		}
	}
	// Mark fulfillment only after the subscription row is locked and its
	// entitlement is written. The fulfillment trigger then locks the
	// user/promotion pair in the same order as one-time settlements.
	if _, err := tx.Exec(ctx, `UPDATE recharge_orders SET fulfilled_at=clock_timestamp() WHERE id=$1 AND fulfilled_at IS NULL`, orderID); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE payment_vip_checkout_intents
		SET status='Completed',switch_to_provider=NULL,subscription_id=COALESCE(NULLIF(subscription_id,''),$1),updated_at=now()
		WHERE status='Pending'
		  AND (paypal_intent_id=(SELECT checkout_intent_id FROM user_subscriptions WHERE subscription_id=$1 AND payment_method='paypal' LIMIT 1)
		    OR subscription_id=$1)`, sub.SubscriptionID); err != nil {
		return false, err
	}
	var paymentKind *string
	environment := conversion.Environment()
	if err := tx.QueryRow(ctx, `SELECT reader_subscription_payment_kind($1,$2)`, orderID, environment).Scan(&paymentKind); err != nil {
		return false, err
	}
	automatic := paymentKind != nil && *paymentKind == "subscription_renewal"
	if err := conversion.RecordSubscriptionPaymentTx(ctx, tx, orderID, paidAt, "paypal_subscription_ledger", environment, paymentKind, firstConversion, automatic); err != nil {
		return false, err
	}
	return previousPayment == nil, tx.Commit(ctx)
}

func (r *dbRepository) enqueuePayPalCancellation(ctx context.Context, id string, userID int64, reason string) error {
	_, err := db.Pool(ctx).Exec(ctx, `UPDATE user_subscriptions SET status='CANCEL_PENDING',
		cancel_reason=COALESCE(cancel_reason,NULLIF($3,'')),cancel_next_attempt_at=COALESCE(cancel_next_attempt_at,now()),updated_at=now()
		WHERE subscription_id=$1 AND user_id=$2 AND status NOT IN ('CANCELLED','EXPIRED')`, id, userID, reason)
	return err
}

// requeueLateApprovedPayPalCancellation moves a locally CANCELLED PayPal agreement
// back to CANCEL_PENDING when PayPal's live API reports it was still approved or
// activated (e.g. a buyer approved an old APPROVAL_PENDING link after switching
// channels), so reconciliation cancels it on PayPal and stops recurring charges.
func requeueLateApprovedPayPalCancellation(ctx context.Context, subscriptionID, reason string) error {
	if db.Pool(ctx) == nil {
		return nil
	}
	_, err := db.Pool(ctx).Exec(ctx, `UPDATE user_subscriptions
		SET status='CANCEL_PENDING',
		    cancel_reason=COALESCE(NULLIF(cancel_reason,''), $2),
		    cancel_next_attempt_at=now(),
		    cancel_last_error=NULL,
		    next_billing_time=NULL,
		    updated_at=now()
		WHERE subscription_id=$1 AND payment_method='paypal' AND status='CANCELLED'`,
		subscriptionID, reason)
	return err
}

// A late failure cannot overwrite the status set by a newer verified payment.
func recordPayPalRenewalFailure(ctx context.Context, subscriptionID string, failedAt time.Time, observedStatus string) error {
	_, err := db.Pool(ctx).Exec(ctx, `UPDATE user_subscriptions SET
		status=CASE WHEN $3 IN ('ACTIVE','SUSPENDED') AND status<>'CANCEL_PENDING' THEN $3 ELSE status END,
		paypal_renewal_failed_at=$2,paypal_next_check_at=now(),paypal_recovery_requested=true,updated_at=now()
		WHERE subscription_id=$1 AND payment_method='paypal'
		AND payment_subscription_environment(user_subscriptions)=$4
		AND (paypal_renewal_failed_at IS NULL OR paypal_renewal_failed_at<$2)
		AND (last_payment_time IS NULL OR last_payment_time<$2)
		AND status NOT IN ('CANCELLED','EXPIRED')`, subscriptionID, failedAt, observedStatus, conversion.Environment())
	return err
}
