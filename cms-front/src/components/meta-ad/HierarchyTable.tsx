import React, { useState } from 'react';
import type { HierarchyNode } from '../../types/meta';
import { ChevronRight, ChevronDown, Search, Filter } from 'lucide-react';

interface Props {
  nodes: HierarchyNode[];
  loading: boolean;
}

type TabMode = 'funnel' | 'reach' | 'roas';
type StatusFilter = 'ALL' | 'ACTIVE' | 'PAUSED';

export const HierarchyTable: React.FC<Props> = ({ nodes, loading }) => {
  const [expanded, setExpanded] = useState<Record<string, boolean>>({});
  const [activeTab, setActiveTab] = useState<TabMode>('funnel');
  const [searchTerm, setSearchTerm] = useState('');
  const [statusFilter, setStatusFilter] = useState<StatusFilter>('ALL');

  const toggleExpand = (id: string, currentExpanded: boolean) => {
    setExpanded((prev) => ({ ...prev, [id]: !currentExpanded }));
  };

  const getLevelBadge = (level: string) => {
    switch (level) {
      case 'account':
        return <span className="badge badge-violet" style={{ fontSize: '10px' }}>账户</span>;
      case 'campaign':
        return <span className="badge" style={{ fontSize: '10px', backgroundColor: 'rgba(168, 85, 247, 0.1)', color: '#a855f7', borderColor: 'rgba(168, 85, 247, 0.2)' }}>系列</span>;
      case 'adset':
        return <span className="badge badge-orange" style={{ fontSize: '10px' }}>组</span>;
      case 'ad':
        return <span className="badge badge-green" style={{ fontSize: '10px' }}>广告</span>;
      default:
        return null;
    }
  };

  const checkStatusMatch = (node: HierarchyNode): boolean => {
    if (statusFilter === 'ALL') return true;
    if (node.status === statusFilter) return true;
    return node.children?.some((c) => checkStatusMatch(c)) || false;
  };

  const sortNodesBySpend = (list: HierarchyNode[]): HierarchyNode[] => {
    return [...list]
      .sort((a, b) => (b.metrics?.spend || 0) - (a.metrics?.spend || 0))
      .map((item) => ({
        ...item,
        children: item.children ? sortNodesBySpend(item.children) : [],
      }));
  };

  const renderRow = (node: HierarchyNode, depth = 0) => {
    if (!checkStatusMatch(node)) return null;

    const hasChildren = node.children && node.children.length > 0;
    const isExpanded = expanded[node.id] ?? false;

    if (searchTerm && !node.name.toLowerCase().includes(searchTerm.toLowerCase())) {
      const childMatch = node.children?.some((c) => c.name.toLowerCase().includes(searchTerm.toLowerCase()));
      if (!childMatch) return null;
    }

    const m = node.metrics;
    const regCount = (m as any).complete_registration_count || 0;
    const costPerReg = regCount > 0 ? m.spend / regCount : 0;
    const regRate = m.clicks > 0 ? (regCount / m.clicks) * 100 : 0;
    const purchaseRate = m.clicks > 0 ? (m.purchase_count / m.clicks) * 100 : 0;

    return (
      <React.Fragment key={node.id}>
        <tr style={{ borderBottom: '1px solid hsl(var(--border))', backgroundColor: depth === 0 ? 'hsl(var(--bg-card))' : 'transparent', fontWeight: depth === 0 ? 600 : 400 }}>
          <td style={{ padding: '10px 16px', whiteSpace: 'nowrap' }}>
            <div style={{ marginLeft: `${depth * 20}px`, display: 'flex', alignItems: 'center', gap: '8px' }}>
              {hasChildren ? (
                <button
                  onClick={() => toggleExpand(node.id, isExpanded)}
                  style={{ background: 'none', border: 'none', cursor: 'pointer', padding: '2px', display: 'flex', alignItems: 'center', color: 'hsl(var(--text-secondary))' }}
                >
                  {isExpanded ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
                </button>
              ) : (
                <span style={{ width: '18px', display: 'inline-block' }}></span>
              )}
              {getLevelBadge(node.level)}
              <span style={{ fontSize: '0.75rem', fontWeight: 600, color: 'hsl(var(--text-primary))', maxWidth: '280px', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }} title={node.name}>
                {node.name}
              </span>
            </div>
          </td>

          <td style={{ padding: '10px 16px', textAlign: 'center', whiteSpace: 'nowrap' }}>
            <span
              className={node.status === 'ACTIVE' ? 'badge badge-green' : 'badge'}
              style={{ fontSize: '10px' }}
            >
              {node.status}
            </span>
          </td>

          {activeTab === 'roas' && (
            <>
              <td style={{ padding: '10px 16px', textAlign: 'right', fontWeight: 600, color: 'hsl(var(--primary))', whiteSpace: 'nowrap' }}>${m.spend.toFixed(2)}</td>
              <td style={{ padding: '10px 16px', textAlign: 'right', fontWeight: 600, color: 'hsl(var(--accent-green))', whiteSpace: 'nowrap' }}>${m.purchase_value.toFixed(2)}</td>
              <td style={{ padding: '10px 16px', textAlign: 'right', fontWeight: 700, color: '#a855f7', whiteSpace: 'nowrap' }}>
                {m.purchase_roas.toFixed(2)}x
              </td>
            </>
          )}

          {activeTab === 'reach' && (
            <>
              <td style={{ padding: '10px 16px', textAlign: 'right', whiteSpace: 'nowrap', color: 'hsl(var(--text-primary))' }}>{m.impressions.toLocaleString()}</td>
              <td style={{ padding: '10px 16px', textAlign: 'right', whiteSpace: 'nowrap', color: 'hsl(var(--text-secondary))' }}>{m.reach.toLocaleString()}</td>
              <td style={{ padding: '10px 16px', textAlign: 'right', whiteSpace: 'nowrap', color: 'hsl(var(--text-secondary))' }}>{m.frequency.toFixed(2)}</td>
              <td style={{ padding: '10px 16px', textAlign: 'right', whiteSpace: 'nowrap', color: 'hsl(var(--text-secondary))' }}>${m.cpm.toFixed(2)}</td>
              <td style={{ padding: '10px 16px', textAlign: 'right', whiteSpace: 'nowrap', color: 'hsl(var(--text-secondary))' }}>
                <span>{m.clicks.toLocaleString()}</span>
                <span style={{ fontSize: '0.65rem', color: 'hsl(var(--text-muted))', marginLeft: '4px' }}>(${m.cpc.toFixed(2)})</span>
              </td>
              <td style={{ padding: '10px 16px', textAlign: 'right', whiteSpace: 'nowrap', color: 'hsl(var(--text-secondary))' }}>{m.ctr.toFixed(2)}%</td>
              <td style={{ padding: '10px 16px', textAlign: 'right', whiteSpace: 'nowrap', color: 'hsl(var(--text-secondary))' }}>
                <span>{m.link_clicks.toLocaleString()}</span>
                <span style={{ fontSize: '0.65rem', color: 'hsl(var(--text-muted))', marginLeft: '4px' }}>(${m.cost_per_link_click.toFixed(2)})</span>
              </td>
              <td style={{ padding: '10px 16px', textAlign: 'right', whiteSpace: 'nowrap', color: 'hsl(var(--text-secondary))' }}>
                {m.link_ctr.toFixed(2)}%
              </td>
            </>
          )}

          {activeTab === 'funnel' && (
            <>
              <td style={{ padding: '10px 16px', textAlign: 'right', fontWeight: 600, color: 'hsl(var(--primary))', whiteSpace: 'nowrap' }}>${m.spend.toFixed(2)}</td>
              <td style={{ padding: '10px 16px', textAlign: 'right', whiteSpace: 'nowrap', color: 'hsl(var(--text-secondary))' }}>
                <span>{m.clicks.toLocaleString()}</span>
                <span style={{ fontSize: '0.65rem', color: 'hsl(var(--text-muted))', marginLeft: '4px' }}>(${m.cpc.toFixed(2)})</span>
              </td>
              <td style={{ padding: '10px 16px', textAlign: 'right', whiteSpace: 'nowrap', color: 'hsl(var(--text-secondary))' }}>
                {m.ctr.toFixed(2)}%
              </td>
              <td style={{ padding: '10px 16px', textAlign: 'right', whiteSpace: 'nowrap', color: 'hsl(var(--text-secondary))' }}>
                <span>{m.landing_page_views.toLocaleString()}</span>
                <span style={{ fontSize: '0.65rem', color: 'hsl(var(--text-muted))', marginLeft: '4px' }}>(${m.cost_per_landing_page_view.toFixed(2)})</span>
              </td>
              <td style={{ padding: '10px 16px', textAlign: 'right', whiteSpace: 'nowrap', color: 'hsl(var(--text-secondary))' }}>
                <span>{regCount.toLocaleString()}</span>
                <span style={{ fontSize: '0.65rem', color: 'hsl(var(--text-muted))', marginLeft: '4px' }}>(${costPerReg.toFixed(2)})</span>
              </td>
              <td style={{ padding: '10px 16px', textAlign: 'right', whiteSpace: 'nowrap', color: 'hsl(var(--text-secondary))' }}>
                {regRate.toFixed(1)}%
              </td>
              <td style={{ padding: '10px 16px', textAlign: 'right', whiteSpace: 'nowrap', color: 'hsl(var(--text-secondary))' }}>
                <span>{m.purchase_count.toLocaleString()}</span>
                <span style={{ fontSize: '0.65rem', color: 'hsl(var(--text-muted))', marginLeft: '4px' }}>(${m.cost_per_purchase.toFixed(2)})</span>
              </td>
              <td style={{ padding: '10px 16px', textAlign: 'right', whiteSpace: 'nowrap', color: 'hsl(var(--text-secondary))' }}>
                {purchaseRate.toFixed(1)}%
              </td>
              <td style={{ padding: '10px 16px', textAlign: 'right', fontWeight: 600, color: 'hsl(var(--accent-green))', whiteSpace: 'nowrap' }}>
                ${m.purchase_value.toFixed(2)}
              </td>
              <td style={{ padding: '10px 16px', textAlign: 'right', fontWeight: 700, color: '#a855f7', whiteSpace: 'nowrap' }}>
                {m.purchase_roas.toFixed(2)}x
              </td>
            </>
          )}
        </tr>

        {isExpanded && node.children && node.children.map((child) => renderRow(child, depth + 1))}
      </React.Fragment>
    );
  };

  const sortedNodes = sortNodesBySpend(nodes);

  return (
    <div className="glass-panel" style={{ overflow: 'hidden' }}>
      {/* 栏头控制栏 */}
      <div style={{ padding: '14px 20px', borderBottom: '1px solid hsl(var(--border))', display: 'flex', alignItems: 'center', justifyContent: 'space-between', flexWrap: 'wrap', gap: '16px' }}>
        <div style={{ display: 'flex', backgroundColor: 'hsl(var(--bg-base))', padding: '3px', borderRadius: '8px', border: '1px solid hsl(var(--border))', gap: '4px' }}>
          <button
            onClick={() => setActiveTab('funnel')}
            style={{
              padding: '6px 14px',
              fontSize: '0.75rem',
              fontWeight: 600,
              borderRadius: '6px',
              border: 'none',
              cursor: 'pointer',
              backgroundColor: activeTab === 'funnel' ? 'hsl(var(--primary))' : 'transparent',
              color: activeTab === 'funnel' ? '#ffffff' : 'hsl(var(--text-secondary))',
              transition: 'all 0.15s ease'
            }}
          >
            全链路转化漏斗
          </button>
          <button
            onClick={() => setActiveTab('reach')}
            style={{
              padding: '6px 14px',
              fontSize: '0.75rem',
              fontWeight: 600,
              borderRadius: '6px',
              border: 'none',
              cursor: 'pointer',
              backgroundColor: activeTab === 'reach' ? 'hsl(var(--primary))' : 'transparent',
              color: activeTab === 'reach' ? '#ffffff' : 'hsl(var(--text-secondary))',
              transition: 'all 0.15s ease'
            }}
          >
            触达与点击明细
          </button>
          <button
            onClick={() => setActiveTab('roas')}
            style={{
              padding: '6px 14px',
              fontSize: '0.75rem',
              fontWeight: 600,
              borderRadius: '6px',
              border: 'none',
              cursor: 'pointer',
              backgroundColor: activeTab === 'roas' ? 'hsl(var(--primary))' : 'transparent',
              color: activeTab === 'roas' ? '#ffffff' : 'hsl(var(--text-secondary))',
              transition: 'all 0.15s ease'
            }}
          >
            ROAS 专属视图
          </button>
        </div>

        <div style={{ display: 'flex', alignItems: 'center', gap: '12px', flexWrap: 'wrap' }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: '6px' }}>
            <Filter size={14} style={{ color: 'hsl(var(--text-muted))' }} />
            <select
              value={statusFilter}
              onChange={(e) => setStatusFilter(e.target.value as StatusFilter)}
              className="custom-select"
              style={{ height: '34px', fontSize: '0.75rem', padding: '0 32px 0 10px' }}
            >
              <option value="ALL">全部状态 (ALL)</option>
              <option value="ACTIVE">仅启用 (ACTIVE)</option>
              <option value="PAUSED">仅暂停 (PAUSED)</option>
            </select>
          </div>

          <div style={{ position: 'relative', width: '220px' }}>
            <Search size={14} style={{ position: 'absolute', left: '10px', top: '50%', transform: 'translateY(-50%)', color: 'hsl(var(--text-muted))' }} />
            <input
              type="text"
              placeholder="搜索账户/系列/组/广告..."
              value={searchTerm}
              onChange={(e) => setSearchTerm(e.target.value)}
              className="input-field"
              style={{ height: '34px', paddingLeft: '32px', fontSize: '0.75rem' }}
            />
          </div>
        </div>
      </div>

      <div style={{ overflowX: 'auto' }}>
        <table style={{ width: '100%', borderCollapse: 'collapse', textAlign: 'left', fontSize: '0.75rem' }}>
          <thead>
            <tr style={{ borderBottom: '1px solid hsl(var(--border))', backgroundColor: 'hsl(var(--bg-base))', color: 'hsl(var(--text-secondary))' }}>
              <th style={{ padding: '10px 16px', fontWeight: 600, width: activeTab === 'roas' ? '40%' : 'auto' }}>层级结构 / 名称</th>
              <th style={{ padding: '10px 16px', fontWeight: 600, textAlign: 'center', width: activeTab === 'roas' ? '100px' : 'auto' }}>状态</th>

              {activeTab === 'roas' && (
                <>
                  <th style={{ padding: '10px 16px', fontWeight: 600, textAlign: 'right', whiteSpace: 'nowrap' }}>消耗 ($) ↓</th>
                  <th style={{ padding: '10px 16px', fontWeight: 600, textAlign: 'right', whiteSpace: 'nowrap' }}>销售额 ($)</th>
                  <th style={{ padding: '10px 16px', fontWeight: 600, textAlign: 'right', whiteSpace: 'nowrap' }}>ROAS</th>
                </>
              )}

              {activeTab === 'reach' && (
                <>
                  <th style={{ padding: '10px 16px', fontWeight: 600, textAlign: 'right', whiteSpace: 'nowrap' }}>展示次数</th>
                  <th style={{ padding: '10px 16px', fontWeight: 600, textAlign: 'right', whiteSpace: 'nowrap' }}>触达人数</th>
                  <th style={{ padding: '10px 16px', fontWeight: 600, textAlign: 'right', whiteSpace: 'nowrap' }}>频次</th>
                  <th style={{ padding: '10px 16px', fontWeight: 600, textAlign: 'right', whiteSpace: 'nowrap' }}>CPM</th>
                  <th style={{ padding: '10px 16px', fontWeight: 600, textAlign: 'right', whiteSpace: 'nowrap' }}>点击 (全部)</th>
                  <th style={{ padding: '10px 16px', fontWeight: 600, textAlign: 'right', whiteSpace: 'nowrap' }}>点击率 (全部)</th>
                  <th style={{ padding: '10px 16px', fontWeight: 600, textAlign: 'right', whiteSpace: 'nowrap' }}>链接点击 (CPC)</th>
                  <th style={{ padding: '10px 16px', fontWeight: 600, textAlign: 'right', whiteSpace: 'nowrap' }}>链接点击率</th>
                </>
              )}

              {activeTab === 'funnel' && (
                <>
                  <th style={{ padding: '10px 16px', fontWeight: 600, textAlign: 'right', whiteSpace: 'nowrap' }}>消耗 ($) ↓</th>
                  <th style={{ padding: '10px 16px', fontWeight: 600, textAlign: 'right', whiteSpace: 'nowrap' }}>点击 (CPC)</th>
                  <th style={{ padding: '10px 16px', fontWeight: 600, textAlign: 'right', whiteSpace: 'nowrap' }}>点击率</th>
                  <th style={{ padding: '10px 16px', fontWeight: 600, textAlign: 'right', whiteSpace: 'nowrap' }}>落地页浏览 (单价)</th>
                  <th style={{ padding: '10px 16px', fontWeight: 600, textAlign: 'right', whiteSpace: 'nowrap' }}>注册 (CPR)</th>
                  <th style={{ padding: '10px 16px', fontWeight: 600, textAlign: 'right', whiteSpace: 'nowrap' }}>注册率</th>
                  <th style={{ padding: '10px 16px', fontWeight: 600, textAlign: 'right', whiteSpace: 'nowrap' }}>购买 (CPA)</th>
                  <th style={{ padding: '10px 16px', fontWeight: 600, textAlign: 'right', whiteSpace: 'nowrap' }}>购买率</th>
                  <th style={{ padding: '10px 16px', fontWeight: 600, textAlign: 'right', whiteSpace: 'nowrap' }}>销售额</th>
                  <th style={{ padding: '10px 16px', fontWeight: 600, textAlign: 'right', whiteSpace: 'nowrap' }}>ROAS</th>
                </>
              )}
            </tr>
          </thead>
          <tbody>
            {loading ? (
              <tr>
                <td colSpan={14} style={{ textAlign: 'center', padding: '36px', color: 'hsl(var(--text-muted))' }}>
                  数据加载中...
                </td>
              </tr>
            ) : sortedNodes.length === 0 ? (
              <tr>
                <td colSpan={12} style={{ textAlign: 'center', padding: '36px', color: 'hsl(var(--text-muted))' }}>
                  暂无匹配的广告账户数据
                </td>
              </tr>
            ) : (
              sortedNodes.map((node) => renderRow(node))
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
};
