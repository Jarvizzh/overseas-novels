package stripe

import (
	"context"
	"errors"
	"net/url"
	"strings"

	stripego "github.com/stripe/stripe-go/v87"
)

type SalesMode string

const (
	Direct SalesMode = "direct"
	MoR    SalesMode = "mor"
)

type CheckoutMode string

const (
	OneTime      CheckoutMode = "payment"
	Subscription CheckoutMode = "subscription"
)

type CheckoutRequest struct {
	SalesMode      SalesMode
	CheckoutMode   CheckoutMode
	PriceID        string
	AttemptID      string
	SuccessURL     string
	CancelURL      string
	IdempotencyKey string

	// Controls Stripe Tax for direct sales. Managed Payments controls its own tax.
	AutomaticTax           bool
	BillingAddressRequired bool
	USDOnly                bool
}

func (c *Client) CreateCheckout(ctx context.Context, req CheckoutRequest) (*stripego.CheckoutSession, error) {
	params, err := writeParams(req.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	if req.CheckoutMode != OneTime && req.CheckoutMode != Subscription {
		return nil, errors.New("invalid Stripe checkout mode")
	}
	if !validID(req.PriceID, "price_") || strings.TrimSpace(req.AttemptID) == "" || len(req.AttemptID) > 200 {
		return nil, errors.New("Stripe checkout requires a published Price and an attempt reference")
	}
	if !c.validReturnURL(req.SuccessURL) || !c.validReturnURL(req.CancelURL) {
		return nil, errors.New("invalid Stripe checkout return URL")
	}
	request := &stripego.CheckoutSessionCreateParams{
		Params:            params,
		Mode:              stripego.String(string(req.CheckoutMode)),
		UIMode:            stripego.String(string(stripego.CheckoutSessionUIModeHostedPage)),
		ClientReferenceID: stripego.String(req.AttemptID),
		SuccessURL:        stripego.String(req.SuccessURL),
		CancelURL:         stripego.String(req.CancelURL),
		LineItems: []*stripego.CheckoutSessionCreateLineItemParams{
			{Price: stripego.String(req.PriceID), Quantity: stripego.Int64(1)},
		},
	}
	if req.BillingAddressRequired {
		request.BillingAddressCollection = stripego.String("required")
	}
	switch req.SalesMode {
	case Direct:
		if req.USDOnly {
			request.AdaptivePricing = &stripego.CheckoutSessionCreateAdaptivePricingParams{Enabled: stripego.Bool(false)}
		}
		request.AutomaticTax = &stripego.CheckoutSessionCreateAutomaticTaxParams{Enabled: stripego.Bool(req.AutomaticTax)}
	case MoR:
		if req.AutomaticTax {
			return nil, errors.New("Stripe Managed Payments controls automatic tax")
		}
		request.ManagedPayments = &stripego.CheckoutSessionCreateManagedPaymentsParams{Enabled: stripego.Bool(true)}
	default:
		return nil, errors.New("invalid Stripe sales mode")
	}

	// Propagate the durable reference so charge and billing events can be tied
	// back to the attempt even if Session creation returned an ambiguous result.
	metadata := map[string]string{singleAttemptMetadata: req.AttemptID}
	request.Metadata = metadata
	if req.CheckoutMode == OneTime {
		request.PaymentIntentData = &stripego.CheckoutSessionCreatePaymentIntentDataParams{Metadata: metadata}
	} else {
		request.SubscriptionData = &stripego.CheckoutSessionCreateSubscriptionDataParams{Metadata: metadata}
	}
	return apiResult(c.api.V1CheckoutSessions.Create(ctx, request))
}

func (c *Client) GetCheckout(ctx context.Context, sessionID string) (*stripego.CheckoutSession, error) {
	if !validID(sessionID, "cs_") {
		return nil, errors.New("invalid Stripe Checkout Session ID")
	}
	return apiResult(c.api.V1CheckoutSessions.Retrieve(ctx, sessionID, nil))
}

func (c *Client) ListCheckoutLines(ctx context.Context, sessionID string, page PageOptions) (Page[*stripego.LineItem], error) {
	if !validID(sessionID, "cs_") {
		return Page[*stripego.LineItem]{}, errors.New("invalid Stripe Checkout Session ID")
	}
	params, err := page.params()
	if err != nil {
		return Page[*stripego.LineItem]{}, err
	}
	return listPage(c.api.V1CheckoutSessions.ListLineItems(ctx, &stripego.CheckoutSessionListLineItemsParams{
		ListParams: params, Session: stripego.String(sessionID),
	}))
}

func (c *Client) ExpireCheckout(ctx context.Context, sessionID, key string) (*stripego.CheckoutSession, error) {
	if !validID(sessionID, "cs_") {
		return nil, errors.New("invalid Stripe Checkout Session ID")
	}
	params, err := writeParams(key)
	if err != nil {
		return nil, err
	}
	return apiResult(c.api.V1CheckoutSessions.Expire(ctx, sessionID, &stripego.CheckoutSessionExpireParams{Params: params}))
}

// ListCheckouts supports recovery when create returned no Session ID. The
// caller must match the exact ClientReferenceID inside the attempt's time window.
func (c *Client) ListCheckouts(ctx context.Context, created TimeRange, page PageOptions) (Page[*stripego.CheckoutSession], error) {
	params, err := page.params()
	if err != nil {
		return Page[*stripego.CheckoutSession]{}, err
	}
	window, err := created.params()
	if err != nil {
		return Page[*stripego.CheckoutSession]{}, err
	}
	return listPage(c.api.V1CheckoutSessions.List(ctx, &stripego.CheckoutSessionListParams{
		ListParams:   params,
		CreatedRange: window,
	}))
}

func (c *Client) GetPaymentIntent(ctx context.Context, id string) (*stripego.PaymentIntent, error) {
	if !validID(id, "pi_") {
		return nil, errors.New("invalid Stripe PaymentIntent ID")
	}
	return apiResult(c.api.V1PaymentIntents.Retrieve(ctx, id, nil))
}

func (c *Client) GetCharge(ctx context.Context, id string) (*stripego.Charge, error) {
	if !validChargeID(id) {
		return nil, errors.New("invalid Stripe Charge ID")
	}
	return apiResult(c.api.V1Charges.Retrieve(ctx, id, nil))
}

func (c *Client) validReturnURL(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil {
		return false
	}
	if parsed.Scheme == "https" {
		return true
	}
	return c.scope.Environment == Sandbox && parsed.Scheme == "http" &&
		(parsed.Hostname() == "localhost" || parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "::1")
}
