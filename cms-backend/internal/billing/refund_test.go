package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"star-novel-cms/internal/db"
)

type paymentTestTransport func(*http.Request) (*http.Response, error)

func (f paymentTestTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestPaymentCenterRefundAuthorizationIntegration(t *testing.T) {
	dsn := os.Getenv("STAR_NOVEL_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires a disposable PostgreSQL database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openTestBillingPool(t, dsn)
	origDB := db.DB
	db.DB = pool
	t.Cleanup(func() { db.DB = origDB })
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO admins(id,username,password_hash,role,status)
		VALUES('test-editor','test-editor','none','Editor',1),
		('test-disabled','test-disabled','none','Finance',2),
		('test-finance','test-finance','none','Finance',1)`)
	exec(`INSERT INTO users(id) VALUES(501)`)
	t.Run("Stripe market approval scopes access and evidence", func(t *testing.T) {
		const orderID = 519
		key := strings.Repeat("a", 64)
		exec(`INSERT INTO recharge_orders(id,user_id,amount_cents,payment_method,status,order_type,reconciliation_note)
			VALUES($1,501,999,'stripe','Paid','whole_book','Stripe billing country changed and needs verification')`, orderID)
		exec(`INSERT INTO payment_stripe_market_reviews(order_id,evidence_key,evidence) VALUES($1,$2,'{"country":"CA"}')`, orderID, key)
		if err := approveStripeMarketEvidence(ctx, orderID, "test-editor", key, "source verified"); !errors.Is(err, errCaseForbidden) {
			t.Fatalf("editor approved Stripe review: %v", err)
		}
		if err := approveStripeMarketEvidence(ctx, orderID, "test-finance", strings.Repeat("b", 64), "source verified"); !errors.Is(err, errCaseConflict) {
			t.Fatalf("unrelated evidence accepted: %v", err)
		}
		if err := approveStripeMarketEvidence(ctx, orderID, "test-finance", key, "source verified"); err != nil {
			t.Fatal(err)
		}
		var audits int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM admin_audit_logs WHERE action='approve_stripe_market_evidence' AND target_id='519'`).Scan(&audits); err != nil || audits != 1 {
			t.Fatalf("market approval audits=%d error=%v", audits, err)
		}
	})
	exec(`INSERT INTO recharge_orders(id,user_id,external_ref_id,amount_cents,payment_method,status,order_type)
		VALUES(5501,501,'refund-test-order',999,'paypal','Success','single')`)
	exec(`INSERT INTO third_party_payment_orders(order_id,payment_provider,capture_id,currency,gross_amount,status)
		VALUES(5501,'paypal','refund-test-capture','USD',9.99,'VERIFIED')`)
	s := &billingService{repo: NewBillingRepository()}
	for _, actor := range []string{"", "test-editor", "test-disabled", "test-missing"} {
		if _, err := submitRefundCommand(ctx, actor, refundCommandInput{}, 5501); !errors.Is(err, ErrRefundForbidden) {
			t.Fatalf("actor %q: %v", actor, err)
		}
	}
	finance := "test-finance"
	t.Run("template writes require live financial permission and atomic audit", func(t *testing.T) {
		exec(`
			INSERT INTO payment_channel_accounts(provider,account_key,environment,credentials_ref,webhook_ref,status,is_primary,new_orders_enabled,new_subscriptions_enabled)
			VALUES('paypal','tpl-pp-live','live','env:pp','env:pp','published',true,true,true),
			      ('stripe','acct_TplLive','live','env:st','env:st','published',true,true,true);
			INSERT INTO products(id,code,product_type,name,price_cents,currency,coins,bonus,vip_cycle,novel_scope_type)
			OVERRIDING SYSTEM VALUE VALUES
			(9001,'TEST_COIN_999','single','Test Coin 999',999,'USD',100,0,NULL,'none'),
			(9002,'TEST_COIN_1999','single','Test Coin 1999',1999,'USD',200,0,NULL,'none'),
			(9003,'TEST_VIP_MONTH_999','vip','Test VIP Month',999,'USD',0,0,'month','none'),
			(9004,'TEST_BOOK_999','whole_book','Test Whole Book',999,'USD',0,0,NULL,'all_published'),
			(9005,'TEST_COIN_1299','single','Test Coin 1299',1299,'USD',150,0,NULL,'none');
			INSERT INTO product_publications(product_id,environment,status,published_at)
			VALUES(9001,'live','published',now()),(9002,'live','published',now()),
			      (9003,'live','published',now()),(9004,'live','published',now()),
			      (9005,'live','published',now());
			INSERT INTO product_channel_bindings(product_id,account_id,provider,environment,external_product_ref,external_price_ref,validated_catalog,release_evidence,market_policy,validation_status,enabled)
			SELECT p.id, a.id, 'paypal', 'live', CASE WHEN p.product_type='vip' THEN 'PROD-PP' ELSE '' END, CASE WHEN p.product_type='vip' THEN 'P-TEST-999' ELSE '' END, '{}'::jsonb, 'ev', 'US', 'verified', true
			FROM products p, payment_channel_accounts a WHERE p.id BETWEEN 9001 AND 9005 AND a.provider='paypal' AND a.account_key='tpl-pp-live';
			INSERT INTO product_channel_bindings(product_id,account_id,provider,environment,external_product_ref,external_price_ref,validated_catalog,release_evidence,market_policy,validation_status,enabled)
			SELECT p.id, a.id, 'stripe', 'live', 'prod_live_'||p.id, 'price_live_'||p.id, '{}'::jsonb, 'ev', 'US', 'verified', true
			FROM products p, payment_channel_accounts a WHERE p.id BETWEEN 9001 AND 9005 AND a.provider='stripe' AND a.account_key='acct_TplLive';`)
		slots := []RechargeSlot{
			{SlotIndex: 1, ProductID: 9001, DisplayAlias: "Starter"},
			{SlotIndex: 2, ProductID: 9002},
			{SlotIndex: 3, ProductID: 9003, Badge: "POPULAR"},
			{SlotIndex: 4, ProductID: 9004},
		}
		for _, actor := range []string{"", "test-editor", "test-disabled", "test-missing"} {
			if _, err := s.CreateRechargeTemplate(ctx, actor, "unauthorized", "live", false, slots); !errors.Is(err, ErrRefundForbidden) {
				t.Fatalf("create %s: %v", actor, err)
			}
			if err := s.UpdateRechargeTemplate(ctx, actor, 999999, "unauthorized", "live", false, slots); !errors.Is(err, ErrRefundForbidden) {
				t.Fatalf("update %s: %v", actor, err)
			}
			if err := s.DeleteRechargeTemplate(ctx, actor, 999999); !errors.Is(err, ErrRefundForbidden) {
				t.Fatalf("delete %s: %v", actor, err)
			}
			if err := s.SetDefaultRechargeTemplate(ctx, actor, 999999); !errors.Is(err, ErrRefundForbidden) {
				t.Fatalf("default %s: %v", actor, err)
			}
		}
		id, err := s.CreateRechargeTemplate(ctx, finance, "audited", "live", false, slots)
		if err != nil {
			t.Fatal(err)
		}
		slots[0].ProductID = 9005
		slots[0].Badge = "HOT"
		slots[0].IsHighlighted = true
		if err := s.UpdateRechargeTemplate(ctx, finance, id, "audited edit", "live", false, slots); err != nil {
			t.Fatal(err)
		}
		var beforeProd, afterProd int64
		var afterBadge string
		if err := pool.QueryRow(ctx, `SELECT (before_data->'slots'->0->>'product_id')::bigint,(after_data->'slots'->0->>'product_id')::bigint,
			COALESCE(after_data->'slots'->0->>'badge','')
   FROM admin_audit_logs WHERE action='update_recharge_template' AND target_id=$1`, fmt.Sprint(id)).Scan(&beforeProd, &afterProd, &afterBadge); err != nil || beforeProd != 9001 || afterProd != 9005 || afterBadge != "HOT" {
			t.Fatalf("slot product audit: %d -> %d badge=%q %v", beforeProd, afterProd, afterBadge, err)
		}
		if err := s.SetDefaultRechargeTemplate(ctx, finance, id); err != nil {
			t.Fatal(err)
		}
		if err := s.DeleteRechargeTemplate(ctx, finance, id); !errors.Is(err, ErrDefaultTemplateNotDelete) {
			t.Fatalf("deleted default: %v", err)
		}
		// A disabled role invalidates a previously accepted financial account.
		exec(`UPDATE admins SET status=2 WHERE id='test-finance'`)
		if err := s.SetDefaultRechargeTemplate(ctx, finance, id); !errors.Is(err, ErrRefundForbidden) {
			t.Fatalf("stale finance session: %v", err)
		}
		exec(`UPDATE admins SET status=1 WHERE id='test-finance'`)
		another, err := s.CreateRechargeTemplate(ctx, finance, "to delete", "live", false, slots)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.DeleteRechargeTemplate(ctx, finance, another); err != nil {
			t.Fatal(err)
		}
		var audits int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM admin_audit_logs WHERE admin_id='test-finance' AND action IN('create_recharge_template','update_recharge_template','set_default_recharge_template','delete_recharge_template')`).Scan(&audits); err != nil || audits != 5 {
			t.Fatalf("audit count: %d %v", audits, err)
		}
	})
	t.Run("PayPal Sale financial details require matching ledger evidence", func(t *testing.T) {
		exec(`SELECT assert_paypal_identity('refund-command-fixture','sandbox') WHERE NOT EXISTS (SELECT 1 FROM paypal_identity_lock);
			INSERT INTO recharge_orders(id,user_id,amount_cents,payment_method,status,order_type)
			VALUES(95503,501,999,'paypal','Success','subscription');
			INSERT INTO third_party_payment_orders(id,order_id,payment_provider,external_order_id,capture_id,
				currency,gross_amount,status,account_id,object_type)
			SELECT 95503,95503,'paypal','I-SALE','SALE-95503','USD',9.99,'VERIFIED',id,'sale'
			FROM payment_channel_accounts WHERE provider='paypal' AND account_key='refund-command-fixture'`)
		detail, err := s.repo.GetThirdPartyPaymentDetails(ctx, 95503)
		if err != nil || detail == nil || detail.GrossAmount != 9.99 || detail.NetAmount != nil || detail.FeeAmount != nil {
			t.Fatalf("gross alone became net: %+v %v", detail, err)
		}
		exec(`WITH movement AS (
				INSERT INTO payment_ledger_movements(account_id,movement_key,kind,currency,posted_at)
				SELECT id,'SALE-95503/T0002/USD','payment','USD',now() FROM payment_channel_accounts
				WHERE provider='paypal' AND account_key='refund-command-fixture' RETURNING id
			), link AS (
				INSERT INTO payment_ledger_references(movement_id,object_type,object_id)
				SELECT id,'paypal_transaction','SALE-95503' FROM movement
			), evidence AS (
				INSERT INTO payment_ledger_evidence(movement_id,source,source_ref,payload_hash,payload)
				SELECT id,'paypal_transaction_search','test-row',sha256('test'::bytea),'{}' FROM movement RETURNING id,movement_id
			)
			INSERT INTO payment_ledger_amounts(movement_id,evidence_id,component,currency,amount_minor)
			SELECT movement_id,id,amounts.component,'USD',amounts.amount
			FROM evidence CROSS JOIN (VALUES('gross',999),('fee',-38),('net',961)) amounts(component,amount)`)
		detail, err = s.repo.GetThirdPartyPaymentDetails(ctx, 95503)
		if err != nil || detail == nil || detail.NetAmount == nil || *detail.NetAmount != 9.61 ||
			detail.FeeAmount == nil || *detail.FeeAmount != 0.38 {
			t.Fatalf("exactly matched PayPal Sale lacks net evidence: %+v %v", detail, err)
		}
	})
	t.Run("refund command replays one reservation and accepts a distinct equal refund", func(t *testing.T) {
		exec(`SELECT assert_paypal_identity('refund-command-fixture','sandbox') WHERE NOT EXISTS (SELECT 1 FROM paypal_identity_lock);
			INSERT INTO recharge_orders(id,user_id,external_ref_id,amount_cents,payment_method,status,order_type)
			VALUES (95502,501,'new-refund-order',2000,'paypal','Success','single');
			INSERT INTO third_party_payment_orders(id,order_id,payment_provider,capture_id,currency,gross_amount,status)
			VALUES (95502,95502,'paypal','command-capture','USD',20.00,'VERIFIED')`)
		req := refundCommandInput{ReceiptID: 95502, AmountMinor: 500, Currency: "USD", RequestKey: "77a08a65-5835-4b31-818d-72e47702b943"}
		first, err := submitRefundCommand(ctx, "test-finance", req, 0)
		if err != nil || first.Status != "pending" {
			t.Fatalf("first command: %+v %v", first, err)
		}
		replay, err := submitRefundCommand(ctx, "test-finance", req, 0)
		if err != nil || replay.ID != first.ID {
			t.Fatalf("same request: %+v %v", replay, err)
		}
		found, err := findRefundCommand(ctx, "", "test-finance", req.RequestKey)
		if err != nil || found.ID != first.ID {
			t.Fatalf("lost response lookup: %+v %v", found, err)
		}
		if _, err := findRefundCommand(ctx, "", "test-editor", req.RequestKey); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("request key was visible to a different actor: %v", err)
		}
		conflict := req
		conflict.AmountMinor = 501
		if _, err := submitRefundCommand(ctx, "test-finance", conflict, 0); !errors.Is(err, ErrRefundKeyConflict) {
			t.Fatalf("parameter conflict: %v", err)
		}
		req.RequestKey = "784cf48a-2737-47a3-ae07-ff397ffce2fd"
		second, err := submitRefundCommand(ctx, "test-finance", req, 0)
		if err != nil || second.ID == first.ID {
			t.Fatalf("distinct refund: %+v %v", second, err)
		}
		var reserved, audit int
		err = pool.QueryRow(ctx, `SELECT (SELECT sum(amount_minor) FROM payment_refund_commands WHERE receipt_id=95502),
			(SELECT count(*) FROM admin_audit_logs WHERE action='submit_payment_refund')`).Scan(&reserved, &audit)
		if err != nil || reserved != 1000 || audit != 2 {
			t.Fatalf("reserved=%d audit=%d err=%v", reserved, audit, err)
		}
	})
	t.Run("PayPal refunds and VIP offers retain the configured account scope", func(t *testing.T) {
		exec(`INSERT INTO payment_channel_accounts(provider,account_key,environment,credentials_ref,webhook_ref)
			VALUES('paypal','merchant-live','live','env:paypal','env:paypal');
			INSERT INTO recharge_orders(id,user_id,amount_cents,payment_method,status,order_type,checkout_environment)
			VALUES(95508,501,999,'paypal','Success','single','production'),
				(95509,501,999,'paypal','Success','single','sandbox');
			INSERT INTO third_party_payment_orders(id,order_id,payment_provider,capture_id,currency,gross_amount,status,account_id,object_type)
			SELECT o.id,o.id,'paypal','scoped-paypal-'||o.id,'USD',9.99,'VERIFIED',a.id,'capture'
			FROM recharge_orders o CROSS JOIN payment_channel_accounts a
			WHERE o.id IN (95508,95509) AND a.provider='paypal' AND a.account_key='merchant-live'`)
		req := refundCommandInput{ReceiptID: 95508, AmountMinor: 100, Currency: "USD", RequestKey: "c93b2569-0af8-4ca9-aa29-db62f9b19740"}
		cmd, err := submitRefundCommand(ctx, finance, req, 0)
		if err != nil || cmd.Status != "pending" {
			t.Fatalf("scoped refund: %+v %v", cmd, err)
		}
		var environment string
		if err := pool.QueryRow(ctx, `SELECT environment FROM payment_refund_commands WHERE id=$1`, cmd.ID).Scan(&environment); err != nil || environment != "live" {
			t.Fatalf("refund environment=%s error=%v", environment, err)
		}
		req.ReceiptID = 95509
		if _, err := submitRefundCommand(ctx, finance, req, 0); !errors.Is(err, ErrOrderNotRefundable) {
			t.Fatalf("mismatched PayPal receipt became refundable: %v", err)
		}
		exec(`INSERT INTO recharge_templates(id,name) VALUES(955,'PayPal VIP Fixture');
			INSERT INTO recharge_slots(id,template_id,slot_index,product_id,type,price,price_cents,coins,bonus,subscription_cycle)
			VALUES(955,955,1,(SELECT id FROM products WHERE code='VIP_MONTH_1499'),'vip','9.99',999,0,0,'month');
			UPDATE recharge_slots s SET vip_offer_version_id=snapshot_vip_offer(s) WHERE s.id=955;`)
		offers, err := s.repo.ListVIPOfferPublications(ctx)
		if err != nil {
			t.Fatal(err)
		}
		environments := map[int]map[string]bool{}
		for _, offer := range offers {
			if offer.AccountID == 0 || offer.AccountLabel == "merchant-live" {
				t.Fatalf("invalid PayPal offer account projection: %+v", offer)
			}
			if environments[offer.VersionID] == nil {
				environments[offer.VersionID] = make(map[string]bool)
			}
			environments[offer.VersionID][offer.Environment] = true
		}
		if len(environments) == 0 {
			t.Fatal("no PayPal VIP offer versions were listed")
		}
		for version, scopes := range environments {
			if !scopes["live"] || !scopes["sandbox"] {
				t.Fatalf("offer version %d missing account: %v", version, scopes)
			}
		}
	})
	t.Run("CMS payment reads enforce role and field projection", func(t *testing.T) {
		exec(`INSERT INTO admins(id,username,password_hash,role,status) VALUES ('test-support','test-support','none','Support',1);
			INSERT INTO admins(id,username,password_hash,role,status) VALUES ('test-super','test-super','none','SuperAdmin',1);
			INSERT INTO admins(id,username,password_hash,role,status) VALUES ('test-admin','test-admin','none','Admin',1);
			UPDATE third_party_payment_orders SET raw_payload='{"internal":"must-stay-server-side"}' WHERE id=95502`)
		handler := NewHandler(s)
		router := gin.New()
		router.Use(func(c *gin.Context) { c.Set("admin_id", c.GetHeader("X-Test-Actor")); c.Next() })
		router.GET("/orders", paymentRoles("SuperAdmin", "Finance", "Admin", "Support"), handler.ListOrders)
		router.GET("/orders/:id/payment-details", paymentRoles("SuperAdmin", "Finance"), handler.GetThirdPartyPaymentDetails)
		router.GET("/orders/:id/checkout-evidence", paymentRoles("SuperAdmin", "Finance"), handler.getCheckoutEvidence)
		router.GET("/payments/channels/vip-offers", paymentRoles("SuperAdmin"), handler.ListVIPOfferPublications)
		check := func(actor, path string, want int) string {
			t.Helper()
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("X-Test-Actor", actor)
			out := httptest.NewRecorder()
			router.ServeHTTP(out, req)
			if out.Code != want {
				t.Fatalf("%s %s returned %d: %s", actor, path, out.Code, out.Body.String())
			}
			return out.Body.String()
		}
		check("test-editor", "/orders", http.StatusForbidden)
		check("test-disabled", "/orders", http.StatusForbidden)
		check("test-support", "/orders", http.StatusForbidden)
		summary := check("test-support", "/orders?order_id=95502", http.StatusOK)
		if !strings.Contains(summary, `"refund_status":"pending"`) || strings.Contains(summary, `"amount_cents"`) || strings.Contains(summary, `"external_ref_id"`) {
			t.Fatalf("support projection: %s", summary)
		}
		check("test-support", "/orders/95502/payment-details", http.StatusForbidden)
		check("test-support", "/orders/95502/checkout-evidence", http.StatusForbidden)
		check("test-admin", "/orders/95502/checkout-evidence", http.StatusForbidden)
		check("test-finance", "/payments/channels/vip-offers", http.StatusForbidden)
		check("test-support", "/payments/channels/vip-offers", http.StatusForbidden)
		check("test-super", "/payments/channels/vip-offers", http.StatusOK)
		detail := check("test-finance", "/orders/95502/payment-details", http.StatusOK)
		if strings.Contains(detail, "must-stay-server-side") || strings.Contains(detail, "raw_payload") {
			t.Fatalf("raw financial payload escaped: %s", detail)
		}
		evidence := check("test-finance", "/orders/95502/checkout-evidence", http.StatusOK)
		if !strings.Contains(evidence, `"funds_entitlement"`) || strings.Contains(evidence, "must-stay-server-side") {
			t.Fatalf("unexpected checkout evidence projection: %s", evidence)
		}
	})
	t.Run("Stripe mappings and invoice evidence enforce payment roles", func(t *testing.T) {
		exec(`INSERT INTO payment_channel_accounts(id,provider,account_key,environment,credentials_ref,webhook_ref) OVERRIDING SYSTEM VALUE
			VALUES(777,'stripe','acct_Stripe','sandbox','env:stripe','env:stripe');
			INSERT INTO recharge_templates(id,name) VALUES(777,'Stripe catalog');
			INSERT INTO recharge_slots(id,template_id,slot_index,product_id,type,price,price_cents,coins,bonus,subscription_cycle)
			VALUES(777,777,1,(SELECT id FROM products WHERE code='VIP_MONTH_1499'),'vip','9.99',999,0,0,'month'),
			      (778,777,2,(SELECT id FROM products WHERE code='WHOLE_BOOK_999'),'whole_book','9.99',999,0,0,NULL);
			UPDATE recharge_slots s SET vip_offer_version_id=snapshot_vip_offer(s) WHERE s.id=777`)
		handler := NewHandler(s)
		router := gin.New()
		router.Use(func(c *gin.Context) { c.Set("admin_id", c.GetHeader("X-Test-Actor")); c.Next() })
		superOnly := paymentRoles("SuperAdmin")
		router.GET("/channels/:id/stripe-vip", superOnly, handler.listStripeVIPOffers)
		router.PUT("/channels/:id/stripe-vip/:slot", superOnly, handler.updateStripeVIPOffer)
		router.GET("/channels/:id/stripe-one-time", superOnly, handler.listStripeOneTimeOffers)
		router.PUT("/channels/:id/stripe-one-time/:slot", superOnly, handler.updateStripeOneTimeOffer)
		router.GET("/subscriptions/:id/stripe-periods", paymentRoles("Finance", "SuperAdmin"), handler.listStripeVIPPeriods)
		query := func(method, path, actor, body string, want int) string {
			t.Helper()
			req := httptest.NewRequest(method, path, strings.NewReader(body))
			req.Header.Set("X-Test-Actor", actor)
			req.Header.Set("Content-Type", "application/json")
			out := httptest.NewRecorder()
			router.ServeHTTP(out, req)
			if out.Code != want {
				t.Fatalf("%s %s: %d %s", method, path, out.Code, out.Body.String())
			}
			return out.Body.String()
		}
		vipPath := "/channels/777/stripe-vip"
		oneTimePath := "/channels/777/stripe-one-time"
		body := `{"product_id":"prod_VIP","price_id":"price_VIP","sales_mode":"merchant_of_record","tax_liability_account":"acct_Liability",
			"countries":["US","US"],"enabled":true,"reason":"Test eligibility"}`
		query(http.MethodPut, vipPath+"/777", "test-finance", body, http.StatusForbidden)
		query(http.MethodPut, vipPath+"/777", "test-super", strings.Replace(body, "acct_Liability", "", 1), http.StatusBadRequest)
		query(http.MethodPut, vipPath+"/777", "test-super", body, http.StatusOK)
		got := query(http.MethodGet, vipPath, "test-super", "", http.StatusOK)
		if !strings.Contains(got, `"countries":["US"]`) || !strings.Contains(got, "price_VIP") {
			t.Fatalf("Stripe VIP mapping: %s", got)
		}
		query(http.MethodPut, oneTimePath+"/778", "test-super", `{"product_id":"prod_Book","price_id":"price_Book","countries":["US"],"enabled":true,"reason":"Direct test"}`, http.StatusOK)
		got = query(http.MethodGet, oneTimePath, "test-super", "", http.StatusOK)
		t.Run("live Stripe catalog requires matching account and release evidence", func(t *testing.T) {
			exec(`INSERT INTO payment_channel_accounts(id,provider,account_key,environment,credentials_ref,webhook_ref) OVERRIDING SYSTEM VALUE
				VALUES(779,'stripe','acct_Release','live','env:stripe','env:stripe')`)
			const key = "rk_live_catalog_fixture"
			t.Setenv("STRIPE_CATALOG_LIVE_API_KEY", key)
			previous := stripeCatalogHTTP
			t.Cleanup(func() { stripeCatalogHTTP = previous })
			merchant := "acct_Wrong"
			stripeCatalogHTTP = &http.Client{Transport: paymentTestTransport(func(req *http.Request) (*http.Response, error) {
				if req.Method != http.MethodGet || req.URL.Host != "api.stripe.com" ||
					req.Header.Get("Authorization") != "Bearer "+key || req.Header.Get("Stripe-Version") != stripeCatalogAPIVersion {
					t.Fatalf("unexpected catalog lookup %s %s", req.Method, req.URL)
				}
				var response string
				switch req.URL.Path {
				case "/v1/account":
					response = fmt.Sprintf(`{"id":%q}`, merchant)
				case "/v1/prices/price_Book":
					if req.URL.Query().Get("expand[]") != "product" {
						t.Fatal("missing Product evidence")
					}
					response = `{"id":"price_Book","currency":"usd","unit_amount":999,"active":true,"livemode":true,"type":"one_time",
					"product":{"id":"prod_Book","active":true,"livemode":true}}`
				default:
					t.Fatalf("unexpected catalog lookup %s", req.URL.Path)
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(response))}, nil
			})}
			const offer = `{"product_id":"prod_Book","price_id":"price_Book","countries":["US"],"enabled":true,"reason":"Direct test"}`
			path := "/channels/779/stripe-one-time"
			query(http.MethodPut, path+"/778", "test-super", offer, http.StatusUnprocessableEntity)
			approved := strings.TrimSuffix(offer, "}") + `,"release_evidence":"qa-fixture","market_policy":"US verification fixture"}`
			query(http.MethodPut, path+"/778", "test-super", approved, http.StatusUnprocessableEntity)
			merchant = "acct_Release"
			query(http.MethodPut, path+"/778", "test-super", approved, http.StatusOK)
			result := query(http.MethodGet, path, "test-super", "", http.StatusOK)
			if !strings.Contains(result, `"release_evidence":"qa-fixture"`) || !strings.Contains(result, `"unit_amount":999`) || strings.Contains(result, key) {
				t.Fatalf("unexpected live catalog projection %s", result)
			}
		})
		if !strings.Contains(got, `"sales_mode":"direct"`) {
			t.Fatalf("one-time mapping: %s", got)
		}
		query(http.MethodGet, "/subscriptions/712/stripe-periods", "test-support", "", http.StatusForbidden)
		query(http.MethodGet, "/subscriptions/712/stripe-periods", "test-finance", "", http.StatusOK)
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM admin_audit_logs WHERE admin_id='test-super' AND action IN ('publish_stripe_vip_offer','publish_stripe_single_offer')`).Scan(&n); err != nil || n != 3 {
			t.Fatalf("Stripe audit rows: %d %v", n, err)
		}
	})
	t.Run("finance commands and ledger schedules use role and request identity", func(t *testing.T) {
		input := settlementCommandInput{Kind: "bank_statement", Environment: "sandbox", Reference: "bank_fixture", BankAccount: "operating",
			Reason: "finance fixture", RequestKey: "0328cd40-8579-422b-a81d-e7f12f647e23"}
		upload := []settlementUpload{{Name: "bank.csv", Data: []byte("fixture")}}
		if _, err := saveSettlementCommand(ctx, "test-support", input, upload); !errors.Is(err, errCaseForbidden) {
			t.Fatalf("support command: %v", err)
		}
		one, err := saveSettlementCommand(ctx, "test-finance", input, upload)
		if err != nil || one["status"] != "pending" {
			t.Fatalf("command=%v error=%v", one, err)
		}
		retry, err := saveSettlementCommand(ctx, "test-finance", input, upload)
		if err != nil || retry["id"] != one["id"] {
			t.Fatalf("duplicate command=%v error=%v", retry, err)
		}
		if _, err := saveSettlementCommand(ctx, "test-finance", input, []settlementUpload{{Name: "bank.csv", Data: []byte("different")}}); !errors.Is(err, errCaseConflict) {
			t.Fatalf("changed upload reused original key: %v", err)
		}
		if err := saveAutomaticLedger(ctx, 777, "test-support", true, "fixture"); !errors.Is(err, errCaseForbidden) {
			t.Fatalf("support schedule: %v", err)
		}
		if err := saveAutomaticLedger(ctx, 777, "test-finance", true, "permissions verified"); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("financial reconciliation follows ledger evidence and bank settlement", func(t *testing.T) {
		testPaymentReconciliation(t, s, exec)
	})
	t.Run("financial cases and actions enforce scope", func(t *testing.T) {
		testAftercareRoutes(t, s, exec)
	})
	t.Run("order links use the verified receipt's environment", func(t *testing.T) {
		orders, _, err := s.ListOrders(ctx, 1, 20, "", "", "", "", "", "", "95503", reconciliationScope{})
		if err != nil || len(orders) != 1 || orders[0].PaymentEnvironment != "sandbox" {
			t.Fatalf("order list scope: %+v %v", orders, err)
		}
		accountID := orders[0].PaymentAccountID
		account, err := strconv.ParseInt(accountID, 10, 64)
		if err != nil || orders[0].PaymentProvider != "paypal" {
			t.Fatalf("order account scope: %+v %v", orders[0], err)
		}
		for _, test := range []struct {
			scope reconciliationScope
			total int
		}{
			{reconciliationScope{Environment: "sandbox", Provider: "paypal", AccountID: account}, 1},
			{reconciliationScope{Environment: "live", Provider: "paypal", AccountID: account}, 0},
			{reconciliationScope{Environment: "sandbox", Provider: "stripe", AccountID: account}, 0},
		} {
			_, total, err := s.ListOrders(ctx, 1, 20, "", "", "", "", "", "", "95503", test.scope)
			if err != nil || total != test.total {
				t.Fatalf("order filter %+v returned %d: %v", test.scope, total, err)
			}
		}
		exec(`INSERT INTO user_subscriptions(id,user_id,subscription_id,payment_method,status,checkout_account_id)
			VALUES(95510,501,'I-SCOPE-95510','paypal','ACTIVE',$1), (95511,501,'I-SCOPE-95511','paypal','SUSPENDED',$1)`, account)
		subscriptions := gin.New()
		subscriptions.Use(func(c *gin.Context) { c.Set("payment_role", c.GetHeader("X-Test-Role")); c.Next() })
		subscriptions.GET("/subscriptions", NewHandler(s).ListPaymentSubscriptions)
		for _, test := range []struct {
			role, query    string
			status         int
			accountVisible bool
		}{
			{"Finance", "?environment=sandbox&provider=paypal&account_id=" + accountID + "&status=ACTIVE&page=1&page_size=1", http.StatusOK, true},
			{"Support", "", http.StatusForbidden, false},
			{"Support", "?user_id=501", http.StatusOK, false},
		} {
			req := httptest.NewRequest(http.MethodGet, "/subscriptions"+test.query, nil)
			req.Header.Set("X-Test-Role", test.role)
			rec := httptest.NewRecorder()
			subscriptions.ServeHTTP(rec, req)
			if rec.Code != test.status || strings.Contains(rec.Body.String(), `"account_id"`) != test.accountVisible {
				t.Fatalf("subscription visibility %s: %d %s", test.role, rec.Code, rec.Body.String())
			}
			if test.accountVisible && (!strings.Contains(rec.Body.String(), "95510") || strings.Contains(rec.Body.String(), "95511")) {
				t.Fatalf("subscription scope: %s", rec.Body.String())
			}
		}
		handler := NewHandler(s)
		router := gin.New()
		router.GET("/orders/:id/evidence", handler.getCheckoutEvidence)
		router.GET("/orders/:id/receipts", handler.ListPaymentReceipts)
		for _, endpoint := range []string{"/orders/95503/evidence", "/orders/95503/receipts"} {
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, endpoint, nil))
			if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"environment":"sandbox"`) {
				t.Fatalf("scope missing at %s: %d %s", endpoint, rec.Code, rec.Body.String())
			}
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/orders/95503/receipts?receipt_id=1", nil))
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"receipts":[]`) {
			t.Fatalf("pinned receipt lookup returned a different receipt: %d %s", rec.Code, rec.Body.String())
		}
	})
	t.Run("verified PayPal refund closes only the attested reservation", func(t *testing.T) {
		req := refundCommandInput{ReceiptID: 95508, AmountMinor: 200, Currency: "USD", RequestKey: "d8132019-9e3b-41c7-b4ef-4d45ef85ad86"}
		cmd, err := submitRefundCommand(ctx, finance, req, 0)
		if err != nil {
			t.Fatal(err)
		}
		exec(`UPDATE payment_refund_commands SET status='review',first_sent_at=now(),next_check_at=NULL WHERE id=$1`, cmd.ID)
		exec(`SELECT save_paypal_refund_item('refund-known-1','scoped-paypal-95508',200,'USD');
			SELECT save_paypal_refund_item('refund-known-2','scoped-paypal-95508',200,'USD')`)
		if _, err := linkConfirmedPayPalRefund(ctx, "test-editor", cmd.ID, "refund-known-1", "test"); !errors.Is(err, ErrRefundForbidden) {
			t.Fatalf("unauthorized link: %v", err)
		}
		if _, err := linkConfirmedPayPalRefund(ctx, finance, cmd.ID, "refund-unknown", "test"); !errors.Is(err, ErrRefundAmountUnavailable) {
			t.Fatalf("unverified refund linked: %v", err)
		}
		for range 2 {
			if result, err := linkConfirmedPayPalRefund(ctx, finance, cmd.ID, "refund-known-2", "same PayPal request confirmed"); err != nil || result.Status != "succeeded" {
				t.Fatalf("link completed refund: %+v %v", result, err)
			}
		}
		var ref string
		var audit int
		if err := pool.QueryRow(ctx, `SELECT provider_refund_id,(SELECT count(*) FROM admin_audit_logs
			WHERE action='reconcile_paypal_refund' AND target_id=$1::text) FROM payment_refund_commands WHERE id=$1::uuid`,
			cmd.ID).Scan(&ref, &audit); err != nil || ref != "refund-known-2" || audit != 1 {
			t.Fatalf("linked %q audit %d: %v", ref, audit, err)
		}
		queue, err := listRefundQueue(ctx, refundQueueFilter{
			reconciliationPage: reconciliationPage{reconciliationScope: reconciliationScope{Environment: "live", Provider: "paypal"}},
			OrderID:            95508, Status: "succeeded", CommandID: cmd.ID,
		})
		if err != nil || !strings.Contains(string(queue), cmd.ID) || !strings.Contains(string(queue), "refund-known-2") {
			t.Fatalf("completed refund queue: %s, %v", queue, err)
		}
		queue, err = listRefundQueue(ctx, refundQueueFilter{
			reconciliationPage: reconciliationPage{reconciliationScope: reconciliationScope{Environment: "sandbox", Provider: "paypal"}},
			CommandID:          cmd.ID,
		})
		if err != nil || strings.Contains(string(queue), cmd.ID) {
			t.Fatalf("refund command crossed environment: %s, %v", queue, err)
		}
		queue, err = listRefundQueue(ctx, refundQueueFilter{
			reconciliationPage: reconciliationPage{reconciliationScope: reconciliationScope{Environment: "live", Provider: "paypal"}},
			OrderID:            95508, Status: "succeeded",
		})
		if err != nil || !strings.Contains(string(queue), cmd.ID) {
			t.Fatalf("refund queue by order: %s, %v", queue, err)
		}
	})
	t.Run("migrated PayPal refund can close with verified evidence and no send timestamp", func(t *testing.T) {
		const id = "59719318-a112-486d-80b7-1a096c93954e"
		exec(`INSERT INTO payment_refund_commands(id,environment,request_key,order_id,receipt_id,amount_minor,
			currency,status,legacy_source) VALUES($1,'live',$1,95508,95508,200,'USD','review',true)`, id)
		cmd, err := findRefundCommand(ctx, id, finance, "")
		if err != nil || !cmd.CanReconcile {
			t.Fatalf("legacy eligibility: %+v %v", cmd, err)
		}
		receipts := gin.New()
		receipts.GET("/orders/:id/receipts", NewHandler(s).ListPaymentReceipts)
		available := func() int {
			t.Helper()
			w := httptest.NewRecorder()
			receipts.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/orders/95508/receipts", nil))
			var body struct {
				Receipts []struct {
					Available int `json:"available_minor"`
				} `json:"receipts"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); w.Code != http.StatusOK || err != nil || len(body.Receipts) != 1 {
				t.Fatalf("receipts: %d %s %v", w.Code, w.Body.String(), err)
			}
			return body.Receipts[0].Available
		}
		before := available()
		// refund-known-2 is already linked to the equal-amount modern command.
		if _, err := linkConfirmedPayPalRefund(ctx, finance, id, "refund-known-2", "already taken"); !errors.Is(err, ErrRefundAmountUnavailable) {
			t.Fatalf("duplicate reference: %v", err)
		}
		for range 2 {
			cmd, err := linkConfirmedPayPalRefund(ctx, finance, id, "refund-known-1", "verified original request in PayPal")
			if err != nil || cmd.Status != "succeeded" || cmd.CanReconcile {
				t.Fatalf("legacy closure: %+v %v", cmd, err)
			}
		}
		if after := available(); after-before != 200 {
			t.Fatalf("confirmed refund and reservation were not reconciled: %d -> %d", before, after)
		}
		var audits, refunds, closedWithoutSend int
		err = pool.QueryRow(ctx, `SELECT count(*) FILTER(WHERE first_sent_at IS NULL AND status='succeeded'),
			(SELECT count(*) FROM paypal_refund_items WHERE capture_id='scoped-paypal-95508'),
			(SELECT count(*) FROM admin_audit_logs WHERE action='reconcile_paypal_refund' AND target_id=$1::text)
			FROM payment_refund_commands WHERE id=$1::uuid`, id).Scan(&closedWithoutSend, &refunds, &audits)
		if err != nil || closedWithoutSend != 1 || refunds != 2 || audits != 1 {
			t.Fatalf("refund records=%d audit=%d closed without send=%d: %v", refunds, audits, closedWithoutSend, err)
		}
	})

}
