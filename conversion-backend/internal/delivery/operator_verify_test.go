package delivery

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type stubRow struct {
	role string
	err  error
}

func (r stubRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*(dest[0].(*string)) = r.role
	return nil
}

type stubTx struct {
	pgx.Tx
	gotSQL  string
	gotArgs []any
	row     stubRow
}

func (tx *stubTx) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	tx.gotSQL = sql
	tx.gotArgs = append([]any(nil), args...)
	return tx.row
}

func (tx *stubTx) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func TestVerifyOperatorTxPermissionAndSuperAdmin(t *testing.T) {
	ctx := context.Background()

	t.Run("business permission calls lock_active_admin_for_permission and backfills real role_code", func(t *testing.T) {
		tx := &stubTx{row: stubRow{role: "CustomOptimizer"}}
		got, err := verifyOperatorTx(ctx, tx, Operator{ID: "op-1", Permission: PermissionAdsConversionsWrite})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if !strings.Contains(tx.gotSQL, "SELECT role_code FROM conversion.lock_active_admin_for_permission($1, $2)") {
			t.Fatalf("unexpected SQL: %s", tx.gotSQL)
		}
		if len(tx.gotArgs) != 2 || tx.gotArgs[0] != "op-1" || tx.gotArgs[1] != PermissionAdsConversionsWrite {
			t.Fatalf("unexpected args: %#v", tx.gotArgs)
		}
		if got.Role != "CustomOptimizer" {
			t.Fatalf("expected role_code backfill, got %+v", got)
		}
	})

	t.Run("super admin mode calls lock_active_super_admin and backfills role_code", func(t *testing.T) {
		tx := &stubTx{row: stubRow{role: "Admin"}}
		got, err := verifyOperatorTx(ctx, tx, Operator{ID: "root-1", RequireSuperAdmin: true})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if !strings.Contains(tx.gotSQL, "SELECT role_code FROM conversion.lock_active_super_admin($1)") {
			t.Fatalf("unexpected SQL: %s", tx.gotSQL)
		}
		if len(tx.gotArgs) != 1 || tx.gotArgs[0] != "root-1" {
			t.Fatalf("unexpected args: %#v", tx.gotArgs)
		}
		if got.Role != "Admin" {
			t.Fatalf("expected role_code backfill, got %+v", got)
		}
	})

	t.Run("rejects empty ID, missing permission, ErrNoRows, and role mismatch", func(t *testing.T) {
		if _, err := verifyOperatorTx(ctx, &stubTx{}, Operator{ID: " ", Permission: PermissionAdsPixelsWrite}); !errors.Is(err, ErrOperatorForbidden) {
			t.Fatalf("blank ID: got %v", err)
		}
		if _, err := verifyOperatorTx(ctx, &stubTx{}, Operator{ID: "op-1"}); !errors.Is(err, ErrOperatorForbidden) {
			t.Fatalf("missing permission: got %v", err)
		}
		if _, err := verifyOperatorTx(ctx, &stubTx{row: stubRow{err: pgx.ErrNoRows}}, Operator{ID: "op-1", Permission: PermissionAdsPixelsWrite}); !errors.Is(err, ErrOperatorForbidden) {
			t.Fatalf("no rows: got %v", err)
		}
		if _, err := verifyOperatorTx(ctx, &stubTx{row: stubRow{err: pgx.ErrNoRows}}, Operator{ID: "op-1", RequireSuperAdmin: true}); !errors.Is(err, ErrOperatorForbidden) {
			t.Fatalf("super admin no rows: got %v", err)
		}
		if _, err := verifyOperatorTx(ctx, &stubTx{row: stubRow{role: "AdOptimizer"}}, Operator{ID: "op-1", Role: "Admin", Permission: PermissionAdsPixelsWrite}); !errors.Is(err, ErrOperatorForbidden) {
			t.Fatalf("role mismatch: got %v", err)
		}
	})

	t.Run("propagates database errors", func(t *testing.T) {
		boom := errors.New("connection lost")
		if _, err := verifyOperatorTx(ctx, &stubTx{row: stubRow{err: boom}}, Operator{ID: "op-1", Permission: PermissionAdsPixelsWrite}); !errors.Is(err, boom) {
			t.Fatalf("expected db error, got %v", err)
		}
	})
}

func TestRequireBusinessPermission(t *testing.T) {
	got, err := requireBusinessPermission(Operator{ID: "op-1"}, PermissionAdsPixelsWrite)
	if err != nil || got.Permission != PermissionAdsPixelsWrite {
		t.Fatalf("default business perm: %+v %v", got, err)
	}
	if _, err := requireBusinessPermission(Operator{ID: "op-1", Permission: PermissionAdsConversionsWrite}, PermissionAdsPixelsWrite); !errors.Is(err, ErrOperatorForbidden) {
		t.Fatalf("mismatched perm: %v", err)
	}
	if _, err := requireBusinessPermission(Operator{ID: "op-1", RequireSuperAdmin: true}, PermissionAdsPixelsWrite); !errors.Is(err, ErrOperatorForbidden) {
		t.Fatalf("super admin flag on business method: %v", err)
	}
}
