package meta

import (
	"context"
	"fmt"
	"log"

	"github.com/jackc/pgx/v5/pgxpool"
	"star-novel-cms/internal/db"
)

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository() *Repository {
	return &Repository{pool: db.DB}
}

func (r *Repository) GetConfig(ctx context.Context, key string) (string, error) {
	var value string
	err := r.pool.QueryRow(ctx, "SELECT value FROM meta_configs WHERE key = $1", key).Scan(&value)
	if err != nil {
		return "", err
	}
	return value, nil
}

func (r *Repository) SetConfig(ctx context.Context, key, value string) error {
	query := `
	INSERT INTO meta_configs (key, value, updated_at)
	VALUES ($1, $2, CURRENT_TIMESTAMP)
	ON CONFLICT (key) DO UPDATE
	SET value = EXCLUDED.value, updated_at = CURRENT_TIMESTAMP;`
	_, err := r.pool.Exec(ctx, query, key, value)
	return err
}

func (r *Repository) SaveAccount(ctx context.Context, acc AdAccount) error {
	query := `
	INSERT INTO meta_accounts (id, name, currency, timezone, status, bm_id, updated_at)
	VALUES ($1, $2, $3, $4, $5, $6, CURRENT_TIMESTAMP)
	ON CONFLICT (id) DO UPDATE
	SET name = EXCLUDED.name, currency = EXCLUDED.currency, timezone = EXCLUDED.timezone,
	    status = EXCLUDED.status, bm_id = EXCLUDED.bm_id, updated_at = CURRENT_TIMESTAMP;`
	_, err := r.pool.Exec(ctx, query, acc.ID, acc.Name, acc.Currency, acc.Timezone, acc.Status, acc.BmID)
	return err
}

func (r *Repository) SaveCampaign(ctx context.Context, c Campaign) error {
	query := `
	INSERT INTO meta_campaigns (id, account_id, name, objective, status, updated_at)
	VALUES ($1, $2, $3, $4, $5, CURRENT_TIMESTAMP)
	ON CONFLICT (id) DO UPDATE
	SET account_id = EXCLUDED.account_id, name = EXCLUDED.name, objective = EXCLUDED.objective,
	    status = EXCLUDED.status, updated_at = CURRENT_TIMESTAMP;`
	_, err := r.pool.Exec(ctx, query, c.ID, c.AccountID, c.Name, c.Objective, c.Status)
	return err
}

func (r *Repository) SaveAdSet(ctx context.Context, s AdSet) error {
	query := `
	INSERT INTO meta_adsets (id, campaign_id, account_id, name, daily_budget, status, updated_at)
	VALUES ($1, $2, $3, $4, $5, $6, CURRENT_TIMESTAMP)
	ON CONFLICT (id) DO UPDATE
	SET campaign_id = EXCLUDED.campaign_id, account_id = EXCLUDED.account_id, name = EXCLUDED.name,
	    daily_budget = EXCLUDED.daily_budget, status = EXCLUDED.status, updated_at = CURRENT_TIMESTAMP;`
	_, err := r.pool.Exec(ctx, query, s.ID, s.CampaignID, s.AccountID, s.Name, s.DailyBudget, s.Status)
	return err
}

func (r *Repository) SaveAd(ctx context.Context, a Ad) error {
	query := `
	INSERT INTO meta_ads (id, ad_set_id, campaign_id, account_id, name, status, created_time, updated_at)
	VALUES ($1, $2, $3, $4, $5, $6, $7, CURRENT_TIMESTAMP)
	ON CONFLICT (id) DO UPDATE
	SET ad_set_id = EXCLUDED.ad_set_id, campaign_id = EXCLUDED.campaign_id, account_id = EXCLUDED.account_id,
	    name = EXCLUDED.name, status = EXCLUDED.status, created_time = EXCLUDED.created_time, updated_at = CURRENT_TIMESTAMP;`
	_, err := r.pool.Exec(ctx, query, a.ID, a.AdSetID, a.CampaignID, a.AccountID, a.Name, a.Status, a.CreatedTime)
	return err
}

