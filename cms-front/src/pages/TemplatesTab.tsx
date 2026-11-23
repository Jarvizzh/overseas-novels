import React, { useCallback, useContext, useEffect, useId, useMemo, useState } from 'react';
import { Link, useLocation, useNavigate, useParams, useSearchParams } from 'react-router-dom';
import {
  ArrowDown,
  ArrowUp,
  BookOpen,
  Check,
  ChevronLeft,
  Copy,
  Crown,
  Lock,
  Plus,
  RefreshCw,
  Save,
  Sparkles,
  Star,
  Trash2,
} from 'lucide-react';
import CustomSelect from '../components/CustomSelect';
import { Pagination } from '../components/ui/Pagination';
import { SearchInput, SkeletonRows, StatusDot, TableRegion } from '../components/ui/Primitives';
import { ConfirmContext } from '../context/ConfirmContext';
import { ToastContext } from '../context/ToastContext';
import {
  NOVEL_SCOPE_LABELS,
  type PaymentEnvironment,
  PRODUCT_TYPE_LABELS,
  productsApi,
  type ProductView,
  type RechargeTemplateDTO,
  templatesApi,
  type TemplateSlotDTO,
  type TemplateSlotWriteItem,
  VIP_CYCLE_LABELS,
  type VipCycle,
} from '../utils/paymentCatalogApi';
import {
  canDeleteTemplate,
  canSetDefaultTemplate,
  filterEligibleTemplateProducts,
  shouldLockDefaultTemplate,
} from '../utils/templateCatalogHelpers';

type SlotDraft = {
  uid: string;
  product_id: number;
  badge: string;
  is_highlighted: boolean;
  display_alias: string;
};

let draftSeq = 1;
function nextDraftUid(): string {
  draftSeq += 1;
  return `tpl-slot-${draftSeq}`;
}

function summarizeSlots(slots: TemplateSlotDTO[]) {
  let single = 0;
  let book = 0;
  let vip = 0;
  let minCents = Infinity;
  let maxCents = 0;
  for (const s of slots || []) {
    const pType = s.product_type || (s.type === 'subscription' ? 'vip' : s.type);
    if (pType === 'vip') vip += 1;
    else if (pType === 'whole_book') book += 1;
    else single += 1;
    const cents = s.price_cents || 0;
    if (cents > 0) {
      minCents = Math.min(minCents, cents);
      maxCents = Math.max(maxCents, cents);
    }
  }
  const parts: string[] = [];
  if (single > 0) parts.push(`${single} 金币`);
  if (book > 0) parts.push(`${book} 整本`);
  if (vip > 0) parts.push(`${vip} VIP`);
  return {
    summary: parts.join(' · ') || '暂无槽位',
    priceRange: minCents === Infinity ? '$0.00' : `$${(minCents / 100).toFixed(2)} – $${(maxCents / 100).toFixed(2)}`,
  };
}

