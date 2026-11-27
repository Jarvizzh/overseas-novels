package model

import "time"

// User represents a user in the system (can be guest or registered)
type User struct {
	ID           int64     `json:"id" db:"id"`
	Email        *string   `json:"email" db:"email"`     // Nullable
	PasswordHash *string   `json:"-" db:"password_hash"` // Nullable, omitted in JSON
	Nickname     string    `json:"nickname" db:"nickname"`
	AvatarURL    string    `json:"avatar_url" db:"avatar_url"`
	Status       int16     `json:"status" db:"status"` // 1-Normal, 2-Banned
	Device       string    `json:"device" db:"device"`
	IPAddress    string    `json:"ip_address" db:"ip_address"`
	UTMSource    string    `json:"utm_source" db:"utm_source"`
	UTMCampaign  string    `json:"utm_campaign" db:"utm_campaign"`
	CreatedAt    time.Time `json:"created_at" db:"created_at"`
}

// Novel represents novel metadata
type Novel struct {
	ContentVersion       int64     `json:"content_version"`
	ID                   int64     `json:"id" db:"id"`
	Title                string    `json:"title" db:"title"`
	Author               string    `json:"author" db:"author"`
	CoverURL             string    `json:"cover" db:"cover_url"`
	Rating               float64   `json:"rating" db:"rating"`
	Status               string    `json:"status" db:"status"`                 // Ongoing / Completed / archived
	PublishStatus        string    `json:"publish_status" db:"publish_status"` // draft / published / offline
	Synopsis             string    `json:"synopsis" db:"synopsis"`
	Genres               []string  `json:"genres" db:"genres"`
	WordCount            int       `json:"words" db:"word_count"`
	ViewCount            int64     `json:"views" db:"view_count"`
	CoinCostPerThousand  *int      `json:"coin_cost_per_thousand" db:"coin_cost_per_thousand"`
	StartPayChapterIndex int       `json:"start_pay_chapter_index" db:"start_pay_chapter_index"`
	CreatedAt            time.Time `json:"created_at" db:"created_at"`
}

// Chapter represents a chapter of a novel
type Chapter struct {
	BodyRevision       int64     `json:"-"`
	PublicationVersion int64     `json:"content_version,omitempty"`
	ObjectKey          string    `json:"-"`
	ContentHash        string    `json:"-"`
	ID                 string    `json:"id" db:"id"`
	NovelID            int64     `json:"novel_id" db:"novel_id"`
	ChapterIndex       int       `json:"chapter_index" db:"chapter_index"`
	Title              string    `json:"title" db:"title"`
	Content            string    `json:"content,omitempty" db:"content"` // Omitted in lists
	WordCount          int       `json:"word_count" db:"word_count"`
	IsPaid             bool      `json:"is_paid" db:"is_paid"`
	Price              int       `json:"price" db:"price"`
	CreatedAt          time.Time `json:"created_at" db:"created_at"`
}

// Bookshelf represents a user's saved novel and reading progress
type Bookshelf struct {
	ProgressVersion        string    `json:"progress_version"`
	ID                     int64     `json:"-" db:"id"`
	UserID                 int64     `json:"user_id" db:"user_id"`
	NovelID                int64     `json:"novel_id" db:"novel_id"`
	ChapterIndex           int       `json:"chapter_index" db:"chapter_index"`
	ScrollOffsetPercentage float64   `json:"scroll_offset_percentage" db:"scroll_offset_percentage"`
	InShelf                bool      `json:"in_shelf" db:"in_shelf"`
	UpdatedAt              time.Time `json:"updated_at" db:"updated_at"`
}

type BookshelfWithNovel struct {
	Bookshelf
	Novel      Novel     `json:"novel"`
	ActivityAt time.Time `json:"-"`
}

// Wallet represents a user's coins balance
type Wallet struct {
	UserID       int64     `json:"user_id" db:"user_id"`
	ChargedCoins int       `json:"charged_coins" db:"charged_coins"`
	BonusCoins   int       `json:"bonus_coins" db:"bonus_coins"`
	UpdatedAt    time.Time `json:"updated_at" db:"updated_at"`
}

