import { useEffect, useState } from 'react';
import { Award, LayoutDashboard, Sparkles } from 'lucide-react';
import { Link } from 'react-router-dom';
import { apiRequest } from '../utils/api';
import { usePermission } from '../context/PermissionContext';
import { TableRegion } from '../components/ui/Primitives';

interface TopNovelRow {
  id: string;
  title: string;
  author: string;
  unlocks: number;
  revenue: number;
}

function PaymentActivityCard() {
  const [activity, setActivity] = useState<{ refund_count: number; chargeback_debit_count: number }>();
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    const controller = new AbortController();
    void apiRequest('GET', '/payments/reconciliation/activity', undefined, false, controller.signal)
      .then((data) => {
        if (!controller.signal.aborted) setActivity(data);
      })
      .catch(() => {
        if (!controller.signal.aborted) setFailed(true);
      });
    return () => controller.abort();
  }, []);

  return (
    <div className="cms-card p-5 space-y-1.5">
      <Link className="btn-detail" to="/payments/reconciliation">
        退款与拒付划扣
      </Link>
      {failed ? (
        <p role="alert" className="text-xs text-[var(--cms-muted-fg)] m-0">
          支付统计暂不可用
        </p>
      ) : activity ? (
        <>
          <dl className="grid grid-cols-2 gap-2">
            <div>
              <dt className="text-xs text-[var(--cms-muted-fg)]">已确认退款</dt>
              <dd className="text-xl font-semibold tabular-nums m-0">{activity.refund_count.toLocaleString()}</dd>
            </div>
            <div>
              <dt className="text-xs text-[var(--cms-muted-fg)]">拒付划扣</dt>
              <dd className="text-xl font-semibold tabular-nums m-0">
                {activity.chargeback_debit_count.toLocaleString()}
              </dd>
            </div>
          </dl>
          <p className="text-xs text-[var(--cms-muted-fg)] m-0">正式环境 USD · 已入库的退款记录与渠道资金流水</p>
        </>
      ) : (
        <p role="status" className="text-xs text-[var(--cms-muted-fg)] m-0">
          正在加载支付统计…
        </p>
      )}
    </div>
  );
}

