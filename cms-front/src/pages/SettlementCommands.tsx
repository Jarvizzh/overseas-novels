import { useEffect, useRef, useState } from 'react';
import { apiRequest } from '../utils/api';

const base = '/payments/reconciliation';
const kinds = {
  paypal_report: '导入 PayPal 结算报表',
  stripe_payout: '拉取 Stripe 划拨',
  stripe_report: '导入已生成的 Stripe 报表',
  bank_statement: '导入银行流水',
  bank_match: '匹配划拨和银行流水',
} as const;
type Kind = keyof typeof kinds;
type Account = { id: number; provider: string; environment: string; label: string; auto_enabled?: boolean; auto_error?: string };
type Command = { id: string; kind: Kind; reference?: string; status: 'pending' | 'running' | 'succeeded' | 'failed'; error?: string };
const states = { pending: '待处理', running: '处理中', succeeded: '已完成', failed: '失败' };

export function SettlementCommands({ environment, onSettled }: { environment: string; onSettled: () => void }) {
  const [kind, setKind] = useState<Kind>('paypal_report');
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [commands, setCommands] = useState<Command[]>([]);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState('');
  const [error, setError] = useState('');
  const [revision, setRevision] = useState(0);
  const [accountRevision, setAccountRevision] = useState(0);
  const [autoBusy, setAutoBusy] = useState(false);
  const key = useRef('');
  const onSettledRef = useRef(onSettled);
  useEffect(() => { onSettledRef.current = onSettled; }, [onSettled]);

  useEffect(() => {
    const controller = new AbortController();
    void apiRequest('GET', base + '/accounts', undefined, false, controller.signal)
      .then((result) => { if (!controller.signal.aborted) setAccounts(result.accounts ?? []); })
      .catch(() => { if (!controller.signal.aborted) setError('渠道账户读取失败。'); });
    return () => controller.abort();
  }, [accountRevision]);

  useEffect(() => {
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout>;
    let hadPending = false;
    const refresh = async () => {
      try {
        const result = await apiRequest('GET', base + '/commands?environment=' + encodeURIComponent(environment), undefined, false, controller.signal);
        if (controller.signal.aborted) return;
        const items: Command[] = result.commands ?? [];
        setCommands(items);
        const pending = items.some((item) => item.status === 'pending' || item.status === 'running');
        if (hadPending && !pending) onSettledRef.current();
        hadPending = pending;
        if (pending) timer = setTimeout(() => void refresh(), 4000);
      } catch {
        if (!controller.signal.aborted) setError('财务命令状态读取失败。');
      }
    };
    void refresh();
    return () => { controller.abort(); clearTimeout(timer); };
  }, [environment, revision]);

  const channels = accounts.filter((account) => account.environment === environment &&
    (kind === 'bank_match' || (kind === 'paypal_report' ? account.provider === 'paypal' : account.provider === 'stripe')));
  const bank = kind === 'bank_match' || kind === 'bank_statement';
  const submit = async (form: HTMLFormElement) => {
    if (busy) return;
    setBusy(true);
    setError('');
    setMessage('');
    try {
      const body = new FormData(form);
      body.set('environment', environment);
      if (!key.current) key.current = crypto.randomUUID();
      body.set('request_key', key.current);
      const saved = await apiRequest('POST', base + '/commands', body, true);
      key.current = '';
      setMessage('命令已受理 · ' + saved.id);
      setRevision((value) => value + 1);
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : '请求结果暂未确认。可以用原表单重试。');
    } finally {
      setBusy(false);
    }
  };

  const configureAutomatic = async (form: HTMLFormElement) => {
    if (autoBusy) return;
    const data = new FormData(form);
    setAutoBusy(true);
    setError('');
    try {
      const account = encodeURIComponent(String(data.get('account_id')));
      await apiRequest('PUT', base + '/accounts/' + account + '/automatic', {
        enabled: data.get('enabled') === 'true',
        reason: String(data.get('reason') ?? '').trim(),
      });
      setAccountRevision((value) => value + 1);
      setMessage('自动拉取设置已保存。');
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : '设置保存失败。');
    } finally {
      setAutoBusy(false);
    }
  };

  return (
    <details className="rounded border border-[var(--border-color)] p-3">
      <summary className="cursor-pointer font-semibold">报表导入与银行匹配</summary>
      <form className="mt-3 space-y-3" onChange={() => { key.current = ''; }}
        onSubmit={(event) => { event.preventDefault(); void submit(event.currentTarget); }}>
        <fieldset disabled={busy} className="grid max-w-xl gap-3">
          <label className="grid gap-1">操作
            <select name="kind" className="input-field" value={kind} onChange={(event) => setKind(event.target.value as Kind)}>
              {Object.entries(kinds).map(([value, label]) => <option key={value} value={value}>{label}</option>)}
            </select>
          </label>
          {kind !== 'bank_statement' && <label className="grid gap-1">渠道账户
            <select key={kind + environment} name="account_id" className="input-field" defaultValue="" required>
              <option value="" disabled>选择账户</option>
              {channels.map((account) => <option key={account.id} value={account.id}>{account.provider} · {account.label}</option>)}
            </select>
          </label>}
          {kind !== 'paypal_report' && <label className="grid gap-1">
            {kind === 'bank_statement' ? '银行对账单编号' : kind === 'stripe_report' ? 'Stripe 报表运行号（frr_）' : '渠道划拨号'}
            <input name="reference" className="input-field" maxLength={160} required />
          </label>}
          {bank && <label className="grid gap-1">银行账户标识
            <input name="bank_account" className="input-field" maxLength={160} required />
          </label>}
          {kind === 'bank_match' && <>
            <label className="grid gap-1">银行交易号
              <input name="bank_transaction" className="input-field" maxLength={160} required />
            </label>
            <label className="grid gap-1">手工匹配凭证编号（参考号不一致时填写）
              <input name="bank_evidence" className="input-field" maxLength={500} />
            </label>
          </>}
          {(kind === 'paypal_report' || kind === 'bank_statement') && <label className="grid gap-1">
            {kind === 'paypal_report' ? 'PayPal 报表全部分卷（CSV 或 TAB，合计最多 8 MB）' : '银行流水 CSV（最多 8 MB）'}
            <input key={kind} type="file" name="files" accept={kind === 'paypal_report' ? '.csv,.tab,text/csv,text/tab-separated-values' : '.csv,text/csv'}
              multiple={kind === 'paypal_report'} required />
          </label>}
          <label className="grid gap-1">操作依据
            <textarea className="input-field" name="reason" maxLength={500} required />
          </label>
          <button className="btn-primary justify-self-start" type="submit">{busy ? '提交中…' : '提交财务命令'}</button>
        </fieldset>
      </form>
      <details className="mt-3">
        <summary className="cursor-pointer">按账户定期拉取</summary>
        <form className="mt-2 grid max-w-xl gap-2" onSubmit={(event) => { event.preventDefault(); void configureAutomatic(event.currentTarget); }}>
          <label className="grid gap-1">已开通报表读取权限的渠道账户
            <select className="input-field" name="account_id" defaultValue="" required>
              <option value="" disabled>选择账户</option>
              {accounts.filter((account) => account.environment === environment).map((account) => (
                <option key={account.id} value={account.id}>{account.provider} · {account.label} · {account.auto_enabled ? '已开启' : '已暂停'}</option>
              ))}
            </select>
          </label>
          <label className="grid gap-1">定期拉取
            <select className="input-field" name="enabled" defaultValue="" required>
              <option value="" disabled>选择操作</option><option value="true">开启</option><option value="false">暂停</option>
            </select>
          </label>
          <label className="grid gap-1">报表授权依据或暂停原因
            <textarea className="input-field" name="reason" maxLength={500} required />
          </label>
          <button className="btn-secondary justify-self-start" type="submit" disabled={autoBusy}>保存自动拉取设置</button>
        </form>
        {accounts.filter((account) => account.environment === environment && account.auto_error).map((account) => (
          <p key={account.id} role="status">{account.provider} · {account.label} · {account.auto_error}</p>
        ))}
      </details>
      {message && <p role="status">{message}</p>}
      {error && <p role="alert">{error}</p>}
      <button type="button" className="btn-secondary mt-3" onClick={() => setRevision((value) => value + 1)}>刷新命令状态</button>
      {commands.length > 0 && <ul className="mt-3 space-y-1" aria-label="近期财务命令">
        {commands.map((item) => <li key={item.id}>{kinds[item.kind]} {item.reference} · {states[item.status]}{item.error && <p>{item.error}</p>}</li>)}
      </ul>}
    </details>
  );
}
