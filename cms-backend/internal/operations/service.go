package operations

import (
	"context"
	"errors"
	"strings"
	"time"

	"star-novel-cms/internal/storage"
)

var (
	ErrInvalidGroup              = errors.New("invalid recommendation group")
	ErrInvalidRankingSort        = errors.New("invalid ranking sort_rule; expected views, rating, latest, or manual")
	ErrInvalidRankingLimit       = errors.New("ranking limit must be between 1 and 100")
	ErrDuplicateNovelInGroup     = errors.New("duplicate novel_id in group")
	ErrInvalidNovelID            = errors.New("novel_id must be a positive integer")
	ErrInvalidScheduleRange      = errors.New("scheduled_end must be after scheduled_start")
	ErrNovelNotFound             = errors.New("novel not found or archived")
	ErrEmptyRankingUpdate        = errors.New("at least one of ranking_config or items is required")
	ErrInvalidCheckInRewards     = errors.New("checkin rewards must contain 7 integers between 1 and 10000")
	ErrInvalidNovelOptionsFilter = errors.New("invalid novel options filter")
)

type Service interface {
	GetConfig(ctx context.Context) (*OperationsConfigResponse, error)
	SaveRankingConfig(ctx context.Context, req SaveRankingConfigRequest) (RankingConfig, error)
	SaveGroup(ctx context.Context, groupName string, items []GroupItemInput) ([]RecommendationEntry, error)
	SaveRanking(ctx context.Context, req SaveRankingRequest) (*SaveRankingResponse, error)
	SaveCheckInConfig(ctx context.Context, req CheckInConfig) (CheckInConfig, error)
	Preview(ctx context.Context, req PreviewRequest) (*PreviewResponse, error)
	ListNovelOptions(ctx context.Context, page, pageSize int, filter NovelOptionsFilter) (*NovelOptionsResponse, error)
}

type service struct {
	repo       Repository
	coverStore *storage.CoverOSSStore
}

func NewService(repo Repository, coverStore *storage.CoverOSSStore) Service {
	return &service{
		repo:       repo,
		coverStore: coverStore,
	}
}

func isValidGroup(groupName string) bool {
	return groupName == GroupEditorPick || groupName == GroupCurated || groupName == GroupRanking
}

func isValidRankingSortRule(rule string) bool {
	return rule == RankingSortViews || rule == RankingSortRating || rule == RankingSortLatest || rule == RankingSortManual
}

func validateRankingConfig(req SaveRankingConfigRequest) (RankingConfig, error) {
	rule := strings.TrimSpace(req.SortRule)
	if !isValidRankingSortRule(rule) {
		return RankingConfig{}, ErrInvalidRankingSort
	}
	if req.Limit < 1 || req.Limit > 100 {
		return RankingConfig{}, ErrInvalidRankingLimit
	}
	return RankingConfig{
		SortRule: rule,
		Limit:    req.Limit,
	}, nil
}

func validateAndNormalizeGroupItems(items []GroupItemInput) ([]GroupItemInput, error) {
	if len(items) > 100 {
		return nil, ErrInvalidRankingLimit
	}
	seen := make(map[int64]bool, len(items))
	normalized := make([]GroupItemInput, len(items))
	for i, item := range items {
		if item.NovelID <= 0 {
			return nil, ErrInvalidNovelID
		}
		if seen[item.NovelID] {
			return nil, ErrDuplicateNovelInGroup
		}
		if item.ScheduledStart != nil && item.ScheduledEnd != nil && item.ScheduledEnd.Before(*item.ScheduledStart) {
			return nil, ErrInvalidScheduleRange
		}
		seen[item.NovelID] = true
		normalized[i] = GroupItemInput{
			NovelID:        item.NovelID,
			SortOrder:      i,
			ScheduledStart: item.ScheduledStart,
			ScheduledEnd:   item.ScheduledEnd,
		}
	}
	return normalized, nil
}

func (s *service) signNovelCover(n *RecommendationNovel) {
	if s.coverStore == nil || n == nil {
		return
	}
	n.CoverURL = s.coverStore.SignCoverURL(n.CoverURL)
}

