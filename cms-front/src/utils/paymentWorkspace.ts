import { useEffect, useState } from 'react';
import { ApiError, apiRequest } from './api';

export const PAYMENT_PAGE_SIZE = 20;
export const providerLabels: Record<string, string> = { paypal: 'PayPal', stripe: 'Stripe' };
export const environmentLabels: Record<string, string> = { live: '正式', sandbox: '沙箱' };

export type PaymentScope = { provider: string; environment: string; account_id: string };
export type SettlementAccount = {
  id: number;
  provider: string;
  environment: string;
  label: string;
  auto_enabled?: boolean;
  auto_error?: string;
  auto_completed_at?: string;
};

export function paymentScope(params: URLSearchParams, defaultEnvironment = 'live'): PaymentScope {
  return {
    provider: params.get('provider') ?? '',
    environment: params.get('environment') ?? defaultEnvironment,
    account_id: params.get('account_id') ?? '',
  };
}

export function paymentParams(fields: Record<string, string | null | undefined>) {
  const result = new URLSearchParams();
  for (const [key, value] of Object.entries(fields)) if (value) result.set(key, value);
  return result;
}

export function paymentScopeLabel(scope: PaymentScope) {
  return [
    providerLabels[scope.provider] ?? scope.provider,
    environmentLabels[scope.environment] ?? '环境待核对',
    scope.account_id ? '商户 #' + scope.account_id : '商户待核对',
  ]
    .filter(Boolean)
    .join(' · ');
}

export { currencyDecimals, formatCaseAmount, paymentMoney } from './paymentCurrency';

export function paymentTime(value?: string | null) {
  return value ? new Date(value).toLocaleString('zh-CN', { hour12: false, timeZone: 'UTC' }) : '—';
}

export function paymentError(error: unknown) {
  if (error instanceof ApiError) {
    if (error.status === 403) return '当前账号没有此支付操作的权限。';
    if (error.status === 409) return '记录、金额或凭证已变化，请刷新后核对同一目标。';
    if (error.status === 404) return '找不到这条记录。';
    if (error.status < 500) return error.message;
  }
  return '支付服务暂不可用，请重试。';
}

export async function paymentGet<T>(url: string, signal?: AbortSignal): Promise<T> {
  return apiRequest('GET', url, undefined, false, signal);
}

export function usePaymentQuery<T>(url: string, revision = 0, retainOnRefresh = false) {
  const [result, setResult] = useState<{ url: string; revision: number; data?: T; error?: string }>();
  useEffect(() => {
    if (!url) return;
    const controller = new AbortController();
    void paymentGet<T>(url, controller.signal)
      .then((data) => {
        if (!controller.signal.aborted) setResult({ url, revision, data });
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted) setResult({ url, revision, error: paymentError(error) });
      });
    return () => controller.abort();
  }, [url, revision]);
  if (result?.url !== url) return undefined;
  if (result.revision === revision) return { ...result, refreshing: false };
  return retainOnRefresh && result.data ? { ...result, refreshing: true } : undefined;
}
