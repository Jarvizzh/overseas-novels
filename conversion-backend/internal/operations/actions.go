package operations

import (
	"context"
	"net/http"

	"conversion-backend/internal/delivery"
)

type actionInput struct {
	Reason           string `json:"reason"`
	DestinationID    string `json:"destination_id"`
	ExpectedAttempts *int   `json:"expected_attempts"`
	Acknowledge      bool   `json:"acknowledge_duplicate_risk"`
}

type queueActions struct {
	service delivery.Service
}

func (a queueActions) append(ctx context.Context, actor delivery.Operator, id int64, input actionInput) (delivery.OperationResult, error) {
	target, ok := positiveID(input.DestinationID)
	if !ok {
		return delivery.OperationResult{}, delivery.ErrInvalidOperation
	}
	return a.service.AppendTarget(ctx, actor, id, target, input.Reason)
}

func (a queueActions) retry(ctx context.Context, actor delivery.Operator, id int64, input actionInput) (delivery.OperationResult, error) {
	if input.ExpectedAttempts == nil {
		return delivery.OperationResult{}, delivery.ErrInvalidOperation
	}
	return a.service.RetryDelivery(ctx, actor, id, *input.ExpectedAttempts, input.Reason, input.Acknowledge)
}

func (a queueActions) retryReport(ctx context.Context, actor delivery.Operator, id int64, input actionInput) (delivery.OperationResult, error) {
	return a.service.RetryPartnerReport(ctx, actor, id, input.Reason)
}

type queueAction func(context.Context, delivery.Operator, int64, actionInput) (delivery.OperationResult, error)

func handleAction(action queueAction, status int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := positiveID(r.PathValue("id"))
		var input actionInput
		if !ok || !decodeConfiguration(w, r, &input) {
			writeError(w, http.StatusBadRequest, "invalid_operation")
			return
		}
		result, err := action(r.Context(), operatorFrom(r), id, input)
		writeOperation(w, result, err, status)
	}
}
