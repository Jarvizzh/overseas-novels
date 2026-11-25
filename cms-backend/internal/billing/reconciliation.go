package billing

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"star-novel-cms/internal/auth"
)

const reconciliationCurrency = "USD"

type reconciliationScope struct {
	Environment string `form:"environment" binding:"omitempty,oneof=live sandbox"`
	Provider    string `form:"provider" binding:"omitempty,oneof=paypal stripe"`
	AccountID   int64  `form:"account_id" binding:"min=0"`
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
	MovementID     int64      `form:"movement_id" binding:"min=0"`
	OrderID        int64      `form:"order_id" binding:"min=0"`
	PayoutID       int64      `form:"payout_id" binding:"min=0"`
	ReviewRequired bool       `form:"review_required"`
	MovementKey    string     `form:"movement_key" binding:"max=256"`
	Kind           string     `form:"kind" binding:"omitempty,oneof=payment refund reversal dispute_debit dispute_credit fee tax fx payout hold release other"`
	From           *time.Time `form:"from" time_format:"2006-01-02" time_utc:"1"`
	Through        *time.Time `form:"through" time_format:"2006-01-02" time_utc:"1"`
}

type payoutFilter struct {
	reconciliationPage
	PayoutID        int64  `form:"payout_id" binding:"min=0"`
	PayoutRef       string `form:"payout_ref" binding:"max=256"`
	DifferencesOnly bool   `form:"differences_only"`
}

func (h *Handler) registerReconciliationRoutes(payments *gin.RouterGroup) {
	reconRead := auth.RequirePermission(auth.PermPaymentsReconciliationRead)
	reconWrite := auth.RequirePermission(auth.PermPaymentsReconciliationWrite)
	group := payments.Group("/reconciliation")
	group.GET("/movements", reconRead, reconciliationQuery(h.service.ListLedgerMovements))
	group.GET("/payouts", reconRead, reconciliationQuery(h.service.ListSettlementPayouts))
	group.GET("/report-issues", reconRead, reconciliationQuery(h.service.ListSettlementReportIssues))
	group.GET("/activity", reconRead, reconciliationQuery(h.service.GetPaymentActivity))
	group.GET("/accounts", reconRead, h.listSettlementAccounts)
	group.PUT("/accounts/:id/automatic", reconWrite, h.updateAutomaticLedger)
	group.GET("/commands", reconRead, h.listSettlementCommands)
	group.GET("/commands/request/:key", reconRead, h.getOwnSettlementCommand)
	group.GET("/commands/:id", reconRead, h.getSettlementCommand)
	group.POST("/commands", reconWrite, h.submitSettlementCommand)
}

var errReconciliationDateRange = errors.New("reconciliation dates are out of order")

func reconciliationQuery[T, R any](load func(context.Context, T) (R, error)) gin.HandlerFunc {
	return func(c *gin.Context) {
		var filter T
		if err := c.ShouldBindQuery(&filter); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid reconciliation filters"})
			return
		}
		data, err := load(c.Request.Context(), filter)
		if errors.Is(err, errReconciliationDateRange) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "开始日期不能晚于结束日期。"})
			return
		}
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
	if filter.From != nil && filter.Through != nil && filter.From.After(*filter.Through) {
		return movementPage{}, errReconciliationDateRange
	}
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
