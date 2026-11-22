package application

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"reader-backend/internal/novel"
	"reader-backend/internal/payment"
	"reader-backend/internal/payment/paypal"
	"reader-backend/internal/testdb"
	"reader-backend/internal/wallet"
)

func TestWholeBookEnvironmentScope(t *testing.T) {
	t.Setenv("CONVERSION_ENVIRONMENT", "production")
	pool := testdb.Open(t)
	ctx := t.Context()
	exec := func(sql string) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO users(id) VALUES(1),(2),(3),(4);
		INSERT INTO novels(id,title,author,publish_status,coin_cost_per_thousand,start_pay_chapter_index)
		VALUES(1,'Book','Author','published',50,1);
		INSERT INTO chapters(id,novel_id,chapter_index,title,word_count,content) VALUES('chapter',1,0,'Chapter',1000,'Content');
		INSERT INTO wallets(user_id,charged_coins,bonus_coins) VALUES(1,100,0),(2,100,0),(3,100,0),(4,100,0);
		INSERT INTO admins(id,username,password_hash,role) VALUES('proof','proof','test','Finance');
		INSERT INTO payment_channel_accounts(provider,account_key,environment,credentials_ref,webhook_ref)
		VALUES('paypal','book-live','live','test','test'),('paypal','book-sandbox','sandbox','test','test');
		INSERT INTO recharge_orders(id,user_id,novel_id,amount_cents,payment_method,status,order_type,checkout_id,fulfilled_at,checkout_environment)
		VALUES(1,1,1,999,'paypal','Success','whole_book',gen_random_uuid(),now(),'sandbox'),
		  (2,2,1,999,'paypal','Success','whole_book',gen_random_uuid(),now(),'production'),
		  (3,3,1,999,'paypal','Success','whole_book',NULL,now(),NULL),
		  (4,4,1,999,'paypal','Success','whole_book',NULL,now(),NULL);
		INSERT INTO legacy_whole_book_proofs(order_id,novel_id,evidence_reference,verified_by)
		VALUES(3,1,'legacy product only','proof'),(4,1,'legacy product and verified receipt','proof');
		INSERT INTO third_party_payment_orders(order_id,payment_provider,capture_id,currency,gross_amount,status,account_id,object_type)
		SELECT 4,'paypal','capture-book','USD',9.99,'VERIFIED',id,'capture'
		FROM payment_channel_accounts WHERE account_key='book-live'`)
	var version int64
	if err := pool.QueryRow(ctx, `SELECT content_version FROM novels WHERE id=1`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	check := func(user int64, environment string, owned bool, checkUnlock bool) {
		t.Helper()
		actual, err := novel.NewDBRepository().HasWholeBook(ctx, user, 1)
		if err != nil || actual != owned {
			t.Fatalf("%s user %d ownership=%v: %v", environment, user, actual, err)
		}
		err = pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
			_, err := reserveWholeBook(ctx, tx, environment, user, 1, 0, 1)
			return err
		})
		if (owned && !errors.Is(err, ErrCheckoutConflict)) || (!owned && err != nil) {
			t.Fatalf("%s user %d reservation: %v", environment, user, err)
		}
		if checkUnlock {
			err := wallet.NewDBRepository().UnlockChapterTx(ctx, user, 1, 0, 0, wallet.ChapterQuote{ID: "chapter", Version: version, Price: 50})
			if (owned && !errors.Is(err, wallet.ErrAlreadyUnlocked)) || (!owned && err != nil) {
				t.Fatalf("%s user %d wallet unlock: %v", environment, user, err)
			}
		}
	}
	check(1, "production", false, true)
	check(2, "production", true, true)
	check(3, "production", false, true)
	check(4, "production", true, true)
	t.Setenv("CONVERSION_ENVIRONMENT", "sandbox")
	check(1, "sandbox", true, false)
	check(2, "sandbox", false, false)
	check(3, "sandbox", false, false)
	check(4, "sandbox", false, false)
}

type legacyBookPayPal struct {
	*vipOfferAdapter
	capture payment.OrderCapture
	outcome error
	writes  int
}

func (a *legacyBookPayPal) GetCapture(context.Context, string) (*payment.OrderCapture, error) {
	return &a.capture, a.outcome
}

func (a *legacyBookPayPal) CaptureOrder(context.Context, string, payment.WebhookAccount) (*payment.OrderCapture, error) {
	a.writes++
	return nil, errors.New("unexpected duplicate capture")
}

func TestLegacyWholeBookPendingReservation(t *testing.T) {
	t.Setenv("CONVERSION_ENVIRONMENT", "production")
	pool := testdb.Open(t)
	ctx := t.Context()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO users(id) VALUES(912),(913),(914);
		INSERT INTO novels(id,title,author,publish_status) VALUES(912,'Book','Author','published');
		INSERT INTO admins(id,username,password_hash,role) VALUES('r12','R12','test','Finance');
		SELECT assert_paypal_identity('r12-live','live');
		INSERT INTO payment_channel_accounts(provider,account_key,environment,credentials_ref,webhook_ref)
			VALUES('paypal','r12-sandbox','sandbox','test','test');`)
	seedTestProductAndSlot(t, pool, testCatalogSpec{
		SlotID: 912, TemplateID: 912, TemplateName: "Test", IsDefault: true, SlotIndex: 1,
		Code: "TEST_R12_912", ProductType: "whole_book", PriceCents: 999,
		Environment: "live", PayPalAccountKey: "r12-live",
	})
	exec(`INSERT INTO recharge_orders(id,user_id,novel_id,amount_cents,coins,payment_method,status,order_type,external_ref_id,checkout_environment,created_at)
			VALUES(912,912,912,999,5,'paypal','Pending','whole_book','legacy-unknown',NULL,now()-interval '1 day'),
				(913,913,912,999,5,'paypal','Pending','whole_book','legacy-sandbox',NULL,now()-interval '1 day'),
				(914,914,912,999,5,'paypal','Pending','whole_book','legacy-live','production',now()-interval '1 day');
		INSERT INTO legacy_whole_book_proofs(order_id,novel_id,evidence_reference,verified_by)
			VALUES(912,912,'old item 912','r12'),(913,912,'old item 913','r12'),(914,912,'old item 914','r12');
		INSERT INTO third_party_payment_orders(order_id,payment_provider,external_order_id,capture_id,currency,gross_amount,status,account_id,object_type)
			SELECT 913,'paypal','legacy-sandbox','sandbox-receipt','USD',9.99,'VERIFIED',id,'capture'
			FROM payment_channel_accounts WHERE account_key='r12-sandbox';
		UPDATE recharge_orders SET checkout_id=gen_random_uuid(),recharge_slot_id=912 WHERE id=914`)
	repo := NewDBRepository(wallet.NewDBRepository())
	prepare := func(user int64) (*PurchaseIntent, error) {
		return repo.preparePurchase(ctx, user, purchaseRequest{key: uuid.New(), slotID: 912, novelID: 912}, checkoutAttribution{})
	}
	for _, user := range []int64{912, 914} {
		if _, err := prepare(user); !errors.Is(err, ErrCheckoutConflict) {
			t.Fatalf("pending legacy checkout for %d did not reserve the book: %v", user, err)
		}
	}
	if intent, err := prepare(913); err != nil || intent.Status != "ready" {
		t.Fatalf("verified sandbox order blocked live checkout: %+v %v", intent, err)
	}
	if allowed, err := repo.leaseCheckoutCapture(ctx, "legacy-sandbox"); err != nil || allowed {
		t.Fatalf("sandbox legacy capture became sendable in production: %t %v", allowed, err)
	}
	jobs, err := claimPayPalOrders(ctx)
	if err != nil || len(jobs) == 0 {
		t.Fatalf("legacy read reconciliation: %v %v", jobs, err)
	}
	account := payment.WebhookAccount{AccountKey: "r12-live", Environment: "live"}
	adapter := &legacyBookPayPal{vipOfferAdapter: &vipOfferAdapter{account: account},
		capture: payment.OrderCapture{OrderID: "legacy-unknown", Account: account, CustomID: "912:5", GrossAmount: "9.99", CurrencyCode: "USD"},
		outcome: paypal.ErrOrderApproved}
	svc := NewService(repo, nil, payment.NewAdapters(adapter)).(*service)

	// Simulate an intent and PayPal attempt already opened before R12 shipped.
	exec(`INSERT INTO recharge_orders(id,user_id,novel_id,amount_cents,payment_method,status,order_type,checkout_id,checkout_environment,recharge_slot_id)
		VALUES(915,912,912,999,'paypal','Pending','whole_book',gen_random_uuid(),'production',912)`)
	var modern string
	if err := pool.QueryRow(ctx, `SELECT checkout_id::text FROM recharge_orders WHERE id=915`).Scan(&modern); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.reservePayPalAttempt(ctx, 912, modern, "https://reader.example", "https://reader.example", account); !errors.Is(err, ErrCheckoutConflict) {
		t.Fatalf("existing intent opened a new channel attempt before legacy resolution: %v", err)
	}
	exec(`INSERT INTO payment_checkout_attempts(id,order_id,provider,account_id,sales_mode,status,provider_order_id,return_url,cancel_url)
			SELECT gen_random_uuid(),915,'paypal',id,'direct','buyer_action','modern-open','https://reader.example','https://reader.example'
			FROM payment_channel_accounts WHERE account_key='r12-live'`)
	if _, err := svc.completePayPalOrder(ctx, 912, "legacy-unknown", true); !errors.Is(err, ErrCheckoutPending) || adapter.writes != 0 {
		t.Fatalf("competing checkout left legacy capture sendable: %v writes=%d", err, adapter.writes)
	}
	if allowed, err := repo.leaseCheckoutCapture(ctx, "modern-open"); err != nil || allowed {
		t.Fatalf("new capture proceeded before the legacy outcome was verified: %t %v", allowed, err)
	}
	adapter.outcome = paypal.ErrOrderClosed
	if _, err := svc.completePayPalOrder(ctx, 0, "legacy-unknown", true); !errors.Is(err, ErrCheckoutPending) {
		t.Fatalf("terminal channel check did not reconcile: %v", err)
	}
	if intent, err := prepare(912); err != nil || intent.ID == "" {
		t.Fatalf("terminal legacy order still blocks: %+v %v", intent, err)
	}

	if allowed, err := repo.leaseCheckoutCapture(ctx, "modern-open"); err != nil || !allowed {
		t.Fatalf("verified Legacy closure did not release the new capture: %t %v", allowed, err)
	}
	// A late, verified capture must still be recorded without fulfilling a
	// second entitlement or re-crediting the original bonus.
	exec(`UPDATE recharge_orders SET status='Success',fulfilled_at=now() WHERE id=915`)
	adapter.capture.CaptureID, adapter.capture.Status, adapter.capture.RawPayload = "late-legacy-capture", "COMPLETED", "{}"
	for range 2 {
		if result, err := repo.settleCheckout(ctx, "legacy-unknown", &adapter.capture); err != nil || result.Coins != 0 {
			t.Fatalf("late capture replay: %+v %v", result, err)
		}
	}
	var receipts, grants, funded int
	err = pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM third_party_payment_orders WHERE order_id=912 AND capture_id='late-legacy-capture' AND account_id IS NOT NULL),
		(SELECT count(*) FROM recharge_orders WHERE user_id=912 AND novel_id=912 AND fulfilled_at IS NOT NULL),
		(SELECT count(*) FROM payment_fulfillment_funding WHERE order_id=912)`).Scan(&receipts, &grants, &funded)
	if err != nil || receipts != 1 || grants != 1 || funded != 0 {
		t.Fatalf("late capture receipt=%d grants=%d funded=%d: %v", receipts, grants, funded, err)
	}
	exec(`INSERT INTO wallets(user_id,charged_coins,bonus_coins) VALUES(912,0,10)`)
	if err := repo.CompletePayPalRefund(ctx, "late-refund", "late-legacy-capture", 999, "USD", account); err != nil {
		t.Fatal(err)
	}
	var state string
	var bonus int
	err = pool.QueryRow(ctx, `SELECT o.status,w.bonus_coins FROM recharge_orders o JOIN wallets w ON w.user_id=o.user_id WHERE o.id=912`).
		Scan(&state, &bonus)
	if err != nil || state != "Refunded" || bonus != 10 {
		t.Fatalf("refund of unfunded duplicate: status=%s bonus=%d: %v", state, bonus, err)
	}
}

type supersededStripeCheckout struct {
	payment.SingleCheckoutAdapter
	created, closed int
}

func (a *supersededStripeCheckout) CreateSingleCheckout(context.Context, payment.SingleCheckoutRequest) (payment.SingleCheckoutProof, error) {
	a.created++
	return payment.SingleCheckoutProof{}, errors.New("unexpected Stripe checkout creation")
}

func (a *supersededStripeCheckout) FindSingleCheckout(context.Context, string, time.Time, time.Time) (string, error) {
	return "cs_R13", nil
}

func (a *supersededStripeCheckout) ReadSingleCheckout(_ context.Context, session, attempt string, _ payment.SingleCheckoutOffer) (payment.SingleCheckoutProof, error) {
	return payment.SingleCheckoutProof{ID: session, AttemptID: attempt, AwaitingBuyer: true}, nil
}

func (a *supersededStripeCheckout) CloseSingleCheckout(_ context.Context, session, attempt string, _ payment.SingleCheckoutOffer) (payment.SingleCheckoutProof, error) {
	a.closed++
	return payment.SingleCheckoutProof{ID: session, AttemptID: attempt, Expired: true}, nil
}

func TestWholeBookRecoveryFencesPreviouslyReservedChannelWrites(t *testing.T) {
	t.Setenv("CONVERSION_ENVIRONMENT", "production")
	pool := testdb.Open(t)
	ctx := t.Context()
	exec := func(sql string) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO users(id) VALUES(930);
		INSERT INTO novels(id,title,author,publish_status) VALUES(930,'Book','Author','published');
		INSERT INTO admins(id,username,password_hash,role) VALUES('r13','R13','test','Finance');
		SELECT assert_paypal_identity('r13-live','live');
		INSERT INTO payment_channel_accounts(provider,account_key,environment,credentials_ref,webhook_ref)
			VALUES('stripe','acct_R13','live','fixture','fixture');
		INSERT INTO recharge_orders(id,user_id,novel_id,amount_cents,coins,payment_method,status,order_type,external_ref_id,checkout_id,checkout_environment)
			VALUES(930,930,930,999,1,'paypal','Pending','whole_book','old',NULL,NULL),
			  (931,930,930,999,0,'paypal','Pending','whole_book','next',gen_random_uuid(),'production'),
			  (932,930,930,999,0,'paypal','Pending','whole_book',NULL,gen_random_uuid(),'production'),
			  (933,930,930,999,0,'paypal','Pending','whole_book',NULL,gen_random_uuid(),'production');
		INSERT INTO legacy_whole_book_proofs(order_id,novel_id,evidence_reference,verified_by) VALUES(930,930,'original','r13');
		INSERT INTO payment_checkout_attempts(id,order_id,provider,account_id,sales_mode,status,provider_order_id,return_url,cancel_url,first_sent_at,retry_until)
			SELECT gen_random_uuid(),x.id,a.provider,a.id,'direct',x.status,x.reference,'https://reader.example','https://reader.example',x.sent,x.sent+interval '15 minutes'
			FROM (VALUES(931,'paypal','buyer_action','next',NULL::timestamptz),(932,'stripe','creating',NULL,NULL),
				(933,'stripe','creating',NULL,now())) x(id,provider,status,reference,sent)
			JOIN payment_channel_accounts a ON a.provider=x.provider;
		INSERT INTO payment_stripe_checkout_terms(attempt_id,product_ref,price_ref,amount_minor,allowed_countries)
			SELECT id,'prod_R13','price_R13',999,ARRAY['US'] FROM payment_checkout_attempts WHERE provider='stripe'`)
	account := payment.WebhookAccount{AccountKey: "r13-live", Environment: "live"}
	stripeAccount := payment.WebhookAccount{AccountKey: "acct_R13", Environment: "live"}
	repo := NewDBRepository(wallet.NewDBRepository())
	adapter := &legacyBookPayPal{vipOfferAdapter: &vipOfferAdapter{account: account}, outcome: paypal.ErrOrderApproved}
	svc := NewService(repo, nil, payment.NewAdapters(adapter)).(*service)
	var checkout string
	if err := pool.QueryRow(ctx, `SELECT checkout_id::text FROM recharge_orders WHERE id=931`).Scan(&checkout); err != nil {
		t.Fatal(err)
	}
	adapter.capture = payment.OrderCapture{OrderID: "next", CustomID: checkout, Account: account, CurrencyCode: "USD", GrossAmount: "9.99"}
	assertFenced := func() {
		t.Helper()
		if _, err := svc.completePayPalOrder(ctx, 930, "next", true); !errors.Is(err, ErrCheckoutPending) || adapter.writes != 0 {
			t.Fatalf("superseded Capture was submitted: writes=%d %v", adapter.writes, err)
		}
		rows, err := repo.claimStripeAttempts(ctx, stripeAccount, "", true)
		if err != nil || len(rows) != 1 || rows[0].OrderID != 933 || rows[0].MayCreate {
			t.Fatalf("Stripe must claim only the previously sent lookup: %+v %v", rows, err)
		}
		exec(`UPDATE payment_checkout_attempts SET next_create_at=now() WHERE provider='stripe'`)
	}
	assertFenced()
	old := &payment.OrderCapture{OrderID: "old", CustomID: "930:1", Account: account, Status: "COMPLETED",
		CaptureID: "old-paid", CurrencyCode: "USD", GrossAmount: "9.99", RawPayload: "{}"}
	if _, err := repo.settleCheckout(ctx, "old", old); err != nil {
		t.Fatal(err)
	}
	assertFenced()
	if owned, err := novel.NewDBRepository().HasWholeBook(ctx, 930, 930); err != nil || !owned {
		t.Fatalf("old order failed to grant the book: %v %v", owned, err)
	}
	exec(`UPDATE recharge_orders SET status='Paid',fulfilled_at=NULL WHERE id=930`)
	assertFenced()
	rows, err := repo.claimStripeAttempts(ctx, stripeAccount, "", true)
	if err != nil || len(rows) != 1 {
		t.Fatalf("sent Stripe checkout recovery: %+v %v", rows, err)
	}
	stripeAdapter := &supersededStripeCheckout{}
	if err := svc.syncStripeAttempt(ctx, stripeAdapter, rows[0], ""); err != nil || stripeAdapter.created != 0 || stripeAdapter.closed != 1 {
		t.Fatalf("previous Stripe session was not closed: creates=%d closes=%d %v", stripeAdapter.created, stripeAdapter.closed, err)
	}
	var attemptState string
	if err := pool.QueryRow(ctx, `SELECT status FROM payment_checkout_attempts WHERE order_id=933`).Scan(&attemptState); err != nil || attemptState != "closed" {
		t.Fatalf("verified terminal Stripe session: %s %v", attemptState, err)
	}
	exec(`UPDATE recharge_orders SET status='Refunded' WHERE id=930;
		UPDATE payment_checkout_attempts SET status='closed' WHERE order_id IN (932,933)`)
	if allowed, err := repo.leaseCheckoutCapture(ctx, "next"); err != nil || !allowed {
		t.Fatalf("resolved older order blocked the existing Capture: %t %v", allowed, err)
	}
}

