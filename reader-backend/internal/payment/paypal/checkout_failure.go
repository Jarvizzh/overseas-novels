package paypal

import (
	"errors"
	"net/http"

	"reader-backend/internal/payment"
)

func captureAPIOutcome(err error) error {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return errors.Join(payment.ErrCheckoutTechnicalFailure, err)
	}
	switch {
	case apiErr.Authentication || apiErr.Status >= http.StatusInternalServerError ||
		apiErr.Status == http.StatusTooManyRequests || apiErr.Status == http.StatusRequestTimeout:
		return errors.Join(payment.ErrCheckoutTechnicalFailure, err)
	case apiErr.HasIssue("PAYEE_BLOCKED_TRANSACTION"):
		return errors.Join(payment.ErrCheckoutRiskBlocked, err)
	case apiErr.HasIssue("INSTRUMENT_DECLINED"):
		return errors.Join(payment.ErrCheckoutDeclined, err)
	case apiErr.HasIssue("PAYER_ACTION_REQUIRED"), apiErr.HasIssue("ORDER_NOT_APPROVED"):
		return errors.Join(payment.ErrCheckoutVerificationRequired, err)
	default:
		return err
	}
}
