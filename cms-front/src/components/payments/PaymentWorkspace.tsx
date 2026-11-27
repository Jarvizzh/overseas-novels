import { useId, useRef, useState, type KeyboardEvent, type ReactNode } from 'react';
import CustomSelect from '../CustomSelect';
import { type FilterChip, FilterChipsBar } from '../ui/Primitives';
import {
  environmentLabels,
  providerLabels,
  usePaymentQuery,
  type PaymentScope,
  type SettlementAccount,
} from '../../utils/paymentWorkspace';

type Tab<T> = { value: T; label: string; icon: ReactNode };

export function PaymentTabs<T extends string>({
  tabs,
  value,
  onChange,
  label,
  children,
}: {
  tabs: readonly Tab<T>[];
  value: T;
  onChange: (value: T) => void;
  label: string;
  children: ReactNode;
}) {
  const id = useId();
  const refs = useRef<Array<HTMLButtonElement | null>>([]);

  function onKeyDown(event: KeyboardEvent<HTMLDivElement>) {
    const current = refs.current.indexOf(document.activeElement as HTMLButtonElement);
    const count = tabs.length;
    const index = {
      ArrowLeft: (current + count - 1) % count,
      ArrowRight: (current + 1) % count,
      Home: 0,
      End: count - 1,
    }[event.key];
    if (current < 0 || index === undefined) return;
    event.preventDefault();
    refs.current[index]?.focus();
    refs.current[index]?.scrollIntoView({ block: 'nearest', inline: 'nearest' });
  }

  return (
    <div className="space-y-4">
      <div className="border-b border-[var(--cms-border)]">
        <div
          role="tablist"
          aria-label={label}
          onKeyDown={onKeyDown}
          className="flex items-center gap-6 overflow-x-auto h5-no-scrollbar"
        >
          {tabs.map((tab, index) => {
            const active = tab.value === value;
            return (
              <button
                key={tab.value}
                type="button"
                id={`${id}-${tab.value}`}
                role="tab"
                aria-selected={active}
                aria-controls={`${id}-panel`}
                tabIndex={active ? 0 : -1}
                ref={(element) => {
                  refs.current[index] = element;
                }}
                onClick={() => onChange(tab.value)}
                className={`focus-ring shrink-0 pb-2.5 text-sm font-medium whitespace-nowrap cursor-pointer inline-flex items-center gap-2 transition-colors ${
                  active ? 'text-[var(--cms-fg)]' : 'text-[var(--cms-muted-fg)] hover:text-[var(--cms-fg)]'
                }`}
              >
                {tab.icon}
                <span>{tab.label}</span>
              </button>
            );
          })}
        </div>
      </div>
      <div className="space-y-4" id={`${id}-panel`} role="tabpanel" aria-labelledby={`${id}-${value}`}>
        {children}
      </div>
    </div>
  );
}

