package shelf

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"reader-backend/internal/auth"
	"reader-backend/internal/httpx"
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
	shelfGroup := r.Group("/shelf", auth.AuthMiddleware())
	{
		shelfGroup.GET("", h.GetShelf)
		shelfGroup.GET("/cursor", h.GetShelfPage)
		shelfGroup.GET("/ids", h.GetShelfIDs)
		shelfGroup.GET("/progress/:novel_id", h.GetProgress)
		shelfGroup.POST("/add", h.AddToShelf)
		shelfGroup.POST("/remove", h.RemoveFromShelf)
		shelfGroup.POST("/sync", h.SyncProgress)
	}
}

func (h *Handler) GetShelf(c *gin.Context) {
	userID := c.MustGet("user_id").(int64)
	ctx := c.Request.Context()

	items, err := h.service.GetShelf(ctx, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch bookshelf items"})
		return
	}

	c.JSON(http.StatusOK, items)
}

func (h *Handler) GetProgress(c *gin.Context) {
	userID := c.MustGet("user_id").(int64)
	if !verifyShelfAccount(c, userID) {
		return
	}
	novelID, err := strconv.ParseInt(c.Param("novel_id"), 10, 32)
	if err != nil || novelID <= 0 {
		httpx.Fail(c, http.StatusBadRequest, "invalid_novel_id", "Invalid novel ID", nil)
		return
	}
	progress, err := h.service.GetProgress(c.Request.Context(), userID, novelID)
	if err != nil {
		httpx.Fail(c, http.StatusInternalServerError, "internal_error", "Failed to load reading progress", err)
		return
	}
	c.JSON(http.StatusOK, progress)
}

func verifyShelfAccount(c *gin.Context, userID int64) bool {
	c.Header("Cache-Control", "private, no-store")
	if c.Query("user_id") != strconv.FormatInt(userID, 10) {
		httpx.Fail(c, http.StatusConflict, "account_changed", "Reading library belongs to a different account", nil)
		return false
	}
	return true
}

func (h *Handler) GetShelfPage(c *gin.Context) {
	userID := c.MustGet("user_id").(int64)
	if !verifyShelfAccount(c, userID) {
		return
	}
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if err != nil || limit < 1 || limit > httpx.MaxPageSize {
		httpx.Fail(c, http.StatusBadRequest, "invalid_pagination", "Page size must be 1–100", nil)
		return
	}
	after, err := parseShelfCursor(c.Query("cursor"))
	if err != nil {
		httpx.Fail(c, http.StatusBadRequest, "invalid_cursor", "Invalid library cursor", nil)
		return
	}
	page, err := h.service.GetShelfPage(c.Request.Context(), userID, limit, after)
	if err != nil {
		httpx.Fail(c, http.StatusInternalServerError, "internal_error", "Failed to load library", err)
		return
	}
	c.JSON(http.StatusOK, page)
}

func (h *Handler) GetShelfIDs(c *gin.Context) {
	userID := c.MustGet("user_id").(int64)
	if !verifyShelfAccount(c, userID) {
		return
	}
	ids, err := h.service.GetShelfIDs(c.Request.Context(), userID)
	if err != nil {
		httpx.Fail(c, http.StatusInternalServerError, "internal_error", "Failed to load library", err)
		return
	}
	c.JSON(http.StatusOK, ids)
}

type AddRequest struct {
	NovelID int64 `json:"novel_id" binding:"required,gt=0"`
}

func (h *Handler) AddToShelf(c *gin.Context) {
	userID := c.MustGet("user_id").(int64)
	var req AddRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, http.StatusBadRequest, "invalid_request", "Invalid request fields", nil)
		return
	}

	ctx := c.Request.Context()
	err := h.service.AddToShelf(ctx, userID, req.NovelID)
	if err != nil {
		if errors.Is(err, ErrNovelUnavailable) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Novel not found or unavailable"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to add novel to bookshelf"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Novel added to bookshelf successfully"})
}

type RemoveRequest struct {
	NovelIDs []int64 `json:"novel_ids" binding:"required,min=1,max=100,dive,gt=0"`
}

func (h *Handler) RemoveFromShelf(c *gin.Context) {
	userID := c.MustGet("user_id").(int64)
	var req RemoveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, http.StatusBadRequest, "invalid_request", "Invalid request fields", nil)
		return
	}

	ctx := c.Request.Context()
	err := h.service.RemoveFromShelf(ctx, userID, req.NovelIDs)
	if err != nil {
		if errors.Is(err, ErrInvalidBatch) {
			c.JSON(400, gin.H{"error": ErrInvalidBatch.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to remove novels from bookshelf"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Novels removed from bookshelf successfully"})
}

type SyncRequest struct {
	UserID          int64            `json:"user_id" binding:"required,gt=0"`
	ProgressUpdates []ProgressUpdate `json:"progress_updates" binding:"required,min=1,max=100,dive"`
}

func (h *Handler) SyncProgress(c *gin.Context) {
	userID := c.MustGet("user_id").(int64)
	var req SyncRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, http.StatusBadRequest, "invalid_request", "Invalid request fields", nil)
		return
	}

	if req.UserID != userID {
		httpx.Fail(c, http.StatusConflict, "account_changed", "Reading progress belongs to a different account", nil)
		return
	}

	ctx := c.Request.Context()
	err := h.service.SyncProgress(ctx, userID, req.ProgressUpdates)
	if err != nil {
		if errors.Is(err, ErrInvalidProgress) || errors.Is(err, ErrInvalidBatch) {
			if errors.Is(err, ErrInvalidBatch) {
				httpx.Fail(c, 400, "invalid_batch", ErrInvalidBatch.Error(), nil)
			} else {
				httpx.Fail(c, 400, "invalid_progress", ErrInvalidProgress.Error(), nil)
			}
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to sync reading progress"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Reading progress synchronized successfully"})
}
