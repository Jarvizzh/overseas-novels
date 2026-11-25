package stripe

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	stripego "github.com/stripe/stripe-go/v87"
	"github.com/stripe/stripe-go/v87/webhook"
)

func eventFixture(version, eventType, object string) string {
	return fmt.Sprintf(
		`{"id":"evt_fixture","object":"event","api_version":%q,"type":%q,"created":1795132800,"livemode":false,"request":{"id":"req_event","idempotency_key":"attempt-event"},"data":{"object":%s}}`,
		version, eventType, object)
}

func TestEventBackfillRetainsOriginalVersionAndRequestKey(t *testing.T) {
	client := fixtureClient(t, func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/v1/events/evt_fixture":
			return stripeResponse(200, eventFixture("2025-03-31.basil", "invoice.paid", `{"id":"in_original","object":"invoice"}`))
		case "/v1/events":
			q := req.URL.Query()
			if q.Get("types[0]") != "review.opened" || q.Get("created[gte]") != "1795132800" ||
				q.Get("created[lt]") != "1795219200" || q.Get("delivery_success") != "false" ||
				q.Get("starting_after") != "evt_previous" {
				t.Fatalf("unexpected Stripe event query: %s", req.URL)
			}
			body := eventFixture(APIVersion, "review.opened", `{"id":"prv_open","object":"review","open":true,"charge":"ch_paid","reason":"rule"}`)
			return stripeResponse(200, `{"object":"list","has_more":true,"data":[`+body+`]}`)
		default:
			t.Fatalf("unexpected event request: %s %s", req.Method, req.URL)
			return nil, nil
		}
	})
	ctx := context.Background()
	event, err := client.GetEvent(ctx, "evt_fixture")
	if err != nil || event.Source != EventFromAPI || event.APIVersion != "2025-03-31.basil" ||
		event.ObjectID != "in_original" || event.RequestID != "req_event" || event.IdempotencyKey != "attempt-event" {
		t.Fatalf("missing historical event data: %+v, error %v", event, err)
	}
	if _, err := event.DecodeObject(); !errors.Is(err, ErrEventVersion) {
		t.Fatalf("expected an explicit version error for an old event, got %v", err)
	}
	delivered := false
	page, err := client.ListEvents(ctx, EventQuery{
		Created: TimeRange{From: time.Unix(1795132800, 0), Before: time.Unix(1795219200, 0)},
		Types:   []string{"review.opened"}, DeliverySuccess: &delivered,
		PageOptions: PageOptions{StartingAfter: "evt_previous"},
	})
	if err != nil || !page.HasMore || len(page.Items) != 1 {
		t.Fatalf("unexpected event backfill page: %+v, error %v", page, err)
	}
	signal, err := page.Items[0].RiskSignal()
	if err != nil || signal.Scope != testConfig.Scope || signal.SourceID != "prv_open" ||
		signal.Review == nil || !signal.Review.Open || signal.Review.Charge.ID != "ch_paid" {
		t.Fatalf("unexpected risk evidence: %+v, error %v", signal, err)
	}
}

func TestListEventsPreservesIncompleteSnapshotWithVerifiedScope(t *testing.T) {
	client := fixtureClient(t, func(_ *http.Request) (*http.Response, error) {
		incomplete := eventFixture(APIVersion, "invoice.paid", `null`)
		valid := strings.Replace(eventFixture(APIVersion, "invoice.paid", `{"id":"in_Paid","object":"invoice"}`),
			"evt_fixture", "evt_valid", 1)
		return stripeResponse(200, `{"object":"list","has_more":true,"data":[`+incomplete+`,`+valid+`]}`)
	})
	start := time.Unix(1795132800, 0)
	page, err := client.ListEvents(t.Context(), EventQuery{Created: TimeRange{From: start, Before: start.Add(time.Hour)}})
	if err != nil || !page.HasMore || len(page.Items) != 2 || page.Items[0].ID != "evt_fixture" {
		t.Fatalf("event page: %+v %v", page, err)
	}
	if _, err := client.PaymentNotice(page.Items[0]); !errors.Is(err, errIncompleteSnapshot) {
		t.Fatalf("incomplete snapshot error: %v", err)
	}
	if notice, err := client.PaymentNotice(page.Items[1]); err != nil || notice == nil || notice.InvoiceID != "in_Paid" {
		t.Fatalf("healthy event: %+v %v", notice, err)
	}
}

