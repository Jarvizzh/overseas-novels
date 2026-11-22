package payment

import (
	"encoding/json"
	"time"
)

type PayoutEvidence struct {
	Reference          string
	Expected           LedgerMoney
	BankReference      string
	ExpectedAt         time.Time
	Status             string
	TransferKey        string
	ReversalKey        string
	AllocationRequired bool
	AllocationComplete bool
	ObservedAt         time.Time
	Payload            json.RawMessage
}
