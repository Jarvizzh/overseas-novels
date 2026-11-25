package settlement

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"reader-backend/internal/payment"
)

func recordPayout(ctx context.Context, tx pgx.Tx, accountID, importID int64, payout payment.PayoutEvidence) error {
	if payout.Reference == "" || payout.Expected.Minor <= 0 || payout.ObservedAt.IsZero() || !json.Valid(payout.Payload) {
		return errors.New("incomplete settlement payout")
	}
	if payout.Expected.Currency != CurrencyUSD {
		return errors.New("settlement payouts require USD")
	}
	var expected *time.Time
	if !payout.ExpectedAt.IsZero() {
		expected = &payout.ExpectedAt
	}
	var id int64
	err := tx.QueryRow(ctx, `INSERT INTO payment_settlement_payouts
		(account_id,external_ref,currency,expected_minor,bank_reference,expected_at,channel_status,allocation_required,
		 allocation_complete,transfer_key,reversal_key,last_observed_at)
		VALUES($1,$2,$3,$4,NULLIF($5,''),$6,$7,$8,$9,NULLIF($10,''),NULLIF($11,''),$12)
		ON CONFLICT (account_id,external_ref) DO UPDATE SET
		review_required=payment_settlement_payouts.review_required
			OR payment_settlement_payouts.currency<>EXCLUDED.currency
			OR payment_settlement_payouts.expected_minor<>EXCLUDED.expected_minor
			OR payment_settlement_payouts.allocation_required<>EXCLUDED.allocation_required
			OR (payment_settlement_payouts.transfer_key IS NOT NULL AND EXCLUDED.transfer_key IS NOT NULL
				AND payment_settlement_payouts.transfer_key<>EXCLUDED.transfer_key)
			OR (payment_settlement_payouts.bank_reference IS NOT NULL AND EXCLUDED.bank_reference IS NOT NULL
				AND payment_settlement_payouts.bank_reference<>EXCLUDED.bank_reference),
		bank_reference=COALESCE(payment_settlement_payouts.bank_reference,EXCLUDED.bank_reference),
		transfer_key=COALESCE(payment_settlement_payouts.transfer_key,EXCLUDED.transfer_key),
		reversal_key=COALESCE(payment_settlement_payouts.reversal_key,EXCLUDED.reversal_key),
		allocation_complete=payment_settlement_payouts.allocation_complete OR EXCLUDED.allocation_complete,
		channel_status=CASE WHEN EXCLUDED.last_observed_at>=payment_settlement_payouts.last_observed_at
			AND payment_settlement_payouts.channel_status NOT IN ('failed','canceled')
			THEN EXCLUDED.channel_status ELSE payment_settlement_payouts.channel_status END,
		expected_at=CASE WHEN EXCLUDED.last_observed_at>=payment_settlement_payouts.last_observed_at
			THEN COALESCE(EXCLUDED.expected_at,payment_settlement_payouts.expected_at) ELSE payment_settlement_payouts.expected_at END,
		last_observed_at=GREATEST(payment_settlement_payouts.last_observed_at,EXCLUDED.last_observed_at)
		RETURNING id`, accountID, payout.Reference, payout.Expected.Currency, payout.Expected.Minor, payout.BankReference, expected,
		payout.Status, payout.AllocationRequired, payout.AllocationComplete, payout.TransferKey, payout.ReversalKey, payout.ObservedAt).Scan(&id)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO payment_settlement_payout_evidence(payout_id,import_id,payload)
		VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, id, importID, payout.Payload)
	return err
}

type PayoutStatus struct {
	Reference          string     `json:"payout_id"`
	Currency           string     `json:"currency"`
	ExpectedMinor      int64      `json:"expected_minor"`
	ExpectedAt         *time.Time `json:"expected_at"`
	ChannelStatus      string     `json:"channel_status"`
	ChannelCheck       string     `json:"channel_check"`
	Status             string     `json:"status"`
	TransferMinor      *int64     `json:"transfer_minor"`
	AllocationRequired bool       `json:"allocation_required"`
	AllocationComplete bool       `json:"allocation_complete"`
	MovementCount      int64      `json:"movement_count"`
	MovementNetMinor   *string    `json:"movement_net_minor"`
	BankAmountMinor    *int64     `json:"bank_amount_minor"`
	BankCurrency       *string    `json:"bank_currency"`
	BankBookedAt       *time.Time `json:"bank_booked_at"`
	BankTransaction    *string    `json:"bank_transaction_id"`
	DifferenceMinor    *string    `json:"difference_minor"`
	BankReference      *string    `json:"bank_reference"`
}

func (r *Repository) PayoutStatus(ctx context.Context, provider string, account payment.WebhookAccount, reference string) (PayoutStatus, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return PayoutStatus{}, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	accountID, err := findAccount(ctx, tx, provider, account)
	if err != nil {
		return PayoutStatus{}, err
	}
	var result PayoutStatus
	err = tx.QueryRow(ctx, `SELECT external_ref,currency,expected_minor,expected_at,channel_status,channel_check,status,
		transfer_minor,allocation_required,allocation_complete,row_count,net_total_minor::text,
		bank_amount_minor,bank_currency,booked_at,bank_transaction_id,difference_minor::text,bank_reference
		FROM payment_settlement_reconciliation WHERE account_id=$1 AND external_ref=$2`, accountID, reference).
		Scan(&result.Reference, &result.Currency, &result.ExpectedMinor, &result.ExpectedAt, &result.ChannelStatus,
			&result.ChannelCheck, &result.Status, &result.TransferMinor, &result.AllocationRequired, &result.AllocationComplete,
			&result.MovementCount, &result.MovementNetMinor, &result.BankAmountMinor, &result.BankCurrency,
			&result.BankBookedAt, &result.BankTransaction, &result.DifferenceMinor, &result.BankReference)
	return result, err
}
