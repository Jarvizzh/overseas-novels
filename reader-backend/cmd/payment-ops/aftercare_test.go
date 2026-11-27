package main

import (
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
	"reader-backend/internal/payment/aftercare"
	"reader-backend/internal/payment/directory"
	"reader-backend/internal/payment/stripe"
	"reader-backend/internal/testdb"
)

type opsRoundTripper func(*http.Request) (*http.Response, error)

func (f opsRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func opsStripeResponse(status int, body string) (*http.Response, error) {
	header := make(http.Header)
	header.Set("Content-Type", "application/json")
	header.Set("Request-Id", "req_ops_test")
	return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body))}, nil
}

func TestRunStripeAftercareUsesEncryptedDirectoryRotationAndDisabledMerchant(t *testing.T) {
	t.Setenv("STRIPE_AFTERCARE_API_KEY", "")
	t.Setenv("STRIPE_LEDGER_API_KEY", "")

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
	ctx := t.Context()

	var accID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO payment_channel_accounts(
			provider, environment, account_key, display_name, credentials_ref, webhook_ref,
			status, is_primary, active_version, pending_version, new_orders_enabled, new_subscriptions_enabled
		) VALUES (
			'stripe', 'sandbox', 'acct_OpsDyn01', 'Stripe Ops', 'db:encrypted', 'channel_token',
			'published', true, 1, 1, true, true
		) RETURNING id`).Scan(&accID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO payment_channel_account_versions(account_id, version, display_name, account_key, status, validation_status)
		VALUES ($1, 1, 'Stripe Ops', 'acct_OpsDyn01', 'published', 'verified')`, accID); err != nil {
		t.Fatal(err)
	}

	v1Key := "rk_test_ops_v1"
	v1CT, err := ring.Encrypt("v1", v1Key, credentials.CredentialAAD(accID, 1, "api_key"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO payment_channel_credentials(account_id, version, purpose, key_version, ciphertext, hint)
		VALUES ($1, 1, 'api_key', 'v1', $2, '••v1')`, accID, v1CT); err != nil {
		t.Fatal(err)
	}

	v2Key := "rk_test_ops_v2"
	mockHTTP := &http.Client{Transport: opsRoundTripper(func(req *http.Request) (*http.Response, error) {
		auth := req.Header.Get("Authorization")
		switch req.URL.Path {
		case "/v1/account":
			return opsStripeResponse(200, `{"id":"acct_OpsDyn01","object":"account"}`)
		case "/v1/disputes/dp_ops01":
			if auth != "Bearer "+v1Key {
				t.Fatalf("expected v1 key, got %s", auth)
			}
			return opsStripeResponse(200, `{"id":"dp_ops01","object":"dispute","amount":500,"currency":"usd","status":"needs_response","reason":"fraudulent","charge":"ch_ops01","livemode":false}`)
		case "/v1/disputes/dp_denied":
			return opsStripeResponse(403, `{"error":{"type":"invalid_request_error","message":"missing dispute read permission"}}`)
		case "/v1/disputes/dp_hist01":
			if auth != "Bearer "+v2Key {
				t.Fatalf("expected rotated v2 key, got %s", auth)
			}
			return opsStripeResponse(200, `{"id":"dp_hist01","object":"dispute","amount":700,"currency":"usd","status":"won","reason":"general","charge":"ch_hist01","livemode":false}`)
		default:
			t.Fatalf("unexpected Stripe request: %s %s", req.Method, req.URL.Path)
			return nil, nil
		}
	})}

	dir := directory.New(pool, payment.NewAdapters()).WithStripeHTTPClient(mockHTTP)
	repo := aftercare.NewRepository(pool)

	// 1. Aftercare dispute recovery works with CMS encrypted key only.
	if err := runStripeAftercare(ctx, dir, repo, "acct_OpsDyn01", "sandbox", "dp_ops01", time.Time{}, time.Time{}); err != nil {
		t.Fatalf("aftercare dp_ops01 failed: %v", err)
	}

	// 2. Missing permission returns structured CredentialError with PurposeAftercare and no secret.
	err = runStripeAftercare(ctx, dir, repo, "acct_OpsDyn01", "sandbox", "dp_denied", time.Time{}, time.Time{})
	var credErr *stripe.CredentialError
	if !errors.As(err, &credErr) || credErr.Purpose != stripe.PurposeAftercare || credErr.Reason != stripe.ReasonPermissionDenied || credErr.HTTPStatus != 403 {
		t.Fatalf("expected aftercare permission_denied CredentialError, got %v", err)
	}
	if strings.Contains(err.Error(), v1Key) {
		t.Fatalf("error leaked secret: %s", err.Error())
	}

	// 3. Wrong environment returns structured ReasonEnvironmentMismatch.
	err = runStripeAftercare(ctx, dir, repo, "acct_OpsDyn01", "live", "dp_ops01", time.Time{}, time.Time{})
	if !errors.As(err, &credErr) || credErr.Reason != stripe.ReasonEnvironmentMismatch {
		t.Fatalf("expected environment_mismatch CredentialError, got %v", err)
	}

	// 4. Key rotation + disabled merchant still recovers historical dispute.
	v2CT, err := ring.Encrypt("v1", v2Key, credentials.CredentialAAD(accID, 2, "api_key"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO payment_channel_account_versions(account_id, version, display_name, account_key, status, validation_status)
		VALUES ($1, 2, 'Stripe Ops', 'acct_OpsDyn01', 'published', 'verified')`, accID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO payment_channel_credentials(account_id, version, purpose, key_version, ciphertext, hint)
		VALUES ($1, 2, 'api_key', 'v1', $2, '••v2')`, accID, v2CT); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE payment_channel_accounts SET active_version = 2, pending_version = 2, status = 'disabled', is_primary = false, new_orders_enabled = false, new_subscriptions_enabled = false WHERE id = $1`, accID); err != nil {
		t.Fatal(err)
	}
	if err := runStripeAftercare(ctx, dir, repo, "acct_OpsDyn01", "sandbox", "dp_hist01", time.Time{}, time.Time{}); err != nil {
		t.Fatalf("disabled merchant rotated-key aftercare failed: %v", err)
	}
}
