package paymentpolicy

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

var (
	ErrPaymentConflict = errors.New("payment_policy_fact_conflict")
	ErrSequenceLimit   = errors.New("payment_policy_sequence_exhausted")
)

// FirstPaymentLookup resolves a missing first-payment fact in the decision transaction.
type FirstPaymentLookup func(context.Context, pgx.Tx, string) (*bool, error)

type Decision struct {
	ID            int64  `json:"id,string"`
	VersionID     *int64 `json:"version_id,string,omitempty"`
	BranchID      *int64 `json:"branch_id,string,omitempty"`
	Mode          string `json:"mode,omitempty"`
	CycleSequence *int64 `json:"cycle_sequence,string,omitempty"`
	SampleBucket  *int   `json:"sample_bucket,omitempty"`
	SampleVersion *int   `json:"sample_version,omitempty"`
	Outcome       string `json:"outcome"`
	Reason        string `json:"reason"`
}

// DecideTx resolves one authorized payment in the caller's event transaction.
// ErrFactMissing leaves no decision or counter changes and allows the caller
// to commit a blocked event. Keep the integration lock ahead of the payment
// claim so workers already holding it cannot wait behind a queued publisher.
func DecideTx(ctx context.Context, tx pgx.Tx, f Facts) (Decision, error) {
	if f.Environment == "" || f.IntegrationID <= 0 || f.App == "" || f.PaymentKey == "" || f.Destination <= 0 ||
		(f.AmountMinor != nil && (*f.AmountMinor < 0 || f.Currency == "")) {
		return Decision{}, invalid("invalid_payment_policy_facts", "payment")
	}
	if f.Currency != "" {
		if _, ok := currencyUnit(f.Currency); !ok {
			return Decision{}, invalid("invalid_currency", "currency")
		}
	}
	previous, err := existingDecision(ctx, tx, f)
	if !errors.Is(err, pgx.ErrNoRows) {
		return previous, err
	}

	var integrationID int64
	err = tx.QueryRow(ctx, `SELECT id FROM conversion.integrations WHERE id=$1 AND environment=$2 FOR SHARE`,
		f.IntegrationID, f.Environment).Scan(&integrationID)
	if err != nil {
		return Decision{}, err
	}
	var out Decision
	err = pgx.BeginFunc(ctx, tx, func(claim pgx.Tx) error {
		err := claim.QueryRow(ctx, `
			INSERT INTO conversion.payment_policy_decisions
				(environment,integration_id,app_scope,payment_key,scope_kind,promotion_key,
				 original_destination_id,amount_minor,currency,payment_kind)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,NULLIF($9,''),$10)
			ON CONFLICT (environment,integration_id,app_scope,payment_key) DO NOTHING RETURNING id`,
			f.Environment, f.IntegrationID, f.App, f.PaymentKey, f.kind(), f.Promotion, f.Destination,
			f.AmountMinor, f.Currency, f.Kind).Scan(&out.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			// A concurrent insert can commit while the INSERT is waiting. Read
			// it in a separate statement with the caller's READ COMMITTED snapshot.
			out, err = existingDecision(ctx, claim, f)
			return err
		}
		if err != nil {
			return err
		}
		return completeDecision(ctx, claim, f, &out)
	})
	if err != nil {
		return Decision{}, err
	}
	return out, nil
}

