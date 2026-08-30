package payment

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"reader-backend/internal/config"
)

type PayPalClient struct {
	baseURL string
}

func NewPayPalClient() *PayPalClient {
	baseURL := "https://api-m.sandbox.paypal.com"
	if config.AppConfig != nil && (strings.ToLower(config.AppConfig.PayPalMode) == "live" || strings.ToLower(config.AppConfig.PayPalMode) == "production") {
		baseURL = "https://api-m.paypal.com"
	}
	return &PayPalClient{
		baseURL: baseURL,
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
	ID            string `json:"id"`
	Status        string `json:"status"` // COMPLETED
	PurchaseUnits []struct {
		CustomID string `json:"custom_id"`
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

type ApplicationContext struct {
	ReturnURL          string `json:"return_url,omitempty"`
	CancelURL          string `json:"cancel_url,omitempty"`
	BrandName          string `json:"brand_name,omitempty"`
	UserAction         string `json:"user_action,omitempty"`
	ShippingPreference string `json:"shipping_preference,omitempty"`
}

type CreateOrderRequest struct {
	Intent             string               `json:"intent"`
	PurchaseUnits      []CreatePurchaseUnit `json:"purchase_units"`
	ApplicationContext *ApplicationContext  `json:"application_context,omitempty"`
}

type CreatePurchaseUnit struct {
	Amount struct {
		CurrencyCode string `json:"currency_code"`
		Value        string `json:"value"`
	} `json:"amount"`
	CustomID    string `json:"custom_id,omitempty"`
	Description string `json:"description,omitempty"`
}

type CreateOrderResponse struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Links  []struct {
		Href   string `json:"href"`
		Rel    string `json:"rel"`
		Method string `json:"method"`
	} `json:"links"`
}

// CreateOrder creates an order in PayPal and returns the order ID and approve URL for redirect flow
func (p *PayPalClient) CreateOrder(ctx context.Context, amountCents int64, coinsAmount int, userID int64, description, returnURL, cancelURL string) (string, string, error) {
	clientID := config.AppConfig.PayPalClientID
	if clientID == "" || clientID == "paypal_client_id_placeholder" {
		mockID := fmt.Sprintf("mock_paypal_order_%d", time.Now().UnixNano())
		mockRedirect := returnURL
		if mockRedirect == "" {
			mockRedirect = "http://localhost:5173/recharge"
		}
		separator := "?"
		if strings.Contains(mockRedirect, "?") {
			separator = "&"
		}
		return mockID, fmt.Sprintf("%s%stoken=%s&payment=success", mockRedirect, separator, mockID), nil
	}

	token, err := p.getAccessToken(ctx)
	if err != nil {
		return "", "", fmt.Errorf("failed to get paypal access token: %w", err)
	}

	valStr := fmt.Sprintf("%.2f", float64(amountCents)/100.0)
	customID := fmt.Sprintf("%d:%d", userID, coinsAmount)

	orderReq := CreateOrderRequest{
		Intent: "CAPTURE",
		PurchaseUnits: []CreatePurchaseUnit{
			{
				Amount: struct {
					CurrencyCode string `json:"currency_code"`
					Value        string `json:"value"`
				}{
					CurrencyCode: "USD",
					Value:        valStr,
				},
				CustomID:    customID,
				Description: description,
			},
		},
	}

	if returnURL != "" || cancelURL != "" {
		orderReq.ApplicationContext = &ApplicationContext{
			ReturnURL:          returnURL,
			CancelURL:          cancelURL,
			BrandName:          "Star Novel",
			UserAction:         "PAY_NOW",
			ShippingPreference: "NO_SHIPPING",
		}
	}

	reqBody, err := json.Marshal(orderReq)
	if err != nil {
		return "", "", err
	}

	url := fmt.Sprintf("%s/v2/checkout/orders", p.baseURL)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(reqBody))
	if err != nil {
		return "", "", err
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return "", "", fmt.Errorf("paypal create order failed (status %d): %s", resp.StatusCode, string(body))
	}

	var createResp CreateOrderResponse
	if err := json.NewDecoder(resp.Body).Decode(&createResp); err != nil {
		return "", "", err
	}

	var approveURL string
	for _, link := range createResp.Links {
		if link.Rel == "approve" || link.Rel == "payer-action" {
			approveURL = link.Href
			break
		}
	}

	if approveURL == "" {
		// Fallback
		domain := "www.sandbox.paypal.com"
		if config.AppConfig != nil && (strings.ToLower(config.AppConfig.PayPalMode) == "live" || strings.ToLower(config.AppConfig.PayPalMode) == "production") {
			domain = "www.paypal.com"
		}
		approveURL = fmt.Sprintf("https://%s/checkoutnow?token=%s", domain, createResp.ID)
	}

	return createResp.ID, approveURL, nil
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
	if len(captureResp.PurchaseUnits) > 0 {
		pu := captureResp.PurchaseUnits[0]
		if len(pu.Payments.Captures) > 0 {
			capture := pu.Payments.Captures[0]
			customID := capture.CustomID
			if customID == "" {
				customID = pu.CustomID
			}
			return customID, capture.Amount.CurrencyCode, capture.Amount.Value, nil
		}
	}

	return "", "", "", errors.New("no captures found in paypal response")
}
