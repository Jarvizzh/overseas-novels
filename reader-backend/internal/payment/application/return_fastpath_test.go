package application

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"reader-backend/internal/config"
	"reader-backend/internal/payment"
	"reader-backend/internal/payment/paypal"
)

type stubReturnRepo struct {
	Repository
	intent          PurchaseIntent
	order           checkoutOrder
	settled         bool
	settleCtxErr    error
	recordedOutcome captureOutcome
}

func (r *stubReturnRepo) purchaseByReference(_ context.Context, _ int64, _, _ string) (*PurchaseIntent, error) {
	out := r.intent
	if r.settled {
		out.Status = "completed"
	}
	return &out, nil
}

func (r *stubReturnRepo) findCheckout(_ context.Context, _ string) (*checkoutOrder, error) {
	o := r.order
	return &o, nil
}

func (r *stubReturnRepo) matchPayPalAttempt(_ context.Context, _ string, _ payment.WebhookAccount) (bool, error) {
	return true, nil
}

func (r *stubReturnRepo) leaseCheckoutCapture(_ context.Context, _ string) (bool, error) {
	return true, nil
}

func (r *stubReturnRepo) settleCheckout(ctx context.Context, _ string, _ *payment.OrderCapture) (*CheckoutResult, error) {
	r.settleCtxErr = ctx.Err()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	r.settled = true
	return &CheckoutResult{settled: true, Coins: r.order.coins + r.order.bonus, OrderType: r.order.orderType}, nil
}

func (r *stubReturnRepo) recordCaptureOutcome(_ context.Context, _ int64, _ string, code captureOutcome) error {
	r.recordedOutcome = code
	return nil
}

type stubReturnPayPal struct {
	paypalCheckout
	account      payment.WebhookAccount
	getErr       error
	getCapture   *payment.OrderCapture
	captured     *payment.OrderCapture
	onCapture    func()
	onCheckout   func()
	getCalls     int
	captureCalls int
}

func (p *stubReturnPayPal) Provider() string {
	return payment.ProviderPayPal
}

func (p *stubReturnPayPal) CheckoutAccount(_ context.Context) (payment.WebhookAccount, error) {
	if p.onCheckout != nil {
		p.onCheckout()
	}
	return p.account, nil
}

func (p *stubReturnPayPal) GetCapture(_ context.Context, _ string) (*payment.OrderCapture, error) {
	p.getCalls++
	return p.getCapture, p.getErr
}

func (p *stubReturnPayPal) CaptureOrder(_ context.Context, _ string, _ payment.WebhookAccount) (*payment.OrderCapture, error) {
	p.captureCalls++
	if p.onCapture != nil {
		p.onCapture()
	}
	return p.captured, nil
}

