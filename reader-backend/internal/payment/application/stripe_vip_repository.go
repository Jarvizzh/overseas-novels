package application

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"reader-backend/internal/conversion"
	"reader-backend/internal/db"
	"reader-backend/internal/payment"
)

type stripeVIPIntent struct {
	ID, Status, ReturnURL, CancelURL, ReturnPath, SessionID, SubscriptionID, CustomerID, ApprovalURL string
	AccountID, UserID                                                                                int64
	Account                                                                                          payment.WebhookAccount
	SlotID, TemplateID                                                                               int
	Offer                                                                                            payment.StripeVIPOffer
	FrozenParams                                                                                     *payment.FrozenCheckoutParams
	InvoiceScanCursor                                                                                string
	LookupOnly                                                                                       bool
	FirstSentAt                                                                                      *time.Time
	MayCreate                                                                                        bool
}

const stripeVIPFields = `v.id::text,v.status,v.user_id,v.account_id,v.slot_id,v.template_id,v.product_ref,v.price_ref,
	v.amount_minor,v.cycle,v.sales_mode,v.allowed_countries,COALESCE(v.expected_country,''),COALESCE(v.tax_liability_account,''),
	v.frozen_checkout_params,
	v.return_url,v.cancel_url,v.return_path,COALESCE(v.checkout_ref,''),COALESCE(v.subscription_ref,''),
	COALESCE(v.customer_ref,''),COALESCE(v.approval_url,''),COALESCE(v.invoice_scan_cursor,''),v.first_sent_at,
	(v.create_lookup_only OR COALESCE(v.retry_until<=now(),false))`

func scanStripeVIP(row pgx.Row) (stripeVIPIntent, error) {
	var v stripeVIPIntent
	var frozenRaw []byte
	err := row.Scan(&v.ID, &v.Status, &v.UserID, &v.AccountID, &v.SlotID, &v.TemplateID, &v.Offer.ProductID, &v.Offer.PriceID,
		&v.Offer.AmountMinor, &v.Offer.Cycle, &v.Offer.SalesMode, &v.Offer.Countries, &v.Offer.ExpectedCountry, &v.Offer.TaxLiabilityAccount,
		&frozenRaw, &v.ReturnURL,
		&v.CancelURL, &v.ReturnPath, &v.SessionID, &v.SubscriptionID, &v.CustomerID, &v.ApprovalURL, &v.InvoiceScanCursor, &v.FirstSentAt, &v.LookupOnly)
	if err == nil && len(frozenRaw) > 0 {
		var frozen payment.FrozenCheckoutParams
		if unmarshalErr := json.Unmarshal(frozenRaw, &frozen); unmarshalErr != nil {
			return v, unmarshalErr
		}
		v.FrozenParams = &frozen
	}
	return v, err
}

func findStripeVIPIntent(ctx context.Context, q checkoutQueryer, account payment.WebhookAccount, id string) (stripeVIPIntent, error) {
	intent, err := scanStripeVIP(q.QueryRow(ctx, `SELECT `+stripeVIPFields+` FROM payment_stripe_subscription_intents v
		JOIN payment_channel_accounts a ON a.id=v.account_id
		WHERE v.id=$1 AND a.provider='stripe' AND a.account_key=$2 AND a.environment=$3 AND v.checkout_environment=$4`,
		id, account.AccountKey, account.Environment, conversion.Environment()))
	intent.Account = account
	return intent, err
}

type stripeVIPRequest struct {
	userID                           int64
	slotID                           int
	returnURL, cancelURL, returnPath string
	attribution                      checkoutAttribution
	account                          payment.WebhookAccount
}

func reserveStripeVIP(ctx context.Context, req stripeVIPRequest, allowNew bool) (*stripeVIPIntent, error) {
	evaluation := checkoutEvaluation{stage: subscriptionIntentStage, provider: payment.ProviderStripe, userID: req.userID, slotID: req.slotID}
	return evaluateCheckout(ctx, evaluation, func(tx pgx.Tx, e *checkoutEvaluation) (*stripeVIPIntent, error) {
		return reserveStripeVIPTx(ctx, tx, e, req, allowNew)
	})
}

