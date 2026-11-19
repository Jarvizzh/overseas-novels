package tracking

import "time"

type FacebookCAPILog struct {
	ID         string    `json:"id"`
	PixelID    string    `json:"pixel_id"`
	EventName  string    `json:"event_name"`
	UserID     *string   `json:"user_id"`
	Value      *float64  `json:"value"`
	Currency   *string   `json:"currency"`
	Test       bool      `json:"test"`
	StatusCode int       `json:"status_code"`
	CreatedAt  time.Time `json:"created_at"`
}
