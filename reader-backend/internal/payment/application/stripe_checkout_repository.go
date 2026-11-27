package application

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"reader-backend/internal/conversion"
	"reader-backend/internal/db"
	"reader-backend/internal/payment"
)

const stripeCreateRetryWindow = 23 * time.Hour
const stripeCheckoutLease = time.Minute

type stripePurchaseAttempt struct {
	ID, IntentID, SessionID, ReturnURL, CancelURL, Status string
	OrderID, AccountID                                    int64
	Offer                                                 payment.SingleCheckoutOffer
	CreatedAt                                             time.Time
	LookupOnly                                            bool
	MayCreate                                             bool
}

const stripeAttemptFields = `p.id::text,o.checkout_id::text,p.order_id,p.account_id,COALESCE(p.provider_order_id,''),
	p.return_url,p.cancel_url,p.status,t.product_ref,t.price_ref,t.amount_minor,t.allowed_countries,COALESCE(t.expected_country,''),p.sales_mode,COALESCE(t.tax_liability_account,''),p.created_at,
	p.create_lookup_only OR COALESCE(p.retry_until<=now(),false)`

func scanStripeAttempt(row pgx.Row) (stripePurchaseAttempt, error) {
	var a stripePurchaseAttempt
	err := row.Scan(&a.ID, &a.IntentID, &a.OrderID, &a.AccountID, &a.SessionID, &a.ReturnURL, &a.CancelURL, &a.Status,
		&a.Offer.ProductID, &a.Offer.PriceID, &a.Offer.AmountMinor, &a.Offer.Countries, &a.Offer.ExpectedCountry, &a.Offer.SalesMode, &a.Offer.TaxLiabilityAccount, &a.CreatedAt, &a.LookupOnly)
	return a, err
}

func (r *dbRepository) reserveStripePurchase(ctx context.Context, userID int64, intent, returnURL, cancelURL string, account payment.WebhookAccount) (*stripePurchaseAttempt, error) {
	evaluation := checkoutEvaluation{stage: paymentAttemptStage, userID: userID, provider: payment.ProviderStripe}
	return evaluateCheckout(ctx, evaluation, func(tx pgx.Tx, e *checkoutEvaluation) (*stripePurchaseAttempt, error) {
		return reserveStripeAttemptTx(ctx, tx, e, intent, returnURL, cancelURL, account)
	})
}

