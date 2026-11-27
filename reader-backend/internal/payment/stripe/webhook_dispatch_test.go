package stripe

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	stripego "github.com/stripe/stripe-go/v87"
	"github.com/stripe/stripe-go/v87/webhook"
)

func TestResolveWebhookNoticeRefetchesCurrentResourcesAcrossVersions(t *testing.T) {
	versions := []string{APIVersion, "2026-11-19.endive", "2025-03-31.basil"}
	const attemptID = "11111111-1111-1111-1111-111111111111"
	const vipIntentID = "22222222-2222-2222-2222-222222222222"

	client := fixtureClient(t, func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/v1/checkout/sessions/cs_single_old":
			return stripeResponse(200, fmt.Sprintf(`{"id":"cs_single_old","object":"checkout.session","livemode":false,"mode":"payment","client_reference_id":%q,"metadata":{"payment_attempt_id":%q}}`, attemptID, attemptID))
		case "/v1/checkout/sessions/cs_vip_old":
			return stripeResponse(200, fmt.Sprintf(`{"id":"cs_vip_old","object":"checkout.session","livemode":false,"mode":"subscription","client_reference_id":%q,"metadata":{"payment_attempt_id":%q}}`, vipIntentID, vipIntentID))
		case "/v1/invoice_payments/inpay_old":
			return stripeResponse(200, `{"id":"inpay_old","object":"invoice_payment","livemode":false,"invoice":"in_vip_1","amount_paid":999,"amount_requested":999}`)
		case "/v1/invoices/in_vip_1":
			return stripeResponse(200, `{"id":"in_vip_1","object":"invoice","livemode":false}`)
		case "/v1/subscriptions/sub_vip_1":
			return stripeResponse(200, `{"id":"sub_vip_1","object":"subscription","livemode":false}`)
		case "/v1/refunds/re_1":
			return stripeResponse(200, `{"id":"re_1","object":"refund","charge":"ch_1","status":"succeeded"}`)
		case "/v1/payouts/po_1":
			return stripeResponse(200, `{"id":"po_1","object":"payout","livemode":false,"status":"paid"}`)
		case "/v1/reporting/report_runs/frr_itemized":
			return stripeResponse(200, `{"id":"frr_itemized","object":"reporting.report_run","livemode":false,"status":"succeeded","report_type":"payout_reconciliation.itemized.7"}`)
		case "/v1/reporting/report_runs/frr_summary":
			return stripeResponse(200, `{"id":"frr_summary","object":"reporting.report_run","livemode":false,"status":"succeeded","report_type":"balance.summary.1"}`)
		case "/v1/charges/ch_old":
			return stripeResponse(200, `{"id":"ch_old","object":"charge","livemode":false,"status":"succeeded","outcome":{"type":"authorized","reason":"approved"}}`)
		case "/v1/payment_intents/pi_old_fail":
			return stripeResponse(200, `{"id":"pi_old_fail","object":"payment_intent","livemode":false,"last_payment_error":{"type":"card_error","code":"card_declined","decline_code":"insufficient_funds","charge":"ch_fail"}}`)
		default:
			t.Fatalf("unexpected Stripe request %s %s", req.Method, req.URL.Path)
			return nil, nil
		}
	})

	ctx := context.Background()
	for _, ver := range versions {
		t.Run("version="+ver, func(t *testing.T) {
			now := time.Now()
			verifyAndResolve := func(eventType, objJSON string) (*Event, error) {
				raw := []byte(fmt.Sprintf(
					`{"id":"evt_%s","type":%q,"object":"event","created":%d,"api_version":%q,"livemode":false,"data":{"object":%s}}`,
					"test", eventType, now.Unix(), ver, objJSON))
				signed := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{
					Payload: raw, Secret: testConfig.WebhookSecret, Timestamp: now,
				})
				ev, err := client.VerifyWebhook(raw, signed.Header)
				if err != nil {
					return nil, err
				}
				return &ev, nil
			}

			// Single Checkout
			ev, err := verifyAndResolve("checkout.session.completed", `{"id":"cs_single_old","object":"checkout.session"}`)
			if err != nil {
				t.Fatalf("verify single checkout: %v", err)
			}
			notice, err := client.ResolveWebhookNotice(ctx, *ev)
			if err != nil || notice == nil || notice.SessionID != "cs_single_old" || notice.AttemptID != attemptID {
				t.Fatalf("single checkout notice=%+v err=%v", notice, err)
			}

			// VIP Checkout
			ev, err = verifyAndResolve("checkout.session.completed", `{"id":"cs_vip_old","object":"checkout.session"}`)
			if err != nil {
				t.Fatalf("verify vip checkout: %v", err)
			}
			notice, err = client.ResolveWebhookNotice(ctx, *ev)
			if err != nil || notice == nil || notice.SessionID != "cs_vip_old" || notice.VIPIntentID != vipIntentID {
				t.Fatalf("vip checkout notice=%+v err=%v", notice, err)
			}

			// InvoicePayment
			ev, err = verifyAndResolve("invoice_payment.paid", `{"id":"inpay_old","object":"invoice_payment"}`)
			if err != nil {
				t.Fatalf("verify invoice_payment: %v", err)
			}
			notice, err = client.ResolveWebhookNotice(ctx, *ev)
			if err != nil || notice == nil || notice.InvoiceID != "in_vip_1" {
				t.Fatalf("invoice_payment notice=%+v err=%v", notice, err)
			}

			// Invoice
			ev, err = verifyAndResolve("invoice.paid", `{"id":"in_vip_1","object":"invoice"}`)
			if err != nil {
				t.Fatalf("verify invoice: %v", err)
			}
			notice, err = client.ResolveWebhookNotice(ctx, *ev)
			if err != nil || notice == nil || notice.InvoiceID != "in_vip_1" {
				t.Fatalf("invoice notice=%+v err=%v", notice, err)
			}

			// Subscription
			ev, err = verifyAndResolve("customer.subscription.updated", `{"id":"sub_vip_1","object":"subscription"}`)
			if err != nil {
				t.Fatalf("verify subscription: %v", err)
			}
			notice, err = client.ResolveWebhookNotice(ctx, *ev)
			if err != nil || notice == nil || notice.SubscriptionID != "sub_vip_1" {
				t.Fatalf("subscription notice=%+v err=%v", notice, err)
			}

			// Refund
			ev, err = verifyAndResolve("refund.updated", `{"id":"re_1","object":"refund"}`)
			if err != nil {
				t.Fatalf("verify refund: %v", err)
			}
			notice, err = client.ResolveWebhookNotice(ctx, *ev)
			if err != nil || notice == nil || notice.RefundID != "re_1" {
				t.Fatalf("refund notice=%+v err=%v", notice, err)
			}

			// Payout
			ev, err = verifyAndResolve("payout.paid", `{"id":"po_1","object":"payout"}`)
			if err != nil {
				t.Fatalf("verify payout: %v", err)
			}
			notice, err = client.ResolveWebhookNotice(ctx, *ev)
			if err != nil || notice == nil || notice.SettlementReference != "po_1" {
				t.Fatalf("payout notice=%+v err=%v", notice, err)
			}

			// Itemized Report Run
			ev, err = verifyAndResolve("reporting.report_run.succeeded", `{"id":"frr_itemized","object":"reporting.report_run"}`)
			if err != nil {
				t.Fatalf("verify report run: %v", err)
			}
			notice, err = client.ResolveWebhookNotice(ctx, *ev)
			if err != nil || notice == nil || notice.SettlementReference != "frr_itemized" {
				t.Fatalf("report run notice=%+v err=%v", notice, err)
			}

			// Non-itemized Report Run (ignored cleanly)
			ev, err = verifyAndResolve("reporting.report_run.succeeded", `{"id":"frr_summary","object":"reporting.report_run"}`)
			if err != nil {
				t.Fatalf("verify summary report run: %v", err)
			}
			notice, err = client.ResolveWebhookNotice(ctx, *ev)
			if err != nil || notice != nil {
				t.Fatalf("summary report run should be ignored, got notice=%+v err=%v", notice, err)
			}

			// Risk signal (charge.succeeded & payment_intent.payment_failed) via AftercareEvent fallback
			ev, err = verifyAndResolve("charge.succeeded", `{"id":"ch_old","object":"charge","status":"succeeded","outcome":{"type":"authorized","reason":"approved"}}`)
			if err != nil {
				t.Fatalf("verify charge.succeeded: %v", err)
			}
			notice, err = client.ResolveWebhookNotice(ctx, *ev)
			if err != nil || notice == nil || len(notice.Aftercare) == 0 {
				t.Fatalf("charge aftercare notice=%+v err=%v", notice, err)
			}
			obs, err := client.ResolveQueuedAftercare(ctx, notice.Aftercare)
			if err != nil || obs == nil || len(obs.Fact.ReceiptRefs) != 1 || obs.Fact.ReceiptRefs[0] != "ch_old" {
				t.Fatalf("charge aftercare obs=%+v err=%v", obs, err)
			}

			ev, err = verifyAndResolve("payment_intent.payment_failed", `{"id":"pi_old_fail","object":"payment_intent","last_payment_error":{"type":"card_error","code":"card_declined","decline_code":"insufficient_funds","charge":"ch_fail"}}`)
			if err != nil {
				t.Fatalf("verify payment_intent.payment_failed: %v", err)
			}
			notice, err = client.ResolveWebhookNotice(ctx, *ev)
			if err != nil || notice == nil || len(notice.Aftercare) == 0 {
				t.Fatalf("pi aftercare notice=%+v err=%v", notice, err)
			}
			obs, err = client.ResolveQueuedAftercare(ctx, notice.Aftercare)
			if err != nil || obs == nil || obs.Fact.Reason != "insufficient_funds" {
				t.Fatalf("pi aftercare obs=%+v err=%v", obs, err)
			}
		})
	}
}

