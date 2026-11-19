package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

type Event struct {
	IntegrationID    int64
	SourceType       string
	SourceID         string
	Name             string
	OrderID          string
	OrderType        string
	TimeSource       string
	PromotionID      string
	AttributionState string
	Reason           string
	OccurredAt       *time.Time
	ReceivedAt       time.Time
	Amount           *string
	Currency         *string
	ActionSource     *string
	SourceURL        *string
	MatchingData     json.RawMessage
	PartnerFields    json.RawMessage
}

type Target struct {
	DestinationID   int64
	MetaEventID     string
	Original        bool
	ExistingBinding bool
	// AuthorizedInboxID preserves an accepted original target after route revocation.
	AuthorizedInboxID int64
	SkipReason        string
	BlockedReason     string
	FrozenRoute       json.RawMessage
}

const (
	deliveryLifetime = 24 * time.Hour
	metaWindow       = 7 * 24 * time.Hour
	metaSafetyMargin = time.Minute
)

// EnqueueTx is called within the same transaction that consumes the source.
// A replay keeps the original event and target selection.
func (s Service) EnqueueTx(ctx context.Context, tx pgx.Tx, event Event, targets []Target) (int64, error) {
	if event.ReceivedAt.IsZero() || event.SourceID == "" || event.SourceType == "" {
		return 0, errors.New("missing source event identity or received time")
	}
	id, createdAt, err := insertEventTx(ctx, tx, s.Environment, event)
	if errors.Is(err, pgx.ErrNoRows) {
		return findEventIDTx(ctx, tx, s.Environment, event)
	}
	if err != nil {
		return 0, err
	}

	deadline := event.ReceivedAt.Add(deliveryLifetime)
	if event.OccurredAt != nil {
		windowEnd := event.OccurredAt.Add(metaWindow - metaSafetyMargin)
		if windowEnd.Before(deadline) {
			deadline = windowEnd
		}
	}
	originalID := int64(0)
	for _, target := range targets {
		if target.Original {
			if originalID != 0 && originalID != target.DestinationID {
				return 0, errors.New("multiple original destinations")
			}
			originalID = target.DestinationID
		}
	}
	seen := make(map[int64]struct{}, len(targets))
	for _, target := range targets {
		if target.DestinationID == originalID && !target.Original {
			continue
		}
		if _, alreadyAdded := seen[target.DestinationID]; alreadyAdded {
			continue
		}
		seen[target.DestinationID] = struct{}{}
		state, reason := "pending", ""
		switch {
		case target.SkipReason != "":
			state, reason = "skipped", target.SkipReason
		case event.OccurredAt == nil:
			state, reason = "failed", "event_time_missing"
		case !createdAt.Before(deadline):
			state, reason = "failed", "deadline_exhausted"
		case target.BlockedReason != "":
			state, reason = "blocked", target.BlockedReason
		}
		deliveryID, err := insertTargetTx(ctx, tx, id, s.Environment, target, deadline, state, reason)
		if err != nil {
			return 0, err
		}
		if state == "skipped" || state == "failed" {
			if err := createReport(ctx, tx, deliveryID); err != nil {
				return 0, err
			}
		}
	}
	return id, nil
}

func insertEventTx(ctx context.Context, tx pgx.Tx, environment string, event Event) (int64, time.Time, error) {
	var id int64
	var createdAt time.Time
	err := tx.QueryRow(ctx, `
		INSERT INTO conversion.events
			(integration_id, environment, source_event_type, source_event_id, event_name, source_order_id,
			 promotion_id, attribution_state, reason, occurred_at, received_at, amount, currency,
			 action_source, source_url, matching_data, partner_fields, order_type, time_source)
		VALUES ($1,$2,$3,$4,$5,NULLIF($6,''),NULLIF($7,''),$8,NULLIF($9,''),$10,$11,$12::text::numeric,
		        $13,$14,$15,COALESCE($16,'{}'::jsonb),$17,NULLIF($18,''),NULLIF($19,''))
		ON CONFLICT (integration_id, source_event_type, source_event_id) DO NOTHING
		RETURNING id, created_at`, event.IntegrationID, environment, event.SourceType, event.SourceID, event.Name,
		event.OrderID, event.PromotionID, event.AttributionState, event.Reason, event.OccurredAt, event.ReceivedAt,
		event.Amount, event.Currency, event.ActionSource, event.SourceURL, event.MatchingData, event.PartnerFields, event.OrderType, event.TimeSource).
		Scan(&id, &createdAt)
	return id, createdAt, err
}

func findEventIDTx(ctx context.Context, tx pgx.Tx, environment string, event Event) (int64, error) {
	var id int64
	err := tx.QueryRow(ctx, `
		SELECT id FROM conversion.events
		WHERE integration_id = $1 AND source_event_type = $2 AND source_event_id = $3 AND environment = $4`,
		event.IntegrationID, event.SourceType, event.SourceID, environment).Scan(&id)
	return id, err
}

func insertTargetTx(ctx context.Context, tx pgx.Tx, eventID int64, environment string, target Target, deadline time.Time, state, reason string) (int64, error) {
	role := "additional"
	if target.Original {
		role = "original"
	}
	var id int64
	filter := "r.destination_id IS NOT NULL"
	args := []any{eventID, target.DestinationID, environment, target.MetaEventID, role, target.FrozenRoute, deadline, state, reason, target.ExistingBinding}
	if target.AuthorizedInboxID > 0 && target.Original {
		filter = `(r.destination_id IS NOT NULL OR EXISTS (
			SELECT 1 FROM conversion.inbox b WHERE b.id=$11 AND b.state='pending'
			  AND b.integration_id=e.integration_id AND b.source_event_type=e.source_event_type
			  AND b.source_event_id=e.source_event_id AND b.payload->>'original_destination_id'=x.id::text))`
		args = append(args, target.AuthorizedInboxID)
	}
	err := tx.QueryRow(ctx, `
		INSERT INTO conversion.deliveries
			(event_id, destination_id, environment, meta_event_id, role, frozen_route, deadline_at, state, reason, finalized_at)
		SELECT e.id, x.id, e.environment, $4, $5, COALESCE($6,'{}'::jsonb), $7, $8, NULLIF($9,''),
		       CASE WHEN $8 IN ('skipped','failed') THEN now() END
		FROM conversion.events e
		JOIN conversion.destinations x ON x.id=$2 AND x.environment=e.environment
		LEFT JOIN conversion.integration_destinations r ON r.integration_id=e.integration_id AND r.destination_id=x.id
		WHERE e.id=$1 AND e.environment=$3 AND ($10 OR (x.enabled AND x.retired_at IS NULL))
		AND `+filter+`
		RETURNING id`, args...).Scan(&id)
	return id, err
}
