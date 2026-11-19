package tracking

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"star-novel-cms/internal/auth"
)

type conversionOrderClient struct {
	endpoint *url.URL
	token    string
	http     *http.Client
}

type adminLookup interface {
	GetAdminByID(context.Context, string) (*auth.Admin, error)
}

func RegisterConversionOrders(rg *gin.RouterGroup, admins adminLookup, rawURL, token string) error {
	var client *conversionOrderClient
	if rawURL != "" || token != "" {
		endpoint, err := url.Parse(rawURL)
		if err != nil || token == "" || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" ||
			endpoint.Fragment != "" || (endpoint.Scheme != "https" && endpoint.Scheme != "http") {
			return errors.New("invalid conversion private endpoint or token")
		}
		client = &conversionOrderClient{endpoint: endpoint, token: token, http: &http.Client{
			Timeout:       5 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}}
	}
	service := conversionOrderService{admins: admins, client: client}
	for _, route := range []struct {
		method, pattern string
		path            func(*gin.Context) []string
	}{
		{http.MethodGet, "/conversion/orders/:order_id", func(c *gin.Context) []string { return []string{"orders", c.Param("order_id")} }},
		{http.MethodGet, "/conversion/summary", func(*gin.Context) []string { return []string{"summary"} }},
		{http.MethodGet, "/conversion/events", func(*gin.Context) []string { return []string{"events"} }},
		{http.MethodGet, "/conversion/events/:id", func(c *gin.Context) []string { return []string{"events", c.Param("id")} }},
		{http.MethodGet, "/conversion/events/:id/destinations", func(c *gin.Context) []string { return []string{"events", c.Param("id"), "destinations"} }},
		{http.MethodPost, "/conversion/events/:id/deliveries", func(c *gin.Context) []string { return []string{"events", c.Param("id"), "deliveries"} }},
		{http.MethodPost, "/conversion/deliveries/:id/retry", func(c *gin.Context) []string { return []string{"deliveries", c.Param("id"), "retry"} }},
		{http.MethodPost, "/conversion/partner-reports/:id/retry", func(c *gin.Context) []string { return []string{"partner-reports", c.Param("id"), "retry"} }},
		{http.MethodGet, "/conversion/integrations", func(*gin.Context) []string { return []string{"integrations"} }},
		{http.MethodPost, "/conversion/integrations", func(*gin.Context) []string { return []string{"integrations"} }},
		{http.MethodPatch, "/conversion/integrations/:id", func(c *gin.Context) []string { return []string{"integrations", c.Param("id")} }},
		{http.MethodGet, "/conversion/destinations", func(*gin.Context) []string { return []string{"destinations"} }},
		{http.MethodPost, "/conversion/destinations", func(*gin.Context) []string { return []string{"destinations"} }},
		{http.MethodPatch, "/conversion/destinations/:id", func(c *gin.Context) []string { return []string{"destinations", c.Param("id")} }},
	} {
		rg.Handle(route.method, route.pattern, auth.AuthMiddleware(), func(c *gin.Context) {
			path := route.path(c)
			for _, part := range path {
				if part == "." || part == ".." || len(part) > 512 || strings.Contains(part, "/") {
					c.JSON(http.StatusBadRequest, gin.H{"code": "invalid_operation", "error": "标识符格式错误"})
					return
				}
			}
			if len(c.Request.URL.RawQuery) > 2048 {
				c.JSON(http.StatusBadRequest, gin.H{"code": "invalid_filter", "error": "查询参数过长"})
				return
			}
			var input []byte
			if route.method != http.MethodGet {
				var err error
				input, err = io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 8<<10))
				if err != nil {
					c.JSON(http.StatusBadRequest, gin.H{"code": "invalid_operation", "error": "操作请求格式错误"})
					return
				}
			}
			body, status := service.proxy(c.Request.Context(), c.GetString("admin_id"), route.method, path, c.Request.URL.RawQuery, input)
			c.Header("Cache-Control", "no-store")
			c.Data(status, "application/json", body)
		})
	}
	return nil
}

type conversionOrderService struct {
	admins adminLookup
	client *conversionOrderClient
}

var reconciliationUnavailable = []byte(`{"code":"reconciliation_unavailable","error":"转化对账服务暂不可用"}`)

func (s conversionOrderService) proxy(ctx context.Context, adminID, method string, path []string, query string, input []byte) ([]byte, int) {
	admin, err := s.admins.GetAdminByID(ctx, adminID)
	if err != nil {
		return []byte(`{"code":"operator_check_unavailable","error":"暂时无法核验账号"}`), http.StatusServiceUnavailable
	}
	if admin == nil || admin.Status != 1 || !conversionReadAllowed(admin.Role) || (method != http.MethodGet && admin.Role != "Admin" && admin.Role != "SuperAdmin") {
		return []byte(`{"code":"operator_forbidden","error":"当前账号无权查看转化对账"}`), http.StatusForbidden
	}
	if s.client == nil {
		return reconciliationUnavailable, http.StatusServiceUnavailable
	}
	return s.client.request(ctx, admin, method, path, query, input)
}

func conversionReadAllowed(role string) bool {
	switch role {
	case "SuperAdmin", "Admin", "MediaBuyer", "Finance":
		return true
	default:
		return false
	}
}

func (c *conversionOrderClient) request(ctx context.Context, admin *auth.Admin, method string, path []string, query string, input []byte) ([]byte, int) {
	parts := []string{"internal", "conversion"}
	for _, part := range path {
		parts = append(parts, url.PathEscape(part))
	}
	endpoint := c.endpoint.JoinPath(parts...)
	endpoint.RawQuery = query
	req, err := http.NewRequestWithContext(ctx, method, endpoint.String(), bytes.NewReader(input))
	if err != nil {
		return reconciliationUnavailable, http.StatusServiceUnavailable
	}
	if method != http.MethodGet {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("X-Operator-ID", admin.ID)
	req.Header.Set("X-Operator-Role", admin.Role)
	res, err := c.http.Do(req)
	if err != nil {
		return reconciliationUnavailable, http.StatusServiceUnavailable
	}
	defer res.Body.Close()
	switch res.StatusCode {
	case http.StatusBadRequest:
		return []byte(`{"code":"invalid_order_key","error":"查询或操作参数格式错误"}`), res.StatusCode
	case http.StatusNotFound:
		return []byte(`{"code":"order_not_found","error":"未找到指定记录"}`), res.StatusCode
	case http.StatusConflict:
		return []byte(`{"code":"state_conflict","error":"记录状态、投递窗口或授权已变化，请刷新后核对"}`), res.StatusCode
	case http.StatusForbidden:
		return []byte(`{"code":"operator_forbidden","error":"当前账号无权执行此操作"}`), res.StatusCode
	case http.StatusOK, http.StatusCreated, http.StatusAccepted:
		const maxResponse = 8 << 20
		body, err := io.ReadAll(io.LimitReader(res.Body, maxResponse+1))
		if err == nil && len(body) <= maxResponse && json.Valid(body) {
			return body, res.StatusCode
		}
	}
	return reconciliationUnavailable, http.StatusServiceUnavailable
}
