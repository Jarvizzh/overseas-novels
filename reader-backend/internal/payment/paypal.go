package payment

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"reader-backend/internal/config"
)

type PayPalClient struct {
	baseURL string
}

func NewPayPalClient() *PayPalClient {
	return &PayPalClient{
		baseURL: "https://api-m.sandbox.paypal.com", // Sandbox default
	}
}

type TokenResponse struct {
	AccessToken string `json:"access_token"`
}

func (p *PayPalClient) getAccessToken(ctx context.Context) (string, error) {
	clientID := config.AppConfig.PayPalClientID
	secret := config.AppConfig.PayPalClientSecret

	if clientID == "" || secret == "" || clientID == "paypal_client_id_placeholder" {
		return "mock_paypal_token", nil
	}

	req, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/v1/oauth2/token", bytes.NewBufferString("grant_type=client_credentials"))
	if err != nil {
		return "", err
	}

	req.SetBasicAuth(clientID, secret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("paypal auth failed: %s", string(body))
	}

	var tokenResp TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", err
	}

	return tokenResp.AccessToken, nil
}

type CaptureResponse struct {
	ID    string `json:"id"`
	Status string `json:"status"` // COMPLETED
	PurchaseUnits []struct {
		Payments struct {
			Captures []struct {
				Amount struct {
					CurrencyCode string `json:"currency_code"`
					Value        string `json:"value"`
				} `json:"amount"`
				CustomID string `json:"custom_id"` // Holds user_id:coins_amount
			} `json:"captures"`
		} `json:"payments"`
	} `json:"purchase_units"`
}

// CaptureOrder captures a PayPal transaction and returns custom_id, currency, value, error
func (p *PayPalClient) CaptureOrder(ctx context.Context, orderID string) (string, string, string, error) {
	clientID := config.AppConfig.PayPalClientID
	if clientID == "" || clientID == "paypal_client_id_placeholder" {
		// Mock PayPal capture in local sandbox development
		return "mock_user_id:1000", "USD", "9.99", nil
	}

	token, err := p.getAccessToken(ctx)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to get paypal access token: %w", err)
	}

	url := fmt.Sprintf("%s/v2/checkout/orders/%s/capture", p.baseURL, orderID)
	req, err := http.NewRequestWithContext(ctx, "POST", url, nil)
	if err != nil {
		return "", "", "", err
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return "", "", "", fmt.Errorf("paypal capture failed: %s", string(body))
	}

	var captureResp CaptureResponse
	if err := json.NewDecoder(resp.Body).Decode(&captureResp); err != nil {
		return "", "", "", err
	}

	if captureResp.Status != "COMPLETED" {
		return "", "", "", fmt.Errorf("paypal transaction is not completed: status is %s", captureResp.Status)
	}

	// Extract details
	if len(captureResp.PurchaseUnits) > 0 && len(captureResp.PurchaseUnits[0].Payments.Captures) > 0 {
		capture := captureResp.PurchaseUnits[0].Payments.Captures[0]
		return capture.CustomID, capture.Amount.CurrencyCode, capture.Amount.Value, nil
	}

	return "", "", "", errors.New("no captures found in paypal response")
}
