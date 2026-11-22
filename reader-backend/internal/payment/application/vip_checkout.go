package application

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"reader-backend/internal/conversion"
	"reader-backend/internal/db"
	"reader-backend/internal/novel"
	"reader-backend/internal/payment"
)

type vipCheckoutIntentRow struct {
	intentID       string
	userID         int64
	productID      int64
	slotID         int
	templateID     int
	priceCents     int64
	currency       string
	cycle          string
	environment    string
	returnPath     string
	activeProvider string
	paypalIntentID string
	stripeIntentID string
	status         string
	subscriptionID string
	snapshot       json.RawMessage
	attribution    json.RawMessage
}

func scanVIPCheckoutIntentRow(row pgx.Row) (vipCheckoutIntentRow, error) {
	var r vipCheckoutIntentRow
	err := row.Scan(
		&r.intentID, &r.userID, &r.productID, &r.slotID, &r.templateID, &r.priceCents,
		&r.currency, &r.cycle, &r.environment, &r.returnPath, &r.activeProvider,
		&r.paypalIntentID, &r.stripeIntentID, &r.status, &r.subscriptionID, &r.snapshot, &r.attribution,
	)
	return r, err
}

const vipCheckoutIntentSelect = `
	SELECT v.intent_id::text, v.user_id, v.product_id, v.slot_id, v.template_id, v.price_cents,
	       v.currency, v.cycle, v.checkout_environment, v.return_path, COALESCE(v.active_provider, ''),
	       COALESCE(v.paypal_intent_id::text, ''), COALESCE(v.stripe_intent_id::text, ''),
	       v.status, COALESCE(v.subscription_id, ''), v.product_snapshot, v.attribution
	FROM payment_vip_checkout_intents v`

func buildVIPPurchaseIntentView(ctx context.Context, q checkoutQueryer, r vipCheckoutIntentRow) (*PurchaseIntent, error) {
	out := &PurchaseIntent{
		ID:          r.intentID,
		ProductID:   r.productID,
		SlotID:      r.slotID,
		OrderType:   "subscription",
		Cycle:       r.cycle,
		AmountMinor: r.priceCents,
		Currency:    r.currency,
		Coins:       0,
		ReturnPath:  r.returnPath,
		Provider:    r.activeProvider,
		Status:      "ready",
	}
	var snap struct {
		Badge        string `json:"badge"`
		DisplayAlias string `json:"display_alias"`
	}
	if len(r.snapshot) > 0 {
		_ = json.Unmarshal(r.snapshot, &snap)
		out.Badge = snap.Badge
		out.DisplayAlias = snap.DisplayAlias
	}
	if r.status == "Pending" {
		out.AvailableChannels = loadAvailableProductChannels(ctx, r.productID, "vip", currentPaymentLedgerEnvironment())
	}

	switch {
	case r.status == "Completed":
		out.Status = "completed"
		out.ExternalOrderID = r.subscriptionID
	case r.status == "Canceled" || r.status == "Expired":
		out.Status = "retryable"
	case r.activeProvider == payment.ProviderPayPal && r.paypalIntentID != "":
		var approval, subID, subStatus string
		var paid bool
		err := q.QueryRow(ctx, `
			SELECT COALESCE(i.approval_url, ''), COALESCE(i.subscription_id, ''),
			       COALESCE((SELECT status FROM user_subscriptions WHERE subscription_id = i.subscription_id AND user_id = i.user_id LIMIT 1), ''),
			       EXISTS(SELECT 1 FROM user_subscriptions WHERE subscription_id = i.subscription_id AND user_id = i.user_id AND `+novel.PaidSubscriptionPredicate+`)
			FROM paypal_subscription_intents i
			WHERE i.intent_id = $1::uuid`, r.paypalIntentID).Scan(&approval, &subID, &subStatus, &paid)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		if err == nil {
			out.ExternalOrderID = subID
			switch {
			case paid:
				out.Status = "completed"
			case subStatus == "CANCELLED" || subStatus == "EXPIRED":
				out.Status = "retryable"
			case approval != "":
				out.Status = "awaiting_approval"
				out.ApprovalURL = approval
			default:
				out.Status = "confirming"
			}
		}
	case r.activeProvider == payment.ProviderStripe && r.stripeIntentID != "":
		var approval, subID, stStatus string
		var paid bool
		err := q.QueryRow(ctx, `
			SELECT COALESCE(v.approval_url, ''), COALESCE(v.subscription_ref, ''), v.status,
			       EXISTS(
			           SELECT 1 FROM payment_stripe_invoice_periods p
			           JOIN recharge_orders o ON o.id = p.order_id
			           WHERE p.intent_id = v.id AND p.admitted AND o.fulfilled_at IS NOT NULL
			       )
			FROM payment_stripe_subscription_intents v
			WHERE v.id = $1`, r.stripeIntentID).Scan(&approval, &subID, &stStatus, &paid)
		if err == nil {
			out.ExternalOrderID = subID
			switch {
			case paid:
				out.Status = "completed"
			case stStatus == "closed":
				out.Status = "retryable"
			case stStatus == "buyer_action" && approval != "":
				out.Status = "awaiting_approval"
				out.ApprovalURL = approval
			default:
				out.Status = "confirming"
			}
		}
	}
	return out, nil
}

