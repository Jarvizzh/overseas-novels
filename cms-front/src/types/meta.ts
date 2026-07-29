export type DailyInsight = {
  id: string;
  entity_level: 'account' | 'campaign' | 'adset' | 'ad';
  entity_id: string;
  stat_date: string;
  spend: number;
  impressions: number;
  reach: number;
  frequency: number;
  cpm: number;
  clicks: number;
  cpc: number;
  ctr: number;
  link_clicks: number;
  cost_per_link_click: number;
  link_ctr: number;
  landing_page_views: number;
  cost_per_landing_page_view: number;
  view_content_count: number;
  cost_per_view_content: number;
  add_to_cart_count: number;
  cost_per_add_to_cart: number;
  initiate_checkout_count: number;
  cost_per_initiate_checkout: number;
  complete_registration_count: number;
  cost_per_complete_registration: number;
  purchase_count: number;
  cost_per_purchase: number;
  purchase_value: number;
  purchase_roas: number;
};

export type HierarchyNode = {
  id: string;
  name: string;
  level: 'account' | 'campaign' | 'adset' | 'ad';
  status: string;
  budget?: number;
  currency?: string;
  metrics: DailyInsight;
  children?: HierarchyNode[];
};

export type DailyTrendItem = {
  stat_date: string;
  spend: number;
  revenue: number;
  roas: number;
  add_to_cart: number;
  purchases: number;
};

export type OverviewResult = {
  total_spend: number;
  total_revenue: number;
  average_roas: number;
  total_impressions: number;
  total_reach: number;
  total_clicks: number;
  total_link_clicks?: number;
  total_landing_page_views?: number;
  total_add_to_cart: number;
  total_registration?: number;
  total_purchases: number;
  daily_trend: DailyTrendItem[];
};

export type ConfigData = {
  meta_access_token: string;
  meta_business_id: string;
  meta_api_version?: string;
};
