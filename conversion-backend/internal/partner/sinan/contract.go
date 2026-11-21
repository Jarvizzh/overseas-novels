package sinan

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"conversion-backend/internal/partner"
)

const MockContract = "sinan-mock-v1"

type Config struct {
	AppPixels        map[string][]string          `json:"app_pixels"`
	PromotionPixels  map[string]map[string]string `json:"promotion_pixels,omitempty"`
	SourceHosts      []string                     `json:"source_hosts"`
	Endpoint         string                       `json:"endpoint"`
	NoTrace          string                       `json:"no_trace_encoding"`
	MissingPromotion string                       `json:"missing_promotion_encoding,omitempty"`
	Replay           bool                         `json:"replay_supported"`
	MaxAge           int                          `json:"max_age_seconds"`
	AlertAfter       int                          `json:"alert_after_seconds,omitempty"`
}

func (c Config) allows(app, pixel string) bool {
	return slices.Contains(c.AppPixels[app], pixel)
}

func (c Config) Validate() bool {
	if len(c.AppPixels) == 0 || len(c.AppPixels) > 100 || len(c.SourceHosts) > 100 ||
		c.MaxAge < 1 || c.MaxAge > 30*24*60*60 || c.AlertAfter < 0 || c.AlertAfter > 999999 ||
		(c.NoTrace != "" && c.NoTrace != "omit" && c.NoTrace != "empty") ||
		(c.MissingPromotion != "" && c.MissingPromotion != "null") {
		return false
	}
	if _, err := mockEndpoint(c.Endpoint); err != nil {
		return false
	}
	for app, pixels := range c.AppPixels {
		if !validIntegerID(app) || len(pixels) == 0 || len(pixels) > 100 {
			return false
		}
		for _, pixel := range pixels {
			if !decimalID.MatchString(pixel) {
				return false
			}
		}
	}
	for app, promotions := range c.PromotionPixels {
		if len(c.AppPixels[app]) == 0 {
			return false
		}
		for promotion, pixel := range promotions {
			if !validIntegerID(promotion) || !c.allows(app, pixel) {
				return false
			}
		}
	}
	for _, host := range c.SourceHosts {
		if !hostPattern.MatchString(host) {
			return false
		}
	}
	return true
}

func ValidMockCredentials(inboundToken, signingSeed string) bool {
	if inboundToken != "" && !tokenPattern.MatchString(inboundToken) {
		return false
	}
	if signingSeed != "" {
		_, err := mockPrivateKey(signingSeed)
		return err == nil
	}
	return true
}

type identifier string

