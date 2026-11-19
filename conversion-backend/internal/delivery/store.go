package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	Accepted           = "accepted"
	NotSent            = "not_sent"
	RetryableRejection = "retryable_rejection"
	PermanentRejection = "permanent_rejection"
	OutcomeUnknown     = "outcome_unknown"

	attemptBudget = 24
)

var ErrLeaseLost = errors.New("delivery lease no longer active")

type Result struct {
	Outcome        string
	Reason         string
	Block          bool
	HTTPStatus     *int
	GraphCode      *int
	GraphSubcode   *int
	EventsReceived *int
	TraceID        string
	RespondedAt    time.Time
	RetryAfter     time.Time
}

type Lease struct {
	ID      int64
	Token   string
	Attempt int
}

type Payload struct {
	PixelID      string
	Enabled      bool
	CredentialID *int64
	Ciphertext   []byte
	KeyVersion   *string
	EventID      string
	EventName    string
	OccurredAt   *time.Time
	ActionSource *string
	SourceURL    *string
	MatchingData json.RawMessage
	Amount       *string
	RouteIssue   string
	Currency     *string
}

type Store struct {
	Pool        *pgxpool.Pool
	Environment string
}

func (s Store) CheckSchema(ctx context.Context) error {
	const required = 2026111909
	var version int
	if err := s.Pool.QueryRow(ctx, "SELECT max(version) FROM reader_schema_migrations").Scan(&version); err != nil {
		return err
	}
	if version != required {
		return fmt.Errorf("schema version %d, required %d", version, required)
	}
	return nil
}

func claimTx(ctx context.Context, tx pgx.Tx, environment string, paymentFirst bool, limit int, lease, requestWindow, pixelInterval time.Duration) (Lease, error) {
	var job Lease
	err := tx.QueryRow(ctx, `
		WITH candidate AS (
			SELECT d.id, d.destination_id
			FROM conversion.deliveries d
			JOIN conversion.events e ON e.id = d.event_id
			JOIN conversion.destinations x ON x.id = d.destination_id
			WHERE d.environment = $1 AND d.state = 'pending' AND d.lease_until IS NULL
			  AND (e.event_name IN ('Purchase','Subscribe')) = $4
			  AND d.next_attempt_at <= now() AND d.deadline_at > now() + $2::interval
			  AND x.next_claim_at <= now()
			  AND NOT EXISTS (
			    SELECT 1 FROM conversion.deliveries active
			    WHERE active.destination_id = x.id AND active.lease_until > now()
			  )
			  AND (SELECT count(*) FROM conversion.deliveries
			       WHERE environment = $1 AND lease_until > now()) < $3
			ORDER BY d.next_attempt_at, d.id
			LIMIT 1 FOR UPDATE OF d, x SKIP LOCKED
		), reserved AS (
			UPDATE conversion.destinations x SET next_claim_at = now() + $5::interval
			FROM candidate c WHERE x.id = c.destination_id RETURNING x.id
		), claimed AS (
			UPDATE conversion.deliveries d SET lease_token = gen_random_uuid(),
				lease_until = now() + $6::interval, attempts = attempts + 1, last_attempt_at = now()
			FROM candidate c JOIN reserved r ON r.id = c.destination_id
			WHERE d.id = c.id RETURNING d.id, d.lease_token, d.attempts
		)
		INSERT INTO conversion.delivery_attempts (delivery_id, attempt_no, lease_token)
		SELECT id, attempts, lease_token FROM claimed
		RETURNING delivery_id, lease_token::text, attempt_no`,
		environment, requestWindow, limit, paymentFirst, pixelInterval, lease).Scan(&job.ID, &job.Token, &job.Attempt)
	if err != nil {
		return Lease{}, err
	}
	return job, nil
}

func (s Store) Payload(ctx context.Context, job Lease) (Payload, error) {
	var p Payload
	err := s.Pool.QueryRow(ctx, `
		SELECT x.pixel_id, x.enabled, x.credential_id, c.ciphertext, c.key_version, d.meta_event_id,
		       e.event_name, e.occurred_at, e.action_source, e.source_url, e.matching_data,
		       e.amount::text, e.currency, COALESCE(conversion.sinan_route_issue(e.id,x.id),'')
		FROM conversion.deliveries d
		JOIN conversion.events e ON e.id = d.event_id
		JOIN conversion.destinations x ON x.id = d.destination_id
		LEFT JOIN conversion.credentials c ON c.id = x.credential_id
		WHERE d.id = $1 AND d.lease_token = $2::uuid AND d.attempts = $3
		  AND d.environment = $4 AND d.lease_until > now()`,
		job.ID, job.Token, job.Attempt, s.Environment).Scan(&p.PixelID, &p.Enabled, &p.CredentialID,
		&p.Ciphertext, &p.KeyVersion, &p.EventID, &p.EventName, &p.OccurredAt, &p.ActionSource,
		&p.SourceURL, &p.MatchingData, &p.Amount, &p.Currency, &p.RouteIssue)
	return p, err
}

