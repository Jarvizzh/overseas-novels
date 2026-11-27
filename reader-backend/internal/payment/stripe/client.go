// Package stripe provides the Reader's account-scoped Stripe v1 channel APIs.
// Results are Stripe evidence. Callers apply StarNovel's payment and entitlement rules.
package stripe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	stripego "github.com/stripe/stripe-go/v87"
	"reader-backend/internal/payment"
)

const (
	APIVersion = stripego.APIVersion

	// IdempotencyWindow is measured from the first submission. Stripe can prune
	// the key after this window, so the caller must persist its retry deadline.
	IdempotencyWindow = 24 * time.Hour
)

type Environment string

const (
	Sandbox Environment = "sandbox"
	Live    Environment = "live"
)

type Scope struct {
	AccountID   string
	Environment Environment
}

type RetryPolicy struct {
	MaxRetries int
	BaseDelay  time.Duration
	MaxDelay   time.Duration
	Sleep      func(context.Context, time.Duration) error
}

type Config struct {
	Scope
	APIKey                string
	WebhookSecret         string
	PreviousWebhookSecret string
	HTTPClient            *http.Client
	RetryPolicy           *RetryPolicy
	DisableRetries        bool
}

type Client struct {
	api                   *stripego.Client
	scope                 Scope
	webhookSecret         string
	previousWebhookSecret string
	apiKey                string
	reportHTTP            *http.Client
}

const (
	defaultStripeHTTPTimeout = 15 * time.Second
	defaultStripeMaxRetries  = 2
	maxAllowedStripeRetries  = 3
	defaultStripeRetryBase   = 200 * time.Millisecond
	defaultStripeRetryMax    = 2 * time.Second
)

// DefaultRetryPolicy returns the bounded retry policy applied to pure reads and
// writes carrying a stable Idempotency-Key.
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxRetries: defaultStripeMaxRetries,
		BaseDelay:  defaultStripeRetryBase,
		MaxDelay:   defaultStripeRetryMax,
		Sleep:      sleepWithContext,
	}
}

func resolveRetryPolicy(cfg Config) RetryPolicy {
	if cfg.DisableRetries {
		return RetryPolicy{}
	}
	if cfg.RetryPolicy == nil {
		return DefaultRetryPolicy()
	}
	policy := *cfg.RetryPolicy
	if policy.MaxRetries < 0 {
		policy.MaxRetries = 0
	}
	if policy.MaxRetries > maxAllowedStripeRetries {
		policy.MaxRetries = maxAllowedStripeRetries
	}
	if policy.BaseDelay <= 0 {
		policy.BaseDelay = defaultStripeRetryBase
	}
	if policy.MaxDelay <= 0 {
		policy.MaxDelay = defaultStripeRetryMax
	}
	if policy.MaxDelay < policy.BaseDelay {
		policy.MaxDelay = policy.BaseDelay
	}
	if policy.Sleep == nil {
		policy.Sleep = sleepWithContext
	}
	return policy
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type retryRoundTripper struct {
	base   http.RoundTripper
	policy RetryPolicy
}

func wrapHTTPClientWithRetries(base *http.Client, policy RetryPolicy) *http.Client {
	if policy.MaxRetries <= 0 {
		return base
	}
	transport := base.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	cloned := *base
	cloned.Transport = &retryRoundTripper{
		base:   transport,
		policy: policy,
	}
	return &cloned
}

func isRetryableStripeRequest(req *http.Request) (isRead bool, isIdempotentWrite bool) {
	switch req.Method {
	case http.MethodGet, http.MethodHead:
		return true, false
	case http.MethodPost, http.MethodPut, http.MethodDelete:
		return false, strings.TrimSpace(req.Header.Get("Idempotency-Key")) != ""
	default:
		return false, false
	}
}

func (rt *retryRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	isRead, isIdempotentWrite := isRetryableStripeRequest(req)
	if rt.policy.MaxRetries <= 0 || (!isRead && !isIdempotentWrite) {
		return rt.base.RoundTrip(req)
	}

	var bodyBytes []byte
	if req.Body != nil {
		var err error
		bodyBytes, err = io.ReadAll(req.Body)
		_ = req.Body.Close()
		if err != nil {
			return nil, err
		}
	}

	for attempt := 0; ; attempt++ {
		attemptReq := req.Clone(req.Context())
		if bodyBytes != nil {
			attemptReq.Body = io.NopCloser(bytes.NewReader(bodyBytes))
			attemptReq.ContentLength = int64(len(bodyBytes))
		}
		resp, err := rt.base.RoundTrip(attemptReq)
		if err != nil {
			if req.Context().Err() != nil || attempt >= rt.policy.MaxRetries {
				return nil, err
			}
			if sleepErr := rt.policy.Sleep(req.Context(), rt.backoff(attempt, nil)); sleepErr != nil {
				return nil, err
			}
			continue
		}
		if !shouldRetryStripeResponse(resp, isIdempotentWrite) || attempt >= rt.policy.MaxRetries {
			return resp, nil
		}
		delay := rt.backoff(attempt, resp.Header)
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if sleepErr := rt.policy.Sleep(req.Context(), delay); sleepErr != nil {
			return nil, sleepErr
		}
	}
}

func shouldRetryStripeResponse(resp *http.Response, isIdempotentWrite bool) bool {
	if resp == nil {
		return false
	}
	switch resp.StatusCode {
	case http.StatusTooManyRequests, http.StatusRequestTimeout,
		http.StatusInternalServerError, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	case http.StatusConflict:
		if !isIdempotentWrite || resp.Body == nil {
			return false
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(body))
		if err != nil {
			return false
		}
		return strings.Contains(string(body), string(stripego.ErrorCodeIdempotencyKeyInUse))
	default:
		return false
	}
}

func (rt *retryRoundTripper) backoff(attempt int, header http.Header) time.Duration {
	delay := rt.policy.BaseDelay << attempt
	if header != nil {
		if raw := strings.TrimSpace(header.Get("Retry-After")); raw != "" {
			if secs, err := strconv.Atoi(raw); err == nil && secs > 0 {
				delay = time.Duration(secs) * time.Second
			}
		}
	}
	if delay > rt.policy.MaxDelay {
		delay = rt.policy.MaxDelay
	}
	return delay
}

// NewClient checks that the credential belongs to the configured merchant account.
func NewClient(ctx context.Context, cfg Config) (*Client, error) {
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultStripeHTTPTimeout}
	}
	return newClient(ctx, cfg, httpClient)
}

