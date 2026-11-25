package operations

import (
	"encoding/json"
	"testing"
	"time"
)

func TestMixedBatchMemberMetricsAndInboxProcessing(t *testing.T) {
	ctx, pool := operationsTestPool(t)
	mustExec(t, ctx, pool, `
		INSERT INTO conversion.integrations(environment,source,account_key,contract_version)
		VALUES ('test','starnovel','reader','v1'),('test','sinan','fixture','v1');
		INSERT INTO conversion.credentials(ciphertext,key_version) VALUES ('fixture','v1');
		INSERT INTO conversion.destinations(environment,media,pixel_id,credential_id) VALUES ('test','meta','123456',1);
		INSERT INTO conversion.events(integration_id,environment,source_event_type,source_event_id,event_name,attribution_state,occurred_at,received_at,created_at)
		SELECT CASE WHEN n=1 THEN 1 ELSE 2 END,'test','Purchase','event-'||n,
		    CASE WHEN n=1 THEN 'InitiateCheckout' ELSE 'Purchase' END,'matched',now(),now(),
		    CASE WHEN n=2 THEN now()-interval '10 minutes' ELSE now() END
		FROM generate_series(1,100) n;
		INSERT INTO conversion.deliveries(event_id,destination_id,environment,meta_event_id,role,deadline_at)
		SELECT id,1,'test',source_event_id,'original',now()+interval '1 hour' FROM conversion.events;
		INSERT INTO conversion.meta_batches(environment,destination_id,lease_until,started_at,sent_count,
		    credential_id,key_version,receipt_at,receipt,finished_at)
		VALUES ('test',1,now(),now(),100,1,'v1',now(),'{"Outcome":"accepted","HTTPStatus":200,"EventsReceived":100}',now()),
		       ('test',1,now(),now(),100,1,'v1',now(),'{"Outcome":"outcome_unknown","HTTPStatus":429,"EventsReceived":99}',now());
		INSERT INTO conversion.delivery_attempts(delivery_id,attempt_no,lease_token,batch_id,batch_position,sending_at)
		SELECT d.id,b.id,b.lease_token,b.id,row_number() OVER (PARTITION BY b.id ORDER BY d.id),b.started_at
		FROM conversion.deliveries d CROSS JOIN conversion.meta_batches b;
		INSERT INTO conversion.inbox(integration_id,source_event_type,source_event_id,payload_digest,received_at)
		VALUES (2,'recharge-paid','original','a',now()-interval '12 minutes'),
		       (2,'recharge-paid','alias','b',now()-interval '10 minutes'),
		       (2,'recharge-paid','quarantined','c',now()-interval '9 minutes'),
		       (2,'recharge-paid','historical','d',now()-interval '30 seconds');
		UPDATE conversion.inbox SET state='consumed',normalized_event_id=2 WHERE source_event_id='original';
		UPDATE conversion.inbox SET processed_at=now()-interval '10 minutes' WHERE source_event_id='original';
		UPDATE conversion.inbox SET state='consumed',normalized_event_id=2,reason='duplicate_payment_event'
		WHERE source_event_id='alias';
		UPDATE conversion.inbox SET state='quarantined',reason='invalid_snapshot' WHERE source_event_id='quarantined';
		UPDATE conversion.inbox SET state='consumed',normalized_event_id=2 WHERE source_event_id='historical';
		UPDATE conversion.inbox SET processed_at=NULL WHERE source_event_id='historical';
		UPDATE conversion.destinations SET blocked_credential_id=credential_id WHERE id=1;`)

	type requestMetrics struct {
		Requests  int
		Sent      int
		Mean      float64 `json:"mean_batch_size"`
		Limited   int     `json:"http_429"`
		Confirmed int     `json:"confirmed_members"`
		Unknown   int     `json:"unknown_deliveries"`
	}
	read := func(sql string, args ...any) requestMetrics {
		t.Helper()
		var raw []byte
		if err := pool.QueryRow(ctx, sql, args...).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var report struct{ Requests []requestMetrics }
		if err := json.Unmarshal(raw, &report); err != nil || len(report.Requests) != 1 {
			t.Fatalf("invalid request metrics: %s (%v)", raw, err)
		}
		return report.Requests[0]
	}
	for _, tc := range []struct {
		source, event  string
		account, count int
	}{
		{"", "", 0, 100}, {"starnovel", "", 0, 1}, {"sinan", "", 0, 99},
		{"", "InitiateCheckout", 0, 1}, {"", "", 2, 99},
	} {
		want := requestMetrics{Requests: 2, Sent: 2 * tc.count, Mean: 100, Limited: 1, Confirmed: tc.count, Unknown: tc.count}
		if got := read(summarySQL, "test", 5*time.Minute, tc.source, tc.event, tc.account, int64(0)); got != want {
			t.Errorf("summary filter %+v: got %+v, want %+v", tc, got, want)
		}
		if tc.event == "" && tc.account == 0 {
			if got := read(throughputSQL, "test", 5*time.Minute, tc.source, "123456"); got != want {
				t.Errorf("throughput filter %+v: got %+v, want %+v", tc, got, want)
			}
		}
	}

	var raw []byte
	if err := pool.QueryRow(ctx, throughputSQL, "test", 5*time.Minute, "sinan", "").Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var report struct {
		RecordingSince *time.Time `json:"partner_completion_recording_since"`
		Ingress        []struct {
			Processed, Received, Normalized, Merged, Quarantined, Rejected int
			Unknown                                                        int       `json:"unknown_completion_time"`
			NewEvents                                                      int       `json:"new_events"`
			Latency                                                        []float64 `json:"processing_ms_p50_p95_p99"`
		}
	}
	if err := json.Unmarshal(raw, &report); err != nil || report.RecordingSince == nil || len(report.Ingress) != 1 {
		t.Fatalf("invalid ingress metrics: %s (%v)", raw, err)
	}
	i := report.Ingress[0]
	if i.Processed != 2 || i.Received != 1 || i.Normalized != 1 || i.Merged != 1 ||
		i.Quarantined != 1 || i.Rejected != 1 || i.Unknown != 1 || i.NewEvents != 0 ||
		len(i.Latency) != 3 || i.Latency[0] < (9*time.Minute).Seconds()*1000 {
		t.Fatalf("unexpected cross-window processing metrics: %+v", i)
	}
	repo := Repository{Pool: pool, Environment: "test"}
	checkAlert := func(state string) {
		t.Helper()
		changes, err := repo.Collect(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, change := range changes {
			if change.Kind == "meta_credential_paused" && change.State == state {
				return
			}
		}
		t.Fatalf("credential alert %s missing: %+v", state, changes)
	}
	checkAlert("opened")
	mustExec(t, ctx, pool, `UPDATE conversion.destinations SET blocked_credential_id=NULL WHERE id=1`)
	checkAlert("resolved")
}
