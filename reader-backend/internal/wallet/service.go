package wallet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"reader-backend/internal/db"
	"reader-backend/internal/model"
	"reader-backend/internal/novel"
	"reader-backend/internal/payment"
	"reader-backend/internal/tracking"
	"reader-backend/internal/workerpool"
)

var (
	ErrPriceChanged        = errors.New("Chapter or price changed. Refresh before confirming.")
	ErrStripeDisabled      = errors.New("Stripe payments are disabled")
	ErrChapterNotFound     = errors.New("chapter not found")
	ErrPurchaseNeedsReview = errors.New("An older chapter purchase for this book needs review. Contact support before buying another chapter.")
)

type Service interface {
	RunReconciliation(context.Context)
	GetWallet(ctx context.Context, userID int64) (*model.Wallet, error)
	GetHistory(ctx context.Context, userID int64, limit, offset int) ([]model.Transaction, error)
	GetHistoryPage(ctx context.Context, userID int64, limit int, after *historyCursor) (*HistoryPage, error)
	UnlockChapter(ctx context.Context, userID int64, novelID int64, chapterIndex int, promoID int, utmSource, utmCampaign string, quoted ChapterQuote) error
	InitiateCheckout(ctx context.Context, userID int64, amountCents int64, coinsAmount int, fbp, fbc, pixelID, ip, ua, sourceURL, country string) error
	CreateStripeIntent(ctx context.Context, userID int64, amountCents int64, coinsAmount int, fbp, fbc, pixelID, ip, ua, sourceURL, country string) (string, error)
	CreatePayPalOrder(context.Context, int64, int, int64, string, string, checkoutAttribution) (string, string, error)
	CapturePayPalPayment(context.Context, int64, string) (*CheckoutResult, error)
	CreateSubscription(ctx context.Context, userID int64, providerName string, slotID int, returnURL, cancelURL, fbp, fbc, pixelID, ip, ua, sourceURL, country string) (*payment.SubscriptionResult, error)
	ActivateSubscription(ctx context.Context, userID int64, providerName string, subscriptionID string, fbp, fbc, pixelID, ip, ua, sourceURL, country string) error
	HasSubscriptionAccess(context.Context, int64) (bool, error)
	GetManagedSubscription(ctx context.Context, userID int64) (*model.UserSubscription, error)
	ListManagedSubscriptions(context.Context, int64) ([]model.UserSubscription, error)
	CancelSubscription(ctx context.Context, userID int64, subscriptionID string, reason string) error
	ProcessSubscriptionWebhook(ctx context.Context, providerName string, payload []byte, headers map[string]string) error
	ProcessStripeWebhook(ctx context.Context, payload []byte, sigHeader string, webhookSecret string) error
	CheckIn(context.Context, int64, bool) (*CheckInState, error)
	GetRechargeTemplates(ctx context.Context, userID int64, novelID int64) (*model.RechargeTemplate, error)
}

type service struct {
	repo         Repository
	novelRepo    novel.Repository
	stripeClient *payment.StripeClient
	paypalClient *payment.PayPalClient
}

func NewService(repo Repository, novelRepo novel.Repository, stripeClient *payment.StripeClient, paypalClient *payment.PayPalClient) Service {
	return &service{
		repo:         repo,
		novelRepo:    novelRepo,
		stripeClient: stripeClient,
		paypalClient: paypalClient,
	}
}

func (s *service) GetWallet(ctx context.Context, userID int64) (*model.Wallet, error) {
	w, err := s.repo.GetWallet(ctx, userID)
	if err != nil {
		return nil, err
	}
	if w == nil {
		err = s.repo.AddCoins(ctx, userID, 0, false, "recharge", "Wallet Initialized")
		if err != nil {
			return nil, err
		}
		return s.repo.GetWallet(ctx, userID)
	}
	return w, nil
}

func (s *service) GetHistory(ctx context.Context, userID int64, limit, offset int) ([]model.Transaction, error) {
	return s.repo.GetTransactions(ctx, userID, limit, offset)
}

type ChapterQuote struct {
	ID      string
	Version int64
	Price   int
}

