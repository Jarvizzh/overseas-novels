package billing

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"star-novel-cms/internal/config"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"star-novel-cms/internal/db"
)

func TestRefundAuthorizationAndFailedAttemptIntegration(t *testing.T) {
	dsn := os.Getenv("STAR_NOVEL_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires a disposable PostgreSQL database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	db.DB = pool
	defer func() { db.DB = nil }()
	schema, err := os.ReadFile("../../db/schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(string(schema))
	exec(`INSERT INTO admins(id,username,password_hash,role,status)
		VALUES('test-editor','test-editor','none','Editor',1),
		('test-disabled','test-disabled','none','Finance',2),
		('test-finance','test-finance','none','Finance',1)`)
	exec(`INSERT INTO users(id) VALUES(501)`)
	exec(`INSERT INTO recharge_orders(id,user_id,external_ref_id,amount_cents,payment_method,status,order_type)
		VALUES(5501,501,'refund-test-order',999,'paypal','Success','single')`)
	exec(`INSERT INTO third_party_payment_orders(order_id,payment_provider,capture_id,currency,gross_amount,status)
		VALUES(5501,'paypal','refund-test-capture','USD',9.99,'VERIFIED')`)
	s := &billingService{repo: NewBillingRepository()}
	if err := s.RefundOrder(ctx, "5501", ""); !errors.Is(err, ErrRefundForbidden) {
		t.Fatalf("empty actor: %v", err)
	}
	for _, actor := range []string{"test-editor", "test-disabled", "test-missing"} {
		if _, err := s.beginPayPalRefund(ctx, "5501", &actor); !errors.Is(err, ErrRefundForbidden) {
			t.Fatalf("%s: %v", actor, err)
		}
	}
	if req, err := s.beginPayPalRefund(ctx, "5501", nil); err != nil || req != nil {
		t.Fatalf("worker created a refund: %+v %v", req, err)
	}

	finance := "test-finance"
	first, err := s.beginPayPalRefund(ctx, "5501", &finance)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.failPayPalRefund(ctx, first, &paypalRefundResult{ID: "failed-refund", Status: "FAILED"}); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM recharge_orders WHERE id=5501`).Scan(&status); err != nil || status != "Success" {
		t.Fatalf("failed refund did not restore order: %s %v", status, err)
	}
	second, err := s.beginPayPalRefund(ctx, "5501", &finance)
	if err != nil || second.requestID == first.requestID || second.refundID != "" {
		t.Fatalf("retry attempt: %+v %v", second, err)
	}
	if err := s.failPayPalRefund(ctx, first, &paypalRefundResult{ID: "failed-refund", Status: "FAILED"}); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT status FROM recharge_orders WHERE id=5501`).Scan(&status); err != nil || status != "RefundPending" {
		t.Fatalf("stale failure affected retry: %s %v", status, err)
	}
	t.Run("definitive HTTP rejection restores paid status and records failed attempt", func(t *testing.T) {
		exec(`INSERT INTO recharge_orders(id,user_id,external_ref_id,amount_cents,payment_method,status,order_type)
   VALUES(5502,501,'refund-rejection-order',999,'paypal','Success','single')`)
		exec(`INSERT INTO third_party_payment_orders(order_id,payment_provider,capture_id,currency,gross_amount,status)
   VALUES(5502,'paypal','refund-rejection-capture','USD',9.99,'VERIFIED')`)
		request, err := s.beginPayPalRefund(ctx, "5502", &finance)
		if err != nil {
			t.Fatal(err)
		}
		oldConfig := config.AppConfig
		config.AppConfig = &config.Config{PayPalClientID: "test-id", PayPalClientSecret: "test-secret"}
		defer func() { config.AppConfig = oldConfig }()
		oldTransport := http.DefaultTransport
		defer func() { http.DefaultTransport = oldTransport }()
		refundCode, refundBody := 422, `{"name":"UNPROCESSABLE_ENTITY","details":[{"issue":"REFUND_TIME_LIMIT_EXCEEDED"}]}`
		posts := 0
		http.DefaultTransport = refundTransportFunc(func(req *http.Request) (*http.Response, error) {
			code, body := http.StatusOK, `{"access_token":"test-token"}`
			if strings.HasSuffix(req.URL.Path, "/refund") {
				if req.Header.Get("PayPal-Request-Id") != request.requestID {
					t.Error("provider retry key changed")
				}
				posts++
				code, body = refundCode, refundBody
			} else if req.Method == http.MethodGet && strings.Contains(req.URL.Path, "/captures/") {
				code, body = 200, `{"id":"refund-rejection-capture","status":"REFUNDED"}`
			} else if !strings.HasSuffix(req.URL.Path, "/oauth2/token") {
				return nil, fmt.Errorf("unexpected test request %s", req.URL.Path)
			}
			return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
		})
		if err := s.submitPayPalRefund(ctx, request); !errors.Is(err, ErrRefundFailed) {
			t.Fatalf("definitive rejection remained pending: %v", err)
		}
		var status, attempt, refundID string
		if err := pool.QueryRow(ctx, `SELECT o.status,r.status,COALESCE(r.provider_refund_id,'') FROM recharge_orders o JOIN payment_refunds r ON r.order_id=o.id WHERE o.id=5502`).Scan(&status, &attempt, &refundID); err != nil || status != "Success" || attempt != "Failed" || refundID != "" {
			t.Fatalf("rejection state: %s %s %s %v", status, attempt, refundID, err)
		}
		var failures int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM payment_refund_failures WHERE order_id=5502 AND provider_status='REFUND_TIME_LIMIT_EXCEEDED'`).Scan(&failures); err != nil || failures != 1 {
			t.Fatalf("missing rejection audit: %d %v", failures, err)
		}
		request, err = s.beginPayPalRefund(ctx, "5502", &finance)
		if err != nil {
			t.Fatal(err)
		}
		refundCode, refundBody = 503, `{"name":"INTERNAL_SERVER_ERROR"}`
		if err := s.submitPayPalRefund(ctx, request); !errors.Is(err, ErrRefundPending) {
			t.Fatalf("uncertain result was not preserved: %v", err)
		}
		retry, err := s.beginPayPalRefund(ctx, "5502", &finance)
		if err != nil || retry.requestID != request.requestID {
			t.Fatalf("uncertain retry changed idempotency key: %+v %v", retry, err)
		}
		refundCode, refundBody = 422, `{"name":"UNPROCESSABLE_ENTITY","details":[{"issue":"CAPTURED_AMOUNT_FULLY_REFUNDED"}]}`
		if err := s.submitPayPalRefund(ctx, retry); !errors.Is(err, ErrRefundPending) {
			t.Fatalf("external refund reconciliation: %v", err)
		}
		lookup, err := s.beginPayPalRefund(ctx, "5502", &finance)
		if err != nil || !lookup.lookupOnly {
			t.Fatalf("external refund did not enter lookup: %+v %v", lookup, err)
		}
		beforePosts := posts
		if err := s.submitPayPalRefund(ctx, lookup); !errors.Is(err, ErrRefundPending) || posts != beforePosts {
			t.Fatalf("lookup issued another refund: %d %d %v", beforePosts, posts, err)
		}

	})

	t.Run("template writes require live financial permission and atomic audit", func(t *testing.T) {
		slots := []RechargeSlot{{SlotIndex: 1, Type: "single", Coins: 100, Price: "$9.99", PriceCents: 999},
			{SlotIndex: 2, Type: "single", Coins: 200, Price: "$19.99", PriceCents: 1999},
			{SlotIndex: 3, Type: "subscription", SubscriptionCycle: "month", Price: "$9.99", PriceCents: 999},
			{SlotIndex: 4, Type: "whole_book", Price: "$9.99", PriceCents: 999}}
		for _, actor := range []string{"", "test-editor", "test-disabled", "test-missing"} {
			if _, err := s.CreateRechargeTemplate(ctx, actor, "unauthorized", false, slots); !errors.Is(err, ErrRefundForbidden) {
				t.Fatalf("create %s: %v", actor, err)
			}
			if err := s.UpdateRechargeTemplate(ctx, actor, 999999, "unauthorized", false, slots); !errors.Is(err, ErrRefundForbidden) {
				t.Fatalf("update %s: %v", actor, err)
			}
			if err := s.DeleteRechargeTemplate(ctx, actor, 999999); !errors.Is(err, ErrRefundForbidden) {
				t.Fatalf("delete %s: %v", actor, err)
			}
			if err := s.SetDefaultRechargeTemplate(ctx, actor, 999999); !errors.Is(err, ErrRefundForbidden) {
				t.Fatalf("default %s: %v", actor, err)
			}
		}
		id, err := s.CreateRechargeTemplate(ctx, finance, "audited", false, slots)
		if err != nil {
			t.Fatal(err)
		}
		slots[0].PriceCents = 1299
		slots[0].Price = "$12.99"
		if err := s.UpdateRechargeTemplate(ctx, finance, id, "audited edit", false, slots); err != nil {
			t.Fatal(err)
		}
		var before, after int
		if err := pool.QueryRow(ctx, `SELECT (before_data->'slots'->0->>'price_cents')::int,(after_data->'slots'->0->>'price_cents')::int
   FROM admin_audit_logs WHERE action='update_recharge_template' AND target_id=$1`, fmt.Sprint(id)).Scan(&before, &after); err != nil || before != 999 || after != 1299 {
			t.Fatalf("price audit: %d %d %v", before, after, err)
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
		another, err := s.CreateRechargeTemplate(ctx, finance, "to delete", false, slots)
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

}

type refundTransportFunc func(*http.Request) (*http.Response, error)

func (f refundTransportFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestPayPalRefundErrorAndDecimalParsing(t *testing.T) {
	client := &http.Client{Transport: refundTransportFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 422, Body: io.NopCloser(strings.NewReader(`{"name":"UNPROCESSABLE_ENTITY","details":[{"issue":"REFUND_TIME_LIMIT_EXCEEDED"}]}`)), Header: make(http.Header)}, nil
	})}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://example.invalid/refund", nil)
	err := readPayPalResponse(client, req, &struct{}{})
	var rejection *paypalHTTPError
	if !errors.As(err, &rejection) || rejection.status != 422 || rejection.issue != "REFUND_TIME_LIMIT_EXCEEDED" {
		t.Fatalf("lost structured provider error: %v", err)
	}
	if !matchesRefundAmount("9.990", 999) || matchesRefundAmount("9.999", 999) || matchesRefundAmount("-9.99", 999) {
		t.Fatal("refund cents were not compared exactly")
	}
}
