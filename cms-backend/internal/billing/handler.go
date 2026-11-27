package billing

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"star-novel-cms/internal/auth"
)

type Handler struct {
	service BillingService
}

func NewHandler(service BillingService) *Handler {
	return &Handler{
		service: service,
	}
}

func noStoreHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "private, no-store")
		c.Next()
	}
}

func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	ordersRead := auth.RequirePermission(auth.PermPaymentsOrdersRead)
	ordersWrite := auth.RequirePermission(auth.PermPaymentsOrdersWrite)
	subsRead := auth.RequirePermission(auth.PermPaymentsSubscriptionsRead)
	refundsRead := auth.RequirePermission(auth.PermPaymentsRefundsRead)
	refundsWrite := auth.RequirePermission(auth.PermPaymentsRefundsWrite)
	channelsRead := auth.RequirePermission(auth.PermPaymentsChannelsRead)
	channelsWrite := auth.RequirePermission(auth.PermPaymentsChannelsWrite)
	productsRead := auth.RequirePermission(auth.PermPaymentsProductsRead)
	productsWrite := auth.RequirePermission(auth.PermPaymentsProductsWrite)
	productBindingsWrite := auth.RequireAllPermissions(auth.PermPaymentsProductsWrite, auth.PermPaymentsChannelsWrite)
	templatesRead := auth.RequirePermission(auth.PermAdsTemplatesRead)
	templatesWrite := auth.RequirePermission(auth.PermAdsTemplatesWrite)

	orders := rg.Group("/orders", auth.AuthMiddleware(), noStoreHeaders())
	{
		orders.GET("", ordersRead, h.ListOrders)
		orders.GET("/:id/payment-details", ordersRead, h.GetThirdPartyPaymentDetails)
		orders.POST("/:id/refund", refundsWrite, h.RefundOrder)
	}
	payments := rg.Group("/payments", auth.AuthMiddleware(), noStoreHeaders())
	h.registerReconciliationRoutes(payments)
	h.registerAftercareRoutes(payments)
	{
		payments.GET("/orders", ordersRead, h.ListOrders)
		payments.GET("/accounts", auth.RequireAnyPermission(auth.AllPaymentReadPermissions...), h.listPaymentAccountOptions)
		payments.GET("/orders/:id/payment-details", ordersRead, h.GetThirdPartyPaymentDetails)
		payments.GET("/orders/:id/receipts", auth.RequireAnyPermission(auth.PermPaymentsOrdersRead, auth.PermPaymentsRefundsRead), h.ListPaymentReceipts)
		payments.GET("/orders/:id/checkout-evidence", ordersRead, h.getCheckoutEvidence)
		payments.GET("/subscriptions", subsRead, h.ListPaymentSubscriptions)
		payments.GET("/subscriptions/:id/stripe-periods", subsRead, h.listStripeVIPPeriods)
		payments.GET("/subscriptions/:id/paypal-periods", subsRead, h.listPayPalPeriods)
		payments.GET("/orders/:id/stripe-market-review", ordersRead, h.getStripeMarketReview)
		payments.POST("/orders/:id/stripe-market-review", ordersWrite, h.approveStripeMarketReview)
		payments.GET("/channels", channelsRead, h.ListPaymentChannels)
		payments.POST("/channels", channelsWrite, h.CreatePaymentChannel)
		payments.PATCH("/channels/:id", channelsWrite, h.EditPaymentChannel)
		payments.POST("/channels/:id/validate", channelsWrite, h.ValidatePaymentChannel)
		payments.POST("/channels/:id/publish", channelsWrite, h.PublishPaymentChannel)
		payments.POST("/channels/:id/deactivate", channelsWrite, h.DeactivatePaymentChannel)
		payments.DELETE("/channels/:id", channelsWrite, h.DeletePaymentChannel)
		payments.PUT("/channels/:id", channelsWrite, h.UpdatePaymentChannel)
		payments.GET("/channels/:id/aftercare-policy", channelsRead, h.getAftercarePolicy)
		payments.PUT("/channels/:id/aftercare-policy", channelsWrite, h.updateAftercarePolicy)
		payments.POST("/refunds", refundsWrite, h.SubmitRefundCommand)
		payments.GET("/refunds", refundsRead, h.ListRefundCommands)
		payments.GET("/refunds/queue", refundsRead, h.ListRefundQueue)
		payments.GET("/refunds/request/:key", refundsRead, h.GetRefundCommand)
		payments.GET("/refunds/:id", refundsRead, h.GetRefundCommand)
		payments.POST("/refunds/:id/reconcile-paypal", refundsWrite, h.ReconcilePayPalRefund)
	}

	products := rg.Group("/products", auth.AuthMiddleware(), noStoreHeaders())
	{
		products.GET("", productsRead, h.ListProducts)
		products.GET("/:id", productsRead, h.GetProduct)
		products.POST("", productsWrite, h.CreateProduct)
		products.PUT("/:id", productsWrite, h.UpdateProduct)
		products.DELETE("/:id", productsWrite, h.DeleteProduct)
		products.POST("/:id/copy", productsWrite, h.CopyProduct)
		products.PUT("/:id/bindings/paypal", productBindingsWrite, h.UpsertProductPayPalBinding)
		products.PUT("/:id/bindings/stripe", productBindingsWrite, h.UpsertProductStripeBinding)
		products.POST("/:id/publish", productsWrite, h.PublishProduct)
		products.POST("/:id/pause", productsWrite, h.PauseProduct)
		products.POST("/:id/retire", productsWrite, h.RetireProduct)
	}

	templates := rg.Group("/recharge-templates", auth.AuthMiddleware(), noStoreHeaders())
	{
		templates.GET("", templatesRead, h.ListRechargeTemplates)
		templates.GET("/product-options", templatesRead, h.ListTemplateProductOptions)
		templates.POST("", templatesWrite, h.CreateRechargeTemplate)
		templates.PUT("/:id", templatesWrite, h.UpdateRechargeTemplate)
		templates.DELETE("/:id", templatesWrite, h.DeleteRechargeTemplate)
		templates.POST("/:id/set-default", templatesWrite, h.SetDefaultRechargeTemplate)
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
	orderID := c.Query("order_id")
	var scope reconciliationScope
	if c.ShouldBindQuery(&scope) != nil || page < 1 || page > 1000000 || pageSize < 1 || pageSize > 100 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid payment order filters"})
		return
	}
	for _, id := range []string{orderID, userID} {
		if id != "" {
			if n, err := strconv.ParseInt(id, 10, 64); err != nil || n <= 0 {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid order or user number"})
				return
			}
		}
	}

	ctx := c.Request.Context()
	orders, total, err := h.service.ListOrders(ctx, page, pageSize, status, userID, orderType, promotionLinkID, paidStart, paidEnd, orderID, scope)
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
	orderID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || orderID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid order number"})
		return
	}
	command, err := submitRefundCommand(c.Request.Context(), c.GetString("admin_id"), refundCommandInput{}, orderID)
	if err != nil {
		writeRefundCommandError(c, err)
		return
	}
	if command.Status == "succeeded" {
		c.JSON(http.StatusOK, gin.H{"message": "Refund confirmed", "command": command})
		return
	}
	c.JSON(http.StatusConflict, gin.H{"error": "Check the refund operation for its confirmed result", "command": command})
}

