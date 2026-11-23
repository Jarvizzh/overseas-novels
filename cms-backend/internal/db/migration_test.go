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
			reviewFixesSQL, catalogSQL, switchSQL, templateFixesSQL, conversionReviewSQL, batchSQL, rereviewSQL := dbassets.PaymentReviewFixesSQL, dbassets.PaymentCatalogAndChannelSQL, dbassets.PaymentVIPSwitchSQL, dbassets.PaymentTemplateCatalogFixesSQL, dbassets.ConversionCenterReviewSQL, dbassets.ConversionBatchSQL, dbassets.ConversionThroughputRereviewSQL
			paymentSQL, checkoutSQL, vipSQL, ledgerSQL, settlementSQL, decisionSQL, aftercareSQL, stripeCheckoutSQL, stripeVIPSQL, completionSQL, paymentRecoverySQL := dbassets.PaymentDomainFoundationSQL, dbassets.PaymentCheckoutSQL, dbassets.VIPOfferVersionsSQL, dbassets.PaymentLedgerSQL, dbassets.PaymentSettlementSQL, dbassets.PaymentCheckoutDecisionsSQL, dbassets.PaymentAftercareSQL, dbassets.StripeOneTimeCheckoutSQL, dbassets.StripeVIPCheckoutSQL, dbassets.PaymentCompletionSQL, dbassets.PaymentRecoverySQL
			t.Cleanup(func() {
				dbassets.ReaderPaymentReviewSQL, dbassets.ReaderPaymentRecoverySQL = reviewSQL, recoverySQL
				dbassets.ReaderSubscriptionEvidenceSQL = evidenceSQL
				dbassets.PaymentDomainFoundationSQL, dbassets.PaymentCheckoutSQL, dbassets.VIPOfferVersionsSQL, dbassets.PaymentLedgerSQL, dbassets.PaymentSettlementSQL, dbassets.PaymentCheckoutDecisionsSQL, dbassets.PaymentAftercareSQL, dbassets.StripeOneTimeCheckoutSQL, dbassets.StripeVIPCheckoutSQL, dbassets.PaymentCompletionSQL, dbassets.PaymentRecoverySQL = paymentSQL, checkoutSQL, vipSQL, ledgerSQL, settlementSQL, decisionSQL, aftercareSQL, stripeCheckoutSQL, stripeVIPSQL, completionSQL, paymentRecoverySQL
				dbassets.PaymentReviewFixesSQL, dbassets.PaymentCatalogAndChannelSQL, dbassets.PaymentVIPSwitchSQL, dbassets.PaymentTemplateCatalogFixesSQL, dbassets.ConversionCenterReviewSQL, dbassets.ConversionBatchSQL, dbassets.ConversionThroughputRereviewSQL = reviewFixesSQL, catalogSQL, switchSQL, templateFixesSQL, conversionReviewSQL, batchSQL, rereviewSQL
			})
			dbassets.ReaderSubscriptionEvidenceSQL = ""
			dbassets.PaymentDomainFoundationSQL, dbassets.PaymentCheckoutSQL, dbassets.VIPOfferVersionsSQL, dbassets.PaymentLedgerSQL, dbassets.PaymentSettlementSQL, dbassets.PaymentCheckoutDecisionsSQL, dbassets.PaymentAftercareSQL, dbassets.StripeOneTimeCheckoutSQL, dbassets.StripeVIPCheckoutSQL, dbassets.PaymentCompletionSQL, dbassets.PaymentRecoverySQL = "", "", "", "", "", "", "", "", "", "", ""
			dbassets.PaymentReviewFixesSQL, dbassets.PaymentCatalogAndChannelSQL, dbassets.PaymentVIPSwitchSQL, dbassets.PaymentTemplateCatalogFixesSQL, dbassets.ConversionCenterReviewSQL, dbassets.ConversionBatchSQL, dbassets.ConversionThroughputRereviewSQL = "", "", "", "", "", "", ""
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
			dbassets.PaymentDomainFoundationSQL, dbassets.PaymentCheckoutSQL, dbassets.VIPOfferVersionsSQL, dbassets.PaymentLedgerSQL, dbassets.PaymentSettlementSQL, dbassets.PaymentCheckoutDecisionsSQL, dbassets.PaymentAftercareSQL, dbassets.StripeOneTimeCheckoutSQL, dbassets.StripeVIPCheckoutSQL, dbassets.PaymentCompletionSQL, dbassets.PaymentRecoverySQL = paymentSQL, checkoutSQL, vipSQL, ledgerSQL, settlementSQL, decisionSQL, aftercareSQL, stripeCheckoutSQL, stripeVIPSQL, completionSQL, paymentRecoverySQL
			dbassets.PaymentReviewFixesSQL, dbassets.PaymentCatalogAndChannelSQL, dbassets.PaymentVIPSwitchSQL, dbassets.PaymentTemplateCatalogFixesSQL, dbassets.ConversionCenterReviewSQL, dbassets.ConversionBatchSQL, dbassets.ConversionThroughputRereviewSQL = reviewFixesSQL, catalogSQL, switchSQL, templateFixesSQL, conversionReviewSQL, batchSQL, rereviewSQL
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

