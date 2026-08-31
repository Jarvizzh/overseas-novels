package billing

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

type Handler struct {
	service BillingService
}

func NewHandler(service BillingService) *Handler {
	return &Handler{
		service: service,
	}
}

func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	orders := rg.Group("/orders")
	{
		orders.GET("", h.ListOrders)
		orders.GET("/:id/payment-details", h.GetThirdPartyPaymentDetails)
		orders.POST("/:id/refund", h.RefundOrder)
		orders.POST("/mock-webhook", h.MockPaymentWebhook)
	}

	templates := rg.Group("/recharge-templates")
	{
		templates.GET("", h.ListRechargeTemplates)
		templates.POST("", h.CreateRechargeTemplate)
		templates.PUT("/:id", h.UpdateRechargeTemplate)
		templates.DELETE("/:id", h.DeleteRechargeTemplate)
		templates.POST("/:id/set-default", h.SetDefaultRechargeTemplate)
	}
}

func (h *Handler) ListOrders(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "10"))
	status := c.Query("status")
	userID := c.Query("user_id")
	orderType := c.Query("order_type")
	promotionLinkID := c.Query("promotion_link_id")
	paidStart := c.Query("paid_start")
	paidEnd := c.Query("paid_end")

	ctx := c.Request.Context()
	orders, total, err := h.service.ListOrders(ctx, page, pageSize, status, userID, orderType, promotionLinkID, paidStart, paidEnd)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to query orders: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"orders":    orders,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

func (h *Handler) RefundOrder(c *gin.Context) {
	orderID := c.Param("id")
	adminID, _ := c.Get("admin_id")

	ctx := c.Request.Context()
	err := h.service.RefundOrder(ctx, orderID, adminID.(string))
	if err != nil {
		if errors.Is(err, ErrOrderNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		if errors.Is(err, ErrOrderAlreadyRefunded) || errors.Is(err, ErrOrderNotRefundable) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Order successfully refunded. User wallet updated."})
}

func (h *Handler) GetThirdPartyPaymentDetails(c *gin.Context) {
	idStr := c.Param("id")
	orderID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid order ID"})
		return
	}

	ctx := c.Request.Context()
	details, err := h.service.GetThirdPartyPaymentDetails(ctx, orderID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch third-party payment details: " + err.Error()})
		return
	}

	if details == nil {
		c.JSON(http.StatusOK, gin.H{
			"found":   false,
			"details": nil,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"found":   true,
		"details": details,
	})
}

type MockWebhookReq struct {
	UserID        string `json:"user_id" binding:"required"`
	AmountCents   int    `json:"amount_cents" binding:"required"`
	ChargedCoins  int    `json:"charged_coins" binding:"required"`
	BonusCoins    int    `json:"bonus_coins"`
	PaymentMethod string `json:"payment_method" binding:"required,oneof=stripe paypal"`
	Status        string `json:"status" binding:"required,oneof=Paid Refunded"`
	UtmSource     string `json:"utm_source"`
	UtmCampaign   string `json:"utm_campaign"`
	ExternalRefID string `json:"external_ref_id"`
}

func (h *Handler) MockPaymentWebhook(c *gin.Context) {
	var req MockWebhookReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()
	err := h.service.MockPaymentWebhook(ctx, req.UserID, req.AmountCents, req.ChargedCoins, req.BonusCoins, req.PaymentMethod, req.Status, req.UtmSource, req.UtmCampaign, req.ExternalRefID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Mock payment webhook parsed successfully", "status": req.Status})
}

func (h *Handler) ListRechargeTemplates(c *gin.Context) {
	ctx := c.Request.Context()
	templates, err := h.service.ListRechargeTemplates(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询模板列表失败: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, templates)
}

type SlotReq struct {
	SlotIndex   int    `json:"slot_index" binding:"gte=0"`
	Type        string `json:"type" binding:"required"`
	Coins       int    `json:"coins"`
	Bonus       int    `json:"bonus"`
	VipDuration string `json:"vip_duration"`
	VipName     string `json:"vip_name"`
	VipDesc     string `json:"vip_desc"`
	Price       string `json:"price" binding:"required"`
	PriceCents  int    `json:"price_cents" binding:"required"`
}

type SaveTemplateReq struct {
	Name      string    `json:"name" binding:"required"`
	IsDefault bool      `json:"is_default"`
	Slots     []SlotReq `json:"slots" binding:"required"`
}

func (h *Handler) CreateRechargeTemplate(c *gin.Context) {
	var req SaveTemplateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	slots := make([]RechargeSlot, len(req.Slots))
	for i, s := range req.Slots {
		slots[i] = RechargeSlot{
			SlotIndex:   s.SlotIndex,
			Type:        s.Type,
			Coins:       s.Coins,
			Bonus:       s.Bonus,
			VipDuration: s.VipDuration,
			VipName:     s.VipName,
			VipDesc:     s.VipDesc,
			Price:       s.Price,
			PriceCents:  s.PriceCents,
		}
	}

	ctx := c.Request.Context()
	templateID, err := h.service.CreateRechargeTemplate(ctx, req.Name, req.IsDefault, slots)
	if err != nil {
		if errors.Is(err, ErrTemplateSlotCountInvalid) || errors.Is(err, ErrTemplateSlotBonusTooHigh) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "充值配置模板及卡位创建成功", "id": templateID})
}

func (h *Handler) UpdateRechargeTemplate(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的模板 ID"})
		return
	}

	var req SaveTemplateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	slots := make([]RechargeSlot, len(req.Slots))
	for i, s := range req.Slots {
		slots[i] = RechargeSlot{
			SlotIndex:   s.SlotIndex,
			Type:        s.Type,
			Coins:       s.Coins,
			Bonus:       s.Bonus,
			VipDuration: s.VipDuration,
			VipName:     s.VipName,
			VipDesc:     s.VipDesc,
			Price:       s.Price,
			PriceCents:  s.PriceCents,
		}
	}

	ctx := c.Request.Context()
	err = h.service.UpdateRechargeTemplate(ctx, id, req.Name, req.IsDefault, slots)
	if err != nil {
		if errors.Is(err, ErrTemplateSlotCountInvalid) || errors.Is(err, ErrTemplateSlotBonusTooHigh) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if errors.Is(err, ErrTemplateNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "模板及卡位更新成功"})
}

func (h *Handler) DeleteRechargeTemplate(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的模板 ID"})
		return
	}

	ctx := c.Request.Context()
	err = h.service.DeleteRechargeTemplate(ctx, id)
	if err != nil {
		if errors.Is(err, ErrDefaultTemplateNotDelete) || errors.Is(err, ErrTemplateNotFound) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "模板删除成功"})
}

func (h *Handler) SetDefaultRechargeTemplate(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的模板 ID"})
		return
	}

	ctx := c.Request.Context()
	err = h.service.SetDefaultRechargeTemplate(ctx, id)
	if err != nil {
		if errors.Is(err, ErrTemplateNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "已成功设为默认模版"})
}
