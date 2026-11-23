package delivery

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type DestinationChange struct {
	PixelID string
	Name    *string
	Token   string
	Retire  bool
	Reason  string
}

type EncryptCredential func(string) ([]byte, string, error)

func (s Service) ConfigureDestination(ctx context.Context, actor Operator, id int64, input DestinationChange, encrypt EncryptCredential) (OperationResult, error) {
	if id < 0 || (id == 0 && (!pixelPattern.MatchString(input.PixelID) || len(input.PixelID) > 50 ||
		input.Name == nil || input.Token == "" || input.Retire)) || (id > 0 && input.PixelID != "") {
		return OperationResult{}, ErrInvalidOperation
	}
	if input.Name != nil {
		name := strings.TrimSpace(*input.Name)
		if name == "" || len([]rune(name)) > 255 || strings.ContainsRune(name, 0) {
			return OperationResult{}, ErrInvalidOperation
		}
		input.Name = &name
	}
	if len(input.Token) > 4096 || strings.ContainsAny(input.Token, "\r\n\x00") ||
		(id > 0 && input.Name == nil && input.Token == "" && !input.Retire) {
		return OperationResult{}, ErrInvalidOperation
	}
	var cipher []byte
	var keyVersion string
	if input.Token != "" {
		if encrypt == nil {
			return OperationResult{}, errors.New("credential key version unavailable")
		}
		var err error
		cipher, keyVersion, err = encrypt(input.Token)
		if err != nil {
			return OperationResult{}, err
		}
	}
	return s.operate(ctx, actor, input.Reason, func(tx pgx.Tx, operator Operator, reason string) (OperationResult, error) {
		// Keep the lock order shared with the historical Pixel importer.
		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(1930717)"); err != nil {
			return OperationResult{}, err
		}
		if id == 0 {
			return s.createDestinationTx(ctx, tx, operator, input, reason, cipher, keyVersion)
		}
		var current destinationConfig
		err := tx.QueryRow(ctx, `SELECT pixel_id,display_name,legacy_fb_pixel_id,credential_id,retired_at
			FROM conversion.destinations WHERE environment=$1 AND media='meta' AND id=$2 FOR UPDATE`, s.Environment, id).
			Scan(&current.pixel, &current.name, &current.legacyID, &current.credential, &current.retired)
		if err != nil {
			return OperationResult{}, err
		}
		if input.Retire && current.retired == nil && current.legacyID != nil {
			var referenced bool
			if err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM promotion_links WHERE fb_pixel_id=$1)", *current.legacyID).Scan(&referenced); err != nil {
				return OperationResult{}, err
			}
			if referenced {
				return OperationResult{}, ErrOperationConflict
			}
		}
		state := current
		if input.Name != nil {
			state.name = *input.Name
		}
		if len(cipher) > 0 {
			credential, err := insertCredential(ctx, tx, cipher, keyVersion)
			if err != nil {
				return OperationResult{}, err
			}
			state.credential = &credential
		}
		update, err := tx.Exec(ctx, `UPDATE conversion.destinations SET display_name=$2,credential_id=$3,
            blocked_credential_id=CASE WHEN credential_id IS DISTINCT FROM $3 THEN NULL ELSE blocked_credential_id END,
			retired_at=CASE WHEN $4 THEN COALESCE(retired_at,clock_timestamp()) ELSE retired_at END
			WHERE id=$1`, id, state.name, state.credential, input.Retire)
		if err := requireOne(update.RowsAffected(), err); err != nil {
			return OperationResult{}, err
		}
		if current.legacyID != nil && state.name != current.name {
			if _, err = tx.Exec(ctx, "UPDATE fb_pixels SET name=$2 WHERE id=$1", *current.legacyID, state.name); err != nil {
				return OperationResult{}, err
			}
		}
		out := OperationResult{ID: strconv.FormatInt(id, 10), Action: "configure_destination"}
		err = s.audit(ctx, tx, operator, out.Action, out.ID, reason, "", current.audit(),
			map[string]any{"pixel_id": state.pixel, "display_name": state.name, "retired": input.Retire || state.retired != nil,
				"credential_configured": state.credential != nil, "credential_rotated": len(cipher) > 0})
		return out, err
	})
}

type destinationConfig struct {
	pixel, name          string
	legacyID, credential *int64
	retired              *time.Time
}

