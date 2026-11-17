import { useEffect, useRef, useState } from 'react';
import {
  ArrowDown,
  ArrowUp,
  BookOpen,
  Flame,
  Plus,
  Save,
  Search,
  Sparkles,
  Star,
  Trash2,
  Trophy,
} from 'lucide-react';
import CustomSelect from '../components/CustomSelect';
import {
  operationsApi,
  type GroupItemInput,
  type RankingSortRule,
  type RecommendationEntry,
  type RecommendationGroupName,
  type RecommendationNovel,
} from '../utils/api';

type OpsSubTab = 'recommendations' | 'rankings';
type RecSlot = 'editor_pick' | 'curated';

const rankingSortOptions: Array<{ value: RankingSortRule; label: string }> = [
  { value: 'views', label: '按阅读量降序（默认）' },
  { value: 'rating', label: '按评分降序' },
  { value: 'latest', label: '按最新上架降序' },
  { value: 'manual', label: '仅展示人工配置榜单' },
];

const slotMeta: Record<RecSlot, { title: string; subtitle: string; badge: string }> = {
  editor_pick: {
    title: '首页精选主推（Featured Stories）',
    subtitle: '对应 H5 首页顶部轮播精选推荐区。留空时将默认展示热门小说前 4 本。',
    badge: 'Hero 轮播',
  },
  curated: {
    title: '首页编辑推荐（Curated For You）',
    subtitle: '对应 H5 首页为你推荐列表区。留空时将默认按书籍目录顺序展示。',
    badge: '编辑精选',
  },
};

const FALLBACK_COVER =
  'data:image/svg+xml;utf8,' +
  encodeURIComponent(
    '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 120 160"><rect width="120" height="160" fill="#1e293b"/><text x="60" y="84" fill="#94a3b8" font-family="sans-serif" font-size="12" text-anchor="middle">No Cover</text></svg>',
  );

function toLocalInputValue(iso?: string | null): string {
  if (!iso) return '';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '';
  const offset = d.getTimezoneOffset();
  const local = new Date(d.getTime() - offset * 60_000);
  return local.toISOString().slice(0, 16);
}

function toISOOrNull(localVal?: string | null): string | null {
  if (!localVal || !localVal.trim()) return null;
  const d = new Date(localVal);
  if (Number.isNaN(d.getTime())) return null;
  return d.toISOString();
}

