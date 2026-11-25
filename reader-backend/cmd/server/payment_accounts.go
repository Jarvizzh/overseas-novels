package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"reader-backend/internal/config"
	"reader-backend/internal/db"
	"reader-backend/internal/payment"
	"reader-backend/internal/payment/paypal"
	"reader-backend/internal/payment/stripe"
)

func registerPaymentAccounts(adapters payment.Adapters) error {
	primaries := make(map[string]bool)
	for _, ref := range config.AppConfig.PaymentAccounts {
		if ref.Primary && primaries[ref.Provider] {
			return fmt.Errorf("multiple primary payment accounts configured for %q", ref.Provider)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		err := registerPaymentAccount(ctx, adapters, ref)
		cancel()
		if err != nil {
			return err
		}
		primaries[ref.Provider] = primaries[ref.Provider] || ref.Primary
	}
	return nil
}

func registerPaymentAccount(ctx context.Context, adapters payment.Adapters, ref config.PaymentAccount) error {
	var registered bool
	if err := db.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM payment_channel_accounts
		WHERE provider=$1 AND account_key=$2 AND environment=$3)`, ref.Provider, ref.Account, ref.Environment).Scan(&registered); err != nil {
		return err
	}
	secret, webhook := os.Getenv(ref.SecretEnv), os.Getenv(ref.WebhookEnv)
	if !registered || secret == "" || webhook == "" {
		return fmt.Errorf("payment account %q lacks registered identity or credential references", ref.Account)
	}
	var adapter payment.Adapter
	var err error
	switch ref.Provider {
	case payment.ProviderPayPal:
		merchant := os.Getenv(ref.MerchantEnv)
		if merchant == "" {
			return fmt.Errorf("PayPal merchant identity is missing for account %q", ref.Account)
		}
		adapter, err = paypal.NewScopedClient(paypal.Config{ClientID: ref.Account, Environment: ref.Environment,
			Secret: secret, WebhookID: webhook, MerchantID: merchant})
	case payment.ProviderStripe:
		adapter, err = stripe.NewClient(ctx, stripe.Config{Scope: stripe.Scope{AccountID: ref.Account, Environment: stripe.Environment(ref.Environment)},
			APIKey: secret, WebhookSecret: webhook})
	default:
		return fmt.Errorf("unsupported payment provider %q", ref.Provider)
	}
	if err != nil {
		return err
	}
	return adapters.RegisterAccount(payment.WebhookAccount{AccountKey: ref.Account, Environment: ref.Environment}, adapter, ref.Primary)
}
