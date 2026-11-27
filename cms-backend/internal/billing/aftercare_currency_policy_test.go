package billing

import (
	"encoding/json"
	"os"
	"testing"

	"star-novel-cms/internal/db"
)

func TestDisputeOriginalCurrencyAndAutoMitigationPolicy(t *testing.T) {
	dsn := os.Getenv("STAR_NOVEL_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires a disposable PostgreSQL database")
	}
	ctx := t.Context()
	pool := openTestBillingPool(t, dsn)
	origDB := db.DB
	db.DB = pool
	t.Cleanup(func() { db.DB = origDB })

	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.DB.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}

	exec(`
		INSERT INTO admins(id,username,password_hash,role,role_id,status)
			VALUES('policy-admin','policy-admin','test','Admin',(SELECT id FROM roles WHERE code='Admin'),1)
			ON CONFLICT (id) DO NOTHING;
		INSERT INTO payment_channel_accounts(id,provider,account_key,environment,status,credentials_ref,webhook_ref)
			OVERRIDING SYSTEM VALUE VALUES(7701,'stripe','acct_p24','sandbox','published','env:test','wh_test_7701');
		INSERT INTO payment_cases(id,account_id,kind,external_id,receipt_refs,provider_status,stage,reason,outcome,is_open,amount_minor,original_currency,original_amount_minor,observed_at)
			OVERRIDING SYSTEM VALUE VALUES
			(7801,7701,'dispute','du_gbp_cms',ARRAY['ch_gbp'],'needs_response','needs_response','fraudulent','',true,NULL,'GBP',1250,now()),
			(7802,7701,'dispute','du_jpy_cms',ARRAY['ch_jpy'],'needs_response','needs_response','fraudulent','',true,NULL,'JPY',1500,now()),
			(7803,7701,'dispute','du_usd_cms',ARRAY['ch_usd'],'needs_response','needs_response','fraudulent','',true,999,'USD',999,now()),
			(7804,7701,'dispute','du_legacy_cms',ARRAY['ch_leg'],'needs_response','needs_response','fraudulent','',true,NULL,NULL,NULL,now());
	`)

	raw, err := listCases(ctx, caseFilter{
		reconciliationScope: reconciliationScope{Environment: "sandbox", Provider: "stripe", AccountID: 7701},
		View:                "disputes",
		Kind:                "dispute",
	})
	if err != nil {
		t.Fatal(err)
	}
	var page struct {
		Items []struct {
			ExternalID          string  `json:"external_id"`
			AmountMinor         *string `json:"amount_minor"`
			OriginalCurrency    *string `json:"original_currency"`
			OriginalAmountMinor *string `json:"original_amount_minor"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatal(err)
	}
	byID := map[string]struct {
		usd  *string
		curr *string
		orig *string
	}{}
	for _, item := range page.Items {
		byID[item.ExternalID] = struct {
			usd  *string
			curr *string
			orig *string
		}{item.AmountMinor, item.OriginalCurrency, item.OriginalAmountMinor}
	}
	if got := byID["du_gbp_cms"]; got.curr == nil || *got.curr != "GBP" || got.orig == nil || *got.orig != "1250" || got.usd != nil {
		t.Fatalf("GBP case mismatch: %+v", got)
	}
	if got := byID["du_jpy_cms"]; got.curr == nil || *got.curr != "JPY" || got.orig == nil || *got.orig != "1500" || got.usd != nil {
		t.Fatalf("JPY case mismatch: %+v", got)
	}
	if got := byID["du_usd_cms"]; got.curr == nil || *got.curr != "USD" || got.orig == nil || *got.orig != "999" || got.usd == nil || *got.usd != "999" {
		t.Fatalf("USD case mismatch: %+v", got)
	}
	if got := byID["du_legacy_cms"]; got.curr != nil || got.orig != nil || got.usd != nil {
		t.Fatalf("legacy unknown case should preserve nil original currency/amount: %+v", got)
	}

	// Verify account-level mitigation toggles default to false and can be toggled independently.
	if err := saveAftercarePolicy(ctx, 7701, "policy-admin", aftercarePolicy{
		AutoRefund:             false,
		AutoCancelSubscription: true,
		DisputeFreeze:          false,
		Reason:                 "enable auto-cancel only",
	}); err != nil {
		t.Fatal(err)
	}
	var autoRefund, autoCancel, disputeFreeze bool
	if err := db.DB.QueryRow(ctx, `SELECT auto_refund,auto_cancel_subscription,dispute_freeze
		FROM payment_aftercare_policies WHERE account_id=7701`).Scan(&autoRefund, &autoCancel, &disputeFreeze); err != nil {
		t.Fatal(err)
	}
	if autoRefund || !autoCancel || disputeFreeze {
		t.Fatalf("independent toggle mismatch: autoRefund=%v autoCancel=%v disputeFreeze=%v", autoRefund, autoCancel, disputeFreeze)
	}
}
