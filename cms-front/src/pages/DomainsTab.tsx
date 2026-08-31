import { useState, useEffect } from 'react';
import { createPortal } from 'react-dom';
import { domainApi } from '../utils/api';
import type { SystemDomain } from '../utils/api';
import {
  Globe,
  Plus,
  CheckCircle,
  XCircle,
  Trash2,
  ExternalLink,
  ShieldCheck,
  Star,
  RefreshCw,
  Layers,
  Search,
  X
} from 'lucide-react';
import CustomSelect from '../components/CustomSelect';

export default function DomainsTab() {
  const [domains, setDomains] = useState<SystemDomain[]>([]);
  const [loading, setLoading] = useState(true);
  const [searchQuery, setSearchQuery] = useState('');
  const [typeFilter, setTypeFilter] = useState<string>('all');

  // Modal State
  const [isModalOpen, setIsModalOpen] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [formData, setFormData] = useState<{
    name: string;
    domain: string;
    type: 'main' | 'sub';
    is_default: boolean;
  }>({
    name: '',
    domain: '',
    type: 'sub',
    is_default: false
  });

  const fetchDomains = async () => {
    setLoading(true);
    try {
      const data = await domainApi.getDomains();
      setDomains(data || []);
    } catch (err: any) {
      if (window.showToast) {
        window.showToast(`获取域名列表失败: ${err.message}`, 'error');
      }
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchDomains();
  }, []);

  const handleCreateSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!formData.name.trim() || !formData.domain.trim()) {
      if (window.showToast) window.showToast('请填写完整的域名信息', 'error');
      return;
    }

    setSubmitting(true);
    try {
      await domainApi.createDomain(formData);
      if (window.showToast) window.showToast('域名新建成功！', 'success');
      setIsModalOpen(false);
      setFormData({ name: '', domain: '', type: 'sub', is_default: false });
      fetchDomains();
    } catch (err: any) {
      if (window.showToast) window.showToast(`新建域名失败: ${err.message}`, 'error');
    } finally {
      setSubmitting(false);
    }
  };

  const handleToggleStatus = async (domain: SystemDomain) => {
    const nextStatus = domain.status === 1 ? 2 : 1;
    try {
      await domainApi.updateDomainStatus(domain.id, nextStatus);
      if (window.showToast) {
        window.showToast(`已${nextStatus === 1 ? '启用' : '禁用'}域名 ${domain.domain}`, 'success');
      }
      fetchDomains();
    } catch (err: any) {
      if (window.showToast) window.showToast(`操作失败: ${err.message}`, 'error');
    }
  };

  const handleSetDefault = async (domain: SystemDomain) => {
    try {
      await domainApi.setDefaultDomain(domain.id);
      if (window.showToast) {
        window.showToast(`已将 ${domain.domain} 设为默认主域名`, 'success');
      }
      fetchDomains();
    } catch (err: any) {
      if (window.showToast) window.showToast(`设置默认失败: ${err.message}`, 'error');
    }
  };

  const handleDelete = (domain: SystemDomain) => {
    if (domain.is_default) {
      if (window.showToast) window.showToast('默认域名不可删除', 'error');
      return;
    }
    if (window.showConfirm) {
      window.showConfirm(`确定要删除域名 [${domain.name}] (${domain.domain}) 吗？`, async () => {
        try {
          await domainApi.deleteDomain(domain.id);
          if (window.showToast) window.showToast('域名删除成功', 'success');
          fetchDomains();
        } catch (err: any) {
          if (window.showToast) window.showToast(`删除失败: ${err.message}`, 'error');
        }
      });
    }
  };

  // Filtered Domains
  const filteredDomains = domains.filter(d => {
    const matchesSearch =
      d.name.toLowerCase().includes(searchQuery.toLowerCase()) ||
      d.domain.toLowerCase().includes(searchQuery.toLowerCase());
    const matchesType = typeFilter === 'all' || d.type === typeFilter;
    return matchesSearch && matchesType;
  });

  // Statistics
  const totalCount = domains.length;
  const mainCount = domains.filter(d => d.type === 'main').length;
  const activeSubCount = domains.filter(d => d.type === 'sub' && d.status === 1).length;

  return (
    <div style={{ padding: '16px 24px 24px 24px', display: 'flex', flexDirection: 'column', gap: '16px' }}>
      {/* Header Title & Action */}
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <div>
          <h1 style={{ fontSize: '1.5rem', fontWeight: 700 }} className="gradient-text">
            域名管理
          </h1>
        </div>
        <div style={{ display: 'flex', gap: '12px' }}>
          <button
            onClick={fetchDomains}
            className="btn-secondary"
            style={{
              padding: '8px 14px',
              fontSize: '0.8rem',
              display: 'flex',
              alignItems: 'center',
              gap: '6px',
              cursor: 'pointer',
              borderRadius: '8px',
              backgroundColor: 'hsl(var(--bg-card))',
              border: '1px solid hsl(var(--border))'
            }}
          >
            <RefreshCw size={14} className={loading ? 'spin' : ''} />
            刷新
          </button>
          <button
            onClick={() => setIsModalOpen(true)}
            className="btn-primary"
            style={{
              padding: '8px 16px',
              fontSize: '0.85rem',
              fontWeight: 600,
              display: 'flex',
              alignItems: 'center',
              gap: '6px',
              cursor: 'pointer',
              borderRadius: '8px',
              backgroundColor: 'hsl(var(--primary))',
              color: '#ffffff',
              border: 'none',
              boxShadow: 'var(--shadow-neon)'
            }}
          >
            <Plus size={16} />
            新建域名
          </button>
        </div>
      </div>

      {/* Statistics Cards */}
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(220px, 1fr))', gap: '16px' }}>
        <div className="glass-panel" style={{ padding: '16px 20px', display: 'flex', alignItems: 'center', gap: '16px' }}>
          <div style={{
            width: '44px', height: '44px', borderRadius: '12px',
            backgroundColor: 'hsl(var(--primary) / 0.12)', color: 'hsl(var(--primary))',
            display: 'flex', alignItems: 'center', justifyContent: 'center'
          }}>
            <Globe size={22} />
          </div>
          <div>
            <div style={{ fontSize: '0.75rem', color: 'hsl(var(--text-secondary))' }}>总配置域名数</div>
            <div style={{ fontSize: '1.4rem', fontWeight: 700, marginTop: '2px' }}>{totalCount}</div>
          </div>
        </div>

        <div className="glass-panel" style={{ padding: '16px 20px', display: 'flex', alignItems: 'center', gap: '16px' }}>
          <div style={{
            width: '44px', height: '44px', borderRadius: '12px',
            backgroundColor: 'hsl(var(--accent-pink) / 0.12)', color: 'hsl(var(--accent-pink))',
            display: 'flex', alignItems: 'center', justifyContent: 'center'
          }}>
            <Star size={22} />
          </div>
          <div>
            <div style={{ fontSize: '0.75rem', color: 'hsl(var(--text-secondary))' }}>主域名数量</div>
            <div style={{ fontSize: '1.4rem', fontWeight: 700, marginTop: '2px' }}>{mainCount}</div>
          </div>
        </div>

        <div className="glass-panel" style={{ padding: '16px 20px', display: 'flex', alignItems: 'center', gap: '16px' }}>
          <div style={{
            width: '44px', height: '44px', borderRadius: '12px',
            backgroundColor: 'hsl(var(--accent-green) / 0.12)', color: 'hsl(var(--accent-green))',
            display: 'flex', alignItems: 'center', justifyContent: 'center'
          }}>
            <Layers size={22} />
          </div>
          <div>
            <div style={{ fontSize: '0.75rem', color: 'hsl(var(--text-secondary))' }}>活跃子域名数</div>
            <div style={{ fontSize: '1.4rem', fontWeight: 700, marginTop: '2px' }}>{activeSubCount}</div>
          </div>
        </div>
      </div>

      {/* Filter Bar & Table Container */}
      <div className="glass-panel" style={{ padding: '20px', display: 'flex', flexDirection: 'column', gap: '16px' }}>
        {/* Filters */}
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: '16px', flexWrap: 'wrap' }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: '12px', flex: 1, minWidth: '280px' }}>
            <div style={{
              position: 'relative', flex: 1, display: 'flex', alignItems: 'center'
            }}>
              <Search size={16} style={{ position: 'absolute', left: '12px', color: 'hsl(var(--text-muted))' }} />
              <input
                type="text"
                placeholder="搜索域名描述或地址 (例如: sub1.star-novel.com)..."
                value={searchQuery}
                onChange={(e) => setSearchQuery(e.target.value)}
                style={{
                  width: '100%',
                  height: '38px',
                  paddingLeft: '36px',
                  paddingRight: '12px',
                  fontSize: '0.825rem',
                  borderRadius: '8px',
                  border: '1px solid hsl(var(--border))',
                  backgroundColor: 'hsl(var(--bg-card))',
                  color: 'hsl(var(--text-primary))'
                }}
              />
            </div>
            <CustomSelect
              options={[
                { value: 'all', label: '全部类型' },
                { value: 'main', label: '主域名' },
                { value: 'sub', label: '子域名' },
              ]}
              value={typeFilter}
              onChange={(val) => setTypeFilter(val)}
              width="140px"
            />
          </div>
        </div>

        {/* Data Table */}
        <div style={{ overflowX: 'auto', borderRadius: '8px', border: '1px solid hsl(var(--border))' }}>
          <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: '0.825rem', textAlign: 'left' }}>
            <thead>
              <tr style={{ backgroundColor: 'hsl(var(--bg-card))', borderBottom: '1px solid hsl(var(--border))' }}>
                <th style={{ padding: '12px 16px', fontWeight: 600, color: 'hsl(var(--text-secondary))' }}>ID</th>
                <th style={{ padding: '12px 16px', fontWeight: 600, color: 'hsl(var(--text-secondary))' }}>域名说明</th>
                <th style={{ padding: '12px 16px', fontWeight: 600, color: 'hsl(var(--text-secondary))' }}>域名地址 (Domain URL)</th>
                <th style={{ padding: '12px 16px', fontWeight: 600, color: 'hsl(var(--text-secondary))' }}>类型</th>
                <th style={{ padding: '12px 16px', fontWeight: 600, color: 'hsl(var(--text-secondary))' }}>默认标志</th>
                <th style={{ padding: '12px 16px', fontWeight: 600, color: 'hsl(var(--text-secondary))' }}>状态</th>
                <th style={{ padding: '12px 16px', fontWeight: 600, color: 'hsl(var(--text-secondary))' }}>创建时间</th>
                <th style={{ padding: '12px 16px', fontWeight: 600, color: 'hsl(var(--text-secondary))', textAlign: 'right' }}>操作</th>
              </tr>
            </thead>
            <tbody>
              {loading ? (
                <tr>
                  <td colSpan={8} style={{ padding: '32px', textAlign: 'center', color: 'hsl(var(--text-muted))' }}>
                    加载域名列表中...
                  </td>
                </tr>
              ) : filteredDomains.length === 0 ? (
                <tr>
                  <td colSpan={8} style={{ padding: '32px', textAlign: 'center', color: 'hsl(var(--text-muted))' }}>
                    暂无匹配的域名配置
                  </td>
                </tr>
              ) : (
                filteredDomains.map((item) => (
                  <tr
                    key={item.id}
                    style={{
                      borderBottom: '1px solid hsl(var(--border))',
                      transition: 'background-color 0.15s ease'
                    }}
                  >
                    <td style={{ padding: '12px 16px', color: 'hsl(var(--text-muted))' }}>#{item.id}</td>
                    <td style={{ padding: '12px 16px', fontWeight: 600, color: 'hsl(var(--text-primary))' }}>
                      {item.name}
                    </td>
                    <td style={{ padding: '12px 16px' }}>
                      <a
                        href={`https://${item.domain}`}
                        target="_blank"
                        rel="noreferrer"
                        style={{
                          display: 'inline-flex',
                          alignItems: 'center',
                          gap: '4px',
                          color: 'hsl(var(--primary))',
                          fontWeight: 500,
                          textDecoration: 'none'
                        }}
                      >
                        {item.domain}
                        <ExternalLink size={12} />
                      </a>
                    </td>
                    <td style={{ padding: '12px 16px' }}>
                      <span
                        style={{
                          padding: '3px 8px',
                          borderRadius: '6px',
                          fontSize: '0.75rem',
                          fontWeight: 600,
                          backgroundColor: item.type === 'main' ? 'hsl(var(--primary) / 0.15)' : 'hsl(var(--accent-blue) / 0.15)',
                          color: item.type === 'main' ? 'hsl(var(--primary))' : 'hsl(var(--accent-blue))',
                          border: `1px solid ${item.type === 'main' ? 'hsl(var(--primary) / 0.3)' : 'hsl(var(--accent-blue) / 0.3)'}`
                        }}
                      >
                        {item.type === 'main' ? '主域名' : '子域名'}
                      </span>
                    </td>
                    <td style={{ padding: '12px 16px' }}>
                      {item.is_default ? (
                        <span
                          style={{
                            display: 'inline-flex',
                            alignItems: 'center',
                            gap: '4px',
                            padding: '3px 8px',
                            borderRadius: '6px',
                            fontSize: '0.75rem',
                            fontWeight: 600,
                            backgroundColor: 'hsl(var(--accent-pink) / 0.15)',
                            color: 'hsl(var(--accent-pink))',
                            border: '1px solid hsl(var(--accent-pink) / 0.3)'
                          }}
                        >
                          <Star size={12} /> 默认落地页
                        </span>
                      ) : (
                        <span style={{ color: 'hsl(var(--text-muted))', fontSize: '0.75rem' }}>-</span>
                      )}
                    </td>
                    <td style={{ padding: '12px 16px' }}>
                      <span
                        style={{
                          display: 'inline-flex',
                          alignItems: 'center',
                          gap: '4px',
                          padding: '3px 8px',
                          borderRadius: '6px',
                          fontSize: '0.75rem',
                          fontWeight: 500,
                          backgroundColor: item.status === 1 ? 'hsl(var(--accent-green) / 0.15)' : 'hsl(var(--text-muted) / 0.15)',
                          color: item.status === 1 ? 'hsl(var(--accent-green))' : 'hsl(var(--text-muted))'
                        }}
                      >
                        {item.status === 1 ? <CheckCircle size={12} /> : <XCircle size={12} />}
                        {item.status === 1 ? '正常启用' : '已禁用'}
                      </span>
                    </td>
                    <td style={{ padding: '12px 16px', color: 'hsl(var(--text-secondary))', fontSize: '0.775rem' }}>
                      {new Date(item.created_at).toLocaleString()}
                    </td>
                    <td style={{ padding: '12px 16px', textAlign: 'right' }}>
                      <div style={{ display: 'flex', justifyContent: 'flex-end', gap: '8px' }}>
                        {!item.is_default && (
                          <button
                            onClick={() => handleSetDefault(item)}
                            style={{
                              padding: '4px 8px',
                              fontSize: '0.75rem',
                              borderRadius: '6px',
                              border: '1px solid hsl(var(--border))',
                              backgroundColor: 'hsl(var(--bg-card))',
                              color: 'hsl(var(--text-primary))',
                              cursor: 'pointer',
                              display: 'flex',
                              alignItems: 'center',
                              gap: '4px'
                            }}
                            title="设为系统默认主域名"
                          >
                            <ShieldCheck size={12} /> 设为默认
                          </button>
                        )}

                        <button
                          onClick={() => handleToggleStatus(item)}
                          style={{
                            padding: '4px 8px',
                            fontSize: '0.75rem',
                            borderRadius: '6px',
                            border: '1px solid hsl(var(--border))',
                            backgroundColor: 'hsl(var(--bg-card))',
                            color: item.status === 1 ? '#f87171' : 'hsl(var(--accent-green))',
                            cursor: 'pointer'
                          }}
                        >
                          {item.status === 1 ? '禁用' : '启用'}
                        </button>

                        {!item.is_default && (
                          <button
                            onClick={() => handleDelete(item)}
                            style={{
                              padding: '4px 8px',
                              fontSize: '0.75rem',
                              borderRadius: '6px',
                              border: '1px solid rgba(239, 68, 68, 0.2)',
                              backgroundColor: 'rgba(239, 68, 68, 0.08)',
                              color: '#f87171',
                              cursor: 'pointer'
                            }}
                          >
                            <Trash2 size={12} />
                          </button>
                        )}
                      </div>
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      </div>

      {/* Create Domain Modal */}
      {isModalOpen && createPortal(
        <div
          style={{
            position: 'fixed',
            inset: 0,
            backgroundColor: 'rgba(15, 23, 42, 0.35)',
            backdropFilter: 'blur(2.5px)',
            WebkitBackdropFilter: 'blur(2.5px)',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            zIndex: 10000
          }}
          className="animate-fade-in"
          onClick={() => setIsModalOpen(false)}
        >
          <div
            className="glass-panel"
            style={{
              width: '100%',
              maxWidth: '480px',
              padding: '24px',
              position: 'relative',
              backgroundColor: 'hsl(var(--bg-surface))',
              borderRadius: '12px',
              boxShadow: 'var(--shadow-lg)',
              border: '1px solid hsl(var(--border))',
              color: 'hsl(var(--text-primary))',
              display: 'flex',
              flexDirection: 'column',
              gap: '18px'
            }}
            onClick={(e) => e.stopPropagation()}
          >
            <button
              onClick={() => setIsModalOpen(false)}
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

            <h3 style={{ fontSize: '1.15rem', fontWeight: 600, display: 'flex', alignItems: 'center', gap: '8px', color: 'hsl(var(--text-primary))' }}>
              <Globe size={18} style={{ color: 'hsl(var(--primary))' }} />
              新建域名/子域名
            </h3>

            <form onSubmit={handleCreateSubmit} style={{ display: 'flex', flexDirection: 'column', gap: '16px' }}>
              <div>
                <label style={{ display: 'block', fontSize: '0.8rem', fontWeight: 600, marginBottom: '6px' }}>
                  域名描述名称 *
                </label>
                <input
                  type="text"
                  placeholder="如: Facebook引流专用子域名A"
                  value={formData.name}
                  onChange={(e) => setFormData({ ...formData, name: e.target.value })}
                  required
                  style={{
                    width: '100%',
                    height: '38px',
                    padding: '0 12px',
                    fontSize: '0.825rem',
                    borderRadius: '8px',
                    border: '1px solid hsl(var(--border))',
                    backgroundColor: 'hsl(var(--bg-card))',
                    color: 'hsl(var(--text-primary))'
                  }}
                />
              </div>

              <div>
                <label style={{ display: 'block', fontSize: '0.8rem', fontWeight: 600, marginBottom: '6px' }}>
                  域名地址 (Domain String) *
                </label>
                <input
                  type="text"
                  placeholder="如: sub1.star-novel.com 或 star-novel.com"
                  value={formData.domain}
                  onChange={(e) => setFormData({ ...formData, domain: e.target.value })}
                  required
                  style={{
                    width: '100%',
                    height: '38px',
                    padding: '0 12px',
                    fontSize: '0.825rem',
                    borderRadius: '8px',
                    border: '1px solid hsl(var(--border))',
                    backgroundColor: 'hsl(var(--bg-card))',
                    color: 'hsl(var(--text-primary))'
                  }}
                />
                <span style={{ fontSize: '0.725rem', color: 'hsl(var(--text-muted))', marginTop: '4px', display: 'block' }}>
                  无需包含 http:// 或 https:// 前缀
                </span>
              </div>

              <div>
                <label style={{ display: 'block', fontSize: '0.8rem', fontWeight: 600, marginBottom: '6px' }}>
                  域名类型
                </label>
                <div style={{ display: 'flex', gap: '16px' }}>
                  <label style={{ display: 'flex', alignItems: 'center', gap: '6px', cursor: 'pointer', fontSize: '0.825rem' }}>
                    <input
                      type="radio"
                      name="type"
                      checked={formData.type === 'sub'}
                      onChange={() => setFormData({ ...formData, type: 'sub' })}
                    />
                    子域名 (Subdomain)
                  </label>
                  <label style={{ display: 'flex', alignItems: 'center', gap: '6px', cursor: 'pointer', fontSize: '0.825rem' }}>
                    <input
                      type="radio"
                      name="type"
                      checked={formData.type === 'main'}
                      onChange={() => setFormData({ ...formData, type: 'main' })}
                    />
                    主域名 (Main Domain)
                  </label>
                </div>
              </div>

              <div style={{ display: 'flex', alignItems: 'center', gap: '8px', paddingTop: '4px' }}>
                <input
                  type="checkbox"
                  id="is_default_check"
                  checked={formData.is_default}
                  onChange={(e) => setFormData({ ...formData, is_default: e.target.checked })}
                  style={{ cursor: 'pointer' }}
                />
                <label htmlFor="is_default_check" style={{ fontSize: '0.8rem', cursor: 'pointer', color: 'hsl(var(--text-primary))' }}>
                  设为系统默认落地页入口域名
                </label>
              </div>

              <div style={{ display: 'flex', justifyContent: 'flex-end', gap: '12px', marginTop: '12px' }}>
                <button
                  type="button"
                  onClick={() => setIsModalOpen(false)}
                  className="btn-secondary"
                  style={{
                    padding: '8px 16px',
                    fontSize: '0.825rem',
                    borderRadius: '8px',
                    cursor: 'pointer',
                    border: '1px solid hsl(var(--border))'
                  }}
                >
                  取消
                </button>
                <button
                  type="submit"
                  disabled={submitting}
                  className="btn-primary"
                  style={{
                    padding: '8px 20px',
                    fontSize: '0.825rem',
                    fontWeight: 600,
                    borderRadius: '8px',
                    cursor: 'pointer',
                    backgroundColor: 'hsl(var(--primary))',
                    color: '#ffffff',
                    border: 'none',
                    opacity: submitting ? 0.7 : 1
                  }}
                >
                  {submitting ? '创建中...' : '提交创建'}
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
