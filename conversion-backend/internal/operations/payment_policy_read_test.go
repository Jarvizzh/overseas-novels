package operations

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"conversion-backend/internal/delivery"
	"conversion-backend/internal/paymentpolicy"

	"github.com/jackc/pgx/v5"
)

func TestPaymentPolicyReconciliationAndMetrics(t *testing.T) {
	ctx, pool := operationsTestPool(t)
	mustExec(t, ctx, pool, `
		INSERT INTO admins(id,username,password_hash,role,role_id) VALUES('ops','ops','fixture','Admin',(SELECT id FROM roles WHERE code='Admin'));
		INSERT INTO users(id) VALUES(1);
		INSERT INTO recharge_orders(id,user_id,amount_cents,payment_method,status,fulfilled_at,subscription_id)
		VALUES (900,1,5000,'paypal','Success',now(),'I-METRICS'),(901,1,5000,'paypal','Success',now(),NULL);
		INSERT INTO user_subscriptions(user_id,subscription_id,capi_subscribe_state,capi_subscribe_order_id)
		VALUES(1,'I-METRICS','claimed',900);
		INSERT INTO conversion.integrations(environment,source,account_key,contract_version)
		VALUES('test','starnovel','reader','fixture');
		INSERT INTO conversion.destinations(environment,media,pixel_id) VALUES ('test','meta','123'),('test','meta','456');
	`)
	service := delivery.Service{Store: delivery.Store{Pool: pool, Environment: "test"}}
	scope := paymentpolicy.Scope{Environment: "test", IntegrationID: 1, App: paymentpolicy.ReaderApp, Promotion: "80"}
	send, skip, min, max := int64(1), int64(1), int64(3000), int64(9000)
	published, err := service.PublishPaymentPolicy(ctx, delivery.Operator{ID: "ops", Role: "Admin"}, 1, paymentpolicy.Draft{
		App: paymentpolicy.ReaderApp, Reason: "fixture scope",
		Operations: []paymentpolicy.Operation{{Type: "add_band", Currency: "USD", Bands: []paymentpolicy.BandInput{{
			Min: &min, Max: &max, Action: paymentpolicy.Action{Mode: paymentpolicy.Cycle, SendCount: &send, SkipCount: &skip},
		}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"900", "901"} {
		err = pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
			amount := int64(5000)
			decision, err := paymentpolicy.DecideTx(ctx, tx, paymentpolicy.Facts{
				Scope: scope, PaymentKey: key, Destination: 1, Kind: "subscription_initial", AmountMinor: &amount, Currency: "USD",
			})
			if err != nil {
				return err
			}
			_, err = tx.Exec(ctx, `
				INSERT INTO conversion.events(integration_id,environment,source_event_type,source_event_id,event_name,
				    source_order_id,promotion_id,attribution_state,received_at,occurred_at,
				    payment_app_scope,payment_policy_state,payment_policy_decision_id)
				VALUES (1,'test','Purchase','payment:'||$1||':Purchase','Purchase',$1,'80','matched',now(),now(),'reader','decided',$2)`,
				key, decision.ID)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	mustExec(t, ctx, pool, `
		INSERT INTO conversion.deliveries(event_id,destination_id,environment,meta_event_id,role,deadline_at,state,finalized_at)
		SELECT id,1,'test','original-confirmed','original',now(),'succeeded',now() FROM conversion.events
		WHERE source_event_id='payment:900:Purchase';
		UPDATE recharge_orders o SET capi_expectation=jsonb_build_object('environment','test','payment_key',o.id::text,
		    'occurred_at',now(),'events',jsonb_build_array(jsonb_build_object('name','Purchase','business_key','payment:'||o.id||':Purchase'))
		    || CASE WHEN o.id=900 THEN '[{"name":"Subscribe","business_key":"subscription:I-METRICS:Subscribe"}]'::jsonb ELSE '[]'::jsonb END)
		WHERE o.id IN (900,901);
		INSERT INTO conversion.events(integration_id,environment,source_event_type,source_event_id,event_name,
		    source_order_id,attribution_state,received_at,reason,payment_app_scope,payment_policy_state)
		VALUES (1,'test','Purchase','missing-evidence','Purchase','902','matched',now(),'rule_fact_missing','reader','pending');
	`)
	// The selected event retains the old matched interval after the live
	// configuration changes.
	if _, err := service.PublishPaymentPolicy(ctx, delivery.Operator{ID: "ops", Role: "Admin"}, 1, paymentpolicy.Draft{
		App: paymentpolicy.ReaderApp, ExpectedVersion: &published.OwnVersion.ID, Reason: "replace current rules",
		Operations: []paymentpolicy.Operation{{Type: "remove_band", BranchID: published.Config.Groups[0].Bands[0].ID}},
	}); err != nil {
		t.Fatal(err)
	}

	repo := Repository{Pool: pool, Environment: "test"}
	handler := NewHandler(repo, "test-token")
	get := func(path string) string {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/internal/conversion"+path, nil)
		req.Header.Set("Authorization", "Bearer test-token")
		req.Header.Set("X-Operator-ID", "reader")
		req.Header.Set("X-Operator-Role", "FinanceManager")
		req.Header.Set("X-Operator-Access", "read")
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != 200 {
			t.Fatalf("%s returned %d: %s", path, res.Code, res.Body)
		}
		return res.Body.String()
	}
	for _, path := range []string{"/orders/900", "/events/1"} {
		var detail struct {
			Policy struct {
				Version string `json:"version_number"`
				Source  string `json:"config_source"`
				Seq     string `json:"cycle_sequence"`
				Match   struct {
					Min string `json:"min_minor"`
					Max string `json:"max_minor"`
				} `json:"match"`
			} `json:"payment_policy"`
		}
		if err := json.Unmarshal([]byte(get(path)), &detail); err != nil ||
			detail.Policy.Version != "1" || detail.Policy.Source != "inherited" || detail.Policy.Seq != "1" ||
			detail.Policy.Match.Min != "3000" || detail.Policy.Match.Max != "9000" {
			t.Fatalf("immutable decision evidence: %+v, %v", detail, err)
		}
	}
	for _, tc := range []struct {
		query                 string
		evaluated, selected   int
		confirmationRowCount  int
		originalConfirmations int
	}{
		{"", 2, 1, 2, 1},
		{"?event_name=Subscribe", 1, 1, 1, 0},
		{"?destination_id=2", 0, 0, 0, 0},
	} {
		var data struct {
			Coverage     struct{ Evaluated, Selected int }   `json:"payment_coverage"`
			Confirmation []struct{ Expected, Confirmed int } `json:"selected_confirmation"`
		}
		if err := json.Unmarshal([]byte(get("/summary"+tc.query)), &data); err != nil ||
			data.Coverage.Evaluated != tc.evaluated || data.Coverage.Selected != tc.selected ||
			len(data.Confirmation) != tc.confirmationRowCount {
			t.Fatalf("payment metrics %s: %+v, %v", tc.query, data, err)
		}
		confirmed := 0
		for _, row := range data.Confirmation {
			if row.Expected != 1 {
				t.Fatalf("expected original-target event once: %+v", row)
			}
			confirmed += row.Confirmed
		}
		if confirmed != tc.originalConfirmations {
			t.Fatalf("original confirmation count: %d", confirmed)
		}
	}
	type populationRow struct {
		Event       string `json:"event_name"`
		Eligibility string
		Events      int
		Confirmed   int
		NoTarget    int `json:"missing_target"`
		NoOutbox    int `json:"missing_outbox"`
		NoTime      int `json:"missing_time"`
	}
	type summary struct {
		Population   []populationRow
		Coverage     struct{ Evaluated, Selected int }   `json:"payment_coverage"`
		Confirmation []struct{ Expected, Confirmed int } `json:"selected_confirmation"`
	}
	population := func(path string, want ...populationRow) summary {
		t.Helper()
		var data summary
		if err := json.Unmarshal([]byte(get("/summary"+path)), &data); err != nil || len(data.Population) != len(want) {
			t.Fatalf("population %s: %+v, %v", path, data.Population, err)
		}
		for _, row := range want {
			if !slices.Contains(data.Population, row) {
				t.Fatalf("missing %+v in %s: %+v", row, path, data.Population)
			}
		}
		return data
	}
	confirmed := populationRow{Event: "Purchase", Eligibility: "expected", Events: 1, Confirmed: 1}
	skipped := populationRow{Event: "Purchase", Eligibility: "policy_skipped", Events: 1}
	population("?source=starnovel", confirmed, skipped,
		populationRow{Event: "Subscribe", Eligibility: "expected", Events: 1, NoOutbox: 1})
	const subscribeFilter = "?source=starnovel&event_name=Subscribe"
	assertSubscribeMetrics := func(data summary, confirmed int) {
		t.Helper()
		if data.Coverage.Evaluated != 1 || data.Coverage.Selected != 1 || len(data.Confirmation) != 1 ||
			data.Confirmation[0].Expected != 1 || data.Confirmation[0].Confirmed != confirmed {
			t.Fatalf("Subscribe metrics want original confirmations=%d: %+v", confirmed, data)
		}
	}
	assertSubscribeMetrics(population(subscribeFilter,
		populationRow{Event: "Subscribe", Eligibility: "expected", Events: 1, NoOutbox: 1}), 0)
	assertSubscribeMetrics(population(subscribeFilter+"&destination_id=1"), 0)
	mustExec(t, ctx, pool, `
		INSERT INTO conversion.events(integration_id,environment,source_event_type,source_event_id,event_name,
		    source_order_id,promotion_id,attribution_state,received_at,occurred_at,
		    payment_app_scope,payment_policy_state,payment_policy_decision_id)
		SELECT 1,'test','Subscribe','subscription:I-METRICS:Subscribe','Subscribe','900','80','matched',now(),now(),'reader','decided',id
		FROM conversion.payment_policy_decisions WHERE integration_id=1 AND payment_key='900';
		INSERT INTO conversion.deliveries(event_id,destination_id,environment,meta_event_id,role,deadline_at,state,finalized_at)
		SELECT id,2,'test','additional-confirmed','additional',now(),'succeeded',now() FROM conversion.events
		WHERE source_event_id='subscription:I-METRICS:Subscribe';
	`)
	assertSubscribeMetrics(population(subscribeFilter,
		populationRow{Event: "Subscribe", Eligibility: "expected", Events: 1, NoTarget: 1}), 0)
	mustExec(t, ctx, pool, `
		INSERT INTO conversion.deliveries(event_id,destination_id,environment,meta_event_id,role,deadline_at,state,finalized_at)
		SELECT id,1,'test','subscribe-confirmed','original',now(),'succeeded',now() FROM conversion.events
		WHERE source_event_id='subscription:I-METRICS:Subscribe';
	`)
	assertSubscribeMetrics(population(subscribeFilter,
		populationRow{Event: "Subscribe", Eligibility: "expected", Events: 1, Confirmed: 1}), 1)
	mustExec(t, ctx, pool, `
		UPDATE recharge_orders SET fulfilled_at=now()-interval '48 hours',
		    capi_expectation=jsonb_set(capi_expectation,'{occurred_at}',to_jsonb(now()-interval '48 hours')) WHERE id=900;
	`)
	assertSubscribeMetrics(population(subscribeFilter), 1)

	mustExec(t, ctx, pool, `
		INSERT INTO conversion.integrations(environment,source,account_key,contract_version)
		VALUES ('test','sinan','partner','fixture'),('test','sinan','only-skipped','fixture');
		INSERT INTO conversion.payment_policy_decisions(environment,integration_id,app_scope,payment_key,scope_kind,
		    promotion_key,original_destination_id,payment_kind,outcome,reason,decided_at)
		VALUES ('test',2,'10','1','default','',1,'single','selected','rule_send_all',now()),
		       ('test',2,'10','2','default','',1,'single','skipped','rule_ratio_skipped',now()),
		       ('test',3,'10','3','default','',1,'single','skipped','rule_ratio_skipped',now());
		INSERT INTO conversion.events(integration_id,environment,source_event_type,source_event_id,event_name,
		    source_order_id,attribution_state,received_at,occurred_at,payment_app_scope,payment_policy_state,payment_policy_decision_id)
		SELECT integration_id,'test','recharge-paid',payment_key,'Purchase',payment_key,'matched',now(),now(),app_scope,'decided',id
		FROM conversion.payment_policy_decisions WHERE integration_id IN (2,3);
		INSERT INTO conversion.deliveries(event_id,destination_id,environment,meta_event_id,role,deadline_at,state,finalized_at)
		SELECT id,1,'test','partner-confirmed','original',now(),'succeeded',now() FROM conversion.events
		WHERE integration_id=2 AND source_event_id='1';
	`)
	data := population("?source=sinan&integration_id=2", confirmed, skipped)
	if data.Coverage.Evaluated != 2 || data.Coverage.Selected != 1 || len(data.Confirmation) != 1 ||
		data.Confirmation[0].Expected != 1 || data.Confirmation[0].Confirmed != 1 {
		t.Fatalf("Sinan coverage and confirmation: %+v", data)
	}
	data = population("?source=sinan&integration_id=3", skipped)
	if data.Coverage.Evaluated != 1 || data.Coverage.Selected != 0 || len(data.Confirmation) != 0 {
		t.Fatalf("all-skipped coverage and confirmation: %+v", data)
	}
	if _, err := repo.Collect(ctx); err != nil {
		t.Fatal(err)
	}
	if body := get("/summary"); !strings.Contains(body, `"kind": "rule_fact_missing"`) &&
		!strings.Contains(body, `"kind":"rule_fact_missing"`) {
		t.Fatalf("missing rule fact alert: %s", body)
	}
}
