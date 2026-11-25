package ledger_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"reader-backend/internal/payment"
	"reader-backend/internal/payment/ledger"
	"reader-backend/internal/payment/stripe"
	"reader-backend/internal/testdb"
)

func TestStripeRefundEvidenceConvergesAcrossEntryPoints(t *testing.T) {
	pool := testdb.Open(t)
	ctx := t.Context()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO payment_channel_accounts(provider,environment,account_key,credentials_ref,webhook_ref)
		VALUES ('stripe','sandbox','acct_fixture','test','test'),('stripe','live','acct_fixture','test','test');
		INSERT INTO users(id) VALUES(910);
		INSERT INTO recharge_orders(id,user_id,payment_method,status,amount_cents) VALUES(910,910,'stripe','Success',1000),(911,910,'stripe','Success',1000);
		INSERT INTO third_party_payment_orders(id,order_id,payment_provider,external_order_id,capture_id,currency,
			gross_amount,status,account_id,object_type)
		SELECT 910+(environment='live')::int,910+(environment='live')::int,'stripe','cs_'||environment,'ch_origin','USD',10,'VERIFIED',id,'charge'
		FROM payment_channel_accounts WHERE provider='stripe'`)
	repo := ledger.NewRepository(pool)
	scope := stripe.Scope{AccountID: "acct_fixture", Environment: stripe.Sandbox}
	event := stripe.Event{Scope: scope, Source: stripe.EventFromWebhook, ID: "evt_refund",
		Type: "refund.created", ObjectType: "refund",
		Object: json.RawMessage(`{"id":"re_fixture","object":"refund","balance_transaction":"txn_refund","charge":"ch_origin","amount":400}`)}
	observation, err := event.RefundLedgerEvidence()
	if err != nil || len(observation) != 1 {
		t.Fatalf("refund observation: %v %v", observation, err)
	}
	seen, err := repo.Record(ctx, observation[0])
	if err != nil || seen.Posted {
		t.Fatalf("business event touched balance: %+v %v", seen, err)
	}
	verifyLinks := func(id int64, want int64) {
		t.Helper()
		var receipt int64
		if err := pool.QueryRow(ctx, `SELECT COALESCE((SELECT receipt_id FROM payment_ledger_receipt_matches WHERE movement_id=$1),0)`,
			id).Scan(&receipt); err != nil || receipt != want {
			t.Fatalf("ledger receipt match=%d want %d: %v", receipt, want, err)
		}
	}
	verifyLinks(seen.MovementID, 0)
	report := stripe.ReportRow{"balance_transaction_id": "txn_refund", "source_id": "re_fixture", "charge_id": "ch_origin",
		"reporting_category": "refund", "currency": "usd", "gross": "-4.00", "fee": "0.00", "net": "-4.00",
		"withheld_tax": "-0.41", "fee_net_of_withheld_tax": "0.41",
		"customer_facing_currency": "jpy", "customer_facing_amount": "-620", "exchange_rate": "0.00645161"}
	reportEvidence, err := report.LedgerEvidence(scope, "frr_fixture")
	if err != nil {
		t.Fatal(err)
	}
	api := payment.LedgerEvidence{Provider: payment.ProviderStripe, Account: reportEvidence.Account,
		MovementKey: reportEvidence.MovementKey, Source: payment.LedgerStripeBalance, SourceRef: "txn_refund",
		Payload: json.RawMessage(`{"id":"txn_refund","amount":-400,"fee":0,"net":-400}`),
		Kind:    payment.MovementRefund, ProviderCode: "refund", Currency: "USD",
		OccurredAt: time.Date(2026, 11, 20, 1, 0, 0, 0, time.UTC),
		Amounts: map[payment.LedgerComponent]payment.LedgerMoney{
			payment.LedgerGross: {Currency: "USD", Minor: -400}, payment.LedgerFee: {Currency: "USD", Minor: 0},
			payment.LedgerNet: {Currency: "USD", Minor: -400},
		},
	}
	lookup := observation[0]
	lookup.Source, lookup.SourceRef = payment.LedgerStripeRefund, "re_fixture"
	var group sync.WaitGroup
	for _, entry := range []payment.LedgerEvidence{lookup, api, reportEvidence, api, reportEvidence} {
		group.Go(func() {
			result, err := repo.Record(ctx, entry)
			if err != nil || result.Conflict || result.MovementID != seen.MovementID {
				t.Errorf("replay did not converge: %+v %v", result, err)
			}
		})
	}
	group.Wait()
	var count, evidenceCount, net, tax, buyer int64
	var rate string
	err = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM payment_ledger_movements WHERE posted_at IS NOT NULL),
		(SELECT count(*) FROM payment_ledger_evidence WHERE movement_id=$1),
		max(amount_minor) FILTER(WHERE component='net'),
		max(amount_minor) FILTER(WHERE component='withheld_tax'),
		max(amount_minor) FILTER(WHERE component='customer_facing' AND currency='JPY'),
		(SELECT exchange_rate::text FROM payment_ledger_movements WHERE id=$1)
		FROM payment_ledger_amounts WHERE movement_id=$1`, seen.MovementID).Scan(&count, &evidenceCount, &net, &tax, &buyer, &rate)
	if err != nil || count != 1 || evidenceCount != 4 || net != -400 || tax != 41 || buyer != -620 || rate != "0.00645161" {
		t.Fatalf("moves=%d evidence=%d net=%d tax=%d buyer=%d rate=%s: %v", count, evidenceCount, net, tax, buyer, rate, err)
	}
	verifyLinks(seen.MovementID, 910)
	live := reportEvidence
	live.Account.Environment = "live"
	other, err := repo.Record(ctx, live)
	if err != nil || other.MovementID == seen.MovementID {
		t.Fatalf("isolated environment: %+v %v", other, err)
	}
	verifyLinks(other.MovementID, 911)
	report["gross"], report["net"] = "-5.00", "-5.00"
	disagree, err := report.LedgerEvidence(scope, "frr_conflict")
	if err != nil {
		t.Fatal(err)
	}
	result, err := repo.Record(ctx, disagree)
	if err != nil || !result.Conflict || result.MovementID != seen.MovementID {
		t.Fatalf("conflict not held for review: %+v %v", result, err)
	}
	verifyLinks(seen.MovementID, 0)
	var original int
	if err := pool.QueryRow(ctx, `SELECT amount_minor FROM payment_ledger_amounts WHERE movement_id=$1 AND component='net'`,
		seen.MovementID).Scan(&original); err != nil || original != -400 {
		t.Fatalf("conflicting evidence replaced original net: %d %v", original, err)
	}
}

func TestInvalidAuthorityRollsBackEvidence(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `SELECT assert_paypal_identity('fixture-paypal','sandbox')`); err != nil {
		t.Fatal(err)
	}
	repo := ledger.NewRepository(pool)
	entry := payment.LedgerEvidence{Provider: payment.ProviderPayPal,
		Account:     payment.WebhookAccount{AccountKey: "fixture-paypal", Environment: "sandbox"},
		MovementKey: "paypal-row/T0002/USD", Source: payment.LedgerPayPalSearch, SourceRef: "paypal-row",
		Payload: json.RawMessage(`{"transaction_id":"paypal-row"}`), Kind: payment.MovementPayment, Currency: "USD",
		Amounts:    map[payment.LedgerComponent]payment.LedgerMoney{payment.LedgerGross: {Currency: "USD", Minor: 700}},
		References: []payment.LedgerReference{{Type: "paypal_transaction"}},
	}
	if _, err := repo.Record(ctx, entry); err == nil {
		t.Fatal("invalid link accepted")
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM payment_ledger_movements`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed record left a movement: %d %v", count, err)
	}
}
