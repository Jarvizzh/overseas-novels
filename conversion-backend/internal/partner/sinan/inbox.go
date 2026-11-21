package sinan

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"slices"
	"strings"
	"time"

	"conversion-backend/internal/partner"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Decrypt func(version string, ciphertext []byte) (string, error)

type Inbox struct {
	Pool        *pgxpool.Pool
	Environment string
	Decrypt     Decrypt
}

const TestEnvironment = "test"

var eventNames = map[string]string{
	"attribution-succeeded": "CompleteRegistration",
	"order-created":         "InitiateCheckout",
	"recharge-paid":         "Purchase",
}

func (s Inbox) Handler() http.Handler {
	mux := http.NewServeMux()
	if s.Environment != TestEnvironment || s.Decrypt == nil {
		return mux
	}
	for eventType := range eventNames {
		mux.HandleFunc("POST /callbacks/sinan/{integration_key}/"+eventType, func(w http.ResponseWriter, r *http.Request) {
			s.receive(w, r, eventType)
		})
	}
	return mux
}

func (s Inbox) receive(w http.ResponseWriter, r *http.Request, eventType string) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		reply(w, http.StatusBadRequest, "invalid_content_type")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 32<<10))
	if err != nil {
		var sizeErr *http.MaxBytesError
		if errors.As(err, &sizeErr) {
			reply(w, http.StatusRequestEntityTooLarge, "body_too_large")
		} else {
			reply(w, http.StatusBadRequest, "invalid_body")
		}
		return
	}
	token, found := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	key := r.PathValue("integration_key")
	if !found || token == "" || len(key) > 100 {
		reply(w, http.StatusUnauthorized, "authentication_failed")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	status, message := s.accept(ctx, key, token, eventType, body)
	reply(w, status, message)
}

func reply(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	code := status
	if status == http.StatusOK {
		code = 0
	}
	_ = json.NewEncoder(w).Encode(struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}{code, message})
}

type inboxPayload struct {
	Callback      callback `json:"callback"`
	DestinationID int64    `json:"original_destination_id"`
}

func (s Inbox) accept(ctx context.Context, key, token, eventType string, body []byte) (int, string) {
	const unavailable = "inbox_unavailable"
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return http.StatusServiceUnavailable, unavailable
	}
	defer tx.Rollback(ctx)

	var id int64
	var enabled bool
	var apps, events []string
	var rawConfig, ciphertext []byte
	var version string
	var received time.Time
	err = tx.QueryRow(ctx, `
		SELECT i.id,i.enabled,i.allowed_apps,i.allowed_events,i.report_config,c.key_version,c.ciphertext,transaction_timestamp()
		FROM conversion.integrations i JOIN conversion.credentials c ON c.id=i.inbound_credential_id
		WHERE i.environment=$1 AND i.source='sinan' AND i.account_key=$2 AND i.contract_version=$3
		FOR SHARE OF i`, s.Environment, key, MockContract).Scan(&id, &enabled, &apps, &events, &rawConfig, &version, &ciphertext, &received)
	if errors.Is(err, pgx.ErrNoRows) {
		return http.StatusUnauthorized, "authentication_failed"
	}
	if err != nil {
		return http.StatusServiceUnavailable, unavailable
	}
	secret, err := s.Decrypt(version, ciphertext)
	if err != nil {
		return http.StatusServiceUnavailable, unavailable
	}
	given, expected := sha256.Sum256([]byte(token)), sha256.Sum256([]byte(secret))
	if subtle.ConstantTimeCompare(given[:], expected[:]) != 1 {
		return http.StatusUnauthorized, "authentication_failed"
	}
	if !enabled || !slices.Contains(events, eventType) {
		return http.StatusForbidden, "callback_not_authorized"
	}
	p, legacy, err := parseCallback(body)
	if err != nil {
		return http.StatusBadRequest, "invalid_payload"
	}
	var config Config
	if err := json.Unmarshal(rawConfig, &config); err != nil {
		return http.StatusServiceUnavailable, unavailable
	}
	if !slices.Contains(apps, string(p.AppID)) || !config.allows(string(p.AppID), string(p.PixelID)) {
		return http.StatusForbidden, "app_or_pixel_not_authorized"
	}
	var destination int64
	err = tx.QueryRow(ctx, `
		SELECT d.id FROM conversion.integration_destinations r JOIN conversion.destinations d ON d.id=r.destination_id
		WHERE r.integration_id=$1 AND r.environment=$2 AND d.pixel_id=$3 AND d.media='meta'`,
		id, s.Environment, p.PixelID).Scan(&destination)
	if errors.Is(err, pgx.ErrNoRows) {
		return http.StatusForbidden, "app_or_pixel_not_authorized"
	}
	if err != nil {
		return http.StatusServiceUnavailable, unavailable
	}

	digest, err := callbackDigest(p)
	if err != nil {
		return http.StatusBadRequest, "invalid_payload"
	}
	reason := p.invalidReason(received, eventType)
	p.sanitize(config)
	snapshot, err := json.Marshal(inboxPayload{Callback: p, DestinationID: destination})
	if err != nil {
		return http.StatusBadRequest, "invalid_payload"
	}
	receipt := partner.Receipt{
		IntegrationID: id, SourceType: eventType, SourceID: p.EventID, OrderID: string(p.OrderID), AppID: string(p.AppID),
		ReceivedAt: received, Payload: snapshot, Digest: digest, DigestVersion: 2, Quarantine: reason,
		LegacyDigest: func() ([]byte, error) { return callbackDigest(legacy) },
	}
	if reason == "order_id_invalid" {
		receipt.OrderID, receipt.Payload = "", nil
	}
	if reason == "event_id_invalid" {
		// Invalid identifiers may contain control characters or arbitrary data.
		receipt.SourceID, receipt.Payload = "", nil
	}
	conflict, err := partner.StoreInboxTx(ctx, tx, receipt)
	if err != nil || tx.Commit(ctx) != nil {
		return http.StatusServiceUnavailable, unavailable
	}
	if conflict {
		return http.StatusConflict, "source_event_conflict"
	}
	if reason != "" {
		return http.StatusUnprocessableEntity, reason
	}
	return http.StatusOK, "success"
}

func callbackDigest(p callback) ([]byte, error) {
	canonical, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(canonical)
	return digest[:], nil
}
