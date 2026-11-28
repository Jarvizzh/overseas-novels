package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
	dbassets "star-novel-cms/db"
	"star-novel-cms/internal/config"
)

var DB *pgxpool.Pool

func InitDB(migrate bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	cfg, err := pgxpool.ParseConfig(config.AppConfig.DatabaseURL)
	if err != nil {
		log.Fatalf("Unable to parse database URL: %v", err)
	}

	cfg.ConnConfig.ConnectTimeout = 5 * time.Second
	cfg.MaxConns = 15
	cfg.MinConns = 2
	cfg.MaxConnIdleTime = 15 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		log.Fatalf("Unable to connect to database: %v", err)
	}

	if err := pool.Ping(ctx); err != nil {
		log.Fatalf("Database ping failed: %v", err)
	}

	log.Println("CMS Database connection pool initialized successfully")
	DB = pool

	check := CheckSchema
	if migrate {
		bootstrapCfg := bootstrapConfigFromAppConfig()
		check = func(ctx context.Context) error { return autoMigrateAndBootstrap(ctx, pool, bootstrapCfg) }
	}
	if err := check(ctx); err != nil {
		pool.Close()
		log.Fatalf("Database schema check failed: %v", err)
	}
}

const RequiredSchemaVersion int64 = 2026112802

// BootstrapAdminConfig holds environment-injected credentials for initializing
// the first super admin on an empty database during `-migrate`.
type BootstrapAdminConfig struct {
	Username string
	Password string
	Nickname string
}

func bootstrapConfigFromAppConfig() BootstrapAdminConfig {
	if config.AppConfig == nil {
		return BootstrapAdminConfig{
			Username: "admin",
			Nickname: "管理员",
		}
	}
	return BootstrapAdminConfig{
		Username: config.AppConfig.BootstrapAdminUsername,
		Password: config.AppConfig.BootstrapAdminPassword,
		Nickname: config.AppConfig.BootstrapAdminNickname,
	}
}

func autoMigrate(ctx context.Context, pool *pgxpool.Pool) error {
	return autoMigrateInternal(ctx, pool, nil)
}

func autoMigrateAndBootstrap(ctx context.Context, pool *pgxpool.Pool, bootstrap BootstrapAdminConfig) error {
	return autoMigrateInternal(ctx, pool, &bootstrap)
}

