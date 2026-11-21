package sinan

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"conversion-backend/internal/partner"
)

type reportSnapshot struct {
	SourceFields sourceFields `json:"source_fields"`
	EventID      string       `json:"event_id"`
	EventName    string       `json:"event_name"`
	PixelID      identifier   `json:"pixel_id"`
	ReportTime   time.Time    `json:"report_time"`
	TraceID      *string      `json:"fbtrace_id"`
	Reason       string       `json:"reason"`
}

type reportBody struct {
	AppID         int64           `json:"app_id"`
	PixelID       identifier      `json:"pixel_id"`
	DistributorID int64           `json:"distributor_id,omitempty"`
	PromotionID   json.RawMessage `json:"promotion_id,omitempty"`
	AdID          opaqueID        `json:"ad_id,omitempty"`
	AdSetID       opaqueID        `json:"ad_set_id,omitempty"`
	OrderID       opaqueID        `json:"order_id,omitempty"`
	DeviceID      opaqueID        `json:"device_id,omitempty"`
	EventID       string          `json:"event_id"`
	Status        int             `json:"report_status"`
	ReportTime    *int64          `json:"report_time,omitempty"`
	TraceID       *string         `json:"fbtrace_id,omitempty"`
	Message       string          `json:"message,omitempty"`
}

func buildReport(status int, snapshot []byte, config Config) ([]byte, string) {
	var original reportSnapshot
	if json.Unmarshal(snapshot, &original) != nil || original.EventID == "" ||
		original.SourceFields.AppID == "" || original.PixelID == "" || original.PixelID != original.SourceFields.PixelID {
		return nil, "invalid_report_snapshot"
	}
	fields := original.SourceFields
	report := reportBody{
		AppID: fields.AppID.number(), PixelID: original.PixelID,
		DistributorID: fields.DistributorID.number(),
		AdID:          fields.AdID, AdSetID: fields.AdSetID, OrderID: fields.OrderID, DeviceID: fields.DeviceID,
		EventID: original.EventID, Status: status,
	}
	switch status {
	case 0:
		// A skipped event has no send time, trace or message in this fixture.
	case 1, -1:
		if original.ReportTime.IsZero() {
			return nil, "invalid_report_snapshot"
		}
		reportTime := original.ReportTime.Unix()
		report.ReportTime = &reportTime
		report.TraceID = original.TraceID
		report.Message = original.Reason
		if status == 1 {
			report.Message = "accepted"
		} else {
			if report.Message == "" {
				return nil, "invalid_report_snapshot"
			}
			if report.TraceID == nil || *report.TraceID == "" {
				switch config.NoTrace {
				case "omit":
					report.TraceID = nil
				case "empty":
					report.TraceID = new(string)
				default:
					return nil, "no_trace_contract_unconfirmed"
				}
			}
		}
	default:
		return nil, "invalid_report_snapshot"
	}
	switch {
	case fields.PromotionID != "":
		report.PromotionID = strconv.AppendInt(nil, fields.PromotionID.number(), 10)
	case original.EventName == "Purchase":
		if config.MissingPromotion != "null" {
			return nil, "missing_promotion_contract_unconfirmed"
		}
		report.PromotionID = json.RawMessage("null")
	}
	body, err := json.Marshal(report)
	if err != nil {
		return nil, "invalid_report_snapshot"
	}
	return body, ""
}

func mockEndpoint(raw string) (*url.URL, error) {
	endpoint, err := url.Parse(raw)
	if err != nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") ||
		endpoint.User != nil || endpoint.RawQuery != "" || endpoint.ForceQuery || endpoint.Fragment != "" ||
		endpoint.Path == "" || !net.ParseIP(endpoint.Hostname()).IsLoopback() {
		return nil, errors.New("mock endpoint must use a loopback IP and a path")
	}
	return endpoint, nil
}