func TestVerifiedEventsDecodeChannelEvidence(t *testing.T) {
	client := fixtureClient(t, nil)
	cases := []struct {
		eventType string
		object    string
		verify    func(*testing.T, Event, any)
	}{
		{
			"radar.early_fraud_warning.created",
			`{"id":"issfr_after","object":"radar.early_fraud_warning","actionable":true,"charge":"ch_paid","fraud_type":"made_with_stolen_card"}`,
			func(t *testing.T, event Event, decoded any) {
				signal, err := event.RiskSignal()
				warning, ok := decoded.(*stripego.RadarEarlyFraudWarning)
				if !ok || err != nil || signal.EarlyFraudWarning == nil || !warning.Actionable || warning.Charge.ID != "ch_paid" {
					t.Fatalf("wrong early warning evidence: %+v, error %v", decoded, err)
				}
			},
		},
		{
			"payment_intent.payment_failed",
			`{"id":"pi_failed","object":"payment_intent","last_payment_error":{"type":"card_error","code":"card_declined","decline_code":"generic_decline","charge":"ch_failed","network_decline_code":"05","network_advice_code":"03","advice_code":"do_not_try_again"}}`,
			func(t *testing.T, event Event, decoded any) {
				signal, err := event.RiskSignal()
				failure := signal.Failure
				if err != nil || failure == nil || failure.Type != "card_error" || failure.ChargeID != "ch_failed" ||
					failure.PaymentIntentID != "pi_failed" || failure.NetworkDeclineCode != "05" || failure.AdviceCode != "do_not_try_again" {
					t.Fatalf("wrong failure evidence: %+v, error %v", decoded, err)
				}
			},
		},
		{
			"invoice_payment.paid",
			`{"id":"inpay_fixture","object":"invoice_payment","invoice":"in_original","amount_paid":0,"amount_requested":700,"payment":{"type":"payment_record","payment_record":"pr_external"}}`,
			func(t *testing.T, _ Event, decoded any) {
				p, ok := decoded.(*InvoicePayment)
				if !ok || p.AmountPaid == nil || *p.AmountPaid != 0 || p.Payment.PaymentRecord.ID != "pr_external" {
					t.Fatalf("wrong invoice payment evidence: %+v", decoded)
				}
			},
		},
		{
			"charge.dispute.funds_withdrawn",
			`{"id":"dp_fixture","object":"dispute","charge":"ch_paid","balance_transactions":[{"id":"txn_withdrawn","amount":-700}]}`,
			func(t *testing.T, _ Event, decoded any) {
				d, ok := decoded.(*stripego.Dispute)
				if !ok || d.Charge.ID != "ch_paid" || len(d.BalanceTransactions) != 1 || d.BalanceTransactions[0].ID != "txn_withdrawn" {
					t.Fatalf("wrong dispute evidence: %+v", decoded)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.eventType, func(t *testing.T) {
			signed := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{
				Payload: []byte(eventFixture(APIVersion, tc.eventType, tc.object)),
				Secret:  testConfig.WebhookSecret, Timestamp: time.Now(),
			})
			event, err := client.VerifyWebhook(signed.Payload, signed.Header)
			if err != nil || event.Source != EventFromWebhook {
				t.Fatalf("verify webhook: %+v, error %v", event, err)
			}
			decoded, err := event.DecodeObject()
			if err != nil {
				t.Fatal(err)
			}
			tc.verify(t, event, decoded)
		})
	}
}
