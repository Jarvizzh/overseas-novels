package settlement_test

import (
	"crypto/rand"
	"encoding/base64"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"reader-backend/internal/credentials"
	"reader-backend/internal/payment"
	"reader-backend/internal/payment/directory"
	"reader-backend/internal/payment/settlement"
	"reader-backend/internal/payment/stripe"
	"reader-backend/internal/testdb"
)

func seedEncryptedStripeAccount(t *testing.T, pool *pgxpool.Pool, accountKey, apiKey string) (int64, *credentials.Keyring) {
	t.Helper()
	ctx := t.Context()

	keyBytes := make([]byte, 32)
	if _, err := rand.Read(keyBytes); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PAYMENT_CREDENTIAL_KEYS", "v1:"+base64.StdEncoding.EncodeToString(keyBytes))
	ring, err := credentials.LoadFromEnv()
	if err != nil {
		t.Fatal(err)
	}

	if _, err := pool.Exec(ctx, `INSERT INTO admins(id,username,password_hash,role,role_id,status)
		VALUES('finance-report','finance-report','none','FinanceManager',(SELECT id FROM roles WHERE code='FinanceManager'),1)
		ON CONFLICT (id) DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	var accID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO payment_channel_accounts(
			provider, environment, account_key, display_name, credentials_ref, webhook_ref,
			status, is_primary, active_version, pending_version, new_orders_enabled, new_subscriptions_enabled
		) VALUES (
			'stripe', 'sandbox', $1, 'Stripe Reports', 'db:encrypted', 'channel_token',
			'published', true, 1, 1, true, true
		) RETURNING id`, accountKey).Scan(&accID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO payment_channel_account_versions(account_id, version, display_name, account_key, status, validation_status)
		VALUES ($1, 1, 'Stripe Reports', $2, 'published', 'verified')`, accID, accountKey); err != nil {
		t.Fatal(err)
	}
	ct, err := ring.Encrypt("v1", apiKey, credentials.CredentialAAD(accID, 1, "api_key"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO payment_channel_credentials(account_id, version, purpose, key_version, ciphertext, hint)
		VALUES ($1, 1, 'api_key', 'v1', $2, '••v1')`, accID, ct); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO payment_ledger_sync_settings(account_id, enabled, actor_id, reason, window_start, window_end, next_attempt_at)
		VALUES ($1, true, 'finance-report', 'scheduled reports', now() - interval '1 day', now(), now() + interval '1 day')`,
		accID); err != nil {
		t.Fatal(err)
	}
	return accID, ring
}

func TestStripeReportRunRestartRecoveryAndDuplicateDeliveryImportsOnce(t *testing.T) {
	pool := testdb.Open(t)
	ctx := t.Context()
	apiKey := "rk_test_report_sched_secret_01"
	accID, _ := seedEncryptedStripeAccount(t, pool, "acct_ReportSched01", apiKey)
	account := payment.WebhookAccount{AccountKey: "acct_ReportSched01", Environment: "sandbox"}

	var createCalls atomic.Int32
	var getCalls atomic.Int32
	var downloadCalls atomic.Int32
	var runReady atomic.Bool

	mockHTTP := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.Method + " " + req.URL.Host + req.URL.Path {
		case "GET api.stripe.com/v1/account":
			return stripeHTTPResponse(200, "application/json", `{"id":"acct_ReportSched01","object":"account"}`)
		case "POST api.stripe.com/v1/reporting/report_runs":
			createCalls.Add(1)
			raw, _ := io.ReadAll(req.Body)
			body := string(raw)
			if !strings.Contains(body, stripe.ReportColumnWithheldTax) || !strings.Contains(body, stripe.ReportColumnFeeNetOfWithheldTax) {
				t.Errorf("scheduled MoR report run did not request withheld_tax and fee_net_of_withheld_tax: %s", body)
			}
			return stripeHTTPResponse(200, "application/json", `{"id":"frr_sched_01","object":"reporting.report_run","status":"pending","livemode":false,"report_type":"balance_change_from_activity.itemized.6"}`)
		case "GET api.stripe.com/v1/reporting/report_runs/frr_sched_01":
			getCalls.Add(1)
			if !runReady.Load() {
				return stripeHTTPResponse(200, "application/json", `{"id":"frr_sched_01","object":"reporting.report_run","status":"pending","livemode":false,"report_type":"balance_change_from_activity.itemized.6"}`)
			}
			return stripeHTTPResponse(200, "application/json", `{"id":"frr_sched_01","object":"reporting.report_run","status":"succeeded","livemode":false,"report_type":"balance_change_from_activity.itemized.6","result":{"id":"file_sched_01","type":"csv","purpose":"finance_report_run","url":"https://files.stripe.com/v1/files/file_sched_01/contents"}}`)
		case "GET files.stripe.com/v1/files/file_sched_01/contents":
			downloadCalls.Add(1)
			return stripeHTTPResponse(200, "text/csv",
				"balance_transaction_id,source_id,charge_id,payment_intent_id,invoice_id,subscription_id,reporting_category,currency,gross,fee,net,customer_facing_currency,customer_facing_amount,exchange_rate,is_managed_payments,withheld_tax,fee_net_of_withheld_tax\n"+
					"txn_mor_sched01,ch_mor_sched01,ch_mor_sched01,pi_mor_sched01,,,charge,usd,10.00,1.80,8.20,usd,10.00,1.0,true,1.23,0.57\n")
		default:
			t.Fatalf("unexpected Stripe request: %s %s", req.Method, req.URL)
			return nil, nil
		}
	})}

	dir := directory.New(pool, payment.NewAdapters()).WithStripeHTTPClient(mockHTTP)
	repo1 := settlement.NewRepository(pool).WithStripeResolver(dir)

	start := time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC)
	req := settlement.ReportScheduleRequest{
		Account:           account,
		ReportType:        stripe.DefaultBalanceChangeReportType,
		IntervalStart:     start,
		IntervalEnd:       end,
		IncludeMoRColumns: true,
		ActorID:           "finance-report",
		Reason:            "daily MoR activity report",
	}

	// 1. EnsureReportRunSchedule is idempotent for identical (account, env, type, params).
	rec1, created1, err := repo1.EnsureReportRunSchedule(ctx, req)
	if err != nil || !created1 {
		t.Fatalf("first EnsureReportRunSchedule failed: created=%v err=%v", created1, err)
	}
	rec2, created2, err := repo1.EnsureReportRunSchedule(ctx, req)
	if err != nil || created2 || rec2.ID != rec1.ID || rec2.ScheduleKey != rec1.ScheduleKey {
		t.Fatalf("duplicate EnsureReportRunSchedule should reuse existing row: rec1=%+v rec2=%+v created2=%v err=%v", rec1, rec2, created2, err)
	}

	// 2. First worker tick creates the Report Run and persists report_run_id (status=running).
	worked, err := repo1.ProcessNextStripeReportRun(ctx, "sandbox")
	if err != nil || !worked {
		t.Fatalf("first ProcessNextStripeReportRun failed: worked=%v err=%v", worked, err)
	}
	if createCalls.Load() != 1 {
		t.Fatalf("expected exactly 1 CreateReportRun call, got %d", createCalls.Load())
	}
	var persistedRunID, status string
	if err := pool.QueryRow(ctx, `SELECT COALESCE(report_run_id,''), status FROM payment_stripe_report_runs WHERE id=$1`, rec1.ID).
		Scan(&persistedRunID, &status); err != nil {
		t.Fatal(err)
	}
	if persistedRunID != "frr_sched_01" || status != settlement.ReportRunStatusRunning {
		t.Fatalf("expected persisted report_run_id=frr_sched_01 status=running, got runID=%q status=%q", persistedRunID, status)
	}

	// 3. Simulate process restart (brand-new Repository instance) and due poll while still pending.
	if _, err := pool.Exec(ctx, `UPDATE payment_stripe_report_runs SET next_attempt_at = now() - interval '1 minute' WHERE id=$1`, rec1.ID); err != nil {
		t.Fatal(err)
	}
	repo2 := settlement.NewRepository(pool).WithStripeResolver(dir)
	if worked, err := repo2.ProcessNextStripeReportRun(ctx, "sandbox"); err != nil || !worked {
		t.Fatalf("restarted poll failed: worked=%v err=%v", worked, err)
	}
	if createCalls.Load() != 1 || getCalls.Load() != 1 {
		t.Fatalf("restart must reuse persisted report_run_id without re-creating: creates=%d gets=%d", createCalls.Load(), getCalls.Load())
	}

	// 4. Report Run succeeds: BOTH poll completion AND duplicate reporting.report_run.succeeded
	// webhooks fire for frr_sched_01 -> must enqueue only ONE stripe_report command and import ONCE.
	runReady.Store(true)
	if _, err := pool.Exec(ctx, `UPDATE payment_stripe_report_runs SET next_attempt_at = now() - interval '1 minute' WHERE id=$1`, rec1.ID); err != nil {
		t.Fatal(err)
	}
	if worked, err := repo2.ProcessNextStripeReportRun(ctx, "sandbox"); err != nil || !worked {
		t.Fatalf("poll completion failed: worked=%v err=%v", worked, err)
	}
	// Duplicate webhook deliveries with different event IDs for the same report run.
	if err := repo2.ScheduleStripeNotice(ctx, account, "evt_report_dup_1", "frr_sched_01"); err != nil {
		t.Fatalf("webhook notice 1 failed: %v", err)
	}
	if err := repo2.ScheduleStripeNotice(ctx, account, "evt_report_dup_2", "frr_sched_01"); err != nil {
		t.Fatalf("webhook notice 2 failed: %v", err)
	}

	var cmdCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM payment_settlement_commands WHERE account_id=$1 AND kind='stripe_report'`, accID).Scan(&cmdCount); err != nil {
		t.Fatal(err)
	}
	if cmdCount != 1 {
		t.Fatalf("expected exactly 1 enqueued stripe_report command across poll + duplicate webhooks, got %d", cmdCount)
	}

	// Execute settlement command queue.
	if worked, err := repo2.ExecuteNextCommand(ctx, "sandbox"); err != nil || !worked {
		t.Fatalf("ExecuteNextCommand failed: worked=%v err=%v", worked, err)
	}
	if worked, err := repo2.ExecuteNextCommand(ctx, "sandbox"); err != nil || worked {
		t.Fatalf("expected no second stripe_report command to execute: worked=%v err=%v", worked, err)
	}
	if downloadCalls.Load() != 1 {
		t.Fatalf("expected exactly 1 CSV download, got %d", downloadCalls.Load())
	}

	// Verify MoR withheld_tax and fee_net_of_withheld_tax booked accurately in BOTH ledger and settlement rows.
	var withheldTax, processingFee int64
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT amount_minor FROM payment_ledger_amounts a JOIN payment_ledger_movements m ON m.id=a.movement_id WHERE m.movement_key='txn_mor_sched01' AND a.component='withheld_tax'),
			(SELECT amount_minor FROM payment_ledger_amounts a JOIN payment_ledger_movements m ON m.id=a.movement_id WHERE m.movement_key='txn_mor_sched01' AND a.component='processing_fee')`).
		Scan(&withheldTax, &processingFee); err != nil {
		t.Fatal(err)
	}
	if withheldTax != -123 || processingFee != -57 {
		t.Fatalf("unexpected ledger MoR tax/fee amounts: withheld_tax=%d processing_fee=%d", withheldTax, processingFee)
	}
	var rowWithheldTax, rowFeeNet int64
	if err := pool.QueryRow(ctx, `SELECT withheld_tax_minor, fee_net_of_withheld_tax_minor FROM payment_settlement_rows WHERE source_file='file_sched_01' AND row_number=1`).
		Scan(&rowWithheldTax, &rowFeeNet); err != nil {
		t.Fatal(err)
	}
	if rowWithheldTax != -123 || rowFeeNet != -57 {
		t.Fatalf("unexpected settlement_rows MoR tax/fee amounts: withheld_tax_minor=%d fee_net_of_withheld_tax_minor=%d", rowWithheldTax, rowFeeNet)
	}

	var importCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM payment_settlement_imports WHERE account_id=$1 AND source='stripe_report' AND external_ref='frr_sched_01'`, accID).Scan(&importCount); err != nil {
		t.Fatal(err)
	}
	if importCount != 1 {
		t.Fatalf("expected exactly 1 settlement import row, got %d", importCount)
	}
}