func checkVIPActiveConflictTx(ctx context.Context, tx pgx.Tx, userID int64, env string, ignoreVIPIntentID string) error {
	var existing bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM user_subscriptions
			WHERE user_id = $1 AND payment_subscription_environment(user_subscriptions) = $2
			  AND (status IN ('PENDING', 'ACTIVE', 'SUSPENDED', 'CANCEL_PENDING') OR (`+novel.PaidSubscriptionPredicate+`))
			  AND NOT EXISTS (
			      SELECT 1 FROM payment_vip_checkout_intents v
			      JOIN paypal_subscription_intents pi ON pi.intent_id = v.paypal_intent_id
			      WHERE v.intent_id = NULLIF($3, '')::uuid AND pi.subscription_id = user_subscriptions.subscription_id
			        AND user_subscriptions.status = 'PENDING'
			  )
		) OR EXISTS (
			SELECT 1 FROM paypal_subscription_intents i
			WHERE i.user_id = $1 AND i.checkout_environment = $2
			  AND NOT EXISTS (
			      SELECT 1 FROM user_subscriptions s
			      WHERE s.user_id = i.user_id AND s.subscription_id = i.subscription_id AND s.status IN ('CANCELLED', 'EXPIRED')
			  )
			  AND ($3 = '' OR NOT EXISTS (SELECT 1 FROM payment_vip_checkout_intents v WHERE v.intent_id = $3::uuid AND v.paypal_intent_id = i.intent_id))
			  AND (i.submitted_at IS NOT NULL OR COALESCE(i.lease_until > now(), false) OR i.subscription_id IS NOT NULL)
		) OR EXISTS (
			SELECT 1 FROM payment_stripe_subscription_intents s
			WHERE s.user_id = $1 AND s.checkout_environment = $2 AND s.status IN ('creating', 'buyer_action')
			  AND ($3 = '' OR NOT EXISTS (SELECT 1 FROM payment_vip_checkout_intents v WHERE v.intent_id = $3::uuid AND v.stripe_intent_id = s.id))
		)`, userID, env, ignoreVIPIntentID).Scan(&existing)
	if err != nil {
		return err
	}
	if existing {
		return ErrCheckoutConflict
	}
	return nil
}

func reserveVIPCheckoutIntentTx(
	ctx context.Context, tx pgx.Tx, e *checkoutEvaluation, prod resolvedProduct, req purchaseRequest, attribution checkoutAttribution,
) (*PurchaseIntent, error) {
	env := conversion.Environment()
	// Expire any stale unsubmitted VIP checkout intent for a different product before creating a new one.
	if _, err := tx.Exec(ctx, `
		UPDATE payment_vip_checkout_intents SET status = 'Expired', updated_at = now()
		WHERE user_id = $1 AND checkout_environment = $2 AND status = 'Pending'
		  AND active_provider IS NULL AND paypal_intent_id IS NULL AND stripe_intent_id IS NULL
		  AND product_id <> $3`, e.userID, env, prod.ID); err != nil {
		return nil, err
	}

	var existingID string
	err := tx.QueryRow(ctx, `
		SELECT intent_id::text FROM payment_vip_checkout_intents
		WHERE user_id = $1 AND checkout_environment = $2 AND status = 'Pending' FOR UPDATE`, e.userID, env).Scan(&existingID)
	if err == nil {
		row, loadErr := scanVIPCheckoutIntentRow(tx.QueryRow(ctx, vipCheckoutIntentSelect+` WHERE v.intent_id = $1 FOR UPDATE`, existingID))
		if loadErr != nil {
			return nil, loadErr
		}
		if row.productID != prod.ID {
			if row.activeProvider == "" {
				if _, err := tx.Exec(ctx, `UPDATE payment_vip_checkout_intents SET status='Expired', updated_at=now() WHERE intent_id=$1`, existingID); err != nil {
					return nil, err
				}
			} else {
				return nil, ErrCheckoutConflict
			}
		} else {
			if _, err := tx.Exec(ctx, `
				INSERT INTO payment_checkout_requests(environment, user_id, request_key, product_id, slot_id, novel_id, return_path, vip_intent_id)
				VALUES($1, $2, $3, $4, $5, 0, $6, $7)`,
				env, e.userID, req.key, prod.ID, req.slotID, req.returnPath, existingID); err != nil {
				return nil, err
			}
			e.resumed = true
			e.checkoutID = existingID
			return buildVIPPurchaseIntentView(ctx, tx, row)
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}

	if err := checkVIPActiveConflictTx(ctx, tx, e.userID, env, ""); err != nil {
		return nil, err
	}
	attrJSON, err := json.Marshal(attribution)
	if err != nil {
		return nil, err
	}
	intentID := uuid.NewString()
	_, err = tx.Exec(ctx, `
		INSERT INTO payment_vip_checkout_intents (
			intent_id, user_id, product_id, slot_id, template_id, price_cents, currency, cycle,
			checkout_environment, product_snapshot, return_path, status, request_key, attribution
		)
		SELECT $1, u.id, $2, $3, $4, $5, 'USD', $6, $7, $8::jsonb, $9, 'Pending', $10,
		       $11::jsonb || jsonb_build_object(
		           'pixel_id', fp.pixel_id, 'promotion_link_id', p.id, 'novel_id', p.novel_id,
		           'utm_source', u.utm_source, 'utm_campaign', u.utm_campaign
		       )
		FROM users u
		LEFT JOIN LATERAL reader_promotion_for_user(u.id) p ON true
		LEFT JOIN fb_pixels fp ON fp.id = p.fb_pixel_id
		WHERE u.id = $12`,
		intentID, prod.ID, prod.SlotID, prod.TemplateID, prod.PriceCents, prod.VipCycle,
		env, string(prod.SnapshotJSON), req.returnPath, req.key, attrJSON, e.userID)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO payment_checkout_requests(environment, user_id, request_key, product_id, slot_id, novel_id, return_path, vip_intent_id)
		VALUES($1, $2, $3, $4, $5, 0, $6, $7)`,
		env, e.userID, req.key, prod.ID, req.slotID, req.returnPath, intentID); err != nil {
		return nil, err
	}
	e.checkoutID = intentID
	row, err := scanVIPCheckoutIntentRow(tx.QueryRow(ctx, vipCheckoutIntentSelect+` WHERE v.intent_id = $1`, intentID))
	if err != nil {
		return nil, err
	}
	return buildVIPPurchaseIntentView(ctx, tx, row)
}

