import type { ProductView, RechargeTemplateDTO } from './paymentCatalogApi';

export function filterEligibleTemplateProducts(envProducts: ProductView[]): ProductView[] {
  return envProducts.filter((p) => p.dual_channel_ready);
}

export function canDeleteTemplate(template: Pick<RechargeTemplateDTO, 'is_default' | 'linked_promotion_count'>): boolean {
  if (template.is_default) return false;
  return (template.linked_promotion_count ?? 0) <= 0;
}

export function canSetDefaultTemplate(template: Pick<RechargeTemplateDTO, 'environment' | 'is_default'>): boolean {
  return (template.environment ?? 'live') === 'live' && !template.is_default;
}

export function filterPromotionTemplatesByEnv<T extends { environment?: string }>(
  templates: T[],
  environment: 'live' | 'sandbox' = 'live',
): T[] {
  return templates.filter((t) => (t.environment ?? 'live') === environment);
}

export function filterLivePromotionTemplates<T extends { environment?: string }>(templates: T[]): T[] {
  return filterPromotionTemplatesByEnv(templates, 'live');
}

export function shouldLockDefaultTemplate(
  templates: Array<Pick<RechargeTemplateDTO, 'id' | 'is_default'>>,
  editingTemplateId: number | null,
): boolean {
  if (editingTemplateId === null) return false;
  const target = templates.find((t) => t.id === editingTemplateId);
  if (!target?.is_default) return false;
  return !templates.some((t) => t.is_default && t.id !== editingTemplateId);
}
