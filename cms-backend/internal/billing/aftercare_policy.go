package billing

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"star-novel-cms/internal/auth"
	"star-novel-cms/internal/db"
)

type aftercarePolicy struct {
	DisputeLookup          bool    `json:"dispute_lookup"`
	HoldFulfillment        bool    `json:"hold_fulfillment"`
	AutoRefund             bool    `json:"auto_refund"`
	AutoCancelSubscription bool    `json:"auto_cancel_subscription"`
	DisputeFreeze          bool    `json:"dispute_freeze"`
	ReviewerID             string  `json:"reviewer_id" binding:"max=36"`
	FinanceID              string  `json:"finance_id" binding:"max=36"`
	ReviewSeconds          *int    `json:"review_seconds" binding:"omitempty,min=1"`
	RefundSeconds          *int    `json:"refund_seconds" binding:"omitempty,min=1"`
	ReminderSeconds        *int    `json:"reminder_seconds" binding:"omitempty,min=1"`
	Reason                 string  `json:"reason" binding:"required,max=500"`
	Revision               *string `json:"revision" binding:"omitempty,max=32"`
}

func (h *Handler) getAftercarePolicy(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid payment account"})
		return
	}
	var policy json.RawMessage
	err = db.DB.QueryRow(c.Request.Context(), `SELECT jsonb_build_object(
		'configured',p.account_id IS NOT NULL,'revision',COALESCE(md5(to_jsonb(p)::text),''),
		'dispute_lookup',COALESCE(p.dispute_lookup,false),'hold_fulfillment',COALESCE(p.hold_fulfillment,false),
		'auto_refund',COALESCE(p.auto_refund,false),'auto_cancel_subscription',COALESCE(p.auto_cancel_subscription,false),
		'dispute_freeze',COALESCE(p.dispute_freeze,false),
		'reviewer_id',COALESCE(p.reviewer_id,''),'finance_id',COALESCE(p.finance_id,''),
		'review_seconds',p.review_seconds,'refund_seconds',p.refund_seconds,'reminder_seconds',p.reminder_seconds
	) FROM payment_channel_accounts a LEFT JOIN payment_aftercare_policies p ON p.account_id=a.id WHERE a.id=$1`, id).Scan(&policy)
	writeCaseResult(c, policy, err)
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
	if err := requirePermissionTx(ctx, tx, actor, errCaseForbidden, auth.PermPaymentsChannelsWrite); err != nil {
		return err
	}
	if input.ReviewerID != "" {
		if err := requirePermissionTx(ctx, tx, input.ReviewerID, errCaseForbidden, auth.PermPaymentsRiskWrite); err != nil {
			return err
		}
	}
	if input.FinanceID != "" {
		if err := requirePermissionTx(ctx, tx, input.FinanceID, errCaseForbidden, auth.PermPaymentsDisputesWrite, auth.PermPaymentsRefundsWrite); err != nil {
			return err
		}
	}
	var accountID int64
	if err = tx.QueryRow(ctx, `SELECT id FROM payment_channel_accounts WHERE id=$1 FOR UPDATE`, id).Scan(&accountID); err != nil {
		return err
	}
	if input.Revision != nil {
		var revision string
		err := tx.QueryRow(ctx, `SELECT COALESCE((SELECT md5(to_jsonb(p)::text)
			FROM payment_aftercare_policies p WHERE account_id=$1),'')`, accountID).Scan(&revision)
		if err != nil {
			return err
		}
		if revision != *input.Revision {
			return errCaseConflict
		}
	}
	_, err = tx.Exec(ctx, `WITH previous AS MATERIALIZED (
		SELECT to_jsonb(p) AS data FROM payment_aftercare_policies p WHERE account_id=$1
	), saved AS (
		INSERT INTO payment_aftercare_policies(account_id,dispute_lookup,hold_fulfillment,auto_refund,auto_cancel_subscription,dispute_freeze,reviewer_id,finance_id,review_seconds,refund_seconds,reminder_seconds)
		VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,''),NULLIF($8,''),$9,$10,$11)
		ON CONFLICT(account_id) DO UPDATE SET dispute_lookup=EXCLUDED.dispute_lookup,hold_fulfillment=EXCLUDED.hold_fulfillment,
			auto_refund=EXCLUDED.auto_refund,auto_cancel_subscription=EXCLUDED.auto_cancel_subscription,dispute_freeze=EXCLUDED.dispute_freeze,
			reviewer_id=EXCLUDED.reviewer_id,finance_id=EXCLUDED.finance_id,review_seconds=EXCLUDED.review_seconds,
			refund_seconds=EXCLUDED.refund_seconds,reminder_seconds=EXCLUDED.reminder_seconds
		RETURNING *
	)
	INSERT INTO admin_audit_logs(admin_id,action,target_id,before_data,after_data)
	SELECT $12,'update_payment_aftercare_policy',$1::bigint::text,(SELECT data FROM previous),
		to_jsonb(saved)||jsonb_build_object('reason',$13::text) FROM saved`,
		accountID, input.DisputeLookup, input.HoldFulfillment, input.AutoRefund, input.AutoCancelSubscription, input.DisputeFreeze,
		input.ReviewerID, input.FinanceID, input.ReviewSeconds, input.RefundSeconds, input.ReminderSeconds, actor, input.Reason)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
