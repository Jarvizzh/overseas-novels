import { useState, useEffect } from 'react';
import { apiRequest } from '../utils/api';
import type { HierarchyNode, OverviewResult, ConfigData } from '../types/meta';
import { MetricsOverview } from '../components/meta-ad/MetricsOverview';
import { HierarchyTable } from '../components/meta-ad/HierarchyTable';
import { SettingsModal } from '../components/meta-ad/SettingsModal';
import { RefreshCw, Settings, Calendar, BarChart2 } from 'lucide-react';

export default function MetaAdStatsTab() {
  const [overview, setOverview] = useState<OverviewResult | null>(null);
  const [hierarchy, setHierarchy] = useState<HierarchyNode[]>([]);
  const [loading, setLoading] = useState(true);
  const [config, setConfig] = useState<ConfigData | null>(null);
  const [isSettingsOpen, setIsSettingsOpen] = useState(false);
  const [dateRange, setDateRange] = useState<'today' | 'last_7d' | 'last_30d'>('last_7d');

  const getDateParams = (range: string) => {
    const end = new Date();
    const start = new Date();
    if (range === 'today') {
      // today
    } else if (range === 'last_7d') {
      start.setDate(start.getDate() - 7);
    } else if (range === 'last_30d') {
      start.setDate(start.getDate() - 30);
    }
    const startDate = start.toISOString().split('T')[0];
    const endDate = end.toISOString().split('T')[0];
    return { startDate, endDate };
  };

  const loadData = async () => {
    setLoading(true);
    try {
      const { startDate, endDate } = getDateParams(dateRange);
      const [overviewRes, hierarchyRes] = await Promise.all([
        apiRequest('GET', `/meta/overview?start_date=${startDate}&end_date=${endDate}`),
        apiRequest('GET', `/meta/hierarchy?start_date=${startDate}&end_date=${endDate}`),
      ]);
      setOverview(overviewRes.data || null);
      setHierarchy(hierarchyRes.data || []);
    } catch (err: any) {
      if (window.showToast) {
        window.showToast(err.message || '加载 Meta 广告统计数据失败', 'error');
      }
    } finally {
      setLoading(false);
    }
  };

  const loadConfig = async () => {
    try {
      const res = await apiRequest('GET', '/meta/config');
      setConfig(res);
    } catch (err) {
      console.error('Failed to load Meta config', err);
    }
  };

  useEffect(() => {
    loadData();
    loadConfig();
  }, [dateRange]);

  const handleSaveConfig = async (token: string, bmID: string, apiVersion: string) => {
    await apiRequest('POST', '/meta/config', {
      meta_access_token: token,
      meta_business_id: bmID,
      meta_api_version: apiVersion,
    });
    if (window.showToast) {
      window.showToast('配置已保存，已自动拉取近7天 Meta 数据！', 'success');
    }
    loadConfig();
    loadData();
  };

  const handleTriggerSync = async (preset: string) => {
    await apiRequest('POST', '/meta/sync', { date_preset: preset });
    if (window.showToast) {
      window.showToast(`数据同步任务已触发 (${preset})，正在同步中...`, 'info');
    }
    setTimeout(() => {
      loadData();
    }, 3000);
  };

  const handlePurgeData = async () => {
    await apiRequest('POST', '/meta/purge');
    if (window.showToast) {
      window.showToast('数据已成功清空！', 'success');
    }
    loadData();
  };

  return (
    <div className="animate-fade-in" style={{ padding: '20px 24px 32px 24px', display: 'flex', flexDirection: 'column', gap: '20px' }}>
      {/* 头部标题与控制栏 */}
      <div className="glass-panel" style={{ padding: '16px 20px', display: 'flex', justifyContent: 'space-between', alignItems: 'center', flexWrap: 'wrap', gap: '16px' }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: '12px' }}>
          <div style={{ padding: '10px', backgroundColor: 'hsl(var(--primary) / 0.1)', color: 'hsl(var(--primary))', borderRadius: '10px', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
            <BarChart2 size={24} />
          </div>
          <div>
            <h1 style={{ fontSize: '1.25rem', fontWeight: 700, margin: 0, color: 'hsl(var(--text-primary))', display: 'flex', alignItems: 'center', gap: '8px' }}>
              Meta 广告数据洞察与统计
            </h1>
            <p style={{ fontSize: '0.75rem', color: 'hsl(var(--text-secondary))', margin: '2px 0 0 0' }}>
              多账户四级下钻分析、全链路转化漏斗与收益趋势
            </p>
          </div>
        </div>

        <div style={{ display: 'flex', alignItems: 'center', gap: '12px', flexWrap: 'wrap' }}>
          {/* 时间维度切 */}
          <div style={{ display: 'flex', backgroundColor: 'hsl(var(--bg-base))', padding: '3px', borderRadius: '8px', border: '1px solid hsl(var(--border))', gap: '4px' }}>
            <button
              onClick={() => setDateRange('today')}
              style={{
                padding: '6px 12px',
                fontSize: '0.75rem',
                fontWeight: 600,
                borderRadius: '6px',
                border: 'none',
                cursor: 'pointer',
                display: 'flex',
                alignItems: 'center',
                gap: '4px',
                backgroundColor: dateRange === 'today' ? 'hsl(var(--primary))' : 'transparent',
                color: dateRange === 'today' ? '#ffffff' : 'hsl(var(--text-secondary))',
                transition: 'all 0.15s ease'
              }}
            >
              <Calendar size={13} />
              今天
            </button>
            <button
              onClick={() => setDateRange('last_7d')}
              style={{
                padding: '6px 12px',
                fontSize: '0.75rem',
                fontWeight: 600,
                borderRadius: '6px',
                border: 'none',
                cursor: 'pointer',
                display: 'flex',
                alignItems: 'center',
                gap: '4px',
                backgroundColor: dateRange === 'last_7d' ? 'hsl(var(--primary))' : 'transparent',
                color: dateRange === 'last_7d' ? '#ffffff' : 'hsl(var(--text-secondary))',
                transition: 'all 0.15s ease'
              }}
            >
              近 7 天
            </button>
            <button
              onClick={() => setDateRange('last_30d')}
              style={{
                padding: '6px 12px',
                fontSize: '0.75rem',
                fontWeight: 600,
                borderRadius: '6px',
                border: 'none',
                cursor: 'pointer',
                display: 'flex',
                alignItems: 'center',
                gap: '4px',
                backgroundColor: dateRange === 'last_30d' ? 'hsl(var(--primary))' : 'transparent',
                color: dateRange === 'last_30d' ? '#ffffff' : 'hsl(var(--text-secondary))',
                transition: 'all 0.15s ease'
              }}
            >
              近 30 天
            </button>
          </div>

          <button
            onClick={loadData}
            disabled={loading}
            className="btn-secondary"
            style={{ height: '34px', fontSize: '0.75rem', padding: '0 12px', gap: '6px' }}
          >
            <RefreshCw size={14} className={loading ? 'animate-spin' : ''} />
            刷新
          </button>

          <button
            onClick={() => setIsSettingsOpen(true)}
            className="btn-primary"
            style={{ height: '34px', fontSize: '0.75rem', padding: '0 14px', gap: '6px' }}
          >
            <Settings size={14} />
            账户与 API 配置
          </button>
        </div>
      </div>

      {/* 概览大盘组件 */}
      <MetricsOverview overview={overview} loading={loading} />

      {/* 四级树状明细表格 */}
      <HierarchyTable nodes={hierarchy} loading={loading} />

      {/* API 与同步配置弹窗 */}
      <SettingsModal
        isOpen={isSettingsOpen}
        onClose={() => setIsSettingsOpen(false)}
        onSave={handleSaveConfig}
        onTriggerSync={handleTriggerSync}
        onPurge={handlePurgeData}
        config={config}
      />
    </div>
  );
}