func (s *service) tryGetVIPPurchaseIntent(ctx context.Context, userID int64, intentID, externalSubID string) (*PurchaseIntent, bool, error) {
	if db.Pool(ctx) == nil || (intentID == "" && externalSubID == "") {
		return nil, false, nil
	}
	var row vipCheckoutIntentRow
	var err error
	if intentID != "" {
		row, err = scanVIPCheckoutIntentRow(db.Pool(ctx).QueryRow(ctx,
			vipCheckoutIntentSelect+` WHERE v.intent_id = $1::uuid AND v.user_id = $2 AND v.checkout_environment = $3`,
			intentID, userID, conversion.Environment()))
	} else {
		row, err = scanVIPCheckoutIntentRow(db.Pool(ctx).QueryRow(ctx,
			vipCheckoutIntentSelect+`
			LEFT JOIN paypal_subscription_intents pi ON pi.intent_id = v.paypal_intent_id
			LEFT JOIN payment_stripe_subscription_intents si ON si.id = v.stripe_intent_id
			WHERE v.user_id = $1 AND v.checkout_environment = $2
			  AND (v.subscription_id = $3 OR pi.subscription_id = $3 OR si.subscription_ref = $3)
			ORDER BY v.created_at DESC LIMIT 1`,
			userID, conversion.Environment(), externalSubID))
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, true, err
	}
	if row.status == "Pending" && row.activeProvider == payment.ProviderStripe && row.stripeIntentID != "" {
		if scope, scopeErr := stripeVIPAccountForOwner(ctx, userID, row.stripeIntentID, true); scopeErr == nil {
			if adapter, ok := stripeCapabilityForAccount[payment.StripeVIPAdapter](s, scope); ok {
				if intentObj, findErr := findStripeVIPIntent(ctx, db.Pool(ctx), scope, row.stripeIntentID); findErr == nil {
					_ = s.syncStripeVIP(ctx, adapter, intentObj, "")
				}
			}
		}
		row, _ = scanVIPCheckoutIntentRow(db.Pool(ctx).QueryRow(ctx, vipCheckoutIntentSelect+` WHERE v.intent_id = $1::uuid`, row.intentID))
	}
	if row.status == "Pending" && row.activeProvider == payment.ProviderPayPal && row.paypalIntentID != "" {
		var ppSubID string
		_ = db.Pool(ctx).QueryRow(ctx, `SELECT COALESCE(subscription_id,'') FROM paypal_subscription_intents WHERE intent_id=$1`, row.paypalIntentID).Scan(&ppSubID)
		if ppSubID != "" {
			if sub, subErr := s.repo.GetSubscriptionByID(ctx, ppSubID); subErr == nil && sub != nil && sub.Status == "PENDING" {
				// Best-effort check if approved in PayPal during H5 return
				_ = s.ActivateSubscription(ctx, userID, payment.ProviderPayPal, ppSubID, "", "", "", "", "", "", "")
			}
			row, _ = scanVIPCheckoutIntentRow(db.Pool(ctx).QueryRow(ctx, vipCheckoutIntentSelect+` WHERE v.intent_id = $1::uuid`, row.intentID))
		}
	}
	view, err := buildVIPPurchaseIntentView(ctx, db.Pool(ctx), row)
	return view, true, err
}

