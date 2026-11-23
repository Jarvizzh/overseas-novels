package sinan

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"conversion-backend/internal/credentials"
	"conversion-backend/internal/delivery"
	"conversion-backend/internal/partner"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const fixtureToken = "fixture-sinan-callback-token"

type fixture struct {
	ctx      context.Context
	owner    *pgxpool.Pool
	report   partner.ReportQueue
	svc      delivery.Service
	api      delivery.Service
	inbox    Inbox
	keys     credentials.Keyring
	public   ed25519.PublicKey
	occurred int64
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	return newFixtureFrom(t, "2026111926", nil)
}

func newFixtureFrom(t *testing.T, schemaVersion string, beforeUpgrade func(fixture)) fixture {
	t.Helper()
	dsn := os.Getenv("STAR_NOVEL_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires disposable PostgreSQL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	db := "sinan_test_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	dbName := pgx.Identifier{db}.Sanitize()
	exec(t, ctx, admin, "CREATE DATABASE "+dbName)
	t.Cleanup(func() { exec(t, context.Background(), admin, "DROP DATABASE "+dbName+" WITH (FORCE)") })
	cfg := admin.Config()
	cfg.ConnConfig.Database = db
	owner, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(owner.Close)
	execSQLFile(t, ctx, owner, "schema.sql", nil)
	versions := []string{"2026111702", "2026111703", "2026111704", "2026111901", "2026111902",
		"2026111903", "2026111904", "2026111905", "2026111906", "2026111907", "2026111908", "2026111909", "2026111910", "2026111911", "2026111912", "2026111913", "2026111914", "2026111915", "2026111916", "2026111917", "2026111918", "2026111919", "2026111920", "2026111921", "2026111922", "2026111923", "2026111924", "2026111925", "2026111926", "2026111927", "2026111928", "2026111929"}
	for _, version := range versions {
		if version <= schemaVersion {
			execSQLFile(t, ctx, owner, "migrations/"+version+".sql", nil)
		}
	}
	keys, err := credentials.Parse(fmt.Sprintf(`{"fixture":%q}`, base64.StdEncoding.EncodeToString(make([]byte, 32))))
	if err != nil {
		t.Fatal(err)
	}
	public, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	inbound, err := keys.Encrypt("fixture", fixtureToken)
	if err != nil {
		t.Fatal(err)
	}
	signingKey, err := keys.Encrypt("fixture", base64.StdEncoding.EncodeToString(private.Seed()))
	if err != nil {
		t.Fatal(err)
	}
	exec(t, ctx, owner, "INSERT INTO conversion.credentials(ciphertext,key_version) VALUES($1,'fixture'),($2,'fixture')", inbound, signingKey)
	exec(t, ctx, owner, `
		INSERT INTO conversion.integrations(source,account_key,environment,enabled,allowed_apps,allowed_events,contract_version,
		                                    inbound_credential_id,report_credential_id,report_config)
		VALUES ('sinan','publisher','test',true,ARRAY['1','2'],ARRAY['attribution-succeeded','order-created','recharge-paid'],
		  'sinan-mock-v1',1,2,'{"app_pixels":{"1":["123","456"],"2":["789"]},"source_hosts":["reader.example"],"max_age_seconds":86400}');
		INSERT INTO conversion.destinations(environment,media,pixel_id,credential_id)
		VALUES ('test','meta','123',1),('test','meta','456',1),('test','meta','789',1);
		INSERT INTO conversion.integration_destinations(integration_id,destination_id,environment,default_route)
		SELECT 1,id,environment,true FROM conversion.destinations`)
	f := fixture{ctx: ctx, owner: owner, keys: keys, public: public, occurred: time.Now().Add(-time.Minute).Unix()}
	if beforeUpgrade != nil {
		beforeUpgrade(f)
	}
	for _, version := range versions {
		if version > schemaVersion {
			execSQLFile(t, ctx, owner, "migrations/"+version+".sql", nil)
		}
	}
	oldRoles := []string{"reader", "cms", "worker", "api", "collector", "retention"}
	newRoles := []string{"callback", "partner_worker"}
	roles := make(map[string]string)
	for _, kind := range append(oldRoles, newRoles...) {
		role := pgx.Identifier{db + "_" + kind}.Sanitize()
		roles[kind] = role
		exec(t, ctx, owner, "CREATE ROLE "+role+" NOLOGIN")
		t.Cleanup(func() { exec(t, context.Background(), owner, "DROP OWNED BY "+role+"; DROP ROLE "+role) })
	}
	execSQLFile(t, ctx, owner, "conversion_role_grants.sql", roles)
	execSQLFile(t, ctx, owner, "conversion_sinan_mock_grants.sql", roles)
	rolePool := func(kind string) *pgxpool.Pool {
		config := owner.Config()
		config.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
			_, err := conn.Exec(ctx, "SET ROLE "+roles[kind])
			return err
		}
		pool, err := pgxpool.NewWithConfig(ctx, config)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(pool.Close)
		return pool
	}
	f.report = partner.ReportQueue{Pool: rolePool("partner_worker"), Environment: TestEnvironment, Source: "sinan", Contract: MockContract}
	f.svc = delivery.Service{Store: delivery.Store{Pool: rolePool("worker"), Environment: TestEnvironment}}
	f.api = delivery.Service{Store: delivery.Store{Pool: rolePool("api"), Environment: TestEnvironment}}
	f.inbox = Inbox{Pool: rolePool("callback"), Environment: TestEnvironment, Decrypt: keys.Decrypt}
	return f
}

