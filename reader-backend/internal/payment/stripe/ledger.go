package stripe

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	stripego "github.com/stripe/stripe-go/v87"
	"reader-backend/internal/payment"
)

func ledgerAccount(scope Scope) payment.WebhookAccount {
	return payment.WebhookAccount{AccountKey: scope.AccountID, Environment: string(scope.Environment)}
}

func (c *Client) GetLedgerMovement(ctx context.Context, id string) (payment.LedgerEvidence, error) {
	txn, err := c.GetBalanceTransaction(ctx, id)
	if err != nil {
		return payment.LedgerEvidence{}, err
	}
	if txn.ID != id {
		return payment.LedgerEvidence{}, errors.New("unexpected Stripe balance transaction identity")
	}
	return c.ledgerMovement(txn)
}

func (c *Client) ledgerMovement(txn *BalanceTransaction) (payment.LedgerEvidence, error) {
	if !validID(txn.ID, "txn_") || txn.Created <= 0 {
		return payment.LedgerEvidence{}, errors.New("invalid Stripe balance transaction identity")
	}
	var signedFields struct {
		Amount *int64 `json:"amount"`
		Fee    *int64 `json:"fee"`
		Net    *int64 `json:"net"`
	}
	if err := json.Unmarshal(txn.raw, &signedFields); err != nil {
		return payment.LedgerEvidence{}, err
	}
	if signedFields.Amount == nil || signedFields.Fee == nil || signedFields.Net == nil {
		return payment.LedgerEvidence{}, errors.New("incomplete Stripe balance amount evidence")
	}
	code := strings.ToUpper(string(txn.Currency))
	if _, err := stripeBalanceScale(code); err != nil {
		return payment.LedgerEvidence{}, err
	}
	fee, err := payment.ExactMinorUnits(decimal.NewFromInt(txn.Fee).Neg(), 0)
	if err != nil {
		return payment.LedgerEvidence{}, err
	}
	const stripeFeeDetailWithheldTax = "withheld_tax"
	var withheldTax int64
	for _, detail := range txn.FeeDetails {
		if detail == nil || detail.Type != stripeFeeDetailWithheldTax {
			continue
		}
		if strings.ToUpper(string(detail.Currency)) == code && detail.Amount != 0 {
			withheldTax += detail.Amount
		}
	}
	amounts := map[payment.LedgerComponent]payment.LedgerMoney{
		payment.LedgerGross: {Currency: code, Minor: txn.Amount},
		payment.LedgerFee:   {Currency: code, Minor: fee},
		payment.LedgerNet:   {Currency: code, Minor: txn.Net},
	}
	if withheldTax != 0 {
		withheldMinor, err := payment.ExactMinorUnits(decimal.NewFromInt(withheldTax).Neg(), 0)
		if err != nil {
			return payment.LedgerEvidence{}, err
		}
		amounts[payment.LedgerWithheldTax] = payment.LedgerMoney{Currency: code, Minor: withheldMinor}
		if txn.Fee >= withheldTax {
			procFee, err := payment.ExactMinorUnits(decimal.NewFromInt(txn.Fee-withheldTax).Neg(), 0)
			if err != nil {
				return payment.LedgerEvidence{}, err
			}
			amounts[payment.LedgerProcessingFee] = payment.LedgerMoney{Currency: code, Minor: procFee}
		}
	}
	entry := payment.LedgerEvidence{
		Provider: payment.ProviderStripe, Account: ledgerAccount(c.scope), MovementKey: txn.ID,
		Source: payment.LedgerStripeBalance, SourceRef: txn.ID, Payload: txn.raw,
		Kind: stripeMovementKind(string(txn.ReportingCategory), txn.Amount), ProviderCode: string(txn.ReportingCategory),
		Currency: code, OccurredAt: time.Unix(txn.Created, 0).UTC(),
		Amounts: amounts,
	}
	if txn.Source != nil {
		entry.References = stripeLedgerReferences(txn.Source.ID, "", "", "", "", "")
	}
	if txn.ExchangeRate != nil {
		entry.ExchangeRate = txn.ExchangeRate.String()
	}
	return entry, validateStripeBalanceAmounts(entry.Amounts)
}

