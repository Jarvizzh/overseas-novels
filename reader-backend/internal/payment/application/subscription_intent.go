package application

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"reader-backend/internal/conversion"
	"reader-backend/internal/db"
	"reader-backend/internal/model"
	"reader-backend/internal/novel"
	"reader-backend/internal/payment"
)

var ErrSubscriptionConflict = errors.New("Finish or cancel the existing VIP subscription before starting another.")
var ErrSubscriptionReview = errors.New("PayPal checkout requires reconciliation before it can be retried")
var ErrSubscriptionPending = errors.New("PayPal checkout is still being prepared. Retry the same package shortly.")

type subscriptionIntent struct {
	id              string
	userID          int64
	productID       int64
	productSnapshot []byte
	slotID          int
	templateID      int
	priceCents      int
	cycle           string
	returnURL       string
	cancelURL       string
	returnPath      string
	planID          string
	offerVersionID  *int
	subscriptionID  string
	approvalURL     string
	leaseActive     bool
	needsReview     bool
	account         payment.WebhookAccount
	accountID       *int64
	attribution     checkoutAttribution
}

func (r *dbRepository) reserveSubscriptionIntent(ctx context.Context, proposed subscriptionIntent) (*subscriptionIntent, error) {
	evaluation := checkoutEvaluation{stage: subscriptionIntentStage, userID: proposed.userID, provider: payment.ProviderPayPal, slotID: proposed.slotID}
	return evaluateCheckout(ctx, evaluation, func(tx pgx.Tx, e *checkoutEvaluation) (*subscriptionIntent, error) {
		return r.reservePayPalSubscriptionTx(ctx, tx, e, proposed)
	})
}

