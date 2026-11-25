package paypal

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"reader-backend/internal/payment"
)

func (d Dispute) CaseFact(source, sourceRef string, fetchedAt time.Time) payment.CaseFact {
	when := d.UpdatedAt
	if when.IsZero() {
		when = d.CreatedAt
	}
	if when.IsZero() {
		when = fetchedAt
	}
	var refs []string
	for _, transaction := range d.Transactions {
		if transaction.SellerTransactionID != "" {
			refs = append(refs, transaction.SellerTransactionID)
		}
	}
	fact := payment.CaseFact{Account: d.Account, Provider: payment.ProviderPayPal, ExternalID: d.ID,
		Kind: payment.CaseDispute, ReceiptRefs: refs, Status: d.Status, Stage: d.Stage, Reason: d.Reason,
		Open: d.Status != "RESOLVED", DueAt: d.SellerResponseDue, AmountMinor: usdCaseAmount(d.Amount),
		ObservedAt: when, Source: source, SourceRef: sourceRef, Evidence: d.Raw}
	if d.Outcome != nil {
		fact.Outcome = d.Outcome.Code
	}
	return fact
}

func usdCaseAmount(amount *Amount) *int64 {
	if amount == nil || !strings.EqualFold(amount.Currency, "USD") {
		return nil
	}
	value, err := decimal.NewFromString(amount.Value)
	if err != nil {
		return nil
	}
	minor, err := payment.ExactMinorUnits(value, 2)
	if err != nil || minor < 0 {
		return nil
	}
	return &minor
}

func WebhookCase(event *payment.WebhookEventResult) (*payment.CaseFact, error) {
	if event.RawPayload == "" {
		return nil, nil
	}
	switch event.ProviderEventType {
	case "CUSTOMER.DISPUTE.CREATED", "CUSTOMER.DISPUTE.UPDATED", "CUSTOMER.DISPUTE.RESOLVED",
		"PAYMENT.CAPTURE.REVERSED", "PAYMENT.SALE.REVERSED", "PAYMENT.CAPTURE.DENIED", "PAYMENT.CAPTURE.PENDING", "PAYMENT.CAPTURE.COMPLETED":
	default:
		return nil, nil
	}
	decoded, err := decodeEvent(event.Account, []byte(event.RawPayload))
	if err != nil {
		return nil, err
	}
	if decoded.ID != event.EventID || decoded.Type != event.ProviderEventType {
		return nil, errors.New("PayPal case event identity mismatch")
	}
	return decoded.CaseFact("webhook")
}

func (e Event) CaseFact(source string) (*payment.CaseFact, error) {
	switch e.Type {
	case "CUSTOMER.DISPUTE.CREATED", "CUSTOMER.DISPUTE.UPDATED", "CUSTOMER.DISPUTE.RESOLVED":
		dispute, err := decodeDispute(e.Account, e.Resource)
		if err != nil {
			return nil, err
		}
		fact := dispute.CaseFact(source, e.ID, e.CreatedAt)
		return &fact, nil
	case "PAYMENT.CAPTURE.REVERSED", "PAYMENT.SALE.REVERSED":
		reversal, err := e.Reversal()
		if err != nil {
			return nil, err
		}
		var refs []string
		if reversal.OriginalReceiptID != "" {
			refs = []string{reversal.OriginalReceiptID}
		}
		return &payment.CaseFact{Account: e.Account, Provider: payment.ProviderPayPal, ExternalID: reversal.ResourceID,
			Kind: payment.CaseReversal, ReceiptRefs: refs, Status: reversal.Status, Stage: e.Type, Open: true,
			AmountMinor: usdCaseAmount(reversal.Amount), ObservedAt: e.CreatedAt, Source: source, SourceRef: e.ID, Evidence: e.Raw}, nil
	case "PAYMENT.CAPTURE.DENIED", "PAYMENT.CAPTURE.PENDING", "PAYMENT.CAPTURE.COMPLETED":
		var capture CaptureEvidence
		if err := json.Unmarshal(e.Resource, &capture); err != nil {
			return nil, err
		}
		if capture.ID == "" {
			return nil, errors.New("missing PayPal risk capture ID")
		}
		if e.Type == "PAYMENT.CAPTURE.COMPLETED" && capture.StatusDetails == nil && capture.Processor == nil {
			return nil, nil
		}
		reason := ""
		if capture.StatusDetails != nil {
			reason = capture.StatusDetails.Reason
		}
		raw, err := json.Marshal(struct {
			Status    string                `json:"status"`
			Details   *CaptureStatusDetails `json:"status_details,omitempty"`
			Processor *ProcessorResponse    `json:"processor_response,omitempty"`
		}{capture.Status, capture.StatusDetails, capture.Processor})
		if err != nil {
			return nil, fmt.Errorf("marshal capture risk: %w", err)
		}
		kind, identifier, open := payment.CaseRiskSignal, e.ID, false
		if reason == "PENDING_REVIEW" {
			kind, identifier, open = payment.CaseRiskReview, capture.ID, true
		}
		return &payment.CaseFact{Account: e.Account, Provider: payment.ProviderPayPal, ExternalID: identifier, Kind: kind, Open: open,
			ReceiptRefs: []string{capture.ID}, Status: capture.Status, Stage: e.Type, Reason: reason, ObservedAt: e.CreatedAt,
			Source: source, SourceRef: e.ID, Evidence: raw}, nil
	}
	return nil, nil
}

