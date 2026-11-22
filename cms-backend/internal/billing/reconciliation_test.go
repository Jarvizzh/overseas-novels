package billing

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func testPaymentReconciliation(t *testing.T, service BillingService, exec func(string, ...any)) {
	t.Helper()
	exec(`
		INSERT INTO payment_channel_accounts (id,provider,environment,account_key,credentials_ref,webhook_ref)
			OVERRIDING SYSTEM VALUE VALUES (901,'stripe','live','acct_test_stripe','env:stripe','env:stripe'),
			(902,'stripe','sandbox','acct_test_stripe','env:stripe','env:stripe'),
			(903,'paypal','live','paypal_client_fin03','env:paypal','env:paypal');
		INSERT INTO recharge_orders(id,user_id,external_ref_id,amount_cents,payment_method,status,order_type)
			VALUES (5000000001,501,'fin03-order',1000,'stripe','Success','single');
		INSERT INTO third_party_payment_orders(order_id,payment_provider,account_id,object_type,capture_id,currency,gross_amount,status)
			VALUES (5000000001,'stripe',901,'charge','ch_fin03','USD',10.00,'VERIFIED');
		UPDATE third_party_payment_orders SET account_id=903,object_type='capture' WHERE capture_id='refund-test-capture';

		INSERT INTO payment_ledger_movements(id,account_id,movement_key,kind,currency,posted_at,occurred_at,review_required)
			OVERRIDING SYSTEM VALUE VALUES
			(101,901,'txn_charge','payment','USD',now(),now(),false),
			(102,901,'txn_payout','payout','USD',now(),now(),false),
			(103,901,'txn_refund','refund','USD',now(),now(),false),
			(104,901,'txn_chargeback','dispute_debit','USD',now(),now(),false),
			(105,902,'txn_charge','payment','USD',now(),now(),false),
			(106,901,'txn_foreign','payment','EUR',now(),now(),false),
			(107,901,'txn_conflict','refund','USD',now(),now(),true),
			(108,903,'paypal-refund/T1107/USD','refund','USD',now(),now(),false);
		INSERT INTO payment_ledger_movements(account_id,movement_key,kind,currency,posted_at,occurred_at)
			SELECT 901,'txn_chargeback_'||n,'dispute_debit','USD',now(),now() FROM generate_series(1,20) n;
		INSERT INTO payment_ledger_references(movement_id,object_type,object_id) VALUES
			(101,'stripe_charge','ch_fin03'),(101,'stripe_payout_allocation','po_fin03'),
			(103,'stripe_refund','re_fin03'),(103,'stripe_charge','ch_fin03'),
			(105,'stripe_charge','ch_fin03'),(105,'stripe_payout_allocation','po_fin03'),
			(107,'stripe_refund','re_conflict'),(108,'paypal_refund','paypal-refund');
		INSERT INTO payment_ledger_evidence(movement_id,source,source_ref,payload_hash,payload)
			SELECT id,'stripe_balance_transaction',movement_key,decode(repeat('aa',32),'hex'),
				'{"internal":"private-fin03"}'::jsonb
			FROM payment_ledger_movements WHERE id IN (101,102,107);
		INSERT INTO payment_ledger_amounts(movement_id,component,currency,amount_minor,evidence_id)
			SELECT v.movement_id,v.component,v.currency,v.amount_minor,e.id
			FROM (VALUES (101,'gross','USD',1000),(101,'fee','USD',-50),(101,'net','USD',850),
				(101,'withheld_tax','USD',100),(101,'processing_fee','USD',50),(101,'sales_tax','EUR',0),
				(102,'gross','USD',-850),(107,'gross','USD',-500)) v(movement_id,component,currency,amount_minor)
			JOIN payment_ledger_evidence e ON e.movement_id=v.movement_id;
		INSERT INTO payment_settlement_payouts(id,account_id,external_ref,currency,expected_minor,bank_reference,channel_status,
			allocation_required,allocation_complete,transfer_key,last_observed_at)
			OVERRIDING SYSTEM VALUE VALUES
			(5000000002,901,'po_fin03','USD',850,'bank-ref-fin03','paid',true,true,'txn_payout',now()),
			(5000000003,901,'po_no_transfer','USD',1000,NULL,'paid',false,false,NULL,now());
		INSERT INTO payment_bank_statement_entries(environment,bank_account,transaction_id,statement_ref,booked_at,currency,
			amount_minor,bank_reference,operator,reason)
			VALUES ('live','bank-account','bank-fin03','statement-fin03',now(),'USD',850,'bank-ref-fin03','tester','fixture');
		INSERT INTO payment_settlement_bank_reviews(payout_id,entry_id,outcome,operator,reason)
			SELECT 5000000002,id,'matched','tester','fixture' FROM payment_bank_statement_entries WHERE transaction_id='bank-fin03';
		INSERT INTO payment_settlement_imports(account_id,source,external_ref,fingerprint,operator,reason)
			VALUES (901,'stripe_report','report_fin03',decode(repeat('ab',32),'hex'),'tester','fixture');
		INSERT INTO payment_settlement_rows(import_id,source_file,row_number,movement_id,payload,review_reason)
			SELECT id,'report.csv',2,NULL,'{"internal":"private-fin03"}','missing_reference'
			FROM payment_settlement_imports WHERE external_ref='report_fin03';
		INSERT INTO paypal_refund_items(refund_id,capture_id,currency,amount_cents)
			VALUES ('paypal-refund','refund-test-capture','USD',99),
				('paypal-partial-refund','refund-test-capture','USD',10);
	`)

	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("admin_id", c.GetHeader("X-Test-Actor")); c.Next() })
	NewHandler(service).registerReconciliationRoutes(router.Group("/payments"), paymentRoles("SuperAdmin", "Finance"))
	get := func(actor, path string, want int) string {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/payments/reconciliation/"+path, nil)
		req.Header.Set("X-Test-Actor", actor)
		out := httptest.NewRecorder()
		router.ServeHTTP(out, req)
		if out.Code != want || strings.Contains(out.Body.String(), "private-fin03") {
			t.Fatalf("%s %s returned %d: %s", actor, path, out.Code, out.Body.String())
		}
		return out.Body.String()
	}
	for _, path := range []string{"movements", "payouts", "report-issues", "activity"} {
		for _, actor := range []string{"test-editor", "test-support", "test-disabled"} {
			get(actor, path, http.StatusForbidden)
		}
	}
	get("test-super", "activity", http.StatusOK)
	get("test-finance", "movements?payout_id=-2", http.StatusBadRequest)
	get("test-finance", "payouts?environment=invalid", http.StatusBadRequest)
	get("test-finance", "report-issues?page=1000001", http.StatusBadRequest)
	var movements movementPage
	decode := func(path string, dest any) {
		t.Helper()
		if err := json.Unmarshal([]byte(get("test-finance", path, http.StatusOK)), dest); err != nil {
			t.Fatal(err)
		}
	}
	decode("movements?order_id=5000000001", &movements)
	if movements.Total != 2 || movements.Totals["withheld_tax"] != "100" || movements.Totals["processing_fee"] != "50" ||
		movements.Totals["sales_tax"] != "" {
		t.Fatalf("order-linked USD movements: %+v", movements)
	}
	movements = movementPage{}
	decode("movements?payout_id=5000000002", &movements)
	if movements.Total != 2 || len(movements.Items[0].Payouts) != 1 || movements.Items[0].Payouts[0].ID != "5000000002" {
		t.Fatalf("payout members: %+v", movements)
	}
	movements = movementPage{}
	decode("movements?review_required=true", &movements)
	if movements.Total != 1 || len(movements.Totals) != 0 || !movements.Items[0].ReviewRequired {
		t.Fatalf("review exclusion: %+v", movements)
	}
	movements = movementPage{}
	decode("movements?provider=stripe&page=2", &movements)
	if movements.Total != 25 || len(movements.Items) != 5 || movements.Totals["gross"] != "150" {
		t.Fatalf("all-pages totals: %+v", movements)
	}
	movements = movementPage{}
	decode("movements?environment=sandbox&provider=stripe", &movements)
	if movements.Total != 1 || len(movements.Items[0].OrderIDs) != 0 || len(movements.Items[0].Payouts) != 0 {
		t.Fatalf("sandbox scope: %+v", movements)
	}
	var payouts payoutPage
	decode("payouts?payout_id=5000000002", &payouts)
	if payouts.Total != 1 || payouts.Items[0].Status != "matched" ||
		payouts.Items[0].DifferenceMinor == nil || *payouts.Items[0].DifferenceMinor != "0" ||
		payouts.Items[0].BankTransactionID == nil || *payouts.Items[0].BankTransactionID != "bank-fin03" {
		t.Fatalf("matched bank entry: %+v", payouts)
	}
	payouts = payoutPage{}
	decode("payouts?differences_only=true", &payouts)
	if payouts.Total != 1 || payouts.Items[0].Status != "missing_transfer" || payouts.Items[0].BankAmountMinor != nil {
		t.Fatalf("payout exceptions: %+v", payouts)
	}
	var issues reportIssuePage
	decode("report-issues", &issues)
	if issues.Total != 1 || issues.Items[0].RowNumber != 2 || issues.Items[0].MovementID != nil {
		t.Fatalf("unresolved report rows: %+v", issues)
	}
	var activity paymentActivity
	decode("activity", &activity)
	if activity.RefundCount != 3 || activity.ChargebackDebitCount != 21 {
		t.Fatalf("full financial activity: %+v", activity)
	}
}
