import React, { useCallback, useEffect, useId, useRef, useState } from 'react';
import { useLocation, useNavigate, useParams, useSearchParams } from 'react-router-dom';
import {
  BookOpen,
  Check,
  ChevronLeft,
  Crown,
  GripVertical,
  Plus,
  Save,
  Trash2,
} from 'lucide-react';
import { apiRequest } from '../utils/api';
import CustomSelect from '../components/CustomSelect';
import { CmsDialog } from '../components/ui/CmsDialog';
import { Pagination } from '../components/ui/Pagination';
import {
  RowActionMenu,
  SearchInput,
  SkeletonRows,
  StatusDot,
  type TableDensity,
  TableRegion,
  ViewOptionsPopover,
} from '../components/ui/Primitives';

const H5GoldCoin: React.FC<{ size?: number }> = ({ size = 16 }) => {
  const gradId = useId();
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      viewBox="0 0 24 24"
      aria-hidden="true"
      style={{
        width: `${size}px`,
        height: `${size}px`,
        display: 'inline-block',
        verticalAlign: 'middle',
        flexShrink: 0,
      }}
    >
      <circle cx="12" cy="12" r="10" fill={`url(#${gradId})`} stroke="#9A7132" strokeWidth="1.25" />
      <circle cx="12" cy="12" r="7.5" fill="none" stroke="rgba(255, 248, 225, 0.55)" strokeWidth="0.75" />
      <path
        d="M12 6.5l1.35 3.65L17 11.5l-3.65 1.35L12 16.5l-1.35-3.65L7 11.5l3.65-1.35L12 6.5z"
        fill="#6B4A16"
      />
      <defs>
        <linearGradient id={gradId} x1="0%" y1="0%" x2="100%" y2="100%">
          <stop offset="0%" stopColor="#F5E1A4" />
          <stop offset="50%" stopColor="#D4AE68" />
          <stop offset="100%" stopColor="#A67C37" />
        </linearGradient>
      </defs>
    </svg>
  );
};

interface RechargeSlot {
  _uid?: string;
  id?: number;
  template_id?: number;
  slot_index: number;
  type: 'single' | 'subscription' | 'vip' | 'whole_book';
  coins: number;
  bonus: number;
  vip_duration: string;
  subscription_cycle?: string; // 'day', 'week', 'month'
  vip_name: string;
  vip_desc: string;
  price: string;
  price_cents: number;
  price_amount?: number | string;
}

let slotUidSeq = 1;
function nextSlotUid(): string {
  slotUidSeq += 1;
  return `slot-${slotUidSeq}`;
}

function makeDefaultNewSlot(slotIndex: number): RechargeSlot {
  return {
    _uid: nextSlotUid(),
    slot_index: slotIndex,
    type: 'single',
    coins: 499,
    bonus: 0,
    vip_duration: '',
    subscription_cycle: '',
    vip_name: '',
    vip_desc: '',
    price: '$4.99',
    price_cents: 499,
    price_amount: 4.99,
  };
}

interface RechargeTemplate {
  id: number;
  name: string;
  is_default: boolean;
  slots: RechargeSlot[];
  created_at: string;
  updated_at: string;
}

type SlotTypeNormalized = 'single' | 'subscription' | 'whole_book';

const CYCLE_LABELS: Record<string, string> = {
  day: '日卡 · 1 天',
  week: '周卡 · 7 天',
  month: '月卡 · 30 天',
};

function normalizeSlotType(type: RechargeSlot['type']): SlotTypeNormalized {
  if (type === 'vip' || type === 'subscription') return 'subscription';
  if (type === 'whole_book') return 'whole_book';
  return 'single';
}

function getSlotAmount(slot: RechargeSlot): number {
  if (slot.price_amount === '') return 0;
  if (slot.price_amount !== undefined) {
    const num = Number(slot.price_amount);
    return Number.isFinite(num) ? num : 0;
  }
  return (slot.price_cents || 0) / 100;
}

function getSlotMaxBonus(slot: RechargeSlot): number {
  return Math.floor((Number(slot.coins) || 0) * 1.5);
}

function isSlotBonusValid(slot: RechargeSlot): boolean {
  if (normalizeSlotType(slot.type) !== 'single') return true;
  const bonus = Number(slot.bonus);
  return Number.isFinite(bonus) && bonus >= 0 && bonus <= getSlotMaxBonus(slot);
}

function summarizeTemplate(tpl: RechargeTemplate) {
  let single = 0;
  let sub = 0;
  let book = 0;
  let minP = Infinity;
  let maxP = 0;
  let maxBonusPct = 0;

  (tpl.slots || []).forEach((s) => {
    const t = normalizeSlotType(s.type);
    if (t === 'single') {
      single += 1;
      if (s.coins > 0 && s.bonus > 0) {
        maxBonusPct = Math.max(maxBonusPct, Math.round((s.bonus / s.coins) * 100));
      }
    } else if (t === 'subscription') {
      sub += 1;
    } else {
      book += 1;
    }
    const amt = getSlotAmount(s);
    if (amt > 0) {
      minP = Math.min(minP, amt);
      maxP = Math.max(maxP, amt);
    }
  });

  const parts: string[] = [];
  if (single > 0) parts.push(`${single} 金币包`);
  if (sub > 0) parts.push(`${sub} VIP 订阅`);
  if (book > 0) parts.push(`${book} 整本购买`);

  return {
    summaryText: parts.join(' · ') || '暂无卡位',
    priceRange: minP === Infinity ? '$0.00' : `$${minP.toFixed(2)} – $${maxP.toFixed(2)}`,
    maxBonusPct,
  };
}

const initDefaultSlots = (): RechargeSlot[] => [
  { _uid: nextSlotUid(), slot_index: 1, type: 'single', coins: 499, bonus: 50, vip_duration: '', subscription_cycle: '', vip_name: '', vip_desc: '', price: '$4.99', price_cents: 499, price_amount: 4.99 },
  { _uid: nextSlotUid(), slot_index: 2, type: 'single', coins: 999, bonus: 150, vip_duration: '', subscription_cycle: '', vip_name: '', vip_desc: '', price: '$9.99', price_cents: 999, price_amount: 9.99 },
  { _uid: nextSlotUid(), slot_index: 3, type: 'single', coins: 1999, bonus: 400, vip_duration: '', subscription_cycle: '', vip_name: '', vip_desc: '', price: '$19.99', price_cents: 1999, price_amount: 19.99 },
  { _uid: nextSlotUid(), slot_index: 4, type: 'single', coins: 4999, bonus: 1200, vip_duration: '', subscription_cycle: '', vip_name: '', vip_desc: '', price: '$49.99', price_cents: 4999, price_amount: 49.99 },
  { _uid: nextSlotUid(), slot_index: 5, type: 'subscription', coins: 0, bonus: 0, vip_duration: 'day', subscription_cycle: 'day', vip_name: 'VIP Daily Pass', vip_desc: '全站小说24小时无限畅读，每日自动续费', price: '$0.99', price_cents: 99, price_amount: 0.99 },
  { _uid: nextSlotUid(), slot_index: 6, type: 'subscription', coins: 0, bonus: 0, vip_duration: 'week', subscription_cycle: 'week', vip_name: 'VIP Weekly Pass', vip_desc: '全站小说7天无限畅读，每周自动续费', price: '$4.99', price_cents: 499, price_amount: 4.99 },
  { _uid: nextSlotUid(), slot_index: 7, type: 'subscription', coins: 0, bonus: 0, vip_duration: 'month', subscription_cycle: 'month', vip_name: 'VIP Monthly Pass', vip_desc: '全站小说30天无限畅读，每月自动续费', price: '$14.99', price_cents: 1499, price_amount: 14.99 },
];

