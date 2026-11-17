package payment

import (
	"context"
	"errors"
)

// ValidateIdentity also guards resuming an existing approval URL, before the buyer can approve it.
func (p *PayPalClient) ValidateIdentity(ctx context.Context) error {
	id, secret, _, _ := p.getConfig(ctx)
	if id == "" || secret == "" || id == "paypal_client_id_placeholder" {
		return errors.New("PayPal identity unavailable or requires audited migration")
	}
	return nil
}
