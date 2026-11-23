package application

import (
	"context"
	"errors"
	"log"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"reader-backend/internal/db"
	"reader-backend/internal/payment"
)

const (
	payPalVIPCreateRetryWindow = 24 * time.Hour
	payPalVIPCreateLease       = 5 * time.Minute
	payPalVIPRequestTimeout    = 2 * time.Minute
)

func vipSwitchSource(row vipCheckoutIntentRow) string {
	if row.activeProvider == payment.ProviderPayPal {
		return row.paypalIntentID
	}
	return row.stripeIntentID
}

const vipSwitchSourcePredicate = `intent_id=$1 AND user_id=$2 AND checkout_environment=$3 AND status='Pending'
	AND active_provider=$4 AND CASE WHEN $4='paypal' THEN paypal_intent_id=$5::uuid ELSE stripe_intent_id=$5::uuid END`

func (s *service) switchVIPChannel(ctx context.Context, row vipCheckoutIntentRow, target string) error {
	if !slices.Contains(loadAvailableProductChannels(ctx, row.productID, "vip", currentPaymentLedgerEnvironment()), target) {
		return ErrCheckoutPaused
	}
	if err := beginVIPSwitch(ctx, row, target); err != nil {
		return err
	}
	row.switchTo = target
	ctx, cancel := context.WithTimeout(ctx, payPalVIPRequestTimeout)
	defer cancel()
	if row.activeProvider == payment.ProviderPayPal {
		return s.closePayPalVIPForSwitch(ctx, row)
	}
	return s.closeStripeVIPForSwitch(ctx, row)
}

func beginVIPSwitch(ctx context.Context, row vipCheckoutIntentRow, target string) error {
	source := vipSwitchSource(row)
	if source == "" {
		return ErrCheckoutPending
	}
	tx, err := db.Pool(ctx).Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	tag, err := tx.Exec(ctx, `UPDATE payment_vip_checkout_intents
		SET switch_to_provider=$6, updated_at=now()
		WHERE `+vipSwitchSourcePredicate+` AND (switch_to_provider IS NULL OR switch_to_provider=$6)`,
		row.intentID, row.userID, row.environment, row.activeProvider, source, target)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrCheckoutPending
	}
	if row.switchTo != target {
		if err := validateVIPQuoteTx(ctx, tx, row.userID, row.productID, row.slotID, row.templateID, row.priceCents, row.cycle); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func releaseVIPSwitch(ctx context.Context, row vipCheckoutIntentRow) error {
	_, err := db.Pool(ctx).Exec(ctx, `UPDATE payment_vip_checkout_intents SET switch_to_provider=NULL,updated_at=now()
		WHERE `+vipSwitchSourcePredicate+` AND switch_to_provider=$6`,
		row.intentID, row.userID, row.environment, row.activeProvider, vipSwitchSource(row), row.switchTo)
	return err
}

func detachVIPChannelTx(ctx context.Context, tx pgx.Tx, row vipCheckoutIntentRow) error {
	tag, err := tx.Exec(ctx, `UPDATE payment_vip_checkout_intents
		SET active_provider=NULL,paypal_intent_id=NULL,stripe_intent_id=NULL,subscription_id=NULL,switch_to_provider=NULL,updated_at=now()
		WHERE `+vipSwitchSourcePredicate+` AND switch_to_provider=$6`,
		row.intentID, row.userID, row.environment, row.activeProvider, vipSwitchSource(row), row.switchTo)
	if err == nil && tag.RowsAffected() != 1 {
		return ErrCheckoutPending
	}
	return err
}

// The create lease and the first-send marker are written together, before any
// provider call. A switch can detach an unidentified child only if it never sent.
func claimUnifiedPayPalVIP(ctx context.Context, row vipCheckoutIntentRow, switching bool) (*subscriptionIntent, error) {
	tx, err := db.Pool(ctx).Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))

	var intent subscriptionIntent
	var submitted, enabled bool
	var switchTo string
	intent.id, intent.userID = row.paypalIntentID, row.userID
	err = tx.QueryRow(ctx, `SELECT i.slot_id,i.template_id,i.price_cents,i.cycle,i.return_url,i.cancel_url,
		COALESCE(i.plan_id,''),COALESCE(i.subscription_id,''),i.checkout_account_id,a.account_key,a.environment,
		i.submitted_at IS NOT NULL,COALESCE(i.lease_until>now(),false),
		COALESCE(i.submitted_at<=now()-make_interval(secs=>$4::int),false),
		a.new_subscriptions_enabled AND a.status='published' AND a.is_primary,COALESCE(v.switch_to_provider,'')
		FROM paypal_subscription_intents i
		JOIN payment_channel_accounts a ON a.id=i.checkout_account_id AND a.provider='paypal'
		JOIN payment_vip_checkout_intents v ON v.paypal_intent_id=i.intent_id AND v.user_id=i.user_id
		  AND v.checkout_environment=i.checkout_environment
		WHERE v.intent_id=$1 AND v.user_id=$2 AND v.checkout_environment=$3 AND v.status='Pending'
		  AND v.active_provider='paypal' AND i.intent_id=$5 FOR UPDATE OF i`,
		row.intentID, row.userID, row.environment, int(payPalVIPCreateRetryWindow/time.Second), row.paypalIntentID).
		Scan(&intent.slotID, &intent.templateID, &intent.priceCents, &intent.cycle, &intent.returnURL, &intent.cancelURL,
			&intent.planID, &intent.subscriptionID, &intent.accountID, &intent.account.AccountKey, &intent.account.Environment,
			&submitted, &intent.leaseActive, &intent.needsReview, &enabled, &switchTo)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrCheckoutPending
	}
	if err != nil {
		return nil, err
	}
	if (!switching && switchTo != "") || (switching && switchTo != row.switchTo) {
		return nil, ErrCheckoutPending
	}
	if intent.subscriptionID != "" || (switching && !submitted) {
		return nil, nil
	}
	if intent.leaseActive {
		return nil, ErrSubscriptionPending
	}
	if intent.needsReview || intent.planID == "" {
		return nil, ErrSubscriptionReview
	}
	if !submitted {
		if !enabled {
			return nil, ErrCheckoutPaused
		}
		if err := validateVIPQuoteTx(ctx, tx, row.userID, row.productID, row.slotID, row.templateID, row.priceCents, row.cycle); err != nil {
			return nil, err
		}
	}
	_, err = tx.Exec(ctx, `UPDATE paypal_subscription_intents
		SET submitted_at=COALESCE(submitted_at,now()),lease_until=now()+make_interval(secs=>$2::int)
		WHERE intent_id=$1`, intent.id, int(payPalVIPCreateLease/time.Second))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &intent, nil
}

