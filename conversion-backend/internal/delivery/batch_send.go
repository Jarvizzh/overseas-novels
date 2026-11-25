package delivery

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

type BatchMedia interface {
	PrepareBatch([]Payload, string) (Send, error)
}

type batchMember struct {
	id      int64
	attempt int
	sent    bool
	state   deliveryState
	payload Payload
}

type preparedBatch struct {
	send         Send
	markIssuedAt time.Time
}

// StartBatch freezes exactly the prepared HTTP members and their positions in
// one transaction. The returned function is called only after commit.
func (s Service) StartBatch(ctx context.Context, batch BatchLease, window, interval time.Duration,
	decrypt func(string, []byte) (string, error), prepare func([]Payload, string) (Send, error),
) (*preparedBatch, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var destination int64
	err = tx.QueryRow(ctx, `
		SELECT destination_id FROM conversion.meta_batches
		WHERE id=$1 AND environment=$2 AND lease_token=$3::uuid AND started_at IS NULL
		  AND finished_at IS NULL AND lease_until>clock_timestamp()+$4::interval FOR UPDATE`,
		batch.ID, s.Environment, batch.Token, window).Scan(&destination)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrLeaseLost
	}
	if err != nil {
		return nil, err
	}
	// Lock integrations before the destination, as configuration and
	// normalization do. The payload query sees the authoritative route.
	if _, err := tx.Exec(ctx, `
		SELECT i.id FROM conversion.integrations i WHERE i.id IN (
		    SELECT e.integration_id FROM conversion.delivery_attempts a
		    JOIN conversion.deliveries d ON d.id=a.delivery_id JOIN conversion.events e ON e.id=d.event_id
		    WHERE a.batch_id=$1
		) ORDER BY i.id FOR SHARE`, batch.ID); err != nil {
		return nil, err
	}

	var pixel string
	var enabled, available, credentialHealthy bool
	var credential *int64
	var version *string
	var ciphertext []byte
	err = tx.QueryRow(ctx, `
		SELECT x.pixel_id,x.enabled,x.credential_id,c.key_version,c.ciphertext,x.next_claim_at<=clock_timestamp(),`+credentialCanSend+`
		FROM conversion.destinations x LEFT JOIN conversion.credentials c ON c.id=x.credential_id
		WHERE x.id=$1 FOR UPDATE OF x`, destination).Scan(&pixel, &enabled, &credential, &version, &ciphertext, &available, &credentialHealthy)
	if err != nil {
		return nil, err
	}
	var token string
	var sharedIssue Result
	switch {
	case !credentialHealthy:
		// This request has not sent anything. Keep its members queued for rotation.
		sharedIssue = Result{Outcome: NotSent, Reason: "meta_credential_paused"}
	case !available:
		sharedIssue = Result{Outcome: NotSent, Reason: "pixel_cooling_down"}
	case !enabled || credential == nil || version == nil:
		sharedIssue = Result{Outcome: NotSent, Reason: "destination_or_credential_unavailable", Block: true}
	default:
		token, err = decrypt(*version, ciphertext)
		if err != nil {
			sharedIssue = Result{Outcome: NotSent, Reason: "credential_decryption_failed", Block: true}
		}
	}
	members, err := batchPayloads(ctx, tx, batch, pixel)
	if err != nil {
		return nil, err
	}
	if len(members) == 0 {
		return nil, ErrLeaseLost
	}
	var ids []int64
	var payloads []Payload
	var local []batchUpdate
	seen := make(map[[2]string]struct{}, len(members))
	for _, member := range members {
		result := preflightBatchMember(member, sharedIssue, window, seen)
		if result.Outcome != "" {
			local = append(local, batchMemberUpdate(member, result, window, 0))
			continue
		}
		ids = append(ids, member.id)
		payloads = append(payloads, member.payload)
	}
	if err := applyBatchUpdates(ctx, tx, batch, local); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, closeBatch(ctx, tx, batch.ID)
	}
	send, err := prepare(payloads, token)
	if err != nil {
		return nil, err
	}
	var marked, throttled int
	issued := time.Now()
	err = tx.QueryRow(ctx, `
		WITH started AS (
			UPDATE conversion.meta_batches SET started_at=clock_timestamp(),
			    sent_count=cardinality($2::bigint[]),credential_id=$3,key_version=$4
			WHERE id=$1 AND lease_until>clock_timestamp()+$5::interval
			RETURNING started_at,destination_id
		), members AS (
			UPDATE conversion.delivery_attempts a SET sending_at=b.started_at,batch_position=m.position
			FROM unnest($2::bigint[]) WITH ORDINALITY m(id,position) CROSS JOIN started b
			WHERE a.batch_id=$1 AND a.delivery_id=m.id AND a.recorded_at IS NULL RETURNING a.delivery_id
		), reservation AS (
			UPDATE conversion.destinations x SET next_claim_at=GREATEST(x.next_claim_at,b.started_at+$6::interval)
			FROM started b WHERE x.id=b.destination_id RETURNING x.id
		)
		SELECT (SELECT count(*) FROM members),(SELECT count(*) FROM reservation)`,
		batch.ID, ids, credential, version, window, interval).Scan(&marked, &throttled)
	if err != nil {
		return nil, err
	}
	if marked != len(ids) || throttled != 1 {
		return nil, ErrLeaseLost
	}
	return &preparedBatch{send: send, markIssuedAt: issued}, tx.Commit(ctx)
}

