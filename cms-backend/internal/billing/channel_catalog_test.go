package billing

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	dbassets "star-novel-cms/db"
	"star-novel-cms/internal/auth"
	"star-novel-cms/internal/config"
	"star-novel-cms/internal/credentials"
	"star-novel-cms/internal/db"
)

func openTestBillingPool(t *testing.T, dsn string) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	database := "cms_bill_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+database); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(ctx, "DROP DATABASE "+database+" WITH (FORCE)"); err != nil {
			t.Error(err)
		}
		admin.Close()
	})
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.Database = database
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	for _, stmt := range []string{
		dbassets.SchemaSQL,
		dbassets.ChapterPublicationSQL, dbassets.ReaderPerformanceSQL, dbassets.ReaderQueueAndShelfSQL,
		dbassets.ConversionReportingSQL, dbassets.ReaderConversionSQL, dbassets.LegacyConversionSQL,
		dbassets.ConversionReconciliationSQL, dbassets.ConversionOperationsSQL, dbassets.ConversionReviewSQL,
		dbassets.SinanConversionSQL, dbassets.SinanReviewSQL, dbassets.SinanRecheckSQL,
		dbassets.PaymentPolicySQL, dbassets.ReaderPaymentPolicySQL, dbassets.SinanPaymentPolicySQL,
		dbassets.ReaderPaymentReviewSQL, dbassets.ReaderPaymentRecoverySQL, dbassets.ReaderSubscriptionEvidenceSQL,
		dbassets.PaymentDomainFoundationSQL, dbassets.PaymentCheckoutSQL, dbassets.VIPOfferVersionsSQL,
		dbassets.PaymentLedgerSQL, dbassets.PaymentSettlementSQL, dbassets.PaymentCheckoutDecisionsSQL,
		dbassets.PaymentAftercareSQL, dbassets.StripeOneTimeCheckoutSQL, dbassets.StripeVIPCheckoutSQL,
		dbassets.PaymentCompletionSQL, dbassets.PaymentRecoverySQL, dbassets.PaymentReviewFixesSQL,
		dbassets.PaymentCatalogAndChannelSQL, dbassets.PaymentVIPSwitchSQL, dbassets.PaymentTemplateCatalogFixesSQL,
		dbassets.ConversionCenterReviewSQL, dbassets.ConversionBatchSQL, dbassets.ConversionThroughputRereviewSQL,
		dbassets.ConversionCredentialHealthSQL, dbassets.RBACPermissionsSQL, dbassets.PromotionEnvironmentSQL, dbassets.StripeTaxLiabilityRelaxSQL, dbassets.StripeLocalCurrencyCustomerSQL, dbassets.StripeRefundDisputeMitigationSQL, dbassets.StripeRadarAndEventRecoverySQL, dbassets.StripeReportScheduleSQL,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	return pool
}

func setupChannelCatalogRouter(t *testing.T) (*gin.Engine, context.Context) {
	t.Helper()
	dsn := os.Getenv("STAR_NOVEL_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set STAR_NOVEL_TEST_DATABASE_URL to run billing DB tests")
	}
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	pool := openTestBillingPool(t, dsn)
	origDB := db.DB
	db.DB = pool
	t.Cleanup(func() { db.DB = origDB })

	keyBytes := make([]byte, 32)
	if _, err := rand.Read(keyBytes); err != nil {
		t.Fatal(err)
	}
	t.Setenv(credentials.EnvPaymentCredentialKeys, "v1:"+base64.StdEncoding.EncodeToString(keyBytes))
	t.Setenv(credentials.EnvPaymentCredentialActiveKeyVersion, "v1")
	origCfg := config.AppConfig
	config.AppConfig = &config.Config{JWTSecret: "test-billing-jwt-secret"}
	t.Cleanup(func() { config.AppConfig = origCfg })

	if _, err := pool.Exec(ctx, `
		INSERT INTO roles (code, name, description, is_system, is_super_admin, status)
		VALUES ('ProductReadOnly', 'Product Read Only', 'Test role with products:read only', false, false, 1)
		ON CONFLICT (code) DO NOTHING;
		INSERT INTO role_permissions (role_id, permission_code)
		SELECT id, 'payments.products:read' FROM roles WHERE code = 'ProductReadOnly'
		ON CONFLICT DO NOTHING;
		INSERT INTO admins(id, username, password_hash, role, role_id, status) VALUES
		('cfg-super', 'cfg-super', 'none', 'Admin', (SELECT id FROM roles WHERE code='Admin'), 1),
		('cfg-finance', 'cfg-finance', 'none', 'FinanceManager', (SELECT id FROM roles WHERE code='FinanceManager'), 1),
		('cfg-support', 'cfg-support', 'none', 'ProductReadOnly', (SELECT id FROM roles WHERE code='ProductReadOnly'), 1)
		ON CONFLICT (id) DO NOTHING`); err != nil {
		t.Fatal(err)
	}

	h := NewHandler(NewBillingService(NewBillingRepository()))
	r := gin.New()
	h.RegisterRoutes(r.Group("/cms"))
	return r, ctx
}