func reserveStripeVIPTx(ctx context.Context, tx pgx.Tx, e *checkoutEvaluation, req stripeVIPRequest, allowNew bool) (*stripeVIPIntent, error) {
	var permitted bool
	err := tx.QueryRow(ctx, `SELECT id,new_subscriptions_enabled FROM payment_channel_accounts
		WHERE provider='stripe' AND environment=$2 AND account_key=$1`, req.account.AccountKey, req.account.Environment).Scan(&e.accountID, &permitted)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrCheckoutUnavailable
	}
	if err != nil {
		return nil, err
	}
	var currentID string
	var scope int64
	var slot int
	var originalAccount payment.WebhookAccount
	err = tx.QueryRow(ctx, `SELECT v.id::text,v.account_id,v.slot_id,a.account_key,a.environment
		FROM payment_stripe_subscription_intents v JOIN payment_channel_accounts a ON a.id=v.account_id
		WHERE v.user_id=$1 AND v.checkout_environment=$2 AND v.status IN ('creating','buyer_action') FOR UPDATE OF v`, req.userID, conversion.Environment()).
		Scan(&currentID, &scope, &slot, &originalAccount.AccountKey, &originalAccount.Environment)
	if err == nil {
		if slot != req.slotID || !paymentEnvironmentMatches(originalAccount) {
			return nil, ErrSubscriptionConflict
		}
		e.accountID, e.checkoutID, e.resumed = scope, currentID, true
		v, err := findStripeVIPIntent(ctx, tx, originalAccount, currentID)
		return &v, err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if !allowNew || !permitted {
		return nil, ErrCheckoutPaused
	}
	var paypalReserved bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM paypal_subscription_intents i WHERE i.user_id=$1 AND i.checkout_environment=$2
		AND (i.submitted_at IS NOT NULL OR COALESCE(i.lease_until>now(),false) OR i.subscription_id IS NOT NULL
		    OR EXISTS(SELECT 1 FROM payment_vip_checkout_intents u WHERE u.paypal_intent_id=i.intent_id AND u.status='Pending'))
		AND NOT EXISTS(SELECT 1 FROM user_subscriptions s WHERE s.user_id=i.user_id AND s.subscription_id=i.subscription_id
		 AND s.payment_method='paypal' AND s.status IN ('CANCELLED','EXPIRED')))`, req.userID, conversion.Environment()).Scan(&paypalReserved)
	if err != nil {
		return nil, err
	}
	if paypalReserved {
		return nil, ErrSubscriptionConflict
	}
	if err := checkVIPCheckout(ctx, tx, e, subscriptionIntent{}, req.slotID); err != nil {
		return nil, err
	}

	id := uuid.NewString()
	returnURL := appendStripeVIPReturn(req.returnURL, id)
	cancelURL := appendStripeVIPReturn(req.cancelURL, id)
	metadata, err := json.Marshal(req.attribution)
	if err != nil {
		return nil, err
	}
	tag, err := tx.Exec(ctx, `INSERT INTO payment_stripe_subscription_intents
		(id,user_id,account_id,checkout_environment,offer_version_id,product_id,product_snapshot,slot_id,template_id,product_ref,price_ref,amount_minor,cycle,
		 sales_mode,allowed_countries,return_url,cancel_url,return_path,attribution,
		 validated_catalog,release_evidence,market_policy,expected_country,market_basis)
		SELECT $1,$2,$3,$4,NULL,prod.id,
		 jsonb_build_object(
			'product_id',prod.id,'code',prod.code,'product_type',prod.product_type,
			'name',prod.name,'description',prod.description,'price_cents',prod.price_cents,
			'currency',prod.currency,'coins',prod.coins,'bonus',prod.bonus,
			'vip_cycle',COALESCE(prod.vip_cycle,''),'slot_id',s.id,
			'badge',s.badge,'display_alias',s.display_alias
		 ),
		 s.id,s.template_id,b.external_product_ref,b.external_price_ref,prod.price_cents,prod.vip_cycle,
		 b.sales_mode,b.allowed_countries,$6,$7,$8,$9::jsonb ||
		    jsonb_build_object('pixel_id',fp.pixel_id,'promotion_link_id',p.id,'novel_id',p.novel_id,'utm_source',u.utm_source,'utm_campaign',u.utm_campaign),
		    b.validated_catalog,b.release_evidence,b.market_policy,expected.country,
		    CASE WHEN expected.country IS NULL THEN 'stripe_confirmation_required' ELSE 'prior_verified_payment' END
		FROM recharge_slots s JOIN products prod ON prod.id=s.product_id
		JOIN product_publications pub ON pub.product_id=prod.id AND pub.environment=$10 AND pub.status='published'
		JOIN product_channel_bindings b ON b.product_id=prod.id AND b.account_id=$3 AND b.provider='stripe' AND b.environment=$10
		JOIN users u ON u.id=$2
		CROSS JOIN LATERAL (SELECT payment_last_stripe_billing_country($3,$2) AS country) expected
		LEFT JOIN LATERAL reader_promotion_for_user_env(u.id, $10) p ON true LEFT JOIN fb_pixels fp ON fp.id=p.fb_pixel_id
		WHERE s.id=$5 AND prod.product_type='vip'
		  AND b.enabled=true AND b.validation_status='verified' AND b.external_product_ref<>'' AND b.external_price_ref<>''
		  AND ($10='sandbox' OR (b.validated_catalog IS NOT NULL AND b.release_evidence IS NOT NULL AND b.market_policy IS NOT NULL
		   AND (expected.country IS NULL OR expected.country=ANY(b.allowed_countries))))
		  AND s.template_id=COALESCE((SELECT recharge_template_id FROM reader_promotion_for_user_env($2, $10) WHERE recharge_template_id IS NOT NULL),
			`+defaultLiveTemplateIDSQL+`)`, id, req.userID, e.accountID,
		conversion.Environment(), req.slotID, returnURL, cancelURL, req.returnPath, metadata, req.account.Environment)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() != 1 {
		return nil, ErrCheckoutUnavailable
	}
	e.checkoutID = id
	v, err := findStripeVIPIntent(ctx, tx, req.account, id)
	return &v, err
}

func stripeVIPSlots(ctx context.Context, account payment.WebhookAccount, user int64) ([]int, error) {
	rows, err := db.Pool(ctx).Query(ctx, `SELECT s.id FROM recharge_slots s
		JOIN products p ON p.id=s.product_id
		JOIN product_publications pub ON pub.product_id=p.id AND pub.environment=$2 AND pub.status='published'
		JOIN product_channel_bindings b ON b.product_id=p.id AND b.provider='stripe' AND b.environment=$2
		JOIN payment_channel_accounts a ON a.id=b.account_id
		WHERE a.provider='stripe' AND a.account_key=$1 AND a.environment=$2 AND a.status='published' AND a.is_primary=true AND a.new_subscriptions_enabled
		 AND p.product_type='vip' AND b.enabled=true AND b.validation_status='verified' AND b.external_product_ref<>'' AND b.external_price_ref<>''
		 AND (a.environment='sandbox' OR (b.validated_catalog IS NOT NULL AND b.release_evidence IS NOT NULL AND b.market_policy IS NOT NULL
		  AND COALESCE(payment_last_stripe_billing_country(a.id,$3)=ANY(b.allowed_countries),true)))
		 AND s.template_id=COALESCE((SELECT recharge_template_id FROM reader_promotion_for_user_env($3, $2) WHERE recharge_template_id IS NOT NULL),
			`+defaultLiveTemplateIDSQL+`)
		ORDER BY s.id`, account.AccountKey, account.Environment, user)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (int, error) { var id int; err := row.Scan(&id); return id, err })
}

// The statement that sets first_sent_at holds a shared lock on the exact binding.
const stripeVIPBindingMatchSQL = `
	SELECT 1 FROM product_channel_bindings b
	WHERE b.product_id=v.product_id AND b.account_id=v.account_id
	  AND b.provider='stripe' AND b.environment=a.environment
	  AND b.enabled AND b.validation_status='verified'
	  AND b.external_product_ref=v.product_ref AND b.external_price_ref=v.price_ref
	  AND b.sales_mode=v.sales_mode
	  AND (v.expected_country IS NULL OR v.expected_country=ANY(b.allowed_countries))
	  AND (b.environment='sandbox' OR (b.validated_catalog IS NOT NULL AND b.release_evidence IS NOT NULL AND b.market_policy IS NOT NULL))
