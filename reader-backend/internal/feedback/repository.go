package feedback

import (
	"context"

	"reader-backend/internal/db"
)

type Repository interface {
	CreateFeedback(ctx context.Context, fb *Feedback) (int64, error)
}

type repository struct{}

func NewRepository() Repository {
	return &repository{}
}

func (r *repository) CreateFeedback(ctx context.Context, fb *Feedback) (int64, error) {
	query := `
		INSERT INTO feedback (user_id, email, subject, content, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, 'pending', NOW(), NOW())
		RETURNING id
	`
	var id int64
	err := db.DB.QueryRow(ctx, query, fb.UserID, fb.Email, fb.Subject, fb.Content).Scan(&id)
	if err != nil {
		return 0, err
	}
	return id, nil
}
