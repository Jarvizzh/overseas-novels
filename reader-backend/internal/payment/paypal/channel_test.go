package paypal

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"reader-backend/internal/payment"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func response(status int, data string) (*http.Response, error) {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(data))}, nil
}

func channelFixture(t *testing.T, fn transportFunc) *Client {
	t.Helper()
	client, err := NewScopedClient(Config{
		ClientID: "fixture-app", Secret: "fixture-secret", Environment: EnvironmentSandbox, MerchantID: "merchant-fixture",
	})
	if err != nil {
		t.Fatal(err)
	}
	client.httpClient.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Scheme != "https" || req.URL.Host != "api-m.sandbox.paypal.com" {
			t.Fatalf("unexpected PayPal endpoint: %s", req.URL)
		}
		if req.URL.Path == "/v1/oauth2/token" {
			id, secret, ok := req.BasicAuth()
			if !ok || id != "fixture-app" || secret != "fixture-secret" {
				t.Fatal("OAuth request has incorrect scope")
			}
			return response(200, `{"access_token":"fixture-token"}`)
		}
		if req.Header.Get("Authorization") != "Bearer fixture-token" {
			t.Fatalf("missing account authorization for %s", req.URL.Path)
		}
		return fn(req)
	})
	return client
}

func TestListEventsKeepsIdentifiableIncompletePayPalEvent(t *testing.T) {
	client := channelFixture(t, func(_ *http.Request) (*http.Response, error) {
		return response(200, `{"events":[
			{"id":"WH-INCOMPLETE","event_type":"PAYMENT.CAPTURE.DECLINED","resource":null},
			{"id":"WH-PROCESSED","event_type":"PAYMENT.CAPTURE.DECLINED","resource":{"id":"capture-ref","status":"DECLINED"}}
		]}`)
	})
	from := time.Now().Add(-time.Hour)
	page, err := client.ListEvents(t.Context(), EventQuery{From: from, Through: from.Add(time.Hour)})
	if err != nil || len(page.Items) != 2 || page.Items[0].ID != "WH-INCOMPLETE" {
		t.Fatalf("event page: %+v %v", page, err)
	}
	if _, err := page.Items[0].Evidence(); err == nil {
		t.Fatal("incomplete PayPal event was accepted")
	}
	if fact, err := page.Items[1].Evidence(); err != nil || fact.EventType != "payment_status" {
		t.Fatalf("healthy event: %+v %v", fact, err)
	}
}

