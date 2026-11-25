import type { PaymentScope } from './paymentWorkspace';

export type ReportIssueOrigin = {
  import_id: string;
  source_file: string;
  row_number: number;
  external_ref: string;
};

export type SettlementDraft = {
  key: string;
  digest: string;
  kind: string;
  scope: PaymentScope;
  payout?: string;
  report?: ReportIssueOrigin;
};

function storageKey(actor: string) {
  return 'cms-settlement-draft:' + actor;
}

export function readSettlementDraft(actor: string): SettlementDraft | undefined {
  try {
    const saved: Partial<SettlementDraft> | null = JSON.parse(sessionStorage.getItem(storageKey(actor)) || 'null');
    if (
      !saved ||
      typeof saved.key !== 'string' ||
      !/^[a-f0-9-]{36}$/i.test(saved.key) ||
      typeof saved.digest !== 'string' ||
      !/^[a-f0-9]{64}$/.test(saved.digest) ||
      typeof saved.kind !== 'string' ||
      !saved.scope ||
      !['live', 'sandbox'].includes(saved.scope.environment) ||
      typeof saved.scope.account_id !== 'string' ||
      typeof saved.scope.provider !== 'string' ||
      (saved.payout !== undefined && typeof saved.payout !== 'string') ||
      (saved.report !== undefined &&
        (!saved.report ||
          typeof saved.report.import_id !== 'string' ||
          !/^[1-9]\d*$/.test(saved.report.import_id) ||
          typeof saved.report.source_file !== 'string' ||
          !Number.isSafeInteger(saved.report.row_number) ||
          saved.report.row_number < 1 ||
          typeof saved.report.external_ref !== 'string'))
    )
      return undefined;
    return saved as SettlementDraft;
  } catch {
    return undefined;
  }
}

export function storeSettlementDraft(actor: string, draft?: SettlementDraft) {
  try {
    if (draft) sessionStorage.setItem(storageKey(actor), JSON.stringify(draft));
    else sessionStorage.removeItem(storageKey(actor));
    return true;
  } catch {
    return false;
  }
}

async function sha256(data: BufferSource) {
  const digest = await crypto.subtle.digest('SHA-256', data);
  return Array.from(new Uint8Array(digest), (byte) => byte.toString(16).padStart(2, '0')).join('');
}

export async function settlementDraftDigest(form: FormData) {
  const files = form.getAll('files').filter((item) => item instanceof File);
  if (files.reduce((size, file) => size + file.size, 0) > 8 * 1024 * 1024) {
    throw new Error('报表文件合计不得超过 8 MB。');
  }
  const fields = [];
  for (const [name, value] of form.entries()) {
    fields.push(
      typeof value === 'string' ? [name, value] : [name, value.name, await sha256(await value.arrayBuffer())],
    );
  }
  return sha256(new TextEncoder().encode(JSON.stringify(fields)));
}
