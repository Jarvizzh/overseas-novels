package billing

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"star-novel-cms/internal/db"
)

type aftercarePolicy struct {
	DisputeLookup   bool   `json:"dispute_lookup"`
	HoldFulfillment bool   `json:"hold_fulfillment"`
	ReviewerID      string `json:"reviewer_id" binding:"max=36"`
	FinanceID       string `json:"finance_id" binding:"max=36"`
	ReviewSeconds   *int   `json:"review_seconds" binding:"omitempty,min=1"`
	RefundSeconds   *int   `json:"refund_seconds" binding:"omitempty,min=1"`
	ReminderSeconds *int   `json:"reminder_seconds" binding:"omitempty,min=1"`
	Reason          string `json:"reason" binding:"required,max=500"`
}

func (h *Handler) updateAftercarePolicy(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	var input aftercarePolicy
	if err != nil || id <= 0 || c.ShouldBindJSON(&input) != nil ||
		input.HoldFulfillment && (input.ReviewerID == "" || input.FinanceID == "" || input.ReviewSeconds == nil || input.RefundSeconds == nil) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid aftercare policy"})
		return
	}
	err = saveAftercarePolicy(c.Request.Context(), id, c.GetString("admin_id"), input)
	writeCaseResult(c, gin.H{"updated": true}, err)
}

func saveAftercarePolicy(ctx context.Context, id int64, actor string, input aftercarePolicy) error {
	if strings.TrimSpace(input.Reason) == "" {
		return errCaseConflict
	}
	tx, err := db.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	var ok bool
	err = tx.QueryRow(ctx, `SELECT true FROM admins WHERE id=$1 AND role='SuperAdmin' AND status=1 FOR SHARE`, actor).Scan(&ok)
	if errors.Is(err, pgx.ErrNoRows) {
		return errCaseForbidden
	}
	if err != nil {
		return err
	}
	for _, owner := range []string{input.ReviewerID, input.FinanceID} {
		if owner != "" {
			if err := lockCaseOperator(ctx, tx, owner); err != nil {
				return err
			}
		}
	}
	var accountID int64
	if err = tx.QueryRow(ctx, `SELECT id FROM payment_channel_accounts WHERE id=$1 FOR UPDATE`, id).Scan(&accountID); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `WITH previous AS MATERIALIZED (
		SELECT to_jsonb(p) AS data FROM payment_aftercare_policies p WHERE account_id=$1
	), saved AS (
		INSERT INTO payment_aftercare_policies(account_id,dispute_lookup,hold_fulfillment,reviewer_id,finance_id,review_seconds,refund_seconds,reminder_seconds)
		VALUES($1,$2,$3,NULLIF($4,''),NULLIF($5,''),$6,$7,$8)
		ON CONFLICT(account_id) DO UPDATE SET dispute_lookup=EXCLUDED.dispute_lookup,hold_fulfillment=EXCLUDED.hold_fulfillment,
			reviewer_id=EXCLUDED.reviewer_id,finance_id=EXCLUDED.finance_id,review_seconds=EXCLUDED.review_seconds,
			refund_seconds=EXCLUDED.refund_seconds,reminder_seconds=EXCLUDED.reminder_seconds
		RETURNING *
	)
	INSERT INTO admin_audit_logs(admin_id,action,target_id,before_data,after_data)
	SELECT $9,'update_payment_aftercare_policy',$1::bigint::text,(SELECT data FROM previous),
		to_jsonb(saved)||jsonb_build_object('reason',$10::text) FROM saved`,
		accountID, input.DisputeLookup, input.HoldFulfillment, input.ReviewerID, input.FinanceID,
		input.ReviewSeconds, input.RefundSeconds, input.ReminderSeconds, actor, input.Reason)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
