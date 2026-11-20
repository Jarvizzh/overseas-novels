import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { BrowserRouter, Routes, Route, Navigate, useNavigate, useLocation } from 'react-router-dom';
import { Menu } from 'lucide-react';
import { ToastProvider } from './context/ToastContext';
import { ConfirmProvider } from './context/ConfirmContext';
import { apiRequest } from './utils/api';
import type { AdminUser } from './utils/api';
import { useThemeMode } from './utils/theme';
import { ThemeSwitcher } from './components/ThemeSwitcher';
import { CmsSidebar } from './components/CmsSidebar';
import {
  CONVERSION_READ_ROLES,
  DEFAULT_EXPANDED_GROUPS,
  flattenNavEntries,
  getVisibleNavTree,
  isNavPathActive,
} from './config/navigation';
import Login from './pages/Login';
import DashboardTab from './pages/DashboardTab';
import NovelsTab from './pages/NovelsTab';
import UsersTab from './pages/UsersTab';
import OrdersTab from './pages/OrdersTab';
import PaymentTab from './pages/PaymentTab';
import AccountsTab from './pages/AccountsTab';
import PixelsTab from './pages/PixelsTab';
import TemplatesTab from './pages/TemplatesTab';
import PromotionsTab from './pages/PromotionsTab';
import ConversionCenterTab from './pages/ConversionCenterTab';
import DomainsTab from './pages/DomainsTab';
import FeedbackTab from './pages/FeedbackTab';
import OperationsTab from './pages/OperationsTab';

const SIDEBAR_COLLAPSED_KEY = 'cms_sidebar_collapsed';
const NAV_EXPANDED_GROUPS_KEY = 'cms_nav_expanded_groups';

function LegacyAdLogsRedirect() {
  const { search } = useLocation();
  return <Navigate to={`/conversions${search}`} replace />;
}

function readInitialSidebarCollapsed(): boolean {
  try {
    return localStorage.getItem(SIDEBAR_COLLAPSED_KEY) === '1';
  } catch {
    return false;
  }
}

function readInitialExpandedGroups(): Record<string, boolean> {
  try {
    const raw = localStorage.getItem(NAV_EXPANDED_GROUPS_KEY);
    if (!raw) return DEFAULT_EXPANDED_GROUPS;
    const parsed = JSON.parse(raw);
    if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
      return { ...DEFAULT_EXPANDED_GROUPS, ...parsed };
    }
  } catch {
    // Fallback to all groups expanded by default
  }
  return DEFAULT_EXPANDED_GROUPS;
}

declare global {
  interface Window {
    showToast?: (message: string, type?: 'success' | 'error' | 'info') => void;
    showConfirm?: (message: string, onConfirm: () => void) => void;
  }
}

