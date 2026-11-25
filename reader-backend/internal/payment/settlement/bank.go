package settlement

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"reader-backend/internal/payment"
)

type BankStatement struct {
	Environment, Account, Reference string
	Audit                           Audit
}

type BankEntry struct {
	TransactionID, Reference string
	BookedAt                 time.Time
	Amount                   payment.LedgerMoney
}

// ReadBankStatement consumes the normalized export prepared from an independent
// bank statement. The signed amounts are already in the stated currency's minor units.
func ReadBankStatement(ctx context.Context, input io.Reader, visit func(BankEntry) error) error {
	reader := csv.NewReader(input)
	header, err := reader.Read()
	if err != nil {
		return fmt.Errorf("read bank statement header: %w", err)
	}
	header[0] = strings.TrimPrefix(header[0], "\ufeff")
	if !slices.Equal(header, []string{"transaction_id", "booked_at", "currency", "amount_minor", "bank_reference"}) {
		return errors.New("unexpected bank statement columns")
	}
	for row := 2; ; row++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		fields, err := reader.Read()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read bank row %d: %w", row, err)
		}
		booked, dateErr := time.Parse(time.RFC3339, fields[1])
		amount, amountErr := strconv.ParseInt(fields[3], 10, 64)
		code := strings.ToUpper(fields[2])
		if code != CurrencyUSD {
			return fmt.Errorf("bank statement row %d requires USD", row)
		}
		if strings.TrimSpace(fields[0]) == "" || dateErr != nil || amountErr != nil || amount == 0 {
			return fmt.Errorf("invalid bank statement row %d", row)
		}
		entry := BankEntry{TransactionID: fields[0], BookedAt: booked, Reference: strings.TrimSpace(fields[4]),
			Amount: payment.LedgerMoney{Currency: code, Minor: amount}}
		if err := visit(entry); err != nil {
			return fmt.Errorf("record bank row %d: %w", row, err)
		}
	}
}

func (r *Repository) ImportBank(ctx context.Context, statement BankStatement, input io.Reader) (int, error) {
	if err := statement.Audit.validate(); err != nil {
		return 0, err
	}
	if strings.TrimSpace(statement.Account) == "" || strings.TrimSpace(statement.Reference) == "" {
		return 0, errors.New("bank account and statement reference are required")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	scope := "payment-bank:" + statement.Environment + ":" + statement.Account
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, scope); err != nil {
		return 0, err
	}
	var entries int
	err = ReadBankStatement(ctx, input, func(entry BankEntry) error {
		// A later statement may contain the same transaction. Preserve the
		// first evidence and reject changes to its business fields.
		var id int64
		err := tx.QueryRow(ctx, `INSERT INTO payment_bank_statement_entries
			(environment,bank_account,transaction_id,statement_ref,booked_at,currency,amount_minor,bank_reference,operator,reason)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
			ON CONFLICT (environment,bank_account,transaction_id) DO UPDATE SET transaction_id=EXCLUDED.transaction_id
			WHERE (payment_bank_statement_entries.booked_at,payment_bank_statement_entries.currency,
			       payment_bank_statement_entries.amount_minor,payment_bank_statement_entries.bank_reference)
			    = (EXCLUDED.booked_at,EXCLUDED.currency,EXCLUDED.amount_minor,EXCLUDED.bank_reference)
			RETURNING id`, statement.Environment, statement.Account, entry.TransactionID, statement.Reference,
			entry.BookedAt, entry.Amount.Currency, entry.Amount.Minor, entry.Reference, statement.Audit.Operator, statement.Audit.Reason).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return errors.New("bank transaction identity already has different evidence")
		}
		if err == nil {
			entries++
		}
		return err
	})
	if err != nil {
		return 0, err
	}
	return entries, tx.Commit(ctx)
}

const (
	MatchConfirmed = "matched"
	MatchAmount    = "amount_mismatch"
	MatchReference = "reference_mismatch"
	MatchNotReady  = "not_ready"
)

type MatchRequest struct {
	Provider, Payout, BankAccount, BankTransaction string
	Account                                        payment.WebhookAccount
	Audit                                          Audit
	ManualEvidence                                 string
}

type MatchResult struct {
	Outcome    string `json:"outcome"`
	Channel    string `json:"channel_check"`
	BankMinor  int64  `json:"bank_minor"`
	Expected   int64  `json:"expected_minor"`
	BankCode   string `json:"bank_currency"`
	PayoutCode string `json:"payout_currency"`
}

// MatchBank records the comparison and binds a bank entry only when the
// payout ledger and the independent bank evidence agree.
func (r *Repository) MatchBank(ctx context.Context, req MatchRequest) (MatchResult, error) {
	if err := req.Audit.validate(); err != nil {
		return MatchResult{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return MatchResult{}, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	accountID, err := findAccount(ctx, tx, req.Provider, req.Account)
	if err != nil {
		return MatchResult{}, err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('payment-settlement:'||$1::bigint::text,0))`, accountID); err != nil {
		return MatchResult{}, err
	}
	var payoutID, entryID int64
	var payoutReference, bankReference string
	var result MatchResult
	err = tx.QueryRow(ctx, `SELECT id,channel_check,currency,expected_minor,COALESCE(bank_reference,'')
		FROM payment_settlement_payout_checks WHERE account_id=$1 AND external_ref=$2`, accountID, req.Payout).
		Scan(&payoutID, &result.Channel, &result.PayoutCode, &result.Expected, &payoutReference)
	if err != nil {
		return MatchResult{}, fmt.Errorf("find settlement payout: %w", err)
	}
	err = tx.QueryRow(ctx, `SELECT id,currency,amount_minor,bank_reference FROM payment_bank_statement_entries
		WHERE environment=$1 AND bank_account=$2 AND transaction_id=$3 FOR UPDATE`,
		req.Account.Environment, req.BankAccount, req.BankTransaction).Scan(&entryID, &result.BankCode, &result.BankMinor, &bankReference)
	if err != nil {
		return MatchResult{}, fmt.Errorf("find bank statement entry: %w", err)
	}
	switch {
	case result.Channel != "ready":
		result.Outcome = MatchNotReady
	case result.BankMinor != result.Expected:
		result.Outcome = MatchAmount
	case payoutReference != "" && payoutReference != bankReference:
		result.Outcome = MatchReference
	case payoutReference == "" && strings.TrimSpace(req.ManualEvidence) == "":
		result.Outcome = MatchReference
	default:
		result.Outcome = MatchConfirmed
	}
	_, err = tx.Exec(ctx, `INSERT INTO payment_settlement_bank_reviews(payout_id,entry_id,outcome,operator,reason,manual_evidence)
		VALUES($1,$2,$3,$4,$5,NULLIF($6,'')) ON CONFLICT (payout_id,entry_id,outcome) DO NOTHING`,
		payoutID, entryID, result.Outcome, req.Audit.Operator, req.Audit.Reason, strings.TrimSpace(req.ManualEvidence))
	if err != nil {
		return MatchResult{}, err
	}
	return result, tx.Commit(ctx)
}
