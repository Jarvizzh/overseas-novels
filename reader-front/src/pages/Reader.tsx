import React, { useState, useEffect, useRef, useCallback } from 'react';
import { ChevronLeft, ChevronRight, List, Lock, BookOpen } from 'lucide-react';
import { useToast } from '../context/toastStore';
import { api, ApiError } from '../utils/api';
import type { Novel, Chapter, ChapterContentResponse } from '../utils/api';
import { ReaderSettings, type ReaderFontFamily, type ReaderLineHeight } from '../components/ReaderSettings';
import { Drawer } from '../components/Drawer';
import { GoldCoin } from '../components/GoldCoin';
import { readerCache } from '../utils/readerCache';

interface ReaderProps {
  userId: number;
  authReady: boolean;
  isVip: boolean;
  novelId: number;
  chapterIndex: number;
  novels: Novel[];
  onNavigate: (page: string, params?: Record<string, unknown>) => void;
  onSyncChapterUrl: (bookId: number, chapterIndex: number) => void;
  shelfBookIds: number[];
  onToggleShelf: (bookId: number) => void;
  readingProgress: {
    [bookId: number]: {
      chapterIndex: number;
      scrollOffsetPercentage: number;
      paragraphIndex?: number;
    };
  };
  onSaveProgress: (bookId: number, chapterIndex: number, scrollOffsetPercentage: number, paragraphIndex?: number) => void;
  unlockedBookChapters: string[];
  onMarkChapterUnlocked: (bookId: number, chapterIndex: number) => void;
  onUnmarkChapterUnlocked: (bookId: number, chapterIndex: number) => void;
  onUnlockChapter: (bookId: number, chapterIndex: number, price: number) => Promise<boolean>;
  userCoins: number;
}

function normalizeReaderTheme(raw: string | null): string {
  if (!raw || raw === 'day') return 'paper';
  if (raw === 'mint') return 'forest';
  if (['paper', 'sepia', 'forest', 'night'].includes(raw)) return raw;
  return 'paper';
}

function splitParagraphs(content: string): string[] {
  const doubleBreaks = content
    .split(/\n{2,}/)
    .map((p) => p.trim())
    .filter(Boolean);
  if (doubleBreaks.length > 1) return doubleBreaks;

  return content
    .split(/\n/)
    .map((p) => p.trim())
    .filter(Boolean);
}

const LINE_HEIGHT_MAP: Record<ReaderLineHeight, number> = {
  compact: 1.5,
  standard: 1.68,
  relaxed: 1.85,
};

