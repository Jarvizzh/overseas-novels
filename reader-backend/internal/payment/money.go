package payment

import (
	"errors"
	"strconv"
	"strings"

	"github.com/shopspring/decimal"
	"golang.org/x/text/currency"
)

func ParseCents(amount string) (int64, error) {
	whole, fraction, found := strings.Cut(amount, ".")
	if !found {
		fraction = "00"
	}
	if len(fraction) == 1 {
		fraction += "0"
	}
	if whole == "" || len(fraction) != 2 || strings.Trim(whole+fraction, "0123456789") != "" {
		return 0, errors.New("invalid payment amount")
	}
	cents, err := strconv.ParseInt(whole+fraction, 10, 64)
	if err != nil || cents <= 0 {
		return 0, errors.New("invalid payment amount")
	}
	return cents, nil
}

// CurrencyScale returns the standard decimal exponent for a known ISO currency.
func CurrencyScale(code string) (int32, error) {
	unit, err := currency.ParseISO(code)
	if err != nil || unit == currency.XXX {
		return 0, errors.New("unknown payment currency")
	}
	scale, _ := currency.Standard.Rounding(unit)
	return int32(scale), nil
}

// ExactMinorUnits converts signed decimal amounts without rounding or overflow.
func ExactMinorUnits(amount decimal.Decimal, scale int32) (int64, error) {
	// Bound the exponent before rescaling numbers from financial reports.
	const maxDecimalExponent = 32
	if amount.Exponent() < -maxDecimalExponent || amount.Exponent() > maxDecimalExponent {
		return 0, errors.New("payment amount exceeds supported decimal precision")
	}
	minor := amount.Shift(scale)
	if !minor.IsInteger() || !minor.BigInt().IsInt64() {
		return 0, errors.New("payment amount exceeds minor-unit precision or range")
	}
	return minor.IntPart(), nil
}
