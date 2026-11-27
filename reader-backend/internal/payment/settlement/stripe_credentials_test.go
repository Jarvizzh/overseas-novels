package settlement_test

import (
	"crypto/rand"
	"encoding/base64"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"reader-backend/internal/credentials"
	"reader-backend/internal/payment"
	"reader-backend/internal/payment/directory"
	"reader-backend/internal/payment/settlement"
	"reader-backend/internal/testdb"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func stripeHTTPResponse(status int, contentType, body string) (*http.Response, error) {
	header := make(http.Header)
	header.Set("Content-Type", contentType)
	header.Set("Request-Id", "req_settle_test")
	return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body))}, nil
}

func TestStripeAutomaticPayoutAndManualCommandsUseEncryptedDirectory(t *testing.T) {
	// Prove zero dependency on Stripe env vars for CMS encrypted accounts.
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
	ctx := t.Context()

	if _, err := pool.Exec(ctx, `INSERT INTO admins(id,username,password_hash,role,role_id,status)
		VALUES('finance-dyn','finance-dyn','none','FinanceManager',(SELECT id FROM roles WHERE code='FinanceManager'),1)`); err != nil {
		t.Fatal(err)
	}
	var accID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO payment_channel_accounts(
			provider, environment, account_key, display_name, credentials_ref, webhook_ref,
			status, is_primary, active_version, pending_version, new_orders_enabled, new_subscriptions_enabled
		) VALUES (
			'stripe', 'sandbox', 'acct_SettleDyn01', 'Stripe Dynamic', 'db:encrypted', 'channel_token',
			'published', true, 1, 1, true, true
		) RETURNING id`).Scan(&accID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO payment_channel_account_versions(account_id, version, display_name, account_key, status, validation_status)
		VALUES ($1, 1, 'Stripe Dynamic', 'acct_SettleDyn01', 'published', 'verified')`, accID); err != nil {
		t.Fatal(err)
	}

	v1Key := "rk_test_settle_v1_no_files"
	v1CT, err := ring.Encrypt("v1", v1Key, credentials.CredentialAAD(accID, 1, "api_key"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO payment_channel_credentials(account_id, version, purpose, key_version, ciphertext, hint)
		VALUES ($1, 1, 'api_key', 'v1', $2, '••v1')`, accID, v1CT); err != nil {
		t.Fatal(err)
	}

	mockHTTP := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		auth := req.Header.Get("Authorization")
		switch req.URL.Host + req.URL.Path {
		case "api.stripe.com/v1/account":
			return stripeHTTPResponse(200, "application/json", `{"id":"acct_SettleDyn01","object":"account"}`)
		case "api.stripe.com/v1/payouts":
			return stripeHTTPResponse(200, "application/json", `{"object":"list","data":[{"id":"po_auto01","object":"payout","amount":900,"currency":"usd","status":"paid","type":"bank_account","automatic":true,"method":"standard","reconciliation_status":"not_applicable","livemode":false,"arrival_date":1763683200,"balance_transaction":"txn_po_auto01"}],"has_more":false}`)
		case "api.stripe.com/v1/payouts/po_auto01":
			return stripeHTTPResponse(200, "application/json", `{"id":"po_auto01","object":"payout","amount":900,"currency":"usd","status":"paid","type":"bank_account","automatic":true,"method":"standard","reconciliation_status":"not_applicable","livemode":false,"arrival_date":1763683200,"balance_transaction":"txn_po_auto01"}`)
		case "api.stripe.com/v1/balance_transactions/txn_po_auto01":
			return stripeHTTPResponse(200, "application/json", `{"id":"txn_po_auto01","object":"balance_transaction","amount":-900,"fee":0,"net":-900,"currency":"usd","type":"payout","reporting_category":"payout","status":"available","created":1763683200,"available_on":1763683200,"source":"po_auto01"}`)
		case "api.stripe.com/v1/payouts/po_manual01":
			return stripeHTTPResponse(200, "application/json", `{"id":"po_manual01","object":"payout","amount":1200,"currency":"usd","status":"paid","type":"bank_account","automatic":true,"method":"standard","reconciliation_status":"not_applicable","livemode":false,"arrival_date":1763683200,"balance_transaction":"txn_po_manual01"}`)
		case "api.stripe.com/v1/balance_transactions/txn_po_manual01":
			return stripeHTTPResponse(200, "application/json", `{"id":"txn_po_manual01","object":"balance_transaction","amount":-1200,"fee":0,"net":-1200,"currency":"usd","type":"payout","reporting_category":"payout","status":"available","created":1763683200,"available_on":1763683200,"source":"po_manual01"}`)
		case "api.stripe.com/v1/reporting/report_runs/frr_perm01":
			return stripeHTTPResponse(200, "application/json", `{"id":"frr_perm01","status":"succeeded","livemode":false,"report_type":"payout_reconciliation.itemized.7","result":{"id":"file_perm01","type":"csv","purpose":"finance_report_run","url":"https://files.stripe.com/v1/files/file_perm01/contents"}}`)
		case "files.stripe.com/v1/files/file_perm01/contents":
			if auth == "Bearer "+v1Key {
				return stripeHTTPResponse(403, "application/json", `{"error":{"type":"invalid_request_error","message":"Missing files permission"}}`)
			}
			return stripeHTTPResponse(200, "text/csv", "balance_transaction_id,source_id,automatic_payout_id,reporting_category,currency,gross,fee,net\ntxn_csv01,ch_csv01,po_manual01,charge,usd,12.00,0,12.00\n")
		default:
			t.Fatalf("unexpected Stripe request: %s %s", req.Method, req.URL)
			return nil, nil
		}
	})}

	dir := directory.New(pool, payment.NewAdapters()).WithStripeHTTPClient(mockHTTP)
	repo := settlement.NewRepository(pool).WithStripeResolver(dir)

	// 1. Automatic payout sync succeeds using only CMS encrypted credentials.
	if _, err := pool.Exec(ctx, `
		INSERT INTO payment_ledger_sync_settings(account_id, enabled, actor_id, reason, window_start, window_end, next_attempt_at)
		VALUES ($1, true, 'finance-dyn', 'auto sync', $2, $3, now() - interval '1 minute')`,
		accID, time.Now().UTC().Add(-24*time.Hour), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	worked, err := repo.SyncNextLedgerPage(ctx, "sandbox")
	if err != nil || !worked {
		t.Fatalf("automatic ledger sync failed: worked=%v err=%v", worked, err)
	}
	var lastErr *string
	if err := pool.QueryRow(ctx, `SELECT last_error FROM payment_ledger_sync_settings WHERE account_id=$1`, accID).Scan(&lastErr); err != nil {
		t.Fatal(err)
	}
	if lastErr != nil {
		t.Fatalf("expected nil last_error after successful auto sync, got %q", *lastErr)
	}

	// 2. Manual stripe_report fails with 403 Files permission on v1 key (recording purpose & reason, no secret).
	reportCmdID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO payment_settlement_commands(id,request_key,actor_id,kind,account_id,environment,reason,parameters,request_digest)
		VALUES($1,$1,'finance-dyn','stripe_report',$2,'sandbox','import report','{"reference":"frr_perm01"}','d1')`,
		reportCmdID, accID); err != nil {
		t.Fatal(err)
	}
	if worked, err := repo.ExecuteNextCommand(ctx, "sandbox"); err != nil || !worked {
		t.Fatalf("stripe_report execution failed unexpectedly: worked=%v err=%v", worked, err)
	}
	var cmdStatus, cmdErr string
	if err := pool.QueryRow(ctx, `SELECT status, COALESCE(error,'') FROM payment_settlement_commands WHERE id=$1`, reportCmdID).Scan(&cmdStatus, &cmdErr); err != nil {
		t.Fatal(err)
	}
	if cmdStatus != "failed" || !strings.Contains(cmdErr, "purpose=files") || !strings.Contains(cmdErr, "reason=permission_denied") {
		t.Fatalf("expected failed command with purpose=files & reason=permission_denied, got status=%q err=%q", cmdStatus, cmdErr)
	}
	if strings.Contains(cmdErr, v1Key) {
		t.Fatalf("command error leaked secret: %q", cmdErr)
	}

	// 3. Repair credentials by rotating to version 2 with Files permission, then retry the same command.
	v2Key := "rk_test_settle_v2_with_files"
	v2CT, err := ring.Encrypt("v1", v2Key, credentials.CredentialAAD(accID, 2, "api_key"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO payment_channel_account_versions(account_id, version, display_name, account_key, status, validation_status)
		VALUES ($1, 2, 'Stripe Dynamic', 'acct_SettleDyn01', 'published', 'verified')`, accID); err != nil {
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
	if _, err := pool.Exec(ctx, `UPDATE payment_settlement_commands SET status = 'pending', error = NULL WHERE id = $1`, reportCmdID); err != nil {
		t.Fatal(err)
	}
	if worked, err := repo.ExecuteNextCommand(ctx, "sandbox"); err != nil || !worked {
		t.Fatalf("retried stripe_report failed: worked=%v err=%v", worked, err)
	}
	var rows int
	if err := pool.QueryRow(ctx, `SELECT status, COALESCE((result->>'rows')::int, 0) FROM payment_settlement_commands WHERE id=$1`, reportCmdID).Scan(&cmdStatus, &rows); err != nil {
		t.Fatal(err)
	}
	if cmdStatus != "succeeded" || rows != 1 {
		t.Fatalf("expected retried stripe_report to succeed with 1 row, got status=%q rows=%d", cmdStatus, rows)
	}

	// 4. Disable merchant for new sales, then run manual historical stripe_payout -> still succeeds.
	if _, err := pool.Exec(ctx, `UPDATE payment_channel_accounts SET status='disabled', is_primary=false, new_orders_enabled=false, new_subscriptions_enabled=false WHERE id=$1`, accID); err != nil {
		t.Fatal(err)
	}
	payoutCmdID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO payment_settlement_commands(id,request_key,actor_id,kind,account_id,environment,reason,parameters,request_digest)
		VALUES($1,$1,'finance-dyn','stripe_payout',$2,'sandbox','historical payout','{"reference":"po_manual01"}','d2')`,
		payoutCmdID, accID); err != nil {
		t.Fatal(err)
	}
	if worked, err := repo.ExecuteNextCommand(ctx, "sandbox"); err != nil || !worked {
		t.Fatalf("disabled merchant stripe_payout failed: worked=%v err=%v", worked, err)
	}
	if err := pool.QueryRow(ctx, `SELECT status, COALESCE(error,'') FROM payment_settlement_commands WHERE id=$1`, payoutCmdID).Scan(&cmdStatus, &cmdErr); err != nil {
		t.Fatal(err)
	}
	if cmdStatus != "succeeded" || cmdErr != "" {
		t.Fatalf("expected disabled merchant stripe_payout to succeed, got status=%q err=%q", cmdStatus, cmdErr)
	}

	// 5. Undecryptable credentials during automatic sync record structured purpose+reason without leaking secrets.
	t.Setenv("PAYMENT_CREDENTIAL_KEYS", "")
	if _, err := pool.Exec(ctx, `UPDATE payment_ledger_sync_settings SET next_attempt_at = now() - interval '1 minute' WHERE account_id=$1`, accID); err != nil {
		t.Fatal(err)
	}
	if worked, err := repo.SyncNextLedgerPage(ctx, "sandbox"); err != nil || !worked {
		t.Fatalf("expected automatic sync to record decrypt error gracefully: worked=%v err=%v", worked, err)
	}
	if err := pool.QueryRow(ctx, `SELECT last_error FROM payment_ledger_sync_settings WHERE account_id=$1`, accID).Scan(&lastErr); err != nil {
		t.Fatal(err)
	}
	if lastErr == nil || !strings.Contains(*lastErr, "purpose=payout") || !strings.Contains(*lastErr, "reason=credential_decrypt_failed") {
		t.Fatalf("expected structured decrypt error in last_error, got %v", lastErr)
	}
	if strings.Contains(*lastErr, v2Key) || strings.Contains(*lastErr, v2CT) {
		t.Fatalf("last_error leaked secret/ciphertext: %q", *lastErr)
	}
}
