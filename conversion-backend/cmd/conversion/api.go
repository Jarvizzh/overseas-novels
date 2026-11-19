package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"time"

	"conversion-backend/internal/credentials"
	"conversion-backend/internal/operations"

	"github.com/jackc/pgx/v5/pgxpool"
)

func runAPI(ctx context.Context, pool *pgxpool.Pool, environment string) error {
	token := os.Getenv("CONVERSION_PRIVATE_TOKEN")
	if token == "" {
		return errors.New("CONVERSION_PRIVATE_TOKEN is required in API mode")
	}
	repo := operations.Repository{Pool: pool, Environment: environment}
	if version := os.Getenv("CONVERSION_CREDENTIAL_KEY_VERSION"); version != "" {
		keys, err := credentials.Parse(os.Getenv("CONVERSION_CREDENTIAL_KEYS"))
		if err != nil {
			return err
		}
		if _, ok := keys[version]; !ok {
			return errors.New("configured API credential key version unavailable")
		}
		repo.EncryptCredential = func(token string) ([]byte, string, error) {
			cipher, err := keys.Encrypt(version, token)
			return cipher, version, err
		}
	}
	addr := os.Getenv("CONVERSION_PRIVATE_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8092"
	}
	return serveHTTP(ctx, addr, operations.NewHandler(repo, token))
}

func serveHTTP(ctx context.Context, addr string, handler http.Handler) error {
	server := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 3 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      8 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServe() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		stop, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(stop); err != nil {
			_ = server.Close()
			return err
		}
		return nil
	}
}
