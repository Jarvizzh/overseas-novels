package stripe

import (
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"reader-backend/internal/payment"
)

func TestResolveStripeRefundEligibilityMatrix(t *testing.T) {
	t.Run("refundable inquiry allows remaining balance and stop-loss full remaining", func(t *testing.T) {
		var createCalls atomic.Int32
		client := fixtureClient(t, func(req *http.Request) (*http.Response, error) {
			switch {
			case req.Method == http.MethodGet && req.URL.Path == "/v1/charges/ch_inq_ok":
				return stripeResponse(200, `{"id":"ch_inq_ok","object":"charge","currency":"usd","livemode":false,"paid":true,"captured":true,"amount_captured":1000,"amount_refunded":300,"disputed":true}`)
			case req.Method == http.MethodGet && req.URL.Path == "/v1/disputes":
				return stripeResponse(200, `{"object":"list","has_more":false,"data":[{"id":"du_inq_ok","object":"dispute","charge":"ch_inq_ok","livemode":false,"status":"warning_needs_response","is_charge_refundable":true,"currency":"usd","amount":700}]}`)
			case req.Method == http.MethodPost && req.URL.Path == "/v1/refunds":
				createCalls.Add(1)
				return stripeResponse(200, `{"id":"re_inq_ok","object":"refund","charge":"ch_inq_ok","currency":"usd","amount":700,"status":"succeeded","metadata":{"payment_refund_command_id":"cmd-inq-ok"}}`)
			default:
				t.Fatalf("unexpected request %s %s", req.Method, req.URL.Path)
				return nil, nil
			}
		})
		proof, err := client.ResolveStripeRefund(t.Context(), payment.RefundRequest{
			Account:               client.AccountScope(),
			ReceiptID:             "ch_inq_ok",
			RequestKey:            "cmd-inq-ok",
			AmountMinor:           700,
			Currency:              "USD",
			Send:                  true,
			StopLossFullRemaining: true,
		})
		if err != nil {
			t.Fatalf("expected refundable inquiry to succeed, got err=%v", err)
		}
		if proof == nil || proof.ID != "re_inq_ok" || proof.AmountMinor != 700 || createCalls.Load() != 1 {
			t.Fatalf("unexpected proof=%+v createCalls=%d", proof, createCalls.Load())
		}
	})

	t.Run("non-refundable inquiry routes to dispute workflow without creating refund", func(t *testing.T) {
		client := fixtureClient(t, func(req *http.Request) (*http.Response, error) {
			switch {
			case req.Method == http.MethodGet && req.URL.Path == "/v1/charges/ch_inq_no":
				return stripeResponse(200, `{"id":"ch_inq_no","object":"charge","currency":"usd","livemode":false,"paid":true,"captured":true,"amount_captured":1000,"amount_refunded":0,"disputed":true}`)
			case req.Method == http.MethodGet && req.URL.Path == "/v1/disputes":
				return stripeResponse(200, `{"object":"list","has_more":false,"data":[{"id":"du_inq_no","object":"dispute","charge":"ch_inq_no","livemode":false,"status":"warning_needs_response","is_charge_refundable":false,"currency":"usd","amount":1000}]}`)
			default:
				t.Fatalf("unexpected request %s %s", req.Method, req.URL.Path)
				return nil, nil
			}
		})
		_, err := client.ResolveStripeRefund(t.Context(), payment.RefundRequest{
			Account:     client.AccountScope(),
			ReceiptID:   "ch_inq_no",
			RequestKey:  "cmd-inq-no",
			AmountMinor: 1000,
			Currency:    "USD",
			Send:        true,
		})
		if !errors.Is(err, payment.ErrStripeRefundNonRefundableInquiry) {
			t.Fatalf("err=%v, want ErrStripeRefundNonRefundableInquiry", err)
		}
	})

	t.Run("formal open dispute routes to dispute workflow", func(t *testing.T) {
		client := fixtureClient(t, func(req *http.Request) (*http.Response, error) {
			switch {
			case req.Method == http.MethodGet && req.URL.Path == "/v1/charges/ch_formal":
				return stripeResponse(200, `{"id":"ch_formal","object":"charge","currency":"usd","livemode":false,"paid":true,"captured":true,"amount_captured":1000,"amount_refunded":0,"disputed":true}`)
			case req.Method == http.MethodGet && req.URL.Path == "/v1/disputes":
				return stripeResponse(200, `{"object":"list","has_more":false,"data":[{"id":"du_formal","object":"dispute","charge":"ch_formal","livemode":false,"status":"needs_response","is_charge_refundable":false,"currency":"usd","amount":1000}]}`)
			default:
				t.Fatalf("unexpected request %s %s", req.Method, req.URL.Path)
				return nil, nil
			}
		})
		_, err := client.ResolveStripeRefund(t.Context(), payment.RefundRequest{
			Account:     client.AccountScope(),
			ReceiptID:   "ch_formal",
			RequestKey:  "cmd-formal",
			AmountMinor: 1000,
			Currency:    "USD",
			Send:        true,
		})
		if !errors.Is(err, payment.ErrStripeRefundOpenDispute) {
			t.Fatalf("err=%v, want ErrStripeRefundOpenDispute", err)
		}
	})

	t.Run("won but not yet refundable stays pending verification then succeeds when refundable", func(t *testing.T) {
		var refundable atomic.Bool
		client := fixtureClient(t, func(req *http.Request) (*http.Response, error) {
			switch {
			case req.Method == http.MethodGet && req.URL.Path == "/v1/charges/ch_won":
				return stripeResponse(200, `{"id":"ch_won","object":"charge","currency":"usd","livemode":false,"paid":true,"captured":true,"amount_captured":1000,"amount_refunded":0,"disputed":true}`)
			case req.Method == http.MethodGet && req.URL.Path == "/v1/disputes":
				flag := "false"
				if refundable.Load() {
					flag = "true"
				}
				return stripeResponse(200, `{"object":"list","has_more":false,"data":[{"id":"du_won","object":"dispute","charge":"ch_won","livemode":false,"status":"won","is_charge_refundable":`+flag+`,"currency":"usd","amount":1000}]}`)
			case req.Method == http.MethodPost && req.URL.Path == "/v1/refunds":
				return stripeResponse(200, `{"id":"re_won","object":"refund","charge":"ch_won","currency":"usd","amount":1000,"status":"succeeded","metadata":{"payment_refund_command_id":"cmd-won"}}`)
			default:
				t.Fatalf("unexpected request %s %s", req.Method, req.URL.Path)
				return nil, nil
			}
		})
		req := payment.RefundRequest{
			Account:     client.AccountScope(),
			ReceiptID:   "ch_won",
			RequestKey:  "cmd-won",
			AmountMinor: 1000,
			Currency:    "USD",
			Send:        true,
		}
		if _, err := client.ResolveStripeRefund(t.Context(), req); !errors.Is(err, payment.ErrStripeRefundWonNotYetRefundable) {
			t.Fatalf("first attempt err=%v, want ErrStripeRefundWonNotYetRefundable", err)
		}
		refundable.Store(true)
		proof, err := client.ResolveStripeRefund(t.Context(), req)
		if err != nil || proof == nil || proof.ID != "re_won" {
			t.Fatalf("second attempt proof=%+v err=%v", proof, err)
		}
	})

	t.Run("conflicting evidence disputed charge with empty disputes keeps verification pending", func(t *testing.T) {
		client := fixtureClient(t, func(req *http.Request) (*http.Response, error) {
			switch {
			case req.Method == http.MethodGet && req.URL.Path == "/v1/charges/ch_conflict":
				return stripeResponse(200, `{"id":"ch_conflict","object":"charge","currency":"usd","livemode":false,"paid":true,"captured":true,"amount_captured":1000,"amount_refunded":0,"disputed":true}`)
			case req.Method == http.MethodGet && req.URL.Path == "/v1/disputes":
				return stripeResponse(200, `{"object":"list","has_more":false,"data":[]}`)
			default:
				t.Fatalf("unexpected request %s %s", req.Method, req.URL.Path)
				return nil, nil
			}
		})
		_, err := client.ResolveStripeRefund(t.Context(), payment.RefundRequest{
			Account:     client.AccountScope(),
			ReceiptID:   "ch_conflict",
			RequestKey:  "cmd-conflict",
			AmountMinor: 1000,
			Currency:    "USD",
			Send:        true,
		})
		if !errors.Is(err, payment.ErrStripeRefundVerificationPending) {
			t.Fatalf("err=%v, want ErrStripeRefundVerificationPending", err)
		}
	})

	t.Run("lost response after remote success recovers via preserved idempotency key", func(t *testing.T) {
		var created atomic.Bool
		client := fixtureClient(t, func(req *http.Request) (*http.Response, error) {
			switch {
			case req.Method == http.MethodGet && req.URL.Path == "/v1/charges/ch_lost_resp":
				return stripeResponse(200, `{"id":"ch_lost_resp","object":"charge","currency":"usd","livemode":false,"paid":true,"captured":true,"amount_captured":1000,"amount_refunded":0,"disputed":false}`)
			case req.Method == http.MethodPost && req.URL.Path == "/v1/refunds":
				created.Store(true)
				return nil, errors.New("connection reset after remote write")
			case req.Method == http.MethodGet && req.URL.Path == "/v1/refunds":
				if !strings.Contains(req.URL.RawQuery, "charge=ch_lost_resp") {
					t.Fatalf("expected charge filter, got %s", req.URL.RawQuery)
				}
				return stripeResponse(200, `{"object":"list","has_more":false,"data":[{"id":"re_recovered","object":"refund","charge":"ch_lost_resp","currency":"usd","amount":1000,"status":"succeeded","metadata":{"payment_refund_command_id":"cmd-lost-resp"}}]}`)
			default:
				t.Fatalf("unexpected request %s %s", req.Method, req.URL.Path)
				return nil, nil
			}
		})
		req := payment.RefundRequest{
			Account:     client.AccountScope(),
			ReceiptID:   "ch_lost_resp",
			RequestKey:  "cmd-lost-resp",
			AmountMinor: 1000,
			Currency:    "USD",
			Send:        true,
		}
		if _, err := client.ResolveStripeRefund(t.Context(), req); err == nil {
			t.Fatal("expected network error on initial send")
		}
		if !created.Load() {
			t.Fatal("expected create call to have executed")
		}
		// Retry with Send=false (simulating worker retry after first_sent_at was set).
		req.Send = false
		proof, err := client.ResolveStripeRefund(t.Context(), req)
		if err != nil || proof == nil || proof.ID != "re_recovered" || proof.CommandKey != "cmd-lost-resp" {
			t.Fatalf("recovery proof=%+v err=%v", proof, err)
		}
	})
}
