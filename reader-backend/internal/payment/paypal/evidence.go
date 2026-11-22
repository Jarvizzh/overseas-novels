package paypal

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	"reader-backend/internal/payment"
)

// CaptureEvidence retains provider state and exact amounts. A missing
// receivable field remains unknown when the channel omits it.
type CaptureEvidence struct {
	Account          payment.WebhookAccount `json:"-"`
	ID               string                 `json:"id"`
	CustomID         string                 `json:"custom_id"`
	Status           string                 `json:"status"`
	StatusDetails    *CaptureStatusDetails  `json:"status_details"`
	Amount           *Amount                `json:"amount"`
	Receivable       *ReceivableBreakdown   `json:"seller_receivable_breakdown"`
	SellerProtection *SellerProtection      `json:"seller_protection"`
	Processor        *ProcessorResponse     `json:"processor_response"`
	CreatedAt        time.Time              `json:"create_time"`
	Relations        []Link                 `json:"links"`
}

type CaptureStatusDetails struct {
	Reason string `json:"reason"`
}

type ReceivableBreakdown struct {
	Gross        *Amount `json:"gross_amount"`
	Fee          *Amount `json:"paypal_fee"`
	Net          *Amount `json:"net_amount"`
	Payable      *Amount `json:"receivable_amount"`
	ExchangeRate *struct {
		SourceCurrency string `json:"source_currency"`
		TargetCurrency string `json:"target_currency"`
		Value          string `json:"value"`
	} `json:"exchange_rate"`
}

type SellerProtection struct {
	Status            string   `json:"status"`
	DisputeCategories []string `json:"dispute_categories"`
}

type ProcessorResponse struct {
	ResponseCode      string `json:"response_code"`
	AVSCode           string `json:"avs_code"`
	CVVCode           string `json:"cvv_code"`
	AdviceCode        string `json:"payment_advice_code"`
	NetworkAdviceCode string `json:"merchant_advice_code"`
}

type OrderEvidence struct {
	Account       payment.WebhookAccount `json:"-"`
	ID            string                 `json:"id"`
	Intent        string                 `json:"intent"`
	Status        string                 `json:"status"`
	Units         []OrderUnit            `json:"purchase_units"`
	Links         []Link                 `json:"links"`
	PaymentSource struct {
		Card *struct {
			Authentication *CardAuthentication `json:"authentication_result"`
		} `json:"card"`
	} `json:"payment_source"`
}

type CardAuthentication struct {
	LiabilityShift string `json:"liability_shift"`
	ThreeDSecure   *struct {
		EnrollmentStatus     string `json:"enrollment_status"`
		AuthenticationStatus string `json:"authentication_status"`
	} `json:"three_d_secure"`
}

type OrderUnit struct {
	CustomID string  `json:"custom_id"`
	Amount   *Amount `json:"amount"`
	Payee    *Payee  `json:"payee"`
	Payments struct {
		Captures []CaptureEvidence `json:"captures"`
	} `json:"payments"`
}

func (p *Client) GetOrder(ctx context.Context, id string) (OrderEvidence, error) {
	if !validObjectID(id) {
		return OrderEvidence{}, errors.New("invalid PayPal Order ID")
	}
	cfg, err := p.credentials(ctx)
	if err != nil {
		return OrderEvidence{}, err
	}
	var result OrderEvidence
	if err := p.send(ctx, cfg, http.MethodGet, "/v2/checkout/orders/"+url.PathEscape(id), "", nil, &result); err != nil {
		return OrderEvidence{}, err
	}
	if result.ID != id {
		return OrderEvidence{}, errors.New("unexpected PayPal Order ID")
	}
	for i, unit := range result.Units {
		if cfg.MerchantID != "" && (unit.Payee == nil || unit.Payee.MerchantID != cfg.MerchantID) {
			return OrderEvidence{}, errors.New("unexpected PayPal order payee")
		}
		for j := range unit.Payments.Captures {
			result.Units[i].Payments.Captures[j].Account = cfg.account()
		}
	}
	result.Account = cfg.account()
	return result, nil
}

func (p *Client) GetCaptureDetails(ctx context.Context, id string) (CaptureEvidence, error) {
	if !validObjectID(id) {
		return CaptureEvidence{}, errors.New("invalid PayPal Capture ID")
	}
	var result CaptureEvidence
	account, err := p.read(ctx, "/v2/payments/captures/"+url.PathEscape(id), &result)
	if err != nil {
		return CaptureEvidence{}, err
	}
	if result.ID != id {
		return CaptureEvidence{}, errors.New("unexpected PayPal Capture ID")
	}
	result.Account = account
	return result, nil
}

type SaleEvidence struct {
	Account        payment.WebhookAccount `json:"-"`
	ID             string                 `json:"id"`
	State          string                 `json:"state"`
	SubscriptionID string                 `json:"billing_agreement_id"`
	Amount         *SaleAmount            `json:"amount"`
	Fee            *SaleAmount            `json:"transaction_fee"`
	CreatedAt      time.Time              `json:"create_time"`
	Links          []Link                 `json:"links"`
}

type SaleAmount struct {
	Currency string `json:"currency"`
	Total    string `json:"total"`
	Value    string `json:"value"`
}

func (p *Client) GetSale(ctx context.Context, id string) (SaleEvidence, error) {
	if !validObjectID(id) {
		return SaleEvidence{}, errors.New("invalid PayPal Sale ID")
	}
	var result SaleEvidence
	account, err := p.read(ctx, "/v1/payments/sale/"+url.PathEscape(id), &result)
	if err != nil {
		return SaleEvidence{}, err
	}
	if result.ID != id {
		return SaleEvidence{}, errors.New("unexpected PayPal Sale ID")
	}
	result.Account = account
	return result, nil
}

// CanCloseSingleCheckout checks an Orders CAPTURE session after Reader has
// fenced all local capture requests for the attempt.
func (p *Client) CanCloseSingleCheckout(ctx context.Context, id, reference string, amount int64, currency string, account payment.WebhookAccount) (bool, error) {
	order, err := p.GetOrder(ctx, id)
	if err != nil {
		return false, err
	}
	if order.Account != account || order.Intent != "CAPTURE" || len(order.Units) != 1 {
		return false, nil
	}
	unit := order.Units[0]
	if unit.CustomID != reference || unit.Amount == nil || unit.Amount.Currency != currency || len(unit.Payments.Captures) != 0 {
		return false, nil
	}
	actual, err := payment.ParseCents(unit.Amount.Value)
	if err != nil || actual != amount {
		return false, err
	}
	switch order.Status {
	case "CREATED", "SAVED", "APPROVED", "PAYER_ACTION_REQUIRED", "VOIDED":
		return true, nil
	default:
		return false, nil
	}
}
