// Package application owns Reader payment use cases and their execution.
package application

import (
	"context"
	"sync"
	"time"

	"reader-backend/internal/model"
	"reader-backend/internal/payment"
)

type Service interface {
	RunReconciliation(context.Context)
	CreatePayPalOrder(context.Context, int64, int, int64, string, string, checkoutAttribution) (string, string, error)
	PreparePurchase(context.Context, int64, int64, int, int64, string, string, checkoutAttribution) (*PurchaseIntent, error)
	StartPayPalCheckout(context.Context, int64, string, string) (*PurchaseIntent, error)
	StartStripeCheckout(context.Context, int64, string, string) (*PurchaseIntent, error)
	GetStripeOfferSlots(context.Context, int64) ([]int, error)
	GetStripeVIPOfferSlots(context.Context, int64) ([]int, error)
	StartStripeVIP(context.Context, int64, int, string, string, checkoutAttribution) (*PurchaseIntent, error)
	GetStripeVIPPurchase(context.Context, int64, string) (*PurchaseIntent, error)
	OpenStripeVIPPortal(context.Context, int64, string, string) (string, error)
	GetPurchase(context.Context, int64, string, string) (*PurchaseIntent, error)
	GetVIPPurchase(context.Context, int64, string, string) (*PurchaseIntent, error)
	ListRecentPurchases(context.Context, int64) ([]PurchaseIntent, error)
	GetPaymentMethods(context.Context) (PaymentMethods, error)
	CapturePayPalPayment(context.Context, int64, string) (*CheckoutResult, error)
	CreateSubscription(ctx context.Context, userID int64, providerName string, slotID int, returnURL, cancelURL, returnPath, fbp, fbc, pixelID, ip, ua, sourceURL, country string) (*payment.SubscriptionResult, error)
	ActivateSubscription(ctx context.Context, userID int64, providerName string, subscriptionID string, fbp, fbc, pixelID, ip, ua, sourceURL, country string) error
	HasSubscriptionAccess(context.Context, int64) (bool, error)
	GetManagedSubscription(ctx context.Context, userID int64) (*model.UserSubscription, error)
	ListManagedSubscriptions(context.Context, int64) ([]model.UserSubscription, error)
	CancelSubscription(ctx context.Context, userID int64, subscriptionID string, reason string) error
	ProcessSubscriptionWebhook(ctx context.Context, providerName string, payload []byte, headers map[string]string) error
	ProcessChannelWebhook(ctx context.Context, token string, payload []byte, headers map[string]string) error
	GetRechargeTemplates(ctx context.Context, userID int64, novelID int64) (*model.RechargeTemplate, error)
}

// SubscriptionAccess reads the reader's effective VIP entitlement.
type SubscriptionAccess interface {
	HasActiveSubscription(context.Context, int64) (bool, error)
}

type returnProbeLimiter struct {
	mu    sync.Mutex
	until map[string]time.Time
}

type service struct {
	repo             Repository
	readingAccess    SubscriptionAccess
	paypalClient     paypalCheckout
	adapters         payment.Adapters
	historicalStripe bool
	paypalEventsWake chan struct{}
	stripeEventsWake chan struct{}
	returnProbe      *returnProbeLimiter
}

// paypalCheckout is the set of capabilities used by the existing PayPal purchase flow.
type paypalCheckout interface {
	subscriptionCheckout
	CreateOrder(context.Context, payment.WebhookAccount, int64, string, string, string, string, string) (string, string, error)
	CaptureOrder(context.Context, string, payment.WebhookAccount) (*payment.OrderCapture, error)
	GetCapture(context.Context, string) (*payment.OrderCapture, error)
	ListSubscriptionTransactions(context.Context, string, time.Time, time.Time) ([]payment.SubscriptionTransaction, error)
}

type subscriptionCheckout interface {
	payment.SubscriptionProvider
	CheckoutAccount(context.Context) (payment.WebhookAccount, error)
	WebhookConfigured(context.Context) bool
}

func NewService(repo Repository, readingAccess SubscriptionAccess, adapters payment.Adapters) Service {
	paypalClient, _ := payment.Capability[paypalCheckout](adapters, payment.ProviderPayPal)
	return &service{
		repo:             repo,
		readingAccess:    readingAccess,
		paypalClient:     paypalClient,
		adapters:         adapters,
		paypalEventsWake: make(chan struct{}, 1),
		stripeEventsWake: make(chan struct{}, 1),
		returnProbe:      &returnProbeLimiter{},
	}
}

func notifyWorker(ch chan struct{}) {
	if ch == nil {
		return
	}
	select {
	case ch <- struct{}{}:
	default:
	}
}

func (s *service) claimReturnProbe(orderID string, cooldown time.Duration) bool {
	if orderID == "" || cooldown <= 0 || s.returnProbe == nil {
		return true
	}
	now := time.Now()
	s.returnProbe.mu.Lock()
	defer s.returnProbe.mu.Unlock()
	if s.returnProbe.until == nil {
		s.returnProbe.until = make(map[string]time.Time)
	} else if len(s.returnProbe.until) > 1000 {
		for id, until := range s.returnProbe.until {
			if !now.Before(until) {
				delete(s.returnProbe.until, id)
			}
		}
	}
	if until, ok := s.returnProbe.until[orderID]; ok && now.Before(until) {
		return false
	}
	s.returnProbe.until[orderID] = now.Add(cooldown)
	return true
}

func (s *service) primaryPayPalClient() paypalCheckout {
	if client, err := payment.Capability[paypalCheckout](s.adapters, payment.ProviderPayPal); err == nil && client != nil {
		return client
	}
	return s.paypalClient
}
