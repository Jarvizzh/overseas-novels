package stripe

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	stripego "github.com/stripe/stripe-go/v87"
	"reader-backend/internal/payment"
)

func invoiceSubscription(invoice *stripego.Invoice) string {
	if invoice.Parent == nil || invoice.Parent.SubscriptionDetails == nil || invoice.Parent.SubscriptionDetails.Subscription == nil {
		return ""
	}
	return invoice.Parent.SubscriptionDetails.Subscription.ID
}

func (c *Client) StripeInvoiceSubscription(ctx context.Context, id string) (string, error) {
	invoice, err := c.GetInvoice(ctx, id)
	if err != nil {
		return "", err
	}
	if invoice.ID != id || invoice.Livemode != (c.scope.Environment == Live) {
		return "", errors.New("Stripe invoice scope mismatch")
	}
	return invoiceSubscription(invoice), nil
}

func (c *Client) StripeVIPInvoiceRefs(ctx context.Context, id, cursor string) ([]string, string, error) {
	page, err := c.ListSubscriptionInvoices(ctx, id, PageOptions{Limit: 5, StartingAfter: cursor})
	if err != nil {
		return nil, "", err
	}
	var refs []string
	for _, invoice := range page.Items {
		if invoice.ID != "" {
			refs = append(refs, invoice.ID)
		}
	}
	next := ""
	if page.HasMore && len(refs) != 0 {
		next = refs[len(refs)-1]
	}
	return refs, next, nil
}

