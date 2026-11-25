package billing

import (
	"slices"
	"testing"
)

func TestStripeCountryPresetsAndNormalization(t *testing.T) {
	if len(allISO2Countries) != 249 {
		t.Fatalf("expected 249 ISO-2 countries, got %d", len(allISO2Countries))
	}
	if !slices.IsSorted(allISO2Countries) {
		t.Fatalf("allISO2Countries must be sorted")
	}
	seen := make(map[string]struct{}, len(allISO2Countries))
	for _, code := range allISO2Countries {
		if !countryISO2Pattern.MatchString(code) {
			t.Fatalf("invalid ISO-2 code in preset: %q", code)
		}
		if _, dup := seen[code]; dup {
			t.Fatalf("duplicate ISO-2 code in preset: %q", code)
		}
		seen[code] = struct{}{}
	}

	// Direct mode defaults to all 249 ISO-2 countries when omitted or wildcarded.
	for _, input := range [][]string{nil, {}, {"*"}, {"all"}} {
		req := bindStripeProductReq{
			Environment:      "sandbox",
			SalesMode:        stripeDirect,
			AllowedCountries: slices.Clone(input),
		}
		if err := normalizeStripeBindingReq(&req); err != nil {
			t.Fatalf("direct normalize(%v) failed: %v", input, err)
		}
		if len(req.AllowedCountries) != 249 {
			t.Fatalf("direct normalize(%v): expected 249 countries, got %d", input, len(req.AllowedCountries))
		}
		if !slices.Contains(req.AllowedCountries, "US") || !slices.Contains(req.AllowedCountries, "CN") {
			t.Fatalf("direct global preset should include US and CN")
		}
	}

	// MoR mode defaults to Stripe Managed Payments supported countries (242 = 249 - 7 restricted).
	for _, input := range [][]string{nil, {}, {"*"}, {"ALL"}} {
		req := bindStripeProductReq{
			Environment:         "sandbox",
			SalesMode:           stripeManagedPayments,
			TaxLiabilityAccount: "acct_1MoRTax000000001",
			AllowedCountries:    slices.Clone(input),
		}
		if err := normalizeStripeBindingReq(&req); err != nil {
			t.Fatalf("mor normalize(%v) failed: %v", input, err)
		}
		if len(req.AllowedCountries) != 242 {
			t.Fatalf("mor normalize(%v): expected 242 countries, got %d", input, len(req.AllowedCountries))
		}
		for restricted := range stripeMoRRestrictedCountries {
			if slices.Contains(req.AllowedCountries, restricted) {
				t.Fatalf("mor preset must exclude restricted country %s", restricted)
			}
		}
		if !slices.Contains(req.AllowedCountries, "US") || !slices.Contains(req.AllowedCountries, "GB") || !slices.Contains(req.AllowedCountries, "JP") {
			t.Fatalf("mor preset should include US, GB, JP")
		}
	}

	// Explicit custom subset still normalizes and deduplicates as before.
	custom := bindStripeProductReq{
		Environment:      "sandbox",
		SalesMode:        stripeDirect,
		AllowedCountries: []string{"gb", " us ", "US"},
	}
	if err := normalizeStripeBindingReq(&custom); err != nil {
		t.Fatalf("custom normalize failed: %v", err)
	}
	if !slices.Equal(custom.AllowedCountries, []string{"GB", "US"}) {
		t.Fatalf("unexpected custom countries: %v", custom.AllowedCountries)
	}
}
