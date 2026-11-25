package billing

import "time"

type ledgerAmounts map[string]string

type ledgerReference struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type payoutReference struct {
	ID          string `json:"id"`
	ExternalRef string `json:"external_ref"`
}

type ledgerMovement struct {
	ID             string            `json:"id"`
	MovementKey    string            `json:"movement_key"`
	Kind           string            `json:"kind"`
	Provider       string            `json:"provider"`
	Environment    string            `json:"environment"`
	AccountID      string            `json:"account_id"`
	ProviderCode   *string           `json:"provider_code"`
	OccurredAt     *time.Time        `json:"occurred_at"`
	ReviewRequired bool              `json:"review_required"`
	Amounts        ledgerAmounts     `json:"amounts"`
	References     []ledgerReference `json:"refs"`
	OrderIDs       []string          `json:"order_ids"`
	Payouts        []payoutReference `json:"payouts"`
}

type settlementPayout struct {
	payoutReference
	AccountID          string     `json:"account_id"`
	Provider           string     `json:"provider"`
	Environment        string     `json:"environment"`
	ChannelStatus      string     `json:"channel_status"`
	ExpectedMinor      string     `json:"expected_minor"`
	ExpectedAt         *time.Time `json:"expected_at"`
	BankReference      *string    `json:"bank_reference"`
	AllocationRequired bool       `json:"allocation_required"`
	AllocationComplete bool       `json:"allocation_complete"`
	RowCount           int64      `json:"row_count"`
	NetTotalMinor      *string    `json:"net_total_minor"`
	TransferMinor      *string    `json:"transfer_minor"`
	ChannelCheck       string     `json:"channel_check"`
	BankAmountMinor    *string    `json:"bank_amount_minor"`
	BookedAt           *time.Time `json:"booked_at"`
	BankTransactionID  *string    `json:"bank_transaction_id"`
	DifferenceMinor    *string    `json:"difference_minor"`
	Status             string     `json:"status"`
}

type settlementReportIssue struct {
	ImportID    string    `json:"import_id"`
	ExternalRef string    `json:"external_ref"`
	ImportedAt  time.Time `json:"imported_at"`
	Source      string    `json:"source"`
	Provider    string    `json:"provider"`
	Environment string    `json:"environment"`
	AccountID   string    `json:"account_id"`
	RowNumber   int       `json:"row_number"`
	SourceFile  string    `json:"source_file"`
	Reason      string    `json:"review_reason"`
	MovementID  *string   `json:"movement_id"`
}

type movementPage struct {
	Currency string           `json:"currency"`
	Total    int64            `json:"total"`
	Totals   ledgerAmounts    `json:"totals"`
	Items    []ledgerMovement `json:"items"`
}

type payoutPage struct {
	Currency string             `json:"currency"`
	Total    int64              `json:"total"`
	Items    []settlementPayout `json:"items"`
}

type reportIssuePage struct {
	Total int64                   `json:"total"`
	Items []settlementReportIssue `json:"items"`
}

type paymentActivity struct {
	RefundCount          int64 `json:"refund_count"`
	ChargebackDebitCount int64 `json:"chargeback_debit_count"`
}
