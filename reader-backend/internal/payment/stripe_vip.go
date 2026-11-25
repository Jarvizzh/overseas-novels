package payment

import (
	"context"
	"time"
)

const (
	StripeSaleDirect = "direct"
	StripeSaleMoR    = "merchant_of_record"
)

type StripeVIPOffer struct {
	SingleCheckoutOffer
	Cycle string
}

type StripeVIPCheckoutRequest struct {
	IntentID, ReturnURL, CancelURL string
	Offer                          StripeVIPOffer
}

type StripeVIPCheckout struct {
	ID, IntentID, SubscriptionID, URL string
	Expired, AwaitingBuyer            bool
}

type StripeVIPSubscription struct {
	ID, IntentID, CustomerID, PriceID, Status, LatestInvoice string
	CancelAtPeriodEnd, ManagedPayments                       bool
	ObservedAt                                               time.Time
}

type StripeVIPAllocation struct {
	ID          string
	AmountMinor int64
	Receipt     SingleChargeProof
}

type StripeVIPInvoice struct {
	ID, Country, TaxLiabilityAccount, Currency, BillingReason string
	CustomerTaxStatus                                         string
	TaxVerified                                               bool
	Subscription                                              StripeVIPSubscription
	Status                                                    string
	DueMinor, RemainingMinor, PrincipalMinor                  int64
	StartsAt, EndsAt                                          time.Time
	ReviewReason                                              string
	RevisesInvoiceID                                          string
	RevisionVerified                                          bool
	Allocations                                               []StripeVIPAllocation
}

type StripeVIPAdapter interface {
	AccountScope() WebhookAccount
	CreateStripeVIPCheckout(context.Context, StripeVIPCheckoutRequest) (StripeVIPCheckout, error)
	FindStripeVIPCheckout(context.Context, string, time.Time, time.Time) (string, error)
	ReadStripeVIPCheckout(context.Context, string, string, string) (StripeVIPCheckout, error)
	ReadStripeVIPSubscription(context.Context, string) (StripeVIPSubscription, error)
	StripeVIPInvoiceRefs(context.Context, string, string) ([]string, string, error)
	StripeInvoiceSubscription(context.Context, string) (string, error)
	ReadStripeVIPInvoice(context.Context, string, StripeVIPOffer) (StripeVIPInvoice, error)
	ScheduleStripeVIPCancellation(context.Context, string, string) (StripeVIPSubscription, error)
	OpenStripeVIPPortal(context.Context, string, string, string) (string, error)
	GetLedgerMovement(context.Context, string) (LedgerEvidence, error)
}
