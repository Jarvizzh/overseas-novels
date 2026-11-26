package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"reader-backend/internal/conversion"
	"reader-backend/internal/db"
	"reader-backend/internal/novel"
	"reader-backend/internal/payment"
)

var ErrVIPQuoteChanged = errors.New("This VIP offer has changed. Select a current package to continue.")

const (
	VIPPlanSwitchCodeApprovalOpen = "vip_plan_switch_approval_open"
	VIPPlanSwitchCodePending      = "vip_plan_switch_pending"
	VIPPlanSwitchCodeConfirming   = "vip_plan_switch_confirming"
)

// VIPPlanSwitchBlockedError explains why an active pending VIP checkout could
// not yet be replaced by another VIP package while preserving original intent details.
type VIPPlanSwitchBlockedError struct {
	Code            string
	Message         string
	CurrentIntentID string
	CurrentSlotID   int
	CurrentProduct  int64
	CurrentCycle    string
	CurrentName     string
	TargetCycle     string
	TargetName      string
	Cause           error
}

func (e *VIPPlanSwitchBlockedError) Error() string {
	return e.Message
}

func (e *VIPPlanSwitchBlockedError) Unwrap() error {
	return e.Cause
}

func vipPlanDisplayName(snapshot json.RawMessage, cycle string) string {
	var snap struct {
		DisplayAlias string `json:"display_alias"`
		Name         string `json:"name"`
	}
	if len(snapshot) > 0 {
		_ = json.Unmarshal(snapshot, &snap)
	}
	if snap.DisplayAlias != "" {
		return snap.DisplayAlias
	}
	if snap.Name != "" {
		return snap.Name
	}
	switch cycle {
	case "day":
		return "VIP Daily Pass"
	case "week":
		return "VIP Weekly Pass"
	case "month":
		return "VIP Monthly Pass"
	case "year":
		return "VIP Yearly Pass"
	default:
		return "VIP Pass"
	}
}

func newVIPPlanSwitchBlockedError(code string, current vipCheckoutIntentRow, target resolvedProduct) *VIPPlanSwitchBlockedError {
	currentName := vipPlanDisplayName(current.snapshot, current.cycle)
	targetName := vipPlanDisplayName(target.SnapshotJSON, target.VipCycle)
	var msg string
	switch code {
	case VIPPlanSwitchCodeApprovalOpen:
		msg = fmt.Sprintf("%s checkout is still open and cannot switch to %s yet. Please complete or close the previous checkout first.", currentName, targetName)
	case VIPPlanSwitchCodeConfirming:
		msg = fmt.Sprintf("%s payment is already being confirmed and cannot switch to %s.", currentName, targetName)
	default:
		code = VIPPlanSwitchCodePending
		msg = fmt.Sprintf("%s payment is still being confirmed and cannot switch to %s right now.", currentName, targetName)
	}
	return &VIPPlanSwitchBlockedError{
		Code:            code,
		Message:         msg,
		CurrentIntentID: current.intentID,
		CurrentSlotID:   current.slotID,
		CurrentProduct:  current.productID,
		CurrentCycle:    current.cycle,
		CurrentName:     currentName,
		TargetCycle:     target.VipCycle,
		TargetName:      targetName,
		Cause:           ErrSubscriptionConflict,
	}
}

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
	switchTo       string
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
		&r.currency, &r.cycle, &r.environment, &r.returnPath, &r.activeProvider, &r.switchTo,
		&r.paypalIntentID, &r.stripeIntentID, &r.status, &r.subscriptionID, &r.snapshot, &r.attribution,
	)
	return r, err
}

