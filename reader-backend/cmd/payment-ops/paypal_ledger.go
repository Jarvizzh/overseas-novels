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
	"reader-backend/internal/payment/ledger"
	"reader-backend/internal/payment/paypal"
	"reader-backend/internal/payment/paypal/credentialstore"
)

const paypalLedgerRunTimeout = 15 * time.Minute

func runPayPalLedger(from, through, accountKey, environment, reason string, apply bool) error {
	start, fromErr := time.Parse(time.RFC3339, from)
	end, throughErr := time.Parse(time.RFC3339, through)
	if fromErr != nil || throughErr != nil || !end.After(start) {
		return errors.New("paypal-ledger requires an increasing RFC3339 -from and -through window")
	}
	if environment != paypal.EnvironmentSandbox && environment != paypal.EnvironmentLive {
		return errors.New("paypal-ledger requires an explicit sandbox or live -environment")
	}
	if !apply {
		fmt.Printf("dry-run: read PayPal balances in %s for %s through %s\n", environment, start, end)
		return nil
	}
	if strings.TrimSpace(reason) == "" {
		return errors.New("paypal-ledger requires an audit reason when applying")
	}
	config.LoadConfig()
	db.InitDB()
	defer db.CloseDB()
	ctx, cancel := context.WithTimeout(context.Background(), paypalLedgerRunTimeout)
	defer cancel()
	client := paypal.NewClient(func(ctx context.Context) (paypal.Config, error) {
		return credentialstore.LoadForAccount(ctx, payment.WebhookAccount{AccountKey: accountKey, Environment: environment})
	})
	account, err := client.CheckoutAccount(ctx)
	if err != nil {
		return err
	}
	if account.Environment != environment {
		return errors.New("configured PayPal account does not match the requested ledger environment")
	}
	repo := ledger.NewRepository(db.DB)
	log.Printf("paypal_ledger_started environment=%s from=%s through=%s operator=%q reason=%q", environment, start, end, os.Getenv("USER"), reason)
	for page := 1; ; page++ {
		report, err := client.ListLedgerMovements(ctx, start, end, page)
		if err != nil {
			return fmt.Errorf("fetch PayPal balance movements page %d: %w", page, err)
		}
		var conflicts int
		for _, entry := range report.Entries {
			if entry.Account != account {
				return fmt.Errorf("PayPal reporting account changed while processing page %d", page)
			}
			result, err := repo.Record(ctx, entry)
			if err != nil {
				return fmt.Errorf("record PayPal balance movements page %d: %w", page, err)
			}
			if result.Conflict {
				conflicts++
			}
		}
		log.Printf("paypal_ledger_page environment=%s page=%d movements=%d need_review=%d", environment, page, len(report.Entries), conflicts)
		if !report.HasMore {
			return nil
		}
	}
}
