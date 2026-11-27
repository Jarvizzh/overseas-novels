package application

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"reader-backend/internal/config"
	"reader-backend/internal/conversion"
	"reader-backend/internal/db"
	"reader-backend/internal/payment"
	"reader-backend/internal/payment/aftercare"
)

const stripeCheckoutInterval = 15 * time.Second

func stripeSingleCheckoutEnabled(ctx context.Context) bool {
	if pool := db.Pool(ctx); pool != nil {
		var dbManaged bool
		err := pool.QueryRow(ctx, `SELECT credentials_ref = 'db:encrypted' AND status = 'published' AND is_primary AND new_orders_enabled
			FROM payment_channel_accounts WHERE provider = 'stripe' AND environment = $1 AND status = 'published' AND is_primary = true
			LIMIT 1`, currentPaymentLedgerEnvironment()).Scan(&dbManaged)
		if err == nil && dbManaged {
			return true
		}
	}
	return config.AppConfig != nil && config.AppConfig.StripeSingleCheckoutEnabled
}

func (s *service) stripeCheckoutAdapter() (payment.SingleCheckoutAdapter, bool) {
	provider, err := payment.Capability[payment.SingleCheckoutAdapter](s.adapters, payment.ProviderStripe)
	if err != nil || !paymentEnvironmentMatches(provider.AccountScope()) {
		return nil, false
	}
	return provider, true
}

func paymentEnvironmentMatches(account payment.WebhookAccount) bool {
	switch account.Environment {
	case "sandbox":
		return conversion.Environment() == "sandbox"
	case "live":
		return conversion.Environment() == "production"
	default:
		return false
	}
}

func (s *service) GetStripeOfferSlots(ctx context.Context, userID int64) ([]int, error) {
	provider, available := s.stripeCheckoutAdapter()
	if !available || !stripeSingleCheckoutEnabled(ctx) {
		return []int{}, nil
	}
	return s.repo.stripeOfferSlots(ctx, provider.AccountScope(), userID)
}

func (s *service) StartStripeCheckout(ctx context.Context, userID int64, intentID, origin string) (*PurchaseIntent, error) {
	intent, err := s.getPurchase(ctx, userID, intentID, "", false)
	if err != nil {
		return nil, err
	}
	if intent.OrderType == "subscription" {
		return s.startVIPStripeCheckout(ctx, userID, intent.ID, origin)
	}
	provider, available := s.stripeCheckoutAdapter()
	if !available {
		return nil, ErrCheckoutPaused
	}
	if intent.Status != "ready" && intent.Status != "retryable" && intent.Status != "confirming" && intent.Status != "awaiting_approval" {
		return intent, nil
	}
	site, err := s.checkoutSite(ctx, origin)
	if err != nil {
		return nil, err
	}
	if intent.Provider == payment.ProviderPayPal && (intent.Status == "confirming" || intent.Status == "awaiting_approval") {
		allowed, err := s.canSwitchPayPalOrderToStripe(ctx, userID, intent)
		if err != nil {
			return nil, err
		}
		if !allowed {
			return nil, ErrCheckoutPaused
		}
		if err := s.closePayPalForCheckoutSwitch(ctx, userID, intent); err != nil {
			return nil, err
		}
		intent, err = s.GetPurchase(ctx, userID, intentID, "")
		if err != nil {
			return nil, err
		}
	}
	if intent.Status != "ready" && intent.Status != "retryable" {
		return intent, nil
	}
	if !stripeSingleCheckoutEnabled(ctx) {
		return nil, ErrCheckoutPaused
	}
	site.Path = "/recharge"
	site.RawQuery = url.Values{"purchase": {intent.ID}, "payment": {"return"}}.Encode()
	returnURL := site.String()
	site.RawQuery = url.Values{"purchase": {intent.ID}, "payment": {"cancel"}}.Encode()
	attempt, err := s.repo.reserveStripePurchase(ctx, userID, intent.ID, returnURL, site.String(), provider.AccountScope())
	if err != nil {
		return nil, err
	}
	due, err := s.repo.claimStripeAttempts(ctx, provider.AccountScope(), attempt.ID, stripeSingleCheckoutEnabled(ctx))
	if err != nil {
		return nil, err
	}
	for _, item := range due {
		if err := s.syncStripeAttempt(ctx, provider, item, ""); err != nil {
			log.Printf("task=stripe_checkout action=create attempt=%s error=%v", item.ID, err)
		}
	}
	return s.GetPurchase(ctx, userID, intent.ID, "")
}

