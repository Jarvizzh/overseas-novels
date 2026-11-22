package billing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"star-novel-cms/internal/db"
)

const (
	productTypeSingle    = "single"
	productTypeWholeBook = "whole_book"
	productTypeVIP       = "vip"

	scopeNone           = "none"
	scopeAllPublished   = "all_published"
	scopePromotedNovel  = "promoted_novel"
	scopeSpecificNovels = "specific_novels"

	pubStatusDraft     = "draft"
	pubStatusPublished = "published"
	pubStatusPaused    = "paused"
	pubStatusRetired   = "retired"

	defaultStripeDigitalTaxCode = "txcd_10103001"
)

var (
	productCodePattern   = regexp.MustCompile(`^[A-Z0-9_]{3,64}$`)
	paypalPlanIDPattern  = regexp.MustCompile(`^P-[A-Z0-9]+$`)
	paypalProdIDPattern  = regexp.MustCompile(`^PROD-[A-Z0-9]+$`)
	stripeTaxCodePattern = regexp.MustCompile(`^txcd_[A-Za-z0-9]+$`)
	countryISO2Pattern   = regexp.MustCompile(`^[A-Z]{2}$`)
	errProductNotFound   = errors.New("product not found")
	errProductInvalid    = errors.New("invalid product specification")
	errProductGateFailed = errors.New("product is not ready on both PayPal and Stripe channels")
)

