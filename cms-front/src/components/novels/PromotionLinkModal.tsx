import React from 'react';
import CustomSelect from '../CustomSelect';
import { CmsDialog } from '../ui/CmsDialog';
import type { SystemDomain } from '../../utils/api';

export interface FBPixel {
  id: number;
  name: string;
  pixel_id: string;
  access_token: string;
}

export interface RechargeTemplate {
  id: number;
  name: string;
  is_default: boolean;
}

export interface PromotionFormData {
  name: string;
  novelId: number;
  title: string;
  chapterIndex: string;
  utmSource: string;
  utmCampaign: string;
  domainId?: string;
  fbPixelId: string;
  rechargeTemplateId: string;
  coinCostPerThousand: string;
  startPayChapterIndex: string;
}

interface PromotionLinkModalProps {
  isOpen: boolean;
  onClose: () => void;
  submitting?: boolean;
  promotionForm: PromotionFormData;
  setPromotionForm: (form: PromotionFormData) => void;
  pixels: FBPixel[];
  templates: RechargeTemplate[];
  domains?: SystemDomain[];
  onGenerateLink: () => void;
  generatedLink: string;
  onCopyLink: () => void;
}

export const PromotionLinkModal: React.FC<PromotionLinkModalProps> = ({
  isOpen,
  onClose,
  submitting = false,
  promotionForm,
  setPromotionForm,
  pixels,
  templates,
  domains = [],
  onGenerateLink,
  generatedLink,
  onCopyLink,
}) => {
  const activeDomains = (domains || []).filter((d) => d.status === 1);
  const defaultDomain =
    activeDomains.find((d) => d.is_default) ||
    activeDomains.find((d) => d.type === 'main') ||
    activeDomains[0];

  return (
    <CmsDialog
      open={isOpen}
      onClose={() => !submitting && onClose()}
      variant="drawer"
      title="生成书籍推广链接"
      subtitle={promotionForm.title ? `${promotionForm.title} · #${promotionForm.novelId}` : undefined}
      size="md"
      closedBy={submitting ? 'none' : 'any'}
      closeDisabled={submitting}
      footer={
        <>
          <button type="button" disabled={submitting} className="btn-secondary" onClick={() => !submitting && onClose()}>
            取消
          </button>
          <button type="button" disabled={submitting} className="btn-primary" onClick={onGenerateLink}>
            {submitting ? '生成中...' : '生成并保存推广链接'}
          </button>
        </>
      }
    >
      <div className="space-y-4 text-xs">
        <label className="block space-y-1.5">
          <span className="block font-medium text-[var(--cms-muted-fg)]">推广活动名称</span>
          <input
            type="text"
            autoFocus
            className="input-field"
            placeholder="e.g. FB_BlackFriday_Campaign"
            value={promotionForm.name}
            onChange={(e) => setPromotionForm({ ...promotionForm, name: e.target.value })}
          />
        </label>

        <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
          <label className="space-y-1.5">
            <span className="block font-medium text-[var(--cms-muted-fg)]">目标书籍</span>
            <input type="text" className="input-field" value={promotionForm.title} disabled />
          </label>

          <label className="space-y-1.5">
            <span className="block font-medium text-[var(--cms-muted-fg)]">落地章节序号</span>
            <input
              type="number"
              className="input-field tabular-nums"
              min={0}
              value={promotionForm.chapterIndex}
              onChange={(e) => setPromotionForm({ ...promotionForm, chapterIndex: e.target.value })}
            />
          </label>
        </div>

        <label className="block space-y-1.5">
          <span className="block font-medium text-[var(--cms-muted-fg)]">
            投放落地页域名（不选则默认主域名）
          </span>
          <CustomSelect
            width="100%"
            value={promotionForm.domainId || ''}
            onChange={(val) => setPromotionForm({ ...promotionForm, domainId: val })}
            options={[
              {
                value: '',
                label: `-- 默认主域名 (${defaultDomain?.domain || '主站默认域名'}) --`,
              },
              ...activeDomains.map((d) => ({
                value: String(d.id),
                label: `${d.name} (${d.domain}) ${d.is_default ? '[默认主域名]' : ''}`,
              })),
            ]}
          />
        </label>

        <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
          <label className="space-y-1.5">
            <span className="block font-medium text-[var(--cms-muted-fg)]">渠道 utm_source</span>
            <input
              type="text"
              className="input-field"
              placeholder="e.g. facebook"
              value={promotionForm.utmSource}
              onChange={(e) => setPromotionForm({ ...promotionForm, utmSource: e.target.value })}
            />
          </label>

          <label className="space-y-1.5">
            <span className="block font-medium text-[var(--cms-muted-fg)]">
              绑定 Facebook 像素 <span className="text-[var(--cms-error)]">*</span>
            </span>
            <CustomSelect
              width="100%"
              value={promotionForm.fbPixelId}
              onChange={(val) => setPromotionForm({ ...promotionForm, fbPixelId: val })}
              options={[
                {
                  value: '',
                  label: pixels.length === 0 ? '-- 请先添加像素配置 --' : '-- 选择绑定的 Facebook 像素 (必选) --',
                },
                ...pixels.map((p) => ({ value: String(p.id), label: `${p.name} (${p.pixel_id})` })),
              ]}
            />
          </label>
        </div>

        <label className="block space-y-1.5">
          <span className="block font-medium text-[var(--cms-muted-fg)]">营销系列 utm_campaign（可选）</span>
          <input
            type="text"
            className="input-field"
            placeholder="e.g. blackfriday_adset1"
            value={promotionForm.utmCampaign}
            onChange={(e) => setPromotionForm({ ...promotionForm, utmCampaign: e.target.value })}
          />
        </label>

        <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
          <label className="space-y-1.5">
            <span className="block font-medium text-[var(--cms-muted-fg)]">专属千字计费（可选）</span>
            <input
              type="number"
              className="input-field tabular-nums"
              placeholder="留空继承书籍/全局"
              min={0}
              value={promotionForm.coinCostPerThousand}
              onChange={(e) => setPromotionForm({ ...promotionForm, coinCostPerThousand: e.target.value })}
            />
          </label>

          <label className="space-y-1.5">
            <span className="block font-medium text-[var(--cms-muted-fg)]">专属起付章节（可选）</span>
            <input
              type="number"
              className="input-field tabular-nums"
              placeholder="留空继承书籍/全局"
              min={1}
              value={promotionForm.startPayChapterIndex}
              onChange={(e) => setPromotionForm({ ...promotionForm, startPayChapterIndex: e.target.value })}
            />
          </label>
        </div>

        <label className="block space-y-1.5">
          <span className="block font-medium text-[var(--cms-muted-fg)]">绑定充值模板（可选）</span>
          <CustomSelect
            width="100%"
            value={promotionForm.rechargeTemplateId}
            onChange={(val) => setPromotionForm({ ...promotionForm, rechargeTemplateId: val })}
            options={[
              { value: '', label: '-- 使用默认激活模板 --' },
              ...templates.map((t) => ({ value: String(t.id), label: `${t.name} ${t.is_default ? '(默认)' : ''}` })),
            ]}
          />
        </label>

        {generatedLink && (
          <div className="p-3.5 rounded-xl bg-[var(--cms-content)] border border-[var(--cms-border)] space-y-2">
            <div className="text-xs font-medium text-[var(--cms-muted-fg)]">已生成的推广链接地址</div>
            <input type="text" className="input-field font-mono-code text-xs" readOnly value={generatedLink} />
            <button type="button" className="btn-secondary w-full" onClick={onCopyLink}>
              复制链接到剪贴板
            </button>
          </div>
        )}
      </div>
    </CmsDialog>
  );
};