func execSQLFile(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name string, roles map[string]string) {
	t.Helper()
	raw, err := os.ReadFile("../../../../cms-backend/db/" + name)
	if err != nil {
		t.Fatal(err)
	}
	sql := strings.TrimPrefix(string(raw), "\\set ON_ERROR_STOP on\n")
	for kind, role := range roles {
		sql = strings.ReplaceAll(sql, `:"`+kind+`_role"`, role)
	}
	exec(t, ctx, pool, sql)
}

func exec(t *testing.T, ctx context.Context, pool *pgxpool.Pool, query string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(ctx, query, args...); err != nil {
		t.Fatal(err)
	}
}

func (f fixture) check(t *testing.T, expected, query string, args ...any) {
	t.Helper()
	var value string
	if err := f.owner.QueryRow(f.ctx, query, args...).Scan(&value); err != nil || value != expected {
		t.Fatalf("expected %q, got %q (%v) from %s", expected, value, err, query)
	}
}

func (f fixture) callbackBody(eventID string) string {
	return fmt.Sprintf(`{"app_id":1,"pixel_id":123,"event_id":%q,"event_time":%d,
		"ad_id":"9223372036854775807","adset_id":"9223372036854775806","order_id":"demo_order_0001",
		"distributor_id":9223372036854775806,"device_id":"device_demo_001",
		"recharge_type":1,"amount":"10.00","currency":"usd",
		"external_id_hash":%q,"email_hash":"invalid",
		"client_ip":"192.0.2.10","user_agent":"fixture-browser","event_source_url":"https://reader.example/read?token=removed#secret"}`,
		eventID, f.occurred, strings.Repeat("A", 64))
}

func post(handler http.Handler, key, eventType, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/callbacks/sinan/"+key+"/"+eventType, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w
}

func (f fixture) ingest(t *testing.T, eventType, eventID string) {
	t.Helper()
	w := post(f.inbox.Handler(), "publisher", eventType, fixtureToken, f.callbackBody(eventID))
	if w.Code != 200 || w.Body.String() != "{\"code\":0,\"msg\":\"success\"}\n" {
		t.Fatalf("callback failed %d %s", w.Code, w.Body.String())
	}
}

