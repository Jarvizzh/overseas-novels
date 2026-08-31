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

type ThirdPartyPaymentOrder struct {
	ID                     int64     `json:"id"`
	OrderID                int64     `json:"order_id"`
	PaymentProvider        string    `json:"payment_provider"`
	ExternalOrderID        string    `json:"external_order_id"`
	CaptureID              string    `json:"capture_id"`
	PayerID                string    `json:"payer_id"`
	PayerEmail             string    `json:"payer_email"`
	PayerName              string    `json:"payer_name"`
	PayerCountry           string    `json:"payer_country"`
	Currency               string    `json:"currency"`
	GrossAmount            float64   `json:"gross_amount"`
	FeeAmount              float64   `json:"fee_amount"`
	NetAmount              float64   `json:"net_amount"`
	Status                 string    `json:"status"`
	SellerProtectionStatus string    `json:"seller_protection_status"`
	RawPayload             string    `json:"raw_payload,omitempty"`
	CreatedAt              time.Time `json:"created_at"`
	UpdatedAt              time.Time `json:"updated_at"`
}
