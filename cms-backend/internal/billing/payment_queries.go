package billing

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"star-novel-cms/internal/config"
	"star-novel-cms/internal/db"
)

func (h *Handler) ListVIPOfferPublications(c *gin.Context) {
	offers, err := h.service.ListVIPOfferPublications(c.Request.Context())
	if err != nil {
		writeRefundCommandError(c, err)
		return
	}
	if offers == nil {
		offers = []VIPOfferPublication{}
	}
	c.JSON(http.StatusOK, gin.H{"offers": offers})
}

func (h *Handler) UpdatePaymentChannel(c *gin.Context) {
	var input struct {
		NewOrders        *bool  `json:"new_orders_enabled" binding:"required"`
		NewSubscriptions *bool  `json:"new_subscriptions_enabled" binding:"required"`
		CredentialsRef   string `json:"credentials_ref" binding:"required,max=120"`
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 || c.ShouldBindJSON(&input) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid payment channel configuration"})
		return
	}
	ctx := c.Request.Context()
	tx, err := db.DB.Begin(ctx)
	if err != nil {
		writeRefundCommandError(c, err)
		return
	}
	defer tx.Rollback(ctx)
	actor := c.GetString("admin_id")
	var role string
	if err := tx.QueryRow(ctx, `SELECT role FROM admins WHERE id=$1 AND status=1 FOR SHARE`, actor).Scan(&role); err != nil || role != "SuperAdmin" {
		c.JSON(http.StatusForbidden, gin.H{"error": "Payment configuration permission is required"})
		return
	}
	var account, provider, environment, oldSource, status string
	var oldOrders, oldSubscriptions bool
	err = tx.QueryRow(ctx, `SELECT account_key,provider,environment,credentials_ref,status,new_orders_enabled,new_subscriptions_enabled
		FROM payment_channel_accounts WHERE id=$1 AND provider IN ('paypal','stripe') FOR UPDATE`, id).
		Scan(&account, &provider, &environment, &oldSource, &status, &oldOrders, &oldSubscriptions)
	if err != nil {
		writeRefundCommandError(c, err)
		return
	}
	if (*input.NewOrders || *input.NewSubscriptions) && status != channelStatusPublished {
		c.JSON(http.StatusConflict, gin.H{"error": "Channel account must be verified and published before enabling checkout switches"})
		return
	}
	if (provider == "stripe" && input.CredentialsRef != oldSource) ||
		(provider == "paypal" && input.CredentialsRef != oldSource && input.CredentialsRef != "legacy:paypal" && input.CredentialsRef != "env:paypal") {
		c.JSON(http.StatusConflict, gin.H{"error": "The credential reference does not match the registered payment channel"})
		return
	}
	if input.CredentialsRef == "env:paypal" && oldSource != input.CredentialsRef &&
		(config.AppConfig == nil || config.AppConfig.PayPalClientID != account || config.AppConfig.PayPalClientSecret == "") {
		c.JSON(http.StatusConflict, gin.H{"error": "Verify that the environment credentials use the same PayPal application"})
		return
	}
	_, err = tx.Exec(ctx, `UPDATE payment_channel_accounts SET credentials_ref=$2,new_orders_enabled=$3,new_subscriptions_enabled=$4,updated_at=now() WHERE id=$1`,
		id, input.CredentialsRef, *input.NewOrders, *input.NewSubscriptions)
	if err == nil {
		_, err = tx.Exec(ctx, `INSERT INTO admin_audit_logs(admin_id,action,target_id,before_data,after_data)
			VALUES ($1,'update_payment_channel',$2,jsonb_build_object('credentials_ref',$3::text,'new_orders_enabled',$4::bool,'new_subscriptions_enabled',$5::bool),
			jsonb_build_object('credentials_ref',$6::text,'new_orders_enabled',$7::bool,'new_subscriptions_enabled',$8::bool))`,
			actor, c.Param("id"), oldSource, oldOrders, oldSubscriptions, input.CredentialsRef, *input.NewOrders, *input.NewSubscriptions)
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		writeRefundCommandError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"updated": true})
}