func mockPrivateKey(encoded string) (ed25519.PrivateKey, error) {
	seed, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, errors.New("invalid mock Ed25519 signing seed")
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

func signedRequest(ctx context.Context, endpoint *url.URL, key ed25519.PrivateKey, body []byte) (*http.Request, error) {
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	var nonceBytes [16]byte
	if _, err := rand.Read(nonceBytes[:]); err != nil {
		return nil, err
	}
	nonce := hex.EncodeToString(nonceBytes[:])
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	signature := ed25519.Sign(key, signingInput(request.URL.EscapedPath(), timestamp, nonce, body))
	request.GetBody = nil
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Sinan-Timestamp", timestamp)
	request.Header.Set("X-Sinan-Nonce", nonce)
	request.Header.Set("X-Sinan-Signature", base64.StdEncoding.EncodeToString(signature))
	return request, nil
}

func signingInput(path, timestamp, nonce string, body []byte) []byte {
	return fmt.Appendf(nil, "POST\n%s\n%s\n%s\n%x", path, timestamp, nonce, sha256.Sum256(body))
}

type ReportAdapter struct {
	Client  *http.Client
	Decrypt Decrypt
}

func (a ReportAdapter) PrepareReport(j partner.ReportJob) ([]byte, func(context.Context) partner.ReportResult, string) {
	var config Config
	if json.Unmarshal(j.Config, &config) != nil {
		return nil, nil, "invalid_partner_report_config"
	}
	endpoint, err := mockEndpoint(config.Endpoint)
	if err != nil {
		return nil, nil, "invalid_partner_report_endpoint"
	}
	if j.Credential == nil || j.KeyVersion == nil {
		return nil, nil, "partner_report_credential_missing"
	}
	secret, err := a.Decrypt(*j.KeyVersion, j.Ciphertext)
	if err != nil {
		return nil, nil, "partner_report_credential_unavailable"
	}
	key, err := mockPrivateKey(secret)
	if err != nil {
		return nil, nil, "partner_report_credential_invalid"
	}
	body := j.Wire
	if body == nil {
		var reason string
		body, reason = buildReport(j.Status, j.Payload, config)
		if reason != "" {
			return nil, nil, reason
		}
	}
	return body, func(ctx context.Context) partner.ReportResult {
		request, err := signedRequest(ctx, endpoint, key, body)
		if err != nil {
			return partner.ReportResult{Outcome: partner.ReportNotSent, Reason: "partner_signing_failed"}
		}
		return sendReport(a.Client, request)
	}, ""
}

func sendReport(client *http.Client, request *http.Request) partner.ReportResult {
	res, err := client.Do(request)
	if err != nil {
		return partner.ReportResult{Outcome: partner.ReportUnknown, Reason: "partner_transport_unconfirmed"}
	}
	defer res.Body.Close()
	const maxResponse = 8 << 10
	body, err := io.ReadAll(io.LimitReader(res.Body, maxResponse+1))
	now := time.Now()
	result := partner.ReportResult{Outcome: partner.ReportUnknown, Reason: "partner_response_unconfirmed",
		RespondedAt: &now}
	if res.StatusCode >= 100 && res.StatusCode <= 599 {
		result.HTTPStatus = &res.StatusCode
	}
	var answer struct {
		Code *int64 `json:"code"`
	}
	if err != nil || len(body) > maxResponse || decodeObject(body, &answer) != nil || answer.Code == nil {
		return result
	}
	result.BusinessCode = answer.Code
	switch {
	case res.StatusCode == http.StatusOK && *answer.Code == 0:
		result.Outcome, result.Reason = partner.ReportAccepted, ""
	case res.StatusCode == http.StatusTooManyRequests && *answer.Code == http.StatusTooManyRequests:
		result.Outcome, result.Reason = partner.ReportRetry, "partner_rate_limited"
		result.RetryAfter = retryAfter(res.Header.Get("Retry-After"), now)
	case res.StatusCode == http.StatusOK && *answer.Code != 0 ||
		res.StatusCode >= 400 && res.StatusCode < 500 && res.StatusCode != http.StatusRequestTimeout && *answer.Code == int64(res.StatusCode):
		result.Outcome, result.Reason = partner.ReportRejected, "partner_rejected"
	}
	return result
}

func retryAfter(value string, now time.Time) time.Time {
	if seconds, err := strconv.ParseInt(value, 10, 32); err == nil && seconds >= 0 {
		return now.Add(time.Duration(seconds) * time.Second)
	}
	at, _ := http.ParseTime(value)
	return at
}
