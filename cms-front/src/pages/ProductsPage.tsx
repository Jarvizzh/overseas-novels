import { useCallback, useContext, useEffect, useId, useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import { CheckCircle2, Copy, Layers, Link2, Lock, PauseCircle, Plus, RefreshCw, Rocket, XCircle } from 'lucide-react';
import CustomSelect from '../components/CustomSelect';
import { CmsDialog } from '../components/ui/CmsDialog';
import { SearchInput, SkeletonRows, StatusDot, TableRegion } from '../components/ui/Primitives';
import { ToastContext } from '../context/ToastContext';
import { operationsApi, type RecommendationNovel } from '../utils/api';
import {
  type ChannelAccount,
  channelsApi,
  NOVEL_SCOPE_LABELS,
  type NovelScopeType,
  type PaymentEnvironment,
  PRODUCT_TYPE_LABELS,
  productsApi,
  type ProductType,
  type ProductView,
  PUBLICATION_STATUS_META,
  type PublicationStatus,
  type StripeSalesMode,
  VIP_CYCLE_LABELS,
  type VipCycle,
} from '../utils/paymentCatalogApi';

interface ProductsPageProps {
  canWrite: boolean;
}

type ProductFormState = {
  code: string;
  product_type: ProductType;
  name: string;
  description: string;
  price_usd: string;
  coins: string;
  bonus: string;
  vip_cycle: VipCycle;
  novel_scope_type: NovelScopeType;
  novel_scope_ids: number[];
  manual_novel_ids: string;
};

const INITIAL_PRODUCT_FORM: ProductFormState = {
  code: '',
  product_type: 'single',
  name: '',
  description: '',
  price_usd: '9.99',
  coins: '1000',
  bonus: '0',
  vip_cycle: 'month',
  novel_scope_type: 'all_published',
  novel_scope_ids: [],
  manual_novel_ids: '',
};

type PayPalBindForm = {
  account_id: string;
  external_product_ref: string;
  external_price_ref: string;
  enabled: boolean;
  reason: string;
};

type StripeBindForm = {
  account_id: string;
  product_ref: string;
  price_ref: string;
  sales_mode: StripeSalesMode;
  tax_liability_account: string;
  countries: string;
  release_evidence: string;
  market_policy: string;
  enabled: boolean;
  reason: string;
};

function errMsg(err: unknown, fallback: string): string {
  return err instanceof Error ? err.message : fallback;
}

export default function ProductsPage({ canWrite }: ProductsPageProps) {
  const toastCtx = useContext(ToastContext);
  const showToast = useCallback(
    (msg: string, type: 'success' | 'error' | 'info' = 'success') => {
      if (toastCtx?.showToast) toastCtx.showToast(msg, type);
      else window.showToast?.(msg, type);
    },
    [toastCtx],
  );
  const [env, setEnv] = useState<PaymentEnvironment>('sandbox');
  const [typeFilter, setTypeFilter] = useState<'all' | ProductType>('all');
  const [statusFilter, setStatusFilter] = useState<'all' | PublicationStatus>('all');
  const [searchQuery, setSearchQuery] = useState('');
  const [readyOnly, setReadyOnly] = useState(false);

  const [products, setProducts] = useState<ProductView[] | null>(null);
  const [accounts, setAccounts] = useState<ChannelAccount[]>([]);
  const [loadError, setLoadError] = useState('');

  const [drawerOpen, setDrawerOpen] = useState(false);
  const [copySource, setCopySource] = useState<ProductView | null>(null);
  const [formState, setFormState] = useState<ProductFormState>(INITIAL_PRODUCT_FORM);
  const [novelOptions, setNovelOptions] = useState<RecommendationNovel[]>([]);
  const [novelSearch, setNovelSearch] = useState('');

  const [bindingProduct, setBindingProduct] = useState<ProductView | null>(null);
  const [ppForm, setPpForm] = useState<PayPalBindForm>({ account_id: '', external_product_ref: '', external_price_ref: '', enabled: true, reason: '' });
  const [stForm, setStForm] = useState<StripeBindForm>({
    account_id: '', product_ref: '', price_ref: '', sales_mode: 'direct',
    tax_liability_account: '', countries: 'US', release_evidence: '', market_policy: '', enabled: true, reason: '',
  });

  const [statusDialog, setStatusDialog] = useState<{ product: ProductView; action: 'publish' | 'pause' | 'retire' } | null>(null);
  const [statusReason, setStatusReason] = useState('');

  const [submitting, setSubmitting] = useState(false);
  const [formError, setFormError] = useState('');
  const formId = useId();

  const loadData = useCallback(async (signal?: AbortSignal) => {
    setLoadError('');
    try {
      const [prodRes, chRes] = await Promise.all([
        productsApi.list(
          {
            environment: env,
            type: typeFilter === 'all' ? undefined : typeFilter,
            status: statusFilter === 'all' ? undefined : statusFilter,
            q: searchQuery.trim() || undefined,
            ready_only: readyOnly,
          },
          signal,
        ),
        channelsApi.list(signal).catch(() => ({ channels: [] as ChannelAccount[] })),
      ]);
      if (!signal?.aborted) {
        setProducts(prodRes.products ?? []);
        setAccounts(chRes.channels ?? []);
      }
    } catch (err) {
      if (!signal?.aborted) setLoadError(errMsg(err, '加载商品列表失败'));
    }
  }, [env, typeFilter, statusFilter, searchQuery, readyOnly]);

  useEffect(() => {
    const controller = new AbortController();
    void loadData(controller.signal);
    return () => controller.abort();
  }, [loadData]);

  useEffect(() => {
    if (!drawerOpen || formState.product_type !== 'whole_book' || formState.novel_scope_type !== 'specific_novels') return;
    const controller = new AbortController();
    void operationsApi
      .searchPublishedNovels({ search: novelSearch, pageSize: 20, signal: controller.signal })
      .then((res) => {
        if (!controller.signal.aborted) {
          setNovelOptions(Array.isArray(res?.novels) ? res.novels : Array.isArray(res?.items) ? res.items : []);
        }
      })
      .catch(() => {});
    return () => controller.abort();
  }, [drawerOpen, formState.product_type, formState.novel_scope_type, novelSearch]);

  const envAccounts = useMemo(() => {
    const pp = accounts.filter((a) => a.provider === 'paypal' && a.environment === env && a.status !== 'disabled');
    const st = accounts.filter((a) => a.provider === 'stripe' && a.environment === env && a.status !== 'disabled');
    return { paypal: pp, stripe: st };
  }, [accounts, env]);

  const openCreateDrawer = () => {
    setCopySource(null);
    setFormState(INITIAL_PRODUCT_FORM);
    setFormError('');
    setDrawerOpen(true);
  };

  const openCopyDrawer = (prod: ProductView) => {
    setCopySource(prod);
    setFormState({
      code: `${prod.code}_V2`.slice(0, 64),
      product_type: prod.product_type,
      name: `${prod.name} (新版本)`,
      description: prod.description || '',
      price_usd: (prod.price_cents / 100).toFixed(2),
      coins: String(prod.coins || 0),
      bonus: String(prod.bonus || 0),
      vip_cycle: (prod.vip_cycle || 'month') as VipCycle,
      novel_scope_type: prod.novel_scope_type === 'none' ? 'all_published' : prod.novel_scope_type,
      novel_scope_ids: prod.novel_scope_ids ?? [],
      manual_novel_ids: (prod.novel_scope_ids ?? []).join(', '),
    });
    setFormError('');
    setDrawerOpen(true);
  };

  const parseSpecificNovelIds = (): number[] => {
    const manual = formState.manual_novel_ids
      .split(/[\s,，]+/)
      .map((item) => Number(item))
      .filter((n) => Number.isInteger(n) && n > 0);
    return Array.from(new Set([...formState.novel_scope_ids, ...manual])).sort((a, b) => a - b);
  };

  const handleSaveProduct = async (e: React.FormEvent) => {
    e.preventDefault();
    if (submitting) return;
    setFormError('');

    const priceCents = Math.round(Number(formState.price_usd) * 100);
    if (!Number.isFinite(priceCents) || priceCents <= 0) {
      setFormError('请输入大于 0 的有效美元价格（如 9.99）');
      return;
    }

    const payload = {
      code: formState.code.trim().toUpperCase() || undefined,
      product_type: formState.product_type,
      name: formState.name.trim(),
      description: formState.description.trim(),
      price_cents: priceCents,
      coins: formState.product_type === 'single' ? Math.max(0, Math.round(Number(formState.coins) || 0)) : 0,
      bonus: formState.product_type === 'single' ? Math.max(0, Math.round(Number(formState.bonus) || 0)) : 0,
      vip_cycle: formState.product_type === 'vip' ? formState.vip_cycle : '',
      novel_scope_type: formState.product_type === 'whole_book' ? formState.novel_scope_type : 'none',
      novel_scope_ids:
        formState.product_type === 'whole_book' && formState.novel_scope_type === 'specific_novels'
          ? parseSpecificNovelIds()
          : [],
    } as const;

    setSubmitting(true);
    try {
      if (copySource) {
        await productsApi.copy(copySource.id, payload);
        showToast('已复制并创建不可变新商品，请为其绑定 PayPal 与 Stripe 渠道', 'success');
      } else {
        await productsApi.create(payload);
        showToast('商品创建成功，请继续配置 PayPal 与 Stripe 双渠道绑定', 'success');
      }
      setDrawerOpen(false);
      await loadData();
    } catch (err) {
      setFormError(errMsg(err, '创建商品失败'));
    } finally {
      setSubmitting(false);
    }
  };

  const syncBindingFormsFromProduct = (prod: ProductView) => {
    const ppAccDefault = envAccounts.paypal.find((a) => a.is_primary) ?? envAccounts.paypal[0];
    const stAccDefault = envAccounts.stripe.find((a) => a.is_primary) ?? envAccounts.stripe[0];
    setPpForm({
      account_id: String(prod.paypal_binding?.account_id ?? ppAccDefault?.id ?? ''),
      external_product_ref: prod.paypal_binding?.external_product_ref ?? '',
      external_price_ref: prod.paypal_binding?.external_price_ref ?? '',
      enabled: prod.paypal_binding?.enabled ?? true,
      reason: '',
    });
    setStForm({
      account_id: String(prod.stripe_binding?.account_id ?? stAccDefault?.id ?? ''),
      product_ref: prod.stripe_binding?.external_product_ref ?? '',
      price_ref: prod.stripe_binding?.external_price_ref ?? '',
      sales_mode: prod.stripe_binding?.sales_mode ?? 'direct',
      tax_liability_account: prod.stripe_binding?.tax_liability_account ?? '',
      countries: prod.stripe_binding?.allowed_countries?.join(', ') || 'US',
      release_evidence: prod.stripe_binding?.release_evidence ?? '',
      market_policy: prod.stripe_binding?.market_policy ?? '',
      enabled: prod.stripe_binding?.enabled ?? true,
      reason: '',
    });
  };

  const openBindingsDrawer = async (prod: ProductView) => {
    setFormError('');
    setBindingProduct(prod);
    syncBindingFormsFromProduct(prod);
    try {
      const fresh = await productsApi.get(prod.id, env);
      setBindingProduct(fresh.product);
      syncBindingFormsFromProduct(fresh.product);
    } catch {
      // keep snapshot
    }
  };

  const handleSavePayPalBinding = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!bindingProduct || submitting) return;
    setFormError('');
    setSubmitting(true);
    try {
      await productsApi.bindPayPal(bindingProduct.id, {
        environment: env,
        account_id: ppForm.account_id ? Number(ppForm.account_id) : undefined,
        external_product_ref: ppForm.external_product_ref.trim(),
        external_price_ref: ppForm.external_price_ref.trim(),
        enabled: ppForm.enabled,
        reason: ppForm.reason.trim(),
      });
      const updated = await productsApi.get(bindingProduct.id, env);
      setBindingProduct(updated.product);
      syncBindingFormsFromProduct(updated.product);
      showToast('PayPal 渠道绑定已验证并保存', 'success');
      await loadData();
    } catch (err) {
      setFormError(errMsg(err, 'PayPal 渠道绑定失败'));
    } finally {
      setSubmitting(false);
    }
  };

  const handleSaveStripeBinding = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!bindingProduct || submitting) return;
    setFormError('');
    const countries = stForm.countries
      .split(/[\s,，]+/)
      .map((c) => c.trim().toUpperCase())
      .filter(Boolean);
    if (countries.length === 0 || countries.some((c) => !/^[A-Z]{2}$/.test(c))) {
      setFormError('请输入有效的 2 位 ISO 国家代码（如 US, CA）');
      return;
    }
    if (!stForm.reason.trim()) {
      setFormError('请填写 Stripe 渠道绑定变更原因');
      return;
    }
    setSubmitting(true);
    try {
      await productsApi.bindStripe(bindingProduct.id, {
        environment: env,
        account_id: stForm.account_id ? Number(stForm.account_id) : undefined,
        product_ref: stForm.product_ref.trim(),
        price_ref: stForm.price_ref.trim(),
        sales_mode: stForm.sales_mode,
        tax_liability_account: stForm.sales_mode === 'merchant_of_record' ? stForm.tax_liability_account.trim() : '',
        countries,
        release_evidence: stForm.release_evidence.trim(),
        market_policy: stForm.market_policy.trim(),
        enabled: stForm.enabled,
        reason: stForm.reason.trim(),
      });
      const updated = await productsApi.get(bindingProduct.id, env);
      setBindingProduct(updated.product);
      syncBindingFormsFromProduct(updated.product);
      showToast('Stripe 渠道绑定已验证并保存', 'success');
      await loadData();
    } catch (err) {
      setFormError(errMsg(err, 'Stripe 渠道绑定失败'));
    } finally {
      setSubmitting(false);
    }
  };

  const handleTransitionStatus = async () => {
    if (!statusDialog || submitting) return;
    setSubmitting(true);
    setFormError('');
    try {
      const { product, action } = statusDialog;
      if (action === 'publish') await productsApi.publish(product.id, env, statusReason.trim());
      else if (action === 'pause') await productsApi.pause(product.id, env, statusReason.trim());
      else await productsApi.retire(product.id, env, statusReason.trim());

      showToast(
        action === 'publish' ? '商品已上架，可被模板引用' : action === 'pause' ? '商品已暂停销售' : '商品已下线（保留历史订单引用）',
        'success',
      );
      setStatusDialog(null);
      setStatusReason('');
      if (bindingProduct?.id === product.id) {
        const refreshed = await productsApi.get(product.id, env);
        setBindingProduct(refreshed.product);
      }
      await loadData();
    } catch (err) {
      setFormError(errMsg(err, '更新商品发布状态失败'));
    } finally {
      setSubmitting(false);
    }
  };

  const toggleNovelId = (novelId: number) => {
    setFormState((prev) => ({
      ...prev,
      novel_scope_ids: prev.novel_scope_ids.includes(novelId)
        ? prev.novel_scope_ids.filter((id) => id !== novelId)
        : [...prev.novel_scope_ids, novelId],
    }));
  };

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="text-xl font-semibold text-[var(--cms-fg)]">商品与定价（不可变商品目录）</h1>
          <p className="text-xs text-[var(--cms-muted-fg)] mt-1">
            商业规格（价格、金币/赠币、VIP 周期、整本范围）创建后不可变；商品必须在选定环境下同时完成 PayPal 与 Stripe 双渠道验证后才可上架销售。
          </p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Link to="/recharge-templates" className="btn-secondary inline-flex items-center gap-1.5 text-sm">
            <Layers size={14} aria-hidden="true" />
            前往充值模板编排
          </Link>
          <button type="button" className="btn-secondary inline-flex items-center gap-1.5" onClick={() => void loadData()}>
            <RefreshCw size={14} aria-hidden="true" />
            刷新
          </button>
          {canWrite && (
            <button type="button" className="btn-primary inline-flex items-center gap-1.5" onClick={openCreateDrawer}>
              <Plus size={15} aria-hidden="true" />
              新建商品
            </button>
          )}
        </div>
      </div>

      {/* 环境 + 类型 + 状态 + 搜索 */}
      <div className="cms-card p-3 space-y-3">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="inline-flex rounded-xl border border-[var(--cms-border)] p-0.5 bg-[var(--cms-bg)]" role="group" aria-label="环境切换">
            {(['sandbox', 'live'] as const).map((e) => (
              <button
                key={e}
                type="button"
                onClick={() => setEnv(e)}
                className={`px-3 py-1.5 rounded-lg text-xs font-medium transition-colors cursor-pointer ${
                  env === e
                    ? e === 'live'
                      ? 'bg-[var(--cms-error)] text-white'
                      : 'bg-[var(--cms-primary)] text-white'
                    : 'text-[var(--cms-muted-fg)] hover:text-[var(--cms-fg)]'
                }`}
              >
                {e === 'sandbox' ? 'Sandbox 沙箱环境' : 'Live 正式生产环境'}
              </button>
            ))}
          </div>

          <div className="flex flex-wrap items-center gap-2.5">
            <SearchInput
              value={searchQuery}
              onChange={setSearchQuery}
              placeholder="搜索商品编码 / 名称…"
              ariaLabel="搜索商品"
            />
            <CustomSelect
              value={statusFilter}
              onChange={(v) => setStatusFilter(v as 'all' | PublicationStatus)}
              width="140px"
              aria-label="按发布状态筛选"
              options={[
                { value: 'all', label: '全部状态' },
                { value: 'draft', label: '草稿' },
                { value: 'published', label: '已上架' },
                { value: 'paused', label: '已暂停' },
                { value: 'retired', label: '已下线' },
              ]}
            />
            <label className="inline-flex items-center gap-1.5 text-xs text-[var(--cms-muted-fg)] cursor-pointer select-none">
              <input type="checkbox" checked={readyOnly} onChange={(e) => setReadyOnly(e.target.checked)} />
              <span>仅看双渠道就绪</span>
            </label>
          </div>
        </div>

        <div className="flex flex-wrap items-center gap-1.5" role="group" aria-label="商品类型筛选">
          {([
            { key: 'all', label: '全部类型' },
            { key: 'single', label: '金币充值' },
            { key: 'whole_book', label: '整本购买' },
            { key: 'vip', label: 'VIP 订阅' },
          ] as const).map((item) => (
            <button
              key={item.key}
              type="button"
              aria-pressed={typeFilter === item.key}
              onClick={() => setTypeFilter(item.key)}
              className={`focus-ring px-3 py-1.5 rounded-xl text-xs font-medium transition-colors cursor-pointer ${
                typeFilter === item.key
                  ? 'bg-[var(--cms-primary)] text-[var(--cms-primary-fg)]'
                  : 'bg-[var(--cms-secondary)] text-[var(--cms-muted-fg)] hover:text-[var(--cms-fg)]'
              }`}
            >
              {item.label}
            </button>
          ))}
        </div>
      </div>

      {loadError && (
        <div role="alert" className="p-3 rounded-xl border border-[var(--cms-error)] bg-[var(--cms-error-soft)] text-sm text-[var(--cms-error)]">
          {loadError}
        </div>
      )}

      <TableRegion ariaLabel="商品与双渠道定价列表">
        <table className="cms-table">
          <thead>
            <tr>
              <th scope="col">商品编码与名称</th>
              <th scope="col">商品类型</th>
              <th scope="col">商业规格（不可变）</th>
              <th scope="col">PayPal / Stripe 绑定状态 ({env})</th>
              <th scope="col">环境上架状态与模板引用</th>
              <th scope="col" className="text-right">操作</th>
            </tr>
          </thead>
          <tbody>
            {!products && !loadError ? (
              <SkeletonRows rows={4} cols={6} />
            ) : products?.length === 0 ? (
              <tr>
                <td colSpan={6} className="text-center py-8 text-sm text-[var(--cms-muted-fg)]">
                  暂无符合条件的商品
                </td>
              </tr>
            ) : (
              products?.map((p) => {
                const pub = p.publications?.[env];
                const pubStatus: PublicationStatus = pub?.status ?? 'draft';
                const pubMeta = PUBLICATION_STATUS_META[pubStatus];
                return (
                  <tr key={p.id}>
                    <td>
                      <div className="flex items-center gap-1.5 font-medium text-[var(--cms-fg)]">
                        <span>{p.name}</span>
                        {p.source_product_id && (
                          <span className="badge badge-gray text-[10px]">复制自 #{p.source_product_id}</span>
                        )}
                      </div>
                      <div className="text-xs font-mono-code text-[var(--cms-muted-fg)] mt-0.5 flex items-center gap-1">
                        <Lock size={11} aria-hidden="true" />
                        <span>{p.code}</span>
                      </div>
                      {p.description && (
                        <div className="text-xs text-[var(--cms-muted-fg)] mt-0.5 line-clamp-1">{p.description}</div>
                      )}
                    </td>
                    <td>
                      <span
                        className={`badge ${
                          p.product_type === 'vip' ? 'badge-purple' : p.product_type === 'whole_book' ? 'badge-blue' : 'badge-amber'
                        }`}
                      >
                        {PRODUCT_TYPE_LABELS[p.product_type]}
                      </span>
                    </td>
                    <td>
                      <div className="font-semibold tabular-nums text-[var(--cms-fg)]">{p.price} USD</div>
                      {p.product_type === 'single' && (
                        <div className="text-xs text-[var(--cms-muted-fg)] mt-0.5 tabular-nums">
                          {p.coins} 金币{p.bonus > 0 ? ` + ${p.bonus} 赠币` : ''}
                        </div>
                      )}
                      {p.product_type === 'whole_book' && (
                        <div className="text-xs text-[var(--cms-muted-fg)] mt-0.5">
                          {NOVEL_SCOPE_LABELS[p.novel_scope_type]}
                          {p.novel_scope_type === 'specific_novels' ? ` (${p.novel_scope_ids.join(', ')})` : ''} · 无赠币
                        </div>
                      )}
                      {p.product_type === 'vip' && (
                        <div className="text-xs text-[var(--cms-muted-fg)] mt-0.5">
                          {p.vip_cycle ? VIP_CYCLE_LABELS[p.vip_cycle] : '—'} · 自动续订 · 无赠币
                        </div>
                      )}
                    </td>
                    <td>
                      <div className="space-y-1 text-xs">
                        <div className="flex items-center gap-2">
                          <StatusDot tone={p.paypal_ready ? 'success' : 'warning'} label={`PayPal: ${p.paypal_ready ? '就绪' : '待完善'}`} />
                          {p.paypal_binding?.external_price_ref && (
                            <code className="text-[11px] font-mono-code text-[var(--cms-muted-fg)]">{p.paypal_binding.external_price_ref}</code>
                          )}
                        </div>
                        <div className="flex items-center gap-2">
                          <StatusDot tone={p.stripe_ready ? 'success' : 'warning'} label={`Stripe: ${p.stripe_ready ? '就绪' : '待完善'}`} />
                          {p.stripe_binding?.external_price_ref && (
                            <code className="text-[11px] font-mono-code text-[var(--cms-muted-fg)]">{p.stripe_binding.external_price_ref}</code>
                          )}
                        </div>
                        {!p.dual_channel_ready && p.readiness_reasons.length > 0 && (
                          <div className="text-[11px] text-[var(--cms-warning)]">{p.readiness_reasons[0]}</div>
                        )}
                      </div>
                    </td>
                    <td>
                      <StatusDot tone={pubMeta.tone} label={pubMeta.label} />
                      <div className="text-xs text-[var(--cms-muted-fg)] mt-1">
                        {p.linked_template_names.length > 0 ? (
                          <span>关联模板: {p.linked_template_names.join('、')}</span>
                        ) : (
                          <span>尚未被模板引用</span>
                        )}
                      </div>
                    </td>
                    <td className="text-right">
                      <div className="inline-flex flex-wrap justify-end gap-1.5">
                        <button type="button" className="btn-detail inline-flex items-center gap-1" onClick={() => void openBindingsDrawer(p)}>
                          <Link2 size={12} aria-hidden="true" />
                          {canWrite ? '绑定配置' : '查看绑定'}
                        </button>
                        {canWrite && pubStatus !== 'retired' && pubStatus !== 'published' && (
                          <button
                            type="button"
                            className="btn-primary text-xs px-2.5 py-1.5 inline-flex items-center gap-1"
                            onClick={() => {
                              setStatusReason('');
                              setFormError('');
                              setStatusDialog({ product: p, action: 'publish' });
                            }}
                          >
                            <Rocket size={12} aria-hidden="true" />
                            上架
                          </button>
                        )}
                        {canWrite && pubStatus === 'published' && (
                          <button
                            type="button"
                            className="btn-detail inline-flex items-center gap-1"
                            onClick={() => {
                              setStatusReason('');
                              setFormError('');
                              setStatusDialog({ product: p, action: 'pause' });
                            }}
                          >
                            <PauseCircle size={12} aria-hidden="true" />
                            暂停
                          </button>
                        )}
                        {canWrite && (
                          <button type="button" className="btn-detail inline-flex items-center gap-1" onClick={() => openCopyDrawer(p)}>
                            <Copy size={12} aria-hidden="true" />
                            复制调价
                          </button>
                        )}
                        {canWrite && pubStatus !== 'retired' && (
                          <button
                            type="button"
                            className="btn-ghost text-xs text-[var(--cms-error)] inline-flex items-center gap-1"
                            onClick={() => {
                              setStatusReason('');
                              setFormError('');
                              setStatusDialog({ product: p, action: 'retire' });
                            }}
                          >
                            <XCircle size={12} aria-hidden="true" />
                            下线
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

      {/* 新建 / 复制商品抽屉 */}
      <CmsDialog
        open={drawerOpen}
        onClose={() => { if (!submitting) setDrawerOpen(false); }}
        variant="drawer"
        size="md"
        title={copySource ? `基于 ${copySource.code} 复制新建商品` : '新建不可变商业商品'}
        subtitle="商品一经创建，价格与核心权益即永久锁定；如需调整定价，请复制生成新商品"
        closedBy="closerequest"
        closeDisabled={submitting}
        footer={
          <>
            <button type="button" className="btn-secondary" disabled={submitting} onClick={() => setDrawerOpen(false)}>取消</button>
            <button type="submit" form={`${formId}-prod`} className="btn-primary" disabled={submitting}>
              {submitting ? '提交中…' : copySource ? '创建新版本商品' : '创建商品（草稿）'}
            </button>
          </>
        }
      >
        <form id={`${formId}-prod`} onSubmit={handleSaveProduct} className="space-y-4">
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
            <div>
              <label htmlFor={`${formId}-type`} className="block text-xs font-medium text-[var(--cms-muted-fg)] mb-1">商品类型 *</label>
              <CustomSelect
                id={`${formId}-type`}
                width="100%"
                value={formState.product_type}
                onChange={(v) => setFormState((prev) => ({ ...prev, product_type: v as ProductType }))}
                options={[
                  { value: 'single', label: '金币充值 (single)' },
                  { value: 'whole_book', label: '整本购买 (whole_book)' },
                  { value: 'vip', label: 'VIP 订阅 (vip)' },
                ]}
              />
            </div>
            <div>
              <label htmlFor={`${formId}-price`} className="block text-xs font-medium text-[var(--cms-muted-fg)] mb-1">美元售价 (USD) *</label>
              <input
                id={`${formId}-price`}
                type="number"
                step="0.01"
                min="0.01"
                required
                data-dialog-initial-focus
                className="input-field w-full tabular-nums"
                value={formState.price_usd}
                onChange={(e) => setFormState((prev) => ({ ...prev, price_usd: e.target.value }))}
              />
            </div>
          </div>

          <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
            <div>
              <label htmlFor={`${formId}-code`} className="block text-xs font-medium text-[var(--cms-muted-fg)] mb-1">唯一商品编码（可选，A-Z0-9_）</label>
              <input
                id={`${formId}-code`}
                type="text"
                className="input-field w-full font-mono-code uppercase"
                placeholder="留空自动生成"
                value={formState.code}
                onChange={(e) => setFormState((prev) => ({ ...prev, code: e.target.value.toUpperCase() }))}
              />
            </div>
            <div>
              <label htmlFor={`${formId}-pname`} className="block text-xs font-medium text-[var(--cms-muted-fg)] mb-1">商品标准名称 *</label>
              <input
                id={`${formId}-pname`}
                type="text"
                required
                className="input-field w-full"
                placeholder="例如：VIP 年度会员"
                value={formState.name}
                onChange={(e) => setFormState((prev) => ({ ...prev, name: e.target.value }))}
              />
            </div>
          </div>

          <div>
            <label htmlFor={`${formId}-desc`} className="block text-xs font-medium text-[var(--cms-muted-fg)] mb-1">说明文案</label>
            <textarea
              id={`${formId}-desc`}
              rows={2}
              className="input-field w-full"
              value={formState.description}
              onChange={(e) => setFormState((prev) => ({ ...prev, description: e.target.value }))}
            />
          </div>

          {formState.product_type === 'single' && (
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-3 p-3 rounded-xl border border-[var(--cms-border)] bg-[var(--cms-bg)]">
              <div>
                <label htmlFor={`${formId}-coins`} className="block text-xs font-medium text-[var(--cms-muted-fg)] mb-1">基础金币（Charged Coins）*</label>
                <input
                  id={`${formId}-coins`}
                  type="number"
                  min={1}
                  required
                  className="input-field w-full tabular-nums"
                  value={formState.coins}
                  onChange={(e) => setFormState((prev) => ({ ...prev, coins: e.target.value }))}
                />
              </div>
              <div>
                <label htmlFor={`${formId}-bonus`} className="block text-xs font-medium text-[var(--cms-muted-fg)] mb-1">
                  赠送金币（上限 {Math.floor((Number(formState.coins) || 0) * 1.5)}）
                </label>
                <input
                  id={`${formId}-bonus`}
                  type="number"
                  min={0}
                  max={Math.floor((Number(formState.coins) || 0) * 1.5)}
                  className="input-field w-full tabular-nums"
                  value={formState.bonus}
                  onChange={(e) => setFormState((prev) => ({ ...prev, bonus: e.target.value }))}
                />
              </div>
            </div>
          )}

          {formState.product_type === 'whole_book' && (
            <div className="space-y-3 p-3 rounded-xl border border-[var(--cms-border)] bg-[var(--cms-bg)]">
              <div className="text-xs text-[var(--cms-muted-fg)]">整本购买不配置赠币（基础金币=0，赠币=0）。</div>
              <div>
                <label htmlFor={`${formId}-scope`} className="block text-xs font-medium text-[var(--cms-muted-fg)] mb-1">整本适用范围 *</label>
                <CustomSelect
                  id={`${formId}-scope`}
                  width="100%"
                  value={formState.novel_scope_type}
                  onChange={(v) => setFormState((prev) => ({ ...prev, novel_scope_type: v as NovelScopeType }))}
                  options={[
                    { value: 'all_published', label: '全站已上架小说（默认适用）' },
                    { value: 'promoted_novel', label: '仅推广落地页关联小说' },
                    { value: 'specific_novels', label: '指定小说 ID 集合' },
                  ]}
                />
              </div>
              {formState.novel_scope_type === 'specific_novels' && (
                <div className="space-y-2">
                  <label htmlFor={`${formId}-manualids`} className="block text-xs font-medium text-[var(--cms-muted-fg)]">
                    指定小说 ID（逗号分隔）或下方勾选
                  </label>
                  <input
                    id={`${formId}-manualids`}
                    type="text"
                    className="input-field w-full font-mono-code"
                    placeholder="例如：101, 102"
                    value={formState.manual_novel_ids}
                    onChange={(e) => setFormState((prev) => ({ ...prev, manual_novel_ids: e.target.value }))}
                  />
                  <SearchInput
                    value={novelSearch}
                    onChange={setNovelSearch}
                    placeholder="按书名筛选已上架小说…"
                    ariaLabel="筛选已上架小说"
                  />
                  <div className="max-h-40 overflow-y-auto space-y-1 border border-[var(--cms-border)] rounded-lg p-2 bg-[var(--cms-card)]">
                    {novelOptions.map((n) => (
                      <label key={n.id} className="flex items-center gap-2 text-xs py-1 cursor-pointer">
                        <input
                          type="checkbox"
                          checked={formState.novel_scope_ids.includes(n.id)}
                          onChange={() => toggleNovelId(n.id)}
                        />
                        <span className="font-mono-code text-[var(--cms-muted-fg)]">#{n.id}</span>
                        <span className="truncate">{n.title}</span>
                      </label>
                    ))}
                    {novelOptions.length === 0 && (
                      <div className="text-xs text-[var(--cms-muted-fg)] py-1">未检索到匹配上架小说</div>
                    )}
                  </div>
                </div>
              )}
            </div>
          )}

          {formState.product_type === 'vip' && (
            <div className="space-y-3 p-3 rounded-xl border border-[var(--cms-border)] bg-[var(--cms-bg)]">
              <div className="text-xs text-[var(--cms-muted-fg)]">VIP 为周期性连续订阅，不配置赠币（基础金币=0，赠币=0）。</div>
              <div>
                <label htmlFor={`${formId}-cycle`} className="block text-xs font-medium text-[var(--cms-muted-fg)] mb-1">订阅周期 *</label>
                <CustomSelect
                  id={`${formId}-cycle`}
                  width="100%"
                  value={formState.vip_cycle}
                  onChange={(v) => setFormState((prev) => ({ ...prev, vip_cycle: v as VipCycle }))}
                  options={[
                    { value: 'day', label: '日卡 · 每日自动续费' },
                    { value: 'week', label: '周卡 · 每周自动续费' },
                    { value: 'month', label: '月卡 · 每月自动续费' },
                    { value: 'year', label: '年卡 · 每年自动续费' },
                  ]}
                />
              </div>
            </div>
          )}

          {formError && <p role="alert" className="text-xs text-[var(--cms-error)]">{formError}</p>}
        </form>
      </CmsDialog>

      {/* 双渠道绑定抽屉 */}
      <CmsDialog
        open={bindingProduct !== null}
        onClose={() => { if (!submitting) setBindingProduct(null); }}
        variant="drawer"
        size="lg"
        title={bindingProduct ? `双渠道绑定配置 · ${bindingProduct.name}` : '双渠道绑定'}
        subtitle={bindingProduct ? `${bindingProduct.code} · ${PRODUCT_TYPE_LABELS[bindingProduct.product_type]} · ${bindingProduct.price} (${env} 环境)` : undefined}
        closedBy="closerequest"
        closeDisabled={submitting}
        footer={
          <button type="button" className="btn-secondary" disabled={submitting} onClick={() => setBindingProduct(null)}>完成并关闭</button>
        }
      >
        {bindingProduct && (
          <div className="space-y-5">
            {/* 双渠道就绪闸门 */}
            <div
              className={`p-3.5 rounded-xl border flex flex-wrap items-center justify-between gap-3 ${
                bindingProduct.dual_channel_ready
                  ? 'border-[var(--cms-success)] bg-[var(--cms-success-soft)]'
                  : 'border-[var(--cms-warning)] bg-[var(--cms-warning-soft)]'
              }`}
            >
              <div className="space-y-1 text-xs">
                <div className="font-semibold text-sm flex items-center gap-1.5 text-[var(--cms-fg)]">
                  {bindingProduct.dual_channel_ready ? (
                    <CheckCircle2 size={16} className="text-[var(--cms-success)]" aria-hidden="true" />
                  ) : (
                    <span className="w-2 h-2 rounded-full bg-[var(--cms-warning)]" aria-hidden="true" />
                  )}
                  <span>{bindingProduct.dual_channel_ready ? `已在 ${env} 环境满足双渠道售卖闸门` : `${env} 环境尚未满足双渠道上架条件`}</span>
                </div>
                {bindingProduct.readiness_reasons.length > 0 && (
                  <ul className="list-disc list-inside text-[var(--cms-muted-fg)]">
                    {bindingProduct.readiness_reasons.map((r) => (
                      <li key={r}>{r}</li>
                    ))}
                  </ul>
                )}
              </div>
              {canWrite && bindingProduct.dual_channel_ready && bindingProduct.publications?.[env]?.status !== 'published' && (
                <button
                  type="button"
                  className="btn-primary text-xs"
                  onClick={() => {
                    setStatusReason('');
                    setFormError('');
                    setStatusDialog({ product: bindingProduct, action: 'publish' });
                  }}
                >
                  立即上架到 {env}
                </button>
              )}
            </div>

            {formError && <p role="alert" className="text-xs text-[var(--cms-error)]">{formError}</p>}

            {/* PayPal 绑定表单 */}
            <form onSubmit={handleSavePayPalBinding} className="cms-card p-4 space-y-3">
              <div className="flex items-center justify-between">
                <h3 className="text-sm font-semibold">PayPal 渠道绑定</h3>
                <StatusDot tone={bindingProduct.paypal_ready ? 'success' : 'warning'} label={bindingProduct.paypal_ready ? '已就绪' : '未就绪'} />
              </div>

              <div>
                <label htmlFor={`${formId}-pp-acc`} className="block text-xs font-medium text-[var(--cms-muted-fg)] mb-1">PayPal 渠道账户</label>
                <CustomSelect
                  id={`${formId}-pp-acc`}
                  width="100%"
                  value={ppForm.account_id}
                  onChange={(v) => setPpForm((prev) => ({ ...prev, account_id: v }))}
                  options={envAccounts.paypal.map((a) => ({
                    value: String(a.id),
                    label: `${a.display_name || a.account_label}${a.is_primary ? ' (主用)' : ''} · ${a.status}`,
                  }))}
                  placeholder="选择 PayPal 账户"
                />
              </div>

              {bindingProduct.product_type === 'vip' ? (
                <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
                  <div>
                    <label htmlFor={`${formId}-pp-prod`} className="block text-xs font-medium text-[var(--cms-muted-fg)] mb-1">PayPal Product ID (PROD-...，可选)</label>
                    <input
                      id={`${formId}-pp-prod`}
                      type="text"
                      className="input-field w-full font-mono-code"
                      placeholder="留空则随 Plan 自动创建"
                      value={ppForm.external_product_ref}
                      onChange={(e) => setPpForm((prev) => ({ ...prev, external_product_ref: e.target.value }))}
                    />
                  </div>
                  <div>
                    <label htmlFor={`${formId}-pp-plan`} className="block text-xs font-medium text-[var(--cms-muted-fg)] mb-1">PayPal Billing Plan ID (P-...，留空自动创建)</label>
                    <input
                      id={`${formId}-pp-plan`}
                      type="text"
                      className="input-field w-full font-mono-code"
                      placeholder="留空将实时调用 PayPal 自动创建并校验周期"
                      value={ppForm.external_price_ref}
                      onChange={(e) => setPpForm((prev) => ({ ...prev, external_price_ref: e.target.value }))}
                    />
                  </div>
                </div>
              ) : (
                <p className="text-xs text-[var(--cms-muted-fg)]">
                  单次金币充值与整本购买使用动态 Orders v2 创建，无需预填 Plan ID，保存时系统将校验当前主用 PayPal 商户账户的 OAuth 与 Webhook 状态。
                </p>
              )}

              <div className="flex flex-wrap items-center justify-between gap-3 pt-1">
                <label className="inline-flex items-center gap-2 text-xs cursor-pointer">
                  <input type="checkbox" checked={ppForm.enabled} onChange={(e) => setPpForm((prev) => ({ ...prev, enabled: e.target.checked }))} />
                  <span>启用该商品在 PayPal 的下单能力</span>
                </label>
                {canWrite && (
                  <button type="submit" className="btn-primary text-xs" disabled={submitting}>
                    {submitting ? '校验并保存中…' : '校验并保存 PayPal 绑定'}
                  </button>
                )}
              </div>
            </form>

            {/* Stripe 绑定表单 */}
            <form onSubmit={handleSaveStripeBinding} className="cms-card p-4 space-y-3">
              <div className="flex items-center justify-between">
                <h3 className="text-sm font-semibold">Stripe 渠道绑定</h3>
                <StatusDot tone={bindingProduct.stripe_ready ? 'success' : 'warning'} label={bindingProduct.stripe_ready ? '已就绪' : '未就绪'} />
              </div>

              <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
                <div>
                  <label htmlFor={`${formId}-st-acc`} className="block text-xs font-medium text-[var(--cms-muted-fg)] mb-1">Stripe 渠道账户</label>
                  <CustomSelect
                    id={`${formId}-st-acc`}
                    width="100%"
                    value={stForm.account_id}
                    onChange={(v) => setStForm((prev) => ({ ...prev, account_id: v }))}
                    options={envAccounts.stripe.map((a) => ({
                      value: String(a.id),
                      label: `${a.display_name || a.account_label}${a.is_primary ? ' (主用)' : ''} · ${a.status}`,
                    }))}
                    placeholder="选择 Stripe 账户"
                  />
                </div>
                <div>
                  <label htmlFor={`${formId}-st-mode`} className="block text-xs font-medium text-[var(--cms-muted-fg)] mb-1">销售模式</label>
                  <CustomSelect
                    id={`${formId}-st-mode`}
                    width="100%"
                    value={stForm.sales_mode}
                    onChange={(v) => setStForm((prev) => ({ ...prev, sales_mode: v as StripeSalesMode }))}
                    options={[
                      { value: 'direct', label: '直收 (Direct)' },
                      { value: 'merchant_of_record', label: 'Managed Payments (MoR)' },
                    ]}
                  />
                </div>
              </div>

              <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
                <div>
                  <label htmlFor={`${formId}-st-prod`} className="block text-xs font-medium text-[var(--cms-muted-fg)] mb-1">Stripe Product ID (prod_...) *</label>
                  <input
                    id={`${formId}-st-prod`}
                    type="text"
                    required
                    className="input-field w-full font-mono-code"
                    placeholder="prod_..."
                    value={stForm.product_ref}
                    onChange={(e) => setStForm((prev) => ({ ...prev, product_ref: e.target.value }))}
                  />
                </div>
                <div>
                  <label htmlFor={`${formId}-st-price`} className="block text-xs font-medium text-[var(--cms-muted-fg)] mb-1">Stripe Price ID (price_...) *</label>
                  <input
                    id={`${formId}-st-price`}
                    type="text"
                    required
                    className="input-field w-full font-mono-code"
                    placeholder="price_..."
                    value={stForm.price_ref}
                    onChange={(e) => setStForm((prev) => ({ ...prev, price_ref: e.target.value }))}
                  />
                </div>
              </div>

              {stForm.sales_mode === 'merchant_of_record' && (
                <div>
                  <label htmlFor={`${formId}-st-tax`} className="block text-xs font-medium text-[var(--cms-muted-fg)] mb-1">Tax Liability Account (acct_...) *</label>
                  <input
                    id={`${formId}-st-tax`}
                    type="text"
                    required
                    className="input-field w-full font-mono-code"
                    placeholder="acct_..."
                    value={stForm.tax_liability_account}
                    onChange={(e) => setStForm((prev) => ({ ...prev, tax_liability_account: e.target.value }))}
                  />
                </div>
              )}

              <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
                <div>
                  <label htmlFor={`${formId}-st-countries`} className="block text-xs font-medium text-[var(--cms-muted-fg)] mb-1">允许销售国家/地区 (ISO-2 逗号分隔) *</label>
                  <input
                    id={`${formId}-st-countries`}
                    type="text"
                    required
                    className="input-field w-full font-mono-code uppercase"
                    placeholder="US, CA, GB"
                    value={stForm.countries}
                    onChange={(e) => setStForm((prev) => ({ ...prev, countries: e.target.value }))}
                  />
                </div>
                <div>
                  <label htmlFor={`${formId}-st-reason`} className="block text-xs font-medium text-[var(--cms-muted-fg)] mb-1">审计变更原因 *</label>
                  <input
                    id={`${formId}-st-reason`}
                    type="text"
                    required
                    className="input-field w-full"
                    placeholder="例如：首期上线 Stripe 定价验证"
                    value={stForm.reason}
                    onChange={(e) => setStForm((prev) => ({ ...prev, reason: e.target.value }))}
                  />
                </div>
              </div>

              {env === 'live' && (
                <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
                  <div>
                    <label htmlFor={`${formId}-st-ev`} className="block text-xs font-medium text-[var(--cms-muted-fg)] mb-1">Live 上线证明 (Release Evidence) *</label>
                    <input
                      id={`${formId}-st-ev`}
                      type="text"
                      required
                      className="input-field w-full"
                      value={stForm.release_evidence}
                      onChange={(e) => setStForm((prev) => ({ ...prev, release_evidence: e.target.value }))}
                    />
                  </div>
                  <div>
                    <label htmlFor={`${formId}-st-pol`} className="block text-xs font-medium text-[var(--cms-muted-fg)] mb-1">市场准入策略 (Market Policy) *</label>
                    <input
                      id={`${formId}-st-pol`}
                      type="text"
                      required
                      className="input-field w-full"
                      value={stForm.market_policy}
                      onChange={(e) => setStForm((prev) => ({ ...prev, market_policy: e.target.value }))}
                    />
                  </div>
                </div>
              )}

              <div className="flex flex-wrap items-center justify-between gap-3 pt-1">
                <label className="inline-flex items-center gap-2 text-xs cursor-pointer">
                  <input type="checkbox" checked={stForm.enabled} onChange={(e) => setStForm((prev) => ({ ...prev, enabled: e.target.checked }))} />
                  <span>启用该商品在 Stripe 的下单能力</span>
                </label>
                {canWrite && (
                  <button type="submit" className="btn-primary text-xs" disabled={submitting}>
                    {submitting ? '调用 Stripe 核验中…' : '校验并保存 Stripe 绑定'}
                  </button>
                )}
              </div>
            </form>
          </div>
        )}
      </CmsDialog>

      {/* 上架 / 暂停 / 下线确认弹窗 */}
      <CmsDialog
        open={statusDialog !== null}
        onClose={() => { if (!submitting) setStatusDialog(null); }}
        size="sm"
        title={
          statusDialog?.action === 'publish'
            ? `上架商品 (${env})`
            : statusDialog?.action === 'pause'
              ? `暂停商品 (${env})`
              : `下线商品 (${env})`
        }
        subtitle={statusDialog ? `${statusDialog.product.name} · ${statusDialog.product.code}` : undefined}
        closedBy="closerequest"
        closeDisabled={submitting}
        footer={
          <>
            <button type="button" data-dialog-initial-focus className="btn-secondary" disabled={submitting} onClick={() => setStatusDialog(null)}>取消</button>
            <button
              type="button"
              className={`btn-primary ${statusDialog?.action === 'retire' ? 'bg-[var(--cms-error)] border-[var(--cms-error)]' : ''}`}
              disabled={submitting}
              onClick={() => void handleTransitionStatus()}
            >
              {submitting ? '提交中…' : '确认'}
            </button>
          </>
        }
      >
        {statusDialog && (
          <div className="space-y-3 text-sm">
            {statusDialog.action === 'retire' && (
              <p className="text-xs text-[var(--cms-warning)]">
                注意：下线后该商品不可重新上架（只能复制新建），同时请确保将其从活跃充值模板槽位移除。
              </p>
            )}
            <div>
              <label htmlFor={`${formId}-sreason`} className="block text-xs font-medium text-[var(--cms-muted-fg)] mb-1">操作备注（可选）</label>
              <input
                id={`${formId}-sreason`}
                type="text"
                className="input-field w-full"
                placeholder="填写审批或下架说明"
                value={statusReason}
                onChange={(e) => setStatusReason(e.target.value)}
              />
            </div>
            {formError && <p role="alert" className="text-xs text-[var(--cms-error)]">{formError}</p>}
          </div>
        )}
      </CmsDialog>
    </div>
  );
}
