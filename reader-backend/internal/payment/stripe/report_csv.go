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
	ErrReportPending        = errors.New("Stripe report run is pending")
	ErrReportFailed         = errors.New("Stripe report run failed")
	ErrReportUnavailable    = errors.New("Stripe report CSV is unavailable")
	ErrMissingReportColumns = errors.New("Stripe report CSV is missing expected columns")
)

// MissingReportColumnsError records exact missing header columns so callers
// fail diagnostically instead of silently zeroing or dropping tax/fee values.
type MissingReportColumnsError struct {
	RunID      string
	ReportType string
	Missing    []string
}

func (e *MissingReportColumnsError) Error() string {
	cols := strings.Join(e.Missing, ", ")
	if e.RunID != "" || e.ReportType != "" {
		return fmt.Sprintf("Stripe report %s (%s) is missing expected CSV columns: %s", e.RunID, e.ReportType, cols)
	}
	return fmt.Sprintf("Stripe report CSV is missing expected columns: %s", cols)
}

func (e *MissingReportColumnsError) Unwrap() error {
	return ErrMissingReportColumns
}

type ReportCSV struct {
	RunID, ReportType, FileID string
	RequestedColumns          []string
	body                      io.ReadCloser
	reader                    *csv.Reader
	columns                   []string
	columnSet                 map[string]bool
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
		reqID := response.Header.Get("Request-Id")
		_ = response.Body.Close()
		switch response.StatusCode {
		case http.StatusUnauthorized:
			return nil, &CredentialError{
				AccountID:   c.scope.AccountID,
				Environment: c.scope.Environment,
				Purpose:     PurposeFiles,
				Reason:      ReasonAuthenticationFailed,
				HTTPStatus:  response.StatusCode,
				RequestID:   reqID,
				Retryable:   true,
				Detail:      "Stripe Files download authentication failed",
			}
		case http.StatusForbidden:
			return nil, &CredentialError{
				AccountID:   c.scope.AccountID,
				Environment: c.scope.Environment,
				Purpose:     PurposeFiles,
				Reason:      ReasonPermissionDenied,
				HTTPStatus:  response.StatusCode,
				RequestID:   reqID,
				Retryable:   true,
				Detail:      "Stripe API key lacks files permission to download report CSV",
			}
		default:
			return nil, fmt.Errorf("download Stripe report: HTTP %d", response.StatusCode)
		}
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
	var requestedCols []string
	if run.Parameters != nil && len(run.Parameters.Columns) > 0 {
		requestedCols = append([]string(nil), run.Parameters.Columns...)
	}
	report := &ReportCSV{
		RunID: run.ID, ReportType: run.ReportType, FileID: file.ID,
		RequestedColumns: requestedCols,
		body:             response.Body, reader: reader, columns: columns, columnSet: names,
	}
	if len(requestedCols) > 0 {
		if err := report.ValidateColumns(requestedCols...); err != nil {
			_ = response.Body.Close()
			return nil, err
		}
	}
	return report, nil
}

func (r *ReportCSV) Close() error { return r.body.Close() }

// Columns returns a copy of the CSV header column names.
func (r *ReportCSV) Columns() []string {
	return append([]string(nil), r.columns...)
}

// HasColumn reports whether the CSV header contains name.
func (r *ReportCSV) HasColumn(name string) bool {
	return r.columnSet[name]
}

// ValidateColumns verifies that every expected column is present in the CSV
// header, returning a diagnostic *MissingReportColumnsError if any are absent.
func (r *ReportCSV) ValidateColumns(required ...string) error {
	var missing []string
	seen := make(map[string]struct{}, len(required))
	for _, col := range required {
		col = strings.TrimSpace(col)
		if col == "" {
			continue
		}
		if _, dup := seen[col]; dup {
			continue
		}
		seen[col] = struct{}{}
		if !r.columnSet[col] {
			missing = append(missing, col)
		}
	}
	if len(missing) > 0 {
		return &MissingReportColumnsError{
			RunID:      r.RunID,
			ReportType: r.ReportType,
			Missing:    missing,
		}
	}
	return nil
}

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
		ID: r[ReportColumnBalanceTransactionID], SourceID: r[ReportColumnSourceID], ChargeID: r[ReportColumnChargeID],
		PaymentIntentID: r[ReportColumnPaymentIntentID], InvoiceID: r[ReportColumnInvoiceID], SubscriptionID: r[ReportColumnSubscriptionID],
		PayoutID: r[ReportColumnAutomaticPayoutID], ReportingCategory: r[ReportColumnReportingCategory],
		Currency: r[ReportColumnCurrency], CustomerFacingCurrency: r[ReportColumnCustomerFacingCurrency],
	}
	if !validID(item.ID, "txn_") || item.Currency == "" {
		return ReportBalanceTransaction{}, errors.New("Stripe report row requires a balance transaction ID and currency")
	}
	if v := strings.TrimSpace(r[ReportColumnIsManagedPayments]); v != "" {
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
		{ReportColumnGross, &item.Gross},
		{ReportColumnFee, &item.Fee},
		{ReportColumnNet, &item.Net},
		{ReportColumnWithheldTax, &item.WithheldTax},
		{ReportColumnFeeNetOfWithheldTax, &item.FeeNetOfWithheldTax},
		{ReportColumnCustomerFacingAmount, &item.CustomerFacingAmount},
		{ReportColumnExchangeRate, &item.ExchangeRate},
	} {
		if v := strings.TrimSpace(r[field.name]); v != "" {
			value, err := decimal.NewFromString(v)
			if err != nil {
				return ReportBalanceTransaction{}, fmt.Errorf("invalid Stripe report decimal in %s", field.name)
			}
			*field.dest = &value
		}
	}
	if item.ManagedPayments != nil && *item.ManagedPayments {
		var missing []string
		for _, col := range MoRReportColumns() {
			if _, present := r[col]; !present {
				missing = append(missing, col)
			}
		}
		if len(missing) > 0 {
			return ReportBalanceTransaction{}, &MissingReportColumnsError{Missing: missing}
		}
		if item.WithheldTax == nil || item.FeeNetOfWithheldTax == nil {
			return ReportBalanceTransaction{}, fmt.Errorf("%w: MoR row %s has blank %s or %s",
				ErrMissingReportColumns, item.ID, ReportColumnWithheldTax, ReportColumnFeeNetOfWithheldTax)
		}
	}
	return item, nil
}