export default function DashboardTab() {
  const { admin, canRead } = usePermission();
  const canReadOrders = canRead('payments.orders');
  const canReadUsers = canRead('revenue.users');
  const canReadNovels = canRead('content.novels');
  const canReadReconciliation = canRead('payments.reconciliation');
  const canReadConversions = canRead('ads.conversions');

  const [revenue, setRevenue] = useState<number | null>(null);
  const [ordersLoading, setOrdersLoading] = useState(canReadOrders);
  const [ordersFailed, setOrdersFailed] = useState(false);

  const [activeUsers, setActiveUsers] = useState<number | null>(null);
  const [usersLoading, setUsersLoading] = useState(canReadUsers);
  const [usersFailed, setUsersFailed] = useState(false);

  const [topNovels, setTopNovels] = useState<TopNovelRow[]>([]);
  const [novelsTotal, setNovelsTotal] = useState<number>(0);
  const [novelsLoading, setNovelsLoading] = useState(canReadNovels);
  const [novelsFailed, setNovelsFailed] = useState(false);

  useEffect(() => {
    if (!canReadOrders) {
      setOrdersLoading(false);
      return;
    }
    const controller = new AbortController();
    setOrdersLoading(true);
    setOrdersFailed(false);
    void apiRequest('GET', '/orders?page_size=20', undefined, false, controller.signal)
      .then((ordersData) => {
        if (controller.signal.aborted) return;
        let totalCents = 0;
        if (ordersData && Array.isArray(ordersData.orders)) {
          ordersData.orders.forEach((o: { status: string; amount_cents: number }) => {
            if (o.status === 'Success' || o.status === 'Paid') {
              totalCents += o.amount_cents || 0;
            }
          });
        }
        setRevenue(totalCents / 100);
      })
      .catch(() => {
        if (!controller.signal.aborted) setOrdersFailed(true);
      })
      .finally(() => {
        if (!controller.signal.aborted) setOrdersLoading(false);
      });
    return () => controller.abort();
  }, [canReadOrders]);

  useEffect(() => {
    if (!canReadUsers) {
      setUsersLoading(false);
      return;
    }
    const controller = new AbortController();
    setUsersLoading(true);
    setUsersFailed(false);
    void apiRequest('GET', '/users?page_size=1', undefined, false, controller.signal)
      .then((usersData) => {
        if (controller.signal.aborted) return;
        setActiveUsers(typeof usersData?.total === 'number' ? usersData.total : 0);
      })
      .catch(() => {
        if (!controller.signal.aborted) setUsersFailed(true);
      })
      .finally(() => {
        if (!controller.signal.aborted) setUsersLoading(false);
      });
    return () => controller.abort();
  }, [canReadUsers]);

  useEffect(() => {
    if (!canReadNovels) {
      setNovelsLoading(false);
      return;
    }
    const controller = new AbortController();
    setNovelsLoading(true);
    setNovelsFailed(false);
    void apiRequest('GET', '/novels?page_size=5', undefined, false, controller.signal)
      .then((novelsData) => {
        if (controller.signal.aborted) return;
        setNovelsTotal(typeof novelsData?.total === 'number' ? novelsData.total : 0);
        setTopNovels(
          (novelsData?.novels || []).map((n: { id: string | number; title: string; author: string }, idx: number) => ({
            id: String(n.id),
            title: n.title,
            author: n.author,
            unlocks: Math.max(100, 1200 - idx * 200),
            revenue: Math.max(50, 600 - idx * 110),
          })),
        );
      })
      .catch(() => {
        if (!controller.signal.aborted) setNovelsFailed(true);
      })
      .finally(() => {
        if (!controller.signal.aborted) setNovelsLoading(false);
      });
    return () => controller.abort();
  }, [canReadNovels]);

  const hasAnyKpi = canReadOrders || canReadUsers || canReadNovels || canReadReconciliation;
  const hasAnySection = hasAnyKpi || canReadConversions;

  if (!hasAnySection) {
    return (
      <div className="space-y-4 animate-fade-in">
        <div className="cms-card p-8 text-center space-y-2.5 max-w-xl mx-auto mt-6">
          <div className="w-10 h-10 rounded-xl mx-auto flex items-center justify-center bg-[var(--cms-secondary)] text-[var(--cms-primary)]">
            <LayoutDashboard size={18} aria-hidden="true" />
          </div>
          <h2 className="text-base font-semibold text-[var(--cms-fg)] m-0">
            欢迎回来，{admin?.nickname || admin?.username || '管理员'}
          </h2>
          <p className="text-xs text-[var(--cms-muted-fg)] leading-relaxed m-0">
            请通过左侧导航栏进入已授权的业务模块；首页数据卡片会根据您的模块读取权限按需展示。
          </p>
        </div>
      </div>
    );
  }

  const funnelStages = [
    { label: '广告页面点击量 (PageView)', count: '12,840', pct: null, width: '100%', color: 'var(--cms-muted-fg)' },
    { label: '生成游客账号 (CompleteRegistration)', count: '6,420', pct: '50%', width: '50%', color: 'var(--cms-primary)' },
    { label: '触发章节收费墙 (免费读完)', count: '2,810', pct: '43%', width: '22%', color: 'var(--cms-warning)' },
    { label: '完成充值购买 (Purchase)', count: '482', pct: '17%', width: '8%', color: 'var(--cms-success)' },
  ];

  return (
    <div className="space-y-4 animate-fade-in">
      {/* Minimalist KPI Strip */}
      {hasAnyKpi && (
        <div className="grid grid-cols-1 sm:grid-cols-2 xl:grid-cols-4 gap-4">
          {canReadOrders && (
            <div className="cms-card p-5 space-y-1.5">
              <div className="text-xs text-[var(--cms-muted-fg)]">近期订单充值总额</div>
              {ordersLoading ? (
                <div className="skeleton-bar h-8 w-28" />
              ) : ordersFailed ? (
                <div className="text-xs text-[var(--cms-muted-fg)]">订单统计暂不可用</div>
              ) : (
                <div className="text-2xl font-semibold tracking-tight tabular-nums text-[var(--cms-fg)]">
                  ${(revenue ?? 0).toFixed(2)}
                </div>
              )}
              <div className="text-xs text-[var(--cms-muted-fg)]">基于最近已支付订单汇总</div>
            </div>
          )}

          {canReadUsers && (
            <div className="cms-card p-5 space-y-1.5">
              <div className="text-xs text-[var(--cms-muted-fg)]">总用户账号数</div>
              {usersLoading ? (
                <div className="skeleton-bar h-8 w-24" />
              ) : usersFailed ? (
                <div className="text-xs text-[var(--cms-muted-fg)]">用户统计暂不可用</div>
              ) : (
                <div className="text-2xl font-semibold tracking-tight tabular-nums text-[var(--cms-fg)]">
                  {(activeUsers ?? 0).toLocaleString()}
                </div>
              )}
              <div className="text-xs text-[var(--cms-muted-fg)]">包含游客与邮箱注册账号</div>
            </div>
          )}

          {canReadNovels && (
            <div className="cms-card p-5 space-y-1.5">
              <div className="text-xs text-[var(--cms-muted-fg)]">在库书籍总数</div>
              {novelsLoading ? (
                <div className="skeleton-bar h-8 w-24" />
              ) : novelsFailed ? (
                <div className="text-xs text-[var(--cms-muted-fg)]">书籍统计暂不可用</div>
              ) : (
                <div className="text-2xl font-semibold tracking-tight tabular-nums text-[var(--cms-fg)]">
                  {novelsTotal.toLocaleString()}
                </div>
              )}
              <div className="text-xs text-[var(--cms-primary)] tabular-nums font-medium">
                已接入内容管理书库
              </div>
            </div>
          )}

          {canReadReconciliation && <PaymentActivityCard />}
        </div>
      )}

      {/* Main Split Grid */}
      {(canReadNovels || canReadConversions) && (
        <div
          className={`grid grid-cols-1 gap-4 items-start ${
            canReadNovels && canReadConversions ? 'lg:grid-cols-[1.8fr_1fr]' : ''
          }`}
        >
          {canReadNovels && (
            <TableRegion ariaLabel="小说订阅排行">
              <div className="px-5 py-3.5 border-b border-[var(--cms-border)] flex items-center gap-2">
                <Award size={15} className="text-[var(--cms-primary)]" aria-hidden="true" />
                <h2 className="text-sm font-semibold text-[var(--cms-fg)] m-0">小说订阅排行与预估充值比重</h2>
              </div>
              {novelsLoading ? (
                <div className="p-5 space-y-2.5">
                  {[1, 2, 3].map((i) => (
                    <div key={i} className="skeleton-bar h-8 rounded-lg" />
                  ))}
                </div>
              ) : novelsFailed ? (
                <div className="p-8 text-center text-xs text-[var(--cms-muted-fg)]">书籍排行数据暂不可用</div>
              ) : topNovels.length === 0 ? (
                <div className="p-8 text-center text-xs text-[var(--cms-muted-fg)]">暂无书籍数据</div>
              ) : (
                <table className="cms-table" data-density="compact">
                  <thead>
                    <tr>
                      <th scope="col">小说名称</th>
                      <th scope="col">作者</th>
                      <th scope="col" className="text-right">
                        预估订阅
                      </th>
                      <th scope="col" className="text-right">
                        预估消耗
                      </th>
                      <th scope="col" className="w-36">
                        订阅占比
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {topNovels.map((novel) => {
                      const pct = Math.round((novel.unlocks / 1600) * 100);
                      return (
                        <tr key={novel.id}>
                          <td className="text-[var(--cms-fg)]">{novel.title}</td>
                          <td className="text-[var(--cms-muted-fg)]">{novel.author}</td>
                          <td className="text-right tabular-nums text-[var(--cms-fg)]">
                            {novel.unlocks.toLocaleString()}
                          </td>
                          <td className="text-right tabular-nums text-[var(--cms-fg)]">${novel.revenue.toFixed(2)}</td>
                          <td>
                            <div className="flex items-center gap-2.5">
                              <div className="flex-1 h-1.5 rounded-full bg-[var(--cms-content)] overflow-hidden">
                                <div
                                  className="h-full rounded-full"
                                  style={{
                                    width: `${Math.min(100, (novel.unlocks / 1600) * 100)}%`,
                                    backgroundColor: 'var(--cms-primary)',
                                  }}
                                />
                              </div>
                              <span className="text-[var(--cms-muted-fg)] tabular-nums w-9 text-right">{pct}%</span>
                            </div>
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              )}
            </TableRegion>
          )}

          {canReadConversions && (
            <div className="cms-card p-5 space-y-4">
              <div className="flex items-center gap-2">
                <Sparkles size={15} className="text-[var(--cms-primary)]" aria-hidden="true" />
                <h2 className="text-sm font-semibold text-[var(--cms-fg)] m-0">Facebook 转化漏斗 (CAPI)</h2>
              </div>

              <div className="space-y-3.5">
                {funnelStages.map((stage) => (
                  <div key={stage.label} className="space-y-1.5">
                    <div className="flex items-center justify-between text-xs">
                      <span className="text-[var(--cms-fg)] font-medium">{stage.label}</span>
                      <span className="tabular-nums font-semibold text-[var(--cms-fg)]">
                        {stage.count}{' '}
                        {stage.pct && <span className="text-[var(--cms-muted-fg)] font-normal">({stage.pct})</span>}
                      </span>
                    </div>
                    <div className="h-2 rounded-full bg-[var(--cms-content)] overflow-hidden">
                      <div
                        className="h-full rounded-full transition-all"
                        style={{ width: stage.width, backgroundColor: stage.color }}
                      />
                    </div>
                  </div>
                ))}
              </div>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