// LedgerEvidence normalizes an itemized report row into the same minor
// units and BalanceTransaction identity as the object obtained via the API.
func (r ReportRow) LedgerEvidence(scope Scope, reportRunID string) (payment.LedgerEvidence, error) {
	if !validID(reportRunID, "frr_") {
		return payment.LedgerEvidence{}, errors.New("Stripe ledger report requires a Report Run ID")
	}
	row, err := r.BalanceTransaction()
	if err != nil {
		return payment.LedgerEvidence{}, err
	}
	if row.Gross == nil {
		return payment.LedgerEvidence{}, errors.New("Stripe ledger report requires gross")
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return payment.LedgerEvidence{}, err
	}
	code := strings.ToUpper(row.Currency)
	amounts := make(map[payment.LedgerComponent]payment.LedgerMoney)
	for _, component := range []struct {
		name     payment.LedgerComponent
		value    *decimal.Decimal
		currency string
		expense  bool
	}{
		{payment.LedgerGross, row.Gross, code, false},
		{payment.LedgerFee, row.Fee, code, true},
		{payment.LedgerNet, row.Net, code, false},
		{payment.LedgerWithheldTax, row.WithheldTax, code, true},
		{payment.LedgerProcessingFee, row.FeeNetOfWithheldTax, code, true},
		{payment.LedgerCustomerFacing, row.CustomerFacingAmount, strings.ToUpper(row.CustomerFacingCurrency), false},
	} {
		if component.value == nil {
			continue
		}
		scale, err := stripeBalanceScale(component.currency)
		if err != nil {
			return payment.LedgerEvidence{}, err
		}
		value := *component.value
		if component.expense {
			value = value.Neg()
		}
		minor, err := payment.ExactMinorUnits(value, scale)
		if err != nil {
			return payment.LedgerEvidence{}, err
		}
		amounts[component.name] = payment.LedgerMoney{Currency: component.currency, Minor: minor}
	}
	if err := validateStripeBalanceAmounts(amounts); err != nil {
		return payment.LedgerEvidence{}, err
	}
	entry := payment.LedgerEvidence{
		Provider: payment.ProviderStripe, Account: ledgerAccount(scope), MovementKey: row.ID,
		Source: payment.LedgerStripeReport, SourceRef: reportRunID + "/" + row.ID, Payload: raw,
		Kind:         stripeMovementKind(row.ReportingCategory, amounts[payment.LedgerGross].Minor),
		ProviderCode: row.ReportingCategory, Currency: code, Amounts: amounts,
		References: stripeLedgerReferences(row.SourceID, row.ChargeID, row.PaymentIntentID, row.InvoiceID, row.SubscriptionID, row.PayoutID),
	}
	if row.ExchangeRate != nil {
		entry.ExchangeRate = strings.TrimSpace(r["exchange_rate"])
	}
	return entry, nil
}

// RefundLedgerEvidence accepts a decoded account-scoped event. It associates a
// refund and its original charge even when the financial source arrives later.
func (event Event) RefundLedgerEvidence() ([]payment.LedgerEvidence, error) {
	if event.ObjectType != "refund" || !strings.HasPrefix(string(event.Type), "refund.") {
		return nil, nil
	}
	if !validID(event.ID, "evt_") || (event.Source != EventFromWebhook && event.Source != EventFromAPI) {
		return nil, errors.New("missing Stripe refund event provenance")
	}
	var refund stripego.Refund
	if err := json.Unmarshal(event.Object, &refund); err != nil {
		return nil, err
	}
	return stripeRefundEvidence(event.Scope, payment.LedgerStripeWebhook, event.ID, &refund, event.Object)
}

func (c *Client) GetRefundLedgerEvidence(ctx context.Context, id string) ([]payment.LedgerEvidence, error) {
	refund, err := c.GetRefund(ctx, id)
	if err != nil {
		return nil, err
	}
	if refund.ID != id {
		return nil, errors.New("unexpected Stripe refund identity")
	}
	raw := refund.LastResponse.RawJSON
	return stripeRefundEvidence(c.scope, payment.LedgerStripeRefund, id, refund, raw)
}

