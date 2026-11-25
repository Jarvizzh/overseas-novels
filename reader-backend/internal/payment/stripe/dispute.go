package stripe

import (
	"context"
	"errors"

	stripego "github.com/stripe/stripe-go/v87"
)

func (c *Client) GetDispute(ctx context.Context, id string) (*stripego.Dispute, error) {
	if !validID(id, "dp_") && !validID(id, "du_") {
		return nil, errors.New("invalid Stripe Dispute ID")
	}
	return apiResult(c.api.V1Disputes.Retrieve(ctx, id, nil))
}

func (c *Client) ListDisputes(ctx context.Context, created TimeRange, page PageOptions) (Page[*stripego.Dispute], error) {
	params, err := page.params()
	if err != nil {
		return Page[*stripego.Dispute]{}, err
	}
	window, err := created.params()
	if err != nil {
		return Page[*stripego.Dispute]{}, err
	}
	return listPage(c.api.V1Disputes.List(ctx, &stripego.DisputeListParams{ListParams: params, CreatedRange: window}))
}
