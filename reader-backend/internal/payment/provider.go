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
	CycleYear  SubscriptionCycle = "year"  // Annual recurring billing
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

// DynamicSource resolves live and historical channel clients from database state.
type DynamicSource interface {
	PrimaryAdapter(provider string) (Adapter, bool)
	AdapterForScope(provider string, scope WebhookAccount) (Adapter, bool)
	AllAdapters(provider string) []Adapter
	HasAccounts(provider string) bool
}

// WebhookTokenResolver resolves an opaque per-account webhook token to its channel client.
type WebhookTokenResolver interface {
	ResolveWebhookToken(ctx context.Context, token string) (accountID int64, provider string, scope WebhookAccount, adapter Adapter, err error)
}

// Adapters is the set of channels enabled by the application at startup and dynamically via DB.
type Adapters struct {
	channels  map[string]Adapter
	legacy    map[string]Adapter
	accounts  map[string]map[WebhookAccount]Adapter
	preferred map[string]bool
	dynamic   DynamicSource
}

func NewAdapters(adapters ...Adapter) Adapters {
	channels := make(map[string]Adapter, len(adapters))
	for _, adapter := range adapters {
		channels[adapter.Provider()] = adapter
	}
	return Adapters{
		channels:  maps.Clone(channels),
		legacy:    channels,
		accounts:  make(map[string]map[WebhookAccount]Adapter),
		preferred: make(map[string]bool),
	}
}

// WithDynamicSource attaches a DB-backed dynamic adapter directory to a shallow copy of Adapters.
func (a Adapters) WithDynamicSource(source DynamicSource) Adapters {
	a.dynamic = source
	return a
}

// DynamicSource returns the attached DB-backed dynamic adapter directory if configured.
func (a Adapters) DynamicSource() DynamicSource {
	return a.dynamic
}

// TokenResolver returns the dynamic webhook token resolver if configured.
func (a Adapters) TokenResolver() (WebhookTokenResolver, bool) {
	resolver, ok := a.dynamic.(WebhookTokenResolver)
	return resolver, ok
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
	if a.dynamic != nil {
		if raw, ok := a.dynamic.AdapterForScope(provider, scope); ok {
			if typed, match := raw.(T); match {
				return typed, true
			}
		}
	}
	adapter, ok := a.accounts[provider][scope].(T)
	return adapter, ok
}

func (a Adapters) HasRegisteredAccounts(provider string) bool {
	if len(a.accounts[provider]) != 0 {
		return true
	}
	return a.dynamic != nil && a.dynamic.HasAccounts(provider)
}

func AccountCapabilities[T any](a Adapters, provider string) []T {
	var result []T
	seen := make(map[Adapter]bool)
	appendUnique := func(candidate Adapter) {
		if candidate == nil || seen[candidate] {
			return
		}
		if typed, ok := candidate.(T); ok {
			seen[candidate] = true
			result = append(result, typed)
		}
	}
	if adapter, ok := a.legacy[provider]; ok {
		appendUnique(adapter)
	}
	if a.dynamic != nil {
		for _, candidate := range a.dynamic.AllAdapters(provider) {
			appendUnique(candidate)
		}
	}
	for _, candidate := range a.accounts[provider] {
		appendUnique(candidate)
	}
	return result
}

// Prefer scopes a worker to one client without changing the runtime default.
func (a Adapters) Prefer(adapter Adapter) Adapters {
	a.channels = maps.Clone(a.channels)
	a.preferred = maps.Clone(a.preferred)
	if a.preferred == nil {
		a.preferred = make(map[string]bool)
	}
	a.channels[adapter.Provider()] = adapter
	a.preferred[adapter.Provider()] = true
	return a
}

// Capability resolves an explicitly registered channel and operation.
func Capability[T any](adapters Adapters, channel string) (T, error) {
	if adapters.dynamic != nil && !adapters.preferred[channel] {
		if raw, ok := adapters.dynamic.PrimaryAdapter(channel); ok {
			if typed, match := raw.(T); match {
				return typed, nil
			}
		}
	}
	adapter, ok := adapters.channels[channel].(T)
	if !ok {
		return adapter, fmt.Errorf("payment channel %q does not support the requested operation", channel)
	}
	return adapter, nil
}
