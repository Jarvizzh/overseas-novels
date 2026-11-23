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
	"reader-backend/internal/novel"
	"reader-backend/internal/payment"
	"reader-backend/internal/payment/paypal"
)

var (
	ErrCheckoutConflict    = errors.New("A checkout for this book already exists. Check your purchases before starting another.")
	ErrCheckoutKeyConflict = errors.New("This request key has already been used with different checkout details.")
	ErrCheckoutPending     = errors.New("The checkout is still being confirmed. Check its status before trying again.")
	ErrCheckoutPaused      = errors.New("This payment method is currently unavailable for new purchases.")
)

type PurchaseIntent struct {
	ID                  string         `json:"intent_id"`
	ProductID           int64          `json:"product_id,omitempty"`
	SlotID              int            `json:"slot_id"`
	NovelID             int64          `json:"novel_id,omitempty"`
	OrderType           string         `json:"order_type"`
	Cycle               string         `json:"cycle,omitempty"`
	AmountMinor         int64          `json:"amount_minor"`
	Currency            string         `json:"currency"`
	Coins               int            `json:"coins"`
	Status              string         `json:"status"`
	ReturnPath          string         `json:"return_path,omitempty"`
	ApprovalURL         string         `json:"approve_url,omitempty"`
	Provider            string         `json:"provider,omitempty"`
	ExternalOrderID     string         `json:"order_id,omitempty"`
	ConfirmationCode    captureOutcome `json:"confirmation_code,omitempty"`
	ConfirmationMessage string         `json:"confirmation_message,omitempty"`
	AvailableChannels   []string       `json:"available_channels,omitempty"`
	Badge               string         `json:"badge,omitempty"`
	DisplayAlias        string         `json:"display_alias,omitempty"`
}

type purchaseRequest struct {
	key        uuid.UUID
	productID  int64
	slotID     int
	novelID    int64
	returnPath string
	nonVIPOnly bool
}

var purchaseProjection = `
	COALESCE(o.checkout_id::text,''),COALESCE(o.recharge_slot_id,0),COALESCE(o.novel_id,0),o.order_type,o.amount_cents,o.currency,
	COALESCE(o.coins,0)+COALESCE(o.bonus_coins_credited,0),
	CASE
		WHEN o.status='Refunded' THEN 'refunded'
		WHEN o.fulfilled_at IS NOT NULL THEN 'completed'
		WHEN o.status='Pending' AND (` + agedLegacyBook("o") + `) THEN 'review'
		WHEN o.reconciliation_note IS NOT NULL OR EXISTS(SELECT 1 FROM payment_deferred_fulfillment d WHERE d.order_id=o.id AND d.resolved_at IS NULL) THEN 'review'
		WHEN o.paid_at IS NOT NULL OR o.status IN ('Success','Paid') THEN 'confirming'
		WHEN o.status='Failed' OR a.status='closed' THEN 'retryable'
		WHEN a.status='creating' AND (a.create_lookup_only OR a.retry_until<=now()) THEN 'review'
		WHEN a.status='capturing' AND (a.capture_lookup_only OR a.capture_retry_until<=now()) THEN 'review'
		WHEN a.paypal_switch_token IS NOT NULL THEN 'confirming'
		WHEN a.status IN ('creating','capturing') OR (a.status='buyer_action' AND a.capture_lookup_only) THEN 'confirming'
		WHEN a.status='buyer_action' AND a.can_resume THEN 'awaiting_approval'
		WHEN a.status='buyer_action' THEN 'review'
		WHEN o.external_ref_id IS NOT NULL THEN 'confirming'
		ELSE 'ready'
	END,o.checkout_return_path,COALESCE(a.approval_url,''),COALESCE(a.provider_order_id,o.external_ref_id,''),
    COALESCE(CASE WHEN a.status IN ('creating','buyer_action','capturing','settled') THEN a.provider
        WHEN o.paid_at IS NOT NULL THEN o.payment_method END,''),
    CASE WHEN o.status='Pending' AND (` + agedLegacyBook("o") + `) THEN '` + string(captureLegacyBookReview) + `'
      WHEN a.paypal_switch_token IS NOT NULL AND o.status='Pending' THEN 'payment_switch_pending'
      WHEN o.status IN ('Pending','Failed') AND a.status IN ('buyer_action','capturing','closed')
        THEN COALESCE(a.capture_outcome,'') ELSE '' END,
    COALESCE(o.product_id,0), COALESCE(o.product_snapshot,'{}'::jsonb)`

