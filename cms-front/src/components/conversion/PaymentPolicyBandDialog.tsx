import { type FormEvent, useId, useRef, useState } from 'react';
import CustomSelect from '../CustomSelect';
import { CmsDialog } from '../ui/CmsDialog';
import {
  type ActionFields,
  type BandInput,
  type BandRow,
  actionFields,
  amountToMinor,
  bandLabel,
  parseAction,
  paymentKindLabel,
  showMinor,
} from './paymentPolicy';

export interface BandEdit {
  key: number;
  kind: string;
  type: 'add_band' | 'update_band' | 'split_band' | 'merge_bands';
  rows: BandRow[];
}

const actionOptions = [
  { value: 'all', label: '全传' },
  { value: 'ratio', label: '按比例' },
  { value: 'cycle', label: '传 X 漏 Y' },
];

export function PaymentActionFields({
  id,
  title,
  fields,
  setFields,
  error,
  disabled,
}: {
  id: string;
  title: string;
  fields: ActionFields;
  setFields: (value: ActionFields) => void;
  error?: string;
  disabled?: boolean;
}) {
  const fieldInput = (key: 'ratio' | 'send' | 'skip', label: string, mode: 'decimal' | 'numeric') => (
    <label key={key} htmlFor={`${id}-${key}`} className="block space-y-1 min-w-0">
      <span className="block text-[var(--cms-muted-fg)]">{label}</span>
      <input
        className="input-field tabular-nums"
        id={`${id}-${key}`}
        name={`${id}-${key}`}
        inputMode={mode}
        maxLength={16}
        value={fields[key]}
        aria-invalid={Boolean(error)}
        aria-describedby={error ? `${id}-error` : undefined}
        onChange={(e) => setFields({ ...fields, [key]: e.target.value })}
        disabled={disabled}
      />
    </label>
  );
  return (
    <fieldset className="space-y-2 min-w-0" disabled={disabled}>
      <legend className="font-medium mb-2">{title}</legend>
      <div className="grid gap-3 sm:grid-cols-3">
        <div className="space-y-1">
          <label htmlFor={`${id}-mode`} className="block text-[var(--cms-muted-fg)]">
            回传方式
          </label>
          <CustomSelect
            id={`${id}-mode`}
            name={`${id}-mode`}
            width="100%"
            options={actionOptions}
            value={fields.mode}
            onChange={(mode) => setFields({ ...fields, mode: mode as ActionFields['mode'] })}
            disabled={disabled}
          />
        </div>
        {fields.mode === 'ratio' && fieldInput('ratio', '比例（%）', 'decimal')}
        {fields.mode === 'cycle' && fieldInput('send', '传几笔', 'numeric')}
        {fields.mode === 'cycle' && fieldInput('skip', '漏几笔', 'numeric')}
      </div>
      {error && (
        <p id={`${id}-error`} className="text-[var(--cms-error)] m-0">
          {error}
        </p>
      )}
    </fieldset>
  );
}

const titles = {
  add_band: '新增金额区间',
  update_band: '编辑金额区间',
  split_band: '拆分金额区间',
  merge_bands: '合并相邻区间',
};

