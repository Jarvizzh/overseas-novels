package wallet

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"reader-backend/internal/db"
	"reader-backend/internal/model"
	"reader-backend/internal/payment"
)

func (r *dbRepository) savePayPalBillingSnapshot(ctx context.Context, details *payment.SubscriptionDetails) error {
	if details.LastPaymentTime == nil || details.CurrentPeriodEnd == nil || !details.CurrentPeriodEnd.After(*details.LastPaymentTime) ||
		details.LastPaymentCents <= 0 || len(details.LastPaymentCurrency) != 3 {
		return nil
	}
	_, err := db.Pool(ctx).Exec(ctx, `INSERT INTO paypal_subscription_billing_snapshots(subscription_id,paid_at,currency,amount_cents,paid_period_end)
		VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, details.SubscriptionID, *details.LastPaymentTime,
		details.LastPaymentCurrency, details.LastPaymentCents, *details.CurrentPeriodEnd)
	return err
}

func (r *dbRepository) getPayPalBillingSnapshot(ctx context.Context, id string, paidAt time.Time, currency string, cents int64) (time.Time, error) {
	var end time.Time
	err := db.Pool(ctx).QueryRow(ctx, `SELECT paid_period_end FROM paypal_subscription_billing_snapshots
		WHERE subscription_id=$1 AND currency=$3 AND amount_cents=$4 AND paid_at BETWEEN
			$2::timestamptz - interval '1 minute' AND $2::timestamptz + interval '1 minute'
			AND paid_period_end>$2 ORDER BY abs(extract(epoch from paid_at-$2)) LIMIT 1`, id, paidAt, currency, cents).Scan(&end)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, nil
	}
	return end, err
}

// A regular payment on its plan's billing boundary can be mapped even if
// PayPal has removed the next billing date after external cancellation.
func regularPayPalPeriodEnd(sub *model.UserSubscription, anchor, paidAt time.Time) time.Time {
	if anchor.IsZero() || paidAt.Before(anchor.Add(-time.Minute)) {
		return time.Time{}
	}
	anchor, paidAt = anchor.UTC(), paidAt.UTC()
	var cycleDays int
	switch payment.SubscriptionCycle(sub.Cycle) {
	case payment.CycleDay:
		cycleDays = 1
	case payment.CycleWeek:
		cycleDays = 7
	case payment.CycleMonth:
		months := (paidAt.Year()-anchor.Year())*12 + int(paidAt.Month()-anchor.Month())
		for _, offset := range []int{months, months - 1, months + 1} {
			if offset >= 0 && anchoredMonth(anchor, offset).Sub(paidAt).Abs() <= time.Minute {
				return anchoredMonth(anchor, offset+1)
			}
		}
		return time.Time{}
	default:
		return time.Time{}
	}
	cycleDuration := time.Duration(cycleDays) * 24 * time.Hour
	periods := int((paidAt.Sub(anchor) + time.Minute) / cycleDuration)
	start := anchor.AddDate(0, 0, periods*cycleDays)
	if start.Sub(paidAt).Abs() <= time.Minute {
		return start.AddDate(0, 0, cycleDays)
	}
	return time.Time{}
}

func anchoredMonth(anchor time.Time, offset int) time.Time {
	month := time.Date(anchor.Year(), anchor.Month()+time.Month(offset), 1, anchor.Hour(), anchor.Minute(), anchor.Second(), anchor.Nanosecond(), time.UTC)
	days := month.AddDate(0, 1, -1).Day()
	return month.AddDate(0, 0, min(days, anchor.Day())-1)
}