func (c *Client) ReadStripeVIPInvoice(ctx context.Context, id string, offer payment.StripeVIPOffer) (payment.StripeVIPInvoice, error) {
	invoice, err := c.GetInvoice(ctx, id)
	if err != nil {
		return payment.StripeVIPInvoice{}, err
	}
	subRef := invoiceSubscription(invoice)
	if invoice.ID != id || subRef == "" || invoice.Livemode != (c.scope.Environment == Live) || invoice.Customer == nil || invoice.AmountDue < 0 {
		return payment.StripeVIPInvoice{}, errors.New("Stripe invoice scope mismatch")
	}
	sub, err := c.ReadStripeVIPSubscription(ctx, subRef)
	if err != nil {
		return payment.StripeVIPInvoice{}, err
	}
	if sub.CustomerID != invoice.Customer.ID {
		return payment.StripeVIPInvoice{}, errors.New("Stripe invoice customer mismatch")
	}
	proof := payment.StripeVIPInvoice{ID: id, Subscription: sub, Currency: strings.ToUpper(string(invoice.Currency)),
		BillingReason: string(invoice.BillingReason), Status: string(invoice.Status), DueMinor: invoice.AmountDue, RemainingMinor: invoice.AmountRemaining}
	if invoice.CustomerTaxExempt != nil {
		proof.CustomerTaxStatus = string(*invoice.CustomerTaxExempt)
	}
	if invoice.CustomerAddress != nil {
		proof.Country = strings.ToUpper(invoice.CustomerAddress.Country)
	}
	if invoice.AutomaticTax != nil && invoice.AutomaticTax.Liability != nil && invoice.AutomaticTax.Liability.Account != nil {
		proof.TaxLiabilityAccount = invoice.AutomaticTax.Liability.Account.ID
		proof.TaxVerified = proof.TaxLiabilityAccount != "" && proof.TaxLiabilityAccount == offer.TaxLiabilityAccount &&
			proof.CustomerTaxStatus != "" && invoice.AutomaticTax.Status == "complete"
	}
	lines, err := c.ListInvoiceLines(ctx, id, PageOptions{Limit: 2})
	if err != nil {
		return proof, err
	}
	switch {
	case sub.ManagedPayments != (offer.SalesMode == payment.StripeSaleMoR):
		proof.ReviewReason = "Stripe billing agreement has a different sales mode"
	case proof.Currency != "USD":
		proof.ReviewReason = "Stripe VIP invoice is outside the approved USD currency"
	case invoice.AmountPaidOffStripe != 0 || invoice.AmountDue == 0:
		proof.ReviewReason = "Stripe VIP invoice is zero or contains an off-Stripe payment"
	case invoice.BillingReason != "subscription_create" && invoice.BillingReason != "subscription_cycle":
		proof.ReviewReason = "Stripe invoice is not a regular VIP billing period"
	case len(lines.Items) != 1 || lines.HasMore:
		proof.ReviewReason = "Stripe VIP invoice contains ambiguous line items"
	default:
		line := lines.Items[0]
		if line == nil {
			return proof, errors.New("Stripe returned an empty invoice line")
		}
		if line.Period != nil && line.Period.Start > 0 && line.Period.End > line.Period.Start {
			proof.StartsAt, proof.EndsAt = time.Unix(line.Period.Start, 0).UTC(), time.Unix(line.Period.End, 0).UTC()
		}
		pricing := line.Pricing
		regularLine := line.Parent != nil && line.Parent.SubscriptionItemDetails != nil &&
			line.Parent.SubscriptionItemDetails.Subscription == subRef && !line.Parent.SubscriptionItemDetails.Proration
		managed := offer.SalesMode == payment.StripeSaleMoR
		priceMatches := line.Subtotal == offer.AmountMinor || (managed && invoice.AmountDue == offer.AmountMinor && line.Subtotal == invoice.TotalExcludingTax)
		if !regularLine || pricing == nil || pricing.PriceDetails == nil || pricing.PriceDetails.Price == nil ||
			pricing.PriceDetails.Price.ID != offer.PriceID || pricing.PriceDetails.Product != offer.ProductID ||
			line.Quantity != 1 || line.Currency != stripego.CurrencyUSD || !priceMatches ||
			len(line.DiscountAmounts) > 0 || len(line.PretaxCreditAmounts) > 0 || proof.StartsAt.IsZero() || proof.EndsAt.IsZero() {
			proof.ReviewReason = "Stripe VIP invoice line does not match the published period"
		} else if !managed && invoice.AmountDue != offer.AmountMinor {
			proof.ReviewReason = "Stripe direct VIP invoice amount differs from the published price"
		} else if managed && (invoice.Total != invoice.AmountDue || invoice.TotalExcludingTax <= 0 || invoice.TotalExcludingTax > invoice.Total) {
			proof.ReviewReason = "Stripe VIP invoice has no verified amount excluding tax"
		} else if managed {
			proof.PrincipalMinor = invoice.TotalExcludingTax
		} else {
			proof.PrincipalMinor = invoice.AmountDue
		}
	}
	allocations, err := c.ListInvoicePayments(ctx, id, PageOptions{Limit: 100})
	if err != nil {
		return proof, err
	}
	if allocations.HasMore {
		proof.ReviewReason = "Stripe VIP invoice has more allocations than can be verified in one scan"
	}
	for _, item := range allocations.Items {
		if item.Status != "paid" {
			continue
		}
		allocation, err := c.stripeVIPAllocation(ctx, id, sub.CustomerID, item)
		if err != nil {
			return proof, err
		}
		if allocation == nil {
			proof.ReviewReason = "Stripe VIP invoice contains an allocation without independent captured funds"
			continue
		}
		proof.Allocations = append(proof.Allocations, *allocation)
	}
	if invoice.FromInvoice != nil && invoice.FromInvoice.Invoice != nil {
		proof.RevisesInvoiceID = invoice.FromInvoice.Invoice.ID
		if proof.Status == "paid" && proof.ReviewReason == "" {
			verified, err := c.verifyStripeVIPInvoiceRevision(ctx, invoice, proof)
			if err != nil {
				return proof, err
			}
			proof.RevisionVerified = verified
			if !verified {
				proof.ReviewReason = "Stripe VIP invoice revision needs verified original billing-period evidence"
			}
		}
	}
	return proof, nil
}

