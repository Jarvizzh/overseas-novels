package operations

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"conversion-backend/internal/delivery"
	"conversion-backend/internal/partner/sinan"
	"conversion-backend/internal/paymentpolicy"

	"github.com/jackc/pgx/v5"
)

func (repo Repository) registerConfiguration(mux *http.ServeMux, svc delivery.Service) {
	mux.HandleFunc("GET /internal/conversion/destinations", repo.configurationList(destinationsSQL))
	mux.HandleFunc("GET /internal/conversion/integrations", repo.configurationList(integrationsSQL))
	mux.HandleFunc("GET /internal/conversion/audit", repo.handleAudit)
	destination := func(w http.ResponseWriter, r *http.Request) {
		id, ok := configurationID(r)
		var input struct {
			PixelID string  `json:"pixel_id"`
			Name    *string `json:"display_name"`
			Token   string  `json:"access_token"`
			Retire  bool    `json:"retire"`
			Reason  string  `json:"reason"`
		}
		if !ok || !decodeConfiguration(w, r, &input) {
			writeError(w, http.StatusBadRequest, "invalid_operation")
			return
		}
		out, err := svc.ConfigureDestination(r.Context(), operatorFrom(r), id, delivery.DestinationChange{
			PixelID: input.PixelID, Name: input.Name, Token: input.Token, Retire: input.Retire, Reason: input.Reason,
		}, repo.EncryptCredential)
		writeConfiguration(w, out, err, id == 0)
	}
	integration := func(w http.ResponseWriter, r *http.Request) {
		id, ok := configurationID(r)
		var input struct {
			Source     string    `json:"source"`
			AccountKey string    `json:"account_key"`
			Version    string    `json:"contract_version"`
			Reason     string    `json:"reason"`
			Enabled    *bool     `json:"enabled"`
			Apps       *[]string `json:"allowed_apps"`
			Events     *[]string `json:"allowed_events"`
			Bindings   []struct {
				Destination string `json:"destination_id"`
				Additional  bool   `json:"additional"`
				Remove      bool   `json:"remove"`
			} `json:"bindings"`
		}
		if !ok || !decodeConfiguration(w, r, &input) {
			writeError(w, http.StatusBadRequest, "invalid_operation")
			return
		}
		change := delivery.IntegrationChange{Source: input.Source, AccountKey: input.AccountKey, ContractVersion: input.Version,
			Reason: input.Reason, Enabled: input.Enabled, AllowedApps: input.Apps, AllowedEvents: input.Events}
		for _, item := range input.Bindings {
			id, ok := positiveID(item.Destination)
			if !ok {
				writeError(w, http.StatusBadRequest, "invalid_operation")
				return
			}
			change.Bindings = append(change.Bindings, delivery.IntegrationBinding{DestinationID: id, Additional: item.Additional, Remove: item.Remove})
		}
		out, err := svc.ConfigureIntegration(r.Context(), operatorFrom(r), id, change)
		writeConfiguration(w, out, err, id == 0)
	}
	mux.HandleFunc("POST /internal/conversion/destinations", destination)
	mux.HandleFunc("PATCH /internal/conversion/destinations/{id}", destination)
	mux.HandleFunc("POST /internal/conversion/integrations", integration)
	mux.HandleFunc("PATCH /internal/conversion/integrations/{id}", integration)
	if repo.Environment == sinan.TestEnvironment {
		mux.HandleFunc("PUT /internal/conversion/integrations/{id}/sinan-mock", repo.configureSinanMock(svc))
	}
}

func (repo Repository) configureSinanMock(svc delivery.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := configurationID(r)
		var input struct {
			Config       sinan.Config `json:"config"`
			InboundToken string       `json:"inbound_token"`
			SigningSeed  string       `json:"signing_seed"`
			Reason       string       `json:"reason"`
		}
		if !ok || !decodeConfiguration(w, r, &input) || !input.Config.Validate() ||
			!sinan.ValidMockCredentials(input.InboundToken, input.SigningSeed) {
			writeError(w, http.StatusBadRequest, "invalid_operation")
			return
		}
		config, err := json.Marshal(input.Config)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_operation")
			return
		}
		out, err := svc.ConfigureSinanMock(r.Context(), operatorFrom(r), id, input.Reason, delivery.MockPartnerSettings{
			Config: config, InboundToken: input.InboundToken, SigningSeed: input.SigningSeed,
		}, repo.EncryptCredential)
		writeConfiguration(w, out, err, false)
	}
}

