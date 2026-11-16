package billing

import (
	"context"
	"errors"
	"os"
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
}
