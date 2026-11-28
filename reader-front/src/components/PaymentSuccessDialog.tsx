import React, { useEffect, useId, useRef } from 'react';
import { BookOpen, Crown, Sparkles } from 'lucide-react';
import { GoldCoin } from './GoldCoin';

export interface PaymentSuccessDialogProps {
  open: boolean;
  isVip: boolean;
  vipTitle?: string;
  isWholeBook: boolean;
  novelTitle?: string;
  coinsAdded: number;
  actionLabel: string;
  onClose: () => void;
  onAction: () => void;
}

const isPointerOutside = (e: React.PointerEvent<HTMLDialogElement>, dialog: HTMLDialogElement) => {
  const r = dialog.getBoundingClientRect();
  return e.clientX < r.left || e.clientX > r.right || e.clientY < r.top || e.clientY > r.bottom;
};

export const PaymentSuccessDialog: React.FC<PaymentSuccessDialogProps> = ({
  open,
  isVip,
  vipTitle = 'StarNovel Unlimited',
  isWholeBook,
  novelTitle,
  coinsAdded,
  actionLabel,
  onClose,
  onAction,
}) => {
  const dialogRef = useRef<HTMLDialogElement>(null);
  const pressedOutsideRef = useRef(false);
  const titleId = useId();

  useEffect(() => {
    const dialog = dialogRef.current;
    if (!dialog) return;
    if (open && !dialog.open) {
      dialog.showModal();
    } else if (!open && dialog.open) {
      dialog.close();
    }
  }, [open]);

  return (
    <dialog
      ref={dialogRef}
      className="sn-dialog"
      aria-labelledby={titleId}
      onCancel={(e) => {
        e.preventDefault();
        onClose();
      }}
      onPointerDown={(e) => {
        if (dialogRef.current) {
          pressedOutsideRef.current = isPointerOutside(e, dialogRef.current);
        }
      }}
      onPointerUp={(e) => {
        if (dialogRef.current && pressedOutsideRef.current && isPointerOutside(e, dialogRef.current)) {
          onClose();
        }
        pressedOutsideRef.current = false;
      }}
      style={{ width: '100%', maxWidth: '340px', padding: '26px 24px', textAlign: 'center' }}
    >
      <div
        style={{
          width: '52px',
          height: '52px',
          borderRadius: '50%',
          backgroundColor: 'var(--accent-light)',
          color: 'var(--accent-text)',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          margin: '0 auto 14px',
        }}
      >
        {isVip ? <Crown size={24} strokeWidth={1.8} aria-hidden="true" /> : <Sparkles size={24} strokeWidth={1.8} aria-hidden="true" />}
      </div>
      <h2 id={titleId} style={{ fontFamily: 'var(--font-display)', fontSize: '1.25rem', fontWeight: 600, marginBottom: '8px' }}>
        {isVip ? 'Unlimited Membership Activated' : 'Purchase Complete'}
      </h2>
      <p style={{ fontSize: '0.875rem', color: 'var(--text-secondary)', marginBottom: '18px', lineHeight: 1.5 }}>
        {isVip
          ? 'You now have unlimited access to every story and chapter across StarNovel.'
          : isWholeBook
            ? 'This novel is now unlocked, including future chapters.'
            : 'Your coins have been credited to your wallet. Enjoy reading!'}
      </p>

      {isVip ? (
        <div
          style={{
            display: 'inline-flex',
            alignItems: 'center',
            gap: '6px',
            backgroundColor: 'var(--accent-light)',
            color: 'var(--accent-text)',
            fontWeight: 600,
            padding: '7px 14px',
            borderRadius: '999px',
            margin: '0 auto 20px',
            fontSize: '0.8125rem',
          }}
        >
          <Crown size={14} strokeWidth={1.9} aria-hidden="true" />
          <span>{vipTitle} Active</span>
        </div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', gap: '8px', margin: '0 auto 20px' }}>
          {isWholeBook && (
            <div
              style={{
                display: 'inline-flex',
                alignItems: 'center',
                gap: '6px',
                backgroundColor: 'var(--accent-light)',
                color: 'var(--accent-text)',
                fontWeight: 600,
                padding: '7px 14px',
                borderRadius: '999px',
                fontSize: '0.8125rem',
              }}
            >
              <BookOpen size={14} strokeWidth={1.9} aria-hidden="true" />
              <span>{novelTitle ? `${novelTitle} Unlocked` : 'Full Novel Unlocked'}</span>
            </div>
          )}
          {coinsAdded > 0 && (
            <div
              className="tabular-nums"
              style={{
                display: 'inline-flex',
                alignItems: 'center',
                gap: '6px',
                backgroundColor: 'var(--accent-light)',
                color: 'var(--accent-text)',
                fontWeight: 700,
                padding: '7px 16px',
                borderRadius: '999px',
                fontSize: '0.875rem',
              }}
            >
              <GoldCoin size={15} />
              <span>+{coinsAdded.toLocaleString()} Coins Added</span>
            </div>
          )}
        </div>
      )}

      <button
        type="button"
        autoFocus
        className="btn-cta-primary"
        style={{ width: '100%', borderRadius: '999px' }}
        onClick={onAction}
      >
        {actionLabel}
      </button>
    </dialog>
  );
};
