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
	Service            Service
	ConsumeInbox       func(context.Context, Service, int) (int, error)
	Media              Media
	Decrypt            func(string, []byte) (string, error)
	Logger             *slog.Logger
	Instance           string
	Paused             bool
	Concurrency        int
	GlobalLimit        int
	BatchSize          int
	PollInterval       time.Duration
	QueueProbeInterval time.Duration
	NormalizeBatch     int
	ReaderNormalizers  int
	InboxNormalizers   int
	PixelInterval      time.Duration
	RequestTimeout     time.Duration
}

const (
	dbTimeout       = 3 * time.Second
	queueProbeLimit = 250 * time.Millisecond
	firstIdleDelay  = 50 * time.Millisecond
	firstErrorDelay = 100 * time.Millisecond
	maxErrorDelay   = 5 * time.Second
)

func (w Worker) Run(ctx context.Context) {
	var workers sync.WaitGroup
	var readerProgress, inboxProgress, metaProgress atomic.Bool
	for range max(1, w.ReaderNormalizers) {
		workers.Go(func() { w.normalize(ctx, "normalize", w.Service.ConsumeReader, &readerProgress) })
	}
	if w.ConsumeInbox != nil {
		consume := func(ctx context.Context, limit int) (int, error) {
			return w.ConsumeInbox(ctx, w.Service, limit)
		}
		for range max(1, w.InboxNormalizers) {
			workers.Go(func() { w.normalize(ctx, "normalize_inbox", consume, &inboxProgress) })
		}
	}
	workers.Go(func() {
		w.poll(ctx, func(ctx context.Context) {
			w.heartbeat(ctx, "normalize", false, readerProgress.Swap(false))
			if w.ConsumeInbox != nil {
				w.heartbeat(ctx, "normalize_inbox", false, inboxProgress.Swap(false))
			}
		})
	})
	workers.Go(func() {
		w.poll(ctx, func(ctx context.Context) {
			swept := w.sweep(ctx)
			sent := metaProgress.Swap(false)
			w.heartbeat(ctx, "meta", w.Paused, swept || sent)
		})
	})
	if !w.Paused {
		w.sendReady(ctx, &metaProgress)
	}
	workers.Wait()
}

