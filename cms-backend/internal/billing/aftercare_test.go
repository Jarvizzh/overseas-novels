package billing

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func testAftercareRoutes(t *testing.T, service BillingService, exec func(string, ...any)) {
	t.Helper()
	exec(`INSERT INTO payment_cases(id,account_id,kind,external_id,receipt_refs,provider_status,is_open,observed_at)
		OVERRIDING SYSTEM VALUE VALUES (5101,901,'dispute','du_CMS',ARRAY['ch_fin03']::text[],'lost',false,now());`)
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("admin_id", c.GetHeader("X-Test-Actor")); c.Next() })
	NewHandler(service).registerAftercareRoutes(router.Group("/payments"), paymentRoles("SuperAdmin", "Finance"))
	check := func(actor, method, path, body string, status int) string {
		t.Helper()
		req := httptest.NewRequest(method, "/payments/cases"+path, strings.NewReader(body))
		req.Header.Set("X-Test-Actor", actor)
		req.Header.Set("Content-Type", "application/json")
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		if res.Code != status {
			t.Fatalf("%s %s got %d: %s", method, path, res.Code, res.Body.String())
		}
		return res.Body.String()
	}
	for _, path := range []string{"?view=disputes", "/5101", "/owners", "/decisions"} {
		check("test-support", http.MethodGet, path, "", http.StatusForbidden)
	}
	found := check("test-finance", http.MethodGet, "?view=disputes&environment=live&order_id=5000000001", "", http.StatusOK)
	if !strings.Contains(found, "du_CMS") {
		t.Fatalf("order did not link to dispute: %s", found)
	}
	found = check("test-finance", http.MethodGet, "/5101", "", http.StatusOK)
	if !strings.Contains(found, "ch_fin03") || strings.Contains(found, "private-fin03") {
		t.Fatalf("case detail projection: %s", found)
	}
	body := fmt.Sprintf(`{"request_id":"50000000-0000-4000-8000-000000000001","action":"assign","owner":"test-finance","reason":%q}`, "fixture")
	check("test-support", http.MethodPost, "/5101/actions", body, http.StatusForbidden)
	check("test-finance", http.MethodPost, "/5101/actions", body, http.StatusOK)
	check("test-finance", http.MethodPost, "/5101/actions", body, http.StatusOK)
	found = check("test-finance", http.MethodGet, "/5101", "", http.StatusOK)
	if strings.Count(found, `"action": "assign"`)+strings.Count(found, `"action":"assign"`) != 1 {
		t.Fatalf("case audit replay: %s", found)
	}
}
