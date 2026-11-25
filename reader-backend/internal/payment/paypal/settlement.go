package paypal

import (
	"bufio"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const settlementTimestamp = "2006/01/02 15:04:05 -0700"

var settlementName = regexp.MustCompile(`^STL-([0-9]{8})\.(?:([AHRX])\.)?([0-9]{2})\.(?:([0-9]{2})\.)?([0-9]{3})\.(CSV|TAB)$`)

type SettlementFile struct {
	Name string
	Body io.Reader
}

type MoneyDirection string

const (
	Credit MoneyDirection = "CR"
	Debit  MoneyDirection = "DR"
)

// ReportMoney stores the unsigned integer as written by PayPal. The report
// scale is currency-dependent and may differ from an API amount's scale.
type ReportMoney struct {
	Units     string
	Currency  string
	Direction MoneyDirection
}

type SettlementEntry struct {
	MerchantID, FileName                   string
	Row                                    int
	PeriodStart, PeriodEnd                 time.Time
	TransactionID, EventCode               string
	ReferenceID, ReferenceType             string
	BankReferenceID, InvoiceID, TrackingID string
	InitiatedAt, CompletedAt               *time.Time
	Gross                                  ReportMoney
	Fee                                    *ReportMoney
	Fields                                 map[string]string
}

type SettlementSummary struct {
	ReportDate, Window, Version, Recipient string
	Files, Rows                            int
}

type settlementSection struct {
	merchant   string
	start, end time.Time
	columns    map[string]int
	rows       int
}

// ReadSettlementReport streams the supplied report parts in file order. A
// consumer can stage the rows as they are read and publish them on success.
func ReadSettlementReport(ctx context.Context, merchantID string, files []SettlementFile, visit func(SettlementEntry) error) (SettlementSummary, error) {
	var summary SettlementSummary
	if len(files) == 0 || visit == nil || merchantID == "" {
		return summary, errors.New("PayPal settlement requires files, a merchant and a row consumer")
	}
	var section settlementSection
	var matchedMerchant bool
	reportCount := -1
	for partIndex, file := range files {
		match := settlementName.FindStringSubmatch(file.Name)
		if match == nil || file.Body == nil || match[3] != fmt.Sprintf("%02d", partIndex+1) ||
			(match[2] == "") != (match[4] == "") ||
			(match[4] != "" && match[4] != fmt.Sprintf("%02d", len(files))) {
			return SettlementSummary{}, errors.New("invalid PayPal settlement filename or file sequence")
		}
		if partIndex == 0 {
			if _, err := time.Parse("20060102", match[1]); err != nil {
				return SettlementSummary{}, errors.New("invalid PayPal settlement report date")
			}
			summary.ReportDate, summary.Window, summary.Version = match[1], match[2], match[5]
		} else if summary.ReportDate != match[1] || (match[2] != "" && summary.Window != match[2]) || summary.Version != match[5] {
			return SettlementSummary{}, errors.New("PayPal settlement files refer to different reports")
		}

		input := bufio.NewReader(file.Body)
		if bom, err := input.Peek(3); err == nil && string(bom) == "\xef\xbb\xbf" {
			_, _ = input.Discard(3)
		}
		reader := csv.NewReader(input)
		reader.FieldsPerRecord = -1
		if match[6] == "TAB" {
			reader.Comma = '\t'
		}
		rows, fileHeader, fileFooter := 0, false, false
		for {
			if err := ctx.Err(); err != nil {
				return SettlementSummary{}, err
			}
			record, err := reader.Read()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return SettlementSummary{}, fmt.Errorf("read PayPal settlement rows: %w", err)
			}
			if len(record) == 0 || fileFooter {
				return SettlementSummary{}, errors.New("unexpected PayPal settlement record")
			}
			switch record[0] {
			case "RH":
				if len(record) < 5 || partIndex != 0 || summary.Recipient != "" || record[3] == "" ||
					record[4] != summary.Version || (summary.Window != "" && record[2] != summary.Window) {
					return SettlementSummary{}, errors.New("invalid PayPal settlement report header")
				}
				summary.Recipient = record[3]
				if summary.Window == "" {
					summary.Window = record[2]
				}
			case "FH":
				if fileHeader || !reportRowCount(record, partIndex+1) {
					return SettlementSummary{}, errors.New("invalid PayPal settlement file header")
				}
				fileHeader = true
			case "SH":
				if section.merchant != "" && summary.Version != "001" {
					return SettlementSummary{}, errors.New("PayPal settlement section missing its record count")
				}
				section, err = readSection(record)
				matchedMerchant = matchedMerchant || section.merchant == merchantID
			case "CH":
				section.columns, err = readColumns(record)
			case "SB":
				rows++
				section.rows++
				if section.merchant == "" || len(section.columns) != len(record) {
					return SettlementSummary{}, errors.New("PayPal settlement row has no matching section or columns")
				}
				if section.merchant != merchantID {
					continue
				}
				entry, err := section.entry(file.Name, rows, record)
				if err != nil {
					return SettlementSummary{}, err
				}
				if err := visit(entry); err != nil {
					return SettlementSummary{}, err
				}
			case "SC":
				if section.merchant == "" || !reportRowCount(record, section.rows) {
					return SettlementSummary{}, errors.New("PayPal settlement section count mismatch")
				}
				section = settlementSection{}
			case "RC":
				if reportCount != -1 || partIndex != len(files)-1 || len(record) != 2 {
					return SettlementSummary{}, errors.New("invalid PayPal settlement report count")
				}
				reportCount, err = strconv.Atoi(record[1])
				if err != nil || reportCount < 0 {
					return SettlementSummary{}, errors.New("invalid PayPal settlement report count")
				}
			case "FF":
				if !reportRowCount(record, rows) {
					return SettlementSummary{}, errors.New("PayPal settlement file count mismatch")
				}
				fileFooter = true
			case "SF", "RF":
				// These rows summarize balances. SB supplies the individual movements.
			default:
				return SettlementSummary{}, errors.New("unknown PayPal settlement record type")
			}
			if err != nil {
				return SettlementSummary{}, err
			}
		}
		if !fileFooter || (!fileHeader && summary.Version != "001") {
			return SettlementSummary{}, errors.New("incomplete PayPal settlement file")
		}
		summary.Rows += rows
		summary.Files++
	}
	if summary.Recipient == "" || (summary.Version != "001" && reportCount != summary.Rows) {
		return SettlementSummary{}, errors.New("incomplete PayPal settlement report")
	}
	if summary.Version != "001" && section.merchant != "" {
		return SettlementSummary{}, errors.New("unclosed PayPal settlement section")
	}
	if !matchedMerchant {
		return SettlementSummary{}, errors.New("merchant not found in PayPal settlement report")
	}
	return summary, nil
}

