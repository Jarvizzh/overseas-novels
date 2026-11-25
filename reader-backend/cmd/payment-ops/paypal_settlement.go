package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"

	"reader-backend/internal/payment"
	"reader-backend/internal/payment/paypal"
	"reader-backend/internal/payment/paypal/credentialstore"
	"reader-backend/internal/payment/settlement"
)

func importPayPalSettlement(ctx context.Context, repo *settlement.Repository, paths []string, accountKey, environment string, audit settlement.Audit) (settlement.ImportResult, error) {
	cfg, err := credentialstore.LoadForAccount(ctx, payment.WebhookAccount{AccountKey: accountKey, Environment: environment})
	if err != nil {
		return settlement.ImportResult{}, err
	}
	if cfg.Environment != environment || cfg.ClientID == "" || cfg.MerchantID == "" {
		return settlement.ImportResult{}, errors.New("PayPal settlement requires the configured account, environment and merchant ID")
	}
	account := payment.WebhookAccount{AccountKey: cfg.ClientID, Environment: cfg.Environment}
	files, sums, fingerprint, err := openPayPalReport(paths)
	if err != nil {
		return settlement.ImportResult{}, err
	}
	defer closePayPalFiles(files)
	report := settlement.Report{Provider: payment.ProviderPayPal, Source: settlement.PayPalReport, Account: account,
		Reference: filepath.Base(files[0].Name()), Fingerprint: fingerprint, Audit: audit}
	return repo.Import(ctx, report, func(save func(settlement.ReportRow) error) error {
		importFiles := make([]paypal.SettlementFile, 0, len(files))
		digests := make([]hash.Hash, 0, len(files))
		for _, file := range files {
			digest := sha256.New()
			digests = append(digests, digest)
			importFiles = append(importFiles, paypal.SettlementFile{Name: filepath.Base(file.Name()), Body: io.TeeReader(file, digest)})
		}
		err := settlement.ReadPayPalReport(ctx, account, cfg.MerchantID, importFiles, save)
		if err != nil {
			return err
		}
		for i, digest := range digests {
			if !bytes.Equal(sums[i], digest.Sum(nil)) {
				return errors.New("PayPal settlement file changed during import")
			}
		}
		return nil
	})
}

func openPayPalReport(paths []string) ([]*os.File, [][]byte, [32]byte, error) {
	var files []*os.File
	var sums [][]byte
	importDigest := sha256.New()
	for _, path := range paths {
		file, err := os.Open(path)
		if err != nil {
			closePayPalFiles(files)
			return nil, nil, [32]byte{}, err
		}
		files = append(files, file)
		digest := sha256.New()
		if _, err := io.Copy(digest, file); err != nil {
			closePayPalFiles(files)
			return nil, nil, [32]byte{}, err
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			closePayPalFiles(files)
			return nil, nil, [32]byte{}, err
		}
		sum := digest.Sum(nil)
		sums = append(sums, sum)
		fmt.Fprintf(importDigest, "%s\x00%x\n", filepath.Base(path), sum)
	}
	return files, sums, [32]byte(importDigest.Sum(nil)), nil
}

func closePayPalFiles(files []*os.File) {
	for _, file := range files {
		_ = file.Close()
	}
}
