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
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"star-novel-cms/internal/auth"
	"star-novel-cms/internal/config"
	"star-novel-cms/internal/credentials"
	"star-novel-cms/internal/db"
)

const (
	channelStatusDraft     = "draft"
	channelStatusVerified  = "verified"
	channelStatusPublished = "published"
	channelStatusDisabled  = "disabled"

	validationPending  = "pending"
	validationVerified = "verified"
	validationFailed   = "failed"
	validationReview   = "review"

	credRefDBEncrypted = "db:encrypted"
	webhookRefToken    = "channel_token"

	defaultWebhookGraceHours = 24
	maxWebhookGraceHours     = 168
	channelCheckTimeout      = 10 * time.Second
)

var (
	webhookSecretPattern  = regexp.MustCompile(`^whsec_[A-Za-z0-9_]+$`)
	paypalAPIBaseSandbox  = "https://api-m.sandbox.paypal.com"
	paypalAPIBaseLive     = "https://api-m.paypal.com"
	stripeAPIBaseURL      = "https://api.stripe.com"
	channelHTTPClient     = &http.Client{Timeout: channelCheckTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	errChannelConflict    = errors.New("channel account state conflict")
	errChannelNotFound    = errors.New("payment channel account not found")
	errChannelUnverified  = errors.New("channel credentials must pass verification before publishing")
	errKeyringUnavailable = errors.New("PAYMENT_CREDENTIAL_KEYS must be configured to store or decrypt channel credentials")
)

type channelValidationResult struct {
	ProviderOK     bool   `json:"provider_ok"`
	IdentityOK     bool   `json:"identity_ok"`
	WebhookOK      bool   `json:"webhook_ok"`
	Environment    string `json:"environment"`
	AccountIDMatch string `json:"account_id_match,omitempty"`
	WebhookURL     string `json:"webhook_url,omitempty"`
	CheckedAt      string `json:"checked_at"`
	Error          string `json:"error,omitempty"`
}

type PayPalChannelCreds struct {
	AccountKey        string
	MerchantID        string
	Environment       string
	ClientID          string
	ClientSecret      string
	WebhookID         string
	PreviousWebhookID string
}

type ChannelView struct {
	ID                       int64             `json:"id"`
	Provider                 string            `json:"provider"`
	Environment              string            `json:"environment"`
	DisplayName              string            `json:"display_name"`
	AccountLabel             string            `json:"account_label"`
	AccountKey               string            `json:"account_key"`
	MerchantID               string            `json:"merchant_id,omitempty"`
	Status                   string            `json:"status"`
	IsPrimary                bool              `json:"is_primary"`
	ActiveVersion            int               `json:"active_version"`
	PendingVersion           int               `json:"pending_version"`
	CredentialsRef           string            `json:"credentials_ref"`
	WebhookRef               string            `json:"webhook_ref"`
	WebhookToken             string            `json:"webhook_token"`
	WebhookPath              string            `json:"webhook_path"`
	NewOrdersEnabled         bool              `json:"new_orders_enabled"`
	NewSubscriptionsEnabled  bool              `json:"new_subscriptions_enabled"`
	ValidationStatus         string            `json:"validation_status"`
	ValidationSummary        json.RawMessage   `json:"validation_summary,omitempty"`
	LastValidatedAt          *time.Time        `json:"last_validated_at,omitempty"`
	PublishedAt              *time.Time        `json:"published_at,omitempty"`
	CredentialHints          map[string]string `json:"credential_hints"`
	PreviousWebhookExpiresAt *time.Time        `json:"previous_webhook_expires_at,omitempty"`
	CreatedAt                time.Time         `json:"created_at"`
	UpdatedAt                time.Time         `json:"updated_at"`
}

func maskAccountKey(provider, key string) string {
	if provider == "stripe" && strings.HasPrefix(key, "acct_") {
		return key
	}
	if len(key) > 4 {
		return "••••" + key[len(key)-4:]
	}
	return "••••"
}

func requireChannelWriteTx(ctx context.Context, tx pgx.Tx, actor string) error {
	return requirePermissionTx(ctx, tx, actor, errCaseForbidden, auth.PermPaymentsChannelsWrite)
}

func loadKeyringForWrite() (*credentials.Keyring, string, error) {
	ring, err := credentials.LoadFromEnv()
	if err != nil {
		return nil, "", fmt.Errorf("%w: %v", errKeyringUnavailable, err)
	}
	ver, err := credentials.ActiveKeyVersionFromEnv(ring)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %v", errKeyringUnavailable, err)
	}
	return ring, ver, nil
}

func saveEncryptedCredentialTx(ctx context.Context, tx pgx.Tx, ring *credentials.Keyring, keyVer string, accountID int64, version int, purpose, plaintext, actor string, expiresAt *time.Time) error {
	ct, err := ring.Encrypt(keyVer, plaintext, credentials.CredentialAAD(accountID, version, purpose))
	if err != nil {
		return err
	}
	hint := credentials.MaskSecret(plaintext)
	_, err = tx.Exec(ctx, `
		INSERT INTO payment_channel_credentials (account_id, version, purpose, key_version, ciphertext, hint, expires_at, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (account_id, version, purpose) DO UPDATE SET
			key_version = EXCLUDED.key_version,
			ciphertext = EXCLUDED.ciphertext,
			hint = EXCLUDED.hint,
			expires_at = EXCLUDED.expires_at,
			created_by = EXCLUDED.created_by,
			created_at = now()`,
		accountID, version, purpose, keyVer, ct, hint, expiresAt, actor)
	return err
}

