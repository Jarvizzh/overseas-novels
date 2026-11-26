package application

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
	"reader-backend/internal/conversion"
	"reader-backend/internal/db"
	"reader-backend/internal/novel"
	"reader-backend/internal/payment"
)

const vipPlanSwitchVerifyTimeout = 15 * time.Second

func recordVIPPlanSwitchBlockedDecision(ctx context.Context, userID int64, row vipCheckoutIntentRow, targetSlotID int) {
	if db.Pool(ctx) == nil {
		return
	}
	_, _ = db.Pool(ctx).Exec(ctx, `INSERT INTO payment_checkout_decisions
		(user_id,checkout_environment,stage,provider,checkout_id,slot_id,outcome,reason,rule_version)
		VALUES ($1,$2,$3,NULLIF($4,''),NULLIF($5,'')::uuid,NULLIF(GREATEST($6,0),0),$7,$8,$9)`,
		userID, conversion.Environment(), purchaseIntentStage, row.activeProvider, row.intentID,
		targetSlotID, checkoutBlock, reasonVIPConflict, checkoutRuleVersion)
}

// prepareVIPPlanReplacement verifies and safely terminates an unpaid active VIP
// checkout attempt on PayPal or Stripe before reserveVIPCheckoutIntentTx creates
// a replacement VIP plan intent. Provider calls happen outside the reservation
// transaction so DB locks are never held across remote HTTP requests.
func (s *service) prepareVIPPlanReplacement(ctx context.Context, userID int64, req purchaseRequest) error {
	pool := db.Pool(ctx)
	if pool == nil || req.nonVIPOnly {
		return nil
	}
	env := conversion.Environment()

	// Idempotent request replay must go straight to preparePurchase without probing providers.
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM payment_checkout_requests
		WHERE environment=$1 AND user_id=$2 AND request_key=$3)`, env, userID, req.key).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}

	// Read the target VIP product and any existing Pending VIP intent.
	readTx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer readTx.Rollback(context.WithoutCancel(ctx))
	prod, prodErr := resolveProductForCheckoutTx(ctx, readTx, userID, req.productID, req.slotID, currentPaymentLedgerEnvironment())
	if prodErr != nil || prod.OrderType != "subscription" {
		_ = readTx.Rollback(ctx)
		return nil
	}
	if err := validateVIPQuoteTx(ctx, readTx, userID, prod.ID, prod.SlotID, prod.TemplateID, int64(prod.PriceCents), prod.VipCycle); err != nil {
		_ = readTx.Rollback(ctx)
		return nil
	}
	// Preserve standard subscription_conflict when the user already owns an active or paid subscription.
	var hasActivePaid bool
	if err := readTx.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM user_subscriptions
		WHERE user_id=$1 AND payment_subscription_environment(user_subscriptions)=$2
		  AND (status IN ('ACTIVE','SUSPENDED','CANCEL_PENDING') OR (`+novel.PaidSubscriptionPredicate+`))
	)`, userID, env).Scan(&hasActivePaid); err != nil {
		_ = readTx.Rollback(ctx)
		return err
	}
	if hasActivePaid {
		_ = readTx.Rollback(ctx)
		return nil
	}
	row, rowErr := scanVIPCheckoutIntentRow(readTx.QueryRow(ctx, vipCheckoutIntentSelect+`
		WHERE v.user_id=$1 AND v.checkout_environment=$2 AND v.status='Pending'`, userID, env))
	_ = readTx.Rollback(ctx)
	if errors.Is(rowErr, pgx.ErrNoRows) {
		return nil
	}
	if rowErr != nil {
		return rowErr
	}

	sameQuote := row.productID == prod.ID && row.slotID == prod.SlotID && row.templateID == prod.TemplateID &&
		row.priceCents == int64(prod.PriceCents) && row.cycle == prod.VipCycle
	if sameQuote || row.activeProvider == "" {
		return nil
	}

	switch row.activeProvider {
	case payment.ProviderPayPal:
		return s.closePayPalVIPForPlanSwitch(ctx, row, prod)
	case payment.ProviderStripe:
		return s.closeStripeVIPForPlanSwitch(ctx, row, prod)
	default:
		return nil
	}
}

