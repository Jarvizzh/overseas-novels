import { readerStorage } from './readerStorage';
const BASE_URL = import.meta.env.VITE_API_BASE_URL || 'http://localhost:8080/api/v1';

export interface CurrentIntentSummary {
  intent_id: string;
  slot_id: number;
  product_id: number;
  cycle?: string;
  name?: string;
}

export class ApiError extends Error {
  status: number;
  readonly code?: string;
  readonly currentIntent?: CurrentIntentSummary;
  constructor(message: string, status: number, code?: string, currentIntent?: CurrentIntentSummary) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.code = code;
    this.currentIntent = currentIntent;
  }
}

type SessionRefreshHandler = () => Promise<string | null>;

let sessionRefreshHandler: SessionRefreshHandler | null = null;
let inFlightRefreshPromise: Promise<string | null> | null = null;

export function registerSessionRefreshHandler(handler: SessionRefreshHandler | null): void {
  sessionRefreshHandler = handler;
}

async function runSingleFlightRefresh(): Promise<string | null> {
  if (!sessionRefreshHandler) {
    return null;
  }
  if (!inFlightRefreshPromise) {
    inFlightRefreshPromise = sessionRefreshHandler().finally(() => {
      inFlightRefreshPromise = null;
    });
  }
  return inFlightRefreshPromise;
}

function getCookie(name: string): string {
  const value = `; ${document.cookie}`;
  const parts = value.split(`; ${name}=`);
  if (parts.length === 2) return parts.pop()?.split(';').shift() || '';
  return '';
}

function buildHeaders(options: RequestInit, token: string | null): Headers {
  const headers = new Headers(options.headers || {});

  if (token) {
    headers.set('Authorization', `Bearer ${token}`);
  } else {
    headers.delete('Authorization');
  }

  if (options.body && !(options.body instanceof FormData)) {
    headers.set('Content-Type', 'application/json');
  }

  const fbp = getCookie('_fbp');
  const fbc = getCookie('_fbc');
  const utmSource = readerStorage.getItem('utm_source');
  const utmCampaign = readerStorage.getItem('utm_campaign');
  const pixelId = readerStorage.getItem('fb_pixel_id');
  const promoId = readerStorage.getItem('promo_id');

  if (fbp) headers.set('X-FB-FBP', fbp);
  if (fbc) headers.set('X-FB-FBC', fbc);
  if (utmSource && /^[\x20-\x7e]{1,100}$/.test(utmSource)) headers.set('X-UTM-Source', utmSource);
  if (utmCampaign && /^[\x20-\x7e]{1,100}$/.test(utmCampaign)) headers.set('X-UTM-Campaign', utmCampaign);
  if (pixelId) headers.set('X-FB-Pixel-ID', pixelId);
  if (promoId) headers.set('X-Promo-ID', promoId);
  const source = new URL(window.location.href);
  for (const key of ['purchase', 'vip_purchase', 'stripe_vip', 'token', 'subscription_id', 'PayerID', 'ba_token']) {
    source.searchParams.delete(key);
  }
  headers.set('X-Event-Source-URL', source.href);

  return headers;
}

async function request<T>(endpoint: string, options: RequestInit & { authToken?: string | null } = {}, retried = false): Promise<T> {
  const { authToken, ...init } = options;
  const requestToken = authToken === undefined ? readerStorage.getItem('auth-token') : authToken;
  const headers = buildHeaders(init, requestToken);
  const response = await fetch(`${BASE_URL}${endpoint}`, {
    ...init,
    headers,
    credentials: authToken ? 'omit' : 'include',
  });

  const isAuthCredentialEndpoint =
    endpoint.startsWith('/auth/login') ||
    endpoint.startsWith('/auth/register') ||
    endpoint.startsWith('/auth/guest') ||
    endpoint.startsWith('/auth/refresh');

  if (response.status === 401 && !isAuthCredentialEndpoint && !retried &&
      requestToken === readerStorage.getItem('auth-token')) {
    if (authToken) {
      // Scoped work waits for the app to verify the refreshed session's owner.
      void runSingleFlightRefresh().catch(() => {});
    } else {
      const refreshedToken = await runSingleFlightRefresh();
      if (refreshedToken && refreshedToken === readerStorage.getItem('auth-token')) {
        return request<T>(endpoint, options, true);
      }
    }
  }

  if (!response.ok) {
    const errorBody = await response.json().catch(() => ({}));
    const currentIntent =
      errorBody?.current_intent && typeof errorBody.current_intent.intent_id === 'string'
        ? (errorBody.current_intent as CurrentIntentSummary)
        : undefined;
    throw new ApiError(
      errorBody.error || `HTTP error! Status: ${response.status}`,
      response.status,
      errorBody.code,
      currentIntent,
    );
  }

  if (response.status === 204) {
    return {} as T;
  }

  return response.json();
}