func (s *service) sendUnifiedPayPalVIP(ctx context.Context, row vipCheckoutIntentRow, switching bool) error {
	intent, err := claimUnifiedPayPalVIP(ctx, row, switching)
	if err != nil || intent == nil {
		return err
	}
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		s.repo.releaseSubscriptionIntent(releaseCtx, intent.id)
	}()
	provider, err := s.payPalForScope(ctx, intent.account)
	if err != nil {
		return ErrSubscriptionReview
	}
	request, cancel := context.WithTimeout(ctx, payPalVIPRequestTimeout)
	defer cancel()
	result, err := provider.CreateSubscription(request, payment.CreateSubscriptionParam{
		Account: intent.account, RequestID: intent.id, UserID: intent.userID, PlanID: intent.planID,
		SlotID: intent.slotID, PriceCents: int64(intent.priceCents), Currency: "USD", CustomID: intent.id,
		ReturnURL: intent.returnURL, CancelURL: intent.cancelURL,
	})
	if err != nil {
		log.Printf("task=vip_checkout provider=paypal intent=%s error=%v", intent.id, err)
		return ErrSubscriptionPending
	}
	return s.repo.finishSubscriptionIntent(ctx, intent, result)
}

func (s *service) closePayPalVIPForSwitch(ctx context.Context, row vipCheckoutIntentRow) error {
	// PayPal's original create request can be replayed inside its idempotency
	// window to recover a missing ID. An unsent child needs no provider call.
	if err := s.sendUnifiedPayPalVIP(ctx, row, true); err != nil {
		return err
	}
	var subID, planID string
	var account payment.WebhookAccount
	err := db.Pool(ctx).QueryRow(ctx, `SELECT COALESCE(i.subscription_id,''),COALESCE(i.plan_id,''),a.account_key,a.environment
		FROM paypal_subscription_intents i JOIN payment_channel_accounts a ON a.id=i.checkout_account_id
		WHERE i.intent_id=$1 AND i.user_id=$2 AND i.checkout_environment=$3 AND a.provider='paypal'`,
		row.paypalIntentID, row.userID, row.environment).Scan(&subID, &planID, &account.AccountKey, &account.Environment)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrCheckoutPending
	}
	if err != nil {
		return err
	}
	if subID != "" {
		client, err := s.payPalForScope(ctx, account)
		if err != nil {
			return ErrCheckoutPending
		}
		read := func() (*payment.SubscriptionDetails, error) {
			details, err := client.GetSubscription(ctx, subID)
			if err != nil || details == nil || details.SubscriptionID != subID ||
				details.PlanID != planID || details.CustomID != row.paypalIntentID {
				return nil, ErrCheckoutPending
			}
			return details, nil
		}
		details, err := read()
		if err != nil {
			return err
		}
		if details.Status == "APPROVAL_PENDING" && details.LastPaymentTime == nil {
			if err := client.CancelSubscription(ctx, subID, "User switched VIP checkout to Stripe"); err != nil {
				return ErrCheckoutPending
			}
			details, err = read()
			if err != nil {
				return err
			}
		}
		if details.LastPaymentTime != nil || details.Status == "APPROVED" || details.Status == "ACTIVE" || details.Status == "SUSPENDED" {
			if err := releaseVIPSwitch(ctx, row); err != nil {
				return err
			}
			return ErrSubscriptionConflict
		}
		if details.Status != "CANCELLED" && details.Status != "EXPIRED" {
			return ErrCheckoutPending
		}
	}
	return closeUnifiedPayPalChild(ctx, row, subID)
}

