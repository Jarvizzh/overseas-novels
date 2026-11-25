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
  requiredPermissions: readonly string[];
}

export interface NavNode {
  id: string;
  label: string;
  icon: ReactNode;
  path?: string;
  requiredPermissions?: readonly string[];
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

export const NAV_TREE: NavNode[] = [
  {
    id: 'dashboard',
    label: '首页总览',
    path: '/',
    icon: <LayoutDashboard size={16} aria-hidden="true" />,
    requiredPermissions: ['dashboard:read'],
  },
  {
    id: 'content',
    label: '内容运营',
    icon: <BookOpen size={16} aria-hidden="true" />,
    children: [
      { id: 'novels', label: '书籍管理', path: '/novels', requiredPermissions: ['content.novels:read'] },
      { id: 'operations', label: '书城配置', path: '/operations', requiredPermissions: ['content.operations:read'] },
    ],
  },
  {
    id: 'revenue',
    label: '用户营收',
    icon: <Coins size={16} aria-hidden="true" />,
    children: [
      { id: 'users', label: '用户管理', path: '/users', requiredPermissions: ['revenue.users:read'] },
    ],
  },
  {
    id: 'payments',
    label: '支付中心',
    icon: <Coins size={16} aria-hidden="true" />,
    children: [
      { id: 'payment-orders', label: '交易与订单', path: '/payments/orders', requiredPermissions: ['payments.orders:read'] },
      {
        id: 'payment-subscriptions',
        label: '订阅与账期',
        path: '/payments/subscriptions',
        requiredPermissions: ['payments.subscriptions:read'],
      },
      { id: 'payment-refunds', label: '退款', path: '/payments/refunds', requiredPermissions: ['payments.refunds:read'] },
      { id: 'payment-risk', label: '风控', path: '/payments/risk', requiredPermissions: ['payments.risk:read'] },
      {
        id: 'payment-disputes',
        label: '争议与冲回',
        path: '/payments/disputes',
        requiredPermissions: ['payments.disputes:read'],
      },
      {
        id: 'payment-reconciliation',
        label: '资金对账',
        path: '/payments/reconciliation',
        requiredPermissions: ['payments.reconciliation:read'],
      },
      {
        id: 'payment-channels',
        label: '渠道与商户',
        path: '/payments/channels',
        requiredPermissions: ['payments.channels:read'],
      },
      { id: 'products', label: '商品与定价', path: '/payments/products', requiredPermissions: ['payments.products:read'] },
    ],
  },
  {
    id: 'ads',
    label: '广告投放',
    icon: <Target size={16} aria-hidden="true" />,
    children: [
      { id: 'promotions', label: '推广链接', path: '/promotions', requiredPermissions: ['ads.promotions:read'] },
      { id: 'templates', label: '充值模板', path: '/recharge-templates', requiredPermissions: ['ads.templates:read'] },
      { id: 'ad-pixels', label: '像素管理', path: '/pixels', requiredPermissions: ['ads.pixels:read'] },
      { id: 'ad-conversions', label: '回传中心', path: '/conversions', requiredPermissions: ['ads.conversions:read'] },
    ],
  },
  {
    id: 'support',
    label: '客诉管理',
    icon: <MessageSquare size={16} aria-hidden="true" />,
    children: [
      { id: 'feedback', label: '用户反馈', path: '/feedback', requiredPermissions: ['support.feedback:read'] },
    ],
  },
  {
    id: 'system',
    label: '系统管理',
    icon: <Settings size={16} aria-hidden="true" />,
    children: [
      { id: 'settings-domains', label: '域名管理', path: '/domains', requiredPermissions: ['system.domains:read'] },
      {
        id: 'settings-accounts',
        label: '账号管理',
        path: '/accounts',
        requiredPermissions: ['system.accounts:read', 'system.roles:read'],
      },
    ],
  },
];

export const DEFAULT_EXPANDED_GROUPS: Record<string, boolean> = Object.fromEntries(
  NAV_TREE.filter((node) => node.children && node.children.length > 0).map((node) => [node.id, true]),
);

export function isNavPathActive(pathname: string, targetPath: string): boolean {
  return pathname === targetPath || (targetPath !== '/' && pathname.startsWith(`${targetPath}/`));
}

export function getVisibleNavTree(hasAnyPermission: (codes: readonly string[]) => boolean): NavNode[] {
  const result: NavNode[] = [];
  for (const node of NAV_TREE) {
    if (node.requiredPermissions && !hasAnyPermission(node.requiredPermissions)) {
      continue;
    }
    if (node.children) {
      const visibleChildren = node.children.filter((child) => hasAnyPermission(child.requiredPermissions));
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
