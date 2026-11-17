package feedback

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"reader-backend/internal/auth"
	"reader-backend/internal/httpx"
)

type Handler struct {
	service Service
}

func NewHandler(service Service) *Handler {
	return &Handler{service: service}
}

func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/feedback", auth.OptionalAuthMiddleware(), h.SubmitFeedback)
}

func (h *Handler) SubmitFeedback(c *gin.Context) {
	var req CreateFeedbackRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, http.StatusBadRequest, "invalid_request", "Invalid request fields", nil)
		return
	}

	if strings.TrimSpace(req.Content) == "" {
		c.JSON(400, gin.H{"error": "Feedback content is required"})
		return
	}
	var userID int64 = 0
	if val, exists := c.Get("user_id"); exists {
		if uid, ok := val.(int64); ok {
			userID = uid
		}
	}

	id, err := h.service.SubmitFeedback(c.Request.Context(), userID, &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to submit feedback"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Feedback submitted successfully",
		"id":      id,
	})
}
