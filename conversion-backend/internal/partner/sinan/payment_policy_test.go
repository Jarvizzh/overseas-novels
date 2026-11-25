package sinan

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"conversion-backend/internal/delivery"
	"conversion-backend/internal/paymentpolicy"

	"github.com/jackc/pgx/v5"
)

func TestSinanPaymentPolicyIdentityAndFinalReports(t *testing.T) {
	f := newFixture(t)
	operator := f.paymentOperator(t)
	send, skip := int64(1), int64(1)
	if _, err := f.api.PublishPaymentPolicy(f.ctx, operator, 1, paymentpolicy.Draft{
		App: "1", Reason: "fixture cycle", Operations: []paymentpolicy.Operation{
			{Type: "set_fallback", Action: &paymentpolicy.Action{Mode: paymentpolicy.Cycle, SendCount: &send, SkipCount: &skip}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	selected := f.callbackBody("selected-first")
	f.postPayment(t, selected)
	f.normalize(t, 1)
	f.check(t, "default::selected:1", `
		SELECT scope_kind||':'||promotion_key||':'||outcome||':'||cycle_sequence
		FROM conversion.payment_policy_decisions WHERE app_scope='1' AND payment_key='demo_order_0001'`)

	duplicate := strings.Replace(selected, "selected-first", "same-payment", 1)
	f.postPayment(t, duplicate)
	f.postPayment(t, duplicate)
	for _, tc := range []struct{ id, old, new string }{
		{"amount-conflict", `"10.00"`, `"12.00"`},
		{"currency-conflict", `"usd"`, `"eur"`},
		{"kind-conflict", `"recharge_type":1`, `"recharge_type":2`},
		{"promo-conflict", `"app_id":1`, `"app_id":1,"promotion_id":56`},
		{"pixel-conflict", `"pixel_id":123`, `"pixel_id":456`},
	} {
		changed := strings.Replace(strings.Replace(selected, "selected-first", tc.id, 1), tc.old, tc.new, 1)
		f.postPayment(t, changed)
	}

	otherApp := strings.Replace(f.callbackBody("other-app"), `"app_id":1`, `"app_id":2`, 1)
	otherApp = strings.Replace(otherApp, `"pixel_id":123`, `"pixel_id":789`, 1)
	f.postPayment(t, otherApp)
	f.normalize(t, 7)
	f.check(t, "1:5:1", `
		SELECT count(*) FILTER (WHERE b.reason='duplicate_payment_event')||':'||
		       count(*) FILTER (WHERE b.reason='payment_policy_fact_conflict')||':'||count(DISTINCT e.id)
		FROM conversion.inbox b JOIN conversion.events e ON e.id=b.normalized_event_id
		WHERE b.source_app_id='1' AND b.state='consumed'`)
	f.check(t, "1:1,2:1", `
		SELECT string_agg(app_scope||':'||frequency,',' ORDER BY app_scope)
		FROM (SELECT app_scope,count(*) frequency FROM conversion.payment_policy_decisions
		      WHERE payment_key='demo_order_0001' GROUP BY app_scope) counted`)
	f.check(t, "3", "SELECT count(*)::text FROM conversion.deliveries")

	f.postPayment(t, strings.Replace(f.callbackBody("skipped-second"), "demo_order_0001", "demo_order_0002", 1))
	f.normalize(t, 1)
	f.check(t, "default:skipped:2", `
		SELECT scope_kind||':'||outcome||':'||cycle_sequence FROM conversion.payment_policy_decisions
		WHERE payment_key='demo_order_0002'`)
	f.check(t, "2:2:0", `
		SELECT count(*)||':'||count(*) FILTER(WHERE d.state='skipped')||':'||sum(d.attempts)
		FROM conversion.deliveries d JOIN conversion.events e ON e.id=d.event_id WHERE e.source_event_id='skipped-second'`)
	f.check(t, "0:blocked:missing_promotion_contract_unconfirmed:123", `
		SELECT r.report_status||':'||r.state||':'||r.reason||':'||(r.payload->>'pixel_id')
		FROM conversion.partner_reports r JOIN conversion.events e ON e.id=r.event_id WHERE e.source_event_id='skipped-second'`)
	skipReport := f.reportID(t, "skipped-second")
	if _, err := f.api.RetryPartnerReport(f.ctx, operator, skipReport, "unconfirmed empty promotion"); !errors.Is(err, delivery.ErrOperationConflict) {
		t.Fatalf("retry without an agreed encoding: %v", err)
	}
	if _, err := f.report.Claim(f.ctx); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("unconfirmed empty promotion was claimable: %v", err)
	}

	wire := make(chan []byte, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		wire <- body
		_, _ = io.WriteString(w, `{"code":0}`)
	}))
	defer server.Close()
	f.configureReport(t, server.URL+"/final", false, "")
	exec(t, f.ctx, f.owner, `UPDATE conversion.integrations SET report_config=report_config||'{"missing_promotion_encoding":"null"}'`)
	if _, err := f.api.RetryPartnerReport(f.ctx, operator, skipReport, "mock accepts null promotion"); err != nil {
		t.Fatal(err)
	}
	reporter := f.reporter(server.Client())
	f.processReport(t, reporter)
	checkPaymentWire(t, wire, "skipped-second", "0")
	f.check(t, "0:confirmed:1", "SELECT report_status||':'||state||':'||attempts FROM conversion.partner_reports WHERE id=$1", skipReport)

	// Lease the selected original and record transport uncertainty. The
	// additional target may expire too but must not create a second report.
	job := f.completeMeta(t, delivery.Result{Outcome: delivery.OutcomeUnknown, Reason: "fixture_timeout"})
	f.check(t, "selected-first:original", `
		SELECT e.source_event_id||':'||d.role FROM conversion.deliveries d JOIN conversion.events e ON e.id=d.event_id WHERE d.id=$1`, job.ID)
	exec(t, f.ctx, f.owner, `
		UPDATE conversion.deliveries d SET deadline_at=now()
		FROM conversion.events e WHERE e.id=d.event_id AND e.source_event_id='selected-first'`)
	if n, err := f.svc.Sweep(f.ctx, 10, time.Second); n != 2 || err != nil {
		t.Fatalf("expired %d: %v", n, err)
	}
	expiredReport := f.reportID(t, "selected-first")
	f.check(t, "-1:blocked:no_trace_contract_unconfirmed:meta_no_confirmation", `
		SELECT report_status||':'||state||':'||reason||':'||(payload->>'reason') FROM conversion.partner_reports WHERE id=$1`, expiredReport)
	f.configureReport(t, server.URL+"/final", false, "omit")
	if _, err := f.api.RetryPartnerReport(f.ctx, operator, expiredReport, "mock accepts no trace"); err != nil {
		t.Fatal(err)
	}
	f.processReport(t, reporter)
	checkPaymentWire(t, wire, "selected-first", "-1")
	f.check(t, "-1:confirmed:1", "SELECT report_status||':'||state||':'||attempts FROM conversion.partner_reports WHERE id=$1", expiredReport)
	f.check(t, "2", "SELECT count(*)::text FROM conversion.partner_reports")
}

func TestSinanPaymentDedupAndRouteRecoveryUnderConcurrentWorkers(t *testing.T) {
	f := newFixture(t)
	operator := f.paymentOperator(t)
	rate := 0
	if _, err := f.api.PublishPaymentPolicy(f.ctx, operator, 1, paymentpolicy.Draft{
		App: "1", Reason: "fixture skip", Operations: []paymentpolicy.Operation{
			{Type: "set_fallback", Action: &paymentpolicy.Action{Mode: paymentpolicy.Ratio, RateBPS: &rate}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	exec(t, f.ctx, f.owner, "UPDATE conversion.destinations SET enabled=false WHERE id=1")
	for _, id := range []string{"receipt-a", "receipt-b"} {
		f.postPayment(t, f.callbackBody(id))
	}
	var workers sync.WaitGroup
	for range 2 {
		workers.Go(func() {
			if _, err := Consume(f.ctx, f.svc, 1); err != nil {
				t.Error(err)
			}
		})
	}
	workers.Wait()
	f.check(t, "1:0:1", `
		SELECT (SELECT count(*) FROM conversion.events)||':'||
		       (SELECT count(*) FROM conversion.payment_policy_decisions)||':'||
		       (SELECT count(*) FROM conversion.inbox WHERE reason='duplicate_payment_event')`)
	f.check(t, "2:1", `SELECT count(*)||':'||count(DISTINCT normalized_event_id)
	    FROM conversion.inbox WHERE source_event_id IN ('receipt-a','receipt-b')
	      AND processed_at IS NOT NULL AND processed_at>=received_at`)
	f.check(t, "2", "SELECT count(*)::text FROM conversion.deliveries WHERE state='blocked'")
	var original int64
	if err := f.owner.QueryRow(f.ctx, "SELECT id FROM conversion.deliveries WHERE role='original'").Scan(&original); err != nil {
		t.Fatal(err)
	}
	if _, err := f.api.RetryDelivery(f.ctx, operator, original, 0, "original still disabled", false); !errors.Is(err, delivery.ErrOperationConflict) {
		t.Fatalf("unauthorized retry: %v", err)
	}
	exec(t, f.ctx, f.owner, "UPDATE conversion.destinations SET enabled=true WHERE id=1")
	if result, err := f.api.RetryDelivery(f.ctx, operator, original, 0, "original restored", false); err != nil || result.NextAttemptAt != nil {
		t.Fatalf("retry policy result: %+v, %v", result, err)
	}
	f.check(t, "1:2:1", `
		SELECT (SELECT count(*) FROM conversion.payment_policy_decisions WHERE reason='rule_ratio_skipped')||':'||
		       (SELECT count(*) FROM conversion.deliveries WHERE state='skipped')||':'||
		       (SELECT count(*) FROM conversion.partner_reports WHERE original_delivery_id=$1 AND report_status=0)`, original)
	f.check(t, "true", `
		SELECT ((r.payload->>'event_id')=e.source_event_id AND (r.payload->>'pixel_id')='123')::text
		FROM conversion.partner_reports r JOIN conversion.events e ON e.id=r.event_id`)

	badAmount := strings.Replace(f.callbackBody("invalid-amount"), "demo_order_0001", "invalid-payment", 1)
	f.postPayment(t, strings.Replace(badAmount, `"10.00"`, `"10.123"`, 1))
	f.normalize(t, 1)
	f.check(t, "0:failed:missing_purchase_amount:-1", `
		SELECT (SELECT count(*) FROM conversion.payment_policy_decisions WHERE payment_key='invalid-payment')||':'||
		       d.state||':'||d.reason||':'||r.report_status
		FROM conversion.events e JOIN conversion.deliveries d ON d.event_id=e.id
		JOIN conversion.partner_reports r ON r.original_delivery_id=d.id WHERE e.source_event_id='invalid-amount'`)
}

func TestSinanPaymentMigrationPreservesHistoricIdentity(t *testing.T) {
	f := newFixtureFrom(t, "2026111911", func(f fixture) {
		exec(t, f.ctx, f.owner, `
			INSERT INTO conversion.events(integration_id,environment,source_event_type,source_event_id,event_name,source_order_id,
			    attribution_state,occurred_at,received_at,amount,currency,order_type,partner_fields)
			VALUES(1,'test','recharge-paid','historical-event','Purchase','demo_order_0001','matched',now(),now(),10,'USD','1',
			       '{"app_id":"1","pixel_id":"123"}');
			INSERT INTO conversion.inbox(integration_id,source_event_type,source_event_id,source_order_id,payload_digest,state)
			VALUES(1,'recharge-paid','historical-event','demo_order_0001','fixture','consumed'),
			      (1,'recharge-paid','historical-event','demo_order_0001','conflicting-fixture','quarantined');
		`)
	})
	f.check(t, "1", "SELECT source_app_id FROM conversion.inbox WHERE source_event_id='historical-event' AND state='consumed'")
	f.check(t, "true", "SELECT (source_app_id IS NULL)::text FROM conversion.inbox WHERE source_event_id='historical-event' AND state='quarantined'")
	f.postPayment(t, f.callbackBody("historical-alias"))
	f.normalize(t, 1)
	f.check(t, "historical-event:duplicate_payment_event:historical", `
		SELECT e.source_event_id||':'||b.reason||':'||e.payment_policy_state
		FROM conversion.inbox b JOIN conversion.events e ON e.id=b.normalized_event_id WHERE b.source_event_id='historical-alias'`)
	f.check(t, "0", "SELECT count(*)::text FROM conversion.payment_policy_decisions")
}

func (f fixture) paymentOperator(t *testing.T) delivery.Operator {
	t.Helper()
	exec(t, f.ctx, f.owner, "INSERT INTO admins(id,username,password_hash,role,role_id) VALUES('policy-ops','policy-ops','fixture','Admin',(SELECT id FROM roles WHERE code='Admin'))")
	return delivery.Operator{ID: "policy-ops", Role: "Admin", Permission: delivery.PermissionAdsConversionsWrite}
}

func (f fixture) postPayment(t *testing.T, body string) {
	t.Helper()
	if w := post(f.inbox.Handler(), "publisher", "recharge-paid", fixtureToken, body); w.Code != http.StatusOK {
		t.Fatalf("payment callback: %d %s", w.Code, w.Body.String())
	}
}

func (f fixture) normalize(t *testing.T, want int) {
	t.Helper()
	if n, err := Consume(f.ctx, f.svc, 20); n != want || err != nil {
		t.Fatalf("normalized %d, want %d: %v", n, want, err)
	}
}

func (f fixture) reportID(t *testing.T, eventID string) int64 {
	t.Helper()
	var id int64
	err := f.owner.QueryRow(f.ctx, `
		SELECT r.id FROM conversion.partner_reports r JOIN conversion.events e ON e.id=r.event_id WHERE e.source_event_id=$1`, eventID).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func checkPaymentWire(t *testing.T, wire <-chan []byte, eventID, status string) {
	t.Helper()
	select {
	case body := <-wire:
		var payload map[string]json.RawMessage
		if err := json.Unmarshal(body, &payload); err != nil || string(payload["promotion_id"]) != "null" ||
			string(payload["event_id"]) != `"`+eventID+`"` || string(payload["pixel_id"]) != `"123"` ||
			string(payload["report_status"]) != status || payload["fbtrace_id"] != nil {
			t.Fatalf("payment report: %s, %v", body, err)
		}
	default:
		t.Fatal("Mock did not receive a report")
	}
}
