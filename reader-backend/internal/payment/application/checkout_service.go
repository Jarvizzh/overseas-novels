package application

import (
	"context"
	"errors"
	"log"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"reader-backend/internal/config"
	"reader-backend/internal/db"
	"reader-backend/internal/model"
	"reader-backend/internal/payment"
	"reader-backend/internal/payment/aftercare"
	"reader-backend/internal/payment/paypal"
)

func (s *service) PreparePurchase(ctx context.Context, userID int64, slotID int, novelID int64, requestKey, returnPath string, attribution checkoutAttribution) (*PurchaseIntent, error) {
	key, err := uuid.Parse(requestKey)
	if err != nil || key == uuid.Nil {
		return nil, ErrCheckoutUnavailable
	}
	returnPath = checkoutReadingPath(returnPath, novelID)
	return s.repo.preparePurchase(ctx, userID, purchaseRequest{key: key, slotID: slotID, novelID: novelID, returnPath: returnPath}, attribution)
}

func checkoutReadingPath(raw string, novelID int64) string {
	var requested, chapter int64
	if path, err := url.ParseRequestURI(raw); err == nil && path.Path == "/content" && path.Host == "" && !path.IsAbs() && path.Fragment == "" {
		requested, _ = strconv.ParseInt(path.Query().Get("novel_id"), 10, 64)
		chapter, _ = strconv.ParseInt(path.Query().Get("chapter_index"), 10, 64)
	}
	if novelID <= 0 {
		novelID = requested
	}
	if novelID <= 0 {
		return ""
	}
	values := url.Values{"novel_id": {strconv.FormatInt(novelID, 10)}}
	if requested == novelID && chapter > 0 {
		values.Set("chapter_index", strconv.FormatInt(chapter, 10))
	}
	return "/content?" + values.Encode()
}

func (s *service) GetPurchase(ctx context.Context, userID int64, intentID, paypalOrderID string) (*PurchaseIntent, error) {
	if intentID != "" {
		id, err := uuid.Parse(intentID)
		if err != nil || id == uuid.Nil {
			return nil, ErrCheckoutUnavailable
		}
		intentID = id.String()
	} else if paypalOrderID == "" {
		return nil, ErrCheckoutUnavailable
	}
	return s.repo.purchaseByReference(ctx, userID, intentID, paypalOrderID)
}

func (s *service) ListRecentPurchases(ctx context.Context, userID int64) ([]PurchaseIntent, error) {
	purchases, err := s.repo.recentPurchases(ctx, userID)
	if err != nil {
		return nil, err
	}
	paypalVIP, err := s.repo.recentPayPalVIPPurchases(ctx, userID)
	if err != nil {
		return nil, err
	}
	purchases = append(purchases, paypalVIP...)
	visited := make(map[payment.WebhookAccount]bool)
	for _, adapter := range payment.AccountCapabilities[payment.StripeVIPAdapter](s.adapters, payment.ProviderStripe) {
		account := adapter.AccountScope()
		if visited[account] || !paymentEnvironmentMatches(account) {
			continue
		}
		visited[account] = true
		stripeVIP, err := recentStripeVIPPurchases(ctx, userID, account)
		if err != nil {
			return nil, err
		}
		purchases = append(purchases, stripeVIP...)
	}
	return purchases, nil
}

func (s *service) GetPaymentMethods(ctx context.Context) (PaymentMethods, error) {
	if s.paypalClient == nil || !s.paypalClient.WebhookConfigured(ctx) {
		return PaymentMethods{}, nil
	}
	account, err := s.paypalClient.CheckoutAccount(ctx)
	if err != nil || !paymentEnvironmentMatches(account) {
		return PaymentMethods{}, nil
	}
	return s.repo.payPalAvailability(ctx, account)
}

func (s *service) GetVIPPurchase(ctx context.Context, userID int64, intentID, externalID string) (*PurchaseIntent, error) {
	if intentID != "" {
		id, err := uuid.Parse(intentID)
		if err != nil || id == uuid.Nil {
			return nil, ErrCheckoutUnavailable
		}
		intentID = id.String()
	} else if externalID == "" {
		return nil, ErrCheckoutUnavailable
	}
	return s.repo.getVIPPurchase(ctx, userID, intentID, externalID)
}

