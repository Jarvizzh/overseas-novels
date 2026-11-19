package novel

import "time"

type Novel struct {
	ContentVersion               int64     `json:"content_version"`
	ID                           int64     `json:"id"`
	Title                        string    `json:"title"`
	Author                       string    `json:"author"`
	CoverURL                     string    `json:"cover_url"`
	CoverKey                     string    `json:"cover_key,omitempty"`
	Rating                       float64   `json:"rating"`
	Status                       string    `json:"status"`         // Ongoing, Completed, archived
	PublishStatus                string    `json:"publish_status"` // draft, published, offline
	Synopsis                     string    `json:"synopsis"`
	Genres                       []string  `json:"genres"`
	WordCount                    int       `json:"word_count"`
	ViewCount                    int64     `json:"view_count"`
	CoinCostPerThousand          *int      `json:"coin_cost_per_thousand"`
	EffectiveCoinCostPerThousand int       `json:"effective_coin_cost_per_thousand"`
	StartPayChapterIndex         int       `json:"start_pay_chapter_index"`
	CreatedAt                    time.Time `json:"created_at"`
	UpdatedAt                    time.Time `json:"updated_at"`
}

type Chapter struct {
	Preview      string    `json:"-"`
	ObjectKey    string    `json:"-"`
	ContentHash  string    `json:"-"`
	ID           string    `json:"id"`
	NovelID      int64     `json:"novel_id"`
	ChapterIndex int       `json:"chapter_index"`
	Title        string    `json:"title"`
	Content      string    `json:"content"`
	WordCount    int       `json:"word_count"`
	IsPaid       bool      `json:"is_paid"`
	Price        int       `json:"price"`
	CreatedAt    time.Time `json:"created_at"`
}

type PromotionLink struct {
	ID                   int       `json:"id"`
	Name                 string    `json:"name"`
	NovelID              int64     `json:"novel_id"`
	NovelTitle           string    `json:"novel_title"`
	ChapterIndex         int       `json:"chapter_index"`
	UtmSource            string    `json:"utm_source"`
	UtmCampaign          string    `json:"utm_campaign"`
	GeneratedURL         string    `json:"generated_url"`
	FBPixelID            *int      `json:"fb_pixel_id"`
	RechargeTemplateID   *int      `json:"recharge_template_id"`
	DomainID             *int      `json:"domain_id"`
	Domain               string    `json:"domain"`
	CoinCostPerThousand  *int      `json:"coin_cost_per_thousand"`
	StartPayChapterIndex *int      `json:"start_pay_chapter_index"`
	CreatedAt            time.Time `json:"created_at"`
}

type FBPixel struct {
	ID        int       `json:"id"`
	Name      string    `json:"name"`
	PixelID   string    `json:"pixel_id"`
	CreatedAt time.Time `json:"created_at"`
}

type SystemConfig struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	Desc  string `json:"desc"`
}
