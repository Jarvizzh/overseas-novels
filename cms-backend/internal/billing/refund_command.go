package billing

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"star-novel-cms/internal/db"
)

var (
	ErrRefundForbidden         = errors.New("refund permission is required")
	ErrRefundKeyConflict       = errors.New("request key is already associated with different refund parameters")
	ErrRefundAmountUnavailable = errors.New("refund amount exceeds the available balance or another refund needs review")
)

type RefundCommand struct {
	ID           string    `json:"id"`
	OrderID      int64     `json:"order_id"`
	ReceiptID    *int64    `json:"receipt_id,omitempty"`
	AmountMinor  *int64    `json:"amount_minor,omitempty"`
	Currency     string    `json:"currency"`
	Status       string    `json:"status"`
	ResultNote   string    `json:"result_note,omitempty"`
	CanReconcile bool      `json:"can_reconcile,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

const refundReconciliationCandidate = `status='review' AND (first_sent_at IS NOT NULL OR legacy_source)
	AND receipt_id IS NOT NULL AND amount_minor IS NOT NULL AND provider_refund_id IS NULL
	AND (claimed_until IS NULL OR claimed_until<=now())`

const refundCommandFields = `id::text,order_id,receipt_id,amount_minor,currency,status,COALESCE(result_note,''),created_at,updated_at,(` + refundReconciliationCandidate + `)`

func scanRefundCommand(row pgx.Row) (RefundCommand, error) {
	var c RefundCommand
	err := row.Scan(&c.ID, &c.OrderID, &c.ReceiptID, &c.AmountMinor, &c.Currency, &c.Status, &c.ResultNote, &c.CreatedAt, &c.UpdatedAt, &c.CanReconcile)
	return c, err
}

type refundCommandInput struct {
	ReceiptID   int64  `json:"receipt_id" binding:"gt=0"`
	AmountMinor int64  `json:"amount_minor" binding:"gt=0"`
	Currency    string `json:"currency" binding:"required,len=3"`
	RequestKey  string `json:"request_key" binding:"required,uuid"`
}

func submitRefundCommand(ctx context.Context, actor string, input refundCommandInput, legacyOrderID int64) (RefundCommand, error) {
	tx, err := db.DB.Begin(ctx)
	if err != nil {
		return RefundCommand{}, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if err := requireFinancialAdmin(ctx, tx, actor); err != nil {
		return RefundCommand{}, err
	}

	var orderID, gross int64
	var capture, receiptCurrency, provider, environment string
	err = tx.QueryRow(ctx, `SELECT p.id,p.order_id,p.capture_id,p.currency,(p.gross_amount*100)::bigint,p.payment_provider,
		COALESCE(a.environment,l.environment)
		FROM third_party_payment_orders p JOIN recharge_orders o ON o.id=p.order_id
		LEFT JOIN payment_channel_accounts a ON p.account_id=a.id AND a.provider=p.payment_provider
		LEFT JOIN paypal_identity_lock l ON p.payment_provider='paypal' AND p.account_id IS NULL
		WHERE (
		  (p.payment_provider='paypal' AND p.status IN ('VERIFIED','COMPLETED') AND p.capture_id NOT LIKE 'mock%'
		    AND (o.order_type<>'subscription' OR p.capture_id IS DISTINCT FROM o.subscription_id)
		    AND COALESCE(a.environment,l.environment) IS NOT NULL
		    AND (o.checkout_environment IS NULL OR COALESCE(a.environment,l.environment)=
		      CASE o.checkout_environment WHEN 'production' THEN 'live' ELSE o.checkout_environment END))
		  OR ($2=0 AND p.payment_provider='stripe' AND p.status='VERIFIED' AND p.object_type='charge'
		    AND p.currency='USD' AND ((a.environment='sandbox' AND o.checkout_environment='sandbox') OR (a.environment='live' AND o.checkout_environment='production')))
		) AND p.capture_id<>'' AND (($2=0 AND p.id=$1) OR ($2<>0 AND p.order_id=$2 AND
		       (EXISTS (SELECT 1 FROM payment_fulfillment_funding f WHERE f.order_id=o.id AND f.receipt_id=p.id)
		        OR (NOT EXISTS(SELECT 1 FROM payment_fulfillment_funding f WHERE f.order_id=o.id)
		            AND p.gross_amount*100=o.amount_cents AND p.currency=o.currency))))
		ORDER BY p.id DESC LIMIT 1`, input.ReceiptID, legacyOrderID).Scan(&input.ReceiptID, &orderID, &capture, &receiptCurrency, &gross, &provider, &environment)
	if errors.Is(err, pgx.ErrNoRows) {
		return RefundCommand{}, ErrOrderNotRefundable
	}
	if err != nil {
		return RefundCommand{}, err
	}

	if legacyOrderID != 0 {
		input.RequestKey = uuid.NewSHA1(uuid.NameSpaceOID, []byte("legacy-refund:"+actor+":"+strconv.FormatInt(legacyOrderID, 10))).String()
	}
	_, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('refund-command:'||$1||':'||$2||':'||$3,0))`,
		environment, actor, input.RequestKey)
	if err != nil {
		return RefundCommand{}, err
	}
	previous, err := scanRefundCommand(tx.QueryRow(ctx, `SELECT `+refundCommandFields+` FROM payment_refund_commands
		WHERE environment=$1 AND actor_id=$2 AND request_key=$3`, environment, actor, input.RequestKey))
	if err == nil {
		if legacyOrderID == 0 && (previous.ReceiptID == nil || *previous.ReceiptID != input.ReceiptID ||
			previous.AmountMinor == nil || *previous.AmountMinor != input.AmountMinor || previous.Currency != input.Currency) {
			return RefundCommand{}, ErrRefundKeyConflict
		}
		return previous, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return RefundCommand{}, err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1||'-capture:'||$2,0))`, provider, capture); err != nil {
		return RefundCommand{}, err
	}
	var orderStatus string
	if err := tx.QueryRow(ctx, `SELECT status FROM recharge_orders WHERE id=$1 FOR UPDATE`, orderID).Scan(&orderStatus); err != nil {
		return RefundCommand{}, err
	}
	switch orderStatus {
	case "Success", "Paid", "RefundPending", "Refunded":
	default:
		return RefundCommand{}, ErrOrderNotRefundable
	}
	if legacyOrderID != 0 && orderStatus == "Refunded" {
		return RefundCommand{}, ErrOrderAlreadyRefunded
	}

	var available int64
	var hasUnknownLegacy bool
	err = tx.QueryRow(ctx, `SELECT $1::bigint
		- COALESCE((SELECT sum(amount_cents)::bigint FROM paypal_refund_items WHERE $6='paypal' AND capture_id=$2 AND currency=$3),0)
 - COALESCE((SELECT sum(amount_minor)::bigint FROM payment_stripe_refund_items WHERE $6='stripe' AND receipt_id=$4),0)
		- COALESCE((SELECT sum(c.amount_minor)::bigint FROM payment_refund_commands c
			WHERE c.receipt_id=$4 AND c.status IN ('pending','confirming','review')
			AND NOT EXISTS (SELECT 1 FROM paypal_refund_items i WHERE $6='paypal' AND i.refund_id=c.provider_refund_id)
 AND NOT EXISTS(SELECT 1 FROM payment_stripe_refund_items i WHERE $6='stripe' AND i.receipt_id=c.receipt_id AND i.refund_ref=c.provider_refund_id)),0),
		EXISTS (SELECT 1 FROM payment_refund_commands WHERE order_id=$5 AND legacy_source
			AND status IN ('confirming','review') AND (receipt_id IS NULL OR amount_minor IS NULL))`,
		gross, capture, receiptCurrency, input.ReceiptID, orderID, provider).Scan(&available, &hasUnknownLegacy)
	if err != nil {
		return RefundCommand{}, err
	}
	if legacyOrderID != 0 {
		input.AmountMinor, input.Currency = available, receiptCurrency
	}
	if hasUnknownLegacy || input.AmountMinor <= 0 || input.AmountMinor > available || input.Currency != receiptCurrency {
		return RefundCommand{}, ErrRefundAmountUnavailable
	}
	command, err := scanRefundCommand(tx.QueryRow(ctx, `INSERT INTO payment_refund_commands
		(id,environment,actor_id,request_key,order_id,receipt_id,amount_minor,currency,status,next_check_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,'pending',now()) RETURNING `+refundCommandFields,
		uuid.New(), environment, actor, input.RequestKey, orderID, input.ReceiptID, input.AmountMinor, input.Currency))
	if err != nil {
		return RefundCommand{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO admin_audit_logs(admin_id,action,target_id,after_data)
		VALUES($1,'submit_payment_refund',$2,jsonb_build_object('order_id',$3::bigint,'receipt_id',$4::bigint,'amount_minor',$5::bigint,'currency',$6::text))`,
		actor, command.ID, orderID, input.ReceiptID, input.AmountMinor, input.Currency)
	if err != nil {
		return RefundCommand{}, err
	}
	return command, tx.Commit(ctx)
}