func TestStripeReportScheduleMissingPermissionsAndManualImportCompatibility(t *testing.T) {
	pool := testdb.Open(t)
	ctx := t.Context()
	restrictedKey := "rk_test_no_report_run_perm_secret"
	accID, _ := seedEncryptedStripeAccount(t, pool, "acct_ReportPerm01", restrictedKey)
	account := payment.WebhookAccount{AccountKey: "acct_ReportPerm01", Environment: "sandbox"}

	mockHTTP := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.Method + " " + req.URL.Host + req.URL.Path {
		case "GET api.stripe.com/v1/account":
			return stripeHTTPResponse(200, "application/json", `{"id":"acct_ReportPerm01","object":"account"}`)
		case "POST api.stripe.com/v1/reporting/report_runs":
			return stripeHTTPResponse(403, "application/json", `{"error":{"type":"invalid_request_error","message":"The provided key does not have reporting.report_runs write permission"}}`)
		case "GET api.stripe.com/v1/reporting/report_runs/frr_manual_compat":
			return stripeHTTPResponse(200, "application/json", `{"id":"frr_manual_compat","status":"succeeded","livemode":false,"report_type":"payout_reconciliation.itemized.7","result":{"id":"file_manual_compat","type":"csv","purpose":"finance_report_run","url":"https://files.stripe.com/v1/files/file_manual_compat/contents"}}`)
		case "GET files.stripe.com/v1/files/file_manual_compat/contents":
			return stripeHTTPResponse(200, "text/csv", "balance_transaction_id,source_id,automatic_payout_id,reporting_category,currency,gross,fee,net\ntxn_manual_01,ch_manual_01,po_manual_01,charge,usd,15.00,0.50,14.50\n")
		case "GET api.stripe.com/v1/reporting/report_runs/frr_mor_missing_col":
			return stripeHTTPResponse(200, "application/json", `{"id":"frr_mor_missing_col","status":"succeeded","livemode":false,"report_type":"balance_change_from_activity.itemized.6","result":{"id":"file_mor_missing","type":"csv","purpose":"finance_report_run","url":"https://files.stripe.com/v1/files/file_mor_missing/contents"}}`)
		case "GET files.stripe.com/v1/files/file_mor_missing/contents":
			return stripeHTTPResponse(200, "text/csv", "balance_transaction_id,source_id,reporting_category,currency,gross,fee,net\ntxn_missing_01,ch_missing_01,charge,usd,10.00,1.80,8.20\n")
		default:
			t.Fatalf("unexpected Stripe request: %s %s", req.Method, req.URL)
			return nil, nil
		}
	})}

	dir := directory.New(pool, payment.NewAdapters()).WithStripeHTTPClient(mockHTTP)
	repo := settlement.NewRepository(pool).WithStripeResolver(dir)

	// 1. Scheduled Report Run fails with 403 Report Run permission -> recorded with purpose=report_run, no secret leaked.
	created, err := repo.ScheduleDueStripeReportRuns(ctx, "sandbox", settlement.ReportScheduleConfig{
		BalanceChangeReportType:        stripe.DefaultBalanceChangeReportType,
		PayoutReconciliationReportType: stripe.DefaultPayoutReconciliationReportType,
		IncludeMoRColumns:              true,
	}, time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC))
	if err != nil || created != 2 {
		t.Fatalf("ScheduleDueStripeReportRuns expected 2 schedules, got created=%d err=%v", created, err)
	}
	if worked, err := repo.ProcessNextStripeReportRun(ctx, "sandbox"); err != nil || !worked {
		t.Fatalf("ProcessNextStripeReportRun failed unexpectedly: worked=%v err=%v", worked, err)
	}
	var lastErr string
	if err := pool.QueryRow(ctx, `SELECT COALESCE(last_error,'') FROM payment_stripe_report_runs WHERE account_id=$1 ORDER BY id LIMIT 1`, accID).Scan(&lastErr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(lastErr, "purpose=report_run") || !strings.Contains(lastErr, "reason=permission_denied") {
		t.Fatalf("expected structured report_run permission_denied error, got %q", lastErr)
	}
	if strings.Contains(lastErr, restrictedKey) {
		t.Fatalf("last_error leaked API key: %q", lastErr)
	}

	// 2. Manual stripe_report import without MoR options remains 100% backward compatible.
	manualCmdID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO payment_settlement_commands
		(id,request_key,actor_id,kind,account_id,environment,reason,parameters,request_digest)
		VALUES($1,$1,'finance-report','stripe_report',$2,'sandbox','manual import','{"reference":"frr_manual_compat"}','manual-1')`,
		manualCmdID, accID); err != nil {
		t.Fatal(err)
	}
	if worked, err := repo.ExecuteNextCommand(ctx, "sandbox"); err != nil || !worked {
		t.Fatalf("manual stripe_report execution failed: worked=%v err=%v", worked, err)
	}
	var status string
	var rows int
	if err := pool.QueryRow(ctx, `SELECT status, COALESCE((result->>'rows')::int,0) FROM payment_settlement_commands WHERE id=$1`, manualCmdID).Scan(&status, &rows); err != nil {
		t.Fatal(err)
	}
	if status != "succeeded" || rows != 1 {
		t.Fatalf("expected manual stripe_report to succeed with 1 row, got status=%q rows=%d", status, rows)
	}

	// 3. MoR stripe_report command missing withheld_tax / fee_net_of_withheld_tax fails with diagnostic error.
	morMissingCmdID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO payment_settlement_commands
		(id,request_key,actor_id,kind,account_id,environment,reason,parameters,request_digest)
		VALUES($1,$1,'finance-report','stripe_report',$2,'sandbox','scheduled MoR import','{"reference":"frr_mor_missing_col","require_mor_columns":true}','mor-miss-1')`,
		morMissingCmdID, accID); err != nil {
		t.Fatal(err)
	}
	if worked, err := repo.ExecuteNextCommand(ctx, "sandbox"); err != nil || !worked {
		t.Fatalf("MoR missing-col stripe_report execution failed unexpectedly: worked=%v err=%v", worked, err)
	}
	var cmdErr string
	if err := pool.QueryRow(ctx, `SELECT status, COALESCE(error,'') FROM payment_settlement_commands WHERE id=$1`, morMissingCmdID).Scan(&status, &cmdErr); err != nil {
		t.Fatal(err)
	}
	if status != "failed" || !strings.Contains(cmdErr, "withheld_tax") || !strings.Contains(cmdErr, "fee_net_of_withheld_tax") {
		t.Fatalf("expected failed command naming missing MoR columns, got status=%q err=%q", status, cmdErr)
	}
	var importedRows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM payment_settlement_imports WHERE account_id=$1 AND external_ref='frr_mor_missing_col'`, accID).Scan(&importedRows); err != nil {
		t.Fatal(err)
	}
	if importedRows != 0 {
		t.Fatalf("expected 0 imports when MoR columns are missing, got %d", importedRows)
	}
	_ = account
}
