package application

import (
	"reader-backend/internal/model"
	"testing"
	"time"
)

func TestRegularPayPalPeriodEnd(t *testing.T) {
	anchor := time.Date(2026, 1, 31, 10, 0, 0, 0, time.UTC)
	sub := &model.UserSubscription{Cycle: "month"}
	february := time.Date(2026, 2, 28, 10, 0, 0, 0, time.UTC)
	if end := regularPayPalPeriodEnd(sub, anchor, february); !end.Equal(time.Date(2026, 3, 31, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("month boundary drift: %v", end)
	}
	if end := regularPayPalPeriodEnd(sub, anchor, february.Add(24*time.Hour)); !end.IsZero() {
		t.Fatalf("off-boundary payment guessed: %v", end)
	}
	if end := regularPayPalPeriodEnd(sub, time.Time{}, february); !end.IsZero() {
		t.Fatalf("missing billing anchor guessed: %v", end)
	}
}
