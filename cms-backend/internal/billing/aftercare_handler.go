package billing

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"star-novel-cms/internal/auth"
)

func (h *Handler) registerAftercareRoutes(payments *gin.RouterGroup) {
	caseRead := auth.RequireAnyPermission(auth.PermPaymentsRiskRead, auth.PermPaymentsDisputesRead)
	caseWrite := auth.RequireAnyPermission(auth.PermPaymentsRiskWrite, auth.PermPaymentsDisputesWrite)
	ownersRead := auth.RequireAnyPermission(auth.PermPaymentsRiskRead, auth.PermPaymentsDisputesRead, auth.PermPaymentsChannelsRead)

	cases := payments.Group("/cases")
	cases.GET("", caseRead, h.listPaymentCases)
	cases.GET("/owners", ownersRead, h.listCaseOwners)
	cases.GET("/decisions", auth.RequirePermission(auth.PermPaymentsRiskRead), h.listCheckoutDecisions)
	cases.GET("/:id", caseRead, h.getPaymentCase)
	cases.POST("/:id/actions", caseWrite, h.actOnPaymentCase)
}

func resolvePrincipal(c *gin.Context) (*auth.Principal, error) {
	if p, ok := auth.PrincipalFromContext(c); ok && p != nil {
		return p, nil
	}
	return auth.LoadPrincipal(c.Request.Context(), c.GetString("admin_id"))
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
	principal, err := resolvePrincipal(c)
	if err != nil {
		if errors.Is(err, auth.ErrAccountInactive) {
			c.JSON(http.StatusUnauthorized, gin.H{"code": "account_inactive", "error": "Account is disabled or does not exist"})
			return
		}
		writeCaseResult(c, nil, err)
		return
	}
	requiredPerm := auth.PermPaymentsRiskRead
	if filter.View == "disputes" {
		requiredPerm = auth.PermPaymentsDisputesRead
	}
	if !principal.HasPermission(requiredPerm) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Payment case access denied"})
		return
	}
	if filter.Kind != "" {
		if kindPerm := caseReadPermission(filter.Kind); kindPerm != "" && !principal.HasPermission(kindPerm) {
			c.JSON(http.StatusForbidden, gin.H{"error": "Payment case access denied"})
			return
		}
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
	principal, err := resolvePrincipal(c)
	if err != nil {
		if errors.Is(err, auth.ErrAccountInactive) {
			c.JSON(http.StatusUnauthorized, gin.H{"code": "account_inactive", "error": "Account is disabled or does not exist"})
			return
		}
		writeCaseResult(c, nil, err)
		return
	}
	kind, value, err := getCase(c.Request.Context(), id)
	if err != nil {
		writeCaseResult(c, nil, err)
		return
	}
	reqPerm := caseReadPermission(kind)
	if reqPerm == "" || !principal.HasPermission(reqPerm) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Payment case access denied"})
		return
	}
	c.JSON(http.StatusOK, value)
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
	roleScope := strings.TrimSpace(c.Query("role_scope"))
	switch roleScope {
	case "", "risk", "disputes", "aftercare_finance":
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid role_scope"})
		return
	}
	data, err := listCaseOwnersByScope(c.Request.Context(), roleScope)
	writeCaseResult(c, data, err)
}
