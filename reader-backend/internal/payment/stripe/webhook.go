package stripe

import (
	"encoding/json"
	"errors"

	stripego "github.com/stripe/stripe-go/v87"
	"github.com/stripe/stripe-go/v87/webhook"
)

type EventSource string

const (
	EventFromWebhook EventSource = "webhook"
	EventFromAPI     EventSource = "api"
)

// Event holds the original snapshot plus the metadata used to recover and
// correlate a Stripe operation.
type Event struct {
	Scope          Scope
	Source         EventSource
	ID             string
	Type           stripego.EventType
	APIVersion     string
	Created        int64
	RequestID      string
	IdempotencyKey string
	// Some Stripe objects (such as balance) have no ID.
	ObjectID           string
	ObjectType         string
	Object             json.RawMessage
	IncompleteSnapshot bool `json:",omitempty"`
}

type VerifiedEvent = Event

func (c *Client) VerifyWebhook(body []byte, signature string) (VerifiedEvent, error) {
	if c.webhookSecret == "" && c.previousWebhookSecret == "" {
		return VerifiedEvent{}, errors.New("Stripe webhook signing secret is unconfigured")
	}
	var event stripego.Event
	var err error
	if c.webhookSecret != "" {
		event, err = webhook.ConstructEvent(body, signature, c.webhookSecret)
	}
	if (err != nil || c.webhookSecret == "") && c.previousWebhookSecret != "" && c.previousWebhookSecret != c.webhookSecret {
		event, err = webhook.ConstructEvent(body, signature, c.previousWebhookSecret)
	}
	if err != nil {
		return VerifiedEvent{}, errors.New("invalid Stripe webhook signature or payload")
	}
	// The SDK permits compatible versions in the same release family.
	if event.APIVersion != APIVersion {
		return VerifiedEvent{}, ErrEventVersion
	}
	return c.eventSnapshot(&event, EventFromWebhook)
}

func (c *Client) eventSnapshot(event *stripego.Event, source EventSource) (Event, error) {
	if event == nil || !validID(event.ID, "evt_") {
		return Event{}, errors.New("invalid Stripe event ID")
	}
	if event.Livemode != (c.scope.Environment == Live) ||
		(event.Account != "" && event.Account != c.scope.AccountID) ||
		(event.Context != "" && event.Context != c.scope.AccountID) {
		return Event{}, errors.New("unexpected Stripe event account or environment")
	}
	snapshot := Event{
		Scope: c.scope, Source: source, ID: event.ID, Type: event.Type,
		APIVersion: event.APIVersion, Created: event.Created,
	}
	if event.Request != nil {
		snapshot.RequestID = event.Request.ID
		snapshot.IdempotencyKey = event.Request.IdempotencyKey
	}
	if event.Data != nil {
		snapshot.Object = event.Data.Raw
	}
	var object *struct {
		ID   string `json:"id"`
		Type string `json:"object"`
	}
	if event.Type == "" || event.Created <= 0 || json.Unmarshal(snapshot.Object, &object) != nil || object == nil {
		snapshot.IncompleteSnapshot = true
		return snapshot, errIncompleteSnapshot
	}
	snapshot.ObjectID, snapshot.ObjectType = object.ID, object.Type
	return snapshot, nil
}
