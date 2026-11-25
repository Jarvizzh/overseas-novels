package application

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"reader-backend/internal/payment"
)

type testCatalogSpec struct {
	ProductID        int64
	SlotID           int
	TemplateID       int
	TemplateName     string
	IsDefault        bool
	SlotIndex        int
	Code             string
	ProductType      string // "single", "whole_book", "vip"
	PriceCents       int
	Coins            int
	Bonus            int
	VipCycle         string
	NovelScopeType   string
	NovelScopeIDs    []int64
	Badge            string
	IsHighlighted    bool
	DisplayAlias     string
	Environment      string // "sandbox" or "live"; default "sandbox"
	PayPalAccountKey string
	PayPalPlanID     string
	StripeAccountKey string
	StripeProductRef string
	StripePriceRef   string
	StripeSalesMode  string
	StripeTaxAccount string
	AllowedCountries []string
	PublishStatus    string // default "published"
	SkipPayPalBind   bool
	SkipStripeBind   bool
}

func seedTestProductAndSlot(t *testing.T, pool *pgxpool.Pool, spec testCatalogSpec) int64 {
	t.Helper()
	ctx := t.Context()
	env := spec.Environment
	if env == "" {
		env = currentPaymentLedgerEnvironment()
	}
	pubStatus := spec.PublishStatus
	if pubStatus == "" {
		pubStatus = "published"
	}
	if spec.TemplateID <= 0 {
		spec.TemplateID = 1
	}
	if spec.SlotIndex <= 0 {
		spec.SlotIndex = 1
	}
	if spec.TemplateName == "" {
		spec.TemplateName = fmt.Sprintf("Template %d", spec.TemplateID)
	}
	if spec.Code == "" {
		if spec.ProductID > 0 {
			spec.Code = fmt.Sprintf("TEST_PROD_%d", spec.ProductID)
		} else {
			spec.Code = fmt.Sprintf("TEST_SLOT_%d_%s", spec.SlotID, spec.ProductType)
		}
	}
	if spec.ProductType == "whole_book" || spec.ProductType == "vip" {
		spec.Coins = 0
		spec.Bonus = 0
	}
	if spec.ProductType == "whole_book" && spec.NovelScopeType == "" {
		spec.NovelScopeType = "all_published"
	}
	if spec.ProductType != "whole_book" {
		spec.NovelScopeType = "none"
	}
	if spec.NovelScopeIDs == nil {
		spec.NovelScopeIDs = []int64{}
	}
	if spec.StripeSalesMode == "" {
		spec.StripeSalesMode = payment.StripeSaleDirect
	}
	if len(spec.AllowedCountries) == 0 {
		spec.AllowedCountries = []string{"US"}
	}
	if spec.PayPalAccountKey == "" {
		spec.PayPalAccountKey = "fixture-paypal-" + env
	}
	if spec.StripeAccountKey == "" {
		spec.StripeAccountKey = "fixture-stripe-" + env
	}
	if spec.StripeProductRef == "" {
		spec.StripeProductRef = fmt.Sprintf("prod_%s_%d", spec.ProductType, spec.SlotID)
	}
	if spec.StripePriceRef == "" {
		spec.StripePriceRef = fmt.Sprintf("price_%s_%d", spec.ProductType, spec.SlotID)
	}
	if spec.ProductType == "vip" && spec.PayPalPlanID == "" {
		spec.PayPalPlanID = fmt.Sprintf("P-VIP-%d", spec.SlotID)
	}

	var vipCycle any
	if spec.ProductType == "vip" {
		if spec.VipCycle == "" {
			spec.VipCycle = "month"
		}
		vipCycle = spec.VipCycle
	}

	var productID int64
	if spec.ProductID > 0 {
		if _, err := pool.Exec(ctx, `
			INSERT INTO products (id, code, product_type, name, description, price_cents, currency, coins, bonus, vip_cycle, novel_scope_type, novel_scope_ids)
			VALUES ($1, $2, $3, $2, 'Test product', $4, 'USD', $5, $6, $7, $8, $9)
			ON CONFLICT DO NOTHING`,
			spec.ProductID, spec.Code, spec.ProductType, spec.PriceCents, spec.Coins, spec.Bonus,
			vipCycle, spec.NovelScopeType, spec.NovelScopeIDs,
		); err != nil {
			t.Fatalf("seed product %s: %v", spec.Code, err)
		}
		productID = spec.ProductID
	} else {
		_, err := pool.Exec(ctx, `
			INSERT INTO products (code, product_type, name, description, price_cents, currency, coins, bonus, vip_cycle, novel_scope_type, novel_scope_ids)
			VALUES ($1, $2, $1, 'Test product', $3, 'USD', $4, $5, $6, $7, $8)
			ON CONFLICT (code) DO NOTHING`,
			spec.Code, spec.ProductType, spec.PriceCents, spec.Coins, spec.Bonus,
			vipCycle, spec.NovelScopeType, spec.NovelScopeIDs,
		)
		if err != nil {
			t.Fatalf("seed product %s: %v", spec.Code, err)
		}
		if err := pool.QueryRow(ctx, `SELECT id FROM products WHERE code = $1`, spec.Code).Scan(&productID); err != nil {
			t.Fatalf("lookup product %s: %v", spec.Code, err)
		}
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO product_publications (product_id, environment, status, published_at)
		VALUES ($1, $2, $3, CASE WHEN $3 = 'published' THEN now() ELSE NULL END)
		ON CONFLICT (product_id, environment) DO UPDATE SET status = EXCLUDED.status, updated_at = now()`,
		productID, env, pubStatus); err != nil {
		t.Fatalf("seed publication %d: %v", productID, err)
	}

	if !spec.SkipPayPalBind {
		var ppAccountID int64
		err := pool.QueryRow(ctx, `SELECT id FROM payment_channel_accounts WHERE provider='paypal' AND account_key=$1 AND environment=$2`,
			spec.PayPalAccountKey, env).Scan(&ppAccountID)
		if err != nil {
			err = pool.QueryRow(ctx, `
				INSERT INTO payment_channel_accounts (provider, account_key, environment, credentials_ref, webhook_ref, display_name, status, is_primary, new_orders_enabled, new_subscriptions_enabled)
				VALUES ('paypal', $1, $2, 'test', 'test', $1, 'published', true, true, true)
				ON CONFLICT (provider, account_key, environment) DO UPDATE SET
				  status = 'published', is_primary = true, new_orders_enabled = true, new_subscriptions_enabled = true
				RETURNING id`, spec.PayPalAccountKey, env).Scan(&ppAccountID)
			if err != nil {
				t.Fatalf("ensure paypal account: %v", err)
			}
		} else {
			if _, err := pool.Exec(ctx, `
				UPDATE payment_channel_accounts
				SET status = 'published',
				    new_orders_enabled = true,
				    new_subscriptions_enabled = true,
				    is_primary = CASE
				        WHEN EXISTS (SELECT 1 FROM payment_channel_accounts other WHERE other.provider='paypal' AND other.environment=$2 AND other.is_primary=true AND other.id<>$1)
				        THEN is_primary
				        ELSE true
				    END
				WHERE id = $1`, ppAccountID, env); err != nil {
				t.Fatalf("publish paypal account %d: %v", ppAccountID, err)
			}
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO product_channel_bindings (product_id, account_id, provider, environment, external_price_ref, enabled, validation_status, validated_at)
			VALUES ($1, $2, 'paypal', $3, $4, true, 'verified', now())
			ON CONFLICT (product_id, account_id) DO UPDATE SET
			  external_price_ref = EXCLUDED.external_price_ref, enabled = true, validation_status = 'verified', validated_at = now()`,
			productID, ppAccountID, env, spec.PayPalPlanID); err != nil {
			t.Fatalf("seed paypal binding: %v", err)
		}
	}

	if !spec.SkipStripeBind {
		var stAccountID int64
		err := pool.QueryRow(ctx, `SELECT id FROM payment_channel_accounts WHERE provider='stripe' AND account_key=$1 AND environment=$2`,
			spec.StripeAccountKey, env).Scan(&stAccountID)
		if err != nil {
			err = pool.QueryRow(ctx, `
				INSERT INTO payment_channel_accounts (provider, account_key, environment, credentials_ref, webhook_ref, display_name, status, is_primary, new_orders_enabled, new_subscriptions_enabled)
				VALUES ('stripe', $1, $2, 'test', 'test', $1, 'published', true, true, true)
				ON CONFLICT (provider, account_key, environment) DO UPDATE SET
				  status = 'published', is_primary = true, new_orders_enabled = true, new_subscriptions_enabled = true
				RETURNING id`, spec.StripeAccountKey, env).Scan(&stAccountID)
			if err != nil {
				t.Fatalf("ensure stripe account: %v", err)
			}
		} else {
			if _, err := pool.Exec(ctx, `
				UPDATE payment_channel_accounts
				SET status = 'published',
				    new_orders_enabled = true,
				    new_subscriptions_enabled = true,
				    is_primary = CASE
				        WHEN EXISTS (SELECT 1 FROM payment_channel_accounts other WHERE other.provider='stripe' AND other.environment=$2 AND other.is_primary=true AND other.id<>$1)
				        THEN is_primary
				        ELSE true
				    END
				WHERE id = $1`, stAccountID, env); err != nil {
				t.Fatalf("publish stripe account %d: %v", stAccountID, err)
			}
		}
		var valCatalog, releaseEv, mktPolicy any
		if env == "live" {
			rawCat, _ := json.Marshal(map[string]any{"product_ref": spec.StripeProductRef, "price_ref": spec.StripePriceRef})
			valCatalog = string(rawCat)
			ev := "test-evidence"
			pol := "test-policy"
			releaseEv = ev
			mktPolicy = pol
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO product_channel_bindings (
				product_id, account_id, provider, environment, external_product_ref, external_price_ref,
				sales_mode, allowed_countries, tax_liability_account, validated_catalog, release_evidence, market_policy,
				enabled, validation_status, validated_at
			)
			VALUES ($1, $2, 'stripe', $3, $4, $5, $6, $7, NULLIF($8, ''), $9::jsonb, $10, $11, true, 'verified', now())
			ON CONFLICT (product_id, account_id) DO UPDATE SET
			  external_product_ref = EXCLUDED.external_product_ref,
			  external_price_ref = EXCLUDED.external_price_ref,
			  sales_mode = EXCLUDED.sales_mode,
			  allowed_countries = EXCLUDED.allowed_countries,
			  tax_liability_account = EXCLUDED.tax_liability_account,
			  validated_catalog = COALESCE(EXCLUDED.validated_catalog, product_channel_bindings.validated_catalog),
			  release_evidence = COALESCE(EXCLUDED.release_evidence, product_channel_bindings.release_evidence),
			  market_policy = COALESCE(EXCLUDED.market_policy, product_channel_bindings.market_policy),
			  enabled = true, validation_status = 'verified', validated_at = now()`,
			productID, stAccountID, env, spec.StripeProductRef, spec.StripePriceRef,
			spec.StripeSalesMode, spec.AllowedCountries, spec.StripeTaxAccount,
			valCatalog, releaseEv, mktPolicy); err != nil {
			t.Fatalf("seed stripe binding: %v", err)
		}
	}

	if spec.SlotID > 0 {
		if _, err := pool.Exec(ctx, `
			INSERT INTO recharge_templates (id, name, is_default)
			VALUES ($1, $2, $3)
			ON CONFLICT (id) DO NOTHING`, spec.TemplateID, spec.TemplateName, spec.IsDefault); err != nil {
			t.Fatalf("ensure template %d: %v", spec.TemplateID, err)
		}
		slotOrderType := spec.ProductType
		if _, err := pool.Exec(ctx, `
			INSERT INTO recharge_slots (id, template_id, slot_index, product_id, type, price, price_cents, coins, bonus, subscription_cycle, badge, is_highlighted, display_alias)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
			ON CONFLICT (id) DO UPDATE SET
			  product_id = EXCLUDED.product_id,
			  badge = EXCLUDED.badge,
			  is_highlighted = EXCLUDED.is_highlighted,
			  display_alias = EXCLUDED.display_alias`,
			spec.SlotID, spec.TemplateID, spec.SlotIndex, productID, slotOrderType,
			formatUSDPrice(spec.PriceCents), spec.PriceCents, spec.Coins, spec.Bonus,
			vipCycle, spec.Badge, spec.IsHighlighted, spec.DisplayAlias); err != nil {
			t.Fatalf("seed slot %d: %v", spec.SlotID, err)
		}
	}
	return productID
}
