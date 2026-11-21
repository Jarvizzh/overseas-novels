import { ApiError } from './api';
import type {
  ConversionDelivery,
  ConversionDestination,
} from '../components/conversion/types';

const utf8Encoder = new TextEncoder();

export function utf8ByteLength(value: string): number {
  return utf8Encoder.encode(value).length;
}

export type StatusTone = 'success' | 'warning' | 'error' | 'primary' | 'muted';

export interface StatusMeta {
  label: string;
  tone: StatusTone;
}

const STATUS_META_MAP: Record<string, StatusMeta> = {
  policy_selected: { label: '已选中回传', tone: 'success' },
  policy_skipped: { label: '按规则跳过', tone: 'muted' },
  policy_historical: { label: '历史订单', tone: 'muted' },
  policy_pending: { label: '待核查规则数据', tone: 'warning' },
  // 归因状态
  matched: { label: '已归因', tone: 'success' },
  organic: { label: '自然流量', tone: 'muted' },
  missing_pixel: { label: '缺 Pixel', tone: 'warning' },
  unknown: { label: '未知', tone: 'muted' },

  // 投递与尝试结果
  succeeded: { label: '已确认接收', tone: 'success' },
  confirmed: { label: '已确认接收', tone: 'success' },
  accepted: { label: 'Meta 已接收', tone: 'success' },
  pending: { label: '待处理', tone: 'primary' },
  leased: { label: '租约处理中', tone: 'primary' },
  backoff: { label: '退避重试中', tone: 'warning' },
  blocked: { label: '阻塞', tone: 'warning' },
  outcome_unknown: { label: '结果未知', tone: 'warning' },
  failed: { label: '失败', tone: 'error' },
  skipped: { label: '已跳过', tone: 'muted' },
  not_sent: { label: '未发出', tone: 'muted' },
  retryable_rejection: { label: '可重试拒绝', tone: 'warning' },
  permanent_rejection: { label: '永久拒绝', tone: 'error' },
  rejected: { label: '合作方拒绝', tone: 'error' },

  // 对账、资格与 Inbox
  expected: { label: '应回传', tone: 'primary' },
  not_required: { label: '无需回传', tone: 'muted' },
  pending_review: { label: '待业务复核', tone: 'warning' },
  historical_unknown: { label: '历史未知', tone: 'muted' },
  missing_outbox: { label: '应回传但未入队', tone: 'error' },
  awaiting_normalization: { label: '已入队，待归一化', tone: 'primary' },
  normalization_failed: { label: '归一化失败', tone: 'error' },
  missing_event: { label: '已消费，缺少统一事件', tone: 'error' },
  missing_target: { label: '缺少原 Pixel 任务', tone: 'warning' },
  delivery_created: { label: '已建立投递', tone: 'primary' },
  legacy_record: { label: '历史队列记录', tone: 'muted' },
  delivered: { label: '已投递', tone: 'success' },
  unattributed: { label: '缺少归因', tone: 'warning' },
  quarantined: { label: '已隔离', tone: 'error' },
  consumed: { label: '已归一化', tone: 'success' },

  // 兼容旧文案内嵌态
  no_trace_contract_unconfirmed: { label: '失败已定稿，回告待契约确认', tone: 'warning' },

  // 配置与启用状态
  enabled: { label: '已启用', tone: 'success' },
  disabled: { label: '已停用', tone: 'warning' },
  retired: { label: '已退役', tone: 'muted' },
};

