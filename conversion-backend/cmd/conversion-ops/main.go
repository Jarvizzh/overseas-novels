package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"conversion-backend/internal/credentials"
	"conversion-backend/internal/delivery"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: conversion-ops <import-legacy-pixels|import-legacy-events|backfill-legacy-subscriptions|repair-missing|retry-delivery|retry-partner-report|append-target|review-legacy-environment|retention> [flags]")
	}
	command := args[0]
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	limit := flags.Int("limit", 100, "number of legacy rows to process per transaction (1–500)")
	version := flags.String("key-version", "", "credential encryption key version for Pixel import")
	evidenceFile := flags.String("evidence", "", "JSON map of historical event IDs to their verified environment and evidence reference")
	after := flags.Int64("after-id", 0, "exclusive numeric cursor for Pixels and subscriptions")
	afterEvent := flags.String("after-event", "", "exclusive historical event ID cursor")
	orderID := flags.Int64("order-id", 0, "local payment order ID")
	eventName := flags.String("event-name", "", "Purchase or Subscribe")
	apply := flags.Bool("apply", false, "apply missing-row repair or confirm retention")
	keepDays := flags.Int("keep-days", 0, "retention period in days (minimum seven)")
	operator := flags.String("operator", "", "active Admin or SuperAdmin account ID")
	reason := flags.String("reason", "", "audit reason")
	evidenceRef := flags.String("evidence-reference", "", "reference for missing-row repair evidence")
	deliveryID := flags.Int64("delivery-id", 0, "delivery ID")
	reportID := flags.Int64("report-id", 0, "partner report ID")
	legacyEventID := flags.String("legacy-event-id", "", "exact legacy event ID for evidence review")
	eventID := flags.Int64("event-id", 0, "normalized event ID")
	destinationID := flags.Int64("destination-id", 0, "authorized destination ID")
	expected := flags.Int("expected-attempts", -1, "expected delivery attempt count")
	acknowledge := flags.Bool("acknowledge-duplicate-risk", false, "authorize a single retry after an unconfirmed send")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if *limit < 1 || *limit > 500 || *after < 0 || flags.NArg() != 0 {
		return errors.New("invalid batch limit, cursor or positional argument")
	}
	dsn, environment := os.Getenv("DATABASE_URL"), strings.TrimSpace(os.Getenv("CONVERSION_ENVIRONMENT"))
	if dsn == "" || environment == "" {
		return errors.New("DATABASE_URL and CONVERSION_ENVIRONMENT are required")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return errors.New("invalid DATABASE_URL")
	}
	cfg.ConnConfig.ConnectTimeout = 3 * time.Second
	cfg.MaxConns = 2
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return errors.New("open conversion database pool")
	}
	defer pool.Close()
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	service := delivery.Service{Store: delivery.Store{Pool: pool, Environment: environment}}
	if err := service.CheckSchema(ctx); err != nil {
		return err
	}

	var result any
	switch command {
	case "import-legacy-pixels":
		keys, err := credentials.Parse(os.Getenv("CONVERSION_CREDENTIAL_KEYS"))
		if err != nil {
			return err
		}
		if _, ok := keys[*version]; !ok {
			return errors.New("key-version must identify a configured encryption key")
		}
		result, err = service.ImportLegacyPixels(ctx, *after, *limit, *version, keys.Encrypt)
		if err != nil {
			return err
		}
	case "import-legacy-events":
		evidence, err := loadEvidence(*evidenceFile, environment)
		if err != nil {
			return err
		}
		result, err = service.ImportLegacyEvents(ctx, *afterEvent, *limit, evidence)
		if err != nil {
			return err
		}
	case "backfill-legacy-subscriptions":
		result, err = service.BackfillLegacySubscriptions(ctx, *after, *limit)
		if err != nil {
			return err
		}
	case "retention":
		if !*apply || *operator == "" || *reason == "" {
			return delivery.ErrRetentionApply
		}
		result, err = service.RedactExpired(ctx, delivery.Operator{ID: *operator}, *keepDays, *limit, *reason)
	case "review-legacy-environment":
		result, err = service.ReviewLegacyEnvironment(ctx, delivery.Operator{ID: *operator}, *legacyEventID, *evidenceRef, *reason)
	case "repair-missing":
		result, err = service.RepairMissing(ctx, delivery.Operator{ID: *operator}, delivery.RepairRequest{
			OrderID: *orderID, EventName: *eventName, Apply: *apply, Reason: *reason, Evidence: *evidenceRef})
	case "retry-delivery":
		result, err = service.RetryDelivery(ctx, delivery.Operator{ID: *operator}, *deliveryID, *expected, *reason, *acknowledge)
	case "retry-partner-report":
		result, err = service.RetryPartnerReport(ctx, delivery.Operator{ID: *operator}, *reportID, *reason)
	case "append-target":
		result, err = service.AppendTarget(ctx, delivery.Operator{ID: *operator}, *eventID, *destinationID, *reason)
	default:
		return errors.New("unknown conversion operation: " + command)
	}
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(result)
}

func loadEvidence(path, environment string) (map[string]delivery.LegacyEvidence, error) {
	if path == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	evidence := make(map[string]delivery.LegacyEvidence)
	if err := json.Unmarshal(raw, &evidence); err != nil {
		return nil, errors.New("invalid legacy event evidence JSON")
	}
	for id, entry := range evidence {
		if id == "" || entry.Environment != environment || strings.TrimSpace(entry.Reference) == "" ||
			len(entry.Reference) > 500 || strings.ContainsRune(entry.Reference, 0) {
			return nil, errors.New("every evidence entry needs an event ID, matching environment and reference of up to 500 bytes")
		}
	}
	return evidence, nil
}