func TestDisputeAndReversalEvidence(t *testing.T) {
	const original = `{"dispute_id":"PP-D-TEST","status":"WAITING_FOR_SELLER_RESPONSE","dispute_life_cycle_stage":"CHARGEBACK","seller_response_due_date":"2026-11-25T15:05:00Z","disputed_transactions":[{"seller_transaction_id":"capture-origin","buyer_transaction_id":"buyer-reference"}],"links":[{"rel":"provide-evidence","method":"POST","href":"https://api-m.sandbox.paypal.com/v1/customer/disputes/PP-D-TEST/provide-evidence"}]}`
	client := channelFixture(t, func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/v1/customer/disputes/PP-D-TEST":
			return response(200, original)
		case "/v1/customer/disputes":
			q := req.URL.Query()
			if q.Get("page") == "2" {
				return response(200, `{"items":[]}`)
			}
			if q.Get("update_time_after") != "2026-11-20T00:00:00Z" || q.Get("page_size") != "5" {
				t.Fatalf("bad dispute filters: %s", req.URL)
			}
			return response(200, `{"items":[`+original+`],"links":[{"rel":"next","method":"GET","href":"https://api-m.sandbox.paypal.com/v1/customer/disputes?page=2&page_size=5"}]}`)
		case "/v1/notifications/webhooks-events/WH-REVERSE":
			return response(200, `{"id":"WH-REVERSE","event_type":"PAYMENT.SALE.REVERSED","resource_type":"sale","resource_version":"1.0","create_time":"2026-11-21T00:05:01Z","resource":{"id":"reversal-id","sale_id":"original-sale","billing_agreement_id":"I-SUB","state":"reversed","amount":{"total":"4.01","currency":"USD"}}}`)
		case "/v1/notifications/webhooks-events":
			return response(200, `{"events":[{"id":"WH-DENIED","event_type":"PAYMENT.CAPTURE.DECLINED","resource":{"id":"capture-attempt","status":"DECLINED","status_details":{"reason":"DECLINED_BY_RISK_FRAUD_FILTERS"}}}],"links":[]}`)
		default:
			t.Fatalf("unexpected dispute or event API: %s", req.URL)
			return nil, nil
		}
	})

	ctx := t.Context()
	window := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	page, err := client.ListDisputes(ctx, DisputeQuery{UpdatedFrom: window, PageSize: 5})
	if err != nil || len(page.Items) != 1 || page.Next == "" {
		t.Fatalf("unexpected dispute list: %+v, %v", page, err)
	}
	next, err := client.ListDisputes(ctx, DisputeQuery{Cursor: page.Next})
	if err != nil || next.Next != "" || len(next.Items) != 0 {
		t.Fatalf("unexpected next dispute list: %+v, %v", next, err)
	}
	dispute, err := client.GetDispute(ctx, "PP-D-TEST")
	if err != nil || dispute.Stage != "CHARGEBACK" || dispute.SellerResponseDue == nil ||
		dispute.Transactions[0].SellerTransactionID != "capture-origin" ||
		len(dispute.AvailableActions()) != 1 || dispute.AvailableActions()[0] != "provide-evidence" {
		t.Fatalf("lost dispute evidence: %+v, %v", dispute, err)
	}
	event, err := client.GetEvent(ctx, "WH-REVERSE")
	if err != nil || event.Account.AccountKey != "fixture-app" || event.ResourceVersion != "1.0" {
		t.Fatalf("lost event account or version: %+v, %v", event, err)
	}
	fact, err := event.Reversal()
	if err != nil || fact.ResourceID != "reversal-id" || fact.OriginalReceiptID != "original-sale" ||
		fact.Amount == nil || fact.Amount.Value != "4.01" || fact.SubscriptionID != "I-SUB" {
		t.Fatalf("lost reversal evidence: %+v, %v", fact, err)
	}
	events, err := client.ListEvents(ctx, EventQuery{From: window, Through: window.Add(time.Hour)})
	if err != nil || len(events.Items) != 1 {
		t.Fatalf("unexpected events page: %+v, %v", events, err)
	}
	decline, err := events.Items[0].Evidence()
	if err != nil || decline.EventType != "payment_status" || decline.ProviderReason != "DECLINED_BY_RISK_FRAUD_FILTERS" ||
		decline.RawPayload == "" {
		t.Fatalf("lost decline evidence: %+v, %v", decline, err)
	}
}

func TestPayPalFinancialQueriesKeepIndependentMoneyMovements(t *testing.T) {
	client := channelFixture(t, func(req *http.Request) (*http.Response, error) {
		if req.Header.Get("PayPal-Enforce-ISO8601-Format") != "true" {
			t.Fatal("missing PayPal ISO timestamp format")
		}
		switch req.URL.Path {
		case transactionsPath:
			query := req.URL.Query()
			if query.Get("page") != "2" || query.Get("page_size") != "3" ||
				query.Get("fields") != "transaction_info" || query.Get("balance_affecting_records_only") != "Y" {
				t.Fatalf("unexpected reporting filters: %s", req.URL)
			}
			return response(200, `{"account_number":"merchant-fixture","page":2,"total_pages":3,"transaction_details":[
				{"transaction_info":{"transaction_id":"shared-reference","transaction_event_code":"T0006","transaction_amount":{"currency_code":"USD","value":"10.00"},"fee_amount":{"currency_code":"USD","value":"-0.38"}}},
				{"transaction_info":{"transaction_id":"shared-reference","paypal_reference_id":"original-capture","paypal_reference_id_type":"TXN","transaction_event_code":"T1107","transaction_amount":{"currency_code":"USD","value":"-10.00"}}}
			]}`)
		case balancesPath:
			return response(200, `{"account_id":"merchant-fixture","balances":[{"currency":"USD","primary":true,"total_balance":{"currency_code":"USD","value":"10.00"},"withheld_balance":{"currency_code":"USD","value":"10.00"},"available_balance":{"currency_code":"USD","value":"0.00"}}]}`)
		default:
			t.Fatalf("unexpected finance API: %s", req.URL)
			return nil, nil
		}
	})
	window := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	page, err := client.ListTransactions(t.Context(), TransactionQuery{From: window, Through: window.Add(time.Hour), Page: 2, PageSize: 3})
	if err != nil || page.Page != 2 || page.TotalPages != 3 || len(page.Transactions) != 2 ||
		page.Transactions[0].Fee == nil || page.Transactions[0].Fee.Value != "-0.38" ||
		page.Transactions[0].ID != page.Transactions[1].ID || page.Transactions[0].EventCode == page.Transactions[1].EventCode ||
		page.Transactions[1].ReferenceID != "original-capture" || page.Transactions[1].Fee != nil {
		t.Fatalf("lost report movement: %+v, %v", page, err)
	}
	balances, err := client.GetBalances(t.Context(), window, "USD")
	if err != nil || len(balances.Items) != 1 || balances.Items[0].Available.Value != "0.00" ||
		balances.Items[0].Withheld.Value != "10.00" {
		t.Fatalf("lost balance evidence: %+v, %v", balances, err)
	}
}