export interface PurchaseIntent {
  intent_id: string;
  slot_id: number;
  product_id?: number;
  novel_id?: number;
  order_type: 'single' | 'whole_book' | 'subscription';
  provider?: 'paypal' | 'stripe';
  cycle?: 'day' | 'week' | 'month' | 'year' | string;
  available_channels?: ('paypal' | 'stripe')[];
  badge?: string;
  display_alias?: string;
  amount_minor: number;
  currency: string;
  coins: number;
  status: 'ready' | 'awaiting_approval' | 'confirming' | 'review' | 'completed' | 'refunded' | 'retryable' | 'closed';
  confirmation_message?: string;
  confirmation_code?: string;
  return_path?: string;
  approve_url?: string;
  order_id?: string;
}

export interface User {
  id: number;
  email: string | null;
  nickname: string;
  avatar_url: string;
  status: number;
  created_at: string;
}

export interface Novel {
  id: number;
  title: string;
  author: string;
  cover: string;
  rating: number;
  status: 'Ongoing' | 'Completed' | 'archived';
  publish_status?: 'draft' | 'published' | 'offline';
  synopsis: string;
  genres: string[];
  words: number;
  views: number;
  created_at?: string;
}

export interface Chapter {
  content_version?: number;
  id: string;
  novel_id: number;
  chapter_index: number;
  title: string;
  content?: string;
  word_count: number;
  is_paid: boolean;
  price: number;
  created_at: string;
}

export interface ProgressUpdate {
  novel_id: number;
  chapter_index: number;
  scroll_offset_percentage: number;
  updated_at: string;
  version: string;
}

export type SavedProgress = Omit<ProgressUpdate, 'version'> & { version?: string };

export interface BookshelfItem {
  user_id: number;
  novel_id: number;
  chapter_index: number;
  scroll_offset_percentage: number;
  in_shelf: boolean;
  progress_version: string;
  updated_at: string;
  novel: Novel;
}

export interface ShelfPage {
  items: BookshelfItem[];
  next_cursor?: string;
}

export interface OperationsCatalog {
  featured: Novel[];
  curated: Novel[];
  rankings: Novel[];
  ranking_rule: string;
}

type ShelfRequestOptions = { authToken?: string; signal?: AbortSignal };

export interface Wallet {
  user_id: number;
  charged_coins: number;
  bonus_coins: number;
  updated_at: string;
}

export interface Transaction {
  id: string;
  user_id: number;
  type: 'credit' | 'debit';
  biz_type: 'recharge' | 'checkin' | 'unlock' | 'reward_task';
  amount: number;
  charged_amount: number;
  bonus_amount: number;
  desc: string;
  date: string;
}

export interface TransactionPage {
  items: Transaction[];
  next_cursor?: string;
}

export interface ChapterContentResponse {
  content_version: number;
  purchase_needs_review?: boolean;
  chapter: Chapter;
  locked: boolean;
  price: number;
  access_type?: 'free' | 'chapter' | 'pass';
}

export interface CheckInState {
  checked_in: boolean;
  day: number;
  coins_awarded: number;
  rewards: number[];
}

export type SubscriptionNormalizedStatus =
  | 'none'
  | 'active'
  | 'pending'
  | 'cancel_pending'
  | 'canceled_with_paid_period'
  | 'canceled'
  | 'renewal_failed';

export interface SubscriptionCapabilities {
  can_cancel_auto_renew: boolean;
  cancel_pending: boolean;
  can_open_billing_portal: boolean;
  billing_portal_endpoint?: string;
  can_open_mor_link_portal: boolean;
  mor_link_url?: string;
}

export interface SubscriptionInfo {
  id: number;
  user_id: number;
  subscription_id: string;
  plan_id: string;
  plan_name?: string;
  slot_id: number;
  template_id: number;
  status: string;
  normalized_status?: SubscriptionNormalizedStatus;
  cycle: string;
  price_cents: number;
  currency: string;
  payment_method: string;
  provider?: 'paypal' | 'stripe' | string;
  sales_mode?: 'direct' | 'merchant_of_record' | string;
  management?: 'stripe_portal' | 'stripe_link';
  renewal_failed_at?: string;
  current_period_start?: string;
  current_period_end?: string;
  paid_through?: string;
  next_billing_time?: string;
  next_billing_at?: string;
  last_payment_time?: string;
  cancelled_at?: string;
  capabilities?: SubscriptionCapabilities;
}

