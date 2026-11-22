import type { ReactNode } from 'react';
import {
  BookOpen,
  Coins,
  LayoutDashboard,
  MessageSquare,
  Settings,
  Target,
} from 'lucide-react';

export interface NavLinkItem {
  id: string;
  label: string;
  path: string;
  roles?: string[];
}

export interface NavNode {
  id: string;
  label: string;
  icon: ReactNode;
  path?: string;
  roles?: string[];
  children?: NavLinkItem[];
}

export interface FlatNavEntry {
  id: string;
  label: string;
  path: string;
  groupId?: string;
  groupLabel?: string;
  icon: ReactNode;
}

export const ROLE_LABELS: Record<string, string> = {
  SuperAdmin: '超级管理员',
  Admin: '管理员',
  Editor: '编辑',
  MediaBuyer: '投手',
  Finance: '财务',
  Support: '客服',
};

export const CONVERSION_READ_ROLES = ['SuperAdmin', 'Admin', 'MediaBuyer', 'Finance'];
export const PAYMENT_FINANCE_ROLES = ['SuperAdmin', 'Finance'];
export const PAYMENT_SUMMARY_ROLES = [...PAYMENT_FINANCE_ROLES, 'Admin', 'Support'];

export const NAV_TREE: NavNode[] = [
  {
    id: 'dashboard',
    label: '首页总览',
    path: '/',
    icon: <LayoutDashboard size={16} aria-hidden="true" />,
  },
  {
    id: 'content',
    label: '内容运营',
    icon: <BookOpen size={16} aria-hidden="true" />,
    children: [
      { id: 'novels', label: '书籍管理', path: '/novels' },
      { id: 'operations', label: '书城配置', path: '/operations' },
    ],
  },
  {
    id: 'revenue',
    label: '用户营收',
    icon: <Coins size={16} aria-hidden="true" />,
    children: [
      { id: 'users', label: '用户管理', path: '/users' },
    ],
  },
  {
    id: 'payments',
    label: '支付中心',
    icon: <Coins size={16} aria-hidden="true" />,
    roles: PAYMENT_SUMMARY_ROLES,
    children: [
      { id: 'payment-orders', label: '交易与订单', path: '/payments/orders', roles: PAYMENT_SUMMARY_ROLES },
      { id: 'payment-subscriptions', label: '订阅与账期', path: '/payments/subscriptions', roles: PAYMENT_SUMMARY_ROLES },
      { id: 'payment-refunds', label: '退款', path: '/payments/refunds', roles: PAYMENT_FINANCE_ROLES },
      { id: 'payment-risk', label: '风控', path: '/payments/risk', roles: PAYMENT_FINANCE_ROLES },
      { id: 'payment-disputes', label: '争议与冲回', path: '/payments/disputes', roles: PAYMENT_FINANCE_ROLES },
      { id: 'payment-reconciliation', label: '资金对账', path: '/payments/reconciliation', roles: PAYMENT_FINANCE_ROLES },
      { id: 'payment-channels', label: '渠道与商户', path: '/payments/channels', roles: ['SuperAdmin'] },
    ],
  },
  {
    id: 'ads',
    label: '广告投放',
    icon: <Target size={16} aria-hidden="true" />,
    children: [
      { id: 'promotions', label: '推广链接', path: '/promotions' },
      { id: 'templates', label: '充值模板', path: '/recharge-templates' },
      { id: 'ad-pixels', label: '像素管理', path: '/pixels', roles: CONVERSION_READ_ROLES },
      { id: 'ad-conversions', label: '回传中心', path: '/conversions', roles: CONVERSION_READ_ROLES },
    ],
  },
  {
    id: 'support',
    label: '客诉管理',
    icon: <MessageSquare size={16} aria-hidden="true" />,
    children: [{ id: 'feedback', label: '用户反馈', path: '/feedback' }],
  },
  {
    id: 'system',
    label: '系统管理',
    icon: <Settings size={16} aria-hidden="true" />,
    roles: ['SuperAdmin'],
    children: [
      { id: 'settings-domains', label: '域名管理', path: '/domains', roles: ['SuperAdmin'] },
      { id: 'settings-accounts', label: '账号管理', path: '/accounts', roles: ['SuperAdmin'] },
    ],
  },
];

export const DEFAULT_EXPANDED_GROUPS: Record<string, boolean> = Object.fromEntries(
  NAV_TREE.filter((node) => node.children && node.children.length > 0).map((node) => [node.id, true]),
);

export function isNavPathActive(pathname: string, targetPath: string): boolean {
  return pathname === targetPath || (targetPath !== '/' && pathname.startsWith(`${targetPath}/`));
}

export function getVisibleNavTree(role: string): NavNode[] {
  const result: NavNode[] = [];
  for (const node of NAV_TREE) {
    if (node.roles && !node.roles.includes(role)) {
      continue;
    }
    if (node.children) {
      const visibleChildren = node.children.filter((child) => !child.roles || child.roles.includes(role));
      if (visibleChildren.length === 0) {
        continue;
      }
      result.push({ ...node, children: visibleChildren });
    } else if (node.path) {
      result.push(node);
    }
  }
  return result;
}

export function flattenNavEntries(tree: NavNode[]): FlatNavEntry[] {
  const flat: FlatNavEntry[] = [];
  for (const node of tree) {
    if (node.children) {
      for (const child of node.children) {
        flat.push({
          id: child.id,
          label: child.label,
          path: child.path,
          groupId: node.id,
          groupLabel: node.label,
          icon: node.icon,
        });
      }
    } else if (node.path) {
      flat.push({
        id: node.id,
        label: node.label,
        path: node.path,
        icon: node.icon,
      });
    }
  }
  return flat;
}
