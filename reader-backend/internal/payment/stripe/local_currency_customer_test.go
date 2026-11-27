package stripe

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"testing"

	"reader-backend/internal/payment"
)

func TestCreateCheckoutAdaptivePricingCustomerAndSavedPaymentMethod(t *testing.T) {
	t.Run("direct default disables adaptive pricing and keeps billing address required", func(t *testing.T) {
		var form url.Values
		client := fixtureClient(t, func(req *http.Request) (*http.Response, error) {
			if req.URL.Path == "/v1/checkout/sessions" {
				body, _ := io.ReadAll(req.Body)
				form, _ = url.ParseQuery(string(body))
				return stripeResponse(200, `{"id":"cs_DirectOff","mode":"payment","status":"open","payment_status":"unpaid","url":"https://checkout.stripe.com/c/pay/cs_DirectOff","client_reference_id":"att-1","metadata":{"payment_attempt_id":"att-1"}}`)
			}
			t.Fatalf("unexpected path %s", req.URL.Path)
			return nil, nil
		})
		_, err := client.CreateCheckout(t.Context(), CheckoutRequest{
			SalesMode:                Direct,
			CheckoutMode:             OneTime,
			PriceID:                  "price_USD1",
			AttemptID:                "att-1",
			SuccessURL:               "https://reader.example.invalid/ok",
			CancelURL:                "https://reader.example.invalid/cancel",
			IdempotencyKey:           "att-1",
			BillingAddressCollection: payment.BillingAddressCollectionRequired,
		})
		if err != nil {
			t.Fatalf("CreateCheckout error: %v", err)
		}
		if got := form.Get("adaptive_pricing[enabled]"); got != "false" {
			t.Fatalf("adaptive_pricing[enabled] = %q, want false", got)
		}
		if got := form.Get("billing_address_collection"); got != "required" {
			t.Fatalf("billing_address_collection = %q, want required", got)
		}
		if got := form.Get("saved_payment_method_options[payment_method_save]"); got != "" {
			t.Fatalf("expected saved_payment_method_options unset by default, got %q", got)
		}
	})

	t.Run("direct enabled adaptive pricing, customer reuse, saved payment method, simplified address", func(t *testing.T) {
		var form url.Values
		client := fixtureClient(t, func(req *http.Request) (*http.Response, error) {
			if req.URL.Path == "/v1/checkout/sessions" {
				body, _ := io.ReadAll(req.Body)
				form, _ = url.ParseQuery(string(body))
				return stripeResponse(200, `{"id":"cs_DirectOn","mode":"payment","status":"open","payment_status":"unpaid","customer":"cus_Verified01","url":"https://checkout.stripe.com/c/pay/cs_DirectOn","client_reference_id":"att-2","metadata":{"payment_attempt_id":"att-2"}}`)
			}
			t.Fatalf("unexpected path %s", req.URL.Path)
			return nil, nil
		})
		_, err := client.CreateCheckout(t.Context(), CheckoutRequest{
			SalesMode:                Direct,
			CheckoutMode:             OneTime,
			PriceID:                  "price_USD1",
			AttemptID:                "att-2",
			SuccessURL:               "https://reader.example.invalid/ok",
			CancelURL:                "https://reader.example.invalid/cancel",
			IdempotencyKey:           "att-2",
			AdaptivePricingEnabled:   true,
			BillingAddressCollection: payment.BillingAddressCollectionAuto,
			CustomerID:               "cus_Verified01",
			SavePaymentMethodEnabled: true,
			SetupFutureUsage:         payment.SetupFutureUsageOnSession,
		})
		if err != nil {
			t.Fatalf("CreateCheckout error: %v", err)
		}
		if got := form.Get("adaptive_pricing[enabled]"); got != "true" {
			t.Fatalf("adaptive_pricing[enabled] = %q, want true", got)
		}
		if got := form.Get("customer"); got != "cus_Verified01" {
			t.Fatalf("customer = %q, want cus_Verified01", got)
		}
		if got := form.Get("billing_address_collection"); got != "auto" {
			t.Fatalf("billing_address_collection = %q, want auto", got)
		}
		if got := form.Get("saved_payment_method_options[payment_method_save]"); got != "enabled" {
			t.Fatalf("saved_payment_method_options[payment_method_save] = %q, want enabled", got)
		}
		if got := form.Get("payment_intent_data[setup_future_usage]"); got != "on_session" {
			t.Fatalf("payment_intent_data[setup_future_usage] = %q, want on_session", got)
		}
	})

	t.Run("MoR leaves adaptive pricing controlled by Stripe and omits setup_future_usage", func(t *testing.T) {
		var form url.Values
		client := fixtureClient(t, func(req *http.Request) (*http.Response, error) {
			if req.URL.Path == "/v1/checkout/sessions" {
				body, _ := io.ReadAll(req.Body)
				form, _ = url.ParseQuery(string(body))
				return stripeResponse(200, `{"id":"cs_MoR1","mode":"payment","status":"open","payment_status":"unpaid","managed_payments":{"enabled":true},"customer":"cus_MoR01","url":"https://checkout.stripe.com/c/pay/cs_MoR1","client_reference_id":"att-3","metadata":{"payment_attempt_id":"att-3"}}`)
			}
			t.Fatalf("unexpected path %s", req.URL.Path)
			return nil, nil
		})
		_, err := client.CreateCheckout(t.Context(), CheckoutRequest{
			SalesMode:                MoR,
			CheckoutMode:             OneTime,
			PriceID:                  "price_MoR1",
			AttemptID:                "att-3",
			SuccessURL:               "https://reader.example.invalid/ok",
			CancelURL:                "https://reader.example.invalid/cancel",
			IdempotencyKey:           "att-3",
			AdaptivePricingEnabled:   true, // must be ignored for MoR
			BillingAddressCollection: payment.BillingAddressCollectionRequired,
			CustomerID:               "cus_MoR01",
			SavePaymentMethodEnabled: true,
			SetupFutureUsage:         payment.SetupFutureUsageOnSession,
		})
		if err != nil {
			t.Fatalf("CreateCheckout MoR error: %v", err)
		}
		if form.Has("adaptive_pricing[enabled]") {
			t.Fatalf("MoR must not send adaptive_pricing[enabled], got %q", form.Get("adaptive_pricing[enabled]"))
		}
		if got := form.Get("managed_payments[enabled]"); got != "true" {
			t.Fatalf("managed_payments[enabled] = %q, want true", got)
		}
		if form.Has("payment_intent_data[setup_future_usage]") {
			t.Fatalf("MoR must not send payment_intent_data[setup_future_usage], got %q", form.Get("payment_intent_data[setup_future_usage]"))
		}
	})

	t.Run("saved payment method without customer fails closed", func(t *testing.T) {
		client := fixtureClient(t, nil)
		_, err := client.CreateCheckout(t.Context(), CheckoutRequest{
			SalesMode:                Direct,
			CheckoutMode:             OneTime,
			PriceID:                  "price_USD1",
			AttemptID:                "att-4",
			SuccessURL:               "https://reader.example.invalid/ok",
			CancelURL:                "https://reader.example.invalid/cancel",
			IdempotencyKey:           "att-4",
			SavePaymentMethodEnabled: true,
		})
		if err == nil {
			t.Fatal("expected error when SavePaymentMethodEnabled is true without CustomerID")
		}
	})
}

