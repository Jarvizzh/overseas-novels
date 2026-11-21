const API_BASE = import.meta.env.VITE_API_BASE_URL || 'http://localhost:8081/api/v1/admin';

export class ApiError extends Error {
  readonly status: number;
  readonly code?: string;
  readonly field?: string;
  readonly appIds?: string[];

  constructor(message: string, status: number, code?: string, field?: string, appIds?: string[]) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.code = code;
    this.field = field;
    this.appIds = appIds;
  }
}

export interface AdminUser {
  id: string;
  username: string;
  nickname: string;
  role: string;
}

export async function apiRequest(
  method: 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE',
  path: string,
  body?: any,
  isMultipart = false,
  signal?: AbortSignal
) {
  const token = localStorage.getItem('cms_token');
  const headers: HeadersInit = {};

  if (token) {
    headers['Authorization'] = `Bearer ${token}`;
  }

  let requestBody: any = body;
  if (body && !isMultipart) {
    headers['Content-Type'] = 'application/json';
    requestBody = JSON.stringify(body);
  }

  const response = await fetch(`${API_BASE}${path}`, {
    method,
    headers,
    body: requestBody,
    signal,
  });

  if (response.status === 401) {
    const errorData = await response.json().catch(() => ({}));
    const errorMsg = errorData.error || '用户名或密码错误';
    const errorCode = typeof errorData.code === 'string' ? errorData.code : undefined;

    // 如果是登录或注册接口本身的 401，不触发全局页面跳转，直接抛出错误供表单展示提示
    if (path.includes('/auth/login') || path.includes('/auth/register')) {
      throw new ApiError(errorMsg, response.status, errorCode);
    }

    localStorage.removeItem('cms_token');
    localStorage.removeItem('cms_admin');
    window.location.href = '/';
    throw new ApiError('登录凭证已失效，请重新登录', response.status, errorCode);
  }

  if (!response.ok) {
    const errorData = await response.json().catch(() => ({}));
    const errorCode = typeof errorData.code === 'string' ? errorData.code : undefined;
    throw new ApiError(
      errorData.error || `请求失败 (${response.status})`, response.status, errorCode,
      typeof errorData.field === 'string' ? errorData.field : undefined,
      Array.isArray(errorData.app_ids) ? errorData.app_ids.filter((id: unknown) => typeof id === 'string') : undefined,
    );
  }

  return response.json();
}

export interface SystemDomain {
  id: number;
  name: string;
  domain: string;
  type: 'main' | 'sub';
  status: number; // 1-Enabled, 2-Disabled
  is_default: boolean;
  created_at: string;
}

export const domainApi = {
  getDomains: () => apiRequest('GET', '/domains'),
  createDomain: (data: { name: string; domain: string; type: 'main' | 'sub'; is_default?: boolean }) =>
    apiRequest('POST', '/domains', data),
  updateDomainStatus: (id: number, status: number) =>
    apiRequest('PUT', `/domains/${id}/status`, { status }),
  setDefaultDomain: (id: number) =>
    apiRequest('POST', `/domains/${id}/set-default`),
  deleteDomain: (id: number) =>
    apiRequest('DELETE', `/domains/${id}`),
};

export interface FeedbackItem {
  id: number;
  user_id: number;
  email: string;
  subject: string;
  content: string;
  status: 'pending' | 'replied' | 'resolved';
  admin_reply: string;
  created_at: string;
  updated_at: string;
}

export const feedbackApi = {
  getFeedbackList: (params?: { page?: number; limit?: number; status?: string; keyword?: string }) => {
    const query = new URLSearchParams();
    if (params?.page) query.append('page', params.page.toString());
    if (params?.limit) query.append('limit', params.limit.toString());
    if (params?.status) query.append('status', params.status);
    if (params?.keyword) query.append('keyword', params.keyword);
    return apiRequest('GET', `/feedback?${query.toString()}`);
  },
  getFeedbackDetail: (id: number) => apiRequest('GET', `/feedback/${id}`),
  updateFeedback: (id: number, data: { status: string; admin_reply: string }) =>
    apiRequest('PUT', `/feedback/${id}`, data),
};