`

func claimStripeVIP(ctx context.Context, account payment.WebhookAccount, id string, allowNew bool) ([]stripeVIPIntent, error) {
	rows, err := db.Pool(ctx).Query(ctx, `WITH due AS (
		SELECT v.id FROM payment_stripe_subscription_intents v JOIN payment_channel_accounts a ON a.id=v.account_id
		WHERE a.provider='stripe' AND a.account_key=$1 AND a.environment=$2 AND v.checkout_environment=$3
		  AND ($4='' OR v.id=NULLIF($4,'')::uuid) AND v.next_check_at<=now()
		  AND v.status IN ('creating','buyer_action','bound')
		  AND (v.cancelled_at IS NULL OR v.cancelled_at>now()-interval '7 days' OR EXISTS (
		      SELECT 1 FROM payment_stripe_invoice_periods p
		      WHERE p.intent_id=v.id AND p.settlement_state='pending_settlement' AND NOT p.admitted
		  ))
		  AND (v.status<>'creating' OR v.first_sent_at IS NOT NULL
		       OR v.checkout_ref IS NOT NULL OR ($6 AND a.new_subscriptions_enabled AND a.status='published' AND a.is_primary
		          AND (v.product_id IS NULL OR EXISTS (`+stripeVIPBindingMatchSQL+` FOR SHARE OF b))
		          AND NOT EXISTS(
		              SELECT 1 FROM payment_vip_checkout_intents u WHERE u.stripe_intent_id=v.id
		                AND NOT EXISTS (
		                    SELECT 1 FROM recharge_slots s JOIN products p ON p.id=s.product_id
		                    JOIN product_publications pub ON pub.product_id=p.id AND pub.environment=a.environment AND pub.status='published'
		                    WHERE s.id=u.slot_id AND s.template_id=u.template_id AND p.id=u.product_id
		                      AND p.product_type='vip' AND p.currency=u.currency AND p.price_cents=u.price_cents AND p.vip_cycle=u.cycle
		                      AND s.template_id=COALESCE(
		                          (SELECT recharge_template_id FROM reader_promotion_for_user_env(u.user_id, a.environment) WHERE recharge_template_id IS NOT NULL),
		                          `+defaultLiveTemplateIDSQL+`)
		                    FOR SHARE OF s,p,pub
		                ))))
		  AND (v.checkout_ref IS NOT NULL OR v.subscription_ref IS NOT NULL OR NOT EXISTS (
		      SELECT 1 FROM payment_vip_checkout_intents u
		      WHERE u.stripe_intent_id=v.id AND (u.status<>'Pending' OR u.switch_to_provider IS NOT NULL)))
		ORDER BY v.next_check_at,v.id LIMIT 5 FOR UPDATE OF v SKIP LOCKED
	), leased AS (
		UPDATE payment_stripe_subscription_intents v SET
		  first_sent_at=CASE WHEN v.status='creating' AND v.checkout_ref IS NULL THEN COALESCE(v.first_sent_at,now()) ELSE v.first_sent_at END,
		  retry_until=CASE WHEN v.status='creating' AND v.checkout_ref IS NULL THEN COALESCE(v.retry_until,now()+make_interval(secs=>$5::int)) ELSE v.retry_until END,
		  next_check_at=now()+CASE WHEN v.status='bound' THEN interval '15 minutes' ELSE interval '1 minute' END
		FROM due WHERE v.id=due.id RETURNING v.*
	) SELECT `+stripeVIPFields+` FROM leased v`, account.AccountKey, account.Environment, conversion.Environment(), id,
		int(stripeCreateRetryWindow/time.Second), allowNew)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (stripeVIPIntent, error) {
		v, err := scanStripeVIP(row)
		v.Account = account
		v.MayCreate = v.Status == "creating" && v.SessionID == "" && v.FirstSentAt != nil && !v.LookupOnly
		return v, err
	})
}

func saveStripeVIPCheckout(ctx context.Context, v stripeVIPIntent, proof payment.StripeVIPCheckout) error {
	if proof.IntentID != v.ID || proof.ID == "" || (v.SessionID != "" && v.SessionID != proof.ID) {
		return ErrCheckoutUnavailable
	}
	status := "buyer_action"
	if proof.Expired {
		status = "closed"
	}
	tx, err := db.Pool(ctx).Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	tag, err := tx.Exec(ctx, `UPDATE payment_stripe_subscription_intents SET checkout_ref=$2,
		approval_url=CASE WHEN status='bound' THEN NULL ELSE NULLIF($3,'') END,
		status=CASE WHEN status='bound' THEN 'bound' ELSE $4 END
		WHERE id=$1 AND (checkout_ref IS NULL OR checkout_ref=$2) AND status<>'closed'`, v.ID, proof.ID, proof.URL, status)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrCheckoutUnavailable
	}
	if (proof.AwaitingBuyer || proof.SubscriptionID != "") && !proof.Expired {
		if err := recordStripeVIPCheckoutInitiated(ctx, tx, v.ID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func recordStripeVIPCheckoutInitiated(ctx context.Context, tx pgx.Tx, intentID string) error {
	_, err := tx.Exec(ctx, `INSERT INTO reader_conversion_outbox(business_key,event_name,intent_id,environment,occurred_at,snapshot)
		SELECT 'subscription-intent:'||v.id||':InitiateCheckout','InitiateCheckout',v.id::text,v.checkout_environment,clock_timestamp(),
			jsonb_build_object('user_id',u.id::text,'email',u.email,'amount_cents',v.amount_minor,'currency','USD','order_type','subscription',
			'promotion_id',v.attribution->>'promotion_link_id','action_source','website','attribution',v.attribution,
			'attribution_state',CASE WHEN NULLIF(v.attribution->>'pixel_id','') IS NOT NULL THEN 'matched'
				WHEN NULLIF(v.attribution->>'promotion_link_id','') IS NOT NULL OR COALESCE(v.attribution->>'utm_source','')<>''
				OR COALESCE(v.attribution->>'utm_campaign','')<>'' OR COALESCE(v.attribution->>'fbc','')<>'' THEN 'missing_pixel' ELSE 'organic' END)
		FROM payment_stripe_subscription_intents v JOIN users u ON u.id=v.user_id WHERE v.id=$1
		ON CONFLICT(business_key) DO NOTHING`, intentID)
	return err
}

func bindStripeVIPTx(ctx context.Context, tx pgx.Tx, v stripeVIPIntent, proof payment.StripeVIPSubscription) error {
	if proof.IntentID != v.ID || proof.ID == "" || proof.CustomerID == "" ||
		(proof.ManagedPayments != (v.Offer.SalesMode == payment.StripeSaleMoR)) ||
		(v.SubscriptionID != "" && v.SubscriptionID != proof.ID) || (v.CustomerID != "" && v.CustomerID != proof.CustomerID) ||
		(v.SubscriptionID == "" && proof.PriceID != v.Offer.PriceID && stripeLocalAgreementStatus(proof) != "CANCELLED") {
		return ErrCheckoutUnavailable
	}
	var currentID, currentCustomer, currentStatus string
	var cancelledAt, observedAt *time.Time
	err := tx.QueryRow(ctx, `SELECT COALESCE(subscription_ref,''),COALESCE(customer_ref,''),status,cancelled_at,provider_observed_at FROM payment_stripe_subscription_intents
		WHERE id=$1 AND account_id=$2 FOR UPDATE`, v.ID, v.AccountID).Scan(&currentID, &currentCustomer, &currentStatus, &cancelledAt, &observedAt)
	if err != nil {
		return err
	}
	if currentStatus == "closed" || (currentID != "" && currentID != proof.ID) || (currentCustomer != "" && currentCustomer != proof.CustomerID) {
		return ErrCheckoutUnavailable
	}
	status := stripeLocalAgreementStatus(proof)
	if (observedAt != nil && !proof.ObservedAt.After(*observedAt)) ||
		(status != "CANCELLED" && cancelledAt != nil && !proof.ObservedAt.After(*cancelledAt)) {
		// A poll begun before cancellation can commit after the cancellation response.
		return nil
	}
	tag, err := tx.Exec(ctx, `INSERT INTO user_subscriptions(user_id,subscription_id,plan_id,slot_id,template_id,status,cycle,price_cents,currency,
		payment_method,checkout_account_id,checkout_intent_id,checkout_return_path,attribution,capi_subscribe_state,vip_offer_version_id,product_id,product_snapshot,cancelled_at)
		SELECT v.user_id,$2,v.price_ref,v.slot_id,v.template_id,$3::varchar(30),v.cycle,v.amount_minor,'USD','stripe',
		  v.account_id,v.id,v.return_path,v.attribution,'eligible',v.offer_version_id,v.product_id,v.product_snapshot,CASE WHEN $3::varchar(30)='CANCELLED' THEN now() END FROM payment_stripe_subscription_intents v WHERE v.id=$1
		ON CONFLICT(subscription_id) DO UPDATE SET status=CASE
		  WHEN EXCLUDED.status='CANCELLED' THEN 'CANCELLED'
		  WHEN user_subscriptions.status='CANCEL_PENDING' THEN 'CANCEL_PENDING'
		  ELSE EXCLUDED.status END,updated_at=now(),
		  cancelled_at=CASE WHEN EXCLUDED.status='CANCELLED' THEN COALESCE(user_subscriptions.cancelled_at,now()) END,
		  next_billing_time=CASE WHEN EXCLUDED.status<>'ACTIVE' OR user_subscriptions.status='CANCEL_PENDING' THEN NULL ELSE user_subscriptions.next_billing_time END
		WHERE user_subscriptions.user_id=EXCLUDED.user_id AND user_subscriptions.payment_method='stripe'
		  AND user_subscriptions.checkout_account_id=EXCLUDED.checkout_account_id
		  AND user_subscriptions.checkout_intent_id=EXCLUDED.checkout_intent_id`, v.ID, proof.ID, status)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrCheckoutUnavailable
	}
	if _, err := tx.Exec(ctx, `UPDATE payment_vip_checkout_intents
		SET subscription_id=$2,status=CASE WHEN $3='CANCELLED' AND status='Pending' THEN 'Canceled' ELSE status END,updated_at=now()
		WHERE stripe_intent_id=$1::uuid`, v.ID, proof.ID, status); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE payment_stripe_subscription_intents SET status='bound',subscription_ref=$2,customer_ref=$3,
		approval_url=NULL,cancelled_at=CASE WHEN $4='CANCELLED' THEN COALESCE(cancelled_at,now()) ELSE NULL END,
		cancel_next_at=CASE WHEN $4='CANCELLED' THEN NULL ELSE cancel_next_at END,
		provider_observed_at=COALESCE($5::timestamptz,provider_observed_at) WHERE id=$1`,
		v.ID, proof.ID, proof.CustomerID, status, verifiedPaidAt(proof.ObservedAt))
	return err
}

