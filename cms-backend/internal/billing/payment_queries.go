package billing

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"star-novel-cms/internal/auth"
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
	if err := requirePermissionTx(ctx, tx, actor, errCaseForbidden, auth.PermPaymentsChannelsWrite); err != nil {
		if err == errCaseForbidden {
			c.JSON(http.StatusForbidden, gin.H{"error": "Payment channel write permission is required"})
			return
		}
		writeRefundCommandError(c, err)
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

func (h *Handler) listPaymentAccountOptions(c *gin.Context) {
	var result json.RawMessage
	err := db.DB.QueryRow(c.Request.Context(), `SELECT COALESCE(jsonb_agg(jsonb_build_object(
		'id',a.id::text,'provider',a.provider,'environment',a.environment,
		'display_name',COALESCE(NULLIF(a.display_name,''),'••••'||right(a.account_key,4)),
		'label','••••'||right(a.account_key,4),
		'status',a.status
	) ORDER BY a.provider,a.environment,a.id),'[]')
	FROM payment_channel_accounts a WHERE a.provider IN ('paypal','stripe')`).Scan(&result)
	writeCaseResult(c, gin.H{"accounts": result}, err)
}

func (h *Handler) ListPaymentSubscriptions(c *gin.Context) {
	var filter struct {
		reconciliationScope
		UserID   string `form:"user_id"`
		Status   string `form:"status" binding:"max=64"`
		Page     int    `form:"page,default=1" binding:"min=1,max=1000000"`
		PageSize int    `form:"page_size,default=100" binding:"min=1,max=100"`
	}
	if c.ShouldBindQuery(&filter) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid subscription filters"})
		return
	}
	if filter.UserID != "" {
		if id, err := strconv.ParseInt(filter.UserID, 10, 64); err != nil || id <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user number"})
			return
		}
	}
	var result json.RawMessage
	err := db.DB.QueryRow(c.Request.Context(), `
		WITH scoped AS (
			SELECT s.id, s.user_id::text, COALESCE(s.payment_method,'') AS provider, s.status,
				COALESCE(s.cycle,'') AS cycle, COALESCE(to_char(s.current_period_end AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),'') AS paid_until,
				COALESCE(payment_subscription_environment(s),'unknown') AS environment,
				COALESCE(a.id, CASE WHEN s.checkout_account_id IS NULL AND s.payment_method='paypal' THEN legacy.account_id END) AS account_id
			FROM user_subscriptions s LEFT JOIN payment_channel_accounts a ON a.id=s.checkout_account_id AND a.provider=s.payment_method
			LEFT JOIN LATERAL (
				SELECT CASE WHEN count(DISTINCT p.account_id)=1 THEN min(p.account_id) END AS account_id
				FROM recharge_orders o JOIN third_party_payment_orders p ON p.order_id=o.id
				JOIN payment_channel_accounts pa ON pa.id=p.account_id AND pa.provider='paypal'
				WHERE s.checkout_account_id IS NULL AND s.payment_method='paypal'
					AND o.subscription_id=s.subscription_id AND o.user_id=s.user_id AND p.status='VERIFIED'
			) legacy ON true
			WHERE ($1='' OR s.user_id=NULLIF($1,'')::bigint) AND s.subscription_id NOT LIKE 'I-MOCK%'
				AND ($2='' OR s.payment_method=$2) AND ($3='' OR s.status=$3)
		), filtered AS MATERIALIZED (
			SELECT * FROM scoped WHERE ($4='' OR environment=CASE $4 WHEN 'live' THEN 'production' ELSE $4 END)
				AND ($5::bigint=0 OR (environment<>'unknown' AND account_id=$5))
		), page AS (
			SELECT id AS sort_id,id::text,user_id,provider,status,cycle,paid_until,environment,
				CASE WHEN environment<>'unknown' THEN account_id::text END AS account_id
			FROM filtered ORDER BY id DESC LIMIT $6 OFFSET $7
		)
		SELECT jsonb_build_object('subscriptions',COALESCE((SELECT jsonb_agg(jsonb_strip_nulls(to_jsonb(page)-'sort_id')
			ORDER BY sort_id DESC) FROM page),'[]'),'total',(SELECT count(*) FROM filtered),'page',$8::integer,'page_size',$6::integer)`,
		filter.UserID, filter.Provider, filter.Status, filter.Environment, filter.AccountID, filter.PageSize,
		(filter.Page-1)*filter.PageSize, filter.Page).Scan(&result)
	writeCaseResult(c, result, err)
}

