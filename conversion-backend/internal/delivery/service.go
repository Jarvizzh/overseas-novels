package delivery

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"

	"github.com/jackc/pgx/v5"
)

type Service struct {
	Store
}

type deliveryState struct {
	now, deadline time.Time
	base          int
	unknown       *int
	risk          bool
}

type transition struct {
	state   string
	reason  string
	next    time.Time
	unknown *int
}

func decide(current deliveryState, attempt int, r Result, requestWindow time.Duration) transition {
	t := transition{reason: r.Reason, next: current.now, unknown: current.unknown}
	switch {
	case r.Outcome == Accepted:
		t.state = "succeeded"
		t.reason = ""
	case r.Outcome == OutcomeUnknown || current.unknown != nil || current.risk:
		t.state = "outcome_unknown"
		t.reason = "meta_no_confirmation"
		if t.unknown == nil {
			t.unknown = &attempt
		}
	case r.Block:
		t.state = "blocked"
	case r.Outcome == PermanentRejection:
		t.state = "failed"
	default:
		n := min(max(attempt-current.base-1, 0), 7)
		backoff := min(30*time.Second<<n, time.Hour)
		t.next = maxTime(current.now.Add(backoff/2+rand.N(backoff/2)), r.RetryAfter)
		t.state = "pending"
		if attempt-current.base >= attemptBudget {
			t.state = "blocked"
			t.reason = "retry_budget_exhausted"
		}
	}
	if t.state == "pending" && !t.next.Add(requestWindow).Before(current.deadline) ||
		(t.state == "blocked" || t.state == "outcome_unknown") && !current.now.Add(requestWindow).Before(current.deadline) {
		t.state = "failed"
		if t.unknown == nil {
			t.reason = "deadline_exhausted"
		}
	}
	return t
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func (s Service) Complete(ctx context.Context, job Lease, r Result, requestWindow time.Duration) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var current deliveryState
	err = lockCurrent(ctx, tx, job, s.Environment, &current)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrLeaseLost
	}
	if err != nil {
		return err
	}
	t := decide(current, job.Attempt, r, requestWindow)
	if err := recordAttempt(ctx, tx, job, r); err != nil {
		return err
	}
	if err := updateDelivery(ctx, tx, job.ID, t); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Claim reserves a sending slot across workers in the same environment.
func (s Service) Claim(ctx context.Context, paymentFirst bool, limit int, lease, requestWindow, pixelInterval time.Duration) (Lease, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return Lease{}, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(1930716, hashtext($1))", s.Environment); err != nil {
		return Lease{}, err
	}

	job, err := claimTx(ctx, tx, s.Environment, paymentFirst, limit, lease, requestWindow, pixelInterval)
	if errors.Is(err, pgx.ErrNoRows) {
		job, err = claimTx(ctx, tx, s.Environment, !paymentFirst, limit, lease, requestWindow, pixelInterval)
	}
	if err != nil {
		return Lease{}, err
	}
	return job, tx.Commit(ctx)
}

// Sweep handles expired leases and deadlines without requiring a new send.
func (s Service) Sweep(ctx context.Context, limit int, requestWindow time.Duration) (int, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	batch, err := expiredTx(ctx, tx, s.Environment, limit)
	if err != nil {
		return 0, err
	}

	for _, e := range batch {
		t := transition{state: "failed", reason: "deadline_exhausted", next: e.current.now, unknown: e.current.unknown}
		if e.job.Token != "" {
			r := Result{Outcome: NotSent, Reason: "lease_expired_before_send"}
			if e.sending {
				r = Result{Outcome: OutcomeUnknown, Reason: "lease_expired_after_send_started"}
			}
			confirmation, err := confirmedEvidenceTx(ctx, tx, e)
			if err != nil {
				return 0, err
			}
			if confirmation != nil {
				r = *confirmation
			}
			if err := recordAttempt(ctx, tx, e.job, r); err != nil {
				return 0, err
			}
			t = decide(e.current, e.job.Attempt, r, requestWindow)
		} else if t.unknown != nil {
			t.reason = "meta_no_confirmation"
		} else if e.reason != nil {
			t.reason = *e.reason
		}
		if err := updateDelivery(ctx, tx, e.job.ID, t); err != nil {
			return 0, err
		}
	}
	return len(batch), tx.Commit(ctx)
}
