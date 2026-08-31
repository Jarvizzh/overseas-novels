package wallet

import (
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"

	"github.com/gin-gonic/gin"
	"reader-backend/internal/auth"
)

type Handler struct {
	service Service
}

func NewHandler(service Service) *Handler {
	return &Handler{
		service: service,
	}
}

func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	walletGroup := r.Group("/wallet", auth.AuthMiddleware())
	{
		walletGroup.GET("/balance", h.GetBalance)
		walletGroup.GET("/history", h.GetHistory)
		walletGroup.POST("/unlock", h.UnlockChapter)
		walletGroup.POST("/recharge/initiate", h.InitiateCheckout)
		walletGroup.POST("/recharge/stripe", h.CreateStripeIntent)
		walletGroup.POST("/recharge/paypal/create-order", h.CreatePayPalOrder)
		walletGroup.POST("/recharge/paypal/capture", h.CapturePayPalPayment)
		walletGroup.POST("/recharge/subscription/create", h.CreateSubscription)
		walletGroup.POST("/recharge/subscription/activate", h.ActivateSubscription)
		walletGroup.POST("/recharge/paypal/create-subscription", h.CreateSubscription)
		walletGroup.POST("/recharge/paypal/activate-subscription", h.ActivateSubscription)
		walletGroup.GET("/subscription/status", h.GetSubscriptionStatus)
		walletGroup.POST("/subscription/cancel", h.CancelSubscription)
		walletGroup.POST("/rewards/checkin", h.DailyCheckIn)
		walletGroup.GET("/recharge/templates", h.GetRechargeTemplates)
	}

	r.POST("/wallet/webhook/stripe", h.StripeWebhook)
	r.POST("/wallet/webhook/paypal", h.SubscriptionWebhook)
	r.POST("/wallet/webhook/subscription/:provider", h.SubscriptionWebhook)
}

func (h *Handler) GetBalance(c *gin.Context) {
	userID := c.MustGet("user_id").(int64)
	ctx := c.Request.Context()

	w, err := h.service.GetWallet(ctx, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch wallet info"})
		return
	}

	c.JSON(http.StatusOK, w)
}

func (h *Handler) GetHistory(c *gin.Context) {
	userID := c.MustGet("user_id").(int64)
	pageStr := c.DefaultQuery("page", "1")
	limitStr := c.DefaultQuery("limit", "20")

	page, err := strconv.Atoi(pageStr)
	if err != nil || page < 1 {
		page = 1
	}

	limit, err := strconv.Atoi(limitStr)
	if err != nil || limit < 1 {
		limit = 20
	}

	offset := (page - 1) * limit
	ctx := c.Request.Context()

	txs, err := h.service.GetHistory(ctx, userID, limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch transactions history"})
		return
	}

	c.JSON(http.StatusOK, txs)
}

type UnlockRequest struct {
	NovelID      int64 `json:"novel_id" binding:"required"`
	ChapterIndex int   `json:"chapter_index" binding:"gte=0"`
}

