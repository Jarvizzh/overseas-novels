# 商品与定价、Stripe 产品联动创建及充值模板联动 CodeReview 与修复方案 · 2026-11-23

## 1. 评审范围与验证基线

本次 CodeReview 聚焦以下核心业务闭环（静态审计范围覆盖数据库 Schema/迁移、CMS 后端、CMS 前端、Reader 后端、Reader 前端）：

1. **商品与定价管理**：不可变商品模型（`products`）、环境发布状态（`product_publications`）、复制版本（`CopyProduct`）及上/下架/暂停生命周期。
2. **渠道产品联动创建与绑定**：Stripe 自动联动创建 Product / Price 与税码/含税模式（Direct / Managed Payments MoR）、PayPal VIP Plan 自动创建与手动关联、双渠道就绪闸门（`DualChannelReady`）及渠道主用账户切换。
3. **充值模板与 H5 收银台联动**：模板与卡位配置（`recharge_templates` / `recharge_slots`）、推广链接模板归因（`promotion_links` / `X-Recharge-Template-ID`）、Reader 端槽位水合、报价锁定（`resolveProductForCheckoutTx` / `validateVIPQuoteTx`）与结算页展示。

### 1.1 当前基线测试观测（2026-11-23）

* **CMS 后端**（`cd cms-backend && go test ./internal/billing/... ./internal/novel/... ./internal/db/... -v`）：
  * 退出码 `0`；`internal/novel`（11 项单测）与 `internal/db`（1 项非 DB 单测）通过；
  * **DB 集成盲区**：由于未配置 `STAR_NOVEL_TEST_DATABASE_URL`，`internal/billing` 全部 3 个集成测试（`TestChannelLifecycleEncryptionValidationAndPrimarySwitch`、`TestProductCatalogDualChannelGateAndTemplateDecoupling`、`TestPaymentCenterRefundAuthorizationIntegration`）及 `internal/novel`、`internal/db` 的 DB 测试均被 **SKIP**。
* **Reader 后端**（`cd reader-backend && go test ./internal/payment/... ./internal/novel/... ./internal/auth/... -v`）：
  * 退出码 `0`，纯单测与协议模拟测试（如 `internal/payment/stripe`、`internal/payment/paypal`、`TestPaymentAndWalletKeepReaderRoutes` 等）通过；
  * 需要临时 PostgreSQL 的集成测试（包括 `TestSixH5PurchaseFlowsEndToEnd`、`TestStripeSandboxCheckoutSettlementAndRefund`、`TestStripeVIPInvoiceLifecycle`、`TestVIPOfferPublicationAndVersionPinnedCheckout` 等）均被 **SKIP**。
* **CMS 前端**（`cd cms-front && ./node_modules/.bin/tsc -b --noEmit && npm run lint`）：TypeScript 类型检查通过；`oxlint`（71 个文件）0 error / 0 warning。
* **Reader 前端**（`cd reader-front && ./node_modules/.bin/tsc -b --noEmit && npm run lint`）：TypeScript 类型检查通过；`oxlint`（33 个文件）0 error / 0 warning。

### 1.2 已确认产品与架构定稿口径（2026-11-23）

1. **广告归因与模板绑定（对应 F-04）**：保持“首次触达归因（`users.promotion_link_id` 首次绑定后不覆盖）”，符合海外买量 ROAS/LTV 与 CAPI Pixel 准确回传口径；全面移除推广链接中的 `?template_id=` 与前端 `X-Recharge-Template-ID` 死链路及网关对应放行。
2. **付费墙与模板外直购边界（对应 F-11）**：所有通过 H5 Reader 端付费墙/充值模板购买的下单，**必须严格命中用户当前生效模板槽位（禁止无条件 `slot_id=0` 直购）**；当前阶段先全面封堵 `slot_id=0` 通道，并预留 `campaign_id` 活动准入模型，供后续推出优惠活动页面时按“活动白名单 + 用户资格/限购校验”显式支持 `single` / `whole_book` / `vip` 模板外直购。

---

## 2. 缺陷总览（共 12 项：P1 × 8，P2 × 4）