func (repo Repository) configurationList(sql string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var out json.RawMessage
		if err := repo.Pool.QueryRow(r.Context(), sql, repo.Environment).Scan(&out); err != nil {
			writeError(w, http.StatusServiceUnavailable, "operation_unavailable")
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
}

func (repo Repository) handleAudit(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var actions []string
	switch q.Get("subject_type") {
	case "destination":
		actions = []string{"configure_destination"}
	case "integration":
		actions = []string{"configure_integration", "configure_sinan_mock", "publish_payment_policy", "inherit_payment_policy"}
	default:
		writeError(w, http.StatusBadRequest, "invalid_filter")
		return
	}
	subjectID, ok := positiveID(q.Get("subject_id"))
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_id")
		return
	}
	if q.Has("publication_id") {
		repo.handlePublicationAudit(w, r, subjectID)
		return
	}
	limit := 20
	if raw := q.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 50 {
			writeError(w, http.StatusBadRequest, "invalid_limit")
			return
		}
		limit = parsed
	}
	var beforeID int64
	if raw := q.Get("before_id"); raw != "" {
		parsed, valid := positiveID(raw)
		if !valid {
			writeError(w, http.StatusBadRequest, "invalid_id")
			return
		}
		beforeID = parsed
	}
	var out json.RawMessage
	err := repo.Pool.QueryRow(r.Context(), auditListSQL, repo.Environment, actions, strconv.FormatInt(subjectID, 10), beforeID, limit).Scan(&out)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "operation_unavailable")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type publicationAuditRecord struct {
	Action        string  `json:"action"`
	VersionID     *string `json:"version_id"`
	VersionNumber *string `json:"version_number"`
}

type publicationAuditResult struct {
	PublicationID string                  `json:"publication_id"`
	Record        *publicationAuditRecord `json:"record"`
	Current       *paymentpolicy.View     `json:"current"`
	CurrentStatus string                  `json:"current_status"`
}

func (repo Repository) handlePublicationAudit(w http.ResponseWriter, r *http.Request, integrationID int64) {
	q := r.URL.Query()
	publicationID, app, promotion := q.Get("publication_id"), q.Get("app_id"), q.Get("promotion_id")
	_, validApp := positiveID(app)
	_, validPromotion := positiveID(promotion)
	if q.Get("subject_type") != "integration" || !paymentpolicy.ValidPublicationID(publicationID) ||
		(app != paymentpolicy.ReaderApp && !validApp) || (promotion != "" && !validPromotion) {
		writeError(w, http.StatusBadRequest, "invalid_filter")
		return
	}
	ctx := r.Context()
	out := publicationAuditResult{PublicationID: publicationID}
	err := pgx.BeginFunc(ctx, repo.Pool, func(tx pgx.Tx) error {
		// Wait for an in-flight publication holding this integration's write lock.
		// The following statement reads the audit with a fresh snapshot.
		var source string
		if err := tx.QueryRow(ctx, "SELECT source FROM conversion.integrations WHERE id=$1 AND environment=$2 FOR SHARE",
			integrationID, repo.Environment).Scan(&source); err != nil {
			return err
		}
		var record publicationAuditRecord
		err := tx.QueryRow(ctx, publicationAuditSQL, repo.Environment, strconv.FormatInt(integrationID, 10),
			operatorFrom(r).ID, publicationID, app, promotion).Scan(&record.Action, &record.VersionID, &record.VersionNumber)
		switch {
		case err == nil:
			out.Record = &record
		case !errors.Is(err, pgx.ErrNoRows):
			return err
		}
		// Read the effective version under the same lock so a missing audit
		// cannot be paired with a version committed after the lookup.
		current, err := paymentpolicy.ReadTx(ctx, tx, paymentpolicy.Scope{
			Environment: repo.Environment, IntegrationID: integrationID, App: app, Promotion: promotion,
		})
		var invalid *paymentpolicy.ValidationError
		switch {
		case err == nil:
			out.Current = &current
			out.CurrentStatus = "available"
		case source == "sinan" && validApp && errors.As(err, &invalid) && invalid.Code == "invalid_payment_policy_app":
			out.CurrentStatus = "app_unauthorized"
		default:
			return err
		}
		return nil
	})
	writePolicy(w, out, err)
}

