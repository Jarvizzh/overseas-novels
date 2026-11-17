package payment

import (
	"context"
	"reader-backend/internal/testdb"
	"testing"
)

func TestPayPalIdentityCannotSwitch(t *testing.T) {
	p := testdb.Open(t)
	ctx := context.Background()
	if _, err := p.Exec(ctx, `INSERT INTO system_configs(key,value) VALUES('paypal_client_id','merchant-original'),('paypal_secret_key','secret'),('paypal_mode','sandbox')`); err != nil {
		t.Fatal(err)
	}
	ppCache.Lock()
	ppCache.lastFetched = ppCache.lastFetched.AddDate(-1, 0, 0)
	ppCache.Unlock()
	id, _, _, _ := NewPayPalClient().getConfig(ctx)
	if id != "merchant-original" {
		t.Fatal("identity not bound")
	}
	for _, query := range []string{`SELECT assert_paypal_identity('different-merchant','sandbox')`, `SELECT assert_paypal_identity('merchant-original','live')`, `UPDATE system_configs SET value='live' WHERE key='paypal_mode'`} {
		if _, err := p.Exec(ctx, query); err == nil {
			t.Fatalf("unsafe switch allowed: %s", query)
		}
	}
	if _, err := p.Exec(ctx, `UPDATE system_configs SET value='rotated-secret' WHERE key='paypal_secret_key';INSERT INTO system_configs(key,value) VALUES('paypal_mode','sandbox') ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value`); err != nil {
		t.Fatalf("safe rotation/no-op rejected: %v", err)
	}
}
