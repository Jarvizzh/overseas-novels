package auth

import (
	"errors"
	"fmt"
	"strings"
)

// Authoritative permission code constants aligned with CMS navigation (§4.1).
const (
	PermDashboardRead = "dashboard:read"

	PermContentNovelsRead      = "content.novels:read"
	PermContentNovelsWrite     = "content.novels:write"
	PermContentOperationsRead  = "content.operations:read"
	PermContentOperationsWrite = "content.operations:write"

	PermRevenueUsersRead  = "revenue.users:read"
	PermRevenueUsersWrite = "revenue.users:write"

	PermPaymentsOrdersRead          = "payments.orders:read"
	PermPaymentsOrdersWrite         = "payments.orders:write"
	PermPaymentsSubscriptionsRead   = "payments.subscriptions:read"
	PermPaymentsSubscriptionsWrite  = "payments.subscriptions:write"
	PermPaymentsRefundsRead         = "payments.refunds:read"
	PermPaymentsRefundsWrite        = "payments.refunds:write"
	PermPaymentsRiskRead            = "payments.risk:read"
	PermPaymentsRiskWrite           = "payments.risk:write"
	PermPaymentsDisputesRead        = "payments.disputes:read"
	PermPaymentsDisputesWrite       = "payments.disputes:write"
	PermPaymentsReconciliationRead  = "payments.reconciliation:read"
	PermPaymentsReconciliationWrite = "payments.reconciliation:write"
	PermPaymentsChannelsRead        = "payments.channels:read"
	PermPaymentsChannelsWrite       = "payments.channels:write"
	PermPaymentsProductsRead        = "payments.products:read"
	PermPaymentsProductsWrite       = "payments.products:write"

	PermAdsPromotionsRead   = "ads.promotions:read"
	PermAdsPromotionsWrite  = "ads.promotions:write"
	PermAdsTemplatesRead    = "ads.templates:read"
	PermAdsTemplatesWrite   = "ads.templates:write"
	PermAdsPixelsRead       = "ads.pixels:read"
	PermAdsPixelsWrite      = "ads.pixels:write"
	PermAdsConversionsRead  = "ads.conversions:read"
	PermAdsConversionsWrite = "ads.conversions:write"

	PermSupportFeedbackRead  = "support.feedback:read"
	PermSupportFeedbackWrite = "support.feedback:write"

	PermSystemDomainsRead   = "system.domains:read"
	PermSystemDomainsWrite  = "system.domains:write"
	PermSystemAccountsRead  = "system.accounts:read"
	PermSystemAccountsWrite = "system.accounts:write"
	PermSystemRolesRead     = "system.roles:read"
	PermSystemRolesWrite    = "system.roles:write"
)

// AllPaymentReadPermissions lists every payments.*:read permission for shared
// read-only payment dropdown endpoints (e.g. GET /payments/accounts).
var AllPaymentReadPermissions = []string{
	PermPaymentsOrdersRead,
	PermPaymentsSubscriptionsRead,
	PermPaymentsRefundsRead,
	PermPaymentsRiskRead,
	PermPaymentsDisputesRead,
	PermPaymentsReconciliationRead,
	PermPaymentsChannelsRead,
	PermPaymentsProductsRead,
}

var (
	ErrAccountInactive           = errors.New("账号不存在或已禁用")
	ErrPermissionDenied          = errors.New("没有执行此操作的权限")
	ErrSuperAdminRequired        = fmt.Errorf("%w: 仅超级管理员可执行此操作", ErrPermissionDenied)
	ErrInvalidPermission         = errors.New("包含无效的权限编码")
	ErrSuperAdminOnlyPermission  = errors.New("该权限仅超级管理员可拥有，不能授予普通角色")
	ErrRoleNotFound              = errors.New("角色不存在")
	ErrRoleCodeExists            = errors.New("角色编码已存在")
	ErrRoleInactive              = errors.New("所选角色已禁用")
	ErrSystemRoleImmutable       = errors.New("超级管理员角色不可修改")
	ErrSystemRoleDeleteForbidden = errors.New("系统内置角色不可删除")
	ErrRoleInUse                 = errors.New("该角色下仍有绑定账号，无法删除")
	ErrSelfModifyForbidden       = errors.New("不可删除、禁用或降权当前登录账号")
	ErrLastSuperAdmin            = errors.New("系统必须至少保留一个启用状态的超级管理员")
)

type PermissionAction struct {
	Code           string `json:"code"`
	Action         string `json:"action"`
	Label          string `json:"label"`
	SuperAdminOnly bool   `json:"super_admin_only"`
}

type PermissionModule struct {
	Resource    string             `json:"resource"`
	Label       string             `json:"label"`
	Description string             `json:"description,omitempty"`
	Read        *PermissionAction  `json:"read,omitempty"`
	Write       *PermissionAction  `json:"write,omitempty"`
	Actions     []PermissionAction `json:"actions"`
}

type PermissionGroup struct {
	Group   string             `json:"group"`
	Label   string             `json:"label"`
	Modules []PermissionModule `json:"modules"`
}

