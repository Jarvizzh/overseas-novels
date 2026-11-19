package partner

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ReportQueue struct {
	Pool        *pgxpool.Pool
	Environment string
	Source      string
	Contract    string
}

type ReportJob struct {
	ID          int64
	Token       string
	Attempt     int
	Status      int
	CreatedAt   time.Time
	Payload     []byte
	Wire        []byte
	Config      []byte
	Ciphertext  []byte
	KeyVersion  *string
	Credential  *int64
	NeedsReplay bool
}

const (
	reportConcurrency = 5
	reportBudget      = 24
	reportDBTimeout   = 3 * time.Second
	reportLease       = 30 * time.Second
)

var ErrReportLeaseLost = errors.New("partner report lease lost")

func (q ReportQueue) Claim(ctx context.Context) (ReportJob, error) {
	var j ReportJob
	err := pgx.BeginFunc(ctx, q.Pool, func(tx pgx.Tx) error {
		// Serialize global capacity checks across worker instances.
		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(1930717, hashtext($1))", q.Environment); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `
			WITH candidate AS (
				SELECT r.id FROM conversion.partner_reports r JOIN conversion.integrations i ON i.id=r.integration_id
				WHERE i.environment=$1 AND i.source=$5 AND i.enabled AND i.contract_version=$4 AND r.contract_version=$4
				  AND r.state='pending' AND r.lease_until IS NULL AND r.next_attempt_at<=now()
				  AND (SELECT count(*) FROM conversion.partner_reports active JOIN conversion.integrations owner ON owner.id=active.integration_id
				       WHERE owner.environment=$1 AND owner.source=$5 AND active.lease_until>now()) < $3
				ORDER BY r.next_attempt_at,r.id LIMIT 1 FOR UPDATE OF r SKIP LOCKED
			), claimed AS (
				UPDATE conversion.partner_reports r SET lease_token=gen_random_uuid(),lease_until=now()+$2::interval,attempts=attempts+1
				FROM candidate c WHERE r.id=c.id RETURNING r.*
			), attempt AS (
				INSERT INTO conversion.partner_report_attempts(report_id,attempt_no)
				SELECT id,attempts FROM claimed RETURNING report_id
			)
			SELECT r.id,r.lease_token::text,r.attempts,r.report_status,r.created_at,r.payload,r.wire_payload,
			       i.report_config,c.id,c.key_version,c.ciphertext,
			       EXISTS(SELECT 1 FROM conversion.partner_report_attempts prior
			              WHERE prior.report_id=r.id AND prior.outcome='outcome_unknown')
			FROM claimed r JOIN attempt a ON a.report_id=r.id JOIN conversion.integrations i ON i.id=r.integration_id
			LEFT JOIN conversion.credentials c ON c.id=i.report_credential_id`,
			q.Environment, reportLease, reportConcurrency, q.Contract, q.Source).
			Scan(&j.ID, &j.Token, &j.Attempt, &j.Status, &j.CreatedAt, &j.Payload, &j.Wire,
				&j.Config, &j.Credential, &j.KeyVersion, &j.Ciphertext, &j.NeedsReplay)
	})
	return j, err
}

func (q ReportQueue) Start(ctx context.Context, j ReportJob, wire []byte, replay bool, window, maxAge time.Duration) error {
	tag, err := q.Pool.Exec(ctx, `
		WITH frozen AS (
			UPDATE conversion.partner_reports r SET wire_payload=COALESCE(r.wire_payload,$8)
			FROM conversion.integrations i JOIN conversion.credentials c ON c.id=i.report_credential_id
			WHERE r.integration_id=i.id AND r.id=$1 AND r.lease_token=$2::uuid AND r.attempts=$3
			  AND r.state='pending' AND r.lease_until>clock_timestamp()+$10::interval
			  AND r.created_at+$13::interval>clock_timestamp()+$10::interval
			  AND i.environment=$4 AND i.source=$14 AND i.enabled AND i.contract_version=$11 AND r.contract_version=$11
			  AND i.report_config=$5 AND c.id=$6 AND c.ciphertext=$7 AND c.key_version=$12
			  AND (r.wire_payload IS NULL OR r.wire_payload=$8)
			  AND EXISTS(SELECT 1 FROM conversion.partner_report_attempts a WHERE a.report_id=r.id AND a.attempt_no=r.attempts
			             AND a.sending_at IS NULL AND a.recorded_at IS NULL)
			RETURNING r.id
		)
		UPDATE conversion.partner_report_attempts a SET sending_at=clock_timestamp(),replay_allowed=$9
		FROM frozen r WHERE a.report_id=r.id AND a.attempt_no=$3 AND a.sending_at IS NULL AND a.recorded_at IS NULL`,
		j.ID, j.Token, j.Attempt, q.Environment, j.Config, j.Credential, j.Ciphertext, wire, replay, window, q.Contract, j.KeyVersion, maxAge, q.Source)
	if err == nil && tag.RowsAffected() != 1 {
		return ErrReportLeaseLost
	}
	return err
}

type ReportPolicy struct {
	Replay bool `json:"replay_supported"`
	MaxAge int  `json:"max_age_seconds"`
}

type ReportResult struct {
	Outcome    string
	Reason     string
	RetryAfter time.Time
}

const (
	ReportAccepted = "accepted"
	ReportRejected = "rejected"
	ReportRetry    = "retryable_rejection"
	ReportNotSent  = "not_sent"
	ReportUnknown  = "outcome_unknown"
	ReportBlocked  = "blocked"
)

func (config ReportPolicy) Deadline(created time.Time) time.Time {
	if config.MaxAge < 1 || config.MaxAge > 30*24*60*60 {
		return time.Time{}
	}
	return created.Add(time.Duration(config.MaxAge) * time.Second)
}

func reportTransition(j ReportJob, now time.Time, result ReportResult, replay bool, deadline time.Time) (state, reason string, next time.Time) {
	if result.Outcome == ReportAccepted {
		return "confirmed", "", now
	}
	unknown := j.NeedsReplay || result.Outcome == ReportUnknown
	state, reason = "blocked", result.Reason
	if unknown {
		state = "outcome_unknown"
	}
	switch result.Outcome {
	case ReportUnknown, ReportRetry, ReportNotSent:
		backoff := min(30*time.Second<<min(j.Attempt-1, 7), time.Hour)
		next = now.Add(backoff/2 + rand.N(backoff/2))
		if result.RetryAfter.After(next) {
			next = result.RetryAfter
		}
		if j.Attempt < reportBudget && (!unknown || replay) && next.Before(deadline) {
			return "pending", reason, next
		}
		if !unknown {
			reason = "partner_retry_budget_or_deadline"
		}
	}
	return state, reason, now
}

// Finish closes the attempt and changes the queue in one fenced transaction.
func (q ReportQueue) Finish(ctx context.Context, j ReportJob, result ReportResult, fromSweep bool) error {
	return pgx.BeginFunc(ctx, q.Pool, func(tx pgx.Tx) error {
		return q.finishTx(ctx, tx, j, result, fromSweep)
	})
}

func (q ReportQueue) finishTx(ctx context.Context, tx pgx.Tx, j ReportJob, result ReportResult, fromSweep bool) error {
	var now time.Time
	var config ReportPolicy
	var raw []byte
	var sending, replay bool
	err := tx.QueryRow(ctx, `
		SELECT clock_timestamp(),i.report_config,a.sending_at IS NOT NULL,a.replay_allowed
		FROM conversion.partner_reports r JOIN conversion.integrations i ON i.id=r.integration_id
		JOIN conversion.partner_report_attempts a ON a.report_id=r.id AND a.attempt_no=r.attempts
		WHERE r.id=$1 AND r.lease_token=$2::uuid AND r.attempts=$3 AND r.state='pending'
		  AND i.environment=$4 AND a.recorded_at IS NULL
		  AND ($5 OR r.lease_until>clock_timestamp())
		FOR UPDATE OF r`, j.ID, j.Token, j.Attempt, q.Environment, fromSweep).Scan(&now, &raw, &sending, &replay)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrReportLeaseLost
	}
	if err != nil {
		return err
	}
	// An invalid current policy must never authorize a replay.
	if json.Unmarshal(raw, &config) != nil {
		config = ReportPolicy{}
	}
	if fromSweep {
		result = ReportResult{Outcome: ReportNotSent, Reason: "partner_lease_expired_before_send"}
		if sending {
			result = ReportResult{Outcome: ReportUnknown, Reason: "partner_lease_expired_after_send"}
		}
	}
	if result.Outcome == ReportAccepted && !sending {
		return ErrReportLeaseLost
	}
	allowReplay := config.Replay && (!sending || replay)
	state, reason, next := reportTransition(j, now, result, allowReplay, config.Deadline(j.CreatedAt))
	recorded, err := tx.Exec(ctx, `
		UPDATE conversion.partner_report_attempts SET outcome=$3,reason=NULLIF($4,''),recorded_at=$5,
		    retry_at=CASE WHEN $6='pending' THEN $7::timestamptz END
		WHERE report_id=$1 AND attempt_no=$2 AND recorded_at IS NULL`, j.ID, j.Attempt, result.Outcome, result.Reason, now, state, next)
	if err != nil {
		return err
	}
	updated, err := tx.Exec(ctx, `
		UPDATE conversion.partner_reports SET state=$4,reason=NULLIF($5,''),next_attempt_at=$6,lease_token=NULL,lease_until=NULL
		WHERE id=$1 AND lease_token=$2::uuid AND attempts=$3`, j.ID, j.Token, j.Attempt, state, reason, next)
	if err != nil {
		return err
	}
	if recorded.RowsAffected() != 1 || updated.RowsAffected() != 1 {
		return ErrReportLeaseLost
	}
	return nil
}

func (q ReportQueue) Sweep(ctx context.Context, limit int) (count int, err error) {
	for count < limit {
		err = pgx.BeginFunc(ctx, q.Pool, func(tx pgx.Tx) error {
			var j ReportJob
			err := tx.QueryRow(ctx, `
				SELECT r.id,r.lease_token::text,r.attempts,r.created_at,
				       EXISTS(SELECT 1 FROM conversion.partner_report_attempts p
				              WHERE p.report_id=r.id AND p.outcome='outcome_unknown')
				FROM conversion.partner_reports r JOIN conversion.integrations i ON i.id=r.integration_id
				WHERE i.environment=$1 AND i.source=$2 AND r.contract_version=$3 AND r.state='pending' AND r.lease_until<=now()
				ORDER BY r.lease_until,r.id LIMIT 1 FOR UPDATE OF r SKIP LOCKED`, q.Environment, q.Source, q.Contract).
				Scan(&j.ID, &j.Token, &j.Attempt, &j.CreatedAt, &j.NeedsReplay)
			if err != nil {
				return err
			}
			return q.finishTx(ctx, tx, j, ReportResult{}, true)
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return count, nil
		}
		if err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}