func TestReadSingleCheckoutPresentmentCurrenciesAndZeroDecimalJPY(t *testing.T) {
	cases := []struct {
		name               string
		salesMode          string
		country            string
		sessionPresentment string
		intentPresentment  string
		wantCurrency       string
		wantAmount         *int64
		wantEligible       bool
		wantLocal          bool
	}{
		{
			name:         "USD baseline without presentment details",
			salesMode:    payment.StripeSaleDirect,
			country:      "US",
			wantCurrency: "",
			wantAmount:   nil,
			wantEligible: true,
			wantLocal:    false,
		},
		{
			name:               "GBP direct local currency",
			salesMode:          payment.StripeSaleDirect,
			country:            "GB",
			sessionPresentment: `,"presentment_details":{"presentment_currency":"gbp","presentment_amount":799}`,
			intentPresentment:  `,"presentment_details":{"presentment_currency":"gbp","presentment_amount":799}`,
			wantCurrency:       "GBP",
			wantAmount:         int64Ptr(799),
			wantEligible:       true,
			wantLocal:          true,
		},
		{
			name:               "CAD MoR local currency",
			salesMode:          payment.StripeSaleMoR,
			country:            "CA",
			sessionPresentment: `,"presentment_details":{"presentment_currency":"cad","presentment_amount":1495}`,
			intentPresentment:  `,"presentment_details":{"presentment_currency":"cad","presentment_amount":1495}`,
			wantCurrency:       "CAD",
			wantAmount:         int64Ptr(1495),
			wantEligible:       true,
			wantLocal:          true,
		},
		{
			name:               "JPY zero-decimal local currency",
			salesMode:          payment.StripeSaleDirect,
			country:            "JP",
			sessionPresentment: `,"presentment_details":{"presentment_currency":"jpy","presentment_amount":1500}`,
			intentPresentment:  `,"presentment_details":{"presentment_currency":"jpy","presentment_amount":1500}`,
			wantCurrency:       "JPY",
			wantAmount:         int64Ptr(1500),
			wantEligible:       true,
			wantLocal:          true,
		},
		{
			name:               "conflicting checkout and payment intent presentment fails eligibility",
			salesMode:          payment.StripeSaleDirect,
			country:            "GB",
			sessionPresentment: `,"presentment_details":{"presentment_currency":"gbp","presentment_amount":799}`,
			intentPresentment:  `,"presentment_details":{"presentment_currency":"gbp","presentment_amount":850}`,
			wantCurrency:       "GBP",
			wantAmount:         int64Ptr(799),
			wantEligible:       false,
			wantLocal:          true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			managed := tc.salesMode == payment.StripeSaleMoR
			managedJSON := `{"enabled":false}`
			taxAmount := int64(0)
			subtotal := int64(999)
			total := int64(999)
			autoTax := `{"enabled":false,"status":null}`
			if managed {
				managedJSON = `{"enabled":true}`
				taxAmount = 100
				total = 1099
				autoTax = `{"enabled":true,"status":"complete","liability":{"type":"account","account":"acct_StripeMoR01"}}`
			}
			client := fixtureClient(t, func(req *http.Request) (*http.Response, error) {
				switch req.URL.Path {
				case "/v1/checkout/sessions/cs_Local1":
					return stripeResponse(200, fmt.Sprintf(`{
						"id":"cs_Local1","mode":"payment","status":"complete","payment_status":"paid",
						"currency":"usd","amount_total":%d,"livemode":false,"customer":"cus_Buyer01",
						"client_reference_id":"att-local","metadata":{"payment_attempt_id":"att-local"},
						"payment_intent":"pi_Local1","managed_payments":%s,
						"customer_details":{"tax_exempt":"none","address":{"country":%q}},
						"automatic_tax":%s%s
					}`, total, managedJSON, tc.country, autoTax, tc.sessionPresentment))
				case "/v1/payment_intents/pi_Local1":
					return stripeResponse(200, fmt.Sprintf(`{
						"id":"pi_Local1","status":"succeeded","currency":"usd","amount_received":%d,
						"livemode":false,"customer":"cus_Buyer01","latest_charge":"ch_Local1",
						"metadata":{"payment_attempt_id":"att-local"},"managed_payments":%s%s
					}`, total, managedJSON, tc.intentPresentment))
				case "/v1/charges/ch_Local1":
					return stripeResponse(200, fmt.Sprintf(`{
						"id":"ch_Local1","payment_intent":"pi_Local1","status":"succeeded","currency":"usd",
						"amount_captured":%d,"amount_refunded":0,"captured":true,"paid":true,"created":1795136400,
						"livemode":false,"customer":"cus_Buyer01","balance_transaction":"txn_Local1"
					}`, total))
				case "/v1/checkout/sessions/cs_Local1/line_items":
					return stripeResponse(200, fmt.Sprintf(`{
						"object":"list","has_more":false,"data":[{
							"id":"li_1","quantity":1,"currency":"usd","amount_subtotal":%d,"amount_tax":%d,
							"amount_discount":0,"amount_total":%d,"price":{"id":"price_USD1"}
						}]
					}`, subtotal, taxAmount, total))
				}
				t.Fatalf("unexpected path %s", req.URL.Path)
				return nil, nil
			})
			offer := payment.SingleCheckoutOffer{
				ProductID:   "prod_USD1",
				PriceID:     "price_USD1",
				SalesMode:   tc.salesMode,
				AmountMinor: 999,
				Countries:   []string{"US", "GB", "CA", "JP"},
			}
			proof, err := client.ReadSingleCheckout(t.Context(), "cs_Local1", "att-local", offer)
			if err != nil {
				t.Fatalf("ReadSingleCheckout error: %v", err)
			}
			if proof.Receipt == nil {
				t.Fatal("expected receipt")
			}
			r := proof.Receipt
			if r.PresentmentCurrency != tc.wantCurrency {
				t.Fatalf("PresentmentCurrency = %q, want %q", r.PresentmentCurrency, tc.wantCurrency)
			}
			if (r.PresentmentAmountMinor == nil) != (tc.wantAmount == nil) ||
				(r.PresentmentAmountMinor != nil && *r.PresentmentAmountMinor != *tc.wantAmount) {
				t.Fatalf("PresentmentAmountMinor = %v, want %v", r.PresentmentAmountMinor, tc.wantAmount)
			}
			if r.EligibleTerms != tc.wantEligible {
				t.Fatalf("EligibleTerms = %v, want %v", r.EligibleTerms, tc.wantEligible)
			}
			if r.IsLocalCurrency() != tc.wantLocal {
				t.Fatalf("IsLocalCurrency() = %v, want %v", r.IsLocalCurrency(), tc.wantLocal)
			}
			if r.CustomerID != "cus_Buyer01" {
				t.Fatalf("CustomerID = %q, want cus_Buyer01", r.CustomerID)
			}
			if r.PrincipalMinor != 999 {
				t.Fatalf("PrincipalMinor = %d, want 999", r.PrincipalMinor)
			}
		})
	}
}