func (s *service) closePayPalVIPForPlanSwitch(ctx context.Context, row vipCheckoutIntentRow, target resolvedProduct) error {
	var subID, planID, localStatus string
	var submitted, leaseActive, hasPayment, activationRequested, remoteTerminationConfirmed bool
	var account payment.WebhookAccount
	err := db.Pool(ctx).QueryRow(ctx, `
		SELECT COALESCE(i.subscription_id,''), COALESCE(i.plan_id,''),
		       i.submitted_at IS NOT NULL, COALESCE(i.lease_until > now(), false),
		       a.account_key, a.environment,
		       COALESCE(s.status,''), s.last_payment_time IS NOT NULL,
		       s.paypal_activation_requested_at IS NOT NULL,
		       COALESCE((s.attribution->>'remote_termination_confirmed')::boolean, false)
		FROM paypal_subscription_intents i
		JOIN payment_channel_accounts a ON a.id = i.checkout_account_id AND a.provider = 'paypal'
		LEFT JOIN user_subscriptions s ON s.subscription_id = i.subscription_id
		  AND s.user_id = i.user_id AND s.checkout_intent_id = i.intent_id
		  AND s.payment_method = 'paypal'
		WHERE i.intent_id = $1 AND i.user_id = $2 AND i.checkout_environment = $3`,
		row.paypalIntentID, row.userID, row.environment).Scan(
		&subID, &planID, &submitted, &leaseActive,
		&account.AccountKey, &account.Environment,
		&localStatus, &hasPayment, &activationRequested, &remoteTerminationConfirmed,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}

	// No remote subscription ID yet: preserve existing stale-quote and in-flight invariants.
	if subID == "" {
		if leaseActive || submitted || row.switchTo != "" {
			recordVIPPlanSwitchBlockedDecision(ctx, row.userID, row, target.SlotID)
			return newVIPPlanSwitchBlockedError(VIPPlanSwitchCodePending, row, target)
		}
		return nil
	}

	if hasPayment || activationRequested || localStatus == "ACTIVE" || localStatus == "SUSPENDED" {
		recordVIPPlanSwitchBlockedDecision(ctx, row.userID, row, target.SlotID)
		return newVIPPlanSwitchBlockedError(VIPPlanSwitchCodeConfirming, row, target)
	}
	// Only trust local terminal state without re-checking PayPal when termination was
	// confirmed by the provider (EXPIRED is provider-originated; CANCELLED requires
	// explicit remote_termination_confirmed proof). Locally cancelled unapproved
	// agreements (e.g. PayPal 404/422 on /cancel) must be re-verified with PayPal
	// because their remote approval link may still be open.
	if (localStatus == "EXPIRED" || (localStatus == "CANCELLED" && remoteTerminationConfirmed)) && row.switchTo == "" {
		return nil
	}

	client, err := s.payPalForScope(ctx, account)
	if err != nil || client == nil {
		log.Printf("task=vip_plan_switch action=paypal_scope intent=%s sub=%s error=%v", row.intentID, subID, err)
		recordVIPPlanSwitchBlockedDecision(ctx, row.userID, row, target.SlotID)
		return newVIPPlanSwitchBlockedError(VIPPlanSwitchCodePending, row, target)
	}

	reqCtx, cancel := context.WithTimeout(ctx, vipPlanSwitchVerifyTimeout)
	defer cancel()
	read := func() (*payment.SubscriptionDetails, error) {
		details, readErr := client.GetSubscription(reqCtx, subID)
		if readErr != nil || details == nil || details.SubscriptionID != subID ||
			details.PlanID != planID || details.CustomID != row.paypalIntentID {
			log.Printf("task=vip_plan_switch action=read_paypal intent=%s sub=%s err=%v", row.intentID, subID, readErr)
			return nil, ErrCheckoutPending
		}
		return details, nil
	}

	details, err := read()
	if err != nil {
		recordVIPPlanSwitchBlockedDecision(ctx, row.userID, row, target.SlotID)
		return newVIPPlanSwitchBlockedError(VIPPlanSwitchCodePending, row, target)
	}
	if details.LastPaymentTime != nil || details.Status == "APPROVED" || details.Status == "ACTIVE" || details.Status == "SUSPENDED" {
		if row.switchTo != "" {
			_ = releaseVIPSwitch(ctx, row)
		}
		recordVIPPlanSwitchBlockedDecision(ctx, row.userID, row, target.SlotID)
		return newVIPPlanSwitchBlockedError(VIPPlanSwitchCodeConfirming, row, target)
	}

	if details.Status == "APPROVAL_PENDING" && details.LastPaymentTime == nil {
		cancelErr := client.CancelSubscription(reqCtx, subID, "User switched VIP plan")
		details, err = read()
		if err != nil {
			log.Printf("task=vip_plan_switch action=cancel_verify_paypal intent=%s sub=%s cancel_err=%v", row.intentID, subID, cancelErr)
			recordVIPPlanSwitchBlockedDecision(ctx, row.userID, row, target.SlotID)
			return newVIPPlanSwitchBlockedError(VIPPlanSwitchCodePending, row, target)
		}
		if details.LastPaymentTime != nil || details.Status == "APPROVED" || details.Status == "ACTIVE" || details.Status == "SUSPENDED" {
			if row.switchTo != "" {
				_ = releaseVIPSwitch(ctx, row)
			}
			recordVIPPlanSwitchBlockedDecision(ctx, row.userID, row, target.SlotID)
			return newVIPPlanSwitchBlockedError(VIPPlanSwitchCodeConfirming, row, target)
		}
		if details.Status == "APPROVAL_PENDING" {
			// For cross-plan replacement, do not release the original plan while PayPal's
			// approval link remains open or cancellation result is uncertain.
			log.Printf("task=vip_plan_switch action=paypal_approval_still_open intent=%s sub=%s cancel_err=%v", row.intentID, subID, cancelErr)
			recordVIPPlanSwitchBlockedDecision(ctx, row.userID, row, target.SlotID)
			code := VIPPlanSwitchCodeApprovalOpen
			if cancelErr != nil && !isUnapprovedPayPalCancelRejected(cancelErr) {
				code = VIPPlanSwitchCodePending
			}
			return newVIPPlanSwitchBlockedError(code, row, target)
		}
	}

	if details.Status != "CANCELLED" && details.Status != "EXPIRED" {
		log.Printf("task=vip_plan_switch action=unexpected_paypal_status intent=%s sub=%s status=%s", row.intentID, subID, details.Status)
		recordVIPPlanSwitchBlockedDecision(ctx, row.userID, row, target.SlotID)
		return newVIPPlanSwitchBlockedError(VIPPlanSwitchCodePending, row, target)
	}

	// Confirmed terminated with no payment: atomically close PayPal child and detach parent.
	tx, err := db.Pool(ctx).Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	tag, err := tx.Exec(ctx, `DELETE FROM paypal_subscription_intents
		WHERE intent_id=$1 AND user_id=$2 AND checkout_environment=$3 AND subscription_id=$4`,
		row.paypalIntentID, row.userID, row.environment, subID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		recordVIPPlanSwitchBlockedDecision(ctx, row.userID, row, target.SlotID)
		return newVIPPlanSwitchBlockedError(VIPPlanSwitchCodePending, row, target)
	}
	tag, err = tx.Exec(ctx, `UPDATE user_subscriptions SET status=$5,
		cancelled_at=COALESCE(cancelled_at,now()),
		cancel_reason=COALESCE(NULLIF(cancel_reason,''), 'Superseded by VIP plan switch'),
		attribution=COALESCE(attribution,'{}'::jsonb) || jsonb_build_object(
			'superseded_vip_intent_id', $4::text,
			'termination_source', 'provider_confirmed',
			'remote_termination_confirmed', true
		),
		next_billing_time=NULL,updated_at=now()
		WHERE subscription_id=$1 AND user_id=$2 AND checkout_intent_id=$3::uuid AND payment_method='paypal'
		  AND status IN ('PENDING','CANCEL_PENDING','CANCELLED','EXPIRED') AND last_payment_time IS NULL
		  AND paypal_activation_requested_at IS NULL`,
		subID, row.userID, row.paypalIntentID, row.intentID, details.Status)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		recordVIPPlanSwitchBlockedDecision(ctx, row.userID, row, target.SlotID)
		return newVIPPlanSwitchBlockedError(VIPPlanSwitchCodeConfirming, row, target)
	}
	tag, err = tx.Exec(ctx, `UPDATE payment_vip_checkout_intents
		SET active_provider=NULL,paypal_intent_id=NULL,stripe_intent_id=NULL,subscription_id=NULL,switch_to_provider=NULL,updated_at=now()
		WHERE intent_id=$1 AND user_id=$2 AND checkout_environment=$3 AND status='Pending'
		  AND active_provider='paypal' AND paypal_intent_id=$4::uuid`,
		row.intentID, row.userID, row.environment, row.paypalIntentID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		recordVIPPlanSwitchBlockedDecision(ctx, row.userID, row, target.SlotID)
		return newVIPPlanSwitchBlockedError(VIPPlanSwitchCodePending, row, target)
	}
	return tx.Commit(ctx)
}

