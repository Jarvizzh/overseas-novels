import { useEffect, useRef, useState } from 'react';
import CustomSelect from '../components/CustomSelect';
import { PaymentLoadError } from '../components/payments/PaymentWorkspace';
import { CmsDialog } from '../components/ui/CmsDialog';
import { Pagination } from '../components/ui/Pagination';
import { SkeletonRows, StatusDot, TableRegion } from '../components/ui/Primitives';
import { usePermission } from '../context/PermissionContext';
import { ApiError, apiRequest } from '../utils/api';
import {
  readSettlementDraft,
  settlementDraftDigest,
  storeSettlementDraft,
  type SettlementDraft,
  type ReportIssueOrigin,
} from '../utils/settlementRecovery';
import {
  PAYMENT_PAGE_SIZE,
  environmentLabels,
  paymentError,
  paymentGet,
  paymentParams,
  paymentScopeLabel,
  paymentTime,
  providerLabels,
  usePaymentQuery,
  type PaymentScope,
  type SettlementAccount,
} from '../utils/paymentWorkspace';

const base = '/payments/reconciliation';
const kinds = {
  paypal_report: '导入 PayPal 报表分卷',
  stripe_payout: '拉取 Stripe 划拨',
  stripe_report: '导入 Stripe 报表',
  bank_statement: '导入银行流水',
  bank_match: '匹配银行流水',
} as const;
type Kind = keyof typeof kinds;
export type SettlementOrigin = PaymentScope &
  ({ kind: 'bank_match'; reference: string } | { kind: 'paypal_report' | 'stripe_report'; report: ReportIssueOrigin });
type Command = PaymentScope & {
  id: string;
  kind: Kind;
  reference?: string;
  report_issue?: Pick<ReportIssueOrigin, 'import_id' | 'source_file' | 'row_number'>;
  status: keyof typeof states;
  error?: string;
  result?: Record<string, unknown>;
  actor: string;
  reason: string;
  created_at: string;
  updated_at: string;
};
const states = {
  pending: { label: '待处理', tone: 'muted' },
  running: { label: '处理中', tone: 'warning' },
  succeeded: { label: '已完成', tone: 'success' },
  failed: { label: '失败', tone: 'error' },
} as const;
const isPending = (item: Command) => item.status === 'pending' || item.status === 'running';