func TestGetPurchaseCapturesImmediatelyOnPayPalReturn(t *testing.T) {
	t.Setenv("CONVERSION_ENVIRONMENT", "sandbox")
	const intentID = "11111111-1111-4111-8111-111111111111"
	const paypalOrderID = "PAYPAL-ORDER-FAST"
	account := payment.WebhookAccount{AccountKey: "client-sbx", Environment: "sandbox"}

	repo := &stubReturnRepo{
		intent: PurchaseIntent{
			ID:              intentID,
			SlotID:          1,
			OrderType:       "single",
			AmountMinor:     999,
			Currency:        "USD",
			Coins:           999,
			Status:          "awaiting_approval",
			Provider:        payment.ProviderPayPal,
			ExternalOrderID: paypalOrderID,
		},
		order: checkoutOrder{
			id:             42,
			userID:         7,
			checkoutID:     intentID,
			status:         "Pending",
			orderType:      "single",
			amount:         999,
			currency:       "USD",
			coins:          999,
			managedAttempt: true,
			paymentMethod:  payment.ProviderPayPal,
		},
	}
	pp := &stubReturnPayPal{
		account: account,
		getErr:  paypal.ErrOrderApproved,
		getCapture: &payment.OrderCapture{
			Account:      account,
			OrderID:      paypalOrderID,
			CustomID:     intentID,
			GrossAmount:  "9.99",
			CurrencyCode: "USD",
		},
		captured: &payment.OrderCapture{
			Account:      account,
			OrderID:      paypalOrderID,
			CaptureID:    "CAP-FAST-1",
			CustomID:     intentID,
			GrossAmount:  "9.99",
			CurrencyCode: "USD",
			Status:       "COMPLETED",
			OccurredAt:   time.Now().UTC(),
		},
	}
	svc := &service{repo: repo, paypalClient: pp, returnProbe: &returnProbeLimiter{}}

	// Without PayPal return token (e.g. normal poll or cancel return), must not call PayPal.
	unchanged, err := svc.GetPurchase(t.Context(), 7, intentID, "")
	if err != nil {
		t.Fatalf("GetPurchase without token failed: %v", err)
	}
	if unchanged.Status != "awaiting_approval" || pp.getCalls != 0 || pp.captureCalls != 0 {
		t.Fatalf("expected read-only without token, got status=%s get=%d cap=%d", unchanged.Status, pp.getCalls, pp.captureCalls)
	}

	// With PayPal return token, must capture and return completed immediately.
	completed, err := svc.GetPurchase(t.Context(), 7, intentID, paypalOrderID)
	if err != nil {
		t.Fatalf("GetPurchase with token failed: %v", err)
	}
	if completed.Status != "completed" || pp.captureCalls != 1 || !repo.settled {
		t.Fatalf("expected immediate capture completion, got status=%s cap=%d settled=%v", completed.Status, pp.captureCalls, repo.settled)
	}
}

func TestGetPurchaseCreatedOrderThrottledAndClean(t *testing.T) {
	t.Setenv("CONVERSION_ENVIRONMENT", "sandbox")
	const intentID = "22222222-2222-4222-8222-222222222222"
	const paypalOrderID = "PAYPAL-ORDER-CREATED"
	account := payment.WebhookAccount{AccountKey: "client-sbx", Environment: "sandbox"}

	repo := &stubReturnRepo{
		intent: PurchaseIntent{
			ID:              intentID,
			SlotID:          1,
			OrderType:       "single",
			AmountMinor:     999,
			Currency:        "USD",
			Status:          "awaiting_approval",
			Provider:        payment.ProviderPayPal,
			ExternalOrderID: paypalOrderID,
		},
		order: checkoutOrder{
			id:             43,
			userID:         7,
			checkoutID:     intentID,
			status:         "Pending",
			orderType:      "single",
			amount:         999,
			currency:       "USD",
			managedAttempt: true,
			paymentMethod:  payment.ProviderPayPal,
		},
	}
	pp := &stubReturnPayPal{
		account: account,
		getErr:  paypal.ErrOrderCreated,
	}
	svc := &service{repo: repo, paypalClient: pp, returnProbe: &returnProbeLimiter{}}

	got, err := svc.GetPurchase(t.Context(), 7, intentID, paypalOrderID)
	if err != nil {
		t.Fatalf("GetPurchase failed: %v", err)
	}
	if got.Status != "awaiting_approval" || repo.recordedOutcome != "" || pp.getCalls != 1 || pp.captureCalls != 0 {
		t.Fatalf("CREATED order should stay awaiting_approval cleanly, got status=%s outcome=%q get=%d cap=%d",
			got.Status, repo.recordedOutcome, pp.getCalls, pp.captureCalls)
	}

	// Immediate follow-up poll within cooldown must read DB without hammering PayPal API.
	got2, err := svc.GetPurchase(t.Context(), 7, intentID, paypalOrderID)
	if err != nil {
		t.Fatalf("second GetPurchase failed: %v", err)
	}
	if got2.Status != "awaiting_approval" || pp.getCalls != 1 {
		t.Fatalf("expected cooldown to skip second PayPal probe, got status=%s get=%d", got2.Status, pp.getCalls)
	}
}

