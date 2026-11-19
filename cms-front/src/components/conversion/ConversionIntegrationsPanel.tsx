import { useCallback, useMemo, useState } from 'react';
import { History, Plus, RefreshCw } from 'lucide-react';
import { sourceEventLabel, sourceLabel } from '../../utils/conversionStatus';
import CustomSelect from '../CustomSelect';
import {
  type ColumnOption,
  type FilterChip,
  FilterChipsBar,
  SearchInput,
  SkeletonRows,
  type TableDensity,
  TableRegion,
  ViewOptionsPopover,
} from '../ui/Primitives';
import { ConversionStatusDot, CopyableText } from './ConversionShared';
import { IntegrationConfigDrawer, type IntegrationDrawerTab } from './IntegrationConfigDrawer';
import type { ConversionDestination, ConversionIntegration } from './types';

type IntegrationStatusFilter = '' | 'enabled' | 'disabled';
type IntegrationOptionalCol = 'integration_id' | 'allowed_apps';

const SOURCE_OPTIONS = [
  { value: '', label: '全部来源' },
  { value: 'starnovel', label: 'StarNovel 自建' },
  { value: 'sinan', label: '番茄司南' },
];

const STATUS_OPTIONS = [
  { value: '', label: '全部状态' },
  { value: 'enabled', label: '已启用' },
  { value: 'disabled', label: '已停用' },
];

