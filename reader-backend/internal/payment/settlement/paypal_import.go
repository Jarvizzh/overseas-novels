package settlement

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"reader-backend/internal/payment"
	"reader-backend/internal/payment/paypal"
)

type Upload struct {
	Name string `json:"name"`
	Data []byte `json:"data"`
}

func ImportPayPalUploads(ctx context.Context, repo *Repository, account payment.WebhookAccount, merchant string, uploads []Upload, audit Audit) (ImportResult, error) {
	if len(uploads) == 0 || merchant == "" {
		return ImportResult{}, errors.New("PayPal report and merchant are required")
	}
	// File pickers may supply the parts alphabetically or in selection order.
	// PayPal's names include zero-padded part numbers, checked by the parser.
	uploads = slices.Clone(uploads)
	slices.SortFunc(uploads, func(a, b Upload) int { return strings.Compare(a.Name, b.Name) })
	files := make([]paypal.SettlementFile, 0, len(uploads))
	digest := sha256.New()
	for _, upload := range uploads {
		fmt.Fprintf(digest, "%s\x00%x\n", upload.Name, sha256.Sum256(upload.Data))
		files = append(files, paypal.SettlementFile{Name: upload.Name, Body: bytes.NewReader(upload.Data)})
	}
	report := Report{Provider: payment.ProviderPayPal, Source: PayPalReport, Account: account,
		Reference: uploads[0].Name, Fingerprint: [32]byte(digest.Sum(nil)), Audit: audit}
	return repo.Import(ctx, report, func(save func(ReportRow) error) error {
		return ReadPayPalReport(ctx, account, merchant, files, save)
	})
}

func ReadPayPalReport(ctx context.Context, account payment.WebhookAccount, merchant string, files []paypal.SettlementFile, save func(ReportRow) error) error {
	_, err := paypal.ReadSettlementReport(ctx, merchant, files, func(row paypal.SettlementEntry) error {
		if row.Gross.Currency != CurrencyUSD || (row.Fee != nil && row.Fee.Currency != CurrencyUSD) {
			return fmt.Errorf("PayPal settlement row %d requires USD", row.Row)
		}
		record := ReportRow{File: row.FileName, Number: row.Row}
		entry, err := row.LedgerEvidence(account)
		if errors.Is(err, paypal.ErrSettlementMovementIdentity) {
			record.ReviewReason = "missing_movement_identity"
			record.Payload, err = json.Marshal(row.Fields)
			if err != nil {
				return err
			}
			return save(record)
		}
		if err != nil {
			return fmt.Errorf("normalize PayPal settlement row %d: %w", row.Row, err)
		}
		record.Payload, record.Evidence = entry.Payload, &entry
		if entry.Kind == payment.MovementPayout {
			gross := entry.Amounts[payment.LedgerGross]
			const paid = "paid"
			if gross.Minor == -1<<63 {
				return errors.New("PayPal settlement withdrawal exceeds supported amount")
			}
			record.Payout = &payment.PayoutEvidence{
				Reference: entry.MovementKey, TransferKey: entry.MovementKey, Status: paid,
				Expected:      payment.LedgerMoney{Currency: gross.Currency, Minor: -gross.Minor},
				BankReference: row.BankReferenceID, ObservedAt: row.PeriodEnd, Payload: entry.Payload,
			}
		}
		return save(record)
	})
	return err
}