func loadDecryptedCredentials(ctx context.Context, q interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, accountID int64, version int) (map[string]string, map[string]*time.Time, error) {
	ring, err := credentials.LoadFromEnv()
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", errKeyringUnavailable, err)
	}
	rows, err := q.Query(ctx, `
		SELECT purpose, key_version, ciphertext, expires_at
		FROM payment_channel_credentials
		WHERE account_id = $1 AND version = $2`, accountID, version)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	secrets := make(map[string]string)
	expirations := make(map[string]*time.Time)
	for rows.Next() {
		var purpose, keyVer, ct string
		var expiresAt *time.Time
		if err := rows.Scan(&purpose, &keyVer, &ct, &expiresAt); err != nil {
			return nil, nil, err
		}
		pt, err := ring.Decrypt(keyVer, ct, credentials.CredentialAAD(accountID, version, purpose))
		if err != nil {
			return nil, nil, fmt.Errorf("decrypt credential %s: %w", purpose, err)
		}
		secrets[purpose] = pt
		expirations[purpose] = expiresAt
	}
	return secrets, expirations, rows.Err()
}

func loadChannelStripeKey(ctx context.Context, accountID int64, requestedVersion int) (apiKey string, accountKey string, env string, credRef string, err error) {
	var activeVer, pendingVer int
	err = db.DB.QueryRow(ctx, `
		SELECT account_key, environment, credentials_ref, active_version, pending_version
		FROM payment_channel_accounts
		WHERE id = $1 AND provider = 'stripe'`, accountID).
		Scan(&accountKey, &env, &credRef, &activeVer, &pendingVer)
	if err != nil {
		return "", "", "", "", err
	}
	ver := requestedVersion
	if ver <= 0 {
		ver = activeVer
	}
	if credRef == credRefDBEncrypted {
		secrets, _, decErr := loadDecryptedCredentials(ctx, db.DB, accountID, ver)
		if decErr != nil {
			return "", "", "", "", decErr
		}
		if key := secrets["api_key"]; key != "" {
			return key, accountKey, env, credRef, nil
		}
		return "", "", "", "", errors.New("Stripe account has no stored encrypted API key")
	}
	if env == "live" {
		return os.Getenv("STRIPE_CATALOG_LIVE_API_KEY"), accountKey, env, credRef, nil
	}
	return os.Getenv("STRIPE_CATALOG_SANDBOX_API_KEY"), accountKey, env, credRef, nil
}

func loadChannelPayPalCreds(ctx context.Context, accountID int64, requestedVersion int) (PayPalChannelCreds, string, error) {
	var credRef, env, accountKey, merchantID string
	var activeVer int
	err := db.DB.QueryRow(ctx, `
		SELECT account_key, merchant_id, environment, credentials_ref, active_version
		FROM payment_channel_accounts
		WHERE id = $1 AND provider = 'paypal'`, accountID).
		Scan(&accountKey, &merchantID, &env, &credRef, &activeVer)
	if err != nil {
		return PayPalChannelCreds{}, "", err
	}
	creds := PayPalChannelCreds{AccountKey: accountKey, MerchantID: merchantID, Environment: env, ClientID: accountKey}
	ver := requestedVersion
	if ver <= 0 {
		ver = activeVer
	}
	if credRef == credRefDBEncrypted {
		secrets, _, decErr := loadDecryptedCredentials(ctx, db.DB, accountID, ver)
		if decErr != nil {
			return PayPalChannelCreds{}, "", decErr
		}
		if secrets["client_id"] != "" {
			creds.ClientID = secrets["client_id"]
		}
		creds.ClientSecret = secrets["api_secret"]
		creds.WebhookID = secrets["webhook_id"]
		creds.PreviousWebhookID = secrets["previous_webhook_id"]
		return creds, credRef, nil
	}
	if config.AppConfig != nil && (credRef == "env:paypal" || credRef == "legacy:paypal") &&
		(config.AppConfig.PayPalClientID == "" || config.AppConfig.PayPalClientID == accountKey) {
		creds.ClientID = config.AppConfig.PayPalClientID
		creds.ClientSecret = config.AppConfig.PayPalClientSecret
		creds.WebhookID = os.Getenv("PAYPAL_WEBHOOK_ID")
	}
	return creds, credRef, nil
}

