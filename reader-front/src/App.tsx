import { useState, useEffect, useRef, useCallback } from 'react';
import { flushSync } from 'react-dom';
import { Routes, Route, useNavigate, useLocation, useSearchParams } from 'react-router-dom';
import { Header } from './components/Header';
import { BottomNav, type NavTabType } from './components/BottomNav';
import { TopNav } from './components/TopNav';
import { Home } from './pages/Home';
import { Search } from './pages/Search';
import { Detail } from './pages/Detail';
import { Reader } from './pages/Reader';
import { Shelf } from './pages/Shelf';
import { Profile } from './pages/Profile';
import { Rewards } from './pages/Rewards';
import { Recharge } from './pages/Recharge';
import { api, registerSessionRefreshHandler } from './utils/api';
import type { Novel, User, Transaction, SubscriptionInfo } from './utils/api';
import { ToastProvider } from './context/ToastContext';
import { ConfirmProvider } from './context/ConfirmContext';
import {
  applyThemePreference,
  getStoredThemePreference,
  getSystemTheme,
  type ThemePreference,
} from './utils/theme';
import './App.css';

const TRACKING_KEYS = [
  'utm_source',
  'utm_campaign',
  'fbclid',
  'pixel_id',
  'template_id',
  'link_id',
  'promo_id',
];

const TX_PAGE_SIZE = 30;

function loadScopedUnlockedChapters(userId: number | null): string[] {
  if (!userId) return [];
  try {
    const raw = localStorage.getItem(`unlocked-chapters-u:${userId}`);
    if (!raw) return [];
    const parsed = JSON.parse(raw);
    return Array.isArray(parsed) ? parsed.filter((x): x is string => typeof x === 'string') : [];
  } catch {
    return [];
  }
}

function saveScopedUnlockedChapters(userId: number | null, list: string[]): void {
  if (!userId) return;
  try {
    localStorage.setItem(`unlocked-chapters-u:${userId}`, JSON.stringify(list));
  } catch {
    // Ignore storage errors
  }
}

function runWithViewTransition(update: () => void): void {
  const prefersReduced =
    typeof window !== 'undefined' &&
    window.matchMedia?.('(prefers-reduced-motion: reduce)').matches;

  const doc = document as Document & { startViewTransition?: (cb: () => void) => unknown };
  if (!doc.startViewTransition || prefersReduced) {
    update();
    return;
  }

  doc.startViewTransition(() => {
    flushSync(() => {
      update();
    });
  });
}

