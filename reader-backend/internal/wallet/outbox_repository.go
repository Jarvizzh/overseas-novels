package wallet

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"reader-backend/internal/db"
)

func claimPaymentEvents(ctx context.Context) ([]paymentEventJob, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	lease := uuid.NewString()
	rows, err := db.DB.Query(ctx, `WITH due AS (
 SELECT event_id FROM payment_event_outbox WHERE state='pending' AND delivered_at IS NULL AND next_attempt_at<=now()
 AND (lease_until IS NULL OR lease_until<=now()) ORDER BY next_attempt_at,event_id LIMIT 5 FOR UPDATE SKIP LOCKED
 ) UPDATE payment_event_outbox e SET lease_token=$1,lease_until=now()+interval '2 minutes',attempts=attempts+1
 FROM due WHERE e.event_id=due.event_id RETURNING e.event_id,e.payload`, lease)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (paymentEventJob, error) {
		job := paymentEventJob{lease: lease}
		err := row.Scan(&job.id, &job.payload)
		return job, err
	})
}

func finishPaymentEvent(ctx context.Context, job paymentEventJob, deliveryErr error) error {
	if deliveryErr != nil {
		_, err := db.DB.Exec(ctx, `UPDATE payment_event_outbox SET lease_until=NULL,last_error=left($3,1000),next_attempt_at=now()+interval '1 second'*LEAST(30*attempts,3600) WHERE event_id=$1 AND lease_token=$2`, job.id, job.lease, deliveryErr.Error())
		return err
	}
	_, err := db.DB.Exec(ctx, `UPDATE payment_event_outbox SET state='delivered',delivered_at=now(),lease_until=NULL,last_error=NULL WHERE event_id=$1 AND lease_token=$2`, job.id, job.lease)
	return err
}
