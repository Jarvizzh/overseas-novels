package meta

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"regexp"
	"strconv"
	"sync/atomic"
	"time"

	"conversion-backend/internal/delivery"
)

const maxResponse = 64 << 10

var tracePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

type Client struct {
	BaseURL *url.URL
	HTTP    *http.Client
}

type event struct {
	EventName    string          `json:"event_name"`
	EventID      string          `json:"event_id"`
	EventTime    int64           `json:"event_time"`
	ActionSource string          `json:"action_source"`
	SourceURL    string          `json:"event_source_url,omitempty"`
	UserData     json.RawMessage `json:"user_data"`
	CustomData   map[string]any  `json:"custom_data,omitempty"`
}

func (c Client) Prepare(p delivery.Payload, token string) (delivery.Send, error) {
	if err := delivery.ValidatePayload(p); err != nil {
		return nil, err
	}
	e := event{EventName: p.EventName, EventID: p.EventID, EventTime: p.OccurredAt.Unix(),
		ActionSource: *p.ActionSource, UserData: p.MatchingData}
	if p.SourceURL != nil {
		e.SourceURL = *p.SourceURL
	}
	if p.Amount != nil && p.Currency != nil {
		e.CustomData = map[string]any{"value": json.Number(*p.Amount), "currency": *p.Currency}
	}
	body, err := json.Marshal(struct {
		Data []event `json:"data"`
	}{[]event{e}})
	if err != nil {
		return nil, errors.New("invalid_event_payload")
	}
	req, err := http.NewRequest(http.MethodPost, c.BaseURL.JoinPath(p.PixelID, "events").String(), bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("invalid_meta_endpoint")
	}
	req.GetBody = nil
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	return func(ctx context.Context) delivery.Result { return c.send(ctx, req) }, nil
}

func (c Client) send(ctx context.Context, req *http.Request) delivery.Result {
	var connected atomic.Bool
	trace := &httptrace.ClientTrace{GotConn: func(httptrace.GotConnInfo) { connected.Store(true) }}
	req = req.WithContext(httptrace.WithClientTrace(ctx, trace))
	res, err := c.HTTP.Do(req)
	if err != nil {
		// A connection may have written a partial request even when the write hook reports an error.
		if !connected.Load() {
			var op *net.OpError
			var dns *net.DNSError
			if (errors.As(err, &op) && op.Op == "dial") || errors.As(err, &dns) {
				return delivery.Result{Outcome: delivery.NotSent, Reason: "connection_not_established"}
			}
		}
		return delivery.Result{Outcome: delivery.OutcomeUnknown, Reason: "transport_confirmation_missing"}
	}
	defer res.Body.Close()
	now := time.Now()
	r := delivery.Result{Outcome: delivery.OutcomeUnknown, Reason: "meta_no_confirmation",
		HTTPStatus: &res.StatusCode, RespondedAt: now}
	raw, err := io.ReadAll(io.LimitReader(res.Body, maxResponse+1))
	if err != nil || len(raw) > maxResponse {
		return r
	}
	var response struct {
		EventsReceived *int   `json:"events_received"`
		TraceID        string `json:"fbtrace_id"`
		Error          *struct {
			Code      int    `json:"code"`
			Subcode   int    `json:"error_subcode"`
			Transient bool   `json:"is_transient"`
			TraceID   string `json:"fbtrace_id"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return r
	}
	r.EventsReceived = response.EventsReceived
	r.TraceID = validTrace(response.TraceID)
	if res.StatusCode == http.StatusOK && response.Error == nil && r.EventsReceived != nil && *r.EventsReceived == 1 {
		r.Outcome, r.Reason = delivery.Accepted, ""
		return r
	}
	e := response.Error
	if e == nil || e.Code <= 0 || res.StatusCode < 400 || (r.EventsReceived != nil && *r.EventsReceived != 0) {
		return r
	}
	r.GraphCode, r.GraphSubcode = &e.Code, &e.Subcode
	if responseTrace := validTrace(e.TraceID); responseTrace != "" {
		r.TraceID = responseTrace
	}
	switch {
	case e.Code == 190 || e.Code == 10 || e.Code == 200:
		r.Outcome, r.Reason, r.Block = delivery.PermanentRejection, "meta_credentials_or_permissions", true
	case res.StatusCode == http.StatusTooManyRequests || e.Transient ||
		e.Code == 4 || e.Code == 17 || e.Code == 32 || e.Code == 613:
		r.Outcome, r.Reason = delivery.RetryableRejection, "meta_retryable_rejection"
		r.RetryAfter = retryAfter(res.Header.Get("Retry-After"), now)
	case res.StatusCode == http.StatusBadRequest && e.Code == 100:
		r.Outcome, r.Reason = delivery.PermanentRejection, "meta_invalid_parameter"
	}
	return r
}

func validTrace(value string) string {
	if tracePattern.MatchString(value) {
		return value
	}
	return ""
}

func retryAfter(value string, now time.Time) time.Time {
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds >= 0 && seconds <= 1<<32 {
		return now.Add(time.Duration(seconds) * time.Second)
	}
	if date, err := http.ParseTime(value); err == nil {
		return date
	}
	return time.Time{}
}
