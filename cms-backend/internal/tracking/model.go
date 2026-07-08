package tracking

import "time"

type FacebookCAPILog struct {
	ID            int64     `json:"id"`
	PixelID       string    `json:"pixel_id"`
	EventName     string    `json:"event_name"`
	UserID        *int64    `json:"user_id"`
	Value         *float64  `json:"value"`
	Currency      *string   `json:"currency"`
	TestEventCode *string   `json:"test_event_code"`
	StatusCode    int       `json:"status_code"`
	Payload       string    `json:"payload"`
	Response      string    `json:"response"`
	CreatedAt     time.Time `json:"created_at"`
}