export const Reader: React.FC<ReaderProps> = ({
  userId,
  authReady,
  isVip,
  novelId,
  chapterIndex: initialChapterIndex,
  onNavigate,
  onSyncChapterUrl,
  shelfBookIds,
  onToggleShelf,
  readingProgress,
  onSaveProgress,
  unlockedBookChapters,
  onMarkChapterUnlocked,
  onUnmarkChapterUnlocked,
  onUnlockChapter,
  userCoins,
}) => {
  const { showToast } = useToast();
  const [novel, setNovel] = useState<Novel | null>(null);
  const [chapters, setChapters] = useState<Chapter[]>([]);
  const [chapterDetail, setChapterDetail] = useState<Chapter | null>(null);
  const [chapterAccessType, setChapterAccessType] = useState<'free' | 'chapter' | 'pass' | null>(null);
  const [isLocked, setIsLocked] = useState<boolean>(false);
  const [chapterPrice, setChapterPrice] = useState<number>(50);
  const [loading, setLoading] = useState(true);
  const [novelError, setNovelError] = useState(false);
  const [currentChIndex, setCurrentChIndex] = useState(initialChapterIndex);

  const scrollRef = useRef<HTMLDivElement>(null);
  const isTransitioning = useRef(false);
  const loadSeqRef = useRef(0);
  const currentActiveRef = useRef({ novelId, chapterIndex: initialChapterIndex });

  useEffect(() => {
    currentActiveRef.current = { novelId, chapterIndex: currentChIndex };
  }, [novelId, currentChIndex]);

  const readingProgressRef = useRef(readingProgress);
  useEffect(() => {
    readingProgressRef.current = readingProgress;
  }, [readingProgress]);

  const onSaveProgressRef = useRef(onSaveProgress);
  useEffect(() => {
    onSaveProgressRef.current = onSaveProgress;
  }, [onSaveProgress]);

  const chaptersRef = useRef(chapters);
  useEffect(() => {
    chaptersRef.current = chapters;
  }, [chapters]);

  const onUnlockChapterRef = useRef(onUnlockChapter);
  useEffect(() => {
    onUnlockChapterRef.current = onUnlockChapter;
  }, [onUnlockChapter]);

  const onMarkChapterUnlockedRef = useRef(onMarkChapterUnlocked);
  useEffect(() => {
    onMarkChapterUnlockedRef.current = onMarkChapterUnlocked;
  }, [onMarkChapterUnlocked]);

  const onUnmarkChapterUnlockedRef = useRef(onUnmarkChapterUnlocked);
  useEffect(() => {
    onUnmarkChapterUnlockedRef.current = onUnmarkChapterUnlocked;
  }, [onUnmarkChapterUnlocked]);

  const unlockedBookChaptersRef = useRef(unlockedBookChapters);
  useEffect(() => {
    unlockedBookChaptersRef.current = unlockedBookChapters;
  }, [unlockedBookChapters]);

  const userIdRef = useRef(userId);
  useEffect(() => {
    userIdRef.current = userId;
  }, [userId]);

  const isVipRef = useRef(isVip);
  useEffect(() => {
    isVipRef.current = isVip;
  }, [isVip]);

  // Typography & surface settings (default: Humanist Serif + Warm Paper)
  const [fontSize, setFontSize] = useState<number>(() => {
    const parsed = parseInt(localStorage.getItem('reader-font-size') || '18', 10);
    return Number.isNaN(parsed) ? 18 : Math.min(28, Math.max(14, parsed));
  });
  const [theme, setTheme] = useState<string>(() => normalizeReaderTheme(localStorage.getItem('reader-theme')));
  const [fontFamily, setFontFamily] = useState<ReaderFontFamily>(() => {
    const saved = localStorage.getItem('reader-font-family');
    return saved === 'sans' ? 'sans' : 'serif';
  });
  const [lineHeight, setLineHeight] = useState<ReaderLineHeight>(() => {
    const saved = localStorage.getItem('reader-line-height');
    if (saved === 'compact' || saved === 'relaxed') return saved;
    return 'standard';
  });

  const handleFontSizeChange = (newSize: number) => {
    setFontSize(newSize);
    localStorage.setItem('reader-font-size', String(newSize));
  };

  const handleThemeChange = (newTheme: string) => {
    setTheme(newTheme);
    localStorage.setItem('reader-theme', newTheme);
  };

  const handleFontFamilyChange = (family: ReaderFontFamily) => {
    setFontFamily(family);
    localStorage.setItem('reader-font-family', family);
  };

  const handleLineHeightChange = (lh: ReaderLineHeight) => {
    setLineHeight(lh);
    localStorage.setItem('reader-line-height', lh);
  };

  const [showSettings, setShowSettings] = useState(false);
  const [showTOC, setShowTOC] = useState(false);

  const [autoUnlock, setAutoUnlock] = useState<boolean>(() => {
    return localStorage.getItem('reader-auto-unlock') === 'true';
  });

  const autoUnlockRef = useRef(autoUnlock);
  useEffect(() => {
    autoUnlockRef.current = autoUnlock;
  }, [autoUnlock]);

  const userCoinsRef = useRef(userCoins);
  useEffect(() => {
    userCoinsRef.current = userCoins;
  }, [userCoins]);

  // Screen Wake Lock while reading
  useEffect(() => {
    let wakeLock: { release?: () => Promise<void> } | null = null;
    const requestWakeLock = async () => {
      try {
        if ('wakeLock' in navigator) {
          wakeLock = await navigator.wakeLock.request('screen');
        }
      } catch {
        // Fallback gracefully if battery saver blocks wakeLock
      }
    };
    requestWakeLock();
    return () => {
      if (wakeLock?.release) {
        wakeLock.release().catch(() => {});
      }
    };
  }, []);

  const fetchChapterData = useCallback(
    async (nId: number, cIdx: number): Promise<ChapterContentResponse> => {
      const uid = userIdRef.current;
      const knownChapter = chaptersRef.current.find((c) => c.novel_id === nId && c.chapter_index === cIdx);
      const isFreeChapter = knownChapter ? !knownChapter.is_paid : false;
      const isLocallyUnlocked = unlockedBookChaptersRef.current.includes(`${nId}-${cIdx}`);
      const cached = await readerCache.getCachedChapter(uid, nId, cIdx);

      // Strategy 1 & 2: Free, VIP, or verified locally unlocked chapters -> Cache-first + SWR
      if (cached && !cached.locked && (cached.access_type === 'free' ? isFreeChapter : cached.access_type === 'chapter' && isLocallyUnlocked)) {
        api
          .getChapterContent(nId, cIdx)
          .then(async (fresh) => {
            if (!fresh) return;
            if (fresh.locked) {
              // Access expired or revoked on server: purge stale IndexedDB cache & relock if still viewing
              await readerCache.removeCachedChapter(uid, nId, cIdx);
              onUnmarkChapterUnlockedRef.current(nId, cIdx);
              if (currentActiveRef.current.novelId === nId && currentActiveRef.current.chapterIndex === cIdx) {
                setChapterDetail(fresh.chapter);
                setIsLocked(true);
                setChapterPrice(fresh.price ?? 0);
              }
            } else {
              await readerCache.setCachedChapter(uid, nId, cIdx, fresh);
            }
          })
          .catch(() => {});
        return cached;
      }

      // Strategy 3: Network-first for unverified / paid chapters
      try {
        const fresh = await api.getChapterContent(nId, cIdx);
        if (fresh && !fresh.locked) {
          await readerCache.setCachedChapter(uid, nId, cIdx, fresh);
          if (fresh.access_type === 'chapter') {
            onMarkChapterUnlockedRef.current(nId, cIdx);
          }
        } else if (fresh?.locked) {
          await readerCache.removeCachedChapter(uid, nId, cIdx);
        }
        return fresh;
      } catch (err) {
        if (
          cached &&
          !cached.locked &&
          (cached.access_type === 'free'
            ? isFreeChapter
            : cached.access_type === 'chapter'
              ? isLocallyUnlocked
              : cached.access_type === 'pass' && isVipRef.current)
        ) {
          return cached;
        }
        throw err;
      }
    },
    []
  );

  // 1. Fetch novel metadata & chapters list once auth settles
  useEffect(() => {
    if (!authReady) return;
    let cancelled = false;
    setNovelError(false);
    setNovel(null);
    setChapters([]);
    setChapterDetail(null);
    setChapterAccessType(null);

    const loadNovelMeta = async () => {
      try {
        const [detail, chList] = await Promise.all([
          api.getNovelDetail(novelId),
          api.getChaptersList(novelId).catch(() => []),
        ]);
        if (cancelled) return;
        if (!detail) {
          setNovelError(true);
          return;
        }
        setNovel(detail);
        setChapters(chList || []);
      } catch (err) {
        if (!cancelled) {
          console.error('Failed to load novel metadata:', err);
          setNovelError(true);
        }
      }
    };
    loadNovelMeta();
    return () => {
      cancelled = true;
    };
  }, [novelId, authReady, userId]);

  // Trigger bounded prefetch once chapters list is known
  useEffect(() => {
    if (!authReady || !userId || chapters.length === 0) return;
    readerCache.prefetchChapters(
      userId,
      novelId,
      currentChIndex,
      chapters.length,
      (nId, idx) => api.getChapterContent(nId, idx)
    );
  }, [authReady, userId, novelId, currentChIndex, chapters.length]);

  // 2. Load specified chapter with sequence guard against rapid clicks
  const loadChapter = useCallback(
    async (targetIdx: number, restoreProgress = false) => {
      const seq = ++loadSeqRef.current;
      setLoading(true);
      setChapterAccessType(null);
      isTransitioning.current = true;
      setCurrentChIndex(targetIdx);

      try {
        let res = await fetchChapterData(novelId, targetIdx);
        if (seq !== loadSeqRef.current) return;

        if (res.locked && autoUnlockRef.current && userCoinsRef.current >= res.price) {
          try {
            const success = await onUnlockChapterRef.current(novelId, targetIdx, res.price);
            if (seq !== loadSeqRef.current) return;
            if (success) {
              showToast(`Unlocked ${res.chapter.title || `Chapter ${targetIdx + 1}`}`, 'success');
              res = await api.getChapterContent(novelId, targetIdx);
              if (seq !== loadSeqRef.current) return;
              if (!res.locked) {
                await readerCache.setCachedChapter(userIdRef.current, novelId, targetIdx, res);
                if (res.access_type === 'chapter') {
                  onMarkChapterUnlockedRef.current(novelId, targetIdx);
                }
              }
            }
          } catch (unlockErr) {
            if (seq !== loadSeqRef.current) return;
            if (unlockErr instanceof ApiError && unlockErr.status === 409) {
              res = await api.getChapterContent(novelId, targetIdx);
              if (seq !== loadSeqRef.current) return;
              showToast('Chapter price was updated. Please confirm before unlocking.', 'info');
            }
          }
        }

        if (seq !== loadSeqRef.current) return;
        setChapterDetail(res.chapter);
        setIsLocked(res.locked);
        setChapterPrice(res.price ?? 0);
        setChapterAccessType(res.access_type ?? null);

        requestAnimationFrame(() => {
          if (seq !== loadSeqRef.current) return;
          const container = scrollRef.current;
          if (container) {
            if (restoreProgress) {
              const savedProgress = readingProgressRef.current[novelId];
              if (savedProgress && savedProgress.chapterIndex === targetIdx) {
                const maxScroll = container.scrollHeight - container.clientHeight;
                if (maxScroll > 0) {
                  container.scrollTop = savedProgress.scrollOffsetPercentage * maxScroll;
                }
              } else {
                container.scrollTop = 0;
              }
            } else {
              container.scrollTop = 0;
              onSaveProgressRef.current(novelId, targetIdx, 0);
            }
          }
          isTransitioning.current = false;
        });
      } catch (err) {
        if (seq !== loadSeqRef.current) return;
        console.error('Failed to load chapter content:', err);
        showToast('Failed to load chapter content.', 'error');
        isTransitioning.current = false;
      } finally {
        if (seq === loadSeqRef.current) {
          setLoading(false);
        }
      }
    },
    [novelId, fetchChapterData, showToast]
  );

  const mountedNovelId = useRef<number | null>(null);
  const mountedInitialIndex = useRef<number | null>(null);
  const mountedUserId = useRef<number | null>(null);

  useEffect(() => {
    if (!authReady) return;
    if (
      mountedNovelId.current !== novelId ||
      mountedInitialIndex.current !== initialChapterIndex ||
      mountedUserId.current !== userId
    ) {
      mountedNovelId.current = novelId;
      mountedInitialIndex.current = initialChapterIndex;
      mountedUserId.current = userId;
      loadChapter(initialChapterIndex, true);
    }
  }, [authReady, userId, novelId, initialChapterIndex, loadChapter]);

  useEffect(() => {
    localStorage.setItem('reader-auto-unlock', autoUnlock.toString());
  }, [autoUnlock]);

  const handleChapterSelect = useCallback(
    (targetIdx: number) => {
      const total = chaptersRef.current.length;
      if (targetIdx < 0 || (total > 0 && targetIdx >= total)) return;
      setShowTOC(false);
      setShowSettings(false);
      mountedInitialIndex.current = targetIdx;
      onSyncChapterUrl(novelId, targetIdx);
      loadChapter(targetIdx, false);
    },
    [novelId, onSyncChapterUrl, loadChapter]
  );

  const scrollTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  useEffect(() => {
    return () => {
      if (scrollTimeoutRef.current) clearTimeout(scrollTimeoutRef.current);
    };
  }, []);

  const handleScroll = () => {
    if (isTransitioning.current || isLocked || loading) return;
    const container = scrollRef.current;
    if (!container) return;

    if (scrollTimeoutRef.current) {
      clearTimeout(scrollTimeoutRef.current);
    }

    scrollTimeoutRef.current = setTimeout(() => {
      const { scrollTop, scrollHeight, clientHeight } = container;
      const maxScroll = scrollHeight - clientHeight;
      if (maxScroll > 0) {
        const percentage = Math.min(1, Math.max(0, scrollTop / maxScroll));
        onSaveProgressRef.current(novelId, currentChIndex, percentage);
      }
    }, 200);
  };

  const [isUnlocking, setIsUnlocking] = useState(false);

  const handleUnlockNow = async () => {
    if (isUnlocking) return;
    const unlockSeq = loadSeqRef.current;
    setIsUnlocking(true);
    try {
      if (userCoins >= chapterPrice) {
        const success = await onUnlockChapter(novelId, currentChIndex, chapterPrice);
        if (unlockSeq !== loadSeqRef.current) return;
        if (success) {
          showToast('Chapter unlocked!', 'success');
          const res = await api.getChapterContent(novelId, currentChIndex);
          if (unlockSeq !== loadSeqRef.current) return;
          if (!res.locked) {
            await readerCache.setCachedChapter(userId, novelId, currentChIndex, res);
            if (res.access_type === 'chapter') {
              onMarkChapterUnlocked(novelId, currentChIndex);
            }
          }
          setChapterDetail(res.chapter);
          setIsLocked(res.locked);
          setChapterPrice(res.price ?? chapterPrice);
          setChapterAccessType(res.access_type ?? null);
        }
      } else {
        onNavigate('recharge', { id: novelId });
      }
    } catch (err) {
      if (unlockSeq !== loadSeqRef.current) return;
      if (err instanceof ApiError && err.status === 409) {
        showToast('Chapter price updated. Refreshing price...', 'info');
        const refreshed = await api.getChapterContent(novelId, currentChIndex).catch(() => null);
        if (unlockSeq !== loadSeqRef.current) return;
        if (refreshed) {
          setChapterDetail(refreshed.chapter);
          setIsLocked(refreshed.locked);
          setChapterPrice(refreshed.price ?? chapterPrice);
          setChapterAccessType(refreshed.access_type ?? null);
        }
      } else if (err instanceof ApiError && err.status === 402) {
        showToast('Insufficient balance. Please top up coins.', 'error');
        onNavigate('recharge', { id: novelId });
      } else {
        const msg = err instanceof Error ? err.message : 'Failed to unlock chapter.';
        showToast(msg, 'error');
      }
    } finally {
      setIsUnlocking(false);
    }
  };

  const handleScreenClick = () => {
    setShowSettings((prev) => !prev);
  };

  const isInShelf = shelfBookIds.includes(novelId);
  const handleToggleShelf = () => {
    onToggleShelf(novelId);
    showToast(isInShelf ? 'Removed from library' : 'Added to library', 'success');
  };

  if (novelError) {
    return (
      <div className="scroll-container animate-fade-in">
        <div className="shelf-empty-state">
          <div className="shelf-empty-icon-wrap">
            <BookOpen size={24} strokeWidth={1.65} aria-hidden="true" />
          </div>
          <p style={{ fontWeight: 600, color: 'var(--text-primary)' }}>Story unavailable</p>
          <p style={{ fontSize: '0.8125rem' }}>This book is unavailable or has been taken offline.</p>
          <button type="button" className="btn-cta-primary" style={{ marginTop: '12px' }} onClick={() => onNavigate('home')}>
            Back to Store
          </button>
        </div>
      </div>
    );
  }

  if (!novel) {
    return (
      <div className="scroll-container animate-fade-in" style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', gap: '12px', color: 'var(--text-secondary)' }}>
        <div className="sn-spinner" />
        <p style={{ fontSize: '0.875rem' }}>Preparing reader...</p>
      </div>
    );
  }

  const currentChapterTitle = chapterDetail?.title || chapters[currentChIndex]?.title || `Chapter ${currentChIndex + 1}`;
  const totalChaptersCount = chapters.length || 1;
  const paragraphs = chapterDetail?.content ? splitParagraphs(chapterDetail.content) : [];

  return (
    <div className={`reader-container reader-theme-${theme} page-container-full`}>
      <button
        type="button"
        className={`reader-floating-back-btn ${showSettings ? 'hud-open' : ''}`}
        onClick={() => onNavigate('detail', { id: novelId })}
        aria-label="Back to book details"
      >
        <ChevronLeft size={19} strokeWidth={1.8} aria-hidden="true" />
      </button>

      <ReaderSettings
        visible={showSettings}
        fontSize={fontSize}
        onFontSizeChange={handleFontSizeChange}
        fontFamily={fontFamily}
        onFontFamilyChange={handleFontFamilyChange}
        lineHeight={lineHeight}
        onLineHeightChange={handleLineHeightChange}
        theme={theme}
        onThemeChange={handleThemeChange}
        onOpenDrawer={() => setShowTOC(true)}
        novelTitle={novel.title}
        onBack={() => onNavigate('detail', { id: novelId })}
        isInShelf={isInShelf}
        onAddToShelf={handleToggleShelf}
      />

      <Drawer
        visible={showTOC}
        onClose={() => setShowTOC(false)}
        title="Table of Contents"
      >
        <div className="chapters-list" style={{ padding: '4px 10px' }}>
          {chapters.map((ch, index) => {
            const chIdx = ch.chapter_index ?? index;
            const isCurrent = chIdx === currentChIndex;
            const isChPaid = ch.is_paid !== undefined ? ch.is_paid : chIdx >= 2;
            const hasPassAccess = isVip || chapterAccessType === 'pass' || (isChPaid && ch.price === 0);
            const isChLocked = !hasPassAccess && isChPaid && !unlockedBookChapters.includes(`${novelId}-${chIdx}`);
            return (
              <button
                type="button"
                key={ch.id || chIdx}
                className="chapter-item-link"
                style={{
                  backgroundColor: isCurrent ? 'var(--accent-light)' : 'transparent',
                  paddingLeft: '12px',
                  paddingRight: '12px',
                  borderRadius: isCurrent ? '8px' : '0',
                }}
                onClick={() => handleChapterSelect(chIdx)}
              >
                <div style={{ display: 'flex', alignItems: 'center', gap: '8px', minWidth: 0 }}>
                  <span
                    className="chapter-item-title"
                    style={{
                      fontWeight: isCurrent ? 600 : 500,
                      color: isCurrent ? 'var(--accent-text)' : 'var(--text-primary)',
                    }}
                  >
                    {ch.title || `Chapter ${index + 1}`}
                  </span>
                  {isChLocked && (
                    <Lock size={13} strokeWidth={1.8} style={{ color: 'var(--text-tertiary)', flexShrink: 0 }} aria-label="Locked" />
                  )}
                </div>
                <span className="chapter-item-meta">{(ch.word_count || 0).toLocaleString()} w</span>
              </button>
            );
          })}
        </div>
      </Drawer>

      <div
        ref={scrollRef}
        className="reader-scroll-area"
        onScroll={handleScroll}
        onClick={handleScreenClick}
        style={{
          fontFamily: fontFamily === 'sans' ? 'var(--font-sans)' : 'var(--font-serif)',
          fontSize: `${fontSize}px`,
          lineHeight: LINE_HEIGHT_MAP[lineHeight],
        }}
      >
        {loading ? (
          <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', padding: '120px 20px', gap: '12px', color: 'var(--reader-muted)', fontFamily: 'var(--font-sans)' }}>
            <div className="sn-spinner" />
            <span style={{ fontSize: '0.875rem' }}>Loading chapter...</span>
          </div>
        ) : (
          <article className="reader-article">
            <div className="reader-chapter-eyebrow">
              Chapter {currentChIndex + 1} of {totalChaptersCount}
            </div>
            <header
              style={{
                borderBottom: '1px solid var(--reader-border)',
                paddingBottom: '16px',
                marginBottom: '28px',
              }}
            >
              <h1
                className="reader-chapter-title"
                style={{ borderBottom: 'none', paddingBottom: 0, marginBottom: 0 }}
              >
                {currentChapterTitle}
              </h1>
              {chapterDetail?.word_count ? (
                <div className="reader-chapter-meta">
                  {chapterDetail.word_count.toLocaleString()} words
                </div>
              ) : null}
            </header>

            {paragraphs.map((paragraph, pIdx) => (
              <p key={pIdx} data-paragraph-index={pIdx} className="reader-text-paragraph">
                {paragraph}
              </p>
            ))}

            {isLocked && (
              <div
                className="reader-paywall-card"
                onClick={(e) => e.stopPropagation()}
              >
                <div
                  style={{
                    width: '48px',
                    height: '48px',
                    borderRadius: '50%',
                    backgroundColor: 'var(--reader-hud-pill)',
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'center',
                    color: 'var(--reader-accent)',
                  }}
                >
                  <Lock size={20} strokeWidth={1.8} aria-hidden="true" />
                </div>
                <h2 style={{ fontFamily: 'var(--font-display)', fontSize: '1.1875rem', fontWeight: 600, color: 'var(--reader-text)' }}>
                  Unlock to Continue Reading
                </h2>
                <p style={{ fontSize: '0.8125rem', color: 'var(--reader-muted)' }}>
                  Full chapter length: {(chapterDetail?.word_count || 0).toLocaleString()} words
                </p>

                <div
                  style={{
                    width: '100%',
                    maxWidth: '300px',
                    borderTop: '1px solid var(--reader-border)',
                    paddingTop: '12px',
                    display: 'flex',
                    flexDirection: 'column',
                    gap: '8px',
                    fontSize: '0.8125rem',
                  }}
                >
                  <div style={{ display: 'flex', justifyContent: 'space-between' }}>
                    <span style={{ color: 'var(--reader-muted)' }}>Chapter Price</span>
                    <span className="tabular-nums" style={{ fontWeight: 600, color: 'var(--reader-text)', display: 'inline-flex', alignItems: 'center', gap: '5px' }}>
                      <GoldCoin size={14} />
                      <span>{chapterPrice} Coins</span>
                    </span>
                  </div>
                  <div style={{ display: 'flex', justifyContent: 'space-between' }}>
                    <span style={{ color: 'var(--reader-muted)' }}>Your Balance</span>
                    <span
                      className="tabular-nums"
                      style={{
                        fontWeight: 600,
                        color: userCoins >= chapterPrice ? '#16a34a' : '#dc2626',
                      }}
                    >
                      {userCoins.toLocaleString()} Coins
                    </span>
                  </div>
                </div>

                <label
                  htmlFor="auto-unlock-checkbox"
                  style={{
                    display: 'inline-flex',
                    alignItems: 'center',
                    gap: '8px',
                    fontSize: '0.8125rem',
                    color: 'var(--reader-muted)',
                    cursor: 'pointer',
                    marginTop: '2px',
                  }}
                >
                  <input
                    type="checkbox"
                    id="auto-unlock-checkbox"
                    checked={autoUnlock}
                    onChange={(e) => setAutoUnlock(e.target.checked)}
                    style={{ width: '16px', height: '16px', cursor: 'pointer' }}
                  />
                  <span>Auto-unlock next chapters</span>
                </label>

                <button
                  type="button"
                  onClick={handleUnlockNow}
                  disabled={isUnlocking}
                  style={{
                    width: '100%',
                    maxWidth: '300px',
                    minHeight: '46px',
                    borderRadius: '999px',
                    border: 'none',
                    display: 'inline-flex',
                    alignItems: 'center',
                    justifyContent: 'center',
                    gap: '8px',
                    fontSize: '0.9375rem',
                    fontWeight: 600,
                    cursor: isUnlocking ? 'not-allowed' : 'pointer',
                    marginTop: '4px',
                    backgroundColor: 'var(--reader-cta-bg)',
                    color: 'var(--reader-cta-text)',
                  }}
                >
                  {isUnlocking ? (
                    <span>Unlocking...</span>
                  ) : userCoins >= chapterPrice ? (
                    <>
                      <span>Unlock Chapter</span>
                      <span className="tabular-nums" style={{ opacity: 0.85 }}>· {chapterPrice} Coins</span>
                    </>
                  ) : (
                    <>
                      <span>Top Up Coins</span>
                      <ChevronRight size={16} strokeWidth={2.2} aria-hidden="true" />
                    </>
                  )}
                </button>
              </div>
            )}

            <div className="reader-bottom-nav-row" onClick={(e) => e.stopPropagation()}>
              <button
                type="button"
                className="reader-chapter-nav-btn"
                disabled={currentChIndex <= 0}
                onClick={() => handleChapterSelect(currentChIndex - 1)}
                aria-label="Previous chapter"
              >
                <ChevronLeft size={16} strokeWidth={2} aria-hidden="true" />
                <span>Previous</span>
              </button>

              <button
                type="button"
                className="reader-chapter-nav-btn reader-toc-nav-btn"
                onClick={() => setShowTOC(true)}
                aria-label="Table of Contents"
              >
                <List size={16} strokeWidth={1.9} aria-hidden="true" />
                <span>Contents</span>
              </button>

              <button
                type="button"
                className="reader-chapter-nav-btn primary"
                disabled={chapters.length > 0 && currentChIndex >= chapters.length - 1}
                onClick={() => handleChapterSelect(currentChIndex + 1)}
                aria-label="Next chapter"
              >
                <span>Next</span>
                <ChevronRight size={16} strokeWidth={2} aria-hidden="true" />
              </button>
            </div>
          </article>
        )}
      </div>
    </div>
  );
};
