package operations

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestOrderReconciliation(t *testing.T) {
	ctx, pool := operationsTestPool(t)
	mustExec(t, ctx, pool, `
		INSERT INTO users(id) VALUES (10);
		INSERT INTO conversion.integrations(environment,source,account_key,contract_version,enabled)
		VALUES ('test','sinan','partner','v1',true),('test','starnovel','reader','v1',true);
		INSERT INTO conversion.destinations(environment,media,pixel_id) VALUES ('test','meta','123');
		INSERT INTO recharge_orders(id,user_id,amount_cents,payment_method,status,fulfilled_at,capi_expectation)
		SELECT id,10,999,'paypal','Success',now(),
		       jsonb_build_object('events',jsonb_build_array(jsonb_build_object('name','Purchase','business_key','payment:'||id||':Purchase')),
		          'environment','test','time_source','paypal_capture_create_time','occurred_at',now())
		FROM generate_series(200,203) AS id;
		INSERT INTO reader_conversion_outbox(business_key,event_name,order_id,environment,occurred_at,snapshot)
		VALUES ('payment:201:Purchase','Purchase',201,'test',now(),'{"email":"secret-fixture"}');
		INSERT INTO conversion.events(integration_id,environment,source_event_type,source_event_id,event_name,source_order_id,
		   attribution_state,received_at,occurred_at,matching_data)
		VALUES (2,'test','Purchase','payment:202:Purchase','Purchase','202','missing_pixel',now(),now(),'{"em":["secret-fixture"]}'),
		       (2,'test','Purchase','payment:203:Purchase','Purchase','203','matched',now(),now(),'{"em":["secret-fixture"]}'),
		       (1,'test','recharge-paid','sinan-order-paid','Purchase','external-1','matched',now(),now(),'{}');
		INSERT INTO conversion.deliveries(event_id,destination_id,environment,meta_event_id,role,deadline_at,finalized_at,state)
		SELECT id,1,'test','test-confirmed-original','original',now(),now(),'succeeded'
		FROM conversion.events WHERE source_event_id='payment:203:Purchase';
		INSERT INTO conversion.inbox(integration_id,source_event_type,source_event_id,source_order_id,state,reason,payload)
		VALUES (1,'order-created','sinan-order-created','external-1','quarantined','invalid_fixture','{"email":"secret-fixture"}');
		INSERT INTO recharge_orders(id,user_id,amount_cents,payment_method,status,fulfilled_at) VALUES (100,10,999,'paypal','Success',now());
		WITH e AS (
			INSERT INTO conversion.events(integration_id,environment,source_event_type,source_event_id,event_name,source_order_id,attribution_state,received_at)
			VALUES (2,'test','Purchase','payment:100:Purchase','Purchase','100','unknown',now()) RETURNING id
		) INSERT INTO conversion.legacy_outbox_imports(legacy_event_id,event_id,legacy_state,legacy_attempts,
		    legacy_delivered_at,resolution,legacy_next_attempt_at,legacy_had_lease)
		  SELECT 'payment:100:Purchase',id,'delivered',1,now(),'succeeded_without_target',now(),false FROM e;`)
	mustExec(t, ctx, pool, `
		INSERT INTO conversion.events(integration_id,environment,source_event_type,source_event_id,event_name,source_order_id,
			attribution_state,received_at,partner_fields)
		VALUES(1,'test','recharge-paid','app-1-payment','Purchase','shared-order','matched',now(),'{"app_id":"1"}'),
		      (1,'test','recharge-paid','app-2-payment','Purchase','shared-order','matched',now(),'{"app_id":"2"}');
		INSERT INTO conversion.inbox(integration_id,source_event_type,source_event_id,source_order_id,source_app_id,
			state,reason,payload_digest,normalized_event_id)
		SELECT 1,'recharge-paid','app-1-alias','shared-order','1','consumed','duplicate_payment_event','fixture',id
		FROM conversion.events WHERE source_event_id='app-1-payment';
	`)
	handler := NewHandler(Repository{Pool: pool, Environment: "test"}, "service-fixture")
	for _, tt := range []struct {
		path, token, role string
		status            int
		want              string
	}{
		{"/200", "", "Finance", 401, "service_auth_required"},
		{"/200", "service-fixture", "Editor", 403, "operator_forbidden"},
		{"/abc", "service-fixture", "Finance", 400, "invalid_order_key"},
		{"/9999", "service-fixture", "Finance", 404, "order_not_found"},
		{"/200", "service-fixture", "Finance", 200, "missing_outbox"},
		{"/201", "service-fixture", "MediaBuyer", 200, "awaiting_normalization"},
		{"/202", "service-fixture", "Admin", 200, "missing_target"},
		{"/203", "service-fixture", "SuperAdmin", 200, "test-confirmed-original"},
		{"/100", "service-fixture", "Finance", 200, "confirmed"},
		{"/external-1?integration_id=1", "service-fixture", "Finance", 200, "quarantined"},
		{"/external-1?integration_id=2", "service-fixture", "Finance", 404, "order_not_found"},
		{"/shared-order?integration_id=1", "service-fixture", "Finance", 409, `"app_ids":["1","2"]`},
		{"/shared-order?integration_id=1&app_id=1", "service-fixture", "Finance", 200, "duplicate_payment_event"},
		{"/shared-order?integration_id=1&app_id=2", "service-fixture", "Finance", 200, "app-2-payment"},
		{"/shared-order?integration_id=1&app_id=3", "service-fixture", "Finance", 404, "order_not_found"},
	} {
		t.Run(tt.path+"/"+tt.role+"/"+tt.want, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/internal/conversion/orders"+tt.path, nil)
			req.Header.Set("Authorization", "Bearer "+tt.token)
			req.Header.Set("X-Operator-ID", "test-admin")
			req.Header.Set("X-Operator-Role", tt.role)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if response.Code != tt.status || !strings.Contains(response.Body.String(), tt.want) || !json.Valid(response.Body.Bytes()) {
				t.Fatalf("status=%d result=%s", response.Code, response.Body.String())
			}
			if strings.Contains(response.Body.String(), "secret-fixture") {
				t.Fatal("private source data leaked into reconciliation")
			}
			if strings.HasSuffix(tt.path, "app_id=1") &&
				(strings.Contains(response.Body.String(), "app-2-payment") || !strings.Contains(response.Body.String(), "normalized_event_id")) {
				t.Fatalf("unexpected scoped evidence: %s", response.Body.String())
			}
		})
	}
}

