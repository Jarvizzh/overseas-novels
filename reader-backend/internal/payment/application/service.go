// Package application owns Reader payment use cases and their execution.
package application

import (
	"context"
	"time"

	"reader-backend/internal/model"
	"reader-backend/internal/payment"
)

type Service interface {
	RunReconciliation(context.Context)
	CreatePayPalOrder(context.Context, int64, int, int64, string, string, checkoutAttribution) (string, string, error)
	PreparePurchase(context.Context, int64, int, int64, string, string, checkoutAttribution) (*PurchaseIntent, error)
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
	GetRechargeTemplates(ctx context.Context, userID int64, novelID int64) (*model.RechargeTemplate, error)
}

// SubscriptionAccess reads the reader's effective VIP entitlement.
type SubscriptionAccess interface {
	HasActiveSubscription(context.Context, int64) (bool, error)
}

type service struct {
	repo             Repository
	readingAccess    SubscriptionAccess
	paypalClient     paypalCheckout
	adapters         payment.Adapters
	historicalStripe bool
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
		repo:          repo,
		readingAccess: readingAccess,
		paypalClient:  paypalClient,
		adapters:      adapters,
	}
}
