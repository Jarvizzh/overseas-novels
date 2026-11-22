package application

import (
	"context"
	"errors"

	"reader-backend/internal/db"
	"reader-backend/internal/payment"
	"reader-backend/internal/payment/aftercare"
	"reader-backend/internal/payment/paypal"
)

const refundDisputePageLimit = 5

var errRefundDispute = errors.New("active dispute requires financial review before refund")
var errRefundPrincipalLost = errors.New("original payment principal has already been reversed")

func (s *service) checkRefundDispute(ctx context.Context, job refundJob) error {
	var lookup, opened, fullLoss bool
	err := db.Pool(ctx).QueryRow(ctx, `SELECT COALESCE(p.dispute_lookup,false),EXISTS(
		SELECT 1 FROM payment_case_receipts r JOIN payment_cases c ON c.id=r.case_id
		WHERE r.receipt_id=t.id AND c.kind='dispute' AND c.is_open
	), COALESCE((SELECT loss.confirmed_loss_minor>=t.gross_amount*100 FROM payment_principal_loss_by_receipt loss WHERE loss.receipt_id=t.id),false)
	FROM third_party_payment_orders t LEFT JOIN payment_channel_accounts a ON a.id=t.account_id
	LEFT JOIN payment_aftercare_policies p ON p.account_id=a.id
	WHERE t.order_id=$1 AND t.capture_id=$2 AND t.payment_provider='paypal' AND t.id=$3`, job.orderID, job.request.ReceiptID, job.receiptID).Scan(&lookup, &opened, &fullLoss)
	if err != nil {
		return err
	}
	if fullLoss {
		return errRefundPrincipalLost
	}
	if opened && !lookup {
		return errRefundDispute
	}
	if !lookup {
		return nil
	}
	client, err := payment.Capability[interface {
		ListDisputes(context.Context, paypal.DisputeQuery) (paypal.DisputePage, error)
		GetDispute(context.Context, string) (paypal.Dispute, error)
	}](s.adapters, payment.ProviderPayPal)
	if err != nil {
		return err
	}
	query := paypal.DisputeQuery{TransactionID: job.request.ReceiptID, PageSize: 50}
	for range refundDisputePageLimit {
		list, err := client.ListDisputes(ctx, query)
		if err != nil {
			return err
		}
		for _, item := range list.Items {
			detail, err := client.GetDispute(ctx, item.ID)
			if err != nil {
				return err
			}
			if detail.Account != job.request.Account {
				return ErrCheckoutUnavailable
			}
			fact := detail.CaseFact("provider_api", detail.ID, job.lease)
			if err := aftercare.NewRepository(db.Pool(ctx)).Record(ctx, fact); err != nil {
				return err
			}
			if fact.Open {
				return errRefundDispute
			}
		}
		if list.Next == "" {
			err := db.Pool(ctx).QueryRow(ctx, `SELECT EXISTS(
				SELECT 1 FROM payment_case_receipts r JOIN payment_cases c ON c.id=r.case_id
				WHERE r.receipt_id=$1 AND c.kind='dispute' AND c.is_open
			)`, job.receiptID).Scan(&opened)
			if err != nil {
				return err
			}
			if opened {
				return errRefundDispute
			}
			return nil
		}
		query.Cursor = list.Next
	}
	return errors.New("PayPal dispute lookup did not complete")
}

func parkRefundBeforeSend(ctx context.Context, job refundJob, reason error) error {
	message := "Could not verify that this receipt can be refunded"
	if errors.Is(reason, errRefundDispute) {
		message = aftercare.RefundDisputeReviewNote
	} else if errors.Is(reason, errRefundPrincipalLost) {
		message = errRefundPrincipalLost.Error()
	}
	_, err := db.Pool(ctx).Exec(ctx, `UPDATE payment_refund_commands SET claimed_until=NULL,first_sent_at=NULL,
		status='review',result_note=$3,updated_at=now()
		WHERE id=$1 AND claimed_until=$2 AND status='confirming' AND provider_refund_id IS NULL`, job.id, job.lease, message)
	return err
}
