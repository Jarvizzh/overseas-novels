package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"reader-backend/internal/db"
	"reader-backend/internal/payment"
)

const (
	stripeCheckoutModeOneTime      = "payment"
	stripeCheckoutModeSubscription = "subscription"
)

type stripeAccountCheckoutPolicy struct {
	AdaptivePricingEnabled             bool
	SavedPaymentMethodEnabled          bool
	SimplifiedAddressCollectionEnabled bool
}

type stripeCustomerRecord struct {
	CustomerID             string
	VerifiedBillingCountry string
	TaxEvidenceVerified    bool
}

func loadStripeAccountCheckoutPolicy(ctx context.Context, q checkoutQueryer, accountID int64) (stripeAccountCheckoutPolicy, error) {
	var policy stripeAccountCheckoutPolicy
	err := q.QueryRow(ctx, `SELECT adaptive_pricing_enabled, saved_payment_method_enabled, simplified_address_collection_enabled
		FROM payment_channel_accounts WHERE id=$1`, accountID).
		Scan(&policy.AdaptivePricingEnabled, &policy.SavedPaymentMethodEnabled, &policy.SimplifiedAddressCollectionEnabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return stripeAccountCheckoutPolicy{}, nil
	}
	return policy, err
}

func findStripeCustomer(ctx context.Context, q checkoutQueryer, userID, accountID int64, environment string) (*stripeCustomerRecord, error) {
	var rec stripeCustomerRecord
	err := q.QueryRow(ctx, `SELECT stripe_customer_id, COALESCE(verified_billing_country,''), tax_evidence_verified
		FROM payment_stripe_customers
		WHERE user_id=$1 AND account_id=$2 AND environment=$3`, userID, accountID, environment).
		Scan(&rec.CustomerID, &rec.VerifiedBillingCountry, &rec.TaxEvidenceVerified)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &rec, nil
}

func upsertStripeCustomerTx(ctx context.Context, tx pgx.Tx, userID, accountID int64, environment, customerID, billingCountry string, taxVerified bool) (string, error) {
	if userID <= 0 || accountID <= 0 || environment == "" || customerID == "" {
		return "", nil
	}
	var stored string
	err := tx.QueryRow(ctx, `INSERT INTO payment_stripe_customers
		(user_id, account_id, environment, stripe_customer_id, verified_billing_country, tax_evidence_verified)
		VALUES($1, $2, $3, $4, NULLIF($5,''), $6)
		ON CONFLICT (user_id, account_id, environment) DO UPDATE SET
			verified_billing_country = COALESCE(NULLIF(EXCLUDED.verified_billing_country,''), payment_stripe_customers.verified_billing_country),
			tax_evidence_verified = payment_stripe_customers.tax_evidence_verified OR EXCLUDED.tax_evidence_verified,
			updated_at = now()
		RETURNING stripe_customer_id`, userID, accountID, environment, customerID, billingCountry, taxVerified).Scan(&stored)
	return stored, err
}

func stripeCustomerIdempotencyKey(account payment.WebhookAccount, userID int64) string {
	return fmt.Sprintf("stripe-customer:%s:%s:%d", account.Environment, account.AccountKey, userID)
}

