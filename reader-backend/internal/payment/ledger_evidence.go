package payment

import (
	"encoding/json"
	"time"
)

type MovementKind string

const (
	MovementPayment       MovementKind = "payment"
	MovementRefund        MovementKind = "refund"
	MovementReversal      MovementKind = "reversal"
	MovementDisputeDebit  MovementKind = "dispute_debit"
	MovementDisputeCredit MovementKind = "dispute_credit"
	MovementFee           MovementKind = "fee"
	MovementTax           MovementKind = "tax"
	MovementFX            MovementKind = "fx"
	MovementPayout        MovementKind = "payout"
	MovementHold          MovementKind = "hold"
	MovementRelease       MovementKind = "release"
	MovementOther         MovementKind = "other"
)

type LedgerSource string

const (
	LedgerPayPalSearch     LedgerSource = "paypal_transaction_search"
	LedgerPayPalSettlement LedgerSource = "paypal_settlement"
	LedgerStripeBalance    LedgerSource = "stripe_balance_transaction"
	LedgerStripeReport     LedgerSource = "stripe_report"
	LedgerStripeWebhook    LedgerSource = "stripe_webhook"
	LedgerStripeRefund     LedgerSource = "stripe_refund_lookup"
)

type LedgerComponent string

const (
	LedgerGross          LedgerComponent = "gross"
	LedgerFee            LedgerComponent = "fee"
	LedgerNet            LedgerComponent = "net"
	LedgerSalesTax       LedgerComponent = "sales_tax"
	LedgerWithheldTax    LedgerComponent = "withheld_tax"
	LedgerProcessingFee  LedgerComponent = "processing_fee"
	LedgerCustomerFacing LedgerComponent = "customer_facing"
	LedgerFXFee          LedgerComponent = "fx_fee"
)

type LedgerMoney struct {
	Currency string
	Minor    int64
}

type LedgerReference struct {
	Type string
	ID   string
}

// LedgerEvidence identifies a balance movement and one immutable observation
// of it. Webhooks and refund lookups supply references without balance amounts.
type LedgerEvidence struct {
	Provider     string
	Account      WebhookAccount
	MovementKey  string
	Source       LedgerSource
	SourceRef    string
	Payload      json.RawMessage
	Kind         MovementKind
	ProviderCode string
	Currency     string
	ExchangeRate string
	OccurredAt   time.Time
	Amounts      map[LedgerComponent]LedgerMoney
	References   []LedgerReference
}
