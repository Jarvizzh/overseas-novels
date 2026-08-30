package feedback

import "time"

type Feedback struct {
	ID         int64     `json:"id"`
	UserID     int64     `json:"user_id"`
	Email      string    `json:"email"`
	Subject    string    `json:"subject"`
	Content    string    `json:"content"`
	Status     string    `json:"status"` // pending, replied, resolved
	AdminReply string    `json:"admin_reply"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type CreateFeedbackRequest struct {
	Email   string `json:"email" binding:"required,email"`
	Subject string `json:"subject"`
	Content string `json:"content" binding:"required,min=5,max=2000"`
}
