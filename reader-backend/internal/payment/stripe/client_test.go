package stripe

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	stripego "github.com/stripe/stripe-go/v87"
	"github.com/stripe/stripe-go/v87/webhook"
)

type roundTripper func(*http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

var testConfig = Config{
	Scope:         Scope{AccountID: "acct_merchant", Environment: Sandbox},
	APIKey:        "sk_test_fake",
	WebhookSecret: "whsec_fake",
}

func stripeResponse(status int, body string) (*http.Response, error) {
	header := make(http.Header)
	header.Set("Content-Type", "application/json")
	header.Set("Request-Id", "req_fixture")
	return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body))}, nil
}

func fixtureClient(t *testing.T, handle roundTripper) *Client {
	t.Helper()
	client, err := newClient(context.Background(), testConfig, &http.Client{Transport: roundTripper(func(req *http.Request) (*http.Response, error) {
		if (req.URL.Host != "api.stripe.com" && req.URL.Host != "files.stripe.com") ||
			req.Header.Get("Authorization") != "Bearer sk_test_fake" ||
			req.Header.Get("Stripe-Version") != "2026-11-18.endive" ||
			req.Header.Get("Stripe-Account") != "" {
			t.Fatalf("unexpected Stripe request scope for %s", req.URL.Path)
		}
		if req.Method == http.MethodGet && req.URL.Path == "/v1/account" {
			return stripeResponse(200, `{"id":"acct_merchant","object":"account"}`)
		}
		if handle == nil {
			t.Fatalf("unexpected Stripe request %s %s", req.Method, req.URL.Path)
		}
		return handle(req)
	})})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestClientChecksAccountAndEnvironment(t *testing.T) {
	calls := 0
	httpClient := &http.Client{Transport: roundTripper(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.Method != http.MethodGet || req.URL.Path != "/v1/account" {
			t.Fatalf("unexpected account request: %s %s", req.Method, req.URL.Path)
		}
		return stripeResponse(200, `{"id":"acct_other"}`)
	})}
	cfg := testConfig
	cfg.APIKey = "sk_live_other"
	if _, err := newClient(context.Background(), cfg, httpClient); err == nil || calls != 0 {
		t.Fatalf("live key in sandbox produced %d requests, error %v", calls, err)
	}
	if _, err := newClient(context.Background(), testConfig, httpClient); err == nil || calls != 1 {
		t.Fatalf("wrong account produced %d requests, error %v", calls, err)
	}
}

func TestCreateCheckoutUsesModeAndStableAttemptReference(t *testing.T) {
	for _, tc := range []struct {
		name     string
		sale     SalesMode
		checkout CheckoutMode
	}{
		{"direct payment", Direct, OneTime},
		{"direct subscription", Direct, Subscription},
		{"MoR payment", MoR, OneTime},
		{"MoR subscription", MoR, Subscription},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := fixtureClient(t, func(req *http.Request) (*http.Response, error) {
				if req.Method != http.MethodPost || req.URL.Path != "/v1/checkout/sessions" {
					t.Fatalf("unexpected checkout request %s %s", req.Method, req.URL.Path)
				}
				if err := req.ParseForm(); err != nil {
					t.Fatal(err)
				}
				if req.Header.Get("Idempotency-Key") != "checkout-attempt-18" ||
					req.Form.Get("mode") != string(tc.checkout) ||
					req.Form.Get("ui_mode") != "hosted_page" ||
					req.Form.Get("line_items[0][price]") != "price_book" ||
					req.Form.Get("line_items[0][quantity]") != "1" ||
					req.Form.Get("client_reference_id") != "attempt-18" ||
					req.Form.Get("metadata[payment_attempt_id]") != "attempt-18" ||
					req.Form.Get("success_url") != "https://reader.example/return?session={CHECKOUT_SESSION_ID}" {
					t.Fatalf("incorrect checkout form: %v", req.Form)
				}
				metadata := "payment_intent_data[metadata][payment_attempt_id]"
				excluded := "subscription_data[metadata][payment_attempt_id]"
				if tc.checkout == Subscription {
					metadata, excluded = excluded, metadata
				}
				if req.Form.Get(metadata) != "attempt-18" || req.Form.Has(excluded) {
					t.Fatalf("incorrect provider metadata: %v", req.Form)
				}
				switch tc.sale {
				case MoR:
					if req.Form.Get("managed_payments[enabled]") != "true" || req.Form.Has("automatic_tax[enabled]") {
						t.Fatalf("incorrect MoR parameters: %v", req.Form)
					}
				case Direct:
					if req.Form.Has("managed_payments[enabled]") || req.Form.Get("automatic_tax[enabled]") != "true" {
						t.Fatalf("incorrect direct parameters: %v", req.Form)
					}
				}
				return stripeResponse(200, `{"id":"cs_test_session","url":"https://checkout.stripe.com/c/pay/example","status":"open","payment_status":"unpaid"}`)
			})
			result, err := client.CreateCheckout(context.Background(), CheckoutRequest{
				SalesMode: tc.sale, CheckoutMode: tc.checkout, PriceID: "price_book",
				AttemptID: "attempt-18", IdempotencyKey: "checkout-attempt-18", AutomaticTax: tc.sale == Direct,
				SuccessURL: "https://reader.example/return?session={CHECKOUT_SESSION_ID}",
				CancelURL:  "https://reader.example/cancel",
			})
			if err != nil || result.ID != "cs_test_session" || result.PaymentStatus != "unpaid" || result.URL == "" {
				t.Fatalf("unexpected checkout result: %v, error %v", result, err)
			}
		})
	}
}

