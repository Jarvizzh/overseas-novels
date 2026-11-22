package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"reader-backend/internal/conversion"
	"reader-backend/internal/model"
	"reader-backend/internal/payment"
)

func (s *service) CreateSubscription(
	ctx context.Context,
	userID int64,
	providerName string,
	slotID int,
	returnURL, cancelURL, returnPath string,
	fbp, fbc, pixelID, ip, ua, sourceURL, country string,
) (*payment.SubscriptionResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if providerName == "" {
		providerName = payment.ProviderPayPal
	}

	provider, err := payment.Capability[subscriptionCheckout](s.adapters, providerName)
	if err != nil {
		return nil, err
	}
	if providerName == payment.ProviderPayPal {
		provider, err = s.payPalForNewCheckout(ctx, userID, "", slotID)
		if err != nil {
			return nil, err
		}
	}

	account, err := provider.CheckoutAccount(ctx)
	if err != nil {
		return nil, err
	}
	if !paymentEnvironmentMatches(account) {
		return nil, ErrCheckoutPaused
	}
	if !provider.WebhookConfigured(ctx) {
		return nil, errors.New("payment channel webhook is not configured")
	}
	if err := s.validateCheckoutRedirects(ctx, returnURL, cancelURL); err != nil {
		return nil, err
	}

	// An existing intent can resume even if CMS has since replaced its slot.
	slot, err := s.checkoutSlot(ctx, userID, slotID)
	if err != nil && !errors.Is(err, ErrCheckoutUnavailable) {
		return nil, err
	}
	var templateID int
	if slot != nil {
		templateID = slot.TemplateID
	}
	intent, err := s.repo.reserveSubscriptionIntent(ctx, subscriptionIntent{userID: userID, slotID: slotID,
		templateID: templateID, returnURL: returnURL, cancelURL: cancelURL, returnPath: checkoutReadingPath(returnPath, 0),
		account:     account,
		attribution: checkoutAttribution{FBP: fbp, FBC: fbc, IP: ip, UserAgent: ua, SourceURL: sourceURL, Country: country}})
	if err != nil {
		return nil, err
	}
	if intent.approvalURL != "" {
		return &payment.SubscriptionResult{SubscriptionID: intent.subscriptionID, ApproveURL: intent.approvalURL, Status: "APPROVAL_PENDING", IntentID: intent.id}, nil
	}
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		s.repo.releaseSubscriptionIntent(releaseCtx, intent.id)
	}()
	if intent.planID == "" {
		existingPlan, err := s.repo.GetProviderPlan(ctx, providerName, intent.slotID)
		if err != nil {
			return nil, err
		}
		// Only a pre-migration intent may need its original, exact legacy mapping.
		if existingPlan == nil || existingPlan.ExternalPlanID == "" || existingPlan.PriceCents != intent.priceCents ||
			existingPlan.Cycle != intent.cycle || existingPlan.Currency != "USD" {
			return nil, ErrSubscriptionReview
		}
		intent.planID = existingPlan.ExternalPlanID
	}
	if err := s.repo.submitSubscriptionIntent(ctx, intent.id, intent.planID); err != nil {
		return nil, err
	}
	result, err := provider.CreateSubscription(ctx, payment.CreateSubscriptionParam{
		Account:   account,
		RequestID: intent.id, UserID: userID, PlanID: intent.planID, SlotID: intent.slotID, PriceCents: int64(intent.priceCents),
		Currency: "USD", CustomID: intent.id, ReturnURL: intent.returnURL, CancelURL: intent.cancelURL,
	})
	if err != nil {
		return nil, err
	}
	if err := s.repo.finishSubscriptionIntent(ctx, intent, result); err != nil {
		return nil, err
	}
	result.IntentID = intent.id

	return result, nil
}

func (s *service) ActivateSubscription(
	ctx context.Context,
	userID int64,
	providerName string,
	subscriptionID string,
	fbp, fbc, pixelID, ip, ua, sourceURL, country string,
) error {
	if providerName == "" {
		providerName = payment.ProviderPayPal
	}
	sub, err := s.repo.GetSubscriptionByID(ctx, subscriptionID)
	if err != nil {
		return err
	}
	if sub == nil || sub.UserID != userID || sub.PaymentMethod != providerName || sub.Environment != conversion.Environment() {
		return ErrCheckoutUnavailable
	}
	provider, err := payment.Capability[payment.SubscriptionProvider](s.adapters, providerName)
	if err != nil {
		return err
	}
	if providerName == payment.ProviderPayPal {
		provider, err = s.payPalForSubscription(ctx, subscriptionID)
		if err != nil {
			return err
		}
	}
	details, err := provider.GetSubscription(ctx, subscriptionID)
	if err != nil {
		return err
	}
	if details.PlanID != sub.PlanID || details.SubscriptionID != subscriptionID {
		return ErrCheckoutUnavailable
	}
	switch details.Status {
	case "APPROVED":
		if providerName == payment.ProviderPayPal {
			return s.activateApprovedPayPalVIP(ctx, subscriptionID)
		}
		return provider.ActivateSubscription(ctx, subscriptionID)
	case "ACTIVE":
		if details.LastPaymentTime == nil {
			return nil
		}
		end := time.Now().UTC().Truncate(time.Second)
		// The recurring reconciliation job retries if the transaction is not
		// visible in PayPal's ledger while the buyer is returning.
		if err := s.importPayPalSubscriptionTransactions(ctx, sub, details, end.Add(-24*time.Hour), end); err != nil {
			return fmt.Errorf("subscription payment confirmation pending: %w", err)
		}
		return nil
	default:
		return errors.New("subscription is not approved")
	}
}

