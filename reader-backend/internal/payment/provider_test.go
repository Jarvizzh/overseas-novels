package payment_test

import (
	"context"
	"testing"

	"reader-backend/internal/payment"
)

type channel struct{ name string }

func (c channel) Provider() string { return c.name }

type webhookChannel struct{ channel }

func (webhookChannel) ParseWebhook(context.Context, []byte, map[string]string) (*payment.WebhookEventResult, error) {
	return &payment.WebhookEventResult{EventID: "provider-event"}, nil
}

func TestCapabilitiesSelectRegisteredChannelWithoutFallback(t *testing.T) {
	adapters := payment.NewAdapters(webhookChannel{channel{payment.ProviderPayPal}}, channel{payment.ProviderStripe})
	provider, err := payment.Capability[payment.WebhookProvider](adapters, payment.ProviderPayPal)
	if err != nil {
		t.Fatal(err)
	}
	event, err := provider.ParseWebhook(t.Context(), nil, nil)
	if err != nil || event.EventID != "provider-event" {
		t.Fatalf("unexpected provider result: %+v, %v", event, err)
	}
	if _, err := payment.Capability[payment.WebhookProvider](adapters, payment.ProviderStripe); err == nil {
		t.Fatal("Stripe fell back to the PayPal webhook adapter")
	}
	if _, err := payment.Capability[payment.Adapter](adapters, "unknown"); err == nil {
		t.Fatal("an unregistered channel was accepted")
	}
}
