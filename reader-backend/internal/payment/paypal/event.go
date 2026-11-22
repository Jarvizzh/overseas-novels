package paypal

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"

	"reader-backend/internal/payment"
)

const eventsPath = "/v1/notifications/webhooks-events"

type Event struct {
	Account         payment.WebhookAccount `json:"-"`
	ID              string                 `json:"id"`
	Type            string                 `json:"event_type"`
	CreatedAt       time.Time              `json:"create_time"`
	Version         string                 `json:"event_version"`
	ResourceVersion string                 `json:"resource_version"`
	ResourceType    string                 `json:"resource_type"`
	Resource        json.RawMessage        `json:"resource"`
	Raw             json.RawMessage        `json:"-"`
}

type EventQuery struct {
	From, Through time.Time
	Type          string
	TransactionID string
	PageSize      int
	Cursor        string
}

type EventPage struct {
	Items []Event
	Next  string
}

func (p *Client) GetEvent(ctx context.Context, id string) (Event, error) {
	if !validObjectID(id) {
		return Event{}, errors.New("invalid PayPal event ID")
	}
	var raw json.RawMessage
	scope, err := p.read(ctx, eventsPath+"/"+url.PathEscape(id), &raw)
	if err != nil {
		return Event{}, err
	}
	event, err := decodeEvent(scope, raw)
	if err != nil {
		return Event{}, err
	}
	if event.ID != id {
		return Event{}, errors.New("unexpected PayPal event ID")
	}
	return event, nil
}

func (p *Client) ListEvents(ctx context.Context, opts EventQuery) (EventPage, error) {
	query, err := pageQuery(opts.Cursor)
	if err != nil {
		return EventPage{}, err
	}
	if opts.Cursor == "" {
		if opts.From.IsZero() || !opts.Through.After(opts.From) || opts.PageSize < 0 || opts.PageSize > 100 {
			return EventPage{}, errors.New("invalid PayPal event query")
		}
		query.Set("start_time", opts.From.UTC().Format(time.RFC3339Nano))
		query.Set("end_time", opts.Through.UTC().Format(time.RFC3339Nano))
		if opts.Type != "" {
			query.Set("event_type", opts.Type)
		}
		if opts.TransactionID != "" {
			if !validObjectID(opts.TransactionID) {
				return EventPage{}, errors.New("invalid PayPal event transaction ID")
			}
			query.Set("transaction_id", opts.TransactionID)
		}
		if opts.PageSize > 0 {
			query.Set("page_size", strconv.Itoa(opts.PageSize))
		}
	}
	var response struct {
		Events []json.RawMessage `json:"events"`
		Links  []Link            `json:"links"`
	}
	scope, err := p.read(ctx, endpoint(eventsPath, query), &response)
	if err != nil {
		return EventPage{}, err
	}
	cursor, err := nextPage(response.Links, apiBaseURL(scope.Environment), eventsPath)
	if err != nil {
		return EventPage{}, err
	}
	page := EventPage{Next: cursor}
	for _, raw := range response.Events {
		event, err := decodeEvent(scope, raw)
		if err != nil {
			var identity struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(raw, &identity) != nil || !validObjectID(identity.ID) {
				return EventPage{}, err
			}
			event = Event{Account: scope, ID: identity.ID, Raw: raw}
		}
		page.Items = append(page.Items, event)
	}
	return page, nil
}

func decodeEvent(account payment.WebhookAccount, raw json.RawMessage) (Event, error) {
	event := Event{Account: account, Raw: raw}
	if err := json.Unmarshal(raw, &event); err != nil {
		return Event{}, err
	}
	if err := event.validateSnapshot(); err != nil {
		return Event{}, err
	}
	return event, nil
}

func (e Event) validateSnapshot() error {
	if !validObjectID(e.ID) || e.Type == "" || len(e.Resource) == 0 || string(e.Resource) == "null" {
		return errors.New("incomplete PayPal event")
	}
	return nil
}

// Evidence extracts the channel facts needed by the current payment workflow.
func (e Event) Evidence() (*payment.WebhookEventResult, error) {
	if err := e.validateSnapshot(); err != nil {
		return nil, err
	}
	result, err := parsePayPalWebhook(e)
	if err != nil {
		return nil, err
	}
	result.Account = e.Account
	result.ProviderEventType = e.Type
	result.RawPayload = string(e.Raw)
	return result, nil
}

// Reversal is a notification about a money movement. Reporting supplies the
// authoritative funds evidence and any association absent from the payload.
type Reversal struct {
	ResourceID, OriginalReceiptID, SubscriptionID string
	Status                                        string
	Amount                                        *Amount
}

func (e Event) Reversal() (Reversal, error) {
	var resource struct {
		ID          string `json:"id"`
		Status      string `json:"status"`
		State       string `json:"state"`
		CaptureID   string `json:"capture_id"`
		SaleID      string `json:"sale_id"`
		AgreementID string `json:"billing_agreement_id"`
		Amount      *struct {
			CurrencyCode string `json:"currency_code"`
			Value        string `json:"value"`
			Currency     string `json:"currency"`
			Total        string `json:"total"`
		} `json:"amount"`
		SupplementaryData struct {
			RelatedIDs struct {
				CaptureID string `json:"capture_id"`
			} `json:"related_ids"`
		} `json:"supplementary_data"`
		Links []Link `json:"links"`
	}
	if err := json.Unmarshal(e.Resource, &resource); err != nil || resource.ID == "" {
		return Reversal{}, errors.New("invalid PayPal reversal response")
	}
	result := Reversal{ResourceID: resource.ID, SubscriptionID: resource.AgreementID}
	var prefix string
	switch e.Type {
	case "PAYMENT.CAPTURE.REVERSED":
		result.OriginalReceiptID, result.Status = resource.CaptureID, resource.Status
		if result.OriginalReceiptID == "" {
			result.OriginalReceiptID = resource.SupplementaryData.RelatedIDs.CaptureID
		}
		if resource.Amount != nil {
			result.Amount = &Amount{Currency: resource.Amount.CurrencyCode, Value: resource.Amount.Value}
		}
		prefix = "/v2/payments/captures/"
	case "PAYMENT.SALE.REVERSED":
		result.OriginalReceiptID, result.Status = resource.SaleID, resource.State
		if resource.Amount != nil {
			result.Amount = &Amount{Currency: resource.Amount.Currency, Value: resource.Amount.Total}
		}
		prefix = "/v1/payments/sale/"
	default:
		return Reversal{}, errors.New("PayPal event is not a reversal")
	}
	if result.OriginalReceiptID == "" {
		for _, link := range resource.Links {
			parsed, err := url.Parse(link.Href)
			if err != nil {
				continue
			}
			id, matches := strings.CutPrefix(parsed.Path, prefix)
			if matches && validObjectID(id) && (link.Rel == "up" || link.Rel == "self") {
				result.OriginalReceiptID = id
				break
			}
		}
	}
	return result, nil
}
