package application

import (
	"context"
	"errors"
	"strings"
	"time"

	"reader-backend/internal/conversion"
	"reader-backend/internal/db"
	"reader-backend/internal/payment"
	"reader-backend/internal/payment/aftercare"
	"reader-backend/internal/payment/paypal"
)

func (s *service) ProcessSubscriptionWebhook(ctx context.Context, providerName string, payload []byte, headers map[string]string) error {
	if providerName == "" {
		providerName = payment.ProviderPayPal
	}
	if providerName == payment.ProviderStripe {
		for _, checkout := range payment.AccountCapabilities[interface {
			AccountScope() payment.WebhookAccount
			VerifySingleCheckoutEvent([]byte, string) (*payment.SingleCheckoutNotice, error)
		}](s.adapters, providerName) {
			if !paymentEnvironmentMatches(checkout.AccountScope()) {
				continue
			}
			notice, err := checkout.VerifySingleCheckoutEvent(payload, providerHeader(headers, "Stripe-Signature"))
			if err == nil && (notice == nil || notice.Account == checkout.AccountScope()) {
				return queueStripeNotice(ctx, notice, payload)
			}
		}
		return errors.New("Stripe event could not be verified for a configured account")
	}
	if providerName != payment.ProviderPayPal {
		return ErrCheckoutUnavailable
	}
	for _, provider := range payment.AccountCapabilities[payment.WebhookProvider](s.adapters, providerName) {
		accountProvider, ok := provider.(interface {
			CheckoutAccount(context.Context) (payment.WebhookAccount, error)
		})
		if !ok {
			continue
		}
		account, err := accountProvider.CheckoutAccount(ctx)
		if err != nil || !paymentEnvironmentMatches(account) {
			continue
		}
		event, err := provider.ParseWebhook(ctx, payload, headers)
		if err == nil && event != nil && event.EventID != "" && event.Account == account {
			_, registered := payment.AccountCapability[payment.WebhookProvider](s.adapters, providerName, event.Account)
			return s.repo.enqueuePayPalEvent(ctx, event, registered)
		}
	}
	return errors.New("PayPal event could not be verified for a configured account")
}

