package delivery

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// ReviewLegacyEnvironment records evidence for an existing import. It never
// creates a second delivery or grants a retry after a possibly sent request.
func (s Service) ReviewLegacyEnvironment(ctx context.Context, operator Operator, key, reference, reason string) (OperationResult, error) {
	if !validConfigField(key, 500) {
		return OperationResult{}, ErrInvalidOperation
	}
	reference, err := cleanReason(reference)
	if err != nil {
		return OperationResult{}, err
	}
	return s.operate(ctx, operator, reason, func(tx pgx.Tx, actor Operator, reason string) (OperationResult, error) {
		out := OperationResult{ID: key, Action: "review_legacy_environment"}
		var version, oldResolution string
		var eventID int64
		var row legacyMessage
		var occurred *time.Time
		err := tx.QueryRow(ctx, `
			SELECT m.event_id, m.legacy_state,m.legacy_attempts,m.legacy_delivered_at,
			       m.legacy_next_attempt_at,m.legacy_had_lease,COALESCE(m.environment_evidence,''),
			       m.resolution,e.occurred_at
			FROM conversion.legacy_outbox_imports m JOIN conversion.events e ON e.id=m.event_id
			WHERE m.legacy_event_id=$1 AND e.environment=$2 FOR UPDATE OF m`, key, s.Environment).
			Scan(&eventID, &row.State, &row.Attempts, &row.DeliveredAt, &row.NextAttemptAt, &row.HasLease,
				&version, &oldResolution, &occurred)
		if err != nil {
			return OperationResult{}, err
		}
		if version != "" {
			if version != reference {
				return OperationResult{}, ErrOperationConflict
			}
			out.Existing = true
			return out, nil
		}

		type deliverySnapshot struct {
			id                  int64
			state, reason       string
			next, deadline, now time.Time
			attempts            int
			leased              bool
		}
		var d deliverySnapshot
		err = tx.QueryRow(ctx, `
			SELECT id,state,COALESCE(reason,''),next_attempt_at,deadline_at,clock_timestamp(),attempts,lease_token IS NOT NULL
			FROM conversion.deliveries WHERE event_id=$1 AND role='original' AND environment=$2 FOR UPDATE`, eventID, s.Environment).
			Scan(&d.id, &d.state, &d.reason, &d.next, &d.deadline, &d.now, &d.attempts, &d.leased)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return OperationResult{}, err
		}
		resolution := oldResolution
		if err == nil && d.state == "blocked" && d.reason == "legacy_environment_unverified" {
			if d.leased || d.attempts != row.Attempts {
				return OperationResult{}, ErrOperationConflict
			}
			state, cause := legacyState(row, true, true, occurred != nil, d.deadline, d.now)
			if !maxTime(d.now, d.next).Add(requestReserve).Before(d.deadline) && state == "pending" {
				state, cause = "failed", "deadline_exhausted"
			}
			if state == "outcome_unknown" && !d.now.Add(requestReserve).Before(d.deadline) {
				state, cause = "failed", "meta_no_confirmation"
			}
			if _, err = tx.Exec(ctx, `
				UPDATE conversion.deliveries SET state=$2,reason=NULLIF($3,''),
				    finalized_at=CASE WHEN $2='failed' THEN clock_timestamp() END
				WHERE id=$1`, d.id, state, cause); err != nil {
				return OperationResult{}, err
			}
			resolution = state
			if state == "pending" {
				out.NextAttemptAt = &d.next
			}
		}
		if _, err = tx.Exec(ctx, `
			UPDATE conversion.legacy_outbox_imports SET environment_evidence=$2,resolution=$3
			WHERE legacy_event_id=$1`, key, reference, resolution); err != nil {
			return OperationResult{}, err
		}
		err = s.audit(ctx, tx, actor, out.Action, key, reason, reference,
			map[string]any{"resolution": oldResolution, "environment_verified": false},
			map[string]any{"resolution": resolution, "environment_verified": true, "delivery_id": d.id})
		return out, err
	})
}
