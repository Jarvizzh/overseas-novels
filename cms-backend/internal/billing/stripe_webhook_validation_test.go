package billing

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestVerifyStripeChannelRemoteWebhookEndpointValidation(t *testing.T) {
	const (
		accountKey   = "acct_StripeWh01"
		apiKey       = "sk_test_webhook_check_1234"
		whSecret     = "whsec_active_1234"
		prevWhSecret = "whsec_previous_5678"
		webhookToken = "11111111-2222-3333-4444-555555555555"
		publicBase   = "https://reader.example.com"
	)
	expectedURL := publicBase + stripeChannelWebhookPathPrefix + webhookToken
	t.Setenv(envStripeWebhookPublicBaseURL, publicBase)

	allRequired := append([]string(nil), requiredStripeWebhookEvents...)

	tests := []struct {
		name               string
		environment        string
		apiKey             string
		prevSecret         string
		endpointsStatus    int
		endpointsBody      any
		wantErr            bool
		wantErrContains    string
		wantWebhookOK      bool
		wantEndpointStatus string
		wantVerMatches     *bool
		wantMissingCount   int
		wantDualSecret     bool
		wantNoteContains   string
	}{
		{
			name:            "valid endpoint matching full URL, enabled, env, API version, and all required events",
			environment:     "sandbox",
			apiKey:          apiKey,
			endpointsStatus: http.StatusOK,
			endpointsBody: map[string]any{
				"object":   "list",
				"has_more": false,
				"data": []map[string]any{{
					"id":             "we_valid_01",
					"url":            expectedURL,
					"status":         "enabled",
					"livemode":       false,
					"api_version":    stripeCatalogAPIVersion,
					"enabled_events": allRequired,
				}},
			},
			wantWebhookOK:      true,
			wantEndpointStatus: webhookCheckVerified,
			wantVerMatches:     boolPtr(true),
		},
		{
			name:            "valid endpoint with wildcard events and older API version notes cross-version refetch",
			environment:     "sandbox",
			apiKey:          apiKey,
			endpointsStatus: http.StatusOK,
			endpointsBody: map[string]any{
				"object":   "list",
				"has_more": false,
				"data": []map[string]any{{
					"id":             "we_old_ver",
					"url":            expectedURL,
					"status":         "enabled",
					"livemode":       false,
					"api_version":    "2025-03-31.basil",
					"enabled_events": []string{"*"},
				}},
			},
			wantWebhookOK:      true,
			wantEndpointStatus: webhookCheckVerified,
			wantVerMatches:     boolPtr(false),
			wantNoteContains:   "cross-version resource re-fetch",
		},
		{
			name:            "API key lacking webhook endpoint read permission marks unverified without silent pass or failure",
			environment:     "sandbox",
			apiKey:          "rk_test_limited_1234",
			endpointsStatus: http.StatusForbidden,
			endpointsBody: map[string]any{
				"error": map[string]any{
					"type": "permission_error",
					"code": "rak_insufficient_permissions",
				},
			},
			wantErr:            false,
			wantWebhookOK:      false,
			wantEndpointStatus: webhookCheckUnverified,
			wantNoteContains:   "lacks webhook endpoint read permission",
		},
		{
			name:            "disabled endpoint fails validation",
			environment:     "sandbox",
			apiKey:          apiKey,
			endpointsStatus: http.StatusOK,
			endpointsBody: map[string]any{
				"object":   "list",
				"has_more": false,
				"data": []map[string]any{{
					"id":             "we_disabled",
					"url":            expectedURL,
					"status":         "disabled",
					"livemode":       false,
					"api_version":    stripeCatalogAPIVersion,
					"enabled_events": []string{"*"},
				}},
			},
			wantErr:            true,
			wantErrContains:    "disabled",
			wantWebhookOK:      false,
			wantEndpointStatus: webhookCheckFailed,
		},
		{
			name:            "environment livemode mismatch fails validation",
			environment:     "sandbox",
			apiKey:          apiKey,
			endpointsStatus: http.StatusOK,
			endpointsBody: map[string]any{
				"object":   "list",
				"has_more": false,
				"data": []map[string]any{{
					"id":             "we_wrong_env",
					"url":            expectedURL,
					"status":         "enabled",
					"livemode":       true,
					"api_version":    stripeCatalogAPIVersion,
					"enabled_events": []string{"*"},
				}},
			},
			wantErr:            true,
			wantErrContains:    "livemode=true does not match sandbox",
			wantWebhookOK:      false,
			wantEndpointStatus: webhookCheckFailed,
		},
		{
			name:            "wrong callback URL fails validation",
			environment:     "sandbox",
			apiKey:          apiKey,
			endpointsStatus: http.StatusOK,
			endpointsBody: map[string]any{
				"object":   "list",
				"has_more": false,
				"data": []map[string]any{{
					"id":             "we_wrong_url",
					"url":            "https://other.example.com/api/v1/wallet/webhook/channel/" + webhookToken,
					"status":         "enabled",
					"livemode":       false,
					"api_version":    stripeCatalogAPIVersion,
					"enabled_events": []string{"*"},
				}},
			},
			wantErr:            true,
			wantErrContains:    "no Stripe webhook endpoint configured",
			wantWebhookOK:      false,
			wantEndpointStatus: webhookCheckFailed,
		},
		{
			name:            "missing required events fails validation and reports missing list",
			environment:     "sandbox",
			apiKey:          apiKey,
			endpointsStatus: http.StatusOK,
			endpointsBody: map[string]any{
				"object":   "list",
				"has_more": false,
				"data": []map[string]any{{
					"id":             "we_incomplete",
					"url":            expectedURL,
					"status":         "enabled",
					"livemode":       false,
					"api_version":    stripeCatalogAPIVersion,
					"enabled_events": []string{"checkout.session.completed"},
				}},
			},
			wantErr:            true,
			wantErrContains:    "missing required events",
			wantWebhookOK:      false,
			wantEndpointStatus: webhookCheckFailed,
			wantMissingCount:   len(requiredStripeWebhookEvents) - 1,
		},
		{
			name:            "dual-secret parallel endpoint rotation selects matching new version and reports deduplication",
			environment:     "sandbox",
			apiKey:          apiKey,
			prevSecret:      prevWhSecret,
			endpointsStatus: http.StatusOK,
			endpointsBody: map[string]any{
				"object":   "list",
				"has_more": false,
				"data": []map[string]any{
					{
						"id":             "we_old_endpoint",
						"url":            expectedURL,
						"status":         "enabled",
						"livemode":       false,
						"api_version":    "2025-03-31.basil",
						"enabled_events": allRequired,
					},
					{
						"id":             "we_new_endpoint",
						"url":            expectedURL,
						"status":         "enabled",
						"livemode":       false,
						"api_version":    stripeCatalogAPIVersion,
						"enabled_events": allRequired,
					},
				},
			},
			wantWebhookOK:      true,
			wantEndpointStatus: webhookCheckVerified,
			wantVerMatches:     boolPtr(true),
			wantDualSecret:     true,
			wantNoteContains:   "parallel webhook rotation active",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch req.URL.Path {
				case "/v1/account":
					_, _ = w.Write([]byte(`{"id":"` + accountKey + `"}`))
				case "/v1/webhook_endpoints":
					w.WriteHeader(tc.endpointsStatus)
					_ = json.NewEncoder(w).Encode(tc.endpointsBody)
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()

			origBase := stripeAPIBaseURL
			stripeAPIBaseURL = server.URL
			defer func() { stripeAPIBaseURL = origBase }()

			res, err := verifyStripeChannelRemote(context.Background(), accountKey, tc.environment, tc.apiKey, whSecret, webhookToken, tc.prevSecret)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil (res=%+v)", tc.wantErrContains, res)
				}
				if tc.wantErrContains != "" && !strings.Contains(err.Error(), tc.wantErrContains) {
					t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErrContains)
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v (res=%+v)", err, res)
			}

			if !res.ProviderOK || !res.IdentityOK || res.AccountIDMatch != accountKey {
				t.Fatalf("expected account identity OK, got %+v", res)
			}
			if res.WebhookOK != tc.wantWebhookOK {
				t.Fatalf("WebhookOK=%v want %v (res=%+v)", res.WebhookOK, tc.wantWebhookOK, res)
			}
			if res.WebhookEndpointStatus != tc.wantEndpointStatus {
				t.Fatalf("WebhookEndpointStatus=%q want %q", res.WebhookEndpointStatus, tc.wantEndpointStatus)
			}
			if tc.wantVerMatches != nil {
				if res.WebhookAPIVersionMatches == nil || *res.WebhookAPIVersionMatches != *tc.wantVerMatches {
					t.Fatalf("WebhookAPIVersionMatches=%v want %v", res.WebhookAPIVersionMatches, *tc.wantVerMatches)
				}
			}
			if tc.wantMissingCount > 0 && len(res.WebhookMissingEvents) != tc.wantMissingCount {
				t.Fatalf("missing events count=%d want %d (%v)", len(res.WebhookMissingEvents), tc.wantMissingCount, res.WebhookMissingEvents)
			}
			if res.WebhookRotationDualSecret != tc.wantDualSecret {
				t.Fatalf("WebhookRotationDualSecret=%v want %v", res.WebhookRotationDualSecret, tc.wantDualSecret)
			}
			if tc.wantNoteContains != "" && !strings.Contains(res.WebhookEndpointNote, tc.wantNoteContains) {
				t.Fatalf("WebhookEndpointNote=%q does not contain %q", res.WebhookEndpointNote, tc.wantNoteContains)
			}
		})
	}
}

func boolPtr(v bool) *bool { return &v }