func operationsTestPool(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("STAR_NOVEL_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires disposable PostgreSQL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	name := "ops_test_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	identifier := pgx.Identifier{name}.Sanitize()
	mustExec(t, ctx, admin, "CREATE DATABASE "+identifier)
	t.Cleanup(func() {
		defer admin.Close()
		mustExec(t, context.Background(), admin, "DROP DATABASE "+identifier+" WITH (FORCE)")
	})
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.Database = name
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	for _, asset := range []string{"schema.sql", "migrations/2026111702.sql", "migrations/2026111703.sql",
		"migrations/2026111704.sql", "migrations/2026111901.sql", "migrations/2026111902.sql", "migrations/2026111903.sql",
		"migrations/2026111904.sql", "migrations/2026111905.sql", "migrations/2026111906.sql", "migrations/2026111907.sql", "migrations/2026111908.sql", "migrations/2026111909.sql", "migrations/2026111910.sql", "migrations/2026111911.sql", "migrations/2026111912.sql", "migrations/2026111913.sql", "migrations/2026111914.sql", "migrations/2026111915.sql", "migrations/2026111916.sql", "migrations/2026111917.sql", "migrations/2026111918.sql", "migrations/2026111919.sql", "migrations/2026111920.sql", "migrations/2026111921.sql", "migrations/2026111922.sql", "migrations/2026111923.sql", "migrations/2026111924.sql", "migrations/2026111925.sql", "migrations/2026111926.sql", "migrations/2026111927.sql", "migrations/2026111928.sql"} {
		sql, err := os.ReadFile("../../../cms-backend/db/" + asset)
		if err != nil {
			t.Fatal(err)
		}
		mustExec(t, ctx, pool, string(sql))
	}
	return ctx, pool
}

func mustExec(t *testing.T, ctx context.Context, pool *pgxpool.Pool, sql string) {
	t.Helper()
	if _, err := pool.Exec(ctx, sql); err != nil {
		t.Fatal(err)
	}
}

func TestEventDetailShowsBoundedPartnerAttemptEvidence(t *testing.T) {
	ctx, pool := operationsTestPool(t)
	mustExec(t, ctx, pool, `
		INSERT INTO conversion.integrations(environment,source,account_key,contract_version)
		VALUES ('test','sinan','fixture','sinan-mock-v1');
		INSERT INTO conversion.destinations(environment,media,pixel_id) VALUES ('test','meta','123');
		INSERT INTO conversion.events(integration_id,environment,source_event_type,source_event_id,event_name,attribution_state,received_at)
		VALUES(1,'test','order-created','fixture-event','InitiateCheckout','matched',now());
		INSERT INTO conversion.deliveries(event_id,destination_id,environment,meta_event_id,role,deadline_at,state,finalized_at)
		VALUES(1,1,'test','fixture-event','original',now(),'succeeded',now());
		INSERT INTO conversion.partner_reports(integration_id,event_id,original_delivery_id,report_status,contract_version,payload,attempts,wire_payload)
		VALUES(1,1,1,1,'sinan-mock-v1','{"raw":"private-payload-fixture"}',21,'private-wire-fixture');
		INSERT INTO conversion.partner_report_attempts(report_id,attempt_no,sending_at,responded_at,recorded_at,outcome,reason,retry_at,http_status,business_code)
		SELECT 1,n,now(),now(),now(),'rejected','partner_rejected',now()+interval '1 minute',
		       CASE WHEN n=21 THEN 401 ELSE 400 END,9223372036854775807
		FROM generate_series(1,21) n;
	`)
	req := httptest.NewRequest(http.MethodGet, "/internal/conversion/events/1", nil)
	req.Header.Set("Authorization", "Bearer service-fixture")
	req.Header.Set("X-Operator-ID", "test-admin")
	req.Header.Set("X-Operator-Role", "Finance")
	response := httptest.NewRecorder()
	NewHandler(Repository{Pool: pool, Environment: "test"}, "service-fixture").ServeHTTP(response, req)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "private-") {
		t.Fatalf("status=%d result=%s", response.Code, response.Body)
	}
	var detail struct {
		Reports []struct {
			AttemptCount int `json:"attempt_count"`
			Attempts     []struct {
				Number       int        `json:"number"`
				HTTPStatus   int        `json:"http_status"`
				BusinessCode string     `json:"business_code"`
				SendingAt    *time.Time `json:"sending_at"`
				RespondedAt  *time.Time `json:"responded_at"`
				RecordedAt   *time.Time `json:"recorded_at"`
				RetryAt      *time.Time `json:"retry_at"`
			} `json:"attempts"`
		} `json:"partner_reports"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if len(detail.Reports) != 1 || detail.Reports[0].AttemptCount != 21 || len(detail.Reports[0].Attempts) != 20 {
		t.Fatalf("unexpected report history length: %s", response.Body)
	}
	first := detail.Reports[0].Attempts[0]
	if first.Number != 21 || first.HTTPStatus != 401 || first.BusinessCode != "9223372036854775807" ||
		first.SendingAt == nil || first.RespondedAt == nil || first.RecordedAt == nil || first.RetryAt == nil ||
		detail.Reports[0].Attempts[1].HTTPStatus != 400 {
		t.Fatalf("unexpected report evidence: %+v", first)
	}
}

func TestEventsFilterValidation(t *testing.T) {
	handler := NewHandler(Repository{Environment: "test"}, "service-fixture")
	cases := []struct {
		name  string
		query string
	}{
		{name: "invalid partner_report_state", query: "?partner_report_state=unknown_state"},
		{name: "overlong source_event_id", query: "?source_event_id=" + strings.Repeat("a", 257)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/internal/conversion/events"+tc.query, nil)
			req.Header.Set("Authorization", "Bearer service-fixture")
			req.Header.Set("X-Operator-ID", "test-admin")
			req.Header.Set("X-Operator-Role", "Finance")
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), "invalid_filter") {
				t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
			}
		})
	}
}

func TestEventsListProjectionAndFilters(t *testing.T) {
	ctx, pool := operationsTestPool(t)
	mustExec(t, ctx, pool, `
		INSERT INTO conversion.integrations(environment,source,account_key,contract_version,enabled)
		VALUES ('test','sinan','partner','sinan-mock-v1',true);
		INSERT INTO conversion.destinations(environment,media,pixel_id) VALUES ('test','meta','123');
		INSERT INTO conversion.events(integration_id,environment,source_event_type,source_event_id,event_name,source_order_id,
			attribution_state,occurred_at,received_at,created_at,amount,currency,matching_data)
		VALUES (1,'test','recharge-paid','sinan-evt-48h','Purchase','ext-48h','matched',
			now()-interval '48 hours',now()-interval '48 hours',now()-interval '48 hours',19.99,'USD','{}');
		INSERT INTO conversion.deliveries(event_id,destination_id,environment,meta_event_id,role,deadline_at,finalized_at,state,reason)
		VALUES (1,1,'test','sinan-evt-48h','original',now()-interval '1 hour',now()-interval '1 hour','failed','meta_no_confirmation');
		INSERT INTO conversion.partner_reports(integration_id,event_id,original_delivery_id,report_status,contract_version,payload,state,reason,wire_payload)
		VALUES (1,1,1,-1,'sinan-mock-v1','{"secret":"private-fixture"}','blocked','no_trace_contract_unconfirmed','private-wire-fixture');
	`)

	handler := NewHandler(Repository{Pool: pool, Environment: "test"}, "service-fixture")
	doGet := func(target string) (int, []byte) {
		req := httptest.NewRequest(http.MethodGet, "/internal/conversion"+target, nil)
		req.Header.Set("Authorization", "Bearer service-fixture")
		req.Header.Set("X-Operator-ID", "test-admin")
		req.Header.Set("X-Operator-Role", "Finance")
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		return res.Code, res.Body.Bytes()
	}

	// 1. source_event_id bypasses default 24h window and projects amount/currency + partner_report summary
	code, body := doGet("/events?source_event_id=sinan-evt-48h")
	if code != http.StatusOK || strings.Contains(string(body), "private-") {
		t.Fatalf("source_event_id status=%d body=%s", code, string(body))
	}
	var listResp struct {
		Events []struct {
			ID            string `json:"id"`
			Amount        string `json:"amount"`
			Currency      string `json:"currency"`
			PartnerReport *struct {
				ID     string `json:"id"`
				Status int    `json:"status"`
				State  string `json:"state"`
				Reason string `json:"reason"`
			} `json:"partner_report"`
		} `json:"events"`
	}
	if err := json.Unmarshal(body, &listResp); err != nil {
		t.Fatal(err)
	}
	if len(listResp.Events) != 1 || listResp.Events[0].Amount != "19.99" || listResp.Events[0].Currency != "USD" ||
		listResp.Events[0].PartnerReport == nil || listResp.Events[0].PartnerReport.State != "blocked" ||
		listResp.Events[0].PartnerReport.Status != -1 || listResp.Events[0].PartnerReport.Reason != "no_trace_contract_unconfirmed" {
		t.Fatalf("unexpected list projection: %s", string(body))
	}

	// 2. partner_report_state filter within explicit window
	from72h := url.QueryEscape(time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339))
	code, body = doGet("/events?from=" + from72h + "&partner_report_state=blocked")
	if code != http.StatusOK || !strings.Contains(string(body), "sinan-evt-48h") {
		t.Fatalf("partner_report_state=blocked status=%d body=%s", code, string(body))
	}
	code, body = doGet("/events?from=" + from72h + "&partner_report_state=confirmed")
	if code != http.StatusOK || strings.Contains(string(body), "sinan-evt-48h") {
		t.Fatalf("partner_report_state=confirmed should be empty, status=%d body=%s", code, string(body))
	}

	// 3. event detail also projects amount and currency
	code, body = doGet("/events/" + listResp.Events[0].ID)
	if code != http.StatusOK || !strings.Contains(string(body), `"amount":"19.99"`) || !strings.Contains(string(body), `"currency":"USD"`) {
		t.Fatalf("detail projection status=%d body=%s", code, string(body))
	}
}
