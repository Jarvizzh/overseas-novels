package wallet

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"sync"
	"time"

	"reader-backend/internal/model"
	"reader-backend/internal/payment"
)

func (s *service) RunReconciliation(ctx context.Context) {
	var workers sync.WaitGroup
	for _, worker := range []struct {
		run      func(context.Context)
		interval time.Duration
	}{
		{s.reconcilePaymentEvents, 15 * time.Second}, {s.reconcilePayPalEvents, 15 * time.Second}, {s.reconcilePayPalOrders, 15 * time.Second},
		{s.reconcilePayPalCancellations, 15 * time.Second}, {s.reconcilePayPalSubscriptions, 15 * time.Second}, {s.observePaymentTasks, time.Minute},
	} {
		workers.Go(func() {
			ticker := time.NewTicker(worker.interval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					cycle, cancel := context.WithTimeout(ctx, 8*time.Minute)
					worker.run(cycle)
					cancel()
				}
			}
		})
	}
	workers.Wait()
}

func (s *service) reconcilePayPalOrders(ctx context.Context) {
	jobs, err := claimPayPalOrders(ctx)
	if err != nil {
		log.Printf("task=order action=claim error=%v", err)
		return
	}

	for _, job := range jobs {
		requestCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
		_, err := s.completePayPalOrder(requestCtx, 0, job.id, true)
		cancel()
		if err != nil {
			log.Printf("task=order id=%s attempt=%d error=%v", job.id, job.attempt, err)
		}
		if saveErr := finishOrderReconciliation(ctx, job, err); saveErr != nil {
			log.Printf("task=order id=%s action=save error=%v", job.id, saveErr)
		}
	}
}

type paypalCancellation struct {
	id, reason string
	attempt    int
}

func (s *service) reconcilePayPalCancellations(ctx context.Context) {
	tasks, err := claimPayPalCancellations(ctx)
	if err != nil {
		log.Printf("task=cancel action=claim error=%v", err)
		return
	}
	for _, task := range tasks {
		req, cancel := context.WithTimeout(ctx, 40*time.Second)
		err := s.confirmPayPalCancellation(req, task.id, task.reason)
		cancel()
		if err == nil {
			continue
		}
		log.Printf("task=cancel id=%s attempt=%d error=%v", task.id, task.attempt, err)
		if saveErr := finishCancellationRetry(ctx, task, err); saveErr != nil {
			log.Printf("task=cancel id=%s action=save error=%v", task.id, saveErr)
		}
	}
}

func (s *service) confirmPayPalCancellation(ctx context.Context, id, reason string) error {
	details, err := s.paypalClient.GetSubscription(ctx, id)
	if err != nil {
		return err
	}
	if details.SubscriptionID != id {
		return errors.New("unexpected subscription ID")
	}
	if err := s.repo.savePayPalBillingSnapshot(ctx, details); err != nil {
		return err
	}

	switch details.Status {
	case "CANCELLED", "EXPIRED":
		return s.repo.UpdateUserSubscriptionStatus(ctx, id, details.Status)
	default:
		if err := s.paypalClient.CancelSubscription(ctx, id, reason); err != nil {
			return err
		}
		return s.repo.UpdateUserSubscriptionStatus(ctx, id, "CANCELLED")
	}
}

func (s *service) reconcilePayPalSubscriptions(ctx context.Context) {
	jobs, err := claimPayPalSubscriptions(ctx)
	if err != nil {
		log.Printf("task=subscription action=claim error=%v", err)
		return
	}

	for _, job := range jobs {
		req, cancel := context.WithTimeout(ctx, time.Minute)
		now := time.Now().UTC().Truncate(time.Second)
		end := job.cursor.AddDate(0, 0, 6).Truncate(time.Second)
		if end.After(now) {
			end = now
		}
		err := s.reconcileSubscriptionWindow(req, job.id, job.cursor.Add(-24*time.Hour), end)
		cancel()
		if err != nil {
			log.Printf("task=subscription id=%s from=%s to=%s error=%v", job.id, job.cursor.Format(time.RFC3339), end.Format(time.RFC3339), err)
		}
		if saveErr := finishSubscriptionReconciliation(ctx, job, end, err); saveErr != nil {
			log.Printf("task=subscription id=%s action=save error=%v", job.id, saveErr)
		}
	}
}

func (s *service) reconcileSubscriptionWindow(ctx context.Context, id string, from, to time.Time) error {
	sub, err := s.repo.GetSubscriptionByID(ctx, id)
	if err != nil {
		return err
	}
	if sub == nil || strings.TrimSpace(sub.PlanID) == "" {
		return ErrCheckoutUnavailable
	}
	details, err := s.paypalClient.GetSubscription(ctx, id)
	if err != nil {
		return err
	}
	if details.SubscriptionID != id || details.PlanID != sub.PlanID {
		return ErrCheckoutUnavailable
	}
	switch details.Status {
	case "ACTIVE", "CANCELLED", "SUSPENDED", "EXPIRED":
		if err := s.repo.UpdateUserSubscriptionStatus(ctx, id, details.Status); err != nil {
			return err
		}
	}
	if details.Status == "APPROVAL_PENDING" || details.Status == "APPROVED" {
		return nil
	}
	return s.importPayPalSubscriptionTransactions(ctx, sub, details, from, to)
}

func (s *service) importPayPalSubscriptionTransactions(ctx context.Context,
	sub *model.UserSubscription, details *payment.SubscriptionDetails, from, to time.Time) error {

	transactions, err := s.paypalClient.ListSubscriptionTransactions(ctx, sub.SubscriptionID, from, to)
	if err != nil {
		return err
	}
	for _, transaction := range transactions {
		if err := s.recordPayPalSubscriptionTransaction(ctx, sub, details, transaction); err != nil {
			return err
		}
	}
	return nil
}

func (s *service) observePaymentTasks(ctx context.Context) {
	request, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	status, err := PaymentTaskStatus(request)
	if err != nil {
		log.Printf("payment_tasks action=inspect error=%v", err)
		return
	}
	payload, err := json.Marshal(status)
	if err != nil {
		log.Printf("payment_tasks action=encode error=%v", err)
		return
	}
	log.Printf("payment_tasks=%s", payload)
}
