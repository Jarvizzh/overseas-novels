package paypal

import (
	"strings"
	"testing"
)

func TestSettlementReaderKeepsDirectionsAndChecksCompleteBatch(t *testing.T) {
	const first = `RH,2026/11/21 00:00:00 +0000,A,recipient,005
FH,1
SH,2026/11/20 00:00:00 +0000,2026/11/21 00:00:00 +0000,merchant,
CH,transaction_id,transaction_event_code,paypal_reference_id,paypal_reference_id_type,transaction_debit_or_credit,gross_transaction_amount,gross_transaction_currency,fee_debit_or_credit,fee_amount,fee_currency,bank_reference_id
SB,C1,T0006,,,CR,1000,USD,DR,38,USD,
FF,1
`
	const last = `FH,2
SB,C1,T1107,C1,TXN,DR,500,USD,,,,
SB,W1,T0400,,,DR,1000,JPY,,,,BANK1
SC,3
RC,3
FF,2
`
	files := []SettlementFile{
		{Name: "STL-20261121.A.01.02.005.CSV", Body: strings.NewReader(first)},
		{Name: "STL-20261121.A.02.02.005.CSV", Body: strings.NewReader(last)},
	}
	var rows []SettlementEntry
	summary, err := ReadSettlementReport(t.Context(), "merchant", files, func(row SettlementEntry) error {
		rows = append(rows, row)
		return nil
	})
	if err != nil || summary.Rows != 3 || summary.Files != 2 || len(rows) != 3 {
		t.Fatalf("invalid settlement report: %+v %v", summary, err)
	}
	if rows[0].Gross.Direction != Credit || rows[0].Fee == nil || rows[0].Fee.Units != "38" ||
		rows[1].EventCode == rows[0].EventCode || rows[1].Gross.Direction != Debit || rows[1].Fee != nil ||
		rows[1].FileName == rows[0].FileName || rows[2].Gross.Currency != "JPY" || rows[2].BankReferenceID != "BANK1" {
		t.Fatalf("missing settlement evidence: %+v", rows)
	}
	files[0].Body = strings.NewReader(first)
	files[1].Body = strings.NewReader(strings.Replace(last, "FF,2", "FF,3", 1))
	if _, err := ReadSettlementReport(t.Context(), "merchant", files, func(row SettlementEntry) error { return nil }); err == nil {
		t.Fatal("incomplete PayPal file accepted")
	}
}
