package settlement

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	stripego "github.com/stripe/stripe-go/v87"
	"reader-backend/internal/payment"
	"reader-backend/internal/payment/stripe"
)

const (
	ReportRunStatusPending   = "pending"
	ReportRunStatusCreating  = "creating"
	ReportRunStatusRunning   = "running"
	ReportRunStatusSucceeded = "succeeded"
	ReportRunStatusEnqueued  = "enqueued"
	ReportRunStatusFailed    = "failed"

	reportRunLeaseMinutes              = 10
	reportRunPollSeconds               = 60
	reportRunRetryMinutes              = 15
	reportRunMaxErrLen                 = 1000
	reportScheduleDayDuration          = 24 * time.Hour
	reportScheduleDaySeconds           = int64(reportScheduleDayDuration / time.Second)
	defaultReportCatchUpMaxDaysPerTick = 7
)

// ReportScheduleConfig controls which versioned itemized Stripe reports are
// scheduled periodically and whether MoR tax/fee columns are explicitly requested.
type ReportScheduleConfig struct {
	BalanceChangeReportType        string
	PayoutReconciliationReportType string
	IncludeMoRColumns              bool
	// StartDate optionally pins the earliest completed UTC day to schedule/catch up.
	StartDate time.Time
	// MaxCatchUpDaysPerTick caps how many missed UTC days are created per account
	// and report type in a single scheduler pass (defaults to defaultReportCatchUpMaxDaysPerTick).
	MaxCatchUpDaysPerTick int
}

func (c ReportScheduleConfig) resolvedTypes() ([]string, error) {
	balanceType := strings.TrimSpace(c.BalanceChangeReportType)
	if balanceType == "" {
		balanceType = stripe.DefaultBalanceChangeReportType
	}
	payoutType := strings.TrimSpace(c.PayoutReconciliationReportType)
	if payoutType == "" {
		payoutType = stripe.DefaultPayoutReconciliationReportType
	}
	if err := stripe.ValidateItemizedReportType(balanceType); err != nil {
		return nil, err
	}
	if err := stripe.ValidateItemizedReportType(payoutType); err != nil {
		return nil, err
	}
	return []string{balanceType, payoutType}, nil
}

// ReportScheduleRequest describes one deterministic Stripe Report Run schedule
// keyed by (account, environment, report_type, canonical params).
type ReportScheduleRequest struct {
	Account           payment.WebhookAccount
	ReportType        string
	IntervalStart     time.Time
	IntervalEnd       time.Time
	Currency          string
	PayoutID          string
	Columns           []string
	IncludeMoRColumns bool
	ActorID           string
	Reason            string
}

// ReportScheduleRecord represents a persisted row in payment_stripe_report_runs.
type ReportScheduleRecord struct {
	ID                int64
	AccountID         int64
	Environment       string
	ReportType        string
	ScheduleKey       string
	ParamsDigest      string
	IdempotencyKey    string
	ReportRunID       string
	Status            string
	ImportCommandID   *uuid.UUID
	LastError         string
	IncludeMoRColumns bool
	ExpectedColumns   []string
}

type canonicalReportParams struct {
	IntervalStart int64    `json:"interval_start,omitempty"`
	IntervalEnd   int64    `json:"interval_end,omitempty"`
	Currency      string   `json:"currency,omitempty"`
	PayoutID      string   `json:"payout_id,omitempty"`
	Columns       []string `json:"columns,omitempty"`
}