func TestLedgerSearchPreservesPayPalMovementIdentityAndSignedAmounts(t *testing.T) {
	client := channelFixture(t, func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != transactionsPath || req.URL.Query().Get("balance_affecting_records_only") != "Y" ||
			req.URL.Query().Get("page") != "1" {
			t.Fatalf("unexpected search query: %s", req.URL)
		}
		return response(200, `{"account_number":"merchant-fixture","page":1,"total_pages":2,"total_items":5,"transaction_details":[
			{"transaction_info":{"transaction_id":"same-id","transaction_event_code":"T0002","transaction_status":"V","transaction_initiation_date":"2026-11-20T01:00:00Z","transaction_amount":{"currency_code":"USD","value":"10.00"},"fee_amount":{"currency_code":"USD","value":"-0.38"}}},
			{"transaction_info":{"transaction_id":"same-id","paypal_reference_id":"same-id","paypal_reference_id_type":"TXN","transaction_event_code":"T1107","transaction_status":"S","transaction_initiation_date":"2026-11-20T02:00:00Z","transaction_amount":{"currency_code":"USD","value":"-10.00"},"fee_amount":{"currency_code":"USD","value":"0.10"}}},
			{"transaction_info":{"transaction_id":"same-id","transaction_event_code":"T1201","transaction_status":"S","transaction_initiation_date":"2026-11-20T03:00:00Z","transaction_amount":{"currency_code":"USD","value":"-10.00"}}},
			{"transaction_info":{"transaction_id":"same-id","transaction_event_code":"T0200","transaction_status":"S","transaction_initiation_date":"2026-11-20T04:00:00Z","transaction_amount":{"currency_code":"JPY","value":"1521"}}},
			{"transaction_info":{"transaction_id":"pending","transaction_event_code":"T0002","transaction_status":"P"}}
		]}`)
	})
	from := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	page, err := client.ListLedgerMovements(t.Context(), from, from.Add(time.Hour*24), 1)
	if err != nil || !page.HasMore || len(page.Entries) != 4 {
		t.Fatalf("ledger page: %+v %v", page, err)
	}
	sale, refund, debit, fx := page.Entries[0], page.Entries[1], page.Entries[2], page.Entries[3]
	if sale.MovementKey == refund.MovementKey || refund.MovementKey == debit.MovementKey ||
		sale.Kind != payment.MovementPayment || refund.Kind != payment.MovementRefund || debit.Kind != payment.MovementDisputeDebit ||
		fx.Kind != payment.MovementFX || fx.Amounts[payment.LedgerGross].Minor != 1521 ||
		sale.Amounts[payment.LedgerNet].Minor != 962 || refund.Amounts[payment.LedgerNet].Minor != -990 {
		t.Fatalf("ledger normalization: %+v", page.Entries)
	}
	if _, present := debit.Amounts[payment.LedgerNet]; present {
		t.Fatal("net fabricated from gross without fee evidence")
	}
	if len(refund.References) != 2 || refund.References[1] != (payment.LedgerReference{Type: "paypal_transaction", ID: "same-id"}) {
		t.Fatalf("refund origin lost: %+v", refund.References)
	}
}