const vipCheckoutIntentSelect = `
	SELECT v.intent_id::text, v.user_id, v.product_id, v.slot_id, v.template_id, v.price_cents,
	       v.currency, v.cycle, v.checkout_environment, v.return_path, COALESCE(v.active_provider, ''), COALESCE(v.switch_to_provider, ''),
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
	case r.status == "Expired":
		out.Status, out.ConfirmationMessage = "closed", ErrVIPQuoteChanged.Error()
	case r.status == "Canceled":
		out.Status = "closed"
	}
	if r.status == "Completed" {
		out.Status, out.ExternalOrderID = "completed", r.subscriptionID
	}

	switch r.activeProvider {
	case payment.ProviderPayPal:
		var approval, subID, subStatus, firstOrderStatus string
		var busy, needsReview, bindingBlocked, activationRequested bool
		err := q.QueryRow(ctx, `
			SELECT COALESCE(i.approval_url, ''), COALESCE(user_subscriptions.subscription_id, i.subscription_id, ''),
			       COALESCE(user_subscriptions.status, ''), COALESCE(first_order.order_status, ''),
			       COALESCE(i.lease_until > now(), false),
			       COALESCE(i.submitted_at <= now()-make_interval(secs=>$6::int), false),
			       i.product_id IS NOT NULL AND i.submitted_at IS NULL AND i.subscription_id IS NULL
			         AND NOT EXISTS (`+paypalVIPBindingMatchSQL+`),
			       COALESCE(user_subscriptions.paypal_activation_requested_at IS NOT NULL, false)
			FROM payment_vip_checkout_intents parent
			LEFT JOIN paypal_subscription_intents i ON i.intent_id=parent.paypal_intent_id
			LEFT JOIN user_subscriptions ON user_subscriptions.subscription_id=COALESCE(parent.subscription_id,i.subscription_id)
			  AND user_subscriptions.user_id=parent.user_id AND user_subscriptions.payment_method='paypal'
			  AND user_subscriptions.checkout_intent_id=parent.paypal_intent_id
			`+paypalVIPFirstOrderJoin+`
			WHERE parent.intent_id=$1 AND parent.user_id=$2 AND parent.checkout_environment=$3
			  AND parent.paypal_intent_id=$4::uuid AND parent.active_provider=$5`,
			r.intentID, r.userID, r.environment, r.paypalIntentID, r.activeProvider, int(payPalVIPCreateRetryWindow/time.Second)).
			Scan(&approval, &subID, &subStatus, &firstOrderStatus, &busy, &needsReview, &bindingBlocked, &activationRequested)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		if subID != "" {
			out.ExternalOrderID = subID
		}
		switch {
		case firstOrderStatus == "Refunded":
			out.Status, out.ConfirmationMessage = "refunded", ""
		case firstOrderStatus != "":
			out.Status, out.ConfirmationMessage = "completed", ""
		case r.status != "Pending":
		case bindingBlocked:
			markVIPChannelUnavailable(out)
		case subStatus == "CANCELLED" || subStatus == "EXPIRED":
			out.Status = "retryable"
		case subStatus == "PENDING" && !activationRequested && approval != "":
			out.Status, out.ApprovalURL = "awaiting_approval", approval
		case subID != "" || busy:
			out.Status = "confirming"
		case needsReview:
			out.Status = "review"
		default:
			out.Status = "retryable"
		}

	case payment.ProviderStripe:
		var approval, subID, state string
		var bindingBlocked bool
		err := q.QueryRow(ctx, `SELECT COALESCE(v.approval_url,''), COALESCE(v.subscription_ref,''), `+stripeVIPPurchaseStateSQL+`,
			v.status='creating' AND v.first_sent_at IS NULL AND v.checkout_ref IS NULL AND v.subscription_ref IS NULL
			  AND v.product_id IS NOT NULL AND NOT EXISTS (`+stripeVIPBindingMatchSQL+`)
			FROM payment_stripe_subscription_intents v JOIN payment_channel_accounts a ON a.id=v.account_id
			`+stripeVIPFirstPeriodJoin+`
			WHERE v.id=$1 AND v.user_id=$2 AND v.checkout_environment=$3`,
			r.stripeIntentID, r.userID, r.environment).Scan(&approval, &subID, &state, &bindingBlocked)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		if subID != "" {
			out.ExternalOrderID = subID
		}
		switch {
		case state == "refunded" || state == "completed":
			out.Status, out.ConfirmationMessage = state, ""
		case r.status != "Pending":
		case bindingBlocked:
			markVIPChannelUnavailable(out)
		case state == "closed":
			out.Status = "retryable"
		case state != "":
			out.Status = state
			if state == "awaiting_approval" {
				out.ApprovalURL = approval
			}
		default:
			out.Status = "confirming"
		}
	}
	if r.status == "Pending" && r.switchTo != "" && out.Status != "completed" && out.Status != "refunded" {
		if out.Status != "review" {
			out.Status, out.ConfirmationMessage = "confirming", "Confirming the previous VIP checkout before switching payment methods."
		}
		out.ApprovalURL = ""
	}
	return out, nil
}

func markVIPChannelUnavailable(out *PurchaseIntent) {
	out.Status, out.ConfirmationMessage = "confirming", ErrCheckoutPaused.Error()
	out.AvailableChannels = slices.DeleteFunc(out.AvailableChannels, func(provider string) bool { return provider == out.Provider })
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
		return ErrSubscriptionConflict
	}
	return nil
}

func validateVIPQuoteTx(ctx context.Context, tx pgx.Tx, userID, productID int64, slotID, templateID int, price int64, cycle string) error {
	var slot int
	err := tx.QueryRow(ctx, `
		SELECT s.id FROM recharge_slots s
		JOIN products p ON p.id=s.product_id
		JOIN product_publications pub ON pub.product_id=p.id AND pub.environment=$7 AND pub.status='published'
		WHERE s.id=$2 AND s.template_id=$3 AND s.product_id=$4
		  AND p.product_type='vip' AND p.currency='USD' AND p.price_cents=$5 AND p.vip_cycle=$6
		  AND s.template_id=COALESCE(
		    (SELECT recharge_template_id FROM reader_promotion_for_user_env($1, $7) WHERE recharge_template_id IS NOT NULL),
		    `+defaultLiveTemplateIDSQL+`)
		FOR SHARE OF s,p,pub`, userID, slotID, templateID, productID, price, cycle, currentPaymentLedgerEnvironment()).Scan(&slot)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrVIPQuoteChanged
	}
	return err
}

// The user lock in evaluateCheckout serializes new reservations. The parent
// lock also protects against settlement and a concurrent channel switch.
func lockVIPForChannelReservation(ctx context.Context, tx pgx.Tx, v vipCheckoutIntentRow) error {
	var status, provider string
	err := tx.QueryRow(ctx, `SELECT status,COALESCE(active_provider,'')
		FROM payment_vip_checkout_intents WHERE intent_id=$1 AND user_id=$2 AND checkout_environment=$3 FOR UPDATE`,
		v.intentID, v.userID, v.environment).Scan(&status, &provider)
	if err != nil {
		return err
	}
	if status != "Pending" {
		return ErrVIPQuoteChanged
	}
	if provider != "" {
		return ErrCheckoutPending
	}
	if err := validateVIPQuoteTx(ctx, tx, v.userID, v.productID, v.slotID, v.templateID, v.priceCents, v.cycle); err != nil {
		return err
	}
	return checkVIPActiveConflictTx(ctx, tx, v.userID, v.environment, v.intentID)
}

func canExpireStaleVIPQuote(row vipCheckoutIntentRow) bool {
	return row.status == "Pending" && row.switchTo == "" && row.subscriptionID == "" &&
		((row.activeProvider == payment.ProviderStripe && row.stripeIntentID != "") ||
			(row.activeProvider == payment.ProviderPayPal && row.paypalIntentID != ""))
}

// The caller holds the parent row lock. Fulfillment locks the child first,
// so skip a busy child and let the next read see the committed evidence.
func expireStaleVIPQuoteTx(ctx context.Context, tx pgx.Tx, row *vipCheckoutIntentRow) error {
	if !canExpireStaleVIPQuote(*row) {
		return nil
	}
	err := validateVIPQuoteTx(ctx, tx, row.userID, row.productID, row.slotID, row.templateID, row.priceCents, row.cycle)
	if !errors.Is(err, ErrVIPQuoteChanged) {
		return err
	}

	if row.activeProvider == payment.ProviderPayPal {
		// PayPal intents reserve a unique user/environment slot until deleted.
		tag, err := tx.Exec(ctx, `DELETE FROM paypal_subscription_intents WHERE intent_id=(
			SELECT intent_id FROM paypal_subscription_intents
			WHERE intent_id=$1 AND user_id=$2 AND checkout_environment=$3
			  AND submitted_at IS NULL AND subscription_id IS NULL AND approval_url IS NULL
			  AND (lease_until IS NULL OR lease_until<=now())
			FOR UPDATE SKIP LOCKED)`, row.paypalIntentID, row.userID, row.environment)
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE payment_vip_checkout_intents
			SET active_provider=NULL,paypal_intent_id=NULL,status='Expired',updated_at=now() WHERE intent_id=$1`, row.intentID); err != nil {
			return err
		}
		row.activeProvider, row.paypalIntentID, row.status = "", "", "Expired"
		return nil
	}

	var child string
	err = tx.QueryRow(ctx, `SELECT id::text FROM payment_stripe_subscription_intents
		WHERE id=$1 AND user_id=$2 AND checkout_environment=$3 AND status='creating'
		  AND first_sent_at IS NULL AND checkout_ref IS NULL AND subscription_ref IS NULL
		FOR UPDATE SKIP LOCKED`, row.stripeIntentID, row.userID, row.environment).Scan(&child)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE payment_stripe_subscription_intents SET status='closed',approval_url=NULL WHERE id=$1`, child); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE payment_vip_checkout_intents SET status='Expired',updated_at=now() WHERE intent_id=$1`, row.intentID); err != nil {
		return err
	}
	row.status = "Expired"
	return nil
}