const purchaseAttemptJoin = ` LEFT JOIN LATERAL (
	SELECT p.status,p.provider,p.provider_order_id,p.approval_url,p.retry_until,p.capture_retry_until,p.create_lookup_only,p.capture_lookup_only,p.capture_outcome,p.paypal_switch_token,
		p.provider='stripe' OR EXISTS(SELECT 1 FROM payment_channel_accounts a WHERE a.id=p.account_id AND a.provider='paypal'
			AND a.environment=CASE WHEN o.checkout_environment='production' THEN 'live' ELSE o.checkout_environment END) AS can_resume
	FROM payment_checkout_attempts p
	WHERE p.order_id=o.id ORDER BY (p.status IN ('creating','buyer_action','capturing')) DESC, p.created_at DESC,p.id DESC LIMIT 1
) a ON true`

func scanPurchase(row pgx.Row) (*PurchaseIntent, error) {
	var p PurchaseIntent
	var snapJSON []byte
	if err := row.Scan(&p.ID, &p.SlotID, &p.NovelID, &p.OrderType, &p.AmountMinor, &p.Currency, &p.Coins, &p.Status,
		&p.ReturnPath, &p.ApprovalURL, &p.ExternalOrderID, &p.Provider, &p.ConfirmationCode, &p.ProductID, &snapJSON); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrCheckoutUnavailable
		}
		return nil, err
	}
	if len(snapJSON) > 0 {
		var snap struct {
			Badge        string `json:"badge"`
			DisplayAlias string `json:"display_alias"`
		}
		if _ = json.Unmarshal(snapJSON, &snap); snap.Badge != "" || snap.DisplayAlias != "" {
			p.Badge = snap.Badge
			p.DisplayAlias = snap.DisplayAlias
		}
	}
	if p.ConfirmationCode != "" {
		p.ConfirmationMessage = p.ConfirmationCode.publicMessage()
	}
	if p.Status != "awaiting_approval" {
		p.ApprovalURL = ""
	}
	return &p, nil
}

func (r *dbRepository) preparePurchase(ctx context.Context, userID int64, request purchaseRequest, attribution checkoutAttribution) (*PurchaseIntent, error) {
	evaluation := checkoutEvaluation{stage: purchaseIntentStage, userID: userID, slotID: request.slotID}
	return evaluateCheckout(ctx, evaluation, func(tx pgx.Tx, e *checkoutEvaluation) (*PurchaseIntent, error) {
		return r.reservePurchaseIntent(ctx, tx, e, request, attribution)
	})
}