func TestReadStripeVIPInvoiceMultiAllocationPresentmentFromPaymentIntents(t *testing.T) {
	client := fixtureClient(t, func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/v1/invoices/in_Multi1":
			return stripeResponse(200, `{
				"id":"in_Multi1","livemode":false,"customer":"cus_VIP1","currency":"usd",
				"billing_reason":"subscription_cycle","status":"paid","amount_due":1099,"amount_remaining":0,
				"total":1099,"total_excluding_tax":999,"amount_paid":1099,"amount_paid_off_stripe":0,
				"customer_tax_exempt":"none","customer_address":{"country":"JP"},
				"automatic_tax":{"enabled":true,"status":"complete","liability":{"type":"account","account":"acct_StripeMoR01"}},
				"parent":{"subscription_details":{"subscription":"sub_VIP1"}}
			}`)
		case "/v1/subscriptions/sub_VIP1":
			return stripeResponse(200, `{
				"id":"sub_VIP1","livemode":false,"customer":"cus_VIP1","status":"active",
				"metadata":{"payment_attempt_id":"intent-vip-1"},"managed_payments":{"enabled":true},
				"items":{"has_more":false,"data":[{"quantity":1,"price":{"id":"price_VIP1"}}]}
			}`)
		case "/v1/invoices/in_Multi1/lines":
			return stripeResponse(200, `{
				"object":"list","has_more":false,"data":[{
					"id":"il_1","quantity":1,"currency":"usd","subtotal":999,
					"period":{"start":1795136400,"end":1797728400},
					"pricing":{"price_details":{"price":"price_VIP1","product":"prod_VIP1"}},
					"parent":{"subscription_item_details":{"subscription":"sub_VIP1","proration":false}}
				}]
			}`)
		case "/v1/invoice_payments":
			return stripeResponse(200, `{
				"object":"list","has_more":false,"data":[
					{"id":"inpay_1","invoice":"in_Multi1","livemode":false,"status":"paid","currency":"usd","amount_paid":600,"payment":{"payment_intent":"pi_VIP1"}},
					{"id":"inpay_2","invoice":"in_Multi1","livemode":false,"status":"paid","currency":"usd","amount_paid":499,"payment":{"payment_intent":"pi_VIP2"}}
				]
			}`)
		case "/v1/payment_intents/pi_VIP1":
			return stripeResponse(200, `{"id":"pi_VIP1","livemode":false,"status":"succeeded","customer":"cus_VIP1","currency":"usd","amount_received":600,"latest_charge":"ch_VIP1","presentment_details":{"presentment_currency":"jpy","presentment_amount":900}}`)
		case "/v1/payment_intents/pi_VIP2":
			return stripeResponse(200, `{"id":"pi_VIP2","livemode":false,"status":"succeeded","customer":"cus_VIP1","currency":"usd","amount_received":499,"latest_charge":"ch_VIP2","presentment_details":{"presentment_currency":"jpy","presentment_amount":750}}`)
		case "/v1/charges/ch_VIP1":
			return stripeResponse(200, `{"id":"ch_VIP1","livemode":false,"customer":"cus_VIP1","payment_intent":"pi_VIP1","currency":"usd","amount_captured":600,"amount_refunded":0,"captured":true,"paid":true,"status":"succeeded","created":1795136400,"balance_transaction":"txn_VIP1"}`)
		case "/v1/charges/ch_VIP2":
			return stripeResponse(200, `{"id":"ch_VIP2","livemode":false,"customer":"cus_VIP1","payment_intent":"pi_VIP2","currency":"usd","amount_captured":499,"amount_refunded":0,"captured":true,"paid":true,"status":"succeeded","created":1795136401,"balance_transaction":"txn_VIP2"}`)
		}
		t.Fatalf("unexpected path %s", req.URL.Path)
		return nil, nil
	})
	offer := payment.StripeVIPOffer{
		SingleCheckoutOffer: payment.SingleCheckoutOffer{
			ProductID:   "prod_VIP1",
			PriceID:     "price_VIP1",
			SalesMode:   payment.StripeSaleMoR,
			AmountMinor: 999,
			Countries:   []string{"JP"},
		},
		Cycle: "month",
	}
	inv, err := client.ReadStripeVIPInvoice(t.Context(), "in_Multi1", offer)
	if err != nil {
		t.Fatalf("ReadStripeVIPInvoice error: %v", err)
	}
	if inv.ReviewReason != "" || inv.PrincipalMinor != 999 || len(inv.Allocations) != 2 {
		t.Fatalf("unexpected invoice proof: %+v", inv)
	}
	if inv.Allocations[0].Receipt.PresentmentCurrency != "JPY" || inv.Allocations[0].Receipt.PresentmentAmountMinor == nil || *inv.Allocations[0].Receipt.PresentmentAmountMinor != 900 {
		t.Fatalf("allocation 0 presentment mismatch: %+v", inv.Allocations[0].Receipt)
	}
	if inv.Allocations[1].Receipt.PresentmentCurrency != "JPY" || inv.Allocations[1].Receipt.PresentmentAmountMinor == nil || *inv.Allocations[1].Receipt.PresentmentAmountMinor != 750 {
		t.Fatalf("allocation 1 presentment mismatch: %+v", inv.Allocations[1].Receipt)
	}
}

