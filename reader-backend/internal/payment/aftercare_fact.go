package payment

import (
	"encoding/json"
	"time"
)

type CaseKind string

const (
	CaseRiskSignal CaseKind = "risk_signal"
	CaseRiskReview CaseKind = "risk_review"
	CaseDispute    CaseKind = "dispute"
	CaseReversal   CaseKind = "reversal"
)

type CaseFact struct {
	Provider, ExternalID, OrderRef string
	Account                        WebhookAccount
	Kind                           CaseKind
	ReceiptRefs                    []string
	Status, Stage, Reason, Outcome string
	Open                           bool
	DueAt                          *time.Time
	AmountMinor                    *int64
	OriginalCurrency               string
	OriginalAmountMinor            *int64
	ObservedAt                     time.Time
	Source, SourceRef              string
	Evidence                       json.RawMessage
}

type CaseObservation struct {
	Fact      CaseFact
	Movements []LedgerEvidence
}
