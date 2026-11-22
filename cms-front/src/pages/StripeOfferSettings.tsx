import { useEffect, useId, useState } from 'react';
import { CmsDialog } from '../components/ui/CmsDialog';
import { SkeletonRows, TableRegion } from '../components/ui/Primitives';
import { apiRequest } from '../utils/api';

type SaleMode = 'direct' | 'merchant_of_record';

type StripeOffer = {
  slot_id: number;
  version_id: number;
  template: string;
  name: string;
  amount_minor: number;
  cycle?: 'day' | 'week' | 'month';
  product_id?: string;
  price_id?: string;
  countries?: string[];
  sales_mode?: SaleMode;
  tax_liability_account?: string;
  enabled: boolean;
  verified_catalog?: object;
  release_evidence?: string;
  market_policy?: string;
};

const cycles = { day: '每日', week: '每周', month: '每月' };
const modes = { direct: '直收', merchant_of_record: 'Managed Payments' };

export function StripeOfferSettings({ accountID, accountLabel, environment, kind }: { accountID: number; accountLabel: string; environment: string; kind: 'one-time' | 'vip' }) {
  const [offers, setOffers] = useState<StripeOffer[] | null>(null);
  const [loadError, setLoadError] = useState('');
  const [revision, setRevision] = useState(0);
  const [editing, setEditing] = useState<StripeOffer | null>(null);
  const [countries, setCountries] = useState('');
  const [reason, setReason] = useState('');
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState('');
  const formID = useId();
  const path = `/payments/channels/${accountID}/stripe-${kind}`;
  const name = kind === 'vip' ? 'Stripe VIP 套餐' : 'Stripe 整本套餐';

  useEffect(() => {
    const controller = new AbortController();
    void apiRequest('GET', path, undefined, false, controller.signal).then((result) => {
      if (!controller.signal.aborted) {
        setOffers(result.offers ?? []);
        setLoadError('');
      }
    }).catch((error: unknown) => {
      if (!controller.signal.aborted) setLoadError(error instanceof Error ? error.message : '读取 Stripe 套餐失败。');
    });
    return () => controller.abort();
  }, [path, revision]);

  const open = (offer: StripeOffer) => {
    setCountries(offer.countries?.join(', ') ?? '');
    setReason('');
    setSaveError('');
    setEditing({ ...offer, sales_mode: offer.sales_mode ?? 'direct' });
  };
  const save = async () => {
    if (!editing || saving) return;
    const allowed = countries.split(/[\s,，]+/).filter(Boolean).map((country) => country.toUpperCase());
    if (!allowed.length || allowed.some((country) => !/^[A-Z]{2}$/.test(country)) || !reason.trim()) {
      setSaveError('填写两位国家代码及变更原因。');
      return;
    }
    setSaving(true);
    setSaveError('');
    try {
      await apiRequest('PUT', `${path}/${editing.slot_id}`, {
        product_id: editing.product_id,
        price_id: editing.price_id,
        sales_mode: editing.sales_mode,
        tax_liability_account: editing.sales_mode === 'merchant_of_record' ? editing.tax_liability_account : '',
        countries: allowed,
        enabled: editing.enabled,
        reason: reason.trim(),
        release_evidence: editing.release_evidence?.trim() ?? '',
        market_policy: editing.market_policy?.trim() ?? '',
      });
      setEditing(null);
      setRevision((value) => value + 1);
    } catch (error) {
      setSaveError(error instanceof Error ? error.message : '保存 Stripe 套餐失败。');
    } finally {
      setSaving(false);
    }
  };

  return (
    <section className="space-y-3" aria-label={name}>
      <h2 className="text-base font-semibold">{name} · {accountLabel} · {environment === 'live' ? '正式' : '沙箱'}</h2>
      {loadError && <div role="alert">{loadError}
        <button type="button" className="btn-secondary" onClick={() => setRevision((value) => value + 1)}>重试</button>
      </div>}
      <TableRegion ariaLabel={name}>
        <table className="cms-table">
          <thead><tr><th scope="col">套餐</th><th scope="col">价格</th><th scope="col">销售模式</th>
            <th scope="col">市场</th><th scope="col">状态</th><th scope="col">操作</th></tr></thead>
          <tbody>
            {!offers && !loadError ? <SkeletonRows rows={2} cols={6} /> : offers?.map((offer) => (
              <tr key={offer.slot_id}>
                <th scope="row" className="text-left">{offer.name} · {offer.template} · #{offer.version_id}</th>
                <td>USD {(offer.amount_minor / 100).toFixed(2)} · {offer.cycle ? cycles[offer.cycle] : '单次'}</td>
                <td>{offer.sales_mode ? modes[offer.sales_mode] : '—'}</td>
                <td>{offer.countries?.join(', ') || '—'}</td>
                <td>{offer.enabled ? (offer.verified_catalog ? '已启用 · Stripe 已核验' : '已启用 · 待渠道核验') : '暂停'}</td>
                <td><button type="button" className="btn-detail" onClick={() => open(offer)}>编辑映射</button></td>
              </tr>
            ))}
          </tbody>
        </table>
      </TableRegion>
      {offers?.length === 0 && <p role="status">暂无可映射的套餐。</p>}
      <CmsDialog open={editing !== null} onClose={() => { if (!saving) setEditing(null); }}
        title="修改 Stripe 套餐映射" closeDisabled={saving} closedBy="closerequest"
        subtitle={editing ? `${editing.name} · USD ${(editing.amount_minor / 100).toFixed(2)} · ${editing.cycle ? cycles[editing.cycle] : '单次'}` : undefined}
        footer={<>
          <button type="button" className="btn-secondary" data-dialog-initial-focus disabled={saving} onClick={() => setEditing(null)}>取消</button>
          <button type="submit" form={formID} className="btn-primary" disabled={saving}>{saving ? '保存中…' : '保存映射'}</button>
        </>}>
        {editing && (
          <form id={formID} className="grid gap-3" onSubmit={(event) => { event.preventDefault(); void save(); }}>
            <label className="grid gap-1">Stripe Product ID
              <input className="input-field" required pattern="prod_[A-Za-z0-9]+" maxLength={100} value={editing.product_id ?? ''}
                onChange={(event) => setEditing({ ...editing, product_id: event.target.value })} />
            </label>
            <label className="grid gap-1">Stripe Price ID
              <input className="input-field" required pattern="price_[A-Za-z0-9]+" maxLength={100} value={editing.price_id ?? ''}
                onChange={(event) => setEditing({ ...editing, price_id: event.target.value })} />
            </label>
            <label className="grid gap-1">销售模式
              <select className="input-field" value={editing.sales_mode}
                onChange={(event) => setEditing({ ...editing, sales_mode: event.target.value as SaleMode })}>
                <option value="direct">直收</option><option value="merchant_of_record">Managed Payments</option>
              </select>
            </label>
            {editing.sales_mode === 'merchant_of_record' &&
              <label className="grid gap-1">Stripe 确认的税务责任账户
                <input className="input-field" required pattern="acct_[A-Za-z0-9]+" maxLength={100} value={editing.tax_liability_account ?? ''}
                  onChange={(event) => setEditing({ ...editing, tax_liability_account: event.target.value })} />
              </label>}
            <label className="grid gap-1">获批市场的两位国家代码，以逗号分隔
              <input className="input-field" required value={countries} onChange={(event) => setCountries(event.target.value)} />
            </label>
            <label><input type="checkbox" checked={editing.enabled} onChange={(event) => setEditing({ ...editing, enabled: event.target.checked })} /> 允许读者发起新购买</label>
            {environment === 'live' && <>
              <label className="grid gap-1">账户、商品、市场和销售模式的验收及批准凭证
                <textarea className="input-field" maxLength={500} required={editing.enabled} value={editing.release_evidence ?? ''}
                  onChange={(event) => setEditing({ ...editing, release_evidence: event.target.value })} />
              </label>
              <label className="grid gap-1">账单市场约束及范围外交易的获批税务处理办法
                <textarea className="input-field" maxLength={500} required={editing.enabled} value={editing.market_policy ?? ''}
                  onChange={(event) => setEditing({ ...editing, market_policy: event.target.value })} />
              </label>
            </>}
            <label className="grid gap-1">变更原因
              <textarea className="input-field" required maxLength={500} value={reason} onChange={(event) => setReason(event.target.value)} />
            </label>
            {saveError && <p role="alert">{saveError}</p>}
          </form>
        )}
      </CmsDialog>
    </section>
  );
}
