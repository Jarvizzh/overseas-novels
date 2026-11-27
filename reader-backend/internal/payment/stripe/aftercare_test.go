package stripe

import (
	"net/http"
	"strconv"
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
	if evidence.Fact.OriginalCurrency != "USD" || evidence.Fact.OriginalAmountMinor == nil || *evidence.Fact.OriginalAmountMinor != 999 {
		t.Fatalf("USD original dispute amount not preserved: currency=%q amount=%v", evidence.Fact.OriginalCurrency, evidence.Fact.OriginalAmountMinor)
	}
}

func TestDisputeObservationPreservesOriginalCurrencyForGBPAndJPY(t *testing.T) {
	cases := []struct {
		name          string
		disputeID     string
		currency      string
		wantCurrency  string
		amount        int64
		usdSettlement int64
		wantUSDAmount bool
	}{
		{
			name:          "GBP dispute preserves minor units while keeping USD ledger settlement",
			disputeID:     "du_gbp",
			currency:      "gbp",
			wantCurrency:  "GBP",
			amount:        1250,
			usdSettlement: -1580,
			wantUSDAmount: false,
		},
		{
			name:          "JPY zero-decimal dispute preserves integer amount",
			disputeID:     "du_jpy",
			currency:      "jpy",
			wantCurrency:  "JPY",
			amount:        1500,
			usdSettlement: -1010,
			wantUSDAmount: false,
		},
		{
			name:          "USD dispute sets both original and USD amount",
			disputeID:     "du_usd",
			currency:      "usd",
			wantCurrency:  "USD",
			amount:        999,
			usdSettlement: -999,
			wantUSDAmount: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := fixtureClient(t, func(req *http.Request) (*http.Response, error) {
				switch req.URL.Path {
				case "/v1/disputes/" + tc.disputeID:
					return stripeResponse(200, `{"id":"`+tc.disputeID+`","object":"dispute","amount":`+
						jsonInt64(tc.amount)+`,"currency":"`+tc.currency+`","charge":"ch_fx","livemode":false,
						"balance_transactions":[{"id":"txn_fx"}],"reason":"fraudulent","status":"needs_response","is_charge_refundable":false}`)
				case "/v1/balance_transactions/txn_fx":
					return stripeResponse(200, `{"id":"txn_fx","object":"balance_transaction","amount":`+
						jsonInt64(tc.usdSettlement)+`,"fee":1500,"net":`+jsonInt64(tc.usdSettlement-1500)+`,
						"reporting_category":"dispute","created":1740000000,"currency":"usd","source":"`+tc.disputeID+`"}`)
				default:
					t.Fatalf("unexpected Stripe request %s", req.URL.Path)
					return nil, nil
				}
			})
			obs, err := client.AftercareDispute(t.Context(), tc.disputeID, "webhook", "evt_"+tc.disputeID)
			if err != nil {
				t.Fatal(err)
			}
			if obs.Fact.OriginalCurrency != tc.wantCurrency {
				t.Fatalf("OriginalCurrency = %q, want %q", obs.Fact.OriginalCurrency, tc.wantCurrency)
			}
			if obs.Fact.OriginalAmountMinor == nil || *obs.Fact.OriginalAmountMinor != tc.amount {
				t.Fatalf("OriginalAmountMinor = %v, want %d", obs.Fact.OriginalAmountMinor, tc.amount)
			}
			if tc.wantUSDAmount {
				if obs.Fact.AmountMinor == nil || *obs.Fact.AmountMinor != tc.amount {
					t.Fatalf("AmountMinor = %v, want %d", obs.Fact.AmountMinor, tc.amount)
				}
			} else if obs.Fact.AmountMinor != nil {
				t.Fatalf("non-USD dispute should leave USD AmountMinor nil, got %d", *obs.Fact.AmountMinor)
			}
			if len(obs.Movements) != 1 || obs.Movements[0].Currency != "USD" {
				t.Fatalf("expected USD ledger settlement movement, got %+v", obs.Movements)
			}
		})
	}
}

func jsonInt64(v int64) string {
	return strconv.FormatInt(v, 10)
}
