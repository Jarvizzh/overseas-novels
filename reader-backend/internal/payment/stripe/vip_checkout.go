package stripe

import (
	"context"
	"errors"
	"net/url"
	"time"

	stripego "github.com/stripe/stripe-go/v87"
	"reader-backend/internal/payment"
)

func (c *Client) CreateStripeVIPCheckout(ctx context.Context, req payment.StripeVIPCheckoutRequest) (payment.StripeVIPCheckout, error) {
	price, err := c.GetPrice(ctx, req.Offer.PriceID)
	if err != nil {
		return payment.StripeVIPCheckout{}, err
	}
	if price.ID != req.Offer.PriceID || !price.Active || price.Type != stripego.PriceTypeRecurring ||
		price.Currency != stripego.CurrencyUSD || price.UnitAmount != req.Offer.AmountMinor ||
		price.Product == nil || price.Product.ID != req.Offer.ProductID || price.Recurring == nil ||
		price.Recurring.IntervalCount != 1 || string(price.Recurring.Interval) != req.Offer.Cycle ||
		price.Livemode != (c.scope.Environment == Live) {
		return payment.StripeVIPCheckout{}, errors.New("Stripe recurring Price does not match the published VIP offer")
	}
	mode, err := c.publishedSalesMode(ctx, req.Offer.SingleCheckoutOffer)
	if err != nil {
		return payment.StripeVIPCheckout{}, err
	}
	session, err := c.CreateCheckout(ctx, CheckoutRequest{SalesMode: mode, CheckoutMode: Subscription, PriceID: price.ID, AttemptID: req.IntentID,
		IdempotencyKey: req.IntentID, SuccessURL: req.ReturnURL, CancelURL: req.CancelURL, BillingAddressRequired: true, USDOnly: true})
	if err != nil {
		return payment.StripeVIPCheckout{}, err
	}
	return c.vipCheckoutSnapshot(session, session.ID, req.IntentID, req.Offer.SalesMode)
}

func (c *Client) vipCheckoutSnapshot(s *stripego.CheckoutSession, id, intent, mode string) (payment.StripeVIPCheckout, error) {
	if !validID(id, "cs_") || s.ID != id || s.Mode != stripego.CheckoutSessionModeSubscription || s.ClientReferenceID != intent ||
		s.Metadata[singleAttemptMetadata] != intent || s.Livemode != (c.scope.Environment == Live) ||
		(mode == payment.StripeSaleMoR) != (s.ManagedPayments != nil && s.ManagedPayments.Enabled) {
		return payment.StripeVIPCheckout{}, errors.New("Stripe VIP Checkout does not match scope, mode or intent")
	}
	snapshot := payment.StripeVIPCheckout{ID: s.ID, IntentID: intent}
	if s.Status == stripego.CheckoutSessionStatusExpired && s.Subscription == nil {
		snapshot.Expired = true
	}
	if s.Status == stripego.CheckoutSessionStatusComplete && s.Subscription != nil {
		snapshot.SubscriptionID = s.Subscription.ID
	}
	if s.Status == stripego.CheckoutSessionStatusOpen {
		target, err := url.Parse(s.URL)
		if err == nil && target.Scheme == "https" && target.Host == "checkout.stripe.com" && target.User == nil {
			snapshot.URL, snapshot.AwaitingBuyer = target.String(), true
		}
	}
	return snapshot, nil
}

func (c *Client) ReadStripeVIPCheckout(ctx context.Context, id, intent, mode string) (payment.StripeVIPCheckout, error) {
	session, err := c.GetCheckout(ctx, id)
	if err != nil {
		return payment.StripeVIPCheckout{}, err
	}
	return c.vipCheckoutSnapshot(session, id, intent, mode)
}

func (c *Client) FindStripeVIPCheckout(ctx context.Context, intent string, from, through time.Time) (string, error) {
	return c.findCheckoutByReference(ctx, intent, from, through, stripego.CheckoutSessionModeSubscription)
}

func (c *Client) ReadStripeVIPSubscription(ctx context.Context, id string) (payment.StripeVIPSubscription, error) {
	observedAt := time.Now().UTC()
	item, err := c.GetSubscription(ctx, id)
	if err != nil {
		return payment.StripeVIPSubscription{}, err
	}
	if item.ID != id || item.Livemode != (c.scope.Environment == Live) || item.Customer == nil {
		return payment.StripeVIPSubscription{}, errors.New("Stripe VIP subscription scope mismatch")
	}
	proof := payment.StripeVIPSubscription{ID: id, IntentID: item.Metadata[singleAttemptMetadata], CustomerID: item.Customer.ID,
		Status: string(item.Status), CancelAtPeriodEnd: item.CancelAtPeriodEnd, ObservedAt: observedAt,
		ManagedPayments: item.ManagedPayments != nil && item.ManagedPayments.Enabled}
	if item.Items != nil && len(item.Items.Data) == 1 && !item.Items.HasMore && item.Items.Data[0].Price != nil &&
		item.Items.Data[0].Quantity == 1 {
		proof.PriceID = item.Items.Data[0].Price.ID
	}
	if item.LatestInvoice != nil {
		proof.LatestInvoice = item.LatestInvoice.ID
	}
	return proof, nil
}

func (c *Client) ScheduleStripeVIPCancellation(ctx context.Context, id, key string) (payment.StripeVIPSubscription, error) {
	if _, err := c.CancelSubscriptionAtPeriodEnd(ctx, id, key); err != nil {
		return payment.StripeVIPSubscription{}, err
	}
	return c.ReadStripeVIPSubscription(ctx, id)
}

func (c *Client) OpenStripeVIPPortal(ctx context.Context, customer, returnURL, key string) (string, error) {
	portal, err := c.createCustomerPortal(ctx, customer, returnURL, key, &stripego.BillingPortalSessionCreateFlowDataParams{
		Type: stripego.String(string(stripego.BillingPortalSessionFlowTypePaymentMethodUpdate)),
		AfterCompletion: &stripego.BillingPortalSessionCreateFlowDataAfterCompletionParams{
			Type:     stripego.String("redirect"),
			Redirect: &stripego.BillingPortalSessionCreateFlowDataAfterCompletionRedirectParams{ReturnURL: stripego.String(returnURL)},
		},
	})
	if err != nil {
		return "", err
	}
	target, err := url.Parse(portal.URL)
	if err != nil || target.Scheme != "https" || target.Host != "billing.stripe.com" || target.User != nil {
		return "", errors.New("unexpected Stripe customer portal URL")
	}
	return target.String(), nil
}
