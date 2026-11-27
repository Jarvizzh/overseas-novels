package application

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"reader-backend/internal/auth"
	"reader-backend/internal/conversion"
	"reader-backend/internal/httpx"
	"reader-backend/internal/model"
	"reader-backend/internal/payment"
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
		walletGroup.POST("/recharge/initiate", h.InitiateCheckout)
		walletGroup.POST("/recharge/paypal/create-order", h.CreatePayPalOrder)
		walletGroup.POST("/recharge/paypal/capture", h.CapturePayPalPayment)
		walletGroup.POST("/purchase-intents", h.PreparePurchase)
		walletGroup.GET("/purchase-methods", h.GetPaymentMethods)
		walletGroup.GET("/purchase-intents", h.ListRecentPurchases)
		walletGroup.GET("/purchase-intents/return", h.ReturnedPurchase)
		walletGroup.GET("/purchase-intents/:intent", h.GetPurchase)
		walletGroup.POST("/purchase-intents/:intent/checkout", h.StartPayPalCheckout)
		walletGroup.POST("/purchase-intents/:intent/checkout/stripe", h.StartStripeCheckout)
		walletGroup.POST("/recharge/subscription/create", h.CreateSubscription)
		walletGroup.POST("/subscription/stripe/checkout", h.StartStripeVIP)
		walletGroup.GET("/subscription/stripe/purchase/:intent", h.GetStripeVIPPurchase)
		walletGroup.POST("/subscription/stripe/portal", h.OpenStripeVIPPortal)
		walletGroup.POST("/recharge/subscription/activate", h.ActivateSubscription)
		walletGroup.POST("/recharge/paypal/create-subscription", h.CreateSubscription)
		walletGroup.POST("/recharge/paypal/activate-subscription", h.ActivateSubscription)
		walletGroup.GET("/subscription/status", h.GetSubscriptionStatus)
		walletGroup.GET("/subscription/purchase-return", h.ReturnedVIPPurchase)
		walletGroup.POST("/subscription/cancel", h.CancelSubscription)
		walletGroup.GET("/recharge/templates", h.GetRechargeTemplates)
	}

	r.POST("/wallet/webhook/paypal", h.SubscriptionWebhook)
	r.POST("/wallet/webhook/stripe", h.StripeAftercareWebhook)
	r.POST("/wallet/webhook/subscription/:provider", h.SubscriptionWebhook)
	r.POST("/wallet/webhook/channel/:token", h.ChannelTokenWebhook)
}

type InitiateCheckoutRequest struct {
	AmountCents int64 `json:"amount_cents" binding:"required"`
	CoinsAmount int   `json:"coins_amount" binding:"required"`
}

func (h *Handler) InitiateCheckout(c *gin.Context) {
	var req InitiateCheckoutRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, http.StatusBadRequest, "invalid_request", "Invalid request fields", nil)
		return
	}

	// Older clients still submit this request before the PayPal order exists.
	c.JSON(http.StatusOK, gin.H{"message": "Checkout tracking starts when the payment order is bound"})
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
	attribution := purchaseAttribution(c)
	id, approval, err := h.service.CreatePayPalOrder(c.Request.Context(), c.GetInt64("user_id"), req.SlotID, req.NovelID, req.ReturnURL, req.CancelURL, attribution)
	if err != nil {
		writePurchaseError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"order_id": id, "approve_url": approval})
}

func purchaseAttribution(c *gin.Context) checkoutAttribution {
	attribution := checkoutAttribution{
		FBP: c.GetHeader("X-FB-FBP"), FBC: c.GetHeader("X-FB-FBC"), IP: c.ClientIP(),
		UserAgent: c.Request.UserAgent(), SourceURL: c.GetHeader("X-Event-Source-URL"), Country: c.GetHeader("X-Country"),
	}
	if attribution.Country == "" {
		attribution.Country = c.GetHeader("CF-IPCountry")
	}
	return attribution
}

type preparePurchaseRequest struct {
	ProductID  int64  `json:"product_id" binding:"gte=0"`
	SlotID     int    `json:"slot_id" binding:"gt=0"`
	NovelID    int64  `json:"novel_id" binding:"gte=0"`
	RequestKey string `json:"request_key" binding:"required,uuid"`
	ReturnPath string `json:"return_path" binding:"max=1024"`
}

