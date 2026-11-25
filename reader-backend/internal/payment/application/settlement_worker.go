package application

import (
	"context"
	"log"

	"reader-backend/internal/conversion"
	"reader-backend/internal/db"
	"reader-backend/internal/payment/settlement"
)

func currentPaymentLedgerEnvironment() string {
	switch conversion.Environment() {
	case "sandbox":
		return "sandbox"
	case "production":
		return "live"
	default:
		return ""
	}
}

func (s *service) reconcileSettlementCommands(ctx context.Context) {
	environment := currentPaymentLedgerEnvironment()
	if environment == "" {
		return
	}

	if _, err := settlement.NewRepository(db.Pool(ctx)).ExecuteNextCommand(ctx, environment); err != nil {
		log.Printf("task=settlement_commands action=execute error=%v", err)
	}
}

func (s *service) reconcileAutomaticLedger(ctx context.Context) {
	environment := currentPaymentLedgerEnvironment()
	if environment == "" {
		return
	}
	if _, err := settlement.NewRepository(db.Pool(ctx)).SyncNextLedgerPage(ctx, environment); err != nil {
		log.Printf("task=automatic_payment_ledger action=execute error=%v", err)
	}
}
