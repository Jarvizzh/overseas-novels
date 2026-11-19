package delivery

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

var (
	ErrInvalidOperation  = errors.New("invalid_operation")
	ErrOperatorForbidden = errors.New("operator_forbidden")
	ErrOperationConflict = errors.New("state_conflict")
)

type Operator struct {
	ID   string
	Role string
}

type OperationResult struct {
	ID            string     `json:"id,omitempty"`
	Action        string     `json:"action"`
	NextAttemptAt *time.Time `json:"next_attempt_at,omitempty"`
	Existing      bool       `json:"existing,omitempty"`
	DryRun        bool       `json:"dry_run,omitempty"`
}

const requestReserve = 8 * time.Second

func cleanReason(reason string) (string, error) {
	value := strings.TrimSpace(reason)
	if !utf8.ValidString(value) || strings.ContainsRune(value, 0) || value == "" || utf8.RuneCountInString(value) > 500 {
		return "", ErrInvalidOperation
	}
	return value, nil
}

func verifyOperatorTx(ctx context.Context, tx pgx.Tx, operator Operator) (Operator, error) {
	var role string
	err := tx.QueryRow(ctx, "SELECT role FROM conversion.lock_active_admin($1)", operator.ID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && role != "SuperAdmin" && role != "Admin") ||
		(err == nil && operator.Role != "" && operator.Role != role) {
		return Operator{}, ErrOperatorForbidden
	}
	operator.Role = role
	return operator, err
}

func (s Service) operate(ctx context.Context, operator Operator, reason string,
	action func(pgx.Tx, Operator, string) (OperationResult, error),
) (OperationResult, error) {
	reason, err := cleanReason(reason)
	if err != nil {
		return OperationResult{}, err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return OperationResult{}, err
	}
	defer tx.Rollback(ctx)
	operator, err = verifyOperatorTx(ctx, tx, operator)
	if err != nil {
		return OperationResult{}, err
	}
	result, err := action(tx, operator, reason)
	if err != nil {
		return OperationResult{}, err
	}
	return result, tx.Commit(ctx)
}

