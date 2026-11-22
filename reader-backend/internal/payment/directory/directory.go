package directory

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"reader-backend/internal/conversion"
	"reader-backend/internal/credentials"
	"reader-backend/internal/payment"
	"reader-backend/internal/payment/paypal"
	"reader-backend/internal/payment/stripe"
)

const queryTimeout = 3 * time.Second

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
	pool           *pgxpool.Pool
	staticAdapters payment.Adapters

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
		log.Printf("channel directory primary resolve failed provider=%s account=%d: %v", provider, row.id, err)
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
			APIKey:                secrets["api_key"],
			WebhookSecret:         secrets["webhook_secret"],
			PreviousWebhookSecret: secrets["previous_webhook_secret"],
		})
	default:
		return nil, fmt.Errorf("unsupported provider %q", row.provider)
	}
}
