package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"

	"star-novel-cms/internal/config"
)

const (
	webhookCheckVerified   = "verified"
	webhookCheckUnverified = "unverified"
	webhookCheckFailed     = "failed"

	stripeWebhookStatusEnabled            = "enabled"
	stripeWebhookAPIVersionAccountDefault = "account_default"
	stripeWebhookPageLimit                = 100
	stripeWebhookMaxPages                 = 10
	stripeChannelWebhookPathPrefix        = "/api/v1/wallet/webhook/channel/"
	envStripeWebhookPublicBaseURL         = "STRIPE_WEBHOOK_PUBLIC_BASE_URL"
)

var (
	errStripeWebhookPermissionDenied = errors.New("Stripe API key lacks webhook endpoint read permission")

	requiredStripeWebhookEvents = []string{
		"checkout.session.completed",
		"checkout.session.async_payment_succeeded",
		"checkout.session.async_payment_failed",
		"checkout.session.expired",
		"invoice_payment.paid",
		"invoice.paid",
		"invoice.payment_failed",
		"customer.subscription.created",
		"customer.subscription.updated",
		"customer.subscription.deleted",
		"refund.created",
		"refund.updated",
		"refund.failed",
		"charge.dispute.created",
		"charge.dispute.updated",
		"charge.dispute.closed",
		"review.opened",
		"review.closed",
		"radar.early_fraud_warning.created",
		"charge.succeeded",
		"charge.failed",
		"payment_intent.payment_failed",
		"payout.paid",
		"payout.failed",
		"payout.reconciliation_completed",
		"reporting.report_run.succeeded",
	}
)

type stripeWebhookEndpoint struct {
	ID            string   `json:"id"`
	URL           string   `json:"url"`
	Status        string   `json:"status"`
	Livemode      bool     `json:"livemode"`
	APIVersion    string   `json:"api_version"`
	EnabledEvents []string `json:"enabled_events"`
}

func expectedStripeWebhookURL(webhookToken string) (string, string) {
	if webhookToken == "" {
		return "", ""
	}
	path := stripeChannelWebhookPathPrefix + webhookToken
	base := strings.TrimSpace(os.Getenv(envStripeWebhookPublicBaseURL))
	if base == "" && config.AppConfig != nil {
		base = strings.TrimSpace(config.AppConfig.DefaultDomain)
	}
	if base == "" {
		return "", path
	}
	return strings.TrimRight(base, "/") + path, path
}

func listStripeWebhookEndpoints(ctx context.Context, apiKey string) ([]stripeWebhookEndpoint, error) {
	var all []stripeWebhookEndpoint
	startingAfter := ""
	for range stripeWebhookMaxPages {
		query := url.Values{"limit": {strconv.Itoa(stripeWebhookPageLimit)}}
		if startingAfter != "" {
			query.Set("starting_after", startingAfter)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, stripeAPIBaseURL+"/v1/webhook_endpoints?"+query.Encode(), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+apiKey)
		req.Header.Set("Stripe-Version", stripeCatalogAPIVersion)
		resp, err := channelHTTPClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("Stripe webhook endpoint lookup unavailable: %w", err)
		}
		rawBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		resp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("Stripe webhook endpoint response unreadable: %w", readErr)
		}
		if resp.StatusCode == http.StatusForbidden {
			return nil, errStripeWebhookPermissionDenied
		}
		if resp.StatusCode != http.StatusOK {
			var apiErr struct {
				Error struct {
					Code string `json:"code"`
					Type string `json:"type"`
				} `json:"error"`
			}
			if json.Unmarshal(rawBody, &apiErr) == nil &&
				(apiErr.Error.Code == "rak_insufficient_permissions" || apiErr.Error.Type == "permission_error") {
				return nil, errStripeWebhookPermissionDenied
			}
			return nil, fmt.Errorf("Stripe webhook endpoint lookup returned status %d", resp.StatusCode)
		}
		var page struct {
			Data    []stripeWebhookEndpoint `json:"data"`
			HasMore bool                    `json:"has_more"`
		}
		if err := json.Unmarshal(rawBody, &page); err != nil {
			return nil, errors.New("invalid Stripe webhook endpoint list response")
		}
		all = append(all, page.Data...)
		if !page.HasMore || len(page.Data) == 0 {
			return all, nil
		}
		lastID := page.Data[len(page.Data)-1].ID
		if lastID == "" || lastID == startingAfter {
			return all, nil
		}
		startingAfter = lastID
	}
	return all, nil
}

func matchesStripeWebhookURL(rawEndpointURL, expectedFullURL, expectedPath, environment string) bool {
	parsed, err := url.Parse(strings.TrimSpace(rawEndpointURL))
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	schemeOK := parsed.Scheme == "https" ||
		(environment == "sandbox" && parsed.Scheme == "http" &&
			(parsed.Hostname() == "localhost" || parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "::1"))
	if !schemeOK {
		return false
	}
	if expectedFullURL != "" {
		want, err := url.Parse(expectedFullURL)
		if err != nil || want.Hostname() == "" {
			return false
		}
		return strings.EqualFold(parsed.Scheme, want.Scheme) &&
			strings.EqualFold(parsed.Host, want.Host) &&
			parsed.Path == want.Path
	}
	if expectedPath != "" {
		return parsed.Path == expectedPath
	}
	return true
}