// Transaction represents wallet transaction histories
type Transaction struct {
	ID          string    `json:"id" db:"id"`
	UserID      int64     `json:"user_id" db:"user_id"`
	Type        string    `json:"type" db:"type"`         // credit / debit
	BizType     string    `json:"biz_type" db:"biz_type"` // recharge, checkin, unlock, reward_task
	Amount      int       `json:"amount" db:"amount"`
	ChargedAmt  int       `json:"charged_amount" db:"charged_amount"`
	BonusAmt    int       `json:"bonus_amount" db:"bonus_amount"`
	Description string    `json:"desc" db:"description"`
	CreatedAt   time.Time `json:"date" db:"created_at"`
}

// UnlockRecord represents locked chapter unlock logs
type UnlockRecord struct {
	ID           int64     `json:"id" db:"id"`
	UserID       int64     `json:"user_id" db:"user_id"`
	NovelID      int64     `json:"novel_id" db:"novel_id"`
	ChapterIndex int       `json:"chapter_index" db:"chapter_index"`
	CreatedAt    time.Time `json:"created_at" db:"created_at"`
}

// RechargeSlot represents recharge slot settings hydrated from unified immutable products
type RechargeSlot struct {
	ID                int     `json:"id" db:"id"`
	TemplateID        int     `json:"template_id" db:"template_id"`
	SlotIndex         int     `json:"slot_index" db:"slot_index"`
	ProductID         int64   `json:"product_id" db:"product_id"`
	ProductCode       string  `json:"product_code,omitempty" db:"product_code"`
	ProductType       string  `json:"product_type,omitempty" db:"product_type"` // 'single', 'whole_book', 'vip'
	Type              string  `json:"type" db:"type"`                           // 'single', 'subscription', 'whole_book'
	Name              string  `json:"name,omitempty" db:"name"`
	Description       string  `json:"description,omitempty" db:"description"`
	Coins             int     `json:"coins" db:"coins"`
	Bonus             int     `json:"bonus" db:"bonus"`
	VipDuration       string  `json:"vip_duration" db:"vip_duration"`
	SubscriptionCycle string  `json:"subscription_cycle" db:"subscription_cycle"` // 'day', 'week', 'month', 'year'
	VipName           string  `json:"vip_name" db:"vip_name"`
	VipDesc           string  `json:"vip_desc" db:"vip_desc"`
	Price             string  `json:"price" db:"price"`
	Currency          string  `json:"currency,omitempty" db:"currency"`
	PriceCents        int     `json:"price_cents" db:"price_cents"`
	Badge             string  `json:"badge" db:"badge"`
	IsHighlighted     bool    `json:"is_highlighted" db:"is_highlighted"`
	DisplayAlias      string  `json:"display_alias" db:"display_alias"`
	NovelScopeType    string  `json:"novel_scope_type,omitempty" db:"novel_scope_type"`
	NovelScopeIDs     []int64 `json:"novel_scope_ids,omitempty" db:"novel_scope_ids"`
	PayPalReady       bool    `json:"paypal_ready" db:"-"`
	StripeReady       bool    `json:"stripe_ready" db:"-"`
}

