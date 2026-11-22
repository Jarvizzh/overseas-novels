package paypal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"reader-backend/internal/payment"
)

// CreateSubscription creates a subscription agreement on PayPal and returns subscription ID and approval URL
func (p *Client) CreateSubscription(ctx context.Context, param payment.CreateSubscriptionParam) (*payment.SubscriptionResult, error) {
	if !validRequestKey(param.RequestID) || !validObjectID(param.PlanID) {
		return nil, errors.New("PayPal subscription requires a plan and idempotency key")
	}
	cfg, err := p.credentials(ctx)
	if err != nil {
		return nil, err
	}
	if param.Account != cfg.account() {
		return nil, errors.New("PayPal subscription account changed")
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

	var res struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Links  []struct {
			Href string `json:"href"`
			Rel  string `json:"rel"`
		} `json:"links"`
	}
	if err := p.send(ctx, cfg, http.MethodPost, "/v1/billing/subscriptions", param.RequestID, subReq, &res); err != nil {
		return nil, err
	}
	if res.ID == "" {
		return nil, errors.New("PayPal did not return a subscription ID")
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
		if cfg.Environment == EnvironmentLive {
			domain = "www.paypal.com"
		}
		approveURL = fmt.Sprintf("https://%s/webapps/billing/subscriptions?ba_token=%s", domain, res.ID)
	}

	return &payment.SubscriptionResult{
		SubscriptionID: res.ID,
		ApproveURL:     approveURL,
		Status:         res.Status,
	}, nil
}

// GetSubscription fetches the full subscription details from PayPal
func (p *Client) GetSubscription(ctx context.Context, subscriptionID string) (*payment.SubscriptionDetails, error) {
	var bodyBytes json.RawMessage
	path := "/v1/billing/subscriptions/" + url.PathEscape(subscriptionID)
	if err := p.request(ctx, http.MethodGet, path, "", nil, &bodyBytes); err != nil {
		return nil, err
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
	if subResp.ID != subscriptionID {
		return nil, errors.New("unexpected PayPal subscription ID")
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

	lastCents, _ := payment.ParseCents(subResp.BillingInfo.LastPayment.Amount.Value)
	payerName := strings.TrimSpace(subResp.Subscriber.Name.GivenName + " " + subResp.Subscriber.Name.Surname)

	return &payment.SubscriptionDetails{
		SubscriptionID:      subResp.ID,
		PlanID:              subResp.PlanID,
		CustomID:            subResp.CustomID,
		Status:              subResp.Status,
		CurrentPeriodStart:  startT,
		CurrentPeriodEnd:    nextT,
		NextBillingTime:     nextT,
		LastPaymentTime:     lastPayT,
		LastPaymentCents:    lastCents,
		LastPaymentCurrency: subResp.BillingInfo.LastPayment.Amount.CurrencyCode,
		PayerEmail:          subResp.Subscriber.EmailAddress,
		PayerID:             subResp.Subscriber.PayerID,
		PayerName:           payerName,
		RawPayload:          string(bodyBytes),
	}, nil
}

// ActivateSubscription activates an approved subscription
func (p *Client) ActivateSubscription(ctx context.Context, subscriptionID string) error {
	path := "/v1/billing/subscriptions/" + url.PathEscape(subscriptionID) + "/activate"
	err := p.request(ctx, http.MethodPost, path, "", map[string]string{"reason": "Activating subscription on star-novel"}, nil)
	var providerError *APIError
	if errors.As(err, &providerError) && providerError.HasIssue("SUBSCRIPTION_STATUS_INVALID") {
		details, lookupErr := p.GetSubscription(ctx, subscriptionID)
		if lookupErr == nil && details.Status == "ACTIVE" {
			return nil
		}
	}
	return err
}

// CancelSubscription stops future billing for a PayPal agreement.
func (p *Client) CancelSubscription(ctx context.Context, subscriptionID, reason string) error {
	if reason == "" {
		reason = "User requested cancellation"
	}
	path := "/v1/billing/subscriptions/" + url.PathEscape(subscriptionID) + "/cancel"
	return p.request(ctx, http.MethodPost, path, "", map[string]string{"reason": reason}, nil)
}

func (p *Client) ListSubscriptionTransactions(ctx context.Context, id string, from, to time.Time) ([]payment.SubscriptionTransaction, error) {
	query := url.Values{"start_time": {from.UTC().Format(time.RFC3339)}, "end_time": {to.UTC().Format(time.RFC3339)}}
	path := "/v1/billing/subscriptions/" + url.PathEscape(id) + "/transactions?" + query.Encode()
	var response struct {
		Transactions []payment.SubscriptionTransaction `json:"transactions"`
		Links        []struct {
			Rel string `json:"rel"`
		} `json:"links"`
	}
	if err := p.request(ctx, http.MethodGet, path, "", nil, &response); err != nil {
		return nil, err
	}
	for _, link := range response.Links {
		if link.Rel == "next" {
			return nil, errors.New("PayPal transaction result is incomplete; use a smaller reconciliation window")
		}
	}
	return response.Transactions, nil
}