func stripeLocalAgreementStatus(proof payment.StripeVIPSubscription) string {
	if proof.CancelAtPeriodEnd || proof.Status == "canceled" || proof.Status == "incomplete_expired" {
		return "CANCELLED"
	}
	switch proof.Status {
	case "active":
		return "ACTIVE"
	case "past_due", "unpaid", "paused":
		return "SUSPENDED"
	}
	return "PENDING"
}

const stripeVIPPurchaseStateSQL = `CASE WHEN o.status='Refunded' THEN 'refunded'
		WHEN p.admitted AND o.fulfilled_at IS NOT NULL THEN 'completed'
		WHEN v.status='closed' THEN 'closed'
		WHEN EXISTS(SELECT 1 FROM payment_vip_checkout_intents u WHERE u.stripe_intent_id=v.id AND u.switch_to_provider IS NOT NULL) THEN 'confirming'
		WHEN p.review_reason IS NOT NULL OR (v.status='creating' AND (v.create_lookup_only OR v.retry_until<=now())) THEN 'review'
		WHEN v.status='buyer_action' AND v.approval_url IS NOT NULL THEN 'awaiting_approval'
		WHEN v.cancelled_at IS NOT NULL THEN 'closed'
		ELSE 'confirming' END`

const stripeVIPFirstPeriodJoin = `LEFT JOIN LATERAL (
		SELECT b.order_id,b.review_reason,b.admitted FROM payment_stripe_invoice_periods b
		WHERE b.intent_id=v.id AND NOT EXISTS (
			SELECT 1 FROM payment_stripe_invoice_periods revision
			WHERE revision.intent_id=b.intent_id AND revision.revises_invoice_ref=b.invoice_ref AND revision.revision_verified
		)
		ORDER BY (b.billing_reason='subscription_create') DESC,b.service_start NULLS LAST,b.order_id LIMIT 1
	) p ON true LEFT JOIN recharge_orders o ON o.id=p.order_id`

