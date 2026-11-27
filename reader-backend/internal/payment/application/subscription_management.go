package application

import (
	"net/url"
	"strings"
	"time"

	"reader-backend/internal/config"
	"reader-backend/internal/model"
	"reader-backend/internal/payment"
)

const (
	SubscriptionStatusNone                   = "none"
	SubscriptionStatusActive                 = "active"
	SubscriptionStatusPending                = "pending"
	SubscriptionStatusCancelPending          = "cancel_pending"
	SubscriptionStatusCanceledWithPaidPeriod = "canceled_with_paid_period"
	SubscriptionStatusCanceled               = "canceled"
	SubscriptionStatusRenewalFailed          = "renewal_failed"

	stripeBillingPortalEndpoint = "/wallet/subscription/stripe/portal"
	defaultStripeMoRLinkURL     = "https://app.link.com/login"
)

// SubscriptionCancelResult describes the outcome of a self-service auto-renew cancellation request.
type SubscriptionCancelResult struct {
	SubscriptionID   string     `json:"subscription_id"`
	NormalizedStatus string     `json:"status"`
	AlreadyRequested bool       `json:"already_requested"`
	PaidThrough      *time.Time `json:"paid_through,omitempty"`
	Message          string     `json:"message"`
}

func resolveStripeMoRLinkURL() string {
	raw := defaultStripeMoRLinkURL
	if config.AppConfig != nil && strings.TrimSpace(config.AppConfig.StripeMoRLinkURL) != "" {
		raw = strings.TrimSpace(config.AppConfig.StripeMoRLinkURL)
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return ""
	}
	return parsed.String()
}

func normalizePortalReturnPath(raw string) string {
	switch strings.TrimSpace(raw) {
	case "/subscription":
		return "/subscription"
	default:
		return "/recharge"
	}
}

func normalizeSubscriptionStatus(sub model.UserSubscription, now time.Time) string {
	status := strings.ToUpper(strings.TrimSpace(sub.Status))
	hasActivePaidPeriod := sub.CurrentPeriodEnd != nil && sub.CurrentPeriodEnd.After(now)
	hasUnresolvedRenewalFailure := sub.RenewalFailedAt != nil &&
		(sub.LastPaymentTime == nil || sub.RenewalFailedAt.After(*sub.LastPaymentTime))

	switch status {
	case "CANCEL_PENDING":
		return SubscriptionStatusCancelPending
	case "CANCELLED", "EXPIRED":
		if hasActivePaidPeriod {
			return SubscriptionStatusCanceledWithPaidPeriod
		}
		return SubscriptionStatusCanceled
	case "SUSPENDED":
		return SubscriptionStatusRenewalFailed
	case "ACTIVE":
		if hasUnresolvedRenewalFailure {
			return SubscriptionStatusRenewalFailed
		}
		return SubscriptionStatusActive
	case "PENDING":
		return SubscriptionStatusPending
	default:
		if hasActivePaidPeriod {
			return SubscriptionStatusCanceledWithPaidPeriod
		}
		return SubscriptionStatusNone
	}
}

func defaultSubscriptionPlanName(cycle string) string {
	switch strings.ToLower(strings.TrimSpace(cycle)) {
	case "day", "daily":
		return "VIP Daily Pass"
	case "week", "weekly":
		return "VIP Weekly Pass"
	case "month", "monthly":
		return "VIP Monthly Pass"
	case "year", "yearly", "annual":
		return "VIP Annual Pass"
	default:
		return "VIP Membership"
	}
}

func enrichManagedSubscription(sub model.UserSubscription, now time.Time, morLinkURL string) model.UserSubscription {
	provider := strings.ToLower(strings.TrimSpace(sub.PaymentMethod))
	sub.Provider = provider

	salesMode := strings.ToLower(strings.TrimSpace(sub.SalesMode))
	if provider == payment.ProviderStripe {
		if salesMode == "" {
			switch strings.ToLower(strings.TrimSpace(sub.Management)) {
			case "stripe_portal":
				salesMode = payment.StripeSaleDirect
			case "stripe_link":
				salesMode = payment.StripeSaleMoR
			}
		}
		if sub.Management == "" {
			switch salesMode {
			case payment.StripeSaleDirect:
				sub.Management = "stripe_portal"
			case payment.StripeSaleMoR:
				sub.Management = "stripe_link"
			}
		}
	} else {
		salesMode = ""
	}
	sub.SalesMode = salesMode

	if strings.TrimSpace(sub.PlanName) == "" {
		sub.PlanName = defaultSubscriptionPlanName(sub.Cycle)
	}

	normalized := normalizeSubscriptionStatus(sub, now)
	sub.NormalizedStatus = normalized
	sub.PaidThrough = sub.CurrentPeriodEnd

	if (normalized == SubscriptionStatusActive || normalized == SubscriptionStatusRenewalFailed) && sub.NextBillingTime != nil {
		sub.NextBillingAt = sub.NextBillingTime
	} else {
		sub.NextBillingAt = nil
	}

	rawStatus := strings.ToUpper(strings.TrimSpace(sub.Status))
	canCancel := (provider == payment.ProviderPayPal || provider == payment.ProviderStripe) &&
		rawStatus != "CANCELLED" && rawStatus != "EXPIRED" && rawStatus != "CANCEL_PENDING"
	canOpenPortal := provider == payment.ProviderStripe && salesMode == payment.StripeSaleDirect
	canOpenMoRLink := provider == payment.ProviderStripe && salesMode == payment.StripeSaleMoR && morLinkURL != ""

	caps := &model.SubscriptionCapabilities{
		CanCancelAutoRenew:   canCancel,
		CancelPending:        rawStatus == "CANCEL_PENDING",
		CanOpenBillingPortal: canOpenPortal,
		CanOpenMoRLinkPortal: canOpenMoRLink,
	}
	if canOpenPortal {
		caps.BillingPortalEndpoint = stripeBillingPortalEndpoint
	}
	if canOpenMoRLink {
		caps.MoRLinkURL = morLinkURL
	}
	sub.Capabilities = caps
	return sub
}
