package delivery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type readerMessage struct {
	key, name, orderID string
	occurredAt         *time.Time
	createdAt          time.Time
	snapshot           []byte
	integrationID      int64
}

type readerSnapshot struct {
	UserID               string  `json:"user_id"`
	Email                string  `json:"email"`
	PromotionID          string  `json:"promotion_id"`
	AttributionState     string  `json:"attribution_state"`
	Reason               string  `json:"reason"`
	OrderType            string  `json:"order_type"`
	TimeSource           string  `json:"time_source"`
	ActionSource         string  `json:"action_source"`
	AmountCents          *int64  `json:"amount_cents"`
	Currency             *string `json:"currency"`
	PaymentPolicyVersion int     `json:"payment_policy_version"`
	PaymentKey           string  `json:"payment_key"`
	PaymentKind          string  `json:"payment_kind"`
	FirstPaid            *bool   `json:"first_paid_on_promotion"`
	Attribution          struct {
		PixelID   string `json:"pixel_id"`
		FBP       string `json:"fbp"`
		FBC       string `json:"fbc"`
		IP        string `json:"ip_address"`
		UserAgent string `json:"user_agent"`
		SourceURL string `json:"source_url"`
		Country   string `json:"country"`
	} `json:"attribution"`
}

// ConsumeReader commits each message, event and initial destination set together.
func (s Service) ConsumeReader(ctx context.Context, limit int) (int, error) {
	for processed := 0; processed < limit; processed++ {
		err := s.consumeReaderMessage(ctx)
		if errors.Is(err, errReaderQueueEmpty) {
			return processed, nil
		}
		if err != nil {
			return processed, err
		}
	}
	return limit, nil
}

var errReaderQueueEmpty = errors.New("reader conversion queue empty")

func (s Service) consumeReaderMessage(ctx context.Context) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	message, err := claimReaderMessageTx(ctx, tx, s.Environment)
	if errors.Is(err, pgx.ErrNoRows) {
		return errReaderQueueEmpty
	}
	if err != nil {
		return err
	}
	event, targets, reason, err := normalizeReaderMessage(ctx, tx, message)
	if err != nil {
		return err
	}
	if reason == "" {
		if _, err := s.enqueueReaderTx(ctx, tx, event, targets); err != nil {
			return fmt.Errorf("enqueue reader conversion: %w", err)
		}
	}
	if err := finishReaderMessageTx(ctx, tx, message.key, reason); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func normalizeReaderMessage(ctx context.Context, tx pgx.Tx, message readerMessage) (Event, []Target, string, error) {
	var data readerSnapshot
	if err := json.Unmarshal(message.snapshot, &data); err != nil || data.UserID == "" {
		return Event{}, nil, "invalid_reader_snapshot", nil
	}
	switch message.name {
	case "CompleteRegistration", "InitiateCheckout", "Purchase", "Subscribe":
	default:
		return Event{}, nil, "unsupported_reader_event", nil
	}
	switch data.AttributionState {
	case "matched", "organic", "missing_pixel", "unknown":
	default:
		return Event{}, nil, "invalid_reader_attribution_state", nil
	}

	event, err := readerEvent(ctx, tx, message, data)
	if event.failureReason != "" && event.readerPayment == nil {
		return Event{}, nil, event.failureReason, nil
	}
	if err != nil {
		return Event{}, nil, "", err
	}

	if data.AttributionState != "matched" {
		return event, nil, "", nil
	}
	targets, err := readerTargetsTx(ctx, tx, message.integrationID, data.Attribution.PixelID, "starnovel:"+message.key)
	if err != nil {
		return Event{}, nil, "", err
	}
	if len(targets) == 0 || !targets[0].Original {
		event.AttributionState = "missing_pixel"
		if event.Reason != "event_time_missing" {
			event.Reason = "original_target_unavailable"
		}
	}
	return event, targets, "", nil
}

