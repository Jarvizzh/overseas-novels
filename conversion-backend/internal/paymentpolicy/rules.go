// Package paymentpolicy evaluates and versions payment-level reporting rules.
package paymentpolicy

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/text/currency"
)

const (
	ReaderApp = "reader"
	AllPaid   = "all_paid"
	FirstPaid = "first_paid_on_promotion"

	All   = "all"
	Ratio = "ratio"
	Cycle = "cycle"

	Selected = "selected"
	Skipped  = "skipped"

	MaxSequence   = 1<<53 - 1
	sampleVersion = 1
	basisPoints   = 10_000
)

type Scope struct {
	Environment   string `json:"-"`
	IntegrationID int64  `json:"-"`
	App           string `json:"app_id"`
	Promotion     string `json:"promotion_id,omitempty"`
}

func (s Scope) kind() string {
	if s.Promotion == "" {
		return "default"
	}
	return "promotion"
}

type Facts struct {
	Scope
	PaymentKey  string
	Destination int64
	Kind        string
	AmountMinor *int64
	Currency    string
	First       *bool
	LookupFirst FirstPaymentLookup
}

type Action struct {
	Mode      string `json:"mode"`
	RateBPS   *int   `json:"rate_bps,omitempty"`
	SendCount *int64 `json:"send_count,omitempty"`
	SkipCount *int64 `json:"skip_count,omitempty"`
}

type BandInput struct {
	Min *int64 `json:"min_minor,string"`
	Max *int64 `json:"max_minor,string"`
	Action
}

type Branch struct {
	ID int64 `json:"branch_id,string,omitempty"`
	Action
}

type Band struct {
	Branch
	Currency string `json:"currency"`
	Min      int64  `json:"min_minor,string"`
	Max      *int64 `json:"max_minor,string"`
}

type Group struct {
	Kind     string  `json:"payment_kind"`
	Bands    []Band  `json:"bands"`
	Fallback *Branch `json:"fallback,omitempty"`
}

type Config struct {
	Range  string  `json:"payment_range"`
	Groups []Group `json:"groups"`
}

func defaultConfig() Config { return Config{Range: AllPaid, Groups: []Group{}} }

func (a Action) valid() bool {
	switch a.Mode {
	case All:
		return a.RateBPS == nil && a.SendCount == nil && a.SkipCount == nil
	case Ratio:
		return a.RateBPS != nil && *a.RateBPS >= 0 && *a.RateBPS <= basisPoints &&
			a.SendCount == nil && a.SkipCount == nil
	case Cycle:
		return a.RateBPS == nil && a.SendCount != nil && a.SkipCount != nil &&
			*a.SendCount > 0 && *a.SkipCount > 0 && *a.SkipCount <= MaxSequence &&
			*a.SendCount <= MaxSequence-*a.SkipCount
	default:
		return false
	}
}

type ValidationError struct {
	Code  string
	Field string
}

func (e *ValidationError) Error() string { return e.Code + ": " + e.Field }

func invalid(code, field string) error { return &ValidationError{Code: code, Field: field} }

func currencyUnit(code string) (currency.Unit, bool) {
	unit, err := currency.ParseISO(code)
	return unit, err == nil && code == unit.String() && code != "XXX"
}

// MinorAmount converts a non-negative decimal amount without rounding.
func MinorAmount(decimal, code string) (int64, error) {
	unit, ok := currencyUnit(code)
	if !ok {
		return 0, invalid("invalid_currency", "currency")
	}
	scale, _ := currency.Standard.Rounding(unit)
	whole, fraction, dot := strings.Cut(decimal, ".")
	if !digits(whole) || (dot && (!digits(fraction) || len(fraction) > scale)) {
		return 0, invalid("invalid_amount", "amount")
	}
	minor, err := strconv.ParseInt(whole+fraction+strings.Repeat("0", scale-len(fraction)), 10, 64)
	if err != nil {
		return 0, invalid("invalid_amount", "amount")
	}
	return minor, nil
}

func digits(s string) bool {
	return s != "" && strings.IndexFunc(s, func(r rune) bool { return r < '0' || r > '9' }) == -1
}

type match struct {
	Branch
	Reason string
}

var ErrFactMissing = fmt.Errorf("rule_fact_missing")

func (c Config) match(f Facts) (match, error) {
	if c.Range == FirstPaid {
		if f.First == nil {
			return match{}, ErrFactMissing
		}
		if !*f.First {
			return match{Reason: "rule_not_first_payment"}, nil
		}
	}
	// A configured concrete group replaces the entire common group.
	var group *Group
	for i := range c.Groups {
		g := &c.Groups[i]
		if g.Kind == f.Kind && f.Kind != "" {
			group = g
			break
		}
		if g.Kind == "" {
			group = g
		} else if f.Kind == "" {
			return match{}, ErrFactMissing
		}
	}
	if group != nil {
		for _, b := range group.Bands {
			if f.Currency == "" {
				return match{}, ErrFactMissing
			}
			if b.Currency != f.Currency {
				continue
			}
			if f.AmountMinor == nil {
				return match{}, ErrFactMissing
			}
			if *f.AmountMinor >= b.Min && (b.Max == nil || *f.AmountMinor < *b.Max) {
				return match{Branch: b.Branch}, nil
			}
		}
		if group.Fallback != nil {
			return match{Branch: *group.Fallback}, nil
		}
	}
	return match{Branch: Branch{Action: Action{Mode: All}}, Reason: "rule_no_match_send"}, nil
}

// sample encodes the byte length of each UTF-8 field as a four-byte big-endian
// prefix. Keep this encoding and the field order stable for sampleVersion.
func sample(f Facts) int {
	input := make([]byte, 0, len(f.Environment)+len(f.App)+len(f.Promotion)+len(f.PaymentKey)+40)
	for _, value := range [...]string{f.Environment, strconv.FormatInt(f.IntegrationID, 10), f.App, f.Promotion, f.PaymentKey} {
		input = binary.BigEndian.AppendUint32(input, uint32(len(value)))
		input = append(input, value...)
	}
	digest := sha256.Sum256(input)
	return int(binary.BigEndian.Uint64(digest[:8]) % basisPoints)
}

func cmpBand(a, b Band) int {
	if a.Currency != b.Currency {
		return strings.Compare(a.Currency, b.Currency)
	}
	switch {
	case a.Min < b.Min:
		return -1
	case a.Min > b.Min:
		return 1
	default:
		return 0
	}
}

func adjacent(bands []Band) bool {
	for i := 1; i < len(bands); i++ {
		left, right := bands[i-1], bands[i]
		if left.Currency != right.Currency || left.Max == nil || *left.Max != right.Min {
			return false
		}
	}
	return true
}

func nonoverlapping(bands []Band) bool {
	slices.SortFunc(bands, cmpBand)
	for i := 1; i < len(bands); i++ {
		left, right := bands[i-1], bands[i]
		if left.Currency == right.Currency && (left.Max == nil || *left.Max > right.Min) {
			return false
		}
	}
	return true
}
