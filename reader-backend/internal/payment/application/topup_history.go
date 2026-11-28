package application

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"reader-backend/internal/conversion"
	"reader-backend/internal/db"
	"reader-backend/internal/payment"
)

const (
	TopUpStatusCompleted         = "completed"
	TopUpStatusConfirming        = "confirming"
	TopUpStatusUnderReview       = "under_review"
	TopUpStatusRefundPending     = "refund_pending"
	TopUpStatusPartiallyRefunded = "partially_refunded"
	TopUpStatusRefunded          = "refunded"
	TopUpStatusRevoked           = "revoked"

	TopUpBillingInitial = "initial"
	TopUpBillingRenewal = "renewal"

	topUpHistoryDefaultLimit = 20
	topUpHistoryMaxLimit     = 50
	topUpHistoryMaxCursorLen = 128
)

var (
	ErrInvalidTopUpCursor = errors.New("invalid top-up history cursor")
	ErrInvalidTopUpFilter = errors.New("invalid order type filter")
)

type topUpHistoryCursor struct {
	createdAt time.Time
	id        int64
}

type TopUpHistoryItem struct {
	OrderID                int64      `json:"order_id"`
	OrderNo                string     `json:"order_no"`
	OrderType              string     `json:"order_type"`
	Title                  string     `json:"title"`
	DisplayStatus          string     `json:"display_status"`
	AmountMinor            int64      `json:"amount_minor"`
	Currency               string     `json:"currency"`
	PresentmentAmountMinor *int64     `json:"presentment_amount_minor,omitempty"`
	PresentmentCurrency    string     `json:"presentment_currency,omitempty"`
	RefundedMinor          int64      `json:"refunded_minor,omitempty"`
	Coins                  int        `json:"coins,omitempty"`
	BonusCoins             int        `json:"bonus_coins,omitempty"`
	Provider               string     `json:"provider"`
	NovelID                *int64     `json:"novel_id,omitempty"`
	NovelTitle             string     `json:"novel_title,omitempty"`
	Cycle                  string     `json:"cycle,omitempty"`
	BillingKind            string     `json:"billing_kind,omitempty"`
	PeriodStart            *time.Time `json:"period_start,omitempty"`
	PeriodEnd              *time.Time `json:"period_end,omitempty"`
	CreatedAt              time.Time  `json:"created_at"`
	PaidAt                 *time.Time `json:"paid_at,omitempty"`
	FulfilledAt            *time.Time `json:"fulfilled_at,omitempty"`
}

type TopUpHistoryPage struct {
	Items      []TopUpHistoryItem `json:"items"`
	NextCursor string             `json:"next_cursor,omitempty"`
}

func normalizeTopUpOrderTypeFilter(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "all":
		return "", nil
	case "single", "whole_book", "subscription":
		return strings.ToLower(strings.TrimSpace(raw)), nil
	default:
		return "", ErrInvalidTopUpFilter
	}
}

func parseTopUpHistoryCursor(raw string) (*topUpHistoryCursor, error) {
	if raw == "" {
		return nil, nil
	}
	if len(raw) > topUpHistoryMaxCursorLen {
		return nil, ErrInvalidTopUpCursor
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, ErrInvalidTopUpCursor
	}
	rawTime, rawID, ok := strings.Cut(string(decoded), "|")
	if !ok || rawTime == "" || rawID == "" {
		return nil, ErrInvalidTopUpCursor
	}
	createdAt, err := time.Parse(time.RFC3339Nano, rawTime)
	if err != nil {
		return nil, ErrInvalidTopUpCursor
	}
	id, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil || id <= 0 {
		return nil, ErrInvalidTopUpCursor
	}
	return &topUpHistoryCursor{createdAt: createdAt.UTC(), id: id}, nil
}

func formatTopUpHistoryCursor(createdAt time.Time, id int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(createdAt.UTC().Format(time.RFC3339Nano) + "|" + strconv.FormatInt(id, 10)))
}

