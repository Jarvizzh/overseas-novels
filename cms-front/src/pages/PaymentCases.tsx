import { useEffect, useId, useRef, useState, type FormEvent } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { SkeletonRows, TableRegion } from '../components/ui/Primitives';
import { ApiError, apiRequest } from '../utils/api';

type View = 'risk' | 'disputes';
type CaseRow = {
  id: string; provider: string; environment: string; kind: string; external_id: string;
  provider_status: string; stage: string; reason: string; outcome: string; is_open: boolean;
  work_state: string; owner: string | null; due_at: string | null; work_due_at: string | null;
  escalated_at: string | null; amount_minor: string | null; sales_mode: 'direct' | 'merchant_of_record' | null;
  receipt_refs: string[]; order_ids: string[]; reminder: boolean;
};
type Receipt = {
  id: string; receipt: string; order_id: string; type: string; state: string;
  paid_at: string | null; fulfilled_at: string | null; paid_period_start: string | null; paid_period_end: string | null;
  usage_count: number; refunds: { id: string; amount_minor: string }[];
  loss_minor: string | null; blocked: boolean; target_blocked: boolean | null; evidence_key: string | null; adjustment_pending: boolean;
};
type Detail = CaseRow & {
  receipts: Receipt[];
  audit: { actor: string; action: string; reason: string; recorded_at: string }[];
  sources: { source: string; source_ref: string; observed_at: string; codes?: object }[];
};
type Decision = {
  id: string; order_id: string | null; checkout_environment: string; provider: string | null;
  stage: string; reason: string; outcome: string; rule_version: number; evaluated_at: string;
};
type Owner = { id: string; name: string };
type Action = 'assign' | 'approve' | 'reject' | 'apply_loss' | 'restore';
const providerResponseStates = new Set(['WAITING_FOR_SELLER_RESPONSE', 'needs_response', 'warning_needs_response', 'appeal_needs_response']);
const labels: Record<string, string> = {
  risk_review: '渠道复核', risk_signal: '渠道风险信号', dispute: '争议', reversal: '独立冲回',
  observed: '已记录', pending: '待复核', approved: '已批准，待核验', refund_due: '财务退款待办',
  dispute_due: '跟踪关联争议', closed: '已处置', allow: '放行', block: '拦截', verify: '待确认', review: '待复核',
  assign: '分派负责人', approve: '批准重新核验', reject: '拒绝并转财务', apply_loss: '确认权益冲回', restore: '确认权益恢复',
};

function formatDate(value?: string | null) {
  return value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '—';
}

function usd(value?: string | null) {
  if (value == null) return '—';
  const amount = BigInt(value);
  const absolute = amount < 0n ? -amount : amount;
  return `${amount < 0n ? '−' : ''}$${(absolute / 100n).toLocaleString('en-US')}.${String(absolute % 100n).padStart(2, '0')}`;
}

function requestError(error: unknown) {
  if (error instanceof ApiError && error.status === 403) return '当前账号没有支付案件权限。';
  if (error instanceof ApiError && error.status === 409) return '案件或资金凭证已变化。请刷新后重新核对。';
  return '请求失败，请重试。';
}

function useCaseData<T>(url: string, revision: number) {
  const [result, setResult] = useState<{ url: string; revision: number; data?: T; error?: string }>();
  useEffect(() => {
    if (!url) return;
    const controller = new AbortController();
    void apiRequest('GET', url, undefined, false, controller.signal)
      .then((data: T) => { if (!controller.signal.aborted) setResult({ url, revision, data }); })
      .catch((error: unknown) => { if (!controller.signal.aborted) setResult({ url, revision, error: requestError(error) }); });
    return () => controller.abort();
  }, [url, revision]);
  return result?.url === url && result.revision === revision ? result : undefined;
}

