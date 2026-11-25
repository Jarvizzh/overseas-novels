package stripe

import (
	"net/http"
	"testing"

	"reader-backend/internal/payment"
)

func TestReadPayoutSettlementFollowsMembershipPages(t *testing.T) {
	client := fixtureClient(t, func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet {
			t.Fatalf("payout reconciliation sent a channel mutation: %s", req.Method)
		}
		switch req.URL.Path {
		case "/v1/payouts/po_fixture":
			return stripeResponse(200, `{"id":"po_fixture","object":"payout","currency":"usd","amount":900,
				"type":"bank_account","method":"standard","automatic":true,"status":"paid",
				"reconciliation_status":"completed","balance_transaction":"txn_payout",
				"trace_id":{"status":"supported","value":"BANK-TRACE"},"arrival_date":1795309200}`)
		case "/v1/balance_transactions/txn_payout":
			return stripeResponse(200, `{"id":"txn_payout","currency":"usd","created":1795309200,
				"reporting_category":"payout","amount":-900,"net":-900,"fee":0,"source":"po_fixture"}`)
		case "/v1/balance_transactions":
			if req.URL.Query().Get("payout") != "po_fixture" {
				t.Fatalf("listing missed payout filter: %s", req.URL)
			}
			if req.URL.Query().Get("starting_after") == "" {
				return stripeResponse(200, `{"object":"list","has_more":true,"data":[{"id":"txn_charge",
					"currency":"usd","created":1795309200,"reporting_category":"charge","amount":1000,"net":1000,"fee":0}]}`)
			}
			if req.URL.Query().Get("starting_after") != "txn_charge" {
				t.Fatalf("unexpected cursor: %s", req.URL)
			}
			return stripeResponse(200, `{"object":"list","has_more":false,"data":[{"id":"txn_refund",
				"currency":"usd","created":1795309200,"reporting_category":"refund","amount":-100,"net":-100,"fee":0}]}`)
		default:
			t.Fatalf("unexpected Stripe endpoint: %s", req.URL)
			return nil, nil
		}
	})
	var movements []payment.LedgerEvidence
	got, err := client.ReadPayoutSettlement(t.Context(), "po_fixture", func(move payment.LedgerEvidence) error {
		movements = append(movements, move)
		return nil
	})
	if err != nil || !got.AllocationComplete || got.Expected.Minor != 900 || got.BankReference != "BANK-TRACE" ||
		got.TransferKey != "txn_payout" || len(movements) != 3 || movements[2].MovementKey != "txn_refund" {
		t.Fatalf("payout settlement: %+v, moves=%+v %v", got, movements, err)
	}
	if len(movements[2].References) != 1 || movements[2].References[0] != (payment.LedgerReference{Type: "stripe_payout_allocation", ID: "po_fixture"}) {
		t.Fatalf("missing member association: %+v", movements[2].References)
	}
}
