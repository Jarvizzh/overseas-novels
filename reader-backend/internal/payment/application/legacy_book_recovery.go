package application

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"reader-backend/internal/conversion"
	"reader-backend/internal/db"
	"reader-backend/internal/payment"
	"reader-backend/internal/payment/paypal"
)

const unprovenLegacyBookNote = "Independent historical whole-book purchase evidence is required"

const agedLegacyBookNote = "Historical whole-book payment requires review"

func agedLegacyBook(alias string) string {
	return alias + `.order_type='whole_book' AND ` + alias + `.payment_method='paypal'
		AND ` + alias + `.checkout_id IS NOT NULL AND ` + alias + `.created_at<=now()-` + payPalOrderWindow + `
		AND ` + alias + `.external_ref_id IS NOT NULL AND ` + alias + `.external_ref_id NOT LIKE 'mock%'
		AND NOT EXISTS(SELECT 1 FROM payment_checkout_attempts p WHERE p.order_id=` + alias + `.id)`
}

func agedLegacyBookRecovery(alias, environment string) string {
	return agedLegacyBook(alias) + ` AND ` + alias + `.status='Pending'
		AND (` + alias + `.checkout_environment IS NULL OR ` + alias + `.checkout_environment=` + environment + `)
		AND COALESCE(payment_order_environment(` + alias + `),` + channelEnvironmentSQL(environment) + `)=` + channelEnvironmentSQL(environment)
}

func agedLegacyBookLookupOnly(alias string) string {
	return agedLegacyBook(alias) + ` AND (` + alias + `.status IN ('Pending','Failed')
		OR COALESCE(` + alias + `.reconciliation_note='` + agedLegacyBookNote + `',false))`
}

func agedLegacyBookVisible(alias, environment string) string {
	return `(` + agedLegacyBookRecovery(alias, environment) + `) OR (` + agedLegacyBookLookupOnly(alias) + `
		AND ` + alias + `.status='Paid' AND ` + alias + `.fulfilled_at IS NULL
		AND (` + alias + `.checkout_environment IS NULL OR ` + alias + `.checkout_environment=` + environment + `)
		AND payment_order_environment(` + alias + `)=` + channelEnvironmentSQL(environment) + `)`
}

func channelEnvironmentSQL(environment string) string {
	return `CASE ` + environment + ` WHEN 'production' THEN 'live' WHEN 'sandbox' THEN 'sandbox' END`
}

func agedLegacyBookAccount(ctx context.Context, orderID int64) (payment.WebhookAccount, error) {
	var account payment.WebhookAccount
	err := db.Pool(ctx).QueryRow(ctx, `SELECT l.client_id,l.environment FROM paypal_identity_lock l
		JOIN recharge_orders o ON o.id=$1
		WHERE `+agedLegacyBook("o")+`
		AND (o.checkout_environment IS NULL OR o.checkout_environment=$2)
		AND NOT EXISTS(SELECT 1 FROM third_party_payment_orders r
			JOIN payment_channel_accounts a ON a.id=r.account_id
			WHERE r.order_id=o.id AND r.status IN ('VERIFIED','COMPLETED')
			AND (a.provider,a.account_key,a.environment)<>('paypal',l.client_id,l.environment))`,
		orderID, conversion.Environment()).Scan(&account.AccountKey, &account.Environment)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !paymentEnvironmentMatches(account)) {
		return payment.WebhookAccount{}, ErrCheckoutUnavailable
	}
	return account, err
}

func (s *service) reconcileAgedLegacyBook(ctx context.Context, order *checkoutOrder, ref string) error {
	account, err := agedLegacyBookAccount(ctx, order.id)
	if err != nil {
		return err
	}
	client, err := s.payPalForScope(ctx, account)
	if err != nil {
		return err
	}
	return s.lookupLegacyBook(ctx, order, ref, client, account, agedLegacyBookNote)
}

