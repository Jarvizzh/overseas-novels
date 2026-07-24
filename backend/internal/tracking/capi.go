package tracking

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"novel-backend/internal/config"
	"novel-backend/internal/db"
)

type FacebookEvent struct {
	EventName      string                 `json:"event_name"`
	EventTime      int64                  `json:"event_time"`
	EventID        string                 `json:"event_id,omitempty"`
	EventSourceURL string                 `json:"event_source_url,omitempty"`
	ActionSource   string                 `json:"action_source"` // e.g. "website"
	UserData       FacebookUserData       `json:"user_data"`
	CustomData     map[string]interface{} `json:"custom_data,omitempty"`
}

type FacebookUserData struct {
	Emails          []string `json:"em,omitempty"` // Hashed
	ClientIPAddress string   `json:"client_ip_address,omitempty"`
	ClientUserAgent string   `json:"client_user_agent,omitempty"`
	Fbc             string   `json:"fbc,omitempty"`         // Facebook click ID
	Fbp             string   `json:"fbp,omitempty"`         // Facebook browser ID
	ExternalID      string   `json:"external_id,omitempty"` // Hashed user ID
	Country         []string `json:"country,omitempty"`     // Hashed 2-letter ISO country code
	PhoneNumbers    []string `json:"ph,omitempty"`          // Hashed phone number
	FirstNames      []string `json:"fn,omitempty"`          // Hashed first name
	LastNames       []string `json:"ln,omitempty"`          // Hashed last name
	Cities          []string `json:"ct,omitempty"`          // Hashed city
	States          []string `json:"st,omitempty"`          // Hashed state
	ZipCodes        []string `json:"zp,omitempty"`          // Hashed zip code
}

type FacebookCAPIRequest struct {
	Data          []FacebookEvent `json:"data"`
	TestEventCode string          `json:"test_event_code,omitempty"` // Used for testing in Event Manager
}

// Helper to hash string to SHA-256
func HashSHA256(input string) string {
	if input == "" {
		return ""
	}
	cleaned := strings.TrimSpace(strings.ToLower(input))
	hash := sha256.New()
	hash.Write([]byte(cleaned))
	return hex.EncodeToString(hash.Sum(nil))
}

