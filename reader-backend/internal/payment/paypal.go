package payment

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
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
	client := &PayPalClient{
		baseURL: baseURL,
	}
	// Register as default PayPal subscription provider
	RegisterSubscriptionProvider(ProviderPayPal, client)
	return client
}

func (p *PayPalClient) GetProviderName() string {
	return ProviderPayPal
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

type PayPalCaptureResult struct {
	OrderID                string `json:"order_id"`
	CaptureID              string `json:"capture_id"`
	CustomID               string `json:"custom_id"`
	CurrencyCode           string `json:"currency_code"`
	GrossAmount            string `json:"gross_amount"`
	FeeAmount              string `json:"fee_amount"`
	NetAmount              string `json:"net_amount"`
	PayerID                string `json:"payer_id"`
	PayerEmail             string `json:"payer_email"`
	PayerName              string `json:"payer_name"`
	PayerCountry           string `json:"payer_country"`
	Status                 string `json:"status"`
	SellerProtectionStatus string `json:"seller_protection_status"`
	RawPayload             string `json:"raw_payload"`
}

type CaptureResponse struct {
	ID            string `json:"id"`
	Status        string `json:"status"` // COMPLETED
	PaymentSource struct {
		PayPal struct {
			EmailAddress  string `json:"email_address"`
			AccountID     string `json:"account_id"`
			AccountStatus string `json:"account_status"`
			Name          struct {
				GivenName string `json:"given_name"`
				Surname   string `json:"surname"`
			} `json:"name"`
			Address struct {
				CountryCode string `json:"country_code"`
			} `json:"address"`
		} `json:"paypal"`
	} `json:"payment_source"`
	Payer struct {
		EmailAddress string `json:"email_address"`
		PayerID      string `json:"payer_id"`
		Name         struct {
			GivenName string `json:"given_name"`
			Surname   string `json:"surname"`
		} `json:"name"`
		Address struct {
			CountryCode string `json:"country_code"`
		} `json:"address"`
	} `json:"payer"`
	PurchaseUnits []struct {
		ReferenceID string `json:"reference_id"`
		CustomID    string `json:"custom_id"`
		Payments    struct {
			Captures []struct {
				ID       string `json:"id"`
				Status   string `json:"status"`
				CustomID string `json:"custom_id"`
				Amount   struct {
					CurrencyCode string `json:"currency_code"`
					Value        string `json:"value"`
				} `json:"amount"`
				SellerReceivableBreakdown struct {
					GrossAmount struct {
						CurrencyCode string `json:"currency_code"`
						Value        string `json:"value"`
					} `json:"gross_amount"`
					PayPalFee struct {
						CurrencyCode string `json:"currency_code"`
						Value        string `json:"value"`
					} `json:"paypal_fee"`
					NetAmount struct {
						CurrencyCode string `json:"currency_code"`
						Value        string `json:"value"`
					} `json:"net_amount"`
				} `json:"seller_receivable_breakdown"`
				SellerProtection struct {
					Status string `json:"status"`
				} `json:"seller_protection"`
				CreateTime string `json:"create_time"`
				UpdateTime string `json:"update_time"`
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

// CreateOrder creates a single order in PayPal
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
		domain := "www.sandbox.paypal.com"
		if config.AppConfig != nil && (strings.ToLower(config.AppConfig.PayPalMode) == "live" || strings.ToLower(config.AppConfig.PayPalMode) == "production") {
			domain = "www.paypal.com"
		}
		approveURL = fmt.Sprintf("https://%s/checkoutnow?token=%s", domain, createResp.ID)
	}

	return createResp.ID, approveURL, nil
}

// CaptureOrder captures a PayPal single transaction
func (p *PayPalClient) CaptureOrder(ctx context.Context, orderID string) (*PayPalCaptureResult, error) {
	clientID := config.AppConfig.PayPalClientID
	if clientID == "" || clientID == "paypal_client_id_placeholder" {
		return &PayPalCaptureResult{
			OrderID:                orderID,
			CaptureID:              "mock_capture_" + orderID,
			CustomID:               "mock_user_id:1000",
			CurrencyCode:           "USD",
			GrossAmount:            "9.99",
			FeeAmount:              "0.49",
			NetAmount:              "9.50",
			PayerID:                "MOCK_PAYER_ID",
			PayerEmail:             "mock_payer@example.com",
			PayerName:              "Mock User",
			PayerCountry:           "US",
			Status:                 "COMPLETED",
			SellerProtectionStatus: "ELIGIBLE",
			RawPayload:             `{"mock": true}`,
		}, nil
	}

	token, err := p.getAccessToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get paypal access token: %w", err)
	}

	url := fmt.Sprintf("%s/v2/checkout/orders/%s/capture", p.baseURL, orderID)
	req, err := http.NewRequestWithContext(ctx, "POST", url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("paypal capture failed (status %d): %s", resp.StatusCode, string(bodyBytes))
	}

	var captureResp CaptureResponse
	if err := json.Unmarshal(bodyBytes, &captureResp); err != nil {
		return nil, err
	}

	if captureResp.Status != "COMPLETED" {
		return nil, fmt.Errorf("paypal transaction is not completed: status is %s", captureResp.Status)
	}

	if len(captureResp.PurchaseUnits) > 0 {
		pu := captureResp.PurchaseUnits[0]
		if len(pu.Payments.Captures) > 0 {
			capture := pu.Payments.Captures[0]
			customID := capture.CustomID
			if customID == "" {
				customID = pu.CustomID
			}

			payerEmail := captureResp.Payer.EmailAddress
			if payerEmail == "" {
				payerEmail = captureResp.PaymentSource.PayPal.EmailAddress
			}

			payerID := captureResp.Payer.PayerID
			if payerID == "" {
				payerID = captureResp.PaymentSource.PayPal.AccountID
			}

			payerName := strings.TrimSpace(captureResp.Payer.Name.GivenName + " " + captureResp.Payer.Name.Surname)
			if payerName == "" {
				payerName = strings.TrimSpace(captureResp.PaymentSource.PayPal.Name.GivenName + " " + captureResp.PaymentSource.PayPal.Name.Surname)
			}

			payerCountry := captureResp.Payer.Address.CountryCode
			if payerCountry == "" {
				payerCountry = captureResp.PaymentSource.PayPal.Address.CountryCode
			}

			fee := capture.SellerReceivableBreakdown.PayPalFee.Value
			if fee == "" {
				fee = "0.00"
			}
			net := capture.SellerReceivableBreakdown.NetAmount.Value
			if net == "" {
				net = capture.Amount.Value
			}

			return &PayPalCaptureResult{
				OrderID:                orderID,
				CaptureID:              capture.ID,
				CustomID:               customID,
				CurrencyCode:           capture.Amount.CurrencyCode,
				GrossAmount:            capture.Amount.Value,
				FeeAmount:              fee,
				NetAmount:              net,
				PayerID:                payerID,
				PayerEmail:             payerEmail,
				PayerName:              payerName,
				PayerCountry:           payerCountry,
				Status:                 capture.Status,
				SellerProtectionStatus: capture.SellerProtection.Status,
				RawPayload:             string(bodyBytes),
			}, nil
		}
	}

	return nil, errors.New("no captures found in paypal response")
}

// =========================================================================
// PayPal Subscriptions API Implementation (SubscriptionProvider)
// =========================================================================

// CreateOrGetProduct ensures a default subscription product exists on PayPal
func (p *PayPalClient) CreateOrGetProduct(ctx context.Context) (string, error) {
	clientID := config.AppConfig.PayPalClientID
	if clientID == "" || clientID == "paypal_client_id_placeholder" {
		return "PROD_STAR_NOVEL_VIP", nil
	}

	token, err := p.getAccessToken(ctx)
	if err != nil {
		return "", err
	}

	productReq := map[string]interface{}{
		"name":        "Star Novel VIP Subscription",
		"description": "Unlimited reading access for Star Novel VIP Members",
		"type":        "DIGITAL",
		"category":    "BOOKS_PERIODICALS_AND_NEWSPAPERS",
	}

	reqBody, _ := json.Marshal(productReq)
	url := fmt.Sprintf("%s/v1/catalogs/products", p.baseURL)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(reqBody))
	if err != nil {
		return "", err
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("PayPal-Request-Id", "prod_star_novel_vip_01")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
		var res struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(bodyBytes, &res); err == nil && res.ID != "" {
			return res.ID, nil
		}
	}

	return "PROD_STAR_NOVEL_VIP", nil
}

// CreatePlan creates a recurring billing plan on PayPal (Day, Week, Month)
func (p *PayPalClient) CreatePlan(ctx context.Context, param CreateSubscriptionPlanParam) (string, error) {
	clientID := config.AppConfig.PayPalClientID
	if clientID == "" || clientID == "paypal_client_id_placeholder" {
		return fmt.Sprintf("mock_paypal_plan_%s_%d", param.Cycle, param.PriceCents), nil
	}

	productID, err := p.CreateOrGetProduct(ctx)
	if err != nil {
		log.Printf("[PayPal] Failed to ensure product: %v", err)
		productID = "PROD_STAR_NOVEL_VIP"
	}

	token, err := p.getAccessToken(ctx)
	if err != nil {
		return "", err
	}

	intervalUnit := "MONTH"
	switch param.Cycle {
	case CycleDay:
		intervalUnit = "DAY"
	case CycleWeek:
		intervalUnit = "WEEK"
	case CycleMonth:
		intervalUnit = "MONTH"
	}

	currency := param.Currency
	if currency == "" {
		currency = "USD"
	}
	priceVal := fmt.Sprintf("%.2f", float64(param.PriceCents)/100.0)

	planReq := map[string]interface{}{
		"product_id":  productID,
		"name":        param.PlanName,
		"description": param.Description,
		"status":      "ACTIVE",
		"billing_cycles": []map[string]interface{}{
			{
				"frequency": map[string]interface{}{
					"interval_unit":  intervalUnit,
					"interval_count": 1,
				},
				"tenure_type":  "REGULAR",
				"sequence":     1,
				"total_cycles": 0, // Indefinite recurring
				"pricing_scheme": map[string]interface{}{
					"fixed_price": map[string]interface{}{
						"value":         priceVal,
						"currency_code": currency,
					},
				},
			},
		},
		"payment_preferences": map[string]interface{}{
			"auto_bill_outstanding":     true,
			"setup_fee_failure_action": "CONTINUE",
			"payment_failure_threshold": 3,
		},
	}

	reqBody, _ := json.Marshal(planReq)
	url := fmt.Sprintf("%s/v1/billing/plans", p.baseURL)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(reqBody))
	if err != nil {
		return "", err
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("paypal create billing plan failed (status %d): %s", resp.StatusCode, string(bodyBytes))
	}

	var res struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(bodyBytes, &res); err != nil || res.ID == "" {
		return "", errors.New("failed to parse paypal plan ID from response")
	}

	return res.ID, nil
}

