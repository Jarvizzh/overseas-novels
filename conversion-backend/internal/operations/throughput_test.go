package operations

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"conversion-backend/internal/delivery"
	"conversion-backend/internal/media/meta"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestThroughputSample is opt-in because the fixed-duration baseline keeps a
// real PostgreSQL connection and local HTTP server open for the sample window.
func TestThroughputSample(t *testing.T) {
	if os.Getenv("CONVERSION_THROUGHPUT_SAMPLE") != "1" {
		t.Skip("set CONVERSION_THROUGHPUT_SAMPLE=1 to measure an isolated Worker")
	}
	readers := 1
	if raw := os.Getenv("CONVERSION_THROUGHPUT_NORMALIZERS"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 3 {
			t.Fatalf("normalizer sample requires 1–3 consumers, got %q", raw)
		}
		readers = n
	}
	batchSize := throughputSampleInt(t, "CONVERSION_THROUGHPUT_BATCH_SIZE", 1, delivery.MaxBatchSize)
	volume := throughputSampleInt(t, "CONVERSION_THROUGHPUT_VOLUME", 1000, 10000)
	interval := throughputSampleDuration(t, "CONVERSION_THROUGHPUT_PIXEL_INTERVAL", 3*time.Second)
	latency := throughputSampleDuration(t, "CONVERSION_THROUGHPUT_REQUEST_DELAY", 2*time.Second)
	t.Logf("Reader consumers=%d batch size=%d volume=%d Pixel interval=%s simulated response=%s",
		readers, batchSize, volume, interval, latency)
	_, pool := operationsTestPool(t)
	ctx := t.Context()
	fixture := func(environment string) delivery.Service {
		t.Helper()
		throughputExec(t, ctx, pool, `INSERT INTO conversion.integrations
			(source,account_key,environment,contract_version,enabled,allowed_events)
			VALUES ('starnovel','reader',$1,'v1',true,ARRAY['Purchase'])`, environment)
		throughputExec(t, ctx, pool, `INSERT INTO conversion.destinations(environment,media,pixel_id,credential_id)
			SELECT $1,'meta','123456',id FROM conversion.credentials WHERE key_version='throughput'`, environment)
		throughputExec(t, ctx, pool, `INSERT INTO conversion.integration_destinations(integration_id,destination_id,environment)
			SELECT i.id,x.id,i.environment FROM conversion.integrations i JOIN conversion.destinations x USING(environment)
			WHERE i.environment=$1`, environment)
		return delivery.Service{Store: delivery.Store{Pool: pool, Environment: environment}}
	}
	mustExec(t, ctx, pool, `INSERT INTO users(id) VALUES (193100);
		INSERT INTO conversion.credentials(ciphertext,key_version) VALUES ('fixture','throughput');`)
	enqueue := func(environment string, first, count int) time.Time {
		t.Helper()
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if _, err := tx.Exec(ctx, `INSERT INTO recharge_orders
			(id,user_id,amount_cents,payment_method,status,fulfilled_at,fb_lead_metadata)
			SELECT n,193100,999,'paypal','Success',now(),'{"pixel_id":"123456"}'
			FROM generate_series($1::int,$2::int) n`, first, first+count-1); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, `SELECT record_reader_payment_conversion(id,now(),'paypal_capture_create_time',$1,false,true)
			FROM recharge_orders WHERE id BETWEEN $2 AND $3`, environment, first, first+count-1); err != nil {
			t.Fatal(err)
		}
		// Include commit acknowledgement in the sample so a fast consumer
		// cannot report an HTTP start earlier than the clock measurement.
		throughputStart := time.Now()
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		return throughputStart
	}
	worker := func(s delivery.Service, paused bool, mediaClient delivery.Media) func() {
		t.Helper()
		cfg := s.Pool.Config()
		cfg.MaxConns = 16
		workerPool, err := pgxpool.NewWithConfig(ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		s.Pool = workerPool
		workerCtx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		w := delivery.Worker{
			Service: s, Media: mediaClient, Paused: paused, Instance: "throughput-fixture",
			Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
			Decrypt:     func(string, []byte) (string, error) { return "fixture", nil },
			Concurrency: 5, GlobalLimit: 5, NormalizeBatch: 20, ReaderNormalizers: readers, BatchSize: batchSize,
			PollInterval: 15 * time.Second, PixelInterval: interval, RequestTimeout: 5 * time.Second,
		}
		go func() { defer close(done); w.Run(workerCtx) }()
		return func() { cancel(); <-done; workerPool.Close() }
	}

	if os.Getenv("CONVERSION_THROUGHPUT_BATCH_RUN") == "1" {
		scenarios := []struct {
			name    string
			preload bool
		}{{"ready", true}, {"outbox", false}}
		for i, scenario := range scenarios {
			t.Run(scenario.name, func(t *testing.T) {
				environment := "throughput-batch-" + scenario.name
				s := fixture(environment)
				var started time.Time
				if scenario.preload {
					throughputExec(t, ctx, pool, `
                        WITH events AS (
                            INSERT INTO conversion.events(integration_id,environment,source_event_type,source_event_id,event_name,
                                attribution_state,occurred_at,received_at,amount,currency,action_source,matching_data)
                            SELECT a.id,a.environment,'Purchase','ready:'||n,'Purchase','matched',now(),now(),9.99,'USD',
                                'system_generated','{"external_id":["sample"]}'
                            FROM conversion.integrations a CROSS JOIN generate_series(1,$2::int) n
                            WHERE a.environment=$1 RETURNING id,environment,source_event_id
                        )
                        INSERT INTO conversion.deliveries(event_id,destination_id,environment,meta_event_id,role,deadline_at)
                        SELECT e.id,x.id,e.environment,e.source_event_id,'original',now()+interval '1 hour'
                        FROM events e JOIN conversion.destinations x ON x.environment=e.environment`, environment, volume)
				} else {
					started = enqueue(environment, 200000+i*volume, volume)
				}

				var mu sync.Mutex
				var sizes []int
				var arrivals []time.Time
				seen := make(map[string]bool, volume)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var body struct {
						Data []struct {
							ID string `json:"event_id"`
						} `json:"data"`
					}
					err := json.NewDecoder(r.Body).Decode(&body)
					if err != nil || len(body.Data) == 0 || len(body.Data) > batchSize || r.URL.Path != "/v99.0/123456/events" {
						t.Errorf("invalid simulated request: path=%s size=%d parse=%v", r.URL.Path, len(body.Data), err)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					mu.Lock()
					arrivals = append(arrivals, time.Now())
					sizes = append(sizes, len(body.Data))
					for _, e := range body.Data {
						if e.ID == "" || seen[e.ID] {
							t.Errorf("duplicate or missing Meta ID %q", e.ID)
						}
						seen[e.ID] = true
					}
					mu.Unlock()
					select {
					case <-time.After(latency):
						_, _ = fmt.Fprintf(w, `{"events_received":%d,"fbtrace_id":"batch_fixture"}`, len(body.Data))
					case <-r.Context().Done():
					}
				}))
				defer server.Close()
				endpoint, err := url.Parse(server.URL + "/v99.0")
				if err != nil {
					t.Fatal(err)
				}
				if scenario.preload {
					started = time.Now()
				}
				stop := worker(s, false, meta.Client{BaseURL: endpoint, HTTP: server.Client()})
				defer stop()
				deadline := max(60*time.Second, time.Duration(volume)*max(interval, latency)+30*time.Second)
				if batchSize == 100 && volume == 1000 && interval == 3*time.Second && latency == 2*time.Second {
					deadline = 60 * time.Second
					if scenario.preload {
						deadline = 35 * time.Second
					}
				}
				wait := time.NewTimer(deadline)
				defer wait.Stop()
				poll := time.NewTicker(100 * time.Millisecond)
				defer poll.Stop()
				for {
					var confirmed, ready, consumed int
					err := pool.QueryRow(ctx, `SELECT
                        (SELECT count(*) FROM conversion.deliveries WHERE environment=$1 AND state='succeeded'),
                        (SELECT count(*) FROM conversion.deliveries WHERE environment=$1 AND state='pending'),
                        (SELECT count(*) FROM reader_conversion_outbox WHERE environment=$1 AND consumed_at IS NOT NULL)`, environment).
						Scan(&confirmed, &ready, &consumed)
					if err != nil {
						t.Fatal(err)
					}
					if confirmed == volume && (scenario.preload || consumed == volume) {
						break
					}
					select {
					case <-wait.C:
						mu.Lock()
						t.Logf("HTTP starts elapsed from start=%v",
							func() []time.Duration {
								elapsed := make([]time.Duration, len(arrivals))
								for i, at := range arrivals {
									elapsed[i] = at.Sub(started).Round(time.Millisecond)
								}
								return elapsed
							}())
						mu.Unlock()
						t.Fatalf("capacity limit %s: consumed=%d ready=%d confirmed=%d", deadline, consumed, ready, confirmed)
					case <-poll.C:
					}
				}
				elapsed := time.Since(started)
				if elapsed > deadline {
					t.Fatalf("end-to-end confirmation took %s, limit %s", elapsed, deadline)
				}
				mu.Lock()
				defer mu.Unlock()
				expected := (volume + batchSize - 1) / batchSize
				if len(seen) != volume || (scenario.preload && len(sizes) != expected) {
					t.Fatalf("expected %d distinct events and %d ready requests, got %d events and %v", volume, expected, len(seen), sizes)
				}
				var shortest time.Duration
				for i := 1; i < len(arrivals); i++ {
					gap := arrivals[i].Sub(arrivals[i-1])
					if gap < interval {
						t.Errorf("Pixel spacing: got %s, want at least %s", gap, interval)
					}
					if shortest == 0 || gap < shortest {
						shortest = gap
					}
				}
				t.Logf("confirmed=%d requests=%d elapsed=%s shortest_gap=%s batch_sizes=%v",
					volume, len(sizes), elapsed.Round(time.Millisecond), shortest.Round(time.Millisecond), sizes)
			})
		}
		return
	}

	t.Run("normalize 1000 purchases", func(t *testing.T) {
		const environment = "throughput-normalize"
		s := fixture(environment)
		started := enqueue(environment, 193101, 1000)
		stop := worker(s, true, nil)
		defer stop()
		deadline := time.NewTimer(35 * time.Second)
		defer deadline.Stop()
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			var consumed, ready int
			err := pool.QueryRow(ctx, `SELECT
				(SELECT count(*) FROM reader_conversion_outbox WHERE environment=$1 AND consumed_at IS NOT NULL),
				(SELECT count(*) FROM conversion.deliveries WHERE environment=$1 AND state='pending')`, environment).
				Scan(&consumed, &ready)
			if err != nil {
				t.Fatal(err)
			}
			if consumed == 1000 {
				if ready != consumed {
					t.Fatalf("consumed %d but created %d ready delivery tasks", consumed, ready)
				}
				elapsed := time.Since(started)
				var decisions int
				if err := pool.QueryRow(ctx, `SELECT count(*) FROM conversion.payment_policy_decisions WHERE environment=$1`,
					environment).Scan(&decisions); err != nil || decisions != consumed {
					t.Fatalf("consumed %d, decisions %d: %v", consumed, decisions, err)
				}
				t.Logf("consumed=%d ready=%d elapsed=%v", consumed, ready, elapsed.Round(time.Millisecond))
				return
			}
			select {
			case <-deadline.C:
				t.Logf("consumed=%d ready=%d elapsed=%v", consumed, ready, time.Since(started).Round(time.Millisecond))
				return
			case <-ticker.C:
			}
		}
	})

	t.Run("cold payment HTTP start", func(t *testing.T) {
		samples := 1
		if os.Getenv("CONVERSION_THROUGHPUT_COLD_SAMPLES") == "20" {
			samples = 20
		}
		calls := make(chan time.Time, samples)
		var requests atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests.Add(1)
			calls <- time.Now()
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"events_received":1,"fbtrace_id":"throughput_fixture"}`)
		}))
		defer server.Close()
		endpoint, err := url.Parse(server.URL + "/v99.0")
		if err != nil {
			t.Fatal(err)
		}
		var delays []time.Duration
		for n := 0; n < samples; n++ {
			environment := fmt.Sprintf("throughput-cold-%d", n)
			stop := worker(fixture(environment), false, meta.Client{BaseURL: endpoint, HTTP: server.Client()})
			time.Sleep(400 * time.Millisecond) // Let both empty consumers go idle before the insert.
			started := enqueue(environment, 194200+n, 1)
			timeout := time.NewTimer(35 * time.Second)
			select {
			case at := <-calls:
				delays = append(delays, at.Sub(started))
			case <-timeout.C:
				stop()
				t.Fatal("cold payment did not start an HTTP request within the sample window")
			}
			timeout.Stop()
			stop()
			var confirmed int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM conversion.deliveries WHERE environment=$1 AND state='succeeded'`,
				environment).Scan(&confirmed); err != nil || confirmed != 1 {
				t.Fatalf("confirmed %d cold deliveries: %v", confirmed, err)
			}
		}
		sort.Slice(delays, func(i, j int) bool { return delays[i] < delays[j] })
		p95 := delays[(95*len(delays)+99)/100-1]
		if p95 > time.Second {
			t.Fatalf("cold payment HTTP P95 %v exceeds 1s", p95)
		}
		t.Logf("samples=%d requests=%d P95=%v max=%v", len(delays), requests.Load(), p95, delays[len(delays)-1].Round(time.Millisecond))
	})
}

func throughputExec(t *testing.T, ctx context.Context, pool *pgxpool.Pool, query string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(ctx, query, args...); err != nil {
		t.Fatal(err)
	}
}

func throughputSampleInt(t *testing.T, name string, fallback, maximum int) int {
	t.Helper()
	raw := os.Getenv(name)
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > maximum {
		t.Fatalf("%s must be 1–%d", name, maximum)
	}
	return n
}

func throughputSampleDuration(t *testing.T, name string, fallback time.Duration) time.Duration {
	t.Helper()
	raw := os.Getenv(name)
	if raw == "" {
		return fallback
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value < 0 {
		t.Fatalf("invalid %s", name)
	}
	return value
}