func TestGetLedgerMovementExtractsWithheldTaxAndProcessingFee(t *testing.T) {
	client := fixtureClient(t, func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/v1/balance_transactions/txn_MoR1" {
			return stripeResponse(200, `{
				"id":"txn_MoR1","object":"balance_transaction","created":1795136400,
				"reporting_category":"charge","source":"ch_MoR1","currency":"usd",
				"amount":1099,"fee":160,"net":939,"exchange_rate":1.25125,
				"fee_details":[
					{"amount":60,"currency":"usd","type":"stripe_fee"},
					{"amount":100,"currency":"usd","type":"withheld_tax"}
				]
			}`)
		}
		t.Fatalf("unexpected path %s", req.URL.Path)
		return nil, nil
	})
	entry, err := client.GetLedgerMovement(t.Context(), "txn_MoR1")
	if err != nil {
		t.Fatalf("GetLedgerMovement error: %v", err)
	}
	if entry.Amounts[payment.LedgerGross].Minor != 1099 ||
		entry.Amounts[payment.LedgerFee].Minor != -160 ||
		entry.Amounts[payment.LedgerWithheldTax].Minor != -100 ||
		entry.Amounts[payment.LedgerProcessingFee].Minor != -60 ||
		entry.Amounts[payment.LedgerNet].Minor != 939 ||
		entry.ExchangeRate != "1.25125" {
		t.Fatalf("unexpected balance breakdown: %+v", entry)
	}
}

func TestCreateCustomerIdempotentAndScoped(t *testing.T) {
	var seenKey string
	var seenForm url.Values
	client := fixtureClient(t, func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodPost && req.URL.Path == "/v1/customers" {
			seenKey = req.Header.Get("Idempotency-Key")
			body, _ := io.ReadAll(req.Body)
			seenForm, _ = url.ParseQuery(string(body))
			return stripeResponse(200, `{"id":"cus_New01","object":"customer","livemode":false}`)
		}
		t.Fatalf("unexpected request %s %s", req.Method, req.URL.Path)
		return nil, nil
	})
	id, err := client.CreateCustomer(t.Context(), 42, "reader@example.com", "stripe-customer:sandbox:acct_1:42")
	if err != nil {
		t.Fatalf("CreateCustomer error: %v", err)
	}
	if id != "cus_New01" || seenKey != "stripe-customer:sandbox:acct_1:42" ||
		seenForm.Get("email") != "reader@example.com" || seenForm.Get("metadata[reader_user_id]") != "42" {
		t.Fatalf("unexpected CreateCustomer result id=%q key=%q form=%v", id, seenKey, seenForm)
	}
}

func int64Ptr(v int64) *int64 { return &v }
