import { useCallback, useContext, useEffect, useId, useMemo, useRef, useState } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { CheckCircle2, Plus, RefreshCw } from 'lucide-react';
import CustomSelect from '../components/CustomSelect';
import { PaymentLoadError } from '../components/payments/PaymentWorkspace';
import { CmsDialog } from '../components/ui/CmsDialog';
import { Pagination } from '../components/ui/Pagination';
import {
  type ColumnOption,
  type FilterChip,
  FilterChipsBar,
  RowActionMenu,
  type RowActionItem,
  SearchInput,
  SkeletonRows,
  StatusDot,
  type TableDensity,
  TableRegion,
  ViewOptionsPopover,
} from '../components/ui/Primitives';
import { usePermission } from '../context/PermissionContext';
import { ToastContext } from '../context/ToastContext';
import { apiRequest, operationsApi, type RecommendationNovel } from '../utils/api';
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
import {
  defaultStripeMoRTaxCode,
  extractStripeCatalogTax,
  filterPublishedChannelAccounts,
  findBindingForAccount,
  nextCopyCode,
  parseSpecificNovelIds as combineSpecificNovelIds,
  toggleSpecificNovelScope,
} from '../utils/productCatalogHelpers';
import {
  ALL_ISO2_COUNTRIES,
  DEFAULT_STRIPE_COUNTRIES,
  detectStripeCountriesPreset,
  formatAllStripeCountriesInput,
  formatAllowedCountriesSummary,
  formatDefaultStripeCountriesInput,
  parseStripeCountriesInput,
  resolveCountriesOnSalesModeChange,
  STRIPE_MOR_RESTRICTED_COUNTRIES,
  STRIPE_MOR_SUPPORTED_COUNTRIES,
} from '../utils/stripeCountries';

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
  sales_mode: StripeSalesMode;
  tax_code: string;
  tax_behavior: 'exclusive' | 'inclusive';
  countries: string;
  release_evidence: string;
  market_policy: string;
  reprovision: boolean;
  enabled: boolean;
  reason: string;
};

function errMsg(err: unknown, fallback: string): string {
  return err instanceof Error ? err.message : fallback;
}

