package application

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"reader-backend/internal/conversion"
	"reader-backend/internal/db"
	"reader-backend/internal/payment"
	"reader-backend/internal/payment/aftercare"
	"reader-backend/internal/payment/ledger"
)

func settleStripeVIPInvoice(ctx context.Context, v stripeVIPIntent, proof payment.StripeVIPInvoice, movements []payment.LedgerEvidence) error {
	byCharge := make(map[string]*payment.LedgerEvidence, len(movements))
	for i := range movements {
		m := &movements[i]
		for _, ref := range m.References {
			if ref.Type == "stripe_charge" && ref.ID != "" {
				byCharge[ref.ID] = m
			}
		}
	}
	verifications := make(map[string]stripeSettlementVerification, len(proof.Allocations))
	for _, alloc := range proof.Allocations {
		receipt := alloc.Receipt
		verifications[receipt.ID] = settlementVerificationFromMovement(&receipt, byCharge[receipt.ID])
	}
	return settleStripeVIPInvoiceWithVerification(ctx, v, proof, verifications)
}

func settleStripeVIPInvoiceWithVerification(ctx context.Context, v stripeVIPIntent, proof payment.StripeVIPInvoice, verifications map[string]stripeSettlementVerification) error {
	if proof.ID == "" || proof.Subscription.ID == "" || proof.Subscription.IntentID != v.ID || proof.DueMinor < 0 || proof.DueMinor > math.MaxInt32 {
		return ErrCheckoutUnavailable
	}
	tx, err := db.Pool(ctx).Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if err := lockStripeVIPCharges(ctx, tx, proof.Allocations); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('vip-settlement:'||$1::bigint::text||':'||$2,0))`,
		v.UserID, conversion.Environment()); err != nil {
		return err
	}
	// An intent serializes admission of all its invoices. Lock the order before
	// the agreement so that refunds and dispute adjustments use the same order.
	if _, err := tx.Exec(ctx, `SELECT id FROM payment_stripe_subscription_intents WHERE id=$1 AND account_id=$2 FOR UPDATE`, v.ID, v.AccountID); err != nil {
		return err
	}
	orderID, admitted, err := lockStripeVIPOrder(ctx, tx, v, proof)
	if err != nil {
		return err
	}
	if err := bindStripeVIPTx(ctx, tx, v, proof.Subscription); err != nil {
		return err
	}
	funding, paidAt, note, err := recordStripeVIPAllocations(ctx, tx, v, proof, orderID, verifications)
	if err != nil {
		return err
	}
	for _, alloc := range proof.Allocations {
		if sv, ok := verifications[alloc.Receipt.ID]; ok && sv.Movement != nil {
			if _, err := ledger.RecordTx(ctx, tx, *sv.Movement); err != nil {
				return err
			}
		}
	}
	var existingRetryUntil *time.Time
	err = tx.QueryRow(ctx, `SELECT settlement_retry_until FROM payment_stripe_invoice_periods
		WHERE account_id=$1 AND invoice_ref=$2 FOR UPDATE`, v.AccountID, proof.ID).Scan(&existingRetryUntil)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	settlementState := payment.SettlementStateNone
	pendingSettlement := false
	hasVerifiedMovement := false
	var retryUntil, nextCheckAt, verifiedAt *time.Time
	for _, alloc := range proof.Allocations {
		sv := verifications[alloc.Receipt.ID]
		switch sv.Outcome {
		case settlementOutcomeCurrencyMismatch, settlementOutcomeAmountMismatch:
			settlementState = payment.SettlementStateReview
			if note == "" {
				note = sv.Note
			}
		case settlementOutcomePending:
			if settlementState != payment.SettlementStateReview {
				if existingRetryUntil != nil && !existingRetryUntil.After(now) {
					settlementState = payment.SettlementStateReview
					if note == "" {
						note = stripeNoteSettlementWindowExhausted
					}
				} else {
					settlementState = payment.SettlementStatePending
					pendingSettlement = true
					if existingRetryUntil != nil {
						retryUntil = existingRetryUntil
					} else {
						deadline := now.Add(stripeSettlementRetryWindow)
						retryUntil = &deadline
					}
					next := now.Add(stripeSettlementPollInterval)
					nextCheckAt = &next
				}
			}
		case settlementOutcomeVerified:
			hasVerifiedMovement = true
		}
	}
	if settlementState == payment.SettlementStateNone && hasVerifiedMovement {
		settlementState = payment.SettlementStateSettled
		verifiedAt = &now
	}
	if !admitted {
		note, err = stripeVIPAdmissionNote(ctx, tx, v, proof, orderID, funding, note)
		if err != nil {
			return err
		}
		admit := note == "" && !pendingSettlement && proof.Status == "paid" && proof.RemainingMinor == 0 && !paidAt.IsZero()
		if err := updateStripeVIPPeriod(ctx, tx, v, proof, orderID, note, paidAt, admit, settlementState, retryUntil, nextCheckAt, verifiedAt); err != nil {
			return err
		}
		if admit {
			if err := recordStripeVIPFunding(ctx, tx, v, proof, orderID, funding, false); err != nil {
				return err
			}
			if err := grantStripeVIPPeriod(ctx, tx, proof, orderID, paidAt); err != nil {
				return err
			}
			if proof.Subscription.CustomerID != "" && v.Account.Environment != "" {
				if _, err := upsertStripeCustomerTx(ctx, tx, v.UserID, v.AccountID, v.Account.Environment,
					proof.Subscription.CustomerID, proof.Country, proof.Country != "" && proof.TaxVerified); err != nil {
					return err
				}
			}
		} else if !pendingSettlement {
			if err := finalizeUnfulfilledStripeVIPRefund(ctx, tx, orderID); err != nil {
				return err
			}
		}
	} else {
		if settlementState == payment.SettlementStateSettled {
			if _, err := tx.Exec(ctx, `UPDATE payment_stripe_invoice_periods SET settlement_state=$3,
				settlement_next_check_at=NULL,settlement_verified_at=COALESCE(settlement_verified_at,$4),updated_at=now()
				WHERE account_id=$1 AND invoice_ref=$2`, v.AccountID, proof.ID, settlementState, verifiedAt); err != nil {
				return err
			}
		}
		if note == "" && proof.Status == "paid" && proof.RemainingMinor == 0 && stripeVIPPrincipal(v, proof) > 0 {
			if err := recordStripeVIPFunding(ctx, tx, v, proof, orderID, funding, true); err != nil {
				return err
			}
		}
	}
	return tx.Commit(ctx)
}

func lockStripeVIPCharges(ctx context.Context, tx pgx.Tx, allocations []payment.StripeVIPAllocation) error {
	ids := make([]string, 0, len(allocations))
	for _, alloc := range allocations {
		ids = append(ids, alloc.Receipt.ID)
	}
	slices.Sort(ids)
	for _, id := range slices.Compact(ids) {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('stripe-capture:'||$1,0))`, id); err != nil {
			return err
		}
	}
	return nil
}