func TestRefundErrorsKeepProviderCodeAndOriginalKey(t *testing.T) {
	client := channelFixture(t, func(req *http.Request) (*http.Response, error) {
		if req.Method != "POST" || req.URL.Path != "/v2/payments/captures/original-capture/refund" ||
			req.Header.Get("PayPal-Request-Id") != "original-key" {
			t.Fatalf("unexpected refund request: %s %s", req.Method, req.URL)
		}
		return response(422, `{"name":"UNPROCESSABLE_ENTITY","debug_id":"test-request","message":"private message","details":[{"issue":"REFUND_TIME_LIMIT_EXCEEDED"}]}`)
	})
	_, err := client.ResolveRefund(t.Context(), payment.RefundRequest{
		Account:     payment.WebhookAccount{AccountKey: "fixture-app", Environment: EnvironmentSandbox},
		ReceiptType: payment.ReceiptCapture, ReceiptID: "original-capture", RequestKey: "original-key",
		AmountMinor: 999, Currency: "USD", Send: true,
	})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 422 || !apiErr.HasIssue("REFUND_TIME_LIMIT_EXCEEDED") ||
		apiErr.DebugID != "test-request" || strings.Contains(err.Error(), "private message") {
		t.Fatalf("lost structured PayPal error: %v", err)
	}
}

func TestCheckoutRejectsMismatchedMerchantAndPreservesEvidence(t *testing.T) {
	var payee = "merchant-fixture"
	client := channelFixture(t, func(req *http.Request) (*http.Response, error) {
		switch req.Method + " " + req.URL.Path {
		case "POST /v2/checkout/orders":
			data, err := io.ReadAll(req.Body)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), `"value":"9.99"`) ||
				!strings.Contains(string(data), `"merchant_id":"merchant-fixture"`) ||
				req.Header.Get("PayPal-Request-Id") != "create-attempt" {
				t.Fatalf("unexpected order request: %s", data)
			}
			return response(201, `{"id":"PAYPAL-ORDER","links":[{"rel":"approve","href":"https://www.sandbox.paypal.com/checkoutnow?token=PAYPAL-ORDER"}]}`)
		case "GET /v2/checkout/orders/PAYPAL-ORDER":
			return response(200, `{"id":"PAYPAL-ORDER","status":"APPROVED","purchase_units":[{"payee":{"merchant_id":"`+payee+`"},"amount":{"currency_code":"USD","value":"9.99"},"custom_id":"business-attempt"}]}`)
		case "POST /v2/checkout/orders/PAYPAL-ORDER/capture":
			if req.Header.Get("PayPal-Request-Id") != "capture-PAYPAL-ORDER" {
				t.Fatal("capture lost its stable key")
			}
			return response(201, `{"id":"PAYPAL-ORDER","status":"COMPLETED","purchase_units":[{"payee":{"merchant_id":"merchant-fixture"},"custom_id":"business-attempt","payments":{"captures":[{"id":"PAYPAL-CAPTURE","status":"COMPLETED","amount":{"currency_code":"USD","value":"9.99"},"seller_receivable_breakdown":{"paypal_fee":{"value":"0.50","currency_code":"USD"},"net_amount":{"value":"9.49","currency_code":"USD"}}}]}}]}`)
		case "GET /v2/payments/captures/PAYPAL-CAPTURE":
			return response(200, `{"id":"PAYPAL-CAPTURE","status":"PENDING","status_details":{"reason":"PENDING_REVIEW"},"amount":{"currency_code":"USD","value":"9.99"},"processor_response":{"avs_code":"Y"}}`)
		default:
			t.Fatalf("unexpected checkout API: %s %s", req.Method, req.URL)
			return nil, nil
		}
	})
	account, err := client.CheckoutAccount(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	id, url, err := client.CreateOrder(t.Context(), account, 999, "business-attempt", "create-attempt", "", "", "")
	if err != nil || id != "PAYPAL-ORDER" || url == "" {
		t.Fatalf("unexpected checkout: %q %q %v", id, url, err)
	}
	payee = "other-merchant"
	if order, err := client.GetOrder(t.Context(), id); err == nil || order.ID != "" {
		t.Fatalf("wrong payee accepted: %+v %v", order, err)
	}
	payee = "merchant-fixture"
	approved, err := client.GetCapture(t.Context(), id)
	if !errors.Is(err, ErrOrderApproved) || approved.CustomID != "business-attempt" {
		t.Fatalf("unexpected approval: %+v %v", approved, err)
	}
	receipt, err := client.CaptureOrder(t.Context(), id, account)
	if err != nil || receipt.CaptureID != "PAYPAL-CAPTURE" || receipt.NetAmount != "9.49" {
		t.Fatalf("unexpected receipt: %+v %v", receipt, err)
	}
	evidence, err := client.GetCaptureDetails(t.Context(), receipt.CaptureID)
	if err != nil || evidence.StatusDetails == nil || evidence.StatusDetails.Reason != "PENDING_REVIEW" ||
		evidence.Processor == nil || evidence.Processor.AVSCode != "Y" || evidence.Receivable != nil {
		t.Fatalf("unexpected risk evidence: %+v %v", evidence, err)
	}
}