func TestCallbackInboxScopesAndNormalization(t *testing.T) {
	f := newFixture(t)
	handler := f.inbox.Handler()
	body := f.callbackBody("same-id")
	for _, tc := range []struct {
		key, path, token, body string
		status                 int
	}{
		{"publisher", "recharge-paid", "wrong", "this-is-not-json", 401},
		{"other", "recharge-paid", fixtureToken, body, 401},
		{"publisher", "recharge-paid", fixtureToken, strings.Replace(body, `"app_id":1`, `"app_id":2`, 1), 403},
		{"publisher", "recharge-paid", fixtureToken, strings.Replace(body, `"pixel_id":123`, `"pixel_id":789`, 1), 403},
		{"publisher", "recharge-paid", fixtureToken, "{", 400},
		{"publisher", "recharge-paid", fixtureToken, `{"app_id":1,"app_id":2}`, 400},
		{"publisher", "recharge-paid", fixtureToken, strings.Repeat(" ", 33<<10), 413},
		{"publisher", "subscribe", fixtureToken, body, 404},
		{"publisher", "recharge-paid", fixtureToken, strings.Replace(body, "same-id", "", 1), 422},
		{"publisher", "recharge-paid", fixtureToken, strings.Replace(body, strconv.FormatInt(f.occurred, 10), "0", 1), 422},
	} {
		if w := post(handler, tc.key, tc.path, tc.token, tc.body); w.Code != tc.status {
			t.Fatalf("expected %d, got %d %s", tc.status, w.Code, w.Body.String())
		}
	}
	production := f.inbox
	production.Environment = "production"
	if w := post(production.Handler(), "publisher", "recharge-paid", fixtureToken, body); w.Code != 404 {
		t.Fatalf("production callback returned %d", w.Code)
	}
	f.check(t, "0", "SELECT count(*)::text FROM conversion.inbox WHERE state<>'quarantined'")
	exec(t, f.ctx, f.owner, "ALTER TABLE conversion.inbox ADD CONSTRAINT reject_fixture CHECK (state='quarantined') NOT VALID")
	if w := post(handler, "publisher", "order-created", fixtureToken, body); w.Code != 503 {
		t.Fatalf("failed persistence returned %d", w.Code)
	}
	exec(t, f.ctx, f.owner, "ALTER TABLE conversion.inbox DROP CONSTRAINT reject_fixture")

	var concurrent sync.WaitGroup
	for i := range 12 {
		concurrent.Go(func() {
			replay := strings.Replace(body, `"10.00"`, "10", 1)
			replay = strings.Replace(replay, `"ad_id":"9223372036854775807"`, `"ad_id":9223372036854775807`, 1)
			replay = strings.Replace(replay, `"client_ip"`, `"client_ip_address"`, 1)
			replay = strings.Replace(replay, `"user_agent"`, `"client_user_agent"`, 1)
			replay = `{"signature":"transport-` + strconv.Itoa(i) + `",` + replay[1:]
			if w := post(handler, "publisher", "recharge-paid", fixtureToken, replay); w.Code != 200 {
				t.Errorf("replay returned %d %s", w.Code, w.Body.String())
			}
		})
	}
	concurrent.Wait()
	if w := post(handler, "publisher", "recharge-paid", fixtureToken, strings.Replace(body, `"10.00"`, `"11.00"`, 1)); w.Code != 409 {
		t.Fatalf("key conflict returned %d", w.Code)
	}
	f.ingest(t, "order-created", "same-id")
	f.ingest(t, "attribution-succeeded", "same-id")
	f.ingest(t, "attribution-succeeded", "second-dye")
	if w := post(handler, "publisher", "recharge-paid", fixtureToken, strings.Replace(strings.Replace(f.callbackBody("tokens"), `"recharge_type":1`, `"recharge_type":2`, 1), "demo_order_0001", "demo_order_0002", 1)); w.Code != 200 {
		t.Fatalf("token purchase returned %d %s", w.Code, w.Body.String())
	}
	f.check(t, "0", "SELECT count(*)::text FROM conversion.events")
	exec(t, f.ctx, f.owner, "ALTER TABLE conversion.deliveries ADD CONSTRAINT reject_fixture CHECK (false) NOT VALID")
	if count, err := Consume(f.ctx, f.svc, 20); err == nil || count != 0 {
		t.Fatalf("dependent write failure was ignored: %d %v", count, err)
	}
	f.check(t, "0", "SELECT count(*)::text FROM conversion.events")
	f.check(t, "0", "SELECT count(*)::text FROM conversion.payment_policy_decisions")
	f.check(t, "5", "SELECT count(*)::text FROM conversion.inbox WHERE state='pending'")
	exec(t, f.ctx, f.owner, "ALTER TABLE conversion.deliveries DROP CONSTRAINT reject_fixture")
	if count, err := Consume(f.ctx, f.svc, 20); err != nil || count != 5 {
		t.Fatalf("normalized %d: %v", count, err)
	}
	f.ingest(t, "recharge-paid", "same-id")
	f.check(t, "5", "SELECT count(*)::text FROM conversion.inbox WHERE state='consumed'")
	f.check(t, "3", "SELECT count(*)::text FROM conversion.inbox WHERE state='quarantined'")
	f.check(t, "CompleteRegistration,CompleteRegistration,InitiateCheckout,Purchase,Purchase",
		"SELECT string_agg(event_name,',' ORDER BY event_name) FROM conversion.events")
	f.check(t, "1,2", "SELECT string_agg(order_type,',' ORDER BY order_type) FROM conversion.events WHERE event_name='Purchase'")
	f.check(t, "5:5:10", "SELECT count(DISTINCT meta_event_id)||':'||count(*) FILTER(WHERE role='original')||':'||count(*) FROM conversion.deliveries")
	f.check(t, "0", "SELECT count(*)::text FROM conversion.deliveries WHERE destination_id=3")
	f.check(t, "5", "SELECT count(*)::text FROM conversion.events WHERE extract(epoch FROM occurred_at)=$1 AND source_order_id IN ('demo_order_0001','demo_order_0002')", f.occurred)
	f.check(t, "https://reader.example/read:9223372036854775807:"+strings.Repeat("a", 64), `
		SELECT source_url||':'||(partner_fields->>'ad_id')||':'||(matching_data->'external_id'->>0)
		FROM conversion.events WHERE source_event_type='order-created'`)
	f.check(t, "0", "SELECT count(*)::text FROM conversion.events WHERE matching_data ? 'em' OR matching_data->>'client_ip_address' IS DISTINCT FROM '192.0.2.10' OR matching_data->>'client_user_agent' IS DISTINCT FROM 'fixture-browser'")
}