func (r *dbRepository) reservePurchaseIntent(ctx context.Context, tx pgx.Tx, evaluation *checkoutEvaluation, request purchaseRequest, attribution checkoutAttribution) (*PurchaseIntent, error) {
	userID := evaluation.userID
	env := conversion.Environment()
	var orderID, previousProductID, previousNovel int64
	var vipIntentID string
	var previousSlot int
	var previousPath string
	err := tx.QueryRow(ctx, `SELECT COALESCE(order_id,0),COALESCE(vip_intent_id::text,''),COALESCE(product_id,0),slot_id,novel_id,return_path
		FROM payment_checkout_requests
		WHERE environment=$1 AND user_id=$2 AND request_key=$3`, env, userID, request.key).
		Scan(&orderID, &vipIntentID, &previousProductID, &previousSlot, &previousNovel, &previousPath)
	switch {
	case err == nil:
		evaluation.resumed = true
		mismatch := (request.productID > 0 && previousProductID != request.productID) ||
			(request.slotID > 0 && previousSlot != request.slotID) ||
			(request.slotID == 0 && previousSlot != 0) ||
			previousNovel != request.novelID ||
			previousPath != request.returnPath
		if mismatch {
			return nil, ErrCheckoutKeyConflict
		}
		if vipIntentID != "" {
			evaluation.checkoutID = vipIntentID
			row, loadErr := lockVIPCheckoutIntent(ctx, tx, userID, vipIntentID)
			if loadErr != nil {
				return nil, loadErr
			}
			if err := expireStaleStripeVIPQuoteTx(ctx, tx, &row); err != nil {
				return nil, err
			}
			return buildVIPPurchaseIntentView(ctx, tx, row)
		}
		evaluation.orderID = orderID
	case errors.Is(err, pgx.ErrNoRows):
		prod, err := resolveProductForCheckoutTx(ctx, tx, userID, request.productID, request.slotID, currentPaymentLedgerEnvironment())
		if err != nil {
			return nil, err
		}
		if prod.OrderType == "subscription" {
			if request.nonVIPOnly {
				return nil, ErrCheckoutUnavailable
			}
			return reserveVIPCheckoutIntentTx(ctx, tx, evaluation, prod, request, attribution)
		}
		orderID, err = r.reservePurchase(ctx, tx, env, evaluation, prod, request, attribution)
		if err != nil {
			return nil, err
		}
	default:
		return nil, err
	}
	intent, err := scanPurchase(tx.QueryRow(ctx, `SELECT `+purchaseProjection+` FROM recharge_orders o `+
		purchaseAttemptJoin+` WHERE o.id=$1`, orderID))
	if err != nil {
		return nil, err
	}
	if intent.ProductID > 0 && intent.Status != "completed" && intent.Status != "refunded" {
		intent.AvailableChannels = loadAvailableProductChannels(ctx, intent.ProductID, intent.OrderType, currentPaymentLedgerEnvironment())
	}
	evaluation.orderID, evaluation.checkoutID = orderID, intent.ID
	return intent, nil
}

