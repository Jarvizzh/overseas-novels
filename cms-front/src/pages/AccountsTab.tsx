import { useState, useEffect } from 'react';
import { createPortal } from 'react-dom';
import { apiRequest } from '../utils/api';
import {
  ShieldCheck,
  Plus,
  Edit2,
  Trash2,
  Lock,
  X
} from 'lucide-react';
import CustomSelect from '../components/CustomSelect';

interface AdminUserResponse {
  id: string;
  username: string;
  nickname: string;
  role: string;
  status: string;
  created_at: string;
}

export default function AccountsTab() {
  const [admins, setAdmins] = useState<AdminUserResponse[]>([]);
  const [editingAdmin, setEditingAdmin] = useState<AdminUserResponse | null>(null);
  const [isModalOpen, setIsModalOpen] = useState(false);
  const [adminForm, setAdminForm] = useState({
    username: '',
    password: '',
    nickname: '',
    role: 'Editor'
  });
  const [accountError, setAccountError] = useState('');

  const fetchAdmins = async () => {
    try {
      const data = await apiRequest('GET', '/auth/admins');
      setAdmins(data || []);
    } catch (err: any) {
      console.error('Failed to load admin list:', err);
    }
  };

  useEffect(() => {
    fetchAdmins();
  }, []);

  const handleAdminFormSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setAccountError('');
    try {
      if (editingAdmin) {
        await apiRequest('PUT', `/auth/admins/${editingAdmin.id}`, {
          nickname: adminForm.nickname,
          role: adminForm.role,
          password: adminForm.password
        });
        window.showToast?.(`管理员账号“${editingAdmin.username}”更新成功。`, 'success');
        setIsModalOpen(false);
        setEditingAdmin(null);
        setAdminForm({ username: '', password: '', nickname: '', role: 'Editor' });
      } else {
        await apiRequest('POST', '/auth/register', adminForm);
        window.showToast?.(`管理员账号“${adminForm.username}”开通成功。`, 'success');
        setIsModalOpen(false);
        setAdminForm({ username: '', password: '', nickname: '', role: 'Editor' });
      }
      fetchAdmins();
    } catch (err: any) {
      setAccountError(err.message || '操作失败，请重试');
    }
  };

  const handleEditClick = (admin: AdminUserResponse) => {
    setEditingAdmin(admin);
    setAdminForm({
      username: admin.username,
      password: '',
      nickname: admin.nickname,
      role: admin.role
    });
    setAccountError('');
    setIsModalOpen(true);
  };

  const handleDeleteAdmin = async (id: string, username: string) => {
    window.showConfirm?.(`您确定要永久删除管理员账号 “${username}” 吗？此操作不可撤回！`, async () => {
      try {
        await apiRequest('DELETE', `/auth/admins/${id}`);
        window.showToast?.(`管理员账号 “${username}” 删除成功。`, 'success');
        fetchAdmins();
      } catch (err: any) {
        window.showToast?.(err.message || '删除失败', 'error');
      }
    });
  };

  const handleCreateClick = () => {
    setEditingAdmin(null);
    setAdminForm({ username: '', password: '', nickname: '', role: 'Editor' });
    setAccountError('');
    setIsModalOpen(true);
  };

  const handleCloseModal = () => {
    setIsModalOpen(false);
    setEditingAdmin(null);
    setAdminForm({ username: '', password: '', nickname: '', role: 'Editor' });
  };

  return (
    <div className="animate-fade-in" style={{ padding: '16px 24px 24px 24px' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '24px' }}>
        <h1 style={{ fontSize: '1.5rem', fontWeight: 700 }} className="gradient-text">
          账号管理
        </h1>
        <button
          onClick={handleCreateClick}
          className="btn-primary"
          style={{ display: 'flex', gap: '6px', padding: '6px 12px', fontSize: '0.8rem' }}
        >
          <Plus size={14} /> 新建账号
        </button>
      </div>

      <div className="glass-panel animate-fade-in" style={{ padding: '24px' }}>
        <h3 style={{ fontSize: '1.2rem', marginBottom: '18px', display: 'flex', alignItems: 'center', gap: '8px' }}>
          <ShieldCheck size={18} style={{ color: 'hsl(var(--accent-green))' }} />
          账号列表
        </h3>
        <div style={{ overflowX: 'auto' }}>
          <table style={{ width: '100%', borderCollapse: 'collapse', textAlign: 'left', fontSize: '0.85rem' }}>
            <thead>
              <tr style={{ borderBottom: '1px solid hsl(var(--border))', color: 'hsl(var(--text-secondary))' }}>
                <th style={{ padding: '10px 8px' }}>用户名</th>
                <th style={{ padding: '10px 8px' }}>昵称</th>
                <th style={{ padding: '10px 8px' }}>角色</th>
                <th style={{ padding: '10px 8px' }}>操作</th>
              </tr>
            </thead>
            <tbody>
              {admins.map((admin) => (
                <tr key={admin.id} style={{ borderBottom: '1px solid hsl(var(--border) / 0.5)', fontSize: '0.9rem', color: 'hsl(var(--text-secondary))' }}>
                  <td style={{ padding: '12px 8px', fontWeight: 500 }}>{admin.username}</td>
                  <td style={{ padding: '12px 8px' }}>{admin.nickname}</td>
                  <td style={{ padding: '12px 8px' }}>
                    <span className={`badge ${
                      admin.role === 'SuperAdmin' ? 'badge-red' :
                      admin.role === 'Admin' ? 'badge-blue' :
                      admin.role === 'Editor' ? 'badge-violet' :
                      admin.role === 'MediaBuyer' ? 'badge-green' :
                      admin.role === 'Finance' ? 'badge-amber' : 'badge-orange'
                    }`}>
                      {
                        admin.role === 'SuperAdmin' ? '超级管理员' :
                        admin.role === 'Admin' ? '管理员' :
                        admin.role === 'Editor' ? '编辑' :
                        admin.role === 'MediaBuyer' ? '投手' :
                        admin.role === 'Finance' ? '财务' :
                        admin.role === 'Support' ? '客服' : admin.role
                      }
                    </span>
                  </td>
                  <td style={{ padding: '12px 8px' }}>
                    <div style={{ display: 'flex', gap: '8px' }}>
                      <button onClick={() => handleEditClick(admin)} className="btn-secondary" style={{ padding: '4px 8px', fontSize: '0.75rem', borderRadius: '4px', gap: '4px' }}>
                        <Edit2 size={12} /> 编辑
                      </button>
                      <button onClick={() => handleDeleteAdmin(admin.id, admin.username)} className="btn-secondary" style={{ padding: '4px 8px', fontSize: '0.75rem', borderRadius: '4px', gap: '4px', color: '#f87171', borderColor: 'rgba(239, 68, 68, 0.15)' }}>
                        <Trash2 size={12} /> 删除
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>

      {isModalOpen && createPortal(
        <div style={{
          position: 'fixed',
          top: 0,
          left: 0,
          right: 0,
          bottom: 0,
          backgroundColor: 'rgba(15, 23, 42, 0.6)',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          zIndex: 9999
        }} className="animate-fade-in">
          <div className="glass-panel" style={{
            width: '100%',
            maxWidth: '500px',
            padding: '24px',
            position: 'relative',
            backgroundColor: 'hsl(var(--bg-surface))',
            borderRadius: '12px',
            boxShadow: 'var(--shadow-lg)',
            border: '1px solid hsl(var(--border))',
            color: 'hsl(var(--text-primary))'
          }}>
            <button
              onClick={handleCloseModal}
              style={{
                position: 'absolute',
                top: '16px',
                right: '16px',
                background: 'none',
                border: 'none',
                cursor: 'pointer',
                color: 'hsl(var(--text-muted))'
              }}
            >
              <X size={18} />
            </button>

            <h3 style={{ fontSize: '1.2rem', marginBottom: '18px', display: 'flex', alignItems: 'center', gap: '8px', color: 'hsl(var(--text-primary))', fontWeight: 600 }}>
              <Lock size={18} style={{ color: 'hsl(var(--primary))' }} />
              {editingAdmin ? '编辑管理员账号' : '开通后台职员管理账号 (RBAC)'}
            </h3>

            {accountError && (
              <div style={{ backgroundColor: 'rgba(239, 68, 68, 0.1)', border: '1px solid rgba(239, 68, 68, 0.2)', color: '#f87171', padding: '10px', borderRadius: '8px', fontSize: '0.8rem', marginBottom: '14px' }}>
                {accountError}
              </div>
            )}

            <form onSubmit={handleAdminFormSubmit}>
              <div style={{ display: 'flex', flexDirection: 'column', gap: '14px', marginBottom: '20px' }}>
                <div>
                  <label style={{ display: 'block', fontSize: '0.8rem', color: 'hsl(var(--text-secondary))', marginBottom: '6px', fontWeight: 500 }}>员工账号</label>
                  <input
                    type="text"
                    className="input-field"
                    placeholder="请输入员工账号"
                    value={adminForm.username}
                    onChange={(e) => setAdminForm({ ...adminForm, username: e.target.value })}
                    required
                    disabled={!!editingAdmin}
                    style={{
                      width: '100%',
                      backgroundColor: 'hsl(var(--bg-card))',
                      color: 'hsl(var(--text-primary))',
                      borderColor: 'hsl(var(--border))'
                    }}
                  />
                </div>
                <div>
                  <label style={{ display: 'block', fontSize: '0.8rem', color: 'hsl(var(--text-secondary))', marginBottom: '6px', fontWeight: 500 }}>
                    {editingAdmin ? '新登录密码 (留空则不修改)' : '登录密码'}
                  </label>
                  <input
                    type="password"
                    className="input-field"
                    placeholder={editingAdmin ? "留空不修改" : "设置初始登录密码"}
                    value={adminForm.password}
                    onChange={(e) => setAdminForm({ ...adminForm, password: e.target.value })}
                    required={!editingAdmin}
                    style={{
                      width: '100%',
                      backgroundColor: 'hsl(var(--bg-card))',
                      color: 'hsl(var(--text-primary))',
                      borderColor: 'hsl(var(--border))'
                    }}
                  />
                </div>
                <div>
                  <label style={{ display: 'block', fontSize: '0.8rem', color: 'hsl(var(--text-secondary))', marginBottom: '6px', fontWeight: 500 }}>员工昵称</label>
                  <input
                    type="text"
                    className="input-field"
                    placeholder="请输入员工昵称"
                    value={adminForm.nickname}
                    onChange={(e) => setAdminForm({ ...adminForm, nickname: e.target.value })}
                    required
                    style={{
                      width: '100%',
                      backgroundColor: 'hsl(var(--bg-card))',
                      color: 'hsl(var(--text-primary))',
                      borderColor: 'hsl(var(--border))'
                    }}
                  />
                </div>
                <div>
                  <label style={{ display: 'block', fontSize: '0.8rem', color: 'hsl(var(--text-secondary))', marginBottom: '6px', fontWeight: 500 }}>职员岗位指派 (RBAC 角色)</label>
                  <CustomSelect
                    options={[
                      { value: 'Admin', label: '管理员 (Admin)' },
                      { value: 'Editor', label: '编辑 (Editor)' },
                      { value: 'MediaBuyer', label: '投手 (MediaBuyer)' },
                      { value: 'Finance', label: '财务 (Finance)' },
                      { value: 'Support', label: '客服 (Support)' },
                      { value: 'SuperAdmin', label: '超级管理员 (SuperAdmin)' }
                    ]}
                    value={adminForm.role}
                    onChange={(val) => setAdminForm({ ...adminForm, role: val })}
                    width="100%"
                  />
                </div>
              </div>

              <div style={{ display: 'flex', gap: '12px' }}>
                <button type="button" className="btn-secondary" style={{ flex: 1, gap: '6px', backgroundColor: 'hsl(var(--bg-card))', color: 'hsl(var(--text-primary))', border: '1px solid hsl(var(--border))' }} onClick={handleCloseModal}>
                  取消
                </button>
                <button type="submit" className="btn-primary" style={{ flex: 1.5, gap: '6px' }}>
                  确认创建
                </button>
              </div>
            </form>
          </div>
        </div>,
        document.body
      )}
    </div>
  );
}