export type RankingSortRule = 'views' | 'rating' | 'latest' | 'manual';
export type RecommendationGroupName = 'editor_pick' | 'curated' | 'ranking';
export type NovelPickerSort = 'latest' | 'views' | 'rating' | 'updated';
export type PreviewNovelSource = 'pinned' | 'auto' | 'fallback';

export interface RankingConfig {
  sort_rule: RankingSortRule;
  limit: number;
}

export interface RecommendationNovel {
  id: number;
  title: string;
  author: string;
  cover_url: string;
  rating: number;
  status: string;
  publish_status: string;
  synopsis?: string;
  view_count: number;
  genres: string[];
}

export interface RecommendationEntry {
  id?: number;
  group_name: RecommendationGroupName;
  novel_id: number;
  sort_order: number;
  scheduled_start?: string | null;
  scheduled_end?: string | null;
  novel?: RecommendationNovel;
}

export interface CheckInConfig {
  rewards: number[];
}

export interface OperationsConfigResponse {
  ranking_config: RankingConfig;
  checkin_config?: CheckInConfig;
  groups: Record<RecommendationGroupName, RecommendationEntry[]>;
  available_genres?: string[];
}

export interface GroupItemInput {
  novel_id: number;
  sort_order: number;
  scheduled_start?: string | null;
  scheduled_end?: string | null;
}

export interface SaveRankingPayload {
  ranking_config?: RankingConfig;
  items?: GroupItemInput[];
}

export interface SaveRankingResponse {
  ranking_config: RankingConfig;
  items: RecommendationEntry[];
}

export interface PreviewNovelItem {
  novel: RecommendationNovel;
  source: PreviewNovelSource;
}

export interface OperationsPreviewRequest {
  at?: string;
  ranking_config?: RankingConfig;
  groups?: Partial<Record<RecommendationGroupName, GroupItemInput[]>>;
}

export interface OperationsPreviewResponse {
  at: string;
  ranking_config: RankingConfig;
  featured: PreviewNovelItem[];
  featured_is_fallback: boolean;
  new_releases: PreviewNovelItem[];
  curated: PreviewNovelItem[];
  curated_is_override: boolean;
  rankings: PreviewNovelItem[];
  ranking_manual_fallback: boolean;
}

export interface PublishedNovelsQuery {
  search?: string;
  author?: string;
  genre?: string;
  status?: string;
  sort?: NovelPickerSort;
  page?: number;
  pageSize?: number;
  signal?: AbortSignal;
}

export const operationsApi = {
  getConfig: (): Promise<OperationsConfigResponse> => apiRequest('GET', '/operations'),
  saveRankingConfig: (data: RankingConfig) => apiRequest('PUT', '/operations/ranking-config', data),
  saveRanking: (data: SaveRankingPayload): Promise<SaveRankingResponse> =>
    apiRequest('PUT', '/operations/ranking', data),
  saveCheckInConfig: (rewards: number[]): Promise<{ checkin_config: CheckInConfig }> =>
    apiRequest('PUT', '/operations/campaigns/checkin', { rewards }),
  saveGroup: (group: RecommendationGroupName, items: GroupItemInput[]) =>
    apiRequest('PUT', `/operations/groups/${group}`, { items }),
  preview: (data: OperationsPreviewRequest, signal?: AbortSignal): Promise<OperationsPreviewResponse> =>
    apiRequest('POST', '/operations/preview', data, false, signal),
  searchPublishedNovels: (query: PublishedNovelsQuery) => {
    const params = new URLSearchParams({
      publish_status: 'published',
      page: String(query.page ?? 1),
      page_size: String(query.pageSize ?? 20),
    });
    if (query.search?.trim()) params.set('search', query.search.trim());
    if (query.author?.trim()) params.set('author', query.author.trim());
    if (query.genre?.trim()) params.set('genre', query.genre.trim());
    if (query.status?.trim()) params.set('status', query.status.trim());
    if (query.sort?.trim()) params.set('sort', query.sort.trim());
    return apiRequest('GET', `/novels?${params.toString()}`, undefined, false, query.signal);
  },
};
