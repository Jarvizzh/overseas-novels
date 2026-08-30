const BASE_URL = import.meta.env.VITE_API_BASE_URL || 'http://localhost:8080/api/v1';

async function request<T>(endpoint: string, options: RequestInit = {}): Promise<T> {
  const token = localStorage.getItem('auth-token');
  const headers = new Headers(options.headers || {});
  
  if (token) {
    headers.set('Authorization', `Bearer ${token}`);
  }
  
  if (options.body && !(options.body instanceof FormData)) {
    headers.set('Content-Type', 'application/json');
  }

  // Auto-inject Facebook Pixel & UTM tracking parameters in request headers
  const getCookie = (name: string): string => {
    const value = `; ${document.cookie}`;
    const parts = value.split(`; ${name}=`);
    if (parts.length === 2) return parts.pop()?.split(';').shift() || '';
    return '';
  };

  const fbp = getCookie('_fbp');
  const fbc = getCookie('_fbc');
  const utmSource = localStorage.getItem('utm_source');
  const utmCampaign = localStorage.getItem('utm_campaign');
  const pixelId = localStorage.getItem('fb_pixel_id');
  const templateId = localStorage.getItem('recharge_template_id');
  const promoId = localStorage.getItem('promo_id');
  const pageURL = window.location.href;

  if (fbp) headers.set('X-FB-FBP', fbp);
  if (fbc) headers.set('X-FB-FBC', fbc);
  if (utmSource) headers.set('X-UTM-Source', utmSource);
  if (utmCampaign) headers.set('X-UTM-Campaign', utmCampaign);
  if (pixelId) headers.set('X-FB-Pixel-ID', pixelId);
  if (templateId) headers.set('X-Recharge-Template-ID', templateId);
  if (promoId) headers.set('X-Promo-ID', promoId);
  headers.set('X-Event-Source-URL', pageURL);

  const response = await fetch(`${BASE_URL}${endpoint}`, {
    ...options,
    headers,
  });

  if (response.status === 401) {
    localStorage.removeItem('auth-token');
    // Dispatch custom event to let App.tsx know it needs to run Guest Login
    window.dispatchEvent(new Event('auth-unauthorized'));
  }

  if (!response.ok) {
    const errorBody = await response.json().catch(() => ({}));
    throw new Error(errorBody.error || `HTTP error! Status: ${response.status}`);
  }

  // Handle empty responses
  if (response.status === 204) {
    return {} as T;
  }

  return response.json();
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
  status: 'Ongoing' | 'Completed';
  synopsis: string;
  genres: string[];
  words: number;
  views: number;
  created_at?: string;
}