func autoMigrateInternal(ctx context.Context, pool *pgxpool.Pool, bootstrap *BootstrapAdminConfig) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SET LOCAL statement_timeout = '60s'"); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(2026111701)"); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "SET LOCAL search_path = public, pg_temp"); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "SET LOCAL lock_timeout = '3s'"); err != nil {
		return err
	}
	var ledger *string
	if err := tx.QueryRow(ctx, "SELECT to_regclass(format('%I.reader_schema_migrations', current_schema()))::text").Scan(&ledger); err != nil {
		return err
	}
	var version int64
	if ledger != nil {
		if err := tx.QueryRow(ctx, "SELECT COALESCE(max(version),0) FROM reader_schema_migrations").Scan(&version); err != nil {
			return err
		}
		if version > RequiredSchemaVersion {
			return fmt.Errorf("schema %d newer than CMS %d", version, RequiredSchemaVersion)
		}
	}

	switch {
	case ledger == nil:
		if _, err := tx.Exec(ctx, dbassets.SchemaSQL); err != nil {
			return fmt.Errorf("create schema: %w", err)
		}
		if _, err := tx.Exec(ctx, dbassets.InitialDataSQL); err != nil {
			return fmt.Errorf("initialize database: %w", err)
		}
	case version == 2026111942:
		if err := checkLegacyQueryIndexes(ctx, tx); err != nil {
			return err
		}
		if err := applyMigration2026112802(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "INSERT INTO reader_schema_migrations(version) VALUES(2026112801), ($1) ON CONFLICT (version) DO NOTHING", RequiredSchemaVersion); err != nil {
			return err
		}
	case version == 2026112801:
		if err := applyMigration2026112802(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "INSERT INTO reader_schema_migrations(version) VALUES($1) ON CONFLICT (version) DO NOTHING", RequiredSchemaVersion); err != nil {
			return err
		}
	case version != RequiredSchemaVersion:
		return fmt.Errorf("schema %d must first be upgraded to 2026111942 with the previous CMS release", version)
	}

	if bootstrap != nil {
		if err := ensureBootstrapSuperAdmin(ctx, tx, *bootstrap); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// The 1942 release shipped these indexes separately. Check the catalogs without
// issuing DDL against business tables during an upgrade.
func checkLegacyQueryIndexes(ctx context.Context, tx pgx.Tx) error {
	var issues string
	err := tx.QueryRow(ctx, `
 SELECT coalesce(string_agg(name || CASE WHEN i.indexrelid IS NULL THEN ' (missing)' ELSE ' (invalid)' END, ', ' ORDER BY name), '')
 FROM unnest(ARRAY[
        'public.idx_orders_subscription_period',
        'public.idx_whole_book_entitlement_v2',
        'public.idx_transactions_user_feed',
        'public.idx_novels_reader_popular',
        'public.idx_novels_reader_latest',
        'public.idx_payment_outbox_pending',
        'public.idx_reader_sessions_user',
        'public.idx_paypal_order_recent_pending',
        'public.idx_paypal_order_retained_claim',
        'public.idx_paypal_cancel_claim',
        'public.idx_paypal_subscription_continuous',
        'public.idx_paypal_subscription_recent',
        'public.idx_outbox_unattributed_oldest',
        'public.idx_payment_order_attention',
        'public.idx_reader_shelf_activity',
        'public.idx_promotion_links_utm',
        'public.idx_reader_paid_vip_expiry',
        'public.recharge_orders_conversion_fulfilled_at',
        'public.conversion_outbox_retention',
        'public.conversion_repair_fact_retention',
        'conversion.conversion_event_retention',
        'conversion.conversion_inbox_retention',
        'conversion.conversion_events_rule_fact_missing',
        'conversion.conversion_audit_policy_publication',
        'public.conversion_outbox_throughput_created',
        'public.conversion_outbox_throughput_consumed',
        'conversion.conversion_inbox_throughput_received',
        'conversion.conversion_deliveries_throughput_created',
        'conversion.conversion_deliveries_throughput_finalized',
        'conversion.conversion_attempts_throughput_sending'
 ]) AS expected(name)
 LEFT JOIN pg_index i ON i.indexrelid = to_regclass(name)
 WHERE i.indisvalid IS DISTINCT FROM TRUE`).Scan(&issues)
	if err != nil {
		return fmt.Errorf("check baseline query indexes: %w", err)
	}
	if issues != "" {
		return fmt.Errorf("baseline query indexes require repair before migration: %s", issues)
	}
	return nil
}

func applyMigration2026112802(ctx context.Context, tx pgx.Tx) error {
	const ddl = `
ALTER TABLE public.recharge_slots
	ADD COLUMN IF NOT EXISTS list_price_cents integer NOT NULL DEFAULT 0;
DO $$
BEGIN
	IF NOT EXISTS (
		SELECT 1 FROM pg_constraint WHERE conname = 'recharge_slots_list_price_cents_check'
	) THEN
		ALTER TABLE public.recharge_slots
			ADD CONSTRAINT recharge_slots_list_price_cents_check CHECK (list_price_cents >= 0);
	END IF;
END $$;`
	if _, err := tx.Exec(ctx, ddl); err != nil {
		return fmt.Errorf("apply 2026112802 list_price_cents migration: %w", err)
	}
	return nil
}

func ensureBootstrapSuperAdmin(ctx context.Context, tx pgx.Tx, cfg BootstrapAdminConfig) error {
	var hasActiveSuperAdmin bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM admins a
			JOIN roles r ON r.id = a.role_id
			WHERE r.is_super_admin = TRUE
			  AND a.status = 1
			  AND r.status = 1
		)
	`).Scan(&hasActiveSuperAdmin)
	if err != nil {
		return fmt.Errorf("check active super admin: %w", err)
	}
	if hasActiveSuperAdmin {
		return nil
	}

	username := strings.TrimSpace(cfg.Username)
	if username == "" {
		username = "admin"
	}
	nickname := strings.TrimSpace(cfg.Nickname)
	if nickname == "" {
		nickname = "管理员"
	}
	if err := config.ValidateBootstrapAdminConfig(username, cfg.Password, nickname); err != nil {
		return fmt.Errorf("bootstrap super admin: %w", err)
	}

	var roleID int64
	var roleCode string
	err = tx.QueryRow(ctx, `
		SELECT id, code
		FROM roles
		WHERE is_super_admin = TRUE AND status = 1
		ORDER BY id ASC
		LIMIT 1
	`).Scan(&roleID, &roleCode)
	if err != nil {
		return fmt.Errorf("resolve super admin role: %w", err)
	}

	hashed, err := bcrypt.GenerateFromPassword([]byte(cfg.Password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash bootstrap super admin password: %w", err)
	}

	adminID := uuid.New().String()
	_, err = tx.Exec(ctx, `
		INSERT INTO admins (id, username, password_hash, nickname, role, role_id, status)
		VALUES ($1, $2, $3, $4, $5, $6, 1)
	`, adminID, username, string(hashed), nickname, roleCode, roleID)
	if err != nil {
		return fmt.Errorf("create bootstrap super admin %q: %w", username, err)
	}

	afterJSON, err := json.Marshal(map[string]any{
		"username": username,
		"nickname": nickname,
		"role_id":  roleID,
		"role":     roleCode,
		"status":   1,
	})
	if err != nil {
		return fmt.Errorf("marshal bootstrap audit payload: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO admin_audit_logs (admin_id, action, target_id, after_data)
		VALUES ($1, 'bootstrap_super_admin', $2, $3::jsonb)
	`, adminID, adminID, string(afterJSON)); err != nil {
		return fmt.Errorf("write bootstrap super admin audit log: %w", err)
	}

	log.Printf("Bootstrapped initial super admin account: username=%s role=%s", username, roleCode)
	return nil
}

