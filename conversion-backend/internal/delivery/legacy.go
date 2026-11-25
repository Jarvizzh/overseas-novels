package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type LegacyEvidence struct {
	Environment string `json:"environment"`
	Reference   string `json:"reference"`
}

type LegacyEventBatch struct {
	After      string         `json:"after"`
	Imported   int            `json:"imported"`
	Resolution map[string]int `json:"resolution"`
}

type legacyMessage struct {
	ID, Name, State, OrderType string
	OrderID, UserID            int64
	Snapshot                   []byte
	CreatedAt, NextAttemptAt   time.Time
	DeliveredAt                *time.Time
	Attempts                   int
	HasLease, ActiveLease      bool
}

// ImportLegacyEvents imports one bounded batch and advances only after commit.
func (s Service) ImportLegacyEvents(ctx context.Context, after string, limit int, evidence map[string]LegacyEvidence) (LegacyEventBatch, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return LegacyEventBatch{}, err
	}
	defer tx.Rollback(ctx)
	integrationID, err := s.legacyIntegrationTx(ctx, tx)
	if err != nil {
		return LegacyEventBatch{}, err
	}
	rows, err := listLegacyMessagesTx(ctx, tx, after, limit)
	if err != nil {
		return LegacyEventBatch{}, err
	}
	batch := LegacyEventBatch{After: after, Resolution: make(map[string]int)}
	for _, row := range rows {
		resolution, err := s.importLegacyEventTx(ctx, tx, integrationID, row, evidence[row.ID])
		if err != nil {
			return LegacyEventBatch{}, fmt.Errorf("import legacy event %q: %w", row.ID, err)
		}
		batch.After = row.ID
		batch.Imported++
		batch.Resolution[resolution]++
	}
	return batch, tx.Commit(ctx)
}

func (s Service) legacyIntegrationTx(ctx context.Context, tx pgx.Tx) (int64, error) {
	// A single cutover transaction at a time, including across environments.
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(1930717)"); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO conversion.integrations (source,account_key,environment,contract_version,allowed_events)
        VALUES ('starnovel','reader',$1,'v1',ARRAY['CompleteRegistration','InitiateCheckout','Purchase','Subscribe']) ON CONFLICT (environment,source,account_key) DO NOTHING`,
		s.Environment); err != nil {
		return 0, err
	}
	var id int64
	err := tx.QueryRow(ctx, `
		SELECT id FROM conversion.integrations
		WHERE environment=$1 AND source='starnovel' AND account_key='reader' FOR SHARE`, s.Environment).Scan(&id)
	return id, err
}

func listLegacyMessagesTx(ctx context.Context, tx pgx.Tx, after string, limit int) ([]legacyMessage, error) {
	rows, err := tx.Query(ctx, `
		SELECT o.event_id,o.event_name,o.order_id,o.state,o.attempts,o.created_at,o.next_attempt_at,
		       o.delivered_at,o.lease_token IS NOT NULL OR o.lease_until IS NOT NULL,
		       COALESCE(o.lease_until > now(),false),o.payload,COALESCE(p.order_type,''),p.user_id
		FROM payment_event_outbox o JOIN recharge_orders p ON p.id=o.order_id
		WHERE o.event_id > $1 AND NOT EXISTS (
			SELECT 1 FROM conversion.legacy_outbox_imports m WHERE m.legacy_event_id=o.event_id)
		ORDER BY o.event_id LIMIT $2 FOR UPDATE OF o`, after, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (legacyMessage, error) {
		var m legacyMessage
		err := row.Scan(&m.ID, &m.Name, &m.OrderID, &m.State, &m.Attempts, &m.CreatedAt, &m.NextAttemptAt,
			&m.DeliveredAt, &m.HasLease, &m.ActiveLease, &m.Snapshot, &m.OrderType, &m.UserID)
		return m, err
	})
}

func (s Service) importLegacyEventTx(ctx context.Context, tx pgx.Tx, integrationID int64, row legacyMessage, evidence LegacyEvidence) (string, error) {
	if row.ActiveLease {
		return "", errors.New("legacy delivery still has an active lease")
	}
	if row.Name != "Purchase" && row.Name != "Subscribe" {
		return "", errors.New("unsupported historical event name")
	}
	var payload struct {
		readerSnapshot
		ID   string `json:"event_id"`
		Name string `json:"event_name"`
		Time int64  `json:"event_time"`
	}
	snapshotOK := json.Unmarshal(row.Snapshot, &payload) == nil &&
		payload.ID == row.ID && payload.Name == row.Name && payload.UserID == strconv.FormatInt(row.UserID, 10)
	snapshot := payload.readerSnapshot
	if !snapshotOK {
		// Retain structural evidence in the ledger without trusting inconsistent payload fields.
		snapshot = readerSnapshot{}
	}
	snapshot.OrderType, snapshot.ActionSource = row.OrderType, "website"
	snapshot.AttributionState, snapshot.TimeSource = "unknown", "legacy_outbox_payload"
	if pixelPattern.MatchString(snapshot.Attribution.PixelID) {
		snapshot.AttributionState = "matched"
	} else if snapshot.Attribution.PixelID != "" {
		snapshot.AttributionState = "missing_pixel"
	}
	message := readerMessage{key: row.ID, name: row.Name, orderID: strconv.FormatInt(row.OrderID, 10),
		createdAt: row.CreatedAt, integrationID: integrationID}
	if snapshotOK && payload.Time > 0 && payload.Time <= 253402300799 {
		occurredAt := time.Unix(payload.Time, 0).UTC()
		message.occurredAt = &occurredAt
	}
	event, err := readerEvent(ctx, tx, message, snapshot)
	if err != nil {
		return "", err
	}
	if !snapshotOK {
		event.MatchingData = nil
	}
	pixel := snapshot.Attribution.PixelID
	if !pixelPattern.MatchString(pixel) {
		pixel = ""
	}
	destination, err := legacyDestinationTx(ctx, tx, integrationID, pixel)
	if err != nil {
		return "", err
	}
	var verifiedReference string
	if evidence.Environment == s.Environment {
		verifiedReference = strings.TrimSpace(evidence.Reference)
	}
	deadline := event.ReceivedAt.Add(deliveryLifetime)
	if event.OccurredAt != nil {
		windowEnd := event.OccurredAt.Add(metaWindow - metaSafetyMargin)
		if windowEnd.Before(deadline) {
			deadline = windowEnd
		}
	}

	now := time.Now()
	state, reason := legacyState(row, verifiedReference != "", snapshotOK, message.occurredAt != nil, deadline, now)
	if destination == nil && snapshot.AttributionState == "matched" {
		event.AttributionState = "missing_pixel"
	}
	event.Reason = reason
	if destination == nil {
		event.Reason = "legacy_original_target_unavailable"
	}
	id, _, err := insertEventTx(ctx, tx, s.Environment, event)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", errors.New("existing event without legacy import ledger requires reconciliation")
	}
	if err != nil {
		return "", err
	}
	resolution := state
	if destination != nil {
		err = insertLegacyDeliveryTx(ctx, tx, id, *destination, s.Environment, row, deadline, state, reason, now)
	} else {
		resolution += "_without_target"
	}
	if err != nil {
		return "", err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO conversion.legacy_outbox_imports
		 (legacy_event_id,event_id,legacy_state,legacy_attempts,legacy_delivered_at,resolution,environment_evidence,
		  legacy_pixel_id,legacy_next_attempt_at,legacy_had_lease)
		VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,''),NULLIF($8,''),$9,$10)`,
		row.ID, id, row.State, row.Attempts, row.DeliveredAt, resolution, verifiedReference, pixel, row.NextAttemptAt, row.HasLease)
	return resolution, err
}