func (s *service) reconcileUnprovenLegacyBook(ctx context.Context, order *checkoutOrder, ref string) error {
	client, err := s.payPalForOrder(ctx, ref)
	if err != nil || client == nil {
		return errors.Join(ErrCheckoutUnavailable, err)
	}
	return s.lookupLegacyBook(ctx, order, ref, client, payment.WebhookAccount{}, unprovenLegacyBookNote)
}

func (s *service) lookupLegacyBook(ctx context.Context, order *checkoutOrder, ref string, client paypalCheckout, account payment.WebhookAccount, note string) error {
	capture, lookupErr := client.GetCapture(ctx, ref)
	if lookupErr != nil && !errors.Is(lookupErr, paypal.ErrOrderClosed) && !errors.Is(lookupErr, paypal.ErrOrderApproved) {
		return lookupErr
	}
	if capture == nil || capture.OrderID != ref || capture.CurrencyCode != order.currency || !paymentEnvironmentMatches(capture.Account) ||
		(account.AccountKey != "" && capture.Account != account) {
		return ErrCheckoutUnavailable
	}
	amount, err := payment.ParseCents(capture.GrossAmount)
	if err != nil || amount != order.amount {
		return ErrCheckoutUnavailable
	}
	var knownAccount bool
	err = db.Pool(ctx).QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM paypal_identity_lock
		WHERE client_id=$1 AND environment=$2)`, capture.Account.AccountKey, capture.Account.Environment).Scan(&knownAccount)
	if err != nil {
		return err
	}
	if !knownAccount {
		return ErrCheckoutUnavailable
	}
	if errors.Is(lookupErr, paypal.ErrOrderClosed) && capture.CaptureID == "" && order.status == "Pending" {
		if err := s.repo.closeLegacyWholeBookOrder(ctx, order.id, capture.Account); err != nil {
			return err
		}
		return ErrCheckoutPending
	}
	if lookupErr == nil && capture.CaptureID != "" && (capture.Status == "COMPLETED" || capture.Status == "REFUNDED") {
		if err := recordLegacyBookReceipt(ctx, order, capture, note); err != nil {
			return err
		}
	}
	return ErrCheckoutPending
}

func recordLegacyBookReceipt(ctx context.Context, order *checkoutOrder, capture *payment.OrderCapture, note string) error {
	tx, err := db.Pool(ctx).Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if err := lockPayPalCapture(ctx, tx, capture.CaptureID); err != nil {
		return err
	}
	if err := lockWholeBookBuyer(ctx, tx, order.id); err != nil {
		return err
	}
	current, err := scanCheckout(tx.QueryRow(ctx, checkoutSelect+" FOR UPDATE", capture.OrderID))
	if err != nil {
		return err
	}
	if current.id != order.id || !current.isLegacyWholeBook() || current.fulfilled || current.status == "Refunded" ||
		(order.agedLegacyBook && !current.agedLegacyBook) {
		return ErrCheckoutUnavailable
	}
	// Earlier readers stored captures without a channel account. Bind only
	// the exact receipt just verified on the original merchant.
	_, err = tx.Exec(ctx, `UPDATE third_party_payment_orders p SET account_id=a.id,object_type='capture'
		FROM payment_channel_accounts a WHERE a.provider='paypal' AND a.account_key=$4 AND a.environment=$5
		AND p.order_id=$1 AND p.payment_provider='paypal' AND p.external_order_id=$2 AND p.capture_id=$3
		AND p.gross_amount*100=$6 AND p.currency=$7 AND p.account_id IS NULL AND p.status IN ('VERIFIED','COMPLETED')`,
		order.id, capture.OrderID, capture.CaptureID, capture.Account.AccountKey, capture.Account.Environment, order.amount, order.currency)
	if err != nil {
		return err
	}
	if _, err := recordCheckoutReceipt(ctx, tx, current, capture, false); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE recharge_orders SET status='Paid',paid_at=COALESCE(paid_at,$2,now()),
		reconciliation_note=$3,updated_at=now() WHERE id=$1`,
		order.id, verifiedPaidAt(capture.OccurredAt), note)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