const REASON_LABELS: Record<string, string> = {
  rule_fact_missing: '待核查规则数据',
  rule_not_first_payment: '该链接下的复购',
  rule_send_all: '命中全传规则',
  rule_no_match_send: '按默认全传',
  rule_ratio_selected: '比例抽样选中',
  rule_ratio_skipped: '比例抽样跳过',
  rule_cycle_selected: '循环选中',
  rule_cycle_skipped: '循环跳过',
  historical_payment: '沿用历史付款处理',
  duplicate_payment_event: '同单重复回调，已归并',
  payment_policy_fact_conflict: '同单付款事实冲突',
  missing_promotion_contract_unconfirmed: '回告待确认推广 ID 的空值编码',
  deadline_exhausted: '已过投递有效期截止时间',
  retry_budget_exhausted: '自动重试预算已耗尽（需人工排查后重置）',
  meta_no_confirmation: '一直未获得 Meta 明确单事件接收确认',
  meta_retryable_rejection: 'Meta 返回可重试拒绝或限流，等待退避',
  original_target_unavailable: '原目标 Pixel 不可用或未获接入授权',
  legacy_original_target_unavailable: '历史记录缺少可用原目标 Pixel',
  destination_or_credential_unavailable: '目标 Pixel 不存在或凭据缺失',
  credential_decryption_failed: '目标凭据解密失败，请检查密钥与配置',
  meta_credentials_or_permissions: 'Meta Token 无效或缺少对应 Pixel 权限',
  destination_disabled: '目标 Pixel 已停用或退役',
  promotion_pixel_mismatch: '回调 Pixel 与推广链显式绑定的 Pixel 不一致',
  partner_route_not_authorized: '当前应用或推广链未获得该 Pixel 路由授权',
  legacy_environment_unverified: '历史投递环境未核实（需使用 CLI 复核证据）',
  legacy_snapshot_unverified: '历史归因快照未核实',
  legacy_state_unverified: '历史状态未核实',
  legacy_verified: '历史记录已核实资格',
  legacy_confirmed: '历史记录已确认投递',
  event_time_missing: '缺少可验证的原始业务发生时间',
  refunded_before_fulfillment: '订单在履约前已退款',
  organic: '自然流量（无广告归因）',
  no_trace_contract_unconfirmed: '无 trace 失败已定稿，回告格式待契约确认',
  partner_retry_budget_or_deadline: '分销回告达到重试上限或超过回告时限',
  partner_rejected: '分销方拒绝接收回告',
  lease_expired_before_send: '发送前租约过期，已安全重排',
  lease_expired_after_send_started: '进入发送后租约过期，需核查接收结果',
  worker_stopping: 'Worker 停机中断，等待重排',
  payload_unavailable: '事件载荷不可用',
  send_precondition_failed: '发送前置校验未通过',
  invalid_reader_attribution_state: 'Reader 归因快照状态异常',
  order_id_invalid: '外部订单号格式无效',
  event_id_invalid: '外部事件 ID 格式无效',
  original_callback_pixel: '回调自带原始 Pixel',
  integration_default_route: '接入默认附加路由',
  manual_append: '运营人工追加目标',
};

const RECOVERABLE_BLOCKED_REASONS = new Set([
  'retry_budget_exhausted',
  'destination_or_credential_unavailable',
  'credential_decryption_failed',
  'meta_credentials_or_permissions',
  'destination_disabled',
  'promotion_pixel_mismatch',
  'partner_route_not_authorized',
  'rule_fact_missing',
]);

const ALERT_LABELS: Record<string, string> = {
  rule_fact_missing: '付款规则事实待核查',
  meta_queue_delayed: 'Meta 待发队列超时',
  meta_credentials: 'Meta 凭据或路由绑定异常',
  meta_outcome_unknown: 'Meta 接收结果未知待确认',
  worker_heartbeat_missing: 'Worker 心跳缺失',
  worker_stalled: 'Worker 队列推进停滞',
  purchase_missing_outbox: '已履约支付缺少入队',
  missing_original_target: '已归一化但缺少原 Pixel 任务',
  reader_normalization_delayed: 'Reader Outbox 归一化积压或失败',
  inbox_delayed: '分销回调 Inbox 积压或隔离',
  partner_report_delayed: '分销回告阻塞或积压',
};

const WORKER_LABELS: Record<string, { label: string; description: string }> = {
  normalize: { label: 'Reader 归一化', description: '消费 reader_conversion_outbox' },
  normalize_inbox: { label: '分销回调归一化', description: '消费 conversion.inbox' },
  meta: { label: 'Meta CAPI 投递', description: '领取与发送 Meta Pixel 任务' },
  partner_report: { label: '分销结果回告', description: '向分销平台回告原目标定稿结果' },
};