func TestGetPurchaseSkipsProbeWhenConfirmationCodePresent(t *testing.T) {
	t.Setenv("CONVERSION_ENVIRONMENT", "sandbox")
	const intentID = "33333333-3333-4333-8333-333333333333"
	const paypalOrderID = "PAYPAL-ORDER-DECLINED"
	account := payment.WebhookAccount{AccountKey: "client-sbx", Environment: "sandbox"}

	for _, code := range []captureOutcome{captureDeclined, captureBlocked, captureBuyerAction, captureLegacyBookReview, "payment_switch_pending"} {
		repo := &stubReturnRepo{
			intent: PurchaseIntent{
				ID:               intentID,
				SlotID:           1,
				OrderType:        "single",
				AmountMinor:      999,
				Currency:         "USD",
				Status:           "confirming",
				Provider:         payment.ProviderPayPal,
				ExternalOrderID:  paypalOrderID,
				ConfirmationCode: code,
			},
		}
		pp := &stubReturnPayPal{account: account}
		svc := &service{repo: repo, paypalClient: pp, returnProbe: &returnProbeLimiter{}}

		if _, err := svc.GetPurchase(t.Context(), 7, intentID, paypalOrderID); err != nil {
			t.Fatalf("code=%s GetPurchase failed: %v", code, err)
		}
		if pp.getCalls != 0 || pp.captureCalls != 0 {
			t.Fatalf("code=%s should not probe PayPal, got get=%d cap=%d", code, pp.getCalls, pp.captureCalls)
		}
	}
}

func TestGetPurchaseReprobesRecoverableConfirmationCodes(t *testing.T) {
	t.Setenv("CONVERSION_ENVIRONMENT", "sandbox")
	const intentID = "34343434-3434-4343-8343-343434343434"
	const paypalOrderID = "PAYPAL-ORDER-RECOVERABLE"
	account := payment.WebhookAccount{AccountKey: "client-sbx", Environment: "sandbox"}

	for _, code := range []captureOutcome{captureUnavailable, captureUnconfirmed} {
		repo := &stubReturnRepo{
			intent: PurchaseIntent{
				ID:               intentID,
				SlotID:           1,
				OrderType:        "single",
				AmountMinor:      999,
				Currency:         "USD",
				Coins:            999,
				Status:           "awaiting_approval",
				Provider:         payment.ProviderPayPal,
				ExternalOrderID:  paypalOrderID,
				ConfirmationCode: code,
			},
			order: checkoutOrder{
				id:             45,
				userID:         7,
				checkoutID:     intentID,
				status:         "Pending",
				orderType:      "single",
				amount:         999,
				currency:       "USD",
				coins:          999,
				managedAttempt: true,
				paymentMethod:  payment.ProviderPayPal,
			},
		}
		pp := &stubReturnPayPal{
			account: account,
			getErr:  paypal.ErrOrderApproved,
			getCapture: &payment.OrderCapture{
				Account:      account,
				OrderID:      paypalOrderID,
				CustomID:     intentID,
				GrossAmount:  "9.99",
				CurrencyCode: "USD",
			},
			captured: &payment.OrderCapture{
				Account:      account,
				OrderID:      paypalOrderID,
				CaptureID:    "CAP-RECOVER-1",
				CustomID:     intentID,
				GrossAmount:  "9.99",
				CurrencyCode: "USD",
				Status:       "COMPLETED",
				OccurredAt:   time.Now().UTC(),
			},
		}
		svc := &service{repo: repo, paypalClient: pp, returnProbe: &returnProbeLimiter{}}

		got, err := svc.GetPurchase(t.Context(), 7, intentID, paypalOrderID)
		if err != nil {
			t.Fatalf("code=%s GetPurchase failed: %v", code, err)
		}
		if got.Status != "completed" || pp.getCalls != 1 || pp.captureCalls != 1 || !repo.settled {
			t.Fatalf("code=%s expected recoverable re-probe and capture, got status=%s get=%d cap=%d settled=%v",
				code, got.Status, pp.getCalls, pp.captureCalls, repo.settled)
		}
	}
}

