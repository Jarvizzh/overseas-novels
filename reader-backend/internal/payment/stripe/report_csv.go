package stripe

import (
	"bufio"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	stripego "github.com/stripe/stripe-go/v87"
)

var (
	ErrReportPending     = errors.New("Stripe report run is pending")
	ErrReportFailed      = errors.New("Stripe report run failed")
	ErrReportUnavailable = errors.New("Stripe report CSV is unavailable")
)

type ReportCSV struct {
	RunID, ReportType, FileID string
	body                      io.ReadCloser
	reader                    *csv.Reader
	columns                   []string
}

// OpenReportCSV downloads the file belonging to a completed run from the
// authenticated Stripe Files API. The caller closes the stream after reading.
func (c *Client) OpenReportCSV(ctx context.Context, runID string) (*ReportCSV, error) {
	run, err := c.GetReportRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	if run.ID != runID || run.Livemode != (c.scope.Environment == Live) {
		return nil, errors.New("unexpected Stripe report run or environment")
	}
	switch run.Status {
	case stripego.ReportingReportRunStatusPending:
		return nil, ErrReportPending
	case stripego.ReportingReportRunStatusFailed:
		return nil, ErrReportFailed
	case stripego.ReportingReportRunStatusSucceeded:
	default:
		return nil, ErrReportUnavailable
	}

	file := run.Result
	if file == nil || !validID(file.ID, "file_") || file.Type != "csv" ||
		(file.Purpose != "" && file.Purpose != stripego.FilePurposeFinanceReportRun) ||
		(file.ExpiresAt > 0 && file.ExpiresAt <= time.Now().Unix()) {
		return nil, ErrReportUnavailable
	}
	fileURL, err := url.Parse(file.URL)
	if err != nil || fileURL.Scheme != "https" || fileURL.Host != "files.stripe.com" ||
		fileURL.User != nil || fileURL.Fragment != "" || fileURL.RawQuery != "" || fileURL.ForceQuery ||
		fileURL.EscapedPath() != "/v1/files/"+file.ID+"/contents" {
		return nil, ErrReportUnavailable
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, fileURL.String(), nil)
	if err != nil {
		return nil, ErrReportUnavailable
	}
	request.Header.Set("Authorization", "Bearer "+c.apiKey)
	request.Header.Set("Stripe-Version", APIVersion)
	request.Header.Set("Accept", "text/csv")
	response, err := c.reportHTTP.Do(request)
	if err != nil {
		return nil, fmt.Errorf("download Stripe report: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		_ = response.Body.Close()
		return nil, fmt.Errorf("download Stripe report: HTTP %d", response.StatusCode)
	}

	// Strip a UTF-8 BOM before CSV parsing. A quoted first header is valid.
	input := bufio.NewReader(response.Body)
	if prefix, err := input.Peek(3); err == nil && string(prefix) == "\xef\xbb\xbf" {
		_, _ = input.Discard(3)
	}
	reader := csv.NewReader(input)
	columns, err := reader.Read()
	if err != nil {
		_ = response.Body.Close()
		return nil, fmt.Errorf("read Stripe report header: %w", err)
	}
	names := make(map[string]bool, len(columns))
	for _, name := range columns {
		if name == "" || names[name] {
			_ = response.Body.Close()
			return nil, errors.New("Stripe report has an empty or duplicate CSV column")
		}
		names[name] = true
	}
	return &ReportCSV{
		RunID: run.ID, ReportType: run.ReportType, FileID: file.ID,
		body: response.Body, reader: reader, columns: columns,
	}, nil
}

func (r *ReportCSV) Close() error { return r.body.Close() }

// ReportRow preserves Stripe's headers and values exactly, including new columns.
type ReportRow map[string]string

func (r *ReportCSV) Read() (ReportRow, error) {
	record, err := r.reader.Read()
	if err != nil {
		return nil, err
	}
	row := make(ReportRow, len(record))
	for i, column := range r.columns {
		row[column] = record[i]
	}
	return row, nil
}

// ReportBalanceTransaction keeps report amounts in Stripe's major units. The
// BalanceTransactions API uses minor units. Optional columns remain nil.
type ReportBalanceTransaction struct {
	ID, SourceID, ChargeID, PaymentIntentID, InvoiceID, SubscriptionID, PayoutID string
	ReportingCategory, Currency, CustomerFacingCurrency                          string
	Gross, Fee, Net, WithheldTax, FeeNetOfWithheldTax                            *decimal.Decimal
	CustomerFacingAmount, ExchangeRate                                           *decimal.Decimal
	ManagedPayments                                                              *bool
}

// BalanceTransaction extracts an itemized balance or payout record. Summary
// reports are available through Read and don't have a balance transaction ID.
func (r ReportRow) BalanceTransaction() (ReportBalanceTransaction, error) {
	item := ReportBalanceTransaction{
		ID: r["balance_transaction_id"], SourceID: r["source_id"], ChargeID: r["charge_id"],
		PaymentIntentID: r["payment_intent_id"], InvoiceID: r["invoice_id"], SubscriptionID: r["subscription_id"],
		PayoutID: r["automatic_payout_id"], ReportingCategory: r["reporting_category"],
		Currency: r["currency"], CustomerFacingCurrency: r["customer_facing_currency"],
	}
	if !validID(item.ID, "txn_") || item.Currency == "" {
		return ReportBalanceTransaction{}, errors.New("Stripe report row requires a balance transaction ID and currency")
	}
	if v := strings.TrimSpace(r["is_managed_payments"]); v != "" {
		managed, err := strconv.ParseBool(v)
		if err != nil {
			return ReportBalanceTransaction{}, errors.New("invalid Stripe report is_managed_payments")
		}
		item.ManagedPayments = &managed
	}
	for _, field := range []struct {
		name string
		dest **decimal.Decimal
	}{
		{"gross", &item.Gross},
		{"fee", &item.Fee},
		{"net", &item.Net},
		{"withheld_tax", &item.WithheldTax},
		{"fee_net_of_withheld_tax", &item.FeeNetOfWithheldTax},
		{"customer_facing_amount", &item.CustomerFacingAmount},
		{"exchange_rate", &item.ExchangeRate},
	} {
		if v := strings.TrimSpace(r[field.name]); v != "" {
			value, err := decimal.NewFromString(v)
			if err != nil {
				return ReportBalanceTransaction{}, fmt.Errorf("invalid Stripe report decimal in %s", field.name)
			}
			*field.dest = &value
		}
	}
	return item, nil
}
