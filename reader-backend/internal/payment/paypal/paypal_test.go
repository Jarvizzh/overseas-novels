package paypal

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reader-backend/internal/payment"
	"testing"
)

func TestParseCentsUsesExactDecimalAmounts(t *testing.T) {
	for _, tc := range []struct {
		amount string
		want   int64
	}{
		{"9.99", 999}, {"4.10", 410}, {"4.1", 410}, {"4", 400}, {"0.01", 1},
	} {
		t.Run(tc.amount, func(t *testing.T) {
			got, err := payment.ParseCents(tc.amount)
			if err != nil || got != tc.want {
				t.Fatalf("got %d, %v; want %d", got, err, tc.want)
			}
		})
	}
	for _, invalid := range []string{"", "0", "-1.00", "1.001", "1e3", "NaN", "9999999999999999999999"} {
		if _, err := payment.ParseCents(invalid); err == nil {
			t.Errorf("accepted invalid amount %q", invalid)
		}
	}
}

func TestPayPalWebhookCarriesAccountUsedForVerification(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/oauth2/token":
			id, secret, ok := r.BasicAuth()
			if !ok || id != "verified-app" || secret != "fixture-secret" {
				http.Error(w, "invalid application", http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"verification-token"}`))
		case "/v1/notifications/verify-webhook-signature":
			var request struct {
				WebhookID string `json:"webhook_id"`
				Signature string `json:"transmission_sig"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil ||
				request.WebhookID != "configured-webhook" || r.Header.Get("Authorization") != "Bearer verification-token" {
				http.Error(w, "invalid verification request", http.StatusBadRequest)
				return
			}
			status := "FAILURE"
			if request.Signature == "valid" {
				status = "SUCCESS"
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"verification_status": status})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClient(func(context.Context) (Config, error) {
		return Config{ClientID: "verified-app", Secret: "fixture-secret", Environment: "live", WebhookID: "configured-webhook"}, nil
	})
	client.httpClient.Transport = rewriteHostTransport{server.URL}

	headers := map[string]string{
		"PayPal-Auth-Algo": "sha256", "PayPal-Cert-Url": "https://example.invalid/cert",
		"PayPal-Transmission-Id": "test", "PayPal-Transmission-Sig": "valid", "PayPal-Transmission-Time": "test",
	}
	payload := []byte(`{"id":"WH-TEST","event_type":"CHECKOUT.ORDER.APPROVED","resource":{"id":"P-ORDER"}}`)
	event, err := client.ParseWebhook(t.Context(), payload, headers)
	if err != nil || event == nil || event.Account != (payment.WebhookAccount{AccountKey: "verified-app", Environment: "live"}) ||
		event.EventID != "WH-TEST" || event.ExternalRefID != "P-ORDER" {
		t.Fatalf("unexpected verified event %+v, err=%v", event, err)
	}
	headers["PayPal-Transmission-Sig"] = "invalid"
	if event, err := client.ParseWebhook(t.Context(), payload, headers); err == nil || event != nil {
		t.Fatalf("rejected signature produced an event: %+v, err=%v", event, err)
	}
}

type rewriteHostTransport struct{ address string }

func (tr rewriteHostTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	dest, err := url.Parse(tr.address)
	if err != nil {
		return nil, err
	}
	request := req.Clone(req.Context())
	request.URL.Scheme, request.URL.Host = dest.Scheme, dest.Host
	return http.DefaultTransport.RoundTrip(request)
}