// StartSend persists the boundary after which a crash is ambiguous.
func (s Store) StartSend(ctx context.Context, job Lease, credentialID int64, ciphertext []byte, keyVersion string, requestWindow time.Duration) error {
	tag, err := s.Pool.Exec(ctx, `
		WITH locked AS (
			SELECT d.id FROM conversion.deliveries d
			JOIN conversion.destinations x ON x.id = d.destination_id
			JOIN conversion.credentials c ON c.id = x.credential_id
			WHERE d.id = $1 AND d.lease_token = $2::uuid AND d.attempts = $3
			  AND d.environment = $4 AND d.lease_until > now() + $8::interval
			  AND d.deadline_at > now() + $8::interval
			  AND x.enabled AND c.id = $5 AND c.ciphertext = $6 AND c.key_version = $7
			  AND COALESCE(conversion.sinan_route_issue(d.event_id,x.id),'')=''
			FOR UPDATE OF d FOR SHARE OF x
		)
		UPDATE conversion.delivery_attempts a SET sending_at = now()
		FROM locked WHERE a.delivery_id = locked.id AND a.attempt_no = $3
		  AND a.lease_token = $2::uuid AND a.sending_at IS NULL AND a.recorded_at IS NULL`,
		job.ID, job.Token, job.Attempt, s.Environment, credentialID, ciphertext, keyVersion, requestWindow)
	return requireOne(tag.RowsAffected(), err)
}

func recordAttempt(ctx context.Context, tx pgx.Tx, job Lease, r Result) error {
	var respondedAt *time.Time
	if !r.RespondedAt.IsZero() {
		respondedAt = &r.RespondedAt
	}
	tag, err := tx.Exec(ctx, `
		UPDATE conversion.delivery_attempts SET outcome = $4, reason = NULLIF($5,''), http_status = $6,
		  graph_code = $7, graph_subcode = $8, events_received = $9, fbtrace_id = NULLIF($10,''),
		  responded_at = $11, recorded_at = clock_timestamp()
		WHERE delivery_id = $1 AND lease_token = $2::uuid AND attempt_no = $3 AND recorded_at IS NULL`,
		job.ID, job.Token, job.Attempt, r.Outcome, r.Reason, r.HTTPStatus, r.GraphCode,
		r.GraphSubcode, r.EventsReceived, r.TraceID, respondedAt)
	return requireOne(tag.RowsAffected(), err)
}

func updateDelivery(ctx context.Context, tx pgx.Tx, id int64, t transition) error {
	final := t.state == "succeeded" || t.state == "failed" || t.state == "skipped"
	tag, err := tx.Exec(ctx, `
		UPDATE conversion.deliveries SET state = $2, reason = NULLIF($3,''), next_attempt_at = $4,
		  unknown_attempt_no = $5, lease_token = NULL, lease_until = NULL,
		  finalized_at = CASE WHEN $6 THEN clock_timestamp() END
		WHERE id = $1 AND finalized_at IS NULL`, id, t.state, t.reason, t.next, t.unknown, final)
	if err := requireOne(tag.RowsAffected(), err); err != nil {
		return err
	}
	if final {
		return createReport(ctx, tx, id)
	}
	return nil
}

func requireOne(rows int64, err error) error {
	if err != nil {
		return err
	}
	if rows != 1 {
		return ErrLeaseLost
	}
	return nil
}

func lockCurrent(ctx context.Context, tx pgx.Tx, job Lease, environment string, current *deliveryState) error {
	return tx.QueryRow(ctx, `
		SELECT clock_timestamp(), deadline_at, retry_budget_base_attempt, unknown_attempt_no, unknown_retry_used
		FROM conversion.deliveries
		WHERE id = $1 AND lease_token = $2::uuid AND attempts = $3 AND environment = $4
		  AND lease_until > clock_timestamp()
		FOR UPDATE`, job.ID, job.Token, job.Attempt, environment).
		Scan(&current.now, &current.deadline, &current.base, &current.unknown, &current.risk)
}
