package payment

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

const (
	CurrencyUSD = "USD"

	SettlementStateNone    = "none"
	SettlementStatePending = "pending_settlement"
	SettlementStateSettled = "settled"
	SettlementStateReview  = "review"

	BillingAddressCollectionRequired = "required"
	BillingAddressCollectionAuto     = "auto"

	SavedPaymentMethodEnabled  = "enabled"
	SavedPaymentMethodDisabled = "disabled"

	SetupFutureUsageOnSession  = "on_session"
	SetupFutureUsageOffSession = "off_session"
)

// FrozenCheckoutParams captures the exact checkout parameters frozen on
// first write so retries never send different idempotent requests if account
// toggles or customer state change.
type FrozenCheckoutParams struct {
	AdaptivePricingEnabled   bool   `json:"adaptive_pricing_enabled"`
	BillingAddressCollection string `json:"billing_address_collection"`
	CustomerID               string `json:"customer_id,omitempty"`
	SavePaymentMethodEnabled bool   `json:"save_payment_method_enabled"`
	SetupFutureUsage         string `json:"setup_future_usage,omitempty"`
}

// SingleCheckoutOffer describes the approved catalog facts copied to an
// attempt. Prices and collected amounts use USD cents.
type SingleCheckoutOffer struct {
	ProductID, PriceID             string
	SalesMode, TaxLiabilityAccount string
	AmountMinor                    int64
	Countries                      []string
	ExpectedCountry                string
}

type SingleCheckoutRequest struct {
	AttemptID, SuccessURL, CancelURL string
	Offer                            SingleCheckoutOffer
	Params                           *FrozenCheckoutParams
}

type SingleCheckoutProof struct {
	ID, AttemptID, URL, CustomerID string
	Expired                        bool
	Failed                         bool
	AwaitingBuyer                  bool
	Receipt                        *SingleChargeProof
}

type SingleChargeProof struct {
	ID, IntentID, Currency, Country                           string
	CustomerID                                                string
	AmountMinor, RefundedMinor, PrincipalMinor                int64
	PaidAt                                                    time.Time
	EligibleTerms, TaxVerified                                bool
	TaxLiabilityAccount, TaxLiabilityType, AutomaticTaxStatus string
	CustomerTaxStatus, PresentmentCurrency                    string
	PresentmentAmountMinor                                    *int64
	Evidence                                                  json.RawMessage
	MovementID                                                string
	RiskReview                                                *CaseFact
	ReviewUnconfirmed                                         bool
	Disputed                                                  bool
}

// IsLocalCurrency reports whether the buyer paid in a confirmed non-USD
// presentment currency.
func (p SingleChargeProof) IsLocalCurrency() bool {
	return p.PresentmentCurrency != "" && p.PresentmentCurrency != CurrencyUSD
}

type StripeCustomerAdapter interface {
	AccountScope() WebhookAccount
	CreateCustomer(ctx context.Context, userID int64, email, idempotencyKey string) (string, error)
}

type SingleCheckoutNotice struct {
	EventID, EventType, SessionID, AttemptID, RefundID string
	VIPIntentID, InvoiceID, SubscriptionID             string
	SettlementReference                                string
	Account                                            WebhookAccount
	Aftercare                                          json.RawMessage
}

type SingleCheckoutAdapter interface {
	AccountScope() WebhookAccount
	CreateSingleCheckout(context.Context, SingleCheckoutRequest) (SingleCheckoutProof, error)
	FindSingleCheckout(context.Context, string, time.Time, time.Time) (string, error)
	ReadSingleCheckout(context.Context, string, string, SingleCheckoutOffer) (SingleCheckoutProof, error)
	GetLedgerMovement(context.Context, string) (LedgerEvidence, error)
}

type StripeRefundProof struct {
	ID, ChargeID, Status, Currency, CommandKey string
	AmountMinor                                int64
}

var ErrStripeRefundRequiresReview = errors.New("Stripe receipt is currently unavailable for refund")