function SettlementForm({
  kind,
  scope,
  accounts,
  origin,
  draft,
  onPrepared,
  onReject,
  onClose,
  onAccepted,
}: {
  kind: Kind;
  scope: PaymentScope;
  accounts: SettlementAccount[];
  origin?: SettlementOrigin;
  draft?: SettlementDraft;
  onPrepared: (draft: SettlementDraft) => boolean;
  onReject: () => void;
  onClose: () => void;
  onAccepted: (id: string) => void;
}) {
  const [accountID, setAccountID] = useState(origin?.account_id || scope.account_id);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [pendingConfirmation, setPendingConfirmation] = useState(false);
  const submission = useRef<FormData | undefined>(undefined);
  const requestInFlight = useRef(false);
  const bank = kind === 'bank_match' || kind === 'bank_statement';
  const bankStatement = kind === 'bank_statement';
  const uploads = kind === 'paypal_report' || bankStatement;
  const account = accounts.find((item) => String(item.id) === accountID);
  const validAccount = bankStatement || Boolean(origin || account);
  const match = origin?.kind === 'bank_match' ? origin : undefined;
  const report = origin && 'report' in origin ? origin.report : undefined;

  async function submit(form: HTMLFormElement) {
    if (requestInFlight.current || !validAccount) return;
    requestInFlight.current = true;
    setBusy(true);
    setError('');
    let persisted = true;
    try {
      let body = submission.current;
      if (!body) {
        body = new FormData(form);
        body.set('kind', kind);
        body.set('environment', scope.environment);
        if (!bankStatement) body.set('account_id', accountID);
        if (match) body.set('reference', match.reference);
        if (report) {
          body.set('report_import_id', report.import_id);
          body.set('report_source_file', report.source_file);
          body.set('report_row_number', String(report.row_number));
        }
        const key = draft?.key ?? crypto.randomUUID();
        body.set('request_key', key);
        const digest = await settlementDraftDigest(body);
        if (draft && draft.digest !== digest) {
          setError('原请求的字段或文件与本次输入不一致。请按原值填写并选择原文件。');
          return;
        }
        if (!draft) {
          persisted = onPrepared({
            key,
            digest,
            kind,
            scope: {
              environment: scope.environment,
              provider: bankStatement ? '' : (account?.provider ?? scope.provider),
              account_id: bankStatement ? '' : accountID,
            },
            payout: match?.reference,
            report,
          });
        }
        submission.current = body;
      }
      const saved = await apiRequest('POST', base + '/commands', body, true);
      window.showToast?.('财务命令已受理', 'info');
      onAccepted(saved.id);
    } catch (failure) {
      if (!submission.current) {
        setError(failure instanceof Error ? failure.message : paymentError(failure));
        return;
      }
      const unknown = !(failure instanceof ApiError && [400, 403, 409].includes(failure.status));
      if (!unknown && !pendingConfirmation && !draft) {
        submission.current = undefined;
        onReject();
      }
      setPendingConfirmation(unknown || pendingConfirmation);
      setError(
        unknown
          ? '提交结果待确认。请使用同一请求重试，或关闭后查证。' + (persisted ? '' : '当前浏览器无法跨刷新保存此请求。')
          : paymentError(failure),
      );
    } finally {
      requestInFlight.current = false;
      setBusy(false);
    }
  }

  return (
    <CmsDialog
      open
      onClose={onClose}
      title={kinds[kind]}
      subtitle={
        bankStatement
          ? environmentLabels[scope.environment]
          : paymentScopeLabel({
              ...scope,
              provider: account?.provider ?? scope.provider,
              account_id: accountID,
            })
      }
      closedBy="closerequest"
      closeDisabled={busy}
    >
      <form
        className="space-y-4 text-sm"
        onSubmit={(event) => {
          event.preventDefault();
          void submit(event.currentTarget);
        }}
      >
        {draft && (
          <p role="status" className="text-xs text-[var(--cms-muted-fg)]">
            已锁定原请求键。请填回相同内容{uploads ? '，并重新选择原报表文件' : ''}。
          </p>
        )}
        <fieldset className="space-y-3" disabled={busy || pendingConfirmation}>
          {!bankStatement && !origin && (
            <label className="grid gap-1">
              商户账户
              <CustomSelect
                aria-label="结算商户账户"
                disabled={Boolean(draft)}
                width="100%"
                value={accountID}
                options={[
                  { value: '', label: '选择账户' },
                  ...accounts.map((item) => ({
                    value: String(item.id),
                    label: `${providerLabels[item.provider]} · ${item.label}`,
                  })),
                ]}
                onChange={setAccountID}
              />
            </label>
          )}
          {report && (
            <div className="space-y-1">
              <p>
                原报告 <span className="font-mono break-all">{report.external_ref}</span>
              </p>
              <p>
                差异位置 {report.source_file} · 第 {report.row_number} 行
              </p>
            </div>
          )}
          {match ? (
            <p>
              目标划拨 <span className="font-mono">{match.reference}</span>
            </p>
          ) : (
            kind !== 'paypal_report' && (
              <label className="grid gap-1">
                {bankStatement
                  ? '银行对账单编号'
                  : kind === 'stripe_report'
                    ? 'Stripe 报表运行号（frr_）'
                    : '渠道划拨号'}
                <input
                  name="reference"
                  className="input-field"
                  defaultValue={
                    kind === 'stripe_report' && report?.external_ref.startsWith('frr_')
                      ? report.external_ref
                      : undefined
                  }
                  maxLength={160}
                  required
                />
              </label>
            )
          )}
          {bank && (
            <label className="grid gap-1">
              银行账户标识
              <input name="bank_account" className="input-field" maxLength={160} required />
            </label>
          )}
          {kind === 'bank_match' && (
            <>
              <label className="grid gap-1">
                银行交易号
                <input name="bank_transaction" className="input-field" maxLength={160} required />
              </label>
              <label className="grid gap-1">
                手工匹配凭证编号（参考号不一致时填写）
                <input name="bank_evidence" className="input-field" maxLength={500} />
              </label>
            </>
          )}
          {uploads && (
            <label className="grid gap-1">
              {bankStatement ? '银行流水 CSV（最多 8 MB）' : 'PayPal 报表全部分卷（CSV 或 TAB，合计最多 8 MB）'}
              <input
                type="file"
                className="input-field"
                name="files"
                accept={bankStatement ? '.csv' : '.csv,.tab'}
                multiple={!bankStatement}
                required
              />
            </label>
          )}
          <label className="grid gap-1">
            操作依据
            <textarea name="reason" className="input-field" maxLength={500} required />
          </label>
        </fieldset>
        {error && (
          <p role="alert" className="text-[var(--cms-error)]">
            {error}
          </p>
        )}
        <div className="flex justify-end gap-2">
          <button type="button" className="btn-secondary" data-dialog-initial-focus disabled={busy} onClick={onClose}>
            {pendingConfirmation ? '关闭并查证' : '取消'}
          </button>
          <button type="submit" className="btn-primary" disabled={busy || !validAccount}>
            {busy ? '提交中…' : pendingConfirmation ? '重试同一请求' : '确认提交'}
          </button>
        </div>
      </form>
    </CmsDialog>
  );
}