func readerEvent(ctx context.Context, tx pgx.Tx, message readerMessage, data readerSnapshot) (Event, error) {
	matching := map[string]any{"external_id": []string{digest(data.UserID)}}
	if email := strings.TrimSpace(data.Email); email != "" {
		matching["em"] = []string{digest(email)}
	}
	if country := strings.TrimSpace(data.Attribution.Country); len(country) == 2 {
		matching["country"] = []string{digest(country)}
	}
	if data.Attribution.FBC != "" {
		matching["fbc"] = data.Attribution.FBC
	}
	if data.Attribution.FBP != "" {
		matching["fbp"] = data.Attribution.FBP
	}
	if ip, err := netip.ParseAddr(data.Attribution.IP); err == nil {
		matching["client_ip_address"] = ip.WithZone("").String()
	}
	if data.Attribution.UserAgent != "" {
		matching["client_user_agent"] = data.Attribution.UserAgent
	}
	matchingData, err := json.Marshal(matching)
	if err != nil {
		return Event{}, err
	}
	var sourceURL *string
	if data.ActionSource == "website" {
		sourceURL, err = readerSourceURL(ctx, tx, data.Attribution.SourceURL)
		if err != nil {
			return Event{}, err
		}
	}
	event := Event{IntegrationID: message.integrationID, SourceType: message.name, SourceID: message.key,
		Name: message.name, OrderID: message.orderID, OrderType: data.OrderType, TimeSource: data.TimeSource, PromotionID: data.PromotionID, AttributionState: data.AttributionState,
		OccurredAt: message.occurredAt, ReceivedAt: message.createdAt, Reason: data.Reason, SourceURL: sourceURL,
		ActionSource: &data.ActionSource, MatchingData: matchingData}
	if event.Reason == "" && data.AttributionState != "matched" {
		event.Reason = data.AttributionState
	}
	if data.AmountCents != nil && data.Currency != nil && *data.AmountCents >= 0 && currencyCode(*data.Currency) {
		cents := *data.AmountCents
		amount := fmt.Sprintf("%d.%02d", cents/100, cents%100)
		event.Amount, event.Currency = &amount, data.Currency
	}
	event.readerPayment, err = readerPaymentFacts(message, data, event)
	if err != nil {
		event.failureReason = err.Error()
	}
	return event, nil
}

func currencyCode(value string) bool {
	if len(value) != 3 {
		return false
	}
	for _, char := range value {
		if char < 'A' || char > 'Z' {
			return false
		}
	}
	return true
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(value))))
	return hex.EncodeToString(sum[:])
}

func readerSourceURL(ctx context.Context, tx pgx.Tx, raw string) (*string, error) {
	page, err := url.Parse(raw)
	if err != nil || page.Scheme != "https" || page.Hostname() == "" || page.User != nil || page.Port() != "" {
		return nil, nil
	}
	var allowed bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM system_domains WHERE status=1 AND lower(domain)=$1)`,
		strings.ToLower(page.Hostname())).Scan(&allowed); err != nil || !allowed {
		return nil, err
	}
	page.RawQuery, page.Fragment, page.RawFragment, page.ForceQuery = "", "", "", false
	cleaned := page.String()
	return &cleaned, nil
}

func claimReaderMessageTx(ctx context.Context, tx pgx.Tx, environment string) (readerMessage, error) {
	var m readerMessage
	err := tx.QueryRow(ctx, `
		SELECT o.business_key,o.event_name,COALESCE(o.order_id::text,''),o.occurred_at,o.created_at,o.snapshot,i.id
		FROM reader_conversion_outbox o
		JOIN conversion.integrations i ON i.environment=o.environment AND i.source='starnovel' AND i.account_key='reader'
		WHERE o.environment=$1 AND o.consumed_at IS NULL AND i.enabled
		ORDER BY o.created_at,o.business_key LIMIT 1 FOR UPDATE OF o SKIP LOCKED FOR SHARE OF i`, environment).
		Scan(&m.key, &m.name, &m.orderID, &m.occurredAt, &m.createdAt, &m.snapshot, &m.integrationID)
	return m, err
}

func readerTargetsTx(ctx context.Context, tx pgx.Tx, integrationID int64, pixel, metaID string) ([]Target, error) {
	rows, err := tx.Query(ctx, `
		SELECT x.id,x.pixel_id=$2,x.enabled AND x.retired_at IS NULL
		FROM conversion.integration_destinations r JOIN conversion.destinations x ON x.id=r.destination_id
		WHERE r.integration_id=$1 AND (x.pixel_id=$2 OR (r.default_route AND x.enabled AND x.retired_at IS NULL))
		ORDER BY (x.pixel_id=$2) DESC,x.id FOR SHARE OF x`, integrationID, pixel)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Target, error) {
		target := Target{MetaEventID: metaID}
		var enabled bool
		err := row.Scan(&target.DestinationID, &target.Original, &enabled)
		if target.Original {
			target.ExistingBinding = true
			if !enabled {
				target.BlockedReason = "destination_disabled"
			}
		}
		return target, err
	})
}

func finishReaderMessageTx(ctx context.Context, tx pgx.Tx, key, reason string) error {
	tag, err := tx.Exec(ctx, `
		WITH repair_fact AS (
			UPDATE reader_conversion_repair_facts SET normalized_at=clock_timestamp()
			WHERE business_key=$1 AND $2='' AND normalized_at IS NULL
		)
		UPDATE reader_conversion_outbox SET consumed_at=clock_timestamp(),processing_error=NULLIF($2,'')
		WHERE business_key=$1 AND consumed_at IS NULL`, key, reason)
	return requireOne(tag.RowsAffected(), err)
}