| 编号 | 严重度 | 所属模块 | 问题摘要 | 核心涉事文件 |
| --- | --- | --- | --- | --- |
| F-01 | P1 | Stripe 联动 | 已绑定 MoR 修改 `tax_code`/`tax_behavior` 被静默吞掉；`sandbox` 可能漏建含税 Price；前端回显硬编码默认值 | [products.go](../cms-backend/internal/billing/products.go#L1349) · [ProductsPage.tsx](../cms-front/src/pages/ProductsPage.tsx#L307) |
| F-02 | P1 | 渠道绑定与主账户 | 绑定接口未校验渠道账户状态与商品 `retired` 态；非主账户切换 UI 断层；切换主用账户会立即使全站已上架商品在 H5 消失 | [products.go](../cms-backend/internal/billing/products.go#L1187) · [payment_channels.go](../cms-backend/internal/billing/payment_channels.go#L861) |
| F-03 | P1 | 商品生命周期 | `Pause` / `Retire` 在 `live` 环境缺失 `SuperAdmin` 与必填原因门禁，且未校验关联模板最小 4 槽位约束 | [products.go](../cms-backend/internal/billing/products.go#L1569) |
| F-04 | P1 | 推广链接 / H5 模板 | `?template_id=` 与 `X-Recharge-Template-ID` 仅前端采集发送、网关放行 CORS，Reader 后端完全不消费 | [promotion.ts](../cms-front/src/utils/promotion.ts#L121) · [api.ts](../reader-front/src/utils/api.ts#L69) · [checkout.go](../reader-backend/internal/payment/application/checkout.go#L52) |
| F-05 | P1 | 充值模板联动 | 模板未校验发布环境与 `DualChannelReady`；前端混选 Sandbox/Live 商品，导致 Live 收银台静默少卡 | [service.go](../cms-backend/internal/billing/service.go#L211) · [TemplatesTab.tsx](../cms-front/src/pages/TemplatesTab.tsx#L130) |
| F-06 | P1 | 充值模板删除 | 前端宣称“已引用不可删除”，后端未检查 `promotion_links`，触发 `ON DELETE SET NULL` 静默脱钩并遗留无效 URL 参数 | [TemplatesTab.tsx](../cms-front/src/pages/TemplatesTab.tsx#L257) · [service.go](../cms-backend/internal/billing/service.go#L329) · [schema.sql](../cms-backend/db/schema.sql#L237) |
| F-07 | P1 | 充值模板编辑 | 编辑模板全删全插重置所有 `recharge_slots.id`，导致未刷新页面及在途 VIP 结账报错；且允许把唯一默认模板改为非默认 | [service.go](../cms-backend/internal/billing/service.go#L296) · [vip_checkout.go](../reader-backend/internal/payment/application/vip_checkout.go#L211) |
| F-08 | P1 | Reader H5 展示 | `single` 与 `whole_book` 槽位错取 `s.vip_name` 忽略 `s.name`，导致未填别名时丢失 `products.name` | [catalog_repository.go](../reader-backend/internal/payment/application/catalog_repository.go#L120) · [Recharge.tsx](../reader-front/src/pages/Recharge.tsx#L365) |
| F-09 | P2 | 渠道异常恢复 | Stripe 远端 Product/Price 被 Archive 后无自愈重绑路径；PayPal VIP 任意报错永久锁死 `review_required` | [products.go](../cms-backend/internal/billing/products.go#L755) · [products.go](../cms-backend/internal/billing/products.go#L1350) |
| F-10 | P2 | 商品复制与作用域 | `CopyProduct` API 漏继承 `src.Bonus`；前端 `specific_novels` 取消勾选不能消除 `manual_novel_ids` 残留 | [products.go](../cms-backend/internal/billing/products.go#L579) · [ProductsPage.tsx](../cms-front/src/pages/ProductsPage.tsx#L442) |
| F-11 | P2 | Reader 直购下单 | 仅传 `product_id`（`slot_id=0`）时 `single`/`whole_book` 绕过模板归属而 `vip` 必卡死并报 `ErrVIPQuoteChanged` | [product_catalog.go](../reader-backend/internal/payment/application/product_catalog.go#L67) · [vip_checkout.go](../reader-backend/internal/payment/application/vip_checkout.go#L217) |
| F-12 | P2 | 遗留代码与迁移 | 旧版 Offer 接口/页面/`reconcilePayPalVIPOffers` 仍在维护面内；`2026111928.sql` 硬编码假定默认模板 `id=1` 存在 | [handler.go](../cms-backend/internal/billing/handler.go#L56) · [2026111928.sql](../cms-backend/db/migrations/2026111928.sql#L266) |

---

## 3. 详述与完整修复方案

### F-01 · [P1] Stripe 已绑定 MoR 商品修改税码/含税方式被静默忽略，Sandbox 可能漏建 Price，UI 硬编码默认值

#### 源码证据
* [products.go#L1349-L1362](../cms-backend/internal/billing/products.go#L1349-L1362)：`ensureAndVerifyStripeCatalog` 在已有 `prodRef != "" && priceRef != ""` 时优先调用 `verifyStripeCatalogPair`，若返回 `err == nil` 则直接复用并返回。
* [products.go#L1410-L1412](../cms-backend/internal/billing/products.go#L1410-L1412)：`verifyStripeCatalogPair` 仅校验 `price.Product.TaxCode == ""` 与 `(live && price.TaxBehavior != "inclusive" && price.TaxBehavior != "exclusive")`，**没有比对远端值与请求 `req.TaxCode` / `req.TaxBehavior` 是否一致**。
* [ProductsPage.tsx#L307-L313](../cms-front/src/pages/ProductsPage.tsx#L307-L313)：`syncBindingFormsFromProduct` 每次固定写入 `tax_code: 'txcd_10103001'`、`tax_behavior: 'exclusive'`，未读取 `validated_catalog`。

#### 触发路径与危害
1. 商品以 `merchant_of_record`（`txcd_10103001` / `exclusive`）绑定后，将 `tax_code` 改为新值或将 `tax_behavior` 改为 `inclusive`：后端命中 `verifyStripeCatalogPair == nil`，直接返回旧 `prod_`/`price_` 与旧 `validated_catalog` 并响应 `200 verified: true`。线上结算仍按旧含税方式或旧税码执行。
2. 在 `sandbox` 环境下，若 `prod_` 已带有非空 `tax_code`（如曾以 MoR 创建，切回 `direct` 后再切回 `merchant_of_record`），`live && ...` 短路导致 `verifyStripeCatalogPair` 放行 `tax_behavior = "unspecified"` 的旧 `price_`，跳过新建含税 Price。
3. 运营重新打开任意 MoR 商品抽屉看到默认值 `exclusive` / `txcd_10103001`，与实际 Stripe 资源脱节。

#### 完整修复方案
1. **后端核验严格匹配目标配置（[products.go](../cms-backend/internal/billing/products.go#L1328-L1414)）**：
   * 扩展 `verifyStripeCatalogPair` 签名，传入 `expectedTaxCode, expectedTaxBehavior string`：
     * 若 `!price.Active || !price.Product.Active || price.Live != live || price.Product.Live != live || !usd || unit_amount != expectedCents || cycleMismatch`，返回不可复用错误（带类型标记，区分“可升级字段差异”与“远端失活/规格冲突”）；
     * 当 `salesMode == stripeManagedPayments` 时，**不论 `sandbox` 或 `live`**，严格校验：
       * `price.Product.TaxCode == expectedTaxCode`；
       * `price.TaxBehavior == expectedTaxBehavior`（必须等于 `"inclusive"` 或 `"exclusive"`）。
   * 调整 `ensureAndVerifyStripeCatalog` 分支：
     * 校验前明确检测“可原地更新项”：`needTaxCodeUpdate := req.SalesMode == stripeManagedPayments && currentProdTaxCode != req.TaxCode`，`needNewPrice := req.SalesMode == stripeManagedPayments && currentPriceTaxBehavior != req.TaxBehavior`；
     * 若 `needTaxCodeUpdate`，调用 `updateStripeCatalogProductTaxCode` 更新远端 Product 税码；
     * 若 `needNewPrice`，将 `priceRef = ""` 触发创建新 Price。
   * **规避 Stripe 24h 幂等键碰撞**：
     * 场景：若操作员在 24h 内做 `inclusive -> exclusive -> inclusive`，直接沿用 `"stripe:price:" + reqKey + ":" + salesMode + ":" + taxBehavior` 可能拿到被之前替代的旧 Price 或被 Stripe 拒绝；而在 `exclusive` 下改 `tax_code` 虽复用 Price，但更换 `tax_behavior` 需要唯一新建意图。
     * 修复方式：仅当本次确实需要新建 Price（且原绑定已有不同 `external_price_ref`）时，在 `prepareStripeBindingWriteTx`（或新增 `catalog_revision INT NOT NULL DEFAULT 1`）中递增该绑定行的版本号 `catalog_revision`，将 Price 幂等键设为 `"stripe:price:" + reqKey + ":v" + strconv.Itoa(catalogRevision) + ":" + req.SalesMode + ":" + req.TaxBehavior`（保持并发重试/超时恢复时在同一次修订内幂等键完全稳定）。
     * **注意**：不要在已有未关闭 `Checkout Session` 时贸然归档旧 `price_`，可在日志与 `validated_catalog` 中记录最新生效 `price_ref`。
2. **前端回显真实配置（[ProductsPage.tsx](../cms-front/src/pages/ProductsPage.tsx#L297-L319)）**：
   * 在 `paymentCatalogApi.ts` / `ProductsPage.tsx` 解析 `stripeBinding.validated_catalog`（支持读取 `validated_catalog?.product?.tax_code` 与 `validated_catalog?.tax_behavior`）；
   * 若 `validated_catalog` 存在对应字段则回填实际值，仅当首次绑定或字段缺失时回退 `'txcd_10103001'` / `'exclusive'`。

---

### F-02 · [P1] 渠道账户/商品状态校验缺失 + 非主账户管理断层 + 切换主用账户中断全站已上架商品

#### 源码证据
* [products.go#L719-L733](../cms-backend/internal/billing/products.go#L719-L733)、[products.go#L1187-L1201](../cms-backend/internal/billing/products.go#L1187-L1201)：当 `req.AccountID > 0` 时，未校验 `payment_channel_accounts.status = 'published'`，也未校验目标环境 `product_publications.status <> 'retired'`。
* [ProductsPage.tsx#L212-L216](../cms-front/src/pages/ProductsPage.tsx#L212-L216) & [ProductsPage.tsx#L1198-L1208](../cms-front/src/pages/ProductsPage.tsx#L1198-L1208)：账户下拉包含 `draft` 账户；`onChange` 仅改 `stForm.account_id`，不从 `all_bindings` 切换对应绑定信息。
* [products.go#L418-L429](../cms-backend/internal/billing/products.go#L418-L429)：列表接口中 `PayPalBinding` / `StripeBinding` 仅反映 `is_primary = true` 账户。
* [payment_channels.go#L861-L870](../cms-backend/internal/billing/payment_channels.go#L861-L870) & [catalog_repository.go#L90-L100](../reader-backend/internal/payment/application/catalog_repository.go#L90-L100)：`PublishPaymentChannel` 直接切换 `is_primary`；Reader 侧 `dualChannelSellableSQL` 强制要求绑定关联 `ast.is_primary = true`。

#### 触发路径与危害
1. 运营可在尚未验证通过或未发布的 `draft` Stripe 账户下提前创建远端 `prod_`/`price_`，甚至通过 API 给 `disabled` 账户或已 `retired` 商品写入绑定。
2. 在生产或沙箱切换 PayPal / Stripe 主用账户（灰度切流/商户轮换）时，旧主用账户的 `is_primary` 瞬间变 `false`。由于新主用账户下没有任何商品绑定（或绑定未完成），**Reader H5 所有 `status='published'` 的商品即刻全部从充值页消失、无法下单**，而 CMS 商品列表没有阻塞也无任何预警。

#### 完整修复方案
1. **绑定端点前置守卫**：
   * 在 `UpsertProductPayPalBinding` 与 `UpsertProductStripeBinding` 读取渠道账户及商品状态时强制校验：
     * 账户 `status == 'published'`（禁止对 `draft` 或 `disabled` 账户写入商品绑定，返回 `409 Conflict`）；
     * 查询 `product_publications` 中 `(product_id, req.Environment)` 的状态，若为 `'retired'`，拒绝新增/更新启用绑定（`409 Conflict`）。
2. **主账户切换预检保护（[payment_channels.go](../cms-backend/internal/billing/payment_channels.go#L783-L900)）**：
   * 在 `publishChannelReq` 中增加可选字段 `ForceSwitchWhenProductsUnready bool`（或提供专门预检逻辑，在无显式确认时拦截）：
   * 当 `setPrimary == true` 且目标环境存在正在切换的旧主用账户时，事务内查询：所有在 `product_publications(environment=$env, status='published')` 中处于上架态的商品，有多少在**目标新主用账户 `id`** 上尚未达到就绪态（`PayPal/Stripe` 各自检查是否满足 `bindingIsReady` 规则）；
   * 若存在未就绪的已上架商品且请求未确认强制切换：返回 `409 Conflict` 并携带 `unready_published_products: [{id, code, name}]`，提示运营先为新账户完成预绑定（借助非主账户预绑定能力）再切主；
   * 为支持“零停机切主”工作流：允许运营提前给 `status='published' AND is_primary=false` 的备账户预先完成所有已上架商品的绑定（Stripe 自动建品 + PayPal VIP 建 Plan），待 `unready_published_products` 清零后一键平滑切换主账户。
3. **前端同步补齐非主账户绑定体验（[ProductsPage.tsx](../cms-front/src/pages/ProductsPage.tsx#L212-L216)、[#L1198-L1243](../cms-front/src/pages/ProductsPage.tsx#L1198-L1243)）**：
   * `envAccounts` 仅过滤 `a.status === 'published'` 供绑定选择（若存在关联的非发布历史绑定可做只读标记）；
   * 抽屉内选择不同 Stripe `account_id` 时，从 `bindingProduct.all_bindings` 中按 `(provider === 'stripe' && b.account_id === Number(v))` 同步更新 `stForm`、当前显示的 `external_product_ref` / `external_price_ref` 与 `validation_error`；PayPal 绑定表单同理补充按 `all_bindings` 中对应 `account_id` 切换展示（或统一支持选备账户预绑定）。

---

### F-03 · [P1] `Pause` / `Retire` 在 `live` 环境权限降级、无必填原因且未护栏模板最小槽位

#### 源码证据
* [products.go#L1434-L1443](../cms-backend/internal/billing/products.go#L1434-L1443)：`PublishProduct` 对 `live` 环境强制要求 `SuperAdmin` 且 `reason` 非空。
* [products.go#L1561-L1630](../cms-backend/internal/billing/products.go#L1561-L1630)：`PauseProduct` / `RetireProduct` 调用 `transitionProductStatus`，仅检查 `requireFinancialAdmin`（`Finance` 即可执行）且不校验 `req.Reason` 非空；也未查询 `recharge_slots` 是否导致模板有效卡位低于 `4`。

#### 触发路径与危害
* `Finance` 角色在无需填写原因、无需 `SuperAdmin` 复核的情况下，可直接把 `live` 线上在售核心包执行不可逆 `retire`，直接破坏线上营收与默认模板槽位完整性（`retired` 后不可再上架）。

#### 完整修复方案
1. **权限与审计原因对称校验（[products.go](../cms-backend/internal/billing/products.go#L1569-L1630)）**：
   * `req.Environment == "live"` 时，改为调用 `requireSuperAdminTx(ctx, tx, actor)`，并要求 `strings.TrimSpace(req.Reason) != ""`（否则返回 `400 Bad Request`）。
   * 若原状态已为 `pubStatusRetired` 再次请求 `retire`，拒绝重复刷新 `retired_at`（返回 `409` 或幂等直接返回不覆盖 `retired_at`）。
2. **模板最小槽位保护**：
   * 在 `transitionProductStatus` 事务内锁行后，校验该商品关联的所有 `recharge_templates`：
     * 查询每个引用该 `product_id` 的模板，在剔除当前商品后，在 `req.Environment` 环境下处于 `status = 'published'` 的剩余槽位数；
     * 若 `req.Environment == "live"` 且会导致默认模板（或被 `promotion_links` 引用的模板）剩余可用槽位 `< 4`，默认返回 `409 Conflict` 携带 `affected_templates`（除非管理员先修改模板或传入显式确认字段并记录审计原因，禁止误操作导致收银台残缺）。
   * 前端 `ProductsPage.tsx` 同步控制：`env === 'live'` 下只有 `canPublish`（`SuperAdmin`）可见/可触发 `pause` 与 `retire` 动作，且弹窗原因输入框为必填。

---

### F-04 · [P1] 推广链接 `?template_id=` / `X-Recharge-Template-ID` 与 Reader 后端脱节（客户端无效链路）

#### 源码证据
* [promotion.ts#L121](../cms-front/src/utils/promotion.ts#L121)：生成推广 URL 携带 `template_id`。
* [App.tsx#L282-L288](../reader-front/src/App.tsx#L282-L288) & [api.ts#L61-L69](../reader-front/src/utils/api.ts#L61-L69)：采集 URL 中 `template_id` 并以 `X-Recharge-Template-ID` 请求头发送。
* [main.go#L241](../reader-backend/cmd/server/main.go#L241)：CORS `Access-Control-Allow-Headers` 允许 `X-Recharge-Template-ID`。
* [handler.go#L244-L262](../reader-backend/internal/payment/application/handler.go#L244-L262) & [checkout.go#L52-L59](../reader-backend/internal/payment/application/checkout.go#L52-L59)：`GetRechargeTemplates` 与 `GetCheckoutTemplate` 从未读取 `X-Recharge-Template-ID`，一律走 `reader_promotion_for_user(user_id)` -> 默认模板兜底。

#### 触发路径与危害
* **安全边界注意**：`reader-backend` 刻意没有直接相信前端传入的裸 `X-Recharge-Template-ID`，是为了防止攻击者在浏览器修改请求头套用低价推广模板下单。
* 但前后端实现处于“前端努力传、网关显式放行、后端完全忽略”的半吊子状态：运营在链接管理里给测试手机或老用户发了带 `?template_id=...` 的推广链接，如果用户之前已有账号（`users.promotion_resolved=true`）或使用无 `link_id` 的预览 URL，模板切换**静默无效**，排查困难。

#### 完整修复方案（已定稿：坚持首触归因，移除客户端裸模板参数）
1. **清理前端与网关无效参数/请求头**：
   * `cms-front/src/utils/promotion.ts`（`validateAndBuildPromotionPayload`）：移除 `if (input.rechargeTemplateId) params.set('template_id', input.rechargeTemplateId);`，广告落地 URL 仅携带经服务端校验的 `link_id` 与 `utm_source`/`utm_campaign`/`pixel_id`；
   * `reader-front/src/App.tsx`：移除 `URLSearchParams` 中 `'template_id'` 的读取与 `readerStorage.setItem('recharge_template_id', ...)`（保留现有 `utm_*`、`link_id`/`promo_id`、`fbclid`、`pixel_id` 逻辑）；
   * `reader-front/src/utils/api.ts`：移除 `readerStorage.getItem('recharge_template_id')` 与 `headers.set('X-Recharge-Template-ID', ...)`；
   * `reader-backend/cmd/server/main.go`（与 `cms-backend/cmd/server/main.go`）：从 CORS `Access-Control-Allow-Headers` 中移除 `X-Recharge-Template-ID`，确保契约前后端一致。
2. **维持首触归因口径与未来扩展示意**：
   * 坚持 `auth/repository.go`（`resolve_reader_promotion(X-Promo-ID, utm_source, utm_campaign)` -> `users.promotion_link_id`、`promotion_resolved = true`）及 `reader_promotion_for_user(uid)` 的首次触达锁定策略，保障海外买量 ROAS/LTV 与 Meta CAPI 回传准确；
   * 在 CMS 推广配置页面（`PromotionsTab.tsx` / `PromotionLinkModal.tsx`）补充说明文案：“该模板绑定仅对通过此推广链接首次注册/首次触达的读者生效；已归因老用户仍按首触推广或默认模板计价”；
   * **未来扩展预留**：若运营需手机端即时验收某套未绑定账号的新模板，后续应采用 CMS 颁发的**短期签名预览 Token**（仅允许只读预览 `GetRechargeTemplates`，严禁下单）或使用无痕访客会话配合测试 `link_id` 验证；若未来需支持再营销（Retargeting）独立分析，再在订单表区分 `acquisition_promo_id`（首触）与 `session_promo_id`（当次进站），**绝不恢复裸 `X-Recharge-Template-ID`**。

---

### F-05 · [P1] 充值模板跨环境校验缺失，Sandbox-only 商品可进入模板导致 Live 线上少卡

#### 源码证据
* [service.go#L211-L222](../cms-backend/internal/billing/service.go#L211-L222)：`validateTemplateSlotsTx` 仅校验 `pub.status = 'published'`，**未过滤 `pub.environment`**，也未校验 `dual_channel_ready`。
* [TemplatesTab.tsx#L130-L143](../cms-front/src/pages/TemplatesTab.tsx#L130-L143) & [TemplatesTab.tsx#L456-L458](../cms-front/src/pages/TemplatesTab.tsx#L456-L458)：合并 `sandbox` 与 `live` 的已上架商品为同一个下拉列表，无环境区分标签。

#### 触发路径与危害
* 若商品只在 `sandbox` 完成绑定与上架，尚未在 `live` 上架，运营就能将其选入默认模板或推广模板并保存成功。到生产环境后，Reader 端 `dualChannelSellableSQL` 按 `live` 过滤，直接将该槽位隐藏，造成线上收银台缺失关键档位甚至不足 4 个可买槽位。

#### 完整修复方案
1. **明确模板环境约束并校验就绪度（[service.go](../cms-backend/internal/billing/service.go#L190-L224) + 迁移可选）**：
   * 鉴于模板表为全局配置表，同时为 Sandbox 验证与 Live 生产服务：
     * **默认规则（最稳妥）**：创建/更新模板时要求所选每个商品**至少在 `live` 环境为 `published`（或如是 Sandbox 专用测试模板需显式标记 `environment = 'sandbox'`）**；若暂不拆分模板环境字段，至少应在 `validateTemplateSlotsTx` 中强制校验所选商品集**必须同时在 `live` 环境全部 `published` 且满足双渠道就绪**（若允许纯沙箱测试模板，可给 `recharge_templates` 增加 `environment VARCHAR(16) NOT NULL DEFAULT 'live' CHECK (environment IN ('sandbox','live'))`，并在 `Create/UpdateRechargeTemplate` 按该模板的 `environment` 严格匹配 `pub.environment = $env` + 双渠道就绪 SQL）。
     * 建议同时阻止对“主账户已被切换或凭证停用导致失去双渠道就绪”的商品执行新模板装配。
2. **前端明确环境维度筛选（[TemplatesTab.tsx](../cms-front/src/pages/TemplatesTab.tsx#L130-L148)、[#L455-L476](../cms-front/src/pages/TemplatesTab.tsx#L455-L476)）**：
   * 下拉选项显式标出商品的 `[Live 已上架]` / `[仅 Sandbox]`（或根据模板目标环境仅加载 `live` 已上架且 `dual_channel_ready = true` 的商品），在槽位行上给出告警 badge，消除误选。

---

### F-06 · [P1] 删除充值模板前端文案与后端逻辑不一致：`promotion_links` 引用未拦截

#### 源码证据
* [TemplatesTab.tsx#L257](../cms-front/src/pages/TemplatesTab.tsx#L257)：前端提示 `"确认删除模板「...」吗？已被推广链接引用的模板无法删除。"`。
* [service.go#L329-L357](../cms-backend/internal/billing/service.go#L329-L357)：`DeleteRechargeTemplate` 仅拦截 `is_default`，**未校验 `promotion_links`**。
* [schema.sql#L237](../cms-backend/db/schema.sql#L237)：`promotion_links.recharge_template_id` 外键策略为 `ON DELETE SET NULL`。

#### 触发路径与危害
* 正在跑广告流量的推广链接所绑定的模板可直接被删除，链接表的 `recharge_template_id` 静默变 `NULL`，新客/绑定老客全量切回默认模板，产生不可预期转化波动与统计口径失真。

#### 完整修复方案
1. **后端事务加锁阻断引用删除（[service.go](../cms-backend/internal/billing/service.go#L329-L357)）**：
   * 在 `SELECT is_default FROM recharge_templates WHERE id=$1 FOR UPDATE` 之后，补充查询引用检查：
     ```sql
     SELECT count(*) FROM promotion_links WHERE recharge_template_id = $1 FOR SHARE
     ```
   * 需注意并发竞态：若并发恰好有人创建/修改 `promotion_links` 引用该模板，由于删除事务先持模板 `FOR UPDATE` 行锁，建议在 `CreatePromotionLink` / `UpdatePromotionLink`（[novel/repository.go](../cms-backend/internal/novel/repository.go#L453-L487)）中，当 `RechargeTemplateID != nil` 时做存在性及锁检查（例如 `SELECT 1 FROM recharge_templates WHERE id = $1 FOR SHARE` 或依赖事务顺序与外键，但因为 `promotion_links` 外键为 `ON DELETE SET NULL` 而非 `ON DELETE RESTRICT`，**最根本稳妥的办法是在新迁移中将 `promotion_links.recharge_template_id` 外键改为 `ON DELETE RESTRICT` + 业务层显式返回 `409 Conflict`**）。
   * 新增错误码 `ErrTemplateReferenced`（HTTP `409`），并返回具体引用推广链接数量/名称便于管理员先去解绑。
2. **前端增强展示**：`GET /recharge-templates` 返回 `linked_promotion_count`，列表页展示绑定推广链接数，计数 `> 0` 时禁用删除按钮或提示具体引用的推广链接名称。

---

### F-07 · [P1] `UpdateRechargeTemplate` 全删全插重置 `slot.id`（且允许取消唯一默认模板），破坏在途 VIP 结账

#### 源码证据
* [service.go#L296-L321](../cms-backend/internal/billing/service.go#L296-L321) & [repository.go#L281-L304](../cms-backend/internal/billing/repository.go#L281-L304)：每次更新模板先 `DELETE FROM recharge_slots WHERE template_id = $1` 再全量 `INSERT`。
* [vip_checkout.go#L211-L227](../reader-backend/internal/payment/application/vip_checkout.go#L211-L227) & [stripe_vip_repository.go#L195](../reader-backend/internal/payment/application/stripe_vip_repository.go#L195)：VIP 启动和恢复严格匹配 `s.id = $slot_id`。
* [service.go#L296-L309](../cms-backend/internal/billing/service.go#L296-L309)：传入 `is_default = false` 更新当前唯一默认模板不会报错。

#### 触发路径与危害
1. 运营仅仅把某模板的标题改个字、或把某张卡角标从 `"HOT"` 改成 `"Popular"`、或调换两张卡顺序，导致该模板下全部 `recharge_slots.id` 失效重建：
   * 用户在 H5 已加载的 `/recharge` 页面点支付即报 `checkout_unavailable`；
   * 正在进行中的 `Pending` VIP 意向（选择渠道/切渠道重试）立即报 `ErrVIPQuoteChanged` 失效。
2. 编辑唯一默认模板取消勾选 `is_default` 后，全库失去默认模板，线上只能退化为任意取 `id` 最小的模板。

#### 完整修复方案
1. **按 `(template_id, product_id)` 增量差异更新 `recharge_slots` 保留原 `id`（[service.go](../cms-backend/internal/billing/service.go#L276-L327) & [repository.go](../cms-backend/internal/billing/repository.go#L266-L312)）**：
   * 同一模板内 `product_id` 唯一（`validateTemplateSlotsTx` 已保证 `seenProd` 不重复），且 `recharge_slots` 表除 `UNIQUE (template_id, slot_index)` 外无 `slot_index > 0` 检查约束（已核对 `schema.sql` 及全量迁移）；
   * 事务内实现四步原位同步：
     1. 查询并锁住当前模板已有槽位：`SELECT id, product_id FROM recharge_slots WHERE template_id = $1 FOR UPDATE`；
     2. **删除已移除商品的旧槽位**：对原表有但新 `slots` 中不存在的 `product_id`，执行 `DELETE FROM recharge_slots WHERE id = ANY($1::int[])`；
     3. **规避唯一索引冲突并原位更新保留商品的槽位**：
        * 对新旧交集 `product_id` 对应的旧行，先执行 `UPDATE recharge_slots SET slot_index = -id WHERE id = ANY($1::int[])`（全部为唯一负整数，不与正数 `1..8` 冲突）；
        * 再逐行按 `id` 更新 `slot_index`、`badge`、`is_highlighted`、`display_alias` —— **所有未移除商品的 `recharge_slots.id` 100% 保持不变**；
     4. **插入新增商品的槽位**：仅对新加进来的 `product_id` 执行 `INSERT INTO recharge_slots`。
2. **默认模板保护（[service.go](../cms-backend/internal/billing/service.go#L296-L309)）**：
   * 更新事务中锁行读取旧 `wasDefault`。若 `wasDefault == true && !isDefault`，校验库中是否存在其他 `is_default = true` 的模板；若不存在，返回 `409 Conflict`（`"不能直接取消唯一默认模板，请先将其他模板设为默认"`），前端同时禁用编辑唯一默认模板时取消勾选“设为默认”。

---

### F-08 · [P1] Reader H5 `single` / `whole_book` 卡片错取 `vip_name`，留空别名时丢失商品原名

#### 源码证据
* [catalog_repository.go#L120-L124](../reader-backend/internal/payment/application/catalog_repository.go#L120-L124)：`scanHydratedSlot` 仅在 `s.ProductType == "vip"` 时才填充 `s.VipName = s.Name`；对 `single` 与 `whole_book`，商品名称在 `s.Name`（JSON 字段 `name`），而 `s.VipName` 为空。
* [Recharge.tsx#L78-L95](../reader-front/src/pages/Recharge.tsx#L78-L95) & [Recharge.tsx#L365](../reader-front/src/pages/Recharge.tsx#L365) & [Recharge.tsx#L1009-L1040](../reader-front/src/pages/Recharge.tsx#L1009-L1040)：`RechargeSlotItem` 类型漏声明 `name?: string`，且 `coinPacks` 映射写了 `name: s.display_alias || s.vip_name`。

#### 触发路径与危害
* 当模板槽位未配置 `display_alias` 时，`single` 金币包丢失商品名副标题，`whole_book` 整本解锁卡片永远无法展示 CMS 配置的商品名称 `products.name`（固定写死回退 `'Unlock Entire Novel'`）。

#### 完整修复方案
1. **Reader 前端补全类型与字段回退（[Recharge.tsx](../reader-front/src/pages/Recharge.tsx#L78-L95)、[#L342](../reader-front/src/pages/Recharge.tsx#L342)、[#L365](../reader-front/src/pages/Recharge.tsx#L365)）**：
   * 在 `RechargeSlotItem` 补充 `name?: string; description?: string;`；
   * `coinPacks` 改为 `name: s.display_alias || s.name || s.vip_name || ''`；
   * 同步完善 `subscriptionPacks` 为 `name: s.display_alias || s.name || s.vip_name || vipCycleLabel(cycle)`、`desc: s.description || s.vip_desc || '...'`。

---

### F-09 · [P2] Stripe 远端 Archive 无自愈机制；PayPal VIP 任意错误即永久锁死 `review_required`

#### 源码证据
* [products.go#L1101-L1103](../cms-backend/internal/billing/products.go#L1101-L1103) & [products.go#L1350-L1388](../cms-backend/internal/billing/products.go#L1350-L1388)：Stripe 远端 Product/Price 被设为 `active=false` 后，`verifyStripeCatalogPair` 拒绝复用，同时接口禁止人工传入参考 ID 且不主动重置重创。
* [products.go#L755-L758](../cms-backend/internal/billing/products.go#L755-L758) & [products.go#L886-L941](../cms-backend/internal/billing/products.go#L886-L941)：PayPal VIP 创建中任意失败（包含确定性 4xx 校验错）一律返回 `reviewReq = true`，导致后续自动建 Plan 被 `409 Conflict` 永久锁死。

#### 完整修复方案
1. **Stripe 受控重建开关**：
   * 在 `bindStripeProductReq` 增加 `Reprovision bool` 标志（或当 `validation_status == 'failed'` 且确认 Stripe 远端返回 `active=false` 时，由 `SuperAdmin` 显式传 `reprovision: true` + 变更 `reason`）：
     * 在事务内校验当前 `binding` 的既有 `external_product_ref`/`external_price_ref` 确实因 `active=false` 失活（避免误替正常在用 Price）；
     * 清空失效 ref（若仅 Price 失活则只清空 `priceRef`；若 Product 失活则清空 `prodRef` 和 `priceRef`），生成新 `request_key`（并审计记录旧的 `external_product_ref`/`external_price_ref`）重新联动创建。
2. **PayPal VIP 错误分级与幂等隔离**：
   * 改造 `sendPayPalJSON` 返回结构化错误（区分 HTTP status code 与网络/超时/解包错）：
     * 仅在 **结果不确定窗口**（网络错/超时、HTTP `5xx`、返回 `2xx` 但响应体缺少 ID、`CreatePlan` 已发出成功但 `verifyPayPalPlanRemote` 失败）时设置 `reviewReq = true`；
     * 对于 OAuth 鉴权失败或 PayPal 返回确定性 `400/401/403/422` 拒单错误时，`reviewReq = false`，让运营修复描述/凭证后可直接重试自动创建，无需手动去 PayPal 后台建 Plan。
   * 将 `prodIdem` 由 `"paypal:catalog-product:" + code` 改为基于 `(product_id, account_id)` 的 `"paypal:catalog-product:" + reqKey`，确保多账户/重置场景隔离。

---

### F-10 · [P2] `CopyProduct` 漏拷贝 `bonus`；前端 `specific_novels` 取消勾选无效

#### 源码证据
* [products.go#L579-L600](../cms-backend/internal/billing/products.go#L579-L600)：`CopyProduct` 继承了 `Coins`/`PriceCents`/`VipCycle`/`NovelScopeType`/`NovelScopeIDs`，但遗漏 `src.Bonus`。
* [ProductsPage.tsx#L238-L250](../cms-front/src/pages/ProductsPage.tsx#L238-L250) & [ProductsPage.tsx#L442-L449](../cms-front/src/pages/ProductsPage.tsx#L442-L449)：`openCopyDrawer` 双写 `novel_scope_ids` 与 `manual_novel_ids`，而 `toggleNovelId` 取消勾选仅删减 `novel_scope_ids`，最终 `parseSpecificNovelIds()` 取并集恢复了被取消勾选的 ID。

#### 完整修复方案
1. **后端准确区分“未传”与“显式传 0”（[products.go](../cms-backend/internal/billing/products.go#L555-L608)）**：
   * `CopyProduct` 改用独立结构体 `copyProductReq`（对 `Coins *int`、`Bonus *int`、`PriceCents *int` 使用指针字段），当 `req.Bonus == nil && req.ProductType == productTypeSingle` 时回填 `src.Bonus`；仅当请求明确传 `bonus: 0` 时覆盖为 0。
2. **前端统一单一状态源（[ProductsPage.tsx](../cms-front/src/pages/ProductsPage.tsx#L225-L250)、[#L442-L449](../cms-front/src/pages/ProductsPage.tsx#L442-L449)、[#L1003-L1028](../cms-front/src/pages/ProductsPage.tsx#L1003-L1028)）**：
   * 方案：`openCopyDrawer` 中只初始化 `novel_scope_ids`（或在 `manual_novel_ids` 仅保留“不在可选列表内的手工输入 ID”）；更彻底地，在 `toggleNovelId(novelId)` 取消勾选某 `novelId` 时同步过滤掉 `manual_novel_ids` 文本中的该 ID 字符串，保证勾选框状态与最终提交数组 100% 一致；
   * 顺带优化复制建议 `code`：去掉末尾已有的 `_V\d+` 再自增版本号（如 `_V2` -> `_V3`），避免 `_V2_V2` 无限叠加截断。

---

### F-11 · [P2] `slot_id == 0` 仅传 `product_id` 的结账分支在单品与 VIP 间语义分裂

#### 源码证据
* [product_catalog.go#L67-L94](../reader-backend/internal/payment/application/product_catalog.go#L67-L94) & [#L118-L124](../reader-backend/internal/payment/application/product_catalog.go#L118-L124)：`productID > 0, slotID == 0` 时若商品不在当前用户生效模板内，`p.SlotID` 为 `0` 且 `p.TemplateID` 仍被赋为用户当前模板 ID。
* [checkout.go#L117-L118](../reader-backend/internal/payment/application/checkout.go#L117-L118)：`($6 = 0 OR ...)` 放行 `single`/`whole_book`。
* [checkout_intent.go#L159-L168](../reader-backend/internal/payment/application/checkout_intent.go#L159-L168)、[vip_checkout.go#L211-L227](../reader-backend/internal/payment/application/vip_checkout.go#L211-L227) & [2026111928.sql#L382-L408](../cms-backend/db/migrations/2026111928.sql#L382-L408)：VIP 会成功插入 `slot_id = 0` 的 `Pending` 父意向并占住唯一 Pending 索引，但启动 PayPal/Stripe 必因 `WHERE s.id = $2`（`0`）失败报错 `ErrVIPQuoteChanged`。

#### 完整修复方案（已定稿：当前严控模板槽位命中 + 预留活动 `campaign_id` 直购扩展设计）

1. **当前阶段（立即落地）：全面封堵无条件 `slot_id == 0` 绕过模板路径**
   * **请求入口校验**（[handler.go](../reader-backend/internal/payment/application/handler.go#L108-L124) & [checkout_service.go](../reader-backend/internal/payment/application/checkout_service.go#L23-L32)）：
     * 在活动系统未启用前，`preparePurchaseRequest` 与 `PreparePurchase` 要求 `slot_id > 0`（拒绝 `slot_id == 0`，返回 `400` / `ErrCheckoutUnavailable`），防止创建任何 `slot_id = 0` 的孤儿 `Pending` VIP 意向，也杜绝遍历任意已发布 `product_id` 绕过付费墙模板。
   * **商品解析与模板槽位强校验**（[product_catalog.go](../reader-backend/internal/payment/application/product_catalog.go#L60-L125) & [checkout.go](../reader-backend/internal/payment/application/checkout.go#L107-L128)）：
     * 删除 `resolveProductForCheckoutTx` 中 `LEFT JOIN LATERAL` 与 `$3 = 0` 兼容分支，改为强制 `INNER JOIN recharge_slots s`：要求 `s.id = $slot_id`、属于当前用户的生效模板（`COALESCE(reader_promotion_for_user(userID), defaultTemplate)`），且若传了 `product_id > 0` 必须满足 `s.product_id = product_id`；
     * 移除 `insertCheckout`（`checkout.go#L117-L118`）中的 `$6 = 0 OR ...` 绕过条件，强制校验 `s.id = $slot_id AND s.template_id = $template_id AND s.product_id = $product_id`；
     * 若 `whole_book` 仅在用户生效模板中存在对应槽位时才允许通过付费墙下单。
   * **历史测试同步收敛**：将 [six_flows_integration_test.go#L339](../reader-backend/internal/payment/application/six_flows_integration_test.go#L339) 中原 `{product_id: pCoins, slot_id: 0}` 用例更新为传对应有效 `slot_id`，并补充验证 `slot_id == 0` 被拦截的测试。

2. **后续扩展阶段（推出优惠活动页面时）：基于 `campaign_id` 显式放行 `single` / `whole_book` / `vip` 模板外直购**
   * **接口与入参预留设计**：`PreparePurchase` 保持 `slot_id`（付费墙）与 `campaign_id`（优惠活动）两条互斥语义分支：
     * **分支 A（付费墙路径：`slot_id > 0 && campaign_id == 0`）**：严格按上述逻辑命中用户当前模板槽位；
     * **分支 B（活动直购路径：`campaign_id > 0 && product_id > 0 && slot_id == 0`）**：不走 `recharge_slots` / `recharge_templates`，在事务内加用户锁校验：
       1. 目标 `campaign_id` 处于生效状态、未过期、匹配当前运行环境；
       2. `product_id` 在该活动授权的商品白名单内，且满足 `product_publications.status = 'published'` 与 `dualChannelSellableSQL`；
       3. 当前 `user_id` 满足活动人群资格及限购次数（防并发超购）；
       4. 落库 `recharge_orders` 与 `payment_vip_checkout_intents` 时写入 `slot_id = 0`、`template_id = 0`、`campaign_id = $campaign_id`（避免污染常规模板统计）。
   * **VIP 渠道启动/切换兼容扩展**：预留改造 [vip_checkout.go#L211-L227](../reader-backend/internal/payment/application/vip_checkout.go#L211-L227)（`validateVIPQuoteTx`）与 [stripe_vip_repository.go#L195](../reader-backend/internal/payment/application/stripe_vip_repository.go#L195)：当 `slot_id > 0` 时校验 `recharge_slots`；当 `slot_id == 0 && campaign_id > 0` 时改为校验 `campaign` 有效性 + `products` + `product_publications` + `dualChannelSellableSQL`，使 `single`、`whole_book`、`vip` 在活动页均可同等支持模板外直购且不会出现 `ErrVIPQuoteChanged`。

---

### F-12 · [P2] 遗留旧 Offer 体系未清理彻底 + 迁移脚本假定默认模板 `id=1`

#### 源码证据
* [handler.go#L56-L61](../cms-backend/internal/billing/handler.go#L56-L61)、[stripe_offer.go](../cms-backend/internal/billing/stripe_offer.go)、[stripe_vip.go](../cms-backend/internal/billing/stripe_vip.go)、[StripeOfferSettings.tsx](../cms-front/src/pages/StripeOfferSettings.tsx)、[vip_offers.go](../reader-backend/internal/payment/application/vip_offers.go) & [reconcile.go#L32](../reader-backend/internal/payment/application/reconcile.go#L32)。
* [2026111928.sql#L266-L317](../cms-backend/db/migrations/2026111928.sql#L266-L317)。

#### 完整修复方案
1. 确认生产库所有历史 `payment_vip_offer_versions` / `payment_stripe_vip_offers` / `payment_stripe_one_time_offers` 不再需要新写操作后：
   * 下线 CMS 旧路由 `/channels/vip-offers`、`/channels/:id/stripe-one-time`、`/channels/:id/stripe-vip`，删除死文件 `StripeOfferSettings.tsx`；
   * 评估停用 `reconcile.go` 中的 `reconcilePayPalVIPOffers`（或限定仅当存在未处理遗留 `payment_paypal_vip_plans` 且无对应新 `products` 时才打印告警，防止读取空字段误操作）。
2. 迁移规范加固：若后续有类似目录级预置迁移脚本，改用 `SELECT id FROM recharge_templates ORDER BY is_default DESC NULLS LAST, id ASC LIMIT 1` 动态定位默认模板，不再硬编码 `id = 1`。

---

## 4. 补测用例设计（Test Plan & Cases）

> **前提要求**：请在测试环境启动隔离 PostgreSQL 并导出 `STAR_NOVEL_TEST_DATABASE_URL`（同时建议将其加入 CI 门禁，禁止跳过核心 Billing 与 Payment DB 集成测试）。

### 4.1 CMS 后端集成测试（建议落在 `cms-backend/internal/billing/`）

可直接在 [channel_catalog_test.go](../cms-backend/internal/billing/channel_catalog_test.go) 中扩充 `TestProductCatalogDualChannelGateAndTemplateDecoupling` 或新增 `product_template_regression_test.go`：

| 用例 ID | 覆盖缺陷 | 测试名建议 | 前置准备与步骤 | 预期断言 |
| --- | --- | --- | --- | --- |
| TC-CMS-01 | F-01 | `TestStripeBinding_UpdateTaxCodeAndBehaviorReprovisionsPrice` | 1. 先用 `direct` 绑定商品 A（Mock 记录创建 `prod_1` / `price_1`）；<br>2. 升级 `merchant_of_record` (`txcd_10103001`, `exclusive`)；<br>3. 修改为 `inclusive`；<br>4. 修改为 `txcd_10000000` (`inclusive`)；<br>5. 切回 `exclusive` | - 第 2 步更新 Product `tax_code` 且新建 `price_2(tax_behavior=exclusive)`；<br>- 第 3 步新建 `price_3(tax_behavior=inclusive)`，响应和 `validated_catalog` 均更新为 `price_3`；<br>- 第 4 步更新 Product `tax_code=txcd_10000000` 并反映在 `validated_catalog`；<br>- 第 5 步创建 `price_4`（不因 24h 幂等键撞回旧 `price_2`）；<br>- 同步验证 `sandbox` 环境下存在初始 `tax_code` 的 `direct->MoR` 切换也强制校验并建含税 Price |
| TC-CMS-02 | F-02 | `TestBinding_RejectsDraftDisabledAccountAndRetiredProduct` | 1. 创建 `draft` 渠道与 `disabled` 渠道；<br>2. 将商品 B 在 `live` 下标记 `retired`；<br>3. 分别调用 `PUT /bindings/stripe` 与 `PUT /bindings/paypal` | - 绑定 `draft` / `disabled` 账户均返回 `409 Conflict`，未产生外部 HTTP 调用；<br>- 给 `retired` 商品绑定返回 `409 Conflict` |
| TC-CMS-03 | F-02 | `TestPublishChannel_PrimarySwitchGuardsPublishedProducts` | 1. 账户 #1 为主，完成商品 A 双渠道绑定并 `publish`；<br>2. 创建验证新账户 #2（未绑定商品 A）；<br>3. 调用 `POST /channels/#2/publish` (`set_primary: true`) 无强制确认；<br>4. 为 #2 预绑定商品 A 后再 `set_primary: true` | - 第 3 步返回 `409 Conflict` 并列出 `unready_published_products` 包含商品 A，且账户 #1 仍保持 `is_primary=true`；<br>- 第 4 步切换成功，`loadProductViews` 下商品 A `DualChannelReady` 保持 `true` |
| TC-CMS-04 | F-03 | `TestPauseAndRetire_EnforceLiveSuperAdminReasonAndMinSlots` | 1. 在 `live` 环境上架 4 个商品并放入默认模板（共 4 槽）；<br>2. 用 `Finance` 调 `POST /products/:id/pause` (`live`)；<br>3. 用 `SuperAdmin` 不填 `reason` 调 `/retire` (`live`)；<br>4. 填 `reason` 调 `/retire` (`live`)（剩余 < 4 槽） | - 第 2 步返回 `403 Forbidden`；<br>- 第 3 步返回 `400 Bad Request`；<br>- 第 4 步被模板底线检查阻断返回 `409 Conflict` 并提示受影响默认模板 |
| TC-CMS-05 | F-05 | `TestTemplateSave_RejectsSandboxOnlyOrUnreadyProducts` | 1. 商品 X 仅在 `sandbox` `published`（`live` 为 `draft`）；<br>2. 商品 Y 两环境 `published`，但随后禁用其主 Stripe 绑定；<br>3. 分别保存包含 X 或 Y 的模板 | - 面向 `live` 的模板创建/更新对 X 返回 `409`；<br>- 对失去就绪度的 Y 也返回校验失败并说明原因 |
| TC-CMS-06 | F-06 | `TestDeleteTemplate_BlockedWhenReferencedByPromotionLink` | 1. 创建非默认模板 T 并被 `promotion_links` 引用；<br>2. 调 `DELETE /cms/recharge-templates/:T`；<br>3. 解绑 `promotion_links` 后再删除 | - 第 2 步返回 `409 Conflict`，DB 中 `promotion_links.recharge_template_id` 仍为 `T`；<br>- 第 3 步删除成功返回 `200` |
| TC-CMS-07 | F-07 | `TestUpdateTemplate_PreservesSlotIDsAndDefaultInvariant` | 1. 创建默认模板（含 4 槽，记录 `slotID[1..4]`）；<br>2. 仅修改 `name`、`badge` 并互换第 1、2 槽顺序再保存；<br>3. 尝试传 `is_default: false` 保存唯一默认模板 | - 第 2 步后，保留商品对应的 4 个 `recharge_slots.id` **完全不变**，仅 `slot_index` / `badge` 原位更新；<br>- 第 3 步返回 `409`，库内仍保留有效默认模板 |
| TC-CMS-08 | F-09 | `TestStripeArchiveRecoveryAndPayPalVIP4xxRetry` | 1. 已绑定 Stripe 商品 Mock 查询返回 `active: false`，先无 `reprovision` 调用返回失败，再带 `reprovision: true` 调用；<br>2. PayPal VIP 建 Plan Mock 首次返回 `400 Bad Request`，第二次返回 `200 P-NEW`（不手填 Plan ID）；<br>3. 另建一单 Mock `504` 超时后再重试无 `P-` | - 第 1 步重建生成新 `prod_`/`price_` 并更新审计；<br>- 第 2 步首次 `400` 后 `review_required=false`，第二次自动创建直接成功；<br>- 第 3 步超时后 `review_required=true`，未手填 `P-` 重试返回 `409` 防重复建 Plan |
| TC-CMS-09 | F-10 | `TestCopyProduct_PreservesBonusUnlessExplicitlyZero` | 1. 创建 `single` 商品 `coins=1000, bonus=200`；<br>2. `POST /copy` 只传 `{code, name}`（不传 `bonus`）；<br>3. `POST /copy` 显式传 `{code, name, bonus: 0}` | - 第 2 步新商品 `bonus == 200`；<br>- 第 3 步新商品 `bonus == 0` |

### 4.2 Reader 后端集成测试（建议扩充 `reader-backend/internal/payment/application/`）

| 用例 ID | 覆盖缺陷 | 测试名建议 | 前置准备与步骤 | 预期断言 |
| --- | --- | --- | --- | --- |
| TC-RDR-01 | F-04 | `TestTemplateResolution_FirstTouchLockedAndIgnoresTemplateHeader` | 1. 存在推广模板 T2（绑定推广链接 P2）与默认模板 T1；<br>2. 自然注册/已绑定旧推广的用户 U1（`promotion_resolved=true`）点击新推广（带 `X-Promo-ID: P2` 或伪造请求头 `X-Recharge-Template-ID: T2`）请求 `GET /wallet/recharge/templates` 与 `POST /purchase-intents`；<br>3. 经 P2 首次访客建号的新用户 U2 请求同接口 | - U1 保持首触归因不变，仅拿到 T1（跨模板用 T2 槽位下单报 `ErrCheckoutUnavailable`）；<br>- U2 基于 `promotion_links` 绑定拿到 T2 且可正常下单；<br>- 网关 CORS 允许头中已移除 `X-Recharge-Template-ID` |
| TC-RDR-02 | F-07 | `TestVIPCheckout_ContinuesAfterTemplateCosmeticEdit` | 1. 用户预留基于某 VIP 槽位的 `Pending` 意向（未选渠道）；<br>2. 模拟 CMS 仅改写该模板 `badge` / 交换槽位顺序（执行增量保留 `slot.id` 的更新）；<br>3. 用户继续 `StartPayPalCheckout` / `StartStripeCheckout` | - 在途 VIP 意向顺利取得审批链接，不再无故误报 `ErrVIPQuoteChanged` |
| TC-RDR-03 | F-11 | `TestPreparePurchase_BlocksSlotZeroAndRequiresActiveTemplateSlot` | 1. 对已双渠道上架、但未放入用户当前模板（以及已放入模板）的 `single`、`whole_book`、`vip` 商品，分别传 `{product_id, slot_id: 0}` 调用 `PreparePurchase`（无 `campaign_id`）；<br>2. 对在当前模板中的槽位传 `{slot_id: validSlot, product_id: otherProductID}`；<br>3. 验证 `six_flows_integration_test` 中传正确 `slot_id` 跑通全流程 | - 第 1 步三类商品全部被统一拦截（`400`/`ErrCheckoutUnavailable`），DB 不产生 `recharge_orders` 也不残留 `slot_id=0` 的 `Pending` VIP 意向；<br>- 第 2 步交叉校验失败拦截；<br>- 第 3 步通过模板槽位下单的 `single`、`whole_book`、`vip` 均正常履约（预留未来 `campaign_id > 0` 时的白名单直购兼容空间） |
| TC-RDR-04 | F-02 | `TestCatalog_PrimarySwitchImmediatelyHidesUnboundAccountProducts` | 1. 用户正常读到商品 A；<br>2. 主用 Stripe 账户切到未绑定商品 A 的账户 #2 | - 回归验证 `GetRechargeSlots` 与 `PreparePurchase` 均严格受新主账户绑定门禁约束，不会错路由到旧主账户下单 |

### 4.3 前端单测与端到端交互校验（`cms-front` / `reader-front`）

| 用例 ID | 覆盖缺陷 | 验证方式 | 检查点与断言 |
| --- | --- | --- | --- |
| TC-FE-01 | F-01, F-02 | `ProductsPage` 组件单测 / 浏览器验证 | 打开已绑定 MoR 且为 `inclusive` + 非默认 `txcd_*` 的商品抽屉时，表单回显与 `validated_catalog` 完全一致；下拉列表不包含 `draft` 账户；切换 `account_id` 同步切换展示对应绑定信息与历史错误 |
| TC-FE-02 | F-10 | `ProductsPage` `specific_novels` 复制流程验证 | 从带 `specific_novels=[101, 102]` 的整本商品点击“复制” -> 在勾选列表取消勾选 `101` -> 提交 payload 的 `novel_scope_ids` 必须仅为 `[102]`（不被 `manual_novel_ids` 复活） |
| TC-FE-03 | F-05, F-06, F-07 | `TemplatesTab` 流程验证 | 商品选择器标注环境且过滤非 Live / 未就绪 SKU；存在引用推广链接时阻断删除并明确展示引用数量；编辑唯一默认模板时不允许取消默认 |
| TC-FE-04 | F-08 | `Recharge.tsx` 渲染回归（`single` & `whole_book`） | 当 API 返回 `single` / `whole_book` 槽位 `display_alias=""` 且 `name="2026 Starter Pack"` / `"Full Novel Access"` 时，H5 卡片分别正确渲染 `products.name` 而非空字符串或硬编码默认文案 |
| TC-FE-05 | F-04 | `promotion.ts` & `reader-front` 链接/请求校验 | `validateAndBuildPromotionPayload` 生成的落地页 URL 不再包含 `template_id=`；H5 访问 `?template_id=999` 落地页时不再写入 `recharge_template_id` 且接口请求不发送 `X-Recharge-Template-ID` |

---

## 5. 分波次实施建议

1. **Wave 1（高危阻断 & 合规口径 · 优先修复）**：`F-01`（Stripe 税码/含税更新失效）、`F-02`（账户状态校验 + 切主预检保护）、`F-03`（Live 下架 `SuperAdmin` + 原因 + 最小槽位）、`F-05`（模板环境隔离）、`F-07`（模板原位保留 `slot.id`）。
2. **Wave 2（联动体验 & 已定稿契约收敛）**：`F-04`（移除 `X-Recharge-Template-ID` / `?template_id=` 并锁定首触归因）、`F-06`（删除模板拦截推广链接引用）、`F-08`（H5 商品名渲染）、`F-10`（`CopyProduct` `bonus` + 取消勾选 bug）、`F-11`（封堵 `slot_id=0` 强校验模板槽位，预留 `campaign_id` 扩展设计）。
3. **Wave 3（自愈与技术债清理 + 全量隔离 DB 回归）**：`F-09`（Stripe Archive 重建 + PayPal 4xx 解除误锁）、`F-12`（下线旧 Offer 接口与页面）；配置 `STAR_NOVEL_TEST_DATABASE_URL` 跑通全部 Go DB 集成测试与前端校验后合入。

---

## 6. 第一轮复审验收与新发现遗留缺陷（2026-11-23 复审）

> 本轮由 Reviewer 在本地 PostgreSQL（`STAR_NOVEL_TEST_DATABASE_URL="postgres://postgres:starnovel_dev_local@127.0.0.1:15439/starnovel_local_test?sslmode=disable"`）实库环境下完成全量代码 Diff 审计与回归测试复核。

### 6.1 F-01 ~ F-12 逐项验收结论

| 编号 | 复审状态 | 修复核验摘要 | 已验证通过的回归用例 |
| --- | --- | --- | --- |
| F-01 | **已修复** | 新增 `verifyStripeCatalogPairDetailed` / `extractStripeCatalogTax` / `catalog_revision` / `advanceStripePriceRevisionTx`；UI 从 `validated_catalog` 回显 | `TestStripeBinding_UpdateTaxCodeAndBehaviorReprovisionsPrice`、`TC-FE-01` |
| F-02 | **已修复** | 绑定拒绝 `draft`/`disabled` 账户与 `retired` 商品；切主 preflight 阻断并返回 `409 unready_published_products`；UI 过滤 `published` 账户并切换 `all_bindings` | `TestBinding_RejectsDraftDisabledAccountAndRetiredProduct`、`TestPublishChannel_PrimarySwitchGuardsPublishedProducts`、`TestCatalog_PrimarySwitchImmediatelyHidesUnboundAccountProducts`、`TC-FE-01` |
| F-03 | **已修复（待配合 R-1 收敛）** | `live` 暂停/退市强制 `SuperAdmin` + 必填 `reason`；禁止重复 `retire`；拦截默认/推广模板跌破 4 个有效上架槽位 | `TestPauseAndRetire_EnforceLiveSuperAdminReasonAndMinSlots` |
| F-04 | **已修复** | 移除 `?template_id=` / `recharge_template_id` / `X-Recharge-Template-ID` 与网关 CORS 放行头，保留首触归因 | `TestCORSAllowHeadersOmitsTemplateID`、`TestTemplateResolution_FirstTouchLockedAndIgnoresTemplateHeader`、`TC-FE-05 (CMS & Reader)` |
| F-05 | **部分修复（见 R-1）** | 模板保存已按目标 `environment` 校验已上架且双渠道就绪，前端也按环境过滤候选 SKU；**但未隔离默认模板与推广绑定对 `sandbox` 模板的选择** | `TestTemplateSave_RejectsSandboxOnlyOrUnreadyProducts`、`TC-FE-03` |
| F-06 | **已修复（含 R-3 并发细节）** | [2026111930.sql](../cms-backend/db/migrations/2026111930.sql) 改为 `ON DELETE RESTRICT`，后端查锁 `promotion_links` 返回 409，前端存在引用时禁用删除 | `TestDeleteTemplate_BlockedWhenReferencedByPromotionLink`、`TC-FE-03` |
| F-07 | **已修复（待配合 R-1 收敛）** | `SyncRechargeSlotsTx` 按 `product_id` 原地更新保留 `recharge_slots.id`；禁止取消唯一默认模板；前端锁定取消勾选 | `TestUpdateTemplate_PreservesSlotIDsAndDefaultInvariant`、`TestVIPCheckout_ContinuesAfterTemplateCosmeticEdit`、`TC-FE-03` |
| F-08 | **已修复** | `rechargeCatalogHelpers.ts` 补齐 `name` / `description` 并按 `display_alias \|\| name \|\| vip_name \|\| ''` 渲染 | `TC-FE-04` |
| F-09 | **已修复（含 R-2 重试瑕疵）** | 支持 Stripe 失效资源按 `reprovision` 重建；PayPal VIP 对 4xx 确定性失败保留重试能力 | `TestStripeArchiveRecoveryAndPayPalVIP4xxRetry` |
| F-10 | **已修复** | `copyProductReq` 使用指针字段保留 `src.Bonus`；`specific_novels` 取消勾选同步清理 `manual_novel_ids` 并规范 `_V` 后缀 | `TestCopyProduct_PreservesBonusUnlessExplicitlyZero`、`TC-FE-02` |
| F-11 | **已修复** | `PreparePurchase`、`resolveProductForCheckoutTx`、`insertCheckout` 强制要求 `slot_id > 0` 且命中用户当前生效模板；同步收敛 `vip_checkout_review_test.go` 与 `six_flows_integration_test.go` | `TestPreparePurchase_BlocksSlotZeroAndRequiresActiveTemplateSlot`（6 个子用例）、`TestSixH5PurchaseFlowsEndToEnd` |
| F-12 | **已修复** | 移除 CMS `/channels/vip-offers`、`/channels/:id/stripe-one-time`、`/channels/:id/stripe-vip`、`StripeOfferSettings.tsx` 及 `reconcilePayPalVIPOffers`；[2026111928.sql#L302](../cms-backend/db/migrations/2026111928.sql#L302) 已有 `IF EXISTS` 防护 | `TC-FE-05 (CMS) & F-12`、`internal/db` 全部迁移测试 |

---

### 6.2 复审新发现与遗留缺陷（待修复）

#### `R-1` · [P1 · 阻断合入] `recharge_templates.environment` 引入后，默认模板与推广绑定及 Reader 默认查询未按环境隔离

* **源码证据**：
  1. [2026111930.sql#L8-L10](../cms-backend/db/migrations/2026111930.sql#L8-L10) 为 `recharge_templates` 增加了 `environment TEXT NOT NULL DEFAULT 'live' CHECK (environment IN ('sandbox', 'live'))`。
  2. [repository.go#L277-L280](../cms-backend/internal/billing/repository.go#L277-L280) 的 `ClearDefaultTemplatesTx` 无条件将全表 `is_default = FALSE`；[service.go#L291-L404](../cms-backend/internal/billing/service.go#L291-L404)（`CreateRechargeTemplate` / `UpdateRechargeTemplate`）与 [service.go#L474-L502](../cms-backend/internal/billing/service.go#L474-L502)（`SetDefaultRechargeTemplate`）**未禁止将 `environment = 'sandbox'` 模板设为默认**，且允许把 `is_default = true` 或正被 `promotion_links` 引用的 `live` 模板编辑切换为 `sandbox`。
  3. [TemplatesTab.tsx#L847-L856](../cms-front/src/pages/TemplatesTab.tsx#L847-L856) 对 `sandbox` 模板同样显示“设为默认”按钮；[PromotionsTab.tsx#L598-L606](../cms-front/src/pages/PromotionsTab.tsx#L598-L606)、[PromotionLinkModal.tsx#L238-L246](../cms-front/src/components/novels/PromotionLinkModal.tsx#L238-L246) 与 [novel/repository.go#L462,L483](../cms-backend/internal/novel/repository.go#L462) 允许将 `sandbox` 模板绑定至推广链接。
  4. Reader 侧所有默认模板兜底 SQL 均仍为全局 `ORDER BY is_default DESC NULLS LAST, id LIMIT 1`（未过滤 `environment`）：
     * [product_catalog.go#L75-L78](../reader-backend/internal/payment/application/product_catalog.go#L75-L78)（下单槽位解析）
     * [checkout.go#L54-L57](../reader-backend/internal/payment/application/checkout.go#L54-L57) & [#L118](../reader-backend/internal/payment/application/checkout.go#L118)
     * [vip_checkout.go#L235-L237](../reader-backend/internal/payment/application/vip_checkout.go#L235-L237)（`validateVIPQuoteTx`）
     * [stripe_checkout_repository.go#L185-L186](../reader-backend/internal/payment/application/stripe_checkout_repository.go#L185-L186)
     * [stripe_vip_repository.go#L148-L149](../reader-backend/internal/payment/application/stripe_vip_repository.go#L148-L149)、[#L172-L173](../reader-backend/internal/payment/application/stripe_vip_repository.go#L172-L173)、[#L210-L212](../reader-backend/internal/payment/application/stripe_vip_repository.go#L210-L212)
     * [catalog_repository.go#L33-L65](../reader-backend/internal/payment/application/catalog_repository.go#L33-L65)、[#L180,L185](../reader-backend/internal/payment/application/catalog_repository.go#L180)（同时注意 [schema.sql#L648](../cms-backend/db/schema.sql#L648) 中 `whole_book_offer_available` 也有相同子查询）
  5. 另外 [products.go#L1907-L1909](../cms-backend/internal/billing/products.go#L1907-L1909) 在 `transitionProductStatus` 检查最小 4 槽位时未加 `AND t.environment = $2`。
* **触发危害**：一旦操作员新建或编辑一个 `sandbox` 测试模板并设为默认（或误绑到推广链接），`live` 线上 Reader H5 将命中使用 `sandbox` SKU 的模板；在双渠道就绪条件过滤后，线上 H5 收银台会变成空列表且无法发起任何购买。
* **完整修复方案**（推荐“全局唯一默认模板必须是 `live`”；若确需按环境各自维护默认模板，则同步调整唯一性约束与 Reader 查询按运行环境过滤）：
  1. **CMS 后端校验收口（[service.go](../cms-backend/internal/billing/service.go)、[novel/repository.go](../cms-backend/internal/novel/repository.go)）**：
     * `CreateRechargeTemplate`、`UpdateRechargeTemplate`、`SetDefaultRechargeTemplate` 中若目标模板 `environment != 'live'` 且 `isDefault == true`，拒绝返回 `409 Conflict`（仅 `live` 模板可作为系统默认模板）；
     * `UpdateRechargeTemplate` 若发现 `env == 'sandbox'` 且该模板当前 `wasDefault == true` 或 `EXISTS(SELECT 1 FROM promotion_links WHERE recharge_template_id = $1)`，返回 `409 Conflict` 禁止降级为 `sandbox`；
     * `CreatePromotionLink` 与 `UpdatePromotionLink` 将 `WHERE rt.id = $x` 收紧为 `WHERE rt.id = $x AND rt.environment = 'live'`；
     * `transitionProductStatus`（[products.go#L1908](../cms-backend/internal/billing/products.go#L1908)）增加 `AND t.environment = $2` 仅核对对应环境模板。
  2. **Reader 后端默认模板兜底过滤（防御性加固）**：
     * 上述 Reader 端及 `whole_book_offer_available` 中的默认模板兜底查询统一优先匹配 `environment = 'live'`（或当明确按环境区分默认模板时按 `currentPaymentLedgerEnvironment()` 过滤 `WHERE environment = $env`），避免任何异常写入 `sandbox` 默认行时波及 `live` 流量。
  3. **CMS 前端同步过滤**：
     * `TemplatesTab.tsx` 仅在 `t.environment === 'live'` 且 `!t.is_default` 时显示“设为默认”及允许勾选“设为默认”；`PromotionsTab.tsx` 与 `PromotionLinkModal.tsx` 仅展示 `t.environment === 'live'` 的模板供推广链接绑定。

#### `R-2` · [P2] Stripe `reprovision=true` 中途失败后同参重试被断言拦截

* **源码证据**：[products.go#L1566-L1616](../cms-backend/internal/billing/products.go#L1566-L1616) 在已有 `prodRef != "" && priceRef != ""` 时执行 `reprovisionStripeRefsTx` 先清空并持久化 `external_price_ref`（或两者），若随后的 Stripe `POST /v1/prices` 失败，`saveStripeBindingResultTx` 会将空 `external_price_ref` 写回数据库；紧接着若以同样 `{reprovision: true}` 参数重试，会落入第 1614 行 `else if req.Reprovision` 报错 `"reprovision requires an existing Stripe Product/Price reference"`。
* **完整修复方案**：若当前处于部分重建态（如 `prodRef != "" && priceRef == ""`，或 `prevValStatus == 'failed'` 且刚轮换过 `request_key`），当传入 `reprovision: true` 且缺失某一方引用时，允许直接沿用当前 `request_key` 与 `catalogRevision` 进入后续创建缺失 `Product`/`Price` 分支完成幂等自愈，而不应反手拒绝重试。

#### `R-3` · [P2] 并发绑定 `promotion_links` 时删除模板可能抛出 `500`

* **源码证据**：[service.go#L435-L471](../cms-backend/internal/billing/service.go#L435-L471)（`DeleteRechargeTemplate`）先查 `promotion_links ... FOR SHARE`，在检查结束至 `DELETE FROM recharge_templates` 之间若并发事务插入/更新 `promotion_links` 引用同一 `id`，PostgreSQL `ON DELETE RESTRICT` 会触发外键违约（`SQLSTATE 23503`），[handler.go#L451-L459](../cms-backend/internal/billing/handler.go#L451-L459) 未将其映射为 `ErrTemplateReferenced`（409），会返回 `500`。
* **完整修复方案**：在 `DeleteRechargeTemplate` 执行 `DELETE FROM recharge_templates` 报错时判断 `*pgconn.PgError` `Code == "23503"` 并包装返回 `ErrTemplateReferenced`（同时建议在 `FOR SHARE` 检查前先通过 `SELECT 1 FROM recharge_templates WHERE id = $1 FOR UPDATE` 锁定模板行）。

### 6.3 新增补测用例建议（对应 `R-1` ~ `R-3`）

| 用例 ID | 覆盖项 | 测试建议 | 预期断言 |
| --- | --- | --- | --- |
| TC-CMS-10 | R-1 | `TestTemplateEnvironmentIsolation_DefaultAndPromotionGuards` | 1. 创建/编辑/设为默认 `sandbox` 模板时 `is_default=true` 被拒绝返回 `409`；<br>2. 将 `is_default=true` 或被 `promotion_links` 引用的 `live` 模板改环境为 `sandbox` 被拒绝；<br>3. `CreatePromotionLink` / `UpdatePromotionLink` 绑定 `sandbox` 模板被拒绝 |
| TC-RDR-05 | R-1 | `TestReaderDefaultTemplateIgnoresSandboxDefaultRow` | 若 DB 人为注入 `sandbox` 且 `is_default=true` 的脏行，`live` 环境 `GetRechargeTemplates` 与 `PreparePurchase` 仍落到 `live` 默认模板而不会出现空收银台 |
| TC-CMS-11 | R-2 | `TestStripeReprovision_IdempotentRetryAfterPriceCreateFailure` | 模拟 `reprovision=true` 清空 `priceRef` 后新建 Price 504 失败，再次携带 `reprovision=true` 重试可直接补建新 `price_` 成功 |
| TC-CMS-12 | R-3 | `TestDeleteTemplate_ConcurrentFKViolationMaps409` | 并发新增推广引用或触发 `23503` 时，`DELETE /cms/recharge-templates/:id` 稳定返回 `409 Conflict` 而非 `500` |

### 6.4 复审实测流水记录（2026-11-23）

* **CMS 后端（连接 `STAR_NOVEL_TEST_DATABASE_URL`，无 SKIP）**：
  * `star-novel-cms/internal/billing`：`PASS`（`54.793s`，含 9 个新增回归测试与既有 3 个集成测试）
  * `star-novel-cms/internal/novel`：`PASS`（`13.829s`）
  * `star-novel-cms/internal/db`：`PASS`（`47.436s`）
* **Reader 后端（连接 `STAR_NOVEL_TEST_DATABASE_URL`，无 SKIP）**：
  * `reader-backend/cmd/server`：`PASS`（`0.799s`）
  * `reader-backend/internal/payment/application`：`PASS`（`171.170s`，含新增 F-04/F-07/F-11/F-02 回归及 `TestSixH5PurchaseFlowsEndToEnd` 等全部用例）
  * `reader-backend/internal/payment`、`aftercare`、`directory`、`ledger`、`paypal`、`paypal/credentialstore`、`settlement`、`stripe`、`internal/novel`、`internal/auth`：全部 `PASS`
* **CMS 前端**：
  * `./node_modules/.bin/tsc -b --noEmit`：`PASS`（0 error）
  * `./node_modules/.bin/oxlint`：`PASS`（73 files，0 warning / 0 error）
  * `npm run test:catalog-regression`：**4/4 PASS**（`TC-FE-01`、`TC-FE-02`、`TC-FE-03`、`TC-FE-05 (CMS) & F-12`）
* **Reader 前端**：
  * `./node_modules/.bin/tsc -b --noEmit`：`PASS`（0 error）
  * `./node_modules/.bin/oxlint`：`PASS`（35 files，0 warning / 0 error）
  * `npm run test:template-regression`：**2/2 PASS**（`TC-FE-04`、`TC-FE-05 (Reader)`）
  * `npm run test:shelf-batches`（既有未改动文件）：**1/1 FAIL**（`ERR_UNSUPPORTED_RESOLVE_REQUEST: Failed to resolve module specifier "./readerStorage" from "data:text/javascript;base64,..."`，属既有脚本加载方式问题，与本次修复无关）