func actorRole(actor string) string {
	switch actor {
	case "cfg-super":
		return "Admin"
	case "cfg-finance":
		return "FinanceManager"
	default:
		return "ProductReadOnly"
	}
}

func doJSON(t *testing.T, r http.Handler, method, path, actor string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	tok, err := auth.GenerateToken(actor, actorRole(actor))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestChannelLifecycleEncryptionValidationAndPrimarySwitch(t *testing.T) {
	r, ctx := setupChannelCatalogRouter(t)

	// Mock PayPal and Stripe verification APIs cleanly outside DB locks.
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case req.URL.Path == "/v1/oauth2/token":
			_, _ = w.Write([]byte(`{"access_token":"tok_paypal_test"}`))
		case req.URL.Path == "/v1/notifications/webhooks/WH_SBX_01":
			_, _ = w.Write([]byte(`{"id":"WH_SBX_01","url":"https://example.test/webhook"}`))
		case req.URL.Path == "/v1/account":
			auth := req.Header.Get("Authorization")
			if strings.Contains(auth, "acct2") {
				_, _ = w.Write([]byte(`{"id":"acct_StripeTest02"}`))
			} else {
				_, _ = w.Write([]byte(`{"id":"acct_StripeTest01"}`))
			}
		case req.URL.Path == "/v1/webhook_endpoints":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":{"type":"permission_error","code":"rak_insufficient_permissions"}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer mockServer.Close()

	origPP, origST := paypalAPIBaseSandbox, stripeAPIBaseURL
	paypalAPIBaseSandbox, stripeAPIBaseURL = mockServer.URL, mockServer.URL
	defer func() { paypalAPIBaseSandbox, stripeAPIBaseURL = origPP, origST }()

	// Finance role must NOT create or manage channel credentials
	if rec := doJSON(t, r, http.MethodPost, "/cms/payments/channels", "cfg-finance", map[string]any{
		"provider": "stripe", "environment": "sandbox", "display_name": "Stripe 1",
		"account_key": "acct_StripeTest01", "api_key": "sk_test_secret_1234", "webhook_secret": "whsec_secret_1111",
	}); rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for finance channel creation, got %d: %s", rec.Code, rec.Body.String())
	}

	// SuperAdmin creates PayPal & Stripe sandbox accounts
	ppRec := doJSON(t, r, http.MethodPost, "/cms/payments/channels", "cfg-super", map[string]any{
		"provider": "paypal", "environment": "sandbox", "display_name": "PayPal Sandbox A",
		"account_key": "paypal_client_sbx_a", "client_secret": "pp_secret_never_leak", "webhook_id": "WH_SBX_01",
	})
	if ppRec.Code != http.StatusCreated {
		t.Fatalf("create PayPal channel failed (%d): %s", ppRec.Code, ppRec.Body.String())
	}
	var ppCreated struct {
		ID          int64  `json:"id"`
		WebhookPath string `json:"webhook_path"`
	}
	_ = json.Unmarshal(ppRec.Body.Bytes(), &ppCreated)
	if !strings.HasPrefix(ppCreated.WebhookPath, "/api/v1/wallet/webhook/channel/") {
		t.Fatalf("unexpected webhook_path: %q", ppCreated.WebhookPath)
	}

	stRec := doJSON(t, r, http.MethodPost, "/cms/payments/channels", "cfg-super", map[string]any{
		"provider": "stripe", "environment": "sandbox", "display_name": "Stripe Sandbox 1",
		"account_key": "acct_StripeTest01", "api_key": "sk_test_acct1_secret_9876", "webhook_secret": "whsec_acct1_old",
	})
	if stRec.Code != http.StatusCreated {
		t.Fatalf("create Stripe channel failed (%d): %s", stRec.Code, stRec.Body.String())
	}
	var stCreated struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(stRec.Body.Bytes(), &stCreated)

	// Cannot publish unverified channel
	if rec := doJSON(t, r, http.MethodPost, fmt.Sprintf("/cms/payments/channels/%d/publish", stCreated.ID), "cfg-super", map[string]any{}); rec.Code != http.StatusConflict {
		t.Fatalf("expected unverified publish rejection (409), got %d: %s", rec.Code, rec.Body.String())
	}

	// Validate both PayPal and Stripe channels
	if rec := doJSON(t, r, http.MethodPost, fmt.Sprintf("/cms/payments/channels/%d/validate", ppCreated.ID), "cfg-super", nil); rec.Code != http.StatusOK {
		t.Fatalf("validate PayPal failed (%d): %s", rec.Code, rec.Body.String())
	}
	if rec := doJSON(t, r, http.MethodPost, fmt.Sprintf("/cms/payments/channels/%d/validate", stCreated.ID), "cfg-super", nil); rec.Code != http.StatusOK {
		t.Fatalf("validate Stripe failed (%d): %s", rec.Code, rec.Body.String())
	}

	// Publish both as primary
	if rec := doJSON(t, r, http.MethodPost, fmt.Sprintf("/cms/payments/channels/%d/publish", ppCreated.ID), "cfg-super", map[string]any{"set_primary": true}); rec.Code != http.StatusOK {
		t.Fatalf("publish PayPal failed (%d): %s", rec.Code, rec.Body.String())
	}
	if rec := doJSON(t, r, http.MethodPost, fmt.Sprintf("/cms/payments/channels/%d/publish", stCreated.ID), "cfg-super", map[string]any{"set_primary": true}); rec.Code != http.StatusOK {
		t.Fatalf("publish Stripe failed (%d): %s", rec.Code, rec.Body.String())
	}

	// Rotate webhook secret on Stripe -> enters pending_version=2, active_version stays 1 until validate+publish
	if rec := doJSON(t, r, http.MethodPatch, fmt.Sprintf("/cms/payments/channels/%d", stCreated.ID), "cfg-super", map[string]any{
		"display_name": "Stripe Sandbox 1 Rotated", "webhook_secret": "whsec_acct1_new", "rotate_webhook_grace_hours": 12,
	}); rec.Code != http.StatusOK {
		t.Fatalf("rotate Stripe failed (%d): %s", rec.Code, rec.Body.String())
	}
	if rec := doJSON(t, r, http.MethodPost, fmt.Sprintf("/cms/payments/channels/%d/validate", stCreated.ID), "cfg-super", nil); rec.Code != http.StatusOK {
		t.Fatalf("re-validate rotated Stripe failed (%d): %s", rec.Code, rec.Body.String())
	}
	if rec := doJSON(t, r, http.MethodPost, fmt.Sprintf("/cms/payments/channels/%d/publish", stCreated.ID), "cfg-super", map[string]any{"expected_version": 2}); rec.Code != http.StatusOK {
		t.Fatalf("publish rotated Stripe v2 failed (%d): %s", rec.Code, rec.Body.String())
	}

	// Verify ZERO plaintext secrets leak via GET /channels or DB audit logs
	listRec := doJSON(t, r, http.MethodGet, "/cms/payments/channels", "cfg-super", nil)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list channels failed: %d", listRec.Code)
	}
	body := listRec.Body.String()
	for _, secret := range []string{"pp_secret_never_leak", "sk_test_acct1_secret_9876", "whsec_acct1_old", "whsec_acct1_new"} {
		if strings.Contains(body, secret) {
			t.Fatalf("channel response leaked plaintext secret %q!", secret)
		}
	}
	var leakCount int
	if err := db.DB.QueryRow(ctx, `SELECT count(*) FROM admin_audit_logs WHERE after_data::text LIKE '%pp_secret_never_leak%' OR after_data::text LIKE '%whsec_acct1%'`).Scan(&leakCount); err != nil || leakCount != 0 {
		t.Fatalf("audit logs leaked secret count=%d err=%v", leakCount, err)
	}

	// Second Stripe account becomes primary -> atomically demotes first Stripe primary without violating unique index
	st2Rec := doJSON(t, r, http.MethodPost, "/cms/payments/channels", "cfg-super", map[string]any{
		"provider": "stripe", "environment": "sandbox", "display_name": "Stripe Sandbox 2",
		"account_key": "acct_StripeTest02", "api_key": "sk_test_acct2_secret_5555", "webhook_secret": "whsec_acct2_1111",
	})
	if st2Rec.Code != http.StatusCreated {
		t.Fatalf("create Stripe 2 failed: %s", st2Rec.Body.String())
	}
	var st2Created struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(st2Rec.Body.Bytes(), &st2Created)
	_ = doJSON(t, r, http.MethodPost, fmt.Sprintf("/cms/payments/channels/%d/validate", st2Created.ID), "cfg-super", nil)
	if rec := doJSON(t, r, http.MethodPost, fmt.Sprintf("/cms/payments/channels/%d/publish", st2Created.ID), "cfg-super", map[string]any{"set_primary": true}); rec.Code != http.StatusOK {
		t.Fatalf("primary switch failed (%d): %s", rec.Code, rec.Body.String())
	}
	var primaryCount int
	if err := db.DB.QueryRow(ctx, `SELECT count(*) FROM payment_channel_accounts WHERE provider='stripe' AND environment='sandbox' AND is_primary`).Scan(&primaryCount); err != nil || primaryCount != 1 {
		t.Fatalf("expected exactly 1 primary stripe sandbox account, got %d err=%v", primaryCount, err)
	}

	// Deactivate strips primary & order switches cleanly
	if rec := doJSON(t, r, http.MethodPost, fmt.Sprintf("/cms/payments/channels/%d/deactivate", st2Created.ID), "cfg-super", nil); rec.Code != http.StatusOK {
		t.Fatalf("deactivate failed (%d): %s", rec.Code, rec.Body.String())
	}
	var status string
	var isPrimary, ordersOK bool
	if err := db.DB.QueryRow(ctx, `SELECT status,is_primary,new_orders_enabled FROM payment_channel_accounts WHERE id=$1`, st2Created.ID).Scan(&status, &isPrimary, &ordersOK); err != nil {
		t.Fatal(err)
	}
	if status != "disabled" || isPrimary || ordersOK {
		t.Fatalf("unexpected deactivated state: status=%s primary=%v orders=%v", status, isPrimary, ordersOK)
	}

	// Delete rules:
	// 1. Finance role without payments.channels:write cannot delete
	if rec := doJSON(t, r, http.MethodDelete, fmt.Sprintf("/cms/payments/channels/%d", st2Created.ID), "cfg-finance", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for finance channel delete, got %d: %s", rec.Code, rec.Body.String())
	}
	// 2. Published account cannot be deleted
	if rec := doJSON(t, r, http.MethodDelete, fmt.Sprintf("/cms/payments/channels/%d", ppCreated.ID), "cfg-super", nil); rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 when deleting published channel, got %d: %s", rec.Code, rec.Body.String())
	}
	// 3. Verified (not yet published) account cannot be deleted
	verifiedRec := doJSON(t, r, http.MethodPost, "/cms/payments/channels", "cfg-super", map[string]any{
		"provider": "paypal", "environment": "sandbox", "display_name": "PayPal Verified Temp",
		"account_key": "paypal_client_verified_tmp", "client_secret": "pp_secret_tmp", "webhook_id": "WH_SBX_01",
	})
	if verifiedRec.Code != http.StatusCreated {
		t.Fatalf("create verified temp failed (%d): %s", verifiedRec.Code, verifiedRec.Body.String())
	}
	var verifiedCreated struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(verifiedRec.Body.Bytes(), &verifiedCreated)
	if rec := doJSON(t, r, http.MethodPost, fmt.Sprintf("/cms/payments/channels/%d/validate", verifiedCreated.ID), "cfg-super", nil); rec.Code != http.StatusOK {
		t.Fatalf("validate temp failed (%d): %s", rec.Code, rec.Body.String())
	}
	if rec := doJSON(t, r, http.MethodDelete, fmt.Sprintf("/cms/payments/channels/%d", verifiedCreated.ID), "cfg-super", nil); rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 when deleting verified channel, got %d: %s", rec.Code, rec.Body.String())
	}
	// 4. Draft account can be deleted
	draftRec := doJSON(t, r, http.MethodPost, "/cms/payments/channels", "cfg-super", map[string]any{
		"provider": "paypal", "environment": "sandbox", "display_name": "PayPal Draft Temp",
		"account_key": "paypal_client_draft_tmp", "client_secret": "pp_secret_draft_never_leak", "webhook_id": "WH_SBX_01",
	})
	if draftRec.Code != http.StatusCreated {
		t.Fatalf("create draft temp failed (%d): %s", draftRec.Code, draftRec.Body.String())
	}
	var draftCreated struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(draftRec.Body.Bytes(), &draftCreated)
	if rec := doJSON(t, r, http.MethodDelete, fmt.Sprintf("/cms/payments/channels/%d", draftCreated.ID), "cfg-super", nil); rec.Code != http.StatusOK {
		t.Fatalf("delete draft channel failed (%d): %s", rec.Code, rec.Body.String())
	}
	// 5. Disabled account with 1-to-1 aftercare policy & ledger sync setting (and no historical records) can be deleted
	if _, err := db.DB.Exec(ctx, `INSERT INTO payment_aftercare_policies(account_id, dispute_lookup, hold_fulfillment) VALUES ($1, true, false)`, st2Created.ID); err != nil {
		t.Fatalf("seed aftercare policy failed: %v", err)
	}
	if _, err := db.DB.Exec(ctx, `INSERT INTO payment_ledger_sync_settings(account_id, enabled, actor_id, reason, window_start, window_end) VALUES ($1, false, 'cfg-super', 'test', now() - interval '1 hour', now())`, st2Created.ID); err != nil {
		t.Fatalf("seed ledger sync setting failed: %v", err)
	}
	if rec := doJSON(t, r, http.MethodDelete, fmt.Sprintf("/cms/payments/channels/%d", st2Created.ID), "cfg-super", nil); rec.Code != http.StatusOK {
		t.Fatalf("delete disabled channel failed (%d): %s", rec.Code, rec.Body.String())
	}
	// 6. Deleting again returns 404
	if rec := doJSON(t, r, http.MethodDelete, fmt.Sprintf("/cms/payments/channels/%d", st2Created.ID), "cfg-super", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 when deleting already-deleted channel, got %d: %s", rec.Code, rec.Body.String())
	}
	// 7. Disabled account referenced by historical payment_event_inbox returns 409 and preserves its aftercare policy (tx rollback)
	if rec := doJSON(t, r, http.MethodPost, fmt.Sprintf("/cms/payments/channels/%d/deactivate", verifiedCreated.ID), "cfg-super", nil); rec.Code != http.StatusOK {
		t.Fatalf("deactivate verified temp failed (%d): %s", rec.Code, rec.Body.String())
	}
	if _, err := db.DB.Exec(ctx, `INSERT INTO payment_aftercare_policies(account_id, dispute_lookup, hold_fulfillment) VALUES ($1, true, false)`, verifiedCreated.ID); err != nil {
		t.Fatalf("seed referenced aftercare policy failed: %v", err)
	}
	if _, err := db.DB.Exec(ctx, `INSERT INTO payment_event_inbox(provider, account_id, event_id, event_type, amount_cents, currency, event) VALUES ('paypal', $1, 'EVT-REF-TEST-01', 'PAYMENT.CAPTURE.COMPLETED', 999, 'USD', '{}'::jsonb)`, verifiedCreated.ID); err != nil {
		t.Fatalf("seed referenced payment event failed: %v", err)
	}
	if rec := doJSON(t, r, http.MethodDelete, fmt.Sprintf("/cms/payments/channels/%d", verifiedCreated.ID), "cfg-super", nil); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), errChannelReferenced.Error()) {
		t.Fatalf("expected 409 referenced error, got %d: %s", rec.Code, rec.Body.String())
	}
	var policyStillExists bool
	if err := db.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM payment_aftercare_policies WHERE account_id=$1)`, verifiedCreated.ID).Scan(&policyStillExists); err != nil || !policyStillExists {
		t.Fatalf("expected aftercare policy preserved after FK rollback, exists=%v err=%v", policyStillExists, err)
	}
	// 8. Disabled account referenced by product_channel_bindings returns 409 errChannelHasBindings
	if _, err := db.DB.Exec(ctx, `DELETE FROM payment_event_inbox WHERE account_id = $1`, verifiedCreated.ID); err != nil {
		t.Fatalf("cleanup payment_event_inbox failed: %v", err)
	}
	if _, err := db.DB.Exec(ctx, `INSERT INTO product_channel_bindings(product_id, account_id, provider, environment, external_product_ref, external_price_ref) SELECT id, $1, 'paypal', 'sandbox', 'PROD-1', 'P-1' FROM products LIMIT 1`, verifiedCreated.ID); err != nil {
		t.Fatalf("seed binding fixture failed: %v", err)
	}
	if rec := doJSON(t, r, http.MethodDelete, fmt.Sprintf("/cms/payments/channels/%d", verifiedCreated.ID), "cfg-super", nil); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), errChannelHasBindings.Error()) {
		t.Fatalf("expected 409 binding error, got %d: %s", rec.Code, rec.Body.String())
	}
	// Verify audit log recorded delete without leaking secrets
	var delAuditCount int
	if err := db.DB.QueryRow(ctx, `SELECT count(*) FROM admin_audit_logs WHERE action='delete_payment_channel' AND target_id IN ($1,$2)`,
		fmt.Sprintf("%d", draftCreated.ID), fmt.Sprintf("%d", st2Created.ID)).Scan(&delAuditCount); err != nil || delAuditCount != 2 {
		t.Fatalf("expected 2 delete_payment_channel audit logs, got %d err=%v", delAuditCount, err)
	}
}

func TestProductCatalogDualChannelGateAndTemplateDecoupling(t *testing.T) {
	r, ctx := setupChannelCatalogRouter(t)

	stripeProdCreates := 0
	stripePriceCreates := 0
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case req.URL.Path == "/v1/oauth2/token":
			_, _ = w.Write([]byte(`{"access_token":"tok_pp_cat"}`))
		case req.URL.Path == "/v1/notifications/webhooks/WH_CAT_01":
			_, _ = w.Write([]byte(`{"id":"WH_CAT_01","url":"https://example.test/pp"}`))
		case req.URL.Path == "/v1/catalogs/products":
			_, _ = w.Write([]byte(`{"id":"PROD-PP-ANNUAL-01"}`))
		case req.URL.Path == "/v1/billing/plans":
			_, _ = w.Write([]byte(`{"id":"P-ANNUAL-9999"}`))
		case req.URL.Path == "/v1/billing/plans/P-ANNUAL-9999":
			_, _ = w.Write([]byte(`{"id":"P-ANNUAL-9999","product_id":"PROD-PP-ANNUAL-01","status":"ACTIVE","billing_cycles":[{"tenure_type":"REGULAR","total_cycles":0,"frequency":{"interval_unit":"YEAR","interval_count":1},"pricing_scheme":{"fixed_price":{"value":"99.99","currency_code":"USD"}}}]}`))
		case req.URL.Path == "/v1/account":
			_, _ = w.Write([]byte(`{"id":"acct_StripeCat01"}`))
		case req.Method == http.MethodGet && req.URL.Path == "/v1/webhook_endpoints":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":{"type":"permission_error","code":"rak_insufficient_permissions"}}`))
		case req.Method == http.MethodPost && req.URL.Path == "/v1/products":
			if req.Header.Get("Idempotency-Key") == "" || req.ParseForm() != nil || req.PostForm.Get("name") == "" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			stripeProdCreates++
			_, _ = w.Write([]byte(`{"id":"prod_AnnualVIP","active":true,"livemode":false}`))
		case req.Method == http.MethodPost && req.URL.Path == "/v1/prices":
			if req.Header.Get("Idempotency-Key") == "" || req.ParseForm() != nil ||
				req.PostForm.Get("product") != "prod_AnnualVIP" || req.PostForm.Get("currency") != "usd" ||
				req.PostForm.Get("unit_amount") != "9999" || req.PostForm.Get("recurring[interval]") != "year" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			stripePriceCreates++
			_, _ = w.Write([]byte(`{"id":"price_AnnualVIP9999","active":true,"livemode":false}`))
		case req.Method == http.MethodGet && req.URL.Path == "/v1/prices/price_AnnualVIP9999":
			_, _ = w.Write([]byte(`{"id":"price_AnnualVIP9999","currency":"usd","type":"recurring","tax_behavior":"exclusive","unit_amount":9999,"active":true,"livemode":false,"recurring":{"interval":"year","interval_count":1},"product":{"id":"prod_AnnualVIP","active":true,"livemode":false,"tax_code":"txcd_10103001"}}`))
		case req.URL.Path == "/v1/prices/price_Coin499":
			_, _ = w.Write([]byte(`{"id":"price_Coin499","currency":"usd","type":"one_time","tax_behavior":"exclusive","unit_amount":499,"active":true,"livemode":false,"product":{"id":"prod_Coin499","active":true,"livemode":false,"tax_code":"txcd_10103001"}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer mockServer.Close()

	origPP, origST := paypalAPIBaseSandbox, stripeAPIBaseURL
	paypalAPIBaseSandbox, stripeAPIBaseURL = mockServer.URL, mockServer.URL
	defer func() { paypalAPIBaseSandbox, stripeAPIBaseURL = origPP, origST }()

	// Setup active primary PayPal and Stripe sandbox accounts
	var ppID, stID int64
	_ = json.Unmarshal(doJSON(t, r, http.MethodPost, "/cms/payments/channels", "cfg-super", map[string]any{
		"provider": "paypal", "environment": "sandbox", "display_name": "PP Cat",
		"account_key": "pp_cat_client", "client_secret": "pp_cat_secret", "webhook_id": "WH_CAT_01",
	}).Body.Bytes(), &struct {
		ID *int64 `json:"id"`
	}{&ppID})
	_ = doJSON(t, r, http.MethodPost, fmt.Sprintf("/cms/payments/channels/%d/validate", ppID), "cfg-super", nil)
	_ = doJSON(t, r, http.MethodPost, fmt.Sprintf("/cms/payments/channels/%d/publish", ppID), "cfg-super", map[string]any{"set_primary": true})

	_ = json.Unmarshal(doJSON(t, r, http.MethodPost, "/cms/payments/channels", "cfg-super", map[string]any{
		"provider": "stripe", "environment": "sandbox", "display_name": "ST Cat",
		"account_key": "acct_StripeCat01", "api_key": "sk_test_cat_1234", "webhook_secret": "whsec_cat_1234",
	}).Body.Bytes(), &struct {
		ID *int64 `json:"id"`
	}{&stID})
	_ = doJSON(t, r, http.MethodPost, fmt.Sprintf("/cms/payments/channels/%d/validate", stID), "cfg-super", nil)
	_ = doJSON(t, r, http.MethodPost, fmt.Sprintf("/cms/payments/channels/%d/publish", stID), "cfg-super", map[string]any{"set_primary": true})

	// 1. Product rule validation: VIP & Whole Book reject bonus > 0
	if rec := doJSON(t, r, http.MethodPost, "/cms/products", "cfg-finance", map[string]any{
		"product_type": "whole_book", "name": "Bad Book", "price_cents": 999, "bonus": 50, "novel_scope_type": "all_published",
	}); rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for whole_book bonus, got %d", rec.Code)
	}
	if rec := doJSON(t, r, http.MethodPost, "/cms/products", "cfg-finance", map[string]any{
		"product_type": "vip", "name": "Bad VIP", "price_cents": 999, "bonus": 100, "vip_cycle": "month",
	}); rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for vip bonus, got %d", rec.Code)
	}

	// 2. Create annual VIP product (year) + coin product
	var vipID int64
	vipRec := doJSON(t, r, http.MethodPost, "/cms/products", "cfg-finance", map[string]any{
		"code": "CAT_VIP_YEAR_9999", "product_type": "vip", "name": "VIP Annual Plan",
		"description": "Unlimited annual reading", "price_cents": 9999, "vip_cycle": "year",
	})
	if vipRec.Code != http.StatusCreated {
		t.Fatalf("create VIP annual failed (%d): %s", vipRec.Code, vipRec.Body.String())
	}
	_ = json.Unmarshal(vipRec.Body.Bytes(), &struct {
		ID *int64 `json:"id"`
	}{&vipID})

	// Database immutability trigger forbids mutating commercial terms
	if _, err := db.DB.Exec(ctx, `UPDATE products SET price_cents = 1 WHERE id = $1`, vipID); err == nil {
		t.Fatal("expected products UPDATE trigger to fail")
	}

	// Copy creates a new immutable version
	var copyID int64
	copyRec := doJSON(t, r, http.MethodPost, fmt.Sprintf("/cms/products/%d/copy", vipID), "cfg-finance", map[string]any{
		"code": "CAT_VIP_YEAR_9999_V2", "name": "VIP Annual Plan V2",
	})
	if copyRec.Code != http.StatusCreated {
		t.Fatalf("copy product failed (%d): %s", copyRec.Code, copyRec.Body.String())
	}
	_ = json.Unmarshal(copyRec.Body.Bytes(), &struct {
		ID *int64 `json:"id"`
	}{&copyID})

	// 3. Dual-channel publish gate: PayPal alone must NOT allow publish
	for _, provider := range []string{"paypal", "stripe"} {
		rec := doJSON(t, r, http.MethodPut, fmt.Sprintf("/cms/products/%d/bindings/%s", vipID, provider), "cfg-finance", map[string]any{"environment": "sandbox"})
		if rec.Code != http.StatusForbidden {
			t.Fatalf("finance binding %s: %d %s", provider, rec.Code, rec.Body.String())
		}
	}
	if rec := doJSON(t, r, http.MethodPost, fmt.Sprintf("/cms/products/%d/publish", vipID), "cfg-finance", map[string]any{
		"environment": "live", "reason": "unauthorized live publication",
	}); rec.Code != http.StatusForbidden {
		t.Fatalf("finance live publish: %d %s", rec.Code, rec.Body.String())
	}
	if rec := doJSON(t, r, http.MethodPost, fmt.Sprintf("/cms/products/%d/publish", vipID), "cfg-super", map[string]any{
		"environment": "live",
	}); rec.Code != http.StatusBadRequest {
		t.Fatalf("live publish without reason: %d %s", rec.Code, rec.Body.String())
	}
	if rec := doJSON(t, r, http.MethodPut, fmt.Sprintf("/cms/products/%d/bindings/paypal", vipID), "cfg-super", map[string]any{
		"environment": "sandbox", "reason": "provision annual plan",
	}); rec.Code != http.StatusOK {
		t.Fatalf("PayPal annual VIP binding failed (%d): %s", rec.Code, rec.Body.String())
	}
	if rec := doJSON(t, r, http.MethodPost, fmt.Sprintf("/cms/products/%d/publish", vipID), "cfg-finance", map[string]any{
		"environment": "sandbox",
	}); rec.Code != http.StatusConflict {
		t.Fatalf("expected publish blocked when Stripe missing (409), got %d: %s", rec.Code, rec.Body.String())
	}

	// Template must reject unpublished product & reject commercial override
	if rec := doJSON(t, r, http.MethodPost, "/cms/recharge-templates", "cfg-finance", map[string]any{
		"name": "Bad Commercial Override",
		"slots": []map[string]any{
			{"slot_index": 1, "product_id": vipID, "price_cents": 199},
			{"slot_index": 2, "product_id": vipID},
			{"slot_index": 3, "product_id": vipID},
			{"slot_index": 4, "product_id": vipID},
		},
	}); rec.Code != http.StatusBadRequest {
		t.Fatalf("expected commercial override rejection (400), got %d", rec.Code)
	}
	if rec := doJSON(t, r, http.MethodPost, "/cms/recharge-templates", "cfg-finance", map[string]any{
		"name": "Unpublished Ref",
		"slots": []map[string]any{
			{"slot_index": 1, "product_id": vipID},
			{"slot_index": 2, "product_id": copyID},
			{"slot_index": 3, "product_id": 1},
			{"slot_index": 4, "product_id": 2},
		},
	}); rec.Code != http.StatusConflict {
		t.Fatalf("expected unpublished product rejection (409), got %d: %s", rec.Code, rec.Body.String())
	}

	// Manual product_ref / price_ref binding must be rejected (auto-provisioning only).
	if rec := doJSON(t, r, http.MethodPut, fmt.Sprintf("/cms/products/%d/bindings/stripe", vipID), "cfg-super", map[string]any{
		"environment": "sandbox", "product_ref": "prod_AnnualVIP", "price_ref": "price_AnnualVIP9999",
		"sales_mode": "direct", "countries": []string{"US", "GB"}, "reason": "manual ref should fail",
	}); rec.Code != http.StatusBadRequest {
		t.Fatalf("expected manual Stripe ref rejection (400), got %d: %s", rec.Code, rec.Body.String())
	}

	// Auto-provision Stripe Product + Price to complete dual-channel readiness & publish annual VIP
	if rec := doJSON(t, r, http.MethodPut, fmt.Sprintf("/cms/products/%d/bindings/stripe", vipID), "cfg-super", map[string]any{
		"environment": "sandbox",
		"sales_mode":  "direct", "countries": []string{"US", "GB"}, "reason": "auto provision annual stripe",
	}); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"external_price_ref":"price_AnnualVIP9999"`) {
		t.Fatalf("Stripe annual VIP binding failed (%d): %s", rec.Code, rec.Body.String())
	}
	if stripeProdCreates != 1 || stripePriceCreates != 1 {
		t.Fatalf("expected 1 product and 1 price auto-created, got prod=%d price=%d", stripeProdCreates, stripePriceCreates)
	}
	// Updating countries reuses the provisioned Stripe Product/Price without re-creating
	if rec := doJSON(t, r, http.MethodPut, fmt.Sprintf("/cms/products/%d/bindings/stripe", vipID), "cfg-super", map[string]any{
		"environment": "sandbox",
		"sales_mode":  "direct", "countries": []string{"US", "GB", "CA"}, "reason": "expand countries",
	}); rec.Code != http.StatusOK {
		t.Fatalf("Stripe binding update failed (%d): %s", rec.Code, rec.Body.String())
	}
	if stripeProdCreates != 1 || stripePriceCreates != 1 {
		t.Fatalf("expected reuse of provisioned Stripe refs without re-create, got prod=%d price=%d", stripeProdCreates, stripePriceCreates)
	}
	if rec := doJSON(t, r, http.MethodPost, fmt.Sprintf("/cms/products/%d/publish", vipID), "cfg-finance", map[string]any{
		"environment": "sandbox",
	}); rec.Code != http.StatusOK {
		t.Fatalf("expected dual-channel ready publish success (200), got %d: %s", rec.Code, rec.Body.String())
	}
	// ProductReadOnly (lacking payments.channels:read) must NOT see binding details; FinanceManager (has payments.channels:read) DOES see them
	if rec := doJSON(t, r, http.MethodGet, fmt.Sprintf("/cms/products/%d?environment=sandbox", vipID), "cfg-support", nil); rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "price_AnnualVIP9999") || !strings.Contains(rec.Body.String(), `"dual_channel_ready":true`) {
		t.Fatalf("summary projection for cfg-support: %d %s", rec.Code, rec.Body.String())
	}
	if rec := doJSON(t, r, http.MethodGet, fmt.Sprintf("/cms/products/%d?environment=sandbox", vipID), "cfg-finance", nil); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "price_AnnualVIP9999") || !strings.Contains(rec.Body.String(), `"dual_channel_ready":true`) {
		t.Fatalf("channel-read projection for cfg-finance: %d %s", rec.Code, rec.Body.String())
	}
	policyPath := fmt.Sprintf("/cms/payments/channels/%d/aftercare-policy", ppID)
	draft := map[string]any{"reason": "test review policy", "revision": "", "hold_fulfillment": true,
		"reviewer_id": "cfg-super", "finance_id": "cfg-finance", "review_seconds": 600, "refund_seconds": 1200}
	// FinanceManager has payments.channels:read (GET 200) but NOT payments.channels:write (PUT 403)
	if rec := doJSON(t, r, http.MethodGet, policyPath, "cfg-finance", nil); rec.Code != http.StatusOK {
		t.Fatalf("finance policy GET: %d %s", rec.Code, rec.Body.String())
	}
	if rec := doJSON(t, r, http.MethodPut, policyPath, "cfg-finance", draft); rec.Code != http.StatusForbidden {
		t.Fatalf("finance policy PUT: %d %s", rec.Code, rec.Body.String())
	}
	readPolicy := func() struct {
		Configured    bool `json:"configured"`
		ReviewSeconds int  `json:"review_seconds"`
	} {
		rec := doJSON(t, r, http.MethodGet, policyPath, "cfg-super", nil)
		var policy struct {
			Configured    bool `json:"configured"`
			ReviewSeconds int  `json:"review_seconds"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &policy); rec.Code != http.StatusOK || err != nil {
			t.Fatalf("read policy: %d %s %v", rec.Code, rec.Body.String(), err)
		}
		return policy
	}
	if readPolicy().Configured {
		t.Fatal("new account policy must be unconfigured")
	}
	if rec := doJSON(t, r, http.MethodPut, policyPath, "cfg-super", draft); rec.Code != http.StatusOK {
		t.Fatalf("policy save: %d %s", rec.Code, rec.Body.String())
	}
	draft["review_seconds"] = 60
	if rec := doJSON(t, r, http.MethodPut, policyPath, "cfg-super", draft); rec.Code != http.StatusConflict {
		t.Fatalf("stale policy edit: %d %s", rec.Code, rec.Body.String())
	}
	if policy := readPolicy(); !policy.Configured || policy.ReviewSeconds != 600 {
		t.Fatalf("saved policy projection: %+v", policy)
	}
}