func (d destinationConfig) audit() map[string]any {
	return map[string]any{"pixel_id": d.pixel, "display_name": d.name, "retired": d.retired != nil, "credential_configured": d.credential != nil}
}

func (s Service) createDestinationTx(ctx context.Context, tx pgx.Tx, actor Operator, input DestinationChange, reason string, cipher []byte, keyVersion string) (OperationResult, error) {
	var legacyID int64
	err := tx.QueryRow(ctx, `INSERT INTO fb_pixels(name,pixel_id,access_token) VALUES($1,$2,'')
		ON CONFLICT(pixel_id) DO NOTHING RETURNING id`, *input.Name, input.PixelID).Scan(&legacyID)
	if errors.Is(err, pgx.ErrNoRows) {
		return OperationResult{}, ErrOperationConflict
	}
	if err != nil {
		return OperationResult{}, err
	}
	credential, err := insertCredential(ctx, tx, cipher, keyVersion)
	if err != nil {
		return OperationResult{}, err
	}
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO conversion.destinations(environment,media,pixel_id,display_name,credential_id,legacy_fb_pixel_id)
		VALUES($1,'meta',$2,$3,$4,$5) RETURNING id`, s.Environment, input.PixelID, *input.Name, credential, legacyID).Scan(&id)
	if err != nil {
		return OperationResult{}, configConflict(err)
	}
	// Reader promotions use the associated legacy ID as the stable reference.
	integration, err := s.legacyIntegrationTx(ctx, tx)
	if err != nil {
		return OperationResult{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO conversion.integration_destinations(integration_id,destination_id,environment)
		VALUES($1,$2,$3)`, integration, id, s.Environment); err != nil {
		return OperationResult{}, err
	}
	out := OperationResult{ID: strconv.FormatInt(id, 10), Action: "configure_destination"}
	err = s.audit(ctx, tx, actor, out.Action, out.ID, reason, "", map[string]any{}, destinationConfig{
		pixel: input.PixelID, name: *input.Name, credential: &credential}.audit())
	return out, err
}

func insertCredential(ctx context.Context, tx pgx.Tx, cipher []byte, version string) (int64, error) {
	var id int64
	err := tx.QueryRow(ctx, "INSERT INTO conversion.credentials(ciphertext,key_version) VALUES($1,$2) RETURNING id", cipher, version).Scan(&id)
	return id, err
}

var (
	ErrSinanAppsRequired   = fmt.Errorf("sinan_apps_required: %w", ErrInvalidOperation)
	ErrSinanEventsRequired = fmt.Errorf("sinan_events_required: %w", ErrInvalidOperation)
)

type IntegrationChange struct {
	Source, AccountKey, ContractVersion, Reason string
	Enabled                                     *bool
	AllowedApps, AllowedEvents                  *[]string
	Bindings                                    []IntegrationBinding
}

type IntegrationBinding struct {
	DestinationID int64
	Additional    bool
	Remove        bool
}

func validConfigField(value string, maximum int) bool {
	return strings.TrimSpace(value) == value && value != "" && len(value) <= maximum && !strings.ContainsAny(value, "\r\n\x00")
}

