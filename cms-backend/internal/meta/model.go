package meta

import (
	"time"
)

// AdAccount 广告账户
type AdAccount struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Currency  string    `json:"currency"`
	Timezone  string    `json:"timezone"`
	Status    string    `json:"status"`
	BmID      string    `json:"bm_id"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Campaign 广告系列
type Campaign struct {
	ID        string    `json:"id"`
	AccountID string    `json:"account_id"`
	Name      string    `json:"name"`
	Objective string    `json:"objective"`
	Status    string    `json:"status"`
	UpdatedAt time.Time `json:"updated_at"`
}

// AdSet 广告组
type AdSet struct {
	ID          string    `json:"id"`
	CampaignID  string    `json:"campaign_id"`
	AccountID   string    `json:"account_id"`
	Name        string    `json:"name"`
	DailyBudget float64   `json:"daily_budget"`
	Status      string    `json:"status"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Ad 广告
type Ad struct {
	ID          string     `json:"id"`
	AdSetID     string     `json:"ad_set_id"`
	CampaignID  string     `json:"campaign_id"`
	AccountID   string     `json:"account_id"`
	Name        string     `json:"name"`
	Status      string     `json:"status"`
	CreatedTime *time.Time `json:"created_time"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// DailyInsight 每日数据报表
type DailyInsight struct {
	ID          string `json:"id"`           // {entity_level}_{entity_id}_{stat_date}
	EntityLevel string `json:"entity_level"` // account, campaign, adset, ad
	EntityID    string `json:"entity_id"`
	StatDate    string `json:"stat_date"` // YYYY-MM-DD

	// 1. 基础与触达指标
	Spend       float64 `json:"spend"`
	Impressions int64   `json:"impressions"`
	Reach       int64   `json:"reach"`
	Frequency   float64 `json:"frequency"`
	CPM         float64 `json:"cpm"`
	Clicks      int64   `json:"clicks"`
	CPC         float64 `json:"cpc"`
	CTR         float64 `json:"ctr"`

	// 链接点击指标 (Link Clicks)
	LinkClicks       int64   `json:"link_clicks"`
	CostPerLinkClick float64 `json:"cost_per_link_click"`
	LinkCTR          float64 `json:"link_ctr"`

	// 2. 全链路转化漏斗指标
	LandingPageViews       int64   `json:"landing_page_views"`
	CostPerLandingPageView float64 `json:"cost_per_landing_page_view"`

	ViewContentCount   int64   `json:"view_content_count"`
	CostPerViewContent float64 `json:"cost_per_view_content"`

	AddToCartCount   int64   `json:"add_to_cart_count"`
	CostPerAddToCart float64 `json:"cost_per_add_to_cart"`

	InitiateCheckoutCount   int64   `json:"initiate_checkout_count"`
	CostPerInitiateCheckout float64 `json:"cost_per_initiate_checkout"`

	CompleteRegistrationCount   int64   `json:"complete_registration_count"`
	CostPerCompleteRegistration float64 `json:"cost_per_complete_registration"`

	PurchaseCount   int64   `json:"purchase_count"`
	CostPerPurchase float64 `json:"cost_per_purchase"`

	PurchaseValue float64 `json:"purchase_value"` // Revenue
	PurchaseROAS  float64 `json:"purchase_roas"`  // Revenue / Spend

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// SystemConfig 系统 API 配置表
type SystemConfig struct {
	Key       string    `json:"key"`
	Value     string    `json:"value"`
	UpdatedAt time.Time `json:"updated_at"`
}

// HierarchyNode 前端树形结构节点 (API 返回用)
type HierarchyNode struct {
	ID       string           `json:"id"`
	Name     string           `json:"name"`
	Level    string           `json:"level"` // account, campaign, adset, ad
	Status   string           `json:"status"`
	Budget   float64          `json:"budget,omitempty"`
	Currency string           `json:"currency,omitempty"`
	Metrics  DailyInsight     `json:"metrics"`
	Children []*HierarchyNode `json:"children,omitempty"`
}

type OverviewResult struct {
	TotalSpend            float64          `json:"total_spend"`
	TotalRevenue          float64          `json:"total_revenue"`
	AverageROAS           float64          `json:"average_roas"`
	TotalImpressions      int64            `json:"total_impressions"`
	TotalReach            int64            `json:"total_reach"`
	TotalClicks           int64            `json:"total_clicks"`
	TotalLinkClicks       int64            `json:"total_link_clicks"`
	TotalLandingPageViews int64            `json:"total_landing_page_views"`
	TotalAddToCart        int64            `json:"total_add_to_cart"`
	TotalRegistration     int64            `json:"total_registration"`
	TotalPurchases        int64            `json:"total_purchases"`
	DailyTrend            []DailyTrendItem `json:"daily_trend"`
}

type DailyTrendItem struct {
	StatDate  string  `json:"stat_date"`
	Spend     float64 `json:"spend"`
	Revenue   float64 `json:"revenue"`
	ROAS      float64 `json:"roas"`
	AddToCart int64   `json:"add_to_cart"`
	Purchases int64   `json:"purchases"`
}