export function PaymentScopeBar({
  scope,
  onChange,
  showAccount = true,
  allEnvironments = false,
  children,
}: {
  scope: PaymentScope;
  onChange: (scope: PaymentScope) => void;
  showAccount?: boolean;
  allEnvironments?: boolean;
  children?: ReactNode;
}) {
  const [revision, setRevision] = useState(0);
  const accounts = usePaymentQuery<{ accounts: SettlementAccount[] }>(
    showAccount ? '/payments/accounts' : '',
    revision,
  );
  const options =
    accounts?.data?.accounts.filter(
      (account) =>
        (!scope.environment || account.environment === scope.environment) &&
        (!scope.provider || account.provider === scope.provider),
    ) ?? [];
  const accountOptions = options.map((account) => ({
    value: String(account.id),
    label: [
      providerLabels[account.provider],
      !scope.environment && environmentLabels[account.environment],
      account.label,
    ]
      .filter(Boolean)
      .join(' · '),
  }));
  if (scope.account_id && !accountOptions.some((item) => item.value === scope.account_id)) {
    accountOptions.push({ value: scope.account_id, label: `商户 #${scope.account_id}` });
  }

  return (
    <div className="flex flex-wrap items-center justify-between gap-3">
      <div
        role="group"
        aria-label="支付渠道"
        className="inline-flex flex-wrap items-center gap-1 rounded-xl bg-[var(--cms-secondary)] p-1"
      >
        {['', 'paypal', 'stripe'].map((provider) => (
          <button
            key={provider}
            type="button"
            aria-pressed={scope.provider === provider}
            className={`focus-ring px-3 py-1.5 rounded-lg text-xs font-medium inline-flex items-center gap-2 cursor-pointer transition-colors ${
              scope.provider === provider
                ? 'bg-[var(--cms-card)] text-[var(--cms-fg)] shadow-xs'
                : 'text-[var(--cms-muted-fg)] hover:text-[var(--cms-fg)]'
            }`}
            onClick={() => {
              if (scope.provider !== provider) onChange({ ...scope, provider, account_id: '' });
            }}
          >
            {providerLabels[provider] ?? '全部渠道'}
          </button>
        ))}
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <CustomSelect
          aria-label="环境"
          width="104px"
          value={scope.environment}
          options={[
            ...(allEnvironments ? [{ value: '', label: '全部环境' }] : []),
            ...Object.entries(environmentLabels).map(([value, label]) => ({ value, label })),
          ]}
          onChange={(environment) => onChange({ ...scope, environment, account_id: '' })}
        />
        {showAccount && (
          <CustomSelect
            aria-label="商户账户"
            value={scope.account_id}
            options={[{ value: '', label: '全部商户账户' }, ...accountOptions]}
            onChange={(account_id) => onChange({ ...scope, account_id })}
          />
        )}
        {accounts?.error && (
          <button type="button" className="btn-secondary" onClick={() => setRevision((value) => value + 1)}>
            重试加载商户
          </button>
        )}
        {children}
      </div>
    </div>
  );
}

export function PaymentFilterBar({
  children,
  right,
  error,
  chips,
  onResetAll,
}: {
  children: ReactNode;
  right?: ReactNode;
  error?: string;
  chips?: FilterChip[];
  onResetAll?: () => void;
}) {
  return (
    <search className="block space-y-2">
      <div className="flex flex-wrap items-center justify-between gap-2.5">
        <div className="flex flex-wrap items-center gap-2 flex-1 min-w-[260px]">{children}</div>
        {right && (
          <div className="flex items-center gap-2 text-xs text-[var(--cms-muted-fg)] shrink-0">{right}</div>
        )}
      </div>
      {error && (
        <p role="alert" className="text-xs text-[var(--cms-error)] m-0">
          {error}
        </p>
      )}
      {chips && chips.length > 0 && <FilterChipsBar chips={chips} onResetAll={onResetAll} />}
    </search>
  );
}

export function PaymentFilterToggle({
  checked,
  onChange,
  label,
}: {
  checked: boolean;
  onChange: (checked: boolean) => void;
  label: string;
}) {
  return (
    <button
      type="button"
      aria-pressed={checked}
      onClick={() => onChange(!checked)}
      className={`focus-ring h-9 px-3 rounded-[10px] border text-xs font-medium inline-flex items-center gap-1.5 whitespace-nowrap cursor-pointer transition-colors ${
        checked
          ? 'border-[var(--cms-primary)] bg-[var(--cms-accent)] text-[var(--cms-primary)]'
          : 'border-[var(--cms-border)] bg-[var(--cms-card)] text-[var(--cms-muted-fg)] hover:bg-[var(--cms-content)] hover:text-[var(--cms-fg)]'
      }`}
    >
      <span
        className={`w-1.5 h-1.5 rounded-full shrink-0 ${
          checked ? 'bg-[var(--cms-primary)]' : 'bg-[var(--cms-placeholder)]'
        }`}
        aria-hidden="true"
      />
      <span>{label}</span>
    </button>
  );
}

export function PaymentLoadError({ message, onRetry }: { message: string; onRetry: () => void }) {
  return (
    <div role="alert" className="cms-card flex flex-wrap items-center justify-between gap-2 px-4 py-3 text-sm">
      <p className="text-[var(--cms-error)]">{message}</p>
      <button type="button" className="btn-secondary" onClick={onRetry}>
        重试
      </button>
    </div>
  );
}
