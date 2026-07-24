import React, { useState, useEffect, useRef } from 'react';
import { api } from '../utils/api';
import type { Novel, Chapter } from '../utils/api';
import { ReaderSettings } from '../components/ReaderSettings';
import { Drawer } from '../components/Drawer';
import { GoldCoin } from '../components/GoldCoin';

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
    };
  };
  onSaveProgress: (bookId: number, chapterIndex: number, scrollOffsetPercentage: number) => void;
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
  const [novel, setNovel] = useState<Novel | null>(null);
  const [chapters, setChapters] = useState<Chapter[]>([]);
  const [chapterDetail, setChapterDetail] = useState<Chapter | null>(null);
  const [isLocked, setIsLocked] = useState(true);
  const [chapterPrice, setChapterPrice] = useState(50);
  const [loading, setLoading] = useState(true);
  const [currentChIndex, setCurrentChIndex] = useState(initialChapterIndex);

  const scrollRef = useRef<HTMLDivElement>(null);
  const touchStartX = useRef(0);
  const touchStartY = useRef(0);
  const touchStartScrollTop = useRef(0);
  const scrollTargetPosition = useRef<'top' | 'bottom' | 'restore'>('restore');
  const hasScrolledDown = useRef(false);
  const isTransitioning = useRef(false);

  // Settings states with localStorage defaults
  const [fontSize, setFontSize] = useState<number>(() => {
    return parseInt(localStorage.getItem('reader-font-size') || '18');
  });
  const [theme, setTheme] = useState<string>(() => {
    return localStorage.getItem('reader-theme') || 'sepia';
  });
  const [fontFamily] = useState<'serif' | 'sans'>(() => {
    return (localStorage.getItem('reader-font-family') as 'serif' | 'sans') || 'serif';
  });
  const [lineHeight] = useState<'narrow' | 'medium' | 'wide'>(() => {
    return (localStorage.getItem('reader-line-height') as 'narrow' | 'medium' | 'wide') || 'medium';
  });

  const [showSettings, setShowSettings] = useState(false);
  const [showTOC, setShowTOC] = useState(false);
  const [toastMessage, setToastMessage] = useState<string | null>(null);

  // Auto-unlock chapters preference
  const [autoUnlock, setAutoUnlock] = useState<boolean>(() => {
    return localStorage.getItem('reader-auto-unlock') === 'true';
  });

  // 1. Fetch novel meta and chapters list
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

  // 2. Fetch specific chapter content when index changes
  useEffect(() => {
    const fetchChapter = async () => {
      setLoading(true);
      try {
        const res = await api.getChapterContent(novelId, currentChIndex);
        setChapterDetail(res.chapter);
        setIsLocked(res.locked);
        setChapterPrice(res.price);
      } catch (err) {
        console.error("Failed to load chapter content:", err);
        triggerToast("Failed to load chapter content.");
      } finally {
        setLoading(false);
      }
    };
    fetchChapter();
  }, [novelId, currentChIndex]);

  // Sync settings changes to localStorage
  useEffect(() => {
    localStorage.setItem('reader-font-size', fontSize.toString());
  }, [fontSize]);

  useEffect(() => {
    localStorage.setItem('reader-theme', theme);
  }, [theme]);

  useEffect(() => {
    localStorage.setItem('reader-auto-unlock', autoUnlock.toString());
  }, [autoUnlock]);

  // 3. Handle Auto-Unlock logic
  useEffect(() => {
    if (isLocked && autoUnlock && userCoins >= chapterPrice) {
      handleUnlockNow();
    }
  }, [currentChIndex, isLocked, autoUnlock, userCoins, novelId, chapterPrice]);

  // When chapter index changes, restore scroll position if saved, or scroll to top
  useEffect(() => {
    const progress = readingProgress[novelId];
    const container = scrollRef.current;
    
    if (container && !isLocked && !loading) {
      isTransitioning.current = true; // Lock scroll checking during positioning
      
      const finishPositioning = () => {
        setTimeout(() => {
          isTransitioning.current = false;
        }, 150);
      };

      if (scrollTargetPosition.current === 'restore' && progress && progress.chapterIndex === currentChIndex) {
        setTimeout(() => {
          const maxScroll = container.scrollHeight - container.clientHeight;
          container.scrollTop = progress.scrollOffsetPercentage * maxScroll;
          finishPositioning();
        }, 80);
      } else {
        container.scrollTop = 0;
        finishPositioning();
      }
      scrollTargetPosition.current = 'restore';
    }
  }, [currentChIndex, novelId, isLocked, loading]);

  // Swipe touch gestures
  const handleTouchStart = (e: React.TouchEvent) => {
    touchStartX.current = e.changedTouches[0].clientX;
    touchStartY.current = e.changedTouches[0].clientY;
    const container = scrollRef.current;
    touchStartScrollTop.current = container ? container.scrollTop : 0;
  };

  const handleTouchEnd = (e: React.TouchEvent) => {
    if (isTransitioning.current) return;
    const diffX = e.changedTouches[0].clientX - touchStartX.current;
    const diffY = e.changedTouches[0].clientY - touchStartY.current;

    // Detect vertical swipe down at the top: load previous chapter
    if (diffY > 80 && Math.abs(diffX) < 50 && touchStartScrollTop.current <= 5) {
      if (currentChIndex > 0) {
        isTransitioning.current = true;
        triggerToast("Loading previous chapter...");
        setTimeout(() => {
          handleChapterSelect(currentChIndex - 1, 'top');
        }, 800);
      } else {
        triggerToast("You've reached the first chapter.");
      }
    }
    // Detect vertical swipe up at the bottom: load next chapter
    else if (diffY < -80 && Math.abs(diffX) < 50) {
      const container = scrollRef.current;
      if (container) {
        const maxScroll = container.scrollHeight - container.clientHeight;
        if (touchStartScrollTop.current >= maxScroll - 15 || maxScroll <= 0) {
          if (isLocked) {
            triggerToast("Please unlock this chapter first.");
            return;
          }
          if (currentChIndex < chapters.length - 1) {
            isTransitioning.current = true;
            triggerToast("Loading next chapter...");
            setTimeout(() => {
              handleChapterSelect(currentChIndex + 1, 'top');
            }, 800);
          } else {
            triggerToast("You've reached the last chapter.");
          }
        }
      }
    }
  };

  // Track scrolling to save progress
  const handleScroll = () => {
    if (isLocked || isTransitioning.current) return;
    const container = scrollRef.current;
    if (!container) return;

    const maxScroll = container.scrollHeight - container.clientHeight;
    if (maxScroll <= 0) return;

    const scrollTop = container.scrollTop;
    const percentage = scrollTop / maxScroll;
    onSaveProgress(novelId, currentChIndex, percentage);

    if (scrollTop > 20) {
      hasScrolledDown.current = true;
    }

    // Scroll to bottom page turn:
    if (scrollTop >= maxScroll - 10) {
      if (currentChIndex < chapters.length - 1) {
        isTransitioning.current = true;
        triggerToast("Loading next chapter...");
        setTimeout(() => {
          handleChapterSelect(currentChIndex + 1, 'top');
          hasScrolledDown.current = false;
        }, 800);
      } else {
        triggerToast("You've reached the last chapter.");
      }
    }
    // Scroll to top page turn:
    else if (scrollTop <= 2 && hasScrolledDown.current) {
      if (currentChIndex > 0) {
        isTransitioning.current = true;
        triggerToast("Loading previous chapter...");
        setTimeout(() => {
          handleChapterSelect(currentChIndex - 1, 'top');
          hasScrolledDown.current = false;
        }, 800);
      } else {
        triggerToast("You've reached the first chapter.");
      }
    }
  };

  // Clicking in content area toggles settings HUD
  const handleScreenClick = () => {
    if (isLocked) return;
    setShowSettings(!showSettings);
  };

  const triggerToast = (msg: string) => {
    setToastMessage(msg);
    setTimeout(() => {
      setToastMessage(null);
    }, 2000);
  };

  const handleToggleShelf = () => {
    onToggleShelf(novelId);
    triggerToast(isInShelf ? "Removed from shelf" : "Added to shelf!");
  };

  const handleChapterSelect = (idx: number, targetPos: 'top' | 'bottom' | 'restore' = 'top') => {
    scrollTargetPosition.current = targetPos;
    hasScrolledDown.current = false;
    setCurrentChIndex(idx);
    setShowTOC(false);
    setShowSettings(false);
  };

  const handleUnlockNow = async () => {
    if (userCoins >= chapterPrice) {
      const success = await onUnlockChapter(novelId, currentChIndex, chapterPrice);
      if (success) {
        triggerToast("Unlocked successfully!");
        try {
          const res = await api.getChapterContent(novelId, currentChIndex);
          setChapterDetail(res.chapter);
          setIsLocked(res.locked);
        } catch (err) {
          console.error("Failed to reload unlocked chapter content:", err);
        }
      }
    } else {
      onNavigate('recharge');
    }
  };

  const getLineHeightVal = () => {
    if (lineHeight === 'narrow') return '1.5';
    if (lineHeight === 'wide') return '2.0';
    return '1.75';
  };

  if (!novel || !chapterDetail) {
    return (
      <div className="scroll-container animate-fade-in" style={{ textAlign: 'center', paddingTop: '100px', color: 'var(--text-secondary)' }}>
        <p>Loading reader content...</p>
      </div>
    );
  }

  const isInShelf = shelfBookIds.includes(novelId);

  return (
    <div className={`reader-container reader-theme-${theme} page-container-full`}>
      {toastMessage && <div className="toast-msg">{toastMessage}</div>}

      {/* Floating Back Button (Always visible when unlocked) */}
      {!isLocked && (
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
      )}

      {/* Settings HUD panel */}
      {!isLocked && (
        <ReaderSettings
          visible={showSettings}
          fontSize={fontSize}
          onFontSizeChange={setFontSize}
          theme={theme}
          onThemeChange={setTheme}
          onOpenDrawer={() => setShowTOC(true)}
          novelTitle={novel.title}
          onBack={() => onNavigate('detail', { id: novelId })}
          isInShelf={isInShelf}
          onAddToShelf={handleToggleShelf}
        />
      )}

      {/* Header displayed on Locked Screen */}
      {isLocked && (
        <header className="app-header glass-panel" style={{ color: 'var(--text-primary)', position: 'absolute', top: 0, left: 0, right: 0, zIndex: 10 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
            <button className="header-btn" onClick={() => onNavigate('detail', { id: novelId })} aria-label="Go back">
              <svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" strokeWidth={2.5} stroke="currentColor" style={{ width: '20px', height: '20px' }}>
                <path strokeLinecap="round" strokeLinejoin="round" d="M15.75 19.5L8.25 12l7.5-7.5" />
              </svg>
            </button>
            <h1 className="header-title" style={{ fontSize: '15px' }}>{novel.title}</h1>
          </div>
          <button className="header-btn" onClick={() => setShowTOC(true)} aria-label="Open TOC">
            <svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" strokeWidth={2} stroke="currentColor" style={{ width: '20px', height: '20px' }}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M8.25 6.75h12M8.25 12h12m-12 5.25h12M3.75 6.75h.007v.008H3.75V6.75zm.375 0a.375.375 0 11-.75 0 .375.375 0 01.75 0zM3.75 12h.007v.008H3.75V12zm.375 0a.375.375 0 11-.75 0 .375.375 0 01.75 0zm-.375 5.25h.007v.008H3.75v-.008zm.375 0a.375.375 0 11-.75 0 .375.375 0 01.75 0z" />
            </svg>
          </button>
        </header>
      )}

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
                onClick={() => handleChapterSelect(index, 'top')}
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

      {/* Main Text Content */}
      <div 
        ref={scrollRef}
        className={`reader-scroll-area ${isLocked ? 'reader-locked-preview' : ''}`}
        onScroll={isLocked ? undefined : handleScroll}
        onClick={isLocked ? undefined : handleScreenClick}
        onTouchStart={handleTouchStart}
        onTouchEnd={handleTouchEnd}
        style={{
          fontFamily: fontFamily === 'serif' ? 'var(--font-serif)' : 'var(--font-sans)',
          fontSize: `${fontSize}px`,
          lineHeight: getLineHeightVal(),
          overflowY: isLocked ? 'hidden' : 'auto',
          maxHeight: isLocked ? 'calc(100vh - 56px)' : 'none',
          position: 'relative',
          paddingTop: '64px',
          paddingBottom: isLocked ? '260px' : '80px'
        }}
      >
        <h2 className="reader-chapter-title">{chapterDetail.title}</h2>
        
        {loading ? (
          <div style={{ textAlign: 'center', padding: '40px', color: 'var(--text-secondary)' }}>
            Loading chapter text...
          </div>
        ) : (
          (chapterDetail.content || '').split('\n\n').map((paragraph, index) => (
            <p key={index} className="reader-text-paragraph">
              {paragraph}
            </p>
          ))
        )}

        {/* Faded partial text indicator for locked preview */}
        {isLocked && !loading && (
          <p className="reader-text-paragraph" style={{ opacity: 0.25, userSelect: 'none', filter: 'blur(0.5px)' }}>
            The ancient pathways of the cosmos were shrouded in starlight, hiding secrets that even the celestial kings dared not speak aloud...
          </p>
        )}

        {/* Regular Reader Navigation links */}
        {!isLocked && (
          <div className="reader-bottom-nav-row" onClick={(e) => e.stopPropagation()}>
            <button
              className="reader-chapter-nav-btn"
              disabled={currentChIndex === 0}
              onClick={() => handleChapterSelect(currentChIndex - 1, 'top')}
            >
              ← Previous Chapter
            </button>
            <button
              className="reader-chapter-nav-btn"
              disabled={currentChIndex === chapters.length - 1}
              onClick={() => handleChapterSelect(currentChIndex + 1, 'top')}
            >
              Next Chapter →
            </button>
          </div>
        )}
      </div>

      {/* Lock Screen Overlay */}
      {isLocked && !loading && (
        <div style={{
          position: 'absolute',
          bottom: 0,
          left: 0,
          right: 0,
          height: '70%',
          background: 'linear-gradient(to bottom, var(--reader-bg-transparent) 0%, var(--reader-bg) 35%, var(--reader-bg) 100%)',
          display: 'flex',
          flexDirection: 'column',
          alignItems: 'center',
          justifyContent: 'flex-end',
          padding: '24px 20px 48px',
          zIndex: 10,
          pointerEvents: 'none'
        }}>
          <div style={{
            pointerEvents: 'auto',
            display: 'flex',
            flexDirection: 'column',
            alignItems: 'center',
            width: '100%',
            maxWidth: '340px'
          }}>
            <div style={{
              width: '52px',
              height: '52px',
              borderRadius: '50%',
              backgroundColor: 'var(--reader-border)',
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              fontSize: '22px',
              marginBottom: '10px',
              boxShadow: 'var(--card-shadow)',
              color: 'var(--reader-text)'
            }}>
              🔒
            </div>

            <h3 className="rr-unlock-title" style={{ fontSize: '16px', fontWeight: 800, marginBottom: '4px', color: 'var(--reader-text)' }}>
              Unlock to Continue Reading
            </h3>
            <p style={{ fontSize: '12px', color: 'var(--text-tertiary)', marginBottom: '16px' }}>
              This chapter is locked ({chapterDetail.word_count.toLocaleString()} words)
            </p>

            <div style={{
              width: '100%',
              backgroundColor: 'var(--bg-secondary)',
              borderRadius: '16px',
              padding: '16px',
              border: '1px solid var(--border-color)',
              boxShadow: 'var(--card-shadow)',
              marginBottom: '16px',
              display: 'flex',
              flexDirection: 'column',
              gap: '10px'
            }}>
              <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: '13px' }}>
                <span style={{ color: 'var(--text-secondary)' }}>Price:</span>
                <span style={{ fontWeight: 700, color: 'var(--text-primary)' }}>{chapterPrice} Coins</span>
              </div>
              <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: '13px', borderTop: '1px solid var(--border-color)', paddingTop: '10px' }}>
                <span style={{ color: 'var(--text-secondary)' }}>Your Balance:</span>
                <span style={{ fontWeight: 700, color: userCoins >= chapterPrice ? '#22c55e' : '#ef4444' }}>
                  {userCoins} Coins
                </span>
              </div>

              {/* Auto Unlock checkbox */}
              <div style={{ display: 'flex', alignItems: 'center', gap: '8px', fontSize: '12px', marginTop: '2px' }}>
                <input 
                  type="checkbox" 
                  id="auto-unlock" 
                  checked={autoUnlock}
                  onChange={(e) => setAutoUnlock(e.target.checked)}
                  style={{ width: '15px', height: '15px', accentColor: 'var(--accent-color)', cursor: 'pointer' }}
                />
                <label htmlFor="auto-unlock" style={{ cursor: 'pointer', color: 'var(--text-secondary)' }}>
                  Auto-unlock next chapters
                </label>
              </div>
            </div>

            {/* Unlock Button */}
            <button 
              className={userCoins >= chapterPrice ? "btn-unlock-now" : "btn-top-up-now"}
              onClick={handleUnlockNow}
              style={{
                width: '100%',
                height: '48px',
                borderRadius: '24px',
                border: 'none',
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                gap: '8px',
                fontSize: '15px',
                fontWeight: 700,
                cursor: 'pointer',
                color: 'white',
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
                  <span>Insufficient Balance - Top Up</span>
                  <svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" strokeWidth={2.5} stroke="currentColor" style={{ width: '16px', height: '16px' }}>
                    <path strokeLinecap="round" strokeLinejoin="round" d="M8.25 4.5l7.5 7.5-7.5 7.5" />
                  </svg>
                </>
              )}
            </button>
          </div>
        </div>
      )}
    </div>
  );
};
