// Package settlement imports channel and bank evidence and reconciles verified payouts.
package settlement

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"reader-backend/internal/payment"
	"reader-backend/internal/payment/directory"
	"reader-backend/internal/payment/ledger"
	"reader-backend/internal/payment/stripe"
)

const (
	PayPalReport = "paypal_settlement"
	StripeReport = "stripe_report"
	StripePayout = "stripe_payout"
	CurrencyUSD  = "USD"
)

type Audit struct {
	Operator string
	Reason   string
}

func (a Audit) validate() error {
	if strings.TrimSpace(a.Operator) == "" || strings.TrimSpace(a.Reason) == "" {
		return errors.New("a settlement operator and reason are required")
	}
	return nil
}

type Report struct {
	Provider, Source, Reference string
	Account                     payment.WebhookAccount
	Fingerprint                 [32]byte
	Audit                       Audit
	Payout                      *payment.PayoutEvidence
}

type ReportRow struct {
	File, ReviewReason       string
	Number                   int
	Payload                  json.RawMessage
	Evidence                 *payment.LedgerEvidence
	Payout                   *payment.PayoutEvidence
	WithheldTaxMinor         *int64
	FeeNetOfWithheldTaxMinor *int64
}

type ImportResult struct {
	ImportID int64 `json:"import_id"`
	Rows     int   `json:"rows"`
	Review   int   `json:"review_rows"`
	Replay   bool  `json:"replay"`
}

