package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
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
	return doStripeCatalogFormRequest(ctx, http.MethodGet, path, key, "", nil, output)
}

func doStripeCatalogFormRequest(ctx context.Context, method, path, key, idemKey string, form url.Values, output any) error {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, stripeAPIBaseURL+"/v1/"+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Stripe-Version", stripeCatalogAPIVersion)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if idemKey != "" {
		req.Header.Set("Idempotency-Key", idemKey)
	}
	res, err := stripeCatalogHTTP.Do(req)
	if err != nil {
		return fmt.Errorf("%w: Stripe API unavailable", errStripeCatalog)
	}
	defer res.Body.Close()
	rawBody, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("%w: failed to read Stripe response", errStripeCatalog)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		var apiErr struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(rawBody, &apiErr) == nil && strings.TrimSpace(apiErr.Error.Message) != "" {
			return fmt.Errorf("%w: Stripe API returned %d: %s", errStripeCatalog, res.StatusCode, strings.TrimSpace(apiErr.Error.Message))
		}
		return fmt.Errorf("%w: Stripe API returned %d", errStripeCatalog, res.StatusCode)
	}
	if output != nil {
		if err := json.Unmarshal(rawBody, output); err != nil {
			return fmt.Errorf("%w: invalid Stripe catalog response", errStripeCatalog)
		}
	}
	return nil
}

func createStripeCatalogProduct(
	ctx context.Context, key, idemKey, name, desc, env, code string, productID int64, productType, taxCode string,
) (string, error) {
	form := url.Values{
		"name":                             {name},
		"active":                           {"true"},
		"metadata[starnovel_product_id]":   {strconv.FormatInt(productID, 10)},
		"metadata[starnovel_product_code]": {code},
		"metadata[starnovel_product_type]": {productType},
		"metadata[starnovel_environment]":  {env},
	}
	if strings.TrimSpace(desc) != "" {
		form.Set("description", strings.TrimSpace(desc))
	}
	if strings.TrimSpace(taxCode) != "" {
		form.Set("tax_code", strings.TrimSpace(taxCode))
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := doStripeCatalogFormRequest(ctx, http.MethodPost, "products", key, idemKey, form, &out); err != nil {
		return "", err
	}
	if !strings.HasPrefix(out.ID, "prod_") {
		return "", fmt.Errorf("%w: Stripe did not return a valid product ID", errStripeCatalog)
	}
	return out.ID, nil
}

func updateStripeCatalogProductTaxCode(ctx context.Context, key, prodID, taxCode string) error {
	form := url.Values{"tax_code": {strings.TrimSpace(taxCode)}}
	return doStripeCatalogFormRequest(ctx, http.MethodPost, "products/"+url.PathEscape(prodID), key, "", form, nil)
}

func createStripeCatalogPrice(
	ctx context.Context, key, idemKey, prodID string, priceCents int, productType, vipCycle, env, code string, productID int64, taxBehavior string,
) (string, error) {
	form := url.Values{
		"product":                          {prodID},
		"currency":                         {"usd"},
		"unit_amount":                      {strconv.Itoa(priceCents)},
		"active":                           {"true"},
		"metadata[starnovel_product_id]":   {strconv.FormatInt(productID, 10)},
		"metadata[starnovel_product_code]": {code},
		"metadata[starnovel_product_type]": {productType},
		"metadata[starnovel_environment]":  {env},
	}
	if productType == productTypeVIP {
		form.Set("recurring[interval]", vipCycle)
		form.Set("recurring[interval_count]", "1")
	}
	if strings.TrimSpace(taxBehavior) != "" {
		form.Set("tax_behavior", strings.TrimSpace(taxBehavior))
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := doStripeCatalogFormRequest(ctx, http.MethodPost, "prices", key, idemKey, form, &out); err != nil {
		return "", err
	}
	if !strings.HasPrefix(out.ID, "price_") {
		return "", fmt.Errorf("%w: Stripe did not return a valid price ID", errStripeCatalog)
	}
	return out.ID, nil
}

func verifyStripeOffer(ctx context.Context, accountID int64, actor string, input stripeVIPInput) (*stripeCatalogPrice, error) {
	if !*input.Enabled {
		return nil, nil
	}
	key, account, environment, credRef, kErr := loadChannelStripeKey(ctx, accountID, 0)
	if credRef == "" {
		return nil, errCaseForbidden
	}
	live := environment == "live"
	prefix := "test_"
	if live {
		prefix = "live_"
		if strings.TrimSpace(input.ReleaseEvidence) == "" || strings.TrimSpace(input.MarketPolicy) == "" {
			return nil, fmt.Errorf("%w: Live offers require approval evidence and a market policy", errStripeCatalog)
		}
	}
	if !live && credRef != credRefDBEncrypted && key == "" {
		return nil, nil
	}
	if kErr != nil {
		return nil, kErr
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
