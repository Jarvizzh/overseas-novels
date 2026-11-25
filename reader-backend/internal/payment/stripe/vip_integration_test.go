package stripe

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stripe/stripe-go/v87/webhook"
	"reader-backend/internal/payment"
)

func TestStripeVIPAdapterChecksCheckoutAndInvoiceObjects(t *testing.T) {
	const intent = "9ca63608-c8ab-43bb-a983-26c70932821a"
	const subscription = `{"id":"sub_VIP","status":"active","customer":"cus_VIP","metadata":{"payment_attempt_id":"` + intent + `"},
		"items":{"data":[{"price":{"id":"price_VIP"},"quantity":1}],"has_more":false}}`
	offStripe := false
	wrongCustomer := false
	client := fixtureClient(t, func(req *http.Request) (*http.Response, error) {
		switch req.Method + " " + req.URL.Path {
		case "GET /v1/prices/price_VIP":
			return stripeResponse(200, `{"id":"price_VIP","active":true,"type":"recurring","currency":"usd","unit_amount":999,
				"product":"prod_VIP","recurring":{"interval":"month","interval_count":1}}`)
		case "POST /v1/checkout/sessions":
			if err := req.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if req.Header.Get("Idempotency-Key") != intent || req.Form.Get("mode") != "subscription" ||
				req.Form.Get("adaptive_pricing[enabled]") != "false" ||
				req.Form.Get("subscription_data[metadata][payment_attempt_id]") != intent {
				t.Fatalf("invalid subscription Checkout contract: %v", req.Form)
			}
			return stripeResponse(200, `{"id":"cs_test_VIP","mode":"subscription","status":"open","url":"https://checkout.stripe.com/fixture",
				"client_reference_id":"`+intent+`","metadata":{"payment_attempt_id":"`+intent+`"}}`)
		case "GET /v1/subscriptions/sub_VIP":
			return stripeResponse(200, subscription)
		case "GET /v1/invoices/in_VIP":
			customer := "cus_VIP"
			if wrongCustomer {
				customer = "cus_Wrong"
			}
			manual := 0
			if offStripe {
				manual = 999
			}
			return stripeResponse(200, fmt.Sprintf(`{"id":"in_VIP","parent":{"type":"subscription_details","subscription_details":{"subscription":"sub_VIP"}},
				"customer":%q,"customer_address":{"country":"US"},"status":"paid","billing_reason":"subscription_create","currency":"usd","amount_due":999,
				"amount_paid_off_stripe":%d}`, customer, manual))
		case "GET /v1/invoices/in_VIP/lines":
			return stripeResponse(200, `{"object":"list","has_more":false,"data":[{"id":"il_VIP","quantity":1,"currency":"usd","subtotal":999,
				"parent":{"type":"subscription_item_details","subscription_item_details":{"subscription":"sub_VIP","subscription_item":"si_VIP"}},
				"period":{"start":1740000000,"end":1742419200},"pricing":{"type":"price_details","price_details":{"price":"price_VIP","product":"prod_VIP"}}}]}`)
		case "GET /v1/invoice_payments":
			if req.URL.Query().Get("invoice") != "in_VIP" {
				t.Fatalf("invoice payment list scope: %s", req.URL)
			}
			return stripeResponse(200, `{"object":"list","has_more":false,"data":[{"id":"inpay_VIP","currency":"usd","status":"paid",
				"invoice":"in_VIP","amount_paid":999,"payment":{"type":"payment_intent","payment_intent":"pi_VIP"}}]}`)
		case "GET /v1/payment_intents/pi_VIP":
			return stripeResponse(200, `{"id":"pi_VIP","latest_charge":"ch_VIP","customer":"cus_VIP","currency":"usd","status":"succeeded","amount_received":999}`)
		case "GET /v1/charges/ch_VIP":
			return stripeResponse(200, `{"id":"ch_VIP","customer":"cus_VIP","currency":"usd","status":"succeeded","paid":true,"captured":true,"amount_captured":999,
				"created":1740000123,"payment_intent":"pi_VIP","balance_transaction":"txn_VIP"}`)
		case "POST /v1/billing_portal/sessions":
			if err := req.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if req.Form.Get("customer") != "cus_VIP" || req.Form.Get("flow_data[type]") != "payment_method_update" {
				t.Fatalf("unexpected customer portal permissions: %v", req.Form)
			}
			return stripeResponse(200, `{"url":"https://billing.stripe.com/p/session"}`)
		default:
			t.Fatalf("unexpected Stripe SDK request: %s %s", req.Method, req.URL)
			return nil, nil
		}
	})
	ctx := t.Context()
	offer := payment.StripeVIPOffer{Cycle: "month", SingleCheckoutOffer: payment.SingleCheckoutOffer{
		ProductID: "prod_VIP", PriceID: "price_VIP", SalesMode: payment.StripeSaleDirect, AmountMinor: 999,
	}}
	checkout, err := client.CreateStripeVIPCheckout(ctx, payment.StripeVIPCheckoutRequest{IntentID: intent, Offer: offer,
		ReturnURL: "https://reader.example/recharge?payment=return", CancelURL: "https://reader.example/recharge?payment=cancel"})
	if err != nil || !checkout.AwaitingBuyer || checkout.URL == "" {
		t.Fatalf("checkout=%+v error=%v", checkout, err)
	}
	proof, err := client.ReadStripeVIPInvoice(ctx, "in_VIP", offer)
	if err != nil || len(proof.Allocations) != 1 || proof.ReviewReason != "" || proof.Allocations[0].Receipt.ID != "ch_VIP" || !proof.EndsAt.After(proof.StartsAt) {
		t.Fatalf("invoice proof=%+v error=%v", proof, err)
	}
	offStripe = true
	proof, err = client.ReadStripeVIPInvoice(ctx, "in_VIP", offer)
	if err != nil || proof.ReviewReason == "" {
		t.Fatalf("off-Stripe invoice=%+v error=%v", proof, err)
	}
	wrongCustomer = true
	if _, err := client.ReadStripeVIPInvoice(ctx, "in_VIP", offer); err == nil {
		t.Fatal("invoice from another customer was accepted")
	}
	if url, err := client.OpenStripeVIPPortal(ctx, "cus_VIP", "https://reader.example/recharge", intent); err != nil || url == "" {
		t.Fatalf("Stripe portal=%q error=%v", url, err)
	}
}

