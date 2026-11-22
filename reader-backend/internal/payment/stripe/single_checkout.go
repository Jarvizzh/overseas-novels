package stripe

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"

	stripego "github.com/stripe/stripe-go/v87"
	"reader-backend/internal/payment"
)

const singleAttemptMetadata = "payment_attempt_id"

func (c *Client) AccountScope() payment.WebhookAccount { return ledgerAccount(c.scope) }

func (c *Client) CreateSingleCheckout(ctx context.Context, req payment.SingleCheckoutRequest) (payment.SingleCheckoutProof, error) {
	price, err := c.GetPrice(ctx, req.Offer.PriceID)
	if err != nil {
		return payment.SingleCheckoutProof{}, err
	}
	if price.ID != req.Offer.PriceID || !price.Active || price.Type != stripego.PriceTypeOneTime ||
		price.Currency != stripego.CurrencyUSD || price.UnitAmount != req.Offer.AmountMinor ||
		price.Product == nil || price.Product.ID != req.Offer.ProductID || price.Livemode != (c.scope.Environment == Live) {
		return payment.SingleCheckoutProof{}, errors.New("Stripe price does not match the published USD offer")
	}
	mode, err := c.publishedSalesMode(ctx, req.Offer)
	if err != nil {
		return payment.SingleCheckoutProof{}, err
	}
	session, err := c.CreateCheckout(ctx, CheckoutRequest{SalesMode: mode, CheckoutMode: OneTime, PriceID: price.ID,
		AttemptID: req.AttemptID, SuccessURL: req.SuccessURL, CancelURL: req.CancelURL,
		IdempotencyKey: req.AttemptID, BillingAddressRequired: true, USDOnly: true})
	if err != nil {
		return payment.SingleCheckoutProof{}, err
	}
	if (mode == MoR) != (session.ManagedPayments != nil && session.ManagedPayments.Enabled) {
		return payment.SingleCheckoutProof{}, errors.New("Stripe returned a different Checkout sales mode")
	}
	return c.singleSessionProof(session, session.ID, req.AttemptID)
}

func (c *Client) singleSessionProof(session *stripego.CheckoutSession, id, attempt string) (payment.SingleCheckoutProof, error) {
	if !validID(id, "cs_") || session.ID != id || session.ClientReferenceID != attempt ||
		session.Metadata[singleAttemptMetadata] != attempt || session.Livemode != (c.scope.Environment == Live) ||
		session.Mode != stripego.CheckoutSessionModePayment {
		return payment.SingleCheckoutProof{}, errors.New("Stripe Checkout does not match the attempt")
	}
	proof := payment.SingleCheckoutProof{ID: id, AttemptID: attempt}
	proof.Expired = session.Status == stripego.CheckoutSessionStatusExpired && session.PaymentStatus == stripego.CheckoutSessionPaymentStatusUnpaid
	if session.Status == stripego.CheckoutSessionStatusOpen && session.PaymentStatus == stripego.CheckoutSessionPaymentStatusUnpaid {
		target, err := url.Parse(session.URL)
		if err == nil && target.Scheme == "https" && target.Host == "checkout.stripe.com" && target.User == nil {
			proof.AwaitingBuyer, proof.URL = true, target.String()
		}
	}
	return proof, nil
}