func (h *Handler) PreparePurchase(c *gin.Context) {
	var req preparePurchaseRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.SlotID <= 0 {
		httpx.Fail(c, http.StatusBadRequest, "invalid_request", "Invalid checkout details", nil)
		return
	}
	intent, err := h.service.PreparePurchase(c.Request.Context(), c.GetInt64("user_id"), req.ProductID, req.SlotID, req.NovelID, req.RequestKey, req.ReturnPath, purchaseAttribution(c))
	writePurchase(c, intent, err)
}

func (h *Handler) StartPayPalCheckout(c *gin.Context) {
	intent, err := h.service.StartPayPalCheckout(c.Request.Context(), c.GetInt64("user_id"), c.Param("intent"), c.GetHeader("Origin"))
	writePurchase(c, intent, err)
}

func (h *Handler) StartStripeCheckout(c *gin.Context) {
	intent, err := h.service.StartStripeCheckout(c.Request.Context(), c.GetInt64("user_id"), c.Param("intent"), c.GetHeader("Origin"))
	writePurchase(c, intent, err)
}

func (h *Handler) GetPurchase(c *gin.Context) {
	intent, err := h.service.GetPurchase(c.Request.Context(), c.GetInt64("user_id"), c.Param("intent"), "")
	writePurchase(c, intent, err)
}

func (h *Handler) ReturnedPurchase(c *gin.Context) {
	intent, err := h.service.GetPurchase(c.Request.Context(), c.GetInt64("user_id"), c.Query("intent"), c.Query("token"))
	writePurchase(c, intent, err)
}

func (h *Handler) ReturnedVIPPurchase(c *gin.Context) {
	intent, err := h.service.GetVIPPurchase(c.Request.Context(), c.GetInt64("user_id"), c.Query("intent"), c.Query("token"))
	writePurchase(c, intent, err)
}

func (h *Handler) ListRecentPurchases(c *gin.Context) {
	intents, err := h.service.ListRecentPurchases(c.Request.Context(), c.GetInt64("user_id"))
	if err != nil {
		writePurchaseError(c, err)
		return
	}
	c.Header("Cache-Control", "private, no-store")
	c.JSON(http.StatusOK, gin.H{"purchases": intents})
}

func (h *Handler) GetPaymentMethods(c *gin.Context) {
	methods, err := h.service.GetPaymentMethods(c.Request.Context())
	if err != nil {
		writePurchaseError(c, err)
		return
	}
	c.Header("Cache-Control", "private, no-store")
	stripeSlots, err := h.service.GetStripeOfferSlots(c.Request.Context(), c.GetInt64("user_id"))
	if err != nil {
		writePurchaseError(c, err)
		return
	}
	vipSlots, err := h.service.GetStripeVIPOfferSlots(c.Request.Context(), c.GetInt64("user_id"))
	if err != nil {
		writePurchaseError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"paypal_single":    methods.PayPalSingle,
		"paypal_vip":       methods.PayPalVIP,
		"stripe_single":    methods.StripeSingle,
		"stripe_vip":       methods.StripeVIP,
		"environment":      methods.Environment,
		"stripe_slots":     stripeSlots,
		"stripe_vip_slots": vipSlots,
		"stripe_sandbox":   conversion.Environment() == "sandbox",
	})
}

func writePurchase(c *gin.Context, intent *PurchaseIntent, err error) {
	if err != nil {
		writePurchaseError(c, err)
		return
	}
	c.Header("Cache-Control", "private, no-store")
	c.JSON(http.StatusOK, intent)
}

