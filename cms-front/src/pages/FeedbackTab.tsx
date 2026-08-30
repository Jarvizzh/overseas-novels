import React, { useEffect, useState } from 'react';
import { createPortal } from 'react-dom';
import { feedbackApi } from '../utils/api';
import type { FeedbackItem } from '../utils/api';
import { MessageSquare, Search, ChevronLeft, ChevronRight, CheckCircle2, Clock, MessageCircle, X } from 'lucide-react';
import CustomSelect from '../components/CustomSelect';

const statusOptions = [
  { value: '', label: '全部留言状态' },
  { value: 'pending', label: '待处理 (Pending)' },
  { value: 'replied', label: '已回复 (Replied)' },
  { value: 'resolved', label: '已解决 (Resolved)' },
];

const modalStatusOptions = [
  { value: 'pending', label: '待处理 (Pending)' },
  { value: 'replied', label: '已回复 (Replied)' },
  { value: 'resolved', label: '已解决 (Resolved)' },
];

export default function FeedbackTab() {
  const [items, setItems] = useState<FeedbackItem[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [statusFilter, setStatusFilter] = useState('');
  const [keyword, setKeyword] = useState('');
  const [loading, setLoading] = useState(true);

  // Detail / Reply Modal
  const [selectedItem, setSelectedItem] = useState<FeedbackItem | null>(null);
  const [editStatus, setEditStatus] = useState<string>('pending');
  const [adminReply, setAdminReply] = useState<string>('');
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    fetchList();
  }, [page, statusFilter]);

  // Handle ESC to close modal
  useEffect(() => {
    if (!selectedItem) return;
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        setSelectedItem(null);
      }
    };
    document.addEventListener('keydown', handleKeyDown);
    return () => document.removeEventListener('keydown', handleKeyDown);
  }, [selectedItem]);

  const fetchList = async () => {
    setLoading(true);
    try {
      const data = await feedbackApi.getFeedbackList({
        page,
        limit: 15,
        status: statusFilter,
        keyword: keyword.trim(),
      });
      setItems(data.items || []);
      setTotal(data.total || 0);
    } catch (e: any) {
      window.showToast?.(e.message || '获取留言列表失败', 'error');
    } finally {
      setLoading(false);
    }
  };

  const handleSearch = (e: React.FormEvent) => {
    e.preventDefault();
    setPage(1);
    fetchList();
  };

  const openDetailModal = (item: FeedbackItem) => {
    setSelectedItem(item);
    setEditStatus(item.status || 'pending');
    setAdminReply(item.admin_reply || '');
  };

  const handleUpdateSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!selectedItem) return;

    setSaving(true);
    try {
      await feedbackApi.updateFeedback(selectedItem.id, {
        status: editStatus,
        admin_reply: adminReply,
      });
      window.showToast?.('留言状态与回复已成功保存', 'success');
      setSelectedItem(null);
      fetchList();
    } catch (e: any) {
      window.showToast?.(e.message || '更新失败', 'error');
    } finally {
      setSaving(false);
    }
  };

  const renderStatusBadge = (status: string) => {
    switch (status) {
      case 'resolved':
        return (
          <span className="badge badge-green" style={{ display: 'inline-flex', alignItems: 'center', gap: '4px', fontSize: '0.75rem' }}>
            <CheckCircle2 size={12} />
            已解决
          </span>
        );
      case 'replied':
        return (
          <span className="badge badge-blue" style={{ display: 'inline-flex', alignItems: 'center', gap: '4px', fontSize: '0.75rem' }}>
            <MessageCircle size={12} />
            已回复
          </span>
        );
      case 'pending':
      default:
        return (
          <span className="badge badge-yellow" style={{ display: 'inline-flex', alignItems: 'center', gap: '4px', fontSize: '0.75rem' }}>
            <Clock size={12} />
            待处理
          </span>
        );
    }
  };

  const totalPages = Math.ceil(total / 15) || 1;

  return (
    <div className="animate-fade-in" style={{ padding: '16px 24px 24px 24px' }}>
      {/* Top Header */}
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '20px' }}>
        <div>
          <h1 style={{ fontSize: '1.5rem', fontWeight: 700, display: 'flex', alignItems: 'center', gap: '8px' }} className="gradient-text">
            <MessageSquare size={24} style={{ color: 'hsl(var(--primary))' }} />
            客服留言管理
          </h1>
          <p style={{ fontSize: '0.85rem', color: 'hsl(var(--text-secondary))', marginTop: '4px' }}>
            查看和处理来自 H5 读者端的客服工单、充值异常反馈与合规咨询留言
          </p>
        </div>
      </div>

      {/* Filter and Search Bar */}
      <form onSubmit={handleSearch} className="glass-panel" style={{ padding: '16px 20px', marginBottom: '20px', display: 'flex', gap: '16px', flexWrap: 'wrap', alignItems: 'flex-end', position: 'relative', zIndex: 20 }}>
        <div style={{ minWidth: '180px' }}>
          <label style={{ display: 'block', fontSize: '0.8rem', color: 'hsl(var(--text-secondary))', marginBottom: '6px' }}>处理状态</label>
          <CustomSelect
            options={statusOptions}
            value={statusFilter}
            onChange={(val) => {
              setStatusFilter(val);
              setPage(1);
            }}
            width="100%"
          />
        </div>

        <div style={{ flex: 1, minWidth: '220px' }}>
          <label style={{ display: 'block', fontSize: '0.8rem', color: 'hsl(var(--text-secondary))', marginBottom: '6px' }}>搜索关键词</label>
          <div style={{ position: 'relative' }}>
            <input
              type="text"
              className="input-field"
              placeholder="搜索联系邮箱、留言主题或正文内容..."
              value={keyword}
              onChange={(e) => setKeyword(e.target.value)}
              style={{ width: '100%', paddingLeft: '34px' }}
            />
            <Search size={16} style={{ position: 'absolute', left: '10px', top: '10px', color: 'hsl(var(--text-muted))' }} />
          </div>
        </div>

        <div style={{ display: 'flex', gap: '8px' }}>
          <button type="submit" className="btn-primary" style={{ padding: '8px 16px', fontSize: '0.85rem' }}>
            搜索
          </button>
          {keyword && (
            <button
              type="button"
              className="btn-secondary"
              onClick={() => {
                setKeyword('');
                setPage(1);
                fetchList();
              }}
              style={{ padding: '8px 14px', fontSize: '0.85rem' }}
            >
              重置
            </button>
          )}
        </div>
      </form>

      {/* Table Section */}
      <div className="glass-panel" style={{ padding: '8px', overflowX: 'auto', marginBottom: '16px' }}>
        <table style={{ width: '100%', borderCollapse: 'collapse', textAlign: 'left' }}>
          <thead>
            <tr style={{ borderBottom: '1px solid hsl(var(--border))', color: 'hsl(var(--text-secondary))', fontSize: '0.8rem' }}>
              <th style={{ padding: '12px 16px' }}>ID</th>
              <th style={{ padding: '12px 16px' }}>用户 ID</th>
              <th style={{ padding: '12px 16px' }}>联系邮箱</th>
              <th style={{ padding: '12px 16px' }}>咨询主题</th>
              <th style={{ padding: '12px 16px', minWidth: '240px' }}>留言内容摘要</th>
              <th style={{ padding: '12px 16px' }}>处理状态</th>
              <th style={{ padding: '12px 16px' }}>提交时间</th>
              <th style={{ padding: '12px 16px', textAlign: 'right' }}>操作</th>
            </tr>
          </thead>
          <tbody>
            {loading && items.length === 0 ? (
              <tr>
                <td colSpan={8} style={{ padding: '24px', textAlign: 'center', color: 'hsl(var(--text-muted))' }}>
                  正在加载留言记录...
                </td>
              </tr>
            ) : items.length === 0 ? (
              <tr>
                <td colSpan={8} style={{ padding: '24px', textAlign: 'center', color: 'hsl(var(--text-muted))' }}>
                  暂无客服留言记录。
                </td>
              </tr>
            ) : (
              items.map((item) => (
                <tr key={item.id} style={{ borderBottom: '1px solid hsl(var(--border) / 0.5)', fontSize: '0.875rem', color: 'hsl(var(--text-secondary))' }}>
                  <td style={{ padding: '14px 16px', fontFamily: 'monospace', fontSize: '0.8rem', fontWeight: 600 }}>
                    #{item.id}
                  </td>
                  <td style={{ padding: '14px 16px', fontFamily: 'monospace', fontSize: '0.8rem' }}>
                    {item.user_id && item.user_id > 0 ? (
                      <span style={{ color: 'hsl(var(--primary))', fontWeight: 600 }}>{item.user_id}</span>
                    ) : (
                      <span style={{ color: 'hsl(var(--text-muted))' }}>访客</span>
                    )}
                  </td>
                  <td style={{ padding: '14px 16px' }}>
                    <a href={`mailto:${item.email}`} style={{ color: 'hsl(var(--primary))', textDecoration: 'none', fontWeight: 500 }} className="hover:underline">
                      {item.email}
                    </a>
                  </td>
                  <td style={{ padding: '14px 16px' }}>
                    <span className="badge badge-gray" style={{ fontSize: '0.75rem' }}>
                      {item.subject || '常规咨询'}
                    </span>
                  </td>
                  <td style={{ padding: '14px 16px', maxWidth: '300px', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }} title={item.content}>
                    {item.content}
                  </td>
                  <td style={{ padding: '14px 16px' }}>
                    {renderStatusBadge(item.status)}
                  </td>
                  <td style={{ padding: '14px 16px', fontSize: '0.8rem', color: 'hsl(var(--text-muted))' }}>
                    {new Date(item.created_at).toLocaleString()}
                  </td>
                  <td style={{ padding: '14px 16px', textAlign: 'right' }}>
                    <button
                      onClick={() => openDetailModal(item)}
                      className="btn-secondary"
                      style={{ padding: '4px 12px', fontSize: '0.75rem' }}
                    >
                      详情 / 处理
                    </button>
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>

      {/* Pagination */}
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', fontSize: '0.85rem', color: 'hsl(var(--text-secondary))' }}>
        <div>共 {total} 条留言 · 当前第 {page} / {totalPages} 页</div>
        <div style={{ display: 'flex', gap: '8px', alignItems: 'center' }}>
          <button
            onClick={() => setPage((p) => Math.max(p - 1, 1))}
            disabled={page <= 1 || loading}
            className="btn-secondary"
            style={{ padding: '6px 10px', opacity: page <= 1 ? 0.5 : 1, cursor: page <= 1 ? 'not-allowed' : 'pointer' }}
          >
            <ChevronLeft size={16} />
          </button>
          <span style={{ padding: '0 8px', fontWeight: 600 }}>{page}</span>
          <button
            onClick={() => setPage((p) => Math.min(p + 1, totalPages))}
            disabled={page >= totalPages || loading}
            className="btn-secondary"
            style={{ padding: '6px 10px', opacity: page >= totalPages ? 0.5 : 1, cursor: page >= totalPages ? 'not-allowed' : 'pointer' }}
          >
            <ChevronRight size={16} />
          </button>
        </div>
      </div>

      {/* Detail & Reply Modal via createPortal */}
      {selectedItem && createPortal(
        <div
          style={{
            position: 'fixed',
            top: 0,
            left: 0,
            right: 0,
            bottom: 0,
            backgroundColor: 'rgba(15, 23, 42, 0.5)',
            backdropFilter: 'blur(4px)',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            zIndex: 1000,
            padding: '20px',
          }}
          onClick={() => setSelectedItem(null)}
        >
          <div
            className="glass-panel animate-fade-in"
            style={{
              width: '100%',
              maxWidth: '560px',
              backgroundColor: 'hsl(var(--bg-surface))',
              border: '1px solid hsl(var(--border))',
              borderRadius: '16px',
              boxShadow: 'var(--shadow-lg)',
              maxHeight: '90vh',
              display: 'flex',
              flexDirection: 'column',
              overflow: 'hidden',
            }}
            onClick={(e) => e.stopPropagation()}
          >
            {/* Modal Header */}
            <div
              style={{
                padding: '18px 24px',
                borderBottom: '1px solid hsl(var(--border))',
                display: 'flex',
                justifyContent: 'space-between',
                alignItems: 'center',
              }}
            >
              <h3 style={{ fontSize: '1.15rem', fontWeight: 600, display: 'flex', alignItems: 'center', gap: '8px', margin: 0 }}>
                <MessageSquare size={18} style={{ color: 'hsl(var(--primary))' }} />
                留言工单详情 #{selectedItem.id}
              </h3>
              <button
                type="button"
                onClick={() => setSelectedItem(null)}
                style={{ background: 'none', border: 'none', color: 'hsl(var(--text-muted))', cursor: 'pointer', padding: '4px' }}
              >
                <X size={20} />
              </button>
            </div>

            {/* Modal Body */}
            <form onSubmit={handleUpdateSubmit} style={{ padding: '24px', display: 'flex', flexDirection: 'column', gap: '16px', overflowY: 'auto' }}>
              <div
                style={{
                  display: 'grid',
                  gridTemplateColumns: '1fr 1fr',
                  gap: '12px',
                  backgroundColor: 'hsl(var(--bg-card))',
                  padding: '14px',
                  borderRadius: '10px',
                  border: '1px solid hsl(var(--border))',
                  fontSize: '0.8rem',
                }}
              >
                <div>
                  <span style={{ color: 'hsl(var(--text-muted))', display: 'block', marginBottom: '2px' }}>联系邮箱：</span>
                  <a href={`mailto:${selectedItem.email}`} style={{ color: 'hsl(var(--primary))', fontWeight: 600, textDecoration: 'none' }}>
                    {selectedItem.email}
                  </a>
                </div>
                <div>
                  <span style={{ color: 'hsl(var(--text-muted))', display: 'block', marginBottom: '2px' }}>用户 ID：</span>
                  <span style={{ fontWeight: 600 }}>{selectedItem.user_id || '访客 (未登录)'}</span>
                </div>
                <div>
                  <span style={{ color: 'hsl(var(--text-muted))', display: 'block', marginBottom: '2px' }}>咨询主题：</span>
                  <span style={{ fontWeight: 600 }}>{selectedItem.subject || '常规咨询'}</span>
                </div>
                <div>
                  <span style={{ color: 'hsl(var(--text-muted))', display: 'block', marginBottom: '2px' }}>提交时间：</span>
                  <span>{new Date(selectedItem.created_at).toLocaleString()}</span>
                </div>
              </div>

              <div>
                <label style={{ display: 'block', fontSize: '0.75rem', fontWeight: 600, color: 'hsl(var(--text-secondary))', marginBottom: '6px' }}>
                  用户留言原始内容：
                </label>
                <div
                  style={{
                    backgroundColor: 'hsl(var(--bg-card))',
                    border: '1px solid hsl(var(--border))',
                    borderRadius: '10px',
                    padding: '12px 14px',
                    fontSize: '0.875rem',
                    lineHeight: '1.6',
                    whiteSpace: 'pre-wrap',
                    color: 'hsl(var(--text-primary))',
                    maxHeight: '180px',
                    overflowY: 'auto',
                  }}
                >
                  {selectedItem.content}
                </div>
              </div>

              <div style={{ borderTop: '1px solid hsl(var(--border))', paddingTop: '16px', display: 'flex', flexDirection: 'column', gap: '14px' }}>
                <div>
                  <label style={{ display: 'block', fontSize: '0.75rem', fontWeight: 600, color: 'hsl(var(--text-primary))', marginBottom: '6px' }}>
                    处理状态变更：
                  </label>
                  <CustomSelect
                    options={modalStatusOptions}
                    value={editStatus}
                    onChange={(val) => setEditStatus(val)}
                    width="100%"
                  />
                </div>

                <div>
                  <label style={{ display: 'block', fontSize: '0.75rem', fontWeight: 600, color: 'hsl(var(--text-primary))', marginBottom: '6px' }}>
                    运营回复 / 处理备注：
                  </label>
                  <textarea
                    rows={4}
                    value={adminReply}
                    onChange={(e) => setAdminReply(e.target.value)}
                    placeholder="输入处理记录或回复备注（如已通过邮件回复、退款补单单号等）..."
                    className="input-field"
                    style={{ width: '100%', resize: 'vertical' }}
                  />
                </div>
              </div>

              {/* Modal Footer */}
              <div
                style={{
                  paddingTop: '16px',
                  borderTop: '1px solid hsl(var(--border))',
                  display: 'flex',
                  justifyContent: 'flex-end',
                  alignItems: 'center',
                  gap: '12px',
                  width: '100%',
                  boxSizing: 'border-box',
                }}
              >
                <button
                  type="button"
                  className="btn-secondary"
                  onClick={() => setSelectedItem(null)}
                >
                  取消
                </button>
                <button
                  type="submit"
                  disabled={saving}
                  className="btn-primary"
                  style={{ cursor: saving ? 'not-allowed' : 'pointer' }}
                >
                  {saving ? '保存中...' : '保存处理结果'}
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