func TestUnprovenLegacyWholeBookReconciliation(t *testing.T) {
	t.Setenv("CONVERSION_ENVIRONMENT", "production")
	pool := testdb.Open(t)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `SELECT assert_paypal_identity('r14-live','live');
		INSERT INTO users(id) VALUES(940),(941),(942);
		INSERT INTO novels(id,title,author,publish_status) VALUES(940,'Book','Author','published');
		INSERT INTO recharge_orders(id,user_id,novel_id,amount_cents,payment_method,status,order_type,external_ref_id,created_at)
			SELECT id,id,940,999,'paypal','Pending','whole_book','legacy-'||id,now()-interval '1 day'
			FROM users WHERE id IN (940,941,942)`); err != nil {
		t.Fatal(err)
	}
	seedTestProductAndSlot(t, pool, testCatalogSpec{
		SlotID: 940, TemplateID: 940, TemplateName: "Test", IsDefault: true, SlotIndex: 1,
		Code: "TEST_R14_940", ProductType: "whole_book", PriceCents: 999,
		Environment: "live", PayPalAccountKey: "r14-live",
	})
	repo := NewDBRepository(wallet.NewDBRepository())
	account := payment.WebhookAccount{AccountKey: "r14-live", Environment: "live"}
	adapter := &legacyBookPayPal{vipOfferAdapter: &vipOfferAdapter{account: account}}
	svc := NewService(repo, nil, payment.NewAdapters(adapter)).(*service)
	for _, example := range []struct {
		id       int64
		outcome  error
		capture  string
		status   string
		order    string
		canOrder bool
	}{{940, paypal.ErrOrderClosed, "", "", "Failed", true},
		{941, nil, "actual-capture", "COMPLETED", "Paid", false},
		{942, context.DeadlineExceeded, "", "", "Pending", false}} {
		adapter.outcome = example.outcome
		adapter.capture = payment.OrderCapture{OrderID: fmt.Sprint("legacy-", example.id), Account: account, CurrencyCode: "USD",
			GrossAmount: "9.99", CaptureID: example.capture, Status: example.status, RawPayload: "{}"}
		_, _ = svc.completePayPalOrder(ctx, 0, adapter.capture.OrderID, true)
		var status string
		var fulfilled bool
		var receipts int
		err := pool.QueryRow(ctx, `SELECT status,fulfilled_at IS NOT NULL,
			(SELECT count(*) FROM third_party_payment_orders WHERE order_id=o.id AND status='VERIFIED')
			FROM recharge_orders o WHERE id=$1`, example.id).Scan(&status, &fulfilled, &receipts)
		if err != nil || status != example.order || fulfilled || (example.capture != "") != (receipts == 1) || adapter.writes != 0 {
			t.Fatalf("R14 order %d: state=%s fulfilled=%t receipts=%d writes=%d %v", example.id, status, fulfilled, receipts, adapter.writes, err)
		}
		_, err = repo.preparePurchase(ctx, example.id, purchaseRequest{key: uuid.New(), slotID: 940, novelID: 940}, checkoutAttribution{})
		if example.canOrder && err != nil || !example.canOrder && !errors.Is(err, ErrCheckoutConflict) {
			t.Fatalf("R14 replacement for %d: %v", example.id, err)
		}
	}
}