func (s *service) UnlockChapter(ctx context.Context, userID int64, novelID int64, chapterIndex int, promoID int, utmSource, utmCampaign string, quoted ChapterQuote) error {
	n, err := s.novelRepo.GetByID(ctx, novelID)
	if err != nil {
		return err
	}
	if !novel.IsNovelPublished(n) {
		return novel.ErrNovelNotFound
	}

	ch, err := s.novelRepo.GetChapter(ctx, novelID, chapterIndex)
	if err != nil {
		return err
	}
	if ch == nil {
		return ErrChapterNotFound
	}

	effectiveStartPay, effectiveCost, err := s.novelRepo.GetEffectivePricingRule(ctx, userID, novelID, promoID, utmSource, utmCampaign)
	if err != nil {
		return err
	}

	price := 0
	if chapterIndex >= effectiveStartPay-1 {
		price = novel.CalculateChapterPrice(ch.WordCount, effectiveCost)
	}
	quote := ChapterQuote{ID: ch.ID, Version: n.ContentVersion, Price: price}
	if quote.ID == "" || quoted != quote {
		return ErrPriceChanged
	}
	if effectiveCost == 0 || chapterIndex < effectiveStartPay-1 {
		return nil
	}
	hasPass, err := novel.HasReadingPass(ctx, s.novelRepo, userID, novelID)
	if err != nil || hasPass {
		return err
	}
	if err := s.repo.UnlockChapterTx(ctx, userID, novelID, chapterIndex, promoID, quote); err != nil {
		return err
	}
	return nil
}

func (s *service) InitiateCheckout(ctx context.Context, userID int64, amountCents int64, coinsAmount int, fbp, fbc, pixelID, ip, ua, sourceURL, country string) error {
	email, _ := s.repo.GetUserEmail(ctx, userID)

	// Trigger FB Conversions API InitiateCheckout event asynchronously via WorkerPool strictly using CMS configured pixel
	workerpool.Submit(func() {
		taskCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		effectivePixelID := ""
		if effectivePixelID == "" && db.DB != nil {
			_ = db.Pool(db.BackgroundContext(taskCtx)).QueryRow(taskCtx, "SELECT fp.pixel_id FROM reader_promotion_for_user($1) pl JOIN fb_pixels fp ON pl.fb_pixel_id = fp.id", userID).Scan(&effectivePixelID)
		}
		if effectivePixelID != "" {
			tracking.SendFacebookEvent(effectivePixelID, "InitiateCheckout", strconv.FormatInt(userID, 10), email, ip, ua, fbc, fbp, float64(amountCents)/100.0, "USD", sourceURL, country)
		}
	})

	return nil
}

func (s *service) CreateStripeIntent(ctx context.Context, userID int64, amountCents int64, coinsAmount int, fbp, fbc, pixelID, ip, ua, sourceURL, country string) (string, error) {
	// TODO(stripe): implement server-priced orders and authenticated, idempotent settlement.
	return "", ErrStripeDisabled
}

