package stripe

import (
	"context"
	"errors"
	"strings"

	stripego "github.com/stripe/stripe-go/v87"
	"reader-backend/internal/payment"
)

const (
	refundCommandMetadata    = "payment_refund_command_id"
	refundLookupPageLimit    = 5
	disputeLookupPageLimit   = 5
	stripePaginationPageSize = 100
)

func (c *Client) ReadStripeRefund(ctx context.Context, id string) (*payment.StripeRefundProof, error) {
	refund, err := c.GetRefund(ctx, id)
	if err != nil {
		return nil, err
	}
	if refund.ID != id {
		return nil, errors.New("Stripe refund identity mismatch")
	}
	return stripeRefundProof(refund)
}

func stripeRefundProof(refund *stripego.Refund) (*payment.StripeRefundProof, error) {
	if refund == nil || refund.ID == "" || refund.Charge == nil || refund.Charge.ID == "" || refund.Amount <= 0 {
		return nil, errors.New("incomplete Stripe refund proof")
	}
	return &payment.StripeRefundProof{ID: refund.ID, ChargeID: refund.Charge.ID, Status: string(refund.Status),
		Currency: strings.ToUpper(string(refund.Currency)), AmountMinor: refund.Amount, CommandKey: refund.Metadata[refundCommandMetadata]}, nil
}

func (c *Client) ResolveStripeRefund(ctx context.Context, req payment.RefundRequest) (*payment.StripeRefundProof, error) {
	if req.Account != c.AccountScope() || req.Currency != "USD" || req.AmountMinor <= 0 || !validChargeID(req.ReceiptID) {
		return nil, errors.New("Stripe refund scope or amount mismatch")
	}
	if req.RefundID != "" {
		return c.ReadStripeRefund(ctx, req.RefundID)
	}
	if !req.Send {
		return c.findStripeRefundCommand(ctx, req.ReceiptID, req.RequestKey)
	}
	if err := c.verifyStripeRefundEligibility(ctx, req); err != nil {
		return nil, err
	}
	params, err := writeParams(req.RequestKey)
	if err != nil {
		return nil, err
	}
	refund, err := apiResult(c.api.V1Refunds.Create(ctx, &stripego.RefundCreateParams{
		Params: params, Charge: stripego.String(req.ReceiptID), Amount: stripego.Int64(req.AmountMinor),
		Metadata: map[string]string{refundCommandMetadata: req.RequestKey},
	}))
	if err != nil {
		return nil, err
	}
	return stripeRefundProof(refund)
}

// verifyStripeRefundEligibility verifies live Charge receipt, remaining balance,
// and live Dispute status (treating charge.Disputed only as a verification signal).
func (c *Client) verifyStripeRefundEligibility(ctx context.Context, req payment.RefundRequest) error {
	charge, err := c.GetCharge(ctx, req.ReceiptID)
	if err != nil {
		return errors.Join(payment.ErrStripeRefundVerificationPending, err)
	}
	if charge == nil || charge.ID != req.ReceiptID || charge.Livemode != (c.scope.Environment == Live) {
		return payment.ErrStripeRefundVerificationPending
	}
	if charge.Currency != stripego.CurrencyUSD || !charge.Paid || !charge.Captured {
		return payment.ErrStripeRefundRequiresReview
	}
	remaining := charge.AmountCaptured - charge.AmountRefunded
	if remaining <= 0 || req.AmountMinor > remaining {
		return payment.ErrStripeRefundInsufficientBalance
	}
	if req.StopLossFullRemaining && req.AmountMinor != remaining {
		return payment.ErrStripeRefundVerificationPending
	}
	if !charge.Disputed && !req.VerifyDisputes {
		return nil
	}
	return c.verifyChargeDisputeRefundEligibility(ctx, charge, req, remaining)
}

func (c *Client) verifyChargeDisputeRefundEligibility(ctx context.Context, charge *stripego.Charge, req payment.RefundRequest, remaining int64) error {
	disputes, err := c.listAllChargeDisputes(ctx, charge.ID)
	if err != nil {
		return errors.Join(payment.ErrStripeRefundVerificationPending, err)
	}
	if len(disputes) == 0 {
		if charge.Disputed {
			// Conflicting evidence: Charge reports disputed=true but no Dispute object is readable.
			return payment.ErrStripeRefundVerificationPending
		}
		return nil
	}
	hasWonNotYetRefundable := false
	hasOpenInquiry := false
	for _, d := range disputes {
		if d == nil || d.Livemode != (c.scope.Environment == Live) || d.Charge == nil || d.Charge.ID != charge.ID {
			return payment.ErrStripeRefundVerificationPending
		}
		switch ClassifyDisputeStatus(string(d.Status)) {
		case DisputeCategoryFormalOpen:
			return payment.ErrStripeRefundOpenDispute
		case DisputeCategoryClosedLost:
			return payment.ErrStripeRefundDisputeLost
		case DisputeCategoryInquiryOpen:
			hasOpenInquiry = true
			if !d.IsChargeRefundable {
				return payment.ErrStripeRefundNonRefundableInquiry
			}
		case DisputeCategoryClosedWon:
			if !d.IsChargeRefundable {
				hasWonNotYetRefundable = true
			}
		case DisputeCategoryClosedOther:
			// warning_closed or prevented: follow remaining balance and live refundability.
			if !d.IsChargeRefundable && remaining <= 0 {
				return payment.ErrStripeRefundInsufficientBalance
			}
		default:
			return payment.ErrStripeRefundVerificationPending
		}
	}
	if hasWonNotYetRefundable {
		return payment.ErrStripeRefundWonNotYetRefundable
	}
	if hasOpenInquiry && req.StopLossFullRemaining && req.AmountMinor != remaining {
		return payment.ErrStripeRefundVerificationPending
	}
	if req.AmountMinor > remaining {
		return payment.ErrStripeRefundInsufficientBalance
	}
	return nil
}

func (c *Client) listAllChargeDisputes(ctx context.Context, chargeID string) ([]*stripego.Dispute, error) {
	pageOpts := PageOptions{Limit: stripePaginationPageSize}
	var all []*stripego.Dispute
	for range disputeLookupPageLimit {
		page, err := c.ListChargeDisputes(ctx, chargeID, pageOpts)
		if err != nil {
			return nil, err
		}
		all = append(all, page.Items...)
		if !page.HasMore {
			return all, nil
		}
		if len(page.Items) == 0 {
			break
		}
		pageOpts.StartingAfter = page.Items[len(page.Items)-1].ID
	}
	return nil, errors.New("Stripe dispute lookup exceeded pagination budget")
}

func (c *Client) findStripeRefundCommand(ctx context.Context, charge, key string) (*payment.StripeRefundProof, error) {
	pageOpts := PageOptions{Limit: stripePaginationPageSize}
	var found *payment.StripeRefundProof
	for range refundLookupPageLimit {
		page, err := c.ListRefunds(ctx, charge, pageOpts)
		if err != nil {
			return nil, err
		}
		for _, item := range page.Items {
			if item.Metadata[refundCommandMetadata] != key {
				continue
			}
			if found != nil {
				return nil, errors.New("Stripe refund command matched more than one refund")
			}
			found, err = stripeRefundProof(item)
			if err != nil {
				return nil, err
			}
		}
		if !page.HasMore {
			return found, nil
		}
		if len(page.Items) == 0 {
			break
		}
		pageOpts.StartingAfter = page.Items[len(page.Items)-1].ID
	}
	return nil, errors.New("Stripe refund lookup exceeded pagination budget")
}
