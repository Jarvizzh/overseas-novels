package paypal

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/shopspring/decimal"
	"reader-backend/internal/payment"
)

var ErrSettlementMovementIdentity = errors.New("PayPal row is missing a shared transaction identity")

// LedgerEvidence converts PayPal's fixed two-place file encoding into the
// same units and transaction/T-code identity as Transaction Search.
func (row SettlementEntry) LedgerEvidence(account payment.WebhookAccount) (payment.LedgerEvidence, error) {
	if !validObjectID(row.TransactionID) || len(row.EventCode) != 5 {
		return payment.LedgerEvidence{}, ErrSettlementMovementIdentity
	}
	raw, err := json.Marshal(row.Fields)
	if err != nil {
		return payment.LedgerEvidence{}, err
	}
	input := Transaction{ID: row.TransactionID, ReferenceID: row.ReferenceID, ReferenceType: row.ReferenceType,
		EventCode: row.EventCode, Raw: raw, Gross: settlementAmount(row.Gross)}
	if row.InitiatedAt != nil {
		input.InitiatedAt = *row.InitiatedAt
	}
	// The report supplies accounting entries even when initiation time is
	// absent. Completion time is only used in payout/bank timing.
	if row.Fee != nil {
		input.Fee = settlementAmount(*row.Fee)
	} else {
		input.Fee = &Amount{Currency: input.Gross.Currency, Value: "0"}
	}
	entry, err := ledgerEvidence(account, input)
	if err != nil {
		return payment.LedgerEvidence{}, err
	}
	entry.Source = payment.LedgerPayPalSettlement
	entry.SourceRef = row.FileName + "/" + entry.SourceRef
	return entry, nil
}

func settlementAmount(m ReportMoney) *Amount {
	// The parser has already validated the unsigned integer and direction.
	const settlementReportScale = 2
	value, _ := decimal.NewFromString(m.Units)
	value = value.Shift(-settlementReportScale)
	if m.Direction == Debit {
		value = value.Neg()
	}
	return &Amount{Currency: strings.ToUpper(m.Currency), Value: value.String()}
}