func findRefundCommand(ctx context.Context, id, actor, key string) (RefundCommand, error) {
	return scanRefundCommand(db.DB.QueryRow(ctx, `SELECT `+refundCommandFields+` FROM payment_refund_commands
		WHERE ($1<>'' AND id=NULLIF($1,'')::uuid) OR ($1='' AND request_key=NULLIF($3,'')::uuid AND actor_id=$2)
		ORDER BY created_at DESC LIMIT 1`, id, actor, key))
}

func refundCommandsForReceipt(ctx context.Context, receiptID int64) ([]RefundCommand, error) {
	rows, err := db.DB.Query(ctx, `SELECT `+refundCommandFields+` FROM payment_refund_commands
		WHERE receipt_id=$1 ORDER BY created_at DESC LIMIT 50`, receiptID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (RefundCommand, error) { return scanRefundCommand(row) })
}

// linkConfirmedPayPalRefund records a finance attestation against a completed
// refund already booked for the same receipt. It never sends a channel request.
func linkConfirmedPayPalRefund(ctx context.Context, actor, id, refundRef, reason string) (RefundCommand, error) {
	tx, err := db.DB.Begin(ctx)
	if err != nil {
		return RefundCommand{}, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if err := requireFinancialAdmin(ctx, tx, actor); err != nil {
		return RefundCommand{}, err
	}
	var capture string
	err = tx.QueryRow(ctx, `SELECT p.capture_id FROM payment_refund_commands c
		JOIN third_party_payment_orders p ON p.id=c.receipt_id AND p.order_id=c.order_id AND p.payment_provider='paypal'
		LEFT JOIN payment_channel_accounts a ON a.id=p.account_id AND a.provider='paypal'
		LEFT JOIN paypal_identity_lock l ON p.account_id IS NULL
		WHERE c.id=$1 AND p.status IN ('VERIFIED','COMPLETED') AND p.capture_id<>'' AND p.capture_id NOT LIKE 'mock%'
		AND COALESCE(a.environment,l.environment)=c.environment AND c.environment IN ('live','sandbox')`, id).Scan(&capture)
	if err != nil {
		return RefundCommand{}, err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('paypal-capture:'||$1,0))`, capture); err != nil {
		return RefundCommand{}, err
	}
	var alreadyLinked bool
	err = tx.QueryRow(ctx, `SELECT status='succeeded' AND COALESCE(provider_refund_id=$2,false)
		FROM payment_refund_commands WHERE id=$1 FOR UPDATE`, id, refundRef).Scan(&alreadyLinked)
	if err != nil {
		return RefundCommand{}, err
	}
	if alreadyLinked {
		command, err := scanRefundCommand(tx.QueryRow(ctx, `SELECT `+refundCommandFields+` FROM payment_refund_commands WHERE id=$1`, id))
		return command, err
	}
	command, err := scanRefundCommand(tx.QueryRow(ctx, `UPDATE payment_refund_commands c SET status='succeeded',
		provider_refund_id=$2,result_note='PayPal refund reconciled by Finance',next_check_at=NULL,claimed_until=NULL,updated_at=now()
		WHERE c.id=$1 AND `+refundReconciliationCandidate+`
		AND EXISTS(SELECT 1 FROM paypal_refund_items r WHERE r.refund_id=$2 AND r.capture_id=$3
			AND r.amount_cents=c.amount_minor AND r.currency=c.currency)
		AND NOT EXISTS(SELECT 1 FROM payment_refund_commands other
			WHERE other.id<>c.id AND other.provider_refund_id=$2)
		RETURNING `+refundCommandFields, id, refundRef, capture))
	if errors.Is(err, pgx.ErrNoRows) {
		return RefundCommand{}, ErrRefundAmountUnavailable
	}
	if err != nil {
		return RefundCommand{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO admin_audit_logs(admin_id,action,target_id,after_data)
		VALUES($1,'reconcile_paypal_refund',$2,jsonb_build_object('refund_ref',$3::text,'reason',$4::text))`,
		actor, id, refundRef, reason)
	if err != nil {
		return RefundCommand{}, err
	}
	return command, tx.Commit(ctx)
}