func reserveStripeAttemptTx(ctx context.Context, tx pgx.Tx, e *checkoutEvaluation, intent, returnURL, cancelURL string, account payment.WebhookAccount) (*stripePurchaseAttempt, error) {
	var slot int
	var orderID, productID, accountID, expected int64
	var orderType string
	var enabled bool
	err := tx.QueryRow(ctx, `SELECT o.id,COALESCE(o.recharge_slot_id,0),COALESCE(o.product_id,0),o.order_type,a.id,a.new_orders_enabled,o.amount_cents
		FROM recharge_orders o CROSS JOIN payment_channel_accounts a
		WHERE o.user_id=$1 AND o.checkout_id=$2 AND o.checkout_environment=$3
		 AND o.status='Pending' AND o.order_type IN ('single','whole_book') AND o.currency='USD'
		 AND a.provider='stripe' AND a.environment=$5 AND a.account_key=$4
		FOR UPDATE OF o`, e.userID, intent, conversion.Environment(), account.AccountKey, account.Environment).
		Scan(&orderID, &slot, &productID, &orderType, &accountID, &enabled, &expected)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrCheckoutUnavailable
	}
	if err != nil {
		return nil, err
	}
	e.orderID, e.accountID, e.checkoutID, e.slotID = orderID, accountID, intent, slot
	if orderType == "whole_book" {
		if err := checkOtherWholeBookReservation(ctx, tx, orderID, conversion.Environment()); err != nil {
			return nil, err
		}
	}
	var activeID, provider string
	var activeAccount int64
	err = tx.QueryRow(ctx, `SELECT id::text,provider,account_id FROM payment_checkout_attempts
		WHERE order_id=$1 AND status IN ('creating','buyer_action','capturing') FOR UPDATE`, orderID).Scan(&activeID, &provider, &activeAccount)
	switch {
	case err == nil:
		if provider != payment.ProviderStripe || activeAccount != accountID {
			return nil, ErrCheckoutPending
		}
		e.resumed = true
		a, err := loadStripeAttempt(ctx, tx, account, activeID)
		return &a, err
	case !errors.Is(err, pgx.ErrNoRows):
		return nil, err
	case !enabled:
		return nil, ErrCheckoutPaused
	}
	if productID <= 0 {
		return nil, ErrCheckoutUnavailable
	}
	var offer payment.SingleCheckoutOffer
	var verifiedCatalog []byte
	var releaseEvidence, marketPolicy, expectedCountry *string
	err = tx.QueryRow(ctx, `SELECT b.external_product_ref,b.external_price_ref,p.price_cents,b.allowed_countries,b.sales_mode,
		b.validated_catalog,b.release_evidence,b.market_policy,expected.country
		FROM product_channel_bindings b
		JOIN products p ON p.id=b.product_id
		JOIN product_publications pub ON pub.product_id=p.id AND pub.environment=$4 AND pub.status='published'
		CROSS JOIN LATERAL (SELECT payment_last_stripe_billing_country($1,$5) AS country) expected
		WHERE b.account_id=$1 AND b.product_id=$2 AND b.provider='stripe' AND b.environment=$4
		 AND b.enabled=true AND b.validation_status='verified'
		 AND b.external_product_ref<>'' AND b.external_price_ref<>''
		 AND ($4='sandbox' OR (b.validated_catalog IS NOT NULL AND b.release_evidence IS NOT NULL AND b.market_policy IS NOT NULL
		   AND (expected.country IS NULL OR expected.country=ANY(b.allowed_countries))))
		 AND p.product_type IN ('single','whole_book') AND p.price_cents=$3 FOR SHARE OF b,p`,
		accountID, productID, expected, account.Environment, e.userID).Scan(&offer.ProductID, &offer.PriceID, &offer.AmountMinor, &offer.Countries, &offer.SalesMode, &verifiedCatalog, &releaseEvidence, &marketPolicy, &expectedCountry)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrCheckoutUnavailable
	}
	if err != nil {
		return nil, err
	}
	id := uuid.NewString()
	_, err = tx.Exec(ctx, `INSERT INTO payment_checkout_attempts(id,order_id,provider,account_id,sales_mode,status,return_url,cancel_url)
		VALUES($1,$2,'stripe',$3,$4,'creating',$5,$6)`, id, orderID, accountID, offer.SalesMode, returnURL, cancelURL)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO payment_stripe_checkout_terms(attempt_id,product_ref,price_ref,amount_minor,allowed_countries,
		   validated_catalog,release_evidence,market_policy,expected_country,market_basis)
		VALUES($1,$2,$3,$4,$5,$6::jsonb,$7,$8,$9::text,CASE WHEN $9::text IS NULL THEN 'stripe_confirmation_required' ELSE 'prior_verified_payment' END)`,
		id, offer.ProductID, offer.PriceID, offer.AmountMinor, offer.Countries, verifiedCatalog, releaseEvidence, marketPolicy, expectedCountry)
	if err != nil {
		return nil, err
	}
	a, err := loadStripeAttempt(ctx, tx, account, id)
	return &a, err
}

func loadStripeAttempt(ctx context.Context, q checkoutQueryer, account payment.WebhookAccount, id string) (stripePurchaseAttempt, error) {
	return scanStripeAttempt(q.QueryRow(ctx, `SELECT `+stripeAttemptFields+`
		FROM payment_checkout_attempts p JOIN payment_stripe_checkout_terms t ON t.attempt_id=p.id
		JOIN recharge_orders o ON o.id=p.order_id JOIN payment_channel_accounts a ON a.id=p.account_id
		WHERE p.id=$1 AND p.provider='stripe' AND a.account_key=$2 AND a.environment=$3
		 AND o.checkout_environment=$4`, id, account.AccountKey, account.Environment, conversion.Environment()))
}

// The first transaction records the Stripe idempotency window before sending.
// On restart, the worker either reuses that key or switches to lookups.
func (r *dbRepository) claimStripeAttempts(ctx context.Context, account payment.WebhookAccount, id string, allowNew bool) ([]stripePurchaseAttempt, error) {
	rows, err := db.Pool(ctx).Query(ctx, `WITH due AS (
		SELECT p.id,(o.status='Pending' AND NOT `+wholeBookWriteBlocked("o", "$3")+`) AS allow_write FROM payment_checkout_attempts p JOIN payment_channel_accounts a ON a.id=p.account_id
		JOIN payment_stripe_checkout_terms t ON t.attempt_id=p.id JOIN recharge_orders o ON o.id=p.order_id
		WHERE p.provider='stripe' AND a.account_key=$1 AND a.environment=$2 AND o.checkout_environment=$3
		 AND ($4='' OR p.id=NULLIF($4,'')::uuid) AND p.next_create_at<=now()
		 AND (p.status<>'creating' OR p.first_sent_at IS NOT NULL OR
			($7 AND a.new_orders_enabled AND o.status='Pending' AND NOT `+wholeBookWriteBlocked("o", "$3")+`))
         AND (p.status IN ('creating','buyer_action','capturing') OR
		      p.status='settled' AND (t.ledger_observed_at IS NULL OR (o.status IN ('Success','Paid') AND EXISTS (
				SELECT 1 FROM payment_fulfillment_funding f JOIN third_party_payment_orders receipt ON receipt.id=f.receipt_id
				WHERE f.order_id=o.id AND f.principal_minor IS NULL AND receipt.account_id=p.account_id
				AND receipt.external_order_id=p.provider_order_id)) OR (o.fulfilled_at IS NULL AND o.status='Paid' AND EXISTS (
		    SELECT 1 FROM payment_stripe_market_reviews r WHERE r.order_id=o.id AND r.approved_at IS NOT NULL
		      AND r.approved_at>=o.updated_at)) OR EXISTS (SELECT 1 FROM payment_deferred_fulfillment h
		        LEFT JOIN payment_cases c ON c.id=h.case_id WHERE h.order_id=o.id AND h.resolved_at IS NULL
		          AND (h.case_id IS NULL OR c.work_state='approved'))))
		ORDER BY p.next_create_at,p.id LIMIT 5 FOR UPDATE OF p SKIP LOCKED
	), leased AS (
		UPDATE payment_checkout_attempts p SET first_sent_at=CASE WHEN p.status='creating' AND due.allow_write THEN COALESCE(p.first_sent_at,now()) ELSE p.first_sent_at END,
		  retry_until=CASE WHEN p.status='creating' AND due.allow_write THEN COALESCE(p.retry_until,now()+make_interval(secs=>$5::int)) ELSE p.retry_until END,
		  next_create_at=now()+make_interval(secs=>$6::int)
		FROM due WHERE p.id=due.id RETURNING p.*,due.allow_write
	) SELECT `+stripeAttemptFields+` OR NOT p.allow_write FROM leased p JOIN recharge_orders o ON o.id=p.order_id
		JOIN payment_stripe_checkout_terms t ON t.attempt_id=p.id`,
		account.AccountKey, account.Environment, conversion.Environment(), id, int(stripeCreateRetryWindow/time.Second), int(stripeCheckoutLease/time.Second), allowNew)
	if err != nil {
		return nil, err
	}
	attempts, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (stripePurchaseAttempt, error) { return scanStripeAttempt(row) })
	for i := range attempts {
		attempts[i].MayCreate = attempts[i].SessionID == "" && !attempts[i].LookupOnly
	}
	return attempts, err
}

func (r *dbRepository) stripeOfferSlots(ctx context.Context, account payment.WebhookAccount, userID int64) ([]int, error) {
	rows, err := db.Pool(ctx).Query(ctx, `SELECT s.id FROM recharge_slots s
		JOIN products p ON p.id=s.product_id
		JOIN product_publications pub ON pub.product_id=p.id AND pub.environment=$2 AND pub.status='published'
		JOIN product_channel_bindings b ON b.product_id=p.id AND b.provider='stripe' AND b.environment=$2
		JOIN payment_channel_accounts a ON a.id=b.account_id
		WHERE a.provider='stripe' AND a.account_key=$1 AND a.environment=$2 AND a.status='published' AND a.is_primary=true AND a.new_orders_enabled
		  AND b.enabled=true AND b.validation_status='verified' AND b.external_product_ref<>'' AND b.external_price_ref<>''
		  AND p.product_type IN ('single','whole_book')
		  AND (a.environment='sandbox' OR (b.validated_catalog IS NOT NULL AND b.release_evidence IS NOT NULL AND b.market_policy IS NOT NULL
		   AND COALESCE(payment_last_stripe_billing_country(a.id,$3)=ANY(b.allowed_countries),true)))
		  AND s.template_id=COALESCE((SELECT recharge_template_id FROM reader_promotion_for_user_env($3, $2) WHERE recharge_template_id IS NOT NULL),
			`+defaultLiveTemplateIDSQL+`)
		ORDER BY s.id`, account.AccountKey, account.Environment, userID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (int, error) { var id int; err := row.Scan(&id); return id, err })
}

func (r *dbRepository) stripeProductAvailable(ctx context.Context, account payment.WebhookAccount, productID int64) (bool, error) {
	var ok bool
	err := db.Pool(ctx).QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM products p
		JOIN product_publications pub ON pub.product_id=p.id AND pub.environment=$2 AND pub.status='published'
		JOIN product_channel_bindings b ON b.product_id=p.id AND b.provider='stripe' AND b.environment=$2
		JOIN payment_channel_accounts a ON a.id=b.account_id
		WHERE p.id=$3 AND p.product_type IN ('single','whole_book')
		  AND a.provider='stripe' AND a.account_key=$1 AND a.environment=$2 AND a.status='published' AND a.is_primary=true AND a.new_orders_enabled
		  AND b.enabled=true AND b.validation_status='verified' AND b.external_product_ref<>'' AND b.external_price_ref<>''
		  AND ($2='sandbox' OR (b.validated_catalog IS NOT NULL AND b.release_evidence IS NOT NULL AND b.market_policy IS NOT NULL))
	)`, account.AccountKey, account.Environment, productID).Scan(&ok)
	return ok, err
}
