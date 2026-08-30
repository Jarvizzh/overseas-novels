import { useState, useEffect, useRef, useCallback } from 'react';
import { Routes, Route, useNavigate, useLocation, useSearchParams } from 'react-router-dom';
import { Header } from './components/Header';
import { BottomNav } from './components/BottomNav';
import { Home } from './pages/Home';
import { Search } from './pages/Search';
import { Detail } from './pages/Detail';
import { Reader } from './pages/Reader';
import { Shelf } from './pages/Shelf';
import { Profile } from './pages/Profile';
import { Rewards } from './pages/Rewards';
import { Recharge } from './pages/Recharge';
import { GoldCoin } from './components/GoldCoin';
import { api } from './utils/api';
import type { Novel, User, Transaction } from './utils/api';
import { ToastProvider } from './context/ToastContext';
import { ConfirmProvider } from './context/ConfirmContext';
import './App.css';

function AppContent() {
  const navigate = useNavigate();
  const location = useLocation();
  const [searchParams] = useSearchParams();

  // User auth state
  const [user, setUser] = useState<User | null>(null);

  // Dynamic Novels State loaded from Backend API
  const [novels, setNovels] = useState<Novel[]>([]);

  // Personal Shelf Saved Books
  const [shelfBookIds, setShelfBookIds] = useState<number[]>([]);

  // Reading Progress: { [bookId]: { chapterIndex, scrollOffsetPercentage, paragraphIndex } }
  const [readingProgress, setReadingProgress] = useState<{
    [bookId: number]: { chapterIndex: number; scrollOffsetPercentage: number; paragraphIndex?: number };
  }>({});

  // Global theme settings
  const [globalTheme] = useState<'light' | 'dark'>(() => {
    const saved = localStorage.getItem('color-scheme');
    if (saved === 'dark' || saved === 'light') return saved;
    return 'light';
  });

  // Wallet Coins Balance
  const [userCoins, setUserCoins] = useState<number>(0);

  // Unlocked Chapters List across books
  const [unlockedBookChapters, setUnlockedBookChapters] = useState<string[]>(() => {
    try {
      const saved = localStorage.getItem('unlocked-chapters');
      return saved ? JSON.parse(saved) : [];
    } catch {
      return [];
    }
  });

  // Wallet Transaction billing history
  const [transactionHistory, setTransactionHistory] = useState<Transaction[]>([]);

  // Simple debounce ref for reading progress sync
  const syncTimeouts = useRef<{ [bookId: string]: any }>({});

  // Auth & API Initializer
  const reloadWalletAndShelf = useCallback(async () => {
    try {
      const balance = await api.getWalletBalance();
      setUserCoins(balance.charged_coins + balance.bonus_coins);
      
      const shelfItems = (await api.getShelf()) || [];
      setShelfBookIds(shelfItems.map(item => item.novel_id));
      
      const progressMap: { [bookId: number]: { chapterIndex: number; scrollOffsetPercentage: number } } = {};
      shelfItems.forEach(item => {
        progressMap[item.novel_id] = {
          chapterIndex: item.chapter_index,
          scrollOffsetPercentage: item.scroll_offset_percentage,
        };
      });
      setReadingProgress(progressMap);

      const txs = await api.getTransactionHistory(1, 30);
      setTransactionHistory(txs);
    } catch (err) {
      console.error("Failed to load shelf or wallet info:", err);
    }
  }, []);

  const triggerGuestLogin = useCallback(async () => {
    try {
      const res = await api.guestLogin();
      localStorage.setItem('auth-token', res.token);
      setUser(res.user);
      await reloadWalletAndShelf();
    } catch (err) {
      console.error("Guest login failed:", err);
    }
  }, [reloadWalletAndShelf]);

  // Capture Facebook Ads / UTM tracking parameters on initial load
  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    const utmSource = params.get('utm_source');
    const utmCampaign = params.get('utm_campaign');
    const fbclid = params.get('fbclid');
    const pixelId = params.get('pixel_id');
    const templateId = params.get('template_id');
    const linkId = params.get('link_id') || params.get('promo_id');

    if (utmSource) localStorage.setItem('utm_source', utmSource);
    if (utmCampaign) localStorage.setItem('utm_campaign', utmCampaign);
    if (pixelId) localStorage.setItem('fb_pixel_id', pixelId);
    if (templateId) localStorage.setItem('recharge_template_id', templateId);
    if (linkId) localStorage.setItem('promo_id', linkId);
    if (fbclid) {
      localStorage.setItem('fbclid', fbclid);
      const creationTime = Date.now();
      const fbcValue = `fb.1.${creationTime}.${fbclid}`;
      
      const date = new Date();
      date.setTime(date.getTime() + 90 * 24 * 60 * 60 * 1000);
      document.cookie = `_fbc=${fbcValue}; expires=${date.toUTCString()}; path=/`;
    }

    const getCookie = (name: string): string => {
      const value = `; ${document.cookie}`;
      const parts = value.split(`; ${name}=`);
      if (parts.length === 2) return parts.pop()?.split(';').shift() || '';
      return '';
    };

    if (!getCookie('_fbp')) {
      const creationTime = Date.now();
      const randomNumber = Math.floor(1000000000 + Math.random() * 9000000000);
      const fbpValue = `fb.1.${creationTime}.${randomNumber}`;
      
      const date = new Date();
      date.setTime(date.getTime() + 90 * 24 * 60 * 60 * 1000);
      document.cookie = `_fbp=${fbpValue}; expires=${date.toUTCString()}; path=/`;
    }
  }, []);

  useEffect(() => {
    const initAuth = async () => {
      const token = localStorage.getItem('auth-token');
      if (!token) {
        await triggerGuestLogin();
      } else {
        try {
          const u = await api.getProfile();
          setUser(u);
          await reloadWalletAndShelf();
        } catch (err) {
          console.error("Auth init failed, fallback to guest:", err);
          await triggerGuestLogin();
        }
      }
    };

    const loadNovels = async () => {
      try {
        const list = await api.getNovels();
        if (list && list.length > 0) {
          setNovels(list);
        }
      } catch (err) {
        console.error("Failed to load novels catalog:", err);
      }
    };

    const handleUnauthorized = () => {
      triggerGuestLogin();
    };

    initAuth();
    loadNovels();

    window.addEventListener('auth-unauthorized', handleUnauthorized);
    return () => {
      window.removeEventListener('auth-unauthorized', handleUnauthorized);
      Object.values(syncTimeouts.current).forEach(clearTimeout);
    };
  }, [triggerGuestLogin, reloadWalletAndShelf]);

  // Sync state changes to local storage for unlocked chapters cache
  useEffect(() => {
    localStorage.setItem('unlocked-chapters', JSON.stringify(unlockedBookChapters));
  }, [unlockedBookChapters]);

  // Handle Global Theme settings
  useEffect(() => {
    const root = document.documentElement;
    root.setAttribute('data-user-theme', globalTheme);
    localStorage.setItem('color-scheme', globalTheme);
  }, [globalTheme]);

  // Centralized SPA Router Navigation helper preserving CMS tracking params
  const navigateTo = useCallback((pageOrPath: string, params: any = null) => {
    const trackingKeys = ['utm_source', 'utm_campaign', 'fbclid', 'pixel_id', 'template_id', 'link_id', 'promo_id'];
    const newSearchParams = new URLSearchParams();
    
    trackingKeys.forEach((key) => {
      const val = searchParams.get(key);
      if (val) newSearchParams.set(key, val);
    });

    let targetPath = '/';
    if (pageOrPath === 'detail' && params?.id) {
      targetPath = '/detail';
      newSearchParams.set('novel_id', String(params.id));
    } else if (pageOrPath === 'reader' && params?.id) {
      targetPath = '/content';
      newSearchParams.set('novel_id', String(params.id));
      if (params.chapterIndex !== undefined) {
        newSearchParams.set('chapter_index', String(params.chapterIndex + 1));
      }
    } else if (pageOrPath === 'search') {
      targetPath = '/search';
      if (params?.genre) {
        newSearchParams.set('genre', params.genre);
      }
    } else if (pageOrPath === 'shelf') {
      targetPath = '/shelf';
    } else if (pageOrPath === 'rewards') {
      targetPath = '/rewards';
    } else if (pageOrPath === 'profile') {
      targetPath = '/profile';
    } else if (pageOrPath === 'recharge') {
      targetPath = '/recharge';
    } else if (pageOrPath === 'home') {
      targetPath = '/';
    } else if (pageOrPath.startsWith('/')) {
      targetPath = pageOrPath;
    }

    const finalUrl = `${targetPath}${newSearchParams.toString() ? `?${newSearchParams.toString()}` : ''}`;

    const doNavigate = () => {
      navigate(finalUrl);
    };

    if (!(document as any).startViewTransition) {
      doNavigate();
    } else {
      (document as any).startViewTransition(() => {
        doNavigate();
      });
    }
  }, [navigate, searchParams]);

  const navigateBack = useCallback(() => {
    const doNavigate = () => {
      navigate(-1);
    };

    if (!(document as any).startViewTransition) {
      doNavigate();
    } else {
      (document as any).startViewTransition(() => {
        doNavigate();
      });
    }
  }, [navigate]);

  // Manage adding/removing from shelf via API
  const handleToggleShelf = useCallback(async (bookId: number) => {
    setShelfBookIds((prev) => {
      const inShelf = prev.includes(bookId);
      if (inShelf) {
        api.removeFromShelf([bookId]).catch(err => console.error("Failed to remove from shelf:", err));
        return prev.filter((id) => id !== bookId);
      } else {
        api.addToShelf(bookId).catch(err => console.error("Failed to add to shelf:", err));
        return [...prev, bookId];
      }
    });
  }, []);

  const handleRemoveMultipleFromShelf = useCallback(async (bookIds: number[]) => {
    try {
      await api.removeFromShelf(bookIds);
      setShelfBookIds((prev) => prev.filter((id) => !bookIds.includes(id)));
    } catch (err) {
      console.error("Failed to remove multiple books:", err);
    }
  }, []);

  // Save progress from reading viewport with 2s debounce
  const handleSaveProgress = useCallback((bookId: number, chapterIndex: number, scrollOffsetPercentage: number, paragraphIndex?: number) => {
    setReadingProgress((prev) => ({
      ...prev,
      [bookId]: { chapterIndex, scrollOffsetPercentage, paragraphIndex },
    }));

    if (syncTimeouts.current[bookId]) {
      clearTimeout(syncTimeouts.current[bookId]);
    }

    syncTimeouts.current[bookId] = setTimeout(async () => {
      try {
        await api.syncProgress([{
          novel_id: bookId,
          chapter_index: chapterIndex,
          scroll_offset_percentage: scrollOffsetPercentage,
          updated_at: new Date().toISOString()
        }]);
        delete syncTimeouts.current[bookId];
      } catch (err) {
        console.error("Reading progress sync failed:", err);
      }
    }, 2000);
  }, []);

  // Wallet Add Coins Trigger
  const handleAddCoins = useCallback(async (_amount: number, _reason: string) => {
    await reloadWalletAndShelf();
  }, [reloadWalletAndShelf]);

  // Wallet Deduct Coins to Unlock Chapter
  const handleUnlockChapter = useCallback(async (bookId: number, chapterIndex: number, _price: number): Promise<boolean> => {
    try {
      await api.unlockChapter(bookId, chapterIndex);
      setUnlockedBookChapters((prev) => [...prev, `${bookId}-${chapterIndex}`]);
      await reloadWalletAndShelf();
      return true;
    } catch (err) {
      console.error("Failed to unlock chapter:", err);
      return false;
    }
  }, [reloadWalletAndShelf]);

  // Active Bottom Navigation Tab based on pathname
  const getActiveTab = (): 'home' | 'shelf' | 'rewards' | 'profile' => {
    if (location.pathname === '/shelf') return 'shelf';
    if (location.pathname === '/rewards') return 'rewards';
    if (location.pathname === '/profile') return 'profile';
    return 'home';
  };

  const activeTab = getActiveTab();

  // Configure Header details based on current pathname
  const getHeaderDetails = () => {
    switch (location.pathname) {
      case '/search':
        return {
          title: 'Search Books',
          showBack: true,
          onBack: navigateBack,
        };
      case '/shelf':
        return {
          title: 'My Library',
          showBack: false,
        };
      case '/rewards':
        return {
          title: 'Reward Center',
          showBack: false,
        };
      case '/profile':
        return {
          title: 'Profile Settings',
          showBack: false,
        };
      case '/':
      default:
        return {
          title: 'StarNovel',
          showBack: false,
          rightElement: (
            <div style={{ display: 'flex', alignItems: 'center', gap: '10px' }}>
              <div 
                onClick={() => navigateTo('recharge')}
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: '4px',
                  backgroundColor: 'var(--bg-tertiary)',
                  padding: '4px 10px',
                  borderRadius: '99px',
                  fontSize: '11px',
                  fontWeight: 700,
                  cursor: 'pointer',
                  border: '1px solid var(--border-color)',
                  color: 'var(--text-primary)',
                  boxShadow: 'var(--card-shadow)'
                }}
              >
                <GoldCoin size={13} />
                <span>{userCoins}</span>
                <span style={{ color: 'var(--accent-color)', fontWeight: 800 }}>+</span>
              </div>
              <button 
                className="header-btn" 
                onClick={() => navigateTo('search')}
                aria-label="Open search"
              >
                <svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" strokeWidth={2.2} stroke="currentColor" style={{ width: '18px', height: '18px' }}>
                  <path strokeLinecap="round" strokeLinejoin="round" d="M21 21l-5.197-5.197m0 0A7.5 7.5 0 105.196 5.196a7.5 7.5 0 0010.637 10.637z" />
                </svg>
              </button>
            </div>
          ),
        };
    }
  };

  const isFullScreenPage = ['/content', '/detail', '/recharge'].includes(location.pathname);
  const headerInfo = getHeaderDetails();
  const isHomePage = location.pathname === '/';

  // Extract query parameters for direct Route element rendering
  const detailNovelId = parseInt(searchParams.get('novel_id') || '0', 10);
  const readerNovelId = parseInt(searchParams.get('novel_id') || '0', 10);
  const readerChapterIdxStr = searchParams.get('chapter_index');
  const readerChapterIndex = readerChapterIdxStr ? parseInt(readerChapterIdxStr, 10) - 1 : 0;
  const searchGenre = searchParams.get('genre') || undefined;

  return (
    <>
      {!isFullScreenPage && !isHomePage && (
        <Header
          title={headerInfo.title}
          showBack={headerInfo.showBack}
          onBack={headerInfo.onBack}
          rightElement={headerInfo.rightElement}
        />
      )}
      
      <main className={isFullScreenPage ? 'page-container-full' : (isHomePage ? 'page-container-home' : 'page-container')}>
        <Routes>
          <Route path="/" element={<Home novels={novels} onNavigate={navigateTo} userCoins={userCoins} />} />
          <Route path="/shelf" element={<Shelf novels={novels} onNavigate={navigateTo} shelfBookIds={shelfBookIds} onRemoveFromShelf={handleRemoveMultipleFromShelf} readingProgress={readingProgress} />} />
          <Route path="/rewards" element={<Rewards onAddCoins={handleAddCoins} globalTheme={globalTheme} />} />
          <Route 
            path="/profile" 
            element={
              <Profile
                shelfBookIds={shelfBookIds}
                readingProgress={readingProgress}
                onNavigate={navigateTo}
                userCoins={userCoins}
                transactionHistory={transactionHistory}
                currentUser={user}
                onLoginSuccess={async (token, u) => {
                  localStorage.setItem('auth-token', token);
                  setUser(u);
                  await reloadWalletAndShelf();
                  navigateTo('profile');
                }}
                onLogout={() => {
                  localStorage.removeItem('auth-token');
                  setUser(null);
                  setShelfBookIds([]);
                  setReadingProgress({});
                  setUserCoins(0);
                  setTransactionHistory([]);
                  triggerGuestLogin();
                }}
              />
            } 
          />
          <Route path="/search" element={<Search novels={novels} onNavigate={navigateTo} initialGenre={searchGenre} />} />
          <Route path="/detail" element={<Detail novelId={detailNovelId} novels={novels} onNavigate={navigateTo} shelfBookIds={shelfBookIds} onToggleShelf={handleToggleShelf} readingProgress={readingProgress} />} />
          <Route path="/content" element={<Reader novelId={readerNovelId} chapterIndex={readerChapterIndex} novels={novels} onNavigate={navigateTo} shelfBookIds={shelfBookIds} onToggleShelf={handleToggleShelf} readingProgress={readingProgress} onSaveProgress={handleSaveProgress} unlockedBookChapters={unlockedBookChapters} onUnlockChapter={handleUnlockChapter} userCoins={userCoins} />} />
          <Route path="/recharge" element={<Recharge userCoins={userCoins} onAddCoins={handleAddCoins} onBack={navigateBack} />} />
        </Routes>
      </main>

      {!isFullScreenPage && (
        <BottomNav
          activeTab={activeTab}
          onChangeTab={(tab) => navigateTo(tab)}
        />
      )}
    </>
  );
}

export default function App() {
  return (
    <ToastProvider>
      <ConfirmProvider>
        <AppContent />
      </ConfirmProvider>
    </ToastProvider>
  );
}