// NewConfiguredClient initializes a dynamic DB-verified Stripe client without a blocking remote round-trip.
func NewConfiguredClient(cfg Config) (*Client, error) {
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultStripeHTTPTimeout}
	}
	return initClient(context.Background(), cfg, httpClient, false)
}

func newClient(ctx context.Context, cfg Config, httpClient *http.Client) (*Client, error) {
	return initClient(ctx, cfg, httpClient, true)
}

func initClient(ctx context.Context, cfg Config, httpClient *http.Client, verifyRemote bool) (*Client, error) {
	if !validID(cfg.AccountID, "acct_") {
		return nil, &CredentialError{
			AccountID:   cfg.AccountID,
			Environment: cfg.Environment,
			Purpose:     PurposeAccount,
			Reason:      ReasonAccountMismatch,
			Retryable:   false,
			Detail:      "invalid Stripe account ID",
		}
	}

	var keyMode string
	switch cfg.Environment {
	case Sandbox:
		keyMode = "test_"
	case Live:
		keyMode = "live_"
	default:
		return nil, &CredentialError{
			AccountID:   cfg.AccountID,
			Environment: cfg.Environment,
			Purpose:     PurposeAccount,
			Reason:      ReasonEnvironmentMismatch,
			Retryable:   false,
			Detail:      "invalid Stripe environment",
		}
	}
	if !validID(cfg.APIKey, "sk_"+keyMode) && !validID(cfg.APIKey, "rk_"+keyMode) {
		return nil, &CredentialError{
			AccountID:   cfg.AccountID,
			Environment: cfg.Environment,
			Purpose:     PurposeAccount,
			Reason:      ReasonEnvironmentMismatch,
			Retryable:   true,
			Detail:      "Stripe API key does not match the configured environment",
		}
	}
	if cfg.WebhookSecret != "" && !validID(cfg.WebhookSecret, "whsec_") {
		return nil, errors.New("invalid Stripe webhook secret")
	}
	if cfg.PreviousWebhookSecret != "" && !validID(cfg.PreviousWebhookSecret, "whsec_") {
		return nil, errors.New("invalid Stripe previous webhook secret")
	}

	retryPolicy := resolveRetryPolicy(cfg)
	apiHTTP := wrapHTTPClientWithRetries(httpClient, retryPolicy)
	backend := stripego.NewBackendsWithConfig(&stripego.BackendConfig{
		HTTPClient:        apiHTTP,
		MaxNetworkRetries: stripego.Int64(0),
		LeveledLogger:     &stripego.LeveledLogger{Level: stripego.LevelNull},
	})
	api := stripego.NewClient(cfg.APIKey, stripego.WithBackends(backend))
	reportHTTP := &http.Client{
		Transport: httpClient.Transport,
		Timeout:   2 * time.Minute,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	client := &Client{
		api: api, scope: cfg.Scope, webhookSecret: cfg.WebhookSecret,
		previousWebhookSecret: cfg.PreviousWebhookSecret,
		apiKey:                cfg.APIKey, reportHTTP: reportHTTP,
	}
	if verifyRemote {
		if err := client.VerifyAccount(ctx); err != nil {
			return nil, err
		}
	}
	return client, nil
}

func (c *Client) Scope() Scope     { return c.scope }
func (c *Client) Provider() string { return payment.ProviderStripe }

type Disposition string

const (
	Rejected     Disposition = "rejected"
	RetrySameKey Disposition = "retry_same_key"
	LookupOnly   Disposition = "lookup_only"
)

func CanRetryWrite(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Disposition == RetrySameKey
}

// APIError omits the provider's raw error message, which can contain customer data.
type APIError struct {
	Disposition Disposition
	Status      int
	RequestID   string
	PaymentFailure
}

func (e *APIError) Error() string {
	return fmt.Sprintf("Stripe API request: %s (HTTP %d, code %q, request %q)", e.Disposition, e.Status, e.Code, e.RequestID)
}

func classify(err error) error {
	var apiErr *stripego.Error
	if !errors.As(err, &apiErr) {
		// A lost response or an unreadable response can follow a successful write.
		return &APIError{Disposition: LookupOnly}
	}

	disposition := LookupOnly
	switch {
	case apiErr.Code == stripego.ErrorCodeIdempotencyKeyInUse, apiErr.HTTPStatusCode == http.StatusTooManyRequests:
		disposition = RetrySameKey
	case apiErr.HTTPStatusCode >= 400 && apiErr.HTTPStatusCode < 500 &&
		apiErr.Type != stripego.ErrorTypeIdempotency &&
		apiErr.HTTPStatusCode != http.StatusConflict && apiErr.HTTPStatusCode != http.StatusRequestTimeout:
		disposition = Rejected
	}

	return &APIError{
		Disposition:    disposition,
		Status:         apiErr.HTTPStatusCode,
		RequestID:      apiErr.RequestID,
		PaymentFailure: paymentFailure(apiErr),
	}
}

func apiResult[T any](result *T, err error) (*T, error) {
	if err != nil {
		return nil, classify(err)
	}
	return result, nil
}

func writeParams(key string) (stripego.Params, error) {
	var params stripego.Params
	if len(key) == 0 || len(key) > 255 || strings.ContainsFunc(key, func(r rune) bool {
		return r <= ' ' || r > '~'
	}) {
		return params, errors.New("Stripe write requires a valid idempotency key")
	}
	params.SetIdempotencyKey(key)
	return params, nil
}

func validID(value, prefix string) bool {
	return strings.HasPrefix(value, prefix) && len(value) > len(prefix) && validToken(value)
}

func validToken(value string) bool {
	return value != "" && !strings.ContainsFunc(value, func(r rune) bool {
		return r != '_' && r != '-' && !(r >= '0' && r <= '9') &&
			!(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z')
	})
}

func validChargeID(value string) bool {
	return validID(value, "ch_") || validID(value, "py_")
}

type PageOptions struct {
	Limit         int64
	StartingAfter string
}

type Page[T any] struct {
	Items   []T
	HasMore bool
}

func (opts PageOptions) params() (stripego.ListParams, error) {
	params := stripego.ListParams{Single: true}
	if opts.Limit < 0 || opts.Limit > 100 {
		return params, errors.New("Stripe page size must be between 1 and 100")
	}
	if opts.Limit != 0 {
		params.Limit = stripego.Int64(opts.Limit)
	}
	if opts.StartingAfter != "" {
		params.StartingAfter = stripego.String(opts.StartingAfter)
	}
	return params, nil
}

func listPage[T any](list *stripego.V1List[T]) (Page[T], error) {
	if err := list.Err(); err != nil {
		return Page[T]{}, classify(err)
	}
	return Page[T]{Items: list.Data(), HasMore: list.Meta().HasMore}, nil
}

// rawPageFields recovers fields that SDK scalar types cannot represent losslessly.
func rawPageFields[T, F any](list *stripego.V1List[T]) ([]F, error) {
	var fields struct {
		Data []F `json:"data"`
	}
	if err := json.Unmarshal(list.LastResponse().RawJSON, &fields); err != nil {
		return nil, classify(err)
	}
	if len(fields.Data) != len(list.Data()) {
		return nil, errors.New("unexpected Stripe response page")
	}
	return fields.Data, nil
}

// TimeRange specifies an inclusive lower bound and exclusive upper bound.
type TimeRange struct {
	From, Before time.Time
}

func (r TimeRange) params() (*stripego.RangeQueryParams, error) {
	start, end := r.From.Unix(), r.Before.Unix()
	if start <= 0 || end <= start {
		return nil, errors.New("invalid Stripe query time range")
	}
	return &stripego.RangeQueryParams{GreaterThanOrEqual: start, LesserThan: end}, nil
}
