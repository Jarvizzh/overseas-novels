package credentialstore

import (
	"context"
	"testing"

	"reader-backend/internal/config"
	"reader-backend/internal/payment"
	"reader-backend/internal/payment/paypal"
	"reader-backend/internal/testdb"
)

func TestPayPalIdentityCannotSwitch(t *testing.T) {
	p := testdb.Open(t)
	ctx := context.Background()
	if _, err := p.Exec(ctx, `INSERT INTO system_configs(key,value) VALUES('paypal_client_id','merchant-original'),('paypal_secret_key','secret'),('paypal_mode','sandbox')`); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(ctx)
	if err != nil || cfg.ClientID != "merchant-original" {
		t.Fatalf("identity not bound: %v", err)
	}
	for _, query := range []string{`SELECT assert_paypal_identity('different-merchant','sandbox')`, `SELECT assert_paypal_identity('merchant-original','live')`, `UPDATE system_configs SET value='live' WHERE key='paypal_mode'`} {
		if _, err := p.Exec(ctx, query); err == nil {
			t.Fatalf("unsafe switch allowed: %s", query)
		}
	}
	if _, err := p.Exec(ctx, `UPDATE system_configs SET value='rotated-secret' WHERE key='paypal_secret_key';INSERT INTO system_configs(key,value) VALUES('paypal_mode','sandbox') ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value`); err != nil {
		t.Fatalf("safe rotation/no-op rejected: %v", err)
	}
}

func TestPayPalCredentialSourceSwitchPreservesAccount(t *testing.T) {
	p := testdb.Open(t)
	ctx := t.Context()
	previous := config.AppConfig
	t.Cleanup(func() { config.AppConfig = previous })
	config.AppConfig = &config.Config{PayPalClientID: "switch-fixture", PayPalClientSecret: "env-fixture",
		PayPalMode: "live", PayPalWebhookID: "webhook-fixture"}
	if _, err := p.Exec(ctx, `INSERT INTO system_configs(key,value)
		VALUES ('paypal_client_id','switch-fixture'),('paypal_secret_key','db-fixture'),('paypal_mode','sandbox')`); err != nil {
		t.Fatal(err)
	}
	scope := payment.WebhookAccount{AccountKey: "switch-fixture", Environment: "sandbox"}
	for _, source := range []struct{ ref, secret string }{{"legacy:paypal", "db-fixture"}, {"env:paypal", "env-fixture"}, {"legacy:paypal", "db-fixture"}} {
		if _, err := p.Exec(ctx, `UPDATE payment_channel_accounts SET credentials_ref=$1 WHERE account_key='switch-fixture'`, source.ref); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load(ctx)
		if err != nil || cfg.Secret != source.secret || cfg.WebhookID != "webhook-fixture" {
			t.Fatalf("unexpected credential resolution for %s: %v", source.ref, err)
		}
		account, err := paypal.NewClient(Load).CheckoutAccount(ctx)
		if err != nil || account != scope {
			t.Fatalf("credential source %s changed channel account: %+v %v", source.ref, account, err)
		}
	}
}
