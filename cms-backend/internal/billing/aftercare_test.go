package billing

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func testAftercareRoutes(t *testing.T, service BillingService, exec func(string, ...any)) {
	t.Helper()
	exec(`INSERT INTO payment_cases(id,account_id,kind,external_id,receipt_refs,provider_status,is_open,observed_at)
		OVERRIDING SYSTEM VALUE VALUES (5101,901,'dispute','du_CMS',ARRAY['ch_fin03']::text[],'lost',false,now());`)
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("admin_id", c.GetHeader("X-Test-Actor")); c.Next() })
	NewHandler(service).registerAftercareRoutes(router.Group("/payments"), paymentRoles("SuperAdmin", "Finance"))
	check := func(actor, method, path, body string, status int) string {
		t.Helper()
		req := httptest.NewRequest(method, "/payments/cases"+path, strings.NewReader(body))
		req.Header.Set("X-Test-Actor", actor)
		req.Header.Set("Content-Type", "application/json")
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		if res.Code != status {
			t.Fatalf("%s %s got %d: %s", method, path, res.Code, res.Body.String())
		}
		return res.Body.String()
	}
	for _, path := range []string{"?view=disputes", "/5101", "/owners", "/decisions"} {
		check("test-support", http.MethodGet, path, "", http.StatusForbidden)
	}
	found := check("test-finance", http.MethodGet, "?view=disputes&environment=live&order_id=5000000001", "", http.StatusOK)
	if !strings.Contains(found, "du_CMS") {
		t.Fatalf("order did not link to dispute: %s", found)
	}
	exec(`INSERT INTO payment_cases(account_id,kind,external_id,provider_status,is_open,observed_at)
		SELECT 902,'risk_review','review_CMS_'||n,'open',true,now() FROM generate_series(1,21) n`)
	var page struct {
		Items      []json.RawMessage `json:"items"`
		NextCursor *string           `json:"next_cursor"`
	}
	const riskPath = "?view=risk&kind=risk_review&environment=sandbox&provider=stripe&account_id=902"
	found = check("test-finance", http.MethodGet, riskPath, "", http.StatusOK)
	if err := json.Unmarshal([]byte(found), &page); err != nil || len(page.Items) != 20 || page.NextCursor == nil {
		t.Fatalf("first risk page: %s, %v", found, err)
	}
	found = check("test-finance", http.MethodGet, riskPath+"&before="+*page.NextCursor, "", http.StatusOK)
	if err := json.Unmarshal([]byte(found), &page); err != nil || len(page.Items) != 1 || page.NextCursor != nil {
		t.Fatalf("last risk page: %s, %v", found, err)
	}
	const signalPath = "?view=risk&kind=risk_signal&environment=sandbox&provider=stripe&account_id=902"
	for _, scenario := range []struct {
		setup string
		state string
	}{
		{"", "unconfigured"},
		{"UPDATE payment_channel_accounts SET status='verified' WHERE id=902", "awaiting_signals"},
		{`INSERT INTO payment_cases(account_id,kind,external_id,provider_status,is_open,observed_at)
			VALUES (902,'risk_signal','observed_mock_signal','observed',false,now())`, "collected"},
	} {
		if scenario.setup != "" {
			exec(scenario.setup)
		}
		body := check("test-finance", http.MethodGet, signalPath+"&external_id=missing", "", http.StatusOK)
		var empty struct {
			Items       []json.RawMessage `json:"items"`
			SignalState string            `json:"signal_state"`
		}
		if err := json.Unmarshal([]byte(body), &empty); err != nil || len(empty.Items) != 0 || empty.SignalState != scenario.state {
			t.Fatalf("signal state %s: %s, %v", scenario.state, body, err)
		}
	}
	exec("UPDATE payment_channel_accounts SET status='draft' WHERE id=902")
	found = check("test-finance", http.MethodGet, "/5101", "", http.StatusOK)
	if !strings.Contains(found, "ch_fin03") || strings.Contains(found, "private-fin03") {
		t.Fatalf("case detail projection: %s", found)
	}
	body := fmt.Sprintf(`{"request_id":"50000000-0000-4000-8000-000000000001","action":"assign","owner":"test-finance","reason":%q}`, "fixture")
	check("test-support", http.MethodPost, "/5101/actions", body, http.StatusForbidden)
	check("test-finance", http.MethodPost, "/5101/actions", body, http.StatusOK)
	check("test-finance", http.MethodPost, "/5101/actions", body, http.StatusOK)
	found = check("test-finance", http.MethodGet, "/5101", "", http.StatusOK)
	if strings.Count(found, `"action": "assign"`)+strings.Count(found, `"action":"assign"`) != 1 {
		t.Fatalf("case audit replay: %s", found)
	}

	exec(`INSERT INTO payment_cases(id,account_id,kind,external_id,receipt_refs,provider_status,is_open,work_state,work_due_at,observed_at)
		OVERRIDING SYSTEM VALUE VALUES
		(5102,901,'risk_review','needs_finance',ARRAY['ch_fin03'],'open',true,'pending',now()-interval '1 hour',now());
		INSERT INTO payment_deferred_fulfillment(order_id,receipt_id,case_id)
		SELECT order_id,id,5102 FROM third_party_payment_orders WHERE capture_id='ch_fin03';`)
	var detail struct {
		AvailableActions []string `json:"available_actions"`
		Receipts         []struct {
			EvidenceKey string `json:"evidence_key"`
			Action      string `json:"entitlement_action"`
			Result      *struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			} `json:"entitlement_result"`
		} `json:"receipts"`
	}
	loadDetail := func(path string) {
		t.Helper()
		found := check("test-finance", http.MethodGet, path, "", http.StatusOK)
		detail.AvailableActions = nil
		detail.Receipts = nil
		if err := json.Unmarshal([]byte(found), &detail); err != nil {
			t.Fatalf("action eligibility payload: %s, %v", found, err)
		}
	}
	loadDetail("/5102")
	if !slices.Equal(detail.AvailableActions, []string{"assign", "approve"}) {
		t.Fatalf("approval with no open dispute and no refund policy: %+v", detail.AvailableActions)
	}
	exec(`UPDATE payment_cases SET is_open=true,due_at=now()+interval '5 hours' WHERE id=5101;
		INSERT INTO payment_aftercare_policies(account_id,finance_id,refund_seconds)
			VALUES(901,'test-finance',600);`)
	loadDetail("/5102")
	if !slices.Equal(detail.AvailableActions, []string{"assign", "reject"}) {
		t.Fatalf("open dispute blocks approval: %+v", detail.AvailableActions)
	}
	if found := check("test-finance", http.MethodGet, "?view=risk&work_state=pending&deadline=overdue&account_id=901", "", http.StatusOK); !strings.Contains(found, "needs_finance") {
		t.Fatalf("overdue review: %s", found)
	}
	if found := check("test-finance", http.MethodGet, "?view=disputes&deadline=upcoming&account_id=901", "", http.StatusOK); !strings.Contains(found, "du_CMS") {
		t.Fatalf("upcoming dispute: %s", found)
	}
	check("test-finance", http.MethodPost, "/5102/actions", `{"request_id":"50000000-0000-4000-8000-000000000002","action":"approve","reason":"open dispute"}`, http.StatusConflict)
	check("test-finance", http.MethodPost, "/5102/actions", `{"request_id":"50000000-0000-4000-8000-000000000003","action":"reject","reason":"route to finance"}`, http.StatusOK)
	loadDetail("/5102")
	if !slices.Equal(detail.AvailableActions, []string{"assign"}) {
		t.Fatalf("review referred to dispute: %+v", detail.AvailableActions)
	}

	exec(`UPDATE payment_cases SET is_open=false,outcome='lost' WHERE id=5101;
		UPDATE recharge_orders SET fulfilled_at=now() WHERE id=5000000001;
		INSERT INTO payment_fulfillment_funding(order_id,receipt_id,principal_minor,currency)
			SELECT order_id,id,1000,'USD' FROM third_party_payment_orders WHERE capture_id='ch_fin03';
		INSERT INTO payment_ledger_references(movement_id,object_type,object_id) VALUES
			(104,'stripe_charge','ch_fin03'),(104,'stripe_dispute','du_CMS');
		INSERT INTO payment_ledger_evidence(movement_id,source,source_ref,payload_hash,payload)
			VALUES (104,'stripe_balance_transaction','txn_chargeback',decode(repeat('aa',32),'hex'),'{}');
		INSERT INTO payment_ledger_amounts(movement_id,component,currency,amount_minor,evidence_id)
			SELECT 104,'gross','USD',-1000,id FROM payment_ledger_evidence WHERE movement_id=104;`)
	loadDetail("/5101")
	if len(detail.Receipts) != 1 || detail.Receipts[0].Action != "apply_loss" || detail.Receipts[0].EvidenceKey == "" {
		t.Fatalf("eligible entitlement adjustment: %+v", detail.Receipts)
	}
	const adjustmentID = "50000000-0000-4000-8000-000000000004"
	check("test-finance", http.MethodPost, "/5101/actions", fmt.Sprintf(
		`{"request_id":%q,"action":"apply_loss","reason":"verified loss","evidence_key":%q}`,
		adjustmentID, detail.Receipts[0].EvidenceKey), http.StatusOK)
	loadDetail("/5101")
	pending := detail.Receipts[0]
	if pending.Action != "" || pending.Result == nil || pending.Result.ID != adjustmentID || pending.Result.Status != "pending" {
		t.Fatalf("pending entitlement result: %+v", pending)
	}
	exec(`UPDATE payment_loss_commands SET status='stale',finished_at=now() WHERE id=$1`, adjustmentID)
	loadDetail("/5101")
	if detail.Receipts[0].Result == nil || detail.Receipts[0].Result.Status != "stale" || detail.Receipts[0].Action != "apply_loss" {
		t.Fatalf("stale command keeps the current eligible action: %+v", detail.Receipts[0])
	}
}
