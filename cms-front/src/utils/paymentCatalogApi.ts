import { apiRequest } from './api';

export type PaymentEnvironment = 'sandbox' | 'live';
export type PaymentProvider = 'paypal' | 'stripe';
export type ChannelStatus = 'draft' | 'verified' | 'published' | 'disabled';
export type ProductType = 'single' | 'whole_book' | 'vip';
export type VipCycle = 'day' | 'week' | 'month' | 'year';
export type NovelScopeType = 'none' | 'all_published' | 'promoted_novel' | 'specific_novels';
export type PublicationStatus = 'draft' | 'published' | 'paused' | 'retired';
export type StripeSalesMode = 'direct' | 'merchant_of_record';

export interface ChannelValidationSummary {
  provider_ok: boolean;
  identity_ok: boolean;
  webhook_ok: boolean;
  environment: string;
  account_id_match?: string;
  webhook_url?: string;
  checked_at: string;
  error?: string;
}

export interface ChannelAccount {
  id: number;
  provider: PaymentProvider;
  environment: PaymentEnvironment;
  display_name: string;
  account_label: string;
  account_key: string;
  merchant_id?: string;
  status: ChannelStatus;
  is_primary: boolean;
  active_version: number;
  pending_version: number;
  credentials_ref: string;
  webhook_ref: string;
  webhook_token: string;
  webhook_path: string;
  new_orders_enabled: boolean;
  new_subscriptions_enabled: boolean;
  validation_status: 'pending' | 'verified' | 'failed' | 'review';
  validation_summary?: ChannelValidationSummary | null;
  last_validated_at?: string;
  published_at?: string;
  credential_hints: Record<string, string>;
  previous_webhook_expires_at?: string;
  created_at: string;
  updated_at: string;
}

export interface CreateChannelPayload {
  provider: PaymentProvider;
  environment: PaymentEnvironment;
  display_name: string;
  account_key: string;
  merchant_id?: string;
  client_secret?: string;
  webhook_id?: string;
  api_key?: string;
  webhook_secret?: string;
}

export interface EditChannelPayload {
  display_name?: string;
  merchant_id?: string;
  client_secret?: string;
  webhook_id?: string;
  api_key?: string;
  webhook_secret?: string;
  rotate_webhook_grace_hours?: number;
}

export interface PublishChannelPayload {
  set_primary?: boolean;
  new_orders_enabled?: boolean;
  new_subscriptions_enabled?: boolean;
  expected_version?: number;
  force_switch_when_products_unready?: boolean;
}

export interface ProductPublicationView {
  environment: PaymentEnvironment;
  status: PublicationStatus;
  published_at?: string;
  paused_at?: string;
  retired_at?: string;
  updated_at: string;
}

export interface ChannelBindingView {
  id: number;
  product_id: number;
  account_id: number;
  account_display_name: string;
  account_key: string;
  account_is_primary: boolean;
  account_status: string;
  provider: PaymentProvider;
  environment: PaymentEnvironment;
  external_product_ref?: string;
  external_price_ref?: string;
  sales_mode: StripeSalesMode;
  allowed_countries: string[];
  tax_liability_account?: string;
  validated_catalog?: Record<string, unknown>;
  release_evidence?: string;
  market_policy?: string;
  validation_status: 'pending' | 'verified' | 'failed' | 'review';
  validation_error?: string;
  review_required?: boolean;
  validated_at?: string;
  enabled: boolean;
  ready: boolean;
}

export interface ProductView {
  id: number;
  code: string;
  source_product_id?: number;
  product_type: ProductType;
  name: string;
  description: string;
  price_cents: number;
  price: string;
  currency: string;
  coins: number;
  bonus: number;
  vip_cycle?: VipCycle | '';
  novel_scope_type: NovelScopeType;
  novel_scope_ids: number[];
  created_by?: string;
  created_at: string;
  publications: Record<string, ProductPublicationView>;
  paypal_binding?: ChannelBindingView;
  stripe_binding?: ChannelBindingView;
  all_bindings?: ChannelBindingView[];
  paypal_ready: boolean;
  stripe_ready: boolean;
  dual_channel_ready: boolean;
  readiness_reasons: string[];
  linked_template_ids: number[];
  linked_template_names: string[];
}