func (s *service) startVIPPayPalCheckout(ctx context.Context, userID int64, intentID, origin string) (*PurchaseIntent, error) {
	site, err := s.checkoutSite(ctx, origin)
	if err != nil {
		return nil, err
	}
	provider := s.primaryPayPalClient()
	if provider == nil {
		return nil, ErrCheckoutPaused
	}
	account, err := provider.CheckoutAccount(ctx)
	if err != nil || !paymentEnvironmentMatches(account) || !provider.WebhookConfigured(ctx) {
		return nil, ErrCheckoutPaused
	}

	row, err := scanVIPCheckoutIntentRow(db.Pool(ctx).QueryRow(ctx,
		vipCheckoutIntentSelect+` WHERE v.intent_id = $1::uuid AND v.user_id = $2 AND v.checkout_environment = $3`,
		intentID, userID, conversion.Environment()))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrCheckoutUnavailable
	}
	if err != nil {
		return nil, err
	}
	if row.status == "Completed" {
		return buildVIPPurchaseIntentView(ctx, db.Pool(ctx), row)
	}

	// Switch off Stripe if an unsubmitted Stripe VIP checkout session exists.
	if row.activeProvider == payment.ProviderStripe && row.stripeIntentID != "" {
		if err := s.closeStripeVIPForSwitch(ctx, userID, row.intentID, row.stripeIntentID); err != nil {
			return nil, err
		}
		row.stripeIntentID = ""
		row.activeProvider = ""
	}

	// Resume existing PayPal subscription intent if already approved URL created
	if row.activeProvider == payment.ProviderPayPal && row.paypalIntentID != "" {
		return buildVIPPurchaseIntentView(ctx, db.Pool(ctx), row)
	}

	site.Path = "/recharge"
	site.RawQuery = url.Values{"purchase": {row.intentID}, "payment": {"return"}, "type": {"vip"}}.Encode()
	returnURL := site.String()
	site.RawQuery = url.Values{"purchase": {row.intentID}, "payment": {"cancel"}, "type": {"vip"}}.Encode()
	cancelURL := site.String()

	ppIntent, err := reserveUnifiedPayPalVIPIntentTx(ctx, userID, row, returnURL, cancelURL, account)
	if err != nil {
		return nil, err
	}
	if ppIntent.approvalURL != "" {
		updated, _ := scanVIPCheckoutIntentRow(db.Pool(ctx).QueryRow(ctx, vipCheckoutIntentSelect+` WHERE v.intent_id = $1::uuid`, row.intentID))
		return buildVIPPurchaseIntentView(ctx, db.Pool(ctx), updated)
	}
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		s.repo.releaseSubscriptionIntent(releaseCtx, ppIntent.id)
	}()

	if err := s.repo.submitSubscriptionIntent(ctx, ppIntent.id, ppIntent.planID); err != nil {
		return nil, err
	}
	result, err := provider.CreateSubscription(ctx, payment.CreateSubscriptionParam{
		Account: account, RequestID: ppIntent.id, UserID: userID, PlanID: ppIntent.planID,
		SlotID: row.slotID, PriceCents: row.priceCents, Currency: "USD", CustomID: ppIntent.id,
		ReturnURL: ppIntent.returnURL, CancelURL: ppIntent.cancelURL,
	})
	if err != nil {
		log.Printf("task=vip_checkout provider=paypal intent=%s error=%v", ppIntent.id, err)
		return nil, err
	}
	if err := s.repo.finishSubscriptionIntent(ctx, ppIntent, result); err != nil {
		return nil, err
	}
	_, _ = db.Pool(ctx).Exec(ctx, `UPDATE payment_vip_checkout_intents SET subscription_id = $2, updated_at = now() WHERE intent_id = $1::uuid`,
		row.intentID, result.SubscriptionID)
	updated, err := scanVIPCheckoutIntentRow(db.Pool(ctx).QueryRow(ctx, vipCheckoutIntentSelect+` WHERE v.intent_id = $1::uuid`, row.intentID))
	if err != nil {
		return nil, err
	}
	return buildVIPPurchaseIntentView(ctx, db.Pool(ctx), updated)
}

