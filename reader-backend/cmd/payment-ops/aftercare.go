package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"reader-backend/internal/config"
	"reader-backend/internal/db"
	"reader-backend/internal/payment"
	"reader-backend/internal/payment/aftercare"
	"reader-backend/internal/payment/paypal"
	"reader-backend/internal/payment/paypal/credentialstore"
	"reader-backend/internal/payment/stripe"
)

const aftercareMaxPages = 100

func runAftercare(provider, account, environment, id, from, through, reason string, apply bool) error {
	if (provider != "paypal" && provider != "stripe") || (environment != "live" && environment != "sandbox") || strings.TrimSpace(account) == "" {
		return errors.New("aftercare requires an explicit provider, account and environment")
	}
	var start, end time.Time
	if id == "" {
		var err error
		start, err = time.Parse(time.RFC3339, from)
		if err != nil {
			return fmt.Errorf("parse -from: %w", err)
		}
		end, err = time.Parse(time.RFC3339, through)
		if err != nil {
			return fmt.Errorf("parse -through: %w", err)
		}
		if !end.After(start) || end.Sub(start) > 31*24*time.Hour {
			return errors.New("aftercare window must be within 31 days")
		}
	}
	if !apply {
		fmt.Printf("dry-run: read %s payment cases in %s\n", provider, environment)
		return nil
	}
	if strings.TrimSpace(reason) == "" {
		return errors.New("aftercare requires a reason when applying")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	config.LoadConfig()
	db.InitDB()
	defer db.CloseDB()
	repo := aftercare.NewRepository(db.DB)
	log.Printf("task=aftercare_backfill provider=%s environment=%s actor=%q reason=%q", provider, environment, os.Getenv("USER"), reason)
	if provider == "paypal" {
		client := paypal.NewClient(func(ctx context.Context) (paypal.Config, error) {
			return credentialstore.LoadForAccount(ctx, payment.WebhookAccount{AccountKey: account, Environment: environment})
		})
		scope, err := client.CheckoutAccount(ctx)
		if err != nil {
			return err
		}
		if scope.AccountKey != account || scope.Environment != environment {
			return errors.New("PayPal credentials do not match the selected account")
		}
		if strings.HasPrefix(id, "WH-") {
			event, err := client.GetEvent(ctx, id)
			if err != nil {
				return err
			}
			return recordPayPalCaseEvent(ctx, repo, event)
		}
		if id != "" {
			return recordPayPalDispute(ctx, client, repo, id)
		}
		return backfillPayPalCases(ctx, client, repo, start, end)
	}
	client, err := stripe.NewClient(ctx, stripe.Config{Scope: stripe.Scope{AccountID: account, Environment: stripe.Environment(environment)},
		APIKey: config.StripeAftercareAPIKey(account, environment)})
	if err != nil {
		return err
	}
	if strings.HasPrefix(id, "dp_") || strings.HasPrefix(id, "du_") {
		item, err := client.AftercareDispute(ctx, id, "provider_api", id)
		if err != nil {
			return err
		}
		return repo.RecordObservation(ctx, item)
	}
	if id != "" {
		event, err := client.GetEvent(ctx, id)
		if err != nil {
			return err
		}
		return recordStripeCaseEvent(ctx, client, repo, event)
	}
	return backfillStripeCases(ctx, client, repo, start, end)
}

func recordPayPalDispute(ctx context.Context, client *paypal.Client, repo *aftercare.Repository, id string) error {
	item, err := client.GetDispute(ctx, id)
	if err != nil {
		return err
	}
	return repo.Record(ctx, item.CaseFact("provider_api", item.ID, time.Now()))
}

func recordPayPalCaseEvent(ctx context.Context, repo *aftercare.Repository, event paypal.Event) error {
	fact, err := event.CaseFact("provider_api")
	if err != nil || fact == nil {
		return err
	}
	return repo.Record(ctx, *fact)
}

func recordStripeCaseEvent(ctx context.Context, client *stripe.Client, repo *aftercare.Repository, event stripe.Event) error {
	fact, err := client.AftercareEvent(ctx, event)
	if err != nil {
		return err
	}
	return repo.RecordObservation(ctx, fact)
}

func backfillPayPalCases(ctx context.Context, client *paypal.Client, repo *aftercare.Repository, start, end time.Time) error {
	disputes := paypal.DisputeQuery{UpdatedFrom: start, UpdatedBefore: end, PageSize: 50}
	for range aftercareMaxPages {
		page, err := client.ListDisputes(ctx, disputes)
		if err != nil {
			return err
		}
		for _, item := range page.Items {
			if err := recordPayPalDispute(ctx, client, repo, item.ID); err != nil {
				return err
			}
		}
		disputes.Cursor = page.Next
		if page.Next == "" {
			break
		}
	}
	if disputes.Cursor != "" {
		return errors.New("PayPal disputes exceeded pagination budget; narrow the window")
	}
	for _, eventType := range []string{"PAYMENT.SALE.REVERSED", "PAYMENT.CAPTURE.REVERSED", "PAYMENT.CAPTURE.DENIED", "PAYMENT.CAPTURE.PENDING"} {
		query := paypal.EventQuery{From: start, Through: end, Type: eventType, PageSize: 100}
		for range aftercareMaxPages {
			page, err := client.ListEvents(ctx, query)
			if err != nil {
				return err
			}
			for _, event := range page.Items {
				if err := recordPayPalCaseEvent(ctx, repo, event); err != nil {
					return err
				}
			}
			query.Cursor = page.Next
			if page.Next == "" {
				break
			}
		}
		if query.Cursor != "" {
			return errors.New("PayPal events exceeded pagination budget; narrow the window")
		}
	}
	return nil
}

func backfillStripeCases(ctx context.Context, client *stripe.Client, repo *aftercare.Repository, start, end time.Time) error {
	window := stripe.TimeRange{From: start, Before: end}
	query := stripe.EventQuery{Created: window, PageOptions: stripe.PageOptions{Limit: 100}, Types: []string{
		"charge.failed", "charge.succeeded", "payment_intent.payment_failed", "review.opened", "review.closed",
		"radar.early_fraud_warning.created", "radar.early_fraud_warning.updated",
		"charge.dispute.created", "charge.dispute.updated", "charge.dispute.closed", "charge.dispute.funds_withdrawn", "charge.dispute.funds_reinstated",
	}}
	eventsComplete := false
	for range aftercareMaxPages {
		page, err := client.ListEvents(ctx, query)
		if err != nil {
			return err
		}
		for _, event := range page.Items {
			if err := recordStripeCaseEvent(ctx, client, repo, event); err != nil {
				return err
			}
		}
		if !page.HasMore {
			eventsComplete = true
			break
		}
		if len(page.Items) == 0 {
			return errors.New("Stripe event pagination did not advance")
		}
		cursor := page.Items[len(page.Items)-1].ID
		if cursor == query.StartingAfter {
			return errors.New("Stripe repeated the event cursor")
		}
		query.StartingAfter = cursor
	}
	if !eventsComplete {
		return errors.New("Stripe events exceeded pagination budget; narrow the window")
	}
	// Stripe dispute objects can outlive the Events API retention.
	pageOptions := stripe.PageOptions{Limit: 100}
	for range aftercareMaxPages {
		page, err := client.ListDisputes(ctx, window, pageOptions)
		if err != nil {
			return err
		}
		for _, item := range page.Items {
			observation, err := client.AftercareDispute(ctx, item.ID, "provider_api", item.ID)
			if err != nil {
				return err
			}
			if err := repo.RecordObservation(ctx, observation); err != nil {
				return err
			}
		}
		if !page.HasMore {
			return nil
		}
		if len(page.Items) == 0 {
			return errors.New("Stripe dispute pagination did not advance")
		}
		pageOptions.StartingAfter = page.Items[len(page.Items)-1].ID
	}
	return errors.New("Stripe disputes exceeded pagination budget; narrow the window")
}
