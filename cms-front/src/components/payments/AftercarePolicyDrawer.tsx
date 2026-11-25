import { useRef, useState } from 'react';
import { usePermission } from '../../context/PermissionContext';
import CustomSelect from '../CustomSelect';
import { CmsDialog } from '../ui/CmsDialog';
import { apiRequest, paymentCaseOwnersPath, type PaymentCaseOwner } from '../../utils/api';
import type { ChannelAccount } from '../../utils/paymentCatalogApi';
import { paymentError, paymentScopeLabel, usePaymentQuery } from '../../utils/paymentWorkspace';
import { PaymentLoadError } from './PaymentWorkspace';

type Policy = {
  configured: boolean;
  revision: string;
  dispute_lookup: boolean;
  hold_fulfillment: boolean;
  reviewer_id: string;
  finance_id: string;
  review_seconds: number | null;
  refund_seconds: number | null;
  reminder_seconds: number | null;
};

function PolicyForm({
  policy,
  reviewers,
  financeOwners,
  canWrite,
  submit,
  busy,
}: {
  policy: Policy;
  reviewers: PaymentCaseOwner[];
  financeOwners: PaymentCaseOwner[];
  canWrite: boolean;
  submit: (payload: object) => void;
  busy: boolean;
}) {
  const [hold, setHold] = useState(policy.hold_fulfillment);
  const [reviewer, setReviewer] = useState(policy.reviewer_id);
  const [finance, setFinance] = useState(policy.finance_id);
  const numberFields = [
    { name: 'review_seconds', label: '复核期限（秒）', required: hold },
    { name: 'refund_seconds', label: '退款待办期限（秒）', required: hold },
    { name: 'reminder_seconds', label: '举证提醒提前量（秒）', required: false },
  ] as const;

  function ownerOptions(list: PaymentCaseOwner[], current: string) {
    const options = [
      { value: '', label: '请选择负责人' },
      ...list.map((item) => ({ value: item.id, label: item.name })),
    ];
    if (current && !list.some((item) => item.id === current))
      options.push({ value: current, label: '当前负责人已失效' });
    return options;
  }

  return (
    <form
      id="channel-aftercare-policy"
      className="space-y-4 text-sm"
      onSubmit={(event) => {
        event.preventDefault();
        if (!canWrite) return;
        const data = new FormData(event.currentTarget);
        submit({
          revision: policy.revision,
          dispute_lookup: data.has('dispute_lookup'),
          hold_fulfillment: hold,
          reviewer_id: reviewer,
          finance_id: finance,
          reason: String(data.get('reason') || '').trim(),
          ...Object.fromEntries(numberFields.map(({ name }) => [name, data.get(name) ? Number(data.get(name)) : null])),
        });
      }}
    >
      {!canWrite && (
        <p role="status" className="text-xs text-[var(--cms-muted-fg)]">
          当前账号仅有渠道策略只读查看权限，不可修改。
        </p>
      )}
      {!policy.configured && (
        <p role="status" className="text-xs text-[var(--cms-muted-fg)]">
          该商户尚未配置账户策略。
        </p>
      )}
      <label className="flex items-center gap-2">
        <input
          type="checkbox"
          name="dispute_lookup"
          defaultChecked={policy.dispute_lookup}
          disabled={!canWrite || busy}
        />
        主动补查渠道争议
      </label>
      <label className="flex items-center gap-2">
        <input
          type="checkbox"
          checked={hold}
          disabled={!canWrite || busy}
          onChange={(event) => setHold(event.target.checked)}
        />
        高风险收款暂缓履约
      </label>
      <div className="grid gap-3 sm:grid-cols-2">
        <label className="grid gap-1">
          风控复核负责人
          <CustomSelect
            aria-label="风控复核负责人"
            width="100%"
            disabled={!canWrite || busy}
            options={ownerOptions(reviewers, reviewer)}
            value={reviewer}
            onChange={setReviewer}
          />
        </label>
        <label className="grid gap-1">
          售后争议与退款负责人
          <CustomSelect
            aria-label="售后争议与退款负责人"
            width="100%"
            disabled={!canWrite || busy}
            options={ownerOptions(financeOwners, finance)}
            value={finance}
            onChange={setFinance}
          />
        </label>
        {numberFields.map(({ name, label, required }) => (
          <label key={name} className="grid gap-1">
            {label}
            <input
              name={name}
              className="input-field"
              type="number"
              min={1}
              max={2147483647}
              step={1}
              defaultValue={policy[name] ?? ''}
              required={required}
              disabled={!canWrite || busy}
            />
          </label>
        ))}
      </div>
      {canWrite && hold && (!reviewer || !finance) && (
        <p className="text-xs text-[var(--cms-muted-fg)]">启用暂缓履约时须指定两位岗位负责人及处置期限。</p>
      )}
      {canWrite && (
        <>
          <label className="grid gap-1">
            变更依据
            <textarea name="reason" required maxLength={500} className="input-field" rows={2} disabled={busy} />
          </label>
          <button type="submit" className="btn-primary" disabled={busy || (hold && (!reviewer || !finance))}>
            {busy ? '保存中…' : '保存策略'}
          </button>
        </>
      )}
    </form>
  );
}

export function AftercarePolicyDrawer({ account, onClose }: { account: ChannelAccount; onClose: () => void }) {
  const { canWrite } = usePermission();
  const canWriteChannels = canWrite('payments.channels');
  const [revision, setRevision] = useState(0);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const inFlight = useRef(false);
  const url = `/payments/channels/${account.id}/aftercare-policy`;
  const policy = usePaymentQuery<Policy>(url, revision);
  const reviewers = usePaymentQuery<PaymentCaseOwner[]>(paymentCaseOwnersPath('risk'), revision);
  const financeOwners = usePaymentQuery<PaymentCaseOwner[]>(paymentCaseOwnersPath('aftercare_finance'), revision);
  const fetchError = policy?.error || reviewers?.error || financeOwners?.error;
  const refresh = () => {
    setError('');
    setRevision((value) => value + 1);
  };

  async function submit(payload: object) {
    if (!canWriteChannels || inFlight.current) return;
    setBusy(true);
    inFlight.current = true;
    setError('');
    try {
      await apiRequest('PUT', url, payload);
      window.showToast?.('账户风控策略已保存');
      onClose();
    } catch (failure) {
      setError(paymentError(failure));
    } finally {
      inFlight.current = false;
      setBusy(false);
    }
  }

  return (
    <CmsDialog
      open
      title="账户风控策略"
      variant="drawer"
      size="md"
      closeDisabled={busy}
      onClose={onClose}
      subtitle={paymentScopeLabel({
        provider: account.provider,
        environment: account.environment,
        account_id: String(account.id),
      })}
    >
      <div className="space-y-4">
        {fetchError ? (
          <PaymentLoadError message={fetchError} onRetry={refresh} />
        ) : policy?.data && reviewers?.data && financeOwners?.data ? (
          <PolicyForm
            key={revision}
            policy={policy.data}
            reviewers={reviewers.data}
            financeOwners={financeOwners.data}
            canWrite={canWriteChannels}
            submit={(payload) => void submit(payload)}
            busy={busy}
          />
        ) : (
          <p role="status" className="text-sm">
            正在加载账户策略…
          </p>
        )}
        {error && <PaymentLoadError message={error} onRetry={refresh} />}
      </div>
    </CmsDialog>
  );
}
