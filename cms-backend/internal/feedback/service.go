package feedback

import "context"

type Service interface {
	List(ctx context.Context, page, limit int, status, keyword string) ([]Feedback, int, error)
	GetByID(ctx context.Context, id int64) (*Feedback, error)
	Update(ctx context.Context, id int64, req *UpdateFeedbackRequest) error
}

type service struct {
	repo Repository
}

func NewService(repo Repository) Service {
	return &service{repo: repo}
}

func (s *service) List(ctx context.Context, page, limit int, status, keyword string) ([]Feedback, int, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	return s.repo.List(ctx, page, limit, status, keyword)
}

func (s *service) GetByID(ctx context.Context, id int64) (*Feedback, error) {
	return s.repo.GetByID(ctx, id)
}

func (s *service) Update(ctx context.Context, id int64, req *UpdateFeedbackRequest) error {
	return s.repo.Update(ctx, id, req.Status, req.AdminReply)
}
