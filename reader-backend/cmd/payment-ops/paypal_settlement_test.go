package main

import (
	"crypto/sha256"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"reader-backend/internal/payment"
	"reader-backend/internal/payment/ledger"
	"reader-backend/internal/payment/paypal"
	"reader-backend/internal/payment/settlement"
	"reader-backend/internal/testdb"
)

func TestPayPalSettlementAndBankImport(t *testing.T) {
	ctx := t.Context()
	pool := testdb.Open(t)
	if _, err := pool.Exec(ctx, `SELECT assert_paypal_identity('merchant-fixture','sandbox')`); err != nil {
		t.Fatal(err)
	}
	account := payment.WebhookAccount{AccountKey: "merchant-fixture", Environment: "sandbox"}
	audit := settlement.Audit{Operator: "finance-fixture", Reason: "validate independent evidence"}
	repo := settlement.NewRepository(pool)
	booked := time.Date(2026, 11, 20, 1, 0, 0, 0, time.UTC)
	// Transaction Search arrives before the report and has yet to supply fees.
	preexisting, err := ledger.NewRepository(pool).Record(ctx, payment.LedgerEvidence{
		Provider: payment.ProviderPayPal, Account: account, MovementKey: "C1/T0006/USD",
		Source: payment.LedgerPayPalSearch, SourceRef: "C1/T0006/USD", Payload: json.RawMessage(`{"id":"C1"}`),
		Kind: payment.MovementPayment, Currency: "USD", ProviderCode: "T0006", OccurredAt: booked,
		Amounts: map[payment.LedgerComponent]payment.LedgerMoney{payment.LedgerGross: {Currency: "USD", Minor: 1000}},
	})
	if err != nil {
		t.Fatal(err)
	}
	const report = `RH,2026/11/21 00:00:00 +0000,A,recipient,005
FH,1
SH,2026/11/20 00:00:00 +0000,2026/11/21 00:00:00 +0000,merchant,
CH,transaction_id,transaction_event_code,paypal_reference_id,paypal_reference_id_type,transaction_debit_or_credit,gross_transaction_amount,gross_transaction_currency,fee_debit_or_credit,fee_amount,fee_currency,bank_reference_id,transaction_initiation_date
SB,C1,T0006,,,CR,1000,USD,DR,38,USD,,2026/11/20 01:00:00 +0000
SB,W1,T0400,,,DR,10000,USD,,,,BANK-TRACE,
SB,,T0100,,,DR,500,USD,,,,,
SC,3
RC,3
FF,3
`
	const name = "STL-20261121.A.01.01.005.CSV"
	importFile := func(payload string) (settlement.ImportResult, error) {
		return repo.Import(ctx, settlement.Report{Provider: payment.ProviderPayPal, Source: settlement.PayPalReport,
			Reference: name, Account: account, Audit: audit, Fingerprint: sha256.Sum256([]byte(payload))},
			func(save func(settlement.ReportRow) error) error {
				return settlement.ReadPayPalReport(ctx, account, "merchant", []paypal.SettlementFile{{Name: name, Body: strings.NewReader(payload)}}, save)
			})
	}
	if _, err := importFile(strings.Replace(report, "FF,3", "FF,4", 1)); err == nil {
		t.Fatal("truncated or inconsistent report was accepted")
	}
	if _, err := importFile(strings.Replace(report, "DR,10000,USD", "DR,10000,EUR", 1)); err == nil {
		t.Fatal("non-USD report was accepted")
	}
	if _, err := repo.PayoutStatus(ctx, payment.ProviderPayPal, account, "W1/T0400/USD"); err == nil {
		t.Fatal("incomplete report exposed a payout")
	}
	first, err := importFile(report)
	if err != nil || first.Replay || first.Rows != 3 || first.Review != 1 {
		t.Fatalf("report result: %+v %v", first, err)
	}
	second, err := importFile(report)
	if err != nil || !second.Replay || second.ImportID != first.ImportID {
		t.Fatalf("report replay: %+v %v", second, err)
	}
	var net, movements int64
	if err := pool.QueryRow(ctx, `SELECT amount_minor,(SELECT count(*) FROM payment_ledger_movements)
		FROM payment_ledger_amounts WHERE movement_id=$1 AND component='net'`, preexisting.MovementID).Scan(&net, &movements); err != nil ||
		net != 962 || movements != 2 {
		t.Fatalf("report failed to merge with same ledger movement: net=%d count=%d %v", net, movements, err)
	}
	const statement = `transaction_id,booked_at,currency,amount_minor,bank_reference
wrong-ref,2026-11-21T01:00:00Z,USD,10000,OTHER
wrong-amount,2026-11-21T01:00:00Z,USD,10002,BANK-TRACE
right,2026-11-21T01:00:00Z,USD,10000,BANK-TRACE
`
	bank := settlement.BankStatement{Environment: "sandbox", Account: "test-bank", Reference: "statement-2026", Audit: audit}
	if _, err := repo.ImportBank(ctx, bank, strings.NewReader(strings.Replace(statement, ",USD,", ",EUR,", 1))); err == nil {
		t.Fatal("non-USD bank statement was accepted")
	}
	if n, err := repo.ImportBank(ctx, bank, strings.NewReader(statement)); err != nil || n != 3 {
		t.Fatalf("bank statement: %d %v", n, err)
	}
	if _, err := repo.ImportBank(ctx, bank, strings.NewReader(statement)); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ImportBank(ctx, bank, strings.NewReader(strings.Replace(statement, "USD,10000,BANK-TRACE", "USD,10001,BANK-TRACE", 1))); err == nil {
		t.Fatal("duplicate bank identity changed amount")
	}
	for _, test := range []struct{ id, outcome string }{
		{"wrong-ref", settlement.MatchReference}, {"wrong-amount", settlement.MatchAmount},
		{"right", settlement.MatchConfirmed}, {"right", settlement.MatchConfirmed},
	} {
		got, err := repo.MatchBank(ctx, settlement.MatchRequest{Provider: payment.ProviderPayPal, Account: account,
			Payout: "W1/T0400/USD", BankAccount: bank.Account, BankTransaction: test.id, Audit: audit})
		if err != nil || got.Outcome != test.outcome {
			t.Fatalf("%s: got %+v, expected %s: %v", test.id, got, test.outcome, err)
		}
	}
	payout, err := repo.PayoutStatus(ctx, payment.ProviderPayPal, account, "W1/T0400/USD")
	if err != nil || payout.Status != "matched" || payout.DifferenceMinor == nil || *payout.DifferenceMinor != "0" ||
		payout.TransferMinor == nil || *payout.TransferMinor != -10000 {
		t.Fatalf("payout evidence was not reconciled: %+v %v", payout, err)
	}
	changed := strings.Replace(report, "DR,38,USD", "DR,40,USD", 1)
	correction, err := importFile(changed)
	if err != nil || correction.Replay || correction.ImportID == first.ImportID || correction.Review != 2 {
		t.Fatalf("changed report lacks its own audited review: %+v %v", correction, err)
	}
}
