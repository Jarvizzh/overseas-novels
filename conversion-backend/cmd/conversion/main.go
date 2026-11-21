package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"syscall"
	"time"

	"conversion-backend/internal/credentials"
	"conversion-backend/internal/delivery"
	"conversion-backend/internal/media/meta"
	"conversion-backend/internal/partner/sinan"

	"github.com/jackc/pgx/v5/pgxpool"
)

var graphVersion = regexp.MustCompile(`^v[0-9]+\.[0-9]+$`)

func main() {
	mode := flag.String("mode", "worker", "conversion service mode (worker, api, collector, callback or partner-worker)")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, *mode); err != nil {
		slog.Error("conversion service stopped", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, mode string) error {
	switch mode {
	case "worker", "api", "collector", "callback", "partner-worker":
	default:
		return errors.New("mode must be worker, api, collector, callback or partner-worker")
	}
	dsn, environment, version := os.Getenv("DATABASE_URL"), os.Getenv("CONVERSION_ENVIRONMENT"), os.Getenv("META_GRAPH_VERSION")
	if dsn == "" || environment == "" {
		return errors.New("DATABASE_URL and CONVERSION_ENVIRONMENT are required")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return errors.New("invalid DATABASE_URL")
	}
	cfg.ConnConfig.ConnectTimeout = 3 * time.Second
	cfg.MaxConns = 8
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return errors.New("create conversion database pool")
	}
	defer pool.Close()
	service := delivery.Service{Store: delivery.Store{Pool: pool, Environment: environment}}
	startup, cancel := context.WithTimeout(ctx, 5*time.Second)
	err = service.CheckSchema(startup)
	cancel()
	if err != nil {
		return err
	}

	switch mode {
	case "api":
		return runAPI(ctx, pool, environment)
	case "callback":
		return runCallback(ctx, service.Store)
	case "partner-worker":
		return runPartnerWorker(ctx, service.Store)
	case "collector":
		runCollector(ctx, pool, environment)
		return nil
	}
	return runWorker(ctx, service, version)
}

func runWorker(ctx context.Context, service delivery.Service, version string) error {
	enabled := os.Getenv("CONVERSION_META_ENABLED") == "true"
	if enabled && !graphVersion.MatchString(version) {
		return errors.New("META_GRAPH_VERSION must be set to a version such as vNN.0")
	}
	keys, err := credentials.Parse(os.Getenv("CONVERSION_CREDENTIAL_KEYS"))
	if err != nil {
		return err
	}
	if enabled && len(keys) == 0 {
		return errors.New("CONVERSION_CREDENTIAL_KEYS is required when Meta sending is enabled")
	}

	host, err := os.Hostname()
	if err != nil {
		return errors.New("read conversion worker hostname")
	}
	client := &http.Client{
		Timeout:       5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	defer client.CloseIdleConnections()
	endpoint := &url.URL{Scheme: "https", Host: "graph.facebook.com", Path: "/" + version}
	worker := delivery.Worker{
		Service: service, Media: meta.Client{BaseURL: endpoint, HTTP: client},
		Decrypt: keys.Decrypt, Logger: slog.Default(), Instance: host,
		Paused: !enabled, Concurrency: 5, GlobalLimit: 5,
		PollInterval: 15 * time.Second, PixelInterval: 3 * time.Second,
		RequestTimeout: client.Timeout,
	}
	if service.Environment == sinan.TestEnvironment {
		worker.ConsumeInbox = sinan.Consume
	}
	worker.Run(ctx)
	return nil
}