func (h *Handler) ListPaymentReceipts(c *gin.Context) {
	orderID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || orderID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid order number"})
		return
	}
	var receiptID int64
	if value := c.Query("receipt_id"); value != "" {
		receiptID, err = strconv.ParseInt(value, 10, 64)
		if err != nil || receiptID <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid receipt number"})
			return
		}
	}
	rows, err := db.DB.Query(c.Request.Context(), `SELECT p.id,p.payment_provider,p.currency,(p.gross_amount*100)::bigint,
		GREATEST(0,(p.gross_amount*100)::bigint-r.amount-h.amount),r.amount,h.amount,r.items,
		COALESCE(p.capture_id,''),COALESCE(a.id::text,''),
		EXISTS(SELECT 1 FROM payment_fulfillment_funding f WHERE f.order_id=p.order_id AND f.receipt_id=p.id),
		COALESCE(p.reconciliation_note,''),COALESCE(a.environment,payment_order_environment(o),''),
		p.payment_provider='paypal' AND p.status IN ('VERIFIED','COMPLETED') AND COALESCE(p.capture_id,'')<>'' AND p.capture_id NOT LIKE 'mock%'
		OR (p.payment_provider='stripe' AND p.object_type='charge' AND p.status='VERIFIED' AND p.currency='USD'
		 AND EXISTS(SELECT 1 FROM payment_channel_accounts a JOIN recharge_orders o ON o.id=p.order_id WHERE a.id=p.account_id
		 AND ((a.environment='sandbox' AND o.checkout_environment='sandbox') OR (a.environment='live' AND o.checkout_environment='production'))))
		FROM third_party_payment_orders p JOIN recharge_orders o ON o.id=p.order_id
		LEFT JOIN payment_channel_accounts a ON a.id=p.account_id
		CROSS JOIN LATERAL (
			SELECT COALESCE(sum(x.amount),0)::bigint AS amount,
				COALESCE(jsonb_agg(jsonb_build_object('ref',x.ref,'amount_minor',x.amount::text) ORDER BY x.ref),'[]') AS items
			FROM (
				SELECT f.refund_id AS ref,f.amount_cents AS amount FROM paypal_refund_items f
				WHERE p.payment_provider='paypal' AND f.capture_id=p.capture_id AND f.currency=p.currency
				UNION ALL
				SELECT f.refund_ref,f.amount_minor FROM payment_stripe_refund_items f WHERE f.receipt_id=p.id
			) x
		) r
		CROSS JOIN LATERAL (
			SELECT COALESCE(sum(c.amount_minor),0)::bigint AS amount FROM payment_refund_commands c
			WHERE c.receipt_id=p.id AND c.status IN ('pending','confirming','review')
				AND NOT EXISTS(SELECT 1 FROM paypal_refund_items i WHERE p.payment_provider='paypal' AND i.refund_id=c.provider_refund_id)
				AND NOT EXISTS(SELECT 1 FROM payment_stripe_refund_items i WHERE i.receipt_id=p.id AND i.refund_ref=c.provider_refund_id)
		) h
		WHERE p.order_id=$1 AND ($2::bigint=0 OR p.id=$2) ORDER BY p.id DESC LIMIT 50`, orderID, receiptID)
	if err != nil {
		writeRefundCommandError(c, err)
		return
	}
	result, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (gin.H, error) {
		var id, gross, available, refunded, reserved int64
		var provider, currency, note, environment, reference, accountID string
		var facts json.RawMessage
		var funded, refundable bool
		err := row.Scan(&id, &provider, &currency, &gross, &available, &refunded, &reserved, &facts, &reference, &accountID,
			&funded, &note, &environment, &refundable)
		return gin.H{"id": id, "provider": provider, "currency": currency, "amount_minor": gross, "available_minor": available, "environment": environment,
			"funds_entitlement": funded, "note": note, "refundable": refundable, "refunded_minor": refunded, "reserved_minor": reserved,
			"refund_facts": facts, "receipt_ref": reference, "account_id": accountID}, err
	})
	if err != nil {
		writeRefundCommandError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"receipts": result})
}
