package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

const MetaBatchParameterRejected = "meta_batch_parameter_rejected"

type batchUpdate struct {
	ID          int64      `json:"id"`
	Attempt     int        `json:"attempt"`
	Outcome     string     `json:"outcome"`
	Reason      string     `json:"reason"`
	HTTPStatus  *int       `json:"http_status"`
	GraphCode   *int       `json:"graph_code"`
	GraphSub    *int       `json:"graph_subcode"`
	Trace       string     `json:"trace"`
	RespondedAt *time.Time `json:"responded_at"`
	State       string     `json:"state"`
	StateReason string     `json:"state_reason"`
	Next        time.Time  `json:"next_attempt_at"`
	Unknown     *int       `json:"unknown_attempt_no"`
	Final       bool       `json:"final"`
	BatchLimit  int        `json:"batch_limit"`
}

func batchMemberUpdate(m batchMember, result Result, window time.Duration, batchLimit int) batchUpdate {
	t := decide(m.state, m.attempt, result, window)
	var respondedAt *time.Time
	if !result.RespondedAt.IsZero() {
		respondedAt = &result.RespondedAt
	}
	return batchUpdate{ID: m.id, Attempt: m.attempt, Outcome: result.Outcome, Reason: result.Reason,
		HTTPStatus: result.HTTPStatus, GraphCode: result.GraphCode, GraphSub: result.GraphSubcode, Trace: result.TraceID, RespondedAt: respondedAt,
		State: t.state, StateReason: t.reason, Next: t.next, Unknown: t.unknown,
		Final: t.state == "failed" || t.state == "succeeded" || t.state == "skipped", BatchLimit: batchLimit}
}

// PersistBatchReceipt is separate from member finalization so a crash or report
// write failure can replay only the local updates. An expired lease may still
// persist evidence until the sweeper closes it.
func (s Service) PersistBatchReceipt(ctx context.Context, batch BatchLease, result Result, spacing time.Duration) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var destination, credential int64
	var started, now time.Time
	var count int
	var finished, hasReceipt, hasLateReceipt bool
	var maxAttempt int
	err = tx.QueryRow(ctx, `
		SELECT b.destination_id,b.credential_id,b.started_at,b.sent_count,b.finished_at IS NOT NULL,b.receipt IS NOT NULL,b.late_receipt IS NOT NULL,clock_timestamp(),
		       COALESCE((SELECT max(a.attempt_no-d.retry_budget_base_attempt)
		                 FROM conversion.delivery_attempts a JOIN conversion.deliveries d ON d.id=a.delivery_id
		                 WHERE a.batch_id=b.id AND a.batch_position IS NOT NULL),1)
		FROM conversion.meta_batches b
		WHERE b.id=$1 AND b.environment=$2 AND b.lease_token=$3::uuid AND b.started_at IS NOT NULL FOR UPDATE OF b`,
		batch.ID, s.Environment, batch.Token).Scan(&destination, &credential, &started, &count, &finished, &hasReceipt, &hasLateReceipt, &now, &maxAttempt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrLeaseLost
	}
	if err != nil {
		return err
	}
	if hasReceipt || finished && hasLateReceipt {
		return tx.Commit(ctx)
	}
	result = validateBatchReceipt(result, count)
	next := started.Add(spacing)
	if result.RateLimited || result.Reason == MetaRateLimited {
		result.RateLimited = true
		result.RetryAfter = retryAt(deliveryState{now: now}, maxAttempt, result)
		next = maxTime(next, result.RetryAfter)
	}
	evidence, err := json.Marshal(result)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `
		UPDATE conversion.meta_batches SET
		    receipt=CASE WHEN NOT $3 THEN $2 ELSE receipt END,
		    receipt_at=CASE WHEN NOT $3 THEN clock_timestamp() ELSE receipt_at END,
		    late_receipt=CASE WHEN $3 THEN COALESCE(late_receipt,$2) ELSE late_receipt END,
		    late_receipt_at=CASE WHEN $3 THEN COALESCE(late_receipt_at,clock_timestamp()) ELSE late_receipt_at END
		WHERE id=$1`, batch.ID, evidence, finished); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `
		UPDATE conversion.destinations SET
            next_claim_at=CASE WHEN $3 AND credential_id IS DISTINCT FROM $4 THEN next_claim_at
                               ELSE GREATEST(next_claim_at,$2) END,
            blocked_credential_id=CASE WHEN $3 AND credential_id=$4 THEN $4 ELSE blocked_credential_id END
        WHERE id=$1`, destination, next, result.CredentialRejected(), credential); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func validateBatchReceipt(r Result, count int) Result {
	confirmed := r.HTTPStatus != nil && *r.HTTPStatus == 200 && r.EventsReceived != nil &&
		*r.EventsReceived == count && r.GraphCode == nil
	zero := r.HTTPStatus != nil && *r.HTTPStatus >= 400 && r.EventsReceived != nil && *r.EventsReceived == 0
	if (r.Outcome == Accepted && !confirmed) ||
		((r.Outcome == PermanentRejection || r.Outcome == RetryableRejection) && !zero) {
		r.Outcome, r.Reason, r.Block = OutcomeUnknown, "meta_no_confirmation", false
	}
	return r
}

func (s Service) FinalizeBatch(ctx context.Context, batch BatchLease, window time.Duration) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	return s.finalizeBatchTx(ctx, tx, batch, window, false)
}

