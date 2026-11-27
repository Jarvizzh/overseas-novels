package application

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"reader-backend/internal/config"
	"reader-backend/internal/conversion"
	"reader-backend/internal/model"
	"reader-backend/internal/payment"
)

func newAuthenticatedSubscriptionRouter(h *Handler, userID int64) *gin.Engine {
	router := gin.New()
	group := router.Group("/api/v1/wallet", func(c *gin.Context) {
		c.Set("user_id", userID)
		c.Next()
	})
	group.GET("/subscription/status", h.GetSubscriptionStatus)
	group.POST("/subscription/cancel", h.CancelSubscription)
	group.POST("/subscription/stripe/portal", h.OpenStripeVIPPortal)
	return router
}

type fakeSubscriptionRepo struct {
	Repository
	subsByUser   map[int64][]model.UserSubscription
	subsByID     map[string]*model.UserSubscription
	cancelCalls  int
	lastCancelID string
}

func (f *fakeSubscriptionRepo) ListManagedSubscriptions(_ context.Context, userID int64) ([]model.UserSubscription, error) {
	list := f.subsByUser[userID]
	out := make([]model.UserSubscription, len(list))
	copy(out, list)
	return out, nil
}

func (f *fakeSubscriptionRepo) GetManagedSubscriptionByUserID(ctx context.Context, userID int64) (*model.UserSubscription, error) {
	list, err := f.ListManagedSubscriptions(ctx, userID)
	if err != nil || len(list) == 0 {
		return nil, err
	}
	return &list[0], nil
}

func (f *fakeSubscriptionRepo) GetSubscriptionByID(_ context.Context, subID string) (*model.UserSubscription, error) {
	sub, ok := f.subsByID[subID]
	if !ok {
		return nil, nil
	}
	cp := *sub
	return &cp, nil
}

func (f *fakeSubscriptionRepo) enqueuePayPalCancellation(_ context.Context, id string, userID int64, _ string) error {
	f.cancelCalls++
	f.lastCancelID = id
	if sub, ok := f.subsByID[id]; ok && sub.UserID == userID && sub.Status != "CANCELLED" && sub.Status != "EXPIRED" {
		sub.Status = "CANCEL_PENDING"
	}
	return nil
}

type fakeSubscriptionAccess struct {
	vipByUser map[int64]bool
}

func (f *fakeSubscriptionAccess) HasActiveSubscription(_ context.Context, userID int64) (bool, error) {
	return f.vipByUser[userID], nil
}