func (s *service) CreatePayPalOrder(ctx context.Context, userID int64, slotID int, novelID int64, returnURL, cancelURL string, attribution checkoutAttribution) (string, string, error) {
	slot, err := s.checkoutSlot(ctx, userID, slotID)
	if err != nil {
		return "", "", err
	}
	switch slot.Type {
	case "single":
		novelID = 0
	case "whole_book":
		book, err := s.novelRepo.GetByID(ctx, novelID)
		if err != nil {
			return "", "", err
		}
		if !novel.IsNovelPublished(book) {
			return "", "", ErrCheckoutUnavailable
		}
		// Whole book purchases grant reading access. Only explicitly configured bonus coins are issued.
		slot.Coins = 0
	default:
		return "", "", ErrCheckoutUnavailable
	}
	if !s.paypalClient.WebhookConfigured() {
		return "", "", errors.New("PayPal webhook is not configured")
	}

	checkoutID, err := s.repo.CreateCheckout(ctx, userID, slot, novelID, attribution)
	if err != nil {
		return "", "", err
	}
	orderID, approvalURL, err := s.paypalClient.CreateOrder(ctx, int64(slot.PriceCents), checkoutID, "Star Novel "+slot.Type, returnURL, cancelURL)
	if err != nil {
		return "", "", err
	}
	if orderID == "" {
		return "", "", errors.New("missing PayPal order ID")
	}
	if err := s.repo.BindCheckout(ctx, checkoutID, orderID); err != nil {
		return "", "", err
	}
	s.trackCheckoutInitiation(orderID)
	return orderID, approvalURL, nil
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
		if !order.needsHistoricalBookVerification() {
			return &CheckoutResult{Coins: order.coins + order.bonus, OrderType: order.orderType}, nil
		}
	case "Pending":
	case "Refunded":
		return nil, ErrPaymentRefunded
	default:
		return nil, ErrCheckoutUnavailable
	}
	capture, err := s.paypalClient.GetCapture(ctx, orderID)
	if errors.Is(err, payment.ErrPayPalApproved) && allowCapture && order.status == "Pending" {
		if !order.matchesPayPalOrder(capture, orderID) {
			if err := s.repo.flagCheckoutForReview(ctx, order.id, "PayPal approval does not match order snapshot"); err != nil {
				return nil, err
			}
			return nil, ErrCheckoutUnavailable
		}
		capture, err = s.paypalClient.CaptureOrder(ctx, orderID)
		if err != nil {
			capture, err = s.paypalClient.GetCapture(ctx, orderID)
		}
	}
	if err != nil {
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

func (s *service) trackCheckoutInitiation(orderID string) {
	workerpool.Submit(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		var userID int64
		var amount int64
		var pixel, currency, email string
		var metadata []byte
		err := db.Pool(ctx).QueryRow(ctx, `SELECT o.user_id,o.amount_cents,o.currency,
			COALESCE(u.email,''),fp.pixel_id,COALESCE(o.fb_lead_metadata,'{}')
			FROM recharge_orders o JOIN users u ON u.id=o.user_id
			JOIN promotion_links p ON p.id=o.promotion_link_id JOIN fb_pixels fp ON fp.id=p.fb_pixel_id
			WHERE o.external_ref_id=$1 AND o.payment_method='paypal'`, orderID).
			Scan(&userID, &amount, &currency, &email, &pixel, &metadata)
		if err != nil {
			return
		}
		var attribution checkoutAttribution
		if err := json.Unmarshal(metadata, &attribution); err != nil {
			return
		}
		tracking.SendFacebookEvent(pixel, "InitiateCheckout", strconv.FormatInt(userID, 10), email, attribution.IP, attribution.UserAgent,
			attribution.FBC, attribution.FBP, float64(amount)/100, currency, attribution.SourceURL, attribution.Country)
	})
}

func (s *service) ProcessStripeWebhook(ctx context.Context, payload []byte, sigHeader string, webhookSecret string) error {
	// TODO(stripe): verify the signature and reconcile the actual payment with a local order.
	return ErrStripeDisabled
}

