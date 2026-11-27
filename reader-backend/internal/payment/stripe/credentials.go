package stripe

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	stripego "github.com/stripe/stripe-go/v87"
)

// Purpose names a distinct Stripe API capability so credentials and permissions
// can be resolved, verified, and diagnosed without over-requiring unused scopes.
type Purpose string

const (
	PurposeAccount             Purpose = "account"
	PurposePayout              Purpose = "payout"
	PurposeBalanceTransactions Purpose = "balance_transactions"
	PurposeReportRun           Purpose = "report_run"
	PurposeFiles               Purpose = "files"
	PurposeEvents              Purpose = "events"
	PurposeAftercare           Purpose = "aftercare"
)

// CredentialReason classifies why Stripe credential resolution or authorization failed.
type CredentialReason string

const (
	ReasonAccountNotFound      CredentialReason = "account_not_found"
	ReasonAccountNotReady      CredentialReason = "account_not_ready"
	ReasonAccountMismatch      CredentialReason = "account_mismatch"
	ReasonEnvironmentMismatch  CredentialReason = "environment_mismatch"
	ReasonCredentialMissing    CredentialReason = "credential_missing"
	ReasonCredentialDecrypt    CredentialReason = "credential_decrypt_failed"
	ReasonAuthenticationFailed CredentialReason = "authentication_failed"
	ReasonPermissionDenied     CredentialReason = "permission_denied"
	ReasonUpstreamUnavailable  CredentialReason = "upstream_unavailable"
)

const (
	permissionProbeLimit int64 = 1
	redactedToken              = "[REDACTED]"
)