// BuildReportScheduleKey validates req and derives its deterministic schedule
// key, parameter digest, Stripe write idempotency key, canonical JSON params,
// and expected CSV columns.
func BuildReportScheduleKey(req ReportScheduleRequest) (scheduleKey, paramsDigest, idempotencyKey string, paramsJSON []byte, expectedCols []string, err error) {
	if strings.TrimSpace(req.Account.AccountKey) == "" || (req.Account.Environment != string(stripe.Sandbox) && req.Account.Environment != string(stripe.Live)) {
		return "", "", "", nil, nil, errors.New("Stripe report schedule requires a valid account and environment")
	}
	if err := stripe.ValidateItemizedReportType(req.ReportType); err != nil {
		return "", "", "", nil, nil, err
	}
	if strings.TrimSpace(req.ActorID) == "" || strings.TrimSpace(req.Reason) == "" {
		return "", "", "", nil, nil, errors.New("Stripe report schedule requires an actor and reason")
	}
	params := canonicalReportParams{
		Currency: strings.ToLower(strings.TrimSpace(req.Currency)),
		PayoutID: strings.TrimSpace(req.PayoutID),
	}
	if !req.IntervalStart.IsZero() || !req.IntervalEnd.IsZero() {
		start := req.IntervalStart.UTC().Truncate(time.Second)
		end := req.IntervalEnd.UTC().Truncate(time.Second)
		if !end.After(start) {
			return "", "", "", nil, nil, errors.New("Stripe report schedule interval_end must be after interval_start")
		}
		params.IntervalStart = start.Unix()
		params.IntervalEnd = end.Unix()
	} else if params.PayoutID == "" {
		return "", "", "", nil, nil, errors.New("Stripe report schedule requires an interval or payout ID")
	}
	resolvedCols := stripe.ResolveReportColumns(req.ReportType, req.Columns, req.IncludeMoRColumns)
	if len(resolvedCols) > 0 {
		params.Columns = resolvedCols
	}
	paramsJSON, err = json.Marshal(params)
	if err != nil {
		return "", "", "", nil, nil, err
	}
	sum := sha256.Sum256(paramsJSON)
	paramsDigest = hex.EncodeToString(sum[:])
	scheduleKey = fmt.Sprintf("stripe-report:%s:%s:%s:%s", req.Account.Environment, req.Account.AccountKey, req.ReportType, paramsDigest)
	idempotencyKey = scheduleKey
	if req.IncludeMoRColumns {
		expectedCols = append([]string(nil), resolvedCols...)
	} else if len(req.Columns) > 0 {
		expectedCols = append([]string(nil), resolvedCols...)
	}
	return scheduleKey, paramsDigest, idempotencyKey, paramsJSON, expectedCols, nil
}

// EnsureReportRunSchedule persists a deterministic Report Run schedule row so
// retries and process restarts reuse the exact same schedule key and report_run_id.
func (r *Repository) EnsureReportRunSchedule(ctx context.Context, req ReportScheduleRequest) (ReportScheduleRecord, bool, error) {
	scheduleKey, paramsDigest, idempotencyKey, paramsJSON, expectedCols, err := BuildReportScheduleKey(req)
	if err != nil {
		return ReportScheduleRecord{}, false, err
	}
	if expectedCols == nil {
		expectedCols = []string{}
	}
	var accountID int64
	err = r.pool.QueryRow(ctx, `SELECT id FROM payment_channel_accounts
		WHERE provider='stripe' AND account_key=$1 AND environment=$2`,
		req.Account.AccountKey, req.Account.Environment).Scan(&accountID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ReportScheduleRecord{}, false, errors.New("Stripe report schedule account has not been registered")
	}
	if err != nil {
		return ReportScheduleRecord{}, false, err
	}

	var rec ReportScheduleRecord
	var runID, lastErr *string
	err = r.pool.QueryRow(ctx, `INSERT INTO payment_stripe_report_runs
		(account_id,environment,report_type,schedule_key,params_digest,parameters,
		 include_mor_columns,expected_columns,idempotency_key,status,actor_id,reason)
		VALUES ($1,$2,$3,$4,$5,$6::jsonb,$7,$8,$9,$10,$11,$12)
		ON CONFLICT (schedule_key) DO NOTHING
		RETURNING id,account_id,environment,report_type,schedule_key,params_digest,
			idempotency_key,report_run_id,status,import_command_id,last_error,
			include_mor_columns,expected_columns`,
		accountID, req.Account.Environment, req.ReportType, scheduleKey, paramsDigest, paramsJSON,
		req.IncludeMoRColumns, expectedCols, idempotencyKey, ReportRunStatusPending, req.ActorID, req.Reason).
		Scan(&rec.ID, &rec.AccountID, &rec.Environment, &rec.ReportType, &rec.ScheduleKey, &rec.ParamsDigest,
			&rec.IdempotencyKey, &runID, &rec.Status, &rec.ImportCommandID, &lastErr,
			&rec.IncludeMoRColumns, &rec.ExpectedColumns)
	created := true
	if errors.Is(err, pgx.ErrNoRows) {
		created = false
		err = r.pool.QueryRow(ctx, `SELECT id,account_id,environment,report_type,schedule_key,params_digest,
			idempotency_key,report_run_id,status,import_command_id,last_error,
			include_mor_columns,expected_columns
			FROM payment_stripe_report_runs WHERE schedule_key=$1`, scheduleKey).
			Scan(&rec.ID, &rec.AccountID, &rec.Environment, &rec.ReportType, &rec.ScheduleKey, &rec.ParamsDigest,
				&rec.IdempotencyKey, &runID, &rec.Status, &rec.ImportCommandID, &lastErr,
				&rec.IncludeMoRColumns, &rec.ExpectedColumns)
	}
	if err != nil {
		return ReportScheduleRecord{}, false, err
	}
	if runID != nil {
		rec.ReportRunID = *runID
	}
	if lastErr != nil {
		rec.LastError = *lastErr
	}
	return rec, created, nil
}

