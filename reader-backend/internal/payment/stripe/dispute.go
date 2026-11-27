package stripe

import (
	"context"
	"errors"

	stripego "github.com/stripe/stripe-go/v87"
)

// Stripe Dispute status classification constants.
const (
	DisputeStatusWarningNeedsResponse = "warning_needs_response"
	DisputeStatusWarningUnderReview   = "warning_under_review"
	DisputeStatusWarningClosed        = "warning_closed"
	DisputeStatusNeedsResponse        = "needs_response"
	DisputeStatusUnderReview          = "under_review"
	DisputeStatusWon                  = "won"
	DisputeStatusLost                 = "lost"
	DisputeStatusPrevented            = "prevented"
)

// DisputeCategory classifies the live lifecycle stage of a Stripe Dispute.
type DisputeCategory string

const (
	DisputeCategoryInquiryOpen DisputeCategory = "inquiry_open"
	DisputeCategoryFormalOpen  DisputeCategory = "formal_open"
	DisputeCategoryClosedWon   DisputeCategory = "closed_won"
	DisputeCategoryClosedLost  DisputeCategory = "closed_lost"
	DisputeCategoryClosedOther DisputeCategory = "closed_other"
	DisputeCategoryUnknown     DisputeCategory = "unknown"
)

// ClassifyDisputeStatus maps a live Stripe Dispute status to its domain category.
func ClassifyDisputeStatus(status string) DisputeCategory {
	switch status {
	case DisputeStatusWarningNeedsResponse, DisputeStatusWarningUnderReview:
		return DisputeCategoryInquiryOpen
	case DisputeStatusNeedsResponse, DisputeStatusUnderReview:
		return DisputeCategoryFormalOpen
	case DisputeStatusWon:
		return DisputeCategoryClosedWon
	case DisputeStatusLost:
		return DisputeCategoryClosedLost
	case DisputeStatusWarningClosed, DisputeStatusPrevented:
		return DisputeCategoryClosedOther
	default:
		return DisputeCategoryUnknown
	}
}

func (c *Client) GetDispute(ctx context.Context, id string) (*stripego.Dispute, error) {
	if !validID(id, "dp_") && !validID(id, "du_") {
		return nil, errors.New("invalid Stripe Dispute ID")
	}
	dispute, err := apiResult(c.api.V1Disputes.Retrieve(ctx, id, nil))
	if err != nil {
		return nil, ClassifyPurposeError(c.scope, PurposeAftercare, err)
	}
	return dispute, nil
}

func (c *Client) ListDisputes(ctx context.Context, created TimeRange, page PageOptions) (Page[*stripego.Dispute], error) {
	params, err := page.params()
	if err != nil {
		return Page[*stripego.Dispute]{}, err
	}
	window, err := created.params()
	if err != nil {
		return Page[*stripego.Dispute]{}, err
	}
	res, err := listPage(c.api.V1Disputes.List(ctx, &stripego.DisputeListParams{ListParams: params, CreatedRange: window}))
	if err != nil {
		return Page[*stripego.Dispute]{}, ClassifyPurposeError(c.scope, PurposeAftercare, err)
	}
	return res, nil
}

func (c *Client) ListChargeDisputes(ctx context.Context, chargeID string, page PageOptions) (Page[*stripego.Dispute], error) {
	if !validChargeID(chargeID) {
		return Page[*stripego.Dispute]{}, errors.New("invalid Stripe Charge ID")
	}
	params, err := page.params()
	if err != nil {
		return Page[*stripego.Dispute]{}, err
	}
	res, err := listPage(c.api.V1Disputes.List(ctx, &stripego.DisputeListParams{
		ListParams: params,
		Charge:     stripego.String(chargeID),
	}))
	if err != nil {
		return Page[*stripego.Dispute]{}, ClassifyPurposeError(c.scope, PurposeAftercare, err)
	}
	return res, nil
}