func (w Worker) poll(ctx context.Context, run func(context.Context)) {
	ticker := time.NewTicker(w.PollInterval)
	defer ticker.Stop()
	for ctx.Err() == nil {
		run(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w Worker) queueProbeInterval() time.Duration {
	if w.QueueProbeInterval > 0 {
		return min(w.PollInterval, w.QueueProbeInterval)
	}
	return min(w.PollInterval, queueProbeLimit)
}

func (w Worker) normalize(ctx context.Context, workerType string, consume func(context.Context, int) (int, error), progress *atomic.Bool) {
	batch := w.NormalizeBatch
	if batch <= 0 {
		batch = 20
	}
	idleMax := w.queueProbeInterval()
	idleDelay, errorDelay := min(firstIdleDelay, idleMax), firstErrorDelay
	timer := time.NewTimer(0)
	timer.Stop()
	defer timer.Stop()
	for ctx.Err() == nil {
		batchCtx, cancel := context.WithTimeout(ctx, dbTimeout)
		count, err := consume(batchCtx, batch)
		budgetSpent := errors.Is(err, context.DeadlineExceeded) && batchCtx.Err() != nil
		cancel()
		if ctx.Err() != nil {
			return
		}
		if count > 0 {
			progress.Store(true)
			idleDelay = min(firstIdleDelay, idleMax)
		}
		var delay time.Duration
		switch {
		case err != nil && !(budgetSpent && count > 0):
			w.Logger.Error("conversion normalization failed", "worker_type", workerType, "error_type", fmt.Sprintf("%T", err))
			delay = errorDelay
			errorDelay = min(2*errorDelay, maxErrorDelay)
		case count == batch || budgetSpent:
			// A full batch or a spent budget with committed progress can leave
			// more work. Start a fresh budget without waiting for a heartbeat.
			errorDelay = firstErrorDelay
			continue
		default:
			errorDelay = firstErrorDelay
			delay = idleDelay
			idleDelay = min(2*idleDelay, idleMax)
		}
		timer.Reset(delay)
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
	}
}

func (w Worker) sweep(ctx context.Context) bool {
	dbCtx, cancel := context.WithTimeout(ctx, dbTimeout)
	swept, err := w.Service.Sweep(dbCtx, 50, w.RequestTimeout+dbTimeout)
	cancel()
	if err != nil && ctx.Err() == nil {
		w.Logger.Error("conversion deadline and lease scan failed", "error_type", fmt.Sprintf("%T", err))
	}
	return swept > 0
}

func (w Worker) sendReady(ctx context.Context, progress *atomic.Bool) {
	window := w.RequestTimeout + dbTimeout
	finished := make(chan bool, w.Concurrency)
	active := 0
	for ctx.Err() == nil {
		delay := w.PollInterval
		if active < w.Concurrency {
			dbCtx, cancel := context.WithTimeout(ctx, dbTimeout)
			delay = w.nextClaimDelay(dbCtx, window)
			cancel()
			if delay == 0 {
				dbCtx, cancel = context.WithTimeout(ctx, dbTimeout)
				job, err := w.Service.ClaimBatch(dbCtx, w.GlobalLimit, w.BatchSize, window+2*dbTimeout, window)
				cancel()
				switch {
				case err == nil:
					active++
					go func() { finished <- w.processBatch(ctx, job, window) }()
					continue
				case errors.Is(err, pgx.ErrNoRows):
					delay = 25 * time.Millisecond
				default:
					delay = w.PollInterval
					if ctx.Err() == nil {
						w.Logger.Error("conversion claim failed", "error_type", fmt.Sprintf("%T", err))
					}
				}
			}
		}
		timer := time.NewTimer(delay)
		select {
		case progressed := <-finished:
			active--
			if progressed {
				progress.Store(true)
			}
		case <-timer.C:
		case <-ctx.Done():
		}
		timer.Stop()
	}
	for range active {
		<-finished
	}
}

func (w Worker) nextClaimDelay(ctx context.Context, window time.Duration) time.Duration {
	var seconds *float64
	err := w.Service.Pool.QueryRow(ctx, `
        WITH active AS MATERIALIZED (
            SELECT destination_id,lease_until FROM conversion.meta_batches
            WHERE environment=$1 AND finished_at IS NULL
        )
        SELECT extract(epoch FROM min(due.ready)-now())::float8
        FROM conversion.destinations x
        JOIN LATERAL (
            SELECT d.next_attempt_at,d.deadline_at FROM conversion.deliveries d
            JOIN conversion.events e ON e.id=d.event_id
            WHERE d.destination_id=x.id AND d.state='pending' AND d.lease_until IS NULL AND `+paymentCanSend+`
              AND d.deadline_at>GREATEST(now(),d.next_attempt_at,x.next_claim_at)+$2::interval
            ORDER BY d.next_attempt_at,d.id LIMIT 1
        ) d ON true
        CROSS JOIN LATERAL (SELECT GREATEST(d.next_attempt_at,x.next_claim_at) AS ready) due
        WHERE x.environment=$1 AND `+credentialCanSend+`
          AND NOT EXISTS (SELECT 1 FROM active a WHERE a.destination_id=x.id)
          AND (SELECT count(*) FROM active)<$3`, w.Service.Environment, window, w.GlobalLimit).Scan(&seconds)
	if err != nil {
		return w.PollInterval
	}
	// A new commit can make work ready on another Pixel before the next
	// previously known deadline.
	idle := w.queueProbeInterval()
	if seconds == nil || *seconds >= idle.Seconds() {
		return idle
	}
	if *seconds <= 0 {
		return 0
	}
	return max(25*time.Millisecond, time.Duration(*seconds*float64(time.Second)))
}

func (w Worker) heartbeat(ctx context.Context, workerType string, paused, progressed bool) {
	dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), dbTimeout)
	defer cancel()
	if err := w.Service.Heartbeat(dbCtx, workerType, w.Instance, paused, progressed); err != nil {
		w.Logger.Error("conversion heartbeat failed", "worker_type", workerType, "error_type", fmt.Sprintf("%T", err))
	}
}

func (w Worker) processBatch(ctx context.Context, batch BatchLease, window time.Duration) bool {
	dbCtx, stop := context.WithTimeout(ctx, dbTimeout)
	prepared, err := w.Service.StartBatch(dbCtx, batch, window, w.PixelInterval, w.Decrypt, w.prepareBatch)
	stop()
	if err != nil {
		if ctx.Err() == nil && !errors.Is(err, ErrLeaseLost) {
			w.Logger.Error("conversion batch preparation failed", "batch_id", batch.ID, "error_type", fmt.Sprintf("%T", err))
		}
		return false
	}
	if prepared == nil {
		return true
	}
	spacing := w.PixelInterval + time.Since(prepared.markIssuedAt)
	result := Result{Outcome: NotSent, Reason: "worker_stopping"}
	if ctx.Err() == nil {
		sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), w.RequestTimeout)
		result = prepared.send(sendCtx)
		cancel()
	}
	for range 2 {
		finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), dbTimeout)
		err = w.Service.PersistBatchReceipt(finishCtx, batch, result, spacing)
		cancel()
		if err == nil || errors.Is(err, ErrLeaseLost) {
			break
		}
	}
	if err == nil {
		finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), dbTimeout)
		err = w.Service.FinalizeBatch(finishCtx, batch, window)
		cancel()
	}
	if err != nil {
		w.Logger.Error("conversion batch result commit failed", "batch_id", batch.ID, "error_type", fmt.Sprintf("%T", err))
	}
	return err == nil
}

func (w Worker) prepareBatch(payloads []Payload, token string) (Send, error) {
	if media, ok := w.Media.(BatchMedia); ok {
		return media.PrepareBatch(payloads, token)
	}
	if len(payloads) == 1 {
		return w.Media.Prepare(payloads[0], token)
	}
	return nil, errors.New("media transport does not support batches")
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
