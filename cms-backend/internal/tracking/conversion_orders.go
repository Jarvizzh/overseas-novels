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
	"star-novel-cms/internal/db"
)

type conversionOrderClient struct {
	endpoint *url.URL
	token    string
	http     *http.Client
}

type ConversionPromotionOption struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	NovelTitle string `json:"novel_title"`
	FBPixelID  *int   `json:"fb_pixel_id"`
}

func RegisterConversionOrders(rg *gin.RouterGroup, _ any, rawURL, token string) error {
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
	service := conversionOrderService{client: client}

	rg.GET(
		"/conversion/promotion-options",
		auth.AuthMiddleware(),
		auth.RequirePermission(auth.PermAdsConversionsRead),
		listConversionPromotionOptions,
	)

	pixelsOrConvRead := auth.RequireAnyPermission(auth.PermAdsPixelsRead, auth.PermAdsConversionsRead)
	pixelsWrite := auth.RequirePermission(auth.PermAdsPixelsWrite)
	convRead := auth.RequirePermission(auth.PermAdsConversionsRead)
	convWrite := auth.RequirePermission(auth.PermAdsConversionsWrite)

	for _, route := range []struct {
		method, pattern string
		guard           gin.HandlerFunc
		access          string
		resolvePerm     func(*auth.Principal) string
		path            func(*gin.Context) []string
	}{
		{http.MethodGet, "/conversion/orders/:order_id", convRead, "read", constPerm(auth.PermAdsConversionsRead), func(c *gin.Context) []string { return []string{"orders", c.Param("order_id")} }},
		{http.MethodGet, "/conversion/summary", convRead, "read", constPerm(auth.PermAdsConversionsRead), func(*gin.Context) []string { return []string{"summary"} }},
		{http.MethodGet, "/conversion/throughput", convRead, "read", constPerm(auth.PermAdsConversionsRead), func(*gin.Context) []string { return []string{"throughput"} }},
		{http.MethodGet, "/conversion/events", convRead, "read", constPerm(auth.PermAdsConversionsRead), func(*gin.Context) []string { return []string{"events"} }},
		{http.MethodGet, "/conversion/events/:id", convRead, "read", constPerm(auth.PermAdsConversionsRead), func(c *gin.Context) []string { return []string{"events", c.Param("id")} }},
		{http.MethodGet, "/conversion/events/:id/destinations", convRead, "read", constPerm(auth.PermAdsConversionsRead), func(c *gin.Context) []string { return []string{"events", c.Param("id"), "destinations"} }},
		{http.MethodGet, "/conversion/events/:id/deliveries", convRead, "read", constPerm(auth.PermAdsConversionsRead), func(c *gin.Context) []string { return []string{"events", c.Param("id"), "deliveries"} }},
		{http.MethodPost, "/conversion/events/:id/deliveries", convWrite, "write", constPerm(auth.PermAdsConversionsWrite), func(c *gin.Context) []string { return []string{"events", c.Param("id"), "deliveries"} }},
		{http.MethodPost, "/conversion/deliveries/:id/retry", convWrite, "write", constPerm(auth.PermAdsConversionsWrite), func(c *gin.Context) []string { return []string{"deliveries", c.Param("id"), "retry"} }},
		{http.MethodPost, "/conversion/partner-reports/:id/retry", convWrite, "write", constPerm(auth.PermAdsConversionsWrite), func(c *gin.Context) []string { return []string{"partner-reports", c.Param("id"), "retry"} }},
		{http.MethodGet, "/conversion/integrations", pixelsOrConvRead, "read", pixelsOrConversionsReadPerm, func(*gin.Context) []string { return []string{"integrations"} }},
		{http.MethodPost, "/conversion/integrations", pixelsWrite, "write", constPerm(auth.PermAdsPixelsWrite), func(*gin.Context) []string { return []string{"integrations"} }},
		{http.MethodPatch, "/conversion/integrations/:id", pixelsWrite, "write", constPerm(auth.PermAdsPixelsWrite), func(c *gin.Context) []string { return []string{"integrations", c.Param("id")} }},
		{http.MethodGet, "/conversion/integrations/:id/payment-policy", convRead, "read", constPerm(auth.PermAdsConversionsRead), func(c *gin.Context) []string { return []string{"integrations", c.Param("id"), "payment-policy"} }},
		{http.MethodPost, "/conversion/integrations/:id/payment-policy/preview", convWrite, "write", constPerm(auth.PermAdsConversionsWrite), func(c *gin.Context) []string {
			return []string{"integrations", c.Param("id"), "payment-policy", "preview"}
		}},
		{http.MethodPost, "/conversion/integrations/:id/payment-policy/publish", convWrite, "write", constPerm(auth.PermAdsConversionsWrite), func(c *gin.Context) []string {
			return []string{"integrations", c.Param("id"), "payment-policy", "publish"}
		}},
		{http.MethodGet, "/conversion/destinations", pixelsOrConvRead, "read", pixelsOrConversionsReadPerm, func(*gin.Context) []string { return []string{"destinations"} }},
		{http.MethodPost, "/conversion/destinations", pixelsWrite, "write", constPerm(auth.PermAdsPixelsWrite), func(*gin.Context) []string { return []string{"destinations"} }},
		{http.MethodPatch, "/conversion/destinations/:id", pixelsWrite, "write", constPerm(auth.PermAdsPixelsWrite), func(c *gin.Context) []string { return []string{"destinations", c.Param("id")} }},
		{http.MethodGet, "/conversion/audit", convRead, "read", constPerm(auth.PermAdsConversionsRead), func(*gin.Context) []string { return []string{"audit"} }},
	} {
		route := route
		rg.Handle(route.method, route.pattern, auth.AuthMiddleware(), route.guard, func(c *gin.Context) {
			principal, ok := auth.PrincipalFromContext(c)
			if !ok || !principal.IsActive() {
				c.JSON(http.StatusUnauthorized, gin.H{"code": "account_inactive", "error": "账号未启用或不存在"})
				return
			}
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
				limit, code, message := int64(8<<10), "invalid_operation", "操作请求格式错误"
				if len(path) == 4 && path[0] == "integrations" && path[2] == "payment-policy" {
					limit, code, message = 64<<10, "invalid_payment_policy_request", "回传规则请求格式无效或超出大小限制"
				}
				var err error
				input, err = io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, limit))
				if err != nil {
					c.JSON(http.StatusBadRequest, gin.H{"code": code, "error": message})
					return
				}
			}
			perm := route.resolvePerm(principal)
			body, status := service.proxy(c.Request.Context(), principal, route.access, perm, route.method, path, c.Request.URL.RawQuery, input)
			c.Header("Cache-Control", "no-store")
			c.Data(status, "application/json", body)
		})
	}
	return nil
}

