package stripe

import (
	"net/http"
	"testing"

	"reader-backend/internal/payment"
)

func TestStripeReadOnlyLedgerEvidenceUsesBalanceAmounts(t *testing.T) {
	client := fixtureClient(t, func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet {
			t.Fatalf("unexpected Stripe write request: %s %s", req.Method, req.URL)
		}
		switch req.URL.Path {
		case "/v1/refunds/re_fixture":
			return stripeResponse(200, `{"id":"re_fixture","object":"refund","charge":"ch_origin",
				"balance_transaction":"txn_refund","failure_balance_transaction":"txn_return","amount":400}`)
		case "/v1/balance_transactions/txn_refund":
			return stripeResponse(200, `{"id":"txn_refund","object":"balance_transaction","created":1795136400,
				"reporting_category":"refund","source":"re_fixture","currency":"usd",
				"amount":-400,"fee":-30,"net":-370,"exchange_rate":0.0064516129}`)
		case "/v1/balance_transactions/txn_missing":
			return stripeResponse(200, `{"id":"txn_missing","object":"balance_transaction","created":1795136400,
				"reporting_category":"refund","currency":"usd","amount":-400,"net":-400}`)
		default:
			t.Fatalf("unexpected Stripe ledger request: %s", req.URL)
			return nil, nil
		}
	})
	observed, err := client.GetRefundLedgerEvidence(t.Context(), "re_fixture")
	if err != nil || len(observed) != 2 || observed[0].MovementKey != "txn_refund" ||
		observed[1].MovementKey != "txn_return" || len(observed[0].Amounts) != 0 {
		t.Fatalf("refund did not identify both balance movements without posting: %+v %v", observed, err)
	}
	post, err := client.GetLedgerMovement(t.Context(), "txn_refund")
	if err != nil || post.Kind != payment.MovementRefund || post.ExchangeRate != "0.0064516129" ||
		post.Amounts[payment.LedgerFee].Minor != 30 || post.Amounts[payment.LedgerNet].Minor != -370 {
		t.Fatalf("incorrect balance normalization: %+v %v", post, err)
	}
	if _, err := client.GetLedgerMovement(t.Context(), "txn_missing"); err == nil {
		t.Fatal("missing fee was interpreted as zero")
	}
}