func (c *Client) ReadSingleCheckout(ctx context.Context, id, attempt string, offer payment.SingleCheckoutOffer) (payment.SingleCheckoutProof, error) {
	session, err := c.GetCheckout(ctx, id)
	if err != nil {
		return payment.SingleCheckoutProof{}, err
	}
	proof, err := c.singleSessionProof(session, id, attempt)
	if err != nil {
		return proof, err
	}
	completedUnpaid := session.Status == stripego.CheckoutSessionStatusComplete && session.PaymentStatus == stripego.CheckoutSessionPaymentStatusUnpaid
	if (proof.Expired || completedUnpaid) && session.PaymentIntent != nil {
		intent, err := c.GetPaymentIntent(ctx, session.PaymentIntent.ID)
		if err != nil {
			return payment.SingleCheckoutProof{}, err
		}
		uncollected := intent.ID == session.PaymentIntent.ID && intent.Livemode == session.Livemode &&
			intent.Metadata[singleAttemptMetadata] == attempt && intent.AmountReceived == 0
		canceled := uncollected && intent.Status == stripego.PaymentIntentStatusCanceled
		failed := completedUnpaid && uncollected && intent.Status == stripego.PaymentIntentStatusRequiresPaymentMethod && intent.LatestCharge != nil
		if (canceled || failed) && intent.LatestCharge != nil {
			charge, err := c.GetCharge(ctx, intent.LatestCharge.ID)
			if err != nil {
				return payment.SingleCheckoutProof{}, err
			}
			failedCharge := charge.ID == intent.LatestCharge.ID && charge.Livemode == session.Livemode &&
				charge.PaymentIntent != nil && charge.PaymentIntent.ID == intent.ID &&
				charge.Status == stripego.ChargeStatusFailed && charge.AmountCaptured == 0 && !charge.Paid && !charge.Captured
			canceled = canceled && failedCharge
			failed = failed && failedCharge
		}
		proof.Failed = failed || (completedUnpaid && canceled)
		proof.Expired = proof.Expired && canceled
	}
	if session.PaymentStatus != stripego.CheckoutSessionPaymentStatusPaid {
		return proof, nil
	}
	if session.Status != stripego.CheckoutSessionStatusComplete || session.PaymentIntent == nil {
		return proof, errors.New("Stripe reports payment without a completed Checkout and PaymentIntent")
	}
	intent, err := c.GetPaymentIntent(ctx, session.PaymentIntent.ID)
	if err != nil {
		return proof, err
	}
	if intent.ID != session.PaymentIntent.ID || intent.Metadata[singleAttemptMetadata] != attempt ||
		intent.Livemode != session.Livemode || intent.Status != stripego.PaymentIntentStatusSucceeded || intent.LatestCharge == nil {
		return proof, errors.New("Stripe PaymentIntent does not confirm this Checkout")
	}
	charge, err := c.GetCharge(ctx, intent.LatestCharge.ID)
	if err != nil {
		return proof, err
	}
	if charge.ID != intent.LatestCharge.ID || charge.PaymentIntent == nil || charge.PaymentIntent.ID != intent.ID ||
		charge.Livemode != session.Livemode || charge.Currency != intent.Currency || charge.AmountCaptured <= 0 ||
		charge.AmountCaptured != intent.AmountReceived || !charge.Captured || !charge.Paid ||
		charge.Status != stripego.ChargeStatusSucceeded || charge.Created <= 0 {
		return proof, errors.New("Stripe Charge does not prove the captured amount for this Checkout")
	}
	lines, err := c.ListCheckoutLines(ctx, id, PageOptions{Limit: 2})
	if err != nil {
		return proof, err
	}
	var country, taxStatus, taxAccount, presentment string
	if session.CustomerDetails != nil {
		taxStatus = string(session.CustomerDetails.TaxExempt)
		if session.CustomerDetails.Address != nil {
			country = strings.ToUpper(session.CustomerDetails.Address.Country)
		}
	}
	if session.AutomaticTax != nil && session.AutomaticTax.Liability != nil && session.AutomaticTax.Liability.Account != nil {
		taxAccount = session.AutomaticTax.Liability.Account.ID
	}
	if charge.PresentmentDetails != nil {
		presentment = strings.ToUpper(string(charge.PresentmentDetails.PresentmentCurrency))
	}
	managed := offer.SalesMode == payment.StripeSaleMoR
	modeMatches := managed == (session.ManagedPayments != nil && session.ManagedPayments.Enabled) &&
		managed == (intent.ManagedPayments != nil && intent.ManagedPayments.Enabled)
	taxMatches := !managed || (taxAccount != "" && taxAccount == offer.TaxLiabilityAccount && taxStatus != "" &&
		session.AutomaticTax.Status == stripego.CheckoutSessionAutomaticTaxStatusComplete)
	var matching bool
	var principal int64
	if len(lines.Items) == 1 && !lines.HasMore {
		line := lines.Items[0]
		if line != nil && line.AmountTax >= 0 && line.AmountTax < line.AmountTotal {
			principal = line.AmountTotal - line.AmountTax
			matching = line.Price != nil && line.Price.ID == offer.PriceID && line.Quantity == 1 &&
				line.Currency == stripego.CurrencyUSD && line.AmountTotal == session.AmountTotal && line.AmountDiscount == 0 &&
				(line.AmountSubtotal == offer.AmountMinor || (managed && line.AmountTotal == offer.AmountMinor && line.AmountSubtotal == principal)) &&
				(managed || line.AmountTax == 0)
		}
	}
	receipt := payment.SingleChargeProof{ID: charge.ID, IntentID: intent.ID, Currency: strings.ToUpper(string(charge.Currency)),
		Country: country, AmountMinor: charge.AmountCaptured, RefundedMinor: charge.AmountRefunded, PrincipalMinor: principal,
		TaxLiabilityAccount: taxAccount, CustomerTaxStatus: taxStatus, PresentmentCurrency: presentment, TaxVerified: taxMatches,
		PaidAt: time.Unix(charge.Created, 0).UTC(), EligibleTerms: matching && session.Currency == stripego.CurrencyUSD &&
			session.AmountTotal == charge.AmountCaptured && (managed || charge.AmountCaptured == offer.AmountMinor) &&
			modeMatches && (presentment == "" || presentment == "USD")}
	if charge.BalanceTransaction != nil {
		receipt.MovementID = charge.BalanceTransaction.ID
	}
	receipt.Evidence, err = json.Marshal(struct {
		CheckoutID, IntentID, ChargeID, BillingCountry, TaxAccount, CustomerTaxStatus, PresentmentCurrency string
		AmountMinor, RefundedMinor, PrincipalMinor                                                         int64
	}{session.ID, intent.ID, charge.ID, country, taxAccount, taxStatus, presentment, receipt.AmountMinor, receipt.RefundedMinor, principal})
	if err != nil {
		return proof, err
	}
	c.checkChargeReview(ctx, charge, &receipt)
	receipt.Disputed = charge.Disputed

	proof.Receipt = &receipt
	return proof, nil
}