func reportRowCount(record []string, expected int) bool {
	if len(record) != 2 {
		return false
	}
	count, err := strconv.Atoi(record[1])
	return err == nil && count == expected
}

func readSection(record []string) (settlementSection, error) {
	if len(record) < 4 || record[3] == "" {
		return settlementSection{}, errors.New("invalid PayPal settlement section")
	}
	start, startErr := time.Parse(settlementTimestamp, record[1])
	end, endErr := time.Parse(settlementTimestamp, record[2])
	if startErr != nil || endErr != nil || end.Before(start) {
		return settlementSection{}, errors.New("invalid PayPal settlement section dates")
	}
	return settlementSection{merchant: record[3], start: start, end: end}, nil
}

func readColumns(record []string) (map[string]int, error) {
	columns := make(map[string]int, len(record))
	for index, column := range record {
		name := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(column)), " ", "_")
		if _, exists := columns[name]; exists || name == "" {
			return nil, errors.New("duplicate or empty PayPal settlement column")
		}
		columns[name] = index
	}
	return columns, nil
}

func (section settlementSection) entry(file string, row int, record []string) (SettlementEntry, error) {
	field := func(name string) string {
		if index, exists := section.columns[name]; exists {
			return record[index]
		}
		return ""
	}
	entry := SettlementEntry{
		MerchantID: section.merchant, FileName: file, Row: row,
		PeriodStart: section.start, PeriodEnd: section.end,
		TransactionID: field("transaction_id"), EventCode: field("transaction_event_code"),
		ReferenceID: field("paypal_reference_id"), ReferenceType: field("paypal_reference_id_type"),
		BankReferenceID: field("bank_reference_id"), InvoiceID: field("invoice_id"), TrackingID: field("payment_tracking_id"),
		Gross: ReportMoney{Units: field("gross_transaction_amount"), Currency: field("gross_transaction_currency"),
			Direction: MoneyDirection(field("transaction_debit_or_credit"))},
	}
	entry.Fields = make(map[string]string, len(section.columns))
	for name, index := range section.columns {
		entry.Fields[name] = record[index]
	}
	if entry.EventCode == "" || !entry.Gross.valid() {
		return SettlementEntry{}, errors.New("invalid PayPal settlement funds evidence")
	}
	if fee := field("fee_amount"); fee != "" {
		entry.Fee = &ReportMoney{Units: fee, Currency: field("fee_currency"), Direction: MoneyDirection(field("fee_debit_or_credit"))}
		if !entry.Fee.valid() {
			return SettlementEntry{}, errors.New("invalid PayPal settlement fee")
		}
	}
	for _, item := range []struct {
		name string
		dest **time.Time
	}{
		{"transaction_initiation_date", &entry.InitiatedAt},
		{"transaction_completion_date", &entry.CompletedAt},
	} {
		if value := field(item.name); value != "" {
			stamp, err := time.Parse(settlementTimestamp, value)
			if err != nil {
				return SettlementEntry{}, errors.New("invalid PayPal settlement transaction date")
			}
			*item.dest = &stamp
		}
	}
	return entry, nil
}

func (m ReportMoney) valid() bool {
	return len(m.Currency) == 3 && m.Units != "" && len(m.Units) <= 26 &&
		strings.Trim(m.Units, "0123456789") == "" && (m.Direction == Credit || m.Direction == Debit)
}