// detachClosedVIPChildTx clears a terminal unpaid child while the caller holds
// the parent row lock, skipping busy children to preserve child-before-parent lock order.
func detachClosedVIPChildTx(ctx context.Context, tx pgx.Tx, row *vipCheckoutIntentRow) error {
	if row.status != "Pending" || row.switchTo != "" {
		return nil
	}
	if row.activeProvider == payment.ProviderPayPal && row.paypalIntentID != "" {
		tag, err := tx.Exec(ctx, `DELETE FROM paypal_subscription_intents WHERE intent_id=(
			SELECT i.intent_id FROM paypal_subscription_intents i
			JOIN user_subscriptions s ON s.subscription_id=i.subscription_id
			  AND s.user_id=i.user_id AND s.checkout_intent_id=i.intent_id
			  AND s.payment_method='paypal'
			WHERE i.intent_id=$1 AND i.user_id=$2 AND i.checkout_environment=$3
			  AND (s.status = 'EXPIRED'
			    OR (s.status = 'CANCELLED' AND COALESCE((s.attribution->>'remote_termination_confirmed')::boolean, false) = true))
			  AND s.last_payment_time IS NULL
			  AND s.paypal_activation_requested_at IS NULL
			FOR UPDATE OF i, s SKIP LOCKED)`, row.paypalIntentID, row.userID, row.environment)
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE payment_vip_checkout_intents
			SET active_provider=NULL, paypal_intent_id=NULL, stripe_intent_id=NULL, subscription_id=NULL, updated_at=now()
			WHERE intent_id=$1`, row.intentID); err != nil {
			return err
		}
		row.activeProvider, row.paypalIntentID, row.subscriptionID = "", "", ""
		return nil
	}
	if row.activeProvider == payment.ProviderStripe && row.stripeIntentID != "" {
		var child string
		err := tx.QueryRow(ctx, `SELECT id::text FROM payment_stripe_subscription_intents
			WHERE id=$1 AND user_id=$2 AND checkout_environment=$3
			  AND status='closed' AND subscription_ref IS NULL
			FOR UPDATE SKIP LOCKED`, row.stripeIntentID, row.userID, row.environment).Scan(&child)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE payment_vip_checkout_intents
			SET active_provider=NULL, paypal_intent_id=NULL, stripe_intent_id=NULL, subscription_id=NULL, updated_at=now()
			WHERE intent_id=$1`, row.intentID); err != nil {
			return err
		}
		row.activeProvider, row.stripeIntentID, row.subscriptionID = "", "", ""
		return nil
	}
	return nil
}

