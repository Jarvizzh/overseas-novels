package partner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"conversion-backend/internal/delivery"

	"github.com/jackc/pgx/v5"
)

// ReportAdapter prepares a frozen payload and its protocol-specific transport.
type ReportAdapter interface {
	PrepareReport(ReportJob) (body []byte, send func(context.Context) ReportResult, blockedReason string)
}

type Reporter struct {
	Queue    ReportQueue
	Adapter  ReportAdapter
	Logger   *slog.Logger
	Instance string
}

const reportHTTPTimeout = 5 * time.Second

func (w Reporter) Run(ctx context.Context) {
	var maintenance sync.WaitGroup
	var progress atomic.Bool
	maintenance.Go(func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for ctx.Err() == nil {
			var recovered int
			if w.Adapter != nil {
				dbCtx, cancel := context.WithTimeout(ctx, reportDBTimeout)
				count, err := w.Queue.Sweep(dbCtx, 50)
				cancel()
				recovered = count
				w.logError("partner report lease recovery failed", 0, err)
			}
			sent := progress.Swap(false)
			w.heartbeat(ctx, recovered > 0 || sent)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	})
	if w.Adapter != nil {
		w.sendReady(ctx, &progress)
	}
	maintenance.Wait()
}

func (w Reporter) sendReady(ctx context.Context, progress *atomic.Bool) {
	finished := make(chan bool, reportConcurrency)
	active := 0
	for ctx.Err() == nil {
		delay := 250 * time.Millisecond
		if active < reportConcurrency {
			dbCtx, cancel := context.WithTimeout(ctx, reportDBTimeout)
			job, err := w.Queue.Claim(dbCtx)
			cancel()
			if err == nil {
				active++
				go func() { finished <- w.Process(ctx, job) == nil }()
				continue
			}
			if !errors.Is(err, pgx.ErrNoRows) && ctx.Err() == nil {
				w.logError("partner report claim failed", 0, err)
				delay = time.Second
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

func (w Reporter) heartbeat(ctx context.Context, progressed bool) {
	dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), reportDBTimeout)
	defer cancel()
	store := delivery.Store{Pool: w.Queue.Pool, Environment: w.Queue.Environment}
	w.logError("partner report heartbeat failed", 0, store.Heartbeat(dbCtx, "partner_report", w.Instance, w.Adapter == nil, progressed))
}

func (w Reporter) Process(ctx context.Context, job ReportJob) error {
	result := w.attempt(ctx, job)
	finish := func() error {
		dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), reportDBTimeout)
		defer cancel()
		return w.Queue.Finish(dbCtx, job, result, false)
	}
	err := finish()
	if err != nil && result.confirmed() {
		dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), reportDBTimeout)
		receiptErr := w.Queue.RecordReceipt(dbCtx, job, result)
		cancel()
		if !errors.Is(receiptErr, ErrReportLeaseLost) {
			w.logError("partner acknowledgement persistence failed", job.ID, receiptErr)
		}
	}
	if err != nil && !errors.Is(err, ErrReportLeaseLost) {
		// Retry only persistence, never the signed HTTP request.
		err = finish()
	}
	w.logError("partner report completion failed", job.ID, err)
	return err
}

func (w Reporter) attempt(ctx context.Context, j ReportJob) ReportResult {
	var policy ReportPolicy
	if json.Unmarshal(j.Config, &policy) != nil || policy.Deadline(j.CreatedAt).IsZero() {
		return ReportResult{Outcome: ReportBlocked, Reason: "invalid_partner_report_config"}
	}
	window := reportHTTPTimeout + reportDBTimeout
	if !time.Now().Add(window).Before(policy.Deadline(j.CreatedAt)) {
		return ReportResult{Outcome: ReportBlocked, Reason: "partner_report_expired"}
	}
	if j.NeedsReplay && !policy.Replay {
		return ReportResult{Outcome: ReportBlocked, Reason: "partner_replay_not_authorized"}
	}
	body, send, reason := w.Adapter.PrepareReport(j)
	if reason != "" {
		return ReportResult{Outcome: ReportBlocked, Reason: reason}
	}
	if ctx.Err() != nil {
		return ReportResult{Outcome: ReportNotSent, Reason: "worker_stopping"}
	}
	// Bound the lifetime through both the durable send marker and the HTTP exchange.
	sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), window)
	defer cancel()
	dbCtx, stop := context.WithTimeout(sendCtx, reportDBTimeout)
	err := w.Queue.Start(dbCtx, j, body, policy.Replay, window, time.Duration(policy.MaxAge)*time.Second)
	stop()
	if err != nil {
		return ReportResult{Outcome: ReportNotSent, Reason: "partner_send_precondition_failed"}
	}
	return send(sendCtx)
}

func (w Reporter) logError(message string, id int64, err error) {
	if err != nil {
		w.Logger.Error(message, "report_id", id, "error_type", fmt.Sprintf("%T", err))
	}
}
