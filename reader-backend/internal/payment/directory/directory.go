package directory

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"reader-backend/internal/config"
	"reader-backend/internal/conversion"
	"reader-backend/internal/credentials"
	"reader-backend/internal/payment"
	"reader-backend/internal/payment/paypal"
	"reader-backend/internal/payment/stripe"
)

const (
	queryTimeout = 3 * time.Second

	credRefDBEncrypted = "db:encrypted"

	statusPublished = "published"
	statusDisabled  = "disabled"
	statusVerified  = "verified"

	credPurposeAPIKey                = "api_key"
	credPurposeWebhookSecret         = "webhook_secret"
	credPurposePreviousWebhookSecret = "previous_webhook_secret"
	credPurposePayoutAPIKey          = "payout_api_key"
	credPurposeLedgerAPIKey          = "ledger_api_key"
	credPurposeReportAPIKey          = "report_api_key"
	credPurposeFilesAPIKey           = "files_api_key"
	credPurposeEventsAPIKey          = "events_api_key"
	credPurposeAftercareAPIKey       = "aftercare_api_key"
)

type cacheKey struct {
	accountID int64
	version   int
	graceSig  string
}

type accountRow struct {
	id             int64
	provider       string
	environment    string
	accountKey     string
	merchantID     string
	credentialsRef string
	status         string
	isPrimary      bool
	activeVersion  int
}

// Directory resolves live and historical payment channel clients from Postgres
// without restarting Reader when credentials are published or rotated in CMS.
type Directory struct {
	pool             *pgxpool.Pool
	staticAdapters   payment.Adapters
	stripeHTTPClient *http.Client

	mu    sync.RWMutex
	cache map[cacheKey]payment.Adapter
}

func New(pool *pgxpool.Pool, static payment.Adapters) *Directory {
	return &Directory{
		pool:           pool,
		staticAdapters: static,
		cache:          make(map[cacheKey]payment.Adapter),
	}
}

// WithStripeHTTPClient returns a Directory configured with a custom HTTP client
// for Stripe account verification and API requests (primarily for tests).
func (d *Directory) WithStripeHTTPClient(client *http.Client) *Directory {
	return &Directory{
		pool:             d.pool,
		staticAdapters:   d.staticAdapters,
		stripeHTTPClient: client,
		cache:            make(map[cacheKey]payment.Adapter),
	}
}

func targetChannelEnvironment() string {
	if conversion.Environment() == "production" {
		return "live"
	}
	return "sandbox"
}

