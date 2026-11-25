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
		Payee       *Payee `json:"payee"`
		Amount      struct {
			CurrencyCode string `json:"currency_code"`
			Value        string `json:"value"`
		} `json:"amount"`
		Payments struct {
			Captures []struct {
				ID            string `json:"id"`
				Status        string `json:"status"`
				StatusDetails struct {
					Reason string `json:"reason"`
				} `json:"status_details"`
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
	Payee       *Payee `json:"payee,omitempty"`
}

type Payee struct {
	MerchantID string `json:"merchant_id"`
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

// PayPal retains Orders API create and capture idempotency keys for six hours.
const OrderWriteWindow = 6*time.Hour - time.Minute

var ErrOrderLookupOnly = errors.New("PayPal order operation requires lookup")

func orderWriteError(err error) error {
	var providerError *APIError
	if errors.As(err, &providerError) && !providerError.Authentication && providerError.Status < http.StatusInternalServerError &&
		providerError.Status != http.StatusTooManyRequests && providerError.Status != http.StatusConflict &&
		providerError.Status != http.StatusRequestTimeout {
		return fmt.Errorf("%w: %w", ErrOrderLookupOnly, err)
	}
	return err
}

func (p *Client) CreateOrder(ctx context.Context, account payment.WebhookAccount, amountCents int64, checkoutID, requestID string, description, returnURL, cancelURL string) (string, string, error) {
	if amountCents <= 0 || checkoutID == "" || !validRequestKey(requestID) {
		return "", "", errors.New("PayPal order requires a positive amount, checkout reference and idempotency key")
	}
	cfg, err := p.credentials(ctx)
	if err != nil {
		return "", "", err
	}
	if cfg.account() != account {
		return "", "", errors.New("PayPal checkout account changed")
	}

	valStr := formatAmount(amountCents, "USD")

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
				CustomID:    checkoutID,
				Description: description,
			},
		},
	}

	if cfg.MerchantID != "" {
		orderReq.PurchaseUnits[0].Payee = &Payee{MerchantID: cfg.MerchantID}
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

	var createResp CreateOrderResponse
	if err := p.send(ctx, cfg, http.MethodPost, "/v2/checkout/orders", requestID, orderReq, &createResp); err != nil {
		return "", "", orderWriteError(err)
	}
	if createResp.ID == "" {
		return "", "", errors.New("PayPal returned an empty order ID")
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
		if cfg.Environment == EnvironmentLive {
			domain = "www.paypal.com"
		}
		approveURL = fmt.Sprintf("https://%s/checkoutnow?token=%s", domain, createResp.ID)
	}

	return createResp.ID, approveURL, nil
}

// CaptureOrder captures a PayPal single transaction
var (
	ErrOrderApproved = errors.New("PayPal order approved and awaiting capture")
	ErrOrderClosed   = errors.New("PayPal order is voided")
)

func (p *Client) CaptureOrder(ctx context.Context, orderID string, account payment.WebhookAccount) (*payment.OrderCapture, error) {
	return p.readCapture(ctx, orderID, &account)
}

func (p *Client) GetCapture(ctx context.Context, orderID string) (*payment.OrderCapture, error) {
	return p.readCapture(ctx, orderID, nil)
}

func (p *Client) readCapture(ctx context.Context, orderID string, expectedAccount *payment.WebhookAccount) (*payment.OrderCapture, error) {
	cfg, err := p.credentials(ctx)
	if err != nil {
		return nil, err
	}
	account := cfg.account()
	captureOrder := expectedAccount != nil
	if captureOrder && *expectedAccount != account {
		return nil, errors.New("PayPal capture account changed")
	}

	path := "/v2/checkout/orders/" + url.PathEscape(orderID)
	method, requestID := http.MethodGet, ""
	if captureOrder {
		path += "/capture"
		method, requestID = http.MethodPost, "capture-"+orderID
	}
	var bodyBytes json.RawMessage
	if err := p.send(ctx, cfg, method, path, requestID, nil, &bodyBytes); err != nil {
		if captureOrder {
			err = orderWriteError(err)
		}
		return nil, captureAPIOutcome(err)
	}

	var captureResp CaptureResponse
	if err := json.Unmarshal(bodyBytes, &captureResp); err != nil {
		return nil, err
	}

	if captureResp.ID != orderID {
		return nil, errors.New("unexpected PayPal order ID")
	}
	if len(captureResp.PurchaseUnits) != 1 {
		return nil, errors.New("unexpected PayPal purchase units")
	}
	if cfg.MerchantID != "" {
		payee := captureResp.PurchaseUnits[0].Payee
		if payee == nil || payee.MerchantID != cfg.MerchantID {
			return nil, errors.New("unexpected PayPal order payee")
		}
	}
	if !captureOrder && (captureResp.Status == "APPROVED" || captureResp.Status == "VOIDED") {
		unit := captureResp.PurchaseUnits[0]
		result := &payment.OrderCapture{Account: account, OrderID: orderID, CustomID: unit.CustomID,
			GrossAmount: unit.Amount.Value, CurrencyCode: unit.Amount.CurrencyCode}
		if captureResp.Status == "VOIDED" && len(unit.Payments.Captures) == 0 {
			return result, ErrOrderClosed
		}
		if captureResp.Status == "APPROVED" {
			return result, ErrOrderApproved
		}
	}
	if captureResp.Status == "PAYER_ACTION_REQUIRED" {
		return nil, payment.ErrCheckoutVerificationRequired
	}
	if captureResp.Status != "COMPLETED" {
		return nil, fmt.Errorf("paypal transaction is not completed: status is %s", captureResp.Status)
	}

	if len(captureResp.PurchaseUnits) > 0 {
		pu := captureResp.PurchaseUnits[0]
		if len(pu.Payments.Captures) > 0 {
			capture := pu.Payments.Captures[0]
			if capture.Status == "DECLINED" {
				if capture.StatusDetails.Reason == "DECLINED_BY_RISK_FRAUD_FILTERS" {
					return nil, payment.ErrCheckoutRiskBlocked
				}
				return nil, payment.ErrCheckoutDeclined
			}
			if (capture.Status != "COMPLETED" && (captureOrder || capture.Status != "REFUNDED")) || capture.ID == "" {
				return nil, errors.New("PayPal capture has not completed")
			}
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

			breakdown := capture.SellerReceivableBreakdown
			var fee, net string
			if breakdown.PayPalFee.CurrencyCode == capture.Amount.CurrencyCode {
				fee = breakdown.PayPalFee.Value
			}
			if breakdown.NetAmount.CurrencyCode == capture.Amount.CurrencyCode {
				net = breakdown.NetAmount.Value
			}

			occurredAt, _ := time.Parse(time.RFC3339, capture.CreateTime)
			return &payment.OrderCapture{
				Account:                account,
				OccurredAt:             occurredAt,
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