func (s *service) CheckIn(ctx context.Context, userID int64, claim bool) (*CheckInState, error) {
	return s.repo.CheckIn(ctx, userID, claim)
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

func (s *service) CreateSubscription(
	ctx context.Context,
	userID int64,
	providerName string,
	slotID int,
	returnURL, cancelURL string,
	fbp, fbc, pixelID, ip, ua, sourceURL, country string,
) (*payment.SubscriptionResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if providerName == "" {
		providerName = payment.ProviderPayPal
	}

	provider, err := payment.GetSubscriptionProvider(providerName)
	if err != nil {
		return nil, err
	}

	if providerName == payment.ProviderPayPal {
		if err := s.paypalClient.ValidateIdentity(ctx); err != nil {
			return nil, err
		}
	}
	if providerName == payment.ProviderPayPal && !s.paypalClient.WebhookConfigured() {
		return nil, errors.New("PayPal webhook is not configured")
	}

	slot, err := s.checkoutSlot(ctx, userID, slotID)
	if err != nil {
		return nil, err
	}

	if slot.Type != "subscription" && slot.Type != "vip" {
		return nil, errors.New("selected recharge slot is not a subscription package")
	}

	cycleStr := slot.SubscriptionCycle
	if cycleStr == "" {
		cycleStr = slot.VipDuration
	}
	if cycleStr == "" {
		cycleStr = "month"
	}

	switch payment.SubscriptionCycle(cycleStr) {
	case payment.CycleDay, payment.CycleWeek, payment.CycleMonth:
	default:
		return nil, ErrCheckoutUnavailable
	}
	intent, err := s.repo.reserveSubscriptionIntent(ctx, subscriptionIntent{userID: userID, slotID: slot.ID,
		templateID: slot.TemplateID, priceCents: slot.PriceCents, cycle: cycleStr, returnURL: returnURL, cancelURL: cancelURL,
		attribution: checkoutAttribution{FBP: fbp, FBC: fbc, IP: ip, UserAgent: ua, SourceURL: sourceURL, Country: country}})
	if err != nil {
		return nil, err
	}
	if intent.approvalURL != "" {
		return &payment.SubscriptionResult{SubscriptionID: intent.subscriptionID, ApproveURL: intent.approvalURL, Status: "APPROVAL_PENDING"}, nil
	}
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		s.repo.releaseSubscriptionIntent(releaseCtx, intent.id)
	}()
	if intent.planID == "" {
		existingPlan, err := s.repo.GetProviderPlan(ctx, providerName, intent.slotID)
		if err != nil {
			return nil, err
		}
		var planID string
		if existingPlan != nil && existingPlan.ExternalPlanID != "" && existingPlan.PriceCents == intent.priceCents && existingPlan.Cycle == intent.cycle && existingPlan.Currency == "USD" {
			planID = existingPlan.ExternalPlanID
		} else {
			planName := slot.VipName
			if planName == "" {
				planName = "Star Novel VIP " + strings.Title(intent.cycle)
			}
			desc := slot.VipDesc
			if desc == "" {
				desc = "Unlimited reading access for VIP members"
			}
			newPlanID, errPlan := provider.CreatePlan(ctx, payment.CreateSubscriptionPlanParam{
				PlanName:    planName,
				Description: desc,
				Cycle:       payment.SubscriptionCycle(intent.cycle),
				PriceCents:  int64(intent.priceCents),
				Currency:    "USD",
			})
			if errPlan != nil {
				return nil, fmt.Errorf("failed to create subscription plan with %s: %w", providerName, errPlan)
			}
			planID = newPlanID
			err = s.repo.SaveProviderPlan(ctx, &model.PaymentProviderPlan{
				Provider:       providerName,
				SlotID:         intent.slotID,
				Cycle:          intent.cycle,
				PriceCents:     intent.priceCents,
				Currency:       "USD",
				ExternalPlanID: planID,
				Status:         "ACTIVE",
			})
			if err != nil {
				return nil, err
			}
		}

		intent.planID = planID
	}
	if err := s.repo.submitSubscriptionIntent(ctx, intent.id, intent.planID); err != nil {
		return nil, err
	}
	result, err := provider.CreateSubscription(ctx, payment.CreateSubscriptionParam{
		RequestID: intent.id, UserID: userID, PlanID: intent.planID, SlotID: intent.slotID, PriceCents: int64(intent.priceCents),
		Currency: "USD", CustomID: intent.id, ReturnURL: intent.returnURL, CancelURL: intent.cancelURL,
	})
	if err != nil {
		return nil, err
	}
	if err := s.repo.finishSubscriptionIntent(ctx, intent, result); err != nil {
		return nil, err
	}

	email, _ := s.repo.GetUserEmail(ctx, userID)
	workerpool.Submit(func() {
		taskCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		effectivePixelID := ""
		if effectivePixelID == "" && db.DB != nil {
			_ = db.Pool(db.BackgroundContext(taskCtx)).QueryRow(taskCtx, "SELECT fp.pixel_id FROM reader_promotion_for_user($1) pl JOIN fb_pixels fp ON pl.fb_pixel_id = fp.id", userID).Scan(&effectivePixelID)
		}
		if effectivePixelID != "" {
			tracking.SendFacebookEvent(effectivePixelID, "InitiateCheckout", strconv.FormatInt(userID, 10), email, ip, ua, fbc, fbp, float64(slot.PriceCents)/100.0, "USD", sourceURL, country)
		}
	})

	return result, nil
}

