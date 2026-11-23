package delivery

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func testReaderConversionReview(t *testing.T, ctx context.Context, base Service) {
	s := Service{Store{Pool: base.Pool, Environment: "reader-review"}}
	pool := s.Pool
	mustExec(t, ctx, pool, `
		INSERT INTO users(id) VALUES (1931);
		INSERT INTO conversion.integrations(source,account_key,environment,contract_version,enabled)
		VALUES ('starnovel','reader','reader-review','v1',true);
		INSERT INTO conversion.destinations(environment,media,pixel_id,credential_id)
		VALUES ('reader-review','meta','193100',1),('reader-review','meta','193101',1);
		INSERT INTO conversion.integration_destinations(integration_id,destination_id,environment)
		SELECT i.id,d.id,i.environment FROM conversion.integrations i
		JOIN conversion.destinations d ON d.environment=i.environment WHERE i.environment='reader-review';
		INSERT INTO user_subscriptions(id,user_id,subscription_id,attribution)
		VALUES (1931,1931,'I-CONVERSION-REVIEW','{"pixel_id":"193100"}');
		INSERT INTO recharge_orders(id,user_id,amount_cents,payment_method,status,fulfilled_at,order_type,subscription_id,fb_lead_metadata)
		VALUES (1931,1931,999,'paypal','Success',now(),'subscription','I-CONVERSION-REVIEW',
			'{"pixel_id":"193100","source_url":"https://example.invalid/checkout","user_agent":"fixture"}');
		SELECT record_reader_payment_conversion(1931,now(),'paypal_subscription_ledger','reader-review',true,false);
		INSERT INTO reader_conversion_outbox(business_key,event_name,environment,occurred_at,snapshot)
		SELECT 'review:'||name,name,'reader-review',now(),'{"user_id":"1931","attribution_state":"matched","attribution":{"pixel_id":"193100"}}'
		FROM unnest(ARRAY['CompleteRegistration','InitiateCheckout']) name;`)
	var integration, original, additional int64
	if err := pool.QueryRow(ctx, `SELECT i.id,min(d.id),max(d.id) FROM conversion.integrations i
		JOIN conversion.destinations d ON d.environment=i.environment WHERE i.environment='reader-review'
		GROUP BY i.id`).Scan(&integration, &original, &additional); err != nil {
		t.Fatal(err)
	}
	actor := Operator{ID: "ops"}
	setEvents := func(names ...string) {
		t.Helper()
		if _, err := s.ConfigureIntegration(ctx, actor, integration, IntegrationChange{
			AllowedEvents: &names, Reason: "set test event permissions",
		}); err != nil {
			t.Fatal(err)
		}
	}
	setEvents("Purchase")
	if _, err := s.ConfigureDestination(ctx, actor, original, DestinationChange{Retire: true, Reason: "retire original"}, nil); err != nil {
		t.Fatal(err)
	}
	if n, err := s.ConsumeReader(ctx, 10); n != 4 || err != nil {
		t.Fatalf("normalize = %d, %v", n, err)
	}
	assertString(t, ctx, pool, `SELECT count(*)::text FROM conversion.events
		WHERE environment='reader-review' AND reason='event_not_allowed'`, "3")
	assertString(t, ctx, pool, `SELECT count(*)::text FROM conversion.payment_policy_decisions
		WHERE environment='reader-review' AND payment_key='1931'`, "1")
	assertString(t, ctx, pool, `SELECT state||':'||meta_event_id FROM conversion.deliveries
		WHERE environment='reader-review'`, "pending:starnovel:payment:1931:Purchase")
	if _, err := s.ConfigureIntegration(ctx, actor, integration, IntegrationChange{
		Bindings: []IntegrationBinding{{DestinationID: original, Additional: true}}, Reason: "try to add retired route",
	}); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("add retired route: %v", err)
	}
	setEvents("Purchase", "Subscribe")
	var subscribe int64
	if err := pool.QueryRow(ctx, `SELECT id FROM conversion.events WHERE environment='reader-review'
		AND event_name='Subscribe'`).Scan(&subscribe); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendTarget(ctx, actor, subscribe, additional, "verify the frozen exclusion"); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("append excluded event: %v", err)
	}
	if _, err := s.RepairMissing(ctx, actor, RepairRequest{OrderID: 1931, EventName: "Subscribe"}); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("repair excluded event: %v", err)
	}
	mustExec(t, ctx, pool, `
        UPDATE conversion.destinations SET enabled=false WHERE environment='reader-review' AND pixel_id='193100';
		INSERT INTO recharge_orders(id,user_id,amount_cents,payment_method,status,fulfilled_at,order_type,subscription_id,fb_lead_metadata)
		VALUES (1932,1931,999,'paypal','Success',now(),'subscription','I-CONVERSION-REVIEW','{"pixel_id":"193100"}'),
		       (1933,1931,999,'paypal','Success',now(),'single',NULL,
		        '{"pixel_id":"193100","source_url":"https://example.invalid","user_agent":"fixture"}');
		SELECT record_reader_payment_conversion(1932,now(),'paypal_subscription_ledger','reader-review',false,true,'subscription_renewal');
		SELECT record_reader_payment_conversion(1933,now(),'paypal_capture_create_time','reader-review',false,false);`)
	if n, err := s.ConsumeReader(ctx, 10); n != 2 || err != nil {
		t.Fatalf("normalize after retirement = %d, %v", n, err)
	}
	assertString(t, ctx, pool, `SELECT state FROM conversion.deliveries WHERE meta_event_id='starnovel:payment:1932:Purchase'`, "blocked")
	assertString(t, ctx, pool, `SELECT state FROM conversion.deliveries WHERE meta_event_id='starnovel:payment:1933:Purchase'`, "blocked")
	mustExec(t, ctx, pool, `UPDATE conversion.destinations SET enabled=true WHERE environment='reader-review' AND pixel_id='193100'`)
	if _, err := s.ConfigureDestination(ctx, actor, original, DestinationChange{Token: "rotated", Reason: "replace test credential"},
		func(value string) ([]byte, string, error) { return []byte(value), "fixture", nil }); err != nil {
		t.Fatal(err)
	}
	var renewal int64
	if err := pool.QueryRow(ctx, `SELECT id FROM conversion.deliveries WHERE meta_event_id='starnovel:payment:1932:Purchase'`).Scan(&renewal); err != nil {
		t.Fatal(err)
	}
	if out, err := s.RetryDelivery(ctx, actor, renewal, 0, "resume the existing renewal", false); err != nil || out.State != "pending" {
		t.Fatalf("resume renewal: %+v %v", out, err)
	}
	job, err := s.Claim(ctx, true, 2, time.Minute, time.Second, 0)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := s.Payload(ctx, job)
	if err != nil || payload.CredentialID == nil || string(payload.Ciphertext) != "rotated" || payload.RouteIssue != "" {
		t.Fatalf("retired original payload: %+v %v", payload, err)
	}
	if err := s.StartSend(ctx, job, *payload.CredentialID, payload.Ciphertext, *payload.KeyVersion, time.Second); err != nil {
		t.Fatal(err)
	}
	if err := s.Complete(ctx, job, confirmedReviewReceipt(), time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RetryDelivery(ctx, actor, job.ID, 1, "confirmed event must never be resent", false); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("retry accepted delivery: %v", err)
	}
	if _, err := s.ConfigureIntegration(ctx, actor, integration, IntegrationChange{
		Bindings: []IntegrationBinding{{DestinationID: original, Remove: true}}, Reason: "revoke Reader route",
	}); err != nil {
		t.Fatal(err)
	}
	job, err = s.Claim(ctx, true, 2, time.Minute, time.Second, 0)
	if err != nil || job.ID != renewal {
		t.Fatalf("claim renewal: %+v %v", job, err)
	}
	payload, err = s.Payload(ctx, job)
	if err != nil || payload.RouteIssue != "reader_route_not_authorized" {
		t.Fatalf("revoked route: %s %v", payload.RouteIssue, err)
	}
}

