package application

import (
	"context"
	"errors"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"reader-backend/internal/conversion"
	"reader-backend/internal/db"
	"reader-backend/internal/payment"
)

func (s *service) closeStripeForCheckoutSwitch(ctx context.Context, user int64, intent *PurchaseIntent) error {
	var id string
	var account payment.WebhookAccount
	err := db.Pool(ctx).QueryRow(ctx, `SELECT p.id::text,a.account_key,a.environment FROM payment_checkout_attempts p
		JOIN payment_channel_accounts a ON a.id=p.account_id
		JOIN recharge_orders o ON o.id=p.order_id WHERE o.checkout_id=$1 AND o.user_id=$2 AND o.checkout_environment=$3
		AND o.status='Pending' AND p.provider='stripe' AND p.status IN ('creating','buyer_action','capturing')`,
		intent.ID, user, conversion.Environment()).Scan(&id, &account.AccountKey, &account.Environment)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrCheckoutPending
	}
	if err != nil {
		return err
	}
	closer, ok := stripeCapabilityForAccount[interface {
		CloseSingleCheckout(context.Context, string, string, payment.SingleCheckoutOffer) (payment.SingleCheckoutProof, error)
	}](s, account)
	if !ok {
		return ErrCheckoutPending
	}
	attempt, err := loadStripeAttempt(ctx, db.Pool(ctx), account, id)
	if err != nil {
		return err
	}
	if attempt.SessionID == "" {
		return ErrCheckoutPending
	}
	proof, err := closer.CloseSingleCheckout(ctx, attempt.SessionID, attempt.ID, attempt.Offer)
	if err != nil {
		return err
	}
	if err := s.repo.applyStripeProof(ctx, attempt, proof, nil); err != nil {
		return err
	}
	if !proof.Expired && !proof.Failed {
		return ErrCheckoutPending
	}
	return nil
}

func (s *service) closePayPalForCheckoutSwitch(ctx context.Context, user int64, intent *PurchaseIntent) error {
	client, err := s.payPalForNewCheckout(ctx, user, intent.ID, 0)
	if err != nil {
		return err
	}
	account, err := client.CheckoutAccount(ctx)
	if err != nil {
		return err
	}
	if !paymentEnvironmentMatches(account) {
		return ErrCheckoutPaused
	}
	var ref string
	var token uuid.UUID
	err = db.Pool(ctx).QueryRow(ctx, `UPDATE payment_checkout_attempts p SET paypal_switch_token=COALESCE(paypal_switch_token,$6)
		FROM recharge_orders o,payment_channel_accounts a
		WHERE o.id=p.order_id AND a.id=p.account_id AND o.checkout_id=$1 AND o.user_id=$2
		  AND o.checkout_environment=$3 AND o.status='Pending' AND p.status='buyer_action' AND p.provider='paypal'
		  AND p.provider_order_id IS NOT NULL AND p.capture_first_sent_at IS NULL AND NOT p.capture_lookup_only
		  AND a.account_key=$4 AND a.environment=$5
		RETURNING p.provider_order_id,p.paypal_switch_token`, intent.ID, user, conversion.Environment(),
		account.AccountKey, account.Environment, uuid.New()).Scan(&ref, &token)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrCheckoutPending
	}
	if err != nil {
		return err
	}
	return s.verifyPayPalCheckoutSwitch(ctx, client, account, intent, ref, token)
}

func (s *service) verifyPayPalCheckoutSwitch(ctx context.Context, client paypalCheckout, account payment.WebhookAccount,
	intent *PurchaseIntent, ref string, token uuid.UUID) error {
	closer, ok := client.(interface {
		CanCloseSingleCheckout(context.Context, string, string, int64, string, payment.WebhookAccount) (bool, error)
	})
	if !ok {
		return ErrCheckoutPending
	}
	closed, err := closer.CanCloseSingleCheckout(ctx, ref, intent.ID, intent.AmountMinor, intent.Currency, account)
	if err != nil {
		return err
	}
	if !closed {
		return ErrCheckoutPending
	}
	tag, err := db.Pool(ctx).Exec(ctx, `UPDATE payment_checkout_attempts SET status='closed',paypal_switch_token=NULL
		WHERE provider='paypal' AND provider_order_id=$1 AND paypal_switch_token=$2 AND status='buyer_action'
		AND capture_first_sent_at IS NULL`, ref, token)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrCheckoutPending
	}
	return err
}

func (s *service) reconcilePayPalCheckoutSwitch(ctx context.Context, ref string) (bool, error) {
	var intent PurchaseIntent
	var userID int64
	var account payment.WebhookAccount
	var token uuid.UUID
	err := db.Pool(ctx).QueryRow(ctx, `SELECT o.checkout_id::text,o.amount_cents,o.currency,COALESCE(o.recharge_slot_id,0),o.user_id,a.account_key,a.environment,p.paypal_switch_token
		FROM payment_checkout_attempts p JOIN recharge_orders o ON o.id=p.order_id JOIN payment_channel_accounts a ON a.id=p.account_id
		WHERE p.provider='paypal' AND p.provider_order_id=$1 AND p.paypal_switch_token IS NOT NULL
		AND p.status='buyer_action' AND o.checkout_environment=$2`, ref, conversion.Environment()).
		Scan(&intent.ID, &intent.AmountMinor, &intent.Currency, &intent.SlotID, &userID, &account.AccountKey, &account.Environment, &token)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	slots, err := s.GetStripeOfferSlots(ctx, userID)
	if err != nil {
		return true, err
	}
	if !slices.Contains(slots, intent.SlotID) {
		return true, ErrCheckoutPaused
	}
	client, err := s.payPalForScope(ctx, account)
	if err != nil {
		return true, err
	}
	return true, s.verifyPayPalCheckoutSwitch(ctx, client, account, &intent, ref, token)
}
