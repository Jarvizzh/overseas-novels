package settlement

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"hash"
	"io"
	"os"

	"reader-backend/internal/payment"
	"reader-backend/internal/payment/stripe"
)

// StripeReportImportOptions configures optional header validation during
// Stripe Report Run CSV import while preserving manual import compatibility.
type StripeReportImportOptions struct {
	ExpectedColumns   []string
	RequireMoRColumns bool
}

func ImportStripeReport(ctx context.Context, client *stripe.Client, repo *Repository, runID string, audit Audit) (ImportResult, error) {
	return ImportStripeReportWithOptions(ctx, client, repo, runID, audit, StripeReportImportOptions{})
}

func ImportStripeReportWithOptions(ctx context.Context, client *stripe.Client, repo *Repository, runID string, audit Audit, opts StripeReportImportOptions) (ImportResult, error) {
	file, err := client.OpenReportCSV(ctx, runID)
	if err != nil {
		return ImportResult{}, err
	}
	defer file.Close()
	required := append([]string(nil), opts.ExpectedColumns...)
	if opts.RequireMoRColumns {
		required = append(required, stripe.MoRReportColumns()...)
	}
	if len(required) > 0 {
		if err := file.ValidateColumns(required...); err != nil {
			return ImportResult{}, err
		}
	}
	staged, err := newStagedSettlement()
	if err != nil {
		return ImportResult{}, err
	}
	defer staged.close()
	for {
		row, err := file.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return ImportResult{}, err
		}
		evidence, err := row.LedgerEvidence(client.Scope(), runID)
		if err != nil {
			return ImportResult{}, err
		}
		if err := staged.add(file.FileID, evidence); err != nil {
			return ImportResult{}, err
		}
	}
	return staged.commit(ctx, repo, client.Scope(), StripeReport, runID, audit, nil)
}

func ImportStripePayout(ctx context.Context, client *stripe.Client, repo *Repository, id string, audit Audit) (ImportResult, error) {
	staged, err := newStagedSettlement()
	if err != nil {
		return ImportResult{}, err
	}
	defer staged.close()
	payout, err := client.ReadPayoutSettlement(ctx, id, func(e payment.LedgerEvidence) error { return staged.add(id, e) })
	if err != nil {
		return ImportResult{}, err
	}
	_, _ = staged.digest.Write(payout.Payload)
	return staged.commit(ctx, repo, client.Scope(), StripePayout, id, audit, &payout)
}

// Stage the full download on local disk before opening a database transaction.
type stagedSettlement struct {
	file    *os.File
	digest  hash.Hash
	encoder *json.Encoder
	rows    int
}

func newStagedSettlement() (*stagedSettlement, error) {
	file, err := os.CreateTemp("", "starnovel-settlement-*")
	if err != nil {
		return nil, err
	}
	digest := sha256.New()
	return &stagedSettlement{file: file, digest: digest, encoder: json.NewEncoder(io.MultiWriter(file, digest))}, nil
}

func (s *stagedSettlement) add(file string, raw payment.LedgerEvidence) error {
	s.rows++
	return s.encoder.Encode(ReportRow{File: file, Number: s.rows, Payload: raw.Payload, Evidence: &raw})
}

func (s *stagedSettlement) close() {
	_ = s.file.Close()
	_ = os.Remove(s.file.Name())
}

func (s *stagedSettlement) commit(ctx context.Context, repo *Repository, scope stripe.Scope, source, ref string, audit Audit, payout *payment.PayoutEvidence) (ImportResult, error) {
	if _, err := s.file.Seek(0, io.SeekStart); err != nil {
		return ImportResult{}, err
	}
	report := Report{Provider: payment.ProviderStripe, Source: source, Reference: ref,
		Account:     payment.WebhookAccount{AccountKey: scope.AccountID, Environment: string(scope.Environment)},
		Fingerprint: [32]byte(s.digest.Sum(nil)), Audit: audit, Payout: payout}
	return repo.Import(ctx, report, func(save func(ReportRow) error) error {
		decoder := json.NewDecoder(s.file)
		for i := 0; i < s.rows; i++ {
			var row ReportRow
			if err := decoder.Decode(&row); err != nil {
				return err
			}
			if err := save(row); err != nil {
				return err
			}
		}
		return nil
	})
}
