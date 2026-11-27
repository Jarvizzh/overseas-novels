package directory

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"reader-backend/internal/credentials"
	"reader-backend/internal/payment"
	"reader-backend/internal/payment/stripe"
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

type mockRoundTripper func(*http.Request) (*http.Response, error)

func (f mockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func mockJSONResponse(status int, body string) (*http.Response, error) {
	header := make(http.Header)
	header.Set("Content-Type", "application/json")
	header.Set("Request-Id", "req_dir_test")
	return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body))}, nil
}

func TestDirectoryResolveStripeClientPurposeRotationDisabledAndErrors(t *testing.T) {
	t.Setenv("CONVERSION_ENVIRONMENT", "sandbox")
	t.Setenv("STRIPE_LEDGER_API_KEY", "")
	t.Setenv("STRIPE_AFTERCARE_API_KEY", "")

	keyBytes := make([]byte, 32)
	if _, err := rand.Read(keyBytes); err != nil {
		t.Fatal(err)
	}
	validKeyring := "v1:" + base64.StdEncoding.EncodeToString(keyBytes)
	t.Setenv("PAYMENT_CREDENTIAL_KEYS", validKeyring)
	ring, err := credentials.LoadFromEnv()
	if err != nil {
		t.Fatal(err)
	}

	pool := testdb.Open(t)
	ctx := context.Background()

	var accID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO payment_channel_accounts(
			provider, environment, account_key, display_name, credentials_ref, webhook_ref,
			status, is_primary, active_version, pending_version, new_orders_enabled, new_subscriptions_enabled
		) VALUES (
			'stripe', 'sandbox', 'acct_DirPurpose01', 'Stripe Purpose', 'db:encrypted', 'channel_token',
			'published', true, 1, 1, true, true
		) RETURNING id`,
	).Scan(&accID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO payment_channel_account_versions(account_id, version, display_name, account_key, status, validation_status)
		VALUES ($1, 1, 'Stripe Purpose', 'acct_DirPurpose01', 'published', 'verified')`, accID); err != nil {
		t.Fatal(err)
	}
	v1Secret := "rk_test_dir_v1_no_files"
	v1CT, err := ring.Encrypt("v1", v1Secret, credentials.CredentialAAD(accID, 1, "api_key"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO payment_channel_credentials(account_id, version, purpose, key_version, ciphertext, hint)
		VALUES ($1, 1, 'api_key', 'v1', $2, '••v1')`, accID, v1CT); err != nil {
		t.Fatal(err)
	}

	var seenAuth []string
	mockHTTP := &http.Client{Transport: mockRoundTripper(func(req *http.Request) (*http.Response, error) {
		auth := req.Header.Get("Authorization")
		seenAuth = append(seenAuth, auth)
		switch req.URL.Path {
		case "/v1/account":
			return mockJSONResponse(200, `{"id":"acct_DirPurpose01","object":"account"}`)
		case "/v1/payouts":
			return mockJSONResponse(200, `{"object":"list","data":[],"has_more":false}`)
		case "/v1/files":
			if auth == "Bearer "+v1Secret {
				return mockJSONResponse(403, `{"error":{"type":"invalid_request_error","message":"missing files permission"}}`)
			}
			return mockJSONResponse(200, `{"object":"list","data":[],"has_more":false}`)
		default:
			t.Fatalf("unexpected Stripe request: %s %s", req.Method, req.URL.Path)
			return nil, nil
		}
	})}

	dir := New(pool, payment.NewAdapters()).WithStripeHTTPClient(mockHTTP)
	scope := payment.WebhookAccount{AccountKey: "acct_DirPurpose01", Environment: "sandbox"}

	// Payout purpose passes without requiring Files permission.
	if err := dir.VerifyStripePurpose(ctx, scope, stripe.PurposePayout); err != nil {
		t.Fatalf("expected payout purpose to succeed with v1 key: %v", err)
	}

	// Account check passes, but Files permission is missing -> structured CredentialError.
	err = dir.VerifyStripePurpose(ctx, scope, stripe.PurposeFiles)
	var credErr *stripe.CredentialError
	if !errors.As(err, &credErr) || credErr.Purpose != stripe.PurposeFiles || credErr.Reason != stripe.ReasonPermissionDenied || credErr.HTTPStatus != 403 || !credErr.Retryable {
		t.Fatalf("expected files permission_denied CredentialError, got %v", err)
	}
	if strings.Contains(err.Error(), v1Secret) {
		t.Fatalf("error leaked secret: %s", err.Error())
	}

	// Wrong environment -> structured ReasonEnvironmentMismatch.
	_, err = dir.ResolveStripeClient(ctx, payment.WebhookAccount{AccountKey: "acct_DirPurpose01", Environment: "live"}, stripe.PurposePayout)
	if !errors.As(err, &credErr) || credErr.Reason != stripe.ReasonEnvironmentMismatch || credErr.Purpose != stripe.PurposePayout {
		t.Fatalf("expected environment_mismatch CredentialError, got %v", err)
	}

	// Wrong account returned by Stripe /v1/account -> ReasonAccountMismatch.
	wrongAccountDir := New(pool, payment.NewAdapters()).WithStripeHTTPClient(&http.Client{Transport: mockRoundTripper(func(req *http.Request) (*http.Response, error) {
		return mockJSONResponse(200, `{"id":"acct_OtherMerchant","object":"account"}`)
	})})
	_, err = wrongAccountDir.ResolveStripeClient(ctx, scope, stripe.PurposeAftercare)
	if !errors.As(err, &credErr) || credErr.Reason != stripe.ReasonAccountMismatch || credErr.Purpose != stripe.PurposeAftercare {
		t.Fatalf("expected account_mismatch CredentialError, got %v", err)
	}

	// Undecryptable credentials -> ReasonCredentialDecrypt without leaking ciphertext.
	t.Setenv("PAYMENT_CREDENTIAL_KEYS", "")
	_, err = dir.ResolveStripeClient(ctx, scope, stripe.PurposeReportRun)
	if !errors.As(err, &credErr) || credErr.Reason != stripe.ReasonCredentialDecrypt || credErr.Purpose != stripe.PurposeReportRun || !credErr.Retryable {
		t.Fatalf("expected credential_decrypt_failed CredentialError, got %v", err)
	}
	if strings.Contains(err.Error(), v1CT) || strings.Contains(err.Error(), v1Secret) {
		t.Fatalf("decrypt error leaked secret/ciphertext: %s", err.Error())
	}
	t.Setenv("PAYMENT_CREDENTIAL_KEYS", validKeyring)

	// Rotate to version 2 with Files permission enabled -> succeeds without restart.
	v2Secret := "rk_test_dir_v2_with_files"
	v2CT, err := ring.Encrypt("v1", v2Secret, credentials.CredentialAAD(accID, 2, "api_key"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO payment_channel_account_versions(account_id, version, display_name, account_key, status, validation_status)
		VALUES ($1, 2, 'Stripe Purpose', 'acct_DirPurpose01', 'published', 'verified')`, accID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO payment_channel_credentials(account_id, version, purpose, key_version, ciphertext, hint)
		VALUES ($1, 2, 'api_key', 'v1', $2, '••v2')`, accID, v2CT); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE payment_channel_accounts SET active_version = 2, pending_version = 2 WHERE id = $1`, accID); err != nil {
		t.Fatal(err)
	}
	if err := dir.VerifyStripePurpose(ctx, scope, stripe.PurposeFiles); err != nil {
		t.Fatalf("expected rotated v2 key to pass files verification: %v", err)
	}

	// Disabled merchant still resolves for historical reconciliation/aftercare.
	if _, err := pool.Exec(ctx, `UPDATE payment_channel_accounts SET status='disabled', is_primary=false, new_orders_enabled=false, new_subscriptions_enabled=false WHERE id=$1`, accID); err != nil {
		t.Fatal(err)
	}
	if _, err := dir.ResolveStripeClient(ctx, scope, stripe.PurposePayout); err != nil {
		t.Fatalf("expected disabled merchant to resolve for historical payout: %v", err)
	}

	// Legacy static channel still uses env var fallback.
	if _, err := pool.Exec(ctx, `
		INSERT INTO payment_channel_accounts(
			provider, environment, account_key, display_name, credentials_ref, webhook_ref,
			status, is_primary, active_version, pending_version
		) VALUES (
			'stripe', 'sandbox', 'acct_LegacyStatic01', 'Stripe Legacy', 'env:stripe', 'env:webhook',
			'published', false, 1, 1
		)`); err != nil {
		t.Fatal(err)
	}
	t.Setenv("STRIPE_LEDGER_API_KEY", "sk_test_legacy_env_key")
	legacyDir := New(pool, payment.NewAdapters()).WithStripeHTTPClient(&http.Client{Transport: mockRoundTripper(func(req *http.Request) (*http.Response, error) {
		if req.Header.Get("Authorization") != "Bearer sk_test_legacy_env_key" {
			t.Fatalf("expected legacy env key, got %s", req.Header.Get("Authorization"))
		}
		return mockJSONResponse(200, `{"id":"acct_LegacyStatic01","object":"account"}`)
	})})
	if _, err := legacyDir.ResolveStripeClient(ctx, payment.WebhookAccount{AccountKey: "acct_LegacyStatic01", Environment: "sandbox"}, stripe.PurposePayout); err != nil {
		t.Fatalf("expected legacy static channel to resolve from env fallback: %v", err)
	}
}
