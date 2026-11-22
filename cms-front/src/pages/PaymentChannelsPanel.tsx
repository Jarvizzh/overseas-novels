import { useCallback, useContext, useEffect, useId, useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import { Copy, KeyRound, Plus, RefreshCw, ShieldCheck, ShieldOff } from 'lucide-react';
import { CmsDialog } from '../components/ui/CmsDialog';
import { SkeletonRows, StatusDot, TableRegion } from '../components/ui/Primitives';
import CustomSelect from '../components/CustomSelect';
import { ToastContext } from '../context/ToastContext';
import {
  CHANNEL_STATUS_META,
  type ChannelAccount,
  type ChannelValidationSummary,
  channelsApi,
  type CreateChannelPayload,
  type PaymentEnvironment,
  type PaymentProvider,
} from '../utils/paymentCatalogApi';

type SecretForm = {
  display_name: string;
  merchant_id: string;
  account_key: string;
  client_secret: string;
  webhook_id: string;
  api_key: string;
  webhook_secret: string;
  rotate_webhook_grace_hours: string;
};

const EMPTY_FORM: SecretForm = {
  display_name: '',
  merchant_id: '',
  account_key: '',
  client_secret: '',
  webhook_id: '',
  api_key: '',
  webhook_secret: '',
  rotate_webhook_grace_hours: '24',
};

function errMsg(err: unknown, fallback: string): string {
  return err instanceof Error ? err.message : fallback;
}

export function PaymentChannelsPanel() {
  const toastCtx = useContext(ToastContext);
  const showToast = useCallback(
    (msg: string, type: 'success' | 'error' | 'info' = 'success') => {
      if (toastCtx?.showToast) toastCtx.showToast(msg, type);
      else window.showToast?.(msg, type);
    },
    [toastCtx],
  );
  const [channels, setChannels] = useState<ChannelAccount[] | null>(null);
  const [loadError, setLoadError] = useState('');
  const [providerFilter, setProviderFilter] = useState<'all' | PaymentProvider>('all');
  const [envFilter, setEnvFilter] = useState<'all' | PaymentEnvironment>('all');

  const [createOpen, setCreateOpen] = useState(false);
  const [createProvider, setCreateProvider] = useState<PaymentProvider>('paypal');
  const [createEnv, setCreateEnv] = useState<PaymentEnvironment>('sandbox');
  const [createForm, setCreateForm] = useState<SecretForm>(EMPTY_FORM);

  const [editTarget, setEditTarget] = useState<ChannelAccount | null>(null);
  const [editForm, setEditForm] = useState<SecretForm>(EMPTY_FORM);

  const [publishTarget, setPublishTarget] = useState<ChannelAccount | null>(null);
  const [publishPrimary, setPublishPrimary] = useState(true);
  const [publishOrders, setPublishOrders] = useState(true);
  const [publishSubs, setPublishSubs] = useState(true);

  const [deactivateTarget, setDeactivateTarget] = useState<ChannelAccount | null>(null);
  const [validationNote, setValidationNote] = useState<{ account: ChannelAccount; ok: boolean; summary?: ChannelValidationSummary; message?: string } | null>(null);

  const [submitting, setSubmitting] = useState(false);
  const [busyId, setBusyId] = useState<number | null>(null);
  const [dialogError, setDialogError] = useState('');
  const formId = useId();

  const loadChannels = useCallback(async (signal?: AbortSignal) => {
    setLoadError('');
    try {
      const res = await channelsApi.list(signal);
      if (!signal?.aborted) setChannels(res.channels ?? []);
    } catch (err) {
      if (!signal?.aborted) setLoadError(errMsg(err, '加载渠道列表失败'));
    }
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    void loadChannels(controller.signal);
    return () => controller.abort();
  }, [loadChannels]);

  const filteredChannels = useMemo(() => {
    if (!channels) return null;
    return channels.filter((c) => {
      if (providerFilter !== 'all' && c.provider !== providerFilter) return false;
      if (envFilter !== 'all' && c.environment !== envFilter) return false;
      return true;
    });
  }, [channels, providerFilter, envFilter]);

  const webhookOrigin = typeof window !== 'undefined' ? window.location.origin : '';

  const copyText = async (text: string, label: string) => {
    try {
      await navigator.clipboard.writeText(text);
      showToast(`已复制${label}`, 'success');
    } catch {
      showToast('复制失败，请手动选中复制', 'error');
    }
  };

  const openCreate = () => {
    setCreateForm(EMPTY_FORM);
    setCreateProvider('paypal');
    setCreateEnv('sandbox');
    setDialogError('');
    setCreateOpen(true);
  };

  const submitCreate = async (e: React.FormEvent) => {
    e.preventDefault();
    if (submitting) return;
    setDialogError('');
    setSubmitting(true);
    try {
      const payload: CreateChannelPayload = {
        provider: createProvider,
        environment: createEnv,
        display_name: createForm.display_name.trim(),
        account_key: createForm.account_key.trim(),
      };
      if (createProvider === 'paypal') {
        payload.merchant_id = createForm.merchant_id.trim();
        payload.client_secret = createForm.client_secret.trim();
        payload.webhook_id = createForm.webhook_id.trim();
      } else {
        payload.api_key = createForm.api_key.trim();
        payload.webhook_secret = createForm.webhook_secret.trim();
      }
      await channelsApi.create(payload);
      showToast('渠道账户草稿已创建，请执行连通性验证后发布', 'success');
      setCreateOpen(false);
      await loadChannels();
    } catch (err) {
      setDialogError(errMsg(err, '创建渠道账户失败'));
    } finally {
      setSubmitting(false);
    }
  };

  const openEdit = (ch: ChannelAccount) => {
    setEditTarget(ch);
    setEditForm({
      ...EMPTY_FORM,
      display_name: ch.display_name || ch.account_label,
      merchant_id: ch.merchant_id || '',
      account_key: ch.account_key,
    });
    setDialogError('');
  };

  const submitEdit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!editTarget || submitting) return;
    setDialogError('');
    setSubmitting(true);
    try {
      const graceNum = Number(editForm.rotate_webhook_grace_hours);
      await channelsApi.edit(editTarget.id, {
        display_name: editForm.display_name.trim(),
        merchant_id: editTarget.provider === 'paypal' ? editForm.merchant_id.trim() : undefined,
        client_secret: editForm.client_secret.trim() || undefined,
        webhook_id: editForm.webhook_id.trim() || undefined,
        api_key: editForm.api_key.trim() || undefined,
        webhook_secret: editForm.webhook_secret.trim() || undefined,
        rotate_webhook_grace_hours: Number.isFinite(graceNum) ? graceNum : 24,
      });
      showToast('配置/密钥已写入待验证版本，请先验证后再发布生效', 'success');
      setEditTarget(null);
      await loadChannels();
    } catch (err) {
      setDialogError(errMsg(err, '更新渠道配置失败'));
    } finally {
      setSubmitting(false);
    }
  };

  const handleValidate = async (ch: ChannelAccount) => {
    if (busyId !== null) return;
    setBusyId(ch.id);
    setValidationNote(null);
    try {
      const res = await channelsApi.validate(ch.id);
      setValidationNote({
        account: ch,
        ok: res.verified,
        summary: res.summary,
        message: res.verified ? '连通性与商户身份校验通过，可发布生效' : res.error || '验证未通过',
      });
      await loadChannels();
    } catch (err) {
      setValidationNote({
        account: ch,
        ok: false,
        message: errMsg(err, '校验请求失败'),
      });
      await loadChannels();
    } finally {
      setBusyId(null);
    }
  };

  const openPublish = (ch: ChannelAccount) => {
    setPublishTarget(ch);
    setPublishPrimary(true);
    setPublishOrders(ch.new_orders_enabled || ch.status !== 'published');
    setPublishSubs(ch.new_subscriptions_enabled || ch.status !== 'published');
    setDialogError('');
  };

  const submitPublish = async () => {
    if (!publishTarget || submitting) return;
    setSubmitting(true);
    setDialogError('');
    try {
      await channelsApi.publish(publishTarget.id, {
        set_primary: publishPrimary,
        new_orders_enabled: publishOrders,
        new_subscriptions_enabled: publishSubs,
        expected_version: publishTarget.pending_version,
      });
      showToast(`${publishTarget.display_name || publishTarget.account_label} v${publishTarget.pending_version} 已发布`, 'success');
      setPublishTarget(null);
      await loadChannels();
    } catch (err) {
      setDialogError(errMsg(err, '发布渠道失败'));
    } finally {
      setSubmitting(false);
    }
  };

  const submitDeactivate = async () => {
    if (!deactivateTarget || submitting) return;
    setSubmitting(true);
    setDialogError('');
    try {
      await channelsApi.deactivate(deactivateTarget.id);
      showToast('已停用该渠道账户的新单/新订阅入口', 'success');
      setDeactivateTarget(null);
      await loadChannels();
    } catch (err) {
      setDialogError(errMsg(err, '停用失败'));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="text-xl font-semibold text-[var(--cms-fg)]">渠道与商户</h1>
          <p className="text-xs text-[var(--cms-muted-fg)] mt-1">
            管理 PayPal 与 Stripe 商户账户的加密密钥、版本验证、主备切换与独立 Webhook 回调入口；修改经验证并发布后即时热加载至 Reader 支付域。
          </p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Link to="/products" className="btn-secondary text-sm">前往商品与定价</Link>
          <button type="button" className="btn-secondary inline-flex items-center gap-1.5" onClick={() => void loadChannels()}>
            <RefreshCw size={14} aria-hidden="true" />
            刷新
          </button>
          <button type="button" className="btn-primary inline-flex items-center gap-1.5" onClick={openCreate}>
            <Plus size={15} aria-hidden="true" />
            新增渠道账户
          </button>
        </div>
      </div>

      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex flex-wrap items-center gap-1.5" role="group" aria-label="环境筛选">
          {([
            { key: 'all', label: '全部环境' },
            { key: 'sandbox', label: 'Sandbox 沙箱' },
            { key: 'live', label: 'Live 正式' },
          ] as const).map((item) => (
            <button
              key={item.key}
              type="button"
              aria-pressed={envFilter === item.key}
              onClick={() => setEnvFilter(item.key)}
              className={`focus-ring px-3 py-1.5 rounded-xl text-xs font-medium transition-colors cursor-pointer ${
                envFilter === item.key
                  ? 'bg-[var(--cms-primary)] text-[var(--cms-primary-fg)]'
                  : 'bg-[var(--cms-secondary)] text-[var(--cms-muted-fg)] hover:text-[var(--cms-fg)]'
              }`}
            >
              {item.label}
            </button>
          ))}
        </div>
        <div className="flex flex-wrap items-center gap-1.5" role="group" aria-label="渠道筛选">
          {(['all', 'paypal', 'stripe'] as const).map((prov) => (
            <button
              key={prov}
              type="button"
              aria-pressed={providerFilter === prov}
              onClick={() => setProviderFilter(prov)}
              className={`focus-ring px-2.5 py-1.5 rounded-xl text-xs font-medium transition-colors cursor-pointer ${
                providerFilter === prov
                  ? 'bg-[var(--cms-primary-soft)] text-[var(--cms-primary)]'
                  : 'text-[var(--cms-muted-fg)] hover:bg-[var(--cms-surface-hover)]'
              }`}
            >
              {prov === 'all' ? '全部渠道' : prov === 'paypal' ? 'PayPal' : 'Stripe'}
            </button>
          ))}
        </div>
      </div>

      {loadError && (
        <div role="alert" className="p-3 rounded-xl border border-[var(--cms-error)] bg-[var(--cms-error-soft)] text-sm text-[var(--cms-error)]">
          {loadError}
        </div>
      )}

      {validationNote && (
        <div
          role={validationNote.ok ? 'status' : 'alert'}
          className={`p-3.5 rounded-xl border text-sm flex flex-wrap items-start justify-between gap-3 ${
            validationNote.ok
              ? 'border-[var(--cms-success)] bg-[var(--cms-success-soft)] text-[var(--cms-fg)]'
              : 'border-[var(--cms-error)] bg-[var(--cms-error-soft)] text-[var(--cms-error)]'
          }`}
        >
          <div className="space-y-1">
            <div className="font-medium">
              {validationNote.account.display_name || validationNote.account.account_label} · {validationNote.message}
            </div>
            {validationNote.summary && (
              <div className="text-xs text-[var(--cms-muted-fg)] flex flex-wrap gap-3">
                <span>接口鉴权: {validationNote.summary.provider_ok ? '通过' : '失败'}</span>
                <span>账户身份: {validationNote.summary.identity_ok ? '匹配' : '不匹配'}</span>
                <span>Webhook 凭证: {validationNote.summary.webhook_ok ? '就绪' : '未完善'}</span>
                {validationNote.summary.account_id_match && (
                  <span className="font-mono-code">标识: {validationNote.summary.account_id_match}</span>
                )}
              </div>
            )}
          </div>
          <button type="button" className="btn-ghost text-xs" onClick={() => setValidationNote(null)}>关闭</button>
        </div>
      )}

      <TableRegion ariaLabel="支付渠道账户列表">
        <table className="cms-table">
          <thead>
            <tr>
              <th scope="col">渠道 / 环境</th>
              <th scope="col">显示名称与账号</th>
              <th scope="col">状态与版本</th>
              <th scope="col">加密凭据摘要</th>
              <th scope="col">独立 Webhook 入口</th>
              <th scope="col">业务开关</th>
              <th scope="col" className="text-right">操作</th>
            </tr>
          </thead>
          <tbody>
            {!filteredChannels && !loadError ? (
              <SkeletonRows rows={3} cols={7} />
            ) : filteredChannels?.length === 0 ? (
              <tr>
                <td colSpan={7} className="text-center py-8 text-sm text-[var(--cms-muted-fg)]">
                  暂无符合筛选条件的渠道账户
                </td>
              </tr>
            ) : (
              filteredChannels?.map((ch) => {
                const statusMeta = CHANNEL_STATUS_META[ch.status] ?? { label: ch.status, tone: 'muted' as const };
                const pendingDiff = ch.pending_version > ch.active_version;
                const fullWebhookUrl = ch.webhook_path ? `${webhookOrigin}${ch.webhook_path}` : '';
                return (
                  <tr key={ch.id}>
                    <td>
                      <div className="flex items-center gap-1.5">
                        <span className={`badge ${ch.provider === 'paypal' ? 'badge-blue' : 'badge-purple'}`}>
                          {ch.provider === 'paypal' ? 'PayPal' : 'Stripe'}
                        </span>
                        <span className={`badge ${ch.environment === 'live' ? 'badge-red' : 'badge-gray'}`}>
                          {ch.environment === 'live' ? 'Live' : 'Sandbox'}
                        </span>
                      </div>
                    </td>
                    <td>
                      <div className="flex items-center gap-1.5 font-medium text-[var(--cms-fg)]">
                        <span>{ch.display_name || ch.account_label}</span>
                        {ch.is_primary && <span className="badge badge-green">主用</span>}
                      </div>
                      <div className="text-xs text-[var(--cms-muted-fg)] font-mono-code mt-0.5">
                        {ch.account_label}
                        {ch.merchant_id ? ` · 商户 ${ch.merchant_id}` : ''}
                      </div>
                    </td>
                    <td>
                      <StatusDot tone={statusMeta.tone} label={statusMeta.label} />
                      <div className="text-xs text-[var(--cms-muted-fg)] mt-1 tabular-nums">
                        生效 v{ch.active_version}
                        {pendingDiff && (
                          <span className="ml-1.5 text-[var(--cms-warning)]">· 待发布 v{ch.pending_version}</span>
                        )}
                      </div>
                      <div className="text-xs text-[var(--cms-muted-fg)] mt-0.5">
                        校验: {ch.validation_status === 'verified' ? '通过' : ch.validation_status === 'failed' ? '失败' : '待验证'}
                      </div>
                    </td>
                    <td>
                      <div className="text-xs text-[var(--cms-muted-fg)] space-y-0.5">
                        <div>来源: <span className="font-mono-code">{ch.credentials_ref}</span></div>
                        {Object.entries(ch.credential_hints || {}).map(([k, v]) => (
                          <div key={k} className="font-mono-code text-[11px]">{k}: {v}</div>
                        ))}
                      </div>
                    </td>
                    <td>
                      {fullWebhookUrl ? (
                        <div className="flex items-center gap-1.5 max-w-[220px]">
                          <code className="text-[11px] font-mono-code truncate text-[var(--cms-muted-fg)]" title={fullWebhookUrl}>
                            {ch.webhook_path}
                          </code>
                          <button
                            type="button"
                            className="btn-ghost p-1"
                            aria-label="复制 Webhook 地址"
                            onClick={() => void copyText(fullWebhookUrl, 'Webhook 回调地址')}
                          >
                            <Copy size={13} aria-hidden="true" />
                          </button>
                        </div>
                      ) : (
                        <span className="text-xs text-[var(--cms-muted-fg)]">{ch.webhook_ref}</span>
                      )}
                      {ch.previous_webhook_expires_at && (
                        <div className="text-[11px] text-[var(--cms-warning)] mt-0.5">
                          旧 Webhook 宽限至 {new Date(ch.previous_webhook_expires_at).toLocaleString()}
                        </div>
                      )}
                    </td>
                    <td>
                      <div className="flex flex-col gap-1 text-xs">
                        <StatusDot
                          tone={ch.new_orders_enabled ? 'success' : 'muted'}
                          label={`单次: ${ch.new_orders_enabled ? '开放' : '关闭'}`}
                        />
                        <StatusDot
                          tone={ch.new_subscriptions_enabled ? 'success' : 'muted'}
                          label={`订阅: ${ch.new_subscriptions_enabled ? '开放' : '关闭'}`}
                        />
                      </div>
                    </td>
                    <td className="text-right">
                      <div className="inline-flex flex-wrap justify-end gap-1.5">
                        <button type="button" className="btn-detail inline-flex items-center gap-1" onClick={() => openEdit(ch)}>
                          <KeyRound size={12} aria-hidden="true" />
                          编辑/轮换
                        </button>
                        <button
                          type="button"
                          className="btn-detail inline-flex items-center gap-1"
                          disabled={busyId === ch.id}
                          onClick={() => void handleValidate(ch)}
                        >
                          <ShieldCheck size={12} aria-hidden="true" />
                          {busyId === ch.id ? '验证中…' : '验证'}
                        </button>
                        <button
                          type="button"
                          className="btn-primary text-xs px-2.5 py-1.5"
                          onClick={() => openPublish(ch)}
                        >
                          发布
                        </button>
                        {ch.status !== 'disabled' && (
                          <button
                            type="button"
                            className="btn-ghost text-xs text-[var(--cms-error)] inline-flex items-center gap-1"
                            onClick={() => { setDialogError(''); setDeactivateTarget(ch); }}
                          >
                            <ShieldOff size={12} aria-hidden="true" />
                            停用
                          </button>
                        )}
                      </div>
                    </td>
                  </tr>
                );
              })
            )}
          </tbody>
        </table>
      </TableRegion>

      {/* 新增渠道账户弹窗 */}
      <CmsDialog
        open={createOpen}
        onClose={() => { if (!submitting) setCreateOpen(false); }}
        title="新增支付渠道账户"
        subtitle="凭据采用 AES-256-GCM 加密落库，保存后请执行“验证”与“发布”"
        size="md"
        closedBy="closerequest"
        closeDisabled={submitting}
        footer={
          <>
            <button type="button" className="btn-secondary" disabled={submitting} onClick={() => setCreateOpen(false)}>取消</button>
            <button type="submit" form={`${formId}-create`} className="btn-primary" disabled={submitting}>
              {submitting ? '创建中…' : '创建草稿'}
            </button>
          </>
        }
      >
        <form id={`${formId}-create`} onSubmit={submitCreate} className="space-y-4">
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
            <div>
              <label htmlFor={`${formId}-prov`} className="block text-xs font-medium text-[var(--cms-muted-fg)] mb-1">支付渠道 *</label>
              <CustomSelect
                id={`${formId}-prov`}
                width="100%"
                value={createProvider}
                onChange={(val) => setCreateProvider(val as PaymentProvider)}
                options={[
                  { value: 'paypal', label: 'PayPal' },
                  { value: 'stripe', label: 'Stripe' },
                ]}
              />
            </div>
            <div>
              <label htmlFor={`${formId}-env`} className="block text-xs font-medium text-[var(--cms-muted-fg)] mb-1">运行环境 *</label>
              <CustomSelect
                id={`${formId}-env`}
                width="100%"
                value={createEnv}
                onChange={(val) => setCreateEnv(val as PaymentEnvironment)}
                options={[
                  { value: 'sandbox', label: 'Sandbox 沙箱' },
                  { value: 'live', label: 'Live 正式' },
                ]}
              />
            </div>
          </div>

          <div>
            <label htmlFor={`${formId}-name`} className="block text-xs font-medium text-[var(--cms-muted-fg)] mb-1">账户显示名称 *</label>
            <input
              id={`${formId}-name`}
              type="text"
              required
              data-dialog-initial-focus
              className="input-field w-full"
              placeholder={createProvider === 'paypal' ? '例如：PayPal 主商户（HK）' : '例如：Stripe US Direct'}
              value={createForm.display_name}
              onChange={(e) => setCreateForm((prev) => ({ ...prev, display_name: e.target.value }))}
            />
          </div>

          <div>
            <label htmlFor={`${formId}-key`} className="block text-xs font-medium text-[var(--cms-muted-fg)] mb-1">
              {createProvider === 'paypal' ? 'PayPal Client ID *' : 'Stripe Account ID (acct_...) *'}
            </label>
            <input
              id={`${formId}-key`}
              type="text"
              required
              autoComplete="off"
              className="input-field w-full font-mono-code"
              placeholder={createProvider === 'paypal' ? 'AWxxxx...' : 'acct_1234567890'}
              value={createForm.account_key}
              onChange={(e) => setCreateForm((prev) => ({ ...prev, account_key: e.target.value }))}
            />
          </div>

          {createProvider === 'paypal' ? (
            <>
              <div>
                <label htmlFor={`${formId}-mid`} className="block text-xs font-medium text-[var(--cms-muted-fg)] mb-1">PayPal Merchant ID（可选，用于核验身份）</label>
                <input
                  id={`${formId}-mid`}
                  type="text"
                  autoComplete="off"
                  className="input-field w-full font-mono-code"
                  value={createForm.merchant_id}
                  onChange={(e) => setCreateForm((prev) => ({ ...prev, merchant_id: e.target.value }))}
                />
              </div>
              <div>
                <label htmlFor={`${formId}-csec`} className="block text-xs font-medium text-[var(--cms-muted-fg)] mb-1">Client Secret *</label>
                <input
                  id={`${formId}-csec`}
                  type="password"
                  required
                  autoComplete="new-password"
                  className="input-field w-full font-mono-code"
                  value={createForm.client_secret}
                  onChange={(e) => setCreateForm((prev) => ({ ...prev, client_secret: e.target.value }))}
                />
              </div>
              <div>
                <label htmlFor={`${formId}-whid`} className="block text-xs font-medium text-[var(--cms-muted-fg)] mb-1">Webhook ID</label>
                <input
                  id={`${formId}-whid`}
                  type="text"
                  autoComplete="off"
                  className="input-field w-full font-mono-code"
                  value={createForm.webhook_id}
                  onChange={(e) => setCreateForm((prev) => ({ ...prev, webhook_id: e.target.value }))}
                />
              </div>
            </>
          ) : (
            <>
              <div>
                <label htmlFor={`${formId}-apikey`} className="block text-xs font-medium text-[var(--cms-muted-fg)] mb-1">Stripe Secret API Key *</label>
                <input
                  id={`${formId}-apikey`}
                  type="password"
                  required
                  autoComplete="new-password"
                  placeholder={createEnv === 'live' ? 'sk_live_... 或 rk_live_...' : 'sk_test_... 或 rk_test_...'}
                  className="input-field w-full font-mono-code"
                  value={createForm.api_key}
                  onChange={(e) => setCreateForm((prev) => ({ ...prev, api_key: e.target.value }))}
                />
              </div>
              <div>
                <label htmlFor={`${formId}-whsec`} className="block text-xs font-medium text-[var(--cms-muted-fg)] mb-1">Webhook Signing Secret (whsec_...)</label>
                <input
                  id={`${formId}-whsec`}
                  type="password"
                  autoComplete="new-password"
                  placeholder="whsec_..."
                  className="input-field w-full font-mono-code"
                  value={createForm.webhook_secret}
                  onChange={(e) => setCreateForm((prev) => ({ ...prev, webhook_secret: e.target.value }))}
                />
              </div>
            </>
          )}

          {dialogError && <p role="alert" className="text-xs text-[var(--cms-error)]">{dialogError}</p>}
        </form>
      </CmsDialog>

      {/* 编辑与轮换密钥 */}
      <CmsDialog
        open={editTarget !== null}
        onClose={() => { if (!submitting) setEditTarget(null); }}
        title="编辑渠道信息与轮换密钥"
        subtitle={editTarget ? `${editTarget.provider.toUpperCase()} · ${editTarget.account_label} · ${editTarget.environment}` : undefined}
        size="md"
        closedBy="closerequest"
        closeDisabled={submitting}
        footer={
          <>
            <button type="button" className="btn-secondary" disabled={submitting} onClick={() => setEditTarget(null)}>取消</button>
            <button type="submit" form={`${formId}-edit`} className="btn-primary" disabled={submitting}>
              {submitting ? '写入新版本…' : '保存为待发布版本'}
            </button>
          </>
        }
      >
        {editTarget && (
          <form id={`${formId}-edit`} onSubmit={submitEdit} className="space-y-4">
            <p className="text-xs text-[var(--cms-muted-fg)]">
              只写密钥输入框留空表示继承当前版本密钥；填入新密钥将生成待发布版本（v{editTarget.pending_version + (editTarget.pending_version === editTarget.active_version ? 1 : 0)}），现网流量在您点击「验证 → 发布」前不受影响。
            </p>
            <div>
              <label htmlFor={`${formId}-ename`} className="block text-xs font-medium text-[var(--cms-muted-fg)] mb-1">显示名称</label>
              <input
                id={`${formId}-ename`}
                type="text"
                data-dialog-initial-focus
                className="input-field w-full"
                value={editForm.display_name}
                onChange={(e) => setEditForm((prev) => ({ ...prev, display_name: e.target.value }))}
              />
            </div>
            {editTarget.provider === 'paypal' ? (
              <>
                <div>
                  <label htmlFor={`${formId}-emid`} className="block text-xs font-medium text-[var(--cms-muted-fg)] mb-1">Merchant ID</label>
                  <input
                    id={`${formId}-emid`}
                    type="text"
                    className="input-field w-full font-mono-code"
                    value={editForm.merchant_id}
                    onChange={(e) => setEditForm((prev) => ({ ...prev, merchant_id: e.target.value }))}
                  />
                </div>
                <div>
                  <label htmlFor={`${formId}-esecret`} className="block text-xs font-medium text-[var(--cms-muted-fg)] mb-1">
                    新 Client Secret {editTarget.credential_hints?.api_secret ? `(当前 ${editTarget.credential_hints.api_secret})` : ''}
                  </label>
                  <input
                    id={`${formId}-esecret`}
                    type="password"
                    autoComplete="new-password"
                    placeholder="留空不修改"
                    className="input-field w-full font-mono-code"
                    value={editForm.client_secret}
                    onChange={(e) => setEditForm((prev) => ({ ...prev, client_secret: e.target.value }))}
                  />
                </div>
                <div>
                  <label htmlFor={`${formId}-ewhid`} className="block text-xs font-medium text-[var(--cms-muted-fg)] mb-1">
                    新 Webhook ID {editTarget.credential_hints?.webhook_id ? `(当前 ${editTarget.credential_hints.webhook_id})` : ''}
                  </label>
                  <input
                    id={`${formId}-ewhid`}
                    type="text"
                    autoComplete="off"
                    placeholder="留空不修改"
                    className="input-field w-full font-mono-code"
                    value={editForm.webhook_id}
                    onChange={(e) => setEditForm((prev) => ({ ...prev, webhook_id: e.target.value }))}
                  />
                </div>
              </>
            ) : (
              <>
                <div>
                  <label htmlFor={`${formId}-eapikey`} className="block text-xs font-medium text-[var(--cms-muted-fg)] mb-1">
                    新 Stripe API Key {editTarget.credential_hints?.api_key ? `(当前 ${editTarget.credential_hints.api_key})` : ''}
                  </label>
                  <input
                    id={`${formId}-eapikey`}
                    type="password"
                    autoComplete="new-password"
                    placeholder="留空不修改"
                    className="input-field w-full font-mono-code"
                    value={editForm.api_key}
                    onChange={(e) => setEditForm((prev) => ({ ...prev, api_key: e.target.value }))}
                  />
                </div>
                <div>
                  <label htmlFor={`${formId}-ewhsec`} className="block text-xs font-medium text-[var(--cms-muted-fg)] mb-1">
                    新 Webhook Secret {editTarget.credential_hints?.webhook_secret ? `(当前 ${editTarget.credential_hints.webhook_secret})` : ''}
                  </label>
                  <input
                    id={`${formId}-ewhsec`}
                    type="password"
                    autoComplete="new-password"
                    placeholder="留空不修改"
                    className="input-field w-full font-mono-code"
                    value={editForm.webhook_secret}
                    onChange={(e) => setEditForm((prev) => ({ ...prev, webhook_secret: e.target.value }))}
                  />
                </div>
              </>
            )}
            <div>
              <label htmlFor={`${formId}-grace`} className="block text-xs font-medium text-[var(--cms-muted-fg)] mb-1">轮换旧 Webhook 宽限期（小时，0–168）</label>
              <input
                id={`${formId}-grace`}
                type="number"
                min={0}
                max={168}
                className="input-field w-32 tabular-nums"
                value={editForm.rotate_webhook_grace_hours}
                onChange={(e) => setEditForm((prev) => ({ ...prev, rotate_webhook_grace_hours: e.target.value }))}
              />
            </div>
            {dialogError && <p role="alert" className="text-xs text-[var(--cms-error)]">{dialogError}</p>}
          </form>
        )}
      </CmsDialog>

      {/* 发布开关与主账号切换 */}
      <CmsDialog
        open={publishTarget !== null}
        onClose={() => { if (!submitting) setPublishTarget(null); }}
        title="发布支付渠道版本"
        subtitle={publishTarget ? `${publishTarget.display_name || publishTarget.account_label} · 发布版本 v${publishTarget.pending_version}` : undefined}
        size="sm"
        closedBy="closerequest"
        closeDisabled={submitting}
        footer={
          <>
            <button type="button" className="btn-secondary" disabled={submitting} onClick={() => setPublishTarget(null)}>取消</button>
            <button type="button" className="btn-primary" disabled={submitting} onClick={() => void submitPublish()}>
              {submitting ? '发布中…' : '确认发布'}
            </button>
          </>
        }
      >
        {publishTarget && (
          <div className="space-y-3 text-sm">
            {publishTarget.validation_status !== 'verified' && (
              <div role="alert" className="p-2.5 rounded-lg border border-[var(--cms-warning)] bg-[var(--cms-warning-soft)] text-xs text-[var(--cms-warning)]">
                当前待发布版本 v{publishTarget.pending_version} 尚未通过连通性验证，请先点击列表中的「验证」再发布。
              </div>
            )}
            <label className="flex items-center gap-2.5 cursor-pointer py-1">
              <input type="checkbox" checked={publishPrimary} onChange={(e) => setPublishPrimary(e.target.checked)} />
              <span>设为 {publishTarget.provider.toUpperCase()} ({publishTarget.environment}) 主账户</span>
            </label>
            <label className="flex items-center gap-2.5 cursor-pointer py-1">
              <input type="checkbox" checked={publishOrders} onChange={(e) => setPublishOrders(e.target.checked)} />
              <span>开放新单次购买（金币充值 / 整本买断）</span>
            </label>
            <label className="flex items-center gap-2.5 cursor-pointer py-1">
              <input type="checkbox" checked={publishSubs} onChange={(e) => setPublishSubs(e.target.checked)} />
              <span>开放新 VIP 订阅创建</span>
            </label>
            {dialogError && <p role="alert" className="text-xs text-[var(--cms-error)]">{dialogError}</p>}
          </div>
        )}
      </CmsDialog>

      {/* 停用确认 */}
      <CmsDialog
        open={deactivateTarget !== null}
        onClose={() => { if (!submitting) setDeactivateTarget(null); }}
        title="停用渠道账户"
        subtitle={deactivateTarget ? `${deactivateTarget.display_name || deactivateTarget.account_label} · ${deactivateTarget.environment}` : undefined}
        size="sm"
        closedBy="closerequest"
        closeDisabled={submitting}
        footer={
          <>
            <button type="button" data-dialog-initial-focus className="btn-secondary" disabled={submitting} onClick={() => setDeactivateTarget(null)}>取消</button>
            <button type="button" className="btn-primary bg-[var(--cms-error)] border-[var(--cms-error)]" disabled={submitting} onClick={() => void submitDeactivate()}>
              {submitting ? '停用中…' : '确认停用'}
            </button>
          </>
        }
      >
        <div className="space-y-2 text-sm text-[var(--cms-muted-fg)]">
          <p>停用后，该账户的新单与新订阅入口将立即关闭，同时不再担任主账户；历史未完成订单的退款、Webhook 回调与对账验证仍会保留以保障售后链路。</p>
          {dialogError && <p role="alert" className="text-xs text-[var(--cms-error)]">{dialogError}</p>}
        </div>
      </CmsDialog>
    </div>
  );
}
