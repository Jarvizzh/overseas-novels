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

	// MoR mode defaults to Stripe Managed Payments supported countries (242 = 249 - 7 restricted)
	// without requiring a pre-filled tax_liability_account.
	for _, input := range [][]string{nil, {}, {"*"}, {"ALL"}} {
		req := bindStripeProductReq{
			Environment:      "sandbox",
			SalesMode:        stripeManagedPayments,
			AllowedCountries: slices.Clone(input),
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

func TestStripeMoRDefaultTaxCodesByProductType(t *testing.T) {
	cases := []struct {
		productType string
		wantTaxCode string
	}{
		{productType: productTypeVIP, wantTaxCode: stripeMoRTaxCodeVIP},
		{productType: productTypeWholeBook, wantTaxCode: stripeMoRTaxCodeWholeBook},
		{productType: productTypeSingle, wantTaxCode: stripeMoRTaxCodeSingle},
	}
	for _, tc := range cases {
		req := bindStripeProductReq{
			Environment:     "live",
			SalesMode:       stripeManagedPayments,
			ReleaseEvidence: "approval-2026-11-27",
			MarketPolicy:    "US-approved",
		}
		if err := normalizeStripeBindingReq(&req); err != nil {
			t.Fatalf("normalizeStripeBindingReq(%s) failed: %v", tc.productType, err)
		}
		if req.TaxBehavior != "exclusive" {
			t.Fatalf("expected default tax_behavior=exclusive, got %q", req.TaxBehavior)
		}
		if err := resolveStripeBindingTaxCode(&req, tc.productType); err != nil {
			t.Fatalf("resolveStripeBindingTaxCode(%s) failed: %v", tc.productType, err)
		}
		if req.TaxCode != tc.wantTaxCode {
			t.Fatalf("product_type=%s: got tax_code=%q, want %q", tc.productType, req.TaxCode, tc.wantTaxCode)
		}
		if req.TaxCode == "txcd_10103001" {
			t.Fatalf("product_type=%s must not fall back to legacy SaaS tax code txcd_10103001", tc.productType)
		}
	}

	// Explicit valid override is preserved.
	override := bindStripeProductReq{
		Environment: "sandbox",
		SalesMode:   stripeManagedPayments,
		TaxCode:     "txcd_20030000",
	}
	if err := normalizeStripeBindingReq(&override); err != nil {
		t.Fatalf("override normalize failed: %v", err)
	}
	if err := resolveStripeBindingTaxCode(&override, productTypeVIP); err != nil || override.TaxCode != "txcd_20030000" {
		t.Fatalf("expected explicit tax_code to be preserved, got %q err=%v", override.TaxCode, err)
	}

	// Unsupported product type fails closed for MoR default resolution.
	unknown := bindStripeProductReq{Environment: "sandbox", SalesMode: stripeManagedPayments}
	if err := resolveStripeBindingTaxCode(&unknown, "unknown_type"); err == nil {
		t.Fatal("expected error for unsupported product type in MoR tax code resolution")
	}

	// Direct mode leaves TaxCode empty.
	direct := bindStripeProductReq{Environment: "sandbox", SalesMode: stripeDirect}
	if err := resolveStripeBindingTaxCode(&direct, productTypeVIP); err != nil || direct.TaxCode != "" {
		t.Fatalf("direct mode should leave tax_code empty, got %q err=%v", direct.TaxCode, err)
	}

	// Live bindings still require release_evidence and market_policy.
	missingEvidence := bindStripeProductReq{Environment: "live", SalesMode: stripeManagedPayments}
	if err := normalizeStripeBindingReq(&missingEvidence); err == nil {
		t.Fatal("expected live MoR binding without release_evidence/market_policy to fail")
	}
}