func (h *Handler) ListPaymentSubscriptions(c *gin.Context) {
	userID := c.Query("user_id")
	if c.GetString("payment_role") == "Support" && userID == "" {
		c.JSON(http.StatusForbidden, gin.H{"error": "Provide a user number for a support lookup"})
		return
	}
	if userID != "" {
		if id, err := strconv.ParseInt(userID, 10, 64); err != nil || id <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user number"})
			return
		}
	}
	rows, err := db.DB.Query(c.Request.Context(), `SELECT id::text, user_id::text, COALESCE(payment_method,''),status,
		COALESCE(cycle,''),COALESCE(to_char(current_period_end AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),''),
		COALESCE(payment_subscription_environment(user_subscriptions),'unknown')
		FROM user_subscriptions WHERE ($1='' OR user_id=NULLIF($1,'')::bigint) AND subscription_id NOT LIKE 'I-MOCK%'
		ORDER BY id DESC LIMIT 100`, userID)
	if err != nil {
		writeRefundCommandError(c, err)
		return
	}
	result, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (gin.H, error) {
		var id, user, provider, status, cycle, paidEnd, environment string
		err := row.Scan(&id, &user, &provider, &status, &cycle, &paidEnd, &environment)
		return gin.H{"id": id, "user_id": user, "provider": provider, "status": status, "cycle": cycle, "paid_until": paidEnd, "environment": environment}, err
	})
	if err != nil {
		writeRefundCommandError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"subscriptions": result})
}

func (h *Handler) ListPaymentReceipts(c *gin.Context) {
	orderID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || orderID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid order number"})
		return
	}
	rows, err := db.DB.Query(c.Request.Context(), `SELECT p.id,p.payment_provider,p.currency,(p.gross_amount*100)::bigint,
		GREATEST(0,(p.gross_amount*100)::bigint
		    -COALESCE((SELECT sum(amount_cents)::bigint FROM paypal_refund_items WHERE p.payment_provider='paypal' AND capture_id=p.capture_id AND currency=p.currency),0)
 -COALESCE((SELECT sum(amount_minor)::bigint FROM payment_stripe_refund_items WHERE receipt_id=p.id),0)
		    -COALESCE((SELECT sum(c.amount_minor)::bigint FROM payment_refund_commands c WHERE c.receipt_id=p.id
		       AND c.status IN ('pending','confirming','review')
		       AND NOT EXISTS(SELECT 1 FROM paypal_refund_items i WHERE p.payment_provider='paypal' AND i.refund_id=c.provider_refund_id)
 AND NOT EXISTS(SELECT 1 FROM payment_stripe_refund_items i WHERE i.receipt_id=p.id AND i.refund_ref=c.provider_refund_id)),0)),
		EXISTS(SELECT 1 FROM payment_fulfillment_funding f WHERE f.order_id=p.order_id AND f.receipt_id=p.id),
		COALESCE(p.reconciliation_note,''),COALESCE(a.environment,payment_order_environment(o),''),
		p.payment_provider='paypal' AND p.status IN ('VERIFIED','COMPLETED') AND COALESCE(p.capture_id,'')<>'' AND p.capture_id NOT LIKE 'mock%'
		OR (p.payment_provider='stripe' AND p.object_type='charge' AND p.status='VERIFIED' AND p.currency='USD'
		 AND EXISTS(SELECT 1 FROM payment_channel_accounts a JOIN recharge_orders o ON o.id=p.order_id WHERE a.id=p.account_id
		 AND ((a.environment='sandbox' AND o.checkout_environment='sandbox') OR (a.environment='live' AND o.checkout_environment='production'))))
		FROM third_party_payment_orders p JOIN recharge_orders o ON o.id=p.order_id
		LEFT JOIN payment_channel_accounts a ON a.id=p.account_id WHERE p.order_id=$1 ORDER BY p.id DESC LIMIT 50`, orderID)
	if err != nil {
		writeRefundCommandError(c, err)
		return
	}
	result, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (gin.H, error) {
		var id, gross, available int64
		var provider, currency, note, environment string
		var funded, refundable bool
		err := row.Scan(&id, &provider, &currency, &gross, &available, &funded, &note, &environment, &refundable)
		return gin.H{"id": id, "provider": provider, "currency": currency, "amount_minor": gross, "available_minor": available, "environment": environment,
			"funds_entitlement": funded, "note": note, "refundable": refundable}, err
	})
	if err != nil {
		writeRefundCommandError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"receipts": result})
}