func testPixelClaimReview(t *testing.T, ctx context.Context, base Service) {
	s := Service{Store{Pool: base.Pool, Environment: "claim-review"}}
	pool := s.Pool
	mustExec(t, ctx, pool, `
		INSERT INTO conversion.integrations(source,account_key,environment,contract_version,enabled)
		VALUES ('fixture','claim','claim-review','v1',true);
		INSERT INTO conversion.destinations(environment,media,pixel_id,credential_id)
		VALUES ('claim-review','meta','193110',1),('claim-review','meta','193111',1);
		INSERT INTO conversion.events(integration_id,environment,source_event_type,source_event_id,event_name,attribution_state,occurred_at,received_at)
		SELECT i.id,i.environment,'claim-fixture',n::text,
			CASE WHEN n=8 THEN 'CompleteRegistration' ELSE 'Purchase' END,'matched',now(),now()
		FROM conversion.integrations i CROSS JOIN generate_series(1,9) n WHERE i.environment='claim-review';
		INSERT INTO conversion.deliveries(event_id,destination_id,environment,meta_event_id,role,deadline_at)
		SELECT e.id,x.id,e.environment,e.source_event_id,'original',now()+interval '3 hours'
		FROM conversion.events e JOIN conversion.destinations x ON x.environment=e.environment
			AND x.pixel_id=CASE WHEN e.source_event_id='9' THEN '193111' ELSE '193110' END
		WHERE e.environment='claim-review' ORDER BY e.id;`)
	claim := func(pixel, event string) Lease {
		t.Helper()
		job, err := s.Claim(ctx, true, 2, time.Minute, time.Second, 0)
		if err != nil {
			t.Fatal(err)
		}
		payload, err := s.Payload(ctx, job)
		if err != nil || payload.PixelID != pixel || payload.EventID != event {
			t.Fatalf("claim = %s/%s, want %s/%s: %v", payload.PixelID, payload.EventID, pixel, event, err)
		}
		if err := s.StartSend(ctx, job, *payload.CredentialID, payload.Ciphertext, *payload.KeyVersion, time.Second); err != nil {
			t.Fatal(err)
		}
		return job
	}
	for _, event := range []string{"1", "2", "3", "4"} {
		job := claim("193110", event)
		if err := s.Complete(ctx, job, confirmedReviewReceipt(), time.Second); err != nil {
			t.Fatal(err)
		}
	}
	job := claim("193110", "8")
	cooldown := time.Now().Add(time.Hour)
	if err := s.Complete(ctx, job, Result{Outcome: RetryableRejection, Reason: MetaRateLimited, RetryAfter: cooldown}, time.Second); err != nil {
		t.Fatal(err)
	}
	job = claim("193111", "9")
	second := Service{Store: s.Store}
	if _, err := second.Claim(ctx, true, 2, time.Minute, time.Second, 0); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("second worker claimed a cooled or leased target: %v", err)
	}
	assertString(t, ctx, pool, `SELECT (next_claim_at>now()+interval '59 minutes')::text
		FROM conversion.destinations WHERE environment='claim-review' AND pixel_id='193110'`, "true")
	if err := s.Complete(ctx, job, confirmedReviewReceipt(), time.Second); err != nil {
		t.Fatal(err)
	}
}