function AutomaticPull({
  scope,
  accounts,
  onClose,
  onSaved,
}: {
  scope: PaymentScope;
  accounts: SettlementAccount[];
  onClose: () => void;
  onSaved: () => void;
}) {
  const [accountID, setAccountID] = useState(scope.account_id);
  const [enabled, setEnabled] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const account = accounts.find((item) => String(item.id) === accountID);
  return (
    <CmsDialog
      open
      onClose={onClose}
      title="按账户定期拉取"
      subtitle={paymentScopeLabel(scope)}
      closedBy="closerequest"
      closeDisabled={busy}
    >
      <form
        className="space-y-3 text-sm"
        onSubmit={async (event) => {
          event.preventDefault();
          if (busy || !account) return;
          const body = new FormData(event.currentTarget);
          setBusy(true);
          setError('');
          try {
            await apiRequest('PUT', `${base}/accounts/${account.id}/automatic`, {
              enabled: body.get('enabled') === 'true',
              reason: String(body.get('reason') ?? '').trim(),
            });
            window.showToast?.('自动拉取设置已保存');
            onSaved();
          } catch (failure) {
            setError(paymentError(failure));
          } finally {
            setBusy(false);
          }
        }}
      >
        <CustomSelect
          aria-label="自动拉取商户"
          width="100%"
          value={accountID}
          onChange={(value) => {
            setAccountID(value);
            setEnabled('');
          }}
          disabled={busy}
          options={[
            { value: '', label: '选择商户账户' },
            ...accounts.map((item) => ({
              value: String(item.id),
              label: `${providerLabels[item.provider]} · ${item.label}`,
            })),
          ]}
        />
        {account && (
          <div className="space-y-1">
            <StatusDot
              tone={account.auto_enabled ? 'success' : 'muted'}
              label={account.auto_enabled ? '已开启' : '已暂停'}
            />
            <p>最近成功（UTC） {paymentTime(account.auto_completed_at)}</p>
            {account.auto_error && (
              <p role="status" className="text-[var(--cms-error)]">
                {account.auto_error}
              </p>
            )}
          </div>
        )}
        <CustomSelect
          key={accountID}
          aria-label="定期拉取设置"
          name="enabled"
          width="100%"
          value={enabled}
          options={[
            { value: '', label: '选择操作' },
            { value: 'true', label: '开启' },
            { value: 'false', label: '暂停' },
          ]}
          onChange={setEnabled}
        />
        <label className="grid gap-1">
          授权依据或暂停原因
          <textarea className="input-field" name="reason" maxLength={500} required />
        </label>
        {error && (
          <p role="alert" className="text-[var(--cms-error)]">
            {error}
          </p>
        )}
        <div className="flex justify-end gap-2">
          <button type="button" className="btn-secondary" data-dialog-initial-focus onClick={onClose} disabled={busy}>
            取消
          </button>
          <button type="submit" className="btn-primary" disabled={busy || !account || !enabled}>
            保存设置
          </button>
        </div>
      </form>
    </CmsDialog>
  );
}

