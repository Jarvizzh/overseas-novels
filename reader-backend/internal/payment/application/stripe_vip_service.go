package application

import (
	"context"
	"errors"
	"log"
	"net/url"
	"slices"
	"time"

	"github.com/google/uuid"
	"reader-backend/internal/config"
	"reader-backend/internal/db"
	"reader-backend/internal/payment"
)

const stripeVIPRequestTimeout = 45 * time.Second

func (s *service) stripeVIPAdapter() (payment.StripeVIPAdapter, bool) {
	adapter, err := payment.Capability[payment.StripeVIPAdapter](s.adapters, payment.ProviderStripe)
	if err != nil || !paymentEnvironmentMatches(adapter.AccountScope()) {
		return nil, false
	}
	return adapter, true
}

func stripeVIPCheckoutEnabled(ctx context.Context) bool {
	if pool := db.Pool(ctx); pool != nil {
		var dbManaged bool
		err := pool.QueryRow(ctx, `SELECT credentials_ref = 'db:encrypted' AND status = 'published' AND is_primary AND new_subscriptions_enabled
			FROM payment_channel_accounts WHERE provider = 'stripe' AND environment = $1 AND status = 'published' AND is_primary = true
			LIMIT 1`, currentPaymentLedgerEnvironment()).Scan(&dbManaged)
		if err == nil && dbManaged {
			return true
		}
	}
	return config.AppConfig != nil && config.AppConfig.StripeVIPCheckoutEnabled
}

func appendStripeVIPReturn(rawURL, intentID string) string {
	target, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	query := target.Query()
	query.Set("stripe_vip", intentID)
	target.RawQuery = query.Encode()
	return target.String()
}

func (s *service) GetStripeVIPOfferSlots(ctx context.Context, userID int64) ([]int, error) {
	adapter, available := s.stripeVIPAdapter()
	if !available || !stripeVIPCheckoutEnabled(ctx) {
		return []int{}, nil
	}
	return stripeVIPSlots(ctx, adapter.AccountScope(), userID)
}

func (s *service) StartStripeVIP(ctx context.Context, userID int64, slot int, origin, returnPath string, attribution checkoutAttribution) (*PurchaseIntent, error) {
	adapter, available := s.stripeVIPAdapter()
	if !available {
		return nil, ErrCheckoutPaused
	}
	site, err := s.checkoutSite(ctx, origin)
	if err != nil {
		return nil, err
	}
	site.Path = "/recharge"
	site.RawQuery = "payment=return"
	returnURL := site.String()
	site.RawQuery = "payment=cancel"
	v, err := reserveStripeVIP(ctx, stripeVIPRequest{
		userID: userID, slotID: slot, returnURL: returnURL, cancelURL: site.String(),
		returnPath: checkoutReadingPath(returnPath, 0), attribution: attribution, account: adapter.AccountScope(),
	}, stripeVIPCheckoutEnabled(ctx))
	if err != nil {
		return nil, err
	}
	primary := adapter.AccountScope() == v.Account
	if !primary {
		adapter, available = stripeCapabilityForAccount[payment.StripeVIPAdapter](s, v.Account)
		if !available {
			return nil, ErrSubscriptionConflict
		}
	}
	due, err := claimStripeVIP(ctx, v.Account, v.ID, primary && stripeVIPCheckoutEnabled(ctx))
	if err != nil {
		return nil, err
	}
	for _, intent := range due {
		request, cancel := context.WithTimeout(ctx, stripeVIPRequestTimeout)
		err := s.syncStripeVIP(request, adapter, intent, "")
		cancel()
		if err != nil {
			log.Printf("task=stripe_vip action=start intent=%s error=%v", intent.ID, err)
		}
	}
	return stripeVIPPurchase(ctx, userID, v.Account, v.ID)
}

func (s *service) GetStripeVIPPurchase(ctx context.Context, userID int64, rawID string) (*PurchaseIntent, error) {
	id, err := uuid.Parse(rawID)
	if err != nil || id == uuid.Nil {
		return nil, ErrCheckoutUnavailable
	}
	account, err := stripeVIPAccountForOwner(ctx, userID, id.String(), true)
	if err != nil {
		return nil, err
	}
	return stripeVIPPurchase(ctx, userID, account, id.String())
}