// ScheduleDueStripeReportRuns ensures completed UTC day itemized Stripe Report
// Runs are scheduled for all enabled Stripe reconciliation accounts, catching up
// any missed days chronologically up to a bounded per-tick cap.
func (r *Repository) ScheduleDueStripeReportRuns(ctx context.Context, environment string, cfg ReportScheduleConfig, now time.Time) (int, error) {
	reportTypes, err := cfg.resolvedTypes()
	if err != nil {
		return 0, err
	}
	latestEnd := now.UTC().Truncate(reportScheduleDayDuration)
	latestStart := latestEnd.Add(-reportScheduleDayDuration)
	maxDaysPerTick := cfg.MaxCatchUpDaysPerTick
	if maxDaysPerTick <= 0 {
		maxDaysPerTick = defaultReportCatchUpMaxDaysPerTick
	}

	type enabledAccount struct {
		accountID        int64
		account          payment.WebhookAccount
		actorID          string
		reason           string
		initialSyncStart time.Time
	}
	rows, err := r.pool.Query(ctx, `SELECT a.id, a.account_key, a.environment, s.actor_id, s.reason, s.window_start
		FROM payment_ledger_sync_settings s
		JOIN payment_channel_accounts a ON a.id = s.account_id
		JOIN admins u ON u.id = s.actor_id AND public.admin_has_permission(u.id, 'payments.reconciliation:write')
		WHERE s.enabled AND a.provider = 'stripe' AND a.environment = $1
		ORDER BY a.id`, environment)
	if err != nil {
		return 0, err
	}
	var accounts []enabledAccount
	for rows.Next() {
		var item enabledAccount
		if err := rows.Scan(&item.accountID, &item.account.AccountKey, &item.account.Environment, &item.actorID, &item.reason, &item.initialSyncStart); err != nil {
			rows.Close()
			return 0, err
		}
		accounts = append(accounts, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	createdCount := 0
	var errs []error
	for _, item := range accounts {
		accountEarliestExactDaily, err := r.loadEarliestExactDailyStartForAccount(ctx, item.accountID, item.account.Environment)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, reportType := range reportTypes {
			existingKeys, earliestExactDaily, err := r.loadScheduledReportState(ctx, item.accountID, item.account.Environment, reportType)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			firstStart := latestStart
			if !cfg.StartDate.IsZero() {
				firstStart = cfg.StartDate.UTC().Truncate(reportScheduleDayDuration)
			} else if !earliestExactDaily.IsZero() {
				// Once at least one exact UTC daily run exists for this report type,
				// anchor strictly on that immutable history rather than sync window_start.
				if earliestExactDaily.Before(firstStart) {
					firstStart = earliestExactDaily
				}
			} else {
				// Before the first successful run of this report type, recover from the
				// earliest stable anchor: any sibling report type's exact daily start or
				// the account's initial sync window_start (when <= latestStart).
				if !accountEarliestExactDaily.IsZero() && accountEarliestExactDaily.Before(firstStart) {
					firstStart = accountEarliestExactDaily
				}
				if !item.initialSyncStart.IsZero() {
					syncDay := item.initialSyncStart.UTC().Truncate(reportScheduleDayDuration)
					if !syncDay.After(latestStart) && syncDay.Before(firstStart) {
						firstStart = syncDay
					}
				}
			}
			if firstStart.After(latestStart) {
				continue
			}

			scheduledThisPair := 0
			for dayStart := firstStart; !dayStart.After(latestStart) && scheduledThisPair < maxDaysPerTick; dayStart = dayStart.Add(reportScheduleDayDuration) {
				req := ReportScheduleRequest{
					Account:           item.account,
					ReportType:        reportType,
					IntervalStart:     dayStart,
					IntervalEnd:       dayStart.Add(reportScheduleDayDuration),
					IncludeMoRColumns: cfg.IncludeMoRColumns,
					ActorID:           item.actorID,
					Reason:            item.reason,
				}
				scheduleKey, _, _, _, _, keyErr := BuildReportScheduleKey(req)
				if keyErr != nil {
					errs = append(errs, keyErr)
					break
				}
				if existingKeys[scheduleKey] {
					continue
				}
				_, created, err := r.EnsureReportRunSchedule(ctx, req)
				if err != nil {
					errs = append(errs, err)
					break
				}
				existingKeys[scheduleKey] = true
				if created {
					createdCount++
					scheduledThisPair++
				}
			}
		}
	}
	return createdCount, errors.Join(errs...)
}

func (r *Repository) loadEarliestExactDailyStartForAccount(ctx context.Context, accountID int64, environment string) (time.Time, error) {
	var minStart *int64
	err := r.pool.QueryRow(ctx, `SELECT MIN((parameters->>'interval_start')::bigint)
		FROM payment_stripe_report_runs
		WHERE account_id = $1
		  AND environment = $2
		  AND parameters ? 'interval_start'
		  AND parameters ? 'interval_end'
		  AND ((parameters->>'interval_start')::bigint % $3) = 0
		  AND ((parameters->>'interval_end')::bigint - (parameters->>'interval_start')::bigint) = $3`,
		accountID, environment, reportScheduleDaySeconds).Scan(&minStart)
	if err != nil || minStart == nil {
		return time.Time{}, err
	}
	return time.Unix(*minStart, 0).UTC(), nil
}

func (r *Repository) loadScheduledReportState(ctx context.Context, accountID int64, environment, reportType string) (map[string]bool, time.Time, error) {
	rows, err := r.pool.Query(ctx, `SELECT schedule_key,
			CASE
				WHEN parameters ? 'interval_start'
				 AND parameters ? 'interval_end'
				 AND ((parameters->>'interval_start')::bigint % $4) = 0
				 AND ((parameters->>'interval_end')::bigint - (parameters->>'interval_start')::bigint) = $4
				THEN (parameters->>'interval_start')::bigint
				ELSE NULL
			END AS exact_daily_start
		FROM payment_stripe_report_runs
		WHERE account_id = $1
		  AND environment = $2
		  AND report_type = $3`,
		accountID, environment, reportType, reportScheduleDaySeconds)
	if err != nil {
		return nil, time.Time{}, err
	}
	defer rows.Close()

	keys := make(map[string]bool)
	var earliest time.Time
	for rows.Next() {
		var key string
		var exactStart *int64
		if err := rows.Scan(&key, &exactStart); err != nil {
			return nil, time.Time{}, err
		}
		keys[key] = true
		if exactStart != nil {
			day := time.Unix(*exactStart, 0).UTC()
			if earliest.IsZero() || day.Before(earliest) {
				earliest = day
			}
		}
	}
	return keys, earliest, rows.Err()
}

type reportRunJob struct {
	id                int64
	accountID         int64
	account           payment.WebhookAccount
	reportType        string
	scheduleKey       string
	idempotencyKey    string
	reportRunID       string
	status            string
	parameters        []byte
	includeMoRColumns bool
	expectedColumns   []string
	actorID           string
	reason            string
	token             uuid.UUID
}

// ProcessNextStripeReportRun claims one due persisted Report Run schedule and
// advances it through create -> poll -> idempotent stripe_report enqueue.
// All external Stripe API calls execute outside database transactions.
func (r *Repository) ProcessNextStripeReportRun(ctx context.Context, environment string) (bool, error) {
	job, err := r.claimStripeReportRun(ctx, environment)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	if err := r.advanceStripeReportRun(ctx, job); err != nil {
		if ctx.Err() != nil {
			return true, ctx.Err()
		}
		note := stripe.RedactSecrets(err.Error())
		_, saveErr := r.pool.Exec(ctx, `UPDATE payment_stripe_report_runs
			SET last_error=NULLIF(left($3,$4),''),
				next_attempt_at=now() + ($5 * interval '1 minute'),
				lease_token=NULL, lease_until=NULL, updated_at=now()
			WHERE id=$1 AND lease_token=$2`,
			job.id, job.token, note, reportRunMaxErrLen, reportRunRetryMinutes)
		return true, saveErr
	}
	return true, nil
}

func (r *Repository) claimStripeReportRun(ctx context.Context, environment string) (reportRunJob, error) {
	var job reportRunJob
	job.token = uuid.New()
	var runID *string
	err := r.pool.QueryRow(ctx, `WITH due AS (
		SELECT r.id, a.account_key, a.environment
		FROM payment_stripe_report_runs r
		JOIN payment_channel_accounts a ON a.id = r.account_id
		WHERE r.environment = $1
			AND r.status IN ($2, $3, $4, $5)
			AND r.next_attempt_at <= now()
			AND (r.lease_until IS NULL OR r.lease_until <= now())
		ORDER BY r.next_attempt_at, r.id
		FOR UPDATE OF r SKIP LOCKED
		LIMIT 1
	)
	UPDATE payment_stripe_report_runs r
	SET lease_token = $6,
		lease_until = now() + ($7 * interval '1 minute'),
		updated_at = now()
	FROM due
	WHERE r.id = due.id
	RETURNING r.id, r.account_id, due.account_key, due.environment, r.report_type,
		r.schedule_key, r.idempotency_key, r.report_run_id, r.status, r.parameters,
		r.include_mor_columns, r.expected_columns, r.actor_id, r.reason`,
		environment,
		ReportRunStatusPending, ReportRunStatusCreating, ReportRunStatusRunning, ReportRunStatusSucceeded,
		job.token, reportRunLeaseMinutes).
		Scan(&job.id, &job.accountID, &job.account.AccountKey, &job.account.Environment, &job.reportType,
			&job.scheduleKey, &job.idempotencyKey, &runID, &job.status, &job.parameters,
			&job.includeMoRColumns, &job.expectedColumns, &job.actorID, &job.reason)
	if runID != nil {
		job.reportRunID = *runID
	}
	return job, err
}

func (r *Repository) advanceStripeReportRun(ctx context.Context, job reportRunJob) error {
	// If already marked succeeded with a persisted run ID, go straight to idempotent enqueue.
	if job.status == ReportRunStatusSucceeded && job.reportRunID != "" {
		return r.enqueueScheduledStripeReport(ctx, job, job.reportRunID)
	}

	client, err := r.resolveStripeClient(ctx, job.account, stripe.PurposeReportRun)
	if err != nil {
		return err
	}

	expectLive := job.account.Environment == string(stripe.Live)
	var run *stripego.ReportingReportRun

	if job.reportRunID == "" {
		var params canonicalReportParams
		if err := json.Unmarshal(job.parameters, &params); err != nil {
			return fmt.Errorf("invalid Stripe report schedule parameters: %w", err)
		}
		if _, err := r.pool.Exec(ctx, `UPDATE payment_stripe_report_runs
			SET status=$3, updated_at=now() WHERE id=$1 AND lease_token=$2`,
			job.id, job.token, ReportRunStatusCreating); err != nil {
			return err
		}
		req := stripe.ReportRequest{
			Type:              job.reportType,
			Columns:           params.Columns,
			IncludeMoRColumns: job.includeMoRColumns,
			Currency:          params.Currency,
			PayoutID:          params.PayoutID,
			IdempotencyKey:    job.idempotencyKey,
		}
		if params.IntervalStart > 0 && params.IntervalEnd > 0 {
			req.Interval = stripe.TimeRange{
				From:   time.Unix(params.IntervalStart, 0).UTC(),
				Before: time.Unix(params.IntervalEnd, 0).UTC(),
			}
		}
		run, err = client.CreateReportRun(ctx, req)
		if err != nil {
			return err
		}
		if run == nil || !strings.HasPrefix(run.ID, "frr_") || run.Livemode != expectLive {
			return errors.New("unexpected Stripe report run response or environment mismatch")
		}
		// Persist report_run_id immediately so any subsequent crash or restart
		// resumes polling this run instead of creating a duplicate.
		if _, err := r.pool.Exec(ctx, `UPDATE payment_stripe_report_runs
			SET report_run_id=$3, status=$4, last_error=NULL, updated_at=now()
			WHERE id=$1 AND lease_token=$2`,
			job.id, job.token, run.ID, ReportRunStatusRunning); err != nil {
			return err
		}
		job.reportRunID = run.ID
	} else {
		run, err = client.GetReportRun(ctx, job.reportRunID)
		if err != nil {
			return err
		}
		if run == nil || run.ID != job.reportRunID || run.Livemode != expectLive {
			return errors.New("unexpected Stripe report run or environment mismatch")
		}
	}

	switch run.Status {
	case stripego.ReportingReportRunStatusSucceeded:
		return r.enqueueScheduledStripeReport(ctx, job, run.ID)
	case stripego.ReportingReportRunStatusFailed:
		reason := strings.TrimSpace(run.Error)
		if reason == "" {
			reason = "Stripe report run failed"
		}
		_, err := r.pool.Exec(ctx, `UPDATE payment_stripe_report_runs
			SET status=$3, last_error=NULLIF(left($4,$5),''),
				lease_token=NULL, lease_until=NULL, updated_at=now()
			WHERE id=$1 AND lease_token=$2`,
			job.id, job.token, ReportRunStatusFailed, stripe.RedactSecrets(reason), reportRunMaxErrLen)
		return err
	case stripego.ReportingReportRunStatusPending:
		_, err := r.pool.Exec(ctx, `UPDATE payment_stripe_report_runs
			SET status=$3, last_error=NULL,
				next_attempt_at=now() + ($4 * interval '1 second'),
				lease_token=NULL, lease_until=NULL, updated_at=now()
			WHERE id=$1 AND lease_token=$2`,
			job.id, job.token, ReportRunStatusRunning, reportRunPollSeconds)
		return err
	default:
		return fmt.Errorf("unexpected Stripe report run status %q", run.Status)
	}
}

func (r *Repository) enqueueScheduledStripeReport(ctx context.Context, job reportRunJob, runID string) error {
	cmdID := reportImportCommandID(job.account, runID)
	paramsMap := map[string]any{
		"reference":    runID,
		"schedule_key": job.scheduleKey,
		"report_type":  job.reportType,
	}
	if len(job.expectedColumns) > 0 {
		paramsMap["expected_columns"] = job.expectedColumns
	}
	if job.includeMoRColumns {
		paramsMap["require_mor_columns"] = true
	}
	paramsJSON, err := json.Marshal(paramsMap)
	if err != nil {
		return err
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))

	if _, err := tx.Exec(ctx, `INSERT INTO payment_settlement_commands
		(id,request_key,actor_id,kind,account_id,environment,reason,parameters,request_digest)
		VALUES ($1,$1,$2,$3,$4,$5,$6,$7::jsonb,$8)
		ON CONFLICT DO NOTHING`,
		cmdID, job.actorID, StripeReport, job.accountID, job.account.Environment, job.reason, paramsJSON, runID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE payment_stripe_report_runs
		SET status=$3, report_run_id=$4, import_command_id=$5,
			last_error=NULL, lease_token=NULL, lease_until=NULL, updated_at=now()
		WHERE id=$1 AND lease_token=$2`,
		job.id, job.token, ReportRunStatusEnqueued, runID, cmdID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