func (s *service) StartPayPalCheckout(ctx context.Context, userID int64, intentID, origin string) (*PurchaseIntent, error) {
	intent, err := s.GetPurchase(ctx, userID, intentID, "")
	if err != nil {
		return nil, err
	}
	if intent.Status != "ready" && intent.Status != "retryable" && intent.Status != "confirming" && intent.Status != "awaiting_approval" {
		return intent, nil
	}
	site, err := s.checkoutSite(ctx, origin)
	if err != nil {
		return nil, err
	}
	if intent.Provider == payment.ProviderStripe && (intent.Status == "confirming" || intent.Status == "awaiting_approval") {
		methods, err := s.GetPaymentMethods(ctx)
		if err != nil {
			return nil, err
		}
		if !methods.PayPalSingle {
			return nil, ErrCheckoutPaused
		}
		if err := s.closeStripeForCheckoutSwitch(ctx, userID, intent); err != nil {
			return nil, err
		}
		intent, err = s.GetPurchase(ctx, userID, intentID, "")
		if err != nil {
			return nil, err
		}
	}
	if intent.Status != "ready" && intent.Status != "retryable" && intent.Status != "confirming" && intent.Status != "awaiting_approval" {
		return intent, nil
	}
	site.Path = "/recharge"
	site.RawQuery = url.Values{"purchase": {intent.ID}, "payment": {"return"}}.Encode()
	returnURL := site.String()
	site.RawQuery = url.Values{"purchase": {intent.ID}, "payment": {"cancel"}}.Encode()
	_, _, err = s.startPayPalAttempt(ctx, userID, intent.ID, returnURL, site.String())
	if err != nil && !errors.Is(err, ErrCheckoutPending) {
		return nil, err
	}
	return s.GetPurchase(ctx, userID, intent.ID, "")
}

func (s *service) checkoutSite(ctx context.Context, origin string) (*url.URL, error) {
	if origin == "" && config.AppConfig != nil {
		origin = config.AppConfig.DefaultDomain
	}
	site, err := url.Parse(origin)
	if err != nil || site.User != nil || site.Host == "" || site.RawQuery != "" || site.Fragment != "" ||
		(site.Path != "" && site.Path != "/") {
		return nil, ErrCheckoutUnavailable
	}
	configured := false
	if config.AppConfig != nil {
		canonical, err := url.Parse(config.AppConfig.DefaultDomain)
		configured = err == nil && canonical.Scheme == site.Scheme && strings.EqualFold(canonical.Host, site.Host)
	}
	if configured {
		return site, nil
	}
	if site.Scheme != "https" || site.Port() != "" {
		return nil, ErrCheckoutUnavailable
	}
	allowed, err := s.repo.isCheckoutOrigin(ctx, site.Hostname())
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, ErrCheckoutUnavailable
	}
	return site, nil
}

func (s *service) createLegacyPayPalOrder(ctx context.Context, userID int64, slotID int, novelID int64, returnURL, cancelURL string, attribution checkoutAttribution) (string, string, error) {
	if err := s.validateCheckoutRedirects(ctx, returnURL, cancelURL); err != nil {
		return "", "", err
	}
	intent, err := s.repo.preparePurchase(ctx, userID, purchaseRequest{key: uuid.New(), slotID: slotID, novelID: novelID}, attribution)
	if err != nil {
		return "", "", err
	}
	return s.startPayPalAttempt(ctx, userID, intent.ID, returnURL, cancelURL)
}

func (s *service) validateCheckoutRedirects(ctx context.Context, redirects ...string) error {
	for _, raw := range redirects {
		if raw == "" {
			continue
		}
		target, err := url.Parse(raw)
		if err != nil || target.User != nil || target.Path != "/recharge" || target.Fragment != "" {
			return ErrCheckoutUnavailable
		}
		base := url.URL{Scheme: target.Scheme, Host: target.Host}
		if _, err := s.checkoutSite(ctx, base.String()); err != nil {
			return err
		}
	}
	return nil
}

func (s *service) startPayPalAttempt(ctx context.Context, userID int64, intentID, returnURL, cancelURL string) (string, string, error) {
	client, err := s.payPalForNewCheckout(ctx, userID, intentID, 0)
	if err != nil {
		return "", "", err
	}
	if client == nil || !client.WebhookConfigured(ctx) {
		return "", "", errors.New("PayPal webhook is not configured")
	}
	account, err := client.CheckoutAccount(ctx)
	if err != nil {
		return "", "", err
	}
	if !paymentEnvironmentMatches(account) {
		return "", "", ErrCheckoutPaused
	}
	attempt, err := s.repo.reservePayPalAttempt(ctx, userID, intentID, returnURL, cancelURL, account)
	if err != nil {
		return "", "", err
	}
	if attempt.externalID != "" {
		return attempt.externalID, attempt.approvalURL, nil
	}
	if !attempt.send {
		return "", "", ErrCheckoutPending
	}
	return s.sendPayPalCheckout(ctx, attempt)
}

