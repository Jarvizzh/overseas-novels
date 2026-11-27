package application

import (
	"context"
	"log"
	"time"

	"reader-backend/internal/payment"
)

const (
	stripeSettlementRetryWindow  = 24 * time.Hour
	stripeSettlementPollInterval = time.Minute

	stripeNoteSettlementCurrencyMismatch = "Stripe balance transaction settlement currency is not USD"
	stripeNoteSettlementAmountMismatch   = "Stripe balance transaction settlement amount does not match charge"
	stripeNoteSettlementWindowExhausted  = "Stripe balance transaction settlement timed out; manual review required"
)

type stripeSettlementOutcome string

const (
	settlementOutcomeNotRequired      stripeSettlementOutcome = "not_required"
	settlementOutcomeVerified         stripeSettlementOutcome = "verified"
	settlementOutcomePending          stripeSettlementOutcome = "pending"
	settlementOutcomeCurrencyMismatch stripeSettlementOutcome = "currency_mismatch"
	settlementOutcomeAmountMismatch   stripeSettlementOutcome = "amount_mismatch"
)

type stripeSettlementVerification struct {
	Outcome  stripeSettlementOutcome
	Movement *payment.LedgerEvidence
	Note     string
}

type ledgerMovementFetcher interface {
	GetLedgerMovement(context.Context, string) (payment.LedgerEvidence, error)
}

func verifyStripeChargeSettlement(ctx context.Context, fetcher ledgerMovementFetcher, receipt payment.SingleChargeProof, logTask, logID string, extraRefs ...payment.LedgerReference) stripeSettlementVerification {
	if receipt.MovementID == "" {
		if receipt.IsLocalCurrency() {
			return stripeSettlementVerification{Outcome: settlementOutcomePending}
		}
		return stripeSettlementVerification{Outcome: settlementOutcomeNotRequired}
	}
	entry, err := fetcher.GetLedgerMovement(ctx, receipt.MovementID)
	if err != nil {
		log.Printf("task=%s action=ledger id=%s movement=%s error=%v", logTask, logID, receipt.MovementID, err)
		if receipt.IsLocalCurrency() {
			return stripeSettlementVerification{Outcome: settlementOutcomePending}
		}
		return stripeSettlementVerification{Outcome: settlementOutcomeNotRequired}
	}
	if entry.Currency != payment.CurrencyUSD {
		return stripeSettlementVerification{
			Outcome: settlementOutcomeCurrencyMismatch,
			Note:    stripeNoteSettlementCurrencyMismatch,
		}
	}
	if entry.Kind != payment.MovementPayment || entry.Amounts[payment.LedgerGross].Minor != receipt.AmountMinor {
		return stripeSettlementVerification{
			Outcome: settlementOutcomeAmountMismatch,
			Note:    stripeNoteSettlementAmountMismatch,
		}
	}
	entry.References = append(entry.References, payment.LedgerReference{Type: "stripe_charge", ID: receipt.ID})
	entry.References = append(entry.References, extraRefs...)
	return stripeSettlementVerification{
		Outcome:  settlementOutcomeVerified,
		Movement: &entry,
	}
}

type settlementEvidenceColumns struct {
	MovementRef  string
	Currency     string
	ExchangeRate string
	GrossMinor   *int64
	FeeMinor     *int64
	WithheldTax  *int64
	NetMinor     *int64
}

func extractSettlementColumns(movement *payment.LedgerEvidence) settlementEvidenceColumns {
	if movement == nil {
		return settlementEvidenceColumns{}
	}
	cols := settlementEvidenceColumns{
		MovementRef:  movement.MovementKey,
		Currency:     movement.Currency,
		ExchangeRate: movement.ExchangeRate,
	}
	if gross, ok := movement.Amounts[payment.LedgerGross]; ok {
		v := gross.Minor
		cols.GrossMinor = &v
	}
	if fee, ok := movement.Amounts[payment.LedgerFee]; ok {
		v := fee.Minor
		cols.FeeMinor = &v
	}
	if tax, ok := movement.Amounts[payment.LedgerWithheldTax]; ok {
		v := tax.Minor
		cols.WithheldTax = &v
	}
	if net, ok := movement.Amounts[payment.LedgerNet]; ok {
		v := net.Minor
		cols.NetMinor = &v
	}
	return cols
}

func settlementVerificationFromMovement(receipt *payment.SingleChargeProof, movement *payment.LedgerEvidence) stripeSettlementVerification {
	if movement != nil {
		if movement.Currency != payment.CurrencyUSD {
			return stripeSettlementVerification{
				Outcome: settlementOutcomeCurrencyMismatch,
				Note:    stripeNoteSettlementCurrencyMismatch,
			}
		}
		if receipt != nil && (movement.Kind != payment.MovementPayment || movement.Amounts[payment.LedgerGross].Minor != receipt.AmountMinor) {
			return stripeSettlementVerification{
				Outcome: settlementOutcomeAmountMismatch,
				Note:    stripeNoteSettlementAmountMismatch,
			}
		}
		return stripeSettlementVerification{
			Outcome:  settlementOutcomeVerified,
			Movement: movement,
		}
	}
	if receipt != nil && receipt.IsLocalCurrency() {
		return stripeSettlementVerification{Outcome: settlementOutcomePending}
	}
	return stripeSettlementVerification{Outcome: settlementOutcomeNotRequired}
}
