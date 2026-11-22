package application

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"reader-backend/internal/conversion"
	"reader-backend/internal/db"
	"reader-backend/internal/payment"
)

type stripeScopedAdapter interface {
	payment.Adapter
	AccountScope() payment.WebhookAccount
}

func stripeCapabilityForAccount[T any](s *service, scope payment.WebhookAccount) (adapter T, available bool) {
	if !paymentEnvironmentMatches(scope) {
		return adapter, false
	}
	if adapter, ok := payment.AccountCapability[T](s.adapters, payment.ProviderStripe, scope); ok {
		return adapter, true
	}
	legacy, ok := payment.LegacyCapability[stripeScopedAdapter](s.adapters, payment.ProviderStripe)
	if ok && legacy.AccountScope() == scope {
		adapter, available = legacy.(T)
	}
	return adapter, available
}

func (s *service) eachStripeAccount(ctx context.Context, run func(*service, context.Context)) {
	primary, _ := payment.Capability[stripeScopedAdapter](s.adapters, payment.ProviderStripe)
	visited := make(map[payment.WebhookAccount]bool)
	for _, candidate := range payment.AccountCapabilities[stripeScopedAdapter](s.adapters, payment.ProviderStripe) {
		scope := candidate.AccountScope()
		if visited[scope] || !paymentEnvironmentMatches(scope) || ctx.Err() != nil {
			continue
		}
		visited[scope] = true
		adapter, ok := stripeCapabilityForAccount[stripeScopedAdapter](s, scope)
		if !ok {
			continue
		}
		scoped := *s
		scoped.adapters = s.adapters.Prefer(adapter)
		scoped.historicalStripe = primary == nil || primary.AccountScope() != scope
		run(&scoped, ctx)
	}
}

func (s *service) eachPayPalAccount(ctx context.Context, run func(*service, context.Context)) {
	visited := make(map[payment.WebhookAccount]bool)
	for _, candidate := range payment.AccountCapabilities[paypalCheckout](s.adapters, payment.ProviderPayPal) {
		scope, err := candidate.CheckoutAccount(ctx)
		if err != nil || visited[scope] || !paymentEnvironmentMatches(scope) || ctx.Err() != nil {
			continue
		}
		visited[scope] = true
		adapter, err := s.payPalForScope(ctx, scope)
		if err != nil {
			continue
		}
		scoped := *s
		scoped.adapters = s.adapters.Prefer(adapter)
		scoped.paypalClient = adapter
		run(&scoped, ctx)
	}
}

func (s *service) payPalForScope(ctx context.Context, scope payment.WebhookAccount) (paypalCheckout, error) {
	if scope.AccountKey != "" && !paymentEnvironmentMatches(scope) {
		return nil, ErrCheckoutUnavailable
	}
	if adapter, ok := payment.AccountCapability[paypalCheckout](s.adapters, payment.ProviderPayPal, scope); ok {
		return adapter, nil
	}
	if legacy, ok := payment.LegacyCapability[paypalCheckout](s.adapters, payment.ProviderPayPal); ok {
		actual, err := legacy.CheckoutAccount(ctx)
		if err != nil {
			return nil, err
		}
		if paymentEnvironmentMatches(actual) && (scope.AccountKey == "" || scope == actual) {
			return legacy, nil
		}
	}
	return nil, ErrCheckoutUnavailable
}

func (s *service) payPalForSubscription(ctx context.Context, subscription string) (paypalCheckout, error) {
	var scope payment.WebhookAccount
	err := db.Pool(ctx).QueryRow(ctx, `SELECT COALESCE(a.account_key,''),COALESCE(a.environment,'')
		FROM user_subscriptions s LEFT JOIN payment_channel_accounts a ON a.id=s.checkout_account_id AND a.provider='paypal'
		WHERE s.subscription_id=$1 AND s.payment_method='paypal'
		AND payment_subscription_environment(s)=$2`, subscription, conversion.Environment()).Scan(&scope.AccountKey, &scope.Environment)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrCheckoutUnavailable
	}
	if err != nil {
		return nil, err
	}
	if !s.adapters.HasRegisteredAccounts(payment.ProviderPayPal) && s.paypalClient != nil {
		actual, err := s.paypalClient.CheckoutAccount(ctx)
		if err != nil {
			return nil, err
		}
		if paymentEnvironmentMatches(actual) && (scope.AccountKey == "" || scope == actual) {
			return s.paypalClient, nil
		}
		return nil, ErrCheckoutUnavailable
	}
	return s.payPalForScope(ctx, scope)
}

func (s *service) payPalForOrder(ctx context.Context, externalID string) (paypalCheckout, error) {
	if !s.adapters.HasRegisteredAccounts(payment.ProviderPayPal) {
		return s.paypalClient, nil
	}
	var scope payment.WebhookAccount
	err := db.Pool(ctx).QueryRow(ctx, `SELECT a.account_key,a.environment FROM payment_checkout_attempts p
		JOIN payment_channel_accounts a ON a.id=p.account_id WHERE p.provider='paypal' AND p.provider_order_id=$1`,
		externalID).Scan(&scope.AccountKey, &scope.Environment)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	return s.payPalForScope(ctx, scope)
}

func (s *service) payPalForNewCheckout(ctx context.Context, userID int64, intentID string, slotID int) (paypalCheckout, error) {
	if !s.adapters.HasRegisteredAccounts(payment.ProviderPayPal) {
		return s.paypalClient, nil
	}
	var scope payment.WebhookAccount
	err := db.Pool(ctx).QueryRow(ctx, `SELECT a.account_key,a.environment FROM payment_channel_accounts a
		WHERE a.provider='paypal' AND a.id=(
			SELECT p.account_id FROM payment_checkout_attempts p JOIN recharge_orders o ON o.id=p.order_id
			WHERE o.checkout_id=NULLIF($2,'')::uuid AND o.user_id=$1 AND o.checkout_environment=$4 AND p.provider='paypal'
			AND p.status IN ('creating','buyer_action','capturing')
			UNION ALL SELECT i.checkout_account_id FROM paypal_subscription_intents i
			WHERE $2='' AND i.user_id=$1 AND i.slot_id=$3 AND i.checkout_environment=$4 AND (i.subscription_id IS NULL OR EXISTS (
				SELECT 1 FROM user_subscriptions u WHERE u.subscription_id=i.subscription_id AND u.status NOT IN ('CANCELLED','EXPIRED')))
		)`, userID, intentID, slotID, conversion.Environment()).Scan(&scope.AccountKey, &scope.Environment)
	if errors.Is(err, pgx.ErrNoRows) {
		return s.paypalClient, nil
	}
	if err != nil {
		return nil, err
	}
	return s.payPalForScope(ctx, scope)
}

func stripeVIPAccountForOwner(ctx context.Context, user int64, ref string, checkout bool) (payment.WebhookAccount, error) {
	var scope payment.WebhookAccount
	err := db.Pool(ctx).QueryRow(ctx, `SELECT a.account_key,a.environment FROM payment_stripe_subscription_intents v
		JOIN payment_channel_accounts a ON a.id=v.account_id WHERE a.provider='stripe' AND v.user_id=$1 AND v.checkout_environment=$2
		AND CASE WHEN $4 THEN v.id::text=$3 ELSE v.subscription_ref=$3 AND v.status='bound' END`,
		user, conversion.Environment(), ref, checkout).Scan(&scope.AccountKey, &scope.Environment)
	if errors.Is(err, pgx.ErrNoRows) {
		return scope, ErrCheckoutUnavailable
	}
	return scope, err
}
