package stripe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	stripego "github.com/stripe/stripe-go/v87"
)

const (
	// Supported itemized Stripe reporting prefixes and default configured versions.
	ReportTypeBalanceChangeItemizedPrefix        = "balance_change_from_activity.itemized."
	ReportTypePayoutReconciliationItemizedPrefix = "payout_reconciliation.itemized."
	DefaultBalanceChangeReportType               = "balance_change_from_activity.itemized.6"
	DefaultPayoutReconciliationReportType        = "payout_reconciliation.itemized.7"

	// Canonical Stripe itemized report CSV column names.
	ReportColumnBalanceTransactionID   = "balance_transaction_id"
	ReportColumnSourceID               = "source_id"
	ReportColumnChargeID               = "charge_id"
	ReportColumnPaymentIntentID        = "payment_intent_id"
	ReportColumnInvoiceID              = "invoice_id"
	ReportColumnSubscriptionID         = "subscription_id"
	ReportColumnAutomaticPayoutID      = "automatic_payout_id"
	ReportColumnReportingCategory      = "reporting_category"
	ReportColumnCurrency               = "currency"
	ReportColumnGross                  = "gross"
	ReportColumnFee                    = "fee"
	ReportColumnNet                    = "net"
	ReportColumnWithheldTax            = "withheld_tax"
	ReportColumnFeeNetOfWithheldTax    = "fee_net_of_withheld_tax"
	ReportColumnCustomerFacingCurrency = "customer_facing_currency"
	ReportColumnCustomerFacingAmount   = "customer_facing_amount"
	ReportColumnExchangeRate           = "exchange_rate"
	ReportColumnIsManagedPayments      = "is_managed_payments"
)

// IsSupportedItemizedReportType reports whether reportType is a versioned
// balance_change_from_activity.itemized.* or payout_reconciliation.itemized.* report.
func IsSupportedItemizedReportType(reportType string) bool {
	var suffix string
	switch {
	case strings.HasPrefix(reportType, ReportTypeBalanceChangeItemizedPrefix):
		suffix = strings.TrimPrefix(reportType, ReportTypeBalanceChangeItemizedPrefix)
	case strings.HasPrefix(reportType, ReportTypePayoutReconciliationItemizedPrefix):
		suffix = strings.TrimPrefix(reportType, ReportTypePayoutReconciliationItemizedPrefix)
	default:
		return false
	}
	v, err := strconv.Atoi(suffix)
	return err == nil && v > 0 && strconv.Itoa(v) == suffix
}

// ValidateItemizedReportType validates that reportType is a supported versioned
// itemized settlement report.
func ValidateItemizedReportType(reportType string) error {
	if !IsSupportedItemizedReportType(reportType) {
		return fmt.Errorf("unsupported Stripe itemized report type %q", reportType)
	}
	return nil
}

// MoRReportColumns returns the explicit tax and net-fee columns required for
// Merchant of Record (Managed Payments) reconciliation reports.
func MoRReportColumns() []string {
	return []string{ReportColumnWithheldTax, ReportColumnFeeNetOfWithheldTax}
}

// ResolveReportColumns returns the deduplicated column list to pass to Stripe
// Report Runs. When includeMoR is true, withheld_tax and fee_net_of_withheld_tax
// are explicitly requested alongside the itemized columns.
func ResolveReportColumns(reportType string, requested []string, includeMoR bool) []string {
	if len(requested) == 0 && !includeMoR {
		return nil
	}
	base := requested
	if len(base) == 0 {
		base = []string{
			ReportColumnBalanceTransactionID,
			ReportColumnSourceID,
			ReportColumnChargeID,
			ReportColumnPaymentIntentID,
			ReportColumnInvoiceID,
			ReportColumnSubscriptionID,
		}
		if strings.HasPrefix(reportType, ReportTypePayoutReconciliationItemizedPrefix) {
			base = append(base, ReportColumnAutomaticPayoutID)
		}
		base = append(base,
			ReportColumnReportingCategory,
			ReportColumnCurrency,
			ReportColumnGross,
			ReportColumnFee,
			ReportColumnNet,
			ReportColumnCustomerFacingCurrency,
			ReportColumnCustomerFacingAmount,
			ReportColumnExchangeRate,
			ReportColumnIsManagedPayments,
		)
	}
	seen := make(map[string]struct{}, len(base)+2)
	resolved := make([]string, 0, len(base)+2)
	add := func(col string) {
		col = strings.TrimSpace(col)
		if col == "" {
			return
		}
		if _, ok := seen[col]; ok {
			return
		}
		seen[col] = struct{}{}
		resolved = append(resolved, col)
	}
	for _, col := range base {
		add(col)
	}
	if includeMoR {
		for _, col := range MoRReportColumns() {
			add(col)
		}
	}
	return resolved
}

