import { type FormEvent, useEffect, useId, useMemo, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { ApiError, apiRequest } from '../../utils/api';
import { getErrorMessage } from '../../utils/conversionStatus';
import CustomSelect from '../CustomSelect';
import { SkeletonRows, TableRegion } from '../ui/Primitives';
import { InlineErrorBanner } from './ConversionShared';
import { type BandEdit, PaymentActionFields, PaymentPolicyBandDialog } from './PaymentPolicyBandDialog';
import {
  type ActionFields,
  type BandChange,
  type BandInput,
  type BandRow,
  type PaymentPolicyView,
  type PaymentRange,
  type PolicyDraft,
  type PolicyOp,
  actionFields,
  actionLabel,
  bandChangeOperation,
  bandLabel,
  overlappingBand,
  parseAction,
  discardPaymentDraft,
  paymentBandRows,
  paymentKindLabel,
  policyEndpoint,
  policySourceLabels,
  policyVersion,
  reconcileBandChanges,
  showMinor,
} from './paymentPolicy';
import type { ConversionIntegration } from './types';

const rangeOptions = [
  { value: 'all_paid', label: '全部有效付款' },
  { value: 'first_paid_on_promotion', label: '仅用户在本链接的首单' },
];
const disabledFirstText: Record<string, string> = {
  payment_policy_first_requires_promotion: '仅推广链接专属规则可配置首单',
  payment_policy_first_coverage_unverified: '历史付款覆盖尚未核实',
  payment_policy_first_source_unsupported: '分销来源暂不支持首单规则',
};
type ScopeSelection = { app: string; promotion: string };
type PolicyPublication = PolicyDraft & { publication_id: string };
interface PolicyPublicationResult {
  publication_id: string;
  record: {
    action: 'publish_payment_policy' | 'inherit_payment_policy';
    version_id: string | null;
    version_number: string | null;
  } | null;
  current: PaymentPolicyView | null;
  current_status?: 'available' | 'app_unauthorized';
}

function publicationVersion(number: string | null, id: string | null) {
  return number && id ? `v${number} · #${id}` : '默认全传';
}

export function PaymentPolicyPanel({
  integration,
  canWrite,
  initialPromotion = '',
  footer,
  onClose,
  onState,
  onPublished,
}: {
  integration: ConversionIntegration;
  canWrite: boolean;
  initialPromotion?: string;
  footer: HTMLElement | null;
  onClose: () => void;
  onState: (dirty: boolean, busy: boolean) => void;
  onPublished: (view: PaymentPolicyView | null) => void;
}) {
  const id = useId();
  const isReader = integration.source === 'starnovel';
  const fixedLink = Boolean(initialPromotion);
  const [scope, setScope] = useState<ScopeSelection>({
    app: isReader ? 'reader' : (integration.allowed_apps?.[0] ?? ''),
    promotion: initialPromotion,
  });
  const [promotionMode, setPromotionMode] = useState(Boolean(initialPromotion));
  const [promotionInput, setPromotionInput] = useState(initialPromotion);
  const [view, setView] = useState<PaymentPolicyView | null>(null);
  const [edit, setEdit] = useState<BandEdit | null>(null);
  const [bands, setBands] = useState<BandChange[]>([]);
  const [fallbacks, setFallbacks] = useState<Record<string, ActionFields | null>>({});
  const [removedGroups, setRemovedGroups] = useState<string[]>([]);
  const [group, setGroup] = useState('');
  const [range, setRange] = useState<PaymentRange>();
  const [deactivate, setDeactivate] = useState(false);
  const [error, setError] = useState<{ message: string; field?: string }>({ message: '' });
  const [loading, setLoading] = useState(false);
  const [pending, setPending] = useState<'publish' | 'verify' | ''>('');
  const [conflict, setConflict] = useState(false);
  const [uncertain, setUncertain] = useState<PolicyPublication | null>(null);
  const [notice, setNotice] = useState('');
  const [reload, setReload] = useState(0);
  const seq = useRef(0);
  const panel = useRef<HTMLDivElement>(null);
  const statusMessage = useRef<HTMLDivElement>(null);
  const requestBusy = useRef(false);
  const dirty =
    bands.length > 0 ||
    Object.keys(fallbacks).length > 0 ||
    removedGroups.length > 0 ||
    range !== undefined ||
    deactivate;
  const active = Boolean(scope.app && (!promotionMode || scope.promotion));
  const path = `/conversion/integrations/${encodeURIComponent(integration.id)}/payment-policy`;
  const busy = Boolean(pending);
  const rows = useMemo(() => (view ? paymentBandRows(view.config, bands) : []), [view, bands]);
  const bandConflicts = useMemo(
    () => (view ? reconcileBandChanges(view.config, bands).conflicts : []),
    [view, bands],
  );
  const conflictingKeys = new Set(bandConflicts.map(({ key }) => key));

  useEffect(() => {
    onState(dirty || Boolean(uncertain), busy);
  }, [dirty, uncertain, busy, onState]);
  useEffect(() => () => onState(false, false), [onState]);

  useEffect(() => {
    if (!active) return;
    const controller = new AbortController();
    setLoading(true);
    setView(null);
    void apiRequest(
      'GET',
      policyEndpoint(integration.id, scope.app, scope.promotion),
      undefined,
      false,
      controller.signal,
    )
      .then((data: PaymentPolicyView) => {
        if (!controller.signal.aborted) {
          setView(data);
          setError({ message: '' });
        }
      })
      .catch((err: unknown) => {
        if (!controller.signal.aborted) setError({ message: getErrorMessage(err, '读取回传规则失败') });
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
  }, [active, integration.id, scope.app, scope.promotion, reload]);

  function clearFeedback() {
    setError({ message: '' });
    setNotice('');
  }

  function clearDraft() {
    setBands([]);
    setFallbacks({});
    setRemovedGroups([]);
    setRange(undefined);
    setDeactivate(false);
    setConflict(false);
    setUncertain(null);
    clearFeedback();
  }

  function changeScope(update: () => void) {
    if (busy) return;
    const change = () => {
      clearDraft();
      setView(null);
      setGroup('');
      update();
    };
    if (dirty || uncertain) discardPaymentDraft(change);
    else change();
  }

  function findPromotion(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const value = promotionInput.trim();
    if (!/^[1-9][0-9]{0,18}$/.test(value)) {
      fail('请输入正整数推广链接 ID', 'promotion_id');
      return;
    }
    if (scope.promotion === value) {
      if (conflict || uncertain || dirty) return;
      setReload((n) => n + 1);
    } else {
      changeScope(() => setScope({ ...scope, promotion: value }));
    }
  }

  function fail(message: string, field?: string) {
    setError({ message, field });
    focusField(field);
  }

  function focusField(field?: string) {
    requestAnimationFrame(() => {
      const target =
        field &&
        Array.from(panel.current?.querySelectorAll<HTMLElement>('[data-policy-field]') ?? []).find(
          (element) => element.dataset.policyField === field,
        );
      if (target) target.focus();
      else statusMessage.current?.focus();
    });
  }

  function draftPayload(): PolicyDraft | undefined {
    if (!view) return undefined;
    const base = {
      app_id: view.app_id,
      promotion_id: view.promotion_id,
      expected_version: view.own_version?.id ?? null,
      expected_inherited_version: view.inherited_version?.id ?? null,
    };
    if (deactivate) return { ...base, deactivate: true, operations: [] };
    const overlap = overlappingBand(rows);
    if (overlap) {
      setGroup(overlap.kind);
      fail('同一付款类型和币种下，金额区间不可重叠', overlap.key);
      return undefined;
    }
    const operations: PolicyOp[] = bands.map(bandChangeOperation);
    for (const kind of removedGroups) operations.push({ op: 'remove_group', payment_kind: kind });
    for (const [kind, fields] of Object.entries(fallbacks)) {
      if (fields === null) {
        if (view.config.groups.some((item) => item.payment_kind === kind && item.fallback)) {
          operations.push({ op: 'remove_fallback', payment_kind: kind });
        }
      } else {
        const action = parseAction(fields);
        if (!action) {
          setGroup(kind);
          fail(`${paymentKindLabel(kind)}的兜底参数无效，请检查比例或循环笔数`, 'fallback');
          return undefined;
        }
        operations.push({ op: 'set_fallback', payment_kind: kind, action });
      }
    }
    return { ...base, payment_range: range, operations };
  }

  function handlePublishError(err: unknown, publication: PolicyPublication) {
    if (err instanceof ApiError && err.status === 409) {
      setConflict(true);
      fail('生效规则版本已变化。草稿已保留，请查看最新配置后重试');
    } else if (!(err instanceof ApiError) || err.status >= 500) {
      setUncertain(publication);
      fail('发布结果待确认。请读取最新配置和审计记录后再操作');
    } else {
      let field = err instanceof ApiError ? err.field : undefined;
      const match = field?.match(/^operations\.(\d+)$/);
      if (match) {
        const owner = bands[Number(match[1])];
        if (owner) setGroup(owner.kind);
        field = owner?.key ?? 'fallback';
      }
      const message =
        err instanceof ApiError && err.code === 'invalid_operation'
          ? '回传规则请求无效，请核对配置后重试'
          : getErrorMessage(err, '发布规则失败');
      fail(message, field);
    }
  }

  async function publish() {
    if (!canWrite || requestBusy.current || conflict || uncertain || bandConflicts.length || !dirty) return;
    const draft = draftPayload();
    if (!draft) return;
    const publication: PolicyPublication = {
      ...draft,
      publication_id: Array.from(crypto.getRandomValues(new Uint8Array(16)), (byte) =>
        byte.toString(16).padStart(2, '0'),
      ).join(''),
    };
    requestBusy.current = true;
    setPending('publish');
    setError({ message: '' });
    try {
      const next: PaymentPolicyView = await apiRequest(
        'POST',
        `${path}/publish`,
        publication,
        false,
        AbortSignal.timeout(15000),
      );
      setView(next);
      clearDraft();
      setNotice('回传规则已发布');
      onPublished(next);
    } catch (err) {
      handlePublishError(err, publication);
    } finally {
      requestBusy.current = false;
      setPending('');
    }
  }

  function rebaseDraft(next: PaymentPolicyView) {
    const reconciled = reconcileBandChanges(next.config, bands);
    const groups = new Map(next.config.groups.map((item) => [item.payment_kind, item]));
    setBands(reconciled.bands);
    setRemovedGroups(removedGroups.filter((kind) => groups.has(kind)));
    setFallbacks(
      Object.fromEntries(
        Object.entries(fallbacks).filter(([kind, fields]) => fields !== null || groups.get(kind)?.fallback),
      ),
    );
    if (range === next.config.payment_range) setRange(undefined);
    if (!next.own_version) setDeactivate(false);
    const firstConflict = reconciled.conflicts[0];
    if (firstConflict) {
      setGroup(firstConflict.kind);
      focusField(firstConflict.key);
    }
    return reconciled.bands.length !== bands.length;
  }

  async function verifyLatest() {
    if (requestBusy.current) return;
    requestBusy.current = true;
    setPending('verify');
    try {
      let next: PaymentPolicyView | null;
      if (uncertain) {
        const params = new URLSearchParams({
          subject_type: 'integration',
          subject_id: integration.id,
          publication_id: uncertain.publication_id,
          app_id: uncertain.app_id,
        });
        if (uncertain.promotion_id) params.set('promotion_id', uncertain.promotion_id);
        const result: PolicyPublicationResult = await apiRequest('GET', `/conversion/audit?${params}`);
        const action = uncertain.deactivate ? 'inherit_payment_policy' : 'publish_payment_policy';
        const unauthorized = result?.current_status === 'app_unauthorized' && result.current === null;
        const available =
          (result?.current_status === undefined || result.current_status === 'available') && result?.current;
        if (
          result?.publication_id !== uncertain.publication_id ||
          (!available && !unauthorized) ||
          result.record === undefined ||
          (result.record && result.record.action !== action)
        ) {
          throw new Error('本次发布状态暂无法核对');
        }
        next = result.current;
        const record = result.record;
        if (record) {
          const publishedVersion = publicationVersion(record.version_number, record.version_id);
          const submitted = uncertain.deactivate
            ? `已确认恢复继承成功。提交时生效 ${publishedVersion}。`
            : `已确认本次发布成功。发布版本 ${publishedVersion}。`;
          clearDraft();
          if (next) {
            const current = policyVersion(next);
            const currentVersion = publicationVersion(current?.number ?? null, current?.id ?? null);
            const superseded = record.version_id !== (current?.id ?? null) ? '本次生效版本已被替代。' : '';
            setNotice(`${submitted}${superseded}当前生效 ${currentVersion}。`);
          } else {
            setNotice(`${submitted}当前应用已解除授权。`);
          }
          onPublished(next);
        } else {
          if (next) {
            rebaseDraft(next);
            setNotice('已刷新生效版本和审计记录。未找到本次发布记录，请核对草稿');
          } else {
            setNotice('未找到本次发布记录。当前应用已解除授权，草稿已保留');
          }
          setUncertain(null);
        }
      } else {
        next = await apiRequest('GET', policyEndpoint(integration.id, scope.app, scope.promotion));
        if (!next) throw new Error('读取回传规则失败');
        const removed = rebaseDraft(next);
        setNotice(
          removed ? '已加载最新生效规则，草稿中已完成的区间删除已移除' : '已加载最新生效规则，请核对草稿',
        );
      }
      setView(next);
      setConflict(false);
      setError({ message: '' });
    } catch (err) {
      fail(getErrorMessage(err, '核对最新配置失败'));
    } finally {
      requestBusy.current = false;
      setPending('');
    }
  }

  function addEdit(type: BandEdit['type'], rows: BandRow[]) {
    setEdit({ key: ++seq.current, kind: group, type, rows });
  }

  function saveBandEdit(edit: BandEdit, currency: string, values: BandInput[]) {
    const [first, second] = edit.rows;
    const changes: BandChange[] = values.map((value, index) => {
      // Splitting and merging keep the lowest band's identity.
      const original = index === 0 ? first : undefined;
      return {
        key: original?.key ?? `draft-band-${++seq.current}`,
        kind: edit.kind,
        band: { ...value, currency, branch_id: original?.band.branch_id },
      };
    });
    const removed = second?.band.branch_id;
    if (removed) changes.push({ key: second.key, kind: second.kind, band: null, branchId: removed });
    const replaced = new Set(edit.rows.map(({ key }) => key));
    setBands((current) => {
      const editGroups = new Set(
        current.flatMap(({ key, editGroup }) =>
          replaced.has(key) && editGroup !== undefined ? [editGroup] : [],
        ),
      );
      const remaining = current.filter(({ key }) => !replaced.has(key));
      if (changes.length === 1 && editGroups.size === 0) return [...remaining, ...changes];
      // Keep the dependent changes together if a split or merge has to be discarded later.
      return [
        ...remaining.map((change) =>
          change.editGroup !== undefined && editGroups.has(change.editGroup)
            ? { ...change, editGroup: edit.key }
            : change,
        ),
        ...changes.map((change) => ({ ...change, editGroup: edit.key })),
      ];
    });
    clearFeedback();
    setEdit(null);
  }

  function deleteBand(row: BandRow, focusKey: string) {
    const branchId = row.band.branch_id;
    setBands((current) => {
      const remaining = current.filter(({ key }) => key !== row.key);
      if (branchId) remaining.push({ key: row.key, kind: row.kind, band: null, branchId });
      return remaining;
    });
    clearFeedback();
    focusField(focusKey);
  }

  function discardBandConflict(key: string) {
    const affectedGroup = bands.find((change) => change.key === key)?.editGroup;
    const keep = (change: BandChange) =>
      change.key !== key && (affectedGroup === undefined || change.editGroup !== affectedGroup);
    setBands((current) => current.filter(keep));
    clearFeedback();
    const next = bandConflicts.find(keep);
    if (next) setGroup(next.kind);
    focusField(next?.key ?? 'add-band');
  }

  const effectiveGroup = view?.config.groups.find((item) => item.payment_kind === group);
  const removeGroup = removedGroups.includes(group);
  const fallback =
    group in fallbacks ? fallbacks[group] : effectiveGroup?.fallback && actionFields(effectiveGroup.fallback);
  const version = view && policyVersion(view);
  const currentBands = rows.filter(({ kind }) => kind === group);
  const locked = busy || deactivate || removeGroup;
  const footerButtons = (
    <>
      <button type="button" className="btn-secondary" onClick={onClose} disabled={busy}>
        {canWrite ? '取消' : '关闭'}
      </button>
      {canWrite && (
        <>
          <button
            type="button"
            className="btn-primary"
            onClick={() => void publish()}
            disabled={busy || !view || !dirty || conflict || Boolean(uncertain) || bandConflicts.length > 0}
          >
            {pending === 'publish' ? '发布中…' : '发布'}
          </button>
        </>
      )}
    </>
  );

  return (
    <div ref={panel} className="space-y-4 text-xs">
      <form method="get" onSubmit={findPromotion} className="grid sm:grid-cols-3 gap-3">
        <div className="space-y-1">
          <label htmlFor={`${id}-app`} className="block text-[var(--cms-muted-fg)]">
            应用
          </label>
          {isReader ? (
            <p id={`${id}-app`} className="m-0 py-2">
              Reader
            </p>
          ) : (
            <CustomSelect
              id={`${id}-app`}
              name="app_id"
              width="100%"
              value={scope.app}
              options={(integration.allowed_apps ?? []).map((app) => ({ value: app, label: app }))}
              onChange={(app) => changeScope(() => setScope({ ...scope, app }))}
              disabled={busy}
            />
          )}
        </div>
        {fixedLink ? (
          <p className="m-0 self-end py-2">推广链接 #{initialPromotion}</p>
        ) : (
          <>
            <div className="space-y-1">
              <label htmlFor={`${id}-scope`} className="block text-[var(--cms-muted-fg)]">
                作用域
              </label>
              <CustomSelect
                id={`${id}-scope`}
                name="scope"
                width="100%"
                value={promotionMode ? 'link' : 'default'}
                options={[
                  { value: 'default', label: '接入默认' },
                  { value: 'link', label: '指定推广链接' },
                ]}
                onChange={(value) =>
                  changeScope(() => {
                    setPromotionMode(value === 'link');
                    setPromotionInput('');
                    setScope({ ...scope, promotion: '' });
                  })
                }
                disabled={busy}
              />
            </div>
            {promotionMode && (
              <div className="space-y-1 min-w-0">
                <label htmlFor={`${id}-promotion`} className="block text-[var(--cms-muted-fg)]">
                  推广链接 ID
                </label>
                <div className="flex gap-2">
                  <input
                    id={`${id}-promotion`}
                    name="promotion_id"
                    value={promotionInput}
                    className="input-field min-w-0 tabular-nums"
                    inputMode="numeric"
                    maxLength={19}
                    aria-invalid={error.field === 'promotion_id'}
                    data-policy-field="promotion_id"
                    aria-describedby={error.field === 'promotion_id' ? `${id}-error` : undefined}
                    onChange={(e) => {
                      setPromotionInput(e.target.value);
                      if (error.field === 'promotion_id') setError({ message: '' });
                    }}
                    disabled={busy}
                  />
                  <button type="submit" className="btn-secondary shrink-0" disabled={busy}>
                    查询
                  </button>
                </div>
              </div>
            )}
          </>
        )}
      </form>

      <div id={`${id}-error`} ref={statusMessage} tabIndex={-1}>
        {error.message && (
          <InlineErrorBanner
            message={error.message}
            onRetry={!view && !loading && active ? () => setReload((n) => n + 1) : undefined}
          />
        )}
      </div>
      {(conflict || uncertain) && (
        <button type="button" className="btn-secondary" onClick={() => void verifyLatest()} disabled={busy}>
          查看最新配置
        </button>
      )}
      <div role="status" className="space-y-1">
        {notice && <p className="m-0 text-[var(--cms-muted-fg)]">{notice}</p>}
        {bandConflicts.length > 0 && (
          <p className="m-0 text-[var(--cms-muted-fg)]">
            {bandConflicts.length} 项区间编辑冲突，请在对应付款类型下撤销冲突编辑后发布。
          </p>
        )}
      </div>
      {active && loading && (
        <TableRegion ariaLabel="正在加载回传规则">
          <table>
            <tbody>
              <SkeletonRows rows={3} cols={4} />
            </tbody>
          </table>
        </TableRegion>
      )}
      {!scope.app && <p className="text-[var(--cms-muted-fg)]">请先在接入配置中授权应用。</p>}
      {view && active && !loading && (
        <>
          <div className="flex flex-wrap gap-x-4 gap-y-2 items-center rounded-xl border border-[var(--cms-border)] px-4 py-3">
            {view.promotion_name && (
              <strong>
                {view.promotion_name} · #{view.promotion_id}
              </strong>
            )}
            <strong>
              {view.config_source === 'own' && !view.promotion_id
                ? '接入默认规则'
                : policySourceLabels[view.config_source]}
            </strong>
            {version && (
              <span className="tabular-nums">
                生效版本 v{version.number} · #{version.id}
              </span>
            )}
            {canWrite && view.promotion_id && view.own_version && (
              <button
                type="button"
                className="btn-ghost ml-auto"
                disabled={busy}
                onClick={() => {
                  const toggle = () => {
                    clearDraft();
                    setDeactivate(!deactivate);
                  };
                  if (dirty && !deactivate) discardPaymentDraft(toggle);
                  else toggle();
                }}
              >
                {deactivate ? '撤销恢复继承' : '恢复继承'}
              </button>
            )}
          </div>
          {deactivate ? (
            <p className="text-[var(--cms-muted-fg)]">发布后，本链接使用当前接入默认规则。</p>
          ) : (
            <>
              <div className="grid sm:grid-cols-2 gap-3">
                <div className="space-y-1">
                  <label htmlFor={`${id}-range`} className="block text-[var(--cms-muted-fg)]">
                    付款范围
                  </label>
                  <CustomSelect
                    id={`${id}-range`}
                    name="payment_range"
                    width="100%"
                    data-policy-field="payment_range"
                    aria-invalid={error.field === 'payment_range'}
                    aria-describedby={error.field === 'payment_range' ? `${id}-error` : undefined}
                    options={view.first_paid_on_promotion ? rangeOptions : rangeOptions.slice(0, 1)}
                    value={range ?? view.config.payment_range}
                    disabled={!canWrite || busy}
                    onChange={(value) => {
                      setRange(value === view.config.payment_range ? undefined : (value as PaymentRange));
                      clearFeedback();
                    }}
                  />
                  {!view.first_paid_on_promotion && view.first_paid_disabled_reason && (
                    <p className="text-[var(--cms-muted-fg)] m-0">
                      {disabledFirstText[view.first_paid_disabled_reason]}
                    </p>
                  )}
                </div>
                <div className="space-y-1">
                  <label htmlFor={`${id}-kind`} className="block text-[var(--cms-muted-fg)]">
                    付款类型
                  </label>
                  <CustomSelect
                    id={`${id}-kind`}
                    name="payment_kind"
                    width="100%"
                    value={group}
                    disabled={busy}
                    options={['', ...view.payment_kinds].map((kind) => ({
                      value: kind,
                      label: paymentKindLabel(kind),
                    }))}
                    onChange={setGroup}
                  />
                  <p className="text-[var(--cms-muted-fg)] m-0">
                    先选付款类型组，再匹配同币种区间，最后使用本组兜底。
                  </p>
                </div>
              </div>

              <div className="space-y-2 border-t border-[var(--cms-border)] pt-3">
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <h3 className="font-medium m-0">本组兜底</h3>
                  {canWrite && !locked && (
                    <button
                      type="button"
                      className="btn-ghost"
                      onClick={() => {
                        setFallbacks((current) => ({
                          ...current,
                          [group]: fallback ? null : actionFields(),
                        }));
                        clearFeedback();
                      }}
                    >
                      {fallback ? '清除兜底' : '设置兜底'}
                    </button>
                  )}
                </div>
                {fallback && !removeGroup ? (
                  <div tabIndex={-1} data-policy-field="fallback">
                    {canWrite ? (
                      <PaymentActionFields
                        id={`${id}-fallback`}
                        title="区间未命中时"
                        fields={fallback}
                        disabled={busy}
                        error={error.field === 'fallback' ? error.message : undefined}
                        setFields={(fields) => {
                          setFallbacks((current) => ({ ...current, [group]: fields }));
                          clearFeedback();
                        }}
                      />
                    ) : (
                      <p className="m-0">{actionLabel(effectiveGroup!.fallback!)}</p>
                    )}
                  </div>
                ) : (
                  <p className="m-0 text-[var(--cms-muted-fg)]">
                    {removeGroup ? '待移除本组，发布后使用通用组' : '本组未命中时全传'}
                  </p>
                )}
              </div>

              <div className="flex flex-wrap items-center justify-between gap-2">
                <h3 className="font-medium m-0">金额区间</h3>
                {canWrite && (
                  <div className="flex gap-2">
                    {(effectiveGroup || removeGroup) && group && (
                      <button
                        type="button"
                        className="btn-ghost"
                        disabled={busy}
                        onClick={() => {
                          if (removeGroup)
                            setRemovedGroups((current) => current.filter((kind) => kind !== group));
                          else {
                            setBands((current) => current.filter((change) => change.kind !== group));
                            setFallbacks(({ [group]: _previous, ...current }) => current);
                            setRemovedGroups((current) => [...current, group]);
                          }
                          clearFeedback();
                        }}
                      >
                        {removeGroup ? '撤销移除分组' : '移除类型分组'}
                      </button>
                    )}
                    <button
                      type="button"
                      className="btn-secondary"
                      data-policy-field="add-band"
                      onClick={() => addEdit('add_band', [])}
                      disabled={locked}
                    >
                      新增区间
                    </button>
                  </div>
                )}
              </div>
              <PolicyBandTable
                rows={currentBands}
                branches={view.branches}
                actions={
                  canWrite
                    ? (row, i) => {
                        const next = currentBands[i + 1];
                        const { band } = row;
                        const label = bandLabel(band);
                        if (conflictingKeys.has(row.key)) {
                          const description = `${id}-conflict-${row.key}`;
                          return (
                            <div className="flex items-center gap-1 whitespace-nowrap">
                              <span id={description}>该区间已不在生效规则中</span>
                              <button
                                type="button"
                                className="btn-ghost"
                                aria-label={`撤销冲突编辑 ${label}`}
                                aria-describedby={description}
                                data-policy-field={row.key}
                                disabled={busy}
                                onClick={() => discardBandConflict(row.key)}
                              >
                                撤销冲突编辑
                              </button>
                            </div>
                          );
                        }
                        return (
                          <div className="flex items-center gap-1 whitespace-nowrap">
                            <button
                              type="button"
                              className="btn-ghost"
                              aria-label={`编辑 ${label}`}
                              data-policy-field={row.key}
                              disabled={locked}
                              onClick={() => addEdit('update_band', [row])}
                            >
                              编辑
                            </button>
                            <button
                              type="button"
                              className="btn-ghost"
                              aria-label={`拆分 ${label}`}
                              disabled={locked}
                              onClick={() => addEdit('split_band', [row])}
                            >
                              拆分
                            </button>
                            {next &&
                              band.currency === next.band.currency &&
                              !conflictingKeys.has(next.key) &&
                              band.max_minor === next.band.min_minor && (
                                <button
                                  type="button"
                                  className="btn-ghost"
                                  aria-label={`合并 ${label} 与下一段`}
                                  disabled={locked}
                                  onClick={() => addEdit('merge_bands', [row, next])}
                                >
                                  合并
                                </button>
                              )}
                            <button
                              type="button"
                              className="btn-ghost"
                              aria-label={`删除 ${label}`}
                              disabled={locked}
                              onClick={() =>
                                deleteBand(row, next?.key ?? currentBands[i - 1]?.key ?? 'add-band')
                              }
                            >
                              删除
                            </button>
                          </div>
                        );
                      }
                    : undefined
                }
              />
            </>
          )}
        </>
      )}

      {footer && createPortal(footerButtons, footer)}
      {edit && (
        <PaymentPolicyBandDialog
          key={edit.key}
          edit={edit}
          onClose={() => setEdit(null)}
          onSave={(currency, values) => saveBandEdit(edit, currency, values)}
        />
      )}
    </div>
  );
}

function sequenceLabel(branch: PaymentPolicyView['branches'][number] | undefined) {
  return branch?.next_sequence ? ` · 下一笔第 ${branch.next_sequence} 笔` : '';
}

function PolicyBandTable({
  rows,
  branches,
  actions,
}: {
  rows: BandRow[];
  branches: PaymentPolicyView['branches'];
  actions?: (row: BandRow, index: number) => React.ReactNode;
}) {
  return (
    <TableRegion ariaLabel="金额区间">
      <table className="cms-table text-xs whitespace-nowrap">
        <thead>
          <tr>
            <th scope="col">币种</th>
            <th scope="col">最小金额（含）</th>
            <th scope="col">最大金额（不含）</th>
            <th scope="col">回传方式与参数</th>
            {actions && <th scope="col">操作</th>}
          </tr>
        </thead>
        <tbody>
          {rows.length === 0 && (
            <tr>
              <td colSpan={actions ? 5 : 4} className="text-[var(--cms-muted-fg)]">
                暂无金额区间
              </td>
            </tr>
          )}
          {rows.map((row, i) => {
            const { band } = row;
            return (
              <tr key={row.key}>
                <td>{band.currency}</td>
                <td className="tabular-nums">{showMinor(band.min_minor, band.currency)}</td>
                <td className="tabular-nums">
                  {band.max_minor === null ? '无上限' : showMinor(band.max_minor, band.currency)}
                </td>
                <td>
                  {actionLabel(band)}
                  {band.mode === 'cycle' &&
                    sequenceLabel(
                      branches.find(
                        (branch) =>
                          branch.payment_kind === row.kind &&
                          branch.currency === band.currency &&
                          band.branch_id !== undefined &&
                          branch.branch_id === band.branch_id,
                      ),
                    )}
                </td>
                {actions && <td>{actions(row, i)}</td>}
              </tr>
            );
          })}
        </tbody>
      </table>
    </TableRegion>
  );
}