// CreateSubscription creates a subscription agreement on PayPal and returns subscription ID and approval URL
func (p *PayPalClient) CreateSubscription(ctx context.Context, param CreateSubscriptionParam) (*SubscriptionResult, error) {
	clientID := config.AppConfig.PayPalClientID
	if clientID == "" || clientID == "paypal_client_id_placeholder" {
		mockSubID := fmt.Sprintf("I-MOCK%d", time.Now().UnixNano())
		mockRedirect := param.ReturnURL
		if mockRedirect == "" {
			mockRedirect = "http://localhost:5173/recharge"
		}
		separator := "?"
		if strings.Contains(mockRedirect, "?") {
			separator = "&"
		}
		approveURL := fmt.Sprintf("%s%ssubscription_id=%s&payment=success", mockRedirect, separator, mockSubID)
		return &SubscriptionResult{
			SubscriptionID: mockSubID,
			ApproveURL:     approveURL,
			Status:         "APPROVAL_PENDING",
		}, nil
	}

	token, err := p.getAccessToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get paypal access token: %w", err)
	}

	subReq := map[string]interface{}{
		"plan_id":   param.PlanID,
		"custom_id": param.CustomID,
		"application_context": map[string]interface{}{
			"brand_name":          "Star Novel",
			"locale":              "en-US",
			"shipping_preference": "NO_SHIPPING",
			"user_action":         "SUBSCRIBE_NOW",
			"return_url":          param.ReturnURL,
			"cancel_url":          param.CancelURL,
		},
	}

	reqBody, _ := json.Marshal(subReq)
	url := fmt.Sprintf("%s/v1/billing/subscriptions", p.baseURL)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("paypal create subscription failed (status %d): %s", resp.StatusCode, string(bodyBytes))
	}

	var res struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Links  []struct {
			Href string `json:"href"`
			Rel  string `json:"rel"`
		} `json:"links"`
	}
	if err := json.Unmarshal(bodyBytes, &res); err != nil || res.ID == "" {
		return nil, errors.New("failed to parse paypal subscription response")
	}

	var approveURL string
	for _, l := range res.Links {
		if l.Rel == "approve" || l.Rel == "payer-action" {
			approveURL = l.Href
			break
		}
	}

	if approveURL == "" {
		domain := "www.sandbox.paypal.com"
		if config.AppConfig != nil && (strings.ToLower(config.AppConfig.PayPalMode) == "live" || strings.ToLower(config.AppConfig.PayPalMode) == "production") {
			domain = "www.paypal.com"
		}
		approveURL = fmt.Sprintf("https://%s/webapps/billing/subscriptions?ba_token=%s", domain, res.ID)
	}

	return &SubscriptionResult{
		SubscriptionID: res.ID,
		ApproveURL:     approveURL,
		Status:         res.Status,
	}, nil
}

