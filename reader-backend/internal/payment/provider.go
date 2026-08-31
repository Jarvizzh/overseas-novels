package payment

import (
	"context"
	"fmt"
	"sync"
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
	PlanName    string
	Description string
	Cycle       SubscriptionCycle
	PriceCents  int64
	Currency    string
}

type CreateSubscriptionParam struct {
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
}

type SubscriptionDetails struct {
	SubscriptionID     string     `json:"subscription_id"`
	PlanID             string     `json:"plan_id"`
	CustomID           string     `json:"custom_id"`
	Status             string     `json:"status"` // ACTIVE, CANCELLED, SUSPENDED, EXPIRED, PENDING
	CurrentPeriodStart *time.Time `json:"current_period_start"`
	CurrentPeriodEnd   *time.Time `json:"current_period_end"`
	NextBillingTime    *time.Time `json:"next_billing_time"`
	LastPaymentTime    *time.Time `json:"last_payment_time"`
	PayerEmail         string     `json:"payer_email"`
	PayerID            string     `json:"payer_id"`
	PayerName          string     `json:"payer_name"`
	RawPayload         string     `json:"raw_payload"`
}

type WebhookEventResult struct {
	EventType      string // "subscription_activated", "payment_succeeded", "subscription_cancelled", "subscription_suspended"
	SubscriptionID string
	ExternalRefID  string // Transaction ID / Sale ID
	AmountCents    int64
	Currency       string
	Status         string
	PayerEmail     string
	PayerID        string
	PayerName      string
	RawPayload     string
}

type SubscriptionProvider interface {
	GetProviderName() string
	CreatePlan(ctx context.Context, param CreateSubscriptionPlanParam) (planID string, err error)
	CreateSubscription(ctx context.Context, param CreateSubscriptionParam) (*SubscriptionResult, error)
	GetSubscription(ctx context.Context, subscriptionID string) (*SubscriptionDetails, error)
	ActivateSubscription(ctx context.Context, subscriptionID string) error
	CancelSubscription(ctx context.Context, subscriptionID string, reason string) error
	ParseWebhook(ctx context.Context, payload []byte, headers map[string]string) (*WebhookEventResult, error)
}

var (
	providersMu sync.RWMutex
	providers   = make(map[string]SubscriptionProvider)
)

func RegisterSubscriptionProvider(name string, p SubscriptionProvider) {
	providersMu.Lock()
	defer providersMu.Unlock()
	providers[name] = p
}

func GetSubscriptionProvider(name string) (SubscriptionProvider, error) {
	providersMu.RLock()
	defer providersMu.RUnlock()
	p, ok := providers[name]
	if !ok {
		return nil, fmt.Errorf("subscription provider '%s' not registered", name)
	}
	return p, nil
}
