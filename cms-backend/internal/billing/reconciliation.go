package billing

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
)

const reconciliationCurrency = "USD"

type reconciliationScope struct {
	Environment string `form:"environment" binding:"omitempty,oneof=live sandbox"`
	Provider    string `form:"provider" binding:"omitempty,oneof=paypal stripe"`
}

type reconciliationPage struct {
	reconciliationScope
	Page int `form:"page" binding:"omitempty,min=1,max=1000000"`
}

const reconciliationPageSize = 20

func (p reconciliationPage) offset() int {
	if p.Page == 0 {
		return 0
	}
	return (p.Page - 1) * reconciliationPageSize
}

type movementFilter struct {
	reconciliationPage
	MovementID     int64 `form:"movement_id" binding:"min=0"`
	OrderID        int64 `form:"order_id" binding:"min=0"`
	PayoutID       int64 `form:"payout_id" binding:"min=0"`
	ReviewRequired bool  `form:"review_required"`
}

type payoutFilter struct {
	reconciliationPage
	PayoutID        int64 `form:"payout_id" binding:"min=0"`
	DifferencesOnly bool  `form:"differences_only"`
}

func (h *Handler) registerReconciliationRoutes(payments *gin.RouterGroup, financeRoles gin.HandlerFunc) {
	group := payments.Group("/reconciliation", financeRoles)
	group.GET("/movements", reconciliationQuery(h.service.ListLedgerMovements))
	group.GET("/payouts", reconciliationQuery(h.service.ListSettlementPayouts))
	group.GET("/report-issues", reconciliationQuery(h.service.ListSettlementReportIssues))
	group.GET("/activity", reconciliationQuery(h.service.GetPaymentActivity))
	group.GET("/accounts", h.listSettlementAccounts)
	group.PUT("/accounts/:id/automatic", h.updateAutomaticLedger)
	group.GET("/commands", h.listSettlementCommands)
	group.POST("/commands", h.submitSettlementCommand)
}

func reconciliationQuery[T, R any](load func(context.Context, T) (R, error)) gin.HandlerFunc {
	return func(c *gin.Context) {
		var filter T
		if err := c.ShouldBindQuery(&filter); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid reconciliation filters"})
			return
		}
		data, err := load(c.Request.Context(), filter)
		if err != nil {
			slog.ErrorContext(c.Request.Context(), "read payment reconciliation", "path", c.FullPath(), "error", err)
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Payment reconciliation unavailable"})
			return
		}
		c.JSON(http.StatusOK, data)
	}
}

func (s reconciliationScope) environment() string {
	if s.Environment == "" {
		return "live"
	}
	return s.Environment
}

func (s *billingService) ListLedgerMovements(ctx context.Context, filter movementFilter) (movementPage, error) {
	return s.repo.ListLedgerMovements(ctx, filter)
}

func (s *billingService) ListSettlementPayouts(ctx context.Context, filter payoutFilter) (payoutPage, error) {
	return s.repo.ListSettlementPayouts(ctx, filter)
}

func (s *billingService) ListSettlementReportIssues(ctx context.Context, filter reconciliationPage) (reportIssuePage, error) {
	return s.repo.ListSettlementReportIssues(ctx, filter)
}

func (s *billingService) GetPaymentActivity(ctx context.Context, scope reconciliationScope) (paymentActivity, error) {
	return s.repo.GetPaymentActivity(ctx, scope)
}

func reconciliationResult[T any](data T, err error) (T, error) {
	if err != nil {
		return data, fmt.Errorf("query reconciliation: %w", err)
	}
	return data, nil
}
