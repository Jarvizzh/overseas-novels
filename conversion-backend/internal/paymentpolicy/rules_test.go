package paymentpolicy

import (
	"errors"
	"testing"
)

func ptr[T any](value T) *T { return &value }

func TestMinorAmountAndSample(t *testing.T) {
	for _, tc := range []struct {
		currency, amount string
		minor            int64
		bad              bool
	}{
		{"USD", "30.00", 3000, false}, {"JPY", "30", 30, false}, {"BHD", "3.1", 3100, false},
		{"USD", "30.001", 0, true}, {"JPY", "30.0", 0, true}, {"USD", "1e3", 0, true},
		{"USD", "92233720368547758.08", 0, true}, {"ZZZ", "30", 0, true},
	} {
		got, err := MinorAmount(tc.amount, tc.currency)
		if got != tc.minor || (err != nil) != tc.bad {
			t.Errorf("%s %s = %d, %v", tc.currency, tc.amount, got, err)
		}
	}
	facts := Facts{Scope: Scope{Environment: "test", IntegrationID: 7, App: ReaderApp, Promotion: "13"}, PaymentKey: "订阅-42"}
	if got := sample(facts); got != 2901 {
		t.Fatalf("sample encoding changed: %d", got)
	}
}

func TestPaymentMatch(t *testing.T) {
	config := Config{Range: AllPaid, Groups: []Group{
		{Kind: "single", Fallback: &Branch{ID: 8, Action: Action{Mode: Ratio, RateBPS: ptr(5000)}}},
		{Bands: []Band{
			{Currency: "USD", Min: 3000, Max: ptr(int64(9000)), Branch: Branch{ID: 1, Action: Action{Mode: All}}},
			{Currency: "USD", Min: 9000, Branch: Branch{ID: 2, Action: Action{Mode: All}}},
			{Currency: "EUR", Min: 3000, Branch: Branch{ID: 3, Action: Action{Mode: All}}},
		}},
	}}
	for _, tc := range []struct {
		kind, currency string
		amount         *int64
		id             int64
		missing        bool
	}{
		{"single", "", nil, 8, false},
		{"single", "USD", ptr(int64(5000)), 8, false},
		{"whole_book", "USD", ptr(int64(2999)), 0, false},
		{"whole_book", "USD", ptr(int64(3000)), 1, false},
		{"whole_book", "USD", ptr(int64(9000)), 2, false},
		{"whole_book", "EUR", ptr(int64(9000)), 3, false},
		{"whole_book", "JPY", nil, 0, false},
		{"whole_book", "USD", nil, 0, true},
		{"whole_book", "", nil, 0, true},
	} {
		got, err := config.match(Facts{Kind: tc.kind, AmountMinor: tc.amount, Currency: tc.currency})
		if got.ID != tc.id || errors.Is(err, ErrFactMissing) != tc.missing {
			t.Errorf("%+v: %+v, %v", tc, got, err)
		}
	}
	config.Range = FirstPaid
	if got, err := config.match(Facts{First: ptr(false)}); got.Reason != "rule_not_first_payment" || err != nil {
		t.Fatalf("first payment check did not precede amount matching: %+v, %v", got, err)
	}
	if _, err := config.match(Facts{}); !errors.Is(err, ErrFactMissing) {
		t.Fatalf("unknown first payment = %v", err)
	}
}

func TestDraftRejectsInvalidEdits(t *testing.T) {
	cycle := Action{Mode: Cycle, SendCount: ptr(int64(3)), SkipCount: ptr(int64(2))}
	band := BandInput{Min: ptr(int64(3000)), Max: ptr(int64(9000)), Action: cycle}
	base := Config{Range: AllPaid, Groups: []Group{{Bands: []Band{
		{Currency: "USD", Min: 3000, Max: ptr(int64(9000)), Branch: Branch{ID: 1, Action: cycle}},
	}}}}
	for name, op := range map[string]Operation{
		"bad currency":   {Type: "add_band", Currency: "XXX", Bands: []BandInput{band}},
		"overlap":        {Type: "add_band", Currency: "USD", Bands: []BandInput{band}},
		"missing min":    {Type: "add_band", Currency: "USD", Bands: []BandInput{{Action: cycle}}},
		"unknown mode":   {Type: "set_fallback", Action: &Action{Mode: "disabled"}},
		"cycle overflow": {Type: "set_fallback", Action: &Action{Mode: Cycle, SendCount: ptr(int64(MaxSequence)), SkipCount: ptr(int64(1))}},
		"foreign group":  {Type: "set_fallback", Kind: "vip", Action: &cycle},
		"partial split": {Type: "split_band", BranchID: 1, Bands: []BandInput{
			{Min: ptr(int64(3001)), Max: ptr(int64(6000)), Action: cycle},
			{Min: ptr(int64(6000)), Max: ptr(int64(9000)), Action: cycle},
		}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := applyDraft(base, Draft{Operations: []Operation{op}}, Capabilities{}); err == nil {
				t.Fatal("accepted invalid edit")
			}
		})
	}
}
