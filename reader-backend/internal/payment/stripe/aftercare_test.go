package stripe

import (
	"net/http"
	"testing"

	"reader-backend/internal/payment"
)

func TestDisputeObservationPreservesMerchantReferences(t *testing.T) {
	client := fixtureClient(t, func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/v1/disputes/du_fixture":
			return stripeResponse(200, `{"id":"du_fixture","object":"dispute","amount":999,"currency":"usd","charge":"ch_fixture","livemode":false,
				"balance_transactions":[{"id":"txn_fixture"}],"reason":"fraudulent","status":"lost","evidence_details":{"due_by":1750000000}}`)
		case "/v1/balance_transactions/txn_fixture":
			return stripeResponse(200, `{"id":"txn_fixture","object":"balance_transaction","amount":-999,"fee":0,"net":-999,
				"reporting_category":"dispute","created":1740000000,"currency":"usd","source":"du_fixture"}`)
		default:
			t.Fatalf("unexpected Stripe request %s", req.URL.Path)
			return nil, nil
		}
	})
	evidence, err := client.AftercareDispute(t.Context(), "du_fixture", "provider_api", "du_fixture")
	if err != nil {
		t.Fatal(err)
	}
	if evidence.Fact.Open || evidence.Fact.Kind != payment.CaseDispute || evidence.Fact.Outcome != "lost" ||
		evidence.Fact.DueAt == nil || len(evidence.Movements) != 1 || evidence.Movements[0].Kind != payment.MovementDisputeDebit {
		t.Fatalf("dispute evidence %+v", evidence)
	}
	found := false
	for _, ref := range evidence.Movements[0].References {
		if ref.Type == "stripe_charge" && ref.ID == "ch_fixture" {
			found = true
		}
	}
	if !found {
		t.Fatal("balance movement was not correlated with the disputed charge")
	}
}
