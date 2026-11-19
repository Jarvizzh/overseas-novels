import { type FormEvent, useCallback, useEffect, useId, useMemo, useRef, useState } from 'react';
import { Link } from 'react-router-dom';
import {
  AlertTriangle,
  ExternalLink,
  Eye,
  EyeOff,
  FileSearch,
  KeyRound,
  Plus,
  RefreshCw,
} from 'lucide-react';
import { ApiError, apiRequest } from '../../utils/api';
import {
  getErrorMessage,
  sourceLabel,
  timestamp,
  utf8ByteLength,
} from '../../utils/conversionStatus';
import CustomSelect from '../CustomSelect';
import { CmsDialog } from '../ui/CmsDialog';
import {
  type ColumnOption,
  type FilterChip,
  FilterChipsBar,
  RowActionMenu,
  SearchInput,
  SkeletonRows,
  StatusDot,
  type TableDensity,
  TableRegion,
  ViewOptionsPopover,
} from '../ui/Primitives';
import { ConfigurationAuditSection } from './ConfigurationAuditSection';
import { ConversionStatusDot, CopyableText, InlineErrorBanner } from './ConversionShared';
import type { ConversionDestination, ConversionIntegration } from './types';

type PixelStatusFilter = '' | 'active' | 'disabled' | 'missing_credential' | 'retired';
type PixelOptionalCol = 'destination_id' | 'created_at';
type PixelDrawerTab = 'config' | 'audit';

interface BoundIntegrationBadge {
  integrationId: string;
  source: string;
  accountKey: string;
  enabled: boolean;
  additional: boolean;
}

const PIXEL_STATUS_OPTIONS = [
  { value: '', label: '全部状态' },
  { value: 'active', label: '运行正常' },
  { value: 'missing_credential', label: '缺少凭据' },
  { value: 'disabled', label: '已停用' },
  { value: 'retired', label: '已退役' },
];

function classifyDestinationState(dest: ConversionDestination): Exclude<PixelStatusFilter, ''> {
  if (dest.retired_at) return 'retired';
  if (!dest.enabled) return 'disabled';
  if (!dest.credential_configured) return 'missing_credential';
  return 'active';
}

function validateReason(value: string): string {
  const trimmed = value.trim();
  if (!trimmed) return '请填写操作原因（必填，将写入永久审计日志）';
  if (trimmed.length > 500) return '操作原因不能超过 500 字';
  return '';
}

function validatePixelId(value: string): string {
  const trimmed = value.trim();
  if (!trimmed) return '请输入 Meta Pixel ID';
  if (!/^\d{1,50}$/.test(trimmed)) return 'Meta Pixel ID 须为 1-50 位纯数字';
  return '';
}

function validateDisplayName(value: string): string {
  const trimmed = value.trim();
  if (!trimmed) return '请输入 Pixel 显示名称';
  if (Array.from(trimmed).length > 255) return '显示名称不能超过 255 字';
  if (trimmed.includes('\0')) return '显示名称包含非法空字符';
  return '';
}

function validateAccessToken(value: string, required = false): string {
  if (!value) {
    return required ? '新增 Pixel 时必须填写 Meta Access Token' : '';
  }
  if (value.includes('\r') || value.includes('\n') || value.includes('\0')) {
    return 'Access Token 不能包含换行符或空字符';
  }
  if (utf8ByteLength(value) > 4096) return 'Access Token 长度不能超过 4096 字节';
  return '';
}

