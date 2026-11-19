package main

import (
	"context"
	"log/slog"
	"time"

	"conversion-backend/internal/operations"

	"github.com/jackc/pgx/v5/pgxpool"
)

func runCollector(ctx context.Context, pool *pgxpool.Pool, environment string) {
	repo := operations.Repository{Pool: pool, Environment: environment}
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for ctx.Err() == nil {
		sample, cancel := context.WithTimeout(ctx, 10*time.Second)
		changes, err := repo.Collect(sample)
		cancel()
		if err != nil && ctx.Err() == nil {
			slog.Error("conversion alert collection failed")
		}
		for _, change := range changes {
			slog.Info("conversion alert changed", "environment", environment, "kind", change.Kind, "subject", change.Subject, "state", change.State)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