func lockStripeVIPOrder(ctx context.Context, tx pgx.Tx, v stripeVIPIntent, proof payment.StripeVIPInvoice) (int64, bool, error) {
	var orderID int64
	var admitted bool
	var intent string
	err := tx.QueryRow(ctx, `SELECT o.id,p.admitted,p.intent_id::text FROM payment_stripe_invoice_periods p
		JOIN recharge_orders o ON o.id=p.order_id WHERE p.account_id=$1 AND p.invoice_ref=$2 FOR UPDATE OF o`,
		v.AccountID, proof.ID).Scan(&orderID, &admitted, &intent)
	if err == nil {
		if intent != v.ID {
			return 0, false, ErrCheckoutUnavailable
		}
		return orderID, admitted, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, false, err
	}
	err = tx.QueryRow(ctx, `INSERT INTO recharge_orders(user_id,external_ref_id,amount_cents,currency,coins,bonus_coins_credited,
		payment_method,status,order_type,subscription_id,recharge_slot_id,recharge_template_id,product_id,product_snapshot,checkout_environment,
		utm_source,utm_campaign,promotion_link_id,novel_id,fb_lead_metadata)
		SELECT v.user_id,$2,$3,'USD',0,0,'stripe','Pending','subscription',$4,v.slot_id,t.id,v.product_id,v.product_snapshot,v.checkout_environment,
		   COALESCE(v.attribution->>'utm_source',u.utm_source),COALESCE(v.attribution->>'utm_campaign',u.utm_campaign),
		   p.id,p.novel_id,v.attribution
		FROM payment_stripe_subscription_intents v JOIN users u ON u.id=v.user_id
		LEFT JOIN recharge_templates t ON t.id=v.template_id
		LEFT JOIN LATERAL reader_promotion_for_user_env(v.user_id, CASE WHEN v.checkout_environment IN ('sandbox','test') THEN 'sandbox' ELSE 'live' END) p ON true WHERE v.id=$1 RETURNING id`,
		v.ID, proof.ID, proof.DueMinor, proof.Subscription.ID).Scan(&orderID)
	if err != nil {
		return 0, false, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO payment_stripe_invoice_periods(account_id,invoice_ref,intent_id,order_id,billed_minor,billing_reason)
		VALUES($1,$2,$3,$4,$5,$6)`, v.AccountID, proof.ID, v.ID, orderID, proof.DueMinor, proof.BillingReason)
	return orderID, false, err
}

type stripeVIPFunding struct {
	receiptID                int64
	allocatedMinor, netMinor int64
	disputed                 bool
}

func recordStripeVIPAllocations(ctx context.Context, tx pgx.Tx, v stripeVIPIntent, proof payment.StripeVIPInvoice, orderID int64, verifications map[string]stripeSettlementVerification) ([]stripeVIPFunding, time.Time, string, error) {
	note := proof.ReviewReason
	var funding []stripeVIPFunding
	var paidAt time.Time
	seen := make(map[string]bool, len(proof.Allocations))
	for _, allocation := range proof.Allocations {
		receipt := allocation.Receipt
		if seen[receipt.ID] {
			note = "Stripe VIP invoice contains repeated allocations from a single charge"
			continue
		}
		seen[receipt.ID] = true
		entry, err := saveStripeVIPAllocation(ctx, tx, v, proof, orderID, allocation, verifications[receipt.ID].Movement)
		if errors.Is(err, pgx.ErrNoRows) {
			note = "Stripe VIP allocation conflicts with existing collection evidence"
			continue
		}
		if err != nil {
			return nil, time.Time{}, "", err
		}
		if receipt.PaidAt.After(paidAt) {
			paidAt = receipt.PaidAt
		}
		if receipt.ReviewUnconfirmed || !receipt.EligibleTerms {
			note = "Stripe VIP charge allocation or review needs verification"
		}
		funding = append(funding, entry)
	}
	return funding, paidAt, note, nil
}

func saveStripeVIPAllocation(ctx context.Context, tx pgx.Tx, v stripeVIPIntent, proof payment.StripeVIPInvoice,
	orderID int64, allocation payment.StripeVIPAllocation, movement *payment.LedgerEvidence) (stripeVIPFunding, error) {
	receipt := allocation.Receipt
	if allocation.ID == "" || receipt.ID == "" || allocation.AmountMinor <= 0 || receipt.Currency != payment.CurrencyUSD ||
		receipt.AmountMinor < allocation.AmountMinor || receipt.PaidAt.IsZero() {
		return stripeVIPFunding{}, pgx.ErrNoRows
	}
	entry := stripeVIPFunding{allocatedMinor: allocation.AmountMinor, disputed: receipt.Disputed}
	err := tx.QueryRow(ctx, `INSERT INTO third_party_payment_orders(order_id,payment_provider,external_order_id,capture_id,
		payer_country,currency,gross_amount,status,raw_payload,account_id,object_type,presentment_currency,presentment_amount_minor)
		VALUES($1,'stripe',$2,$3,NULLIF($4,''),'USD',$5::bigint::numeric/100,'VERIFIED',$6,$7,'charge',NULLIF($8,''),$9)
		ON CONFLICT(account_id,object_type,capture_id) WHERE account_id IS NOT NULL
		DO UPDATE SET raw_payload=EXCLUDED.raw_payload,
		    presentment_currency=COALESCE(EXCLUDED.presentment_currency,third_party_payment_orders.presentment_currency),
		    presentment_amount_minor=COALESCE(EXCLUDED.presentment_amount_minor,third_party_payment_orders.presentment_amount_minor)
		WHERE third_party_payment_orders.order_id=EXCLUDED.order_id
		  AND third_party_payment_orders.gross_amount=EXCLUDED.gross_amount RETURNING id`,
		orderID, proof.ID, receipt.ID, proof.Country, receipt.AmountMinor, receipt.Evidence, v.AccountID,
		receipt.PresentmentCurrency, receipt.PresentmentAmountMinor).Scan(&entry.receiptID)
	if err != nil {
		return entry, err
	}
	cols := extractSettlementColumns(movement)
	err = tx.QueryRow(ctx, `WITH allocation AS (
		INSERT INTO payment_stripe_invoice_allocations(account_id,invoice_ref,allocation_ref,receipt_id,amount_minor,
			presentment_currency,presentment_amount_minor,settlement_movement_ref,settlement_currency,
			settlement_gross_minor,settlement_fee_minor,settlement_withheld_tax_minor,settlement_net_minor,settlement_exchange_rate)
		VALUES($1,$2,$3,$4,$5,NULLIF($7,''),$8,NULLIF($9,''),NULLIF($10,''),$11,$12,$13,$14,NULLIF($15,''))
		ON CONFLICT(account_id,allocation_ref) DO UPDATE SET
			presentment_currency=COALESCE(EXCLUDED.presentment_currency,payment_stripe_invoice_allocations.presentment_currency),
			presentment_amount_minor=COALESCE(EXCLUDED.presentment_amount_minor,payment_stripe_invoice_allocations.presentment_amount_minor),
			settlement_movement_ref=COALESCE(EXCLUDED.settlement_movement_ref,payment_stripe_invoice_allocations.settlement_movement_ref),
			settlement_currency=COALESCE(EXCLUDED.settlement_currency,payment_stripe_invoice_allocations.settlement_currency),
			settlement_gross_minor=COALESCE(EXCLUDED.settlement_gross_minor,payment_stripe_invoice_allocations.settlement_gross_minor),
			settlement_fee_minor=COALESCE(EXCLUDED.settlement_fee_minor,payment_stripe_invoice_allocations.settlement_fee_minor),
			settlement_withheld_tax_minor=COALESCE(EXCLUDED.settlement_withheld_tax_minor,payment_stripe_invoice_allocations.settlement_withheld_tax_minor),
			settlement_net_minor=COALESCE(EXCLUDED.settlement_net_minor,payment_stripe_invoice_allocations.settlement_net_minor),
			settlement_exchange_rate=COALESCE(EXCLUDED.settlement_exchange_rate,payment_stripe_invoice_allocations.settlement_exchange_rate)
		WHERE payment_stripe_invoice_allocations.invoice_ref=EXCLUDED.invoice_ref
		  AND payment_stripe_invoice_allocations.receipt_id=EXCLUDED.receipt_id
		  AND payment_stripe_invoice_allocations.amount_minor=EXCLUDED.amount_minor
		RETURNING receipt_id
	)
	SELECT GREATEST(0,$5::bigint-GREATEST($6::bigint,COALESCE((SELECT sum(amount_minor) FROM payment_stripe_refund_items WHERE receipt_id=$4),0))
		-COALESCE((SELECT confirmed_loss_minor FROM payment_principal_loss_by_receipt WHERE receipt_id=$4),0))::bigint FROM allocation`,
		v.AccountID, proof.ID, allocation.ID, entry.receiptID, allocation.AmountMinor, receipt.RefundedMinor,
		receipt.PresentmentCurrency, receipt.PresentmentAmountMinor,
		cols.MovementRef, cols.Currency, cols.GrossMinor, cols.FeeMinor, cols.WithheldTax, cols.NetMinor, cols.ExchangeRate).Scan(&entry.netMinor)
	if err == nil && receipt.RiskReview != nil {
		err = aftercare.RecordTx(ctx, tx, *receipt.RiskReview)
	}
	return entry, err
}

func stripeVIPAdmissionNote(ctx context.Context, tx pgx.Tx, v stripeVIPIntent, proof payment.StripeVIPInvoice,
	orderID int64, funding []stripeVIPFunding, note string) (string, error) {
	if note != "" || proof.Status == "draft" {
		return note, nil
	}
	if proof.Status != "paid" || proof.RemainingMinor != 0 {
		if proof.Status == "void" || proof.Status == "uncollectible" {
			return "Stripe closed the invoice without confirmed payment", nil
		}
		return "", nil
	}
	if (proof.BillingReason != "subscription_create" && proof.BillingReason != "subscription_cycle") ||
		proof.Currency != "USD" || proof.DueMinor <= 0 || !proof.EndsAt.After(proof.StartsAt) || proof.StartsAt.IsZero() {
		return "Stripe VIP invoice has no eligible USD service period", nil
	}
	var total int64
	for _, entry := range funding {
		if entry.netMinor != entry.allocatedMinor {
			return "Stripe VIP invoice contains refunded or reversed funds", nil
		}
		if entry.netMinor > proof.DueMinor-total {
			return "Stripe VIP allocations exceed the verified invoice amount", nil
		}
		total += entry.netMinor
	}
	if total != proof.DueMinor {
		return "Stripe VIP invoice does not have enough verified captured funds", nil
	}
	if stripeVIPPrincipal(v, proof) <= 0 {
		return "Stripe VIP invoice has no verified amount excluding tax", nil
	}
	var overlapping bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM payment_stripe_invoice_periods WHERE intent_id=$1
		AND admitted AND service_start<$3 AND service_end>$2)`, v.ID, proof.StartsAt, proof.EndsAt).Scan(&overlapping)
	if err != nil {
		return "", err
	}
	if overlapping {
		return "Stripe VIP invoice overlaps an already admitted period", nil
	}
	var parentFulfilledElsewhere bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM payment_vip_checkout_intents
		WHERE stripe_intent_id=$1::uuid AND status='Completed'
		  AND COALESCE(subscription_id,'')<>'' AND subscription_id<>$2)`, v.ID, proof.Subscription.ID).Scan(&parentFulfilledElsewhere)
	if err != nil {
		return "", err
	}
	if parentFulfilledElsewhere {
		return "Duplicate payment for an already completed VIP checkout intent; manual review or refund required", nil
	}
	if proof.BillingReason == "subscription_create" {
		var overlappingOtherVIP bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(
			SELECT 1 FROM recharge_orders o
			WHERE o.user_id=$1
			  AND o.order_type='subscription'
			  AND COALESCE(o.subscription_id,'')<>$2
			  AND o.checkout_environment=$3
			  AND o.fulfilled_at IS NOT NULL
			  AND o.status IN ('Success','Paid','RefundPending')
			  AND o.paid_period_end IS NOT NULL
			  AND COALESCE(o.paid_period_start, o.paid_at, o.created_at) < $5
			  AND o.paid_period_end > $4
		)`, v.UserID, proof.Subscription.ID, conversion.Environment(), proof.StartsAt, proof.EndsAt).Scan(&overlappingOtherVIP)
		if err != nil {
			return "", err
		}
		if overlappingOtherVIP {
			return "Duplicate payment overlapping an already fulfilled VIP subscription; manual review or refund required", nil
		}
	}
	var conflictingVoid bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM payment_stripe_invoice_periods WHERE intent_id=$1
		AND invoice_ref<>$2 AND provider_status='void' AND service_start=$3 AND service_end=$4
		AND NOT ($6 AND invoice_ref=$5))`, v.ID, proof.ID, proof.StartsAt, proof.EndsAt, proof.RevisesInvoiceID, proof.RevisionVerified).Scan(&conflictingVoid)
	if err != nil {
		return "", err
	}
	if conflictingVoid || (proof.RevisesInvoiceID != "" && !proof.RevisionVerified) {
		return "Stripe VIP invoice revision needs verified original billing-period evidence", nil
	}
	var priorCountry string
	err = tx.QueryRow(ctx, `SELECT COALESCE((SELECT p.country FROM payment_stripe_invoice_periods p
		WHERE p.intent_id=$1 AND p.admitted AND p.service_start<$2
		ORDER BY p.service_start DESC LIMIT 1),'')`, v.ID, proof.StartsAt).Scan(&priorCountry)
	if err != nil {
		return "", err
	}
	if priorCountry == "" {
		priorCountry = v.Offer.ExpectedCountry
	}
	references := make([]string, 0, len(proof.Allocations))
	for _, item := range proof.Allocations {
		references = append(references, item.ID+":"+item.Receipt.ID)
	}
	slices.Sort(references)
	evidence := stripeMarketEvidence{FundingReferences: references, PeriodStart: &proof.StartsAt, PeriodEnd: &proof.EndsAt, Reference: proof.ID, SalesMode: v.Offer.SalesMode, Country: proof.Country, ExpectedCountry: priorCountry,
		TaxAccount: proof.TaxLiabilityAccount, TaxLiabilityType: proof.TaxLiabilityType, AutomaticTaxStatus: proof.AutomaticTaxStatus,
		ExpectedTaxAccount: v.Offer.TaxLiabilityAccount, CustomerTaxStatus: proof.CustomerTaxStatus,
		TaxVerified: proof.TaxVerified, AmountMinor: proof.DueMinor}
	if reason, err := stripeMarketReview(ctx, tx, orderID, evidence, stripeMarketNote(v.Offer.SingleCheckoutOffer, evidence)); err != nil || reason != "" {
		return reason, err
	}

	var hasDispute, deferred bool
	for _, entry := range funding {
		dispute, err := unresolvedStripeChargeDisputeTx(ctx, tx, entry.receiptID, entry.disputed)
		if err != nil {
			return "", err
		}
		hasDispute = hasDispute || dispute
		hold, err := aftercare.EvaluateFulfillment(ctx, tx, orderID, entry.receiptID)
		if err != nil {
			return "", err
		}
		deferred = deferred || hold.Deferred
	}
	if hasDispute {
		return "Stripe VIP invoice has an unresolved or unverified dispute", nil
	}
	if deferred {
		return "Stripe VIP invoice is awaiting risk or refund review", nil
	}
	return "", nil
}

func updateStripeVIPPeriod(ctx context.Context, tx pgx.Tx, v stripeVIPIntent, proof payment.StripeVIPInvoice,
	orderID int64, note string, paidAt time.Time, admit bool, settlementState string, retryUntil, nextCheckAt, verifiedAt *time.Time) error {
	var start, end, observedPayment *time.Time
	if !proof.StartsAt.IsZero() && proof.EndsAt.After(proof.StartsAt) {
		start, end = &proof.StartsAt, &proof.EndsAt
	}
	if !paidAt.IsZero() {
		observedPayment = &paidAt
	}
	_, err := tx.Exec(ctx, `UPDATE payment_stripe_invoice_periods SET service_start=$3,service_end=$4,billed_minor=$5,billing_reason=$10,
		country=NULLIF($6,''),observed_tax_liability_account=NULLIF($7,''),observed_customer_tax_status=NULLIF($11,''),review_reason=NULLIF($8,''),admitted=$9,
		provider_status=$12,revises_invoice_ref=NULLIF($13,''),revision_verified=$14,
		settlement_state=$15,settlement_retry_until=COALESCE($16,settlement_retry_until),
		settlement_next_check_at=$17,settlement_verified_at=COALESCE($18,settlement_verified_at),updated_at=now()
		WHERE account_id=$1 AND invoice_ref=$2`, v.AccountID, proof.ID, start, end, proof.DueMinor, proof.Country, proof.TaxLiabilityAccount,
		note, admit, proof.BillingReason, proof.CustomerTaxStatus, proof.Status, proof.RevisesInvoiceID, proof.RevisionVerified,
		settlementState, retryUntil, nextCheckAt, verifiedAt)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE recharge_orders SET amount_cents=$2, paid_at=COALESCE(GREATEST(paid_at,$3::timestamptz),paid_at),
		status=CASE WHEN $4 THEN 'Success' WHEN paid_at IS NOT NULL OR $3::timestamptz IS NOT NULL THEN 'Paid' ELSE status END,
		paid_period_start=CASE WHEN $4 THEN $5::timestamptz END,paid_period_end=CASE WHEN $4 THEN $6::timestamptz END,
		reconciliation_note=NULLIF($7,''),updated_at=now() WHERE id=$1 AND status<>'Refunded'`,
		orderID, proof.DueMinor, observedPayment, admit, start, end, note)
	return err
}

