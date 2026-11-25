package application

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"reader-backend/internal/payment"
	"reader-backend/internal/wallet"
)

func TestPaymentAndWalletKeepReaderRoutes(t *testing.T) {
	router := gin.New()
	api := router.Group("/api")
	wallet.NewHandler(wallet.NewService(nil, nil)).RegisterRoutes(api)
	NewHandler(NewService(nil, nil, payment.Adapters{})).RegisterRoutes(api)

	routes := make(map[string]bool)
	for _, route := range router.Routes() {
		routes[route.Method+" "+route.Path] = true
	}
	for _, path := range []string{
		"GET /api/wallet/balance",
		"GET /api/wallet/history",
		"GET /api/wallet/history/cursor",
		"POST /api/wallet/unlock",
		"GET /api/wallet/rewards/checkin",
		"POST /api/wallet/rewards/checkin",
		"POST /api/wallet/recharge/initiate",
		"POST /api/wallet/recharge/paypal/create-order",
		"POST /api/wallet/recharge/paypal/capture",
		"POST /api/wallet/purchase-intents",
		"GET /api/wallet/purchase-methods",
		"GET /api/wallet/purchase-intents",
		"GET /api/wallet/purchase-intents/return",
		"GET /api/wallet/purchase-intents/:intent",
		"POST /api/wallet/purchase-intents/:intent/checkout",
		"POST /api/wallet/purchase-intents/:intent/checkout/stripe",
		"POST /api/wallet/subscription/stripe/checkout",
		"GET /api/wallet/subscription/stripe/purchase/:intent",
		"POST /api/wallet/subscription/stripe/portal",
		"POST /api/wallet/recharge/subscription/create",
		"POST /api/wallet/recharge/subscription/activate",
		"POST /api/wallet/recharge/paypal/create-subscription",
		"POST /api/wallet/recharge/paypal/activate-subscription",
		"GET /api/wallet/subscription/status",
		"GET /api/wallet/subscription/purchase-return",
		"POST /api/wallet/subscription/cancel",
		"GET /api/wallet/recharge/templates",
		"POST /api/wallet/webhook/paypal",
		"POST /api/wallet/webhook/subscription/:provider",
	} {
		if !routes[path] {
			t.Errorf("missing existing Reader route %s", path)
		}
	}

	for _, tc := range []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/api/wallet/balance", http.StatusUnauthorized},
		{http.MethodPost, "/api/wallet/recharge/paypal/capture", http.StatusUnauthorized},
		{http.MethodGet, "/api/wallet/subscription/status", http.StatusUnauthorized},
		{http.MethodPost, "/api/wallet/webhook/paypal", http.StatusServiceUnavailable},
		{http.MethodPost, "/api/wallet/webhook/subscription/paypal", http.StatusServiceUnavailable},
	} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(tc.method, tc.path, nil))
		if recorder.Code != tc.want {
			t.Errorf("%s %s returned %d, want %d", tc.method, tc.path, recorder.Code, tc.want)
		}
	}
}
