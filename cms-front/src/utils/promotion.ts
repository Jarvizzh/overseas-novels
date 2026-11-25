import type { PromotionLinkDomainOption, SystemDomain } from './api';

/** Prepend https:// only when a host string does not already carry an HTTP(S) scheme. */
export function normalizeDomainHref(domain: string): string {
  const trimmed = domain.trim();
  if (/^https?:\/\//i.test(trimmed)) return trimmed;
  return `https://${trimmed}`;
}

export interface PromotionBuildInput {
  novelId: number;
  name: string;
  novelTitle?: string;
  chapterIndex: string;
  utmSource: string;
  utmCampaign: string;
  domainId: string;
  fbPixelId: string;
  rechargeTemplateId: string;
  coinCostPerThousand: string;
  startPayChapterIndex: string;
  linkId?: number;
}

export interface PromotionBuildResult {
  error?: string;
  baseUrl?: string;
  path?: '/content' | '/detail';
  params?: URLSearchParams;
  finalUrl?: string;
  payload?: {
    name: string;
    novel_id: number;
    novel_title?: string;
    chapter_index: number;
    utm_source: string;
    utm_campaign: string;
    generated_url: string;
    domain_id: number | null;
    domain: string;
    fb_pixel_id: number | null;
    recharge_template_id: number | null;
    coin_cost_per_thousand: number | null;
    start_pay_chapter_index: number | null;
  };
}

/** Validate promotion form inputs and build consistent landing URL + API payload for create/edit flows. */
export function validateAndBuildPromotionPayload(
  input: PromotionBuildInput,
  domains: Array<SystemDomain | PromotionLinkDomainOption>,
  pixels: Array<{ id: number; pixel_id: string }>,
): PromotionBuildResult {
  if (!input.fbPixelId) {
    return { error: '推广链接必须显式绑定 Facebook 像素！' };
  }

  const chapterRaw = String(input.chapterIndex ?? '').trim();
  const parsedChapter = chapterRaw === '' ? 0 : Number(chapterRaw);
  if (chapterRaw !== '' && (!Number.isInteger(parsedChapter) || parsedChapter < 0)) {
    return { error: '落地章节索引须为不小于 0 的整数' };
  }

  const costRaw = String(input.coinCostPerThousand ?? '').trim();
  if (costRaw !== '') {
    const parsedCost = Number(costRaw);
    if (!Number.isInteger(parsedCost) || parsedCost < 0) {
      return { error: '专属千字计费须为不小于 0 的整数' };
    }
  }

  const payChRaw = String(input.startPayChapterIndex ?? '').trim();
  if (payChRaw !== '') {
    const parsedPayCh = Number(payChRaw);
    if (!Number.isInteger(parsedPayCh) || parsedPayCh < 1) {
      return { error: '专属起付章节须为不小于 1 的整数' };
    }
  }

  const activeDomains = (domains || []).filter((d) => d.status === undefined || d.status === 1);
  const targetDomainObj = activeDomains.find((d) => String(d.id) === input.domainId);
  const defaultDomainObj =
    activeDomains.find((d) => d.is_default) || activeDomains.find((d) => d.type === 'main') || activeDomains[0];
  const domainHost = targetDomainObj
    ? targetDomainObj.domain
    : defaultDomainObj
      ? defaultDomainObj.domain
      : typeof window !== 'undefined'
        ? window.location.host
        : '';

  const baseUrl = normalizeDomainHref(domainHost).replace(/\/+$/, '');
  const utmSource = input.utmSource.trim();
  const utmCampaign = input.utmCampaign.trim();

  const params = new URLSearchParams();
  params.set('novel_id', String(input.novelId));
  if (parsedChapter > 0) params.set('chapter_index', String(parsedChapter));
  if (utmSource) params.set('utm_source', utmSource);
  if (utmCampaign) params.set('utm_campaign', utmCampaign);

  const targetPixel = pixels.find((p) => p.id === parseInt(input.fbPixelId, 10));
  if (targetPixel) params.set('pixel_id', targetPixel.pixel_id);
  if (costRaw !== '') params.set('cost', costRaw);
  if (payChRaw !== '') params.set('pay_ch', payChRaw);
  if (input.linkId !== undefined) params.set('link_id', String(input.linkId));

  const path: '/content' | '/detail' = parsedChapter > 0 ? '/content' : '/detail';
  const finalUrl = `${baseUrl}${path}?${params.toString()}`;

  return {
    baseUrl,
    path,
    params,
    finalUrl,
    payload: {
      name: input.name,
      novel_id: input.novelId,
      ...(input.novelTitle !== undefined ? { novel_title: input.novelTitle } : {}),
      chapter_index: parsedChapter,
      utm_source: utmSource,
      utm_campaign: utmCampaign,
      generated_url: finalUrl,
      domain_id: targetDomainObj ? targetDomainObj.id : defaultDomainObj ? defaultDomainObj.id : null,
      domain: targetDomainObj ? targetDomainObj.domain : defaultDomainObj ? defaultDomainObj.domain : '',
      fb_pixel_id: input.fbPixelId ? parseInt(input.fbPixelId, 10) : null,
      recharge_template_id: input.rechargeTemplateId ? parseInt(input.rechargeTemplateId, 10) : null,
      coin_cost_per_thousand: costRaw !== '' ? parseInt(costRaw, 10) : null,
      start_pay_chapter_index: payChRaw !== '' ? parseInt(payChRaw, 10) : null,
    },
  };
}
