package payment

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

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
}

type SingleCheckoutProof struct {
	ID, AttemptID, URL string
	Expired            bool
	Failed             bool
	AwaitingBuyer      bool
	Receipt            *SingleChargeProof
}

type SingleChargeProof struct {
	ID, IntentID, Currency, Country                             string
	AmountMinor, RefundedMinor, PrincipalMinor                  int64
	PaidAt                                                      time.Time
	EligibleTerms, TaxVerified                                  bool
	TaxLiabilityAccount, CustomerTaxStatus, PresentmentCurrency string
	Evidence                                                    json.RawMessage
	MovementID                                                  string
	RiskReview                                                  *CaseFact
	ReviewUnconfirmed                                           bool
	Disputed                                                    bool
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
