package delivery

import (
	"context"
	"errors"
	"regexp"

	"github.com/jackc/pgx/v5"
)

var pixelPattern = regexp.MustCompile(`^[0-9]+$`)

type LegacySetupBatch struct {
	After   int64 `json:"after"`
	Scanned int   `json:"scanned"`
	Changed int   `json:"changed"`
	Skipped int   `json:"skipped"`
}

type legacyPixel struct {
	id    int64
	pixel string
	name  string
	token string
}

func (s Service) ImportLegacyPixels(ctx context.Context, after int64, limit int, keyVersion string,
	encrypt func(string, string) ([]byte, error),
) (LegacySetupBatch, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return LegacySetupBatch{}, err
	}
	defer tx.Rollback(ctx)
	integrationID, err := s.legacyIntegrationTx(ctx, tx)
	if err != nil {
		return LegacySetupBatch{}, err
	}
	pixels, err := listLegacyPixelsTx(ctx, tx, after, limit)
	if err != nil {
		return LegacySetupBatch{}, err
	}
	batch := LegacySetupBatch{After: after, Scanned: len(pixels)}
	for _, pixel := range pixels {
		batch.After = pixel.id
		if !pixelPattern.MatchString(pixel.pixel) {
			batch.Skipped++
			continue
		}
		changed, err := s.importLegacyPixelTx(ctx, tx, integrationID, pixel, keyVersion, encrypt)
		if err != nil {
			return LegacySetupBatch{}, err
		}
		if changed {
			batch.Changed++
		}
	}
	return batch, tx.Commit(ctx)
}

func listLegacyPixelsTx(ctx context.Context, tx pgx.Tx, after int64, limit int) ([]legacyPixel, error) {
	rows, err := tx.Query(ctx, `
		SELECT id,pixel_id,name,access_token FROM fb_pixels WHERE id > $1 ORDER BY id LIMIT $2 FOR SHARE`, after, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (legacyPixel, error) {
		var pixel legacyPixel
		err := row.Scan(&pixel.id, &pixel.pixel, &pixel.name, &pixel.token)
		return pixel, err
	})
}

func (s Service) importLegacyPixelTx(ctx context.Context, tx pgx.Tx, integrationID int64, pixel legacyPixel,
	keyVersion string, encrypt func(string, string) ([]byte, error),
) (bool, error) {
	var destinationID int64
	var legacyID, credentialID *int64
	err := tx.QueryRow(ctx, `
		SELECT id,legacy_fb_pixel_id,credential_id FROM conversion.destinations
		WHERE environment=$1 AND media='meta' AND pixel_id=$2 FOR UPDATE`, s.Environment, pixel.pixel).
		Scan(&destinationID, &legacyID, &credentialID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	if legacyID != nil && *legacyID != pixel.id {
		return false, errors.New("destination already mapped to a different legacy Pixel")
	}
	changed := legacyID == nil
	if credentialID == nil && pixel.token != "" {
		encrypted, err := encrypt(keyVersion, pixel.token)
		if err != nil {
			return false, err
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO conversion.credentials(ciphertext,key_version) VALUES($1,$2) RETURNING id`,
			encrypted, keyVersion).Scan(&credentialID); err != nil {
			return false, err
		}
		changed = true
	}
	if destinationID == 0 {
		err := tx.QueryRow(ctx, `
			INSERT INTO conversion.destinations(environment,media,pixel_id,display_name,credential_id,legacy_fb_pixel_id)
			VALUES($1,'meta',$2,$3,$4,$5) RETURNING id`, s.Environment, pixel.pixel, pixel.name, credentialID, pixel.id).
			Scan(&destinationID)
		if err != nil {
			return false, err
		}
	} else if changed {
		if _, err := tx.Exec(ctx, `
			UPDATE conversion.destinations SET legacy_fb_pixel_id=$2,credential_id=$3 WHERE id=$1`,
			destinationID, pixel.id, credentialID); err != nil {
			return false, err
		}
	}
	tag, err := tx.Exec(ctx, `
		INSERT INTO conversion.integration_destinations(integration_id,destination_id,environment)
		VALUES($1,$2,$3) ON CONFLICT (integration_id,destination_id) DO NOTHING`, integrationID, destinationID, s.Environment)
	return changed || tag.RowsAffected() == 1, err
}

func (s Service) BackfillLegacySubscriptions(ctx context.Context, after int64, limit int) (LegacySetupBatch, error) {
	var b LegacySetupBatch
	err := s.Pool.QueryRow(ctx, `
		WITH candidates AS (
			SELECT id,subscription_id,user_id,last_payment_time FROM user_subscriptions
			WHERE id>$1 AND payment_method='paypal' AND capi_subscribe_state='unknown'
			ORDER BY id LIMIT $2 FOR UPDATE
		), evidence AS (
			SELECT s.id, r.id AS original_order_id, s.last_payment_time IS NOT NULL AS fulfilled
			FROM candidates s LEFT JOIN payment_event_outbox o
			  ON o.event_id='subscription:'||s.subscription_id||':Subscribe' AND o.event_name='Subscribe'
			LEFT JOIN recharge_orders r ON r.id=o.order_id AND r.subscription_id=s.subscription_id
			  AND r.user_id=s.user_id AND r.order_type='subscription' AND r.payment_method='paypal'
		), changed AS (
			UPDATE user_subscriptions s
			SET capi_subscribe_state=CASE WHEN e.original_order_id IS NULL THEN 'historical' ELSE 'claimed' END,
			    capi_subscribe_order_id=e.original_order_id
			FROM evidence e WHERE s.id=e.id AND (e.original_order_id IS NOT NULL OR e.fulfilled)
			RETURNING s.id
		)
		SELECT COALESCE(max(id),$1),count(*)::int,(SELECT count(*)::int FROM changed)
		FROM candidates`, after, limit).Scan(&b.After, &b.Scanned, &b.Changed)
	b.Skipped = b.Scanned - b.Changed
	return b, err
}