func (s *service) ActivateSubscription(
	ctx context.Context,
	userID int64,
	providerName string,
	subscriptionID string,
	fbp, fbc, pixelID, ip, ua, sourceURL, country string,
) error {
	if providerName == "" {
		providerName = payment.ProviderPayPal
	}
	sub, err := s.repo.GetSubscriptionByID(ctx, subscriptionID)
	if err != nil {
		return err
	}
	if sub == nil || sub.UserID != userID || sub.PaymentMethod != providerName {
		return ErrCheckoutUnavailable
	}
	provider, err := payment.GetSubscriptionProvider(providerName)
	if err != nil {
		return err
	}
	details, err := provider.GetSubscription(ctx, subscriptionID)
	if err != nil {
		return err
	}
	if details.PlanID != sub.PlanID || details.SubscriptionID != subscriptionID {
		return ErrCheckoutUnavailable
	}
	switch details.Status {
	case "APPROVED":
		return provider.ActivateSubscription(ctx, subscriptionID)
	case "ACTIVE":
		if details.LastPaymentTime == nil {
			return nil
		}
		end := time.Now().UTC().Truncate(time.Second)
		// The recurring reconciliation job retries if the transaction is not
		// visible in PayPal's ledger while the buyer is returning.
		if err := s.importPayPalSubscriptionTransactions(ctx, sub, details, end.Add(-24*time.Hour), end); err != nil {
			return fmt.Errorf("subscription payment confirmation pending: %w", err)
		}
		return nil
	default:
		return errors.New("subscription is not approved")
	}
}

func (s *service) GetManagedSubscription(ctx context.Context, userID int64) (*model.UserSubscription, error) {
	return s.repo.GetManagedSubscriptionByUserID(ctx, userID)
}

func (s *service) CancelSubscription(ctx context.Context, userID int64, subscriptionID string, reason string) error {
	sub, err := s.repo.GetSubscriptionByID(ctx, subscriptionID)
	if err != nil {
		return err
	}
	if sub == nil || sub.UserID != userID || sub.PaymentMethod != payment.ProviderPayPal {
		return ErrCheckoutUnavailable
	}
	return s.repo.enqueuePayPalCancellation(ctx, subscriptionID, userID, reason)
}

func (s *service) ProcessSubscriptionWebhook(ctx context.Context, providerName string, payload []byte, headers map[string]string) error {
	if providerName == "" {
		providerName = payment.ProviderPayPal
	}
	provider, err := payment.GetSubscriptionProvider(providerName)
	if err != nil {
		return err
	}
	event, err := provider.ParseWebhook(ctx, payload, headers)
	if err != nil {
		return err
	}
	if event.EventID == "" {
		return errors.New("missing payment event ID")
	}
	return s.repo.enqueuePayPalEvent(ctx, event)
}

func (s *service) applyPayPalEvent(ctx context.Context, provider payment.SubscriptionProvider, event *payment.WebhookEventResult) error {
	switch event.EventType {
	case "refund_completed":
		return s.repo.CompletePayPalRefund(ctx, event.ExternalRefID, event.CaptureID, event.AmountCents, event.Currency)
	case "order_approved", "capture_completed":
		if event.ExternalRefID == "" {
			return errors.New("missing PayPal order ID")
		}
		_, err := s.completePayPalOrder(ctx, 0, event.ExternalRefID, event.EventType == "order_approved")
		if errors.Is(err, ErrPaymentRefunded) {
			return nil
		}
		if errors.Is(err, ErrCheckoutUnavailable) {
			order, lookupErr := s.repo.findCheckout(ctx, event.ExternalRefID)
			if lookupErr == nil && order.status != "Pending" {
				return nil
			}
		}
		return err
	case "payment_succeeded", "subscription_cancelled", "subscription_suspended", "subscription_expired":
	default:
		return nil
	}
	if event.SubscriptionID == "" {
		return nil
	} // A one-time sale is handled by the capture event.
	sub, err := s.repo.GetSubscriptionByID(ctx, event.SubscriptionID)
	if err != nil {
		return err
	}
	if sub == nil || sub.PaymentMethod != provider.GetProviderName() {
		return ErrCheckoutUnavailable
	}

	details, err := provider.GetSubscription(ctx, sub.SubscriptionID)
	if err != nil {
		return err
	}
	if details.SubscriptionID != sub.SubscriptionID || details.PlanID != sub.PlanID {
		return ErrCheckoutUnavailable
	}

	if event.EventType != "payment_succeeded" {
		switch details.Status {
		case "CANCELLED", "EXPIRED", "SUSPENDED":
			return s.repo.UpdateUserSubscriptionStatus(ctx, sub.SubscriptionID, details.Status)
		default:
			return nil
		}
	}

	// The signed notification identifies the sale. The merchant transaction list
	// confirms the amount and transaction's subscription, including catch-up payments.
	end := time.Now().UTC().Add(time.Second)
	if after := event.PaidAt.Add(24 * time.Hour); after.Before(end) {
		end = after
	}
	transactions, err := s.paypalClient.ListSubscriptionTransactions(ctx, sub.SubscriptionID, event.PaidAt.Add(-24*time.Hour), end)
	if err != nil {
		return err
	}
	for _, transaction := range transactions {
		if transaction.ID == event.ExternalRefID {
			return s.recordPayPalSubscriptionTransaction(ctx, sub, details, transaction)
		}
	}
	return errors.New("PayPal transaction is not visible in the subscription ledger yet")
}

