package wallet

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"reader-backend/internal/auth"
	"reader-backend/internal/config"
	"reader-backend/internal/model"
	"reader-backend/internal/novel"
	"reader-backend/internal/payment"
	"reader-backend/internal/testdb"
)

func TestAllOwnedAgreementsRemainManageable(t *testing.T) {
	p := testdb.Open(t)
	ctx := context.Background()
	gin.SetMode(gin.TestMode)
	config.AppConfig = &config.Config{JWTSecret: "isolated-agreement-test-secret"}
	if _, err := p.Exec(ctx, `INSERT INTO users(id) VALUES(1),(2)`); err != nil {
		t.Fatal(err)
	}
	service := NewService(NewDBRepository(), novel.NewDBRepository(), nil, nil, nil)
	router := gin.New()
	NewHandler(service).RegisterRoutes(router.Group("/api/v1"))
	token, err := auth.GenerateToken(1)
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	for _, pair := range [][2]string{{"ACTIVE", "CANCELLED"}, {"ACTIVE", "PENDING"}, {"ACTIVE", "ACTIVE"}, {"SUSPENDED", "CANCEL_PENDING"}} {
		t.Run(pair[0]+"_"+pair[1], func(t *testing.T) {
			if _, err := p.Exec(ctx, `DELETE FROM user_subscriptions`); err != nil {
				t.Fatal(err)
			}
			if _, err := p.Exec(ctx, `INSERT INTO user_subscriptions(id,user_id,subscription_id,status,last_payment_time,current_period_end)
    VALUES(1,1,'I-OLDER',$1,now()-interval '2 months',now()-interval '1 day'),(2,1,'I-NEWER',$2,now()-interval '1 day',now()+interval '20 days'),(3,2,'I-OTHER','ACTIVE',NULL,NULL),(4,1,'I-MOCK-HIDDEN','ACTIVE',NULL,NULL)`, pair[0], pair[1]); err != nil {
				t.Fatal(err)
			}
			response := call(http.MethodGet, "/api/v1/wallet/subscription/status", "")
			if response.Code != 200 {
				t.Fatalf("status %d: %s", response.Code, response.Body.String())
			}
			var status struct {
				Subscriptions []model.UserSubscription `json:"subscriptions"`
				Subscription  *model.UserSubscription  `json:"subscription"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil {
				t.Fatal(err)
			}
			if len(status.Subscriptions) != 2 || status.Subscriptions[0].SubscriptionID != "I-NEWER" || status.Subscriptions[1].SubscriptionID != "I-OLDER" || status.Subscription == nil {
				t.Fatalf("missing owned agreement: %s", response.Body.String())
			}
			response = call(http.MethodPost, "/api/v1/wallet/subscription/cancel", `{"subscription_id":"I-OLDER"}`)
			if response.Code != 202 {
				t.Fatalf("cannot cancel older agreement: %d %s", response.Code, response.Body.String())
			}
			var actual, newer string
			if err := p.QueryRow(ctx, `SELECT status FROM user_subscriptions WHERE subscription_id='I-OLDER'`).Scan(&actual); err != nil || actual != "CANCEL_PENDING" {
				t.Fatal("older cancellation not persisted", err)
			}
			if err := p.QueryRow(ctx, `SELECT status FROM user_subscriptions WHERE subscription_id='I-NEWER'`).Scan(&newer); err != nil || newer != pair[1] {
				t.Fatal("wrong agreement changed", err)
			}
			response = call(http.MethodPost, "/api/v1/wallet/subscription/cancel", `{"subscription_id":"I-OTHER"}`)
			if response.Code != http.StatusNotFound {
				t.Fatal("another user's agreement was cancellable")
			}
			if err := p.QueryRow(ctx, `SELECT status FROM user_subscriptions WHERE subscription_id='I-OTHER'`).Scan(&actual); err != nil || actual != "ACTIVE" {
				t.Fatal("another user's agreement changed", err)
			}
		})
	}
	if _, err := p.Exec(ctx, `DELETE FROM user_subscriptions`); err != nil {
		t.Fatal(err)
	}
	response := call(http.MethodGet, "/api/v1/wallet/subscription/status", "")
	if !strings.Contains(response.Body.String(), `"subscriptions":[]`) {
		t.Fatalf("empty list not serialized: %s", response.Body.String())
	}
}

func TestInheritedAndExplicitOffersKeepPromotionIdentity(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		for _, hasPixel := range []bool{false, true} {
			t.Run(fmt.Sprintf("explicit_%v_pixel_%v", explicit, hasPixel), func(t *testing.T) {
				p := testdb.Open(t)
				ctx := context.Background()
				exec := func(q string, args ...any) {
					t.Helper()
					if _, err := p.Exec(ctx, q, args...); err != nil {
						t.Fatal(err)
					}
				}
				exec(`INSERT INTO novels(id,title,author) VALUES(1,'Book','Author'),(2,'Other','Author');INSERT INTO recharge_templates(id,name,is_default) VALUES(1,'Default',true),(2,'Explicit',false);
    INSERT INTO recharge_slots(id,template_id,slot_index,type,coins,price,price_cents) VALUES(1,1,1,'single',100,'$9.99',999),(2,2,1,'single',100,'$9.99',999),(3,1,2,'whole_book',0,'$9.99',999),(4,2,2,'whole_book',0,'$9.99',999);
    INSERT INTO fb_pixels(id,name,pixel_id,access_token) VALUES(1,'Pixel','configured-pixel','fake');`)
				var template, pixel any
				templateID := 1
				if explicit {
					template = 2
					templateID = 2
				}
				if hasPixel {
					pixel = 1
				}
				exec(`INSERT INTO promotion_links(id,novel_id,novel_title,generated_url,recharge_template_id,fb_pixel_id) VALUES(1,1,'Book','https://example.invalid',$1,$2);`, template, pixel)
				exec(`INSERT INTO users(id,promotion_link_id,promotion_resolved) VALUES(1,1,true)`)
				r := &dbRepository{}
				selected, err := r.GetCheckoutTemplate(ctx, 1)
				if err != nil || selected != templateID {
					t.Fatal("wrong effective template", selected, err)
				}
				slot, err := r.GetRechargeSlotByID(ctx, templateID)
				if err != nil {
					t.Fatal(err)
				}
				key, err := r.CreateCheckout(ctx, 1, slot, 0, checkoutAttribution{FBC: "original-click"})
				if err != nil {
					t.Fatal(err)
				}
				var link int
				var selectedPixel *string
				if err := p.QueryRow(ctx, `SELECT promotion_link_id,fb_lead_metadata->>'pixel_id' FROM recharge_orders WHERE checkout_id=$1`, key).Scan(&link, &selectedPixel); err != nil || link != 1 {
					t.Fatal("promotion lost", err)
				}
				if hasPixel && (selectedPixel == nil || *selectedPixel != "configured-pixel") {
					t.Fatal("configured pixel lost")
				}
				if !hasPixel && selectedPixel != nil {
					t.Fatal("pixel invented")
				}
				if err := r.BindCheckout(ctx, key, "order"); err != nil {
					t.Fatal(err)
				}
				if _, err := r.settleCheckout(ctx, "order", &payment.PayPalCaptureResult{OrderID: "order", CustomID: key, CaptureID: "capture", CurrencyCode: "USD", GrossAmount: "9.99", FeeAmount: "0.00", NetAmount: "9.99", Status: "COMPLETED", RawPayload: "{}"}); err != nil {
					t.Fatal(err)
				}
				var state string
				if err := p.QueryRow(ctx, `SELECT state FROM payment_event_outbox`).Scan(&state); err != nil {
					t.Fatal(err)
				}
				want := "unattributed"
				if hasPixel {
					want = "pending"
				}
				if state != want {
					t.Fatalf("state %s want %s", state, want)
				}
				bookSlot, err := r.GetRechargeSlotByID(ctx, templateID+2)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := r.CreateCheckout(ctx, 1, bookSlot, 2, checkoutAttribution{}); err != ErrCheckoutUnavailable {
					t.Fatalf("promotion admitted wrong whole book: %v", err)
				}
				if _, err := r.CreateCheckout(ctx, 1, bookSlot, 1, checkoutAttribution{}); err != nil {
					t.Fatalf("promotion rejected its own whole book: %v", err)
				}
			})
		}
	}
}

func TestLegacyRestorationNeverEnrollsPaymentEvents(t *testing.T) {
	p := testdb.Open(t)
	ctx := context.Background()
	if _, err := p.Exec(ctx, `INSERT INTO users(id) VALUES(1);INSERT INTO recharge_orders(id,user_id,amount_cents,payment_method,status,fulfilled_at,paid_at,fb_lead_metadata)
  VALUES(100,1,999,'paypal','Success',now()-interval '1 hour',now()-interval '1 hour','{"pixel_id":"original"}');
  UPDATE recharge_orders SET status='RefundPending' WHERE id=100;UPDATE recharge_orders SET status='Success' WHERE id=100;
  UPDATE recharge_orders SET reconciliation_note='reviewed' WHERE id=100`); err != nil {
		t.Fatal(err)
	}
	schema, err := os.ReadFile("../../../cms-backend/db/schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Exec(ctx, string(schema)); err != nil {
		t.Fatal(err)
	}
	var enabled bool
	if err := p.QueryRow(ctx, `SELECT payment_events_enabled FROM recharge_orders WHERE id=100`).Scan(&enabled); err != nil || enabled {
		t.Fatal("legacy order implicitly enrolled", err)
	}
	var count int
	if err := p.QueryRow(ctx, `SELECT count(*) FROM payment_event_outbox`).Scan(&count); err != nil || count != 0 {
		t.Fatal("legacy payment reemitted", err)
	}
	// Even explicitly toggling eligibility cannot turn an already fulfilled UPDATE into first fulfillment.
	if _, err := p.Exec(ctx, `UPDATE recharge_orders SET payment_events_enabled=true WHERE id=100;UPDATE recharge_orders SET status='RefundPending' WHERE id=100;UPDATE recharge_orders SET status='Success' WHERE id=100`); err != nil {
		t.Fatal(err)
	}
	if err := p.QueryRow(ctx, `SELECT count(*) FROM payment_event_outbox`).Scan(&count); err != nil || count != 0 {
		t.Fatal("restoration treated as settlement", err)
	}
}

func TestDeferredNewSubscriptionEmitsOnlyAtFirstFulfillment(t *testing.T) {
	p := testdb.Open(t)
	ctx := context.Background()
	if _, err := p.Exec(ctx, `INSERT INTO users(id) VALUES(1);INSERT INTO user_subscriptions(user_id,subscription_id,status,cycle,price_cents,attribution) VALUES(1,'I-DEFERRED','PENDING','month',999,'{"pixel_id":"original"}')`); err != nil {
		t.Fatal(err)
	}
	r := &dbRepository{}
	sub, err := r.GetSubscriptionByID(ctx, "I-DEFERRED")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	receipt := &model.ThirdPartyPaymentOrder{Status: "COMPLETED", RawPayload: "{}"}
	if _, err := r.RecordSubscriptionRenewalOrderTx(ctx, sub, "deferred-sale", 999, "USD", now, time.Time{}, "ACTIVE", receipt); err != nil {
		t.Fatal(err)
	}
	access := novel.NewDBRepository()
	if active, err := access.HasActiveSubscription(ctx, 1); err != nil || active {
		t.Fatalf("unproven period granted VIP: %t %v", active, err)
	}
	statuses, err := PaymentTaskStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int64{}
	for _, status := range statuses {
		counts[status.Kind] = status.Count
	}
	if counts["manual_review"] != 1 || counts["paid_unfulfilled"] != 1 {
		t.Fatalf("unproven period not visible: %+v", statuses)
	}
	var count int
	if err := p.QueryRow(ctx, `SELECT count(*) FROM payment_event_outbox`).Scan(&count); err != nil || count != 0 {
		t.Fatal("unfulfilled payment emitted", err)
	}
	for range 2 {
		if _, err := r.RecordSubscriptionRenewalOrderTx(ctx, sub, "deferred-sale", 999, "USD", now, now.AddDate(0, 1, 0), "ACTIVE", receipt); err != nil {
			t.Fatal(err)
		}
	}
	if active, err := access.HasActiveSubscription(ctx, 1); err != nil || !active {
		t.Fatalf("verified period did not grant VIP: %t %v", active, err)
	}
	if err := p.QueryRow(ctx, `SELECT count(*) FROM payment_event_outbox`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("Purchase/Subscribe count=%d %v", count, err)
	}
	if _, err := p.Exec(ctx, `UPDATE recharge_orders SET status='RefundPending';UPDATE recharge_orders SET status='Success'`); err != nil {
		t.Fatal(err)
	}
	if err := p.QueryRow(ctx, `SELECT count(*) FROM payment_event_outbox`).Scan(&count); err != nil || count != 2 {
		t.Fatal("restoration duplicated events", err)
	}
}
