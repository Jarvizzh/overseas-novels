package payment

import "testing"

func TestParseCentsUsesExactDecimalAmounts(t *testing.T) {
	for _, tc := range []struct {
		amount string
		want   int64
	}{
		{"9.99", 999}, {"4.10", 410}, {"4.1", 410}, {"4", 400}, {"0.01", 1},
	} {
		t.Run(tc.amount, func(t *testing.T) {
			got, err := ParseCents(tc.amount)
			if err != nil || got != tc.want {
				t.Fatalf("got %d, %v; want %d", got, err, tc.want)
			}
		})
	}
	for _, invalid := range []string{"", "0", "-1.00", "1.001", "1e3", "NaN", "9999999999999999999999"} {
		if _, err := ParseCents(invalid); err == nil {
			t.Errorf("accepted invalid amount %q", invalid)
		}
	}
}
