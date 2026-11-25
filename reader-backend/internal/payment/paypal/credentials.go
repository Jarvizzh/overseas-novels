package paypal

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"reader-backend/internal/payment"
)

const (
	credentialCacheTTL = 5 * time.Minute
	EnvironmentLive    = "live"
	EnvironmentSandbox = "sandbox"
	liveAPI            = "https://api-m.paypal.com"
	sandboxAPI         = "https://api-m.sandbox.paypal.com"
)

var ErrCredentials = errors.New("PayPal credentials unavailable")

type Config struct {
	ClientID          string
	Secret            string
	Environment       string
	WebhookID         string
	PreviousWebhookID string
	MerchantID        string
}

type CredentialsSource func(context.Context) (Config, error)

type Client struct {
	loadCredentials CredentialsSource
	httpClient      *http.Client
	mu              sync.Mutex
	config          Config
	configUntil     time.Time
}

func NewClient(load CredentialsSource) *Client {
	return &Client{loadCredentials: load, httpClient: newHTTPClient()}
}

func NewScopedClient(cfg Config) (*Client, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return NewClient(func(context.Context) (Config, error) { return cfg, nil }), nil
}

func (p *Client) Provider() string { return payment.ProviderPayPal }

func (cfg Config) validate() error {
	if cfg.ClientID == "" || cfg.Secret == "" || cfg.ClientID == "paypal_client_id_placeholder" {
		return ErrCredentials
	}
	switch cfg.Environment {
	case EnvironmentSandbox, EnvironmentLive, "production":
		return nil
	default:
		return errors.New("invalid PayPal environment")
	}
}

func (p *Client) credentials(ctx context.Context) (Config, error) {
	if p.loadCredentials == nil {
		return Config{}, ErrCredentials
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if time.Now().Before(p.configUntil) {
		return p.config, nil
	}
	cfg, err := p.loadCredentials(ctx)
	if err != nil {
		return Config{}, err
	}
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	if cfg.Environment == "production" {
		cfg.Environment = EnvironmentLive
	}
	cfg.WebhookID = strings.TrimSpace(cfg.WebhookID)
	cfg.PreviousWebhookID = strings.TrimSpace(cfg.PreviousWebhookID)
	p.config = cfg
	p.configUntil = time.Now().Add(credentialCacheTTL)
	return cfg, nil
}

func (cfg Config) account() payment.WebhookAccount {
	return payment.WebhookAccount{AccountKey: cfg.ClientID, Environment: cfg.Environment}
}

func (cfg Config) baseURL() string {
	return apiBaseURL(cfg.Environment)
}

func (p *Client) CheckoutAccount(ctx context.Context) (payment.WebhookAccount, error) {
	cfg, err := p.credentials(ctx)
	if err != nil {
		return payment.WebhookAccount{}, err
	}
	return cfg.account(), nil
}

// WebhookConfigured checks the endpoint for the active credential reference.
func (p *Client) WebhookConfigured(ctx context.Context) bool {
	cfg, err := p.credentials(ctx)
	return err == nil && cfg.WebhookID != ""
}

func apiBaseURL(environment string) string {
	if environment == EnvironmentLive {
		return liveAPI
	}
	return sandboxAPI
}