func (h *Handler) SubmitRefundCommand(c *gin.Context) {
	var req refundCommandInput
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid refund parameters"})
		return
	}
	command, err := submitRefundCommand(c.Request.Context(), c.GetString("admin_id"), req, 0)
	if err != nil {
		writeRefundCommandError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, command)
}

func (h *Handler) ReconcilePayPalRefund(c *gin.Context) {
	var input struct {
		RefundRef string `json:"refund_ref" binding:"required,max=100"`
		Reason    string `json:"reason" binding:"required,max=500"`
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil || c.ShouldBindJSON(&input) != nil || strings.TrimSpace(input.RefundRef) == "" || strings.TrimSpace(input.Reason) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Refund reference and reconciliation evidence are required"})
		return
	}
	command, err := linkConfirmedPayPalRefund(c.Request.Context(), c.GetString("admin_id"), id.String(),
		strings.TrimSpace(input.RefundRef), strings.TrimSpace(input.Reason))
	if err != nil {
		writeRefundCommandError(c, err)
		return
	}
	c.JSON(http.StatusOK, command)
}

func (h *Handler) GetRefundCommand(c *gin.Context) {
	id, key := c.Param("id"), c.Param("key")
	ref := id
	if ref == "" {
		ref = key
	}
	if n, err := uuid.Parse(ref); err != nil || n == uuid.Nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid refund reference"})
		return
	}
	command, err := findRefundCommand(c.Request.Context(), id, c.GetString("admin_id"), key)
	if err != nil {
		writeRefundCommandError(c, err)
		return
	}
	c.JSON(http.StatusOK, command)
}

func (h *Handler) ListRefundCommands(c *gin.Context) {
	receiptID, err := strconv.ParseInt(c.Query("receipt_id"), 10, 64)
	if err != nil || receiptID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Provide the payment receipt number"})
		return
	}
	commands, err := refundCommandsForReceipt(c.Request.Context(), receiptID)
	if err != nil {
		writeRefundCommandError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"refunds": commands})
}

func (h *Handler) ListRefundQueue(c *gin.Context) {
	var filter refundQueueFilter
	if c.ShouldBindQuery(&filter) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid refund queue filters"})
		return
	}
	result, err := listRefundQueue(c.Request.Context(), filter)
	writeCaseResult(c, result, err)
}