func constPerm(code string) func(*auth.Principal) string {
	return func(*auth.Principal) string { return code }
}

func pixelsOrConversionsReadPerm(p *auth.Principal) string {
	if p != nil && p.HasPermission(auth.PermAdsPixelsRead) {
		return auth.PermAdsPixelsRead
	}
	return auth.PermAdsConversionsRead
}

func listConversionPromotionOptions(c *gin.Context) {
	if db.DB == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "database unavailable"})
		return
	}
	rows, err := db.DB.Query(c.Request.Context(), `
		SELECT id, COALESCE(name, ''), COALESCE(novel_title, ''), fb_pixel_id
		FROM promotion_links
		ORDER BY created_at DESC, id DESC`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch promotion options"})
		return
	}
	defer rows.Close()

	items := make([]ConversionPromotionOption, 0)
	for rows.Next() {
		var item ConversionPromotionOption
		if err := rows.Scan(&item.ID, &item.Name, &item.NovelTitle, &item.FBPixelID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch promotion options"})
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch promotion options"})
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, items)
}

type conversionOrderService struct {
	client *conversionOrderClient
}

var reconciliationUnavailable = []byte(`{"code":"reconciliation_unavailable","error":"转化对账服务暂不可用"}`)

func (s conversionOrderService) proxy(ctx context.Context, principal *auth.Principal, access, permission, method string, path []string, query string, input []byte) ([]byte, int) {
	if principal == nil || !principal.IsActive() {
		return []byte(`{"code":"operator_forbidden","error":"当前账号无权查看转化对账"}`), http.StatusForbidden
	}
	if s.client == nil {
		return reconciliationUnavailable, http.StatusServiceUnavailable
	}
	return s.client.request(ctx, principal, access, permission, method, path, query, input)
}

func (c *conversionOrderClient) request(ctx context.Context, principal *auth.Principal, access, permission, method string, path []string, query string, input []byte) ([]byte, int) {
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
	req.Header.Set("X-Operator-ID", principal.ID)
	req.Header.Set("X-Operator-Role", principal.Role)
	req.Header.Set("X-Operator-Access", access)
	req.Header.Set("X-Operator-Permission", permission)
	res, err := c.http.Do(req)
	if err != nil {
		return reconciliationUnavailable, http.StatusServiceUnavailable
	}
	defer res.Body.Close()
	switch res.StatusCode {
	case http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound, http.StatusConflict:
		return mapConversionUpstreamError(res.StatusCode, res.Body), res.StatusCode
	case http.StatusOK, http.StatusCreated, http.StatusAccepted:
		const maxResponse = 8 << 20
		body, err := io.ReadAll(io.LimitReader(res.Body, maxResponse+1))
		if err == nil && len(body) <= maxResponse && json.Valid(body) {
			return body, res.StatusCode
		}
	}
	return reconciliationUnavailable, http.StatusServiceUnavailable
}

