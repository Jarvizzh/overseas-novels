package conversion

import "testing"

func TestPromotionEnvironmentFailClosed(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"production", "live"},
		{"", "live"},
		{"staging", "live"},
		{"unknown", "live"},
		{"sandbox", "sandbox"},
		{"test", "sandbox"},
	}
	for _, tc := range cases {
		if got := PromotionEnvironmentFor(tc.in); got != tc.want {
			t.Fatalf("PromotionEnvironmentFor(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}

	t.Setenv("CONVERSION_ENVIRONMENT", "")
	if got := PromotionEnvironment(); got != "live" {
		t.Fatalf("default PromotionEnvironment() = %q, want live", got)
	}
	t.Setenv("CONVERSION_ENVIRONMENT", "staging")
	if got := PromotionEnvironment(); got != "live" {
		t.Fatalf("unrecognized PromotionEnvironment() = %q, want live", got)
	}
	t.Setenv("CONVERSION_ENVIRONMENT", "sandbox")
	if got := PromotionEnvironment(); got != "sandbox" {
		t.Fatalf("sandbox PromotionEnvironment() = %q, want sandbox", got)
	}
}
