package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"reader-backend/internal/db"
	"reader-backend/internal/payment"
	"reader-backend/internal/payment/directory"
	"reader-backend/internal/payment/settlement"
	"reader-backend/internal/payment/stripe"
)

func parseStripeWindow(from, through string) (time.Time, time.Time, error) {
	start, startErr := time.Parse(time.RFC3339, from)
	end, endErr := time.Parse(time.RFC3339, through)
	if startErr != nil || endErr != nil || !end.After(start) {
		return time.Time{}, time.Time{}, errors.New("stripe-payout requires an increasing RFC3339 -from and -through window")
	}
	return start, end, nil
}

func (c settlementCommand) syncStripePayouts(ctx context.Context, repo *settlement.Repository, audit settlement.Audit) error {
	dir := directory.New(db.DB, payment.NewAdapters())
	client, err := dir.ResolveStripeClient(ctx, payment.WebhookAccount{AccountKey: c.account, Environment: c.environment}, stripe.PurposePayout)
	if err != nil {
		return err
	}
	sync := func(id string) error {
		result, err := settlement.ImportStripePayout(ctx, client, repo, id, audit)
		if err != nil {
			return fmt.Errorf("import Stripe payout %s: %w", id, err)
		}
		return writeSettlementResult(struct {
			PayoutID string `json:"payout_id"`
			settlement.ImportResult
		}{id, result}, nil)
	}
	if c.id != "" {
		return sync(c.id)
	}
	from, through, err := parseStripeWindow(c.from, c.through)
	if err != nil {
		return err
	}
	query := stripe.TimeRange{From: from, Before: through}
	cursor := stripe.PageOptions{Limit: 100}
	for {
		page, err := client.ListPayouts(ctx, query, cursor)
		if err != nil {
			return err
		}
		for _, payout := range page.Items {
			if payout.Type != "bank_account" || payout.OriginalPayout != nil {
				continue
			}
			if err := sync(payout.ID); err != nil {
				return err
			}
		}
		if !page.HasMore {
			return nil
		}
		if len(page.Items) == 0 || page.Items[len(page.Items)-1].ID == cursor.StartingAfter {
			return errors.New("Stripe payout cursor did not advance")
		}
		cursor.StartingAfter = page.Items[len(page.Items)-1].ID
	}
}
