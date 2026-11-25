package operations

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestConversionReviewReadPaths(t *testing.T) {
	ctx, pool := operationsTestPool(t)
	production := Repository{Pool: pool, Environment: "production"}
	sandbox := Repository{Pool: pool, Environment: "sandbox"}
	productionAPI, sandboxAPI := NewHandler(production, "fixture"), NewHandler(sandbox, "fixture")
	request := func(handler http.Handler, path string, status int, want string) []byte {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/internal/conversion"+path, nil)
		req.Header.Set("Authorization", "Bearer fixture")
		req.Header.Set("X-Operator-ID", "fixture")
		req.Header.Set("X-Operator-Role", "FinanceManager")
		req.Header.Set("X-Operator-Access", "read")
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != status || !strings.Contains(res.Body.String(), want) {
			t.Fatalf("%s: HTTP %d, expected %d with %s; response %.1000s", path, res.Code, status, want, res.Body.String())
		}
		return res.Body.Bytes()
	}

	mustExec(t, ctx, pool, `
		INSERT INTO users(id) VALUES (1931);
		INSERT INTO recharge_orders(id,user_id,amount_cents,payment_method,status,checkout_environment)
		VALUES (1931,1931,999,'paypal','Pending','sandbox'),
		       (1932,1931,999,'paypal','Pending','production'),
		       (1933,1931,999,'paypal','Pending',NULL),
		       (1934,1931,999,'paypal','Pending','live'),
		       (1935,1931,999,'paypal','Pending',NULL);
		INSERT INTO payment_channel_accounts(provider,environment,account_key,credentials_ref,webhook_ref)
		VALUES ('paypal','sandbox','conversion-review','fixture','fixture');
		INSERT INTO third_party_payment_orders(order_id,payment_provider,external_order_id,capture_id,object_type,account_id,status,gross_amount)
		SELECT n,'paypal','ORDER-'||n,'CAPTURE-'||n,'capture',a.id,'VERIFIED',9.99
		FROM payment_channel_accounts a CROSS JOIN (VALUES(1934),(1935)) fixture(n) WHERE a.account_key='conversion-review';
		INSERT INTO conversion.integrations(environment,source,account_key,contract_version)
		VALUES ('production','starnovel','reader','v1');
		INSERT INTO conversion.destinations(environment,media,pixel_id)
		SELECT 'production','meta',n::text FROM generate_series(1001,1051) n;
		INSERT INTO conversion.events(integration_id,environment,source_event_type,source_event_id,event_name,attribution_state,occurred_at,received_at)
		VALUES (1,'production','Purchase','payment-fixture','Purchase','matched',now(),now());
		INSERT INTO conversion.deliveries(event_id,destination_id,environment,meta_event_id,role,state,reason,deadline_at,finalized_at)
		SELECT 1,id,'production',pixel_id,CASE WHEN pixel_id='1001' THEN 'original' ELSE 'additional' END,
		    CASE WHEN pixel_id='1001' THEN 'succeeded' WHEN pixel_id='1050' THEN 'skipped'
		        WHEN pixel_id='1051' THEN 'failed' ELSE 'pending' END,
		    CASE WHEN pixel_id='1051' THEN 'meta_invalid_parameter' WHEN pixel_id='1050' THEN 'rule_ratio_skipped' END,
		    now()+interval '1 hour',CASE WHEN pixel_id IN ('1001','1050','1051') THEN now() END
		FROM conversion.destinations ORDER BY pixel_id;
		INSERT INTO conversion.delivery_attempts(delivery_id,attempt_no,lease_token,outcome,reason,responded_at,recorded_at)
		SELECT id,1,gen_random_uuid(),'retryable_rejection','meta_rate_limited',now(),now() FROM conversion.deliveries
		WHERE meta_event_id='1049';`)

	request(productionAPI, "/orders/1931", 404, "order_not_found")
	request(sandboxAPI, "/orders/1932", 404, "order_not_found")
	request(sandboxAPI, "/orders/1931", 200, `"environment":"sandbox"`)
	request(productionAPI, "/orders/1932", 200, `"environment":"production"`)
	request(productionAPI, "/orders/1933", 200, `"environment":"unknown"`)
	request(productionAPI, "/orders/1934", 200, `"environment":"conflict"`)
	request(sandboxAPI, "/orders/1935", 200, `"environment":"sandbox"`)
	request(productionAPI, "/orders/1935", 404, "order_not_found")

	type page struct {
		Deliveries []struct {
			ID      string
			PixelID string `json:"pixel_id"`
		}
		Cursor string `json:"next_cursor"`
	}
	var first, next, filtered page
	decode := func(path string, page *page) {
		t.Helper()
		if err := json.Unmarshal(request(productionAPI, path, 200, "deliveries"), page); err != nil {
			t.Fatal(err)
		}
	}
	decode("/events/1/deliveries", &first)
	if len(first.Deliveries) != 50 || first.Deliveries[0].PixelID != "1001" || first.Cursor == "" {
		t.Fatalf("first delivery page: %+v", first)
	}
	decode("/events/1/deliveries?cursor="+url.QueryEscape(first.Cursor), &next)
	decode("/events/1/deliveries?pixel_id=1051", &filtered)
	if len(next.Deliveries) != 1 || len(filtered.Deliveries) != 1 || next.Cursor != "" ||
		next.Deliveries[0].PixelID != "1051" || next.Deliveries[0].ID != filtered.Deliveries[0].ID {
		t.Fatalf("last delivery page and Pixel lookup: %+v / %+v", next, filtered)
	}
	request(sandboxAPI, "/events/1/deliveries", 404, "resource_not_found")

	mustExec(t, ctx, pool, `
        INSERT INTO reader_conversion_outbox(business_key,event_name,environment,created_at,consumed_at,snapshot)
        VALUES('throughput-input','Purchase','production',now()-interval '1 second',now(),'{}'),
              ('throughput-other-environment','Purchase','sandbox',now(),NULL,'{}');`)
	throughput := request(productionAPI, "/throughput?minutes=5&source=starnovel&pixel_id=1001", 200, `"received":1`)
	if !strings.Contains(string(throughput), `"normalized":1`) ||
		!strings.Contains(string(throughput), `"original_confirmed":1`) ||
		!strings.Contains(string(throughput), `"pixel_id":"1001"`) ||
		strings.Contains(string(throughput), `"pixel_id":"1051"`) {
		t.Fatalf("unexpected ingress or target scope: %s", throughput)
	}
	request(sandboxAPI, "/throughput?minutes=5", 200, `"pending":1`)
	request(productionAPI, "/throughput?minutes=61", 400, "invalid_filter")

	request(productionAPI, "/summary", 200, `"state":"waiting"`)
	changes, err := production.Collect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wanted := map[string]string{"meta_parameter_rejected": "opened", "meta_rate_limited": "opened"}
	for _, a := range changes {
		if wanted[a.Kind] == a.State {
			delete(wanted, a.Kind)
		}
		if a.Kind == "meta_local_validation_failed" {
			t.Fatalf("policy skip generated a failure alert: %+v", changes)
		}
	}
	if len(wanted) != 0 {
		t.Fatalf("missing active alerts: %v (got %+v)", wanted, changes)
	}
	if changes, err := production.Collect(ctx); err != nil || len(changes) != 0 {
		t.Fatalf("duplicate alerts: %+v %v", changes, err)
	}
	request(productionAPI, "/summary", 200, `"state":"fresh"`)
	mustExec(t, ctx, pool, `
		UPDATE conversion.collector_samples SET sampled_at=now()-interval '91 seconds';
		UPDATE conversion.deliveries SET finalized_at=now()-interval '16 minutes' WHERE state='failed';
		UPDATE conversion.delivery_attempts SET responded_at=now()-interval '16 minutes';
		ALTER TABLE conversion.collector_samples ADD CHECK (sampled_at <= '2000-01-01'::timestamptz) NOT VALID;`)
	if _, err = production.Collect(ctx); err == nil {
		t.Fatal("expected the sampling update to fail")
	}
	request(productionAPI, "/summary", 200, `"state":"stale"`)
	request(productionAPI, "/summary", 200, `"kind":"meta_parameter_rejected"`)
	mustExec(t, ctx, pool, `ALTER TABLE conversion.collector_samples DROP CONSTRAINT collector_samples_sampled_at_check;`)
	changes, err = production.Collect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wanted = map[string]string{"meta_parameter_rejected": "resolved", "meta_rate_limited": "resolved"}
	for _, a := range changes {
		if wanted[a.Kind] == a.State {
			delete(wanted, a.Kind)
		}
	}
	if len(wanted) != 0 {
		t.Fatalf("alerts did not resolve: %v (got %+v)", wanted, changes)
	}
	request(productionAPI, "/summary", 200, `"state":"fresh"`)
}
