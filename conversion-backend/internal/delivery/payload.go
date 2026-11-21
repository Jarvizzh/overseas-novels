package delivery

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"
)

var errInvalidWebsiteContext = errors.New("invalid_website_context")

// ValidatePayload is shared by the media transport and audited queue writers.
func ValidatePayload(p Payload) error {
	var matches map[string]json.RawMessage
	if err := json.Unmarshal(p.MatchingData, &matches); err != nil {
		return errors.New("missing_matching_data")
	}
	hasMatch := false
	for key, value := range matches {
		if key != "client_user_agent" && len(value) != 0 && string(value) != "null" && string(value) != "\"\"" && string(value) != "[]" {
			hasMatch = true
			break
		}
	}
	if !hasMatch {
		return errors.New("missing_matching_data")
	}
	if p.OccurredAt == nil || p.ActionSource == nil {
		return errors.New("missing_event_time_or_source")
	}
	switch *p.ActionSource {
	case "website":
		if p.SourceURL == nil {
			return errInvalidWebsiteContext
		}
		page, err := url.Parse(*p.SourceURL)
		var userAgent string
		userAgentErr := json.Unmarshal(matches["client_user_agent"], &userAgent)
		if err != nil || page.Scheme != "https" || page.Hostname() == "" || page.User != nil ||
			page.RawQuery != "" || page.Fragment != "" || userAgentErr != nil || strings.TrimSpace(userAgent) == "" {
			return errInvalidWebsiteContext
		}
	case "system_generated":
	default:
		return errors.New("unsupported_action_source")
	}
	if p.EventName == "Purchase" && (p.Amount == nil || p.Currency == nil) {
		return errors.New("missing_purchase_amount")
	}
	if p.Amount != nil && p.Currency != nil && (!json.Valid([]byte(*p.Amount)) || !currencyCode(*p.Currency)) {
		return errors.New("invalid_event_amount")
	}
	return nil
}