// CaptureRisk extracts documented processor and card checks from the verified
// order response. PayPal wallet payments can omit these fields.
func CaptureRisk(capture *payment.OrderCapture, observedAt time.Time) (*payment.CaseFact, error) {
	var order OrderEvidence
	if err := json.Unmarshal([]byte(capture.RawPayload), &order); err != nil {
		return nil, err
	}
	if len(order.Units) != 1 {
		return nil, nil
	}
	for _, current := range order.Units[0].Payments.Captures {
		if current.ID != capture.CaptureID {
			continue
		}
		var auth *CardAuthentication
		if order.PaymentSource.Card != nil {
			auth = order.PaymentSource.Card.Authentication
		}
		if current.Processor == nil && current.StatusDetails == nil && auth == nil {
			return nil, nil
		}
		raw, err := json.Marshal(struct {
			Processor      *ProcessorResponse    `json:"processor_response,omitempty"`
			Details        *CaptureStatusDetails `json:"status_details,omitempty"`
			Authentication *CardAuthentication   `json:"authentication_result,omitempty"`
		}{current.Processor, current.StatusDetails, auth})
		if err != nil {
			return nil, err
		}
		reason := ""
		if current.StatusDetails != nil {
			reason = current.StatusDetails.Reason
		}
		return &payment.CaseFact{Provider: payment.ProviderPayPal, Account: capture.Account, Kind: payment.CaseRiskSignal,
			ExternalID: capture.CaptureID, ReceiptRefs: []string{capture.CaptureID}, Status: current.Status, Reason: reason, Stage: "orders_capture",
			Source: "provider_api", SourceRef: current.ID, ObservedAt: observedAt, Evidence: raw}, nil
	}
	return nil, nil
}

// FailureRisk keeps the provider issue codes with the payment attempt, without
// copying purchaser or payment instrument fields from an error body.
func FailureRisk(account payment.WebhookAccount, orderID string, failure error, when time.Time) (*payment.CaseFact, error) {
	var apiErr *APIError
	if !errors.As(failure, &apiErr) || len(apiErr.Issues) == 0 {
		return nil, nil
	}
	raw, err := json.Marshal(struct {
		HTTPStatus int
		Name       string
		Issues     []string
	}{apiErr.Status, apiErr.Name, apiErr.Issues})
	if err != nil {
		return nil, err
	}
	return &payment.CaseFact{Account: account, Provider: payment.ProviderPayPal,
		Kind: payment.CaseRiskSignal, ExternalID: orderID + "/capture_failure", OrderRef: orderID, Status: apiErr.Name,
		Stage: "orders_capture", Reason: apiErr.Issues[0], ObservedAt: when,
		Source: "provider_api", SourceRef: orderID, Evidence: raw}, nil
}