func (s *service) sendPayPalCheckout(ctx context.Context, attempt *paypalCheckoutAttempt) (string, string, error) {
	if !paymentEnvironmentMatches(attempt.account) {
		return "", "", ErrCheckoutPaused
	}
	client := s.paypalClient
	if s.adapters.HasRegisteredAccounts(payment.ProviderPayPal) {
		var err error
		client, err = s.payPalForScope(ctx, attempt.account)
		if err != nil {
			return "", "", err
		}
	}
	id, approval, err := client.CreateOrder(ctx, attempt.account, attempt.amount, attempt.intentID, attempt.id,
		"Star Novel "+attempt.orderType, attempt.returnURL, attempt.cancelURL)
	if errors.Is(err, paypal.ErrOrderLookupOnly) {
		if err := s.repo.holdCheckoutCreate(context.WithoutCancel(ctx), attempt.id); err != nil {
			return "", "", err
		}
	}
	if err != nil || id == "" {
		return "", "", ErrCheckoutPending
	}
	if err := s.repo.finishPayPalAttempt(ctx, attempt, id, approval); err != nil {
		return "", "", err
	}
	return id, approval, nil
}

func (s *service) CreatePayPalOrder(ctx context.Context, userID int64, slotID int, novelID int64, returnURL, cancelURL string, attribution checkoutAttribution) (string, string, error) {
	return s.createLegacyPayPalOrder(ctx, userID, slotID, novelID, returnURL, cancelURL, attribution)
}

func (s *service) checkoutSlot(ctx context.Context, userID int64, slotID int) (*model.RechargeSlot, error) {
	templateID, err := s.repo.GetCheckoutTemplate(ctx, userID)
	if err != nil {
		return nil, err
	}
	slot, err := s.repo.GetRechargeSlotByID(ctx, slotID)
	if err != nil {
		return nil, err
	}
	if slot == nil || slot.TemplateID != templateID || slot.PriceCents <= 0 || slot.Coins < 0 || slot.Bonus < 0 {
		return nil, ErrCheckoutUnavailable
	}
	return slot, nil
}

func (s *service) CapturePayPalPayment(ctx context.Context, userID int64, orderID string) (*CheckoutResult, error) {
	return s.completePayPalOrder(ctx, userID, orderID, true)
}

