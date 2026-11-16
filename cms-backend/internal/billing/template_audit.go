package billing

import (
	"context"
	"errors"
	"strconv"

	"github.com/jackc/pgx/v5"
)

func requireFinancialAdmin(ctx context.Context, tx pgx.Tx, adminID string) error {
	if adminID == "" {
		return ErrRefundForbidden
	}
	var role string
	var status int
	err := tx.QueryRow(ctx, `SELECT role,status FROM admins WHERE id=$1 FOR SHARE`, adminID).Scan(&role, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrRefundForbidden
	}
	if err != nil {
		return err
	}
	if status != 1 || (role != "Finance" && role != "SuperAdmin") {
		return ErrRefundForbidden
	}
	return nil
}

func (s *billingService) beginTemplateChange(ctx context.Context, adminID string) (pgx.Tx, error) {
	tx, err := s.repo.BeginTx(ctx)
	if err != nil {
		return nil, err
	}
	if err := requireFinancialAdmin(ctx, tx, adminID); err != nil {
		tx.Rollback(context.WithoutCancel(ctx))
		return nil, err
	}
	// Serialize changes to the shared default and their before/after audit snapshots.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(1928374601)`); err != nil {
		tx.Rollback(context.WithoutCancel(ctx))
		return nil, err
	}
	return tx, nil
}

func templateSnapshot(ctx context.Context, tx pgx.Tx, id int) (string, error) {
	var snapshot string
	err := tx.QueryRow(ctx, `SELECT jsonb_build_object(
 'template',(SELECT to_jsonb(t) FROM recharge_templates t WHERE id=$1),
 'slots',COALESCE((SELECT jsonb_agg(to_jsonb(s) ORDER BY slot_index,id) FROM recharge_slots s WHERE template_id=$1),'[]'::jsonb),
 'default_template_ids',COALESCE((SELECT jsonb_agg(id ORDER BY id) FROM recharge_templates WHERE is_default),'[]'::jsonb))::text`, id).Scan(&snapshot)
	return snapshot, err
}

func (s *billingService) auditTemplateChange(ctx context.Context, tx pgx.Tx, adminID, action string, id int, before string) error {
	after, err := templateSnapshot(ctx, tx, id)
	if err != nil {
		return err
	}
	return s.repo.InsertAdminAuditLogTx(ctx, tx, adminID, action, strconv.Itoa(id), before, after)
}