func (s *service) signEntryCovers(entries []RecommendationEntry) {
	if s.coverStore == nil {
		return
	}
	for i := range entries {
		s.signNovelCover(entries[i].Novel)
	}
}

func (s *service) signPreviewCovers(items []PreviewNovelItem) {
	if s.coverStore == nil {
		return
	}
	for i := range items {
		s.signNovelCover(&items[i].Novel)
	}
}

func (s *service) GetConfig(ctx context.Context) (*OperationsConfigResponse, error) {
	rankingCfg, err := s.repo.GetRankingConfig(ctx)
	if err != nil {
		return nil, err
	}
	checkInCfg, err := s.repo.GetCheckInConfig(ctx)
	if err != nil {
		return nil, err
	}
	entries, err := s.repo.ListRecommendations(ctx)
	if err != nil {
		return nil, err
	}
	genres, err := s.repo.ListPublishedGenres(ctx)
	if err != nil {
		return nil, err
	}
	if genres == nil {
		genres = []string{}
	}
	s.signEntryCovers(entries)

	groups := map[string][]RecommendationEntry{
		GroupEditorPick: {},
		GroupCurated:    {},
		GroupRanking:    {},
	}
	for _, entry := range entries {
		if _, exists := groups[entry.GroupName]; exists {
			groups[entry.GroupName] = append(groups[entry.GroupName], entry)
		}
	}

	return &OperationsConfigResponse{
		RankingConfig:   rankingCfg,
		CheckInConfig:   checkInCfg,
		Groups:          groups,
		AvailableGenres: genres,
	}, nil
}

func (s *service) SaveRankingConfig(ctx context.Context, req SaveRankingConfigRequest) (RankingConfig, error) {
	cfg, err := validateRankingConfig(req)
	if err != nil {
		return RankingConfig{}, err
	}
	if err := s.repo.SaveRankingConfig(ctx, cfg); err != nil {
		return RankingConfig{}, err
	}
	return cfg, nil
}

func (s *service) SaveGroup(ctx context.Context, groupName string, items []GroupItemInput) ([]RecommendationEntry, error) {
	group := strings.TrimSpace(groupName)
	if !isValidGroup(group) {
		return nil, ErrInvalidGroup
	}
	normalized, err := validateAndNormalizeGroupItems(items)
	if err != nil {
		return nil, err
	}

	if err := s.repo.SaveGroup(ctx, group, normalized); err != nil {
		return nil, err
	}
	saved, err := s.repo.ListGroup(ctx, group)
	if err != nil {
		return nil, err
	}
	s.signEntryCovers(saved)
	return saved, nil
}

func (s *service) SaveRanking(ctx context.Context, req SaveRankingRequest) (*SaveRankingResponse, error) {
	if req.RankingConfig == nil && req.Items == nil {
		return nil, ErrEmptyRankingUpdate
	}

	var cfgPtr *RankingConfig
	if req.RankingConfig != nil {
		cfg, err := validateRankingConfig(*req.RankingConfig)
		if err != nil {
			return nil, err
		}
		cfgPtr = &cfg
	}

	var itemsPtr *[]GroupItemInput
	if req.Items != nil {
		normalized, err := validateAndNormalizeGroupItems(*req.Items)
		if err != nil {
			return nil, err
		}
		itemsPtr = &normalized
	}

	if err := s.repo.SaveRanking(ctx, cfgPtr, itemsPtr); err != nil {
		return nil, err
	}

	freshCfg, err := s.repo.GetRankingConfig(ctx)
	if err != nil {
		return nil, err
	}
	freshItems, err := s.repo.ListGroup(ctx, GroupRanking)
	if err != nil {
		return nil, err
	}
	s.signEntryCovers(freshItems)

	return &SaveRankingResponse{
		RankingConfig: freshCfg,
		Items:         freshItems,
	}, nil
}

func (s *service) SaveCheckInConfig(ctx context.Context, req CheckInConfig) (CheckInConfig, error) {
	if len(req.Rewards) != 7 {
		return CheckInConfig{}, ErrInvalidCheckInRewards
	}
	normalized := make([]int, 7)
	for i, reward := range req.Rewards {
		if reward < 1 || reward > 10000 {
			return CheckInConfig{}, ErrInvalidCheckInRewards
		}
		normalized[i] = reward
	}
	cfg := CheckInConfig{Rewards: normalized}
	if err := s.repo.SaveCheckInConfig(ctx, cfg); err != nil {
		return CheckInConfig{}, err
	}
	return cfg, nil
}

