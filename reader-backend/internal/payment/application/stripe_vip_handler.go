package application

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"reader-backend/internal/httpx"
)

func (h *Handler) StartStripeVIP(c *gin.Context) {
	var req struct {
		SlotID     int    `json:"slot_id" binding:"gt=0"`
		ReturnPath string `json:"return_path" binding:"max=1024"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, http.StatusBadRequest, "invalid_request", "Invalid VIP checkout details", nil)
		return
	}
	intent, err := h.service.StartStripeVIP(c.Request.Context(), c.GetInt64("user_id"), req.SlotID, c.GetHeader("Origin"), req.ReturnPath, purchaseAttribution(c))
	writePurchase(c, intent, err)
}

func (h *Handler) GetStripeVIPPurchase(c *gin.Context) {
	intent, err := h.service.GetStripeVIPPurchase(c.Request.Context(), c.GetInt64("user_id"), c.Param("intent"))
	writePurchase(c, intent, err)
}

func (h *Handler) OpenStripeVIPPortal(c *gin.Context) {
	var req struct {
		SubscriptionID string `json:"subscription_id" binding:"required,max=100"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, http.StatusBadRequest, "invalid_request", "Invalid subscription", nil)
		return
	}
	portal, err := h.service.OpenStripeVIPPortal(c.Request.Context(), c.GetInt64("user_id"), req.SubscriptionID, c.GetHeader("Origin"))
	if err != nil {
		writePurchaseError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"portal_url": portal})
}
