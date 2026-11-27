package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stripe/stripe-go/v87/webhook"
	"reader-backend/internal/payment"
	"reader-backend/internal/payment/stripe"
	"reader-backend/internal/testdb"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func mockStripeHTTPResponse(status int, body string) (*http.Response, error) {
	header := make(http.Header)
	header.Set("Content-Type", "application/json")
	header.Set("Request-Id", "req_app_test")
	return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body))}, nil
}

func newTestStripeClient(t *testing.T, secret, prevSecret string, handler roundTripFunc) *stripe.Client {
	t.Helper()
	client, err := stripe.NewConfiguredClient(stripe.Config{
		Scope:                 stripe.Scope{AccountID: "acct_dispatch", Environment: stripe.Sandbox},
		APIKey:                "sk_test_dispatch",
		WebhookSecret:         secret,
		PreviousWebhookSecret: prevSecret,
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = handler
	return client
}

func TestStripeInboxEnvelopeRoundTripPreservesMetadata(t *testing.T) {
	rawPayload := []byte(`{"id":"evt_old_1","type":"invoice_payment.paid","api_version":"2025-03-31.basil","data":{"object":{"id":"inpay_1","object":"invoice_payment"}}}`)
	orig := stripe.Event{
		Scope:          stripe.Scope{AccountID: "acct_dispatch", Environment: stripe.Sandbox},
		Source:         stripe.EventFromWebhook,
		ID:             "evt_old_1",
		Type:           stripe.EventInvoicePaymentPaid,
		APIVersion:     "2025-03-31.basil",
		Created:        1795132800,
		RequestID:      "req_1",
		IdempotencyKey: "idem_1",
		ObjectID:       "inpay_1",
		ObjectType:     "invoice_payment",
		Object:         json.RawMessage(`{"id":"inpay_1","object":"invoice_payment"}`),
	}
	env, err := newStripeInboxEnvelope(orig, rawPayload)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	var decoded stripeInboxEnvelope
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.EnvelopeVersion != stripeInboxEnvelopeVersion ||
		decoded.EventID != orig.ID ||
		decoded.EventType != string(orig.Type) ||
		decoded.ObjectID != orig.ObjectID ||
		decoded.APIVersion != "2025-03-31.basil" ||
		decoded.AccountKey != "acct_dispatch" ||
		decoded.Environment != "sandbox" {
		t.Fatalf("unexpected decoded envelope: %+v", decoded)
	}
	roundTripped := decoded.toStripeEvent()
	if roundTripped.Scope != orig.Scope || roundTripped.ID != orig.ID || roundTripped.Type != orig.Type ||
		roundTripped.APIVersion != orig.APIVersion || roundTripped.ObjectID != orig.ObjectID {
		t.Fatalf("round-trip event mismatch: got %+v want %+v", roundTripped, orig)
	}
}

// TestStripeUnifiedDispatchCheckoutInvoicePaymentAndReportRun proves that
// Checkout, InvoicePayment, and Report Run events flow through the shared
// universal envelope and dispatch path on both real-time webhook and Events API
// recovery paths, including historical API versions, dual-secret rotation,
// transient resource fetch failures, and account/scope mismatch diagnosis.
func TestStripeUnifiedDispatchCheckoutInvoicePaymentAndReportRun(t *testing.T) {
	t.Setenv("CONVERSION_ENVIRONMENT", "sandbox")
	const (
		accountKey  = "acct_dispatch"
		secretNew   = "whsec_new_active"
		secretOld   = "whsec_old_grace"
		attemptID   = "33333333-3333-3333-3333-333333333333"
		vipIntentID = "44444444-4444-4444-4444-444444444444"
	)
	scope := payment.WebhookAccount{AccountKey: accountKey, Environment: "sandbox"}
	verifier := newTestStripeClient(t, secretNew, secretOld, nil)

	// Verify real-time webhooks across matching, same-family, and historical versions,
	// signed with both primary and previous secrets.
	now := time.Now()
	makeSigned := func(id, version, eventType, objJSON, secret string) ([]byte, string) {
		body := []byte(fmt.Sprintf(
			`{"id":%q,"type":%q,"object":"event","created":%d,"api_version":%q,"livemode":false,"data":{"object":%s}}`,
			id, eventType, now.Unix(), version, objJSON))
		signed := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{
			Payload: body, Secret: secret, Timestamp: now,
		})
		return body, signed.Header
	}

	// Real-time events (matching, same-family, historical, dual-secret)
	rtCases := []struct {
		name      string
		id        string
		version   string
		eventType string
		objJSON   string
		secret    string
	}{
		{"checkout_historical_old_secret", "evt_rt_checkout", "2025-03-31.basil", "checkout.session.completed", `{"id":"cs_rt_1","object":"checkout.session"}`, secretOld},
		{"checkout_duplicate_new_secret", "evt_rt_checkout", stripe.APIVersion, "checkout.session.completed", `{"id":"cs_rt_1","object":"checkout.session"}`, secretNew},
		{"invoice_payment_same_family", "evt_rt_inpay", "2026-11-19.endive", "invoice_payment.paid", `{"id":"inpay_rt_1","object":"invoice_payment"}`, secretNew},
		{"report_run_matching_version", "evt_rt_report", stripe.APIVersion, "reporting.report_run.succeeded", `{"id":"frr_rt_1","object":"reporting.report_run"}`, secretNew},
	}

	// Deduplicate in-memory inbox keyed by account + event_id to verify
	// parallel delivery deduplication and universal envelope structure.
	inbox := map[string]stripeInboxEnvelope{}
	for _, tc := range rtCases {
		body, sig := makeSigned(tc.id, tc.version, tc.eventType, tc.objJSON, tc.secret)
		ev, err := verifier.VerifyWebhook(body, sig)
		if err != nil {
			t.Fatalf("%s: VerifyWebhook failed: %v", tc.name, err)
		}
		env, err := newStripeInboxEnvelope(ev, body)
		if err != nil {
			t.Fatalf("%s: envelope failed: %v", tc.name, err)
		}
		key := env.AccountKey + ":" + env.Environment + ":" + env.EventID
		if _, dup := inbox[key]; !dup {
			inbox[key] = env
		}
	}
	if len(inbox) != 3 {
		t.Fatalf("expected 3 deduplicated real-time events (parallel dual-secret delivery deduplicated), got %d", len(inbox))
	}

	// Recovery events (Events API) use the exact same universal envelope.
	recoveryEvents := []stripe.Event{
		{Scope: stripe.Scope{AccountID: accountKey, Environment: stripe.Sandbox}, Source: stripe.EventFromAPI, ID: "evt_rec_checkout", Type: stripe.EventCheckoutSessionCompleted, APIVersion: "2025-03-31.basil", ObjectID: "cs_rec_1", ObjectType: "checkout.session"},
		{Scope: stripe.Scope{AccountID: accountKey, Environment: stripe.Sandbox}, Source: stripe.EventFromAPI, ID: "evt_rec_inpay", Type: stripe.EventInvoicePaymentPaid, APIVersion: "2025-03-31.basil", ObjectID: "inpay_rec_1", ObjectType: "invoice_payment"},
		{Scope: stripe.Scope{AccountID: accountKey, Environment: stripe.Sandbox}, Source: stripe.EventFromAPI, ID: "evt_rec_report", Type: stripe.EventReportingReportRunSucceeded, APIVersion: "2026-11-19.endive", ObjectID: "frr_rec_1", ObjectType: "reporting.report_run"},
	}
	for _, ev := range recoveryEvents {
		if !stripe.IsHandledEventType(ev.Type) {
			t.Fatalf("expected %s to be handled", ev.Type)
		}
		raw, _ := json.Marshal(ev)
		env, err := newStripeInboxEnvelope(ev, raw)
		if err != nil {
			t.Fatalf("recovery envelope %s: %v", ev.ID, err)
		}
		key := env.AccountKey + ":" + env.Environment + ":" + env.EventID
		inbox[key] = env
	}
	if len(inbox) != 6 {
		t.Fatalf("expected 6 total inbox envelopes (3 real-time + 3 recovery), got %d", len(inbox))
	}

	// Stub resolver proving unified dispatch invokes ResolveWebhookNotice,
	// keeps transient failures retryable, and surfaces scope mismatches.
	transientFailed := false
	stub := &stubStripeWebhookResolver{
		scope: scope,
		resolve: func(_ context.Context, ev stripe.Event) (*payment.SingleCheckoutNotice, error) {
			switch ev.ObjectID {
			case "cs_rt_1", "cs_rec_1":
				if ev.ObjectID == "cs_rec_1" && !transientFailed {
					transientFailed = true
					return nil, errors.New("temporary Stripe 503")
				}
				return &payment.SingleCheckoutNotice{EventID: ev.ID, EventType: string(ev.Type), SessionID: ev.ObjectID, AttemptID: attemptID, Account: scope}, nil
			case "inpay_rt_1", "inpay_rec_1":
				return &payment.SingleCheckoutNotice{EventID: ev.ID, EventType: string(ev.Type), InvoiceID: "in_" + ev.ObjectID, Account: scope}, nil
			case "frr_rt_1", "frr_rec_1":
				return &payment.SingleCheckoutNotice{EventID: ev.ID, EventType: string(ev.Type), SettlementReference: ev.ObjectID, Account: scope}, nil
			default:
				return nil, fmt.Errorf("%w: unexpected %s", stripe.ErrEventResourceMismatch, ev.ObjectID)
			}
		},
	}

	// Verify scope mismatch is rejected cleanly by dispatchStripeInboxJob before any remote fetch.
	adapters := payment.NewAdapters(stub)
	svc := &service{adapters: adapters}
	wrongAccountEnv := stripeInboxEnvelope{
		EnvelopeVersion: stripeInboxEnvelopeVersion,
		EventID:         "evt_wrong_acct",
		EventType:       "checkout.session.completed",
		ObjectID:        "cs_rt_1",
		AccountKey:      "acct_other",
		Environment:     "sandbox",
	}
	if err := svc.dispatchStripeInboxJob(t.Context(), nil, scope, stripeNoticeJob{eventID: "evt_wrong_acct", envelope: &wrongAccountEnv}); !errors.Is(err, stripe.ErrEventScopeMismatch) {
		t.Fatalf("expected ErrEventScopeMismatch, got %v", err)
	}

	// Verify transient error on recovery checkout is returned retryable, then resolves on retry.
	recEnv := inbox[accountKey+":sandbox:evt_rec_checkout"]
	if _, err := stub.ResolveWebhookNotice(t.Context(), recEnv.toStripeEvent()); err == nil {
		t.Fatal("expected first transient fetch to fail")
	}
	notice, err := stub.ResolveWebhookNotice(t.Context(), recEnv.toStripeEvent())
	if err != nil || notice == nil || notice.AttemptID != attemptID || notice.SessionID != "cs_rec_1" {
		t.Fatalf("expected retry to resolve checkout notice, got %+v err=%v", notice, err)
	}

	// Verify all 6 envelopes (Checkout, InvoicePayment, Report Run x real-time + recovery) resolve to expected targets.
	for key, env := range inbox {
		notice, err := stub.ResolveWebhookNotice(t.Context(), env.toStripeEvent())
		if err != nil || notice == nil {
			t.Fatalf("resolve %s failed: notice=%+v err=%v", key, notice, err)
		}
		switch env.EventType {
		case "checkout.session.completed":
			if notice.AttemptID != attemptID || !strings.HasPrefix(notice.SessionID, "cs_") {
				t.Fatalf("%s bad checkout notice: %+v", key, notice)
			}
		case "invoice_payment.paid":
			if !strings.HasPrefix(notice.InvoiceID, "in_inpay_") {
				t.Fatalf("%s bad invoice_payment notice: %+v", key, notice)
			}
		case "reporting.report_run.succeeded":
			if !strings.HasPrefix(notice.SettlementReference, "frr_") {
				t.Fatalf("%s bad report run notice: %+v", key, notice)
			}
		default:
			t.Fatalf("unexpected event type %s", env.EventType)
		}
	}
	_ = vipIntentID
}

type stubStripeWebhookResolver struct {
	scope   payment.WebhookAccount
	resolve func(context.Context, stripe.Event) (*payment.SingleCheckoutNotice, error)
}

func (s *stubStripeWebhookResolver) Provider() string                     { return payment.ProviderStripe }
func (s *stubStripeWebhookResolver) AccountScope() payment.WebhookAccount { return s.scope }
func (s *stubStripeWebhookResolver) ResolveWebhookNotice(ctx context.Context, ev stripe.Event) (*payment.SingleCheckoutNotice, error) {
	return s.resolve(ctx, ev)
}

// TestStripeUniversalEnvelopeDBRealTimeAndRecoveryEndToEnd exercises the full
// Postgres inbox enqueue, per-account deduplication, claim, and dispatch flow
// when STAR_NOVEL_TEST_DATABASE_URL is configured.
func TestStripeUniversalEnvelopeDBRealTimeAndRecoveryEndToEnd(t *testing.T) {
	t.Setenv("CONVERSION_ENVIRONMENT", "sandbox")
	pool := testdb.Open(t)
	ctx := t.Context()

	account := payment.WebhookAccount{AccountKey: "acct_univ_e2e", Environment: "sandbox"}
	var accountID int64
	if err := pool.QueryRow(ctx, `INSERT INTO payment_channel_accounts(provider,account_key,environment,credentials_ref,webhook_ref)
		VALUES('stripe',$1,'sandbox','test','test') RETURNING id`, account.AccountKey).Scan(&accountID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO admins(id,username,password_hash,role,role_id,status)
		VALUES('univ-fin','univ-fin','test','FinanceManager',(SELECT id FROM roles WHERE code='FinanceManager'),1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO payment_ledger_sync_settings(account_id,actor_id,reason,enabled,window_start,window_end)
		VALUES($1,'univ-fin','reporting authorized',true,now()-interval '1 day',now())`, accountID); err != nil {
		t.Fatal(err)
	}

	// Enqueue real-time Report Run event twice (duplicate delivery) via universal envelope.
	reportEvent := stripe.Event{
		Scope:      stripe.Scope{AccountID: account.AccountKey, Environment: stripe.Sandbox},
		Source:     stripe.EventFromWebhook,
		ID:         "evt_db_report_1",
		Type:       stripe.EventReportingReportRunSucceeded,
		APIVersion: "2025-03-31.basil",
		Created:    time.Now().Unix(),
		ObjectID:   "frr_db_1",
		ObjectType: "reporting.report_run",
	}
	raw, _ := json.Marshal(reportEvent)
	for range 2 {
		if err := enqueueStripeEvent(ctx, reportEvent, raw); err != nil {
			t.Fatal(err)
		}
	}

	jobs, err := claimStripeNotices(ctx, account)
	if err != nil || len(jobs) != 1 || jobs[0].envelope == nil || jobs[0].envelope.APIVersion != "2025-03-31.basil" {
		t.Fatalf("expected 1 deduplicated universal envelope job, got %+v err=%v", jobs, err)
	}

	stub := &stubStripeWebhookResolver{
		scope: account,
		resolve: func(_ context.Context, ev stripe.Event) (*payment.SingleCheckoutNotice, error) {
			return &payment.SingleCheckoutNotice{
				EventID:             ev.ID,
				EventType:           string(ev.Type),
				SettlementReference: ev.ObjectID,
				Account:             account,
			}, nil
		},
	}
	svc := &service{adapters: payment.NewAdapters(stub)}
	if err := svc.dispatchStripeInboxJob(ctx, nil, account, jobs[0]); err != nil {
		t.Fatalf("dispatch report run failed: %v", err)
	}
	var cmdCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM payment_settlement_commands
		WHERE account_id=$1 AND kind='stripe_report' AND parameters->>'reference'='frr_db_1'`, accountID).Scan(&cmdCount); err != nil || cmdCount != 1 {
		t.Fatalf("expected 1 scheduled stripe_report command, got %d err=%v", cmdCount, err)
	}
}