// PaymentProviderPlan maps a recharge slot or pricing rule to a third-party payment provider plan ID (e.g. PayPal P-XXXXX, Stripe price_XXXXX)
type PaymentProviderPlan struct {
	ID             int64     `json:"id" db:"id"`
	Provider       string    `json:"provider" db:"provider"`
	SlotID         int       `json:"slot_id" db:"slot_id"`
	Cycle          string    `json:"cycle" db:"cycle"`
	PriceCents     int       `json:"price_cents" db:"price_cents"`
	Currency       string    `json:"currency" db:"currency"`
	ExternalPlanID string    `json:"external_plan_id" db:"external_plan_id"`
	Status         string    `json:"status" db:"status"`
	RawPayload     string    `json:"raw_payload,omitempty" db:"raw_payload"`
	CreatedAt      time.Time `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time `json:"updated_at" db:"updated_at"`
}

// RechargeTemplate represents recharge template settings
type RechargeTemplate struct {
	ID        int            `json:"id" db:"id"`
	Name      string         `json:"name" db:"name"`
	IsDefault bool           `json:"is_default" db:"is_default"`
	Slots     []RechargeSlot `json:"slots" db:"-"`
}

// SubscriptionCapabilities describes server-authorized self-service actions for a subscription.
type SubscriptionCapabilities struct {
	CanCancelAutoRenew    bool   `json:"can_cancel_auto_renew"`
	CancelPending         bool   `json:"cancel_pending"`
	CanOpenBillingPortal  bool   `json:"can_open_billing_portal"`
	BillingPortalEndpoint string `json:"billing_portal_endpoint,omitempty"`
	CanOpenMoRLinkPortal  bool   `json:"can_open_mor_link_portal"`
	MoRLinkURL            string `json:"mor_link_url,omitempty"`
}

// UserSubscription represents recurring subscription state and membership status
type UserSubscription struct {
	ID                 int64                     `json:"id" db:"id"`
	UserID             int64                     `json:"user_id" db:"user_id"`
	SubscriptionID     string                    `json:"subscription_id" db:"subscription_id"` // Provider Sub ID (e.g. I-XXXXX)
	ProductID          *int64                    `json:"product_id,omitempty" db:"product_id"`
	PlanID             string                    `json:"plan_id" db:"plan_id"`
	PlanName           string                    `json:"plan_name,omitempty"`
	SlotID             int                       `json:"slot_id" db:"slot_id"`
	TemplateID         int                       `json:"template_id" db:"template_id"`
	Status             string                    `json:"status" db:"status"` // 'PENDING', 'ACTIVE', 'CANCELLED', 'SUSPENDED', 'EXPIRED'
	NormalizedStatus   string                    `json:"normalized_status,omitempty"`
	Cycle              string                    `json:"cycle" db:"cycle"` // 'day', 'week', 'month'
	PriceCents         int                       `json:"price_cents" db:"price_cents"`
	Currency           string                    `json:"currency" db:"currency"`
	PaymentMethod      string                    `json:"payment_method" db:"payment_method"`
	Provider           string                    `json:"provider,omitempty"`
	SalesMode          string                    `json:"sales_mode,omitempty"`
	Management         string                    `json:"management,omitempty"`
	Environment        string                    `json:"-"`
	RenewalFailedAt    *time.Time                `json:"renewal_failed_at,omitempty"`
	CurrentPeriodStart *time.Time                `json:"current_period_start" db:"current_period_start"`
	CurrentPeriodEnd   *time.Time                `json:"current_period_end" db:"current_period_end"`
	PaidThrough        *time.Time                `json:"paid_through,omitempty"`
	NextBillingTime    *time.Time                `json:"next_billing_time" db:"next_billing_time"`
	NextBillingAt      *time.Time                `json:"next_billing_at,omitempty"`
	LastPaymentTime    *time.Time                `json:"last_payment_time" db:"last_payment_time"`
	CancelledAt        *time.Time                `json:"cancelled_at" db:"cancelled_at"`
	Capabilities       *SubscriptionCapabilities `json:"capabilities,omitempty"`
	RawPayload         string                    `json:"raw_payload,omitempty" db:"raw_payload"`
	CreatedAt          time.Time                 `json:"created_at" db:"created_at"`
	UpdatedAt          time.Time                 `json:"updated_at" db:"updated_at"`
}

// ThirdPartyPaymentOrder represents third-party payment transaction details (e.g. PayPal, Stripe)
type ThirdPartyPaymentOrder struct {
	ID                     int64     `json:"id" db:"id"`
	OrderID                int64     `json:"order_id" db:"order_id"`
	PaymentProvider        string    `json:"payment_provider" db:"payment_provider"`
	ExternalOrderID        string    `json:"external_order_id" db:"external_order_id"`
	CaptureID              string    `json:"capture_id" db:"capture_id"`
	PayerID                string    `json:"payer_id" db:"payer_id"`
	PayerEmail             string    `json:"payer_email" db:"payer_email"`
	PayerName              string    `json:"payer_name" db:"payer_name"`
	PayerCountry           string    `json:"payer_country" db:"payer_country"`
	Currency               string    `json:"currency" db:"currency"`
	GrossAmount            float64   `json:"gross_amount" db:"gross_amount"`
	FeeAmount              float64   `json:"fee_amount" db:"fee_amount"`
	NetAmount              float64   `json:"net_amount" db:"net_amount"`
	Status                 string    `json:"status" db:"status"`
	SellerProtectionStatus string    `json:"seller_protection_status" db:"seller_protection_status"`
	RawPayload             string    `json:"raw_payload" db:"raw_payload"`
	CreatedAt              time.Time `json:"created_at" db:"created_at"`
	UpdatedAt              time.Time `json:"updated_at" db:"updated_at"`
}
