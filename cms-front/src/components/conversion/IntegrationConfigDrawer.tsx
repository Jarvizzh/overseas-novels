import { type FormEvent, type KeyboardEvent, lazy, Suspense, useCallback, useEffect, useId, useMemo, useRef, useState } from 'react';
import { AlertTriangle, Check, Plus, Radio, Search, ShieldCheck, ShieldOff } from 'lucide-react';
import { ApiError, apiRequest } from '../../utils/api';
import {
  getErrorMessage,
  sourceEventLabel,
  sourceLabel,
  utf8ByteLength,
} from '../../utils/conversionStatus';
import CustomSelect from '../CustomSelect';
import { CmsDialog } from '../ui/CmsDialog';
import { ConfigurationAuditSection } from './ConfigurationAuditSection';
import { ConversionStatusDot, InlineErrorBanner } from './ConversionShared';
import { discardPaymentDraft } from './paymentPolicy';
import type { PaymentPolicyView } from './paymentPolicy';
import type { ConversionDestination, ConversionIntegration } from './types';

const PaymentPolicyPanel = lazy(() => import('./PaymentPolicyPanel').then(({ PaymentPolicyPanel }) => ({ default: PaymentPolicyPanel })));

export type IntegrationDrawerTab = 'config' | 'policy' | 'audit';
type RouteMode = 'none' | 'authorized' | 'additional';

const SOURCE_CREATE_OPTIONS = [
  { value: 'sinan', label: '番茄司南 (sinan)' },
  { value: 'starnovel', label: 'StarNovel 自建 (starnovel)' },
];

const EVENT_PRESETS: Record<string, Array<{ value: string; hint: string }>> = {
  sinan: [
    { value: 'attribution-succeeded', hint: '染色成功' },
    { value: 'order-created', hint: '订单创建' },
    { value: 'recharge-paid', hint: '充值支付完成' },
  ],
  starnovel: [
    { value: 'CompleteRegistration', hint: '注册完成' },
    { value: 'InitiateCheckout', hint: '发起结账' },
    { value: 'Purchase', hint: '支付购买' },
    { value: 'Subscribe', hint: '订阅会员' },
  ],
};

function parseTokenList(raw: string): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const part of raw.split(/[\n,，]+/)) {
    const trimmed = part.trim();
    if (trimmed && !seen.has(trimmed)) {
      seen.add(trimmed);
      out.push(trimmed);
    }
  }
  return out;
}

function validateReason(value: string): string {
  const trimmed = value.trim();
  if (!trimmed) return '请填写变更原因（必填，1-500 字）';
  if (trimmed.length > 500) return '变更原因不能超过 500 字';
  return '';
}

function validateListLimits(items: string[], fieldLabel: string): string {
  if (items.length > 100) return `${fieldLabel}最多允许 100 项`;
  for (const item of items) {
    if (item.includes('\r') || item.includes('\n') || item.includes('\0')) {
      return `${fieldLabel}不能包含换行符或空字符`;
    }
    if (utf8ByteLength(item) > 128) {
      return `${fieldLabel}单项长度不能超过 128 字节 (${item.slice(0, 16)}…)`;
    }
  }
  return '';
}

