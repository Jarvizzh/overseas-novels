import React, { useState } from 'react';
import { useToast } from '../context/ToastContext';
import { GoldCoin } from '../components/GoldCoin';
import { api } from '../utils/api';
import { LegalModal } from '../components/LegalModal';
import type { LegalModalType } from '../components/LegalModal';

interface RechargeProps {
  userCoins: number;
  onAddCoins: (amount: number, reason: string) => void;
  onBack: () => void;
}

export const Recharge: React.FC<RechargeProps> = ({
  userCoins,
  onAddCoins,
  onBack,
}) => {
  const { showToast } = useToast();
  const [selectedPack, setSelectedPack] = useState<number | null>(null);
  const [isProcessing, setIsProcessing] = useState(false);
  const [paymentStep, setPaymentStep] = useState(0); // 0: Idle, 1: connecting, 2: processing
  const [showSuccess, setShowSuccess] = useState(false);
  const [successAmount, setSuccessAmount] = useState(0);
  const [activeLegalModal, setActiveLegalModal] = useState<LegalModalType | null>(null);

  // Recharge slot configurations from active default template
  const [slots, setSlots] = useState<Array<{
    id: number;
    template_id: number;
    slot_index: number;
    type: 'single' | 'vip' | 'whole_book';
    coins: number;
    bonus: number;
    vip_duration: string;
    vip_name: string;
    vip_desc: string;
    price: string;
    price_cents: number;
  }>>([]);
  const [loadingTemplates, setLoadingTemplates] = useState(true);

  React.useEffect(() => {
    const fetchTemplates = async () => {
      try {
        const data = await api.getRechargeTemplates();
        const slotList = data?.slots || [];
        setSlots(slotList);
        if (slotList.length > 0) {
          setSelectedPack(slotList[0].id);
        }
      } catch (err) {
        console.error("Failed to load templates:", err);
      } finally {
        setLoadingTemplates(false);
      }
    };
    fetchTemplates();
  }, []);

  // Handle PayPal redirect return
  React.useEffect(() => {
    const handlePayPalReturn = async () => {
      const urlParams = new URLSearchParams(window.location.search);
      const paymentStatus = urlParams.get('payment');
      const token = urlParams.get('token'); // PayPal Order ID is passed in token

      if (paymentStatus === 'cancel') {
        showToast("PayPal payment was cancelled.", "info");
        sessionStorage.removeItem('pending_paypal_recharge');
        window.history.replaceState({}, '', window.location.pathname);
        return;
      }

      if (token) {
        setIsProcessing(true);
        setPaymentStep(2);

        let pendingInfo: any = null;
        try {
          const saved = sessionStorage.getItem('pending_paypal_recharge');
          if (saved) {
            pendingInfo = JSON.parse(saved);
          }
        } catch (_) {}

        const expectedCoins = pendingInfo?.coins || 1000;

        try {
          await api.capturePayPalPayment(token, expectedCoins);
          setIsProcessing(false);
          setPaymentStep(0);

          let reason = 'PayPal Recharge Topup';
          if (pendingInfo) {
            if (pendingInfo.type === 'single') {
              reason = `PayPal Recharge: ${pendingInfo.price} pack`;
            } else if (pendingInfo.type === 'vip') {
              reason = `PayPal Subscription: ${pendingInfo.name}`;
            } else {
              reason = `PayPal Purchase: Whole Book (${pendingInfo.name})`;
            }
          }

          onAddCoins(expectedCoins, reason);
          setSuccessAmount(expectedCoins);
          setShowSuccess(true);
          sessionStorage.removeItem('pending_paypal_recharge');
        } catch (err: any) {
          setIsProcessing(false);
          setPaymentStep(0);
          showToast(err.message || "Failed to complete PayPal payment capture", "error");
        } finally {
          window.history.replaceState({}, '', window.location.pathname);
        }
      }
    };

    handlePayPalReturn();
  }, []);

  const coinPacks = slots
    .filter(s => s.type === 'single')
    .map(s => ({
      id: s.id,
      coins: s.coins,
      bonus: s.bonus,
      price: s.price,
      priceCents: s.price_cents,
      total: s.coins + s.bonus,
      type: s.type
    }));

  const vipPacks = slots
    .filter(s => s.type === 'vip')
    .map(s => ({
      id: s.id,
      name: s.vip_name,
      price: s.price,
      priceCents: s.price_cents,
      desc: s.vip_desc,
      total: s.coins + s.bonus,
      type: s.type
    }));

  const wholeBookPacks = slots
    .filter(s => s.type === 'whole_book')
    .map(s => ({
      id: s.id,
      name: s.vip_name,
      price: s.price,
      priceCents: s.price_cents,
      desc: s.vip_desc,
      total: s.coins + s.bonus,
      type: s.type
    }));

  const handlePurchase = async () => {
    if (selectedPack === null) return;

    const tSlot = slots.find(p => p.id === selectedPack);
    if (!tSlot) return;

    const totalCoins = tSlot.coins + tSlot.bonus;
    setIsProcessing(true);
    setPaymentStep(1);

    try {
      // Save pending recharge info in sessionStorage
      sessionStorage.setItem('pending_paypal_recharge', JSON.stringify({
        slotId: tSlot.id,
        coins: totalCoins,
        type: tSlot.type,
        name: tSlot.vip_name || tSlot.price,
        price: tSlot.price,
        time: Date.now(),
      }));

      // Build return & cancel URLs
      const currentUrl = new URL(window.location.href);
      currentUrl.searchParams.delete('token');
      currentUrl.searchParams.delete('PayerID');
      currentUrl.searchParams.delete('payment');

      const returnUrl = `${currentUrl.origin}${currentUrl.pathname}?payment=success`;
      const cancelUrl = `${currentUrl.origin}${currentUrl.pathname}?payment=cancel`;

      const resp = await api.createPayPalOrder(tSlot.price_cents, totalCoins, returnUrl, cancelUrl);

      if (resp?.approve_url) {
        window.location.href = resp.approve_url;
      } else {
        throw new Error("No PayPal checkout URL received");
      }
    } catch (err: any) {
      setIsProcessing(false);
      setPaymentStep(0);
      showToast(err.message || "Failed to initiate PayPal checkout. Please try again.", "error");
    }
  };

  const selectedSlot = slots.find(p => p.id === selectedPack);

  if (loadingTemplates) {
    return (
      <div style={{
        display: 'flex', minHeight: '100vh', alignItems: 'center', justifyContent: 'center',
        backgroundColor: 'var(--bg-primary)', color: 'var(--text-secondary)', fontSize: '14px'
      }}>
        Loading recharge packages...
      </div>
    );
  }

  return (
    <div className="page-container-full animate-fade-in" style={{ backgroundColor: 'var(--bg-primary)' }}>
      {/* Top Header */}
      <header className="app-header glass-panel">
        <div style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
          <button className="header-btn" onClick={onBack} aria-label="Go back">
            <svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" strokeWidth={2.5} stroke="currentColor" style={{ width: '20px', height: '20px' }}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M15.75 19.5L8.25 12l7.5-7.5" />
            </svg>
          </button>
          <h1 className="header-title">Top Up Coins</h1>
        </div>
      </header>

      {/* Main Content Area */}
      <div className="scroll-container-no-pad" style={{ flex: 1, minHeight: 0, padding: '16px 16px 60px' }}>
        {/* Balance Display Card */}
        <div style={{
          backgroundColor: 'var(--bg-secondary)',
          borderRadius: '16px',
          padding: '20px',
          border: '1px solid var(--border-color)',
          boxShadow: 'var(--card-shadow)',
          marginBottom: '20px',
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center'
        }}>
          <div>
            <span style={{ fontSize: '12px', color: 'var(--text-tertiary)' }}>Current Balance</span>
            <div style={{ display: 'flex', alignItems: 'center', gap: '8px', marginTop: '4px' }}>
              <GoldCoin size={24} />
              <span style={{ fontSize: '28px', fontWeight: 800, color: 'var(--text-primary)', fontFamily: 'monospace' }}>
                {userCoins}
              </span>
              <span style={{ fontSize: '14px', color: 'var(--text-secondary)', fontWeight: 600 }}>Coins</span>
            </div>
          </div>
          <span style={{ fontSize: '32px', opacity: 0.8 }}>👛</span>
        </div>

        {/* Top Up Coins Grid */}
        {coinPacks.length > 0 && (
          <>
            <h3 className="hot-tags-title" style={{ fontSize: '13px', marginBottom: '10px' }}>Select Coin Pack</h3>
            <div style={{
              display: 'grid',
              gridTemplateColumns: 'repeat(2, 1fr)',
              gap: '12px',
              marginBottom: '20px'
            }}>
          {coinPacks.map((pack) => {
            const isSelected = selectedPack === pack.id;
            return (
              <div
                key={pack.id}
                onClick={() => setSelectedPack(pack.id)}
                style={{
                  backgroundColor: 'var(--bg-secondary)',
                  borderRadius: '12px',
                  padding: '16px 12px',
                  border: isSelected ? '2px solid var(--accent-color)' : '1px solid var(--border-color)',
                  boxShadow: isSelected ? '0 4px 15px rgba(79, 70, 229, 0.15)' : 'var(--card-shadow)',
                  cursor: 'pointer',
                  display: 'flex',
                  flexDirection: 'column',
                  alignItems: 'center',
                  position: 'relative',
                  transition: 'transform 0.15s ease'
                }}
              >
                {/* Bonus Badge */}
                <div style={{
                  position: 'absolute',
                  top: '-8px',
                  left: '10px',
                  backgroundColor: '#ef4444',
                  color: 'white',
                  fontSize: '9px',
                  fontWeight: 700,
                  padding: '2px 6px',
                  borderRadius: '4px',
                  boxShadow: '0 2px 4px rgba(239,68,68,0.2)'
                }}>
                  +{pack.bonus} Bonus
                </div>

                <div style={{ display: 'flex', alignItems: 'center', gap: '4px', marginTop: '6px', marginBottom: '4px' }}>
                  <GoldCoin size={16} />
                  <span style={{ fontSize: '20px', fontWeight: 800, color: 'var(--text-primary)' }}>{pack.coins}</span>
                </div>
                <span style={{ fontSize: '11px', color: 'var(--text-tertiary)', marginBottom: '12px' }}>
                  Total: {pack.total} Coins
                </span>
                <span style={{
                  fontSize: '13px',
                  fontWeight: 700,
                  backgroundColor: isSelected ? 'var(--accent-color)' : 'var(--bg-tertiary)',
                  color: isSelected ? 'white' : 'var(--text-secondary)',
                  width: '100%',
                  textAlign: 'center',
                  padding: '6px 0',
                  borderRadius: '8px',
                  transition: 'var(--transition-fast)'
                }}>
                  {pack.price}
                </span>
              </div>
            );
          })}
            </div>
          </>
        )}

        {/* VIP Membership Grid */}
        {vipPacks.length > 0 && (
          <>
            <h3 className="hot-tags-title" style={{ fontSize: '13px', marginBottom: '10px' }}>VIP Membership</h3>
            <div style={{ display: 'flex', flexDirection: 'column', gap: '12px', marginBottom: '24px' }}>
          {vipPacks.map((pack) => {
            const isSelected = selectedPack === pack.id;
            return (
              <div
                key={pack.id}
                onClick={() => setSelectedPack(pack.id)}
                style={{
                  display: 'flex',
                  justifyContent: 'space-between',
                  alignItems: 'center',
                  backgroundColor: 'var(--bg-secondary)',
                  borderRadius: '12px',
                  padding: '16px',
                  border: isSelected ? '2px solid var(--accent-color)' : '1px solid var(--border-color)',
                  boxShadow: isSelected ? '0 4px 15px rgba(79, 70, 229, 0.15)' : 'var(--card-shadow)',
                  cursor: 'pointer',
                  position: 'relative'
                }}
              >
                <div style={{ display: 'flex', gap: '12px', alignItems: 'center' }}>
                  <span style={{ fontSize: '28px' }}>👑</span>
                  <div>
                    <h4 style={{ fontSize: '14px', fontWeight: 700 }}>{pack.name}</h4>
                    <p style={{ fontSize: '11px', color: 'var(--text-tertiary)', marginTop: '2px' }}>{pack.desc}</p>
                  </div>
                </div>
                <span style={{
                  fontSize: '13px',
                  fontWeight: 700,
                  backgroundColor: isSelected ? 'var(--accent-color)' : 'var(--bg-tertiary)',
                  color: isSelected ? 'white' : 'var(--text-secondary)',
                  padding: '6px 12px',
                  borderRadius: '8px',
                  width: '76px',
                  textAlign: 'center'
                }}>
                  {pack.price}
                </span>
              </div>
            );
          })}
            </div>
          </>
        )}

        {/* Whole Book Purchase Grid */}
        {wholeBookPacks.length > 0 && (
          <>
            <h3 className="hot-tags-title" style={{ fontSize: '13px', marginBottom: '10px' }}>Whole Book Purchase</h3>
            <div style={{ display: 'flex', flexDirection: 'column', gap: '12px', marginBottom: '24px' }}>
              {wholeBookPacks.map((pack) => {
                const isSelected = selectedPack === pack.id;
                return (
                  <div
                    key={pack.id}
                    onClick={() => setSelectedPack(pack.id)}
                    style={{
                      display: 'flex',
                      justifyContent: 'space-between',
                      alignItems: 'center',
                      backgroundColor: 'var(--bg-secondary)',
                      borderRadius: '12px',
                      padding: '16px',
                      border: isSelected ? '2px solid var(--accent-color)' : '1px solid var(--border-color)',
                      boxShadow: isSelected ? '0 4px 15px rgba(79, 70, 229, 0.15)' : 'var(--card-shadow)',
                      cursor: 'pointer',
                      position: 'relative'
                    }}
                  >
                    <div style={{ display: 'flex', gap: '12px', alignItems: 'center' }}>
                      <span style={{ fontSize: '28px' }}>📖</span>
                      <div>
                        <h4 style={{ fontSize: '14px', fontWeight: 700 }}>{pack.name}</h4>
                        <p style={{ fontSize: '11px', color: 'var(--text-tertiary)', marginTop: '2px' }}>{pack.desc}</p>
                      </div>
                    </div>
                    <span style={{
                      fontSize: '13px',
                      fontWeight: 700,
                      backgroundColor: isSelected ? 'var(--accent-color)' : 'var(--bg-tertiary)',
                      color: isSelected ? 'white' : 'var(--text-secondary)',
                      padding: '6px 12px',
                      borderRadius: '8px',
                      width: '76px',
                      textAlign: 'center'
                    }}>
                      {pack.price}
                    </span>
                  </div>
                );
              })}
            </div>
          </>
        )}

        {/* Direct PayPal Checkout Card */}
        {selectedSlot && (
          <div style={{
            backgroundColor: 'var(--bg-secondary)',
            borderRadius: '16px',
            padding: '18px',
            border: '1px solid var(--border-color)',
            boxShadow: 'var(--card-shadow)',
            marginBottom: '24px'
          }}>
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '16px' }}>
              <div>
                <span style={{ fontSize: '11px', color: 'var(--text-tertiary)', textTransform: 'uppercase', letterSpacing: '0.5px', fontWeight: 600 }}>Selected Item</span>
                <span style={{ fontSize: '15px', fontWeight: 800, color: 'var(--text-primary)', display: 'block', marginTop: '2px' }}>
                  {selectedSlot.type === 'single'
                    ? `${selectedSlot.coins} Coins (+${selectedSlot.bonus} Bonus)`
                    : selectedSlot.vip_name}
                </span>
              </div>
              <div style={{ textAlign: 'right' }}>
                <span style={{ fontSize: '11px', color: 'var(--text-tertiary)', textTransform: 'uppercase', letterSpacing: '0.5px', fontWeight: 600 }}>Amount Due</span>
                <span style={{ fontSize: '22px', fontWeight: 900, color: 'var(--accent-color)', display: 'block', marginTop: '2px' }}>
                  {selectedSlot.price}
                </span>
              </div>
            </div>

            {/* Official Branded PayPal Checkout Button */}
            <button
              onClick={handlePurchase}
              disabled={isProcessing}
              style={{
                width: '100%',
                padding: '14px 0',
                backgroundColor: '#ffc439',
                color: '#003087',
                border: 'none',
                borderRadius: '24px',
                fontSize: '15px',
                fontWeight: 800,
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                gap: '8px',
                boxShadow: '0 4px 14px rgba(255, 196, 57, 0.4)',
                cursor: isProcessing ? 'not-allowed' : 'pointer',
                transition: 'all 0.2s ease',
                fontFamily: 'system-ui, -apple-system, sans-serif'
              }}
            >
              <svg width="22" height="22" viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg" style={{ flexShrink: 0 }}>
                <path d="M7.076 21.337H2.47a.64.64 0 0 1-.633-.74L4.931 2.378A.853.853 0 0 1 5.774 1.7h7.24c3.483 0 5.69 1.642 5.093 5.372-.44 2.75-2.228 4.316-4.945 4.316h-2.14l-1.07 6.776-.026.173a.853.853 0 0 1-.85.8z" fill="#003087"/>
                <path d="M8.766 18.066l1.24-7.852h2.64c2.717 0 4.505-1.566 4.945-4.316.597-3.73-1.61-5.372-5.093-5.372H5.258a.853.853 0 0 0-.843.678L1.321 19.723a.64.64 0 0 0 .633.74h4.606l1.07-6.776.026-.173a.853.853 0 0 1 .85-.8z" fill="#0079C1"/>
                <path d="M9.13 14.77l1.07-6.776h2.14c2.717 0 4.505-1.566 4.945-4.316.14-.876.105-1.614-.078-2.217C16.89 2.532 15.65 2 13.904 2H8.354a.853.853 0 0 0-.843.678L4.417 21.2a.64.64 0 0 0 .633.74h4.606l1.07-6.776.026-.173a.853.853 0 0 1 .85-.8z" fill="#00457C"/>
              </svg>
              <span>{isProcessing ? 'Connecting to PayPal...' : `PayPal · Pay ${selectedSlot.price}`}</span>
            </button>

            <div style={{
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              gap: '12px',
              marginTop: '12px',
              fontSize: '11px',
              color: 'var(--text-tertiary)'
            }}>
              <span>🔒 256-Bit SSL Encrypted</span>
              <span>•</span>
              <span>⚡ Instant Coins Delivery</span>
            </div>
          </div>
        )}

        {/* Tips Box */}
        <div style={{
          backgroundColor: 'var(--bg-tertiary)',
          borderRadius: '12px',
          padding: '16px',
          border: '1px solid var(--border-color)',
          fontSize: '11px',
          color: 'var(--text-secondary)',
          lineHeight: '1.6'
        }}>
          <h4 style={{ fontWeight: 700, marginBottom: '6px', color: 'var(--text-primary)', fontSize: '12px' }}>Tips:</h4>
          <p style={{ marginBottom: '4px' }}>1. Coins are virtual items and cannot be refunded once consumed. They can only be used within this app.</p>
          <p style={{ marginBottom: '4px' }}>2. Reward coins are time-sensitive. Please use them within the 7-day validity period, as they will be deducted first when unlocking content.</p>
          <p>3. For billing assistance or issues, please reach out via <span style={{ color: 'var(--accent-color)', fontWeight: 600, cursor: 'pointer', textDecoration: 'underline' }} onClick={() => setActiveLegalModal('contact')}>Customer Support</span>.</p>
        </div>

        {/* Compliance & Legal Footer Links */}
        <div style={{
          display: 'flex',
          flexWrap: 'wrap',
          justifyContent: 'center',
          alignItems: 'center',
          gap: '8px 12px',
          marginTop: '20px',
          padding: '0 8px',
          fontSize: '11px',
          color: 'var(--text-tertiary)'
        }}>
          <span style={{ cursor: 'pointer', textDecoration: 'underline' }} onClick={() => setActiveLegalModal('terms')}>Terms of Service</span>
          <span>•</span>
          <span style={{ cursor: 'pointer', textDecoration: 'underline' }} onClick={() => setActiveLegalModal('privacy')}>Privacy Policy</span>
          <span>•</span>
          <span style={{ cursor: 'pointer', textDecoration: 'underline' }} onClick={() => setActiveLegalModal('refund')}>Refund Policy</span>
          <span>•</span>
          <span style={{ cursor: 'pointer', textDecoration: 'underline' }} onClick={() => setActiveLegalModal('contact')}>Contact Us</span>
        </div>
      </div>

      {/* Payment Processing Loading Screen */}
      {isProcessing && (
        <div style={{
          position: 'fixed',
          inset: 0,
          backgroundColor: 'rgba(0,0,0,0.7)',
          backdropFilter: 'blur(5px)',
          zIndex: 9999,
          display: 'flex',
          flexDirection: 'column',
          alignItems: 'center',
          justifyContent: 'center',
          color: 'white'
        }}>
          <div style={{
            display: 'inline-block',
            width: '40px',
            height: '40px',
            border: '4px solid rgba(255,255,255,0.1)',
            borderTopColor: 'var(--accent-color)',
            borderRadius: '50%',
            animation: 'rotate 1s linear infinite',
            marginBottom: '20px'
          }} />
          <h3 style={{ fontSize: '16px', fontWeight: 700 }}>
            {paymentStep === 1 ? 'Redirecting to PayPal Checkout...' : 'Processing PayPal transaction...'}
          </h3>
          <p style={{ fontSize: '12px', color: '#94a3b8', marginTop: '6px' }}>Please do not close this window</p>
        </div>
      )}

      {/* Payment Success Modal */}
      {showSuccess && (
        <div style={{
          position: 'fixed',
          inset: 0,
          backgroundColor: 'rgba(0,0,0,0.6)',
          backdropFilter: 'blur(4px)',
          zIndex: 9999,
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          padding: '24px'
        }}>
          <div className="animate-fade-in" style={{
            backgroundColor: 'var(--bg-secondary)',
            borderRadius: '16px',
            padding: '24px',
            width: '100%',
            maxWidth: '320px',
            textAlign: 'center',
            boxShadow: '0 20px 25px -5px rgba(0,0,0,0.3)',
            border: '1px solid var(--border-color)'
          }}>
            <span style={{ fontSize: '48px', display: 'block', marginBottom: '12px' }}>🛍️</span>
            <h3 style={{ fontSize: '18px', fontWeight: 800, marginBottom: '8px', color: 'var(--text-primary)' }}>Purchase Successful!</h3>
            <p style={{ fontSize: '13px', color: 'var(--text-secondary)', marginBottom: '20px' }}>
              Your account has been credited. Happy reading!
            </p>
            <div style={{
              display: 'inline-flex',
              alignItems: 'center',
              gap: '6px',
              backgroundColor: '#dcfce7',
              color: '#15803d',
              fontWeight: 700,
              padding: '6px 14px',
              borderRadius: '99px',
              marginBottom: '20px',
              fontSize: '14px'
            }}>
              <GoldCoin size={14} />
              <span>+{successAmount} Coins Added</span>
            </div>
            <button 
              className="btn-cta-primary" 
              style={{ width: '100%', padding: '10px 0' }}
              onClick={() => {
                setShowSuccess(false);
                onBack(); // Return to previous page
              }}
            >
              Back to Reader
            </button>
          </div>
        </div>
      )}

      {/* Legal & Compliance Policy Modal */}
      <LegalModal
        isOpen={activeLegalModal !== null}
        type={activeLegalModal || 'contact'}
        onClose={() => setActiveLegalModal(null)}
      />
    </div>
  );
};