type PermissionCatalogResponse struct {
	Groups []PermissionGroup `json:"groups"`
}

type permissionMeta struct {
	Code           string
	Group          string
	Resource       string
	Action         string
	SuperAdminOnly bool
	ImpliedRead    string
	Order          int
}

func makeReadAction(code string) *PermissionAction {
	return &PermissionAction{
		Code:   code,
		Action: "read",
		Label:  "查看",
	}
}

func makeWriteAction(code string, superAdminOnly bool) *PermissionAction {
	return &PermissionAction{
		Code:           code,
		Action:         "write",
		Label:          "编辑",
		SuperAdminOnly: superAdminOnly,
	}
}

func makeModule(resource, label, description, readCode, writeCode string, writeSuperAdminOnly bool) PermissionModule {
	mod := PermissionModule{
		Resource:    resource,
		Label:       label,
		Description: description,
		Actions:     make([]PermissionAction, 0, 2),
	}
	if readCode != "" {
		r := makeReadAction(readCode)
		mod.Read = r
		mod.Actions = append(mod.Actions, *r)
	}
	if writeCode != "" {
		w := makeWriteAction(writeCode, writeSuperAdminOnly)
		mod.Write = w
		mod.Actions = append(mod.Actions, *w)
	}
	return mod
}

var (
	permissionCatalogGroups []PermissionGroup
	permissionRegistry      map[string]permissionMeta
	allPermissionCodes      []string
)

func init() {
	permissionCatalogGroups = []PermissionGroup{
		{
			Group: "dashboard",
			Label: "首页总览",
			Modules: []PermissionModule{
				makeModule("dashboard", "首页总览", "仅允许进入首页壳，各卡片按业务读权限按需加载", PermDashboardRead, "", false),
			},
		},
		{
			Group: "content",
			Label: "内容运营",
			Modules: []PermissionModule{
				makeModule("content.novels", "书籍管理", "小说、章节、封面、批量导入、全局计费设置", PermContentNovelsRead, PermContentNovelsWrite, false),
				makeModule("content.operations", "书城配置", "推荐位、榜单、签到活动配置与预览", PermContentOperationsRead, PermContentOperationsWrite, false),
			},
		},
		{
			Group: "revenue",
			Label: "用户营收",
			Modules: []PermissionModule{
				makeModule("revenue.users", "用户管理", "读者列表/详情/钱包；封禁与调账", PermRevenueUsersRead, PermRevenueUsersWrite, false),
			},
		},
		{
			Group: "payments",
			Label: "支付中心",
			Modules: []PermissionModule{
				makeModule("payments.orders", "交易与订单", "订单、支付详情/凭证；Stripe 市场审核批准", PermPaymentsOrdersRead, PermPaymentsOrdersWrite, false),
				makeModule("payments.subscriptions", "订阅与账期", "订阅列表与账期明细", PermPaymentsSubscriptionsRead, PermPaymentsSubscriptionsWrite, false),
				makeModule("payments.refunds", "退款", "退款单与队列查看；退款发起与 PayPal 对账", PermPaymentsRefundsRead, PermPaymentsRefundsWrite, false),
				makeModule("payments.risk", "风控", "风控工单/决策查看与工单处置", PermPaymentsRiskRead, PermPaymentsRiskWrite, false),
				makeModule("payments.disputes", "争议与冲回", "争议/冲回工单查看与处置", PermPaymentsDisputesRead, PermPaymentsDisputesWrite, false),
				makeModule("payments.reconciliation", "资金对账", "账本流水/打款/异常；对账指令与自动对账开关", PermPaymentsReconciliationRead, PermPaymentsReconciliationWrite, false),
				makeModule("payments.channels", "渠道与商户", "渠道/售后策略；渠道绑定及商品 live 生命周期", PermPaymentsChannelsRead, PermPaymentsChannelsWrite, false),
				makeModule("payments.products", "商品与定价", "商品目录；新建/复制/沙箱发布暂停退役", PermPaymentsProductsRead, PermPaymentsProductsWrite, false),
			},
		},
		{
			Group: "ads",
			Label: "广告投放",
			Modules: []PermissionModule{
				makeModule("ads.promotions", "推广链接", "推广链接列表、创建、编辑、删除", PermAdsPromotionsRead, PermAdsPromotionsWrite, false),
				makeModule("ads.templates", "充值模板", "充值模板查看、创建、编辑、删除、设默认", PermAdsTemplatesRead, PermAdsTemplatesWrite, false),
				makeModule("ads.pixels", "像素管理", "像素、接入源、投递目标管理", PermAdsPixelsRead, PermAdsPixelsWrite, false),
				makeModule("ads.conversions", "回传中心", "回传汇总/事件/订单/审计；重试、补发、付款规则发布", PermAdsConversionsRead, PermAdsConversionsWrite, false),
			},
		},
		{
			Group: "support",
			Label: "客诉管理",
			Modules: []PermissionModule{
				makeModule("support.feedback", "用户反馈", "反馈查看与回复处理", PermSupportFeedbackRead, PermSupportFeedbackWrite, false),
			},
		},
		{
			Group: "system",
			Label: "系统管理",
			Modules: []PermissionModule{
				makeModule("system.domains", "域名管理", "落地页域名管理", PermSystemDomainsRead, PermSystemDomainsWrite, false),
				makeModule("system.accounts", "账号管理", "后台管理员管理（写权限仅超级管理员拥有）", PermSystemAccountsRead, PermSystemAccountsWrite, true),
				makeModule("system.roles", "角色与权限", "角色与权限矩阵管理（写权限仅超级管理员拥有）", PermSystemRolesRead, PermSystemRolesWrite, true),
			},
		},
	}

	permissionRegistry = make(map[string]permissionMeta)
	order := 0
	for _, g := range permissionCatalogGroups {
		for _, m := range g.Modules {
			readCode := ""
			if m.Read != nil {
				readCode = m.Read.Code
			}
			for _, act := range m.Actions {
				implied := ""
				if act.Action == "write" && readCode != "" {
					implied = readCode
				}
				permissionRegistry[act.Code] = permissionMeta{
					Code:           act.Code,
					Group:          g.Group,
					Resource:       m.Resource,
					Action:         act.Action,
					SuperAdminOnly: act.SuperAdminOnly,
					ImpliedRead:    implied,
					Order:          order,
				}
				allPermissionCodes = append(allPermissionCodes, act.Code)
				order++
			}
		}
	}
}