func (s Service) finalizeBatchTx(ctx context.Context, tx pgx.Tx, batch BatchLease, window time.Duration, expired bool) error {
	var receipt []byte
	var sentCount *int
	var finished bool
	err := tx.QueryRow(ctx, `
		SELECT receipt,sent_count,finished_at IS NOT NULL FROM conversion.meta_batches
		WHERE id=$1 AND environment=$2 AND lease_token=$3::uuid FOR UPDATE`,
		batch.ID, s.Environment, batch.Token).Scan(&receipt, &sentCount, &finished)
	if err != nil {
		return err
	}
	if finished {
		return tx.Commit(ctx)
	}
	received := Result{Outcome: OutcomeUnknown, Reason: "lease_expired_after_send_started"}
	if receipt != nil {
		if err := json.Unmarshal(receipt, &received); err != nil {
			return err
		}
	} else if !expired {
		return ErrLeaseLost
	}

	rows, err := tx.Query(ctx, `
		SELECT d.id,a.attempt_no,a.batch_position IS NOT NULL,
		       now(),d.deadline_at,d.retry_budget_base_attempt,d.unknown_attempt_no,d.unknown_retry_used
		FROM conversion.delivery_attempts a JOIN conversion.deliveries d
		  ON d.id=a.delivery_id AND d.attempts=a.attempt_no AND d.lease_token=a.lease_token
		WHERE a.batch_id=$1 AND a.lease_token=$2::uuid AND a.recorded_at IS NULL
		ORDER BY d.id FOR UPDATE OF d`, batch.ID, batch.Token)
	if err != nil {
		return err
	}
	members, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (batchMember, error) {
		var m batchMember
		err := row.Scan(&m.id, &m.attempt, &m.sent, &m.state.now, &m.state.deadline, &m.state.base, &m.state.unknown, &m.state.risk)
		return m, err
	})
	if err != nil {
		return err
	}
	if sentCount != nil && len(members) != *sentCount {
		return ErrLeaseLost
	}
	updates := make([]batchUpdate, 0, len(members))
	for _, m := range members {
		result, limit := received, 0
		if !m.sent {
			result = Result{Outcome: NotSent, Reason: "lease_expired_before_send"}
		} else if result.Reason == MetaBatchParameterRejected && sentCount != nil {
			limit = max(1, *sentCount/2)
		}
		updates = append(updates, batchMemberUpdate(m, result, window, limit))
	}
	if err := applyBatchUpdates(ctx, tx, batch, updates); err != nil {
		return err
	}
	return closeBatch(ctx, tx, batch.ID)
}

func applyBatchUpdates(ctx context.Context, tx pgx.Tx, batch BatchLease, updates []batchUpdate) error {
	if len(updates) == 0 {
		return nil
	}
	raw, err := json.Marshal(updates)
	if err != nil {
		return err
	}
	var writtenAttempts, writtenDeliveries int
	err = tx.QueryRow(ctx, `
		WITH input AS MATERIALIZED (
		    SELECT * FROM jsonb_to_recordset($3::jsonb) AS u(id bigint,attempt int,outcome text,reason text,
		        http_status int,graph_code int,graph_subcode int,trace text,responded_at timestamptz,
		        state text,state_reason text,next_attempt_at timestamptz,unknown_attempt_no int,final bool,batch_limit int)
		), attempts AS (
		    UPDATE conversion.delivery_attempts a SET outcome=u.outcome,reason=NULLIF(u.reason,''),recorded_at=clock_timestamp(),
		        http_status=u.http_status,graph_code=u.graph_code,graph_subcode=u.graph_subcode,
		        fbtrace_id=NULLIF(u.trace,''),responded_at=u.responded_at
		    FROM input u WHERE a.batch_id=$1 AND a.lease_token=$2::uuid
		      AND a.delivery_id=u.id AND a.attempt_no=u.attempt AND a.recorded_at IS NULL
		    RETURNING a.delivery_id
		), deliveries AS (
		    UPDATE conversion.deliveries d SET state=u.state,reason=NULLIF(u.state_reason,''),
		        next_attempt_at=u.next_attempt_at,unknown_attempt_no=u.unknown_attempt_no,lease_token=NULL,lease_until=NULL,
		        finalized_at=CASE WHEN u.final THEN clock_timestamp() END,
		        max_batch_size=LEAST(d.max_batch_size,COALESCE(NULLIF(u.batch_limit,0),d.max_batch_size))
		    FROM input u JOIN attempts a ON a.delivery_id=u.id
		    WHERE d.id=u.id AND d.attempts=u.attempt AND d.lease_token=$2::uuid AND d.finalized_at IS NULL
		    RETURNING d.id
		)
		SELECT (SELECT count(*) FROM attempts),(SELECT count(*) FROM deliveries)`,
		batch.ID, batch.Token, raw).Scan(&writtenAttempts, &writtenDeliveries)
	if err != nil {
		return err
	}
	if writtenAttempts != len(updates) || writtenDeliveries != len(updates) {
		return ErrLeaseLost
	}
	var finalized []int64
	for _, u := range updates {
		if u.Final {
			finalized = append(finalized, u.ID)
		}
	}
	if len(finalized) != 0 {
		return createReports(ctx, tx, finalized)
	}
	return nil
}

func (s Service) sweepBatches(ctx context.Context, limit int, window time.Duration) (int, error) {
	for processed := 0; processed < limit; processed++ {
		empty, err := s.sweepBatch(ctx, window)
		if err != nil || empty {
			return processed, err
		}
	}
	return limit, nil
}

func (s Service) sweepBatch(ctx context.Context, window time.Duration) (bool, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var batch BatchLease
	err = tx.QueryRow(ctx, `
		SELECT id,lease_token::text FROM conversion.meta_batches
		WHERE environment=$1 AND finished_at IS NULL AND (lease_until<=now() OR receipt IS NOT NULL)
		ORDER BY lease_until,id LIMIT 1 FOR UPDATE SKIP LOCKED`, s.Environment).Scan(&batch.ID, &batch.Token)
	if errors.Is(err, pgx.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return false, s.finalizeBatchTx(ctx, tx, batch, window, true)
}
