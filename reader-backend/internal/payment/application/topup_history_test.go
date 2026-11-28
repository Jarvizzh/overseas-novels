package application

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"reader-backend/internal/payment"
	"reader-backend/internal/testdb"
)

func TestResolveTopUpDisplayStatus(t *testing.T) {
	now := time.Now().UTC()
	cases := []struct {
		name string
		in   topUpStatusInput
		want string
	}{
		{
			name: "completed fulfilled order",
			in:   topUpStatusInput{rawStatus: "Success", fulfilledAt: &now},
			want: TopUpStatusCompleted,
		},
		{
			name: "confirming paid not yet fulfilled",
			in:   topUpStatusInput{rawStatus: "Paid", fulfilledAt: nil},
			want: TopUpStatusConfirming,
		},
		{
			name: "under review via reconciliation note",
			in:   topUpStatusInput{rawStatus: "Paid", fulfilledAt: nil, reconciliationNote: "needs period evidence"},
			want: TopUpStatusUnderReview,
		},
		{
			name: "under review via deferred fulfillment",
			in:   topUpStatusInput{rawStatus: "Paid", fulfilledAt: nil, hasDeferredFulfillment: true},
			want: TopUpStatusUnderReview,
		},
		{
			name: "partially refunded keeps entitlement",
			in:   topUpStatusInput{rawStatus: "Success", fulfilledAt: &now, refundedMinor: 500},
			want: TopUpStatusPartiallyRefunded,
		},
		{
			name: "refund pending from order status",
			in:   topUpStatusInput{rawStatus: "RefundPending", fulfilledAt: &now},
			want: TopUpStatusRefundPending,
		},
		{
			name: "refund pending from active command",
			in:   topUpStatusInput{rawStatus: "Success", fulfilledAt: &now, hasPendingRefund: true},
			want: TopUpStatusRefundPending,
		},
		{
			name: "refund pending from paypal refund reported",
			in:   topUpStatusInput{rawStatus: "Success", fulfilledAt: &now, paypalRefundReported: true},
			want: TopUpStatusRefundPending,
		},
		{
			name: "revoked takes precedence over partial or pending refund",
			in:   topUpStatusInput{rawStatus: "Success", fulfilledAt: &now, entitlementBlocked: true, refundedMinor: 200},
			want: TopUpStatusRevoked,
		},
		{
			name: "refunded takes highest precedence",
			in:   topUpStatusInput{rawStatus: "Refunded", fulfilledAt: &now, entitlementBlocked: true, refundedMinor: 999},
			want: TopUpStatusRefunded,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveTopUpDisplayStatus(tc.in); got != tc.want {
				t.Fatalf("resolveTopUpDisplayStatus() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResolveTopUpTitle(t *testing.T) {
	if got := resolveTopUpTitle("whole_book", "Moonlit Alpha", "Full Book", "Book Pack", "Prod", "", 0); got != "Moonlit Alpha" {
		t.Fatalf("whole_book title = %q, want Moonlit Alpha", got)
	}
	if got := resolveTopUpTitle("whole_book", "", "", "", "", "", 0); got != "Full Book Unlock" {
		t.Fatalf("whole_book fallback = %q", got)
	}
	if got := resolveTopUpTitle("subscription", "", "VIP Weekly", "Weekly", "Prod", "week", 0); got != "VIP Weekly" {
		t.Fatalf("subscription alias = %q", got)
	}
	if got := resolveTopUpTitle("subscription", "", "", "", "", "month", 0); got != "Monthly Unlimited Pass" {
		t.Fatalf("subscription fallback = %q", got)
	}
	if got := resolveTopUpTitle("single", "", "", "", "", "", 500); got != "500 Coins Pack" {
		t.Fatalf("single fallback = %q", got)
	}
}

func TestParseTopUpHistoryCursorAndFilter(t *testing.T) {
	ts := time.Date(2026, 11, 28, 12, 34, 56, 123456000, time.UTC)
	raw := formatTopUpHistoryCursor(ts, 10482)
	parsed, err := parseTopUpHistoryCursor(raw)
	if err != nil {
		t.Fatalf("parseTopUpHistoryCursor(%q) unexpected error: %v", raw, err)
	}
	if !parsed.createdAt.Equal(ts) || parsed.id != 10482 {
		t.Fatalf("parsed cursor = %+v, want %v / 10482", parsed, ts)
	}

	for _, bad := range []string{"!", "bm90LWEtdGltZQ", strings.Repeat("A", 129), formatTopUpHistoryCursor(ts, 0)} {
		if _, err := parseTopUpHistoryCursor(bad); err == nil {
			t.Fatalf("expected error for bad cursor %q", bad)
		}
	}

	for _, valid := range []string{"", "all", "single", "whole_book", "subscription", "SINGLE"} {
		if _, err := normalizeTopUpOrderTypeFilter(valid); err != nil {
			t.Fatalf("unexpected error for filter %q: %v", valid, err)
		}
	}
	if _, err := normalizeTopUpOrderTypeFilter("vip"); err == nil {
		t.Fatal("expected error for invalid filter")
	}
}

type fakeTopUpHistoryRepo struct {
	Repository
	items     []TopUpHistoryItem
	lastUser  int64
	lastType  string
	lastLimit int
}

func (f *fakeTopUpHistoryRepo) listTopUpHistory(_ context.Context, userID int64, orderType string, limit int, _ *topUpHistoryCursor) ([]TopUpHistoryItem, error) {
	f.lastUser = userID
	f.lastType = orderType
	f.lastLimit = limit
	out := make([]TopUpHistoryItem, len(f.items))
	copy(out, f.items)
	return out, nil
}

func TestListTopUpHistoryHandlerValidationAndPrivacy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	now := time.Date(2026, 11, 28, 10, 0, 0, 0, time.UTC)
	eur := int64(920)
	repo := &fakeTopUpHistoryRepo{
		items: []TopUpHistoryItem{
			{
				OrderID:                10482,
				OrderNo:                "SN-10482",
				OrderType:              "single",
				Title:                  "500 Coins Pack",
				DisplayStatus:          TopUpStatusCompleted,
				AmountMinor:            999,
				Currency:               "USD",
				PresentmentAmountMinor: &eur,
				PresentmentCurrency:    "EUR",
				Coins:                  500,
				BonusCoins:             100,
				Provider:               "stripe",
				CreatedAt:              now,
				PaidAt:                 &now,
				FulfilledAt:            &now,
			},
		},
	}
	svc := NewService(repo, nil, payment.Adapters{})
	h := NewHandler(svc)

	router := gin.New()
	group := router.Group("/api/v1/wallet", func(c *gin.Context) {
		c.Set("user_id", int64(77))
		c.Next()
	})
	group.GET("/topup-history", h.ListTopUpHistory)

	t.Run("valid request returns private no-store response without external refs", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/wallet/topup-history?limit=10&type=single", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Cache-Control"); got != "private, no-store" {
			t.Fatalf("Cache-Control = %q", got)
		}
		if repo.lastUser != 77 || repo.lastType != "single" || repo.lastLimit != 11 {
			t.Fatalf("repo args = user:%d type:%q limit:%d", repo.lastUser, repo.lastType, repo.lastLimit)
		}
		body := rec.Body.String()
		for _, forbidden := range []string{"external_ref_id", "subscription_id", "reconciliation_note", "capture_id", "payer_email"} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("response must not expose %q: %s", forbidden, body)
			}
		}
		var page TopUpHistoryPage
		if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		if len(page.Items) != 1 || page.Items[0].OrderNo != "SN-10482" || page.Items[0].PresentmentCurrency != "EUR" {
			t.Fatalf("unexpected page: %+v", page)
		}
	})

	t.Run("empty result serializes items as empty JSON array rather than null", func(t *testing.T) {
		emptySvc := NewService(&fakeTopUpHistoryRepo{items: nil}, nil, payment.Adapters{})
		emptyHandler := NewHandler(emptySvc)
		emptyRouter := gin.New()
		emptyRouter.GET("/api/v1/wallet/topup-history", func(c *gin.Context) {
			c.Set("user_id", int64(88))
			emptyHandler.ListTopUpHistory(c)
		})
		req := httptest.NewRequest(http.MethodGet, "/api/v1/wallet/topup-history", nil)
		rec := httptest.NewRecorder()
		emptyRouter.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
		if strings.TrimSpace(rec.Body.String()) != `{"items":[]}` {
			t.Fatalf("empty body = %s, want {\"items\":[]}", rec.Body.String())
		}
	})

	for _, tc := range []struct {
		name string
		url  string
		code string
	}{
		{name: "limit too small", url: "/api/v1/wallet/topup-history?limit=0", code: "invalid_pagination"},
		{name: "limit too large", url: "/api/v1/wallet/topup-history?limit=51", code: "invalid_pagination"},
		{name: "invalid cursor", url: "/api/v1/wallet/topup-history?cursor=not-valid", code: "invalid_cursor"},
		{name: "invalid filter", url: "/api/v1/wallet/topup-history?type=invalid", code: "invalid_filter"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.url, nil)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rec.Code)
			}
			if !strings.Contains(rec.Body.String(), tc.code) {
				t.Fatalf("body %s missing code %q", rec.Body.String(), tc.code)
			}
		})
	}
}