// GetSubscription fetches the full subscription details from PayPal
func (p *PayPalClient) GetSubscription(ctx context.Context, subscriptionID string) (*SubscriptionDetails, error) {
	clientID := config.AppConfig.PayPalClientID
	if clientID == "" || clientID == "paypal_client_id_placeholder" || strings.HasPrefix(subscriptionID, "I-MOCK") {
		now := time.Now()
		periodEnd := now.Add(30 * 24 * time.Hour)
		return &SubscriptionDetails{
			SubscriptionID:     subscriptionID,
			PlanID:             "mock_paypal_plan_month_1499",
			CustomID:           "mock_user_1",
			Status:             "ACTIVE",
			CurrentPeriodStart: &now,
			CurrentPeriodEnd:   &periodEnd,
			NextBillingTime:    &periodEnd,
			LastPaymentTime:    &now,
			PayerEmail:         "mock_subscriber@example.com",
			PayerID:            "MOCK_SUBSCRIBER_PAYER_ID",
			PayerName:          "Mock VIP Subscriber",
			RawPayload:         `{"mock": true}`,
		}, nil
	}

	token, err := p.getAccessToken(ctx)
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/v1/billing/subscriptions/%s", p.baseURL, subscriptionID)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("paypal get subscription failed (status %d): %s", resp.StatusCode, string(bodyBytes))
	}

	var subResp struct {
		ID         string `json:"id"`
		PlanID     string `json:"plan_id"`
		Status     string `json:"status"` // ACTIVE, APPROVED, CANCELLED, SUSPENDED, EXPIRED
		CustomID   string `json:"custom_id"`
		StartTime  string `json:"start_time"`
		Subscriber struct {
			EmailAddress string `json:"email_address"`
			PayerID      string `json:"payer_id"`
			Name         struct {
				GivenName string `json:"given_name"`
				Surname   string `json:"surname"`
			} `json:"name"`
		} `json:"subscriber"`
		BillingInfo struct {
			NextBillingTime string `json:"next_billing_time"`
			LastPayment     struct {
				Amount struct {
					CurrencyCode string `json:"currency_code"`
					Value        string `json:"value"`
				} `json:"amount"`
				Time string `json:"time"`
			} `json:"last_payment"`
		} `json:"billing_info"`
	}

	if err := json.Unmarshal(bodyBytes, &subResp); err != nil {
		return nil, err
	}

	var startT, nextT, lastPayT *time.Time
	if t, err := time.Parse(time.RFC3339, subResp.StartTime); err == nil {
		startT = &t
	}
	if t, err := time.Parse(time.RFC3339, subResp.BillingInfo.NextBillingTime); err == nil {
		nextT = &t
	}
	if t, err := time.Parse(time.RFC3339, subResp.BillingInfo.LastPayment.Time); err == nil {
		lastPayT = &t
	}

	payerName := strings.TrimSpace(subResp.Subscriber.Name.GivenName + " " + subResp.Subscriber.Name.Surname)

	return &SubscriptionDetails{
		SubscriptionID:     subResp.ID,
		PlanID:             subResp.PlanID,
		CustomID:           subResp.CustomID,
		Status:             subResp.Status,
		CurrentPeriodStart: startT,
		CurrentPeriodEnd:   nextT,
		NextBillingTime:    nextT,
		LastPaymentTime:    lastPayT,
		PayerEmail:         subResp.Subscriber.EmailAddress,
		PayerID:            subResp.Subscriber.PayerID,
		PayerName:          payerName,
		RawPayload:         string(bodyBytes),
	}, nil
}

