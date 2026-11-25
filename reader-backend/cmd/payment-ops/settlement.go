package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/user"
	"strings"
	"time"

	"reader-backend/internal/config"
	"reader-backend/internal/db"
	"reader-backend/internal/payment"
	"reader-backend/internal/payment/settlement"
)

const (
	cmdPayPalSettlement  = "paypal-settlement"
	cmdStripePayout      = "stripe-payout"
	cmdBankStatement     = "bank-statement"
	cmdSettlementMatch   = "settlement-match"
	cmdSettlementStatus  = "settlement-status"
	settlementRunTimeout = 20 * time.Minute
)

type settlementCommand struct {
	kind, id, provider, account, environment, from, through string
	bankAccount, bankTransaction, bankEvidence, reason      string
	files                                                   []string
	apply                                                   bool
}

func isSettlementCommand(kind string) bool {
	switch kind {
	case cmdPayPalSettlement, cmdStripePayout, cmdBankStatement, cmdSettlementMatch, cmdSettlementStatus:
		return true
	default:
		return false
	}
}

func (c settlementCommand) run() error {
	if err := c.validate(); err != nil {
		return err
	}
	if !c.apply && c.kind != cmdSettlementStatus {
		fmt.Printf("dry-run: %s in %s\n", c.kind, c.environment)
		return nil
	}
	var audit settlement.Audit
	if c.kind != cmdSettlementStatus {
		operator, err := user.Current()
		if err != nil {
			return fmt.Errorf("resolve settlement operator: %w", err)
		}
		audit = settlement.Audit{Operator: operator.Username, Reason: c.reason}
	}
	config.LoadConfig()
	db.InitDB()
	defer db.CloseDB()
	ctx, cancel := context.WithTimeout(context.Background(), settlementRunTimeout)
	defer cancel()
	repo := settlement.NewRepository(db.DB)
	switch c.kind {
	case cmdPayPalSettlement:
		result, err := importPayPalSettlement(ctx, repo, c.files, c.account, c.environment, audit)
		return writeSettlementResult(result, err)
	case cmdStripePayout:
		return c.syncStripePayouts(ctx, repo, audit)
	case cmdBankStatement:
		file, err := os.Open(c.files[0])
		if err != nil {
			return err
		}
		defer file.Close()
		rows, err := repo.ImportBank(ctx, settlement.BankStatement{Environment: c.environment, Account: c.bankAccount,
			Reference: c.id, Audit: audit}, file)
		return writeSettlementResult(map[string]int{"rows": rows}, err)
	case cmdSettlementMatch:
		result, err := repo.MatchBank(ctx, settlement.MatchRequest{Provider: c.provider, Account: c.channelScope(), Payout: c.id,
			BankAccount: c.bankAccount, BankTransaction: c.bankTransaction, ManualEvidence: c.bankEvidence, Audit: audit})
		return writeSettlementResult(result, err)
	default:
		result, err := repo.PayoutStatus(ctx, c.provider, c.channelScope(), c.id)
		return writeSettlementResult(result, err)
	}
}

func (c settlementCommand) validate() error {
	if c.environment != "sandbox" && c.environment != "live" {
		return errors.New("settlement requires an explicit sandbox or live environment")
	}
	if c.apply && c.kind != cmdSettlementStatus && strings.TrimSpace(c.reason) == "" {
		return errors.New("settlement writes require an audit reason")
	}
	switch c.kind {
	case cmdPayPalSettlement:
		if len(c.files) == 0 {
			return errors.New("paypal-settlement requires ordered -file arguments")
		}
	case cmdStripePayout:
		if !strings.HasPrefix(c.account, "acct_") {
			return errors.New("stripe-payout requires a Stripe -account")
		}
		if c.id == "" {
			_, _, err := parseStripeWindow(c.from, c.through)
			return err
		}
		if !strings.HasPrefix(c.id, "po_") || c.from != "" || c.through != "" {
			return errors.New("stripe-payout accepts one payout ID or one query window")
		}
	case cmdBankStatement:
		if len(c.files) != 1 || c.bankAccount == "" || c.id == "" {
			return errors.New("bank-statement requires -file, -bank-account and statement -id")
		}
	case cmdSettlementMatch, cmdSettlementStatus:
		if (c.provider != payment.ProviderStripe && c.provider != payment.ProviderPayPal) || c.account == "" || c.id == "" {
			return errors.New("settlement lookup requires -provider, -account and payout -id")
		}
		if c.kind == cmdSettlementMatch && (c.bankAccount == "" || c.bankTransaction == "") {
			return errors.New("settlement-match requires -bank-account and -bank-transaction")
		}
	}
	return nil
}

func (c settlementCommand) channelScope() payment.WebhookAccount {
	return payment.WebhookAccount{AccountKey: c.account, Environment: c.environment}
}

func writeSettlementResult(value any, err error) error {
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(value)
}
