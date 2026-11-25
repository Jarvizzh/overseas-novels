package stripe

import (
	"context"
	"errors"
	"strings"

	stripego "github.com/stripe/stripe-go/v87"
	"reader-backend/internal/payment"
)

const refundCommandMetadata = "payment_refund_command_id"

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
	charge, err := c.GetCharge(ctx, req.ReceiptID)
	if err != nil {
		return nil, err
	}
	if charge.ID != req.ReceiptID || charge.Currency != stripego.CurrencyUSD || charge.Livemode != (c.scope.Environment == Live) ||
		!charge.Paid || !charge.Captured || charge.AmountCaptured-charge.AmountRefunded < req.AmountMinor || charge.Disputed {
		return nil, payment.ErrStripeRefundRequiresReview
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

func (c *Client) findStripeRefundCommand(ctx context.Context, charge, key string) (*payment.StripeRefundProof, error) {
	pageOpts := PageOptions{Limit: 100}
	var found *payment.StripeRefundProof
	for range 5 {
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
