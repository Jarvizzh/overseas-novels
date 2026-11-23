package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

type expired struct {
	job     Lease
	current deliveryState
	reason  *string
	sending bool
}

func expiredTx(ctx context.Context, tx pgx.Tx, environment string, limit int) ([]expired, error) {
	rows, err := tx.Query(ctx, `
		WITH due AS (
			(SELECT id, lease_until AS due_at FROM conversion.deliveries
			 WHERE environment = $1 AND lease_until <= now() AND NOT EXISTS (
			    SELECT 1 FROM conversion.delivery_attempts a WHERE a.delivery_id=deliveries.id
			      AND a.attempt_no=deliveries.attempts AND a.batch_id IS NOT NULL
			 ) ORDER BY lease_until, id LIMIT $2)
			UNION ALL
			(SELECT id, deadline_at AS due_at FROM conversion.deliveries
			 WHERE environment = $1 AND finalized_at IS NULL AND lease_until IS NULL AND deadline_at <= now()
			 ORDER BY deadline_at, id LIMIT $2)
		)
		SELECT d.id, COALESCE(d.lease_token::text,''), d.attempts, now(), d.deadline_at,
		       d.retry_budget_base_attempt, d.unknown_attempt_no, d.unknown_retry_used, d.reason, a.sending_at IS NOT NULL
		FROM due JOIN conversion.deliveries d ON d.id = due.id
		LEFT JOIN conversion.delivery_attempts a ON a.delivery_id = d.id AND a.attempt_no = d.attempts
		WHERE d.finalized_at IS NULL AND (d.lease_until <= now() OR (d.lease_until IS NULL AND d.deadline_at <= now()))
		ORDER BY due.due_at, d.id LIMIT $2 FOR UPDATE OF d SKIP LOCKED`, environment, limit)
	if err != nil {
		return nil, err
	}
	batch, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (expired, error) {
		var e expired
		err := row.Scan(&e.job.ID, &e.job.Token, &e.job.Attempt, &e.current.now, &e.current.deadline,
			&e.current.base, &e.current.unknown, &e.current.risk, &e.reason, &e.sending)
		return e, err
	})
	if err != nil {
		return nil, err
	}
	return batch, nil
}

// createReport freezes the outcome and source fields. Partner-specific encoding
// and credentials are applied by the adapter for the stored contract version.
func createReport(ctx context.Context, tx pgx.Tx, id int64) error {
	return createReports(ctx, tx, []int64{id})
}