type topUpStatusInput struct {
	rawStatus              string
	fulfilledAt            *time.Time
	reconciliationNote     string
	hasDeferredFulfillment bool
	hasPendingRefund       bool
	paypalRefundReported   bool
	refundedMinor          int64
	entitlementBlocked     bool
}

func resolveTopUpDisplayStatus(in topUpStatusInput) string {
	if in.rawStatus == "Refunded" {
		return TopUpStatusRefunded
	}
	if in.entitlementBlocked {
		return TopUpStatusRevoked
	}
	if in.rawStatus == "RefundPending" || in.hasPendingRefund || in.paypalRefundReported {
		return TopUpStatusRefundPending
	}
	if in.refundedMinor > 0 {
		return TopUpStatusPartiallyRefunded
	}
	if in.fulfilledAt == nil {
		if in.reconciliationNote != "" || in.hasDeferredFulfillment {
			return TopUpStatusUnderReview
		}
		return TopUpStatusConfirming
	}
	return TopUpStatusCompleted
}

func resolveTopUpTitle(orderType, novelTitle, displayAlias, snapshotName, productName, cycle string, coins int) string {
	firstNonEmpty := func(candidates ...string) string {
		for _, c := range candidates {
			if trimmed := strings.TrimSpace(c); trimmed != "" {
				return trimmed
			}
		}
		return ""
	}
	switch orderType {
	case "whole_book":
		if t := firstNonEmpty(novelTitle, displayAlias, snapshotName, productName); t != "" {
			return t
		}
		return "Full Book Unlock"
	case "subscription":
		if t := firstNonEmpty(displayAlias, snapshotName, productName); t != "" {
			return t
		}
		switch strings.ToLower(strings.TrimSpace(cycle)) {
		case "day":
			return "Daily Unlimited Pass"
		case "week":
			return "Weekly Unlimited Pass"
		case "month":
			return "Monthly Unlimited Pass"
		case "year":
			return "Annual Unlimited Pass"
		default:
			return "Unlimited Subscription"
		}
	default:
		if t := firstNonEmpty(displayAlias, snapshotName, productName); t != "" {
			return t
		}
		if coins > 0 {
			return fmt.Sprintf("%d Coins Pack", coins)
		}
		return "Coin Top-Up"
	}
}

func normalizeTopUpProvider(receiptProvider, orderMethod string) string {
	p := strings.ToLower(strings.TrimSpace(receiptProvider))
	if p == "" {
		p = strings.ToLower(strings.TrimSpace(orderMethod))
	}
	switch p {
	case payment.ProviderPayPal:
		return payment.ProviderPayPal
	case payment.ProviderStripe:
		return payment.ProviderStripe
	default:
		return p
	}
}

func (s *service) ListTopUpHistory(ctx context.Context, userID int64, limit int, cursorRaw string, orderTypeRaw string) (*TopUpHistoryPage, error) {
	if limit < 1 || limit > topUpHistoryMaxLimit {
		limit = topUpHistoryDefaultLimit
	}
	orderType, err := normalizeTopUpOrderTypeFilter(orderTypeRaw)
	if err != nil {
		return nil, err
	}
	after, err := parseTopUpHistoryCursor(cursorRaw)
	if err != nil {
		return nil, err
	}
	items, err := s.repo.listTopUpHistory(ctx, userID, orderType, limit+1, after)
	if err != nil {
		return nil, err
	}
	if items == nil {
		items = []TopUpHistoryItem{}
	}
	page := &TopUpHistoryPage{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		last := page.Items[limit-1]
		page.NextCursor = formatTopUpHistoryCursor(last.CreatedAt, last.OrderID)
	}
	return page, nil
}

const topUpOrderDateExpr = `COALESCE(o.created_at, TIMESTAMPTZ '0001-01-01 00:00:00+00')`

