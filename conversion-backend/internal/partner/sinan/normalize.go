package sinan

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"conversion-backend/internal/delivery"
	"conversion-backend/internal/partner"

	"github.com/jackc/pgx/v5"
	"golang.org/x/text/currency"
)

func Consume(ctx context.Context, svc delivery.Service, limit int) (int, error) {
	if svc.Environment != TestEnvironment {
		return 0, nil
	}
	return partner.Consume(ctx, svc, "sinan", limit, normalizeInbox)
}

func normalizeInbox(ctx context.Context, tx pgx.Tx, j partner.InboxJob) (partner.Normalized, error) {
	name, supported := eventNames[j.SourceType]
	var snapshot inboxPayload
	if !supported || json.Unmarshal(j.Payload, &snapshot) != nil || snapshot.DestinationID <= 0 ||
		snapshot.Callback.EventID != j.SourceID || snapshot.Callback.invalidReason(j.ReceivedAt, j.SourceType) != "" {
		return partner.Normalized{Quarantine: "invalid_normalization_snapshot"}, nil
	}
	var config Config
	if err := json.Unmarshal(j.Config, &config); err != nil {
		return partner.Normalized{}, err
	}

	p := snapshot.Callback
	event := normalizeEvent(p, name, j.SourceType, j.IntegrationID, j.ReceivedAt)
	authorized := j.Enabled && j.Contract == MockContract && slices.Contains(j.Apps, string(p.AppID)) && slices.Contains(j.Events, j.SourceType)
	targets, err := route(ctx, tx, TestEnvironment, j.IntegrationID, j.ID, snapshot, config, authorized, j.SourceType)
	if err != nil {
		return partner.Normalized{}, err
	}
	return partner.Normalized{Event: event, Targets: targets}, nil
}

func normalizeEvent(p callback, name, eventType string, integration int64, received time.Time) delivery.Event {
	match, _ := json.Marshal(p.matchingData())
	fields, _ := json.Marshal(p.sourceFields)
	occurred := time.Unix(p.EventTime, 0).UTC()
	source := "website"
	event := delivery.Event{
		IntegrationID: integration, SourceType: eventType, SourceID: p.EventID, Name: name,
		OrderID: string(p.OrderID), PromotionID: string(p.PromotionID), TimeSource: "partner_event_time",
		OccurredAt: &occurred, ReceivedAt: received, AttributionState: "matched",
		ActionSource: &source, MatchingData: match, PartnerFields: fields,
	}
	if p.SourceURL != "" {
		event.SourceURL = &p.SourceURL
	}
	if amount := settlementAmount(p); amount != "" {
		event.Amount, event.Currency = &amount, &p.Currency
	}
	if eventType == "recharge-paid" {
		event.OrderType = strconv.Itoa(p.RechargeType)
	}
	return event
}

func settlementAmount(p callback) string {
	if !money.MatchString(string(p.Amount)) || p.Currency == "XXX" {
		return ""
	}
	unit, err := currency.ParseISO(p.Currency)
	if err != nil {
		return ""
	}
	scale, _ := currency.Standard.Rounding(unit)
	integer, fraction, _ := strings.Cut(string(p.Amount), ".")
	if len(fraction) > scale {
		return ""
	}
	if scale == 0 {
		return integer
	}
	return integer + "." + fraction + strings.Repeat("0", scale-len(fraction))
}

func route(ctx context.Context, tx pgx.Tx, environment string, integration, inboxID int64, p inboxPayload, config Config, enabled bool, eventType string) ([]delivery.Target, error) {
	eventID := delivery.SourceMetaID(integration, eventType, p.Callback.EventID)
	original := delivery.Target{
		DestinationID: p.DestinationID, MetaEventID: eventID, Original: true,
		ExistingBinding: true, AuthorizedInboxID: inboxID,
		FrozenRoute: []byte(`{"reason":"original_callback_pixel","contract_version":"` + MockContract + `"}`),
	}

	var active, authorized bool
	err := tx.QueryRow(ctx, `
		SELECT x.enabled AND x.retired_at IS NULL, r.destination_id IS NOT NULL
		FROM conversion.destinations x
		LEFT JOIN conversion.integration_destinations r ON r.destination_id=x.id AND r.integration_id=$1
		WHERE x.id=$2 AND x.environment=$3 AND x.pixel_id=$4 FOR SHARE OF x`,
		integration, p.DestinationID, environment, p.Callback.PixelID).Scan(&active, &authorized)
	if err != nil {
		return nil, errors.Join(errors.New("original Sinan destination disappeared"), err)
	}
	var mismatch bool
	if local := localPromotionID(p.Callback.PromotionID); local != nil {
		err = tx.QueryRow(ctx, "SELECT conversion.promotion_pixel_mismatch($1,$2)", *local, p.Callback.PixelID).Scan(&mismatch)
		if err != nil {
			return nil, err
		}
	}
	switch {
	case !enabled || !authorized || !config.allows(string(p.Callback.AppID), string(p.Callback.PixelID)):
		original.BlockedReason = "partner_route_not_authorized"
	case mismatch:
		original.BlockedReason = "promotion_pixel_mismatch"
	case !active:
		original.BlockedReason = "destination_disabled"
	}
	targets := []delivery.Target{original}
	if original.BlockedReason != "" && original.BlockedReason != "destination_disabled" {
		return targets, nil
	}
	rows, err := tx.Query(ctx, `
		SELECT x.id,x.pixel_id FROM conversion.integration_destinations r
		JOIN conversion.destinations x ON x.id=r.destination_id
		WHERE r.integration_id=$1 AND r.default_route AND x.id<>$2 AND x.enabled AND x.retired_at IS NULL
		ORDER BY x.id FOR SHARE OF x`, integration, p.DestinationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var destination int64
		var pixel string
		if err := rows.Scan(&destination, &pixel); err != nil {
			return nil, err
		}
		if config.allows(string(p.Callback.AppID), pixel) {
			targets = append(targets, delivery.Target{
				DestinationID: destination, MetaEventID: eventID,
				FrozenRoute: []byte(`{"reason":"integration_default_route"}`),
			})
		}
	}
	return targets, rows.Err()
}
