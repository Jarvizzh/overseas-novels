package delivery

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func testRestrictedRoles(t *testing.T, ctx context.Context, s Service) {
	t.Helper()
	raw, err := os.ReadFile("../../../cms-backend/db/conversion_role_grants.sql")
	if err != nil {
		t.Fatal(err)
	}
	roles := map[string]string{}
	for _, kind := range []string{"reader", "cms", "worker", "api", "collector", "retention"} {
		name := pgx.Identifier{s.Pool.Config().ConnConfig.Database + "_" + kind}.Sanitize()
		roles[kind] = name
		mustExec(t, ctx, s.Pool, "CREATE ROLE "+name+" NOLOGIN")
		t.Cleanup(func() {
			// The fixture database is dropped by the enclosing test.
			mustExec(t, context.Background(), s.Pool, "DROP OWNED BY "+name+"; DROP ROLE "+name)
		})
	}
	grant := strings.TrimPrefix(string(raw), "\\set ON_ERROR_STOP on\n")
	for kind, role := range roles {
		grant = strings.ReplaceAll(grant, ":\""+kind+"_role\"", role)
	}
	mustExec(t, ctx, s.Pool, grant)
	// Baseline business privileges needed for Reader settlement are separate.
	mustExec(t, ctx, s.Pool, "GRANT SELECT ON users,third_party_payment_orders,recharge_orders TO "+roles["reader"])
	mustExec(t, ctx, s.Pool, "GRANT UPDATE (capi_expectation) ON recharge_orders TO "+roles["reader"])
	mustExec(t, ctx, s.Pool, `
		INSERT INTO recharge_orders(id,user_id,amount_cents,payment_method,status,fulfilled_at,fb_lead_metadata)
		VALUES(500,10,999,'paypal','Success',now(),
		  '{"pixel_id":"123","source_url":"https://example.invalid/checkout","user_agent":"fixture"}');
		UPDATE conversion.deliveries SET next_attempt_at=now()+interval '2 hours'
		 WHERE state='pending' AND lease_token IS NULL;
		UPDATE reader_conversion_outbox SET consumed_at=now() WHERE business_key='payment:401:Purchase';`)

	roleService := func(kind string) Service {
		config := s.Pool.Config()
		config.AfterConnect = func(ctx context.Context, c *pgx.Conn) error {
			_, err := c.Exec(ctx, "SET ROLE "+roles[kind])
			return err
		}
		pool, err := pgxpool.NewWithConfig(ctx, config)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(pool.Close)
		return Service{Store{Pool: pool, Environment: s.Environment}}
	}
	reader := roleService("reader")
	mustExec(t, ctx, reader.Pool, "SELECT record_reader_payment_conversion(500,now()-interval '10 seconds','paypal_capture_create_time','test',false,false)")
	for _, query := range []string{"SELECT ciphertext FROM conversion.credentials", "SELECT access_token FROM fb_pixels", "UPDATE conversion.deliveries SET state='succeeded'", "UPDATE reader_conversion_repair_facts SET original_send_started_at=NULL"} {
		_, err := reader.Pool.Exec(ctx, query)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
			t.Fatalf("reader privilege for %s: %v", query, err)
		}
	}
	worker := roleService("worker")
	if n, err := worker.ConsumeReader(ctx, 1); err != nil || n != 1 {
		t.Fatalf("worker normalize: %d %v", n, err)
	}
	job, err := worker.Claim(ctx, true, 5, time.Minute, time.Second, 0)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := worker.Payload(ctx, job)
	if err != nil || payload.CredentialID == nil {
		t.Fatalf("worker payload: %v", err)
	}
	if err := worker.StartSend(ctx, job, *payload.CredentialID, payload.Ciphertext, *payload.KeyVersion, time.Second); err != nil {
		t.Fatal(err)
	}
	httpStatus, received := 200, 1
	if err := worker.Complete(ctx, job, Result{Outcome: Accepted, HTTPStatus: &httpStatus, EventsReceived: &received}, time.Second); err != nil {
		t.Fatal(err)
	}
	assertString(t, ctx, s.Pool, "SELECT (original_send_started_at IS NOT NULL)::text FROM reader_conversion_repair_facts WHERE business_key='payment:500:Purchase'", "true")
	api := roleService("api")
	var eventID, targetID int64
	if err := s.Pool.QueryRow(ctx, `SELECT e.id,d.id FROM conversion.events e CROSS JOIN conversion.destinations d
		WHERE e.source_event_id='payment:500:Purchase' AND d.pixel_id='789'`).Scan(&eventID, &targetID); err != nil {
		t.Fatal(err)
	}
	if _, err := api.AppendTarget(ctx, Operator{ID: "ops", Role: "Admin"}, eventID, targetID, "permission check"); err != nil {
		t.Fatal(err)
	}
	if _, err := api.Pool.Exec(ctx, "SELECT ciphertext FROM conversion.credentials"); err == nil {
		t.Fatal("API can read credential plaintext material")
	}
	if _, err := api.Pool.Exec(ctx, "UPDATE admins SET status=2"); err == nil {
		t.Fatal("API can modify administrator records")
	}

	mustExec(t, ctx, s.Pool, `
		INSERT INTO reader_conversion_outbox(business_key,event_name,environment,created_at,consumed_at,snapshot)
		 VALUES ('expired-fixture','CompleteRegistration','test',now()-interval '45 days',now()-interval '44 days','{"email":"fixture"}');
		UPDATE reader_conversion_repair_facts SET created_at=now()-interval '45 days',occurred_at=now()-interval '45 days'
		 WHERE business_key='payment:400:Purchase';
		UPDATE conversion.deliveries SET state='failed',finalized_at=now(),lease_token=NULL,lease_until=NULL
		 WHERE event_id=(SELECT id FROM conversion.events WHERE source_event_id='payment:400:Purchase');
		UPDATE conversion.events SET created_at=now()-interval '45 days',occurred_at=now()-interval '45 days'
		 WHERE source_event_id='payment:400:Purchase';
		UPDATE conversion.delivery_diagnostics SET recorded_at=now()-interval '45 days' WHERE delivery_id=3;`)
	retention := roleService("retention")
	batch, err := retention.RedactExpired(ctx, Operator{ID: "ops"}, 30, 100, "retention fixture")
	if err != nil || batch.Outbox != 1 || batch.RepairFacts != 1 || batch.Events != 1 || batch.Diagnostics != 1 {
		t.Fatalf("redaction: %+v %v", batch, err)
	}
	assertString(t, ctx, s.Pool, "SELECT (snapshot='{}' AND original_send_started_at IS NOT NULL)::text FROM reader_conversion_repair_facts WHERE business_key='payment:400:Purchase'", "true")
	assertString(t, ctx, s.Pool, "SELECT (snapshot<>'{}' AND original_send_started_at IS NOT NULL)::text FROM reader_conversion_repair_facts WHERE business_key='payment:500:Purchase'", "true")
	assertString(t, ctx, s.Pool, "SELECT state FROM conversion.deliveries WHERE id=3", "succeeded")
	if batch, err := retention.RedactExpired(ctx, Operator{ID: "ops"}, 30, 100, "repeat"); err != nil || batch != (RetentionResult{}) {
		t.Fatalf("retention replay: %+v %v", batch, err)
	}
}
