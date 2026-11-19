package delivery

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
)

type Send func(context.Context) Result

type Media interface {
	Prepare(Payload, string) (Send, error)
}

type Worker struct {
	Service        Service
	ConsumeInbox   func(context.Context, Service, int) (int, error)
	Media          Media
	Decrypt        func(string, []byte) (string, error)
	Logger         *slog.Logger
	Instance       string
	Paused         bool
	Concurrency    int
	GlobalLimit    int
	PollInterval   time.Duration
	PixelInterval  time.Duration
	RequestTimeout time.Duration
}

const dbTimeout = 3 * time.Second

func (w Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(w.PollInterval)
	defer ticker.Stop()
	for ctx.Err() == nil {
		w.cycle(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w Worker) cycle(ctx context.Context) {
	normalizeCtx, stop := context.WithTimeout(ctx, dbTimeout)
	normalized, normalizeErr := w.Service.ConsumeReader(normalizeCtx, 20)
	stop()
	if normalizeErr != nil && ctx.Err() == nil {
		w.Logger.Error("reader conversion normalization failed", "error_type", fmt.Sprintf("%T", normalizeErr))
	}

	if w.ConsumeInbox != nil {
		inboxCtx, cancel := context.WithTimeout(ctx, dbTimeout)
		count, err := w.ConsumeInbox(inboxCtx, w.Service, 20)
		cancel()
		w.heartbeatInbox(ctx, count, err)
	}

	window := w.RequestTimeout + dbTimeout
	dbCtx, cancel := context.WithTimeout(ctx, dbTimeout)
	swept, err := w.Service.Sweep(dbCtx, 50, window)
	cancel()
	if err != nil && ctx.Err() == nil {
		w.Logger.Error("conversion deadline and lease scan failed", "error_type", fmt.Sprintf("%T", err))
	}
	var progressed atomic.Bool
	var workers sync.WaitGroup
	if !w.Paused {
		for slot := range w.Concurrency {
			if ctx.Err() != nil {
				break
			}
			dbCtx, cancel := context.WithTimeout(ctx, dbTimeout)
			job, err := w.Service.Claim(dbCtx, slot != w.Concurrency-1, w.GlobalLimit,
				window+2*dbTimeout, window, w.PixelInterval)
			cancel()
			if errors.Is(err, pgx.ErrNoRows) {
				break
			}
			if err != nil {
				w.Logger.Error("conversion claim failed", "error_type", fmt.Sprintf("%T", err))
				break
			}
			workers.Go(func() {
				if w.process(ctx, job, window) {
					progressed.Store(true)
				}
			})
		}
	}
	workers.Wait()
	dbCtx, cancel = context.WithTimeout(context.WithoutCancel(ctx), dbTimeout)
	defer cancel()
	if err := w.Service.Heartbeat(dbCtx, "normalize", w.Instance, false, normalized > 0); err != nil {
		w.Logger.Error("conversion normalization heartbeat failed", "error_type", fmt.Sprintf("%T", err))
	}
	if err := w.Service.Heartbeat(dbCtx, "meta", w.Instance, w.Paused, swept > 0 || progressed.Load()); err != nil {
		w.Logger.Error("conversion heartbeat failed", "error_type", fmt.Sprintf("%T", err))
	}
}

func (w Worker) process(ctx context.Context, job Lease, window time.Duration) bool {
	result := w.attempt(ctx, job, window)
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), dbTimeout)
	err := w.Service.Complete(finishCtx, job, result, window)
	cancel()
	if result.Outcome != Accepted || err != nil {
		// Save the known receipt before another local commit can be delayed or
		// interrupted. Diagnostic determines whether the term is still current.
		diagnosticCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), dbTimeout)
		diagErr := w.Service.Diagnostic(diagnosticCtx, job, result)
		stop()
		if diagErr != nil {
			w.Logger.Error("conversion diagnostic write failed", "delivery_id", job.ID, "attempt", job.Attempt, "error_type", fmt.Sprintf("%T", diagErr))
		}
	}
	if err != nil && !errors.Is(err, ErrLeaseLost) {
		// Retry only the local commit under the same lease.
		retryCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), dbTimeout)
		err = w.Service.Complete(retryCtx, job, result, window)
		stop()
	}
	if err != nil {
		w.Logger.Error("conversion result commit failed", "delivery_id", job.ID, "attempt", job.Attempt, "error_type", fmt.Sprintf("%T", err))
	}
	return err == nil
}

func (w Worker) attempt(ctx context.Context, job Lease, window time.Duration) Result {
	if ctx.Err() != nil {
		return Result{Outcome: NotSent, Reason: "worker_stopping"}
	}
	dbCtx, cancel := context.WithTimeout(ctx, dbTimeout)
	defer cancel()
	payload, err := w.Service.Payload(dbCtx, job)
	if err != nil {
		return Result{Outcome: NotSent, Reason: "payload_unavailable"}
	}
	if payload.RouteIssue != "" {
		return Result{Outcome: NotSent, Reason: payload.RouteIssue, Block: true}
	}
	if !payload.Enabled || payload.CredentialID == nil || payload.KeyVersion == nil {
		return Result{Outcome: NotSent, Reason: "destination_or_credential_unavailable", Block: true}
	}
	token, err := w.Decrypt(*payload.KeyVersion, payload.Ciphertext)
	if err != nil {
		return Result{Outcome: NotSent, Reason: "credential_decryption_failed", Block: true}
	}
	send, err := w.Media.Prepare(payload, token)
	if err != nil {
		return Result{Outcome: PermanentRejection, Reason: err.Error()}
	}
	if err := w.Service.StartSend(dbCtx, job, *payload.CredentialID, payload.Ciphertext, *payload.KeyVersion, window); err != nil {
		return Result{Outcome: NotSent, Reason: "send_precondition_failed"}
	}
	if ctx.Err() != nil {
		return Result{Outcome: NotSent, Reason: "worker_stopping"}
	}
	sendCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), w.RequestTimeout)
	defer stop()
	return send(sendCtx)
}

func (w Worker) heartbeatInbox(ctx context.Context, count int, err error) {
	if err != nil && ctx.Err() == nil {
		w.Logger.Error("conversion inbox normalization failed", "error_type", fmt.Sprintf("%T", err))
	}
	dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), dbTimeout)
	defer cancel()
	if err := w.Service.Heartbeat(dbCtx, "normalize_inbox", w.Instance, false, count > 0); err != nil {
		w.Logger.Error("conversion inbox heartbeat failed", "error_type", fmt.Sprintf("%T", err))
	}
}