func (c *Client) FindSingleCheckout(ctx context.Context, attempt string, from, through time.Time) (string, error) {
	return c.findCheckoutByReference(ctx, attempt, from, through, stripego.CheckoutSessionModePayment)
}

func (c *Client) findCheckoutByReference(ctx context.Context, attempt string, from, through time.Time, mode stripego.CheckoutSessionMode) (string, error) {

	query := PageOptions{Limit: 100}
	var found string
	for range 10 {
		page, err := c.ListCheckouts(ctx, TimeRange{From: from, Before: through}, query)
		if err != nil {
			return "", err
		}
		for _, session := range page.Items {
			if session.ClientReferenceID != attempt || session.Mode != mode {
				continue
			}
			if session.Metadata[singleAttemptMetadata] != attempt || !validID(session.ID, "cs_") || session.Livemode != (c.scope.Environment == Live) {
				return "", errors.New("Stripe recovered checkout scope mismatch")
			}
			if found != "" && found != session.ID {
				return "", errors.New("multiple Stripe sessions for this attempt")
			}
			found = session.ID
		}
		if !page.HasMore {
			return found, nil
		}
		if len(page.Items) == 0 {
			break
		}
		query.StartingAfter = page.Items[len(page.Items)-1].ID
	}
	return "", errors.New("Stripe recovery exceeds the checkout lookup budget")
}

func (c *Client) VerifySingleCheckoutEvent(body []byte, signature string) (*payment.SingleCheckoutNotice, error) {
	event, err := c.VerifyWebhook(body, signature)
	if err != nil {
		return nil, err
	}
	return c.PaymentNotice(event)
}

