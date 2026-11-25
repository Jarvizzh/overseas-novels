package application

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"reader-backend/internal/db"
	"reader-backend/internal/payment"
)

func saveUnprocessedPaymentEvent(ctx context.Context, accountID int64, eventID string, raw json.RawMessage, cause error) error {
	if eventID == "" || !json.Valid(raw) {
		return errors.New("recovery cannot identify the failed event")
	}
	tag, err := db.Pool(ctx).Exec(ctx, `INSERT INTO payment_event_recovery_failures(account_id,event_id,event,last_error)
		VALUES($1,$2,$3,left($4,500)) ON CONFLICT DO NOTHING`, accountID, eventID, raw, cause.Error())
	if err == nil && tag.RowsAffected() == 1 {
		log.Printf("task=payment_event_recovery action=held account_id=%d event=%s error=%v", accountID, eventID, cause)
	}
	return err
}

// ScheduleRecoveredPaymentEvent retries one held event without rewinding the
// discovery cursor or altering its original evidence.
func ScheduleRecoveredPaymentEvent(ctx context.Context, provider string, account payment.WebhookAccount, id string) error {
	tag, err := db.Pool(ctx).Exec(ctx, `UPDATE payment_event_recovery_failures f SET next_attempt_at=now()
		FROM payment_channel_accounts a WHERE a.id=f.account_id AND a.provider=$1 AND a.account_key=$2 AND a.environment=$3
		AND f.event_id=$4 AND f.resolved_at IS NULL AND (f.lease_until IS NULL OR f.lease_until<=now())`,
		provider, account.AccountKey, account.Environment, id)
	if err == nil && tag.RowsAffected() != 1 {
		return ErrTaskNotRetryable
	}
	return err
}

func (s *service) retryRecoveredEvents(ctx context.Context, provider string, account payment.WebhookAccount,
	recoverEvent func(context.Context, json.RawMessage) (review, transient error)) {
	var accountID int64
	var eventID string
	var raw json.RawMessage
	token := uuid.New()
	pool := db.Pool(ctx)
	err := pool.QueryRow(ctx, `WITH due AS (
		SELECT f.account_id,f.event_id FROM payment_event_recovery_failures f JOIN payment_channel_accounts a ON a.id=f.account_id
		WHERE a.provider=$1 AND a.account_key=$2 AND a.environment=$3 AND f.resolved_at IS NULL
		AND f.next_attempt_at<=now() AND (f.lease_until IS NULL OR f.lease_until<=now())
		ORDER BY f.next_attempt_at,f.event_id LIMIT 1 FOR UPDATE OF f SKIP LOCKED
	) UPDATE payment_event_recovery_failures f SET lease_token=$4,lease_until=now()+interval '2 minutes'
	FROM due WHERE f.account_id=due.account_id AND f.event_id=due.event_id RETURNING f.account_id,f.event_id,f.event`,
		provider, account.AccountKey, account.Environment, token).Scan(&accountID, &eventID, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return
	}
	if err != nil {
		log.Printf("task=payment_event_retry provider=%s action=claim error=%v", provider, err)
		return
	}
	request, cancel := context.WithTimeout(ctx, 45*time.Second)
	review, transient := recoverEvent(request, raw)
	cancel()
	result := errors.Join(review, transient)
	message := ""
	if result != nil {
		message = result.Error()
	}
	_, err = pool.Exec(ctx, `UPDATE payment_event_recovery_failures SET lease_token=NULL,lease_until=NULL,
		next_attempt_at=CASE WHEN $5 THEN now()+interval '1 minute' END,resolved_at=CASE WHEN $4='' THEN now() END,
		last_error=CASE WHEN $4='' THEN last_error ELSE left($4,500) END WHERE account_id=$1 AND event_id=$2 AND lease_token=$3`,
		accountID, eventID, token, message, transient != nil)
	if err != nil {
		log.Printf("task=payment_event_retry provider=%s event=%s action=finish error=%v", provider, eventID, err)
	}
}