function CaseActionForm({ detail, action, evidenceKey, onDone }: {
  detail: Detail; action: Action; evidenceKey?: string; onDone: () => void;
}) {
  const id = useId();
  const [owner, setOwner] = useState('');
  const [reason, setReason] = useState('');
  const [pending, setPending] = useState(false);
  const [error, setError] = useState('');
  const [ownersRevision, setOwnersRevision] = useState(0);
  const request = useRef<{ body: string; id: string } | undefined>(undefined);
  const owners = useCaseData<Owner[]>(action === 'assign' ? '/payments/cases/owners' : '', ownersRevision);

  async function submit(event: FormEvent) {
    event.preventDefault();
    if (pending || !reason.trim()) return;
    const body = { action, reason: reason.trim(), owner, evidence_key: evidenceKey };
    const serialized = JSON.stringify(body);
    if (request.current?.body !== serialized) request.current = { body: serialized, id: crypto.randomUUID() };
    setError('');
    setPending(true);
    try {
      await apiRequest('POST', `/payments/cases/${detail.id}/actions`, { ...body, request_id: request.current.id });
      onDone();
    } catch (failure) {
      setError(requestError(failure));
    } finally {
      setPending(false);
    }
  }

  return (
    <form className="space-y-2 p-3 border border-[var(--cms-border)] rounded" onSubmit={(event) => void submit(event)}>
      <h3 className="font-medium">{labels[action]}</h3>
      {action === 'assign' && <div className="grid gap-1">
        <label htmlFor={`${id}-owner`}>负责人（必填）</label>
        <select id={`${id}-owner`} className="input-field" required value={owner} onChange={(event) => setOwner(event.target.value)}>
          <option value="">选择财务账号</option>
          {owners?.data?.map((entry) => <option key={entry.id} value={entry.id}>{entry.name}</option>)}
        </select>
        {owners?.error && <div><span role="alert">{owners.error}</span> <button type="button" onClick={() => setOwnersRevision((value) => value + 1)}>重试</button></div>}
      </div>}
      <div className="grid gap-1">
        <label htmlFor={`${id}-reason`}>处理依据（必填）</label>
        <textarea id={`${id}-reason`} className="input-field" required maxLength={500} value={reason} onChange={(event) => setReason(event.target.value)} />
      </div>
      <button className="btn-primary" type="submit" disabled={pending || (action === 'assign' && !owners?.data)}>
        {pending ? '提交中…' : '确认提交'}
      </button>
      {error && <p role="alert">{error}</p>}
    </form>
  );
}

function providerConsole(row: CaseRow) {
  if (row.provider === 'paypal' && row.kind === 'dispute') {
    return row.environment === 'live' ? 'https://www.paypal.com/disputes/' : 'https://www.sandbox.paypal.com/disputes/';
  }
  if (row.provider !== 'stripe' || row.kind !== 'dispute') return '';
  return `https://dashboard.stripe.com/${row.environment === 'sandbox' ? 'test/' : ''}disputes/${encodeURIComponent(row.external_id)}`;
}