func (r *Repository) SaveDailyInsight(ctx context.Context, di DailyInsight) error {
	query := `
	INSERT INTO meta_daily_insights (
		id, entity_level, entity_id, stat_date, spend, impressions, reach, frequency, cpm,
		clicks, cpc, ctr, link_clicks, cost_per_link_click, link_ctr, landing_page_views,
		cost_per_landing_page_view, view_content_count, cost_per_view_content, add_to_cart_count,
		cost_per_add_to_cart, initiate_checkout_count, cost_per_initiate_checkout,
		complete_registration_count, cost_per_complete_registration, purchase_count,
		cost_per_purchase, purchase_value, purchase_roas, updated_at
	) VALUES (
		$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19,
		$20, $21, $22, $23, $24, $25, $26, $27, $28, $29, CURRENT_TIMESTAMP
	) ON CONFLICT (id) DO UPDATE SET
		spend = EXCLUDED.spend, impressions = EXCLUDED.impressions, reach = EXCLUDED.reach,
		frequency = EXCLUDED.frequency, cpm = EXCLUDED.cpm, clicks = EXCLUDED.clicks, cpc = EXCLUDED.cpc,
		ctr = EXCLUDED.ctr, link_clicks = EXCLUDED.link_clicks, cost_per_link_click = EXCLUDED.cost_per_link_click,
		link_ctr = EXCLUDED.link_ctr, landing_page_views = EXCLUDED.landing_page_views,
		cost_per_landing_page_view = EXCLUDED.cost_per_landing_page_view,
		view_content_count = EXCLUDED.view_content_count, cost_per_view_content = EXCLUDED.cost_per_view_content,
		add_to_cart_count = EXCLUDED.add_to_cart_count, cost_per_add_to_cart = EXCLUDED.cost_per_add_to_cart,
		initiate_checkout_count = EXCLUDED.initiate_checkout_count,
		cost_per_initiate_checkout = EXCLUDED.cost_per_initiate_checkout,
		complete_registration_count = EXCLUDED.complete_registration_count,
		cost_per_complete_registration = EXCLUDED.cost_per_complete_registration,
		purchase_count = EXCLUDED.purchase_count, cost_per_purchase = EXCLUDED.cost_per_purchase,
		purchase_value = EXCLUDED.purchase_value, purchase_roas = EXCLUDED.purchase_roas,
		updated_at = CURRENT_TIMESTAMP;`
	_, err := r.pool.Exec(ctx, query,
		di.ID, di.EntityLevel, di.EntityID, di.StatDate, di.Spend, di.Impressions, di.Reach,
		di.Frequency, di.CPM, di.Clicks, di.CPC, di.CTR, di.LinkClicks, di.CostPerLinkClick,
		di.LinkCTR, di.LandingPageViews, di.CostPerLandingPageView, di.ViewContentCount,
		di.CostPerViewContent, di.AddToCartCount, di.CostPerAddToCart, di.InitiateCheckoutCount,
		di.CostPerInitiateCheckout, di.CompleteRegistrationCount, di.CostPerCompleteRegistration,
		di.PurchaseCount, di.CostPerPurchase, di.PurchaseValue, di.PurchaseROAS,
	)
	return err
}

func (r *Repository) PurgeAllData(ctx context.Context) error {
	tables := []string{"meta_daily_insights", "meta_ads", "meta_adsets", "meta_campaigns", "meta_accounts"}
	for _, t := range tables {
		_, err := r.pool.Exec(ctx, fmt.Sprintf("DELETE FROM %s", t))
		if err != nil {
			log.Printf("[Meta Repository] Purge table %s error: %v", t, err)
		}
	}
	return nil
}

