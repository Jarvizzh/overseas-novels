package meta

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"conversion-backend/internal/delivery"
)

func TestSendClassification(t *testing.T) {
	cases := []struct {
		name    string
		code    int
		body    string
		outcome string
		block   bool
	}{
		{"accepted", 200, `{"events_received":1,"fbtrace_id":"actual"}`, delivery.Accepted, false},
		{"missing count", 200, `{"fbtrace_id":"actual"}`, delivery.OutcomeUnknown, false},
		{"invalid count", 200, `{"events_received":2}`, delivery.OutcomeUnknown, false},
		{"truncated", 200, `{"events_received":1`, delivery.OutcomeUnknown, false},
		{"rate limited", 429, `{"error":{"code":4,"fbtrace_id":"retry"}}`, delivery.RetryableRejection, false},
		{"ordinary transient", 503, `{"error":{"code":1,"is_transient":true}}`, delivery.RetryableRejection, false},
		{"bad token", 400, `{"error":{"code":190,"error_subcode":463}}`, delivery.PermanentRejection, true},
		{"bad parameter", 400, `{"error":{"code":100}}`, delivery.PermanentRejection, false},
		{"ambiguous upstream error", 503, `{"error":{"code":1}}`, delivery.OutcomeUnknown, false},
		{"contradictory response", 429, `{"events_received":1,"error":{"code":4}}`, delivery.OutcomeUnknown, false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.URL.Path != "/v99.0/123/events" || r.Header.Get("Authorization") != "Bearer fixture" {
					t.Errorf("unexpected Meta request")
				}
				w.Header().Set("Retry-After", "30")
				w.WriteHeader(tt.code)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()
			endpoint, err := url.Parse(server.URL + "/v99.0")
			if err != nil {
				t.Fatal(err)
			}
			c := Client{BaseURL: endpoint, HTTP: server.Client()}
			now := time.Now()
			source := "system_generated"
			send, err := c.Prepare(delivery.Payload{PixelID: "123", EventID: "event", EventName: "Subscribe",
				OccurredAt: &now, ActionSource: &source, MatchingData: []byte(`{"external_id":["abcdef"]}`)}, "fixture")
			if err != nil {
				t.Fatal(err)
			}
			r := send(context.Background())
			if r.Outcome != tt.outcome || r.Block != tt.block {
				t.Fatalf("result = %+v", r)
			}
			if tt.outcome == delivery.RetryableRejection {
				if r.RetryAfter.Before(now.Add(30 * time.Second)) {
					t.Fatal("Retry-After was ignored")
				}
				if (r.Reason == delivery.MetaRateLimited) != (tt.code == 429) {
					t.Fatalf("rate limit classification: %+v", r)
				}
			}
		})
	}
}

func TestTimeoutAfterRequestArrivalIsUnknown(t *testing.T) {
	arrived := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(arrived)
		<-release
	}))
	defer server.Close()
	defer close(release)
	endpoint, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := Client{BaseURL: endpoint, HTTP: server.Client()}
	now := time.Now()
	source := "system_generated"
	send, err := client.Prepare(delivery.Payload{PixelID: "123", EventID: "event", EventName: "Subscribe",
		OccurredAt: &now, ActionSource: &source, MatchingData: []byte(`{"external_id":["abcdef"]}`)}, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan delivery.Result, 1)
	go func() { result <- send(ctx) }()
	select {
	case <-arrived:
	case <-time.After(3 * time.Second):
		t.Fatal("request did not reach the mock endpoint")
	}
	cancel()
	select {
	case got := <-result:
		if got.Outcome != delivery.OutcomeUnknown {
			t.Fatalf("result = %+v", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("send did not return after cancellation")
	}
}

func TestBatchReceiptRequiresCompleteOrExplicitZero(t *testing.T) {
	cases := []struct {
		name, body, outcome, reason string
		status                      int
		limited                     bool
	}{
		{"complete", `{"events_received":2}`, delivery.Accepted, "", 200, false},
		{"partial", `{"events_received":1}`, delivery.OutcomeUnknown, "meta_no_confirmation", 200, false},
		{"rate limit without count", `{"error":{"code":4}}`, delivery.OutcomeUnknown, "meta_no_confirmation", 429, true},
		{"partial rate limit", `{"events_received":1,"error":{"code":4}}`, delivery.OutcomeUnknown, "meta_no_confirmation", 429, true},
		{"definite zero limit", `{"events_received":0,"error":{"code":4}}`, delivery.RetryableRejection, delivery.MetaRateLimited, 429, true},
		{"definite zero bad parameters", `{"events_received":0,"error":{"code":100}}`, delivery.RetryableRejection, delivery.MetaBatchParameterRejected, 400, false},
		{"credential fault without acceptance evidence", `{"error":{"code":190,"error_subcode":463}}`, delivery.OutcomeUnknown, "meta_no_confirmation", 400, false},
		{"credential fault with confirmed zero", `{"events_received":0,"error":{"code":190,"error_subcode":463}}`, delivery.PermanentRejection, "meta_credentials_or_permissions", 400, false},
		{"contradictory complete", `{"events_received":2,"error":{"code":100}}`, delivery.OutcomeUnknown, "meta_no_confirmation", 200, false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Retry-After", "60")
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()
			endpoint, err := url.Parse(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			c := Client{BaseURL: endpoint, HTTP: server.Client()}
			now := time.Now()
			source := "system_generated"
			p := delivery.Payload{PixelID: "123", EventName: "Subscribe", EventID: "first", OccurredAt: &now, ActionSource: &source, MatchingData: []byte(`{"external_id":["fixture"]}`)}
			sibling := p
			sibling.EventID = "second"
			send, err := c.PrepareBatch([]delivery.Payload{p, sibling}, "fixture")
			if err != nil {
				t.Fatal(err)
			}
			got := send(t.Context())
			if got.CredentialRejected() != strings.HasPrefix(tt.name, "credential fault") {
				t.Fatalf("credential signal: %+v", got)
			}
			if got.Outcome != tt.outcome || got.Reason != tt.reason || got.RateLimited != tt.limited {
				t.Fatalf("receipt: %+v", got)
			}
		})
	}
}
