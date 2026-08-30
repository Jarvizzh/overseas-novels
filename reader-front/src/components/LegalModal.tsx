import React, { useState } from 'react';
import { api } from '../utils/api';
import { useToast } from '../context/ToastContext';

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

  // Feedback form state
  const [email, setEmail] = useState(defaultEmail);
  const [subject, setSubject] = useState('Payment / Recharge Issue');
  const [content, setContent] = useState('');
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [submittedSuccess, setSubmittedSuccess] = useState(false);

  // Sync defaultEmail when modal opens
  React.useEffect(() => {
    if (isOpen && defaultEmail && !email) {
      setEmail(defaultEmail);
    }
    if (isOpen) {
      setSubmittedSuccess(false);
    }
  }, [isOpen, defaultEmail]);

  if (!isOpen) return null;

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
    } catch (err: any) {
      showToast(err.message || 'Failed to submit message. Please try again.', 'error');
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

  return (
    <div
      style={{
        position: 'fixed',
        inset: 0,
        backgroundColor: 'rgba(0, 0, 0, 0.7)',
        backdropFilter: 'blur(5px)',
        zIndex: 9999,
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        padding: '16px',
      }}
    >
      <div
        className="animate-fade-in"
        style={{
          backgroundColor: 'var(--bg-secondary)',
          borderRadius: '16px',
          padding: '24px 20px 20px',
          width: '100%',
          maxWidth: '440px',
          maxHeight: '85vh',
          display: 'flex',
          flexDirection: 'column',
          boxShadow: '0 25px 50px -12px rgba(0,0,0,0.5)',
          border: '1px solid var(--border-color)',
          position: 'relative',
        }}
      >
        {/* Header */}
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '16px', borderBottom: '1px solid var(--border-color)', paddingBottom: '12px' }}>
          <h3 style={{ fontSize: '17px', fontWeight: 800, color: 'var(--text-primary)', margin: 0 }}>
            {renderTitle()}
          </h3>
          <button
            onClick={onClose}
            style={{
              background: 'none',
              border: 'none',
              color: 'var(--text-secondary)',
              fontSize: '22px',
              cursor: 'pointer',
              lineHeight: 1,
              padding: '4px',
            }}
            aria-label="Close"
          >
            ✕
          </button>
        </div>

        {/* Scrollable Body Content */}
        <div
          className="scroll-container-no-pad"
          style={{
            flex: 1,
            overflowY: 'auto',
            paddingRight: '6px',
            fontSize: '12.5px',
            color: 'var(--text-secondary)',
            lineHeight: 1.7,
          }}
        >
          {type === 'terms' && (
            <div style={{ display: 'flex', flexDirection: 'column', gap: '12px' }}>
              <p><strong>Last Updated:</strong> August 2026</p>
              <p>
                Welcome to <strong>starsfic.com</strong> (&quot;Starsfic&quot;, &quot;we&quot;, &quot;us&quot;, or &quot;our&quot;). By accessing or using our online novel reading platform, websites, and associated services, you agree to be bound by these Terms of Service.
              </p>
              
              <h4 style={{ color: 'var(--text-primary)', fontWeight: 700, marginTop: '8px' }}>1. User Accounts</h4>
              <p>
                You may access content as a guest or create a registered account. You are responsible for safeguarding your login credentials and for all activities that occur under your account.
              </p>

              <h4 style={{ color: 'var(--text-primary)', fontWeight: 700, marginTop: '8px' }}>2. Virtual Currency and Purchases</h4>
              <p>
                Coins and VIP memberships purchased on starsfic.com are digital virtual tokens intended exclusively for unlocking reading content on this platform. Virtual currency has no cash value outside starsfic.com and cannot be transferred, exchanged, or redeemed for fiat currency.
              </p>

              <h4 style={{ color: 'var(--text-primary)', fontWeight: 700, marginTop: '8px' }}>3. Intellectual Property Rights</h4>
              <p>
                All literary works, book covers, illustrations, trademarks, software, and other materials available on starsfic.com are protected by copyright and intellectual property laws. Unauthorized reproduction, distribution, scraping, or commercial exploitation is strictly prohibited.
              </p>

              <h4 style={{ color: 'var(--text-primary)', fontWeight: 700, marginTop: '8px' }}>4. Code of Conduct</h4>
              <p>
                Users agree not to engage in unauthorized scraping, reverse engineering, fraudulent payments, chargeback abuse, or posting malicious, infringing, or harassing comments.
              </p>

              <h4 style={{ color: 'var(--text-primary)', fontWeight: 700, marginTop: '8px' }}>5. Contact & Inquiries</h4>
              <p>
                For questions regarding these terms, please contact us at: <a href="mailto:support@startsfic.com" style={{ color: 'var(--accent-color)', fontWeight: 600 }}>support@startsfic.com</a>.
              </p>
            </div>
          )}

          {type === 'privacy' && (
            <div style={{ display: 'flex', flexDirection: 'column', gap: '12px' }}>
              <p><strong>Last Updated:</strong> August 2026</p>
              <p>
                Your privacy is essential to <strong>starsfic.com</strong>. This Privacy Policy explains how we collect, use, and protect your information when you visit our website or use our reading services.
              </p>

              <h4 style={{ color: 'var(--text-primary)', fontWeight: 700, marginTop: '8px' }}>1. Information We Collect</h4>
              <p>
                - <strong>Account Information:</strong> Email address, nickname, and account preferences when registering.<br />
                - <strong>Usage & Reading Data:</strong> Reading history, unlocked chapters, bookmarks, and device identifiers to synchronize reading progress across sessions.<br />
                - <strong>Transaction Data:</strong> Payment order ID, purchased coin amounts, timestamp, and payment status (we do not store your raw credit card or banking passwords).
              </p>

              <h4 style={{ color: 'var(--text-primary)', fontWeight: 700, marginTop: '8px' }}>2. How We Use Information</h4>
              <p>
                We use your data to provide reader functionalities, fulfill digital purchases, deliver customer support, detect fraud, and optimize user experience.
              </p>

              <h4 style={{ color: 'var(--text-primary)', fontWeight: 700, marginTop: '8px' }}>3. Third-Party Services & Payment Gateways</h4>
              <p>
                Payments are securely processed via certified payment partners (including PayPal). Data transmitted during checkout is encrypted using industry-standard SSL protocols.
              </p>

              <h4 style={{ color: 'var(--text-primary)', fontWeight: 700, marginTop: '8px' }}>4. User Rights & Data Deletion</h4>
              <p>
                You may request access to, correction, or deletion of your account and personal data at any time by contacting our privacy team at <a href="mailto:support@startsfic.com" style={{ color: 'var(--accent-color)', fontWeight: 600 }}>support@startsfic.com</a>.
              </p>
            </div>
          )}

          {type === 'refund' && (
            <div style={{ display: 'flex', flexDirection: 'column', gap: '12px' }}>
              <p><strong>Last Updated:</strong> August 2026</p>
              <p>
                Thank you for purchasing reading coins and memberships on <strong>starsfic.com</strong>. Please read our refund policy carefully before making any purchases.
              </p>

              <h4 style={{ color: 'var(--text-primary)', fontWeight: 700, marginTop: '8px' }}>1. Nature of Digital Goods</h4>
              <p>
                Reading coins, chapter unlocks, and VIP passes on starsfic.com are <strong>intangible virtual digital items</strong> that are credited to your account immediately upon payment confirmation. As digital goods are consumed upon unlocking reading content, purchases are generally <strong>non-refundable</strong> once delivered.
              </p>

              <h4 style={{ color: 'var(--text-primary)', fontWeight: 700, marginTop: '8px' }}>2. Exceptions and Refund Eligibility</h4>
              <p>
                We provide full resolution or refunds under the following specific circumstances:<br />
                - <strong>Duplicate Charging:</strong> If you were accidentally billed multiple times for the same transaction due to a network glitch.<br />
                - <strong>Technical Failure / Non-Delivery:</strong> If a payment succeeded but coins were not credited to your account and our system cannot manually fulfill the credit within 24 hours.<br />
                - <strong>Unauthorized Transaction:</strong> Verified unauthorized or fraudulent use of your payment method.
              </p>

              <h4 style={{ color: 'var(--text-primary)', fontWeight: 700, marginTop: '8px' }}>3. How to Request Assistance</h4>
              <p>
                If you encounter any payment discrepancy, please submit a message via our <strong>Contact Us</strong> tab or email our support desk directly at <a href="mailto:support@startsfic.com" style={{ color: 'var(--accent-color)', fontWeight: 600 }}>support@startsfic.com</a> with:
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
              {/* Contact Info Header Card */}
              <div
                style={{
                  backgroundColor: 'var(--bg-primary)',
                  borderRadius: '12px',
                  padding: '14px',
                  border: '1px solid var(--border-color)',
                  display: 'flex',
                  flexDirection: 'column',
                  gap: '6px',
                }}
              >
                <div style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
                  <span style={{ fontSize: '18px' }}>✉️</span>
                  <div>
                    <span style={{ fontSize: '11px', color: 'var(--text-tertiary)', display: 'block' }}>Official Support Email</span>
                    <a href="mailto:support@startsfic.com" style={{ fontSize: '13px', fontWeight: 700, color: 'var(--accent-color)', textDecoration: 'none' }}>
                      support@startsfic.com
                    </a>
                  </div>
                </div>
                <div style={{ fontSize: '11px', color: 'var(--text-secondary)', marginTop: '4px', borderTop: '1px solid var(--border-color)', paddingTop: '6px' }}>
                  ⏱️ <strong>Response Time:</strong> Within 24-48 business hours (Mon - Fri)
                </div>
              </div>

              {submittedSuccess ? (
                <div
                  style={{
                    backgroundColor: '#dcfce7',
                    border: '1px solid #86efac',
                    borderRadius: '12px',
                    padding: '20px',
                    textAlign: 'center',
                    color: '#15803d',
                  }}
                >
                  <span style={{ fontSize: '32px', display: 'block', marginBottom: '8px' }}>✅</span>
                  <h4 style={{ fontSize: '15px', fontWeight: 800, marginBottom: '6px' }}>Message Received!</h4>
                  <p style={{ fontSize: '12px', lineHeight: 1.5 }}>
                    Thank you for reaching out. Our support team has logged your inquiry and will follow up with you at <strong>{email}</strong>.
                  </p>
                  <button
                    onClick={() => setSubmittedSuccess(false)}
                    style={{
                      marginTop: '14px',
                      padding: '6px 16px',
                      fontSize: '12px',
                      fontWeight: 700,
                      backgroundColor: '#15803d',
                      color: 'white',
                      border: 'none',
                      borderRadius: '8px',
                      cursor: 'pointer',
                    }}
                  >
                    Send Another Message
                  </button>
                </div>
              ) : (
                /* Feedback Message Form */
                <form onSubmit={handleFeedbackSubmit} style={{ display: 'flex', flexDirection: 'column', gap: '12px' }}>
                  <h4 style={{ fontSize: '13px', fontWeight: 700, color: 'var(--text-primary)', margin: '4px 0 0' }}>
                    Leave a Message / Submit a Ticket
                  </h4>

                  <div>
                    <label style={{ fontSize: '11.5px', color: 'var(--text-secondary)', display: 'block', marginBottom: '4px', fontWeight: 600 }}>
                      Your Contact Email <span style={{ color: '#ef4444' }}>*</span>
                    </label>
                    <input
                      type="email"
                      value={email}
                      onChange={(e) => setEmail(e.target.value)}
                      placeholder="e.g. yourname@gmail.com"
                      required
                      style={{
                        width: '100%',
                        padding: '9px 12px',
                        borderRadius: '8px',
                        border: '1px solid var(--border-color)',
                        backgroundColor: 'var(--bg-primary)',
                        color: 'var(--text-primary)',
                        fontSize: '13px',
                      }}
                    />
                  </div>

                  <div>
                    <label style={{ fontSize: '11.5px', color: 'var(--text-secondary)', display: 'block', marginBottom: '4px', fontWeight: 600 }}>
                      Inquiry Category
                    </label>
                    <select
                      value={subject}
                      onChange={(e) => setSubject(e.target.value)}
                      style={{
                        width: '100%',
                        padding: '9px 12px',
                        borderRadius: '8px',
                        border: '1px solid var(--border-color)',
                        backgroundColor: 'var(--bg-primary)',
                        color: 'var(--text-primary)',
                        fontSize: '13px',
                      }}
                    >
                      <option value="Payment / Recharge Issue">Payment / Recharge Issue</option>
                      <option value="Account & Login Issue">Account & Login Issue</option>
                      <option value="Book / Content Feedback">Book / Content Feedback</option>
                      <option value="Bug Report & Suggestions">Bug Report & Suggestions</option>
                      <option value="Other Inquiries">Other Inquiries</option>
                    </select>
                  </div>

                  <div>
                    <label style={{ fontSize: '11.5px', color: 'var(--text-secondary)', display: 'block', marginBottom: '4px', fontWeight: 600 }}>
                      Message Content <span style={{ color: '#ef4444' }}>*</span>
                    </label>
                    <textarea
                      value={content}
                      onChange={(e) => setContent(e.target.value)}
                      placeholder="Describe your issue or feedback in detail (e.g. Order ID, book title, problem description)..."
                      required
                      rows={4}
                      style={{
                        width: '100%',
                        padding: '10px 12px',
                        borderRadius: '8px',
                        border: '1px solid var(--border-color)',
                        backgroundColor: 'var(--bg-primary)',
                        color: 'var(--text-primary)',
                        fontSize: '13px',
                        resize: 'vertical',
                      }}
                    />
                  </div>

                  <button
                    type="submit"
                    disabled={isSubmitting}
                    className="btn-cta-primary"
                    style={{
                      width: '100%',
                      padding: '11px 0',
                      fontSize: '13.5px',
                      fontWeight: 700,
                      borderRadius: '10px',
                      marginTop: '4px',
                      cursor: isSubmitting ? 'not-allowed' : 'pointer',
                    }}
                  >
                    {isSubmitting ? 'Submitting...' : 'Submit Message'}
                  </button>
                </form>
              )}
            </div>
          )}
        </div>

        {/* Footer Close Button */}
        <div style={{ marginTop: '14px', paddingTop: '10px', borderTop: '1px solid var(--border-color)', textAlign: 'right' }}>
          <button
            onClick={onClose}
            style={{
              padding: '7px 18px',
              fontSize: '12px',
              fontWeight: 700,
              backgroundColor: 'var(--bg-tertiary)',
              color: 'var(--text-primary)',
              border: '1px solid var(--border-color)',
              borderRadius: '8px',
              cursor: 'pointer',
            }}
          >
            Close
          </button>
        </div>
      </div>
    </div>
  );
};