func TestCallbackOriginalRoutingAndRevocation(t *testing.T) {
	f := newFixture(t)
	exec(t, f.ctx, f.owner, `
		INSERT INTO novels(id,title,author) VALUES(1,'fixture','fixture');
		INSERT INTO fb_pixels(id,name,pixel_id,access_token) VALUES(7,'Fixture','456','fixture');
		INSERT INTO promotion_links(id,novel_id,novel_title,generated_url,fb_pixel_id) VALUES(33,1,'fixture','fixture',7);
	`)
	// A mapping for application 2 has no effect on application 1, even when
	// an unrelated StarNovel promotion uses the same numeric ID.
	exec(t, f.ctx, f.owner, `UPDATE conversion.integrations SET report_config=report_config ||
	    '{"promotion_pixels":{"2":{"33":"789"}}}'`)
	body := strings.Replace(f.callbackBody("independent"), `"app_id":1`, `"app_id":1,"promotion_id":33`, 1)
	if w := post(f.inbox.Handler(), "publisher", "order-created", fixtureToken, body); w.Code != 200 {
		t.Fatalf("expected authorized callback: %d %s", w.Code, w.Body.String())
	}
	if count, err := Consume(f.ctx, f.svc, 20); count != 1 || err != nil {
		t.Fatalf("normalized %d: %v", count, err)
	}
	f.check(t, "pending:", `
		SELECT d.state||':'||conversion.sinan_route_issue(d.event_id,d.destination_id)
		FROM conversion.deliveries d JOIN conversion.events e ON e.id=d.event_id
		WHERE d.role='original' AND e.source_event_id='independent'`)
	exec(t, f.ctx, f.owner, `UPDATE conversion.integrations SET report_config=report_config ||
	    '{"promotion_pixels":{"1":{"33":"456"},"2":{"33":"789"}}}'`)
	f.check(t, "promotion_pixel_mismatch", `
		SELECT conversion.sinan_route_issue(d.event_id,d.destination_id)
		FROM conversion.deliveries d JOIN conversion.events e ON e.id=d.event_id
		WHERE d.role='original' AND e.source_event_id='independent'`)
	body = strings.Replace(f.callbackBody("mismatch"), `"app_id":1`, `"app_id":1,"promotion_id":33`, 1)
	if w := post(f.inbox.Handler(), "publisher", "order-created", fixtureToken, body); w.Code != 200 {
		t.Fatalf("expected persisted mismatch: %d %s", w.Code, w.Body.String())
	}
	if count, err := Consume(f.ctx, f.svc, 20); count != 1 || err != nil {
		t.Fatalf("normalized %d: %v", count, err)
	}
	f.check(t, "original:blocked:promotion_pixel_mismatch:123", `
		SELECT d.role||':'||d.state||':'||d.reason||':'||x.pixel_id
		FROM conversion.deliveries d JOIN conversion.destinations x ON x.id=d.destination_id
		JOIN conversion.events e ON e.id=d.event_id WHERE e.source_event_id='mismatch'`)
	f.ingest(t, "order-created", "revoked")
	exec(t, f.ctx, f.owner, "DELETE FROM conversion.integration_destinations WHERE destination_id=1")
	if count, err := Consume(f.ctx, f.svc, 20); count != 1 || err != nil {
		t.Fatalf("normalized %d: %v", count, err)
	}
	f.check(t, "original:blocked:partner_route_not_authorized:123", `
		SELECT d.role||':'||d.state||':'||d.reason||':'||x.pixel_id
		FROM conversion.deliveries d JOIN conversion.destinations x ON x.id=d.destination_id
		JOIN conversion.events e ON e.id=d.event_id WHERE e.source_event_id='revoked'`)
	exec(t, f.ctx, f.owner, `
		UPDATE conversion.integrations SET contract_version='future-contract';
		UPDATE conversion.deliveries SET deadline_at=now();
	`)
	if count, err := f.svc.Sweep(f.ctx, 20, time.Second); count != 4 || err != nil {
		t.Fatalf("finalized %d: %v", count, err)
	}
	f.check(t, "3", "SELECT count(*)::text FROM conversion.partner_reports WHERE contract_version='sinan-mock-v1'")
}

func TestReportWorkerReplaysFrozenBusinessPayload(t *testing.T) {
	f := newFixture(t)
	body := strings.Replace(f.callbackBody("payment"), `"app_id":1`, `"app_id":1,"promotion_id":9223372036854775805`, 1)
	if w := post(f.inbox.Handler(), "publisher", "recharge-paid", fixtureToken, body); w.Code != 200 {
		t.Fatalf("callback returned %d %s", w.Code, w.Body.String())
	}
	if count, err := Consume(f.ctx, f.svc, 10); count != 1 || err != nil {
		t.Fatalf("normalized %d: %v", count, err)
	}
	// Deliver the additional target first. The partner should see only the original.
	exec(t, f.ctx, f.owner, "UPDATE conversion.deliveries SET next_attempt_at=now()+interval '1 hour' WHERE role='original'")
	f.completeMeta(t, delivery.Result{Outcome: delivery.Accepted, TraceID: "additional_trace"})
	f.check(t, "0", "SELECT count(*)::text FROM conversion.partner_reports")
	exec(t, f.ctx, f.owner, "UPDATE conversion.deliveries SET next_attempt_at=now() WHERE role='original'")
	f.completeMeta(t, delivery.Result{Outcome: delivery.Accepted, TraceID: "original_trace"})

	type sent struct {
		body, nonce, signature string
	}
	sends := make(chan sent, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		nonce, sig := r.Header.Get("X-Sinan-Nonce"), r.Header.Get("X-Sinan-Signature")
		timestamp := r.Header.Get("X-Sinan-Timestamp")
		signature, decodeErr := base64.StdEncoding.DecodeString(sig)
		if err != nil || decodeErr != nil || r.Method != "POST" || r.URL.Path != "/final" || len(nonce) != 32 ||
			!ed25519.Verify(f.public, signingInput("/final", timestamp, nonce, body), signature) {
			t.Error("invalid signed report")
			w.WriteHeader(http.StatusForbidden)
			return
		}
		sends <- sent{string(body), nonce, sig}
		switch len(sends) {
		case 1:
			w.Header().Set("Retry-After", "120")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"code":429}`)
		case 2:
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"code":0}`)
		default:
			_, _ = io.WriteString(w, `{"code":0}`)
		}
	}))
	defer server.Close()
	f.configureReport(t, server.URL+"/final", true, "")
	reporter := f.reporter(server.Client())
	f.processReport(t, reporter)
	f.check(t, "pending:partner_rate_limited:true", "SELECT state||':'||reason||':'||(next_attempt_at>now()+interval '110 seconds')::text FROM conversion.partner_reports")
	makeDue := "UPDATE conversion.partner_reports SET next_attempt_at=now()"
	exec(t, f.ctx, f.owner, makeDue)
	f.processReport(t, reporter)
	f.check(t, "pending:partner_response_unconfirmed", "SELECT state||':'||reason FROM conversion.partner_reports")
	exec(t, f.ctx, f.owner, makeDue)
	f.processReport(t, reporter)
	f.check(t, "confirmed:3", "SELECT state||':'||attempts FROM conversion.partner_reports")
	f.check(t, "succeeded:1", "SELECT state||':'||attempts FROM conversion.deliveries WHERE role='original'")

	first, second, third := <-sends, <-sends, <-sends
	if first.body != second.body || second.body != third.body || first.nonce == second.nonce || second.nonce == third.nonce ||
		first.signature == second.signature || second.signature == third.signature {
		t.Fatal("report business body or authentication did not follow replay semantics")
	}
	var result map[string]json.RawMessage
	if err := json.Unmarshal([]byte(first.body), &result); err != nil ||
		string(result["report_status"]) != "1" || string(result["pixel_id"]) != `"123"` ||
		string(result["event_id"]) != `"payment"` || string(result["fbtrace_id"]) != `"original_trace"` ||
		string(result["ad_id"]) != `"9223372036854775807"` || string(result["ad_set_id"]) != `"9223372036854775806"` ||
		result["adset_id"] != nil || string(result["app_id"]) != "1" ||
		string(result["distributor_id"]) != "9223372036854775806" || string(result["promotion_id"]) != "9223372036854775805" ||
		string(result["order_id"]) != `"demo_order_0001"` || string(result["device_id"]) != `"device_demo_001"` ||
		len(result["report_time"]) == 0 {
		t.Fatalf("unexpected report payload %s (%v)", first.body, err)
	}
}