func (s *service) syncStripeAttempt(ctx context.Context, provider payment.SingleCheckoutAdapter, a stripePurchaseAttempt, sessionID string) error {
	if sessionID == "" {
		sessionID = a.SessionID
	}
	if sessionID == "" && a.MayCreate {
		allowed, err := s.repo.wholeBookWriteAllowed(ctx, a.OrderID)
		if err != nil {
			return err
		}
		if !allowed {
			return nil
		}
		frozenParams := a.FrozenParams
		if frozenParams == nil {
			policy, err := loadStripeAccountCheckoutPolicy(ctx, db.Pool(ctx), a.AccountID)
			if err != nil {
				return err
			}
			customer, err := ensureStripeCustomer(ctx, provider, a.UserID, a.AccountID, a.Account)
			if err != nil {
				return err
			}
			frozen, err := freezeSingleAttemptParams(ctx, a.ID, buildFrozenCheckoutParams(a.Offer.SalesMode, stripeCheckoutModeOneTime, policy, customer))
			if err != nil {
				return err
			}
			frozenParams = &frozen
		}
		proof, err := provider.CreateSingleCheckout(ctx, payment.SingleCheckoutRequest{
			AttemptID: a.ID, SuccessURL: a.ReturnURL, CancelURL: a.CancelURL, Offer: a.Offer, Params: frozenParams,
		})
		if err != nil {
			return recordStripeWriteFailure(ctx, err, `UPDATE payment_checkout_attempts SET create_lookup_only=true
				WHERE id=$1 AND account_id=$2 AND status='creating' AND provider_order_id IS NULL`, a.ID, a.AccountID)
		}
		return s.repo.applyStripeProof(ctx, a, proof, nil)
	}
	if sessionID == "" {
		windowEnd := a.CreatedAt.Add(stripeCreateRetryWindow + time.Hour)
		var err error
		sessionID, err = provider.FindSingleCheckout(ctx, a.ID, a.CreatedAt.Add(-time.Minute), windowEnd)
		if err != nil || sessionID == "" {
			return err
		}
	}
	proof, err := provider.ReadSingleCheckout(ctx, sessionID, a.ID, a.Offer)
	if err != nil {
		return err
	}
	if proof.AwaitingBuyer && proof.Receipt == nil {
		superseded, err := anotherWholeBookPaid(ctx, db.Pool(ctx), a.OrderID)
		if err != nil {
			return err
		}
		if closer, ok := provider.(interface {
			CloseSingleCheckout(context.Context, string, string, payment.SingleCheckoutOffer) (payment.SingleCheckoutProof, error)
		}); superseded && ok {
			proof, err = closer.CloseSingleCheckout(ctx, sessionID, a.ID, a.Offer)
			if err != nil {
				return err
			}
		}
	}
	var verification stripeSettlementVerification
	if proof.Receipt != nil && proof.Receipt.Currency == payment.CurrencyUSD {
		verification = verifyStripeChargeSettlement(ctx, provider, *proof.Receipt, "stripe_checkout", a.ID)
	}
	return s.repo.applyStripeProofWithSettlement(ctx, a, proof, verification)
}

func (s *service) reconcileStripeCheckouts(ctx context.Context) {
	provider, err := payment.Capability[payment.SingleCheckoutAdapter](s.adapters, payment.ProviderStripe)
	if err != nil {
		return
	}
	account := provider.AccountScope()
	jobs, err := claimStripeNotices(ctx, account)
	if err != nil {
		log.Printf("task=stripe_checkout action=claim_events error=%v", err)
		return
	}
	for _, job := range jobs {
		request, cancel := context.WithTimeout(ctx, 45*time.Second)
		err := s.dispatchStripeInboxJob(request, provider, account, job)
		cancel()
		if errors.Is(err, pgx.ErrNoRows) {
			err = nil
		}
		if finishErr := finishInboxEvent(ctx, inboxJob{id: job.id, attempt: job.lease}, err); finishErr != nil {
			log.Printf("task=stripe_checkout action=finish_event id=%d error=%v", job.id, finishErr)
		}
	}
	if !paymentEnvironmentMatches(account) {
		return
	}
	due, err := s.repo.claimStripeAttempts(ctx, account, "", !s.historicalStripe && stripeSingleCheckoutEnabled(ctx))
	if err != nil {
		log.Printf("task=stripe_checkout action=claim_attempts error=%v", err)
		return
	}
	for _, item := range due {
		request, cancel := context.WithTimeout(ctx, 45*time.Second)
		err := s.syncStripeAttempt(request, provider, item, "")
		cancel()
		if err != nil {
			log.Printf("task=stripe_checkout action=sync attempt=%s error=%v", item.ID, err)
		}
	}
}

