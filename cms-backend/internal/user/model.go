package user

import "time"

type User struct {
	ID               int64      `json:"id"`
	Email            string     `json:"email"`
	Nickname         string     `json:"nickname"`
	AvatarURL        string     `json:"avatar_url"`
	Status           int16      `json:"status"` // 1-Normal, 2-Banned
	Device           string     `json:"device"`
	IPAddress        string     `json:"ip_address"`
	UTMSource        string     `json:"utm_source"`
	UTMCampaign      string     `json:"utm_campaign"`
	TotalRecharge    int        `json:"total_recharge"`
	Balance          int        `json:"balance"`
	TotalSpent       int        `json:"total_spent"`
	RecentlyReadBook string     `json:"recently_read_book"`
	IsVIP            bool       `json:"is_vip"`
	VIPCycle         string     `json:"vip_cycle"`
	VIPExpireAt      *time.Time `json:"vip_expire_at"`
	VIPStatus        string     `json:"vip_status"`
	CreatedAt        time.Time  `json:"created_at"`
}

type UserBookshelfItem struct {
	NovelID                int64     `json:"novel_id"`
	NovelTitle             string    `json:"novel_title"`
	CoverURL               string    `json:"cover_url"`
	ChapterIndex           int       `json:"chapter_index"`
	ScrollOffsetPercentage float64   `json:"scroll_offset_percentage"`
	InShelf                bool      `json:"in_shelf"`
	UpdatedAt              time.Time `json:"updated_at"`
}

type UserSubscriptionItem struct {
	ID                 int64      `json:"id"`
	SubscriptionID     string     `json:"subscription_id"`
	PlanID             string     `json:"plan_id"`
	Status             string     `json:"status"` // ACTIVE, CANCELLED, EXPIRED, PENDING, SUSPENDED
	Cycle              string     `json:"cycle"`  // day, week, month
	PriceCents         int        `json:"price_cents"`
	Currency           string     `json:"currency"`
	PaymentMethod      string     `json:"payment_method"`
	CurrentPeriodStart *time.Time `json:"current_period_start"`
	CurrentPeriodEnd   *time.Time `json:"current_period_end"`
	NextBillingTime    *time.Time `json:"next_billing_time"`
	LastPaymentTime    *time.Time `json:"last_payment_time"`
	CancelledAt        *time.Time `json:"cancelled_at"`
	CreatedAt          time.Time  `json:"created_at"`
}

type UserDetail struct {
	User
	ChargedCoins       int                    `json:"charged_coins"`
	BonusCoins         int                    `json:"bonus_coins"`
	Bookshelf          []UserBookshelfItem    `json:"bookshelf"`
	Subscriptions      []UserSubscriptionItem `json:"subscriptions"`
	ActiveSubscription *UserSubscriptionItem  `json:"active_subscription,omitempty"`
}