func (s *service) completePayPalOrder(ctx context.Context, userID int64, orderID string, allowCapture bool) (*CheckoutResult, error) {
	order, err := s.repo.findCheckout(ctx, orderID)
	if err != nil {
		return nil, err
	}
	if userID != 0 && order.userID != userID {
		return nil, ErrCheckoutUnavailable
	}
	if order.status != "Refunded" && order.orderType == "whole_book" && (order.novelID <= 0 || (order.checkoutID == "" && !order.legacyBookProven)) {
		if err := s.repo.flagCheckoutForReview(ctx, order.id, "Independent historical whole-book purchase evidence is required"); err != nil {
			return nil, err
		}
		return nil, ErrCheckoutUnavailable
	}
	switch order.status {
	case "Success", "Paid":
		if !order.deferred && !order.needsHistoricalBookVerification() && (!order.managedAttempt || order.providerSettled) {
			return &CheckoutResult{Coins: order.coins + order.bonus, OrderType: order.orderType}, nil
		}
	case "Pending":
	case "Refunded":
		if !order.managedAttempt || order.providerSettled {
			return nil, ErrPaymentRefunded
		}
	case "RefundPending":
		if !order.managedAttempt || !order.fulfilled || order.providerSettled {
			return nil, ErrCheckoutUnavailable
		}
	default:
		return nil, ErrCheckoutUnavailable
	}
	client, err := s.payPalForOrder(ctx, orderID)
	if err != nil || client == nil {
		return nil, errors.Join(ErrCheckoutUnavailable, err)
	}
	if order.managedAttempt {
		scope, err := client.CheckoutAccount(ctx)
		if err != nil {
			return nil, err
		}
		matched, err := s.repo.matchPayPalAttempt(ctx, orderID, scope)
		if err != nil {
			return nil, err
		}
		if !matched {
			return nil, ErrCheckoutUnavailable
		}
	}
	capture, err := client.GetCapture(ctx, orderID)
	if errors.Is(err, paypal.ErrOrderClosed) && order.managedAttempt && order.matchesPayPalOrder(capture, orderID) {
		matched, err := s.repo.matchPayPalAttempt(ctx, orderID, capture.Account)
		if err != nil {
			return nil, err
		}
		if !matched {
			return nil, ErrCheckoutUnavailable
		}
		if err := s.repo.closePayPalAttempt(ctx, orderID); err != nil {
			return nil, err
		}
		return nil, ErrCheckoutPending
	}
	if errors.Is(err, paypal.ErrOrderApproved) && allowCapture && order.status == "Pending" {
		if !order.matchesPayPalOrder(capture, orderID) {
			if err := s.repo.flagCheckoutForReview(ctx, order.id, "PayPal approval does not match order snapshot"); err != nil {
				return nil, err
			}
			return nil, ErrCheckoutUnavailable
		}
		if !paymentEnvironmentMatches(capture.Account) {
			return nil, ErrCheckoutPaused
		}
		allowed, leaseErr := s.repo.leaseCheckoutCapture(ctx, orderID)
		if leaseErr != nil {
			return nil, leaseErr
		}
		if !allowed {
			return nil, ErrCheckoutPending
		}
		captureAccount := capture.Account
		capture, err = client.CaptureOrder(ctx, orderID, captureAccount)
		if err != nil {
			captureErr := err
			if errors.Is(err, paypal.ErrOrderLookupOnly) {
				if holdErr := s.repo.holdCheckoutCapture(context.WithoutCancel(ctx), orderID); holdErr != nil {
					return nil, holdErr
				}
			}
			if order.managedAttempt {
				fact, factErr := paypal.FailureRisk(captureAccount, orderID, captureErr, time.Now())
				if factErr != nil {
					return nil, factErr
				}
				if fact != nil {
					if saveErr := aftercare.NewRepository(db.Pool(ctx)).Record(ctx, *fact); saveErr != nil {
						log.Printf("task=aftercare action=capture_evidence order=%d error=%v", order.id, saveErr)
					}
				}
			}

			capture, err = client.GetCapture(ctx, orderID)
			if errors.Is(err, paypal.ErrOrderApproved) && (errors.Is(captureErr, payment.ErrCheckoutDeclined) ||
				errors.Is(captureErr, payment.ErrCheckoutRiskBlocked) || errors.Is(captureErr, payment.ErrCheckoutVerificationRequired)) {
				err = captureErr
			}
		}
	}
	if err != nil {
		if order.managedAttempt {
			if saveErr := s.repo.recordCaptureOutcome(ctx, order.id, orderID, captureOutcomeFor(err)); saveErr != nil {
				return nil, errors.Join(err, saveErr)
			}
		}
		if order.needsHistoricalBookVerification() {
			if markErr := s.repo.flagCheckoutForReview(ctx, order.id, "Historical PayPal receipt could not be confirmed"); markErr != nil {
				return nil, markErr
			}
		}
		return nil, err
	}
	result, err := s.repo.settleCheckout(ctx, orderID, capture)
	if errors.Is(err, ErrCheckoutUnavailable) || errors.Is(err, errRefundUnmatched) {
		if markErr := s.repo.flagCheckoutForReview(ctx, order.id, "PayPal payment or refund requires verification"); markErr != nil {
			return nil, markErr
		}
	}
	return result, err
}

func (s *service) GetRechargeTemplates(ctx context.Context, userID int64, novelID int64) (*model.RechargeTemplate, error) {
	templateID, err := s.repo.GetCheckoutTemplate(ctx, userID)
	if err != nil {
		return nil, err
	}
	if templateID == 0 {
		return &model.RechargeTemplate{Slots: []model.RechargeSlot{}}, nil
	}
	t, err := s.repo.GetRechargeTemplateByID(ctx, templateID)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, ErrCheckoutUnavailable
	}

	slots, err := s.repo.GetRechargeSlots(ctx, t.ID)
	if err != nil {
		return nil, err
	}
	canBuyBook, err := s.repo.WholeBookAvailable(ctx, userID, templateID, novelID)
	if err != nil {
		return nil, err
	}
	t.Slots = make([]model.RechargeSlot, 0, len(slots))
	for _, slot := range slots {
		if slot.Type != "whole_book" || canBuyBook {
			t.Slots = append(t.Slots, slot)
		}
	}

	return t, nil
}
