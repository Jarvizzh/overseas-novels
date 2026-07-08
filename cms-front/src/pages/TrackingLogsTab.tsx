import { useEffect, useState } from 'react';
import { createPortal } from 'react-dom';
import { apiRequest } from '../utils/api';
import { Search, Eye, X, ChevronLeft, ChevronRight, FileText } from 'lucide-react';

interface CAPILog {
  id: number;
  pixel_id: string;
  event_name: string;
  user_id: number | null;
  value: number | null;
  currency: string | null;
  test_event_code: string | null;
  status_code: number;
  payload: string;
  response: string;
  created_at: string;
}

import CustomSelect from '../components/CustomSelect';

const eventOptions = [
  { value: '', label: '全部事件' },
  { value: 'CompleteRegistration', label: 'CompleteRegistration' },
  { value: 'Purchase', label: 'Purchase' }
];

const statusOptions = [
  { value: '', label: '全部状态' },
  { value: '200', label: '200 OK (成功)' },
  { value: '0', label: '0 (空跑/测试)' },
  { value: '-1', label: '-1 (参数缺失)' },
  { value: '-2', label: '-2 (Token配置缺失)' },
  { value: '-4', label: '-4 (网络请求失败)' }
];

export default function TrackingLogsTab() {
  const [logs, setLogs] = useState<CAPILog[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [pageSize] = useState(10);
  const [loading, setLoading] = useState(true);

  // Filter states
  const [pixelFilter, setPixelFilter] = useState('');
  const [eventFilter, setEventFilter] = useState('');
  const [statusFilter, setStatusFilter] = useState('');

  // Details Modal state
  const [selectedLog, setSelectedLog] = useState<CAPILog | null>(null);

  const fetchLogs = async () => {
    setLoading(true);
    try {
      const params = new URLSearchParams();
      params.set('page', String(page));
      params.set('page_size', String(pageSize));
      if (pixelFilter) params.set('pixel_id', pixelFilter.trim());
      if (eventFilter) params.set('event_name', eventFilter);
      if (statusFilter) params.set('status_code', statusFilter);

      const data = await apiRequest('GET', `/tracking-logs?${params.toString()}`);
      setLogs(data.logs || []);
      setTotal(data.total || 0);
    } catch (e) {
      window.showToast?.('加载回传日志失败: ' + e, 'error');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchLogs();
  }, [page, eventFilter, statusFilter]);

  const handleSearchSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    setPage(1);
    fetchLogs();
  };

  const formatJSON = (jsonStr: string) => {
    try {
      const parsed = JSON.parse(jsonStr);
      return JSON.stringify(parsed, null, 2);
    } catch (e) {
      return jsonStr;
    }
  };

  const totalPages = Math.ceil(total / pageSize) || 1;

  return (
    <div className="animate-fade-in" style={{ padding: '24px' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '24px' }}>
        <h1 style={{ fontSize: '1.5rem', fontWeight: 700 }} className="gradient-text">
          回传日志
        </h1>
      </div>

      {/* Filters Search Bar */}
      <form onSubmit={handleSearchSubmit} className="glass-panel" style={{ position: 'relative', zIndex: 10, padding: '16px 20px', marginBottom: '24px', display: 'flex', gap: '16px', alignItems: 'flex-end', flexWrap: 'wrap' }}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: '6px' }}>
          <label style={{ fontSize: '0.8rem', fontWeight: 500, color: 'hsl(var(--text-secondary))' }}>像素 ID</label>
          <input
            type="text"
            className="custom-input"
            placeholder="搜索像素 ID..."
            value={pixelFilter}
            onChange={(e) => setPixelFilter(e.target.value)}
            style={{ width: '200px' }}
          />
        </div>
        <div style={{ display: 'flex', flexDirection: 'column', gap: '6px' }}>
          <label style={{ fontSize: '0.8rem', fontWeight: 500, color: 'hsl(var(--text-secondary))' }}>事件名称</label>
          <CustomSelect
            options={eventOptions}
            value={eventFilter}
            onChange={setEventFilter}
            width="180px"
            placeholder="全部事件"
          />
        </div>
        <div style={{ display: 'flex', flexDirection: 'column', gap: '6px' }}>
          <label style={{ fontSize: '0.8rem', fontWeight: 500, color: 'hsl(var(--text-secondary))' }}>回传状态</label>
          <CustomSelect
            options={statusOptions}
            value={statusFilter}
            onChange={setStatusFilter}
            width="150px"
            placeholder="全部状态"
          />
        </div>
        <button type="submit" className="btn-primary" style={{ height: '38px', display: 'flex', alignItems: 'center', gap: '6px', padding: '0 18px', borderRadius: '8px' }}>
          <Search size={14} /> 筛选
        </button>
      </form>

      {/* Logs Table */}
      <div className="glass-panel" style={{ overflow: 'hidden' }}>
        <table style={{ width: '100%', borderCollapse: 'collapse', textAlign: 'left', fontSize: '0.85rem' }}>
          <thead>
            <tr style={{ borderBottom: '1px solid hsl(var(--border))', color: 'hsl(var(--text-secondary))' }}>
              <th style={{ padding: '12px 16px' }}>ID</th>
              <th style={{ padding: '12px 16px' }}>像素 ID</th>
              <th style={{ padding: '12px 16px' }}>事件类型</th>
              <th style={{ padding: '12px 16px' }}>关联用户</th>
              <th style={{ padding: '12px 16px' }}>回传金额</th>
              <th style={{ padding: '12px 16px' }}>测试代码</th>
              <th style={{ padding: '12px 16px' }}>状态码</th>
              <th style={{ padding: '12px 16px' }}>回传时间</th>
              <th style={{ padding: '12px 16px', textAlign: 'right' }}>操作</th>
            </tr>
          </thead>
          <tbody>
            {loading ? (
              <tr>
                <td colSpan={9} style={{ padding: '30px', textAlign: 'center', color: 'hsl(var(--text-secondary))' }}>加载中...</td>
              </tr>
            ) : logs.length === 0 ? (
              <tr>
                <td colSpan={9} style={{ padding: '30px', textAlign: 'center', color: 'hsl(var(--text-muted))' }}>暂无回传日志记录</td>
              </tr>
            ) : (
              logs.map((log) => (
                <tr key={log.id} style={{ borderBottom: '1px solid hsl(var(--border) / 0.5)', fontSize: '0.9rem', color: 'hsl(var(--text-secondary))' }}>
                  <td style={{ padding: '14px 16px', fontFamily: 'monospace', color: 'hsl(var(--text-secondary))' }}>{log.id}</td>
                  <td style={{ padding: '14px 16px', fontFamily: 'monospace', color: 'hsl(var(--text-secondary))' }}>{log.pixel_id}</td>
                  <td style={{ padding: '14px 16px', color: 'hsl(var(--text-secondary))' }}>{log.event_name}</td>
                  <td style={{ padding: '14px 16px', color: 'hsl(var(--text-secondary))' }}>{log.user_id ? `User ${log.user_id}` : <span style={{ color: 'hsl(var(--text-muted))' }}>-</span>}</td>
                  <td style={{ padding: '14px 16px' }}>
                    {log.value ? (
                      <span className="badge badge-violet">{log.value} {log.currency}</span>
                    ) : (
                      <span style={{ color: 'hsl(var(--text-muted))' }}>-</span>
                    )}
                  </td>
                  <td style={{ padding: '14px 16px', fontFamily: 'monospace', color: 'hsl(var(--text-secondary))' }}>{log.test_event_code || '-'}</td>
                  <td style={{ padding: '14px 16px' }}>
                    <span className={`badge ${log.status_code === 200 ? 'badge-green' : log.status_code === 0 ? 'badge-orange' : 'badge-orange'}`} style={{ color: log.status_code < 0 ? '#ef4444' : undefined, borderColor: log.status_code < 0 ? 'rgba(239, 68, 68, 0.2)' : undefined, backgroundColor: log.status_code < 0 ? 'rgba(239, 68, 68, 0.1)' : undefined }}>
                      {log.status_code} {log.status_code === 200 ? 'OK' : log.status_code === 0 ? 'Dry Run' : 'Error'}
                    </span>
                  </td>
                  <td style={{ padding: '14px 16px', color: 'hsl(var(--text-secondary))' }}>{new Date(log.created_at).toLocaleString()}</td>
                  <td style={{ padding: '14px 16px', textAlign: 'right' }}>
                    <button
                      className="btn-secondary"
                      onClick={() => setSelectedLog(log)}
                      style={{ padding: '6px 10px', display: 'inline-flex', alignItems: 'center', gap: '4px', fontSize: '0.8rem', borderRadius: '6px' }}
                    >
                      <Eye size={12} /> 查看明细
                    </button>
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>

        {/* Pagination Footer */}
        {!loading && logs.length > 0 && (
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: '14px 16px', borderTop: '1px solid hsl(var(--border) / 0.5)' }}>
            <span style={{ fontSize: '0.8rem', color: 'hsl(var(--text-secondary))' }}>
              共 {total} 条日志，第 {page} / {totalPages} 页
            </span>
            <div style={{ display: 'flex', gap: '8px' }}>
              <button
                className="btn-secondary"
                disabled={page <= 1}
                onClick={() => setPage(page - 1)}
                style={{ padding: '6px 12px', fontSize: '0.8rem', borderRadius: '6px', display: 'inline-flex', alignItems: 'center', gap: '4px' }}
              >
                <ChevronLeft size={14} /> 上一页
              </button>
              <button
                className="btn-secondary"
                disabled={page >= totalPages}
                onClick={() => setPage(page + 1)}
                style={{ padding: '6px 12px', fontSize: '0.8rem', borderRadius: '6px', display: 'inline-flex', alignItems: 'center', gap: '4px' }}
              >
                下一页 <ChevronRight size={14} />
              </button>
            </div>
          </div>
        )}
      </div>

      {/* Details Portal Modal Overlay */}
      {selectedLog && createPortal(
        <div style={{
          position: 'fixed',
          top: 0,
          left: 0,
          right: 0,
          bottom: 0,
          backgroundColor: 'rgba(0, 0, 0, 0.5)',
          display: 'flex',
          justifyContent: 'center',
          alignItems: 'center',
          zIndex: 9999
        }} onClick={handleCloseModal}>
          <div className="animate-zoom-in" style={{
            width: '800px',
            maxWidth: '95%',
            maxHeight: '90vh',
            padding: '30px',
            position: 'relative',
            borderRadius: '16px',
            backgroundColor: '#ffffff',
            color: '#1f2937',
            boxShadow: '0 20px 25px -5px rgba(0, 0, 0, 0.15), 0 10px 10px -5px rgba(0, 0, 0, 0.04)',
            overflow: 'hidden',
            display: 'flex',
            flexDirection: 'column',
            border: '1px solid #e5e7eb'
          }} onClick={(e) => e.stopPropagation()}>
            <button
              type="button"
              onClick={handleCloseModal}
              style={{ position: 'absolute', top: '20px', right: '20px', background: 'transparent', border: 'none', color: '#9ca3af', cursor: 'pointer', transition: 'color 0.2s' }}
              onMouseEnter={(e) => e.currentTarget.style.color = '#4b5563'}
              onMouseLeave={(e) => e.currentTarget.style.color = '#9ca3af'}
            >
              <X size={18} />
            </button>

            <h3 style={{ fontSize: '1.25rem', fontWeight: 700, marginBottom: '6px', display: 'flex', alignItems: 'center', gap: '8px', color: '#111827' }}>
              <FileText size={20} style={{ color: '#8b5cf6' }} />
              回传审计日志明细 (ID: {selectedLog.id})
            </h3>
            <p style={{ fontSize: '0.8rem', color: '#6b7280', marginBottom: '20px' }}>
              事件名: <strong style={{ color: '#111827' }}>{selectedLog.event_name}</strong> | 像素 ID: <strong style={{ color: '#111827' }}>{selectedLog.pixel_id}</strong> | 时间: {new Date(selectedLog.created_at).toLocaleString()}
            </p>

            <div style={{ display: 'grid', gridTemplateRows: '1fr 1fr', gap: '16px', overflowY: 'auto', flex: 1 }}>
              <div style={{ display: 'flex', flexDirection: 'column' }}>
                <label style={{ fontSize: '0.8rem', fontWeight: 600, color: '#4b5563', marginBottom: '6px' }}>Request Payload (请求载荷):</label>
                <pre style={{
                  flex: 1,
                  backgroundColor: '#121212',
                  color: '#4af626',
                  padding: '14px',
                  borderRadius: '8px',
                  fontFamily: 'Consolas, Monaco, "Courier New", Courier, monospace',
                  fontSize: '0.8rem',
                  overflow: 'auto',
                  border: '1px solid #2e2e2e',
                  lineHeight: '1.4'
                }}>
                  {formatJSON(selectedLog.payload)}
                </pre>
              </div>

              <div style={{ display: 'flex', flexDirection: 'column' }}>
                <label style={{ fontSize: '0.8rem', fontWeight: 600, color: '#4b5563', marginBottom: '6px' }}>Response Body (响应包体):</label>
                <pre style={{
                  flex: 1,
                  backgroundColor: '#121212',
                  color: selectedLog.status_code === 200 ? '#10b981' : '#ff5555',
                  padding: '14px',
                  borderRadius: '8px',
                  fontFamily: 'Consolas, Monaco, "Courier New", Courier, monospace',
                  fontSize: '0.8rem',
                  overflow: 'auto',
                  border: '1px solid #2e2e2e',
                  lineHeight: '1.4'
                }}>
                  {formatJSON(selectedLog.response)}
                </pre>
              </div>
            </div>
          </div>
        </div>,
        document.body
      )}
    </div>
  );

  function handleCloseModal() {
    setSelectedLog(null);
  }
}