func (h *Handler) UnlockChapter(c *gin.Context) {
	userID := c.MustGet("user_id").(int64)
	var req UnlockRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	promoIDStr := c.GetHeader("X-Promo-ID")
	if promoIDStr == "" {
		promoIDStr = c.Query("link_id")
		if promoIDStr == "" {
			promoIDStr = c.Query("promo_id")
		}
	}
	promoID, _ := strconv.Atoi(promoIDStr)

	utmSource := c.GetHeader("X-UTM-Source")
	if utmSource == "" {
		utmSource = c.Query("utm_source")
	}
	utmCampaign := c.GetHeader("X-UTM-Campaign")
	if utmCampaign == "" {
		utmCampaign = c.Query("utm_campaign")
	}

	ctx := c.Request.Context()
	err := h.service.UnlockChapter(ctx, userID, req.NovelID, req.ChapterIndex, promoID, utmSource, utmCampaign)
	if err != nil {
		if errors.Is(err, ErrInsufficientBalance) {
			c.JSON(http.StatusPaymentRequired, gin.H{"error": "Insufficient balance. Please top up."})
			return
		}
		if errors.Is(err, ErrAlreadyUnlocked) {
			c.JSON(http.StatusOK, gin.H{"message": "Chapter is already unlocked"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Chapter unlocked successfully"})
}

type InitiateCheckoutRequest struct {
	AmountCents int64 `json:"amount_cents" binding:"required"`
	CoinsAmount int   `json:"coins_amount" binding:"required"`
}

func (h *Handler) InitiateCheckout(c *gin.Context) {
	userID := c.MustGet("user_id").(int64)
	var req InitiateCheckoutRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()

	fbp := c.GetHeader("X-FB-FBP")
	fbc := c.GetHeader("X-FB-FBC")
	pixelID := c.GetHeader("X-FB-Pixel-ID")
	ip := c.ClientIP()
	ua := c.Request.UserAgent()
	sourceURL := c.GetHeader("X-Event-Source-URL")
	country := c.GetHeader("X-Country")
	if country == "" {
		country = c.GetHeader("CF-IPCountry")
	}

	err := h.service.InitiateCheckout(ctx, userID, req.AmountCents, req.CoinsAmount, fbp, fbc, pixelID, ip, ua, sourceURL, country)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "InitiateCheckout tracked"})
}

type StripeRechargeRequest struct {
	AmountCents int64 `json:"amount_cents" binding:"required"`
	CoinsAmount int   `json:"coins_amount" binding:"required"`
}

func (h *Handler) CreateStripeIntent(c *gin.Context) {
	userID := c.MustGet("user_id").(int64)
	var req StripeRechargeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()

	fbp := c.GetHeader("X-FB-FBP")
	fbc := c.GetHeader("X-FB-FBC")
	pixelID := c.GetHeader("X-FB-Pixel-ID")
	ip := c.ClientIP()
	ua := c.Request.UserAgent()
	sourceURL := c.GetHeader("X-Event-Source-URL")
	country := c.GetHeader("X-Country")
	if country == "" {
		country = c.GetHeader("CF-IPCountry")
	}

	clientSecret, err := h.service.CreateStripeIntent(ctx, userID, req.AmountCents, req.CoinsAmount, fbp, fbc, pixelID, ip, ua, sourceURL, country)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create payment intent"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"client_secret": clientSecret,
	})
}

type PayPalCreateOrderRequest struct {
	AmountCents int64  `json:"amount_cents" binding:"required"`
	CoinsAmount int    `json:"coins_amount" binding:"required"`
	ReturnURL   string `json:"return_url"`
	CancelURL   string `json:"cancel_url"`
}

func (h *Handler) CreatePayPalOrder(c *gin.Context) {
	userID := c.MustGet("user_id").(int64)
	var req PayPalCreateOrderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()

	fbp := c.GetHeader("X-FB-FBP")
	fbc := c.GetHeader("X-FB-FBC")
	pixelID := c.GetHeader("X-FB-Pixel-ID")
	ip := c.ClientIP()
	ua := c.Request.UserAgent()
	sourceURL := c.GetHeader("X-Event-Source-URL")
	country := c.GetHeader("X-Country")
	if country == "" {
		country = c.GetHeader("CF-IPCountry")
	}

	orderID, approveURL, err := h.service.CreatePayPalOrder(ctx, userID, req.AmountCents, req.CoinsAmount, req.ReturnURL, req.CancelURL, fbp, fbc, pixelID, ip, ua, sourceURL, country)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create PayPal order: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"order_id":    orderID,
		"approve_url": approveURL,
	})
}

type PayPalCaptureRequest struct {
	OrderID     string `json:"order_id" binding:"required"`
	CoinsAmount int    `json:"coins_amount" binding:"required"`
}

func (h *Handler) CapturePayPalPayment(c *gin.Context) {
	userID := c.MustGet("user_id").(int64)
	var req PayPalCaptureRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()

	fbp := c.GetHeader("X-FB-FBP")
	fbc := c.GetHeader("X-FB-FBC")
	pixelID := c.GetHeader("X-FB-Pixel-ID")
	ip := c.ClientIP()
	ua := c.Request.UserAgent()
	sourceURL := c.GetHeader("X-Event-Source-URL")
	country := c.GetHeader("X-Country")
	if country == "" {
		country = c.GetHeader("CF-IPCountry")
	}

	err := h.service.CapturePayPalPayment(ctx, userID, req.OrderID, req.CoinsAmount, fbp, fbc, pixelID, ip, ua, sourceURL, country)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Payment captured and coins credited successfully"})
}

func (h *Handler) StripeWebhook(c *gin.Context) {
	payload, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to read request body"})
		return
	}

	sigHeader := c.GetHeader("Stripe-Signature")
	webhookSecret := os.Getenv("STRIPE_WEBHOOK_SECRET")

	ctx := c.Request.Context()
	err = h.service.ProcessStripeWebhook(ctx, payload, sigHeader, webhookSecret)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"received": true})
}

