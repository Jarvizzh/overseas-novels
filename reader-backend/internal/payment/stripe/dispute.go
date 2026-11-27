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
	dispute, err := apiResult(c.api.V1Disputes.Retrieve(ctx, id, nil))
	if err != nil {
		return nil, ClassifyPurposeError(c.scope, PurposeAftercare, err)
	}
	return dispute, nil
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
	res, err := listPage(c.api.V1Disputes.List(ctx, &stripego.DisputeListParams{ListParams: params, CreatedRange: window}))
	if err != nil {
		return Page[*stripego.Dispute]{}, ClassifyPurposeError(c.scope, PurposeAftercare, err)
	}
	return res, nil
}