func (f fixture) completeMeta(t *testing.T, result delivery.Result) delivery.Lease {
	t.Helper()
	job, err := f.svc.Claim(f.ctx, true, 5, time.Minute, time.Second, 0)
	if err != nil {
		t.Fatal(err)
	}
	p, err := f.svc.Payload(f.ctx, job)
	if err != nil || p.CredentialID == nil || p.RouteIssue != "" {
		t.Fatalf("Meta payload %v %q", err, p.RouteIssue)
	}
	if err := delivery.ValidatePayload(p); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.StartSend(f.ctx, job, *p.CredentialID, p.Ciphertext, *p.KeyVersion, time.Second); err != nil {
		t.Fatal(err)
	}
	if result.Outcome == delivery.Accepted {
		status, received := 200, 1
		result.HTTPStatus, result.EventsReceived = &status, &received
	}
	if err := f.svc.Complete(f.ctx, job, result, time.Second); err != nil {
		t.Fatal(err)
	}
	return job
}

func (f fixture) reporter(client *http.Client) partner.Reporter {
	return partner.Reporter{Queue: f.report, Adapter: ReportAdapter{Client: client, Decrypt: f.keys.Decrypt},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Instance: "test"}
}

func (f fixture) configureReport(t *testing.T, endpoint string, replay bool, noTrace string) {
	t.Helper()
	exec(t, f.ctx, f.owner, `
		UPDATE conversion.integrations SET report_config=report_config||
		  jsonb_build_object('endpoint',$1::text,'replay_supported',$2::boolean,'no_trace_encoding',$3::text)`,
		endpoint, replay, noTrace)
}