func TestStripeWriteFailureDispositions(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   int
		body     string
		want     Disposition
		wantCode string
		wantPI   string
	}{
		{"rate limited", 429, `{"error":{"type":"invalid_request_error"}}`, RetrySameKey, "", ""},
		{"concurrent request", 409, `{"error":{"type":"invalid_request_error","code":"idempotency_key_in_use"}}`, RetrySameKey, "idempotency_key_in_use", ""},
		{"conflicting parameters", 400, `{"error":{"type":"idempotency_error"}}`, LookupOnly, "", ""},
		{"provider failure", 500, `{"error":{"type":"api_error"}}`, LookupOnly, "", ""},
		{"lost response", 0, "", LookupOnly, "", ""},
		{"decline", 402, `{"error":{"type":"card_error","code":"card_declined","decline_code":"generic_decline","payment_intent":{"id":"pi_original"},"message":"private customer info"}}`, Rejected, "card_declined", "pi_original"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			client := fixtureClient(t, func(req *http.Request) (*http.Response, error) {
				calls++
				if err := req.ParseForm(); err != nil {
					t.Fatal(err)
				}
				if req.Method != http.MethodPost || req.URL.Path != "/v1/refunds" ||
					req.Header.Get("Idempotency-Key") != "refund-r1" ||
					req.Form.Get("charge") != "ch_original" || req.Form.Get("amount") != "550" {
					t.Fatalf("incorrect refund request %s %s %v", req.Method, req.URL.Path, req.Form)
				}
				if tc.status == 0 {
					return nil, io.ErrUnexpectedEOF
				}
				return stripeResponse(tc.status, tc.body)
			})
			if _, err := client.CreateRefund(context.Background(), "ch_original", 550, ""); err == nil || calls != 0 {
				t.Fatalf("missing idempotency key produced %d requests, error %v", calls, err)
			}
			_, err := client.CreateRefund(context.Background(), "ch_original", 550, "refund-r1")
			var providerErr *APIError
			if calls != 1 || !errors.As(err, &providerErr) || providerErr.Disposition != tc.want ||
				providerErr.Status != tc.status || providerErr.Code != tc.wantCode ||
				providerErr.PaymentIntentID != tc.wantPI || strings.Contains(err.Error(), "private customer info") {
				t.Fatalf("wrong failure result after %d calls: %+v", calls, err)
			}
		})
	}
}

