package shelf

import (
	"net/http"

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
	shelfGroup := r.Group("/shelf", auth.AuthMiddleware())
	{
		shelfGroup.GET("", h.GetShelf)
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

type AddRequest struct {
	NovelID int64 `json:"novel_id" binding:"required"`
}

func (h *Handler) AddToShelf(c *gin.Context) {
	userID := c.MustGet("user_id").(int64)
	var req AddRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()
	err := h.service.AddToShelf(ctx, userID, req.NovelID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to add novel to bookshelf"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Novel added to bookshelf successfully"})
}

type RemoveRequest struct {
	NovelIDs []int64 `json:"novel_ids" binding:"required"`
}

func (h *Handler) RemoveFromShelf(c *gin.Context) {
	userID := c.MustGet("user_id").(int64)
	var req RemoveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()
	err := h.service.RemoveFromShelf(ctx, userID, req.NovelIDs)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to remove novels from bookshelf"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Novels removed from bookshelf successfully"})
}

type SyncRequest struct {
	ProgressUpdates []ProgressUpdate `json:"progress_updates" binding:"required,dive"`
}

func (h *Handler) SyncProgress(c *gin.Context) {
	userID := c.MustGet("user_id").(int64)
	var req SyncRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()
	err := h.service.SyncProgress(ctx, userID, req.ProgressUpdates)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to sync reading progress"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Reading progress synchronized successfully"})
}
