export type RechargeSlotItem = {
  id: number;
  template_id: number;
  slot_index: number;
  product_id?: number;
  product_code?: string;
  product_type?: 'single' | 'whole_book' | 'vip';
  type: 'single' | 'subscription' | 'vip' | 'whole_book';
  name?: string;
  description?: string;
  coins: number;
  bonus: number;
  vip_duration: string;
  subscription_cycle?: string;
  vip_name: string;
  vip_desc: string;
  badge?: string;
  is_highlighted?: boolean;
  display_alias?: string;
  novel_scope_type?: string;
  paypal_ready?: boolean;
  stripe_ready?: boolean;
  price: string;
  price_cents: number;
};

export function vipCycleLabel(cycle?: string): string {
  switch ((cycle || '').toLowerCase()) {
    case 'day':
      return 'VIP Daily Pass';
    case 'week':
      return 'VIP Weekly Pass';
    case 'year':
      return 'VIP Annual Pass';
    case 'month':
    default:
      return 'VIP Monthly Pass';
  }
}

export function buildSubscriptionPacks(slots: RechargeSlotItem[]) {
  return slots
    .filter((s) => s.type === 'subscription' || s.type === 'vip' || s.product_type === 'vip')
    .map((s) => {
      const cycle = s.subscription_cycle || s.vip_duration || 'month';
      return {
        id: s.id,
        productId: s.product_id,
        name: s.display_alias || s.name || s.vip_name || vipCycleLabel(cycle),
        badge: s.badge || (s.is_highlighted ? 'Popular' : ''),
        isHighlighted: Boolean(s.is_highlighted),
        price: s.price,
        priceCents: s.price_cents,
        desc: s.description || s.vip_desc || 'Unlimited reading across all titles. Auto-renews, cancel anytime.',
        cycle,
        type: s.type,
      };
    });
}

export function buildCoinPacks(slots: RechargeSlotItem[], novelId?: number) {
  return slots
    .filter((s) => s.type === 'single' || (s.type === 'whole_book' && novelId))
    .map((s) => {
      const bonus = s.type === 'single' ? s.bonus : 0;
      return {
        id: s.id,
        productId: s.product_id,
        coins: s.coins,
        bonus,
        price: s.price,
        priceCents: s.price_cents,
        total: s.coins + bonus,
        name: s.display_alias || s.name || s.vip_name || '',
        badge: s.badge || '',
        isHighlighted: Boolean(s.is_highlighted),
        type: s.type,
      };
    });
}

export function formatCoinPackSubtitle(pack: { name: string; bonus: number; total: number }): string {
  if (pack.name) {
    return pack.bonus > 0 ? `${pack.name} · +${pack.bonus} bonus` : pack.name;
  }
  return `Total: ${pack.total.toLocaleString()} Coins`;
}