function CaseDetail({ detail, onDone }: { detail: Detail; onDone: () => void }) {
  const [action, setAction] = useState<{ name: Action; evidence?: string }>();
  const consoleURL = providerConsole(detail);
  const managed = detail.provider === 'stripe' && detail.sales_mode === 'merchant_of_record';
  const merchant = detail.provider === 'paypal' || detail.sales_mode === 'direct';
  const responsibility = managed ? 'Stripe 负责渠道处置' : merchant ? '商户负责渠道处置' : '销售模式待核实';
  const responseHint = managed ? '跟踪 Stripe 处置进度' : merchant ? '等待商户响应' : '核实处理责任后响应';
  const confirm = () => { setAction(undefined); onDone(); };
  return (
    <section className="cms-card p-4 space-y-3" aria-labelledby="payment-case-title">
      <h2 id="payment-case-title" className="font-semibold">{labels[detail.kind]} · {detail.external_id}</h2>
      <p>{detail.provider.toUpperCase()} · {detail.environment} · {responsibility}</p>
      <p>{detail.stage || '—'} · {detail.provider_status || '—'} · {detail.outcome || '裁决待确认'}
        {detail.kind === 'dispute' && detail.is_open && providerResponseStates.has(detail.provider_status) && ` · ${responseHint}`}</p>
      <p>渠道原因 {detail.reason || '未提供'} · 金额 {usd(detail.amount_minor)}</p>
      <p>举证截止 {formatDate(detail.due_at)} · 处置期限 {formatDate(detail.work_due_at)} · {detail.owner || '未分派'}</p>
      {consoleURL && <a href={consoleURL} target="_blank" rel="noreferrer" className="btn-detail">打开渠道后台</a>}
      <div className="flex flex-wrap gap-2">
        <button className="btn-secondary" onClick={() => setAction({ name: 'assign' })}>分派负责人</button>
        {detail.kind === 'risk_review' && ['pending', 'refund_due', 'dispute_due'].includes(detail.work_state) &&
          <button className="btn-secondary" onClick={() => setAction({ name: 'approve' })}>批准后重新核验收款</button>}
        {detail.kind === 'risk_review' && detail.work_state === 'pending' &&
          <button className="btn-secondary" onClick={() => setAction({ name: 'reject' })}>拒绝并转财务</button>}
      </div>
      {detail.receipts.map((receipt) => (
        <article key={receipt.id} className="rounded border border-[var(--cms-border)] p-3 space-y-2">
          <h3 className="font-medium">原收款 {receipt.receipt}</h3>
          <p>订单 {receipt.state} · 收款 {formatDate(receipt.paid_at)} · 履约 {formatDate(receipt.fulfilled_at)}</p>
          {receipt.paid_period_end && <p>原账期 {formatDate(receipt.paid_period_start)} 至 {formatDate(receipt.paid_period_end)}</p>}
          <p>相关章节解锁 {receipt.usage_count} 次 · 已确认本金损失 {usd(receipt.loss_minor)}{receipt.blocked && ' · 权益已冲回'}</p>
          <div className="flex flex-wrap gap-2">
            <Link className="btn-detail" to={`/payments/orders?order_id=${receipt.order_id}`}>订单 #{receipt.order_id}</Link>
            <Link className="btn-detail" to={`/payments/reconciliation?order_id=${receipt.order_id}&environment=${detail.environment}`}>流水与结算</Link>
            <Link className="btn-detail" to={`/payments/refunds?order_id=${receipt.order_id}&receipt_id=${receipt.id}&environment=${detail.environment}`}>退款</Link>
            <Link className="btn-detail" to={`/payments/disputes?receipt=${encodeURIComponent(receipt.receipt)}&environment=${detail.environment}`}>关联争议和冲回</Link>
          </div>
          {receipt.refunds.length > 0 && <ul>{receipt.refunds.map((refund) => <li key={refund.id}>{refund.id} · {usd(refund.amount_minor)}</li>)}</ul>}
          {receipt.adjustment_pending ? <p role="status">权益处置等待执行</p>
            : ['dispute', 'reversal'].includes(detail.kind) && receipt.evidence_key && receipt.target_blocked !== receipt.blocked && receipt.fulfilled_at &&
              <button className="btn-secondary" onClick={() => setAction({ name: receipt.target_blocked ? 'apply_loss' : 'restore', evidence: receipt.evidence_key! })}>
                {receipt.target_blocked ? labels.apply_loss : labels.restore}
              </button>}
        </article>
      ))}
      {detail.receipts.length === 0 && detail.order_ids.map((order) => (
        <Link key={order} className="btn-detail" to={`/payments/orders?order_id=${order}`}>订单 #{order}</Link>
      ))}
      {detail.receipts.length === 0 && <p>暂无同账户已验证原收款。</p>}
      {action && <CaseActionForm key={`${action.name}/${action.evidence ?? ''}`} detail={detail} action={action.name} evidenceKey={action.evidence} onDone={confirm} />}
      {detail.sources.length > 0 && <details><summary>渠道信号与来源</summary>
        {detail.sources.map((source, index) => <article key={`${source.source_ref}/${index}`}>
          <p>{source.source} · {source.source_ref} · {formatDate(source.observed_at)}</p>
          <pre className="overflow-x-auto">{JSON.stringify(source.codes, null, 2)}</pre>
        </article>)}
      </details>}
      {detail.audit.length > 0 && <details><summary>操作审计</summary><ul>
        {detail.audit.map((entry, index) => <li key={index}>{formatDate(entry.recorded_at)} · {entry.actor} · {labels[entry.action] ?? entry.action} · {entry.reason}</li>)}
      </ul></details>}
    </section>
  );
}

