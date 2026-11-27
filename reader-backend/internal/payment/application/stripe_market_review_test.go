package application

import (
	"testing"

	"reader-backend/internal/payment"
)

func TestStripeMarketNoteDoesNotRequireFixedTaxAccount(t *testing.T) {
	offer := payment.SingleCheckoutOffer{
		SalesMode: payment.StripeSaleMoR,
		Countries: []string{"US", "GB"},
	}

	cases := []struct {
		name     string
		evidence stripeMarketEvidence
		wantNote string
	}{
		{
			name: "approved market with empty tax account and verified tax passes",
			evidence: stripeMarketEvidence{
				SalesMode:          payment.StripeSaleMoR,
				Country:            "US",
				TaxAccount:         "",
				AutomaticTaxStatus: "complete",
				CustomerTaxStatus:  "none",
				TaxVerified:        true,
			},
			wantNote: "",
		},
		{
			name: "approved market with differing historical tax account and verified tax passes",
			evidence: stripeMarketEvidence{
				SalesMode:          payment.StripeSaleMoR,
				Country:            "GB",
				ExpectedCountry:    "GB",
				TaxAccount:         "acct_StripeMoRNew",
				ExpectedTaxAccount: "acct_HistoricalOld",
				TaxLiabilityType:   "account",
				AutomaticTaxStatus: "complete",
				CustomerTaxStatus:  "none",
				TaxVerified:        true,
			},
			wantNote: "",
		},
		{
			name: "country outside approved markets triggers review",
			evidence: stripeMarketEvidence{
				SalesMode:   payment.StripeSaleMoR,
				Country:     "FR",
				TaxVerified: true,
			},
			wantNote: "Stripe billing country is outside the approved markets",
		},
		{
			name: "country changed from prior verified market triggers review",
			evidence: stripeMarketEvidence{
				SalesMode:       payment.StripeSaleMoR,
				Country:         "GB",
				ExpectedCountry: "US",
				TaxVerified:     true,
			},
			wantNote: "Stripe billing country changed and needs verification",
		},
		{
			name: "unverified MoR tax evidence triggers review",
			evidence: stripeMarketEvidence{
				SalesMode:          payment.StripeSaleMoR,
				Country:            "US",
				TaxLiabilityType:   "self",
				AutomaticTaxStatus: "complete",
				CustomerTaxStatus:  "none",
				TaxVerified:        false,
			},
			wantNote: "Stripe Managed Payments tax liability needs verification",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := stripeMarketNote(offer, tc.evidence); got != tc.wantNote {
				t.Fatalf("stripeMarketNote() = %q, want %q", got, tc.wantNote)
			}
		})
	}
}
