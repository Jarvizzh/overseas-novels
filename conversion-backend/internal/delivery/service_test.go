package delivery

import (
	"context"
	"errors"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDurableDeliveryLifecycle(t *testing.T) {
	dsn := os.Getenv("STAR_NOVEL_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires disposable PostgreSQL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	database := "conversion_test_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	name := pgx.Identifier{database}.Sanitize()
	mustExec(t, ctx, admin, "CREATE DATABASE "+name)
	defer mustExec(t, context.Background(), admin, "DROP DATABASE "+name+" WITH (FORCE)")
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.Database = name[1 : len(name)-1]
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	for _, asset := range []string{"schema.sql", "migrations/2026111702.sql", "migrations/2026111703.sql",
		"migrations/2026111704.sql", "migrations/2026111901.sql", "migrations/2026111902.sql", "migrations/2026111903.sql", "migrations/2026111904.sql", "migrations/2026111905.sql", "migrations/2026111906.sql", "migrations/2026111907.sql", "migrations/2026111908.sql", "migrations/2026111909.sql", "migrations/2026111910.sql", "migrations/2026111911.sql", "migrations/2026111912.sql", "migrations/2026111913.sql", "migrations/2026111914.sql", "migrations/2026111915.sql", "migrations/2026111916.sql", "migrations/2026111917.sql", "migrations/2026111918.sql", "migrations/2026111919.sql", "migrations/2026111920.sql", "migrations/2026111921.sql", "migrations/2026111922.sql", "migrations/2026111923.sql", "migrations/2026111924.sql", "migrations/2026111925.sql", "migrations/2026111926.sql", "migrations/2026111927.sql", "migrations/2026111928.sql"} {
		sql, err := os.ReadFile("../../../cms-backend/db/" + asset)
		if err != nil {
			t.Fatal(err)
		}
		mustExec(t, ctx, pool, string(sql))
	}
	s := Service{Store{Pool: pool, Environment: "test"}}
	if err := s.CheckSchema(ctx); err != nil {
		t.Fatal(err)
	}
	mustExec(t, ctx, pool, `
		INSERT INTO conversion.integrations(source,account_key,environment,contract_version,enabled,allowed_apps,allowed_events,report_config)
		VALUES ('sinan','a','test','sinan-mock-v1',true,ARRAY['10'],ARRAY['recharge-paid'],'{"app_pixels":{"10":["123","456"]}}');
		INSERT INTO conversion.credentials(ciphertext,key_version) VALUES ('token','fixture');
		INSERT INTO conversion.destinations(environment,media,pixel_id,credential_id)
		VALUES ('test','meta','123',1),('test','meta','456',1);
		INSERT INTO conversion.integration_destinations SELECT 1, id, environment, false FROM conversion.destinations`)

	now := time.Now()
	event := Event{IntegrationID: 1, SourceType: "recharge-paid", SourceID: "9223372036854775807",
		Name: "Purchase", AttributionState: "matched", OccurredAt: &now, ReceivedAt: now,
		PartnerFields: []byte(`{"app_id":"10","pixel_id":"123"}`)}
	targets := []Target{{DestinationID: 1, Original: true, MetaEventID: "original"}, {DestinationID: 2, MetaEventID: "additional"}}
	for range 2 {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if id, err := s.EnqueueTx(ctx, tx, event, targets); err != nil || id != 1 {
			_ = tx.Rollback(ctx)
			t.Fatalf("enqueue = %d, %v", id, err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}

	results := make(chan Lease, 6)
	var wg sync.WaitGroup
	for range cap(results) {
		wg.Go(func() {
			job, err := s.Claim(ctx, true, 2, time.Minute, time.Second, 0)
			if err == nil {
				results <- job
			} else if !errors.Is(err, pgx.ErrNoRows) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	close(results)
	jobs := make(map[int64]Lease)
	for job := range results {
		jobs[job.ID] = job
	}
	if len(jobs) != 2 {
		t.Fatalf("claimed jobs: %v", jobs)
	}

	startSend := func(job Lease) {
		t.Helper()
		if err := s.StartSend(ctx, job, 1, []byte("token"), "fixture", time.Second); err != nil {
			t.Fatal(err)
		}
	}
	startSend(jobs[1])
	mustExec(t, ctx, pool, "UPDATE conversion.deliveries SET lease_until = now() - interval '1 second'")
	if count, err := s.Sweep(ctx, 10, time.Second); err != nil || count != 2 {
		t.Fatalf("lease recovery = %d, %v", count, err)
	}
	assertString(t, ctx, pool, "SELECT state FROM conversion.deliveries WHERE id=1", "outcome_unknown")
	assertString(t, ctx, pool, "SELECT state FROM conversion.deliveries WHERE id=2", "pending")
	assertString(t, ctx, pool, "SELECT count(*)::text FROM conversion.partner_reports", "0")

	status, count := 200, 1
	accepted := Result{Outcome: Accepted, HTTPStatus: &status, EventsReceived: &count, TraceID: "actual_trace"}
	if err := s.Complete(ctx, jobs[1], accepted, time.Second); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("late result = %v", err)
	}
	if err := s.Diagnostic(ctx, jobs[1], accepted); err != nil {
		t.Fatal(err)
	}
	mustExec(t, ctx, pool, "UPDATE conversion.deliveries SET next_attempt_at=now() WHERE id=2")
	additional, err := s.Claim(ctx, true, 2, time.Minute, time.Second, 0)
	if err != nil || additional.ID != 2 || additional.Attempt != 2 {
		t.Fatalf("reclaimed = %+v, %v", additional, err)
	}
	startSend(additional)
	if err := s.Complete(ctx, additional, accepted, time.Second); err != nil {
		t.Fatal(err)
	}
	assertString(t, ctx, pool, "SELECT count(*)::text FROM conversion.partner_reports", "0")
	mustExec(t, ctx, pool, "UPDATE conversion.deliveries SET deadline_at=now() WHERE id=1")
	if count, err := s.Sweep(ctx, 10, time.Second); err != nil || count != 1 {
		t.Fatalf("deadline recovery = %d, %v", count, err)
	}
	assertString(t, ctx, pool, "SELECT state||':'||reason FROM conversion.deliveries WHERE id=1", "failed:meta_no_confirmation")
	assertString(t, ctx, pool, "SELECT report_status||':'||state||':'||(payload->>'pixel_id') FROM conversion.partner_reports", "-1:blocked:123")
	assertString(t, ctx, pool, "SELECT count(*)::text FROM conversion.delivery_diagnostics WHERE late", "1")

	// Force a failure in the terminal transaction and verify that both writes roll back.
	event.SourceID = "second"
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnqueueTx(ctx, tx, event, targets[:1]); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	job, err := s.Claim(ctx, true, 2, time.Minute, time.Second, 0)
	if err != nil {
		t.Fatal(err)
	}
	startSend(job)
	mustExec(t, ctx, pool, "ALTER TABLE conversion.partner_reports ADD CONSTRAINT reject_success_fixture CHECK (report_status <> 1)")
	if err := s.Complete(ctx, job, accepted, time.Second); err == nil {
		t.Fatal("expected terminal transaction to fail")
	}
	assertString(t, ctx, pool, "SELECT state FROM conversion.deliveries WHERE id=3", "pending")
	assertString(t, ctx, pool, "SELECT count(*)::text FROM conversion.delivery_attempts WHERE delivery_id=3 AND outcome IS NOT NULL", "0")
	if err := pool.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&accepted.RespondedAt); err != nil {
		t.Fatal(err)
	}
	if err := s.Diagnostic(ctx, job, accepted); err != nil {
		t.Fatal(err)
	}
	mustExec(t, ctx, pool, "ALTER TABLE conversion.partner_reports DROP CONSTRAINT reject_success_fixture")
	if _, err := pool.Exec(ctx, "UPDATE conversion.deliveries SET lease_until=$2,deadline_at=now() WHERE id=$1",
		job.ID, accepted.RespondedAt); err != nil {
		t.Fatal(err)
	}
	if swept, err := s.Sweep(ctx, 10, time.Second); err != nil || swept != 1 {
		t.Fatalf("recover persisted confirmation = %d, %v", swept, err)
	}
	assertString(t, ctx, pool, "SELECT attempts::text FROM conversion.deliveries WHERE id=3", "1")

	assertString(t, ctx, pool, "SELECT report_status||':'||(payload->>'fbtrace_id') FROM conversion.partner_reports WHERE original_delivery_id=3", "1:actual_trace")
	mustExec(t, ctx, pool, "ALTER TABLE conversion.delivery_diagnostics RENAME TO unavailable_diagnostics")
	if err := s.Diagnostic(ctx, job, accepted); err == nil {
		t.Fatal("expected diagnostic write failure")
	}
	assertString(t, ctx, pool, "SELECT state FROM conversion.deliveries WHERE id=3", "succeeded")
	mustExec(t, ctx, pool, "ALTER TABLE conversion.unavailable_diagnostics RENAME TO delivery_diagnostics")
	mustExec(t, ctx, pool, `
		INSERT INTO conversion.integrations(source,account_key,environment,contract_version,enabled)
		VALUES ('starnovel','reader','test','v1',true);
		INSERT INTO conversion.integration_destinations(integration_id,destination_id,environment,default_route)
		VALUES(2,1,'test',false),(2,2,'test',true);
		INSERT INTO system_domains(name,domain,status) VALUES ('test','example.invalid',1);
		INSERT INTO reader_conversion_outbox(business_key,event_name,environment,occurred_at,snapshot)
		VALUES ('registration:10:CompleteRegistration','CompleteRegistration','test',now(),
			'{"user_id":"10","email":" User@Example.Invalid ","attribution_state":"matched","action_source":"website","attribution":{"pixel_id":"123","user_agent":"fixture","source_url":"https://example.invalid/reader?credential=secret#fragment"}}'),
		       ('invalid','CompleteRegistration','test',now(),'{}'),
		       ('off-environment','CompleteRegistration','production',now(),'{}')`)
	if count, err := s.ConsumeReader(ctx, 10); err != nil || count != 2 {
		t.Fatalf("reader normalization = %d, %v", count, err)
	}
	if count, err := s.ConsumeReader(ctx, 10); err != nil || count != 0 {
		t.Fatalf("reader normalization replay = %d, %v", count, err)
	}
	assertString(t, ctx, pool, "SELECT source_url FROM conversion.events WHERE integration_id=2", "https://example.invalid/reader")
	assertString(t, ctx, pool, "SELECT count(*)::text FROM conversion.deliveries d JOIN conversion.events e ON e.id=d.event_id WHERE e.integration_id=2", "2")
	assertString(t, ctx, pool, "SELECT processing_error FROM reader_conversion_outbox WHERE business_key='invalid'", "invalid_reader_snapshot")
	assertString(t, ctx, pool, "SELECT (consumed_at IS NULL)::text FROM reader_conversion_outbox WHERE business_key='off-environment'", "true")

	t.Run("R01 worker receipt recovery", func(t *testing.T) { testWorkerReceiptRecovery(t, ctx, pool) })
	t.Run("legacy cutover", func(t *testing.T) {
		testLegacyCutover(t, ctx, s)
	})
	t.Run("audited operations", func(t *testing.T) { testAuditedOperations(t, ctx, s) })
	t.Run("legacy evidence review", func(t *testing.T) { testLegacyReview(t, ctx, s) })
	t.Run("restricted roles and retention", func(t *testing.T) { testRestrictedRoles(t, ctx, s) })
	t.Run("reader payment policy", func(t *testing.T) { testReaderPaymentPolicy(t, ctx, s) })

}

func mustExec(t *testing.T, ctx context.Context, pool *pgxpool.Pool, sql string) {
	t.Helper()
	if _, err := pool.Exec(ctx, sql); err != nil {
		t.Fatal(err)
	}
}

func assertString(t *testing.T, ctx context.Context, pool *pgxpool.Pool, sql, want string) {
	t.Helper()
	var got string
	if err := pool.QueryRow(ctx, sql).Scan(&got); err != nil || got != want {
		t.Fatalf("%s: got %q, want %q, err %v", sql, got, want, err)
	}
}