func (s *service) OpenStripeVIPPortal(ctx context.Context, userID int64, subscription, origin string) (string, error) {
	account, err := stripeVIPAccountForOwner(ctx, userID, subscription, false)
	if err != nil {
		return "", err
	}
	adapter, available := stripeCapabilityForAccount[payment.StripeVIPAdapter](s, account)
	if !available {
		return "", ErrCheckoutUnavailable
	}
	v, err := stripeVIPForOwner(ctx, adapter.AccountScope(), userID, subscription)
	if err != nil {
		return "", err
	}
	if v.Offer.SalesMode != payment.StripeSaleDirect || v.CustomerID == "" {
		return "", ErrCheckoutUnavailable
	}
	site, err := s.checkoutSite(ctx, origin)
	if err != nil {
		return "", err
	}
	site.Path = "/recharge"
	request, cancel := context.WithTimeout(ctx, stripeVIPRequestTimeout)
	defer cancel()
	proof, err := adapter.ReadStripeVIPSubscription(request, subscription)
	if err != nil {
		return "", err
	}
	if proof.IntentID != v.ID || proof.CustomerID != v.CustomerID || proof.ID != v.SubscriptionID {
		return "", ErrCheckoutUnavailable
	}
	return adapter.OpenStripeVIPPortal(request, v.CustomerID, site.String(), uuid.NewString())
}

func (s *service) cancelStripeVIP(ctx context.Context, userID int64, subscription, reason string) error {
	account, err := stripeVIPAccountForOwner(ctx, userID, subscription, false)
	if err != nil {
		return err
	}
	if _, available := stripeCapabilityForAccount[payment.StripeVIPAdapter](s, account); !available {
		return ErrCheckoutUnavailable
	}
	v, err := stripeVIPForOwner(ctx, account, userID, subscription)
	if err != nil {
		return err
	}
	return queueStripeVIPCancellation(ctx, v, reason)
}

func (s *service) refreshUnifiedStripeVIP(ctx context.Context, row vipCheckoutIntentRow) {
	account, err := stripeVIPAccountForOwner(ctx, row.userID, row.stripeIntentID, true)
	if err != nil {
		return
	}
	adapter, ok := stripeCapabilityForAccount[payment.StripeVIPAdapter](s, account)
	if !ok {
		return
	}
	request, cancel := context.WithTimeout(ctx, stripeVIPRequestTimeout)
	defer cancel()
	intent, err := findStripeVIPIntent(request, db.Pool(ctx), account, row.stripeIntentID)
	if err != nil || intent.Status == "closed" {
		return
	}
	if intent.SessionID != "" || intent.SubscriptionID != "" {
		_ = s.syncStripeVIP(request, adapter, intent, "")
		return
	}
	primary, available := s.stripeVIPAdapter()
	allowNew := available && primary.AccountScope() == account && !s.historicalStripe && stripeVIPCheckoutEnabled(request)
	due, err := claimStripeVIP(request, account, intent.ID, allowNew)
	if err != nil {
		return
	}
	for _, item := range due {
		if err := s.syncStripeVIP(request, adapter, item, ""); err != nil {
			log.Printf("task=stripe_vip action=refresh intent=%s error=%v", item.ID, err)
		}
	}
}

func findUnidentifiedStripeVIPCheckout(ctx context.Context, adapter payment.StripeVIPAdapter, v stripeVIPIntent) (string, error) {
	return adapter.FindStripeVIPCheckout(ctx, v.ID, v.FirstSentAt.Add(-time.Minute), v.FirstSentAt.Add(stripeCreateRetryWindow+time.Hour))
}