export default function TemplatesTab() {
  const navigate = useNavigate();
  const location = useLocation();
  const { templateId } = useParams<{ templateId?: string }>();
  const [searchParams] = useSearchParams();

  const isNewRoute = location.pathname === '/recharge-templates/new';
  const parsedTemplateId =
    templateId && /^\d+$/.test(templateId) ? Number(templateId) : null;
  const isEditRoute = parsedTemplateId !== null;
  const isInvalidEditRoute = Boolean(templateId && parsedTemplateId === null);
  const isEditorPage = isNewRoute || isEditRoute || isInvalidEditRoute;
  const copyFromParam = searchParams.get('copyFrom');
  const parsedCopyFromId =
    copyFromParam && /^\d+$/.test(copyFromParam) ? Number(copyFromParam) : null;
  const routeKey = `${location.pathname}?${searchParams.toString()}`;

  const [templates, setTemplates] = useState<RechargeTemplate[]>([]);
  const [loading, setLoading] = useState(true);
  const [searchQuery, setSearchQuery] = useState('');
  const [currentPage, setCurrentPage] = useState(1);
  const [density, setDensity] = useState<TableDensity>('compact');
  const pageSize = 10;

  // Editor state
  const [editID, setEditID] = useState<number | null>(null);
  const [templateName, setTemplateName] = useState('');
  const [isDefaultTemplate, setIsDefaultTemplate] = useState(false);
  const [slots, setSlots] = useState<RechargeSlot[]>(initDefaultSlots);
  const [selectedSlotIdx, setSelectedSlotIdx] = useState(0);
  const [saving, setSaving] = useState(false);
  const [formError, setFormError] = useState('');
  const [h5Theme, setH5Theme] = useState<'light' | 'dark'>('light');
  const [dragSlotIdx, setDragSlotIdx] = useState<number | null>(null);
  const [dragOverIdx, setDragOverIdx] = useState<number | null>(null);
  const [reorderStatus, setReorderStatus] = useState('');
  const [editingSlotTarget, setEditingSlotTarget] = useState<number | 'new' | null>(null);
  const [slotDraft, setSlotDraft] = useState<RechargeSlot | null>(null);
  const [slotDraftError, setSlotDraftError] = useState('');
  const [missingEditTemplate, setMissingEditTemplate] = useState(false);
  const slotFormId = useId();
  const editIdRef = useRef<number | null>(null);
  const hydratedRouteKeyRef = useRef<string | null>(null);
  useEffect(() => {
    editIdRef.current = editID;
  }, [editID]);

  const fetchTemplates = async () => {
    setLoading(true);
    try {
      const data: RechargeTemplate[] = (await apiRequest('GET', '/recharge-templates')) || [];
      setTemplates(data);
      const currentEditId = editIdRef.current;
      if (currentEditId !== null) {
        const matched = data.find((item) => item.id === currentEditId);
        if (matched) {
          setIsDefaultTemplate(Boolean(matched.is_default));
        }
      }
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : '获取充值模板列表失败';
      window.showToast?.(msg, 'error');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void fetchTemplates();
  }, []);

  useEffect(() => {
    setCurrentPage(1);
  }, [searchQuery]);

  const resetBlankEditor = useCallback(() => {
    setEditID(null);
    setTemplateName('');
    setIsDefaultTemplate(false);
    setSlots(initDefaultSlots());
    setSelectedSlotIdx(0);
    setFormError('');
    setReorderStatus('');
    setEditingSlotTarget(null);
    setSlotDraft(null);
    setSlotDraftError('');
  }, []);

  const applyTemplateToEditor = useCallback((t: RechargeTemplate, asCopy: boolean) => {
    setEditID(asCopy ? null : t.id);
    setTemplateName(asCopy ? `${t.name} (副本)` : t.name);
    setIsDefaultTemplate(asCopy ? false : t.is_default);
    const sorted: RechargeSlot[] = [...(t.slots || [])]
      .sort((a, b) => a.slot_index - b.slot_index)
      .map((s) => ({
        ...s,
        _uid: nextSlotUid(),
        price_amount: s.price_amount !== undefined ? s.price_amount : s.price_cents / 100,
      }));
    while (sorted.length < 4) {
      sorted.push(makeDefaultNewSlot(sorted.length + 1));
    }
    setSlots(sorted);
    setSelectedSlotIdx(0);
    setFormError('');
    setReorderStatus('');
    setEditingSlotTarget(null);
    setSlotDraft(null);
    setSlotDraftError('');
  }, []);

  // Hydrate standalone /new, /new?copyFrom=<id>, and /:templateId/edit pages cleanly
  useEffect(() => {
    if (!isEditorPage) {
      hydratedRouteKeyRef.current = null;
      setMissingEditTemplate(false);
      setEditingSlotTarget(null);
      setSlotDraft(null);
      setSlotDraftError('');
      return;
    }
    if (isInvalidEditRoute) {
      setMissingEditTemplate(true);
      return;
    }
    if (isNewRoute) {
      if (parsedCopyFromId !== null) {
        if (loading) return;
        const source = templates.find((item) => item.id === parsedCopyFromId);
        if (!source) {
          setMissingEditTemplate(true);
          return;
        }
        setMissingEditTemplate(false);
        if (hydratedRouteKeyRef.current !== routeKey) {
          hydratedRouteKeyRef.current = routeKey;
          applyTemplateToEditor(source, true);
        }
        return;
      }
      setMissingEditTemplate(false);
      if (hydratedRouteKeyRef.current !== routeKey) {
        hydratedRouteKeyRef.current = routeKey;
        resetBlankEditor();
      }
      return;
    }
    if (isEditRoute && parsedTemplateId !== null) {
      if (loading) return;
      const matched = templates.find((item) => item.id === parsedTemplateId);
      if (!matched) {
        setMissingEditTemplate(true);
        return;
      }
      setMissingEditTemplate(false);
      if (hydratedRouteKeyRef.current !== routeKey) {
        hydratedRouteKeyRef.current = routeKey;
        applyTemplateToEditor(matched, false);
      }
    }
  }, [
    isEditorPage,
    isInvalidEditRoute,
    isNewRoute,
    isEditRoute,
    parsedCopyFromId,
    parsedTemplateId,
    loading,
    templates,
    routeKey,
    applyTemplateToEditor,
    resetBlankEditor,
  ]);

  const startCreateTemplate = () => {
    navigate('/recharge-templates/new');
  };

  const openEditorForTemplate = (t: RechargeTemplate) => {
    navigate(`/recharge-templates/${t.id}/edit`);
  };

  const openCopyTemplate = (t: RechargeTemplate) => {
    navigate(`/recharge-templates/new?copyFrom=${t.id}`);
  };

  const handleSetDefault = async (id: number) => {
    try {
      await apiRequest('POST', `/recharge-templates/${id}/set-default`);
      if (editID !== null) {
        setIsDefaultTemplate(editID === id);
      }
      window.showToast?.('已更新全站默认充值模板', 'success');
      void fetchTemplates();
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : '设置默认模板失败';
      window.showToast?.(msg, 'error');
    }
  };

  const handleDelete = (t: RechargeTemplate) => {
    if (t.is_default) {
      window.showToast?.('全站默认模板不可直接删除，请先切换其他默认模板', 'error');
      return;
    }
    window.showConfirm?.(`您确定要删除充值模板「${t.name}」吗？关联的卡位配置将同步删除，且不可恢复。`, async () => {
      try {
        await apiRequest('DELETE', `/recharge-templates/${t.id}`);
        window.showToast?.(`已删除模板「${t.name}」`, 'success');
        if (editID === t.id) {
          setEditID(null);
          navigate('/recharge-templates');
        }
        void fetchTemplates();
      } catch (err: unknown) {
        const msg = err instanceof Error ? err.message : '删除模板失败';
        window.showToast?.(msg, 'error');
      }
    });
  };

  const applySlotDraftChange = (field: keyof RechargeSlot, value: string | number) => {
    setSlotDraftError('');
    setSlotDraft((prev) => {
      if (!prev) return prev;
      const prevCents = Math.round(getSlotAmount(prev) * 100);
      const current = { ...prev, [field]: value };
      const normalizedType = normalizeSlotType(current.type);

      if (field === 'price_amount') {
        const amount = value === '' ? 0 : Number(value);
        const safeCents = value === '' || !Number.isFinite(amount) || amount < 0 ? 0 : Math.round(amount * 100);
        current.price_cents = safeCents;
        current.price = `$${(safeCents / 100).toFixed(2)}`;
        if (normalizedType === 'single' && (prev.coins === prevCents || prev.coins === 0)) {
          current.coins = safeCents;
        }
      }

      if (field === 'coins') {
        const coinsNum = value === '' ? 0 : Math.max(0, Math.round(Number(value) || 0));
        current.coins = coinsNum;
      }

      if (field === 'bonus') {
        const bonusNum = value === '' ? 0 : Math.round(Number(value) || 0);
        current.bonus = bonusNum;
      }

      if (field === 'type') {
        const nextType = normalizeSlotType(value as RechargeSlot['type']);
        const amount = getSlotAmount(current);
        if (nextType === 'single') {
          current.coins = current.coins > 0 ? current.coins : Math.round(amount * 100);
          current.vip_duration = '';
          current.subscription_cycle = '';
        } else if (nextType === 'subscription') {
          current.coins = 0;
          current.bonus = 0;
          const cycle = current.subscription_cycle || current.vip_duration || 'week';
          current.subscription_cycle = cycle;
          current.vip_duration = cycle;
          current.vip_name = current.vip_name || 'VIP Weekly Pass';
          current.vip_desc = current.vip_desc || 'Unlimited reading across all titles. Auto-renews, cancel anytime.';
        } else {
          current.coins = 0;
          current.bonus = 0;
          current.vip_duration = '';
          current.subscription_cycle = '';
          current.vip_name = current.vip_name || 'Unlock Entire Novel';
          current.vip_desc = current.vip_desc || 'One-time purchase to unlock all premium chapters in this book.';
        }
      }

      if (field === 'subscription_cycle') {
        current.subscription_cycle = String(value);
        current.vip_duration = String(value);
      }

      return current;
    });
  };

  const openEditSlotModal = (index: number) => {
    const target = slots[index];
    if (!target) return;
    setSelectedSlotIdx(index);
    setSlotDraft({ ...target });
    setSlotDraftError('');
    setEditingSlotTarget(index);
  };

  const handleAddSlot = () => {
    if (slots.length >= 8) {
      window.showToast?.('单个模板最多配置 8 个卡位', 'error');
      return;
    }
    setSlotDraft(makeDefaultNewSlot(slots.length + 1));
    setSlotDraftError('');
    setEditingSlotTarget('new');
  };

  const closeSlotModal = () => {
    setEditingSlotTarget(null);
    setSlotDraft(null);
    setSlotDraftError('');
  };

  const handleConfirmSlotModal = (e: React.FormEvent) => {
    e.preventDefault();
    if (!slotDraft || editingSlotTarget === null) return;
    const amt = getSlotAmount(slotDraft);
    const type = normalizeSlotType(slotDraft.type);

    if (!(amt > 0)) {
      setSlotDraftError('档位售价必须大于 $0.00');
      return;
    }
    if (type === 'single') {
      if (!Number.isInteger(Number(slotDraft.coins)) || Number(slotDraft.coins) <= 0) {
        setSlotDraftError('基础金币数必须为大于 0 的整数');
        return;
      }
      if (!isSlotBonusValid(slotDraft)) {
        setSlotDraftError(
          Number(slotDraft.bonus) < 0 || !Number.isFinite(Number(slotDraft.bonus))
            ? '额外赠送金币数不能为负数'
            : `赠送不可超过基础金币 1.5 倍（上限 ${getSlotMaxBonus(slotDraft)} 金币）`,
        );
        return;
      }
    }
    if (type === 'subscription' && (!slotDraft.vip_name.trim() || !slotDraft.vip_desc.trim())) {
      setSlotDraftError('请完整填写会员卡标题与权益描述');
      return;
    }
    if (type === 'whole_book' && (!slotDraft.vip_name.trim() || !slotDraft.vip_desc.trim())) {
      setSlotDraftError('请完整填写整本购买的商品名称与权益描述');
      return;
    }

    const committed: RechargeSlot = {
      ...slotDraft,
      _uid: slotDraft._uid || nextSlotUid(),
      price: `$${amt.toFixed(2)}`,
      price_cents: Math.round(amt * 100),
      price_amount: amt,
    };

    setFormError('');
    if (editingSlotTarget === 'new') {
      const nextIdx = slots.length;
      setSlots((prev) => [...prev, { ...committed, slot_index: prev.length + 1 }]);
      setSelectedSlotIdx(nextIdx);
      setReorderStatus(`已新增卡位 ${nextIdx + 1}`);
    } else {
      const targetIdx = editingSlotTarget;
      setSlots((prev) =>
        prev.map((item, idx) => (idx === targetIdx ? { ...committed, slot_index: idx + 1 } : item)),
      );
      setSelectedSlotIdx(targetIdx);
    }
    closeSlotModal();
  };

  const handleDeleteSlot = (index: number) => {
    if (slots.length <= 4) {
      window.showToast?.('单个模板至少保留 4 个卡位', 'error');
      return;
    }
    setSlots((prev) =>
      prev
        .filter((_, idx) => idx !== index)
        .map((s, idx) => ({ ...s, slot_index: idx + 1 })),
    );
    setSelectedSlotIdx((prevIdx) => {
      if (prevIdx >= slots.length - 1) return Math.max(0, slots.length - 2);
      if (prevIdx > index) return prevIdx - 1;
      return prevIdx;
    });
    setReorderStatus(`已移除卡位 ${index + 1}`);
  };

  const reorderSlots = (fromIdx: number, toIdx: number) => {
    if (fromIdx === toIdx || fromIdx < 0 || toIdx < 0 || fromIdx >= slots.length || toIdx >= slots.length) return;
    setSlots((prev) => {
      const next = [...prev];
      const [moved] = next.splice(fromIdx, 1);
      next.splice(toIdx, 0, moved);
      return next.map((s, i) => ({ ...s, slot_index: i + 1 }));
    });
    setSelectedSlotIdx((prevSel) => {
      if (prevSel === fromIdx) return toIdx;
      if (fromIdx < toIdx && prevSel > fromIdx && prevSel <= toIdx) return prevSel - 1;
      if (fromIdx > toIdx && prevSel >= toIdx && prevSel < fromIdx) return prevSel + 1;
      return prevSel;
    });
    setReorderStatus(`已将卡位调整至第 ${toIdx + 1} 位，共 ${slots.length} 个卡位`);
  };

  const handleSlotDragStart = (e: React.DragEvent<HTMLElement>, index: number) => {
    setDragSlotIdx(index);
    setDragOverIdx(index);
    e.dataTransfer.effectAllowed = 'move';
    e.dataTransfer.setData('text/plain', String(index));
  };

  const handleSlotDragOver = (e: React.DragEvent<HTMLElement>, index: number) => {
    e.preventDefault();
    e.dataTransfer.dropEffect = 'move';
    if (dragOverIdx !== index) setDragOverIdx(index);
  };

  const handleSlotDrop = (e: React.DragEvent<HTMLElement>, targetIndex: number) => {
    e.preventDefault();
    const raw = e.dataTransfer.getData('text/plain');
    const sourceIndex = dragSlotIdx !== null ? dragSlotIdx : raw !== '' ? Number(raw) : -1;
    if (sourceIndex >= 0 && sourceIndex < slots.length && sourceIndex !== targetIndex) {
      reorderSlots(sourceIndex, targetIndex);
    }
    setDragSlotIdx(null);
    setDragOverIdx(null);
  };

  const handleSlotDragEnd = () => {
    setDragSlotIdx(null);
    setDragOverIdx(null);
  };

  const handleSaveTemplate = async (e: React.FormEvent) => {
    e.preventDefault();
    setFormError('');

    if (!templateName.trim()) {
      setFormError('请填写充值模板名称');
      return;
    }
    if (slots.length < 4 || slots.length > 8) {
      setFormError('每个模板配置的卡位数量必须在 4 到 8 个之间');
      return;
    }

    for (let i = 0; i < slots.length; i++) {
      const slot = slots[i];
      const amt = getSlotAmount(slot);
      if (!(amt > 0)) {
        setSelectedSlotIdx(i);
        setFormError(`卡位 ${i + 1} 的售价必须大于 $0.00`);
        return;
      }
      if (normalizeSlotType(slot.type) === 'single' && !(Number(slot.coins) > 0)) {
        setSelectedSlotIdx(i);
        setFormError(`卡位 ${i + 1} 的基础金币数必须大于 0`);
        return;
      }
      if (normalizeSlotType(slot.type) === 'single' && !isSlotBonusValid(slot)) {
        setSelectedSlotIdx(i);
        setFormError(
          Number(slot.bonus) < 0 || !Number.isFinite(Number(slot.bonus))
            ? `卡位 ${i + 1} 的赠送金币数不能为负数`
            : `卡位 ${i + 1} 的赠送金币数不能超过充值金币数的 1.5 倍（上限 ${getSlotMaxBonus(slot)} 金币）`,
        );
        return;
      }
      if (normalizeSlotType(slot.type) === 'subscription' && (!slot.vip_name.trim() || !slot.vip_desc.trim())) {
        setSelectedSlotIdx(i);
        setFormError(`卡位 ${i + 1} 请完整填写会员卡名称与权益描述`);
        return;
      }
      if (normalizeSlotType(slot.type) === 'whole_book' && (!slot.vip_name.trim() || !slot.vip_desc.trim())) {
        setSelectedSlotIdx(i);
        setFormError(`卡位 ${i + 1} 请完整填写整本购买的商品名称与权益描述`);
        return;
      }
    }

    const finalSlots = slots.map(({ _uid: _ignored, ...s }, idx) => {
      const amount = getSlotAmount(s);
      return {
        ...s,
        slot_index: idx + 1,
        price: `$${amount.toFixed(2)}`,
        price_cents: Math.round(amount * 100),
      };
    });

    const payload = {
      name: templateName.trim(),
      is_default: isDefaultTemplate,
      slots: finalSlots,
    };

    setSaving(true);
    try {
      if (editID !== null) {
        await apiRequest('PUT', `/recharge-templates/${editID}`, payload);
        window.showToast?.(`模板「${payload.name}」修改成功`, 'success');
      } else {
        await apiRequest('POST', '/recharge-templates', payload);
        window.showToast?.(`模板「${payload.name}」创建成功`, 'success');
      }
      setEditID(null);
      hydratedRouteKeyRef.current = null;
      navigate('/recharge-templates');
      void fetchTemplates();
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : '保存充值模板失败，请重试';
      setFormError(msg);
      window.showToast?.(msg, 'error');
    } finally {
      setSaving(false);
    }
  };

  const filteredTemplates = templates.filter(
    (t) =>
      String(t.id).includes(searchQuery.trim()) ||
      t.name.toLowerCase().includes(searchQuery.trim().toLowerCase()),
  );
  const startIndex = (currentPage - 1) * pageSize;
  const paginatedTemplates = filteredTemplates.slice(startIndex, startIndex + pageSize);

  const activeSlot = slots[selectedSlotIdx] || slots[0];
  const activeSlotType = activeSlot ? normalizeSlotType(activeSlot.type) : 'single';

  // Mirror real H5 Reader grouping (reader-front/src/pages/Recharge.tsx)
  const subscriptionPreviewPacks = slots
    .map((slot, idx) => ({ slot, idx }))
    .filter(({ slot }) => normalizeSlotType(slot.type) === 'subscription');

  const coinAndBookPreviewPacks = slots
    .map((slot, idx) => ({ slot, idx }))
    .filter(({ slot }) => {
      const t = normalizeSlotType(slot.type);
      return t === 'single' || t === 'whole_book';
    });

  const awaitingInitialHydration =
    isEditorPage &&
    !isInvalidEditRoute &&
    hydratedRouteKeyRef.current !== routeKey &&
    (isEditRoute || parsedCopyFromId !== null) &&
    loading;

  return (
    <div className="space-y-4 animate-fade-in">
      {!isEditorPage ? (
        /* ROUTE A: TEMPLATES LIST PAGE (/recharge-templates) */
        <div className="space-y-4">
          <search className="flex flex-wrap items-center justify-between gap-2.5">
            <SearchInput
              className="w-full sm:w-80"
              ariaLabel="搜索模板名称或 ID"
              placeholder="搜索模板名称或 ID..."
              value={searchQuery}
              onChange={(val) => {
                setSearchQuery(val);
                setCurrentPage(1);
              }}
            />
            <div className="flex items-center gap-2.5 text-xs text-[var(--cms-muted-fg)]">
              <span className="tabular-nums">共 {filteredTemplates.length} 套模板</span>
              <ViewOptionsPopover density={density} onDensityChange={setDensity} />
              <button
                type="button"
                onClick={startCreateTemplate}
                className="btn-primary h-9 px-3.5 text-xs whitespace-nowrap"
              >
                <Plus size={14} aria-hidden="true" />
                <span>新建模板</span>
              </button>
            </div>
          </search>

          <TableRegion
            ariaLabel="充值模板列表"
            footer={
              <Pagination
                page={currentPage}
                pageSize={pageSize}
                total={filteredTemplates.length}
                onPageChange={setCurrentPage}
                itemLabel="套模板"
              />
            }
          >
            <table className="cms-table min-w-[820px]" data-density={density}>
              <thead>
                <tr>
                  <th scope="col">模板名称</th>
                  <th scope="col">档位结构摘要</th>
                  <th scope="col">价格区间 &amp; 最高赠送</th>
                  <th scope="col">状态</th>
                  <th scope="col">更新时间</th>
                  <th scope="col" className="text-right w-px whitespace-nowrap">
                    操作
                  </th>
                </tr>
              </thead>
              <tbody>
                {loading ? (
                  <SkeletonRows rows={5} cols={6} />
                ) : paginatedTemplates.length === 0 ? (
                  <tr>
                    <td colSpan={6} className="py-16 text-center text-[var(--cms-muted-fg)]">
                      {searchQuery.trim() ? '暂无匹配的充值模板' : '暂未创建充值模板，点击右上角「新建模板」开始配置'}
                    </td>
                  </tr>
                ) : (
                  paginatedTemplates.map((t) => {
                    const meta = summarizeTemplate(t);
                    return (
                      <tr key={t.id}>
                        <td>
                          <div className="space-y-0.5">
                            <button
                              type="button"
                              onClick={() => openEditorForTemplate(t)}
                              className="focus-ring text-left text-[var(--cms-fg)] hover:underline cursor-pointer transition-colors"
                            >
                              {t.name}
                            </button>
                            <div className="text-[var(--cms-muted-fg)] tabular-nums">
                              #{t.id} <span className="mx-1">·</span> {t.slots?.length || 0} 个卡位
                            </div>
                          </div>
                        </td>
                        <td className="text-[var(--cms-fg)]">{meta.summaryText}</td>
                        <td className="tabular-nums">
                          <div className="text-[var(--cms-fg)]">{meta.priceRange}</div>
                          <div className="text-[var(--cms-muted-fg)] mt-0.5">
                            {meta.maxBonusPct > 0 ? `最高赠送 +${meta.maxBonusPct}%` : '无额外赠送'}
                          </div>
                        </td>
                        <td>
                          {t.is_default ? (
                            <StatusDot tone="primary" label="全站默认" />
                          ) : (
                            <StatusDot tone="muted" label="备用方案" />
                          )}
                        </td>
                        <td className="tabular-nums text-[var(--cms-muted-fg)]">
                          {new Date(t.updated_at).toLocaleString('zh-CN', { hour12: false })}
                        </td>
                        <td className="text-right whitespace-nowrap">
                          <div className="inline-flex items-center justify-end gap-0.5 whitespace-nowrap">
                            <button
                              type="button"
                              onClick={() => openEditorForTemplate(t)}
                              className="btn-detail whitespace-nowrap"
                            >
                              编辑
                            </button>
                            <RowActionMenu
                              ariaLabel={`模板 ${t.name} 更多操作`}
                              items={[
                                ...(!t.is_default
                                  ? [{
                                      label: '设为全站默认',
                                      tone: 'success' as const,
                                      onClick: () => void handleSetDefault(t.id),
                                    }]
                                  : []),
                                {
                                  label: '复制为新模板',
                                  onClick: () => openCopyTemplate(t),
                                },
                                ...(!t.is_default
                                  ? [{
                                      label: '删除模板',
                                      tone: 'danger' as const,
                                      dividerBefore: true,
                                      onClick: () => handleDelete(t),
                                    }]
                                  : []),
                              ]}
                            />
                          </div>
                        </td>
                      </tr>
                    );
                  })
                )}
              </tbody>
            </table>
          </TableRegion>
        </div>
      ) : awaitingInitialHydration ? (
        <div className="cms-card p-8 space-y-4">
          <div className="flex items-center justify-between gap-3">
            <span className="text-sm text-[var(--cms-muted-fg)]">正在加载充值模板配置...</span>
            <button
              type="button"
              onClick={() => navigate('/recharge-templates')}
              className="btn-secondary whitespace-nowrap"
            >
              <ChevronLeft size={15} aria-hidden="true" />
              <span>返回列表</span>
            </button>
          </div>
          <div className="grid grid-cols-1 lg:grid-cols-12 gap-4 animate-pulse">
            <div className="lg:col-span-7 h-72 rounded-xl bg-[var(--cms-surface-muted)]" />
            <div className="lg:col-span-5 h-72 rounded-xl bg-[var(--cms-surface-muted)]" />
          </div>
        </div>
      ) : missingEditTemplate ? (
        <div className="cms-card p-10 text-center space-y-4">
          <div className="text-sm font-medium text-[var(--cms-fg)]">
            未找到指定的充值模板（ID 无效或已被删除）
          </div>
          <div>
            <button
              type="button"
              onClick={() => navigate('/recharge-templates')}
              className="btn-secondary inline-flex items-center gap-1.5 whitespace-nowrap"
            >
              <ChevronLeft size={15} aria-hidden="true" />
              <span>返回模板列表</span>
            </button>
          </div>
        </div>
      ) : (
        /* ROUTE B: STANDALONE TEMPLATE EDITOR + LIVE H5 PREVIEW (/recharge-templates/new | /:id/edit) */
        <form onSubmit={handleSaveTemplate} className="space-y-6">
          {/* Top Meta & Save Strip */}
          <div className="cms-card p-5 flex flex-wrap items-center justify-between gap-4">
            <div className="flex flex-wrap items-center gap-5 flex-1 min-w-[260px]">
              <label className="flex-1 min-w-[220px] max-w-sm space-y-1.5">
                <span className="block text-xs font-medium text-[var(--cms-muted-fg)]">
                  模板名称 <span className="text-[var(--cms-error)]">*</span>
                </span>
                <input
                  type="text"
                  required
                  placeholder="例如：欧美通用默认模板 / 新客首充高转化方案"
                  value={templateName}
                  onChange={(e) => {
                    setFormError('');
                    setTemplateName(e.target.value);
                  }}
                  className="input-field"
                />
              </label>

              <label className="inline-flex items-center gap-2 pt-5 text-xs font-medium text-[var(--cms-fg)] cursor-pointer select-none">
                <input
                  type="checkbox"
                  checked={isDefaultTemplate}
                  onChange={(e) => setIsDefaultTemplate(e.target.checked)}
                  className="w-4 h-4 rounded cursor-pointer"
                />
                <span>设为全站默认模板</span>
              </label>
            </div>

            <div className="flex items-center gap-2.5 shrink-0">
              <button
                type="button"
                onClick={() => navigate('/recharge-templates')}
                className="btn-secondary whitespace-nowrap"
              >
                <ChevronLeft size={15} aria-hidden="true" />
                <span>返回列表</span>
              </button>
              <button type="submit" disabled={saving} className="btn-primary px-5 whitespace-nowrap">
                <Save size={15} aria-hidden="true" />
                <span>{saving ? '保存中...' : '保存模板配置'}</span>
              </button>
            </div>
          </div>

          {formError && (
            <div
              role="alert"
              className="px-4 py-3 rounded-xl border text-xs font-medium"
              style={{
                backgroundColor: 'color-mix(in srgb, var(--cms-error) 10%, transparent)',
                borderColor: 'color-mix(in srgb, var(--cms-error) 28%, transparent)',
                color: 'var(--cms-error)',
              }}
            >
              {formError}
            </div>
          )}

          {/* Split Workspace */}
          <p role="status" className="visually-hidden">
            {reorderStatus}
          </p>
          <div className="grid grid-cols-1 xl:grid-cols-[1fr_390px] gap-6 items-start">
            {/* LEFT: Slot Sequence List (Draggable, Click Edit -> Modal) */}
            <div className="min-w-0">
              <div className="cms-card overflow-hidden">
                <div className="px-5 py-4 border-b border-[var(--cms-border)] flex flex-wrap items-center justify-between gap-3">
                  <div className="flex items-center gap-2.5">
                    <h2 className="text-sm font-semibold text-[var(--cms-fg)] m-0">卡位顺序与结构</h2>
                    <span className="tabular-nums text-xs text-[var(--cms-muted-fg)]">{slots.length} / 8</span>
                    <span className="hidden sm:inline text-xs text-[var(--cms-muted-fg)]">
                      · 支持鼠标拖拽调整顺序，点击「编辑」弹窗修改属性
                    </span>
                  </div>
                  <button
                    type="button"
                    disabled={slots.length >= 8}
                    onClick={handleAddSlot}
                    className="btn-secondary h-8 px-3 text-xs"
                  >
                    <Plus size={14} aria-hidden="true" />
                    <span>添加卡位</span>
                  </button>
                </div>

                <div className="divide-y divide-[color-mix(in_srgb,var(--cms-border)_60%,transparent)]">
                  {slots.map((slot, idx) => {
                    const active = idx === selectedSlotIdx;
                    const isDragging = dragSlotIdx === idx;
                    const isDragTarget = dragOverIdx === idx && dragSlotIdx !== null && dragSlotIdx !== idx;
                    const type = normalizeSlotType(slot.type);
                    const invalid = !isSlotBonusValid(slot) || (type === 'single' && !(Number(slot.coins) > 0));
                    const amount = getSlotAmount(slot);
                    const pct = slot.coins > 0 && slot.bonus > 0 ? Math.round((slot.bonus / slot.coins) * 100) : 0;

                    return (
                      <div
                        key={slot._uid || slot.id || idx}
                        draggable={!saving}
                        onDragStart={(e) => handleSlotDragStart(e, idx)}
                        onDragOver={(e) => handleSlotDragOver(e, idx)}
                        onDrop={(e) => handleSlotDrop(e, idx)}
                        onDragEnd={handleSlotDragEnd}
                        role="button"
                        tabIndex={0}
                        onClick={() => setSelectedSlotIdx(idx)}
                        onDoubleClick={() => openEditSlotModal(idx)}
                        onKeyDown={(e) => {
                          if (e.target !== e.currentTarget) return;
                          if (e.key === 'Enter' || e.key === ' ') {
                            e.preventDefault();
                            setSelectedSlotIdx(idx);
                          }
                        }}
                        className={`px-4 py-3.5 flex items-center justify-between gap-3 cursor-grab active:cursor-grabbing transition-all ${
                          isDragging
                            ? 'opacity-45 bg-[var(--cms-content)]'
                            : isDragTarget
                              ? 'ring-2 ring-inset ring-[var(--cms-primary)] bg-[color-mix(in_srgb,var(--cms-primary)_10%,var(--cms-card))]'
                              : active
                                ? 'bg-[color-mix(in_srgb,var(--cms-primary)_7%,var(--cms-card))]'
                                : 'hover:bg-[var(--cms-content)]'
                        }`}
                      >
                        <div className="flex items-center gap-3 min-w-0 flex-1">
                          <span
                            aria-hidden="true"
                            className="text-[var(--cms-placeholder)] hover:text-[var(--cms-muted-fg)] shrink-0 inline-flex items-center"
                          >
                            <GripVertical size={15} />
                          </span>

                          <span
                            className={`w-7 h-7 rounded-lg text-xs font-medium tabular-nums flex items-center justify-center shrink-0 ${
                              active
                                ? 'nav-active-item'
                                : 'bg-[var(--cms-content)] text-[var(--cms-muted-fg)]'
                            }`}
                          >
                            0{idx + 1}
                          </span>

                          <div className="min-w-0 flex-1 space-y-0.5">
                            <div className="flex flex-wrap items-center gap-2 text-xs">
                              <span className="inline-flex items-center gap-1.5 text-[var(--cms-muted-fg)] shrink-0">
                                <span
                                  className="w-1.5 h-1.5 rounded-full"
                                  style={{
                                    backgroundColor:
                                      type === 'subscription'
                                        ? 'var(--cms-primary)'
                                        : type === 'whole_book'
                                          ? 'var(--cms-success)'
                                          : 'var(--cms-muted-fg)',
                                  }}
                                />
                                {type === 'subscription'
                                  ? CYCLE_LABELS[slot.subscription_cycle || slot.vip_duration || 'month'] || 'VIP 订阅'
                                  : type === 'whole_book'
                                    ? '整本购买'
                                    : '金币包'}
                              </span>

                              <span className="text-[var(--cms-border)]">·</span>

                              {type === 'single' ? (
                                <>
                                  <span className="text-[var(--cms-fg)] tabular-nums">{slot.coins} 金币</span>
                                  {slot.bonus > 0 ? (
                                    <span
                                      className={`tabular-nums ${
                                        invalid ? 'text-[var(--cms-error)]' : 'text-[var(--cms-muted-fg)]'
                                      }`}
                                    >
                                      +{slot.bonus} 赠 (+{pct}%)
                                    </span>
                                  ) : (
                                    <span className="text-[var(--cms-muted-fg)]">无额外赠送</span>
                                  )}
                                  {invalid && <span className="text-[var(--cms-error)]">· 配置异常</span>}
                                </>
                              ) : (
                                <span className="text-[var(--cms-fg)] truncate">
                                  {slot.vip_name || (type === 'subscription' ? 'VIP 会员卡' : '整本买断')}
                                </span>
                              )}
                            </div>

                            {type !== 'single' && slot.vip_desc && (
                              <div className="text-xs text-[var(--cms-muted-fg)] truncate">{slot.vip_desc}</div>
                            )}
                          </div>
                        </div>

                        <div
                          className="flex items-center gap-2 shrink-0"
                          onClick={(e) => e.stopPropagation()}
                          onDoubleClick={(e) => e.stopPropagation()}
                        >
                          <span className="text-xs tabular-nums text-[var(--cms-fg)] mr-1">
                            ${amount.toFixed(2)}
                          </span>

                          <button
                            type="button"
                            onClick={() => openEditSlotModal(idx)}
                            className="btn-detail whitespace-nowrap"
                          >
                            编辑
                          </button>

                          <button
                            type="button"
                            disabled={slots.length <= 4}
                            onClick={() => handleDeleteSlot(idx)}
                            title={slots.length <= 4 ? '模板至少保留 4 个卡位' : '移除卡位'}
                            aria-label={`移除卡位 ${idx + 1}`}
                            className="focus-ring w-7 h-7 rounded-md hover:bg-[color-mix(in_srgb,var(--cms-error)_12%,transparent)] text-[var(--cms-muted-fg)] hover:text-[var(--cms-error)] flex items-center justify-center cursor-pointer disabled:opacity-25 disabled:cursor-not-allowed"
                          >
                            <Trash2 size={13} aria-hidden="true" />
                          </button>
                        </div>
                      </div>
                    );
                  })}
                </div>
              </div>
            </div>

            {/* RIGHT: Real H5 Reader Recharge Preview (Draggable + Double-Click Edit) */}
            <div className="xl:sticky xl:top-20 space-y-2.5">
              <div className="flex items-center justify-between gap-2 px-1">
                <span className="text-xs text-[var(--cms-muted-fg)]">
                  H5 Reader 真实样式预览（可拖拽排序 · 双击编辑）
                </span>
                <div className="flex items-center rounded-lg border border-[var(--cms-border)] bg-[var(--cms-card)] p-0.5 text-xs shrink-0">
                  <button
                    type="button"
                    onClick={() => setH5Theme('light')}
                    className={`px-2 py-0.5 rounded-md cursor-pointer transition-colors ${
                      h5Theme === 'light'
                        ? 'bg-[var(--cms-secondary)] text-[var(--cms-fg)] font-medium'
                        : 'text-[var(--cms-muted-fg)]'
                    }`}
                  >
                    日间
                  </button>
                  <button
                    type="button"
                    onClick={() => setH5Theme('dark')}
                    className={`px-2 py-0.5 rounded-md cursor-pointer transition-colors ${
                      h5Theme === 'dark'
                        ? 'bg-[var(--cms-secondary)] text-[var(--cms-fg)] font-medium'
                        : 'text-[var(--cms-muted-fg)]'
                    }`}
                  >
                    夜间
                  </button>
                </div>
              </div>

              {/* H5 Phone Container mirroring reader-front /recharge */}
              <div
                className={`rounded-[26px] border border-[var(--cms-border)] overflow-hidden transition-colors ${
                  h5Theme === 'dark' ? 'h5-preview-dark' : 'h5-preview-light'
                }`}
                style={{ boxShadow: 'var(--shadow-sm)' }}
              >
                {/* Real H5 Reader Sticky Header */}
                <div
                  className="px-3.5 py-3 flex items-center justify-between"
                  style={{
                    borderBottom: '1px solid var(--h5-border)',
                    backgroundColor: 'var(--h5-bg-primary)',
                  }}
                >
                  <span
                    aria-hidden="true"
                    className="w-7 h-7 inline-flex items-center justify-center rounded-lg"
                    style={{ color: 'var(--h5-text-primary)' }}
                  >
                    <ChevronLeft size={18} strokeWidth={1.8} />
                  </span>
                  <span
                    style={{
                      fontFamily: "'Iowan Old Style', Georgia, serif",
                      fontSize: '0.95rem',
                      fontWeight: 600,
                      letterSpacing: '-0.02em',
                      color: 'var(--h5-text-primary)',
                    }}
                  >
                    Membership &amp; Coins
                  </span>
                  <span className="w-7" />
                </div>

                {/* Real H5 Reader Scrollable Body */}
                <div className="p-3.5 space-y-4 max-h-[600px] overflow-y-auto">
                  {/* Section 1: UNLIMITED MEMBERSHIP */}
                  {subscriptionPreviewPacks.length > 0 && (
                    <section className="space-y-2">
                      <div
                        style={{
                          fontSize: '0.6875rem',
                          fontWeight: 600,
                          textTransform: 'uppercase',
                          letterSpacing: '0.1em',
                          color: 'var(--h5-text-tertiary)',
                        }}
                      >
                        Unlimited Membership
                      </div>

                      <div className="flex flex-col gap-2">
                        {subscriptionPreviewPacks.map(({ slot, idx }) => {
                          const isSelected = idx === selectedSlotIdx;
                          const isDragging = dragSlotIdx === idx;
                          const isDragTarget = dragOverIdx === idx && dragSlotIdx !== null && dragSlotIdx !== idx;
                          const amt = getSlotAmount(slot).toFixed(2);
                          const cycle = slot.subscription_cycle || slot.vip_duration || 'month';
                          const fallbackName =
                            cycle === 'day'
                              ? 'VIP Daily Pass'
                              : cycle === 'week'
                                ? 'VIP Weekly Pass'
                                : 'VIP Monthly Pass';

                          return (
                            <div
                              key={slot._uid || slot.id || idx}
                              draggable={!saving}
                              onDragStart={(e) => handleSlotDragStart(e, idx)}
                              onDragOver={(e) => handleSlotDragOver(e, idx)}
                              onDrop={(e) => handleSlotDrop(e, idx)}
                              onDragEnd={handleSlotDragEnd}
                              role="button"
                              tabIndex={0}
                              onClick={() => setSelectedSlotIdx(idx)}
                              onDoubleClick={() => openEditSlotModal(idx)}
                              onKeyDown={(e) => {
                                if (e.key === 'Enter' || e.key === ' ') {
                                  e.preventDefault();
                                  setSelectedSlotIdx(idx);
                                }
                              }}
                              title="点击选中 · 拖拽调整顺序 · 双击编辑属性"
                              className={`text-left flex items-center justify-between gap-3 cursor-grab active:cursor-grabbing select-none transition-all ${
                                isDragging ? 'opacity-45' : ''
                              }`}
                              style={{
                                borderRadius: '14px',
                                padding: '12px 13px',
                                backgroundColor: isSelected ? 'var(--h5-hero-bg)' : 'var(--h5-bg-secondary)',
                                color: isSelected ? 'var(--h5-hero-text)' : 'var(--h5-text-primary)',
                                border: isDragTarget
                                  ? '1.5px dashed var(--h5-accent)'
                                  : isSelected
                                    ? '1.5px solid var(--h5-accent)'
                                    : '1px solid var(--h5-border)',
                              }}
                            >
                              <div className="flex items-start gap-2.5 min-w-0 flex-1">
                                <Crown
                                  size={16}
                                  strokeWidth={1.8}
                                  aria-hidden="true"
                                  style={{
                                    color: isSelected ? '#E2BB6B' : 'var(--h5-accent)',
                                    marginTop: '2px',
                                    flexShrink: 0,
                                  }}
                                />
                                <div className="min-w-0 flex-1">
                                  <div className="flex items-center gap-1.5 flex-wrap">
                                    <span
                                      className="truncate"
                                      style={{
                                        fontFamily: "'Iowan Old Style', Georgia, serif",
                                        fontSize: '0.875rem',
                                        fontWeight: 600,
                                      }}
                                    >
                                      {slot.vip_name || fallbackName}
                                    </span>
                                    <span
                                      style={{
                                        fontSize: '0.625rem',
                                        backgroundColor: isSelected ? 'rgba(212, 174, 104, 0.2)' : 'var(--h5-accent-light)',
                                        color: isSelected ? '#E2BB6B' : 'var(--h5-accent-text)',
                                        fontWeight: 600,
                                        padding: '1px 7px',
                                        borderRadius: '999px',
                                        textTransform: 'uppercase',
                                      }}
                                    >
                                      {cycle}
                                    </span>
                                  </div>
                                  <div
                                    className="line-clamp-2 mt-1"
                                    style={{
                                      fontSize: '0.6875rem',
                                      color: isSelected ? 'rgba(250, 248, 245, 0.74)' : 'var(--h5-text-secondary)',
                                      lineHeight: 1.4,
                                    }}
                                  >
                                    {slot.vip_desc || 'Unlimited reading across all titles. Auto-renews, cancel anytime.'}
                                  </div>
                                </div>
                              </div>

                              <div className="text-right shrink-0 tabular-nums">
                                <div style={{ fontSize: '0.9375rem', fontWeight: 700 }}>${amt}</div>
                                <div style={{ fontSize: '0.625rem', opacity: 0.7 }}>/{cycle}</div>
                              </div>
                            </div>
                          );
                        })}
                      </div>
                    </section>
                  )}

                  {/* Section 2: SINGLE PURCHASES (Coin Packs & Whole Book 2-col Grid) */}
                  {coinAndBookPreviewPacks.length > 0 && (
                    <section className="space-y-2">
                      <div className="flex items-baseline justify-between">
                        <div
                          style={{
                            fontSize: '0.6875rem',
                            fontWeight: 600,
                            textTransform: 'uppercase',
                            letterSpacing: '0.1em',
                            color: 'var(--h5-text-tertiary)',
                          }}
                        >
                          Single Purchases
                        </div>
                        <span style={{ fontSize: '0.6875rem', color: 'var(--h5-text-tertiary)' }}>Never expire</span>
                      </div>

                      <div className="grid grid-cols-2 gap-2.5">
                        {coinAndBookPreviewPacks.map(({ slot, idx }) => {
                          const isSelected = idx === selectedSlotIdx;
                          const isDragging = dragSlotIdx === idx;
                          const isDragTarget = dragOverIdx === idx && dragSlotIdx !== null && dragSlotIdx !== idx;
                          const type = normalizeSlotType(slot.type);
                          const invalid = !isSlotBonusValid(slot) || (type === 'single' && !(Number(slot.coins) > 0));
                          const amt = getSlotAmount(slot).toFixed(2);
                          const totalCoins = (Number(slot.coins) || 0) + (Number(slot.bonus) || 0);

                          return (
                            <div
                              key={slot._uid || slot.id || idx}
                              draggable={!saving}
                              onDragStart={(e) => handleSlotDragStart(e, idx)}
                              onDragOver={(e) => handleSlotDragOver(e, idx)}
                              onDrop={(e) => handleSlotDrop(e, idx)}
                              onDragEnd={handleSlotDragEnd}
                              role="button"
                              tabIndex={0}
                              onClick={() => setSelectedSlotIdx(idx)}
                              onDoubleClick={() => openEditSlotModal(idx)}
                              onKeyDown={(e) => {
                                if (e.key === 'Enter' || e.key === ' ') {
                                  e.preventDefault();
                                  setSelectedSlotIdx(idx);
                                }
                              }}
                              title="点击选中 · 拖拽调整顺序 · 双击编辑属性"
                              className={`text-left flex flex-col justify-between gap-2.5 cursor-grab active:cursor-grabbing select-none transition-all ${
                                isDragging ? 'opacity-45' : ''
                              }`}
                              style={{
                                backgroundColor: 'var(--h5-bg-secondary)',
                                color: 'var(--h5-text-primary)',
                                borderRadius: '13px',
                                padding: '11px 10px',
                                border: isDragTarget
                                  ? '1.5px dashed var(--h5-accent)'
                                  : invalid
                                    ? '1.5px solid #dc2626'
                                    : isSelected
                                      ? '1.5px solid var(--h5-accent)'
                                      : '1px solid var(--h5-border)',
                                boxShadow: isSelected && !invalid && !isDragTarget ? '0 0 0 1px var(--h5-accent)' : 'none',
                              }}
                            >
                              <div className="flex items-center justify-between gap-1">
                                {type === 'whole_book' ? (
                                  <span
                                    style={{
                                      backgroundColor: 'var(--h5-accent-light)',
                                      color: 'var(--h5-accent-text)',
                                      fontSize: '0.625rem',
                                      fontWeight: 600,
                                      padding: '2px 6px',
                                      borderRadius: '999px',
                                    }}
                                  >
                                    Full Book
                                  </span>
                                ) : Number(slot.bonus) > 0 ? (
                                  <span
                                    className="tabular-nums"
                                    style={{
                                      backgroundColor: invalid ? 'rgba(220,38,38,0.12)' : 'var(--h5-accent-light)',
                                      color: invalid ? '#dc2626' : 'var(--h5-accent-text)',
                                      fontSize: '0.625rem',
                                      fontWeight: 600,
                                      padding: '2px 6px',
                                      borderRadius: '999px',
                                    }}
                                  >
                                    +{slot.bonus} bonus
                                  </span>
                                ) : (
                                  <span />
                                )}

                                <span
                                  aria-hidden="true"
                                  style={{
                                    width: '15px',
                                    height: '15px',
                                    borderRadius: '50%',
                                    border: isSelected ? 'none' : '1.5px solid var(--h5-border-strong)',
                                    backgroundColor: isSelected ? 'var(--h5-accent)' : 'transparent',
                                    color: '#FFFFFF',
                                    display: 'inline-flex',
                                    alignItems: 'center',
                                    justifyContent: 'center',
                                    flexShrink: 0,
                                  }}
                                >
                                  {isSelected && <Check size={10} strokeWidth={2.8} />}
                                </span>
                              </div>

                              {type === 'whole_book' ? (
                                <div>
                                  <div className="flex items-center gap-1.5">
                                    <BookOpen
                                      size={14}
                                      strokeWidth={1.9}
                                      style={{ color: 'var(--h5-accent)', flexShrink: 0 }}
                                      aria-hidden="true"
                                    />
                                    <span
                                      className="truncate"
                                      style={{
                                        fontSize: '0.8125rem',
                                        fontWeight: 700,
                                        color: 'var(--h5-text-primary)',
                                        lineHeight: 1.25,
                                      }}
                                    >
                                      {slot.vip_name || 'Unlock Entire Novel'}
                                    </span>
                                  </div>
                                  <span
                                    className="block truncate mt-0.5"
                                    style={{ fontSize: '0.6875rem', color: 'var(--h5-text-tertiary)' }}
                                  >
                                    Current Novel
                                  </span>
                                </div>
                              ) : (
                                <div>
                                  <div className="flex items-center gap-1.5">
                                    <H5GoldCoin size={15} />
                                    <span
                                      className="tabular-nums"
                                      style={{ fontSize: '1rem', fontWeight: 700, color: 'var(--h5-text-primary)' }}
                                    >
                                      {Number(slot.coins).toLocaleString()}
                                    </span>
                                  </div>
                                  <span
                                    className="block tabular-nums mt-0.5"
                                    style={{ fontSize: '0.6875rem', color: 'var(--h5-text-tertiary)' }}
                                  >
                                    Total: {totalCoins.toLocaleString()} Coins
                                  </span>
                                </div>
                              )}

                              <div
                                className="tabular-nums text-center"
                                style={{
                                  fontSize: '0.75rem',
                                  fontWeight: 600,
                                  backgroundColor: isSelected ? 'var(--h5-cta-bg)' : 'var(--h5-bg-tertiary)',
                                  color: isSelected ? 'var(--h5-cta-text)' : 'var(--h5-text-primary)',
                                  padding: '6px 0',
                                  borderRadius: '8px',
                                }}
                              >
                                ${amt}
                              </div>
                            </div>
                          );
                        })}
                      </div>
                    </section>
                  )}

                  {/* Real PayPal Pill CTA */}
                  {activeSlot && (
                    <div
                      className="w-full flex items-center justify-center gap-2 tabular-nums"
                      style={{
                        minHeight: '44px',
                        backgroundColor: '#F5C344',
                        color: '#0F265C',
                        borderRadius: '999px',
                        fontSize: '0.8125rem',
                        fontWeight: 700,
                      }}
                    >
                      <svg width="17" height="17" viewBox="0 0 24 24" fill="none" aria-hidden="true">
                        <path
                          d="M7.076 21.337H2.47a.64.64 0 0 1-.633-.74L4.931 2.378A.853.853 0 0 1 5.774 1.7h7.24c3.483 0 5.69 1.642 5.093 5.372-.44 2.75-2.228 4.316-4.945 4.316h-2.14l-1.07 6.776-.026.173a.853.853 0 0 1-.85.8z"
                          fill="#003087"
                        />
                        <path
                          d="M8.766 18.066l1.24-7.852h2.64c2.717 0 4.505-1.566 4.945-4.316.597-3.73-1.61-5.372-5.093-5.372H5.258a.853.853 0 0 0-.843.678L1.321 19.723a.64.64 0 0 0 .633.74h4.606l1.07-6.776.026-.173a.853.853 0 0 1 .85-.8z"
                          fill="#0079C1"
                        />
                        <path
                          d="M9.13 14.77l1.07-6.776h2.14c2.717 0 4.505-1.566 4.945-4.316.14-.876.105-1.614-.078-2.217C16.89 2.532 15.65 2 13.904 2H8.354a.853.853 0 0 0-.843.678L4.417 21.2a.64.64 0 0 0 .633.74h4.606l1.07-6.776.026-.173a.853.853 0 0 1 .85-.8z"
                          fill="#00457C"
                        />
                      </svg>
                      <span>
                        {activeSlotType === 'subscription'
                          ? `Subscribe with PayPal · $${getSlotAmount(activeSlot).toFixed(2)}`
                          : `Pay with PayPal · $${getSlotAmount(activeSlot).toFixed(2)}`}
                      </span>
                    </div>
                  )}

                  {/* Real H5 Notes & Legal Footer */}
                  <div
                    style={{
                      backgroundColor: 'var(--h5-bg-secondary)',
                      borderRadius: '12px',
                      padding: '10px 12px',
                      border: '1px solid var(--h5-border)',
                      fontSize: '0.6875rem',
                      color: 'var(--h5-text-secondary)',
                      lineHeight: 1.55,
                    }}
                  >
                    <p className="m-0">
                      • <strong>VIP Unlimited Pass:</strong> Instant access to all novel chapters across the site without consuming coins.
                    </p>
                    <p className="m-0">
                      • <strong>Coin Top-up:</strong> Permanent coins to unlock individual chapters across StarNovel.
                    </p>
                    <p className="m-0">
                      • Need help?{' '}
                      <span style={{ color: 'var(--h5-accent-text)', fontWeight: 600, textDecoration: 'underline' }}>
                        Contact Customer Support
                      </span>
                      .
                    </p>
                  </div>

                  <div
                    className="flex flex-wrap justify-center items-center gap-x-2 gap-y-1 pb-1"
                    style={{ fontSize: '0.6875rem', color: 'var(--h5-text-tertiary)' }}
                  >
                    <span className="underline">Terms of Service</span>
                    <span aria-hidden="true">·</span>
                    <span className="underline">Privacy Policy</span>
                    <span aria-hidden="true">·</span>
                    <span className="underline">Refund Policy</span>
                    <span aria-hidden="true">·</span>
                    <span className="underline">Contact Us</span>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </form>
      )}

      {/* Centered Slot Edit / Add Modal */}
      <CmsDialog
        open={Boolean(editingSlotTarget !== null && slotDraft)}
        onClose={closeSlotModal}
        variant="modal"
        size="md"
        closedBy="any"
        title={
          editingSlotTarget === 'new'
            ? `新增卡位 (第 ${slots.length + 1} 位)`
            : typeof editingSlotTarget === 'number'
              ? `编辑卡位 0${editingSlotTarget + 1}`
              : '编辑卡位'
        }
        subtitle="修改类型、售价、基础金币 / 赠送数量及展示标题描述，保存后即时更新右侧 H5 预览"
        footer={
          <>
            <button type="button" onClick={closeSlotModal} className="btn-secondary">
              取消
            </button>
            <button type="submit" form={slotFormId} className="btn-primary">
              {editingSlotTarget === 'new' ? '确认添加卡位' : '确认修改'}
            </button>
          </>
        }
      >
        {slotDraft && (
          <form id={slotFormId} onSubmit={handleConfirmSlotModal} className="space-y-4 text-xs">
            {/* Slot Type Switcher */}
            <div className="space-y-1.5">
              <span className="block font-medium text-[var(--cms-muted-fg)]">卡位充值类型</span>
              <div
                role="group"
                aria-label="卡位充值类型"
                className="grid grid-cols-3 gap-1 rounded-xl bg-[var(--cms-content)] border border-[var(--cms-border)] p-1"
              >
                {(
                  [
                    { id: 'single', label: '单次金币包' },
                    { id: 'subscription', label: 'VIP 周期订阅' },
                    { id: 'whole_book', label: '整本购买' },
                  ] as Array<{ id: SlotTypeNormalized; label: string }>
                ).map((tab) => {
                  const selected = normalizeSlotType(slotDraft.type) === tab.id;
                  return (
                    <button
                      key={tab.id}
                      type="button"
                      aria-pressed={selected}
                      onClick={() => applySlotDraftChange('type', tab.id)}
                      className={`focus-ring py-2 rounded-lg font-medium cursor-pointer transition-colors ${
                        selected
                          ? 'bg-[var(--cms-card)] text-[var(--cms-fg)] shadow-xs'
                          : 'text-[var(--cms-muted-fg)] hover:text-[var(--cms-fg)]'
                      }`}
                    >
                      {tab.label}
                    </button>
                  );
                })}
              </div>
            </div>

            {normalizeSlotType(slotDraft.type) === 'single' && (
              <div className="space-y-4">
                <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
                  <label className="space-y-1.5">
                    <span className="block font-medium text-[var(--cms-muted-fg)]">档位售价 (USD)</span>
                    <div className="relative">
                      <span className="absolute left-3 top-1/2 -translate-y-1/2 text-xs text-[var(--cms-muted-fg)]">$</span>
                      <input
                        type="number"
                        step="0.01"
                        min="0.01"
                        autoFocus
                        required
                        className="input-field tabular-nums"
                        style={{ paddingLeft: '26px' }}
                        value={slotDraft.price_amount ?? getSlotAmount(slotDraft)}
                        onChange={(e) =>
                          applySlotDraftChange('price_amount', e.target.value === '' ? '' : parseFloat(e.target.value))
                        }
                      />
                    </div>
                  </label>

                  <label className="space-y-1.5">
                    <div className="flex items-center justify-between gap-2">
                      <span className="font-medium text-[var(--cms-muted-fg)]">基础金币 (Coins)</span>
                      {slotDraft.coins !== Math.round(getSlotAmount(slotDraft) * 100) && (
                        <button
                          type="button"
                          onClick={() => applySlotDraftChange('coins', Math.round(getSlotAmount(slotDraft) * 100))}
                          className="text-[11px] text-[var(--cms-detail)] hover:underline cursor-pointer"
                        >
                          按 1:100 折算
                        </button>
                      )}
                    </div>
                    <input
                      type="number"
                      min="1"
                      step="1"
                      required
                      className="input-field tabular-nums"
                      value={slotDraft.coins}
                      onChange={(e) => applySlotDraftChange('coins', e.target.value)}
                    />
                  </label>
                </div>

                <label className="block space-y-1.5">
                  <div className="flex items-center justify-between">
                    <span className="font-medium text-[var(--cms-muted-fg)]">额外赠送金币 (Bonus)</span>
                    <span
                      className={`tabular-nums font-medium ${
                        !isSlotBonusValid(slotDraft) ? 'text-[var(--cms-error)]' : 'text-[var(--cms-primary)]'
                      }`}
                    >
                      {!isSlotBonusValid(slotDraft)
                        ? Number(slotDraft.bonus) < 0
                          ? '不能为负数'
                          : '超出 1.5x 上限'
                        : slotDraft.coins > 0 && slotDraft.bonus > 0
                          ? `+${Math.round((slotDraft.bonus / slotDraft.coins) * 100)}%`
                          : '不赠送'}
                    </span>
                  </div>
                  <input
                    type="number"
                    min="0"
                    step="1"
                    required
                    className={`input-field tabular-nums ${!isSlotBonusValid(slotDraft) ? 'border-[var(--cms-error)]' : ''}`}
                    value={slotDraft.bonus}
                    onChange={(e) => applySlotDraftChange('bonus', e.target.value)}
                  />
                  <div
                    className={`text-xs pt-0.5 tabular-nums ${
                      !isSlotBonusValid(slotDraft) ? 'text-[var(--cms-error)]' : 'text-[var(--cms-muted-fg)]'
                    }`}
                  >
                    总计到账 {(Number(slotDraft.coins) || 0) + (Number(slotDraft.bonus) || 0)} 金币 · 风控上限{' '}
                    {getSlotMaxBonus(slotDraft)} Bonus（基础金币 × 1.5）
                  </div>
                </label>
              </div>
            )}

            {normalizeSlotType(slotDraft.type) === 'subscription' && (
              <div className="space-y-4">
                <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
                  <label className="space-y-1.5">
                    <span className="block font-medium text-[var(--cms-muted-fg)]">订阅扣费周期</span>
                    <CustomSelect
                      width="100%"
                      value={slotDraft.subscription_cycle || slotDraft.vip_duration || 'month'}
                      onChange={(val) => applySlotDraftChange('subscription_cycle', val)}
                      options={[
                        { value: 'day', label: '日卡 · 每 1 天续费 (Daily)' },
                        { value: 'week', label: '周卡 · 每 7 天续费 (Weekly)' },
                        { value: 'month', label: '月卡 · 每 30 天续费 (Monthly)' },
                      ]}
                    />
                  </label>

                  <label className="space-y-1.5">
                    <span className="block font-medium text-[var(--cms-muted-fg)]">订阅周期售价 (USD)</span>
                    <div className="relative">
                      <span className="absolute left-3 top-1/2 -translate-y-1/2 text-xs text-[var(--cms-muted-fg)]">$</span>
                      <input
                        type="number"
                        step="0.01"
                        min="0.01"
                        required
                        className="input-field tabular-nums"
                        style={{ paddingLeft: '26px' }}
                        value={slotDraft.price_amount ?? getSlotAmount(slotDraft)}
                        onChange={(e) =>
                          applySlotDraftChange('price_amount', e.target.value === '' ? '' : parseFloat(e.target.value))
                        }
                      />
                    </div>
                  </label>
                </div>

                <label className="block space-y-1.5">
                  <span className="block font-medium text-[var(--cms-muted-fg)]">会员卡标题 *</span>
                  <input
                    type="text"
                    required
                    placeholder="例如：VIP Weekly Pass"
                    className="input-field"
                    value={slotDraft.vip_name}
                    onChange={(e) => applySlotDraftChange('vip_name', e.target.value)}
                  />
                </label>

                <label className="block space-y-1.5">
                  <span className="block font-medium text-[var(--cms-muted-fg)]">权益描述 *</span>
                  <input
                    type="text"
                    required
                    placeholder="例如：Unlimited reading across all titles."
                    className="input-field"
                    value={slotDraft.vip_desc}
                    onChange={(e) => applySlotDraftChange('vip_desc', e.target.value)}
                  />
                </label>
              </div>
            )}

            {normalizeSlotType(slotDraft.type) === 'whole_book' && (
              <div className="space-y-4">
                <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
                  <label className="space-y-1.5">
                    <span className="block font-medium text-[var(--cms-muted-fg)]">整本买断价格 (USD)</span>
                    <div className="relative">
                      <span className="absolute left-3 top-1/2 -translate-y-1/2 text-xs text-[var(--cms-muted-fg)]">$</span>
                      <input
                        type="number"
                        step="0.01"
                        min="0.01"
                        required
                        className="input-field tabular-nums"
                        style={{ paddingLeft: '26px' }}
                        value={slotDraft.price_amount ?? getSlotAmount(slotDraft)}
                        onChange={(e) =>
                          applySlotDraftChange('price_amount', e.target.value === '' ? '' : parseFloat(e.target.value))
                        }
                      />
                    </div>
                  </label>

                  <label className="space-y-1.5">
                    <span className="block font-medium text-[var(--cms-muted-fg)]">商品名称 *</span>
                    <input
                      type="text"
                      required
                      placeholder="例如：Unlock Entire Novel"
                      className="input-field"
                      value={slotDraft.vip_name}
                      onChange={(e) => applySlotDraftChange('vip_name', e.target.value)}
                    />
                  </label>
                </div>

                <label className="block space-y-1.5">
                  <span className="block font-medium text-[var(--cms-muted-fg)]">权益描述 *</span>
                  <input
                    type="text"
                    required
                    placeholder="例如：一次性解锁当前小说全部付费章节"
                    className="input-field"
                    value={slotDraft.vip_desc}
                    onChange={(e) => applySlotDraftChange('vip_desc', e.target.value)}
                  />
                </label>
              </div>
            )}

            {slotDraftError && (
              <div
                role="alert"
                className="px-3.5 py-2.5 rounded-xl border text-xs"
                style={{
                  backgroundColor: 'color-mix(in srgb, var(--cms-error) 10%, transparent)',
                  borderColor: 'color-mix(in srgb, var(--cms-error) 28%, transparent)',
                  color: 'var(--cms-error)',
                }}
              >
                {slotDraftError}
              </div>
            )}
          </form>
        )}
      </CmsDialog>
    </div>
  );
}
