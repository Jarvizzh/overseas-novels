package billing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"star-novel-cms/internal/config"
	"star-novel-cms/internal/db"
)

var (
	ErrRefundPending   = errors.New("PayPal refund is pending. Check again to refresh its status.")
	ErrRefundForbidden = errors.New("refund permission is required")
	ErrRefundFailed    = errors.New("PayPal rejected or cancelled the refund")
)

type refundRequest struct {
	orderID   int64
	amount    int
	currency  string
	capture   string
	orderType string
	requestID string
	refundID  string
}

func (s *billingService) RefundOrder(ctx context.Context, orderID, adminID string) error {
	if adminID == "" {
		return ErrRefundForbidden
	}
	request, err := s.beginPayPalRefund(ctx, orderID, &adminID)
	if err != nil || request == nil {
		return err
	}
	return s.submitPayPalRefund(ctx, request)
}

func (s *billingService) submitPayPalRefund(ctx context.Context, request *refundRequest) error {
	result, err := refundThroughPayPal(ctx, request)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrRefundPending, err)
	}
	if result.ID == "" {
		return ErrRefundPending
	}

	switch strings.ToUpper(result.Status) {
	case "FAILED", "CANCELLED", "CANCELED", "DENIED":
		if err := s.failPayPalRefund(ctx, request, result); err != nil {
			return err
		}
		return ErrRefundFailed
	}
	tag, err := db.DB.Exec(ctx, `UPDATE payment_refunds SET provider_refund_id=$2, updated_at=now()
		WHERE order_id=$1 AND request_id=$3 AND status IN ('Pending','Completed')
		AND (provider_refund_id IS NULL OR provider_refund_id=$2)`, request.orderID, result.ID, request.requestID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("refund ID mismatch")
	}
	if !strings.EqualFold(result.Status, "completed") {
		return ErrRefundPending
	}
	if result.Currency != request.currency || result.Amount != fmt.Sprintf("%d.%02d", request.amount/100, request.amount%100) {
		return errors.New("PayPal returned a refund amount that differs from the order")
	}
	_, err = db.DB.Exec(ctx, `SELECT complete_paypal_refund($1,$2)`, request.orderID, result.ID)
	return err
}

