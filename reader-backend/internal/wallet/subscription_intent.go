package wallet

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"reader-backend/internal/conversion"
	"reader-backend/internal/db"
	"reader-backend/internal/model"
	"reader-backend/internal/payment"
)

var ErrSubscriptionConflict = errors.New("Finish or cancel the existing PayPal subscription before starting another.")
var ErrSubscriptionReview = errors.New("PayPal checkout requires reconciliation before it can be retried")
var ErrSubscriptionPending = errors.New("PayPal checkout is still being prepared. Retry the same package shortly.")

type subscriptionIntent struct {
	id             string
	userID         int64
	slotID         int
	templateID     int
	priceCents     int
	cycle          string
	returnURL      string
	cancelURL      string
	planID         string
	subscriptionID string
	approvalURL    string
	leaseActive    bool
	needsReview    bool
	attribution    checkoutAttribution
}

func (r *dbRepository) reserveSubscriptionIntent(ctx context.Context, proposed subscriptionIntent) (*subscriptionIntent, error) {
	tx, err := db.Pool(ctx).Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	// All checkout entry points share this account lock, including when
	// migration has left existing subscriptions without an intent.
	if _, err := tx.Exec(ctx, `SELECT id FROM users WHERE id=$1 FOR NO KEY UPDATE`, proposed.userID); err != nil {
		return nil, err
	}
	// An unsubmitted checkout cannot have created a provider subscription.
	if _, err := tx.Exec(ctx, `DELETE FROM paypal_subscription_intents WHERE user_id=$1 AND slot_id<>$2
 AND submitted_at IS NULL AND subscription_id IS NULL AND COALESCE(lease_until,now())<=now()`, proposed.userID, proposed.slotID); err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `DELETE FROM paypal_subscription_intents i USING user_subscriptions s
		WHERE i.user_id=$1 AND s.user_id=i.user_id AND s.subscription_id=i.subscription_id
		AND s.status IN ('CANCELLED','EXPIRED')`, proposed.userID)
	if err != nil {
		return nil, err
	}
	var current subscriptionIntent
	err = tx.QueryRow(ctx, `SELECT intent_id::text,user_id,slot_id,template_id,cycle,price_cents,return_url,cancel_url,
		COALESCE(plan_id,''),COALESCE(subscription_id,''),COALESCE(approval_url,''),
		COALESCE(lease_until>now(),false),COALESCE(submitted_at<now()-interval '24 hours',false)
		FROM paypal_subscription_intents WHERE user_id=$1 FOR UPDATE`, proposed.userID).
		Scan(&current.id, &current.userID, &current.slotID, &current.templateID, &current.cycle, &current.priceCents,
			&current.returnURL, &current.cancelURL, &current.planID, &current.subscriptionID, &current.approvalURL, &current.leaseActive, &current.needsReview)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	var conflicting bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM user_subscriptions
		WHERE user_id=$1 AND payment_method='paypal' AND status NOT IN ('CANCELLED','EXPIRED')
		AND subscription_id NOT LIKE 'I-MOCK%' AND (subscription_id<>$2 OR status<>'PENDING'))`,
		proposed.userID, current.subscriptionID).Scan(&conflicting); err != nil {
		return nil, err
	}
	if conflicting || (current.id != "" && current.slotID != proposed.slotID) {
		return nil, ErrSubscriptionConflict
	}
	if current.id == "" {
		current = proposed
		current.id = uuid.NewString()
		metadata, err := json.Marshal(current.attribution)
		if err != nil {
			return nil, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO paypal_subscription_intents
			(user_id,intent_id,slot_id,template_id,cycle,price_cents,return_url,cancel_url,attribution)
 SELECT $1,$2,$3,$4,$5,$6,$7,$8,$9::jsonb || jsonb_build_object('pixel_id',fp.pixel_id,'promotion_link_id',p.id,'novel_id',p.novel_id,'utm_source',u.utm_source,'utm_campaign',u.utm_campaign)
 FROM users u LEFT JOIN LATERAL reader_promotion_for_user(u.id) p ON true LEFT JOIN fb_pixels fp ON fp.id=p.fb_pixel_id WHERE u.id=$1`, current.userID, current.id, current.slotID, current.templateID,
			current.cycle, current.priceCents, current.returnURL, current.cancelURL, metadata)
		if err != nil {
			return nil, err
		}
	}
	if current.approvalURL != "" {
		return &current, tx.Commit(ctx)
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
	return &current, tx.Commit(ctx)
}

func (r *dbRepository) releaseSubscriptionIntent(ctx context.Context, id string) {
	_, _ = db.Pool(ctx).Exec(ctx, `UPDATE paypal_subscription_intents SET lease_until=NULL WHERE intent_id=$1 AND subscription_id IS NULL`, id)
}

func (r *dbRepository) submitSubscriptionIntent(ctx context.Context, id, planID string) error {
	tag, err := db.Pool(ctx).Exec(ctx, `UPDATE paypal_subscription_intents SET plan_id=$2,submitted_at=COALESCE(submitted_at,now())
		WHERE intent_id=$1 AND (plan_id IS NULL OR plan_id=$2) AND subscription_id IS NULL`, id, planID)
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
	tag, err = tx.Exec(ctx, `INSERT INTO user_subscriptions(user_id,subscription_id,plan_id,slot_id,template_id,status,cycle,price_cents,currency,payment_method,current_period_start,attribution,capi_subscribe_state)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,(SELECT attribution FROM paypal_subscription_intents WHERE intent_id=$12),'eligible') ON CONFLICT(subscription_id) DO UPDATE SET updated_at=user_subscriptions.updated_at
		WHERE user_subscriptions.user_id=EXCLUDED.user_id AND user_subscriptions.plan_id=EXCLUDED.plan_id`,
		sub.UserID, sub.SubscriptionID, sub.PlanID, sub.SlotID, sub.TemplateID, sub.Status, sub.Cycle, sub.PriceCents, sub.Currency, sub.PaymentMethod, sub.CurrentPeriodStart, intent.id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrSubscriptionConflict
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
