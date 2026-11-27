package stripe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	stripego "github.com/stripe/stripe-go/v87"
	"reader-backend/internal/payment"
)

func (c *Client) VerifyAftercare(ctx context.Context, body []byte, signature string) (*payment.CaseObservation, error) {
	event, err := c.VerifyWebhook(body, signature)
	if err != nil {
		return nil, err
	}
	return c.AftercareEvent(ctx, event)
}

func (c *Client) AftercareEvent(ctx context.Context, event Event) (*payment.CaseObservation, error) {
	if event.Scope != c.scope {
		return nil, errors.New("Stripe event account mismatch")
	}
	source := "provider_api"
	if event.Source == EventFromWebhook {
		source = "webhook"
	}
	fact := payment.CaseFact{Provider: payment.ProviderStripe, Account: ledgerAccount(c.scope),
		ExternalID: event.ID, Stage: string(event.Type), ObservedAt: time.Unix(event.Created, 0),
		Source: source, SourceRef: event.ID, Kind: payment.CaseRiskSignal}
	observation := payment.CaseObservation{Fact: fact}
	switch {
	case strings.HasPrefix(string(event.Type), "charge.dispute."):
		return c.AftercareDispute(ctx, event.ObjectID, source, event.ID)
	case event.Type == "review.opened" || event.Type == "review.closed":
		observed := time.Now().UTC()
		review, err := c.GetReview(ctx, event.ObjectID)
		if err != nil {
			return nil, err
		}
		if review.ID != event.ObjectID || review.Livemode != (c.scope.Environment == Live) {
			return nil, errors.New("Stripe review identity mismatch")
		}
		observation.Fact.Kind = payment.CaseRiskReview
		observation.Fact.ExternalID = review.ID
		observation.Fact.ObservedAt = observed
		observation.Fact.Open = review.Open
		observation.Fact.Status = "closed"
		if review.Open {
			observation.Fact.Status = "open"
		}
		observation.Fact.Reason = string(review.Reason)
		observation.Fact.Outcome = string(review.ClosedReason)
		if review.Charge != nil {
			observation.Fact.ReceiptRefs = []string{review.Charge.ID}
		}
		return withEvidence(observation, struct {
			Opened, Closed string
		}{string(review.OpenedReason), string(review.ClosedReason)})
	case strings.HasPrefix(string(event.Type), "radar.early_fraud_warning."):
		warning, err := c.GetEarlyFraudWarning(ctx, event.ObjectID)
		if err != nil {
			return nil, err
		}
		if warning.ID != event.ObjectID || warning.Livemode != (c.scope.Environment == Live) {
			return nil, errors.New("Stripe warning identity mismatch")
		}
		observation.Fact.ExternalID = warning.ID
		observation.Fact.Status = "observed"
		observation.Fact.Reason = string(warning.FraudType)
		observation.Fact.ObservedAt = time.Now().UTC()
		chargeID := ""
		if warning.Charge != nil {
			chargeID = warning.Charge.ID
			observation.Fact.ReceiptRefs = []string{chargeID}
		}
		return withEvidence(observation, struct {
			Actionable bool   `json:"Actionable"`
			FraudType  string `json:"fraud_type,omitempty"`
			ChargeID   string `json:"charge_id,omitempty"`
		}{
			Actionable: warning.Actionable,
			FraudType:  string(warning.FraudType),
			ChargeID:   chargeID,
		})
	case event.Type == EventChargeFailed || event.Type == EventChargeSucceeded || event.Type == EventPaymentIntentPaymentFailed:
		signal, err := event.RiskSignal()
		if errors.Is(err, ErrEventVersion) && (event.ObjectType == "charge" || event.Type == EventChargeFailed || event.Type == EventChargeSucceeded) {
			risk, fetchErr := c.GetChargeRisk(ctx, event.ObjectID)
			if fetchErr != nil {
				return nil, fetchErr
			}
			signal.Charge = &risk
			err = nil
		}
		if errors.Is(err, ErrEventVersion) && (event.ObjectType == "payment_intent" || event.Type == EventPaymentIntentPaymentFailed) {
			intent, fetchErr := c.GetPaymentIntent(ctx, event.ObjectID)
			if fetchErr != nil {
				return nil, fetchErr
			}
			if intent.ID != event.ObjectID || intent.Livemode != (c.scope.Environment == Live) {
				return nil, fmt.Errorf("%w: payment intent %s", ErrEventResourceMismatch, event.ObjectID)
			}
			signal.Failure = PaymentIntentFailure(intent)
			err = nil
		}
		if err != nil {
			return nil, err
		}
		switch {
		case signal.Charge != nil:
			risk := signal.Charge
			observation.Fact.Status = string(risk.ChargeStatus)
			observation.Fact.Reason = risk.OutcomeReason
			if observation.Fact.Reason == "" {
				observation.Fact.Reason = risk.FailureCode
			}
			observation.Fact.ReceiptRefs = []string{risk.ChargeID}
			return withEvidence(observation, risk)
		case signal.Failure != nil:
			failure := signal.Failure
			observation.Fact.Status = "failed"
			observation.Fact.Reason = failure.DeclineCode
			if observation.Fact.Reason == "" {
				observation.Fact.Reason = failure.Code
			}
			if failure.ChargeID != "" {
				observation.Fact.ReceiptRefs = []string{failure.ChargeID}
			}
			return withEvidence(observation, failure)
		}
	}
	return nil, nil
}

