package application

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"sync"
	"time"

	"reader-backend/internal/config"
	"reader-backend/internal/conversion"
	"reader-backend/internal/db"
	"reader-backend/internal/model"
	"reader-backend/internal/payment"
)

const vipPlanPublishInterval = 15 * time.Second

func (s *service) RunReconciliation(ctx context.Context) {
	ctx = db.BackgroundContext(ctx)
	var workers sync.WaitGroup
	type scheduledWorker struct {
		run      func(context.Context)
		interval time.Duration
		wake     <-chan struct{}
	}
	scheduled := []scheduledWorker{
		{func(ctx context.Context) { s.eachPayPalAccount(ctx, (*service).reconcilePayPalEvents) }, 15 * time.Second, s.paypalEventsWake},
		{s.reconcilePayPalOrders, 15 * time.Second, nil},
		{s.reconcilePayPalCancellations, 15 * time.Second, nil},
		{s.reconcilePayPalSubscriptions, 15 * time.Second, nil},
		{func(ctx context.Context) { s.eachPayPalAccount(ctx, (*service).reconcileRefundCommands) }, 15 * time.Second, nil},
		{s.reconcileAftercare, aftercareInterval, nil},
		{func(ctx context.Context) { s.eachStripeAccount(ctx, (*service).reconcileStripeRadarApprovals) }, aftercareInterval, nil},
		{func(ctx context.Context) { s.eachStripeAccount(ctx, (*service).reconcileStripeCheckouts) }, stripeCheckoutInterval, s.stripeEventsWake},
		{func(ctx context.Context) { s.eachPayPalAccount(ctx, (*service).recoverPayPalEventWindow) }, paymentEventRecoveryInterval, nil},
		{func(ctx context.Context) { s.eachStripeAccount(ctx, (*service).recoverStripeEventWindow) }, paymentEventRecoveryInterval, nil},
		{func(ctx context.Context) { s.eachStripeAccount(ctx, (*service).reconcileStripeVIP) }, stripeCheckoutInterval, nil},
		{func(ctx context.Context) { s.eachStripeAccount(ctx, (*service).reconcileStripeRefunds) }, stripeCheckoutInterval, nil},
		{s.reconcileSettlementCommands, stripeCheckoutInterval, nil},
		{s.reconcileAutomaticLedger, paymentEventRecoveryInterval, nil},
		{s.reconcileStripeReportRuns, paymentEventRecoveryInterval, nil},
	}
	if config.AppConfig.PaymentTaskMetrics {
		scheduled = append(scheduled, scheduledWorker{s.observePaymentTasks, 30 * time.Second, nil})
	}
	for _, worker := range scheduled {
		workers.Go(func() {
			ticker := time.NewTicker(worker.interval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				case <-worker.wake:
				}
				if ctx.Err() != nil {
					return
				}
				cycle, cancel := context.WithTimeout(ctx, 8*time.Minute)
				worker.run(cycle)
				cancel()
			}
		})
	}
	workers.Wait()
}

func (s *service) reconcilePayPalOrders(ctx context.Context) {
	s.eachPayPalAccount(ctx, (*service).reconcilePayPalCheckoutCreation)
	jobs, err := claimPayPalOrders(ctx)
	if err != nil {
		log.Printf("task=order action=claim error=%v", err)
		return
	}

	for _, job := range jobs {
		requestCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
		switching, switchErr := s.reconcilePayPalCheckoutSwitch(requestCtx, job.id)
		var err error
		if !switching || switchErr != nil {
			_, err = s.completePayPalOrder(requestCtx, 0, job.id, !switching)
		}
		err = errors.Join(switchErr, err)
		cancel()
		if err != nil {
			log.Printf("task=order id=%s attempt=%d error=%v", job.id, job.attempt, err)
		}
		if saveErr := finishOrderReconciliation(ctx, job, err); saveErr != nil {
			log.Printf("task=order id=%s action=save error=%v", job.id, saveErr)
		}
	}
}

