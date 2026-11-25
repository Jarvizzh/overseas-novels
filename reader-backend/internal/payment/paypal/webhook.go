package paypal

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"reader-backend/internal/payment"
)

func (p *Client) ParseWebhook(ctx context.Context, payload []byte, headers map[string]string) (*payment.WebhookEventResult, error) {
	event, err := p.VerifyWebhook(ctx, payload, headers)
	if err != nil {
		return nil, err
	}
	return event.Evidence()
}

func (p *Client) VerifyWebhook(ctx context.Context, payload []byte, headers map[string]string) (Event, error) {
	account, err := p.verifyWebhook(ctx, payload, headers)
	if err != nil {
		return Event{}, err
	}
	return decodeEvent(account, payload)
}

func parsePayPalWebhook(event Event) (*payment.WebhookEventResult, error) {

	switch event.Type {
	case "CUSTOMER.DISPUTE.CREATED", "CUSTOMER.DISPUTE.UPDATED", "CUSTOMER.DISPUTE.RESOLVED":
		dispute, err := decodeDispute(event.Account, event.Resource)
		if err != nil {
			return nil, err
		}
		return &payment.WebhookEventResult{EventID: event.ID, EventType: "dispute_updated",
			ExternalRefID: dispute.ID, Status: dispute.Status}, nil

	case "PAYMENT.CAPTURE.REVERSED", "PAYMENT.SALE.REVERSED":
		fact, err := event.Reversal()
		if err != nil {
			return nil, err
		}
		return &payment.WebhookEventResult{EventID: event.ID, EventType: "payment_reversed",
			ExternalRefID: fact.ResourceID, CaptureID: fact.OriginalReceiptID,
			SubscriptionID: fact.SubscriptionID, Status: fact.Status}, nil

	case "BILLING.SUBSCRIPTION.PAYMENT.FAILED":
		var sub struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		}
		if err := json.Unmarshal(event.Resource, &sub); err != nil || sub.ID == "" {
			return nil, errors.New("invalid PayPal subscription payment failure")
		}
		return &payment.WebhookEventResult{EventID: event.ID, EventType: "subscription_payment_failed",
			SubscriptionID: sub.ID, Status: sub.Status, PaidAt: event.CreatedAt}, nil

	case "PAYMENT.REFUND.PENDING", "PAYMENT.REFUND.FAILED":
		var refund struct {
			ID        string `json:"id"`
			Status    string `json:"status"`
			CaptureID string `json:"capture_id"`
		}
		if err := json.Unmarshal(event.Resource, &refund); err != nil || refund.ID == "" {
			return nil, errors.New("invalid PayPal refund status event")
		}
		return &payment.WebhookEventResult{EventID: event.ID, EventType: "refund_status",
			ExternalRefID: refund.ID, CaptureID: refund.CaptureID, Status: refund.Status}, nil

	case "PAYMENT.CAPTURE.DECLINED", "PAYMENT.CAPTURE.DENIED", "PAYMENT.CAPTURE.PENDING", "PAYMENT.SALE.DENIED", "PAYMENT.SALE.PENDING":
		var receipt struct {
			ID                 string                `json:"id"`
			Status             string                `json:"status"`
			State              string                `json:"state"`
			BillingAgreementID string                `json:"billing_agreement_id"`
			StatusDetails      *CaptureStatusDetails `json:"status_details"`
		}
		if err := json.Unmarshal(event.Resource, &receipt); err != nil || receipt.ID == "" {
			return nil, errors.New("invalid PayPal payment status event")
		}
		if receipt.Status == "" {
			receipt.Status = receipt.State
		}
		result := &payment.WebhookEventResult{EventID: event.ID, EventType: "payment_status",
			ExternalRefID: receipt.ID, Status: receipt.Status, SubscriptionID: receipt.BillingAgreementID}
		if receipt.StatusDetails != nil {
			result.ProviderReason = receipt.StatusDetails.Reason
		}
		return result, nil

	case "PAYMENT.CAPTURE.REFUNDED", "PAYMENT.SALE.REFUNDED":
		var refund struct {
			ID                string `json:"id"`
			CaptureID         string `json:"capture_id"`
			SaleID            string `json:"sale_id"`
			Status            string `json:"status"`
			State             string `json:"state"`
			SupplementaryData struct {
				RelatedIDs struct {
					CaptureID string `json:"capture_id"`
				} `json:"related_ids"`
			} `json:"supplementary_data"`
			Links []struct {
				Rel  string `json:"rel"`
				Href string `json:"href"`
			} `json:"links"`
			Amount struct {
				Value        string `json:"value"`
				CurrencyCode string `json:"currency_code"`
				Total        string `json:"total"`
				Currency     string `json:"currency"`
			}
		}
		if err := json.Unmarshal(event.Resource, &refund); err != nil {
			return nil, err
		}
		amount, currency, captureID := refund.Amount.Value, refund.Amount.CurrencyCode, refund.CaptureID
		if event.Type == "PAYMENT.SALE.REFUNDED" {
			amount, currency, captureID = refund.Amount.Total, refund.Amount.Currency, refund.SaleID
		}
		if captureID == "" {
			captureID = refund.SupplementaryData.RelatedIDs.CaptureID
		}
		if captureID == "" {
			for _, link := range refund.Links {
				if link.Rel != "up" {
					continue
				}
				u, err := url.Parse(link.Href)
				if err != nil {
					continue
				}
				for _, prefix := range []string{"/v2/payments/captures/", "/v1/payments/sale/"} {
					if id, ok := strings.CutPrefix(u.Path, prefix); ok && id != "" && !strings.Contains(id, "/") {
						captureID = id
					}
				}
			}
		}
		if !strings.EqualFold(refund.Status, "completed") && !strings.EqualFold(refund.State, "completed") {
			return &payment.WebhookEventResult{EventID: event.ID}, nil
		}
		cents, err := payment.ParseCents(amount)
		if err != nil || refund.ID == "" {
			return nil, errors.New("invalid PayPal refund event")
		}
		return &payment.WebhookEventResult{EventID: event.ID, EventType: "refund_completed", ExternalRefID: refund.ID,
			CaptureID: captureID, AmountCents: cents, Currency: currency}, nil

	case "CHECKOUT.ORDER.APPROVED", "PAYMENT.CAPTURE.COMPLETED":
		var resource struct {
			ID                string `json:"id"`
			SupplementaryData struct {
				RelatedIDs struct {
					OrderID string `json:"order_id"`
				} `json:"related_ids"`
			} `json:"supplementary_data"`
		}
		if err := json.Unmarshal(event.Resource, &resource); err != nil {
			return nil, err
		}
		if event.Type == "CHECKOUT.ORDER.APPROVED" {
			return &payment.WebhookEventResult{EventID: event.ID, EventType: "order_approved", ExternalRefID: resource.ID}, nil
		}
		return &payment.WebhookEventResult{EventID: event.ID, EventType: "capture_completed", ExternalRefID: resource.SupplementaryData.RelatedIDs.OrderID}, nil

	case "PAYMENT.SALE.COMPLETED":
		// Recurring cycle payment completed
		var sale struct {
			CreateTime         string `json:"create_time"`
			ID                 string `json:"id"`
			BillingAgreementID string `json:"billing_agreement_id"` // This is the Subscription ID
			Amount             struct {
				Total    string `json:"total"`
				Currency string `json:"currency"`
			} `json:"amount"`
			State  string `json:"state"` // completed
			Custom string `json:"custom"`
		}
		if err := json.Unmarshal(event.Resource, &sale); err != nil {
			return nil, err
		}

		amountCents, err := payment.ParseCents(sale.Amount.Total)
		if err != nil || sale.State != "completed" || sale.ID == "" {
			return nil, errors.New("invalid PayPal sale")
		}

		paidAt, err := time.Parse(time.RFC3339, sale.CreateTime)
		if err != nil {
			return nil, err
		}
		return &payment.WebhookEventResult{
			EventType:      "payment_succeeded",
			EventID:        event.ID,
			PaidAt:         paidAt,
			SubscriptionID: sale.BillingAgreementID,
			ExternalRefID:  sale.ID,
			AmountCents:    amountCents,
			Currency:       sale.Amount.Currency,
			Status:         "COMPLETED",
			RawPayload:     string(event.Raw),
		}, nil

	case "BILLING.SUBSCRIPTION.ACTIVATED", "BILLING.SUBSCRIPTION.CREATED":
		var sub struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		}
		_ = json.Unmarshal(event.Resource, &sub)
		return &payment.WebhookEventResult{
			EventType:      "subscription_activated",
			EventID:        event.ID,
			SubscriptionID: sub.ID,
			Status:         sub.Status,
			RawPayload:     string(event.Raw),
		}, nil

	case "BILLING.SUBSCRIPTION.CANCELLED", "BILLING.SUBSCRIPTION.EXPIRED", "BILLING.SUBSCRIPTION.SUSPENDED":
		var sub struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		}
		_ = json.Unmarshal(event.Resource, &sub)
		return &payment.WebhookEventResult{
			EventType:      "subscription_cancelled",
			EventID:        event.ID,
			SubscriptionID: sub.ID,
			Status:         sub.Status,
			RawPayload:     string(event.Raw),
		}, nil
	}

	return &payment.WebhookEventResult{EventID: event.ID}, nil
}

