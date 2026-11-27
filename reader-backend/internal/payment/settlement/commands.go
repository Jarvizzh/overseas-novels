package settlement

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"reader-backend/internal/payment"
	"reader-backend/internal/payment/paypal/credentialstore"
	"reader-backend/internal/payment/stripe"
)

type settlementJob struct {
	id, kind, provider, operator, reason string
	account                              payment.WebhookAccount
	token                                uuid.UUID
	parameters                           []byte
	upload                               []byte
}

type settlementParameters struct {
	Reference         string   `json:"reference"`
	BankAccount       string   `json:"bank_account"`
	BankTransaction   string   `json:"bank_transaction"`
	BankEvidence      string   `json:"bank_evidence"`
	ExpectedColumns   []string `json:"expected_columns,omitempty"`
	RequireMoRColumns bool     `json:"require_mor_columns,omitempty"`
}

func reportImportCommandID(account payment.WebhookAccount, runID string) uuid.UUID {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("stripe-report-import:"+account.Environment+":"+account.AccountKey+":"+runID))
}

// ScheduleStripeNotice uses the same durable import queue as Finance. Enabled
// accounts can process a provider event once even if inbox completion is retried.
// For Stripe Report Runs (frr_*), the command ID is keyed by (account, environment,
// report_run_id) so duplicate webhook deliveries and poll completions enqueue at most once.
func (r *Repository) ScheduleStripeNotice(ctx context.Context, account payment.WebhookAccount, eventID, reference string) error {
	if strings.HasPrefix(reference, "frr_") {
		return r.scheduleStripeReportNotice(ctx, account, eventID, reference)
	}
	if !strings.HasPrefix(reference, "po_") {
		return errors.New("invalid Stripe settlement notification reference")
	}
	id := uuid.NewSHA1(uuid.NameSpaceOID, []byte("stripe-settlement:"+account.Environment+":"+account.AccountKey+":"+eventID))
	_, err := r.pool.Exec(ctx, `INSERT INTO payment_settlement_commands
		(id,request_key,actor_id,kind,account_id,environment,reason,parameters,request_digest)
		SELECT $1,$1,s.actor_id,$2,a.id,a.environment,'Stripe event '||$3::text,
			jsonb_build_object('reference',$4::text,'source_event',$3::text),$3::text
		FROM payment_channel_accounts a JOIN payment_ledger_sync_settings s ON s.account_id=a.id AND s.enabled
		JOIN admins u ON u.id = s.actor_id AND public.admin_has_permission(u.id, 'payments.reconciliation:write')
		WHERE a.provider='stripe' AND a.account_key=$5 AND a.environment=$6 ON CONFLICT DO NOTHING`,
		id, StripePayout, eventID, reference, account.AccountKey, account.Environment)
	return err
}

func (r *Repository) scheduleStripeReportNotice(ctx context.Context, account payment.WebhookAccount, eventID, runID string) error {
	cmdID := reportImportCommandID(account, runID)
	paramsMap := map[string]any{
		"reference":    runID,
		"source_event": eventID,
	}
	var expectedCols []string
	var includeMoR bool
	err := r.pool.QueryRow(ctx, `SELECT r.expected_columns, r.include_mor_columns
		FROM payment_stripe_report_runs r
		JOIN payment_channel_accounts a ON a.id = r.account_id
		WHERE a.provider='stripe' AND a.account_key=$1 AND r.environment=$2 AND r.report_run_id=$3`,
		account.AccountKey, account.Environment, runID).Scan(&expectedCols, &includeMoR)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if len(expectedCols) > 0 {
		paramsMap["expected_columns"] = expectedCols
	}
	if includeMoR {
		paramsMap["require_mor_columns"] = true
	}
	paramsJSON, err := json.Marshal(paramsMap)
	if err != nil {
		return err
	}
	_, err = r.pool.Exec(ctx, `INSERT INTO payment_settlement_commands
		(id,request_key,actor_id,kind,account_id,environment,reason,parameters,request_digest)
		SELECT $1,$1,s.actor_id,$2,a.id,a.environment,'Stripe event '||$3::text,
			$4::jsonb,$5::text
		FROM payment_channel_accounts a JOIN payment_ledger_sync_settings s ON s.account_id=a.id AND s.enabled
		JOIN admins u ON u.id = s.actor_id AND public.admin_has_permission(u.id, 'payments.reconciliation:write')
		WHERE a.provider='stripe' AND a.account_key=$6 AND a.environment=$7 ON CONFLICT DO NOTHING`,
		cmdID, StripeReport, eventID, paramsJSON, runID, account.AccountKey, account.Environment)
	if err != nil {
		return err
	}
	_, err = r.pool.Exec(ctx, `UPDATE payment_stripe_report_runs r
		SET status=$4, import_command_id=COALESCE(r.import_command_id, $5),
			last_error=NULL, lease_token=NULL, lease_until=NULL, updated_at=now()
		FROM payment_channel_accounts a
		WHERE r.account_id=a.id AND a.provider='stripe' AND a.account_key=$1 AND r.environment=$2
			AND r.report_run_id=$3 AND EXISTS (SELECT 1 FROM payment_settlement_commands c WHERE c.id=$5)`,
		account.AccountKey, account.Environment, runID, ReportRunStatusEnqueued, cmdID)
	return err
}

