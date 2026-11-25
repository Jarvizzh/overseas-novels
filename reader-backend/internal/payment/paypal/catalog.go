package paypal

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"reader-backend/internal/payment"
)

// The existing VIP plans allow three failed payments before suspension.
const vipFailedPaymentThreshold = 3

func (p *Client) createVIPProduct(ctx context.Context, cfg Config) (string, error) {
	productReq := map[string]interface{}{
		"name":        "Star Novel VIP Subscription",
		"description": "Unlimited reading access for Star Novel VIP Members",
		"type":        "DIGITAL",
		"category":    "BOOKS_PERIODICALS_AND_NEWSPAPERS",
	}

	var result struct {
		ID string `json:"id"`
	}
	if err := p.send(ctx, cfg, http.MethodPost, "/v1/catalogs/products", "prod_star_novel_vip_01", productReq, &result); err != nil {
		return "", err
	}
	if result.ID == "" {
		return "", errors.New("PayPal did not return a subscription product ID")
	}
	return result.ID, nil
}

// CreatePlan publishes the recurring price for a subscription.
func (p *Client) CreatePlan(ctx context.Context, param payment.CreateSubscriptionPlanParam) (string, error) {
	var intervalUnit string
	switch param.Cycle {
	case payment.CycleDay:
		intervalUnit = "DAY"
	case payment.CycleWeek:
		intervalUnit = "WEEK"
	case payment.CycleMonth:
		intervalUnit = "MONTH"
	case payment.CycleYear:
		intervalUnit = "YEAR"
	default:
		return "", errors.New("invalid PayPal subscription interval")
	}
	if param.PriceCents <= 0 || param.PlanName == "" || !validRequestKey(param.RequestID) {
		return "", errors.New("PayPal subscription plan requires a name, price and idempotency key")
	}

	cfg, err := p.credentials(ctx)
	if err != nil {
		return "", err
	}
	if param.Account != cfg.account() {
		return "", errors.New("PayPal subscription plan account changed")
	}
	productID, err := p.createVIPProduct(ctx, cfg)
	if err != nil {
		return "", err
	}
	currency := param.Currency
	if currency == "" {
		currency = "USD"
	}
	priceVal := formatAmount(param.PriceCents, currency)

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
			"setup_fee_failure_action":  "CONTINUE",
			"payment_failure_threshold": vipFailedPaymentThreshold,
		},
	}

	var result struct {
		ID string `json:"id"`
	}
	requestKey := uuid.NewSHA1(uuid.NameSpaceOID, []byte("paypal:plan:"+param.RequestID)).String()
	if err := p.send(ctx, cfg, http.MethodPost, "/v1/billing/plans", requestKey, planReq, &result); err != nil {
		return "", err
	}
	if result.ID == "" {
		return "", errors.New("PayPal did not return a billing plan ID")
	}
	return result.ID, nil
}

type Product struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Type        string `json:"type"`
	Category    string `json:"category"`
}

type Plan struct {
	ID                 string          `json:"id"`
	ProductID          string          `json:"product_id"`
	Name               string          `json:"name"`
	Status             string          `json:"status"`
	BillingCycles      json.RawMessage `json:"billing_cycles"`
	PaymentPreferences struct {
		SetupFee *Amount `json:"setup_fee"`
	} `json:"payment_preferences"`
}

func (p *Client) GetProduct(ctx context.Context, id string) (Product, error) {
	if !validObjectID(id) {
		return Product{}, errors.New("invalid PayPal Product ID")
	}
	var result Product
	err := p.request(ctx, http.MethodGet, "/v1/catalogs/products/"+url.PathEscape(id), "", nil, &result)
	return result, err
}

func (p *Client) GetPlan(ctx context.Context, id string) (Plan, error) {
	if !validObjectID(id) {
		return Plan{}, errors.New("invalid PayPal Plan ID")
	}
	var result Plan
	err := p.request(ctx, http.MethodGet, "/v1/billing/plans/"+url.PathEscape(id), "", nil, &result)
	return result, err
}

// VerifyVIPPlan checks the exact payable terms when recovering a provisioned Plan.
func (p *Client) VerifyVIPPlan(ctx context.Context, id string, offer payment.CreateSubscriptionPlanParam) error {
	if !validObjectID(id) {
		return errors.New("invalid PayPal Plan ID")
	}
	cfg, err := p.credentials(ctx)
	if err != nil {
		return err
	}
	if cfg.account() != offer.Account {
		return errors.New("PayPal Plan account changed")
	}
	var plan Plan
	if err := p.send(ctx, cfg, http.MethodGet, "/v1/billing/plans/"+url.PathEscape(id), "", nil, &plan); err != nil {
		return err
	}
	var cycles []struct {
		Tenure    string `json:"tenure_type"`
		Total     *int   `json:"total_cycles"`
		Frequency struct {
			Unit  string `json:"interval_unit"`
			Count int    `json:"interval_count"`
		} `json:"frequency"`
		Pricing struct {
			FixedPrice Amount `json:"fixed_price"`
		} `json:"pricing_scheme"`
	}
	if err := json.Unmarshal(plan.BillingCycles, &cycles); err != nil {
		return err
	}
	if plan.ID != id || plan.Status != "ACTIVE" || len(cycles) != 1 {
		return errors.New("expected an active PayPal Plan with a single billing cycle")
	}
	cycle := cycles[0]
	price := cycle.Pricing.FixedPrice
	minor, err := payment.ParseCents(price.Value)
	if err != nil || price.Currency != offer.Currency || minor != offer.PriceCents ||
		cycle.Tenure != "REGULAR" || cycle.Total == nil || *cycle.Total != 0 ||
		cycle.Frequency.Count != 1 || !strings.EqualFold(cycle.Frequency.Unit, string(offer.Cycle)) {
		return errors.New("PayPal Plan terms differ from the published offer")
	}
	if fee := plan.PaymentPreferences.SetupFee; fee != nil {
		if fee.Currency != offer.Currency || (fee.Value != "0" && fee.Value != "0.0" && fee.Value != "0.00") {
			return errors.New("PayPal Plan contains an unexpected setup fee")
		}
	}
	return nil
}
