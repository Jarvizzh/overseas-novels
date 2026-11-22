package paypal

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"reader-backend/internal/payment"
)

func (p *Client) ResolveRefund(ctx context.Context, input payment.RefundRequest) (*payment.RefundResult, error) {
	if !validObjectID(input.ReceiptID) || (input.RefundID != "" && !validObjectID(input.RefundID)) ||
		input.AmountMinor <= 0 || len(input.Currency) != 3 ||
		(input.Send && input.RefundID == "" && !validRequestKey(input.RequestKey)) {
		return nil, errors.New("invalid PayPal refund request")
	}
	switch input.ReceiptType {
	case payment.ReceiptCapture, payment.ReceiptSale:
	default:
		return nil, errors.New("unsupported PayPal refund receipt type")
	}
	cfg, err := p.credentials(ctx)
	if err != nil {
		return nil, err
	}
	if cfg.account() != input.Account {
		return nil, errors.New("PayPal refund account changed")
	}

	receiptPath := "/v2/payments/captures/" + url.PathEscape(input.ReceiptID)
	refundPath := "/v2/payments/refunds/"
	amount := formatAmount(input.AmountMinor, input.Currency)
	amountFields := map[string]string{"value": amount, "currency_code": input.Currency}
	if input.ReceiptType == payment.ReceiptSale {
		receiptPath = "/v1/payments/sale/" + url.PathEscape(input.ReceiptID)
		refundPath = "/v1/payments/refund/"
		amountFields = map[string]string{"total": amount, "currency": input.Currency}
	}
	method, path, requestID := http.MethodGet, receiptPath, ""
	var body any
	switch {
	case input.RefundID != "":
		path = refundPath + url.PathEscape(input.RefundID)
	case input.Send:
		method, path, requestID = http.MethodPost, receiptPath+"/refund", input.RequestKey
		body = map[string]any{"amount": amountFields}
	default:
		// A capture may confirm that money moved without identifying the individual refund.
		// Leave the command reserved until there is transaction-level evidence.
		return nil, p.send(ctx, cfg, method, path, "", nil, nil)
	}
	var response struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		State  string `json:"state"`
		SaleID string `json:"sale_id"`
		Amount struct {
			Value        string `json:"value"`
			CurrencyCode string `json:"currency_code"`
			Total        string `json:"total"`
			Currency     string `json:"currency"`
		} `json:"amount"`
	}
	if err := p.send(ctx, cfg, method, path, requestID, body, &response); err != nil {
		return nil, err
	}
	if response.ID == "" || (input.RefundID != "" && input.RefundID != response.ID) ||
		(response.SaleID != "" && response.SaleID != input.ReceiptID) {
		return nil, errors.New("PayPal refund reference does not match the original request")
	}
	status, amount, currency := response.Status, response.Amount.Value, response.Amount.CurrencyCode
	if input.ReceiptType == payment.ReceiptSale {
		status, amount, currency = response.State, response.Amount.Total, response.Amount.Currency
	}
	status = strings.ToUpper(status)
	minor, parseErr := parseAmountMinor(amount, currency)
	if status == "COMPLETED" && (parseErr != nil || currency != input.Currency || minor != input.AmountMinor) {
		return nil, errors.New("PayPal refund amount does not match the original request")
	}
	return &payment.RefundResult{ID: response.ID, Status: status, AmountMinor: minor, Currency: currency}, nil
}

func formatAmount(minor int64, currency string) string {
	if zeroDecimalCurrency(currency) {
		return strconv.FormatInt(minor, 10)
	}
	return fmt.Sprintf("%d.%02d", minor/100, minor%100)
}

func parseAmountMinor(value, currency string) (int64, error) {
	if zeroDecimalCurrency(currency) {
		if value == "" || strings.Trim(value, "0123456789") != "" {
			return 0, errors.New("invalid PayPal whole-unit refund amount")
		}
		return strconv.ParseInt(value, 10, 64)
	}
	if whole, fraction, ok := strings.Cut(value, "."); ok && len(fraction) > 2 {
		value = whole + "." + strings.TrimRight(fraction, "0")
	}
	return payment.ParseCents(value)
}

// These PayPal REST currencies use whole units for payment requests.
func zeroDecimalCurrency(currency string) bool {
	switch currency {
	case "HUF", "JPY", "TWD":
		return true
	default:
		return false
	}
}
