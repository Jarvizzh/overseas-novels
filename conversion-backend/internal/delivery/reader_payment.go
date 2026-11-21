package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"conversion-backend/internal/paymentpolicy"

	"github.com/jackc/pgx/v5"
)

type readerPayment struct {
	facts paymentpolicy.Facts
	pixel string
	order int64
	user  int64
}

func readerPaymentFacts(message readerMessage, data readerSnapshot, event Event) (*readerPayment, error) {
	if data.PaymentPolicyVersion == 0 || (message.name != "Purchase" && message.name != "Subscribe") {
		return nil, nil
	}
	order, err := strconv.ParseInt(data.PaymentKey, 10, 64)
	if data.PaymentPolicyVersion != 1 || err != nil || order <= 0 || data.PaymentKey != message.orderID {
		return nil, errors.New("invalid_reader_payment_snapshot")
	}
	user, userErr := strconv.ParseInt(data.UserID, 10, 64)
	payment := &readerPayment{pixel: data.Attribution.PixelID, order: order, user: user, facts: paymentpolicy.Facts{
		Scope:      paymentpolicy.Scope{IntegrationID: message.integrationID, App: paymentpolicy.ReaderApp, Promotion: data.PromotionID},
		PaymentKey: data.PaymentKey, Kind: data.PaymentKind, First: data.FirstPaid,
	}}
	if data.FirstPaid == nil {
		promotion, promotionErr := strconv.ParseInt(data.PromotionID, 10, 32)
		if userErr == nil && promotionErr == nil && user > 0 && promotion > 0 {
			payment.facts.LookupFirst = func(ctx context.Context, tx pgx.Tx, environment string) (*bool, error) {
				var first *bool
				err := tx.QueryRow(ctx, `SELECT reader_verified_first_payment($1,$2,$3,$4)`,
					environment, order, user, int32(promotion)).Scan(&first)
				return first, err
			}
		}
	}
	if userErr != nil || strconv.FormatInt(user, 10) != data.UserID {
		payment.user = 0
	}
	if event.Currency != nil {
		payment.facts.Currency = *event.Currency
	}
	if event.Amount != nil {
		// Reader stores cents. Dropping insignificant fractional zeros is
		// exact and also supports a whole amount in a zero-decimal currency.
		decimal := strings.TrimSuffix(strings.TrimRight(*event.Amount, "0"), ".")
		amount, err := paymentpolicy.MinorAmount(decimal, payment.facts.Currency)
		if err != nil {
			return nil, errors.New("invalid_event_amount")
		}
		payment.facts.AmountMinor = &amount
	}
	return payment, nil
}