func (r *dbRepository) listTopUpHistory(ctx context.Context, userID int64, orderType string, limit int, after *topUpHistoryCursor) ([]TopUpHistoryItem, error) {
	checkoutEnv := conversion.Environment()
	channelEnv := currentPaymentLedgerEnvironment()

	args := []any{userID, checkoutEnv, channelEnv, orderType, limit}
	cursorClause := ""
	if after != nil {
		cursorClause = ` AND (` + topUpOrderDateExpr + `, o.id) < ($6::timestamptz, $7::bigint)`
		args = append(args, after.createdAt, after.id)
	}

	query := `
		SELECT
			o.id,
			o.order_type,
			o.status,
			o.amount_cents::bigint,
			COALESCE(NULLIF(upper(btrim(o.currency)), ''), 'USD'),
			COALESCE(o.coins, 0),
			COALESCE(o.bonus_coins_credited, 0),
			COALESCE(o.payment_method, ''),
			COALESCE(receipt.provider, ''),
			COALESCE(receipt.presentment_currency, ''),
			COALESCE(receipt.presentment_amount_minor, 0)::bigint,
			COALESCE(refunds.refunded_minor, 0)::bigint,
			COALESCE(o.reconciliation_note, ''),
			o.paypal_refund_reported,
			EXISTS (
				SELECT 1 FROM payment_deferred_fulfillment df
				WHERE df.order_id = o.id AND df.resolved_at IS NULL
			) AS has_deferred,
			EXISTS (
				SELECT 1 FROM payment_refund_commands rc
				WHERE rc.order_id = o.id AND rc.status IN ('pending', 'confirming', 'review')
				  AND NOT EXISTS (
					  SELECT 1 FROM paypal_refund_items pri
					  WHERE rc.provider_refund_id IS NOT NULL AND pri.refund_id = rc.provider_refund_id
				  )
				  AND NOT EXISTS (
					  SELECT 1 FROM payment_stripe_refund_items sri
					  WHERE rc.receipt_id IS NOT NULL AND rc.provider_refund_id IS NOT NULL
						AND sri.receipt_id = rc.receipt_id AND sri.refund_ref = rc.provider_refund_id
				  )
			) AS has_pending_refund,
			EXISTS (
				SELECT 1 FROM payment_entitlement_adjustments adj
				WHERE adj.order_id = o.id AND adj.blocked = true
			) AS entitlement_blocked,
			o.novel_id::bigint,
			COALESCE(n.title, ''),
			COALESCE(NULLIF(o.product_snapshot->>'display_alias', ''), ''),
			COALESCE(NULLIF(o.product_snapshot->>'name', ''), ''),
			COALESCE(pr.name, ''),
			CASE WHEN o.order_type = 'subscription' THEN COALESCE(
				NULLIF(o.product_snapshot->>'vip_cycle', ''),
				NULLIF(o.product_snapshot->>'cycle', ''),
				NULLIF(pr.vip_cycle, ''),
				NULLIF(us.cycle, ''),
				''
			) ELSE '' END AS cycle,
			CASE
				WHEN o.order_type <> 'subscription' THEN ''
				WHEN sip.billing_reason = 'subscription_create' THEN 'initial'
				WHEN sip.billing_reason = 'subscription_cycle' THEN 'renewal'
				WHEN us.capi_subscribe_order_id = o.id THEN 'initial'
				WHEN NULLIF(o.subscription_id, '') IS NOT NULL AND EXISTS (
					SELECT 1 FROM recharge_orders prev
					WHERE prev.user_id = o.user_id
					  AND prev.order_type = 'subscription'
					  AND prev.subscription_id = o.subscription_id
					  AND (prev.paid_at IS NOT NULL OR prev.fulfilled_at IS NOT NULL)
					  AND (COALESCE(prev.paid_at, prev.created_at, TIMESTAMPTZ '0001-01-01 00:00:00+00'), prev.id)
						< (COALESCE(o.paid_at, o.created_at, TIMESTAMPTZ '0001-01-01 00:00:00+00'), o.id)
				) THEN 'renewal'
				ELSE 'initial'
			END AS billing_kind,
			o.paid_period_start,
			o.paid_period_end,
			` + topUpOrderDateExpr + ` AS created_at,
			o.paid_at,
			o.fulfilled_at
		FROM recharge_orders o
		LEFT JOIN novels n ON n.id = o.novel_id
		LEFT JOIN products pr ON pr.id = o.product_id
		LEFT JOIN LATERAL (
			SELECT s.cycle, s.capi_subscribe_order_id
			FROM user_subscriptions s
			WHERE o.order_type = 'subscription'
			  AND NULLIF(o.subscription_id, '') IS NOT NULL
			  AND s.subscription_id = o.subscription_id
			ORDER BY s.id DESC
			LIMIT 1
		) us ON true
		LEFT JOIN LATERAL (
			SELECT p.billing_reason
			FROM payment_stripe_invoice_periods p
			WHERE o.order_type = 'subscription' AND p.order_id = o.id
			ORDER BY p.admitted DESC, p.updated_at DESC
			LIMIT 1
		) sip ON true
		LEFT JOIN LATERAL (
			SELECT
				count(*)::int AS verified_count,
				(array_agg(p.payment_provider ORDER BY
					EXISTS(SELECT 1 FROM payment_fulfillment_funding f WHERE f.order_id = o.id AND f.receipt_id = p.id) DESC,
					p.id DESC))[1] AS provider,
				CASE
					WHEN count(DISTINCT upper(p.presentment_currency)) FILTER (
						WHERE p.presentment_currency IS NOT NULL AND btrim(p.presentment_currency) <> ''
						  AND p.presentment_amount_minor IS NOT NULL AND p.presentment_amount_minor > 0
					) = 1
					THEN min(upper(btrim(p.presentment_currency))) FILTER (
						WHERE p.presentment_currency IS NOT NULL AND btrim(p.presentment_currency) <> ''
						  AND p.presentment_amount_minor IS NOT NULL AND p.presentment_amount_minor > 0
					)
					ELSE ''
				END AS presentment_currency,
				CASE
					WHEN count(DISTINCT upper(p.presentment_currency)) FILTER (
						WHERE p.presentment_currency IS NOT NULL AND btrim(p.presentment_currency) <> ''
						  AND p.presentment_amount_minor IS NOT NULL AND p.presentment_amount_minor > 0
					) = 1
					THEN COALESCE(sum(p.presentment_amount_minor) FILTER (
						WHERE p.presentment_currency IS NOT NULL AND btrim(p.presentment_currency) <> ''
						  AND p.presentment_amount_minor IS NOT NULL AND p.presentment_amount_minor > 0
						  AND (
							  EXISTS(SELECT 1 FROM payment_fulfillment_funding f WHERE f.order_id = o.id AND f.receipt_id = p.id)
							  OR NOT EXISTS(SELECT 1 FROM payment_fulfillment_funding f WHERE f.order_id = o.id)
						  )
					), 0)::bigint
					ELSE 0::bigint
				END AS presentment_amount_minor
			FROM third_party_payment_orders p
			WHERE p.order_id = o.id AND p.status IN ('VERIFIED', 'COMPLETED')
		) receipt ON true
		LEFT JOIN LATERAL (
			SELECT COALESCE(sum(x.amt), 0)::bigint AS refunded_minor
			FROM (
				SELECT pr_item.amount_cents::bigint AS amt
				FROM paypal_refund_items pr_item
				WHERE EXISTS (
					SELECT 1 FROM third_party_payment_orders p
					WHERE p.order_id = o.id AND p.status IN ('VERIFIED', 'COMPLETED')
					  AND p.payment_provider = 'paypal'
					  AND p.capture_id = pr_item.capture_id AND p.currency = pr_item.currency
				)
				UNION ALL
				SELECT sr_item.amount_minor::bigint AS amt
				FROM payment_stripe_refund_items sr_item
				JOIN third_party_payment_orders p ON p.id = sr_item.receipt_id
				WHERE p.order_id = o.id AND p.status IN ('VERIFIED', 'COMPLETED')
				  AND p.payment_provider = 'stripe'
			) x
		) refunds ON true
		WHERE o.user_id = $1
		  AND payment_order_environment(o) = $3
		  AND (o.checkout_environment IS NULL OR o.checkout_environment = $2)
		  AND COALESCE(o.external_ref_id, '') NOT LIKE 'mock%'
		  AND COALESCE(o.subscription_id, '') NOT LIKE 'I-MOCK%'
		  AND ($4 = '' OR o.order_type = $4)
		  AND (
			  o.paid_at IS NOT NULL
			  OR o.fulfilled_at IS NOT NULL
			  OR o.status IN ('Success', 'Paid', 'RefundPending', 'Refunded')
			  OR COALESCE(receipt.verified_count, 0) > 0
			  OR EXISTS (
				  SELECT 1 FROM payment_entitlement_adjustments adj
				  WHERE adj.order_id = o.id AND adj.blocked = true
			  )
		  )` + cursorClause + `
		ORDER BY ` + topUpOrderDateExpr + ` DESC, o.id DESC
		LIMIT $5`

	rows, err := db.Pool(ctx).Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (TopUpHistoryItem, error) {
		var (
			item                 TopUpHistoryItem
			rawStatus            string
			orderMethod          string
			receiptProvider      string
			presentmentCurrency  string
			presentmentAmount    int64
			reconciliationNote   string
			paypalRefundReported bool
			hasDeferred          bool
			hasPendingRefund     bool
			entitlementBlocked   bool
			novelID              *int64
			novelTitle           string
			displayAlias         string
			snapshotName         string
			productName          string
		)
		if err := row.Scan(
			&item.OrderID,
			&item.OrderType,
			&rawStatus,
			&item.AmountMinor,
			&item.Currency,
			&item.Coins,
			&item.BonusCoins,
			&orderMethod,
			&receiptProvider,
			&presentmentCurrency,
			&presentmentAmount,
			&item.RefundedMinor,
			&reconciliationNote,
			&paypalRefundReported,
			&hasDeferred,
			&hasPendingRefund,
			&entitlementBlocked,
			&novelID,
			&novelTitle,
			&displayAlias,
			&snapshotName,
			&productName,
			&item.Cycle,
			&item.BillingKind,
			&item.PeriodStart,
			&item.PeriodEnd,
			&item.CreatedAt,
			&item.PaidAt,
			&item.FulfilledAt,
		); err != nil {
			return TopUpHistoryItem{}, err
		}

		item.OrderNo = fmt.Sprintf("SN-%d", item.OrderID)
		item.Provider = normalizeTopUpProvider(receiptProvider, orderMethod)
		item.Title = resolveTopUpTitle(item.OrderType, novelTitle, displayAlias, snapshotName, productName, item.Cycle, item.Coins)
		item.DisplayStatus = resolveTopUpDisplayStatus(topUpStatusInput{
			rawStatus:              rawStatus,
			fulfilledAt:            item.FulfilledAt,
			reconciliationNote:     reconciliationNote,
			hasDeferredFulfillment: hasDeferred,
			hasPendingRefund:       hasPendingRefund,
			paypalRefundReported:   paypalRefundReported,
			refundedMinor:          item.RefundedMinor,
			entitlementBlocked:     entitlementBlocked,
		})
		if presentmentCurrency != "" && presentmentAmount > 0 &&
			(presentmentCurrency != item.Currency || presentmentAmount != item.AmountMinor) {
			amt := presentmentAmount
			item.PresentmentCurrency = presentmentCurrency
			item.PresentmentAmountMinor = &amt
		}
		if item.OrderType == "whole_book" && novelID != nil && *novelID > 0 {
			idCopy := *novelID
			item.NovelID = &idCopy
			item.NovelTitle = strings.TrimSpace(novelTitle)
		}
		if item.OrderType != "single" {
			item.Coins = 0
			item.BonusCoins = 0
		}
		if item.OrderType != "subscription" {
			item.Cycle = ""
			item.BillingKind = ""
			item.PeriodStart = nil
			item.PeriodEnd = nil
		}
		return item, nil
	})
}
