package stripe

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestVerifyPurposeChecksOnlyRequestedCapability(t *testing.T) {
	cases := []struct {
		purpose  Purpose
		wantPath string
	}{
		{PurposePayout, "/v1/payouts"},
		{PurposeBalanceTransactions, "/v1/balance_transactions"},
		{PurposeReportRun, "/v1/reporting/report_runs"},
		{PurposeFiles, "/v1/files"},
		{PurposeEvents, "/v1/events"},
		{PurposeAftercare, "/v1/disputes"},
	}

	for _, tc := range cases {
		t.Run("allowed_"+string(tc.purpose), func(t *testing.T) {
			var paths []string
			client := fixtureClient(t, func(req *http.Request) (*http.Response, error) {
				paths = append(paths, req.URL.Path)
				if req.URL.Path != tc.wantPath {
					t.Fatalf("purpose %s touched unexpected endpoint %s", tc.purpose, req.URL.Path)
				}
				return stripeResponse(200, `{"object":"list","data":[],"has_more":false}`)
			})
			if err := client.VerifyPurpose(context.Background(), tc.purpose); err != nil {
				t.Fatalf("VerifyPurpose(%s) failed: %v", tc.purpose, err)
			}
			if len(paths) != 1 || paths[0] != tc.wantPath {
				t.Fatalf("expected exactly 1 probe to %s, got %v", tc.wantPath, paths)
			}
		})

		t.Run("forbidden_"+string(tc.purpose), func(t *testing.T) {
			client := fixtureClient(t, func(req *http.Request) (*http.Response, error) {
				if req.URL.Path != tc.wantPath {
					t.Fatalf("purpose %s touched unexpected endpoint %s", tc.purpose, req.URL.Path)
				}
				return stripeResponse(403, `{"error":{"type":"invalid_request_error","message":"The provided key 'rk_test_secret123456' does not have the required permissions"}}`)
			})
			err := client.VerifyPurpose(context.Background(), tc.purpose)
			var credErr *CredentialError
			if !errors.As(err, &credErr) {
				t.Fatalf("expected CredentialError, got %T: %v", err, err)
			}
			if credErr.Purpose != tc.purpose || credErr.Reason != ReasonPermissionDenied || credErr.HTTPStatus != 403 || !credErr.Retryable {
				t.Fatalf("unexpected CredentialError: %+v", credErr)
			}
			if strings.Contains(err.Error(), "rk_test_") || strings.Contains(err.Error(), "sk_test_") {
				t.Fatalf("error leaked secret: %s", err.Error())
			}
		})
	}
}

func TestOpenReportCSVFilesPermissionMissingRecordsPurpose(t *testing.T) {
	client := fixtureClient(t, func(req *http.Request) (*http.Response, error) {
		switch req.URL.Host + req.URL.Path {
		case "api.stripe.com/v1/reporting/report_runs/frr_perm":
			return stripeResponse(200, `{"id":"frr_perm","status":"succeeded","report_type":"payout_reconciliation.itemized.7","result":{"id":"file_perm","type":"csv","purpose":"finance_report_run","url":"https://files.stripe.com/v1/files/file_perm/contents"}}`)
		case "files.stripe.com/v1/files/file_perm/contents":
			return stripeResponse(403, `{"error":{"type":"invalid_request_error","message":"Missing files permission for rk_test_leak"}}`)
		default:
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL)
			return nil, nil
		}
	})

	report, err := client.OpenReportCSV(context.Background(), "frr_perm")
	if report != nil {
		_ = report.Close()
		t.Fatal("expected nil report on 403")
	}
	var credErr *CredentialError
	if !errors.As(err, &credErr) {
		t.Fatalf("expected CredentialError, got %T: %v", err, err)
	}
	if credErr.Purpose != PurposeFiles || credErr.Reason != ReasonPermissionDenied || credErr.HTTPStatus != 403 || !credErr.Retryable {
		t.Fatalf("unexpected CredentialError: %+v", credErr)
	}
	if strings.Contains(err.Error(), "sk_test_") || strings.Contains(err.Error(), "rk_test_") {
		t.Fatalf("error leaked secret: %s", err.Error())
	}
}

func TestRedactSecretsMasksStripeCredentials(t *testing.T) {
	raw := "failed with Authorization: Bearer sk_live_abc123XYZ and rk_test_999888 and whsec_secret_value"
	redacted := RedactSecrets(raw)
	for _, secret := range []string{"sk_live_abc123XYZ", "rk_test_999888", "whsec_secret_value"} {
		if strings.Contains(redacted, secret) {
			t.Fatalf("redacted output still contains %q: %s", secret, redacted)
		}
	}
	if !strings.Contains(redacted, "[REDACTED]") {
		t.Fatalf("expected redaction marker in %q", redacted)
	}

	credErr := &CredentialError{
		AccountID:   "acct_merchant",
		Environment: Sandbox,
		Purpose:     PurposeFiles,
		Reason:      ReasonPermissionDenied,
		HTTPStatus:  403,
		Retryable:   true,
		Detail:      "rejected key sk_test_do_not_leak with Bearer rk_test_also_hidden",
	}
	msg := credErr.Error()
	if strings.Contains(msg, "sk_test_do_not_leak") || strings.Contains(msg, "rk_test_also_hidden") {
		t.Fatalf("CredentialError leaked secret: %s", msg)
	}
	if !strings.Contains(msg, "purpose=files") || !strings.Contains(msg, "reason=permission_denied") {
		t.Fatalf("CredentialError missing structured fields: %s", msg)
	}
}

func TestClientAccountAndEnvironmentMismatchReturnCredentialError(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripper(func(req *http.Request) (*http.Response, error) {
		return stripeResponse(200, `{"id":"acct_wrong"}`)
	})}

	envCfg := testConfig
	envCfg.APIKey = "sk_live_wrong_env"
	envCfg.HTTPClient = httpClient
	_, err := NewClient(context.Background(), envCfg)
	var credErr *CredentialError
	if !errors.As(err, &credErr) || credErr.Reason != ReasonEnvironmentMismatch {
		t.Fatalf("expected environment mismatch CredentialError, got %v", err)
	}

	acctCfg := testConfig
	acctCfg.HTTPClient = httpClient
	_, err = NewClient(context.Background(), acctCfg)
	if !errors.As(err, &credErr) || credErr.Reason != ReasonAccountMismatch {
		t.Fatalf("expected account mismatch CredentialError, got %v", err)
	}
}
