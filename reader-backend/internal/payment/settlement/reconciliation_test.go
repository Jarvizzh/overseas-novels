package settlement_test

import (
	"crypto/sha256"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"reader-backend/internal/payment"
	"reader-backend/internal/payment/ledger"
	"reader-backend/internal/payment/settlement"
	"reader-backend/internal/payment/stripe"
	"reader-backend/internal/testdb"
)

func TestStripePayoutCanMatchAfterAllocationAndReopensReviewAfterFailure(t *testing.T) {
	pool := testdb.Open(t)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `INSERT INTO payment_channel_accounts(provider,environment,account_key,credentials_ref,webhook_ref)
		VALUES ('stripe','sandbox','acct_reconcile','test','test')`); err != nil {
		t.Fatal(err)
	}
	scope := stripe.Scope{AccountID: "acct_reconcile", Environment: stripe.Sandbox}
	account := payment.WebhookAccount{AccountKey: scope.AccountID, Environment: "sandbox"}
	audit := settlement.Audit{Operator: "finance-fixture", Reason: "bank checked"}
	repo := settlement.NewRepository(pool)
	reportRow := func(id, source, category, amount string, number int) settlement.ReportRow {
		t.Helper()
		row := stripe.ReportRow{"balance_transaction_id": id, "source_id": source, "automatic_payout_id": "po_fixture",
			"reporting_category": category, "currency": "usd", "gross": amount, "fee": "0", "net": amount}
		evidence, err := row.LedgerEvidence(scope, "frr_fixture")
		if err != nil {
			t.Fatal(err)
		}
		return settlement.ReportRow{File: "file_fixture", Number: number, Payload: evidence.Payload, Evidence: &evidence}
	}
	importWith := func(kind, revision string, payout *payment.PayoutEvidence, rows ...settlement.ReportRow) {
		t.Helper()
		report := settlement.Report{Provider: payment.ProviderStripe, Source: kind, Reference: "po_fixture",
			Account: account, Audit: audit, Fingerprint: sha256.Sum256([]byte(revision)), Payout: payout}
		_, err := repo.Import(ctx, report, func(save func(settlement.ReportRow) error) error {
			for _, row := range rows {
				if err := save(row); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	state := func(want string) settlement.PayoutStatus {
		t.Helper()
		got, err := repo.PayoutStatus(ctx, payment.ProviderStripe, account, "po_fixture")
		if err != nil || got.Status != want {
			t.Fatalf("payout status %+v, wanted %q: %v", got, want, err)
		}
		return got
	}
	payout := payment.PayoutEvidence{Reference: "po_fixture", Expected: payment.LedgerMoney{Currency: "USD", Minor: 900},
		BankReference: "TRACE", Status: "paid", TransferKey: "txn_payout", AllocationRequired: true,
		ObservedAt: time.Now(), ExpectedAt: time.Now().AddDate(0, 0, -2), Payload: json.RawMessage(`{"paid":true}`)}
	transfer := reportRow("txn_payout", "po_fixture", "payout", "-9", 1)
	importWith(settlement.StripePayout, "direct-read-unallocated", &payout, transfer)
	state("missing_movements")
	if _, err := repo.ImportBank(ctx, settlement.BankStatement{Environment: "sandbox", Account: "alias", Reference: "bank-1", Audit: audit},
		strings.NewReader("transaction_id,booked_at,currency,amount_minor,bank_reference\nb1,2026-11-21T00:00:00Z,USD,900,TRACE\n")); err != nil {
		t.Fatal(err)
	}
	request := settlement.MatchRequest{Provider: payment.ProviderStripe, Account: account, Payout: "po_fixture",
		BankAccount: "alias", BankTransaction: "b1", Audit: audit}
	if match, err := repo.MatchBank(ctx, request); err != nil || match.Outcome != settlement.MatchNotReady {
		t.Fatalf("missing allocation became bank confirmation: %+v %v", match, err)
	}
	charge := reportRow("txn_charge", "ch_fixture", "charge", "10", 1)
	refund := reportRow("txn_refund", "re_fixture", "refund", "-1", 2)
	importWith(settlement.StripeReport, "report-first", nil, charge, refund)
	state("missing_movements")
	// An earlier balance API read can have the exact same payload before
	// Stripe publishes the confirmed payout allocation.
	apiCharge := *charge.Evidence
	apiCharge.Source, apiCharge.SourceRef, apiCharge.References = payment.LedgerStripeBalance, apiCharge.MovementKey, nil
	if _, err := ledger.NewRepository(pool).Record(ctx, apiCharge); err != nil {
		t.Fatal(err)
	}
	apiCharge.References = []payment.LedgerReference{{Type: "stripe_payout_allocation", ID: "po_fixture"}}
	charge.Evidence = &apiCharge
	apiRefund := *refund.Evidence
	apiRefund.Source, apiRefund.SourceRef = payment.LedgerStripeBalance, apiRefund.MovementKey
	apiRefund.References = []payment.LedgerReference{{Type: "stripe_payout_allocation", ID: "po_fixture"}}
	refund.Evidence = &apiRefund
	payout.AllocationComplete = true
	payout.ObservedAt = payout.ObservedAt.Add(time.Second)
	importWith(settlement.StripePayout, "fully-allocated", &payout, charge, refund)
	checked := state("bank_delayed")
	if checked.MovementCount != 2 || checked.MovementNetMinor == nil || *checked.MovementNetMinor != "900" {
		t.Fatalf("payout members failed to reconcile: %+v", checked)
	}
	var group sync.WaitGroup
	for range 2 {
		group.Go(func() {
			if match, err := repo.MatchBank(ctx, request); err != nil || match.Outcome != settlement.MatchConfirmed {
				t.Errorf("bank match: %+v %v", match, err)
			}
		})
	}
	group.Wait()
	state("matched")
	payout.ObservedAt = payout.ObservedAt.Add(time.Second)
	payout.Status, payout.ReversalKey = "failed", "txn_return"
	importWith(settlement.StripePayout, "reversed", &payout, reportRow("txn_return", "po_fixture", "payout_reversal", "9", 1))
	checked = state("matched_requires_review")
	if checked.BankTransaction == nil || *checked.BankTransaction != "b1" || checked.ChannelCheck != "reversed" {
		t.Fatalf("reversal lost bank evidence: %+v", checked)
	}
}