type ProductPublicationView struct {
	Environment string     `json:"environment"`
	Status      string     `json:"status"`
	PublishedAt *time.Time `json:"published_at,omitempty"`
	PausedAt    *time.Time `json:"paused_at,omitempty"`
	RetiredAt   *time.Time `json:"retired_at,omitempty"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

type ChannelBindingView struct {
	ID                  int64           `json:"id"`
	ProductID           int64           `json:"product_id"`
	AccountID           int64           `json:"account_id"`
	AccountDisplayName  string          `json:"account_display_name"`
	AccountKey          string          `json:"account_key"`
	AccountIsPrimary    bool            `json:"account_is_primary"`
	AccountStatus       string          `json:"account_status"`
	Provider            string          `json:"provider"`
	Environment         string          `json:"environment"`
	ExternalProductRef  string          `json:"external_product_ref,omitempty"`
	ExternalPriceRef    string          `json:"external_price_ref,omitempty"`
	SalesMode           string          `json:"sales_mode"`
	AllowedCountries    []string        `json:"allowed_countries"`
	TaxLiabilityAccount string          `json:"tax_liability_account,omitempty"`
	ValidatedCatalog    json.RawMessage `json:"validated_catalog,omitempty"`
	ReleaseEvidence     string          `json:"release_evidence,omitempty"`
	MarketPolicy        string          `json:"market_policy,omitempty"`
	ValidationStatus    string          `json:"validation_status"`
	ValidationError     string          `json:"validation_error,omitempty"`
	ReviewRequired      bool            `json:"review_required,omitempty"`
	ValidatedAt         *time.Time      `json:"validated_at,omitempty"`
	Enabled             bool            `json:"enabled"`
	Ready               bool            `json:"ready"`
}

type ProductView struct {
	ID                 int64                             `json:"id"`
	Code               string                            `json:"code"`
	SourceProductID    *int64                            `json:"source_product_id,omitempty"`
	ProductType        string                            `json:"product_type"`
	Name               string                            `json:"name"`
	Description        string                            `json:"description"`
	PriceCents         int                               `json:"price_cents"`
	PriceDisplay       string                            `json:"price"`
	Currency           string                            `json:"currency"`
	Coins              int                               `json:"coins"`
	Bonus              int                               `json:"bonus"`
	VipCycle           string                            `json:"vip_cycle,omitempty"`
	NovelScopeType     string                            `json:"novel_scope_type"`
	NovelScopeIDs      []int64                           `json:"novel_scope_ids"`
	CreatedBy          string                            `json:"created_by,omitempty"`
	CreatedAt          time.Time                         `json:"created_at"`
	Publications       map[string]ProductPublicationView `json:"publications"`
	PayPalBinding      *ChannelBindingView               `json:"paypal_binding,omitempty"`
	StripeBinding      *ChannelBindingView               `json:"stripe_binding,omitempty"`
	AllBindings        []ChannelBindingView              `json:"all_bindings,omitempty"`
	PayPalReady        bool                              `json:"paypal_ready"`
	StripeReady        bool                              `json:"stripe_ready"`
	DualChannelReady   bool                              `json:"dual_channel_ready"`
	ReadinessReasons   []string                          `json:"readiness_reasons"`
	LinkedTemplateIDs  []int                             `json:"linked_template_ids"`
	LinkedTemplateList []string                          `json:"linked_template_names"`
}

type productSpecInput struct {
	Code           string  `json:"code"`
	ProductType    string  `json:"product_type"`
	Name           string  `json:"name"`
	Description    string  `json:"description"`
	PriceCents     int     `json:"price_cents"`
	Coins          int     `json:"coins"`
	Bonus          int     `json:"bonus"`
	VipCycle       string  `json:"vip_cycle"`
	NovelScopeType string  `json:"novel_scope_type"`
	NovelScopeIDs  []int64 `json:"novel_scope_ids"`
}

func formatUSDCents(cents int) string {
	return fmt.Sprintf("$%d.%02d", cents/100, cents%100)
}

func normalizeAndValidateProductSpec(in *productSpecInput) error {
	in.Code = strings.ToUpper(strings.TrimSpace(in.Code))
	in.ProductType = strings.TrimSpace(in.ProductType)
	in.Name = strings.TrimSpace(in.Name)
	in.Description = strings.TrimSpace(in.Description)
	in.VipCycle = strings.TrimSpace(in.VipCycle)
	in.NovelScopeType = strings.TrimSpace(in.NovelScopeType)

	if in.Name == "" || len(in.Name) > 120 {
		return fmt.Errorf("%w: product name must be 1..120 characters", errProductInvalid)
	}
	if in.PriceCents <= 0 || in.PriceCents > 1000000 {
		return fmt.Errorf("%w: price_cents must be positive USD cents", errProductInvalid)
	}
	if in.Code != "" && !productCodePattern.MatchString(in.Code) {
		return fmt.Errorf("%w: code must match ^[A-Z0-9_]{3,64}$", errProductInvalid)
	}

	switch in.ProductType {
	case productTypeSingle:
		if in.Coins <= 0 || in.Bonus < 0 || in.Bonus*10 > in.Coins*15 {
			return fmt.Errorf("%w: coin packages require positive coins and bonus <= 1.5x coins", errProductInvalid)
		}
		if in.VipCycle != "" {
			return fmt.Errorf("%w: coin packages cannot specify vip_cycle", errProductInvalid)
		}
		in.NovelScopeType = scopeNone
		in.NovelScopeIDs = []int64{}
	case productTypeWholeBook:
		if in.Coins != 0 || in.Bonus != 0 {
			return fmt.Errorf("%w: whole-book products do not grant coins or bonus coins", errProductInvalid)
		}
		if in.VipCycle != "" {
			return fmt.Errorf("%w: whole-book products cannot specify vip_cycle", errProductInvalid)
		}
		switch in.NovelScopeType {
		case "":
			in.NovelScopeType = scopeAllPublished
			in.NovelScopeIDs = []int64{}
		case scopeAllPublished, scopePromotedNovel:
			in.NovelScopeIDs = []int64{}
		case scopeSpecificNovels:
			slices.Sort(in.NovelScopeIDs)
			in.NovelScopeIDs = slices.Compact(in.NovelScopeIDs)
			if len(in.NovelScopeIDs) == 0 {
				return fmt.Errorf("%w: specific_novels scope requires at least one novel_id", errProductInvalid)
			}
			for _, id := range in.NovelScopeIDs {
				if id <= 0 {
					return fmt.Errorf("%w: novel_scope_ids must be positive", errProductInvalid)
				}
			}
		default:
			return fmt.Errorf("%w: invalid novel_scope_type for whole_book", errProductInvalid)
		}
	case productTypeVIP:
		if in.Coins != 0 || in.Bonus != 0 {
			return fmt.Errorf("%w: VIP subscriptions do not grant coins or bonus coins", errProductInvalid)
		}
		switch in.VipCycle {
		case "day", "week", "month", "year":
		default:
			return fmt.Errorf("%w: vip_cycle must be one of day, week, month, year", errProductInvalid)
		}
		in.NovelScopeType = scopeNone
		in.NovelScopeIDs = []int64{}
	default:
		return fmt.Errorf("%w: product_type must be single, whole_book or vip", errProductInvalid)
	}
	if in.Code == "" {
		suffix := strings.ToUpper(strings.ReplaceAll(uuid.NewString()[:8], "-", ""))
		in.Code = fmt.Sprintf("%s_%d_%s", strings.ToUpper(in.ProductType), in.PriceCents, suffix)
	}
	return nil
}

func (h *Handler) ListProducts(c *gin.Context) {
	env := c.DefaultQuery("environment", "sandbox")
	if env != "sandbox" && env != "live" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "environment must be sandbox or live"})
		return
	}
	filterType := strings.TrimSpace(c.Query("type"))
	filterStatus := strings.TrimSpace(c.Query("status"))
	search := strings.TrimSpace(c.Query("q"))
	readyOnly := c.Query("ready_only") == "true"

	items, err := loadProductViews(c.Request.Context(), 0, env, filterType, filterStatus, search)
	if err != nil {
		writeProductError(c, err)
		return
	}
	if readyOnly {
		filtered := make([]ProductView, 0, len(items))
		for _, item := range items {
			if item.DualChannelReady {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}
	if items == nil {
		items = []ProductView{}
	}
	for i := range items {
		items[i] = productForRole(items[i], c.GetString("payment_role"))
	}
	c.JSON(http.StatusOK, gin.H{"products": items, "environment": env})
}

func (h *Handler) GetProduct(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid product ID"})
		return
	}
	env := c.DefaultQuery("environment", "sandbox")
	if env != "sandbox" && env != "live" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "environment must be sandbox or live"})
		return
	}
	items, err := loadProductViews(c.Request.Context(), id, env, "", "", "")
	if err != nil {
		writeProductError(c, err)
		return
	}
	if len(items) == 0 {
		writeProductError(c, errProductNotFound)
		return
	}
	c.JSON(http.StatusOK, gin.H{"product": productForRole(items[0], c.GetString("payment_role")), "environment": env})
}

func productForRole(item ProductView, role string) ProductView {
	if role == "SuperAdmin" {
		return item
	}
	item.PayPalBinding = nil
	item.StripeBinding = nil
	item.AllBindings = nil
	item.CreatedBy = ""
	if role != "Finance" {
		item.ReadinessReasons = []string{}
	}
	return item
}

func loadProductViews(ctx context.Context, singleID int64, env, filterType, filterStatus, search string) ([]ProductView, error) {
	rows, err := db.DB.Query(ctx, `
		SELECT p.id, p.code, p.source_product_id, p.product_type, p.name, p.description,
		       p.price_cents, p.currency, p.coins, p.bonus, COALESCE(p.vip_cycle, ''),
		       p.novel_scope_type, p.novel_scope_ids, COALESCE(p.created_by, ''), p.created_at
		FROM products p
		WHERE ($1 = 0 OR p.id = $1)
		  AND ($2 = '' OR p.product_type = $2)
		  AND ($3 = '' OR p.name ILIKE '%'||$3||'%' OR p.code ILIKE '%'||$3||'%')
		  AND ($4 = '' OR EXISTS (
		      SELECT 1 FROM product_publications pub
		      WHERE pub.product_id = p.id AND pub.environment = $5 AND pub.status = $4
		  ))
		ORDER BY p.product_type ASC, p.price_cents ASC, p.id DESC`,
		singleID, filterType, search, filterStatus, env)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	products := make([]ProductView, 0)
	byID := make(map[int64]int)
	for rows.Next() {
		var p ProductView
		if err := rows.Scan(
			&p.ID, &p.Code, &p.SourceProductID, &p.ProductType, &p.Name, &p.Description,
			&p.PriceCents, &p.Currency, &p.Coins, &p.Bonus, &p.VipCycle,
			&p.NovelScopeType, &p.NovelScopeIDs, &p.CreatedBy, &p.CreatedAt,
		); err != nil {
			return nil, err
		}
		p.PriceDisplay = formatUSDCents(p.PriceCents)
		if p.NovelScopeIDs == nil {
			p.NovelScopeIDs = []int64{}
		}
		p.Publications = make(map[string]ProductPublicationView)
		p.ReadinessReasons = []string{}
		p.LinkedTemplateIDs = []int{}
		p.LinkedTemplateList = []string{}
		byID[p.ID] = len(products)
		products = append(products, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(products) == 0 {
		return products, nil
	}

	pubRows, err := db.DB.Query(ctx, `
		SELECT product_id, environment, status, published_at, paused_at, retired_at, updated_at
		FROM product_publications`)
	if err != nil {
		return nil, err
	}
	defer pubRows.Close()
	for pubRows.Next() {
		var pid int64
		var pub ProductPublicationView
		if err := pubRows.Scan(&pid, &pub.Environment, &pub.Status, &pub.PublishedAt, &pub.PausedAt, &pub.RetiredAt, &pub.UpdatedAt); err != nil {
			return nil, err
		}
		if idx, ok := byID[pid]; ok {
			products[idx].Publications[pub.Environment] = pub
		}
	}
	if err := pubRows.Err(); err != nil {
		return nil, err
	}

	type primaryInfo struct {
		id               int64
		ordersEnabled    bool
		subsEnabled      bool
		status           string
		displayName      string
		credentialsValid bool
	}
	primaries := make(map[string]primaryInfo)
	pRows, err := db.DB.Query(ctx, `
		SELECT a.provider, a.id, COALESCE(NULLIF(a.display_name, ''), a.account_key), a.status,
		       a.new_orders_enabled, a.new_subscriptions_enabled
		FROM payment_channel_accounts a
		WHERE a.environment = $1 AND a.is_primary = true`, env)
	if err != nil {
		return nil, err
	}
	defer pRows.Close()
	for pRows.Next() {
		var prov string
		var info primaryInfo
		if err := pRows.Scan(&prov, &info.id, &info.displayName, &info.status, &info.ordersEnabled, &info.subsEnabled); err != nil {
			return nil, err
		}
		info.credentialsValid = info.status == channelStatusPublished
		primaries[prov] = info
	}
	if err := pRows.Err(); err != nil {
		return nil, err
	}

	bRows, err := db.DB.Query(ctx, `
		SELECT b.id, b.product_id, b.account_id, COALESCE(NULLIF(a.display_name, ''), a.account_key),
		       a.account_key, a.is_primary, a.status, b.provider, b.environment,
		       b.external_product_ref, b.external_price_ref, b.sales_mode, b.allowed_countries,
		       COALESCE(b.tax_liability_account, ''), b.validated_catalog, COALESCE(b.release_evidence, ''),
		       COALESCE(b.market_policy, ''), b.validation_status, COALESCE(b.validation_error, ''),
		       b.review_required, b.validated_at, b.enabled
		FROM product_channel_bindings b
		JOIN payment_channel_accounts a ON a.id = b.account_id
		WHERE b.environment = $1
		ORDER BY a.is_primary DESC, b.id DESC`, env)
	if err != nil {
		return nil, err
	}
	defer bRows.Close()
	for bRows.Next() {
		var bv ChannelBindingView
		if err := bRows.Scan(
			&bv.ID, &bv.ProductID, &bv.AccountID, &bv.AccountDisplayName,
			&bv.AccountKey, &bv.AccountIsPrimary, &bv.AccountStatus, &bv.Provider, &bv.Environment,
			&bv.ExternalProductRef, &bv.ExternalPriceRef, &bv.SalesMode, &bv.AllowedCountries,
			&bv.TaxLiabilityAccount, &bv.ValidatedCatalog, &bv.ReleaseEvidence,
			&bv.MarketPolicy, &bv.ValidationStatus, &bv.ValidationError,
			&bv.ReviewRequired, &bv.ValidatedAt, &bv.Enabled,
		); err != nil {
			return nil, err
		}
		idx, ok := byID[bv.ProductID]
		if !ok {
			continue
		}
		if aCred := bv.AccountKey; aCred != "" && bv.Provider == "paypal" {
			bv.AccountKey = maskAccountKey(bv.Provider, bv.AccountKey)
		}
		bv.Ready = bindingIsReady(products[idx].ProductType, bv)
		if singleID > 0 {
			products[idx].AllBindings = append(products[idx].AllBindings, bv)
		}
		if bv.Environment != env || !bv.AccountIsPrimary {
			continue
		}
		copyBV := bv
		if bv.Provider == "paypal" {
			products[idx].PayPalBinding = &copyBV
		} else if bv.Provider == "stripe" {
			products[idx].StripeBinding = &copyBV
		}
	}
	if err := bRows.Err(); err != nil {
		return nil, err
	}

	tRows, err := db.DB.Query(ctx, `
		SELECT DISTINCT s.product_id, t.id, t.name
		FROM recharge_slots s
		JOIN recharge_templates t ON t.id = s.template_id
		ORDER BY t.id ASC`)
	if err != nil {
		return nil, err
	}
	defer tRows.Close()
	for tRows.Next() {
		var pid bigintOrInt64
		var tid int
		var tname string
		if err := tRows.Scan(&pid, &tid, &tname); err != nil {
			return nil, err
		}
		if idx, ok := byID[int64(pid)]; ok {
			products[idx].LinkedTemplateIDs = append(products[idx].LinkedTemplateIDs, tid)
			products[idx].LinkedTemplateList = append(products[idx].LinkedTemplateList, tname)
		}
	}
	if err := tRows.Err(); err != nil {
		return nil, err
	}

	for i := range products {
		p := &products[i]
		p.PayPalReady, p.StripeReady, p.ReadinessReasons = assessProductReadiness(
			p.ProductType, env,
			primaries["paypal"].id > 0 && primaries["paypal"].credentialsValid && switchEnabledForType(p.ProductType, primaries["paypal"].ordersEnabled, primaries["paypal"].subsEnabled),
			primaries["stripe"].id > 0 && primaries["stripe"].credentialsValid && switchEnabledForType(p.ProductType, primaries["stripe"].ordersEnabled, primaries["stripe"].subsEnabled),
			p.PayPalBinding, p.StripeBinding,
		)
		p.DualChannelReady = p.PayPalReady && p.StripeReady
	}
	return products, nil
}

type bigintOrInt64 int64

func switchEnabledForType(productType string, ordersOK, subsOK bool) bool {
	if productType == productTypeVIP {
		return subsOK
	}
	return ordersOK
}

func bindingIsReady(productType string, bv ChannelBindingView) bool {
	if !bv.Enabled || bv.ValidationStatus != validationVerified || bv.AccountStatus != channelStatusPublished || !bv.AccountIsPrimary {
		return false
	}
	if bv.Provider == "paypal" {
		if productType == productTypeVIP {
			return bv.ExternalPriceRef != ""
		}
		return true
	}
	if bv.ExternalProductRef == "" || bv.ExternalPriceRef == "" {
		return false
	}
	if bv.Environment == "live" && (len(bv.ValidatedCatalog) == 0 || bv.ReleaseEvidence == "" || bv.MarketPolicy == "") {
		return false
	}
	return true
}

func assessProductReadiness(productType, env string, paypalAccountOK, stripeAccountOK bool, pp, st *ChannelBindingView) (bool, bool, []string) {
	var reasons []string
	ppReady := false
	if !paypalAccountOK {
		reasons = append(reasons, fmt.Sprintf("No published primary PayPal account accepts %s checkout in %s", productType, env))
	} else if pp == nil {
		reasons = append(reasons, fmt.Sprintf("PayPal primary channel binding missing for %s", env))
	} else if !pp.Enabled {
		reasons = append(reasons, fmt.Sprintf("PayPal binding is disabled for %s", env))
	} else if pp.ValidationStatus != validationVerified {
		reasons = append(reasons, fmt.Sprintf("PayPal binding validation status is %s", pp.ValidationStatus))
	} else if productType == productTypeVIP && pp.ExternalPriceRef == "" {
		reasons = append(reasons, "PayPal VIP binding lacks an active Plan ID")
	} else {
		ppReady = true
	}

	stReady := false
	if !stripeAccountOK {
		reasons = append(reasons, fmt.Sprintf("No published primary Stripe account accepts %s checkout in %s", productType, env))
	} else if st == nil {
		reasons = append(reasons, fmt.Sprintf("Stripe primary channel binding missing for %s", env))
	} else if !st.Enabled {
		reasons = append(reasons, fmt.Sprintf("Stripe binding is disabled for %s", env))
	} else if st.ValidationStatus != validationVerified {
		reasons = append(reasons, fmt.Sprintf("Stripe binding validation status is %s", st.ValidationStatus))
	} else if st.ExternalProductRef == "" || st.ExternalPriceRef == "" {
		reasons = append(reasons, "Stripe binding lacks verified Product or Price ID")
	} else if env == "live" && (len(st.ValidatedCatalog) == 0 || st.ReleaseEvidence == "" || st.MarketPolicy == "") {
		reasons = append(reasons, "Live Stripe binding requires verified catalog, release_evidence and market_policy")
	} else {
		stReady = true
	}
	if reasons == nil {
		reasons = []string{}
	}
	return ppReady, stReady, reasons
}

func (h *Handler) CreateProduct(c *gin.Context) {
	var in productSpecInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	id, code, err := createProductRecord(c.Request.Context(), c.GetString("admin_id"), nil, in)
	if err != nil {
		writeProductError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"id": id, "code": code, "status": pubStatusDraft})
}

func (h *Handler) CopyProduct(c *gin.Context) {
	sourceID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || sourceID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid source product ID"})
		return
	}
	var req productSpecInput
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	var src productSpecInput
	err = db.DB.QueryRow(c.Request.Context(), `
		SELECT product_type, name, description, price_cents, coins, bonus, COALESCE(vip_cycle, ''), novel_scope_type, novel_scope_ids
		FROM products WHERE id = $1`, sourceID).
		Scan(&src.ProductType, &src.Name, &src.Description, &src.PriceCents, &src.Coins, &src.Bonus, &src.VipCycle, &src.NovelScopeType, &src.NovelScopeIDs)
	if errors.Is(err, pgx.ErrNoRows) {
		writeProductError(c, errProductNotFound)
		return
	}
	if err != nil {
		writeProductError(c, err)
		return
	}
	// Apply overrides from copy request while enforcing target product rules.
	if req.ProductType == "" {
		req.ProductType = src.ProductType
	}
	if req.Name == "" {
		req.Name = src.Name + " (Copy)"
	}
	if req.Description == "" {
		req.Description = src.Description
	}
	if req.PriceCents == 0 {
		req.PriceCents = src.PriceCents
	}
	if req.ProductType == productTypeSingle && req.Coins == 0 {
		req.Coins = src.Coins
	}
	if req.ProductType == productTypeVIP && req.VipCycle == "" {
		req.VipCycle = src.VipCycle
	}
	if req.ProductType == productTypeWholeBook && req.NovelScopeType == "" {
		req.NovelScopeType = src.NovelScopeType
		req.NovelScopeIDs = src.NovelScopeIDs
	}

	id, code, err := createProductRecord(c.Request.Context(), c.GetString("admin_id"), &sourceID, req)
	if err != nil {
		writeProductError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"id": id, "code": code, "source_product_id": sourceID, "status": pubStatusDraft})
}

func createProductRecord(ctx context.Context, actor string, sourceID *int64, in productSpecInput) (int64, string, error) {
	if err := normalizeAndValidateProductSpec(&in); err != nil {
		return 0, "", err
	}
	tx, err := db.DB.Begin(ctx)
	if err != nil {
		return 0, "", err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if err := requireFinancialAdmin(ctx, tx, actor); err != nil {
		return 0, "", err
	}
	if in.NovelScopeType == scopeSpecificNovels {
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM novels WHERE id = ANY($1::bigint[])`, in.NovelScopeIDs).Scan(&count); err != nil {
			return 0, "", err
		}
		if count != len(in.NovelScopeIDs) {
			return 0, "", fmt.Errorf("%w: one or more novel_scope_ids do not exist", errProductInvalid)
		}
	}

	var vipCycleArg any
	if in.VipCycle != "" {
		vipCycleArg = in.VipCycle
	}
	var id int64
	err = tx.QueryRow(ctx, `
		INSERT INTO products (
			code, source_product_id, product_type, name, description,
			price_cents, currency, coins, bonus, vip_cycle, novel_scope_type, novel_scope_ids, created_by
		) VALUES ($1, $2, $3, $4, $5, $6, 'USD', $7, $8, $9, $10, $11, $12)
		RETURNING id`,
		in.Code, sourceID, in.ProductType, in.Name, in.Description,
		in.PriceCents, in.Coins, in.Bonus, vipCycleArg, in.NovelScopeType, in.NovelScopeIDs, actor,
	).Scan(&id)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return 0, "", fmt.Errorf("%w: duplicate product code %s", errProductInvalid, in.Code)
		}
		return 0, "", err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO product_publications (product_id, environment, status, updated_by)
		VALUES ($1, 'sandbox', 'draft', $2), ($1, 'live', 'draft', $2)`, id, actor); err != nil {
		return 0, "", err
	}
	afterJSON, _ := json.Marshal(map[string]any{
		"code": in.Code, "source_product_id": sourceID, "product_type": in.ProductType,
		"name": in.Name, "price_cents": in.PriceCents, "coins": in.Coins, "bonus": in.Bonus,
		"vip_cycle": in.VipCycle, "novel_scope_type": in.NovelScopeType, "novel_scope_ids": in.NovelScopeIDs,
	})
	if err := billingAuditLogTx(ctx, tx, actor, "create_product", strconv.FormatInt(id, 10), "", string(afterJSON)); err != nil {
		return 0, "", err
	}
	return id, in.Code, tx.Commit(ctx)
}

type bindPayPalProductReq struct {
	Environment        string `json:"environment" binding:"required,oneof=sandbox live"`
	AccountID          int64  `json:"account_id"`
	ExternalProductRef string `json:"external_product_ref"`
	ExternalPriceRef   string `json:"external_price_ref"`
	Enabled            *bool  `json:"enabled"`
	Reason             string `json:"reason" binding:"max=500"`
}

func (h *Handler) UpsertProductPayPalBinding(c *gin.Context) {
	productID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || productID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid product ID"})
		return
	}
	var req bindPayPalProductReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	req.ExternalProductRef = strings.TrimSpace(req.ExternalProductRef)
	req.ExternalPriceRef = strings.TrimSpace(req.ExternalPriceRef)
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	ctx := c.Request.Context()
	actor := c.GetString("admin_id")
	if err := verifyProductSuperAdmin(ctx, actor); err != nil {
		writeProductError(c, err)
		return
	}

	var pType, pCode, pName, pDesc, pCycle string
	var priceCents int
	err = db.DB.QueryRow(ctx, `
		SELECT product_type, code, name, description, price_cents, COALESCE(vip_cycle, '')
		FROM products WHERE id = $1`, productID).
		Scan(&pType, &pCode, &pName, &pDesc, &priceCents, &pCycle)
	if errors.Is(err, pgx.ErrNoRows) {
		writeProductError(c, errProductNotFound)
		return
	}
	if err != nil {
		writeProductError(c, err)
		return
	}

	accountID := req.AccountID
	if accountID == 0 {
		err = db.DB.QueryRow(ctx, `
			SELECT id FROM payment_channel_accounts
			WHERE provider = 'paypal' AND environment = $1 AND status = 'published' AND is_primary = true
			LIMIT 1`, req.Environment).Scan(&accountID)
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(http.StatusConflict, gin.H{"error": "No published primary PayPal channel account found for " + req.Environment})
			return
		}
		if err != nil {
			writeProductError(c, err)
			return
		}
	}

	creds, _, err := loadChannelPayPalCreds(ctx, accountID, 0)
	if err != nil {
		writeProductError(c, err)
		return
	}
	if creds.Environment != req.Environment {
		c.JSON(http.StatusBadRequest, gin.H{"error": "PayPal channel account environment does not match request"})
		return
	}
	if creds.ClientID == "" || creds.ClientSecret == "" {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "PayPal channel account credentials are missing"})
		return
	}

	// Reserve idempotent request_key & mark first_sent_at before external VIP writes.
	reqKey, reviewReq, existingPlan, existingProd, err := preparePayPalBindingWriteTx(ctx, productID, accountID, req.Environment, actor)
	if err != nil {
		writeProductError(c, err)
		return
	}
	if pType == productTypeVIP && req.ExternalPriceRef == "" && reviewReq && existingPlan == "" {
		c.JSON(http.StatusConflict, gin.H{"error": "PayPal VIP plan creation previously timed out or requires manual Plan ID verification; provide the confirmed P-... Plan ID"})
		return
	}
	if req.ExternalProductRef == "" {
		req.ExternalProductRef = existingProd
	}

	var catalogJSON []byte
	var validationErr string
	validationState := validationFailed
	extProd, extPlan := req.ExternalProductRef, req.ExternalPriceRef

	if pType != productTypeVIP {
		if _, err := fetchPayPalAccessToken(ctx, paypalBaseForEnv(creds.Environment), creds.ClientID, creds.ClientSecret); err != nil {
			validationErr = err.Error()
		} else {
			validationState = validationVerified
			catalogJSON, _ = json.Marshal(map[string]any{
				"flow": "orders_v2", "currency": "USD", "amount_minor": priceCents, "product_type": pType,
				"verified_at": time.Now().UTC().Format(time.RFC3339),
			})
		}
	} else {
		if extPlan == "" && existingPlan != "" {
			extPlan = existingPlan
		}
		var verifyErr error
		extProd, extPlan, catalogJSON, reviewReq, verifyErr = ensureAndVerifyPayPalVIPPlan(
			ctx, creds, pCode, pName, pDesc, pCycle, priceCents, reqKey, extProd, extPlan,
		)
		if verifyErr != nil {
			validationErr = verifyErr.Error()
			if reviewReq {
				validationState = validationReview
			}
		} else {
			validationState = validationVerified
			reviewReq = false
		}
	}

	if err := savePayPalBindingResultTx(
		ctx, productID, accountID, req.Environment, actor,
		extProd, extPlan, catalogJSON, validationState, validationErr, reviewReq, enabled && validationState == validationVerified, req.Reason,
	); err != nil {
		writeProductError(c, err)
		return
	}
	if validationState != validationVerified {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"verified": false, "validation_status": validationState, "error": validationErr,
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"verified": true, "provider": "paypal", "environment": req.Environment, "account_id": accountID,
		"external_product_ref": extProd, "external_price_ref": extPlan, "enabled": enabled,
	})
}

func verifyProductSuperAdmin(ctx context.Context, actor string) error {
	var allowed bool
	if err := db.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM admins WHERE id=$1 AND status=1 AND role='SuperAdmin')`, actor).Scan(&allowed); err != nil {
		return err
	}
	if !allowed {
		return errCaseForbidden
	}
	return nil
}