func (d *Directory) PrimaryAdapter(provider string) (payment.Adapter, bool) {
	if d == nil || d.pool == nil {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
	defer cancel()
	var row accountRow
	err := d.pool.QueryRow(ctx, `
		SELECT id, provider, environment, account_key, merchant_id, credentials_ref, status, is_primary, active_version
		FROM payment_channel_accounts
		WHERE provider = $1 AND environment = $2 AND status = 'published' AND is_primary = true
		LIMIT 1`, provider, targetChannelEnvironment()).
		Scan(&row.id, &row.provider, &row.environment, &row.accountKey, &row.merchantID, &row.credentialsRef, &row.status, &row.isPrimary, &row.activeVersion)
	if err != nil {
		return nil, false
	}
	adapter, err := d.adapterForRow(ctx, row)
	if err != nil {
		log.Printf("channel directory primary resolve failed provider=%s account=%d: %s", provider, row.id, stripe.RedactSecrets(err.Error()))
		return nil, false
	}
	return adapter, true
}

func (d *Directory) AdapterForScope(provider string, scope payment.WebhookAccount) (payment.Adapter, bool) {
	if d == nil || d.pool == nil || scope.AccountKey == "" || scope.Environment == "" {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
	defer cancel()
	var row accountRow
	err := d.pool.QueryRow(ctx, `
		SELECT id, provider, environment, account_key, merchant_id, credentials_ref, status, is_primary, active_version
		FROM payment_channel_accounts
		WHERE provider = $1 AND account_key = $2 AND environment = $3 AND status IN ('published', 'disabled', 'verified')
		LIMIT 1`, provider, scope.AccountKey, scope.Environment).
		Scan(&row.id, &row.provider, &row.environment, &row.accountKey, &row.merchantID, &row.credentialsRef, &row.status, &row.isPrimary, &row.activeVersion)
	if err != nil {
		return nil, false
	}
	adapter, err := d.adapterForRow(ctx, row)
	if err != nil {
		return nil, false
	}
	return adapter, true
}

func (d *Directory) AllAdapters(provider string) []payment.Adapter {
	if d == nil || d.pool == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
	defer cancel()
	rows, err := d.pool.Query(ctx, `
		SELECT id, provider, environment, account_key, merchant_id, credentials_ref, status, is_primary, active_version
		FROM payment_channel_accounts
		WHERE provider = $1 AND status IN ('published', 'disabled', 'verified')
		ORDER BY is_primary DESC, id ASC`, provider)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var result []payment.Adapter
	for rows.Next() {
		var row accountRow
		if scanErr := rows.Scan(&row.id, &row.provider, &row.environment, &row.accountKey, &row.merchantID, &row.credentialsRef, &row.status, &row.isPrimary, &row.activeVersion); scanErr != nil {
			continue
		}
		if adapter, err := d.adapterForRow(ctx, row); err == nil && adapter != nil {
			result = append(result, adapter)
		}
	}
	return result
}

func (d *Directory) HasAccounts(provider string) bool {
	if d == nil || d.pool == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
	defer cancel()
	var exists bool
	if err := d.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM payment_channel_accounts
			WHERE provider = $1 AND status IN ('published', 'disabled', 'verified')
		)`, provider).Scan(&exists); err != nil {
		return false
	}
	return exists
}

// ResolveWebhookToken finds the exact channel account owning an incoming webhook token
// and builds its adapter with active + unexpired previous webhook secrets.
func (d *Directory) ResolveWebhookToken(ctx context.Context, rawToken string) (int64, string, payment.WebhookAccount, payment.Adapter, error) {
	if d == nil || d.pool == nil {
		return 0, "", payment.WebhookAccount{}, nil, errors.New("channel directory unavailable")
	}
	token, err := uuid.Parse(rawToken)
	if err != nil || token == uuid.Nil {
		return 0, "", payment.WebhookAccount{}, nil, errors.New("invalid webhook channel token")
	}
	var row accountRow
	err = d.pool.QueryRow(ctx, `
		SELECT id, provider, environment, account_key, merchant_id, credentials_ref, status, is_primary, active_version
		FROM payment_channel_accounts
		WHERE webhook_token = $1 AND status IN ('published', 'disabled', 'verified')
		LIMIT 1`, token).
		Scan(&row.id, &row.provider, &row.environment, &row.accountKey, &row.merchantID, &row.credentialsRef, &row.status, &row.isPrimary, &row.activeVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, "", payment.WebhookAccount{}, nil, errors.New("webhook channel account not found")
	}
	if err != nil {
		return 0, "", payment.WebhookAccount{}, nil, err
	}
	if row.environment != targetChannelEnvironment() {
		return 0, "", payment.WebhookAccount{}, nil, errors.New("webhook environment mismatch")
	}
	adapter, err := d.adapterForRow(ctx, row)
	if err != nil {
		return 0, "", payment.WebhookAccount{}, nil, err
	}
	scope := payment.WebhookAccount{AccountKey: row.accountKey, Environment: row.environment}
	return row.id, row.provider, scope, adapter, nil
}

func (d *Directory) adapterForRow(ctx context.Context, row accountRow) (payment.Adapter, error) {
	scope := payment.WebhookAccount{AccountKey: row.accountKey, Environment: row.environment}
	if row.credentialsRef != "db:encrypted" {
		if adapter, ok := payment.AccountCapability[payment.Adapter](d.staticAdapters, row.provider, scope); ok {
			return adapter, nil
		}
		if adapter, ok := payment.LegacyCapability[payment.Adapter](d.staticAdapters, row.provider); ok {
			return adapter, nil
		}
		return nil, fmt.Errorf("static adapter %s/%s (%s) not registered", row.provider, row.environment, row.accountKey)
	}

	secrets, graceSig, err := d.loadEncryptedSecrets(ctx, row.id, row.activeVersion)
	if err != nil {
		return nil, err
	}
	key := cacheKey{accountID: row.id, version: row.activeVersion, graceSig: graceSig}
	d.mu.RLock()
	cached, ok := d.cache[key]
	d.mu.RUnlock()
	if ok && cached != nil {
		return cached, nil
	}

	built, err := buildEncryptedAdapter(row, secrets)
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	d.cache[key] = built
	d.mu.Unlock()
	return built, nil
}

func (d *Directory) loadEncryptedSecrets(ctx context.Context, accountID int64, version int) (map[string]string, string, error) {
	ring, err := credentials.LoadFromEnv()
	if err != nil {
		return nil, "", err
	}
	rows, err := d.pool.Query(ctx, `
		SELECT purpose, key_version, ciphertext, expires_at
		FROM payment_channel_credentials
		WHERE account_id = $1 AND version = $2`, accountID, version)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	now := time.Now().UTC()
	out := make(map[string]string)
	var graceSig string
	for rows.Next() {
		var purpose, keyVer, ciphertext string
		var expiresAt *time.Time
		if err := rows.Scan(&purpose, &keyVer, &ciphertext, &expiresAt); err != nil {
			return nil, "", err
		}
		if expiresAt != nil && now.After(*expiresAt) {
			continue
		}
		pt, err := ring.Decrypt(keyVer, ciphertext, credentials.CredentialAAD(accountID, version, purpose))
		if err != nil {
			return nil, "", fmt.Errorf("decrypt %s: %w", purpose, err)
		}
		out[purpose] = pt
		if purpose == "previous_webhook_secret" || purpose == "previous_webhook_id" {
			graceSig += purpose
		}
	}
	return out, graceSig, rows.Err()
}

func buildEncryptedAdapter(row accountRow, secrets map[string]string) (payment.Adapter, error) {
	switch row.provider {
	case payment.ProviderPayPal:
		clientID := secrets["client_id"]
		if clientID == "" {
			clientID = row.accountKey
		}
		return paypal.NewScopedClient(paypal.Config{
			ClientID:          clientID,
			Secret:            secrets["api_secret"],
			Environment:       row.environment,
			WebhookID:         secrets["webhook_id"],
			PreviousWebhookID: secrets["previous_webhook_id"],
			MerchantID:        row.merchantID,
		})
	case payment.ProviderStripe:
		env := stripe.Sandbox
		if row.environment == "live" {
			env = stripe.Live
		}
		return stripe.NewConfiguredClient(stripe.Config{
			Scope:                 stripe.Scope{AccountID: row.accountKey, Environment: env},
			APIKey:                secrets[credPurposeAPIKey],
			WebhookSecret:         secrets[credPurposeWebhookSecret],
			PreviousWebhookSecret: secrets[credPurposePreviousWebhookSecret],
		})
	default:
		return nil, fmt.Errorf("unsupported provider %q", row.provider)
	}
}

// ResolveStripeClient resolves a verified Stripe client for a specific merchant account,
// environment, and explicit purpose. Dynamic CMS accounts ("db:encrypted") only use
// encrypted database credentials; legacy static channels retain env-var fallback.
func (d *Directory) ResolveStripeClient(ctx context.Context, scope payment.WebhookAccount, purpose stripe.Purpose) (*stripe.Client, error) {
	stripeScope := stripe.Scope{AccountID: strings.TrimSpace(scope.AccountKey), Environment: stripe.Environment(strings.TrimSpace(scope.Environment))}
	if purpose == "" {
		purpose = stripe.PurposeAccount
	}
	if stripeScope.AccountID == "" {
		return nil, &stripe.CredentialError{
			Environment: stripeScope.Environment,
			Purpose:     purpose,
			Reason:      stripe.ReasonAccountMismatch,
			Retryable:   false,
			Detail:      "Stripe account key is required",
		}
	}
	if stripeScope.Environment != stripe.Sandbox && stripeScope.Environment != stripe.Live {
		return nil, &stripe.CredentialError{
			AccountID:   stripeScope.AccountID,
			Environment: stripeScope.Environment,
			Purpose:     purpose,
			Reason:      stripe.ReasonEnvironmentMismatch,
			Retryable:   false,
			Detail:      "Stripe environment must be sandbox or live",
		}
	}
	if d == nil || d.pool == nil {
		return d.resolveStaticStripeClient(ctx, stripeScope, purpose)
	}

	var row accountRow
	err := d.pool.QueryRow(ctx, `
		SELECT id, provider, environment, account_key, merchant_id, credentials_ref, status, is_primary, active_version
		FROM payment_channel_accounts
		WHERE provider = $1 AND account_key = $2 AND environment = $3
		LIMIT 1`, payment.ProviderStripe, stripeScope.AccountID, string(stripeScope.Environment)).
		Scan(&row.id, &row.provider, &row.environment, &row.accountKey, &row.merchantID, &row.credentialsRef, &row.status, &row.isPrimary, &row.activeVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		var otherEnv string
		otherErr := d.pool.QueryRow(ctx, `
			SELECT environment
			FROM payment_channel_accounts
			WHERE provider = $1 AND account_key = $2
			LIMIT 1`, payment.ProviderStripe, stripeScope.AccountID).Scan(&otherEnv)
		if otherErr == nil && otherEnv != "" {
			return nil, &stripe.CredentialError{
				AccountID:   stripeScope.AccountID,
				Environment: stripeScope.Environment,
				Purpose:     purpose,
				Reason:      stripe.ReasonEnvironmentMismatch,
				Retryable:   true,
				Detail:      fmt.Sprintf("Stripe account is registered for %s, not %s", otherEnv, stripeScope.Environment),
			}
		}
		if _, configured := config.FindPaymentAccount(payment.ProviderStripe, stripeScope.AccountID, string(stripeScope.Environment)); configured {
			return d.resolveStaticStripeClient(ctx, stripeScope, purpose)
		}
		return nil, &stripe.CredentialError{
			AccountID:   stripeScope.AccountID,
			Environment: stripeScope.Environment,
			Purpose:     purpose,
			Reason:      stripe.ReasonAccountNotFound,
			Retryable:   true,
			Detail:      "Stripe channel account is not registered",
		}
	}
	if err != nil {
		return nil, &stripe.CredentialError{
			AccountID:   stripeScope.AccountID,
			Environment: stripeScope.Environment,
			Purpose:     purpose,
			Reason:      stripe.ReasonUpstreamUnavailable,
			Retryable:   true,
			Detail:      "failed to query Stripe channel account",
			Err:         err,
		}
	}

	if row.status != statusPublished && row.status != statusDisabled && row.status != statusVerified {
		return nil, &stripe.CredentialError{
			AccountID:   stripeScope.AccountID,
			Environment: stripeScope.Environment,
			Purpose:     purpose,
			Reason:      stripe.ReasonAccountNotReady,
			Retryable:   true,
			Detail:      fmt.Sprintf("Stripe channel account status %q is not verified or published", row.status),
		}
	}

	if row.credentialsRef != credRefDBEncrypted {
		return d.resolveStaticStripeClient(ctx, stripeScope, purpose)
	}

	secrets, _, err := d.loadEncryptedSecrets(ctx, row.id, row.activeVersion)
	if err != nil {
		return nil, &stripe.CredentialError{
			AccountID:   stripeScope.AccountID,
			Environment: stripeScope.Environment,
			Purpose:     purpose,
			Reason:      stripe.ReasonCredentialDecrypt,
			Retryable:   true,
			Detail:      "failed to decrypt Stripe channel credentials",
			Err:         err,
		}
	}
	apiKey := selectStripeAPIKey(secrets, purpose)
	if apiKey == "" {
		return nil, &stripe.CredentialError{
			AccountID:   stripeScope.AccountID,
			Environment: stripeScope.Environment,
			Purpose:     purpose,
			Reason:      stripe.ReasonCredentialMissing,
			Retryable:   true,
			Detail:      "Stripe channel account has no active encrypted API key",
		}
	}
	client, err := stripe.NewClient(ctx, stripe.Config{
		Scope:                 stripeScope,
		APIKey:                apiKey,
		WebhookSecret:         secrets[credPurposeWebhookSecret],
		PreviousWebhookSecret: secrets[credPurposePreviousWebhookSecret],
		HTTPClient:            d.stripeHTTPClient,
	})
	if err != nil {
		return nil, stripe.ClassifyPurposeError(stripeScope, purpose, err)
	}
	return client, nil
}

// VerifyStripePurpose resolves the Stripe client for scope and purpose, then
// validates both account ownership and the minimal API permission for purpose.
func (d *Directory) VerifyStripePurpose(ctx context.Context, scope payment.WebhookAccount, purpose stripe.Purpose) error {
	client, err := d.ResolveStripeClient(ctx, scope, purpose)
	if err != nil {
		return err
	}
	return client.VerifyPurpose(ctx, purpose)
}

func (d *Directory) resolveStaticStripeClient(ctx context.Context, stripeScope stripe.Scope, purpose stripe.Purpose) (*stripe.Client, error) {
	apiKey := staticStripeAPIKey(stripeScope, purpose)
	if apiKey != "" {
		var httpClient *http.Client
		if d != nil {
			httpClient = d.stripeHTTPClient
		}
		client, err := stripe.NewClient(ctx, stripe.Config{
			Scope:      stripeScope,
			APIKey:     apiKey,
			HTTPClient: httpClient,
		})
		if err != nil {
			return nil, stripe.ClassifyPurposeError(stripeScope, purpose, err)
		}
		return client, nil
	}
	if d != nil {
		scope := payment.WebhookAccount{AccountKey: stripeScope.AccountID, Environment: string(stripeScope.Environment)}
		if adapter, ok := payment.AccountCapability[*stripe.Client](d.staticAdapters, payment.ProviderStripe, scope); ok && adapter != nil {
			if err := adapter.VerifyAccount(ctx); err != nil {
				return nil, stripe.ClassifyPurposeError(stripeScope, purpose, err)
			}
			return adapter, nil
		}
	}
	return nil, &stripe.CredentialError{
		AccountID:   stripeScope.AccountID,
		Environment: stripeScope.Environment,
		Purpose:     purpose,
		Reason:      stripe.ReasonCredentialMissing,
		Retryable:   true,
		Detail:      "legacy static Stripe API key is not configured",
	}
}

func selectStripeAPIKey(secrets map[string]string, purpose stripe.Purpose) string {
	var candidates []string
	switch purpose {
	case stripe.PurposePayout:
		candidates = []string{credPurposePayoutAPIKey, credPurposeLedgerAPIKey, credPurposeAPIKey}
	case stripe.PurposeBalanceTransactions:
		candidates = []string{credPurposeLedgerAPIKey, credPurposeAPIKey}
	case stripe.PurposeReportRun:
		candidates = []string{credPurposeReportAPIKey, credPurposeLedgerAPIKey, credPurposeAPIKey}
	case stripe.PurposeFiles:
		candidates = []string{credPurposeFilesAPIKey, credPurposeReportAPIKey, credPurposeLedgerAPIKey, credPurposeAPIKey}
	case stripe.PurposeEvents:
		candidates = []string{credPurposeEventsAPIKey, credPurposeAftercareAPIKey, credPurposeAPIKey}
	case stripe.PurposeAftercare:
		candidates = []string{credPurposeAftercareAPIKey, credPurposeAPIKey}
	default:
		candidates = []string{credPurposeAPIKey}
	}
	for _, key := range candidates {
		if val := strings.TrimSpace(secrets[key]); val != "" {
			return val
		}
	}
	return ""
}

func staticStripeAPIKey(scope stripe.Scope, purpose stripe.Purpose) string {
	switch purpose {
	case stripe.PurposeAftercare, stripe.PurposeEvents:
		return strings.TrimSpace(config.StripeAftercareAPIKey(scope.AccountID, string(scope.Environment)))
	default:
		return strings.TrimSpace(config.StripeLedgerAPIKey(scope.AccountID, string(scope.Environment)))
	}
}
