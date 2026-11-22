package stripe

import (
	"context"
	"net/http"
	"testing"
	"time"

	stripego "github.com/stripe/stripe-go/v87"
)

func TestRadarQueriesAndReviewApproval(t *testing.T) {
	ctx := context.Background()
	client := fixtureClient(t, func(req *http.Request) (*http.Response, error) {
		query := req.URL.Query()
		switch req.Method + " " + req.URL.Path {
		case "GET /v1/reviews":
			if query.Get("starting_after") != "prv_previous" || query.Get("limit") != "2" {
				t.Fatalf("unexpected Review query: %s", req.URL)
			}
			return stripeResponse(200, `{"object":"list","has_more":true,"data":[{"id":"prv_current","open":true,"charge":"ch_paid","payment_intent":"pi_paid","reason":"rule"}]}`)
		case "GET /v1/reviews/prv_current":
			return stripeResponse(200, `{"id":"prv_current","open":true,"charge":"ch_paid","payment_intent":"pi_paid","reason":"rule"}`)
		case "POST /v1/reviews/prv_current/approve":
			if req.Header.Get("Idempotency-Key") != "review-prv-current" {
				t.Fatal("review approval sent without the caller's original key")
			}
			return stripeResponse(200, `{"id":"prv_current","open":false,"closed_reason":"approved","charge":"ch_paid"}`)
		case "GET /v1/radar/early_fraud_warnings":
			if query.Get("charge") != "ch_paid" &&
				(query.Get("created[gte]") != "1795132800" || query.Get("created[lt]") != "1795219200") {
				t.Fatalf("unexpected warning filter: %s", req.URL)
			}
			return stripeResponse(200, `{"object":"list","data":[{"id":"issfr_original","charge":"ch_paid","actionable":true,"fraud_type":"unauthorized_use_of_card"}]}`)
		case "GET /v1/radar/early_fraud_warnings/issfr_original":
			return stripeResponse(200, `{"id":"issfr_original","charge":"ch_paid","payment_intent":"pi_paid","actionable":false}`)
		default:
			t.Fatalf("unexpected Radar request: %s %s", req.Method, req.URL)
			return nil, nil
		}
	})

	reviews, err := client.ListOpenReviews(ctx, PageOptions{Limit: 2, StartingAfter: "prv_previous"})
	if err != nil || !reviews.HasMore || len(reviews.Items) != 1 ||
		reviews.Items[0].Charge.ID != "ch_paid" || !reviews.Items[0].Open {
		t.Fatalf("unexpected open reviews: %+v, error %v", reviews, err)
	}
	review, err := client.GetReview(ctx, "prv_current")
	if err != nil || !review.Open || review.PaymentIntent.ID != "pi_paid" {
		t.Fatalf("unexpected Review: %+v, error %v", review, err)
	}
	if _, err := client.ApproveReview(ctx, "prv_current", ""); err == nil {
		t.Fatal("review approval accepted without an idempotency key")
	}
	approved, err := client.ApproveReview(ctx, "prv_current", "review-prv-current")
	if err != nil || approved.Open || approved.ClosedReason != stripego.ReviewClosedReasonApproved {
		t.Fatalf("unexpected approval: %+v, error %v", approved, err)
	}
	warnings, err := client.ListEarlyFraudWarnings(ctx, "ch_paid", PageOptions{})
	if err != nil || len(warnings.Items) != 1 || !warnings.Items[0].Actionable {
		t.Fatalf("unexpected warnings by Charge: %+v, error %v", warnings, err)
	}
	window := TimeRange{From: time.Unix(1795132800, 0), Before: time.Unix(1795219200, 0)}
	warnings, err = client.ListEarlyFraudWarningsByTime(ctx, window, PageOptions{})
	if err != nil || len(warnings.Items) != 1 {
		t.Fatalf("unexpected warning backfill: %+v, error %v", warnings, err)
	}
	warning, err := client.GetEarlyFraudWarning(ctx, "issfr_original")
	if err != nil || warning.Actionable || warning.PaymentIntent.ID != "pi_paid" {
		t.Fatalf("unexpected warning details: %+v, error %v", warning, err)
	}
}

func TestChargeRiskPreservesAbsentScoreAnd3DS(t *testing.T) {
	client := fixtureClient(t, func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/v1/charges/ch_paid":
			return stripeResponse(200, `{
				"id":"ch_paid","object":"charge","status":"succeeded","payment_intent":"pi_paid","review":"prv_current",
				"outcome":{"type":"manual_review","reason":"rule","risk_level":"elevated","risk_score":0,"rule":"rule_1","network_status":"approved_by_network"},
				"payment_method_details":{"card":{
					"checks":{"cvc_check":"pass","address_postal_code_check":"unavailable"},
					"three_d_secure":{"result":"authenticated","authentication_flow":"challenge","version":"2.2.0"}
				}}
			}`)
		case "/v1/charges/ch_blocked":
			return stripeResponse(200, `{"id":"ch_blocked","object":"charge","status":"failed","failure_code":"card_declined","outcome":{"type":"blocked","reason":"highest_risk_level","risk_level":"highest","network_status":"not_sent_to_network"}}`)
		default:
			t.Fatalf("unexpected Charge query: %s", req.URL)
			return nil, nil
		}
	})
	paid, err := client.GetChargeRisk(context.Background(), "ch_paid")
	if err != nil || paid.ChargeStatus != "succeeded" || paid.OutcomeType != "manual_review" ||
		paid.ReviewID != "prv_current" || paid.RuleID != "rule_1" ||
		paid.RiskScore == nil || *paid.RiskScore != 0 ||
		paid.CardChecks == nil || paid.CardChecks.CVCCheck != "pass" ||
		paid.ThreeDSecure == nil || paid.ThreeDSecure.Result != "authenticated" {
		t.Fatalf("lost paid Charge risk evidence: %+v, error %v", paid, err)
	}
	blocked, err := client.GetChargeRisk(context.Background(), "ch_blocked")
	if err != nil || blocked.OutcomeType != "blocked" || blocked.NetworkStatus != "not_sent_to_network" ||
		blocked.RiskScore != nil || blocked.ThreeDSecure != nil || blocked.FailureCode != "card_declined" {
		t.Fatalf("lost blocked Charge risk evidence: %+v, error %v", blocked, err)
	}
}