func (c *Client) GetBalance(ctx context.Context) (*stripego.Balance, error) {
	return apiResult(c.api.V1Balance.Retrieve(ctx, nil))
}

// BalanceTransaction preserves the original exchange-rate precision and nullability.
type BalanceTransaction struct {
	*stripego.BalanceTransaction
	ExchangeRate *json.Number `json:"exchange_rate"`
	raw          json.RawMessage
}

type balanceTransactionFields struct {
	ExchangeRate *json.Number `json:"exchange_rate"`
	raw          json.RawMessage
}

func (f *balanceTransactionFields) UnmarshalJSON(data []byte) error {
	type fields balanceTransactionFields
	if err := json.Unmarshal(data, (*fields)(f)); err != nil {
		return err
	}
	f.raw = append(f.raw[:0], data...)
	return nil
}

func (c *Client) GetBalanceTransaction(ctx context.Context, id string) (*BalanceTransaction, error) {
	if !validID(id, "txn_") {
		return nil, errors.New("invalid Stripe BalanceTransaction ID")
	}
	txn, err := c.api.V1BalanceTransactions.Retrieve(ctx, id, nil)
	if err != nil {
		return nil, ClassifyPurposeError(c.scope, PurposeBalanceTransactions, classify(err))
	}
	var fields balanceTransactionFields
	if err := json.Unmarshal(txn.LastResponse.RawJSON, &fields); err != nil {
		return nil, classify(err)
	}
	return &BalanceTransaction{BalanceTransaction: txn, ExchangeRate: fields.ExchangeRate, raw: fields.raw}, nil
}

type BalanceTransactionQuery struct {
	// A time range is required when a payout is unspecified.
	Created *TimeRange
	// Payout filtering is supported for automatic payouts.
	PayoutID string
	PageOptions
}

func (c *Client) ListBalanceTransactions(ctx context.Context, query BalanceTransactionQuery) (Page[BalanceTransaction], error) {
	params, err := query.PageOptions.params()
	if err != nil {
		return Page[BalanceTransaction]{}, err
	}
	request := &stripego.BalanceTransactionListParams{ListParams: params}
	if query.Created != nil {
		request.CreatedRange, err = query.Created.params()
		if err != nil {
			return Page[BalanceTransaction]{}, err
		}
	}
	switch {
	case validID(query.PayoutID, "po_"):
		request.Payout = stripego.String(query.PayoutID)
	case query.PayoutID != "":
		return Page[BalanceTransaction]{}, errors.New("invalid Stripe Payout ID")
	case request.CreatedRange == nil:
		return Page[BalanceTransaction]{}, errors.New("Stripe balance transaction query requires a time range or payout")
	}
	list := c.api.V1BalanceTransactions.List(ctx, request)
	decoded, err := listPage(list)
	if err != nil {
		return Page[BalanceTransaction]{}, ClassifyPurposeError(c.scope, PurposeBalanceTransactions, err)
	}
	fields, err := rawPageFields[*stripego.BalanceTransaction, balanceTransactionFields](list)
	if err != nil {
		return Page[BalanceTransaction]{}, err
	}
	items := make([]BalanceTransaction, len(decoded.Items))
	for i, txn := range decoded.Items {
		if txn == nil {
			return Page[BalanceTransaction]{}, errors.New("unexpected Stripe balance transaction")
		}
		items[i] = BalanceTransaction{BalanceTransaction: txn, ExchangeRate: fields[i].ExchangeRate, raw: fields[i].raw}
	}
	return Page[BalanceTransaction]{Items: items, HasMore: decoded.HasMore}, nil
}

func (c *Client) GetPayout(ctx context.Context, id string) (*stripego.Payout, error) {
	if !validID(id, "po_") {
		return nil, errors.New("invalid Stripe Payout ID")
	}
	payout, err := apiResult(c.api.V1Payouts.Retrieve(ctx, id, nil))
	if err != nil {
		return nil, ClassifyPurposeError(c.scope, PurposePayout, err)
	}
	return payout, nil
}