type settlementDatabase interface {
	Begin(context.Context) (pgx.Tx, error)
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

// StripeClientResolver resolves a verified Stripe client for a merchant account,
// environment, and explicit purpose.
type StripeClientResolver interface {
	ResolveStripeClient(ctx context.Context, scope payment.WebhookAccount, purpose stripe.Purpose) (*stripe.Client, error)
}

type Repository struct {
	pool           settlementDatabase
	stripeResolver StripeClientResolver
}

func NewRepository(pool settlementDatabase) *Repository {
	return &Repository{pool: pool}
}

// WithStripeResolver returns a shallow copy of Repository using the provided
// Stripe credential resolver (such as the shared dynamic payment directory).
func (r *Repository) WithStripeResolver(resolver StripeClientResolver) *Repository {
	copied := *r
	copied.stripeResolver = resolver
	return &copied
}

func (r *Repository) resolveStripeClient(ctx context.Context, scope payment.WebhookAccount, purpose stripe.Purpose) (*stripe.Client, error) {
	if r.stripeResolver != nil {
		return r.stripeResolver.ResolveStripeClient(ctx, scope, purpose)
	}
	if pool, ok := r.pool.(*pgxpool.Pool); ok && pool != nil {
		return directory.New(pool, payment.NewAdapters()).ResolveStripeClient(ctx, scope, purpose)
	}
	return nil, &stripe.CredentialError{
		AccountID:   scope.AccountKey,
		Environment: stripe.Environment(scope.Environment),
		Purpose:     purpose,
		Reason:      stripe.ReasonCredentialMissing,
		Retryable:   true,
		Detail:      "Stripe credential directory is unavailable",
	}
}

// Import records a verified report atomically. read supplies local or already
// fetched evidence and returns an error if its source was incomplete.
func (r *Repository) Import(ctx context.Context, report Report, read func(func(ReportRow) error) error) (ImportResult, error) {
	if err := report.Audit.validate(); err != nil {
		return ImportResult{}, err
	}
	if report.Reference == "" || read == nil ||
		!((report.Provider == payment.ProviderPayPal && report.Source == PayPalReport) ||
			(report.Provider == payment.ProviderStripe && (report.Source == StripeReport || report.Source == StripePayout))) {
		return ImportResult{}, errors.New("invalid settlement report source")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return ImportResult{}, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	accountID, err := findAccount(ctx, tx, report.Provider, report.Account)
	if err != nil {
		return ImportResult{}, err
	}
	// Serialize multi-row imports per account so overlapping reports cannot
	// take movement locks in opposite order.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('payment-settlement:'||$1::bigint::text,0))`, accountID); err != nil {
		return ImportResult{}, err
	}
	var result ImportResult
	err = tx.QueryRow(ctx, `INSERT INTO payment_settlement_imports(account_id,source,external_ref,fingerprint,operator,reason)
		VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT (account_id,source,external_ref,fingerprint) DO NOTHING RETURNING id`,
		accountID, report.Source, report.Reference, report.Fingerprint[:], report.Audit.Operator, report.Audit.Reason).Scan(&result.ImportID)
	if errors.Is(err, pgx.ErrNoRows) {
		result.Replay = true
		err = tx.QueryRow(ctx, `SELECT i.id,count(r.import_id),count(r.review_reason)
			FROM payment_settlement_imports i LEFT JOIN payment_settlement_rows r ON r.import_id=i.id
			WHERE i.account_id=$1 AND i.source=$2 AND i.external_ref=$3 AND i.fingerprint=$4 GROUP BY i.id`,
			accountID, report.Source, report.Reference, report.Fingerprint[:]).Scan(&result.ImportID, &result.Rows, &result.Review)
	}
	if err != nil {
		return ImportResult{}, err
	}
	if !result.Replay {
		if err := read(func(row ReportRow) error { return recordReportRow(ctx, tx, report, accountID, &result, row) }); err != nil {
			return ImportResult{}, err
		}
		if report.Payout != nil {
			if err := recordPayout(ctx, tx, accountID, result.ImportID, *report.Payout); err != nil {
				return ImportResult{}, err
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return ImportResult{}, err
	}
	return result, nil
}

func findAccount(ctx context.Context, tx pgx.Tx, provider string, account payment.WebhookAccount) (int64, error) {
	var id int64
	err := tx.QueryRow(ctx, `SELECT id FROM payment_channel_accounts WHERE provider=$1 AND account_key=$2 AND environment=$3`,
		provider, account.AccountKey, account.Environment).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, errors.New("settlement account has not been registered")
	}
	return id, err
}

func recordReportRow(ctx context.Context, tx pgx.Tx, report Report, accountID int64, result *ImportResult, row ReportRow) error {
	var movementID *int64
	if row.Evidence != nil {
		if row.Evidence.Provider != report.Provider || row.Evidence.Account != report.Account {
			return errors.New("settlement row belongs to a different account")
		}
		if row.Evidence.Currency != CurrencyUSD {
			return fmt.Errorf("settlement row %d requires a USD balance", row.Number)
		}
		for _, amount := range row.Evidence.Amounts {
			if amount.Currency != CurrencyUSD {
				return fmt.Errorf("settlement row %d contains a non-USD amount", row.Number)
			}
		}
		movement, err := ledger.RecordTx(ctx, tx, *row.Evidence)
		if err != nil {
			return fmt.Errorf("settlement row %d: %w", row.Number, err)
		}
		movementID = &movement.MovementID
		if movement.Conflict {
			row.ReviewReason = "conflicting_ledger_evidence"
		}
	}
	withheldTaxMinor := row.WithheldTaxMinor
	feeNetOfWithheldTaxMinor := row.FeeNetOfWithheldTaxMinor
	if row.Evidence != nil {
		if withheldTaxMinor == nil {
			if m, ok := row.Evidence.Amounts[payment.LedgerWithheldTax]; ok {
				v := m.Minor
				withheldTaxMinor = &v
			}
		}
		if feeNetOfWithheldTaxMinor == nil {
			if m, ok := row.Evidence.Amounts[payment.LedgerProcessingFee]; ok {
				v := m.Minor
				feeNetOfWithheldTaxMinor = &v
			}
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO payment_settlement_rows(import_id,source_file,row_number,movement_id,payload,review_reason,withheld_tax_minor,fee_net_of_withheld_tax_minor)
		VALUES($1,$2,$3,$4,$5,NULLIF($6,''),$7,$8)`,
		result.ImportID, row.File, row.Number, movementID, row.Payload, row.ReviewReason, withheldTaxMinor, feeNetOfWithheldTaxMinor); err != nil {
		return err
	}
	result.Rows++
	if row.ReviewReason != "" {
		result.Review++
	}
	if row.Payout != nil {
		return recordPayout(ctx, tx, accountID, result.ImportID, *row.Payout)
	}
	return nil
}
