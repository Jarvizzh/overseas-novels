// Package stripe provides the Reader's account-scoped Stripe v1 channel APIs.
// Results are Stripe evidence. Callers apply StarNovel's payment and entitlement rules.
package stripe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
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

type Config struct {
	Scope
	APIKey        string
	WebhookSecret string
}

type Client struct {
	api           *stripego.Client
	scope         Scope
	webhookSecret string
	apiKey        string
	reportHTTP    *http.Client
}

// NewClient checks that the credential belongs to the configured merchant account.
func NewClient(ctx context.Context, cfg Config) (*Client, error) {
	return newClient(ctx, cfg, &http.Client{Timeout: 15 * time.Second})
}

func newClient(ctx context.Context, cfg Config, httpClient *http.Client) (*Client, error) {
	if !validID(cfg.AccountID, "acct_") {
		return nil, errors.New("invalid Stripe account ID")
	}

	var keyMode string
	switch cfg.Environment {
	case Sandbox:
		keyMode = "test_"
	case Live:
		keyMode = "live_"
	default:
		return nil, errors.New("invalid Stripe environment")
	}
	if !validID(cfg.APIKey, "sk_"+keyMode) && !validID(cfg.APIKey, "rk_"+keyMode) {
		return nil, errors.New("Stripe API key does not match the configured environment")
	}
	if cfg.WebhookSecret != "" && !validID(cfg.WebhookSecret, "whsec_") {
		return nil, errors.New("invalid Stripe webhook secret")
	}

	backend := stripego.NewBackendsWithConfig(&stripego.BackendConfig{
		HTTPClient:        httpClient,
		MaxNetworkRetries: stripego.Int64(0),
		LeveledLogger:     &stripego.LeveledLogger{Level: stripego.LevelNull},
	})
	api := stripego.NewClient(cfg.APIKey, stripego.WithBackends(backend))
	account, err := api.V1Accounts.Retrieve(ctx, nil)
	if err != nil {
		return nil, classify(err)
	}
	if account.ID != cfg.AccountID {
		return nil, errors.New("Stripe API key belongs to a different account")
	}
	reportHTTP := &http.Client{
		Transport: httpClient.Transport,
		Timeout:   2 * time.Minute,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return &Client{
		api: api, scope: cfg.Scope, webhookSecret: cfg.WebhookSecret,
		apiKey: cfg.APIKey, reportHTTP: reportHTTP,
	}, nil
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
