import { type FormEvent, useState } from 'react';
import { apiRequest } from '../utils/api';

interface Integration {
  id: string;
  source: string;
  account_key: string;
  contract_version: string;
  enabled: boolean;
  allowed_apps: string[];
  allowed_events: string[];
  bindings: { destination_id: string; pixel_id: string; additional: boolean; retired: boolean }[];
}

function list(text: FormDataEntryValue | null) {
  return String(text ?? '')
    .split(',')
    .map((item) => item.trim())
    .filter(Boolean);
}

export default function ConversionIntegrationsPanel({ canWrite }: { canWrite: boolean }) {
  const [integrations, setIntegrations] = useState<Integration[]>();
  const [editing, setEditing] = useState<Integration>();
  const [creating, setCreating] = useState(false);
  const [loading, setLoading] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  async function load() {
    setLoading(true);
    setError('');
    try {
      setIntegrations(await apiRequest('GET', '/conversion/integrations'));
    } catch (err) {
      setError(err instanceof Error ? err.message : '接入配置暂不可用');
    } finally {
      setLoading(false);
    }
  }

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (busy) return;
    const data = new FormData(event.currentTarget);
    const destination = String(data.get('destination_id') ?? '').trim();
    const reason = String(data.get('reason') ?? '').trim();
    if (!reason) {
      setError('请输入操作原因。');
      return;
    }
    setError('');
    setBusy(true);
    try {
      const change = {
        reason,
        contract_version: String(data.get('contract_version') ?? '').trim(),
        enabled: data.has('enabled'),
        allowed_apps: list(data.get('allowed_apps')),
        allowed_events: list(data.get('allowed_events')),
        bindings: destination
          ? [
              {
                destination_id: destination,
                additional: data.get('route') === 'additional',
                remove: data.get('route') === 'remove',
              },
            ]
          : [],
      };
      if (editing) await apiRequest('PATCH', `/conversion/integrations/${editing.id}`, change);
      else
        await apiRequest('POST', '/conversion/integrations', {
          ...change,
          source: data.get('source'),
          account_key: String(data.get('account_key') ?? '').trim(),
        });
      setEditing(undefined);
      setCreating(false);
      await load();
    } catch (err) {
      setError(err instanceof Error ? err.message : '保存失败');
    } finally {
      setBusy(false);
    }
  }

  return (
    <details
      className="space-y-3"
      onToggle={(event) => {
        if (event.currentTarget.open && !integrations && !loading) void load();
      }}
    >
      <summary className="cursor-pointer text-sm font-semibold">接入与授权绑定</summary>
      {loading && (
        <p role="status" className="text-sm">
          正在加载接入配置…
        </p>
      )}
      {error && (
        <p role="alert" className="text-sm text-[var(--cms-error)]">
          {error}
        </p>
      )}
      {integrations?.map((item) => (
        <div key={item.id} className="space-y-1 rounded-xl border border-[var(--cms-border)] p-3 text-xs">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <span>
              {item.source} / {item.account_key} · #{item.id} · {item.enabled ? '启用' : '暂停'}
            </span>
            {canWrite && (
              <button
                type="button"
                className="btn-detail"
                disabled={busy}
                onClick={() => {
                  setCreating(false);
                  setEditing(item);
                }}
              >
                编辑接入
              </button>
            )}
          </div>
          {item.bindings.map((binding) => (
            <p key={binding.destination_id}>
              Pixel {binding.pixel_id} · 目标 ID {binding.destination_id} ·{' '}
              {binding.additional ? '自动附加' : '已授权'}
              {binding.retired && ' · 已退役'}
            </p>
          ))}
        </div>
      ))}
      {canWrite && (
        <button
          type="button"
          className="btn-secondary"
          disabled={busy}
          onClick={() => {
            setEditing(undefined);
            setCreating(true);
          }}
        >
          新建接入
        </button>
      )}
      {(editing || creating) && (
        <form
          key={editing?.id ?? 'new'}
          onSubmit={submit}
          className="grid gap-3 rounded-xl border border-[var(--cms-border)] p-4 text-xs sm:grid-cols-2"
        >
          {creating && (
            <>
              <label className="grid gap-1">
                来源
                <select className="input-field" name="source">
                  <option value="starnovel">StarNovel</option>
                  <option value="sinan">司南</option>
                </select>
              </label>
              <label className="grid gap-1">
                接入账户键
                <input
                  className="input-field"
                  name="account_key"
                  required
                  maxLength={100}
                  defaultValue="reader"
                />
              </label>
            </>
          )}
          <label className="grid gap-1">
            契约版本
            <input
              className="input-field"
              name="contract_version"
              required
              maxLength={100}
              defaultValue={editing?.contract_version ?? 'v1'}
            />
          </label>
          <label className="flex items-center gap-2">
            <input type="checkbox" name="enabled" defaultChecked={editing?.enabled ?? false} />
            启用接入
          </label>
          <label className="grid gap-1">
            授权应用（逗号分隔）
            <input
              className="input-field"
              name="allowed_apps"
              maxLength={1000}
              defaultValue={editing?.allowed_apps.join(', ')}
            />
          </label>
          <label className="grid gap-1">
            允许事件（逗号分隔）
            <input
              className="input-field"
              name="allowed_events"
              maxLength={1000}
              defaultValue={editing?.allowed_events.join(', ')}
            />
          </label>
          <label className="grid gap-1">
            变更绑定的目标 ID（选填）
            <input
              className="input-field"
              name="destination_id"
              inputMode="numeric"
              pattern="[1-9][0-9]{0,18}"
              maxLength={19}
            />
          </label>
          <label className="grid gap-1">
            绑定方式
            <select className="input-field" name="route">
              <option value="authorized">授权</option>
              <option value="additional">授权并自动附加</option>
              <option value="remove">解除授权</option>
            </select>
          </label>
          <label className="grid gap-1 sm:col-span-2">
            操作原因（必填）
            <textarea
              className="input-field user-invalid:border-[var(--cms-error)]"
              name="reason"
              required
              maxLength={500}
              rows={2}
            />
          </label>
          <div className="flex gap-2">
            <button type="submit" className="btn-primary" disabled={busy}>
              {busy ? '保存中…' : '保存接入'}
            </button>
            <button
              type="button"
              className="btn-secondary"
              disabled={busy}
              onClick={() => {
                setEditing(undefined);
                setCreating(false);
              }}
            >
              取消
            </button>
          </div>
        </form>
      )}
    </details>
  );
}