func (c *Client) ListPayouts(ctx context.Context, created TimeRange, page PageOptions) (Page[*stripego.Payout], error) {
	params, err := page.params()
	if err != nil {
		return Page[*stripego.Payout]{}, err
	}
	window, err := created.params()
	if err != nil {
		return Page[*stripego.Payout]{}, err
	}
	res, err := listPage(c.api.V1Payouts.List(ctx, &stripego.PayoutListParams{ListParams: params, CreatedRange: window}))
	if err != nil {
		return Page[*stripego.Payout]{}, ClassifyPurposeError(c.scope, PurposePayout, err)
	}
	return res, nil
}

type ReportRequest struct {
	Type              string
	Interval          TimeRange
	Columns           []string
	IncludeMoRColumns bool
	Currency          string
	PayoutID          string
	IdempotencyKey    string
}

func (c *Client) CreateReportRun(ctx context.Context, req ReportRequest) (*stripego.ReportingReportRun, error) {
	if !validToken(strings.ReplaceAll(req.Type, ".", "_")) {
		return nil, errors.New("invalid Stripe report type")
	}
	columns := ResolveReportColumns(req.Type, req.Columns, req.IncludeMoRColumns)
	report := &stripego.ReportingReportRunCreateParametersParams{}
	if len(columns) > 0 {
		report.Columns = stripego.StringSlice(columns)
	}
	if req.PayoutID != "" {
		if !validID(req.PayoutID, "po_") {
			return nil, errors.New("invalid Stripe report Payout ID")
		}
		report.Payout = stripego.String(req.PayoutID)
	}
	if req.PayoutID == "" || !req.Interval.From.IsZero() || !req.Interval.Before.IsZero() {
		interval, err := req.Interval.params()
		if err != nil {
			return nil, err
		}
		report.IntervalStart = stripego.Int64(interval.GreaterThanOrEqual)
		report.IntervalEnd = stripego.Int64(interval.LesserThan)
	}
	if req.Currency != "" {
		report.Currency = stripego.String(req.Currency)
	}
	params, err := writeParams(req.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	run, err := apiResult(c.api.V1ReportingReportRuns.Create(ctx, &stripego.ReportingReportRunCreateParams{
		Params: params, ReportType: stripego.String(req.Type), Parameters: report,
	}))
	if err != nil {
		return nil, ClassifyPurposeError(c.scope, PurposeReportRun, err)
	}
	return run, nil
}

func (c *Client) GetReportType(ctx context.Context, id string) (*stripego.ReportingReportType, error) {
	if !validToken(strings.ReplaceAll(id, ".", "_")) {
		return nil, errors.New("invalid Stripe report type")
	}
	rt, err := apiResult(c.api.V1ReportingReportTypes.Retrieve(ctx, id, nil))
	if err != nil {
		return nil, ClassifyPurposeError(c.scope, PurposeReportRun, err)
	}
	return rt, nil
}

func (c *Client) ListReportTypes(ctx context.Context) ([]*stripego.ReportingReportType, error) {
	list, err := listPage(c.api.V1ReportingReportTypes.List(ctx, nil))
	if err != nil {
		return nil, ClassifyPurposeError(c.scope, PurposeReportRun, err)
	}
	return list.Items, nil
}

func (c *Client) GetReportRun(ctx context.Context, id string) (*stripego.ReportingReportRun, error) {
	if !validID(id, "frr_") {
		return nil, errors.New("invalid Stripe ReportRun ID")
	}
	run, err := apiResult(c.api.V1ReportingReportRuns.Retrieve(ctx, id, nil))
	if err != nil {
		return nil, ClassifyPurposeError(c.scope, PurposeReportRun, err)
	}
	return run, nil
}

func (c *Client) ListReportRuns(ctx context.Context, created TimeRange, page PageOptions) (Page[*stripego.ReportingReportRun], error) {
	params, err := page.params()
	if err != nil {
		return Page[*stripego.ReportingReportRun]{}, err
	}
	window, err := created.params()
	if err != nil {
		return Page[*stripego.ReportingReportRun]{}, err
	}
	res, err := listPage(c.api.V1ReportingReportRuns.List(ctx, &stripego.ReportingReportRunListParams{
		ListParams:   params,
		CreatedRange: window,
	}))
	if err != nil {
		return Page[*stripego.ReportingReportRun]{}, ClassifyPurposeError(c.scope, PurposeReportRun, err)
	}
	return res, nil
}
