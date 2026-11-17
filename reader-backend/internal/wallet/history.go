package wallet

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"reader-backend/internal/model"
)

type historyCursor struct {
	createdAt time.Time
	id        string
}

type HistoryPage struct {
	Items      []model.Transaction `json:"items"`
	NextCursor string              `json:"next_cursor,omitempty"`
}

func parseHistoryCursor(raw string) (*historyCursor, error) {
	if raw == "" {
		return nil, nil
	}
	invalid := errors.New("invalid history cursor")
	if len(raw) > 256 {
		return nil, invalid
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, invalid
	}
	rawTime, id, ok := strings.Cut(string(decoded), "|")
	if !ok || id == "" || !utf8.ValidString(id) || utf8.RuneCountInString(id) > 36 || strings.ContainsRune(id, 0) {
		return nil, invalid
	}
	createdAt, err := time.Parse(time.RFC3339Nano, rawTime)
	if err != nil {
		return nil, invalid
	}
	return &historyCursor{createdAt: createdAt, id: id}, nil
}

func (s *service) GetHistoryPage(ctx context.Context, userID int64, limit int, after *historyCursor) (*HistoryPage, error) {
	items, err := s.repo.getTransactionsAfter(ctx, userID, limit+1, after)
	if err != nil {
		return nil, err
	}
	page := &HistoryPage{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		last := page.Items[limit-1]
		page.NextCursor = base64.RawURLEncoding.EncodeToString([]byte(last.CreatedAt.UTC().Format(time.RFC3339Nano) + "|" + last.ID))
	}
	return page, nil
}
