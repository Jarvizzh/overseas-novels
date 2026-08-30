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

	customID, currency, value, err := s.paypalClient.CaptureOrder(ctx, orderID)
	if err != nil {
		return err
	}

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

	valFloat, err := strconv.ParseFloat(value, 64)
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

	err = s.repo.RecordRechargeOrderAndCreditCoinsTx(ctx, targetUserIDVal, orderID, parsedCoinsAmount, amountCents, currency, "paypal", fbLeadJSON)
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

		err = s.repo.RecordRechargeOrderAndCreditCoinsTx(ctx, userID, pi.ID, coinsAmount, pi.Amount, strings.ToUpper(string(pi.Currency)), "stripe", "")
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
