import React, { useState, useEffect, useRef, useCallback } from 'react';
import { useToast } from '../context/ToastContext';
import { api } from '../utils/api';
import type { Novel, Chapter, ChapterContentResponse } from '../utils/api';
import { ReaderSettings } from '../components/ReaderSettings';
import { Drawer } from '../components/Drawer';
import { GoldCoin } from '../components/GoldCoin';
import { readerCache } from '../utils/readerCache';

interface ReaderProps {
  novelId: number;
  chapterIndex: number;
  novels: Novel[];
  onNavigate: (page: string, params?: any) => void;
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
  onUnlockChapter: (bookId: number, chapterIndex: number, price: number) => Promise<boolean>;
  userCoins: number;
}

export const Reader: React.FC<ReaderProps> = ({
  novelId,
  chapterIndex: initialChapterIndex,
  onNavigate,
  shelfBookIds,
  onToggleShelf,
  readingProgress,
  onSaveProgress,
  unlockedBookChapters,
  onUnlockChapter,
  userCoins,
}) => {
  const { showToast } = useToast();
  const [novel, setNovel] = useState<Novel | null>(null);
  const [chapters, setChapters] = useState<Chapter[]>([]);
  const [chapterDetail, setChapterDetail] = useState<Chapter | null>(null);
  const [isLocked, setIsLocked] = useState<boolean>(false);
  const [chapterPrice, setChapterPrice] = useState<number>(50);
  const [loading, setLoading] = useState(true);
  const [currentChIndex, setCurrentChIndex] = useState(initialChapterIndex);

  const scrollRef = useRef<HTMLDivElement>(null);
  const isTransitioning = useRef(false);

  // Refs for props to prevent re-triggering effects on parent re-renders
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

  const unlockedBookChaptersRef = useRef(unlockedBookChapters);
  useEffect(() => {
    unlockedBookChaptersRef.current = unlockedBookChapters;
  }, [unlockedBookChapters]);

  // Typography settings: font size only, default white theme
  const [fontSize, setFontSize] = useState<number>(() => {
    return parseInt(localStorage.getItem('reader-font-size') || '18', 10);
  });
  const theme = 'day'; // Default clean white theme

  const handleFontSizeChange = (newSize: number) => {
    setFontSize(newSize);
    localStorage.setItem('reader-font-size', newSize.toString());
  };

  const [showSettings, setShowSettings] = useState(false);
  const [showTOC, setShowTOC] = useState(false);

  // Auto-unlock chapters preference
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

  // Screen Wake Lock API: Keep screen awake while reading
  useEffect(() => {
    let wakeLock: any = null;
    const requestWakeLock = async () => {
      try {
        if ('wakeLock' in navigator) {
          wakeLock = await (navigator as any).wakeLock.request('screen');
        }
      } catch {
        // Fallback gracefully
      }
    };
    requestWakeLock();
    return () => {
      if (wakeLock && wakeLock.release) {
        wakeLock.release().catch(() => {});
      }
    };
  }, []);

  // Tiered Hybrid Caching & SWR strategy:
  // 1. Guaranteed Free Chapters (targetIdx < 2): Cache-First for 0ms instantaneous render.
  // 2. Previously Unlocked Chapters: Cache-First + SWR background revalidation.
  // 3. Unpurchased Paid Chapters: Network-First to strictly enforce server-side paywall & promo prices.
  const fetchChapterData = useCallback(
    async (nId: number, cIdx: number): Promise<ChapterContentResponse> => {
      const isFreeChapter = cIdx < 2;
      const isLocallyUnlocked = unlockedBookChaptersRef.current.includes(`${nId}-${cIdx}`);
      const cached = await readerCache.getCachedChapter(nId, cIdx);

      // Strategy 1: Free Chapters -> Pure Cache-First (0ms load)
      if (isFreeChapter && cached && !cached.locked) {
        // Asynchronously update in background if needed (SWR)
        api.getChapterContent(nId, cIdx).then(fresh => {
          if (fresh) readerCache.setCachedChapter(nId, cIdx, fresh);
        }).catch(() => {});
        return cached;
      }

      // Strategy 2: Authenticated Unlocked Chapters -> SWR (Fast Paint from Cache + Background Revalidation)
      if (isLocallyUnlocked && cached && !cached.locked) {
        // Revalidate in background to keep data fresh and synchronized with backend
        api.getChapterContent(nId, cIdx).then(fresh => {
          if (fresh) readerCache.setCachedChapter(nId, cIdx, fresh);
        }).catch(() => {});
        return cached;
      }

      // Strategy 3: Unpurchased Paid Chapters / No Valid Cache -> Network-First
      try {
        const fresh = await api.getChapterContent(nId, cIdx);
        if (fresh) {
          await readerCache.setCachedChapter(nId, cIdx, fresh);
        }
        return fresh;
      } catch (err) {
        // Offline Fallback: If offline and cached exists (and is either free or unlocked), fallback to cache
        if (cached && (!cached.locked && (isFreeChapter || isLocallyUnlocked))) {
          return cached;
        }
        throw err;
      }
    },
    []
  );

  // 1. Fetch novel metadata & chapters list
  useEffect(() => {
    const loadNovelMeta = async () => {
      try {
        const detail = await api.getNovelDetail(novelId);
        setNovel(detail);
        const chList = await api.getChaptersList(novelId);
        setChapters(chList);
      } catch (err) {
        console.error("Failed to load novel metadata:", err);
      }
    };
    loadNovelMeta();
  }, [novelId]);

  // 2. Load specified chapter function
  const loadChapter = useCallback(
    async (targetIdx: number, restoreProgress = false) => {
      setLoading(true);
      isTransitioning.current = true;
      setCurrentChIndex(targetIdx);

      // Sync URL parameter without full reload
      const search = new URLSearchParams(window.location.search);
      search.set('chapter_index', String(targetIdx + 1));
      window.history.replaceState(null, '', `${window.location.pathname}?${search.toString()}`);

      try {
        let res = await fetchChapterData(novelId, targetIdx);

        // Auto-unlock chapter if locked and autoUnlock preference is enabled with sufficient coins
        if (res.locked && autoUnlockRef.current && userCoinsRef.current >= res.price) {
          const success = await onUnlockChapterRef.current(novelId, targetIdx, res.price);
          if (success) {
            showToast(`Auto-unlocked ${res.chapter.title || `Chapter ${targetIdx + 1}`}!`, "success");
            res = await api.getChapterContent(novelId, targetIdx);
            await readerCache.setCachedChapter(novelId, targetIdx, res);
          }
        }

        setChapterDetail(res.chapter);
        setIsLocked(res.locked);
        setChapterPrice(res.price || 50);

        // Background prefetch next 2 chapters into IndexedDB
        readerCache.prefetchChapters(novelId, targetIdx, chaptersRef.current.length || 100, (nId, idx) =>
          api.getChapterContent(nId, idx)
        );

        // Manage scroll restoration or reset to top
        requestAnimationFrame(() => {
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
        console.error("Failed to load chapter content:", err);
        showToast("Failed to load chapter content.", "error");
        isTransitioning.current = false;
      } finally {
        setLoading(false);
      }
    },
    [novelId, fetchChapterData, showToast]
  );

  // Track initial load with refs so internal chapter navigation won't be re-triggered or reset
  const mountedNovelId = useRef<number | null>(null);
  const mountedInitialIndex = useRef<number | null>(null);

  useEffect(() => {
    if (mountedNovelId.current !== novelId || mountedInitialIndex.current !== initialChapterIndex) {
      mountedNovelId.current = novelId;
      mountedInitialIndex.current = initialChapterIndex;
      loadChapter(initialChapterIndex, true);
    }
  }, [novelId, initialChapterIndex, loadChapter]);

  // Sync settings changes to localStorage
  useEffect(() => {
    localStorage.setItem('reader-font-size', fontSize.toString());
  }, [fontSize]);

  useEffect(() => {
    localStorage.setItem('reader-auto-unlock', autoUnlock.toString());
  }, [autoUnlock]);

  // Handle active Chapter Change triggered by user click (Previous/Next/TOC jump)
  const handleChapterSelect = useCallback(
    (targetIdx: number) => {
      const total = chaptersRef.current.length;
      if (targetIdx < 0 || (total > 0 && targetIdx >= total)) return;
      setShowTOC(false);
      setShowSettings(false);
      mountedInitialIndex.current = targetIdx;
      loadChapter(targetIdx, false);
    },
    [loadChapter]
  );

  // Scroll Listener ONLY for debounced progress percentage tracking
  // DOES NOT switch chapters or load next/prev pages on scroll
  const scrollTimeoutRef = useRef<any>(null);
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

  // Chapter Unlock handling
  const [isUnlocking, setIsUnlocking] = useState(false);

  const handleUnlockNow = async () => {
    if (isUnlocking) return;
    setIsUnlocking(true);
    try {
      if (userCoins >= chapterPrice) {
        const success = await onUnlockChapter(novelId, currentChIndex, chapterPrice);
        if (success) {
          showToast("Unlocked successfully!", "success");
          const res = await api.getChapterContent(novelId, currentChIndex);
          await readerCache.setCachedChapter(novelId, currentChIndex, res);
          setChapterDetail(res.chapter);
          setIsLocked(res.locked);
        }
      } else {
        onNavigate('recharge');
      }
    } catch (err) {
      console.error("Failed to unlock chapter:", err);
      showToast("Failed to unlock chapter.", "error");
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
    showToast(isInShelf ? "Removed from shelf" : "Added to shelf!", "success");
  };

  if (!novel) {
    return (
      <div className="scroll-container animate-fade-in" style={{ textAlign: 'center', paddingTop: '100px', color: 'var(--text-secondary)' }}>
        <p>Loading reader content...</p>
      </div>
    );
  }

  const currentChapterTitle = chapterDetail?.title || chapters[currentChIndex]?.title || `Chapter ${currentChIndex + 1}`;
  const totalChaptersCount = chapters.length || 1;

  return (
    <div className={`reader-container reader-theme-${theme} page-container-full`}>
      {/* Floating Back Button */}
      <button 
        className="reader-floating-back-btn" 
        onClick={() => onNavigate('detail', { id: novelId })}
        aria-label="Go back"
        style={{
          position: 'absolute',
          top: '12px',
          left: '12px',
          width: '36px',
          height: '36px',
          borderRadius: '50%',
          border: '1px solid var(--reader-border)',
          backgroundColor: 'var(--reader-bg)',
          color: 'var(--reader-text)',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          zIndex: 90,
          cursor: 'pointer',
          boxShadow: '0 2px 8px rgba(0, 0, 0, 0.08)',
        }}
      >
        <svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" strokeWidth={2.5} stroke="currentColor" style={{ width: '18px', height: '18px' }}>
          <path strokeLinecap="round" strokeLinejoin="round" d="M15.75 19.5L8.25 12l7.5-7.5" />
        </svg>
      </button>

      {/* Settings HUD panel */}
      <ReaderSettings
        visible={showSettings}
        fontSize={fontSize}
        onFontSizeChange={handleFontSizeChange}
        onOpenDrawer={() => setShowTOC(true)}
        novelTitle={novel.title}
        onBack={() => onNavigate('detail', { id: novelId })}
        isInShelf={isInShelf}
        onAddToShelf={handleToggleShelf}
        currentChapterIndex={currentChIndex}
        totalChapters={totalChaptersCount}
        onPrevChapter={() => handleChapterSelect(currentChIndex - 1)}
        onNextChapter={() => handleChapterSelect(currentChIndex + 1)}
      />

      {/* Sidebar Drawer: Table of Contents */}
      <Drawer
        visible={showTOC}
        onClose={() => setShowTOC(false)}
        title="Table of Contents"
      >
        <div className="chapters-list" style={{ padding: '8px 0' }}>
          {chapters.map((ch, index) => {
            const isCurrent = index === currentChIndex;
            const isChPaid = ch.is_paid !== undefined ? ch.is_paid : (index >= 2);
            const isChLocked = isChPaid && !unlockedBookChapters.includes(`${novelId}-${index}`);
            return (
              <div
                key={ch.id || index}
                className="chapter-item-link"
                style={{ 
                  backgroundColor: isCurrent ? 'var(--accent-light)' : 'transparent',
                  borderLeft: isCurrent ? '4px solid var(--accent-color)' : '4px solid transparent'
                }}
                onClick={() => handleChapterSelect(index)}
              >
                <div style={{ display: 'flex', alignItems: 'center', gap: '6px' }}>
                  <span 
                    className="chapter-item-title"
                    style={{ 
                      fontWeight: isCurrent ? 700 : 500,
                      color: isCurrent ? 'var(--accent-color)' : 'var(--text-primary)'
                    }}
                  >
                    {ch.title || `Chapter ${index + 1}`}
                  </span>
                  {isChLocked && <span style={{ fontSize: '12px' }}>🔒</span>}
                </div>
                <span className="chapter-item-meta">{ch.word_count?.toLocaleString() || 0} words</span>
              </div>
            );
          })}
        </div>
      </Drawer>

      {/* Single Chapter Main Reading Area */}
      <div 
        ref={scrollRef}
        className="reader-scroll-area"
        onScroll={handleScroll}
        onClick={handleScreenClick}
        style={{
          fontFamily: '-apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif',
          fontSize: `${fontSize}px`,
          lineHeight: '1.8',
          overflowY: 'auto',
          flex: 1,
          minHeight: 0,
          height: '100%',
          position: 'relative',
          paddingTop: '64px',
          paddingBottom: '80px',
          boxSizing: 'border-box',
        }}
      >
        {loading ? (
          <div style={{ textAlign: 'center', padding: '100px 20px', color: 'var(--text-secondary)' }}>
            Loading chapter text...
          </div>
        ) : (
          <article>
            <h2 className="reader-chapter-title">
              {currentChapterTitle}
              <div style={{ fontSize: '12px', fontWeight: 500, color: 'var(--text-tertiary)', marginTop: '6px' }}>
                Chapter {currentChIndex + 1} of {totalChaptersCount}
                {chapterDetail?.word_count ? ` · ${chapterDetail.word_count.toLocaleString()} words` : ''}
              </div>
            </h2>

            {/* Chapter Text Paragraphs from Backend API */}
            {chapterDetail?.content && (
              chapterDetail.content.split('\n\n').map((paragraph, pIdx) => (
                <p key={pIdx} data-paragraph-index={pIdx} className="reader-text-paragraph">
                  {paragraph}
                </p>
              ))
            )}

            {/* Locked Preview Card */}
            {isLocked && (
              <div style={{ position: 'relative', marginTop: '20px' }}>
                <div style={{
                  backgroundColor: 'var(--bg-secondary)',
                  borderRadius: '20px',
                  padding: '24px 20px',
                  border: '1px solid var(--border-color)',
                  boxShadow: '0 10px 30px rgba(0, 0, 0, 0.1)',
                  marginTop: '24px',
                  display: 'flex',
                  flexDirection: 'column',
                  alignItems: 'center',
                  gap: '12px',
                  textAlign: 'center'
                }}>
                  <div style={{
                    width: '48px',
                    height: '48px',
                    borderRadius: '50%',
                    backgroundColor: 'var(--reader-border)',
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'center',
                    fontSize: '20px',
                    color: 'var(--reader-text)'
                  }}>
                    🔒
                  </div>
                  <h3 style={{ fontSize: '16px', fontWeight: 800, margin: 0, color: 'var(--reader-text)' }}>
                    Unlock to Continue Reading
                  </h3>
                  <p style={{ fontSize: '12px', color: 'var(--text-tertiary)', margin: 0 }}>
                    This chapter is locked ({chapterDetail?.word_count?.toLocaleString() || 0} words)
                  </p>

                  <div style={{ width: '100%', maxWidth: '280px', display: 'flex', justifyContent: 'space-between', fontSize: '13px', borderTop: '1px solid var(--border-color)', paddingTop: '12px' }}>
                    <span style={{ color: 'var(--text-secondary)' }}>Price:</span>
                    <span style={{ fontWeight: 700, color: 'var(--text-primary)' }}>{chapterPrice} Coins</span>
                  </div>
                  <div style={{ width: '100%', maxWidth: '280px', display: 'flex', justifyContent: 'space-between', fontSize: '13px' }}>
                    <span style={{ color: 'var(--text-secondary)' }}>Your Balance:</span>
                    <span style={{ fontWeight: 700, color: userCoins >= chapterPrice ? '#22c55e' : '#ef4444' }}>
                      {userCoins} Coins
                    </span>
                  </div>

                  <div style={{ display: 'flex', alignItems: 'center', gap: '8px', fontSize: '12px', marginTop: '4px' }}>
                    <input 
                      type="checkbox" 
                      id="auto-unlock-checkbox"
                      checked={autoUnlock}
                      onChange={(e) => setAutoUnlock(e.target.checked)}
                      style={{ width: '15px', height: '15px', accentColor: 'var(--accent-color)', cursor: 'pointer' }}
                    />
                    <label htmlFor="auto-unlock-checkbox" style={{ cursor: 'pointer', color: 'var(--text-secondary)' }}>
                      Auto-unlock next chapters
                    </label>
                  </div>

                  <button 
                    onClick={(e) => {
                      e.stopPropagation();
                      handleUnlockNow();
                    }}
                    disabled={isUnlocking}
                    style={{
                      width: '100%',
                      maxWidth: '280px',
                      height: '46px',
                      borderRadius: '23px',
                      border: 'none',
                      display: 'flex',
                      alignItems: 'center',
                      justifyContent: 'center',
                      gap: '8px',
                      fontSize: '15px',
                      fontWeight: 700,
                      cursor: 'pointer',
                      color: 'white',
                      marginTop: '8px',
                      boxShadow: userCoins >= chapterPrice 
                        ? '0 6px 20px rgba(79, 70, 229, 0.4)' 
                        : '0 6px 20px rgba(239, 68, 68, 0.4)',
                      background: userCoins >= chapterPrice 
                        ? 'linear-gradient(135deg, #6366f1 0%, #493cd6 100%)' 
                        : 'linear-gradient(135deg, #ef4444 0%, #ca2b2b 100%)',
                    }}
                  >
                    {userCoins >= chapterPrice ? (
                      <>
                        <span>Unlock Now</span>
                        <span style={{ display: 'inline-flex', alignItems: 'center', gap: '3px', backgroundColor: 'rgba(255,255,255,0.15)', padding: '2px 8px', borderRadius: '12px', fontSize: '12px' }}>
                          <GoldCoin size={12} />
                          <span>{chapterPrice}</span>
                        </span>
                      </>
                    ) : (
                      <>
                        <span>Top Up Coins</span>
                        <svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" strokeWidth={2.5} stroke="currentColor" style={{ width: '16px', height: '16px' }}>
                          <path strokeLinecap="round" strokeLinejoin="round" d="M8.25 4.5l7.5 7.5-7.5 7.5" />
                        </svg>
                      </>
                    )}
                  </button>
                </div>
              </div>
            )}

            {/* Active Bottom Chapter Navigation Row */}
            <div 
              className="reader-bottom-nav-row" 
              onClick={(e) => e.stopPropagation()}
            >
              <button
                className="reader-chapter-nav-btn"
                disabled={currentChIndex <= 0}
                onClick={() => handleChapterSelect(currentChIndex - 1)}
                aria-label="Previous chapter"
              >
                <svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" strokeWidth={2.5} stroke="currentColor" style={{ width: '15px', height: '15px' }}>
                  <path strokeLinecap="round" strokeLinejoin="round" d="M15.75 19.5L8.25 12l7.5-7.5" />
                </svg>
                <span>Previous</span>
              </button>

              <button
                className="reader-chapter-nav-btn reader-toc-nav-btn"
                onClick={() => setShowTOC(true)}
                aria-label="Table of Contents"
              >
                <svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" strokeWidth={2} stroke="currentColor" style={{ width: '16px', height: '16px' }}>
                  <path strokeLinecap="round" strokeLinejoin="round" d="M8.25 6.75h12M8.25 12h12m-12 5.25h12M3.75 6.75h.007v.008H3.75V6.75zm.375 0a.375.375 0 11-.75 0 .375.375 0 01.75 0zM3.75 12h.007v.008H3.75V12zm.375 0a.375.375 0 11-.75 0 .375.375 0 01.75 0zm-.375 5.25h.007v.008H3.75v-.008zm.375 0a.375.375 0 11-.75 0 .375.375 0 01.75 0z" />
                </svg>
                <span>Contents</span>
              </button>

              <button
                className="reader-chapter-nav-btn primary"
                disabled={chapters.length > 0 && currentChIndex >= chapters.length - 1}
                onClick={() => handleChapterSelect(currentChIndex + 1)}
                aria-label="Next chapter"
              >
                <span>Next</span>
                <svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" strokeWidth={2.5} stroke="currentColor" style={{ width: '15px', height: '15px' }}>
                  <path strokeLinecap="round" strokeLinejoin="round" d="M8.25 4.5l7.5 7.5-7.5 7.5" />
                </svg>
              </button>
            </div>
          </article>
        )}
      </div>
    </div>
  );
};
