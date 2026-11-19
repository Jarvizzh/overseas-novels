package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"conversion-backend/internal/credentials"
	"conversion-backend/internal/delivery"
	"conversion-backend/internal/partner"
	"conversion-backend/internal/partner/sinan"
)

func runCallback(ctx context.Context, store delivery.Store) error {
	if store.Environment != sinan.TestEnvironment || os.Getenv("CONVERSION_SINAN_MOCK_ENABLED") != "true" {
		return errors.New("callback mode requires the isolated test environment and CONVERSION_SINAN_MOCK_ENABLED=true")
	}
	keys, err := credentials.Parse(os.Getenv("CONVERSION_CREDENTIAL_KEYS"))
	if err != nil {
		return err
	}
	if len(keys) == 0 {
		return errors.New("CONVERSION_CREDENTIAL_KEYS is required for mock callbacks")
	}
	addr := os.Getenv("CONVERSION_CALLBACK_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8093"
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil || !net.ParseIP(host).IsLoopback() {
		return errors.New("CONVERSION_CALLBACK_ADDR must bind to a loopback IP")
	}
	inbox := sinan.Inbox{Pool: store.Pool, Environment: store.Environment, Decrypt: keys.Decrypt}
	return serveHTTP(ctx, addr, inbox.Handler())
}

func runPartnerWorker(ctx context.Context, store delivery.Store) error {
	enabled := os.Getenv("CONVERSION_PARTNER_REPORT_ENABLED") == "true"
	var keys credentials.Keyring
	if enabled {
		if store.Environment != sinan.TestEnvironment || os.Getenv("CONVERSION_SINAN_MOCK_ENABLED") != "true" {
			return errors.New("partner report sending requires the isolated test environment and CONVERSION_SINAN_MOCK_ENABLED=true")
		}
		var err error
		keys, err = credentials.Parse(os.Getenv("CONVERSION_CREDENTIAL_KEYS"))
		if err != nil {
			return err
		}
		if len(keys) == 0 {
			return errors.New("CONVERSION_CREDENTIAL_KEYS is required for mock partner reports")
		}
	}
	instance, err := os.Hostname()
	if err != nil {
		return errors.New("read partner worker hostname")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	client := &http.Client{
		Transport: transport, Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	defer client.CloseIdleConnections()
	reporter := partner.Reporter{
		Queue:  partner.ReportQueue{Pool: store.Pool, Environment: store.Environment, Source: "sinan", Contract: sinan.MockContract},
		Logger: slog.Default(), Instance: instance,
	}
	if enabled {
		reporter.Adapter = sinan.ReportAdapter{Client: client, Decrypt: keys.Decrypt}
	}
	reporter.Run(ctx)
	return nil
}