func missingStripeWebhookEvents(enabled []string) []string {
	if slices.Contains(enabled, "*") {
		return nil
	}
	present := make(map[string]struct{}, len(enabled))
	for _, ev := range enabled {
		present[ev] = struct{}{}
	}
	var missing []string
	for _, req := range requiredStripeWebhookEvents {
		if _, ok := present[req]; !ok {
			missing = append(missing, req)
		}
	}
	return missing
}

func verifyStripeWebhookEndpoints(ctx context.Context, environment, apiKey, webhookToken, previousWebhookSecret string, res *channelValidationResult) error {
	res.WebhookRotationDualSecret = previousWebhookSecret != ""
	expectedFullURL, expectedPath := expectedStripeWebhookURL(webhookToken)

	endpoints, err := listStripeWebhookEndpoints(ctx, apiKey)
	if errors.Is(err, errStripeWebhookPermissionDenied) {
		res.WebhookOK = false
		res.WebhookEndpointStatus = webhookCheckUnverified
		res.WebhookURL = expectedFullURL
		res.WebhookEndpointNote = "Stripe API key lacks webhook endpoint read permission; endpoint URL, status, API version, and events are unverified"
		return nil
	}
	if err != nil {
		res.WebhookOK = false
		res.WebhookEndpointStatus = webhookCheckFailed
		return err
	}

	var matched []stripeWebhookEndpoint
	for _, ep := range endpoints {
		if matchesStripeWebhookURL(ep.URL, expectedFullURL, expectedPath, environment) {
			matched = append(matched, ep)
		}
	}
	if len(matched) == 0 {
		res.WebhookOK = false
		res.WebhookEndpointStatus = webhookCheckFailed
		if expectedFullURL != "" {
			return fmt.Errorf("no Stripe webhook endpoint configured for callback URL %s", expectedFullURL)
		}
		return errors.New("no Stripe webhook endpoint configured for channel callback path")
	}

	expectLive := environment == "live"
	var enabled []stripeWebhookEndpoint
	for _, ep := range matched {
		if ep.Status == stripeWebhookStatusEnabled {
			enabled = append(enabled, ep)
		}
	}
	if len(enabled) == 0 {
		res.WebhookOK = false
		res.WebhookEndpointStatus = webhookCheckFailed
		res.WebhookEndpointID = matched[0].ID
		res.WebhookURL = matched[0].URL
		return fmt.Errorf("Stripe webhook endpoint %s is disabled", matched[0].ID)
	}

	var envMatched []stripeWebhookEndpoint
	for _, ep := range enabled {
		if ep.Livemode == expectLive {
			envMatched = append(envMatched, ep)
		}
	}
	if len(envMatched) == 0 {
		res.WebhookOK = false
		res.WebhookEndpointStatus = webhookCheckFailed
		res.WebhookEndpointID = enabled[0].ID
		res.WebhookURL = enabled[0].URL
		return fmt.Errorf("Stripe webhook endpoint %s livemode=%t does not match %s environment", enabled[0].ID, enabled[0].Livemode, environment)
	}

	// Select the best enabled endpoint (preferring exact SDK API version and complete event coverage).
	selected := envMatched[0]
	selectedMissing := missingStripeWebhookEvents(selected.EnabledEvents)
	for _, candidate := range envMatched[1:] {
		candMissing := missingStripeWebhookEvents(candidate.EnabledEvents)
		candExactVer := candidate.APIVersion == stripeCatalogAPIVersion
		selExactVer := selected.APIVersion == stripeCatalogAPIVersion
		if (len(candMissing) < len(selectedMissing)) ||
			(len(candMissing) == len(selectedMissing) && candExactVer && !selExactVer) {
			selected = candidate
			selectedMissing = candMissing
		}
	}

	apiVer := selected.APIVersion
	if apiVer == "" {
		apiVer = stripeWebhookAPIVersionAccountDefault
	}
	verMatches := selected.APIVersion == stripeCatalogAPIVersion

	res.WebhookEndpointID = selected.ID
	res.WebhookURL = selected.URL
	res.WebhookAPIVersion = apiVer
	res.WebhookAPIVersionMatches = &verMatches
	res.WebhookMissingEvents = selectedMissing

	if len(selectedMissing) > 0 {
		res.WebhookOK = false
		res.WebhookEndpointStatus = webhookCheckFailed
		return fmt.Errorf("Stripe webhook endpoint %s is missing required events: %s", selected.ID, strings.Join(selectedMissing, ", "))
	}

	res.WebhookOK = true
	res.WebhookEndpointStatus = webhookCheckVerified
	if len(envMatched) > 1 {
		if previousWebhookSecret != "" {
			res.WebhookEndpointNote = fmt.Sprintf("parallel webhook rotation active across %d endpoints with dual-secret verification and per-account event deduplication", len(envMatched))
		} else {
			res.WebhookEndpointNote = fmt.Sprintf("multiple (%d) enabled Stripe webhook endpoints target this callback URL; keep dual-secret grace period active during rotation", len(envMatched))
		}
	} else if !verMatches {
		res.WebhookEndpointNote = fmt.Sprintf("Stripe webhook endpoint %s uses API version %s (current SDK version is %s); events are handled via cross-version resource re-fetch", selected.ID, apiVer, stripeCatalogAPIVersion)
	}
	return nil
}