function AppContent() {
  const [token, setToken] = useState<string | null>(() => localStorage.getItem('cms_token'));
  const [admin, setAdmin] = useState<AdminUser | null>(() => {
    try {
      const saved = localStorage.getItem('cms_admin');
      return saved ? JSON.parse(saved) : null;
    } catch {
      return null;
    }
  });
  const [initializing, setInitializing] = useState(true);
  const { mode, resolvedTheme, setMode } = useThemeMode();

  const [sidebarCollapsed, setSidebarCollapsed] = useState<boolean>(readInitialSidebarCollapsed);
  const [expandedGroups, setExpandedGroups] = useState<Record<string, boolean>>(readInitialExpandedGroups);
  const [mobileNavOpen, setMobileNavOpen] = useState(false);

  const navigate = useNavigate();
  const location = useLocation();

  const handleLogout = useCallback(() => {
    localStorage.removeItem('cms_token');
    localStorage.removeItem('cms_admin');
    setMobileNavOpen(false);
    setToken(null);
    setAdmin(null);
    navigate('/login', { replace: true });
  }, [navigate]);

  const handleLogoutRef = useRef(handleLogout);
  useEffect(() => {
    handleLogoutRef.current = handleLogout;
  }, [handleLogout]);

  useEffect(() => {
    let active = true;
    const verifySession = async () => {
      if (token) {
        try {
          const profile = await apiRequest('GET', '/auth/me');
          if (!active) return;
          setAdmin(profile);
          localStorage.setItem('cms_admin', JSON.stringify(profile));
        } catch {
          if (active) {
            handleLogoutRef.current();
          }
        }
      }
      if (active) {
        setInitializing(false);
      }
    };
    void verifySession();
    return () => {
      active = false;
    };
  }, [token]);

  const visibleNavTree = useMemo(
    () => (admin ? getVisibleNavTree(admin.role) : []),
    [admin],
  );
  const visibleFlatItems = useMemo(() => flattenNavEntries(visibleNavTree), [visibleNavTree]);
  const allFlatItems = useMemo(() => flattenNavEntries(getVisibleNavTree('SuperAdmin')), []);

  const currentNavItem = useMemo(
    () =>
      visibleFlatItems.find((item) => isNavPathActive(location.pathname, item.path)) ||
      allFlatItems.find((item) => isNavPathActive(location.pathname, item.path)) ||
      visibleFlatItems[0] ||
      allFlatItems[0],
    [location.pathname, visibleFlatItems, allFlatItems],
  );

  // Auto-expand the active item's parent group and close mobile drawer on route change
  useEffect(() => {
    setMobileNavOpen(false);
    const activeGroupId = currentNavItem?.groupId;
    if (activeGroupId) {
      setExpandedGroups((prev) => {
        if (prev[activeGroupId]) return prev;
        const next = { ...prev, [activeGroupId]: true };
        try {
          localStorage.setItem(NAV_EXPANDED_GROUPS_KEY, JSON.stringify(next));
        } catch {
          // Ignore storage write errors
        }
        return next;
      });
    }
  }, [location.pathname, currentNavItem?.groupId]);

  // Update browser tab title cleanly
  useEffect(() => {
    if (location.pathname === '/login') {
      document.title = '登录 - STAR NOVEL CMS';
      return;
    }
    const found = allFlatItems.find((item) => isNavPathActive(location.pathname, item.path));
    document.title = found ? `${found.label} - STAR NOVEL CMS` : 'STAR NOVEL CMS';
  }, [location.pathname, allFlatItems]);

  // Auth route guard redirects
  useEffect(() => {
    if (!initializing) {
      if (!token || !admin) {
        if (location.pathname !== '/login') {
          navigate('/login', { replace: true });
        }
      } else if (location.pathname === '/login') {
        navigate('/', { replace: true });
      }
    }
  }, [token, admin, initializing, location.pathname, navigate]);

  const handleToggleCollapse = useCallback(() => {
    setSidebarCollapsed((prev) => {
      const next = !prev;
      try {
        localStorage.setItem(SIDEBAR_COLLAPSED_KEY, next ? '1' : '0');
      } catch {
        // Ignore storage write errors
      }
      return next;
    });
  }, []);

  const handleToggleGroup = useCallback((groupId: string) => {
    setExpandedGroups((prev) => {
      const next = { ...prev, [groupId]: !(prev[groupId] ?? true) };
      try {
        localStorage.setItem(NAV_EXPANDED_GROUPS_KEY, JSON.stringify(next));
      } catch {
        // Ignore storage write errors
      }
      return next;
    });
  }, []);

  const handleCloseMobile = useCallback(() => {
    setMobileNavOpen(false);
  }, []);

  const handleLoginSuccess = (newToken: string, newAdmin: AdminUser) => {
    localStorage.setItem('cms_token', newToken);
    localStorage.setItem('cms_admin', JSON.stringify(newAdmin));
    setToken(newToken);
    setAdmin(newAdmin);
    navigate('/', { replace: true });
  };

  if (initializing) {
    return (
      <div className="min-h-screen flex items-center justify-center bg-[var(--cms-bg)] text-sm text-[var(--cms-muted-fg)]">
        正在初始化控制台会话...
      </div>
    );
  }

  if (!token || !admin) {
    return (
      <Routes>
        <Route path="/login" element={<Login onLoginSuccess={handleLoginSuccess} />} />
        <Route path="*" element={<Navigate to="/login" replace />} />
      </Routes>
    );
  }

  const routeSubtitle =
    location.pathname === '/recharge-templates/new'
      ? ' · 新建'
      : /^\/recharge-templates\/\d+\/edit$/.test(location.pathname)
        ? ` · 编辑 #${location.pathname.split('/')[2]}`
        : '';

  return (
    <div className="min-h-screen flex bg-[var(--cms-bg)] text-[var(--cms-fg)]">
      <CmsSidebar
        navTree={visibleNavTree}
        admin={admin}
        resolvedTheme={resolvedTheme}
        collapsed={sidebarCollapsed}
        onToggleCollapse={handleToggleCollapse}
        expandedGroups={expandedGroups}
        onToggleGroup={handleToggleGroup}
        mobileOpen={mobileNavOpen}
        onCloseMobile={handleCloseMobile}
        onLogout={handleLogout}
      />

      {/* Main Workspace */}
      <div className="flex-1 min-w-0 flex flex-col">
        {/* Compact Top Header */}
        <header className="h-12 px-4 md:px-6 flex items-center justify-between gap-4 border-b border-[var(--cms-border)] bg-[var(--cms-bg)]/90 backdrop-blur-sm sticky top-0 z-10 shrink-0">
          <div className="flex items-center gap-2.5 min-w-0">
            <button
              type="button"
              onClick={() => setMobileNavOpen(true)}
              aria-label="打开导航菜单"
              aria-expanded={mobileNavOpen}
              aria-controls="mobile-nav-drawer"
              className="md:hidden focus-ring w-8 h-8 rounded-lg border border-[var(--cms-border)] bg-[var(--cms-card)] hover:bg-[var(--cms-secondary)] text-[var(--cms-fg)] inline-flex items-center justify-center cursor-pointer transition-colors shrink-0"
            >
              <Menu size={16} aria-hidden="true" />
            </button>

            {/* Breadcrumb */}
            <nav aria-label="当前位置" className="flex items-center gap-2 text-sm text-[var(--cms-muted-fg)] min-w-0">
              {currentNavItem.groupLabel && (
                <>
                  <span className="hidden sm:inline truncate">{currentNavItem.groupLabel}</span>
                  <span className="hidden sm:inline opacity-50" aria-hidden="true">
                    /
                  </span>
                </>
              )}
              <h1 className="m-0 text-[15px] sm:text-base font-semibold tracking-tight text-[var(--cms-fg)] truncate">
                {currentNavItem.label}
                {routeSubtitle}
              </h1>
            </nav>
          </div>

          {/* Right Controls: Icon-Only Theme Switcher */}
          <div className="flex items-center gap-2 shrink-0">
            <ThemeSwitcher mode={mode} resolvedTheme={resolvedTheme} onChange={setMode} />
          </div>
        </header>

        {/* Adaptive Full-Width Page Container */}
        <main className="flex-1 w-full px-4 md:px-6 py-4">
          <Routes>
            <Route path="/" element={<DashboardTab />} />
            <Route path="/novels" element={<NovelsTab />} />
            <Route path="/users" element={<UsersTab />} />
            <Route path="/orders" element={<OrdersTab />} />
            <Route path="/feedback" element={<FeedbackTab />} />
            <Route path="/promotions" element={<PromotionsTab />} />
            <Route path="/recharge-templates" element={<TemplatesTab />} />
            <Route path="/recharge-templates/new" element={<TemplatesTab />} />
            <Route path="/recharge-templates/:templateId/edit" element={<TemplatesTab />} />
            <Route path="/operations" element={<OperationsTab />} />
            <Route path="/campaign" element={<Navigate to="/operations?tab=campaigns" replace />} />
            <Route path="/pixels" element={CONVERSION_READ_ROLES.includes(admin.role) ? <PixelsTab /> : <Navigate to="/" replace />} />
            <Route
              path="/conversions"
              element={
                CONVERSION_READ_ROLES.includes(admin.role)
                  ? <ConversionCenterTab canWrite={admin.role === 'SuperAdmin' || admin.role === 'Admin'} />
                  : <Navigate to="/" replace />
              }
            />
            <Route path="/ad-config/logs" element={<LegacyAdLogsRedirect />} />
            <Route path="/ad-config" element={<Navigate to="/pixels" replace />} />
            <Route
              path="/domains"
              element={admin.role === 'SuperAdmin' ? <DomainsTab /> : <Navigate to="/" replace />}
            />
            <Route
              path="/payment"
              element={admin.role === 'SuperAdmin' ? <PaymentTab /> : <Navigate to="/" replace />}
            />
            <Route
              path="/accounts"
              element={admin.role === 'SuperAdmin' ? <AccountsTab /> : <Navigate to="/" replace />}
            />
            <Route
              path="/settings"
              element={admin.role === 'SuperAdmin' ? <Navigate to="/domains" replace /> : <Navigate to="/" replace />}
            />
            <Route path="*" element={<Navigate to="/" replace />} />
          </Routes>
        </main>
      </div>
    </div>
  );
}

export default function App() {
  return (
    <BrowserRouter>
      <ToastProvider>
        <ConfirmProvider>
          <AppContent />
        </ConfirmProvider>
      </ToastProvider>
    </BrowserRouter>
  );
}