func (c *Client) PaymentNotice(event Event) (*payment.SingleCheckoutNotice, error) {
	if event.Scope != c.scope {
		return nil, errors.New("Stripe event scope mismatch")
	}
	if event.IncompleteSnapshot {
		return nil, errIncompleteSnapshot
	}
	notice := payment.SingleCheckoutNotice{EventID: event.ID, EventType: string(event.Type), Account: ledgerAccount(event.Scope)}
	switch event.Type {
	case "invoice_payment.paid":
		allocation, err := decodeObject[stripego.InvoicePayment](event)
		if err != nil {
			return nil, err
		}
		if !validID(event.ObjectID, "inpay_") || allocation.ID != event.ObjectID ||
			allocation.Livemode != (event.Scope.Environment == Live) ||
			allocation.Invoice == nil || !validID(allocation.Invoice.ID, "in_") {
			return nil, errors.New("invalid Stripe invoice payment event")
		}
		notice.InvoiceID = allocation.Invoice.ID
		return &notice, nil
	case "invoice.paid", "invoice.payment_succeeded", "invoice.payment_failed", "invoice.payment_action_required",
		"invoice.finalized", "invoice.voided", "invoice.marked_uncollectible":
		if !validID(event.ObjectID, "in_") {
			return nil, errors.New("invalid Stripe invoice event")
		}
		notice.InvoiceID = event.ObjectID
		return &notice, nil
	case "customer.subscription.created", "customer.subscription.updated", "customer.subscription.deleted":
		if !validID(event.ObjectID, "sub_") {
			return nil, errors.New("invalid Stripe subscription event")
		}
		notice.SubscriptionID = event.ObjectID
		return &notice, nil
	case "refund.created", "refund.updated", "refund.failed":
		if !strings.HasPrefix(event.ObjectID, "re_") && !strings.HasPrefix(event.ObjectID, "pyr_") {
			return nil, errors.New("invalid Stripe refund event")
		}
		return &payment.SingleCheckoutNotice{EventID: event.ID, EventType: string(event.Type), RefundID: event.ObjectID, Account: ledgerAccount(event.Scope)}, nil
	case "payout.paid", "payout.failed", "payout.canceled", "payout.reconciliation_completed":
		if !validID(event.ObjectID, "po_") || event.ObjectType != "payout" {
			return nil, errors.New("invalid Stripe payout event")
		}
		notice.SettlementReference = event.ObjectID
		return &notice, nil
	case "reporting.report_run.succeeded":
		report, err := decodeObject[stripego.ReportingReportRun](event)
		if err != nil || !validID(event.ObjectID, "frr_") || report.ID != event.ObjectID || report.Status != "succeeded" ||
			report.Livemode != (event.Scope.Environment == Live) {
			return nil, errors.New("invalid Stripe completed report event")
		}
		if !strings.HasPrefix(report.ReportType, "payout_reconciliation.itemized.") && !strings.HasPrefix(report.ReportType, "balance_change_from_activity.itemized.") {
			return nil, nil
		}
		notice.SettlementReference = event.ObjectID
		return &notice, nil
	case "checkout.session.completed", "checkout.session.async_payment_succeeded", "checkout.session.async_payment_failed", "checkout.session.expired":
	default:
		if !stripeAftercareNotice(event.Type) {
			return nil, nil
		}
		var err error
		notice.Aftercare, err = json.Marshal(event)
		return &notice, err
	}
	session, err := decodeObject[stripego.CheckoutSession](event)
	if err != nil {
		return nil, err
	}
	attempt := session.Metadata[singleAttemptMetadata]
	if attempt != "" && session.Mode == stripego.CheckoutSessionModeSubscription {
		if !validID(event.ObjectID, "cs_") || session.ID != event.ObjectID || session.ClientReferenceID != attempt ||
			session.Livemode != (event.Scope.Environment == Live) {
			return nil, errors.New("invalid Stripe VIP session event")
		}
		notice.VIPIntentID, notice.SessionID = attempt, session.ID
		return &notice, nil
	}
	if attempt == "" || session.Mode != stripego.CheckoutSessionModePayment {
		return nil, nil
	}
	if _, err := c.singleSessionProof(session, event.ObjectID, attempt); err != nil {
		return nil, err
	}
	return &payment.SingleCheckoutNotice{EventID: event.ID, EventType: string(event.Type), SessionID: session.ID, AttemptID: attempt,
		Account: ledgerAccount(event.Scope)}, nil
}