export interface CreateProductPayload {
  code?: string;
  product_type: ProductType;
  name: string;
  description?: string;
  price_cents: number;
  coins?: number;
  bonus?: number;
  vip_cycle?: VipCycle | '';
  novel_scope_type?: NovelScopeType;
  novel_scope_ids?: number[];
}

export interface BindPayPalProductPayload {
  environment: PaymentEnvironment;
  account_id?: number;
  external_product_ref?: string;
  external_price_ref?: string;
  enabled?: boolean;
  reason?: string;
}

export interface BindStripeProductPayload {
  environment: PaymentEnvironment;
  account_id?: number;
  sales_mode: StripeSalesMode;
  tax_liability_account?: string;
  tax_code?: string;
  tax_behavior?: 'exclusive' | 'inclusive';
  countries: string[];
  release_evidence?: string;
  market_policy?: string;
  reprovision?: boolean;
  enabled?: boolean;
  reason: string;
}

export interface TemplateSlotDTO {
  id?: number;
  template_id?: number;
  slot_index: number;
  product_id: number;
  badge: string;
  is_highlighted: boolean;
  display_alias: string;
  product_code?: string;
  product_type?: ProductType;
  type?: 'single' | 'whole_book' | 'subscription';
  name?: string;
  description?: string;
  coins?: number;
  bonus?: number;
  vip_duration?: string;
  subscription_cycle?: string;
  price?: string;
  price_cents?: number;
  novel_scope_type?: NovelScopeType;
  novel_scope_ids?: number[];
  published_envs?: PaymentEnvironment[];
}

export interface RechargeTemplateDTO {
  id: number;
  name: string;
  environment?: PaymentEnvironment;
  is_default: boolean;
  linked_promotion_count?: number;
  linked_promotion_names?: string[];
  slots: TemplateSlotDTO[];
  created_at: string;
  updated_at: string;
}

export interface TemplateSlotWriteItem {
  slot_index: number;
  product_id: number;
  badge: string;
  is_highlighted: boolean;
  display_alias: string;
}

export const PRODUCT_TYPE_LABELS: Record<ProductType, string> = {
  single: '金币充值',
  whole_book: '整本购买',
  vip: 'VIP 订阅',
};

export const VIP_CYCLE_LABELS: Record<VipCycle, string> = {
  day: '日卡（1 天）',
  week: '周卡（7 天）',
  month: '月卡（1 个月）',
  year: '年卡（1 年）',
};

export const NOVEL_SCOPE_LABELS: Record<NovelScopeType, string> = {
  none: '无书籍范围',
  all_published: '全站已上架小说',
  promoted_novel: '推广落地页关联小说',
  specific_novels: '指定小说集合',
};

export const CHANNEL_STATUS_META: Record<ChannelStatus, { label: string; tone: 'muted' | 'primary' | 'success' | 'warning' }> = {
  draft: { label: '草稿 / 待验证', tone: 'muted' },
  verified: { label: '已验证待发布', tone: 'primary' },
  published: { label: '已发布运行中', tone: 'success' },
  disabled: { label: '已停用', tone: 'warning' },
};

export const PUBLICATION_STATUS_META: Record<PublicationStatus, { label: string; tone: 'muted' | 'success' | 'warning' | 'error' }> = {
  draft: { label: '草稿', tone: 'muted' },
  published: { label: '已上架', tone: 'success' },
  paused: { label: '已暂停', tone: 'warning' },
  retired: { label: '已下线', tone: 'error' },
};

export function formatUSD(cents: number): string {
  return `$${((cents || 0) / 100).toFixed(2)}`;
}