func withEvidence(observation payment.CaseObservation, data any) (*payment.CaseObservation, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	observation.Fact.Evidence = raw
	return &observation, nil
}

func (c *Client) AftercareDispute(ctx context.Context, id, source, sourceRef string) (*payment.CaseObservation, error) {
	observed := time.Now().UTC()
	dispute, err := c.GetDispute(ctx, id)
	if err != nil {
		return nil, err
	}
	if dispute.ID != id || dispute.Livemode != (c.scope.Environment == Live) || dispute.Charge == nil || dispute.Charge.ID == "" {
		return nil, errors.New("Stripe dispute identity mismatch")
	}
	status := string(dispute.Status)
	category := ClassifyDisputeStatus(status)
	fact := payment.CaseFact{Provider: payment.ProviderStripe, Account: ledgerAccount(c.scope), Kind: payment.CaseDispute,
		ExternalID: id, ReceiptRefs: []string{dispute.Charge.ID}, Status: status, Stage: status, Reason: string(dispute.Reason),
		Source: source, SourceRef: sourceRef, ObservedAt: observed, Open: true}
	switch category {
	case DisputeCategoryClosedWon, DisputeCategoryClosedLost, DisputeCategoryClosedOther:
		fact.Open = false
		fact.Outcome = status
	}
	originalCurrency := strings.ToUpper(strings.TrimSpace(string(dispute.Currency)))
	if originalCurrency != "" && dispute.Amount >= 0 {
		origAmount := dispute.Amount
		fact.OriginalCurrency = originalCurrency
		fact.OriginalAmountMinor = &origAmount
	}
	if dispute.Currency == stripego.CurrencyUSD {
		usdAmount := dispute.Amount
		fact.AmountMinor = &usdAmount
	}
	if dispute.EvidenceDetails != nil && dispute.EvidenceDetails.DueBy > 0 {
		due := time.Unix(dispute.EvidenceDetails.DueBy, 0)
		fact.DueAt = &due
	}
	observation := payment.CaseObservation{Fact: fact}
	for _, txn := range dispute.BalanceTransactions {
		if txn == nil {
			continue
		}
		entry, err := c.GetLedgerMovement(ctx, txn.ID)
		if err != nil {
			return nil, fmt.Errorf("read dispute ledger: %w", err)
		}
		if entry.Currency != "USD" {
			continue
		}
		entry.References = append(entry.References, payment.LedgerReference{Type: "stripe_dispute", ID: id},
			payment.LedgerReference{Type: "stripe_charge", ID: dispute.Charge.ID})
		observation.Movements = append(observation.Movements, entry)
	}
	return withEvidence(observation, struct {
		Reason, NetworkReason string
		ChargeRefundable      bool
		DisputeCategory       string `json:"dispute_category,omitempty"`
		OriginalCurrency      string `json:"original_currency,omitempty"`
		OriginalAmountMinor   *int64 `json:"original_amount_minor,omitempty"`
	}{
		Reason:              string(dispute.Reason),
		NetworkReason:       dispute.NetworkReasonCode,
		ChargeRefundable:    dispute.IsChargeRefundable,
		DisputeCategory:     string(category),
		OriginalCurrency:    fact.OriginalCurrency,
		OriginalAmountMinor: fact.OriginalAmountMinor,
	})
}