func createReports(ctx context.Context, tx pgx.Tx, ids []int64) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO conversion.partner_reports
			(integration_id, event_id, original_delivery_id, report_status, contract_version, payload, state, reason)
		SELECT i.id, e.id, d.id, CASE d.state WHEN 'succeeded' THEN 1 WHEN 'skipped' THEN 0 ELSE -1 END,
		       COALESCE(d.frozen_route->>'contract_version',i.contract_version),
		       jsonb_strip_nulls(jsonb_build_object(
		         'source_fields', e.partner_fields, 'event_id', e.source_event_id, 'pixel_id', x.pixel_id,
		         'event_name', CASE WHEN i.source='sinan' AND e.payment_policy_state<>'historical' THEN e.event_name END,
		         'report_time', d.finalized_at, 'last_attempt_at', d.last_attempt_at, 'reason', d.reason,
		         'fbtrace_id', a.fbtrace_id, 'http_status', a.http_status, 'events_received', a.events_received,
		         'graph_code', a.graph_code, 'graph_subcode', a.graph_subcode, 'outcome', a.outcome)),
		       CASE WHEN contract.block_reason IS NULL THEN 'pending' ELSE 'blocked' END,
		       contract.block_reason
		FROM conversion.deliveries d JOIN conversion.events e ON e.id = d.event_id
		JOIN conversion.integrations i ON i.id = e.integration_id
		JOIN conversion.destinations x ON x.id = d.destination_id
		LEFT JOIN conversion.delivery_attempts a ON a.delivery_id = d.id AND a.attempt_no =
		    CASE WHEN d.state = 'succeeded' THEN d.attempts ELSE COALESCE(d.unknown_attempt_no,d.attempts) END
		CROSS JOIN LATERAL (
		    SELECT CASE
		        WHEN d.state='failed' AND a.fbtrace_id IS NULL
		             AND COALESCE(i.report_config->>'no_trace_encoding','') NOT IN ('omit','empty')
		        THEN 'no_trace_contract_unconfirmed'
		        WHEN i.source='sinan' AND e.event_name='Purchase' AND e.payment_policy_state<>'historical'
		             AND NULLIF(e.partner_fields->>'promotion_id','') IS NULL
		             AND COALESCE(i.report_config->>'missing_promotion_encoding','') <> 'null'
		        THEN 'missing_promotion_contract_unconfirmed'
		    END AS block_reason
		) contract
		WHERE d.id=ANY($1::bigint[]) AND d.role = 'original' AND i.source <> 'starnovel'
		ON CONFLICT (integration_id, event_id, original_delivery_id) DO NOTHING`, ids)
	return err
}

// confirmedEvidenceTx is called after locking the delivery. Its fresh statement
// sees any evidence committed under that lock before recovery acquired it.
func confirmedEvidenceTx(ctx context.Context, tx pgx.Tx, e expired) (*Result, error) {
	if !e.sending {
		return nil, nil
	}
	var data []byte
	var persistedAt time.Time
	err := tx.QueryRow(ctx, `
		SELECT n.evidence,n.recorded_at FROM conversion.delivery_diagnostics n
		JOIN conversion.delivery_attempts a USING(delivery_id,attempt_no)
		WHERE n.delivery_id=$1 AND n.attempt_no=$2 AND NOT n.late
		  AND a.lease_token=$3::uuid AND a.sending_at IS NOT NULL AND a.recorded_at IS NULL`,
		e.job.ID, e.job.Attempt, e.job.Token).Scan(&data, &persistedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var result Result
	if json.Unmarshal(data, &result) != nil {
		return nil, nil
	}
	accepted := result.Outcome == Accepted && result.HTTPStatus != nil && *result.HTTPStatus == 200 &&
		result.EventsReceived != nil && *result.EventsReceived == 1
	rateLimited := result.Outcome == RetryableRejection && result.Reason == MetaRateLimited
	if !accepted && !rateLimited {
		return nil, nil
	}

	// The delivery lock orders receipt persistence against finalization. The
	// application clock is useful for diagnostics, not for fencing a DB lease.
	if result.RespondedAt.IsZero() {
		result.RespondedAt = persistedAt
	}
	return &result, nil
}

func (s Store) Diagnostic(ctx context.Context, job Lease, result Result) error {
	// Lease expiry permits recovery, but the current attempt remains authoritative
	// until recovery takes the lock and clears or replaces its token.
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var current bool
	err = tx.QueryRow(ctx, `
		SELECT COALESCE(lease_token=$2::uuid AND attempts=$3 AND finalized_at IS NULL,false)
		FROM conversion.deliveries WHERE id=$1 AND environment=$4 FOR UPDATE`,
		job.ID, job.Token, job.Attempt, s.Environment).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	evidence, err := json.Marshal(result)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO conversion.delivery_diagnostics (delivery_id, attempt_no, late, evidence)
		SELECT delivery_id, attempt_no, $4, $5 FROM conversion.delivery_attempts
		WHERE delivery_id=$1 AND attempt_no=$2 AND lease_token=$3::uuid
		ON CONFLICT (delivery_id,attempt_no,late) DO NOTHING`, job.ID, job.Attempt, job.Token, !current, evidence)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s Store) Heartbeat(ctx context.Context, workerType, instance string, paused bool, progress bool) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO conversion.worker_heartbeats
		    (environment, worker_type, instance_id, heartbeat_at, paused, last_progress_at)
		VALUES ($1, $5, $2, now(), $3, CASE WHEN $4 THEN now() END)
		ON CONFLICT (environment, worker_type, instance_id) DO UPDATE SET
		    heartbeat_at=EXCLUDED.heartbeat_at, paused=EXCLUDED.paused,
		    last_progress_at=COALESCE(EXCLUDED.last_progress_at, conversion.worker_heartbeats.last_progress_at)`,
		s.Environment, instance, paused, progress, workerType)
	return err
}