func (s Service) ConfigureIntegration(ctx context.Context, actor Operator, id int64, input IntegrationChange) (OperationResult, error) {
	if id < 0 || (id == 0 && (!validConfigField(input.Source, 50) || !validConfigField(input.AccountKey, 100) ||
		!validConfigField(input.ContractVersion, 100) || (input.Source == "starnovel" && input.AccountKey != "reader"))) ||
		(id > 0 && (input.Source != "" || input.AccountKey != "")) || len(input.Bindings) > 100 {
		return OperationResult{}, ErrInvalidOperation
	}
	for _, fields := range []*[]string{input.AllowedApps, input.AllowedEvents} {
		if fields == nil {
			continue
		}
		if len(*fields) > 100 {
			return OperationResult{}, ErrInvalidOperation
		}
		for _, field := range *fields {
			if !validConfigField(field, 128) {
				return OperationResult{}, ErrInvalidOperation
			}
		}
	}
	if input.ContractVersion != "" && !validConfigField(input.ContractVersion, 100) {
		return OperationResult{}, ErrInvalidOperation
	}
	return s.operate(ctx, actor, input.Reason, func(tx pgx.Tx, operator Operator, reason string) (OperationResult, error) {
		var before map[string]any
		if id == 0 {
			err := tx.QueryRow(ctx, `INSERT INTO conversion.integrations(source,account_key,environment,contract_version)
				VALUES($1,$2,$3,$4) RETURNING id`, input.Source, input.AccountKey, s.Environment, input.ContractVersion).Scan(&id)
			if err != nil {
				return OperationResult{}, configConflict(err)
			}
			before = map[string]any{}
		} else {
			if err := tx.QueryRow(ctx, `SELECT jsonb_build_object('enabled',enabled,'allowed_apps',allowed_apps,
				'allowed_events',allowed_events,'contract_version',contract_version)
				FROM conversion.integrations WHERE id=$1 AND environment=$2 FOR UPDATE`, id, s.Environment).Scan(&before); err != nil {
				return OperationResult{}, err
			}
		}
		var after map[string]any
		var source string
		var enabled bool
		var apps, events []string
		err := tx.QueryRow(ctx, `UPDATE conversion.integrations SET enabled=COALESCE($2,enabled),
			allowed_apps=COALESCE($3,allowed_apps),allowed_events=COALESCE($4,allowed_events),
			contract_version=COALESCE(NULLIF($5,''),contract_version)
			WHERE id=$1 RETURNING source,enabled,allowed_apps,allowed_events,jsonb_build_object('enabled',enabled,'allowed_apps',allowed_apps,
				'allowed_events',allowed_events,'contract_version',contract_version)`,
			id, input.Enabled, input.AllowedApps, input.AllowedEvents, input.ContractVersion).Scan(&source, &enabled, &apps, &events, &after)
		if err != nil {
			return OperationResult{}, err
		}
		if source == "sinan" && enabled {
			if len(apps) == 0 {
				return OperationResult{}, ErrSinanAppsRequired
			}
			if len(events) == 0 {
				return OperationResult{}, ErrSinanEventsRequired
			}
		}
		beforeBindings := make(map[string]any)
		afterBindings := make(map[string]any)
		for _, binding := range input.Bindings {
			if binding.DestinationID <= 0 {
				return OperationResult{}, ErrInvalidOperation
			}
			subject := strconv.FormatInt(binding.DestinationID, 10)
			if _, seen := beforeBindings[subject]; seen {
				return OperationResult{}, ErrInvalidOperation
			}
			old, err := s.applyBinding(ctx, tx, id, binding)
			if err != nil {
				return OperationResult{}, err
			}
			beforeBindings[subject] = old
			if binding.Remove {
				afterBindings[subject] = nil
			} else {
				afterBindings[subject] = binding.Additional
			}
		}
		before["bindings"], after["bindings"] = beforeBindings, afterBindings
		out := OperationResult{ID: strconv.FormatInt(id, 10), Action: "configure_integration"}
		err = s.audit(ctx, tx, operator, out.Action, out.ID, reason, "", before, after)
		return out, err
	})
}

func (s Service) applyBinding(ctx context.Context, tx pgx.Tx, id int64, b IntegrationBinding) (*bool, error) {
	var previous *bool
	err := tx.QueryRow(ctx, `SELECT default_route FROM conversion.integration_destinations WHERE integration_id=$1 AND destination_id=$2
		FOR UPDATE`, id, b.DestinationID).Scan(&previous)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if b.Remove {
		_, err = tx.Exec(ctx, "DELETE FROM conversion.integration_destinations WHERE integration_id=$1 AND destination_id=$2", id, b.DestinationID)
		return previous, err
	}
	var destination int64
	err = tx.QueryRow(ctx, `SELECT id FROM conversion.destinations WHERE id=$1 AND environment=$2 AND enabled
		AND retired_at IS NULL FOR SHARE`, b.DestinationID, s.Environment).Scan(&destination)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrOperationConflict
	}
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO conversion.integration_destinations(integration_id,destination_id,environment,default_route)
		VALUES($1,$2,$3,$4) ON CONFLICT(integration_id,destination_id) DO UPDATE SET default_route=excluded.default_route`,
		id, destination, s.Environment, b.Additional)
	return previous, err
}

func configConflict(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" {
		return ErrOperationConflict
	}
	return err
}