func (r *Repository) ExecuteNextCommand(ctx context.Context, environment string) (bool, error) {
	job, err := r.claimSettlementCommand(ctx, environment)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	result, issue := r.executeSettlementCommand(ctx, job)
	if ctx.Err() != nil {
		return true, ctx.Err()
	}
	var body []byte
	status, note := "succeeded", ""
	if issue != nil {
		status, note = "failed", stripe.RedactSecrets(issue.Error())
	} else if body, err = json.Marshal(result); err != nil {
		return true, err
	}
	_, err = r.pool.Exec(ctx, `UPDATE payment_settlement_commands SET status=$3,error=NULLIF(left($4,1000),''),result=$5::jsonb,
		upload=NULL,lease_until=NULL,lease_token=NULL,updated_at=now() WHERE id=$1 AND lease_token=$2`, job.id, job.token, status, note, body)
	return true, err
}

func (r *Repository) claimSettlementCommand(ctx context.Context, environment string) (settlementJob, error) {
	var job settlementJob
	job.token = uuid.New()
	err := r.pool.QueryRow(ctx, `WITH due AS (
		SELECT c.id FROM payment_settlement_commands c WHERE c.environment=$1 AND
		  (c.status='pending' OR (c.status='running' AND c.lease_until<=now()))
		ORDER BY c.created_at,c.id FOR UPDATE OF c SKIP LOCKED LIMIT 1)
		UPDATE payment_settlement_commands c SET status='running',lease_token=$2,lease_until=now()+interval '10 minutes',updated_at=now()
		FROM due WHERE c.id=due.id RETURNING c.id::text,c.kind,c.actor_id,c.reason,c.environment,c.parameters,c.upload,
		  COALESCE((SELECT provider FROM payment_channel_accounts a WHERE a.id=c.account_id AND a.environment=c.environment),''),
		  COALESCE((SELECT account_key FROM payment_channel_accounts a WHERE a.id=c.account_id AND a.environment=c.environment),'')`, environment, job.token).
		Scan(&job.id, &job.kind, &job.operator, &job.reason, &job.account.Environment, &job.parameters, &job.upload, &job.provider, &job.account.AccountKey)
	return job, err
}

func (r *Repository) executeSettlementCommand(ctx context.Context, job settlementJob) (any, error) {
	var params settlementParameters
	if err := json.Unmarshal(job.parameters, &params); err != nil {
		return nil, errors.New("invalid settlement command parameters")
	}
	audit := Audit{Operator: job.operator, Reason: job.reason}
	var files []Upload
	if len(job.upload) > 0 {
		if err := json.Unmarshal(job.upload, &files); err != nil {
			return nil, errors.New("invalid settlement upload")
		}
	}
	switch job.kind {
	case "bank_statement":
		if len(files) != 1 {
			return nil, errors.New("bank statement requires one file")
		}
		count, err := r.ImportBank(ctx, BankStatement{Environment: job.account.Environment, Account: params.BankAccount, Reference: params.Reference, Audit: audit},
			bytes.NewReader(files[0].Data))
		return map[string]int{"rows": count}, err
	case "bank_match":
		return r.MatchBank(ctx, MatchRequest{Provider: job.provider, Account: job.account, Payout: params.Reference,
			BankAccount: params.BankAccount, BankTransaction: params.BankTransaction, ManualEvidence: params.BankEvidence, Audit: audit})
	case "paypal_report":
		cfg, err := credentialstore.LoadForAccount(ctx, job.account)
		if err != nil {
			return nil, err
		}
		if cfg.ClientID != job.account.AccountKey || cfg.Environment != job.account.Environment || job.provider != payment.ProviderPayPal {
			return nil, errors.New("PayPal settlement credentials do not match the selected account")
		}
		return ImportPayPalUploads(ctx, r, job.account, cfg.MerchantID, files, audit)
	case "stripe_payout", "stripe_report":
		if job.provider != payment.ProviderStripe {
			return nil, errors.New("Stripe settlement account does not match the command")
		}
		purpose := stripe.PurposePayout
		if job.kind == "stripe_report" {
			purpose = stripe.PurposeReportRun
		}
		client, err := r.resolveStripeClient(ctx, job.account, purpose)
		if err != nil {
			return nil, err
		}
		if job.kind == "stripe_payout" {
			return ImportStripePayout(ctx, client, r, params.Reference, audit)
		}
		return ImportStripeReportWithOptions(ctx, client, r, params.Reference, audit, StripeReportImportOptions{
			ExpectedColumns:   params.ExpectedColumns,
			RequireMoRColumns: params.RequireMoRColumns,
		})
	default:
		return nil, fmt.Errorf("unknown settlement command kind %q", job.kind)
	}
}