func (s *service) syncStripeVIP(ctx context.Context, adapter payment.StripeVIPAdapter, v stripeVIPIntent, sessionID string) error {
	if sessionID == "" && v.SubscriptionID != "" {
		return s.syncStripeVIPSubscription(ctx, adapter, v.SubscriptionID, "")
	}
	if sessionID == "" {
		sessionID = v.SessionID
	}
	var proof payment.StripeVIPCheckout
	var err error
	if sessionID == "" && v.MayCreate {
		proof, err = adapter.CreateStripeVIPCheckout(ctx, payment.StripeVIPCheckoutRequest{
			IntentID: v.ID, ReturnURL: v.ReturnURL, CancelURL: v.CancelURL, Offer: v.Offer,
		})
		if err != nil {
			return recordStripeWriteFailure(ctx, err, `UPDATE payment_stripe_subscription_intents SET create_lookup_only=true
				WHERE id=$1 AND account_id=$2 AND status='creating' AND checkout_ref IS NULL`, v.ID, v.AccountID)
		}
	} else {
		if sessionID == "" {
			if v.FirstSentAt == nil {
				return nil
			}
			sessionID, err = findUnidentifiedStripeVIPCheckout(ctx, adapter, v)
			if err != nil || sessionID == "" {
				return err
			}
		}
		proof, err = adapter.ReadStripeVIPCheckout(ctx, sessionID, v.ID, v.Offer.SalesMode)
	}
	if err != nil {
		return err
	}
	if err := saveStripeVIPCheckout(ctx, v, proof); err != nil {
		return err
	}
	if proof.SubscriptionID != "" {
		return s.syncStripeVIPSubscription(ctx, adapter, proof.SubscriptionID, "")
	}
	return nil
}

func (s *service) syncStripeVIPSubscription(ctx context.Context, adapter payment.StripeVIPAdapter, subscription, invoice string) error {
	if subscription == "" {
		var err error
		subscription, err = adapter.StripeInvoiceSubscription(ctx, invoice)
		if err != nil || subscription == "" {
			return err
		}
	}
	proof, err := adapter.ReadStripeVIPSubscription(ctx, subscription)
	if err != nil {
		return err
	}
	id, bad := uuid.Parse(proof.IntentID)
	if bad != nil || id == uuid.Nil {
		return nil
	}
	v, err := findStripeVIPIntent(ctx, db.Pool(ctx), adapter.AccountScope(), id.String())
	if err != nil {
		return err
	}
	if invoice != "" {
		return s.syncStripeVIPInvoice(ctx, adapter, v, invoice, subscription)
	}
	if err := bindStripeVIP(ctx, v, proof); err != nil {
		return err
	}
	refs, next, err := adapter.StripeVIPInvoiceRefs(ctx, subscription, "")
	if err != nil {
		return err
	}
	if proof.LatestInvoice != "" && !slices.Contains(refs, proof.LatestInvoice) {
		refs = append([]string{proof.LatestInvoice}, refs...)
	}
	if v.InvoiceScanCursor != "" {
		older, cursor, err := adapter.StripeVIPInvoiceRefs(ctx, subscription, v.InvoiceScanCursor)
		if err != nil {
			return err
		}
		refs = append(refs, older...)
		next = cursor
	}

	// The Stripe list is newest first. Admit older invoices first so paid periods
	// and the first-payment event can recover cleanly after an outage.
	slices.Reverse(refs)
	refs = slices.Compact(refs)
	var failures []error
	for _, id := range refs {
		if err := s.syncStripeVIPInvoice(ctx, adapter, v, id, subscription); err != nil {
			failures = append(failures, err)
		}
	}
	if err := errors.Join(failures...); err != nil {
		return err
	}
	return advanceStripeVIPInvoiceScan(ctx, v, next)
}

