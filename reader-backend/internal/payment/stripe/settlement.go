package stripe

import (
	"context"
	"errors"
	"strings"
	"time"

	stripego "github.com/stripe/stripe-go/v87"
	"reader-backend/internal/payment"
)

// ReadPayoutSettlement reads the payout, transfer and full member list when
// Stripe has published the allocation. It performs no channel mutations.
func (c *Client) ReadPayoutSettlement(ctx context.Context, id string, receive func(payment.LedgerEvidence) error) (payment.PayoutEvidence, error) {
	observed := time.Now().UTC()
	raw, err := c.GetPayout(ctx, id)
	if err != nil {
		return payment.PayoutEvidence{}, err
	}
	if raw.ID != id || raw.Livemode != (c.scope.Environment == Live) || raw.Amount <= 0 ||
		raw.Type != stripego.PayoutTypeBank || raw.OriginalPayout != nil || raw.LastResponse == nil {
		return payment.PayoutEvidence{}, errors.New("unexpected Stripe bank payout identity or amount")
	}
	code := strings.ToUpper(string(raw.Currency))
	if _, err := stripeBalanceScale(code); err != nil {
		return payment.PayoutEvidence{}, err
	}
	result := payment.PayoutEvidence{
		Reference: id, Expected: payment.LedgerMoney{Currency: code, Minor: raw.Amount},
		Status: string(raw.Status), AllocationRequired: raw.Automatic && raw.Method == stripego.PayoutMethodStandard,
		ObservedAt: observed, Payload: raw.LastResponse.RawJSON,
	}
	const traceSupported = "supported"
	if raw.TraceID != nil && raw.TraceID.Status == traceSupported {
		result.BankReference = raw.TraceID.Value
	}
	if raw.ArrivalDate > 0 {
		result.ExpectedAt = time.Unix(raw.ArrivalDate, 0).UTC()
	}
	if raw.ReversedBy != nil && raw.ReversedBy.ID != "" {
		result.Status = "canceled"
	}
	for _, item := range []struct {
		Txn *stripego.BalanceTransaction
		Key *string
	}{{raw.BalanceTransaction, &result.TransferKey}, {raw.FailureBalanceTransaction, &result.ReversalKey}} {
		if item.Txn == nil {
			continue
		}
		*item.Key = item.Txn.ID
		movement, err := c.GetLedgerMovement(ctx, item.Txn.ID)
		if err != nil {
			return payment.PayoutEvidence{}, err
		}
		if movement.Currency != code {
			return payment.PayoutEvidence{}, errors.New("Stripe payout and transfer currencies differ")
		}
		if err := receive(movement); err != nil {
			return payment.PayoutEvidence{}, err
		}
	}
	if !result.AllocationRequired || raw.ReconciliationStatus != stripego.PayoutReconciliationStatusCompleted {
		return result, nil
	}
	query := BalanceTransactionQuery{PayoutID: id, PageOptions: PageOptions{Limit: 100}}
	for {
		page, err := c.ListBalanceTransactions(ctx, query)
		if err != nil {
			return payment.PayoutEvidence{}, err
		}
		for i := range page.Items {
			movement, err := c.ledgerMovement(&page.Items[i])
			if err != nil {
				return payment.PayoutEvidence{}, err
			}
			movement.References = append(movement.References, payment.LedgerReference{Type: "stripe_payout_allocation", ID: id})
			if err := receive(movement); err != nil {
				return payment.PayoutEvidence{}, err
			}
		}
		if !page.HasMore {
			result.AllocationComplete = true
			return result, nil
		}
		if len(page.Items) == 0 || page.Items[len(page.Items)-1].ID == query.StartingAfter {
			return payment.PayoutEvidence{}, errors.New("Stripe payout allocation cursor did not advance")
		}
		query.StartingAfter = page.Items[len(page.Items)-1].ID
	}
}
