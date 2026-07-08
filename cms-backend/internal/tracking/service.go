package tracking

import "context"

type Service interface {
	GetLogs(ctx context.Context, page, pageSize int, pixelID, eventName string, statusCode string) ([]*FacebookCAPILog, int, error)
}

type service struct {
	repo Repository
}

func NewService(repo Repository) Service {
	return &service{repo: repo}
}

func (s *service) GetLogs(ctx context.Context, page, pageSize int, pixelID, eventName string, statusCode string) ([]*FacebookCAPILog, int, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 10
	}
	return s.repo.ListLogs(ctx, page, pageSize, pixelID, eventName, statusCode)
}