func TestSubscriptionStatusAndCapabilitiesMatrix(t *testing.T) {
	gin.SetMode(gin.TestMode)
	prevCfg := config.AppConfig
	t.Cleanup(func() { config.AppConfig = prevCfg })
	config.AppConfig = &config.Config{
		JWTSecret:        "sub-mgmt-test-secret",
		StripeMoRLinkURL: "https://app.link.com/login",
	}

	now := time.Now().UTC().Truncate(time.Second)
	future := now.Add(14 * 24 * time.Hour)
	past := now.Add(-2 * 24 * time.Hour)
	failedLater := now.Add(-1 * time.Hour)
	paidEarlier := now.Add(-10 * 24 * time.Hour)

	cases := []struct {
		name               string
		subs               []model.UserSubscription
		vip                bool
		wantTopStatus      string
		wantProvider       string
		wantSalesMode      string
		wantPlanName       string
		wantNormStatus     string
		wantHasNextBilling bool
		wantCanCancel      bool
		wantCancelPending  bool
		wantCanPortal      bool
		wantCanMoRLink     bool
	}{
		{
			name:          "no subscription",
			subs:          []model.UserSubscription{},
			vip:           false,
			wantTopStatus: SubscriptionStatusNone,
		},
		{
			name: "paypal active",
			subs: []model.UserSubscription{{
				ID: 1, UserID: 10, SubscriptionID: "I-PP-ACTIVE", Status: "ACTIVE",
				Cycle: "month", PriceCents: 1999, Currency: "USD", PaymentMethod: "paypal",
				PlanName: "StarNovel Monthly VIP", CurrentPeriodEnd: &future, NextBillingTime: &future,
				LastPaymentTime: &paidEarlier,
			}},
			vip:                true,
			wantTopStatus:      SubscriptionStatusActive,
			wantProvider:       "paypal",
			wantSalesMode:      "",
			wantPlanName:       "StarNovel Monthly VIP",
			wantNormStatus:     SubscriptionStatusActive,
			wantHasNextBilling: true,
			wantCanCancel:      true,
			wantCancelPending:  false,
			wantCanPortal:      false,
			wantCanMoRLink:     false,
		},
		{
			name: "paypal cancel pending hides next billing and disables repeat cancel",
			subs: []model.UserSubscription{{
				ID: 2, UserID: 10, SubscriptionID: "I-PP-PEND", Status: "CANCEL_PENDING",
				Cycle: "week", PriceCents: 999, Currency: "USD", PaymentMethod: "paypal",
				CurrentPeriodEnd: &future, NextBillingTime: &future,
			}},
			vip:                true,
			wantTopStatus:      SubscriptionStatusCancelPending,
			wantProvider:       "paypal",
			wantPlanName:       "VIP Weekly Pass",
			wantNormStatus:     SubscriptionStatusCancelPending,
			wantHasNextBilling: false,
			wantCanCancel:      false,
			wantCancelPending:  true,
			wantCanPortal:      false,
			wantCanMoRLink:     false,
		},
		{
			name: "paypal canceled with remaining paid period",
			subs: []model.UserSubscription{{
				ID: 3, UserID: 10, SubscriptionID: "I-PP-CANC", Status: "CANCELLED",
				Cycle: "month", PriceCents: 1999, Currency: "USD", PaymentMethod: "paypal",
				CurrentPeriodEnd: &future, CancelledAt: &past,
			}},
			vip:                true,
			wantTopStatus:      SubscriptionStatusCanceledWithPaidPeriod,
			wantProvider:       "paypal",
			wantPlanName:       "VIP Monthly Pass",
			wantNormStatus:     SubscriptionStatusCanceledWithPaidPeriod,
			wantHasNextBilling: false,
			wantCanCancel:      false,
			wantCancelPending:  false,
			wantCanPortal:      false,
			wantCanMoRLink:     false,
		},
		{
			name: "paypal renewal failed while still active",
			subs: []model.UserSubscription{{
				ID: 4, UserID: 10, SubscriptionID: "I-PP-FAIL", Status: "ACTIVE",
				Cycle: "month", PriceCents: 1999, Currency: "USD", PaymentMethod: "paypal",
				CurrentPeriodEnd: &future, NextBillingTime: &future,
				LastPaymentTime: &paidEarlier, RenewalFailedAt: &failedLater,
			}},
			vip:                true,
			wantTopStatus:      SubscriptionStatusRenewalFailed,
			wantProvider:       "paypal",
			wantPlanName:       "VIP Monthly Pass",
			wantNormStatus:     SubscriptionStatusRenewalFailed,
			wantHasNextBilling: true,
			wantCanCancel:      true,
			wantCancelPending:  false,
			wantCanPortal:      false,
			wantCanMoRLink:     false,
		},
		{
			name: "stripe direct active authorizes billing portal only",
			subs: []model.UserSubscription{{
				ID: 5, UserID: 10, SubscriptionID: "sub_direct_1", Status: "ACTIVE",
				Cycle: "month", PriceCents: 1999, Currency: "USD", PaymentMethod: "stripe",
				SalesMode: payment.StripeSaleDirect, Management: "stripe_portal",
				PlanName: "VIP Direct Monthly", CurrentPeriodEnd: &future, NextBillingTime: &future,
			}},
			vip:                true,
			wantTopStatus:      SubscriptionStatusActive,
			wantProvider:       "stripe",
			wantSalesMode:      payment.StripeSaleDirect,
			wantPlanName:       "VIP Direct Monthly",
			wantNormStatus:     SubscriptionStatusActive,
			wantHasNextBilling: true,
			wantCanCancel:      true,
			wantCancelPending:  false,
			wantCanPortal:      true,
			wantCanMoRLink:     false,
		},
		{
			name: "stripe mor active authorizes link portal only",
			subs: []model.UserSubscription{{
				ID: 6, UserID: 10, SubscriptionID: "sub_mor_1", Status: "ACTIVE",
				Cycle: "week", PriceCents: 999, Currency: "USD", PaymentMethod: "stripe",
				SalesMode: payment.StripeSaleMoR, Management: "stripe_link",
				CurrentPeriodEnd: &future, NextBillingTime: &future,
			}},
			vip:                true,
			wantTopStatus:      SubscriptionStatusActive,
			wantProvider:       "stripe",
			wantSalesMode:      payment.StripeSaleMoR,
			wantPlanName:       "VIP Weekly Pass",
			wantNormStatus:     SubscriptionStatusActive,
			wantHasNextBilling: true,
			wantCanCancel:      true,
			wantCancelPending:  false,
			wantCanPortal:      false,
			wantCanMoRLink:     true,
		},
		{
			name: "stripe mor suspended shows renewal_failed and still allows cancel and link portal",
			subs: []model.UserSubscription{{
				ID: 7, UserID: 10, SubscriptionID: "sub_mor_fail", Status: "SUSPENDED",
				Cycle: "month", PriceCents: 1999, Currency: "USD", PaymentMethod: "stripe",
				SalesMode: payment.StripeSaleMoR, Management: "stripe_link",
				CurrentPeriodEnd: &future, NextBillingTime: &future,
			}},
			vip:                true,
			wantTopStatus:      SubscriptionStatusRenewalFailed,
			wantProvider:       "stripe",
			wantSalesMode:      payment.StripeSaleMoR,
			wantPlanName:       "VIP Monthly Pass",
			wantNormStatus:     SubscriptionStatusRenewalFailed,
			wantHasNextBilling: true,
			wantCanCancel:      true,
			wantCancelPending:  false,
			wantCanPortal:      false,
			wantCanMoRLink:     true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeSubscriptionRepo{subsByUser: map[int64][]model.UserSubscription{10: tc.subs}}
			access := &fakeSubscriptionAccess{vipByUser: map[int64]bool{10: tc.vip}}
			svc := NewService(repo, access, payment.Adapters{})
			router := newAuthenticatedSubscriptionRouter(NewHandler(svc), 10)

			req := httptest.NewRequest(http.MethodGet, "/api/v1/wallet/subscription/status", nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if cc := w.Header().Get("Cache-Control"); cc != "private, no-store" {
				t.Fatalf("Cache-Control=%q, want private, no-store", cc)
			}

			var body struct {
				IsVIP         bool                     `json:"is_vip"`
				Status        string                   `json:"status"`
				Subscription  *model.UserSubscription  `json:"subscription"`
				Subscriptions []model.UserSubscription `json:"subscriptions"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.IsVIP != tc.vip {
				t.Fatalf("is_vip=%v want %v", body.IsVIP, tc.vip)
			}
			if body.Status != tc.wantTopStatus {
				t.Fatalf("status=%q want %q", body.Status, tc.wantTopStatus)
			}
			if len(tc.subs) == 0 {
				if body.Subscription != nil || len(body.Subscriptions) != 0 {
					t.Fatalf("expected empty subscription, got %+v", body)
				}
				return
			}
			got := body.Subscription
			if got == nil {
				t.Fatalf("expected primary subscription")
			}
			if got.Provider != tc.wantProvider || got.SalesMode != tc.wantSalesMode || got.PlanName != tc.wantPlanName || got.NormalizedStatus != tc.wantNormStatus {
				t.Fatalf("got provider=%q sales_mode=%q plan_name=%q norm=%q; want %q %q %q %q",
					got.Provider, got.SalesMode, got.PlanName, got.NormalizedStatus,
					tc.wantProvider, tc.wantSalesMode, tc.wantPlanName, tc.wantNormStatus)
			}
			if (got.NextBillingAt != nil) != tc.wantHasNextBilling {
				t.Fatalf("next_billing_at=%v want present=%v", got.NextBillingAt, tc.wantHasNextBilling)
			}
			if got.PaidThrough == nil || !got.PaidThrough.Equal(*got.CurrentPeriodEnd) {
				t.Fatalf("paid_through=%v want %v", got.PaidThrough, got.CurrentPeriodEnd)
			}
			if got.Capabilities == nil {
				t.Fatalf("expected capabilities")
			}
			if got.Capabilities.CanCancelAutoRenew != tc.wantCanCancel ||
				got.Capabilities.CancelPending != tc.wantCancelPending ||
				got.Capabilities.CanOpenBillingPortal != tc.wantCanPortal ||
				got.Capabilities.CanOpenMoRLinkPortal != tc.wantCanMoRLink {
				t.Fatalf("capabilities=%+v want cancel=%v pending=%v portal=%v mor=%v",
					got.Capabilities, tc.wantCanCancel, tc.wantCancelPending, tc.wantCanPortal, tc.wantCanMoRLink)
			}
			if tc.wantCanPortal && got.Capabilities.BillingPortalEndpoint != stripeBillingPortalEndpoint {
				t.Fatalf("billing_portal_endpoint=%q want %q", got.Capabilities.BillingPortalEndpoint, stripeBillingPortalEndpoint)
			}
			if tc.wantCanMoRLink && got.Capabilities.MoRLinkURL != "https://app.link.com/login" {
				t.Fatalf("mor_link_url=%q", got.Capabilities.MoRLinkURL)
			}
		})
	}
}

func TestStripeMoRLinkURLRejectsNonHTTPS(t *testing.T) {
	prevCfg := config.AppConfig
	t.Cleanup(func() { config.AppConfig = prevCfg })
	config.AppConfig = &config.Config{StripeMoRLinkURL: "javascript:alert(1)"}

	if got := resolveStripeMoRLinkURL(); got != "" {
		t.Fatalf("resolveStripeMoRLinkURL() = %q, want empty for non-https URL", got)
	}
	sub := enrichManagedSubscription(model.UserSubscription{
		PaymentMethod: "stripe",
		SalesMode:     payment.StripeSaleMoR,
		Status:        "ACTIVE",
	}, time.Now().UTC(), resolveStripeMoRLinkURL())
	if sub.Capabilities.CanOpenMoRLinkPortal || sub.Capabilities.MoRLinkURL != "" {
		t.Fatalf("expected MoR link capability disabled for invalid URL, got %+v", sub.Capabilities)
	}
}

func TestCancelSubscriptionIdempotencyAndOwnership(t *testing.T) {
	gin.SetMode(gin.TestMode)
	prevCfg := config.AppConfig
	t.Cleanup(func() { config.AppConfig = prevCfg })
	config.AppConfig = &config.Config{JWTSecret: "cancel-test-secret"}

	env := conversion.Environment()
	paidThrough := time.Now().UTC().Add(10 * 24 * time.Hour).Truncate(time.Second)

	repo := &fakeSubscriptionRepo{
		subsByID: map[string]*model.UserSubscription{
			"I-OWNED-ACTIVE": {
				ID: 1, UserID: 100, SubscriptionID: "I-OWNED-ACTIVE", Status: "ACTIVE",
				PaymentMethod: "paypal", Environment: env, CurrentPeriodEnd: &paidThrough,
			},
			"I-OWNED-CANCELED": {
				ID: 2, UserID: 100, SubscriptionID: "I-OWNED-CANCELED", Status: "CANCELLED",
				PaymentMethod: "paypal", Environment: env, CurrentPeriodEnd: &paidThrough,
			},
			"I-OTHER-USER": {
				ID: 3, UserID: 200, SubscriptionID: "I-OTHER-USER", Status: "ACTIVE",
				PaymentMethod: "paypal", Environment: env, CurrentPeriodEnd: &paidThrough,
			},
			"I-WRONG-ENV": {
				ID: 4, UserID: 100, SubscriptionID: "I-WRONG-ENV", Status: "ACTIVE",
				PaymentMethod: "paypal", Environment: "other-env", CurrentPeriodEnd: &paidThrough,
			},
		},
	}
	svc := NewService(repo, &fakeSubscriptionAccess{}, payment.Adapters{})
	router := newAuthenticatedSubscriptionRouter(NewHandler(svc), 100)

	postCancel := func(subID string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/wallet/subscription/cancel",
			strings.NewReader(`{"subscription_id":"`+subID+`","reason":"User requested cancellation"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}

	// First cancel: transitions to cancel_pending.
	w1 := postCancel("I-OWNED-ACTIVE")
	if w1.Code != http.StatusAccepted {
		t.Fatalf("first cancel status=%d body=%s", w1.Code, w1.Body.String())
	}
	var res1 SubscriptionCancelResult
	if err := json.Unmarshal(w1.Body.Bytes(), &res1); err != nil {
		t.Fatal(err)
	}
	if res1.NormalizedStatus != SubscriptionStatusCancelPending || res1.AlreadyRequested || res1.PaidThrough == nil || !res1.PaidThrough.Equal(paidThrough) {
		t.Fatalf("unexpected first cancel result: %+v", res1)
	}
	if repo.cancelCalls != 1 {
		t.Fatalf("expected 1 enqueue call, got %d", repo.cancelCalls)
	}

	// Second cancel (repeated click): idempotent, does not re-enqueue.
	w2 := postCancel("I-OWNED-ACTIVE")
	if w2.Code != http.StatusAccepted {
		t.Fatalf("second cancel status=%d body=%s", w2.Code, w2.Body.String())
	}
	var res2 SubscriptionCancelResult
	if err := json.Unmarshal(w2.Body.Bytes(), &res2); err != nil {
		t.Fatal(err)
	}
	if res2.NormalizedStatus != SubscriptionStatusCancelPending || !res2.AlreadyRequested || res2.PaidThrough == nil || !res2.PaidThrough.Equal(paidThrough) {
		t.Fatalf("unexpected repeated cancel result: %+v", res2)
	}
	if repo.cancelCalls != 1 {
		t.Fatalf("expected repeated cancel not to re-enqueue, got %d calls", repo.cancelCalls)
	}

	// Already canceled with paid period: distinguishes stopped vs pending confirmation.
	w3 := postCancel("I-OWNED-CANCELED")
	if w3.Code != http.StatusAccepted {
		t.Fatalf("already-canceled status=%d body=%s", w3.Code, w3.Body.String())
	}
	var res3 SubscriptionCancelResult
	if err := json.Unmarshal(w3.Body.Bytes(), &res3); err != nil {
		t.Fatal(err)
	}
	if res3.NormalizedStatus != SubscriptionStatusCanceledWithPaidPeriod || !res3.AlreadyRequested || res3.PaidThrough == nil || !res3.PaidThrough.Equal(paidThrough) {
		t.Fatalf("unexpected already-canceled result: %+v", res3)
	}

	// Cross-user rejection.
	wOther := postCancel("I-OTHER-USER")
	if wOther.Code != http.StatusNotFound {
		t.Fatalf("cross-user cancel status=%d want 404", wOther.Code)
	}
	if repo.subsByID["I-OTHER-USER"].Status != "ACTIVE" {
		t.Fatalf("cross-user subscription was mutated")
	}

	// Cross-environment rejection.
	wWrongEnv := postCancel("I-WRONG-ENV")
	if wWrongEnv.Code != http.StatusNotFound {
		t.Fatalf("cross-env cancel status=%d want 404", wWrongEnv.Code)
	}
}

type stubPortalService struct {
	Service
	lastUserID     int64
	lastSubID      string
	lastOrigin     string
	lastReturnPath string
	portalURL      string
	err            error
}

func (s *stubPortalService) OpenStripeVIPPortal(_ context.Context, userID int64, subID, origin, returnPath string) (string, error) {
	s.lastUserID = userID
	s.lastSubID = subID
	s.lastOrigin = origin
	s.lastReturnPath = returnPath
	return s.portalURL, s.err
}

func TestOpenStripeVIPPortalHandlerAndReturnPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	prevCfg := config.AppConfig
	t.Cleanup(func() { config.AppConfig = prevCfg })
	config.AppConfig = &config.Config{JWTSecret: "portal-test-secret"}

	if got := normalizePortalReturnPath("/subscription"); got != "/subscription" {
		t.Fatalf("normalizePortalReturnPath(/subscription) = %q", got)
	}
	if got := normalizePortalReturnPath("https://evil.example/phish"); got != "/recharge" {
		t.Fatalf("normalizePortalReturnPath(evil) = %q, want /recharge", got)
	}
	if got := normalizePortalReturnPath(""); got != "/recharge" {
		t.Fatalf("normalizePortalReturnPath(empty) = %q, want /recharge", got)
	}

	t.Run("direct portal authorized", func(t *testing.T) {
		stub := &stubPortalService{portalURL: "https://billing.stripe.com/session/test_123"}
		router := newAuthenticatedSubscriptionRouter(NewHandler(stub), 42)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/wallet/subscription/stripe/portal",
			strings.NewReader(`{"subscription_id":"sub_direct_42","return_path":"/subscription"}`))
		req.Header.Set("Origin", "https://h5.starsfic.com")
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
		if stub.lastUserID != 42 || stub.lastSubID != "sub_direct_42" || stub.lastReturnPath != "/subscription" {
			t.Fatalf("unexpected service args: %+v", stub)
		}
		if !strings.Contains(w.Body.String(), `"portal_url":"https://billing.stripe.com/session/test_123"`) {
			t.Fatalf("unexpected body: %s", w.Body.String())
		}
	})

	t.Run("cross-user or MoR rejected with 404", func(t *testing.T) {
		stub := &stubPortalService{err: ErrCheckoutUnavailable}
		router := newAuthenticatedSubscriptionRouter(NewHandler(stub), 42)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/wallet/subscription/stripe/portal",
			strings.NewReader(`{"subscription_id":"sub_other_99"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		if w.Code != http.StatusNotFound {
			t.Fatalf("status=%d want 404 body=%s", w.Code, w.Body.String())
		}
	})
}
