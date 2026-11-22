package billing

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"star-novel-cms/internal/db"
)

func (h *Handler) listPayPalPeriods(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid subscription number"})
		return
	}
	var result json.RawMessage
	err = db.DB.QueryRow(c.Request.Context(), `SELECT COALESCE(jsonb_agg(to_jsonb(period) ORDER BY period.order_id::bigint DESC),'[]') FROM (
		SELECT o.id::text AS order_id,COALESCE(o.external_ref_id,'') AS invoice_ref,o.paid_period_start AS service_start,
		  o.paid_period_end AS service_end,o.amount_cents AS billed_minor,o.status,o.reconciliation_note AS review_reason,
		  o.fulfilled_at IS NOT NULL AND o.paid_period_end>o.paid_period_start AS admitted,'direct' AS sales_mode,
		  COALESCE(a.environment,'unknown') AS environment,
		  (SELECT count(*) FROM third_party_payment_orders t WHERE t.order_id=o.id AND t.payment_provider='paypal'
		   AND t.status IN ('VERIFIED','COMPLETED') AND t.capture_id IS DISTINCT FROM s.subscription_id) AS allocation_count
		FROM user_subscriptions s JOIN recharge_orders o ON o.subscription_id=s.subscription_id AND o.user_id=s.user_id
		LEFT JOIN payment_channel_accounts a ON a.id=s.checkout_account_id
		WHERE s.id=$1 AND s.payment_method='paypal' AND o.payment_method='paypal' AND o.order_type='subscription'
		ORDER BY o.id DESC LIMIT 50) period`, id).Scan(&result)
	writeCaseResult(c, gin.H{"periods": result}, err)
}
