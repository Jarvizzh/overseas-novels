package operations

import "time"

const (
	GroupEditorPick = "editor_pick"
	GroupCurated    = "curated"
	GroupRanking    = "ranking"

	RankingSortViews  = "views"
	RankingSortRating = "rating"
	RankingSortLatest = "latest"
	RankingSortManual = "manual"

	configKeyRankingSortRule = "ranking_sort_rule"
	configKeyRankingLimit    = "ranking_limit"
	configKeyCheckInGradient = "checkin_reward_gradient"
)

type CheckInConfig struct {
	Rewards []int `json:"rewards"`
}

type RankingConfig struct {
	SortRule string `json:"sort_rule"`
	Limit    int    `json:"limit"`
}

type RecommendationNovel struct {
	ID            int64    `json:"id"`
	Title         string   `json:"title"`
	Author        string   `json:"author"`
	CoverURL      string   `json:"cover_url"`
	Rating        float64  `json:"rating"`
	Status        string   `json:"status"`
	PublishStatus string   `json:"publish_status"`
	Synopsis      string   `json:"synopsis"`
	ViewCount     int64    `json:"view_count"`
	Genres        []string `json:"genres"`
}

type RecommendationEntry struct {
	ID             int64                `json:"id"`
	GroupName      string               `json:"group_name"`
	NovelID        int64                `json:"novel_id"`
	SortOrder      int                  `json:"sort_order"`
	ScheduledStart *time.Time           `json:"scheduled_start,omitempty"`
	ScheduledEnd   *time.Time           `json:"scheduled_end,omitempty"`
	CreatedAt      time.Time            `json:"created_at"`
	Novel          *RecommendationNovel `json:"novel,omitempty"`
}

type OperationsConfigResponse struct {
	RankingConfig   RankingConfig                    `json:"ranking_config"`
	CheckInConfig   CheckInConfig                    `json:"checkin_config"`
	Groups          map[string][]RecommendationEntry `json:"groups"`
	AvailableGenres []string                         `json:"available_genres"`
}

type GroupItemInput struct {
	NovelID        int64      `json:"novel_id"`
	SortOrder      int        `json:"sort_order"`
	ScheduledStart *time.Time `json:"scheduled_start"`
	ScheduledEnd   *time.Time `json:"scheduled_end"`
}

type SaveGroupRequest struct {
	Items []GroupItemInput `json:"items"`
}

type SaveRankingConfigRequest struct {
	SortRule string `json:"sort_rule"`
	Limit    int    `json:"limit"`
}

type SaveRankingRequest struct {
	RankingConfig *SaveRankingConfigRequest `json:"ranking_config,omitempty"`
	Items         *[]GroupItemInput         `json:"items,omitempty"`
}

type SaveRankingResponse struct {
	RankingConfig RankingConfig         `json:"ranking_config"`
	Items         []RecommendationEntry `json:"items"`
}

type PreviewRequest struct {
	At            *time.Time                  `json:"at,omitempty"`
	RankingConfig *SaveRankingConfigRequest   `json:"ranking_config,omitempty"`
	Groups        map[string][]GroupItemInput `json:"groups,omitempty"`
}

type PreviewNovelItem struct {
	Novel  RecommendationNovel `json:"novel"`
	Source string              `json:"source"` // "pinned" | "auto" | "fallback"
}

type PreviewResponse struct {
	At                    time.Time          `json:"at"`
	RankingConfig         RankingConfig      `json:"ranking_config"`
	Featured              []PreviewNovelItem `json:"featured"`
	FeaturedIsFallback    bool               `json:"featured_is_fallback"`
	NewReleases           []PreviewNovelItem `json:"new_releases"`
	Curated               []PreviewNovelItem `json:"curated"`
	CuratedIsOverride     bool               `json:"curated_is_override"`
	Rankings              []PreviewNovelItem `json:"rankings"`
	RankingManualFallback bool               `json:"ranking_manual_fallback"`
}

type NovelOption struct {
	ID            int64    `json:"id"`
	Title         string   `json:"title"`
	Author        string   `json:"author"`
	CoverURL      string   `json:"cover_url"`
	Rating        float64  `json:"rating"`
	Status        string   `json:"status"`
	PublishStatus string   `json:"publish_status"`
	ViewCount     int64    `json:"view_count"`
	Genres        []string `json:"genres"`
}

type NovelOptionsFilter struct {
	Search string
	Author string
	Genre  string
	Status string
	Sort   string
}

type NovelOptionsResponse struct {
	Novels   []NovelOption `json:"novels"`
	Total    int           `json:"total"`
	Page     int           `json:"page"`
	PageSize int           `json:"page_size"`
}