func TestPaymentDomainFoundationUpgrade(t *testing.T) {
	pool, _ := migrationPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	migration, checkout, vipSQL, ledgerSQL, settlementSQL, decisionSQL, aftercareSQL, stripeCheckoutSQL, stripeVIPSQL, completionSQL, paymentRecoverySQL := dbassets.PaymentDomainFoundationSQL, dbassets.PaymentCheckoutSQL, dbassets.VIPOfferVersionsSQL, dbassets.PaymentLedgerSQL, dbassets.PaymentSettlementSQL, dbassets.PaymentCheckoutDecisionsSQL, dbassets.PaymentAftercareSQL, dbassets.StripeOneTimeCheckoutSQL, dbassets.StripeVIPCheckoutSQL, dbassets.PaymentCompletionSQL, dbassets.PaymentRecoverySQL
	reviewFixesSQL, catalogSQL, switchSQL, templateFixesSQL, conversionReviewSQL, batchSQL, rereviewSQL := dbassets.PaymentReviewFixesSQL, dbassets.PaymentCatalogAndChannelSQL, dbassets.PaymentVIPSwitchSQL, dbassets.PaymentTemplateCatalogFixesSQL, dbassets.ConversionCenterReviewSQL, dbassets.ConversionBatchSQL, dbassets.ConversionThroughputRereviewSQL
	t.Cleanup(func() {
		dbassets.PaymentDomainFoundationSQL, dbassets.PaymentCheckoutSQL, dbassets.VIPOfferVersionsSQL, dbassets.PaymentLedgerSQL, dbassets.PaymentSettlementSQL, dbassets.PaymentCheckoutDecisionsSQL, dbassets.PaymentAftercareSQL, dbassets.StripeOneTimeCheckoutSQL, dbassets.StripeVIPCheckoutSQL, dbassets.PaymentCompletionSQL, dbassets.PaymentRecoverySQL = migration, checkout, vipSQL, ledgerSQL, settlementSQL, decisionSQL, aftercareSQL, stripeCheckoutSQL, stripeVIPSQL, completionSQL, paymentRecoverySQL
		dbassets.PaymentReviewFixesSQL, dbassets.PaymentCatalogAndChannelSQL, dbassets.PaymentVIPSwitchSQL, dbassets.PaymentTemplateCatalogFixesSQL, dbassets.ConversionCenterReviewSQL, dbassets.ConversionBatchSQL, dbassets.ConversionThroughputRereviewSQL = reviewFixesSQL, catalogSQL, switchSQL, templateFixesSQL, conversionReviewSQL, batchSQL, rereviewSQL
	})
	dbassets.PaymentDomainFoundationSQL, dbassets.PaymentCheckoutSQL, dbassets.VIPOfferVersionsSQL, dbassets.PaymentLedgerSQL, dbassets.PaymentSettlementSQL, dbassets.PaymentCheckoutDecisionsSQL, dbassets.PaymentAftercareSQL, dbassets.StripeOneTimeCheckoutSQL, dbassets.StripeVIPCheckoutSQL, dbassets.PaymentCompletionSQL, dbassets.PaymentRecoverySQL = "", "", "", "", "", "", "", "", "", "", ""
	dbassets.PaymentReviewFixesSQL, dbassets.PaymentCatalogAndChannelSQL, dbassets.PaymentVIPSwitchSQL, dbassets.PaymentTemplateCatalogFixesSQL, dbassets.ConversionCenterReviewSQL, dbassets.ConversionBatchSQL, dbassets.ConversionThroughputRereviewSQL = "", "", "", "", "", "", ""
	if err := autoMigrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		SELECT assert_paypal_identity('legacy-paypal-app','production');
		INSERT INTO recharge_templates(id,name) VALUES(91916,'Legacy VIP');
		INSERT INTO recharge_slots(id,template_id,slot_index,type,price,price_cents,subscription_cycle)
		VALUES (91916,91916,1,'vip','9.99',999,'month'),(91917,91916,2,'vip','19.99',1999,'month');
		INSERT INTO payment_provider_plans(provider,slot_id,cycle,price_cents,currency,external_plan_id,identity_client_id,identity_environment)
		VALUES ('paypal',91916,'month',999,'USD','P-ORIGINAL','legacy-paypal-app','live'),
		       ('paypal',91917,'month',999,'USD','P-WRONG-PRICE','legacy-paypal-app','live');
		INSERT INTO paypal_event_inbox (event_id,event_type,amount_cents,currency,event,attempts,next_attempt_at,processed_at,last_error)
		VALUES ('WH-PENDING','ignored',0,'','{"EventID":"WH-PENDING"}',3,'2026-11-20T02:00:00Z',NULL,'receipt pending'),
		       ('WH-DONE','ignored',0,'','{"EventID":"WH-DONE"}',1,now(),'2026-11-20T01:00:00Z',NULL);
		INSERT INTO users(id) VALUES (91916);
		INSERT INTO user_subscriptions(user_id,subscription_id) VALUES (91916,'I-LEGACY');
		INSERT INTO recharge_orders(id,user_id,external_ref_id,subscription_id,amount_cents,currency,payment_method,status,
			order_type,paid_at,fulfilled_at)
		VALUES (91916,91916,'SALE-LEGACY','I-LEGACY',999,'USD','paypal','RefundPending','subscription',now(),now());
			INSERT INTO third_party_payment_orders(order_id,payment_provider,external_order_id,capture_id,gross_amount,fee_amount,net_amount,status)
			VALUES (91916,'paypal','I-LEGACY','SALE-LEGACY',9.99,0,9.99,'VERIFIED');
		INSERT INTO payment_refunds(order_id,request_id,requested_amount_cents)
		VALUES (91916,'39debb1e-3d00-4ffb-934e-58f76ee13d3d',999);
		INSERT INTO reader_conversion_outbox(business_key,event_name,order_id,environment,occurred_at,snapshot)
		VALUES ('payment:91916:Purchase','Purchase',91916,'test',now(),'{}');
	`); err != nil {
		t.Fatal(err)
	}

	dbassets.PaymentDomainFoundationSQL = migration + "\nSELECT 1/0;"
	if err := autoMigrate(ctx, pool); err == nil {
		t.Fatal("payment migration failure ignored")
	}
	var version int64
	var originalInbox bool
	if err := pool.QueryRow(ctx, `SELECT max(version),to_regclass('paypal_event_inbox') IS NOT NULL FROM reader_schema_migrations`).
		Scan(&version, &originalInbox); err != nil || version != readerSubscriptionEvidenceVersion || !originalInbox {
		t.Fatalf("failed upgrade changed the previous schema: %d, inbox=%t, err=%v", version, originalInbox, err)
	}

	dbassets.PaymentDomainFoundationSQL, dbassets.PaymentCheckoutSQL, dbassets.VIPOfferVersionsSQL, dbassets.PaymentLedgerSQL, dbassets.PaymentSettlementSQL, dbassets.PaymentCheckoutDecisionsSQL, dbassets.PaymentAftercareSQL, dbassets.StripeOneTimeCheckoutSQL, dbassets.StripeVIPCheckoutSQL, dbassets.PaymentCompletionSQL, dbassets.PaymentRecoverySQL = migration, checkout, vipSQL, ledgerSQL, settlementSQL, decisionSQL, aftercareSQL, stripeCheckoutSQL, stripeVIPSQL, completionSQL, paymentRecoverySQL
	dbassets.PaymentReviewFixesSQL, dbassets.PaymentCatalogAndChannelSQL, dbassets.PaymentVIPSwitchSQL, dbassets.PaymentTemplateCatalogFixesSQL, dbassets.ConversionCenterReviewSQL, dbassets.ConversionBatchSQL, dbassets.ConversionThroughputRereviewSQL = reviewFixesSQL, catalogSQL, switchSQL, templateFixesSQL, conversionReviewSQL, batchSQL, rereviewSQL
	for range 2 {
		if err := autoMigrate(ctx, pool); err != nil {
			t.Fatal(err)
		}
	}
	var accounts, events, stableReferences int
	err := pool.QueryRow(ctx, `SELECT
		(SELECT max(version) FROM reader_schema_migrations),
		(SELECT count(*) FROM payment_channel_accounts WHERE provider='paypal' AND environment='live'
			AND account_key='legacy-paypal-app' AND credentials_ref='legacy:paypal' AND webhook_ref='env:PAYPAL_WEBHOOK_ID'),
		(SELECT count(*) FROM payment_event_inbox WHERE provider='paypal' AND account_id IS NULL AND
			((event_id='WH-PENDING' AND attempts=3 AND processed_at IS NULL
			  AND next_attempt_at='2026-11-20T02:00:00Z' AND last_error='receipt pending')
			OR (event_id='WH-DONE' AND attempts=1 AND processed_at='2026-11-20T01:00:00Z'))),
		(SELECT count(*) FROM recharge_orders o JOIN user_subscriptions s ON s.subscription_id=o.subscription_id
			JOIN payment_refunds r ON r.order_id=o.id JOIN third_party_payment_orders p ON p.order_id=o.id
			JOIN reader_conversion_outbox b ON b.order_id=o.id
			JOIN payment_refund_commands c ON c.id=r.request_id AND c.receipt_id=p.id
			WHERE o.id=91916 AND o.fulfilled_at IS NOT NULL AND o.status='RefundPending' AND s.subscription_id='I-LEGACY'
			AND p.capture_id='SALE-LEGACY' AND r.request_id='39debb1e-3d00-4ffb-934e-58f76ee13d3d'
			AND p.gross_amount=9.99 AND p.fee_amount IS NULL AND p.net_amount IS NULL
			AND c.legacy_source AND c.first_sent_at IS NULL AND c.amount_minor=999 AND c.status='confirming'
			AND r.requested_amount_cents=999 AND b.business_key='payment:91916:Purchase')`).
		Scan(&version, &accounts, &events, &stableReferences)
	if err != nil || version != RequiredSchemaVersion || accounts != 1 || events != 2 || stableReferences != 1 {
		t.Fatalf("upgraded version=%d accounts=%d preserved events=%d preserved references=%d err=%v",
			version, accounts, events, stableReferences, err)
	}
	var matchingOffers, importedPlans int
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM payment_vip_offer_versions WHERE template_id=91916),
		(SELECT count(*) FROM payment_paypal_vip_plans WHERE slot_id IN (91916,91917) AND plan_id IS NOT NULL)`).
		Scan(&matchingOffers, &importedPlans); err != nil || matchingOffers != 2 || importedPlans != 1 {
		t.Fatalf("VIP offer backfill: versions=%d mapped=%d err=%v", matchingOffers, importedPlans, err)
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

func TestCheckoutDecisionUpgradeFromSettlement(t *testing.T) {
	pool, _ := migrationPool(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	migration, aftercare, stripeCheckoutSQL, stripeVIPSQL, completionSQL, paymentRecoverySQL := dbassets.PaymentCheckoutDecisionsSQL, dbassets.PaymentAftercareSQL, dbassets.StripeOneTimeCheckoutSQL, dbassets.StripeVIPCheckoutSQL, dbassets.PaymentCompletionSQL, dbassets.PaymentRecoverySQL
	reviewFixesSQL, catalogSQL, switchSQL, templateFixesSQL, conversionReviewSQL, batchSQL, rereviewSQL := dbassets.PaymentReviewFixesSQL, dbassets.PaymentCatalogAndChannelSQL, dbassets.PaymentVIPSwitchSQL, dbassets.PaymentTemplateCatalogFixesSQL, dbassets.ConversionCenterReviewSQL, dbassets.ConversionBatchSQL, dbassets.ConversionThroughputRereviewSQL
	t.Cleanup(func() {
		dbassets.PaymentCheckoutDecisionsSQL, dbassets.PaymentAftercareSQL, dbassets.StripeOneTimeCheckoutSQL, dbassets.StripeVIPCheckoutSQL, dbassets.PaymentCompletionSQL, dbassets.PaymentRecoverySQL = migration, aftercare, stripeCheckoutSQL, stripeVIPSQL, completionSQL, paymentRecoverySQL
		dbassets.PaymentReviewFixesSQL, dbassets.PaymentCatalogAndChannelSQL, dbassets.PaymentVIPSwitchSQL, dbassets.PaymentTemplateCatalogFixesSQL, dbassets.ConversionCenterReviewSQL, dbassets.ConversionBatchSQL, dbassets.ConversionThroughputRereviewSQL = reviewFixesSQL, catalogSQL, switchSQL, templateFixesSQL, conversionReviewSQL, batchSQL, rereviewSQL
	})
	dbassets.PaymentCheckoutDecisionsSQL, dbassets.PaymentAftercareSQL, dbassets.StripeOneTimeCheckoutSQL, dbassets.StripeVIPCheckoutSQL, dbassets.PaymentCompletionSQL, dbassets.PaymentRecoverySQL = "", "", "", "", "", ""
	dbassets.PaymentReviewFixesSQL, dbassets.PaymentCatalogAndChannelSQL, dbassets.PaymentVIPSwitchSQL, dbassets.PaymentTemplateCatalogFixesSQL, dbassets.ConversionCenterReviewSQL, dbassets.ConversionBatchSQL, dbassets.ConversionThroughputRereviewSQL = "", "", "", "", "", "", ""
	if err := autoMigrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var previous int64
	if err := pool.QueryRow(ctx, "SELECT max(version) FROM reader_schema_migrations").Scan(&previous); err != nil || previous != paymentSettlementVersion {
		t.Fatalf("baseline version=%d err=%v", previous, err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO users(id) VALUES (1921);
		INSERT INTO recharge_orders(id,user_id,amount_cents,payment_method,status,order_type)
		VALUES (1921,1921,999,'paypal','Pending','single')`); err != nil {
		t.Fatal(err)
	}
	dbassets.PaymentCheckoutDecisionsSQL, dbassets.PaymentAftercareSQL, dbassets.StripeOneTimeCheckoutSQL, dbassets.StripeVIPCheckoutSQL, dbassets.PaymentCompletionSQL, dbassets.PaymentRecoverySQL = migration, aftercare, stripeCheckoutSQL, stripeVIPSQL, completionSQL, paymentRecoverySQL
	dbassets.PaymentReviewFixesSQL, dbassets.PaymentCatalogAndChannelSQL, dbassets.PaymentVIPSwitchSQL, dbassets.PaymentTemplateCatalogFixesSQL, dbassets.ConversionCenterReviewSQL, dbassets.ConversionBatchSQL, dbassets.ConversionThroughputRereviewSQL = reviewFixesSQL, catalogSQL, switchSQL, templateFixesSQL, conversionReviewSQL, batchSQL, rereviewSQL
	if err := autoMigrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := autoMigrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var version int64
	var hasLedger, hasOrder bool
	var decisions int
	err := pool.QueryRow(ctx, `SELECT max(version),to_regclass('payment_settlement_reconciliation') IS NOT NULL,
		EXISTS(SELECT 1 FROM recharge_orders WHERE id=1921 AND amount_cents=999),
		(SELECT count(*) FROM payment_checkout_decisions) FROM reader_schema_migrations`).
		Scan(&version, &hasLedger, &hasOrder, &decisions)
	if err != nil || version != RequiredSchemaVersion || !hasLedger || !hasOrder || decisions != 0 {
		t.Fatalf("version=%d ledger=%v order=%v decisions=%d err=%v", version, hasLedger, hasOrder, decisions, err)
	}
}

func TestPaymentReviewFixesUpgradeFromRecovery(t *testing.T) {
	pool, _ := migrationPool(t)
	ctx := t.Context()
	migration, catalogSQL, switchSQL, templateFixesSQL, conversionReviewSQL, batchSQL, rereviewSQL := dbassets.PaymentReviewFixesSQL, dbassets.PaymentCatalogAndChannelSQL, dbassets.PaymentVIPSwitchSQL, dbassets.PaymentTemplateCatalogFixesSQL, dbassets.ConversionCenterReviewSQL, dbassets.ConversionBatchSQL, dbassets.ConversionThroughputRereviewSQL
	t.Cleanup(func() {
		dbassets.PaymentReviewFixesSQL, dbassets.PaymentCatalogAndChannelSQL, dbassets.PaymentVIPSwitchSQL, dbassets.PaymentTemplateCatalogFixesSQL, dbassets.ConversionCenterReviewSQL, dbassets.ConversionBatchSQL, dbassets.ConversionThroughputRereviewSQL = migration, catalogSQL, switchSQL, templateFixesSQL, conversionReviewSQL, batchSQL, rereviewSQL
	})
	dbassets.PaymentReviewFixesSQL, dbassets.PaymentCatalogAndChannelSQL, dbassets.PaymentVIPSwitchSQL, dbassets.PaymentTemplateCatalogFixesSQL, dbassets.ConversionCenterReviewSQL, dbassets.ConversionBatchSQL, dbassets.ConversionThroughputRereviewSQL = "", "", "", "", "", "", ""
	if err := autoMigrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `SELECT assert_paypal_identity('upgrade-sandbox','sandbox');
		INSERT INTO users(id) VALUES(817);
		INSERT INTO recharge_orders(id,user_id,amount_cents,payment_method,status,checkout_environment)
		VALUES(817,817,999,'paypal','Pending','sandbox');
		INSERT INTO payment_checkout_attempts(id,order_id,provider,account_id,sales_mode,status,provider_order_id,
			return_url,cancel_url,capture_lookup_only)
		SELECT gen_random_uuid(),817,'paypal',id,'direct','buyer_action','upgrade-order',
			'https://example.test/recharge','https://example.test/recharge',true
		FROM payment_channel_accounts WHERE account_key='upgrade-sandbox';
		INSERT INTO user_subscriptions(user_id,subscription_id,plan_id,status,payment_method)
		VALUES(817,'I-UNKNOWN','P-UNKNOWN','ACTIVE','paypal');
		INSERT INTO paypal_subscription_intents(user_id,intent_id,slot_id,template_id,cycle,price_cents,
			return_url,cancel_url,checkout_account_id)
		SELECT 817,gen_random_uuid(),1,1,'month',999,'https://example.test/recharge','https://example.test/recharge',id
		FROM payment_channel_accounts WHERE account_key='upgrade-sandbox'`); err != nil {
		t.Fatal(err)
	}
	dbassets.PaymentReviewFixesSQL, dbassets.PaymentCatalogAndChannelSQL, dbassets.PaymentVIPSwitchSQL, dbassets.PaymentTemplateCatalogFixesSQL, dbassets.ConversionCenterReviewSQL, dbassets.ConversionBatchSQL, dbassets.ConversionThroughputRereviewSQL = migration, catalogSQL, switchSQL, templateFixesSQL, conversionReviewSQL, batchSQL, rereviewSQL
	if err := autoMigrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var version int64
	var recovered, unknown bool
	var environment string
	err := pool.QueryRow(ctx, `SELECT (SELECT max(version) FROM reader_schema_migrations),
		(SELECT paypal_switch_token IS NOT NULL AND NOT capture_lookup_only FROM payment_checkout_attempts WHERE order_id=817),
		(SELECT payment_subscription_environment(s) IS NULL FROM user_subscriptions s WHERE subscription_id='I-UNKNOWN'),
		(SELECT checkout_environment FROM paypal_subscription_intents WHERE user_id=817)`).
		Scan(&version, &recovered, &unknown, &environment)
	if err != nil || version != RequiredSchemaVersion || !recovered || !unknown || environment != "sandbox" {
		t.Fatalf("upgrade version=%d switch=%t unknown=%t env=%s: %v", version, recovered, unknown, environment, err)
	}
}

func TestVIPSwitchUpgradePreservesPendingCheckout(t *testing.T) {
	pool, _ := migrationPool(t)
	ctx := t.Context()
	switchSQL, templateFixesSQL, conversionReviewSQL, batchSQL, rereviewSQL := dbassets.PaymentVIPSwitchSQL, dbassets.PaymentTemplateCatalogFixesSQL, dbassets.ConversionCenterReviewSQL, dbassets.ConversionBatchSQL, dbassets.ConversionThroughputRereviewSQL
	t.Cleanup(func() {
		dbassets.PaymentVIPSwitchSQL, dbassets.PaymentTemplateCatalogFixesSQL, dbassets.ConversionCenterReviewSQL, dbassets.ConversionBatchSQL, dbassets.ConversionThroughputRereviewSQL = switchSQL, templateFixesSQL, conversionReviewSQL, batchSQL, rereviewSQL
	})
	dbassets.PaymentVIPSwitchSQL, dbassets.PaymentTemplateCatalogFixesSQL, dbassets.ConversionCenterReviewSQL, dbassets.ConversionBatchSQL, dbassets.ConversionThroughputRereviewSQL = "", "", "", "", ""
	if err := autoMigrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO users(id) VALUES(1929);
		INSERT INTO products(id,code,product_type,name,price_cents,vip_cycle)
		VALUES(1929,'VIP_SWITCH_FIXTURE','vip','VIP switch fixture',999,'month');
		INSERT INTO payment_vip_checkout_intents(user_id,product_id,price_cents,cycle,checkout_environment,
		    product_snapshot,active_provider,paypal_intent_id,subscription_id)
		VALUES(1929,1929,999,'month','sandbox','{}','paypal','df09db5f-9c72-4a32-a2c3-cd19bfdf4c21','I-UPGRADE')`); err != nil {
		t.Fatal(err)
	}
	dbassets.PaymentVIPSwitchSQL, dbassets.PaymentTemplateCatalogFixesSQL, dbassets.ConversionCenterReviewSQL, dbassets.ConversionBatchSQL, dbassets.ConversionThroughputRereviewSQL = switchSQL, templateFixesSQL, conversionReviewSQL, batchSQL, rereviewSQL
	if err := autoMigrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var version int64
	var kept bool
	if err := pool.QueryRow(ctx, `SELECT (SELECT max(version) FROM reader_schema_migrations),
		status='Pending' AND active_provider='paypal' AND switch_to_provider IS NULL
		AND paypal_intent_id='df09db5f-9c72-4a32-a2c3-cd19bfdf4c21' AND subscription_id='I-UPGRADE'
		FROM payment_vip_checkout_intents WHERE user_id=1929`).Scan(&version, &kept); err != nil || version != RequiredSchemaVersion || !kept {
		t.Fatalf("VIP upgrade version=%d original checkout preserved=%v: %v", version, kept, err)
	}
}

func TestConversionRereviewUpgradeFromBatchLedger(t *testing.T) {
	pool, _ := migrationPool(t)
	ctx := t.Context()
	rereview := dbassets.ConversionThroughputRereviewSQL
	t.Cleanup(func() { dbassets.ConversionThroughputRereviewSQL = rereview })
	dbassets.ConversionThroughputRereviewSQL = ""
	if err := autoMigrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO conversion.integrations(source,account_key,environment,contract_version)
		VALUES ('sinan','rollout','test','v1');
		INSERT INTO conversion.credentials(ciphertext,key_version) VALUES ('fixture','v1'),('fixture2','v2');
		INSERT INTO conversion.destinations(environment,media,pixel_id,credential_id)
		VALUES ('test','meta','101',1),('test','meta','102',2),('test','meta','103',1);
		INSERT INTO conversion.meta_batches(environment,destination_id,lease_until,started_at,sent_count,
		    credential_id,key_version,receipt_at,receipt,finished_at)
		SELECT 'test',id,now(),now(),1,1,'v1',now(),'{"Outcome":"outcome_unknown","HTTPStatus":400,"GraphCode":190}',now()
		FROM conversion.destinations;
		INSERT INTO conversion.meta_batches(environment,destination_id,lease_until,started_at,sent_count,
		    credential_id,key_version,receipt_at,receipt,finished_at)
		SELECT 'test',id,now(),now(),1,1,'v1',now(),'{"Outcome":"accepted","HTTPStatus":200,"EventsReceived":1}',now()
		FROM conversion.destinations WHERE pixel_id='103';
		INSERT INTO conversion.inbox(integration_id,source_event_type,source_event_id,payload_digest,state)
		VALUES (1,'recharge-paid','historic','fixture','consumed');`); err != nil {
		t.Fatal(err)
	}
	dbassets.ConversionThroughputRereviewSQL = rereview
	if err := autoMigrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var version int64
	var blocked string
	var unknown bool
	if err := pool.QueryRow(ctx, `SELECT (SELECT max(version) FROM reader_schema_migrations),
		(SELECT string_agg(pixel_id,',') FROM conversion.destinations WHERE blocked_credential_id=credential_id),
		(SELECT processed_at IS NULL FROM conversion.inbox WHERE source_event_id='historic')`).
		Scan(&version, &blocked, &unknown); err != nil || version != RequiredSchemaVersion || blocked != "101" || !unknown {
		t.Fatalf("upgrade gate=%d blocked=%q historical time unknown=%v: %v", version, blocked, unknown, err)
	}
}