func (s *service) startVIPStripeCheckout(ctx context.Context, userID int64, intentID, origin string) (*PurchaseIntent, error) {
	adapter, available := s.stripeVIPAdapter()
	if !available || !stripeVIPCheckoutEnabled(ctx) {
		return nil, ErrCheckoutPaused
	}
	site, err := s.checkoutSite(ctx, origin)
	if err != nil {
		return nil, err
	}

	row, err := scanVIPCheckoutIntentRow(db.Pool(ctx).QueryRow(ctx,
		vipCheckoutIntentSelect+` WHERE v.intent_id = $1::uuid AND v.user_id = $2 AND v.checkout_environment = $3`,
		intentID, userID, conversion.Environment()))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrCheckoutUnavailable
	}
	if err != nil {
		return nil, err
	}
	if row.status == "Completed" {
		return buildVIPPurchaseIntentView(ctx, db.Pool(ctx), row)
	}

	// Switch off PayPal if an unsubmitted PayPal VIP approval exists.
	if row.activeProvider == payment.ProviderPayPal && row.paypalIntentID != "" {
		if err := s.closePayPalVIPForSwitch(ctx, userID, row.intentID, row.paypalIntentID); err != nil {
			return nil, err
		}
		row.paypalIntentID = ""
		row.activeProvider = ""
	}

	if row.activeProvider == payment.ProviderStripe && row.stripeIntentID != "" {
		if scope, scopeErr := stripeVIPAccountForOwner(ctx, userID, row.stripeIntentID, true); scopeErr == nil {
			if scAdapter, ok := stripeCapabilityForAccount[payment.StripeVIPAdapter](s, scope); ok {
				if intentObj, findErr := findStripeVIPIntent(ctx, db.Pool(ctx), scope, row.stripeIntentID); findErr == nil {
					_ = s.syncStripeVIP(ctx, scAdapter, intentObj, "")
				}
			}
		}
		updated, _ := scanVIPCheckoutIntentRow(db.Pool(ctx).QueryRow(ctx, vipCheckoutIntentSelect+` WHERE v.intent_id = $1::uuid`, row.intentID))
		return buildVIPPurchaseIntentView(ctx, db.Pool(ctx), updated)
	}

	site.Path = "/recharge"
	site.RawQuery = url.Values{"purchase": {row.intentID}, "payment": {"return"}, "type": {"vip"}}.Encode()
	returnURL := site.String()
	site.RawQuery = url.Values{"purchase": {row.intentID}, "payment": {"cancel"}, "type": {"vip"}}.Encode()
	cancelURL := site.String()

	stIntent, err := reserveUnifiedStripeVIPIntentTx(ctx, row, returnURL, cancelURL, adapter.AccountScope())
	if err != nil {
		return nil, err
	}
	due, err := claimStripeVIP(ctx, stIntent.Account, stIntent.ID, stripeVIPCheckoutEnabled(ctx))
	if err != nil {
		return nil, err
	}
	for _, item := range due {
		reqCtx, cancel := context.WithTimeout(ctx, stripeVIPRequestTimeout)
		_ = s.syncStripeVIP(reqCtx, adapter, item, "")
		cancel()
	}
	updated, err := scanVIPCheckoutIntentRow(db.Pool(ctx).QueryRow(ctx, vipCheckoutIntentSelect+` WHERE v.intent_id = $1::uuid`, row.intentID))
	if err != nil {
		return nil, err
	}
	return buildVIPPurchaseIntentView(ctx, db.Pool(ctx), updated)
}