func writePurchaseError(c *gin.Context, err error) {
	var planSwitchErr *VIPPlanSwitchBlockedError
	switch {
	case errors.As(err, &planSwitchErr):
		c.AbortWithStatusJSON(http.StatusConflict, gin.H{
			"code":  planSwitchErr.Code,
			"error": planSwitchErr.Message,
			"current_intent": gin.H{
				"intent_id":  planSwitchErr.CurrentIntentID,
				"slot_id":    planSwitchErr.CurrentSlotID,
				"product_id": planSwitchErr.CurrentProduct,
				"cycle":      planSwitchErr.CurrentCycle,
				"name":       planSwitchErr.CurrentName,
			},
		})
	case errors.Is(err, ErrCheckoutUnavailable):
		httpx.Fail(c, http.StatusNotFound, "resource_unavailable", "Checkout unavailable for this account", nil)
	case errors.Is(err, ErrCheckoutKeyConflict):
		httpx.Fail(c, http.StatusConflict, "request_key_conflict", err.Error(), nil)
	case errors.Is(err, ErrCheckoutConflict):
		httpx.Fail(c, http.StatusConflict, "checkout_exists", err.Error(), nil)
	case errors.Is(err, ErrSubscriptionConflict), errors.Is(err, ErrSubscriptionPending), errors.Is(err, ErrSubscriptionReview):
		code, message := publicSubscriptionError(err)
		httpx.Fail(c, http.StatusConflict, code, message, nil)
	case errors.Is(err, ErrVIPQuoteChanged):
		httpx.Fail(c, http.StatusConflict, "vip_quote_changed", ErrVIPQuoteChanged.Error(), nil)
	case errors.Is(err, ErrCheckoutPending):
		httpx.Fail(c, http.StatusConflict, "checkout_pending", err.Error(), nil)
	case errors.Is(err, ErrCheckoutPaused):
		httpx.Fail(c, http.StatusServiceUnavailable, "checkout_paused", err.Error(), nil)
	default:
		httpx.Fail(c, http.StatusServiceUnavailable, "checkout_unavailable", "PayPal checkout unavailable", err)
	}
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
		code := captureOutcomeFor(err)
		httpx.Fail(c, http.StatusBadGateway, string(code), code.publicMessage(), nil)
		return
	}
	c.JSON(http.StatusOK, result)
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
	ReturnPath    string `json:"return_path" binding:"max=1024"`
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

	res, err := h.service.CreateSubscription(ctx, userID, provider, req.SlotID, req.ReturnURL, req.CancelURL, req.ReturnPath, fbp, fbc, pixelID, ip, ua, sourceURL, country)
	if errors.Is(err, ErrCheckoutPaused) {
		httpx.Fail(c, http.StatusServiceUnavailable, "checkout_paused", err.Error(), nil)
		return
	}
	if errors.Is(err, ErrSubscriptionConflict) || errors.Is(err, ErrSubscriptionPending) || errors.Is(err, ErrSubscriptionReview) || errors.Is(err, ErrCheckoutUnavailable) {
		code, message := publicSubscriptionError(err)
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
	overallStatus := SubscriptionStatusNone
	if len(subscriptions) > 0 {
		sub = &subscriptions[0]
		if sub.NormalizedStatus != "" {
			overallStatus = sub.NormalizedStatus
		}
	}

	vip, err := h.service.HasSubscriptionAccess(ctx, userID)
	if err != nil {
		c.AbortWithStatus(503)
		return
	}
	c.Header("Cache-Control", "private, no-store")
	c.JSON(http.StatusOK, gin.H{
		"is_vip":        vip,
		"status":        overallStatus,
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
	result, err := h.service.CancelSubscriptionWithResult(ctx, userID, req.SubscriptionID, req.Reason)
	if errors.Is(err, ErrCheckoutUnavailable) {
		c.JSON(http.StatusNotFound, gin.H{"error": "Subscription unavailable"})
		return
	}
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Cancellation unavailable"})
		return
	}

	c.Header("Cache-Control", "private, no-store")
	c.JSON(http.StatusAccepted, result)
}

func (h *Handler) SubscriptionWebhook(c *gin.Context) {
	provider := c.Param("provider")
	if provider == "" {
		provider = "paypal"
	}

	h.handleProviderWebhook(c, provider)
}

func (h *Handler) StripeAftercareWebhook(c *gin.Context) {
	h.handleProviderWebhook(c, payment.ProviderStripe)
}

func (h *Handler) ChannelTokenWebhook(c *gin.Context) {
	payload, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to read body"})
		return
	}
	headers := make(map[string]string, len(c.Request.Header))
	for k, v := range c.Request.Header {
		if len(v) > 0 {
			headers[k] = v[0]
		}
	}
	if err := h.service.ProcessChannelWebhook(c.Request.Context(), c.Param("token"), payload, headers); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Payment event could not be processed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"received": true})
}

func (h *Handler) handleProviderWebhook(c *gin.Context, provider string) {

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

func publicSubscriptionError(err error) (string, string) {
	for _, rule := range []struct {
		cause error
		code  string
	}{
		{ErrSubscriptionConflict, "subscription_conflict"},
		{ErrSubscriptionPending, "subscription_pending"}, {ErrSubscriptionReview, "subscription_review"}, {ErrCheckoutUnavailable, "checkout_unavailable"},
	} {
		if errors.Is(err, rule.cause) {
			return rule.code, rule.cause.Error()
		}
	}
	return "wallet_unavailable", "Wallet unavailable"
}