func preparePayPalBindingWriteTx(ctx context.Context, productID, accountID int64, env, actor string) (reqKey string, reviewRequired bool, existingPlan, existingProd string, err error) {
	tx, err := db.DB.Begin(ctx)
	if err != nil {
		return "", false, "", "", err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if err := requireSuperAdminTx(ctx, tx, actor); err != nil {
		return "", false, "", "", err
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO product_channel_bindings (
			product_id, account_id, provider, environment, sales_mode, allowed_countries,
			validation_status, first_sent_at, created_by, updated_by
		) VALUES ($1, $2, 'paypal', $3, 'direct', ARRAY['US'], 'pending', now(), $4, $4)
		ON CONFLICT (product_id, account_id) DO UPDATE SET
			first_sent_at = COALESCE(product_channel_bindings.first_sent_at, now()),
			updated_by = EXCLUDED.updated_by,
			updated_at = now()
		RETURNING request_key::text, review_required, external_price_ref, external_product_ref`,
		productID, accountID, env, actor).Scan(&reqKey, &reviewRequired, &existingPlan, &existingProd)
	if err != nil {
		return "", false, "", "", err
	}
	return reqKey, reviewRequired, existingPlan, existingProd, tx.Commit(ctx)
}

func ensureAndVerifyPayPalVIPPlan(
	ctx context.Context, creds PayPalChannelCreds, code, name, desc, cycle string, priceCents int,
	reqKey, prodRef, planRef string,
) (string, string, []byte, bool, error) {
	baseURL := paypalBaseForEnv(creds.Environment)
	token, err := fetchPayPalAccessToken(ctx, baseURL, creds.ClientID, creds.ClientSecret)
	if err != nil {
		return prodRef, planRef, nil, false, err
	}
	if planRef != "" {
		if !paypalPlanIDPattern.MatchString(planRef) {
			return prodRef, planRef, nil, false, errors.New("PayPal plan_id must match ^P-[A-Z0-9]+$")
		}
		summary, resolvedProd, vErr := verifyPayPalPlanRemote(ctx, baseURL, token, planRef, cycle, priceCents)
		if vErr != nil {
			return prodRef, planRef, nil, false, vErr
		}
		if prodRef == "" {
			prodRef = resolvedProd
		}
		return prodRef, planRef, summary, false, nil
	}

	if prodRef == "" {
		if desc == "" {
			desc = "Star Novel VIP Membership: " + name
		}
		prodPayload, _ := json.Marshal(map[string]any{
			"name":        name,
			"description": desc,
			"type":        "DIGITAL",
			"category":    "BOOKS_PERIODICALS_AND_NEWSPAPERS",
		})
		prodIdem := uuid.NewSHA1(uuid.NameSpaceOID, []byte("paypal:catalog-product:"+code)).String()
		var prodResp struct {
			ID string `json:"id"`
		}
		if err := sendPayPalJSON(ctx, baseURL+"/v1/catalogs/products", token, prodIdem, prodPayload, &prodResp); err != nil {
			return prodRef, planRef, nil, true, err
		}
		if prodResp.ID == "" {
			return prodRef, planRef, nil, true, errors.New("PayPal did not return a catalog product ID")
		}
		prodRef = prodResp.ID
	}

	intervalUnit := strings.ToUpper(cycle)
	planPayload, _ := json.Marshal(map[string]any{
		"product_id":  prodRef,
		"name":        name,
		"description": desc,
		"status":      "ACTIVE",
		"billing_cycles": []map[string]any{{
			"frequency": map[string]any{
				"interval_unit":  intervalUnit,
				"interval_count": 1,
			},
			"tenure_type":  "REGULAR",
			"sequence":     1,
			"total_cycles": 0,
			"pricing_scheme": map[string]any{
				"fixed_price": map[string]any{
					"value":         fmt.Sprintf("%d.%02d", priceCents/100, priceCents%100),
					"currency_code": "USD",
				},
			},
		}},
		"payment_preferences": map[string]any{
			"auto_bill_outstanding":     true,
			"setup_fee_failure_action":  "CONTINUE",
			"payment_failure_threshold": 3,
		},
	})
	planIdem := uuid.NewSHA1(uuid.NameSpaceOID, []byte("paypal:billing-plan:"+reqKey)).String()
	var planResp struct {
		ID string `json:"id"`
	}
	if err := sendPayPalJSON(ctx, baseURL+"/v1/billing/plans", token, planIdem, planPayload, &planResp); err != nil {
		return prodRef, planRef, nil, true, err
	}
	if planResp.ID == "" {
		return prodRef, planRef, nil, true, errors.New("PayPal did not return a billing plan ID")
	}
	planRef = planResp.ID
	summary, _, err := verifyPayPalPlanRemote(ctx, baseURL, token, planRef, cycle, priceCents)
	if err != nil {
		return prodRef, planRef, nil, true, err
	}
	return prodRef, planRef, summary, false, nil
}

func sendPayPalJSON(ctx context.Context, endpoint, token, idemKey string, body []byte, out any) error {
	var reader io.Reader
	method := http.MethodGet
	if body != nil {
		method = http.MethodPost
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if idemKey != "" {
		req.Header.Set("PayPal-Request-Id", idemKey)
	}
	resp, err := channelHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("PayPal API returned status %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
}

func verifyPayPalPlanRemote(ctx context.Context, baseURL, token, planID, expectedCycle string, expectedCents int) ([]byte, string, error) {
	var plan struct {
		ID            string `json:"id"`
		ProductID     string `json:"product_id"`
		Status        string `json:"status"`
		BillingCycles []struct {
			Tenure    string `json:"tenure_type"`
			Total     *int   `json:"total_cycles"`
			Frequency struct {
				Unit  string `json:"interval_unit"`
				Count int    `json:"interval_count"`
			} `json:"frequency"`
			Pricing struct {
				FixedPrice struct {
					Value    string `json:"value"`
					Currency string `json:"currency_code"`
				} `json:"fixed_price"`
			} `json:"pricing_scheme"`
		} `json:"billing_cycles"`
	}
	if err := sendPayPalJSON(ctx, baseURL+"/v1/billing/plans/"+url.PathEscape(planID), token, "", nil, &plan); err != nil {
		return nil, "", err
	}
	if plan.ID != planID || plan.Status != "ACTIVE" || len(plan.BillingCycles) != 1 {
		return nil, "", errors.New("PayPal Plan must be ACTIVE with a single billing cycle")
	}
	cycle := plan.BillingCycles[0]
	if cycle.Tenure != "REGULAR" || cycle.Total == nil || *cycle.Total != 0 ||
		cycle.Frequency.Count != 1 || !strings.EqualFold(cycle.Frequency.Unit, expectedCycle) {
		return nil, "", fmt.Errorf("PayPal Plan cycle (%s x%d) does not match product vip_cycle (%s)", cycle.Frequency.Unit, cycle.Frequency.Count, expectedCycle)
	}
	if !strings.EqualFold(cycle.Pricing.FixedPrice.Currency, "USD") {
		return nil, "", errors.New("PayPal Plan currency must be USD")
	}
	parsedCents, err := parseUSDCents(cycle.Pricing.FixedPrice.Value)
	if err != nil || parsedCents != expectedCents {
		return nil, "", fmt.Errorf("PayPal Plan price (%s) differs from product price_cents (%d)", cycle.Pricing.FixedPrice.Value, expectedCents)
	}
	raw, _ := json.Marshal(plan)
	return raw, plan.ProductID, nil
}

func parseUSDCents(raw string) (int, error) {
	parts := strings.Split(strings.TrimSpace(raw), ".")
	if len(parts) > 2 {
		return 0, errors.New("invalid USD amount")
	}
	dollars, err := strconv.Atoi(parts[0])
	if err != nil || dollars < 0 {
		return 0, errors.New("invalid USD dollars")
	}
	cents := 0
	if len(parts) == 2 {
		frac := parts[1]
		if len(frac) == 1 {
			frac += "0"
		} else if len(frac) > 2 {
			return 0, errors.New("fractional cents not supported")
		}
		cents, err = strconv.Atoi(frac)
		if err != nil || cents < 0 {
			return 0, errors.New("invalid USD cents")
		}
	}
	return dollars*100 + cents, nil
}

func savePayPalBindingResultTx(
	ctx context.Context, productID, accountID int64, env, actor, prodRef, planRef string,
	catalog []byte, state, errMsg string, reviewReq, enabled bool, reason string,
) error {
	tx, err := db.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if err := requireSuperAdminTx(ctx, tx, actor); err != nil {
		return err
	}
	var valAt any
	if state == validationVerified {
		valAt = time.Now().UTC()
	}
	_, err = tx.Exec(ctx, `
		UPDATE product_channel_bindings
		SET external_product_ref = $3,
		    external_price_ref = $4,
		    validated_catalog = $5::jsonb,
		    validation_status = $6,
		    validation_error = NULLIF($7, ''),
		    review_required = $8,
		    validated_at = $9,
		    enabled = $10,
		    updated_by = $11,
		    updated_at = now()
		WHERE product_id = $1 AND account_id = $2`,
		productID, accountID, prodRef, planRef, stringOrNilJSON(catalog), state, errMsg, reviewReq, valAt, enabled, actor)
	if err != nil {
		return err
	}
	auditAfter, _ := json.Marshal(map[string]any{
		"provider": "paypal", "environment": env, "account_id": accountID,
		"external_product_ref": prodRef, "external_price_ref": planRef,
		"validation_status": state, "enabled": enabled, "reason": strings.TrimSpace(reason),
	})
	if err := billingAuditLogTx(ctx, tx, actor, "bind_product_paypal", strconv.FormatInt(productID, 10), "", string(auditAfter)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type bindStripeProductReq struct {
	Environment         string   `json:"environment" binding:"required,oneof=sandbox live"`
	AccountID           int64    `json:"account_id"`
	ProductRef          string   `json:"product_ref" binding:"max=100"`
	PriceRef            string   `json:"price_ref" binding:"max=100"`
	SalesMode           string   `json:"sales_mode" binding:"omitempty,oneof=direct merchant_of_record"`
	TaxLiabilityAccount string   `json:"tax_liability_account" binding:"max=100"`
	TaxCode             string   `json:"tax_code" binding:"max=64"`
	TaxBehavior         string   `json:"tax_behavior" binding:"omitempty,oneof=exclusive inclusive"`
	AllowedCountries    []string `json:"countries" binding:"max=250"`
	ReleaseEvidence     string   `json:"release_evidence" binding:"max=500"`
	MarketPolicy        string   `json:"market_policy" binding:"max=500"`
	Enabled             *bool    `json:"enabled"`
	Reason              string   `json:"reason" binding:"max=500"`
}

func normalizeStripeBindingReq(req *bindStripeProductReq) error {
	if strings.TrimSpace(req.ProductRef) != "" || strings.TrimSpace(req.PriceRef) != "" {
		return errors.New("Stripe binding only supports automatic provisioning; do not pass product_ref or price_ref")
	}
	req.TaxLiabilityAccount = strings.TrimSpace(req.TaxLiabilityAccount)
	req.TaxCode = strings.TrimSpace(req.TaxCode)
	req.TaxBehavior = strings.TrimSpace(req.TaxBehavior)
	req.ReleaseEvidence = strings.TrimSpace(req.ReleaseEvidence)
	req.MarketPolicy = strings.TrimSpace(req.MarketPolicy)
	if req.SalesMode == "" {
		req.SalesMode = stripeDirect
	}
	if len(req.AllowedCountries) == 0 {
		req.AllowedCountries = []string{"US"}
	}
	for i := range req.AllowedCountries {
		req.AllowedCountries[i] = strings.ToUpper(strings.TrimSpace(req.AllowedCountries[i]))
		if !countryISO2Pattern.MatchString(req.AllowedCountries[i]) {
			return errors.New("countries must contain 2-letter ISO codes")
		}
	}
	slices.Sort(req.AllowedCountries)
	req.AllowedCountries = slices.Compact(req.AllowedCountries)

	if (req.SalesMode == stripeDirect && req.TaxLiabilityAccount != "") ||
		(req.SalesMode == stripeManagedPayments && !stripeAccountRef.MatchString(req.TaxLiabilityAccount)) {
		return errors.New("tax_liability_account must be empty for direct mode or valid acct_* for merchant_of_record")
	}
	if req.TaxCode != "" && !stripeTaxCodePattern.MatchString(req.TaxCode) {
		return errors.New("tax_code must match txcd_*")
	}
	if req.SalesMode == stripeManagedPayments {
		if req.TaxCode == "" {
			req.TaxCode = defaultStripeDigitalTaxCode
		}
		if req.TaxBehavior == "" {
			req.TaxBehavior = "exclusive"
		}
	}
	if req.Environment == "live" && (req.ReleaseEvidence == "" || req.MarketPolicy == "") {
		return errors.New("Live Stripe bindings require release_evidence and market_policy")
	}
	return nil
}

func (h *Handler) UpsertProductStripeBinding(c *gin.Context) {
	productID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || productID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid product ID"})
		return
	}
	var req bindStripeProductReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := normalizeStripeBindingReq(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	ctx := c.Request.Context()
	actor := c.GetString("admin_id")
	if err := verifyProductSuperAdmin(ctx, actor); err != nil {
		writeProductError(c, err)
		return
	}

	var pType, pCode, pName, pDesc, pCycle string
	var priceCents int
	err = db.DB.QueryRow(ctx, `
		SELECT product_type, code, name, description, price_cents, COALESCE(vip_cycle, '')
		FROM products WHERE id = $1`, productID).
		Scan(&pType, &pCode, &pName, &pDesc, &priceCents, &pCycle)
	if errors.Is(err, pgx.ErrNoRows) {
		writeProductError(c, errProductNotFound)
		return
	}
	if err != nil {
		writeProductError(c, err)
		return
	}

	accountID := req.AccountID
	if accountID == 0 {
		err = db.DB.QueryRow(ctx, `
			SELECT id FROM payment_channel_accounts
			WHERE provider = 'stripe' AND environment = $1 AND status = 'published' AND is_primary = true
			LIMIT 1`, req.Environment).Scan(&accountID)
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(http.StatusConflict, gin.H{"error": "No published primary Stripe channel account found for " + req.Environment})
			return
		}
		if err != nil {
			writeProductError(c, err)
			return
		}
	}

	key, accountKey, acctEnv, _, err := loadChannelStripeKey(ctx, accountID, 0)
	if err != nil {
		writeProductError(c, err)
		return
	}
	if acctEnv != req.Environment {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Stripe channel account environment does not match request"})
		return
	}

	reqKey, existingProd, existingPrice, err := prepareStripeBindingWriteTx(ctx, productID, accountID, actor, req)
	if err != nil {
		writeProductError(c, err)
		return
	}

	prodRef, priceRef, catalogBytes, verifyErr := ensureAndVerifyStripeCatalog(
		ctx, accountKey, key, req, productID, pType, pCode, pName, pDesc, pCycle, priceCents, reqKey, existingProd, existingPrice,
	)
	valState := validationVerified
	valErr := ""
	if verifyErr != nil {
		valState = validationFailed
		valErr = verifyErr.Error()
	}
	effectiveEnabled := enabled && valState == validationVerified
	if err := saveStripeBindingResultTx(
		ctx, productID, accountID, actor, req, prodRef, priceRef, catalogBytes, valState, valErr, effectiveEnabled,
	); err != nil {
		writeProductError(c, err)
		return
	}
	if verifyErr != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"verified": false, "external_product_ref": prodRef, "external_price_ref": priceRef, "error": valErr,
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"verified": true, "provider": "stripe", "environment": req.Environment, "account_id": accountID,
		"external_product_ref": prodRef, "external_price_ref": priceRef, "enabled": effectiveEnabled,
	})
}

func prepareStripeBindingWriteTx(
	ctx context.Context, productID, accountID int64, actor string, req bindStripeProductReq,
) (reqKey, existingProd, existingPrice string, err error) {
	tx, err := db.DB.Begin(ctx)
	if err != nil {
		return "", "", "", err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if err := requireSuperAdminTx(ctx, tx, actor); err != nil {
		return "", "", "", err
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO product_channel_bindings (
			product_id, account_id, provider, environment, sales_mode, allowed_countries, tax_liability_account,
			release_evidence, market_policy, validation_status, first_sent_at, created_by, updated_by
		) VALUES (
			$1, $2, 'stripe', $3, $4, $5, NULLIF($6, ''),
			NULLIF($7, ''), NULLIF($8, ''), 'pending', now(), $9, $9
		)
		ON CONFLICT (product_id, account_id) DO UPDATE SET
			first_sent_at = COALESCE(product_channel_bindings.first_sent_at, now()),
			updated_by = EXCLUDED.updated_by,
			updated_at = now()
		RETURNING request_key::text, external_product_ref, external_price_ref`,
		productID, accountID, req.Environment, req.SalesMode, req.AllowedCountries, req.TaxLiabilityAccount,
		req.ReleaseEvidence, req.MarketPolicy, actor).Scan(&reqKey, &existingProd, &existingPrice)
	if err != nil {
		return "", "", "", err
	}
	return reqKey, existingProd, existingPrice, tx.Commit(ctx)
}

func saveStripeBindingResultTx(
	ctx context.Context, productID, accountID int64, actor string, req bindStripeProductReq,
	prodRef, priceRef string, catalogBytes []byte, valState, valErr string, effectiveEnabled bool,
) error {
	tx, err := db.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if err := requireSuperAdminTx(ctx, tx, actor); err != nil {
		return err
	}
	var valAt any
	if valState == validationVerified {
		valAt = time.Now().UTC()
	}
	_, err = tx.Exec(ctx, `
		UPDATE product_channel_bindings
		SET external_product_ref = $3,
		    external_price_ref = $4,
		    sales_mode = $5,
		    allowed_countries = $6,
		    tax_liability_account = NULLIF($7, ''),
		    validated_catalog = $8::jsonb,
		    release_evidence = NULLIF($9, ''),
		    market_policy = NULLIF($10, ''),
		    validation_status = $11,
		    validation_error = NULLIF($12, ''),
		    validated_at = $13,
		    enabled = $14,
		    updated_by = $15,
		    updated_at = now()
		WHERE product_id = $1 AND account_id = $2`,
		productID, accountID, prodRef, priceRef, req.SalesMode, req.AllowedCountries, req.TaxLiabilityAccount,
		stringOrNilJSON(catalogBytes), req.ReleaseEvidence, req.MarketPolicy, valState, valErr, valAt, effectiveEnabled, actor)
	if err != nil {
		return err
	}
	auditAfter, _ := json.Marshal(map[string]any{
		"provider": "stripe", "environment": req.Environment, "account_id": accountID,
		"product_ref": prodRef, "price_ref": priceRef, "sales_mode": req.SalesMode,
		"validation_status": valState, "enabled": effectiveEnabled, "reason": strings.TrimSpace(req.Reason),
	})
	if err := billingAuditLogTx(ctx, tx, actor, "bind_product_stripe", strconv.FormatInt(productID, 10), "", string(auditAfter)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func ensureAndVerifyStripeCatalog(
	ctx context.Context, accountKey, key string, req bindStripeProductReq,
	productID int64, productType, code, name, desc, vipCycle string, priceCents int,
	reqKey, prodRef, priceRef string,
) (string, string, []byte, error) {
	if key == "" {
		return prodRef, priceRef, nil, errors.New("Stripe account API key is not configured or cannot be decrypted")
	}
	if err := validateStripeSecretFormat(req.Environment, key, ""); err != nil {
		return prodRef, priceRef, nil, err
	}
	var merchant struct {
		ID string `json:"id"`
	}
	if err := getStripeCatalogObject(ctx, "account", key, &merchant); err != nil {
		return prodRef, priceRef, nil, err
	}
	if merchant.ID != accountKey {
		return prodRef, priceRef, nil, fmt.Errorf("Stripe API key belongs to %s instead of %s", merchant.ID, accountKey)
	}

	// Reuse previously provisioned Stripe Product & Price when already valid for the target mode.
	if prodRef != "" && priceRef != "" {
		if catalogBytes, err := verifyStripeCatalogPair(ctx, req.Environment, key, prodRef, priceRef, req.SalesMode, productType, vipCycle, priceCents); err == nil {
			return prodRef, priceRef, catalogBytes, nil
		} else if req.SalesMode == stripeManagedPayments {
			// When upgrading an existing direct binding to MoR, set tax_code and issue a Price with tax_behavior.
			if err := updateStripeCatalogProductTaxCode(ctx, key, prodRef, req.TaxCode); err != nil {
				return prodRef, priceRef, nil, err
			}
			priceRef = ""
		} else {
			return prodRef, priceRef, nil, err
		}
	}

	if prodRef == "" {
		prodIdem := uuid.NewSHA1(uuid.NameSpaceOID, []byte("stripe:product:"+reqKey)).String()
		createdProd, err := createStripeCatalogProduct(ctx, key, prodIdem, name, desc, req.Environment, code, productID, productType, req.TaxCode)
		if err != nil {
			return prodRef, priceRef, nil, err
		}
		prodRef = createdProd
	} else if req.SalesMode == stripeManagedPayments && req.TaxCode != "" {
		if err := updateStripeCatalogProductTaxCode(ctx, key, prodRef, req.TaxCode); err != nil {
			return prodRef, priceRef, nil, err
		}
	}

	if priceRef == "" {
		priceIdem := uuid.NewSHA1(uuid.NameSpaceOID, []byte("stripe:price:"+reqKey+":"+req.SalesMode+":"+req.TaxBehavior)).String()
		createdPrice, err := createStripeCatalogPrice(ctx, key, priceIdem, prodRef, priceCents, productType, vipCycle, req.Environment, code, productID, req.TaxBehavior)
		if err != nil {
			return prodRef, priceRef, nil, err
		}
		priceRef = createdPrice
	}

	catalogBytes, err := verifyStripeCatalogPair(ctx, req.Environment, key, prodRef, priceRef, req.SalesMode, productType, vipCycle, priceCents)
	return prodRef, priceRef, catalogBytes, err
}

func verifyStripeCatalogPair(ctx context.Context, environment, key, prodRef, priceRef, salesMode, productType, vipCycle string, expectedCents int) ([]byte, error) {
	var price stripeCatalogPrice
	if err := getStripeCatalogObject(ctx, "prices/"+url.PathEscape(priceRef)+"?expand%5B%5D=product", key, &price); err != nil {
		return nil, err
	}
	live := environment == "live"
	if price.ID != priceRef || price.Product.ID != prodRef || !price.Active || !price.Product.Active ||
		price.Live != live || price.Product.Live != live || !strings.EqualFold(price.Currency, "usd") {
		return nil, errors.New("Stripe Price/Product must be active, USD and match this environment")
	}
	if int(price.UnitAmount) != expectedCents {
		return nil, fmt.Errorf("Stripe Price unit_amount (%d) differs from product price_cents (%d)", price.UnitAmount, expectedCents)
	}
	if productType == productTypeVIP {
		if price.Kind != "recurring" || price.Recurring == nil || price.Recurring.IntervalCount != 1 || price.Recurring.Interval != vipCycle {
			return nil, fmt.Errorf("Stripe Price must be recurring every 1 %s", vipCycle)
		}
	} else if price.Kind != "one_time" {
		return nil, errors.New("Stripe Price for single/whole_book products must have type=one_time")
	}
	if salesMode == stripeManagedPayments && (price.Product.TaxCode == "" || (live && price.TaxBehavior != "inclusive" && price.TaxBehavior != "exclusive")) {
		return nil, errors.New("Managed Payments requires a Stripe product tax_code and explicit tax_behavior")
	}
	return json.Marshal(price)
}

type productStatusReq struct {
	Environment string `json:"environment" binding:"required,oneof=sandbox live"`
	Reason      string `json:"reason" binding:"max=500"`
}

func (h *Handler) PublishProduct(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid product ID"})
		return
	}
	var req productStatusReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx := c.Request.Context()
	actor := c.GetString("admin_id")
	if req.Environment == "live" {
		if err := verifyProductSuperAdmin(ctx, actor); err != nil {
			writeProductError(c, err)
			return
		}
		if strings.TrimSpace(req.Reason) == "" {
			writeProductError(c, fmt.Errorf("%w: a reason is required for live publication", errProductInvalid))
			return
		}
	}

	// Pre-evaluate dual-channel readiness outside lock for crisp diagnostics, then re-verify inside lock.
	items, err := loadProductViews(ctx, id, req.Environment, "", "", "")
	if err != nil {
		writeProductError(c, err)
		return
	}
	if len(items) == 0 {
		writeProductError(c, errProductNotFound)
		return
	}
	if !items[0].DualChannelReady {
		c.JSON(http.StatusConflict, gin.H{
			"error":             errProductGateFailed.Error(),
			"paypal_ready":      items[0].PayPalReady,
			"stripe_ready":      items[0].StripeReady,
			"readiness_reasons": items[0].ReadinessReasons,
		})
		return
	}

	tx, err := db.DB.Begin(ctx)
	if err != nil {
		writeProductError(c, err)
		return
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	authorize := requireFinancialAdmin
	if req.Environment == "live" {
		authorize = requireSuperAdminTx
	}
	if err := authorize(ctx, tx, actor); err != nil {
		writeProductError(c, err)
		return
	}
	var pType string
	if err := tx.QueryRow(ctx, `SELECT product_type FROM products WHERE id = $1 FOR SHARE`, id).Scan(&pType); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeProductError(c, errProductNotFound)
			return
		}
		writeProductError(c, err)
		return
	}

	var ppReady, stReady bool
	err = tx.QueryRow(ctx, `
		SELECT
		  EXISTS (
		    SELECT 1 FROM product_channel_bindings b
		    JOIN payment_channel_accounts a ON a.id = b.account_id
		    WHERE b.product_id = $1 AND b.provider = 'paypal' AND b.environment = $2
		      AND a.status = 'published' AND a.is_primary = true
		      AND b.enabled = true AND b.validation_status = 'verified'
		      AND (($3 = 'vip' AND a.new_subscriptions_enabled AND b.external_price_ref <> '')
		        OR ($3 <> 'vip' AND a.new_orders_enabled))
		  ),
		  EXISTS (
		    SELECT 1 FROM product_channel_bindings b
		    JOIN payment_channel_accounts a ON a.id = b.account_id
		    WHERE b.product_id = $1 AND b.provider = 'stripe' AND b.environment = $2
		      AND a.status = 'published' AND a.is_primary = true
		      AND b.enabled = true AND b.validation_status = 'verified'
		      AND b.external_product_ref <> '' AND b.external_price_ref <> ''
		      AND ($2 = 'sandbox' OR (b.validated_catalog IS NOT NULL AND b.release_evidence IS NOT NULL AND b.market_policy IS NOT NULL))
		      AND (($3 = 'vip' AND a.new_subscriptions_enabled) OR ($3 <> 'vip' AND a.new_orders_enabled))
		  )`, id, req.Environment, pType).Scan(&ppReady, &stReady)
	if err != nil {
		writeProductError(c, err)
		return
	}
	if !ppReady || !stReady {
		c.JSON(http.StatusConflict, gin.H{
			"error":        errProductGateFailed.Error(),
			"paypal_ready": ppReady,
			"stripe_ready": stReady,
		})
		return
	}

	var currentStatus string
	err = tx.QueryRow(ctx, `SELECT status FROM product_publications WHERE product_id = $1 AND environment = $2 FOR UPDATE`, id, req.Environment).Scan(&currentStatus)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		writeProductError(c, err)
		return
	}
	if currentStatus == pubStatusRetired {
		c.JSON(http.StatusConflict, gin.H{"error": "Retired products cannot be re-published; copy to create a new product"})
		return
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO product_publications (product_id, environment, status, published_at, published_by, paused_at, updated_by, updated_at)
		VALUES ($1, $2, 'published', now(), $3, NULL, $3, now())
		ON CONFLICT (product_id, environment) DO UPDATE SET
			status = 'published',
			published_at = COALESCE(product_publications.published_at, now()),
			published_by = COALESCE(product_publications.published_by, EXCLUDED.published_by),
			paused_at = NULL,
			updated_by = EXCLUDED.updated_by,
			updated_at = now()`, id, req.Environment, actor)
	if err != nil {
		writeProductError(c, err)
		return
	}
	auditAfter, _ := json.Marshal(map[string]any{"environment": req.Environment, "status": pubStatusPublished, "reason": strings.TrimSpace(req.Reason)})
	if err := billingAuditLogTx(ctx, tx, actor, "publish_product", strconv.FormatInt(id, 10), "", string(auditAfter)); err != nil {
		writeProductError(c, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeProductError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"published": true, "environment": req.Environment, "status": pubStatusPublished})
}

func (h *Handler) PauseProduct(c *gin.Context) {
	h.transitionProductStatus(c, pubStatusPaused, "pause_product")
}

func (h *Handler) RetireProduct(c *gin.Context) {
	h.transitionProductStatus(c, pubStatusRetired, "retire_product")
}

func (h *Handler) transitionProductStatus(c *gin.Context, targetStatus, auditAction string) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid product ID"})
		return
	}
	var req productStatusReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx := c.Request.Context()
	actor := c.GetString("admin_id")
	tx, err := db.DB.Begin(ctx)
	if err != nil {
		writeProductError(c, err)
		return
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if err := requireFinancialAdmin(ctx, tx, actor); err != nil {
		writeProductError(c, err)
		return
	}
	var currentStatus string
	err = tx.QueryRow(ctx, `
		SELECT status FROM product_publications
		WHERE product_id = $1 AND environment = $2 FOR UPDATE`, id, req.Environment).Scan(&currentStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		writeProductError(c, errProductNotFound)
		return
	}
	if err != nil {
		writeProductError(c, err)
		return
	}
	if targetStatus == pubStatusPaused && currentStatus != pubStatusPublished {
		c.JSON(http.StatusConflict, gin.H{"error": "Only published products can be paused"})
		return
	}
	_, err = tx.Exec(ctx, `
		UPDATE product_publications
		SET status = $3,
		    paused_at = CASE WHEN $3 = 'paused' THEN now() ELSE paused_at END,
		    retired_at = CASE WHEN $3 = 'retired' THEN now() ELSE retired_at END,
		    updated_by = $4,
		    updated_at = now()
		WHERE product_id = $1 AND environment = $2`, id, req.Environment, targetStatus, actor)
	if err != nil {
		writeProductError(c, err)
		return
	}
	beforeJSON, _ := json.Marshal(map[string]any{"environment": req.Environment, "status": currentStatus})
	afterJSON, _ := json.Marshal(map[string]any{"environment": req.Environment, "status": targetStatus, "reason": strings.TrimSpace(req.Reason)})
	if err := billingAuditLogTx(ctx, tx, actor, auditAction, strconv.FormatInt(id, 10), string(beforeJSON), string(afterJSON)); err != nil {
		writeProductError(c, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeProductError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"updated": true, "environment": req.Environment, "status": targetStatus})
}

func stringOrNilJSON(raw []byte) any {
	if len(raw) == 0 {
		return nil
	}
	return string(raw)
}

func writeProductError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, errCaseForbidden):
		c.JSON(http.StatusForbidden, gin.H{"error": "SuperAdmin permission required"})
	case errors.Is(err, ErrRefundForbidden):
		c.JSON(http.StatusForbidden, gin.H{"error": "Finance or SuperAdmin permission required"})
	case errors.Is(err, errProductNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.Is(err, errProductInvalid):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case errors.Is(err, errProductGateFailed):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, errKeyringUnavailable):
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}