func (s *service) reconcilePayPalCheckoutCreation(ctx context.Context) {
	client := s.primaryPayPalClient()
	if client == nil || !client.WebhookConfigured(ctx) {
		return
	}
	account, err := client.CheckoutAccount(ctx)
	if err != nil {
		return
	}
	attempts, err := claimCheckoutCreate(ctx, account)
	if err != nil {
		log.Printf("task=checkout action=claim error=%v", err)
		return
	}
	for i := range attempts {
		if ctx.Err() != nil {
			return
		}
		attempt := &attempts[i]
		requestCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		_, _, err := s.sendPayPalCheckout(requestCtx, attempt)
		cancel()
		if err != nil {
			log.Printf("task=checkout intent=%s error=%v", attempt.intentID, err)
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
	client, err := s.payPalForSubscription(ctx, id)
	if err != nil || client == nil {
		return errors.Join(ErrCheckoutUnavailable, err)
	}
	details, err := client.GetSubscription(ctx, id)
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
	case "APPROVAL_PENDING":
		if details.LastPaymentTime == nil {
			cancelErr := client.CancelSubscription(ctx, id, reason)
			if cancelErr == nil {
				return s.repo.UpdateUserSubscriptionStatus(ctx, id, "CANCELLED")
			}
			if !isUnapprovedPayPalCancelRejected(cancelErr) {
				return cancelErr
			}
			verified, verifyErr := client.GetSubscription(ctx, id)
			if verifyErr != nil {
				return verifyErr
			}
			if verified.SubscriptionID != id {
				return errors.New("unexpected subscription ID")
			}
			if verified.Status == "CANCELLED" || verified.Status == "EXPIRED" {
				return s.repo.UpdateUserSubscriptionStatus(ctx, id, verified.Status)
			}
			if verified.Status == "APPROVAL_PENDING" && verified.LastPaymentTime == nil {
				return markPayPalSubscriptionLocallyCancelled(ctx, id)
			}
			return cancelErr
		}
		fallthrough
	default:
		if err := client.CancelSubscription(ctx, id, reason); err != nil {
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
	if sub == nil || sub.Environment != conversion.Environment() || strings.TrimSpace(sub.PlanID) == "" {
		return ErrCheckoutUnavailable
	}
	client, err := s.payPalForSubscription(ctx, id)
	if err != nil || client == nil {
		return errors.Join(ErrCheckoutUnavailable, err)
	}
	details, err := client.GetSubscription(ctx, id)
	if err != nil {
		return err
	}
	if details.SubscriptionID != id || details.PlanID != sub.PlanID {
		return ErrCheckoutUnavailable
	}
	if sub.Status == "CANCELLED" && (details.Status == "APPROVED" || details.Status == "ACTIVE" || details.Status == "SUSPENDED") {
		log.Printf("task=subscription action=requeue_late_cancel sub=%s live_status=%s", sub.SubscriptionID, details.Status)
		if err := requeueLateApprovedPayPalCancellation(ctx, sub.SubscriptionID, "Superseded PayPal subscription approved after cancellation"); err != nil {
			return err
		}
		if details.Status == "APPROVED" {
			return nil
		}
	} else {
		switch details.Status {
		case "APPROVED":
			return s.activateApprovedPayPalVIP(ctx, id)
		case "ACTIVE", "CANCELLED", "SUSPENDED", "EXPIRED":
			if err := s.repo.UpdateUserSubscriptionStatus(ctx, id, details.Status); err != nil {
				return err
			}
		}
	}
	if details.Status == "APPROVAL_PENDING" {
		return nil
	}
	return s.importPayPalSubscriptionTransactions(ctx, sub, details, from, to)
}

func (s *service) activateApprovedPayPalVIP(ctx context.Context, subscriptionID string) error {
	client, err := s.payPalForSubscription(ctx, subscriptionID)
	if err != nil || client == nil {
		return errors.Join(ErrCheckoutUnavailable, err)
	}
	account, err := client.CheckoutAccount(ctx)
	if err != nil || !paymentEnvironmentMatches(account) {
		return errors.Join(ErrCheckoutUnavailable, err)
	}
	send, err := s.repo.claimVIPActivation(ctx, subscriptionID, account)
	if err != nil || !send {
		return err
	}
	return client.ActivateSubscription(ctx, subscriptionID)
}

func (s *service) importPayPalSubscriptionTransactions(ctx context.Context,
	sub *model.UserSubscription, details *payment.SubscriptionDetails, from, to time.Time) error {

	client, err := s.payPalForSubscription(ctx, sub.SubscriptionID)
	if err != nil || client == nil {
		return errors.Join(ErrCheckoutUnavailable, err)
	}
	transactions, err := client.ListSubscriptionTransactions(ctx, sub.SubscriptionID, from, to)
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
	claimed, err := claimPaymentTaskMetrics(request)
	if err != nil {
		log.Printf("payment_tasks action=claim error=%v", err)
		return
	}
	if !claimed {
		return
	}
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
