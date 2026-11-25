package aftercare

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"reader-backend/internal/payment"
)

type stubIntRow struct{ val int64 }

func (r stubIntRow) Scan(dest ...any) error {
	*(dest[0].(*int64)) = r.val
	return nil
}

type recordingTx struct {
	pgx.Tx
	execSQL []string
}

func (tx *recordingTx) QueryRow(_ context.Context, _ string, _ ...any) pgx.Row {
	return stubIntRow{val: 1}
}

func (tx *recordingTx) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	tx.execSQL = append(tx.execSQL, sql)
	return pgconn.CommandTag{}, nil
}

func TestRecordTxUsesPermissionFunctionsForAutoAssignment(t *testing.T) {
	base := payment.CaseFact{
		Provider:   payment.ProviderPayPal,
		Account:    payment.WebhookAccount{AccountKey: "acct-1", Environment: "sandbox"},
		ExternalID: "case-1",
		Source:     "webhook",
		SourceRef:  "wh-1",
		Status:     "open",
		Open:       true,
		ObservedAt: time.Now().UTC(),
		Evidence:   json.RawMessage(`{"ok":true}`),
	}

	t.Run("risk review keeps pending transition and guards reviewer_id with payments.risk:write", func(t *testing.T) {
		tx := &recordingTx{}
		fact := base
		fact.Kind = payment.CaseRiskReview
		if err := RecordTx(context.Background(), tx, fact); err != nil {
			t.Fatalf("RecordTx error: %v", err)
		}
		if len(tx.execSQL) != 2 {
			t.Fatalf("expected 2 exec statements for risk_review, got %d", len(tx.execSQL))
		}
		riskSQL := tx.execSQL[1]
		for _, want := range []string{
			"work_state = 'pending'",
			"public.admin_has_permission(p.reviewer_id, 'payments.risk:write')",
			"ELSE NULL",
			"work_due_at = COALESCE",
		} {
			if !strings.Contains(riskSQL, want) {
				t.Fatalf("risk_review SQL missing %q:\n%s", want, riskSQL)
			}
		}
		for _, legacy := range []string{"SuperAdmin", "Finance"} {
			if strings.Contains(riskSQL, legacy) {
				t.Fatalf("risk_review SQL still references legacy role %q:\n%s", legacy, riskSQL)
			}
		}
	})

	t.Run("dispute auto-assignment checks payments.disputes:write", func(t *testing.T) {
		tx := &recordingTx{}
		fact := base
		fact.Kind = payment.CaseDispute
		if err := RecordTx(context.Background(), tx, fact); err != nil {
			t.Fatalf("RecordTx error: %v", err)
		}
		if len(tx.execSQL) != 3 {
			t.Fatalf("expected 3 exec statements for dispute, got %d", len(tx.execSQL))
		}
		disputeSQL := tx.execSQL[2]
		if !strings.Contains(disputeSQL, "public.admin_has_permission(finance.id, 'payments.disputes:write')") {
			t.Fatalf("dispute SQL missing permission check:\n%s", disputeSQL)
		}
		for _, legacy := range []string{"SuperAdmin", "Finance"} {
			if strings.Contains(disputeSQL, legacy) {
				t.Fatalf("dispute SQL still references legacy role %q:\n%s", legacy, disputeSQL)
			}
		}
	})
}
