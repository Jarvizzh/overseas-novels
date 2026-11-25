package application

import (
	"context"
	"errors"
	"time"

	"reader-backend/internal/db"
	"reader-backend/internal/payment/stripe"
)

// A failed response can follow a successful write. Persist the lookup-only
// disposition before another worker can reclaim the attempt.
func recordStripeWriteFailure(ctx context.Context, err error, update string, args ...any) error {
	if err == nil || stripe.CanRetryWrite(err) {
		return err
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_, saveErr := db.Pool(ctx).Exec(ctx, update, args...)
	return errors.Join(err, saveErr)
}
