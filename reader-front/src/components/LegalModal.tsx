import React, { useState, useEffect, useRef, useId } from 'react';
import { Mail, Clock, CheckCircle2, X } from 'lucide-react';
import { api } from '../utils/api';
import { useToast } from '../context/toastStore';

export type LegalModalType = 'terms' | 'privacy' | 'refund' | 'contact';

interface LegalModalProps {
  type: LegalModalType;
  isOpen: boolean;
  onClose: () => void;
  defaultEmail?: string;
}

export const LegalModal: React.FC<LegalModalProps> = ({
  type,
  isOpen,
  onClose,
  defaultEmail = '',
}) => {
  const { showToast } = useToast();
  const dialogRef = useRef<HTMLDialogElement>(null);
  const pressedOutsideRef = useRef(false);
  const titleId = useId();
  const emailInputId = useId();
  const subjectInputId = useId();
  const contentInputId = useId();

  const [email, setEmail] = useState(defaultEmail);
  const [subject, setSubject] = useState('Payment / Recharge Issue');
  const [content, setContent] = useState('');
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [submittedSuccess, setSubmittedSuccess] = useState(false);

  useEffect(() => {
    const dialog = dialogRef.current;
    if (!dialog) return;

    if (isOpen) {
      if (defaultEmail) {
        setEmail((prev) => prev || defaultEmail);
      }
      setSubmittedSuccess(false);
      if (!dialog.open) {
        dialog.showModal();
      }
    } else if (dialog.open) {
      dialog.close();
    }
  }, [isOpen, defaultEmail]);

  const handleFeedbackSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!email || !content) {
      showToast('Please fill in your contact email and message.', 'error');
      return;
    }

    setIsSubmitting(true);
    try {
      await api.submitFeedback(email, content, subject);
      setSubmittedSuccess(true);
      setContent('');
      showToast('Your message has been submitted. Our support team will reply via email.', 'success');
    } catch (err) {
      const msg = err instanceof Error ? err.message : 'Failed to submit message. Please try again.';
      showToast(msg, 'error');
    } finally {
      setIsSubmitting(false);
    }
  };

  const renderTitle = () => {
    switch (type) {
      case 'terms':
        return 'Terms of Service';
      case 'privacy':
        return 'Privacy Policy';
      case 'refund':
        return 'Refund & Purchase Policy';
      case 'contact':
        return 'Contact Us & Customer Support';
    }
  };

  const isPointerOutside = (e: React.PointerEvent<HTMLDialogElement>, dialog: HTMLDialogElement) => {
    const r = dialog.getBoundingClientRect();
    return e.clientX < r.left || e.clientX > r.right || e.clientY < r.top || e.clientY > r.bottom;
  };

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
      style={{ width: '100%', maxWidth: '480px', padding: '20px' }}
    >
      {/* Header */}
      <div
        style={{
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center',
          marginBottom: '16px',
          borderBottom: '1px solid var(--border-color)',
          paddingBottom: '12px',
          flexShrink: 0,
        }}
      >
        <h2
          id={titleId}
          style={{
            fontFamily: 'var(--font-display)',
            fontSize: '1.125rem',
            fontWeight: 600,
            color: 'var(--text-primary)',
            margin: 0,
          }}
        >
          {renderTitle()}
        </h2>
        <button
          type="button"
          className="header-btn"
          onClick={onClose}
          aria-label="Close"
        >
          <X size={18} strokeWidth={1.8} aria-hidden="true" />
        </button>
      </div>

      {/* Scrollable Body Content */}
      <div
        className="scroll-container-no-pad"
        style={{
          flex: 1,
          minHeight: 0,
          paddingRight: '4px',
          fontSize: '0.8125rem',
          color: 'var(--text-secondary)',
          lineHeight: 1.7,
        }}
      >
        {type === 'terms' && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: '12px' }}>
            <p><strong>Last Updated:</strong> August 2026</p>
            <p>
              Welcome to <strong>StarNovel</strong> (<strong>starsfic.com</strong>, &quot;StarNovel&quot;, &quot;we&quot;, &quot;us&quot;, or &quot;our&quot;). By accessing or using our online novel reading platform, websites, and associated services, you agree to be bound by these Terms of Service.
            </p>

            <h3 style={{ color: 'var(--text-primary)', fontWeight: 600, fontSize: '0.875rem', marginTop: '6px' }}>1. User Accounts</h3>
            <p>
              You may access content as a guest or create a registered account. You are responsible for safeguarding your login credentials and for all activities that occur under your account.
            </p>

            <h3 style={{ color: 'var(--text-primary)', fontWeight: 600, fontSize: '0.875rem', marginTop: '6px' }}>2. Virtual Currency and Purchases</h3>
            <p>
              Coins and VIP memberships purchased on starsfic.com are digital virtual tokens intended exclusively for unlocking reading content on this platform. Virtual currency has no cash value outside starsfic.com and cannot be transferred, exchanged, or redeemed for fiat currency.
            </p>

            <h3 style={{ color: 'var(--text-primary)', fontWeight: 600, fontSize: '0.875rem', marginTop: '6px' }}>3. Intellectual Property Rights</h3>
            <p>
              All literary works, book covers, illustrations, trademarks, software, and other materials available on starsfic.com are protected by copyright and intellectual property laws. Unauthorized reproduction, distribution, scraping, or commercial exploitation is strictly prohibited.
            </p>

            <h3 style={{ color: 'var(--text-primary)', fontWeight: 600, fontSize: '0.875rem', marginTop: '6px' }}>4. Code of Conduct</h3>
            <p>
              Users agree not to engage in unauthorized scraping, reverse engineering, fraudulent payments, chargeback abuse, or posting malicious, infringing, or harassing comments.
            </p>

            <h3 style={{ color: 'var(--text-primary)', fontWeight: 600, fontSize: '0.875rem', marginTop: '6px' }}>5. Contact &amp; Inquiries</h3>
            <p>
              For questions regarding these terms, please contact us at:{' '}
              <a href="mailto:support@startsfic.com" style={{ color: 'var(--accent-text)', fontWeight: 600 }}>
                support@startsfic.com
              </a>.
            </p>
          </div>
        )}

        {type === 'privacy' && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: '12px' }}>
            <p><strong>Last Updated:</strong> August 2026</p>
            <p>
              Your privacy is essential to <strong>StarNovel</strong> (<strong>starsfic.com</strong>). This Privacy Policy explains how we collect, use, and protect your information when you visit our website or use our reading services.
            </p>

            <h3 style={{ color: 'var(--text-primary)', fontWeight: 600, fontSize: '0.875rem', marginTop: '6px' }}>1. Information We Collect</h3>
            <p>
              - <strong>Account Information:</strong> Email address, nickname, and account preferences when registering.<br />
              - <strong>Usage &amp; Reading Data:</strong> Reading history, unlocked chapters, bookmarks, and device identifiers to synchronize reading progress across sessions.<br />
              - <strong>Transaction Data:</strong> Payment order ID, purchased coin amounts, timestamp, and payment status (we do not store your raw credit card or banking passwords).
            </p>

            <h3 style={{ color: 'var(--text-primary)', fontWeight: 600, fontSize: '0.875rem', marginTop: '6px' }}>2. How We Use Information</h3>
            <p>
              We use your data to provide reader functionalities, fulfill digital purchases, deliver customer support, detect fraud, and optimize user experience.
            </p>

            <h3 style={{ color: 'var(--text-primary)', fontWeight: 600, fontSize: '0.875rem', marginTop: '6px' }}>3. Third-Party Services &amp; Payment Gateways</h3>
            <p>
              Payments are securely processed via certified payment partners (including PayPal). Data transmitted during checkout is encrypted using industry-standard SSL protocols.
            </p>

            <h3 style={{ color: 'var(--text-primary)', fontWeight: 600, fontSize: '0.875rem', marginTop: '6px' }}>4. User Rights &amp; Data Deletion</h3>
            <p>
              You may request access to, correction, or deletion of your account and personal data at any time by contacting our privacy team at{' '}
              <a href="mailto:support@startsfic.com" style={{ color: 'var(--accent-text)', fontWeight: 600 }}>
                support@startsfic.com
              </a>.
            </p>
          </div>
        )}

        {type === 'refund' && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: '12px' }}>
            <p><strong>Last Updated:</strong> August 2026</p>
            <p>
              Thank you for purchasing reading coins and memberships on <strong>StarNovel</strong> (<strong>starsfic.com</strong>). Please read our refund policy carefully before making any purchases.
            </p>

            <h3 style={{ color: 'var(--text-primary)', fontWeight: 600, fontSize: '0.875rem', marginTop: '6px' }}>1. Nature of Digital Goods</h3>
            <p>
              Reading coins, chapter unlocks, and VIP passes on starsfic.com are <strong>intangible virtual digital items</strong> that are credited to your account immediately upon payment confirmation. As digital goods are consumed upon unlocking reading content, purchases are generally <strong>non-refundable</strong> once delivered.
            </p>

            <h3 style={{ color: 'var(--text-primary)', fontWeight: 600, fontSize: '0.875rem', marginTop: '6px' }}>2. Exceptions and Refund Eligibility</h3>
            <p>
              We provide full resolution or refunds under the following specific circumstances:<br />
              - <strong>Duplicate Charging:</strong> If you were accidentally billed multiple times for the same transaction due to a network glitch.<br />
              - <strong>Technical Failure / Non-Delivery:</strong> If a payment succeeded but coins were not credited to your account and our system cannot manually fulfill the credit within 24 hours.<br />
              - <strong>Unauthorized Transaction:</strong> Verified unauthorized or fraudulent use of your payment method.
            </p>

            <h3 style={{ color: 'var(--text-primary)', fontWeight: 600, fontSize: '0.875rem', marginTop: '6px' }}>3. How to Request Assistance</h3>
            <p>
              If you encounter any payment discrepancy, please submit a message via our <strong>Contact Us</strong> tab or email our support desk directly at{' '}
              <a href="mailto:support@startsfic.com" style={{ color: 'var(--accent-text)', fontWeight: 600 }}>
                support@startsfic.com
              </a>{' '}
              with:
            </p>
            <ul style={{ paddingLeft: '18px', margin: '4px 0' }}>
              <li>Your Account Email / User ID</li>
              <li>PayPal Order ID / Transaction Reference</li>
              <li>Screenshot or description of the issue</li>
            </ul>
            <p>
              Our billing support team investigates and responds to all inquiries within <strong>24 to 48 hours</strong>.
            </p>
          </div>
        )}

        {type === 'contact' && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: '14px' }}>
            <div
              style={{
                backgroundColor: 'var(--bg-primary)',
                borderRadius: '12px',
                padding: '14px',
                border: '1px solid var(--border-color)',
                display: 'flex',
                flexDirection: 'column',
                gap: '8px',
              }}
            >
              <div style={{ display: 'flex', alignItems: 'center', gap: '10px' }}>
                <Mail size={18} strokeWidth={1.75} style={{ color: 'var(--accent-text)', flexShrink: 0 }} aria-hidden="true" />
                <div>
                  <span style={{ fontSize: '0.6875rem', color: 'var(--text-tertiary)', display: 'block' }}>
                    Official Support Email
                  </span>
                  <a
                    href="mailto:support@startsfic.com"
                    style={{ fontSize: '0.875rem', fontWeight: 600, color: 'var(--accent-text)', textDecoration: 'none' }}
                  >
                    support@startsfic.com
                  </a>
                </div>
              </div>
              <div
                style={{
                  fontSize: '0.75rem',
                  color: 'var(--text-secondary)',
                  borderTop: '1px solid var(--border-color)',
                  paddingTop: '8px',
                  display: 'flex',
                  alignItems: 'center',
                  gap: '6px',
                }}
              >
                <Clock size={14} strokeWidth={1.75} aria-hidden="true" />
                <span><strong>Response Time:</strong> Within 24-48 business hours (Mon - Fri)</span>
              </div>
            </div>

            {submittedSuccess ? (
              <div
                style={{
                  backgroundColor: 'var(--bg-tertiary)',
                  border: '1px solid var(--border-strong)',
                  borderRadius: '12px',
                  padding: '20px',
                  textAlign: 'center',
                  color: 'var(--text-primary)',
                  display: 'flex',
                  flexDirection: 'column',
                  alignItems: 'center',
                  gap: '8px',
                }}
              >
                <CheckCircle2 size={32} strokeWidth={1.75} style={{ color: '#15803d' }} aria-hidden="true" />
                <h3 style={{ fontSize: '1rem', fontWeight: 600 }}>Message Received</h3>
                <p style={{ fontSize: '0.8125rem', color: 'var(--text-secondary)', lineHeight: 1.5 }}>
                  Thank you for reaching out. Our support team has logged your inquiry and will follow up with you at <strong>{email}</strong>.
                </p>
                <button
                  type="button"
                  className="btn-cta-primary"
                  style={{ marginTop: '6px' }}
                  onClick={() => setSubmittedSuccess(false)}
                >
                  Send Another Message
                </button>
              </div>
            ) : (
              <form onSubmit={handleFeedbackSubmit} style={{ display: 'flex', flexDirection: 'column', gap: '12px' }}>
                <h3 style={{ fontSize: '0.875rem', fontWeight: 600, color: 'var(--text-primary)' }}>
                  Submit a Support Ticket
                </h3>

                <div>
                  <label htmlFor={emailInputId} style={{ fontSize: '0.75rem', color: 'var(--text-secondary)', display: 'block', marginBottom: '4px', fontWeight: 500 }}>
                    Your Contact Email <span style={{ color: '#DC2626' }}>*</span>
                  </label>
                  <input
                    id={emailInputId}
                    type="email"
                    autoComplete="email"
                    className="sn-input"
                    value={email}
                    onChange={(e) => setEmail(e.target.value)}
                    placeholder="name@example.com"
                    required
                  />
                </div>

                <div>
                  <label htmlFor={subjectInputId} style={{ fontSize: '0.75rem', color: 'var(--text-secondary)', display: 'block', marginBottom: '4px', fontWeight: 500 }}>
                    Inquiry Category
                  </label>
                  <select
                    id={subjectInputId}
                    className="sn-input"
                    value={subject}
                    onChange={(e) => setSubject(e.target.value)}
                  >
                    <option value="Payment / Recharge Issue">Payment / Recharge Issue</option>
                    <option value="Account & Login Issue">Account &amp; Login Issue</option>
                    <option value="Book / Content Feedback">Book / Content Feedback</option>
                    <option value="Bug Report & Suggestions">Bug Report &amp; Suggestions</option>
                    <option value="Other Inquiries">Other Inquiries</option>
                  </select>
                </div>

                <div>
                  <label htmlFor={contentInputId} style={{ fontSize: '0.75rem', color: 'var(--text-secondary)', display: 'block', marginBottom: '4px', fontWeight: 500 }}>
                    Message Content <span style={{ color: '#DC2626' }}>*</span>
                  </label>
                  <textarea
                    id={contentInputId}
                    className="sn-input"
                    value={content}
                    onChange={(e) => setContent(e.target.value)}
                    placeholder="Describe your issue or feedback (e.g. Order ID, book title, details)..."
                    required
                    rows={4}
                    style={{ resize: 'vertical', minHeight: '96px' }}
                  />
                </div>

                <button
                  type="submit"
                  disabled={isSubmitting}
                  className="btn-cta-primary"
                  style={{ width: '100%', marginTop: '4px' }}
                >
                  {isSubmitting ? 'Submitting...' : 'Submit Message'}
                </button>
              </form>
            )}
          </div>
        )}
      </div>

      {/* Footer Close Button */}
      <div
        style={{
          marginTop: '14px',
          paddingTop: '12px',
          borderTop: '1px solid var(--border-color)',
          textAlign: 'right',
          flexShrink: 0,
        }}
      >
        <button
          type="button"
          className="btn-cta-secondary"
          style={{ minHeight: '38px', padding: '6px 16px' }}
          onClick={onClose}
        >
          Close
        </button>
      </div>
    </dialog>
  );
};