func historicalReaderPaymentTx(ctx context.Context, tx pgx.Tx, scope paymentpolicy.Scope, key string) (bool, time.Time, error) {
	var historical bool
	var now time.Time
	err := tx.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM conversion.events
			WHERE integration_id=$1 AND environment=$2 AND source_order_id=$3 AND event_name IN ('Purchase','Subscribe')
			  AND payment_policy_state='historical')
		    OR EXISTS(SELECT 1 FROM payment_event_outbox
			WHERE order_id=$3::text::bigint AND (state='delivered' OR delivered_at IS NOT NULL)),clock_timestamp()`,
		scope.IntegrationID, scope.Environment, key).Scan(&historical, &now)
	return historical, now, err
}

func (s Service) enqueueReaderTx(ctx context.Context, tx pgx.Tx, e Event, targets []Target) (int64, error) {
	if e.readerPayment == nil {
		return s.EnqueueTx(ctx, tx, e, targets)
	}
	e.readerPayment.facts.Environment = s.Environment
	if err := lockPaymentTx(ctx, tx, e.readerPayment.facts.Scope, e.OrderID); err != nil {
		return 0, err
	}
	if id, err := findEventIDTx(ctx, tx, s.Environment, e); !errors.Is(err, pgx.ErrNoRows) {
		return id, err
	}
	var original int64
	for _, target := range targets {
		if target.Original && target.BlockedReason == "" {
			original = target.DestinationID
			break
		}
	}
	decision, err := assessReaderPaymentTx(ctx, tx, &e, original)
	if err != nil {
		return 0, err
	}
	if decision != nil || e.policyState == paymentHistorical || e.Reason == paymentpolicy.ErrFactMissing.Error() {
		if err := applyPaymentPolicyTx(ctx, tx, e, e.readerPayment.facts, decision, false); err != nil {
			return 0, err
		}
	}
	for i := range targets {
		applyPaymentTargetPolicy(e, decision, &targets[i])
	}
	return s.EnqueueTx(ctx, tx, e, targets)
}

func assessReaderPaymentTx(ctx context.Context, tx pgx.Tx, e *Event, originalID int64) (*paymentpolicy.Decision, error) {
	facts, now, err := prepareReaderPaymentTx(ctx, tx, e)
	if err != nil || e.policyState == paymentHistorical {
		return nil, err
	}
	return assessPaymentTx(ctx, tx, e, facts, originalID, now)
}

// Prepare the same evidence for a decision and a read-only repair preview.
func prepareReaderPaymentTx(ctx context.Context, tx pgx.Tx, e *Event) (paymentpolicy.Facts, time.Time, error) {
	facts := e.readerPayment.facts
	e.paymentApp, e.policyState = facts.App, paymentPending
	historical, now, err := historicalReaderPaymentTx(ctx, tx, facts.Scope, facts.PaymentKey)
	if err != nil {
		return facts, now, err
	}
	if historical {
		e.policyState = paymentHistorical
		if e.Reason == "" || e.Reason == "matched" {
			e.Reason = "historical_payment"
		}
		return facts, now, nil
	}
	if e.failureReason == "" && e.OccurredAt != nil && !e.OccurredAt.After(now) && now.Before(deliveryDeadline(e.ReceivedAt, *e.OccurredAt)) {
		err = resolveReaderSubscriptionKindTx(ctx, tx, e, &facts)
	}
	return facts, now, err
}

func resolveReaderSubscriptionKindTx(ctx context.Context, tx pgx.Tx, e *Event, facts *paymentpolicy.Facts) error {
	payment := e.readerPayment
	if e.OrderType != "subscription" || facts.Kind != "" || payment.user <= 0 {
		return nil
	}
	var kind *string
	err := tx.QueryRow(ctx, `
		SELECT CASE WHEN p.id IS NOT NULL THEN p.payment_kind
		            ELSE reader_verified_subscription_payment_kind($1,$4,$5) END
		FROM (VALUES (1)) anchor(n)
		LEFT JOIN conversion.payment_policy_decisions p
		    ON p.environment=$1 AND p.integration_id=$2 AND p.app_scope=$3 AND p.payment_key=$4::bigint::text`,
		facts.Environment, facts.IntegrationID, facts.App, payment.order, payment.user).Scan(&kind)
	if err != nil || kind == nil {
		if err == nil && e.Name == "Purchase" && e.TimeSource == "paypal_subscription_ledger" {
			e.awaitingSubscriptionKind = true
		}
		return err
	}
	facts.Kind = *kind
	if *kind == "subscription_renewal" {
		source := "system_generated"
		e.ActionSource, e.SourceURL, e.recoveredRenewal = &source, nil, true
	}
	return nil
}

func (s Service) readerPaymentEvidenceTx(ctx context.Context, tx pgx.Tx, event Event) (Event, error) {
	var message readerMessage
	err := tx.QueryRow(ctx, `
		SELECT business_key,event_name,order_id::text,occurred_at,created_at,snapshot
		FROM reader_conversion_repair_facts WHERE business_key=$1 AND environment=$2 AND order_id=$3::text::bigint`,
		event.SourceID, s.Environment, event.OrderID).Scan(&message.key, &message.name, &message.orderID,
		&message.occurredAt, &message.createdAt, &message.snapshot)
	if errors.Is(err, pgx.ErrNoRows) {
		return Event{}, ErrOperationConflict
	}
	if err != nil {
		return Event{}, err
	}
	var snapshot readerSnapshot
	if json.Unmarshal(message.snapshot, &snapshot) != nil || snapshot.UserID == "" || snapshot.AttributionState != "matched" {
		return Event{}, ErrOperationConflict
	}
	message.integrationID = event.IntegrationID
	restored, err := readerEvent(ctx, tx, message, snapshot)
	if err == nil && restored.readerPayment == nil {
		return Event{}, ErrOperationConflict
	}
	return restored, err
}

func readerAuthorizedOriginalTx(ctx context.Context, tx pgx.Tx, environment string, integration int64, pixel string, lock bool) (int64, error) {
	query := `
		SELECT x.id FROM conversion.integration_destinations r
		JOIN conversion.destinations x ON x.id=r.destination_id AND x.environment=r.environment
		JOIN conversion.integrations i ON i.id=r.integration_id AND i.environment=r.environment
		WHERE i.id=$1 AND i.environment=$2 AND i.enabled AND x.pixel_id=$3 AND x.enabled AND x.retired_at IS NULL`
	if lock {
		query += " FOR SHARE OF x"
	}
	var target int64
	err := tx.QueryRow(ctx, query, integration, environment, pixel).Scan(&target)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrOperationConflict
	}
	return target, err
}
