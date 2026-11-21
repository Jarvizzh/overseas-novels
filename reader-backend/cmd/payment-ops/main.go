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
	"reader-backend/internal/wallet"
)

func main() {
	kind := flag.String("kind", "", "retry kind: inbox, order, cancel, subscription; empty prints status")
	id := flag.String("id", "", "exact task ID (local order ID for order)")
	from := flag.String("from", "", "subscription cursor in RFC3339, never forward")
	apply := flag.Bool("apply", false, "apply the retry scheduling change; default dry-run")
	reason := flag.String("reason", "", "required audit reason when applying")
	flag.Parse()
	if *kind == "outbox" {
		log.Fatal(wallet.ErrUseConversionOps)
	}
	config.LoadConfig()
	db.InitDB()
	defer db.CloseDB()
	ctx, cancel := context.WithTimeout(db.BackgroundContext(context.Background()), 5*time.Second)
	defer cancel()
	if *kind == "" {
		status, err := wallet.PaymentTaskStatus(ctx)
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
	if err := wallet.RetryPaymentTask(ctx, *kind, *id, cursor); err != nil {
		log.Fatal(err)
	}
	log.Printf("payment_task_retry kind=%q id=%q from=%q operator=%q reason=%q", *kind, *id, *from, os.Getenv("USER"), *reason)
}
