const API_BASE = import.meta.env.VITE_API_BASE_URL || 'http://localhost:8081/api/v1/admin';

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

    // 如果是登录或注册接口本身的 401，不触发全局页面跳转，直接抛出错误供表单展示提示
    if (path.includes('/auth/login') || path.includes('/auth/register')) {
      throw new Error(errorMsg);
    }

    localStorage.removeItem('cms_token');
    localStorage.removeItem('cms_admin');
    window.location.href = '/';
    throw new Error('登录凭证已失效，请重新登录');
  }

  if (!response.ok) {
    const errorData = await response.json().catch(() => ({}));
    throw new Error(errorData.error || `请求失败 (${response.status})`);
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

export interface OperationsConfigResponse {
  ranking_config: RankingConfig;
  groups: Record<RecommendationGroupName, RecommendationEntry[]>;
}

export interface GroupItemInput {
  novel_id: number;
  sort_order: number;
  scheduled_start?: string | null;
  scheduled_end?: string | null;
}

export const operationsApi = {
  getConfig: (): Promise<OperationsConfigResponse> => apiRequest('GET', '/operations'),
  saveRankingConfig: (data: RankingConfig) => apiRequest('PUT', '/operations/ranking-config', data),
  saveGroup: (group: RecommendationGroupName, items: GroupItemInput[]) =>
    apiRequest('PUT', `/operations/groups/${group}`, { items }),
  searchPublishedNovels: (search: string, page = 1, pageSize = 30, signal?: AbortSignal) =>
    apiRequest('GET', `/novels?publish_status=published&page=${page}&page_size=${pageSize}&search=${encodeURIComponent(search)}`, undefined, false, signal),
};
