package stripe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	stripego "github.com/stripe/stripe-go/v87"
	"reader-backend/internal/payment"
)

const (
	EventCheckoutSessionCompleted             = stripego.EventTypeCheckoutSessionCompleted
	EventCheckoutSessionAsyncPaymentSucceeded = stripego.EventTypeCheckoutSessionAsyncPaymentSucceeded
	EventCheckoutSessionAsyncPaymentFailed    = stripego.EventTypeCheckoutSessionAsyncPaymentFailed
	EventCheckoutSessionExpired               = stripego.EventTypeCheckoutSessionExpired

	EventInvoicePaymentPaid           = stripego.EventType("invoice_payment.paid")
	EventInvoicePaid                  = stripego.EventTypeInvoicePaid
	EventInvoicePaymentSucceeded      = stripego.EventTypeInvoicePaymentSucceeded
	EventInvoicePaymentFailed         = stripego.EventTypeInvoicePaymentFailed
	EventInvoicePaymentActionRequired = stripego.EventTypeInvoicePaymentActionRequired
	EventInvoiceFinalized             = stripego.EventTypeInvoiceFinalized
	EventInvoiceVoided                = stripego.EventTypeInvoiceVoided
	EventInvoiceMarkedUncollectible   = stripego.EventTypeInvoiceMarkedUncollectible

	EventCustomerSubscriptionCreated = stripego.EventTypeCustomerSubscriptionCreated
	EventCustomerSubscriptionUpdated = stripego.EventTypeCustomerSubscriptionUpdated
	EventCustomerSubscriptionDeleted = stripego.EventTypeCustomerSubscriptionDeleted

	EventRefundCreated = stripego.EventTypeRefundCreated
	EventRefundUpdated = stripego.EventTypeRefundUpdated
	EventRefundFailed  = stripego.EventTypeRefundFailed

	EventChargeSucceeded            = stripego.EventTypeChargeSucceeded
	EventChargeFailed               = stripego.EventTypeChargeFailed
	EventPaymentIntentPaymentFailed = stripego.EventTypePaymentIntentPaymentFailed
	EventReviewOpened               = stripego.EventTypeReviewOpened
	EventReviewClosed               = stripego.EventTypeReviewClosed

	EventPayoutPaid                    = stripego.EventTypePayoutPaid
	EventPayoutFailed                  = stripego.EventTypePayoutFailed
	EventPayoutCanceled                = stripego.EventTypePayoutCanceled
	EventPayoutReconciliationCompleted = stripego.EventType("payout.reconciliation_completed")
	EventReportingReportRunSucceeded   = stripego.EventTypeReportingReportRunSucceeded

	eventDisputePrefix  = "charge.dispute."
	eventEFWPrefix      = "radar.early_fraud_warning."
	reportTypePayoutPfx = "payout_reconciliation.itemized."
	reportTypeActPfx    = "balance_change_from_activity.itemized."
)

var (
	ErrEventScopeMismatch    = errors.New("Stripe event scope mismatch")
	ErrEventResourceMismatch = errors.New("Stripe event resource identity or environment mismatch")
)

// IsAftercareEventType reports whether the Stripe event type belongs to the
// risk, dispute, review, or early fraud warning aftercare pipeline.
func IsAftercareEventType(kind stripego.EventType) bool {
	return stripeAftercareNotice(kind)
}

// IsHandledEventType reports whether the Stripe event type participates in
// StarNovel's payment, subscription, aftercare, or settlement workflows.
func IsHandledEventType(kind stripego.EventType) bool {
	if IsAftercareEventType(kind) {
		return true
	}
	switch kind {
	case EventCheckoutSessionCompleted, EventCheckoutSessionAsyncPaymentSucceeded,
		EventCheckoutSessionAsyncPaymentFailed, EventCheckoutSessionExpired,
		EventInvoicePaymentPaid, EventInvoicePaid, EventInvoicePaymentSucceeded,
		EventInvoicePaymentFailed, EventInvoicePaymentActionRequired,
		EventInvoiceFinalized, EventInvoiceVoided, EventInvoiceMarkedUncollectible,
		EventCustomerSubscriptionCreated, EventCustomerSubscriptionUpdated, EventCustomerSubscriptionDeleted,
		EventRefundCreated, EventRefundUpdated, EventRefundFailed,
		EventPayoutPaid, EventPayoutFailed, EventPayoutCanceled, EventPayoutReconciliationCompleted,
		EventReportingReportRunSucceeded:
		return true
	default:
		return false
	}
}