func (r *dbRepository) reservePurchase(ctx context.Context, tx pgx.Tx, env string, evaluation *checkoutEvaluation, prod resolvedProduct, request purchaseRequest, attribution checkoutAttribution) (int64, error) {
	userID := evaluation.userID
	if prod.PriceCents <= 0 || prod.Coins < 0 || prod.Bonus < 0 || (prod.OrderType != "single" && prod.OrderType != "whole_book") {
		return 0, ErrCheckoutUnavailable
	}
	var orderID, novelID int64
	var err error
	if prod.OrderType == "whole_book" {
		novelID = request.novelID
		orderID, err = reserveWholeBook(ctx, tx, env, userID, novelID, prod.ID, request.slotID)
		if err != nil {
			return 0, err
		}
	}
	evaluation.resumed = orderID != 0
	if orderID == 0 {
		orderID, err = insertCheckout(ctx, tx, uuid.NewString(), userID, prod, novelID, attribution, &env, request.returnPath)
		if err != nil {
			return 0, err
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO payment_checkout_requests(environment,user_id,request_key,product_id,slot_id,novel_id,return_path,order_id)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, env, userID, request.key, prod.ID, request.slotID, request.novelID, request.returnPath, orderID)
	return orderID, err
}

func reserveWholeBook(ctx context.Context, tx pgx.Tx, env string, userID, novelID int64, productID int64, slotID int) (int64, error) {
	var id int64
	var resumable bool
	err := tx.QueryRow(ctx, `SELECT id,COALESCE(status='Pending' AND checkout_environment=$3
		AND (COALESCE(product_id,0)=$4 OR ($5>0 AND product_id IS NULL AND COALESCE(recharge_slot_id,0)=$5))
		AND ($5=0 OR COALESCE(recharge_slot_id,0)=$5)
		AND NOT (`+legacyWholeBookReservation("$3")+`),false)
		FROM recharge_orders
		WHERE user_id=$1 AND novel_id=$2 AND order_type='whole_book'
		  AND ((`+legacyWholeBookReservation("$3")+`) OR (`+novel.WholeBookEnvironmentPredicate("$3")+` AND (`+novel.WholeBookEntitlementPredicate+`
		       OR (status IN ('Success','Paid','RefundPending') AND fulfilled_at IS NULL)
		       OR (status='Pending' AND (checkout_environment=$3 OR external_ref_id IS NOT NULL)))))
		ORDER BY 2,id
		LIMIT 1 FOR UPDATE`, userID, novelID, env, productID, slotID).Scan(&id, &resumable)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if !resumable {
		return 0, ErrCheckoutConflict
	}
	return id, nil
}

func (r *dbRepository) purchaseByReference(ctx context.Context, userID int64, intentID, paypalOrderID string) (*PurchaseIntent, error) {
	intent, err := scanPurchase(db.Pool(ctx).QueryRow(ctx, `SELECT `+purchaseProjection+` FROM recharge_orders o `+
		purchaseAttemptJoin+` WHERE o.user_id=$1 AND o.order_type IN ('single','whole_book') AND o.payment_method IN ('paypal','stripe')
			AND CASE WHEN (`+agedLegacyBookLookupOnly("o")+`) AND o.status IN ('Pending','Paid')
				THEN (`+agedLegacyBookVisible("o", "$2")+`)
				ELSE (o.checkout_environment=$2 OR (o.checkout_environment IS NULL AND $4<>'' AND o.external_ref_id=$4)) END
			AND ($3='' OR o.checkout_id=NULLIF($3,'')::uuid)
			AND ($4='' OR (o.payment_method='paypal' AND o.external_ref_id=$4) OR EXISTS (SELECT 1 FROM payment_checkout_attempts p
				WHERE p.order_id=o.id AND p.provider='paypal' AND p.provider_order_id=$4))`,
		userID, conversion.Environment(), intentID, paypalOrderID))
	if err != nil {
		return nil, err
	}
	if intent.ProductID > 0 && intent.Status != "completed" && intent.Status != "refunded" {
		intent.AvailableChannels = loadAvailableProductChannels(ctx, intent.ProductID, intent.OrderType, currentPaymentLedgerEnvironment())
	}
	return intent, nil
}

func (r *dbRepository) recentPurchases(ctx context.Context, userID int64) ([]PurchaseIntent, error) {
	rows, err := db.Pool(ctx).Query(ctx, `SELECT `+purchaseProjection+` FROM recharge_orders o `+
		purchaseAttemptJoin+` WHERE o.user_id=$1 AND o.checkout_id IS NOT NULL
			AND CASE WHEN (`+agedLegacyBookLookupOnly("o")+`) AND o.status IN ('Pending','Paid')
				THEN (`+agedLegacyBookVisible("o", "$2")+`) ELSE o.checkout_environment=$2 END
			ORDER BY (`+agedLegacyBookRecovery("o", "$2")+`) DESC,o.id DESC LIMIT 20`, userID, conversion.Environment())
	if err != nil {
		return nil, err
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (PurchaseIntent, error) {
		intent, err := scanPurchase(row)
		if err != nil {
			return PurchaseIntent{}, err
		}
		return *intent, nil
	})
	if err != nil {
		return nil, err
	}
	env := currentPaymentLedgerEnvironment()
	for i := range items {
		if items[i].ProductID > 0 && items[i].Status != "completed" && items[i].Status != "refunded" {
			items[i].AvailableChannels = loadAvailableProductChannels(ctx, items[i].ProductID, items[i].OrderType, env)
		}
	}
	return items, nil
}

type paypalCheckoutAttempt struct {
	id          string
	orderID     int64
	userID      int64
	intentID    string
	amount      int64
	orderType   string
	returnURL   string
	cancelURL   string
	account     payment.WebhookAccount
	externalID  string
	approvalURL string
	send        bool
}

func (r *dbRepository) reservePayPalAttempt(ctx context.Context, userID int64, intentID, returnURL, cancelURL string, scope payment.WebhookAccount) (*paypalCheckoutAttempt, error) {
	evaluation := checkoutEvaluation{stage: paymentAttemptStage, userID: userID, provider: payment.ProviderPayPal}
	return evaluateCheckout(ctx, evaluation, func(tx pgx.Tx, e *checkoutEvaluation) (*paypalCheckoutAttempt, error) {
		return r.reservePayPalAttemptTx(ctx, tx, e, intentID, returnURL, cancelURL, scope)
	})
}

func (r *dbRepository) reservePayPalAttemptTx(ctx context.Context, tx pgx.Tx, evaluation *checkoutEvaluation, intentID, returnURL, cancelURL string, scope payment.WebhookAccount) (*paypalCheckoutAttempt, error) {
	var attempt paypalCheckoutAttempt
	attempt.intentID, attempt.account, attempt.userID = intentID, scope, evaluation.userID
	var accountID int64
	var newOrdersEnabled, productBindingOK bool
	err := tx.QueryRow(ctx, `SELECT o.id,o.amount_cents,o.order_type,a.id,a.new_orders_enabled,
		(o.product_id IS NULL OR (
			EXISTS(SELECT 1 FROM product_publications pub WHERE pub.product_id=o.product_id AND pub.environment=a.environment AND pub.status='published')
			AND EXISTS(SELECT 1 FROM product_channel_bindings b WHERE b.product_id=o.product_id AND b.account_id=a.id AND b.provider='paypal' AND b.environment=a.environment AND b.enabled=true AND b.validation_status='verified')
		))
		FROM recharge_orders o
		CROSS JOIN payment_channel_accounts a
		WHERE o.user_id=$1 AND o.checkout_environment=$2 AND o.checkout_id=$3 AND o.status='Pending'
			AND a.provider='paypal' AND a.account_key=$4 AND a.environment=$5
		FOR UPDATE OF o`, attempt.userID, conversion.Environment(), attempt.intentID, scope.AccountKey, scope.Environment).
		Scan(&attempt.orderID, &attempt.amount, &attempt.orderType, &accountID, &newOrdersEnabled, &productBindingOK)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrCheckoutUnavailable
	}
	if err != nil {
		return nil, err
	}

	evaluation.accountID, evaluation.orderID, evaluation.checkoutID = accountID, attempt.orderID, attempt.intentID
	if attempt.orderType == "whole_book" {
		if err := checkOtherWholeBookReservation(ctx, tx, attempt.orderID, conversion.Environment()); err != nil {
			return nil, err
		}
	}
	var currentAccount int64
	var currentStatus string
	var needsReview bool
	err = tx.QueryRow(ctx, `SELECT id::text,account_id,status,COALESCE(provider_order_id,''),COALESCE(approval_url,''),return_url,cancel_url,
        CASE status
            WHEN 'creating' THEN create_lookup_only OR COALESCE(retry_until<=now(),false)
            WHEN 'capturing' THEN capture_lookup_only OR COALESCE(capture_retry_until<=now(),false)
            ELSE false
        END
		FROM payment_checkout_attempts WHERE order_id=$1 AND status IN ('creating','buyer_action','capturing') FOR UPDATE`, attempt.orderID).
		Scan(&attempt.id, &currentAccount, &currentStatus, &attempt.externalID, &attempt.approvalURL, &attempt.returnURL, &attempt.cancelURL, &needsReview)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		if !newOrdersEnabled || !productBindingOK {
			return nil, ErrCheckoutPaused
		}
		attempt.id, attempt.returnURL, attempt.cancelURL, currentStatus = uuid.NewString(), returnURL, cancelURL, "creating"
		_, err = tx.Exec(ctx, `INSERT INTO payment_checkout_attempts(id,order_id,provider,account_id,sales_mode,status,return_url,cancel_url)
			VALUES($1,$2,'paypal',$3,'direct','creating',$4,$5)`, attempt.id, attempt.orderID, accountID, returnURL, cancelURL)
		if err != nil {
			return nil, err
		}
	case err != nil:
		return nil, err
	case currentAccount != accountID:
		if needsReview {
			return nil, errPaymentAttemptReview
		}
		return nil, ErrCheckoutPending
	default:
		evaluation.resumed = true
		// The original PayPal attempt still owns the order slot. Invalidating
		// its switch token makes an in-flight Stripe switch proof stale.
		if _, err := tx.Exec(ctx, `UPDATE payment_checkout_attempts SET paypal_switch_token=NULL
			WHERE id=$1 AND provider='paypal' AND status='buyer_action' AND paypal_switch_token IS NOT NULL
			AND capture_first_sent_at IS NULL`, attempt.id); err != nil {
			return nil, err
		}
	}
	if currentStatus == "creating" {
		attempt.send, err = leaseCheckoutCreate(ctx, tx, attempt.id)
		if err != nil {
			return nil, err
		}
	}
	if needsReview {
		evaluation.assessment = checkoutDecision{checkoutReview, reasonAttemptReview}
	} else if currentStatus == "creating" && !attempt.send {
		evaluation.assessment = checkoutDecision{checkoutVerify, reasonAttemptInProgress}
	}
	return &attempt, nil
}

