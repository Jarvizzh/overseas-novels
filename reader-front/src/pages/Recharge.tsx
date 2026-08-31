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
  onNavigate?: (pageOrPath: string, params?: any) => void;
}

export const Recharge: React.FC<RechargeProps> = ({
  userCoins,
  onAddCoins,
  onBack,
  onNavigate,
}) => {
  const { showToast } = useToast();
  const [selectedPack, setSelectedPack] = useState<number | null>(null);
  const [isProcessing, setIsProcessing] = useState(false);
  const [paymentStep, setPaymentStep] = useState(0); // 0: Idle, 1: connecting, 2: processing
  const [showSuccess, setShowSuccess] = useState(false);
  const [successAmount, setSuccessAmount] = useState(0);
  const [isVipSuccess, setIsVipSuccess] = useState(false);
  const [vipSuccessTitle, setVipSuccessTitle] = useState('');
  const [activeLegalModal, setActiveLegalModal] = useState<LegalModalType | null>(null);
  const [activeTab, setActiveTab] = useState<'subscription' | 'coins'>('subscription');
  const [isCancelling, setIsCancelling] = useState(false);

  // User VIP subscription status
  const [isVIP, setIsVIP] = useState(false);
  const [userSubscription, setUserSubscription] = useState<any>(null);

  // Recharge slot configurations from active default template
  const [slots, setSlots] = useState<Array<{
    id: number;
    template_id: number;
    slot_index: number;
    type: 'single' | 'subscription' | 'vip' | 'whole_book';
    coins: number;
    bonus: number;
    vip_duration: string;
    subscription_cycle?: string;
    vip_name: string;
    vip_desc: string;
    price: string;
    price_cents: number;
  }>>([]);
  const [loadingTemplates, setLoadingTemplates] = useState(true);

  const fetchStatusAndTemplates = async () => {
    try {
      const [templateData, subData] = await Promise.all([
        api.getRechargeTemplates().catch(() => null),
        api.getSubscriptionStatus().catch(() => null)
      ]);

      const slotList = templateData?.slots || [];
      setSlots(slotList);

      if (subData?.is_vip) {
        setIsVIP(true);
        setUserSubscription(subData.subscription);
      } else {
        setIsVIP(false);
        setUserSubscription(null);
      }

      // Default select first available slot
      if (slotList.length > 0) {
        const subSlot = slotList.find(s => s.type === 'subscription' || s.type === 'vip');
        if (subSlot) {
          setSelectedPack(subSlot.id);
          setActiveTab('subscription');
        } else {
          setSelectedPack(slotList[0].id);
          setActiveTab('coins');
        }
      }
    } catch (err) {
      console.error("Failed to load recharge data:", err);
    } finally {
      setLoadingTemplates(false);
    }
  };

  React.useEffect(() => {
    fetchStatusAndTemplates();
  }, []);

  // Handle PayPal redirect return
  React.useEffect(() => {
    const handlePayPalReturn = async () => {
      const urlParams = new URLSearchParams(window.location.search);
      const paymentStatus = urlParams.get('payment');
      const token = urlParams.get('token'); // PayPal Order ID or Subscription ID
      const subscriptionId = urlParams.get('subscription_id') || token;

      if (paymentStatus === 'cancel') {
        showToast("PayPal payment was cancelled.", "info");
        sessionStorage.removeItem('pending_paypal_recharge');
        window.history.replaceState({}, '', window.location.pathname);
        return;
      }

      if (token || subscriptionId) {
        setIsProcessing(true);
        setPaymentStep(2);

        let pendingInfo: any = null;
        try {
          const saved = sessionStorage.getItem('pending_paypal_recharge');
          if (saved) {
            pendingInfo = JSON.parse(saved);
          }
        } catch (_) {}

        const hasSubIdParam = !!urlParams.get('subscription_id');
        const isSubIdPattern = (subscriptionId && subscriptionId.startsWith('I-')) || (token && token.startsWith('I-'));
        const isSubOrder = pendingInfo?.type === 'subscription' || pendingInfo?.type === 'vip' || hasSubIdParam || isSubIdPattern;
        const expectedCoins = pendingInfo?.coins || 0;

        try {
          if (isSubOrder) {
            // Activate Recurring VIP Subscription
            const subIdToActivate = subscriptionId || token || '';
            await api.activateSubscription(subIdToActivate, 'paypal');
            setIsProcessing(false);
            setPaymentStep(0);
            setIsVIP(true);
            setIsVipSuccess(true);
            setVipSuccessTitle(pendingInfo?.name || 'VIP Unlimited Pass');
            setShowSuccess(true);
            sessionStorage.removeItem('pending_paypal_recharge');
            fetchStatusAndTemplates();
          } else {
            // Capture Single Coin Top-up Order
            await api.capturePayPalPayment(token!, expectedCoins);
            setIsProcessing(false);
            setPaymentStep(0);

            const reason = pendingInfo ? `PayPal Recharge: ${pendingInfo.price} pack` : 'PayPal Recharge Topup';
            onAddCoins(expectedCoins, reason);
            setSuccessAmount(expectedCoins);
            setIsVipSuccess(false);
            setShowSuccess(true);
            sessionStorage.removeItem('pending_paypal_recharge');
          }
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

  const subscriptionPacks = slots
    .filter(s => s.type === 'subscription' || s.type === 'vip')
    .map(s => ({
      id: s.id,
      name: s.vip_name || (s.subscription_cycle === 'day' ? 'VIP Daily Pass' : s.subscription_cycle === 'week' ? 'VIP Weekly Pass' : 'VIP Monthly Pass'),
      price: s.price,
      priceCents: s.price_cents,
      desc: s.vip_desc || '全站小说无限畅读，自动续费，随时可取消',
      cycle: s.subscription_cycle || s.vip_duration || 'month',
      type: s.type
    }));

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

  const handlePurchase = async () => {
    if (selectedPack === null) return;

    const tSlot = slots.find(p => p.id === selectedPack);
    if (!tSlot) return;

    setIsProcessing(true);
    setPaymentStep(1);

    try {
      const isSub = tSlot.type === 'subscription' || tSlot.type === 'vip';
      const lastContentPath = localStorage.getItem('last_read_content_path');

      sessionStorage.setItem('pending_paypal_recharge', JSON.stringify({
        slotId: tSlot.id,
        coins: tSlot.coins + tSlot.bonus,
        type: tSlot.type,
        name: tSlot.vip_name || tSlot.price,
        price: tSlot.price,
        time: Date.now(),
        returnContentPath: lastContentPath || '',
      }));

      const currentUrl = new URL(window.location.href);
      currentUrl.searchParams.delete('token');
      currentUrl.searchParams.delete('subscription_id');
      currentUrl.searchParams.delete('ba_token');
      currentUrl.searchParams.delete('PayerID');
      currentUrl.searchParams.delete('payment');

      const returnUrl = `${currentUrl.origin}${currentUrl.pathname}?payment=success`;
      const cancelUrl = `${currentUrl.origin}${currentUrl.pathname}?payment=cancel`;

      if (isSub) {
        // Create PayPal Subscription
        const resp = await api.createSubscription(tSlot.id, 'paypal', returnUrl, cancelUrl);
        if (resp?.approve_url) {
          window.location.href = resp.approve_url;
        } else {
          throw new Error("No PayPal subscription URL received");
        }
      } else {
        // Create One-time PayPal Order
        const totalCoins = tSlot.coins + tSlot.bonus;
        const resp = await api.createPayPalOrder(tSlot.price_cents, totalCoins, returnUrl, cancelUrl);
        if (resp?.approve_url) {
          window.location.href = resp.approve_url;
        } else {
          throw new Error("No PayPal checkout URL received");
        }
      }
    } catch (err: any) {
      setIsProcessing(false);
      setPaymentStep(0);
      showToast(err.message || "Failed to initiate PayPal checkout. Please try again.", "error");
    }
  };

  const handleCancelSubscription = async () => {
    if (!userSubscription?.subscription_id) return;
    if (!window.confirm("Are you sure you want to cancel your VIP subscription? You will still retain VIP unlimited access until the end of your current billing period.")) {
      return;
    }

    setIsCancelling(true);
    try {
      await api.cancelSubscription(userSubscription.subscription_id, "User requested cancellation from frontend");
      showToast("VIP subscription cancelled successfully. Access remains valid until current period ends.", "success");
      fetchStatusAndTemplates();
    } catch (err: any) {
      showToast(err.message || "Failed to cancel subscription", "error");
    } finally {
      setIsCancelling(false);
    }
  };

  const selectedSlot = slots.find(p => p.id === selectedPack);

  if (loadingTemplates) {
    return (
      <div style={{
        display: 'flex', minHeight: '100vh', alignItems: 'center', justifyContent: 'center',
        backgroundColor: 'var(--bg-primary)', color: 'var(--text-secondary)', fontSize: '14px'
      }}>
        Loading recharge options...
      </div>
    );
  }

  const handleReturnToContent = () => {
    let returnPath = '';
    try {
      const saved = sessionStorage.getItem('pending_paypal_recharge');
      if (saved) {
        const parsed = JSON.parse(saved);
        if (parsed.returnContentPath) {
          returnPath = parsed.returnContentPath;
        }
      }
    } catch (_) {}

    if (!returnPath) {
      returnPath = localStorage.getItem('last_read_content_path') || '';
    }

    if (returnPath) {
      if (onNavigate) {
        onNavigate(returnPath);
      } else {
        window.location.href = returnPath;
      }
    } else {
      if (onNavigate) {
        onNavigate('shelf');
      } else {
        onBack();
      }
    }
  };

  return (
    <div className="page-container-full animate-fade-in" style={{ backgroundColor: 'var(--bg-primary)' }}>
      {/* Top Header */}
      <header className="app-header glass-panel">
        <div style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
          <button className="header-btn" onClick={handleReturnToContent} aria-label="Go back">
            <svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" strokeWidth={2.5} stroke="currentColor" style={{ width: '20px', height: '20px' }}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M15.75 19.5L8.25 12l7.5-7.5" />
            </svg>
          </button>
          <h1 className="header-title">Membership & Top-Up</h1>
        </div>
      </header>

      {/* Main Content Area */}
      <div className="scroll-container-no-pad" style={{ flex: 1, minHeight: 0, padding: '16px 16px 60px' }}>
        
        {/* Active VIP Member Card Banner */}
        {isVIP && userSubscription && (
          <div style={{
            background: 'linear-gradient(135deg, #1e1b4b 0%, #312e81 50%, #4338ca 100%)',
            borderRadius: '16px',
            padding: '20px',
            color: '#ffffff',
            boxShadow: '0 8px 24px rgba(67, 56, 202, 0.25)',
            marginBottom: '20px',
            position: 'relative',
            overflow: 'hidden'
          }}>
            <div style={{ position: 'relative', zIndex: 1 }}>
              <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start' }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: '10px' }}>
                  <span style={{ fontSize: '28px' }}>👑</span>
                  <div>
                    <h3 style={{ fontSize: '18px', fontWeight: 800, margin: 0, color: '#fef08a' }}>VIP Unlimited Member</h3>
                    <p style={{ fontSize: '12px', color: '#cbd5e1', marginTop: '2px' }}>
                      All-Access Pass: Free reading for all novels & chapters
                    </p>
                  </div>
                </div>
                <span style={{
                  backgroundColor: 'rgba(254, 240, 138, 0.2)',
                  color: '#fef08a',
                  border: '1px solid rgba(254, 240, 138, 0.4)',
                  fontSize: '11px',
                  fontWeight: 700,
                  padding: '3px 8px',
                  borderRadius: '99px',
                  textTransform: 'uppercase'
                }}>
                  {userSubscription.cycle} Plan
                </span>
              </div>

              <div style={{
                marginTop: '16px',
                paddingTop: '12px',
                borderTop: '1px solid rgba(255,255,255,0.15)',
                display: 'flex',
                justifyContent: 'space-between',
                alignItems: 'center',
                fontSize: '12px'
              }}>
                <div>
                  <span style={{ color: '#94a3b8' }}>Valid Until: </span>
                  <span style={{ fontWeight: 600, color: '#ffffff' }}>
                    {userSubscription.current_period_end ? new Date(userSubscription.current_period_end).toLocaleDateString() : 'Active'}
                  </span>
                </div>
                {userSubscription.status === 'ACTIVE' && (
                  <button
                    onClick={handleCancelSubscription}
                    disabled={isCancelling}
                    style={{
                      background: 'none',
                      border: 'none',
                      color: '#fca5a5',
                      fontSize: '12px',
                      textDecoration: 'underline',
                      cursor: isCancelling ? 'not-allowed' : 'pointer'
                    }}
                  >
                    {isCancelling ? 'Cancelling...' : 'Cancel Subscription'}
                  </button>
                )}
              </div>
            </div>
          </div>
        )}

        {/* Balance Display Card */}
        <div style={{
          backgroundColor: 'var(--bg-secondary)',
          borderRadius: '16px',
          padding: '16px 20px',
          border: '1px solid var(--border-color)',
          boxShadow: 'var(--card-shadow)',
          marginBottom: '20px',
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center'
        }}>
          <div>
            <span style={{ fontSize: '12px', color: 'var(--text-tertiary)' }}>Current Coin Balance</span>
            <div style={{ display: 'flex', alignItems: 'center', gap: '8px', marginTop: '2px' }}>
              <GoldCoin size={22} />
              <span style={{ fontSize: '24px', fontWeight: 800, color: 'var(--text-primary)', fontFamily: 'monospace' }}>
                {userCoins}
              </span>
              <span style={{ fontSize: '13px', color: 'var(--text-secondary)', fontWeight: 600 }}>Coins</span>
            </div>
          </div>
          <span style={{ fontSize: '28px', opacity: 0.85 }}>👛</span>
        </div>

        {/* Mode Navigation Tabs: VIP Subscription vs Coins Top-Up */}
        <div style={{
          display: 'flex',
          backgroundColor: 'var(--bg-secondary)',
          borderRadius: '12px',
          padding: '4px',
          marginBottom: '20px',
          border: '1px solid var(--border-color)'
        }}>
          <button
            onClick={() => {
              setActiveTab('subscription');
              if (subscriptionPacks.length > 0) setSelectedPack(subscriptionPacks[0].id);
            }}
            style={{
              flex: 1,
              padding: '10px 0',
              borderRadius: '8px',
              border: 'none',
              fontSize: '13px',
              fontWeight: 700,
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              gap: '6px',
              backgroundColor: activeTab === 'subscription' ? 'var(--accent-color)' : 'transparent',
              color: activeTab === 'subscription' ? '#ffffff' : 'var(--text-secondary)',
              cursor: 'pointer',
              transition: 'all 0.2s ease'
            }}
          >
            <span>👑 VIP Unlimited</span>
            <span style={{
              fontSize: '10px',
              backgroundColor: activeTab === 'subscription' ? 'rgba(255,255,255,0.25)' : '#ef4444',
              color: '#ffffff',
              padding: '1px 5px',
              borderRadius: '4px',
              fontWeight: 800
            }}>HOT</span>
          </button>

          <button
            onClick={() => {
              setActiveTab('coins');
              if (coinPacks.length > 0) setSelectedPack(coinPacks[0].id);
            }}
            style={{
              flex: 1,
              padding: '10px 0',
              borderRadius: '8px',
              border: 'none',
              fontSize: '13px',
              fontWeight: 700,
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              gap: '6px',
              backgroundColor: activeTab === 'coins' ? 'var(--accent-color)' : 'transparent',
              color: activeTab === 'coins' ? '#ffffff' : 'var(--text-secondary)',
              cursor: 'pointer',
              transition: 'all 0.2s ease'
            }}
          >
            <span>💰 Coin Packs</span>
          </button>
        </div>

        {/* Tab Content 1: VIP Subscription */}
        {activeTab === 'subscription' && (
          <div className="animate-fade-in" style={{ marginBottom: '24px' }}>
            <div style={{
              backgroundColor: 'rgba(79, 70, 229, 0.08)',
              border: '1px solid rgba(79, 70, 229, 0.2)',
              borderRadius: '12px',
              padding: '12px 16px',
              marginBottom: '16px',
              display: 'flex',
              alignItems: 'center',
              gap: '10px'
            }}>
              <span style={{ fontSize: '20px' }}>✨</span>
              <p style={{ fontSize: '12px', color: 'var(--text-secondary)', margin: 0, lineHeight: 1.5 }}>
                <strong style={{ color: 'var(--accent-color)' }}>VIP All-Access:</strong> Read every novel & paid chapter on the entire site without coins! Auto-renews, cancel anytime.
              </p>
            </div>

            <div style={{ display: 'flex', flexDirection: 'column', gap: '12px' }}>
              {subscriptionPacks.map((pack) => {
                const isSelected = selectedPack === pack.id;
                return (
                  <div
                    key={pack.id}
                    onClick={() => setSelectedPack(pack.id)}
                    style={{
                      display: 'flex',
                      justifyContent: 'space-between',
                      alignItems: 'center',
                      backgroundColor: isSelected ? 'rgba(79, 70, 229, 0.05)' : 'var(--bg-secondary)',
                      borderRadius: '14px',
                      padding: '16px',
                      border: isSelected ? '2px solid var(--accent-color)' : '1px solid var(--border-color)',
                      boxShadow: isSelected ? '0 4px 15px rgba(79, 70, 229, 0.15)' : 'var(--card-shadow)',
                      cursor: 'pointer',
                      position: 'relative',
                      transition: 'all 0.2s ease'
                    }}
                  >
                    <div style={{ display: 'flex', gap: '12px', alignItems: 'center' }}>
                      <span style={{ fontSize: '30px' }}>👑</span>
                      <div>
                        <div style={{ display: 'flex', alignItems: 'center', gap: '6px' }}>
                          <h4 style={{ fontSize: '15px', fontWeight: 800, margin: 0, color: 'var(--text-primary)' }}>{pack.name}</h4>
                          <span style={{
                            fontSize: '10px',
                            backgroundColor: 'rgba(79, 70, 229, 0.15)',
                            color: 'var(--accent-color)',
                            fontWeight: 700,
                            padding: '1px 6px',
                            borderRadius: '4px',
                            textTransform: 'uppercase'
                          }}>
                            {pack.cycle}
                          </span>
                        </div>
                        <p style={{ fontSize: '11px', color: 'var(--text-tertiary)', marginTop: '4px', margin: 0 }}>{pack.desc}</p>
                      </div>
                    </div>

                    <div style={{ textAlign: 'right' }}>
                      <span style={{
                        fontSize: '15px',
                        fontWeight: 800,
                        backgroundColor: isSelected ? 'var(--accent-color)' : 'var(--bg-tertiary)',
                        color: isSelected ? '#ffffff' : 'var(--text-secondary)',
                        padding: '6px 14px',
                        borderRadius: '8px',
                        display: 'inline-block',
                        transition: 'var(--transition-fast)'
                      }}>
                        {pack.price}
                      </span>
                      <span style={{ display: 'block', fontSize: '10px', color: 'var(--text-tertiary)', marginTop: '2px' }}>
                        /{pack.cycle}
                      </span>
                    </div>
                  </div>
                );
              })}
            </div>
          </div>
        )}

        {/* Tab Content 2: Single Coin Packs */}
        {activeTab === 'coins' && (
          <div className="animate-fade-in" style={{ marginBottom: '24px' }}>
            <div style={{
              display: 'grid',
              gridTemplateColumns: 'repeat(2, 1fr)',
              gap: '12px'
            }}>
              {coinPacks.map((pack) => {
                const isSelected = selectedPack === pack.id;
                return (
                  <div
                    key={pack.id}
                    onClick={() => setSelectedPack(pack.id)}
                    style={{
                      backgroundColor: isSelected ? 'rgba(79, 70, 229, 0.05)' : 'var(--bg-secondary)',
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
                    {pack.bonus > 0 && (
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
                    )}

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
          </div>
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
                <span style={{ fontSize: '11px', color: 'var(--text-tertiary)', textTransform: 'uppercase', letterSpacing: '0.5px', fontWeight: 600 }}>Selected Package</span>
                <span style={{ fontSize: '15px', fontWeight: 800, color: 'var(--text-primary)', display: 'block', marginTop: '2px' }}>
                  {selectedSlot.type === 'single'
                    ? `${selectedSlot.coins} Coins (+${selectedSlot.bonus} Bonus)`
                    : selectedSlot.vip_name}
                </span>
                {(selectedSlot.type === 'subscription' || selectedSlot.type === 'vip') && (
                  <span style={{ fontSize: '11px', color: '#16a34a', fontWeight: 600, display: 'block', marginTop: '2px' }}>
                    ✓ VIP Unlimited Reading Access
                  </span>
                )}
              </div>
              <div style={{ textAlign: 'right' }}>
                <span style={{ fontSize: '11px', color: 'var(--text-tertiary)', textTransform: 'uppercase', letterSpacing: '0.5px', fontWeight: 600 }}>Amount</span>
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
              <span>
                {isProcessing
                  ? 'Connecting to PayPal...'
                  : selectedSlot.type === 'subscription' || selectedSlot.type === 'vip'
                    ? `PayPal · Subscribe ${selectedSlot.price}`
                    : `PayPal · Pay ${selectedSlot.price}`}
              </span>
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
              <span>⚡ Instant VIP Activation</span>
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
          <p style={{ marginBottom: '4px' }}>1. <strong>VIP Unlimited Membership</strong> grants instant, full reading access to all novel chapters across the site without consuming coins during the subscription period.</p>
          <p style={{ marginBottom: '4px' }}>2. <strong>Coin Top-up</strong> is a one-time purchase. Coins never expire and can be used to unlock individual chapters.</p>
          <p>3. You can cancel recurring subscriptions at any time. For support, please reach out via <span style={{ color: 'var(--accent-color)', fontWeight: 600, cursor: 'pointer', textDecoration: 'underline' }} onClick={() => setActiveLegalModal('contact')}>Customer Support</span>.</p>
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
            <span style={{ fontSize: '48px', display: 'block', marginBottom: '12px' }}>
              {isVipSuccess ? '👑' : '🛍️'}
            </span>
            <h3 style={{ fontSize: '18px', fontWeight: 800, marginBottom: '8px', color: 'var(--text-primary)' }}>
              {isVipSuccess ? 'VIP Unlimited Activated!' : 'Purchase Successful!'}
            </h3>
            <p style={{ fontSize: '13px', color: 'var(--text-secondary)', marginBottom: '20px', lineHeight: 1.5 }}>
              {isVipSuccess
                ? `You now have full unlimited access to all novels and chapters across the site.`
                : `Your account has been credited with coins. Happy reading!`}
            </p>
            
            {isVipSuccess ? (
              <div style={{
                display: 'inline-flex',
                alignItems: 'center',
                gap: '6px',
                backgroundColor: 'rgba(79, 70, 229, 0.12)',
                color: 'var(--accent-color)',
                fontWeight: 700,
                padding: '6px 14px',
                borderRadius: '99px',
                marginBottom: '20px',
                fontSize: '13px'
              }}>
                <span>✨ {vipSuccessTitle} Active</span>
              </div>
            ) : (
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
            )}

            <button 
              className="btn-cta-primary" 
              style={{ width: '100%', padding: '10px 0' }}
              onClick={() => {
                setShowSuccess(false);
                handleReturnToContent();
              }}
            >
              Start Reading Now
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
