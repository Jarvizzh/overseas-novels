# StarNovel CMS RBAC 权限管理重构 Code Review 报告

> **评审日期**：2026-11-25（首轮评审 & 第 2 轮复审）  
> **对照方案**：[cms-rbac-permission-refactor-plan.md](./cms-rbac-permission-refactor-plan.md)（v3.1）  
> **评审范围**：`cms-backend`、`reader-backend`、`conversion-backend`、`cms-front`、PostgreSQL 迁移（`2026111935.sql`）与授权脚本、`DEPLOYMENT_SOP.md`（共 121 个修改文件 + 新增文件）  
> **评审结论**：**Approved（第 2 轮复审通过：2 项 P1 与 2 项 P2 均已闭环修复并经真实 PostgreSQL 及非 owner 角色授权验证，可合入发布）**

---

## 1. 评审总览

本次重构整体实现质量高，与 v3.1 实施方案高度一致：三端 Schema 版本统一升至 `2026111935`，空库首个超管强口令引导、单角色 RBAC 模型、无缓存实时查库鉴权、账号/角色写权限超管独占与最后一个启用超管保护、最小权限 Options 接口、`conversion-backend` 事务/CLI 分权、`reader-backend` 后台任务与风控/争议分配权限函数、以及前端双 Tab 账号角色管理、像素/回传规则抽屉分权、推广页独立新建入口均已落地并通过自动化测试及隔离库真实 PostgreSQL 集成测试。

通过跨层端到端排查，发现 **2 项阻断级（P1）问题** 和 **2 项次级（P2）隐患**，需在合入发布前修复。

---

## 2. 问题明细（按优先级）

### 问题汇总表

| 编号 | 优先级 | 模块 | 问题摘要 | 涉及文件 |
| --- | :---: | --- | --- | --- |
| CR-1 | **P1** | `cms-backend` / `cms-front` | 仅具备退款权限的账号在退款页无法查看原收款或发起任何退款（403） | `cms-backend/internal/billing/handler.go:59`<br>`cms-front/src/pages/PaymentRefunds.tsx:532-536` |
| CR-2 | **P1** | PostgreSQL 授权脚本 | `conversion_role_grants.sql` 漏授 `cms_role` 对 `roles` / `role_permissions` 的写权限与序列权限 | `cms-backend/db/conversion_role_grants.sql:51` |
| CR-3 | **P2** | PostgreSQL 迁移 | `2026111935.sql` 残留旧角色回填块且将旧 `'Admin'` 映射为超管 `'Admin'` | `cms-backend/db/migrations/2026111935.sql:125-143` |
| CR-4 | **P2** | `cms-backend` 支付售后 | 拒绝风控工单流转 `dispute_due` 时的负责人权限校验口径偏窄 | `cms-backend/internal/billing/aftercare_service.go:46-52,136-142` |

---

### [CR-1 · P1] 仅具备 `payments.refunds:read/write` 的账号在退款页无法查看原收款或提交退款（403）

- **代码位置**：
  - `cms-backend/internal/billing/handler.go:59`
    ```go
    payments.GET("/orders/:id/receipts", ordersRead, h.ListPaymentReceipts)
    ```
  - `cms-front/src/pages/PaymentRefunds.tsx:532-536`
    ```ts
    const source = usePaymentQuery<{ receipts: Receipt[] }>(
      legacy ? '' : `/payments/orders/${encodeURIComponent(order)}/receipts?${paymentParams({ receipt_id: pin })}`,
      revision,
      true,
    );
    ```
- **问题根因与复现路径**：
  1. 创建一个自定义「退款专员」角色，仅勾选 `payments.refunds:read` 与 `payments.refunds:write`（不勾选 `payments.orders:read`）。
  2. 登录 CMS 进入「支付中心 → 退款」（`/payments/refunds`），退款队列与退款历史正常加载。
  3. 点击队列中任意待退款订单，或在搜索框输入订单号发起退款，打开 `RefundDrawer` 后前端立即请求 `GET /payments/orders/:id/receipts`。
  4. 后端仅允许 `payments.orders:read`，返回 `403 Forbidden`，页面展示加载错误并阻断退款表单渲染，导致具备完整退款读写权限的账号完全无法发起退款。