func (s *service) applyPayPalEvent(ctx context.Context, provider payment.SubscriptionProvider, event *payment.WebhookEventResult) error {
	switch event.EventType {
	case "dispute_updated":
		// The signed snapshot is durable even on a merchant without the
		// Disputes API read scope. Only opted-in accounts perform a lookup.
		var enabled bool
		lookupErr := db.Pool(ctx).QueryRow(ctx, `SELECT EXISTS(
			SELECT 1 FROM payment_channel_accounts a JOIN payment_aftercare_policies p ON p.account_id=a.id
			WHERE a.provider='paypal' AND a.account_key=$1 AND a.environment=$2 AND p.dispute_lookup
		)`, event.Account.AccountKey, event.Account.Environment).Scan(&enabled)
		if lookupErr != nil || !enabled {
			return lookupErr
		}
		adapter, err := payment.Capability[interface {
			GetDispute(context.Context, string) (paypal.Dispute, error)
		}](s.adapters, payment.ProviderPayPal)
		if err != nil {
			return err
		}
		dispute, err := adapter.GetDispute(ctx, event.ExternalRefID)
		if err != nil {
			return err
		}
		if event.Account.AccountKey != "" && event.Account != dispute.Account {
			return ErrCheckoutUnavailable
		}
		return aftercare.NewRepository(db.Pool(ctx)).Record(ctx, dispute.CaseFact("provider_api", dispute.ID, time.Now()))
	case "payment_reversed":
		// The signed notification was recorded when enqueued. Financial
		// confirmation is derived independently from authoritative ledger rows.
		return nil
	case "refund_completed":
		return s.repo.CompletePayPalRefund(ctx, event.ExternalRefID, event.CaptureID, event.AmountCents, event.Currency, event.Account)
	case "refund_status":
		return schedulePayPalRefundEventLookup(ctx, event)
	case "order_approved", "capture_completed":
		if event.ExternalRefID == "" {
			return errors.New("missing PayPal order ID")
		}
		if event.Account.AccountKey != "" && s.adapters.HasRegisteredAccounts(payment.ProviderPayPal) {
			client, err := s.payPalForOrder(ctx, event.ExternalRefID)
			if err != nil {
				return err
			}
			account, err := client.CheckoutAccount(ctx)
			if err != nil || account != event.Account {
				return ErrCheckoutUnavailable
			}
		}
		_, err := s.completePayPalOrder(ctx, 0, event.ExternalRefID, event.EventType == "order_approved" && !event.LookupOnly)
		if errors.Is(err, ErrPaymentRefunded) || (event.LookupOnly && errors.Is(err, paypal.ErrOrderApproved)) {
			return nil
		}
		if errors.Is(err, ErrCheckoutUnavailable) {
			order, lookupErr := s.repo.findCheckout(ctx, event.ExternalRefID)
			if lookupErr == nil && order.status != "Pending" {
				return nil
			}
		}
		return err
	case "payment_succeeded", "subscription_payment_failed", "subscription_activated", "subscription_cancelled", "subscription_suspended", "subscription_expired":
	default:
		return nil
	}
	if event.SubscriptionID == "" {
		return nil
	} // A one-time sale is handled by the capture event.
	sub, err := s.repo.GetSubscriptionByID(ctx, event.SubscriptionID)
	if err != nil {
		return err
	}
	if sub == nil || sub.PaymentMethod != provider.Provider() || sub.Environment != conversion.Environment() {
		return ErrCheckoutUnavailable
	}
	if event.EventType == "subscription_payment_failed" &&
		sub.LastPaymentTime != nil && !sub.LastPaymentTime.Before(event.PaidAt) {
		return nil
	}
	if event.Account.AccountKey != "" {
		client, err := s.payPalForSubscription(ctx, sub.SubscriptionID)
		if err != nil {
			return err
		}
		account, err := client.CheckoutAccount(ctx)
		if err != nil || account != event.Account {
			return ErrCheckoutUnavailable
		}
	}

	details, err := provider.GetSubscription(ctx, sub.SubscriptionID)
	if err != nil {
		return err
	}
	if details.SubscriptionID != sub.SubscriptionID || details.PlanID != sub.PlanID {
		return ErrCheckoutUnavailable
	}

	if event.EventType == "subscription_payment_failed" {
		if event.PaidAt.IsZero() {
			return errors.New("PayPal renewal failure has no event timestamp")
		}
		if details.LastPaymentTime != nil && !details.LastPaymentTime.Before(event.PaidAt) {
			return nil
		}
		if details.Status == "CANCELLED" || details.Status == "EXPIRED" {
			return s.repo.UpdateUserSubscriptionStatus(ctx, sub.SubscriptionID, details.Status)
		}
		return recordPayPalRenewalFailure(ctx, sub.SubscriptionID, event.PaidAt, details.Status)
	}

	if event.EventType != "payment_succeeded" {
		switch details.Status {
		case "APPROVED":
			if event.EventType == "subscription_activated" && !event.LookupOnly {
				return s.activateApprovedPayPalVIP(ctx, sub.SubscriptionID)
			}
		case "ACTIVE", "CANCELLED", "EXPIRED", "SUSPENDED":
			return s.repo.UpdateUserSubscriptionStatus(ctx, sub.SubscriptionID, details.Status)
		}
		return nil
	}

	// The signed notification identifies the sale. The merchant transaction list
	// confirms the amount and transaction's subscription, including catch-up payments.
	end := time.Now().UTC().Add(time.Second)
	if after := event.PaidAt.Add(24 * time.Hour); after.Before(end) {
		end = after
	}
	transactions, err := s.paypalClient.ListSubscriptionTransactions(ctx, sub.SubscriptionID, event.PaidAt.Add(-24*time.Hour), end)
	if err != nil {
		return err
	}
	for _, transaction := range transactions {
		if transaction.ID == event.ExternalRefID {
			return s.recordPayPalSubscriptionTransaction(ctx, sub, details, transaction)
		}
	}
	return errors.New("PayPal transaction is not visible in the subscription ledger yet")
}

func providerHeader(headers map[string]string, key string) string {
	for name, value := range headers {
		if strings.EqualFold(name, key) {
			return value
		}
	}
	return ""
}
