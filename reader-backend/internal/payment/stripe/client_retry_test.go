package stripe

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func retryTestClient(t *testing.T, maxRetries int, handle roundTripper) (*Client, *int) {
	t.Helper()
	sleeps := 0
	cfg := Config{
		Scope:         Scope{AccountID: "acct_merchant", Environment: Sandbox},
		APIKey:        "sk_test_fake",
		WebhookSecret: "whsec_fake",
		RetryPolicy: &RetryPolicy{
			MaxRetries: maxRetries,
			BaseDelay:  time.Millisecond,
			MaxDelay:   5 * time.Millisecond,
			Sleep: func(ctx context.Context, _ time.Duration) error {
				sleeps++
				return ctx.Err()
			},
		},
		HTTPClient: &http.Client{Transport: roundTripper(func(req *http.Request) (*http.Response, error) {
			if req.Method == http.MethodGet && req.URL.Path == "/v1/account" {
				return stripeResponse(200, `{"id":"acct_merchant","object":"account"}`)
			}
			return handle(req)
		})},
	}
	client, err := NewClient(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	return client, &sleeps
}

func TestStripeClientBoundedRetriesOnReadsAndIdempotentWrites(t *testing.T) {
	t.Run("pure read retries 429 and timeout up to bound then succeeds", func(t *testing.T) {
		calls := 0
		client, sleeps := retryTestClient(t, 2, func(req *http.Request) (*http.Response, error) {
			calls++
			switch calls {
			case 1:
				resp, _ := stripeResponse(http.StatusTooManyRequests, `{"error":{"type":"rate_limit_error"}}`)
				resp.Header.Set("Retry-After", "1")
				return resp, nil
			case 2:
				return nil, io.ErrUnexpectedEOF
			default:
				return stripeResponse(http.StatusOK, `{"id":"prv_ok","object":"review","open":true}`)
			}
		})
		review, err := client.GetReview(context.Background(), "prv_ok")
		if err != nil || review.ID != "prv_ok" || calls != 3 || *sleeps != 2 {
			t.Fatalf("read retry failed: review=%+v err=%v calls=%d sleeps=%d", review, err, calls, *sleeps)
		}
	})

	t.Run("idempotent write retries 429 and timeout with same key and body", func(t *testing.T) {
		calls := 0
		var seenKeys []string
		client, sleeps := retryTestClient(t, 2, func(req *http.Request) (*http.Response, error) {
			calls++
			seenKeys = append(seenKeys, req.Header.Get("Idempotency-Key"))
			if err := req.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if req.Form.Get("charge") != "ch_retry" || req.Form.Get("amount") != "500" {
				t.Fatalf("retry lost form payload on attempt %d: %v", calls, req.Form)
			}
			if calls == 1 {
				return stripeResponse(http.StatusTooManyRequests, `{"error":{"type":"rate_limit_error"}}`)
			}
			if calls == 2 {
				return nil, context.DeadlineExceeded
			}
			return stripeResponse(http.StatusOK, `{"id":"re_ok","object":"refund","status":"succeeded","amount":500,"currency":"usd","charge":"ch_retry"}`)
		})
		refund, err := client.CreateRefund(context.Background(), "ch_retry", 500, "refund-stable-key")
		if err != nil || refund.ID != "re_ok" || calls != 3 || *sleeps != 2 {
			t.Fatalf("idempotent write retry failed: refund=%+v err=%v calls=%d sleeps=%d", refund, err, calls, *sleeps)
		}
		for i, k := range seenKeys {
			if k != "refund-stable-key" {
				t.Fatalf("attempt %d used key %q, want refund-stable-key", i, k)
			}
		}
	})

	t.Run("exhausted timeout on idempotent write preserves LookupOnly disposition", func(t *testing.T) {
		calls := 0
		client, _ := retryTestClient(t, 2, func(_ *http.Request) (*http.Response, error) {
			calls++
			return nil, io.ErrUnexpectedEOF
		})
		_, err := client.ApproveReview(context.Background(), "prv_timeout", "approve-key-1")
		var apiErr *APIError
		if calls != 3 || !errors.As(err, &apiErr) || apiErr.Disposition != LookupOnly {
			t.Fatalf("exhausted timeout: calls=%d err=%v", calls, err)
		}
	})

	t.Run("non-retryable 4xx never retries", func(t *testing.T) {
		calls := 0
		client, sleeps := retryTestClient(t, 2, func(_ *http.Request) (*http.Response, error) {
			calls++
			return stripeResponse(http.StatusPaymentRequired, `{"error":{"type":"card_error","code":"card_declined"}}`)
		})
		_, err := client.CreateRefund(context.Background(), "ch_decline", 500, "refund-decline-key")
		var apiErr *APIError
		if calls != 1 || *sleeps != 0 || !errors.As(err, &apiErr) || apiErr.Disposition != Rejected {
			t.Fatalf("402 should not retry: calls=%d sleeps=%d err=%v", calls, *sleeps, err)
		}
	})

	t.Run("non-idempotent write without Idempotency-Key never retries", func(t *testing.T) {
		calls := 0
		rt := &retryRoundTripper{
			base: roundTripper(func(_ *http.Request) (*http.Response, error) {
				calls++
				return stripeResponse(http.StatusTooManyRequests, `{"error":{"type":"rate_limit_error"}}`)
			}),
			policy: RetryPolicy{
				MaxRetries: 2,
				BaseDelay:  time.Millisecond,
				MaxDelay:   5 * time.Millisecond,
				Sleep:      func(context.Context, time.Duration) error { return nil },
			},
		}
		req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://api.stripe.com/v1/refunds", strings.NewReader("charge=ch_1"))
		if err != nil {
			t.Fatal(err)
		}
		resp, err := rt.RoundTrip(req)
		if err != nil || resp.StatusCode != http.StatusTooManyRequests || calls != 1 {
			t.Fatalf("non-idempotent POST retried: calls=%d err=%v status=%v", calls, err, resp)
		}
	})
}
