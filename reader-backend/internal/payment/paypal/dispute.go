package paypal

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"time"

	"reader-backend/internal/payment"
)

const disputesPath = "/v1/customer/disputes"

type Dispute struct {
	Account           payment.WebhookAccount `json:"-"`
	ID                string                 `json:"dispute_id"`
	Status            string                 `json:"status"`
	State             string                 `json:"dispute_state"`
	Stage             string                 `json:"dispute_life_cycle_stage"`
	Channel           string                 `json:"dispute_channel"`
	Reason            string                 `json:"reason"`
	ExternalReason    string                 `json:"external_reason_code"`
	CreatedAt         time.Time              `json:"create_time"`
	UpdatedAt         time.Time              `json:"update_time"`
	SellerResponseDue *time.Time             `json:"seller_response_due_date"`
	BuyerResponseDue  *time.Time             `json:"buyer_response_due_date"`
	Amount            *Amount                `json:"dispute_amount"`
	Outcome           *DisputeOutcome        `json:"dispute_outcome"`
	Transactions      []DisputedTransaction  `json:"disputed_transactions"`
	MoneyMovements    json.RawMessage        `json:"money_movements"`
	FundMovements     json.RawMessage        `json:"fund_movements"`
	Links             []Link                 `json:"links"`
	Raw               json.RawMessage        `json:"-"`
}

type DisputeOutcome struct {
	Code           string  `json:"outcome_code"`
	Reason         string  `json:"outcome_reason"`
	AmountRefunded *Amount `json:"amount_refunded"`
}

type DisputedTransaction struct {
	SellerTransactionID string  `json:"seller_transaction_id"`
	BuyerTransactionID  string  `json:"buyer_transaction_id"`
	ReferenceID         string  `json:"reference_id"`
	Status              string  `json:"transaction_status"`
	Gross               *Amount `json:"gross_amount"`
}

// AvailableActions returns the action relations PayPal currently advertises.
func (d Dispute) AvailableActions() []string {
	var actions []string
	for _, link := range d.Links {
		if link.Method == "POST" || link.Method == "PATCH" {
			actions = append(actions, link.Rel)
		}
	}
	return actions
}

type DisputeQuery struct {
	UpdatedFrom, UpdatedBefore time.Time
	TransactionID              string
	PageSize                   int
	Cursor                     string
}

type DisputePage struct {
	Items []Dispute
	Next  string
}

func (p *Client) GetDispute(ctx context.Context, id string) (Dispute, error) {
	if !validObjectID(id) {
		return Dispute{}, errors.New("invalid PayPal dispute ID")
	}
	var raw json.RawMessage
	account, err := p.read(ctx, disputesPath+"/"+url.PathEscape(id), &raw)
	if err != nil {
		return Dispute{}, err
	}
	dispute, err := decodeDispute(account, raw)
	if err != nil {
		return Dispute{}, err
	}
	if dispute.ID != id {
		return Dispute{}, errors.New("unexpected PayPal dispute ID")
	}
	return dispute, nil
}

func (p *Client) ListDisputes(ctx context.Context, opts DisputeQuery) (DisputePage, error) {
	query, err := pageQuery(opts.Cursor)
	if err != nil {
		return DisputePage{}, err
	}
	if opts.Cursor == "" {
		if opts.PageSize < 0 || opts.PageSize > 50 ||
			(!opts.UpdatedFrom.IsZero() && !opts.UpdatedBefore.IsZero() && opts.UpdatedBefore.Before(opts.UpdatedFrom)) {
			return DisputePage{}, errors.New("invalid PayPal dispute query")
		}
		if opts.PageSize > 0 {
			query.Set("page_size", strconv.Itoa(opts.PageSize))
		}
		if opts.TransactionID != "" {
			if !validObjectID(opts.TransactionID) {
				return DisputePage{}, errors.New("invalid PayPal dispute transaction ID")
			}
			query.Set("disputed_transaction_id", opts.TransactionID)
		}
		if !opts.UpdatedFrom.IsZero() {
			query.Set("update_time_after", opts.UpdatedFrom.UTC().Format(time.RFC3339Nano))
		}
		if !opts.UpdatedBefore.IsZero() {
			query.Set("update_time_before", opts.UpdatedBefore.UTC().Format(time.RFC3339Nano))
		}
	}
	var response struct {
		Items []json.RawMessage `json:"items"`
		Links []Link            `json:"links"`
	}
	account, err := p.read(ctx, endpoint(disputesPath, query), &response)
	if err != nil {
		return DisputePage{}, err
	}
	cursor, err := nextPage(response.Links, apiBaseURL(account.Environment), disputesPath)
	if err != nil {
		return DisputePage{}, err
	}
	page := DisputePage{Next: cursor}
	for _, raw := range response.Items {
		dispute, err := decodeDispute(account, raw)
		if err != nil {
			return DisputePage{}, err
		}
		page.Items = append(page.Items, dispute)
	}
	return page, nil
}

func decodeDispute(account payment.WebhookAccount, raw json.RawMessage) (Dispute, error) {
	dispute := Dispute{Account: account, Raw: raw}
	if err := json.Unmarshal(raw, &dispute); err != nil {
		return Dispute{}, err
	}
	if dispute.ID == "" {
		return Dispute{}, errors.New("missing PayPal dispute ID")
	}
	return dispute, nil
}
