package partner

import (
	"bytes"
	"context"
	"errors"
	"regexp"
	"time"

	"conversion-backend/internal/delivery"
	"conversion-backend/internal/paymentpolicy"

	"github.com/jackc/pgx/v5"
)

var externalOrderID = regexp.MustCompile(`^[^\p{C}\s/]{1,128}$`)

func ValidOrderID(id string) bool {
	return id != "." && id != ".." && externalOrderID.MatchString(id)
}

const unversionedInboxDigest int16 = 1

type Receipt struct {
	IntegrationID int64
	SourceType    string
	SourceID      string
	OrderID       string
	AppID         string
	ReceivedAt    time.Time
	Payload       []byte
	Digest        []byte
	DigestVersion int16
	LegacyDigest  func() ([]byte, error)
	Quarantine    string
}

// StoreInboxTx records authenticated input. A matching retry shares the
// original receipt. A conflicting retry is retained independently.
func StoreInboxTx(ctx context.Context, tx pgx.Tx, r Receipt) (conflict bool, err error) {
	if r.Quarantine == "" {
		tag, err := tx.Exec(ctx, `
			INSERT INTO conversion.inbox(integration_id,source_event_type,source_event_id,source_order_id,source_app_id,received_at,payload,payload_digest,digest_version)
			VALUES($1,$2,$3,NULLIF($4,''),NULLIF($5,''),$6,$7,$8,$9)
			ON CONFLICT (integration_id,source_event_type,source_event_id) WHERE state<>'quarantined' DO NOTHING`,
			r.IntegrationID, r.SourceType, r.SourceID, r.OrderID, r.AppID, r.ReceivedAt, r.Payload, r.Digest, r.DigestVersion)
		if err != nil || tag.RowsAffected() == 1 {
			return false, err
		}
		// The new statement sees a concurrently committed first submission.
		var existing []byte
		var version int16
		if err := tx.QueryRow(ctx, `SELECT payload_digest,digest_version FROM conversion.inbox
			WHERE integration_id=$1 AND source_event_type=$2 AND source_event_id=$3 AND state<>'quarantined'`,
			r.IntegrationID, r.SourceType, r.SourceID).Scan(&existing, &version); err != nil {
			return false, err
		}
		// Before digests were versioned, one release used the full field set
		// and earlier releases omitted the partner's network aliases.
		if (version == r.DigestVersion || version == unversionedInboxDigest) && bytes.Equal(existing, r.Digest) {
			return false, nil
		}
		if version == unversionedInboxDigest && r.LegacyDigest != nil {
			legacy, err := r.LegacyDigest()
			if err != nil {
				return false, err
			}
			if bytes.Equal(existing, legacy) {
				return false, nil
			}
		}
		r.Quarantine = "source_event_conflict"
		conflict = true
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO conversion.inbox(integration_id,source_event_type,source_event_id,source_order_id,source_app_id,received_at,payload,payload_digest,digest_version,state,reason)
		VALUES($1,$2,NULLIF($3,''),NULLIF($4,''),NULLIF($5,''),$6,$7,$8,$9,'quarantined',$10)`,
		r.IntegrationID, r.SourceType, r.SourceID, r.OrderID, r.AppID, r.ReceivedAt, r.Payload, r.Digest, r.DigestVersion, r.Quarantine)
	return conflict, err
}

type InboxJob struct {
	ID            int64
	IntegrationID int64
	SourceType    string
	SourceID      string
	ReceivedAt    time.Time
	Payload       []byte
	Config        []byte
	Enabled       bool
	Contract      string
	Apps, Events  []string
}

type Normalized struct {
	Event      delivery.Event
	Targets    []delivery.Target
	Payment    *paymentpolicy.Facts
	Quarantine string
}

type NormalizeFunc func(context.Context, pgx.Tx, InboxJob) (Normalized, error)

var errInboxEmpty = errors.New("partner inbox empty")

// Consume normalizes each Inbox row in the same transaction that stores its
// event, routes, and consumed state.
func Consume(ctx context.Context, svc delivery.Service, source string, limit int, normalize NormalizeFunc) (int, error) {
	for count := 0; count < limit; count++ {
		err := pgx.BeginFunc(ctx, svc.Pool, func(tx pgx.Tx) error {
			var j InboxJob
			err := tx.QueryRow(ctx, `
				SELECT b.id,b.integration_id,b.source_event_type,b.source_event_id,b.received_at,b.payload,
				       i.report_config,i.enabled,i.contract_version,i.allowed_apps,i.allowed_events
				FROM conversion.inbox b JOIN conversion.integrations i ON i.id=b.integration_id
				WHERE b.state='pending' AND i.source=$2 AND i.environment=$1
				ORDER BY b.received_at,b.id LIMIT 1 FOR UPDATE OF b SKIP LOCKED FOR SHARE OF i`, svc.Environment, source).
				Scan(&j.ID, &j.IntegrationID, &j.SourceType, &j.SourceID, &j.ReceivedAt, &j.Payload,
					&j.Config, &j.Enabled, &j.Contract, &j.Apps, &j.Events)
			if errors.Is(err, pgx.ErrNoRows) {
				return errInboxEmpty
			}
			if err != nil {
				return err
			}
			out, err := normalize(ctx, tx, j)
			if err != nil {
				return err
			}
			if out.Quarantine != "" {
				_, err = tx.Exec(ctx, "UPDATE conversion.inbox SET state='quarantined',reason=$2 WHERE id=$1", j.ID, out.Quarantine)
				return err
			}
			var eventID int64
			var reason string
			if out.Payment != nil {
				eventID, reason, err = svc.EnqueuePartnerPaymentTx(ctx, tx, out.Event, out.Targets, *out.Payment)
			} else {
				eventID, err = svc.EnqueueTx(ctx, tx, out.Event, out.Targets)
			}
			if err != nil {
				return err
			}
			_, err = tx.Exec(ctx, `UPDATE conversion.inbox SET state='consumed',reason=NULLIF($2,''),normalized_event_id=$3
				WHERE id=$1`, j.ID, reason, eventID)
			return err
		})
		if errors.Is(err, errInboxEmpty) {
			return count, nil
		}
		if err != nil {
			return count, err
		}
	}
	return limit, nil
}
