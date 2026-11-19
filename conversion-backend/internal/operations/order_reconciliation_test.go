package operations

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
		"migrations/2026111904.sql", "migrations/2026111905.sql", "migrations/2026111906.sql", "migrations/2026111907.sql"} {
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
