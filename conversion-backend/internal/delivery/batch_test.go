package delivery

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func testBatchDelivery(t *testing.T, ctx context.Context, base Service) {
	fixture := func(t *testing.T, environment string, count int) Service {
		t.Helper()
		s := Service{Store{Pool: base.Pool, Environment: environment}}
		execBatchFixture(t, ctx, s, `INSERT INTO conversion.integrations(source,account_key,environment,contract_version,enabled)
			VALUES('fixture','batch',$1,'v1',true)`, environment)
		execBatchFixture(t, ctx, s, `INSERT INTO conversion.destinations(environment,media,pixel_id,credential_id)
			VALUES($1,'meta','123456',1),($1,'meta','234567',1)`, environment)
		execBatchFixture(t, ctx, s, `
			WITH events AS (
				INSERT INTO conversion.events(integration_id,environment,source_event_type,source_event_id,event_name,
				    attribution_state,occurred_at,received_at,amount,currency,action_source,matching_data)
				SELECT i.id,i.environment,'Payment',n::text,'Purchase','matched',now(),now(),9.99,'USD',
				    'system_generated','{"external_id":["fixture"]}'
				FROM conversion.integrations i CROSS JOIN generate_series(1,$2::int) n WHERE i.environment=$1
				RETURNING id,environment,source_event_id
			)
			INSERT INTO conversion.deliveries(event_id,destination_id,environment,meta_event_id,role,deadline_at)
			SELECT e.id,x.id,e.environment,e.source_event_id,'original',now()+interval '1 hour'
			FROM events e JOIN conversion.destinations x ON e.environment=x.environment AND x.pixel_id='123456'
			ORDER BY e.id`, environment, count)
		return s
	}
	claim := func(t *testing.T, s Service, limit, batchSize int) BatchLease {
		t.Helper()
		job, err := s.ClaimBatch(ctx, limit, batchSize, time.Minute, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		return job
	}
	start := func(t *testing.T, s Service, job BatchLease, want int, key string) {
		t.Helper()
		send, err := s.StartBatch(ctx, job, time.Second, 0,
			func(version string, _ []byte) (string, error) { return version, nil },
			func(members []Payload, token string) (Send, error) {
				if len(members) != want || (key != "" && token != key) {
					return nil, fmt.Errorf("got %d members and key version %q, expected %d/%q", len(members), token, want, key)
				}
				return func(context.Context) Result { return Result{} }, nil
			})
		if err != nil || send == nil {
			t.Fatalf("start %d members: %v", want, err)
		}
	}
	count := func(t *testing.T, s Service, query string, wanted int) {
		t.Helper()
		var got int
		err := s.Pool.QueryRow(ctx, query, s.Environment).Scan(&got)
		if err != nil || got != wanted {
			t.Fatalf("%s: got %d want %d: %v", query, got, wanted, err)
		}
	}
	receipt := func(t *testing.T, s Service, b BatchLease, r Result) {
		t.Helper()
		if err := s.PersistBatchReceipt(ctx, b, r, 0); err != nil {
			t.Fatal(err)
		}
	}
	finish := func(t *testing.T, s Service, b BatchLease) {
		t.Helper()
		if err := s.FinalizeBatch(ctx, b, time.Second); err != nil {
			t.Fatal(err)
		}
	}
	response := func(outcome, reason string, status, received int) Result {
		return Result{Outcome: outcome, Reason: reason, HTTPStatus: &status, EventsReceived: &received, RespondedAt: time.Now(), TraceID: "fixture_trace"}
	}

	t.Run("101 members and recovery of durable evidence", func(t *testing.T) {
		s := fixture(t, "batch-boundary", 101)
		job := claim(t, s, 5, 100)
		if _, err := s.ClaimBatch(ctx, 5, 100, time.Minute, time.Second); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("second request for the same Pixel: %v", err)
		}
		start(t, s, job, 100, "")
		receipt(t, s, job, response(Accepted, "", 200, 100))
		if repaired, err := s.Sweep(ctx, 5, time.Second); repaired != 1 || err != nil {
			t.Fatalf("recovery of committed receipt: %d %v", repaired, err)
		}
		finish(t, s, job)
		job = claim(t, s, 5, 100)
		start(t, s, job, 1, "")
		receipt(t, s, job, response(Accepted, "", 200, 1))
		finish(t, s, job)
		count(t, s, `SELECT count(*) FROM conversion.deliveries WHERE environment=$1 AND state='succeeded'`, 101)
		count(t, s, `SELECT count(*) FROM conversion.delivery_attempts a JOIN conversion.deliveries d ON d.id=a.delivery_id
			WHERE d.environment=$1 AND a.outcome='accepted' AND a.batch_id IS NOT NULL AND a.events_received IS NULL`, 101)
		count(t, s, `SELECT count(*) FROM conversion.partner_reports r JOIN conversion.deliveries d ON d.id=r.original_delivery_id
			WHERE d.environment=$1 AND r.report_status=1 AND r.payload->>'fbtrace_id'='fixture_trace'`, 101)
	})
	t.Run("partial response remains unknown and cools Pixel", func(t *testing.T) {
		s := fixture(t, "batch-partial", 4)
		job := claim(t, s, 5, 3)
		start(t, s, job, 3, "")
		r := response(OutcomeUnknown, "meta_no_confirmation", 429, 2)
		r.RateLimited, r.RetryAfter = true, time.Now().Add(time.Hour)
		receipt(t, s, job, r)
		finish(t, s, job)
		count(t, s, `SELECT count(*) FROM conversion.deliveries WHERE environment=$1 AND state='outcome_unknown'`, 3)
		count(t, s, `SELECT count(*) FROM conversion.meta_batches WHERE environment=$1 AND receipt->>'EventsReceived'='2'`, 1)
		if _, err := s.ClaimBatch(ctx, 5, 100, time.Minute, time.Second); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("a fresh batch on a cooled Pixel: %v", err)
		}
	})
	t.Run("shared request slots and independent Pixel cooldown", func(t *testing.T) {
		s := fixture(t, "batch-slots", 6)
		execBatchFixture(t, ctx, s, `
            UPDATE conversion.deliveries d SET destination_id=x.id
            FROM conversion.destinations x WHERE x.environment=d.environment AND x.pixel_id='234567'
              AND d.environment=$1 AND d.meta_event_id IN ('5','6')`, s.Environment)
		job := claim(t, s, 1, 3)
		second := Service{Store: s.Store}
		if _, err := second.ClaimBatch(ctx, 1, 3, time.Minute, time.Second); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("second Worker bypassed request limit: %v", err)
		}
		start(t, s, job, 3, "")
		execBatchFixture(t, ctx, s, `UPDATE conversion.deliveries SET deadline_at=now()+interval '3 hours' WHERE environment=$1`, s.Environment)
		limited := response(RetryableRejection, MetaRateLimited, 429, 0)
		limited.RateLimited, limited.RetryAfter = true, time.Now().Add(time.Hour)
		receipt(t, s, job, limited)
		finish(t, s, job)
		job = claim(t, second, 1, 100)
		start(t, second, job, 2, "")
		receipt(t, second, job, response(Accepted, "", 200, 2))
		finish(t, second, job)
		if _, err := second.ClaimBatch(ctx, 1, 100, time.Minute, time.Second); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("rapid retry during Pixel cooldown: %v", err)
		}
		count(t, s, `SELECT count(*) FROM conversion.deliveries WHERE environment=$1 AND state='succeeded'`, 2)
		count(t, s, `SELECT count(*) FROM conversion.deliveries WHERE environment=$1 AND state='pending' AND reason='meta_rate_limited'`, 3)
	})
	for _, laterSuccessFirst := range []bool{true, false} {
		t.Run(fmt.Sprintf("newer success wins over late credential fault success_first=%t", laterSuccessFirst), func(t *testing.T) {
			s := fixture(t, fmt.Sprintf("batch-credential-order-%t", laterSuccessFirst), 3)
			old := claim(t, s, 5, 1)
			start(t, s, old, 1, "")
			execBatchFixture(t, ctx, s, `UPDATE conversion.meta_batches SET lease_until=now() WHERE id=$1`, old.ID)
			if swept, err := s.Sweep(ctx, 5, time.Second); err != nil || swept != 1 {
				t.Fatalf("expire old batch: %d, %v", swept, err)
			}
			newer := claim(t, s, 5, 1)
			start(t, s, newer, 1, "")
			ok := response(Accepted, "", 200, 1)
			fault := response(OutcomeUnknown, "meta_no_confirmation", 400, 0)
			code, subcode := 190, 463
			fault.EventsReceived, fault.GraphCode, fault.GraphSubcode = nil, &code, &subcode
			if laterSuccessFirst {
				receipt(t, s, newer, ok)
			}
			receipt(t, s, old, fault)
			if !laterSuccessFirst {
				receipt(t, s, newer, ok)
			}
			count(t, s, `SELECT count(*) FROM conversion.destinations
			    WHERE environment=$1 AND blocked_credential_id=credential_id`, 0)
			finish(t, s, newer)

			next := claim(t, s, 5, 1)
			start(t, s, next, 1, "")
			receipt(t, s, next, ok)
			finish(t, s, next)
			count(t, s, `SELECT count(*) FROM conversion.deliveries
			    WHERE environment=$1 AND meta_event_id='1' AND state='outcome_unknown' AND attempts=1`, 1)
			count(t, s, `SELECT count(*) FROM conversion.deliveries
			    WHERE environment=$1 AND meta_event_id IN ('2','3') AND state='succeeded'`, 2)
			count(t, s, `SELECT count(*) FROM conversion.meta_batches
			    WHERE environment=$1 AND receipt IS NULL AND late_receipt->>'GraphSubcode'='463'`, 1)
		})
	}

	t.Run("older success cannot clear a newer credential fault", func(t *testing.T) {
		s := fixture(t, "batch-credential-newer-fault", 3)
		old := claim(t, s, 5, 1)
		start(t, s, old, 1, "")
		execBatchFixture(t, ctx, s, `UPDATE conversion.meta_batches SET lease_until=now() WHERE id=$1`, old.ID)
		if swept, err := s.Sweep(ctx, 5, time.Second); err != nil || swept != 1 {
			t.Fatalf("expire old batch: %d, %v", swept, err)
		}
		newer := claim(t, s, 5, 1)
		start(t, s, newer, 1, "")
		fault := response(PermanentRejection, "meta_credentials_or_permissions", 400, 0)
		code := 190
		fault.GraphCode, fault.Block = &code, true
		receipt(t, s, newer, fault)
		finish(t, s, newer)
		receipt(t, s, old, response(Accepted, "", 200, 1))
		if _, err := s.ClaimBatch(ctx, 5, 1, time.Minute, time.Second); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("older success cleared a newer credential fault: %v", err)
		}
		count(t, s, `SELECT count(*) FROM conversion.deliveries WHERE environment=$1 AND meta_event_id='1' AND state='outcome_unknown'`, 1)
		count(t, s, `SELECT count(*) FROM conversion.deliveries WHERE environment=$1 AND meta_event_id='2' AND state='blocked'`, 1)
	})

	for _, size := range []int{1, 100} {
		t.Run(fmt.Sprintf("credential failure fences the Pixel at batch %d", size), func(t *testing.T) {
			s := fixture(t, fmt.Sprintf("credential-fault-%d", size), size+3)
			execBatchFixture(t, ctx, s, `UPDATE conversion.deliveries d SET destination_id=x.id
                FROM conversion.destinations x WHERE x.environment=d.environment AND x.pixel_id='234567'
                  AND d.environment=$1 AND d.meta_event_id=$2`, s.Environment, fmt.Sprint(size+3))
			job := claim(t, s, 5, size)
			start(t, s, job, size, "")
			fault := response(OutcomeUnknown, "meta_no_confirmation", 400, 0)
			code, subcode := 190, 463
			fault.EventsReceived, fault.GraphCode, fault.GraphSubcode = nil, &code, &subcode
			receipt(t, s, job, fault)
			finish(t, s, job)
			count(t, s, `SELECT count(*) FROM conversion.deliveries WHERE environment=$1 AND state='outcome_unknown'`, size)
			other := Service{Store: s.Store}
			survivor := claim(t, other, 5, size)
			start(t, other, survivor, 1, "")
			receipt(t, other, survivor, response(Accepted, "", 200, 1))
			finish(t, other, survivor)
			if _, err := other.ClaimBatch(ctx, 5, size, time.Minute, time.Second); !errors.Is(err, pgx.ErrNoRows) {
				t.Fatalf("old credential was still eligible: %v", err)
			}
			// Rotate while another old response is outstanding. The original
			// fault is retained in its ledger and cannot affect the new Token.
			execBatchFixture(t, ctx, s, `
                UPDATE conversion.destinations SET blocked_credential_id=NULL
                WHERE environment=$1 AND pixel_id='123456'`, s.Environment)
			late := claim(t, s, 5, 1)
			start(t, s, late, 1, "")
			execBatchFixture(t, ctx, s, `UPDATE conversion.meta_batches SET lease_until=now() WHERE id=$1`, late.ID)
			if _, err := other.Sweep(ctx, 5, time.Second); err != nil {
				t.Fatal(err)
			}
			var destination int64
			if err := s.Pool.QueryRow(ctx, `SELECT id FROM conversion.destinations WHERE environment=$1 AND pixel_id='123456'`,
				s.Environment).Scan(&destination); err != nil {
				t.Fatal(err)
			}
			_, err := s.ConfigureDestination(ctx, Operator{ID: "ops"}, destination, DestinationChange{Token: "fixture-rotated",
				Reason: "rotate the test credential"}, func(string) ([]byte, string, error) { return []byte("fixture"), "fault-rotated", nil })
			if err != nil {
				t.Fatal(err)
			}
			fault.RateLimited, fault.RetryAfter = true, time.Now().Add(time.Hour)
			receipt(t, other, late, fault)
			fresh := claim(t, other, 5, size)
			start(t, other, fresh, 1, "fault-rotated")
			receipt(t, other, fresh, response(Accepted, "", 200, 1))
			finish(t, other, fresh)
			count(t, s, `SELECT count(*) FROM conversion.deliveries WHERE environment=$1 AND state='outcome_unknown'`, size+1)
			count(t, s, `SELECT count(*) FROM conversion.meta_batches WHERE environment=$1 AND late_receipt->>'GraphCode'='190'`, 1)
		})
	}

	for _, size := range []int{1, 100} {
		t.Run(fmt.Sprintf("aged registrations yield to a purchase at batch %d", size), func(t *testing.T) {
			s := fixture(t, fmt.Sprintf("aged-fairness-%d", size), 105)
			execBatchFixture(t, ctx, s, `UPDATE conversion.events SET event_name='CompleteRegistration'
                WHERE environment=$1 AND source_event_id<>'105';
            `, s.Environment)
			execBatchFixture(t, ctx, s, `UPDATE conversion.deliveries SET next_attempt_at=now()-interval '2 minutes'
                WHERE environment=$1`, s.Environment)
			first := claim(t, s, 5, size)
			start(t, s, first, size, "")
			receipt(t, s, first, response(Accepted, "", 200, size))
			finish(t, s, first)
			if size == 1 {
				restarted := Service{Store: s.Store}
				second := claim(t, restarted, 5, size)
				start(t, restarted, second, size, "")
				receipt(t, restarted, second, response(Accepted, "", 200, size))
				finish(t, restarted, second)
			}
			count(t, s, `SELECT count(*) FROM conversion.deliveries WHERE environment=$1 AND meta_event_id='105' AND state='succeeded'`, 1)
		})
	}

	t.Run("fairness counts members across batches", func(t *testing.T) {
		s := fixture(t, "batch-fairness", 10)
		execBatchFixture(t, ctx, s, `UPDATE conversion.events SET event_name='CompleteRegistration'
            WHERE environment=$1 AND source_event_id IN ('9','10')`, s.Environment)
		job := claim(t, s, 5, 4)
		start(t, s, job, 4, "")
		receipt(t, s, job, response(Accepted, "", 200, 4))
		finish(t, s, job)
		job = claim(t, s, 5, 1)
		start(t, s, job, 1, "")
		count(t, s, `SELECT count(*) FROM conversion.delivery_attempts a
            JOIN conversion.deliveries d ON d.id=a.delivery_id JOIN conversion.events e ON e.id=d.event_id
            WHERE d.environment=$1 AND a.recorded_at IS NULL AND e.event_name='CompleteRegistration'`, 1)
		receipt(t, s, job, response(Accepted, "", 200, 1))
		finish(t, s, job)
	})

	t.Run("preflight keeps good rows and uses the current credential", func(t *testing.T) {
		s := fixture(t, "batch-preflight", 4)
		job := claim(t, s, 5, 100)
		execBatchFixture(t, ctx, s, `INSERT INTO conversion.credentials(ciphertext,key_version) VALUES ('rotated','rotated-fixture')`)
		execBatchFixture(t, ctx, s, `UPDATE conversion.destinations SET credential_id=(SELECT id FROM conversion.credentials WHERE key_version='rotated-fixture')
			WHERE environment=$1`, s.Environment)
		execBatchFixture(t, ctx, s, `UPDATE conversion.events SET matching_data='{}' WHERE environment=$1 AND source_event_id='2'`, s.Environment)
		execBatchFixture(t, ctx, s, `UPDATE conversion.deliveries SET deadline_at=now() WHERE environment=$1 AND meta_event_id='3'`, s.Environment)
		execBatchFixture(t, ctx, s, `UPDATE conversion.deliveries SET meta_event_id='1' WHERE environment=$1 AND meta_event_id='4'`, s.Environment)
		start(t, s, job, 1, "rotated-fixture")
		receipt(t, s, job, response(Accepted, "", 200, 1))
		finish(t, s, job)
		count(t, s, `SELECT count(*) FROM conversion.deliveries WHERE environment=$1 AND state='failed'`, 3)
		count(t, s, `SELECT count(*) FROM conversion.deliveries WHERE environment=$1 AND state='succeeded'`, 1)
	})
	t.Run("zero-received parameter error reduces member caps", func(t *testing.T) {
		s := fixture(t, "batch-split", 4)
		job := claim(t, s, 5, 100)
		start(t, s, job, 4, "")
		receipt(t, s, job, response(RetryableRejection, MetaBatchParameterRejected, 400, 0))
		finish(t, s, job)
		count(t, s, `SELECT count(*) FROM conversion.deliveries WHERE environment=$1 AND state='pending' AND max_batch_size=2`, 4)
		execBatchFixture(t, ctx, s, `UPDATE conversion.deliveries SET next_attempt_at=now() WHERE environment=$1`, s.Environment)
		job = claim(t, s, 5, 100)
		start(t, s, job, 2, "")
		receipt(t, s, job, response(Accepted, "", 200, 2))
		finish(t, s, job)
	})
	t.Run("expired fence and late receipt", func(t *testing.T) {
		s := fixture(t, "batch-expiry", 2)
		job := claim(t, s, 5, 100)
		execBatchFixture(t, ctx, s, `UPDATE conversion.meta_batches SET lease_until=now() WHERE id=$1`, job.ID)
		if _, err := s.Sweep(ctx, 5, time.Second); err != nil {
			t.Fatal(err)
		}
		count(t, s, `SELECT count(*) FROM conversion.deliveries WHERE environment=$1 AND state='pending' AND lease_until IS NULL`, 2)
		execBatchFixture(t, ctx, s, `UPDATE conversion.deliveries SET next_attempt_at=now() WHERE environment=$1`, s.Environment)
		job = claim(t, s, 5, 100)
		start(t, s, job, 2, "")
		execBatchFixture(t, ctx, s, `UPDATE conversion.meta_batches SET lease_until=now() WHERE id=$1`, job.ID)
		if _, err := s.Sweep(ctx, 5, time.Second); err != nil {
			t.Fatal(err)
		}
		receipt(t, s, job, response(Accepted, "", 200, 2))
		count(t, s, `SELECT count(*) FROM conversion.meta_batches WHERE environment=$1 AND receipt IS NULL AND late_receipt->>'EventsReceived'='2'`, 1)
		count(t, s, `SELECT count(*) FROM conversion.deliveries WHERE environment=$1 AND state='outcome_unknown'`, 2)
	})
}

func execBatchFixture(t *testing.T, ctx context.Context, s Service, query string, args ...any) {
	t.Helper()
	if _, err := s.Pool.Exec(ctx, query, args...); err != nil {
		t.Fatal(err)
	}
}