const stripeVIPPurchaseProjection = `SELECT v.id::text,v.slot_id,v.amount_minor,v.return_path,COALESCE(v.approval_url,''),COALESCE(v.subscription_ref,''),
	` + stripeVIPPurchaseStateSQL + `
	FROM payment_stripe_subscription_intents v JOIN payment_channel_accounts a ON a.id=v.account_id
	` + stripeVIPFirstPeriodJoin + `
	WHERE v.user_id=$1 AND a.provider='stripe' AND a.account_key=$2 AND a.environment=$3 AND v.checkout_environment=$4`

func scanStripeVIPPurchase(row pgx.Row) (PurchaseIntent, error) {
	result := PurchaseIntent{OrderType: "subscription", Currency: "USD", Provider: payment.ProviderStripe}
	err := row.Scan(&result.ID, &result.SlotID, &result.AmountMinor, &result.ReturnPath, &result.ApprovalURL, &result.ExternalOrderID, &result.Status)
	if result.Status != "awaiting_approval" {
		result.ApprovalURL = ""
	}
	return result, err
}

func stripeVIPPurchase(ctx context.Context, user int64, account payment.WebhookAccount, id string) (*PurchaseIntent, error) {
	result, err := scanStripeVIPPurchase(db.Pool(ctx).QueryRow(ctx, stripeVIPPurchaseProjection+` AND v.id=$5`,
		user, account.AccountKey, account.Environment, conversion.Environment(), id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrCheckoutUnavailable
	}
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func recentStripeVIPPurchases(ctx context.Context, user int64, account payment.WebhookAccount) ([]PurchaseIntent, error) {
	rows, err := db.Pool(ctx).Query(ctx, stripeVIPPurchaseProjection+`
		AND NOT EXISTS (SELECT 1 FROM payment_vip_checkout_intents vc WHERE vc.stripe_intent_id=v.id)
		ORDER BY v.created_at DESC,v.id LIMIT 20`,
		user, account.AccountKey, account.Environment, conversion.Environment())
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (PurchaseIntent, error) { return scanStripeVIPPurchase(row) })
}

func stripeVIPForOwner(ctx context.Context, account payment.WebhookAccount, user int64, subscription string) (stripeVIPIntent, error) {
	v, err := scanStripeVIP(db.Pool(ctx).QueryRow(ctx, `SELECT `+stripeVIPFields+` FROM payment_stripe_subscription_intents v
		JOIN payment_channel_accounts a ON a.id=v.account_id WHERE v.user_id=$1 AND v.subscription_ref=$2 AND v.status='bound'
		  AND a.provider='stripe' AND a.account_key=$3 AND a.environment=$4 AND v.checkout_environment=$5`,
		user, subscription, account.AccountKey, account.Environment, conversion.Environment()))
	if errors.Is(err, pgx.ErrNoRows) {
		return v, ErrCheckoutUnavailable
	}
	return v, err
}

func bindStripeVIP(ctx context.Context, v stripeVIPIntent, proof payment.StripeVIPSubscription) error {
	tx, err := db.Pool(ctx).Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if err := bindStripeVIPTx(ctx, tx, v, proof); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func queueStripeVIPCancellation(ctx context.Context, v stripeVIPIntent, reason string) error {
	tx, err := db.Pool(ctx).Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	tag, err := tx.Exec(ctx, `UPDATE payment_stripe_subscription_intents SET cancel_key=COALESCE(cancel_key,$2),
		cancel_next_at=COALESCE(cancel_next_at,now()) WHERE id=$1 AND subscription_ref=$3 AND cancelled_at IS NULL`,
		v.ID, uuid.NewString(), v.SubscriptionID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 0 {
		_, err = tx.Exec(ctx, `UPDATE user_subscriptions SET status='CANCEL_PENDING',cancel_reason=COALESCE(cancel_reason,NULLIF($3,'')),updated_at=now()
			WHERE checkout_intent_id=$1 AND subscription_id=$2 AND payment_method='stripe' AND status NOT IN ('CANCELLED','EXPIRED')`,
			v.ID, v.SubscriptionID, reason)
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

type stripeVIPCancellation struct {
	intentID, key string
	maySend       bool
}

func claimStripeVIPCancellations(ctx context.Context, account payment.WebhookAccount) ([]stripeVIPCancellation, error) {
	rows, err := db.Pool(ctx).Query(ctx, `WITH due AS (
		SELECT v.id FROM payment_stripe_subscription_intents v JOIN payment_channel_accounts a ON a.id=v.account_id
		WHERE a.provider='stripe' AND a.account_key=$1 AND a.environment=$2 AND v.checkout_environment=$3
		  AND v.status='bound' AND v.cancel_key IS NOT NULL AND v.cancelled_at IS NULL AND v.cancel_next_at<=now()
		ORDER BY v.cancel_next_at,v.id LIMIT 5 FOR UPDATE OF v SKIP LOCKED
	)
	UPDATE payment_stripe_subscription_intents v SET cancel_first_sent_at=COALESCE(cancel_first_sent_at,now()),
		cancel_retry_until=COALESCE(cancel_retry_until,now()+make_interval(secs=>$4::int)),
		cancel_next_at=now()+CASE WHEN cancel_lookup_only OR cancel_retry_until<=now() THEN interval '1 hour' ELSE interval '1 minute' END
	FROM due WHERE v.id=due.id RETURNING v.id::text,v.cancel_key::text,NOT v.cancel_lookup_only AND v.cancel_retry_until>now()`, account.AccountKey,
		account.Environment, conversion.Environment(), int(stripeCreateRetryWindow/time.Second))
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (stripeVIPCancellation, error) {
		var job stripeVIPCancellation
		err := row.Scan(&job.intentID, &job.key, &job.maySend)
		return job, err
	})
}

func advanceStripeVIPInvoiceScan(ctx context.Context, v stripeVIPIntent, next string) error {
	_, err := db.Pool(ctx).Exec(ctx, `UPDATE payment_stripe_subscription_intents SET invoice_scan_cursor=NULLIF($2,'')
		WHERE id=$1 AND COALESCE(invoice_scan_cursor,'')=$3`, v.ID, next, v.InvoiceScanCursor)
	return err
}

type stripeVIPPendingInvoice struct {
	intentID        string
	invoiceRef      string
	subscriptionRef string
}

func claimStripeVIPPendingSettlementInvoices(ctx context.Context, account payment.WebhookAccount) ([]stripeVIPPendingInvoice, error) {
	rows, err := db.Pool(ctx).Query(ctx, `WITH due AS (
		SELECT p.account_id, p.invoice_ref, p.intent_id, v.subscription_ref
		FROM payment_stripe_invoice_periods p
		JOIN payment_stripe_subscription_intents v ON v.id = p.intent_id
		JOIN payment_channel_accounts a ON a.id = p.account_id
		WHERE a.provider = 'stripe' AND a.account_key = $1 AND a.environment = $2
		  AND v.checkout_environment = $3
		  AND p.settlement_state = 'pending_settlement'
		  AND NOT p.admitted
		  AND COALESCE(v.subscription_ref, '') <> ''
		  AND COALESCE(p.settlement_next_check_at, now()) <= now()
		ORDER BY p.settlement_next_check_at NULLS FIRST, p.invoice_ref
		LIMIT 5 FOR UPDATE OF p SKIP LOCKED
	), leased AS (
		UPDATE payment_stripe_invoice_periods p
		SET settlement_next_check_at = now() + make_interval(secs => $4::int),
		    updated_at = now()
		FROM due
		WHERE p.account_id = due.account_id AND p.invoice_ref = due.invoice_ref
		RETURNING due.intent_id::text, due.invoice_ref, due.subscription_ref
	) SELECT intent_id, invoice_ref, subscription_ref FROM leased`,
		account.AccountKey, account.Environment, conversion.Environment(), int(stripeSettlementPollInterval/time.Second))
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (stripeVIPPendingInvoice, error) {
		var item stripeVIPPendingInvoice
		err := row.Scan(&item.intentID, &item.invoiceRef, &item.subscriptionRef)
		return item, err
	})
}
