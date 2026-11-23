package operations

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"conversion-backend/internal/delivery"

	"github.com/jackc/pgx/v5"
)

func NewHandler(repo Repository, token string) http.Handler {
	mux := http.NewServeMux()
	svc := queueActions{service: delivery.Service{Store: delivery.Store{Pool: repo.Pool, Environment: repo.Environment}}}
	repo.registerConfiguration(mux, svc.service)
	repo.registerPaymentPolicy(mux, svc.service)
	mux.HandleFunc("POST /internal/conversion/events/{id}/deliveries", handleAction(svc.append, http.StatusCreated))
	mux.HandleFunc("POST /internal/conversion/deliveries/{id}/retry", handleAction(svc.retry, http.StatusAccepted))
	mux.HandleFunc("POST /internal/conversion/partner-reports/{id}/retry", handleAction(svc.retryReport, http.StatusAccepted))
	mux.HandleFunc("GET /internal/conversion/summary", repo.handleSummary)
	mux.HandleFunc("GET /internal/conversion/throughput", repo.handleThroughput)
	mux.HandleFunc("GET /internal/conversion/events", repo.handleEvents)
	mux.HandleFunc("GET /internal/conversion/events/{id}", repo.handleEvent)
	mux.HandleFunc("GET /internal/conversion/events/{id}/destinations", repo.handleAllowedTargets)
	mux.HandleFunc("GET /internal/conversion/events/{id}/deliveries", repo.handleEventDeliveries)
	mux.HandleFunc("GET /internal/conversion/orders/{order_id}", func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		key, ok := ParseOrderKey(r.PathValue("order_id"), query.Get("integration_id"), query.Get("app_id"))
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid_order_key")
			return
		}
		result, err := repo.Order(r.Context(), key)
		var ambiguous *orderAppAmbiguity
		if errors.As(err, &ambiguous) {
			writeJSON(w, http.StatusConflict, map[string]any{"code": ambiguous.Error(), "app_ids": ambiguous.Apps})
			return
		}
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "order_not_found")
			return
		}
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "reconciliation_unavailable")
			return
		}
		writeJSON(w, http.StatusOK, result)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
			writeError(w, http.StatusUnauthorized, "service_auth_required")
			return
		}
		role := r.Header.Get("X-Operator-Role")
		if r.Header.Get("X-Operator-ID") == "" || ((role != "Admin" && role != "SuperAdmin") &&
			(r.Method != http.MethodGet || (role != "MediaBuyer" && role != "Finance"))) {
			writeError(w, http.StatusForbidden, "operator_forbidden")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
		defer cancel()
		mux.ServeHTTP(w, r.WithContext(ctx))
	})
}

func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"code": code})
}

func writeJSON(w http.ResponseWriter, status int, result any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(result)
}

func (repo Repository) handleAllowedTargets(w http.ResponseWriter, r *http.Request) {
	id, ok := positiveID(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_operation")
		return
	}
	var result json.RawMessage
	err := repo.Pool.QueryRow(r.Context(), `
		SELECT COALESCE((SELECT jsonb_agg(jsonb_build_object('id',x.id::text,'pixel_id',x.pixel_id,'display_name',x.display_name)
		                            ORDER BY x.id)
		   FROM conversion.integration_destinations d JOIN conversion.destinations x ON x.id=d.destination_id
		   WHERE d.integration_id=e.integration_id AND d.environment=e.environment AND x.enabled AND x.retired_at IS NULL
		   AND COALESCE(conversion.sinan_route_issue(e.id,x.id),'')=''
		   AND NOT EXISTS (SELECT 1 FROM conversion.deliveries j WHERE j.event_id=e.id AND j.destination_id=x.id)),'[]')
		FROM conversion.events e WHERE e.id=$1 AND e.environment=$2
		  AND e.reason IS DISTINCT FROM 'event_not_allowed'
          AND (e.payment_policy_state IN ('historical','not_applicable') OR
		      (e.payment_policy_state='decided' AND EXISTS (SELECT 1 FROM conversion.payment_policy_decisions p
		         WHERE p.id=e.payment_policy_decision_id AND p.outcome='selected')))`, id, repo.Environment).Scan(&result)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "resource_not_found")
		return
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "operation_unavailable")
		return
	}
	writeJSON(w, http.StatusOK, result)
}
