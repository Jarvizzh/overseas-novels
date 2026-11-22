package stripe

import (
	"fmt"
	"net/http"
	"testing"

	"reader-backend/internal/payment"
)

func TestCloseSingleCheckoutRequiresTerminalIntent(t *testing.T) {
	for _, scenario := range []struct {
		name, intent, charge      string
		closed, completed, failed bool
	}{
		{name: "processing", intent: "processing"},
		{name: "canceled after failed card", intent: "canceled", charge: "failed", closed: true},
		{name: "unresolved charge", intent: "canceled", charge: "pending"},
		{name: "asynchronous payment failed", intent: "requires_payment_method", charge: "failed", completed: true, failed: true},
		{name: "asynchronous payment still processing", intent: "processing", charge: "pending", completed: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			expired := false
			client := fixtureClient(t, func(req *http.Request) (*http.Response, error) {
				switch req.URL.Path {
				case "/v1/checkout/sessions/cs_Switch":
					status := "open"
					if expired {
						status = "expired"
					}
					if scenario.completed {
						status = "complete"
					}
					return stripeResponse(200, fmt.Sprintf(`{"id":"cs_Switch","mode":"payment","status":%q,"payment_status":"unpaid","client_reference_id":"attempt","metadata":{"payment_attempt_id":"attempt"},"payment_intent":"pi_Switch"}`, status))
				case "/v1/checkout/sessions/cs_Switch/expire":
					if req.Method != http.MethodPost || req.Header.Get("Idempotency-Key") != "switch-attempt" {
						t.Fatalf("unexpected expiration request %s", req.Method)
					}
					expired = true
					return stripeResponse(200, `{"id":"cs_Switch","status":"expired"}`)
				case "/v1/payment_intents/pi_Switch":
					return stripeResponse(200, fmt.Sprintf(`{"id":"pi_Switch","status":%q,"latest_charge":"ch_Switch","metadata":{"payment_attempt_id":"attempt"}}`, scenario.intent))
				case "/v1/charges/ch_Switch":
					return stripeResponse(200, fmt.Sprintf(`{"id":"ch_Switch","payment_intent":"pi_Switch","status":%q}`, scenario.charge))
				}
				t.Fatalf("unexpected Stripe path %s", req.URL.Path)
				return nil, nil
			})
			proof, err := client.CloseSingleCheckout(t.Context(), "cs_Switch", "attempt", payment.SingleCheckoutOffer{})
			if err != nil || expired == scenario.completed || proof.Expired != scenario.closed || proof.Failed != scenario.failed {
				t.Fatalf("proof=%+v expire called=%v error=%v", proof, expired, err)
			}
		})
	}
}

func TestSettlementNoticeUsesExpectedStripeReportTypes(t *testing.T) {
	client := fixtureClient(t, nil)
	event := Event{Scope: client.Scope(), ID: "evt_Payout", Type: "payout.reconciliation_completed", ObjectID: "po_Ready", ObjectType: "payout"}
	if notice, err := client.PaymentNotice(event); err != nil || notice == nil || notice.SettlementReference != "po_Ready" {
		t.Fatalf("payout notice=%+v error=%v", notice, err)
	}
	event = Event{Scope: client.Scope(), ID: "evt_Report", Type: "reporting.report_run.succeeded", ObjectID: "frr_Ready", ObjectType: "reporting.report_run"}
	event.Object = []byte(`{"id":"frr_Ready","report_type":"payout_reconciliation.itemized.7","status":"succeeded"}`)
	if notice, err := client.PaymentNotice(event); err != nil || notice == nil || notice.SettlementReference != "frr_Ready" {
		t.Fatalf("itemized report notice=%+v error=%v", notice, err)
	}
	event.Object = []byte(`{"id":"frr_Ready","report_type":"balance.summary.1","status":"succeeded"}`)
	if notice, err := client.PaymentNotice(event); err != nil || notice != nil {
		t.Fatalf("aggregate report reached import queue: %+v %v", notice, err)
	}
}