func TestGetCaptureDistinguishesCreatedOrder(t *testing.T) {
	client := channelFixture(t, func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet || req.URL.Path != "/v2/checkout/orders/PAYPAL-CREATED" {
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL)
		}
		return response(200, `{"id":"PAYPAL-CREATED","status":"CREATED","purchase_units":[{"payee":{"merchant_id":"merchant-fixture"},"amount":{"currency_code":"USD","value":"9.99"},"custom_id":"attempt-created"}]}`)
	})
	if _, err := client.GetCapture(t.Context(), "PAYPAL-CREATED"); !errors.Is(err, ErrOrderCreated) {
		t.Fatalf("expected ErrOrderCreated, got %v", err)
	}
}

func TestPayPalRefundAmountsUseCurrencyScale(t *testing.T) {
	if got := formatAmount(999, "JPY"); got != "999" {
		t.Fatalf("unexpected JPY request amount: %s", got)
	}
	for _, tc := range []struct {
		value, currency string
		minor           int64
	}{
		{"9.990", "USD", 999},
		{"999", "JPY", 999},
	} {
		if got, err := parseAmountMinor(tc.value, tc.currency); err != nil || got != tc.minor {
			t.Errorf("%s %s returned %d, %v", tc.value, tc.currency, got, err)
		}
	}
	for _, value := range []string{"9.999", "-9.99", "9.90x"} {
		if _, err := parseAmountMinor(value, "USD"); err == nil {
			t.Errorf("accepted invalid USD amount %s", value)
		}
	}
}

