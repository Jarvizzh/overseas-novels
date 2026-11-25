package billing

import (
	"context"
	"errors"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"star-novel-cms/internal/auth"
)

func requirePermissionTx(ctx context.Context, tx pgx.Tx, adminID string, forbiddenErr error, perms ...string) error {
	if err := auth.RequirePermissionTx(ctx, tx, adminID, perms...); err != nil {
		if errors.Is(err, auth.ErrAccountInactive) || errors.Is(err, auth.ErrPermissionDenied) {
			return forbiddenErr
		}
		return err
	}
	return nil
}

func verifyContextPermissions(c *gin.Context, forbiddenErr error, perms ...string) error {
	principal, err := resolvePrincipal(c)
	if err != nil {
		if errors.Is(err, auth.ErrAccountInactive) {
			return forbiddenErr
		}
		return err
	}
	if !principal.HasAllPermissions(perms...) {
		return forbiddenErr
	}
	return nil
}

func (s *billingService) beginTemplateChange(ctx context.Context, adminID string) (pgx.Tx, error) {
	tx, err := s.repo.BeginTx(ctx)
	if err != nil {
		return nil, err
	}
	if err := requirePermissionTx(ctx, tx, adminID, ErrRefundForbidden, auth.PermAdsTemplatesWrite); err != nil {
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
