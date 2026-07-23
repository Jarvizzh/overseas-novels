package wallet

import (
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"

	"github.com/gin-gonic/gin"
	"novel-backend/internal/auth"
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
		walletGroup.POST("/recharge/paypal/capture", h.CapturePayPalPayment)
		walletGroup.POST("/rewards/checkin", h.DailyCheckIn)
		walletGroup.GET("/recharge/templates", h.GetRechargeTemplates)
	}

	r.POST("/wallet/webhook/stripe", h.StripeWebhook)
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
	ChapterIndex int   `json:"chapter_index" binding:"required"`
}

func (h *Handler) UnlockChapter(c *gin.Context) {
	userID := c.MustGet("user_id").(int64)
	var req UnlockRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()
	err := h.service.UnlockChapter(ctx, userID, req.NovelID, req.ChapterIndex)
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

	t, err := h.service.GetRechargeTemplates(ctx, templateIDHeader)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to query template slots: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, t)
}