func TestResolveWebhookNoticeTransientFailureAndMismatches(t *testing.T) {
	failOnce := true
	client := fixtureClient(t, func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/v1/checkout/sessions/cs_transient":
			if failOnce {
				failOnce = false
				return stripeResponse(500, `{"error":{"type":"api_error"}}`)
			}
			return stripeResponse(200, `{"id":"cs_transient","object":"checkout.session","livemode":false,"mode":"payment","client_reference_id":"att_1","metadata":{"payment_attempt_id":"att_1"}}`)
		case "/v1/checkout/sessions/cs_wrong_env":
			return stripeResponse(200, `{"id":"cs_wrong_env","object":"checkout.session","livemode":true,"mode":"payment","client_reference_id":"att_1","metadata":{"payment_attempt_id":"att_1"}}`)
		case "/v1/reporting/report_runs/frr_pending":
			return stripeResponse(200, `{"id":"frr_pending","object":"reporting.report_run","livemode":false,"status":"pending","report_type":"payout_reconciliation.itemized.7"}`)
		default:
			t.Fatalf("unexpected Stripe request %s %s", req.Method, req.URL.Path)
			return nil, nil
		}
	})
	ctx := context.Background()

	// 1. Transient fetch failure is retryable (*APIError), then succeeds on retry.
	ev := Event{Scope: client.Scope(), Source: EventFromWebhook, ID: "evt_t1", Type: EventCheckoutSessionCompleted, APIVersion: "2025-03-31.basil", ObjectID: "cs_transient"}
	_, err := client.ResolveWebhookNotice(ctx, ev)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 500 || errors.Is(err, ErrEventResourceMismatch) {
		t.Fatalf("expected retryable 500 APIError, got %v", err)
	}
	notice, err := client.ResolveWebhookNotice(ctx, ev)
	if err != nil || notice == nil || notice.AttemptID != "att_1" || notice.SessionID != "cs_transient" {
		t.Fatalf("expected retry success, got notice=%+v err=%v", notice, err)
	}

	// 2. Scope mismatch returns ErrEventScopeMismatch.
	wrongScope := ev
	wrongScope.Scope = Scope{AccountID: "acct_other", Environment: Sandbox}
	if _, err := client.ResolveWebhookNotice(ctx, wrongScope); !errors.Is(err, ErrEventScopeMismatch) {
		t.Fatalf("expected ErrEventScopeMismatch, got %v", err)
	}

	// 3. Livemode mismatch returns ErrEventResourceMismatch.
	wrongEnv := Event{Scope: client.Scope(), Source: EventFromWebhook, ID: "evt_e1", Type: EventCheckoutSessionCompleted, APIVersion: APIVersion, ObjectID: "cs_wrong_env"}
	if _, err := client.ResolveWebhookNotice(ctx, wrongEnv); !errors.Is(err, ErrEventResourceMismatch) {
		t.Fatalf("expected ErrEventResourceMismatch for livemode mismatch, got %v", err)
	}

	// 4. Report run not succeeded returns ErrEventResourceMismatch.
	notReady := Event{Scope: client.Scope(), Source: EventFromAPI, ID: "evt_r1", Type: EventReportingReportRunSucceeded, APIVersion: "2025-03-31.basil", ObjectID: "frr_pending"}
	if _, err := client.ResolveWebhookNotice(ctx, notReady); !errors.Is(err, ErrEventResourceMismatch) {
		t.Fatalf("expected ErrEventResourceMismatch for non-succeeded report run, got %v", err)
	}

	// 5. Unhandled event type returns (nil, nil) without remote fetch.
	unhandled := Event{Scope: client.Scope(), Source: EventFromWebhook, ID: "evt_u1", Type: stripego.EventType("customer.created"), APIVersion: APIVersion, ObjectID: "cus_1"}
	if notice, err := client.ResolveWebhookNotice(ctx, unhandled); err != nil || notice != nil {
		t.Fatalf("expected unhandled event to be ignored, got notice=%+v err=%v", notice, err)
	}
}