func (h *Handler) ListPaymentChannels(c *gin.Context) {
	rows, err := db.DB.Query(c.Request.Context(), `
		SELECT a.id, a.provider, a.environment, COALESCE(NULLIF(a.display_name, ''), a.account_key),
		       a.account_key, a.merchant_id, a.status, a.is_primary, a.active_version, a.pending_version,
		       a.credentials_ref, a.webhook_ref, a.webhook_token::text,
		       a.new_orders_enabled, a.new_subscriptions_enabled,
		       COALESCE(v.validation_status, CASE WHEN a.status IN ('verified','published') THEN 'verified' ELSE 'pending' END),
		       COALESCE(v.validation_summary, a.validation_summary),
		       COALESCE(v.validated_at, a.last_validated_at),
		       a.published_at, a.created_at, a.updated_at,
		       COALESCE((
		           SELECT jsonb_object_agg(c.purpose, c.hint)
		           FROM payment_channel_credentials c
		           WHERE c.account_id = a.id AND c.version = a.pending_version
		       ), '{}'::jsonb),
		       (
		           SELECT max(c.expires_at)
		           FROM payment_channel_credentials c
		           WHERE c.account_id = a.id AND c.version = a.active_version
		             AND c.purpose IN ('previous_webhook_secret', 'previous_webhook_id')
		             AND c.expires_at > now()
		       )
		FROM payment_channel_accounts a
		LEFT JOIN payment_channel_account_versions v
		  ON v.account_id = a.id AND v.version = a.pending_version
		ORDER BY a.provider ASC, a.environment ASC, a.is_primary DESC, a.id ASC`)
	if err != nil {
		writeChannelError(c, err)
		return
	}
	channels, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (ChannelView, error) {
		var ch ChannelView
		var hintsRaw json.RawMessage
		if err := row.Scan(
			&ch.ID, &ch.Provider, &ch.Environment, &ch.DisplayName,
			&ch.AccountKey, &ch.MerchantID, &ch.Status, &ch.IsPrimary, &ch.ActiveVersion, &ch.PendingVersion,
			&ch.CredentialsRef, &ch.WebhookRef, &ch.WebhookToken,
			&ch.NewOrdersEnabled, &ch.NewSubscriptionsEnabled,
			&ch.ValidationStatus, &ch.ValidationSummary, &ch.LastValidatedAt,
			&ch.PublishedAt, &ch.CreatedAt, &ch.UpdatedAt, &hintsRaw, &ch.PreviousWebhookExpiresAt,
		); err != nil {
			return ChannelView{}, err
		}
		ch.AccountLabel = maskAccountKey(ch.Provider, ch.AccountKey)
		ch.WebhookPath = "/api/v1/wallet/webhook/channel/" + ch.WebhookToken
		ch.CredentialHints = map[string]string{}
		if len(hintsRaw) > 0 {
			_ = json.Unmarshal(hintsRaw, &ch.CredentialHints)
		}
		if ch.CredentialsRef != credRefDBEncrypted {
			ch.AccountKey = maskAccountKey(ch.Provider, ch.AccountKey)
		}
		return ch, nil
	})
	if err != nil {
		writeChannelError(c, err)
		return
	}
	if channels == nil {
		channels = []ChannelView{}
	}
	c.JSON(http.StatusOK, gin.H{"channels": channels})
}

type createChannelReq struct {
	Provider      string `json:"provider" binding:"required,oneof=paypal stripe"`
	Environment   string `json:"environment" binding:"required,oneof=sandbox live"`
	DisplayName   string `json:"display_name" binding:"required,max=100"`
	AccountKey    string `json:"account_key" binding:"required,max=160"`
	MerchantID    string `json:"merchant_id" binding:"max=160"`
	ClientSecret  string `json:"client_secret" binding:"max=500"`
	WebhookID     string `json:"webhook_id" binding:"max=200"`
	APIKey        string `json:"api_key" binding:"max=500"`
	WebhookSecret string `json:"webhook_secret" binding:"max=500"`
}

