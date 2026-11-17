package auth

import (
	"context"
	"strconv"
)

type promotionContextKey struct{}

func promotionContext(ctx context.Context, raw string) context.Context {
	id, _ := strconv.Atoi(raw)
	return context.WithValue(ctx, promotionContextKey{}, id)
}
func promotionID(ctx context.Context) int { id, _ := ctx.Value(promotionContextKey{}).(int); return id }
