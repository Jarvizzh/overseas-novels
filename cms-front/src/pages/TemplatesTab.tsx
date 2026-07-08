import { useEffect, useState } from 'react';
import { apiRequest } from '../utils/api';
import { Plus, Edit, Trash, CheckCircle, Search, AlertCircle, LayoutGrid } from 'lucide-react';
import CustomSelect from '../components/CustomSelect';

interface RechargeSlot {
  id?: number;
  template_id?: number;
  slot_index: number; // 1 to 6
  type: 'single' | 'vip' | 'whole_book';
  coins: number;
  bonus: number;
  vip_duration: string; // 'day', 'week', 'month', 'year'
  vip_name: string;
  vip_desc: string;
  price: string;
  price_cents: number;
  price_amount?: number | string;
}

interface RechargeTemplate {
  id: number;
  name: string;
  is_default: boolean;
  slots: RechargeSlot[];
  created_at: string;
  updated_at: string;
}

export default function TemplatesTab() {
  const [templates, setTemplates] = useState<RechargeTemplate[]>([]);
  const [loading, setLoading] = useState(true);
  const [searchQuery, setSearchQuery] = useState('');

  // Pagination states
  const [currentPage, setCurrentPage] = useState(1);
  const pageSize = 10;

  // Form states
  const [isEditing, setIsEditing] = useState(false);
  const [editID, setEditID] = useState<number | null>(null);
  const [templateName, setTemplateName] = useState('');
  const [isDefaultTemplate, setIsDefaultTemplate] = useState(false);

  // Initialize slots state with exactly 6 slots
  const initDefaultSlots = (): RechargeSlot[] => [
    { slot_index: 1, type: 'single', coins: 499, bonus: 50, vip_duration: '', vip_name: '', vip_desc: '', price: '$4.99', price_cents: 499, price_amount: 4.99 },
    { slot_index: 2, type: 'single', coins: 999, bonus: 150, vip_duration: '', vip_name: '', vip_desc: '', price: '$9.99', price_cents: 999, price_amount: 9.99 },
    { slot_index: 3, type: 'single', coins: 1999, bonus: 400, vip_duration: '', vip_name: '', vip_desc: '', price: '$19.99', price_cents: 1999, price_amount: 19.99 },
    { slot_index: 4, type: 'single', coins: 4999, bonus: 1200, vip_duration: '', vip_name: '', vip_desc: '', price: '$49.99', price_cents: 4999, price_amount: 49.99 },
    { slot_index: 5, type: 'vip', coins: 299, bonus: 0, vip_duration: 'week', vip_name: 'VIP Weekly', vip_desc: 'Get 299 Coins + 50/day', price: '$2.99', price_cents: 299, price_amount: 2.99 },
    { slot_index: 6, type: 'vip', coins: 999, bonus: 0, vip_duration: 'month', vip_name: 'VIP Monthly', vip_desc: 'Get 999 Coins + 80/day', price: '$9.99', price_cents: 999, price_amount: 9.99 },
  ];

  const [slots, setSlots] = useState<RechargeSlot[]>(initDefaultSlots());

  const [message, setMessage] = useState('');
  const [error, setError] = useState('');

  // Table resize states
  const [colWidths, setColWidths] = useState<number[]>([
    80,  // ID
    220, // 模板名称
    120, // 是否默认
    240, // 创建时间
    240, // 修改时间
    180  // 操作
  ]);

  const columns = [
    { label: '模板 ID', key: 'id' },
    { label: '模板名称', key: 'name' },
    { label: '状态', key: 'is_default' },
    { label: '创建时间', key: 'created_at' },
    { label: '修改时间', key: 'updated_at' },
    { label: '操作', key: 'actions', align: 'right' }
  ];

  const handleMouseDown = (index: number, e: React.MouseEvent) => {
    e.preventDefault();
    const startX = e.clientX;
    const startWidth = colWidths[index];

    const handleMouseMove = (moveEvent: MouseEvent) => {
      const deltaX = moveEvent.clientX - startX;
      const newWidths = [...colWidths];
      newWidths[index] = Math.max(60, startWidth + deltaX);
      setColWidths(newWidths);
    };

    const handleMouseUp = () => {
      window.removeEventListener('mousemove', handleMouseMove);
      window.removeEventListener('mouseup', handleMouseUp);
    };

    window.addEventListener('mousemove', handleMouseMove);
    window.addEventListener('mouseup', handleMouseUp);
  };

  const fetchTemplates = async () => {
    setLoading(true);
    try {
      const data = await apiRequest('GET', '/recharge-templates');
      setTemplates(data || []);
    } catch (e) {
      console.error(e);
      setError('获取充值模板列表失败');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchTemplates();
  }, []);

  useEffect(() => {
    setCurrentPage(1);
  }, [searchQuery]);

  const handleSlotChange = (index: number, field: keyof RechargeSlot, value: any) => {
    const updated = [...slots];
    const updatedSlot = {
      ...updated[index],
      [field]: value
    };

    // If type is single and price_amount changes, auto-calculate coins = price_amount * 100
    if (updatedSlot.type === 'single') {
      if (field === 'price_amount') {
        const amount = value === '' ? 0 : Number(value);
        updatedSlot.coins = Math.round(amount * 100);
      }
    }

    // If type changes to single, auto-calculate coins based on current price_amount
    if (field === 'type' && value === 'single') {
      const amount = updatedSlot.price_amount !== undefined && updatedSlot.price_amount !== '' ? Number(updatedSlot.price_amount) : (updatedSlot.price_cents / 100);
      updatedSlot.coins = Math.round(amount * 100);
    }

    updated[index] = updatedSlot;
    setSlots(updated);
  };

  const handleAddSlot = () => {
    if (slots.length >= 8) return;
    setSlots([
      ...slots,
      {
        slot_index: slots.length + 1,
        type: 'single',
        coins: 500,
        bonus: 0,
        vip_duration: '',
        vip_name: '',
        vip_desc: '',
        price: '$4.99',
        price_cents: 499,
        price_amount: 4.99
      }
    ]);
  };

  const handleDeleteSlot = (index: number) => {
    if (slots.length <= 4) return;
    const updated = slots.filter((_, idx) => idx !== index);
    const reindexed = updated.map((s, idx) => ({
      ...s,
      slot_index: idx + 1
    }));
    setSlots(reindexed);
  };

  const handleCreateOrUpdate = async (e: React.FormEvent) => {
    e.preventDefault();
    setError('');
    setMessage('');

    if (slots.length < 4 || slots.length > 8) {
      setError('每个模板配置的卡位数量必须在 4 到 8 个之间');
      return;
    }

    // Validate bonus coins rule: bonus <= coins * 1.5
    for (let i = 0; i < slots.length; i++) {
      const slot = slots[i];
      if (slot.type === 'single' && slot.bonus > slot.coins * 1.5) {
        setError(`卡位 ${i + 1} 的赠送金币数不能超过充值金币数的 1.5 倍（最大可赠送 ${Math.floor(slot.coins * 1.5)} 金币）`);
        return;
      }
    }

    // Ensure slot indices are properly numbered 1-6 and format the price strings and cents
    const finalSlots = slots.map((s, idx) => {
      const amount = s.price_amount !== undefined && s.price_amount !== '' ? Number(s.price_amount) : (s.price_cents / 100);
      return {
        ...s,
        slot_index: idx + 1,
        price: `$${amount.toFixed(2)}`,
        price_cents: Math.round(amount * 100)
      };
    });

    const payload = {
      name: templateName,
      is_default: isDefaultTemplate,
      slots: finalSlots
    };

    try {
      if (editID !== null) {
        await apiRequest('PUT', `/recharge-templates/${editID}`, payload);
        setMessage('修改充值配置模板成功！');
      } else {
        await apiRequest('POST', '/recharge-templates', payload);
        setMessage('创建充值配置模板成功！');
      }
      setTimeout(() => {
        setMessage('');
      }, 3000);
      setIsEditing(false);
      setEditID(null);
      fetchTemplates();
    } catch (err: any) {
      setError(err.message || '操作失败，请重试');
    }
  };

  const handleEditClick = (t: RechargeTemplate) => {
    setEditID(t.id);
    setTemplateName(t.name);
    setIsDefaultTemplate(t.is_default);

    // Sort slots by slot_index to ensure order 1-6 and populate price_amount
    const sortedSlots = [...t.slots]
      .sort((a, b) => a.slot_index - b.slot_index)
      .map(s => ({
        ...s,
        price_amount: s.price_amount !== undefined ? s.price_amount : (s.price_cents / 100)
      }));
    // If slots count is less than 4 (e.g. database edge case), fill in the rest
    while (sortedSlots.length < 4) {
      sortedSlots.push({
        slot_index: sortedSlots.length + 1,
        type: 'single',
        coins: 499,
        bonus: 0,
        vip_duration: '',
        vip_name: '',
        vip_desc: '',
        price: '$4.99',
        price_cents: 499,
        price_amount: 4.99
      });
    }
    setSlots(sortedSlots);
    setIsEditing(true);
  };

  const handleSetDefault = async (id: number) => {
    setError('');
    setMessage('');
    try {
      await apiRequest('POST', `/recharge-templates/${id}/set-default`);
      setMessage('设置默认模板成功！');
      setTimeout(() => {
        setMessage('');
      }, 3000);
      fetchTemplates();
    } catch (err: any) {
      setError(err.message || '设置默认失败');
    }
  };

  const handleDelete = async (id: number) => {
    window.showConfirm?.('您确定要删除此充值配置模板吗？关联的卡位将同步删除，且不可恢复。', async () => {
      setError('');
      setMessage('');
      try {
        await apiRequest('DELETE', `/recharge-templates/${id}`);
        setMessage('删除成功！');
        setTimeout(() => {
          setMessage('');
        }, 3000);
        fetchTemplates();
      } catch (err: any) {
        setError(err.message || '删除失败');
      }
    });
  };

  const filteredTemplates = templates.filter(t =>
    String(t.id).includes(searchQuery) ||
    t.name.toLowerCase().includes(searchQuery.toLowerCase())
  );

  const totalPages = Math.ceil(filteredTemplates.length / pageSize);
  const startIndex = (currentPage - 1) * pageSize;
  const paginatedTemplates = filteredTemplates.slice(startIndex, startIndex + pageSize);

  return (
    <div className="animate-fade-in" style={{ padding: '24px' }}>

      {/* Page Header */}
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '24px' }}>
        <div>
          <h1 style={{ fontSize: '1.5rem', fontWeight: 700 }} className="gradient-text">充值模板管理</h1>
        </div>
        {!isEditing && (
          <button
            onClick={() => {
              setEditID(null);
              setTemplateName('');
              setIsDefaultTemplate(false);
              setSlots(initDefaultSlots());
              setIsEditing(true);
            }}
            className="btn-primary"
            style={{ display: 'flex', gap: '6px', alignItems: 'center' }}
          >
            <Plus size={16} /> 新建充值模板
          </button>
        )}
      </div>

      {/* Message Alerts */}
      {message && (
        <div style={{ backgroundColor: 'rgba(16, 185, 129, 0.1)', border: '1px solid rgba(16, 185, 129, 0.25)', color: '#10b981', padding: '12px', borderRadius: '8px', fontSize: '0.85rem', marginBottom: '18px', display: 'flex', alignItems: 'center', gap: '8px' }}>
          <CheckCircle size={14} /> {message}
        </div>
      )}
      {error && (
        <div style={{ backgroundColor: 'rgba(239, 68, 68, 0.1)', border: '1px solid rgba(239, 68, 68, 0.25)', color: '#f87171', padding: '12px', borderRadius: '8px', fontSize: '0.85rem', marginBottom: '18px', display: 'flex', alignItems: 'center', gap: '8px' }}>
          <AlertCircle size={14} /> {error}
        </div>
      )}

      {/* Visual Form Editor Panel */}
      {isEditing && (
        <form onSubmit={handleCreateOrUpdate} className="glass-panel" style={{ padding: '24px', marginBottom: '30px' }}>
          <h3 style={{ marginBottom: '20px', display: 'flex', alignItems: 'center', gap: '8px' }}>
            <LayoutGrid size={18} style={{ color: 'hsl(var(--primary))' }} />
            {editID !== null ? `编辑充值配置模板 (ID: ${editID})` : '新建充值配置模板'}
          </h3>

          {/* Header configuration */}
          <div style={{ display: 'grid', gridTemplateColumns: '2fr 1fr', gap: '20px', marginBottom: '24px', alignItems: 'flex-end' }}>
            <div>
              <label style={{ display: 'block', fontSize: '0.8rem', color: 'hsl(var(--text-secondary))', marginBottom: '6px' }}>
                模板名称 <span style={{ color: '#f87171' }}>*</span>
              </label>
              <input
                type="text"
                className="input-field"
                placeholder="例如：玄幻小说充值模板 / 普通默认模板"
                value={templateName}
                onChange={(e) => setTemplateName(e.target.value)}
                required
              />
            </div>
            <div style={{ display: 'flex', alignItems: 'center', gap: '8px', height: '40px' }}>
              <input
                type="checkbox"
                id="is_default"
                checked={isDefaultTemplate}
                onChange={(e) => setIsDefaultTemplate(e.target.checked)}
                style={{ width: '16px', height: '16px', cursor: 'pointer' }}
              />
              <label htmlFor="is_default" style={{ fontSize: '0.85rem', fontWeight: 500, cursor: 'pointer' }}>
                设为默认模板
              </label>
            </div>
          </div>

          <h4 style={{ marginBottom: '16px', fontSize: '0.95rem', fontWeight: 600, borderBottom: '1px solid hsl(var(--border))', paddingBottom: '8px' }}>
            档位设置 (配置 {slots.length} 个卡位，范围 4 至 8 个)
          </h4>

          {/* 6 card slots builder */}
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: '18px', marginBottom: '24px' }}>
            {slots.map((slot, index) => (
              <div
                key={index}
                style={{
                  border: '1px solid hsl(var(--border))',
                  borderRadius: '12px',
                  padding: '16px',
                  backgroundColor: 'hsl(var(--bg-surface) / 0.3)',
                  position: 'relative'
                }}
              >
                {slots.length > 4 && (
                  <button
                    type="button"
                    onClick={() => handleDeleteSlot(index)}
                    style={{
                      position: 'absolute',
                      top: '12px',
                      right: '12px',
                      background: 'none',
                      border: 'none',
                      color: '#f87171',
                      cursor: 'pointer',
                      padding: '4px',
                      display: 'flex',
                      alignItems: 'center',
                      borderRadius: '50%',
                      zIndex: 10
                    }}
                    title="删除卡位"
                  >
                    <Trash size={14} />
                  </button>
                )}

                <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '12px' }}>
                  <span style={{ fontWeight: 700, fontSize: '0.85rem', color: 'hsl(var(--primary))' }}>
                    卡位 {index + 1}
                  </span>
                </div>

                {/* Recharge Type Selection */}
                <div style={{ marginBottom: '12px' }}>
                  <label style={{ display: 'block', fontSize: '0.7rem', color: 'hsl(var(--text-secondary))', marginBottom: '4px' }}>充值类型</label>
                  <CustomSelect
                    options={[
                      { value: 'single', label: '单次充值' },
                      { value: 'vip', label: '时长会员' },
                      { value: 'whole_book', label: '整部购买' }
                    ]}
                    value={slot.type}
                    onChange={(val) => handleSlotChange(index, 'type', val as any)}
                    width="100%"
                    style={{ height: '32px' }}
                  />
                </div>

                {/* Common Price Input */}
                <div style={{ marginBottom: '12px' }}>
                  <label style={{ display: 'block', fontSize: '0.7rem', color: 'hsl(var(--text-secondary))', marginBottom: '4px' }}>充值金额</label>
                  <div style={{ position: 'relative', display: 'flex', alignItems: 'center' }}>
                    <span style={{
                      position: 'absolute',
                      left: '10px',
                      fontSize: '0.8rem',
                      color: 'hsl(var(--text-secondary))',
                      pointerEvents: 'none'
                    }}>$</span>
                    <input
                      type="number"
                      step="0.01"
                      min="0.01"
                      className="input-field"
                      style={{ height: '32px', fontSize: '0.8rem', paddingLeft: '22px', width: '100%' }}
                      placeholder="4.99"
                      value={slot.price_amount !== undefined ? slot.price_amount : (slot.price_cents / 100)}
                      onChange={(e) => handleSlotChange(index, 'price_amount', e.target.value === '' ? '' : parseFloat(e.target.value))}
                      required
                    />
                  </div>
                </div>

                {/* Conditional Fields based on slot type */}
                {slot.type === 'single' && (
                  <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '10px', marginBottom: '10px' }}>
                    <div>
                      <label style={{ display: 'block', fontSize: '0.7rem', color: 'hsl(var(--text-secondary))', marginBottom: '4px' }}>充值金币数</label>
                      <input
                        type="text"
                        className="input-field"
                        style={{ height: '32px', fontSize: '0.8rem', backgroundColor: 'hsl(var(--bg-surface) / 0.15)', cursor: 'not-allowed' }}
                        value={slot.coins}
                        disabled
                        readOnly
                      />
                    </div>
                    <div>
                      <label style={{ display: 'block', fontSize: '0.7rem', color: 'hsl(var(--text-secondary))', marginBottom: '4px' }}>赠送金币数</label>
                      <input
                        type="number"
                        className="input-field"
                        style={{ height: '32px', fontSize: '0.8rem' }}
                        value={slot.bonus}
                        onChange={(e) => handleSlotChange(index, 'bonus', Number(e.target.value))}
                        required
                      />
                    </div>
                  </div>
                )}

                {slot.type === 'vip' && (
                  <div style={{ marginBottom: '10px' }}>
                    <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '10px', marginBottom: '10px' }}>
                      <div>
                        <label style={{ display: 'block', fontSize: '0.7rem', color: 'hsl(var(--text-secondary))', marginBottom: '4px' }}>获得金币数</label>
                        <input
                          type="number"
                          className="input-field"
                          style={{ height: '32px', fontSize: '0.8rem' }}
                          value={slot.coins}
                          onChange={(e) => handleSlotChange(index, 'coins', Number(e.target.value))}
                          required
                        />
                      </div>
                      <div>
                        <label style={{ display: 'block', fontSize: '0.7rem', color: 'hsl(var(--text-secondary))', marginBottom: '4px' }}>会员时长</label>
                        <CustomSelect
                          options={[
                            { value: 'day', label: '天 (Day)' },
                            { value: 'week', label: '周 (Week)' },
                            { value: 'month', label: '月 (Month)' },
                            { value: 'year', label: '年 (Year)' }
                          ]}
                          value={slot.vip_duration}
                          onChange={(val) => handleSlotChange(index, 'vip_duration', val)}
                          width="100%"
                          style={{ height: '32px' }}
                        />
                      </div>
                    </div>
                    <div style={{ marginBottom: '8px' }}>
                      <label style={{ display: 'block', fontSize: '0.7rem', color: 'hsl(var(--text-secondary))', marginBottom: '4px' }}>会员卡名称</label>
                      <input
                        type="text"
                        className="input-field"
                        style={{ height: '32px', fontSize: '0.8rem' }}
                        placeholder="例如：VIP Weekly"
                        value={slot.vip_name}
                        onChange={(e) => handleSlotChange(index, 'vip_name', e.target.value)}
                        required
                      />
                    </div>
                    <div>
                      <label style={{ display: 'block', fontSize: '0.7rem', color: 'hsl(var(--text-secondary))', marginBottom: '4px' }}>权益介绍描述</label>
                      <input
                        type="text"
                        className="input-field"
                        style={{ height: '32px', fontSize: '0.8rem' }}
                        placeholder="例如：300 Coins + 50/day"
                        value={slot.vip_desc}
                        onChange={(e) => handleSlotChange(index, 'vip_desc', e.target.value)}
                        required
                      />
                    </div>
                  </div>
                )}

                {slot.type === 'whole_book' && (
                  <div style={{ marginBottom: '10px' }}>
                    <div style={{ marginBottom: '8px' }}>
                      <label style={{ display: 'block', fontSize: '0.7rem', color: 'hsl(var(--text-secondary))', marginBottom: '4px' }}>商品名称</label>
                      <input
                        type="text"
                        className="input-field"
                        style={{ height: '32px', fontSize: '0.8rem' }}
                        placeholder="例如：整部购买特惠卡"
                        value={slot.vip_name}
                        onChange={(e) => handleSlotChange(index, 'vip_name', e.target.value)}
                        required
                      />
                    </div>
                    <div>
                      <label style={{ display: 'block', fontSize: '0.7rem', color: 'hsl(var(--text-secondary))', marginBottom: '4px' }}>权益描述</label>
                      <input
                        type="text"
                        className="input-field"
                        style={{ height: '32px', fontSize: '0.8rem' }}
                        placeholder="例如：解锁当前小说所有付费章节"
                        value={slot.vip_desc}
                        onChange={(e) => handleSlotChange(index, 'vip_desc', e.target.value)}
                        required
                      />
                    </div>
                  </div>
                )}

              </div>
            ))}
          </div>

          {/* Add Slot Button */}
          {slots.length < 8 && (
            <div style={{ marginBottom: '24px' }}>
              <button
                type="button"
                onClick={handleAddSlot}
                className="btn-secondary"
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: '6px',
                  width: '100%',
                  justifyContent: 'center',
                  padding: '10px 0',
                  border: '1.5px dashed hsl(var(--border))',
                  borderRadius: '12px',
                  fontSize: '0.85rem'
                }}
              >
                <Plus size={16} /> 添加卡位 ({slots.length}/8)
              </button>
            </div>
          )}

          {/* Form Actions */}
          <div style={{ display: 'flex', gap: '12px', justifyContent: 'flex-end' }}>
            <button type="button" className="btn-secondary" onClick={() => { setIsEditing(false); setEditID(null); }}>取消</button>
            <button type="submit" className="btn-primary">保存模板</button>
          </div>
        </form>
      )}

      {/* Filter and Search Bar */}
      {!isEditing && (
        <div style={{ display: 'flex', gap: '16px', marginBottom: '20px' }}>
          <div style={{ position: 'relative', flex: 1 }}>
            <span style={{ position: 'absolute', left: '12px', top: '12px', color: 'hsl(var(--text-muted))' }}>
              <Search size={16} />
            </span>
            <input
              type="text"
              className="input-field"
              style={{ paddingLeft: '38px' }}
              placeholder="搜索模板名称..."
              value={searchQuery}
              onChange={(e) => setSearchQuery(e.target.value)}
            />
          </div>
        </div>
      )}

      {/* Templates List Table */}
      {!isEditing && (
        <div className="glass-panel" style={{ padding: '8px', overflowX: 'auto' }}>
          <table style={{ width: '100%', borderCollapse: 'collapse', textAlign: 'left', tableLayout: 'fixed' }}>
            <colgroup>
              {colWidths.map((width, i) => (
                <col key={i} style={{ width: `${width}px` }} />
              ))}
            </colgroup>
            <thead>
              <tr style={{ borderBottom: '1px solid hsl(var(--border))', color: 'hsl(var(--text-secondary))', fontSize: '0.8rem' }}>
                {columns.map((col, index) => (
                  <th
                    key={col.key}
                    style={{
                      padding: '12px 12px',
                      textAlign: col.align === 'right' ? 'right' : 'left',
                      position: 'relative',
                      userSelect: 'none'
                    }}
                  >
                    {col.label}
                    {index < columns.length - 1 && (
                      <div
                        onMouseDown={(e) => handleMouseDown(index, e)}
                        className="resize-handle"
                        style={{
                          position: 'absolute',
                          right: 0,
                          top: 0,
                          bottom: 0,
                          width: '5px',
                          cursor: 'col-resize',
                          zIndex: 10,
                          borderRadius: '2px'
                        }}
                      />
                    )}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {loading ? (
                <tr>
                  <td colSpan={6} style={{ padding: '24px', textAlign: 'center', color: 'hsl(var(--text-muted))' }}>正在载入模板列表...</td>
                </tr>
              ) : paginatedTemplates.length === 0 ? (
                <tr>
                  <td colSpan={6} style={{ padding: '24px', textAlign: 'center', color: 'hsl(var(--text-muted))' }}>未创建任何配置模板。</td>
                </tr>
              ) : (
                paginatedTemplates.map((t) => (
                  <tr
                    key={t.id}
                    style={{ borderBottom: '1px solid hsl(var(--border) / 0.5)', fontSize: '0.9rem', transition: 'background-color 0.2s', color: 'hsl(var(--text-secondary))' }}
                    onMouseEnter={(e) => e.currentTarget.style.backgroundColor = 'hsl(var(--bg-card))'}
                    onMouseLeave={(e) => e.currentTarget.style.backgroundColor = 'transparent'}
                  >
                    <td style={{ padding: '14px 12px', fontFamily: 'monospace' }}>{t.id}</td>
                    <td style={{ padding: '14px 12px' }}>{t.name}</td>
                    <td style={{ padding: '14px 12px' }}>
                      {t.is_default ? (
                        <span className="badge badge-green" style={{ display: 'inline-flex', alignItems: 'center', gap: '4px' }}>
                          默认模板
                        </span>
                      ) : (
                        <span style={{ fontSize: '0.8rem' }}> - </span>
                      )}
                    </td>
                    <td style={{ padding: '14px 12px', color: 'hsl(var(--text-secondary))' }}>
                      {new Date(t.created_at).toLocaleString()}
                    </td>
                    <td style={{ padding: '14px 12px', color: 'hsl(var(--text-secondary))' }}>
                      {new Date(t.updated_at).toLocaleString()}
                    </td>
                    <td style={{ padding: '14px 12px', textAlign: 'right' }} onClick={(e) => e.stopPropagation()}>
                      <div style={{ display: 'flex', gap: '6px', justifyContent: 'flex-end' }}>
                        {!t.is_default && (
                          <button
                            className="btn-secondary"
                            style={{ padding: '4px 8px', fontSize: '0.75rem', borderRadius: '6px', color: 'hsl(var(--primary))', borderColor: 'hsl(var(--primary) / 0.25)' }}
                            onClick={() => handleSetDefault(t.id)}
                          >
                            设为默认
                          </button>
                        )}
                        <button
                          className="btn-secondary"
                          style={{ padding: '6px', borderRadius: '6px' }}
                          title="编辑模板配置"
                          onClick={() => handleEditClick(t)}
                        >
                          <Edit size={14} />
                        </button>
                        {!t.is_default && (
                          <button
                            className="btn-secondary"
                            onClick={() => handleDelete(t.id)}
                            style={{ padding: '6px', borderRadius: '6px', color: '#f87171', borderColor: 'rgba(239, 68, 68, 0.2)' }}
                            title="删除模板"
                          >
                            <Trash size={14} />
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
      )}

      {/* Pagination Controls */}
      {!isEditing && totalPages > 1 && (
        <div style={{
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center',
          marginTop: '20px',
          padding: '8px 16px',
          fontSize: '0.85rem',
          color: 'hsl(var(--text-secondary))',
          backgroundColor: 'hsl(var(--bg-surface) / 0.5)',
          borderRadius: '8px',
          border: '1px solid hsl(var(--border))'
        }}>
          <div>
            显示第 {startIndex + 1} 至 {Math.min(startIndex + pageSize, filteredTemplates.length)} 项，共 {filteredTemplates.length} 项
          </div>
          <div style={{ display: 'flex', gap: '8px', alignItems: 'center' }}>
            <button
              className="btn-secondary"
              onClick={() => setCurrentPage(p => Math.max(1, p - 1))}
              disabled={currentPage === 1}
              style={{ padding: '6px 12px', fontSize: '0.8rem', borderRadius: '6px' }}
            >
              上一页
            </button>
            <span style={{ display: 'flex', alignItems: 'center', fontSize: '0.85rem', padding: '0 8px' }}>
              第 {currentPage} 页 / 共 {totalPages} 页
            </span>
            <button
              className="btn-secondary"
              onClick={() => setCurrentPage(p => Math.min(totalPages, p + 1))}
              disabled={currentPage === totalPages}
              style={{ padding: '6px 12px', fontSize: '0.8rem', borderRadius: '6px' }}
            >
              下一页
            </button>
          </div>
        </div>
      )}

    </div>
  );
}
