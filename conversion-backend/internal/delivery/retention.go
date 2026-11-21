package delivery

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

type RetentionResult struct {
	Outbox, RepairFacts, Events, Inbox, Diagnostics, LegacyOutbox int64
}

// RedactExpired preserves the evidence used to prevent duplicate sends. Each
// table advances independently within the bounded transaction and selected environment.
func (s Service) RedactExpired(ctx context.Context, actor Operator, days, limit int, reason string) (RetentionResult, error) {
	var result RetentionResult
	if days < 7 || days > 3650 || limit < 1 || limit > 500 {
		return result, ErrInvalidOperation
	}
	_, err := s.operate(ctx, actor, reason, func(tx pgx.Tx, operator Operator, reason string) (OperationResult, error) {
		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(1930720,hashtext($1))", s.Environment); err != nil {
			return OperationResult{}, err
		}
		for _, work := range []struct {
			query string
			count *int64
		}{
			{redactOutbox, &result.Outbox}, {redactRepairFacts, &result.RepairFacts},
			{redactEvents, &result.Events}, {redactInbox, &result.Inbox}, {deleteDiagnostics, &result.Diagnostics},
			{redactLegacyOutbox, &result.LegacyOutbox},
		} {
			tag, err := tx.Exec(ctx, work.query, s.Environment, days, limit)
			if err != nil {
				return OperationResult{}, err
			}
			*work.count = tag.RowsAffected()
		}
		total := result.Outbox + result.RepairFacts + result.Events + result.Inbox + result.Diagnostics + result.LegacyOutbox
		if total > 0 {
			err := s.audit(ctx, tx, operator, "retention_redact", s.Environment, reason, "", map[string]any{"keep_days": days},
				map[string]any{"outbox": result.Outbox, "repair_facts": result.RepairFacts, "events": result.Events,
					"inbox": result.Inbox, "diagnostics": result.Diagnostics, "legacy_outbox": result.LegacyOutbox})
			if err != nil {
				return OperationResult{}, err
			}
		}
		return OperationResult{Action: "retention_redact"}, nil
	})
	if err != nil {
		return RetentionResult{}, err
	}
	return result, nil
}

var ErrRetentionApply = errors.New("retention requires --apply, --operator and --reason")

const redactOutbox = `
	WITH old AS (
		SELECT q.business_key FROM reader_conversion_outbox q
		WHERE q.environment=$1 AND q.consumed_at<now()-make_interval(days=>$2) AND q.snapshot<>'{}'
		ORDER BY q.consumed_at,q.business_key LIMIT $3 FOR UPDATE SKIP LOCKED
	)
	UPDATE reader_conversion_outbox q SET snapshot='{}' FROM old WHERE q.business_key=old.business_key
`

const redactRepairFacts = `
	WITH old AS (
		SELECT f.business_key FROM reader_conversion_repair_facts f
		WHERE f.environment=$1 AND f.created_at<now()-make_interval(days=>$2) AND f.snapshot<>'{}'
		  AND (f.occurred_at IS NULL OR f.occurred_at<=now()-interval '7 days')
		  AND NOT EXISTS(SELECT 1 FROM conversion.integrations i JOIN conversion.events e ON e.integration_id=i.id
		      JOIN conversion.deliveries d ON d.event_id=e.id AND d.finalized_at IS NULL
		      WHERE i.environment=f.environment AND i.source='starnovel' AND i.account_key='reader'
		        AND e.source_event_id=f.business_key AND e.source_event_type=f.event_name)
		ORDER BY f.created_at,f.business_key LIMIT $3 FOR UPDATE OF f SKIP LOCKED
	)
	UPDATE reader_conversion_repair_facts f SET snapshot='{}' FROM old WHERE f.business_key=old.business_key
`

const redactEvents = `
	WITH old AS (
		SELECT e.id FROM conversion.events e
		WHERE e.environment=$1 AND e.created_at<now()-make_interval(days=>$2)
		  AND (e.source_url IS NOT NULL OR e.matching_data<>'{}')
		  AND (e.occurred_at IS NULL OR e.occurred_at<=now()-interval '7 days')
		  AND NOT EXISTS(SELECT 1 FROM conversion.deliveries d WHERE d.event_id=e.id AND d.finalized_at IS NULL)
		ORDER BY e.created_at,e.id LIMIT $3 FOR UPDATE OF e SKIP LOCKED
	)
	UPDATE conversion.events e SET source_url=NULL,matching_data='{}' FROM old WHERE e.id=old.id
`

const redactInbox = `
	WITH old AS (
		SELECT b.id FROM conversion.inbox b JOIN conversion.integrations i ON i.id=b.integration_id
		WHERE i.environment=$1 AND b.received_at<now()-make_interval(days=>$2)
		  AND b.state IN ('consumed','quarantined') AND b.payload IS NOT NULL
		ORDER BY b.received_at,b.id LIMIT $3 FOR UPDATE OF b SKIP LOCKED
	)
	UPDATE conversion.inbox b SET payload=NULL FROM old WHERE b.id=old.id
`

const deleteDiagnostics = `
	WITH old AS (
		SELECT n.delivery_id,n.attempt_no,n.late FROM conversion.delivery_diagnostics n
		JOIN conversion.deliveries d ON d.id=n.delivery_id
		WHERE d.environment=$1 AND d.finalized_at IS NOT NULL AND n.recorded_at<now()-make_interval(days=>$2)
		ORDER BY n.recorded_at,n.delivery_id,n.attempt_no,n.late LIMIT $3 FOR UPDATE OF n SKIP LOCKED
	)
	DELETE FROM conversion.delivery_diagnostics n USING old
	WHERE (n.delivery_id,n.attempt_no,n.late)=(old.delivery_id,old.attempt_no,old.late)
`

const redactLegacyOutbox = `
	WITH old AS (
		SELECT p.event_id FROM payment_event_outbox p JOIN conversion.legacy_outbox_imports m ON m.legacy_event_id=p.event_id
		JOIN conversion.events e ON e.id=m.event_id
		WHERE e.environment=$1 AND p.created_at<now()-make_interval(days=>$2) AND p.payload<>'{}'
		  AND (e.occurred_at IS NULL OR e.occurred_at<=now()-interval '7 days')
		  AND NOT EXISTS(SELECT 1 FROM conversion.deliveries d WHERE d.event_id=e.id AND d.finalized_at IS NULL)
		ORDER BY p.created_at,p.event_id LIMIT $3 FOR UPDATE OF p SKIP LOCKED
	)
	UPDATE payment_event_outbox p SET payload='{}' FROM old WHERE p.event_id=old.event_id
`
