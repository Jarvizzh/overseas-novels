package application

import (
	"context"
	"log"
	"time"

	"reader-backend/internal/config"
	"reader-backend/internal/conversion"
	"reader-backend/internal/db"
	"reader-backend/internal/payment/settlement"
	"reader-backend/internal/payment/stripe"
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

func (s *service) settlementRepository(ctx context.Context) *settlement.Repository {
	repo := settlement.NewRepository(db.Pool(ctx))
	if resolver, ok := s.adapters.DynamicSource().(settlement.StripeClientResolver); ok {
		repo = repo.WithStripeResolver(resolver)
	}
	return repo
}

func (s *service) reconcileSettlementCommands(ctx context.Context) {
	environment := currentPaymentLedgerEnvironment()
	if environment == "" {
		return
	}

	if _, err := s.settlementRepository(ctx).ExecuteNextCommand(ctx, environment); err != nil {
		log.Printf("task=settlement_commands action=execute error=%s", stripe.RedactSecrets(err.Error()))
	}
}

func (s *service) reconcileAutomaticLedger(ctx context.Context) {
	environment := currentPaymentLedgerEnvironment()
	if environment == "" {
		return
	}
	if _, err := s.settlementRepository(ctx).SyncNextLedgerPage(ctx, environment); err != nil {
		log.Printf("task=automatic_payment_ledger action=execute error=%s", stripe.RedactSecrets(err.Error()))
	}
}

func (s *service) reconcileStripeReportRuns(ctx context.Context) {
	environment := currentPaymentLedgerEnvironment()
	if environment == "" {
		return
	}
	repo := s.settlementRepository(ctx)
	// MoR tax/fee columns are opt-in so Direct-only accounts are not asked for
	// columns Stripe does not return for them.
	cfg := settlement.ReportScheduleConfig{}
	if appCfg := config.Get(); appCfg != nil {
		cfg.BalanceChangeReportType = appCfg.StripeBalanceChangeReportType
		cfg.PayoutReconciliationReportType = appCfg.StripePayoutReconciliationReportType
		cfg.IncludeMoRColumns = appCfg.StripeReportMoRColumnsEnabled
	}
	if _, err := repo.ScheduleDueStripeReportRuns(ctx, environment, cfg, time.Now().UTC()); err != nil {
		log.Printf("task=stripe_report_schedule action=schedule error=%s", stripe.RedactSecrets(err.Error()))
	}
	if _, err := repo.ProcessNextStripeReportRun(ctx, environment); err != nil {
		log.Printf("task=stripe_report_schedule action=process error=%s", stripe.RedactSecrets(err.Error()))
	}
}
