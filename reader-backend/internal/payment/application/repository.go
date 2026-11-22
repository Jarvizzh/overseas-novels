package application

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"reader-backend/internal/model"
	"reader-backend/internal/payment"
)

type Repository interface {
	CompletePayPalRefund(context.Context, string, string, int64, string, ...payment.WebhookAccount) error
	enqueuePayPalEvent(context.Context, *payment.WebhookEventResult, ...bool) error
	enqueuePayPalCancellation(context.Context, string, int64, string) error
	savePayPalBillingSnapshot(context.Context, *payment.SubscriptionDetails) error
	getPayPalBillingSnapshot(context.Context, string, time.Time, string, int64) (time.Time, error)
	GetRechargeTemplateByID(ctx context.Context, templateID int) (*model.RechargeTemplate, error)
	GetDefaultRechargeTemplate(ctx context.Context) (*model.RechargeTemplate, error)
	GetFirstRechargeTemplate(ctx context.Context) (*model.RechargeTemplate, error)
	GetRechargeSlots(ctx context.Context, templateID int) ([]model.RechargeSlot, error)
	GetRechargeSlotByID(ctx context.Context, slotID int) (*model.RechargeSlot, error)
	GetProviderPlan(ctx context.Context, provider string, slotID int) (*model.PaymentProviderPlan, error)
	UpsertUserSubscription(ctx context.Context, sub *model.UserSubscription) error
	reserveSubscriptionIntent(context.Context, subscriptionIntent) (*subscriptionIntent, error)
	releaseSubscriptionIntent(context.Context, string)
	submitSubscriptionIntent(context.Context, string, string) error
	finishSubscriptionIntent(context.Context, *subscriptionIntent, *payment.SubscriptionResult) error
	getVIPPurchase(context.Context, int64, string, string) (*PurchaseIntent, error)
	recentPayPalVIPPurchases(context.Context, int64) ([]PurchaseIntent, error)
	claimVIPActivation(context.Context, string, ...payment.WebhookAccount) (bool, error)
	GetManagedSubscriptionByUserID(ctx context.Context, userID int64) (*model.UserSubscription, error)
	ListManagedSubscriptions(context.Context, int64) ([]model.UserSubscription, error)
	GetSubscriptionByID(ctx context.Context, subID string) (*model.UserSubscription, error)
	UpdateUserSubscriptionStatus(ctx context.Context, subID string, status string) error
	RecordSubscriptionRenewalOrderTx(ctx context.Context, sub *model.UserSubscription, externalRefID string, amountCents int64, currency string, paidAt, periodEnd time.Time, providerStatus string, tpOrder *model.ThirdPartyPaymentOrder) (bool, error)
	GetCheckoutTemplate(context.Context, int64) (int, error)
	WholeBookAvailable(context.Context, int64, int, int64) (bool, error)
	CreateCheckout(context.Context, int64, *model.RechargeSlot, int64, checkoutAttribution) (string, error)
	BindCheckout(context.Context, string, string) error
	findCheckout(context.Context, string) (*checkoutOrder, error)
	flagCheckoutForReview(context.Context, int64, string) error
	settleCheckout(context.Context, string, *payment.OrderCapture) (*CheckoutResult, error)
	preparePurchase(context.Context, int64, purchaseRequest, checkoutAttribution) (*PurchaseIntent, error)
	purchaseByReference(context.Context, int64, string, string) (*PurchaseIntent, error)
	recentPurchases(context.Context, int64) ([]PurchaseIntent, error)
	reservePayPalAttempt(context.Context, int64, string, string, string, payment.WebhookAccount) (*paypalCheckoutAttempt, error)
	reserveStripePurchase(context.Context, int64, string, string, string, payment.WebhookAccount) (*stripePurchaseAttempt, error)
	claimStripeAttempts(context.Context, payment.WebhookAccount, string, bool) ([]stripePurchaseAttempt, error)
	stripeOfferSlots(context.Context, payment.WebhookAccount, int64) ([]int, error)
	applyStripeProof(context.Context, stripePurchaseAttempt, payment.SingleCheckoutProof, *payment.LedgerEvidence) error
	finishPayPalAttempt(context.Context, *paypalCheckoutAttempt, string, string) error
	matchPayPalAttempt(context.Context, string, payment.WebhookAccount) (bool, error)
	leaseCheckoutCapture(context.Context, string) (bool, error)
	closePayPalAttempt(context.Context, string) error
	isCheckoutOrigin(context.Context, string) (bool, error)
	holdCheckoutCreate(context.Context, string) error
	holdCheckoutCapture(context.Context, string) error
	recordCaptureOutcome(context.Context, int64, string, captureOutcome) error
	payPalAvailability(context.Context, payment.WebhookAccount) (PaymentMethods, error)
}

// RechargeFulfillment credits the wallet inside the caller's settlement transaction.
type RechargeFulfillment interface {
	CreditRechargeTx(ctx context.Context, tx pgx.Tx, userID int64, chargedCoins, bonusCoins int, description string) error
}

type dbRepository struct {
	fulfillment RechargeFulfillment
}

func NewDBRepository(fulfillment RechargeFulfillment) Repository {
	return &dbRepository{fulfillment: fulfillment}
}
