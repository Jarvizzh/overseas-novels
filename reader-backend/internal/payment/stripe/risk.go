package stripe

import (
	"context"
	"encoding/json"
	"errors"

	stripego "github.com/stripe/stripe-go/v87"
)

func (c *Client) GetReview(ctx context.Context, id string) (*stripego.Review, error) {
	if !validID(id, "prv_") {
		return nil, errors.New("invalid Stripe Review ID")
	}
	review, err := apiResult(c.api.V1Reviews.Retrieve(ctx, id, nil))
	if err != nil {
		return nil, ClassifyPurposeError(c.scope, PurposeAftercare, err)
	}
	return review, nil
}

func (c *Client) GetEarlyFraudWarning(ctx context.Context, id string) (*stripego.RadarEarlyFraudWarning, error) {
	if !validID(id, "issfr_") {
		return nil, errors.New("invalid Stripe early fraud warning ID")
	}
	warning, err := apiResult(c.api.V1RadarEarlyFraudWarnings.Retrieve(ctx, id, nil))
	if err != nil {
		return nil, ClassifyPurposeError(c.scope, PurposeAftercare, err)
	}
	return warning, nil
}

func (c *Client) ListEarlyFraudWarnings(ctx context.Context, chargeID string, page PageOptions) (Page[*stripego.RadarEarlyFraudWarning], error) {
	if !validChargeID(chargeID) {
		return Page[*stripego.RadarEarlyFraudWarning]{}, errors.New("invalid Stripe Charge ID")
	}
	params, err := page.params()
	if err != nil {
		return Page[*stripego.RadarEarlyFraudWarning]{}, err
	}
	return listPage(c.api.V1RadarEarlyFraudWarnings.List(ctx, &stripego.RadarEarlyFraudWarningListParams{
		ListParams: params,
		Charge:     stripego.String(chargeID),
	}))
}

func (c *Client) ListOpenReviews(ctx context.Context, page PageOptions) (Page[*stripego.Review], error) {
	params, err := page.params()
	if err != nil {
		return Page[*stripego.Review]{}, err
	}
	return listPage(c.api.V1Reviews.List(ctx, &stripego.ReviewListParams{ListParams: params}))
}

const ReviewClosedReasonApproved = string(stripego.ReviewClosedReasonApproved)

// ApproveReview updates Stripe's review. The caller owns authorization and fulfillment.
func (c *Client) ApproveReview(ctx context.Context, id, key string) (*stripego.Review, error) {
	if !validID(id, "prv_") {
		return nil, errors.New("invalid Stripe Review ID")
	}
	params, err := writeParams(key)
	if err != nil {
		return nil, err
	}
	review, err := apiResult(c.api.V1Reviews.Approve(ctx, id, &stripego.ReviewApproveParams{Params: params}))
	if err != nil {
		return nil, ClassifyPurposeError(c.scope, PurposeAftercare, err)
	}
	return review, nil
}

// ListEarlyFraudWarningsByTime backfills warnings without requiring a known Charge.
func (c *Client) ListEarlyFraudWarningsByTime(ctx context.Context, created TimeRange, page PageOptions) (Page[*stripego.RadarEarlyFraudWarning], error) {
	params, err := page.params()
	if err != nil {
		return Page[*stripego.RadarEarlyFraudWarning]{}, err
	}
	window, err := created.params()
	if err != nil {
		return Page[*stripego.RadarEarlyFraudWarning]{}, err
	}
	return listPage(c.api.V1RadarEarlyFraudWarnings.List(ctx, &stripego.RadarEarlyFraudWarningListParams{
		ListParams: params, CreatedRange: window,
	}))
}

// ChargeRisk preserves provider risk and authentication evidence separately from
// the status of the charge. A nil score means Stripe did not provide one.
type ChargeRisk struct {
	ChargeID, PaymentIntentID, ReviewID string
	ChargeStatus                        stripego.ChargeStatus
	FailureCode                         string
	OutcomeType, OutcomeReason          string
	NetworkStatus, NetworkDeclineCode   string
	NetworkAdviceCode, AdviceCode       string
	RiskLevel, RuleID                   string
	RiskScore                           *int64
	CardChecks                          *stripego.ChargePaymentMethodDetailsCardChecks
	ThreeDSecure                        *stripego.ChargePaymentMethodDetailsCardThreeDSecure
}