func TestVerifyVIPPlanRequiresExactRecurringTerms(t *testing.T) {
	const plan = `{"id":"P-TEST","status":"ACTIVE","billing_cycles":[{"tenure_type":"REGULAR","total_cycles":0,
		"frequency":{"interval_unit":"MONTH","interval_count":1},
		"pricing_scheme":{"fixed_price":{"value":"9.99","currency_code":"USD"}}}]}`
	for _, tc := range []struct {
		name, body string
		valid      bool
	}{
		{"exact", plan, true},
		{"wrong price", strings.Replace(plan, "9.99", "19.99", 1), false},
		{"wrong cycle", strings.Replace(plan, "MONTH", "WEEK", 1), false},
		{"finite", strings.Replace(plan, `"total_cycles":0`, `"total_cycles":1`, 1), false},
		{"setup fee", strings.TrimSuffix(plan, "}") + `,"payment_preferences":{"setup_fee":{"value":"0.01","currency_code":"USD"}}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := channelFixture(t, func(req *http.Request) (*http.Response, error) {
				if req.Method != http.MethodGet || req.URL.Path != "/v1/billing/plans/P-TEST" {
					t.Fatalf("unexpected Plan verification request: %s %s", req.Method, req.URL)
				}
				return response(http.StatusOK, tc.body)
			})
			account, err := client.CheckoutAccount(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			err = client.VerifyVIPPlan(t.Context(), "P-TEST", payment.CreateSubscriptionPlanParam{
				Account: account, Cycle: payment.CycleMonth, Currency: "USD", PriceCents: 999,
			})
			if (err == nil) != tc.valid {
				t.Fatalf("verify terms: %v", err)
			}
		})
	}
}

func TestSubscriptionWritesKeepAccountAndSeparateRequestKeys(t *testing.T) {
	var planKeys []string
	client := channelFixture(t, func(req *http.Request) (*http.Response, error) {
		switch req.Method + " " + req.URL.Path {
		case "POST /v1/catalogs/products":
			return response(201, `{"id":"PROD-VIP"}`)
		case "POST /v1/billing/plans":
			key := req.Header.Get("PayPal-Request-Id")
			if key == "" || key == "vip-attempt" {
				t.Fatal("plan request was not scoped to its own operation")
			}
			planKeys = append(planKeys, key)
			return response(201, `{"id":"P-VIP"}`)
		case "POST /v1/billing/subscriptions":
			if req.Header.Get("PayPal-Request-Id") != "vip-attempt" {
				t.Fatal("subscription request lost the original key")
			}
			return response(201, `{"id":"I-VIP","status":"APPROVAL_PENDING"}`)
		default:
			t.Fatalf("unexpected subscription API: %s %s", req.Method, req.URL)
			return nil, nil
		}
	})
	scope, err := client.CheckoutAccount(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	request := payment.CreateSubscriptionPlanParam{Account: scope, RequestID: "vip-attempt", PlanName: "VIP",
		Cycle: payment.CycleMonth, Currency: "USD", PriceCents: 999}
	for range 2 {
		if id, err := client.CreatePlan(t.Context(), request); err != nil || id != "P-VIP" {
			t.Fatalf("unexpected published plan %q %v", id, err)
		}
	}
	if len(planKeys) != 2 || planKeys[0] != planKeys[1] {
		t.Fatal("plan retry changed the provider key")
	}
	sub := payment.CreateSubscriptionParam{Account: scope, RequestID: "vip-attempt", PlanID: "P-VIP"}
	sub.Account.Environment = "live"
	if result, err := client.CreateSubscription(t.Context(), sub); result != nil || err == nil {
		t.Fatalf("wrong account accepted: %+v %v", result, err)
	}
	sub.Account = scope
	if result, err := client.CreateSubscription(t.Context(), sub); err != nil || result.SubscriptionID != "I-VIP" {
		t.Fatalf("unexpected subscription response: %+v %v", result, err)
	}
}

func TestCaptureOutcomeDistinguishesProviderFailures(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"funding", 422, `{"details":[{"issue":"INSTRUMENT_DECLINED"}]}`, payment.ErrCheckoutDeclined},
		{"risk", 422, `{"details":[{"issue":"PAYEE_BLOCKED_TRANSACTION"}]}`, payment.ErrCheckoutRiskBlocked},
		{"verification", 422, `{"details":[{"issue":"PAYER_ACTION_REQUIRED"}]}`, payment.ErrCheckoutVerificationRequired},
		{"technical", 503, `{}`, payment.ErrCheckoutTechnicalFailure},
		{"risk capture", 200, `{"id":"PP-ORDER","status":"COMPLETED","purchase_units":[{"payee":{"merchant_id":"merchant-fixture"},"payments":{"captures":[{"status":"DECLINED","status_details":{"reason":"DECLINED_BY_RISK_FRAUD_FILTERS"}}]}}]}`, payment.ErrCheckoutRiskBlocked},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := channelFixture(t, func(req *http.Request) (*http.Response, error) {
				if req.Method != http.MethodPost || req.URL.Path != "/v2/checkout/orders/PP-ORDER/capture" {
					t.Fatalf("unexpected request: %s %s", req.Method, req.URL)
				}
				return response(test.status, test.body)
			})
			account, err := client.CheckoutAccount(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			capture, err := client.CaptureOrder(t.Context(), "PP-ORDER", account)
			if capture != nil || !errors.Is(err, test.want) {
				t.Fatalf("capture=%+v err=%v, want %v", capture, err, test.want)
			}
		})
	}
}