function AppContent() {
  const navigate = useNavigate();
  const location = useLocation();
  const [searchParams] = useSearchParams();

  const [user, setUser] = useState<User | null>(null);
  const [authReady, setAuthReady] = useState(false);

  const [novels, setNovels] = useState<Novel[]>([]);
  const [latestNovels, setLatestNovels] = useState<Novel[]>([]);
  const [novelsPage, setNovelsPage] = useState(1);
  const [hasMoreNovels, setHasMoreNovels] = useState(false);
  const [loadingMoreNovels, setLoadingMoreNovels] = useState(false);

  const [shelfLoading, setShelfLoading] = useState(true);
  const [shelfBookIds, setShelfBookIds] = useState<number[]>([]);
  const [shelfNovelsList, setShelfNovelsList] = useState<Novel[]>([]);

  const [readingProgress, setReadingProgress] = useState<{
    [bookId: number]: { chapterIndex: number; scrollOffsetPercentage: number; paragraphIndex?: number };
  }>({});

  const [themePreference, setThemePreference] = useState<ThemePreference>(getStoredThemePreference);
  const [systemTheme, setSystemTheme] = useState<'light' | 'dark'>(getSystemTheme);

  const [userCoins, setUserCoins] = useState<number>(0);
  const [chargedCoins, setChargedCoins] = useState<number>(0);
  const [bonusCoins, setBonusCoins] = useState<number>(0);
  const [isVip, setIsVip] = useState<boolean>(false);
  const [subscription, setSubscription] = useState<SubscriptionInfo | null>(null);

  const [unlockedBookChapters, setUnlockedBookChapters] = useState<string[]>([]);
  const [transactionHistory, setTransactionHistory] = useState<Transaction[]>([]);
  const [transactionsPage, setTransactionsPage] = useState(1);
  const [hasMoreTransactions, setHasMoreTransactions] = useState(false);
  const [loadingMoreTransactions, setLoadingMoreTransactions] = useState(false);

  const syncTimeouts = useRef<Record<string, ReturnType<typeof setTimeout>>>({});
  const guestLoginPromiseRef = useRef<Promise<string | null> | null>(null);
  const pendingProgressRef = useRef<
    Record<string, { novel_id: number; chapter_index: number; scroll_offset_percentage: number; updated_at: string }>
  >({});

  useEffect(() => {
    if (user?.id) {
      setUnlockedBookChapters(loadScopedUnlockedChapters(user.id));
    } else {
      setUnlockedBookChapters([]);
    }
  }, [user?.id]);

  useEffect(() => {
    const mq = window.matchMedia?.('(prefers-color-scheme: dark)');
    const handleSystemChange = (e: MediaQueryListEvent) => {
      setSystemTheme(e.matches ? 'dark' : 'light');
    };
    mq?.addEventListener('change', handleSystemChange);

    const handleStorage = (e: StorageEvent) => {
      if (e.key === 'color-scheme') {
        setThemePreference(getStoredThemePreference());
      }
    };
    window.addEventListener('storage', handleStorage);

    return () => {
      mq?.removeEventListener('change', handleSystemChange);
      window.removeEventListener('storage', handleStorage);
    };
  }, []);

  useEffect(() => {
    applyThemePreference(themePreference);
  }, [themePreference]);

  const reloadWalletAndShelf = useCallback(async () => {
    try {
      const [balance, shelfItems, txs, subStatus] = await Promise.all([
        api.getWalletBalance(),
        api.getShelf().then((res) => res || []),
        api.getTransactionHistory(1, TX_PAGE_SIZE).then((res) => res || []),
        api.getSubscriptionStatus().catch(() => null),
      ]);

      setChargedCoins(balance.charged_coins);
      setBonusCoins(balance.bonus_coins);
      setUserCoins(balance.charged_coins + balance.bonus_coins);

      setShelfBookIds(shelfItems.map((item) => item.novel_id));
      setShelfNovelsList(shelfItems.map((item) => item.novel).filter(Boolean));

      const progressMap: { [bookId: number]: { chapterIndex: number; scrollOffsetPercentage: number } } = {};
      shelfItems.forEach((item) => {
        progressMap[item.novel_id] = {
          chapterIndex: item.chapter_index,
          scrollOffsetPercentage: item.scroll_offset_percentage,
        };
      });
      setReadingProgress(progressMap);

      setTransactionHistory(txs);
      setTransactionsPage(1);
      setHasMoreTransactions(txs.length >= TX_PAGE_SIZE);

      if (subStatus) {
        setIsVip(Boolean(subStatus.is_vip));
        setSubscription(subStatus.subscription);
      }
    } catch (err) {
      console.error('Failed to load shelf or wallet info:', err);
    } finally {
      setShelfLoading(false);
    }
  }, []);

  const handleLoadMoreTransactions = useCallback(async () => {
    if (loadingMoreTransactions || !hasMoreTransactions) return;
    setLoadingMoreTransactions(true);
    try {
      const nextPage = transactionsPage + 1;
      const batch = (await api.getTransactionHistory(nextPage, TX_PAGE_SIZE)) || [];
      setTransactionHistory((prev) => {
        const seen = new Set(prev.map((t) => t.id));
        const unique = batch.filter((t) => !seen.has(t.id));
        return [...prev, ...unique];
      });
      setTransactionsPage(nextPage);
      setHasMoreTransactions(batch.length >= TX_PAGE_SIZE);
    } catch (err) {
      console.error('Failed to load more transactions:', err);
    } finally {
      setLoadingMoreTransactions(false);
    }
  }, [loadingMoreTransactions, hasMoreTransactions, transactionsPage]);

  const triggerGuestLogin = useCallback(async (): Promise<string | null> => {
    if (!guestLoginPromiseRef.current) {
      guestLoginPromiseRef.current = (async () => {
        try {
          let res;
          try {
            res = await api.refresh();
          } catch (error) {
            if (
              error instanceof Error &&
              'status' in error &&
              error.status === 401 &&
              !localStorage.getItem('auth-token')
            ) {
              res = await api.guestLogin();
            } else {
              throw error;
            }
          }
          localStorage.setItem('auth-token', res.token);
          setUser(res.user);
          await reloadWalletAndShelf();
          return res.token;
        } catch (err) {
          console.error('Guest login failed:', err);
          setShelfLoading(false);
          return null;
        } finally {
          guestLoginPromiseRef.current = null;
        }
      })();
    }
    return guestLoginPromiseRef.current;
  }, [reloadWalletAndShelf]);

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
    registerSessionRefreshHandler(triggerGuestLogin);

    const initAuth = async () => {
      const tokenBefore = localStorage.getItem('auth-token');
      if (!tokenBefore) {
        await triggerGuestLogin();
      } else {
        try {
          const u = await api.getProfile();
          setUser(u);
          const tokenAfter = localStorage.getItem('auth-token');
          // Only reload if 401 refresh didn't already execute reloadWalletAndShelf
          if (tokenAfter === tokenBefore) {
            await reloadWalletAndShelf();
          }
        } catch (err) {
          console.error('Auth init failed, fallback to guest:', err);
          await triggerGuestLogin();
        }
      }
      setAuthReady(true);
    };

    const loadNovels = async () => {
      try {
        const [popularList, newestList] = await Promise.all([
          api.getNovels({ sort: 'popular', page: 1, limit: 50 }),
          api.getNovels({ sort: 'latest', page: 1, limit: 12 }),
        ]);
        if (popularList) {
          setNovels(popularList);
          setNovelsPage(1);
          setHasMoreNovels(popularList.length >= 50);
        }
        if (newestList) {
          setLatestNovels(newestList);
        }
      } catch (err) {
        console.error('Failed to load novels catalog:', err);
      }
    };

    initAuth();
    loadNovels();

    const flushPendingProgress = () => {
      const pending = Object.values(pendingProgressRef.current);
      if (pending.length === 0) return;
      Object.values(syncTimeouts.current).forEach(clearTimeout);
      syncTimeouts.current = {};
      pendingProgressRef.current = {};
      api.syncProgress(pending, { keepalive: true }).catch(() => {});
    };

    const handleVisibilityChange = () => {
      if (document.visibilityState === 'hidden') {
        flushPendingProgress();
      }
    };

    window.addEventListener('pagehide', flushPendingProgress);
    document.addEventListener('visibilitychange', handleVisibilityChange);

    const timeoutsMap = syncTimeouts.current;
    return () => {
      registerSessionRefreshHandler(null);
      window.removeEventListener('pagehide', flushPendingProgress);
      document.removeEventListener('visibilitychange', handleVisibilityChange);
      Object.values(timeoutsMap).forEach(clearTimeout);
    };
  }, [triggerGuestLogin, reloadWalletAndShelf]);

  useEffect(() => {
    if (location.pathname === '/content' || location.pathname === '/detail') {
      localStorage.setItem('last_read_content_path', `${location.pathname}${location.search}`);
    }
  }, [location.pathname, location.search]);

  useEffect(() => {
    const titleMap: Record<string, string> = {
      '/': 'StarNovel — Curated Fiction & Stories',
      '/shelf': 'My Library | StarNovel',
      '/rewards': 'Daily Rewards | StarNovel',
      '/profile': 'Profile | StarNovel',
      '/search': 'Search | StarNovel',
      '/detail': 'Book Details | StarNovel',
      '/content': 'Reader | StarNovel',
      '/recharge': 'Membership & Coins | StarNovel',
    };
    document.title = titleMap[location.pathname] || 'StarNovel';
  }, [location.pathname]);

  const buildMergedUrl = useCallback(
    (pageOrPath: string, params: Record<string, unknown> | null = null): string => {
      const mergedParams = new URLSearchParams();
      TRACKING_KEYS.forEach((key) => {
        const val = searchParams.get(key);
        if (val) mergedParams.set(key, val);
      });

      let targetPath = '/';
      if (pageOrPath === 'detail' && params?.id) {
        targetPath = '/detail';
        mergedParams.set('novel_id', String(params.id));
      } else if (pageOrPath === 'reader' && params?.id) {
        targetPath = '/content';
        mergedParams.set('novel_id', String(params.id));
        if (typeof params.chapterIndex === 'number') {
          mergedParams.set('chapter_index', String(params.chapterIndex + 1));
        }
      } else if (pageOrPath === 'search') {
        targetPath = '/search';
        if (typeof params?.genre === 'string' && params.genre) {
          mergedParams.set('genre', params.genre);
        }
      } else if (pageOrPath === 'shelf') {
        targetPath = '/shelf';
      } else if (pageOrPath === 'rewards') {
        targetPath = '/rewards';
      } else if (pageOrPath === 'profile') {
        targetPath = '/profile';
      } else if (pageOrPath === 'recharge') {
        targetPath = '/recharge';
        if (typeof params?.id === 'number') mergedParams.set('novel_id', String(params.id));
      } else if (pageOrPath === 'home') {
        targetPath = '/';
      } else if (pageOrPath.startsWith('/')) {
        const parsed = new URL(pageOrPath, window.location.origin);
        targetPath = parsed.pathname;
        parsed.searchParams.forEach((value, key) => {
          mergedParams.set(key, value);
        });
      }

      const qs = mergedParams.toString();
      return `${targetPath}${qs ? `?${qs}` : ''}`;
    },
    [searchParams]
  );

  const navigateTo = useCallback(
    (pageOrPath: string, params: Record<string, unknown> | null = null) => {
      const finalUrl = buildMergedUrl(pageOrPath, params);
      runWithViewTransition(() => navigate(finalUrl));
    },
    [buildMergedUrl, navigate]
  );

  const syncReaderChapterUrl = useCallback(
    (bookId: number, chapterIndex: number) => {
      const finalUrl = buildMergedUrl('reader', { id: bookId, chapterIndex });
      navigate(finalUrl, { replace: true });
    },
    [buildMergedUrl, navigate]
  );

  const navigateBack = useCallback(() => {
    const historyIdx = typeof window.history.state?.idx === 'number' ? window.history.state.idx : 0;
    runWithViewTransition(() => {
      if (historyIdx > 0) {
        navigate(-1);
      } else {
        navigate(buildMergedUrl('home'));
      }
    });
  }, [navigate, buildMergedUrl]);

  const handleLoadMoreNovels = useCallback(async () => {
    if (loadingMoreNovels || !hasMoreNovels) return;
    setLoadingMoreNovels(true);
    try {
      const nextPage = novelsPage + 1;
      const batch = (await api.getNovels({ sort: 'popular', page: nextPage, limit: 50 })) || [];
      setNovels((prev) => {
        const seen = new Set(prev.map((n) => n.id));
        const unique = batch.filter((n) => !seen.has(n.id));
        return [...prev, ...unique];
      });
      setNovelsPage(nextPage);
      setHasMoreNovels(batch.length >= 50);
    } catch (err) {
      console.error('Failed to load more novels:', err);
    } finally {
      setLoadingMoreNovels(false);
    }
  }, [loadingMoreNovels, hasMoreNovels, novelsPage]);

  const handleToggleShelf = useCallback(
    async (bookId: number) => {
      const inShelf = shelfBookIds.includes(bookId);
      if (inShelf) {
        const prevIds = shelfBookIds;
        const prevList = shelfNovelsList;
        setShelfBookIds((prev) => prev.filter((id) => id !== bookId));
        setShelfNovelsList((list) => list.filter((n) => n.id !== bookId));
        try {
          await api.removeFromShelf([bookId]);
        } catch (err) {
          console.error('Failed to remove from shelf:', err);
          setShelfBookIds(prevIds);
          setShelfNovelsList(prevList);
        }
        return;
      }

      setShelfBookIds((prev) => (prev.includes(bookId) ? prev : [...prev, bookId]));
      const known = novels.find((n) => n.id === bookId) || latestNovels.find((n) => n.id === bookId);
      if (known) {
        setShelfNovelsList((list) => (list.some((n) => n.id === bookId) ? list : [known, ...list]));
      }
      try {
        await api.addToShelf(bookId);
        if (!known) {
          const detail = await api.getNovelDetail(bookId);
          if (detail) {
            setShelfNovelsList((list) => (list.some((n) => n.id === bookId) ? list : [detail, ...list]));
          }
        }
      } catch (err) {
        console.error('Failed to add to shelf:', err);
        setShelfBookIds((prev) => prev.filter((id) => id !== bookId));
        setShelfNovelsList((list) => list.filter((n) => n.id !== bookId));
      }
    },
    [shelfBookIds, shelfNovelsList, novels, latestNovels]
  );

  const handleRemoveMultipleFromShelf = useCallback(async (bookIds: number[]) => {
    try {
      await api.removeFromShelf(bookIds);
      setShelfBookIds((prev) => prev.filter((id) => !bookIds.includes(id)));
      setShelfNovelsList((prev) => prev.filter((n) => !bookIds.includes(n.id)));
    } catch (err) {
      console.error('Failed to remove multiple books:', err);
    }
  }, []);

  const handleSaveProgress = useCallback(
    (bookId: number, chapterIndex: number, scrollOffsetPercentage: number, paragraphIndex?: number) => {
      setReadingProgress((prev) => ({
        ...prev,
        [bookId]: { chapterIndex, scrollOffsetPercentage, paragraphIndex },
      }));

      const payload = {
        novel_id: bookId,
        chapter_index: chapterIndex,
        scroll_offset_percentage: scrollOffsetPercentage,
        updated_at: new Date().toISOString(),
      };
      pendingProgressRef.current[bookId] = payload;

      if (syncTimeouts.current[bookId]) {
        clearTimeout(syncTimeouts.current[bookId]);
      }

      syncTimeouts.current[bookId] = setTimeout(async () => {
        const latest = pendingProgressRef.current[bookId] || payload;
        delete pendingProgressRef.current[bookId];
        delete syncTimeouts.current[bookId];
        try {
          await api.syncProgress([latest]);
        } catch (err) {
          console.error('Reading progress sync failed:', err);
        }
      }, 2000);
    },
    []
  );

  const handleAddCoins = useCallback(
    async (_amount: number, _reason: string) => {
      await reloadWalletAndShelf();
    },
    [reloadWalletAndShelf]
  );

  const markChapterUnlocked = useCallback(
    (bookId: number, chapterIndex: number) => {
      const key = `${bookId}-${chapterIndex}`;
      setUnlockedBookChapters((prev) => {
        if (prev.includes(key)) return prev;
        const next = [...prev, key];
        saveScopedUnlockedChapters(user?.id ?? null, next);
        return next;
      });
    },
    [user?.id]
  );

  const unmarkChapterUnlocked = useCallback(
    (bookId: number, chapterIndex: number) => {
      const key = `${bookId}-${chapterIndex}`;
      setUnlockedBookChapters((prev) => {
        if (!prev.includes(key)) return prev;
        const next = prev.filter((k) => k !== key);
        saveScopedUnlockedChapters(user?.id ?? null, next);
        return next;
      });
    },
    [user?.id]
  );

  const handleUnlockChapter = useCallback(
    async (bookId: number, chapterIndex: number, price: number): Promise<boolean> => {
      await api.unlockChapter(bookId, chapterIndex, price);
      await reloadWalletAndShelf();
      return true;
    },
    [reloadWalletAndShelf]
  );

  const getActiveTab = (): NavTabType | null => {
    if (location.pathname === '/shelf') return 'shelf';
    if (location.pathname === '/rewards') return 'rewards';
    if (location.pathname === '/profile') return 'profile';
    if (location.pathname === '/') return 'home';
    return null;
  };

  const activeTab = getActiveTab();
  const isFullScreenPage = ['/content', '/detail', '/recharge'].includes(location.pathname);
  const isHomePage = location.pathname === '/';

  const getMobileHeaderDetails = () => {
    switch (location.pathname) {
      case '/search':
        return { title: 'Search', showBack: true, onBack: navigateBack };
      case '/shelf':
        return { title: 'My Library', showBack: false };
      case '/rewards':
        return { title: 'Daily Rewards', showBack: false };
      case '/profile':
        return { title: 'Profile', showBack: false };
      default:
        return { title: 'StarNovel', showBack: false };
    }
  };

  const mobileHeaderInfo = getMobileHeaderDetails();

  const rawDetailNovelId = parseInt(searchParams.get('novel_id') || '0', 10);
  const detailNovelId = Number.isNaN(rawDetailNovelId) ? 0 : rawDetailNovelId;
  const rawReaderNovelId = parseInt(searchParams.get('novel_id') || '0', 10);
  const readerNovelId = Number.isNaN(rawReaderNovelId) ? 0 : rawReaderNovelId;
  const readerChapterIdxStr = searchParams.get('chapter_index');
  const parsedChapterOneBased = readerChapterIdxStr ? parseInt(readerChapterIdxStr, 10) : NaN;
  const readerChapterIndex = Number.isFinite(parsedChapterOneBased)
    ? Math.max(0, parsedChapterOneBased - 1)
    : readingProgress[readerNovelId]?.chapterIndex ?? 0;
  const searchGenre = searchParams.get('genre') || undefined;

  return (
    <>
      {!isFullScreenPage && (
        <TopNav
          activeTab={activeTab}
          onChangeTab={(tab) => navigateTo(tab)}
          onNavigate={navigateTo}
          userCoins={userCoins}
        />
      )}

      {!isFullScreenPage && !isHomePage && (
        <Header
          title={mobileHeaderInfo.title}
          showBack={mobileHeaderInfo.showBack}
          onBack={mobileHeaderInfo.onBack}
        />
      )}

      <main className={isFullScreenPage ? 'page-container-full' : isHomePage ? 'page-container-home' : 'page-container'}>
        <Routes>
          <Route
            path="/"
            element={
              <Home
                novels={novels}
                latestNovels={latestNovels}
                hasMore={hasMoreNovels}
                loadingMore={loadingMoreNovels}
                onLoadMore={handleLoadMoreNovels}
                onNavigate={navigateTo}
                userCoins={userCoins}
              />
            }
          />
          <Route
            path="/shelf"
            element={
              <Shelf
                novels={shelfNovelsList}
                loading={shelfLoading}
                onNavigate={navigateTo}
                shelfBookIds={shelfBookIds}
                onRemoveFromShelf={handleRemoveMultipleFromShelf}
                readingProgress={readingProgress}
              />
            }
          />
          <Route path="/rewards" element={<Rewards authReady={authReady} onAddCoins={handleAddCoins} />} />
          <Route
            path="/profile"
            element={
              <Profile
                shelfBookIds={shelfBookIds}
                onNavigate={navigateTo}
                userCoins={userCoins}
                chargedCoins={chargedCoins}
                bonusCoins={bonusCoins}
                isVip={isVip}
                subscription={subscription}
                transactionHistory={transactionHistory}
                hasMoreTransactions={hasMoreTransactions}
                loadingMoreTransactions={loadingMoreTransactions}
                onLoadMoreTransactions={handleLoadMoreTransactions}
                currentUser={user}
                themePreference={themePreference}
                systemTheme={systemTheme}
                onThemePreferenceChange={setThemePreference}
                onLoginSuccess={async (token, u) => {
                  localStorage.setItem('auth-token', token);
                  setUser(u);
                  setShelfLoading(true);
                  await reloadWalletAndShelf();
                  navigateTo('profile');
                }}
                onLogout={async () => {
                  await api.logout();
                  localStorage.removeItem('auth-token');
                  setUser(null);
                  setShelfBookIds([]);
                  setShelfNovelsList([]);
                  setReadingProgress({});
                  setUserCoins(0);
                  setChargedCoins(0);
                  setBonusCoins(0);
                  setIsVip(false);
                  setSubscription(null);
                  setTransactionHistory([]);
                  setHasMoreTransactions(false);
                  setUnlockedBookChapters([]);
                  setShelfLoading(true);
                  triggerGuestLogin();
                }}
              />
            }
          />
          <Route path="/search" element={<Search novels={novels} onNavigate={navigateTo} initialGenre={searchGenre} />} />
          <Route
            path="/detail"
            element={
              <Detail
                authReady={authReady}
                novelId={detailNovelId}
                novels={novels}
                onNavigate={navigateTo}
                onBack={navigateBack}
                shelfBookIds={shelfBookIds}
                onToggleShelf={handleToggleShelf}
                readingProgress={readingProgress}
              />
            }
          />
          <Route
            path="/content"
            element={
              <Reader
                userId={user?.id ?? 0}
                authReady={authReady}
                isVip={isVip}
                novelId={readerNovelId}
                chapterIndex={readerChapterIndex}
                novels={novels}
                onNavigate={navigateTo}
                onSyncChapterUrl={syncReaderChapterUrl}
                shelfBookIds={shelfBookIds}
                onToggleShelf={handleToggleShelf}
                readingProgress={readingProgress}
                onSaveProgress={handleSaveProgress}
                unlockedBookChapters={unlockedBookChapters}
                onMarkChapterUnlocked={markChapterUnlocked}
                onUnmarkChapterUnlocked={unmarkChapterUnlocked}
                onUnlockChapter={handleUnlockChapter}
                userCoins={userCoins}
              />
            }
          />
          <Route
            path="/recharge"
            element={
              <Recharge
                authReady={authReady}
                novelId={readerNovelId}
                novelTitle={
                  novels.find((book) => book.id === readerNovelId)?.title ||
                  shelfNovelsList.find((book) => book.id === readerNovelId)?.title
                }
                userCoins={userCoins}
                onAddCoins={handleAddCoins}
                onRefreshStatus={reloadWalletAndShelf}
                onBack={navigateBack}
                onNavigate={navigateTo}
              />
            }
          />
        </Routes>
      </main>

      {!isFullScreenPage && activeTab && (
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
