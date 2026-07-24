import React from 'react';
import { createPortal } from 'react-dom';
import CustomSelect from '../CustomSelect';

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
  fbPixelId: string;
  rechargeTemplateId: string;
}

interface PromotionLinkModalProps {
  isOpen: boolean;
  onClose: () => void;
  promotionForm: PromotionFormData;
  setPromotionForm: (form: PromotionFormData) => void;
  pixels: FBPixel[];
  templates: RechargeTemplate[];
  onGenerateLink: () => void;
  generatedLink: string;
  onCopyLink: () => void;
}

export const PromotionLinkModal: React.FC<PromotionLinkModalProps> = ({
  isOpen,
  onClose,
  promotionForm,
  setPromotionForm,
  pixels,
  templates,
  onGenerateLink,
  generatedLink,
  onCopyLink,
}) => {
  React.useEffect(() => {
    if (!isOpen) return;
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        onClose();
      }
    };
    document.addEventListener('keydown', handleKeyDown);
    return () => document.removeEventListener('keydown', handleKeyDown);
  }, [isOpen, onClose]);

  if (!isOpen) return null;

  return createPortal(
    <div
      style={{
        position: 'fixed',
        top: 0,
        left: 0,
        right: 0,
        bottom: 0,
        backgroundColor: 'rgba(15, 23, 42, 0.4)',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        zIndex: 1000,
        padding: '20px',
      }}
      onClick={onClose}
    >
      <div
        className="glass-panel animate-fade-in"
        style={{
          width: '100%',
          maxWidth: '520px',
          backgroundColor: 'hsl(var(--bg-surface))',
          border: '1px solid hsl(var(--border))',
          borderRadius: '16px',
          display: 'flex',
          flexDirection: 'column',
          boxShadow: 'var(--shadow-lg)',
          overflow: 'hidden',
        }}
        onClick={(e) => e.stopPropagation()}
      >
        {/* Modal Header */}
        <div
          style={{
            padding: '20px 24px',
            borderBottom: '1px solid hsl(var(--border))',
            display: 'flex',
            justifyContent: 'space-between',
            alignItems: 'center',
          }}
        >
          <h3 style={{ fontSize: '1.25rem', fontWeight: 600 }}>生成书籍推广链接</h3>
        </div>

        {/* Modal Body */}
        <div style={{ padding: '24px', display: 'flex', flexDirection: 'column', gap: '16px' }}>
          <div>
            <label style={{ display: 'block', fontSize: '0.75rem', color: 'hsl(var(--text-secondary))', marginBottom: '4px' }}>
              推广活动名称
            </label>
            <input
              type="text"
              className="input-field"
              placeholder="e.g. FB_BlackFriday_Campaign"
              value={promotionForm.name}
              onChange={(e) => setPromotionForm({ ...promotionForm, name: e.target.value })}
            />
          </div>

          <div>
            <label style={{ display: 'block', fontSize: '0.75rem', color: 'hsl(var(--text-secondary))', marginBottom: '4px' }}>
              目标书籍 (自动填充)
            </label>
            <input type="text" className="input-field" value={promotionForm.title} disabled style={{ opacity: 0.7 }} />
          </div>

          <div>
            <label style={{ display: 'block', fontSize: '0.75rem', color: 'hsl(var(--text-secondary))', marginBottom: '4px' }}>
              落地章节序号 (从第几章开始阅读)
            </label>
            <input
              type="number"
              className="input-field"
              min={0}
              value={promotionForm.chapterIndex}
              onChange={(e) => setPromotionForm({ ...promotionForm, chapterIndex: e.target.value })}
            />
          </div>

          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '12px' }}>
            <div>
              <label style={{ display: 'block', fontSize: '0.75rem', color: 'hsl(var(--text-secondary))', marginBottom: '4px' }}>
                渠道 utm_source
              </label>
              <input
                type="text"
                className="input-field"
                placeholder="e.g. facebook"
                value={promotionForm.utmSource}
                onChange={(e) => setPromotionForm({ ...promotionForm, utmSource: e.target.value })}
              />
            </div>
            <div>
              <label style={{ display: 'block', fontSize: '0.75rem', color: 'hsl(var(--text-secondary))', marginBottom: '4px' }}>
                绑定 FB Pixel (可选)
              </label>
              <CustomSelect
                options={[
                  { value: '', label: '-- 不关联 Pixel --' },
                  ...pixels.map((p) => ({ value: String(p.id), label: `${p.name} (${p.pixel_id})` })),
                ]}
                value={promotionForm.fbPixelId}
                onChange={(val) => setPromotionForm({ ...promotionForm, fbPixelId: val })}
                width="100%"
              />
            </div>
          </div>

          <div>
            <label style={{ display: 'block', fontSize: '0.75rem', color: 'hsl(var(--text-secondary))', marginBottom: '4px' }}>
              营销系列 utm_campaign (可选)
            </label>
            <input
              type="text"
              className="input-field"
              placeholder="e.g. blackfriday_adset1"
              value={promotionForm.utmCampaign}
              onChange={(e) => setPromotionForm({ ...promotionForm, utmCampaign: e.target.value })}
            />
          </div>

          <div>
            <label style={{ display: 'block', fontSize: '0.75rem', color: 'hsl(var(--text-secondary))', marginBottom: '4px' }}>
              绑定充值模板 (可选)
            </label>
            <CustomSelect
              options={[
                { value: '', label: '-- 使用默认激活模板 --' },
                ...templates.map((t) => ({ value: String(t.id), label: `${t.name} ${t.is_default ? '(默认)' : ''}` })),
              ]}
              value={promotionForm.rechargeTemplateId}
              onChange={(val) => setPromotionForm({ ...promotionForm, rechargeTemplateId: val })}
              width="100%"
            />
          </div>

          <div
            style={{
              paddingTop: '16px',
              borderTop: '1px solid hsl(var(--border))',
              display: 'flex',
              justifyContent: 'flex-end',
              alignItems: 'center',
              gap: '12px',
              width: '100%',
              boxSizing: 'border-box',
            }}
          >
            <button type="button" className="btn-secondary" onClick={onClose} style={{ marginLeft: 'auto' }}>
              取消
            </button>
            <button type="button" className="btn-primary" onClick={onGenerateLink}>
              生成并保存推广链接
            </button>
          </div>

          {generatedLink && (
            <div
              style={{
                marginTop: '12px',
                padding: '12px',
                borderRadius: '8px',
                backgroundColor: 'hsl(var(--bg-card))',
                border: '1px solid hsl(var(--border))',
                display: 'flex',
                flexDirection: 'column',
                gap: '8px',
              }}
            >
              <div style={{ fontSize: '0.75rem', color: 'hsl(var(--text-secondary))' }}>推广链接地址：</div>
              <input type="text" className="input-field" readOnly value={generatedLink} style={{ fontSize: '0.8rem' }} />
              <button
                type="button"
                className="btn-secondary"
                style={{ width: '100%', fontSize: '0.8rem' }}
                onClick={onCopyLink}
              >
                复制链接到剪贴板
              </button>
            </div>
          )}
        </div>
      </div>
    </div>,
    document.body
  );
};
