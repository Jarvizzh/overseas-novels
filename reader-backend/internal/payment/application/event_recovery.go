package application

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"reader-backend/internal/db"
	"reader-backend/internal/payment"
	"reader-backend/internal/payment/paypal"
	"reader-backend/internal/payment/stripe"
)

const (
	paymentEventRecoveryInterval = time.Minute
	paymentEventWindow           = time.Hour
	paymentEventOverlap          = 2 * time.Minute
	paymentEventInitialLookback  = 15 * time.Minute
	paymentEventPageSize         = 100
)

type paymentEventWindowLease struct {
	accountID  int64
	start, end time.Time
	cursor     string
	token      uuid.UUID
}

// SchedulePaymentEventRecovery restarts discovery without changing inbox items.
// Existing workers retain ownership of any page they are currently importing.
func SchedulePaymentEventRecovery(ctx context.Context, provider string, account payment.WebhookAccount, from time.Time) error {
	now := time.Now().Truncate(time.Second)
	if account.AccountKey == "" || (account.Environment != "sandbox" && account.Environment != "live") ||
		(provider != payment.ProviderStripe && provider != payment.ProviderPayPal) || from.IsZero() || !from.Before(now) {
		return errors.New("event recovery requires a channel, account, environment and past start time")
	}
	if provider == payment.ProviderStripe && now.Sub(from) >= 30*24*time.Hour {
		return errors.New("Stripe event window exceeds retention; recover from transaction resources")
	}
	end := from.Add(paymentEventWindow)
	if end.After(now) {
		end = now
	}
	result, err := db.Pool(ctx).Exec(ctx, `INSERT INTO payment_event_recovery(account_id,window_start,window_end)
		SELECT id,$4,$5 FROM payment_channel_accounts WHERE provider=$1 AND account_key=$2 AND environment=$3
		ON CONFLICT(account_id) DO UPDATE SET window_start=EXCLUDED.window_start,window_end=EXCLUDED.window_end,
		page_cursor='',next_attempt_at=now(),lease_token=NULL,lease_until=NULL,last_error=NULL
		WHERE payment_event_recovery.lease_until IS NULL OR payment_event_recovery.lease_until<=now()`,
		provider, account.AccountKey, account.Environment, from, end)
	if err == nil && result.RowsAffected() == 0 {
		return errors.New("channel account was not found or a recovery page is currently leased")
	}
	return err
}

func claimPaymentEventWindow(ctx context.Context, provider string, account payment.WebhookAccount) (paymentEventWindowLease, error) {
	var window paymentEventWindowLease
	window.token = uuid.New()
	pool := db.Pool(ctx)
	_, err := pool.Exec(ctx, `INSERT INTO payment_event_recovery(account_id,window_start,window_end)
		SELECT id,now()-make_interval(secs=>$4::int),now() FROM payment_channel_accounts
		WHERE provider=$1 AND account_key=$2 AND environment=$3 ON CONFLICT DO NOTHING`, provider, account.AccountKey, account.Environment,
		int(paymentEventInitialLookback/time.Second))
	if err != nil {
		return window, err
	}
	err = pool.QueryRow(ctx, `UPDATE payment_event_recovery r SET lease_token=$4,lease_until=now()+interval '2 minutes'
		FROM payment_channel_accounts a WHERE a.id=r.account_id AND a.provider=$1 AND a.account_key=$2 AND a.environment=$3
		  AND r.next_attempt_at<=now() AND (r.lease_until IS NULL OR r.lease_until<=now())
		RETURNING r.account_id,r.window_start,r.window_end,r.page_cursor`, provider, account.AccountKey, account.Environment, window.token).
		Scan(&window.accountID, &window.start, &window.end, &window.cursor)
	return window, err
}

func finishPaymentEventWindow(ctx context.Context, window paymentEventWindowLease, next string, result error) error {
	pool := db.Pool(ctx)
	if result != nil {
		_, err := pool.Exec(ctx, `UPDATE payment_event_recovery SET lease_token=NULL,lease_until=NULL,last_error=left($3,500),
			next_attempt_at=now()+interval '1 minute' WHERE account_id=$1 AND lease_token=$2`, window.accountID, window.token, result.Error())
		return err
	}
	_, err := pool.Exec(ctx, `UPDATE payment_event_recovery SET page_cursor=$3,lease_token=NULL,lease_until=NULL,last_error=NULL,
		last_complete_at=CASE WHEN $3='' THEN window_end ELSE last_complete_at END,
		window_start=CASE WHEN $3='' THEN window_end-make_interval(secs=>$4::int) ELSE window_start END,
		window_end=CASE WHEN $3='' THEN least(now(),window_end+make_interval(secs=>$5::int)) ELSE window_end END,
		next_attempt_at=CASE WHEN $3='' AND window_end>=now()-interval '1 minute' THEN now()+interval '1 minute' ELSE now() END
		WHERE account_id=$1 AND lease_token=$2`, window.accountID, window.token, next, int(paymentEventOverlap/time.Second), int(paymentEventWindow/time.Second))
	return err
}

