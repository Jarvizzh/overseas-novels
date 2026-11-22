package directory

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"testing"
	"time"

	"reader-backend/internal/credentials"
	"reader-backend/internal/payment"
	"reader-backend/internal/testdb"
)

func TestDirectoryDynamicHotReloadAndRotation(t *testing.T) {
	t.Setenv("CONVERSION_ENVIRONMENT", "sandbox")

	keyBytes := make([]byte, 32)
	if _, err := rand.Read(keyBytes); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PAYMENT_CREDENTIAL_KEYS", "v1:"+base64.StdEncoding.EncodeToString(keyBytes))
	ring, err := credentials.LoadFromEnv()
	if err != nil {
		t.Fatal(err)
	}

	pool := testdb.Open(t)
	ctx := context.Background()

	var accID int64
	var webhookToken string
	if err := pool.QueryRow(ctx, `
		INSERT INTO payment_channel_accounts(
			provider, environment, account_key, display_name, credentials_ref, webhook_ref,
			status, is_primary, active_version, pending_version, new_orders_enabled, new_subscriptions_enabled
		) VALUES (
			'stripe', 'sandbox', 'acct_DirTest01', 'Stripe Primary', 'db:encrypted', 'channel_token',
			'published', true, 1, 1, true, true
		) RETURNING id, webhook_token::text`,
	).Scan(&accID, &webhookToken); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO payment_channel_account_versions(account_id, version, display_name, account_key, status, validation_status)
		VALUES ($1, 1, 'Stripe Primary', 'acct_DirTest01', 'published', 'verified')`, accID); err != nil {
		t.Fatal(err)
	}

	apiKeyCT, err := ring.Encrypt("v1", "sk_test_dir_v1", credentials.CredentialAAD(accID, 1, "api_key"))
	if err != nil {
		t.Fatal(err)
	}
	whCT, err := ring.Encrypt("v1", "whsec_dir_v1", credentials.CredentialAAD(accID, 1, "webhook_secret"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO payment_channel_credentials(account_id, version, purpose, key_version, ciphertext, hint)
		VALUES ($1, 1, 'api_key', 'v1', $2, '••v1'),
		       ($1, 1, 'webhook_secret', 'v1', $3, '••v1')`, accID, apiKeyCT, whCT); err != nil {
		t.Fatal(err)
	}

	dir := New(pool, payment.NewAdapters())
	adapters := payment.NewAdapters().WithDynamicSource(dir)

	sc, err := payment.Capability[payment.SingleCheckoutAdapter](adapters, payment.ProviderStripe)
	if err != nil || sc.AccountScope().AccountKey != "acct_DirTest01" {
		t.Fatalf("unexpected primary adapter: sc=%v err=%v", sc, err)
	}

	// Simulate credential rotation in DB (version 2 published without restarting Reader)
	if _, err := pool.Exec(ctx, `
		INSERT INTO payment_channel_account_versions(account_id, version, display_name, account_key, status, validation_status)
		VALUES ($1, 2, 'Stripe Primary', 'acct_DirTest01', 'published', 'verified')`, accID); err != nil {
		t.Fatal(err)
	}
	apiKey2CT, _ := ring.Encrypt("v1", "sk_test_dir_v2", credentials.CredentialAAD(accID, 2, "api_key"))
	wh2CT, _ := ring.Encrypt("v1", "whsec_dir_v2", credentials.CredentialAAD(accID, 2, "webhook_secret"))
	prevWhCT, _ := ring.Encrypt("v1", "whsec_dir_v1", credentials.CredentialAAD(accID, 2, "previous_webhook_secret"))
	if _, err := pool.Exec(ctx, `
		INSERT INTO payment_channel_credentials(account_id, version, purpose, key_version, ciphertext, hint, expires_at)
		VALUES ($1, 2, 'api_key', 'v1', $2, '••v2', NULL),
		       ($1, 2, 'webhook_secret', 'v1', $3, '••v2', NULL),
		       ($1, 2, 'previous_webhook_secret', 'v1', $4, '••v1', $5)`,
		accID, apiKey2CT, wh2CT, prevWhCT, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE payment_channel_accounts SET active_version = 2, pending_version = 2 WHERE id = $1`, accID); err != nil {
		t.Fatal(err)
	}

	sc2, err := payment.Capability[payment.SingleCheckoutAdapter](adapters, payment.ProviderStripe)
	if err != nil || sc2 == sc {
		t.Fatalf("expected rotated version=2 adapter instance without restart, err=%v", err)
	}

	resolvedID, provider, scope, tokAdapter, err := dir.ResolveWebhookToken(ctx, webhookToken)
	if err != nil || resolvedID != accID || provider != payment.ProviderStripe || scope.AccountKey != "acct_DirTest01" || tokAdapter == nil {
		t.Fatalf("webhook token resolve failed: id=%d provider=%s scope=%v err=%v", resolvedID, provider, scope, err)
	}

	// Deactivate stops primary routing, while historical AdapterForScope still resolves
	if _, err := pool.Exec(ctx, `UPDATE payment_channel_accounts SET status='disabled', is_primary=false, new_orders_enabled=false, new_subscriptions_enabled=false WHERE id=$1`, accID); err != nil {
		t.Fatal(err)
	}
	if _, err := payment.Capability[payment.SingleCheckoutAdapter](adapters, payment.ProviderStripe); err == nil {
		t.Fatal("expected disabled account not to resolve as primary")
	}
	if _, ok := payment.AccountCapability[payment.SingleCheckoutAdapter](adapters, payment.ProviderStripe, scope); !ok {
		t.Fatal("expected deactivated account to remain available for historical refund/webhook recovery")
	}
}