type reviewSchedulerMedia func(Payload) Send

func (m reviewSchedulerMedia) Prepare(payload Payload, _ string) (Send, error) {
	return m(payload), nil
}

func testWorkerRefillsSlots(t *testing.T, ctx context.Context, base Service) {
	s := Service{Store{Pool: base.Pool, Environment: "refill-review"}}
	mustExec(t, ctx, s.Pool, `
        INSERT INTO conversion.integrations(source,account_key,environment,contract_version,enabled)
        VALUES ('fixture','refill','refill-review','v1',true);
        INSERT INTO conversion.destinations(environment,media,pixel_id,credential_id)
        VALUES ('refill-review','meta','193120',1),('refill-review','meta','193121',1);
        INSERT INTO conversion.events(integration_id,environment,source_event_type,source_event_id,event_name,attribution_state,occurred_at,received_at,amount,currency,action_source,matching_data)
        SELECT id,environment,'refill',n::text,'Purchase','matched',now(),now(),9.99,'USD','system_generated','{"external_id":["fixture"]}'
        FROM conversion.integrations CROSS JOIN generate_series(1,4) n WHERE environment='refill-review';
        INSERT INTO conversion.deliveries(event_id,destination_id,environment,meta_event_id,role,deadline_at)
        SELECT e.id,x.id,e.environment,e.source_event_id,'original',now()+interval '1 hour'
        FROM conversion.events e JOIN conversion.destinations x ON x.environment=e.environment
            AND x.pixel_id=CASE WHEN e.source_event_id='1' THEN '193120' ELSE '193121' END
        WHERE e.environment='refill-review' ORDER BY e.id;`)
	type call struct {
		pixel   string
		started time.Time
	}
	calls := make(chan call, 4)
	release := make(chan struct{})
	worker := Worker{
		Service: s, PollInterval: 5 * time.Second, PixelInterval: 150 * time.Millisecond,
		Concurrency: 2, GlobalLimit: 2, RequestTimeout: 4 * time.Second, Instance: "review",
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		Decrypt: func(string, []byte) (string, error) { return "fixture", nil },
		Media: reviewSchedulerMedia(func(p Payload) Send {
			return func(ctx context.Context) Result {
				calls <- call{p.PixelID, time.Now()}
				if p.PixelID == "193120" {
					select {
					case <-release:
					case <-ctx.Done():
					}
				}
				return confirmedReviewReceipt()
			}
		}),
	}
	workerCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		worker.Run(workerCtx)
	}()
	defer func() {
		cancel()
		close(release)
		<-done
	}()
	deadline := time.NewTimer(4 * time.Second)
	defer deadline.Stop()
	var last time.Time
	for seen := 0; seen < 3; {
		select {
		case c := <-calls:
			if c.pixel == "193121" {
				if !last.IsZero() && c.started.Sub(last) < 100*time.Millisecond {
					t.Fatalf("hot Pixel interval was only %v", c.started.Sub(last))
				}
				last = c.started
				seen++
			}
		case <-deadline.C:
			t.Fatalf("only %d hot Pixel tasks sent before the next idle poll", seen)
		}
	}
}

func confirmedReviewReceipt() Result {
	status, events := 200, 1
	return Result{Outcome: Accepted, HTTPStatus: &status, EventsReceived: &events, RespondedAt: time.Now()}
}
