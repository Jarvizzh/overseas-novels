import { useState, useEffect, useRef } from 'react';
import { MOCK_NOVELS } from './data/novels';
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
import './App.css';

type PageType = 'home' | 'shelf' | 'rewards' | 'profile' | 'detail' | 'reader' | 'search' | 'recharge';

export default function App() {
  // Navigation Routing States
  const [currentPage, setCurrentPage] = useState<PageType>('home');
  const [pageParams, setPageParams] = useState<any>(null);
  const [activeTab, setActiveTab] = useState<'home' | 'shelf' | 'rewards' | 'profile'>('home');
  const [prevPageStack, setPrevPageStack] = useState<PageType[]>([]);

  // User auth state
  const [user, setUser] = useState<User | null>(null);

  // Dynamic Novels State (using local mocks as fallback)
  const [novels, setNovels] = useState<Novel[]>(MOCK_NOVELS as any);

  // Personal Shelf Saved Books
  const [shelfBookIds, setShelfBookIds] = useState<number[]>([]);

  // Reading Progress: { [bookId]: { chapterIndex, scrollOffsetPercentage } }
  const [readingProgress, setReadingProgress] = useState<{
    [bookId: number]: { chapterIndex: number; scrollOffsetPercentage: number };
  }>({});

  // Global theme settings
  const [globalTheme, setGlobalTheme] = useState<'light' | 'dark'>(() => {
    const saved = localStorage.getItem('color-scheme');
    if (saved === 'dark' || saved === 'light') return saved;
    return 'light';
  });

  // Wallet Coins Balance
  const [userCoins, setUserCoins] = useState<number>(0);

  // Unlocked Chapters List across books
  const [unlockedBookChapters, setUnlockedBookChapters] = useState<string[]>(() => {
    const saved = localStorage.getItem('unlocked-chapters');
    return saved ? JSON.parse(saved) : [];
  });

  // Wallet Transaction billing history
  const [transactionHistory, setTransactionHistory] = useState<Transaction[]>([]);

  // Simple debounce ref for reading progress sync
  const syncTimeouts = useRef<{ [bookId: string]: any }>({});

  // Auth & API Initializer
  const reloadWalletAndShelf = async () => {
    try {
      const balance = await api.getWalletBalance();
      setUserCoins(balance.charged_coins + balance.bonus_coins);
      
      const shelfItems = await api.getShelf();
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
  };

  const triggerGuestLogin = async () => {
    try {
      const res = await api.guestLogin();
      localStorage.setItem('auth-token', res.token);
      setUser(res.user);
      await reloadWalletAndShelf();
    } catch (err) {
      console.error("Guest login failed:", err);
    }
  };

  // Capture Facebook Ads / UTM tracking parameters on initial load
  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    const utmSource = params.get('utm_source');
    const utmCampaign = params.get('utm_campaign');
    const fbclid = params.get('fbclid');
    const pixelId = params.get('pixel_id');
    const templateId = params.get('template_id');

    if (utmSource) localStorage.setItem('utm_source', utmSource);
    if (utmCampaign) localStorage.setItem('utm_campaign', utmCampaign);
    if (pixelId) localStorage.setItem('fb_pixel_id', pixelId);
    if (templateId) localStorage.setItem('recharge_template_id', templateId);
    if (fbclid) {
      localStorage.setItem('fbclid', fbclid);
      // Hand-craft the standard Facebook _fbc cookie
      const creationTime = Date.now();
      const fbcValue = `fb.1.${creationTime}.${fbclid}`;
      
      const date = new Date();
      date.setTime(date.getTime() + 90 * 24 * 60 * 60 * 1000);
      document.cookie = `_fbc=${fbcValue}; expires=${date.toUTCString()}; path=/`;
    }

    // Auto-routing to specific novel/chapter if provided in URL parameters
    const novelIdStr = params.get('novel_id');
    const chapterIndex = params.get('chapter_index');
    if (novelIdStr) {
      const novelId = parseInt(novelIdStr, 10);
      if (!isNaN(novelId)) {
        if (chapterIndex) {
          setCurrentPage('reader');
          setPageParams({ id: novelId, chapterIndex: parseInt(chapterIndex, 10) - 1 });
        } else {
          setCurrentPage('detail');
          setPageParams({ id: novelId });
        }
      }
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
      // Clear all pending progress syncs on unmount
      Object.values(syncTimeouts.current).forEach(clearTimeout);
    };
  }, []);

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

  // View Transitions Router Navigate function
  const navigateTo = (page: string, params: any = null) => {
    const updateState = () => {
      // Save page navigation history stack for recharge back-triggers
      setPrevPageStack((prev) => [...prev, currentPage]);
      
      setCurrentPage(page as PageType);
      setPageParams(params);
      
      // Keep bottom navigation tab synchronized
      if (['home', 'shelf', 'rewards', 'profile'].includes(page)) {
        setActiveTab(page as any);
      }
    };

    if (!(document as any).startViewTransition) {
      updateState();
    } else {
      (document as any).startViewTransition(() => {
        updateState();
      });
    }
  };

  const navigateBack = () => {
    const updateState = () => {
      const nextStack = [...prevPageStack];
      const prevPage = nextStack.pop() || 'home';
      setPrevPageStack(nextStack);
      
      setCurrentPage(prevPage);
      if (['home', 'shelf', 'rewards', 'profile'].includes(prevPage)) {
        setActiveTab(prevPage as any);
      }
    };

    if (!(document as any).startViewTransition) {
      updateState();
    } else {
      (document as any).startViewTransition(() => {
        updateState();
      });
    }
  };

  // Manage adding/removing from shelf via API
  const handleToggleShelf = async (bookId: number) => {
    const inShelf = shelfBookIds.includes(bookId);
    try {
      if (inShelf) {
        await api.removeFromShelf([bookId]);
        setShelfBookIds((prev) => prev.filter((id) => id !== bookId));
      } else {
        await api.addToShelf(bookId);
        setShelfBookIds((prev) => [...prev, bookId]);
      }
    } catch (err) {
      console.error("Failed to toggle bookshelf:", err);
    }
  };

  const handleRemoveMultipleFromShelf = async (bookIds: number[]) => {
    try {
      await api.removeFromShelf(bookIds);
      setShelfBookIds((prev) => prev.filter((id) => !bookIds.includes(id)));
    } catch (err) {
      console.error("Failed to remove multiple books:", err);
    }
  };

  // Save progress from reading viewport with 2s debounce
  const handleSaveProgress = (bookId: number, chapterIndex: number, scrollOffsetPercentage: number) => {
    // 1. Local update for instant feedback
    setReadingProgress((prev) => ({
      ...prev,
      [bookId]: { chapterIndex, scrollOffsetPercentage },
    }));

    // 2. Debounce cloud sync
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
  };

  // Wallet Add Coins Trigger (Daily Checkin, Recharge)
  const handleAddCoins = async (_amount: number, _reason: string) => {
    // Reload state from database to ensure balance parity
    await reloadWalletAndShelf();
  };

  // Wallet Deduct Coins to Unlock Chapter (delegates to API)
  const handleUnlockChapter = async (bookId: number, chapterIndex: number, _price: number): Promise<boolean> => {
    try {
      await api.unlockChapter(bookId, chapterIndex);
      setUnlockedBookChapters((prev) => [...prev, `${bookId}-${chapterIndex}`]);
      await reloadWalletAndShelf();
      return true;
    } catch (err) {
      console.error("Failed to unlock chapter:", err);
      return false;
    }
  };

  // Renders page based on active route state
  const renderPage = () => {
    switch (currentPage) {
      case 'search':
        return (
          <Search 
            novels={novels as any} 
            onNavigate={navigateTo} 
            initialGenre={pageParams?.genre}
          />
        );
      case 'detail':
        return (
          <Detail
            novelId={pageParams?.id}
            novels={novels as any}
            onNavigate={navigateTo}
            shelfBookIds={shelfBookIds}
            onToggleShelf={handleToggleShelf}
            readingProgress={readingProgress}
          />
        );
      case 'reader':
        return (
          <Reader
            novelId={pageParams?.id}
            chapterIndex={pageParams?.chapterIndex}
            novels={novels as any}
            onNavigate={navigateTo}
            shelfBookIds={shelfBookIds}
            onToggleShelf={handleToggleShelf}
            readingProgress={readingProgress}
            onSaveProgress={handleSaveProgress}
            unlockedBookChapters={unlockedBookChapters}
            onUnlockChapter={handleUnlockChapter}
            userCoins={userCoins}
          />
        );
      case 'recharge':
        return (
          <Recharge
            userCoins={userCoins}
            onAddCoins={handleAddCoins}
            onBack={navigateBack}
          />
        );
      case 'shelf':
        return (
          <Shelf
            novels={novels as any}
            onNavigate={navigateTo}
            shelfBookIds={shelfBookIds}
            onRemoveFromShelf={handleRemoveMultipleFromShelf}
            readingProgress={readingProgress}
          />
        );
      case 'rewards':
        return (
          <Rewards 
            onAddCoins={handleAddCoins}
          />
        );
      case 'profile':
        return (
          <Profile
            shelfBookIds={shelfBookIds}
            readingProgress={readingProgress}
            onNavigate={navigateTo}
            globalTheme={globalTheme}
            onChangeGlobalTheme={setGlobalTheme}
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
        );
      case 'home':
      default:
        return (
          <Home 
            novels={novels as any} 
            onNavigate={navigateTo} 
            userCoins={userCoins}
          />
        );
    }
  };

  // Configure Header details based on current view
  const getHeaderDetails = () => {
    switch (currentPage) {
      case 'search':
        return {
          title: 'Search Books',
          showBack: true,
          onBack: navigateBack,
        };
      case 'shelf':
        return {
          title: 'My Library',
          showBack: false,
        };
      case 'rewards':
        return {
          title: 'Reward Center',
          showBack: false,
        };
      case 'profile':
        return {
          title: 'Profile Settings',
          showBack: false,
        };
      case 'home':
      default:
        return {
          title: 'StarNovel',
          showBack: false,
          rightElement: (
            <div style={{ display: 'flex', alignItems: 'center', gap: '10px' }}>
              {/* Clicking coin bubble redirects to Top Up */}
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

  // Full-screen pages (without global header & bottom navigation bar)
  const isFullScreenPage = ['reader', 'detail', 'recharge'].includes(currentPage);
  const headerInfo = getHeaderDetails();

  const isHomePage = currentPage === 'home';

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
        {renderPage()}
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