export function SettlementCommands({
  actor,
  scope,
  history,
  page,
  onPageChange,
  origin,
  onOriginClose,
  onSettled,
}: {
  actor: string;
  scope: PaymentScope;
  history: boolean;
  page: number;
  onPageChange: (page: number) => void;
  origin?: SettlementOrigin;
  onOriginClose: () => void;
  onSettled: () => void;
}) {
  const { canWrite } = usePermission();
  const canWriteRecon = canWrite('payments.reconciliation');
  const [kind, setKind] = useState<Kind>();
  const [draft, setDraft] = useState(() => readSettlementDraft(actor));
  const currentOrigin = useRef<SettlementOrigin | undefined>(undefined);
  const [resume, setResume] = useState(false);
  const [lookupRevision, setLookupRevision] = useState(0);
  const [lookup, setLookup] = useState<{ key: string; state: 'missing' | 'error' }>();
  const [automatic, setAutomatic] = useState(false);
  const [selected, setSelected] = useState<Command>();
  const [tracked, setTracked] = useState('');
  const [revision, setRevision] = useState(0);
  const [accountRevision, setAccountRevision] = useState(0);
  const accounts = usePaymentQuery<{ accounts: SettlementAccount[] }>(base + '/accounts', accountRevision);
  const scopedAccounts =
    accounts?.data?.accounts.filter(
      (item) =>
        item.environment === scope.environment &&
        (!scope.provider || item.provider === scope.provider) &&
        (!scope.account_id || String(item.id) === scope.account_id),
    ) ?? [];
  const provider =
    scope.provider || scopedAccounts.find((account) => String(account.id) === scope.account_id)?.provider;
  const state = usePaymentQuery<{ total: number; commands: Command[] }>(
    history
      ? `${base}/commands?${paymentParams({
          ...scope,
          page: String(page),
        })}`
      : '',
    revision,
    true,
  );
  const trackedResult = usePaymentQuery<Command>(
    tracked ? `${base}/commands/${encodeURIComponent(tracked)}` : '',
    revision,
    true,
  );
  const trackedCommand = trackedResult?.data;
  const pending = state?.data?.commands.some(isPending);
  const onSettledRef = useRef(onSettled);
  const hadPending = useRef(false);
  useEffect(() => {
    onSettledRef.current = onSettled;
  }, [onSettled]);
  useEffect(() => {
    if (trackedCommand && !isPending(trackedCommand)) onSettledRef.current();
  }, [trackedCommand]);
  useEffect(() => {
    if (state?.error || state?.refreshing || trackedResult?.refreshing) return;
    if (!pending && hadPending.current) onSettledRef.current();
    hadPending.current = Boolean(pending);
    if (!pending && (!trackedCommand || !isPending(trackedCommand))) return;

    const timer = window.setTimeout(() => setRevision((value) => value + 1), 5000);
    return () => window.clearTimeout(timer);
  }, [pending, tracked, trackedCommand, state?.data, state?.error, state?.refreshing, trackedResult?.refreshing]);

  useEffect(() => {
    if (draft && origin && origin !== currentOrigin.current) {
      onOriginClose();
      window.showToast?.('请先在对账工作台查证上一笔提交。', 'info');
    }
  }, [draft, origin, onOriginClose]);

  useEffect(() => {
    if (!canWriteRecon || !draft || kind || (origin && origin === currentOrigin.current) || resume) return;
    const controller = new AbortController();
    void paymentGet<Command>(`${base}/commands/request/${encodeURIComponent(draft.key)}`, controller.signal)
      .then((command) => {
        if (controller.signal.aborted) return;
        storeSettlementDraft(actor);
        setDraft(undefined);
        setTracked(command.id);
        setRevision((value) => value + 1);
      })
      .catch((failure: unknown) => {
        if (controller.signal.aborted) return;
        setLookup({
          key: draft.key,
          state: failure instanceof ApiError && failure.status === 404 ? 'missing' : 'error',
        });
      });
    return () => controller.abort();
  }, [actor, canWriteRecon, draft, kind, origin, resume, lookupRevision]);

  const recovered = resume && draft && Object.hasOwn(kinds, draft.kind) ? draft : undefined;
  const recoveredOrigin: SettlementOrigin | undefined =
    recovered?.kind === 'bank_match' && recovered.payout
      ? { ...recovered.scope, kind: recovered.kind, reference: recovered.payout }
      : recovered?.report && (recovered.kind === 'stripe_report' || recovered.kind === 'paypal_report')
        ? { ...recovered.scope, kind: recovered.kind, report: recovered.report }
        : undefined;
  const formOrigin = recoveredOrigin || (!draft || origin === currentOrigin.current ? origin : undefined);
  const activeKind = recovered ? (recovered.kind as Kind) : (formOrigin?.kind ?? kind);
  const formScope = recovered?.scope || formOrigin || scope;
  const formAccounts = recovered
    ? (accounts?.data?.accounts.filter(
        (account) =>
          account.environment === recovered.scope.environment && String(account.id) === recovered.scope.account_id,
      ) ?? [])
    : scopedAccounts;
  const lookupState = draft && lookup?.key === draft.key ? lookup.state : undefined;
  return (
    <div className="space-y-3">
      {canWriteRecon && (
        <div className="flex flex-wrap items-center gap-2">
          {provider === 'paypal' && (
            <button className="btn-secondary" disabled={Boolean(draft)} onClick={() => setKind('paypal_report')}>
              导入 PayPal 报表分卷
            </button>
          )}
          {provider === 'stripe' && (
            <>
              <button className="btn-secondary" disabled={Boolean(draft)} onClick={() => setKind('stripe_payout')}>
                拉取 Stripe 划拨
              </button>
              <button className="btn-secondary" disabled={Boolean(draft)} onClick={() => setKind('stripe_report')}>
                导入 Stripe 报表
              </button>
            </>
          )}
          <button className="btn-secondary" disabled={Boolean(draft)} onClick={() => setKind('bank_statement')}>
            导入银行流水
          </button>
          {provider && (
            <button className="btn-secondary" onClick={() => setAutomatic(true)}>
              定期拉取设置
            </button>
          )}
        </div>
      )}
      {canWriteRecon && draft && !activeKind && (
        <div className="cms-card flex flex-wrap items-center justify-between gap-2 px-4 py-3 text-sm">
          <p role="status">
            {lookupState === 'missing'
              ? '暂未找到原命令。可按原字段和原文件恢复同一请求。'
              : lookupState === 'error'
                ? '原命令查证失败。'
                : '正在查证原财务命令…'}
          </p>
          <div className="flex gap-2">
            {lookupState && (
              <button
                type="button"
                className="btn-secondary"
                onClick={() => {
                  setLookup(undefined);
                  setLookupRevision((value) => value + 1);
                }}
              >
                重新查证
              </button>
            )}
            {lookupState === 'missing' &&
              (draft.kind !== 'bank_match' || draft.payout) &&
              Object.hasOwn(kinds, draft.kind) && (
                <button type="button" className="btn-primary" onClick={() => setResume(true)}>
                  恢复原请求
                </button>
              )}
          </div>
        </div>
      )}
      {accounts?.error && (
        <PaymentLoadError message={accounts.error} onRetry={() => setAccountRevision((value) => value + 1)} />
      )}
      {tracked && (
        <p role="status" className="text-xs text-[var(--cms-muted-fg)]">
          命令 {tracked} · {trackedCommand ? states[trackedCommand.status].label : '已受理，请查操作记录'}
        </p>
      )}
      {trackedResult?.error && (
        <PaymentLoadError message={trackedResult.error} onRetry={() => setRevision((value) => value + 1)} />
      )}
      {state?.error && <PaymentLoadError message={state.error} onRetry={() => setRevision((value) => value + 1)} />}
      {history && !state?.error && (
        <TableRegion
          ariaLabel="财务操作记录"
          footer={
            state?.data && (
              <Pagination
                page={page}
                pageSize={PAYMENT_PAGE_SIZE}
                total={state.data.total}
                onPageChange={onPageChange}
              />
            )
          }
        >
          <table className="cms-table" aria-busy={!state || state.refreshing}>
            <thead>
              <tr>
                <th scope="col">操作 / 编号</th>
                <th scope="col">渠道账户</th>
                <th scope="col">状态</th>
                <th scope="col">操作者 / 时间 UTC</th>
                <th scope="col">操作</th>
              </tr>
            </thead>
            <tbody>
              {!state ? (
                <SkeletonRows rows={4} cols={5} />
              ) : state.data?.commands.length ? (
                state.data.commands.map((item) => (
                  <tr key={item.id}>
                    <td>
                      {kinds[item.kind]}
                      <div className="font-mono">{item.id}</div>
                    </td>
                    <td>
                      {item.provider ? paymentScopeLabel(item) : '银行 · ' + paymentScopeLabel(item).split(' · ')[0]}
                    </td>
                    <td>
                      <StatusDot {...states[item.status]} />
                    </td>
                    <td>
                      {item.actor} · {paymentTime(item.created_at)}
                    </td>
                    <td>
                      <button className="btn-detail" onClick={() => setSelected(item)}>
                        详情
                      </button>
                    </td>
                  </tr>
                ))
              ) : (
                <tr>
                  <td colSpan={5} className="text-center">
                    <p role="status" className="text-[11px] text-[var(--cms-muted-fg)]">
                      当前范围没有操作记录
                    </p>
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </TableRegion>
      )}
      <CmsDialog
        open={Boolean(selected)}
        onClose={() => setSelected(undefined)}
        title={selected ? kinds[selected.kind] : '操作详情'}
        subtitle={selected ? paymentScopeLabel(selected) : undefined}
        variant="drawer"
        size="md"
      >
        {selected && (
          <div className="space-y-3 text-sm">
            <StatusDot {...states[selected.status]} />
            <p className="font-mono">{selected.id}</p>
            {selected.reference && <p>目标凭证 {selected.reference}</p>}
            {selected.report_issue && (
              <p>
                原差异 #{selected.report_issue.import_id} · {selected.report_issue.source_file} · 第{' '}
                {selected.report_issue.row_number} 行
              </p>
            )}
            <p>操作依据 {selected.reason}</p>
            <p>更新时间（UTC） {paymentTime(selected.updated_at)}</p>
            {selected.error && <p className="text-[var(--cms-error)]">{selected.error}</p>}
            {selected.result && (
              <dl className="space-y-2">
                {Object.entries(selected.result).map(([key, value]) => (
                  <div key={key}>
                    <dt className="text-xs text-[var(--cms-muted-fg)]">{key}</dt>
                    <dd className="break-all">{typeof value === 'string' ? value : JSON.stringify(value)}</dd>
                  </div>
                ))}
              </dl>
            )}
          </div>
        )}
      </CmsDialog>
      {canWriteRecon && activeKind && (
        <SettlementForm
          key={activeKind}
          kind={activeKind}
          scope={formScope}
          origin={formOrigin}
          accounts={formAccounts}
          draft={recovered}
          onPrepared={(next) => {
            currentOrigin.current = origin;
            setLookup(undefined);
            setDraft(next);
            return storeSettlementDraft(actor, next);
          }}
          onReject={() => {
            storeSettlementDraft(actor);
            setDraft(undefined);
          }}
          onClose={() => {
            setKind(undefined);
            setResume(false);
            onOriginClose();
          }}
          onAccepted={(id) => {
            storeSettlementDraft(actor);
            setDraft(undefined);
            setTracked(id);
            setKind(undefined);
            setResume(false);
            onOriginClose();
            onPageChange(1);
            setRevision((value) => value + 1);
            onSettled();
          }}
        />
      )}
      {canWriteRecon && automatic && (
        <AutomaticPull
          scope={scope}
          accounts={scopedAccounts}
          onClose={() => setAutomatic(false)}
          onSaved={() => {
            setAutomatic(false);
            setAccountRevision((value) => value + 1);
          }}
        />
      )}
    </div>
  );
}