function PixelConfigDrawer({
  open,
  editingDest,
  initialTab,
  canWrite,
  boundIntegrations,
  onClose,
  onSuccess,
}: {
  open: boolean;
  editingDest: ConversionDestination | null;
  initialTab: PixelDrawerTab;
  canWrite: boolean;
  boundIntegrations: BoundIntegrationBadge[];
  onClose: () => void;
  onSuccess: (message: string) => Promise<void> | void;
}) {
  const formId = useId();
  const pixelErrId = useId();
  const nameErrId = useId();
  const tokenErrId = useId();
  const reasonErrId = useId();

  const [drawerTab, setDrawerTab] = useState<PixelDrawerTab>(initialTab);
  const [pixelId, setPixelId] = useState('');
  const [displayName, setDisplayName] = useState('');
  const [accessToken, setAccessToken] = useState('');
  const [showToken, setShowToken] = useState(false);
  const [reason, setReason] = useState('');

  const [pixelError, setPixelError] = useState('');
  const [nameError, setNameError] = useState('');
  const [tokenError, setTokenError] = useState('');
  const [reasonError, setReasonError] = useState('');
  const [submitError, setSubmitError] = useState('');
  const [submitting, setSubmitting] = useState(false);

  const inFlightRef = useRef(false);
  const pixelInputRef = useRef<HTMLInputElement>(null);
  const nameInputRef = useRef<HTMLInputElement>(null);
  const tokenInputRef = useRef<HTMLInputElement>(null);
  const reasonInputRef = useRef<HTMLTextAreaElement>(null);
  const tabRefs = useRef<Partial<Record<PixelDrawerTab, HTMLButtonElement | null>>>({});

  const isCreate = !editingDest;
  const isRetired = Boolean(editingDest?.retired_at);
  const isEditable = canWrite && !isRetired;
  const destMap = useMemo(
    () => (editingDest ? new Map([[editingDest.id, editingDest]]) : undefined),
    [editingDest],
  );

  useEffect(() => {
    if (!open) return;
    setDrawerTab(editingDest ? initialTab : 'config');
    setPixelId(editingDest?.pixel_id ?? '');
    setDisplayName(editingDest?.name ?? '');
    setAccessToken('');
    setShowToken(false);
    setReason('');
    setPixelError('');
    setNameError('');
    setTokenError('');
    setReasonError('');
    setSubmitError('');
  }, [open, editingDest, initialTab]);

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault();
    if (!isEditable || submitting || inFlightRef.current) return;

    const pErr = isCreate ? validatePixelId(pixelId) : '';
    const nErr = validateDisplayName(displayName);
    const tErr = validateAccessToken(accessToken, isCreate);
    const rErr = validateReason(reason);

    setPixelError(pErr);
    setNameError(nErr);
    setTokenError(tErr);
    setReasonError(rErr);
    setSubmitError('');

    if (pErr || nErr || tErr || rErr) {
      if (pErr) pixelInputRef.current?.focus();
      else if (nErr) nameInputRef.current?.focus();
      else if (tErr) tokenInputRef.current?.focus();
      else if (rErr) reasonInputRef.current?.focus();
      return;
    }

    inFlightRef.current = true;
    setSubmitting(true);
    try {
      if (isCreate) {
        await apiRequest('POST', '/conversion/destinations', {
          pixel_id: pixelId.trim(),
          display_name: displayName.trim(),
          access_token: accessToken,
          reason: reason.trim(),
        });
        await onSuccess(`已新增 Pixel ${pixelId.trim()} 并自动绑定至 StarNovel Reader 接入`);
      } else {
        await apiRequest('PATCH', `/conversion/destinations/${editingDest.id}`, {
          display_name: displayName.trim(),
          ...(accessToken ? { access_token: accessToken } : {}),
          reason: reason.trim(),
        });
        await onSuccess(
          accessToken
            ? `已更新 Pixel ${editingDest.pixel_id} 名称与 Meta Access Token`
            : `已更新 Pixel ${editingDest.pixel_id} 显示名称`,
        );
      }
    } catch (err) {
      if (err instanceof ApiError && err.status === 409) {
        setSubmitError(
          isCreate
            ? '该 Meta Pixel ID 已存在或数据状态冲突，请核对 Pixel ID 后重试'
            : 'Pixel 状态已发生变更，请关闭抽屉并刷新列表后重试',
        );
      } else {
        setSubmitError(getErrorMessage(err, '保存 Pixel 配置失败'));
      }
    } finally {
      inFlightRef.current = false;
      setSubmitting(false);
    }
  };

  const handleTabKeyDown = (e: React.KeyboardEvent, current: PixelDrawerTab) => {
    if (!editingDest) return;
    if (e.key === 'ArrowLeft' || e.key === 'ArrowRight' || e.key === 'Home' || e.key === 'End') {
      e.preventDefault();
      const next: PixelDrawerTab =
        e.key === 'Home'
          ? 'config'
          : e.key === 'End'
            ? 'audit'
            : current === 'config'
              ? 'audit'
              : 'config';
      setDrawerTab(next);
      tabRefs.current[next]?.focus();
    }
  };

  const statusKind = editingDest ? classifyDestinationState(editingDest) : 'active';

  return (
    <CmsDialog
      open={open}
      onClose={() => {
        if (!submitting) onClose();
      }}
      closedBy={submitting ? 'none' : 'any'}
      closeDisabled={submitting}
      variant="drawer"
      size="lg"
      title={
        isCreate
          ? '新增 Meta Pixel 目标'
          : editingDest.name || `Pixel ${editingDest.pixel_id}`
      }
      subtitle={
        isCreate
          ? '注册回传目标并自动授权给 StarNovel 自建 Reader 接入'
          : `Pixel ID ${editingDest.pixel_id} · 目标 #${editingDest.id}`
      }
      footer={
        drawerTab === 'config' && isEditable ? (
          <>
            <button type="button" onClick={onClose} disabled={submitting} className="btn-secondary">
              取消
            </button>
            <button type="submit" form={formId} disabled={submitting} className="btn-primary">
              {submitting ? '正在保存…' : isCreate ? '创建并保存' : '保存变更'}
            </button>
          </>
        ) : (
          <button type="button" onClick={onClose} disabled={submitting} className="btn-secondary">
            关闭
          </button>
        )
      }
    >
      <div className="space-y-4">
        {editingDest && (
          <div className="border-b border-[var(--cms-border)] flex items-center justify-between gap-3">
            <div role="tablist" aria-label="Pixel 详情分区" className="flex items-center gap-6">
              <button
                ref={(el) => {
                  tabRefs.current.config = el;
                }}
                type="button"
                role="tab"
                id={`${formId}-tab-config`}
                aria-selected={drawerTab === 'config'}
                onKeyDown={(e) => handleTabKeyDown(e, 'config')}
                onClick={() => setDrawerTab('config')}
                className={`focus-ring pb-2.5 text-xs font-medium whitespace-nowrap cursor-pointer transition-colors ${drawerTab === 'config'
                    ? 'text-[var(--cms-fg)]'
                    : 'text-[var(--cms-muted-fg)] hover:text-[var(--cms-fg)]'
                  }`}
              >
                配置与凭据
              </button>
              <button
                ref={(el) => {
                  tabRefs.current.audit = el;
                }}
                type="button"
                role="tab"
                id={`${formId}-tab-audit`}
                aria-selected={drawerTab === 'audit'}
                onKeyDown={(e) => handleTabKeyDown(e, 'audit')}
                onClick={() => setDrawerTab('audit')}
                className={`focus-ring pb-2.5 text-xs font-medium whitespace-nowrap cursor-pointer transition-colors ${drawerTab === 'audit'
                    ? 'text-[var(--cms-fg)]'
                    : 'text-[var(--cms-muted-fg)] hover:text-[var(--cms-fg)]'
                  }`}
              >
                变更审计记录
              </button>
            </div>

            <div className="pb-2 flex items-center gap-2 text-xs">
              {statusKind === 'active' ? (
                <StatusDot tone="success" label="运行正常" />
              ) : statusKind === 'missing_credential' ? (
                <StatusDot tone="warning" label="缺少凭据" />
              ) : (
                <ConversionStatusDot code={statusKind} />
              )}
            </div>
          </div>
        )}

        {drawerTab === 'config' && (
          <div
            role={editingDest ? 'tabpanel' : undefined}
            id={editingDest ? `${formId}-panel-config` : undefined}
            aria-labelledby={editingDest ? `${formId}-tab-config` : undefined}
            className="space-y-4 text-xs"
          >
            {editingDest && (
              <div className="cms-card p-3.5 space-y-2.5">
                <div className="grid grid-cols-1 sm:grid-cols-2 gap-2.5">
                  <div>
                    <div className="text-[11px] text-[var(--cms-muted-fg)]">Meta Pixel ID（创建后锁定）</div>
                    <div className="mt-0.5">
                      <CopyableText value={editingDest.pixel_id} label="Pixel ID" />
                    </div>
                  </div>
                  <div>
                    <div className="text-[11px] text-[var(--cms-muted-fg)]">推广链接依赖与创建时间</div>
                    <div className="mt-0.5 text-[var(--cms-fg)] tabular-nums">
                      {editingDest.promotion_dependencies > 0
                        ? `${editingDest.promotion_dependencies} 个推广链接绑定中`
                        : '无推广链依赖'}{' '}
                      · {timestamp(editingDest.created_at)}
                    </div>
                  </div>
                </div>

                <div>
                  <div className="text-[11px] text-[var(--cms-muted-fg)] mb-1">已授权接入与附加路由</div>
                  {boundIntegrations.length === 0 ? (
                    <span className="text-[var(--cms-muted-fg)]">暂无接入方授权，请在「接入与授权绑定」标签页授权</span>
                  ) : (
                    <div className="flex flex-wrap gap-1.5">
                      {boundIntegrations.map((b) => (
                        <span
                          key={b.integrationId}
                          className={`badge ${b.additional ? 'badge-blue' : 'badge-gray'}`}
                        >
                          {sourceLabel(b.source)} · {b.accountKey}
                          {b.additional ? ' (自动附加)' : ' (仅授权)'}
                          {!b.enabled ? ' [接入停用]' : ''}
                        </span>
                      ))}
                    </div>
                  )}
                </div>
              </div>
            )}

            {isRetired && (
              <div
                className="p-3 rounded-xl border flex items-start gap-2.5"
                style={{
                  backgroundColor: 'color-mix(in srgb, var(--cms-warning) 10%, transparent)',
                  borderColor: 'color-mix(in srgb, var(--cms-warning) 30%, transparent)',
                }}
              >
                <AlertTriangle size={15} className="text-[var(--cms-warning)] shrink-0 mt-0.5" aria-hidden="true" />
                <div className="space-y-0.5 text-[var(--cms-fg)]">
                  <div className="font-medium">
                    该 Pixel 已于 {timestamp(editingDest?.retired_at)} 退役
                  </div>
                  <div className="text-[var(--cms-muted-fg)]">
                    退役操作不可恢复，系统不再向其发送新转化事件，仅保留历史日志与审计查询。
                  </div>
                </div>
              </div>
            )}

            {!isEditable && !isRetired && (
              <div className="p-3 rounded-xl bg-[var(--cms-content)] border border-[var(--cms-border)] text-[var(--cms-muted-fg)]">
                当前账号仅有只读查看权限，如需新增、轮换 Token 或退役 Pixel 请联系管理员操作。
              </div>
            )}

            {isEditable ? (
              <form id={formId} onSubmit={handleSubmit} noValidate className="space-y-4">
                {isCreate && (
                  <div className="space-y-1.5">
                    <label htmlFor={`${formId}-pixel-id`} className="block font-medium text-[var(--cms-fg)]">
                      Meta Pixel ID <span className="text-[var(--cms-error)]">*</span>
                    </label>
                    <input
                      ref={pixelInputRef}
                      id={`${formId}-pixel-id`}
                      type="text"
                      inputMode="numeric"
                      disabled={submitting}
                      value={pixelId}
                      placeholder="例如：149283710293847"
                      aria-invalid={pixelError ? 'true' : 'false'}
                      aria-describedby={pixelError ? pixelErrId : undefined}
                      onBlur={() => {
                        if (pixelId.trim().length > 0) {
                          setPixelError(validatePixelId(pixelId));
                        }
                      }}
                      onChange={(e) => {
                        const next = e.target.value;
                        setPixelId(next);
                        if (pixelError) setPixelError(validatePixelId(next));
                      }}
                      className="input-field font-mono-code"
                      style={pixelError ? { borderColor: 'var(--cms-error)' } : undefined}
                    />
                    {pixelError ? (
                      <p id={pixelErrId} className="m-0 text-[11px] text-[var(--cms-error)]">
                        {pixelError}
                      </p>
                    ) : (
                      <p className="m-0 text-[11px] text-[var(--cms-muted-fg)]">
                        创建后不可修改；保存时会自动为 StarNovel 自建 Reader 接入授权该 Pixel。
                      </p>
                    )}
                  </div>
                )}

                <div className="space-y-1.5">
                  <label htmlFor={`${formId}-display-name`} className="block font-medium text-[var(--cms-fg)]">
                    显示名称 <span className="text-[var(--cms-error)]">*</span>
                  </label>
                  <input
                    ref={nameInputRef}
                    id={`${formId}-display-name`}
                    type="text"
                    maxLength={255}
                    disabled={submitting}
                    value={displayName}
                    placeholder="例如：北美主投 - 英语小说账户 01"
                    aria-invalid={nameError ? 'true' : 'false'}
                    aria-describedby={nameError ? nameErrId : undefined}
                    onBlur={() => {
                      if (displayName.trim().length > 0) {
                        setNameError(validateDisplayName(displayName));
                      }
                    }}
                    onChange={(e) => {
                      const next = e.target.value;
                      setDisplayName(next);
                      if (nameError) setNameError(validateDisplayName(next));
                    }}
                    className="input-field"
                    style={nameError ? { borderColor: 'var(--cms-error)' } : undefined}
                  />
                  {nameError && (
                    <p id={nameErrId} className="m-0 text-[11px] text-[var(--cms-error)]">
                      {nameError}
                    </p>
                  )}
                </div>

                <div className="space-y-1.5">
                  <div className="flex items-center justify-between gap-2">
                    <label htmlFor={`${formId}-token`} className="font-medium text-[var(--cms-fg)] inline-flex items-center gap-1.5">
                      <KeyRound size={13} className="text-[var(--cms-muted-fg)]" aria-hidden="true" />
                      <span>
                        {isCreate
                          ? 'Conversions API Access Token'
                          : '轮换 / 更新 Access Token（留空保持不变）'}
                      </span>
                      {isCreate && <span className="text-[var(--cms-error)]">*</span>}
                    </label>
                    {editingDest && (
                      <span className={`badge ${editingDest.credential_configured ? 'badge-green' : 'badge-amber'}`}>
                        {editingDest.credential_configured ? '已托管加密凭据' : '当前未配置 Token'}
                      </span>
                    )}
                  </div>

                  <div className="relative">
                    <input
                      ref={tokenInputRef}
                      id={`${formId}-token`}
                      type={showToken ? 'text' : 'password'}
                      autoComplete="new-password"
                      disabled={submitting}
                      value={accessToken}
                      placeholder={
                        isCreate
                          ? '请粘贴 Meta Conversions API Access Token（必填）'
                          : '输入新 Token 以轮换替换；留空则保持原有密文不变'
                      }
                      aria-invalid={tokenError ? 'true' : 'false'}
                      aria-describedby={tokenError ? tokenErrId : undefined}
                      onBlur={() => {
                        if (accessToken.length > 0) {
                          setTokenError(validateAccessToken(accessToken, isCreate));
                        }
                      }}
                      onChange={(e) => {
                        const next = e.target.value;
                        setAccessToken(next);
                        if (tokenError) setTokenError(validateAccessToken(next, isCreate));
                      }}
                      className="input-field font-mono-code pr-10"
                      style={tokenError ? { borderColor: 'var(--cms-error)' } : undefined}
                    />
                    <button
                      type="button"
                      onClick={() => setShowToken((prev) => !prev)}
                      aria-label={showToken ? '隐藏 Access Token' : '显示 Access Token'}
                      className="focus-ring absolute right-2 top-1/2 -translate-y-1/2 w-7 h-7 rounded-md flex items-center justify-center text-[var(--cms-muted-fg)] hover:text-[var(--cms-fg)] cursor-pointer"
                    >
                      {showToken ? <EyeOff size={14} aria-hidden="true" /> : <Eye size={14} aria-hidden="true" />}
                    </button>
                  </div>
                  {tokenError ? (
                    <p id={tokenErrId} className="m-0 text-[11px] text-[var(--cms-error)]">
                      {tokenError}
                    </p>
                  ) : (
                    <p className="m-0 text-[11px] text-[var(--cms-muted-fg)]">
                      Token 将经信封加密托管于转换后端，保存后永不明文回显。
                    </p>
                  )}
                </div>

                <div className="space-y-1.5">
                  <label htmlFor={`${formId}-reason`} className="block font-medium text-[var(--cms-fg)]">
                    变更原因（必填，1-500 字） <span className="text-[var(--cms-error)]">*</span>
                  </label>
                  <textarea
                    ref={reasonInputRef}
                    id={`${formId}-reason`}
                    rows={3}
                    maxLength={500}
                    disabled={submitting}
                    value={reason}
                    placeholder={
                      isCreate
                        ? '例如：新增英语区主力投放 Pixel，配合推广链上线'
                        : '例如：轮换已到期的 Meta System User Token 并同步名称'
                    }
                    aria-invalid={reasonError ? 'true' : 'false'}
                    aria-describedby={reasonError ? reasonErrId : undefined}
                    onBlur={() => {
                      if (reason.length > 0) {
                        setReasonError(validateReason(reason));
                      }
                    }}
                    onChange={(e) => {
                      const next = e.target.value;
                      setReason(next);
                      if (reasonError) setReasonError(validateReason(next));
                    }}
                    className="input-field"
                    style={reasonError ? { borderColor: 'var(--cms-error)' } : undefined}
                  />
                  <div className="flex items-center justify-between text-[11px]">
                    {reasonError ? (
                      <span id={reasonErrId} className="text-[var(--cms-error)]">
                        {reasonError}
                      </span>
                    ) : (
                      <span className="text-[var(--cms-muted-fg)]">将写入永久操作审计日志</span>
                    )}
                    <span className="tabular-nums text-[var(--cms-muted-fg)]">{reason.trim().length}/500</span>
                  </div>
                </div>

                {submitError && <InlineErrorBanner message={submitError} />}
              </form>
            ) : (
              editingDest && (
                <div className="cms-card p-3.5 space-y-2">
                  <div>
                    <div className="text-[11px] text-[var(--cms-muted-fg)]">显示名称</div>
                    <div className="font-medium text-[var(--cms-fg)] mt-0.5">{editingDest.name || '—'}</div>
                  </div>
                  <div>
                    <div className="text-[11px] text-[var(--cms-muted-fg)]">Meta Access Token</div>
                    <div className="mt-0.5">
                      <span className={`badge ${editingDest.credential_configured ? 'badge-green' : 'badge-amber'}`}>
                        {editingDest.credential_configured ? '已配置密文凭据' : '未配置'}
                      </span>
                    </div>
                  </div>
                </div>
              )
            )}
          </div>
        )}

        {editingDest && drawerTab === 'audit' && (
          <div
            role="tabpanel"
            id={`${formId}-panel-audit`}
            aria-labelledby={`${formId}-tab-audit`}
          >
            <ConfigurationAuditSection
              subjectType="destination"
              subjectId={editingDest.id}
              destinationById={destMap}
            />
          </div>
        )}
      </div>
    </CmsDialog>
  );
}

