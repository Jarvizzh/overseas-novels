package db

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	dbassets "star-novel-cms/db"
)

func migrationPool(t *testing.T) (*pgxpool.Pool, string) {
	t.Helper()
	dsn := os.Getenv("STAR_NOVEL_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires disposable PostgreSQL")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	database := "cms_test_" + uuid.NewString()[:8]
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+database); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(ctx, "DROP DATABASE "+database+" WITH (FORCE)"); err != nil {
			t.Error(err)
		}
		admin.Close()
	})
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.Database = database
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool, "public"
}

func TestMigrationAtomicityAndVersionGate(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, stage := range []string{"schema", "seed"} {
		t.Run(stage+" failure rolls back", func(t *testing.T) {
			pool, schema := migrationPool(t)
			originalSchema, originalSeed := dbassets.SchemaSQL, dbassets.SeedSQL
			t.Cleanup(func() { dbassets.SchemaSQL, dbassets.SeedSQL = originalSchema, originalSeed })
			if stage == "schema" {
				dbassets.SchemaSQL += "\nSELECT 1/0;"
			} else {
				dbassets.SeedSQL += "\nSELECT 1/0;"
			}
			if err := autoMigrate(ctx, pool); err == nil {
				t.Fatal("migration failure ignored")
			}
			var ledger *string
			if err := pool.QueryRow(ctx, "SELECT to_regclass($1)::text", schema+".reader_schema_migrations").Scan(&ledger); err != nil || ledger != nil {
				t.Fatalf("partial migration committed: %v %v", ledger, err)
			}
		})
	}
	pool, _ := migrationPool(t)
	if err := autoMigrate(ctx, pool); err != nil {
		t.Fatal(err)
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
	pool, _ := migrationPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := pool.Exec(ctx, dbassets.SchemaSQL); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO novels(id,title,author) VALUES(1,'Book','Author');
		INSERT INTO users(id) VALUES(1);
		INSERT INTO chapters(id,novel_id,chapter_index,title,content,created_at)
		VALUES('original',1,0,'Original','A',now()-interval '2 days')`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO unlock_records(user_id,novel_id,chapter_index,created_at)
		VALUES(1,1,0,clock_timestamp()),(1,1,1,clock_timestamp())`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO chapters(id,novel_id,chapter_index,title,content,created_at)
		VALUES('replacement',1,1,'Replacement','B',now()-interval '2 days')`); err != nil {
		t.Fatal(err)
	}
	var track string
	if err := pool.QueryRow(ctx, "SHOW track_commit_timestamp").Scan(&track); err != nil {
		t.Fatal(err)
	}

	original := dbassets.SchemaSQL
	dbassets.SchemaSQL = "SELECT 1/0;"
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
	var mapped, unresolved, versions int
	err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM unlock_records WHERE chapter_id='original' AND identity_evidence IS NOT NULL),
		(SELECT count(*) FROM unlock_records WHERE chapter_id IS NULL AND chapter_index=1),
		(SELECT count(*) FROM reader_schema_migrations WHERE version=$1)`, RequiredSchemaVersion).
		Scan(&mapped, &unresolved, &versions)
	wantMapped := 0
	if track == "on" {
		wantMapped = 1
	}
	if err != nil || mapped != wantMapped || unresolved != 1 || versions != 1 {
		t.Fatalf("migration evidence: resolved=%d unresolved=%d versions=%d error=%v", mapped, unresolved, versions, err)
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

func TestReaderPaymentReviewUpgrade(t *testing.T) {
	for _, baseline := range []struct {
		name    string
		version int64
	}{
		{"before review fixes", sinanPaymentPolicyVersion},
		{"before recovery fix", readerPaymentReviewVersion},
		{"before environment-scoped evidence", readerPaymentRecoveryVersion},
	} {
		t.Run(baseline.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			pool, _ := migrationPool(t)
			reviewSQL, recoverySQL := dbassets.ReaderPaymentReviewSQL, dbassets.ReaderPaymentRecoverySQL
			evidenceSQL := dbassets.ReaderSubscriptionEvidenceSQL
			t.Cleanup(func() {
				dbassets.ReaderPaymentReviewSQL, dbassets.ReaderPaymentRecoverySQL = reviewSQL, recoverySQL
				dbassets.ReaderSubscriptionEvidenceSQL = evidenceSQL
			})
			dbassets.ReaderSubscriptionEvidenceSQL = ""
			if baseline.version < readerPaymentRecoveryVersion {
				dbassets.ReaderPaymentRecoverySQL = ""
			}
			if baseline.version == sinanPaymentPolicyVersion {
				dbassets.ReaderPaymentReviewSQL = ""
			}
			if err := autoMigrate(ctx, pool); err != nil {
				t.Fatal(err)
			}
			dbassets.ReaderPaymentReviewSQL, dbassets.ReaderPaymentRecoverySQL = reviewSQL, recoverySQL
			dbassets.ReaderSubscriptionEvidenceSQL = evidenceSQL
			var version int64
			if err := pool.QueryRow(ctx, "SELECT max(version) FROM reader_schema_migrations").Scan(&version); err != nil || version != baseline.version {
				t.Fatalf("upgrade fixture schema=%d, err=%v", version, err)
			}
			if baseline.version == readerPaymentRecoveryVersion {
				if _, err := pool.Exec(ctx, websiteFailureUpgradeFixture); err != nil {
					t.Fatal(err)
				}
			}
			if err := autoMigrate(ctx, pool); err != nil {
				t.Fatal(err)
			}
			if err := pool.QueryRow(ctx, "SELECT max(version) FROM reader_schema_migrations").Scan(&version); err != nil || version != RequiredSchemaVersion {
				t.Fatalf("upgraded schema=%d, err=%v", version, err)
			}
			if baseline.version == readerPaymentRecoveryVersion {
				var reopened, preserved int
				err := pool.QueryRow(ctx, `SELECT
					count(*) FILTER (WHERE e.source_order_id='19141' AND e.reason='rule_fact_missing'
					    AND d.state='blocked' AND d.reason='rule_fact_missing' AND d.finalized_at IS NULL
					    AND d.deadline_at=e.received_at+interval '24 hours'),
					count(*) FILTER (WHERE e.source_order_id<>'19141' AND d.state='failed' AND d.finalized_at IS NOT NULL)
					FROM conversion.deliveries d JOIN conversion.events e ON e.id=d.event_id WHERE e.environment='upgrade-fixture'`).
					Scan(&reopened, &preserved)
				if err != nil || reopened != 1 || preserved != 4 {
					t.Fatalf("legacy website failures: reopened=%d preserved=%d err=%v", reopened, preserved, err)
				}
			}
		})
	}
}

const websiteFailureUpgradeFixture = `
	INSERT INTO users(id) VALUES(19141);
	INSERT INTO conversion.integrations(source,account_key,environment,contract_version,enabled)
	VALUES('starnovel','reader','upgrade-fixture','v1',true);
	INSERT INTO conversion.destinations(environment,media,pixel_id) VALUES ('upgrade-fixture','meta','1914');
	INSERT INTO recharge_orders(id,user_id,external_ref_id,amount_cents,payment_method,status,order_type,subscription_id,paid_at,fulfilled_at,capi_expectation)
	SELECT id,19141,'capture-'||id,999,'paypal','Success','subscription','I-UPGRADE-'||id,now(),now(),
	    jsonb_build_object('environment','upgrade-fixture','payment_key',id::text)
	FROM generate_series(19141,19145) AS id;
	INSERT INTO user_subscriptions(user_id,subscription_id,capi_subscribe_state,capi_subscribe_order_id)
	VALUES(19141,'I-UPGRADE-19145','claimed',19145);
	INSERT INTO third_party_payment_orders(order_id,payment_provider,external_order_id,capture_id,gross_amount,status)
	VALUES(19145,'paypal','I-UPGRADE-19145','capture-19145',9.99,'VERIFIED');
	INSERT INTO conversion.events(integration_id,environment,source_event_type,source_event_id,event_name,
	    source_order_id,attribution_state,received_at,occurred_at,amount,currency,matching_data,action_source,
	    order_type,time_source,payment_app_scope,payment_policy_state)
	SELECT i.id,i.environment,'Purchase','payment:'||o.id||':Purchase','Purchase',o.id::text,'matched',now(),now(),9.99,'USD',
	    '{"external_id":["fixture"]}','website','subscription','paypal_subscription_ledger','reader','pending'
	FROM recharge_orders o CROSS JOIN conversion.integrations i
	WHERE o.id BETWEEN 19141 AND 19145 AND i.environment='upgrade-fixture';
	INSERT INTO reader_conversion_repair_facts(business_key,event_name,order_id,environment,occurred_at,created_at,snapshot,normalized_at)
	SELECT e.source_event_id,e.event_name,e.source_order_id::bigint,e.environment,e.occurred_at,e.received_at,
	    jsonb_build_object('payment_key',e.source_order_id,'order_type','subscription','payment_policy_version',1,
	        'user_id','19141','payment_kind',CASE WHEN e.source_order_id='19144' THEN 'subscription_initial' END),now()
	FROM conversion.events e WHERE e.environment='upgrade-fixture';
	INSERT INTO conversion.deliveries(event_id,destination_id,environment,meta_event_id,role,deadline_at,state,reason,finalized_at,attempts)
	SELECT e.id,d.id,e.environment,e.source_event_id,'original',
	    CASE WHEN e.source_order_id='19142' THEN now()-interval '1 second' ELSE e.received_at+interval '24 hours' END,
	    'failed','invalid_website_context',now(),CASE WHEN e.source_order_id='19143' THEN 1 ELSE 0 END
	FROM conversion.events e JOIN conversion.destinations d ON d.environment=e.environment
	WHERE e.environment='upgrade-fixture';
`

func TestConversionMigrationUpgradesReaderQueueVersionAtomically(t *testing.T) {
	pool, _ := migrationPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, migration := range []string{
		dbassets.SchemaSQL, dbassets.ChapterPublicationSQL, dbassets.ReaderPerformanceSQL, dbassets.ReaderQueueAndShelfSQL,
	} {
		if _, err := pool.Exec(ctx, migration); err != nil {
			t.Fatal(err)
		}
	}

	original := dbassets.ConversionReportingSQL
	defer func() { dbassets.ConversionReportingSQL = original }()
	dbassets.ConversionReportingSQL += "\nSELECT 1/0;"
	if err := autoMigrate(ctx, pool); err == nil {
		t.Fatal("conversion migration failure ignored")
	}
	var version int64
	var conversionTable *string
	err := pool.QueryRow(ctx, "SELECT max(version), to_regclass('conversion.events')::text FROM reader_schema_migrations").
		Scan(&version, &conversionTable)
	if err != nil || version != readerQueueVersion || conversionTable != nil {
		t.Fatalf("failed upgrade changed schema: version=%d, conversion=%v, err=%v", version, conversionTable, err)
	}

	dbassets.ConversionReportingSQL = original
	if err := autoMigrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	err = pool.QueryRow(ctx, "SELECT max(version), to_regclass('conversion.events')::text FROM reader_schema_migrations").
		Scan(&version, &conversionTable)
	if err != nil || version != RequiredSchemaVersion || conversionTable == nil {
		t.Fatalf("upgrade did not install schema: version=%d, conversion=%v, err=%v", version, conversionTable, err)
	}
}

func TestReaderPaymentPolicyBackfillsFirstFulfillment(t *testing.T) {
	pool, _ := migrationPool(t)
	ctx := context.Background()
	for _, migration := range []string{
		dbassets.SchemaSQL, dbassets.ChapterPublicationSQL, dbassets.ReaderPerformanceSQL, dbassets.ReaderQueueAndShelfSQL,
		dbassets.ConversionReportingSQL, dbassets.ReaderConversionSQL, dbassets.LegacyConversionSQL,
		dbassets.ConversionReconciliationSQL, dbassets.ConversionOperationsSQL, dbassets.ConversionReviewSQL,
		dbassets.SinanConversionSQL, dbassets.SinanReviewSQL, dbassets.SinanRecheckSQL, dbassets.PaymentPolicySQL,
	} {
		if _, err := pool.Exec(ctx, migration); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO novels(id,title,author) VALUES (1,'Book','Author');
		INSERT INTO users(id) VALUES (1);
		INSERT INTO promotion_links(id,novel_id,novel_title,generated_url) VALUES (1,1,'Book','https://example.invalid/1');
		INSERT INTO recharge_orders(id,user_id,promotion_link_id,amount_cents,payment_method,status,paid_at,fulfilled_at)
		VALUES (11,1,1,999,'paypal','Success',now()-interval '3 days',now()-interval '1 day'),
		       (12,1,1,999,'paypal','Refunded',now()-interval '2 days',now()-interval '2 days'),
		       (13,1,1,999,'paypal','Success',now()-interval '2 days',now()-interval '2 days'),
		       (14,1,1,999,'paypal','Refunded',now()-interval '4 days',NULL);
	`); err != nil {
		t.Fatal(err)
	}
	if err := autoMigrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var version, order int64
	var coverage bool
	err := pool.QueryRow(ctx, `
		SELECT (SELECT max(version) FROM reader_schema_migrations),order_id,reader_payment_coverage_verified('test',1)
		FROM reader_payment_first_on_promotion WHERE user_id=1 AND promotion_link_id=1`).Scan(&version, &order, &coverage)
	if err != nil || version != RequiredSchemaVersion || order != 12 || coverage {
		t.Fatalf("historical first payment version=%d order=%d complete=%t: %v", version, order, coverage, err)
	}
	if err := autoMigrate(ctx, pool); err != nil {
		t.Fatalf("repeat migrate: %v", err)
	}
}
