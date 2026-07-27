import React, { useState } from 'react';
import { useToast } from '../context/ToastContext';
import { GoldCoin } from '../components/GoldCoin';
import { api } from '../utils/api';

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
  const [paymentMethod, setPaymentMethod] = useState<'stripe' | 'paypal'>('stripe');
  const [isProcessing, setIsProcessing] = useState(false);
  const [paymentStep, setPaymentStep] = useState(0); // 0: Idle, 1: connecting, 2: processing
  const [showStripeModal, setShowStripeModal] = useState(false);
  const [showSuccess, setShowSuccess] = useState(false);
  const [successAmount, setSuccessAmount] = useState(0);

  // Stripe form fields
  const [cardNumber, setCardNumber] = useState('4242 4242 4242 4242');
  const [expiry, setExpiry] = useState('12/28');
  const [cvc, setCvc] = useState('321');

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

    const pack = {
      id: tSlot.id,
      coins: tSlot.coins,
      bonus: tSlot.bonus,
      price: tSlot.price,
      priceCents: tSlot.price_cents,
      total: tSlot.coins + tSlot.bonus,
      type: tSlot.type,
      name: tSlot.vip_name,
      desc: tSlot.vip_desc
    };

    setIsProcessing(true);
    setPaymentStep(1);

    try {
      if (paymentMethod === 'stripe') {
        // 1. Create Stripe Intent via API
        await api.createStripeIntent(pack.priceCents, pack.total);
        
        // 2. Open Stripe Card payment sheet
        setIsProcessing(false);
        setPaymentStep(0);
        setShowStripeModal(true);
      } else {
        // PayPal Flow: Send InitiateCheckout event then execute capture simulation
        try { await api.initiateCheckout(pack.priceCents, pack.total); } catch (_) {}
        setPaymentStep(2);
        const orderId = "mock_paypal_order_" + Date.now();
        await api.capturePayPalPayment(orderId, pack.total);
        
        setIsProcessing(false);
        setPaymentStep(0);

        let reason = '';
        if (pack.type === 'single') {
          reason = `PayPal Recharge: ${pack.price} pack`;
        } else if (pack.type === 'vip') {
          reason = `PayPal Subscription: ${pack.name}`;
        } else {
          reason = `PayPal Purchase: Whole Book (${pack.name})`;
        }
        onAddCoins(pack.total, reason);
        setSuccessAmount(pack.total);
        setShowSuccess(true);
      }
    } catch (err: any) {
      setIsProcessing(false);
      setPaymentStep(0);
      showToast(err.message || "Payment intent creation failed. Please try again.", "error");
    }
  };

  const handleStripePaymentSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setShowStripeModal(false);
    setIsProcessing(true);
    setPaymentStep(2);

    const tSlot = slots.find(p => p.id === selectedPack);
    if (!tSlot) return;

    const pack = {
      id: tSlot.id,
      coins: tSlot.coins,
      bonus: tSlot.bonus,
      price: tSlot.price,
      priceCents: tSlot.price_cents,
      total: tSlot.coins + tSlot.bonus,
      type: tSlot.type,
      name: tSlot.vip_name,
      desc: tSlot.vip_desc
    };

    try {
      // Simulate real db write using PayPal capturing backdoor in local sandbox mode
      const simulatedOrderId = "simulated_stripe_charge_" + Date.now();
      await api.capturePayPalPayment(simulatedOrderId, pack.total);

      setIsProcessing(false);
      setPaymentStep(0);

      let reason = '';
      if (pack.type === 'single') {
        reason = `Stripe Recharge: ${pack.price} pack`;
      } else if (pack.type === 'vip') {
        reason = `Stripe Subscription: ${pack.name}`;
      } else {
        reason = `Stripe Purchase: Whole Book (${pack.name})`;
      }
      onAddCoins(pack.total, reason);
      setSuccessAmount(pack.total);
      setShowSuccess(true);
    } catch (err: any) {
      setIsProcessing(false);
      setPaymentStep(0);
      showToast(err.message || "Stripe mock payment processing failed.", "error");
    }
  };

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
      <div className="scroll-container-no-pad" style={{ height: 'calc(100vh - 56px)', padding: '16px 16px 40px' }}>
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

        {/* Payment Method Selector */}
        <h3 className="hot-tags-title" style={{ fontSize: '13px', marginBottom: '10px' }}>Payment Method</h3>
        <div style={{ display: 'flex', flexDirection: 'column', gap: '10px', marginBottom: '24px' }}>
          <div 
            onClick={() => setPaymentMethod('stripe')}
            style={{
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'space-between',
              padding: '14px 16px',
              backgroundColor: 'var(--bg-secondary)',
              borderRadius: '12px',
              border: paymentMethod === 'stripe' ? '2.5px solid var(--accent-color)' : '1px solid var(--border-color)',
              cursor: 'pointer'
            }}
          >
            <div style={{ display: 'flex', alignItems: 'center', gap: '10px' }}>
              <span style={{ fontSize: '20px' }}>💳</span>
              <span style={{ fontSize: '14px', fontWeight: 600, color: 'var(--text-primary)' }}>Stripe (Credit / Debit Card)</span>
            </div>
            <div style={{
              width: '18px',
              height: '18px',
              borderRadius: '50%',
              border: '2px solid var(--border-color)',
              backgroundColor: paymentMethod === 'stripe' ? 'var(--accent-color)' : 'transparent'
            }} />
          </div>

          <div 
            onClick={() => setPaymentMethod('paypal')}
            style={{
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'space-between',
              padding: '14px 16px',
              backgroundColor: 'var(--bg-secondary)',
              borderRadius: '12px',
              border: paymentMethod === 'paypal' ? '2.5px solid var(--accent-color)' : '1px solid var(--border-color)',
              cursor: 'pointer'
            }}
          >
            <div style={{ display: 'flex', alignItems: 'center', gap: '10px' }}>
              <span style={{ fontSize: '20px' }}>🅿️</span>
              <span style={{ fontSize: '14px', fontWeight: 600, color: 'var(--text-primary)' }}>PayPal Checkout</span>
            </div>
            <div style={{
              width: '18px',
              height: '18px',
              borderRadius: '50%',
              border: '2px solid var(--border-color)',
              backgroundColor: paymentMethod === 'paypal' ? 'var(--accent-color)' : 'transparent'
            }} />
          </div>
        </div>

        {/* Recharge Action Button */}
        <button
          className="btn-cta-primary"
          onClick={handlePurchase}
          style={{ width: '100%', padding: '14px 0', fontSize: '15px', fontWeight: 700, borderRadius: '12px', marginBottom: '28px' }}
        >
          Confirm Purchase
        </button>

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
          <p style={{ marginBottom: '4px' }}>1. Coins are virtual items and cannot be refunded. They can only be used within this app.</p>
          <p style={{ marginBottom: '4px' }}>2. Reward coins are time-sensitive. Please use them within the 7-day validity period, as they will be deducted first when unlocking content. Coins are not redeemable or transferable to other users.</p>
          <p>3. For other questions, please contact us via Profile &gt; Customer Service.</p>
        </div>
      </div>

      {/* Simulated Payment Gateway Loading Screen */}
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
            {paymentStep === 1 ? 'Connecting to payment gateway...' : 'Processing transaction...'}
          </h3>
          <p style={{ fontSize: '12px', color: '#94a3b8', marginTop: '6px' }}>Please do not close this window</p>
        </div>
      )}

      {/* Stripe Payment Form Modal */}
      {showStripeModal && (
        <div style={{
          position: 'fixed',
          inset: 0,
          backgroundColor: 'rgba(0,0,0,0.6)',
          backdropFilter: 'blur(5px)',
          zIndex: 999,
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          padding: '20px'
        }}>
          <div className="animate-fade-in" style={{
            backgroundColor: 'var(--bg-secondary)',
            borderRadius: '16px',
            padding: '24px',
            width: '100%',
            maxWidth: '340px',
            boxShadow: '0 20px 25px -5px rgba(0,0,0,0.3)',
            border: '1px solid var(--border-color)',
            position: 'relative'
          }}>
            <button 
              onClick={() => setShowStripeModal(false)}
              style={{
                position: 'absolute',
                top: '14px',
                right: '14px',
                background: 'none',
                border: 'none',
                color: 'var(--text-secondary)',
                fontSize: '20px',
                cursor: 'pointer'
              }}
            >
              ×
            </button>

            <h3 style={{ fontSize: '18px', fontWeight: 800, marginBottom: '20px', color: 'var(--text-primary)', textAlign: 'center' }}>
              💳 Pay with Stripe
            </h3>

            <form onSubmit={handleStripePaymentSubmit} style={{ display: 'flex', flexDirection: 'column', gap: '14px' }}>
              <div>
                <label style={{ fontSize: '12px', color: 'var(--text-secondary)', display: 'block', marginBottom: '4px' }}>Card Number</label>
                <input 
                  type="text" 
                  value={cardNumber}
                  onChange={(e) => setCardNumber(e.target.value)}
                  required 
                  style={{
                    width: '100%',
                    padding: '10px 14px',
                    borderRadius: '8px',
                    border: '1px solid var(--border-color)',
                    backgroundColor: 'var(--bg-primary)',
                    color: 'var(--text-primary)',
                    fontSize: '14px'
                  }}
                />
              </div>

              <div style={{ display: 'flex', gap: '14px' }}>
                <div style={{ flex: 1 }}>
                  <label style={{ fontSize: '12px', color: 'var(--text-secondary)', display: 'block', marginBottom: '4px' }}>Expiry</label>
                  <input 
                    type="text" 
                    placeholder="MM/YY"
                    value={expiry}
                    onChange={(e) => setExpiry(e.target.value)}
                    required 
                    style={{
                      width: '100%',
                      padding: '10px 14px',
                      borderRadius: '8px',
                      border: '1px solid var(--border-color)',
                      backgroundColor: 'var(--bg-primary)',
                      color: 'var(--text-primary)',
                      fontSize: '14px'
                    }}
                  />
                </div>
                <div style={{ flex: 1 }}>
                  <label style={{ fontSize: '12px', color: 'var(--text-secondary)', display: 'block', marginBottom: '4px' }}>CVC</label>
                  <input 
                    type="text" 
                    placeholder="123"
                    value={cvc}
                    onChange={(e) => setCvc(e.target.value)}
                    required 
                    maxLength={4}
                    style={{
                      width: '100%',
                      padding: '10px 14px',
                      borderRadius: '8px',
                      border: '1px solid var(--border-color)',
                      backgroundColor: 'var(--bg-primary)',
                      color: 'var(--text-primary)',
                      fontSize: '14px'
                    }}
                  />
                </div>
              </div>

              <button 
                type="submit" 
                style={{
                  width: '100%',
                  padding: '12px',
                  backgroundColor: 'var(--accent-color)',
                  color: 'white',
                  border: 'none',
                  borderRadius: '24px',
                  fontSize: '15px',
                  fontWeight: 700,
                  cursor: 'pointer',
                  boxShadow: '0 4px 14px rgba(79,70,229,0.3)',
                  marginTop: '10px'
                }}
              >
                Pay Now
              </button>
            </form>
          </div>
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
    </div>
  );
};
