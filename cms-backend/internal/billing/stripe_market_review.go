package billing

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"star-novel-cms/internal/db"
)

func (h *Handler) getStripeMarketReview(c *gin.Context) {
	orderID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || orderID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid order number"})
		return
	}
	var result json.RawMessage
	err = db.DB.QueryRow(c.Request.Context(), `SELECT (SELECT jsonb_build_object(
		'evidence_key',r.evidence_key,'evidence',r.evidence,'approved_at',r.approved_at,'approval_reason',r.approval_reason,
		'note',o.reconciliation_note,'can_approve',r.approved_at IS NULL AND o.fulfilled_at IS NULL AND o.status='Paid'
		 AND (o.reconciliation_note LIKE 'Stripe billing country%' OR o.reconciliation_note LIKE 'Stripe Managed Payments tax%'))
		FROM payment_stripe_market_reviews r JOIN recharge_orders o ON o.id=r.order_id
		WHERE r.order_id=$1 ORDER BY r.updated_at DESC,r.evidence_key LIMIT 1)`, orderID).Scan(&result)
	writeCaseResult(c, gin.H{"review": result}, err)
}

func (h *Handler) approveStripeMarketReview(c *gin.Context) {
	orderID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	var input struct {
		EvidenceKey string `json:"evidence_key" binding:"required,len=64,hexadecimal"`
		Reason      string `json:"reason" binding:"required,max=500"`
	}
	if err != nil || orderID <= 0 || c.ShouldBindJSON(&input) != nil || strings.TrimSpace(input.Reason) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Provide the review evidence and approval reason"})
		return
	}
	err = approveStripeMarketEvidence(c.Request.Context(), orderID, c.GetString("admin_id"), input.EvidenceKey, strings.TrimSpace(input.Reason))
	writeCaseResult(c, gin.H{"approved": true}, err)
}

func approveStripeMarketEvidence(ctx context.Context, orderID int64, actor, key, reason string) error {
	tx, err := db.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if err := requireFinancialAdmin(ctx, tx, actor); err != nil {
		return errCaseForbidden
	}
	var method string
	var current bool
	err = tx.QueryRow(ctx, `SELECT payment_method,COALESCE(fulfilled_at IS NULL AND status='Paid'
		AND (reconciliation_note LIKE 'Stripe billing country%' OR reconciliation_note LIKE 'Stripe Managed Payments tax%'),false)
		FROM recharge_orders WHERE id=$1 FOR UPDATE`, orderID).Scan(&method, &current)
	if err != nil {
		return err
	}
	if !current || method != "stripe" {
		return errCaseConflict
	}
	tag, err := tx.Exec(ctx, `UPDATE payment_stripe_market_reviews r
		SET approved_at=now(),approved_by=$3,approval_reason=$4
		WHERE r.order_id=$1 AND r.evidence_key=$2 AND r.approved_at IS NULL
		  AND r.evidence_key=(SELECT newest.evidence_key FROM payment_stripe_market_reviews newest
			WHERE newest.order_id=$1 ORDER BY newest.updated_at DESC,newest.evidence_key LIMIT 1)`,
		orderID, key, actor, reason)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errCaseConflict
	}
	_, err = tx.Exec(ctx, `INSERT INTO admin_audit_logs(admin_id,action,target_id,after_data)
		SELECT $2,'approve_stripe_market_evidence',$1::bigint::text,
		jsonb_build_object('evidence_key',evidence_key,'evidence',evidence,'reason',approval_reason)
		FROM payment_stripe_market_reviews WHERE order_id=$1 AND evidence_key=$3`, orderID, actor, key)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO payment_event_inbox(provider,account_id,event_id,event_type,amount_cents,currency,event)
		SELECT 'stripe',p.account_id,'market-review:'||p.order_id||':'||$2::text,'review.approved',0,'USD',jsonb_build_object('invoice_id',p.invoice_ref)
		FROM payment_stripe_invoice_periods p WHERE p.order_id=$1
		ON CONFLICT ON CONSTRAINT payment_event_identity DO NOTHING`, orderID, key)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE payment_checkout_attempts SET next_create_at=now()
		WHERE order_id=$1 AND provider='stripe' AND status='settled'`, orderID)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
