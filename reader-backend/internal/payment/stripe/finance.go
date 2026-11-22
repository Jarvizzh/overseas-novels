package stripe

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	stripego "github.com/stripe/stripe-go/v87"
)

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
		return nil, classify(err)
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
		return Page[BalanceTransaction]{}, err
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
	return apiResult(c.api.V1Payouts.Retrieve(ctx, id, nil))
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
	return listPage(c.api.V1Payouts.List(ctx, &stripego.PayoutListParams{ListParams: params, CreatedRange: window}))
}

type ReportRequest struct {
	Type           string
	Interval       TimeRange
	Columns        []string
	Currency       string
	PayoutID       string
	IdempotencyKey string
}

func (c *Client) CreateReportRun(ctx context.Context, req ReportRequest) (*stripego.ReportingReportRun, error) {
	if !validToken(strings.ReplaceAll(req.Type, ".", "_")) {
		return nil, errors.New("invalid Stripe report type")
	}
	report := &stripego.ReportingReportRunCreateParametersParams{Columns: stripego.StringSlice(req.Columns)}
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
	return apiResult(c.api.V1ReportingReportRuns.Create(ctx, &stripego.ReportingReportRunCreateParams{
		Params: params, ReportType: stripego.String(req.Type), Parameters: report,
	}))
}

func (c *Client) GetReportType(ctx context.Context, id string) (*stripego.ReportingReportType, error) {
	if !validToken(strings.ReplaceAll(id, ".", "_")) {
		return nil, errors.New("invalid Stripe report type")
	}
	return apiResult(c.api.V1ReportingReportTypes.Retrieve(ctx, id, nil))
}

func (c *Client) ListReportTypes(ctx context.Context) ([]*stripego.ReportingReportType, error) {
	list, err := listPage(c.api.V1ReportingReportTypes.List(ctx, nil))
	return list.Items, err
}

func (c *Client) GetReportRun(ctx context.Context, id string) (*stripego.ReportingReportRun, error) {
	if !validID(id, "frr_") {
		return nil, errors.New("invalid Stripe ReportRun ID")
	}
	return apiResult(c.api.V1ReportingReportRuns.Retrieve(ctx, id, nil))
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
	return listPage(c.api.V1ReportingReportRuns.List(ctx, &stripego.ReportingReportRunListParams{
		ListParams:   params,
		CreatedRange: window,
	}))
}
