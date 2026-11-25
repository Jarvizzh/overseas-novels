import { useEffect, useMemo, useState } from 'react';
import { Lock, Plus, Shield } from 'lucide-react';
import {
  authApi,
  type PermissionGroupMeta,
  type RoleItem,
} from '../../utils/api';
import {
  RowActionMenu,
  SearchInput,
  SkeletonRows,
  StatusDot,
  type TableDensity,
  TableRegion,
  ViewOptionsPopover,
} from '../ui/Primitives';
import { RoleFormDrawer } from './RoleFormDrawer';

interface RolesPanelProps {
  roles: RoleItem[];
  loading: boolean;
  hasLoadedOnce: boolean;
  canManage: boolean;
  onRefresh: () => void;
}

export function RolesPanel({
  roles,
  loading,
  hasLoadedOnce,
  canManage,
  onRefresh,
}: RolesPanelProps) {
  const [searchQuery, setSearchQuery] = useState('');
  const [density, setDensity] = useState<TableDensity>('comfortable');

  const [catalog, setCatalog] = useState<PermissionGroupMeta[]>([]);
  const [catalogLoading, setCatalogLoading] = useState(true);

  const [drawerOpen, setDrawerOpen] = useState(false);
  const [drawerMode, setDrawerMode] = useState<'create' | 'edit' | 'view'>('view');
  const [activeRole, setActiveRole] = useState<RoleItem | null>(null);

  useEffect(() => {
    const controller = new AbortController();
    setCatalogLoading(true);
    void authApi
      .getPermissions(controller.signal)
      .then((res) => {
        if (!controller.signal.aborted) {
          setCatalog(res?.groups || []);
        }
      })
      .catch((err: unknown) => {
        if (controller.signal.aborted) return;
        window.showToast?.((err instanceof Error ? err.message : '') || '加载权限目录失败', 'error');
      })
      .finally(() => {
        if (!controller.signal.aborted) setCatalogLoading(false);
      });
    return () => controller.abort();
  }, []);

  const filteredRoles = useMemo(() => {
    const q = searchQuery.trim().toLowerCase();
    if (!q) return roles;
    return roles.filter(
      (r) =>
        r.name.toLowerCase().includes(q) ||
        r.code.toLowerCase().includes(q) ||
        (r.description || '').toLowerCase().includes(q),
    );
  }, [roles, searchQuery]);

  const openCreate = () => {
    setActiveRole(null);
    setDrawerMode('create');
    setDrawerOpen(true);
  };

  const openRoleDrawer = (role: RoleItem) => {
    setActiveRole(role);
    setDrawerMode(!canManage || role.is_super_admin ? 'view' : 'edit');
    setDrawerOpen(true);
  };

  const handleToggleRoleStatus = (role: RoleItem) => {
    if (!canManage || role.is_super_admin) return;
    const nextStatus = role.status === 1 ? 2 : 1;
    const run = async () => {
      try {
        await authApi.updateRole(role.id, {
          name: role.name,
          description: role.description,
          status: nextStatus,
          permissions: role.permissions || [],
        });
        window.showToast?.(`角色「${role.name}」已${nextStatus === 1 ? '启用' : '禁用'}`, 'success');
        onRefresh();
      } catch (err: unknown) {
        window.showToast?.((err instanceof Error ? err.message : '') || '更新角色状态失败', 'error');
      }
    };

    if (nextStatus === 2 && window.showConfirm) {
      window.showConfirm(
        `确定要禁用角色「${role.name}」吗？禁用后绑定该角色的所有管理员将立即无法访问后台。`,
        () => {
          void run();
        },
      );
      return;
    }
    void run();
  };

  const handleDeleteRole = (role: RoleItem) => {
    if (!canManage || role.is_system) return;
    if (role.admin_count > 0) {
      window.showToast?.(`角色「${role.name}」仍绑定 ${role.admin_count} 个账号，请先调整对应账号角色后再删除`, 'error');
      return;
    }
    window.showConfirm?.(`确定要删除自定义角色「${role.name}」吗？此操作不可撤销。`, async () => {
      try {
        await authApi.deleteRole(role.id);
        window.showToast?.(`角色「${role.name}」已删除`, 'success');
        onRefresh();
      } catch (err: unknown) {
        window.showToast?.((err instanceof Error ? err.message : '') || '删除角色失败', 'error');
      }
    });
  };

  return (
    <div className="space-y-4">
      <search className="flex flex-wrap items-center justify-between gap-2.5">
        <div className="flex flex-wrap items-center gap-2 flex-1 min-w-[260px]">
          <SearchInput
            className="w-full sm:w-72"
            ariaLabel="搜索角色名称、标识或说明"
            placeholder="搜索角色名称、标识或说明..."
            value={searchQuery}
            onChange={setSearchQuery}
          />
        </div>

        <div className="flex flex-wrap items-center gap-2 text-xs text-[var(--cms-muted-fg)]">
          <span className="tabular-nums">共 {filteredRoles.length} 个角色</span>
          <ViewOptionsPopover density={density} onDensityChange={setDensity} />
          {canManage && (
            <button type="button" onClick={openCreate} className="btn-primary h-8 px-3.5 text-xs">
              <Plus size={14} aria-hidden="true" />
              <span>新建角色</span>
            </button>
          )}
        </div>
      </search>

      <TableRegion ariaLabel="RBAC 角色与权限列表">
        <table className="cms-table min-w-[780px]" data-density={density}>
          <thead>
            <tr>
              <th scope="col">角色名称</th>
              <th scope="col">角色标识</th>
              <th scope="col">说明</th>
              <th scope="col">权限数</th>
              <th scope="col">绑定账号</th>
              <th scope="col">状态</th>
              <th scope="col" className="text-right w-px whitespace-nowrap">
                操作
              </th>
            </tr>
          </thead>
          <tbody className={loading && hasLoadedOnce ? 'opacity-65 transition-opacity' : ''}>
            {!hasLoadedOnce && loading ? (
              <SkeletonRows rows={5} cols={7} />
            ) : filteredRoles.length === 0 ? (
              <tr>
                <td colSpan={7} className="py-16 text-center text-[var(--cms-muted-fg)]">
                  暂无匹配的角色
                </td>
              </tr>
            ) : (
              filteredRoles.map((role) => (
                <tr key={role.id}>
                  <td>
                    <div className="flex items-center gap-2">
                      <span className="font-medium text-[var(--cms-fg)]">{role.name}</span>
                      {role.is_super_admin ? (
                        <span className="inline-flex items-center gap-1 px-1.5 py-0.5 rounded bg-[color-mix(in_srgb,var(--cms-error)_12%,transparent)] text-[10px] font-medium text-[var(--cms-error)]">
                          <Lock size={10} aria-hidden="true" />
                          超级管理员
                        </span>
                      ) : role.is_system ? (
                        <span className="inline-flex items-center gap-1 px-1.5 py-0.5 rounded bg-[var(--cms-secondary)] text-[10px] text-[var(--cms-muted-fg)]">
                          <Shield size={10} aria-hidden="true" />
                          系统内置
                        </span>
                      ) : null}
                    </div>
                  </td>
                  <td className="font-mono-code text-[var(--cms-muted-fg)]">{role.code}</td>
                  <td className="max-w-[260px] truncate text-[var(--cms-muted-fg)]" title={role.description}>
                    {role.description || '-'}
                  </td>
                  <td className="tabular-nums text-[var(--cms-fg)] whitespace-nowrap">
                    {role.is_super_admin ? '全量权限' : `${role.permissions?.length || 0} 项`}
                  </td>
                  <td className="tabular-nums text-[var(--cms-fg)] whitespace-nowrap">{role.admin_count ?? 0} 人</td>
                  <td>
                    <StatusDot
                      tone={role.status === 1 ? 'success' : 'muted'}
                      label={role.status === 1 ? '正常启用' : '已禁用'}
                    />
                  </td>
                  <td className="text-right whitespace-nowrap">
                    <div className="inline-flex items-center justify-end gap-0.5 whitespace-nowrap">
                      <button
                        type="button"
                        onClick={() => openRoleDrawer(role)}
                        className="btn-detail whitespace-nowrap"
                      >
                        {!canManage || role.is_super_admin ? '查看权限' : '编辑权限'}
                      </button>
                      {canManage && !role.is_super_admin && (
                        <RowActionMenu
                          ariaLabel={`角色 ${role.name} 更多操作`}
                          items={[
                            {
                              label: role.status === 1 ? '禁用角色' : '启用角色',
                              tone: role.status === 1 ? 'warning' : 'success',
                              onClick: () => handleToggleRoleStatus(role),
                            },
                            ...(!role.is_system
                              ? [
                                  {
                                    label: '删除角色',
                                    tone: 'danger' as const,
                                    dividerBefore: true,
                                    disabled: role.admin_count > 0,
                                    onClick: () => handleDeleteRole(role),
                                  },
                                ]
                              : []),
                          ]}
                        />
                      )}
                    </div>
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </TableRegion>

      <RoleFormDrawer
        open={drawerOpen}
        mode={drawerMode}
        role={activeRole}
        catalog={catalog}
        catalogLoading={catalogLoading}
        onClose={() => setDrawerOpen(false)}
        onSaved={onRefresh}
      />
    </div>
  );
}
