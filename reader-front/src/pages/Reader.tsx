import React, { useState, useEffect, useRef, useCallback } from 'react';
import { useToast } from '../context/ToastContext';
import { api } from '../utils/api';
import type { Novel, Chapter, ChapterContentResponse } from '../utils/api';
import { ReaderSettings } from '../components/ReaderSettings';
import { Drawer } from '../components/Drawer';
import { GoldCoin } from '../components/GoldCoin';
import { readerCache } from '../utils/readerCache';

interface ChapterFeedItem {
  index: number;
  chapter: Chapter;
  locked: boolean;
  price: number;
}

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
  const [feedItems, setFeedItems] = useState<ChapterFeedItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [currentChIndex, setCurrentChIndex] = useState(initialChapterIndex);

  const scrollRef = useRef<HTMLDivElement>(null);
  const isFetchingPrev = useRef(false);
  const isFetchingNext = useRef(false);
  const isPositioning = useRef(false);

  // Typography settings with localStorage defaults
  const [fontSize, setFontSize] = useState<number>(() => {
    return parseInt(localStorage.getItem('reader-font-size') || '16', 10);
  });
  const [theme, setTheme] = useState<string>(() => {
    return localStorage.getItem('reader-theme') || 'sepia';
  });
  const [fontFamily, setFontFamily] = useState<'serif' | 'sans'>(() => {
    return (localStorage.getItem('reader-font-family') as 'serif' | 'sans') || 'serif';
  });
  const [lineHeight, setLineHeight] = useState<'narrow' | 'medium' | 'wide'>(() => {
    return (localStorage.getItem('reader-line-height') as 'narrow' | 'medium' | 'wide') || 'medium';
  });

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

  // Fetch Chapter with IndexedDB cache fallback
  const fetchChapterData = useCallback(
    async (nId: number, cIdx: number): Promise<ChapterContentResponse> => {
      const cached = await readerCache.getCachedChapter(nId, cIdx);
      if (cached) {
        return cached;
      }
      const res = await api.getChapterContent(nId, cIdx);
      if (res) {
        await readerCache.setCachedChapter(nId, cIdx, res);
      }
      return res;
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

  // 2. Initial load of target chapter into Feed + background prefetching
  useEffect(() => {
    const initFeed = async () => {
      setLoading(true);
      try {
        let res = await fetchChapterData(novelId, initialChapterIndex);

        // Auto-unlock initial chapter if locked and autoUnlock preference is enabled
        if (res.locked && autoUnlockRef.current && userCoinsRef.current >= res.price) {
          const success = await onUnlockChapter(novelId, initialChapterIndex, res.price);
          if (success) {
            showToast(`Auto-unlocked ${res.chapter.title || `Chapter ${initialChapterIndex + 1}`}!`, "success");
            res = await api.getChapterContent(novelId, initialChapterIndex);
            await readerCache.setCachedChapter(novelId, initialChapterIndex, res);
          }
        }

        setFeedItems([
          {
            index: initialChapterIndex,
            chapter: res.chapter,
            locked: res.locked,
            price: res.price,
          },
        ]);
        setCurrentChIndex(initialChapterIndex);

        // Background prefetch next 2 chapters into IndexedDB
        readerCache.prefetchChapters(novelId, initialChapterIndex, chapters.length || 100, (nId, idx) =>
          api.getChapterContent(nId, idx)
        );

        // Restore paragraph anchor progress after DOM renders
        requestAnimationFrame(() => {
          const container = scrollRef.current;
          const savedProgress = readingProgress[novelId];
          if (savedProgress && savedProgress.chapterIndex === initialChapterIndex) {
            if (savedProgress.paragraphIndex !== undefined) {
              const pEl = container?.querySelector(`[data-paragraph-index="${savedProgress.paragraphIndex}"]`);
              if (pEl) {
                pEl.scrollIntoView({ block: 'center' });
                return;
              }
            }
            if (container) {
              const maxScroll = container.scrollHeight - container.clientHeight;
              container.scrollTop = savedProgress.scrollOffsetPercentage * maxScroll;
            }
          } else if (container) {
            container.scrollTop = 0;
          }
        });
      } catch (err) {
        console.error("Failed to load initial chapter:", err);
        showToast("Failed to load chapter content.", "error");
      } finally {
        setLoading(false);
      }
    };

    initFeed();
  }, [novelId, initialChapterIndex, fetchChapterData, showToast, onUnlockChapter, chapters.length]);

  // Sync settings changes to localStorage
  useEffect(() => {
    localStorage.setItem('reader-font-size', fontSize.toString());
  }, [fontSize]);

  useEffect(() => {
    localStorage.setItem('reader-theme', theme);
  }, [theme]);

  useEffect(() => {
    localStorage.setItem('reader-font-family', fontFamily);
  }, [fontFamily]);

  useEffect(() => {
    localStorage.setItem('reader-line-height', lineHeight);
  }, [lineHeight]);

  useEffect(() => {
    localStorage.setItem('reader-auto-unlock', autoUnlock.toString());
  }, [autoUnlock]);

  // Load Previous Chapter (Prepend to feed without scroll jump)
  const loadPreviousChapter = useCallback(async () => {
    if (feedItems.length === 0 || isFetchingPrev.current) return;
    const firstItem = feedItems[0];
    if (firstItem.index <= 0) return;

    const prevIndex = firstItem.index - 1;
    isFetchingPrev.current = true;

    try {
      const container = scrollRef.current;
      const prevScrollHeight = container ? container.scrollHeight : 0;
      const prevScrollTop = container ? container.scrollTop : 0;

      const res = await fetchChapterData(novelId, prevIndex);
      const newItem: ChapterFeedItem = {
        index: prevIndex,
        chapter: res.chapter,
        locked: res.locked,
        price: res.price,
      };

      setFeedItems((prev) => [newItem, ...prev]);

      requestAnimationFrame(() => {
        if (container) {
          const newScrollHeight = container.scrollHeight;
          const heightDiff = newScrollHeight - prevScrollHeight;
          container.scrollTop = prevScrollTop + heightDiff;
        }
        isFetchingPrev.current = false;
      });
    } catch (err) {
      console.error("Failed to load previous chapter:", err);
      isFetchingPrev.current = false;
    }
  }, [feedItems, novelId, fetchChapterData]);

  // Load Next Chapter (Append to feed with auto-unlock support)
  const loadNextChapter = useCallback(async () => {
    if (feedItems.length === 0 || isFetchingNext.current) return;
    const lastItem = feedItems[feedItems.length - 1];
    if (chapters.length > 0 && lastItem.index >= chapters.length - 1) return;

    const nextIndex = lastItem.index + 1;
    isFetchingNext.current = true;

    try {
      let res = await fetchChapterData(novelId, nextIndex);

      // Check auto-unlock condition
      if (res.locked && autoUnlockRef.current && userCoinsRef.current >= res.price) {
        const success = await onUnlockChapter(novelId, nextIndex, res.price);
        if (success) {
          showToast(`Auto-unlocked ${res.chapter.title || `Chapter ${nextIndex + 1}`}!`, "success");
          res = await api.getChapterContent(novelId, nextIndex);
          await readerCache.setCachedChapter(novelId, nextIndex, res);
        }
      }

      const newItem: ChapterFeedItem = {
        index: nextIndex,
        chapter: res.chapter,
        locked: res.locked,
        price: res.price,
      };

      setFeedItems((prev) => [...prev, newItem]);

      // Background prefetch further ahead
      readerCache.prefetchChapters(novelId, nextIndex, chapters.length || 100, (nId, idx) =>
        api.getChapterContent(nId, idx)
      );
    } catch (err) {
      console.error("Failed to load next chapter:", err);
    } finally {
      isFetchingNext.current = false;
    }
  }, [feedItems, chapters.length, novelId, fetchChapterData, onUnlockChapter, showToast]);

  const lastSavedParagraphRef = useRef<number | null>(null);

  // IntersectionObserver for active chapter & paragraph tracking without Layout Thrashing
  useEffect(() => {
    const container = scrollRef.current;
    if (!container) return;

    // Observe active chapter
    const chapterObserver = new IntersectionObserver(
      (entries) => {
        entries.forEach((entry) => {
          if (entry.isIntersecting && entry.intersectionRatio >= 0.3) {
            const chIdx = parseInt(entry.target.getAttribute('data-chapter-index') || '0', 10);
            if (chIdx !== currentChIndex) {
              setCurrentChIndex(chIdx);
              const search = new URLSearchParams(window.location.search);
              search.set('chapter_index', String(chIdx + 1));
              window.history.replaceState(null, '', `${window.location.pathname}?${search.toString()}`);
            }
          }
        });
      },
      { root: container, threshold: [0.3] }
    );

    // Observe active paragraph for anchor progress
    const paragraphObserver = new IntersectionObserver(
      (entries) => {
        entries.forEach((entry) => {
          if (entry.isIntersecting && entry.intersectionRatio >= 0.5) {
            const pIdx = parseInt(entry.target.getAttribute('data-paragraph-index') || '0', 10);
            if (lastSavedParagraphRef.current !== pIdx) {
              lastSavedParagraphRef.current = pIdx;
              if (container.scrollHeight > container.clientHeight) {
                const percentage = container.scrollTop / (container.scrollHeight - container.clientHeight);
                onSaveProgress(novelId, currentChIndex, percentage, pIdx);
              }
            }
          }
        });
      },
      { root: container, threshold: [0.5] }
    );

    const chapterEls = container.querySelectorAll('[data-chapter-index]');
    chapterEls.forEach((el) => chapterObserver.observe(el));

    const paragraphEls = container.querySelectorAll('[data-paragraph-index]');
    paragraphEls.forEach((el) => paragraphObserver.observe(el));

    return () => {
      chapterObserver.disconnect();
      paragraphObserver.disconnect();
    };
  }, [feedItems, currentChIndex, novelId, onSaveProgress]);

  // Scroll Listener for Infinite Feed Prepend/Append triggers
  const handleScroll = () => {
    const container = scrollRef.current;
    if (!container || isPositioning.current) return;

    const { scrollTop, scrollHeight, clientHeight } = container;

    // Trigger Prepend Top
    if (scrollTop < 120 && !isFetchingPrev.current) {
      loadPreviousChapter();
    }

    // Trigger Append Bottom (Allow loading next chapter if lastItem is unlocked OR autoUnlock is enabled)
    if (scrollTop + clientHeight >= scrollHeight - 350 && !isFetchingNext.current) {
      const lastItem = feedItems[feedItems.length - 1];
      if (lastItem && (!lastItem.locked || autoUnlockRef.current)) {
        loadNextChapter();
      }
    }
  };

  // Chapter Unlock handling
  const [isUnlocking, setIsUnlocking] = useState(false);

  const handleUnlockChapterFeed = async (item: ChapterFeedItem) => {
    if (isUnlocking) return;
    setIsUnlocking(true);
    try {
      if (userCoins >= item.price) {
        const success = await onUnlockChapter(novelId, item.index, item.price);
        if (success) {
          showToast("Unlocked successfully!", "success");
          const res = await api.getChapterContent(novelId, item.index);
          await readerCache.setCachedChapter(novelId, item.index, res);
          setFeedItems((prev) =>
            prev.map((f) =>
              f.index === item.index
                ? { ...f, chapter: res.chapter, locked: res.locked }
                : f
            )
          );
        }
      } else {
        onNavigate('recharge');
      }
    } catch (err) {
      console.error("Failed to unlock chapter:", err);
    } finally {
      setIsUnlocking(false);
    }
  };

  // Auto-unlock active locked items in feed if autoUnlock is checked and user has coins
  useEffect(() => {
    if (!autoUnlock || feedItems.length === 0) return;
    const unlockPending = async () => {
      for (const item of feedItems) {
        if (item.locked && userCoins >= item.price && !isUnlocking) {
          const success = await onUnlockChapter(novelId, item.index, item.price);
          if (success) {
            showToast(`Auto-unlocked ${item.chapter.title || `Chapter ${item.index + 1}`}!`, "success");
            const res = await api.getChapterContent(novelId, item.index);
            await readerCache.setCachedChapter(novelId, item.index, res);
            setFeedItems((prev) =>
              prev.map((f) =>
                f.index === item.index
                  ? { ...f, chapter: res.chapter, locked: res.locked }
                  : f
              )
            );
          }
        }
      }
    };
    unlockPending();
  }, [autoUnlock, userCoins, novelId, onUnlockChapter, showToast]);

  // TOC Navigation: Jump directly to chapter
  const handleJumpToChapter = async (targetIdx: number) => {
    setShowTOC(false);
    setShowSettings(false);
    isPositioning.current = true;

    const existing = feedItems.find((f) => f.index === targetIdx);
    if (existing) {
      const container = scrollRef.current;
      const targetEl = container?.querySelector<HTMLElement>(`[data-chapter-index="${targetIdx}"]`);
      if (targetEl && container) {
        container.scrollTop = targetEl.offsetTop - 60;
      }
      setCurrentChIndex(targetIdx);
      setTimeout(() => {
        isPositioning.current = false;
      }, 300);
    } else {
      setLoading(true);
      try {
        const res = await fetchChapterData(novelId, targetIdx);
        setFeedItems([
          {
            index: targetIdx,
            chapter: res.chapter,
            locked: res.locked,
            price: res.price,
          },
        ]);
        setCurrentChIndex(targetIdx);
        const container = scrollRef.current;
        if (container) container.scrollTop = 0;
      } catch (err) {
        console.error("Failed to jump to chapter:", err);
      } finally {
        setLoading(false);
        setTimeout(() => {
          isPositioning.current = false;
        }, 300);
      }
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

  const getLineHeightVal = () => {
    if (lineHeight === 'narrow') return '1.5';
    if (lineHeight === 'wide') return '2.0';
    return '1.75';
  };

  if (!novel) {
    return (
      <div className="scroll-container animate-fade-in" style={{ textAlign: 'center', paddingTop: '100px', color: 'var(--text-secondary)' }}>
        <p>Loading reader content...</p>
      </div>
    );
  }

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
        onFontSizeChange={setFontSize}
        theme={theme}
        onThemeChange={setTheme}
        fontFamily={fontFamily}
        onFontFamilyChange={setFontFamily}
        lineHeight={lineHeight}
        onLineHeightChange={setLineHeight}
        onOpenDrawer={() => setShowTOC(true)}
        novelTitle={novel.title}
        onBack={() => onNavigate('detail', { id: novelId })}
        isInShelf={isInShelf}
        onAddToShelf={handleToggleShelf}
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
            const isChPaid = index >= 2;
            const isChLocked = isChPaid && !unlockedBookChapters.includes(`${novelId}-${index}`);
            return (
              <div
                key={ch.id}
                className="chapter-item-link"
                style={{ 
                  backgroundColor: isCurrent ? 'var(--accent-light)' : 'transparent',
                  borderLeft: isCurrent ? '4px solid var(--accent-color)' : '4px solid transparent'
                }}
                onClick={() => handleJumpToChapter(index)}
              >
                <div style={{ display: 'flex', alignItems: 'center', gap: '6px' }}>
                  <span 
                    className="chapter-item-title"
                    style={{ 
                      fontWeight: isCurrent ? 700 : 500,
                      color: isCurrent ? 'var(--accent-color)' : 'var(--text-primary)'
                    }}
                  >
                    {ch.title}
                  </span>
                  {isChLocked && <span style={{ fontSize: '12px' }}>🔒</span>}
                </div>
                <span className="chapter-item-meta">{ch.word_count.toLocaleString()} words</span>
              </div>
            );
          })}
        </div>
      </Drawer>

      {/* Scroll Mode High-Performance Reader Engine */}
      <div 
        ref={scrollRef}
        className="reader-scroll-area"
        onScroll={handleScroll}
        onClick={handleScreenClick}
        style={{
          fontFamily: fontFamily === 'serif' ? 'var(--font-serif)' : 'var(--font-sans)',
          fontSize: `${fontSize}px`,
          lineHeight: getLineHeightVal(),
          overflowY: 'auto',
          height: '100vh',
          position: 'relative',
          paddingTop: '64px',
          paddingBottom: '120px'
        }}
      >
        {loading ? (
          <div style={{ textAlign: 'center', padding: '100px 20px', color: 'var(--text-secondary)' }}>
            Loading chapter text...
          </div>
        ) : (
          feedItems.map((item) => (
            <article 
              key={item.index} 
              data-chapter-index={item.index}
              style={{ marginBottom: '48px', paddingBottom: '32px', borderBottom: '1px dashed var(--reader-border)' }}
            >
              <h2 className="reader-chapter-title">{item.chapter.title}</h2>
              
              {/* Unlocked Text Paragraphs */}
              {!item.locked && (item.chapter.content || '').split('\n\n').map((paragraph, pIdx) => (
                <p key={pIdx} data-paragraph-index={pIdx} className="reader-text-paragraph">
                  {paragraph}
                </p>
              ))}

              {/* Locked Preview Card Embedded in Continuous Stream */}
              {item.locked && (
                <div style={{ position: 'relative', marginTop: '20px' }}>
                  <p className="reader-text-paragraph" style={{ opacity: 0.25, filter: 'blur(0.5px)', userSelect: 'none' }}>
                    The ancient pathways of the cosmos were shrouded in starlight, hiding secrets that even the celestial kings dared not speak aloud...
                  </p>
                  
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
                      This chapter is locked ({item.chapter.word_count.toLocaleString()} words)
                    </p>

                    <div style={{ width: '100%', maxWidth: '280px', display: 'flex', justifyContent: 'space-between', fontSize: '13px', borderTop: '1px solid var(--border-color)', paddingTop: '12px' }}>
                      <span style={{ color: 'var(--text-secondary)' }}>Price:</span>
                      <span style={{ fontWeight: 700, color: 'var(--text-primary)' }}>{item.price} Coins</span>
                    </div>
                    <div style={{ width: '100%', maxWidth: '280px', display: 'flex', justifyContent: 'space-between', fontSize: '13px' }}>
                      <span style={{ color: 'var(--text-secondary)' }}>Your Balance:</span>
                      <span style={{ fontWeight: 700, color: userCoins >= item.price ? '#22c55e' : '#ef4444' }}>
                        {userCoins} Coins
                      </span>
                    </div>

                    <div style={{ display: 'flex', alignItems: 'center', gap: '8px', fontSize: '12px', marginTop: '4px' }}>
                      <input 
                        type="checkbox" 
                        id={`auto-unlock-scroll-${item.index}`}
                        checked={autoUnlock}
                        onChange={(e) => setAutoUnlock(e.target.checked)}
                        style={{ width: '15px', height: '15px', accentColor: 'var(--accent-color)', cursor: 'pointer' }}
                      />
                      <label htmlFor={`auto-unlock-scroll-${item.index}`} style={{ cursor: 'pointer', color: 'var(--text-secondary)' }}>
                        Auto-unlock next chapters
                      </label>
                    </div>

                    <button 
                      onClick={(e) => {
                        e.stopPropagation();
                        handleUnlockChapterFeed(item);
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
                        boxShadow: userCoins >= item.price 
                          ? '0 6px 20px rgba(79, 70, 229, 0.4)' 
                          : '0 6px 20px rgba(239, 68, 68, 0.4)',
                        background: userCoins >= item.price 
                          ? 'linear-gradient(135deg, #6366f1 0%, #493cd6 100%)' 
                          : 'linear-gradient(135deg, #ef4444 0%, #ca2b2b 100%)',
                      }}
                    >
                      {userCoins >= item.price ? (
                        <>
                          <span>Unlock Now</span>
                          <span style={{ display: 'inline-flex', alignItems: 'center', gap: '3px', backgroundColor: 'rgba(255,255,255,0.15)', padding: '2px 8px', borderRadius: '12px', fontSize: '12px' }}>
                            <GoldCoin size={12} />
                            <span>{item.price}</span>
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
            </article>
          ))
        )}
      </div>
    </div>
  );
};
