// Package credentialstore resolves Reader's existing PayPal credential reference.
package credentialstore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"
	"reader-backend/internal/config"
	"reader-backend/internal/db"
	"reader-backend/internal/payment"
	"reader-backend/internal/payment/paypal"
)

func LoadForAccount(ctx context.Context, account payment.WebhookAccount) (paypal.Config, error) {
	if ref, ok := config.FindPaymentAccount(payment.ProviderPayPal, account.AccountKey, account.Environment); ok {
		return paypal.Config{ClientID: account.AccountKey, Environment: account.Environment, Secret: os.Getenv(ref.SecretEnv),
			WebhookID: os.Getenv(ref.WebhookEnv), MerchantID: os.Getenv(ref.MerchantEnv)}, nil
	}
	cfg, err := Load(ctx)
	if cfg.Environment == "production" {
		cfg.Environment = paypal.EnvironmentLive
	}
	if err == nil && ((account.AccountKey != "" && cfg.ClientID != account.AccountKey) || cfg.Environment != account.Environment) {
		return paypal.Config{}, paypal.ErrCredentials
	}
	return cfg, err
}

func Load(ctx context.Context) (paypal.Config, error) {
	var dbClientID, dbSecret, dbMode, dbSandboxMode string
	var source, boundMode string
	if db.DB != nil {
		err := db.DB.QueryRow(ctx, `SELECT a.credentials_ref,a.environment FROM payment_channel_accounts a
			JOIN paypal_identity_lock p ON p.client_id=a.account_key AND p.environment=a.environment
			WHERE a.provider='paypal'`).Scan(&source, &boundMode)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return paypal.Config{}, fmt.Errorf("load PayPal credentials: %w", err)
		}
		if source != "" && source != "legacy:paypal" && source != "env:paypal" {
			return paypal.Config{}, errors.New("unsupported PayPal credentials reference")
		}
		for key, value := range map[string]*string{
			"paypal_client_id": &dbClientID, "paypal_secret_key": &dbSecret,
			"paypal_mode": &dbMode, "payment_sandbox_mode": &dbSandboxMode,
		} {
			if source == "env:paypal" {
				dbMode = boundMode
				break
			}
			err := db.DB.QueryRow(ctx, "SELECT value FROM system_configs WHERE key=$1", key).Scan(value)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return paypal.Config{}, fmt.Errorf("load PayPal credentials: %w", err)
			}
		}
	}

	// 1. Determine ClientID and Secret (DB highest priority > .env fallback)
	clientID := strings.TrimSpace(dbClientID)
	secret := strings.TrimSpace(dbSecret)
	if clientID == "" && config.AppConfig != nil {
		clientID = config.AppConfig.PayPalClientID
		secret = config.AppConfig.PayPalClientSecret
	}

	// 2. Determine Mode (DB highest priority > .env fallback)
	mode := strings.ToLower(strings.TrimSpace(dbMode))
	if mode == "" {
		if dbSandboxMode != "" {
			if dbSandboxMode == "false" {
				mode = "live"
			} else {
				mode = "sandbox"
			}
		} else if config.AppConfig != nil {
			mode = strings.ToLower(config.AppConfig.PayPalMode)
		}
	}
	if mode != "live" && mode != "production" {
		mode = "sandbox"
	}

	if db.DB != nil && clientID != "" {
		if _, err := db.DB.Exec(ctx, `SELECT assert_paypal_identity($1,$2)`, clientID, mode); err != nil {
			return paypal.Config{}, fmt.Errorf("load PayPal credentials: %w", err)
		}
	}
	cfg := paypal.Config{ClientID: clientID, Secret: secret, Environment: mode}
	if config.AppConfig != nil {
		cfg.WebhookID = config.AppConfig.PayPalWebhookID
		cfg.MerchantID = config.AppConfig.PayPalMerchantID
	}
	return cfg, nil
}