type CheckInRequest struct {
	Day   int `json:"day" binding:"required,min=1,max=7"`
	Coins int `json:"coins" binding:"required,min=1"`
}

func (h *Handler) DailyCheckIn(c *gin.Context) {
	userID := c.MustGet("user_id").(int64)
	var req CheckInRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()

	err := h.service.AwardDailyCheckIn(ctx, userID, req.Coins, req.Day)
	if err != nil {
		if errors.Is(err, ErrAlreadyCheckedIn) {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to credit check-in rewards"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Checked in successfully", "coins_awarded": req.Coins})
}

func (h *Handler) GetRechargeTemplates(c *gin.Context) {
	ctx := c.Request.Context()
	templateIDHeader := c.GetHeader("X-Recharge-Template-ID")

	slots, err := h.service.GetRechargeTemplates(ctx, templateIDHeader)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to query template slots: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, slots)
}

type CreateSubscriptionRequest struct {
	SlotID        int    `json:"slot_id" binding:"required"`
	PaymentMethod string `json:"payment_method"` // paypal, stripe, etc.
	ReturnURL     string `json:"return_url"`
	CancelURL     string `json:"cancel_url"`
}

func (h *Handler) CreateSubscription(c *gin.Context) {
	userID := c.MustGet("user_id").(int64)
	var req CreateSubscriptionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	provider := req.PaymentMethod
	if provider == "" {
		provider = "paypal"
	}

	ctx := c.Request.Context()
	fbp := c.GetHeader("X-FB-FBP")
	fbc := c.GetHeader("X-FB-FBC")
	pixelID := c.GetHeader("X-FB-Pixel-ID")
	ip := c.ClientIP()
	ua := c.Request.UserAgent()
	sourceURL := c.GetHeader("X-Event-Source-URL")
	country := c.GetHeader("X-Country")
	if country == "" {
		country = c.GetHeader("CF-IPCountry")
	}

	res, err := h.service.CreateSubscription(ctx, userID, provider, req.SlotID, req.ReturnURL, req.CancelURL, fbp, fbc, pixelID, ip, ua, sourceURL, country)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create subscription: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, res)
}

type ActivateSubscriptionRequest struct {
	SubscriptionID string `json:"subscription_id" binding:"required"`
	PaymentMethod  string `json:"payment_method"`
}

func (h *Handler) ActivateSubscription(c *gin.Context) {
	userID := c.MustGet("user_id").(int64)
	var req ActivateSubscriptionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	provider := req.PaymentMethod
	if provider == "" {
		provider = "paypal"
	}

	ctx := c.Request.Context()
	fbp := c.GetHeader("X-FB-FBP")
	fbc := c.GetHeader("X-FB-FBC")
	pixelID := c.GetHeader("X-FB-Pixel-ID")
	ip := c.ClientIP()
	ua := c.Request.UserAgent()
	sourceURL := c.GetHeader("X-Event-Source-URL")
	country := c.GetHeader("X-Country")
	if country == "" {
		country = c.GetHeader("CF-IPCountry")
	}

	err := h.service.ActivateSubscription(ctx, userID, provider, req.SubscriptionID, fbp, fbc, pixelID, ip, ua, sourceURL, country)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Subscription activated successfully. VIP unlimited access enabled!"})
}

func (h *Handler) GetSubscriptionStatus(c *gin.Context) {
	userID := c.MustGet("user_id").(int64)
	ctx := c.Request.Context()

	sub, err := h.service.GetActiveSubscription(ctx, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if sub == nil {
		c.JSON(http.StatusOK, gin.H{
			"is_vip":       false,
			"subscription": nil,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"is_vip":       true,
		"subscription": sub,
	})
}

type CancelSubscriptionRequest struct {
	SubscriptionID string `json:"subscription_id" binding:"required"`
	Reason         string `json:"reason"`
}

func (h *Handler) CancelSubscription(c *gin.Context) {
	userID := c.MustGet("user_id").(int64)
	var req CancelSubscriptionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()
	err := h.service.CancelSubscription(ctx, userID, req.SubscriptionID, req.Reason)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Subscription cancelled successfully"})
}

func (h *Handler) SubscriptionWebhook(c *gin.Context) {
	provider := c.Param("provider")
	if provider == "" {
		provider = "paypal"
	}

	payload, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to read body"})
		return
	}

	headers := make(map[string]string)
	for k, v := range c.Request.Header {
		if len(v) > 0 {
			headers[k] = v[0]
		}
	}

	ctx := c.Request.Context()
	err = h.service.ProcessSubscriptionWebhook(ctx, provider, payload, headers)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"received": true})
}

