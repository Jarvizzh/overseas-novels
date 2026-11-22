// payment-ops inspects durable queues and schedules one audited recovery.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"reader-backend/internal/config"
	"reader-backend/internal/db"
	"reader-backend/internal/payment"
	paymentapp "reader-backend/internal/payment/application"
	"reader-backend/internal/payment/paypal"
	"reader-backend/internal/payment/paypal/credentialstore"
)

func main() {
	kind := flag.String("kind", "", "task kind: inbox, event-recovery, event-retry, order, cancel, subscription, vip-plan, paypal-ledger, stripe-ledger, paypal-settlement, stripe-payout, bank-statement, settlement-match, settlement-status, aftercare")
	id := flag.String("id", "", "exact task ID (local order ID for order)")
	from := flag.String("from", "", "subscription cursor or channel query window start in RFC3339")
	through := flag.String("through", "", "channel query end in RFC3339")
	environment := flag.String("environment", "", "expected ledger environment: sandbox or live")
	account := flag.String("account", "", "channel account ID")
	provider := flag.String("provider", "", "channel for settlement matching or status")
	bankAccount := flag.String("bank-account", "", "bank account alias")
	bankTransaction := flag.String("bank-transaction", "", "bank statement transaction ID")
	bankEvidence := flag.String("bank-evidence", "", "independent bank evidence reference when the channel has no trace ID")
	var files []string
	flag.Func("file", "local report file, repeat in PayPal part order", func(file string) error {
		files = append(files, file)
		return nil
	})
	plan := flag.String("plan", "", "existing PayPal Plan ID for vip-plan recovery")
	apply := flag.Bool("apply", false, "apply the retry scheduling change; default dry-run")
	reason := flag.String("reason", "", "required audit reason when applying")
	flag.Parse()
	if *kind == "outbox" {
		log.Fatal(paymentapp.ErrUseConversionOps)
	}
	if *kind == "event-recovery" {
		if err := runEventRecovery(*provider, *account, *environment, *from, *reason, *apply); err != nil {
			log.Fatal(err)
		}
		return
	}
	if *kind == "event-retry" {
		if *provider == "" || *account == "" || *environment == "" || *id == "" {
			log.Fatal("event-retry requires -provider, -account, -environment and -id")
		}
		if !*apply {
			fmt.Printf("dry-run: retry event provider=%q account=%q environment=%q id=%q\n", *provider, *account, *environment, *id)
			return
		}
		if *reason == "" {
			log.Fatal("-reason is required for audit")
		}
		config.LoadConfig()
		db.InitDB()
		defer db.CloseDB()
		ctx, cancel := context.WithTimeout(db.BackgroundContext(context.Background()), 5*time.Second)
		defer cancel()
		if err := paymentapp.ScheduleRecoveredPaymentEvent(ctx, *provider, payment.WebhookAccount{AccountKey: *account, Environment: *environment}, *id); err != nil {
			log.Fatal(err)
		}
		log.Printf("payment_event_retry_scheduled provider=%q account=%q environment=%q id=%q operator=%q reason=%q",
			*provider, *account, *environment, *id, os.Getenv("USER"), *reason)
		return
	}
	if *kind == "paypal-ledger" {
		if err := runPayPalLedger(*from, *through, *account, *environment, *reason, *apply); err != nil {
			log.Fatal(err)
		}
		return
	}
	if *kind == "stripe-ledger" {
		if err := runStripeLedger(*account, *environment, *id, *reason, *apply); err != nil {
			log.Fatal(err)
		}
		return
	}
	if *kind == "aftercare" {
		if err := runAftercare(*provider, *account, *environment, *id, *from, *through, *reason, *apply); err != nil {
			log.Fatal(err)
		}
		return
	}

	if isSettlementCommand(*kind) {
		input := settlementCommand{kind: *kind, id: *id, provider: *provider, account: *account,
			environment: *environment, from: *from, through: *through, files: files,
			bankAccount: *bankAccount, bankTransaction: *bankTransaction, bankEvidence: *bankEvidence,
			reason: *reason, apply: *apply}
		if err := input.run(); err != nil {
			log.Fatal(err)
		}
		return
	}
	config.LoadConfig()
	db.InitDB()
	defer db.CloseDB()
	ctx, cancel := context.WithTimeout(db.BackgroundContext(context.Background()), 5*time.Second)
	defer cancel()
	if *kind == "" {
		status, err := paymentapp.PaymentTaskStatus(ctx)
		if err != nil {
			log.Fatal(err)
		}
		if err := json.NewEncoder(os.Stdout).Encode(status); err != nil {
			log.Fatal(err)
		}
		return
	}
	if *id == "" {
		log.Fatal("-id is required")
	}
	if *kind == "vip-plan" {
		if *plan == "" || *from != "" {
			log.Fatal("vip-plan requires -plan and does not accept -from")
		}
		if !*apply {
			fmt.Printf("dry-run: verify and bind existing PayPal Plan %q to task %q\n", *plan, *id)
			return
		}
		if *reason == "" {
			log.Fatal("-reason is required for audit")
		}
		verifyCtx, stop := context.WithTimeout(db.BackgroundContext(context.Background()), 40*time.Second)
		defer stop()
		if err := paymentapp.RecoverPayPalVIPPlan(verifyCtx, paypal.NewClient(credentialstore.Load), *id, *plan); err != nil {
			log.Fatal(err)
		}
		log.Printf("vip_plan_recovered id=%q plan=%q operator=%q reason=%q", *id, *plan, os.Getenv("USER"), *reason)
		return
	}
	var cursor *time.Time
	if *from != "" {
		parsed, err := time.Parse(time.RFC3339, *from)
		if err != nil {
			log.Fatal(err)
		}
		cursor = &parsed
	}
	if !*apply {
		fmt.Printf("dry-run: retry kind=%q id=%q from=%q\n", *kind, *id, *from)
		return
	}
	if *reason == "" {
		log.Fatal("-reason is required for audit")
	}
	if err := paymentapp.RetryPaymentTask(ctx, *kind, *id, cursor); err != nil {
		log.Fatal(err)
	}
	log.Printf("payment_task_retry kind=%q id=%q from=%q operator=%q reason=%q", *kind, *id, *from, os.Getenv("USER"), *reason)
}

func runEventRecovery(provider, account, environment, from, reason string, apply bool) error {
	start, err := time.Parse(time.RFC3339, from)
	if err != nil {
		return fmt.Errorf("parse -from: %w", err)
	}
	if provider == "" || account == "" || environment == "" {
		return fmt.Errorf("event recovery requires -provider, -account and -environment")
	}
	if !apply {
		fmt.Printf("dry-run: recover provider=%q account=%q environment=%q from=%s\n", provider, account, environment, start.Format(time.RFC3339))
		return nil
	}
	if reason == "" {
		return fmt.Errorf("-reason is required for audit")
	}
	config.LoadConfig()
	db.InitDB()
	defer db.CloseDB()
	ctx, cancel := context.WithTimeout(db.BackgroundContext(context.Background()), 5*time.Second)
	defer cancel()
	err = paymentapp.SchedulePaymentEventRecovery(ctx, provider, payment.WebhookAccount{AccountKey: account, Environment: environment}, start)
	if err == nil {
		log.Printf("payment_event_recovery_scheduled provider=%q account=%q environment=%q from=%q operator=%q reason=%q",
			provider, account, environment, from, os.Getenv("USER"), reason)
	}
	return err
}