func (s *service) GetManagedSubscription(ctx context.Context, userID int64) (*model.UserSubscription, error) {
	return s.repo.GetManagedSubscriptionByUserID(ctx, userID)
}

func (s *service) CancelSubscription(ctx context.Context, userID int64, subscriptionID string, reason string) error {
	sub, err := s.repo.GetSubscriptionByID(ctx, subscriptionID)
	if err != nil {
		return err
	}
	if sub == nil || sub.UserID != userID || sub.Environment != conversion.Environment() {
		return ErrCheckoutUnavailable
	}
	switch sub.PaymentMethod {
	case payment.ProviderPayPal:
		return s.repo.enqueuePayPalCancellation(ctx, subscriptionID, userID, reason)
	case payment.ProviderStripe:
		return s.cancelStripeVIP(ctx, userID, subscriptionID, reason)
	default:
		return ErrCheckoutUnavailable
	}
}

func (s *service) recordPayPalSubscriptionTransaction(ctx context.Context, sub *model.UserSubscription,
	details *payment.SubscriptionDetails, transaction payment.SubscriptionTransaction) error {
	if transaction.Status != "COMPLETED" && transaction.Status != "REFUNDED" &&
		transaction.Status != "PARTIALLY_REFUNDED" {
		return nil
	}
	amount, err := payment.ParseCents(transaction.Amount.Gross.Value)
	if err != nil || transaction.ID == "" || transaction.Time.IsZero() || transaction.Amount.Gross.Currency != sub.Currency {
		return errors.New("invalid PayPal subscription transaction")
	}
	var periodEnd time.Time
	if transaction.Status == "COMPLETED" && details.LastPaymentTime != nil &&
		transaction.Time.Sub(*details.LastPaymentTime).Abs() <= time.Minute &&
		details.LastPaymentCents == amount && details.LastPaymentCurrency == sub.Currency &&
		amount >= int64(sub.PriceCents) && details.CurrentPeriodEnd != nil && details.CurrentPeriodEnd.After(transaction.Time) {
		periodEnd = *details.CurrentPeriodEnd
	}

	if periodEnd.IsZero() && transaction.Status == "COMPLETED" {
		periodEnd, err = s.repo.getPayPalBillingSnapshot(ctx, sub.SubscriptionID, transaction.Time, sub.Currency, amount)
		if err != nil {
			return err
		}
		if periodEnd.IsZero() && details.Status == "CANCELLED" && details.CurrentPeriodStart != nil &&
			details.LastPaymentTime != nil && transaction.Time.Sub(*details.LastPaymentTime).Abs() <= time.Minute &&
			details.LastPaymentCents == amount && details.LastPaymentCurrency == sub.Currency && amount == int64(sub.PriceCents) {
			periodEnd = regularPayPalPeriodEnd(sub, *details.CurrentPeriodStart, transaction.Time)
		}
	}

	raw, err := json.Marshal(transaction)
	if err != nil {
		return err
	}
	tpOrder := &model.ThirdPartyPaymentOrder{
		PaymentProvider: sub.PaymentMethod, ExternalOrderID: sub.SubscriptionID,
		CaptureID: transaction.ID, Currency: sub.Currency, PayerEmail: details.PayerEmail, Status: transaction.Status,
		PayerID: details.PayerID, PayerName: details.PayerName, RawPayload: string(raw),
	}
	_, err = s.repo.RecordSubscriptionRenewalOrderTx(ctx, sub, transaction.ID, amount, sub.Currency,
		transaction.Time, periodEnd, details.Status, tpOrder)
	return err
}

func (s *service) HasSubscriptionAccess(ctx context.Context, id int64) (bool, error) {
	return s.readingAccess.HasActiveSubscription(ctx, id)
}

func (s *service) ListManagedSubscriptions(ctx context.Context, id int64) ([]model.UserSubscription, error) {
	return s.repo.ListManagedSubscriptions(ctx, id)
}
