package delivery

import (
	"context"
	"errors"
	"time"

	"conversion-backend/internal/paymentpolicy"

	"github.com/jackc/pgx/v5"
)

// EnqueuePartnerPaymentTx keeps the original callback identity, while reserving
// one canonical Purchase per app/order before evaluating a policy or routing.
func (s Service) EnqueuePartnerPaymentTx(ctx context.Context, tx pgx.Tx, event Event, targets []Target, facts paymentpolicy.Facts) (int64, string, error) {
	if event.Name != "Purchase" || facts.App == "" || event.OrderID == "" || facts.PaymentKey != event.OrderID ||
		facts.IntegrationID != event.IntegrationID || facts.Promotion != event.PromotionID {
		return 0, "", errors.New("invalid partner payment identity")
	}
	facts.Environment = s.Environment
	if id, err := findEventIDTx(ctx, tx, s.Environment, event); !errors.Is(err, pgx.ErrNoRows) {
		return id, "", err
	}
	if err := lockPaymentTx(ctx, tx, facts.Scope, facts.PaymentKey); err != nil {
		return 0, "", err
	}
	var originalID int64
	for _, target := range targets {
		if target.Original {
			if target.BlockedReason == "" {
				originalID = target.DestinationID
			}
			break
		}
	}
	// Compare the normalized amount as NUMERIC, so representation differences
	// cannot cause a conflict. The original Pixel is frozen in the callback.
	var id *int64
	var conflict bool
	var now time.Time
	err := tx.QueryRow(ctx, `
		SELECT e.id,COALESCE(e.conflict,false),clock_timestamp()
		FROM (VALUES (1)) anchor(n) LEFT JOIN LATERAL (
		    SELECT id,
		       (amount IS DISTINCT FROM $6::text::numeric OR currency IS DISTINCT FROM $7
		        OR order_type IS DISTINCT FROM NULLIF($8,'')
		        OR promotion_id IS DISTINCT FROM NULLIF($9,'')
		        OR partner_fields->>'pixel_id' IS DISTINCT FROM $10::jsonb->>'pixel_id') AS conflict
		    FROM conversion.events
		    WHERE environment=$1 AND integration_id=$2 AND payment_app_scope=$3 AND source_order_id=$4
		      AND source_event_type=$5 AND event_name='Purchase'
		    ORDER BY id LIMIT 1
		) e ON true`, facts.Environment, facts.IntegrationID, facts.App, facts.PaymentKey, event.SourceType,
		event.Amount, event.Currency, event.OrderType, facts.Promotion, event.PartnerFields).Scan(&id, &conflict, &now)
	if err != nil {
		return 0, "", err
	}
	if id != nil {
		if conflict {
			return *id, paymentpolicy.ErrPaymentConflict.Error(), nil
		}
		return *id, "duplicate_payment_event", nil
	}
	decision, err := assessPaymentTx(ctx, tx, &event, facts, originalID, now)
	if err != nil {
		return 0, "", err
	}
	for i := range targets {
		applyPaymentTargetPolicy(event, decision, &targets[i])
	}
	eventID, err := s.EnqueueTx(ctx, tx, event, targets)
	return eventID, "", err
}

func sinanPaymentEvidenceTx(ctx context.Context, tx pgx.Tx, eventID int64, scope paymentpolicy.Scope) (Event, paymentpolicy.Facts, int64, error) {
	e := Event{IntegrationID: scope.IntegrationID}
	var target int64
	var issue string
	err := tx.QueryRow(ctx, `
		SELECT e.event_name,e.source_order_id,COALESCE(e.order_type,''),COALESCE(e.promotion_id,''),
		       e.occurred_at,e.received_at,e.amount::text,e.currency,e.action_source,e.source_url,e.matching_data,
		       d.destination_id,COALESCE(conversion.sinan_route_issue(e.id,d.destination_id),'partner_route_not_authorized')
		FROM conversion.events e
		JOIN conversion.deliveries d ON d.event_id=e.id AND d.role='original'
		JOIN conversion.destinations x ON x.id=d.destination_id
		WHERE e.id=$1 AND e.environment=$2 AND e.integration_id=$3
		  AND x.pixel_id=e.partner_fields->>'pixel_id' AND e.payment_app_scope=$4
		FOR SHARE OF x`, eventID, scope.Environment, scope.IntegrationID, scope.App).Scan(
		&e.Name, &e.OrderID, &e.OrderType, &e.PromotionID, &e.OccurredAt, &e.ReceivedAt,
		&e.Amount, &e.Currency, &e.ActionSource, &e.SourceURL, &e.MatchingData, &target, &issue)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && issue != "") {
		return Event{}, paymentpolicy.Facts{}, 0, ErrOperationConflict
	}
	scope.Promotion = e.PromotionID
	return e, paymentpolicy.Facts{Scope: scope, PaymentKey: e.OrderID, Kind: e.OrderType}, target, err
}
