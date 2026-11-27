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

func TestPublishedSalesModeMoRWithoutFixedTaxAccount(t *testing.T) {
	productPayload := `{"id":"prod_MoR","active":true,"livemode":false,"tax_code":"txcd_10000000"}`
	client := fixtureClient(t, func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/v1/products/prod_MoR" {
			return stripeResponse(200, productPayload)
		}
		t.Fatalf("unexpected Stripe path %s", req.URL.Path)
		return nil, nil
	})
	offer := payment.SingleCheckoutOffer{
		ProductID: "prod_MoR",
		PriceID:   "price_MoR",
		SalesMode: payment.StripeSaleMoR,
		// TaxLiabilityAccount intentionally empty under P0-2.
	}
	mode, err := client.publishedSalesMode(t.Context(), offer)
	if err != nil || mode != MoR {
		t.Fatalf("publishedSalesMode(MoR without fixed account) = %q, %v; want %q, nil", mode, err, MoR)
	}

	// Missing tax_code on Product still fails closed.
	productPayload = `{"id":"prod_MoR","active":true,"livemode":false}`
	if _, err := client.publishedSalesMode(t.Context(), offer); err == nil {
		t.Fatal("expected error when MoR product has no tax_code")
	}
}

func TestManagedPaymentsTaxVerificationSingleAndVIP(t *testing.T) {
	cases := []struct {
		name              string
		offerTaxAccount   string
		automaticTaxJSON  string
		taxExempt         string
		wantVerified      bool
		wantLiabilityAcct string
		wantLiabilityType string
		wantAutoTaxStatus string
	}{
		{
			name:              "returned Stripe MoR liability account without prefilled expectation",
			automaticTaxJSON:  `{"enabled":true,"status":"complete","liability":{"type":"account","account":"acct_StripeMoR01"}}`,
			taxExempt:         "none",
			wantVerified:      true,
			wantLiabilityAcct: "acct_StripeMoR01",
			wantLiabilityType: "account",
			wantAutoTaxStatus: "complete",
		},
		{
			name:              "empty liability account alone does not fail",
			automaticTaxJSON:  `{"enabled":true,"status":"complete"}`,
			taxExempt:         "none",
			wantVerified:      true,
			wantLiabilityAcct: "",
			wantLiabilityType: "",
			wantAutoTaxStatus: "complete",
		},
		{
			name:              "differing from historical expected account alone does not fail",
			offerTaxAccount:   "acct_HistoricalOld",
			automaticTaxJSON:  `{"enabled":true,"status":"complete","liability":{"type":"account","account":"acct_StripeMoRNew"}}`,
			taxExempt:         "exempt",
			wantVerified:      true,
			wantLiabilityAcct: "acct_StripeMoRNew",
			wantLiabilityType: "account",
			wantAutoTaxStatus: "complete",
		},
		{
			name:              "incomplete automatic tax status fails verification",
			automaticTaxJSON:  `{"enabled":true,"status":"failed","liability":{"type":"account","account":"acct_StripeMoR01"}}`,
			taxExempt:         "none",
			wantVerified:      false,
			wantLiabilityAcct: "acct_StripeMoR01",
			wantLiabilityType: "account",
			wantAutoTaxStatus: "failed",
		},
		{
			name:              "missing customer tax status fails verification",
			automaticTaxJSON:  `{"enabled":true,"status":"complete","liability":{"type":"account","account":"acct_StripeMoR01"}}`,
			taxExempt:         "",
			wantVerified:      false,
			wantLiabilityAcct: "acct_StripeMoR01",
			wantLiabilityType: "account",
			wantAutoTaxStatus: "complete",
		},
		{
			name:              "self liability type in MoR conflicts and fails verification",
			automaticTaxJSON:  `{"enabled":true,"status":"complete","liability":{"type":"self"}}`,
			taxExempt:         "none",
			wantVerified:      false,
			wantLiabilityAcct: "",
			wantLiabilityType: "self",
			wantAutoTaxStatus: "complete",
		},
		{
			name:              "merchant own account as liability account in MoR conflicts and fails verification",
			automaticTaxJSON:  `{"enabled":true,"status":"complete","liability":{"type":"account","account":"acct_merchant"}}`,
			taxExempt:         "none",
			wantVerified:      false,
			wantLiabilityAcct: "acct_merchant",
			wantLiabilityType: "account",
			wantAutoTaxStatus: "complete",
		},
	}

	for _, tc := range cases {
		t.Run("single/"+tc.name, func(t *testing.T) {
			client := fixtureClient(t, func(req *http.Request) (*http.Response, error) {
				switch req.URL.Path {
				case "/v1/checkout/sessions/cs_MoR":
					return stripeResponse(200, fmt.Sprintf(`{
						"id":"cs_MoR","mode":"payment","status":"complete","payment_status":"paid",
						"currency":"usd","amount_total":1080,"client_reference_id":"att_1",
						"metadata":{"payment_attempt_id":"att_1"},"payment_intent":"pi_MoR",
						"managed_payments":{"enabled":true},
						"customer_details":{"tax_exempt":%q,"address":{"country":"US"}},
						"automatic_tax":%s
					}`, tc.taxExempt, tc.automaticTaxJSON))
				case "/v1/payment_intents/pi_MoR":
					return stripeResponse(200, `{
						"id":"pi_MoR","status":"succeeded","currency":"usd","amount_received":1080,
						"latest_charge":"ch_MoR","metadata":{"payment_attempt_id":"att_1"},
						"managed_payments":{"enabled":true}
					}`)
				case "/v1/charges/ch_MoR":
					return stripeResponse(200, `{
						"id":"ch_MoR","payment_intent":"pi_MoR","currency":"usd","status":"succeeded",
						"paid":true,"captured":true,"amount_captured":1080,"created":1740000123
					}`)
				case "/v1/checkout/sessions/cs_MoR/line_items":
					return stripeResponse(200, `{
						"object":"list","has_more":false,"data":[{
							"id":"li_1","quantity":1,"currency":"usd","amount_subtotal":1000,
							"amount_tax":80,"amount_discount":0,"amount_total":1080,
							"price":{"id":"price_MoR"}
						}]
					}`)
				default:
					t.Fatalf("unexpected Stripe path %s", req.URL.Path)
					return nil, nil
				}
			})
			offer := payment.SingleCheckoutOffer{
				ProductID:           "prod_MoR",
				PriceID:             "price_MoR",
				SalesMode:           payment.StripeSaleMoR,
				TaxLiabilityAccount: tc.offerTaxAccount,
				AmountMinor:         1000,
				Countries:           []string{"US"},
			}
			proof, err := client.ReadSingleCheckout(t.Context(), "cs_MoR", "att_1", offer)
			if err != nil || proof.Receipt == nil {
				t.Fatalf("ReadSingleCheckout failed: proof=%+v err=%v", proof, err)
			}
			r := proof.Receipt
			if r.TaxVerified != tc.wantVerified || r.TaxLiabilityAccount != tc.wantLiabilityAcct ||
				r.TaxLiabilityType != tc.wantLiabilityType || r.AutomaticTaxStatus != tc.wantAutoTaxStatus ||
				r.CustomerTaxStatus != tc.taxExempt || r.Country != "US" || r.PrincipalMinor != 1000 || !r.EligibleTerms {
				t.Fatalf("receipt mismatch: got %+v, want verified=%v acct=%q type=%q status=%q taxStatus=%q",
					r, tc.wantVerified, tc.wantLiabilityAcct, tc.wantLiabilityType, tc.wantAutoTaxStatus, tc.taxExempt)
			}
		})

		t.Run("vip/"+tc.name, func(t *testing.T) {
			client := fixtureClient(t, func(req *http.Request) (*http.Response, error) {
				switch req.URL.Path {
				case "/v1/invoices/in_MoR":
					return stripeResponse(200, fmt.Sprintf(`{
						"id":"in_MoR","parent":{"type":"subscription_details","subscription_details":{"subscription":"sub_MoR"}},
						"customer":"cus_MoR","customer_address":{"country":"US"},"customer_tax_exempt":%q,
						"status":"paid","billing_reason":"subscription_create","currency":"usd",
						"amount_due":1080,"total":1080,"total_excluding_tax":1000,"automatic_tax":%s
					}`, tc.taxExempt, tc.automaticTaxJSON))
				case "/v1/subscriptions/sub_MoR":
					return stripeResponse(200, `{
						"id":"sub_MoR","status":"active","customer":"cus_MoR",
						"managed_payments":{"enabled":true},
						"metadata":{"payment_attempt_id":"intent_1"},
						"items":{"data":[{"price":{"id":"price_MoRVIP"},"quantity":1}],"has_more":false}
					}`)
				case "/v1/invoices/in_MoR/lines":
					return stripeResponse(200, `{
						"object":"list","has_more":false,"data":[{
							"id":"il_1","quantity":1,"currency":"usd","subtotal":1000,
							"parent":{"type":"subscription_item_details","subscription_item_details":{"subscription":"sub_MoR","subscription_item":"si_1"}},
							"period":{"start":1740000000,"end":1742419200},
							"pricing":{"type":"price_details","price_details":{"price":"price_MoRVIP","product":"prod_MoRVIP"}}
						}]
					}`)
				case "/v1/invoice_payments":
					return stripeResponse(200, `{
						"object":"list","has_more":false,"data":[{
							"id":"inpay_1","currency":"usd","status":"paid","invoice":"in_MoR",
							"amount_paid":1080,"payment":{"type":"payment_intent","payment_intent":"pi_MoRVIP"}
						}]
					}`)
				case "/v1/payment_intents/pi_MoRVIP":
					return stripeResponse(200, `{
						"id":"pi_MoRVIP","latest_charge":"ch_MoRVIP","customer":"cus_MoR",
						"currency":"usd","status":"succeeded","amount_received":1080
					}`)
				case "/v1/charges/ch_MoRVIP":
					return stripeResponse(200, `{
						"id":"ch_MoRVIP","customer":"cus_MoR","currency":"usd","status":"succeeded",
						"paid":true,"captured":true,"amount_captured":1080,"created":1740000123,
						"payment_intent":"pi_MoRVIP"
					}`)
				default:
					t.Fatalf("unexpected Stripe path %s", req.URL.Path)
					return nil, nil
				}
			})
			offer := payment.StripeVIPOffer{
				Cycle: "month",
				SingleCheckoutOffer: payment.SingleCheckoutOffer{
					ProductID:           "prod_MoRVIP",
					PriceID:             "price_MoRVIP",
					SalesMode:           payment.StripeSaleMoR,
					TaxLiabilityAccount: tc.offerTaxAccount,
					AmountMinor:         1000,
					Countries:           []string{"US"},
				},
			}
			proof, err := client.ReadStripeVIPInvoice(t.Context(), "in_MoR", offer)
			if err != nil {
				t.Fatalf("ReadStripeVIPInvoice failed: %v", err)
			}
			if proof.ReviewReason != "" || proof.TaxVerified != tc.wantVerified ||
				proof.TaxLiabilityAccount != tc.wantLiabilityAcct || proof.TaxLiabilityType != tc.wantLiabilityType ||
				proof.AutomaticTaxStatus != tc.wantAutoTaxStatus || proof.CustomerTaxStatus != tc.taxExempt ||
				proof.Country != "US" || proof.PrincipalMinor != 1000 {
				t.Fatalf("invoice proof mismatch: got %+v, want verified=%v acct=%q type=%q status=%q taxStatus=%q",
					proof, tc.wantVerified, tc.wantLiabilityAcct, tc.wantLiabilityType, tc.wantAutoTaxStatus, tc.taxExempt)
			}
		})
	}
}