export default function ProductsPage() {
  const { canRead, canWrite: hasWrite } = usePermission();
  const canWrite = hasWrite('payments.products');
  const canReadChannels = canRead('payments.channels');
  const canWriteChannels = hasWrite('payments.channels');
  const canManageBindings = canWrite && canWriteChannels;
  const canReadTemplates = canRead('ads.templates');
  const canReadNovels = canRead('content.novels');
  const canReadOperations = canRead('content.operations');
  const toastCtx = useContext(ToastContext);
  const showToast = useCallback(
    (msg: string, type: 'success' | 'error' | 'info' = 'success') => {
      if (toastCtx?.showToast) toastCtx.showToast(msg, type);
      else window.showToast?.(msg, type);
    },
    [toastCtx],
  );
  const [params, setParams] = useSearchParams();
  const env: PaymentEnvironment = params.get('environment') === 'live' ? 'live' : 'sandbox';
  const canPublish = canWrite && (env === 'sandbox' || canManageBindings);
  const [typeFilter, setTypeFilter] = useState<'all' | ProductType>('all');
  const [statusFilter, setStatusFilter] = useState<'all' | PublicationStatus>('all');
  const [searchQuery, setSearchQuery] = useState('');
  const [readyOnly, setReadyOnly] = useState(false);

  const [catalog, setCatalog] = useState<{ environment: PaymentEnvironment; products: ProductView[] }>();
  const products = catalog?.environment === env ? catalog.products : null;
  const [accounts, setAccounts] = useState<ChannelAccount[]>([]);
  const [loading, setLoading] = useState(true);
  const [hasLoadedOnce, setHasLoadedOnce] = useState(false);
  const [loadError, setLoadError] = useState('');
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(10);
  const [density, setDensity] = useState<TableDensity>('comfortable');
  const [visibleCols, setVisibleCols] = useState<{ bindings: boolean; templates: boolean }>({
    bindings: true,
    templates: true,
  });

  const [drawerOpen, setDrawerOpen] = useState(false);
  const [copySource, setCopySource] = useState<ProductView | null>(null);
  const [formState, setFormState] = useState<ProductFormState>(INITIAL_PRODUCT_FORM);
  const [novelOptions, setNovelOptions] = useState<RecommendationNovel[]>([]);
  const [novelSearch, setNovelSearch] = useState('');

  const [bindingProduct, setBindingProduct] = useState<ProductView | null>(null);
  const [ppForm, setPpForm] = useState<PayPalBindForm>({ account_id: '', external_product_ref: '', external_price_ref: '', enabled: true, reason: '' });
  const [stForm, setStForm] = useState<StripeBindForm>({
    account_id: '', sales_mode: 'direct', tax_code: defaultStripeMoRTaxCode(),
    tax_behavior: 'exclusive', countries: formatDefaultStripeCountriesInput('direct'), release_evidence: '', market_policy: '', reprovision: false, enabled: true, reason: '',
  });

  const [statusDialog, setStatusDialog] = useState<{ product: ProductView; action: 'publish' | 'pause' | 'retire' } | null>(null);
  const [statusReason, setStatusReason] = useState('');

  const [submitting, setSubmitting] = useState(false);
  const [formError, setFormError] = useState('');
  const formId = useId();
  const loadSequence = useRef(0);
  const bindingSequence = useRef(0);

  useEffect(() => {
    bindingSequence.current += 1;
    setBindingProduct(null);
    setStatusDialog(null);
  }, [env]);

  const loadData = useCallback(async (signal?: AbortSignal) => {
    const sequence = ++loadSequence.current;
    setLoading(true);
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
        canManageBindings ? channelsApi.list(signal) : Promise.resolve({ channels: [] as ChannelAccount[] }),
      ]);
      if (!signal?.aborted && sequence === loadSequence.current) {
        setCatalog({ environment: env, products: prodRes.products ?? [] });
        setAccounts(chRes.channels ?? []);
        setHasLoadedOnce(true);
      }
    } catch (err) {
      if (!signal?.aborted && sequence === loadSequence.current) {
        setLoadError(errMsg(err, '加载商品列表失败'));
        setHasLoadedOnce(true);
      }
    } finally {
      if (!signal?.aborted && sequence === loadSequence.current) {
        setLoading(false);
      }
    }
  }, [env, typeFilter, statusFilter, searchQuery, readyOnly, canManageBindings]);

  useEffect(() => {
    const controller = new AbortController();
    void loadData(controller.signal);
    return () => controller.abort();
  }, [loadData]);

  useEffect(() => {
    if (!drawerOpen || formState.product_type !== 'whole_book' || formState.novel_scope_type !== 'specific_novels') return;
    if (!canReadNovels && !canReadOperations) {
      setNovelOptions([]);
      return;
    }
    const controller = new AbortController();
    const fetchNovels = canReadNovels
      ? (() => {
          const query = new URLSearchParams({ publish_status: 'published', page: '1', page_size: '20' });
          if (novelSearch.trim()) query.set('search', novelSearch.trim());
          return apiRequest('GET', `/novels?${query.toString()}`, undefined, false, controller.signal);
        })()
      : operationsApi.getNovelOptions({ search: novelSearch, pageSize: 20, signal: controller.signal });
    void fetchNovels
      .then((res) => {
        if (!controller.signal.aborted) {
          setNovelOptions(Array.isArray(res?.novels) ? res.novels : Array.isArray(res?.items) ? res.items : []);
        }
      })
      .catch(() => {});
    return () => controller.abort();
  }, [drawerOpen, formState.product_type, formState.novel_scope_type, novelSearch, canReadNovels, canReadOperations]);

  const envAccounts = useMemo(
    () => ({
      paypal: filterPublishedChannelAccounts(accounts, 'paypal', env),
      stripe: filterPublishedChannelAccounts(accounts, 'stripe', env),
    }),
    [accounts, env],
  );

  const openCreateDrawer = () => {
    setCopySource(null);
    setFormState(INITIAL_PRODUCT_FORM);
    setFormError('');
    setDrawerOpen(true);
  };

  const openCopyDrawer = (prod: ProductView) => {
    setCopySource(prod);
    setFormState({
      code: nextCopyCode(prod.code),
      product_type: prod.product_type,
      name: `${prod.name} (新版本)`,
      description: prod.description || '',
      price_usd: (prod.price_cents / 100).toFixed(2),
      coins: String(prod.coins || 0),
      bonus: String(prod.bonus || 0),
      vip_cycle: (prod.vip_cycle || 'month') as VipCycle,
      novel_scope_type: prod.novel_scope_type === 'none' ? 'all_published' : prod.novel_scope_type,
      novel_scope_ids: prod.novel_scope_ids ?? [],
      manual_novel_ids: '',
    });
    setFormError('');
    setDrawerOpen(true);
  };

  const parseSpecificNovelIds = (): number[] =>
    combineSpecificNovelIds(formState.novel_scope_ids, formState.manual_novel_ids);

  const handleSaveProduct = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!canWrite || submitting) return;
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

  const syncBindingFormsFromProduct = (prod: ProductView, preferredPPAccountId?: string, preferredSTAccountId?: string) => {
    const ppAccDefault = envAccounts.paypal.find((a) => a.is_primary) ?? envAccounts.paypal[0];
    const stAccDefault = envAccounts.stripe.find((a) => a.is_primary) ?? envAccounts.stripe[0];
    const ppAccountId = preferredPPAccountId || String(prod.paypal_binding?.account_id ?? ppAccDefault?.id ?? '');
    const stAccountId = preferredSTAccountId || String(prod.stripe_binding?.account_id ?? stAccDefault?.id ?? '');
    const ppBinding = findBindingForAccount(prod, 'paypal', ppAccountId);
    const stBinding = findBindingForAccount(prod, 'stripe', stAccountId);
    const { taxCode, taxBehavior } = extractStripeCatalogTax(stBinding?.validated_catalog, prod.product_type);

    setPpForm({
      account_id: ppAccountId,
      external_product_ref: ppBinding?.external_product_ref ?? '',
      external_price_ref: ppBinding?.external_price_ref ?? '',
      enabled: ppBinding?.enabled ?? true,
      reason: '',
    });
    const stSalesMode: StripeSalesMode = stBinding?.sales_mode ?? 'direct';
    setStForm({
      account_id: stAccountId,
      sales_mode: stSalesMode,
      tax_code: taxCode,
      tax_behavior: taxBehavior,
      countries: stBinding?.allowed_countries?.length ? stBinding.allowed_countries.join(', ') : formatDefaultStripeCountriesInput(stSalesMode),
      release_evidence: stBinding?.release_evidence ?? '',
      market_policy: stBinding?.market_policy ?? '',
      reprovision: false,
      enabled: stBinding?.enabled ?? true,
      reason: '',
    });
  };

  const closeBindingsDrawer = () => {
    if (submitting) return;
    bindingSequence.current += 1;
    setBindingProduct(null);
  };

  const openBindingsDrawer = async (prod: ProductView) => {
    const sequence = ++bindingSequence.current;
    setFormError('');
    setBindingProduct(prod);
    syncBindingFormsFromProduct(prod);
    try {
      const fresh = await productsApi.get(prod.id, env);
      if (sequence !== bindingSequence.current) return;
      setBindingProduct(fresh.product);
      syncBindingFormsFromProduct(fresh.product);
    } catch {
      // keep snapshot
    }
  };

  const handleSavePayPalBinding = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!canManageBindings) return;
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
      syncBindingFormsFromProduct(updated.product, ppForm.account_id, stForm.account_id);
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
    if (!canManageBindings) return;
    if (!bindingProduct || submitting) return;
    setFormError('');
    const countries = parseStripeCountriesInput(stForm.countries);
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
        sales_mode: stForm.sales_mode,
        tax_code: stForm.sales_mode === 'merchant_of_record' ? stForm.tax_code.trim() : undefined,
        tax_behavior: stForm.sales_mode === 'merchant_of_record' ? stForm.tax_behavior : undefined,
        countries,
        release_evidence: stForm.release_evidence.trim(),
        market_policy: stForm.market_policy.trim(),
        reprovision: stForm.reprovision || undefined,
        enabled: stForm.enabled,
        reason: stForm.reason.trim(),
      });
      const updated = await productsApi.get(bindingProduct.id, env);
      setBindingProduct(updated.product);
      syncBindingFormsFromProduct(updated.product, ppForm.account_id, stForm.account_id);
      showToast('已联动 Stripe 自动创建/核验并保存绑定', 'success');
      await loadData();
    } catch (err) {
      setFormError(errMsg(err, 'Stripe 渠道绑定失败'));
    } finally {
      setSubmitting(false);
    }
  };

  const handleTransitionStatus = async () => {
    if (!statusDialog || submitting) return;
    if (!canPublish || (env === 'live' && !statusReason.trim())) return;
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
      ...toggleSpecificNovelScope(prev.novel_scope_ids, prev.manual_novel_ids, novelId),
    }));
  };

  const totalProducts = products?.length ?? 0;
  const totalPages = Math.max(1, Math.ceil(totalProducts / pageSize));
  const safePage = Math.min(page, totalPages);
  const pagedProducts = useMemo(() => {
    if (!products) return null;
    const start = (safePage - 1) * pageSize;
    return products.slice(start, start + pageSize);
  }, [products, safePage, pageSize]);

  const resetFilters = () => {
    setTypeFilter('all');
    setStatusFilter('all');
    setSearchQuery('');
    setReadyOnly(false);
    setPage(1);
  };

  const hasAnyFilter = Boolean(typeFilter !== 'all' || statusFilter !== 'all' || searchQuery.trim() || readyOnly);

  const filterChips = useMemo<FilterChip[]>(() => {
    const chips: FilterChip[] = [];
    if (searchQuery.trim()) {
      chips.push({
        key: 'q',
        label: `关键词: ${searchQuery.trim()}`,
        onClear: () => {
          setSearchQuery('');
          setPage(1);
        },
      });
    }
    if (typeFilter !== 'all') {
      chips.push({
        key: 'type',
        label: `类型: ${PRODUCT_TYPE_LABELS[typeFilter]}`,
        onClear: () => {
          setTypeFilter('all');
          setPage(1);
        },
      });
    }
    if (statusFilter !== 'all') {
      chips.push({
        key: 'status',
        label: `状态: ${PUBLICATION_STATUS_META[statusFilter].label}`,
        onClear: () => {
          setStatusFilter('all');
          setPage(1);
        },
      });
    }
    if (readyOnly) {
      chips.push({
        key: 'ready',
        label: '仅看双渠道就绪',
        onClear: () => {
          setReadyOnly(false);
          setPage(1);
        },
      });
    }
    return chips;
  }, [searchQuery, typeFilter, statusFilter, readyOnly]);

  const columnOptions: ColumnOption[] = [
    { key: 'bindings', label: '渠道绑定状态', visible: visibleCols.bindings },
    { key: 'templates', label: '模板引用', visible: visibleCols.templates },
  ];
  const colSpan = 6 + (visibleCols.bindings ? 1 : 0) + (visibleCols.templates ? 1 : 0);

  return (
    <div className="space-y-4 animate-fade-in">
      {/* Scope & Primary Actions Strip */}
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div
          role="group"
          aria-label="商品类型筛选"
          className="inline-flex flex-wrap items-center gap-1 rounded-xl bg-[var(--cms-secondary)] p-1"
        >
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
              onClick={() => {
                setTypeFilter(item.key);
                setPage(1);
              }}
              className={`focus-ring px-3 py-1.5 rounded-lg text-xs font-medium inline-flex items-center gap-2 cursor-pointer transition-colors ${
                typeFilter === item.key
                  ? 'bg-[var(--cms-card)] text-[var(--cms-fg)] shadow-xs'
                  : 'text-[var(--cms-muted-fg)] hover:text-[var(--cms-fg)]'
              }`}
            >
              {item.label}
            </button>
          ))}
        </div>

        <div className="flex flex-wrap items-center gap-2">
          <CustomSelect
            aria-label="切换商品环境"
            width="124px"
            value={env}
            options={[
              { value: 'sandbox', label: 'Sandbox 沙箱' },
              { value: 'live', label: 'Live 正式' },
            ]}
            onChange={(value) => {
              setPage(1);
              const next = new URLSearchParams(params);
              next.set('environment', value);
              setParams(next);
            }}
          />
          {canReadTemplates && (
            <Link to="/recharge-templates" className="btn-secondary h-9 px-3 text-xs font-normal whitespace-nowrap">
              充值模板
            </Link>
          )}
          <button
            type="button"
            className="btn-secondary h-9 px-3 text-xs font-normal whitespace-nowrap"
            onClick={() => void loadData()}
          >
            <RefreshCw size={13} className={loading ? 'animate-spin' : ''} aria-hidden="true" />
            <span>刷新</span>
          </button>
          {canWrite && (
            <button type="button" className="btn-primary h-9 px-3.5 text-xs" onClick={openCreateDrawer}>
              <Plus size={14} aria-hidden="true" />
              <span>新建商品</span>
            </button>
          )}
        </div>
      </div>

      {/* Unified Borderless Filter Strip */}
      <search className="block space-y-2">
        <div className="flex flex-wrap items-center justify-between gap-2.5">
          <div className="flex flex-wrap items-center gap-2 flex-1 min-w-[260px]">
            <SearchInput
              className="w-full sm:w-64"
              ariaLabel="搜索商品"
              placeholder="搜索商品编码 / 名称..."
              value={searchQuery}
              onChange={(val) => {
                setSearchQuery(val);
                setPage(1);
              }}
              onClear={() => {
                setSearchQuery('');
                setPage(1);
              }}
            />
            <CustomSelect
              value={statusFilter}
              onChange={(v) => {
                setStatusFilter(v as 'all' | PublicationStatus);
                setPage(1);
              }}
              width="136px"
              aria-label="按发布状态筛选"
              options={[
                { value: 'all', label: '全部状态' },
                { value: 'draft', label: '草稿' },
                { value: 'published', label: '已上架' },
                { value: 'paused', label: '已暂停' },
                { value: 'retired', label: '已下线' },
              ]}
            />
            <label className="inline-flex items-center gap-1.5 h-9 px-2 text-xs text-[var(--cms-muted-fg)] hover:text-[var(--cms-fg)] cursor-pointer select-none whitespace-nowrap">
              <input
                type="checkbox"
                checked={readyOnly}
                onChange={(e) => {
                  setReadyOnly(e.target.checked);
                  setPage(1);
                }}
              />
              <span>仅看双渠道就绪</span>
            </label>
            {hasAnyFilter && (
              <button type="button" onClick={resetFilters} className="btn-ghost h-9 text-xs text-[var(--cms-muted-fg)]">
                重置
              </button>
            )}
          </div>

          <div className="flex items-center gap-2 text-xs text-[var(--cms-muted-fg)]">
            <span className="tabular-nums">共 {totalProducts} 件商品</span>
            <ViewOptionsPopover
              density={density}
              onDensityChange={setDensity}
              columns={columnOptions}
              onToggleColumn={(k) =>
                setVisibleCols((prev) => ({
                  ...prev,
                  [k as 'bindings' | 'templates']: !prev[k as 'bindings' | 'templates'],
                }))
              }
            />
          </div>
        </div>

        <FilterChipsBar chips={filterChips} onResetAll={resetFilters} />
      </search>

      {loadError ? (
        <PaymentLoadError message={loadError} onRetry={() => void loadData()} />
      ) : (
        <TableRegion
          ariaLabel="商品与双渠道定价列表"
          footer={
            <Pagination
              page={safePage}
              pageSize={pageSize}
              total={totalProducts}
              disabled={loading}
              onPageChange={setPage}
              onPageSizeChange={(nextSize) => {
                setPage(1);
                setPageSize(nextSize);
              }}
              itemLabel="件商品"
            />
          }
        >
          <table className="cms-table min-w-[920px]" data-density={density} aria-busy={loading && !hasLoadedOnce}>
            <thead>
              <tr>
                <th scope="col">商品 / 编码</th>
                <th scope="col">类型</th>
                <th scope="col" className="text-right">
                  售价
                </th>
                <th scope="col">商业权益（不可变）</th>
                {visibleCols.bindings && (
                  <th scope="col">渠道绑定 ({env === 'live' ? 'Live' : 'Sandbox'})</th>
                )}
                {visibleCols.templates && <th scope="col">模板引用</th>}
                <th scope="col">状态</th>
                <th scope="col" className="text-right w-px whitespace-nowrap">
                  操作
                </th>
              </tr>
            </thead>
            <tbody className={loading && hasLoadedOnce ? 'opacity-65 transition-opacity' : ''}>
              {!hasLoadedOnce && loading ? (
                <SkeletonRows rows={5} cols={colSpan} />
              ) : !pagedProducts || pagedProducts.length === 0 ? (
                <tr>
                  <td colSpan={colSpan} className="py-16 text-center text-[var(--cms-muted-fg)]">
                    暂无符合条件的商品记录
                  </td>
                </tr>
              ) : (
                pagedProducts.map((p) => {
                  const pub = p.publications?.[env];
                  const pubStatus: PublicationStatus = pub?.status ?? 'draft';
                  const pubMeta = PUBLICATION_STATUS_META[pubStatus];
                  const menuItems: RowActionItem[] = [];
                  if (canPublish && pubStatus === 'published') {
                    menuItems.push({
                      label: '暂停销售',
                      onClick: () => {
                        setStatusReason('');
                        setFormError('');
                        setStatusDialog({ product: p, action: 'pause' });
                      },
                    });
                  }
                  if (canWrite) {
                    menuItems.push({ label: '复制调价', onClick: () => openCopyDrawer(p) });
                  }
                  if (canPublish && pubStatus !== 'retired') {
                    menuItems.push({
                      label: '下线商品',
                      tone: 'danger',
                      dividerBefore: menuItems.length > 0,
                      onClick: () => {
                        setStatusReason('');
                        setFormError('');
                        setStatusDialog({ product: p, action: 'retire' });
                      },
                    });
                  }
                  return (
                    <tr key={p.id} className={bindingProduct?.id === p.id ? 'row-selected' : undefined}>
                      <td>
                        <div className="text-[var(--cms-fg)]">
                          <span>{p.name}</span>
                          {p.source_product_id && (
                            <span className="ml-1.5 text-[var(--cms-muted-fg)]">· 复制自 #{p.source_product_id}</span>
                          )}
                        </div>
                        <div className="text-[var(--cms-muted-fg)] mt-0.5 font-mono-code truncate max-w-[260px]" title={p.description ? `${p.code} · ${p.description}` : p.code}>
                          {p.code}
                          {p.description ? ` · ${p.description}` : ''}
                        </div>
                      </td>
                      <td className="text-[var(--cms-muted-fg)] whitespace-nowrap">
                        {PRODUCT_TYPE_LABELS[p.product_type]}
                      </td>
                      <td className="text-right tabular-nums text-[var(--cms-fg)] whitespace-nowrap">
                        <div>{p.price}</div>
                        <div className="text-[var(--cms-muted-fg)] mt-0.5">USD</div>
                      </td>
                      <td>
                        {p.product_type === 'single' && (
                          <>
                            <div className="tabular-nums text-[var(--cms-fg)] whitespace-nowrap">
                              {p.coins} 金币
                              {p.bonus > 0 && <span className="text-[var(--cms-muted-fg)]"> +{p.bonus}</span>}
                            </div>
                            <div className="text-[var(--cms-muted-fg)] mt-0.5">单次充值</div>
                          </>
                        )}
                        {p.product_type === 'whole_book' && (
                          <>
                            <div className="text-[var(--cms-fg)] whitespace-nowrap">
                              {NOVEL_SCOPE_LABELS[p.novel_scope_type]}
                            </div>
                            <div className="text-[var(--cms-muted-fg)] mt-0.5 tabular-nums">
                              {p.novel_scope_type === 'specific_novels' ? `书籍 ${p.novel_scope_ids.join(', ')} · ` : ''}无赠币
                            </div>
                          </>
                        )}
                        {p.product_type === 'vip' && (
                          <>
                            <div className="text-[var(--cms-fg)] whitespace-nowrap">
                              {p.vip_cycle ? VIP_CYCLE_LABELS[p.vip_cycle] : '—'}
                            </div>
                            <div className="text-[var(--cms-muted-fg)] mt-0.5">自动续订 · 无赠币</div>
                          </>
                        )}
                      </td>
                      {visibleCols.bindings && (
                        <td>
                          <div className="flex flex-col gap-1">
                            <div className="flex items-center gap-1.5">
                              <StatusDot
                                tone={p.paypal_ready ? 'success' : 'warning'}
                                label={`PayPal: ${p.paypal_ready ? '就绪' : '待完善'}`}
                              />
                              {canReadChannels && p.paypal_binding?.external_price_ref && (
                                <span className="font-mono-code text-[var(--cms-muted-fg)] truncate max-w-[140px]">
                                  {p.paypal_binding.external_price_ref}
                                </span>
                              )}
                            </div>
                            <div className="flex items-center gap-1.5">
                              <StatusDot
                                tone={p.stripe_ready ? 'success' : 'warning'}
                                label={`Stripe: ${p.stripe_ready ? '就绪' : '待完善'}`}
                              />
                              {canReadChannels && p.stripe_binding?.external_price_ref && (
                                <span className="font-mono-code text-[var(--cms-muted-fg)] truncate max-w-[140px]">
                                  {p.stripe_binding.external_price_ref}
                                </span>
                              )}
                            </div>
                            {!p.dual_channel_ready && p.readiness_reasons.length > 0 && (
                              <div className="text-[var(--cms-warning)]">{p.readiness_reasons[0]}</div>
                            )}
                          </div>
                        </td>
                      )}
                      {visibleCols.templates && (
                        <td className="text-[var(--cms-muted-fg)] max-w-[200px]">
                          {p.linked_template_names.length > 0 ? p.linked_template_names.join('、') : '—'}
                        </td>
                      )}
                      <td>
                        <StatusDot tone={pubMeta.tone} label={pubMeta.label} />
                      </td>
                      <td className="text-right whitespace-nowrap">
                        <div className="inline-flex items-center justify-end gap-0.5 whitespace-nowrap">
                          {canReadChannels && (
                            <button type="button" className="btn-detail" onClick={() => void openBindingsDrawer(p)}>
                              {canManageBindings ? '绑定' : '绑定详情'}
                            </button>
                          )}
                          {canPublish && pubStatus !== 'retired' && pubStatus !== 'published' && (
                            <button
                              type="button"
                              className="btn-detail"
                              onClick={() => {
                                setStatusReason('');
                                setFormError('');
                                setStatusDialog({ product: p, action: 'publish' });
                              }}
                            >
                              上架
                            </button>
                          )}
                          {menuItems.length > 0 && (
                            <RowActionMenu ariaLabel={`${p.name} 更多操作`} items={menuItems} />
                          )}
                          {!canReadChannels && !(canPublish && pubStatus !== 'retired' && pubStatus !== 'published') && menuItems.length === 0 && (
                            <span className="text-[var(--cms-muted-fg)]">—</span>
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
      )}

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
                    {formState.novel_scope_ids
                      .filter((id) => !novelOptions.some((n) => n.id === id))
                      .map((id) => (
                        <label key={`selected-${id}`} className="flex items-center gap-2 text-xs py-1 cursor-pointer">
                          <input
                            type="checkbox"
                            checked
                            onChange={() => toggleNovelId(id)}
                          />
                          <span className="font-mono-code text-[var(--cms-muted-fg)]">#{id}</span>
                          <span className="truncate text-[var(--cms-muted-fg)]">已关联小说 #{id}</span>
                        </label>
                      ))}
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
                    {novelOptions.length === 0 && formState.novel_scope_ids.length === 0 && (
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
        onClose={closeBindingsDrawer}
        variant="drawer"
        size="lg"
        title={bindingProduct ? `双渠道绑定配置 · ${bindingProduct.name}` : '双渠道绑定'}
        subtitle={bindingProduct ? `${bindingProduct.code} · ${PRODUCT_TYPE_LABELS[bindingProduct.product_type]} · ${bindingProduct.price} (${env} 环境)` : undefined}
        closedBy="closerequest"
        closeDisabled={submitting}
        footer={
          <button type="button" className="btn-secondary" disabled={submitting} onClick={closeBindingsDrawer}>完成并关闭</button>
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
              {canPublish && bindingProduct.dual_channel_ready && bindingProduct.publications?.[env]?.status !== 'published' && (
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

            {canReadChannels && !canManageBindings && (() => {
              const bindings = bindingProduct.all_bindings && bindingProduct.all_bindings.length > 0
                ? bindingProduct.all_bindings
                : [bindingProduct.paypal_binding, bindingProduct.stripe_binding].filter(
                    (b): b is NonNullable<typeof b> => Boolean(b),
                  );
              return (
                <div className="space-y-3 text-xs">
                  <div className="p-3 rounded-xl bg-[var(--cms-content)] border border-[var(--cms-border)] text-[var(--cms-muted-fg)]">
                    当前账号具有渠道绑定只读权限，可查看各渠道绑定明细，修改绑定需同时具备「商品与定价写权限」和「渠道与商户写权限」。
                  </div>
                  {bindings.length === 0 ? (
                    <div className="cms-card p-4 text-[var(--cms-muted-fg)]">
                      该商品在 {env} 环境尚未配置任何渠道绑定记录。
                    </div>
                  ) : (
                    bindings.map((b) => (
                      <div key={b.id} className="cms-card p-4 space-y-2">
                        <div className="flex flex-wrap items-center justify-between gap-2">
                          <div className="font-semibold text-sm text-[var(--cms-fg)]">
                            {b.provider === 'paypal' ? 'PayPal' : 'Stripe'} · {b.account_display_name || b.account_key}
                            {b.account_is_primary ? '（主用）' : '（备选）'}
                          </div>
                          <StatusDot
                            tone={b.validation_status === 'verified' && b.enabled ? 'success' : 'warning'}
                            label={b.validation_status === 'verified' && b.enabled ? '已就绪' : `${b.validation_status}${b.enabled ? '' : ' · 已停用'}`}
                          />
                        </div>
                        <dl className="grid grid-cols-1 sm:grid-cols-2 gap-2 text-[var(--cms-muted-fg)]">
                          <div>
                            <dt>外部商品 ID</dt>
                            <dd className="font-mono-code text-[var(--cms-fg)] break-all">{b.external_product_ref || '—'}</dd>
                          </div>
                          <div>
                            <dt>外部价格 / Plan ID</dt>
                            <dd className="font-mono-code text-[var(--cms-fg)] break-all">{b.external_price_ref || '—'}</dd>
                          </div>
                          {b.provider === 'stripe' && (
                            <>
                              <div>
                                <dt>销售模式</dt>
                                <dd className="text-[var(--cms-fg)]">{b.sales_mode === 'merchant_of_record' ? 'Managed Payments (MoR)' : '直收 (Direct)'}</dd>
                              </div>
                              <div>
                                <dt>允许销售国家/地区</dt>
                                <dd className="font-mono-code text-[var(--cms-fg)]" title={b.allowed_countries?.join(', ')}>
                                  {formatAllowedCountriesSummary(b.allowed_countries)}
                                </dd>
                              </div>
                            </>
                          )}
                        </dl>
                        {b.validation_error && (
                          <p role="alert" className="text-[var(--cms-error)] m-0">
                            上次同步异常：{b.validation_error}
                          </p>
                        )}
                      </div>
                    ))
                  )}
                </div>
              );
            })()}

            {canManageBindings && (() => {
              const activePPBinding = findBindingForAccount(bindingProduct, 'paypal', ppForm.account_id);
              const activeSTBinding = findBindingForAccount(bindingProduct, 'stripe', stForm.account_id);
              return <>
            {/* PayPal 绑定表单 */}
            <form onSubmit={handleSavePayPalBinding} className="cms-card p-4 space-y-3">
              <div className="flex items-center justify-between">
                <h3 className="text-sm font-semibold">PayPal 渠道绑定</h3>
                <StatusDot
                  tone={activePPBinding ? (activePPBinding.validation_status === 'verified' && activePPBinding.enabled ? 'success' : 'warning') : (bindingProduct.paypal_ready ? 'success' : 'warning')}
                  label={activePPBinding ? (activePPBinding.validation_status === 'verified' && activePPBinding.enabled ? (activePPBinding.account_is_primary ? '已就绪' : '备账户已就绪') : activePPBinding.validation_status) : '未绑定'}
                />
              </div>

              <div>
                <label htmlFor={`${formId}-pp-acc`} className="block text-xs font-medium text-[var(--cms-muted-fg)] mb-1">PayPal 渠道账户</label>
                <CustomSelect
                  id={`${formId}-pp-acc`}
                  width="100%"
                  value={ppForm.account_id}
                  onChange={(v) => {
                    const matched = findBindingForAccount(bindingProduct, 'paypal', v);
                    setPpForm({
                      account_id: v,
                      external_product_ref: matched?.external_product_ref ?? '',
                      external_price_ref: matched?.external_price_ref ?? '',
                      enabled: matched?.enabled ?? true,
                      reason: '',
                    });
                  }}
                  options={envAccounts.paypal.map((a) => ({
                    value: String(a.id),
                    label: `${a.display_name || a.account_label}${a.is_primary ? ' (主用)' : ' (备选)'} · ${a.status}`,
                  }))}
                  placeholder="选择 PayPal 账户"
                />
              </div>

              {activePPBinding?.validation_error && (
                <p role="alert" className="text-xs text-[var(--cms-error)]">
                  上次同步异常：{activePPBinding.validation_error}
                </p>
              )}

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
                {canManageBindings && (
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
                <StatusDot
                  tone={activeSTBinding ? (activeSTBinding.validation_status === 'verified' && activeSTBinding.enabled ? 'success' : 'warning') : 'warning'}
                  label={activeSTBinding ? (activeSTBinding.validation_status === 'verified' && activeSTBinding.enabled ? (activeSTBinding.account_is_primary ? '已就绪' : '备账户已就绪') : activeSTBinding.validation_status) : '未绑定'}
                />
              </div>

              <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
                <div>
                  <label htmlFor={`${formId}-st-acc`} className="block text-xs font-medium text-[var(--cms-muted-fg)] mb-1">Stripe 渠道账户</label>
                  <CustomSelect
                    id={`${formId}-st-acc`}
                    width="100%"
                    value={stForm.account_id}
                    onChange={(v) => {
                      const matched = findBindingForAccount(bindingProduct, 'stripe', v);
                      const { taxCode, taxBehavior } = extractStripeCatalogTax(matched?.validated_catalog, bindingProduct?.product_type);
                      const nextSalesMode: StripeSalesMode = matched?.sales_mode ?? 'direct';
                      setStForm({
                        account_id: v,
                        sales_mode: nextSalesMode,
                        tax_code: taxCode,
                        tax_behavior: taxBehavior,
                        countries: matched?.allowed_countries?.length
                          ? matched.allowed_countries.join(', ')
                          : formatDefaultStripeCountriesInput(nextSalesMode),
                        release_evidence: matched?.release_evidence ?? '',
                        market_policy: matched?.market_policy ?? '',
                        reprovision: false,
                        enabled: matched?.enabled ?? true,
                        reason: '',
                      });
                    }}
                    options={envAccounts.stripe.map((a) => ({
                      value: String(a.id),
                      label: `${a.display_name || a.account_label}${a.is_primary ? ' (主用)' : ' (备选)'} · ${a.status}`,
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
                    onChange={(v) => {
                      const nextMode = v as StripeSalesMode;
                      setStForm((prev) => ({
                        ...prev,
                        sales_mode: nextMode,
                        tax_code:
                          nextMode === 'merchant_of_record' && !prev.tax_code.trim()
                            ? defaultStripeMoRTaxCode(bindingProduct?.product_type)
                            : prev.tax_code,
                        countries: resolveCountriesOnSalesModeChange(prev.countries, prev.sales_mode, nextMode),
                      }));
                    }}
                    options={[
                      { value: 'direct', label: '直收 (Direct)' },
                      { value: 'merchant_of_record', label: 'Managed Payments (MoR)' },
                    ]}
                  />
                </div>
              </div>

              {activeSTBinding?.external_price_ref ? (
                <div className="rounded-lg border border-[var(--cms-border)] bg-[var(--cms-surface-alt)] px-3 py-2 text-xs space-y-1">
                  <div className="font-medium text-[var(--cms-fg)]">已自动联动 Stripe 外部资源（不可手动绑定）</div>
                  <div className="flex flex-wrap gap-x-4 gap-y-1 font-mono-code text-[var(--cms-muted-fg)]">
                    <span>Product: {activeSTBinding.external_product_ref || '—'}</span>
                    <span>Price: {activeSTBinding.external_price_ref}</span>
                  </div>
                </div>
              ) : (
                <p className="text-xs text-[var(--cms-muted-fg)]">
                  仅支持自动联动创建：保存时系统将按当前商品名称、USD 售价与计费周期在所选 Stripe 账户自动创建 Product 与 Price（不支持手动绑定已有 ID）。
                </p>
              )}

              {activeSTBinding?.validation_error && (
                <p role="alert" className="text-xs text-[var(--cms-error)]">
                  上次同步异常：{activeSTBinding.validation_error}
                </p>
              )}

              {activeSTBinding?.validation_status === 'failed' && activeSTBinding?.external_price_ref && (
                <label className="inline-flex items-center gap-2 text-xs text-[var(--cms-warning)] cursor-pointer">
                  <input
                    type="checkbox"
                    checked={stForm.reprovision}
                    onChange={(e) => setStForm((prev) => ({ ...prev, reprovision: e.target.checked }))}
                  />
                  <span>远端 Stripe Product / Price 已归档失效，强制重建新资源（Reprovision）</span>
                </label>
              )}

              {stForm.sales_mode === 'merchant_of_record' && (
                <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
                  <div>
                    <label htmlFor={`${formId}-st-taxcode`} className="block text-xs font-medium text-[var(--cms-muted-fg)] mb-1">Stripe Tax Code *</label>
                    <input
                      id={`${formId}-st-taxcode`}
                      type="text"
                      required
                      className="input-field w-full font-mono-code"
                      placeholder={defaultStripeMoRTaxCode(bindingProduct?.product_type)}
                      value={stForm.tax_code}
                      onChange={(e) => setStForm((prev) => ({ ...prev, tax_code: e.target.value }))}
                    />
                  </div>
                  <div>
                    <label htmlFor={`${formId}-st-taxbehav`} className="block text-xs font-medium text-[var(--cms-muted-fg)] mb-1">含税方式 (Tax Behavior) *</label>
                    <CustomSelect
                      id={`${formId}-st-taxbehav`}
                      width="100%"
                      value={stForm.tax_behavior}
                      onChange={(v) => setStForm((prev) => ({ ...prev, tax_behavior: v as 'exclusive' | 'inclusive' }))}
                      options={[
                        { value: 'exclusive', label: '不含税 (exclusive)' },
                        { value: 'inclusive', label: '含税 (inclusive)' },
                      ]}
                    />
                  </div>
                </div>
              )}

              {(() => {
                const parsedCountries = parseStripeCountriesInput(stForm.countries);
                const preset = detectStripeCountriesPreset(parsedCountries);
                const presetLabel =
                  preset === 'global'
                    ? `全球 · ${parsedCountries.length} 个`
                    : preset === 'mor_all'
                      ? `MoR 官方全量 · ${parsedCountries.length} 个`
                      : `已选 ${parsedCountries.length} 个`;
                return (
                  <div className="space-y-3">
                    <div className="space-y-1.5">
                      <div className="flex flex-wrap items-center justify-between gap-2">
                        <label htmlFor={`${formId}-st-countries`} className="text-xs font-medium text-[var(--cms-muted-fg)]">
                          允许销售国家/地区 (ISO-2 逗号分隔) *
                          <span className="ml-2 inline-flex items-center rounded bg-[var(--cms-surface-alt)] px-1.5 py-0.5 text-[11px] font-normal text-[var(--cms-fg)]">
                            {presetLabel}
                          </span>
                        </label>
                        <div className="inline-flex flex-wrap items-center gap-1.5">
                          <button
                            type="button"
                            className="btn-detail text-[11px] px-2 py-0.5"
                            onClick={() =>
                              setStForm((prev) => ({
                                ...prev,
                                countries: formatDefaultStripeCountriesInput(prev.sales_mode),
                              }))
                            }
                          >
                            默认 ({DEFAULT_STRIPE_COUNTRIES.join('/')})
                          </button>
                          {stForm.sales_mode === 'merchant_of_record' && (
                            <button
                              type="button"
                              className="btn-detail text-[11px] px-2 py-0.5"
                              onClick={() =>
                                setStForm((prev) => ({
                                  ...prev,
                                  countries: formatAllStripeCountriesInput('merchant_of_record'),
                                }))
                              }
                            >
                              MoR 官方全选 ({STRIPE_MOR_SUPPORTED_COUNTRIES.length})
                            </button>
                          )}
                          <button
                            type="button"
                            className="btn-detail text-[11px] px-2 py-0.5"
                            onClick={() =>
                              setStForm((prev) => ({
                                ...prev,
                                countries: formatAllStripeCountriesInput('direct'),
                              }))
                            }
                          >
                            全球/全选 ({ALL_ISO2_COUNTRIES.length})
                          </button>
                          <button
                            type="button"
                            className="btn-ghost text-[11px] px-2 py-0.5"
                            onClick={() => setStForm((prev) => ({ ...prev, countries: '' }))}
                          >
                            清空
                          </button>
                        </div>
                      </div>
                      <textarea
                        id={`${formId}-st-countries`}
                        rows={2}
                        required
                        className="input-field w-full font-mono-code text-xs uppercase resize-y"
                        placeholder="US, CA, GB, AU"
                        value={stForm.countries}
                        onChange={(e) => setStForm((prev) => ({ ...prev, countries: e.target.value }))}
                      />
                      {stForm.sales_mode === 'merchant_of_record' && (
                        <p className="text-[11px] text-[var(--cms-muted-fg)] m-0">
                          默认填入美加英澳（{DEFAULT_STRIPE_COUNTRIES.join(', ')}）；MoR 官方支持共 {STRIPE_MOR_SUPPORTED_COUNTRIES.length} 个国家/地区（已排除受限地区：{STRIPE_MOR_RESTRICTED_COUNTRIES.join(', ')}）。
                        </p>
                      )}
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
                );
              })()}

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
                {canManageBindings && (
                  <button type="submit" className="btn-primary text-xs" disabled={submitting}>
                    {submitting
                      ? '联动 Stripe 创建并核验中…'
                      : activeSTBinding?.external_price_ref
                        ? '同步并保存 Stripe 绑定'
                        : '联动 Stripe 创建并绑定'}
                  </button>
                )}
              </div>
            </form>
            </>;
            })()}
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
              disabled={submitting || (env === 'live' && !statusReason.trim())}
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
              <label htmlFor={`${formId}-sreason`} className="block text-xs font-medium text-[var(--cms-muted-fg)] mb-1">
                {env === 'live' ? '正式环境操作依据（必填）' : '操作备注（可选）'}
              </label>
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
