package feedback

import "context"

type Service interface {
	SubmitFeedback(ctx context.Context, userID int64, req *CreateFeedbackRequest) (int64, error)
}

type service struct {
	repo Repository
}

func NewService(repo Repository) Service {
	return &service{repo: repo}
}

func (s *service) SubmitFeedback(ctx context.Context, userID int64, req *CreateFeedbackRequest) (int64, error) {
	fb := &Feedback{
		UserID:  userID,
		Email:   req.Email,
		Subject: req.Subject,
		Content: req.Content,
	}
	return s.repo.CreateFeedback(ctx, fb)
}
