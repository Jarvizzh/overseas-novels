package stripe

import (
	"context"
	"errors"
	"strconv"
	"strings"

	stripego "github.com/stripe/stripe-go/v87"
)

const stripeCustomerUserIDMetadata = "reader_user_id"

// CreateCustomer creates a Stripe Customer idempotently for the scoped account
// and environment. Callers must pass a deterministic idempotency key per
// (user_id, account_id, environment) so concurrent checkouts never create
// duplicate Stripe Customers.
func (c *Client) CreateCustomer(ctx context.Context, userID int64, email, idempotencyKey string) (string, error) {
	if userID <= 0 {
		return "", errors.New("Stripe customer requires a valid user ID")
	}
	params, err := writeParams(idempotencyKey)
	if err != nil {
		return "", err
	}
	req := &stripego.CustomerCreateParams{
		Params: params,
		Metadata: map[string]string{
			stripeCustomerUserIDMetadata: strconv.FormatInt(userID, 10),
		},
	}
	if trimmed := strings.TrimSpace(email); trimmed != "" {
		req.Email = stripego.String(trimmed)
	}
	customer, err := apiResult(c.api.V1Customers.Create(ctx, req))
	if err != nil {
		return "", err
	}
	if !validID(customer.ID, "cus_") || customer.Livemode != (c.scope.Environment == Live) {
		return "", errors.New("Stripe returned an invalid Customer for this scope")
	}
	return customer.ID, nil
}