func (r *Repository) GetAccounts(ctx context.Context) ([]AdAccount, error) {
	rows, err := r.pool.Query(ctx, "SELECT id, name, currency, timezone, status, bm_id, updated_at FROM meta_accounts ORDER BY name ASC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var accs []AdAccount
	for rows.Next() {
		var a AdAccount
		if err := rows.Scan(&a.ID, &a.Name, &a.Currency, &a.Timezone, &a.Status, &a.BmID, &a.UpdatedAt); err == nil {
			accs = append(accs, a)
		}
	}
	return accs, nil
}

func (r *Repository) GetCampaignsByAccountID(ctx context.Context, accountID string) ([]Campaign, error) {
	rows, err := r.pool.Query(ctx, "SELECT id, account_id, name, objective, status, updated_at FROM meta_campaigns WHERE account_id = $1 ORDER BY name ASC", accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var camps []Campaign
	for rows.Next() {
		var c Campaign
		if err := rows.Scan(&c.ID, &c.AccountID, &c.Name, &c.Objective, &c.Status, &c.UpdatedAt); err == nil {
			camps = append(camps, c)
		}
	}
	return camps, nil
}

func (r *Repository) GetAdSetsByCampaignID(ctx context.Context, campaignID string) ([]AdSet, error) {
	rows, err := r.pool.Query(ctx, "SELECT id, campaign_id, account_id, name, daily_budget, status, updated_at FROM meta_adsets WHERE campaign_id = $1 ORDER BY name ASC", campaignID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sets []AdSet
	for rows.Next() {
		var s AdSet
		if err := rows.Scan(&s.ID, &s.CampaignID, &s.AccountID, &s.Name, &s.DailyBudget, &s.Status, &s.UpdatedAt); err == nil {
			sets = append(sets, s)
		}
	}
	return sets, nil
}

func (r *Repository) GetAdsByAdSetID(ctx context.Context, adSetID string) ([]Ad, error) {
	rows, err := r.pool.Query(ctx, "SELECT id, ad_set_id, campaign_id, account_id, name, status, created_time, updated_at FROM meta_ads WHERE ad_set_id = $1 ORDER BY name ASC", adSetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ads []Ad
	for rows.Next() {
		var a Ad
		if err := rows.Scan(&a.ID, &a.AdSetID, &a.CampaignID, &a.AccountID, &a.Name, &a.Status, &a.CreatedTime, &a.UpdatedAt); err == nil {
			ads = append(ads, a)
		}
	}
	return ads, nil
}

func (r *Repository) GetInsights(ctx context.Context, level, entityID, startDate, endDate string) ([]DailyInsight, error) {
	query := "SELECT id, entity_level, entity_id, stat_date, spend, impressions, reach, frequency, cpm, clicks, cpc, ctr, link_clicks, cost_per_link_click, link_ctr, landing_page_views, cost_per_landing_page_view, view_content_count, cost_per_view_content, add_to_cart_count, cost_per_add_to_cart, initiate_checkout_count, cost_per_initiate_checkout, complete_registration_count, cost_per_complete_registration, purchase_count, cost_per_purchase, purchase_value, purchase_roas, created_at, updated_at FROM meta_daily_insights WHERE entity_level = $1 AND entity_id = $2"
	args := []interface{}{level, entityID}

	if startDate != "" && endDate != "" {
		query += " AND stat_date >= $3 AND stat_date <= $4"
		args = append(args, startDate, endDate)
	}

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []DailyInsight
	for rows.Next() {
		var di DailyInsight
		err := rows.Scan(
			&di.ID, &di.EntityLevel, &di.EntityID, &di.StatDate, &di.Spend, &di.Impressions, &di.Reach,
			&di.Frequency, &di.CPM, &di.Clicks, &di.CPC, &di.CTR, &di.LinkClicks, &di.CostPerLinkClick,
			&di.LinkCTR, &di.LandingPageViews, &di.CostPerLandingPageView, &di.ViewContentCount,
			&di.CostPerViewContent, &di.AddToCartCount, &di.CostPerAddToCart, &di.InitiateCheckoutCount,
			&di.CostPerInitiateCheckout, &di.CompleteRegistrationCount, &di.CostPerCompleteRegistration,
			&di.PurchaseCount, &di.CostPerPurchase, &di.PurchaseValue, &di.PurchaseROAS, &di.CreatedAt, &di.UpdatedAt,
		)
		if err == nil {
			list = append(list, di)
		}
	}
	return list, nil
}

func (r *Repository) GetInsightsByLevel(ctx context.Context, level, startDate, endDate string) ([]DailyInsight, error) {
	query := "SELECT id, entity_level, entity_id, stat_date, spend, impressions, reach, frequency, cpm, clicks, cpc, ctr, link_clicks, cost_per_link_click, link_ctr, landing_page_views, cost_per_landing_page_view, view_content_count, cost_per_view_content, add_to_cart_count, cost_per_add_to_cart, initiate_checkout_count, cost_per_initiate_checkout, complete_registration_count, cost_per_complete_registration, purchase_count, cost_per_purchase, purchase_value, purchase_roas, created_at, updated_at FROM meta_daily_insights WHERE entity_level = $1"
	args := []interface{}{level}

	if startDate != "" && endDate != "" {
		query += " AND stat_date >= $2 AND stat_date <= $3"
		args = append(args, startDate, endDate)
	}
	query += " ORDER BY stat_date ASC"

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []DailyInsight
	for rows.Next() {
		var di DailyInsight
		err := rows.Scan(
			&di.ID, &di.EntityLevel, &di.EntityID, &di.StatDate, &di.Spend, &di.Impressions, &di.Reach,
			&di.Frequency, &di.CPM, &di.Clicks, &di.CPC, &di.CTR, &di.LinkClicks, &di.CostPerLinkClick,
			&di.LinkCTR, &di.LandingPageViews, &di.CostPerLandingPageView, &di.ViewContentCount,
			&di.CostPerViewContent, &di.AddToCartCount, &di.CostPerAddToCart, &di.InitiateCheckoutCount,
			&di.CostPerInitiateCheckout, &di.CompleteRegistrationCount, &di.CostPerCompleteRegistration,
			&di.PurchaseCount, &di.CostPerPurchase, &di.PurchaseValue, &di.PurchaseROAS, &di.CreatedAt, &di.UpdatedAt,
		)
		if err == nil {
			list = append(list, di)
		}
	}
	return list, nil
}
