// Package ledger persists channel balance movements and the evidence behind them.
package ledger

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
	"reader-backend/internal/payment"
)

type Repository struct {
	pool interface {
		Begin(context.Context) (pgx.Tx, error)
	}
}

func NewRepository(pool interface {
	Begin(context.Context) (pgx.Tx, error)
}) *Repository {
	return &Repository{pool: pool}
}

type Result struct {
	MovementID int64
	Posted     bool
	Conflict   bool
}

// Record stores one observation atomically. Duplicate deliveries leave balances
// unchanged. Conflicting financial evidence is retained without replacing
// the original figures or adding parts from the conflicting snapshot.
func (r *Repository) Record(ctx context.Context, evidence payment.LedgerEvidence) (Result, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Result{}, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	result, err := RecordTx(ctx, tx, evidence)
	if err != nil {
		return Result{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Result{}, err
	}
	return result, nil
}

// RecordTx lets a settlement import commit its rows and ledger evidence together.
func RecordTx(ctx context.Context, tx pgx.Tx, evidence payment.LedgerEvidence) (Result, error) {
	authority, err := validateEvidence(evidence)
	if err != nil {
		return Result{}, err
	}
	var accountID int64
	err = tx.QueryRow(ctx, `SELECT id FROM payment_channel_accounts WHERE provider=$1 AND account_key=$2 AND environment=$3`,
		evidence.Provider, evidence.Account.AccountKey, evidence.Account.Environment).Scan(&accountID)
	if err != nil {
		return Result{}, fmt.Errorf("resolve ledger account: %w", err)
	}
	movement, err := lockMovement(ctx, tx, accountID, evidence.MovementKey)
	if err != nil {
		return Result{}, err
	}
	result := Result{MovementID: movement.id, Posted: movement.posted, Conflict: movement.review}
	conflict := false
	if authority && movement.posted {
		conflict, err = conflictsWithMovement(ctx, tx, movement, evidence)
		if err != nil {
			return Result{}, err
		}
	}
	sourceID, err := appendEvidence(ctx, tx, movement.id, evidence, conflict)
	if err != nil {
		return Result{}, err
	}
	if conflict {
		result.Conflict = true
		_, err = tx.Exec(ctx, `UPDATE payment_ledger_movements SET review_required=true WHERE id=$1`, movement.id)
	} else {
		if authority && sourceID != 0 {
			if err := postMovement(ctx, tx, movement.id, sourceID, evidence); err != nil {
				return Result{}, err
			}
			result.Posted = true
		}
		err = recordReferences(ctx, tx, movement.id, evidence.References)
	}
	if err != nil {
		return Result{}, err
	}
	return result, nil
}

func validateEvidence(e payment.LedgerEvidence) (bool, error) {
	if e.MovementKey == "" || e.SourceRef == "" || !json.Valid(e.Payload) {
		return false, errors.New("ledger evidence requires a movement key, source reference and original payload")
	}
	switch e.Source {
	case payment.LedgerStripeWebhook, payment.LedgerStripeRefund:
		if e.Provider != payment.ProviderStripe || e.Kind != "" || e.ExchangeRate != "" || len(e.Amounts) != 0 {
			return false, errors.New("Stripe business observations may only reference a ledger movement")
		}
		return false, nil
	case payment.LedgerPayPalSearch, payment.LedgerPayPalSettlement:
		if e.Provider != payment.ProviderPayPal {
			return false, errors.New("unexpected PayPal evidence provider")
		}
	case payment.LedgerStripeBalance, payment.LedgerStripeReport:
		if e.Provider != payment.ProviderStripe {
			return false, errors.New("unexpected Stripe evidence provider")
		}
	default:
		return false, errors.New("unsupported ledger evidence source")
	}
	gross, hasGross := e.Amounts[payment.LedgerGross]
	if e.Kind == "" || e.Currency == "" || !hasGross || gross.Currency != e.Currency {
		return false, errors.New("authoritative ledger evidence requires a gross amount in the balance currency")
	}
	if e.ExchangeRate != "" {
		rate, err := decimal.NewFromString(e.ExchangeRate)
		if err != nil || !rate.IsPositive() {
			return false, errors.New("invalid ledger exchange rate")
		}
	}
	return true, nil
}

type movementRow struct {
	id                     int64
	posted, review         bool
	kind                   payment.MovementKind
	currency, providerCode string
	exchangeRate           string
	occurredAt             *time.Time
}

func lockMovement(ctx context.Context, tx pgx.Tx, accountID int64, key string) (movementRow, error) {
	if _, err := tx.Exec(ctx, `INSERT INTO payment_ledger_movements(account_id,movement_key)
		VALUES($1,$2) ON CONFLICT(account_id,movement_key) DO NOTHING`, accountID, key); err != nil {
		return movementRow{}, err
	}
	var row movementRow
	err := tx.QueryRow(ctx, `SELECT id,posted_at IS NOT NULL,review_required,COALESCE(kind,''),COALESCE(currency,''),
		COALESCE(provider_code,''),occurred_at,COALESCE(exchange_rate::text,'') FROM payment_ledger_movements
		WHERE account_id=$1 AND movement_key=$2 FOR UPDATE`, accountID, key).
		Scan(&row.id, &row.posted, &row.review, &row.kind, &row.currency, &row.providerCode, &row.occurredAt, &row.exchangeRate)
	return row, err
}

func conflictsWithMovement(ctx context.Context, tx pgx.Tx, row movementRow, e payment.LedgerEvidence) (bool, error) {
	if row.currency != e.Currency ||
		(row.kind != payment.MovementOther && e.Kind != payment.MovementOther && row.kind != e.Kind) ||
		(row.providerCode != "" && e.ProviderCode != "" && row.providerCode != e.ProviderCode) ||
		(row.occurredAt != nil && !e.OccurredAt.IsZero() && !row.occurredAt.Equal(e.OccurredAt.Truncate(time.Microsecond))) {
		return true, nil
	}
	if row.exchangeRate != "" && e.ExchangeRate != "" {
		stored, err := decimal.NewFromString(row.exchangeRate)
		if err != nil {
			return false, err
		}
		incoming, err := decimal.NewFromString(e.ExchangeRate)
		if err != nil {
			return false, err
		}
		if !stored.Equal(incoming) {
			return true, nil
		}
	}
	rows, err := tx.Query(ctx, `SELECT component,currency,amount_minor FROM payment_ledger_amounts WHERE movement_id=$1`, row.id)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	var gross, fee, net *payment.LedgerMoney
	for component, amount := range e.Amounts {
		switch component {
		case payment.LedgerGross:
			gross = &amount
		case payment.LedgerFee:
			fee = &amount
		case payment.LedgerNet:
			net = &amount
		}
	}
	for rows.Next() {
		var kind payment.LedgerComponent
		var stored payment.LedgerMoney
		if err := rows.Scan(&kind, &stored.Currency, &stored.Minor); err != nil {
			return false, err
		}
		if incoming, present := e.Amounts[kind]; present && incoming != stored {
			return true, nil
		}
		switch kind {
		case payment.LedgerFee:
			fee = &stored
		case payment.LedgerNet:
			net = &stored
		}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	if gross != nil && fee != nil && net != nil && gross.Currency == fee.Currency && gross.Currency == net.Currency {
		return !decimal.NewFromInt(gross.Minor).Add(decimal.NewFromInt(fee.Minor)).Equal(decimal.NewFromInt(net.Minor)), nil
	}
	return false, nil
}

func appendEvidence(ctx context.Context, tx pgx.Tx, id int64, e payment.LedgerEvidence, conflict bool) (int64, error) {
	hash := sha256.Sum256(e.Payload)
	var sourceID int64
	err := tx.QueryRow(ctx, `INSERT INTO payment_ledger_evidence(movement_id,source,source_ref,payload_hash,payload,conflicts)
		VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(movement_id,source,source_ref,payload_hash) DO NOTHING RETURNING id`,
		id, e.Source, e.SourceRef, hash[:], e.Payload, conflict).Scan(&sourceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return sourceID, err
}

func postMovement(ctx context.Context, tx pgx.Tx, id, sourceID int64, e payment.LedgerEvidence) error {
	var occurred *time.Time
	if !e.OccurredAt.IsZero() {
		at := e.OccurredAt.UTC().Truncate(time.Microsecond)
		occurred = &at
	}
	if _, err := tx.Exec(ctx, `UPDATE payment_ledger_movements SET posted_at=COALESCE(posted_at,now()),
		kind=CASE WHEN kind IS NULL OR kind='other' THEN $2 ELSE kind END,
		currency=$3, provider_code=COALESCE(provider_code,NULLIF($4,'')),occurred_at=COALESCE(occurred_at,$5),
		exchange_rate=COALESCE(exchange_rate,NULLIF($6::text,'')::numeric)
		WHERE id=$1`, id, e.Kind, e.Currency, e.ProviderCode, occurred, e.ExchangeRate); err != nil {
		return err
	}
	for kind, amount := range e.Amounts {
		if _, err := tx.Exec(ctx, `INSERT INTO payment_ledger_amounts(movement_id,component,currency,amount_minor,evidence_id)
			VALUES($1,$2,$3,$4,$5) ON CONFLICT(movement_id,component) DO NOTHING`,
			id, kind, amount.Currency, amount.Minor, sourceID); err != nil {
			return err
		}
	}
	return nil
}

func recordReferences(ctx context.Context, tx pgx.Tx, id int64, refs []payment.LedgerReference) error {
	for _, ref := range refs {
		if _, err := tx.Exec(ctx, `INSERT INTO payment_ledger_references(movement_id,object_type,object_id)
			VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, id, ref.Type, ref.ID); err != nil {
			return err
		}
	}
	return nil
}
