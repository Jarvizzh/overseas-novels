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
	"github.com/jackc/pgx/v5"
	"star-novel-cms/internal/auth"
	"star-novel-cms/internal/db"
)

var stripeProductRef = regexp.MustCompile(`^prod_[a-zA-Z0-9]+$`)
var stripePriceRef = regexp.MustCompile(`^price_[a-zA-Z0-9]+$`)

type stripeOfferInput struct {
	ProductRef       string   `json:"product_id" binding:"required,max=100"`
	PriceRef         string   `json:"price_id" binding:"required,max=100"`
	AllowedCountries []string `json:"countries" binding:"required,min=1,max=250,dive,len=2,alpha,uppercase"`
	Enabled          *bool    `json:"enabled" binding:"required"`
	Reason           string   `json:"reason" binding:"required,max=500"`
}

func (h *Handler) updateStripeOneTimeOffer(c *gin.Context) {
	account, err := strconv.ParseInt(c.Param("id"), 10, 64)
	slot, slotErr := strconv.Atoi(c.Param("slot"))
	var input stripeVIPInput
	if err != nil || slotErr != nil || account <= 0 || slot <= 0 || c.ShouldBindJSON(&input) != nil ||
		!stripeProductRef.MatchString(input.ProductRef) || !stripePriceRef.MatchString(input.PriceRef) || strings.TrimSpace(input.Reason) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid Stripe offer"})
		return
	}
	if input.SalesMode == "" {
		input.SalesMode = stripeDirect
	}
	if !input.validTaxLiability() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid Stripe sales mode or tax liability"})
		return
	}
	slices.Sort(input.AllowedCountries)
	input.AllowedCountries = slices.Compact(input.AllowedCountries)
	err = saveStripeOneTimeOffer(c.Request.Context(), account, slot, c.GetString("admin_id"), input)
	writeCaseResult(c, gin.H{"updated": true}, err)
}

func saveStripeOneTimeOffer(ctx context.Context, account int64, slot int, actor string, input stripeVIPInput) error {
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
	var amount int64
	err = tx.QueryRow(ctx, `SELECT price_cents FROM recharge_slots WHERE id=$1 AND type='whole_book' AND price_cents>0 FOR SHARE`, slot).Scan(&amount)
	if err != nil {
		return err
	}
	verified, err := stripeCatalogJSON(catalog, amount, "")
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `WITH prior AS MATERIALIZED (
		SELECT to_jsonb(p) AS data FROM payment_stripe_one_time_offers p WHERE account_id=$1 AND slot_id=$2
	), saved AS (
		INSERT INTO payment_stripe_one_time_offers(account_id,slot_id,product_ref,price_ref,amount_minor,allowed_countries,enabled,sales_mode,tax_liability_account,validated_catalog,release_evidence,market_policy)
		VALUES($1,$2,$3,$4,$5,$6,$7,$10,NULLIF($11,''),$12::jsonb,NULLIF($13,''),NULLIF($14,''))
		ON CONFLICT(account_id,slot_id) DO UPDATE SET product_ref=EXCLUDED.product_ref,price_ref=EXCLUDED.price_ref,
		    amount_minor=EXCLUDED.amount_minor,allowed_countries=EXCLUDED.allowed_countries,enabled=EXCLUDED.enabled,
		    sales_mode=EXCLUDED.sales_mode,tax_liability_account=EXCLUDED.tax_liability_account,
		    validated_catalog=EXCLUDED.validated_catalog,release_evidence=EXCLUDED.release_evidence,market_policy=EXCLUDED.market_policy
		RETURNING *
	) INSERT INTO admin_audit_logs(admin_id,action,target_id,before_data,after_data)
	SELECT $8,'publish_stripe_single_offer',$1::bigint::text||'/'||$2::int::text,(SELECT data FROM prior),
		to_jsonb(saved)||jsonb_build_object('reason',$9::text) FROM saved`, account, slot, input.ProductRef, input.PriceRef, amount,
		input.AllowedCountries, *input.Enabled, actor, strings.TrimSpace(input.Reason), input.SalesMode, input.TaxLiabilityAccount, verified, strings.TrimSpace(input.ReleaseEvidence), strings.TrimSpace(input.MarketPolicy))
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func lockStripeOfferConfig(ctx context.Context, tx pgx.Tx, account int64, actor string) error {
	if err := requirePermissionTx(ctx, tx, actor, errCaseForbidden, auth.PermPaymentsProductsWrite, auth.PermPaymentsChannelsWrite); err != nil {
		return err
	}
	var id int64
	return tx.QueryRow(ctx, `SELECT id FROM payment_channel_accounts WHERE id=$1 AND provider='stripe' FOR UPDATE`, account).Scan(&id)
}

func (h *Handler) listStripeOneTimeOffers(c *gin.Context) {
	account, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || account <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid Stripe account"})
		return
	}
	var result json.RawMessage
	err = db.DB.QueryRow(c.Request.Context(), `SELECT COALESCE(jsonb_agg(jsonb_build_object(
		'slot_id',s.id,'version_id',s.id,'name',COALESCE(NULLIF(s.vip_name,''),'整本购买'),'amount_minor',s.price_cents,'template',t.name,
		'product_id',m.product_ref,'price_id',m.price_ref,'sales_mode',m.sales_mode,'countries',m.allowed_countries,
		'tax_liability_account',m.tax_liability_account,'verified_catalog',m.validated_catalog,'release_evidence',m.release_evidence,'market_policy',m.market_policy,'enabled',COALESCE(m.enabled,false)) ORDER BY t.id,s.slot_index),'[]')
		FROM payment_channel_accounts a JOIN recharge_slots s ON s.type='whole_book' AND s.price_cents>0
		JOIN recharge_templates t ON t.id=s.template_id
		LEFT JOIN payment_stripe_one_time_offers m ON m.slot_id=s.id AND m.account_id=a.id
		WHERE a.id=$1 AND a.provider='stripe'`, account).Scan(&result)
	writeCaseResult(c, gin.H{"offers": result}, err)
}