// SendFacebookEvent pushes a tracking event server-to-server to Facebook
func SendFacebookEvent(pixelID string, eventName string, userID string, email string, ip string, ua string, fbc string, fbp string, value float64, currency string, sourceURL string, country string) {
	// Construct the payload structure first so we can log it
	userData := FacebookUserData{
		ClientIPAddress: ip,
		ClientUserAgent: ua,
		Fbc:             fbc,
		Fbp:             fbp,
		ExternalID:      HashSHA256(userID),
	}
	if email != "" {
		userData.Emails = []string{HashSHA256(email)}
	}
	if country != "" {
		userData.Country = []string{HashSHA256(country)}
	}

	if sourceURL == "" {
		if config.AppConfig != nil && config.AppConfig.DefaultDomain != "" {
			sourceURL = config.AppConfig.DefaultDomain
		} else {
			sourceURL = "https://h5.star-novel.com"
		}
	}

	event := FacebookEvent{
		EventName:      eventName,
		EventTime:      time.Now().Unix(),
		EventID:        fmt.Sprintf("%s_%s_%d", eventName, userID, time.Now().UnixNano()),
		ActionSource:   "website",
		EventSourceURL: sourceURL,
		UserData:       userData,
	}

	if value > 0 {
		event.CustomData = map[string]interface{}{
			"value":    value,
			"currency": currency,
		}
	}

	payload := FacebookCAPIRequest{
		Data: []FacebookEvent{event},
	}

	testCode := os.Getenv("FB_TEST_EVENT_CODE")
	if testCode != "" {
		payload.TestEventCode = testCode
	}

	jsonBytes, err := json.Marshal(payload)
	var payloadStr string
	if err == nil {
		payloadStr = string(jsonBytes)
	}

	// 1. Check if pixel ID is missing
	if pixelID == "" {
		log.Printf("[FB CAPI Error] Pixel ID is missing. Event Name: %s, User: %s", eventName, userID)
		saveCAPILog("N/A", eventName, userID, value, currency, testCode, -1, payloadStr, "Error - Pixel ID missing")
		return
	}

	// 2. Query access token
	var accessToken string
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if db.DB != nil {
		err := db.DB.QueryRow(ctx, "SELECT access_token FROM fb_pixels WHERE pixel_id = $1", pixelID).Scan(&accessToken)
		if err != nil {
			log.Printf("[FB CAPI Error] Failed to find access token for pixel %s: %v", pixelID, err)
			saveCAPILog(pixelID, eventName, userID, value, currency, testCode, -2, payloadStr, fmt.Sprintf("Error - Access token query failed: %v", err))
			return
		}
	}

	// 3. Dry run check
	if accessToken == "" {
		log.Printf("[FB CAPI Dry Run] Event: %s, User: %s, Value: %.2f %s. Facebook Pixel Credentials not configured for %s.", eventName, userID, value, currency, pixelID)
		saveCAPILog(pixelID, eventName, userID, value, currency, testCode, 0, payloadStr, "Dry Run - Credentials not configured")
		return
	}

	// 4. Send request
	apiURL := fmt.Sprintf("https://graph.facebook.com/v19.0/%s/events?access_token=%s", pixelID, accessToken)
	req, err := http.NewRequest("POST", apiURL, bytes.NewBuffer(jsonBytes))
	if err != nil {
		log.Printf("Failed to create FB CAPI request: %v", err)
		saveCAPILog(pixelID, eventName, userID, value, currency, testCode, -3, payloadStr, fmt.Sprintf("Error - Create request failed: %v", err))
		return
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Failed to transmit FB CAPI event: %v", err)
		saveCAPILog(pixelID, eventName, userID, value, currency, testCode, -4, payloadStr, fmt.Sprintf("Error - Network request failed: %v", err))
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	respStr := string(respBody)

	if resp.StatusCode != http.StatusOK {
		log.Printf("Facebook CAPI responded with error code %d: %s", resp.StatusCode, respStr)
	} else {
		log.Printf("Successfully pushed Facebook CAPI event: %s", eventName)
	}
	saveCAPILog(pixelID, eventName, userID, value, currency, testCode, resp.StatusCode, payloadStr, respStr)
}

type CAPILogItem struct {
	PixelID       string
	EventName     string
	UserID        *int64
	Value         float64
	Currency      string
	TestEventCode string
	StatusCode    int
	Payload       string
	Response      string
}

var logChan = make(chan CAPILogItem, 5000)

func init() {
	go func() {
		batch := make([]CAPILogItem, 0, 50)
		ticker := time.NewTicker(2 * time.Second)
		for {
			select {
			case item, ok := <-logChan:
				if !ok {
					if len(batch) > 0 {
						flushCAPILogs(batch)
					}
					return
				}
				batch = append(batch, item)
				if len(batch) >= 50 {
					flushCAPILogs(batch)
					batch = make([]CAPILogItem, 0, 50)
				}
			case <-ticker.C:
				if len(batch) > 0 {
					flushCAPILogs(batch)
					batch = make([]CAPILogItem, 0, 50)
				}
			}
		}
	}()
}

func saveCAPILog(pixelID string, eventName string, userID string, value float64, currency string, testEventCode string, statusCode int, payload string, response string) {
	var uID *int64
	if userID != "" {
		if id, err := strconv.ParseInt(userID, 10, 64); err == nil {
			uID = &id
		}
	}

	item := CAPILogItem{
		PixelID:       pixelID,
		EventName:     eventName,
		UserID:        uID,
		Value:         value,
		Currency:      currency,
		TestEventCode: testEventCode,
		StatusCode:    statusCode,
		Payload:       payload,
		Response:      response,
	}

	select {
	case logChan <- item:
	default:
		log.Println("[FB CAPI Log Warning] Channel full, dropping CAPI log record")
	}
}

func flushCAPILogs(batch []CAPILogItem) {
	if db.DB == nil || len(batch) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tx, err := db.DB.Begin(ctx)
	if err != nil {
		log.Printf("[FB CAPI Log Error] Failed to begin transaction for log batch: %v", err)
		return
	}
	defer tx.Rollback(ctx)

	query := `
		INSERT INTO facebook_capi_logs (pixel_id, event_name, user_id, value, currency, test_event_code, status_code, payload, response)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`
	for _, item := range batch {
		_, err := tx.Exec(ctx, query, item.PixelID, item.EventName, item.UserID, item.Value, item.Currency, item.TestEventCode, item.StatusCode, item.Payload, item.Response)
		if err != nil {
			log.Printf("[FB CAPI Log Error] Failed to insert log batch item: %v", err)
		}
	}
	_ = tx.Commit(ctx)
}