func (r *dbRepository) reservePayPalSubscriptionTx(ctx context.Context, tx pgx.Tx, evaluation *checkoutEvaluation, proposed subscriptionIntent) (*subscriptionIntent, error) {
	// An unsubmitted checkout cannot have created a provider subscription.
	if _, err := tx.Exec(ctx, `DELETE FROM paypal_subscription_intents WHERE user_id=$1 AND slot_id<>$2 AND checkout_environment=$3
 AND submitted_at IS NULL AND subscription_id IS NULL AND COALESCE(lease_until,now())<=now()
 AND NOT EXISTS(SELECT 1 FROM payment_vip_checkout_intents v WHERE v.paypal_intent_id=paypal_subscription_intents.intent_id AND v.status='Pending')`, proposed.userID, proposed.slotID, conversion.Environment()); err != nil {
		return nil, err
	}
	_, err := tx.Exec(ctx, `DELETE FROM paypal_subscription_intents i USING user_subscriptions s
		WHERE i.user_id=$1 AND i.checkout_environment=$2 AND s.user_id=i.user_id AND s.subscription_id=i.subscription_id
		AND s.status IN ('CANCELLED','EXPIRED')
		AND NOT EXISTS(SELECT 1 FROM payment_vip_checkout_intents v WHERE v.paypal_intent_id=i.intent_id AND v.status='Pending')`, proposed.userID, conversion.Environment())
	if err != nil {
		return nil, err
	}
	var current subscriptionIntent
	err = tx.QueryRow(ctx, `SELECT intent_id::text,user_id,slot_id,template_id,cycle,price_cents,return_url,cancel_url,
		COALESCE(plan_id,''),COALESCE(subscription_id,''),COALESCE(approval_url,''),
		COALESCE(lease_until>now(),false),COALESCE(submitted_at<now()-interval '24 hours',false),checkout_account_id,vip_offer_version_id
		FROM paypal_subscription_intents WHERE user_id=$1 AND checkout_environment=$2 FOR UPDATE`, proposed.userID, conversion.Environment()).
		Scan(&current.id, &current.userID, &current.slotID, &current.templateID, &current.cycle, &current.priceCents,
			&current.returnURL, &current.cancelURL, &current.planID, &current.subscriptionID, &current.approvalURL, &current.leaseActive, &current.needsReview, &current.accountID, &current.offerVersionID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	evaluation.checkoutID = current.id
	evaluation.resumed = current.id != ""
	if current.id != "" {
		var switching bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM payment_vip_checkout_intents
			WHERE paypal_intent_id=$1 AND switch_to_provider IS NOT NULL)`, current.id).Scan(&switching); err != nil {
			return nil, err
		}
		if switching {
			return nil, ErrSubscriptionPending
		}
	}
	if err := checkVIPCheckout(ctx, tx, evaluation, current, proposed.slotID); err != nil {
		return nil, err
	}
	if proposed.account.AccountKey != "" {
		var enabled bool
		if err := tx.QueryRow(ctx, `SELECT id,new_subscriptions_enabled FROM payment_channel_accounts WHERE provider='paypal' AND account_key=$1 AND environment=$2`,
			proposed.account.AccountKey, proposed.account.Environment).Scan(&proposed.accountID, &enabled); err != nil {
			return nil, err
		}
		evaluation.accountID = *proposed.accountID
		if current.id == "" && !enabled {
			return nil, ErrCheckoutPaused
		}
		if current.accountID != nil && *current.accountID != *proposed.accountID {
			return nil, ErrSubscriptionReview
		}
	}
	if current.id == "" {
		current = proposed
		if current.accountID != nil {
			ledgerEnv := proposed.account.Environment
			if ledgerEnv == "" {
				ledgerEnv = currentPaymentLedgerEnvironment()
			}
			err := tx.QueryRow(ctx, `SELECT p.id, COALESCE(p.vip_cycle, ''), p.price_cents, b.external_price_ref,
				jsonb_build_object(
					'product_id', p.id, 'code', p.code, 'product_type', p.product_type,
					'name', p.name, 'description', p.description, 'price_cents', p.price_cents,
					'currency', p.currency, 'coins', p.coins, 'bonus', p.bonus,
					'vip_cycle', COALESCE(p.vip_cycle, ''), 'slot_id', s.id,
					'badge', s.badge, 'display_alias', s.display_alias
				)
				FROM recharge_slots s
				JOIN products p ON p.id=s.product_id
				JOIN product_publications pub ON pub.product_id=p.id AND pub.environment=$4 AND pub.status='published'
				JOIN product_channel_bindings b ON b.product_id=p.id AND b.account_id=$3 AND b.provider='paypal' AND b.environment=$4
				WHERE s.id=$1 AND s.template_id=$2 AND p.product_type='vip'
				  AND b.enabled=true AND b.validation_status='verified' AND b.external_price_ref<>''
				FOR SHARE OF s`,
				current.slotID, current.templateID, current.accountID, ledgerEnv).
				Scan(&current.productID, &current.cycle, &current.priceCents, &current.planID, &current.productSnapshot)
			if errors.Is(err, pgx.ErrNoRows) {
				err = tx.QueryRow(ctx, `SELECT v.slot_id, v.cycle, v.amount_minor, p.plan_id
					FROM recharge_slots s JOIN payment_vip_offer_versions v ON v.slot_id=s.vip_offer_version_id
					JOIN payment_paypal_vip_plans p ON p.slot_id=v.slot_id
					WHERE s.id=$1 AND ($2=0 OR s.template_id=$2) AND s.vip_offer_version_id IS NOT NULL
					  AND p.account_id=$3 AND p.plan_id IS NOT NULL FOR SHARE OF s`,
					current.slotID, current.templateID, current.accountID).Scan(&current.offerVersionID, &current.cycle, &current.priceCents, &current.planID)
			}
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, ErrCheckoutUnavailable
			}
			if err != nil {
				return nil, err
			}
		}
		current.id = uuid.NewString()
		current.returnURL = appendVIPReturnReference(current.returnURL, current.id)
		current.cancelURL = appendVIPReturnReference(current.cancelURL, current.id)
		metadata, err := json.Marshal(current.attribution)
		if err != nil {
			return nil, err
		}
		var snapArg *string
		if len(current.productSnapshot) > 0 {
			s := string(current.productSnapshot)
			snapArg = &s
		}
		_, err = tx.Exec(ctx, `INSERT INTO paypal_subscription_intents
			(user_id,intent_id,product_id,product_snapshot,slot_id,template_id,cycle,price_cents,return_url,cancel_url,attribution,checkout_account_id,checkout_return_path,plan_id,vip_offer_version_id,checkout_environment)
 SELECT $1,$2,NULLIF($15,0),COALESCE($16::jsonb,'{}'::jsonb),$3,$4,$5,$6,$7,$8,$9::jsonb || jsonb_build_object('pixel_id',fp.pixel_id,'promotion_link_id',p.id,'novel_id',p.novel_id,'utm_source',u.utm_source,'utm_campaign',u.utm_campaign)
	 ,$10,$11,NULLIF($12,''),$13,$14
 FROM users u LEFT JOIN LATERAL reader_promotion_for_user(u.id) p ON true LEFT JOIN fb_pixels fp ON fp.id=p.fb_pixel_id WHERE u.id=$1`, current.userID, current.id, current.slotID, current.templateID,
			current.cycle, current.priceCents, current.returnURL, current.cancelURL, metadata, current.accountID, current.returnPath, current.planID, current.offerVersionID, conversion.Environment(), current.productID, snapArg)
		if err != nil {
			return nil, err
		}
	}
	if current.approvalURL != "" {
		return &current, nil
	}
	if current.leaseActive {
		return nil, ErrSubscriptionPending
	}
	if current.needsReview {
		return nil, ErrSubscriptionReview
	}
	_, err = tx.Exec(ctx, `UPDATE paypal_subscription_intents SET lease_until=now()+interval '5 minutes' WHERE intent_id=$1`, current.id)
	if err != nil {
		return nil, err
	}
	evaluation.checkoutID = current.id
	return &current, nil
}

func appendVIPReturnReference(rawURL, intentID string) string {
	if rawURL == "" {
		return ""
	}
	target, err := url.Parse(rawURL)
	if err != nil || target.Host == "" || target.User != nil || (target.Scheme != "https" && target.Scheme != "http") {
		return rawURL
	}
	query := target.Query()
	query.Set("vip_purchase", intentID)
	target.RawQuery = query.Encode()
	return target.String()
}

// Prefer the original fulfillment order recorded on the agreement. Legacy
// agreements may lack that pointer, so use their earliest verified paid period.
const paypalVIPFirstOrderJoin = `LEFT JOIN LATERAL (
	SELECT o.status AS order_status FROM recharge_orders o
	WHERE o.subscription_id=user_subscriptions.subscription_id AND o.user_id=user_subscriptions.user_id
	  AND o.order_type='subscription' AND o.payment_method='paypal'
	  AND (o.id=user_subscriptions.capi_subscribe_order_id OR (user_subscriptions.capi_subscribe_order_id IS NULL
	    AND (o.fulfilled_at IS NOT NULL OR (o.status='Refunded' AND o.paid_period_end IS NOT NULL))))
	ORDER BY o.paid_at NULLS LAST,o.id LIMIT 1
) first_order ON true`

const paypalVIPPurchaseRows = `
	SELECT COALESCE(checkout_intent_id::text,'') AS intent_id,subscription_id AS external_id,COALESCE(slot_id,0) AS slot,
		price_cents AS amount,currency,
		CASE WHEN first_order.order_status='Refunded' THEN 'refunded'
		     WHEN first_order.order_status IS NOT NULL OR ` + novel.PaidSubscriptionPredicate + ` THEN 'completed'
		     WHEN status IN ('CANCELLED','EXPIRED') THEN 'closed'
		     WHEN EXISTS(SELECT 1 FROM payment_vip_checkout_intents v WHERE v.paypal_intent_id=user_subscriptions.checkout_intent_id
		          AND v.switch_to_provider IS NOT NULL) THEN 'confirming'
		     WHEN status='PENDING' AND COALESCE(approval.url,'')<>'' THEN 'awaiting_approval'
		     ELSE 'confirming' END AS checkout_state,
		checkout_return_path AS return_path,COALESCE(approval.url,'') AS approval_url,created_at
	FROM user_subscriptions
	` + paypalVIPFirstOrderJoin + `
	LEFT JOIN LATERAL (SELECT i.approval_url AS url FROM paypal_subscription_intents i WHERE i.intent_id=user_subscriptions.checkout_intent_id
		AND (i.checkout_account_id IS NULL OR EXISTS(SELECT 1 FROM payment_channel_accounts a
		    WHERE a.id=i.checkout_account_id AND a.provider='paypal' AND a.environment=CASE $2 WHEN 'production' THEN 'live' ELSE $2 END))) approval ON true
	WHERE user_id=$1 AND payment_method='paypal' AND subscription_id NOT LIKE 'I-MOCK%'
	AND payment_subscription_environment(user_subscriptions)=$2
	UNION ALL
	SELECT i.intent_id::text,COALESCE(i.subscription_id,''),i.slot_id,i.price_cents,'USD',
		CASE WHEN EXISTS(SELECT 1 FROM payment_vip_checkout_intents v WHERE v.paypal_intent_id=i.intent_id
		          AND v.switch_to_provider IS NOT NULL) THEN 'confirming'
		     WHEN COALESCE(i.approval_url,'')<>'' THEN 'awaiting_approval'
		     WHEN i.submitted_at<now()-interval '24 hours' THEN 'review' ELSE 'confirming' END,
		i.checkout_return_path,COALESCE(i.approval_url,''),COALESCE(i.submitted_at,i.lease_until,now())
	FROM paypal_subscription_intents i
	WHERE i.user_id=$1 AND NOT EXISTS(SELECT 1 FROM user_subscriptions s WHERE s.user_id=i.user_id
		AND (s.checkout_intent_id=i.intent_id OR s.subscription_id=i.subscription_id))
	AND i.checkout_environment=$2
`

func scanPayPalVIPPurchase(row pgx.Row) (PurchaseIntent, error) {
	result := PurchaseIntent{OrderType: "subscription", Provider: payment.ProviderPayPal}
	err := row.Scan(&result.ID, &result.ExternalOrderID, &result.SlotID, &result.AmountMinor, &result.Currency, &result.Status, &result.ReturnPath, &result.ApprovalURL)
	if result.Status != "awaiting_approval" {
		result.ApprovalURL = ""
	}
	return result, err
}

const paypalVIPPurchaseFields = "intent_id,external_id,slot,amount,currency,checkout_state,return_path,approval_url"

func (r *dbRepository) getVIPPurchase(ctx context.Context, userID int64, intentID, externalID string) (*PurchaseIntent, error) {
	result, err := scanPayPalVIPPurchase(db.Pool(ctx).QueryRow(ctx, `SELECT `+paypalVIPPurchaseFields+` FROM (`+paypalVIPPurchaseRows+`) purchases
		WHERE ($3='' OR intent_id=$3) AND ($4='' OR external_id=$4) ORDER BY created_at DESC LIMIT 1`, userID, conversion.Environment(), intentID, externalID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrCheckoutUnavailable
	}
	if err != nil {
		return nil, err
	}
	if externalID != "" && result.Status == "awaiting_approval" {
		result.Status = "confirming"
		result.ApprovalURL = ""
	}
	return &result, nil
}

func (r *dbRepository) recentPayPalVIPPurchases(ctx context.Context, userID int64) ([]PurchaseIntent, error) {
	rows, err := db.Pool(ctx).Query(ctx, `SELECT `+paypalVIPPurchaseFields+` FROM (`+paypalVIPPurchaseRows+`) purchases
		WHERE intent_id<>''
		  AND NOT EXISTS (SELECT 1 FROM payment_vip_checkout_intents vc WHERE vc.paypal_intent_id::text=purchases.intent_id)
		ORDER BY created_at DESC LIMIT 20`, userID, conversion.Environment())
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (PurchaseIntent, error) { return scanPayPalVIPPurchase(row) })
}

func (r *dbRepository) claimVIPActivation(ctx context.Context, subscriptionID string, accounts ...payment.WebhookAccount) (bool, error) {
	var scope payment.WebhookAccount
	if len(accounts) != 0 {
		scope = accounts[0]
	}
	tag, err := db.Pool(ctx).Exec(ctx, `UPDATE user_subscriptions AS s SET paypal_activation_requested_at=now()
		WHERE s.subscription_id=$1 AND s.payment_method='paypal' AND s.status='PENDING'
		AND s.paypal_activation_requested_at IS NULL
		AND NOT EXISTS (SELECT 1 FROM payment_vip_checkout_intents v WHERE v.paypal_intent_id=s.checkout_intent_id
		 AND v.switch_to_provider IS NOT NULL)
		AND (s.checkout_account_id IS NULL OR EXISTS(
			SELECT 1 FROM payment_channel_accounts a WHERE a.id=s.checkout_account_id AND a.provider='paypal' AND
			((a.account_key=$2 AND a.environment=$3) OR ($2='' AND EXISTS(SELECT 1 FROM paypal_identity_lock p
				WHERE a.account_key=p.client_id AND a.environment=p.environment)))))`, subscriptionID, scope.AccountKey, scope.Environment)
	return tag.RowsAffected() == 1, err
}

func (r *dbRepository) releaseSubscriptionIntent(ctx context.Context, id string) {
	_, _ = db.Pool(ctx).Exec(ctx, `UPDATE paypal_subscription_intents SET lease_until=NULL WHERE intent_id=$1 AND subscription_id IS NULL`, id)
}

func (r *dbRepository) submitSubscriptionIntent(ctx context.Context, id, planID string) error {
	tag, err := db.Pool(ctx).Exec(ctx, `UPDATE paypal_subscription_intents i SET plan_id=$2,submitted_at=COALESCE(submitted_at,now())
		WHERE intent_id=$1 AND (plan_id IS NULL OR plan_id=$2) AND subscription_id IS NULL
		AND (i.submitted_at IS NOT NULL OR i.product_id IS NULL OR `+paypalVIPBindingForShare+`)
		AND NOT EXISTS(SELECT 1 FROM payment_vip_checkout_intents v WHERE v.paypal_intent_id=$1 AND v.switch_to_provider IS NOT NULL)`, id, planID)
	if err == nil && tag.RowsAffected() != 1 {
		return ErrSubscriptionConflict
	}
	return err
}

func (r *dbRepository) finishSubscriptionIntent(ctx context.Context, intent *subscriptionIntent, result *payment.SubscriptionResult) error {
	if result.SubscriptionID == "" || result.ApproveURL == "" {
		return errors.New("PayPal did not return a subscription approval link")
	}
	tx, err := db.Pool(ctx).Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	var savedSubscription, savedApproval *string
	err = tx.QueryRow(ctx, "SELECT subscription_id,approval_url FROM paypal_subscription_intents WHERE intent_id=$1 FOR UPDATE",
		intent.id).Scan(&savedSubscription, &savedApproval)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && savedSubscription != nil && *savedSubscription != result.SubscriptionID) {
		return ErrSubscriptionConflict
	}
	if err != nil {
		return err
	}
	firstApproval := savedApproval == nil || *savedApproval == ""
	tag, err := tx.Exec(ctx, `UPDATE paypal_subscription_intents SET subscription_id=$2,approval_url=$3,lease_until=NULL
		WHERE intent_id=$1 AND (subscription_id IS NULL OR subscription_id=$2)`, intent.id, result.SubscriptionID, result.ApproveURL)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrSubscriptionConflict
	}
	now := time.Now().UTC()
	sub := model.UserSubscription{UserID: intent.userID, SubscriptionID: result.SubscriptionID, PlanID: intent.planID,
		SlotID: intent.slotID, TemplateID: intent.templateID, Status: "PENDING", Cycle: intent.cycle,
		PriceCents: intent.priceCents, Currency: "USD", PaymentMethod: "paypal", CurrentPeriodStart: &now}
	tag, err = tx.Exec(ctx, `INSERT INTO user_subscriptions(user_id,subscription_id,plan_id,slot_id,template_id,status,cycle,price_cents,currency,payment_method,current_period_start,attribution,capi_subscribe_state,checkout_account_id,checkout_intent_id,checkout_return_path,vip_offer_version_id,product_id,product_snapshot)
	 SELECT $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,attribution,'eligible',checkout_account_id,intent_id,checkout_return_path,vip_offer_version_id,product_id,product_snapshot FROM paypal_subscription_intents WHERE intent_id=$12
	 ON CONFLICT(subscription_id) DO UPDATE SET updated_at=user_subscriptions.updated_at
		WHERE user_subscriptions.user_id=EXCLUDED.user_id AND user_subscriptions.plan_id=EXCLUDED.plan_id`,
		sub.UserID, sub.SubscriptionID, sub.PlanID, sub.SlotID, sub.TemplateID, sub.Status, sub.Cycle, sub.PriceCents, sub.Currency, sub.PaymentMethod, sub.CurrentPeriodStart, intent.id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrSubscriptionConflict
	}
	if _, err := tx.Exec(ctx, `UPDATE payment_vip_checkout_intents SET subscription_id=$2,updated_at=now() WHERE paypal_intent_id=$1::uuid`,
		intent.id, result.SubscriptionID); err != nil {
		return err
	}
	if firstApproval {
		_, err = tx.Exec(ctx, `
			INSERT INTO reader_conversion_outbox (business_key,event_name,intent_id,environment,occurred_at,snapshot)
			SELECT 'subscription-intent:'||i.intent_id||':InitiateCheckout','InitiateCheckout',i.intent_id::text,$2,clock_timestamp(),
				jsonb_build_object('user_id',u.id::text,'email',u.email,'amount_cents',i.price_cents,'currency','USD',
					'order_type','subscription','promotion_id',i.attribution->>'promotion_link_id','action_source','website',
					'attribution',i.attribution,
					'attribution_state',CASE WHEN NULLIF(i.attribution->>'pixel_id','') IS NOT NULL THEN 'matched'
						WHEN NULLIF(i.attribution->>'promotion_link_id','') IS NOT NULL
							OR COALESCE(i.attribution->>'utm_source','')<>'' OR COALESCE(i.attribution->>'utm_campaign','')<>''
							OR COALESCE(i.attribution->>'fbc','')<>'' THEN 'missing_pixel' ELSE 'organic' END)
			FROM paypal_subscription_intents i JOIN users u ON u.id=i.user_id WHERE i.intent_id=$1
			ON CONFLICT (business_key) DO NOTHING`, intent.id, conversion.Environment())
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