func (c *Client) checkChargeReview(ctx context.Context, charge *stripego.Charge, receipt *payment.SingleChargeProof) {
	if charge.Review != nil {
		observation, reviewErr := c.AftercareEvent(ctx, Event{Scope: c.scope, Source: EventFromAPI, ID: charge.Review.ID,
			Type: "review.opened", ObjectID: charge.Review.ID, Created: charge.Created})
		if reviewErr != nil || observation == nil || len(observation.Fact.ReceiptRefs) != 1 ||
			observation.Fact.ReceiptRefs[0] != charge.ID {
			receipt.ReviewUnconfirmed = true
		} else {
			receipt.RiskReview = &observation.Fact
		}
	}

}

func (c *Client) publishedSalesMode(ctx context.Context, offer payment.SingleCheckoutOffer) (SalesMode, error) {
	switch offer.SalesMode {
	case "", payment.StripeSaleDirect:
		return Direct, nil
	case payment.StripeSaleMoR:
		if offer.TaxLiabilityAccount == "" {
			return "", errors.New("Stripe Managed Payments requires an approved tax liability account")
		}
		product, err := c.GetProduct(ctx, offer.ProductID)
		if err != nil {
			return "", err
		}
		if product.ID != offer.ProductID || !product.Active || product.Livemode != (c.scope.Environment == Live) ||
			product.TaxCode == nil || product.TaxCode.ID == "" {
			return "", errors.New("Stripe Managed Payments requires an active product with a tax code")
		}
		return MoR, nil
	default:
		return "", errors.New("invalid Stripe sales mode")
	}
}

func (c *Client) CloseSingleCheckout(ctx context.Context, id, attempt string, offer payment.SingleCheckoutOffer) (payment.SingleCheckoutProof, error) {
	session, err := c.GetCheckout(ctx, id)
	if err != nil {
		return payment.SingleCheckoutProof{}, err
	}
	if _, err := c.singleSessionProof(session, id, attempt); err != nil {
		return payment.SingleCheckoutProof{}, err
	}
	if session.Status == stripego.CheckoutSessionStatusOpen {
		_, closeErr := c.ExpireCheckout(ctx, id, "switch-"+attempt)
		proof, err := c.ReadSingleCheckout(ctx, id, attempt, offer)
		if err != nil {
			return proof, err
		}
		if !proof.Expired && !proof.Failed && proof.Receipt == nil && closeErr != nil {
			return proof, closeErr
		}
		return proof, nil
	}
	return c.ReadSingleCheckout(ctx, id, attempt, offer)
}

func stripeAftercareNotice(kind stripego.EventType) bool {
	if strings.HasPrefix(string(kind), "charge.dispute.") || strings.HasPrefix(string(kind), "radar.early_fraud_warning.") {
		return true
	}
	switch kind {
	case "charge.succeeded", "charge.failed", "payment_intent.payment_failed", "review.opened", "review.closed":
		return true
	default:
		return false
	}
}

func (c *Client) ResolveQueuedAftercare(ctx context.Context, snapshot json.RawMessage) (*payment.CaseObservation, error) {
	var event Event
	if err := json.Unmarshal(snapshot, &event); err != nil {
		return nil, err
	}
	if !stripeAftercareNotice(event.Type) {
		return nil, errors.New("not a Stripe aftercare event")
	}
	return c.AftercareEvent(ctx, event)
}
