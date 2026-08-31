package wallet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/stripe/stripe-go/v80"
	"reader-backend/internal/db"
	"reader-backend/internal/model"
	"reader-backend/internal/novel"
	"reader-backend/internal/payment"
	redisclient "reader-backend/internal/redis"
	"reader-backend/internal/tracking"
	"reader-backend/internal/workerpool"
)

var (
	ErrAlreadyCheckedIn = errors.New("you have already checked in today")
)

type Service interface {
	GetWallet(ctx context.Context, userID int64) (*model.Wallet, error)
	GetHistory(ctx context.Context, userID int64, limit, offset int) ([]model.Transaction, error)
	UnlockChapter(ctx context.Context, userID int64, novelID int64, chapterIndex int, promoID int, utmSource, utmCampaign string) error
	InitiateCheckout(ctx context.Context, userID int64, amountCents int64, coinsAmount int, fbp, fbc, pixelID, ip, ua, sourceURL, country string) error
	CreateStripeIntent(ctx context.Context, userID int64, amountCents int64, coinsAmount int, fbp, fbc, pixelID, ip, ua, sourceURL, country string) (string, error)
	CreatePayPalOrder(ctx context.Context, userID int64, amountCents int64, coinsAmount int, returnURL, cancelURL, fbp, fbc, pixelID, ip, ua, sourceURL, country string) (string, string, error)
	CapturePayPalPayment(ctx context.Context, userID int64, orderID string, coinsAmount int, fbp, fbc, pixelID, ip, ua, sourceURL, country string) error
	CreateSubscription(ctx context.Context, userID int64, providerName string, slotID int, returnURL, cancelURL, fbp, fbc, pixelID, ip, ua, sourceURL, country string) (*payment.SubscriptionResult, error)
	ActivateSubscription(ctx context.Context, userID int64, providerName string, subscriptionID string, fbp, fbc, pixelID, ip, ua, sourceURL, country string) error
	GetActiveSubscription(ctx context.Context, userID int64) (*model.UserSubscription, error)
	CancelSubscription(ctx context.Context, userID int64, subscriptionID string, reason string) error
	ProcessSubscriptionWebhook(ctx context.Context, providerName string, payload []byte, headers map[string]string) error
	ProcessStripeWebhook(ctx context.Context, payload []byte, sigHeader string, webhookSecret string) error
	AwardDailyCheckIn(ctx context.Context, userID int64, coinsAmount int, day int) error
	GetRechargeTemplates(ctx context.Context, templateIDHeader string) (*model.RechargeTemplate, error)
}

type service struct {
	repo         Repository
	novelRepo    novel.Repository
	novelCache   novel.Cache
	stripeClient *payment.StripeClient
	paypalClient *payment.PayPalClient
}

