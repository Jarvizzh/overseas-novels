package paypal

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"reader-backend/internal/payment"
)

type LedgerPage struct {
	Entries []payment.LedgerEvidence
	HasMore bool
}

// ListLedgerMovements accepts only the scoped, balance-affecting search feed.
func (p *Client) ListLedgerMovements(ctx context.Context, from, through time.Time, page int) (LedgerPage, error) {
	cfg, err := p.credentials(ctx)
	if err != nil {
		return LedgerPage{}, err
	}
	if cfg.MerchantID == "" {
		return LedgerPage{}, errors.New("PayPal ledger requires an expected reporting merchant ID")
	}
	report, err := p.ListTransactions(ctx, TransactionQuery{From: from, Through: through, Page: page, PageSize: maxReportingPageSize})
	if err != nil {
		return LedgerPage{}, err
	}
	if report.Page != page || report.Account != cfg.account() || report.MerchantID != cfg.MerchantID ||
		(report.TotalPages < page && (report.TotalItems != 0 || len(report.Transactions) != 0)) {
		return LedgerPage{}, errors.New("unexpected PayPal ledger pagination or account")
	}
	result := LedgerPage{HasMore: report.Page < report.TotalPages}
	for _, row := range report.Transactions {
		// V retains the original successful movement. Refund/reversal rows
		// are separate T-codes and provide their own balance effect.
		if row.Status != "S" && row.Status != "V" {
			continue
		}
		if row.InitiatedAt.IsZero() {
			return LedgerPage{}, errors.New("PayPal reporting movement is missing its initiation time")
		}
		entry, err := ledgerEvidence(report.Account, row)
		if err != nil {
			return LedgerPage{}, fmt.Errorf("normalize PayPal transaction %s (%s): %w", row.ID, row.EventCode, err)
		}
		result.Entries = append(result.Entries, entry)
	}
	return result, nil
}

func ledgerEvidence(account payment.WebhookAccount, row Transaction) (payment.LedgerEvidence, error) {
	if !validObjectID(row.ID) || len(row.EventCode) != 5 || row.Gross == nil {
		return payment.LedgerEvidence{}, errors.New("incomplete PayPal balance movement")
	}
	gross, err := paypalLedgerMoney(row.Gross)
	if err != nil {
		return payment.LedgerEvidence{}, err
	}
	// Transaction Search can reuse a transaction ID for different events.
	// Amounts and mutable state stay outside the identity so disagreements
	// for the same T-code and balance currency remain reviewable.
	key := strings.Join([]string{row.ID, row.EventCode, gross.Currency}, "/")
	entry := payment.LedgerEvidence{
		Provider: payment.ProviderPayPal, Account: account, MovementKey: key,
		Source: payment.LedgerPayPalSearch, SourceRef: key, Payload: row.Raw,
		Kind: paypalMovementKind(row.EventCode, gross.Minor), Currency: gross.Currency,
		ProviderCode: row.EventCode, OccurredAt: row.InitiatedAt,
		Amounts: map[payment.LedgerComponent]payment.LedgerMoney{payment.LedgerGross: gross},
	}
	if row.Fee != nil {
		fee, err := paypalLedgerMoney(row.Fee)
		if err != nil {
			return payment.LedgerEvidence{}, err
		}
		entry.Amounts[payment.LedgerFee] = fee
		if fee.Currency == gross.Currency {
			net, err := payment.ExactMinorUnits(decimal.NewFromInt(gross.Minor).Add(decimal.NewFromInt(fee.Minor)), 0)
			if err != nil {
				return payment.LedgerEvidence{}, err
			}
			entry.Amounts[payment.LedgerNet] = payment.LedgerMoney{Currency: gross.Currency, Minor: net}
		}
	}
	if row.Tax != nil {
		tax, err := paypalLedgerMoney(row.Tax)
		if err != nil {
			return payment.LedgerEvidence{}, err
		}
		entry.Amounts[payment.LedgerSalesTax] = tax
	}
	if entry.Kind == payment.MovementPayment {
		entry.References = append(entry.References, payment.LedgerReference{Type: "paypal_transaction", ID: row.ID})
	} else if entry.Kind == payment.MovementRefund {
		entry.References = append(entry.References, payment.LedgerReference{Type: "paypal_refund", ID: row.ID})
	}
	if row.EventCode == "T1106" {
		entry.References = append(entry.References, payment.LedgerReference{Type: "paypal_reversal", ID: row.ID})
	}
	if entry.Kind == payment.MovementPayout {
		entry.References = append(entry.References, payment.LedgerReference{Type: "paypal_payout_transfer", ID: row.ID})
	}
	if row.ReferenceID != "" {
		var kind string
		switch row.ReferenceType {
		case "TXN":
			if row.EventCode == "T1101" {
				kind = "paypal_payout"
				break
			}
			switch entry.Kind {
			case payment.MovementRefund, payment.MovementReversal, payment.MovementDisputeDebit, payment.MovementDisputeCredit:
				kind = "paypal_transaction"
			default:
				kind = "paypal_related_transaction"
			}
		case "ODR":
			kind = "paypal_order"
		case "SUB":
			kind = "paypal_subscription"
		case "PAP":
			kind = "paypal_preapproval"
		}
		if kind != "" {
			entry.References = append(entry.References, payment.LedgerReference{Type: kind, ID: row.ReferenceID})
		}
	}
	return entry, nil
}

func paypalLedgerMoney(amount *Amount) (payment.LedgerMoney, error) {
	code := strings.ToUpper(amount.Currency)
	scale, err := payment.CurrencyScale(code)
	if err != nil {
		return payment.LedgerMoney{}, err
	}
	if zeroDecimalCurrency(code) {
		scale = 0
	}
	value, err := decimal.NewFromString(amount.Value)
	if err != nil {
		return payment.LedgerMoney{}, errors.New("invalid PayPal reporting amount")
	}
	minor, err := payment.ExactMinorUnits(value, scale)
	return payment.LedgerMoney{Currency: code, Minor: minor}, err
}

func paypalMovementKind(code string, amount int64) payment.MovementKind {
	switch code {
	case "T0000", "T0002", "T0003", "T0005", "T0006", "T0007", "T0019":
		if amount > 0 {
			return payment.MovementPayment
		}
	case "T1107", "T1120":
		return payment.MovementRefund
	case "T1106":
		return payment.MovementReversal
	case "T1101":
		return payment.MovementReversal
	case "T0400", "T0401", "T0403":
		if amount < 0 {
			return payment.MovementPayout
		}
	case "T1201", "T1202", "T1205", "T1207", "T1208":
		if amount < 0 {
			return payment.MovementDisputeDebit
		}
		return payment.MovementDisputeCredit
	case "T1110":
		return payment.MovementHold
	case "T1111":
		return payment.MovementRelease
	case "T0200", "T0201", "T0202":
		return payment.MovementFX
	case "T0020", "T2301":
		return payment.MovementTax
	case "T1108", "T1109":
		return payment.MovementFee
	}
	if strings.HasPrefix(code, "T01") {
		return payment.MovementFee
	}
	return payment.MovementOther
}
