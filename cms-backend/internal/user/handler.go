package user

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"star-novel-cms/internal/auth"
)

type Handler struct {
	service UserService
}

func NewHandler(service UserService) *Handler {
	return &Handler{
		service: service,
	}
}

func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	users := rg.Group("/users", auth.AuthMiddleware())
	{
		users.GET("", h.ListUsers)
		users.GET("/:id", h.GetUserDetail)
		users.PUT("/:id/status", h.ToggleUserStatus)
		users.POST("/:id/adjust-wallet", h.AdjustWallet)
	}
}

func (h *Handler) ListUsers(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "10"))
	search := c.Query("search")
	statusQuery := c.Query("status")

	ctx := c.Request.Context()
	users, total, err := h.service.ListUsers(ctx, page, pageSize, search, statusQuery)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to query users: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"users":     users,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

func (h *Handler) GetUserDetail(c *gin.Context) {
	userIDStr := c.Param("id")
	userID, err := strconv.ParseInt(userIDStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID"})
		return
	}

	ctx := c.Request.Context()
	detail, err := h.service.GetUserDetail(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, detail)
}

type ToggleUserStatusReq struct {
	Status int16 `json:"status" binding:"required,oneof=1 2"` // 1-Normal, 2-Banned
}

func (h *Handler) ToggleUserStatus(c *gin.Context) {
	userIDStr := c.Param("id")
	userID, err := strconv.ParseInt(userIDStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID"})
		return
	}

	var req ToggleUserStatusReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var adminIDStr string
	if val, exists := c.Get("admin_id"); exists && val != nil {
		if s, ok := val.(string); ok {
			adminIDStr = s
		}
	}
	ctx := c.Request.Context()

	statusStr, err := h.service.ToggleUserStatus(ctx, userID, req.Status, adminIDStr)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": fmt.Sprintf("User status updated to %s", statusStr)})
}

type AdjustWalletReq struct {
	Amount  int    `json:"amount" binding:"required"` // Can be negative or positive
	IsBonus bool   `json:"is_bonus"`                  // Adjust charged coins vs bonus coins
	Reason  string `json:"reason" binding:"required"`
}

func (h *Handler) AdjustWallet(c *gin.Context) {
	userIDStr := c.Param("id")
	userID, err := strconv.ParseInt(userIDStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID"})
		return
	}

	var req AdjustWalletReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var adminIDStr string
	if val, exists := c.Get("admin_id"); exists && val != nil {
		if s, ok := val.(string); ok {
			adminIDStr = s
		}
	}
	ctx := c.Request.Context()

	err = h.service.AdjustWallet(ctx, userID, req.Amount, req.IsBonus, req.Reason, adminIDStr)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "User wallet adjusted successfully"})
}