export const channelsApi = {
  list: (signal?: AbortSignal): Promise<{ channels: ChannelAccount[] }> =>
    apiRequest('GET', '/payments/channels', undefined, false, signal),
  create: (payload: CreateChannelPayload): Promise<{ channel: ChannelAccount }> =>
    apiRequest('POST', '/payments/channels', payload),
  edit: (id: number, payload: EditChannelPayload): Promise<{ channel: ChannelAccount }> =>
    apiRequest('PATCH', `/payments/channels/${id}`, payload),
  validate: (id: number): Promise<{ verified: boolean; summary: ChannelValidationSummary; version?: number; error?: string }> =>
    apiRequest('POST', `/payments/channels/${id}/validate`, {}),
  publish: (id: number, payload: PublishChannelPayload): Promise<{ published: boolean; active_version: number; is_primary: boolean }> =>
    apiRequest('POST', `/payments/channels/${id}/publish`, payload),
  deactivate: (id: number): Promise<{ disabled: boolean }> =>
    apiRequest('POST', `/payments/channels/${id}/deactivate`, {}),
  delete: (id: number): Promise<{ deleted: boolean }> =>
    apiRequest('DELETE', `/payments/channels/${id}`),
};

export const productsApi = {
  list: (
    params: { environment: PaymentEnvironment; type?: string; status?: string; q?: string; ready_only?: boolean },
    signal?: AbortSignal,
  ): Promise<{ products: ProductView[]; environment: PaymentEnvironment }> => {
    const query = new URLSearchParams({ environment: params.environment });
    if (params.type) query.set('type', params.type);
    if (params.status) query.set('status', params.status);
    if (params.q?.trim()) query.set('q', params.q.trim());
    if (params.ready_only) query.set('ready_only', 'true');
    return apiRequest('GET', `/products?${query.toString()}`, undefined, false, signal);
  },
  get: (id: number, environment: PaymentEnvironment, signal?: AbortSignal): Promise<{ product: ProductView; environment: PaymentEnvironment }> =>
    apiRequest('GET', `/products/${id}?environment=${environment}`, undefined, false, signal),
  create: (payload: CreateProductPayload): Promise<{ id: number; code: string }> =>
    apiRequest('POST', '/products', payload),
  copy: (id: number, overrides: Partial<CreateProductPayload>): Promise<{ id: number; code: string; source_product_id: number }> =>
    apiRequest('POST', `/products/${id}/copy`, overrides),
  bindPayPal: (id: number, payload: BindPayPalProductPayload) =>
    apiRequest('PUT', `/products/${id}/bindings/paypal`, payload),
  bindStripe: (id: number, payload: BindStripeProductPayload) =>
    apiRequest('PUT', `/products/${id}/bindings/stripe`, payload),
  publish: (id: number, environment: PaymentEnvironment, reason = '') =>
    apiRequest('POST', `/products/${id}/publish`, { environment, reason }),
  pause: (id: number, environment: PaymentEnvironment, reason = '', force = false) =>
    apiRequest('POST', `/products/${id}/pause`, { environment, reason, force }),
  retire: (id: number, environment: PaymentEnvironment, reason = '', force = false) =>
    apiRequest('POST', `/products/${id}/retire`, { environment, reason, force }),
};

export const templatesApi = {
  list: (signal?: AbortSignal): Promise<RechargeTemplateDTO[]> =>
    apiRequest('GET', '/recharge-templates', undefined, false, signal),
  create: (payload: { name: string; environment?: PaymentEnvironment; is_default: boolean; slots: TemplateSlotWriteItem[] }) =>
    apiRequest('POST', '/recharge-templates', payload),
  update: (id: number, payload: { name: string; environment?: PaymentEnvironment; is_default: boolean; slots: TemplateSlotWriteItem[] }) =>
    apiRequest('PUT', `/recharge-templates/${id}`, payload),
  delete: (id: number) =>
    apiRequest('DELETE', `/recharge-templates/${id}`),
  setDefault: (id: number) =>
    apiRequest('POST', `/recharge-templates/${id}/set-default`, {}),
};
