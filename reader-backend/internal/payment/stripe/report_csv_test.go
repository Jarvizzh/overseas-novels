package stripe

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
)

func TestOpenReportCSVPreservesMoneyAndProviderReferences(t *testing.T) {
	ctx := context.Background()
	client := fixtureClient(t, func(req *http.Request) (*http.Response, error) {
		switch req.URL.Host + req.URL.Path {
		case "api.stripe.com/v1/reporting/report_runs/frr_batch":
			return stripeResponse(200, `{"id":"frr_batch","status":"succeeded","report_type":"payout_reconciliation.itemized.7","result":{"id":"file_batch","type":"csv","purpose":"finance_report_run","url":"https://files.stripe.com/v1/files/file_batch/contents"}}`)
		case "files.stripe.com/v1/files/file_batch/contents":
			if req.Method != http.MethodGet || req.Header.Get("Accept") != "text/csv" {
				t.Fatalf("unexpected report download: %s %s", req.Method, req.URL)
			}
			return stripeResponse(200, "\xef\xbb\xbf"+
				`"balance_transaction_id",source_id,automatic_payout_id,currency,gross,fee,net,withheld_tax,fee_net_of_withheld_tax,customer_facing_currency,customer_facing_amount,exchange_rate,is_managed_payments,description
txn_sale,ch_sale,po_batch,usd,10.00,1.80,8.20,1.23,0.57,cad,15.24,0.656167979002624671,true,"A, B"
txn_refund,re_refund,po_batch,jpy,-500,0,-500,,,,,,false,refund
`)
		default:
			t.Fatalf("unexpected report request: %s %s", req.Method, req.URL)
			return nil, nil
		}
	})
	report, err := client.OpenReportCSV(ctx, "frr_batch")
	if err != nil {
		t.Fatal(err)
	}
	defer report.Close()
	if report.FileID != "file_batch" || report.ReportType != "payout_reconciliation.itemized.7" {
		t.Fatalf("missing report provenance: %+v", report)
	}
	row, err := report.Read()
	if err != nil {
		t.Fatal(err)
	}
	sale, err := row.BalanceTransaction()
	if err != nil || sale.ID != "txn_sale" || sale.SourceID != "ch_sale" || sale.PayoutID != "po_batch" ||
		sale.WithheldTax == nil || sale.WithheldTax.String() != "1.23" ||
		sale.FeeNetOfWithheldTax == nil || sale.FeeNetOfWithheldTax.String() != "0.57" ||
		sale.ExchangeRate == nil || sale.ExchangeRate.String() != "0.656167979002624671" ||
		sale.CustomerFacingCurrency != "cad" || sale.CustomerFacingAmount == nil ||
		sale.CustomerFacingAmount.String() != "15.24" || sale.ManagedPayments == nil || !*sale.ManagedPayments ||
		row["description"] != "A, B" {
		t.Fatalf("lost report evidence: %+v, error %v", sale, err)
	}
	row, err = report.Read()
	if err != nil {
		t.Fatal(err)
	}
	refund, err := row.BalanceTransaction()
	if err != nil || refund.SourceID != "re_refund" || refund.Currency != "jpy" ||
		refund.Gross == nil || refund.Gross.String() != "-500" ||
		refund.Fee == nil || !refund.Fee.IsZero() || refund.WithheldTax != nil ||
		refund.ManagedPayments == nil || *refund.ManagedPayments {
		t.Fatalf("lost refund evidence: %+v, error %v", refund, err)
	}
	if _, err := report.Read(); !errors.Is(err, io.EOF) {
		t.Fatalf("expected CSV EOF, got %v", err)
	}
}

func TestOpenReportCSVOnlyDownloadsScopedCompletedFiles(t *testing.T) {
	for _, tc := range []struct {
		name, run string
		want      error
	}{
		{"pending", `"status":"pending"`, ErrReportPending},
		{"failed", `"status":"failed","error":"private failure"`, ErrReportFailed},
		{"wrong mode", `"status":"succeeded","livemode":true`, nil},
		{"external file", `"status":"succeeded","result":{"id":"file_batch","type":"csv","url":"https://stripe.example/v1/files/file_batch/contents"}`, ErrReportUnavailable},
		{"different file", `"status":"succeeded","result":{"id":"file_other","type":"csv","url":"https://files.stripe.com/v1/files/file_batch/contents"}`, ErrReportUnavailable},
		{"expired file", `"status":"succeeded","result":{"id":"file_batch","type":"csv","expires_at":1,"url":"https://files.stripe.com/v1/files/file_batch/contents"}`, ErrReportUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := fixtureClient(t, func(req *http.Request) (*http.Response, error) {
				if req.URL.Host != "api.stripe.com" || req.URL.Path != "/v1/reporting/report_runs/frr_batch" {
					t.Fatalf("unexpected file request: %s", req.URL)
				}
				return stripeResponse(200, `{"id":"frr_batch",`+tc.run+`}`)
			})
			report, err := client.OpenReportCSV(context.Background(), "frr_batch")
			if report != nil || err == nil || tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("unexpected report result: %v, error %v", report, err)
			}
		})
	}
}

func TestReportDownloadNeverFollowsRedirect(t *testing.T) {
	client := fixtureClient(t, func(req *http.Request) (*http.Response, error) {
		switch req.URL.Host + req.URL.Path {
		case "api.stripe.com/v1/reporting/report_runs/frr_batch":
			return stripeResponse(200, `{"id":"frr_batch","status":"succeeded","result":{"id":"file_batch","type":"csv","url":"https://files.stripe.com/v1/files/file_batch/contents"}}`)
		case "files.stripe.com/v1/files/file_batch/contents":
			response, _ := stripeResponse(302, "")
			response.Header.Set("Location", "https://files.stripe.com/elsewhere")
			return response, nil
		default:
			t.Fatalf("followed unexpected redirect: %s", req.URL)
			return nil, nil
		}
	})
	if report, err := client.OpenReportCSV(context.Background(), "frr_batch"); report != nil || err == nil {
		t.Fatalf("redirect was treated as report CSV: %v, error %v", report, err)
	}
}
