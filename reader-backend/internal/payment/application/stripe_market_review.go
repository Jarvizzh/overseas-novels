package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"reader-backend/internal/payment"
)

type stripeMarketEvidence struct {
	Reference          string     `json:"reference"`
	SalesMode          string     `json:"sales_mode"`
	Country            string     `json:"country"`
	ExpectedCountry    string     `json:"expected_country"`
	TaxAccount         string     `json:"tax_account"`
	ExpectedTaxAccount string     `json:"expected_tax_account"`
	CustomerTaxStatus  string     `json:"customer_tax_status"`
	TaxVerified        bool       `json:"tax_verified"`
	AmountMinor        int64      `json:"amount_minor"`
	FundingReferences  []string   `json:"funding_references,omitempty"`
	PeriodStart        *time.Time `json:"period_start,omitempty"`
	PeriodEnd          *time.Time `json:"period_end,omitempty"`
}

func stripeMarketNote(offer payment.SingleCheckoutOffer, evidence stripeMarketEvidence) string {
	switch {
	case !slices.Contains(offer.Countries, evidence.Country):
		return "Stripe billing country is outside the approved markets"
	case evidence.ExpectedCountry != "" && evidence.ExpectedCountry != evidence.Country:
		return "Stripe billing country changed and needs verification"
	case offer.SalesMode == payment.StripeSaleMoR && !evidence.TaxVerified:
		return "Stripe Managed Payments tax liability needs verification"
	}
	return ""
}

func stripeMarketReview(ctx context.Context, tx pgx.Tx, orderID int64, evidence stripeMarketEvidence, note string) (string, error) {
	if note == "" {
		return "", nil
	}
	body, err := json.Marshal(evidence)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(body)
	var approved bool
	err = tx.QueryRow(ctx, `INSERT INTO payment_stripe_market_reviews(order_id,evidence_key,evidence) VALUES($1,$2,$3)
		ON CONFLICT(order_id,evidence_key) DO UPDATE SET updated_at=clock_timestamp()
		RETURNING approved_at IS NOT NULL`, orderID, hex.EncodeToString(hash[:]), body).Scan(&approved)
	if err != nil || approved {
		return "", err
	}
	return note, nil
}