func TestInvoicePaymentsPreserveAllocationsAndPage(t *testing.T) {
	calls := 0
	client := fixtureClient(t, func(req *http.Request) (*http.Response, error) {
		calls++
		query := req.URL.Query()
		if req.Method != http.MethodGet || req.URL.Path != "/v1/invoice_payments" ||
			query.Get("invoice") != "in_period" || query.Get("limit") != "3" ||
			query.Get("starting_after") != "inpay_previous" {
			t.Fatalf("incorrect invoice payment query: %s %s", req.Method, req.URL)
		}
		return stripeResponse(200, `{"object":"list","has_more":true,"data":[
			{"id":"inpay_open","invoice":"in_period","currency":"cad","amount_paid":null,"amount_requested":900,"status":"open"},
			{"id":"inpay_zero","invoice":"in_period","currency":"cad","amount_paid":0,"amount_requested":300,"status":"paid",
			 "payment":{"type":"payment_record","payment_record":"payrec_external"}},
			{"id":"inpay_part","invoice":"in_period","currency":"cad","amount_paid":550,"amount_requested":900,"status":"paid",
			 "payment":{"type":"payment_intent","payment_intent":"pi_part"}}
		]}`)
	})
	page, err := client.ListInvoicePayments(context.Background(), "in_period", PageOptions{Limit: 3, StartingAfter: "inpay_previous"})
	if err != nil || calls != 1 || !page.HasMore || len(page.Items) != 3 {
		t.Fatalf("unexpected allocation page after %d calls: %v, error %v", calls, page, err)
	}
	open, zero, part := page.Items[0], page.Items[1], page.Items[2]
	if open.AmountPaid != nil || zero.AmountPaid == nil || *zero.AmountPaid != 0 ||
		zero.Payment.PaymentRecord.ID != "payrec_external" ||
		part.AmountPaid == nil || *part.AmountPaid != 550 || part.AmountRequested != 900 ||
		part.Payment.PaymentIntent.ID != "pi_part" || part.Currency != "cad" {
		t.Fatalf("lost invoice payment evidence: %+v", page.Items)
	}
}

func TestBalanceTransactionKeepsRateAndRefundSource(t *testing.T) {
	client := fixtureClient(t, func(req *http.Request) (*http.Response, error) {
		const record = `{"id":"txn_refund","source":"re_original","type":"refund","amount":-550,"fee":31,"net":-581,"currency":"cad","exchange_rate":1.234567890123456789}`
		switch req.URL.Path {
		case "/v1/balance_transactions/txn_refund":
			return stripeResponse(200, record)
		case "/v1/balance_transactions":
			if req.URL.Query().Get("payout") != "po_batch" {
				t.Fatalf("incorrect reconciliation query: %s", req.URL)
			}
			return stripeResponse(200, `{"object":"list","data":[`+record+`,{"id":"txn_fee","exchange_rate":null}]}`)
		default:
			t.Fatalf("unexpected balance transaction request: %s", req.URL)
			return nil, nil
		}
	})
	txn, err := client.GetBalanceTransaction(context.Background(), "txn_refund")
	if err != nil || txn.ExchangeRate == nil || txn.ExchangeRate.String() != "1.234567890123456789" ||
		txn.Source.ID != "re_original" || txn.Amount != -550 || txn.Net != -581 || txn.Currency != "cad" {
		t.Fatalf("lost balance evidence: %+v, error %v", txn, err)
	}
	page, err := client.ListBalanceTransactions(context.Background(), BalanceTransactionQuery{PayoutID: "po_batch"})
	if err != nil || len(page.Items) != 2 || page.Items[0].ExchangeRate == nil ||
		page.Items[0].ExchangeRate.String() != txn.ExchangeRate.String() || page.Items[1].ExchangeRate != nil {
		t.Fatalf("unexpected balance page: %+v, error %v", page, err)
	}
}

