package delivery

import (
	"context"
	"errors"
	"strconv"
	"time"

	"conversion-backend/internal/paymentpolicy"

	"github.com/jackc/pgx/v5"
)

const (
	paymentHistorical = "historical"
	paymentPending    = "pending"
	paymentDecided    = "decided"

	paymentAwaitingOriginal = "original_target_unavailable"

	// The event alias is shared by the claim, send and allowed-target queries.
	paymentCanSend = `(e.reason IS DISTINCT FROM 'event_not_allowed' AND (e.payment_policy_state IN ('historical','not_applicable') OR
		(e.payment_policy_state='decided' AND EXISTS (SELECT 1 FROM conversion.payment_policy_decisions p
		 WHERE p.id=e.payment_policy_decision_id AND p.outcome='selected'))))`
)

func lockPaymentTx(ctx context.Context, tx pgx.Tx, scope paymentpolicy.Scope, key string) error {
	// Always acquire integration -> payment -> deliveries. The payment lock
	// also covers a pair of messages before either has a valid original route.
	var id int64
	if err := tx.QueryRow(ctx, `SELECT id FROM conversion.integrations WHERE id=$1 AND environment=$2 FOR SHARE`,
		scope.IntegrationID, scope.Environment).Scan(&id); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('conversion-payment:'||jsonb_build_array($1::bigint,$2::text,$3::text)::text,0))`,
		id, scope.App, key)
	return err
}

func assessPaymentTx(ctx context.Context, tx pgx.Tx, e *Event, facts paymentpolicy.Facts, originalID int64, now time.Time) (*paymentpolicy.Decision, error) {
	e.paymentApp, e.policyState = facts.App, paymentPending
	if e.OccurredAt == nil {
		return nil, nil // EnqueueTx preserves the missing event-time diagnostic.
	}
	if !now.Before(deliveryDeadline(e.ReceivedAt, *e.OccurredAt)) {
		e.failureReason = "deadline_exhausted"
		return nil, nil
	}
	if e.OccurredAt.After(now) {
		e.failureReason = "event_time_in_future"
		return nil, nil
	}
	if e.failureReason != "" {
		return nil, nil
	}
	if err := validatePaymentEvent(*e); err != nil {
		if errors.Is(err, paymentpolicy.ErrFactMissing) {
			e.Reason = err.Error()
		} else {
			e.failureReason = err.Error()
		}
		return nil, nil
	}
	if e.Currency != nil && facts.Currency == "" {
		facts.Currency = *e.Currency
	}
	if e.Amount != nil && facts.AmountMinor == nil {
		amount, err := paymentpolicy.MinorAmount(*e.Amount, facts.Currency)
		if err != nil {
			e.failureReason = "invalid_event_amount"
			return nil, nil
		}
		facts.AmountMinor = &amount
	}
	if originalID == 0 {
		return nil, nil
	}
	facts.Destination = originalID
	decision, err := paymentpolicy.DecideTx(ctx, tx, facts)
	switch {
	case errors.Is(err, paymentpolicy.ErrFactMissing):
		e.Reason = err.Error()
		return nil, nil
	case errors.Is(err, paymentpolicy.ErrPaymentConflict):
		e.failureReason, e.Reason = err.Error(), err.Error()
		return nil, nil
	case err != nil:
		return nil, err
	}
	e.policyState, e.policyDecisionID = paymentDecided, &decision.ID
	if decision.Outcome == paymentpolicy.Skipped {
		e.Reason = decision.Reason
	}
	return &decision, nil
}

func validatePaymentEvent(e Event) error {
	payload := Payload{EventName: e.Name, OccurredAt: e.OccurredAt, ActionSource: e.ActionSource,
		SourceURL: e.SourceURL, MatchingData: e.MatchingData, Amount: e.Amount, Currency: e.Currency}
	err := ValidatePayload(payload)
	if !e.awaitingSubscriptionKind || !errors.Is(err, errInvalidWebsiteContext) {
		return err
	}
	// Check source-independent fields before waiting for a verified action source.
	systemGenerated := "system_generated"
	payload.ActionSource = &systemGenerated
	if err := ValidatePayload(payload); err != nil {
		return err
	}
	return paymentpolicy.ErrFactMissing
}

func applyPaymentTargetPolicy(e Event, decision *paymentpolicy.Decision, target *Target) {
	switch {
	case decision != nil && decision.Outcome == paymentpolicy.Skipped:
		target.SkipReason = decision.Reason
	case e.policyState == paymentPending && target.BlockedReason == "":
		target.BlockedReason = paymentAwaitingOriginal
		if e.Reason == paymentpolicy.ErrFactMissing.Error() {
			target.BlockedReason = e.Reason
		}
	}
}

// Apply the result to any already-normalized sibling events and lock their
// deliveries in ascending ID order. Preserve terminal failures and deadlines.
func applyPaymentPolicyTx(ctx context.Context, tx pgx.Tx, event Event, facts paymentpolicy.Facts, decision *paymentpolicy.Decision, partner bool) error {
	reason := event.Reason
	if event.policyState == paymentHistorical {
		reason = "historical_payment"
	}
	var decisionID *int64
	var skip bool
	if decision != nil {
		reason = decision.Reason
		decisionID = &decision.ID
		skip = decision.Outcome == paymentpolicy.Skipped
	}
	rows, err := tx.Query(ctx, `
		WITH events AS (
		    UPDATE conversion.events SET payment_policy_state=$8,payment_policy_decision_id=$5,
		        attribution_state='matched',
		        action_source=CASE WHEN $10 AND event_name='Purchase' THEN 'system_generated' ELSE action_source END,
		        source_url=CASE WHEN $10 AND event_name='Purchase' THEN NULL ELSE source_url END,
		        reason=CASE WHEN $6 OR $8<>'decided' THEN $7 ELSE NULL END
		    WHERE environment=$1 AND integration_id=$2 AND payment_app_scope=$3 AND source_order_id=$4
		      AND payment_policy_state='pending'
		    RETURNING id
		), locked AS (
		    SELECT d.id,CASE WHEN $9 AND NOT $6 AND $8<>'pending'
		                THEN COALESCE(conversion.sinan_route_issue(d.event_id,d.destination_id),'') ELSE '' END AS route_issue
		    FROM conversion.deliveries d JOIN events e ON e.id=d.event_id
		    WHERE d.deadline_at>clock_timestamp() AND d.lease_token IS NULL
		      AND ((d.state='blocked' AND d.reason IN ('original_target_unavailable','destination_disabled','rule_fact_missing','partner_route_not_authorized','promotion_pixel_mismatch'))
		           OR ($6 AND d.state='pending'))
		    ORDER BY d.id FOR UPDATE OF d
		), updated AS (
		    UPDATE conversion.deliveries d SET
		        state=CASE WHEN $6 THEN 'skipped' WHEN $8='pending' OR locked.route_issue<>'' THEN 'blocked' ELSE 'pending' END,
		        reason=CASE WHEN $6 OR $8='pending' THEN $7 ELSE NULLIF(locked.route_issue,'') END,
		        finalized_at=CASE WHEN $6 THEN clock_timestamp() ELSE NULL END,
		        next_attempt_at=CASE WHEN $8='pending' THEN d.next_attempt_at ELSE clock_timestamp() END
		    FROM locked WHERE d.id=locked.id
		    RETURNING d.id,d.role
		)
		SELECT id FROM updated WHERE $9 AND $6 AND role='original'`,
		facts.Environment, facts.IntegrationID, facts.App, facts.PaymentKey, decisionID, skip, reason, event.policyState, partner,
		decision != nil && event.recoveredRenewal)
	if err != nil {
		return err
	}
	reports, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return err
	}
	for _, id := range reports {
		if err := createReport(ctx, tx, id); err != nil {
			return err
		}
	}
	return nil
}

func recoverablePaymentRoute(reason string) bool {
	switch reason {
	case paymentAwaitingOriginal, "destination_disabled", "promotion_pixel_mismatch", "partner_route_not_authorized", "rule_fact_missing":
		return true
	default:
		return false
	}
}
func (s Service) retryPendingPaymentTx(ctx context.Context, tx pgx.Tx, actor Operator, deliveryID int64, expected int, reason string) (OperationResult, bool, error) {
	var e Event
	var eventID int64
	var state, source, app string
	err := tx.QueryRow(ctx, `
		SELECT e.id,e.integration_id,e.source_event_id,COALESCE(e.source_order_id,''),e.payment_policy_state,i.source,COALESCE(e.payment_app_scope,'')
		FROM conversion.deliveries d JOIN conversion.events e ON e.id=d.event_id
		JOIN conversion.integrations i ON i.id=e.integration_id AND i.environment=e.environment
		WHERE d.id=$1 AND d.environment=$2`, deliveryID, s.Environment).
		Scan(&eventID, &e.IntegrationID, &e.SourceID, &e.OrderID, &state, &source, &app)
	if err != nil || state != paymentPending {
		return OperationResult{}, false, err
	}
	scope := paymentpolicy.Scope{Environment: s.Environment, IntegrationID: e.IntegrationID, App: app}
	if err := lockPaymentTx(ctx, tx, scope, e.OrderID); err != nil {
		return OperationResult{}, true, err
	}
	if err := tx.QueryRow(ctx, "SELECT payment_policy_state FROM conversion.events WHERE id=$1", eventID).Scan(&state); err != nil {
		return OperationResult{}, true, err
	}
	if state != paymentPending {
		return OperationResult{}, false, nil
	}
	before, err := readRetryState(ctx, tx, deliveryID, s.Environment, false)
	if err != nil {
		return OperationResult{}, true, err
	}
	if before.state != "blocked" || before.attempts != expected || before.leased || !before.now.Add(requestReserve).Before(before.deadline) ||
		!recoverablePaymentRoute(before.reason) {
		return OperationResult{}, true, ErrOperationConflict
	}
	var facts paymentpolicy.Facts
	var decision *paymentpolicy.Decision
	switch source {
	case "starnovel":
		e, err = s.readerPaymentEvidenceTx(ctx, tx, e)
		if err != nil {
			return OperationResult{}, true, err
		}
		var target int64
		target, err = readerAuthorizedOriginalTx(ctx, tx, s.Environment, e.IntegrationID, e.readerPayment.pixel, e.OrderID, e.ReceivedAt, true)
		if err != nil {
			return OperationResult{}, true, err
		}
		e.readerPayment.facts.Environment = s.Environment
		facts = e.readerPayment.facts
		decision, err = assessReaderPaymentTx(ctx, tx, &e, target)
	case "sinan":
		var target int64
		e, facts, target, err = sinanPaymentEvidenceTx(ctx, tx, eventID, scope)
		if err != nil {
			return OperationResult{}, true, err
		}
		decision, err = assessPaymentTx(ctx, tx, &e, facts, target, before.now)
	default:
		return OperationResult{}, true, ErrOperationConflict
	}
	if err != nil {
		return OperationResult{}, true, err
	}
	if e.failureReason != "" || (e.policyState == paymentPending && e.Reason != paymentpolicy.ErrFactMissing.Error()) {
		return OperationResult{}, true, ErrOperationConflict
	}
	if err := applyPaymentPolicyTx(ctx, tx, e, facts, decision, source == "sinan"); err != nil {
		return OperationResult{}, true, err
	}
	after, err := readRetryState(ctx, tx, deliveryID, s.Environment, true)
	if err != nil {
		return OperationResult{}, true, err
	}
	stillBlocked := after.state == "blocked" && after.reason == paymentpolicy.ErrFactMissing.Error()
	if after.attempts != expected || (after.state != "pending" && after.state != "skipped" && !stillBlocked) {
		return OperationResult{}, true, ErrOperationConflict
	}
	out := OperationResult{ID: strconv.FormatInt(deliveryID, 10), Action: "retry_delivery", State: after.state, Reason: after.reason}
	if after.state == "pending" {
		out.NextAttemptAt = &after.next
	}
	err = s.audit(ctx, tx, actor, "retry_delivery", out.ID, reason, "",
		map[string]any{"state": before.state, "reason": before.reason, "attempts": expected},
		map[string]any{"state": after.state, "reason": after.reason, "payment_policy_decision_id": e.policyDecisionID})
	return out, true, err
}