func (s *billingService) beginPayPalRefund(ctx context.Context, id string, adminID *string) (*refundRequest, error) {
	tx, err := s.repo.BeginTx(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if adminID != nil {
		var role string
		var status int
		err := tx.QueryRow(ctx, `SELECT role, status FROM admins WHERE id=$1 FOR SHARE`, *adminID).Scan(&role, &status)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrRefundForbidden
		}
		if err != nil {
			return nil, err
		}
		if status != 1 || (role != "Finance" && role != "SuperAdmin") {
			return nil, ErrRefundForbidden
		}
	}

	o, err := s.repo.GetOrderForUpdateTx(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if o == nil {
		return nil, ErrOrderNotFound
	}
	if o.Status == "Refunded" {
		var completed bool
		err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM payment_refunds WHERE order_id=$1 AND status='Completed')`, o.ID).Scan(&completed)
		if err != nil {
			return nil, err
		}
		if completed {
			return nil, nil
		}
		return nil, ErrOrderAlreadyRefunded
	}
	// TODO(stripe): use the verified Stripe charge and idempotent channel refunds.
	if o.PaymentMethod != "paypal" || o.AmountCents <= 0 ||
		(o.Status != "Success" && o.Status != "Paid" && o.Status != "RefundPending") {
		return nil, ErrOrderNotRefundable
	}
	request := &refundRequest{orderID: o.ID, amount: o.AmountCents, currency: o.Currency, orderType: o.OrderType}
	err = tx.QueryRow(ctx, `SELECT capture_id FROM third_party_payment_orders
		WHERE order_id=$1 AND payment_provider='paypal' AND status IN ('VERIFIED','COMPLETED')
		AND capture_id <> '' AND gross_amount*100=$2 AND currency=$3
		AND capture_id <> ALL(ARRAY[COALESCE($4,'')]) AND capture_id NOT LIKE 'mock%' ORDER BY id DESC LIMIT 1`,
		o.ID, o.AmountCents, o.Currency, legacySubscriptionRef(o)).Scan(&request.capture)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrOrderNotRefundable
	}
	if err != nil {
		return nil, err
	}
	if o.Status == "RefundPending" {
		err = tx.QueryRow(ctx, `SELECT request_id::text,COALESCE(provider_refund_id,'')
			FROM payment_refunds WHERE order_id=$1 AND status='Pending'`, o.ID).Scan(&request.requestID, &request.refundID)
		if err != nil {
			return nil, err
		}
		return request, tx.Commit(ctx)
	}
	// Reconciliation resumes existing requests. Only an authorized admin can create one.
	if adminID == nil {
		return nil, nil
	}
	request.requestID = uuid.NewString()
	tag, err := tx.Exec(ctx, `INSERT INTO payment_refunds(order_id,request_id,admin_id,prior_order_status)
		VALUES($1,$2,$3,$4) ON CONFLICT(order_id) DO UPDATE SET request_id=EXCLUDED.request_id,
		admin_id=EXCLUDED.admin_id,prior_order_status=EXCLUDED.prior_order_status,provider_refund_id=NULL,
		status='Pending',updated_at=now() WHERE payment_refunds.status='Failed'`,
		o.ID, request.requestID, *adminID, o.Status)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() != 1 {
		return nil, ErrOrderNotRefundable
	}
	if err := s.repo.UpdateOrderStatusTx(ctx, tx, id, "RefundPending"); err != nil {
		return nil, err
	}
	if err := s.repo.InsertAdminAuditLogTx(ctx, tx, *adminID, "refund_order", id,
		fmt.Sprintf(`{"status":%q}`, o.Status), `{"status":"RefundPending"}`); err != nil {
		return nil, err
	}
	return request, tx.Commit(ctx)
}

func legacySubscriptionRef(o *Order) string {
	if o.OrderType == "subscription" && strings.HasPrefix(o.ExternalRefID, "I-") {
		return o.ExternalRefID
	}
	return ""
}

type paypalRefundResult struct{ ID, Status, Currency, Amount string }

func refundThroughPayPal(ctx context.Context, request *refundRequest) (*paypalRefundResult, error) {
	values := map[string]string{}
	rows, err := db.DB.Query(ctx, `SELECT key,value FROM system_configs
		WHERE key IN ('paypal_client_id','paypal_secret_key','paypal_mode','payment_sandbox_mode')`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			rows.Close()
			return nil, err
		}
		values[key] = strings.TrimSpace(value)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	id, secret := values["paypal_client_id"], values["paypal_secret_key"]
	if id == "" {
		id, secret = config.AppConfig.PayPalClientID, config.AppConfig.PayPalClientSecret
	}
	if id == "" || secret == "" || id == "paypal_client_id_placeholder" {
		return nil, errors.New("PayPal unavailable")
	}
	mode := strings.ToLower(values["paypal_mode"])
	if mode == "" {
		switch values["payment_sandbox_mode"] {
		case "false":
			mode = "live"
		case "":
			mode = strings.ToLower(os.Getenv("PAYPAL_MODE"))
		default:
			mode = "sandbox"
		}
	}
	host := "https://api-m.sandbox.paypal.com"
	if mode == "live" || mode == "production" {
		host = "https://api-m.paypal.com"
	}
	client := &http.Client{Timeout: 15 * time.Second}
	tokenRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, host+"/v1/oauth2/token", strings.NewReader("grant_type=client_credentials"))
	if err != nil {
		return nil, err
	}
	tokenRequest.SetBasicAuth(id, secret)
	tokenRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var token struct {
		AccessToken string `json:"access_token"`
	}
	if err := readPayPalResponse(client, tokenRequest, &token); err != nil {
		return nil, err
	}
	if token.AccessToken == "" {
		return nil, errors.New("empty PayPal access token")
	}

	path := "/v2/payments/captures/" + url.PathEscape(request.capture) + "/refund"
	refundPath := "/v2/payments/refunds/"
	amount := fmt.Sprintf("%d.%02d", request.amount/100, request.amount%100)
	body := map[string]any{"amount": map[string]string{"value": amount, "currency_code": request.currency}}
	if request.orderType == "subscription" {
		path = "/v1/payments/sale/" + url.PathEscape(request.capture) + "/refund"
		refundPath = "/v1/payments/refund/"
		body["amount"] = map[string]string{"total": amount, "currency": request.currency}
	}
	method := http.MethodPost
	var content io.Reader
	if request.refundID != "" {
		method, path = http.MethodGet, refundPath+url.PathEscape(request.refundID)
	} else {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		content = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, host+path, content)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("PayPal-Request-Id", request.requestID)
	var response struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		State  string `json:"state"`
		Amount struct {
			Value        string `json:"value"`
			CurrencyCode string `json:"currency_code"`
			Total        string `json:"total"`
			Currency     string `json:"currency"`
		} `json:"amount"`
	}
	if err := readPayPalResponse(client, req, &response); err != nil {
		return nil, err
	}
	if request.orderType == "subscription" {
		return &paypalRefundResult{response.ID, response.State, response.Amount.Currency, response.Amount.Total}, nil
	}
	return &paypalRefundResult{response.ID, response.Status, response.Amount.CurrencyCode, response.Amount.Value}, nil
}

func readPayPalResponse(client *http.Client, req *http.Request, target any) error {
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return errors.New("PayPal returned HTTP " + strconv.Itoa(resp.StatusCode))
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(target)
}

func (s *billingService) RunRefundReconciliation(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.reconcileRefunds(ctx)
		}
	}
}

func (s *billingService) reconcileRefunds(ctx context.Context) {
	rows, err := db.DB.Query(ctx, `
		WITH batch AS (
			SELECT order_id FROM payment_refunds WHERE status='Pending'
			AND updated_at < now() - interval '5 minutes'
			ORDER BY updated_at,order_id LIMIT 5 FOR UPDATE SKIP LOCKED
		)
		UPDATE payment_refunds r SET updated_at=now()
		FROM batch WHERE r.order_id=batch.order_id RETURNING r.order_id`)
	if err != nil {
		return
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return
	}
	for _, id := range ids {
		requestCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
		request, err := s.beginPayPalRefund(requestCtx, strconv.FormatInt(id, 10), nil)
		if err == nil && request != nil {
			_ = s.submitPayPalRefund(requestCtx, request)
		}
		cancel()
	}
}

func (s *billingService) failPayPalRefund(ctx context.Context, request *refundRequest, result *paypalRefundResult) error {
	tx, err := s.repo.BeginTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM recharge_orders WHERE id=$1 FOR UPDATE`, request.orderID).Scan(&status); err != nil {
		return err
	}
	if status != "RefundPending" {
		return nil
	}
	var priorStatus string
	err = tx.QueryRow(ctx, `UPDATE payment_refunds SET status='Failed',provider_refund_id=$3,updated_at=now()
		WHERE order_id=$1 AND request_id=$2 AND status='Pending'
		AND (provider_refund_id IS NULL OR provider_refund_id=$3)
		RETURNING prior_order_status`, request.orderID, request.requestID, result.ID).Scan(&priorStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO payment_refund_failures(request_id,order_id,provider_refund_id,provider_status,admin_id)
		SELECT request_id,order_id,provider_refund_id,$2,admin_id FROM payment_refunds WHERE order_id=$1`,
		request.orderID, result.Status)
	if err != nil {
		return err
	}
	if err := s.repo.UpdateOrderStatusTx(ctx, tx, strconv.FormatInt(request.orderID, 10), priorStatus); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