func closeUnifiedPayPalChild(ctx context.Context, row vipCheckoutIntentRow, subID string) error {
	tx, err := db.Pool(ctx).Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	tag, err := tx.Exec(ctx, `DELETE FROM paypal_subscription_intents WHERE intent_id=$1 AND user_id=$2
		AND checkout_environment=$3
		AND (($4='' AND subscription_id IS NULL AND submitted_at IS NULL) OR subscription_id=$4)`,
		row.paypalIntentID, row.userID, row.environment, subID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrCheckoutPending
	}
	if subID != "" {
		tag, err = tx.Exec(ctx, `UPDATE user_subscriptions SET status='CANCELLED',
			cancelled_at=COALESCE(cancelled_at,now()),next_billing_time=NULL,updated_at=now()
			WHERE subscription_id=$1 AND user_id=$2 AND checkout_intent_id=$3::uuid AND payment_method='paypal'
			  AND status IN ('PENDING','CANCELLED','EXPIRED') AND last_payment_time IS NULL
			  AND paypal_activation_requested_at IS NULL`, subID, row.userID, row.paypalIntentID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return ErrCheckoutPending
		}
	}
	if err := detachVIPChannelTx(ctx, tx, row); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *service) closeStripeVIPForSwitch(ctx context.Context, row vipCheckoutIntentRow) error {
	account, err := stripeVIPAccountForOwner(ctx, row.userID, row.stripeIntentID, true)
	if err != nil {
		return ErrCheckoutPending
	}
	intent, err := findStripeVIPIntent(ctx, db.Pool(ctx), account, row.stripeIntentID)
	if err != nil {
		return err
	}
	if intent.SubscriptionID != "" || intent.Status == "bound" {
		if err := releaseVIPSwitch(ctx, row); err != nil {
			return err
		}
		return ErrSubscriptionConflict
	}
	sessionID := intent.SessionID
	if sessionID != "" || intent.FirstSentAt != nil {
		adapter, ok := stripeCapabilityForAccount[payment.StripeVIPAdapter](s, account)
		closer, canClose := adapter.(interface {
			CloseStripeVIPCheckout(context.Context, string, string, string) (payment.StripeVIPCheckout, error)
		})
		if !ok || !canClose {
			return ErrCheckoutPending
		}
		if sessionID == "" {
			sessionID, err = findUnidentifiedStripeVIPCheckout(ctx, adapter, intent)
			if err != nil || sessionID == "" {
				return ErrCheckoutPending
			}
		}
		proof, err := closer.CloseStripeVIPCheckout(ctx, sessionID, intent.ID, intent.Offer.SalesMode)
		if err != nil || proof.ID != sessionID || proof.IntentID != intent.ID {
			return ErrCheckoutPending
		}
		if proof.SubscriptionID != "" {
			if err := releaseVIPSwitch(ctx, row); err != nil {
				return err
			}
			return ErrSubscriptionConflict
		}
		if !proof.Expired {
			return ErrCheckoutPending
		}
	}
	tx, err := db.Pool(ctx).Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	tag, err := tx.Exec(ctx, `UPDATE payment_stripe_subscription_intents SET status='closed',approval_url=NULL,
		checkout_ref=COALESCE(checkout_ref,NULLIF($4,''))
		WHERE id=$1 AND user_id=$2 AND checkout_environment=$3 AND subscription_ref IS NULL
		  AND status IN ('creating','buyer_action','closed')
		  AND (($4='' AND first_sent_at IS NULL AND checkout_ref IS NULL)
		    OR ($4<>'' AND (checkout_ref IS NULL OR checkout_ref=$4)))`,
		intent.ID, row.userID, row.environment, sessionID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrCheckoutPending
	}
	if err := detachVIPChannelTx(ctx, tx, row); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