func (c *Client) verifyStripeVIPInvoiceRevision(ctx context.Context, invoice *stripego.Invoice, proof payment.StripeVIPInvoice) (bool, error) {
	if invoice.FromInvoice.Action != "revision" || !validID(proof.RevisesInvoiceID, "in_") || proof.RevisesInvoiceID == invoice.ID {
		return false, nil
	}
	original, err := c.GetInvoice(ctx, proof.RevisesInvoiceID)
	if err != nil {
		return false, err
	}
	if original.ID != proof.RevisesInvoiceID || original.Livemode != invoice.Livemode || original.Customer == nil ||
		original.Customer.ID != proof.Subscription.CustomerID || invoiceSubscription(original) != proof.Subscription.ID ||
		original.Status != stripego.InvoiceStatusVoid || original.Currency != stripego.CurrencyUSD ||
		original.LatestRevision == nil || original.LatestRevision.ID != invoice.ID || original.AmountPaid != 0 || original.AmountPaidOffStripe != 0 {
		return false, nil
	}
	lines, err := c.ListInvoiceLines(ctx, original.ID, PageOptions{Limit: 2})
	if err != nil {
		return false, err
	}
	if len(lines.Items) != 1 || lines.HasMore {
		return false, nil
	}
	line := lines.Items[0]
	return line != nil && line.Period != nil && line.Period.Start == proof.StartsAt.Unix() && line.Period.End == proof.EndsAt.Unix() &&
		line.Parent != nil && line.Parent.SubscriptionItemDetails != nil && line.Parent.SubscriptionItemDetails.Subscription == proof.Subscription.ID, nil
}

func (c *Client) stripeVIPAllocation(ctx context.Context, id, customer string, item InvoicePayment) (*payment.StripeVIPAllocation, error) {
	if item.Invoice == nil || item.Invoice.ID != id || item.Livemode != (c.scope.Environment == Live) ||
		item.Payment == nil || item.AmountPaid == nil || *item.AmountPaid <= 0 || item.Currency != stripego.CurrencyUSD {
		return nil, nil
	}
	var chargeID, intentID string
	if item.Payment.PaymentIntent != nil {
		intentID = item.Payment.PaymentIntent.ID
		intent, err := c.GetPaymentIntent(ctx, intentID)
		if err != nil {
			return nil, err
		}
		if intent.ID != intentID || intent.Livemode != item.Livemode || intent.Status != stripego.PaymentIntentStatusSucceeded ||
			intent.Customer == nil || intent.Customer.ID != customer || intent.Currency != stripego.CurrencyUSD || intent.AmountReceived < *item.AmountPaid || intent.LatestCharge == nil {
			return nil, nil
		}
		chargeID = intent.LatestCharge.ID
	} else if item.Payment.Charge != nil {
		chargeID = item.Payment.Charge.ID
	}
	if chargeID == "" {
		return nil, nil
	}
	charge, err := c.GetCharge(ctx, chargeID)
	if err != nil {
		return nil, err
	}
	if charge.ID != chargeID || charge.Livemode != item.Livemode || charge.Currency != stripego.CurrencyUSD ||
		charge.Customer == nil || charge.Customer.ID != customer ||
		!charge.Captured || !charge.Paid || charge.Status != stripego.ChargeStatusSucceeded || charge.AmountCaptured < *item.AmountPaid ||
		charge.Created <= 0 || (intentID != "" && (charge.PaymentIntent == nil || charge.PaymentIntent.ID != intentID)) {
		return nil, nil
	}
	paidAt := charge.Created
	if item.StatusTransitions != nil && item.StatusTransitions.PaidAt > paidAt {
		paidAt = item.StatusTransitions.PaidAt
	}
	receipt := payment.SingleChargeProof{ID: chargeID, IntentID: intentID, Currency: "USD", AmountMinor: charge.AmountCaptured,
		RefundedMinor: charge.AmountRefunded, PaidAt: time.Unix(paidAt, 0).UTC(),
		EligibleTerms: charge.AmountCaptured == *item.AmountPaid}
	if charge.BalanceTransaction != nil {
		receipt.MovementID = charge.BalanceTransaction.ID
	}
	c.checkChargeReview(ctx, charge, &receipt)
	receipt.Disputed = charge.Disputed
	if charge.PresentmentDetails != nil {
		receipt.PresentmentCurrency = strings.ToUpper(string(charge.PresentmentDetails.PresentmentCurrency))
	}
	receipt.Evidence, err = json.Marshal(struct {
		Invoice, Allocation, Charge, PresentmentCurrency string
		AllocatedMinor                                   int64
	}{id, item.ID, chargeID, receipt.PresentmentCurrency, *item.AmountPaid})
	if err != nil {
		return nil, err
	}
	return &payment.StripeVIPAllocation{ID: item.ID, AmountMinor: *item.AmountPaid, Receipt: receipt}, nil
}
