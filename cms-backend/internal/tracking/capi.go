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

	"star-novel-cms/internal/config"
	"star-novel-cms/internal/db"
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
func SendFacebookEvent(eventName string, userID string, email string, ip string, ua string, fbc string, fbp string, value float64, currency string, sourceURL string) {
	// Construct payload first
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

	if sourceURL == "" {
		sourceURL = "https://h5.star-novel.com"
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

	testCode := getEnv("FB_TEST_EVENT_CODE", "")
	if testCode != "" {
		payload.TestEventCode = testCode
	}

	jsonBytes, err := json.Marshal(payload)
	var payloadStr string
	if err == nil {
		payloadStr = string(jsonBytes)
	}

	pixelID := config.AppConfig.FbPixelID
	accessToken := config.AppConfig.FbAccessToken

	// Try reading from db system_configs if empty in config
	if pixelID == "" || accessToken == "" {
		var dbPixel, dbToken string
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if db.DB != nil {
			_ = db.DB.QueryRow(ctx, "SELECT value FROM system_configs WHERE key = 'fb_pixel_id'").Scan(&dbPixel)
			_ = db.DB.QueryRow(ctx, "SELECT value FROM system_configs WHERE key = 'fb_access_token'").Scan(&dbToken)
		}
		if dbPixel != "" {
			pixelID = dbPixel
		}
		if dbToken != "" {
			accessToken = dbToken
		}
	}

	if pixelID == "" {
		log.Printf("[FB CAPI Error] Pixel ID is missing. Event Name: %s, User: %s", eventName, userID)
		saveCAPILog("N/A", eventName, userID, value, currency, testCode, -1, payloadStr, "Error - Pixel ID missing")
		return
	}

	if accessToken == "" || pixelID == "fb_pixel_id_placeholder" {
		log.Printf("[FB CAPI Dry Run] Event: %s, User: %s, Value: %.2f %s. Facebook Pixel Credentials not configured.", eventName, userID, value, currency)
		saveCAPILog(pixelID, eventName, userID, value, currency, testCode, 0, payloadStr, "Dry Run - Credentials not configured")
		return
	}

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

func saveCAPILog(pixelID string, eventName string, userID string, value float64, currency string, testEventCode string, statusCode int, payload string, response string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var uID *int64
	if userID != "" {
		if id, err := strconv.ParseInt(userID, 10, 64); err == nil {
			uID = &id
		}
	}

	query := `
		INSERT INTO facebook_capi_logs (pixel_id, event_name, user_id, value, currency, test_event_code, status_code, payload, response)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`
	if db.DB != nil {
		_, err := db.DB.Exec(ctx, query, pixelID, eventName, uID, value, currency, testEventCode, statusCode, payload, response)
		if err != nil {
			log.Printf("[FB CAPI Log Error] Failed to insert log to database: %v", err)
		}
	}
}

func getEnv(key, fallback string) string {
	importOS := true // Dummy to satisfy package
	_ = importOS
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}
