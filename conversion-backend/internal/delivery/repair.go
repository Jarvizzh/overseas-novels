package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

type RepairRequest struct {
	OrderID   int64
	EventName string
	Apply     bool
	Evidence  string
	Reason    string
}

type repairFacts struct {
	key, pixel                 string
	occurredAt, createdAt, now time.Time
	snapshot                   []byte
	normalized, sent           bool
}

func (s Service) RepairMissing(ctx context.Context, operator Operator, input RepairRequest) (OperationResult, error) {
	if input.OrderID <= 0 || (input.EventName != "Purchase" && input.EventName != "Subscribe") {
		return OperationResult{}, ErrInvalidOperation
	}
	if !input.Apply {
		tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly, IsoLevel: pgx.RepeatableRead})
		if err != nil {
			return OperationResult{}, err
		}
		defer tx.Rollback(ctx)
		return s.repairTx(ctx, tx, operator, input)
	}
	evidence, err := cleanReason(input.Evidence)
	if err != nil {
		return OperationResult{}, err
	}
	input.Evidence = evidence
	return s.operate(ctx, operator, input.Reason, func(tx pgx.Tx, actor Operator, reason string) (OperationResult, error) {
		input.Reason = reason
		return s.repairTx(ctx, tx, actor, input)
	})
}

