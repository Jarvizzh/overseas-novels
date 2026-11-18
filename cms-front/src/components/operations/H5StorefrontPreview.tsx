import React from 'react';
import {
  ArrowRight,
  BookOpen,
  Clock,
  Compass,
  Flame,
  Gift,
  RefreshCw,
  Search,
  Star,
  User,
} from 'lucide-react';
import type {
  OperationsPreviewResponse,
  PreviewNovelItem,
} from '../../utils/api';
import {
  FALLBACK_COVER,
  formatCompactDateTime,
  formatCompactNumber,
  toISOOrNull,
  toLocalInputValue,
} from './operationsUtils';

interface H5StorefrontPreviewProps {
  activeView: 'discover' | 'hot';
  h5Theme: 'light' | 'dark';
  onThemeChange: (theme: 'light' | 'dark') => void;
  preview: OperationsPreviewResponse | null;
  loading: boolean;
  errorMsg: string;
  previewAtIso: string | null;
  onPreviewAtChange: (isoOrNull: string | null) => void;
}

export const H5StorefrontPreview: React.FC<H5StorefrontPreviewProps> = ({
  activeView,
  h5Theme,
  onThemeChange,
  preview,
  loading,
  errorMsg,
  previewAtIso,
  onPreviewAtChange,
}) => {
  const applyTimePreset = (hoursOffset: number | 'tomorrow_midnight') => {
    const now = new Date();
    if (hoursOffset === 'tomorrow_midnight') {
      const tomorrow = new Date(now.getFullYear(), now.getMonth(), now.getDate() + 1, 0, 5, 0, 0);
      onPreviewAtChange(tomorrow.toISOString());
      return;
    }
    const target = new Date(now.getTime() + hoursOffset * 60 * 60 * 1000);
    onPreviewAtChange(target.toISOString());
  };

  const featured = preview?.featured || [];
  const newReleases = preview?.new_releases || [];
  const curated = preview?.curated || [];
  const rankings = preview?.rankings || [];

  const pinnedRankCount = rankings.filter((r) => r.source === 'pinned').length;
  const autoRankCount = rankings.length - pinnedRankCount;

  const renderSourceBadge = (item: PreviewNovelItem) => {
    if (item.source === 'pinned') {
      return (
        <span
          style={{
            fontSize: '0.625rem',
            fontWeight: 600,
            padding: '1px 6px',
            borderRadius: '999px',
            backgroundColor: 'var(--h5-accent-light)',
            color: 'var(--h5-accent-text)',
          }}
        >
          人工
        </span>
      );
    }
    if (item.source === 'fallback') {
      return (
        <span
          style={{
            fontSize: '0.625rem',
            fontWeight: 500,
            padding: '1px 6px',
            borderRadius: '999px',
            backgroundColor: 'var(--h5-bg-tertiary)',
            color: 'var(--h5-text-tertiary)',
          }}
        >
          兜底
        </span>
      );
    }
    return (
      <span
        style={{
          fontSize: '0.625rem',
          fontWeight: 500,
          padding: '1px 6px',
          borderRadius: '999px',
          backgroundColor: 'var(--h5-bg-tertiary)',
          color: 'var(--h5-text-tertiary)',
        }}
      >
        自动
      </span>
    );
  };

  return (
    <div className="xl:sticky xl:top-16 space-y-2.5">
      {/* Top Bar: Time Travel + Day/Night */}
      <div className="flex flex-wrap items-center justify-between gap-2 px-0.5">
        <div className="flex items-center gap-1.5 text-xs text-[var(--cms-muted-fg)]">
          <span className="font-medium text-[var(--cms-fg)]">H5 首页实时预览</span>
          {loading && <RefreshCw size={12} className="animate-spin text-[var(--cms-primary)]" aria-hidden="true" />}
        </div>

        <div className="flex items-center rounded-lg border border-[var(--cms-border)] bg-[var(--cms-card)] p-0.5 text-xs shrink-0">
          <button
            type="button"
            onClick={() => onThemeChange('light')}
            className={`focus-ring px-2 py-0.5 rounded-md cursor-pointer transition-colors ${
              h5Theme === 'light'
                ? 'bg-[var(--cms-secondary)] text-[var(--cms-fg)] font-medium'
                : 'text-[var(--cms-muted-fg)]'
            }`}
          >
            日间
          </button>
          <button
            type="button"
            onClick={() => onThemeChange('dark')}
            className={`focus-ring px-2 py-0.5 rounded-md cursor-pointer transition-colors ${
              h5Theme === 'dark'
                ? 'bg-[var(--cms-secondary)] text-[var(--cms-fg)] font-medium'
                : 'text-[var(--cms-muted-fg)]'
            }`}
          >
            夜间
          </button>
        </div>
      </div>

      {/* Time-Travel Simulation Strip */}
      <div className="cms-card p-2.5 space-y-2 text-xs">
        <div className="flex items-center justify-between gap-2">
          <span className="inline-flex items-center gap-1.5 text-[var(--cms-muted-fg)] font-medium">
            <Clock size={13} className={previewAtIso ? 'text-[var(--cms-primary)]' : ''} aria-hidden="true" />
            <span>{previewAtIso ? `模拟时刻：${formatCompactDateTime(previewAtIso)}` : '当前实时生效状态'}</span>
          </span>
          <div className="flex items-center gap-1">
            <button
              type="button"
              onClick={() => onPreviewAtChange(null)}
              className={`focus-ring px-2 py-0.5 rounded-md text-[11px] cursor-pointer transition-colors ${
                !previewAtIso
                  ? 'nav-active-item font-medium'
                  : 'text-[var(--cms-muted-fg)] hover:bg-[var(--cms-secondary)]'
              }`}
            >
              实时
            </button>
            <button
              type="button"
              onClick={() => applyTimePreset('tomorrow_midnight')}
              className="focus-ring px-2 py-0.5 rounded-md text-[11px] text-[var(--cms-muted-fg)] hover:text-[var(--cms-fg)] hover:bg-[var(--cms-secondary)] cursor-pointer transition-colors"
            >
              明早0点
            </button>
            <button
              type="button"
              onClick={() => applyTimePreset(24 * 7)}
              className="focus-ring px-2 py-0.5 rounded-md text-[11px] text-[var(--cms-muted-fg)] hover:text-[var(--cms-fg)] hover:bg-[var(--cms-secondary)] cursor-pointer transition-colors"
            >
              +7天
            </button>
          </div>
        </div>

        <input
          type="datetime-local"
          aria-label="选择模拟预览时间点"
          className="input-field h-7 text-xs tabular-nums"
          value={previewAtIso ? toLocalInputValue(previewAtIso) : ''}
          onChange={(e) => onPreviewAtChange(toISOOrNull(e.target.value))}
        />

        {errorMsg && (
          <div className="text-[11px] text-[var(--cms-error)]">{errorMsg}</div>
        )}
      </div>

      {/* Simulated H5 Reader Phone Shell */}
      <div
        className={`rounded-[26px] border border-[var(--cms-border)] overflow-hidden transition-colors flex flex-col h-[660px] ${
          h5Theme === 'dark' ? 'h5-preview-dark' : 'h5-preview-light'
        }`}
        style={{ boxShadow: 'var(--shadow-sm)', backgroundColor: 'var(--h5-bg-primary)' }}
      >
        {/* H5 Mobile Sticky Header (read-only) */}
        <div
          className="shrink-0 select-none"
          style={{
            borderBottom: '1px solid var(--h5-border)',
            backgroundColor: 'var(--h5-bg-primary)',
            padding: '10px 14px 0',
          }}
        >
          <div className="flex items-center justify-between gap-2">
            <span
              style={{
                fontFamily: "'Iowan Old Style', Georgia, serif",
                fontSize: '0.95rem',
                fontWeight: 600,
                letterSpacing: '-0.02em',
                color: 'var(--h5-text-primary)',
              }}
            >
              StarNovel
            </span>

            <div
              className="flex-1 min-w-0 flex items-center gap-1.5 px-2.5 py-1 rounded-full"
              style={{
                backgroundColor: 'var(--h5-bg-secondary)',
                border: '1px solid var(--h5-border)',
                color: 'var(--h5-text-tertiary)',
                fontSize: '0.6875rem',
              }}
            >
              <Search size={12} aria-hidden="true" className="shrink-0" />
              <span className="truncate">Title or author...</span>
            </div>

            <div
              className="px-2.5 py-1 rounded-full tabular-nums shrink-0 flex items-center gap-1"
              style={{
                backgroundColor: 'var(--h5-bg-secondary)',
                border: '1px solid var(--h5-border)',
                color: 'var(--h5-text-primary)',
                fontSize: '0.6875rem',
                fontWeight: 600,
              }}
            >
              <span
                className="w-2.5 h-2.5 rounded-full inline-block"
                style={{ background: 'linear-gradient(135deg, #F5E1A4, #A67C37)' }}
                aria-hidden="true"
              />
              <span>1,280</span>
              <span style={{ color: 'var(--h5-accent-text)' }}>+</span>
            </div>
          </div>

          <div className="flex items-center gap-5 mt-2">
            <div
              className="relative py-2 text-xs"
              style={{
                fontWeight: activeView === 'discover' ? 600 : 500,
                color: activeView === 'discover' ? 'var(--h5-text-primary)' : 'var(--h5-text-tertiary)',
              }}
            >
              <span>For You</span>
              {activeView === 'discover' && (
                <span
                  aria-hidden="true"
                  className="absolute left-0 right-0 bottom-0 h-[2px] rounded-full"
                  style={{ backgroundColor: 'var(--h5-accent)' }}
                />
              )}
            </div>
            <div
              className="relative py-2 text-xs"
              style={{
                fontWeight: activeView === 'hot' ? 600 : 500,
                color: activeView === 'hot' ? 'var(--h5-text-primary)' : 'var(--h5-text-tertiary)',
              }}
            >
              <span>Top Charts</span>
              {activeView === 'hot' && (
                <span
                  aria-hidden="true"
                  className="absolute left-0 right-0 bottom-0 h-[2px] rounded-full"
                  style={{ backgroundColor: 'var(--h5-accent)' }}
                />
              )}
            </div>
          </div>
        </div>

        {/* H5 Scrollable Content */}
        <div
          className="flex-1 min-h-0 overflow-y-auto p-3.5 space-y-5"
          style={{ backgroundColor: 'var(--h5-bg-primary)' }}
        >
          {activeView === 'discover' ? (
            <>
              {/* Section 1: Featured Stories (editor_pick) */}
              <section className="space-y-2">
                <div className="flex items-end justify-between gap-2">
                  <div>
                    <div
                      style={{
                        fontSize: '0.625rem',
                        fontWeight: 600,
                        textTransform: 'uppercase',
                        letterSpacing: '0.1em',
                        color: 'var(--h5-accent-text)',
                      }}
                    >
                      Editor&apos;s Picks
                    </div>
                    <h4
                      className="m-0"
                      style={{
                        fontFamily: "'Iowan Old Style', Georgia, serif",
                        fontSize: '0.95rem',
                        fontWeight: 600,
                        color: 'var(--h5-text-primary)',
                      }}
                    >
                      Featured Stories
                    </h4>
                  </div>
                  <span
                    style={{
                      fontSize: '0.625rem',
                      padding: '2px 7px',
                      borderRadius: '999px',
                      backgroundColor: preview?.featured_is_fallback ? 'var(--h5-bg-tertiary)' : 'var(--h5-accent-light)',
                      color: preview?.featured_is_fallback ? 'var(--h5-text-tertiary)' : 'var(--h5-accent-text)',
                      fontWeight: 600,
                    }}
                  >
                    {preview?.featured_is_fallback ? '热门兜底 · 前4本' : `人工精选 · ${featured.length}本`}
                  </span>
                </div>

                {featured.length === 0 ? (
                  <div
                    className="p-4 text-center rounded-xl text-xs"
                    style={{
                      backgroundColor: 'var(--h5-bg-secondary)',
                      border: '1px dashed var(--h5-border)',
                      color: 'var(--h5-text-tertiary)',
                    }}
                  >
                    暂无已上架书籍可预览
                  </div>
                ) : (
                  <div className="flex gap-2.5 overflow-x-auto pb-1">
                    {featured.map((item) => {
                      const isPinned = item.source === 'pinned';
                      return (
                        <div
                          key={`feat-${item.novel.id}`}
                          className="shrink-0 flex items-center gap-3 select-none"
                          style={{
                            width: featured.length > 1 ? '88%' : '100%',
                            borderRadius: '14px',
                            backgroundColor: 'var(--h5-hero-bg)',
                            color: 'var(--h5-hero-text)',
                            padding: '12px',
                            border: '1px solid var(--h5-border)',
                          }}
                        >
                          <img
                            src={item.novel.cover_url || FALLBACK_COVER}
                            alt={item.novel.title}
                            className="w-16 aspect-[3/4] object-cover rounded-lg shrink-0"
                            style={{ border: '1px solid rgba(255,255,255,0.16)' }}
                          />
                          <div className="min-w-0 flex-1 space-y-1">
                            <div className="flex items-center justify-between gap-1.5 text-[10px]" style={{ color: 'rgba(250,248,245,0.72)' }}>
                              <span className="inline-flex items-center gap-1 truncate">
                                <span>{item.novel.genres?.[0] || 'Fiction'}</span>
                                <span>·</span>
                                <span className="inline-flex items-center gap-0.5 font-semibold" style={{ color: '#E2BB6B' }}>
                                  <Star size={10} fill="currentColor" strokeWidth={0} aria-hidden="true" />
                                  {item.novel.rating}
                                </span>
                              </span>
                              <span
                                className="px-1.5 py-0.5 rounded-full shrink-0"
                                style={{
                                  backgroundColor: isPinned ? 'rgba(212,174,104,0.2)' : 'rgba(255,255,255,0.1)',
                                  color: isPinned ? '#E2BB6B' : 'rgba(250,248,245,0.7)',
                                  fontSize: '9px',
                                }}
                              >
                                {isPinned ? '人工' : '兜底'}
                              </span>
                            </div>

                            <div
                              className="line-clamp-1"
                              style={{
                                fontFamily: "'Iowan Old Style', Georgia, serif",
                                fontSize: '0.875rem',
                                fontWeight: 600,
                                color: '#FAF8F5',
                              }}
                            >
                              {item.novel.title}
                            </div>

                            <div
                              className="line-clamp-2"
                              style={{
                                fontSize: '0.6875rem',
                                lineHeight: 1.4,
                                color: 'rgba(250,248,245,0.72)',
                              }}
                            >
                              {item.novel.synopsis || `By ${item.novel.author}`}
                            </div>

                            <div className="inline-flex items-center gap-1 text-[11px] font-semibold" style={{ color: '#E2BB6B' }}>
                              <span>Read Story</span>
                              <ArrowRight size={11} aria-hidden="true" />
                            </div>
                          </div>
                        </div>
                      );
                    })}
                  </div>
                )}
              </section>

              {/* Section 2: New Releases (System Auto) */}
              {newReleases.length > 0 && (
                <section className="space-y-2">
                  <div className="flex items-center justify-between gap-2">
                    <h4
                      className="m-0"
                      style={{
                        fontFamily: "'Iowan Old Style', Georgia, serif",
                        fontSize: '0.95rem',
                        fontWeight: 600,
                        color: 'var(--h5-text-primary)',
                      }}
                    >
                      New Releases
                    </h4>
                    <span style={{ fontSize: '0.625rem', color: 'var(--h5-text-tertiary)' }}>
                      系统自动 · 最新 6 本
                    </span>
                  </div>

                  <div className="grid grid-cols-3 gap-2">
                    {newReleases.slice(0, 6).map((item) => (
                      <div key={`new-${item.novel.id}`} className="min-w-0">
                        <div
                          className="relative aspect-[3/4] rounded-lg overflow-hidden mb-1"
                          style={{
                            border: '1px solid var(--h5-border)',
                            backgroundColor: 'var(--h5-bg-tertiary)',
                          }}
                        >
                          <img
                            src={item.novel.cover_url || FALLBACK_COVER}
                            alt={item.novel.title}
                            className="w-full h-full object-cover"
                          />
                          <div
                            className="absolute bottom-1 right-1 px-1.5 py-0.5 rounded-full inline-flex items-center gap-0.5 tabular-nums"
                            style={{
                              backgroundColor: 'rgba(20,20,19,0.82)',
                              color: '#E2BB6B',
                              fontSize: '9px',
                              fontWeight: 600,
                            }}
                          >
                            <Star size={8} fill="currentColor" strokeWidth={0} aria-hidden="true" />
                            <span>{item.novel.rating}</span>
                          </div>
                        </div>
                        <div
                          className="truncate"
                          style={{ fontSize: '0.6875rem', fontWeight: 600, color: 'var(--h5-text-primary)' }}
                        >
                          {item.novel.title}
                        </div>
                        <div
                          className="truncate"
                          style={{ fontSize: '0.625rem', color: 'var(--h5-text-tertiary)' }}
                        >
                          {item.novel.author}
                        </div>
                      </div>
                    ))}
                  </div>
                </section>
              )}

              {/* Section 3: Curated For You (curated) */}
              <section className="space-y-2">
                <div className="flex items-center justify-between gap-2">
                  <h4
                    className="m-0"
                    style={{
                      fontFamily: "'Iowan Old Style', Georgia, serif",
                      fontSize: '0.95rem',
                      fontWeight: 600,
                      color: 'var(--h5-text-primary)',
                    }}
                  >
                    Curated For You
                  </h4>
                  <span
                    style={{
                      fontSize: '0.625rem',
                      padding: '2px 7px',
                      borderRadius: '999px',
                      backgroundColor: preview?.curated_is_override ? 'var(--h5-accent-light)' : 'var(--h5-bg-tertiary)',
                      color: preview?.curated_is_override ? 'var(--h5-accent-text)' : 'var(--h5-text-tertiary)',
                      fontWeight: 600,
                    }}
                  >
                    {preview?.curated_is_override ? `已接管 · ${curated.length}本` : '默认目录流'}
                  </span>
                </div>

                {curated.length === 0 ? (
                  <div
                    className="p-4 text-center rounded-xl text-xs"
                    style={{
                      backgroundColor: 'var(--h5-bg-secondary)',
                      border: '1px dashed var(--h5-border)',
                      color: 'var(--h5-text-tertiary)',
                    }}
                  >
                    暂无可展示书单
                  </div>
                ) : (
                  <div className="divide-y" style={{ borderColor: 'var(--h5-border)' }}>
                    {curated.map((item) => {
                      return (
                        <div
                          key={`cur-${item.novel.id}`}
                          className="py-2.5 flex gap-2.5 select-none"
                          style={{ borderColor: 'var(--h5-border)' }}
                        >
                          <img
                            src={item.novel.cover_url || FALLBACK_COVER}
                            alt={item.novel.title}
                            className="w-12 h-16 object-cover rounded-md shrink-0"
                            style={{ border: '1px solid var(--h5-border)' }}
                          />
                          <div className="min-w-0 flex-1 flex flex-col justify-between">
                            <div>
                              <div className="flex items-center justify-between gap-1.5">
                                <span
                                  className="truncate"
                                  style={{ fontSize: '0.8125rem', fontWeight: 600, color: 'var(--h5-text-primary)' }}
                                >
                                  {item.novel.title}
                                </span>
                                {renderSourceBadge(item)}
                              </div>
                              <p
                                className="m-0 mt-0.5 line-clamp-2"
                                style={{ fontSize: '0.6875rem', lineHeight: 1.4, color: 'var(--h5-text-secondary)' }}
                              >
                                {item.novel.synopsis || `By ${item.novel.author}`}
                              </p>
                            </div>
                            <div className="flex items-center justify-between gap-2 mt-1" style={{ fontSize: '0.625rem' }}>
                              <span className="truncate" style={{ color: 'var(--h5-text-tertiary)' }}>
                                {item.novel.author}
                              </span>
                              <div className="flex items-center gap-1.5 shrink-0 tabular-nums">
                                {item.novel.genres?.[0] && (
                                  <span
                                    className="px-1.5 py-0.5 rounded-full"
                                    style={{
                                      backgroundColor: 'var(--h5-bg-tertiary)',
                                      color: 'var(--h5-text-secondary)',
                                      border: '1px solid var(--h5-border)',
                                    }}
                                  >
                                    {item.novel.genres[0]}
                                  </span>
                                )}
                                <span className="inline-flex items-center gap-0.5 font-semibold" style={{ color: 'var(--h5-accent-text)' }}>
                                  <Star size={9} fill="currentColor" strokeWidth={0} aria-hidden="true" />
                                  {item.novel.rating}
                                </span>
                              </div>
                            </div>
                          </div>
                        </div>
                      );
                    })}
                  </div>
                )}

                {preview?.curated_is_override ? (
                  <div
                    className="text-center py-2 px-3 rounded-lg text-[11px]"
                    style={{
                      backgroundColor: 'var(--h5-bg-secondary)',
                      border: '1px dashed var(--h5-border)',
                      color: 'var(--h5-text-tertiary)',
                    }}
                  >
                    当前已接管精选列表 · H5 不展示 Load More 分页按钮
                  </div>
                ) : (
                  <div
                    className="w-full py-2 text-center rounded-xl text-xs font-semibold"
                    style={{
                      backgroundColor: 'var(--h5-bg-secondary)',
                      border: '1px solid var(--h5-border-strong)',
                      color: 'var(--h5-text-primary)',
                    }}
                  >
                    Load More Stories
                  </div>
                )}
              </section>
            </>
          ) : (
            /* Top Charts View */
            <section className="space-y-2.5">
              <div className="flex items-end justify-between gap-2">
                <div>
                  <div
                    style={{
                      fontSize: '0.625rem',
                      fontWeight: 600,
                      textTransform: 'uppercase',
                      letterSpacing: '0.1em',
                      color: 'var(--h5-accent-text)',
                    }}
                  >
                    Most Read
                  </div>
                  <h4
                    className="m-0"
                    style={{
                      fontFamily: "'Iowan Old Style', Georgia, serif",
                      fontSize: '0.95rem',
                      fontWeight: 600,
                      color: 'var(--h5-text-primary)',
                    }}
                  >
                    Popular Rankings
                  </h4>
                </div>
                <span className="tabular-nums" style={{ fontSize: '0.6875rem', color: 'var(--h5-text-tertiary)' }}>
                  置顶 {pinnedRankCount} + 补全 {autoRankCount} = {rankings.length} 本
                </span>
              </div>

              {preview?.ranking_manual_fallback && (
                <div
                  className="p-2.5 rounded-xl text-[11px] leading-relaxed"
                  style={{
                    backgroundColor: 'var(--h5-accent-light)',
                    color: 'var(--h5-accent-text)',
                    border: '1px solid var(--h5-accent)',
                  }}
                >
                  纯人工模式当前无生效书籍，H5 端已按阅读量自动兜底展示。
                </div>
              )}

              {rankings.length === 0 ? (
                <div
                  className="p-6 text-center rounded-xl text-xs"
                  style={{
                    backgroundColor: 'var(--h5-bg-secondary)',
                    border: '1px dashed var(--h5-border)',
                    color: 'var(--h5-text-tertiary)',
                  }}
                >
                  暂无符合条件的榜单书籍
                </div>
              ) : (
                <div className="space-y-2">
                  {rankings.map((item, idx) => {
                    const rank = idx + 1;

                    let rankBg = 'var(--h5-bg-tertiary)';
                    let rankFg = 'var(--h5-text-tertiary)';
                    let rankBorder = 'none';
                    if (rank === 1) {
                      rankBg = 'var(--h5-cta-bg)';
                      rankFg = 'var(--h5-cta-text)';
                    } else if (rank === 2) {
                      rankBg = 'var(--h5-accent-light)';
                      rankFg = 'var(--h5-accent-text)';
                      rankBorder = '1px solid var(--h5-accent)';
                    } else if (rank === 3) {
                      rankBg = 'var(--h5-bg-tertiary)';
                      rankFg = 'var(--h5-text-primary)';
                      rankBorder = '1px solid var(--h5-border-strong)';
                    }

                    return (
                      <div
                        key={`rank-${item.novel.id}`}
                        className="flex items-center gap-2.5 p-2.5 rounded-xl select-none"
                        style={{
                          backgroundColor: 'var(--h5-bg-secondary)',
                          border: '1px solid var(--h5-border)',
                        }}
                      >
                        <div
                          className="w-6 h-6 rounded-md flex items-center justify-center text-xs font-bold tabular-nums shrink-0"
                          style={{ backgroundColor: rankBg, color: rankFg, border: rankBorder }}
                        >
                          {rank}
                        </div>

                        <img
                          src={item.novel.cover_url || FALLBACK_COVER}
                          alt={item.novel.title}
                          className="w-10 h-14 object-cover rounded-md shrink-0"
                          style={{ border: '1px solid var(--h5-border)' }}
                        />

                        <div className="min-w-0 flex-1">
                          <div className="flex items-center justify-between gap-1.5">
                            <span
                              className="truncate text-xs font-semibold"
                              style={{ color: 'var(--h5-text-primary)' }}
                            >
                              {item.novel.title}
                            </span>
                            {renderSourceBadge(item)}
                          </div>
                          <div
                            className="truncate mt-0.5"
                            style={{ fontSize: '0.6875rem', color: 'var(--h5-text-tertiary)' }}
                          >
                            By {item.novel.author}
                          </div>
                          <div className="flex items-center justify-between gap-2 mt-1 text-[10px] tabular-nums">
                            <span style={{ color: 'var(--h5-accent-text)', fontWeight: 500 }}>
                              {item.novel.genres?.[0] || 'Fiction'}
                            </span>
                            <div className="flex items-center gap-2">
                              <span className="inline-flex items-center gap-0.5 font-semibold" style={{ color: 'var(--h5-accent-text)' }}>
                                <Star size={9} fill="currentColor" strokeWidth={0} aria-hidden="true" />
                                {item.novel.rating}
                              </span>
                              <span className="inline-flex items-center gap-0.5" style={{ color: 'var(--h5-text-tertiary)' }}>
                                <Flame size={10} aria-hidden="true" />
                                {formatCompactNumber(item.novel.view_count || 0)}
                              </span>
                            </div>
                          </div>
                        </div>
                      </div>
                    );
                  })}
                </div>
              )}
            </section>
          )}
        </div>

        {/* Mobile Bottom Navigation mirroring reader-front BottomNav */}
        <div
          className="shrink-0 grid grid-cols-4 pt-2 pb-2.5 px-2 select-none"
          style={{
            borderTop: '1px solid var(--h5-border)',
            backgroundColor: 'var(--h5-bg-secondary)',
          }}
        >
          {(
            [
              { id: 'shelf', label: 'Library', Icon: BookOpen, active: false },
              { id: 'home', label: 'Discover', Icon: Compass, active: true },
              { id: 'rewards', label: 'Rewards', Icon: Gift, active: false },
              { id: 'profile', label: 'Profile', Icon: User, active: false },
            ] as const
          ).map(({ id, label, Icon, active }) => (
            <div
              key={id}
              className="flex flex-col items-center justify-center gap-0.5"
              style={{
                color: active ? 'var(--h5-accent-text)' : 'var(--h5-text-tertiary)',
                fontSize: '10px',
                fontWeight: active ? 600 : 500,
              }}
            >
              <Icon size={17} strokeWidth={active ? 2 : 1.65} aria-hidden="true" />
              <span>{label}</span>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
};
