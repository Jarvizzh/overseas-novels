package application

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"reader-backend/internal/conversion"
	"reader-backend/internal/db"
	"reader-backend/internal/payment"
	"reader-backend/internal/payment/aftercare"
	"reader-backend/internal/payment/ledger"
)

func (r *dbRepository) applyStripeProof(ctx context.Context, a stripePurchaseAttempt, proof payment.SingleCheckoutProof,
	movement *payment.LedgerEvidence) error {
	if proof.ID == "" || proof.AttemptID != a.ID || (a.SessionID != "" && a.SessionID != proof.ID) {
		return ErrCheckoutUnavailable
	}
	tx, err := db.Pool(ctx).Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if proof.Receipt != nil {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('stripe-capture:'||$1,0))`, proof.Receipt.ID); err != nil {
			return err
		}
	}

	if proof.Receipt != nil {
		if err := lockWholeBookBuyer(ctx, tx, a.OrderID); err != nil {
			return err
		}
	}

	var userID int64
	var state string
	var fulfilled bool
	var charged, bonus int
	var orderType string
	err = tx.QueryRow(ctx, `SELECT user_id,status,fulfilled_at IS NOT NULL,coins,bonus_coins_credited,order_type
		FROM recharge_orders WHERE id=$1 AND checkout_environment=$2 AND order_type IN ('single','whole_book') FOR UPDATE`,
		a.OrderID, conversion.Environment()).Scan(&userID, &state, &fulfilled, &charged, &bonus, &orderType)
	if err != nil {
		return err
	}
	var stored *string
	var current string
	err = tx.QueryRow(ctx, `SELECT provider_order_id,status FROM payment_checkout_attempts
		WHERE id=$1 AND order_id=$2 AND account_id=$3 AND provider='stripe' AND sales_mode=$4 FOR UPDATE`,
		a.ID, a.OrderID, a.AccountID, a.Offer.SalesMode).Scan(&stored, &current)
	if err != nil {
		return err
	}
	if stored != nil && *stored != proof.ID {
		return ErrCheckoutUnavailable
	}
	if proof.Receipt == nil {
		if current == "settled" || current == "closed" {
			return tx.Commit(ctx)
		}
		status := "capturing"
		switch {
		case proof.Expired || proof.Failed:
			status = "closed"
		case proof.AwaitingBuyer:
			status = "buyer_action"
		}
		_, err = tx.Exec(ctx, `UPDATE payment_checkout_attempts SET provider_order_id=$2,status=$3,approval_url=NULLIF($4,''),
			capture_outcome=CASE WHEN $5 THEN 'payment_declined' ELSE capture_outcome END WHERE id=$1`,
			a.ID, proof.ID, status, proof.URL, proof.Failed)
		if err != nil {
			return err
		}
		if status == "buyer_action" {
			if err := recordCheckoutInitiatedTx(ctx, tx, a.IntentID, proof.ID); err != nil {
				return err
			}
		}
		return tx.Commit(ctx)
	}
	receipt := proof.Receipt
	if receipt.Currency != "USD" {
		_, err = tx.Exec(ctx, `UPDATE recharge_orders SET reconciliation_note='Stripe reported an unexpected settlement currency'
			WHERE id=$1 AND fulfilled_at IS NULL`, a.OrderID)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE payment_checkout_attempts SET provider_order_id=$2,status='capturing',approval_url=NULL,capture_lookup_only=true WHERE id=$1`, a.ID, proof.ID)
		if err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	var differentAttempt, knownRefund bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM payment_checkout_attempts
		WHERE order_id=$1 AND id<>$2 AND status IN ('creating','buyer_action','capturing')) OR EXISTS (
		SELECT 1 FROM payment_fulfillment_funding f JOIN third_party_payment_orders t ON t.id=f.receipt_id
		WHERE f.order_id=$1 AND (t.capture_id<>$3 OR t.account_id IS DISTINCT FROM $4)
	) OR EXISTS(SELECT 1 FROM payment_deferred_fulfillment d JOIN third_party_payment_orders other ON other.id=d.receipt_id
	    WHERE d.order_id=$1 AND (other.capture_id<>$3 OR other.account_id IS DISTINCT FROM $4)
	) OR EXISTS(SELECT 1 FROM third_party_payment_orders ignored WHERE ignored.order_id=$1 AND ignored.account_id=$4 AND ignored.capture_id=$3
	    AND ignored.reconciliation_note='Stripe received a payment outside the active fulfillment attempt'),
	EXISTS(SELECT 1 FROM payment_stripe_refund_items refund JOIN third_party_payment_orders t ON t.id=refund.receipt_id
	    WHERE t.account_id=$4 AND t.capture_id=$3)`, a.OrderID, a.ID, receipt.ID, a.AccountID).Scan(&differentAttempt, &knownRefund)
	if err != nil {
		return err
	}
	var duplicateBook bool
	if orderType == "whole_book" {
		duplicateBook, err = anotherWholeBookFulfilled(ctx, tx, a.OrderID, conversion.Environment())
		if err != nil {
			return err
		}
		duplicateBook = duplicateBook && !fulfilled
	}
	extra := differentAttempt || current == "closed" || state == "Refunded" || duplicateBook
	var note *string
	principal := receipt.PrincipalMinor
	if a.Offer.SalesMode == payment.StripeSaleDirect && principal == 0 {
		principal = a.Offer.AmountMinor
	}
	switch {
	case extra:
		note = checkoutNote("Stripe received a payment outside the active fulfillment attempt")
	case receipt.PresentmentCurrency != "" && receipt.PresentmentCurrency != "USD":
		note = checkoutNote("Stripe buyer payment currency is outside the approved USD scope")
	case !receipt.EligibleTerms:
		note = checkoutNote("Stripe collected amount or line item differs from the checkout snapshot")
	case principal <= 0 || principal > receipt.AmountMinor:
		note = checkoutNote("Stripe payment has no verified amount excluding tax")
	case receipt.RefundedMinor != 0 || knownRefund:
		note = checkoutNote("Stripe reports a refund; reconcile the original payment")
	case receipt.ReviewUnconfirmed:
		note = checkoutNote("Stripe review status needs verification")
	}
	if note == nil && !fulfilled {
		market := stripeMarketEvidence{Reference: receipt.ID, SalesMode: a.Offer.SalesMode, Country: receipt.Country,
			ExpectedCountry: a.Offer.ExpectedCountry, TaxAccount: receipt.TaxLiabilityAccount,
			ExpectedTaxAccount: a.Offer.TaxLiabilityAccount, CustomerTaxStatus: receipt.CustomerTaxStatus, TaxVerified: receipt.TaxVerified,
			AmountMinor: receipt.AmountMinor}
		reason, err := stripeMarketReview(ctx, tx, a.OrderID, market, stripeMarketNote(a.Offer, market))
		if err != nil {
			return err
		}
		if reason != "" {
			note = checkoutNote(reason)
		}
	}

	var receiptID int64
	err = tx.QueryRow(ctx, `INSERT INTO third_party_payment_orders(order_id,payment_provider,external_order_id,capture_id,
		payer_country,currency,gross_amount,status,raw_payload,account_id,object_type,reconciliation_note)
		VALUES($1,'stripe',$2,$3,NULLIF($4,''),'USD',$5::bigint::numeric/100,'VERIFIED',$6,$7,'charge',$8)
		ON CONFLICT(payment_provider,external_order_id,capture_id) DO UPDATE SET
		    raw_payload=EXCLUDED.raw_payload,reconciliation_note=EXCLUDED.reconciliation_note
		WHERE third_party_payment_orders.order_id=EXCLUDED.order_id AND third_party_payment_orders.account_id=EXCLUDED.account_id
		  AND third_party_payment_orders.gross_amount=EXCLUDED.gross_amount
		RETURNING id`, a.OrderID, proof.ID, receipt.ID, receipt.Country, receipt.AmountMinor, receipt.Evidence, a.AccountID, note).Scan(&receiptID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrCheckoutUnavailable
	}
	if err != nil {
		return err
	}
	if receipt.RiskReview != nil {
		if err := aftercare.RecordTx(ctx, tx, *receipt.RiskReview); err != nil {
			return err
		}
	}

	dispute, err := unresolvedStripeChargeDisputeTx(ctx, tx, receiptID, receipt.Disputed)
	if err != nil {
		return err
	}
	if note == nil && dispute {
		note = checkoutNote("Stripe charge has an unresolved or unverified dispute")
		if _, err := tx.Exec(ctx, `UPDATE third_party_payment_orders SET reconciliation_note=$2 WHERE id=$1`, receiptID, note); err != nil {
			return err
		}
	}

	if movement != nil {
		if _, err := ledger.RecordTx(ctx, tx, *movement); err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `UPDATE payment_stripe_checkout_terms SET observed_country=NULLIF($2,''),country_checked_at=now(),
		observed_tax_liability_account=NULLIF($5,''),observed_customer_tax_status=NULLIF($6,''),
		ledger_observed_at=CASE WHEN $4 THEN NULL WHEN $3 THEN COALESCE(ledger_observed_at,now()) ELSE ledger_observed_at END WHERE attempt_id=$1`,
		a.ID, receipt.Country, movement != nil, receipt.ReviewUnconfirmed || dispute, receipt.TaxLiabilityAccount, receipt.CustomerTaxStatus)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE payment_checkout_attempts SET provider_order_id=$2,status='settled',approval_url=NULL WHERE id=$1`, a.ID, proof.ID)
	if err != nil {
		return err
	}
	if fulfilled && !extra && receipt.EligibleTerms && principal > 0 && principal <= receipt.AmountMinor {
		_, err := tx.Exec(ctx, `UPDATE payment_fulfillment_funding SET principal_minor=$3 WHERE order_id=$1 AND receipt_id=$2 AND principal_minor IS NULL`,
			a.OrderID, receiptID, principal)
		if err != nil {
			return err
		}
	} else if fulfilled && !extra && movement != nil {
		_, err := tx.Exec(ctx, `UPDATE payment_checkout_attempts p SET next_create_at=now()+interval '1 day'
			WHERE p.id=$1 AND EXISTS(SELECT 1 FROM payment_fulfillment_funding f
				WHERE f.order_id=p.order_id AND f.receipt_id=$2 AND f.principal_minor IS NULL)`, a.ID, receiptID)
		if err != nil {
			return err
		}
	}
	if duplicateBook {
		if err := parkAdditionalWholeBookPayment(ctx, tx, a.OrderID, payment.ProviderStripe, &receipt.PaidAt); err != nil {
			return err
		}
	}
	if extra || fulfilled {
		return tx.Commit(ctx)
	}
	if err := recordCheckoutInitiatedTx(ctx, tx, a.IntentID, proof.ID); err != nil {
		return err
	}
	if receipt.RefundedMinor >= receipt.AmountMinor && receipt.RefundedMinor > 0 {
		if _, err := tx.Exec(ctx, `UPDATE recharge_orders SET payment_method='stripe',external_ref_id=COALESCE(external_ref_id,$2),
			paid_at=COALESCE(paid_at,$3),status='Refunded',reconciliation_note=NULL,updated_at=now() WHERE id=$1`,
			a.OrderID, proof.ID, receipt.PaidAt); err != nil {
			return err
		}
		if err := aftercare.FinishFulfillment(ctx, tx, a.OrderID); err != nil {
			return err
		}
		if err := conversion.RecordPaymentTx(ctx, tx, a.OrderID, receipt.PaidAt, "stripe_pre_fulfillment_refund", false, false); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	_, err = tx.Exec(ctx, `UPDATE recharge_orders SET payment_method='stripe',external_ref_id=COALESCE(external_ref_id,$2),
		paid_at=COALESCE(paid_at,$3),status='Paid',reconciliation_note=$4,updated_at=now() WHERE id=$1`,
		a.OrderID, proof.ID, receipt.PaidAt, note)
	if err != nil {
		return err
	}
	if note != nil {
		return tx.Commit(ctx)
	}
	hold, err := aftercare.EvaluateFulfillment(ctx, tx, a.OrderID, receiptID)
	if err != nil {
		return err
	}
	if hold.Deferred {
		return tx.Commit(ctx)
	}
	if err := recordFundingTx(ctx, tx, a.OrderID, receiptID, principal, "USD"); err != nil {
		return err
	}
	if charged+bonus > 0 {
		creditDesc := "Stripe whole book purchase"
		if orderType == "single" {
			creditDesc = "Stripe coins recharge"
		}
		if err := r.fulfillment.CreditRechargeTx(ctx, tx, userID, charged, bonus, creditDesc); err != nil {
			return err
		}
	}
	if err := aftercare.FinishFulfillment(ctx, tx, a.OrderID); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE recharge_orders SET status='Success',fulfilled_at=clock_timestamp(),updated_at=now()
		WHERE id=$1`, a.OrderID)
	if err != nil {
		return err
	}
	if err := conversion.RecordPaymentTx(ctx, tx, a.OrderID, receipt.PaidAt, "stripe_charge_created", false, false); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func checkoutNote(text string) *string { return &text }

func unresolvedStripeChargeDisputeTx(ctx context.Context, tx pgx.Tx, receiptID int64, reported bool) (bool, error) {
	var open, cleared bool
	err := tx.QueryRow(ctx, `SELECT COALESCE(bool_or(c.is_open),false),
		COALESCE(bool_or(NOT c.is_open AND c.outcome='won'),false)
		FROM payment_case_receipts r JOIN payment_cases c ON c.id=r.case_id
		WHERE r.receipt_id=$1 AND c.kind='dispute'`, receiptID).Scan(&open, &cleared)
	return open || (reported && !cleared), err
}