export default function PaymentCases({ view }: { view: View }) {
  const [params, setParams] = useSearchParams();
  const id = useId();
  const [revision, setRevision] = useState(0);
  const [notice, setNotice] = useState('');
  const selected = params.get('case_id');
  const decisions = view === 'risk' && params.get('tab') === 'decisions';
  const formKey = `${view}/${params.get('order_id') ?? ''}/${params.get('receipt') ?? ''}/${params.get('external_id') ?? ''}`;
  const query = new URLSearchParams({ view });
  for (const key of ['environment', 'provider', 'order_id', 'receipt', 'external_id', 'before', 'attention']) {
    const value = params.get(key);
    if (value) query.set(key, value);
  }
  const casePage = useCaseData<{ items: CaseRow[] }>(decisions ? '' : `/payments/cases?${query}`, revision);
  const decisionPage = useCaseData<{ items: Decision[] }>(decisions ? `/payments/cases/decisions?${query}` : '', revision);
  const detail = useCaseData<Detail>(selected && !decisions ? `/payments/cases/${selected}` : '', revision);
  const data = decisions ? decisionPage : casePage;
  const title = view === 'risk' ? '支付风控' : '争议与独立冲回';
  const headers = decisions ? ['阶段', '决定', '原因', '环境 / 版本', '订单', '时间']
    : ['案件', '渠道 / 阶段', '处理状态', '负责人', '截止时间', '金额 USD'];

  function update(key: string, value: string, reset = true) {
    const next = new URLSearchParams(params);
    if (reset) { next.delete('before'); next.delete('case_id'); }
    if (value) next.set(key, value); else next.delete(key);
    setNotice('');
    setParams(next);
  }

  return (
    <div className="space-y-4">
      <h1 className="text-xl font-semibold">{title}</h1>
      {view === 'risk' && <nav aria-label="风控视图" className="flex gap-2">
        <button type="button" className={decisions ? 'btn-secondary' : 'btn-primary'} onClick={() => update('tab', '')}>渠道信号与复核</button>
        <button type="button" className={decisions ? 'btn-primary' : 'btn-secondary'} onClick={() => update('tab', 'decisions')}>公共结账决策</button>
      </nav>}
      <form key={formKey} className="flex flex-wrap items-end gap-2" onSubmit={(event) => {
        event.preventDefault();
        const fields = new FormData(event.currentTarget);
        const next = new URLSearchParams(params);
        next.delete('before'); next.delete('case_id');
        for (const field of ['order_id', 'receipt', 'external_id']) {
          const value = String(fields.get(field) ?? '').trim();
          if (value) next.set(field, value); else next.delete(field);
        }
        setParams(next);
      }}>
        {!decisions && <div className="grid gap-1"><label htmlFor={`${id}-environment`}>环境</label>
          <select id={`${id}-environment`} className="input-field" value={params.get('environment') ?? 'live'} onChange={(event) => update('environment', event.target.value)}>
            <option value="live">正式</option><option value="sandbox">沙箱</option>
          </select>
        </div>}
        <div className="grid gap-1"><label htmlFor={`${id}-order`}>订单号</label>
          <input id={`${id}-order`} className="input-field" name="order_id" inputMode="numeric" pattern="[0-9]+" defaultValue={params.get('order_id') ?? ''} />
        </div>
        {!decisions && <><div className="grid gap-1"><label htmlFor={`${id}-receipt`}>原收款编号</label>
          <input id={`${id}-receipt`} className="input-field" name="receipt" maxLength={128} defaultValue={params.get('receipt') ?? ''} />
        </div><div className="grid gap-1"><label htmlFor={`${id}-external`}>渠道案件编号</label>
          <input id={`${id}-external`} className="input-field" name="external_id" maxLength={128} defaultValue={params.get('external_id') ?? ''} />
        </div></>}
        <button type="submit" className="btn-primary">查询</button>
        {!decisions && <label className="flex items-center gap-1"><input type="checkbox" checked={params.get('attention') === 'true'}
          onChange={(event) => update('attention', event.target.checked ? 'true' : '')} />待处理与期限提醒</label>}
      </form>
      {notice && <p role="status">{notice}</p>}
      {data?.error ? <div><p role="alert">{data.error}</p><button className="btn-secondary" onClick={() => setRevision((value) => value + 1)}>重试</button></div>
        : <TableRegion ariaLabel={title}>
          <table className="cms-table" aria-busy={!data}>
            <thead><tr>{headers.map((header) => <th scope="col" key={header}>{header}</th>)}</tr></thead>
            <tbody>
              {!data && <SkeletonRows rows={4} cols={headers.length} />}
              {decisions ? decisionPage?.data?.items.map((row) => <tr key={row.id}>
                <td>{row.stage}</td><td>{labels[row.outcome]}</td><td>{row.reason}</td>
                <td>{row.checkout_environment} / {row.rule_version}</td>
                <td>{row.order_id ? <Link to={`/payments/orders?order_id=${row.order_id}`}>#{row.order_id}</Link> : '—'}</td>
                <td>{formatDate(row.evaluated_at)}</td>
              </tr>) : casePage?.data?.items.map((row) => <tr key={row.id}>
                <td><button className="btn-detail" onClick={() => update('case_id', row.id, false)}>{row.external_id}</button><div>{labels[row.kind]}</div></td>
                <td>{row.provider} · {row.stage || row.provider_status || '—'}</td>
                <td>{labels[row.work_state]}{row.escalated_at && ' · 已升级'}{row.reminder && row.is_open && ' · 举证提醒'}</td>
                <td>{row.owner || '未分派'}</td><td>{formatDate(row.due_at ?? row.work_due_at)}</td><td className="tabular-nums">{usd(row.amount_minor)}</td>
              </tr>)}
            </tbody>
          </table>
        </TableRegion>}
      {data?.data?.items.length === 0 && <p role="status">没有符合条件的记录。</p>}
      <div className="flex gap-2">
        {params.has('before') && <button className="btn-secondary" onClick={() => update('before', '')}>返回第一页</button>}
        {data?.data?.items.length === 20 && <button className="btn-secondary"
          onClick={() => update('before', data.data!.items.at(-1)!.id)}>下一页</button>}
      </div>
      {selected && !decisions && (detail?.error ? <p role="alert">{detail.error}</p> : detail?.data ?
        <CaseDetail key={detail.data.id} detail={detail.data} onDone={() => { setRevision((value) => value + 1); setNotice('已提交，执行结果以案件状态为准。'); }} />
        : <p role="status">正在加载案件详情…</p>)}
    </div>
  );
}
