package tracking

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// PaymentEvent is an immutable snapshot created in the payment settlement transaction.
type PaymentEvent struct {
	ID          string `json:"event_id"`
	Name        string `json:"event_name"`
	Time        int64  `json:"event_time"`
	UserID      string `json:"user_id"`
	Email       string `json:"email"`
	Amount      int64  `json:"amount_cents"`
	Currency    string `json:"currency"`
	Attribution struct {
		PixelID string `json:"pixel_id"`
		FBP     string `json:"fbp"`
		FBC     string `json:"fbc"`
		IP      string `json:"ip_address"`
		UA      string `json:"user_agent"`
		URL     string `json:"source_url"`
		Country string `json:"country"`
	} `json:"attribution"`
}

// DeliverPayment preserves the original ID/time on retries. No credential enters the payload or logs.
func DeliverPayment(ctx context.Context, event PaymentEvent) error {
	if event.ID == "" || event.Attribution.PixelID == "" {
		return fmt.Errorf("payment event requires event and pixel IDs")
	}
	token, err := paymentPixelToken(ctx, event.Attribution.PixelID)
	if err != nil {
		return err
	}

	data := FacebookUserData{ExternalID: HashSHA256(event.UserID), ClientIPAddress: event.Attribution.IP, ClientUserAgent: event.Attribution.UA, Fbp: event.Attribution.FBP, Fbc: event.Attribution.FBC}
	if event.Email != "" {
		data.Emails = []string{HashSHA256(event.Email)}
	}
	if event.Attribution.Country != "" {
		data.Country = []string{HashSHA256(event.Attribution.Country)}
	}
	payload := FacebookCAPIRequest{Data: []FacebookEvent{{EventID: event.ID, EventName: event.Name, EventTime: event.Time, EventSourceURL: event.Attribution.URL, ActionSource: "website", UserData: data, CustomData: map[string]interface{}{"value": float64(event.Amount) / 100, "currency": event.Currency}}}, TestEventCode: os.Getenv("FB_TEST_EVENT_CODE")}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	version := graphAPIVersion()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://graph.facebook.com/"+version+"/"+event.Attribution.PixelID+"/events", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("payment event delivery failed")
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return err
	}
	if err := savePaymentAttempt(ctx, event, payload.TestEventCode, response.StatusCode, string(body), string(raw)); err != nil {
		return err
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("payment event rejected: HTTP %d", response.StatusCode)
	}
	var result struct {
		Received int `json:"events_received"`
	}
	if err := json.Unmarshal(raw, &result); err != nil || result.Received != 1 {
		return fmt.Errorf("payment event acknowledgment missing")
	}
	return nil
}

func graphAPIVersion() string {
	version := os.Getenv("FB_GRAPH_API_VERSION")
	if version == "" {
		return "v25.0"
	}
	return version
}
