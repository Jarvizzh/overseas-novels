const statusLabels: Record<string, string> = {
  expected: '应回传',
  not_required: '无需回传',
  pending_review: '待业务复核',
  unknown: '资格未知',
  missing_outbox: '应回传但未入队',
  awaiting_normalization: '已入队，待归一化',
  normalization_failed: '归一化失败',
  missing_event: '已消费，缺少统一事件',
  missing_target: '缺少原 Pixel 任务',
  delivery_created: '已建立投递',
  legacy_record: '有历史队列记录',
  confirmed: '已确认接收',
  pending: '待处理',
  blocked: '阻塞',
  outcome_unknown: '结果未知',
  succeeded: '已确认接收',
  failed: '失败',
  skipped: '已跳过',
  delivered: '已投递',
  unattributed: '缺少归因',
  quarantined: '已隔离',
  consumed: '已归一化',
  no_trace_contract_unconfirmed: '失败已定稿，回告待契约确认',
};

export function status(code: string) {
  return statusLabels[code] ?? code;
}

export function timestamp(value?: string) {
  return value ? new Date(value).toLocaleString() : '—';
}

export function sourceEventLabel(source: string, eventType: string) {
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
