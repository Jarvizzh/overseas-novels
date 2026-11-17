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
)

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
	RankingConfig RankingConfig                    `json:"ranking_config"`
	Groups        map[string][]RecommendationEntry `json:"groups"`
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
