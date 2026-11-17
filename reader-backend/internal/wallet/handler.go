package wallet

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"reader-backend/internal/auth"
	"reader-backend/internal/httpx"
	"reader-backend/internal/model"
	"reader-backend/internal/novel"
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
		walletGroup.GET("/history/cursor", h.GetHistoryPage)
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
		walletGroup.GET("/rewards/checkin", h.GetCheckIn)
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
	limit, offset, ok := httpx.Pagination(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()

	txs, err := h.service.GetHistory(ctx, userID, limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch transactions history"})
		return
	}

	c.JSON(http.StatusOK, txs)
}

func (h *Handler) GetHistoryPage(c *gin.Context) {
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if err != nil || limit < 1 || limit > httpx.MaxPageSize {
		httpx.Fail(c, http.StatusBadRequest, "invalid_pagination", "Page size must be 1–100", nil)
		return
	}
	after, err := parseHistoryCursor(c.Query("cursor"))
	if err != nil {
		httpx.Fail(c, http.StatusBadRequest, "invalid_cursor", "Invalid transaction cursor", nil)
		return
	}
	page, err := h.service.GetHistoryPage(c.Request.Context(), c.GetInt64("user_id"), limit, after)
	if err != nil {
		httpx.Fail(c, http.StatusServiceUnavailable, "history_unavailable", "Failed to fetch transaction history", err)
		return
	}
	c.JSON(http.StatusOK, page)
}

type UnlockRequest struct {
	UserID         int64  `json:"user_id"`
	ChapterID      string `json:"chapter_id" binding:"max=64"`
	ContentVersion int64  `json:"content_version"`
	NovelID        int64  `json:"novel_id" binding:"required,gt=0"`
	ChapterIndex   int    `json:"chapter_index" binding:"gte=0"`
	Price          *int   `json:"price" binding:"required,gte=0"`
}

func (h *Handler) UnlockChapter(c *gin.Context) {
	userID := c.MustGet("user_id").(int64)
	var req UnlockRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, http.StatusBadRequest, "invalid_request", "Invalid request fields", nil)
		return
	}

	if req.ChapterID == "" || req.ContentVersion <= 0 {
		httpx.Fail(c, http.StatusConflict, "price_changed", ErrPriceChanged.Error(), nil)
		return
	}

	if req.UserID != userID {
		httpx.Fail(c, http.StatusConflict, "account_changed", "The signed-in account changed. Reload before unlocking.", nil)
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
	err := h.service.UnlockChapter(ctx, userID, req.NovelID, req.ChapterIndex, promoID, utmSource, utmCampaign, ChapterQuote{ID: req.ChapterID, Version: req.ContentVersion, Price: *req.Price})
	if err != nil {
		if errors.Is(err, novel.ErrNovelNotFound) || errors.Is(err, ErrChapterNotFound) {
			httpx.Fail(c, 404, "resource_unavailable", "Resource unavailable", nil)
			return
		}
		if errors.Is(err, ErrPurchaseNeedsReview) {
			httpx.Fail(c, http.StatusConflict, "purchase_needs_review", err.Error(), nil)
			return
		}
		if errors.Is(err, ErrPriceChanged) {
			code, message := publicWalletError(err)
			httpx.Fail(c, 409, code, message, nil)
			return
		}
		if errors.Is(err, ErrInsufficientBalance) {
			c.JSON(http.StatusPaymentRequired, gin.H{"error": "Insufficient balance. Please top up."})
			return
		}
		if errors.Is(err, ErrAlreadyUnlocked) {
			c.JSON(http.StatusOK, gin.H{"message": "Chapter is already unlocked"})
			return
		}
		httpx.Fail(c, http.StatusInternalServerError, "internal_error", "Request failed", err)
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
		httpx.Fail(c, http.StatusBadRequest, "invalid_request", "Invalid request fields", nil)
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
		httpx.Fail(c, http.StatusInternalServerError, "internal_error", "Request failed", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "InitiateCheckout tracked"})
}