export default function OperationsTab() {
  const [activeTab, setActiveTab] = useState<OpsSubTab>('recommendations');
  const [activeRecSlot, setActiveRecSlot] = useState<RecSlot>('editor_pick');

  const [loading, setLoading] = useState(true);
  const [savingRankingRule, setSavingRankingRule] = useState(false);
  const [savingGroup, setSavingGroup] = useState<RecommendationGroupName | null>(null);

  const [sortRule, setSortRule] = useState<RankingSortRule>('views');
  const [rankingLimitInput, setRankingLimitInput] = useState<string>('20');
  const [groups, setGroups] = useState<Record<RecommendationGroupName, RecommendationEntry[]>>({
    editor_pick: [],
    curated: [],
    ranking: [],
  });

  // Search published books picker
  const [searchKeyword, setSearchKeyword] = useState('');
  const [candidatePage, setCandidatePage] = useState(1);
  const [candidateTotal, setCandidateTotal] = useState(0);
  const candidatePageSize = 30;
  const [searchingNovels, setSearchingNovels] = useState(false);
  const [candidateNovels, setCandidateNovels] = useState<RecommendationNovel[]>([]);
  const searchAbortRef = useRef<AbortController | null>(null);
  const searchSeqRef = useRef(0);

  const loadConfig = async () => {
    setLoading(true);
    try {
      const data = await operationsApi.getConfig();
      setSortRule(data.ranking_config?.sort_rule || 'views');
      setRankingLimitInput(String(data.ranking_config?.limit || 20));
      setGroups({
        editor_pick: data.groups?.editor_pick || [],
        curated: data.groups?.curated || [],
        ranking: data.groups?.ranking || [],
      });
    } catch (err: any) {
      window.showToast?.(err.message || '加载运营配置失败', 'error');
    } finally {
      setLoading(false);
    }
  };

  const loadCandidates = async (kw: string, page = 1) => {
    searchAbortRef.current?.abort();
    const controller = new AbortController();
    searchAbortRef.current = controller;
    const seq = ++searchSeqRef.current;

    setSearchingNovels(true);
    try {
      const res = await operationsApi.searchPublishedNovels(kw.trim(), page, candidatePageSize, controller.signal);
      if (controller.signal.aborted || seq !== searchSeqRef.current) return;
      setCandidateNovels(res.novels || []);
      setCandidateTotal(res.total || 0);
      setCandidatePage(page);
    } catch (err: any) {
      if (err?.name === 'AbortError' || controller.signal.aborted || seq !== searchSeqRef.current) return;
      window.showToast?.(err.message || '搜索已上架书籍失败', 'error');
    } finally {
      if (seq === searchSeqRef.current) {
        setSearchingNovels(false);
      }
    }
  };

  useEffect(() => {
    void loadConfig();
    void loadCandidates('', 1);
    return () => searchAbortRef.current?.abort();
  }, []);

  const currentGroupName: RecommendationGroupName =
    activeTab === 'rankings' ? 'ranking' : activeRecSlot;
  const currentEntries = groups[currentGroupName] || [];

  const handleAddBook = (novel: RecommendationNovel) => {
    const exists = currentEntries.some((item) => item.novel_id === novel.id);
    if (exists) {
      window.showToast?.(`《${novel.title}》已在当前配置列表中`, 'info');
      return;
    }
    const nextEntry: RecommendationEntry = {
      group_name: currentGroupName,
      novel_id: novel.id,
      sort_order: currentEntries.length,
      scheduled_start: null,
      scheduled_end: null,
      novel,
    };
    setGroups((prev) => ({
      ...prev,
      [currentGroupName]: [...prev[currentGroupName], nextEntry],
    }));
  };

  const handleMove = (index: number, direction: -1 | 1) => {
    const target = index + direction;
    if (target < 0 || target >= currentEntries.length) return;
    setGroups((prev) => {
      const list = [...prev[currentGroupName]];
      const [moved] = list.splice(index, 1);
      list.splice(target, 0, moved);
      return {
        ...prev,
        [currentGroupName]: list.map((item, idx) => ({ ...item, sort_order: idx })),
      };
    });
  };

  const handleRemove = (novelId: number) => {
    setGroups((prev) => ({
      ...prev,
      [currentGroupName]: prev[currentGroupName]
        .filter((item) => item.novel_id !== novelId)
        .map((item, idx) => ({ ...item, sort_order: idx })),
    }));
  };

  const handleScheduleChange = (
    novelId: number,
    field: 'scheduled_start' | 'scheduled_end',
    localValue: string,
  ) => {
    const iso = toISOOrNull(localValue);
    setGroups((prev) => ({
      ...prev,
      [currentGroupName]: prev[currentGroupName].map((item) =>
        item.novel_id === novelId ? { ...item, [field]: iso } : item,
      ),
    }));
  };

  const handleSaveGroup = async (groupName: RecommendationGroupName) => {
    const entries = groups[groupName] || [];
    for (const item of entries) {
      if (item.scheduled_start && item.scheduled_end && new Date(item.scheduled_end) < new Date(item.scheduled_start)) {
        window.showToast?.(`《${item.novel?.title || `ID:${item.novel_id}`}》的失效时间不能早于生效时间`, 'error');
        return;
      }
    }
    setSavingGroup(groupName);
    try {
      const payload: GroupItemInput[] = entries.map((item, idx) => ({
        novel_id: item.novel_id,
        sort_order: idx,
        scheduled_start: item.scheduled_start || null,
        scheduled_end: item.scheduled_end || null,
      }));
      const res = await operationsApi.saveGroup(groupName, payload);
      setGroups((prev) => ({
        ...prev,
        [groupName]: res.items || [],
      }));
      window.showToast?.('列表配置已保存并即时同步至 H5 端', 'success');
    } catch (err: any) {
      window.showToast?.(err.message || '保存配置失败', 'error');
    } finally {
      setSavingGroup(null);
    }
  };

  const handleSaveRankingConfig = async (e: React.FormEvent) => {
    e.preventDefault();
    const parsedLimit = Number(rankingLimitInput.trim());
    if (!rankingLimitInput.trim() || !Number.isInteger(parsedLimit) || parsedLimit < 1 || parsedLimit > 100) {
      window.showToast?.('榜单展示上限须为 1 至 100 的整数', 'error');
      return;
    }
    setSavingRankingRule(true);
    try {
      await operationsApi.saveRankingConfig({
        sort_rule: sortRule,
        limit: parsedLimit,
      });
      window.showToast?.('排名榜排序规则已保存', 'success');
    } catch (err: any) {
      window.showToast?.(err.message || '保存排序规则失败', 'error');
    } finally {
      setSavingRankingRule(false);
    }
  };

  const handleSearchSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    void loadCandidates(searchKeyword, 1);
  };

  return (
    <div className="animate-fade-in" style={{ padding: '16px 24px 32px 24px' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '20px', flexWrap: 'wrap', gap: '12px' }}>
        <div>
          <h1 style={{ fontSize: '1.5rem', fontWeight: 700, display: 'flex', alignItems: 'center', gap: '10px' }} className="gradient-text">
            <Sparkles size={22} style={{ color: 'hsl(var(--primary))' }} />
            运营配置
          </h1>
          <p style={{ fontSize: '0.82rem', color: 'hsl(var(--text-secondary))', marginTop: '4px' }}>
            管理 H5 首页推荐位书籍与热门排行榜单规则
          </p>
        </div>

        {/* Sub-module switch */}
        <div style={{ display: 'flex', gap: '8px', backgroundColor: 'hsl(var(--bg-surface))', padding: '4px', borderRadius: '10px', border: '1px solid hsl(var(--border))' }}>
          <button
            type="button"
            onClick={() => setActiveTab('recommendations')}
            style={{
              display: 'inline-flex',
              alignItems: 'center',
              gap: '6px',
              padding: '8px 16px',
              borderRadius: '8px',
              fontSize: '0.85rem',
              fontWeight: 600,
              cursor: 'pointer',
              border: 'none',
              backgroundColor: activeTab === 'recommendations' ? 'hsl(var(--primary))' : 'transparent',
              color: activeTab === 'recommendations' ? '#ffffff' : 'hsl(var(--text-secondary))',
              transition: 'all 0.15s ease',
            }}
          >
            <BookOpen size={15} />
            首页推荐设置
          </button>
          <button
            type="button"
            onClick={() => setActiveTab('rankings')}
            style={{
              display: 'inline-flex',
              alignItems: 'center',
              gap: '6px',
              padding: '8px 16px',
              borderRadius: '8px',
              fontSize: '0.85rem',
              fontWeight: 600,
              cursor: 'pointer',
              border: 'none',
              backgroundColor: activeTab === 'rankings' ? 'hsl(var(--primary))' : 'transparent',
              color: activeTab === 'rankings' ? '#ffffff' : 'hsl(var(--text-secondary))',
              transition: 'all 0.15s ease',
            }}
          >
            <Trophy size={15} />
            书籍排名榜设置
          </button>
        </div>
      </div>

      {loading ? (
        <div className="glass-panel" style={{ padding: '48px', textAlign: 'center', color: 'hsl(var(--text-secondary))' }}>
          正在加载运营配置...
        </div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: '20px' }}>
          {activeTab === 'recommendations' ? (
            <div className="glass-panel" style={{ padding: '20px' }}>
              <div style={{ display: 'flex', gap: '12px', flexWrap: 'wrap', marginBottom: '14px' }}>
                {(Object.keys(slotMeta) as RecSlot[]).map((slotKey) => {
                  const selected = activeRecSlot === slotKey;
                  const count = groups[slotKey]?.length || 0;
                  return (
                    <button
                      key={slotKey}
                      type="button"
                      onClick={() => setActiveRecSlot(slotKey)}
                      style={{
                        flex: '1 1 260px',
                        textAlign: 'left',
                        padding: '14px 16px',
                        borderRadius: '10px',
                        cursor: 'pointer',
                        border: selected
                          ? '1.5px solid hsl(var(--primary))'
                          : '1px solid hsl(var(--border))',
                        backgroundColor: selected
                          ? 'hsl(var(--primary) / 0.08)'
                          : 'hsl(var(--bg-base) / 0.45)',
                      }}
                    >
                      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '4px' }}>
                        <span style={{ fontWeight: 700, fontSize: '0.92rem', color: 'hsl(var(--text-primary))' }}>
                          {slotMeta[slotKey].title}
                        </span>
                        <span style={{ fontSize: '0.72rem', fontWeight: 600, padding: '2px 8px', borderRadius: '999px', backgroundColor: 'hsl(var(--primary) / 0.14)', color: 'hsl(var(--primary))' }}>
                          {count} 本
                        </span>
                      </div>
                      <p style={{ fontSize: '0.78rem', color: 'hsl(var(--text-secondary))', margin: 0, lineHeight: 1.45 }}>
                        {slotMeta[slotKey].subtitle}
                      </p>
                    </button>
                  );
                })}
              </div>
            </div>
          ) : (
            <form onSubmit={handleSaveRankingConfig} className="glass-panel" style={{ padding: '20px' }}>
              <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '16px', flexWrap: 'wrap', gap: '10px' }}>
                <div>
                  <h2 style={{ fontSize: '1.05rem', fontWeight: 700, display: 'flex', alignItems: 'center', gap: '8px' }}>
                    <Trophy size={17} style={{ color: 'hsl(var(--primary))' }} />
                    排名榜排序规则（Top Charts）
                  </h2>
                  <p style={{ fontSize: '0.78rem', color: 'hsl(var(--text-secondary))', marginTop: '4px' }}>
                    下方人工置顶列表将按序号优先排在榜单最前；除“仅展示人工配置榜单”且至少配置 1 本书籍外，其余名额将按此规则自动补全。
                  </p>
                </div>
              </div>

              <div style={{ display: 'flex', gap: '16px', alignItems: 'flex-end', flexWrap: 'wrap' }}>
                <div>
                  <label style={{ display: 'block', fontSize: '0.75rem', color: 'hsl(var(--text-secondary))', marginBottom: '6px' }}>
                    榜单排序依据
                  </label>
                  <CustomSelect
                    options={rankingSortOptions}
                    value={sortRule}
                    onChange={(val) => setSortRule(val as RankingSortRule)}
                    width="240px"
                  />
                </div>

                <div>
                  <label style={{ display: 'block', fontSize: '0.75rem', color: 'hsl(var(--text-secondary))', marginBottom: '6px' }}>
                    榜单展示上限（1–100）
                  </label>
                  <input
                    type="number"
                    min={1}
                    max={100}
                    className="input-field"
                    style={{ width: '140px' }}
                    value={rankingLimitInput}
                    onChange={(e) => setRankingLimitInput(e.target.value)}
                  />
                </div>

                <button
                  type="submit"
                  className="btn-primary"
                  disabled={savingRankingRule}
                  style={{ display: 'inline-flex', alignItems: 'center', gap: '6px', height: '38px' }}
                >
                  <Save size={15} />
                  {savingRankingRule ? '保存中...' : '保存榜单规则'}
                </button>
              </div>
            </form>
          )}

          {/* Dual column workspace: Configured List + Published Book Picker */}
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(340px, 1fr))', gap: '20px', alignItems: 'start' }}>
            {/* Left: Current configured items */}
            <div className="glass-panel" style={{ padding: '20px' }}>
              <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '16px', gap: '12px', flexWrap: 'wrap' }}>
                <div>
                  <h3 style={{ fontSize: '1rem', fontWeight: 700 }}>
                    {activeTab === 'rankings'
                      ? '人工置顶 / 指定排名榜书籍'
                      : `${slotMeta[activeRecSlot].title} — 已配置书籍`}
                  </h3>
                  <span style={{ fontSize: '0.75rem', color: 'hsl(var(--text-secondary))' }}>
                    共 {currentEntries.length} 本（按序号从小到大展示）
                  </span>
                </div>

                <button
                  type="button"
                  className="btn-primary"
                  disabled={savingGroup === currentGroupName}
                  onClick={() => handleSaveGroup(currentGroupName)}
                  style={{ display: 'inline-flex', alignItems: 'center', gap: '6px' }}
                >
                  <Save size={15} />
                  {savingGroup === currentGroupName ? '保存中...' : '保存当前列表'}
                </button>
              </div>

              {currentEntries.length === 0 ? (
                <div style={{ padding: '36px 16px', textAlign: 'center', border: '1px dashed hsl(var(--border))', borderRadius: '10px', color: 'hsl(var(--text-secondary))', fontSize: '0.85rem' }}>
                  当前配置为空，请从右侧已上架书库中添加书籍并保存。
                </div>
              ) : (
                <div style={{ display: 'flex', flexDirection: 'column', gap: '10px' }}>
                  {currentEntries.map((entry, idx) => {
                    const novel = entry.novel;
                    return (
                      <div
                        key={entry.novel_id}
                        style={{
                          display: 'flex',
                          alignItems: 'center',
                          gap: '12px',
                          padding: '10px 12px',
                          borderRadius: '10px',
                          border: '1px solid hsl(var(--border))',
                          backgroundColor: 'hsl(var(--bg-base) / 0.45)',
                        }}
                      >
                        <div
                          style={{
                            width: '28px',
                            height: '28px',
                            borderRadius: '8px',
                            display: 'flex',
                            alignItems: 'center',
                            justifyContent: 'center',
                            fontWeight: 700,
                            fontSize: '0.8rem',
                            flexShrink: 0,
                            backgroundColor: idx < 3 ? 'hsl(var(--primary) / 0.16)' : 'hsl(var(--bg-surface))',
                            color: idx < 3 ? 'hsl(var(--primary))' : 'hsl(var(--text-secondary))',
                          }}
                        >
                          {idx + 1}
                        </div>

                        <img
                          src={novel?.cover_url || FALLBACK_COVER}
                          alt={novel?.title || String(entry.novel_id)}
                          style={{ width: '40px', height: '54px', objectFit: 'cover', borderRadius: '6px', flexShrink: 0 }}
                        />

                        <div style={{ flex: 1, minWidth: 0 }}>
                          <div style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
                            <span style={{ fontWeight: 600, fontSize: '0.88rem', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                              {novel?.title || `Book #${entry.novel_id}`}
                            </span>
                            {novel?.publish_status && novel.publish_status !== 'published' && (
                              <span style={{ fontSize: '0.68rem', padding: '1px 6px', borderRadius: '999px', backgroundColor: 'rgba(245, 158, 11, 0.15)', color: '#f59e0b' }}>
                                未上架（H5 隐藏）
                              </span>
                            )}
                          </div>
                          <div style={{ fontSize: '0.75rem', color: 'hsl(var(--text-secondary))', marginTop: '2px', display: 'flex', gap: '10px', alignItems: 'center', flexWrap: 'wrap' }}>
                            <span>ID: {entry.novel_id}</span>
                            <span>{novel?.author || '-'}</span>
                            <span style={{ display: 'inline-flex', alignItems: 'center', gap: '3px' }}>
                              <Star size={11} /> {novel?.rating ?? 0}
                            </span>
                            <span style={{ display: 'inline-flex', alignItems: 'center', gap: '3px' }}>
                              <Flame size={11} /> {(novel?.view_count ?? 0).toLocaleString()}
                            </span>
                          </div>
                          <div style={{ display: 'flex', gap: '8px', marginTop: '6px', flexWrap: 'wrap' }}>
                            <label style={{ fontSize: '0.7rem', color: 'hsl(var(--text-muted))', display: 'inline-flex', alignItems: 'center', gap: '4px' }}>
                              生效:
                              <input
                                type="datetime-local"
                                value={toLocalInputValue(entry.scheduled_start)}
                                onChange={(e) => handleScheduleChange(entry.novel_id, 'scheduled_start', e.target.value)}
                                className="input-field"
                                style={{ padding: '2px 6px', fontSize: '0.72rem', width: '165px' }}
                              />
                            </label>
                            <label style={{ fontSize: '0.7rem', color: 'hsl(var(--text-muted))', display: 'inline-flex', alignItems: 'center', gap: '4px' }}>
                              失效:
                              <input
                                type="datetime-local"
                                value={toLocalInputValue(entry.scheduled_end)}
                                onChange={(e) => handleScheduleChange(entry.novel_id, 'scheduled_end', e.target.value)}
                                className="input-field"
                                style={{ padding: '2px 6px', fontSize: '0.72rem', width: '165px' }}
                              />
                            </label>
                          </div>
                        </div>

                        <div style={{ display: 'flex', alignItems: 'center', gap: '4px', flexShrink: 0 }}>
                          <button
                            type="button"
                            className="btn-secondary"
                            style={{ padding: '6px' }}
                            disabled={idx === 0}
                            onClick={() => handleMove(idx, -1)}
                            title="上移"
                          >
                            <ArrowUp size={14} />
                          </button>
                          <button
                            type="button"
                            className="btn-secondary"
                            style={{ padding: '6px' }}
                            disabled={idx === currentEntries.length - 1}
                            onClick={() => handleMove(idx, 1)}
                            title="下移"
                          >
                            <ArrowDown size={14} />
                          </button>
                          <button
                            type="button"
                            className="btn-secondary"
                            style={{ padding: '6px', color: '#ef4444' }}
                            onClick={() => handleRemove(entry.novel_id)}
                            title="移除"
                          >
                            <Trash2 size={14} />
                          </button>
                        </div>
                      </div>
                    );
                  })}
                </div>
              )}
            </div>

            {/* Right: Published novel picker */}
            <div className="glass-panel" style={{ padding: '20px' }}>
              <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '12px' }}>
                <h3 style={{ fontSize: '0.96rem', fontWeight: 700, margin: 0 }}>
                  从已上架书库添加
                </h3>
                <span style={{ fontSize: '0.74rem', color: 'hsl(var(--text-secondary))' }}>
                  共 {candidateTotal} 本
                </span>
              </div>

              <form onSubmit={handleSearchSubmit} style={{ display: 'flex', gap: '8px', marginBottom: '14px' }}>
                <div style={{ position: 'relative', flex: 1 }}>
                  <Search size={14} style={{ position: 'absolute', left: '10px', top: '50%', transform: 'translateY(-50%)', color: 'hsl(var(--text-muted))' }} />
                  <input
                    type="text"
                    className="input-field"
                    style={{ paddingLeft: '32px', width: '100%' }}
                    placeholder="搜索书名、作者或 ID..."
                    value={searchKeyword}
                    onChange={(e) => setSearchKeyword(e.target.value)}
                  />
                </div>
                <button type="submit" className="btn-secondary" disabled={searchingNovels}>
                  {searchingNovels ? '搜索中' : '搜索'}
                </button>
              </form>

              <div style={{ display: 'flex', flexDirection: 'column', gap: '8px', maxHeight: '520px', overflowY: 'auto' }}>
                {candidateNovels.length === 0 ? (
                  <div style={{ padding: '24px', textAlign: 'center', color: 'hsl(var(--text-secondary))', fontSize: '0.82rem' }}>
                    未找到匹配的已上架书籍
                  </div>
                ) : (
                  candidateNovels.map((novel) => {
                    const alreadyAdded = currentEntries.some((item) => item.novel_id === novel.id);
                    return (
                      <div
                        key={novel.id}
                        style={{
                          display: 'flex',
                          alignItems: 'center',
                          gap: '10px',
                          padding: '8px 10px',
                          borderRadius: '8px',
                          border: '1px solid hsl(var(--border))',
                        }}
                      >
                        <img
                          src={novel.cover_url || FALLBACK_COVER}
                          alt={novel.title}
                          style={{ width: '34px', height: '46px', objectFit: 'cover', borderRadius: '5px', flexShrink: 0 }}
                        />
                        <div style={{ flex: 1, minWidth: 0 }}>
                          <div style={{ fontWeight: 600, fontSize: '0.82rem', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                            {novel.title}
                          </div>
                          <div style={{ fontSize: '0.72rem', color: 'hsl(var(--text-secondary))', display: 'flex', gap: '8px' }}>
                            <span>ID:{novel.id}</span>
                            <span>{novel.author}</span>
                            <span>★ {novel.rating}</span>
                          </div>
                        </div>
                        <button
                          type="button"
                          className={alreadyAdded ? 'btn-secondary' : 'btn-primary'}
                          disabled={alreadyAdded}
                          onClick={() => handleAddBook(novel)}
                          style={{ padding: '5px 10px', fontSize: '0.75rem', display: 'inline-flex', alignItems: 'center', gap: '4px', flexShrink: 0 }}
                        >
                          <Plus size={13} />
                          {alreadyAdded ? '已添加' : '添加'}
                        </button>
                      </div>
                    );
                  })
                )}
              </div>

              {candidateTotal > candidatePageSize && (
                <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginTop: '12px', paddingTop: '10px', borderTop: '1px solid hsl(var(--border))', fontSize: '0.76rem' }}>
                  <span style={{ color: 'hsl(var(--text-secondary))' }}>
                    第 {candidatePage} / {Math.max(1, Math.ceil(candidateTotal / candidatePageSize))} 页
                  </span>
                  <div style={{ display: 'flex', gap: '6px' }}>
                    <button
                      type="button"
                      className="btn-secondary"
                      style={{ padding: '4px 10px', fontSize: '0.74rem' }}
                      disabled={searchingNovels || candidatePage <= 1}
                      onClick={() => loadCandidates(searchKeyword, candidatePage - 1)}
                    >
                      上一页
                    </button>
                    <button
                      type="button"
                      className="btn-secondary"
                      style={{ padding: '4px 10px', fontSize: '0.74rem' }}
                      disabled={searchingNovels || candidatePage * candidatePageSize >= candidateTotal}
                      onClick={() => loadCandidates(searchKeyword, candidatePage + 1)}
                    >
                      下一页
                    </button>
                  </div>
                </div>
              )}
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