func (s *service) closeStripeVIPForSwitch(ctx context.Context, userID int64, vipIntentID, stripeIntentID string) error {
	var acctKey, acctEnv, sessionID, status, mode, subID string
	err := db.Pool(ctx).QueryRow(ctx, `
		SELECT a.account_key, a.environment, COALESCE(v.checkout_ref,''), v.status, v.sales_mode, COALESCE(v.subscription_ref,'')
		FROM payment_stripe_subscription_intents v
		JOIN payment_channel_accounts a ON a.id = v.account_id
		WHERE v.id = $1 AND v.user_id = $2`, stripeIntentID, userID).
		Scan(&acctKey, &acctEnv, &sessionID, &status, &mode, &subID)
	if errors.Is(err, pgx.ErrNoRows) || status == "closed" {
		return nil
	}
	if err != nil {
		return err
	}
	if subID != "" || status == "bound" {
		return ErrCheckoutPending
	}
	if sessionID != "" {
		adapter, ok := stripeCapabilityForAccount[interface {
			CloseStripeVIPCheckout(context.Context, string, string, string) (payment.StripeVIPCheckout, error)
		}](s, payment.WebhookAccount{AccountKey: acctKey, Environment: acctEnv})
		if !ok {
			return ErrCheckoutPending
		}
		proof, err := adapter.CloseStripeVIPCheckout(ctx, sessionID, stripeIntentID, mode)
		if err != nil {
			return ErrCheckoutPending
		}
		if proof.SubscriptionID != "" || !proof.Expired {
			return ErrCheckoutPending
		}
	}
	_, err = db.Pool(ctx).Exec(ctx, `
		UPDATE payment_stripe_subscription_intents SET status='closed', approval_url=NULL
		WHERE id = $1 AND user_id = $2 AND subscription_ref IS NULL`, stripeIntentID, userID)
	if err != nil {
		return err
	}
	_, err = db.Pool(ctx).Exec(ctx, `
		UPDATE payment_vip_checkout_intents SET active_provider = NULL, stripe_intent_id = NULL, updated_at = now()
		WHERE intent_id = $1::uuid AND stripe_intent_id = $2::uuid`, vipIntentID, stripeIntentID)
	return err
}

