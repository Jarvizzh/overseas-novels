package delivery

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

const MaxBatchSize = 100

type BatchLease struct {
	ID    int64
	Token string
}

type batchCandidate struct {
	id      int64
	payment bool
	aged    bool
	limit   int
}

func (s Service) ClaimBatch(ctx context.Context, slots, size int, lease, window time.Duration) (BatchLease, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return BatchLease{}, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(1930716,hashtext($1))", s.Environment); err != nil {
		return BatchLease{}, err
	}
	var destination int64
	var streak classStreak
	err = tx.QueryRow(ctx, `
		SELECT x.id,x.consecutive_payment_claims,x.nonpayment_claim_streak
		FROM conversion.destinations x
		JOIN LATERAL (
			SELECT d.next_attempt_at,d.id FROM conversion.deliveries d
			JOIN conversion.events e ON e.id=d.event_id
			WHERE d.destination_id=x.id AND d.state='pending' AND d.lease_until IS NULL
			  AND d.next_attempt_at<=now() AND d.deadline_at>now()+$2::interval AND `+paymentCanSend+`
			ORDER BY d.next_attempt_at,d.id LIMIT 1
		) oldest ON true
		WHERE x.environment=$1 AND x.next_claim_at<=now() AND `+credentialCanSend+`
		  AND NOT EXISTS (SELECT 1 FROM conversion.meta_batches b WHERE b.destination_id=x.id AND b.finished_at IS NULL)
		  AND NOT EXISTS (SELECT 1 FROM conversion.deliveries d
		      WHERE d.environment=$1 AND d.destination_id=x.id AND d.lease_until IS NOT NULL)
		  AND (
		    (SELECT count(*) FROM conversion.meta_batches WHERE environment=$1 AND finished_at IS NULL)
		    + (SELECT count(*) FROM conversion.deliveries d JOIN conversion.delivery_attempts a
		         ON a.delivery_id=d.id AND a.attempt_no=d.attempts
		       WHERE d.environment=$1 AND d.lease_until>now() AND a.batch_id IS NULL)
		  ) < $3
		ORDER BY x.next_claim_at,oldest.next_attempt_at,oldest.id
		LIMIT 1 FOR UPDATE OF x SKIP LOCKED`, s.Environment, window, slots).Scan(&destination, &streak.payment, &streak.other)
	if err != nil {
		return BatchLease{}, err
	}
	size = min(max(size, 1), MaxBatchSize)
	ids, streak, err := selectBatchMembers(ctx, tx, destination, size, streak, window)
	if err != nil {
		return BatchLease{}, err
	}
	var batch BatchLease
	var reserved, attempted int
	err = tx.QueryRow(ctx, `
		WITH batch AS (
			INSERT INTO conversion.meta_batches(environment,destination_id,lease_until)
			VALUES($1,$2,clock_timestamp()+$3::interval)
			RETURNING id,lease_token,lease_until,created_at
		), reserved AS (
			UPDATE conversion.destinations SET consecutive_payment_claims=$4,nonpayment_claim_streak=$6 WHERE id=$2 RETURNING id
		), claimed AS (
			UPDATE conversion.deliveries d
			SET lease_token=b.lease_token,lease_until=b.lease_until,
			    attempts=d.attempts+1,last_attempt_at=b.created_at
			FROM batch b WHERE d.id=ANY($5::bigint[])
			RETURNING d.id,d.attempts,d.lease_token
		), attempts AS (
		    INSERT INTO conversion.delivery_attempts(delivery_id,attempt_no,lease_token,batch_id)
		    SELECT c.id,c.attempts,c.lease_token,b.id FROM claimed c CROSS JOIN batch b
		    RETURNING delivery_id
		)
		SELECT id,lease_token::text,(SELECT count(*) FROM reserved),(SELECT count(*) FROM attempts) FROM batch`,
		s.Environment, destination, lease, streak.payment, ids, streak.other).Scan(&batch.ID, &batch.Token, &reserved, &attempted)
	if err != nil {
		return BatchLease{}, err
	}
	if reserved != 1 || attempted != len(ids) {
		return BatchLease{}, ErrLeaseLost
	}
	return batch, tx.Commit(ctx)
}

type classStreak struct {
	payment, other int
}

func selectBatchMembers(ctx context.Context, tx pgx.Tx, destination int64, size int, streak classStreak, window time.Duration) ([]int64, classStreak, error) {
	rows, err := tx.Query(ctx, `
		SELECT d.id,k.payment,d.next_attempt_at<=now()-interval '1 minute',d.max_batch_size
		FROM (VALUES (true),(false)) k(payment)
		CROSS JOIN LATERAL (
			SELECT d.id,d.next_attempt_at,d.max_batch_size
			FROM conversion.deliveries d JOIN conversion.events e ON e.id=d.event_id
			WHERE d.destination_id=$1 AND d.state='pending' AND d.lease_until IS NULL
			  AND d.next_attempt_at<=now() AND d.deadline_at>now()+$3::interval
			  AND (e.event_name IN ('Purchase','Subscribe'))=k.payment AND `+paymentCanSend+`
			ORDER BY d.next_attempt_at,d.id LIMIT $2 FOR UPDATE OF d SKIP LOCKED
		) d ORDER BY d.next_attempt_at,d.id`, destination, size, window)
	if err != nil {
		return nil, classStreak{}, err
	}
	candidates, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (batchCandidate, error) {
		var next batchCandidate
		err := row.Scan(&next.id, &next.payment, &next.aged, &next.limit)
		return next, err
	})
	if err != nil {
		return nil, classStreak{}, err
	}
	if len(candidates) == 0 {
		return nil, classStreak{}, pgx.ErrNoRows
	}
	var pay, other []batchCandidate
	for _, c := range candidates {
		if c.payment {
			pay = append(pay, c)
		} else {
			other = append(other, c)
		}
	}
	ids := make([]int64, 0, size)
	for len(ids) < size && (len(pay) > 0 || len(other) > 0) {
		// An aged nonpayment can take an early slot, then must yield to a
		// ready payment. The per-class counters survive batch boundaries.
		useOther := len(other) > 0 && (len(pay) == 0 ||
			(streak.other < maxConsecutivePaymentClaims &&
				(streak.payment >= maxConsecutivePaymentClaims || other[0].aged && streak.other == 0)))
		var c batchCandidate
		if useOther {
			c = other[0]
		} else {
			c = pay[0]
		}
		if len(ids) >= c.limit {
			break
		}
		size = min(size, c.limit)
		ids = append(ids, c.id)
		if useOther {
			other = other[1:]
			streak = classStreak{other: min(streak.other+1, maxConsecutivePaymentClaims)}
		} else {
			pay = pay[1:]
			streak = classStreak{payment: min(streak.payment+1, maxConsecutivePaymentClaims)}
		}
	}
	return ids, streak, nil
}
