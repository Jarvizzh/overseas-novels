package billing

import "time"

type Order struct {
	ID                 int64      `json:"id"`
	UserID             string     `json:"user_id"`
	ExternalRefID      string     `json:"external_ref_id"`
	AmountCents        int        `json:"amount_cents"`
	Currency           string     `json:"currency"`
	Coins              int        `json:"coins"`
	BonusCoinsCredited int        `json:"bonus_coins_credited"`
	PaymentMethod      string     `json:"payment_method"`
	Status             string     `json:"status"` // Success, Refunded, etc
	UtmSource          string     `json:"utm_source"`
	UtmCampaign        string     `json:"utm_campaign"`
	PaidAt             *time.Time `json:"paid_at"`
	OrderType          string     `json:"order_type"`
	PromotionLinkID    *int64     `json:"promotion_link_id"`
	NovelID            *int64     `json:"novel_id"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

type RechargeSlot struct {
	ID          int    `json:"id"`
	TemplateID  int    `json:"template_id"`
	SlotIndex   int    `json:"slot_index"` // 1 to 6
	Type        string `json:"type"`       // 'single', 'vip', 'whole_book'
	Coins       int    `json:"coins"`
	Bonus       int    `json:"bonus"`
	VipDuration string `json:"vip_duration"`
	VipName     string `json:"vip_name"`
	VipDesc     string `json:"vip_desc"`
	Price       string `json:"price"`
	PriceCents  int    `json:"price_cents"`
}

type RechargeTemplate struct {
	ID        int            `json:"id"`
	Name      string         `json:"name"`
	IsDefault bool           `json:"is_default"`
	Slots     []RechargeSlot `json:"slots"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}