func (s *service) closePayPalVIPForSwitch(ctx context.Context, userID int64, vipIntentID, ppIntentID string) error {
	var subID string
	var acctKey, acctEnv string
	err := db.Pool(ctx).QueryRow(ctx, `
		SELECT COALESCE(i.subscription_id, ''), COALESCE(a.account_key, ''), COALESCE(a.environment, '')
		FROM paypal_subscription_intents i
		LEFT JOIN payment_channel_accounts a ON a.id = i.checkout_account_id
		WHERE i.intent_id = $1::uuid AND i.user_id = $2`, ppIntentID, userID).
		Scan(&subID, &acctKey, &acctEnv)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if subID != "" {
		client, cErr := s.payPalForScope(ctx, payment.WebhookAccount{AccountKey: acctKey, Environment: acctEnv})
		if cErr != nil || client == nil {
			return ErrCheckoutPending
		}
		details, gErr := client.GetSubscription(ctx, subID)
		if gErr != nil {
			return ErrCheckoutPending
		}
		if details.Status == "APPROVED" || details.Status == "ACTIVE" || details.LastPaymentTime != nil {
			return ErrCheckoutPending
		}
		if details.Status != "CANCELLED" && details.Status != "EXPIRED" {
			_ = client.CancelSubscription(ctx, subID, "User switched checkout method to Stripe")
		}
		_, _ = db.Pool(ctx).Exec(ctx, `
			UPDATE user_subscriptions SET status = 'CANCELLED', cancelled_at = COALESCE(cancelled_at, now()), updated_at = now()
			WHERE subscription_id = $1 AND user_id = $2 AND status = 'PENDING'`, subID, userID)
	}
	tx, err := db.Pool(ctx).Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if _, err := tx.Exec(ctx, `UPDATE payment_vip_checkout_intents SET active_provider = NULL, paypal_intent_id = NULL, subscription_id = NULL, updated_at = now() WHERE intent_id = $1::uuid`, vipIntentID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM paypal_subscription_intents WHERE intent_id = $1::uuid AND user_id = $2`, ppIntentID, userID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func reserveUnifiedPayPalVIPIntentTx(
	ctx context.Context, userID int64, v vipCheckoutIntentRow, returnURL, cancelURL string, scope payment.WebhookAccount,
) (*subscriptionIntent, error) {
	evaluation := checkoutEvaluation{stage: subscriptionIntentStage, userID: userID, provider: payment.ProviderPayPal, slotID: v.slotID}
	return evaluateCheckout(ctx, evaluation, func(tx pgx.Tx, e *checkoutEvaluation) (*subscriptionIntent, error) {
		if err := checkVIPActiveConflictTx(ctx, tx, userID, conversion.Environment(), v.intentID); err != nil {
			return nil, err
		}
		var accountID int64
		var subsEnabled bool
		err := tx.QueryRow(ctx, `
			SELECT id, new_subscriptions_enabled FROM payment_channel_accounts
			WHERE provider = 'paypal' AND account_key = $1 AND environment = $2 AND status = 'published' AND is_primary = true`,
			scope.AccountKey, scope.Environment).Scan(&accountID, &subsEnabled)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrCheckoutPaused
		}
		if err != nil {
			return nil, err
		}
		if !subsEnabled {
			return nil, ErrCheckoutPaused
		}

		var planID string
		err = tx.QueryRow(ctx, `
			SELECT b.external_price_ref
			FROM product_channel_bindings b
			JOIN product_publications pub ON pub.product_id = b.product_id AND pub.environment = b.environment AND pub.status = 'published'
			WHERE b.product_id = $1 AND b.account_id = $2 AND b.provider = 'paypal' AND b.environment = $3
			  AND b.enabled = true AND b.validation_status = 'verified' AND b.external_price_ref <> ''
			FOR SHARE OF b`, v.productID, accountID, scope.Environment).Scan(&planID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrCheckoutPaused
		}
		if err != nil {
			return nil, err
		}

		ppID := uuid.NewString()
		ppReturn := appendVIPReturnReference(returnURL, ppID)
		ppCancel := appendVIPReturnReference(cancelURL, ppID)
		_, err = tx.Exec(ctx, `
			INSERT INTO paypal_subscription_intents (
				user_id, intent_id, product_id, product_snapshot, slot_id, template_id, cycle, price_cents,
				return_url, cancel_url, attribution, checkout_account_id, checkout_return_path, plan_id, checkout_environment
			) VALUES ($1, $2, $3, $4::jsonb, $5, $6, $7, $8, $9, $10, $11::jsonb, $12, $13, $14, $15)`,
			userID, ppID, v.productID, v.snapshot, v.slotID, v.templateID, v.cycle, v.priceCents,
			ppReturn, ppCancel, v.attribution, accountID, v.returnPath, planID, conversion.Environment())
		if err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE payment_vip_checkout_intents
			SET active_provider = 'paypal', paypal_intent_id = $2::uuid, updated_at = now()
			WHERE intent_id = $1::uuid`, v.intentID, ppID); err != nil {
			return nil, err
		}
		_, _ = tx.Exec(ctx, `UPDATE paypal_subscription_intents SET lease_until = now() + interval '5 minutes' WHERE intent_id = $1::uuid`, ppID)
		return &subscriptionIntent{
			id: ppID, userID: userID, slotID: v.slotID, templateID: v.templateID,
			priceCents: int(v.priceCents), cycle: v.cycle, returnURL: ppReturn, cancelURL: ppCancel,
			returnPath: v.returnPath, planID: planID, account: scope, accountID: &accountID,
		}, nil
	})
}