const REQUEST_RESERVE_MS = 20_000;

export function getStatusMeta(code: string): StatusMeta {
  return STATUS_META_MAP[code] ?? { label: code || '—', tone: 'muted' };
}

export function status(code: string): string {
  return getStatusMeta(code).label;
}

export function getReasonLabel(code?: string): string {
  if (!code) return '';
  return REASON_LABELS[code] ?? code;
}

export function sourceLabel(source: string): string {
  switch (source) {
    case 'starnovel':
      return 'StarNovel 自建';
    case 'sinan':
      return '番茄司南';
    default:
      return source || '—';
  }
}

export function sourceEventLabel(source: string, eventType: string): string {
  if (source === 'sinan') {
    switch (eventType) {
      case 'attribution-succeeded':
        return '染色成功';
      case 'order-created':
        return '订单创建';
      case 'recharge-paid':
        return '充值支付完成';
    }
  }
  return eventType;
}

export function partnerReportStatusLabel(reportStatus: number): string {
  switch (reportStatus) {
    case 1:
      return '1 · 已接收';
    case -1:
      return '-1 · 明确失败';
    case 0:
      return '0 · 不上报';
    default:
      return String(reportStatus);
  }
}

export function alertKindLabel(kind: string): string {
  return ALERT_LABELS[kind] ?? kind;
}

export function workerTypeMeta(workerType: string): { label: string; description: string } {
  return WORKER_LABELS[workerType] ?? { label: workerType, description: '' };
}

export function timestamp(value?: string): string {
  if (!value) return '—';
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString();
}

export function formatAmount(amount?: string, currency?: string): string {
  if (!amount) return '';
  return currency ? `${amount} ${currency}` : amount;
}

export function formatDurationSeconds(value?: number): string {
  if (value === undefined || value === null || Number.isNaN(value)) return '—';
  const rounded = Math.round(value);
  if (rounded < 60) return `${rounded} 秒`;
  const mins = Math.floor(rounded / 60);
  const secs = rounded % 60;
  if (mins < 60) return secs > 0 ? `${mins} 分 ${secs} 秒` : `${mins} 分钟`;
  const hours = Math.floor(mins / 60);
  const remMins = mins % 60;
  return remMins > 0 ? `${hours} 小时 ${remMins} 分` : `${hours} 小时`;
}

export function isServiceUnavailableError(error: unknown): boolean {
  return error instanceof ApiError && (error.status === 503 || error.code === 'reconciliation_unavailable');
}

export function getErrorMessage(error: unknown, fallback = '请求失败'): string {
  if (error instanceof Error && error.message) {
    return error.message;
  }
  return fallback;
}

export function getDeliveryRetryEligibility(
  delivery: ConversionDelivery,
  nowMs = Date.now(),
): { allowed: boolean; isTerminal: boolean; hint?: string } {
  if (delivery.state === 'succeeded' || delivery.state === 'failed' || delivery.state === 'skipped') {
    return { allowed: false, isTerminal: true, hint: '已进入终态，不可重排' };
  }

  if (delivery.deadline_at) {
    const deadlineMs = Date.parse(delivery.deadline_at);
    if (!Number.isNaN(deadlineMs) && nowMs + REQUEST_RESERVE_MS >= deadlineMs) {
      return { allowed: false, isTerminal: false, hint: '已过或临近投递截止时间，不可重排' };
    }
  }

  if (delivery.state === 'pending') {
    if (delivery.reason === 'meta_retryable_rejection') {
      return { allowed: false, isTerminal: false, hint: 'Meta 限流/可重试拒绝正由系统退避调度' };
    }
    const nextMs = delivery.next_attempt_at ? Date.parse(delivery.next_attempt_at) : NaN;
    if (Number.isNaN(nextMs) || nowMs >= nextMs) {
      return { allowed: false, isTerminal: false, hint: '当前已在可执行队列中，等待 Worker 领取' };
    }
    return { allowed: true, isTerminal: false };
  }

  if (delivery.state === 'blocked') {
    if (delivery.reason && RECOVERABLE_BLOCKED_REASONS.has(delivery.reason)) {
      return { allowed: true, isTerminal: false };
    }
    if (delivery.reason?.startsWith('legacy_')) {
      return {
        allowed: false,
        isTerminal: false,
        hint: '历史任务阻塞项需先通过 conversion-ops review-legacy-environment 核实环境证据',
      };
    }
    return { allowed: false, isTerminal: false, hint: '当前阻塞原因不支持人工重排' };
  }

  if (delivery.state === 'outcome_unknown') {
    return { allowed: true, isTerminal: false, hint: '结果未知任务重排前须确认重复计数风险' };
  }

  return { allowed: false, isTerminal: false, hint: '当前状态暂不支持人工重排' };
}