func isItemizedSettlementReportType(reportType string) bool {
	return strings.HasPrefix(reportType, reportTypePayoutPfx) || strings.HasPrefix(reportType, reportTypeActPfx)
}

// ResolveWebhookNotice re-fetches the authoritative current Stripe resource
// identified by event (regardless of the event's original API version), verifies
// its account/environment/identity correlation, and returns the domain notice
// for downstream business handlers.
//
// Transient API or network failures are returned unwrapped so the inbox worker
// retries them. Permanent identity or scope mismatches wrap ErrEventScopeMismatch
// or ErrEventResourceMismatch for diagnosis. Unrelated or non-applicable events
// return (nil, nil).
func (c *Client) ResolveWebhookNotice(ctx context.Context, event Event) (*payment.SingleCheckoutNotice, error) {
	if event.Scope != c.scope {
		return nil, fmt.Errorf("%w: event=%s/%s client=%s/%s",
			ErrEventScopeMismatch, event.Scope.AccountID, event.Scope.Environment, c.scope.AccountID, c.scope.Environment)
	}
	if event.IncompleteSnapshot {
		return nil, errIncompleteSnapshot
	}
	notice := payment.SingleCheckoutNotice{
		EventID:   event.ID,
		EventType: string(event.Type),
		Account:   ledgerAccount(event.Scope),
	}
	expectLive := c.scope.Environment == Live

	switch event.Type {
	case EventCheckoutSessionCompleted, EventCheckoutSessionAsyncPaymentSucceeded,
		EventCheckoutSessionAsyncPaymentFailed, EventCheckoutSessionExpired:
		if !validID(event.ObjectID, "cs_") {
			return nil, fmt.Errorf("%w: invalid checkout session ID %q", ErrEventResourceMismatch, event.ObjectID)
		}
		session, err := c.GetCheckout(ctx, event.ObjectID)
		if err != nil {
			return nil, err
		}
		if session.ID != event.ObjectID || session.Livemode != expectLive {
			return nil, fmt.Errorf("%w: checkout session %s", ErrEventResourceMismatch, event.ObjectID)
		}
		attempt := session.Metadata[singleAttemptMetadata]
		if attempt == "" {
			return nil, nil
		}
		if session.ClientReferenceID != attempt {
			return nil, fmt.Errorf("%w: checkout session %s reference mismatch", ErrEventResourceMismatch, session.ID)
		}
		switch session.Mode {
		case stripego.CheckoutSessionModeSubscription:
			notice.VIPIntentID = attempt
			notice.SessionID = session.ID
			return &notice, nil
		case stripego.CheckoutSessionModePayment:
			notice.AttemptID = attempt
			notice.SessionID = session.ID
			return &notice, nil
		default:
			return nil, nil
		}

	case EventInvoicePaymentPaid:
		if !validID(event.ObjectID, "inpay_") {
			return nil, fmt.Errorf("%w: invalid invoice payment ID %q", ErrEventResourceMismatch, event.ObjectID)
		}
		allocation, err := c.GetInvoicePayment(ctx, event.ObjectID)
		if err != nil {
			return nil, err
		}
		if allocation.ID != event.ObjectID || allocation.Livemode != expectLive ||
			allocation.Invoice == nil || !validID(allocation.Invoice.ID, "in_") {
			return nil, fmt.Errorf("%w: invoice payment %s", ErrEventResourceMismatch, event.ObjectID)
		}
		notice.InvoiceID = allocation.Invoice.ID
		return &notice, nil

	case EventInvoicePaid, EventInvoicePaymentSucceeded, EventInvoicePaymentFailed,
		EventInvoicePaymentActionRequired, EventInvoiceFinalized, EventInvoiceVoided, EventInvoiceMarkedUncollectible:
		if !validID(event.ObjectID, "in_") {
			return nil, fmt.Errorf("%w: invalid invoice ID %q", ErrEventResourceMismatch, event.ObjectID)
		}
		invoice, err := c.GetInvoice(ctx, event.ObjectID)
		if err != nil {
			return nil, err
		}
		if invoice.ID != event.ObjectID || invoice.Livemode != expectLive {
			return nil, fmt.Errorf("%w: invoice %s", ErrEventResourceMismatch, event.ObjectID)
		}
		notice.InvoiceID = invoice.ID
		return &notice, nil

	case EventCustomerSubscriptionCreated, EventCustomerSubscriptionUpdated, EventCustomerSubscriptionDeleted:
		if !validID(event.ObjectID, "sub_") {
			return nil, fmt.Errorf("%w: invalid subscription ID %q", ErrEventResourceMismatch, event.ObjectID)
		}
		sub, err := c.GetSubscription(ctx, event.ObjectID)
		if err != nil {
			return nil, err
		}
		if sub.ID != event.ObjectID || sub.Livemode != expectLive {
			return nil, fmt.Errorf("%w: subscription %s", ErrEventResourceMismatch, event.ObjectID)
		}
		notice.SubscriptionID = sub.ID
		return &notice, nil

	case EventRefundCreated, EventRefundUpdated, EventRefundFailed:
		if !validID(event.ObjectID, "re_") && !validID(event.ObjectID, "pyr_") {
			return nil, fmt.Errorf("%w: invalid refund ID %q", ErrEventResourceMismatch, event.ObjectID)
		}
		refund, err := c.GetRefund(ctx, event.ObjectID)
		if err != nil {
			return nil, err
		}
		if refund.ID != event.ObjectID {
			return nil, fmt.Errorf("%w: refund %s", ErrEventResourceMismatch, event.ObjectID)
		}
		notice.RefundID = refund.ID
		return &notice, nil

	case EventPayoutPaid, EventPayoutFailed, EventPayoutCanceled, EventPayoutReconciliationCompleted:
		if !validID(event.ObjectID, "po_") {
			return nil, fmt.Errorf("%w: invalid payout ID %q", ErrEventResourceMismatch, event.ObjectID)
		}
		payout, err := c.GetPayout(ctx, event.ObjectID)
		if err != nil {
			return nil, err
		}
		if payout.ID != event.ObjectID || payout.Livemode != expectLive {
			return nil, fmt.Errorf("%w: payout %s", ErrEventResourceMismatch, event.ObjectID)
		}
		notice.SettlementReference = payout.ID
		return &notice, nil

	case EventReportingReportRunSucceeded:
		if !validID(event.ObjectID, "frr_") {
			return nil, fmt.Errorf("%w: invalid report run ID %q", ErrEventResourceMismatch, event.ObjectID)
		}
		report, err := c.GetReportRun(ctx, event.ObjectID)
		if err != nil {
			return nil, err
		}
		if report.ID != event.ObjectID || report.Livemode != expectLive {
			return nil, fmt.Errorf("%w: report run %s", ErrEventResourceMismatch, event.ObjectID)
		}
		if report.Status != stripego.ReportingReportRunStatusSucceeded {
			return nil, fmt.Errorf("%w: report run %s status %s", ErrEventResourceMismatch, report.ID, report.Status)
		}
		if !isItemizedSettlementReportType(report.ReportType) {
			return nil, nil
		}
		notice.SettlementReference = report.ID
		return &notice, nil

	default:
		if !IsAftercareEventType(event.Type) {
			return nil, nil
		}
		raw, err := json.Marshal(event)
		if err != nil {
			return nil, err
		}
		notice.Aftercare = raw
		return &notice, nil
	}
}
