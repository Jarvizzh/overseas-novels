package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"reader-backend/internal/config"
)

// TC-RDR-01 (CORS check): Ensure X-Recharge-Template-ID is not in reader gateway Access-Control-Allow-Headers.
func TestCORSAllowHeadersOmitsTemplateID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	orig := config.AppConfig
	config.AppConfig = &config.Config{DefaultDomain: "https://reader.example.invalid"}
	t.Cleanup(func() { config.AppConfig = orig })

	r := gin.New()
	r.Use(corsMiddleware())
	r.GET("/ping", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodOptions, "/ping", nil)
	req.Header.Set("Origin", "https://reader.example.invalid")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204 preflight, got %d", rec.Code)
	}
	allowHeaders := rec.Header().Get("Access-Control-Allow-Headers")
	if strings.Contains(strings.ToLower(allowHeaders), "x-recharge-template-id") {
		t.Fatalf("Access-Control-Allow-Headers still exposes X-Recharge-Template-ID: %q", allowHeaders)
	}
}
