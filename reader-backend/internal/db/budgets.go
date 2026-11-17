package db

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type budgetKey uint8

const (
	acquireBudgetKey budgetKey = iota
	queryBudgetKey
)

// pgx explicitly uses the context returned by these hooks for the entire
// acquire/query. The separate keys keep nested tracing lifetimes independent.
// This caps queue waiting even for a caller with a long provider deadline.
type resourceBudgets struct{ acquire, query time.Duration }

func (b resourceBudgets) TraceAcquireStart(ctx context.Context, _ *pgxpool.Pool, _ pgxpool.TraceAcquireStartData) context.Context {
	ctx, cancel := context.WithTimeout(ctx, b.acquire)
	return context.WithValue(ctx, acquireBudgetKey, cancel)
}
func (resourceBudgets) TraceAcquireEnd(ctx context.Context, _ *pgxpool.Pool, _ pgxpool.TraceAcquireEndData) {
	if cancel, ok := ctx.Value(acquireBudgetKey).(context.CancelFunc); ok {
		cancel()
	}
}
func (b resourceBudgets) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	ctx, cancel := context.WithTimeout(ctx, b.query)
	return context.WithValue(ctx, queryBudgetKey, cancel)
}
func (resourceBudgets) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryEndData) {
	if cancel, ok := ctx.Value(queryBudgetKey).(context.CancelFunc); ok {
		cancel()
	}
}
