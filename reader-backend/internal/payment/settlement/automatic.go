package settlement

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"reader-backend/internal/payment"
	"reader-backend/internal/payment/ledger"
	"reader-backend/internal/payment/paypal"
	"reader-backend/internal/payment/paypal/credentialstore"
	"reader-backend/internal/payment/stripe"
)

type automaticLedgerPage struct {
	id                              int64
	provider, actor, reason, cursor string
	account                         payment.WebhookAccount
	start, end                      time.Time
	token                           uuid.UUID
}

func (r *Repository) SyncNextLedgerPage(ctx context.Context, environment string) (bool, error) {
	job, err := r.claimAutomaticLedgerPage(ctx, environment)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	next, problem := r.syncLedgerPage(ctx, job)
	if ctx.Err() != nil {
		return true, ctx.Err()
	}
	note := ""
	if problem != nil {
		note = stripe.RedactSecrets(problem.Error())
	}
	_, err = r.pool.Exec(ctx, `UPDATE payment_ledger_sync_settings SET lease_token=NULL,lease_until=NULL,
		last_error=NULLIF(left($4,800),''),
		page_cursor=CASE WHEN $4<>'' THEN page_cursor ELSE $3 END,
		window_start=CASE WHEN $4='' AND $3='' THEN now()-interval '7 days' ELSE window_start END,
		window_end=CASE WHEN $4='' AND $3='' THEN now() ELSE window_end END,
		last_complete_at=CASE WHEN $4='' AND $3='' THEN now() ELSE last_complete_at END,
		next_attempt_at=now()+CASE WHEN $4<>'' THEN interval '1 hour' WHEN $3='' THEN interval '1 day' ELSE interval '1 minute' END
		WHERE account_id=$1 AND lease_token=$2`, job.id, job.token, next, note)
	return true, err
}

func (r *Repository) claimAutomaticLedgerPage(ctx context.Context, environment string) (automaticLedgerPage, error) {
	var job automaticLedgerPage
	job.token = uuid.New()
	err := r.pool.QueryRow(ctx, `WITH due AS (
		SELECT s.account_id,a.provider,a.account_key,a.environment FROM payment_ledger_sync_settings s
		JOIN payment_channel_accounts a ON a.id=s.account_id
		JOIN admins u ON u.id = s.actor_id AND public.admin_has_permission(u.id, 'payments.reconciliation:write')
		WHERE s.enabled AND a.environment=$1 AND s.next_attempt_at<=now() AND (s.lease_until IS NULL OR s.lease_until<=now())
		ORDER BY s.next_attempt_at,s.account_id FOR UPDATE OF s SKIP LOCKED LIMIT 1
	)
	UPDATE payment_ledger_sync_settings s SET lease_token=$2,lease_until=now()+interval '10 minutes'
	FROM due WHERE due.account_id=s.account_id
	RETURNING s.account_id,due.provider,due.account_key,due.environment,s.actor_id,s.reason,s.page_cursor,s.window_start,s.window_end`,
		environment, job.token).Scan(&job.id, &job.provider, &job.account.AccountKey, &job.account.Environment, &job.actor, &job.reason, &job.cursor, &job.start, &job.end)
	return job, err
}

func (r *Repository) syncLedgerPage(ctx context.Context, job automaticLedgerPage) (string, error) {
	switch job.provider {
	case payment.ProviderPayPal:
		client := paypal.NewClient(func(ctx context.Context) (paypal.Config, error) {
			return credentialstore.LoadForAccount(ctx, job.account)
		})
		account, err := client.CheckoutAccount(ctx)
		if err != nil {
			return "", err
		}
		if account != job.account {
			return "", errors.New("PayPal reporting credentials do not match the scheduled account")
		}
		page := 1
		if job.cursor != "" {
			page, err = strconv.Atoi(job.cursor)
			if err != nil || page < 1 {
				return "", errors.New("invalid PayPal ledger page")
			}
		}
		proof, err := client.ListLedgerMovements(ctx, job.start, job.end, page)
		if err != nil {
			return "", err
		}
		repo := ledger.NewRepository(r.pool)
		for _, movement := range proof.Entries {
			if movement.Account != job.account {
				return "", errors.New("PayPal reporting account changed")
			}
			if _, err := repo.Record(ctx, movement); err != nil {
				return "", err
			}
		}
		if proof.HasMore {
			return strconv.Itoa(page + 1), nil
		}
		return "", nil
	case payment.ProviderStripe:
		client, err := r.resolveStripeClient(ctx, job.account, stripe.PurposePayout)
		if err != nil {
			return "", err
		}
		page, err := client.ListPayouts(ctx, stripe.TimeRange{From: job.start, Before: job.end}, stripe.PageOptions{Limit: 10, StartingAfter: job.cursor})
		if err != nil {
			return "", err
		}
		audit := Audit{Operator: "automatic:" + job.actor, Reason: job.reason}
		for _, payout := range page.Items {
			if payout.Type != "bank_account" || payout.OriginalPayout != nil {
				continue
			}
			if _, err := ImportStripePayout(ctx, client, r, payout.ID, audit); err != nil {
				return "", err
			}
		}
		if !page.HasMore {
			return "", nil
		}
		if len(page.Items) == 0 || page.Items[len(page.Items)-1].ID == job.cursor {
			return "", errors.New("Stripe payout page did not advance")
		}
		return page.Items[len(page.Items)-1].ID, nil
	default:
		return "", errors.New("unsupported automatic payment reporting provider")
	}
}
