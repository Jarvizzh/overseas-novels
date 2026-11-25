package stripe

import (
	"context"
	"encoding/json"
	"errors"

	stripego "github.com/stripe/stripe-go/v87"
)

func (c *Client) GetSubscription(ctx context.Context, id string) (*stripego.Subscription, error) {
	if !validID(id, "sub_") {
		return nil, errors.New("invalid Stripe Subscription ID")
	}
	return apiResult(c.api.V1Subscriptions.Retrieve(ctx, id, nil))
}

func (c *Client) CancelSubscriptionAtPeriodEnd(ctx context.Context, id, key string) (*stripego.Subscription, error) {
	if !validID(id, "sub_") {
		return nil, errors.New("invalid Stripe Subscription ID")
	}
	params, err := writeParams(key)
	if err != nil {
		return nil, err
	}
	return apiResult(c.api.V1Subscriptions.Update(ctx, id, &stripego.SubscriptionUpdateParams{
		Params:            params,
		CancelAtPeriodEnd: stripego.Bool(true),
	}))
}

func (c *Client) CreateCustomerPortal(ctx context.Context, customerID, returnURL, key string) (*stripego.BillingPortalSession, error) {
	return c.createCustomerPortal(ctx, customerID, returnURL, key, nil)
}

func (c *Client) createCustomerPortal(ctx context.Context, customerID, returnURL, key string, flow *stripego.BillingPortalSessionCreateFlowDataParams) (*stripego.BillingPortalSession, error) {
	if !validID(customerID, "cus_") || !c.validReturnURL(returnURL) {
		return nil, errors.New("Stripe Customer Portal requires a customer and return URL")
	}
	params, err := writeParams(key)
	if err != nil {
		return nil, err
	}
	return apiResult(c.api.V1BillingPortalSessions.Create(ctx, &stripego.BillingPortalSessionCreateParams{
		Params:    params,
		FlowData:  flow,
		Customer:  stripego.String(customerID),
		ReturnURL: stripego.String(returnURL),
	}))
}

func (c *Client) GetInvoice(ctx context.Context, id string) (*stripego.Invoice, error) {
	if !validID(id, "in_") {
		return nil, errors.New("invalid Stripe Invoice ID")
	}
	return apiResult(c.api.V1Invoices.Retrieve(ctx, id, nil))
}

func (c *Client) ListSubscriptionInvoices(ctx context.Context, subscriptionID string, page PageOptions) (Page[*stripego.Invoice], error) {
	if !validID(subscriptionID, "sub_") {
		return Page[*stripego.Invoice]{}, errors.New("invalid Stripe Subscription ID")
	}
	params, err := page.params()
	if err != nil {
		return Page[*stripego.Invoice]{}, err
	}
	return listPage(c.api.V1Invoices.List(ctx, &stripego.InvoiceListParams{
		ListParams: params, Subscription: stripego.String(subscriptionID),
	}))
}

func (c *Client) ListInvoiceLines(ctx context.Context, invoiceID string, page PageOptions) (Page[*stripego.InvoiceLineItem], error) {
	if !validID(invoiceID, "in_") {
		return Page[*stripego.InvoiceLineItem]{}, errors.New("invalid Stripe Invoice ID")
	}
	params, err := page.params()
	if err != nil {
		return Page[*stripego.InvoiceLineItem]{}, err
	}
	return listPage(c.api.V1Invoices.ListLines(ctx, &stripego.InvoiceListLinesParams{
		ListParams: params,
		Invoice:    stripego.String(invoiceID),
	}))
}

// InvoicePayment preserves a null amount_paid, which the SDK decodes as zero.
type InvoicePayment struct {
	*stripego.InvoicePayment
	AmountPaid *int64 `json:"amount_paid"`
}

type invoicePaymentFields struct {
	AmountPaid *int64 `json:"amount_paid"`
}

func decodeInvoicePayment(payment *stripego.InvoicePayment, raw []byte) (*InvoicePayment, error) {
	var fields invoicePaymentFields
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, errors.New("invalid Stripe invoice payment amounts")
	}
	return &InvoicePayment{InvoicePayment: payment, AmountPaid: fields.AmountPaid}, nil
}

func (c *Client) GetInvoicePayment(ctx context.Context, id string) (*InvoicePayment, error) {
	if !validID(id, "inpay_") {
		return nil, errors.New("invalid Stripe InvoicePayment ID")
	}
	payment, err := apiResult(c.api.V1InvoicePayments.Retrieve(ctx, id, nil))
	if err != nil {
		return nil, err
	}
	if payment.ID != id || payment.Invoice == nil || payment.Invoice.ID == "" {
		return nil, errors.New("unexpected Stripe invoice payment")
	}
	return decodeInvoicePayment(payment, payment.LastResponse.RawJSON)
}

// GetPaymentRecord retrieves the payment object that may back an invoice
// allocation. Its existence alone does not prove collection by Stripe.
func (c *Client) GetPaymentRecord(ctx context.Context, id string) (*stripego.PaymentRecord, error) {
	if !validID(id, "pr_") {
		return nil, errors.New("invalid Stripe PaymentRecord ID")
	}
	return apiResult(c.api.V1PaymentRecords.Retrieve(ctx, id, nil))
}

func (c *Client) ListInvoicePayments(ctx context.Context, invoiceID string, page PageOptions) (Page[InvoicePayment], error) {
	if !validID(invoiceID, "in_") {
		return Page[InvoicePayment]{}, errors.New("invalid Stripe Invoice ID")
	}
	params, err := page.params()
	if err != nil {
		return Page[InvoicePayment]{}, err
	}
	list := c.api.V1InvoicePayments.List(ctx, &stripego.InvoicePaymentListParams{
		ListParams: params,
		Invoice:    stripego.String(invoiceID),
	})
	decoded, err := listPage(list)
	if err != nil {
		return Page[InvoicePayment]{}, err
	}
	amounts, err := rawPageFields[*stripego.InvoicePayment, invoicePaymentFields](list)
	if err != nil {
		return Page[InvoicePayment]{}, err
	}
	items := make([]InvoicePayment, len(decoded.Items))
	for i, payment := range decoded.Items {
		if payment == nil || payment.Invoice == nil || payment.Invoice.ID != invoiceID {
			return Page[InvoicePayment]{}, errors.New("unexpected Stripe invoice payment")
		}
		items[i] = InvoicePayment{InvoicePayment: payment, AmountPaid: amounts[i].AmountPaid}
	}
	return Page[InvoicePayment]{Items: items, HasMore: decoded.HasMore}, nil
}