func (s *service) closeStripeVIPForPlanSwitch(ctx context.Context, row vipCheckoutIntentRow, target resolvedProduct) error {
	account, err := stripeVIPAccountForOwner(ctx, row.userID, row.stripeIntentID, true)
	if err != nil {
		log.Printf("task=vip_plan_switch action=stripe_account intent=%s stripe_intent=%s error=%v", row.intentID, row.stripeIntentID, err)
		recordVIPPlanSwitchBlockedDecision(ctx, row.userID, row, target.SlotID)
		return newVIPPlanSwitchBlockedError(VIPPlanSwitchCodePending, row, target)
	}
	intent, err := findStripeVIPIntent(ctx, db.Pool(ctx), account, row.stripeIntentID)
	if err != nil {
		return err
	}
	if intent.SubscriptionID != "" || intent.Status == "bound" {
		if row.switchTo != "" {
			_ = releaseVIPSwitch(ctx, row)
		}
		recordVIPPlanSwitchBlockedDecision(ctx, row.userID, row, target.SlotID)
		return newVIPPlanSwitchBlockedError(VIPPlanSwitchCodeConfirming, row, target)
	}
	if intent.Status == "closed" && row.switchTo == "" {
		return nil
	}
	if intent.SessionID == "" && intent.FirstSentAt == nil {
		if row.switchTo != "" {
			recordVIPPlanSwitchBlockedDecision(ctx, row.userID, row, target.SlotID)
			return newVIPPlanSwitchBlockedError(VIPPlanSwitchCodePending, row, target)
		}
		return nil
	}

	sessionID := intent.SessionID
	if intent.Status != "closed" {
		adapter, ok := stripeCapabilityForAccount[payment.StripeVIPAdapter](s, account)
		closer, canClose := adapter.(interface {
			CloseStripeVIPCheckout(context.Context, string, string, string) (payment.StripeVIPCheckout, error)
		})
		if !ok || !canClose {
			log.Printf("task=vip_plan_switch action=stripe_adapter intent=%s stripe_intent=%s ok=%v can_close=%v", row.intentID, intent.ID, ok, canClose)
			recordVIPPlanSwitchBlockedDecision(ctx, row.userID, row, target.SlotID)
			return newVIPPlanSwitchBlockedError(VIPPlanSwitchCodePending, row, target)
		}
		reqCtx, cancel := context.WithTimeout(ctx, vipPlanSwitchVerifyTimeout)
		defer cancel()
		if sessionID == "" {
			sessionID, err = findUnidentifiedStripeVIPCheckout(reqCtx, adapter, intent)
			if err != nil || sessionID == "" {
				log.Printf("task=vip_plan_switch action=find_stripe_session intent=%s stripe_intent=%s error=%v", row.intentID, intent.ID, err)
				recordVIPPlanSwitchBlockedDecision(ctx, row.userID, row, target.SlotID)
				return newVIPPlanSwitchBlockedError(VIPPlanSwitchCodePending, row, target)
			}
		}
		proof, err := closer.CloseStripeVIPCheckout(reqCtx, sessionID, intent.ID, intent.Offer.SalesMode)
		if err != nil || proof.ID != sessionID || proof.IntentID != intent.ID {
			log.Printf("task=vip_plan_switch action=close_stripe intent=%s stripe_intent=%s session=%s error=%v", row.intentID, intent.ID, sessionID, err)
			recordVIPPlanSwitchBlockedDecision(ctx, row.userID, row, target.SlotID)
			return newVIPPlanSwitchBlockedError(VIPPlanSwitchCodePending, row, target)
		}
		if proof.SubscriptionID != "" {
			if row.switchTo != "" {
				_ = releaseVIPSwitch(ctx, row)
			}
			recordVIPPlanSwitchBlockedDecision(ctx, row.userID, row, target.SlotID)
			return newVIPPlanSwitchBlockedError(VIPPlanSwitchCodeConfirming, row, target)
		}
		if !proof.Expired {
			log.Printf("task=vip_plan_switch action=stripe_not_expired intent=%s stripe_intent=%s session=%s awaiting_buyer=%v", row.intentID, intent.ID, sessionID, proof.AwaitingBuyer)
			recordVIPPlanSwitchBlockedDecision(ctx, row.userID, row, target.SlotID)
			return newVIPPlanSwitchBlockedError(VIPPlanSwitchCodeApprovalOpen, row, target)
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
		recordVIPPlanSwitchBlockedDecision(ctx, row.userID, row, target.SlotID)
		return newVIPPlanSwitchBlockedError(VIPPlanSwitchCodePending, row, target)
	}
	tag, err = tx.Exec(ctx, `UPDATE payment_vip_checkout_intents
		SET active_provider=NULL,paypal_intent_id=NULL,stripe_intent_id=NULL,subscription_id=NULL,switch_to_provider=NULL,updated_at=now()
		WHERE intent_id=$1 AND user_id=$2 AND checkout_environment=$3 AND status='Pending'
		  AND active_provider='stripe' AND stripe_intent_id=$4::uuid`,
		row.intentID, row.userID, row.environment, intent.ID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		recordVIPPlanSwitchBlockedDecision(ctx, row.userID, row, target.SlotID)
		return newVIPPlanSwitchBlockedError(VIPPlanSwitchCodePending, row, target)
	}
	return tx.Commit(ctx)
}