func (s *service) recordPayPalSubscriptionTransaction(ctx context.Context, sub *model.UserSubscription,
	details *payment.SubscriptionDetails, transaction payment.PayPalSubscriptionTransaction) error {
	if transaction.Status != "COMPLETED" && transaction.Status != "REFUNDED" &&
		transaction.Status != "PARTIALLY_REFUNDED" {
		return nil
	}
	amount, err := payment.ParseCents(transaction.Amount.Gross.Value)
	if err != nil || transaction.ID == "" || transaction.Time.IsZero() || transaction.Amount.Gross.Currency != sub.Currency {
		return errors.New("invalid PayPal subscription transaction")
	}
	var periodEnd time.Time
	if transaction.Status == "COMPLETED" && details.LastPaymentTime != nil &&
		transaction.Time.Sub(*details.LastPaymentTime).Abs() <= time.Minute &&
		details.LastPaymentCents == amount && details.LastPaymentCurrency == sub.Currency &&
		amount >= int64(sub.PriceCents) && details.CurrentPeriodEnd != nil && details.CurrentPeriodEnd.After(transaction.Time) {
		periodEnd = *details.CurrentPeriodEnd
	}

	if periodEnd.IsZero() && transaction.Status == "COMPLETED" {
		periodEnd, err = s.repo.getPayPalBillingSnapshot(ctx, sub.SubscriptionID, transaction.Time, sub.Currency, amount)
		if err != nil {
			return err
		}
		if periodEnd.IsZero() && details.Status == "CANCELLED" && details.CurrentPeriodStart != nil &&
			details.LastPaymentTime != nil && transaction.Time.Sub(*details.LastPaymentTime).Abs() <= time.Minute &&
			details.LastPaymentCents == amount && details.LastPaymentCurrency == sub.Currency && amount == int64(sub.PriceCents) {
			periodEnd = regularPayPalPeriodEnd(sub, *details.CurrentPeriodStart, transaction.Time)
		}
	}

	raw, err := json.Marshal(transaction)
	if err != nil {
		return err
	}
	tpOrder := &model.ThirdPartyPaymentOrder{
		PaymentProvider: sub.PaymentMethod, ExternalOrderID: sub.SubscriptionID,
		CaptureID: transaction.ID, Currency: sub.Currency, PayerEmail: details.PayerEmail, Status: transaction.Status,
		PayerID: details.PayerID, PayerName: details.PayerName, RawPayload: string(raw),
	}
	_, err = s.repo.RecordSubscriptionRenewalOrderTx(ctx, sub, transaction.ID, amount, sub.Currency,
		transaction.Time, periodEnd, details.Status, tpOrder)
	return err
}

func (s *service) HasSubscriptionAccess(ctx context.Context, id int64) (bool, error) {
	return s.novelRepo.HasActiveSubscription(ctx, id)
}

func (s *service) ListManagedSubscriptions(ctx context.Context, id int64) ([]model.UserSubscription, error) {
	return s.repo.ListManagedSubscriptions(ctx, id)
}