type sequenceSpec struct {
	table   string
	column  string
	seqName string
}

type migrationDB interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

// SyncSequence advances the backing PostgreSQL sequence for table.column when it lags behind MAX(column).
func SyncSequence(ctx context.Context, table, column, seqName string) error {
	if DB == nil {
		return errors.New("database connection pool is not initialized")
	}
	return syncOneSequence(ctx, DB, sequenceSpec{table: table, column: column, seqName: seqName})
}

func syncOneSequence(ctx context.Context, pool migrationDB, spec sequenceSpec) error {
	seqName := spec.seqName
	if seqName == "" {
		if err := pool.QueryRow(ctx, "SELECT COALESCE(pg_get_serial_sequence($1, $2), '')", spec.table, spec.column).Scan(&seqName); err != nil {
			return err
		}
		if seqName == "" {
			return nil
		}
	}

	maxQuery := fmt.Sprintf("SELECT MAX(%s) FROM %s", pgx.Identifier{spec.column}.Sanitize(), pgx.Identifier{spec.table}.Sanitize())
	var maxID *int64
	if err := pool.QueryRow(ctx, maxQuery).Scan(&maxID); err != nil {
		return err
	}
	if maxID == nil || *maxID <= 0 {
		return nil
	}

	var startValue int64
	var lastValue *int64
	err := pool.QueryRow(ctx, `
		SELECT start_value, last_value
		FROM pg_sequences
		WHERE (schemaname || '.' || sequencename = $1)
		   OR (schemaname = current_schema() AND sequencename = $1)
		LIMIT 1
	`, seqName).Scan(&startValue, &lastValue)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}

	needsAdvance := (lastValue == nil && *maxID >= startValue) || (lastValue != nil && *maxID > *lastValue)
	if !needsAdvance {
		return nil
	}

	if _, err := pool.Exec(ctx, "SELECT setval($1::regclass, $2, true)", seqName, *maxID); err != nil {
		return err
	}
	log.Printf("Synchronized sequence %s to %d (%s.%s)", seqName, *maxID, spec.table, spec.column)
	return nil
}

func CloseDB() {
	if DB != nil {
		DB.Close()
		log.Println("CMS Database connection pool closed")
	}
}

func CheckSchema(ctx context.Context) error {
	if DB == nil {
		return errors.New("database is not initialized")
	}
	var version int64
	if err := DB.QueryRow(ctx, "SELECT COALESCE(max(version),0) FROM reader_schema_migrations").Scan(&version); err != nil {
		return err
	}
	if version != RequiredSchemaVersion {
		return fmt.Errorf("schema %d, required %d", version, RequiredSchemaVersion)
	}
	return nil
}
