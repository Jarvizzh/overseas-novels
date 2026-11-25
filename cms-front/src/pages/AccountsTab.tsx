import { useCallback, useEffect, useMemo, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import { Plus, Shield, Users } from 'lucide-react';
import {
  authApi,
  type AdminListItem,
  type RoleItem,
} from '../utils/api';
import { usePermission } from '../context/PermissionContext';
import CustomSelect from '../components/CustomSelect';
import { RolesPanel } from '../components/accounts/RolesPanel';
import { CmsDialog } from '../components/ui/CmsDialog';
import {
  RowActionMenu,
  SearchInput,
  SkeletonRows,
  StatusDot,
  type TableDensity,
  TableRegion,
  ViewOptionsPopover,
} from '../components/ui/Primitives';

type AccountsSubTab = 'accounts' | 'roles';

export default function AccountsTab() {
  const { admin: currentAdmin, canRead, isSuperAdmin } = usePermission();
  const canReadAccounts = canRead('system.accounts');
  const canReadRoles = canRead('system.roles');
  const canManage = isSuperAdmin;

  const [searchParams, setSearchParams] = useSearchParams();
  const tabParam = searchParams.get('tab');

  const activeTab: AccountsSubTab = useMemo(() => {
    if (tabParam === 'roles' && canReadRoles) return 'roles';
    if (tabParam === 'accounts' && canReadAccounts) return 'accounts';
    if (canReadAccounts) return 'accounts';
    return 'roles';
  }, [tabParam, canReadAccounts, canReadRoles]);

  const setActiveTab = (next: AccountsSubTab) => {
    setSearchParams({ tab: next }, { replace: true });
  };

  const [admins, setAdmins] = useState<AdminListItem[]>([]);
  const [adminsLoading, setAdminsLoading] = useState(canReadAccounts);
  const [adminsLoadedOnce, setAdminsLoadedOnce] = useState(false);

  const [roles, setRoles] = useState<RoleItem[]>([]);
  const [rolesLoading, setRolesLoading] = useState(true);
  const [rolesLoadedOnce, setRolesLoadedOnce] = useState(false);

  const [searchQuery, setSearchQuery] = useState('');
  const [roleFilter, setRoleFilter] = useState('');
  const [density, setDensity] = useState<TableDensity>('comfortable');

  const [editingAdmin, setEditingAdmin] = useState<AdminListItem | null>(null);
  const [isModalOpen, setIsModalOpen] = useState(false);
  const [adminForm, setAdminForm] = useState({
    username: '',
    password: '',
    nickname: '',
    role_id: '',
    status: 1,
  });
  const [accountError, setAccountError] = useState('');
  const [submitting, setSubmitting] = useState(false);

  const fetchRoles = useCallback(async () => {
    setRolesLoading(true);
    try {
      const data = await authApi.listRoles();
      setRoles(Array.isArray(data) ? data : []);
      setRolesLoadedOnce(true);
    } catch (err: unknown) {
      window.showToast?.((err instanceof Error ? err.message : '') || '加载角色列表失败', 'error');
      setRolesLoadedOnce(true);
    } finally {
      setRolesLoading(false);
    }
  }, []);

  const fetchAdmins = useCallback(async () => {
    if (!canReadAccounts) return;
    setAdminsLoading(true);
    try {
      const data = await authApi.listAdmins();
      setAdmins(Array.isArray(data) ? data : []);
      setAdminsLoadedOnce(true);
    } catch (err: unknown) {
      window.showToast?.((err instanceof Error ? err.message : '') || '加载管理员账号列表失败', 'error');
      setAdminsLoadedOnce(true);
    } finally {
      setAdminsLoading(false);
    }
  }, [canReadAccounts]);

  useEffect(() => {
    void fetchRoles();
    if (canReadAccounts) {
      void fetchAdmins();
    }
  }, [fetchRoles, fetchAdmins, canReadAccounts]);

  const activeRoles = useMemo(() => roles.filter((r) => r.status === 1), [roles]);

  const defaultCreateRoleId = useMemo(() => {
    const preferred = activeRoles.find((r) => !r.is_super_admin) || activeRoles[0];
    return preferred ? String(preferred.id) : '';
  }, [activeRoles]);

  const handleOpenCreate = () => {
    setEditingAdmin(null);
    setAdminForm({
      username: '',
      password: '',
      nickname: '',
      role_id: defaultCreateRoleId,
      status: 1,
    });
    setAccountError('');
    setIsModalOpen(true);
  };

  const handleCloseModal = () => {
    if (submitting) return;
    setIsModalOpen(false);
    setEditingAdmin(null);
    setAdminForm({ username: '', password: '', nickname: '', role_id: defaultCreateRoleId, status: 1 });
    setAccountError('');
  };

  const handleAdminFormSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!canManage || submitting) return;

    const parsedRoleId = Number(adminForm.role_id);
    if (!parsedRoleId) {
      setAccountError('请选择有效的岗位角色');
      return;
    }

    setAccountError('');
    setSubmitting(true);
    try {
      if (editingAdmin) {
        const payload: {
          nickname: string;
          role_id: number;
          status: number;
          password?: string;
        } = {
          nickname: adminForm.nickname.trim(),
          role_id: parsedRoleId,
          status: adminForm.status,
        };
        if (adminForm.password) {
          payload.password = adminForm.password;
        }
        await authApi.updateAdmin(editingAdmin.id, payload);
        window.showToast?.(`管理员账号「${editingAdmin.username}」更新成功。`, 'success');
      } else {
        await authApi.createAdmin({
          username: adminForm.username.trim(),
          password: adminForm.password,
          nickname: adminForm.nickname.trim(),
          role_id: parsedRoleId,
        });
        window.showToast?.(`管理员账号「${adminForm.username.trim()}」开通成功。`, 'success');
      }
      setIsModalOpen(false);
      setEditingAdmin(null);
      setAdminForm({ username: '', password: '', nickname: '', role_id: defaultCreateRoleId, status: 1 });
      setAccountError('');
      void fetchAdmins();
      void fetchRoles();
    } catch (err: unknown) {
      setAccountError((err instanceof Error ? err.message : '') || '操作失败，请重试');
    } finally {
      setSubmitting(false);
    }
  };

  const handleEditClick = (item: AdminListItem) => {
    setEditingAdmin(item);
    setAdminForm({
      username: item.username,
      password: '',
      nickname: item.nickname,
      role_id: String(item.role_id),
      status: item.status ?? 1,
    });
    setAccountError('');
    setIsModalOpen(true);
  };

  const handleToggleAdminStatus = (item: AdminListItem) => {
    if (!canManage || item.id === currentAdmin?.id) return;
    const nextStatus = item.status === 1 ? 2 : 1;
    const run = async () => {
      try {
        await authApi.updateAdmin(item.id, { status: nextStatus });
        window.showToast?.(
          `管理员账号「${item.username}」已${nextStatus === 1 ? '启用' : '禁用'}。`,
          'success',
        );
        void fetchAdmins();
      } catch (err: unknown) {
        window.showToast?.((err instanceof Error ? err.message : '') || '更新账号状态失败', 'error');
      }
    };

    if (nextStatus === 2 && window.showConfirm) {
      window.showConfirm(
        `确定要禁用管理员账号「${item.username}」吗？禁用后该账号所有在途请求将立即失效。`,
        () => {
          void run();
        },
      );
      return;
    }
    void run();
  };

  const handleDeleteAdmin = (item: AdminListItem) => {
    if (!canManage || item.id === currentAdmin?.id) return;
    window.showConfirm?.(`您确定要永久删除管理员账号「${item.username}」吗？此操作不可撤回！`, async () => {
      try {
        await authApi.deleteAdmin(item.id);
        window.showToast?.(`管理员账号「${item.username}」删除成功。`, 'success');
        void fetchAdmins();
        void fetchRoles();
      } catch (err: unknown) {
        window.showToast?.((err instanceof Error ? err.message : '') || '删除失败', 'error');
      }
    });
  };

  const filteredAdmins = useMemo(() => {
    const q = searchQuery.trim().toLowerCase();
    return admins.filter((a) => {
      const matchQ =
        !q ||
        a.username.toLowerCase().includes(q) ||
        a.nickname.toLowerCase().includes(q) ||
        (a.role_name || '').toLowerCase().includes(q) ||
        (a.role || '').toLowerCase().includes(q);
      const matchRole = !roleFilter || String(a.role_id) === roleFilter;
      return matchQ && matchRole;
    });
  }, [admins, searchQuery, roleFilter]);

  const roleFilterOptions = useMemo(
    () => [
      { value: '', label: '全部角色' },
      ...roles.map((r) => ({ value: String(r.id), label: r.name })),
    ],
    [roles],
  );

  const modalRoleOptions = useMemo(() => {
    const list = roles.filter(
      (r) => r.status === 1 || (editingAdmin && r.id === editingAdmin.role_id),
    );
    return list.map((r) => ({
      value: String(r.id),
      label: `${r.name} (${r.code})${r.status !== 1 ? ' [已禁用]' : ''}`,
    }));
  }, [roles, editingAdmin]);

  const isEditingSelf = Boolean(editingAdmin && currentAdmin && editingAdmin.id === currentAdmin.id);
  const accountColSpan = canManage ? 6 : 5;

  return (
    <div className="space-y-4 animate-fade-in">
      {/* Dual-Tab Header */}
      <div className="border-b border-[var(--cms-border)] flex flex-wrap items-center justify-between gap-3">
        <div role="tablist" aria-label="账号与权限管理切换" className="flex items-center gap-6 overflow-x-auto">
          {canReadAccounts && (
            <button
              type="button"
              role="tab"
              aria-selected={activeTab === 'accounts'}
              onClick={() => setActiveTab('accounts')}
              className={`focus-ring pb-2.5 text-sm font-medium whitespace-nowrap cursor-pointer inline-flex items-center gap-2 transition-colors ${
                activeTab === 'accounts'
                  ? 'text-[var(--cms-fg)]'
                  : 'text-[var(--cms-muted-fg)] hover:text-[var(--cms-fg)]'
              }`}
            >
              <Users size={14} aria-hidden="true" />
              <span>账号管理</span>
            </button>
          )}
          {canReadRoles && (
            <button
              type="button"
              role="tab"
              aria-selected={activeTab === 'roles'}
              onClick={() => setActiveTab('roles')}
              className={`focus-ring pb-2.5 text-sm font-medium whitespace-nowrap cursor-pointer inline-flex items-center gap-2 transition-colors ${
                activeTab === 'roles'
                  ? 'text-[var(--cms-fg)]'
                  : 'text-[var(--cms-muted-fg)] hover:text-[var(--cms-fg)]'
              }`}
            >
              <Shield size={14} aria-hidden="true" />
              <span>角色与权限</span>
            </button>
          )}
        </div>

        {!canManage && (
          <div className="pb-1.5">
            <StatusDot tone="muted" label="只读查看模式（仅超级管理员可修改）" />
          </div>
        )}
      </div>

      {activeTab === 'roles' && canReadRoles ? (
        <RolesPanel
          roles={roles}
          loading={rolesLoading}
          hasLoadedOnce={rolesLoadedOnce}
          canManage={canManage}
          onRefresh={() => {
            void fetchRoles();
            if (canReadAccounts) void fetchAdmins();
          }}
        />
      ) : (
        <>
          {/* Unified Borderless Filter Strip */}
          <search className="flex flex-wrap items-center justify-between gap-2.5">
            <div className="flex flex-wrap items-center gap-2 flex-1 min-w-[260px]">
              <SearchInput
                className="w-full sm:w-72"
                ariaLabel="搜索管理员账号或昵称"
                placeholder="搜索用户名或员工昵称..."
                value={searchQuery}
                onChange={setSearchQuery}
              />
              <CustomSelect
                aria-label="筛选岗位角色"
                width="160px"
                value={roleFilter}
                onChange={setRoleFilter}
                options={roleFilterOptions}
              />
            </div>

            <div className="flex flex-wrap items-center gap-2 text-xs text-[var(--cms-muted-fg)]">
              <span className="tabular-nums">共 {filteredAdmins.length} 个账号</span>
              <ViewOptionsPopover density={density} onDensityChange={setDensity} />
              {canManage && (
                <button
                  type="button"
                  onClick={handleOpenCreate}
                  className="btn-primary h-8 px-3.5 text-xs"
                >
                  <Plus size={14} aria-hidden="true" />
                  <span>新建账号</span>
                </button>
              )}
            </div>
          </search>

          <TableRegion ariaLabel="后台管理员账号列表">
            <table className="cms-table min-w-[740px]" data-density={density}>
              <thead>
                <tr>
                  <th scope="col">用户名</th>
                  <th scope="col">员工昵称</th>
                  <th scope="col">岗位角色</th>
                  <th scope="col">状态</th>
                  <th scope="col">创建时间</th>
                  {canManage && (
                    <th scope="col" className="text-right w-px whitespace-nowrap">
                      操作
                    </th>
                  )}
                </tr>
              </thead>
              <tbody className={adminsLoading && adminsLoadedOnce ? 'opacity-65 transition-opacity' : ''}>
                {!adminsLoadedOnce && adminsLoading ? (
                  <SkeletonRows rows={4} cols={accountColSpan} />
                ) : filteredAdmins.length === 0 ? (
                  <tr>
                    <td colSpan={accountColSpan} className="py-16 text-center text-[var(--cms-muted-fg)]">
                      暂无匹配的管理员账号
                    </td>
                  </tr>
                ) : (
                  filteredAdmins.map((item) => {
                    const isSelf = Boolean(currentAdmin && item.id === currentAdmin.id);
                    return (
                      <tr key={item.id}>
                        <td className="text-[var(--cms-fg)]">
                          <span>{item.username}</span>
                          {isSelf && (
                            <span className="ml-1.5 px-1.5 py-0.5 rounded bg-[var(--cms-secondary)] text-[10px] text-[var(--cms-muted-fg)]">
                              当前登录
                            </span>
                          )}
                        </td>
                        <td className="text-[var(--cms-fg)]">{item.nickname}</td>
                        <td>
                          <StatusDot
                            tone={item.is_super_admin ? 'error' : 'primary'}
                            label={item.role_name || item.role}
                          />
                        </td>
                        <td>
                          <StatusDot
                            tone={item.status === 1 ? 'success' : 'muted'}
                            label={item.status === 1 ? '正常启用' : '已禁用'}
                          />
                        </td>
                        <td className="tabular-nums text-[var(--cms-muted-fg)] whitespace-nowrap">
                          {item.created_at ? new Date(item.created_at).toLocaleString() : '-'}
                        </td>
                        {canManage && (
                          <td className="text-right whitespace-nowrap">
                            <div className="inline-flex items-center justify-end gap-0.5 whitespace-nowrap">
                              <button
                                type="button"
                                onClick={() => handleEditClick(item)}
                                className="btn-detail whitespace-nowrap"
                              >
                                编辑
                              </button>
                              <button
                                type="button"
                                disabled={isSelf}
                                title={isSelf ? '不可禁用当前登录账号' : undefined}
                                onClick={() => handleToggleAdminStatus(item)}
                                className="btn-ghost whitespace-nowrap disabled:opacity-40 disabled:cursor-not-allowed"
                              >
                                {item.status === 1 ? '禁用' : '启用'}
                              </button>
                              <RowActionMenu
                                ariaLabel={`管理员 ${item.username} 更多操作`}
                                items={[
                                  {
                                    label: '删除账号',
                                    tone: 'danger',
                                    disabled: isSelf,
                                    onClick: () => handleDeleteAdmin(item),
                                  },
                                ]}
                              />
                            </div>
                          </td>
                        )}
                      </tr>
                    );
                  })
                )}
              </tbody>
            </table>
          </TableRegion>

          {canManage && (
            <CmsDialog
              open={isModalOpen}
              onClose={handleCloseModal}
              variant="drawer"
              title={editingAdmin ? '编辑管理员账号' : '开通后台管理账号 (RBAC)'}
              size="md"
              closedBy={submitting ? 'none' : 'any'}
              closeDisabled={submitting}
              footer={
                <>
                  <button type="button" className="btn-secondary" disabled={submitting} onClick={handleCloseModal}>
                    取消
                  </button>
                  <button type="submit" form="admin-account-form" disabled={submitting} className="btn-primary">
                    {submitting ? '提交中...' : editingAdmin ? '保存修改' : '确认开通'}
                  </button>
                </>
              }
            >
              <form id="admin-account-form" onSubmit={handleAdminFormSubmit} className="space-y-4 text-xs">
                {accountError && (
                  <div
                    role="alert"
                    className="p-3 rounded-xl border font-medium"
                    style={{
                      backgroundColor: 'color-mix(in srgb, var(--cms-error) 10%, transparent)',
                      borderColor: 'color-mix(in srgb, var(--cms-error) 25%, transparent)',
                      color: 'var(--cms-error)',
                    }}
                  >
                    {accountError}
                  </div>
                )}

                <label className="block space-y-1.5">
                  <span className="block font-medium text-[var(--cms-muted-fg)]">员工账号（至少 4 位）</span>
                  <input
                    type="text"
                    autoFocus={!editingAdmin}
                    className="input-field font-mono-code"
                    placeholder="请输入员工账号"
                    minLength={editingAdmin ? undefined : 4}
                    value={adminForm.username}
                    onChange={(e) => setAdminForm({ ...adminForm, username: e.target.value })}
                    required
                    disabled={Boolean(editingAdmin)}
                  />
                </label>

                <label className="block space-y-1.5">
                  <span className="block font-medium text-[var(--cms-muted-fg)]">
                    {editingAdmin ? '新登录密码（留空则不修改，填写需至少 6 位）' : '登录密码（至少 6 位）'}
                  </span>
                  <input
                    type="password"
                    className="input-field"
                    placeholder={editingAdmin ? '留空不修改' : '设置初始登录密码'}
                    minLength={adminForm.password || !editingAdmin ? 6 : undefined}
                    value={adminForm.password}
                    onChange={(e) => setAdminForm({ ...adminForm, password: e.target.value })}
                    required={!editingAdmin}
                  />
                </label>

                <label className="block space-y-1.5">
                  <span className="block font-medium text-[var(--cms-muted-fg)]">员工昵称</span>
                  <input
                    type="text"
                    className="input-field"
                    placeholder="请输入员工昵称"
                    value={adminForm.nickname}
                    onChange={(e) => setAdminForm({ ...adminForm, nickname: e.target.value })}
                    required
                  />
                </label>

                <label className="block space-y-1.5">
                  <span className="block font-medium text-[var(--cms-muted-fg)]">职员岗位指派 (RBAC 角色)</span>
                  <CustomSelect
                    aria-label="职员岗位指派"
                    options={modalRoleOptions}
                    value={adminForm.role_id}
                    onChange={(val) => setAdminForm({ ...adminForm, role_id: val })}
                    disabled={isEditingSelf}
                    width="100%"
                  />
                  {isEditingSelf && (
                    <span className="block text-[11px] text-[var(--cms-muted-fg)]">
                      当前登录账号不可自行修改所属角色
                    </span>
                  )}
                </label>

                {editingAdmin && (
                  <label className="block space-y-1.5">
                    <span className="block font-medium text-[var(--cms-muted-fg)]">账号状态</span>
                    <CustomSelect
                      aria-label="账号状态"
                      options={[
                        { value: '1', label: '正常启用' },
                        { value: '2', label: '已禁用' },
                      ]}
                      value={String(adminForm.status)}
                      onChange={(val) => setAdminForm({ ...adminForm, status: Number(val) })}
                      disabled={isEditingSelf}
                      width="100%"
                    />
                    {isEditingSelf && (
                      <span className="block text-[11px] text-[var(--cms-muted-fg)]">
                        当前登录账号不可禁用自己
                      </span>
                    )}
                  </label>
                )}
              </form>
            </CmsDialog>
          )}
        </>
      )}
    </div>
  );
}