// ActivateSubscription activates an approved subscription
func (p *PayPalClient) ActivateSubscription(ctx context.Context, subscriptionID string) error {
	if strings.HasPrefix(subscriptionID, "I-MOCK") {
		return nil
	}

	token, err := p.getAccessToken(ctx)
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/v1/billing/subscriptions/%s/activate", p.baseURL, subscriptionID)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBufferString(`{"reason": "Activating subscription on star-novel"}`))
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		bodyBytes, _ := io.ReadAll(resp.Body)
		// If already active, it's ok
		if strings.Contains(string(bodyBytes), "SUBSCRIPTION_STATUS_INVALID") {
			return nil
		}
		return fmt.Errorf("paypal activate subscription failed (status %d): %s", resp.StatusCode, string(bodyBytes))
	}

	return nil
}

// CancelSubscription cancels a subscription on PayPal
func (p *PayPalClient) CancelSubscription(ctx context.Context, subscriptionID string, reason string) error {
	if strings.HasPrefix(subscriptionID, "I-MOCK") {
		return nil
	}

	token, err := p.getAccessToken(ctx)
	if err != nil {
		return err
	}

	if reason == "" {
		reason = "User requested cancellation"
	}

	bodyJSON, _ := json.Marshal(map[string]string{"reason": reason})
	url := fmt.Sprintf("%s/v1/billing/subscriptions/%s/cancel", p.baseURL, subscriptionID)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(bodyJSON))
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("paypal cancel subscription failed (status %d): %s", resp.StatusCode, string(bodyBytes))
	}

	return nil
}