type StripeRechargeRequest struct {
	AmountCents int64 `json:"amount_cents" binding:"required"`
	CoinsAmount int   `json:"coins_amount" binding:"required"`
}

func (h *Handler) CreateStripeIntent(c *gin.Context) {
	// TODO(stripe): enable this endpoint when Stripe settlement is implemented.
	c.JSON(http.StatusServiceUnavailable, gin.H{"error": ErrStripeDisabled.Error()})
}

type PayPalCreateOrderRequest struct {
	SlotID    int    `json:"slot_id" binding:"gt=0"`
	NovelID   int64  `json:"novel_id"`
	ReturnURL string `json:"return_url"`
	CancelURL string `json:"cancel_url"`
}

func (h *Handler) CreatePayPalOrder(c *gin.Context) {
	var req PayPalCreateOrderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid checkout package"})
		return
	}
	attribution := checkoutAttribution{
		FBP: c.GetHeader("X-FB-FBP"), FBC: c.GetHeader("X-FB-FBC"), IP: c.ClientIP(),
		UserAgent: c.Request.UserAgent(), SourceURL: c.GetHeader("X-Event-Source-URL"), Country: c.GetHeader("X-Country"),
	}
	if attribution.Country == "" {
		attribution.Country = c.GetHeader("CF-IPCountry")
	}
	id, approval, err := h.service.CreatePayPalOrder(c.Request.Context(), c.GetInt64("user_id"), req.SlotID, req.NovelID, req.ReturnURL, req.CancelURL, attribution)
	if errors.Is(err, ErrCheckoutUnavailable) {
		httpx.Fail(c, 404, "resource_unavailable", "Resource unavailable", nil)
		return
	}
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "PayPal checkout unavailable"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"order_id": id, "approve_url": approval})
}

type PayPalCaptureRequest struct {
	OrderID string `json:"order_id" binding:"required,max=100"`
}

func (h *Handler) CapturePayPalPayment(c *gin.Context) {
	var req PayPalCaptureRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid payment order"})
		return
	}
	result, err := h.service.CapturePayPalPayment(c.Request.Context(), c.GetInt64("user_id"), req.OrderID)
	if errors.Is(err, ErrCheckoutUnavailable) {
		httpx.Fail(c, 404, "resource_unavailable", "Resource unavailable", nil)
		return
	}
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "Payment confirmation is pending. Please try again."})
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h *Handler) StripeWebhook(c *gin.Context) {
	// TODO(stripe): enable this endpoint when Stripe settlement is implemented.
	c.JSON(http.StatusServiceUnavailable, gin.H{"error": ErrStripeDisabled.Error()})
}

func (h *Handler) GetCheckIn(c *gin.Context) {
	h.checkIn(c, false)
}

func (h *Handler) DailyCheckIn(c *gin.Context) {
	h.checkIn(c, true)
}

func (h *Handler) checkIn(c *gin.Context, claim bool) {
	state, err := h.service.CheckIn(c.Request.Context(), c.GetInt64("user_id"), claim)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Check-in unavailable"})
		return
	}
	c.JSON(http.StatusOK, state)
}

func (h *Handler) GetRechargeTemplates(c *gin.Context) {
	ctx := c.Request.Context()
	var novelID int64
	if raw := c.Query("novel_id"); raw != "" {
		var err error
		novelID, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || novelID <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid novel ID"})
			return
		}
	}
	slots, err := h.service.GetRechargeTemplates(ctx, c.GetInt64("user_id"), novelID)
	if err != nil {
		httpx.Fail(c, http.StatusInternalServerError, "internal_error", "Failed to query template slots", err)
		return
	}

	c.JSON(http.StatusOK, slots)
}

type CreateSubscriptionRequest struct {
	SlotID        int    `json:"slot_id" binding:"required,gt=0"`
	PaymentMethod string `json:"payment_method"` // paypal, stripe, etc.
	ReturnURL     string `json:"return_url"`
	CancelURL     string `json:"cancel_url"`
}