func existingDecision(ctx context.Context, tx pgx.Tx, f Facts) (Decision, error) {
	var out Decision
	var promotion, currency, kind string
	var destination int64
	var amount *int64
	err := tx.QueryRow(ctx, `
		SELECT id,policy_version_id,branch_id,COALESCE(mode,''),cycle_sequence,sample_bucket,sample_version,
		       outcome,reason,promotion_key,original_destination_id,amount_minor,COALESCE(currency,''),payment_kind
		FROM conversion.payment_policy_decisions
		WHERE environment=$1 AND integration_id=$2 AND app_scope=$3 AND payment_key=$4`,
		f.Environment, f.IntegrationID, f.App, f.PaymentKey).Scan(&out.ID, &out.VersionID, &out.BranchID, &out.Mode,
		&out.CycleSequence, &out.SampleBucket, &out.SampleVersion, &out.Outcome, &out.Reason,
		&promotion, &destination, &amount, &currency, &kind)
	if err != nil {
		return Decision{}, err
	}
	if promotion != f.Promotion || destination != f.Destination ||
		(amount != nil && f.AmountMinor != nil && *amount != *f.AmountMinor) ||
		(currency != "" && f.Currency != "" && currency != f.Currency) ||
		(kind != "" && f.Kind != "" && kind != f.Kind) {
		return Decision{}, ErrPaymentConflict
	}
	return out, nil
}

func completeDecision(ctx context.Context, tx pgx.Tx, f Facts, out *Decision) error {
	own, inherited, err := versionsTx(ctx, tx, f.Scope)
	if err != nil {
		return err
	}
	version := own
	if version == nil {
		version = inherited
	}
	config, _ := effective(own, inherited)
	if config.Range == FirstPaid && f.First == nil && f.LookupFirst != nil {
		f.First, err = f.LookupFirst(ctx, tx, f.Environment)
		if err != nil {
			return err
		}
	}
	matched, err := config.match(f)
	if err != nil {
		return err
	}
	var policyScope *int64
	if version != nil {
		out.VersionID, policyScope = &version.ID, &version.scope
	}
	if matched.ID != 0 {
		out.BranchID = &matched.ID
	}
	out.Mode, out.Reason = matched.Mode, matched.Reason
	switch matched.Mode {
	case "":
		out.Outcome = Skipped
	case All:
		out.Outcome = Selected
		if out.Reason == "" {
			out.Reason = "rule_send_all"
		}
	case Ratio:
		bucket, algorithm := sample(f), sampleVersion
		out.SampleBucket, out.SampleVersion = &bucket, &algorithm
		out.Outcome, out.Reason = Skipped, "rule_ratio_skipped"
		if bucket < *matched.RateBPS {
			out.Outcome, out.Reason = Selected, "rule_ratio_selected"
		}
	case Cycle:
		var seq int64
		err := tx.QueryRow(ctx, `
			INSERT INTO conversion.payment_policy_counters
				(environment,integration_id,app_scope,scope_kind,promotion_key,branch_id,last_sequence)
			VALUES ($1,$2,$3,$4,$5,$6,1)
			ON CONFLICT (environment,integration_id,app_scope,scope_kind,promotion_key,branch_id)
			DO UPDATE SET last_sequence=payment_policy_counters.last_sequence+1
			WHERE payment_policy_counters.last_sequence<$7
			RETURNING last_sequence`, f.Environment, f.IntegrationID, f.App, f.kind(), f.Promotion, matched.ID, MaxSequence).Scan(&seq)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrSequenceLimit
		}
		if err != nil {
			return err
		}
		out.CycleSequence = &seq
		out.Outcome, out.Reason = Skipped, "rule_cycle_skipped"
		if (seq-1)%(*matched.SendCount+*matched.SkipCount) < *matched.SendCount {
			out.Outcome, out.Reason = Selected, "rule_cycle_selected"
		}
	default:
		return invalid("invalid_payment_policy", "mode")
	}
	_, err = tx.Exec(ctx, `
		UPDATE conversion.payment_policy_decisions
		SET policy_scope_id=$2,policy_version_id=$3,branch_id=$4,mode=NULLIF($5,''),
		    cycle_sequence=$6,sample_bucket=$7,sample_version=$8,outcome=$9,reason=$10,decided_at=clock_timestamp()
		WHERE id=$1`, out.ID, policyScope, out.VersionID, out.BranchID, out.Mode, out.CycleSequence,
		out.SampleBucket, out.SampleVersion, out.Outcome, out.Reason)
	return err
}
