import { useState, useEffect } from 'react';
import { BrowserRouter, Routes, Route, Navigate, useNavigate, useLocation, Link } from 'react-router-dom';
import { apiRequest } from './utils/api';
import type { AdminUser } from './utils/api';
import Login from './pages/Login';
import DashboardTab from './pages/DashboardTab';
import NovelsTab from './pages/NovelsTab';
import UsersTab from './pages/UsersTab';
import OrdersTab from './pages/OrdersTab';
import PaymentTab from './pages/PaymentTab';
import CampaignTab from './pages/CampaignTab';
import AccountsTab from './pages/AccountsTab';
import PixelsTab from './pages/PixelsTab';
import TemplatesTab from './pages/TemplatesTab';
import PromotionsTab from './pages/PromotionsTab';
import TrackingLogsTab from './pages/TrackingLogsTab';
import {
  LayoutDashboard,
  Book,
  Users,
  CreditCard,
  Settings,
  LogOut,
  ChevronLeft,
  ChevronRight,
  Coins,
  ShieldCheck,
  ChevronDown,
  ChevronUp,
  Link2,
  Target,
  Gift,
  FileText
} from 'lucide-react';


const navItems = [
  { id: 'dashboard', label: '首页看板', path: '/', icon: <LayoutDashboard size={18} /> },
  { id: 'novels', label: '书籍管理', path: '/novels', icon: <Book size={18} /> },
  { id: 'users', label: '用户管理', path: '/users', icon: <Users size={18} /> },
  { id: 'orders', label: '订单管理', path: '/orders', icon: <CreditCard size={18} /> },
  { id: 'promotions', label: '推广链接', path: '/promotions', icon: <Link2 size={18} /> },
  { id: 'templates', label: '充值模板', path: '/recharge-templates', icon: <Coins size={18} /> },
  {
    id: 'campaign',
    label: '活动配置',
    path: '/campaign',
    icon: <Gift size={18} />
  },
  {
    id: 'ad-config',
    label: '广告配置',
    path: '/ad-config',
    icon: <Target size={18} />,
    children: [
      { id: 'ad-pixels', label: '像素管理', path: '/ad-config/pixels', icon: <Target size={14} /> },
      { id: 'ad-logs', label: '回传日志', path: '/ad-config/logs', icon: <FileText size={14} /> }
    ]
  },
  {
    id: 'settings',
    label: '系统管理',
    path: '/settings',
    icon: <Settings size={18} />,
    roles: ['SuperAdmin'],
    children: [
      { id: 'settings-payment', label: '支付管理', path: '/settings/payment', icon: <CreditCard size={14} /> },
      { id: 'settings-accounts', label: '账号管理', path: '/settings/accounts', icon: <ShieldCheck size={14} /> }
    ]
  },
];



declare global {
  interface Window {
    showToast?: (message: string, type?: 'success' | 'error' | 'info') => void;
    showConfirm?: (message: string, onConfirm: () => void) => void;
  }
}