const AUDIT_ACTION_LABELS: Record<string, string> = {
  configure_destination: 'Pixel 目标配置',
  configure_integration: '接入与路由绑定',
  configure_sinan_mock: '司南联调配置',
  publish_payment_policy: '发布付款回传规则',
  inherit_payment_policy: '恢复继承付款回传规则',
  append_delivery_target: '追加投递目标',
  retry_delivery: '重排投递任务',
  retry_partner_report: '重排分销回告',
};

export function auditActionLabel(action: string): string {
  return AUDIT_ACTION_LABELS[action] ?? (action || '—');
}

function asRecord(value: unknown): Record<string, unknown> | null {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    return null;
  }
  return value as Record<string, unknown>;
}

function formatScalarDiff(value: unknown): string {
  if (typeof value === 'boolean') return value ? '是' : '否';
  if (typeof value === 'string') return value || '空';
  if (typeof value === 'number') return String(value);
  if (Array.isArray(value)) return value.length ? value.join(', ') : '空';
  return value == null ? '未设置' : JSON.stringify(value);
}

function formatBindingMode(value: unknown): string {
  if (value === true) return '自动附加';
  if (value === false) return '仅授权';
  if (value === null || value === undefined) return '未授权';
  return String(value);
}

export function formatAuditHighlights(
  beforeState: unknown,
  afterState: unknown,
  destinationById?: Map<string, ConversionDestination>,
): string[] {
  const before = asRecord(beforeState);
  const after = asRecord(afterState);
  if (!after) return [];

  const hasBefore = before !== null && Object.keys(before).length > 0;
  const notes: string[] = [];

  if ('own_version' in after && typeof after.app_id === 'string') {
    const describe = (snapshot: Record<string, unknown> | null) => {
      const own = asRecord(snapshot?.own_version);
      const inherited = asRecord(snapshot?.inherited_version);
      const version = own ?? inherited;
      return version ? `${own ? '专属' : '继承默认'} v${version.number} · #${version.id}` : '默认全传';
    };
    notes.push(`应用 ${after.app_id} · ${after.promotion_id ? `推广链接 #${after.promotion_id}` : '接入默认'}`);
    notes.push(`${describe(before)} → ${describe(after)}`);
  }

  if ('display_name' in after) {
    const prevName = before?.display_name;
    const nextName = after.display_name;
    if (!hasBefore || prevName === undefined) {
      notes.push(`显示名称：${formatScalarDiff(nextName)}`);
    } else if (prevName !== nextName) {
      notes.push(`名称：${formatScalarDiff(prevName)} → ${formatScalarDiff(nextName)}`);
    }
  }

  if (typeof after.enabled === 'boolean') {
    if (!hasBefore || typeof before?.enabled !== 'boolean') {
      notes.push(`初始状态：${after.enabled ? '已启用' : '已停用'}`);
    } else if (before.enabled !== after.enabled) {
      notes.push(
        `启用状态：${before.enabled ? '已启用' : '已停用'} → ${after.enabled ? '已启用' : '已停用'}`,
      );
    }
  }

  const wasRetired = Boolean(before?.retired_at || before?.retired);
  const isRetired = Boolean(after.retired_at || after.retired);
  if (isRetired && !wasRetired) {
    notes.push('目标状态变更为：已退役（停止后续投递，保留历史追溯）');
  }

  if (after.credential_rotated === true || after.credential_replaced === true) {
    notes.push('已轮换/更新 Meta Access Token 凭据');
  } else if ((!hasBefore || before?.credential_configured === false) && after.credential_configured === true) {
    notes.push('已配置 Meta Access Token 凭据');
  }

  if (after.inbound_replaced === true) {
    notes.push('已更新入站签名凭据');
  }
  if (after.report_replaced === true) {
    notes.push('已更新回告签名种子');
  }

  if ('contract_version' in after) {
    if (!hasBefore || before?.contract_version === undefined) {
      notes.push(`契约版本：${formatScalarDiff(after.contract_version)}`);
    } else if (before.contract_version !== after.contract_version) {
      notes.push(
        `契约版本：${formatScalarDiff(before.contract_version)} → ${formatScalarDiff(after.contract_version)}`,
      );
    }
  }

  if (Array.isArray(after.allowed_apps)) {
    const nextApps = after.allowed_apps.join(', ') || '全部/不限制';
    const prevApps =
      hasBefore && Array.isArray(before?.allowed_apps)
        ? before.allowed_apps.join(', ') || '全部/不限制'
        : null;
    if (prevApps === null) {
      notes.push(`允许应用：${nextApps}`);
    } else if (prevApps !== nextApps) {
      notes.push(`允许应用：${prevApps} → ${nextApps}`);
    }
  }

  if (Array.isArray(after.allowed_events)) {
    const nextEvents = after.allowed_events.join(', ') || '无';
    const prevEvents =
      hasBefore && Array.isArray(before?.allowed_events)
        ? before.allowed_events.join(', ') || '无'
        : null;
    if (prevEvents === null) {
      notes.push(`允许事件：${nextEvents}`);
    } else if (prevEvents !== nextEvents) {
      notes.push(`允许事件：${prevEvents} → ${nextEvents}`);
    }
  }

  if (Array.isArray(after.bindings)) {
    const formatArrayBindings = (list: unknown[]) =>
      list
        .map((item) => {
          const rec = asRecord(item);
          if (!rec) return '';
          const idLabel = rec.pixel_id ?? rec.destination_id ?? '?';
          return `${String(idLabel)}${rec.additional ? '(自动附加)' : ''}`;
        })
        .filter(Boolean)
        .join('、') || '无授权 Pixel';

    const nextBindings = formatArrayBindings(after.bindings);
    const prevBindings =
      hasBefore && Array.isArray(before?.bindings) ? formatArrayBindings(before.bindings) : null;
    if (prevBindings === null) {
      notes.push(`Pixel 路由绑定：${nextBindings}`);
    } else if (prevBindings !== nextBindings) {
      notes.push(`Pixel 路由绑定：${prevBindings} → ${nextBindings}`);
    }
  } else {
    const afterBindingMap = asRecord(after.bindings);
    if (afterBindingMap) {
      const beforeBindingMap = hasBefore ? asRecord(before?.bindings) : null;
      for (const [destId, nextVal] of Object.entries(afterBindingMap)) {
        const dest = destinationById?.get(destId);
        const destTitle = dest
          ? dest.name
            ? `Pixel ${dest.pixel_id} (${dest.name})`
            : `Pixel ${dest.pixel_id}`
          : `目标 #${destId}`;
        if (beforeBindingMap && destId in beforeBindingMap) {
          const prevLabel = formatBindingMode(beforeBindingMap[destId]);
          const nextLabel = nextVal === null ? '解除绑定' : formatBindingMode(nextVal);
          notes.push(`${destTitle}：${prevLabel} → ${nextLabel}`);
        } else {
          notes.push(`${destTitle}：${nextVal === null ? '解除绑定' : formatBindingMode(nextVal)}`);
        }
      }
    }
  }

  return notes;
}
