package application

import (
	"errors"

	"reader-backend/internal/payment"
)

type captureOutcome string

const (
	captureBlocked          captureOutcome = "payment_blocked"
	captureDeclined         captureOutcome = "payment_declined"
	captureBuyerAction      captureOutcome = "payment_action_required"
	captureUnavailable      captureOutcome = "payment_unavailable"
	captureUnconfirmed      captureOutcome = "payment_unconfirmed"
	captureLegacyBookReview captureOutcome = "historical_payment_review"
)

func captureOutcomeFor(err error) captureOutcome {
	switch {
	case errors.Is(err, payment.ErrCheckoutRiskBlocked):
		return captureBlocked
	case errors.Is(err, payment.ErrCheckoutDeclined):
		return captureDeclined
	case errors.Is(err, payment.ErrCheckoutVerificationRequired):
		return captureBuyerAction
	case errors.Is(err, payment.ErrCheckoutTechnicalFailure):
		return captureUnavailable
	default:
		return captureUnconfirmed
	}
}

func (o captureOutcome) publicMessage() string {
	switch o {
	case "payment_switch_pending":
		return "We are checking the original checkout. You can continue with PayPal while this check is pending."
	case captureLegacyBookReview:
		return "We are checking this PayPal purchase. Check its status again, or contact support with your PayPal order reference."
	case captureBlocked:
		return "This payment could not be completed."
	case captureDeclined:
		return "Your payment method was declined. Check your payment details."
	case captureBuyerAction:
		return "Check PayPal to complete any required steps."
	case captureUnavailable:
		return "PayPal confirmation is temporarily unavailable. Check the original order before trying again."
	default:
		return "Payment confirmation is pending. Please check your order again."
	}
}
