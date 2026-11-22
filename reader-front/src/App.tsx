import { readerStorage } from './utils/readerStorage';
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
import type { BookshelfItem, Novel, User, Transaction, SubscriptionInfo, SavedProgress } from './utils/api';
import { useProgressOutbox } from './utils/useProgressOutbox';
import { useShelf } from './utils/useShelf';
import { withRequestTimeout } from './utils/requestTimeout';
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
    const raw = readerStorage.getItem(`unlocked-chapter-ids-u:${userId}`);
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
    readerStorage.setItem(`unlocked-chapter-ids-u:${userId}`, JSON.stringify(list));
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

  const [session, setSession] = useState<{ user: User; token: string } | null>(null);
  const user = session?.user ?? null;
  const [authReady, setAuthReady] = useState(false);

  const [novels, setNovels] = useState<Novel[]>([]);
  const [latestNovels, setLatestNovels] = useState<Novel[]>([]);
  const [featuredNovels, setFeaturedNovels] = useState<Novel[]>([]);
  const [curatedNovels, setCuratedNovels] = useState<Novel[]>([]);
  const [rankedNovels, setRankedNovels] = useState<Novel[]>([]);
  const [recentBooks, setRecentBooks] = useState<BookshelfItem[]>([]);
  const [recentLoading, setRecentLoading] = useState(false);
  const [recentRefreshTick, setRecentRefreshTick] = useState(0);
  const [novelsPage, setNovelsPage] = useState(1);
  const [hasMoreNovels, setHasMoreNovels] = useState(false);
  const [loadingMoreNovels, setLoadingMoreNovels] = useState(false);

  type ReadingPosition = {
    chapterIndex: number;
    scrollOffsetPercentage: number;
    paragraphIndex?: number;
    updatedAt: number;
    version: string;
  };
  const [positions, setPositions] = useState<Record<number, Record<number, ReadingPosition>>>({});
  const [restoredEntry, setRestoredEntry] = useState('');
  const [progressRestoreError, setProgressRestoreError] = useState(false);
  const readingProgress = user ? positions[user.id] ?? {} : {};
  const handleProgressSynced = useCallback(() => {
    setRecentRefreshTick((n) => n + 1);
  }, []);
  const { enqueue, pending } = useProgressOutbox(user?.id, session?.token, handleProgressSynced);
  const mergePosition = useCallback((userId: number, entry: SavedProgress, paragraphIndex?: number) => {
    const position = {
      chapterIndex: entry.chapter_index,
      scrollOffsetPercentage: entry.scroll_offset_percentage,
      paragraphIndex,
      updatedAt: Date.parse(entry.updated_at),
      version: entry.version ?? '',
    };
    setPositions((previous) => {
      const owner = previous[userId] ?? {};
      const current = owner[entry.novel_id];
      if (current && (current.updatedAt > position.updatedAt ||
          (current.updatedAt === position.updatedAt && current.version >= position.version))) {
        return previous;
      }
      return { ...previous, [userId]: { ...owner, [entry.novel_id]: position } };
    });
  }, []);

  const shelf = useShelf(user?.id, session?.token, location.pathname === '/shelf', mergePosition);

  const [themePreference, setThemePreference] = useState<ThemePreference>(getStoredThemePreference);
  const [systemTheme, setSystemTheme] = useState<'light' | 'dark'>(getSystemTheme);

  const [userCoins, setUserCoins] = useState<number>(0);
  const [chargedCoins, setChargedCoins] = useState<number>(0);
  const [bonusCoins, setBonusCoins] = useState<number>(0);
  const [isVip, setIsVip] = useState<boolean>(false);
  const [subscription, setSubscription] = useState<SubscriptionInfo | null>(null);

  const [unlockedBookChapters, setUnlockedBookChapters] = useState<string[]>([]);
  const [transactionHistory, setTransactionHistory] = useState<Transaction[]>([]);
  const [nextTransactionCursor, setNextTransactionCursor] = useState<string>();
  const [loadingMoreTransactions, setLoadingMoreTransactions] = useState(false);
  const transactionRequest = useRef<AbortController | null>(null);

  const guestLoginPromiseRef = useRef<Promise<string | null> | null>(null);

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

  const reloadWallet = useCallback(async () => {
    transactionRequest.current?.abort();
    const controller = new AbortController();
    transactionRequest.current = controller;
    setLoadingMoreTransactions(false);
    try {
      const [balance, txs, subStatus] = await Promise.all([
        api.getWalletBalance(),
        api.getTransactionHistory(TX_PAGE_SIZE, undefined, controller.signal),
        api.getSubscriptionStatus().catch(() => null),
      ]);
      if (controller.signal.aborted) return;

      setChargedCoins(balance.charged_coins);
      setBonusCoins(balance.bonus_coins);
      setUserCoins(balance.charged_coins + balance.bonus_coins);

      setTransactionHistory(txs.items);
      setNextTransactionCursor(txs.next_cursor);

      if (subStatus) {
        setIsVip(Boolean(subStatus.is_vip));
        setSubscription(subStatus.subscription);
      }
    } catch (err) {
      if (!controller.signal.aborted) console.error('Failed to load wallet info:', err);
    } finally {
      if (transactionRequest.current === controller) {
        transactionRequest.current = null;
      }
    }
  }, []);

  const handleLoadMoreTransactions = useCallback(async () => {
    if (!nextTransactionCursor || transactionRequest.current) return;
    const controller = new AbortController();
    transactionRequest.current = controller;
    setLoadingMoreTransactions(true);
    try {
      const page = await api.getTransactionHistory(TX_PAGE_SIZE, nextTransactionCursor, controller.signal);
      if (controller.signal.aborted) return;
      setTransactionHistory((prev) => [...prev, ...page.items]);
      setNextTransactionCursor(page.next_cursor);
    } catch (err) {
      if (!controller.signal.aborted) console.error('Failed to load more transactions:', err);
    } finally {
      if (transactionRequest.current === controller) {
        transactionRequest.current = null;
        setLoadingMoreTransactions(false);
      }
    }
  }, [nextTransactionCursor]);

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
              !readerStorage.getItem('auth-token')
            ) {
              res = await api.guestLogin();
            } else {
              throw error;
            }
          }
          readerStorage.setItem('auth-token', res.token);
          setSession({ user: res.user, token: res.token });
          await reloadWallet();
          return res.token;
        } catch (err) {
          console.error('Guest login failed:', err);
          return null;
        } finally {
          guestLoginPromiseRef.current = null;
        }
      })();
    }
    return guestLoginPromiseRef.current;
  }, [reloadWallet]);

  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    const utmSource = params.get('utm_source');
    const utmCampaign = params.get('utm_campaign');
    const fbclid = params.get('fbclid');
    const pixelId = params.get('pixel_id');
    const templateId = params.get('template_id');
    const linkId = params.get('link_id') || params.get('promo_id');

    if (utmSource) readerStorage.setItem('utm_source', utmSource);
    if (utmCampaign) readerStorage.setItem('utm_campaign', utmCampaign);
    if (pixelId) readerStorage.setItem('fb_pixel_id', pixelId);
    if (templateId) readerStorage.setItem('recharge_template_id', templateId);
    if (linkId) readerStorage.setItem('promo_id', linkId);
    if (fbclid) {
      readerStorage.setItem('fbclid', fbclid);
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

    let authenticating = false;
    let disposed = false;
    const initAuth = async () => {
      if (authenticating) return;
      authenticating = true;
      try {
        await restoreSession();
      } finally {
        authenticating = false;
        if (!disposed) setAuthReady(true);
      }
    };

    const restoreSession = async () => {
      const tokenBefore = readerStorage.getItem('auth-token');
      if (!tokenBefore) {
        await triggerGuestLogin();
      } else {
        try {
          const u = await api.getProfile();
          if (!disposed && readerStorage.getItem('auth-token') === tokenBefore) {
            setSession({ user: u, token: tokenBefore });
            await reloadWallet();
          }
        } catch (err) {
          console.error('Auth init failed, fallback to guest:', err);
          await triggerGuestLogin();
        }
      }
    };

    const loadNovels = async () => {
      try {
        const [popularList, newestList, opsCatalog] = await Promise.all([
          api.getNovels({ sort: 'popular', page: 1, limit: 50 }),
          api.getNovels({ sort: 'latest', page: 1, limit: 12 }),
          api.getOperationsCatalog().catch(() => null),
        ]);
        if (popularList) {
          setNovels(popularList);
          setNovelsPage(1);
          setHasMoreNovels(popularList.length >= 50);
        }
        if (newestList) {
          setLatestNovels(newestList);
        }
        if (opsCatalog) {
          setFeaturedNovels(opsCatalog.featured || []);
          setCuratedNovels(opsCatalog.curated || []);
          setRankedNovels(opsCatalog.rankings || []);
        }
      } catch (err) {
        console.error('Failed to load novels catalog:', err);
      }
    };

    void initAuth();
    void loadNovels();
    window.addEventListener('online', initAuth);

    return () => {
      disposed = true;
      window.removeEventListener('online', initAuth);
      registerSessionRefreshHandler(null);
    };
  }, [triggerGuestLogin, reloadWallet]);

  useEffect(() => {
    let request: AbortController | undefined;
    const onAccountChange = (event: StorageEvent) => {
      if (event.key !== 'auth-token') return;
      request?.abort();
      if (!event.newValue) {
        transactionRequest.current?.abort();
        setSession(null);
        setRecentBooks([]);
        setUserCoins(0);
        setChargedCoins(0);
        setBonusCoins(0);
        setIsVip(false);
        setSubscription(null);
        setTransactionHistory([]);
        setNextTransactionCursor(undefined);
        setLoadingMoreTransactions(false);
        return;
      }
      const token = event.newValue;
      const controller = new AbortController();
      request = controller;
      void api.getProfile(controller.signal, token).then((profile) => {
        if (controller.signal.aborted || readerStorage.getItem('auth-token') !== token) return;
        setSession({ user: profile, token });
        void reloadWallet();
      }).catch(() => {});
    };
    window.addEventListener('storage', onAccountChange);
    return () => {
      request?.abort();
      window.removeEventListener('storage', onAccountChange);
    };
  }, [reloadWallet]);

  useEffect(() => {
    if (location.pathname === '/content' || location.pathname === '/detail') {
      readerStorage.setItem('last_read_content_path', `${location.pathname}${location.search}`);
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
        if (typeof params?.tab === 'string' && params.tab) {
          mergedParams.set('tab', params.tab);
        }
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

  const handleSaveProgress = useCallback(
    (bookId: number, chapterIndex: number, scrollOffsetPercentage: number, paragraphIndex?: number) => {
      if (!user?.id) return;
      const update = enqueue({
        novel_id: bookId,
        chapter_index: chapterIndex,
        scroll_offset_percentage: scrollOffsetPercentage,
      });
      if (update) {
        mergePosition(user.id, update, paragraphIndex);
        setRecentBooks((prev) => {
          const existing = prev.find((item) => item.novel_id === bookId && item.user_id === user.id);
          const knownNovel =
            existing?.novel ||
            shelf.novels.find((n) => n.id === bookId) ||
            novels.find((n) => n.id === bookId) ||
            latestNovels.find((n) => n.id === bookId) ||
            featuredNovels.find((n) => n.id === bookId) ||
            curatedNovels.find((n) => n.id === bookId) ||
            rankedNovels.find((n) => n.id === bookId);
          if (!knownNovel) return prev;
          const nextItem: BookshelfItem = {
            user_id: user.id,
            novel_id: bookId,
            chapter_index: update.chapter_index,
            scroll_offset_percentage: update.scroll_offset_percentage,
            in_shelf: existing ? existing.in_shelf : shelf.ids.includes(bookId),
            progress_version: update.version ?? '',
            updated_at: update.updated_at,
            novel: knownNovel,
          };
          return [nextItem, ...prev.filter((item) => item.user_id === user.id && item.novel_id !== bookId)].slice(0, 20);
        });
      }
    },
    [curatedNovels, enqueue, featuredNovels, latestNovels, mergePosition, novels, rankedNovels, shelf.ids, shelf.novels, user?.id],
  );

  const handleAddCoins = useCallback(
    async (_amount: number, _reason: string) => {
      await reloadWallet();
    },
    [reloadWallet]
  );

  const markChapterUnlocked = useCallback(
    (chapterId: string) => {
      const key = chapterId;
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
    (chapterId: string) => {
      const key = chapterId;
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
    async (bookId: number, chapterIndex: number, price: number, chapterId: string, version: number): Promise<boolean> => {
      if (!session || readerStorage.getItem('auth-token') !== session.token) return false;
      await api.unlockChapter(bookId, chapterIndex, price, chapterId, version, session.user.id, session.token);
      await reloadWallet();
      return true;
    },
    [reloadWallet, session]
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
  const entryNovelId = location.pathname === '/content' || location.pathname === '/detail' ? readerNovelId : 0;
  const ownerId = user?.id;
  const sessionToken = session?.token;
  const progressEntry = ownerId && entryNovelId > 0 ? `${ownerId}:${entryNovelId}` : '';

  useEffect(() => {
    if (!ownerId || !sessionToken) {
      setRecentBooks([]);
      setRecentLoading(false);
      return;
    }
    setRecentBooks((prev) => prev.filter((item) => item.user_id === ownerId));
    if (location.pathname !== '/shelf' && location.pathname !== '/profile') {
      return;
    }
    const controller = new AbortController();
    setRecentLoading(true);
    void api
      .getRecentBooks(ownerId, 20, { authToken: sessionToken, signal: controller.signal })
      .then((items) => {
        if (controller.signal.aborted || readerStorage.getItem('auth-token') !== sessionToken) return;
        const list = items || [];
        setRecentBooks((prev) => {
          const mergedByNovel = new Map<number, BookshelfItem>();
          for (const item of list) {
            mergedByNovel.set(item.novel_id, item);
          }
          for (const local of prev) {
            if (local.user_id !== ownerId) continue;
            const remote = mergedByNovel.get(local.novel_id);
            const localTime = Date.parse(local.updated_at);
            const remoteTime = remote ? Date.parse(remote.updated_at) : -1;
            if (!remote || localTime > remoteTime ||
                (localTime === remoteTime && local.progress_version > remote.progress_version)) {
              mergedByNovel.set(local.novel_id, local);
            }
          }
          return [...mergedByNovel.values()]
            .sort((a, b) => Date.parse(b.updated_at) - Date.parse(a.updated_at) || b.novel_id - a.novel_id)
            .slice(0, 20);
        });
        for (const item of list) {
          mergePosition(ownerId, {
            novel_id: item.novel_id,
            chapter_index: item.chapter_index,
            scroll_offset_percentage: item.scroll_offset_percentage,
            updated_at: item.updated_at,
            version: item.progress_version,
          });
        }
      })
      .catch((err) => {
        if (!controller.signal.aborted) {
          console.error('Failed to load recently read books:', err);
        }
      })
      .finally(() => {
        if (!controller.signal.aborted) setRecentLoading(false);
      });
    return () => controller.abort();
  }, [location.pathname, mergePosition, ownerId, recentRefreshTick, sessionToken]);

  useEffect(() => {
    const owner = ownerId ?? 0;
    if (!progressEntry || !owner) {
      setRestoredEntry('');
      return;
    }
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout> | undefined;
    let failures = 0;
    let running = false;
    setRestoredEntry('');
    setProgressRestoreError(false);

    async function restore() {
      if (running || controller.signal.aborted) return;
      running = true;
      try {
        // Read local state before the server so acknowledgements cannot hide a
        // newer local update behind an older remote snapshot.
        const cached = await pending(owner);
        if (controller.signal.aborted) return;
        const local = cached.entries.find((entry) => entry.novel_id === entryNovelId);
        if (local) mergePosition(owner, local);
        if (cached.storageFailed) throw new Error('Local reading progress is temporarily unavailable');

        const remote = await withRequestTimeout(controller.signal, 4_000, (signal) =>
          api.getProgress(entryNovelId, owner, signal, sessionToken),
        ).catch(() => null);
        if (controller.signal.aborted) return;
        if (remote) mergePosition(owner, remote);
        setProgressRestoreError(false);
        setRestoredEntry(progressEntry);
      } catch {
        if (controller.signal.aborted) return;
        setProgressRestoreError(true);
        failures = Math.min(failures + 1, 6);
        timer = setTimeout(() => {
          timer = undefined;
          void restore();
        }, Math.min(2_000 * 2 ** (failures - 1), 60_000));
      } finally {
        running = false;
      }
    }

    function wakeRestore() {
      if (document.visibilityState === 'hidden') return;
      if (timer !== undefined) clearTimeout(timer);
      timer = undefined;
      void restore();
    }
    void restore();
    window.addEventListener('online', wakeRestore);
    document.addEventListener('visibilitychange', wakeRestore);
    return () => {
      controller.abort();
      if (timer !== undefined) clearTimeout(timer);
      window.removeEventListener('online', wakeRestore);
      document.removeEventListener('visibilitychange', wakeRestore);
    };
  }, [entryNovelId, mergePosition, ownerId, pending, progressEntry, sessionToken]);
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
        {progressRestoreError && progressEntry && restoredEntry !== progressEntry && (
          <p role="status">Reading progress is temporarily unavailable. Retrying…</p>
        )}
        <Routes>
          <Route
            path="/"
            element={
              <Home
                novels={novels}
                latestNovels={latestNovels}
                featuredNovels={featuredNovels}
                curatedNovels={curatedNovels}
                rankedNovels={rankedNovels}
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
                key={user?.id}
                novels={shelf.novels}
                recentBooks={recentBooks}
                recentLoading={recentLoading}
                initialTab={searchParams.get('tab') === 'recent' ? 'recent' : 'shelf'}
                loading={!authReady || shelf.initialLoading}
                loadingMore={shelf.loadingMore}
                hasMore={shelf.hasMore}
                pageFailed={shelf.pageFailed}
                editing={shelf.editing}
                onLoadMore={shelf.loadMore}
                onNavigate={navigateTo}
                onRemoveFromShelf={shelf.remove}
                readingProgress={readingProgress}
              />
            }
          />
          <Route path="/rewards" element={<Rewards authReady={authReady} onAddCoins={handleAddCoins} />} />
          <Route
            path="/profile"
            element={
              <Profile
                shelfBookIds={shelf.ids}
                recentBookCount={recentBooks.length}
                onNavigate={navigateTo}
                userCoins={userCoins}
                chargedCoins={chargedCoins}
                bonusCoins={bonusCoins}
                isVip={isVip}
                subscription={subscription}
                transactionHistory={transactionHistory}
                hasMoreTransactions={Boolean(nextTransactionCursor)}
                loadingMoreTransactions={loadingMoreTransactions}
                onLoadMoreTransactions={handleLoadMoreTransactions}
                currentUser={user}
                themePreference={themePreference}
                systemTheme={systemTheme}
                onThemePreferenceChange={setThemePreference}
                onLoginSuccess={async (token, u) => {
                  setTransactionHistory([]);
                  setNextTransactionCursor(undefined);
                  readerStorage.setItem('auth-token', token);
                  setSession({ user: u, token });
                  await reloadWallet();
                  navigateTo('profile');
                }}
                onProfileUpdated={(updatedUser, nextToken) => {
                  if (nextToken) {
                    readerStorage.setItem('auth-token', nextToken);
                    setSession({ user: updatedUser, token: nextToken });
                  } else {
                    setSession((prev) => (prev ? { ...prev, user: updatedUser } : prev));
                  }
                }}
                onLogout={async () => {
                  await api.logout();
                  transactionRequest.current?.abort();
                  transactionRequest.current = null;
                  readerStorage.removeItem('auth-token');
                  setSession(null);
                  setRecentBooks([]);
                  setPositions({});
                  setUserCoins(0);
                  setChargedCoins(0);
                  setBonusCoins(0);
                  setIsVip(false);
                  setSubscription(null);
                  setTransactionHistory([]);
                  setNextTransactionCursor(undefined);
                  setLoadingMoreTransactions(false);
                  setUnlockedBookChapters([]);
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
                shelfBookIds={shelf.ids}
                onToggleShelf={shelf.toggle}
                readingProgress={readingProgress}
              />
            }
          />
          <Route
            path="/content"
            element={
              <Reader
                key={`${user?.id ?? 0}:${readerNovelId}`}
                userId={user?.id ?? 0}
                authReady={authReady && (!progressEntry || restoredEntry === progressEntry)}
                isVip={isVip}
                novelId={readerNovelId}
                chapterIndex={readerChapterIndex}
                novels={novels}
                onNavigate={navigateTo}
                onSyncChapterUrl={syncReaderChapterUrl}
                shelfBookIds={shelf.ids}
                onToggleShelf={shelf.toggle}
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
                key={user?.id}
                authReady={authReady}
                userId={user?.id}
                novelId={readerNovelId}
                novelTitle={
                  novels.find((book) => book.id === readerNovelId)?.title ||
                  shelf.novels.find((book) => book.id === readerNovelId)?.title
                }
                userCoins={userCoins}
                onRefreshStatus={reloadWallet}
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