func stripeVIPPrincipal(v stripeVIPIntent, proof payment.StripeVIPInvoice) int64 {
	if v.Offer.SalesMode == payment.StripeSaleDirect {
		return proof.DueMinor
	}
	if proof.PrincipalMinor <= 0 || proof.PrincipalMinor > proof.DueMinor {
		return 0
	}
	return proof.PrincipalMinor
}

func recordStripeVIPFunding(ctx context.Context, tx pgx.Tx, v stripeVIPIntent, proof payment.StripeVIPInvoice, orderID int64,
	funding []stripeVIPFunding, backfill bool) error {
	principal := stripeVIPPrincipal(v, proof)
	if backfill {
		receiptIDs := make([]int64, 0, len(funding))
		var total int64
		for _, entry := range funding {
			if entry.allocatedMinor > proof.DueMinor-total {
				return nil
			}
			total += entry.allocatedMinor
			receiptIDs = append(receiptIDs, entry.receiptID)
		}
		if total != proof.DueMinor {
			return nil
		}
		var matches bool
		err := tx.QueryRow(ctx, `SELECT p.billed_minor=$2 AND count(f.receipt_id)=cardinality($3::bigint[])
			AND bool_and(f.receipt_id=ANY($3)) AND bool_or(f.principal_minor IS NULL)
			AND cardinality($3)=(SELECT count(*) FROM payment_stripe_invoice_allocations a
				WHERE a.account_id=p.account_id AND a.invoice_ref=p.invoice_ref)
			FROM payment_stripe_invoice_periods p JOIN payment_fulfillment_funding f ON f.order_id=p.order_id
			WHERE p.order_id=$1 GROUP BY p.billed_minor,p.account_id,p.invoice_ref`, orderID, proof.DueMinor, receiptIDs).Scan(&matches)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if !matches {
			return nil
		}
	}
	// Allocate the exact pre-tax total across captured funds with stable
	// rounding. The verified invoice total fits in a signed 32-bit integer.
	slices.SortFunc(funding, func(a, b stripeVIPFunding) int { return cmp.Compare(a.receiptID, b.receiptID) })
	var cumulative, allocated int64
	for _, entry := range funding {
		cumulative += entry.allocatedMinor
		through := cumulative * principal / proof.DueMinor
		if backfill {
			_, err := tx.Exec(ctx, `UPDATE payment_fulfillment_funding SET principal_minor=$3 WHERE order_id=$1 AND receipt_id=$2 AND principal_minor IS NULL`,
				orderID, entry.receiptID, through-allocated)
			if err != nil {
				return err
			}
		} else if err := recordFundingTx(ctx, tx, orderID, entry.receiptID, through-allocated, "USD"); err != nil {
			return err
		}
		allocated = through
	}
	return nil
}