func TestCompletePayPalOrderSettlesEvenAfterCallerCancel(t *testing.T) {
	t.Setenv("CONVERSION_ENVIRONMENT", "sandbox")
	const intentID = "44444444-4444-4444-8444-444444444444"
	const paypalOrderID = "PAYPAL-ORDER-CANCEL-MID"
	account := payment.WebhookAccount{AccountKey: "client-sbx", Environment: "sandbox"}

	reqCtx, cancelReq := context.WithCancel(t.Context())
	defer cancelReq()

	repo := &stubReturnRepo{
		order: checkoutOrder{
			id:             44,
			userID:         7,
			checkoutID:     intentID,
			status:         "Pending",
			orderType:      "single",
			amount:         999,
			currency:       "USD",
			coins:          999,
			managedAttempt: true,
			paymentMethod:  payment.ProviderPayPal,
		},
	}
	pp := &stubReturnPayPal{
		account: account,
		getErr:  paypal.ErrOrderApproved,
		getCapture: &payment.OrderCapture{
			Account:      account,
			OrderID:      paypalOrderID,
			CustomID:     intentID,
			GrossAmount:  "9.99",
			CurrencyCode: "USD",
		},
		captured: &payment.OrderCapture{
			Account:      account,
			OrderID:      paypalOrderID,
			CaptureID:    "CAP-CANCEL-1",
			CustomID:     intentID,
			GrossAmount:  "9.99",
			CurrencyCode: "USD",
			Status:       "COMPLETED",
			OccurredAt:   time.Now().UTC(),
		},
		onCapture: cancelReq, // Simulate client disconnecting right after PayPal confirms capture.
	}
	svc := &service{repo: repo, paypalClient: pp, returnProbe: &returnProbeLimiter{}}

	res, err := svc.completePayPalOrder(reqCtx, 7, paypalOrderID, true)
	if err != nil || res == nil || !repo.settled || repo.settleCtxErr != nil {
		t.Fatalf("expected settlement to commit despite caller cancel, got res=%+v err=%v settled=%v settleCtxErr=%v",
			res, err, repo.settled, repo.settleCtxErr)
	}
}

func TestRunReconciliationWakesImmediatelyOnWebhookSignal(t *testing.T) {
	t.Setenv("CONVERSION_ENVIRONMENT", "sandbox")
	prevCfg := config.AppConfig
	config.AppConfig = &config.Config{}
	defer func() { config.AppConfig = prevCfg }()

	woke := make(chan struct{}, 1)
	var hits atomic.Int32
	pp := &stubReturnPayPal{
		// Non-matching environment causes reconcilePayPalEvents to return right after
		// CheckoutAccount without touching DB, proving RunReconciliation woke up immediately.
		account: payment.WebhookAccount{AccountKey: "wake-client", Environment: "live"},
		onCheckout: func() {
			if hits.Add(1) == 1 {
				notifyWorker(woke)
			}
		},
	}
	svc := NewService(nil, nil, payment.NewAdapters(pp)).(*service)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		svc.RunReconciliation(ctx)
	}()

	notifyWorker(svc.paypalEventsWake)
	select {
	case <-woke:
	case <-time.After(2 * time.Second):
		cancel()
		<-done
		t.Fatal("RunReconciliation did not wake immediately on paypalEventsWake")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunReconciliation did not stop cleanly on cancel")
	}

	notifyWorker(nil)
	if errors.Is(paypal.ErrOrderCreated, paypal.ErrOrderApproved) {
		t.Fatal("ErrOrderCreated must remain distinct from ErrOrderApproved")
	}
}