func (f fixture) processReport(t *testing.T, reporter partner.Reporter) {
	t.Helper()
	j, err := f.report.Claim(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := reporter.Process(f.ctx, j); err != nil {
		t.Fatal(err)
	}
}

func TestReportWorkerNoConfirmationAndLeaseRecovery(t *testing.T) {
	f := newFixture(t)
	f.ingest(t, "order-created", "unconfirmed-meta")
	if n, err := Consume(f.ctx, f.svc, 10); n != 1 || err != nil {
		t.Fatalf("normalized %d: %v", n, err)
	}
	job := f.completeMeta(t, delivery.Result{Outcome: delivery.OutcomeUnknown, Reason: "timeout"})
	exec(t, f.ctx, f.owner, "UPDATE conversion.deliveries SET deadline_at=now() WHERE id=$1", job.ID)
	if n, err := f.svc.Sweep(f.ctx, 10, time.Second); n != 1 || err != nil {
		t.Fatalf("swept %d: %v", n, err)
	}
	f.check(t, "-1:blocked:no_trace_contract_unconfirmed", "SELECT report_status||':'||state||':'||reason FROM conversion.partner_reports")
	if _, err := f.report.Claim(f.ctx); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("unconfirmed no-trace contract was claimable: %v", err)
	}
	sent := make(chan []byte, 1)
	var acknowledge atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		sent <- data
		if acknowledge.Load() {
			_, _ = io.WriteString(w, `{"code":0}`)
			return
		}
		<-r.Context().Done()
	}))
	defer server.Close()
	server.Client().Timeout = 150 * time.Millisecond
	f.configureReport(t, server.URL+"/final", false, "omit")
	exec(t, f.ctx, f.owner, "INSERT INTO admins(id,username,password_hash,role,status) VALUES('sinan-ops','sinan-ops','hash','Admin',1)")
	var reportID int64
	if err := f.owner.QueryRow(f.ctx, "SELECT id FROM conversion.partner_reports").Scan(&reportID); err != nil {
		t.Fatal(err)
	}
	operator := delivery.Operator{ID: "sinan-ops", Role: "Admin"}
	asOwner := delivery.Service{Store: delivery.Store{Pool: f.owner, Environment: TestEnvironment}}
	if _, err := asOwner.RetryPartnerReport(f.ctx, operator, reportID, "mock contract now accepts omitted trace"); err != nil {
		t.Fatal(err)
	}
	beforeSend, err := f.report.Claim(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	expire := "UPDATE conversion.partner_reports SET lease_until=now()-interval '1 second'"
	exec(t, f.ctx, f.owner, expire)
	if n, err := f.report.Sweep(f.ctx, 10); n != 1 || err != nil {
		t.Fatalf("recover before send %d: %v", n, err)
	}
	f.check(t, "pending:partner_lease_expired_before_send", "SELECT state||':'||reason FROM conversion.partner_reports")
	if err := f.report.Finish(f.ctx, beforeSend, partner.ReportResult{Outcome: partner.ReportAccepted}, false); !errors.Is(err, partner.ErrReportLeaseLost) {
		t.Fatalf("stale result accepted: %v", err)
	}
	exec(t, f.ctx, f.owner, "UPDATE conversion.partner_reports SET next_attempt_at=now()")
	f.processReport(t, f.reporter(server.Client()))
	f.check(t, "outcome_unknown:partner_transport_unconfirmed", "SELECT state||':'||reason FROM conversion.partner_reports")
	var firstPayload []byte
	select {
	case payload := <-sent:
		firstPayload = payload
		if bytes.Contains(payload, []byte("fbtrace_id")) || !bytes.Contains(payload, []byte(`"report_status":-1`)) ||
			!bytes.Contains(payload, []byte(`"message":"meta_no_confirmation"`)) {
			t.Fatalf("unexpected no-trace failure payload %s", payload)
		}
	default:
		t.Fatal("mock server did not receive the report")
	}
	if _, err := asOwner.RetryPartnerReport(f.ctx, operator, reportID, "unknown response"); !errors.Is(err, delivery.ErrOperationConflict) {
		t.Fatalf("unconfirmed replay should be blocked: %v", err)
	}

	f.configureReport(t, server.URL+"/final", true, "empty")
	if _, err := asOwner.RetryPartnerReport(f.ctx, operator, reportID, "mock confirms replay"); err != nil {
		t.Fatal(err)
	}
	afterSend, err := f.report.Claim(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(afterSend.Wire, []byte("fbtrace_id")) || len(afterSend.Wire) == 0 {
		t.Fatalf("contract edit rewrote frozen wire format: %s", afterSend.Wire)
	}
	if err := f.report.Start(f.ctx, afterSend, afterSend.Wire, true, time.Second, 24*time.Hour); err != nil {
		t.Fatal(err)
	}
	f.configureReport(t, server.URL+"/final", false, "empty")
	exec(t, f.ctx, f.owner, expire)
	if n, err := f.report.Sweep(f.ctx, 10); n != 1 || err != nil {
		t.Fatalf("recover after send %d: %v", n, err)
	}
	f.check(t, "outcome_unknown:partner_lease_expired_after_send", "SELECT state||':'||reason FROM conversion.partner_reports")
	acknowledge.Store(true)
	f.configureReport(t, server.URL+"/final", true, "empty")
	if _, err := asOwner.RetryPartnerReport(f.ctx, operator, reportID, "mock confirms replay"); err != nil {
		t.Fatal(err)
	}
	f.processReport(t, f.reporter(server.Client()))
	f.check(t, "confirmed", "SELECT state FROM conversion.partner_reports")
	if !bytes.Equal(<-sent, firstPayload) {
		t.Fatal("recovery changed the frozen no-trace payload")
	}
	f.check(t, "failed:meta_no_confirmation:1", "SELECT state||':'||reason||':'||attempts FROM conversion.deliveries WHERE id=$1", job.ID)
}

func TestReportStatusEncoding(t *testing.T) {
	const snapshot = `{"source_fields":{"app_id":"1","pixel_id":"123"},"event_id":"original","pixel_id":"123",
		"report_time":"2026-11-19T08:00:00Z","reason":"meta_no_confirmation"}`
	for _, tc := range []struct {
		status   int
		encoding string
		trace    string
		reason   string
	}{
		{0, "", "", ""},
		{-1, "", "", "no_trace_contract_unconfirmed"},
		{-1, "omit", "", ""},
		{-1, "empty", `""`, ""},
	} {
		body, reason := buildReport(tc.status, []byte(snapshot), Config{NoTrace: tc.encoding})
		if reason != tc.reason {
			t.Fatalf("encoding %d %q: %q", tc.status, tc.encoding, reason)
		}
		if reason != "" {
			continue
		}
		var payload map[string]json.RawMessage
		if err := json.Unmarshal(body, &payload); err != nil || string(payload["fbtrace_id"]) != tc.trace ||
			(tc.status == 0 && (payload["report_time"] != nil || payload["message"] != nil)) {
			t.Fatalf("invalid wire body %s: %v", body, err)
		}
	}
}

func TestReportReceiptSurvivesExpiredLease(t *testing.T) {
	f := newFixture(t)
	f.ingest(t, "order-created", "accepted-after-expiry")
	if n, err := Consume(f.ctx, f.svc, 10); n != 1 || err != nil {
		t.Fatalf("normalized %d: %v", n, err)
	}
	f.completeMeta(t, delivery.Result{Outcome: delivery.Accepted, TraceID: "original_trace"})
	var sends atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sends.Add(1)
		if _, err := f.owner.Exec(r.Context(), "UPDATE conversion.partner_reports SET lease_until=now()-interval '1 second'"); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, `{"code":0}`)
	}))
	defer server.Close()
	f.configureReport(t, server.URL+"/final", false, "")
	job, err := f.report.Claim(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.reporter(server.Client()).Process(f.ctx, job); !errors.Is(err, partner.ErrReportLeaseLost) {
		t.Fatalf("expected expiry before completion, got %v", err)
	}
	f.check(t, "200:0:true:true", `SELECT http_status||':'||business_code||':'||
		(responded_at IS NOT NULL)::text||':'||(recorded_at IS NULL)::text FROM conversion.partner_report_attempts`)
	if n, err := f.report.Sweep(f.ctx, 10); n != 1 || err != nil {
		t.Fatalf("recovered %d: %v", n, err)
	}
	f.check(t, "confirmed:1", "SELECT state||':'||attempts FROM conversion.partner_reports")
	f.check(t, "accepted:200:0", "SELECT outcome||':'||http_status||':'||business_code FROM conversion.partner_report_attempts")
	status, code, now := http.StatusOK, int64(0), time.Now()
	receipt := partner.ReportResult{Outcome: partner.ReportAccepted, HTTPStatus: &status, BusinessCode: &code, RespondedAt: &now}
	if err := f.report.RecordReceipt(f.ctx, job, receipt); !errors.Is(err, partner.ErrReportLeaseLost) {
		t.Fatalf("stale receipt returned %v", err)
	}
	if _, err := f.report.Claim(f.ctx); !errors.Is(err, pgx.ErrNoRows) || sends.Load() != 1 {
		t.Fatalf("confirmed report was available for resend: sends=%d claim=%v", sends.Load(), err)
	}
}

