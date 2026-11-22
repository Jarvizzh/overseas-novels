package wallet

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"reader-backend/internal/auth"
	"reader-backend/internal/httpx"
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
		walletGroup.GET("/rewards/checkin", h.GetCheckIn)
		walletGroup.POST("/rewards/checkin", h.DailyCheckIn)
	}
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
			httpx.Fail(c, 409, "price_changed", ErrPriceChanged.Error(), nil)
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
