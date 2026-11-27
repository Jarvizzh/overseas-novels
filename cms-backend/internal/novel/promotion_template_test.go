package novel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	dbassets "star-novel-cms/db"
	"star-novel-cms/internal/db"
)

func openPromoTestPool(t *testing.T) *pgxpool.Pool {
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
	database := "cms_promo_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+database); err != nil {
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
	origDB := db.DB
	db.DB = pool
	t.Cleanup(func() {
		db.DB = origDB
		pool.Close()
	})
	for _, stmt := range []string{
		dbassets.SchemaSQL,
		dbassets.ChapterPublicationSQL, dbassets.ReaderPerformanceSQL, dbassets.ReaderQueueAndShelfSQL,
		dbassets.ConversionReportingSQL, dbassets.ReaderConversionSQL, dbassets.LegacyConversionSQL,
		dbassets.ConversionReconciliationSQL, dbassets.ConversionOperationsSQL, dbassets.ConversionReviewSQL,
		dbassets.SinanConversionSQL, dbassets.SinanReviewSQL, dbassets.SinanRecheckSQL,
		dbassets.PaymentPolicySQL, dbassets.ReaderPaymentPolicySQL, dbassets.SinanPaymentPolicySQL,
		dbassets.ReaderPaymentReviewSQL, dbassets.ReaderPaymentRecoverySQL, dbassets.ReaderSubscriptionEvidenceSQL,
		dbassets.PaymentDomainFoundationSQL, dbassets.PaymentCheckoutSQL, dbassets.VIPOfferVersionsSQL,
		dbassets.PaymentLedgerSQL, dbassets.PaymentSettlementSQL, dbassets.PaymentCheckoutDecisionsSQL,
		dbassets.PaymentAftercareSQL, dbassets.StripeOneTimeCheckoutSQL, dbassets.StripeVIPCheckoutSQL,
		dbassets.PaymentCompletionSQL, dbassets.PaymentRecoverySQL, dbassets.PaymentReviewFixesSQL,
		dbassets.PaymentCatalogAndChannelSQL, dbassets.PaymentVIPSwitchSQL, dbassets.PaymentTemplateCatalogFixesSQL,
		dbassets.ConversionCenterReviewSQL, dbassets.ConversionBatchSQL, dbassets.ConversionThroughputRereviewSQL,
		dbassets.ConversionCredentialHealthSQL, dbassets.RBACPermissionsSQL, dbassets.PromotionEnvironmentSQL, dbassets.StripeTaxLiabilityRelaxSQL, dbassets.StripeLocalCurrencyCustomerSQL, dbassets.StripeRefundDisputeMitigationSQL, dbassets.StripeRadarAndEventRecoverySQL, dbassets.StripeReportScheduleSQL,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	return pool
}

// TC-CMS-10 (Promotion Link check for R-1): CreatePromotionLink & UpdatePromotionLink reject sandbox templates and accept live templates.
func TestPromotionLinkRejectsSandboxTemplate(t *testing.T) {
	pool := openPromoTestPool(t)
	ctx := context.Background()

	var novelID, pixelID, sbxTplID, liveTplID int
	if err := pool.QueryRow(ctx, `INSERT INTO novels(title, author, cover_url, rating, status, publish_status, synopsis, genres) VALUES('Promo Env Book', 'Author', 'oss://covers/promo.png', 4.5, 'Ongoing', 'published', 'Synopsis', ARRAY['Romance']) RETURNING id`).Scan(&novelID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO fb_pixels(name, pixel_id, access_token) VALUES('Pix 1', '1234567890', 'tok_pix') RETURNING id`).Scan(&pixelID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		WITH integ AS (
			INSERT INTO conversion.integrations(source, account_key, environment, enabled, contract_version)
			VALUES('starnovel', 'reader', 'sandbox', true, 'v1')
			ON CONFLICT (environment, source, account_key) DO UPDATE SET enabled = true
			RETURNING id
		),
		dest AS (
			INSERT INTO conversion.destinations(environment, media, pixel_id, display_name, enabled, legacy_fb_pixel_id)
			VALUES('sandbox', 'meta', '1234567890', 'Pix 1', true, $1)
			RETURNING id
		)
		INSERT INTO conversion.integration_destinations(integration_id, destination_id, environment)
		SELECT integ.id, dest.id, 'sandbox' FROM integ, dest
		ON CONFLICT DO NOTHING`, pixelID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO recharge_templates(name, environment, is_default) VALUES('Sbx Template', 'sandbox', false) RETURNING id`).Scan(&sbxTplID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO recharge_templates(name, environment, is_default) VALUES('Live Template', 'live', true) RETURNING id`).Scan(&liveTplID); err != nil {
		t.Fatal(err)
	}

	gin.SetMode(gin.TestMode)
	h := NewHandler(NewNovelService(NewNovelRepository(), nil, fakeCoverStore{}))
	r := gin.New()
	r.POST("/promotion-links", h.CreatePromotionLink)
	r.PUT("/promotion-links/:id", h.UpdatePromotionLink)

	doReq := func(method, path string, body map[string]any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(method, path, strings.NewReader(string(raw)))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	// Create referencing sandbox template -> 409 Conflict
	createSbx := doReq(http.MethodPost, "/promotion-links", map[string]any{
		"name": "Bad Sbx Promo", "novel_id": novelID, "novel_title": "Promo Env Book",
		"chapter_index": 1, "utm_source": "fb", "utm_campaign": "c1",
		"generated_url": "https://reader.example.test/detail?novel_id=" + strconv.Itoa(novelID),
		"fb_pixel_id":   pixelID, "recharge_template_id": sbxTplID,
	})
	if createSbx.Code != http.StatusConflict {
		t.Fatalf("expected 409 when creating promotion link with sandbox template, got %d: %s", createSbx.Code, createSbx.Body.String())
	}

	// Create referencing live template -> 200 OK
	createLive := doReq(http.MethodPost, "/promotion-links", map[string]any{
		"name": "Good Live Promo", "novel_id": novelID, "novel_title": "Promo Env Book",
		"chapter_index": 1, "utm_source": "fb", "utm_campaign": "c2",
		"generated_url": "https://reader.example.test/detail?novel_id=" + strconv.Itoa(novelID),
		"fb_pixel_id":   pixelID, "recharge_template_id": liveTplID,
	})
	if createLive.Code != http.StatusOK {
		t.Fatalf("expected 200 when creating promotion link with live template, got %d: %s", createLive.Code, createLive.Body.String())
	}
	var created struct {
		ID int `json:"id"`
	}
	_ = json.Unmarshal(createLive.Body.Bytes(), &created)

	// Update referencing sandbox template -> 409 Conflict and preserves live template binding.
	updateSbx := doReq(http.MethodPut, "/promotion-links/"+strconv.Itoa(created.ID), map[string]any{
		"name": "Good Live Promo", "chapter_index": 1, "utm_source": "fb", "utm_campaign": "c2",
		"generated_url": "https://reader.example.test/detail?novel_id=" + strconv.Itoa(novelID),
		"fb_pixel_id":   pixelID, "recharge_template_id": sbxTplID,
	})
	if updateSbx.Code != http.StatusConflict {
		t.Fatalf("expected 409 when updating promotion link to sandbox template, got %d: %s", updateSbx.Code, updateSbx.Body.String())
	}
	var boundTpl int
	if err := pool.QueryRow(ctx, `SELECT recharge_template_id FROM promotion_links WHERE id=$1`, created.ID).Scan(&boundTpl); err != nil || boundTpl != liveTplID {
		t.Fatalf("expected promotion link template to remain %d, got %d err=%v", liveTplID, boundTpl, err)
	}

	// Concurrent live->sandbox demotion holding FOR UPDATE serializes against FOR KEY SHARE in Create/UpdatePromotionLink.
	var raceTplID int
	if err := pool.QueryRow(ctx, `INSERT INTO recharge_templates(name, environment, is_default) VALUES('Race Live Template', 'live', false) RETURNING id`).Scan(&raceTplID); err != nil {
		t.Fatal(err)
	}
	for _, action := range []struct {
		name   string
		method string
		path   string
		body   map[string]any
	}{
		{
			name:   "create",
			method: http.MethodPost,
			path:   "/promotion-links",
			body: map[string]any{
				"name": "Concurrent Create", "novel_id": novelID, "novel_title": "Promo Env Book",
				"chapter_index": 1, "utm_source": "fb", "utm_campaign": "c3",
				"generated_url": "https://reader.example.test/detail?novel_id=" + strconv.Itoa(novelID),
				"fb_pixel_id":   pixelID, "recharge_template_id": raceTplID,
			},
		},
		{
			name:   "update",
			method: http.MethodPut,
			path:   "/promotion-links/" + strconv.Itoa(created.ID),
			body: map[string]any{
				"name": "Concurrent Update", "chapter_index": 1, "utm_source": "fb", "utm_campaign": "c2",
				"generated_url": "https://reader.example.test/detail?novel_id=" + strconv.Itoa(novelID),
				"fb_pixel_id":   pixelID, "recharge_template_id": raceTplID,
			},
		},
	} {
		if _, err := pool.Exec(ctx, `UPDATE recharge_templates SET environment='live' WHERE id=$1`, raceTplID); err != nil {
			t.Fatal(err)
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var env string
		if err := tx.QueryRow(ctx, `SELECT environment FROM recharge_templates WHERE id=$1 FOR UPDATE`, raceTplID).Scan(&env); err != nil || env != "live" {
			tx.Rollback(ctx)
			t.Fatalf("%s: lock template failed env=%q err=%v", action.name, env, err)
		}

		resCh := make(chan *httptest.ResponseRecorder, 1)
		go func(method, path string, body map[string]any) {
			resCh <- doReq(method, path, body)
		}(action.method, action.path, action.body)

		// Confirm the handler blocks on FOR KEY SHARE until tx commits the demotion to sandbox.
		select {
		case early := <-resCh:
			tx.Rollback(ctx)
			t.Fatalf("%s: expected handler to wait on FOR KEY SHARE lock, returned early (%d): %s", action.name, early.Code, early.Body.String())
		case <-time.After(80 * time.Millisecond):
		}

		if _, err := tx.Exec(ctx, `UPDATE recharge_templates SET environment='sandbox' WHERE id=$1`, raceTplID); err != nil {
			tx.Rollback(ctx)
			t.Fatal(err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}

		rec := <-resCh
		if rec.Code != http.StatusConflict {
			t.Fatalf("%s: expected 409 Conflict after concurrent demotion to sandbox, got %d: %s", action.name, rec.Code, rec.Body.String())
		}
	}

	var countRace int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM promotion_links WHERE recharge_template_id=$1`, raceTplID).Scan(&countRace); err != nil || countRace != 0 {
		t.Fatalf("expected zero promotion links referencing demoted template %d, got %d err=%v", raceTplID, countRace, err)
	}

	// Sandbox promotion link accepts sandbox template and rejects live template.
	createSbxPromoOK := doReq(http.MethodPost, "/promotion-links", map[string]any{
		"name": "Good Sbx Promo", "environment": "sandbox", "novel_id": novelID, "novel_title": "Promo Env Book",
		"chapter_index": 1, "utm_source": "fb", "utm_campaign": "sbx_c1",
		"generated_url": "https://reader.example.test/detail?novel_id=" + strconv.Itoa(novelID),
		"fb_pixel_id":   pixelID, "recharge_template_id": sbxTplID,
	})
	if createSbxPromoOK.Code != http.StatusOK {
		t.Fatalf("expected 200 when creating sandbox promotion link with sandbox template, got %d: %s", createSbxPromoOK.Code, createSbxPromoOK.Body.String())
	}
	var createdSbx struct {
		ID          int    `json:"id"`
		Environment string `json:"environment"`
	}
	if err := json.Unmarshal(createSbxPromoOK.Body.Bytes(), &createdSbx); err != nil || createdSbx.ID <= 0 || createdSbx.Environment != "sandbox" {
		t.Fatalf("unexpected sandbox promo create response: %+v err=%v", createdSbx, err)
	}

	// Updating a sandbox promotion link while omitting "environment" must preserve "sandbox" (never promote to "live").
	updateSbxOmitEnvNullTpl := doReq(http.MethodPut, "/promotion-links/"+strconv.Itoa(createdSbx.ID), map[string]any{
		"name": "Good Sbx Promo Edited", "chapter_index": 1, "utm_source": "fb", "utm_campaign": "sbx_c1",
		"generated_url": "https://reader.example.test/detail?novel_id=" + strconv.Itoa(novelID),
		"fb_pixel_id":   pixelID, "recharge_template_id": nil,
	})
	if updateSbxOmitEnvNullTpl.Code != http.StatusOK {
		t.Fatalf("expected 200 updating sandbox promo without environment field, got %d: %s", updateSbxOmitEnvNullTpl.Code, updateSbxOmitEnvNullTpl.Body.String())
	}
	var persistedEnv string
	if err := pool.QueryRow(ctx, `SELECT environment FROM promotion_links WHERE id = $1`, createdSbx.ID).Scan(&persistedEnv); err != nil || persistedEnv != "sandbox" {
		t.Fatalf("expected sandbox promo %d to keep environment=sandbox when omitted on PUT, got %q err=%v", createdSbx.ID, persistedEnv, err)
	}
	// Still rejects live template when environment is omitted on a sandbox promotion link.
	updateSbxOmitEnvLiveTpl := doReq(http.MethodPut, "/promotion-links/"+strconv.Itoa(createdSbx.ID), map[string]any{
		"name": "Good Sbx Promo Bad Live Tpl", "chapter_index": 1, "utm_source": "fb", "utm_campaign": "sbx_c1",
		"generated_url": "https://reader.example.test/detail?novel_id=" + strconv.Itoa(novelID),
		"fb_pixel_id":   pixelID, "recharge_template_id": liveTplID,
	})
	if updateSbxOmitEnvLiveTpl.Code != http.StatusConflict {
		t.Fatalf("expected 409 attaching live template to sandbox promo when environment omitted, got %d: %s", updateSbxOmitEnvLiveTpl.Code, updateSbxOmitEnvLiveTpl.Body.String())
	}

	createSbxPromoBadTpl := doReq(http.MethodPost, "/promotion-links", map[string]any{
		"name": "Bad Sbx Promo With Live Tpl", "environment": "sandbox", "novel_id": novelID, "novel_title": "Promo Env Book",
		"chapter_index": 1, "utm_source": "fb", "utm_campaign": "sbx_c2",
		"generated_url": "https://reader.example.test/detail?novel_id=" + strconv.Itoa(novelID),
		"fb_pixel_id":   pixelID, "recharge_template_id": liveTplID,
	})
	if createSbxPromoBadTpl.Code != http.StatusConflict {
		t.Fatalf("expected 409 when creating sandbox promotion link with live template, got %d: %s", createSbxPromoBadTpl.Code, createSbxPromoBadTpl.Body.String())
	}
}