func reserveUnifiedStripeVIPIntentTx(
	ctx context.Context, v vipCheckoutIntentRow, returnURL, cancelURL string, scope payment.WebhookAccount,
) (*stripeVIPIntent, error) {
	evaluation := checkoutEvaluation{stage: subscriptionIntentStage, provider: payment.ProviderStripe, userID: v.userID, slotID: v.slotID}
	return evaluateCheckout(ctx, evaluation, func(tx pgx.Tx, e *checkoutEvaluation) (*stripeVIPIntent, error) {
		if err := checkVIPActiveConflictTx(ctx, tx, v.userID, conversion.Environment(), v.intentID); err != nil {
			return nil, err
		}
		var accountID int64
		var subsEnabled bool
		err := tx.QueryRow(ctx, `
			SELECT id, new_subscriptions_enabled FROM payment_channel_accounts
			WHERE provider = 'stripe' AND account_key = $1 AND environment = $2 AND status = 'published' AND is_primary = true`,
			scope.AccountKey, scope.Environment).Scan(&accountID, &subsEnabled)
		if errors.Is(err, pgx.ErrNoRows) || !subsEnabled {
			return nil, ErrCheckoutPaused
		}
		if err != nil {
			return nil, err
		}

		stID := uuid.NewString()
		stReturn := appendStripeVIPReturn(returnURL, stID)
		stCancel := appendStripeVIPReturn(cancelURL, stID)
		tag, err := tx.Exec(ctx, `
			INSERT INTO payment_stripe_subscription_intents (
				id, user_id, account_id, checkout_environment, offer_version_id, product_id, product_snapshot,
				slot_id, template_id, product_ref, price_ref, amount_minor, cycle,
				sales_mode, allowed_countries, tax_liability_account, return_url, cancel_url, return_path, attribution,
				validated_catalog, release_evidence, market_policy, expected_country, market_basis
			)
			SELECT $1, $2, $3, $4, NULL, $5, $6::jsonb,
			       $7, $8, b.external_product_ref, b.external_price_ref, $9, $10,
			       b.sales_mode, b.allowed_countries, b.tax_liability_account, $11, $12, $13, $14::jsonb,
			       b.validated_catalog, b.release_evidence, b.market_policy, expected.country,
			       CASE WHEN expected.country IS NULL THEN 'stripe_confirmation_required' ELSE 'prior_verified_payment' END
			FROM product_channel_bindings b
			JOIN product_publications pub ON pub.product_id = b.product_id AND pub.environment = b.environment AND pub.status = 'published'
			CROSS JOIN LATERAL (SELECT payment_last_stripe_billing_country($3, $2) AS country) expected
			WHERE b.product_id = $5 AND b.account_id = $3 AND b.provider = 'stripe' AND b.environment = $15
			  AND b.enabled = true AND b.validation_status = 'verified'
			  AND b.external_product_ref <> '' AND b.external_price_ref <> ''
			  AND ($15 = 'sandbox' OR (b.validated_catalog IS NOT NULL AND b.release_evidence IS NOT NULL AND b.market_policy IS NOT NULL
			       AND (expected.country IS NULL OR expected.country = ANY(b.allowed_countries))))`,
			stID, v.userID, accountID, conversion.Environment(), v.productID, v.snapshot,
			v.slotID, v.templateID, v.priceCents, v.cycle, stReturn, stCancel, v.returnPath, v.attribution, scope.Environment)
		if err != nil {
			return nil, err
		}
		if tag.RowsAffected() != 1 {
			return nil, ErrCheckoutPaused
		}
		if _, err := tx.Exec(ctx, `
			UPDATE payment_vip_checkout_intents
			SET active_provider = 'stripe', stripe_intent_id = $2::uuid, updated_at = now()
			WHERE intent_id = $1::uuid`, v.intentID, stID); err != nil {
			return nil, err
		}
		res, err := findStripeVIPIntent(ctx, tx, scope, stID)
		return &res, err
	})
}

func (s *service) recentUnifiedVIPPurchases(ctx context.Context, userID int64) ([]PurchaseIntent, error) {
	rows, err := db.Pool(ctx).Query(ctx, vipCheckoutIntentSelect+`
		WHERE v.user_id = $1 AND v.checkout_environment = $2
		ORDER BY v.created_at DESC, v.intent_id LIMIT 20`, userID, conversion.Environment())
	if err != nil {
		return nil, err
	}
	vipRows, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (vipCheckoutIntentRow, error) {
		return scanVIPCheckoutIntentRow(row)
	})
	if err != nil {
		return nil, err
	}
	out := make([]PurchaseIntent, 0, len(vipRows))
	for _, r := range vipRows {
		view, err := buildVIPPurchaseIntentView(ctx, db.Pool(ctx), r)
		if err != nil {
			return nil, err
		}
		out = append(out, *view)
	}
	return out, nil
}
