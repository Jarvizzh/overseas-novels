package db

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
	dbassets "star-novel-cms/db"
	"star-novel-cms/internal/testdb"
)

func TestMigrationAtomicityAndVersionGate(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, stage := range []string{"schema", "initial data"} {
		t.Run(stage+" failure rolls back", func(t *testing.T) {
			pool := testdb.Open(t)
			originalSchema, originalSeed := dbassets.SchemaSQL, dbassets.InitialDataSQL
			t.Cleanup(func() { dbassets.SchemaSQL, dbassets.InitialDataSQL = originalSchema, originalSeed })
			if stage == "schema" {
				dbassets.SchemaSQL += "\nSELECT 1/0;"
			} else {
				dbassets.InitialDataSQL += "\nSELECT 1/0;"
			}
			if err := autoMigrate(ctx, pool); err == nil {
				t.Fatal("migration failure ignored")
			}
			var ledger *string
			if err := pool.QueryRow(ctx, "SELECT to_regclass($1)::text", "public.reader_schema_migrations").Scan(&ledger); err != nil || ledger != nil {
				t.Fatalf("partial migration committed: %v %v", ledger, err)
			}
		})
	}
	pool := testdb.Open(t)
	if err := autoMigrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var businessRows int
	if err := pool.QueryRow(ctx, `SELECT
    (SELECT count(*) FROM novels) + (SELECT count(*) FROM chapters) +
    (SELECT count(*) FROM recharge_templates) + (SELECT count(*) FROM recharge_slots) +
    (SELECT count(*) FROM products) + (SELECT count(*) FROM product_publications)`).Scan(&businessRows); err != nil || businessRows != 0 {
		t.Fatalf("fresh database contains sample data: %d rows, error %v", businessRows, err)
	}
	var before, after time.Time
	if err := pool.QueryRow(ctx, "SELECT applied_at FROM reader_schema_migrations WHERE version=$1", RequiredSchemaVersion).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := autoMigrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT applied_at FROM reader_schema_migrations WHERE version=$1", RequiredSchemaVersion).Scan(&after); err != nil || !after.Equal(before) {
		t.Fatalf("cutover was extended: %s %s %v", before, after, err)
	}
	if _, err := pool.Exec(ctx, "INSERT INTO reader_schema_migrations(version) VALUES($1)", RequiredSchemaVersion+1); err != nil {
		t.Fatal(err)
	}
	if err := autoMigrate(ctx, pool); err == nil {
		t.Fatal("older CMS accepted newer schema")
	}
}

