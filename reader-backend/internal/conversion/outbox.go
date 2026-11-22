// Package conversion writes immutable conversion snapshots in Reader transactions.
package conversion

import (
	"context"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
)

func Environment() string {
	if name := os.Getenv("CONVERSION_ENVIRONMENT"); name != "" {
		return name
	}
	return "production"
}

type Attribution struct {
	FBP       string `json:"fbp,omitempty"`
	FBC       string `json:"fbc,omitempty"`
	IP        string `json:"ip_address,omitempty"`
	UserAgent string `json:"user_agent,omitempty"`
	SourceURL string `json:"source_url,omitempty"`
	Country   string `json:"country,omitempty"`
}

func EnqueueRegistrationTx(ctx context.Context, tx pgx.Tx, userID int64, attribution Attribution, promotionHint bool) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO reader_conversion_outbox (business_key,event_name,environment,occurred_at,snapshot)
		SELECT 'registration:'||u.id||':CompleteRegistration','CompleteRegistration',$2,u.created_at,
			jsonb_build_object('user_id',u.id::text,'email',u.email,'promotion_id',p.id::text,'action_source','website',
				'attribution',$3::jsonb || jsonb_build_object('pixel_id',f.pixel_id),
				'attribution_state',CASE WHEN f.pixel_id IS NOT NULL THEN 'matched'
					WHEN p.id IS NOT NULL OR $4 OR COALESCE(u.utm_source,'')<>'' OR COALESCE(u.utm_campaign,'')<>''
					THEN 'missing_pixel' ELSE 'organic' END)
		FROM users u
		LEFT JOIN promotion_links p ON p.id=u.promotion_link_id
		LEFT JOIN fb_pixels f ON f.id=p.fb_pixel_id
		WHERE u.id=$1
		ON CONFLICT (business_key) DO NOTHING`, userID, Environment(), attribution, promotionHint || attribution.FBC != "")
	return err
}

func RecordPaymentTx(ctx context.Context, tx pgx.Tx, orderID int64, occurredAt time.Time, source string, subscribe, automatic bool) error {
	return recordPaymentTx(ctx, tx, orderID, occurredAt, source, Environment(), subscribe, automatic, nil)
}

func RecordSubscriptionPaymentTx(ctx context.Context, tx pgx.Tx, orderID int64, occurredAt time.Time, source, environment string, kind *string, subscribe, automatic bool) error {
	return recordPaymentTx(ctx, tx, orderID, occurredAt, source, environment, subscribe, automatic, kind)
}

func recordPaymentTx(ctx context.Context, tx pgx.Tx, orderID int64, occurredAt time.Time, source, environment string, subscribe, automatic bool, subscriptionKind *string) error {
	var happenedAt *time.Time
	if !occurredAt.IsZero() {
		happenedAt = &occurredAt
	} else {
		source = "missing"
	}
	_, err := tx.Exec(ctx, "SELECT record_reader_payment_conversion($1,$2,$3,$4,$5,$6,$7)",
		orderID, happenedAt, source, environment, subscribe, automatic, subscriptionKind)
	return err
}