- **修复建议**：
  1. 在 `cms-backend/internal/billing/handler.go:59` 将路由权限调整为：
     ```go
     payments.GET(
         "/orders/:id/receipts",
         auth.RequireAnyPermission(auth.PermPaymentsOrdersRead, auth.PermPaymentsRefundsRead),
         h.ListPaymentReceipts,
     )
     ```
     （该接口返回收款金额、已退/占用/可退余额及脱敏凭据 ID，是退款页查看与计算可退金额的必需前置只读数据。）
  2. 在 `cms-backend/internal/billing/rbac_routes_test.go` 补充单测，验证仅拥有 `payments.refunds:read`（无 `payments.orders:read`）的账号可正常访问 `GET /payments/orders/:id/receipts`，而仍不可访问 `GET /payments/orders` 和 `GET /payments/orders/:id/payment-details`。

---

### [CR-2 · P1] `conversion_role_grants.sql` 漏授 `cms_role` 对 `roles` / `role_permissions` 的写权限与序列权限

- **代码位置**：
  - `cms-backend/db/conversion_role_grants.sql:51`
    ```sql
    GRANT SELECT ON public.roles, public.role_permissions TO :"reader_role", :"cms_role";
    ```
- **问题根因与影响**：
  - 按 `conversion_role_grants.sql` 文件头约定，迁移由 `migration_owner` 执行，运行时 `cms_role` 为独立非 owner 登录账号。
  - `2026111935.sql` 新建的 `public.roles`、`public.role_permissions` 表及自增序列 `public.roles_id_seq` 归属于 `migration_owner`。当前脚本只给了 `cms_role` 对两张表的 `SELECT` 权限，未授予 `INSERT, UPDATE, DELETE` 及序列 `USAGE, SELECT`。
  - 在最小权限生产 RDS 部署环境下，超级管理员在 CMS 后台执行新建角色、编辑角色权限或删除角色（`POST /auth/roles`、`PUT /auth/roles/:id`、`DELETE /auth/roles/:id`）时会触发 PostgreSQL `permission denied for table roles` 或 `permission denied for sequence roles_id_seq`。
- **修复建议**：
  将 `cms-backend/db/conversion_role_grants.sql:51` 拆分并补全为：
  ```sql
  GRANT SELECT ON public.roles, public.role_permissions TO :"reader_role";
  GRANT SELECT, INSERT, UPDATE, DELETE ON public.roles, public.role_permissions TO :"cms_role";
  GRANT USAGE, SELECT ON SEQUENCE public.roles_id_seq TO :"cms_role";
  GRANT EXECUTE ON FUNCTION public.admin_has_permission(text, text) TO :"reader_role", :"cms_role";
  ```
  （显式授予 `public.admin_has_permission` 的 `EXECUTE` 权限，防止生产库配置 `REVOKE ALL ON FUNCTIONS FROM PUBLIC` 时影响 `reader_role`/`cms_role`。）

---

### [CR-3 · P2] `2026111935.sql` 残留旧角色映射块且将旧 `'Admin'` 映射为超管 `'Admin'`

- **代码位置**：
  - `cms-backend/db/migrations/2026111935.sql:125-143`
    ```sql
    UPDATE admins a
    SET role_id = r.id,
        role = r.code
    FROM roles r
    WHERE a.role_id IS NULL
      AND r.code = CASE a.role
          WHEN 'SuperAdmin' THEN 'Admin'
          WHEN 'Admin' THEN 'Admin'
          ...
    ```
- **问题根因与影响**：
  - 虽然 v3.1 已确认本次为空库首次发布（无存量 `admins` 行），该段 SQL 不会命中任何行；但若后续在任何非空旧测试/预演库运行该迁移，旧版普通管理员（`role = 'Admin'`）会被直接映射至新版超级管理员（`is_super_admin = TRUE`），存在静默提权风险。
