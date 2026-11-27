package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	stripego "github.com/stripe/stripe-go/v87"
	"reader-backend/internal/db"
	"reader-backend/internal/payment"
	"reader-backend/internal/payment/settlement"
	"reader-backend/internal/payment/stripe"
)

const stripeInboxEnvelopeVersion = 2

type stripeWebhookVerifier interface {
	AccountScope() payment.WebhookAccount
	VerifyWebhook([]byte, string) (stripe.VerifiedEvent, error)
}

type stripeWebhookResolver interface {
	AccountScope() payment.WebhookAccount
	ResolveWebhookNotice(context.Context, stripe.Event) (*payment.SingleCheckoutNotice, error)
}

// stripeInboxEnvelope is the version-agnostic durable envelope persisted in
// payment_event_inbox for both real-time Stripe webhooks and Events API recovery.
type stripeInboxEnvelope struct {
	EnvelopeVersion int             `json:"envelope_version"`
	EventID         string          `json:"event_id"`
	EventType       string          `json:"event_type"`
	ObjectID        string          `json:"object_id"`
	ObjectType      string          `json:"object_type,omitempty"`
	APIVersion      string          `json:"api_version"`
	AccountKey      string          `json:"account_key"`
	Environment     string          `json:"environment"`
	Source          string          `json:"source"`
	Created         int64           `json:"created,omitempty"`
	RequestID       string          `json:"request_id,omitempty"`
	IdempotencyKey  string          `json:"idempotency_key,omitempty"`
	Snapshot        json.RawMessage `json:"snapshot,omitempty"`
	Raw             json.RawMessage `json:"source_payload,omitempty"`
}

func stripeAccountScope(scope stripe.Scope) payment.WebhookAccount {
	return payment.WebhookAccount{
		AccountKey:  scope.AccountID,
		Environment: string(scope.Environment),
	}
}

func newStripeInboxEnvelope(event stripe.Event, raw []byte) (stripeInboxEnvelope, error) {
	if event.ID == "" || event.Type == "" || event.Scope.AccountID == "" || event.Scope.Environment == "" {
		return stripeInboxEnvelope{}, errors.New("Stripe event envelope missing required identity fields")
	}
	env := stripeInboxEnvelope{
		EnvelopeVersion: stripeInboxEnvelopeVersion,
		EventID:         event.ID,
		EventType:       string(event.Type),
		ObjectID:        event.ObjectID,
		ObjectType:      event.ObjectType,
		APIVersion:      event.APIVersion,
		AccountKey:      event.Scope.AccountID,
		Environment:     string(event.Scope.Environment),
		Source:          string(event.Source),
		Created:         event.Created,
		RequestID:       event.RequestID,
		IdempotencyKey:  event.IdempotencyKey,
	}
	if len(event.Object) > 0 && json.Valid(event.Object) {
		env.Snapshot = event.Object
	}
	if len(raw) > 0 && json.Valid(raw) {
		env.Raw = raw
	}
	return env, nil
}

func (e stripeInboxEnvelope) toStripeEvent() stripe.Event {
	return stripe.Event{
		Scope: stripe.Scope{
			AccountID:   e.AccountKey,
			Environment: stripe.Environment(e.Environment),
		},
		Source:         stripe.EventSource(e.Source),
		ID:             e.EventID,
		Type:           stripego.EventType(e.EventType),
		APIVersion:     e.APIVersion,
		Created:        e.Created,
		RequestID:      e.RequestID,
		IdempotencyKey: e.IdempotencyKey,
		ObjectID:       e.ObjectID,
		ObjectType:     e.ObjectType,
		Object:         e.Snapshot,
	}
}

// enqueueStripeEvent persists a verified Stripe event as a universal envelope in
// payment_event_inbox, deduplicated per (provider, account_id, event_id).
// Returns an error if the account is unknown or the durable queue write fails.
func enqueueStripeEvent(ctx context.Context, event stripe.Event, raw []byte) error {
	env, err := newStripeInboxEnvelope(event, raw)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(env)
	if err != nil {
		return err
	}
	pool := db.Pool(ctx)
	var accountID int64
	err = pool.QueryRow(ctx, `SELECT id FROM payment_channel_accounts
		WHERE provider='stripe' AND account_key=$1 AND environment=$2`,
		env.AccountKey, env.Environment).Scan(&accountID)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: account %s (%s) not found", stripe.ErrEventScopeMismatch, env.AccountKey, env.Environment)
	}
	if err != nil {
		return err
	}
	_, err = pool.Exec(ctx, `INSERT INTO payment_event_inbox(provider,account_id,event_id,event_type,amount_cents,currency,event)
		VALUES('stripe',$1,$2,$3,0,'USD',$4::jsonb)
		ON CONFLICT ON CONSTRAINT payment_event_identity DO NOTHING`,
		accountID, env.EventID, env.EventType, payload)
	return err
}

