package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"strconv"
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
	var poolSize int
	var settings workerSettings
	var err error
	if mode == "worker" {
		settings, err = loadWorkerSettings(environment)
		poolSize = settings.poolSize
	} else {
		poolSize, err = workerInt("CONVERSION_DB_MAX_CONNS", 8, 128)
	}
	if err != nil {
		return err
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return errors.New("invalid DATABASE_URL")
	}
	cfg.ConnConfig.ConnectTimeout = 3 * time.Second
	cfg.MaxConns = int32(poolSize)
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
	return runWorker(ctx, service, version, settings)
}

func runWorker(ctx context.Context, service delivery.Service, version string, settings workerSettings) error {
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

	host, err := workerInstance()
	if err != nil {
		return errors.New("read conversion worker hostname")
	}
	client := &http.Client{
		Timeout:       settings.requestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	defer client.CloseIdleConnections()
	endpoint := &url.URL{Scheme: "https", Host: "graph.facebook.com", Path: "/" + version}
	worker := delivery.Worker{
		Service: service, Media: meta.Client{BaseURL: endpoint, HTTP: client},
		Decrypt: keys.Decrypt, Logger: slog.Default(), Instance: host,
		Paused: !enabled, Concurrency: settings.concurrency, GlobalLimit: settings.globalLimit, BatchSize: settings.batchSize,
		PollInterval: settings.pollInterval, PixelInterval: settings.pixelInterval, NormalizeBatch: settings.normalizeBatch,
		ReaderNormalizers: settings.readerNormalizers, InboxNormalizers: settings.inboxNormalizers, QueueProbeInterval: settings.queueProbe,
		RequestTimeout: client.Timeout,
	}
	if settings.inboxEnabled {
		worker.ConsumeInbox = sinan.Consume
	}
	worker.Run(ctx)
	return nil
}

func workerInstance() (string, error) {
	if name := os.Getenv("CONVERSION_INSTANCE_ID"); name != "" {
		return name, nil
	}
	return os.Hostname()
}

type workerSettings struct {
	poolSize, globalLimit, concurrency, batchSize, normalizeBatch int
	readerNormalizers, inboxNormalizers                           int
	inboxEnabled                                                  bool
	pixelInterval, pollInterval, queueProbe, requestTimeout       time.Duration
}

func loadWorkerSettings(environment string) (workerSettings, error) {
	var w workerSettings
	var err error
	w.inboxEnabled = os.Getenv("CONVERSION_SINAN_MOCK_ENABLED") == "true"
	if w.inboxEnabled && environment != sinan.TestEnvironment {
		return w, errors.New("CONVERSION_SINAN_MOCK_ENABLED requires CONVERSION_ENVIRONMENT=test")
	}
	for _, option := range []struct {
		dest              *int
		name              string
		fallback, maximum int
	}{
		{&w.poolSize, "CONVERSION_WORKER_DB_MAX_CONNS", 16, 128},
		{&w.globalLimit, "CONVERSION_META_GLOBAL_LIMIT", 5, 100},
		{&w.batchSize, "CONVERSION_META_BATCH_SIZE", 1, delivery.MaxBatchSize},
		{&w.normalizeBatch, "CONVERSION_NORMALIZE_BATCH", 20, 500},
		{&w.readerNormalizers, "CONVERSION_READER_NORMALIZERS", 2, 3},
		{&w.inboxNormalizers, "CONVERSION_INBOX_NORMALIZERS", 2, 3},
	} {
		if *option.dest, err = workerInt(option.name, option.fallback, option.maximum); err != nil {
			return w, err
		}
	}
	w.concurrency, err = workerInt("CONVERSION_META_CONCURRENCY", min(5, w.globalLimit), w.globalLimit)
	if err != nil {
		return w, err
	}
	budget, err := workerInt("CONVERSION_NORMALIZER_LIMIT", min(4, w.poolSize/2), w.poolSize/2)
	if err != nil {
		return w, err
	}
	total := w.readerNormalizers
	if w.inboxEnabled {
		total += w.inboxNormalizers
	}
	if total > budget {
		return w, fmt.Errorf("enabled normalizers (%d) exceed CONVERSION_NORMALIZER_LIMIT (%d) for a Worker pool of %d connections",
			total, budget, w.poolSize)
	}
	w.pixelInterval, err = workerInterval("CONVERSION_META_PIXEL_INTERVAL", 3*time.Second)
	if err != nil {
		return w, err
	}
	w.pollInterval, err = workerInterval("CONVERSION_WORKER_POLL_INTERVAL", 15*time.Second)
	if err != nil {
		return w, err
	}
	w.queueProbe, err = workerInterval("CONVERSION_WORKER_QUEUE_PROBE_INTERVAL", 250*time.Millisecond)
	if err != nil {
		return w, err
	}
	w.requestTimeout, err = workerInterval("CONVERSION_META_REQUEST_TIMEOUT", 5*time.Second)
	return w, err
}

func workerInt(name string, fallback, upper int) (int, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 || value > upper {
		return 0, fmt.Errorf("%s must be an integer from 1 to %d", name, upper)
	}
	return value, nil
}

func workerInterval(name string, fallback time.Duration) (time.Duration, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value < 100*time.Millisecond || value > 30*time.Second {
		return 0, fmt.Errorf("%s must be a duration from 100ms to 30s", name)
	}
	return value, nil
}