function AppContent() {
  const [toast, setToast] = useState<{ message: string; type: 'success' | 'error' | 'info' } | null>(null);
  const [confirmData, setConfirmData] = useState<{ message: string; onConfirm: () => void } | null>(null);

  useEffect(() => {
    window.showToast = (message: string, type: 'success' | 'error' | 'info' = 'success') => {
      setToast({ message, type });
    };
    window.showConfirm = (message: string, onConfirm: () => void) => {
      setConfirmData({ message, onConfirm });
    };
  }, []);

  useEffect(() => {
    if (toast) {
      const timer = setTimeout(() => {
        setToast(null);
      }, 3000);
      return () => clearTimeout(timer);
    }
  }, [toast]);
  const [token, setToken] = useState<string | null>(localStorage.getItem('cms_token'));
  const [admin, setAdmin] = useState<AdminUser | null>(
    localStorage.getItem('cms_admin') ? JSON.parse(localStorage.getItem('cms_admin')!) : null
  );
  const [initializing, setInitializing] = useState(true);
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false);
  const [settingsExpanded, setSettingsExpanded] = useState(true);
  const [adConfigExpanded, setAdConfigExpanded] = useState(true);

  const navigate = useNavigate();
  const location = useLocation();

  useEffect(() => {
    const verifySession = async () => {
      if (token) {
        try {
          const profile = await apiRequest('GET', '/auth/me');
          setAdmin(profile);
          localStorage.setItem('cms_admin', JSON.stringify(profile));
        } catch (e) {
          handleLogout();
        }
      }
      setInitializing(false);
    };
    verifySession();
  }, [token]);

  // Dynamically update browser tab title to match navigation name
  useEffect(() => {
    if (location.pathname === '/login') {
      document.title = '登录 - STAR NOVEL CMS';
      return;
    }
    const currentItem = navItems.find(item => item.path === location.pathname);
    if (currentItem) {
      document.title = `${currentItem.label} - STAR NOVEL CMS`;
    } else {
      document.title = 'STAR NOVEL CMS';
    }
  }, [location.pathname]);

  // Auth & RBAC Route Guard Redirects
  useEffect(() => {
    if (!initializing) {
      if (!token || !admin) {
        if (location.pathname !== '/login') {
          navigate('/login', { replace: true });
        }
      } else {
        if (location.pathname === '/login') {
          navigate('/', { replace: true });
        }
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

  const handleLogout = () => {
    localStorage.removeItem('cms_token');
    localStorage.removeItem('cms_admin');
    setToken(null);
    setAdmin(null);
    navigate('/login', { replace: true });
  };

  if (initializing) {
    return (
      <div style={{
        display: 'flex', minHeight: '100vh', alignItems: 'center', justifyContent: 'center',
        backgroundColor: 'hsl(var(--bg-base))', color: 'hsl(var(--text-secondary))'
      }}>
        正在初始化控制台会话...
      </div>
    );
  }

  // If not authenticated, only allow login route
  if (!token || !admin) {
    return (
      <Routes>
        <Route path="/login" element={<Login onLoginSuccess={handleLoginSuccess} />} />
        <Route path="*" element={<Navigate to="/login" replace />} />
      </Routes>
    );
  }

  const filteredNavItems = navItems.filter(item => {
    if (!item.roles) return true;
    return item.roles.includes(admin.role);
  });

  return (
    <div style={{ display: 'flex', minHeight: '100vh', backgroundColor: 'hsl(var(--bg-base))' }}>

      {/* Sidebar Panel */}
      <aside className="glass-panel" style={{
        width: sidebarCollapsed ? '72px' : '200px',
        borderWidth: '0 1px 0 0',
        borderRadius: 0,
        display: 'flex',
        flexDirection: 'column',
        padding: sidebarCollapsed ? '24px 8px' : '24px 16px',
        backgroundColor: 'hsl(var(--bg-surface) / 0.95)',
        transition: 'width 0.25s cubic-bezier(0.16, 1, 0.3, 1), padding 0.25s ease',
        overflow: 'hidden'
      }}>
        {/* Sidebar Brand header */}
        <div style={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: sidebarCollapsed ? 'center' : 'space-between',
          marginBottom: '32px',
          paddingLeft: sidebarCollapsed ? '0' : '8px'
        }}>
          {!sidebarCollapsed && (
            <div style={{ display: 'flex', alignItems: 'center', gap: '10px' }}>
              <img src="/assets/logo.png" alt="STAR NOVEL Logo" style={{ width: '36px', height: '36px', objectFit: 'contain', borderRadius: '8px', border: '1px solid hsl(var(--border))' }} />
              <div>
                <h3 style={{ fontSize: '0.9rem', fontWeight: 700 }} className="gradient-text">STAR NOVEL</h3>
                {/* <span style={{ fontSize: '0.55rem', color: 'hsl(var(--text-muted))', letterSpacing: '0.05em' }}>小说管理系统</span> */}
              </div>
            </div>
          )}
          {sidebarCollapsed && (
            <img src="/assets/logo.png" alt="STAR NOVEL Logo" style={{ width: '36px', height: '36px', objectFit: 'contain', borderRadius: '8px', border: '1px solid hsl(var(--border))' }} />
          )}

          {/* Sidebar Collapse Toggle Button */}
          <button
            onClick={() => setSidebarCollapsed(!sidebarCollapsed)}
            className="btn-secondary"
            style={{
              padding: '4px',
              borderRadius: '6px',
              border: 'none',
              marginLeft: sidebarCollapsed ? '0' : '6px',
              marginTop: sidebarCollapsed ? '8px' : '0',
              display: 'inline-flex',
              alignItems: 'center',
              justifyContent: 'center',
              cursor: 'pointer',
              boxShadow: 'var(--shadow-sm)'
            }}
            title={sidebarCollapsed ? '展开导航栏' : '收起导航栏'}
          >
            {sidebarCollapsed ? <ChevronRight size={14} /> : <ChevronLeft size={14} />}
          </button>
        </div>

        {/* Navigation links with React Router Link */}
        <nav style={{ display: 'flex', flexDirection: 'column', gap: '6px', flex: 1 }}>
          {filteredNavItems.map((item) => {
            const hasChildren = item.children && item.children.length > 0;
            const isChildPathActive = hasChildren && item.children.some(child => location.pathname.startsWith(child.path));
            const active = hasChildren ? isChildPathActive : location.pathname === item.path;

            const isExpanded = item.id === 'settings' ? settingsExpanded : adConfigExpanded;
            const toggleExpanded = () => {
              if (item.id === 'settings') {
                setSettingsExpanded(!settingsExpanded);
              } else {
                setAdConfigExpanded(!adConfigExpanded);
              }
            };

            if (hasChildren && !sidebarCollapsed) {
              return (
                <div key={item.id} style={{ display: 'flex', flexDirection: 'column', gap: '4px' }}>
                  <div
                    onClick={toggleExpanded}
                    style={{
                      display: 'flex',
                      alignItems: 'center',
                      justifyContent: 'space-between',
                      padding: '10px 12px',
                      borderRadius: '8px',
                      fontSize: '0.85rem',
                      fontWeight: 500,
                      cursor: 'pointer',
                      width: '100%',
                      backgroundColor: active ? 'hsl(var(--primary) / 0.05)' : 'transparent',
                      color: active ? 'hsl(var(--text-primary))' : 'hsl(var(--text-secondary))',
                      transition: 'all 0.2s ease'
                    }}
                  >
                    <div style={{ display: 'flex', alignItems: 'center', gap: '10px' }}>
                      <span style={{ color: active ? 'hsl(var(--primary))' : 'inherit', display: 'flex', alignItems: 'center' }}>
                        {item.icon}
                      </span>
                      {item.label}
                    </div>
                    {isExpanded ? <ChevronUp size={14} /> : <ChevronDown size={14} />}
                  </div>

                  {isExpanded && (
                    <div style={{ display: 'flex', flexDirection: 'column', gap: '4px', paddingLeft: '16px' }}>
                      {item.children.map((child) => {
                        const childActive = location.pathname === child.path;
                        return (
                          <Link
                            key={child.id}
                            to={child.path}
                            style={{
                              display: 'flex',
                              alignItems: 'center',
                              gap: '8px',
                              padding: '8px 12px',
                              borderRadius: '6px',
                              fontSize: '0.8rem',
                              fontWeight: 500,
                              textDecoration: 'none',
                              cursor: 'pointer',
                              backgroundColor: childActive ? 'hsl(var(--primary) / 0.15)' : 'transparent',
                              color: childActive ? 'hsl(var(--text-primary))' : 'hsl(var(--text-secondary))',
                              border: childActive ? '1px solid hsl(var(--primary) / 0.25)' : '1px solid transparent',
                              transition: 'all 0.2s ease'
                            }}
                          >
                            <span style={{ color: childActive ? 'hsl(var(--primary))' : 'inherit', display: 'flex', alignItems: 'center' }}>
                              {child.icon}
                            </span>
                            {child.label}
                          </Link>
                        );
                      })}
                    </div>
                  )}
                </div>
              );
            }

            // Collapsed view or regular links
            return (
              <Link
                key={item.id}
                to={hasChildren ? item.children[0].path : item.path}
                title={sidebarCollapsed ? item.label : undefined}
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: sidebarCollapsed ? 'center' : 'flex-start',
                  gap: sidebarCollapsed ? '0' : '10px',
                  padding: '10px 12px',
                  borderRadius: '8px',
                  fontSize: '0.85rem',
                  fontWeight: 500,
                  cursor: 'pointer',
                  width: '100%',
                  textAlign: 'left',
                  textDecoration: 'none',
                  backgroundColor: active ? 'hsl(var(--primary) / 0.15)' : 'transparent',
                  color: active ? 'hsl(var(--text-primary))' : 'hsl(var(--text-secondary))',
                  border: active ? '1px solid hsl(var(--primary) / 0.25)' : '1px solid transparent',
                  transition: 'all 0.2s ease'
                }}
              >
                <span style={{ color: active ? 'hsl(var(--primary))' : 'inherit', display: 'flex', alignItems: 'center' }}>
                  {item.icon}
                </span>
                {!sidebarCollapsed && item.label}
              </Link>
            );
          })}
        </nav>

      </aside>

      {/* Main Content Area */}
      <main style={{ flex: 1, overflowY: 'auto', display: 'flex', flexDirection: 'column' }}>
        {/* Header Bar */}
        <header className="glass-panel" style={{
          display: 'flex',
          justifyContent: 'flex-end',
          alignItems: 'center',
          padding: '12px 24px',
          borderWidth: '0 0 1px 0',
          borderRadius: 0,
          backgroundColor: 'hsl(var(--bg-surface) / 0.9)',
          backdropFilter: 'blur(8px)',
          position: 'sticky',
          top: 0,
          zIndex: 50,
          gap: '20px'
        }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: '10px' }}>
            <div style={{
              width: '32px', height: '32px', borderRadius: '16px', backgroundColor: 'hsl(var(--primary) / 0.1)',
              display: 'flex', alignItems: 'center', justifyContent: 'center', fontWeight: 600, color: 'hsl(var(--primary))',
              fontSize: '0.85rem'
            }}>
              {admin.nickname ? admin.nickname.charAt(0).toUpperCase() : 'A'}
            </div>
            <div>
              <div style={{ fontSize: '0.85rem', fontWeight: 500 }}>
                {admin.nickname || admin.username}
              </div>
            </div>
          </div>

          <button onClick={handleLogout} className="btn-secondary" style={{
            padding: '6px 12px', fontSize: '0.8rem', display: 'flex', gap: '6px', alignItems: 'center',
            color: '#f87171', borderColor: 'rgba(239, 68, 68, 0.15)'
          }}>
            <LogOut size={14} /> 退出登录
          </button>
        </header>

        <Routes>
          <Route path="/" element={<DashboardTab />} />
          <Route path="/novels" element={<NovelsTab />} />
          <Route path="/users" element={<UsersTab />} />
          <Route path="/orders" element={<OrdersTab />} />
          <Route path="/promotions" element={<PromotionsTab />} />
          <Route path="/recharge-templates" element={<TemplatesTab />} />
          <Route path="/campaign" element={<CampaignTab />} />
          <Route path="/ad-config/pixels" element={<PixelsTab />} />
          <Route path="/ad-config/logs" element={<TrackingLogsTab />} />
          <Route path="/ad-config" element={<Navigate to="/ad-config/pixels" replace />} />
          <Route path="/settings/payment" element={admin.role === 'SuperAdmin' ? <PaymentTab /> : <Navigate to="/" replace />} />
          <Route path="/settings/accounts" element={admin.role === 'SuperAdmin' ? <AccountsTab /> : <Navigate to="/" replace />} />
          <Route path="/settings" element={admin.role === 'SuperAdmin' ? <Navigate to="/settings/payment" replace /> : <Navigate to="/" replace />} />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </main>

      {toast && (
        <div style={{
          position: 'fixed',
          top: '24px',
          right: '24px',
          backgroundColor: toast.type === 'success' ? '#10b981' : toast.type === 'error' ? '#ef4444' : 'hsl(var(--primary))',
          color: '#ffffff',
          padding: '12px 20px',
          borderRadius: '8px',
          boxShadow: '0 10px 15px -3px rgba(0, 0, 0, 0.1), 0 4px 6px -2px rgba(0, 0, 0, 0.05)',
          zIndex: 99999,
          fontSize: '0.85rem',
          fontWeight: 600,
          display: 'flex',
          alignItems: 'center',
          gap: '8px'
        }} className="animate-fade-in">
          {toast.message}
        </div>
      )}

      {confirmData && (
        <div style={{
          position: 'fixed',
          top: 0,
          left: 0,
          right: 0,
          bottom: 0,
          backgroundColor: 'rgba(15, 23, 42, 0.45)',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          zIndex: 999999,
        }} className="animate-fade-in">
          <div className="animate-scale-up" style={{
            width: '420px',
            padding: '24px',
            borderRadius: '16px',
            backgroundColor: 'hsl(var(--bg-surface))',
            border: '1px solid hsl(var(--border))',
            boxShadow: 'var(--shadow-lg)'
          }}>
            <h3 style={{
              fontSize: '1.1rem',
              fontWeight: 700,
              color: 'hsl(var(--text-primary))',
              marginBottom: '12px'
            }}>确认操作</h3>
            <p style={{
              fontSize: '0.875rem',
              color: 'hsl(var(--text-secondary))',
              lineHeight: 1.6,
              marginBottom: '24px'
            }}>{confirmData.message}</p>
            <div style={{
              display: 'flex',
              justifyContent: 'flex-end',
              gap: '12px'
            }}>
              <button onClick={() => setConfirmData(null)} className="btn-secondary" style={{ padding: '8px 16px', fontSize: '0.85rem' }}>
                取消
              </button>
              <button onClick={() => {
                confirmData.onConfirm();
                setConfirmData(null);
              }} className="btn-primary" style={{
                padding: '8px 16px',
                fontSize: '0.85rem',
                backgroundColor: '#ef4444',
                borderColor: '#ef4444'
              }}>
                确定
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

export default function App() {
  return (
    <BrowserRouter>
      <AppContent />
    </BrowserRouter>
  );
}
