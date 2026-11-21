package operations

import (
	"errors"
	"net/http"

	"conversion-backend/internal/delivery"
	"conversion-backend/internal/paymentpolicy"

	"github.com/jackc/pgx/v5"
)

func (repo Repository) registerPaymentPolicy(mux *http.ServeMux, svc delivery.Service) {
	const path = "/internal/conversion/integrations/{id}/payment-policy"
	mux.HandleFunc("GET "+path, func(w http.ResponseWriter, r *http.Request) {
		id, ok := positiveID(r.PathValue("id"))
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid_id")
			return
		}
		scope := paymentpolicy.Scope{Environment: repo.Environment, IntegrationID: id,
			App: r.URL.Query().Get("app_id"), Promotion: r.URL.Query().Get("promotion_id")}
		var out paymentpolicy.View
		err := pgx.BeginTxFunc(r.Context(), repo.Pool, policyReadOptions, func(tx pgx.Tx) error {
			var err error
			out, err = paymentpolicy.ReadTx(r.Context(), tx, scope)
			return err
		})
		writePolicy(w, out, err)
	})
	mux.HandleFunc("POST "+path+"/preview", func(w http.ResponseWriter, r *http.Request) {
		id, ok := positiveID(r.PathValue("id"))
		var draft paymentpolicy.Draft
		if !ok || !decodeConfigurationBody(w, r, &draft, paymentPolicyRequestLimit) {
			writeError(w, http.StatusBadRequest, "invalid_payment_policy_request")
			return
		}
		scope := paymentpolicy.Scope{Environment: repo.Environment, IntegrationID: id, App: draft.App, Promotion: draft.Promotion}
		var out paymentpolicy.Preview
		err := pgx.BeginTxFunc(r.Context(), repo.Pool, policyReadOptions, func(tx pgx.Tx) error {
			var err error
			out, err = paymentpolicy.PreviewTx(r.Context(), tx, scope, draft)
			return err
		})
		writePolicy(w, out, err)
	})
	mux.HandleFunc("POST "+path+"/publish", func(w http.ResponseWriter, r *http.Request) {
		id, ok := positiveID(r.PathValue("id"))
		var draft paymentpolicy.Draft
		if !ok || !decodeConfigurationBody(w, r, &draft, paymentPolicyRequestLimit) {
			writeError(w, http.StatusBadRequest, "invalid_payment_policy_request")
			return
		}
		out, err := svc.PublishPaymentPolicy(r.Context(), operatorFrom(r), id, draft)
		writePolicy(w, out, err)
	})
}

// Allow a draft with a full set of interval operations and exact decimal fields.
const paymentPolicyRequestLimit = 64 << 10

var policyReadOptions = pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}

func writePolicy(w http.ResponseWriter, out any, err error) {
	var invalid *paymentpolicy.ValidationError
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, out)
	case errors.As(err, &invalid):
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": invalid.Code, "field": invalid.Field})
	case errors.Is(err, paymentpolicy.ErrVersionConflict):
		writeError(w, http.StatusConflict, err.Error())
	default:
		writeOperation(w, delivery.OperationResult{}, err, http.StatusOK)
	}
}