func queueStripeNotice(ctx context.Context, n *payment.SingleCheckoutNotice, rawEvent []byte) error {
	if n == nil || (len(n.Aftercare) == 0 && !paymentEnvironmentMatches(n.Account)) {
		return nil
	}
	if len(n.Aftercare) != 0 {
		_, err := db.Pool(ctx).Exec(ctx, `INSERT INTO payment_event_inbox(provider,account_id,event_id,event_type,amount_cents,currency,event)
			SELECT 'stripe',a.id,$1,$2,0,'USD',jsonb_build_object('aftercare',$3::jsonb,'source',$4::jsonb)
			FROM payment_channel_accounts a WHERE a.provider='stripe' AND a.account_key=$5 AND a.environment=$6
			ON CONFLICT ON CONSTRAINT payment_event_identity DO NOTHING`, n.EventID, n.EventType, n.Aftercare, rawEvent, n.Account.AccountKey, n.Account.Environment)
		return err
	}
	if n.RefundID != "" || n.InvoiceID != "" || n.SubscriptionID != "" || n.VIPIntentID != "" || n.SettlementReference != "" {
		if n.VIPIntentID != "" {
			if _, err := uuid.Parse(n.VIPIntentID); err != nil {
				return nil
			}
		}
		_, err := db.Pool(ctx).Exec(ctx, `INSERT INTO payment_event_inbox(provider,account_id,event_id,event_type,amount_cents,currency,event)
			SELECT 'stripe',a.id,$1,$2,0,'USD',jsonb_build_object('refund_id',$3::text,'invoice_id',$4::text,
			 'subscription_id',$5::text,'vip_intent_id',$6::text,'session_id',$7::text,'source',$8::jsonb,'settlement_ref',$11::text)
			FROM payment_channel_accounts a WHERE a.provider='stripe' AND a.account_key=$9 AND a.environment=$10
			ON CONFLICT ON CONSTRAINT payment_event_identity DO NOTHING`, n.EventID, n.EventType, n.RefundID, n.InvoiceID, n.SubscriptionID,
			n.VIPIntentID, n.SessionID, rawEvent, n.Account.AccountKey, n.Account.Environment, n.SettlementReference)
		return err
	}
	if _, err := uuid.Parse(n.AttemptID); err != nil {
		return nil
	}
	_, err := db.Pool(ctx).Exec(ctx, `INSERT INTO payment_event_inbox(provider,account_id,event_id,event_type,amount_cents,currency,event)
		SELECT 'stripe',a.id,$1,$2,0,'USD',jsonb_build_object('attempt_id',$3::uuid,'session_id',$4::text,'source',$8::jsonb)
		FROM payment_checkout_attempts p JOIN payment_channel_accounts a ON a.id=p.account_id
		JOIN payment_stripe_checkout_terms t ON t.attempt_id=p.id JOIN recharge_orders o ON o.id=p.order_id
		WHERE p.id=$3 AND p.provider='stripe' AND a.account_key=$5 AND a.environment=$6 AND o.checkout_environment=$7
		ON CONFLICT ON CONSTRAINT payment_event_identity DO NOTHING`, n.EventID, n.EventType, n.AttemptID, n.SessionID,
		n.Account.AccountKey, n.Account.Environment, conversion.Environment(), rawEvent)
	return err
}

type stripeNoticeJob struct {
	id                                     int64
	lease                                  int
	attemptID, sessionID, refundID         string
	vipIntentID, invoiceID, subscriptionID string
	aftercare                              json.RawMessage
	eventID, settlementReference           string
	envelope                               *stripeInboxEnvelope
}

func claimStripeNotices(ctx context.Context, account payment.WebhookAccount) ([]stripeNoticeJob, error) {
	rows, err := db.Pool(ctx).Query(ctx, `WITH due AS (
		SELECT i.id FROM payment_event_inbox i JOIN payment_channel_accounts a ON a.id=i.account_id
		WHERE i.provider='stripe' AND a.account_key=$1 AND a.environment=$2
		  AND i.processed_at IS NULL AND i.next_attempt_at<=now()
		ORDER BY i.next_attempt_at,i.id LIMIT 5 FOR UPDATE OF i SKIP LOCKED
	) UPDATE payment_event_inbox i SET attempts=i.attempts+1,next_attempt_at=now()+interval '10 minutes'
		FROM due WHERE i.id=due.id RETURNING i.id,i.attempts,COALESCE(i.event->>'attempt_id',''),COALESCE(i.event->>'session_id',''),COALESCE(i.event->>'refund_id',''),
			COALESCE(i.event->>'vip_intent_id',''),COALESCE(i.event->>'invoice_id',''),COALESCE(i.event->>'subscription_id',''),i.event->'aftercare',
			i.event_id,COALESCE(i.event->>'settlement_ref',''),
			CASE WHEN (i.event->>'envelope_version') IS NOT NULL THEN i.event ELSE NULL END`,
		account.AccountKey, account.Environment)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (stripeNoticeJob, error) {
		var item stripeNoticeJob
		var rawEnvelope json.RawMessage
		err := row.Scan(&item.id, &item.lease, &item.attemptID, &item.sessionID, &item.refundID, &item.vipIntentID,
			&item.invoiceID, &item.subscriptionID, &item.aftercare, &item.eventID, &item.settlementReference, &rawEnvelope)
		if err != nil {
			return item, err
		}
		if len(rawEnvelope) > 0 {
			var env stripeInboxEnvelope
			if err := json.Unmarshal(rawEnvelope, &env); err != nil {
				return item, err
			}
			item.envelope = &env
		}
		return item, nil
	})
}

func (s *service) processQueuedStripeAftercare(ctx context.Context, snapshot json.RawMessage) error {
	adapter, err := payment.Capability[interface {
		ResolveQueuedAftercare(context.Context, json.RawMessage) (*payment.CaseObservation, error)
	}](s.adapters, payment.ProviderStripe)
	if err != nil {
		return err
	}
	evidence, err := adapter.ResolveQueuedAftercare(ctx, snapshot)
	if err != nil {
		return err
	}
	return aftercare.NewRepository(db.Pool(ctx)).RecordObservation(ctx, evidence)
}