func batchPayloads(ctx context.Context, tx pgx.Tx, batch BatchLease, pixel string) ([]batchMember, error) {
	rows, err := tx.Query(ctx, `
		SELECT d.id,a.attempt_no,clock_timestamp(),d.deadline_at,d.retry_budget_base_attempt,d.unknown_attempt_no,d.unknown_retry_used,
		       d.meta_event_id,e.event_name,e.occurred_at,e.action_source,e.source_url,e.matching_data,e.amount::text,e.currency,
		       CASE WHEN NOT (`+paymentCanSend+`) THEN 'payment_policy_not_sendable'
		            ELSE COALESCE(conversion.sinan_route_issue(e.id,x.id),NULLIF(`+readerRouteIssue+`,''),'') END
		FROM conversion.delivery_attempts a JOIN conversion.deliveries d
		  ON d.id=a.delivery_id AND d.attempts=a.attempt_no AND d.lease_token=a.lease_token
		JOIN conversion.events e ON e.id=d.event_id JOIN conversion.destinations x ON x.id=d.destination_id
		WHERE a.batch_id=$1 AND a.lease_token=$2::uuid AND a.recorded_at IS NULL
		ORDER BY d.id FOR UPDATE OF d`, batch.ID, batch.Token)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (batchMember, error) {
		m := batchMember{payload: Payload{PixelID: pixel}}
		p, st := &m.payload, &m.state
		err := row.Scan(&m.id, &m.attempt, &st.now, &st.deadline, &st.base, &st.unknown, &st.risk,
			&p.EventID, &p.EventName, &p.OccurredAt, &p.ActionSource, &p.SourceURL, &p.MatchingData, &p.Amount, &p.Currency, &p.RouteIssue)
		return m, err
	})
}

func preflightBatchMember(m batchMember, shared Result, window time.Duration, seen map[[2]string]struct{}) Result {
	p := m.payload
	switch {
	case !m.state.now.Add(window).Before(m.state.deadline):
		return Result{Outcome: PermanentRejection, Reason: "deadline_exhausted"}
	case p.RouteIssue != "":
		return Result{Outcome: NotSent, Reason: p.RouteIssue, Block: true}
	case shared.Outcome != "":
		return shared
	}
	if err := ValidatePayload(p); err != nil {
		return Result{Outcome: PermanentRejection, Reason: err.Error()}
	}
	if p.EventName == "" || p.EventID == "" {
		return Result{Outcome: PermanentRejection, Reason: "invalid_event_identity"}
	}
	if p.OccurredAt.After(m.state.now) || !m.state.now.Add(window).Before(p.OccurredAt.Add(metaWindow)) {
		return Result{Outcome: PermanentRejection, Reason: "event_time_outside_meta_window"}
	}
	identity := [2]string{p.EventName, p.EventID}
	if _, found := seen[identity]; found {
		return Result{Outcome: PermanentRejection, Reason: "duplicate_meta_event_identity"}
	}
	seen[identity] = struct{}{}
	return Result{}
}

func closeBatch(ctx context.Context, tx pgx.Tx, id int64) error {
	tag, err := tx.Exec(ctx, `UPDATE conversion.meta_batches SET finished_at=clock_timestamp()
		WHERE id=$1 AND finished_at IS NULL`, id)
	if err := requireOne(tag.RowsAffected(), err); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
