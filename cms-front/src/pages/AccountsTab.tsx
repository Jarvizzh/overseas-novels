import { useEffect, useMemo, useState } from 'react';
import { Plus } from 'lucide-react';
import { apiRequest } from '../utils/api';
import CustomSelect from '../components/CustomSelect';
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

interface AdminUserResponse {
  id: string;
  username: string;
  nickname: string;
  role: string;
  status: number;
  created_at: string;
}

const ROLE_LABELS: Record<string, string> = {
  SuperAdmin: '超级管理员',
  Admin: '管理员',
  Editor: '编辑',
  MediaBuyer: '投手',
  Finance: '财务',
  Support: '客服',
};

const ROLE_TONES: Record<string, 'error' | 'primary' | 'success' | 'warning' | 'muted'> = {
  SuperAdmin: 'error',
  Admin: 'primary',
  Editor: 'primary',
  MediaBuyer: 'success',
  Finance: 'warning',
  Support: 'muted',
};

export default function AccountsTab() {
  const [admins, setAdmins] = useState<AdminUserResponse[]>([]);
  const [loading, setLoading] = useState(true);
  const [hasLoadedOnce, setHasLoadedOnce] = useState(false);
  const [searchQuery, setSearchQuery] = useState('');
  const [roleFilter, setRoleFilter] = useState('');
  const [density, setDensity] = useState<TableDensity>('comfortable');

  const [editingAdmin, setEditingAdmin] = useState<AdminUserResponse | null>(null);
  const [isModalOpen, setIsModalOpen] = useState(false);
  const [adminForm, setAdminForm] = useState({
    username: '',
    password: '',
    nickname: '',
    role: 'Editor',
  });
  const [accountError, setAccountError] = useState('');
  const [submitting, setSubmitting] = useState(false);

  const fetchAdmins = async () => {
    setLoading(true);
    try {
      const data = await apiRequest('GET', '/auth/admins');
      setAdmins(data || []);
      setHasLoadedOnce(true);
    } catch (err: unknown) {
      window.showToast?.((err instanceof Error ? err.message : '') || '加载管理员账号列表失败', 'error');
      setHasLoadedOnce(true);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void fetchAdmins();
  }, []);

  const handleCloseModal = () => {
    if (submitting) return;
    setIsModalOpen(false);
    setEditingAdmin(null);
    setAdminForm({ username: '', password: '', nickname: '', role: 'Editor' });
    setAccountError('');
  };

  const handleAdminFormSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (submitting) return;
    setAccountError('');
    setSubmitting(true);
    try {
      if (editingAdmin) {
        await apiRequest('PUT', `/auth/admins/${editingAdmin.id}`, {
          nickname: adminForm.nickname,
          role: adminForm.role,
          password: adminForm.password,
        });
        window.showToast?.(`管理员账号「${editingAdmin.username}」更新成功。`, 'success');
      } else {
        await apiRequest('POST', '/auth/register', adminForm);
        window.showToast?.(`管理员账号「${adminForm.username}」开通成功。`, 'success');
      }
      setIsModalOpen(false);
      setEditingAdmin(null);
      setAdminForm({ username: '', password: '', nickname: '', role: 'Editor' });
      setAccountError('');
      void fetchAdmins();
    } catch (err: unknown) {
      setAccountError((err instanceof Error ? err.message : '') || '操作失败，请重试');
    } finally {
      setSubmitting(false);
    }
  };

  const handleEditClick = (admin: AdminUserResponse) => {
    setEditingAdmin(admin);
    setAdminForm({
      username: admin.username,
      password: '',
      nickname: admin.nickname,
      role: admin.role,
    });
    setAccountError('');
    setIsModalOpen(true);
  };

  const handleDeleteAdmin = (id: string, username: string) => {
    window.showConfirm?.(`您确定要永久删除管理员账号「${username}」吗？此操作不可撤回！`, async () => {
      try {
        await apiRequest('DELETE', `/auth/admins/${id}`);
        window.showToast?.(`管理员账号「${username}」删除成功。`, 'success');
        void fetchAdmins();
      } catch (err: unknown) {
        window.showToast?.((err instanceof Error ? err.message : '') || '删除失败', 'error');
      }
    });
  };

  const filteredAdmins = useMemo(() => {
    const q = searchQuery.trim().toLowerCase();
    return admins.filter((a) => {
      const matchQ = !q || a.username.toLowerCase().includes(q) || a.nickname.toLowerCase().includes(q);
      const matchRole = !roleFilter || a.role === roleFilter;
      return matchQ && matchRole;
    });
  }, [admins, searchQuery, roleFilter]);

  return (
    <div className="space-y-4 animate-fade-in">
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
            width="150px"
            value={roleFilter}
            onChange={setRoleFilter}
            options={[
              { value: '', label: '全部角色' },
              { value: 'SuperAdmin', label: '超级管理员' },
              { value: 'Admin', label: '管理员' },
              { value: 'Editor', label: '编辑' },
              { value: 'MediaBuyer', label: '投手' },
              { value: 'Finance', label: '财务' },
              { value: 'Support', label: '客服' },
            ]}
          />
        </div>

        <div className="flex flex-wrap items-center gap-2 text-xs text-[var(--cms-muted-fg)]">
          <span className="tabular-nums">共 {filteredAdmins.length} 个账号</span>
          <ViewOptionsPopover density={density} onDensityChange={setDensity} />
          <button
            type="button"
            onClick={() => {
              setEditingAdmin(null);
              setAdminForm({ username: '', password: '', nickname: '', role: 'Editor' });
              setAccountError('');
              setIsModalOpen(true);
            }}
            className="btn-primary h-8 px-3.5 text-xs"
          >
            <Plus size={14} aria-hidden="true" />
            <span>新建账号</span>
          </button>
        </div>
      </search>

      <TableRegion ariaLabel="后台管理员账号列表">
        <table className="cms-table" data-density={density}>
          <thead>
            <tr>
              <th scope="col">用户名</th>
              <th scope="col">员工昵称</th>
              <th scope="col">岗位角色</th>
              <th scope="col" className="text-right w-px whitespace-nowrap">
                操作
              </th>
            </tr>
          </thead>
          <tbody className={loading && hasLoadedOnce ? 'opacity-65 transition-opacity' : ''}>
            {!hasLoadedOnce && loading ? (
              <SkeletonRows rows={4} cols={4} />
            ) : filteredAdmins.length === 0 ? (
              <tr>
                <td colSpan={4} className="py-16 text-center text-[var(--cms-muted-fg)]">
                  暂无匹配的管理员账号
                </td>
              </tr>
            ) : (
              filteredAdmins.map((admin) => (
                <tr key={admin.id}>
                  <td className="text-[var(--cms-fg)]">{admin.username}</td>
                  <td className="text-[var(--cms-fg)]">{admin.nickname}</td>
                  <td>
                    <StatusDot
                      tone={ROLE_TONES[admin.role] || 'muted'}
                      label={ROLE_LABELS[admin.role] || admin.role}
                    />
                  </td>
                  <td className="text-right whitespace-nowrap">
                    <div className="inline-flex items-center justify-end gap-0.5 whitespace-nowrap">
                      <button type="button" onClick={() => handleEditClick(admin)} className="btn-detail whitespace-nowrap">
                        编辑
                      </button>
                      <RowActionMenu
                        ariaLabel={`管理员 ${admin.username} 更多操作`}
                        items={[
                          {
                            label: '删除账号',
                            tone: 'danger',
                            onClick: () => handleDeleteAdmin(admin.id, admin.username),
                          },
                        ]}
                      />
                    </div>
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </TableRegion>

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
              options={[
                { value: 'Admin', label: '管理员 (Admin)' },
                { value: 'Editor', label: '编辑 (Editor)' },
                { value: 'MediaBuyer', label: '投手 (MediaBuyer)' },
                { value: 'Finance', label: '财务 (Finance)' },
                { value: 'Support', label: '客服 (Support)' },
                { value: 'SuperAdmin', label: '超级管理员 (SuperAdmin)' },
              ]}
              value={adminForm.role}
              onChange={(val) => setAdminForm({ ...adminForm, role: val })}
              width="100%"
            />
          </label>
        </form>
      </CmsDialog>
    </div>
  );
}
