package feedback

import (
	"context"
	"strings"
)

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
	email := strings.TrimSpace(req.Email)
	if len(email) > 100 {
		email = email[:100]
	}
	subject := strings.TrimSpace(req.Subject)
	if subject == "" {
		subject = "Customer Support Inquiry"
	}
	if len(subject) > 150 {
		subject = subject[:150]
	}
	content := strings.TrimSpace(req.Content)

	fb := &Feedback{
		UserID:  userID,
		Email:   email,
		Subject: subject,
		Content: content,
	}
	return s.repo.CreateFeedback(ctx, fb)
}