const publicationAuditSQL = `
	SELECT action,
		COALESCE(after_state#>>'{own_version,id}',after_state#>>'{inherited_version,id}'),
		COALESCE(after_state#>>'{own_version,number}',after_state#>>'{inherited_version,number}')
	FROM conversion.operation_audit
	WHERE environment=$1 AND subject_id=$2 AND operator_id=$3
	  AND action IN ('publish_payment_policy','inherit_payment_policy')
	  AND after_state ? 'publication_id' AND after_state->>'publication_id'=$4
	  AND after_state->>'app_id'=$5 AND COALESCE(after_state->>'promotion_id','')=$6
	ORDER BY id DESC LIMIT 1
`

const auditListSQL = `
	WITH matched AS (
		SELECT id,action,subject_id,operator_id,operator_role,reason,evidence_reference,
			before_state,after_state,created_at
		FROM conversion.operation_audit
		WHERE environment=$1
			AND action=ANY($2::text[])
			AND subject_id=$3
			AND ($4::bigint=0 OR id<$4)
		ORDER BY id DESC
		LIMIT $5::integer+1
	), page AS (
		SELECT * FROM matched ORDER BY id DESC LIMIT $5
	)
	SELECT jsonb_build_object(
		'records',COALESCE((SELECT jsonb_agg(jsonb_strip_nulls(jsonb_build_object(
			'id',id::text,'action',action,'subject_id',subject_id,
			'operator_id',operator_id,'operator_role',operator_role,
			'reason',reason,'evidence_reference',evidence_reference,
			'before_state',before_state,'after_state',after_state,
			'created_at',created_at)) ORDER BY id DESC) FROM page),'[]'),
		'has_more',(SELECT count(*) FROM matched)>$5
	)
`

const destinationsSQL = `
	SELECT COALESCE(jsonb_agg(jsonb_strip_nulls(jsonb_build_object('id',x.id::text,'pixel_id',x.pixel_id,
		'name',x.display_name,'enabled',x.enabled,'retired_at',x.retired_at,
		'credential_configured',x.credential_id IS NOT NULL,'promotion_dependencies',
		(SELECT count(*) FROM promotion_links p WHERE p.fb_pixel_id=x.legacy_fb_pixel_id)))
		ORDER BY x.id DESC),'[]')
	FROM conversion.destinations x WHERE x.environment=$1 AND x.media='meta'
`
const integrationsSQL = `
	SELECT COALESCE(jsonb_agg(jsonb_build_object('id',i.id::text,'source',i.source,'account_key',i.account_key,
		'enabled',i.enabled,'contract_version',i.contract_version,'allowed_apps',i.allowed_apps,'allowed_events',i.allowed_events,
		'bindings',COALESCE((SELECT jsonb_agg(jsonb_build_object('destination_id',x.id::text,'pixel_id',x.pixel_id,
			'additional',r.default_route,'retired',x.retired_at IS NOT NULL) ORDER BY x.id)
			FROM conversion.integration_destinations r JOIN conversion.destinations x ON x.id=r.destination_id
			WHERE r.integration_id=i.id),'[]')) ORDER BY i.id),'[]')
	FROM conversion.integrations i WHERE i.environment=$1
`

func configurationID(r *http.Request) (int64, bool) {
	if r.Method == http.MethodPost {
		return 0, true
	}
	return positiveID(r.PathValue("id"))
}

func operatorFrom(r *http.Request) delivery.Operator {
	return delivery.Operator{ID: r.Header.Get("X-Operator-ID"), Role: r.Header.Get("X-Operator-Role")}
}

func decodeConfiguration(w http.ResponseWriter, r *http.Request, value any) bool {
	return decodeConfigurationBody(w, r, value, 8<<10)
}

func decodeConfigurationBody(w http.ResponseWriter, r *http.Request, value any, limit int64) bool {
	reader := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	reader.DisallowUnknownFields()
	return reader.Decode(value) == nil && errors.Is(reader.Decode(new(any)), io.EOF)
}

func writeConfiguration(w http.ResponseWriter, result delivery.OperationResult, err error, created bool) {
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeOperation(w, result, err, status)
}

func writeOperation(w http.ResponseWriter, result delivery.OperationResult, err error, status int) {
	switch {
	case errors.Is(err, delivery.ErrInvalidOperation):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, delivery.ErrOperatorForbidden):
		writeError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, pgx.ErrNoRows):
		writeError(w, http.StatusNotFound, "resource_not_found")
	case errors.Is(err, delivery.ErrOperationConflict):
		writeError(w, http.StatusConflict, err.Error())
	case err != nil:
		writeError(w, http.StatusServiceUnavailable, "operation_unavailable")
	default:
		if result.Existing {
			status = http.StatusOK
		}
		writeJSON(w, status, result)
	}
}