function PixelRetireModal({
  target,
  boundCount,
  onClose,
  onSuccess,
}: {
  target: ConversionDestination | null;
  boundCount: number;
  onClose: () => void;
  onSuccess: (message: string) => Promise<void> | void;
}) {
  const formId = useId();
  const reasonErrId = useId();
  const reasonRef = useRef<HTMLTextAreaElement>(null);
  const inFlightRef = useRef(false);
  const [reason, setReason] = useState('');
  const [reasonError, setReasonError] = useState('');
  const [submitError, setSubmitError] = useState('');
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    setReason('');
    setReasonError('');
    setSubmitError('');
  }, [target]);

  const handleConfirm = async (e: FormEvent) => {
    e.preventDefault();
    if (!target || busy || inFlightRef.current) return;
    const rErr = validateReason(reason);
    setReasonError(rErr);
    setSubmitError('');
    if (rErr) {
      reasonRef.current?.focus();
      return;
    }

    inFlightRef.current = true;
    setBusy(true);
    try {
      await apiRequest('PATCH', `/conversion/destinations/${target.id}`, {
        retire: true,
        reason: reason.trim(),
      });
      await onSuccess(`已退役 Pixel ${target.pixel_id}（已记录不可逆审计）`);
    } catch (err) {
      if (err instanceof ApiError && err.status === 409) {
        setSubmitError('无法退役：该 Pixel 可能刚被推广链接绑定或已被退役，请刷新列表核对推广依赖');
      } else {
        setSubmitError(getErrorMessage(err, '退役 Pixel 失败'));
      }
    } finally {
      inFlightRef.current = false;
      setBusy(false);
    }
  };

  return (
    <CmsDialog
      open={Boolean(target)}
      onClose={() => {
        if (!busy) onClose();
      }}
      closedBy={busy ? 'none' : 'any'}
      closeDisabled={busy}
      variant="modal"
      size="md"
      title={target ? `确认退役 Pixel ${target.pixel_id}` : '确认退役 Pixel'}
      subtitle="高风险不可逆操作 · 需填写审计原因"
      footer={
        <>
          <button type="button" className="btn-secondary" disabled={busy} onClick={onClose}>
            取消
          </button>
          <button
            type="submit"
            form={formId}
            disabled={busy}
            className="btn-primary"
            style={{ backgroundColor: 'var(--cms-error)', borderColor: 'var(--cms-error)' }}
          >
            {busy ? '正在退役…' : '确认永久退役'}
          </button>
        </>
      }
    >
      {target && (
        <form id={formId} onSubmit={handleConfirm} noValidate className="space-y-4 text-xs">
          <div
            className="p-3 rounded-xl border flex items-start gap-2.5"
            style={{
              backgroundColor: 'color-mix(in srgb, var(--cms-error) 10%, transparent)',
              borderColor: 'color-mix(in srgb, var(--cms-error) 30%, transparent)',
            }}
          >
            <AlertTriangle size={16} className="text-[var(--cms-error)] shrink-0 mt-0.5" aria-hidden="true" />
            <div className="space-y-1 text-[var(--cms-fg)] leading-relaxed">
              <div className="font-semibold">
                退役「{target.name || target.pixel_id}」（Pixel ID：{target.pixel_id}）后无法重新启用
              </div>
              <ul className="m-0 pl-4 space-y-0.5 text-[var(--cms-muted-fg)]">
                <li>系统将立即停止向该 Pixel 投递新事件；已有 {boundCount} 处接入授权将不再产生有效投递。</li>
                <li>历史转化事件、投递尝试及配置审计记录会完整保留以备对账追溯。</li>
              </ul>
            </div>
          </div>

          <div className="space-y-1.5">
            <label htmlFor={`${formId}-retire-reason`} className="block font-medium text-[var(--cms-fg)]">
              退役原因（必填，1-500 字） <span className="text-[var(--cms-error)]">*</span>
            </label>
            <textarea
              ref={reasonRef}
              id={`${formId}-retire-reason`}
              rows={3}
              maxLength={500}
              disabled={busy}
              value={reason}
              placeholder="例如：广告账户封停，已完成推广链解绑，正式退役该 Pixel"
              aria-invalid={reasonError ? 'true' : 'false'}
              aria-describedby={reasonError ? reasonErrId : undefined}
              onBlur={() => {
                if (reason.length > 0) setReasonError(validateReason(reason));
              }}
              onChange={(e) => {
                const next = e.target.value;
                setReason(next);
                if (reasonError) setReasonError(validateReason(next));
              }}
              className="input-field"
              style={reasonError ? { borderColor: 'var(--cms-error)' } : undefined}
            />
            {reasonError && (
              <p id={reasonErrId} className="m-0 text-[11px] text-[var(--cms-error)]">
                {reasonError}
              </p>
            )}
          </div>

          {submitError && <InlineErrorBanner message={submitError} />}
        </form>
      )}
    </CmsDialog>
  );
}