func TestStripeVIPInvoiceNoticeUsesSignedIdentity(t *testing.T) {
	client := fixtureClient(t, nil)
	at := time.Now()
	body := []byte(fmt.Sprintf(`{"id":"evt_VIP","type":"invoice.payment_failed","object":"event","created":%d,"api_version":%q,
		"account":"acct_merchant","livemode":false,"data":{"object":{"id":"in_VIP"}}}`, at.Unix(), APIVersion))
	signed := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{Payload: body, Secret: testConfig.WebhookSecret, Timestamp: at})
	notice, err := client.VerifySingleCheckoutEvent(body, signed.Header)
	if err != nil || notice == nil || notice.InvoiceID != "in_VIP" || notice.Account.AccountKey != "acct_merchant" {
		t.Fatalf("signed invoice event=%+v error=%v", notice, err)
	}
	if _, err := client.VerifySingleCheckoutEvent(append(body, ' '), signed.Header); err == nil {
		t.Fatal("modified invoice notice passed verification")
	}
	allocation := []byte(fmt.Sprintf(`{"id":"evt_Allocation","type":"invoice_payment.paid","object":"event","created":%d,
		"api_version":%q,"livemode":false,"data":{"object":{"id":"inpay_VIP","invoice":"in_VIP","livemode":false}}}`, at.Unix(), APIVersion))
	allocated := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{Payload: allocation, Secret: testConfig.WebhookSecret, Timestamp: at})
	notice, err = client.VerifySingleCheckoutEvent(allocation, allocated.Header)
	if err != nil || notice == nil || notice.InvoiceID != "in_VIP" {
		t.Fatalf("signed partial invoice payment=%+v error=%v", notice, err)
	}

}

func TestStripeAftercareIngestUsesSignedSnapshotWithoutRemoteLookup(t *testing.T) {
	client := fixtureClient(t, nil)
	at := time.Now()
	body := []byte(fmt.Sprintf(`{"id":"evt_Review","type":"review.opened","object":"event","created":%d,"api_version":%q,
		"livemode":false,"data":{"object":{"id":"prv_fixture","object":"review"}}}`, at.Unix(), APIVersion))
	signed := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{Payload: body, Secret: testConfig.WebhookSecret, Timestamp: at})
	notice, err := client.VerifySingleCheckoutEvent(body, signed.Header)
	if err != nil || notice == nil || len(notice.Aftercare) == 0 {
		t.Fatalf("aftercare ingress=%+v error=%v", notice, err)
	}
}