func TestCallbackReplayAcrossDigestVersions(t *testing.T) {
	const eventType = "recharge-paid"
	for _, version := range []string{"2026111907", "2026111908"} {
		t.Run(version, func(t *testing.T) {
			var digest [sha256.Size]byte
			f := newFixtureFrom(t, version, func(f fixture) {
				canonical := fmt.Sprintf(`{"app_id":"1","pixel_id":"123","order_id":"1001","device_id":"1002",`+
					`"event_id":"pre-upgrade","event_time":%d,"recharge_type":1,"amount":10,"currency":"USD"`, f.occurred)
				// 1907 ignored the partner network keys. 1908 included them
				// in canonical form but still wrote an unversioned digest.
				if version == "2026111908" {
					canonical += `,"client_ip_address":"192.0.2.20","client_user_agent":"fixture-agent"`
				}
				canonical += "}"
				digest = sha256.Sum256([]byte(canonical))
				snapshot := fmt.Sprintf(`{"callback":%s,"original_destination_id":1}`, canonical)
				exec(t, f.ctx, f.owner, `
					INSERT INTO conversion.inbox(integration_id,source_event_type,source_event_id,source_order_id,received_at,payload,payload_digest)
					VALUES(1,$1,'pre-upgrade','1001',now(),$2,$3)`, eventType, snapshot, digest[:])
			})
			body := fmt.Sprintf(`{"app_id":1,"pixel_id":"123","order_id":"1001","device_id":"1002","event_id":"pre-upgrade","event_time":%d,
				"recharge_type":1,"amount":10.00,"currency":"usd","client_ip":"192.0.2.20","user_agent":"fixture-agent"}`, f.occurred)
			handler := f.inbox.Handler()
			send := func(body string, status int) {
				t.Helper()
				if w := post(handler, "publisher", eventType, fixtureToken, body); w.Code != status {
					t.Fatalf("expected status %d, got %d %s", status, w.Code, w.Body.String())
				}
			}
			send(body, http.StatusOK)
			if n, err := Consume(f.ctx, f.svc, 10); err != nil || n != 1 {
				t.Fatalf("normalized %d: %v", n, err)
			}
			send(body, http.StatusOK)
			f.check(t, "1", "SELECT count(*)::text FROM conversion.events WHERE source_event_id='pre-upgrade'")
			f.check(t, "1:consumed:true", `SELECT digest_version||':'||state||':'||
				(payload_digest=$1)::text FROM conversion.inbox WHERE source_event_id='pre-upgrade'`, digest[:])
			send(strings.Replace(body, "10.00", "11.00", 1), http.StatusConflict)
			if version == "2026111908" {
				send(strings.Replace(body, "192.0.2.20", "192.0.2.21", 1), http.StatusConflict)
				send(strings.Replace(body, "fixture-agent", "other-agent", 1), http.StatusConflict)
			}

			fresh := strings.Replace(body, "pre-upgrade", "current-digest", 1)
			send(fresh, http.StatusOK)
			send(fresh, http.StatusOK)
			send(strings.Replace(fresh, "192.0.2.20", "192.0.2.21", 1), http.StatusConflict)
			send(strings.Replace(fresh, "fixture-agent", "other-agent", 1), http.StatusConflict)
			f.check(t, "2:pending", `SELECT digest_version||':'||state
				FROM conversion.inbox WHERE source_event_id='current-digest' AND state<>'quarantined'`)

			withFields := strings.Replace(body, "pre-upgrade", "current-no-network-fields", 1)
			withoutFields := strings.Split(withFields, `,"client_ip"`)[0] + "}"
			send(withoutFields, http.StatusOK)
			send(withFields, http.StatusConflict)
		})
	}
}

