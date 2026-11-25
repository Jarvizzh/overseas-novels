package stripe

import (
	"context"
	"errors"

	stripego "github.com/stripe/stripe-go/v87"
)

func (c *Client) CreateRefund(ctx context.Context, chargeID string, amountMinor int64, key string) (*stripego.Refund, error) {
	if !validChargeID(chargeID) {
		return nil, errors.New("invalid Stripe Charge ID")
	}
	if amountMinor <= 0 {
		return nil, errors.New("Stripe refund amount must be positive")
	}
	params, err := writeParams(key)
	if err != nil {
		return nil, err
	}
	return apiResult(c.api.V1Refunds.Create(ctx, &stripego.RefundCreateParams{
		Params: params,
		Charge: stripego.String(chargeID),
		Amount: stripego.Int64(amountMinor),
	}))
}

func (c *Client) GetRefund(ctx context.Context, id string) (*stripego.Refund, error) {
	if !validID(id, "re_") && !validID(id, "pyr_") {
		return nil, errors.New("invalid Stripe Refund ID")
	}
	return apiResult(c.api.V1Refunds.Retrieve(ctx, id, nil))
}

func (c *Client) ListRefunds(ctx context.Context, chargeID string, page PageOptions) (Page[*stripego.Refund], error) {
	if !validChargeID(chargeID) {
		return Page[*stripego.Refund]{}, errors.New("invalid Stripe Charge ID")
	}
	params, err := page.params()
	if err != nil {
		return Page[*stripego.Refund]{}, err
	}
	return listPage(c.api.V1Refunds.List(ctx, &stripego.RefundListParams{ListParams: params, Charge: stripego.String(chargeID)}))
}