export interface SubscriptionStatusResponse {
  is_vip: boolean;
  status?: SubscriptionNormalizedStatus;
  subscription: SubscriptionInfo | null;
  subscriptions: SubscriptionInfo[];
}

export interface SubscriptionCancelResponse {
  subscription_id?: string;
  status?: SubscriptionNormalizedStatus;
  already_requested?: boolean;
  paid_through?: string;
  message: string;
}

async function sendShelfBatches<T>(
  items: T[],
  send: (batch: T[]) => Promise<{ message: string }>,
  keepalive = false,
): Promise<{ message: string }> {
  const batches: T[][] = [];
  for (let offset = 0; offset < items.length; offset += 100) {
    batches.push(items.slice(offset, offset + 100));
  }
  let result = { message: 'No changes' };
  if (keepalive) {
    // Schedule exit requests immediately; a later response may arrive after page teardown.
    const responses = await Promise.all(batches.map(send));
    return responses.at(-1) ?? result;
  }
  for (const batch of batches) {
    result = await send(batch);
  }
  return result;
}

export const api = {
  // === AUTH ===
  refresh: () => request<{ token: string; user: User }>('/auth/refresh', { method: 'POST', authToken: null }),
  logout: () => request<void>('/auth/logout', { method: 'POST' }),
  guestLogin: () => request<{ token: string; user: User }>('/auth/guest', { method: 'POST', authToken: null }),

  register: (body: { email: string; password: string; nickname?: string }) =>
    request<{ token: string; user: User }>('/auth/register', {
      method: 'POST',
      body: JSON.stringify(body),
    }),

  login: (body: { email: string; password: string }) =>
    request<{ token: string; user: User }>('/auth/login', {
      method: 'POST',
      body: JSON.stringify(body),
    }),

  bindEmail: (body: { email: string; password: string; nickname?: string }) =>
    request<{ token: string; user: User }>('/auth/bind', {
      method: 'POST',
      body: JSON.stringify(body),
    }),

  getProfile: (signal?: AbortSignal, authToken?: string) =>
    request<User>('/auth/profile', { signal, authToken }),

  updateProfile: (body: {
    nickname?: string;
    email?: string;
    current_password?: string;
    new_password?: string;
  }) =>
    request<{ user: User; token?: string }>('/auth/profile', {
      method: 'PUT',
      body: JSON.stringify(body),
    }),

  // === NOVELS ===
  getOperationsCatalog: (signal?: AbortSignal) =>
    request<OperationsCatalog>('/novels/operations', { signal }),
  getNovels: (params: { genre?: string; sort?: 'popular' | 'latest'; page?: number; limit?: number } = {}) => {
    const query = new URLSearchParams();
    if (params.genre) query.set('genre', params.genre);
    if (params.sort) query.set('sort', params.sort);
    if (params.page) query.set('page', String(params.page));
    if (params.limit) query.set('limit', String(params.limit));
    const queryString = query.toString();
    return request<Novel[]>(`/novels${queryString ? `?${queryString}` : ''}`);
  },

  searchNovels: (q: string, page: number = 1, limit: number = 20) =>
    request<Novel[]>(`/novels/search?q=${encodeURIComponent(q)}&page=${page}&limit=${limit}`),

  getNovelDetail: (id: number) =>
    request<Novel>(`/novels/${id}`),

  getChaptersList: (id: number) =>
    request<Chapter[]>(`/novels/${id}/chapters`),

  getChapterContent: (id: number, index: number, userId: number) =>
    request<ChapterContentResponse>(`/novels/${id}/chapters/${index}?user_id=${userId}`),

  // === SHELF ===
  getShelfIDs: (userId: number, options: ShelfRequestOptions) =>
    request<number[]>(`/shelf/ids?user_id=${userId}`, { ...options, cache: 'no-store' }),

  getShelfPage: (userId: number, cursor: string | undefined, options: ShelfRequestOptions) => {
    const query = new URLSearchParams({ user_id: String(userId) });
    if (cursor) query.set('cursor', cursor);
    return request<ShelfPage>(`/shelf/cursor?${query}`, { ...options, cache: 'no-store' });
  },

  getRecentBooks: (userId: number, limit: number = 20, options: ShelfRequestOptions = {}) =>
    request<BookshelfItem[]>(`/shelf/recent?user_id=${userId}&limit=${limit}`, { ...options, cache: 'no-store' }),

  getProgress: (novelId: number, userId: number, signal?: AbortSignal, authToken?: string) =>
    request<SavedProgress | null>(`/shelf/progress/${novelId}?user_id=${userId}`, { signal, authToken, cache: 'no-store' }),

  addToShelf: (novelId: number, options: ShelfRequestOptions = {}) =>
    request<{ message: string }>('/shelf/add', {
      ...options,
      method: 'POST',
      body: JSON.stringify({ novel_id: novelId }),
    }),

  removeFromShelf: (novelIds: number[], options: ShelfRequestOptions = {}) =>
    sendShelfBatches(novelIds, (batch) =>
      request<{ message: string }>('/shelf/remove', {
        ...options,
        method: 'POST',
        body: JSON.stringify({ novel_ids: batch }),
      }),
    ),

  syncProgress: (
    updates: SavedProgress[],
    options: { userId: number; keepalive?: boolean; signal?: AbortSignal; authToken?: string },
  ) =>
    sendShelfBatches(
      updates,
      (batch) =>
        request<{ message: string }>('/shelf/sync', {
          method: 'POST',
          keepalive: options.keepalive,
          signal: options.signal,
          authToken: options.authToken,
          body: JSON.stringify({ user_id: options.userId, progress_updates: batch }),
        }),
      options.keepalive,
    ),

  // === WALLET ===
  getWalletBalance: (signal?: AbortSignal) =>
    request<Wallet>('/wallet/balance', { signal }),

  getTransactionHistory: (limit: number, cursor?: string, signal?: AbortSignal) => {
    const params = new URLSearchParams({ limit: String(limit) });
    if (cursor) params.set('cursor', cursor);
    return request<TransactionPage>(`/wallet/history/cursor?${params}`, { signal });
  },

  unlockChapter: (novelId: number, chapterIndex: number, price: number, chapterId: string, contentVersion: number, userId: number, authToken: string) =>
    request<{ message: string }>('/wallet/unlock', {
      method: 'POST',
      authToken,
      body: JSON.stringify({ novel_id: novelId, chapter_index: chapterIndex, price, chapter_id: chapterId, content_version: contentVersion, user_id: userId }),
    }),

  initiateCheckout: (amountCents: number, coinsAmount: number) =>
    request<{ message: string }>('/wallet/recharge/initiate', {
      method: 'POST',
      body: JSON.stringify({ amount_cents: amountCents, coins_amount: coinsAmount }),
    }),

  createPayPalOrder: (slotId: number, novelId: number = 0, returnUrl?: string, cancelUrl?: string) =>
    request<{ order_id: string; approve_url: string }>('/wallet/recharge/paypal/create-order', {
      method: 'POST',
      body: JSON.stringify({
        slot_id: slotId,
        novel_id: novelId,
        return_url: returnUrl,
        cancel_url: cancelUrl,
      }),
    }),

  capturePayPalPayment: (orderId: string) =>
    request<{ coins_awarded: number; order_type: string }>('/wallet/recharge/paypal/capture', {
      method: 'POST',
      body: JSON.stringify({ order_id: orderId }),
    }),

  getPaymentMethods: (signal?: AbortSignal) =>
    request<{
      paypal_single: boolean;
      paypal_vip: boolean;
      stripe_single?: boolean;
      stripe_vip?: boolean;
      environment?: string;
      stripe_slots?: number[];
      stripe_vip_slots?: number[];
      stripe_sandbox?: boolean;
    }>('/wallet/purchase-methods', { signal, cache: 'no-store' }),

  preparePurchase: (
    slotId: number,
    novelId: number,
    requestKey: string,
    returnPath: string,
    signal?: AbortSignal,
    productId?: number,
  ) =>
    request<PurchaseIntent>('/wallet/purchase-intents', {
      method: 'POST',
      signal,
      body: JSON.stringify({
        slot_id: slotId,
        ...(productId ? { product_id: productId } : {}),
        novel_id: novelId,
        request_key: requestKey,
        return_path: returnPath,
      }),
    }),

  startPayPalCheckout: (intentId: string, signal?: AbortSignal) =>
    request<PurchaseIntent>(`/wallet/purchase-intents/${encodeURIComponent(intentId)}/checkout`, { method: 'POST', signal }),

  startStripeCheckout: (intentId: string, signal?: AbortSignal) =>
    request<PurchaseIntent>(`/wallet/purchase-intents/${encodeURIComponent(intentId)}/checkout/stripe`, { method: 'POST', signal }),

  startStripeVIP: (slotId: number, returnPath: string, signal?: AbortSignal) =>
    request<PurchaseIntent>('/wallet/subscription/stripe/checkout', {
      method: 'POST',
      body: JSON.stringify({ slot_id: slotId, return_path: returnPath }),
      signal,
    }),

  getStripeVIPPurchase: (intentId: string, signal?: AbortSignal) =>
    request<PurchaseIntent>(`/wallet/subscription/stripe/purchase/${encodeURIComponent(intentId)}`, { signal, cache: 'no-store' }),

  openStripeVIPPortal: (subscriptionId: string, returnPath: string = '/subscription') =>
    request<{ portal_url: string }>('/wallet/subscription/stripe/portal', {
      method: 'POST',
      body: JSON.stringify({ subscription_id: subscriptionId, return_path: returnPath }),
    }),

  getReturnedPurchase: (intentId: string, paypalOrderId: string, vip: boolean, signal?: AbortSignal) => {
    const query = new URLSearchParams();
    if (intentId) query.set('intent', intentId);
    if (paypalOrderId) query.set('token', paypalOrderId);
    const path = vip ? '/wallet/subscription/purchase-return' : '/wallet/purchase-intents/return';
    return request<PurchaseIntent>(`${path}?${query}`, { signal, cache: 'no-store' });
  },

  getRecentPurchases: (signal?: AbortSignal) =>
    request<{ purchases: PurchaseIntent[] }>('/wallet/purchase-intents', { signal, cache: 'no-store' }),

  getRechargeTemplates: (novelId?: number, signal?: AbortSignal) =>
    request<{
      id: number;
      name: string;
      is_default: boolean;
      slots: Array<{
        id: number;
        template_id: number;
        slot_index: number;
        product_id?: number;
        product_code?: string;
        product_type?: 'single' | 'whole_book' | 'vip';
        type: 'single' | 'subscription' | 'vip' | 'whole_book';
        coins: number;
        bonus: number;
        vip_duration: string;
        subscription_cycle?: string;
        vip_name: string;
        vip_desc: string;
        badge?: string;
        is_highlighted?: boolean;
        display_alias?: string;
        novel_scope_type?: string;
        paypal_ready?: boolean;
        stripe_ready?: boolean;
        price: string;
        price_cents: number;
      }>;
    }>(`/wallet/recharge/templates${novelId ? `?${new URLSearchParams({ novel_id: String(novelId) })}` : ''}`, {
      signal,
    }),

  createSubscription: (slotId: number, paymentMethod: string = 'paypal', returnUrl?: string, cancelUrl?: string, returnPath?: string) =>
    request<{ subscription_id: string; approve_url: string; status: string }>('/wallet/recharge/subscription/create', {
      method: 'POST',
      body: JSON.stringify({
        slot_id: slotId,
        payment_method: paymentMethod,
        return_url: returnUrl,
        cancel_url: cancelUrl,
        return_path: returnPath,
      }),
    }),

  activateSubscription: (subscriptionId: string, paymentMethod: string = 'paypal') =>
    request<{ message: string }>('/wallet/recharge/subscription/activate', {
      method: 'POST',
      body: JSON.stringify({
        subscription_id: subscriptionId,
        payment_method: paymentMethod,
      }),
    }),

  getSubscriptionStatus: (signal?: AbortSignal) =>
    request<SubscriptionStatusResponse>('/wallet/subscription/status', { signal, cache: 'no-store' }),

  cancelSubscription: (subscriptionId: string, reason: string = 'User requested cancellation') =>
    request<SubscriptionCancelResponse>('/wallet/subscription/cancel', {
      method: 'POST',
      body: JSON.stringify({
        subscription_id: subscriptionId,
        reason,
      }),
    }),

  getCheckInState: () =>
    request<CheckInState>('/wallet/rewards/checkin'),

  dailyCheckIn: () =>
    request<CheckInState>('/wallet/rewards/checkin', {
      method: 'POST',
    }),

  submitFeedback: (email: string, content: string, subject: string = '') =>
    request<{ message: string; id: number }>('/feedback', {
      method: 'POST',
      body: JSON.stringify({ email, content, subject }),
    }),
};
