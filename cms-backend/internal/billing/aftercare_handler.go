package billing

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (h *Handler) registerAftercareRoutes(payments *gin.RouterGroup, role gin.HandlerFunc) {
	cases := payments.Group("/cases", role)
	cases.GET("", h.listPaymentCases)
	cases.GET("/owners", h.listCaseOwners)
	cases.GET("/decisions", h.listCheckoutDecisions)
	cases.GET("/:id", h.getPaymentCase)
	cases.POST("/:id/actions", h.actOnPaymentCase)
}

type caseFilter struct {
	reconciliationScope
	View       string `form:"view" binding:"required,oneof=risk disputes"`
	Kind       string `form:"kind" binding:"omitempty,oneof=risk_review risk_signal dispute reversal"`
	Owner      string `form:"owner_id" binding:"max=36"`
	OrderID    int64  `form:"order_id" binding:"min=0"`
	Receipt    string `form:"receipt" binding:"max=128"`
	ExternalID string `form:"external_id" binding:"max=128"`
	Before     int64  `form:"before" binding:"min=0"`
	Attention  bool   `form:"attention"`
	WorkState  string `form:"work_state" binding:"omitempty,oneof=observed pending approved refund_due dispute_due closed"`
	Deadline   string `form:"deadline" binding:"omitempty,oneof=overdue upcoming"`
}

func (h *Handler) listPaymentCases(c *gin.Context) {
	var filter caseFilter
	if c.ShouldBindQuery(&filter) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid payment case filters"})
		return
	}
	value, err := listCases(c.Request.Context(), filter)
	writeCaseResult(c, value, err)
}

func (h *Handler) getPaymentCase(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid payment case ID"})
		return
	}
	value, err := getCase(c.Request.Context(), id)
	writeCaseResult(c, value, err)
}

type caseAction struct {
	RequestID   uuid.UUID `json:"request_id" binding:"required"`
	Action      string    `json:"action" binding:"required,oneof=assign approve reject apply_loss restore"`
	Owner       string    `json:"owner" binding:"max=36"`
	Reason      string    `json:"reason" binding:"required,max=500"`
	EvidenceKey string    `json:"evidence_key" binding:"max=32"`
}

func (h *Handler) actOnPaymentCase(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	var input caseAction
	if err != nil || id <= 0 || c.ShouldBindJSON(&input) != nil || input.RequestID == uuid.Nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid payment case action"})
		return
	}
	err = applyCaseAction(c.Request.Context(), c.GetString("admin_id"), id, input)
	writeCaseResult(c, gin.H{"accepted": true}, err)
}

func writeCaseResult(c *gin.Context, value any, err error) {
	switch {
	case err == nil:
		c.JSON(http.StatusOK, value)
	case errors.Is(err, pgx.ErrNoRows):
		c.JSON(http.StatusNotFound, gin.H{"error": "Payment case not found"})
	case errors.Is(err, errCaseConflict):
		c.JSON(http.StatusConflict, gin.H{"error": "The action no longer matches the current evidence or case state"})
	case errors.Is(err, errStripeCatalog):
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
	case errors.Is(err, errCaseForbidden):
		c.JSON(http.StatusForbidden, gin.H{"error": "Payment case action denied"})
	default:
		slog.ErrorContext(c.Request.Context(), "payment case operation", "path", c.FullPath(), "error", err)
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Payment case operation unavailable"})
	}
}

func (h *Handler) listCheckoutDecisions(c *gin.Context) {
	var filter struct {
		OrderID     int64  `form:"order_id" binding:"min=0"`
		Before      int64  `form:"before" binding:"min=0"`
		Environment string `form:"checkout_environment" binding:"max=60"`
		Provider    string `form:"provider" binding:"omitempty,oneof=paypal stripe"`
	}
	if c.ShouldBindQuery(&filter) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid checkout decision filters"})
		return
	}
	data, err := listDecisions(c.Request.Context(), filter.OrderID, filter.Before, filter.Environment, filter.Provider)
	writeCaseResult(c, data, err)
}

func (h *Handler) listCaseOwners(c *gin.Context) {
	data, err := listFinanceOwners(c.Request.Context())
	writeCaseResult(c, data, err)
}
