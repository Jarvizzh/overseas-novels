package operations

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCollectorAndSummaryWithoutWorker(t *testing.T) {
	ctx, pool := operationsTestPool(t)
	repo := Repository{Pool: pool, Environment: "test"}
	mustExec(t, ctx, pool, `
		INSERT INTO conversion.integrations(environment,source,account_key,contract_version,enabled)
		VALUES ('test','starnovel','reader','v1',true);
		INSERT INTO users(id) VALUES (10);
		INSERT INTO recharge_orders(id,user_id,amount_cents,payment_method,status,fulfilled_at,capi_expectation)
		VALUES (10,10,999,'paypal','Success',now()-interval '2 minutes',
			jsonb_build_object('events',jsonb_build_array(jsonb_build_object('name','Purchase','business_key','payment:10:Purchase')),
			  'environment','test','occurred_at',now()-interval '2 minutes'));
		INSERT INTO conversion.destinations(environment,media,pixel_id) VALUES ('test','meta','123');
		INSERT INTO conversion.events(integration_id,environment,source_event_type,source_event_id,event_name,attribution_state,occurred_at,received_at)
		VALUES (1,'test','Registration','test-registration','CompleteRegistration','matched',now()-interval '7 minutes',now()-interval '7 minutes');
		INSERT INTO conversion.deliveries(event_id,destination_id,environment,meta_event_id,role,deadline_at,next_attempt_at)
		VALUES (1,1,'test','meta-fixture','original',now()+interval '1 hour',now()-interval '7 minutes');`)
	first, err := repo.Collect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]bool{}
	for _, a := range first {
		kinds[a.Kind] = true
	}
	for _, expected := range []string{"purchase_missing_outbox", "worker_heartbeat_missing", "meta_queue_delayed"} {
		if !kinds[expected] {
			t.Errorf("missing alert %s: %+v", expected, first)
		}
	}
	if changes, err := repo.Collect(ctx); err != nil || len(changes) != 0 {
		t.Fatalf("dedup: %+v %v", changes, err)
	}

	var sample json.RawMessage
	if err := pool.QueryRow(ctx, summarySQL, "test", 24*time.Hour, "", "", int64(0), int64(0)).Scan(&sample); err != nil {
		t.Fatal(err)
	}

	handler := NewHandler(repo, "fixture")
	for _, tt := range []struct{ route, body string }{
		{"/summary", `"missing_outbox":1`}, {"/events?limit=1", `test-registration`}, {"/events/1", `meta-fixture`},
	} {
		req := httptest.NewRequest(http.MethodGet, "/internal/conversion"+tt.route, nil)
		req.Header.Set("Authorization", "Bearer fixture")
		req.Header.Set("X-Operator-ID", "test-admin")
		req.Header.Set("X-Operator-Role", "Finance")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != 200 || !strings.Contains(w.Body.String(), tt.body) {
			t.Fatalf("%s: %d %s", tt.route, w.Code, w.Body.String())
		}
	}
	mustExec(t, ctx, pool, `
		INSERT INTO conversion.worker_heartbeats(environment,worker_type,instance_id,heartbeat_at,paused)
		VALUES ('test','meta','fixture',now(),true);`)
	changes, err := repo.Collect(ctx)
	if err != nil || len(changes) != 1 || changes[0].Kind != "worker_heartbeat_missing" || changes[0].State != "resolved" {
		t.Fatalf("pause: %+v %v", changes, err)
	}

	mustExec(t, ctx, pool, `
		INSERT INTO user_subscriptions(user_id,subscription_id) VALUES (10,'I-REVIEW');
		INSERT INTO recharge_orders(id,user_id,amount_cents,payment_method,status,order_type,subscription_id,fulfilled_at)
		VALUES(11,10,999,'paypal','Success','subscription','I-REVIEW',now()-interval '1 hour'),
		      (12,10,999,'paypal','Success','single',NULL,now()-interval '1 hour'),
		      (13,10,999,'paypal','Success','single',NULL,now()-interval '1 hour');
		UPDATE user_subscriptions SET capi_subscribe_state='claimed',capi_subscribe_order_id=11 WHERE subscription_id='I-REVIEW';
		INSERT INTO conversion.events(integration_id,environment,source_event_type,source_event_id,event_name,
			received_at,occurred_at,source_order_id,attribution_state,reason)
		SELECT 1,'test',name,key,name,now()-interval '1 hour',now()-interval '1 hour',ord,state,reason
		FROM (VALUES ('Purchase','payment:11:Purchase','11','matched','legacy_confirmed'),
		             ('Subscribe','subscription:I-REVIEW:Subscribe','11','matched','legacy_confirmed'),
		             ('Purchase','payment:12:Purchase','12','missing_pixel','original_target_unavailable'))
		     f(name,key,ord,state,reason);
		INSERT INTO conversion.legacy_outbox_imports(legacy_event_id,event_id,legacy_state,legacy_attempts,legacy_delivered_at,
			resolution,legacy_pixel_id,environment_evidence,legacy_next_attempt_at,legacy_had_lease)
		SELECT source_event_id,id,CASE WHEN source_order_id='11' THEN 'delivered' ELSE 'pending' END,1,
			CASE WHEN source_order_id='11' THEN occurred_at END,
			CASE WHEN source_order_id='11' THEN 'succeeded' ELSE 'blocked_without_target' END,'123','release fixture',now(),false
		FROM conversion.events WHERE source_order_id IN ('11','12');
		INSERT INTO conversion.deliveries(event_id,destination_id,environment,meta_event_id,role,deadline_at,finalized_at,state)
		SELECT id,1,'test',source_event_id,'original',now(),now(),'succeeded'
		FROM conversion.events WHERE source_order_id='11';`)
	var payload struct {
		Population []struct {
			Name              string `json:"event_name"`
			Eligibility       string
			Events, Confirmed int
			MissingTarget     int `json:"missing_target"`
		}
	}
	if err := pool.QueryRow(ctx, summarySQL, "test", 24*time.Hour, "starnovel", "", int64(0), int64(0)).Scan(&sample); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(sample, &payload); err != nil {
		t.Fatal(err)
	}
	type counts struct{ total, confirmed, missingTarget int }
	group := map[string]counts{}
	for _, row := range payload.Population {
		group[row.Name+":"+row.Eligibility] = counts{row.Events, row.Confirmed, row.MissingTarget}
	}
	if group["Purchase:expected"] != (counts{3, 1, 1}) || group["Subscribe:expected"] != (counts{1, 1, 0}) ||
		group["Purchase:environment_unknown"].total != 1 {
		t.Fatalf("population: %+v", group)
	}
	changes, err = repo.Collect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, change := range changes {
		found = found || change.Kind == "missing_original_target" && change.State == "opened"
	}
	if !found {
		t.Fatalf("missing route alert: %+v", changes)
	}
	mustExec(t, ctx, pool, `INSERT INTO conversion.deliveries(event_id,destination_id,environment,meta_event_id,role,deadline_at)
		SELECT id,1,'test',source_event_id,'original',now()+interval '1 hour'
		FROM conversion.events WHERE source_order_id='12';`)
	changes, err = repo.Collect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found = false
	for _, change := range changes {
		found = found || change.Kind == "missing_original_target" && change.State == "resolved"
	}
	if !found {
		t.Fatalf("alert not closed after original restored: %+v", changes)
	}

}
