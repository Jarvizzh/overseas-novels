package paypal

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"time"

	"reader-backend/internal/payment"
)

const (
	transactionsPath     = "/v1/reporting/transactions"
	balancesPath         = "/v1/reporting/balances"
	maxReportingWindow   = 31 * 24 * time.Hour
	maxReportingPageSize = 500
)

type TransactionQuery struct {
	From, Through time.Time
	TransactionID string
	Currency      string
	EventCode     string
	// IncludeNonBalanceRecords exposes reporting rows that don't affect the balance.
	IncludeNonBalanceRecords bool
	Page, PageSize           int
}

type Transaction struct {
	Raw              json.RawMessage `json:"-"`
	MerchantID       string          `json:"paypal_account_id"`
	ID               string          `json:"transaction_id"`
	ReferenceID      string          `json:"paypal_reference_id"`
	ReferenceType    string          `json:"paypal_reference_id_type"`
	EventCode        string          `json:"transaction_event_code"`
	Status           string          `json:"transaction_status"`
	InitiatedAt      time.Time       `json:"transaction_initiation_date"`
	UpdatedAt        time.Time       `json:"transaction_updated_date"`
	Gross            *Amount         `json:"transaction_amount"`
	Fee              *Amount         `json:"fee_amount"`
	Tax              *Amount         `json:"sales_tax_amount"`
	EndingBalance    *Amount         `json:"ending_balance"`
	AvailableBalance *Amount         `json:"available_balance"`
	InvoiceID        string          `json:"invoice_id"`
	CustomField      string          `json:"custom_field"`
}

type TransactionPage struct {
	Account      payment.WebhookAccount `json:"-"`
	Transactions []Transaction          `json:"-"`
	MerchantID   string                 `json:"account_number"`
	Page         int                    `json:"page"`
	TotalPages   int                    `json:"total_pages"`
	TotalItems   int                    `json:"total_items"`
	RefreshedAt  time.Time              `json:"last_refreshed_datetime"`
}

func (p *Client) ListTransactions(ctx context.Context, opts TransactionQuery) (TransactionPage, error) {
	if opts.From.IsZero() || !opts.Through.After(opts.From) || opts.Through.Sub(opts.From) > maxReportingWindow ||
		opts.Page < 0 || opts.PageSize < 0 || opts.PageSize > maxReportingPageSize {
		return TransactionPage{}, errors.New("invalid PayPal Transaction Search window or page")
	}
	query := url.Values{
		"start_date":                     {opts.From.UTC().Format(time.RFC3339Nano)},
		"end_date":                       {opts.Through.UTC().Format(time.RFC3339Nano)},
		"fields":                         {"transaction_info"},
		"balance_affecting_records_only": {"Y"},
	}
	if opts.IncludeNonBalanceRecords {
		query.Set("balance_affecting_records_only", "N")
	}
	for key, value := range map[string]string{
		"transaction_id":       opts.TransactionID,
		"transaction_currency": opts.Currency,
		"transaction_type":     opts.EventCode,
	} {
		if value != "" {
			query.Set(key, value)
		}
	}
	if opts.PageSize > 0 {
		query.Set("page_size", strconv.Itoa(opts.PageSize))
	}
	if opts.Page > 0 {
		query.Set("page", strconv.Itoa(opts.Page))
	}
	var response struct {
		TransactionPage
		Details []struct {
			Transaction json.RawMessage `json:"transaction_info"`
		} `json:"transaction_details"`
	}
	cfg, err := p.credentials(ctx)
	if err != nil {
		return TransactionPage{}, err
	}
	if err := p.send(ctx, cfg, "GET", endpoint(transactionsPath, query), "", nil, &response); err != nil {
		return TransactionPage{}, err
	}
	page := response.TransactionPage
	page.Account = cfg.account()
	if cfg.MerchantID != "" && page.MerchantID != cfg.MerchantID {
		return TransactionPage{}, errors.New("PayPal reported an unexpected merchant")
	}
	for _, detail := range response.Details {
		var item Transaction
		if err := json.Unmarshal(detail.Transaction, &item); err != nil {
			return TransactionPage{}, err
		}
		item.Raw = detail.Transaction
		if item.ID == "" || item.EventCode == "" {
			return TransactionPage{}, errors.New("incomplete PayPal reporting transaction")
		}
		page.Transactions = append(page.Transactions, item)
	}
	return page, nil
}

type Balance struct {
	Currency  string  `json:"currency"`
	Primary   bool    `json:"primary"`
	Total     *Amount `json:"total_balance"`
	Available *Amount `json:"available_balance"`
	Withheld  *Amount `json:"withheld_balance"`
}

type Balances struct {
	Account     payment.WebhookAccount `json:"-"`
	MerchantID  string                 `json:"account_id"`
	AsOf        time.Time              `json:"as_of_time"`
	RefreshedAt time.Time              `json:"last_refresh_time"`
	Items       []Balance              `json:"balances"`
}

func (p *Client) GetBalances(ctx context.Context, asOf time.Time, currency string) (Balances, error) {
	query := url.Values{}
	if !asOf.IsZero() {
		query.Set("as_of_time", asOf.UTC().Format(time.RFC3339Nano))
	}
	if currency != "" {
		query.Set("currency_code", currency)
	}
	var result Balances
	cfg, err := p.credentials(ctx)
	if err != nil {
		return Balances{}, err
	}
	if err := p.send(ctx, cfg, "GET", endpoint(balancesPath, query), "", nil, &result); err != nil {
		return Balances{}, err
	}
	if cfg.MerchantID != "" && result.MerchantID != cfg.MerchantID {
		return Balances{}, errors.New("PayPal reported an unexpected merchant")
	}
	result.Account = cfg.account()
	return result, nil
}