export default function TemplatesTab() {
  const navigate = useNavigate();
  const location = useLocation();
  const { templateId } = useParams<{ templateId?: string }>();
  const [searchParams] = useSearchParams();
  const toastCtx = useContext(ToastContext);
  const confirmCtx = useContext(ConfirmContext);
  const showToast = useCallback(
    (msg: string, type: 'success' | 'error' | 'info' = 'success') => {
      if (toastCtx?.showToast) toastCtx.showToast(msg, type);
      else window.showToast?.(msg, type);
    },
    [toastCtx],
  );
  const formId = useId();

  const isNewRoute = location.pathname === '/recharge-templates/new';
  const editTemplateId = !isNewRoute && templateId ? Number(templateId) : null;
  const isEditor = isNewRoute || (editTemplateId !== null && Number.isInteger(editTemplateId) && editTemplateId > 0);
  const copyFromId = isNewRoute ? Number(searchParams.get('copyFrom') || 0) : 0;

  // List state
  const [templates, setTemplates] = useState<RechargeTemplateDTO[] | null>(null);
  const [loadError, setLoadError] = useState('');
  const [search, setSearch] = useState('');
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(10);

  // Published product catalog for editor dropdown grouped by environment
  const [liveProducts, setLiveProducts] = useState<ProductView[]>([]);
  const [sandboxProducts, setSandboxProducts] = useState<ProductView[]>([]);
  const [productsLoading, setProductsLoading] = useState(false);

  // Editor state
  const [tplName, setTplName] = useState('');
  const [tplEnv, setTplEnv] = useState<PaymentEnvironment>('live');
  const [tplIsDefault, setTplIsDefault] = useState(false);
  const [drafts, setDrafts] = useState<SlotDraft[]>([]);
  const [previewTheme, setPreviewTheme] = useState<'light' | 'dark'>('light');
  const [selectedPreviewIdx, setSelectedPreviewIdx] = useState(0);
  const [saving, setSaving] = useState(false);
  const [editorError, setEditorError] = useState('');

  const loadTemplates = useCallback(async (signal?: AbortSignal) => {
    setLoadError('');
    try {
      const list = await templatesApi.list(signal);
      if (!signal?.aborted) setTemplates(Array.isArray(list) ? list : []);
    } catch (err) {
      if (!signal?.aborted) setLoadError(err instanceof Error ? err.message : '加载模板列表失败');
    }
  }, []);

  const loadPublishedCatalog = useCallback(async (signal?: AbortSignal) => {
    setProductsLoading(true);
    try {
      const [sbx, live] = await Promise.all([
        productsApi.list({ environment: 'sandbox', status: 'published' }, signal),
        productsApi.list({ environment: 'live', status: 'published' }, signal),
      ]);
      if (signal?.aborted) return;
      setLiveProducts(live.products ?? []);
      setSandboxProducts(sbx.products ?? []);
    } catch {
      if (!signal?.aborted) {
        setLiveProducts([]);
        setSandboxProducts([]);
      }
    } finally {
      if (!signal?.aborted) setProductsLoading(false);
    }
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    void loadTemplates(controller.signal);
    if (isEditor) void loadPublishedCatalog(controller.signal);
    return () => controller.abort();
  }, [loadTemplates, loadPublishedCatalog, isEditor]);

  // Populate editor form when templates finish loading
  useEffect(() => {
    if (!isEditor || !templates) return;
    setEditorError('');
    if (editTemplateId !== null) {
      const target = templates.find((t) => t.id === editTemplateId);
      if (!target) {
        setEditorError('未找到要编辑的充值模板');
        return;
      }
      setTplName(target.name);
      setTplEnv(target.environment === 'sandbox' ? 'sandbox' : 'live');
      setTplIsDefault(target.is_default);
      setDrafts(
        (target.slots || []).map((s) => ({
          uid: nextDraftUid(),
          product_id: s.product_id,
          badge: s.badge || '',
          is_highlighted: Boolean(s.is_highlighted),
          display_alias: s.display_alias || '',
        })),
      );
      return;
    }

    if (copyFromId > 0) {
      const src = templates.find((t) => t.id === copyFromId);
      if (src) {
        setTplName(`${src.name} - 副本`);
        setTplEnv(src.environment === 'sandbox' ? 'sandbox' : 'live');
        setTplIsDefault(false);
        setDrafts(
          (src.slots || []).map((s) => ({
            uid: nextDraftUid(),
            product_id: s.product_id,
            badge: s.badge || '',
            is_highlighted: Boolean(s.is_highlighted),
            display_alias: s.display_alias || '',
          })),
        );
        return;
      }
    }

    setTplName('');
    setTplEnv('live');
    setTplIsDefault(false);
    setDrafts([]);
  }, [isEditor, editTemplateId, copyFromId, templates]);

  const envPublishedProducts = useMemo(
    () => (tplEnv === 'live' ? liveProducts : sandboxProducts),
    [tplEnv, liveProducts, sandboxProducts],
  );
  const eligibleProducts = useMemo(
    () => filterEligibleTemplateProducts(envPublishedProducts),
    [envPublishedProducts],
  );
  const eligibleProductMap = useMemo(() => {
    const map = new Map<number, ProductView>();
    for (const p of eligibleProducts) map.set(p.id, p);
    return map;
  }, [eligibleProducts]);
  const productMap = useMemo(() => {
    const map = new Map<number, ProductView>();
    for (const p of [...sandboxProducts, ...liveProducts]) map.set(p.id, p);
    for (const p of envPublishedProducts) map.set(p.id, p);
    return map;
  }, [liveProducts, sandboxProducts, envPublishedProducts]);
  const lockDefaultCheckbox = useMemo(
    () => shouldLockDefaultTemplate(templates ?? [], editTemplateId),
    [templates, editTemplateId],
  );

  // Also index slot product metadata already hydrated from GET /recharge-templates so existing slot details remain readable even if paused later
  const fallbackSlotMetaMap = useMemo(() => {
    const map = new Map<number, TemplateSlotDTO>();
    for (const t of templates ?? []) {
      for (const s of t.slots || []) {
        if (s.product_id > 0 && !map.has(s.product_id)) map.set(s.product_id, s);
      }
    }
    return map;
  }, [templates]);

  const filteredTemplates = useMemo(() => {
    if (!templates) return [];
    const q = search.trim().toLowerCase();
    if (!q) return templates;
    return templates.filter((t) => t.name.toLowerCase().includes(q) || String(t.id).includes(q));
  }, [templates, search]);

  const pagedTemplates = useMemo(() => {
    const start = (page - 1) * pageSize;
    return filteredTemplates.slice(start, start + pageSize);
  }, [filteredTemplates, page, pageSize]);

  const handleSetDefault = async (tpl: RechargeTemplateDTO) => {
    try {
      await templatesApi.setDefault(tpl.id);
      showToast(`已将「${tpl.name}」设为默认模板`, 'success');
      await loadTemplates();
    } catch (err) {
      showToast(err instanceof Error ? err.message : '设置默认失败', 'error');
    }
  };

  const handleDelete = (tpl: RechargeTemplateDTO) => {
    if (tpl.is_default) {
      showToast('默认模板不可删除，请先设置其他模板为默认', 'error');
      return;
    }
    if ((tpl.linked_promotion_count ?? 0) > 0) {
      const names = (tpl.linked_promotion_names ?? []).slice(0, 3).join('、');
      showToast(`该模板已被 ${tpl.linked_promotion_count} 条推广链接引用${names ? `（${names}）` : ''}，无法删除`, 'error');
      return;
    }
    const runDelete = async () => {
      try {
        await templatesApi.delete(tpl.id);
        showToast('模板已删除', 'success');
        await loadTemplates();
      } catch (err) {
        showToast(err instanceof Error ? err.message : '删除失败', 'error');
      }
    };
    const msg = `确认删除模板「${tpl.name}」吗？已被推广链接引用的模板无法删除。`;
    if (confirmCtx?.showConfirm) {
      confirmCtx.showConfirm(msg, () => void runDelete());
    } else if (window.showConfirm) {
      window.showConfirm(msg, () => void runDelete());
    }
  };

  const selectedProductIds = useMemo(() => new Set(drafts.map((d) => d.product_id).filter((id) => id > 0)), [drafts]);

  const addSlot = () => {
    if (drafts.length >= 8) {
      showToast('每个模板最多只能配置 8 个槽位', 'error');
      return;
    }
    const nextUnassigned = eligibleProducts.find((p) => !selectedProductIds.has(p.id));
    setDrafts((prev) => [
      ...prev,
      {
        uid: nextDraftUid(),
        product_id: nextUnassigned?.id ?? 0,
        badge: '',
        is_highlighted: false,
        display_alias: '',
      },
    ]);
  };

  const removeSlot = (uid: string) => {
    setDrafts((prev) => prev.filter((item) => item.uid !== uid));
  };

  const moveSlot = (idx: number, delta: -1 | 1) => {
    const target = idx + delta;
    if (target < 0 || target >= drafts.length) return;
    setDrafts((prev) => {
      const copy = [...prev];
      const [moved] = copy.splice(idx, 1);
      copy.splice(target, 0, moved);
      return copy;
    });
  };

  const updateDraft = (uid: string, patch: Partial<SlotDraft>) => {
    setDrafts((prev) => prev.map((d) => (d.uid === uid ? { ...d, ...patch } : d)));
  };

  const editorValidationError = useMemo(() => {
    if (!tplName.trim()) return '请填写模板名称';
    if (drafts.length < 4 || drafts.length > 8) return `槽位数量需为 4–8 个（当前 ${drafts.length} 个）`;
    const seen = new Set<number>();
    for (let i = 0; i < drafts.length; i += 1) {
      const pid = drafts[i].product_id;
      if (!pid || pid <= 0) return `第 ${i + 1} 个槽位尚未选择已上架商品`;
      if (seen.has(pid)) return `商品 #${pid} 在同一模板中重复选择`;
      if (!productsLoading && !eligibleProductMap.has(pid)) {
        return `第 ${i + 1} 个槽位商品 #${pid} 未在 ${tplEnv} 环境下架或未满足双渠道就绪`;
      }
      seen.add(pid);
    }
    return '';
  }, [tplName, drafts, productsLoading, eligibleProductMap, tplEnv]);

  const handleSaveTemplate = async (e: React.FormEvent) => {
    e.preventDefault();
    if (saving) return;
    if (editorValidationError) {
      setEditorError(editorValidationError);
      return;
    }
    setEditorError('');
    setSaving(true);
    try {
      const slots: TemplateSlotWriteItem[] = drafts.map((d, idx) => ({
        slot_index: idx + 1,
        product_id: d.product_id,
        badge: d.badge.trim(),
        is_highlighted: d.is_highlighted,
        display_alias: d.display_alias.trim(),
      }));
      if (editTemplateId !== null) {
        await templatesApi.update(editTemplateId, { name: tplName.trim(), environment: tplEnv, is_default: tplIsDefault, slots });
        showToast('模板保存成功', 'success');
      } else {
        await templatesApi.create({ name: tplName.trim(), environment: tplEnv, is_default: tplIsDefault, slots });
        showToast('模板创建成功', 'success');
      }
      navigate('/recharge-templates');
    } catch (err) {
      setEditorError(err instanceof Error ? err.message : '保存模板失败');
    } finally {
      setSaving(false);
    }
  };

  const resolveProductSummary = (productId: number) => {
    const p = productMap.get(productId);
    if (p) {
      return {
        name: p.name,
        code: p.code,
        type: p.product_type,
        price: p.price,
        coins: p.coins,
        bonus: p.bonus,
        vipCycle: p.vip_cycle,
        novelScope: p.novel_scope_type,
        published: true,
      };
    }
    const fb = fallbackSlotMetaMap.get(productId);
    if (fb) {
      const pType = fb.product_type || (fb.type === 'subscription' ? 'vip' : fb.type) || 'single';
      return {
        name: fb.name || `商品 #${productId}`,
        code: fb.product_code || `#${productId}`,
        type: pType,
        price: fb.price || '$0.00',
        coins: fb.coins || 0,
        bonus: fb.bonus || 0,
        vipCycle: (fb.subscription_cycle || fb.vip_duration || '') as VipCycle | '',
        novelScope: fb.novel_scope_type || 'none',
        published: false,
      };
    }
    return null;
  };

  if (isEditor) {
    return (
      <div className="space-y-5">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="flex items-center gap-3">
            <button type="button" className="btn-secondary inline-flex items-center gap-1.5" onClick={() => navigate('/recharge-templates')}>
              <ChevronLeft size={16} aria-hidden="true" />
              返回列表
            </button>
            <div>
              <h1 className="text-xl font-semibold text-[var(--cms-fg)]">
                {editTemplateId ? `编辑充值模板 #${editTemplateId}` : '新建充值模板'}
              </h1>
              <p className="text-xs text-[var(--cms-muted-fg)] mt-0.5">
                模板严格只存槽位序号、商品引用与非商业 UI 字段；商业定价由「商品与定价」统一管控。
              </p>
            </div>
          </div>

          <div className="flex items-center gap-2">
            <Link to="/payments/products" className="btn-secondary text-xs">管理 / 新建商品</Link>
            <button type="submit" form={`${formId}-editor`} className="btn-primary inline-flex items-center gap-1.5" disabled={saving}>
              <Save size={15} aria-hidden="true" />
              {saving ? '保存中…' : '保存模板'}
            </button>
          </div>
        </div>

        <div className="grid grid-cols-1 xl:grid-cols-12 gap-5 items-start">
          {/* 左侧：模板与槽位编排表单 */}
          <form id={`${formId}-editor`} onSubmit={handleSaveTemplate} className="xl:col-span-7 space-y-4">
            <div className="cms-card p-4 space-y-4">
              <div className="grid grid-cols-1 sm:grid-cols-2 gap-4 items-end">
                <div>
                  <label htmlFor={`${formId}-tname`} className="block text-xs font-medium text-[var(--cms-muted-fg)] mb-1">模板名称 *</label>
                  <input
                    id={`${formId}-tname`}
                    type="text"
                    required
                    className="input-field w-full"
                    placeholder="例如：标准双渠道模板 v1"
                    value={tplName}
                    onChange={(e) => setTplName(e.target.value)}
                  />
                </div>
                <div>
                  <label htmlFor={`${formId}-tenv`} className="block text-xs font-medium text-[var(--cms-muted-fg)] mb-1">模板校验环境 *</label>
                  <CustomSelect
                    id={`${formId}-tenv`}
                    width="100%"
                    value={tplEnv}
                    onChange={(v) => {
                      const nextEnv = v === 'sandbox' ? 'sandbox' : 'live';
                      setTplEnv(nextEnv);
                      if (nextEnv !== 'live') setTplIsDefault(false);
                    }}
                    options={[
                      { value: 'live', label: 'Live 正式（推荐：要求商品在 Live 已上架且双渠道就绪）' },
                      { value: 'sandbox', label: 'Sandbox 沙箱测试（要求商品在 Sandbox 已上架）' },
                    ]}
                  />
                </div>
              </div>
              <div className="space-y-1">
                <label className={`flex items-center gap-2 text-sm ${lockDefaultCheckbox || tplEnv !== 'live' ? 'cursor-not-allowed opacity-75' : 'cursor-pointer'}`}>
                  <input
                    type="checkbox"
                    checked={tplEnv === 'live' && tplIsDefault}
                    disabled={lockDefaultCheckbox || tplEnv !== 'live'}
                    onChange={(e) => setTplIsDefault(e.target.checked)}
                  />
                  <span>设为全站默认模板（自然流量与兜底用户生效）</span>
                </label>
                {lockDefaultCheckbox && (
                  <p className="text-xs text-[var(--cms-muted-fg)]">
                    当前为全站唯一默认模板，不能直接取消默认；请先在列表将其他模板「设为默认」。
                  </p>
                )}
                {tplEnv !== 'live' && (
                  <p className="text-xs text-[var(--cms-muted-fg)]">
                    仅 Live 环境模板可设为全站默认模板或用于推广绑定；Sandbox 模板仅用于沙箱预演。
                  </p>
                )}
              </div>
            </div>

            <div className="cms-card p-4 space-y-3">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <div>
                  <h2 className="text-sm font-semibold text-[var(--cms-fg)]">
                    商品槽位编排 ({drafts.length} / 8，需 4–8 个)
                  </h2>
                  <p className="text-xs text-[var(--cms-muted-fg)]">
                    仅可选择在 {tplEnv === 'live' ? 'Live' : 'Sandbox'} 环境已上架且 PayPal + Stripe 双渠道就绪的不重复商品。
                  </p>
                </div>
                <button
                  type="button"
                  className="btn-secondary text-xs inline-flex items-center gap-1"
                  disabled={drafts.length >= 8 || productsLoading}
                  onClick={addSlot}
                >
                  <Plus size={14} aria-hidden="true" />
                  添加槽位
                </button>
              </div>

              {eligibleProducts.length === 0 && !productsLoading && (
                <div role="alert" className="p-3 rounded-xl border border-[var(--cms-warning)] bg-[var(--cms-warning-soft)] text-xs text-[var(--cms-warning)]">
                  当前尚未有在 {tplEnv === 'live' ? 'Live' : 'Sandbox'} 环境下「已上架且双渠道就绪」的商品，请先前往 <Link to={`/payments/products?environment=${tplEnv}`} className="underline font-medium">商品与定价</Link> 完成 PayPal / Stripe 绑定并上架。
                </div>
              )}

              <div className="space-y-3">
                {drafts.map((draft, idx) => {
                  const summary = resolveProductSummary(draft.product_id);
                  const envEligible = draft.product_id <= 0 || eligibleProductMap.has(draft.product_id);
                  const selectOptions = eligibleProducts
                    .filter((p) => p.id === draft.product_id || !selectedProductIds.has(p.id))
                    .map((p) => ({
                      value: String(p.id),
                      label: `[${tplEnv === 'live' ? 'Live 已上架' : 'Sandbox 已上架'} · ${PRODUCT_TYPE_LABELS[p.product_type]}] ${p.name} · ${p.price} (${p.code})`,
                    }));
                  if (draft.product_id > 0 && !eligibleProductMap.has(draft.product_id) && summary) {
                    const sbxOnly = tplEnv === 'live' && sandboxProducts.some((sp) => sp.id === draft.product_id);
                    selectOptions.unshift({
                      value: String(draft.product_id),
                      label: `[${sbxOnly ? '仅 Sandbox' : `${tplEnv} 未就绪`}] ${summary.name} · ${summary.price} (${summary.code})`,
                    });
                  }

                  return (
                    <div key={draft.uid} className="p-3.5 rounded-xl border border-[var(--cms-border)] bg-[var(--cms-bg)] space-y-3">
                      <div className="flex flex-wrap items-center justify-between gap-2">
                        <div className="flex items-center gap-2">
                          <span className="badge badge-gray font-mono-code">槽位 #{idx + 1}</span>
                          {!envEligible && !productsLoading && (
                            <span className="badge badge-red text-[11px]">
                              {tplEnv === 'live' && sandboxProducts.some((sp) => sp.id === draft.product_id) ? '仅 Sandbox 上架' : `${tplEnv} 未就绪`}
                            </span>
                          )}
                          {summary && (
                            <span className="text-xs font-medium text-[var(--cms-fg)] flex items-center gap-1">
                              <Lock size={11} aria-hidden="true" />
                              <span className="tabular-nums">{summary.price}</span>
                              {summary.type === 'single' && (
                                <span className="text-[var(--cms-muted-fg)]">· {summary.coins}{summary.bonus > 0 ? `+${summary.bonus}` : ''} 金币</span>
                              )}
                              {summary.type === 'whole_book' && (
                                <span className="text-[var(--cms-muted-fg)]">· {NOVEL_SCOPE_LABELS[summary.novelScope]} · 无赠币</span>
                              )}
                              {summary.type === 'vip' && (
                                <span className="text-[var(--cms-muted-fg)]">· {summary.vipCycle ? VIP_CYCLE_LABELS[summary.vipCycle] : 'VIP'} · 无赠币</span>
                              )}
                            </span>
                          )}
                        </div>
                        <div className="flex items-center gap-1">
                          <button
                            type="button"
                            className="btn-ghost p-1"
                            aria-label={`上移槽位 ${idx + 1}`}
                            disabled={idx === 0}
                            onClick={() => moveSlot(idx, -1)}
                          >
                            <ArrowUp size={14} aria-hidden="true" />
                          </button>
                          <button
                            type="button"
                            className="btn-ghost p-1"
                            aria-label={`下移槽位 ${idx + 1}`}
                            disabled={idx === drafts.length - 1}
                            onClick={() => moveSlot(idx, 1)}
                          >
                            <ArrowDown size={14} aria-hidden="true" />
                          </button>
                          <button
                            type="button"
                            className="btn-ghost p-1 text-[var(--cms-error)]"
                            aria-label={`删除槽位 ${idx + 1}`}
                            onClick={() => removeSlot(draft.uid)}
                          >
                            <Trash2 size={14} aria-hidden="true" />
                          </button>
                        </div>
                      </div>

                      <div>
                        <label htmlFor={`${formId}-prod-${idx}`} className="block text-xs text-[var(--cms-muted-fg)] mb-1">绑定已上架商品 *</label>
                        <CustomSelect
                          id={`${formId}-prod-${idx}`}
                          width="100%"
                          placeholder="请选择商品…"
                          value={draft.product_id ? String(draft.product_id) : ''}
                          onChange={(val) => updateDraft(draft.uid, { product_id: Number(val) || 0 })}
                          options={selectOptions}
                        />
                      </div>

                      <div className="grid grid-cols-1 sm:grid-cols-3 gap-2.5 items-end">
                        <div>
                          <label htmlFor={`${formId}-alias-${idx}`} className="block text-xs text-[var(--cms-muted-fg)] mb-1">展示别名（非商业，可选）</label>
                          <input
                            id={`${formId}-alias-${idx}`}
                            type="text"
                            maxLength={64}
                            className="input-field w-full text-xs"
                            placeholder={summary?.name || '例如：Starter Pack'}
                            value={draft.display_alias}
                            onChange={(e) => updateDraft(draft.uid, { display_alias: e.target.value })}
                          />
                        </div>
                        <div>
                          <label htmlFor={`${formId}-badge-${idx}`} className="block text-xs text-[var(--cms-muted-fg)] mb-1">推荐 / 热门角标（可选）</label>
                          <input
                            id={`${formId}-badge-${idx}`}
                            type="text"
                            maxLength={32}
                            className="input-field w-full text-xs"
                            placeholder="例如：Popular / Save 45%"
                            value={draft.badge}
                            onChange={(e) => updateDraft(draft.uid, { badge: e.target.value })}
                          />
                        </div>
                        <label className="flex items-center gap-2 text-xs cursor-pointer pb-2">
                          <input
                            type="checkbox"
                            checked={draft.is_highlighted}
                            onChange={(e) => updateDraft(draft.uid, { is_highlighted: e.target.checked })}
                          />
                          <span>默认高亮 / 主推</span>
                        </label>
                      </div>
                    </div>
                  );
                })}
              </div>

              {editorError && <p role="alert" className="text-xs text-[var(--cms-error)] pt-1">{editorError}</p>}
            </div>
          </form>

          {/* 右侧：H5 实时效果预览 */}
          <div className="xl:col-span-5 space-y-3 xl:sticky xl:top-20">
            <div className="cms-card p-4 space-y-3">
              <div className="flex items-center justify-between">
                <h2 className="text-sm font-semibold text-[var(--cms-fg)]">Reader H5 收银台预览</h2>
                <div className="inline-flex rounded-lg border border-[var(--cms-border)] p-0.5 text-xs">
                  <button
                    type="button"
                    className={`px-2 py-1 rounded-md cursor-pointer ${previewTheme === 'light' ? 'bg-[var(--cms-primary)] text-white' : 'text-[var(--cms-muted-fg)]'}`}
                    onClick={() => setPreviewTheme('light')}
                  >
                    浅色
                  </button>
                  <button
                    type="button"
                    className={`px-2 py-1 rounded-md cursor-pointer ${previewTheme === 'dark' ? 'bg-[var(--cms-primary)] text-white' : 'text-[var(--cms-muted-fg)]'}`}
                    onClick={() => setPreviewTheme('dark')}
                  >
                    深色
                  </button>
                </div>
              </div>

              <div className={`${previewTheme === 'light' ? 'h5-preview-light' : 'h5-preview-dark'} p-4 rounded-2xl border border-[var(--cms-border)] space-y-3`}>
                <div className="flex items-center justify-between text-xs opacity-75">
                  <span>{tplName || '未命名模板'}</span>
                  <span>支持 PayPal · Stripe</span>
                </div>
                <div className="grid grid-cols-2 gap-2.5">
                  {drafts.map((d, idx) => {
                    const summary = resolveProductSummary(d.product_id);
                    const active = selectedPreviewIdx === idx || d.is_highlighted;
                    return (
                      <button
                        key={d.uid}
                        type="button"
                        onClick={() => setSelectedPreviewIdx(idx)}
                        className={`relative text-left p-3 rounded-xl border transition-colors cursor-pointer ${
                          active
                            ? 'border-amber-500 ring-1 ring-amber-500/40 bg-amber-500/10'
                            : 'border-current/15 hover:border-current/30'
                        }`}
                      >
                        {d.badge && (
                          <span className="inline-flex items-center gap-0.5 text-[10px] font-semibold px-1.5 py-0.5 rounded-full bg-amber-500 text-white mb-1.5">
                            <Sparkles size={9} aria-hidden="true" />
                            {d.badge}
                          </span>
                        )}
                        <div className="flex items-center gap-1 text-xs font-semibold">
                          {summary?.type === 'vip' && <Crown size={12} className="text-amber-500 shrink-0" aria-hidden="true" />}
                          {summary?.type === 'whole_book' && <BookOpen size={12} className="text-indigo-500 shrink-0" aria-hidden="true" />}
                          <span className="truncate">{d.display_alias || summary?.name || `槽位 #${idx + 1}`}</span>
                        </div>
                        <div className="text-base font-bold tabular-nums mt-1">{summary?.price ?? '—'}</div>
                        {summary?.type === 'single' && (
                          <div className="text-[11px] opacity-75 tabular-nums">
                            {summary.coins} Coins{summary.bonus > 0 ? ` + ${summary.bonus}` : ''}
                          </div>
                        )}
                        {summary?.type === 'vip' && (
                          <div className="text-[11px] opacity-75">{summary.vipCycle ? VIP_CYCLE_LABELS[summary.vipCycle] : 'VIP'}</div>
                        )}
                        {summary?.type === 'whole_book' && <div className="text-[11px] opacity-75">Unlock Whole Book</div>}
                      </button>
                    );
                  })}
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>
    );
  }

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="text-xl font-semibold text-[var(--cms-fg)]">充值模板管理</h1>
          <p className="text-xs text-[var(--cms-muted-fg)] mt-1">
            将已双渠道上架的不可变商品编排为 4–8 个 H5 充值卡位，仅维护模板内的排序、角标与默认高亮等非商业展示配置。
          </p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Link to="/payments/products" className="btn-secondary text-sm">商品与定价</Link>
          <button type="button" className="btn-secondary inline-flex items-center gap-1.5" onClick={() => void loadTemplates()}>
            <RefreshCw size={14} aria-hidden="true" />
            刷新
          </button>
          <button type="button" className="btn-primary inline-flex items-center gap-1.5" onClick={() => navigate('/recharge-templates/new')}>
            <Plus size={15} aria-hidden="true" />
            新建模板
          </button>
        </div>
      </div>

      <div className="flex flex-wrap items-center justify-between gap-3">
        <SearchInput value={search} onChange={(v) => { setSearch(v); setPage(1); }} placeholder="搜索模板名称或 ID…" ariaLabel="搜索充值模板" />
      </div>

      {loadError && (
        <div role="alert" className="p-3 rounded-xl border border-[var(--cms-error)] bg-[var(--cms-error-soft)] text-sm text-[var(--cms-error)]">
          {loadError}
        </div>
      )}

      <TableRegion
        ariaLabel="充值模板列表"
        footer={
          filteredTemplates.length > 0 ? (
            <Pagination
              page={page}
              pageSize={pageSize}
              total={filteredTemplates.length}
              onPageChange={setPage}
              onPageSizeChange={(next) => {
                setPageSize(next);
                setPage(1);
              }}
              itemLabel="个模板"
            />
          ) : undefined
        }
      >
        <table className="cms-table">
          <thead>
            <tr>
              <th scope="col">ID</th>
              <th scope="col">模板名称</th>
              <th scope="col">槽位构成（4–8 个）</th>
              <th scope="col">售价区间</th>
              <th scope="col">更新时间</th>
              <th scope="col" className="text-right">操作</th>
            </tr>
          </thead>
          <tbody>
            {!templates && !loadError ? (
              <SkeletonRows rows={3} cols={6} />
            ) : pagedTemplates.length === 0 ? (
              <tr>
                <td colSpan={6} className="text-center py-8 text-sm text-[var(--cms-muted-fg)]">暂无充值模板</td>
              </tr>
            ) : (
              pagedTemplates.map((t) => {
                const { summary, priceRange } = summarizeSlots(t.slots || []);
                const slotCount = t.slots?.length || 0;
                const promoCount = t.linked_promotion_count ?? 0;
                const hasPromoRefs = !canDeleteTemplate(t) && !t.is_default;
                return (
                  <tr key={t.id}>
                    <td className="font-mono-code text-xs text-[var(--cms-muted-fg)]">#{t.id}</td>
                    <td>
                      <div className="flex items-center gap-2 font-medium text-[var(--cms-fg)]">
                        <span>{t.name}</span>
                        <span className="badge badge-gray text-[11px]">{t.environment === 'sandbox' ? 'Sandbox' : 'Live'}</span>
                        {t.is_default && (
                          <span className="badge badge-green inline-flex items-center gap-1">
                            <Check size={11} aria-hidden="true" />
                            全站默认
                          </span>
                        )}
                        {hasPromoRefs && (
                          <span className="badge badge-gray text-[11px]" title={(t.linked_promotion_names ?? []).join('、')}>
                            已绑定 {promoCount} 条推广
                          </span>
                        )}
                      </div>
                    </td>
                    <td>
                      <div className="text-xs text-[var(--cms-fg)]">{summary}</div>
                      <div className="mt-0.5">
                        <StatusDot
                          tone={slotCount >= 4 && slotCount <= 8 ? 'success' : 'warning'}
                          label={`${slotCount} 个槽位`}
                        />
                      </div>
                    </td>
                    <td className="tabular-nums text-xs font-medium">{priceRange}</td>
                    <td className="text-xs text-[var(--cms-muted-fg)] tabular-nums">
                      {new Date(t.updated_at).toLocaleString()}
                    </td>
                    <td className="text-right">
                      <div className="inline-flex flex-wrap justify-end gap-1.5">
                        <button type="button" className="btn-detail" onClick={() => navigate(`/recharge-templates/${t.id}/edit`)}>
                          编辑
                        </button>
                        <button
                          type="button"
                          className="btn-detail inline-flex items-center gap-1"
                          onClick={() => navigate(`/recharge-templates/new?copyFrom=${t.id}`)}
                        >
                          <Copy size={12} aria-hidden="true" />
                          复制
                        </button>
                        {canSetDefaultTemplate(t) && (
                          <button
                            type="button"
                            className="btn-detail inline-flex items-center gap-1"
                            onClick={() => void handleSetDefault(t)}
                          >
                            <Star size={12} aria-hidden="true" />
                            设为默认
                          </button>
                        )}
                        {!t.is_default && (
                          <button
                            type="button"
                            className="btn-ghost text-xs text-[var(--cms-error)] disabled:opacity-45 disabled:cursor-not-allowed"
                            disabled={hasPromoRefs}
                            title={hasPromoRefs ? `已被 ${promoCount} 条推广链接引用，请先解绑后再删除` : undefined}
                            onClick={() => void handleDelete(t)}
                          >
                            {hasPromoRefs ? `引用中 (${promoCount})` : '删除'}
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
    </div>
  );
}
