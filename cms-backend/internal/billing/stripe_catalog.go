package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"star-novel-cms/internal/db"
)

const stripeCatalogAPIVersion = "2026-11-18.endive"

var (
	errStripeCatalog  = errors.New("Stripe verification failed")
	stripeCatalogHTTP = &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
)

type stripeCatalogPrice struct {
	ID          string `json:"id"`
	Currency    string `json:"currency"`
	Kind        string `json:"type"`
	TaxBehavior string `json:"tax_behavior"`
	UnitAmount  int64  `json:"unit_amount"`
	Active      bool   `json:"active"`
	Live        bool   `json:"livemode"`
	Recurring   *struct {
		Interval      string `json:"interval"`
		IntervalCount int    `json:"interval_count"`
	} `json:"recurring"`
	Product struct {
		ID      string `json:"id"`
		Active  bool   `json:"active"`
		Live    bool   `json:"livemode"`
		TaxCode string `json:"tax_code"`
	} `json:"product"`
}

func getStripeCatalogObject(ctx context.Context, path, key string, output any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.stripe.com/v1/"+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Stripe-Version", stripeCatalogAPIVersion)
	res, err := stripeCatalogHTTP.Do(req)
	if err != nil {
		return fmt.Errorf("%w: Stripe API unavailable", errStripeCatalog)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: Stripe API returned %d", errStripeCatalog, res.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(output); err != nil {
		return fmt.Errorf("%w: invalid Stripe catalog response", errStripeCatalog)
	}
	return nil
}

func verifyStripeOffer(ctx context.Context, accountID int64, actor string, input stripeVIPInput) (*stripeCatalogPrice, error) {
	if !*input.Enabled {
		return nil, nil
	}
	var account, environment string
	err := db.DB.QueryRow(ctx, `SELECT account_key,environment FROM payment_channel_accounts
		WHERE id=$1 AND provider='stripe' AND EXISTS(SELECT 1 FROM admins WHERE id=$2 AND role='SuperAdmin' AND status=1)`,
		accountID, actor).Scan(&account, &environment)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errCaseForbidden
	}
	if err != nil {
		return nil, err
	}
	live := environment == "live"
	key, prefix := os.Getenv("STRIPE_CATALOG_SANDBOX_API_KEY"), "test_"
	if live {
		key, prefix = os.Getenv("STRIPE_CATALOG_LIVE_API_KEY"), "live_"
		if strings.TrimSpace(input.ReleaseEvidence) == "" || strings.TrimSpace(input.MarketPolicy) == "" {
			return nil, fmt.Errorf("%w: Live offers require approval evidence and a market policy", errStripeCatalog)
		}
	}
	if !live && key == "" {
		return nil, nil
	}
	if !strings.HasPrefix(key, "rk_"+prefix) && !strings.HasPrefix(key, "sk_"+prefix) {
		return nil, fmt.Errorf("%w: the catalog credential does not match the environment", errStripeCatalog)
	}
	var merchant struct {
		ID string `json:"id"`
	}
	if err := getStripeCatalogObject(ctx, "account", key, &merchant); err != nil {
		return nil, err
	}
	if merchant.ID != account {
		return nil, fmt.Errorf("%w: the credential belongs to another merchant", errStripeCatalog)
	}
	var price stripeCatalogPrice
	if err := getStripeCatalogObject(ctx, "prices/"+input.PriceRef+"?expand%5B%5D=product", key, &price); err != nil {
		return nil, err
	}
	if price.ID != input.PriceRef || price.Product.ID != input.ProductRef || !price.Active || !price.Product.Active ||
		price.Live != live || price.Product.Live != live || price.Currency != "usd" || price.UnitAmount <= 0 {
		return nil, fmt.Errorf("%w: Stripe Price and Product must be active and scoped to this USD account", errStripeCatalog)
	}
	if input.SalesMode == stripeManagedPayments && (price.Product.TaxCode == "" ||
		(live && price.TaxBehavior != "inclusive" && price.TaxBehavior != "exclusive")) {
		return nil, fmt.Errorf("%w: Managed Payments requires a tax code and an explicit tax price policy", errStripeCatalog)
	}
	return &price, nil
}

func stripeCatalogJSON(price *stripeCatalogPrice, amount int64, cycle string) (json.RawMessage, error) {
	if price == nil {
		return nil, nil
	}
	if price.UnitAmount != amount {
		return nil, fmt.Errorf("%w: Stripe price differs from the published package", errStripeCatalog)
	}
	if cycle == "" && price.Kind != "one_time" || cycle != "" &&
		(price.Kind != "recurring" || price.Recurring == nil || price.Recurring.IntervalCount != 1 || price.Recurring.Interval != cycle) {
		return nil, fmt.Errorf("%w: Stripe billing frequency differs from the published package", errStripeCatalog)
	}
	return json.Marshal(price)
}