func grantStripeVIPPeriod(ctx context.Context, tx pgx.Tx, proof payment.StripeVIPInvoice, orderID int64, paidAt time.Time) error {
	if err := aftercare.FinishFulfillment(ctx, tx, orderID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE user_subscriptions SET
		last_payment_time=GREATEST(last_payment_time,$2),
		current_period_start=CASE WHEN current_period_end IS NULL OR current_period_end<$4 THEN $3 ELSE current_period_start END,
		current_period_end=GREATEST(current_period_end,$4),
		next_billing_time=CASE WHEN status='ACTIVE' THEN GREATEST(next_billing_time,$4) ELSE NULL END,updated_at=now()
		WHERE subscription_id=$1 AND payment_method='stripe'`, proof.Subscription.ID, paidAt, proof.StartsAt, proof.EndsAt)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE recharge_orders SET fulfilled_at=clock_timestamp() WHERE id=$1 AND status='Success' AND fulfilled_at IS NULL`, orderID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("Stripe VIP invoice %s cannot fulfill order %d from its current state", proof.ID, orderID)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE payment_vip_checkout_intents
		SET status='Completed',switch_to_provider=NULL,subscription_id=COALESCE(NULLIF(subscription_id,''),$1),updated_at=now()
		WHERE status='Pending'
		  AND stripe_intent_id=(SELECT checkout_intent_id FROM user_subscriptions WHERE subscription_id=$1 AND payment_method='stripe' LIMIT 1)`,
		proof.Subscription.ID); err != nil {
		return err
	}
	first := proof.BillingReason == "subscription_create"
	subscribe := false
	if first {
		tag, err := tx.Exec(ctx, `UPDATE user_subscriptions SET capi_subscribe_state='claimed',capi_subscribe_order_id=$2
			WHERE subscription_id=$1 AND capi_subscribe_state='eligible'`, proof.Subscription.ID, orderID)
		if err != nil {
			return err
		}
		subscribe = tag.RowsAffected() == 1
	}
	kind := "subscription_renewal"
	if first {
		kind = "subscription_initial"
	}
	return conversion.RecordSubscriptionPaymentTx(ctx, tx, orderID, paidAt, "stripe_invoice_charge", conversion.Environment(), &kind, subscribe, !first)
}