func TestLateReportReceiptAfterSweep(t *testing.T) {
	for _, scenario := range []struct {
		name      string
		claimNext bool
		startNext bool
	}{
		{name: "scan wins and clears the lease"},
		{name: "next attempt is claimed but unsent", claimNext: true},
		{name: "next attempt has entered sending", claimNext: true, startNext: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			f := newFixture(t)
			f.ingest(t, "order-created", "late-receipt")
			if n, err := Consume(f.ctx, f.svc, 10); n != 1 || err != nil {
				t.Fatalf("normalized %d: %v", n, err)
			}
			f.completeMeta(t, delivery.Result{Outcome: delivery.Accepted, TraceID: "original_trace"})
			f.configureReport(t, "http://127.0.0.1:8094/final", scenario.claimNext, "")
			job, err := f.report.Claim(f.ctx)
			if err != nil {
				t.Fatal(err)
			}
			wire, reason := buildReport(job.Status, job.Payload, Config{})
			if reason != "" {
				t.Fatal(reason)
			}
			if err := f.report.Start(f.ctx, job, wire, scenario.claimNext, time.Second, 24*time.Hour); err != nil {
				t.Fatal(err)
			}
			status, code, now := http.StatusOK, int64(0), time.Now()
			receipt := partner.ReportResult{Outcome: partner.ReportAccepted, HTTPStatus: &status, BusinessCode: &code, RespondedAt: &now}

			exec(t, f.ctx, f.owner, "UPDATE conversion.partner_reports SET lease_until=now()-interval '1 second'")
			if n, err := f.report.Sweep(f.ctx, 10); n != 1 || err != nil {
				t.Fatalf("swept %d: %v", n, err)
			}
			var next partner.ReportJob
			if scenario.claimNext {
				exec(t, f.ctx, f.owner, "UPDATE conversion.partner_reports SET next_attempt_at=now()")
				next, err = f.report.Claim(f.ctx)
				if err != nil {
					t.Fatal(err)
				}
				if scenario.startNext {
					if err := f.report.Start(f.ctx, next, wire, true, time.Second, 24*time.Hour); err != nil {
						t.Fatal(err)
					}
				}
			} else {
				f.check(t, "outcome_unknown:true", "SELECT state||':'||(lease_token IS NULL)::text FROM conversion.partner_reports")
			}
			forged := job
			forged.Token = "00000000-0000-0000-0000-000000000000"
			if err := f.report.RecordReceipt(f.ctx, forged, receipt); !errors.Is(err, partner.ErrReportLeaseLost) {
				t.Fatalf("invalid attempt token returned %v", err)
			}
			if err := f.report.RecordReceipt(f.ctx, job, receipt); err != nil {
				t.Fatal(err)
			}
			f.check(t, "200:0:true", `SELECT http_status||':'||business_code||':'||(responded_at IS NOT NULL)::text
				FROM conversion.partner_report_attempts WHERE attempt_no=1`)
			if scenario.startNext {
				f.check(t, "pending:2:"+next.Token, "SELECT state||':'||attempts||':'||lease_token::text FROM conversion.partner_reports")
				f.check(t, "outcome_unknown", "SELECT outcome FROM conversion.partner_report_attempts WHERE attempt_no=1")
				f.check(t, "true", "SELECT (recorded_at IS NULL AND responded_at IS NULL AND sending_at IS NOT NULL)::text FROM conversion.partner_report_attempts WHERE attempt_no=2")
				if err := f.report.Finish(f.ctx, next, receipt, false); err != nil {
					t.Fatal(err)
				}
			} else {
				f.check(t, "accepted", "SELECT outcome FROM conversion.partner_report_attempts WHERE attempt_no=1")
				if scenario.claimNext {
					f.check(t, "not_sent:superseded_by_partner_receipt", "SELECT outcome||':'||reason FROM conversion.partner_report_attempts WHERE attempt_no=2")
					if err := f.report.Start(f.ctx, next, wire, true, time.Second, 24*time.Hour); !errors.Is(err, partner.ErrReportLeaseLost) {
						t.Fatalf("superseded attempt was able to send: %v", err)
					}
				}
			}
			f.check(t, "confirmed:true", "SELECT state||':'||(lease_token IS NULL)::text FROM conversion.partner_reports")
			if _, err := f.report.Claim(f.ctx); !errors.Is(err, pgx.ErrNoRows) {
				t.Fatalf("confirmed report could be claimed: %v", err)
			}
		})
	}
}
