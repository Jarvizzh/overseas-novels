const API_BASE = import.meta.env.VITE_API_BASE_URL || 'http://localhost:8081/api/v1/admin';

export interface AdminUser {
  id: string;
  username: string;
  nickname: string;
  role: string;
}

export async function apiRequest(
  method: 'GET' | 'POST' | 'PUT' | 'DELETE',
  path: string,
  body?: any,
  isMultipart = false
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
  });

  if (response.status === 401) {
    localStorage.removeItem('cms_token');
    localStorage.removeItem('cms_admin');
    window.location.href = '/';
    throw new Error('Unauthorized');
  }

  if (!response.ok) {
    const errorData = await response.json().catch(() => ({}));
    throw new Error(errorData.error || `HTTP error! Status: ${response.status}`);
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
