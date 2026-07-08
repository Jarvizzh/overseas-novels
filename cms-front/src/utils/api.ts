const API_BASE = 'http://localhost:8081/api/v1/admin';

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