export function IntegrationConfigDrawer({
  open,
  editingIntegration,
  initialTab,
  destinations,
  canWrite,
  onClose,
  onSuccess,
  initialPolicyPromotion,
  onPolicyPublished,
}: {
  open: boolean;
  editingIntegration: ConversionIntegration | null;
  initialTab: IntegrationDrawerTab;
  destinations: ConversionDestination[];
  canWrite: boolean;
  onClose: () => void;
  onSuccess: (message: string) => Promise<void> | void;
  initialPolicyPromotion?: string;
  onPolicyPublished?: (view: PaymentPolicyView) => void;
}) {
  const formId = useId();
  const accountErrId = useId();
  const contractErrId = useId();
  const appsErrId = useId();
  const eventsErrId = useId();
  const reasonErrId = useId();

  const [drawerTab, setDrawerTab] = useState<IntegrationDrawerTab>(initialTab);
  const [policyFooter, setPolicyFooter] = useState<HTMLDivElement | null>(null);
  const [policyDirty, setPolicyDirty] = useState(false);
  const [policyBusy, setPolicyBusy] = useState(false);
  const [auditRefresh, setAuditRefresh] = useState(0);
  const onPolicyState = useCallback((dirty: boolean, busy: boolean) => {
    setPolicyDirty(dirty);
    setPolicyBusy(busy);
  }, []);

  const [source, setSource] = useState('sinan');
  const [accountKey, setAccountKey] = useState('');
  const [contractVersion, setContractVersion] = useState('sinan-v1');
  const [enabled, setEnabled] = useState(true);
  const [appsInput, setAppsInput] = useState('');
  const [selectedEvents, setSelectedEvents] = useState<string[]>([]);
  const [customEventsInput, setCustomEventsInput] = useState('');
  const [reason, setReason] = useState('');

  const [bindingModes, setBindingModes] = useState<Record<string, RouteMode>>({});
  const [initialBindingModes, setInitialBindingModes] = useState<Record<string, RouteMode>>({});
  const [pixelSearch, setPixelSearch] = useState('');
  const [showUnavailablePixels, setShowUnavailablePixels] = useState(false);

  const [accountError, setAccountError] = useState('');
  const [contractError, setContractError] = useState('');
  const [appsError, setAppsError] = useState('');
  const [eventsError, setEventsError] = useState('');
  const [reasonError, setReasonError] = useState('');
  const [submitError, setSubmitError] = useState('');
  const [submitting, setSubmitting] = useState(false);

  const inFlightRef = useRef(false);
  const accountRef = useRef<HTMLInputElement>(null);
  const contractRef = useRef<HTMLInputElement>(null);
  const appsRef = useRef<HTMLInputElement>(null);
  const customEventsRef = useRef<HTMLInputElement>(null);
  const reasonRef = useRef<HTMLTextAreaElement>(null);
  const tabRefs = useRef<Partial<Record<IntegrationDrawerTab, HTMLButtonElement | null>>>({});

  const isCreate = !editingIntegration;
  const destinationById = useMemo(
    () => new Map(destinations.map((d) => [d.id, d])),
    [destinations],
  );

  useEffect(() => {
    if (!open) return;

    setDrawerTab(editingIntegration ? initialTab : 'config');
    setPixelSearch('');
    setShowUnavailablePixels(false);
    setAccountError('');
    setContractError('');
    setAppsError('');
    setEventsError('');
    setReasonError('');
    setSubmitError('');
    setReason('');

    if (editingIntegration) {
      setSource(editingIntegration.source);
      setAccountKey(editingIntegration.account_key);
      setContractVersion(editingIntegration.contract_version);
      setEnabled(editingIntegration.enabled);
      setAppsInput((editingIntegration.allowed_apps ?? []).join(', '));

      const presets = new Set((EVENT_PRESETS[editingIntegration.source] ?? []).map((p) => p.value));
      const allEvents = editingIntegration.allowed_events ?? [];
      setSelectedEvents(allEvents.filter((ev) => presets.has(ev)));
      setCustomEventsInput(allEvents.filter((ev) => !presets.has(ev)).join(', '));

      const initialMap: Record<string, RouteMode> = {};
      for (const b of editingIntegration.bindings ?? []) {
        initialMap[b.destination_id] = b.additional ? 'additional' : 'authorized';
      }
      setInitialBindingModes(initialMap);
      setBindingModes({ ...initialMap });
    } else {
      setSource('sinan');
      setAccountKey('');
      setContractVersion('sinan-v1');
      setEnabled(true);
      setAppsInput('');
      setSelectedEvents(['recharge-paid', 'order-created']);
      setCustomEventsInput('');
      setInitialBindingModes({});
      setBindingModes({});
    }
  }, [open, editingIntegration, initialTab]);

  const activeSource = editingIntegration ? editingIntegration.source : source;
  const sourcePresets = useMemo(() => EVENT_PRESETS[activeSource] ?? [], [activeSource]);

  const handleSourceChange = (nextSource: string) => {
    setSource(nextSource);
    setAccountError('');
    setContractError('');
    setEventsError('');
    if (nextSource === 'starnovel') {
      setAccountKey('reader');
      setContractVersion('v1');
      setSelectedEvents(['CompleteRegistration', 'InitiateCheckout', 'Purchase', 'Subscribe']);
      setCustomEventsInput('');
    } else {
      setAccountKey((prev) => (prev === 'reader' ? '' : prev));
      setContractVersion('sinan-v1');
      setSelectedEvents(['recharge-paid', 'order-created']);
      setCustomEventsInput('');
    }
  };

  const togglePresetEvent = (eventVal: string) => {
    if (!canWrite) return;
    setEventsError('');
    setSelectedEvents((prev) =>
      prev.includes(eventVal) ? prev.filter((item) => item !== eventVal) : [...prev, eventVal],
    );
  };

  const mergedEvents = useMemo(() => {
    const custom = parseTokenList(customEventsInput);
    const seen = new Set<string>();
    const out: string[] = [];
    for (const item of [...selectedEvents, ...custom]) {
      if (item && !seen.has(item)) {
        seen.add(item);
        out.push(item);
      }
    }
    return out;
  }, [selectedEvents, customEventsInput]);

  const mergedApps = useMemo(() => parseTokenList(appsInput), [appsInput]);

  const diffBindings = useMemo(() => {
    const result: Array<{ destination_id: string; additional?: boolean; remove?: boolean }> = [];
    const seenIds = new Set<string>();
    for (const dest of destinations) {
      seenIds.add(dest.id);
      const prev = initialBindingModes[dest.id] ?? 'none';
      const next = bindingModes[dest.id] ?? 'none';
      if (prev === next) continue;
      if (next === 'none') {
        result.push({ destination_id: dest.id, remove: true });
      } else {
        result.push({ destination_id: dest.id, additional: next === 'additional' });
      }
    }
    // Also catch any historical binding ID whose destination might not appear in active list
    for (const id of Object.keys(initialBindingModes)) {
      if (seenIds.has(id)) continue;
      const prev = initialBindingModes[id];
      const next = bindingModes[id] ?? 'none';
      if (prev !== next && next === 'none') {
        result.push({ destination_id: id, remove: true });
      }
    }
    return result;
  }, [destinations, initialBindingModes, bindingModes]);

  const matrixStats = useMemo(() => {
    let authorized = 0;
    let additional = 0;
    for (const mode of Object.values(bindingModes)) {
      if (mode === 'authorized') authorized += 1;
      else if (mode === 'additional') additional += 1;
    }
    return {
      totalBound: authorized + additional,
      authorized,
      additional,
      changes: diffBindings.length,
    };
  }, [bindingModes, diffBindings.length]);

  const filteredDestinations = useMemo(() => {
    const q = pixelSearch.trim().toLowerCase();
    return destinations.filter((dest) => {
      const prevMode = initialBindingModes[dest.id] ?? 'none';
      const currMode = bindingModes[dest.id] ?? 'none';
      const isBound = prevMode !== 'none' || currMode !== 'none';
      const isUnavailable = Boolean(dest.retired_at) || !dest.enabled;
      if (isUnavailable && !isBound && !showUnavailablePixels) {
        return false;
      }
      if (q) {
        return (
          dest.pixel_id.toLowerCase().includes(q) ||
          dest.name.toLowerCase().includes(q) ||
          dest.id.toLowerCase().includes(q)
        );
      }
      return true;
    });
  }, [destinations, initialBindingModes, bindingModes, pixelSearch, showUnavailablePixels]);

  const unavailableHiddenCount = useMemo(() => {
    if (showUnavailablePixels) return 0;
    return destinations.filter((dest) => {
      const prevMode = initialBindingModes[dest.id] ?? 'none';
      const currMode = bindingModes[dest.id] ?? 'none';
      const isBound = prevMode !== 'none' || currMode !== 'none';
      return (Boolean(dest.retired_at) || !dest.enabled) && !isBound;
    }).length;
  }, [destinations, initialBindingModes, bindingModes, showUnavailablePixels]);

  const handleSetBindingMode = (dest: ConversionDestination, nextMode: RouteMode) => {
    if (!canWrite) return;
    const isUnavailable = Boolean(dest.retired_at) || !dest.enabled;
    const wasBound = (initialBindingModes[dest.id] ?? 'none') !== 'none';
    if (isUnavailable) {
      if (nextMode === 'none' && wasBound) {
        setBindingModes((prev) => ({ ...prev, [dest.id]: 'none' }));
      } else if (nextMode === (initialBindingModes[dest.id] ?? 'none')) {
        setBindingModes((prev) => ({ ...prev, [dest.id]: nextMode }));
      }
      return;
    }
    setBindingModes((prev) => ({ ...prev, [dest.id]: nextMode }));
  };

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault();
    if (!canWrite || submitting || inFlightRef.current) return;

    const trimmedAccount = activeSource === 'starnovel' ? 'reader' : accountKey.trim();
    const trimmedContract = contractVersion.trim();

    let aErr = '';
    if (isCreate && !trimmedAccount) {
      aErr = '请输入接入账户 Key（例如 partner-a）';
    } else if (isCreate && (trimmedAccount.includes('\r') || trimmedAccount.includes('\n') || trimmedAccount.includes('\0'))) {
      aErr = '账户 Key 不能包含换行符或空字符';
    } else if (isCreate && utf8ByteLength(trimmedAccount) > 100) {
      aErr = '账户 Key 长度不能超过 100 字节';
    }

    let cErr = '';
    if (!trimmedContract) {
      cErr = '请输入契约版本（例如 v1 / sinan-v1 / sinan-mock-v1）';
    } else if (trimmedContract.includes('\r') || trimmedContract.includes('\n') || trimmedContract.includes('\0')) {
      cErr = '契约版本不能包含换行符或空字符';
    } else if (utf8ByteLength(trimmedContract) > 100) {
      cErr = '契约版本长度不能超过 100 字节';
    }

    const appsErr = validateListLimits(mergedApps, '允许应用 ID');
    const evErr = validateListLimits(mergedEvents, '允许事件');
    const rErr = validateReason(reason);

    setAccountError(aErr);
    setContractError(cErr);
    setAppsError(appsErr);
    setEventsError(evErr);
    setReasonError(rErr);
    setSubmitError('');

    if (aErr || cErr || appsErr || evErr || rErr) {
      if (aErr) accountRef.current?.focus();
      else if (cErr) contractRef.current?.focus();
      else if (appsErr) appsRef.current?.focus();
      else if (evErr) customEventsRef.current?.focus();
      else if (rErr) reasonRef.current?.focus();
      return;
    }

    if (diffBindings.length > 100) {
      setSubmitError('单次提交的路由绑定变更数量不能超过 100 条');
      return;
    }

    inFlightRef.current = true;
    setSubmitting(true);
    try {
      if (isCreate) {
        await apiRequest('POST', '/conversion/integrations', {
          source: activeSource,
          account_key: trimmedAccount,
          contract_version: trimmedContract,
          enabled,
          allowed_apps: mergedApps,
          allowed_events: mergedEvents,
          bindings: diffBindings,
          reason: reason.trim(),
        });
        await onSuccess(`已新增接入方 ${sourceLabel(activeSource)} · ${trimmedAccount}`);
      } else {
        await apiRequest('PATCH', `/conversion/integrations/${editingIntegration.id}`, {
          contract_version: trimmedContract,
          enabled,
          allowed_apps: mergedApps,
          allowed_events: mergedEvents,
          ...(diffBindings.length > 0 ? { bindings: diffBindings } : {}),
          reason: reason.trim(),
        });
        await onSuccess(
          `已保存 ${sourceLabel(editingIntegration.source)} · ${editingIntegration.account_key} 配置与路由授权`,
        );
      }
    } catch (err) {
      if (err instanceof ApiError && err.status === 409) {
        setSubmitError(
          isCreate
            ? '保存冲突：该接入账户（来源 + 账户 Key）已存在，或所选绑定 Pixel 已停用/退役'
            : '保存冲突：部分待绑定 Pixel 已被停用或退役，请刷新后核对',
        );
      } else {
        setSubmitError(getErrorMessage(err, '保存接入与路由配置失败'));
      }
    } finally {
      inFlightRef.current = false;
      setSubmitting(false);
    }
  };

  const showPolicy = editingIntegration && ['starnovel', 'sinan'].includes(editingIntegration.source);
  const tabs: { id: IntegrationDrawerTab; label: string }[] = [
    ...(!initialPolicyPromotion ? [{ id: 'config' as const, label: '接入与路由矩阵' }] : []),
    ...(showPolicy ? [{ id: 'policy' as const, label: '回传规则' }] : []),
    { id: 'audit', label: '变更审计记录' },
  ];
  const isBusy = submitting || policyBusy;
  const requestClose = () => {
    if (isBusy) return;
    if (policyDirty) discardPaymentDraft(onClose);
    else onClose();
  };
  const moveTab = (next: IntegrationDrawerTab, focus = false) => {
    if (isBusy || next === drawerTab) return;
    const select = () => {
      setDrawerTab(next);
      if (focus) requestAnimationFrame(() => tabRefs.current[next]?.focus());
    };
    if (policyDirty) discardPaymentDraft(select);
    else select();
  };
  const handleTabKeyDown = (e: React.KeyboardEvent, current: IntegrationDrawerTab) => {
    if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(e.key)) return;
    e.preventDefault();
    const i = tabs.findIndex((tab) => tab.id === current);
    const next = e.key === 'Home' ? 0 : e.key === 'End' ? tabs.length - 1
      : (i + (e.key === 'ArrowRight' ? 1 : -1) + tabs.length) % tabs.length;
    moveTab(tabs[next].id, true);
  };

  return (
    <CmsDialog
      open={open}
      onClose={requestClose}
      closedBy={isBusy ? 'none' : policyDirty ? 'closerequest' : 'any'}
      closeDisabled={isBusy}
      interceptCancel={policyDirty}
      variant="drawer"
      size="xl"
      title={
        editingIntegration
          ? `${sourceLabel(editingIntegration.source)} · ${editingIntegration.account_key}`
          : '新建渠道接入与路由配置'
      }
      subtitle={
        editingIntegration
          ? `接入 #${editingIntegration.id} · 契约版本 ${editingIntegration.contract_version}`
          : '定义来源账户、事件白名单及可路由的 Meta Pixel 授权矩阵'
      }
      footer={
        drawerTab === 'policy' ? (
          <div ref={setPolicyFooter} className="flex flex-wrap justify-end items-center gap-2" />
        ) : drawerTab === 'config' && canWrite ? (
          <>
            <button type="button" onClick={requestClose} disabled={isBusy} className="btn-secondary">
              取消
            </button>
            <button type="submit" form={formId} disabled={submitting} className="btn-primary">
              {submitting
                ? '正在保存…'
                : isCreate
                  ? '创建接入方'
                  : diffBindings.length > 0
                    ? `保存配置与 ${diffBindings.length} 项路由变更`
                    : '保存配置'}
            </button>
          </>
        ) : (
          <button type="button" onClick={requestClose} disabled={isBusy} className="btn-secondary">
            关闭
          </button>
        )
      }
    >
      <div className="space-y-4">
        {editingIntegration && (
          <div className="border-b border-[var(--cms-border)] flex items-center justify-between gap-3">
            <div role="tablist" aria-label="接入详情分区" className="flex flex-wrap items-center gap-x-5 gap-y-1">
              {tabs.map(({ id, label }) => (
                <button key={id} type="button" role="tab" id={`${formId}-tab-${id}`}
                  ref={(el) => { tabRefs.current[id] = el; }}
                  aria-selected={drawerTab === id}
                  aria-controls={`${formId}-panel-${id}`}
                  tabIndex={drawerTab === id ? 0 : -1}
                  onKeyDown={(e) => handleTabKeyDown(e, id)}
                  onClick={() => moveTab(id)}
                  className={`focus-ring pb-2.5 text-xs font-medium whitespace-nowrap cursor-pointer transition-colors ${drawerTab === id
                    ? 'text-[var(--cms-fg)] border-b-2 border-[var(--cms-primary)]'
                    : 'text-[var(--cms-muted-fg)] hover:text-[var(--cms-fg)]'}`}>
                  {label}
                </button>
              ))}
            </div>

            <div className="pb-2 text-xs">
              <ConversionStatusDot code={editingIntegration.enabled ? 'enabled' : 'disabled'} />
            </div>
          </div>
        )}

        {drawerTab === 'config' && (
          <div
            role={editingIntegration ? 'tabpanel' : undefined}
            id={editingIntegration ? `${formId}-panel-config` : undefined}
            aria-labelledby={editingIntegration ? `${formId}-tab-config` : undefined}
          >
            {!canWrite && (
              <div className="mb-4 p-3 rounded-xl bg-[var(--cms-content)] border border-[var(--cms-border)] text-xs text-[var(--cms-muted-fg)]">
                当前账号仅有只读查看权限，可查阅事件白名单与 Pixel 路由授权状态，无法修改配置。
              </div>
            )}

            <form id={formId} onSubmit={handleSubmit} noValidate className="space-y-5 text-xs">
              {/* Section 1: Identity & Contract */}
              <div className="cms-card p-4 space-y-4">
                <div className="flex flex-wrap items-center justify-between gap-3">
                  <div>
                    <h3 className="text-sm font-semibold text-[var(--cms-fg)] m-0">基础身份与接入契约</h3>
                    <p className="text-[11px] text-[var(--cms-muted-fg)] m-0 mt-0.5">
                      停用接入方后，系统将不再接收或归一化该渠道新回调，但已入队事件仍可查看审计。
                    </p>
                  </div>

                  <div className="flex items-center gap-2.5">
                    <span className="text-[var(--cms-muted-fg)]">接入状态</span>
                    <button
                      type="button"
                      role="switch"
                      aria-checked={enabled}
                      disabled={!canWrite || submitting}
                      onClick={() => setEnabled((prev) => !prev)}
                      className={`focus-ring inline-flex items-center gap-2 h-8 px-3 rounded-full border text-xs font-medium transition-colors cursor-pointer disabled:opacity-60 disabled:cursor-not-allowed ${
                        enabled
                          ? 'bg-[color-mix(in_srgb,var(--cms-success)_12%,transparent)] border-[color-mix(in_srgb,var(--cms-success)_35%,transparent)] text-[var(--cms-success)]'
                          : 'bg-[var(--cms-content)] border-[var(--cms-border)] text-[var(--cms-muted-fg)]'
                      }`}
                    >
                      <span
                        aria-hidden="true"
                        className={`w-2 h-2 rounded-full ${
                          enabled ? 'bg-[var(--cms-success)]' : 'bg-[var(--cms-muted-fg)]'
                        }`}
                      />
                      <span>{enabled ? '已启用' : '已停用'}</span>
                    </button>
                  </div>
                </div>

                <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
                  <div className="space-y-1">
                    <label htmlFor={`${formId}-source`} className="block font-medium text-[var(--cms-fg)]">
                      接入来源
                    </label>
                    {isCreate ? (
                      <CustomSelect
                        id={`${formId}-source`}
                        width="100%"
                        disabled={!canWrite || submitting}
                        options={SOURCE_CREATE_OPTIONS}
                        value={source}
                        onChange={handleSourceChange}
                      />
                    ) : (
                      <input
                        id={`${formId}-source`}
                        type="text"
                        readOnly
                        disabled
                        value={`${sourceLabel(activeSource)} (${activeSource})`}
                        className="input-field opacity-80"
                      />
                    )}
                  </div>

                  <div className="space-y-1">
                    <label htmlFor={`${formId}-account-key`} className="block font-medium text-[var(--cms-fg)]">
                      账户 Key {isCreate && activeSource !== 'starnovel' && <span className="text-[var(--cms-error)]">*</span>}
                    </label>
                    <input
                      ref={accountRef}
                      id={`${formId}-account-key`}
                      type="text"
                      maxLength={100}
                      readOnly={!isCreate || activeSource === 'starnovel'}
                      disabled={!isCreate || activeSource === 'starnovel' || !canWrite || submitting}
                      value={activeSource === 'starnovel' ? 'reader' : accountKey}
                      placeholder="例如：partner-a"
                      aria-invalid={accountError ? 'true' : 'false'}
                      aria-describedby={accountError ? accountErrId : undefined}
                      onChange={(e) => {
                        setAccountKey(e.target.value);
                        if (accountError) setAccountError('');
                      }}
                      className="input-field font-mono-code disabled:opacity-80"
                      style={accountError ? { borderColor: 'var(--cms-error)' } : undefined}
                    />
                    {accountError ? (
                      <p id={accountErrId} className="m-0 text-[11px] text-[var(--cms-error)]">
                        {accountError}
                      </p>
                    ) : (
                      isCreate &&
                      activeSource === 'starnovel' && (
                        <p className="m-0 text-[11px] text-[var(--cms-muted-fg)]">
                          StarNovel 自建渠道固定绑定账户 key <code className="font-mono-code">reader</code>
                        </p>
                      )
                    )}
                  </div>

                  <div className="space-y-1">
                    <label htmlFor={`${formId}-contract`} className="block font-medium text-[var(--cms-fg)]">
                      契约版本 <span className="text-[var(--cms-error)]">*</span>
                    </label>
                    <input
                      ref={contractRef}
                      id={`${formId}-contract`}
                      type="text"
                      maxLength={100}
                      disabled={!canWrite || submitting}
                      value={contractVersion}
                      placeholder={activeSource === 'starnovel' ? 'v1' : 'sinan-v1 / sinan-mock-v1'}
                      aria-invalid={contractError ? 'true' : 'false'}
                      aria-describedby={contractError ? contractErrId : undefined}
                      onChange={(e) => {
                        setContractVersion(e.target.value);
                        if (contractError) setContractError('');
                      }}
                      className="input-field font-mono-code"
                      style={contractError ? { borderColor: 'var(--cms-error)' } : undefined}
                    />
                    {contractError && (
                      <p id={contractErrId} className="m-0 text-[11px] text-[var(--cms-error)]">
                        {contractError}
                      </p>
                    )}
                  </div>
                </div>
              </div>

              {/* Section 2: App & Event Whitelist */}
              <div className="cms-card p-4 space-y-4">
                <div>
                  <h3 className="text-sm font-semibold text-[var(--cms-fg)] m-0">应用与事件白名单</h3>
                  <p className="text-[11px] text-[var(--cms-muted-fg)] m-0 mt-0.5">
                    限制仅放行指定应用与事件类型；不在白名单内的回调将被归一化拒绝。
                  </p>
                </div>

                <div className="space-y-1.5">
                  <label htmlFor={`${formId}-allowed-apps`} className="block font-medium text-[var(--cms-fg)]">
                    允许应用 ID（逗号或换行分隔，留空代表不限应用）
                  </label>
                  <input
                    ref={appsRef}
                    id={`${formId}-allowed-apps`}
                    type="text"
                    disabled={!canWrite || submitting}
                    value={appsInput}
                    placeholder="例如：1001, 1002"
                    aria-invalid={appsError ? 'true' : 'false'}
                    aria-describedby={appsError ? appsErrId : undefined}
                    onChange={(e) => {
                      setAppsInput(e.target.value);
                      if (appsError) setAppsError('');
                    }}
                    className="input-field font-mono-code"
                    style={appsError ? { borderColor: 'var(--cms-error)' } : undefined}
                  />
                  {appsError ? (
                    <p id={appsErrId} className="m-0 text-[11px] text-[var(--cms-error)]">
                      {appsError}
                    </p>
                  ) : (
                    mergedApps.length > 0 && (
                      <div className="flex flex-wrap gap-1 pt-0.5">
                        {mergedApps.map((app) => (
                          <span key={app} className="badge badge-gray font-mono-code">
                            App {app}
                          </span>
                        ))}
                      </div>
                    )
                  )}
                </div>

                <div className="space-y-2">
                  <div className="font-medium text-[var(--cms-fg)]">允许接收事件</div>
                  {sourcePresets.length > 0 && (
                    <div className="flex flex-wrap gap-2">
                      {sourcePresets.map((preset) => {
                        const active = selectedEvents.includes(preset.value);
                        return (
                          <button
                            key={preset.value}
                            type="button"
                            disabled={!canWrite || submitting}
                            aria-pressed={active}
                            onClick={() => togglePresetEvent(preset.value)}
                            className={`focus-ring inline-flex items-center gap-1.5 h-8 px-3 rounded-lg border text-xs transition-colors cursor-pointer disabled:opacity-60 disabled:cursor-not-allowed ${
                              active
                                ? 'bg-[color-mix(in_srgb,var(--cms-primary)_14%,transparent)] border-[color-mix(in_srgb,var(--cms-primary)_40%,transparent)] text-[var(--cms-primary)] font-medium'
                                : 'bg-[var(--cms-content)] border-[var(--cms-border)] text-[var(--cms-muted-fg)] hover:text-[var(--cms-fg)]'
                            }`}
                          >
                            {active ? (
                              <Check size={12} aria-hidden="true" />
                            ) : (
                              <Plus size={12} aria-hidden="true" />
                            )}
                            <span>{preset.hint}</span>
                            <span className="font-mono-code opacity-80">({preset.value})</span>
                          </button>
                        );
                      })}
                    </div>
                  )}

                  <div className="space-y-1">
                    <label htmlFor={`${formId}-custom-events`} className="block text-[11px] text-[var(--cms-muted-fg)]">
                      其他自定义事件名（逗号或换行分隔）
                    </label>
                    <input
                      ref={customEventsRef}
                      id={`${formId}-custom-events`}
                      type="text"
                      disabled={!canWrite || submitting}
                      value={customEventsInput}
                      placeholder="如有预设之外的事件标识可在此补充"
                      aria-invalid={eventsError ? 'true' : 'false'}
                      aria-describedby={eventsError ? eventsErrId : undefined}
                      onChange={(e) => {
                        setCustomEventsInput(e.target.value);
                        if (eventsError) setEventsError('');
                      }}
                      className="input-field font-mono-code"
                      style={eventsError ? { borderColor: 'var(--cms-error)' } : undefined}
                    />
                    {eventsError ? (
                      <p id={eventsErrId} className="m-0 text-[11px] text-[var(--cms-error)]">
                        {eventsError}
                      </p>
                    ) : (
                      mergedEvents.length > 0 && (
                        <div className="text-[11px] text-[var(--cms-muted-fg)]">
                          当前共生效 {mergedEvents.length} 个事件：
                          {mergedEvents.map((ev) => sourceEventLabel(activeSource, ev)).join('、')}
                        </div>
                      )
                    )}
                  </div>
                </div>
              </div>

              {/* Section 3: Visual Pixel Routing Matrix */}
              <div className="cms-card p-4 space-y-3">
                <div className="flex flex-wrap items-start justify-between gap-2">
                  <div>
                    <h3 className="text-sm font-semibold text-[var(--cms-fg)] m-0">
                      Pixel 路由授权矩阵
                    </h3>
                    <p className="text-[11px] text-[var(--cms-muted-fg)] m-0 mt-0.5">
                      「已授权」允许推广链/原始参数/人工追加向该 Pixel 投递；「授权并自动附加」还会对每条达标转化自动双投。系统自动计算增量 Diff 提交。
                    </p>
                  </div>

                  <div className="flex flex-wrap items-center gap-1.5 tabular-nums text-[11px]">
                    <span className="badge badge-gray">已授权 {matrixStats.totalBound}</span>
                    <span className="badge badge-blue">含自动附加 {matrixStats.additional}</span>
                    {matrixStats.changes > 0 && (
                      <span className="badge badge-amber">待提交变更 {matrixStats.changes}</span>
                    )}
                  </div>
                </div>

                <div className="flex flex-wrap items-center justify-between gap-2">
                  <div className="relative flex-1 min-w-[200px]">
                    <Search
                      size={13}
                      aria-hidden="true"
                      className="absolute left-3 top-1/2 -translate-y-1/2 text-[var(--cms-muted-fg)] pointer-events-none"
                    />
                    <input
                      type="search"
                      aria-label="在路由矩阵中搜索 Pixel"
                      placeholder="过滤矩阵中的 Pixel ID 或名称..."
                      value={pixelSearch}
                      onChange={(e) => setPixelSearch(e.target.value)}
                      className="input-field pl-8 h-8 text-xs"
                    />
                  </div>

                  {unavailableHiddenCount > 0 && (
                    <button
                      type="button"
                      onClick={() => setShowUnavailablePixels(true)}
                      className="btn-ghost text-xs"
                    >
                      显示 {unavailableHiddenCount} 个已退役/停用 Pixel
                    </button>
                  )}
                </div>

                <div className="border border-[var(--cms-border)] rounded-xl divide-y divide-[var(--cms-border)] max-h-72 overflow-y-auto">
                  {filteredDestinations.length === 0 ? (
                    <div className="p-6 text-center text-[var(--cms-muted-fg)]">
                      {destinations.length === 0
                        ? '尚未创建任何 Pixel 目标，请先到「Pixel 目标管理」新增 Pixel'
                        : '当前筛选条件无匹配 Pixel'}
                    </div>
                  ) : (
                    filteredDestinations.map((dest) => {
                      const prevMode = initialBindingModes[dest.id] ?? 'none';
                      const currMode = bindingModes[dest.id] ?? 'none';
                      const isRetired = Boolean(dest.retired_at);
                      const isDisabled = !dest.enabled;
                      const isUnavailable = isRetired || isDisabled;
                      const changed = prevMode !== currMode;

                      return (
                        <div
                          key={dest.id}
                          className={`p-3 flex flex-col lg:flex-row lg:items-center justify-between gap-2.5 ${
                            changed ? 'bg-[color-mix(in_srgb,var(--cms-primary)_6%,transparent)]' : ''
                          }`}
                        >
                          <div className="min-w-0 space-y-0.5">
                            <div className="flex flex-wrap items-center gap-1.5">
                              <span className="font-medium text-[var(--cms-fg)] truncate">
                                {dest.name || '未命名 Pixel'}
                              </span>
                              <span className="font-mono-code text-[11px] text-[var(--cms-muted-fg)]">
                                {dest.pixel_id}
                              </span>
                              {!dest.credential_configured && !isRetired && (
                                <span className="badge badge-amber">缺 Token</span>
                              )}
                              {isRetired && <span className="badge badge-red">已退役</span>}
                              {!isRetired && isDisabled && <span className="badge badge-amber">已停用</span>}
                              {changed && <span className="badge badge-blue">本次修改</span>}
                            </div>

                            {isUnavailable && (
                              <div className="text-[11px] text-[var(--cms-warning)] flex items-center gap-1">
                                <AlertTriangle size={11} aria-hidden="true" />
                                <span>
                                  {prevMode !== 'none'
                                    ? '该 Pixel 已停用或退役，不可变更为启用路由，仅支持解除绑定'
                                    : '已停用或退役的 Pixel 无法新增授权'}
                                </span>
                              </div>
                            )}
                          </div>

                          <div
                            role="radiogroup"
                            aria-label={`Pixel ${dest.pixel_id} 路由策略`}
                            className="inline-flex items-center rounded-lg border border-[var(--cms-border)] bg-[var(--cms-content)] p-0.5 shrink-0 self-start lg:self-auto"
                          >
                            {(
                              [
                                {
                                  mode: 'none',
                                  label: isUnavailable && prevMode !== 'none' ? '解除绑定' : '未授权',
                                  icon: ShieldOff,
                                  allowed: !isUnavailable || prevMode !== 'none',
                                },
                                {
                                  mode: 'authorized',
                                  label: '仅授权',
                                  icon: ShieldCheck,
                                  allowed: !isUnavailable || prevMode === 'authorized',
                                },
                                {
                                  mode: 'additional',
                                  label: '自动附加',
                                  icon: Radio,
                                  allowed: !isUnavailable || prevMode === 'additional',
                                },
                              ] as const
                            ).map((option, idx, allOptions) => {
                              const selected = currMode === option.mode;
                              const Icon = option.icon;
                              const optionDisabled = !canWrite || submitting || !option.allowed;

                              const handleRadioKeyDown = (e: KeyboardEvent<HTMLButtonElement>) => {
                                if (
                                  e.key !== 'ArrowLeft' &&
                                  e.key !== 'ArrowRight' &&
                                  e.key !== 'ArrowUp' &&
                                  e.key !== 'ArrowDown'
                                ) {
                                  return;
                                }
                                e.preventDefault();
                                const step = e.key === 'ArrowLeft' || e.key === 'ArrowUp' ? -1 : 1;
                                for (let offset = 1; offset <= allOptions.length; offset += 1) {
                                  const candidate =
                                    allOptions[(idx + step * offset + allOptions.length) % allOptions.length];
                                  if (canWrite && !submitting && candidate.allowed) {
                                    handleSetBindingMode(dest, candidate.mode);
                                    const container = e.currentTarget.parentElement;
                                    const buttons = container?.querySelectorAll<HTMLButtonElement>('[role="radio"]');
                                    const targetIdx = allOptions.findIndex((o) => o.mode === candidate.mode);
                                    buttons?.[targetIdx]?.focus();
                                    break;
                                  }
                                }
                              };

                              return (
                                <button
                                  key={option.mode}
                                  type="button"
                                  role="radio"
                                  tabIndex={selected ? 0 : -1}
                                  aria-checked={selected}
                                  disabled={optionDisabled}
                                  onKeyDown={handleRadioKeyDown}
                                  onClick={() => handleSetBindingMode(dest, option.mode)}
                                  className={`focus-ring inline-flex items-center gap-1 h-7 px-2.5 rounded-md text-[11px] font-medium transition-colors cursor-pointer disabled:opacity-45 disabled:cursor-not-allowed ${
                                    selected
                                      ? option.mode === 'additional'
                                        ? 'bg-[var(--cms-primary)] text-white shadow-xs'
                                        : option.mode === 'authorized'
                                          ? 'bg-[var(--cms-card)] text-[var(--cms-fg)] shadow-xs border border-[var(--cms-border)]'
                                          : 'bg-[var(--cms-card)] text-[var(--cms-muted-fg)] shadow-xs'
                                      : 'text-[var(--cms-muted-fg)] hover:text-[var(--cms-fg)]'
                                  }`}
                                >
                                  <Icon size={11} aria-hidden="true" />
                                  <span>{option.label}</span>
                                </button>
                              );
                            })}
                          </div>
                        </div>
                      );
                    })
                  )}
                </div>
              </div>

              {canWrite && (
                <div className="space-y-1.5">
                  <label htmlFor={`${formId}-reason`} className="block font-medium text-[var(--cms-fg)]">
                    变更原因（必填，1-500 字） <span className="text-[var(--cms-error)]">*</span>
                  </label>
                  <textarea
                    ref={reasonRef}
                    id={`${formId}-reason`}
                    rows={2}
                    maxLength={500}
                    disabled={submitting}
                    value={reason}
                    placeholder={
                      isCreate
                        ? '例如：新增司南合作方接入，授权主投 Pixel 并开启自动附加双投'
                        : '例如：补充充值完成事件白名单并授权新上线的投放 Pixel'
                    }
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
                  <div className="flex items-center justify-between text-[11px]">
                    {reasonError ? (
                      <span id={reasonErrId} className="text-[var(--cms-error)]">
                        {reasonError}
                      </span>
                    ) : (
                      <span className="text-[var(--cms-muted-fg)]">将写入配置审计记录并作用于后续事件归一化</span>
                    )}
                    <span className="tabular-nums text-[var(--cms-muted-fg)]">{reason.trim().length}/500</span>
                  </div>
                </div>
              )}

              {submitError && <InlineErrorBanner message={submitError} />}
            </form>
          </div>
        )}

        {editingIntegration && drawerTab === 'policy' && (
          <div role="tabpanel" id={`${formId}-panel-policy`} aria-labelledby={`${formId}-tab-policy`}>
            <Suspense fallback={<div className="skeleton-bar h-32 rounded-xl" role="status" aria-label="正在加载回传规则" />}>
            <PaymentPolicyPanel
              key={`${editingIntegration.id}:${initialPolicyPromotion ?? ''}`}
              integration={editingIntegration}
              canWrite={canWrite}
              initialPromotion={initialPolicyPromotion}
              footer={policyFooter}
              onClose={requestClose}
              onState={onPolicyState}
              onPublished={(view) => {
                setAuditRefresh((count) => count + 1);
                onPolicyPublished?.(view);
              }}
            />
            </Suspense>
          </div>
        )}

        {editingIntegration && drawerTab === 'audit' && (
          <div
            role="tabpanel"
            id={`${formId}-panel-audit`}
            aria-labelledby={`${formId}-tab-audit`}
          >
            <ConfigurationAuditSection
              subjectType="integration"
              subjectId={editingIntegration.id}
              refreshKey={auditRefresh}
              destinationById={destinationById}
            />
          </div>
        )}
      </div>
    </CmsDialog>
  );
}
