package application

import (
	"context"
	"errors"
	"log"
	"net/http"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"reader-backend/internal/db"
	"reader-backend/internal/payment"
	"reader-backend/internal/payment/paypal"
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

// A persisted switch is resolved before checking whether the destination
// can still accept a new checkout. Regardless of which channel the buyer clicks
// next, finish closing the previous channel first rather than leaving the intent
// permanently wedged on a mismatched switch_to_provider.
func (s *service) resumeVIPSwitch(ctx context.Context, row *vipCheckoutIntentRow, _ string) error {
	if row.switchTo == "" {
		return nil
	}
	if err := s.switchVIPChannel(ctx, *row, row.switchTo); err != nil {
		return err
	}
	row.activeProvider, row.switchTo = "", ""
	row.paypalIntentID, row.stripeIntentID, row.subscriptionID = "", "", ""
	return nil
}

func (s *service) switchVIPChannel(ctx context.Context, row vipCheckoutIntentRow, target string) error {
	if row.switchTo == "" && !slices.Contains(loadAvailableProductChannels(ctx, row.productID, "vip", currentPaymentLedgerEnvironment()), target) {
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
		WHERE `+vipSwitchSourcePredicate+` AND COALESCE(switch_to_provider,'')=$7`,
		row.intentID, row.userID, row.environment, row.activeProvider, source, target, row.switchTo)
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

// The binding lock is held through the first-send marker, so a CMS update
// either wins the eligibility check or follows an already claimed send.
const paypalVIPBindingMatchSQL = `
	SELECT 1 FROM product_channel_bindings b
	JOIN payment_channel_accounts a ON a.id=i.checkout_account_id AND a.provider='paypal'
	WHERE b.product_id=i.product_id AND b.account_id=i.checkout_account_id
	  AND b.provider='paypal' AND b.environment=a.environment
	  AND b.enabled AND b.validation_status='verified' AND b.external_price_ref=i.plan_id
`

const paypalVIPBindingForShare = `EXISTS (` + paypalVIPBindingMatchSQL + ` FOR SHARE OF b)`

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
		var allowed bool
		if err := tx.QueryRow(ctx, `SELECT `+paypalVIPBindingForShare+`
			FROM paypal_subscription_intents i WHERE i.intent_id=$1`, intent.id).Scan(&allowed); err != nil {
			return nil, err
		}
		if !allowed {
			return nil, ErrCheckoutPaused
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

func isUnapprovedPayPalCancelRejected(err error) bool {
	var providerErr *paypal.APIError
	if !errors.As(err, &providerErr) || providerErr.Authentication {
		return false
	}
	if providerErr.HasIssue("SUBSCRIPTION_STATUS_INVALID") {
		return true
	}
	// PayPal Subscriptions v1 rejects /cancel on APPROVAL_PENDING agreements with
	// HTTP 404 RESOURCE_NOT_FOUND (INVALID_RESOURCE_ID) even though GET confirms
	// the subscription exists in APPROVAL_PENDING. Caller always re-reads GET to
	// verify status remains APPROVAL_PENDING with no payment before detaching.
	return providerErr.Status == http.StatusNotFound &&
		(providerErr.HasIssue("RESOURCE_NOT_FOUND") || providerErr.HasIssue("INVALID_RESOURCE_ID"))
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
			log.Printf("task=vip_switch action=paypal_scope intent=%s sub=%s error=%v", row.intentID, subID, err)
			return ErrCheckoutPending
		}
		read := func() (*payment.SubscriptionDetails, error) {
			details, err := client.GetSubscription(ctx, subID)
			if err != nil || details == nil || details.SubscriptionID != subID ||
				details.PlanID != planID || details.CustomID != row.paypalIntentID {
				log.Printf("task=vip_switch action=read_paypal intent=%s sub=%s err=%v", row.intentID, subID, err)
				return nil, ErrCheckoutPending
			}
			return details, nil
		}
		details, err := read()
		if err != nil {
			return err
		}
		if details.Status == "APPROVAL_PENDING" && details.LastPaymentTime == nil {
			cancelErr := client.CancelSubscription(ctx, subID, "User switched VIP checkout to Stripe")
			unapprovedNotCancellable := isUnapprovedPayPalCancelRejected(cancelErr)
			if cancelErr != nil && !unapprovedNotCancellable {
				log.Printf("task=vip_switch action=cancel_paypal intent=%s sub=%s error=%v", row.intentID, subID, cancelErr)
				return ErrCheckoutPending
			}
			details, err = read()
			if err != nil {
				return err
			}
			// PayPal Subscriptions API rejects /cancel on APPROVAL_PENDING (with HTTP 422
			// SUBSCRIPTION_STATUS_INVALID or HTTP 404 RESOURCE_NOT_FOUND / INVALID_RESOURCE_ID)
			// because unapproved agreements cannot be cancelled via API. Re-reading confirms
			// it is still unapproved with no payment.
			if unapprovedNotCancellable && details.Status == "APPROVAL_PENDING" && details.LastPaymentTime == nil {
				return closeUnifiedPayPalChild(ctx, row, subID, false)
			}
		}
		if details.LastPaymentTime != nil || details.Status == "APPROVED" || details.Status == "ACTIVE" || details.Status == "SUSPENDED" {
			if err := releaseVIPSwitch(ctx, row); err != nil {
				return err
			}
			return ErrSubscriptionConflict
		}
		if details.Status != "CANCELLED" && details.Status != "EXPIRED" {
			log.Printf("task=vip_switch action=unexpected_paypal_status intent=%s sub=%s status=%s", row.intentID, subID, details.Status)
			return ErrCheckoutPending
		}
	}
	return closeUnifiedPayPalChild(ctx, row, subID, true)
}

func closeUnifiedPayPalChild(ctx context.Context, row vipCheckoutIntentRow, subID string, remoteConfirmed bool) error {
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
		terminationSource := "local_unapproved_switch"
		if remoteConfirmed {
			terminationSource = "provider_confirmed"
		}
		tag, err = tx.Exec(ctx, `UPDATE user_subscriptions SET status='CANCELLED',
			cancelled_at=COALESCE(cancelled_at,now()),
			cancel_reason=COALESCE(NULLIF(cancel_reason,''), 'Superseded by VIP checkout switch'),
			attribution=COALESCE(attribution,'{}'::jsonb) || jsonb_build_object(
				'superseded_vip_intent_id', $4::text,
				'termination_source', $5::text,
				'remote_termination_confirmed', $6::boolean
			),
			next_billing_time=NULL,updated_at=now()
			WHERE subscription_id=$1 AND user_id=$2 AND checkout_intent_id=$3::uuid AND payment_method='paypal'
			  AND status IN ('PENDING','CANCELLED','EXPIRED') AND last_payment_time IS NULL
			  AND paypal_activation_requested_at IS NULL`, subID, row.userID, row.paypalIntentID, row.intentID, terminationSource, remoteConfirmed)
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
		log.Printf("task=vip_switch action=stripe_account intent=%s stripe_intent=%s error=%v", row.intentID, row.stripeIntentID, err)
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
			log.Printf("task=vip_switch action=stripe_adapter intent=%s stripe_intent=%s ok=%v can_close=%v", row.intentID, intent.ID, ok, canClose)
			return ErrCheckoutPending
		}
		if sessionID == "" {
			sessionID, err = findUnidentifiedStripeVIPCheckout(ctx, adapter, intent)
			if err != nil || sessionID == "" {
				log.Printf("task=vip_switch action=find_stripe_session intent=%s stripe_intent=%s error=%v", row.intentID, intent.ID, err)
				return ErrCheckoutPending
			}
		}
		proof, err := closer.CloseStripeVIPCheckout(ctx, sessionID, intent.ID, intent.Offer.SalesMode)
		if err != nil || proof.ID != sessionID || proof.IntentID != intent.ID {
			log.Printf("task=vip_switch action=close_stripe intent=%s stripe_intent=%s session=%s error=%v", row.intentID, intent.ID, sessionID, err)
			return ErrCheckoutPending
		}
		if proof.SubscriptionID != "" {
			if err := releaseVIPSwitch(ctx, row); err != nil {
				return err
			}
			return ErrSubscriptionConflict
		}
		if !proof.Expired {
			log.Printf("task=vip_switch action=stripe_not_expired intent=%s stripe_intent=%s session=%s awaiting_buyer=%v", row.intentID, intent.ID, sessionID, proof.AwaitingBuyer)
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
		log.Printf("task=vip_switch action=update_stripe_intent intent=%s stripe_intent=%s rows=%d", row.intentID, intent.ID, tag.RowsAffected())
		return ErrCheckoutPending
	}
	if err := detachVIPChannelTx(ctx, tx, row); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
