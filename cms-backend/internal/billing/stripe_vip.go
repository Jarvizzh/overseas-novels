package billing

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"star-novel-cms/internal/db"
)

const (
	stripeDirect          = "direct"
	stripeManagedPayments = "merchant_of_record"
)

var stripeAccountRef = regexp.MustCompile(`^acct_[a-zA-Z0-9]+$`)

type stripeVIPInput struct {
	stripeOfferInput
	SalesMode       string `json:"sales_mode" binding:"omitempty,oneof=direct merchant_of_record"`
	ReleaseEvidence string `json:"release_evidence" binding:"max=500"`
	MarketPolicy    string `json:"market_policy" binding:"max=500"`
}

func (h *Handler) updateStripeVIPOffer(c *gin.Context) {
	account, err := strconv.ParseInt(c.Param("id"), 10, 64)
	slot, slotErr := strconv.Atoi(c.Param("slot"))
	var input stripeVIPInput
	if err != nil || slotErr != nil || account <= 0 || slot <= 0 || c.ShouldBindJSON(&input) != nil ||
		!stripeProductRef.MatchString(input.ProductRef) || !stripePriceRef.MatchString(input.PriceRef) ||
		strings.TrimSpace(input.Reason) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid Stripe VIP mapping"})
		return
	}
	if input.SalesMode == "" {
		input.SalesMode = stripeDirect
	}
	slices.Sort(input.AllowedCountries)
	input.AllowedCountries = slices.Compact(input.AllowedCountries)
	err = saveStripeVIPOffer(c.Request.Context(), account, slot, c.GetString("admin_id"), input)
	writeCaseResult(c, gin.H{"updated": true}, err)
}

func saveStripeVIPOffer(ctx context.Context, account int64, slot int, actor string, input stripeVIPInput) error {
	catalog, err := verifyStripeOffer(ctx, account, actor, input)
	if err != nil {
		return err
	}
	tx, err := db.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if err := lockStripeOfferConfig(ctx, tx, account, actor); err != nil {
		return err
	}
	var version int
	var amount int64
	var cycle string
	err = tx.QueryRow(ctx, `SELECT v.slot_id,v.amount_minor,v.cycle FROM recharge_slots s JOIN payment_vip_offer_versions v ON v.slot_id=s.vip_offer_version_id
		WHERE s.id=$1 AND s.type IN ('vip','subscription') AND v.currency='USD' AND v.amount_minor=s.price_cents FOR SHARE OF s`, slot).Scan(&version, &amount, &cycle)
	if err != nil {
		return err
	}
	verified, err := stripeCatalogJSON(catalog, amount, cycle)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `WITH prior AS MATERIALIZED (
		SELECT to_jsonb(o) AS data FROM payment_stripe_vip_offers o WHERE account_id=$1 AND offer_version_id=$2
	), saved AS (
		INSERT INTO payment_stripe_vip_offers(account_id,offer_version_id,product_ref,price_ref,sales_mode,allowed_countries,enabled,validated_catalog,release_evidence,market_policy)
		VALUES($1,$2,$3,$4,$5,$6,$7,$10::jsonb,NULLIF($11,''),NULLIF($12,''))
		ON CONFLICT(account_id,offer_version_id) DO UPDATE SET product_ref=EXCLUDED.product_ref,price_ref=EXCLUDED.price_ref,
			sales_mode=EXCLUDED.sales_mode,allowed_countries=EXCLUDED.allowed_countries,
			enabled=EXCLUDED.enabled,validated_catalog=EXCLUDED.validated_catalog,release_evidence=EXCLUDED.release_evidence,market_policy=EXCLUDED.market_policy RETURNING *
	) INSERT INTO admin_audit_logs(admin_id,action,target_id,before_data,after_data)
	SELECT $8,'publish_stripe_vip_offer',$1::bigint::text||'/'||$2::int::text,(SELECT data FROM prior),
		to_jsonb(saved)||jsonb_build_object('reason',$9::text) FROM saved`, account, version, input.ProductRef, input.PriceRef,
		input.SalesMode, input.AllowedCountries, *input.Enabled, actor, strings.TrimSpace(input.Reason), verified, strings.TrimSpace(input.ReleaseEvidence), strings.TrimSpace(input.MarketPolicy))
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (h *Handler) listStripeVIPOffers(c *gin.Context) {
	account, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || account <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid Stripe account"})
		return
	}
	var result json.RawMessage
	err = db.DB.QueryRow(c.Request.Context(), `SELECT COALESCE(jsonb_agg(jsonb_build_object(
		'slot_id',s.id,'version_id',v.slot_id,'name',v.name,'cycle',v.cycle,'amount_minor',v.amount_minor,'template',t.name,
		'product_id',m.product_ref,'price_id',m.price_ref,'sales_mode',m.sales_mode,'countries',m.allowed_countries,
		'tax_liability_account',m.tax_liability_account,'verified_catalog',m.validated_catalog,'release_evidence',m.release_evidence,'market_policy',m.market_policy,'enabled',COALESCE(m.enabled,false)) ORDER BY t.id,s.slot_index),'[]')
		FROM payment_channel_accounts a JOIN recharge_slots s ON s.type IN ('vip','subscription')
		JOIN recharge_templates t ON t.id=s.template_id JOIN payment_vip_offer_versions v ON v.slot_id=s.vip_offer_version_id
		LEFT JOIN payment_stripe_vip_offers m ON m.offer_version_id=v.slot_id AND m.account_id=a.id
		WHERE a.id=$1 AND a.provider='stripe'`, account).Scan(&result)
	writeCaseResult(c, gin.H{"offers": result}, err)
}

func (h *Handler) listStripeVIPPeriods(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid subscription number"})
		return
	}
	var result json.RawMessage
	err = db.DB.QueryRow(c.Request.Context(), `SELECT COALESCE(jsonb_agg(to_jsonb(period) ORDER BY period.order_id::bigint DESC),'[]') FROM (
		SELECT p.invoice_ref,p.order_id::text,p.service_start,p.service_end,p.billed_minor,p.billing_reason,p.country,
			p.provider_status,p.revises_invoice_ref,p.revision_verified,
			p.observed_tax_liability_account,p.observed_customer_tax_status,p.review_reason,p.admitted,v.sales_mode,a.environment,o.status,
			(SELECT count(*) FROM payment_stripe_invoice_allocations f WHERE f.account_id=p.account_id AND f.invoice_ref=p.invoice_ref) AS allocation_count
		FROM user_subscriptions s JOIN payment_stripe_subscription_intents v ON v.id=s.checkout_intent_id
		JOIN payment_channel_accounts a ON a.id=v.account_id JOIN payment_stripe_invoice_periods p ON p.intent_id=v.id
		JOIN recharge_orders o ON o.id=p.order_id WHERE s.id=$1 AND s.payment_method='stripe'
		ORDER BY p.order_id DESC LIMIT 50
	) period`, id).Scan(&result)
	writeCaseResult(c, gin.H{"periods": result}, err)
}
