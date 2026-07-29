import React, { useState } from 'react';
import type { OverviewResult } from '../../types/meta';
import {
  ComposedChart,
  Area,
  Line,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  ResponsiveContainer,
  Legend
} from 'recharts';
import { DollarSign, TrendingUp, ShoppingBag, UserCheck, Filter, ChevronDown } from 'lucide-react';

interface Props {
  overview: OverviewResult | null;
  loading: boolean;
}

export const MetricsOverview: React.FC<Props> = ({ overview, loading }) => {
  // 独立控制每条曲线的显隐状态（反选逻辑）
  const [visibleSeries, setVisibleSeries] = useState<{ [key: string]: boolean }>({
    spend: true,
    revenue: true,
    roas: true,
  });

  const handleLegendClick = (e: any) => {
    const key = e.dataKey as string;
    if (!key) return;

    setVisibleSeries((prev) => ({
      ...prev,
      [key]: !prev[key],
    }));
  };

  if (loading || !overview) {
    return (
      <div className="grid grid-cols-1 md:grid-cols-4 gap-4 mb-8">
        {[1, 2, 3, 4].map((i) => (
          <div key={i} className="glass-panel p-5 rounded-xl animate-pulse h-28 bg-slate-100 dark:bg-slate-800"></div>
        ))}
      </div>
    );
  }

  const dailyTrend = overview.daily_trend || [];
  const totalSpend = overview.total_spend || 0;
  const totalRevenue = overview.total_revenue || 0;
  const averageRoas = overview.average_roas || 0;
  const totalImpressions = overview.total_impressions || 0;
  const totalReach = overview.total_reach || 0;
  const totalClicks = overview.total_clicks || 0;
  const totalLinkClicks = overview.total_link_clicks || 0;
  const totalLandingPageViews = overview.total_landing_page_views || 0;
  const totalRegistration = overview.total_registration || 0;
  const totalPurchases = overview.total_purchases || 0;

  // 1. CTR (展示 ➔ 点击) & CPC
  const ctr = totalImpressions > 0 ? (totalClicks / totalImpressions) * 100 : 0;
  const avgCPC = totalClicks > 0 ? totalSpend / totalClicks : 0;

  // 2. 网页到达率 (链接点击 ➔ 落地页) & CPLPV
  const baseLinkClicks = totalLinkClicks > 0 ? totalLinkClicks : totalClicks;
  const lpvRate = baseLinkClicks > 0 ? (totalLandingPageViews / baseLinkClicks) * 100 : 0;
  const costPerLPV = totalLandingPageViews > 0 ? totalSpend / totalLandingPageViews : 0;

  // 3. 注册率 (点击 ➔ 注册) & CPR
  const avgCPR = totalRegistration > 0 ? totalSpend / totalRegistration : 0;
  const regRate = totalClicks > 0 ? (totalRegistration / totalClicks) * 100 : 0;

  // 4. 下单转化率 (点击 ➔ 购买) & CPA
  const avgCPA = totalPurchases > 0 ? totalSpend / totalPurchases : 0;
  const purchaseRate = totalClicks > 0 ? (totalPurchases / totalClicks) * 100 : 0;

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: '20px' }}>
      {/* KPI 卡片组 */}
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(220px, 1fr))', gap: '16px' }}>
        {/* 卡片 1: 总消耗 */}
        <div className="glass-panel" style={{ padding: '16px 20px', borderLeft: '4px solid hsl(var(--primary))' }}>
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start' }}>
            <div>
              <p style={{ fontSize: '0.75rem', color: 'hsl(var(--text-secondary))', margin: 0, fontWeight: 500 }}>总消耗 (Spend)</p>
              <h3 style={{ fontSize: '1.5rem', fontWeight: 700, margin: '4px 0 0 0', color: 'hsl(var(--text-primary))' }}>
                ${totalSpend.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}
              </h3>
            </div>
            <div style={{ padding: '8px', backgroundColor: 'hsl(var(--primary) / 0.1)', color: 'hsl(var(--primary))', borderRadius: '8px', display: 'flex' }}>
              <DollarSign size={20} />
            </div>
          </div>
          <div style={{ marginTop: '12px', fontSize: '0.75rem', color: 'hsl(var(--text-muted))' }}>
            触达人数: <strong style={{ color: 'hsl(var(--text-primary))' }}>{totalReach.toLocaleString()}</strong>
          </div>
        </div>

        {/* 卡片 2: 销售总额 & ROAS */}
        <div className="glass-panel" style={{ padding: '16px 20px', borderLeft: '4px solid hsl(var(--accent-green))' }}>
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start' }}>
            <div>
              <p style={{ fontSize: '0.75rem', color: 'hsl(var(--text-secondary))', margin: 0, fontWeight: 500 }}>销售总额 (Revenue)</p>
              <h3 style={{ fontSize: '1.5rem', fontWeight: 700, margin: '4px 0 0 0', color: 'hsl(var(--accent-green))' }}>
                ${totalRevenue.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}
              </h3>
            </div>
            <div style={{ padding: '8px', backgroundColor: 'hsl(var(--accent-green) / 0.1)', color: 'hsl(var(--accent-green))', borderRadius: '8px', display: 'flex' }}>
              <TrendingUp size={20} />
            </div>
          </div>
          <div style={{ marginTop: '12px', fontSize: '0.75rem', color: 'hsl(var(--text-muted))', display: 'flex', justifyContent: 'space-between' }}>
            <span>平均 ROAS: <strong style={{ color: 'hsl(var(--accent-green))', fontWeight: 700 }}>{averageRoas.toFixed(2)}x</strong></span>
          </div>
        </div>

        {/* 卡片 3: 完成注册数 & 注册成本 CPR */}
        <div className="glass-panel" style={{ padding: '16px 20px', borderLeft: '4px solid hsl(var(--accent-orange))' }}>
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start' }}>
            <div>
              <p style={{ fontSize: '0.75rem', color: 'hsl(var(--text-secondary))', margin: 0, fontWeight: 500 }}>完成注册 (Registration)</p>
              <h3 style={{ fontSize: '1.5rem', fontWeight: 700, margin: '4px 0 0 0', color: 'hsl(var(--accent-orange))' }}>
                {totalRegistration.toLocaleString()} 次
              </h3>
            </div>
            <div style={{ padding: '8px', backgroundColor: 'hsl(var(--accent-orange) / 0.1)', color: 'hsl(var(--accent-orange))', borderRadius: '8px', display: 'flex' }}>
              <UserCheck size={20} />
            </div>
          </div>
          <div style={{ marginTop: '12px', fontSize: '0.75rem', color: 'hsl(var(--text-muted))', display: 'flex', justifyContent: 'space-between' }}>
            <span>注册单价(CPR): <strong style={{ color: 'hsl(var(--accent-orange))' }}>${avgCPR.toFixed(2)}</strong></span>
            <span>注册转化率: <strong style={{ color: 'hsl(var(--accent-orange))' }}>{regRate.toFixed(1)}%</strong></span>
          </div>
        </div>

        {/* 卡片 4: 购买次数 & CPA 成本 */}
        <div className="glass-panel" style={{ padding: '16px 20px', borderLeft: '4px solid #a855f7' }}>
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start' }}>
            <div>
              <p style={{ fontSize: '0.75rem', color: 'hsl(var(--text-secondary))', margin: 0, fontWeight: 500 }}>成功购买 (Purchases)</p>
              <h3 style={{ fontSize: '1.5rem', fontWeight: 700, margin: '4px 0 0 0', color: '#a855f7' }}>
                {totalPurchases.toLocaleString()} 单
              </h3>
            </div>
            <div style={{ padding: '8px', backgroundColor: 'rgba(168, 85, 247, 0.1)', color: '#a855f7', borderRadius: '8px', display: 'flex' }}>
              <ShoppingBag size={20} />
            </div>
          </div>
          <div style={{ marginTop: '12px', fontSize: '0.75rem', color: 'hsl(var(--text-muted))', display: 'flex', justifyContent: 'space-between' }}>
            <span>平均 CPA: <strong style={{ color: '#a855f7' }}>${avgCPA.toFixed(2)}/单</strong></span>
            <span>下单转化率: <strong style={{ color: '#a855f7' }}>{purchaseRate.toFixed(1)}%</strong></span>
          </div>
        </div>
      </div>

      {/* 图表与全链路分析区 */}
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(320px, 1fr))', gap: '20px' }}>
        {/* 投放与收益趋势 */}
        <div className="glass-panel" style={{ padding: '20px', gridColumn: 'span 2 / span 2', minWidth: 0 }}>
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: '16px' }}>
            <h4 style={{ fontSize: '0.875rem', fontWeight: 700, margin: 0, color: 'hsl(var(--text-primary))', display: 'flex', alignItems: 'center', gap: '8px' }}>
              <TrendingUp style={{ color: 'hsl(var(--primary))' }} size={18} />
              投放与收益趋势
            </h4>
            <span style={{ fontSize: '0.75rem', color: 'hsl(var(--text-muted))' }}>
              点击图例可隐藏/显示对应曲线
            </span>
          </div>
          <div style={{ height: '260px', width: '100%' }}>
            <ResponsiveContainer width="100%" height="100%">
              <ComposedChart data={dailyTrend} margin={{ top: 10, right: 10, left: -10, bottom: 0 }}>
                <defs>
                  <linearGradient id="spendGrad" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="5%" stopColor="#3b82f6" stopOpacity={0.35} />
                    <stop offset="95%" stopColor="#3b82f6" stopOpacity={0.02} />
                  </linearGradient>
                  <linearGradient id="revGrad" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="5%" stopColor="#10b981" stopOpacity={0.35} />
                    <stop offset="95%" stopColor="#10b981" stopOpacity={0.02} />
                  </linearGradient>
                </defs>
                <CartesianGrid strokeDasharray="3 3" stroke="hsl(var(--border))" />
                <XAxis dataKey="stat_date" stroke="hsl(var(--text-muted))" tick={{ fontSize: 10 }} />
                
                {/* 左 Y 轴：金额 ($) */}
                <YAxis
                  yAxisId="left"
                  orientation="left"
                  stroke="hsl(var(--text-muted))"
                  tick={{ fontSize: 10 }}
                  tickFormatter={(val: any) => `$${val}`}
                />

                {/* 右 Y 轴：ROAS */}
                <YAxis
                  yAxisId="right"
                  orientation="right"
                  stroke="#a855f7"
                  tick={{ fontSize: 10 }}
                  tickFormatter={(val: any) => `${Number(val).toFixed(2)}x`}
                />

                <Tooltip
                  contentStyle={{
                    backgroundColor: 'hsl(var(--bg-surface))',
                    borderColor: 'hsl(var(--border))',
                    color: 'hsl(var(--text-primary))',
                    borderRadius: '8px',
                    fontSize: '12px',
                    boxShadow: 'var(--shadow-md)'
                  }}
                  formatter={(value: any, name: any) => {
                    if (name.includes('ROAS')) {
                      return [`${Number(value).toFixed(2)}x`, 'ROAS'];
                    }
                    return [`$${Number(value).toFixed(2)}`, name];
                  }}
                />
                <Legend
                  onClick={handleLegendClick}
                  wrapperStyle={{ fontSize: '11px', paddingTop: '8px', cursor: 'pointer' }}
                />
                
                <Area
                  yAxisId="left"
                  type="monotone"
                  dataKey="spend"
                  name="每日消耗 ($)"
                  stroke="#3b82f6"
                  strokeWidth={2}
                  fillOpacity={1}
                  fill="url(#spendGrad)"
                  hide={!visibleSeries.spend}
                />
                <Area
                  yAxisId="left"
                  type="monotone"
                  dataKey="revenue"
                  name="转化收益 ($)"
                  stroke="#10b981"
                  strokeWidth={2}
                  fillOpacity={1}
                  fill="url(#revGrad)"
                  hide={!visibleSeries.revenue}
                />
                <Line
                  yAxisId="right"
                  type="monotone"
                  dataKey="roas"
                  name="ROAS"
                  stroke="#a855f7"
                  strokeWidth={2.5}
                  dot={false}
                  activeDot={{ r: 4, stroke: '#ffffff', strokeWidth: 2 }}
                  hide={!visibleSeries.roas}
                />
              </ComposedChart>
            </ResponsiveContainer>
          </div>
        </div>

        {/* 转化漏斗 */}
        <div className="glass-panel" style={{ padding: '20px', display: 'flex', flexDirection: 'column', justifyContent: 'space-between' }}>
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: '12px' }}>
            <h4 style={{ fontSize: '0.875rem', fontWeight: 700, margin: 0, color: 'hsl(var(--text-primary))', display: 'flex', alignItems: 'center', gap: '8px' }}>
              <Filter style={{ color: 'hsl(var(--accent-orange))' }} size={18} />
              全链路转化漏斗
            </h4>
          </div>

          {/* 梯形视觉漏斗叠层 */}
          <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', gap: '6px', margin: 'auto 0', width: '100%' }}>
            {/* Step 1: 展示 ➔ 点击 (宽度 100%) */}
            <div style={{ width: '100%', padding: '10px 12px', borderRadius: '8px', border: '1px solid hsl(var(--primary) / 0.3)', backgroundColor: 'hsl(var(--primary) / 0.06)' }}>
              <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', fontSize: '0.75rem', marginBottom: '4px' }}>
                <span style={{ fontWeight: 600, color: 'hsl(var(--primary))', display: 'flex', alignItems: 'center', gap: '6px' }}>
                  <span style={{ width: '8px', height: '8px', borderRadius: '50%', backgroundColor: 'hsl(var(--primary))', display: 'inline-block' }}></span>
                  展示 ➔ 点击 (点击率)
                </span>
                <span style={{ fontWeight: 700, color: 'hsl(var(--primary))' }}>
                  {ctr.toFixed(2)}%
                </span>
              </div>
              <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', fontSize: '0.7rem', color: 'hsl(var(--text-secondary))' }}>
                <span>点击: {totalClicks.toLocaleString()}</span>
                <span>CPC: ${avgCPC.toFixed(2)}</span>
              </div>
            </div>

            <ChevronDown size={14} style={{ color: 'hsl(var(--text-muted))' }} />

            {/* Step 2: 链接点击 ➔ 落地页 (宽度 92%) */}
            <div style={{ width: '92%', padding: '10px 12px', borderRadius: '8px', border: '1px solid hsl(200 85% 45% / 0.3)', backgroundColor: 'hsl(200 85% 45% / 0.06)' }}>
              <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', fontSize: '0.75rem', marginBottom: '4px' }}>
                <span style={{ fontWeight: 600, color: 'hsl(200 85% 45%)', display: 'flex', alignItems: 'center', gap: '6px' }}>
                  <span style={{ width: '8px', height: '8px', borderRadius: '50%', backgroundColor: 'hsl(200 85% 45%)', display: 'inline-block' }}></span>
                  链接点击 ➔ 落地页 (到达率)
                </span>
                <span style={{ fontWeight: 700, color: 'hsl(200 85% 45%)' }}>
                  {lpvRate.toFixed(1)}%
                </span>
              </div>
              <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', fontSize: '0.7rem', color: 'hsl(var(--text-secondary))' }}>
                <span>落地页: {totalLandingPageViews.toLocaleString()}</span>
                <span>单价: ${costPerLPV.toFixed(2)}</span>
              </div>
            </div>

            <ChevronDown size={14} style={{ color: 'hsl(var(--text-muted))' }} />

            {/* Step 3: 点击 ➔ 完成注册 (宽度 84%) */}
            <div style={{ width: '84%', padding: '10px 12px', borderRadius: '8px', border: '1px solid hsl(var(--accent-orange) / 0.3)', backgroundColor: 'hsl(var(--accent-orange) / 0.06)' }}>
              <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', fontSize: '0.75rem', marginBottom: '4px' }}>
                <span style={{ fontWeight: 600, color: 'hsl(var(--accent-orange))', display: 'flex', alignItems: 'center', gap: '6px' }}>
                  <span style={{ width: '8px', height: '8px', borderRadius: '50%', backgroundColor: 'hsl(var(--accent-orange))', display: 'inline-block' }}></span>
                  点击 ➔ 完成注册 (注册率)
                </span>
                <span style={{ fontWeight: 700, color: 'hsl(var(--accent-orange))' }}>
                  {regRate.toFixed(1)}%
                </span>
              </div>
              <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', fontSize: '0.7rem', color: 'hsl(var(--text-secondary))' }}>
                <span>注册: {totalRegistration.toLocaleString()}</span>
                <span>CPR: ${avgCPR.toFixed(2)}</span>
              </div>
            </div>

            <ChevronDown size={14} style={{ color: 'hsl(var(--text-muted))' }} />

            {/* Step 4: 点击 ➔ 成功购买 (宽度 76%) */}
            <div style={{ width: '76%', padding: '10px 12px', borderRadius: '8px', border: '1px solid rgba(168, 85, 247, 0.3)', backgroundColor: 'rgba(168, 85, 247, 0.06)' }}>
              <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', fontSize: '0.75rem', marginBottom: '4px' }}>
                <span style={{ fontWeight: 600, color: '#a855f7', display: 'flex', alignItems: 'center', gap: '6px' }}>
                  <span style={{ width: '8px', height: '8px', borderRadius: '50%', backgroundColor: '#a855f7', display: 'inline-block' }}></span>
                  点击 ➔ 成功购买 (转化率)
                </span>
                <span style={{ fontWeight: 700, color: '#a855f7' }}>
                  {purchaseRate.toFixed(1)}%
                </span>
              </div>
              <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', fontSize: '0.7rem', color: 'hsl(var(--text-secondary))' }}>
                <span>购买: {totalPurchases.toLocaleString()}</span>
                <span>CPA: ${avgCPA.toFixed(2)}</span>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
};
