package shelf

import (
	"context"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"

	"reader-backend/internal/model"
)

type shelfCursor struct {
	activityAt time.Time
	novelID    int64
}

type ShelfPage struct {
	Items      []model.BookshelfWithNovel `json:"items"`
	NextCursor string                     `json:"next_cursor,omitempty"`
}

func parseShelfCursor(raw string) (*shelfCursor, error) {
	if raw == "" {
		return nil, nil
	}
	invalid := errors.New("invalid shelf cursor")
	if len(raw) > 128 {
		return nil, invalid
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, invalid
	}
	stamp, novel, ok := strings.Cut(string(decoded), "|")
	if !ok {
		return nil, invalid
	}
	at, timeErr := time.Parse(time.RFC3339Nano, stamp)
	id, idErr := strconv.ParseInt(novel, 10, 32)
	if timeErr != nil || idErr != nil || id <= 0 {
		return nil, invalid
	}
	return &shelfCursor{activityAt: at, novelID: id}, nil
}

func (s *service) GetShelfPage(ctx context.Context, userID int64, limit int, after *shelfCursor) (*ShelfPage, error) {
	items, err := s.repo.GetShelfPage(ctx, userID, limit+1, after)
	if err != nil {
		return nil, err
	}
	page := &ShelfPage{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		last := page.Items[limit-1]
		key := last.ActivityAt.UTC().Format(time.RFC3339Nano) + "|" + strconv.FormatInt(last.NovelID, 10)
		page.NextCursor = base64.RawURLEncoding.EncodeToString([]byte(key))
	}
	s.signShelfCovers(page.Items)
	return page, nil
}

func (s *service) GetShelfIDs(ctx context.Context, userID int64) ([]int64, error) {
	return s.repo.GetShelfIDs(ctx, userID)
}