func isScheduleActiveAt(at time.Time, start, end *time.Time) bool {
	if start != nil && at.Before(*start) {
		return false
	}
	if end != nil && at.After(*end) {
		return false
	}
	return true
}

func wrapPreviewItems(novels []RecommendationNovel, source string) []PreviewNovelItem {
	out := make([]PreviewNovelItem, 0, len(novels))
	for _, n := range novels {
		out = append(out, PreviewNovelItem{Novel: n, Source: source})
	}
	return out
}

func (s *service) resolveActivePreviewGroups(
	ctx context.Context,
	at time.Time,
	draftGroups map[string][]GroupItemInput,
) (map[string][]RecommendationNovel, error) {
	normalizedDrafts := make(map[string][]GroupItemInput, len(draftGroups))
	draftIDSet := make(map[int64]bool)
	for rawName, items := range draftGroups {
		name := strings.TrimSpace(rawName)
		if !isValidGroup(name) {
			return nil, ErrInvalidGroup
		}
		norm, err := validateAndNormalizeGroupItems(items)
		if err != nil {
			return nil, err
		}
		normalizedDrafts[name] = norm
		for _, it := range norm {
			draftIDSet[it.NovelID] = true
		}
	}

	allGroups := []string{GroupEditorPick, GroupCurated, GroupRanking}
	needDB := false
	for _, g := range allGroups {
		if _, ok := normalizedDrafts[g]; !ok {
			needDB = true
			break
		}
	}

	dbByGroup := make(map[string][]RecommendationEntry)
	if needDB {
		entries, err := s.repo.ListRecommendations(ctx)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			dbByGroup[entry.GroupName] = append(dbByGroup[entry.GroupName], entry)
		}
	}

	publishedMap := make(map[int64]RecommendationNovel)
	if len(draftIDSet) > 0 {
		ids := make([]int64, 0, len(draftIDSet))
		for id := range draftIDSet {
			ids = append(ids, id)
		}
		var err error
		publishedMap, err = s.repo.GetNovelsByIDs(ctx, ids, true)
		if err != nil {
			return nil, err
		}
	}

	resolved := make(map[string][]RecommendationNovel, len(allGroups))
	for _, g := range allGroups {
		if items, hasDraft := normalizedDrafts[g]; hasDraft {
			active := make([]RecommendationNovel, 0, len(items))
			for _, it := range items {
				if !isScheduleActiveAt(at, it.ScheduledStart, it.ScheduledEnd) {
					continue
				}
				if n, ok := publishedMap[it.NovelID]; ok {
					active = append(active, n)
				}
			}
			resolved[g] = active
			continue
		}

		dbItems := dbByGroup[g]
		active := make([]RecommendationNovel, 0, len(dbItems))
		for _, entry := range dbItems {
			if entry.Novel == nil || entry.Novel.PublishStatus != "published" || entry.Novel.Status == "archived" {
				continue
			}
			if !isScheduleActiveAt(at, entry.ScheduledStart, entry.ScheduledEnd) {
				continue
			}
			active = append(active, *entry.Novel)
		}
		resolved[g] = active
	}
	return resolved, nil
}