// PermissionCatalog returns a deep copy of the authoritative permission tree.
func PermissionCatalog() PermissionCatalogResponse {
	groups := make([]PermissionGroup, len(permissionCatalogGroups))
	for i, g := range permissionCatalogGroups {
		mods := make([]PermissionModule, len(g.Modules))
		for j, m := range g.Modules {
			cp := m
			if m.Read != nil {
				r := *m.Read
				cp.Read = &r
			}
			if m.Write != nil {
				w := *m.Write
				cp.Write = &w
			}
			cp.Actions = append([]PermissionAction(nil), m.Actions...)
			mods[j] = cp
		}
		groups[i] = PermissionGroup{
			Group:   g.Group,
			Label:   g.Label,
			Modules: mods,
		}
	}
	return PermissionCatalogResponse{Groups: groups}
}

// AllPermissionCodes returns all registered permission codes in canonical order.
func AllPermissionCodes() []string {
	out := make([]string, len(allPermissionCodes))
	copy(out, allPermissionCodes)
	return out
}

// IsValidPermission reports whether code exists in the authoritative registry.
func IsValidPermission(code string) bool {
	_, ok := permissionRegistry[strings.TrimSpace(code)]
	return ok
}

// IsSuperAdminOnlyPermission reports whether code is restricted exclusively to super admins.
func IsSuperAdminOnlyPermission(code string) bool {
	meta, ok := permissionRegistry[strings.TrimSpace(code)]
	return ok && meta.SuperAdminOnly
}

// NormalizePermissions validates all codes against the authoritative registry,
// automatically adds the corresponding :read code whenever a :write code is present,
// removes duplicates, and returns codes in canonical registry order.
func NormalizePermissions(codes []string) ([]string, error) {
	seen := make(map[string]struct{}, len(codes))
	for _, raw := range codes {
		code := strings.TrimSpace(raw)
		meta, ok := permissionRegistry[code]
		if !ok {
			return nil, fmt.Errorf("%w: %q", ErrInvalidPermission, raw)
		}
		seen[meta.Code] = struct{}{}
		if meta.ImpliedRead != "" {
			seen[meta.ImpliedRead] = struct{}{}
		}
	}

	result := make([]string, 0, len(seen))
	for _, code := range allPermissionCodes {
		if _, ok := seen[code]; ok {
			result = append(result, code)
		}
	}
	return result, nil
}

// ValidateAssignablePermissions normalizes permissions and rejects any code marked super_admin_only.
func ValidateAssignablePermissions(codes []string) ([]string, error) {
	normalized, err := NormalizePermissions(codes)
	if err != nil {
		return nil, err
	}
	for _, code := range normalized {
		if IsSuperAdminOnlyPermission(code) {
			return nil, fmt.Errorf("%w: %s", ErrSuperAdminOnlyPermission, code)
		}
	}
	return normalized, nil
}

// normalizeKnownPermissions filters and orders permissions already stored in the database
// without failing if an unknown code were ever present.
func normalizeKnownPermissions(codes []string) []string {
	seen := make(map[string]struct{}, len(codes))
	for _, raw := range codes {
		code := strings.TrimSpace(raw)
		meta, ok := permissionRegistry[code]
		if !ok {
			continue
		}
		seen[meta.Code] = struct{}{}
		if meta.ImpliedRead != "" {
			seen[meta.ImpliedRead] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for _, code := range allPermissionCodes {
		if _, ok := seen[code]; ok {
			result = append(result, code)
		}
	}
	return result
}
