package aftercare

import (
	"context"

	"reader-backend/internal/payment"
	"reader-backend/internal/payment/ledger"
)

func (r *Repository) RecordObservation(ctx context.Context, evidence *payment.CaseObservation) error {
	if evidence == nil {
		return nil
	}
	tx, err := r.store.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if err := RecordTx(ctx, tx, evidence.Fact); err != nil {
		return err
	}
	for _, movement := range evidence.Movements {
		if _, err := ledger.RecordTx(ctx, tx, movement); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