func TestMigrationSkipsBaselineOnUpgradeAndConcurrentRestart(t *testing.T) {
	pool := testdb.Open(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := pool.Exec(ctx, dbassets.SchemaSQL); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO reader_schema_migrations(version) VALUES(2026111942);
		INSERT INTO novels(id,title,author) VALUES(1,'Book','Author');
		INSERT INTO system_configs VALUES('global_coin_cost_per_thousand','777');
		DROP INDEX public.idx_novels_reader_latest;`); err != nil {
		t.Fatal(err)
	}
	if err := autoMigrate(ctx, pool); err == nil || !strings.Contains(err.Error(), "public.idx_novels_reader_latest (missing)") {
		t.Fatalf("missing index: %v", err)
	}
	var version int64
	if err := pool.QueryRow(ctx, "SELECT max(version) FROM reader_schema_migrations").Scan(&version); err != nil || version != 2026111942 {
		t.Fatalf("unexpected version %d: %v", version, err)
	}
	if _, err := pool.Exec(ctx, `CREATE INDEX CONCURRENTLY idx_novels_reader_latest ON novels(created_at DESC,id DESC) WHERE status <> 'archived' AND publish_status = 'published'`); err != nil {
		t.Fatal(err)
	}
	// An ordinary index build would conflict with an active order writer.
	writer, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback(ctx)
	if _, err := writer.Exec(ctx, "LOCK TABLE recharge_orders IN ROW EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}
	original := dbassets.SchemaSQL
	dbassets.SchemaSQL = "SELECT 1/0;\n" + original
	t.Cleanup(func() { dbassets.SchemaSQL = original })
	results := make(chan error, 3)
	for range 3 {
		go func() { results <- autoMigrate(ctx, pool) }()
	}
	for range 3 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	var preserved bool
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT value FROM system_configs WHERE key='global_coin_cost_per_thousand')='777'
		AND NOT EXISTS(SELECT FROM roles)
		AND to_regclass('public.idx_novels_reader_latest') IS NOT NULL
		AND EXISTS(SELECT FROM reader_schema_migrations WHERE version=$1)`, RequiredSchemaVersion).Scan(&preserved); err != nil || !preserved {
		t.Fatalf("upgrade lost existing configuration or missed an index: %v", err)
	}

	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(ctx)
	if _, err := blocker.Exec(ctx, "LOCK TABLE transactions IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}
	restartCtx, stop := context.WithTimeout(ctx, 500*time.Millisecond)
	defer stop()
	if err := autoMigrate(restartCtx, pool); err != nil {
		t.Fatalf("same-version migration tried to lock a business table: %v", err)
	}
	var title string
	if err := pool.QueryRow(restartCtx, "SELECT title FROM novels WHERE id=1").Scan(&title); err != nil || title != "Book" {
		t.Fatalf("reader query blocked: %q %v", title, err)
	}
}

func TestRuntimeRoleGrants(t *testing.T) {
	grantsBytes, err := os.ReadFile(filepath.Join("..", "..", "ops", "conversion_role_grants.sql"))
	if err != nil {
		t.Fatalf("read conversion_role_grants.sql: %v", err)
	}
	grantsSQL := string(grantsBytes)
	for _, want := range []string{
		`GRANT SELECT ON public.roles, public.role_permissions TO :"reader_role";`,
		`GRANT SELECT, INSERT, UPDATE, DELETE ON public.roles, public.role_permissions TO :"cms_role";`,
		`GRANT USAGE, SELECT ON SEQUENCE public.roles_id_seq TO :"cms_role";`,
		`GRANT EXECUTE ON FUNCTION public.admin_has_permission(text, text) TO :"reader_role", :"cms_role";`,
	} {
		if !strings.Contains(grantsSQL, want) {
			t.Fatalf("conversion_role_grants.sql missing expected grant: %s", want)
		}
	}
}

func TestRBACMigrationAndBootstrapSuperAdmin(t *testing.T) {
	pool := testdb.Open(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Missing password on empty DB must be rejected and rolled back.
	if err := autoMigrateAndBootstrap(ctx, pool, BootstrapAdminConfig{Username: "admin", Password: ""}); err == nil {
		t.Fatal("expected empty bootstrap password to fail")
	}
	// Weak password must be rejected and rolled back.
	if err := autoMigrateAndBootstrap(ctx, pool, BootstrapAdminConfig{Username: "admin", Password: "admin123"}); err == nil {
		t.Fatal("expected weak bootstrap password to fail")
	}

	strongPass := "StarNovel!2026#Bootstrap"
	if err := autoMigrateAndBootstrap(ctx, pool, BootstrapAdminConfig{
		Username: "rootadmin",
		Password: strongPass,
		Nickname: "初始超管",
	}); err != nil {
		t.Fatalf("bootstrap migration failed: %v", err)
	}

	var roleCount, leaderPerms, optimizerPerms, contentPerms, financePerms int
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM roles WHERE is_system = TRUE),
		(SELECT count(*) FROM role_permissions rp JOIN roles r ON r.id = rp.role_id WHERE r.code = 'Leader'),
		(SELECT count(*) FROM role_permissions rp JOIN roles r ON r.id = rp.role_id WHERE r.code = 'AdOptimizer'),
		(SELECT count(*) FROM role_permissions rp JOIN roles r ON r.id = rp.role_id WHERE r.code = 'ContentOperations'),
		(SELECT count(*) FROM role_permissions rp JOIN roles r ON r.id = rp.role_id WHERE r.code = 'FinanceManager')
	`).Scan(&roleCount, &leaderPerms, &optimizerPerms, &contentPerms, &financePerms); err != nil {
		t.Fatal(err)
	}
	if roleCount != 5 || leaderPerms != 20 || optimizerPerms != 10 || contentPerms != 12 || financePerms != 22 {
		t.Fatalf("unexpected built-in role matrix: roles=%d leader=%d optimizer=%d content=%d finance=%d",
			roleCount, leaderPerms, optimizerPerms, contentPerms, financePerms)
	}

	var adminID, passwordHash, roleCode string
	var isSuper bool
	var auditCount int
	if err := pool.QueryRow(ctx, `
		SELECT a.id, a.password_hash, r.code, r.is_super_admin,
		       (SELECT count(*) FROM admin_audit_logs WHERE action = 'bootstrap_super_admin' AND target_id = a.id)
		FROM admins a
		JOIN roles r ON r.id = a.role_id
		WHERE a.username = 'rootadmin'
	`).Scan(&adminID, &passwordHash, &roleCode, &isSuper, &auditCount); err != nil {
		t.Fatal(err)
	}
	if roleCode != "Admin" || !isSuper || auditCount != 1 {
		t.Fatalf("unexpected bootstrap admin: role=%s super=%v audit=%d", roleCode, isSuper, auditCount)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(strongPass)); err != nil {
		t.Fatalf("bootstrap password hash mismatch: %v", err)
	}

	// Re-running with empty password or different password must be idempotent and never reset password.
	if err := autoMigrateAndBootstrap(ctx, pool, BootstrapAdminConfig{Username: "rootadmin", Password: ""}); err != nil {
		t.Fatalf("idempotent re-migrate with empty password failed: %v", err)
	}
	if err := autoMigrateAndBootstrap(ctx, pool, BootstrapAdminConfig{Username: "rootadmin", Password: "Another!2026#Password"}); err != nil {
		t.Fatalf("idempotent re-migrate with new password failed: %v", err)
	}
	var afterHash string
	if err := pool.QueryRow(ctx, `SELECT password_hash FROM admins WHERE id = $1`, adminID).Scan(&afterHash); err != nil {
		t.Fatal(err)
	}
	if afterHash != passwordHash {
		t.Fatal("idempotent re-migrate must not overwrite existing super admin password")
	}

	// Verify SQL permission helper functions for super admin.
	var hasPerm bool
	var lockedRole, lockedSuperRole string
	if err := pool.QueryRow(ctx, `SELECT
		public.admin_has_permission($1, 'payments.refunds:write'),
		(SELECT role_code FROM conversion.lock_active_admin_for_permission($1, 'ads.conversions:write')),
		(SELECT role_code FROM conversion.lock_active_super_admin($1))
	`, adminID).Scan(&hasPerm, &lockedRole, &lockedSuperRole); err != nil {
		t.Fatal(err)
	}
	if !hasPerm || lockedRole != "Admin" || lockedSuperRole != "Admin" {
		t.Fatalf("unexpected SQL function results: hasPerm=%v locked=%q super=%q", hasPerm, lockedRole, lockedSuperRole)
	}
}

func TestMigrationUpgradeFrom2026112801AddsListPriceCents(t *testing.T) {
	pool := testdb.Open(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	if _, err := pool.Exec(ctx, dbassets.SchemaSQL); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO reader_schema_migrations(version) VALUES(2026112801) ON CONFLICT DO NOTHING;
		ALTER TABLE public.recharge_slots DROP COLUMN IF EXISTS list_price_cents;
	`); err != nil {
		t.Fatal(err)
	}

	if err := autoMigrate(ctx, pool); err != nil {
		t.Fatalf("upgrade 2026112801 -> 2026112802 failed: %v", err)
	}

	var version int64
	var hasCol, hasConstraint bool
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT COALESCE(max(version), 0) FROM reader_schema_migrations),
		EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_schema = 'public' AND table_name = 'recharge_slots' AND column_name = 'list_price_cents'
		),
		EXISTS (
			SELECT 1 FROM pg_constraint WHERE conname = 'recharge_slots_list_price_cents_check'
		)
	`).Scan(&version, &hasCol, &hasConstraint); err != nil {
		t.Fatal(err)
	}
	if version != RequiredSchemaVersion || !hasCol || !hasConstraint {
		t.Fatalf("unexpected post-upgrade state: version=%d hasCol=%v hasConstraint=%v", version, hasCol, hasConstraint)
	}
}