func (h *Handler) CreateSubscription(c *gin.Context) {
	userID := c.MustGet("user_id").(int64)
	var req CreateSubscriptionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, http.StatusBadRequest, "invalid_request", "Invalid request fields", nil)
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
	if errors.Is(err, ErrSubscriptionConflict) || errors.Is(err, ErrSubscriptionPending) || errors.Is(err, ErrSubscriptionReview) || errors.Is(err, ErrCheckoutUnavailable) {
		code, message := publicWalletError(err)
		httpx.Fail(c, 409, code, message, nil)
		return
	}
	if err != nil {
		httpx.Fail(c, http.StatusInternalServerError, "internal_error", "Failed to create subscription", err)
		return
	}

	c.JSON(http.StatusOK, res)
}

type ActivateSubscriptionRequest struct {
	SubscriptionID string `json:"subscription_id" binding:"required,max=100"`
	PaymentMethod  string `json:"payment_method"`
}

func (h *Handler) ActivateSubscription(c *gin.Context) {
	userID := c.MustGet("user_id").(int64)
	var req ActivateSubscriptionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, http.StatusBadRequest, "invalid_request", "Invalid request fields", nil)
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
		if errors.Is(err, ErrCheckoutUnavailable) {
			httpx.Fail(c, 404, "checkout_unavailable", ErrCheckoutUnavailable.Error(), nil)
		} else if errors.Is(err, ErrStripeDisabled) {
			httpx.Fail(c, 503, "stripe_disabled", ErrStripeDisabled.Error(), nil)
		} else {
			httpx.Fail(c, 503, "subscription_pending", "Subscription confirmation unavailable", err)
		}
		return
	}

	c.JSON(http.StatusAccepted, gin.H{"message": "Subscription confirmed. VIP starts when payment settles."})
}

func (h *Handler) GetSubscriptionStatus(c *gin.Context) {
	userID := c.MustGet("user_id").(int64)
	ctx := c.Request.Context()

	subscriptions, err := h.service.ListManagedSubscriptions(ctx, userID)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Subscription status unavailable"})
		return
	}
	var sub *model.UserSubscription
	if len(subscriptions) > 0 {
		sub = &subscriptions[0]
	}

	vip, err := h.service.HasSubscriptionAccess(ctx, userID)
	if err != nil {
		c.AbortWithStatus(503)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"is_vip":        vip,
		"subscription":  sub,
		"subscriptions": subscriptions,
	})
}

type CancelSubscriptionRequest struct {
	SubscriptionID string `json:"subscription_id" binding:"required,max=100"`
	Reason         string `json:"reason" binding:"max=128"`
}

func (h *Handler) CancelSubscription(c *gin.Context) {
	userID := c.MustGet("user_id").(int64)
	var req CancelSubscriptionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, http.StatusBadRequest, "invalid_request", "Invalid request fields", nil)
		return
	}

	ctx := c.Request.Context()
	err := h.service.CancelSubscription(ctx, userID, req.SubscriptionID, req.Reason)
	if errors.Is(err, ErrCheckoutUnavailable) {
		c.JSON(http.StatusNotFound, gin.H{"error": "Subscription unavailable"})
		return
	}
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Cancellation unavailable"})
		return
	}

	c.JSON(http.StatusAccepted, gin.H{"message": "Cancellation requested. Access continues through the paid period."})
}

func (h *Handler) SubscriptionWebhook(c *gin.Context) {
	provider := c.Param("provider")
	if provider == "" {
		provider = "paypal"
	}

	payload, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20))
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
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Payment event could not be processed"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"received": true})
}

func publicWalletError(err error) (string, string) {
	for _, rule := range []struct {
		cause error
		code  string
	}{
		{ErrPriceChanged, "price_changed"}, {ErrSubscriptionConflict, "subscription_conflict"},
		{ErrSubscriptionPending, "subscription_pending"}, {ErrSubscriptionReview, "subscription_review"}, {ErrCheckoutUnavailable, "checkout_unavailable"},
	} {
		if errors.Is(err, rule.cause) {
			return rule.code, rule.cause.Error()
		}
	}
	return "wallet_unavailable", "Wallet unavailable"
}