func (c *Client) GetChargeRisk(ctx context.Context, chargeID string) (ChargeRisk, error) {
	charge, err := c.GetCharge(ctx, chargeID)
	if err != nil {
		return ChargeRisk{}, err
	}
	if charge.ID != chargeID || charge.Livemode != (c.scope.Environment == Live) {
		return ChargeRisk{}, errors.New("unexpected Stripe risk charge")
	}
	return chargeRisk(charge, charge.LastResponse.RawJSON)
}

func chargeRisk(charge *stripego.Charge, raw []byte) (ChargeRisk, error) {
	var fields struct {
		Outcome *struct {
			RiskScore *int64 `json:"risk_score"`
		} `json:"outcome"`
	}
	if err := json.Unmarshal(raw, &fields); err != nil {
		return ChargeRisk{}, errors.New("invalid Stripe charge risk evidence")
	}
	risk := ChargeRisk{
		ChargeID: charge.ID, ChargeStatus: charge.Status, FailureCode: charge.FailureCode,
	}
	if charge.PaymentIntent != nil {
		risk.PaymentIntentID = charge.PaymentIntent.ID
	}
	if charge.Review != nil {
		risk.ReviewID = charge.Review.ID
	}
	if outcome := charge.Outcome; outcome != nil {
		risk.OutcomeType, risk.OutcomeReason = outcome.Type, outcome.Reason
		risk.NetworkStatus = outcome.NetworkStatus
		risk.NetworkDeclineCode, risk.NetworkAdviceCode = outcome.NetworkDeclineCode, outcome.NetworkAdviceCode
		risk.AdviceCode, risk.RiskLevel = string(outcome.AdviceCode), outcome.RiskLevel
		if outcome.Rule != nil {
			risk.RuleID = outcome.Rule.ID
		}
	}
	if fields.Outcome != nil {
		risk.RiskScore = fields.Outcome.RiskScore
	}
	if details := charge.PaymentMethodDetails; details != nil && details.Card != nil {
		risk.CardChecks = details.Card.Checks
		risk.ThreeDSecure = details.Card.ThreeDSecure
	}
	return risk, nil
}

// PaymentFailure retains machine-readable provider details without customer
// messages or payment method data.
type PaymentFailure struct {
	Type, Code, DeclineCode                           string
	AdviceCode, NetworkDeclineCode, NetworkAdviceCode string
	ChargeID, PaymentIntentID                         string
}

// RiskSignal is the risk evidence in an individual event. Absence of a signal
// does not establish that the payment was assessed or that it passed a check.
type RiskSignal struct {
	Scope             Scope
	EventID, SourceID string
	EventType         stripego.EventType
	Charge            *ChargeRisk
	Failure           *PaymentFailure
	Review            *stripego.Review
	EarlyFraudWarning *stripego.RadarEarlyFraudWarning
}

func (e Event) RiskSignal() (RiskSignal, error) {
	result := RiskSignal{Scope: e.Scope, EventID: e.ID, EventType: e.Type, SourceID: e.ObjectID}
	object, err := e.DecodeObject()
	if err != nil {
		return RiskSignal{}, err
	}
	switch o := object.(type) {
	case *stripego.Charge:
		risk, err := chargeRisk(o, e.Object)
		if err != nil {
			return RiskSignal{}, err
		}
		result.Charge = &risk
	case *stripego.PaymentIntent:
		result.Failure = PaymentIntentFailure(o)
	case *stripego.Review:
		result.Review = o
	case *stripego.RadarEarlyFraudWarning:
		result.EarlyFraudWarning = o
	default:
		return RiskSignal{}, ErrUnsupportedEventData
	}
	return result, nil
}

func paymentFailure(err *stripego.Error) PaymentFailure {
	failure := PaymentFailure{
		Type: string(err.Type), Code: string(err.Code), DeclineCode: string(err.DeclineCode),
		AdviceCode: err.AdviceCode, NetworkDeclineCode: err.NetworkDeclineCode,
		NetworkAdviceCode: err.NetworkAdviceCode, ChargeID: err.ChargeID,
	}
	if err.PaymentIntent != nil {
		failure.PaymentIntentID = err.PaymentIntent.ID
	}
	return failure
}

// PaymentIntentFailure reads failure evidence associated with the intent's most
// recent failed attempt. Stripe may clear this field on a later update.
func PaymentIntentFailure(intent *stripego.PaymentIntent) *PaymentFailure {
	if intent == nil || intent.LastPaymentError == nil {
		return nil
	}
	failure := paymentFailure(intent.LastPaymentError)
	failure.PaymentIntentID = intent.ID
	return &failure
}