func TestVerifyWebhookChecksRawBytesVersionAndScope(t *testing.T) {
	client := fixtureClient(t, nil)
	for _, tc := range []struct {
		name        string
		version     string
		account     string
		live, stale bool
		tamper      bool
	}{
		{name: "direct event", version: APIVersion},
		{name: "account event", version: APIVersion, account: "acct_merchant"},
		{name: "wrong account", version: APIVersion, account: "acct_other"},
		{name: "wrong environment", version: APIVersion, live: true},
		{name: "wrong exact version", version: "2026-11-19.endive"},
		{name: "expired signature", version: APIVersion, stale: true},
		{name: "changed bytes", version: APIVersion, tamper: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now()
			body := []byte(fmt.Sprintf(
				`{"id":"evt_test","type":"charge.succeeded","object":"event","created":%d,"api_version":%q,"account":%q,"livemode":%t,"data":{"object":{"id":"ch_original"}}}`,
				now.Unix(), tc.version, tc.account, tc.live))
			signingTime := now
			if tc.stale {
				signingTime = now.Add(-10 * time.Minute)
			}
			signed := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{
				Payload: body, Secret: testConfig.WebhookSecret, Timestamp: signingTime,
			})
			if tc.tamper {
				body = append(body, ' ')
			}
			event, err := client.VerifyWebhook(body, signed.Header)
			switch tc.name {
			case "direct event", "account event":
				if err != nil || event.Scope != testConfig.Scope || event.ID != "evt_test" ||
					event.Type != stripego.EventTypeChargeSucceeded || event.ObjectID != "ch_original" {
					t.Fatalf("invalid parsed event: %+v, error %v", event, err)
				}
			default:
				if err == nil {
					t.Fatal("invalid signed event accepted")
				}
			}
		})
	}
}

func TestCancelSubscriptionKeepsPaidPeriod(t *testing.T) {
	client := fixtureClient(t, func(req *http.Request) (*http.Response, error) {
		if err := req.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if req.Method != http.MethodPost || req.URL.Path != "/v1/subscriptions/sub_vip" ||
			req.Form.Get("cancel_at_period_end") != "true" ||
			req.Header.Get("Idempotency-Key") != "cancel-vip-at-renewal" {
			t.Fatalf("incorrect subscription update: %s %s %v", req.Method, req.URL, req.Form)
		}
		return stripeResponse(200, `{"id":"sub_vip","status":"active","cancel_at_period_end":true}`)
	})
	sub, err := client.CancelSubscriptionAtPeriodEnd(context.Background(), "sub_vip", "cancel-vip-at-renewal")
	if err != nil || !sub.CancelAtPeriodEnd || sub.Status != stripego.SubscriptionStatusActive {
		t.Fatalf("unexpected subscription result: %v, error %v", sub, err)
	}
}

func TestCreateReportRunIncludesIntervalAndTaxBreakdown(t *testing.T) {
	client := fixtureClient(t, func(req *http.Request) (*http.Response, error) {
		if err := req.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if req.Method != http.MethodPost || req.URL.Path != "/v1/reporting/report_runs" ||
			req.Form.Get("report_type") != "payout_reconciliation.itemized.7" ||
			req.Form.Get("parameters[interval_start]") != "1795132800" ||
			req.Form.Get("parameters[interval_end]") != "1795219200" ||
			req.Form.Get("parameters[columns][0]") != "withheld_tax" ||
			req.Form.Get("parameters[columns][1]") != "fee_net_of_withheld_tax" ||
			req.Header.Get("Idempotency-Key") != "report-mor-nov20" {
			t.Fatalf("incorrect report request: %s %s %v", req.Method, req.URL, req.Form)
		}
		return stripeResponse(200, `{"id":"frr_nov20","status":"pending"}`)
	})
	run, err := client.CreateReportRun(context.Background(), ReportRequest{
		Type:           "payout_reconciliation.itemized.7",
		Interval:       TimeRange{From: time.Unix(1795132800, 0), Before: time.Unix(1795219200, 0)},
		Columns:        []string{"withheld_tax", "fee_net_of_withheld_tax"},
		IdempotencyKey: "report-mor-nov20",
	})
	if err != nil || run.ID != "frr_nov20" || run.Status != stripego.ReportingReportRunStatusPending {
		t.Fatalf("unexpected report run: %v, error %v", run, err)
	}
}

func TestVerifyWebhookAcceptsBalanceEventWithoutObjectID(t *testing.T) {
	client := fixtureClient(t, nil)
	now := time.Now()
	signed := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{
		Payload: []byte(fmt.Sprintf(
			`{"id":"evt_balance","type":"balance.available","object":"event","created":%d,"api_version":%q,"livemode":false,"data":{"object":{"object":"balance","available":[]}}}`,
			now.Unix(), APIVersion)),
		Secret: testConfig.WebhookSecret, Timestamp: now,
	})
	event, err := client.VerifyWebhook(signed.Payload, signed.Header)
	if err != nil || event.ID != "evt_balance" || event.ObjectID != "" {
		t.Fatalf("unexpected balance event: %v, error %v", event, err)
	}
}