func (h *Handler) CreatePaymentChannel(c *gin.Context) {
	var req createChannelReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	req.DisplayName = strings.TrimSpace(req.DisplayName)
	req.AccountKey = strings.TrimSpace(req.AccountKey)
	req.MerchantID = strings.TrimSpace(req.MerchantID)
	req.ClientSecret = strings.TrimSpace(req.ClientSecret)
	req.WebhookID = strings.TrimSpace(req.WebhookID)
	req.APIKey = strings.TrimSpace(req.APIKey)
	req.WebhookSecret = strings.TrimSpace(req.WebhookSecret)

	if req.Provider == "stripe" {
		if !stripeAccountRef.MatchString(req.AccountKey) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Stripe account_key must match ^acct_[a-zA-Z0-9]+$"})
			return
		}
		if req.APIKey == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Stripe api_key is required"})
			return
		}
		if err := validateStripeSecretFormat(req.Environment, req.APIKey, req.WebhookSecret); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	} else {
		if req.ClientSecret == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "PayPal client_secret is required"})
			return
		}
	}

	ring, keyVer, err := loadKeyringForWrite()
	if err != nil {
		writeChannelError(c, err)
		return
	}

	ctx := c.Request.Context()
	actor := c.GetString("admin_id")
	tx, err := db.DB.Begin(ctx)
	if err != nil {
		writeChannelError(c, err)
		return
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if err := requireChannelWriteTx(ctx, tx, actor); err != nil {
		writeChannelError(c, err)
		return
	}

	var id int64
	var token string
	err = tx.QueryRow(ctx, `
		INSERT INTO payment_channel_accounts (
			provider, environment, account_key, display_name, merchant_id,
			credentials_ref, webhook_ref, status, is_primary, active_version, pending_version,
			new_orders_enabled, new_subscriptions_enabled
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, 'draft', false, 1, 1, false, false
		) RETURNING id, webhook_token::text`,
		req.Provider, req.Environment, req.AccountKey, req.DisplayName, req.MerchantID,
		credRefDBEncrypted, webhookRefToken).Scan(&id, &token)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			c.JSON(http.StatusConflict, gin.H{"error": "A channel account with the same provider, environment and account_key already exists"})
			return
		}
		writeChannelError(c, err)
		return
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO payment_channel_account_versions (
			account_id, version, display_name, merchant_id, account_key, status, validation_status, created_by
		) VALUES ($1, 1, $2, $3, $4, 'draft', 'pending', $5)`,
		id, req.DisplayName, req.MerchantID, req.AccountKey, actor); err != nil {
		writeChannelError(c, err)
		return
	}

	hints := map[string]string{}
	if req.Provider == "stripe" {
		if err := saveEncryptedCredentialTx(ctx, tx, ring, keyVer, id, 1, "api_key", req.APIKey, actor, nil); err != nil {
			writeChannelError(c, err)
			return
		}
		hints["api_key"] = credentials.MaskSecret(req.APIKey)
		if req.WebhookSecret != "" {
			if err := saveEncryptedCredentialTx(ctx, tx, ring, keyVer, id, 1, "webhook_secret", req.WebhookSecret, actor, nil); err != nil {
				writeChannelError(c, err)
				return
			}
			hints["webhook_secret"] = credentials.MaskSecret(req.WebhookSecret)
		}
	} else {
		if err := saveEncryptedCredentialTx(ctx, tx, ring, keyVer, id, 1, "client_id", req.AccountKey, actor, nil); err != nil {
			writeChannelError(c, err)
			return
		}
		if err := saveEncryptedCredentialTx(ctx, tx, ring, keyVer, id, 1, "api_secret", req.ClientSecret, actor, nil); err != nil {
			writeChannelError(c, err)
			return
		}
		hints["client_id"] = credentials.MaskSecret(req.AccountKey)
		hints["api_secret"] = credentials.MaskSecret(req.ClientSecret)
		if req.WebhookID != "" {
			if err := saveEncryptedCredentialTx(ctx, tx, ring, keyVer, id, 1, "webhook_id", req.WebhookID, actor, nil); err != nil {
				writeChannelError(c, err)
				return
			}
			hints["webhook_id"] = credentials.MaskSecret(req.WebhookID)
		}
	}

	auditAfter, _ := json.Marshal(map[string]any{
		"provider": req.Provider, "environment": req.Environment, "display_name": req.DisplayName,
		"account_key": maskAccountKey(req.Provider, req.AccountKey), "hints": hints,
	})
	if err := billingAuditLogTx(ctx, tx, actor, "create_payment_channel", strconv.FormatInt(id, 10), "", string(auditAfter)); err != nil {
		writeChannelError(c, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeChannelError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"id":           id,
		"status":       channelStatusDraft,
		"webhook_path": "/api/v1/wallet/webhook/channel/" + token,
		"hints":        hints,
	})
}

type editChannelReq struct {
	DisplayName             string `json:"display_name" binding:"max=100"`
	MerchantID              string `json:"merchant_id" binding:"max=160"`
	ClientSecret            string `json:"client_secret" binding:"max=500"`
	WebhookID               string `json:"webhook_id" binding:"max=200"`
	APIKey                  string `json:"api_key" binding:"max=500"`
	WebhookSecret           string `json:"webhook_secret" binding:"max=500"`
	RotateWebhookGraceHours *int   `json:"rotate_webhook_grace_hours" binding:"omitempty,gte=0,lte=168"`
}

func (h *Handler) EditPaymentChannel(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid channel account ID"})
		return
	}
	var req editChannelReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	req.DisplayName = strings.TrimSpace(req.DisplayName)
	req.MerchantID = strings.TrimSpace(req.MerchantID)
	req.ClientSecret = strings.TrimSpace(req.ClientSecret)
	req.WebhookID = strings.TrimSpace(req.WebhookID)
	req.APIKey = strings.TrimSpace(req.APIKey)
	req.WebhookSecret = strings.TrimSpace(req.WebhookSecret)

	ctx := c.Request.Context()
	actor := c.GetString("admin_id")
	tx, err := db.DB.Begin(ctx)
	if err != nil {
		writeChannelError(c, err)
		return
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if err := requireChannelWriteTx(ctx, tx, actor); err != nil {
		writeChannelError(c, err)
		return
	}

	var provider, environment, accountKey, oldDisplay, oldMerchant, credRef, status string
	var activeVer, pendingVer int
	err = tx.QueryRow(ctx, `
		SELECT provider, environment, account_key, display_name, merchant_id, credentials_ref, status, active_version, pending_version
		FROM payment_channel_accounts WHERE id = $1 FOR UPDATE`, id).
		Scan(&provider, &environment, &accountKey, &oldDisplay, &oldMerchant, &credRef, &status, &activeVer, &pendingVer)
	if errors.Is(err, pgx.ErrNoRows) {
		writeChannelError(c, errChannelNotFound)
		return
	}
	if err != nil {
		writeChannelError(c, err)
		return
	}

	secretsChanged := req.ClientSecret != "" || req.WebhookID != "" || req.APIKey != "" || req.WebhookSecret != ""
	if secretsChanged && credRef != credRefDBEncrypted {
		credRef = credRefDBEncrypted
	}
	if provider == "stripe" {
		if err := validateStripeSecretFormat(environment, req.APIKey, req.WebhookSecret); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}

	newDisplay := oldDisplay
	if req.DisplayName != "" {
		newDisplay = req.DisplayName
	}
	newMerchant := oldMerchant
	if req.MerchantID != "" {
		newMerchant = req.MerchantID
	}

	targetVersion := pendingVer
	if secretsChanged {
		ring, keyVer, kErr := loadKeyringForWrite()
		if kErr != nil {
			writeChannelError(c, kErr)
			return
		}
		if pendingVer <= activeVer && status != channelStatusDraft {
			targetVersion = activeVer + 1
		}
		existingSecrets, existingExp, decErr := loadDecryptedCredentials(ctx, tx, id, activeVer)
		if decErr != nil {
			existingSecrets = map[string]string{}
			existingExp = map[string]*time.Time{}
		}
		if pendingVer != activeVer {
			pSecrets, pExp, pErr := loadDecryptedCredentials(ctx, tx, id, pendingVer)
			if pErr == nil {
				for k, v := range pSecrets {
					existingSecrets[k] = v
					existingExp[k] = pExp[k]
				}
			}
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO payment_channel_account_versions (
				account_id, version, display_name, merchant_id, account_key, status, validation_status, created_by
			) VALUES ($1, $2, $3, $4, $5, 'draft', 'pending', $6)
			ON CONFLICT (account_id, version) DO UPDATE SET
				display_name = EXCLUDED.display_name,
				merchant_id = EXCLUDED.merchant_id,
				status = 'draft',
				validation_status = 'pending',
				validation_summary = NULL,
				validated_at = NULL,
				updated_at = now()`,
			id, targetVersion, newDisplay, newMerchant, accountKey, actor); err != nil {
			writeChannelError(c, err)
			return
		}

		graceHours := defaultWebhookGraceHours
		if req.RotateWebhookGraceHours != nil {
			graceHours = *req.RotateWebhookGraceHours
		}
		var graceUntil *time.Time
		if graceHours > 0 {
			t := time.Now().UTC().Add(time.Duration(graceHours) * time.Hour)
			graceUntil = &t
		}

		merged := map[string]string{}
		expirations := map[string]*time.Time{}
		for k, v := range existingSecrets {
			merged[k] = v
			expirations[k] = existingExp[k]
		}
		if provider == "stripe" {
			if req.APIKey != "" {
				merged["api_key"] = req.APIKey
			}
			if req.WebhookSecret != "" {
				if prev := existingSecrets["webhook_secret"]; prev != "" && prev != req.WebhookSecret && graceUntil != nil {
					merged["previous_webhook_secret"] = prev
					expirations["previous_webhook_secret"] = graceUntil
				}
				merged["webhook_secret"] = req.WebhookSecret
			}
		} else {
			if merged["client_id"] == "" {
				merged["client_id"] = accountKey
			}
			if req.ClientSecret != "" {
				merged["api_secret"] = req.ClientSecret
			}
			if req.WebhookID != "" {
				if prev := existingSecrets["webhook_id"]; prev != "" && prev != req.WebhookID && graceUntil != nil {
					merged["previous_webhook_id"] = prev
					expirations["previous_webhook_id"] = graceUntil
				}
				merged["webhook_id"] = req.WebhookID
			}
		}

		for purpose, val := range merged {
			if val == "" {
				continue
			}
			if err := saveEncryptedCredentialTx(ctx, tx, ring, keyVer, id, targetVersion, purpose, val, actor, expirations[purpose]); err != nil {
				writeChannelError(c, err)
				return
			}
		}
	} else {
		_, err = tx.Exec(ctx, `
			UPDATE payment_channel_account_versions
			SET display_name = $3, merchant_id = $4, updated_at = now()
			WHERE account_id = $1 AND version = $2`,
			id, targetVersion, newDisplay, newMerchant)
		if err != nil {
			writeChannelError(c, err)
			return
		}
	}

	if _, err := tx.Exec(ctx, `
		UPDATE payment_channel_accounts
		SET display_name = $2, merchant_id = $3, credentials_ref = $4, pending_version = $5, updated_at = now()
		WHERE id = $1`, id, newDisplay, newMerchant, credRef, targetVersion); err != nil {
		writeChannelError(c, err)
		return
	}

	beforeJSON, _ := json.Marshal(map[string]any{"display_name": oldDisplay, "merchant_id": oldMerchant, "pending_version": pendingVer})
	afterJSON, _ := json.Marshal(map[string]any{"display_name": newDisplay, "merchant_id": newMerchant, "pending_version": targetVersion, "secrets_updated": secretsChanged})
	if err := billingAuditLogTx(ctx, tx, actor, "edit_payment_channel", strconv.FormatInt(id, 10), string(beforeJSON), string(afterJSON)); err != nil {
		writeChannelError(c, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeChannelError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": id, "pending_version": targetVersion, "validation_required": secretsChanged})
}

func (h *Handler) ValidatePaymentChannel(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid channel account ID"})
		return
	}
	ctx := c.Request.Context()
	actor := c.GetString("admin_id")
	if err := verifyContextPermissions(c, errCaseForbidden, auth.PermPaymentsChannelsWrite); err != nil {
		writeChannelError(c, err)
		return
	}

	var provider, environment, accountKey, credRef string
	var pendingVer int
	err = db.DB.QueryRow(ctx, `
		SELECT provider, environment, account_key, credentials_ref, pending_version
		FROM payment_channel_accounts
		WHERE id = $1`,
		id).Scan(&provider, &environment, &accountKey, &credRef, &pendingVer)
	if errors.Is(err, pgx.ErrNoRows) {
		writeChannelError(c, errChannelNotFound)
		return
	}
	if err != nil {
		writeChannelError(c, err)
		return
	}

	// Execute provider verification outside DB transactions.
	var summary channelValidationResult
	var checkErr error
	if provider == "stripe" {
		apiKey, _, _, _, kErr := loadChannelStripeKey(ctx, id, pendingVer)
		var webhookSecret string
		if credRef == credRefDBEncrypted {
			if sec, _, dErr := loadDecryptedCredentials(ctx, db.DB, id, pendingVer); dErr == nil {
				webhookSecret = sec["webhook_secret"]
			}
		} else {
			webhookSecret = os.Getenv("STRIPE_WEBHOOK_SECRET")
		}
		if kErr != nil {
			checkErr = kErr
		} else {
			summary, checkErr = verifyStripeChannelRemote(ctx, accountKey, environment, apiKey, webhookSecret)
		}
	} else {
		creds, _, pErr := loadChannelPayPalCreds(ctx, id, pendingVer)
		if pErr != nil {
			checkErr = pErr
		} else {
			summary, checkErr = verifyPayPalChannelRemote(ctx, creds)
		}
	}

	validationState := validationVerified
	if checkErr != nil {
		validationState = validationFailed
		summary.Error = checkErr.Error()
	}
	summary.Environment = environment
	summary.CheckedAt = time.Now().UTC().Format(time.RFC3339)
	summaryJSON, _ := json.Marshal(summary)

	tx, err := db.DB.Begin(ctx)
	if err != nil {
		writeChannelError(c, err)
		return
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if err := requireChannelWriteTx(ctx, tx, actor); err != nil {
		writeChannelError(c, err)
		return
	}
	if _, err := tx.Exec(ctx, `
		UPDATE payment_channel_account_versions
		SET validation_status = $3,
		    validation_summary = $4::jsonb,
		    validated_at = now(),
		    status = CASE WHEN $3 = 'verified' AND status = 'draft' THEN 'verified' ELSE status END,
		    updated_at = now()
		WHERE account_id = $1 AND version = $2`, id, pendingVer, validationState, string(summaryJSON)); err != nil {
		writeChannelError(c, err)
		return
	}
	if _, err := tx.Exec(ctx, `
		UPDATE payment_channel_accounts
		SET status = CASE WHEN $2 = 'verified' AND status = 'draft' THEN 'verified' ELSE status END,
		    last_validated_at = now(),
		    validation_summary = $3::jsonb,
		    updated_at = now()
		WHERE id = $1`, id, validationState, string(summaryJSON)); err != nil {
		writeChannelError(c, err)
		return
	}
	if err := billingAuditLogTx(ctx, tx, actor, "validate_payment_channel", strconv.FormatInt(id, 10), "", string(summaryJSON)); err != nil {
		writeChannelError(c, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeChannelError(c, err)
		return
	}

	if checkErr != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"verified": false, "summary": summary, "error": checkErr.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"verified": true, "summary": summary, "version": pendingVer})
}