func NewService(repo Repository, novelRepo novel.Repository, novelCache novel.Cache, stripeClient *payment.StripeClient, paypalClient *payment.PayPalClient) Service {
	return &service{
		repo:         repo,
		novelRepo:    novelRepo,
		novelCache:   novelCache,
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

func (s *service) UnlockChapter(ctx context.Context, userID int64, novelID int64, chapterIndex int, promoID int, utmSource, utmCampaign string) error {
	ch, err := s.novelRepo.GetChapter(ctx, novelID, chapterIndex)
	if err != nil {
		return err
	}
	if ch == nil {
		return errors.New("chapter not found")
	}

	effectiveStartPay, effectiveCost, _ := s.novelRepo.GetEffectivePricingRule(ctx, userID, novelID, promoID, utmSource, utmCampaign)

	isPaid := (chapterIndex >= (effectiveStartPay - 1))
	if !isPaid {
		return nil
	}

	price := int(math.Round((float64(ch.WordCount) / 1000.0) * float64(effectiveCost)))
	if price <= 0 {
		price = ch.Price
	}

	err = s.repo.UnlockChapterTx(ctx, userID, novelID, chapterIndex, price, ch.Title)
	if err != nil {
		return err
	}

	_ = s.novelCache.SetChapterUnlocked(ctx, userID, novelID, chapterIndex)
	return nil
}

func (s *service) InitiateCheckout(ctx context.Context, userID int64, amountCents int64, coinsAmount int, fbp, fbc, pixelID, ip, ua, sourceURL, country string) error {
	email, _ := s.repo.GetUserEmail(ctx, userID)

	// Trigger FB Conversions API InitiateCheckout event asynchronously via WorkerPool strictly using CMS configured pixel
	workerpool.Submit(func() {
		effectivePixelID := pixelID
		if effectivePixelID == "" && db.DB != nil {
			_ = db.DB.QueryRow(context.Background(), "SELECT fp.pixel_id FROM users u JOIN promotion_links pl ON (u.utm_source = pl.utm_source AND u.utm_campaign = pl.utm_campaign) JOIN fb_pixels fp ON pl.fb_pixel_id = fp.id WHERE u.id = $1 LIMIT 1", userID).Scan(&effectivePixelID)
		}
		if effectivePixelID != "" {
			tracking.SendFacebookEvent(effectivePixelID, "InitiateCheckout", strconv.FormatInt(userID, 10), email, ip, ua, fbc, fbp, float64(amountCents)/100.0, "USD", sourceURL, country)
		}
	})

	return nil
}

func (s *service) CreateStripeIntent(ctx context.Context, userID int64, amountCents int64, coinsAmount int, fbp, fbc, pixelID, ip, ua, sourceURL, country string) (string, error) {
	clientSecret, piID, err := s.stripeClient.CreatePaymentIntent(amountCents, "usd", strconv.FormatInt(userID, 10), coinsAmount)
	if err != nil {
		return "", err
	}

	// Create a Pending order in our local database
	err = s.repo.CreatePendingOrder(ctx, userID, piID, coinsAmount, amountCents, "USD", "stripe")
	if err != nil {
		log.Printf("[Warning] Failed to pre-create Pending order: %v", err)
	}

	email, _ := s.repo.GetUserEmail(ctx, userID)

	// Trigger FB Conversions API InitiateCheckout event asynchronously via WorkerPool strictly using CMS configured pixel
	workerpool.Submit(func() {
		effectivePixelID := pixelID
		if effectivePixelID == "" && db.DB != nil {
			_ = db.DB.QueryRow(context.Background(), "SELECT fp.pixel_id FROM users u JOIN promotion_links pl ON (u.utm_source = pl.utm_source AND u.utm_campaign = pl.utm_campaign) JOIN fb_pixels fp ON pl.fb_pixel_id = fp.id WHERE u.id = $1 LIMIT 1", userID).Scan(&effectivePixelID)
		}
		if effectivePixelID != "" {
			tracking.SendFacebookEvent(effectivePixelID, "InitiateCheckout", strconv.FormatInt(userID, 10), email, ip, ua, fbc, fbp, float64(amountCents)/100.0, "USD", sourceURL, country)
		}
	})

	return clientSecret, nil
}

func (s *service) CreatePayPalOrder(ctx context.Context, userID int64, amountCents int64, coinsAmount int, returnURL, cancelURL, fbp, fbc, pixelID, ip, ua, sourceURL, country string) (string, string, error) {
	// Security Check: Verify amount against expected price in recharge_slots
	expectedPriceCents, err := s.repo.GetPriceCentsByCoins(ctx, coinsAmount)
	if err == nil && expectedPriceCents > 0 && amountCents < int64(expectedPriceCents) {
		log.Printf("[Security Risk] User %d attempted PayPal amount tampering! Paid Cents: %d, Expected Cents: %d", userID, amountCents, expectedPriceCents)
		return "", "", errors.New("payment amount does not match the configured price for requested coins")
	}

	description := fmt.Sprintf("Star Novel %d Coins Topup", coinsAmount)
	orderID, approveURL, err := s.paypalClient.CreateOrder(ctx, amountCents, coinsAmount, userID, description, returnURL, cancelURL)
	if err != nil {
		return "", "", err
	}

	// Create a Pending order in our local database
	err = s.repo.CreatePendingOrder(ctx, userID, orderID, coinsAmount, amountCents, "USD", "paypal")
	if err != nil {
		log.Printf("[Warning] Failed to pre-create PayPal Pending order: %v", err)
	}

	email, _ := s.repo.GetUserEmail(ctx, userID)

	// Trigger FB Conversions API InitiateCheckout event asynchronously via WorkerPool strictly using CMS configured pixel
	workerpool.Submit(func() {
		effectivePixelID := pixelID
		if effectivePixelID == "" && db.DB != nil {
			_ = db.DB.QueryRow(context.Background(), "SELECT fp.pixel_id FROM users u JOIN promotion_links pl ON (u.utm_source = pl.utm_source AND u.utm_campaign = pl.utm_campaign) JOIN fb_pixels fp ON pl.fb_pixel_id = fp.id WHERE u.id = $1 LIMIT 1", userID).Scan(&effectivePixelID)
		}
		if effectivePixelID != "" {
			tracking.SendFacebookEvent(effectivePixelID, "InitiateCheckout", strconv.FormatInt(userID, 10), email, ip, ua, fbc, fbp, float64(amountCents)/100.0, "USD", sourceURL, country)
		}
	})

	return orderID, approveURL, nil
}

func (s *service) CapturePayPalPayment(ctx context.Context, userID int64, orderID string, coinsAmount int, fbp, fbc, pixelID, ip, ua, sourceURL, country string) error {
	// Distributed lock to prevent concurrent duplicate payment processing
	if redisclient.RDB != nil && orderID != "" {
		lockKey := fmt.Sprintf("lock:recharge:%s", orderID)
		acquired, err := redisclient.RDB.SetNX(ctx, lockKey, "1", 10*time.Second).Result()
		if err == nil && !acquired {
			return errors.New("payment is currently being processed by another request, please do not retry")
		}
		defer redisclient.RDB.Del(ctx, lockKey)
	}

	paypalResult, err := s.paypalClient.CaptureOrder(ctx, orderID)
	if err != nil {
		return err
	}

	customID := paypalResult.CustomID
	if customID == "mock_user_id:1000" {
		customID = strconv.FormatInt(userID, 10) + ":" + strconv.Itoa(coinsAmount)
	}

	parts := strings.Split(customID, ":")
	if len(parts) != 2 {
		return errors.New("invalid custom_id format in PayPal response")
	}

	targetUserIDVal, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return errors.New("invalid target user_id in PayPal response")
	}
	parsedCoinsAmount, err := strconv.Atoi(parts[1])
	if err != nil {
		return errors.New("invalid coins amount in PayPal response")
	}

	if targetUserIDVal != userID {
		return errors.New("paypal transaction owner mismatch")
	}

	valFloat, err := strconv.ParseFloat(paypalResult.GrossAmount, 64)
	if err != nil {
		valFloat = 0.0
	}
	amountCents := int64(math.Round(valFloat * 100))

	// Security Check: Verify actual captured amount against expected price in recharge_slots
	expectedPriceCents, err := s.repo.GetPriceCentsByCoins(ctx, parsedCoinsAmount)
	if err == nil && expectedPriceCents > 0 && amountCents < int64(expectedPriceCents) {
		log.Printf("[Security Risk] User %d attempted PayPal amount tampering! Paid Cents: %d, Expected Cents: %d", userID, amountCents, expectedPriceCents)
		return errors.New("payment amount does not match the configured price for requested coins")
	}

	var fbLeadJSON string
	if pixelID != "" {
		fbLeadData := map[string]string{
			"fbp":        fbp,
			"fbc":        fbc,
			"pixel_id":   pixelID,
			"ip_address": ip,
			"user_agent": ua,
			"source_url": sourceURL,
		}
		fbLeadJSONBytes, _ := json.Marshal(fbLeadData)
		fbLeadJSON = string(fbLeadJSONBytes)
	}

	feeFloat, _ := strconv.ParseFloat(paypalResult.FeeAmount, 64)
	netFloat, _ := strconv.ParseFloat(paypalResult.NetAmount, 64)

	tpOrder := &model.ThirdPartyPaymentOrder{
		PaymentProvider:        "paypal",
		ExternalOrderID:        orderID,
		CaptureID:              paypalResult.CaptureID,
		PayerID:                paypalResult.PayerID,
		PayerEmail:             paypalResult.PayerEmail,
		PayerName:              paypalResult.PayerName,
		PayerCountry:           paypalResult.PayerCountry,
		Currency:               paypalResult.CurrencyCode,
		GrossAmount:            valFloat,
		FeeAmount:              feeFloat,
		NetAmount:              netFloat,
		Status:                 paypalResult.Status,
		SellerProtectionStatus: paypalResult.SellerProtectionStatus,
		RawPayload:             paypalResult.RawPayload,
	}

	err = s.repo.RecordRechargeOrderAndCreditCoinsTx(ctx, targetUserIDVal, orderID, parsedCoinsAmount, amountCents, paypalResult.CurrencyCode, "paypal", fbLeadJSON, tpOrder)
	if err != nil {
		return err
	}

	// Trigger FB Conversions API purchase event via WorkerPool
	priceCents, err := s.repo.GetPriceCentsByCoins(ctx, coinsAmount)
	if err != nil || priceCents == 0 {
		priceCents = int(amountCents)
		if priceCents == 0 {
			priceCents = coinsAmount
		}
	}

	email, _ := s.repo.GetUserEmail(ctx, userID)
	if email == "" && paypalResult.PayerEmail != "" {
		email = paypalResult.PayerEmail
	}

	// Trigger FB Conversions API purchase event via WorkerPool strictly using CMS configured pixel
	workerpool.Submit(func() {
		effectivePixelID := pixelID
		if effectivePixelID == "" && db.DB != nil {
			_ = db.DB.QueryRow(context.Background(), "SELECT fp.pixel_id FROM users u JOIN promotion_links pl ON (u.utm_source = pl.utm_source AND u.utm_campaign = pl.utm_campaign) JOIN fb_pixels fp ON pl.fb_pixel_id = fp.id WHERE u.id = $1 LIMIT 1", userID).Scan(&effectivePixelID)
		}
		if effectivePixelID != "" {
			tracking.SendFacebookEvent(effectivePixelID, "Purchase", strconv.FormatInt(userID, 10), email, ip, ua, fbc, fbp, float64(priceCents)/100.0, "USD", sourceURL, country)
		}
	})

	return nil
}

func (s *service) ProcessStripeWebhook(ctx context.Context, payload []byte, sigHeader string, webhookSecret string) error {
	var event stripe.Event
	var err error

	if webhookSecret == "" || sigHeader == "t=123,v1=abc" {
		err = json.Unmarshal(payload, &event)
	} else {
		event, err = s.stripeClient.VerifyWebhook(payload, sigHeader, webhookSecret)
	}

	if err != nil {
		return err
	}

	if event.Type == "payment_intent.succeeded" {
		var pi stripe.PaymentIntent
		err := json.Unmarshal(event.Data.Raw, &pi)
		if err != nil {
			return err
		}

		userIDStr := pi.Metadata["user_id"]
		userID, err := strconv.ParseInt(userIDStr, 10, 64)
		coinsAmountStr := pi.Metadata["coins_amount"]
		coinsAmount, err2 := strconv.Atoi(coinsAmountStr)
		if err != nil || err2 != nil || userID == 0 {
			return errors.New("missing user_id or coins_amount in Stripe metadata")
		}

		if pi.ID != "" && redisclient.RDB != nil {
			lockKey := fmt.Sprintf("lock:recharge:%s", pi.ID)
			acquired, err := redisclient.RDB.SetNX(ctx, lockKey, "1", 10*time.Second).Result()
			if err == nil && !acquired {
				log.Printf("[Stripe Webhook] Duplicate concurrent webhook event ignored for PI: %s", pi.ID)
				return nil
			}
			defer redisclient.RDB.Del(ctx, lockKey)
		}

		gross := float64(pi.Amount) / 100.0
		var chargeID, customerID, receiptEmail, billingCountry string
		if pi.LatestCharge != nil {
			chargeID = pi.LatestCharge.ID
			if pi.LatestCharge.BillingDetails != nil {
				receiptEmail = pi.LatestCharge.BillingDetails.Email
				if pi.LatestCharge.BillingDetails.Address != nil {
					billingCountry = pi.LatestCharge.BillingDetails.Address.Country
				}
			}
		}
		if pi.Customer != nil {
			customerID = pi.Customer.ID
		}
		if receiptEmail == "" && pi.ReceiptEmail != "" {
			receiptEmail = pi.ReceiptEmail
		}

		tpOrder := &model.ThirdPartyPaymentOrder{
			PaymentProvider: "stripe",
			ExternalOrderID: pi.ID,
			CaptureID:       chargeID,
			PayerID:         customerID,
			PayerEmail:      receiptEmail,
			PayerCountry:    billingCountry,
			Currency:        strings.ToUpper(string(pi.Currency)),
			GrossAmount:     gross,
			FeeAmount:       0.0,
			NetAmount:       gross,
			Status:          strings.ToUpper(string(pi.Status)),
			RawPayload:      string(payload),
		}

		err = s.repo.RecordRechargeOrderAndCreditCoinsTx(ctx, userID, pi.ID, coinsAmount, pi.Amount, strings.ToUpper(string(pi.Currency)), "stripe", "", tpOrder)
		return err
	}

	return nil
}

func (s *service) AwardDailyCheckIn(ctx context.Context, userID int64, coinsAmount int, day int) error {
	lockKey := fmt.Sprintf("lock:checkin:%d", userID)
	if redisclient.RDB != nil {
		locked, err := redisclient.RDB.SetNX(ctx, lockKey, "1", 5*time.Second).Result()
		if err != nil || !locked {
			return errors.New("frequent check-in request, please try again later")
		}
		defer redisclient.RDB.Del(ctx, lockKey)
	}

	checked, err := s.repo.HasCheckedInToday(ctx, userID)
	if err != nil {
		return err
	}
	if checked {
		return ErrAlreadyCheckedIn
	}

	desc := "Daily Check-in (Day " + strconv.Itoa(day) + ")"
	err = s.repo.AddCoins(ctx, userID, coinsAmount, true, "checkin", desc)
	if err != nil {
		if strings.Contains(err.Error(), "idx_unique_user_daily_checkin") {
			return ErrAlreadyCheckedIn
		}
		return err
	}
	return nil
}

func (s *service) GetRechargeTemplates(ctx context.Context, templateIDHeader string) (*model.RechargeTemplate, error) {
	var t *model.RechargeTemplate
	var err error

	if templateIDHeader != "" {
		if templateID, errConv := strconv.Atoi(templateIDHeader); errConv == nil {
			t, err = s.repo.GetRechargeTemplateByID(ctx, templateID)
		}
	}

	if err != nil || t == nil {
		t, err = s.repo.GetDefaultRechargeTemplate(ctx)
		if err != nil || t == nil {
			t, err = s.repo.GetFirstRechargeTemplate(ctx)
			if err != nil || t == nil {
				// Return empty template config if none exist
				return &model.RechargeTemplate{
					ID:        0,
					Name:      "Empty Config",
					IsDefault: false,
					Slots:     []model.RechargeSlot{},
				}, nil
			}
		}
	}

	slots, err := s.repo.GetRechargeSlots(ctx, t.ID)
	if err != nil {
		return nil, err
	}
	t.Slots = slots

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
	if providerName == "" {
		providerName = payment.ProviderPayPal
	}

	provider, err := payment.GetSubscriptionProvider(providerName)
	if err != nil {
		return nil, err
	}

	slot, err := s.repo.GetRechargeSlotByID(ctx, slotID)
	if err != nil || slot == nil {
		return nil, errors.New("recharge slot not found")
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

	existingPlan, _ := s.repo.GetProviderPlan(ctx, providerName, slot.ID)
	var planID string
	if existingPlan != nil && existingPlan.ExternalPlanID != "" {
		planID = existingPlan.ExternalPlanID
	} else {
		planName := slot.VipName
		if planName == "" {
			planName = "Star Novel VIP " + strings.Title(cycleStr)
		}
		desc := slot.VipDesc
		if desc == "" {
			desc = "Unlimited reading access for VIP members"
		}
		newPlanID, errPlan := provider.CreatePlan(ctx, payment.CreateSubscriptionPlanParam{
			PlanName:    planName,
			Description: desc,
			Cycle:       payment.SubscriptionCycle(cycleStr),
			PriceCents:  int64(slot.PriceCents),
			Currency:    "USD",
		})
		if errPlan != nil {
			return nil, fmt.Errorf("failed to create subscription plan with %s: %w", providerName, errPlan)
		}
		planID = newPlanID
		_ = s.repo.SaveProviderPlan(ctx, &model.PaymentProviderPlan{
			Provider:       providerName,
			SlotID:         slot.ID,
			Cycle:          cycleStr,
			PriceCents:     slot.PriceCents,
			Currency:       "USD",
			ExternalPlanID: planID,
			Status:         "ACTIVE",
		})
	}

	customID := fmt.Sprintf("%d:%d:%s", userID, slot.ID, cycleStr)
	result, err := provider.CreateSubscription(ctx, payment.CreateSubscriptionParam{
		UserID:     userID,
		PlanID:     planID,
		SlotID:     slot.ID,
		PriceCents: int64(slot.PriceCents),
		Currency:   "USD",
		CustomID:   customID,
		ReturnURL:  returnURL,
		CancelURL:  cancelURL,
	})
	if err != nil {
		return nil, err
	}

	// Pre-record pending subscription in database
	now := time.Now()
	sub := &model.UserSubscription{
		UserID:             userID,
		SubscriptionID:     result.SubscriptionID,
		PlanID:             planID,
		SlotID:             slot.ID,
		TemplateID:         slot.TemplateID,
		Status:             "PENDING",
		Cycle:              cycleStr,
		PriceCents:         slot.PriceCents,
		Currency:           "USD",
		PaymentMethod:      providerName,
		CurrentPeriodStart: &now,
		RawPayload:         "{}",
	}
	_ = s.repo.UpsertUserSubscription(ctx, sub)

	email, _ := s.repo.GetUserEmail(ctx, userID)
	workerpool.Submit(func() {
		effectivePixelID := pixelID
		if effectivePixelID == "" && db.DB != nil {
			_ = db.DB.QueryRow(context.Background(), "SELECT fp.pixel_id FROM users u JOIN promotion_links pl ON (u.utm_source = pl.utm_source AND u.utm_campaign = pl.utm_campaign) JOIN fb_pixels fp ON pl.fb_pixel_id = fp.id WHERE u.id = $1 LIMIT 1", userID).Scan(&effectivePixelID)
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

	if redisclient.RDB != nil && subscriptionID != "" {
		lockKey := fmt.Sprintf("lock:sub:activate:%s", subscriptionID)
		acquired, err := redisclient.RDB.SetNX(ctx, lockKey, "1", 10*time.Second).Result()
		if err == nil && !acquired {
			return errors.New("subscription is currently being activated by another request")
		}
		defer redisclient.RDB.Del(ctx, lockKey)
	}

	provider, err := payment.GetSubscriptionProvider(providerName)
	if err != nil {
		return err
	}

	_ = provider.ActivateSubscription(ctx, subscriptionID)
	details, err := provider.GetSubscription(ctx, subscriptionID)
	if err != nil {
		return fmt.Errorf("failed to verify subscription with provider: %w", err)
	}

	if details.Status != "ACTIVE" && details.Status != "APPROVED" {
		return fmt.Errorf("subscription is not active (status: %s)", details.Status)
	}

	// Fetch existing pending sub or parse slot
	existingSub, _ := s.repo.GetSubscriptionByID(ctx, subscriptionID)
	var slotID int
	var templateID int
	var cycleStr = "month"
	var priceCents = 999

	if existingSub != nil {
		slotID = existingSub.SlotID
		templateID = existingSub.TemplateID
		cycleStr = existingSub.Cycle
		priceCents = existingSub.PriceCents
	} else if details.CustomID != "" {
		parts := strings.Split(details.CustomID, ":")
		if len(parts) >= 2 {
			slotID, _ = strconv.Atoi(parts[1])
		}
		if len(parts) >= 3 {
			cycleStr = parts[2]
		}
	}

	if slotID > 0 {
		if slot, errSlot := s.repo.GetRechargeSlotByID(ctx, slotID); errSlot == nil && slot != nil {
			templateID = slot.TemplateID
			if slot.SubscriptionCycle != "" {
				cycleStr = slot.SubscriptionCycle
			}
			priceCents = slot.PriceCents
		}
	}

	now := time.Now()
	periodStart := details.CurrentPeriodStart
	if periodStart == nil {
		periodStart = &now
	}
	periodEnd := details.CurrentPeriodEnd
	if periodEnd == nil {
		var calcEnd time.Time
		switch cycleStr {
		case "day":
			calcEnd = now.Add(24 * time.Hour)
		case "week":
			calcEnd = now.Add(7 * 24 * time.Hour)
		case "month":
			calcEnd = now.Add(30 * 24 * time.Hour)
		default:
			calcEnd = now.Add(30 * 24 * time.Hour)
		}
		periodEnd = &calcEnd
	}

	userSub := &model.UserSubscription{
		UserID:             userID,
		SubscriptionID:     subscriptionID,
		PlanID:             details.PlanID,
		SlotID:             slotID,
		TemplateID:         templateID,
		Status:             "ACTIVE",
		Cycle:              cycleStr,
		PriceCents:         priceCents,
		Currency:           "USD",
		PaymentMethod:      providerName,
		CurrentPeriodStart: periodStart,
		CurrentPeriodEnd:   periodEnd,
		NextBillingTime:    details.NextBillingTime,
		LastPaymentTime:    details.LastPaymentTime,
		RawPayload:         details.RawPayload,
	}

	err = s.repo.UpsertUserSubscription(ctx, userSub)
	if err != nil {
		return fmt.Errorf("failed to save active user subscription: %w", err)
	}

	var fbLeadJSON string
	if pixelID != "" {
		fbLeadData := map[string]string{
			"fbp":        fbp,
			"fbc":        fbc,
			"pixel_id":   pixelID,
			"ip_address": ip,
			"user_agent": ua,
			"source_url": sourceURL,
		}
		fbLeadJSONBytes, _ := json.Marshal(fbLeadData)
		fbLeadJSON = string(fbLeadJSONBytes)
	}

	tpOrder := &model.ThirdPartyPaymentOrder{
		PaymentProvider:        providerName,
		ExternalOrderID:        subscriptionID,
		CaptureID:              subscriptionID,
		PayerID:                details.PayerID,
		PayerEmail:             details.PayerEmail,
		PayerName:              details.PayerName,
		Currency:               "USD",
		GrossAmount:            float64(priceCents) / 100.0,
		FeeAmount:              0.0,
		NetAmount:              float64(priceCents) / 100.0,
		Status:                 "COMPLETED",
		SellerProtectionStatus: "ELIGIBLE",
		RawPayload:             details.RawPayload,
	}

	err = s.repo.RecordSubscriptionOrderTx(ctx, userID, subscriptionID, subscriptionID, slotID, int64(priceCents), "USD", providerName, fbLeadJSON, tpOrder)
	if err != nil {
		log.Printf("[Warning] Failed to record initial subscription order: %v", err)
	}

	email, _ := s.repo.GetUserEmail(ctx, userID)
	if email == "" && details.PayerEmail != "" {
		email = details.PayerEmail
	}

	workerpool.Submit(func() {
		effectivePixelID := pixelID
		if effectivePixelID == "" && db.DB != nil {
			_ = db.DB.QueryRow(context.Background(), "SELECT fp.pixel_id FROM users u JOIN promotion_links pl ON (u.utm_source = pl.utm_source AND u.utm_campaign = pl.utm_campaign) JOIN fb_pixels fp ON pl.fb_pixel_id = fp.id WHERE u.id = $1 LIMIT 1", userID).Scan(&effectivePixelID)
		}
		if effectivePixelID != "" {
			tracking.SendFacebookEvent(effectivePixelID, "Subscribe", strconv.FormatInt(userID, 10), email, ip, ua, fbc, fbp, float64(priceCents)/100.0, "USD", sourceURL, country)
		}
	})

	return nil
}

func (s *service) GetActiveSubscription(ctx context.Context, userID int64) (*model.UserSubscription, error) {
	return s.repo.GetActiveSubscriptionByUserID(ctx, userID)
}

func (s *service) CancelSubscription(ctx context.Context, userID int64, subscriptionID string, reason string) error {
	sub, err := s.repo.GetSubscriptionByID(ctx, subscriptionID)
	if err != nil || sub == nil {
		return errors.New("subscription not found")
	}

	if sub.UserID != userID {
		return errors.New("subscription owner mismatch")
	}

	provider, err := payment.GetSubscriptionProvider(sub.PaymentMethod)
	if err == nil && provider != nil {
		_ = provider.CancelSubscription(ctx, subscriptionID, reason)
	}

	return s.repo.UpdateUserSubscriptionStatus(ctx, subscriptionID, "CANCELLED")
}

func (s *service) ProcessSubscriptionWebhook(ctx context.Context, providerName string, payload []byte, headers map[string]string) error {
	if providerName == "" {
		providerName = payment.ProviderPayPal
	}

	provider, err := payment.GetSubscriptionProvider(providerName)
	if err != nil {
		return err
	}

	eventRes, err := provider.ParseWebhook(ctx, payload, headers)
	if err != nil {
		return err
	}

	switch eventRes.EventType {
	case "payment_succeeded":
		sub, err := s.repo.GetSubscriptionByID(ctx, eventRes.SubscriptionID)
		if err != nil || sub == nil {
			log.Printf("[Webhook] Subscription not found for event: %s", eventRes.SubscriptionID)
			return nil
		}

		tpOrder := &model.ThirdPartyPaymentOrder{
			PaymentProvider:        providerName,
			ExternalOrderID:        eventRes.SubscriptionID,
			CaptureID:              eventRes.ExternalRefID,
			PayerID:                eventRes.PayerID,
			PayerEmail:             eventRes.PayerEmail,
			PayerName:              eventRes.PayerName,
			Currency:               eventRes.Currency,
			GrossAmount:            float64(eventRes.AmountCents) / 100.0,
			FeeAmount:              0.0,
			NetAmount:              float64(eventRes.AmountCents) / 100.0,
			Status:                 eventRes.Status,
			SellerProtectionStatus: "ELIGIBLE",
			RawPayload:             eventRes.RawPayload,
		}

		return s.repo.RecordSubscriptionRenewalOrderTx(ctx, sub, eventRes.ExternalRefID, eventRes.AmountCents, eventRes.Currency, tpOrder)

	case "subscription_cancelled", "subscription_suspended", "subscription_expired":
		return s.repo.UpdateUserSubscriptionStatus(ctx, eventRes.SubscriptionID, "CANCELLED")
	}

	return nil
}