func legacyState(row legacyMessage, verified, snapshotOK, hasTime bool, deadline, now time.Time) (state, reason string) {
	switch {
	case row.State == "delivered" && row.DeliveredAt != nil:
		return "succeeded", "legacy_confirmed"
	case row.Attempts > 0 || row.HasLease || row.DeliveredAt != nil:
		if !verified {
			return "blocked", "legacy_environment_unverified"
		}
		return "outcome_unknown", "meta_no_confirmation"
	case !snapshotOK:
		return "blocked", "legacy_snapshot_unverified"
	case !verified:
		return "blocked", "legacy_environment_unverified"
	case row.State != "pending":
		return "blocked", "legacy_state_unverified"
	case !hasTime:
		return "failed", "event_time_missing"
	case !maxTime(now, row.NextAttemptAt).Before(deadline):
		return "failed", "deadline_exhausted"
	default:
		return "pending", ""
	}
}

func legacyDestinationTx(ctx context.Context, tx pgx.Tx, integrationID int64, pixel string) (*int64, error) {
	var id int64
	err := tx.QueryRow(ctx, `
		SELECT x.id FROM conversion.integration_destinations r
		JOIN conversion.destinations x ON x.id=r.destination_id
		WHERE r.integration_id=$1 AND x.pixel_id=$2 FOR SHARE OF r,x`, integrationID, pixel).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &id, err
}

func insertLegacyDeliveryTx(ctx context.Context, tx pgx.Tx, id, destinationID int64, environment string,
	row legacyMessage, deadline time.Time, state, reason string, now time.Time,
) error {
	var finalizedAt *time.Time
	if state == "succeeded" {
		finalizedAt = row.DeliveredAt
	} else if state == "failed" {
		finalizedAt = &now
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO conversion.deliveries (
			event_id,destination_id,environment,meta_event_id,role,created_at,deadline_at,state,reason,
			next_attempt_at,finalized_at,attempts,retry_budget_base_attempt,unknown_attempt_no,frozen_route)
		VALUES($1,$2,$3,$4,'original',$5,$6,$7,NULLIF($8,''),$9,$10,$11,0,
			CASE WHEN $11>0 AND $7 IN ('blocked','outcome_unknown') THEN $11 END,
			jsonb_build_object('legacy_import',true))`,
		id, destinationID, environment, row.ID, row.CreatedAt, deadline, state, reason, row.NextAttemptAt,
		finalizedAt, row.Attempts)
	return err
}