type publishChannelReq struct {
	SetPrimary                     *bool `json:"set_primary"`
	NewOrders                      *bool `json:"new_orders_enabled"`
	NewSubscriptions               *bool `json:"new_subscriptions_enabled"`
	ExpectedVersion                int   `json:"expected_version"`
	ForceSwitchWhenProductsUnready bool  `json:"force_switch_when_products_unready"`
}

func (h *Handler) PublishPaymentChannel(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid channel account ID"})
		return
	}
	var req publishChannelReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()
	actor := c.GetString("admin_id")
	tx, err := db.DB.Begin(ctx)
	if err != nil {
		writeChannelError(c, err)
		return
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if err := requireChannelWriteTx(ctx, tx, actor); err != nil {
		writeChannelError(c, err)
		return
	}

	var provider, environment, credRef, oldStatus, vStatus string
	var oldPrimary, oldOrders, oldSubs bool
	var activeVer, pendingVer int
	err = tx.QueryRow(ctx, `
		SELECT a.provider, a.environment, a.credentials_ref, a.status, a.is_primary,
		       a.new_orders_enabled, a.new_subscriptions_enabled, a.active_version, a.pending_version,
		       COALESCE(v.validation_status, 'pending')
		FROM payment_channel_accounts a
		LEFT JOIN payment_channel_account_versions v ON v.account_id = a.id AND v.version = a.pending_version
		WHERE a.id = $1 FOR UPDATE OF a`, id).
		Scan(&provider, &environment, &credRef, &oldStatus, &oldPrimary, &oldOrders, &oldSubs, &activeVer, &pendingVer, &vStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		writeChannelError(c, errChannelNotFound)
		return
	}
	if err != nil {
		writeChannelError(c, err)
		return
	}
	if req.ExpectedVersion > 0 && req.ExpectedVersion != pendingVer {
		c.JSON(http.StatusConflict, gin.H{"error": "Channel version changed concurrently; refresh and re-validate before publishing"})
		return
	}
	if vStatus != validationVerified {
		c.JSON(http.StatusConflict, gin.H{"error": errChannelUnverified.Error()})
		return
	}

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('channel-primary:'||$1||':'||$2, 0))`, provider, environment); err != nil {
		writeChannelError(c, err)
		return
	}

	setPrimary := true
	if req.SetPrimary != nil {
		setPrimary = *req.SetPrimary
	}
	newOrders := true
	if req.NewOrders != nil {
		newOrders = *req.NewOrders
	}
	newSubs := true
	if req.NewSubscriptions != nil {
		newSubs = *req.NewSubscriptions
	}

	if setPrimary {
		if !req.ForceSwitchWhenProductsUnready {
			type unreadyProduct struct {
				ID   int64  `json:"id"`
				Code string `json:"code"`
				Name string `json:"name"`
			}
			var unreadyQuery string
			if provider == "paypal" {
				unreadyQuery = `
					SELECT p.id, p.code, p.name
					FROM product_publications pub
					JOIN products p ON p.id = pub.product_id
					WHERE pub.environment = $2 AND pub.status = 'published'
					  AND NOT (
					      CASE WHEN p.product_type = 'vip' THEN $4::bool ELSE $3::bool END
					      AND EXISTS (
					          SELECT 1 FROM product_channel_bindings b
					          WHERE b.product_id = p.id AND b.account_id = $1
					            AND b.provider = 'paypal' AND b.environment = $2
					            AND b.enabled = true AND b.validation_status = 'verified'
					            AND (p.product_type <> 'vip' OR b.external_price_ref <> '')
					      )
					  )
					ORDER BY p.id ASC`
			} else {
				unreadyQuery = `
					SELECT p.id, p.code, p.name
					FROM product_publications pub
					JOIN products p ON p.id = pub.product_id
					WHERE pub.environment = $2 AND pub.status = 'published'
					  AND NOT (
					      CASE WHEN p.product_type = 'vip' THEN $4::bool ELSE $3::bool END
					      AND EXISTS (
					          SELECT 1 FROM product_channel_bindings b
					          WHERE b.product_id = p.id AND b.account_id = $1
					            AND b.provider = 'stripe' AND b.environment = $2
					            AND b.enabled = true AND b.validation_status = 'verified'
					            AND b.external_product_ref <> '' AND b.external_price_ref <> ''
					            AND ($2 = 'sandbox' OR (b.validated_catalog IS NOT NULL AND b.release_evidence IS NOT NULL AND b.market_policy IS NOT NULL))
					      )
					  )
					ORDER BY p.id ASC`
			}
			uRows, err := tx.Query(ctx, unreadyQuery, id, environment, newOrders, newSubs)
			if err != nil {
				writeChannelError(c, err)
				return
			}
			var unready []unreadyProduct
			for uRows.Next() {
				var item unreadyProduct
				if err := uRows.Scan(&item.ID, &item.Code, &item.Name); err != nil {
					uRows.Close()
					writeChannelError(c, err)
					return
				}
				unready = append(unready, item)
			}
			if err := uRows.Err(); err != nil {
				uRows.Close()
				writeChannelError(c, err)
				return
			}
			uRows.Close()
			if len(unready) > 0 {
				c.JSON(http.StatusConflict, gin.H{
					"error":                      "Target account is missing verified bindings for published products; pre-bind products before switching primary or pass force_switch_when_products_unready=true",
					"unready_published_products": unready,
				})
				return
			}
		}
		if _, err := tx.Exec(ctx, `
			UPDATE payment_channel_accounts
			SET is_primary = false, updated_at = now()
			WHERE provider = $1 AND environment = $2 AND is_primary = true AND id <> $3`,
			provider, environment, id); err != nil {
			writeChannelError(c, err)
			return
		}
	}

	if _, err := tx.Exec(ctx, `
		UPDATE payment_channel_account_versions
		SET status = CASE WHEN version = $2 THEN 'published' WHEN status = 'published' THEN 'superseded' ELSE status END,
		    published_by = CASE WHEN version = $2 THEN $3 ELSE published_by END,
		    published_at = CASE WHEN version = $2 THEN now() ELSE published_at END,
		    updated_at = now()
		WHERE account_id = $1`, id, pendingVer, actor); err != nil {
		writeChannelError(c, err)
		return
	}

	if _, err := tx.Exec(ctx, `
		UPDATE payment_channel_accounts
		SET status = 'published',
		    active_version = $2,
		    is_primary = $3,
		    new_orders_enabled = $4,
		    new_subscriptions_enabled = $5,
		    published_at = now(),
		    published_by = $6,
		    updated_at = now()
		WHERE id = $1`, id, pendingVer, setPrimary, newOrders, newSubs, actor); err != nil {
		writeChannelError(c, err)
		return
	}

	beforeJSON, _ := json.Marshal(map[string]any{"status": oldStatus, "is_primary": oldPrimary, "active_version": activeVer, "new_orders": oldOrders, "new_subscriptions": oldSubs})
	afterJSON, _ := json.Marshal(map[string]any{"status": channelStatusPublished, "is_primary": setPrimary, "active_version": pendingVer, "new_orders": newOrders, "new_subscriptions": newSubs})
	if err := billingAuditLogTx(ctx, tx, actor, "publish_payment_channel", strconv.FormatInt(id, 10), string(beforeJSON), string(afterJSON)); err != nil {
		writeChannelError(c, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeChannelError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"published": true, "active_version": pendingVer, "is_primary": setPrimary})
}

func (h *Handler) DeactivatePaymentChannel(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid channel account ID"})
		return
	}
	ctx := c.Request.Context()
	actor := c.GetString("admin_id")
	tx, err := db.DB.Begin(ctx)
	if err != nil {
		writeChannelError(c, err)
		return
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if err := requireChannelWriteTx(ctx, tx, actor); err != nil {
		writeChannelError(c, err)
		return
	}

	var oldStatus string
	var oldPrimary, oldOrders, oldSubs bool
	err = tx.QueryRow(ctx, `
		SELECT status, is_primary, new_orders_enabled, new_subscriptions_enabled
		FROM payment_channel_accounts WHERE id = $1 FOR UPDATE`, id).
		Scan(&oldStatus, &oldPrimary, &oldOrders, &oldSubs)
	if errors.Is(err, pgx.ErrNoRows) {
		writeChannelError(c, errChannelNotFound)
		return
	}
	if err != nil {
		writeChannelError(c, err)
		return
	}

	if _, err := tx.Exec(ctx, `
		UPDATE payment_channel_accounts
		SET status = 'disabled', is_primary = false,
		    new_orders_enabled = false, new_subscriptions_enabled = false, updated_at = now()
		WHERE id = $1`, id); err != nil {
		writeChannelError(c, err)
		return
	}
	beforeJSON, _ := json.Marshal(map[string]any{"status": oldStatus, "is_primary": oldPrimary, "new_orders": oldOrders, "new_subscriptions": oldSubs})
	afterJSON, _ := json.Marshal(map[string]any{"status": channelStatusDisabled, "is_primary": false, "new_orders": false, "new_subscriptions": false})
	if err := billingAuditLogTx(ctx, tx, actor, "deactivate_payment_channel", strconv.FormatInt(id, 10), string(beforeJSON), string(afterJSON)); err != nil {
		writeChannelError(c, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeChannelError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"deactivated": true})
}

func validateStripeSecretFormat(environment, apiKey, webhookSecret string) error {
	if apiKey != "" {
		wantPrefix := "test_"
		if environment == "live" {
			wantPrefix = "live_"
		}
		if !strings.HasPrefix(apiKey, "sk_"+wantPrefix) && !strings.HasPrefix(apiKey, "rk_"+wantPrefix) {
			return fmt.Errorf("Stripe API key prefix must match %s environment (sk_%s* or rk_%s*)", environment, wantPrefix, wantPrefix)
		}
	}
	if webhookSecret != "" && !webhookSecretPattern.MatchString(webhookSecret) {
		return errors.New("Stripe webhook_secret must start with whsec_")
	}
	return nil
}

func verifyStripeChannelRemote(ctx context.Context, accountKey, environment, apiKey, webhookSecret string) (channelValidationResult, error) {
	res := channelValidationResult{Environment: environment}
	if err := validateStripeSecretFormat(environment, apiKey, webhookSecret); err != nil {
		return res, err
	}
	if webhookSecret == "" {
		return res, errors.New("Stripe webhook_secret is required before publishing")
	}
	res.WebhookOK = true

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, stripeAPIBaseURL+"/v1/account", nil)
	if err != nil {
		return res, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Stripe-Version", stripeCatalogAPIVersion)
	resp, err := channelHTTPClient.Do(req)
	if err != nil {
		return res, fmt.Errorf("Stripe API unavailable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return res, fmt.Errorf("Stripe API returned status %d", resp.StatusCode)
	}
	var acct struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&acct); err != nil {
		return res, errors.New("invalid Stripe account API response")
	}
	if acct.ID != accountKey {
		return res, fmt.Errorf("Stripe API key belongs to %s, expected %s", acct.ID, accountKey)
	}
	res.ProviderOK = true
	res.IdentityOK = true
	res.AccountIDMatch = acct.ID
	return res, nil
}

func paypalBaseForEnv(env string) string {
	if env == "live" {
		return paypalAPIBaseLive
	}
	return paypalAPIBaseSandbox
}

func fetchPayPalAccessToken(ctx context.Context, baseURL, clientID, clientSecret string) (string, error) {
	form := url.Values{"grant_type": {"client_credentials"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/v1/oauth2/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(clientID, clientSecret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := channelHTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("PayPal OAuth unavailable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("PayPal OAuth returned status %d", resp.StatusCode)
	}
	var tokenResp struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&tokenResp); err != nil || tokenResp.AccessToken == "" {
		return "", errors.New("invalid PayPal OAuth response")
	}
	return tokenResp.AccessToken, nil
}

func verifyPayPalChannelRemote(ctx context.Context, creds PayPalChannelCreds) (channelValidationResult, error) {
	res := channelValidationResult{Environment: creds.Environment}
	if creds.ClientID == "" || creds.ClientSecret == "" {
		return res, errors.New("PayPal client_id and client_secret are required")
	}
	if creds.WebhookID == "" {
		return res, errors.New("PayPal webhook_id is required before publishing")
	}
	baseURL := paypalBaseForEnv(creds.Environment)
	token, err := fetchPayPalAccessToken(ctx, baseURL, creds.ClientID, creds.ClientSecret)
	if err != nil {
		return res, err
	}
	res.ProviderOK = true
	res.IdentityOK = true

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/v1/notifications/webhooks/"+url.PathEscape(creds.WebhookID), nil)
	if err != nil {
		return res, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := channelHTTPClient.Do(req)
	if err != nil {
		return res, fmt.Errorf("PayPal webhook verification unavailable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return res, fmt.Errorf("PayPal webhook lookup returned status %d", resp.StatusCode)
	}
	var wh struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&wh); err != nil || wh.ID != creds.WebhookID {
		return res, errors.New("PayPal webhook ID does not match app configuration")
	}
	res.WebhookOK = true
	res.WebhookURL = wh.URL
	return res, nil
}

func billingAuditLogTx(ctx context.Context, tx pgx.Tx, actor, action, targetID, beforeData, afterData string) error {
	var bArg, aArg any
	if beforeData != "" {
		bArg = beforeData
	}
	if afterData != "" {
		aArg = afterData
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO admin_audit_logs (admin_id, action, target_id, before_data, after_data)
		VALUES ($1, $2, $3, $4::jsonb, $5::jsonb)`,
		actor, action, targetID, bArg, aArg)
	return err
}

func writeChannelError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, errCaseForbidden), errors.Is(err, ErrRefundForbidden):
		c.JSON(http.StatusForbidden, gin.H{"error": "Payment channel permission required"})
	case errors.Is(err, errChannelNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.Is(err, errChannelConflict), errors.Is(err, errChannelUnverified):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, errKeyringUnavailable):
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}