// dispatchStripeInboxJob is the single shared dispatch path for both real-time
// Stripe webhooks and Events API recovery items.
func (s *service) dispatchStripeInboxJob(ctx context.Context, provider payment.SingleCheckoutAdapter, account payment.WebhookAccount, job stripeNoticeJob) error {
	if job.envelope == nil {
		return s.executeStripeNoticeJob(ctx, provider, account, job)
	}
	env := job.envelope
	if env.AccountKey != account.AccountKey || env.Environment != account.Environment {
		return fmt.Errorf("%w: inbox envelope=%s/%s worker=%s/%s",
			stripe.ErrEventScopeMismatch, env.AccountKey, env.Environment, account.AccountKey, account.Environment)
	}
	event := env.toStripeEvent()
	if !stripe.IsAftercareEventType(event.Type) && !paymentEnvironmentMatches(account) {
		return ErrCheckoutPaused
	}
	resolver, err := s.stripeEventResolver(account)
	if err != nil {
		return err
	}
	notice, err := resolver.ResolveWebhookNotice(ctx, event)
	if err != nil {
		return err
	}
	if notice == nil {
		return nil
	}
	if notice.Account != account {
		return fmt.Errorf("%w: resolved notice=%s/%s worker=%s/%s",
			stripe.ErrEventScopeMismatch, notice.Account.AccountKey, notice.Account.Environment, account.AccountKey, account.Environment)
	}
	resolved := stripeNoticeJob{
		id:                  job.id,
		lease:               job.lease,
		attemptID:           notice.AttemptID,
		sessionID:           notice.SessionID,
		refundID:            notice.RefundID,
		vipIntentID:         notice.VIPIntentID,
		invoiceID:           notice.InvoiceID,
		subscriptionID:      notice.SubscriptionID,
		aftercare:           notice.Aftercare,
		eventID:             notice.EventID,
		settlementReference: notice.SettlementReference,
	}
	return s.executeStripeNoticeJob(ctx, provider, account, resolved)
}

func (s *service) stripeEventResolver(account payment.WebhookAccount) (stripeWebhookResolver, error) {
	if resolver, ok := payment.AccountCapability[stripeWebhookResolver](s.adapters, payment.ProviderStripe, account); ok && resolver.AccountScope() == account {
		return resolver, nil
	}
	resolver, err := payment.Capability[stripeWebhookResolver](s.adapters, payment.ProviderStripe)
	if err != nil {
		return nil, err
	}
	if resolver.AccountScope() != account {
		return nil, fmt.Errorf("%w: resolver=%s/%s worker=%s/%s",
			stripe.ErrEventScopeMismatch, resolver.AccountScope().AccountKey, resolver.AccountScope().Environment, account.AccountKey, account.Environment)
	}
	return resolver, nil
}

func (s *service) executeStripeNoticeJob(ctx context.Context, provider payment.SingleCheckoutAdapter, account payment.WebhookAccount, job stripeNoticeJob) error {
	switch {
	case len(job.aftercare) != 0:
		return s.processStripeAftercareNotice(ctx, account, job.aftercare)
	case !paymentEnvironmentMatches(account):
		return ErrCheckoutPaused
	case job.refundID != "":
		return s.syncStripeRefundNotice(ctx, account, job.refundID)
	case job.settlementReference != "":
		return settlement.NewRepository(db.Pool(ctx)).ScheduleStripeNotice(ctx, account, job.eventID, job.settlementReference)
	case job.vipIntentID != "" || job.invoiceID != "" || job.subscriptionID != "":
		if job.vipIntentID != "" {
			if _, err := uuid.Parse(job.vipIntentID); err != nil {
				return nil
			}
		}
		return s.handleStripeVIPNotice(ctx, job)
	case job.attemptID != "":
		if _, err := uuid.Parse(job.attemptID); err != nil {
			return nil
		}
		attempt, err := loadStripeAttempt(ctx, db.Pool(ctx), account, job.attemptID)
		if err != nil {
			return err
		}
		return s.syncStripeAttempt(ctx, provider, attempt, job.sessionID)
	default:
		return nil
	}
}
