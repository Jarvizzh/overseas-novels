package stripe

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestCheckoutBillingAndDisputeAPIsKeepChannelEvidence(t *testing.T) {
	client := fixtureClient(t, func(req *http.Request) (*http.Response, error) {
		switch req.Method + " " + req.URL.Path {
		case "GET /v1/checkout/sessions/cs_test_book":
			return stripeResponse(200, `{"id":"cs_test_book","mode":"payment","status":"complete","payment_status":"paid","client_reference_id":"attempt_book","payment_intent":"pi_book","managed_payments":{"enabled":true},"currency":"cad","amount_total":1524,"customer_details":{"address":{"country":"CA"}}}`)
		case "GET /v1/checkout/sessions/cs_test_book/line_items":
			return stripeResponse(200, `{"object":"list","data":[{"id":"li_book","price":{"id":"price_book"},"quantity":1}]}`)
		case "GET /v1/payment_intents/pi_book":
			return stripeResponse(200, `{"id":"pi_book","status":"succeeded","latest_charge":"ch_book","currency":"cad","amount_received":1524}`)
		case "POST /v1/refunds":
			if err := req.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if req.Form.Get("charge") != "ch_book" || req.Form.Get("amount") != "550" ||
				req.Header.Get("Idempotency-Key") != "refund-book-part" {
				t.Fatalf("unexpected partial refund: %v", req.Form)
			}
			return stripeResponse(200, `{"id":"re_book","charge":"ch_book","currency":"cad","amount":550,"status":"pending"}`)
		case "GET /v1/refunds/re_book":
			return stripeResponse(200, `{"id":"re_book","charge":"ch_book","balance_transaction":"txn_refund","currency":"cad","amount":550,"status":"succeeded"}`)
		case "GET /v1/refunds":
			if req.URL.Query().Get("charge") != "ch_book" {
				t.Fatalf("missing charge filter: %s", req.URL)
			}
			return stripeResponse(200, `{"object":"list","data":[{"id":"re_book","charge":"ch_book","balance_transaction":"txn_refund","currency":"cad","amount":550,"status":"succeeded"}]}`)
		case "GET /v1/invoices":
			if req.URL.Query().Get("subscription") != "sub_vip" {
				t.Fatalf("missing subscription filter: %s", req.URL)
			}
			return stripeResponse(200, `{"object":"list","data":[{"id":"in_vip","status":"paid","amount_due":900,"currency":"cad"}]}`)
		case "GET /v1/invoices/in_vip/lines":
			return stripeResponse(200, `{"object":"list","data":[{"id":"il_vip","period":{"start":1795132800,"end":1795219200}}]}`)
		case "GET /v1/invoice_payments/inpay_vip":
			return stripeResponse(200, `{"id":"inpay_vip","invoice":"in_vip","status":"open","amount_paid":null,"amount_requested":900,"payment":{"type":"payment_record","payment_record":"pr_vip"}}`)
		case "GET /v1/payment_records/pr_vip":
			return stripeResponse(200, `{"id":"pr_vip","amount_requested":{"value":900,"currency":"cad"}}`)
		case "GET /v1/disputes":
			query := req.URL.Query()
			if query.Get("created[gte]") != "1795132800" || query.Get("created[lt]") != "1795219200" {
				t.Fatalf("missing dispute time bounds: %s", req.URL)
			}
			return stripeResponse(200, `{"object":"list","data":[{"id":"dp_book","charge":"ch_book","status":"needs_response"}]}`)
		case "GET /v1/disputes/dp_book":
			return stripeResponse(200, `{"id":"dp_book","charge":"ch_book","status":"won","balance_transactions":[{"id":"txn_withdrawn","amount":-974},{"id":"txn_returned","amount":974}]}`)
		default:
			t.Fatalf("unexpected Stripe request: %s %s", req.Method, req.URL)
			return nil, nil
		}
	})
	ctx := context.Background()
	checkout, err := client.GetCheckout(ctx, "cs_test_book")
	if err != nil || checkout.ManagedPayments == nil || !checkout.ManagedPayments.Enabled ||
		checkout.PaymentIntent.ID != "pi_book" || checkout.Currency != "cad" ||
		checkout.AmountTotal != 1524 || checkout.CustomerDetails.Address.Country != "CA" {
		t.Fatalf("unexpected Checkout evidence: %+v, error %v", checkout, err)
	}
	lines, err := client.ListCheckoutLines(ctx, checkout.ID, PageOptions{})
	if err != nil || len(lines.Items) != 1 || lines.Items[0].Price.ID != "price_book" {
		t.Fatalf("unexpected Checkout lines: %+v, error %v", lines, err)
	}
	intent, err := client.GetPaymentIntent(ctx, checkout.PaymentIntent.ID)
	if err != nil || intent.LatestCharge.ID != "ch_book" || intent.AmountReceived != 1524 {
		t.Fatalf("unexpected payment evidence: %+v, error %v", intent, err)
	}
	refund, err := client.CreateRefund(ctx, intent.LatestCharge.ID, 550, "refund-book-part")
	if err != nil || refund.Status != "pending" || refund.Amount != 550 {
		t.Fatalf("unexpected refund creation: %+v, error %v", refund, err)
	}
	refund, err = client.GetRefund(ctx, refund.ID)
	if err != nil || refund.Status != "succeeded" || refund.BalanceTransaction.ID != "txn_refund" {
		t.Fatalf("unexpected refund evidence: %+v, error %v", refund, err)
	}
	refunds, err := client.ListRefunds(ctx, refund.Charge.ID, PageOptions{})
	if err != nil || len(refunds.Items) != 1 || refunds.Items[0].ID != refund.ID {
		t.Fatalf("unexpected refund list: %+v, error %v", refunds, err)
	}
	invoices, err := client.ListSubscriptionInvoices(ctx, "sub_vip", PageOptions{})
	if err != nil || len(invoices.Items) != 1 || invoices.Items[0].AmountDue != 900 {
		t.Fatalf("unexpected invoices: %+v, error %v", invoices, err)
	}
	periods, err := client.ListInvoiceLines(ctx, invoices.Items[0].ID, PageOptions{})
	if err != nil || len(periods.Items) != 1 || periods.Items[0].Period.Start != 1795132800 {
		t.Fatalf("unexpected invoice lines: %+v, error %v", periods, err)
	}
	allocation, err := client.GetInvoicePayment(ctx, "inpay_vip")
	if err != nil || allocation.AmountPaid != nil || allocation.Invoice.ID != "in_vip" ||
		allocation.Payment.PaymentRecord.ID != "pr_vip" {
		t.Fatalf("unexpected invoice payment: %+v, error %v", allocation, err)
	}
	payment, err := client.GetPaymentRecord(ctx, allocation.Payment.PaymentRecord.ID)
	if err != nil || payment.ID != "pr_vip" || payment.AmountRequested.Value != 900 {
		t.Fatalf("unexpected payment record: %+v, error %v", payment, err)
	}
	window := TimeRange{From: time.Unix(1795132800, 0), Before: time.Unix(1795219200, 0)}
	disputes, err := client.ListDisputes(ctx, window, PageOptions{})
	if err != nil || len(disputes.Items) != 1 || disputes.Items[0].Charge.ID != "ch_book" {
		t.Fatalf("unexpected dispute list: %+v, error %v", disputes, err)
	}
	dispute, err := client.GetDispute(ctx, disputes.Items[0].ID)
	if err != nil || dispute.Status != "won" || len(dispute.BalanceTransactions) != 2 ||
		dispute.BalanceTransactions[0].Amount != -974 || dispute.BalanceTransactions[1].Amount != 974 {
		t.Fatalf("unexpected dispute evidence: %+v, error %v", dispute, err)
	}
}