func stripeRefundEvidence(scope Scope, source payment.LedgerSource, ref string, refund *stripego.Refund, raw json.RawMessage) ([]payment.LedgerEvidence, error) {
	if !validID(refund.ID, "re_") {
		return nil, errors.New("invalid Stripe refund identity")
	}
	var charge string
	if refund.Charge != nil {
		charge = refund.Charge.ID
	}
	var result []payment.LedgerEvidence
	for _, txn := range []*stripego.BalanceTransaction{refund.BalanceTransaction, refund.FailureBalanceTransaction} {
		if txn == nil {
			continue
		}
		if !validID(txn.ID, "txn_") {
			return nil, errors.New("invalid Stripe refund balance reference")
		}
		result = append(result, payment.LedgerEvidence{
			Provider: payment.ProviderStripe, Account: ledgerAccount(scope), MovementKey: txn.ID,
			Source: source, SourceRef: ref, Payload: raw,
			References: stripeLedgerReferences(refund.ID, charge, "", "", "", ""),
		})
	}
	return result, nil
}

func stripeLedgerReferences(source, charge, intent, invoice, subscription, payout string) []payment.LedgerReference {
	var refs []payment.LedgerReference
	for _, ref := range []struct{ id, prefix, kind string }{
		{charge, "ch_", "stripe_charge"}, {source, "ch_", "stripe_charge"}, {source, "re_", "stripe_refund"},
		{source, "dp_", "stripe_dispute"}, {source, "du_", "stripe_dispute"}, {intent, "pi_", "stripe_payment_intent"},
		{invoice, "in_", "stripe_invoice"}, {subscription, "sub_", "stripe_subscription"},
		{payout, "po_", "stripe_payout"}, {source, "po_", "stripe_payout"},
	} {
		if validID(ref.id, ref.prefix) {
			refs = append(refs, payment.LedgerReference{Type: ref.kind, ID: ref.id})
		}
	}
	if source != "" {
		refs = append(refs, payment.LedgerReference{Type: "stripe_source", ID: source})
	}
	return refs
}

func stripeBalanceScale(code string) (int32, error) {
	switch code {
	case "ISK", "UGX":
		// Stripe encodes these whole-currency units with two trailing zeros.
		return 2, nil
	default:
		return payment.CurrencyScale(code)
	}
}

func validateStripeBalanceAmounts(amounts map[payment.LedgerComponent]payment.LedgerMoney) error {
	gross := amounts[payment.LedgerGross]
	fee, hasFee := amounts[payment.LedgerFee]
	net, hasNet := amounts[payment.LedgerNet]
	if hasFee && hasNet && !decimal.NewFromInt(gross.Minor).Add(decimal.NewFromInt(fee.Minor)).Equal(decimal.NewFromInt(net.Minor)) {
		return errors.New("Stripe gross, fee and net do not reconcile")
	}
	withheldTax, hasWithheldTax := amounts[payment.LedgerWithheldTax]
	processingFee, hasProcessingFee := amounts[payment.LedgerProcessingFee]
	if hasFee && hasWithheldTax && hasProcessingFee &&
		fee.Currency == withheldTax.Currency && fee.Currency == processingFee.Currency {
		if !decimal.NewFromInt(withheldTax.Minor).Add(decimal.NewFromInt(processingFee.Minor)).Equal(decimal.NewFromInt(fee.Minor)) {
			return errors.New("Stripe fee, withheld_tax and fee_net_of_withheld_tax do not reconcile")
		}
	}
	return nil
}

func stripeMovementKind(category string, amount int64) payment.MovementKind {
	switch category {
	case "charge":
		return payment.MovementPayment
	case "refund":
		return payment.MovementRefund
	case "refund_failure", "partial_capture_reversal":
		return payment.MovementReversal
	case "dispute", "dispute_reversal":
		if amount < 0 {
			return payment.MovementDisputeDebit
		}
		return payment.MovementDisputeCredit
	case "fee", "stripe_fee":
		return payment.MovementFee
	case "payout", "payout_reversal":
		return payment.MovementPayout
	case "tax":
		return payment.MovementTax
	default:
		return payment.MovementOther
	}
}
