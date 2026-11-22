package stripe

import (
	"context"
	"encoding/json"
	"errors"

	stripego "github.com/stripe/stripe-go/v87"
)

var (
	ErrEventVersion         = errors.New("unsupported Stripe event API version")
	ErrUnsupportedEventData = errors.New("unsupported Stripe event object")
	errIncompleteSnapshot   = errors.New("incomplete Stripe event snapshot")
)

// GetEvent retrieves an account event within Stripe's retention period. An old
// snapshot keeps its original API version even when queried via the current API.
func (c *Client) GetEvent(ctx context.Context, id string) (Event, error) {
	if !validID(id, "evt_") {
		return Event{}, errors.New("invalid Stripe Event ID")
	}
	result, err := apiResult(c.api.V1Events.Retrieve(ctx, id, nil))
	if err != nil {
		return Event{}, err
	}
	if result.ID != id {
		return Event{}, errors.New("unexpected Stripe event ID")
	}
	return c.eventSnapshot(result, EventFromAPI)
}

type EventQuery struct {
	Created         TimeRange
	Types           []string
	DeliverySuccess *bool
	PageOptions
}

func (c *Client) ListEvents(ctx context.Context, query EventQuery) (Page[Event], error) {
	params, err := query.PageOptions.params()
	if err != nil {
		return Page[Event]{}, err
	}
	window, err := query.Created.params()
	if err != nil {
		return Page[Event]{}, err
	}
	if len(query.Types) > 20 {
		return Page[Event]{}, errors.New("Stripe event query supports at most 20 types")
	}
	list, err := listPage(c.api.V1Events.List(ctx, &stripego.EventListParams{
		ListParams: params, CreatedRange: window,
		Types: stripego.StringSlice(query.Types), DeliverySuccess: query.DeliverySuccess,
	}))
	if err != nil {
		return Page[Event]{}, err
	}
	items := make([]Event, 0, len(list.Items))
	for _, result := range list.Items {
		event, err := c.eventSnapshot(result, EventFromAPI)
		if err != nil && !errors.Is(err, errIncompleteSnapshot) {
			return Page[Event]{}, err
		}
		items = append(items, event)
	}
	return Page[Event]{Items: items, HasMore: list.HasMore}, nil
}

// DecodeObject returns the typed Stripe snapshot for the payment resources this
// adapter supports. For historical versions, use ObjectID to query current
// evidence from the corresponding resource API.
func (e Event) DecodeObject() (any, error) {
	if e.IncompleteSnapshot {
		return nil, errIncompleteSnapshot
	}
	if e.APIVersion != APIVersion {
		return nil, ErrEventVersion
	}
	switch e.ObjectType {
	case "checkout.session":
		return decodeObject[stripego.CheckoutSession](e)
	case "payment_intent":
		return decodeObject[stripego.PaymentIntent](e)
	case "charge":
		return decodeObject[stripego.Charge](e)
	case "subscription":
		return decodeObject[stripego.Subscription](e)
	case "invoice":
		return decodeObject[stripego.Invoice](e)
	case "invoice_payment":
		payment, err := decodeObject[stripego.InvoicePayment](e)
		if err != nil {
			return nil, err
		}
		return decodeInvoicePayment(payment, e.Object)
	case "payment_record":
		return decodeObject[stripego.PaymentRecord](e)
	case "refund":
		return decodeObject[stripego.Refund](e)
	case "review":
		return decodeObject[stripego.Review](e)
	case "radar.early_fraud_warning":
		return decodeObject[stripego.RadarEarlyFraudWarning](e)
	case "dispute":
		return decodeObject[stripego.Dispute](e)
	case "balance":
		return decodeObject[stripego.Balance](e)
	case "payout":
		return decodeObject[stripego.Payout](e)
	case "reporting.report_run":
		return decodeObject[stripego.ReportingReportRun](e)
	case "reporting.report_type":
		return decodeObject[stripego.ReportingReportType](e)
	default:
		return nil, ErrUnsupportedEventData
	}
}

func decodeObject[T any](e Event) (*T, error) {
	var object *T
	if err := json.Unmarshal(e.Object, &object); err != nil || object == nil ||
		(e.ObjectID == "" && e.ObjectType != "balance") {
		return nil, errors.New("invalid Stripe event snapshot")
	}
	return object, nil
}
