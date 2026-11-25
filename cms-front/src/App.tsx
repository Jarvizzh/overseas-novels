import { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { BrowserRouter, Routes, Route, Navigate, Outlet, useNavigate, useLocation } from 'react-router-dom';
import { Menu, ShieldAlert } from 'lucide-react';
import { ToastProvider } from './context/ToastContext';
import { ConfirmProvider } from './context/ConfirmContext';
import { PermissionProvider, usePermission } from './context/PermissionContext';
import { authApi } from './utils/api';
import type { AdminUser } from './utils/api';
import { useThemeMode } from './utils/theme';
import { ThemeSwitcher } from './components/ThemeSwitcher';
import { HeaderAccountMenu } from './components/HeaderAccountMenu';
import { CmsSidebar } from './components/CmsSidebar';
import {
  DEFAULT_EXPANDED_GROUPS,
  NAV_TREE,
  flattenNavEntries,
  getVisibleNavTree,
  isNavPathActive,
} from './config/navigation';
import Login from './pages/Login';
import DashboardTab from './pages/DashboardTab';
import NovelsTab from './pages/NovelsTab';
import UsersTab from './pages/UsersTab';
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
const PaymentCases = lazy(() => import('./pages/PaymentCases'));
const PaymentReconciliation = lazy(() => import('./pages/PaymentReconciliation'));
const OrdersTab = lazy(() => import('./pages/OrdersTab'));
const PaymentSubscriptions = lazy(() => import('./pages/PaymentCenter').then((page) => ({ default: page.PaymentSubscriptions })));
const PaymentChannels = lazy(() => import('./pages/PaymentCenter').then((page) => ({ default: page.PaymentChannels })));
const PaymentRefunds = lazy(() => import('./pages/PaymentRefunds').then((page) => ({ default: page.PaymentRefunds })));
const ProductsPage = lazy(() => import('./pages/ProductsPage'));

function LegacyAdLogsRedirect() {
  const { search } = useLocation();
  return <Navigate to={`/conversions${search}`} replace />;
}

function NoPermissionState() {
  return (
    <div className="min-h-[60vh] flex items-center justify-center p-6">
      <div className="cms-card max-w-md w-full p-8 text-center space-y-3">
        <div className="w-11 h-11 rounded-2xl mx-auto flex items-center justify-center bg-[var(--cms-secondary)] text-[var(--cms-muted-fg)]">
          <ShieldAlert size={20} aria-hidden="true" />
        </div>
        <h2 className="text-base font-semibold text-[var(--cms-fg)] m-0">暂无访问权限</h2>
        <p className="text-xs text-[var(--cms-muted-fg)] leading-relaxed m-0">
          暂无访问权限，请联系管理员分配角色
        </p>
      </div>
    </div>
  );
}

interface PermissionRouteProps {
  anyOf?: readonly string[];
  allOf?: readonly string[];
  fallbackPath: string | null;
  children: ReactNode;
}

function PermissionRoute({ anyOf, allOf, fallbackPath, children }: PermissionRouteProps) {
  const { hasAnyPermission, hasAllPermissions } = usePermission();
  const allowed =
    (!anyOf || hasAnyPermission(anyOf)) &&
    (!allOf || hasAllPermissions(allOf));

  if (!allowed) {
    if (!fallbackPath) {
      return <NoPermissionState />;
    }
    return <Navigate to={fallbackPath} replace />;
  }

  return <>{children}</>;
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

interface AuthenticatedWorkspaceProps {
  admin: AdminUser;
  onLogout: () => void;
}

function AuthenticatedWorkspace({ admin, onLogout }: AuthenticatedWorkspaceProps) {
  const { mode, resolvedTheme, setMode } = useThemeMode();
  const { hasAnyPermission, canRead } = usePermission();

  const [sidebarCollapsed, setSidebarCollapsed] = useState<boolean>(readInitialSidebarCollapsed);
  const [expandedGroups, setExpandedGroups] = useState<Record<string, boolean>>(readInitialExpandedGroups);
  const [mobileNavOpen, setMobileNavOpen] = useState(false);

  const location = useLocation();

  const visibleNavTree = useMemo(
    () => getVisibleNavTree(hasAnyPermission),
    [hasAnyPermission],
  );
  const visibleFlatItems = useMemo(() => flattenNavEntries(visibleNavTree), [visibleNavTree]);
  const allFlatItems = useMemo(() => flattenNavEntries(NAV_TREE), []);
  const firstVisiblePath = visibleFlatItems[0]?.path ?? null;

  const firstPaymentPath = useMemo(() => {
    const paymentNode = visibleNavTree.find((node) => node.id === 'payments');
    return paymentNode?.children?.[0]?.path ?? firstVisiblePath;
  }, [visibleNavTree, firstVisiblePath]);

  const currentNavItem = useMemo(
    () =>
      visibleFlatItems.find((item) => isNavPathActive(location.pathname, item.path)) ||
      allFlatItems.find((item) => isNavPathActive(location.pathname, item.path)) ||
      visibleFlatItems[0],
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
    const found = allFlatItems.find((item) => isNavPathActive(location.pathname, item.path));
    document.title = found ? `${found.label} - STAR NOVEL CMS` : 'STAR NOVEL CMS';
  }, [location.pathname, allFlatItems]);

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

  const routeSubtitle =
    location.pathname === '/recharge-templates/new'
      ? ' · 新建'
      : /^\/recharge-templates\/\d+\/edit$/.test(location.pathname)
        ? ` · 编辑 #${location.pathname.split('/')[2]}`
        : '';

  const templateWriteFallback = canRead('ads.templates') ? '/recharge-templates' : firstVisiblePath;

  return (
    <div className="min-h-screen flex bg-[var(--cms-bg)] text-[var(--cms-fg)]">
      <CmsSidebar
        navTree={visibleNavTree}
        homePath={firstVisiblePath || '/'}
        resolvedTheme={resolvedTheme}
        collapsed={sidebarCollapsed}
        onToggleCollapse={handleToggleCollapse}
        expandedGroups={expandedGroups}
        onToggleGroup={handleToggleGroup}
        mobileOpen={mobileNavOpen}
        onCloseMobile={handleCloseMobile}
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
              {currentNavItem?.groupLabel && (
                <>
                  <span className="hidden sm:inline truncate">{currentNavItem.groupLabel}</span>
                  <span className="hidden sm:inline opacity-50" aria-hidden="true">
                    /
                  </span>
                </>
              )}
              <h1 className="m-0 text-[15px] sm:text-base font-semibold tracking-tight text-[var(--cms-fg)] truncate">
                {currentNavItem ? currentNavItem.label : 'STAR NOVEL CMS'}
                {routeSubtitle}
              </h1>
            </nav>
          </div>

          {/* Right Controls: Theme Switcher & Account Menu */}
          <div className="flex items-center gap-2 shrink-0">
            <ThemeSwitcher mode={mode} resolvedTheme={resolvedTheme} onChange={setMode} />
            <HeaderAccountMenu admin={admin} onLogout={onLogout} />
          </div>
        </header>

        {/* Adaptive Full-Width Page Container */}
        <main className="flex-1 w-full px-4 md:px-6 py-4">
          {visibleFlatItems.length === 0 ? (
            <NoPermissionState />
          ) : (
            <Routes>
              <Route
                path="/"
                element={
                  <PermissionRoute anyOf={['dashboard:read']} fallbackPath={firstVisiblePath}>
                    <DashboardTab />
                  </PermissionRoute>
                }
              />
              <Route
                path="/novels"
                element={
                  <PermissionRoute anyOf={['content.novels:read']} fallbackPath={firstVisiblePath}>
                    <NovelsTab />
                  </PermissionRoute>
                }
              />
              <Route
                path="/users"
                element={
                  <PermissionRoute anyOf={['revenue.users:read']} fallbackPath={firstVisiblePath}>
                    <UsersTab />
                  </PermissionRoute>
                }
              />
              <Route
                path="/orders"
                element={
                  <PermissionRoute anyOf={['payments.orders:read']} fallbackPath={firstVisiblePath}>
                    <Navigate to={'/payments/orders' + location.search} replace />
                  </PermissionRoute>
                }
              />
              <Route
                path="/payments"
                element={
                  firstPaymentPath ? <Navigate to={firstPaymentPath} replace /> : <NoPermissionState />
                }
              />
              <Route element={<Suspense fallback={<p role="status">正在加载支付中心…</p>}><Outlet /></Suspense>}>
                <Route
                  path="/payments/orders"
                  element={
                    <PermissionRoute anyOf={['payments.orders:read']} fallbackPath={firstVisiblePath}>
                      <OrdersTab />
                    </PermissionRoute>
                  }
                />
                <Route
                  path="/payments/subscriptions"
                  element={
                    <PermissionRoute anyOf={['payments.subscriptions:read']} fallbackPath={firstVisiblePath}>
                      <PaymentSubscriptions />
                    </PermissionRoute>
                  }
                />
                <Route
                  path="/payments/refunds"
                  element={
                    <PermissionRoute anyOf={['payments.refunds:read']} fallbackPath={firstVisiblePath}>
                      <PaymentRefunds key={admin.id} actor={admin.id} />
                    </PermissionRoute>
                  }
                />
                <Route
                  path="/payments/risk"
                  element={
                    <PermissionRoute anyOf={['payments.risk:read']} fallbackPath={firstVisiblePath}>
                      <PaymentCases key="risk" view="risk" />
                    </PermissionRoute>
                  }
                />
                <Route
                  path="/payments/disputes"
                  element={
                    <PermissionRoute anyOf={['payments.disputes:read']} fallbackPath={firstVisiblePath}>
                      <PaymentCases key="disputes" view="disputes" />
                    </PermissionRoute>
                  }
                />
                <Route
                  path="/payments/reconciliation"
                  element={
                    <PermissionRoute anyOf={['payments.reconciliation:read']} fallbackPath={firstVisiblePath}>
                      <PaymentReconciliation key={admin.id} actor={admin.id} />
                    </PermissionRoute>
                  }
                />
                <Route
                  path="/payments/channels"
                  element={
                    <PermissionRoute anyOf={['payments.channels:read']} fallbackPath={firstVisiblePath}>
                      <PaymentChannels />
                    </PermissionRoute>
                  }
                />
                <Route
                  path="/payments/products"
                  element={
                    <PermissionRoute anyOf={['payments.products:read']} fallbackPath={firstVisiblePath}>
                      <ProductsPage />
                    </PermissionRoute>
                  }
                />
              </Route>
              <Route
                path="/products"
                element={
                  <PermissionRoute anyOf={['payments.products:read']} fallbackPath={firstVisiblePath}>
                    <Navigate to={'/payments/products' + location.search} replace />
                  </PermissionRoute>
                }
              />
              <Route
                path="/feedback"
                element={
                  <PermissionRoute anyOf={['support.feedback:read']} fallbackPath={firstVisiblePath}>
                    <FeedbackTab />
                  </PermissionRoute>
                }
              />
              <Route
                path="/promotions"
                element={
                  <PermissionRoute anyOf={['ads.promotions:read']} fallbackPath={firstVisiblePath}>
                    <PromotionsTab />
                  </PermissionRoute>
                }
              />
              <Route
                path="/recharge-templates"
                element={
                  <PermissionRoute anyOf={['ads.templates:read']} fallbackPath={firstVisiblePath}>
                    <TemplatesTab />
                  </PermissionRoute>
                }
              />
              <Route
                path="/recharge-templates/new"
                element={
                  <PermissionRoute allOf={['ads.templates:write']} fallbackPath={templateWriteFallback}>
                    <TemplatesTab />
                  </PermissionRoute>
                }
              />
              <Route
                path="/recharge-templates/:templateId/edit"
                element={
                  <PermissionRoute allOf={['ads.templates:write']} fallbackPath={templateWriteFallback}>
                    <TemplatesTab />
                  </PermissionRoute>
                }
              />
              <Route
                path="/operations"
                element={
                  <PermissionRoute anyOf={['content.operations:read']} fallbackPath={firstVisiblePath}>
                    <OperationsTab />
                  </PermissionRoute>
                }
              />
              <Route
                path="/campaign"
                element={
                  <PermissionRoute anyOf={['content.operations:read']} fallbackPath={firstVisiblePath}>
                    <Navigate to="/operations?tab=campaigns" replace />
                  </PermissionRoute>
                }
              />
              <Route
                path="/pixels"
                element={
                  <PermissionRoute anyOf={['ads.pixels:read']} fallbackPath={firstVisiblePath}>
                    <PixelsTab />
                  </PermissionRoute>
                }
              />
              <Route
                path="/conversions"
                element={
                  <PermissionRoute anyOf={['ads.conversions:read']} fallbackPath={firstVisiblePath}>
                    <ConversionCenterTab />
                  </PermissionRoute>
                }
              />
              <Route
                path="/ad-config/logs"
                element={
                  <PermissionRoute anyOf={['ads.conversions:read']} fallbackPath={firstVisiblePath}>
                    <LegacyAdLogsRedirect />
                  </PermissionRoute>
                }
              />
              <Route
                path="/ad-config"
                element={
                  <PermissionRoute anyOf={['ads.pixels:read']} fallbackPath={firstVisiblePath}>
                    <Navigate to="/pixels" replace />
                  </PermissionRoute>
                }
              />
              <Route
                path="/domains"
                element={
                  <PermissionRoute anyOf={['system.domains:read']} fallbackPath={firstVisiblePath}>
                    <DomainsTab />
                  </PermissionRoute>
                }
              />
              <Route
                path="/payment"
                element={
                  <PermissionRoute anyOf={['payments.channels:read']} fallbackPath={firstVisiblePath}>
                    <Navigate to="/payments/channels" replace />
                  </PermissionRoute>
                }
              />
              <Route
                path="/accounts"
                element={
                  <PermissionRoute anyOf={['system.accounts:read', 'system.roles:read']} fallbackPath={firstVisiblePath}>
                    <AccountsTab />
                  </PermissionRoute>
                }
              />
              <Route
                path="/settings"
                element={
                  <PermissionRoute anyOf={['system.domains:read']} fallbackPath={firstVisiblePath}>
                    <Navigate to="/domains" replace />
                  </PermissionRoute>
                }
              />
              <Route
                path="*"
                element={firstVisiblePath ? <Navigate to={firstVisiblePath} replace /> : <NoPermissionState />}
              />
            </Routes>
          )}
        </main>
      </div>
    </div>
  );
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

  const navigate = useNavigate();
  const location = useLocation();

  const handleLogout = useCallback(() => {
    localStorage.removeItem('cms_token');
    localStorage.removeItem('cms_admin');
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
          const profile = await authApi.me();
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

  useEffect(() => {
    if (location.pathname === '/login') {
      document.title = '登录 - STAR NOVEL CMS';
    }
  }, [location.pathname]);

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

  return (
    <PermissionProvider admin={admin}>
      <AuthenticatedWorkspace admin={admin} onLogout={handleLogout} />
    </PermissionProvider>
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