func leaseCheckoutCreate(ctx context.Context, tx pgx.Tx, attemptID string) (bool, error) {
	tag, err := tx.Exec(ctx, `UPDATE payment_checkout_attempts
		SET first_sent_at=COALESCE(first_sent_at,now()),
		    retry_until=COALESCE(retry_until,now()+interval '1 second'*$2::int),
		    next_create_at=now()+interval '1 minute'
		WHERE id=$1 AND status='creating' AND NOT create_lookup_only
		AND next_create_at<=now() AND (retry_until IS NULL OR retry_until>now())
		AND EXISTS (SELECT 1 FROM recharge_orders o WHERE o.id=payment_checkout_attempts.order_id AND o.status='Pending'
			AND (o.order_type<>'whole_book' OR NOT `+wholeBookWriteBlocked("o", "$3")+`))`,
		attemptID, int(paypal.OrderWriteWindow/time.Second), conversion.Environment())
	return tag.RowsAffected() == 1, err
}

func (r *dbRepository) finishPayPalAttempt(ctx context.Context, attempt *paypalCheckoutAttempt, providerOrderID, approvalURL string) error {
	tx, err := db.Pool(ctx).Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if err := bindCheckoutTx(ctx, tx, attempt.intentID, providerOrderID, true); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE payment_checkout_attempts SET provider_order_id=$2,approval_url=$3,status='buyer_action'
		WHERE id=$1 AND status='creating'`, attempt.id, providerOrderID, approvalURL)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		var alreadyBound bool
		if err := tx.QueryRow(ctx, `SELECT provider_order_id=$2 FROM payment_checkout_attempts WHERE id=$1`, attempt.id, providerOrderID).
			Scan(&alreadyBound); err != nil {
			return err
		}
		if !alreadyBound {
			return ErrCheckoutUnavailable
		}
	}
	return tx.Commit(ctx)
}

func (r *dbRepository) matchPayPalAttempt(ctx context.Context, providerOrderID string, scope payment.WebhookAccount) (bool, error) {
	var matched bool
	err := db.Pool(ctx).QueryRow(ctx, `SELECT NOT EXISTS(
		SELECT 1 FROM payment_checkout_attempts p JOIN payment_channel_accounts a ON a.id=p.account_id
		WHERE p.provider='paypal' AND p.provider_order_id=$1 AND (a.account_key,a.environment)<>($2,$3)
	)`, providerOrderID, scope.AccountKey, scope.Environment).Scan(&matched)
	return matched, err
}

func (r *dbRepository) leaseCheckoutCapture(ctx context.Context, providerOrderID string) (bool, error) {
	var allow bool
	err := db.Pool(ctx).QueryRow(ctx, `WITH claimed AS (
		UPDATE payment_checkout_attempts
		SET status='capturing',capture_first_sent_at=COALESCE(capture_first_sent_at,now()),
		    capture_retry_until=COALESCE(capture_retry_until,now()+interval '1 second'*$2::int),
		    next_capture_at=now()+interval '1 minute'
		WHERE provider='paypal' AND provider_order_id=$1 AND status IN ('buyer_action','capturing')
		  AND NOT capture_lookup_only AND paypal_switch_token IS NULL AND (next_capture_at IS NULL OR next_capture_at<=now())
		  AND (capture_retry_until IS NULL OR capture_retry_until>now())
		  AND EXISTS (SELECT 1 FROM recharge_orders o WHERE o.id=payment_checkout_attempts.order_id AND o.status='Pending'
			AND (o.order_type<>'whole_book' OR NOT `+wholeBookWriteBlocked("o", "$4")+`))
		RETURNING id
	)
	SELECT EXISTS(SELECT 1 FROM claimed) OR (
		NOT EXISTS(SELECT 1 FROM payment_checkout_attempts WHERE provider='paypal' AND provider_order_id=$1)
		AND EXISTS(SELECT 1 FROM recharge_orders legacy WHERE legacy.external_ref_id=$1
			AND legacy.payment_method='paypal' AND legacy.status='Pending' AND NOT (`+agedLegacyBook("legacy")+`) AND (
				legacy.order_type<>'whole_book' OR (
					COALESCE(payment_order_environment(legacy),$3)=$3
					AND NOT EXISTS(SELECT 1 FROM recharge_orders other
						WHERE other.id<>legacy.id AND other.user_id=legacy.user_id AND other.novel_id=legacy.novel_id
						AND other.order_type='whole_book' AND COALESCE(payment_order_environment(other),$3)=$3
						AND (other.status IN ('Success','Paid','RefundPending') OR (other.status='Pending' AND
							(other.external_ref_id IS NOT NULL OR EXISTS(SELECT 1 FROM payment_checkout_attempts p
								WHERE p.order_id=other.id AND p.status IN ('creating','buyer_action','capturing')))))
					)
				)
			))
	)`, providerOrderID, int(paypal.OrderWriteWindow/time.Second), currentPaymentLedgerEnvironment(), conversion.Environment()).Scan(&allow)
	return allow, err
}

func (r *dbRepository) holdCheckoutCreate(ctx context.Context, attemptID string) error {
	_, err := db.Pool(ctx).Exec(ctx, `UPDATE payment_checkout_attempts SET create_lookup_only=true WHERE id=$1 AND status='creating'`, attemptID)
	return err
}

func (r *dbRepository) holdCheckoutCapture(ctx context.Context, providerOrderID string) error {
	_, err := db.Pool(ctx).Exec(ctx, `UPDATE payment_checkout_attempts SET capture_lookup_only=true
		WHERE provider='paypal' AND provider_order_id=$1 AND status='capturing'`, providerOrderID)
	return err
}

func (r *dbRepository) recordCaptureOutcome(ctx context.Context, orderID int64, externalID string, code captureOutcome) error {
	_, err := db.Pool(ctx).Exec(ctx, `UPDATE payment_checkout_attempts SET capture_outcome=$3
        WHERE order_id=$1 AND provider_order_id=$2 AND provider='paypal'
          AND status IN ('buyer_action','capturing') AND capture_outcome IS DISTINCT FROM $3
          AND (capture_outcome IS NULL OR capture_outcome IN ('payment_unavailable','payment_unconfirmed')
            OR $3 IN ('payment_blocked','payment_declined','payment_action_required'))`,
		orderID, externalID, code)
	return err
}

func claimCheckoutCreate(ctx context.Context, account payment.WebhookAccount) ([]paypalCheckoutAttempt, error) {
	rows, err := db.Pool(ctx).Query(ctx, `WITH due AS (
		SELECT p.id,o.user_id,o.checkout_id,o.amount_cents,o.order_type
		FROM payment_checkout_attempts p JOIN payment_channel_accounts a ON a.id=p.account_id
		JOIN recharge_orders o ON o.id=p.order_id
		WHERE p.provider='paypal' AND p.status='creating' AND NOT p.create_lookup_only
		  AND p.retry_until>now()+interval '3 minutes' AND p.next_create_at<=now() AND o.status='Pending'
		  AND a.account_key=$1 AND a.environment=$2 AND o.checkout_environment=$3
		  AND (o.order_type<>'whole_book' OR NOT `+wholeBookWriteBlocked("o", "$3")+`)
		ORDER BY p.next_create_at,p.id LIMIT 5 FOR UPDATE OF p SKIP LOCKED
	)
	UPDATE payment_checkout_attempts p SET next_create_at=now()+interval '1 minute'
	FROM due WHERE p.id=due.id RETURNING p.id::text,p.order_id,due.user_id,due.checkout_id::text,
		due.amount_cents,due.order_type,p.return_url,p.cancel_url`, account.AccountKey, account.Environment, conversion.Environment())
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (paypalCheckoutAttempt, error) {
		a := paypalCheckoutAttempt{account: account}
		err := row.Scan(&a.id, &a.orderID, &a.userID, &a.intentID, &a.amount, &a.orderType, &a.returnURL, &a.cancelURL)
		return a, err
	})
}

func (r *dbRepository) closePayPalAttempt(ctx context.Context, providerOrderID string) error {
	_, err := db.Pool(ctx).Exec(ctx, `UPDATE payment_checkout_attempts SET status='closed',paypal_switch_token=NULL
		WHERE provider='paypal' AND provider_order_id=$1 AND status IN ('buyer_action','capturing')`, providerOrderID)
	return err
}

func (r *dbRepository) isCheckoutOrigin(ctx context.Context, host string) (bool, error) {
	var allowed bool
	err := db.Pool(ctx).QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM system_domains WHERE status=1 AND
		lower(rtrim(regexp_replace(domain,'^https?://','','i'),'/'))=lower($1))`, host).Scan(&allowed)
	return allowed, err
}

type PaymentMethods struct {
	PayPalSingle bool   `json:"paypal_single"`
	PayPalVIP    bool   `json:"paypal_vip"`
	StripeSingle bool   `json:"stripe_single"`
	StripeVIP    bool   `json:"stripe_vip"`
	Environment  string `json:"environment,omitempty"`
}

func (r *dbRepository) payPalAvailability(ctx context.Context, account payment.WebhookAccount) (PaymentMethods, error) {
	methods := PaymentMethods{Environment: conversion.Environment()}
	err := db.Pool(ctx).QueryRow(ctx, `SELECT new_orders_enabled,new_subscriptions_enabled FROM payment_channel_accounts
		WHERE provider='paypal' AND account_key=$1 AND environment=$2`, account.AccountKey, account.Environment).
		Scan(&methods.PayPalSingle, &methods.PayPalVIP)
	if errors.Is(err, pgx.ErrNoRows) {
		return methods, nil
	}
	return methods, err
}