func (s *service) syncStripeVIPInvoice(ctx context.Context, adapter payment.StripeVIPAdapter, v stripeVIPIntent, id, subscription string) error {
	proof, err := adapter.ReadStripeVIPInvoice(ctx, id, v.Offer)
	if err != nil {
		return err
	}
	if proof.ID != id || proof.Subscription.ID != subscription || proof.Subscription.IntentID != v.ID {
		return ErrCheckoutUnavailable
	}
	var movements []payment.LedgerEvidence
	for _, item := range proof.Allocations {
		receipt := item.Receipt
		if receipt.MovementID == "" {
			continue
		}
		entry, err := adapter.GetLedgerMovement(ctx, receipt.MovementID)
		if err != nil {
			log.Printf("task=stripe_vip action=ledger invoice=%s error=%v", id, err)
			continue
		}
		if entry.Currency == "USD" && entry.Kind == payment.MovementPayment &&
			entry.Amounts[payment.LedgerGross].Minor == receipt.AmountMinor {
			entry.References = append(entry.References,
				payment.LedgerReference{Type: "stripe_charge", ID: receipt.ID},
				payment.LedgerReference{Type: "stripe_invoice", ID: id})
			movements = append(movements, entry)
		}
	}
	return settleStripeVIPInvoice(ctx, v, proof, movements)
}

func (s *service) reconcileStripeVIP(ctx context.Context) {
	adapter, available := s.stripeVIPAdapter()
	if !available {
		return
	}
	cancellations, err := claimStripeVIPCancellations(ctx, adapter.AccountScope())
	if err != nil {
		log.Printf("task=stripe_vip action=claim_cancellations error=%v", err)
	}
	for _, job := range cancellations {
		request, cancel := context.WithTimeout(ctx, stripeVIPRequestTimeout)
		err := s.syncStripeVIPCancellation(request, adapter, job)
		cancel()
		if err != nil {
			log.Printf("task=stripe_vip action=cancel intent=%s error=%v", job.intentID, err)
		}
	}
	due, err := claimStripeVIP(ctx, adapter.AccountScope(), "", !s.historicalStripe && stripeVIPCheckoutEnabled(ctx))
	if err != nil {
		log.Printf("task=stripe_vip action=claim error=%v", err)
	}
	for _, v := range due {
		request, cancel := context.WithTimeout(ctx, stripeVIPRequestTimeout)
		err := s.syncStripeVIP(request, adapter, v, "")
		cancel()
		if err != nil {
			log.Printf("task=stripe_vip action=sync intent=%s error=%v", v.ID, err)
		}
	}
}

func (s *service) syncStripeVIPCancellation(ctx context.Context, adapter payment.StripeVIPAdapter, job stripeVIPCancellation) error {
	v, err := findStripeVIPIntent(ctx, db.Pool(ctx), adapter.AccountScope(), job.intentID)
	if err != nil {
		return err
	}
	proof, err := adapter.ReadStripeVIPSubscription(ctx, v.SubscriptionID)
	if err != nil {
		return err
	}
	if proof.ID != v.SubscriptionID || proof.IntentID != v.ID || proof.CustomerID != v.CustomerID {
		return ErrCheckoutUnavailable
	}
	if stripeLocalAgreementStatus(proof) != "CANCELLED" && job.maySend {
		proof, err = adapter.ScheduleStripeVIPCancellation(ctx, proof.ID, job.key)
		if err != nil {
			return recordStripeWriteFailure(ctx, err, `UPDATE payment_stripe_subscription_intents SET cancel_lookup_only=true
				WHERE id=$1 AND account_id=$2 AND cancel_key=$3 AND cancelled_at IS NULL`, v.ID, v.AccountID, job.key)
		}
	}
	return bindStripeVIP(ctx, v, proof)
}

func (s *service) handleStripeVIPNotice(ctx context.Context, job stripeNoticeJob) error {
	adapter, available := s.stripeVIPAdapter()
	if !available {
		return ErrCheckoutPaused
	}
	if job.vipIntentID != "" {
		v, err := findStripeVIPIntent(ctx, db.Pool(ctx), adapter.AccountScope(), job.vipIntentID)
		if err != nil {
			return err
		}
		if v.Status == "closed" {
			return nil
		}
		return s.syncStripeVIP(ctx, adapter, v, job.sessionID)
	}
	return s.syncStripeVIPSubscription(ctx, adapter, job.subscriptionID, job.invoiceID)
}