func (s *service) recoverPayPalEventWindow(ctx context.Context) {
	adapter, err := payment.Capability[interface {
		CheckoutAccount(context.Context) (payment.WebhookAccount, error)
		WebhookConfigured(context.Context) bool
		ListEvents(context.Context, paypal.EventQuery) (paypal.EventPage, error)
	}](s.adapters, payment.ProviderPayPal)
	if err != nil || !adapter.WebhookConfigured(ctx) {
		return
	}
	account, err := adapter.CheckoutAccount(ctx)
	if err != nil {
		return
	}
	s.retryRecoveredEvents(ctx, payment.ProviderPayPal, account, func(ctx context.Context, raw json.RawMessage) (error, error) {
		var event paypal.Event
		if err := json.Unmarshal(raw, &event); err != nil {
			return err, nil
		}
		event.Account, event.Raw = account, raw
		return s.enqueueRecoveredPayPal(ctx, event)
	})
	s.recoverPaymentEventWindow(ctx, payment.ProviderPayPal, account, func(ctx context.Context, w paymentEventWindowLease) (string, error) {
		page, err := adapter.ListEvents(ctx, paypal.EventQuery{From: w.start, Through: w.end, Cursor: w.cursor, PageSize: paymentEventPageSize})
		if err != nil {
			return "", err
		}
		for _, event := range page.Items {
			if event.Account != account {
				return "", errors.New("PayPal event recovery scope changed")
			}
			decodeErr, err := s.enqueueRecoveredPayPal(ctx, event)
			if err != nil {
				return "", err
			}
			if decodeErr != nil {
				if err := saveUnprocessedPaymentEvent(ctx, w.accountID, event.ID, event.Raw, decodeErr); err != nil {
					return "", err
				}
			}
		}
		return page.Next, nil
	})
}

func (s *service) enqueueRecoveredPayPal(ctx context.Context, event paypal.Event) (error, error) {
	fact, err := event.Evidence()
	if err != nil {
		return err, nil
	}
	if fact.EventType == "" || fact.EventType == "unknown" || fact.EventType == "ignored" {
		return nil, nil
	}
	fact.LookupOnly = true
	_, registered := payment.AccountCapability[payment.WebhookProvider](s.adapters, payment.ProviderPayPal, event.Account)
	if err := s.repo.enqueuePayPalEvent(ctx, fact, registered); err != nil {
		return nil, err
	}
	notifyWorker(s.paypalEventsWake)
	return nil, nil
}

func (s *service) recoverStripeEventWindow(ctx context.Context) {
	adapter, err := payment.Capability[interface {
		AccountScope() payment.WebhookAccount
		ListEvents(context.Context, stripe.EventQuery) (stripe.Page[stripe.Event], error)
		PaymentNotice(stripe.Event) (*payment.SingleCheckoutNotice, error)
	}](s.adapters, payment.ProviderStripe)
	if err != nil {
		return
	}
	account := adapter.AccountScope()
	_, usesUniversalDispatch := adapter.(stripeWebhookResolver)
	enqueueRecovered := func(ctx context.Context, event stripe.Event, raw []byte) (review, transient error) {
		if usesUniversalDispatch {
			if event.IncompleteSnapshot {
				return errors.New("incomplete Stripe event snapshot"), nil
			}
			if stripeAccountScope(event.Scope) != account {
				return stripe.ErrEventScopeMismatch, nil
			}
			if !stripe.IsHandledEventType(event.Type) {
				return nil, nil
			}
			if err := enqueueStripeEvent(ctx, event, raw); err != nil {
				return nil, err
			}
			notifyWorker(s.stripeEventsWake)
			return nil, nil
		}
		notice, err := adapter.PaymentNotice(event)
		if err != nil {
			return err, nil
		}
		if err := queueStripeNotice(ctx, notice, raw); err != nil {
			return nil, err
		}
		notifyWorker(s.stripeEventsWake)
		return nil, nil
	}
	s.retryRecoveredEvents(ctx, payment.ProviderStripe, account, func(ctx context.Context, raw json.RawMessage) (error, error) {
		var event stripe.Event
		if err := json.Unmarshal(raw, &event); err != nil {
			return err, nil
		}
		return enqueueRecovered(ctx, event, raw)
	})
	s.recoverPaymentEventWindow(ctx, payment.ProviderStripe, account, func(ctx context.Context, w paymentEventWindowLease) (string, error) {
		if time.Since(w.start) >= 30*24*time.Hour {
			return "", errors.New("Stripe event window exceeds retention; recover from transaction resources")
		}
		page, err := adapter.ListEvents(ctx, stripe.EventQuery{Created: stripe.TimeRange{From: w.start, Before: w.end},
			PageOptions: stripe.PageOptions{Limit: paymentEventPageSize, StartingAfter: w.cursor}})
		if err != nil {
			return "", err
		}
		for _, event := range page.Items {
			body, err := json.Marshal(event)
			if err != nil {
				return "", err
			}
			review, transient := enqueueRecovered(ctx, event, body)
			if transient != nil {
				return "", transient
			}
			if review != nil {
				if err := saveUnprocessedPaymentEvent(ctx, w.accountID, event.ID, body, review); err != nil {
					return "", err
				}
				continue
			}
		}
		if !page.HasMore {
			return "", nil
		}
		if len(page.Items) == 0 {
			return "", errors.New("Stripe event page did not advance")
		}
		return page.Items[len(page.Items)-1].ID, nil
	})
}

func (s *service) recoverPaymentEventWindow(ctx context.Context, provider string, account payment.WebhookAccount,
	recoverPage func(context.Context, paymentEventWindowLease) (string, error)) {
	w, err := claimPaymentEventWindow(ctx, provider, account)
	if errors.Is(err, pgx.ErrNoRows) {
		return
	}
	if err != nil {
		log.Printf("task=payment_event_recovery provider=%s action=claim error=%v", provider, err)
		return
	}
	request, cancel := context.WithTimeout(ctx, 45*time.Second)
	next, result := recoverPage(request, w)
	cancel()
	if result == nil && next != "" && next == w.cursor {
		result = errors.New("payment event cursor did not advance")
	}
	if result != nil {
		log.Printf("task=payment_event_recovery provider=%s action=recover error=%v", provider, result)
	}
	if err := finishPaymentEventWindow(ctx, w, next, result); err != nil {
		log.Printf("task=payment_event_recovery provider=%s action=finish error=%v", provider, err)
	}
}