// ParseWebhook parses incoming PayPal Webhook payloads
func (p *PayPalClient) ParseWebhook(ctx context.Context, payload []byte, headers map[string]string) (*WebhookEventResult, error) {
	var event struct {
		EventType string          `json:"event_type"`
		ID        string          `json:"id"`
		Resource  json.RawMessage `json:"resource"`
	}

	if err := json.Unmarshal(payload, &event); err != nil {
		return nil, fmt.Errorf("invalid paypal webhook payload: %w", err)
	}

	switch event.EventType {
	case "PAYMENT.SALE.COMPLETED":
		// Recurring cycle payment completed
		var sale struct {
			ID                 string `json:"id"`
			BillingAgreementID string `json:"billing_agreement_id"` // This is the Subscription ID
			Amount             struct {
				Total    string `json:"total"`
				Currency string `json:"currency"`
			} `json:"amount"`
			State    string `json:"state"` // completed
			Custom   string `json:"custom"`
		}
		if err := json.Unmarshal(event.Resource, &sale); err != nil {
			return nil, err
		}

		var amountCents int64
		if f, err := json.Number(sale.Amount.Total).Float64(); err == nil {
			amountCents = int64(f * 100)
		}

		return &WebhookEventResult{
			EventType:      "payment_succeeded",
			SubscriptionID: sale.BillingAgreementID,
			ExternalRefID:  sale.ID,
			AmountCents:    amountCents,
			Currency:       sale.Amount.Currency,
			Status:         "COMPLETED",
			RawPayload:     string(payload),
		}, nil

	case "BILLING.SUBSCRIPTION.ACTIVATED", "BILLING.SUBSCRIPTION.CREATED":
		var sub struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		}
		_ = json.Unmarshal(event.Resource, &sub)
		return &WebhookEventResult{
			EventType:      "subscription_activated",
			SubscriptionID: sub.ID,
			Status:         sub.Status,
			RawPayload:     string(payload),
		}, nil

	case "BILLING.SUBSCRIPTION.CANCELLED", "BILLING.SUBSCRIPTION.EXPIRED", "BILLING.SUBSCRIPTION.SUSPENDED":
		var sub struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		}
		_ = json.Unmarshal(event.Resource, &sub)
		return &WebhookEventResult{
			EventType:      "subscription_cancelled",
			SubscriptionID: sub.ID,
			Status:         sub.Status,
			RawPayload:     string(payload),
		}, nil
	}

	return nil, fmt.Errorf("unsupported paypal webhook event_type: %s", event.EventType)
}
