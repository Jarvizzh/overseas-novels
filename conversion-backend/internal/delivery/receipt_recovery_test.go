package delivery

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type receiptMedia struct {
	send Send
}

func (m receiptMedia) Prepare(Payload, string) (Send, error) { return m.send, nil }

func testWorkerReceiptRecovery(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	mustExec(t, ctx, pool, `
		INSERT INTO conversion.integrations(source,account_key,environment,contract_version,enabled)
		VALUES ('partner-fixture','receipt-fixture','receipt-test','fixture',true);
		INSERT INTO conversion.destinations(environment,media,pixel_id,credential_id)
		VALUES ('receipt-test','meta','7890',1);
		INSERT INTO conversion.integration_destinations
		SELECT i.id,d.id,i.environment,false FROM conversion.integrations i
		JOIN conversion.destinations d USING (environment) WHERE i.environment='receipt-test'
	`)
	s := Service{Store{Pool: pool, Environment: "receipt-test"}}
	for _, scenario := range []struct {
		name               string
		expireBeforeCommit bool
	}{
		{"lease expires while worker holds a response", true},
		{"result transaction rolls back", false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, `
				WITH e AS (
					INSERT INTO conversion.events(integration_id,environment,source_event_type,source_event_id,event_name,
						attribution_state,occurred_at,received_at)
					SELECT id,environment,'recharge-paid',$1,'Purchase','matched',now(),now()
					FROM conversion.integrations WHERE environment='receipt-test' RETURNING id,environment
				)
				INSERT INTO conversion.deliveries(event_id,destination_id,environment,meta_event_id,role,deadline_at)
				SELECT e.id,d.id,e.environment,$1,'original',now()+interval '1 minute'
				FROM e JOIN conversion.destinations d USING (environment)`, scenario.name); err != nil {
				t.Fatal(err)
			}
			job, err := s.Claim(ctx, true, 1, 30*time.Second, time.Second, 0)
			if err != nil {
				t.Fatal(err)
			}
			if !scenario.expireBeforeCommit {
				mustExec(t, ctx, pool, `ALTER TABLE conversion.partner_reports
					ADD CONSTRAINT reject_receipt_fixture CHECK (report_status<>1) NOT VALID`)
			}
			calls := 0
			worker := Worker{
				Service: s,
				Media: receiptMedia{send: func(sendCtx context.Context) Result {
					calls++
					if scenario.expireBeforeCommit {
						if _, err := pool.Exec(sendCtx, `UPDATE conversion.deliveries
							SET lease_until=clock_timestamp()-interval '1 second',deadline_at=clock_timestamp()
							WHERE id=$1`, job.ID); err != nil {
							t.Fatal(err)
						}
					}
					status, received := 200, 1
					return Result{Outcome: Accepted, HTTPStatus: &status, EventsReceived: &received,
						TraceID: "verified_fixture_trace", RespondedAt: time.Now().Add(-2 * time.Minute)}
				}},
				Decrypt: func(string, []byte) (string, error) { return "token", nil },
				Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)), RequestTimeout: 5 * time.Second,
			}
			if worker.process(ctx, job, time.Second) {
				t.Fatal("fixture did not interrupt primary completion")
			}
			if !scenario.expireBeforeCommit {
				mustExec(t, ctx, pool, `ALTER TABLE conversion.partner_reports DROP CONSTRAINT reject_receipt_fixture`)
				if _, err := pool.Exec(ctx, `UPDATE conversion.deliveries
					SET lease_until=clock_timestamp()-interval '1 second',deadline_at=clock_timestamp()
					WHERE id=$1`, job.ID); err != nil {
					t.Fatal(err)
				}
			}
			if count, err := s.Sweep(ctx, 10, time.Second); err != nil || count != 1 {
				t.Fatalf("recover confirmation: processed=%d err=%v", count, err)
			}
			var state, outcome, trace string
			var attempts, reportStatus, reportCount int
			err = pool.QueryRow(ctx, `
				SELECT d.state,d.attempts,a.outcome,COALESCE(a.fbtrace_id,''),r.report_status,
					(SELECT count(*) FROM conversion.partner_reports WHERE original_delivery_id=d.id)
				FROM conversion.deliveries d JOIN conversion.delivery_attempts a ON a.delivery_id=d.id
				JOIN conversion.partner_reports r ON r.original_delivery_id=d.id WHERE d.id=$1`, job.ID).
				Scan(&state, &attempts, &outcome, &trace, &reportStatus, &reportCount)
			if err != nil || state != "succeeded" || attempts != 1 || outcome != Accepted ||
				trace != "verified_fixture_trace" || reportStatus != 1 || reportCount != 1 || calls != 1 {
				t.Fatalf("state=%s attempts=%d outcome=%s trace=%s report=%d count=%d sends=%d err=%v",
					state, attempts, outcome, trace, reportStatus, reportCount, calls, err)
			}
		})
	}
}