export interface Chapter {
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

export interface BookshelfItem {
  user_id: number;
  novel_id: number;
  chapter_index: number;
  scroll_offset_percentage: number;
  in_shelf: boolean;
  updated_at: string;
  novel: Novel;
}

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

export interface ChapterContentResponse {
  chapter: Chapter;
  locked: boolean;
  price: number;
}

export const api = {
  // === AUTH ===
  guestLogin: () => 
    request<{ token: string; user: User }>('/auth/guest', { method: 'POST' }),
    
  register: (body: any) => 
    request<{ token: string; user: User }>('/auth/register', {
      method: 'POST',
      body: JSON.stringify(body),
    }),
    
  login: (body: any) => 
    request<{ token: string; user: User }>('/auth/login', {
      method: 'POST',
      body: JSON.stringify(body),
    }),
    
  bindEmail: (body: { email: string; password: string; nickname?: string }) =>
    request<{ token: string; user: User }>('/auth/bind', {
      method: 'POST',
      body: JSON.stringify(body),
    }),

  getProfile: () => 
    request<User>('/auth/profile'),

  // === NOVELS ===
  getNovels: (params: { genre?: string; page?: number; limit?: number } = {}) => {
    const query = new URLSearchParams();
    if (params.genre) query.set('genre', params.genre);
    if (params.page) query.set('page', String(params.page));
    if (params.limit) query.set('limit', String(params.limit));
    const queryString = query.toString();
    return request<Novel[]>(`/novels${queryString ? `?${queryString}` : ''}`);
  },

  searchNovels: (q: string, page: number = 1, limit: number = 10) =>
    request<Novel[]>(`/novels/search?q=${encodeURIComponent(q)}&page=${page}&limit=${limit}`),

  getNovelDetail: (id: number) => 
    request<Novel>(`/novels/${id}`),

  getChaptersList: (id: number) => 
    request<Chapter[]>(`/novels/${id}/chapters`),

  getChapterContent: (id: number, index: number) => 
    request<ChapterContentResponse>(`/novels/${id}/chapters/${index}`),

  // === SHELF ===
  getShelf: () => 
    request<BookshelfItem[]>('/shelf'),

  addToShelf: (novelId: number) => 
    request<{ message: string }>('/shelf/add', {
      method: 'POST',
      body: JSON.stringify({ novel_id: novelId }),
    }),

  removeFromShelf: (novelIds: number[]) => 
    request<{ message: string }>('/shelf/remove', {
      method: 'POST',
      body: JSON.stringify({ novel_ids: novelIds }),
    }),

  syncProgress: (updates: Array<{ novel_id: number; chapter_index: number; scroll_offset_percentage: number; updated_at: string }>) => 
    request<{ message: string }>('/shelf/sync', {
      method: 'POST',
      body: JSON.stringify({ progress_updates: updates }),
    }),

  // === WALLET ===
  getWalletBalance: () => 
    request<Wallet>('/wallet/balance'),

  getTransactionHistory: (page: number = 1, limit: number = 20) =>
    request<Transaction[]>(`/wallet/history?page=${page}&limit=${limit}`),

  unlockChapter: (novelId: number, chapterIndex: number) => 
    request<{ message: string }>('/wallet/unlock', {
      method: 'POST',
      body: JSON.stringify({ novel_id: novelId, chapter_index: chapterIndex }),
    }),

  initiateCheckout: (amountCents: number, coinsAmount: number) =>
    request<{ message: string }>('/wallet/recharge/initiate', {
      method: 'POST',
      body: JSON.stringify({ amount_cents: amountCents, coins_amount: coinsAmount }),
    }),

  createStripeIntent: (amountCents: number, coinsAmount: number) => 
    request<{ client_secret: string }>('/wallet/recharge/stripe', {
      method: 'POST',
      body: JSON.stringify({ amount_cents: amountCents, coins_amount: coinsAmount }),
    }),

  createPayPalOrder: (amountCents: number, coinsAmount: number, returnUrl?: string, cancelUrl?: string) =>
    request<{ order_id: string; approve_url: string }>('/wallet/recharge/paypal/create-order', {
      method: 'POST',
      body: JSON.stringify({
        amount_cents: amountCents,
        coins_amount: coinsAmount,
        return_url: returnUrl,
        cancel_url: cancelUrl,
      }),
    }),

  capturePayPalPayment: (orderId: string, coinsAmount: number) => 
    request<{ message: string }>('/wallet/recharge/paypal/capture', {
      method: 'POST',
      body: JSON.stringify({ order_id: orderId, coins_amount: coinsAmount }),
    }),

  getRechargeTemplates: () => 
    request<{
      id: number;
      name: string;
      is_default: boolean;
      slots: Array<{
        id: number;
        template_id: number;
        slot_index: number;
        type: 'single' | 'vip' | 'whole_book';
        coins: number;
        bonus: number;
        vip_duration: string;
        vip_name: string;
        vip_desc: string;
        price: string;
        price_cents: number;
      }>;
    }>('/wallet/recharge/templates'),

  dailyCheckIn: (day: number, coins: number) => 
    request<{ message: string; coins_awarded: number }>('/wallet/rewards/checkin', {
      method: 'POST',
      body: JSON.stringify({ day, coins }),
    }),

  submitFeedback: (email: string, content: string, subject: string = '') =>
    request<{ message: string; id: number }>('/feedback', {
      method: 'POST',
      body: JSON.stringify({ email, content, subject }),
    }),
};