func (s Service) audit(ctx context.Context, tx pgx.Tx, operator Operator, action, subject, reason, evidence string,
	before, after map[string]any,
) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO conversion.operation_audit(environment,action,subject_id,operator_id,operator_role,
			reason,evidence_reference,before_state,after_state)
		VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,''),$8,$9)`, s.Environment, action, subject,
		operator.ID, operator.Role, reason, evidence, before, after)
	return err
}

type retryState struct {
	state, reason                     string
	now, next, deadline               time.Time
	attempts, base                    int
	unknownRetry, budgetReset, leased bool
	unknownAttempt                    *int
}

func (s Service) RetryDelivery(ctx context.Context, operator Operator, id int64, expected int, reason string, acknowledge bool) (OperationResult, error) {
	if id <= 0 || expected < 0 {
		return OperationResult{}, ErrInvalidOperation
	}
	return s.operate(ctx, operator, reason, func(tx pgx.Tx, actor Operator, reason string) (OperationResult, error) {
		var d retryState
		err := tx.QueryRow(ctx, `
			SELECT state,COALESCE(reason,''),clock_timestamp(),next_attempt_at,deadline_at,attempts,retry_budget_base_attempt,
			       unknown_retry_used,budget_reset_used,lease_token IS NOT NULL,unknown_attempt_no
			FROM conversion.deliveries WHERE id=$1 AND environment=$2 FOR UPDATE`, id, s.Environment).
			Scan(&d.state, &d.reason, &d.now, &d.next, &d.deadline, &d.attempts, &d.base, &d.unknownRetry, &d.budgetReset, &d.leased, &d.unknownAttempt)
		if err != nil {
			return OperationResult{}, err
		}
		base, reset, unknown, err := retryPolicy(d, expected, acknowledge)
		if err != nil {
			return OperationResult{}, err
		}
		next := d.now
		if d.state == "pending" && d.reason == "meta_retryable_rejection" {
			return OperationResult{}, ErrOperationConflict
		}
		_, err = tx.Exec(ctx, `
			UPDATE conversion.deliveries SET state='pending',next_attempt_at=$2,
			       retry_budget_base_attempt=$3,budget_reset_used=$4,unknown_retry_used=$5
			WHERE id=$1`, id, next, base, reset, d.unknownRetry || unknown)
		if err != nil {
			return OperationResult{}, err
		}
		subject := strconv.FormatInt(id, 10)
		before := map[string]any{"state": d.state, "next_attempt_at": d.next, "attempts": expected, "budget_base": d.base, "risk_retry": d.unknownRetry}
		after := map[string]any{"state": "pending", "next_attempt_at": next, "attempts": expected, "budget_base": base, "risk_retry": d.unknownRetry || unknown}
		err = s.audit(ctx, tx, actor, "retry_delivery", subject, reason, "", before, after)
		return OperationResult{ID: subject, Action: "retry_delivery", NextAttemptAt: &next}, err
	})
}

func retryPolicy(d retryState, expected int, acknowledge bool) (base int, reset, unknown bool, err error) {
	if d.attempts != expected || d.leased || !d.now.Add(requestReserve).Before(d.deadline) {
		return 0, false, false, ErrOperationConflict
	}
	unknown = d.state == "outcome_unknown" || d.unknownAttempt != nil
	switch d.state {
	case "pending":
		if !d.now.Before(d.next) {
			return 0, false, false, ErrOperationConflict
		}
	case "blocked":
		switch d.reason {
		case "retry_budget_exhausted", "destination_or_credential_unavailable", "credential_decryption_failed",
			"meta_credentials_or_permissions", "destination_disabled", "promotion_pixel_mismatch", "partner_route_not_authorized":
		default:
			return 0, false, false, ErrOperationConflict
		}
	case "outcome_unknown":
	default:
		return 0, false, false, ErrOperationConflict
	}
	if unknown && (d.unknownRetry || !acknowledge) {
		return 0, false, false, ErrOperationConflict
	}
	base, reset = d.base, d.budgetReset
	if d.attempts-base >= attemptBudget {
		if d.state != "blocked" || d.reason != "retry_budget_exhausted" || reset {
			return 0, false, false, ErrOperationConflict
		}
		base, reset = d.attempts, true
	}
	return base, reset, unknown, nil
}

func (s Service) AppendTarget(ctx context.Context, operator Operator, eventID, destinationID int64, reason string) (OperationResult, error) {
	if eventID <= 0 || destinationID <= 0 {
		return OperationResult{}, ErrInvalidOperation
	}
	return s.operate(ctx, operator, reason, func(tx pgx.Tx, actor Operator, reason string) (OperationResult, error) {
		var p Payload
		var now time.Time
		var sourceType, sourceID string
		var integrationID int64
		err := tx.QueryRow(ctx, `
			SELECT integration_id,event_name,occurred_at,action_source,source_url,matching_data,amount::text,currency,
			       source_event_type,source_event_id,clock_timestamp()
			FROM conversion.events WHERE id=$1 AND environment=$2 FOR UPDATE`, eventID, s.Environment).
			Scan(&integrationID, &p.EventName, &p.OccurredAt, &p.ActionSource, &p.SourceURL, &p.MatchingData,
				&p.Amount, &p.Currency, &sourceType, &sourceID, &now)
		if err != nil {
			return OperationResult{}, err
		}
		var existing int64
		err = tx.QueryRow(ctx, "SELECT id FROM conversion.deliveries WHERE event_id=$1 AND destination_id=$2", eventID, destinationID).Scan(&existing)
		if err == nil {
			return OperationResult{ID: strconv.FormatInt(existing, 10), Action: "append_target", Existing: true}, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return OperationResult{}, err
		}
		var allowed int64
		var routeIssue string
		err = tx.QueryRow(ctx, `
			SELECT x.id,COALESCE(conversion.sinan_route_issue($4,x.id),'')
			FROM conversion.integration_destinations r JOIN conversion.destinations x ON x.id=r.destination_id
			WHERE r.integration_id=$1 AND x.id=$2 AND x.environment=$3 AND x.enabled AND x.retired_at IS NULL FOR SHARE OF r,x`,
			integrationID, destinationID, s.Environment, eventID).Scan(&allowed, &routeIssue)
		if errors.Is(err, pgx.ErrNoRows) {
			return OperationResult{}, ErrOperationConflict
		}
		if err != nil {
			return OperationResult{}, err
		}
		if err := ValidatePayload(p); routeIssue != "" || err != nil || p.OccurredAt.After(now) {
			return OperationResult{}, ErrOperationConflict
		}
		deadline := deliveryDeadline(now, *p.OccurredAt)
		if !now.Add(requestReserve).Before(deadline) {
			return OperationResult{}, ErrOperationConflict
		}
		// The destination receives a stable, namespaced ID derived from the immutable source identity.
		metaID := SourceMetaID(integrationID, sourceType, sourceID)
		id, err := insertTargetTx(ctx, tx, eventID, s.Environment, Target{
			DestinationID: destinationID, MetaEventID: metaID, FrozenRoute: []byte(`{"reason":"manual_append"}`),
		}, deadline, "pending", "")
		if err != nil {
			return OperationResult{}, err
		}
		subject := strconv.FormatInt(id, 10)
		err = s.audit(ctx, tx, actor, "append_target", subject, reason, "", map[string]any{},
			map[string]any{"event_id": eventID, "destination_id": destinationID, "state": "pending", "deadline_at": deadline})
		return OperationResult{ID: subject, Action: "append_target"}, err
	})
}

func SourceMetaID(integrationID int64, sourceType, sourceID string) string {
	return fmt.Sprintf("conversion:%x", sha256.Sum256([]byte(strconv.FormatInt(integrationID, 10)+"\x00"+sourceType+"\x00"+sourceID)))
}

func deliveryDeadline(ingress, occurred time.Time) time.Time {
	byIngress, byAge := ingress.Add(deliveryLifetime), occurred.Add(metaWindow-metaSafetyMargin)
	if byAge.Before(byIngress) {
		return byAge
	}
	return byIngress
}

func (s Service) RetryPartnerReport(ctx context.Context, operator Operator, id int64, reason string) (OperationResult, error) {
	if id <= 0 {
		return OperationResult{}, ErrInvalidOperation
	}
	return s.operate(ctx, operator, reason, func(tx pgx.Tx, actor Operator, reason string) (OperationResult, error) {
		var state, cause string
		var now, next time.Time
		var leased, replay, allowsNoTrace bool
		err := tx.QueryRow(ctx, `
			SELECT r.state,COALESCE(r.reason,''),clock_timestamp(),r.next_attempt_at,r.lease_token IS NOT NULL OR r.lease_until IS NOT NULL,
			       COALESCE(i.report_config->>'replay_supported','false')='true',
			       COALESCE(i.report_config->>'no_trace_encoding','') IN ('omit','empty')
			FROM conversion.partner_reports r JOIN conversion.integrations i ON i.id=r.integration_id
			WHERE r.id=$1 AND i.environment=$2 FOR UPDATE OF r FOR SHARE OF i`, id, s.Environment).
			Scan(&state, &cause, &now, &next, &leased, &replay, &allowsNoTrace)
		if err != nil {
			return OperationResult{}, err
		}
		if leased || state == "confirmed" || state == "pending" && !now.Before(next) ||
			state == "outcome_unknown" && !replay || cause == "no_trace_contract_unconfirmed" && !allowsNoTrace {
			return OperationResult{}, ErrOperationConflict
		}
		if _, err := tx.Exec(ctx, "UPDATE conversion.partner_reports SET state='pending',next_attempt_at=$2,reason=NULL WHERE id=$1", id, now); err != nil {
			return OperationResult{}, err
		}
		subject := strconv.FormatInt(id, 10)
		err = s.audit(ctx, tx, actor, "retry_partner_report", subject, reason, "",
			map[string]any{"state": state, "next_attempt_at": next, "reason": cause},
			map[string]any{"state": "pending", "next_attempt_at": now})
		return OperationResult{ID: subject, Action: "retry_partner_report", NextAttemptAt: &now}, err
	})
}