export function ConversionPixelsPanel({
  destinations,
  integrations,
  loading,
  hasLoadedOnce,
  loadError = '',
  canWrite,
  onRefresh,
}: {
  destinations: ConversionDestination[];
  integrations: ConversionIntegration[];
  loading: boolean;
  hasLoadedOnce: boolean;
  loadError?: string;
  canWrite: boolean;
  onRefresh: () => Promise<void> | void;
}) {
  const [searchQuery, setSearchQuery] = useState('');
  const [statusFilter, setStatusFilter] = useState<PixelStatusFilter>('');
  const [density, setDensity] = useState<TableDensity>('comfortable');
  const [visibleCols, setVisibleCols] = useState<Record<PixelOptionalCol, boolean>>({
    destination_id: false,
    created_at: true,
  });

  const [drawerOpen, setDrawerOpen] = useState(false);
  const [editingDest, setEditingDest] = useState<ConversionDestination | null>(null);
  const [drawerInitialTab, setDrawerInitialTab] = useState<PixelDrawerTab>('config');
  const [retireTarget, setRetireTarget] = useState<ConversionDestination | null>(null);

  const integrationsByDestId = useMemo(() => {
    const map = new Map<string, BoundIntegrationBadge[]>();
    for (const integ of integrations) {
      for (const b of integ.bindings ?? []) {
        const list = map.get(b.destination_id) ?? [];
        list.push({
          integrationId: integ.id,
          source: integ.source,
          accountKey: integ.account_key,
          enabled: integ.enabled,
          additional: b.additional,
        });
        map.set(b.destination_id, list);
      }
    }
    return map;
  }, [integrations]);

  const filteredDestinations = useMemo(() => {
    const q = searchQuery.trim().toLowerCase();
    return destinations.filter((dest) => {
      if (q) {
        const matched =
          dest.pixel_id.toLowerCase().includes(q) ||
          dest.name.toLowerCase().includes(q) ||
          dest.id.toLowerCase().includes(q);
        if (!matched) return false;
      }
      if (statusFilter && classifyDestinationState(dest) !== statusFilter) {
        return false;
      }
      return true;
    });
  }, [destinations, searchQuery, statusFilter]);

  const filterChips = useMemo<FilterChip[]>(() => {
    const chips: FilterChip[] = [];
    if (searchQuery.trim()) {
      chips.push({
        key: 'search',
        label: `搜索: ${searchQuery.trim()}`,
        onClear: () => setSearchQuery(''),
      });
    }
    if (statusFilter) {
      const opt = PIXEL_STATUS_OPTIONS.find((item) => item.value === statusFilter);
      chips.push({
        key: 'status',
        label: `状态: ${opt?.label ?? statusFilter}`,
        onClear: () => setStatusFilter(''),
      });
    }
    return chips;
  }, [searchQuery, statusFilter]);

  const clearAllFilters = useCallback(() => {
    setSearchQuery('');
    setStatusFilter('');
  }, []);

  const columnOptions: ColumnOption[] = [
    { key: 'destination_id', label: '内部目标 ID', visible: visibleCols.destination_id },
    { key: 'created_at', label: '创建时间', visible: visibleCols.created_at },
  ];

  const colSpan =
    6 + (visibleCols.destination_id ? 1 : 0) + (visibleCols.created_at ? 1 : 0);

  const openCreateDrawer = () => {
    if (!canWrite) return;
    setEditingDest(null);
    setDrawerInitialTab('config');
    setDrawerOpen(true);
  };

  const openDestDrawer = (dest: ConversionDestination, tab: PixelDrawerTab = 'config') => {
    setEditingDest(dest);
    setDrawerInitialTab(tab);
    setDrawerOpen(true);
  };

  return (
    <div className="space-y-4 animate-fade-in">
      {/* Unified Borderless Filter & Action Strip */}
      <search className="block space-y-2">
        <div className="flex flex-wrap items-center justify-between gap-2.5">
          <div className="flex flex-wrap items-center gap-2 flex-1 min-w-[260px]">
            <SearchInput
              className="w-full sm:w-64"
              ariaLabel="搜索 Pixel ID 或名称"
              placeholder="搜索 Pixel ID / 名称..."
              value={searchQuery}
              onChange={setSearchQuery}
              onClear={() => setSearchQuery('')}
            />
            <CustomSelect
              aria-label="筛选 Pixel 状态"
              width="144px"
              options={PIXEL_STATUS_OPTIONS}
              value={statusFilter}
              onChange={(val) => setStatusFilter(val as PixelStatusFilter)}
            />
          </div>

          <div className="flex items-center gap-2 shrink-0">
            <ViewOptionsPopover
              density={density}
              onDensityChange={setDensity}
              columns={columnOptions}
              onToggleColumn={(key) =>
                setVisibleCols((prev) => ({
                  ...prev,
                  [key]: !prev[key as PixelOptionalCol],
                }))
              }
            />
            <button
              type="button"
              disabled={loading}
              onClick={() => void onRefresh()}
              className="btn-secondary h-9 px-3 text-xs font-normal"
            >
              <RefreshCw size={13} aria-hidden="true" />
              <span>刷新</span>
            </button>
            {canWrite && (
              <button
                type="button"
                onClick={openCreateDrawer}
                className="btn-primary h-9 px-3.5 text-xs"
              >
                <Plus size={14} aria-hidden="true" />
                <span>新增 Pixel</span>
              </button>
            )}
          </div>
        </div>

        <FilterChipsBar chips={filterChips} onResetAll={clearAllFilters} />
      </search>

      <TableRegion
        ariaLabel="Pixel 目标列表"
        footer={
          <div className="flex flex-wrap items-center justify-between gap-2 text-xs text-[var(--cms-muted-fg)] tabular-nums">
            <span>
              当前展示 <strong className="text-[var(--cms-fg)] font-medium">{filteredDestinations.length}</strong> /{' '}
              {destinations.length} 个 Pixel 目标
            </span>
            <span>点击任意行可查看配置细节与永久操作审计历史</span>
          </div>
        }
      >
        <table className="cms-table min-w-[920px]" data-density={density}>
          <thead>
            <tr>
              {visibleCols.destination_id && <th scope="col">内部 ID</th>}
              <th scope="col">名称与状态</th>
              <th scope="col">Meta Pixel ID</th>
              <th scope="col">Meta 凭据托管</th>
              <th scope="col">接入授权与自动路由</th>
              <th scope="col">推广依赖</th>
              {visibleCols.created_at && <th scope="col">创建时间</th>}
              <th scope="col" className="text-right w-px whitespace-nowrap">
                操作
              </th>
            </tr>
          </thead>
          <tbody className={loading && hasLoadedOnce ? 'opacity-65 transition-opacity' : ''}>
            {!hasLoadedOnce && loading ? (
              <SkeletonRows rows={5} cols={colSpan} />
            ) : filteredDestinations.length === 0 ? (
              <tr>
                <td colSpan={colSpan} className="py-16 text-center text-[var(--cms-muted-fg)]">
                  {!hasLoadedOnce && loadError
                    ? '加载 Pixel 列表失败，请点击上方「重试」或「刷新」重新加载'
                    : destinations.length === 0
                      ? '暂无已注册 Pixel 目标，请先新增 Pixel'
                      : '未找到符合当前筛选条件的 Pixel 目标'}
                </td>
              </tr>
            ) : (
              filteredDestinations.map((dest) => {
                const stateKind = classifyDestinationState(dest);
                const boundList = integrationsByDestId.get(dest.id) ?? [];
                const isRetired = Boolean(dest.retired_at);

                return (
                  <tr
                    key={dest.id}
                    onClick={() => openDestDrawer(dest, 'config')}
                    className={`cursor-pointer transition-colors ${isRetired ? 'opacity-70' : ''}`}
                  >
                    {visibleCols.destination_id && (
                      <td className="font-mono-code text-[var(--cms-muted-fg)] whitespace-nowrap">
                        #{dest.id}
                      </td>
                    )}

                    <th scope="row" className="text-left font-normal min-w-[175px]">
                      <div className="space-y-1">
                        <div className="font-medium text-[var(--cms-fg)]">{dest.name || '未命名 Pixel'}</div>
                        <div className="flex flex-wrap items-center gap-x-2 gap-y-0.5 text-[11px]">
                          {stateKind === 'active' ? (
                            <StatusDot tone="success" label="运行正常" />
                          ) : stateKind === 'missing_credential' ? (
                            <StatusDot tone="warning" label="缺少 Meta 凭据" />
                          ) : (
                            <ConversionStatusDot code={stateKind} />
                          )}
                          {dest.retired_at && (
                            <span className="text-[var(--cms-muted-fg)] tabular-nums whitespace-nowrap">
                              退役于 {timestamp(dest.retired_at)}
                            </span>
                          )}
                        </div>
                      </div>
                    </th>

                    <td onClick={(e) => e.stopPropagation()}>
                      <CopyableText value={dest.pixel_id} label="Pixel ID" />
                    </td>

                    <td>
                      <span
                        className={`badge ${dest.credential_configured ? 'badge-green' : 'badge-amber'}`}
                      >
                        {dest.credential_configured ? '已配置 Token（加密）' : '未配置 Token'}
                      </span>
                    </td>

                    <td>
                      {boundList.length === 0 ? (
                        <span className="text-[var(--cms-muted-fg)]">暂无接入绑定</span>
                      ) : (
                        <div className="flex flex-wrap gap-1">
                          {boundList.map((b) => (
                            <span
                              key={b.integrationId}
                              className={`badge ${b.additional ? 'badge-blue' : 'badge-gray'}`}
                              title={
                                b.additional
                                  ? `${sourceLabel(b.source)} · ${b.accountKey}：授权且作为默认附加目标自动双投`
                                  : `${sourceLabel(b.source)} · ${b.accountKey}：已授权路由`
                              }
                            >
                              {sourceLabel(b.source)} · {b.accountKey}
                              {b.additional ? ' (附加)' : ''}
                            </span>
                          ))}
                        </div>
                      )}
                    </td>

                    <td onClick={(e) => e.stopPropagation()}>
                      {dest.promotion_dependencies > 0 ? (
                        <Link
                          to="/promotions"
                          className="badge badge-blue inline-flex items-center gap-1 hover:opacity-85 no-underline"
                          title="该 Pixel 正被推广链接引用，解除全部推广绑定后方可退役"
                        >
                          <span className="tabular-nums">{dest.promotion_dependencies} 个推广绑定</span>
                          <ExternalLink size={11} aria-hidden="true" />
                        </Link>
                      ) : (
                        <span className="text-[var(--cms-muted-fg)]">无绑定</span>
                      )}
                    </td>

                    {visibleCols.created_at && (
                      <td className="tabular-nums text-[var(--cms-muted-fg)] whitespace-nowrap">
                        {timestamp(dest.created_at)}
                      </td>
                    )}

                    <td className="text-right whitespace-nowrap" onClick={(e) => e.stopPropagation()}>
                      <div className="inline-flex items-center justify-end gap-1 whitespace-nowrap">
                        <Link
                          to={`/conversions?pixel_id=${encodeURIComponent(dest.pixel_id)}`}
                          className="btn-ghost text-xs whitespace-nowrap no-underline inline-flex items-center gap-1"
                          title={`在回传中心查看 Pixel ${dest.pixel_id} 的回传事件`}
                        >
                          <FileSearch size={12} aria-hidden="true" />
                          <span>回传记录</span>
                        </Link>

                        <button
                          type="button"
                          onClick={() => openDestDrawer(dest, canWrite && !isRetired ? 'config' : 'audit')}
                          className="btn-detail whitespace-nowrap"
                        >
                          {canWrite && !isRetired ? '编辑' : '审计'}
                        </button>

                        {canWrite && !isRetired && (
                          <RowActionMenu
                            ariaLabel={`Pixel ${dest.pixel_id} 更多操作`}
                            items={[
                              {
                                label: '修改名称 / 轮换 Token',
                                onClick: () => openDestDrawer(dest, 'config'),
                              },
                              {
                                label: '查看变更审计记录',
                                onClick: () => openDestDrawer(dest, 'audit'),
                              },
                              {
                                label:
                                  dest.promotion_dependencies > 0
                                    ? `退役像素（已被 ${dest.promotion_dependencies} 个推广链占用，不可退役）`
                                    : '退役像素…',
                                tone: 'danger',
                                dividerBefore: true,
                                disabled: dest.promotion_dependencies > 0,
                                onClick: () => setRetireTarget(dest),
                              },
                            ]}
                          />
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

      <PixelConfigDrawer
        open={drawerOpen}
        editingDest={editingDest}
        initialTab={drawerInitialTab}
        canWrite={canWrite}
        boundIntegrations={editingDest ? integrationsByDestId.get(editingDest.id) ?? [] : []}
        onClose={() => setDrawerOpen(false)}
        onSuccess={async (msg) => {
          setDrawerOpen(false);
          window.showToast?.(msg, 'success');
          await onRefresh();
        }}
      />

      <PixelRetireModal
        target={retireTarget}
        boundCount={retireTarget ? (integrationsByDestId.get(retireTarget.id)?.length ?? 0) : 0}
        onClose={() => setRetireTarget(null)}
        onSuccess={async (msg) => {
          setRetireTarget(null);
          window.showToast?.(msg, 'success');
          await onRefresh();
        }}
      />
    </div>
  );
}