- **修复建议**：
  - 按空库首次发布前提，精简移除旧角色别名映射，仅保留按 `r.code = a.role` 的精确匹配；若需要兼容旧测试库数据，则务必将旧 `'Admin'` 映射到 `'Leader'`，仅将旧 `'SuperAdmin'` 映射到 `'Admin'`。

---

### [CR-4 · P2] 拒绝风控工单流转 `dispute_due` 时负责人权限校验与策略配置口径存在偏差

- **代码位置**：
  - `cms-backend/internal/billing/aftercare_service.go:46-52`
    ```sql
    const caseCanRejectSQL = `c.kind='risk_review' AND c.work_state='pending'
        AND EXISTS(SELECT 1 FROM payment_aftercare_policies p
            WHERE p.account_id=c.account_id AND p.refund_seconds IS NOT NULL
              AND p.finance_id IS NOT NULL
              AND public.admin_has_permission(p.finance_id, 'payments.refunds:write'))
    ```
  - `cms-backend/internal/billing/aftercare_service.go:136-142`（`reviewCase` 在存在关联 open dispute 时将 `work_state` 置为 `dispute_due` 且 `owner_id = p.finance_id`）
- **问题根因与影响**：
  - `aftercare_policy.go:73` 与 `listCaseOwnersByScope('aftercare_finance')` 均要求 `finance_id` 同时具备 `payments.disputes:write` 和 `payments.refunds:write`。
  - 但运行时 `caseCanRejectSQL` 仅校验了 `payments.refunds:write`。若 `p.finance_id` 后续不再具备 `payments.disputes:write`，风控拒绝流转到 `dispute_due` 时仍会把争议待办分配给无争议写权限的账号。
- **修复建议**：
  - 在 `caseCanRejectSQL` 中保持与策略配置及 `aftercare_finance` 筛选一致，补充校验 `public.admin_has_permission(p.finance_id, 'payments.disputes:write')`（或对 `dispute_due` 条件分支显式要求 `payments.disputes:write`）。

---

## 3. 已逐项核验通过的核心设计项

1. **三端 Schema 版本同步**：`cms-backend/internal/db/postgres.go`、`reader-backend/internal/db/postgres.go`、`conversion-backend/internal/delivery/store.go` 均已升级为精确校验 `2026111935`，`DEPLOYMENT_SOP.md` 同步更新。
2. **空库首个超管安全引导**：`cms-backend/db/seed.sql` 与 `postgres.go` 已彻底移除硬编码 `admin/admin123`；`CMS_BOOTSTRAP_ADMIN_PASSWORD` 强制要求 ≥12 位且含 ≥3 类字符、拦截弱口令与占位符，已有启用超管时重跑 `-migrate` 自动跳过且不重置密码，审计日志不记录明文密码。
3. **授权上限与最后一个启用超管保护**：
   - `POST /auth/register`、`POST/PUT/DELETE /auth/admins*`、`POST/PUT/DELETE /auth/roles*` 全部由 `RequireSuperAdmin()` 保护并在事务内二次执行 `RequireSuperAdminTx`；
   - `ValidateAssignablePermissions` 阻止将 `system.accounts:write`、`system.roles:write` 写入任何非超管角色；
   - 咨询锁 + 行锁正确拦截删除/禁用/降权最后一个启用超管，且禁止管理员自删/自禁/自降权。
4. **无缓存实时鉴权与业务明细隔离**：
   - `AuthMiddleware` 每次请求直接走主键查询 `admins + roles + role_permissions`，无进程内缓存；
   - `dashboard:read` 不再放行 `/users`、`/orders`、`/novels` 明细接口，`DashboardTab` 按业务读权限分别条件请求；
   - 4 个轻量 Options 接口（`/promotion-links/options`、`/operations/novel-options`、`/recharge-templates/product-options`、`/conversion/promotion-options`）均按最小字段返回，其中 `/recharge-templates/product-options` 按 `{ sandbox, live }` 分离返回，`/promotion-links/options` 全量返回已发布书籍无截断，`promotion.ts` 正确兼容无 `status` 字段的 `PromotionLinkDomainOption`。
5. **支付统一授权口径**：
   - `POST /orders/:id/refund` 与 `POST /payments/refunds`、`POST /payments/refunds/:id/reconcile-paypal` 及事务内 `submitRefundCommand` / `linkConfirmedPayPalRefund` 统一要求 `payments.refunds:write`；
   - 商品新建/复制与 `sandbox` 发布/暂停/退役要求 `payments.products:write`；渠道绑定与 `live` 发布/暂停/退役统一要求 `payments.products:write` + `payments.channels:write`（前端按钮、后端路由、事务内复核三处一致）；商品绑定明细响应按 `payments.channels:read` 控制返回。
6. **跨服务权限与后台任务**：
   - `conversion-backend` HTTP 层校验 `X-Operator-Access` + `X-Operator-Permission`，写事务通过 `conversion.lock_active_admin_for_permission` 核验具体权限，`conversion-ops retention` 严格限制仅超管执行（`conversion.lock_active_super_admin`）；
   - `reader-backend` 自动对账领取、Stripe 通知入队、争议工单自动分配均切换为 `public.admin_has_permission`；风控工单自动分配保持 `work_state = 'pending'` 与 `work_due_at`，同时以 `public.admin_has_permission(p.reviewer_id, 'payments.risk:write')` 守卫 `owner_id`（无权/停用时置 `NULL`）。
7. **前端交互闭环**：
   - `AccountsTab` 合并为「账号管理 / 角色与权限」双 Tab，分级权限矩阵支持写含读联动、分组一键全选及超管独占项置灰锁定；
   - `IntegrationConfigDrawer` 与 `PixelsTab` 独立拆分 `canWriteIntegration`、`canReadPolicy`、`canWritePolicy`、`canReadAudit`，接入配置与付款回传规则完全解耦；
   - `PromotionsTab` 补齐顶部「新建推广链接」按钮与弹窗，仅拥有 `ads.promotions:write` 的账号无需访问书籍管理页即可完整新建、编辑、删除推广链接。

---

## 4. 自动化与真实运行验证记录

| 验证项 | 执行命令 / 测试环境 | 结果 |
| --- | --- | --- |
| `cms-backend` 静态检查与单元测试 | `cd cms-backend && go vet ./... && go test -count=1 ./...` | **PASS** |
| `conversion-backend` 静态检查与单元测试 | `cd conversion-backend && go vet ./... && go test -count=1 ./...` | **PASS** |
| `reader-backend` 静态检查与单元测试 | `cd reader-backend && go vet ./... && go test -count=1 ./...` | **PASS** |
| `cms-front` 代码规范与生产构建 | `cd cms-front && npm run lint && npm run build`（`oxlint`: 0 warning / 0 error；`tsc -b && vite build` 通过） | **PASS** |
| **三端真实 PostgreSQL 集成测试** | 在独立临时库 `rbac_cr_verify`（`postgres:15-alpine`，测完已清理）下执行：<br>- `cms-backend`: `TestRBACMigrationAndBootstrapSuperAdmin`、`TestPaymentCenterRefundAuthorizationIntegration`、`TestOperationsConfigAndGroups`、`TestPromotionLinkRejectsSandboxTemplate`<br>- `reader-backend`: `internal/payment/aftercare`、`internal/payment/settlement`、`internal/db`<br>- `conversion-backend`: `internal/delivery`、`internal/operations` | **全部 PASS（0 skip）** |

---

## 5. 合入前检查项

- [x] 修复 **CR-1（P1）**：`GET /payments/orders/:id/receipts` 放行 `payments.refunds:read` 并补充单测
- [x] 修复 **CR-2（P1）**：`conversion_role_grants.sql` 补全 `cms_role` 对 `roles` / `role_permissions` 写权限、`roles_id_seq` 序列权限及 `public.admin_has_permission` 执行权限
- [x] 修复 **CR-3（P2）**：清理 `2026111935.sql` 中的旧角色别名映射块，仅保留 `r.code = a.role` 精确匹配
- [x] 修复 **CR-4（P2）**：对齐 `aftercare_service.go` `caseCanRejectSQL` 与 `aftercare_policy.go` 的负责人权限校验口径并补充回归单测
- [x] 复跑受影响测试并提交复审

---

## 6. 第 2 轮复审记录（2026-11-25）

### 逐项复核结果

| 编号 | 优先级 | 复核状态 | 修复核验详情 |
| --- | :---: | :---: | --- |
| CR-1 | **P1** | **已闭环** | `cms-backend/internal/billing/handler.go:59` 已改为 `auth.RequireAnyPermission(auth.PermPaymentsOrdersRead, auth.PermPaymentsRefundsRead)`；`cms-backend/internal/billing/rbac_routes_test.go:106-118` 已补充正反单测，验证 `refunds-read` 可访问 `/payments/orders/:id/receipts`，且仍对 `/payments/orders` 和 `/payments/orders/:id/payment-details` 返回 403，`dash-only` 返回 403。 |
| CR-2 | **P1** | **已闭环** | `cms-backend/db/conversion_role_grants.sql:51-54` 已拆分为 `reader_role` 只读、`cms_role` 对 `public.roles`/`public.role_permissions` 读写及 `public.roles_id_seq` 序列 `USAGE, SELECT`，并向两角色显式授予 `EXECUTE ON FUNCTION public.admin_has_permission(text, text)`；`cms-backend/internal/db/migration_test.go:666-680` 已增加资产完整性断言。 |
| CR-3 | **P2** | **已闭环** | `cms-backend/db/migrations/2026111935.sql:125-130` 已彻底移除旧角色别名映射块，仅保留 `r.code = a.role` 精确匹配；`cms-backend/internal/db/migration_test.go:654-664` 已增加反向禁用词断言防止回退。 |
| CR-4 | **P2** | **已闭环** | `cms-backend/internal/billing/aftercare_service.go:46-51` 的 `caseCanRejectSQL` 已同时校验 `public.admin_has_permission(p.finance_id, 'payments.refunds:write')` 与 `public.admin_has_permission(p.finance_id, 'payments.disputes:write')`；`cms-backend/internal/billing/rbac_routes_test.go:210-217` 已新增 `TestCaseCanRejectSQLRequiresFinanceDisputesAndRefundsWrite`。 |

### 第 2 轮复审实测结果

| 验证项 | 执行内容 | 结果 |
| --- | --- | --- |
| `cms-backend` 静态检查与核心单测 | `go vet ./... && go test -count=1 ./internal/billing/... ./internal/db/... ./internal/auth/...` | **PASS** |
| 三端真实 PostgreSQL 集成测试 | 临时独立库 `rbac_cr_verify2`（测完自动清理）运行 `cms-backend`（`internal/db` RBAC 迁移与引导测试、`internal/billing` 全量）、`reader-backend`（`internal/db`、`internal/payment/aftercare`、`internal/payment/settlement`）、`conversion-backend`（`internal/delivery`、`internal/operations`、`cmd/conversion-ops`） | **全部 PASS** |
| 非 owner 角色授权端到端实测 | 临时独立库 `rbac_cr_grants2` 执行 `cms-backend -migrate` + `conversion_role_grants.sql`（6 个非 owner 角色）：验证 `cr_cms` 对 `public.roles` / `public.role_permissions` 完整 `INSERT`/`UPDATE`/`DELETE` 与 `admin_has_permission` 均成功；`cr_reader` 只读与 `admin_has_permission` 成功且写 `public.roles` 被正确拒绝；`cr_api` / `cr_retention` 执行对应锁函数均成功（测完已清理临时库与角色） | **PASS** |

**最终结论**：**Approved（通过，可合入发布）**。