// ensureStripeCustomer returns the persisted verified Stripe Customer for
// (user_id, account_id, environment). If none exists and the adapter supports
// customer creation, it creates one outside any database transaction using a
// deterministic idempotency key so concurrent checkouts converge on a single
// Stripe Customer without creating duplicates.
func ensureStripeCustomer(ctx context.Context, adapter any, userID, accountID int64, account payment.WebhookAccount) (*stripeCustomerRecord, error) {
	if userID <= 0 || accountID <= 0 || account.Environment == "" {
		return nil, nil
	}
	pool := db.Pool(ctx)
	existing, err := findStripeCustomer(ctx, pool, userID, accountID, account.Environment)
	if err != nil || existing != nil {
		return existing, err
	}
	creator, ok := adapter.(payment.StripeCustomerAdapter)
	if !ok {
		return nil, nil
	}
	var email string
	err = pool.QueryRow(ctx, `SELECT COALESCE(email,'') FROM users WHERE id=$1`, userID).Scan(&email)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	customerID, err := creator.CreateCustomer(ctx, userID, email, stripeCustomerIdempotencyKey(account, userID))
	if err != nil {
		return nil, err
	}
	var rec stripeCustomerRecord
	err = pool.QueryRow(ctx, `INSERT INTO payment_stripe_customers
		(user_id, account_id, environment, stripe_customer_id)
		VALUES($1, $2, $3, $4)
		ON CONFLICT (user_id, account_id, environment) DO UPDATE SET updated_at = now()
		RETURNING stripe_customer_id, COALESCE(verified_billing_country,''), tax_evidence_verified`,
		userID, accountID, account.Environment, customerID).
		Scan(&rec.CustomerID, &rec.VerifiedBillingCountry, &rec.TaxEvidenceVerified)
	if err != nil {
		return nil, err
	}
	return &rec, nil
}

func buildFrozenCheckoutParams(salesMode, checkoutMode string, policy stripeAccountCheckoutPolicy, customer *stripeCustomerRecord) payment.FrozenCheckoutParams {
	params := payment.FrozenCheckoutParams{
		AdaptivePricingEnabled:   salesMode == payment.StripeSaleDirect && policy.AdaptivePricingEnabled,
		BillingAddressCollection: payment.BillingAddressCollectionRequired,
	}
	if customer != nil && customer.CustomerID != "" {
		params.CustomerID = customer.CustomerID
		if policy.SimplifiedAddressCollectionEnabled && customer.VerifiedBillingCountry != "" && customer.TaxEvidenceVerified {
			params.BillingAddressCollection = payment.BillingAddressCollectionAuto
		}
		if policy.SavedPaymentMethodEnabled {
			params.SavePaymentMethodEnabled = true
			if salesMode == payment.StripeSaleDirect && checkoutMode == stripeCheckoutModeOneTime {
				params.SetupFutureUsage = payment.SetupFutureUsageOnSession
			}
		}
	}
	return params
}

func freezeSingleAttemptParams(ctx context.Context, attemptID string, candidate payment.FrozenCheckoutParams) (payment.FrozenCheckoutParams, error) {
	raw, err := json.Marshal(candidate)
	if err != nil {
		return payment.FrozenCheckoutParams{}, err
	}
	var stored json.RawMessage
	err = db.Pool(ctx).QueryRow(ctx, `UPDATE payment_stripe_checkout_terms
		SET frozen_checkout_params = COALESCE(frozen_checkout_params, $2::jsonb)
		WHERE attempt_id=$1
		RETURNING frozen_checkout_params`, attemptID, raw).Scan(&stored)
	if err != nil {
		return payment.FrozenCheckoutParams{}, err
	}
	var frozen payment.FrozenCheckoutParams
	if err := json.Unmarshal(stored, &frozen); err != nil {
		return payment.FrozenCheckoutParams{}, err
	}
	return frozen, nil
}

func freezeVIPIntentParams(ctx context.Context, intentID string, candidate payment.FrozenCheckoutParams) (payment.FrozenCheckoutParams, error) {
	raw, err := json.Marshal(candidate)
	if err != nil {
		return payment.FrozenCheckoutParams{}, err
	}
	var stored json.RawMessage
	err = db.Pool(ctx).QueryRow(ctx, `UPDATE payment_stripe_subscription_intents
		SET frozen_checkout_params = COALESCE(frozen_checkout_params, $2::jsonb)
		WHERE id=$1
		RETURNING frozen_checkout_params`, intentID, raw).Scan(&stored)
	if err != nil {
		return payment.FrozenCheckoutParams{}, err
	}
	var frozen payment.FrozenCheckoutParams
	if err := json.Unmarshal(stored, &frozen); err != nil {
		return payment.FrozenCheckoutParams{}, err
	}
	return frozen, nil
}