func lockVIPCheckoutIntent(ctx context.Context, tx pgx.Tx, userID int64, id string) (vipCheckoutIntentRow, error) {
	return scanVIPCheckoutIntentRow(tx.QueryRow(ctx, vipCheckoutIntentSelect+`
		WHERE v.intent_id=$1 AND v.user_id=$2 AND v.checkout_environment=$3 FOR UPDATE`, id, userID, conversion.Environment()))
}

func expireStaleVIPQuote(ctx context.Context, row vipCheckoutIntentRow) (vipCheckoutIntentRow, error) {
	tx, err := db.Pool(ctx).Begin(ctx)
	if err != nil {
		return row, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	current, err := lockVIPCheckoutIntent(ctx, tx, row.userID, row.intentID)
	if err != nil {
		return row, err
	}
	if err := expireStaleVIPQuoteTx(ctx, tx, &current); err != nil {
		return row, err
	}
	return current, tx.Commit(ctx)
}

func reserveVIPCheckoutIntentTx(
	ctx context.Context, tx pgx.Tx, e *checkoutEvaluation, prod resolvedProduct, req purchaseRequest, attribution checkoutAttribution,
) (*PurchaseIntent, error) {
	env := conversion.Environment()
	row, err := scanVIPCheckoutIntentRow(tx.QueryRow(ctx, vipCheckoutIntentSelect+`
		WHERE v.user_id=$1 AND v.checkout_environment=$2 AND v.status='Pending' FOR UPDATE`, e.userID, env))
	hasPending := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if err := validateVIPQuoteTx(ctx, tx, e.userID, prod.ID, prod.SlotID, prod.TemplateID, int64(prod.PriceCents), prod.VipCycle); err != nil {
		return nil, err
	}
	if hasPending {
		sameQuote := row.productID == prod.ID && row.slotID == prod.SlotID && row.templateID == prod.TemplateID &&
			row.priceCents == int64(prod.PriceCents) && row.cycle == prod.VipCycle
		if sameQuote {
			if _, err := tx.Exec(ctx, `
				INSERT INTO payment_checkout_requests(environment, user_id, request_key, product_id, slot_id, novel_id, return_path, vip_intent_id)
				VALUES($1, $2, $3, $4, $5, 0, $6, $7)`,
				env, e.userID, req.key, prod.ID, req.slotID, req.returnPath, row.intentID); err != nil {
				return nil, err
			}
			e.resumed, e.checkoutID = true, row.intentID
			return buildVIPPurchaseIntentView(ctx, tx, row)
		}
		if err := expireStaleVIPQuoteTx(ctx, tx, &row); err != nil {
			return nil, err
		}
		if err := detachClosedVIPChildTx(ctx, tx, &row); err != nil {
			return nil, err
		}
		if row.status == "Pending" {
			if row.activeProvider != "" {
				return nil, ErrSubscriptionConflict
			}
			if _, err := tx.Exec(ctx, `UPDATE payment_vip_checkout_intents SET status='Expired', updated_at=now() WHERE intent_id=$1`, row.intentID); err != nil {
				return nil, err
			}
		}
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
		LEFT JOIN LATERAL reader_promotion_for_user_env(u.id, CASE WHEN $7 IN ('sandbox','test') THEN 'sandbox' ELSE 'live' END) p ON true
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
	row, err = scanVIPCheckoutIntentRow(tx.QueryRow(ctx, vipCheckoutIntentSelect+` WHERE v.intent_id = $1`, intentID))
	if err != nil {
		return nil, err
	}
	return buildVIPPurchaseIntentView(ctx, tx, row)
}

func (s *service) tryGetVIPPurchaseIntent(ctx context.Context, userID int64, intentID, externalSubID string, refresh bool) (*PurchaseIntent, bool, error) {
	if db.Pool(ctx) == nil || (intentID == "" && externalSubID == "") {
		return nil, false, nil
	}
	var row vipCheckoutIntentRow
	var err error
	if intentID != "" {
		row, err = readVIPCheckoutIntent(ctx, userID, intentID)
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
	if canExpireStaleVIPQuote(row) {
		row, err = expireStaleVIPQuote(ctx, row)
		if err != nil {
			return nil, true, err
		}
	}
	if refresh && row.status == "Pending" && row.switchTo == "" {
		switch row.activeProvider {
		case payment.ProviderStripe:
			s.refreshUnifiedStripeVIP(ctx, row)
		case payment.ProviderPayPal:
			if row.subscriptionID != "" {
				// Verify a buyer returning from approval. Transaction admission still requires provider proof.
				_ = s.ActivateSubscription(ctx, userID, payment.ProviderPayPal, row.subscriptionID, "", "", "", "", "", "", "")
			}
		}
		row, err = readVIPCheckoutIntent(ctx, userID, row.intentID)
		if err != nil {
			return nil, true, err
		}
	}
	view, err := buildVIPPurchaseIntentView(ctx, db.Pool(ctx), row)
	return view, true, err
}

func readVIPCheckoutIntent(ctx context.Context, userID int64, intentID string) (vipCheckoutIntentRow, error) {
	return scanVIPCheckoutIntentRow(db.Pool(ctx).QueryRow(ctx,
		vipCheckoutIntentSelect+` WHERE v.intent_id=$1 AND v.user_id=$2 AND v.checkout_environment=$3`,
		intentID, userID, conversion.Environment()))
}

func (s *service) unifiedVIPPurchaseView(ctx context.Context, userID int64, intentID string) (*PurchaseIntent, error) {
	row, err := readVIPCheckoutIntent(ctx, userID, intentID)
	if err != nil {
		return nil, err
	}
	return buildVIPPurchaseIntentView(ctx, db.Pool(ctx), row)
}

func vipCheckoutReturnURLs(site *url.URL, id string) (string, string) {
	site.Path = "/recharge"
	site.RawQuery = url.Values{"purchase": {id}, "payment": {"return"}, "type": {"vip"}}.Encode()
	returnURL := site.String()
	site.RawQuery = url.Values{"purchase": {id}, "payment": {"cancel"}, "type": {"vip"}}.Encode()
	return returnURL, site.String()
}

func detachClosedPayPalVIPChild(ctx context.Context, row *vipCheckoutIntentRow) (bool, error) {
	if row.activeProvider != payment.ProviderPayPal || row.paypalIntentID == "" || row.switchTo != "" {
		return false, nil
	}
	tx, err := db.Pool(ctx).Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	var subID string
	err = tx.QueryRow(ctx, `
		SELECT COALESCE(i.subscription_id, '')
		FROM paypal_subscription_intents i
		JOIN user_subscriptions s ON s.subscription_id = i.subscription_id
		  AND s.user_id = i.user_id AND s.checkout_intent_id = i.intent_id
		  AND s.payment_method = 'paypal'
		WHERE i.intent_id = $1 AND i.user_id = $2 AND i.checkout_environment = $3
		  AND (s.status = 'EXPIRED'
		    OR (s.status = 'CANCELLED' AND COALESCE((s.attribution->>'remote_termination_confirmed')::boolean, false) = true))
		  AND s.last_payment_time IS NULL
		  AND s.paypal_activation_requested_at IS NULL
		FOR UPDATE OF i, s`, row.paypalIntentID, row.userID, row.environment).Scan(&subID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM paypal_subscription_intents WHERE intent_id = $1 AND user_id = $2`, row.paypalIntentID, row.userID); err != nil {
		return false, err
	}
	tag, err := tx.Exec(ctx, `UPDATE payment_vip_checkout_intents
		SET active_provider = NULL, paypal_intent_id = NULL, stripe_intent_id = NULL, subscription_id = NULL, updated_at = now()
		WHERE intent_id = $1 AND user_id = $2 AND checkout_environment = $3
		  AND status = 'Pending' AND active_provider = 'paypal' AND paypal_intent_id = $4::uuid
		  AND switch_to_provider IS NULL`, row.intentID, row.userID, row.environment, row.paypalIntentID)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() != 1 {
		return false, nil
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	row.activeProvider, row.paypalIntentID, row.subscriptionID = "", "", ""
	return true, nil
}

func detachClosedStripeVIPChild(ctx context.Context, row *vipCheckoutIntentRow) (bool, error) {
	if row.activeProvider != payment.ProviderStripe || row.stripeIntentID == "" || row.switchTo != "" {
		return false, nil
	}
	tx, err := db.Pool(ctx).Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	var childID string
	err = tx.QueryRow(ctx, `
		SELECT id::text FROM payment_stripe_subscription_intents
		WHERE id = $1 AND user_id = $2 AND checkout_environment = $3
		  AND status = 'closed' AND subscription_ref IS NULL
		FOR UPDATE`, row.stripeIntentID, row.userID, row.environment).Scan(&childID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	tag, err := tx.Exec(ctx, `UPDATE payment_vip_checkout_intents
		SET active_provider = NULL, paypal_intent_id = NULL, stripe_intent_id = NULL, subscription_id = NULL, updated_at = now()
		WHERE intent_id = $1 AND user_id = $2 AND checkout_environment = $3
		  AND status = 'Pending' AND active_provider = 'stripe' AND stripe_intent_id = $4::uuid
		  AND switch_to_provider IS NULL`, row.intentID, row.userID, row.environment, childID)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() != 1 {
		return false, nil
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	row.activeProvider, row.stripeIntentID, row.subscriptionID = "", "", ""
	return true, nil
}

func (s *service) refreshUnapprovedPayPalVIPBeforeRetry(ctx context.Context, row *vipCheckoutIntentRow) (bool, error) {
	if row.activeProvider != payment.ProviderPayPal || row.paypalIntentID == "" || row.switchTo != "" {
		return false, nil
	}
	var subID, planID, localStatus string
	var account payment.WebhookAccount
	err := db.Pool(ctx).QueryRow(ctx, `
		SELECT COALESCE(i.subscription_id,''), COALESCE(i.plan_id,''), COALESCE(s.status,''), a.account_key, a.environment
		FROM paypal_subscription_intents i
		JOIN payment_channel_accounts a ON a.id = i.checkout_account_id AND a.provider = 'paypal'
		JOIN user_subscriptions s ON s.subscription_id = i.subscription_id
		  AND s.user_id = i.user_id AND s.checkout_intent_id = i.intent_id
		  AND s.payment_method = 'paypal'
		WHERE i.intent_id = $1 AND i.user_id = $2 AND i.checkout_environment = $3
		  AND (s.status = 'PENDING'
		    OR (s.status = 'CANCELLED' AND COALESCE((s.attribution->>'remote_termination_confirmed')::boolean, false) = false))
		  AND s.last_payment_time IS NULL
		  AND s.paypal_activation_requested_at IS NULL`,
		row.paypalIntentID, row.userID, row.environment).Scan(&subID, &planID, &localStatus, &account.AccountKey, &account.Environment)
	if errors.Is(err, pgx.ErrNoRows) || err != nil || subID == "" {
		return false, nil
	}
	client, err := s.payPalForScope(ctx, account)
	if err != nil || client == nil {
		return false, ErrCheckoutPending
	}
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	details, err := client.GetSubscription(probeCtx, subID)
	cancel()
	if err != nil || details == nil || details.SubscriptionID != subID || details.PlanID != planID {
		return false, ErrCheckoutPending
	}
	switch {
	case (details.Status == "EXPIRED" || details.Status == "CANCELLED") && details.LastPaymentTime == nil:
		if err := s.repo.UpdateUserSubscriptionStatus(ctx, subID, details.Status); err != nil {
			return false, err
		}
		detached, err := detachClosedPayPalVIPChild(ctx, row)
		if err != nil {
			return false, err
		}
		if !detached {
			return false, ErrCheckoutPending
		}
		return false, nil
	case details.Status == "APPROVAL_PENDING" && details.LastPaymentTime == nil:
		if localStatus == "CANCELLED" {
			return false, ErrCheckoutPending
		}
		return true, nil
	case details.Status == "APPROVED":
		_ = s.activateApprovedPayPalVIP(ctx, subID)
		return true, nil
	case details.Status == "ACTIVE" || details.Status == "SUSPENDED" || details.LastPaymentTime != nil:
		if details.Status == "ACTIVE" || details.Status == "SUSPENDED" {
			if err := s.repo.UpdateUserSubscriptionStatus(ctx, subID, details.Status); err != nil {
				return false, err
			}
		}
		if details.LastPaymentTime != nil {
			if sub, subErr := s.repo.GetSubscriptionByID(ctx, subID); subErr == nil && sub != nil {
				end := time.Now().UTC().Truncate(time.Second)
				_ = s.importPayPalSubscriptionTransactions(ctx, sub, details, end.Add(-24*time.Hour), end)
			}
		}
		return true, nil
	default:
		return false, ErrCheckoutPending
	}
}

func (s *service) startVIPPayPalCheckout(ctx context.Context, userID int64, intentID, origin string) (*PurchaseIntent, error) {
	row, err := readVIPCheckoutIntent(ctx, userID, intentID)
	if err != nil {
		return nil, err
	}
	if row.status == "Completed" || row.status == "Canceled" {
		return buildVIPPurchaseIntentView(ctx, db.Pool(ctx), row)
	}
	if row.status != "Pending" {
		return nil, ErrVIPQuoteChanged
	}
	if err := s.resumeVIPSwitch(ctx, &row, payment.ProviderPayPal); err != nil {
		return nil, err
	}
	if row.activeProvider == payment.ProviderPayPal {
		detached, err := detachClosedPayPalVIPChild(ctx, &row)
		if err != nil {
			return nil, err
		}
		if !detached {
			verifiedExisting, err := s.refreshUnapprovedPayPalVIPBeforeRetry(ctx, &row)
			if err != nil {
				return nil, err
			}
			if verifiedExisting {
				return s.unifiedVIPPurchaseView(ctx, userID, intentID)
			}
		}
	}
	if row.activeProvider != payment.ProviderPayPal {
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
		if row.activeProvider == payment.ProviderStripe {
			if err := s.switchVIPChannel(ctx, row, payment.ProviderPayPal); err != nil {
				return nil, err
			}
		}
		returnURL, cancelURL := vipCheckoutReturnURLs(site, row.intentID)
		row.paypalIntentID, err = reserveUnifiedPayPalVIPIntentTx(ctx, row, returnURL, cancelURL, account)
		if err != nil {
			return nil, err
		}
		row.activeProvider = payment.ProviderPayPal
	}
	if err := s.sendUnifiedPayPalVIP(ctx, row, false); err != nil && !errors.Is(err, ErrSubscriptionPending) && !errors.Is(err, ErrSubscriptionReview) {
		return nil, err
	}
	return s.unifiedVIPPurchaseView(ctx, userID, intentID)
}

func (s *service) startVIPStripeCheckout(ctx context.Context, userID int64, intentID, origin string) (*PurchaseIntent, error) {
	row, err := readVIPCheckoutIntent(ctx, userID, intentID)
	if err != nil {
		return nil, err
	}
	if row.status == "Completed" || row.status == "Canceled" {
		return buildVIPPurchaseIntentView(ctx, db.Pool(ctx), row)
	}
	if row.status != "Pending" {
		return nil, ErrVIPQuoteChanged
	}
	if err := s.resumeVIPSwitch(ctx, &row, payment.ProviderStripe); err != nil {
		return nil, err
	}
	if row.activeProvider == payment.ProviderStripe {
		if _, err := detachClosedStripeVIPChild(ctx, &row); err != nil {
			return nil, err
		}
	}
	reserveStripe := func() error {
		adapter, available := s.stripeVIPAdapter()
		if !available || !stripeVIPCheckoutEnabled(ctx) {
			return ErrCheckoutPaused
		}
		site, err := s.checkoutSite(ctx, origin)
		if err != nil {
			return err
		}
		if row.activeProvider == payment.ProviderPayPal {
			if err := s.switchVIPChannel(ctx, row, payment.ProviderStripe); err != nil {
				return err
			}
		}
		returnURL, cancelURL := vipCheckoutReturnURLs(site, row.intentID)
		row.stripeIntentID, err = reserveUnifiedStripeVIPIntentTx(ctx, row, returnURL, cancelURL, adapter.AccountScope())
		if err != nil {
			return err
		}
		row.activeProvider = payment.ProviderStripe
		return nil
	}
	createdFresh := false
	if row.activeProvider != payment.ProviderStripe {
		if err := reserveStripe(); err != nil {
			return nil, err
		}
		createdFresh = true
	}
	s.refreshUnifiedStripeVIP(ctx, row)
	if !createdFresh {
		// If refresh just observed that Stripe expired the previous Checkout Session,
		// detach the closed child and create a fresh session in the same click.
		if detached, err := detachClosedStripeVIPChild(ctx, &row); err != nil {
			return nil, err
		} else if detached {
			if err := reserveStripe(); err != nil {
				return nil, err
			}
			s.refreshUnifiedStripeVIP(ctx, row)
		}
	}
	return s.unifiedVIPPurchaseView(ctx, userID, intentID)
}

func reserveUnifiedPayPalVIPIntentTx(
	ctx context.Context, v vipCheckoutIntentRow, returnURL, cancelURL string, scope payment.WebhookAccount,
) (string, error) {
	evaluation := checkoutEvaluation{stage: subscriptionIntentStage, userID: v.userID, provider: payment.ProviderPayPal, slotID: v.slotID}
	id, err := evaluateCheckout(ctx, evaluation, func(tx pgx.Tx, e *checkoutEvaluation) (*string, error) {
		if err := lockVIPForChannelReservation(ctx, tx, v); err != nil {
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
			v.userID, ppID, v.productID, v.snapshot, v.slotID, v.templateID, v.cycle, v.priceCents,
			ppReturn, ppCancel, v.attribution, accountID, v.returnPath, planID, conversion.Environment())
		if err != nil {
			return nil, err
		}
		e.accountID, e.checkoutID = accountID, v.intentID
		return &ppID, attachVIPChannelTx(ctx, tx, v.intentID, payment.ProviderPayPal, ppID)
	})
	if err != nil {
		return "", err
	}
	return *id, nil
}

func reserveUnifiedStripeVIPIntentTx(
	ctx context.Context, v vipCheckoutIntentRow, returnURL, cancelURL string, scope payment.WebhookAccount,
) (string, error) {
	evaluation := checkoutEvaluation{stage: subscriptionIntentStage, provider: payment.ProviderStripe, userID: v.userID, slotID: v.slotID}
	id, err := evaluateCheckout(ctx, evaluation, func(tx pgx.Tx, e *checkoutEvaluation) (*string, error) {
		if err := lockVIPForChannelReservation(ctx, tx, v); err != nil {
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
		e.accountID, e.checkoutID = accountID, v.intentID
		return &stID, attachVIPChannelTx(ctx, tx, v.intentID, payment.ProviderStripe, stID)
	})
	if err != nil {
		return "", err
	}
	return *id, nil
}

func attachVIPChannelTx(ctx context.Context, tx pgx.Tx, parentID, provider, childID string) error {
	tag, err := tx.Exec(ctx, `UPDATE payment_vip_checkout_intents
		SET active_provider=$2, paypal_intent_id=CASE WHEN $2='paypal' THEN $3::uuid END,
		    stripe_intent_id=CASE WHEN $2='stripe' THEN $3::uuid END, updated_at=now()
		WHERE intent_id=$1 AND status='Pending' AND active_provider IS NULL
		  AND paypal_intent_id IS NULL AND stripe_intent_id IS NULL AND switch_to_provider IS NULL`,
		parentID, provider, childID)
	if err == nil && tag.RowsAffected() != 1 {
		return ErrCheckoutPending
	}
	return err
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
