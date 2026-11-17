package operations

import (
	"context"
	"errors"
	"strings"

	"star-novel-cms/internal/storage"
)

var (
	ErrInvalidGroup          = errors.New("invalid recommendation group")
	ErrInvalidRankingSort    = errors.New("invalid ranking sort_rule; expected views, rating, latest, or manual")
	ErrInvalidRankingLimit   = errors.New("ranking limit must be between 1 and 100")
	ErrDuplicateNovelInGroup = errors.New("duplicate novel_id in group")
	ErrInvalidNovelID        = errors.New("novel_id must be a positive integer")
	ErrInvalidScheduleRange  = errors.New("scheduled_end must be after scheduled_start")
	ErrNovelNotFound         = errors.New("novel not found or archived")
)

type Service interface {
	GetConfig(ctx context.Context) (*OperationsConfigResponse, error)
	SaveRankingConfig(ctx context.Context, req SaveRankingConfigRequest) (RankingConfig, error)
	SaveGroup(ctx context.Context, groupName string, items []GroupItemInput) ([]RecommendationEntry, error)
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

func (s *service) signEntryCovers(entries []RecommendationEntry) {
	if s.coverStore == nil {
		return
	}
	for i := range entries {
		if entries[i].Novel != nil {
			entries[i].Novel.CoverURL = s.coverStore.SignCoverURL(entries[i].Novel.CoverURL)
		}
	}
}

func (s *service) GetConfig(ctx context.Context) (*OperationsConfigResponse, error) {
	rankingCfg, err := s.repo.GetRankingConfig(ctx)
	if err != nil {
		return nil, err
	}
	entries, err := s.repo.ListRecommendations(ctx)
	if err != nil {
		return nil, err
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
		RankingConfig: rankingCfg,
		Groups:        groups,
	}, nil
}

func (s *service) SaveRankingConfig(ctx context.Context, req SaveRankingConfigRequest) (RankingConfig, error) {
	rule := strings.TrimSpace(req.SortRule)
	if !isValidRankingSortRule(rule) {
		return RankingConfig{}, ErrInvalidRankingSort
	}
	if req.Limit < 1 || req.Limit > 100 {
		return RankingConfig{}, ErrInvalidRankingLimit
	}
	cfg := RankingConfig{
		SortRule: rule,
		Limit:    req.Limit,
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
