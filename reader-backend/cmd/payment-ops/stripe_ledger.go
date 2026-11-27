package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/user"
	"strings"
	"time"

	"reader-backend/internal/config"
	"reader-backend/internal/db"
	"reader-backend/internal/payment"
	"reader-backend/internal/payment/directory"
	"reader-backend/internal/payment/ledger"
	"reader-backend/internal/payment/settlement"
	"reader-backend/internal/payment/stripe"
)

const stripeLedgerRunTimeout = 15 * time.Minute

func runStripeLedger(account, environment, id, reason string, apply bool) error {
	scope := stripe.Scope{AccountID: account, Environment: stripe.Environment(environment)}
	object, suffix, ok := strings.Cut(id, "_")
	if !ok || suffix == "" || (object != "txn" && object != "re" && object != "frr") {
		return errors.New("stripe-ledger requires a balance transaction, refund or Report Run -id")
	}
	if !strings.HasPrefix(account, "acct_") || account == "acct_" || (scope.Environment != stripe.Sandbox && scope.Environment != stripe.Live) {
		return errors.New("stripe-ledger requires an explicit Stripe -account and sandbox or live -environment")
	}
	if !apply {
		fmt.Printf("dry-run: read Stripe ledger evidence for %s in %s\n", id, environment)
		return nil
	}
	if strings.TrimSpace(reason) == "" {
		return errors.New("stripe-ledger requires an audit reason when applying")
	}
	ctx, cancel := context.WithTimeout(context.Background(), stripeLedgerRunTimeout)
	defer cancel()
	config.LoadConfig()
	db.InitDB()
	defer db.CloseDB()
	purpose := stripe.PurposeBalanceTransactions
	if object == "frr" {
		purpose = stripe.PurposeReportRun
	}
	dir := directory.New(db.DB, payment.NewAdapters())
	client, err := dir.ResolveStripeClient(ctx, payment.WebhookAccount{AccountKey: scope.AccountID, Environment: string(scope.Environment)}, purpose)
	if err != nil {
		return err
	}
	if object == "frr" {
		operator, err := user.Current()
		if err != nil {
			return fmt.Errorf("resolve settlement operator: %w", err)
		}
		result, err := settlement.ImportStripeReport(ctx, client, settlement.NewRepository(db.DB), id,
			settlement.Audit{Operator: operator.Username, Reason: reason})
		return writeSettlementResult(result, err)
	}
	repo := ledger.NewRepository(db.DB)
	log.Printf("stripe_ledger_started id=%s environment=%s operator=%q reason=%q", id, environment, os.Getenv("USER"), reason)
	var processed, conflicts int
	record := func(entries ...payment.LedgerEvidence) error {
		for _, entry := range entries {
			result, err := repo.Record(ctx, entry)
			if err != nil {
				return err
			}
			processed++
			if result.Conflict {
				conflicts++
			}
		}
		return nil
	}
	if err := readStripeLedger(ctx, client, object, id, record); err != nil {
		return fmt.Errorf("record Stripe ledger after %d observations: %w", processed, err)
	}
	log.Printf("stripe_ledger_completed id=%s observations=%d need_review=%d", id, processed, conflicts)
	return nil
}

func readStripeLedger(ctx context.Context, client *stripe.Client, object, id string, record func(...payment.LedgerEvidence) error) error {
	switch object {
	case "txn":
		entry, err := client.GetLedgerMovement(ctx, id)
		if err != nil {
			return err
		}
		return record(entry)
	case "re":
		entries, err := client.GetRefundLedgerEvidence(ctx, id)
		if err != nil {
			return err
		}
		if len(entries) == 0 {
			return errors.New("Stripe refund does not yet reference a balance transaction")
		}
		for _, entry := range entries {
			if err := record(entry); err != nil {
				return err
			}
			movement, err := client.GetLedgerMovement(ctx, entry.MovementKey)
			if err != nil {
				return err
			}
			if err := record(movement); err != nil {
				return err
			}
		}
		return nil
	}
	return errors.New("unsupported Stripe ledger object")
}