var tokenPattern = regexp.MustCompile(`^[A-Za-z0-9_~.+/=-]{24,256}$`)
var hostPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]{0,251}[a-z0-9])?$`)
var decimalID = regexp.MustCompile(`^[1-9][0-9]{0,39}$`)
var sha256Hex = regexp.MustCompile(`^[a-fA-F0-9]{64}$`)
var money = regexp.MustCompile(`^(0|[1-9][0-9]{0,15})(\.[0-9]{1,8})?$`)
var currencyCode = regexp.MustCompile(`^[A-Za-z]{3}$`)
var browserCookie = regexp.MustCompile(`^fb\.[0-9]+\.[0-9]+\.[A-Za-z0-9_-]+$`)

func (v *identifier) UnmarshalJSON(raw []byte) error {
	if bytes.Equal(raw, []byte("null")) {
		return nil
	}
	value := string(raw)
	if len(raw) > 0 && raw[0] == '"' {
		if err := json.Unmarshal(raw, &value); err != nil {
			return err
		}
	}
	if !decimalID.MatchString(value) {
		return errors.New("invalid numeric identifier")
	}
	*v = identifier(value)
	return nil
}

// integerID keeps the canonical Inbox representation while enforcing the
// partner's signed 64-bit range.
type integerID string

func validIntegerID(s string) bool {
	_, err := strconv.ParseInt(s, 10, 64)
	return err == nil && decimalID.MatchString(s)
}

func (v *integerID) UnmarshalJSON(raw []byte) error {
	var id identifier
	if err := json.Unmarshal(raw, &id); err != nil {
		return err
	}
	if id != "" && !validIntegerID(string(id)) {
		return errors.New("identifier exceeds int64")
	}
	*v = integerID(id)
	return nil
}

func (v integerID) number() int64 {
	n, _ := strconv.ParseInt(string(v), 10, 64)
	return n
}

type opaqueID string

var opaqueIDPattern = regexp.MustCompile(`^[^\p{C}]{1,128}$`)

func (v *opaqueID) UnmarshalJSON(raw []byte) error {
	if bytes.Equal(raw, []byte("null")) {
		return nil
	}
	var id string
	if len(raw) > 0 && raw[0] == '"' {
		if err := json.Unmarshal(raw, &id); err != nil {
			return err
		}
	} else if decimalID.Match(raw) {
		// Existing mock callers may still send these opaque IDs as integers.
		id = string(raw)
	} else {
		return errors.New("expected opaque identifier")
	}
	if id != "" && (!opaqueIDPattern.MatchString(id) || strings.TrimSpace(id) != id) {
		return errors.New("invalid opaque identifier")
	}
	*v = opaqueID(id)
	return nil
}

type sourceFields struct {
	AppID         integerID  `json:"app_id"`
	PixelID       identifier `json:"pixel_id"`
	DistributorID integerID  `json:"distributor_id,omitempty"`
	PromotionID   integerID  `json:"promotion_id,omitempty"`
	AdID          opaqueID   `json:"ad_id,omitempty"`
	AdSetID       opaqueID   `json:"adset_id,omitempty"`
	OrderID       opaqueID   `json:"order_id,omitempty"`
	DeviceID      opaqueID   `json:"device_id,omitempty"`
}

type callback struct {
	sourceFields
	EventID      string      `json:"event_id,omitempty"`
	EventTime    int64       `json:"event_time,omitempty"`
	RechargeType int         `json:"recharge_type,omitempty"`
	Amount       json.Number `json:"amount,omitempty"`
	Currency     string      `json:"currency,omitempty"`
	ExternalHash string      `json:"external_id_hash,omitempty"`
	EmailHash    string      `json:"email_hash,omitempty"`
	CountryHash  string      `json:"country_hash,omitempty"`
	FBC          string      `json:"fbc,omitempty"`
	FBP          string      `json:"fbp,omitempty"`
	ClientIP     string      `json:"client_ip_address,omitempty"`
	UserAgent    string      `json:"client_user_agent,omitempty"`
	SourceURL    string      `json:"event_source_url,omitempty"`
}

func parseCallback(body []byte) (callback, callback, error) {
	if !utf8.Valid(body) {
		return callback{}, callback{}, errors.New("invalid JSON encoding")
	}
	// Retain the historic snapshot keys to consume already-persisted rows and
	// compare semantic digests across retries made before this upgrade.
	var input struct {
		callback
		PartnerIP *string `json:"client_ip"`
		PartnerUA *string `json:"user_agent"`
	}
	if err := decodeObject(body, &input); err != nil {
		return callback{}, callback{}, err
	}
	data := input.callback
	// Version 1 encoded only the legacy network keys. Keep the business
	// fields and their JSON representation identical across the two versions.
	data.canonicalize()
	legacy := data
	if input.PartnerIP != nil {
		if input.ClientIP != "" && input.ClientIP != *input.PartnerIP {
			return callback{}, callback{}, errors.New("conflicting client IP fields")
		}
		data.ClientIP = *input.PartnerIP
		if ip := net.ParseIP(data.ClientIP); ip != nil {
			data.ClientIP = ip.String()
		}
	}
	if input.PartnerUA != nil {
		if data.UserAgent != "" && data.UserAgent != *input.PartnerUA {
			return callback{}, callback{}, errors.New("conflicting user agent fields")
		}
		data.UserAgent = *input.PartnerUA
	}
	return data, legacy, nil
}

func (data *callback) canonicalize() {
	data.ExternalHash = strings.ToLower(data.ExternalHash)
	data.EmailHash = strings.ToLower(data.EmailHash)
	data.CountryHash = strings.ToLower(data.CountryHash)
	data.Currency = strings.ToUpper(data.Currency)
	data.Amount = canonicalAmount(data.Amount)
	if ip := net.ParseIP(data.ClientIP); ip != nil {
		data.ClientIP = ip.String()
	}
}

func decodeObject(body []byte, output any) error {
	// Reject ambiguous duplicate field names, including case aliases handled
	// by encoding/json. Unknown transport fields are ignored by the contract.
	decoder := json.NewDecoder(bytes.NewReader(body))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return errors.New("expected JSON object")
	}
	keys := make(map[string]struct{})
	for decoder.More() {
		token, err = decoder.Token()
		if err != nil {
			return err
		}
		key := strings.ToLower(token.(string))
		if _, duplicate := keys[key]; duplicate {
			return errors.New("duplicate JSON field")
		}
		keys[key] = struct{}{}
		var field json.RawMessage
		if err := decoder.Decode(&field); err != nil {
			return err
		}
	}
	if _, err := decoder.Token(); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errors.New("expected a single JSON object")
	}
	if err := json.Unmarshal(body, output); err != nil {
		return err
	}
	return nil
}

func canonicalAmount(amount json.Number) json.Number {
	raw := string(amount)
	if money.MatchString(raw) && strings.Contains(raw, ".") {
		return json.Number(strings.TrimRight(strings.TrimRight(raw, "0"), "."))
	}
	return amount
}

func (p *callback) sanitize(c Config) {
	for _, hash := range []*string{&p.ExternalHash, &p.EmailHash, &p.CountryHash} {
		if !sha256Hex.MatchString(*hash) {
			*hash = ""
		}
	}
	for _, cookie := range []*string{&p.FBC, &p.FBP} {
		if len(*cookie) > 512 || !browserCookie.MatchString(*cookie) {
			*cookie = ""
		}
	}
	if net.ParseIP(p.ClientIP) == nil {
		p.ClientIP = ""
	}
	if len(p.UserAgent) > 1024 || strings.ContainsAny(p.UserAgent, "\x00\r\n") {
		p.UserAgent = ""
	}
	p.SourceURL = sanitizeURL(p.SourceURL, c.SourceHosts)
	if !money.MatchString(string(p.Amount)) || !currencyCode.MatchString(p.Currency) {
		p.Amount, p.Currency = "", ""
	}
}

func sanitizeURL(raw string, hosts []string) string {
	if len(raw) > 2048 {
		return ""
	}
	page, err := url.Parse(raw)
	if err != nil || page.Scheme != "https" || page.User != nil || page.Port() != "" ||
		page.Hostname() == "" || !slices.Contains(hosts, strings.ToLower(page.Hostname())) {
		return ""
	}
	page.RawQuery, page.Fragment, page.RawFragment, page.ForceQuery = "", "", "", false
	return page.String()
}

func (p callback) invalidReason(now time.Time, eventType string) string {
	switch {
	case p.EventID == "":
		return "event_id_missing"
	case len(p.EventID) > 128 || strings.TrimSpace(p.EventID) != p.EventID || strings.ContainsAny(p.EventID, "\r\n\x00"):
		return "event_id_invalid"
	case p.EventTime == 0:
		return "event_time_missing"
	case p.EventTime < 0 || p.EventTime > now.Unix():
		return "event_time_invalid"
	case (eventType == "order-created" || eventType == "recharge-paid") && p.OrderID == "":
		return "order_id_missing"
	case p.OrderID != "" && !partner.ValidOrderID(string(p.OrderID)):
		return "order_id_invalid"
	case eventType == "recharge-paid" && p.RechargeType != 1 && p.RechargeType != 2:
		return "recharge_type_invalid"
	default:
		return ""
	}
}

func (p callback) matchingData() map[string]any {
	out := make(map[string]any)
	for key, value := range map[string]string{"external_id": p.ExternalHash, "em": p.EmailHash, "country": p.CountryHash} {
		if value != "" {
			out[key] = []string{value}
		}
	}
	for key, value := range map[string]string{"fbc": p.FBC, "fbp": p.FBP, "client_ip_address": p.ClientIP, "client_user_agent": p.UserAgent} {
		if value != "" {
			out[key] = value
		}
	}
	return out
}
