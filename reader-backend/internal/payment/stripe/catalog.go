package stripe

import (
	"context"
	"errors"

	stripego "github.com/stripe/stripe-go/v87"
)

func (c *Client) GetProduct(ctx context.Context, id string) (*stripego.Product, error) {
	if !validToken(id) {
		return nil, errors.New("invalid Stripe Product ID")
	}
	return apiResult(c.api.V1Products.Retrieve(ctx, id, nil))
}

func (c *Client) GetPrice(ctx context.Context, id string) (*stripego.Price, error) {
	if !validID(id, "price_") {
		return nil, errors.New("invalid Stripe Price ID")
	}
	request := &stripego.PriceRetrieveParams{}
	request.AddExpand("currency_options")
	return apiResult(c.api.V1Prices.Retrieve(ctx, id, request))
}
