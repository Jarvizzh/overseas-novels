package billing

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"star-novel-cms/internal/db"
)

func (h *Handler) getCheckoutEvidence(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid order number"})
		return
	}
	var result json.RawMessage
	err = db.DB.QueryRow(c.Request.Context(), `SELECT jsonb_build_object(
		'order_type',o.order_type,'amount_minor',o.amount_cents,'currency',o.currency,'slot_id',o.recharge_slot_id,
		'environment',payment_order_environment(o),
		'novel_id',o.novel_id,'status',o.status,'fulfilled_at',o.fulfilled_at,'paid_at',o.paid_at,'note',o.reconciliation_note,
		'attempts',COALESCE((SELECT jsonb_agg(to_jsonb(attempt) ORDER BY attempt.created_at DESC) FROM (
		    SELECT p.id::text,p.provider,p.sales_mode,p.status,p.provider_order_id,p.created_at,a.environment
		    FROM payment_checkout_attempts p JOIN payment_channel_accounts a ON a.id=p.account_id WHERE p.order_id=o.id
		    ORDER BY p.created_at DESC LIMIT 30) attempt),'[]'),
		'receipts',COALESCE((SELECT jsonb_agg(to_jsonb(receipt) ORDER BY receipt.id) FROM (
		    SELECT t.id::text,t.payment_provider AS provider,t.capture_id,t.status,t.currency,(t.gross_amount*100)::bigint AS amount_minor,
		      t.reconciliation_note AS note,EXISTS(SELECT 1 FROM payment_fulfillment_funding f WHERE f.order_id=o.id AND f.receipt_id=t.id) AS funds_entitlement
		    FROM third_party_payment_orders t WHERE t.order_id=o.id ORDER BY t.id LIMIT 50) receipt),'[]')
	) FROM recharge_orders o WHERE o.id=$1`, id).Scan(&result)
	writeCaseResult(c, gin.H{"checkout": result}, err)
}
