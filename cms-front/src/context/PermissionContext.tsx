import { createContext, useContext, useMemo, type ReactNode } from 'react';
import type { AdminUser } from '../utils/api';

export interface PermissionContextValue {
  admin: AdminUser | null;
  permissions: ReadonlySet<string>;
  isSuperAdmin: boolean;
  hasPermission: (code: string) => boolean;
  hasAnyPermission: (codes: readonly string[]) => boolean;
  hasAllPermissions: (codes: readonly string[]) => boolean;
  canRead: (resource: string) => boolean;
  canWrite: (resource: string) => boolean;
}

const EMPTY_SET: ReadonlySet<string> = new Set();

const DEFAULT_CONTEXT: PermissionContextValue = {
  admin: null,
  permissions: EMPTY_SET,
  isSuperAdmin: false,
  hasPermission: () => false,
  hasAnyPermission: () => false,
  hasAllPermissions: () => false,
  canRead: () => false,
  canWrite: () => false,
};

const PermissionContext = createContext<PermissionContextValue>(DEFAULT_CONTEXT);

interface PermissionProviderProps {
  admin: AdminUser | null;
  children: ReactNode;
}

export function PermissionProvider({ admin, children }: PermissionProviderProps) {
  const value = useMemo<PermissionContextValue>(() => {
    const isSuperAdmin = Boolean(admin?.is_super_admin);
    const permissions: ReadonlySet<string> = admin
      ? new Set(Array.isArray(admin.permissions) ? admin.permissions : [])
      : EMPTY_SET;

    const hasPermission = (code: string): boolean => {
      if (!admin) return false;
      if (isSuperAdmin) return true;
      return permissions.has(code);
    };

    const hasAnyPermission = (codes: readonly string[]): boolean => {
      if (!admin) return false;
      if (codes.length === 0) return true;
      if (isSuperAdmin) return true;
      return codes.some((code) => permissions.has(code));
    };

    const hasAllPermissions = (codes: readonly string[]): boolean => {
      if (!admin) return false;
      if (codes.length === 0) return true;
      if (isSuperAdmin) return true;
      return codes.every((code) => permissions.has(code));
    };

    const canRead = (resource: string): boolean =>
      hasPermission(resource.includes(':') ? resource : `${resource}:read`);

    const canWrite = (resource: string): boolean =>
      hasPermission(resource.includes(':') ? resource : `${resource}:write`);

    return {
      admin,
      permissions,
      isSuperAdmin,
      hasPermission,
      hasAnyPermission,
      hasAllPermissions,
      canRead,
      canWrite,
    };
  }, [admin]);

  return <PermissionContext.Provider value={value}>{children}</PermissionContext.Provider>;
}

// oxlint-disable-next-line react/only-export-components
export function usePermission(): PermissionContextValue {
  return useContext(PermissionContext);
}
