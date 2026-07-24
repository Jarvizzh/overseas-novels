package payment

import (
	"strconv"

	"github.com/stripe/stripe-go/v80"
	"github.com/stripe/stripe-go/v80/paymentintent"
	"github.com/stripe/stripe-go/v80/webhook"
	"reader-backend/internal/config"
)

type StripeClient struct{}

func NewStripeClient() *StripeClient {
	stripe.Key = config.AppConfig.StripeSecretKey
	return &StripeClient{}
}

func (s *StripeClient) CreatePaymentIntent(amountCents int64, currency string, userID string, coinsAmount int) (string, string, error) {
	if config.AppConfig.StripeSecretKey == "" || config.AppConfig.StripeSecretKey == "sk_test_placeholder" {
		// Mock payment intent in sandbox placeholder environment
		return "mock_client_secret_" + strconv.Itoa(coinsAmount), "mock_pi_" + strconv.Itoa(coinsAmount), nil
	}

	params := &stripe.PaymentIntentParams{
		Amount:   stripe.Int64(amountCents),
		Currency: stripe.String(currency),
	}
	params.AddMetadata("user_id", userID)
	params.AddMetadata("coins_amount", strconv.Itoa(coinsAmount))

	pi, err := paymentintent.New(params)
	if err != nil {
		return "", "", err
	}

	return pi.ClientSecret, pi.ID, nil
}

func (s *StripeClient) VerifyWebhook(payload []byte, sigHeader string, webhookSecret string) (stripe.Event, error) {
	return webhook.ConstructEvent(payload, sigHeader, webhookSecret)
}