type conversionErrorEntry struct {
	Code   string   `json:"code"`
	Error  string   `json:"error"`
	Field  string   `json:"field,omitempty"`
	AppIDs []string `json:"app_ids,omitempty"`
}

var conversionUpstreamErrorCatalog = map[int]map[string]string{
	http.StatusBadRequest: {
		"invalid_time":                             "时间范围格式错误，或开始时间晚于结束时间",
		"invalid_cursor":                           "分页游标无效，请返回首页重新查询",
		"invalid_limit":                            "每页条数超出允许范围",
		"invalid_id":                               "编号参数格式错误，须为正整数",
		"invalid_event_id":                         "编号参数格式错误，须为正整数",
		"invalid_delivery_id":                      "编号参数格式错误，须为正整数",
		"invalid_attempt_cursor":                   "编号参数格式错误，须为正整数",
		"invalid_filter":                           "筛选参数格式错误",
		"invalid_order_key":                        "订单号或接入 ID 格式错误",
		"invalid_summary_filter":                   "统计筛选参数无效（小时数须在 1 至 168 之间）",
		"invalid_payment_policy_request":           "回传规则请求格式无效，请核对配置后重试",
		"invalid_payment_policy":                   "回传规则配置无效",
		"invalid_payment_policy_operation":         "请核对区间操作和回传参数",
		"invalid_payment_policy_bands":             "金额区间重叠或数量超限",
		"invalid_payment_policy_app":               "请选择当前接入授权的应用",
		"invalid_payment_policy_promotion":         "推广链接编号无效或链接不存在",
		"payment_policy_source_unsupported":        "此接入来源暂不支持付款回传规则",
		"payment_policy_first_requires_promotion":  "仅推广链接专属规则可配置首单",
		"payment_policy_first_coverage_unverified": "该链接的历史付款覆盖尚未核实",
		"payment_policy_first_source_unsupported":  "该来源暂不支持首单规则",
		"sinan_apps_required":                      "启用司南前至少填写一个允许应用 ID",
		"sinan_events_required":                    "启用司南前至少选择一个允许事件",
		"invalid_operation":                        "操作参数无效，请核对必填字段与原因长度（1-500 字）",
	},
	http.StatusForbidden: {
		"operator_forbidden": "当前账号无权执行此操作",
	},
	http.StatusNotFound: {
		"order_not_found":    "未找到指定订单记录",
		"resource_not_found": "未找到指定的事件、投递或目标记录",
	},
	http.StatusConflict: {
		"payment_policy_version_conflict": "规则版本已变化，请核对最新配置后重试",
		"order_app_ambiguous":             "多个应用存在该订单号，请选择应用后查询",
		"state_conflict":                  "记录状态已变化，或存在重复 Pixel/接入账户与推广链绑定冲突，请刷新后核对",
	},
}

var conversionUpstreamFallbackErrors = map[int]conversionErrorEntry{
	http.StatusBadRequest: {Code: "invalid_order_key", Error: "查询或操作参数格式错误"},
	http.StatusForbidden:  {Code: "operator_forbidden", Error: "当前账号无权执行此操作"},
	http.StatusNotFound:   {Code: "order_not_found", Error: "未找到指定记录"},
	http.StatusConflict:   {Code: "state_conflict", Error: "记录状态已变化，或存在重复 Pixel/接入账户与推广链绑定冲突，请刷新后核对"},
}

func mapConversionUpstreamError(status int, body io.Reader) []byte {
	const maxErrorBody = 4 << 10
	raw, _ := io.ReadAll(io.LimitReader(body, maxErrorBody))
	var parsed struct {
		Code   string   `json:"code"`
		Field  string   `json:"field"`
		AppIDs []string `json:"app_ids"`
	}
	if len(raw) > 0 && json.Unmarshal(raw, &parsed) == nil {
		if msg, ok := conversionUpstreamErrorCatalog[status][parsed.Code]; ok {
			encoded, err := json.Marshal(conversionErrorEntry{Code: parsed.Code, Error: msg, Field: parsed.Field, AppIDs: parsed.AppIDs})
			if err == nil {
				return encoded
			}
		}
	}
	fallback, ok := conversionUpstreamFallbackErrors[status]
	if !ok {
		return reconciliationUnavailable
	}
	encoded, err := json.Marshal(fallback)
	if err != nil {
		return reconciliationUnavailable
	}
	return encoded
}