export function ConversionIntegrationsPanel({
  integrations,
  destinations,
  loading,
  hasLoadedOnce,
  loadError = '',
  canWrite,
  onRefresh,
}: {
  integrations: ConversionIntegration[];
  destinations: ConversionDestination[];
  loading: boolean;
  hasLoadedOnce: boolean;
  loadError?: string;
  canWrite: boolean;
  onRefresh: () => Promise<void> | void;
}) {
  const [searchQuery, setSearchQuery] = useState('');
  const [sourceFilter, setSourceFilter] = useState('');
  const [statusFilter, setStatusFilter] = useState<IntegrationStatusFilter>('');
  const [density, setDensity] = useState<TableDensity>('comfortable');
  const [visibleCols, setVisibleCols] = useState<Record<IntegrationOptionalCol, boolean>>({
    integration_id: false,
    allowed_apps: true,
  });

  const [drawerOpen, setDrawerOpen] = useState(false);
  const [editingIntegration, setEditingIntegration] = useState<ConversionIntegration | null>(null);
  const [drawerTab, setDrawerTab] = useState<IntegrationDrawerTab>('config');

  const destinationMap = useMemo(
    () => new Map(destinations.map((d) => [d.id, d])),
    [destinations],
  );

  const filteredIntegrations = useMemo(() => {
    const q = searchQuery.trim().toLowerCase();
    return integrations.filter((item) => {
      if (sourceFilter && item.source !== sourceFilter) return false;
      if (statusFilter === 'enabled' && !item.enabled) return false;
      if (statusFilter === 'disabled' && item.enabled) return false;
      if (q) {
        const hitBinding = (item.bindings ?? []).some((b) => b.pixel_id.toLowerCase().includes(q));
        const hitEvent = (item.allowed_events ?? []).some((ev) => ev.toLowerCase().includes(q));
        const matched =
          item.account_key.toLowerCase().includes(q) ||
          item.source.toLowerCase().includes(q) ||
          sourceLabel(item.source).toLowerCase().includes(q) ||
          item.contract_version.toLowerCase().includes(q) ||
          item.id.toLowerCase().includes(q) ||
          hitBinding ||
          hitEvent;
        if (!matched) return false;
      }
      return true;
    });
  }, [integrations, searchQuery, sourceFilter, statusFilter]);

  const filterChips = useMemo<FilterChip[]>(() => {
    const chips: FilterChip[] = [];
    if (searchQuery.trim()) {
      chips.push({
        key: 'search',
        label: `搜索: ${searchQuery.trim()}`,
        onClear: () => setSearchQuery(''),
      });
    }
    if (sourceFilter) {
      chips.push({
        key: 'source',
        label: `来源: ${sourceLabel(sourceFilter)}`,
        onClear: () => setSourceFilter(''),
      });
    }
    if (statusFilter) {
      chips.push({
        key: 'status',
        label: `状态: ${statusFilter === 'enabled' ? '已启用' : '已停用'}`,
        onClear: () => setStatusFilter(''),
      });
    }
    return chips;
  }, [searchQuery, sourceFilter, statusFilter]);

  const clearAllFilters = useCallback(() => {
    setSearchQuery('');
    setSourceFilter('');
    setStatusFilter('');
  }, []);

  const columnOptions: ColumnOption[] = [
    { key: 'integration_id', label: '接入编号 ID', visible: visibleCols.integration_id },
    { key: 'allowed_apps', label: '允许应用范围', visible: visibleCols.allowed_apps },
  ];

  const colSpan =
    5 + (visibleCols.integration_id ? 1 : 0) + (visibleCols.allowed_apps ? 1 : 0);

  const openCreateDrawer = () => {
    if (!canWrite) return;
    setEditingIntegration(null);
    setDrawerTab('config');
    setDrawerOpen(true);
  };

  const openIntegrationDrawer = (
    integ: ConversionIntegration,
    tab: IntegrationDrawerTab = 'config',
  ) => {
    setEditingIntegration(integ);
    setDrawerTab(tab);
    setDrawerOpen(true);
  };

  return (
    <div className="space-y-4 animate-fade-in">
      {/* Unified Borderless Filter & Action Strip */}
      <search className="block space-y-2">
        <div className="flex flex-wrap items-center justify-between gap-2.5">
          <div className="flex flex-wrap items-center gap-2 flex-1 min-w-[280px]">
            <SearchInput
              className="w-full sm:w-64"
              ariaLabel="搜索接入账户、契约或绑定的 Pixel ID"
              placeholder="搜索账户 Key / 契约 / Pixel ID..."
              value={searchQuery}
              onChange={setSearchQuery}
              onClear={() => setSearchQuery('')}
            />
            <CustomSelect
              aria-label="筛选接入来源"
              width="142px"
              options={SOURCE_OPTIONS}
              value={sourceFilter}
              onChange={setSourceFilter}
            />
            <CustomSelect
              aria-label="筛选接入启用状态"
              width="124px"
              options={STATUS_OPTIONS}
              value={statusFilter}
              onChange={(val) => setStatusFilter(val as IntegrationStatusFilter)}
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
                  [key]: !prev[key as IntegrationOptionalCol],
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
                <span>新建接入方</span>
              </button>
            )}
          </div>
        </div>

        <FilterChipsBar chips={filterChips} onResetAll={clearAllFilters} />
      </search>

      <TableRegion
        ariaLabel="渠道接入与 Pixel 路由授权列表"
        footer={
          <div className="flex flex-wrap items-center justify-between gap-2 text-xs text-[var(--cms-muted-fg)] tabular-nums">
            <span>
              当前展示 <strong className="text-[var(--cms-fg)] font-medium">{filteredIntegrations.length}</strong> /{' '}
              {integrations.length} 个渠道接入配置
            </span>
            <span>点击行可直接管理 Pixel 三态授权矩阵及审计历史</span>
          </div>
        }
      >
        <table className="cms-table min-w-[960px]" data-density={density}>
          <thead>
            <tr>
              {visibleCols.integration_id && <th scope="col">内部 ID</th>}
              <th scope="col">接入来源与账户</th>
              <th scope="col">契约版本与状态</th>
              {visibleCols.allowed_apps && <th scope="col">允许应用</th>}
              <th scope="col">允许事件类型</th>
              <th scope="col">已授权与自动附加 Pixel</th>
              <th scope="col" className="text-right w-px whitespace-nowrap">
                操作
              </th>
            </tr>
          </thead>
          <tbody className={loading && hasLoadedOnce ? 'opacity-65 transition-opacity' : ''}>
            {!hasLoadedOnce && loading ? (
              <SkeletonRows rows={4} cols={colSpan} />
            ) : filteredIntegrations.length === 0 ? (
              <tr>
                <td colSpan={colSpan} className="py-16 text-center text-[var(--cms-muted-fg)]">
                  {!hasLoadedOnce && loadError
                    ? '加载渠道接入配置失败，请点击上方「重试」或「刷新」重新加载'
                    : integrations.length === 0
                      ? '暂无渠道接入配置，请点击右上角「新建接入方」开始接入'
                      : '未找到符合当前筛选条件的接入方'}
                </td>
              </tr>
            ) : (
              filteredIntegrations.map((integ) => {
                const bindings = integ.bindings ?? [];
                const additionalBindings = bindings.filter((b) => b.additional);
                const authorizedOnlyBindings = bindings.filter((b) => !b.additional);
                const apps = integ.allowed_apps ?? [];
                const events = integ.allowed_events ?? [];

                return (
                  <tr
                    key={integ.id}
                    onClick={() => openIntegrationDrawer(integ, 'config')}
                    className={`cursor-pointer transition-colors ${!integ.enabled ? 'opacity-75' : ''}`}
                  >
                    {visibleCols.integration_id && (
                      <td className="font-mono-code text-[var(--cms-muted-fg)] whitespace-nowrap">
                        #{integ.id}
                      </td>
                    )}

                    <th scope="row" className="text-left font-normal">
                      <div className="space-y-1">
                        <div className="flex flex-wrap items-center gap-1.5">
                          <span className="font-medium text-[var(--cms-fg)]">
                            {sourceLabel(integ.source)}
                          </span>
                          <span className="font-mono-code text-xs text-[var(--cms-muted-fg)]">
                            {integ.account_key}
                          </span>
                        </div>
                        <div className="text-[11px] text-[var(--cms-muted-fg)] font-mono-code">
                          {integ.source} · #{integ.id}
                        </div>
                      </div>
                    </th>

                    <td>
                      <div className="space-y-1">
                        <div className="flex flex-wrap items-center gap-1.5">
                          <ConversionStatusDot code={integ.enabled ? 'enabled' : 'disabled'} />
                        </div>
                        <div>
                          <span className="badge badge-gray font-mono-code">
                            {integ.contract_version}
                          </span>
                        </div>
                      </div>
                    </td>

                    {visibleCols.allowed_apps && (
                      <td>
                        {apps.length === 0 ? (
                          <span className="text-[var(--cms-muted-fg)]">不限应用</span>
                        ) : (
                          <div className="flex flex-wrap gap-1 max-w-[180px]">
                            {apps.map((app) => (
                              <span key={app} className="badge badge-gray font-mono-code">
                                App {app}
                              </span>
                            ))}
                          </div>
                        )}
                      </td>
                    )}

                    <td>
                      {events.length === 0 ? (
                        <span className="text-[var(--cms-muted-fg)]">未勾选事件</span>
                      ) : (
                        <div className="flex flex-wrap gap-1 max-w-[230px]">
                          {events.map((ev) => (
                            <span
                              key={ev}
                              className="badge badge-gray"
                              title={ev}
                            >
                              {sourceEventLabel(integ.source, ev)}
                            </span>
                          ))}
                        </div>
                      )}
                    </td>

                    <td>
                      {bindings.length === 0 ? (
                        <span className="text-[var(--cms-warning)]">未授权任何 Pixel</span>
                      ) : (
                        <div className="space-y-1.5">
                          {additionalBindings.length > 0 && (
                            <div className="flex flex-wrap items-center gap-1">
                              {additionalBindings.map((b) => {
                                const dest = destinationMap.get(b.destination_id);
                                const isRetired = b.retired || Boolean(dest?.retired_at);
                                return (
                                  <span
                                    key={b.destination_id}
                                    onClick={(e) => e.stopPropagation()}
                                    className={`badge ${
                                      isRetired ? 'badge-red' : 'badge-blue'
                                    } inline-flex items-center gap-1`}
                                    title={
                                      dest?.name
                                        ? `${dest.name} (${b.pixel_id}) · 自动附加双投`
                                        : `${b.pixel_id} · 自动附加双投`
                                    }
                                  >
                                    <span className="font-mono-code">{b.pixel_id}</span>
                                    <span>{isRetired ? '自动附加 · 已退役' : '自动附加'}</span>
                                  </span>
                                );
                              })}
                            </div>
                          )}

                          {authorizedOnlyBindings.length > 0 && (
                            <div className="flex flex-wrap items-center gap-1">
                              {authorizedOnlyBindings.map((b) => {
                                const dest = destinationMap.get(b.destination_id);
                                const isRetired = b.retired || Boolean(dest?.retired_at);
                                return (
                                  <span
                                    key={b.destination_id}
                                    onClick={(e) => e.stopPropagation()}
                                    className={`badge ${
                                      isRetired ? 'badge-red' : 'badge-gray'
                                    } inline-flex items-center gap-1`}
                                    title={
                                      dest?.name
                                        ? `${dest.name} (${b.pixel_id}) · 仅授权`
                                        : `${b.pixel_id} · 仅授权`
                                    }
                                  >
                                    <CopyableText value={b.pixel_id} label="Pixel ID" />
                                    {isRetired && <span>(已退役)</span>}
                                  </span>
                                );
                              })}
                            </div>
                          )}
                        </div>
                      )}
                    </td>

                    <td className="text-right whitespace-nowrap" onClick={(e) => e.stopPropagation()}>
                      <div className="inline-flex items-center justify-end gap-1 whitespace-nowrap">
                        <button
                          type="button"
                          onClick={() => openIntegrationDrawer(integ, 'audit')}
                          className="btn-ghost text-xs whitespace-nowrap inline-flex items-center gap-1"
                        >
                          <History size={12} aria-hidden="true" />
                          <span>审计</span>
                        </button>
                        <button
                          type="button"
                          onClick={() => openIntegrationDrawer(integ, 'config')}
                          className="btn-detail whitespace-nowrap"
                        >
                          {canWrite ? '路由与配置' : '查看详情'}
                        </button>
                      </div>
                    </td>
                  </tr>
                );
              })
            )}
          </tbody>
        </table>
      </TableRegion>

      <IntegrationConfigDrawer
        open={drawerOpen}
        editingIntegration={editingIntegration}
        initialTab={drawerTab}
        destinations={destinations}
        canWrite={canWrite}
        onClose={() => setDrawerOpen(false)}
        onSuccess={async (msg) => {
          setDrawerOpen(false);
          window.showToast?.(msg, 'success');
          await onRefresh();
        }}
      />
    </div>
  );
}