func writeRefundCommandError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrRefundForbidden):
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
	case errors.Is(err, pgx.ErrNoRows):
		c.JSON(http.StatusNotFound, gin.H{"error": "Refund operation not found"})
	case errors.Is(err, ErrRefundKeyConflict), errors.Is(err, ErrRefundAmountUnavailable), errors.Is(err, ErrOrderAlreadyRefunded):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, ErrOrderNotRefundable):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Refund operation unavailable"})
	}
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
	details.RawPayload = ""

	c.JSON(http.StatusOK, gin.H{
		"found":   true,
		"details": details,
	})
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
	SlotIndex     int    `json:"slot_index" binding:"gte=1,lte=8"`
	ProductID     int64  `json:"product_id" binding:"gt=0"`
	Badge         string `json:"badge" binding:"max=32"`
	IsHighlighted bool   `json:"is_highlighted"`
	DisplayAlias  string `json:"display_alias" binding:"max=64"`

	// Explicit commercial override fields are forbidden in templates (CAT-03).
	Type              *string `json:"type"`
	Coins             *int    `json:"coins"`
	Bonus             *int    `json:"bonus"`
	VipDuration       *string `json:"vip_duration"`
	SubscriptionCycle *string `json:"subscription_cycle"`
	VipCycle          *string `json:"vip_cycle"`
	VipName           *string `json:"vip_name"`
	VipDesc           *string `json:"vip_desc"`
	Price             *string `json:"price"`
	PriceCents        *int    `json:"price_cents"`
	Currency          *string `json:"currency"`
}

func (s SlotReq) hasCommercialOverride() bool {
	return s.Type != nil || s.Coins != nil || s.Bonus != nil || s.VipDuration != nil ||
		s.SubscriptionCycle != nil || s.VipCycle != nil || s.VipName != nil || s.VipDesc != nil ||
		s.Price != nil || s.PriceCents != nil || s.Currency != nil
}

type SaveTemplateReq struct {
	Name        string    `json:"name" binding:"required"`
	Environment string    `json:"environment" binding:"omitempty,oneof=sandbox live"`
	IsDefault   bool      `json:"is_default"`
	Slots       []SlotReq `json:"slots" binding:"required"`
}

func buildTemplateSlots(reqs []SlotReq) ([]RechargeSlot, bool) {
	slots := make([]RechargeSlot, len(reqs))
	for i, s := range reqs {
		if s.hasCommercialOverride() {
			return nil, true
		}
		slots[i] = RechargeSlot{
			SlotIndex:     s.SlotIndex,
			ProductID:     s.ProductID,
			Badge:         s.Badge,
			IsHighlighted: s.IsHighlighted,
			DisplayAlias:  s.DisplayAlias,
		}
	}
	return slots, false
}

func writeTemplateSaveError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrRefundForbidden):
		c.JSON(http.StatusForbidden, gin.H{"error": "Recharge template write permission required"})
	case errors.Is(err, ErrTemplateSlotCountInvalid), errors.Is(err, ErrTemplateSlotBonusTooHigh), errors.Is(err, ErrTemplateSlotInvalid):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case errors.Is(err, ErrTemplateProductNotPublished), errors.Is(err, ErrCannotUnsetLastDefaultTemplate), errors.Is(err, ErrTemplateReferenced), errors.Is(err, ErrTemplateEnvPolicy):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, ErrTemplateNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}

func (h *Handler) CreateRechargeTemplate(c *gin.Context) {
	var req SaveTemplateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	slots, forbiddenOverride := buildTemplateSlots(req.Slots)
	if forbiddenOverride {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Templates cannot store or override product commercial fields (price, coins, bonus, cycle, type); create or copy a product instead"})
		return
	}

	ctx := c.Request.Context()
	templateID, err := h.service.CreateRechargeTemplate(ctx, c.GetString("admin_id"), req.Name, req.Environment, req.IsDefault, slots)
	if err != nil {
		writeTemplateSaveError(c, err)
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

	slots, forbiddenOverride := buildTemplateSlots(req.Slots)
	if forbiddenOverride {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Templates cannot store or override product commercial fields (price, coins, bonus, cycle, type); create or copy a product instead"})
		return
	}

	ctx := c.Request.Context()
	err = h.service.UpdateRechargeTemplate(ctx, c.GetString("admin_id"), id, req.Name, req.Environment, req.IsDefault, slots)
	if err != nil {
		writeTemplateSaveError(c, err)
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
	err = h.service.DeleteRechargeTemplate(ctx, c.GetString("admin_id"), id)
	if err != nil {
		if errors.Is(err, ErrRefundForbidden) {
			c.JSON(http.StatusForbidden, gin.H{"error": "Recharge template write permission required"})
			return
		}
		if errors.Is(err, ErrTemplateReferenced) || errors.Is(err, ErrDefaultTemplateNotDelete) {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		if errors.Is(err, ErrTemplateNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
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
	err = h.service.SetDefaultRechargeTemplate(ctx, c.GetString("admin_id"), id)
	if err != nil {
		if errors.Is(err, ErrRefundForbidden) {
			c.JSON(http.StatusForbidden, gin.H{"error": "Recharge template write permission required"})
			return
		}
		if errors.Is(err, ErrTemplateEnvPolicy) {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		if errors.Is(err, ErrTemplateNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "已成功设为默认模版"})
}
