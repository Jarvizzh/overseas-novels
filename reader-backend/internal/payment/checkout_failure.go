package payment

import "errors"

// Checkout failures describe the channel response independently of payment,
// fulfillment and risk-review state.
var (
	ErrCheckoutDeclined             = errors.New("payment method declined")
	ErrCheckoutRiskBlocked          = errors.New("payment blocked by channel risk controls")
	ErrCheckoutVerificationRequired = errors.New("checkout requires buyer action")
	ErrCheckoutTechnicalFailure     = errors.New("payment channel technical failure")
)
