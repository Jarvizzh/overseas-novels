import { getReasonLabel, timestamp } from '../../utils/conversionStatus';
import { ConversionStatusDot } from './ConversionShared';
import { actionLabel, bandLabel, paymentKindLabel, policySourceLabels } from './paymentPolicy';
import type { PaymentDecision } from './types';

export function PaymentDecisionSummary({ decision }: { decision: PaymentDecision }) {
  const match = decision.match;
  const interval =
    match?.currency && match.min_minor != null
      ? bandLabel({
          currency: match.currency,
          mode: decision.mode ?? 'all',
          min_minor: match.min_minor,
          max_minor: match.max_minor ?? null,
        })
      : match?.is_fallback
        ? '本组兜底'
        : '—';
  const action = decision.mode ? actionLabel({ ...match, mode: decision.mode }) : '';
  const status =
    decision.state === 'decided'
      ? decision.outcome === 'skipped'
        ? 'policy_skipped'
        : 'policy_selected'
      : decision.state === 'historical'
        ? 'policy_historical'
        : 'policy_pending';
  return (
    <section className="cms-card p-4 space-y-3 text-xs" aria-label="付款回传判定">
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
        <h3 className="m-0 text-sm font-semibold">付款回传判定</h3>
        <ConversionStatusDot code={status} />
        {action && (
          <span>
            {action}
            {decision.cycle_sequence && ` · 第 ${decision.cycle_sequence} 笔`}
          </span>
        )}
      </div>
      <dl className="grid sm:grid-cols-2 lg:grid-cols-3 gap-x-3 gap-y-2 m-0">
        <div>
          <dt className="text-[var(--cms-muted-fg)]">实际作用域</dt>
          <dd className="m-0 tabular-nums">
            {decision.app_id && `${decision.app_id} · `}
            {decision.promotion_id ? `推广链接 #${decision.promotion_id}` : '接入默认'}
          </dd>
        </div>
        <div>
          <dt className="text-[var(--cms-muted-fg)]">配置来源与版本</dt>
          <dd className="m-0 tabular-nums">
            {decision.config_source === 'own' && decision.scope_kind === 'default'
              ? '接入默认规则'
              : decision.config_source
                ? policySourceLabels[decision.config_source]
                : '—'}
            {decision.version_id && ` · v${decision.version_number} / #${decision.version_id}`}
          </dd>
        </div>
        <div>
          <dt className="text-[var(--cms-muted-fg)]">命中区间</dt>
          <dd className="m-0 tabular-nums">
            {match && `${paymentKindLabel(match.kind)} · `}
            {interval}
          </dd>
        </div>
        {decision.reason && (
          <div>
            <dt className="text-[var(--cms-muted-fg)]">原因</dt>
            <dd className="m-0">{getReasonLabel(decision.reason)}</dd>
          </div>
        )}
        {decision.decided_at && (
          <div>
            <dt className="text-[var(--cms-muted-fg)]">判定时间</dt>
            <dd className="m-0 tabular-nums">{timestamp(decision.decided_at)}</dd>
          </div>
        )}
      </dl>
    </section>
  );
}