func (p *Client) verifyWebhook(ctx context.Context, payload []byte, headers map[string]string) (payment.WebhookAccount, error) {
	var account payment.WebhookAccount
	cfg, err := p.credentials(ctx)
	if err != nil {
		return account, err
	}
	webhookIDs := make([]string, 0, 2)
	if cfg.WebhookID != "" {
		webhookIDs = append(webhookIDs, cfg.WebhookID)
	}
	if cfg.PreviousWebhookID != "" && cfg.PreviousWebhookID != cfg.WebhookID {
		webhookIDs = append(webhookIDs, cfg.PreviousWebhookID)
	}
	if len(webhookIDs) == 0 {
		return account, errors.New("PayPal webhook ID unavailable")
	}
	h := make(http.Header, len(headers))
	for key, value := range headers {
		h.Set(key, value)
	}
	baseReq := map[string]any{"webhook_event": json.RawMessage(payload)}
	for field, header := range map[string]string{
		"auth_algo": "Paypal-Auth-Algo", "cert_url": "Paypal-Cert-Url",
		"transmission_id": "Paypal-Transmission-Id", "transmission_sig": "Paypal-Transmission-Sig",
		"transmission_time": "Paypal-Transmission-Time",
	} {
		value := h.Get(header)
		if value == "" {
			return account, errors.New("incomplete PayPal signature")
		}
		baseReq[field] = value
	}
	for _, webhookID := range webhookIDs {
		req := make(map[string]any, len(baseReq)+1)
		for k, v := range baseReq {
			req[k] = v
		}
		req["webhook_id"] = webhookID
		var result struct {
			Status string `json:"verification_status"`
		}
		if err := p.send(ctx, cfg, http.MethodPost, "/v1/notifications/verify-webhook-signature", "", req, &result); err != nil {
			if len(webhookIDs) == 1 {
				return account, err
			}
			continue
		}
		if result.Status == "SUCCESS" {
			return cfg.account(), nil
		}
	}
	return account, errors.New("invalid PayPal signature")
}