func (s *service) Preview(ctx context.Context, req PreviewRequest) (*PreviewResponse, error) {
	at := time.Now().UTC()
	if req.At != nil {
		at = req.At.UTC()
	}

	var rankingCfg RankingConfig
	if req.RankingConfig != nil {
		var err error
		rankingCfg, err = validateRankingConfig(*req.RankingConfig)
		if err != nil {
			return nil, err
		}
	} else {
		var err error
		rankingCfg, err = s.repo.GetRankingConfig(ctx)
		if err != nil {
			return nil, err
		}
	}

	activeByGroup, err := s.resolveActivePreviewGroups(ctx, at, req.Groups)
	if err != nil {
		return nil, err
	}

	// 1. Featured Stories (editor_pick, fallback top 4 popular)
	featured := wrapPreviewItems(activeByGroup[GroupEditorPick], "pinned")
	featuredIsFallback := false
	if len(featured) == 0 {
		featuredIsFallback = true
		fallbackFeatured, err := s.repo.ListTopPublishedNovels(ctx, RankingSortViews, nil, 4)
		if err != nil {
			return nil, err
		}
		featured = wrapPreviewItems(fallbackFeatured, "fallback")
	}

	// 2. New Releases (auto top 6 latest published)
	latestNovels, err := s.repo.ListTopPublishedNovels(ctx, RankingSortLatest, nil, 6)
	if err != nil {
		return nil, err
	}
	newReleases := wrapPreviewItems(latestNovels, "auto")

	// 3. Curated For You (curated override, fallback catalog top 10 by views)
	curatedActive := activeByGroup[GroupCurated]
	curatedIsOverride := len(curatedActive) > 0
	var curated []PreviewNovelItem
	if curatedIsOverride {
		curated = wrapPreviewItems(curatedActive, "pinned")
	} else {
		fallbackCurated, err := s.repo.ListTopPublishedNovels(ctx, RankingSortViews, nil, 10)
		if err != nil {
			return nil, err
		}
		curated = wrapPreviewItems(fallbackCurated, "fallback")
	}

	// 4. Top Charts (ranking pinned up to limit + backfill)
	rankingPinned := activeByGroup[GroupRanking]
	if len(rankingPinned) > rankingCfg.Limit {
		rankingPinned = rankingPinned[:rankingCfg.Limit]
	}
	rankings := wrapPreviewItems(rankingPinned, "pinned")
	pinnedCount := len(rankings)
	rankingManualFallback := rankingCfg.SortRule == RankingSortManual && pinnedCount == 0

	if pinnedCount < rankingCfg.Limit && (rankingCfg.SortRule != RankingSortManual || pinnedCount == 0) {
		excludeIDs := make([]int64, 0, pinnedCount)
		for _, item := range rankings {
			excludeIDs = append(excludeIDs, item.Novel.ID)
		}
		backfillRule := rankingCfg.SortRule
		backfillSource := "auto"
		if rankingManualFallback {
			backfillRule = RankingSortViews
			backfillSource = "fallback"
		}
		backfilled, err := s.repo.ListTopPublishedNovels(ctx, backfillRule, excludeIDs, rankingCfg.Limit-pinnedCount)
		if err != nil {
			return nil, err
		}
		rankings = append(rankings, wrapPreviewItems(backfilled, backfillSource)...)
	}

	s.signPreviewCovers(featured)
	s.signPreviewCovers(newReleases)
	s.signPreviewCovers(curated)
	s.signPreviewCovers(rankings)

	return &PreviewResponse{
		At:                    at,
		RankingConfig:         rankingCfg,
		Featured:              featured,
		FeaturedIsFallback:    featuredIsFallback,
		NewReleases:           newReleases,
		Curated:               curated,
		CuratedIsOverride:     curatedIsOverride,
		Rankings:              rankings,
		RankingManualFallback: rankingManualFallback,
	}, nil
}

func (s *service) ListNovelOptions(ctx context.Context, page, pageSize int, filter NovelOptionsFilter) (*NovelOptionsResponse, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	} else if pageSize > 200 {
		pageSize = 200
	}
	filter.Status = strings.TrimSpace(filter.Status)
	if filter.Status != "" && filter.Status != "Ongoing" && filter.Status != "Completed" {
		return nil, ErrInvalidNovelOptionsFilter
	}
	filter.Sort = strings.TrimSpace(filter.Sort)
	switch filter.Sort {
	case "", "views", "rating", "latest", "updated":
	default:
		return nil, ErrInvalidNovelOptionsFilter
	}

	novels, total, err := s.repo.ListPublishedNovelOptions(ctx, page, pageSize, filter)
	if err != nil {
		return nil, err
	}
	if novels == nil {
		novels = []NovelOption{}
	}
	if s.coverStore != nil {
		for i := range novels {
			novels[i].CoverURL = s.coverStore.SignCoverURL(novels[i].CoverURL)
		}
	}
	return &NovelOptionsResponse{
		Novels:   novels,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	}, nil
}