func (s Service) repairTx(ctx context.Context, tx pgx.Tx, actor Operator, input RepairRequest) (OperationResult, error) {
	var eligibility json.RawMessage
	var subID *string
	query := "SELECT capi_expectation,subscription_id FROM recharge_orders WHERE id=$1"
	if input.Apply {
		query = "SELECT capi_expectation,subscription_id FROM conversion.lock_repair_order($1)"
	}
	if err := tx.QueryRow(ctx, query, input.OrderID).Scan(&eligibility, &subID); err != nil {
		return OperationResult{}, err
	}
	var expected struct {
		Environment string     `json:"environment"`
		Pixel       string     `json:"pixel_id"`
		OccurredAt  *time.Time `json:"occurred_at"`
		TimeSource  string     `json:"time_source"`
		Events      []struct {
			Name string `json:"name"`
			Key  string `json:"business_key"`
		} `json:"events"`
	}
	if json.Unmarshal(eligibility, &expected) != nil || expected.Environment != s.Environment ||
		!pixelPattern.MatchString(expected.Pixel) || expected.OccurredAt == nil ||
		(expected.TimeSource != "paypal_capture_create_time" && expected.TimeSource != "paypal_subscription_ledger") {
		return OperationResult{}, ErrOperationConflict
	}
	orderID := strconv.FormatInt(input.OrderID, 10)
	key := "payment:" + orderID + ":Purchase"
	if input.EventName == "Subscribe" {
		if subID == nil {
			return OperationResult{}, ErrOperationConflict
		}
		var claimed bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM user_subscriptions
			WHERE subscription_id=$1 AND capi_subscribe_order_id=$2 AND capi_subscribe_state='claimed')`, *subID, input.OrderID).Scan(&claimed); err != nil {
			return OperationResult{}, err
		}
		if !claimed {
			return OperationResult{}, ErrOperationConflict
		}
		key = "subscription:" + *subID + ":Subscribe"
	}
	qualified := false
	for _, event := range expected.Events {
		qualified = qualified || event.Name == input.EventName && event.Key == key
	}
	if !qualified {
		return OperationResult{}, ErrOperationConflict
	}
	fact, err := readRepairFact(ctx, tx, input, s.Environment, key, expected.Pixel, *expected.OccurredAt)
	if err != nil {
		return OperationResult{}, err
	}
	return s.restoreRepairFact(ctx, tx, actor, input, fact, orderID)
}

func readRepairFact(ctx context.Context, tx pgx.Tx, input RepairRequest, environment, key, pixel string, happened time.Time) (repairFacts, error) {
	fact := repairFacts{key: key, pixel: pixel, occurredAt: happened}
	sql := `SELECT created_at,clock_timestamp(),snapshot,normalized_at IS NOT NULL,original_send_started_at IS NOT NULL
		FROM reader_conversion_repair_facts WHERE business_key=$1 AND order_id=$2 AND environment=$3
		  AND event_name=$4 AND occurred_at=$5 AND snapshot->'attribution'->>'pixel_id'=$6`
	if input.Apply {
		sql += " FOR UPDATE"
	}
	err := tx.QueryRow(ctx, sql, key, input.OrderID, environment, input.EventName, happened, pixel).
		Scan(&fact.createdAt, &fact.now, &fact.snapshot, &fact.normalized, &fact.sent)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrOperationConflict
	}
	return fact, err
}

func (s Service) restoreRepairFact(ctx context.Context, tx pgx.Tx, actor Operator, input RepairRequest, fact repairFacts, orderID string) (OperationResult, error) {
	var integrationID int64
	err := tx.QueryRow(ctx, `SELECT id FROM conversion.integrations
		WHERE environment=$1 AND source='starnovel' AND account_key='reader'`, s.Environment).Scan(&integrationID)
	if err != nil {
		return OperationResult{}, err
	}
	var eventID, originalID *int64
	var originalPixel *string
	var queued, consumed, legacy bool
	err = tx.QueryRow(ctx, `
		SELECT e.id,d.id,x.pixel_id,
		       q.business_key IS NOT NULL,q.consumed_at IS NOT NULL,
		       EXISTS(SELECT 1 FROM conversion.legacy_outbox_imports WHERE legacy_event_id=$2)
		         OR EXISTS(SELECT 1 FROM payment_event_outbox WHERE event_id=$2)
		FROM (VALUES (1)) anchor(n)
		LEFT JOIN conversion.events e ON e.integration_id=$1 AND e.source_event_type=$3 AND e.source_event_id=$2
		LEFT JOIN conversion.deliveries d ON d.event_id=e.id AND d.role='original'
		LEFT JOIN conversion.destinations x ON x.id=d.destination_id
		LEFT JOIN reader_conversion_outbox q ON q.business_key=$2 AND q.environment=$4`,
		integrationID, fact.key, input.EventName, s.Environment).
		Scan(&eventID, &originalID, &originalPixel, &queued, &consumed, &legacy)
	if err != nil {
		return OperationResult{}, err
	}
	out := OperationResult{ID: fact.key, DryRun: !input.Apply}
	switch {
	case originalID != nil && originalPixel != nil && *originalPixel == fact.pixel:
		out.ID, out.Action, out.Existing = strconv.FormatInt(*originalID, 10), "restore_original", true
		return out, nil
	case queued && !consumed && eventID == nil:
		out.Action, out.Existing = "restore_outbox", true
		return out, nil
	case originalID != nil || legacy || fact.sent || (eventID == nil && (consumed || fact.normalized)) ||
		(eventID != nil && !fact.normalized):
		return OperationResult{}, ErrOperationConflict
	}
	deadline := deliveryDeadline(fact.createdAt, fact.occurredAt)
	if fact.occurredAt.After(fact.now) || !fact.now.Add(requestReserve).Before(deadline) {
		return OperationResult{}, ErrOperationConflict
	}
	var data readerSnapshot
	if json.Unmarshal(fact.snapshot, &data) != nil || data.AttributionState != "matched" || data.UserID == "" {
		return OperationResult{}, ErrOperationConflict
	}
	message := readerMessage{key: fact.key, name: input.EventName, orderID: orderID, occurredAt: &fact.occurredAt,
		createdAt: fact.createdAt, integrationID: integrationID}
	event, err := readerEvent(ctx, tx, message, data)
	if err != nil {
		return OperationResult{}, err
	}
	if ValidatePayload(Payload{EventName: event.Name, OccurredAt: event.OccurredAt, ActionSource: event.ActionSource,
		SourceURL: event.SourceURL, MatchingData: event.MatchingData, Amount: event.Amount, Currency: event.Currency}) != nil {
		return OperationResult{}, ErrOperationConflict
	}
	var target int64
	targetQuery := `SELECT x.id FROM conversion.integration_destinations r JOIN conversion.destinations x ON x.id=r.destination_id
		WHERE r.integration_id=$1 AND x.pixel_id=$2`
	if input.Apply {
		targetQuery += " FOR SHARE OF r,x"
	}
	err = tx.QueryRow(ctx, targetQuery, integrationID, fact.pixel).Scan(&target)
	if errors.Is(err, pgx.ErrNoRows) {
		return OperationResult{}, ErrOperationConflict
	}
	if err != nil {
		return OperationResult{}, err
	}
	out.Action = "restore_outbox"
	if eventID != nil {
		out.Action = "restore_original"
	}
	if !input.Apply {
		return out, nil
	}
	if eventID == nil {
		_, err = tx.Exec(ctx, `
			INSERT INTO reader_conversion_outbox(business_key,event_name,order_id,environment,occurred_at,created_at,snapshot)
			VALUES($1,$2,$3,$4,$5,$6,$7)`, fact.key, input.EventName, input.OrderID, s.Environment, fact.occurredAt, fact.createdAt, fact.snapshot)
	} else {
		var id int64
		id, err = insertTargetTx(ctx, tx, *eventID, s.Environment, Target{DestinationID: target, MetaEventID: "starnovel:" + fact.key,
			Original: true, ExistingBinding: true}, deadline, "pending", "")
		out.ID = strconv.FormatInt(id, 10)
	}
	if err != nil {
		return OperationResult{}, err
	}
	err = s.audit(ctx, tx, actor, out.Action, fact.key, input.Reason, input.Evidence, map[string]any{"missing": true},
		map[string]any{"id": out.ID, "order_id": orderID, "pixel_id": fact.pixel})
	return out, err
}