var (
	stripeSecretPattern = regexp.MustCompile(`\b(?:sk|rk)_(?:test|live)_[A-Za-z0-9_]+\b|\bwhsec_[A-Za-z0-9_]+\b`)
	bearerSecretPattern = regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]+`)
)

// RedactSecrets strips Stripe API keys, webhook secrets, and Bearer tokens from
// error messages and log lines before persistence or display.
func RedactSecrets(message string) string {
	if message == "" {
		return ""
	}
	redacted := bearerSecretPattern.ReplaceAllString(message, "Bearer "+redactedToken)
	return stripeSecretPattern.ReplaceAllString(redacted, redactedToken)
}

// CredentialError is a structured, secret-safe diagnostic error for credential
// resolution, account/environment checks, and per-purpose API permissions.
type CredentialError struct {
	AccountID   string
	Environment Environment
	Purpose     Purpose
	Reason      CredentialReason
	HTTPStatus  int
	RequestID   string
	Retryable   bool
	Detail      string
	Err         error
}

func (e *CredentialError) Error() string {
	var parts []string
	if e.Purpose != "" {
		parts = append(parts, "purpose="+string(e.Purpose))
	}
	if e.Reason != "" {
		parts = append(parts, "reason="+string(e.Reason))
	}
	if e.AccountID != "" {
		parts = append(parts, "account="+e.AccountID)
	}
	if e.Environment != "" {
		parts = append(parts, "env="+string(e.Environment))
	}
	if e.HTTPStatus > 0 {
		parts = append(parts, fmt.Sprintf("http_status=%d", e.HTTPStatus))
	}
	if e.RequestID != "" {
		parts = append(parts, "request="+e.RequestID)
	}
	parts = append(parts, fmt.Sprintf("retryable=%t", e.Retryable))

	summary := "stripe credential error (" + strings.Join(parts, ", ") + ")"
	if detail := RedactSecrets(strings.TrimSpace(e.Detail)); detail != "" {
		summary += ": " + detail
	}
	return summary
}

func (e *CredentialError) Unwrap() error { return e.Err }

// ClassifyPurposeError upgrades Stripe authentication/authorization errors (401/403)
// into a structured CredentialError tagged with the failing purpose while preserving
// the underlying APIError via Unwrap.
func ClassifyPurposeError(scope Scope, purpose Purpose, err error) error {
	if err == nil {
		return nil
	}
	var credErr *CredentialError
	if errors.As(err, &credErr) {
		if credErr.Purpose == "" || (credErr.Purpose == PurposeAccount && purpose != "" && purpose != PurposeAccount) {
			copied := *credErr
			copied.Purpose = purpose
			return &copied
		}
		return err
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		switch apiErr.Status {
		case http.StatusUnauthorized:
			return &CredentialError{
				AccountID:   scope.AccountID,
				Environment: scope.Environment,
				Purpose:     purpose,
				Reason:      ReasonAuthenticationFailed,
				HTTPStatus:  apiErr.Status,
				RequestID:   apiErr.RequestID,
				Retryable:   true,
				Detail:      "Stripe API key authentication failed",
				Err:         err,
			}
		case http.StatusForbidden:
			return &CredentialError{
				AccountID:   scope.AccountID,
				Environment: scope.Environment,
				Purpose:     purpose,
				Reason:      ReasonPermissionDenied,
				HTTPStatus:  apiErr.Status,
				RequestID:   apiErr.RequestID,
				Retryable:   true,
				Detail:      fmt.Sprintf("Stripe API key lacks %s permission", purpose),
				Err:         err,
			}
		}
	}
	return err
}

// VerifyAccount confirms that the client's API key is valid and belongs to c.scope.AccountID.
func (c *Client) VerifyAccount(ctx context.Context) error {
	account, err := c.api.V1Accounts.Retrieve(ctx, nil)
	if err != nil {
		classified := classify(err)
		if purposeErr := ClassifyPurposeError(c.scope, PurposeAccount, classified); purposeErr != classified {
			return purposeErr
		}
		var apiErr *APIError
		status := 0
		reqID := ""
		if errors.As(classified, &apiErr) {
			status = apiErr.Status
			reqID = apiErr.RequestID
		}
		return &CredentialError{
			AccountID:   c.scope.AccountID,
			Environment: c.scope.Environment,
			Purpose:     PurposeAccount,
			Reason:      ReasonUpstreamUnavailable,
			HTTPStatus:  status,
			RequestID:   reqID,
			Retryable:   true,
			Detail:      "Stripe account verification failed",
			Err:         classified,
		}
	}
	if account.ID != c.scope.AccountID {
		return &CredentialError{
			AccountID:   c.scope.AccountID,
			Environment: c.scope.Environment,
			Purpose:     PurposeAccount,
			Reason:      ReasonAccountMismatch,
			Retryable:   true,
			Detail:      "Stripe API key belongs to a different account",
		}
	}
	return nil
}

// VerifyPurpose verifies account ownership and the minimal read-only Stripe API
// permission required for purpose without checking unrelated capabilities.
func (c *Client) VerifyPurpose(ctx context.Context, purpose Purpose) error {
	if err := c.VerifyAccount(ctx); err != nil {
		return ClassifyPurposeError(c.scope, purpose, err)
	}
	probe := stripego.ListParams{Single: true, Limit: stripego.Int64(permissionProbeLimit)}
	var err error
	switch purpose {
	case PurposeAccount:
		return nil
	case PurposePayout:
		_, err = listPage(c.api.V1Payouts.List(ctx, &stripego.PayoutListParams{ListParams: probe}))
	case PurposeBalanceTransactions:
		_, err = listPage(c.api.V1BalanceTransactions.List(ctx, &stripego.BalanceTransactionListParams{ListParams: probe}))
	case PurposeReportRun:
		_, err = listPage(c.api.V1ReportingReportRuns.List(ctx, &stripego.ReportingReportRunListParams{ListParams: probe}))
	case PurposeFiles:
		_, err = listPage(c.api.V1Files.List(ctx, &stripego.FileListParams{ListParams: probe}))
	case PurposeEvents:
		_, err = listPage(c.api.V1Events.List(ctx, &stripego.EventListParams{ListParams: probe}))
	case PurposeAftercare:
		_, err = listPage(c.api.V1Disputes.List(ctx, &stripego.DisputeListParams{ListParams: probe}))
	default:
		return fmt.Errorf("unsupported Stripe credential purpose %q", purpose)
	}
	if err != nil {
		if purposeErr := ClassifyPurposeError(c.scope, purpose, err); purposeErr != err {
			return purposeErr
		}
		return err
	}
	return nil
}