export function PaymentPolicyBandDialog({
  edit,
  onClose,
  onSave,
}: {
  edit: BandEdit;
  onClose: () => void;
  onSave: (currency: string, bands: BandInput[]) => void;
}) {
  const id = useId();
  const form = useRef<HTMLFormElement>(null);
  const [original, second] = edit.rows.map(({ band }) => band);
  const initialCurrency = original?.currency ?? 'USD';
  const currencyLocked = !original?.branch_id;
  const fixed = edit.type === 'split_band' || edit.type === 'merge_bands';
  const [editableCurrency, setCurrency] = useState(initialCurrency);
  const currency = currencyLocked ? initialCurrency : editableCurrency;
  const [min, setMin] = useState(original ? showMinor(original.min_minor, initialCurrency) : '');
  const initialMax = second ? second.max_minor : original?.max_minor;
  const [max, setMax] = useState(initialMax ? showMinor(initialMax, initialCurrency) : '');
  const [pivot, setPivot] = useState('');
  const [action, setAction] = useState(() => actionFields(original));
  const [upperAction, setUpperAction] = useState(() => actionFields(original));
  const [error, setError] = useState<{ field: string; message: string } | null>(null);
  const clear = () => setError(null);
  const fail = (field: string, message: string) => {
    setError({ field, message });
    requestAnimationFrame(() => form.current?.querySelector<HTMLElement>('[aria-invalid="true"]')?.focus());
  };

  function submit(e: FormEvent) {
    e.preventDefault();
    const parsed = parseAction(action);
    if (!parsed) return fail('action', '请输入 0–100% 的比例，或两项合计在安全整数范围内的正整数');
    if (!/^[A-Z]{3}$/.test(currency) || currency === 'XXX')
      return fail('currency', '请输入三位 ISO 币种代码，例如 USD');
    const lower = amountToMinor(min, currency);
    const upper = max.trim() ? amountToMinor(max, currency) : null;
    if (lower === undefined) return fail('min', '请输入非负金额，并符合该币种精度');
    if (upper === undefined || (upper !== null && BigInt(upper) <= BigInt(lower))) {
      return fail('max', '最大金额须大于最小金额，并符合币种精度。留空表示无上限');
    }
    const band: BandInput = { ...parsed, min_minor: lower, max_minor: upper };
    if (edit.type === 'split_band') {
      const cut = amountToMinor(pivot, currency);
      if (
        cut === undefined ||
        BigInt(cut) <= BigInt(lower) ||
        (upper !== null && BigInt(cut) >= BigInt(upper))
      ) {
        return fail('pivot', '拆分点须在金额区间内部，并符合币种精度');
      }
      const upperRule = parseAction(upperAction);
      if (!upperRule) return fail('upper', '请检查高金额段的比例或循环笔数');
      onSave(currency, [
        { ...band, max_minor: cut },
        { ...upperRule, min_minor: cut, max_minor: upper },
      ]);
      return;
    }
    onSave(currency, [band]);
  }

  const input = (
    field: string,
    label: string,
    value: string,
    setValue?: (v: string) => void,
    inputMode?: 'decimal',
  ) => (
    <div className="space-y-1 min-w-0">
      <label htmlFor={`${id}-${field}`} className="block text-[var(--cms-muted-fg)]">
        {label}
      </label>
      <input
        id={`${id}-${field}`}
        name={field}
        className="input-field tabular-nums"
        inputMode={inputMode}
        autoCapitalize="characters"
        spellCheck={false}
        maxLength={field === 'currency' ? 3 : 26}
        value={value}
        readOnly={!setValue}
        aria-invalid={error?.field === field}
        aria-describedby={error?.field === field ? `${id}-error` : undefined}
        onChange={
          setValue
            ? (e) => {
                setValue(e.target.value);
                clear();
              }
            : undefined
        }
      />
      {error?.field === field && (
        <p id={`${id}-error`} className="text-[var(--cms-error)] m-0">
          {error.message}
        </p>
      )}
    </div>
  );

  return (
    <CmsDialog
      open
      onClose={onClose}
      title={titles[edit.type]}
      subtitle={paymentKindLabel(edit.kind)}
      closedBy="closerequest"
      footer={
        <>
          <button type="button" className="btn-secondary" onClick={onClose}>
            取消
          </button>
          <button type="submit" className="btn-primary" form={id}>
            确定
          </button>
        </>
      }
    >
      <form id={id} ref={form} method="post" noValidate onSubmit={submit} className="space-y-4 text-xs">
        {fixed && (
          <p className="m-0 text-[var(--cms-muted-fg)]">
            {edit.rows.map(({ band }) => bandLabel(band)).join(' · ')}
          </p>
        )}
        <div className="grid gap-3 sm:grid-cols-3">
          {!fixed &&
            input(
              'currency',
              currencyLocked ? '币种（固定）' : '币种',
              currency,
              currencyLocked ? undefined : (value) => setCurrency(value.toUpperCase()),
            )}
          {input('min', '最小金额（含）', min, setMin, 'decimal')}
          {input('max', '最大金额（不含，可留空）', max, setMax, 'decimal')}
        </div>
        {edit.type === 'split_band' && input('pivot', '拆分金额', pivot, setPivot, 'decimal')}
        <PaymentActionFields
          id={`${id}-action`}
          title={edit.type === 'split_band' ? '低金额段' : '回传规则'}
          fields={action}
          setFields={(v) => {
            setAction(v);
            clear();
          }}
          error={error?.field === 'action' ? error.message : undefined}
        />
        {edit.type === 'split_band' && (
          <PaymentActionFields
            id={`${id}-upper`}
            title="高金额段"
            fields={upperAction}
            setFields={(v) => {
              setUpperAction(v);
              clear();
            }}
            error={error?.field === 'upper' ? error.message : undefined}
          />
        )}
      </form>
    </CmsDialog>
  );
}