func TestTopUpHistoryDatabaseQuery(t *testing.T) {
	t.Setenv("CONVERSION_ENVIRONMENT", "production")
	ctx := t.Context()
	pool := testdb.Open(t)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}

	exec(`
		INSERT INTO users(id) VALUES (501), (502);
		INSERT INTO novels(id, title, author, publish_status) VALUES (901, 'Starlight Heir', 'Author A', 'published');
		INSERT INTO payment_channel_accounts(id, provider, account_key, environment, credentials_ref, webhook_ref)
		OVERRIDING SYSTEM VALUE
		VALUES
			(10, 'stripe', 'acct_live', 'live', 'fixture', 'wh_live'),
			(11, 'paypal', 'pp_live', 'live', 'fixture', 'wh_pp_live'),
			(12, 'stripe', 'acct_sb', 'sandbox', 'fixture', 'wh_sb');
		INSERT INTO user_subscriptions(id, user_id, subscription_id, status, cycle, price_cents, currency, payment_method, checkout_account_id)
		VALUES (301, 501, 'I-LIVE-SUB-1', 'ACTIVE', 'week', 999, 'USD', 'paypal', 11);
	`)

	// Seed orders for user 501 in production:
	// 1001: single coin pack (Stripe, EUR presentment, completed)
	// 1002: whole book (PayPal, partially refunded $5.00 of $14.99)
	// 1003: subscription initial (PayPal, completed)
	// 1004: subscription renewal (PayPal, completed, tied timestamp with 1003 to test cursor tie-break)
	// 1005: under_review (paid, unfulfilled, reconciliation_note set)
	// 1006: confirming (paid, unfulfilled, no note)
	// 1007: refunded (full refund)
	// 1008: revoked (dispute blocked)
	// 1014: refund_pending via active 'review' refund command
	// Excluded orders:
	// 1009: abandoned Pending draft (paid_at NULL, no receipt)
	// 1010: unpaid Failed draft
	// 1011: sandbox order (wrong environment)
	// 1012: mock order
	// 1013: user 502 order (other user)
	exec(`
		INSERT INTO recharge_orders(
			id, user_id, external_ref_id, amount_cents, currency, coins, bonus_coins_credited,
			payment_method, status, order_type, subscription_id, novel_id,
			product_snapshot, checkout_environment, created_at, paid_at, fulfilled_at,
			paid_period_start, paid_period_end, reconciliation_note
		) VALUES
			(1001, 501, 'cs_live_1', 999, 'USD', 500, 100, 'stripe', 'Success', 'single', NULL, NULL,
			 '{"display_alias":"500 Coins Pack","name":"500 Coins"}'::jsonb, 'production',
			 '2026-11-28 12:00:00+00', '2026-11-28 12:00:10+00', '2026-11-28 12:00:11+00', NULL, NULL, NULL),
			(1002, 501, 'PP-ORD-2', 1499, 'USD', 0, 0, 'paypal', 'Success', 'whole_book', NULL, 901,
			 '{"name":"Whole Book"}'::jsonb, 'production',
			 '2026-11-27 12:00:00+00', '2026-11-27 12:00:10+00', '2026-11-27 12:00:11+00', NULL, NULL, NULL),
			(1003, 501, 'PP-SALE-1', 999, 'USD', 0, 0, 'paypal', 'Success', 'subscription', 'I-LIVE-SUB-1', NULL,
			 '{"display_alias":"Weekly Unlimited","vip_cycle":"week"}'::jsonb, 'production',
			 '2026-11-26 12:00:00+00', '2026-11-26 12:00:01+00', '2026-11-26 12:00:02+00',
			 '2026-11-26 12:00:01+00', '2026-12-03 12:00:01+00', NULL),
			(1004, 501, 'PP-SALE-2', 999, 'USD', 0, 0, 'paypal', 'Success', 'subscription', 'I-LIVE-SUB-1', NULL,
			 '{"display_alias":"Weekly Unlimited","vip_cycle":"week"}'::jsonb, 'production',
			 '2026-11-26 12:00:00+00', '2026-11-26 12:05:00+00', '2026-11-26 12:05:01+00',
			 '2026-12-03 12:00:01+00', '2026-12-10 12:00:01+00', NULL),
			(1005, 501, 'PP-ORD-5', 1999, 'USD', 1000, 200, 'paypal', 'Paid', 'single', NULL, NULL,
			 '{"name":"1000 Coins"}'::jsonb, 'production',
			 '2026-11-25 12:00:00+00', '2026-11-25 12:00:05+00', NULL, NULL, NULL, 'manual review required'),
			(1006, 501, 'PP-ORD-6', 499, 'USD', 200, 0, 'paypal', 'Paid', 'single', NULL, NULL,
			 '{}'::jsonb, 'production',
			 '2026-11-24 12:00:00+00', '2026-11-24 12:00:05+00', NULL, NULL, NULL, NULL),
			(1007, 501, 'PP-ORD-7', 999, 'USD', 500, 0, 'paypal', 'Refunded', 'single', NULL, NULL,
			 '{"name":"Refunded Pack"}'::jsonb, 'production',
			 '2026-11-23 12:00:00+00', '2026-11-23 12:00:05+00', '2026-11-23 12:00:06+00', NULL, NULL, NULL),
			(1008, 501, 'cs_live_8', 999, 'USD', 500, 0, 'stripe', 'Success', 'single', NULL, NULL,
			 '{"name":"Disputed Pack"}'::jsonb, 'production',
			 '2026-11-22 12:00:00+00', '2026-11-22 12:00:05+00', '2026-11-22 12:00:06+00', NULL, NULL, NULL),
			(1014, 501, 'cs_live_14', 999, 'USD', 500, 0, 'stripe', 'Success', 'single', NULL, NULL,
			 '{"name":"Refund Review Pack"}'::jsonb, 'production',
			 '2026-11-21 12:00:00+00', '2026-11-21 12:00:05+00', '2026-11-21 12:00:06+00', NULL, NULL, NULL),
			(1009, 501, NULL, 999, 'USD', 500, 0, 'paypal', 'Pending', 'single', NULL, NULL,
			 '{}'::jsonb, 'production', '2026-11-28 13:00:00+00', NULL, NULL, NULL, NULL, NULL),
			(1010, 501, NULL, 999, 'USD', 500, 0, 'paypal', 'Failed', 'single', NULL, NULL,
			 '{}'::jsonb, 'production', '2026-11-28 13:05:00+00', NULL, NULL, NULL, NULL, NULL),
			(1011, 501, 'cs_sb_11', 999, 'USD', 500, 0, 'stripe', 'Success', 'single', NULL, NULL,
			 '{}'::jsonb, 'sandbox', '2026-11-28 13:10:00+00', '2026-11-28 13:10:05+00', '2026-11-28 13:10:06+00', NULL, NULL, NULL),
			(1012, 501, 'mock_12', 999, 'USD', 500, 0, 'paypal', 'Success', 'single', NULL, NULL,
			 '{}'::jsonb, 'production', '2026-11-28 13:15:00+00', '2026-11-28 13:15:05+00', '2026-11-28 13:15:06+00', NULL, NULL, NULL),
			(1013, 502, 'PP-ORD-13', 999, 'USD', 500, 0, 'paypal', 'Success', 'single', NULL, NULL,
			 '{}'::jsonb, 'production', '2026-11-28 13:20:00+00', '2026-11-28 13:20:05+00', '2026-11-28 13:20:06+00', NULL, NULL, NULL);

		INSERT INTO third_party_payment_orders(
			id, order_id, payment_provider, external_order_id, capture_id, currency, gross_amount, status, account_id, object_type,
			presentment_currency, presentment_amount_minor
		) VALUES
			(2001, 1001, 'stripe', 'cs_live_1', 'ch_live_1', 'USD', 9.99, 'VERIFIED', 10, 'charge', 'eur', 920),
			(2002, 1002, 'paypal', 'PP-ORD-2', 'CAP-2', 'USD', 14.99, 'VERIFIED', 11, 'capture', NULL, NULL),
			(2014, 1014, 'stripe', 'cs_live_14', 'ch_live_14', 'USD', 9.99, 'VERIFIED', 10, 'charge', NULL, NULL);

		INSERT INTO paypal_refund_items(refund_id, capture_id, currency, amount_cents)
		VALUES ('REF-PARTIAL-2', 'CAP-2', 'USD', 500);

		INSERT INTO payment_entitlement_adjustments(order_id, blocked, charged_coins, bonus_coins, policy_evidence)
		VALUES (1008, true, 500, 0, 'chargeback lost');

		INSERT INTO payment_refund_commands(id, environment, actor_id, request_key, order_id, receipt_id, amount_minor, currency, status)
		VALUES ('11111111-1111-1111-1111-111111111114', 'live', 'admin-1', '22222222-2222-2222-2222-222222222214', 1014, 2014, 999, 'USD', 'review');
	`)

	svc := NewService(NewDBRepository(nil), nil, payment.Adapters{})

	// Paginate 3 at a time and collect all visible order IDs
	var gotIDs []int64
	byID := make(map[int64]TopUpHistoryItem)
	cursor := ""
	for pageNum := 0; pageNum < 5; pageNum++ {
		page, err := svc.ListTopUpHistory(ctx, 501, 3, cursor, "")
		if err != nil {
			t.Fatalf("page %d error: %v", pageNum, err)
		}
		for _, item := range page.Items {
			gotIDs = append(gotIDs, item.OrderID)
			byID[item.OrderID] = item
		}
		cursor = page.NextCursor
		if cursor == "" {
			break
		}
	}

	wantIDs := []int64{1001, 1002, 1004, 1003, 1005, 1006, 1007, 1008, 1014}
	if !slices.Equal(gotIDs, wantIDs) {
		t.Fatalf("order IDs = %v, want %v", gotIDs, wantIDs)
	}

	// Verify Stripe local presentment currency + USD catalog amount
	o1 := byID[1001]
	if o1.OrderNo != "SN-1001" || o1.DisplayStatus != TopUpStatusCompleted || o1.Provider != "stripe" {
		t.Fatalf("order 1001 mismatch: %+v", o1)
	}
	if o1.PresentmentCurrency != "EUR" || o1.PresentmentAmountMinor == nil || *o1.PresentmentAmountMinor != 920 || o1.AmountMinor != 999 {
		t.Fatalf("order 1001 currency mismatch: %+v", o1)
	}

	// Verify whole book + partial refund + novel title
	o2 := byID[1002]
	if o2.DisplayStatus != TopUpStatusPartiallyRefunded || o2.RefundedMinor != 500 || o2.Title != "Starlight Heir" || o2.NovelID == nil || *o2.NovelID != 901 {
		t.Fatalf("order 1002 mismatch: %+v", o2)
	}

	// Verify subscription initial vs renewal
	if byID[1003].BillingKind != TopUpBillingInitial || byID[1004].BillingKind != TopUpBillingRenewal || byID[1004].Cycle != "week" {
		t.Fatalf("subscription billing kinds mismatch: 1003=%+v 1004=%+v", byID[1003], byID[1004])
	}

	// Verify remaining status mappings (including active 'review' refund command)
	if byID[1005].DisplayStatus != TopUpStatusUnderReview ||
		byID[1006].DisplayStatus != TopUpStatusConfirming ||
		byID[1007].DisplayStatus != TopUpStatusRefunded ||
		byID[1008].DisplayStatus != TopUpStatusRevoked ||
		byID[1014].DisplayStatus != TopUpStatusRefundPending {
		t.Fatalf("statuses mismatch: 1005=%s 1006=%s 1007=%s 1008=%s 1014=%s",
			byID[1005].DisplayStatus, byID[1006].DisplayStatus, byID[1007].DisplayStatus, byID[1008].DisplayStatus, byID[1014].DisplayStatus)
	}

	// Verify type filter
	subPage, err := svc.ListTopUpHistory(ctx, 501, 10, "", "subscription")
	if err != nil {
		t.Fatal(err)
	}
	if len(subPage.Items) != 2 || subPage.Items[0].OrderID != 1004 || subPage.Items[1].OrderID != 1003 {
		t.Fatalf("subscription filter mismatch: %+v", subPage.Items)
	}
}
