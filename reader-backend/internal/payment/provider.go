package payment

import (
	"context"
	"fmt"
	"maps"
	"time"
)

type SubscriptionCycle string

const (
	CycleDay   SubscriptionCycle = "day"   // Daily recurring billing
	CycleWeek  SubscriptionCycle = "week"  // Weekly recurring billing
	CycleMonth SubscriptionCycle = "month" // Monthly recurring billing
)

const (
	ProviderPayPal = "paypal"
	ProviderStripe = "stripe"
)

type CreateSubscriptionPlanParam struct {
	Account     WebhookAccount
	RequestID   string
	PlanName    string
	Description string
	Cycle       SubscriptionCycle
	PriceCents  int64
	Currency    string
}

type CreateSubscriptionParam struct {
	Account    WebhookAccount
	RequestID  string
	UserID     int64
	PlanID     string
	SlotID     int
	PriceCents int64
	Currency   string
	CustomID   string
	ReturnURL  string
	CancelURL  string
}

type SubscriptionResult struct {
	SubscriptionID string `json:"subscription_id"`
	ApproveURL     string `json:"approve_url"`
	Status         string `json:"status"`
	IntentID       string `json:"intent_id,omitempty"`
}

type SubscriptionDetails struct {
	SubscriptionID      string     `json:"subscription_id"`
	PlanID              string     `json:"plan_id"`
	CustomID            string     `json:"custom_id"`
	Status              string     `json:"status"` // ACTIVE, CANCELLED, SUSPENDED, EXPIRED, PENDING
	CurrentPeriodStart  *time.Time `json:"current_period_start"`
	CurrentPeriodEnd    *time.Time `json:"current_period_end"`
	NextBillingTime     *time.Time `json:"next_billing_time"`
	LastPaymentCents    int64
	LastPaymentCurrency string
	LastPaymentTime     *time.Time `json:"last_payment_time"`
	PayerEmail          string     `json:"payer_email"`
	PayerID             string     `json:"payer_id"`
	PayerName           string     `json:"payer_name"`
	RawPayload          string     `json:"raw_payload"`
}

type WebhookEventResult struct {
	LookupOnly        bool           `json:"lookup_only,omitempty"`
	Account           WebhookAccount `json:"-"`
	EventID           string
	ProviderEventType string `json:",omitempty"`
	ProviderReason    string `json:",omitempty"`
	CaptureID         string
	PaidAt            time.Time
	EventType         string // "subscription_activated", "payment_succeeded", "subscription_cancelled", "subscription_suspended"
	SubscriptionID    string
	ExternalRefID     string // Transaction ID / Sale ID
	AmountCents       int64
	Currency          string
	Status            string
	PayerEmail        string
	PayerID           string
	PayerName         string
	RawPayload        string
}

type WebhookAccount struct {
	AccountKey  string
	Environment string
}

type Adapter interface {
	Provider() string
}

type SubscriptionProvider interface {
	Adapter
	CreatePlan(ctx context.Context, param CreateSubscriptionPlanParam) (planID string, err error)
	CreateSubscription(ctx context.Context, param CreateSubscriptionParam) (*SubscriptionResult, error)
	GetSubscription(ctx context.Context, subscriptionID string) (*SubscriptionDetails, error)
	ActivateSubscription(ctx context.Context, subscriptionID string) error
	CancelSubscription(ctx context.Context, subscriptionID string, reason string) error
}

type WebhookProvider interface {
	ParseWebhook(context.Context, []byte, map[string]string) (*WebhookEventResult, error)
}

type RefundProvider interface {
	CheckoutAccount(context.Context) (WebhookAccount, error)
	ResolveRefund(context.Context, RefundRequest) (*RefundResult, error)
}

// Adapters is the set of channels enabled by the application at startup.
type Adapters struct {
	channels map[string]Adapter
	legacy   map[string]Adapter
	accounts map[string]map[WebhookAccount]Adapter
}

func NewAdapters(adapters ...Adapter) Adapters {
	channels := make(map[string]Adapter, len(adapters))
	for _, adapter := range adapters {
		channels[adapter.Provider()] = adapter
	}
	return Adapters{channels: maps.Clone(channels), legacy: channels, accounts: make(map[string]map[WebhookAccount]Adapter)}
}

// RegisterAccount binds a configured client to the scope it was verified for.
// A historical account is eligible for servicing its records only.
func (a Adapters) RegisterAccount(scope WebhookAccount, adapter Adapter, primary bool) error {
	provider := adapter.Provider()
	if a.accounts[provider] == nil {
		a.accounts[provider] = make(map[WebhookAccount]Adapter)
	}
	if _, exists := a.accounts[provider][scope]; exists || scope.AccountKey == "" || scope.Environment == "" {
		return fmt.Errorf("invalid or duplicate payment account for %s", provider)
	}
	a.accounts[provider][scope] = adapter
	if primary {
		a.channels[provider] = adapter
	}
	return nil
}

func LegacyCapability[T any](a Adapters, provider string) (T, bool) {
	adapter, ok := a.legacy[provider].(T)
	return adapter, ok
}

func AccountCapability[T any](a Adapters, provider string, scope WebhookAccount) (T, bool) {
	adapter, ok := a.accounts[provider][scope].(T)
	return adapter, ok
}

func (a Adapters) HasRegisteredAccounts(provider string) bool { return len(a.accounts[provider]) != 0 }

func AccountCapabilities[T any](a Adapters, provider string) []T {
	var result []T
	if adapter, ok := LegacyCapability[T](a, provider); ok {
		result = append(result, adapter)
	}
	for _, candidate := range a.accounts[provider] {
		if adapter, ok := candidate.(T); ok {
			result = append(result, adapter)
		}
	}
	return result
}

// Prefer scopes a worker to one client without changing the runtime default.
func (a Adapters) Prefer(adapter Adapter) Adapters {
	a.channels = maps.Clone(a.channels)
	a.channels[adapter.Provider()] = adapter
	return a
}

// Capability resolves an explicitly registered channel and operation.
func Capability[T any](adapters Adapters, channel string) (T, error) {
	adapter, ok := adapters.channels[channel].(T)
	if !ok {
		return adapter, fmt.Errorf("payment channel %q does not support the requested operation", channel)
	}
	return adapter, nil
}
