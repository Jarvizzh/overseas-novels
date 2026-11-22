package payment

import "time"

type OrderCapture struct {
	Account                WebhookAccount `json:"-"`
	OccurredAt             time.Time      `json:"-"`
	OrderID                string         `json:"order_id"`
	CaptureID              string         `json:"capture_id"`
	CustomID               string         `json:"custom_id"`
	CurrencyCode           string         `json:"currency_code"`
	GrossAmount            string         `json:"gross_amount"`
	FeeAmount              string         `json:"fee_amount"`
	NetAmount              string         `json:"net_amount"`
	PayerID                string         `json:"payer_id"`
	PayerEmail             string         `json:"payer_email"`
	PayerName              string         `json:"payer_name"`
	PayerCountry           string         `json:"payer_country"`
	Status                 string         `json:"status"`
	SellerProtectionStatus string         `json:"seller_protection_status"`
	RawPayload             string         `json:"raw_payload"`
}

type SubscriptionTransaction struct {
	ID     string    `json:"id"`
	Status string    `json:"status"`
	Time   time.Time `json:"time"`
	Amount struct {
		Gross struct {
			Value    string `json:"value"`
			Currency string `json:"currency_code"`
		} `json:"gross_amount"`
	} `json:"amount_with_breakdown"`
}

type ReceiptType string

const (
	ReceiptCapture ReceiptType = "capture"
	ReceiptSale    ReceiptType = "sale"
)

type RefundRequest struct {
	Account     WebhookAccount
	ReceiptType ReceiptType
	ReceiptID   string
	RefundID    string
	RequestKey  string
	AmountMinor int64
	Currency    string
	Send        bool
}

type RefundResult struct {
	ID          string
	Status      string
	AmountMinor int64
	Currency    string
}
