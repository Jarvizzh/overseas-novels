# StarNovel Stripe 接入最终开发与验收方案

2026-11-27 · 代码基线 `f171ce4` · 适用范围为 Reader、CMS、支付任务及共享数据库迁移

## 1. 实施范围与当前证据

保留 Stripe Hosted Checkout。商品标价、业务账本和商户结算统一使用 USD。买家可在通过验收的市场与支付方式下用本地币支付。MoR 商品发布按实际商品资格、税码和市场税务责任核验。**废弃预填 `tax_liability_account`，Stripe 实际返回的账户继续作为交易证据。**

2026-11-26 的线上只读快照仅观察到 Direct sandbox 商品对外可售及三次进入收银台的尝试，应用库当时无 Stripe 收据、Webhook 或业务订单。此前独立 sandbox 样本已验证一笔 USD 扣款和退款的余额流水。StarNovel 端到端实单、MoR 及银行到账仍待验收。本轮整合未重新访问线上或 Stripe。上一轮 Stripe 适配器测试和七项前端套餐选择测试通过，六流程测试因缺少一次性 PostgreSQL 跳过。 [支付域模拟复验](payment-domain-flow-review-2026-11-23.md)

| 已有入口 | 当前代码 |
| --- | --- |
| CMS 商户与商品目录 | 使用 `GET /v1/account` 校验账户，通过 Stripe Product 和 Price API 建品及核价。 [商品目录](../cms-backend/internal/billing/stripe_catalog.go#L55) |
| 单次及 VIP 收款 | Reader 使用 `stripe-go/v87 v87.2.0`，API 版本 `2026-11-18.endive`。单次按 Checkout、PaymentIntent 和 Charge 核验，VIP 每期按 Subscription、Invoice、InvoicePayment 和实收 Charge 核验。 [SDK](../reader-backend/go.mod#L15) · [单次核验](../reader-backend/internal/payment/stripe/single_checkout.go#L95) · [VIP 核验](../reader-backend/internal/payment/stripe/vip_invoice.go#L50) |
| 通知及资金 | 已有按商户验签、Inbox、Events API 补查、退款命令、风险案件、Payout 与报表导入。Radar 批准和 Report Run 创建已有适配器，业务调度待接入。 [事件入口](../reader-backend/internal/payment/application/webhook_service.go#L67) · [结算命令](../reader-backend/internal/payment/settlement/commands.go#L34) · [报表适配器](../reader-backend/internal/payment/stripe/finance.go#L127) |

## 2. 开发顺序

| 批次 | 开发交付 | 进入下一步的条件 |
| --- | --- | --- |
| 1 · 样本与改造底座 | 并行推进 P0-6 样本采集、P0-4 Webhook、P0-5 凭据。P0-1 可先实现商品税码映射并清理旧兜底，P0-2 可先完成固定账户字段退出及迁移。 | 拿到目标 Stripe 账户的资源响应样本。Webhook 和凭据各自具备模拟验收结果。 |
| 2 · 商品与履约 | P0-1 按获准商品配置税码。P0-2、P0-3 根据样本完成税务和 USD 结算准入，接入单次、VIP 首扣和续费。 | P0 的代码和对应 Sandbox 实单验收通过，批准范围可追溯到具体账户、商品、市场及支付方式。 |
| 3 · 售后与订阅管理 | 先实现 P1-2 共用退款准入，再接 P1-1 自动动作。P1-3 Radar 审批和 P1-4 H5 订阅管理可并行。P1-5 与 P2-4 的 409 回归纳入相关发版。 | 达到第 7 节对应模式的 Live 开放条件。 |
| 4 · 后续优化 | P2-1 Customer 和转化优化，P2-2 报表调度，P2-3 回补与举证，P2-4 争议原币金额。 | 各项独立验收后按账户启用。 |

各任务的开发、模拟回归、Stripe Sandbox 和 Live 分别记录结果。缺渠道证据的组合保持关闭，同批中已经验收的组合可以继续。发布顺序以实际依赖为准，其中 P1-4 应在首批 Live VIP 对读者开放前完成。

## 3. P0 · 商品、收款与平台能力

### P0-1 · MoR 商品税码与发布

| 商品 | 首发处理 |
| --- | --- |
| VIP `vip` | 已确认商品描述为订阅期限内站内在线阅读，默认使用 `txcd_10302003`。具体账户和市场仍需验收。 |
| 整本 `whole_book` | 一次付费后获得永久站内阅读权限。MoR 税码使用 `txcd_10000000`。 |
| 金币 `single` | 充值到账户余额后兑换章节。MoR 税码使用 `txcd_10000000`。发布前确认商品准入和计税时点。 |

1. CMS 后端在读取商品类型后按上表解析 MoR 税码，移除 `txcd_10103001` SaaS 兜底。前端使用同一映射回显税码。发布时继续核验商品准入、市场及对应证据。
2. 复用 Product、Price 校验与远端更新。现有 Product 若已被发布绑定或在续费协议引用，先核对 Stripe 对今后账单的影响。在影响未确认时沿用原引用，对新交易采用已验证的绑定版本。已有订单及订阅保留其原始条款。
3. 验收覆盖三类商品的税码自动同步。核对 Stripe Product 上整本及金币均为 `txcd_10000000`，VIP 为 `txcd_10302003`，并核对 `tax_behavior`、价格和周期。回归旧绑定改动、既有协议续费，以及商品或市场准入证据不足时的发布拦截。

[CMS 绑定校验](../cms-backend/internal/billing/products.go#L1275) · [Stripe 目录同步](../cms-backend/internal/billing/products.go#L1666) · [Stripe 税码总表](https://docs.stripe.com/tax/tax-codes) · [MoR 商品资格](https://docs.stripe.com/payments/managed-payments/eligibility#product-eligibility)

### P0-2 · 取消固定税务账户依赖

1. **CMS**。从商品绑定表单、请求及写入路径移除预填 `tax_liability_account`。检查旧单次及 VIP 报价处理器，现行统一商品发布沿用合格税码、允许市场、`release_evidence` 和 `market_policy`。首发通过经批准的市场范围和明确的交易证据规则放行。现有 `market_policy` 为文本，用于记录批准依据。
2. **Reader 结账**。移除 `publishedSalesMode` 的非空检查、VIP 首次发送前的固定账户比较，并从新单次 Checkout 和 VIP 意向快照中停写预期账户。保留原商户与环境、商品和价格、销售模式及市场验证。继续按原意向查证已经发出的请求。
3. **Reader 履约**。用获准的市场规则和 Stripe 样本中的适用字段替换 `TaxVerified` 的等值判定。存储实际账单地区、客户税务状态、税务计算结果及 Stripe 提供的责任类型和账户。MoR 标志、税务责任和税前本金各自核验。结论见下表。
4. **迁移及补查**。新增迁移解除现行绑定和仍需兼容的旧表约束。历史字段、审批及订单快照保留，新交易停写预期账户。按原商户、环境和对象重新查证仅因旧规则待复核的订单。使用现有补查、复核及幂等机制，禁止用批量更新状态触发发货。

| 交易证据 | 结果 |
| --- | --- |
| 已获准市场，销售模式和适用税务证据均通过 | 进入后续资金与权益校验。账户为空或与历史值不同单独出现时不构成复核原因。 |
| 当前市场与已核实快照不一致、市场越界，或税务责任缺证、冲突 | 实收保留，订单或账期进入复核，审批绑定该次证据。 |
| 结果缺失且 Stripe 对该场景允许延后提供 | 按原交易补查。达到补查上限仍无法确认则进入复核。 |

验收覆盖无固定 ID 的商品绑定与 Checkout、单次付款和 VIP 首扣及续费。分别测试返回 ID、空 ID、与历史值不同、市场变化、应税证据冲突和旧单重放。断言已付订单或已生效账期不重复履约，原税务证据及审批仍可查。

[CMS 表单](../cms-front/src/pages/ProductsPage.tsx#L1434) · [销售模式](../reader-backend/internal/payment/stripe/single_checkout.go#L322) · [单次快照](../reader-backend/internal/payment/application/stripe_checkout_repository.go#L117) · [VIP 绑定](../reader-backend/internal/payment/application/stripe_vip_repository.go#L181) · [市场复核](../reader-backend/internal/payment/application/stripe_market_review.go#L30) · [现行约束](../cms-backend/db/migrations/2026111928.sql#L215) · [Stripe 税务覆盖](https://docs.stripe.com/payments/managed-payments/tax-compliance)

### P0-3 · 本地币付款及 USD 结算

**开放范围**。Direct 使用显式 Session 参数控制 Adaptive Pricing，未验收范围继续显式关闭。MoR 由 Stripe 控制 Adaptive Pricing，通过现有渠道及商品发布控制新支付入口。确认 Hosted Checkout 在收款前能约束待开放范围。若账单地区或支付方式只能在收款后核验，须覆盖该入口实际可达的组合，证据不足时保持该入口关闭。在途 Checkout 与已有订阅按原条款继续查证。 [市场与税务设计](payment-domain-architecture-and-implementation.md#52-stripe-mor-收款资格与税务责任) · [Stripe Adaptive Pricing](https://docs.stripe.com/payments/currencies/localize-prices/adaptive-pricing?payment-ui=stripe-hosted) · [MoR Checkout 参数](https://docs.stripe.com/payments/managed-payments/update-checkout#remove-unsupported-parameters)

| 金额 | 证据与用途 |
| --- | --- |
| 买家实际支付 | 记录较新 API 提供的 `presentment_amount` 和 `presentment_currency`，以对应币种最小单位存储。单次核对 Checkout 和 PaymentIntent，续费核对关联的各 PaymentIntent。缺证时保留未知。 |
| 商品金额、含税收款和税前本金 | 用 USD Price 及单次商品项、Charge，或 VIP Invoice、InvoicePayment 和对应 Charge 核实。保留独立字段或账本分量，按已核验的税前本金计算履约资金。 |
| 结算流水 | 按原 Charge 关联的 BalanceTransaction 核实 USD 币种、金额、汇率和费用。总额、手续费、代扣税和净额按实单验证的对象关系入账。 |
| 退款和转化 | 退款按原付费收据和渠道退款金额累计，权益按既有账本调整。转化沿用已核实的 USD 业务金额及原事件去重，买家本地币用于账务证据和展示。 |

1. 单次在 `ReadSingleCheckout`、`syncStripeAttempt` 和 `applyStripeProof` 接入已核实的结算证据。VIP 在 `ReadStripeVIPInvoice`、`syncStripeVIPInvoice` 和 `settleStripeVIPInvoice` 接入逐笔分配及其余额流水。首批按真实样本确认 Charge 与余额流水金额关系，覆盖 inclusive、exclusive 以及多笔有效付款分配。已有币种守卫只在该范围验收后替换。
2. **首发默认建议**。新开放的本地币交易在余额流水缺失或读取暂时失败时，记录已确认实收并暂缓新增权益。单次复用原 attempt 的 `next_create_at` 补查，VIP 针对原 Invoice 补查，保留原账期及原收据。两条路径的 claim 条件均须覆盖“已付待结算”。已确认币种或金额不符、超过补查窗口的订单进入复核。银行 Payout 是独立的 Live 对账验收。
3. 同一个 attempt 发送参数应在首次写入后保持稳定，避免在重试期间因开关改变发送不同幂等请求。前端沿用原单待确认状态和查单入口。

**验收**。USD 对照、GBP、CAD，以及支持市场下的 JPY 零小数币种测试样本。分别覆盖直接收款、MoR、单次、首扣及续费的适用组合。模拟流水未到、查询超时、币种不符、金额不符、补查成功、重复通知、存量订阅续费和关闭新销售入口。核对实收、账本、权益、退款和 CAPI 的币种、金额及幂等。

[Checkout 参数](../reader-backend/internal/payment/stripe/checkout.go#L69) · [单次余额流水](../reader-backend/internal/payment/application/stripe_checkout_service.go#L171) · [单次补查](../reader-backend/internal/payment/application/stripe_checkout_repository.go#L136) · [VIP 余额流水](../reader-backend/internal/payment/application/stripe_vip_service.go#L296) · [Stripe 货币单位](https://docs.stripe.com/currencies#minor-units-in-api-amounts)

### P0-4 · Webhook 版本兼容、回补和切换

1. 保留签名验证、原始请求体、时间容差、端点归属及账户、环境校验。处理 SDK 和应用层两处版本检查。通过验签的旧版事件按通用信封持久化到 Inbox，携带事件 ID、事件类型、对象 ID、API version、账户、环境和原始快照。队列写入成功后确认接收。相同账户同一事件去重。
2. 让实时通知和 Events API 回补共用分派规则。按事件类型回查当前资源，再验证其身份、关联和状态。下表的资源关联通过后才进入现有业务流程。回查失败保持可重试，账户或资源冲突留可诊断记录。Webhook 重投和资源补查都沿用业务幂等。
3. CMS 校验完整回调 URL、启用状态、环境、API version 及必要事件集合。密钥缺权限时将该项标为未验证。端点 API version 的变化通过新端点和签名凭据切换，先实测新旧签名和并行投递去重，再停用旧端点。回退期间保留原端点和补查能力。

| 事件 | 回查与分派 |
| --- | --- |
| `checkout.session.*` | 读 Checkout Session，核对 mode、metadata 及 `client_reference_id`，定位原单次尝试或 VIP 意向。 |
| `invoice_payment.paid`、`invoice.*`、`customer.subscription.*` | 由 InvoicePayment 获取 Invoice ID，或按 Invoice、Subscription 对象 ID 定位原订阅和账期。 |
| `refund.*`、`charge.dispute.*`、`review.*`、EFW | 读原对象及对应付款，按账户和环境更新退款、争议及风险案件。 |
| `charge.succeeded`、`charge.failed`、`payment_intent.payment_failed` | 保留现有风控录入，旧版本通过 Charge 或 PaymentIntent 回查后记录。 |
| `payout.*`、报表成功事件 | 回查 Payout 或 Report Run。核实状态、报告类型与账户后写入现有结算任务。 |

**验收**。匹配版本、同家族不同版本、历史版本、错账户和环境、签名失效、资源短暂不可用及重复投递。至少证明 Checkout、InvoicePayment 和报表各自从入队走到业务处理完成，实时和回补路径都可执行。

[验签入口](../reader-backend/internal/payment/stripe/webhook.go#L38) · [分派代码](../reader-backend/internal/payment/stripe/single_checkout.go#L220) · [Events 回补](../reader-backend/internal/payment/application/event_recovery.go#L156) · [Stripe Webhook 版本](https://docs.stripe.com/webhooks#api-versioning) · [端点对象](https://docs.stripe.com/api/webhook_endpoints/object)

### P0-5 · 动态凭据与权限

1. 复用支付凭据目录，按原 Stripe 账户、环境及用途解析。接入自动 Payout、手动 `stripe_payout`／`stripe_report`、Files 下载和 `payment-ops` 售后回补。保留旧静态渠道显式配置的环境变量路径。
2. 按用途验证账户归属及接口权限。Payout、Balance Transactions、Report Run、Files、Events 和售后读取分别验收。额外用途权限只有在对应流程需要时配置。动态账户缺权、凭据不可解密或商户不匹配时，任务记录用途和原因，同一账户修复后可重试。
3. 历史命令继续使用原账户和环境。验证密钥轮换后用该账户有效凭据查回原资源，在途写操作继续使用原幂等键。停售仍可处理历史退款、Webhook 和对账。

**验收**。仅依赖获准的 CMS 加密凭据完成三类入口。覆盖账户校验通过但 Files 缺权、密钥轮换、停用商户历史任务、错账户和错误环境，并检查日志和 CMS 结果隐藏密钥。

[动态凭据目录](../reader-backend/internal/payment/directory/directory.go#L192) · [自动对账](../reader-backend/internal/payment/settlement/automatic.go#L108) · [手动结算](../reader-backend/internal/payment/settlement/commands.go#L126) · [售后 CLI](../reader-backend/cmd/payment-ops/aftercare.go#L79) · [Files 下载](../reader-backend/internal/payment/stripe/report_csv.go#L59)

### P0-6 · Stripe Sandbox 样本与外部依赖

| 联调项 | 要取得的证据 | 影响范围 |
| --- | --- | --- |
| 商品和市场资格 | 确认账户启用情况及 US、CA、GB 等拟售市场的可达性、账单地区控制和税务责任。以表中税码验证整本和金币的 MoR 商品准入，确认金币在充值或兑换时的计税口径。 | 未确认的商品或市场保持 MoR 关闭。 |
| 税务字段 | Direct 与 MoR 样本保存实际账单地区、客户税务状态、`automatic_tax.status`、`liability.type/account` 及税额。包含可正常零税和应复核的样本。 | 决定 P0-2 的履约证据规则与批准范围。 |
| 本地币及结算 | 使用有效 Sandbox 价格，核对买家实付、USD 含税收款、税前本金、余额流水、退款及费用。覆盖单次、VIP 首扣和续费。 | 决定 P0-3 开放范围及多币种关联规则。 |
| Webhook 和售后 | 实测收银台、重复回调、扣费失败及恢复、期末取消、部分及累计全额退款、Dashboard 外部退款、适用的 Radar 和 Dispute。 | 提交真实事件 ID、处理结果和权益核对。 |
| 报表和结算 | 确认报表类型、可用区间、税费列和权限。Sandbox 不支持的报告用样本验解析。Live 完成首笔 USD Payout 与银行到账核对。 | 作为对账及放量证据。 |

按实际账户复查历史白名单中的 `UK` 和缺失的 `CA`，使用标准 `GB` 和已批准市场。定向本地币可使用 Stripe 的 `customer_email` `+location_XX` 测试方式。续费使用账户支持的 Test Clock 或等效测试。工程记录脱敏对象 ID、事件版本、金额字段、预期结果和实际结果。Stripe 技术侧确认账户能力和字段语义，财务确认税务责任及 USD 入账口径。每个开放组合以同一份联调记录关联上述证据。

[MoR 测试](https://docs.stripe.com/payments/managed-payments/update-checkout#testing) · [本地币测试](https://docs.stripe.com/payments/currencies/localize-prices/adaptive-pricing?payment-ui=stripe-hosted#testing) · [扣款限额](https://docs.stripe.com/currencies#minimum-and-maximum-charge-amounts) · [Report Runs](https://docs.stripe.com/api/reporting/report_run)

## 4. P1 · 售后与订阅管理

### P1-2 · Stripe 退款准入

先实现这一项，再为 P1-1 接入自动退款。把 `charge.Disputed` 用作查证线索。执行时按原账户、环境和 Charge 核对实收、本地预留、远端累计退款、争议状态及可退余额。现有退款命令负责预留、发起、结果确认及恢复。调用结果未知时保存原幂等键并查回原退款，重放继续处理同一命令。

| 查证结果 | 处理 |
| --- | --- |
| 可退的 Inquiry | 以 `is_charge_refundable` 和剩余额度决定能否退款。止损需全退的场景按剩余全额处理。 |
| 正式争议尚未结束 | 转争议流程，保留审核记录。 |
| 胜诉、撤案或问询关闭 | 以当前远端退款资格及剩余额度为准。 |
| 远端未返回、并发结果不明或证据冲突 | 保留预留或进入待确认，补查原 Charge 和命令。 |

**验收**。覆盖人工退款和 EFW 并发、争议状态变化、部分已退、允许及禁止退款的 Inquiry、胜诉后暂不可退、渠道成功但响应丢失和重复 Webhook。断言无超额退款或重复收权。

[退款守卫](../reader-backend/internal/payment/stripe/payment_refund.go#L33) · [Stripe 退款任务](../reader-backend/internal/payment/application/stripe_refunds.go#L237) · [Stripe 争议流程](https://docs.stripe.com/disputes/how-disputes-work) · [Dispute 字段](https://docs.stripe.com/api/disputes/object)

### P1-1 · EFW、Dispute 分派与自动止损

1. EFW 按原账户的财务责任人分派并提醒。由最新资源查证识别 Inquiry、正式争议和已结案。对 EFW 和争议乱序的事件，按 Charge 与策略复用既有命令。
2. 首发建议账户级自动退款、自动停订、争议期冻结初始关闭，分别验收后启用。自动退款使用 P1-2。停订只作用于原付款关联且符合策略的协议，走期末取消并保留已付期。MoR 的允许操作以 Stripe 售后职责和该账户权限为准。
3. 争议期冻结作为独立策略。确认原付款与可冻结权益后，按实际影响范围执行，处理胜诉解冻和判负核销。复用既有本金损失与金币差额账务，避免退款和争议对同一笔资金重复回收。

**验收**。覆盖 `actionable` 预警但不可退、EFW 晚于争议、同一用户多个协议、渠道拒绝停订、退款已回收权益后判负，以及停止续费后已付期可正常阅读。

[风险证据](../reader-backend/internal/payment/stripe/aftercare.go#L63) · [本金损失](../reader-backend/internal/payment/aftercare/loss.go#L38) · [Stripe MoR 售后](https://docs.stripe.com/payments/managed-payments/how-it-works#dispute-support)

### P1-3 · Radar 审批同步与履约补查

1. CMS 对 Stripe 风险案件批准时，在审批事务内保存待提交命令及审计信息。命令按原账户、环境和 Review 唯一标识去重。使用现有 `ApproveReview` 适配器提交，超时后先回查原 Review。
2. Worker、Dashboard 操作和 `review.closed` 进入同一收敛逻辑。仅在 Stripe 确认 Review 已关闭且 `closed_reason=approved` 后更新本地 `work_state`，唤醒对应单次 attempt 或 VIP Invoice。其他关闭原因保留适用的退款或争议待办。
3. 补查仍核验支付、争议、退款及账期。CMS 显示审批待确认、失败或已确认，已有 PayPal 审批流程继续走对应渠道。

**验收**。覆盖远端先成功后超时、明确拒绝、本地与 Dashboard 同时批准、`review.closed` 重投及审批后已退款。检查渠道批准前权益始终暂缓，批准后有效订单只履约一次。

[CMS 审批](../cms-backend/internal/billing/aftercare_service.go#L128) · [Stripe 审批适配器](../reader-backend/internal/payment/stripe/risk.go#L48) · [履约暂缓](../reader-backend/internal/payment/aftercare/fulfillment.go#L16) · [Stripe Review 对象](https://docs.stripe.com/api/radar/reviews/object)

### P1-4 · Reader H5 订阅自助管理

在个人中心添加「我的会员／订阅管理」入口，复用 `GET /wallet/subscription/status` 读取当前用户订阅。显示渠道、套餐、当前状态、已付截止和可用时的下次扣款日。通过现有取消接口关闭自动续费，区分待渠道确认和已停止，并显示原已付截止。Stripe Direct 使用现有 Billing Portal 换卡入口，MoR 使用 Link 管理入口。订阅归属和可执行操作由服务端判断。 [H5 个人中心](../reader-front/src/pages/Profile.tsx) · [现有 API 路由](../reader-backend/internal/payment/application/handler.go#L41) · [Stripe MoR 订单管理](https://docs.stripe.com/payments/managed-payments/how-it-works#manage-orders)

**验收**。覆盖 PayPal 和 Stripe，分别验证有效订阅、待取消、已取消仍有已付期、续费失败和无订阅。重复点击保持幂等，其他用户的协议不能管理。真实验收应验证 Direct Portal 和 MoR Link 各自入口。

### P1-5 · 旧 VIP 首款防重回归（#18）

当前基线已有 PayPal 和 Stripe 的首期账期重叠检查及升级前存量协议回归用例。验收 PayPal → Stripe、PayPal → Stripe → PayPal 两种到账顺序、存量缺替换标记、Webhook 重放和对账补录。两笔实收均能追踪，同一账期只履约一次。仅有一笔有效迟到首款时按原账期履约。 [PayPal 首期检查](../reader-backend/internal/payment/application/subscription_repository.go#L219) · [Stripe 首期检查](../reader-backend/internal/payment/application/stripe_vip_settlement.go#L257) · [回归用例](../reader-backend/internal/payment/application/vip_checkout_review_test.go#L920)

## 5. P2 · 转化、报表与争议证据

| 工作项 | 实现及验收 |
| --- | --- |
| P2-1 · Customer 复用及地址精简 | 按用户、Stripe 账户和环境保存已核实 Customer。关联幂等，避免并发下单重复建客户。保存和复用付款方式走 Stripe 支持的授权流程，按 Direct 与 MoR 各自范围试验。验证实际账单地区和税务证据完整后，再缩减地址字段。仅在 Stripe 确认外跳 Hosted Checkout 支持有用的额外 Radar 信号后试验 Stripe.js，在小范围对比支付转化、错误率和页面性能。 [Checkout 创建](../reader-backend/internal/payment/stripe/checkout.go#L55) · [Stripe 保存付款方式](https://docs.stripe.com/payments/existing-customers?platform=web&ui=stripe-hosted) |
| P2-2 · Report Run 定时生成 | 使用实际支持的 `balance_change_from_activity.itemized.*` 和 `payout_reconciliation.itemized.*` 版本。按账户、环境、报告类型和报告参数持久化调度键及 Run ID，覆盖重试和重启。支持的 MoR 报告显式请求 `withheld_tax`、`fee_net_of_withheld_tax`。完成后进入现有导入队列，失败或缺预期列记录原因。验证重复投递只导入一次、调度缺权、手动导入兼容和税费两列入账。 [Report Run 请求](../reader-backend/internal/payment/stripe/finance.go#L127) · [结算命令](../reader-backend/internal/payment/settlement/commands.go#L34) · [Stripe 报表列](https://docs.stripe.com/payments/managed-payments/how-it-works#reports) |
| P2-3 · 回补、重试及举证 | 在 P0-4 共用事件分派基础上，对 Events API 按白名单分组，每组最多 20 种，分别推进分页与游标。纯读和具有稳定幂等键的写请求可配置有限重试，保留原有查回上下文。增加有权限的 CMS 数字交付证据导出，按案件关联订单、付款、权益使用及可核实的历史记录。Visa CE 3.0 的资格与字段单独按 Stripe 返回确认。验收事件漏页、并发限流、超时重试与导出权限。 [事件列表](../reader-backend/internal/payment/stripe/event.go#L40) · [SDK 重试](../reader-backend/internal/payment/stripe/client.go#L92) · [Stripe 争议举证字段](https://docs.stripe.com/api/disputes/update) |
| P2-4 · 争议原币金额及 409 回归 | 从 Stripe Dispute 保存币种和最小单位金额，读取 API 和 CMS 统一展示，历史未知值保留未知。损失与权益计算使用 USD 结算证据。测试 GBP、JPY、USD 及历史记录。H5 已处理 `checkout_pending`，保留无回跳参数进入、返回 409、查询原 intent、显示到账或继续等待的浏览器回归。 [争议读取](../reader-backend/internal/payment/stripe/aftercare.go#L135) · [案件展示](../cms-front/src/pages/PaymentCases.tsx#L321) · [H5 409](../reader-front/src/pages/Recharge.tsx#L411) |

## 6. 本地开发验收与交付

使用一次性 PostgreSQL 设置 `STAR_NOVEL_TEST_DATABASE_URL`，分别验证空库与升级库的迁移，再运行以下命令。资金相关用例断言订单、收据、权益、退款预留和转化出站记录，前端关键交互用浏览器模拟渠道回包。

```bash
(cd cms-backend && go test -p 2 -count=1 ./internal/db ./internal/billing)
(cd reader-backend && go test -p 2 -count=1 ./internal/payment/... ./internal/wallet ./internal/novel)
(cd cms-front && npm run test:catalog-regression && npm run build)
(cd reader-front && node --test scripts/recharge-selection.test.mjs && npm run build)
```

| 验收对象 | 交付证据 |
| --- | --- |
| 商品发布及快照 | CMS 自动建品、发布、停售，Reader 新单和在途原单，以及旧订阅条款兼容。 |
| 资金及权益 | Direct 和获准 MoR 的金币、整本、VIP 适用组合。覆盖实收待结算、重复及乱序事件、退款与履约并发、账期恢复。验证两笔实收和一次权益的 #18 回归。 |
| 交易归属与凭据 | 两个商户或两个环境的错配用例、动态密钥轮换、缺权、停售后历史任务。 |
| Webhook 与补查 | 验签和入库、逐类资源查证、旧版本与去重、处理失败后恢复，附状态或查询结果。 |
| 发布包 | 受影响服务及前端的构建结果、变更文件、迁移与版本、测试命令及结果、Stripe 样本、开关和回退记录。 |

迁移使用新版本并更新 CMS 内嵌迁移、CMS／Reader／Conversion 的版本门禁和测试夹具。首版只解除固定税务账户约束和停止新写入，保留旧列用于兼容与审计。每批以发布提交的 schema 为准，预演 CMS、Reader 和 Conversion 的同版本启动。 [CMS 版本](../cms-backend/internal/db/postgres.go#L100) · [Reader 版本](../reader-backend/internal/db/postgres.go#L108) · [Conversion 版本](../conversion-backend/internal/delivery/store.go#L86) · [部署手册](../DEPLOYMENT_SOP.md)

## 7. 上线、观察与回退

1. **迁移与服务**。记录发布版本、备份及未处理事件。迁移后同步启动匹配 schema 的三个服务，按 SOP 验证健康检查、现有购买入口、支付 worker 和转化链。新销售范围先维持关闭。配置各账户的端点、签名凭据及批准商品，验真实签名投递。
2. **受控 Live 验证**。确认 P0-4 和 P0-5 覆盖首批账户的实际入口后，按已通过 Sandbox 验收的范围开首笔。Direct USD 可先走已确认路径。本地币须通过 P0-3，MoR 须完成该商品与市场的 P0-1、P0-2。开放 Live VIP 前完成取消与续费回归，读者可通过订阅管理关闭自动续费。启用 Radar 履约暂缓的账户在放量前验收 P1-3。新自动退款和停订按已批准的范围独立启用。
3. **观察后放量**。对照 Stripe 和本地记录核对首笔扣款、事件、履约、退款和转化。按账户、模式、市场观察已付待履约、余额流水补查积压、Webhook 待处理和失败、上游缺权、退款待确认及对账差异。首笔 USD Payout 与银行到账一致后扩大对应资金路径的流量。
4. **中止及回退**。发现错币、错误履约、重复权益、资金对账差异或持续积压时，关闭受影响的新单或新订阅路由。保留原账户范围的 Webhook、续费核验、退款和补查。兼容同 schema 时可回滚应用。跨 schema 以兼容当前数据的前滚为先，按 SOP 在受控窗口保留并核对期间发生的付款、退款、续费及转化记录。 [支付账户与回调](../DEPLOYMENT_SOP.md#41-支付账户回调与商品) · [验收与发版记录](../DEPLOYMENT_SOP.md#6-验收与发版记录) · [回退与密钥轮换](../DEPLOYMENT_SOP.md#7-故障隔离回退与密钥轮换)

## 8. 首次 Review 项目追踪

| 发现 | 最终实施落点 | 当前证据 |
| --- | --- | --- |
| 1 · USD 结算 | P0-3 | 单次和 VIP 余额流水现为可选证据，待改造。 |
| 2 · MoR 税码与责任 | P0-1、P0-2 | 固定账户校验已决定废弃。整本和金币税码已统一为 `txcd_10000000`，VIP 使用 `txcd_10302003`。代码待改，商品准入及市场税务责任待验收。 |
| 3 · Webhook 跨版本 | P0-4 | SDK 和应用仍有版本检查，旧事件分派待改。 |
| 4 · 用途凭据 | P0-5 | 对账和 CLI 仍读环境变量，动态解析和权限待改。 |
| 5 · 实单验收 | P0-6、第 7 节 | Sandbox 端到端和 Live 待完成。 |
| 6 · 争议和自动退款 | P1-2 → P1-1 | 当前退款守卫仍按 `charge.Disputed` 拦截。 |
| 7 · Radar 审批 | P1-3 | 本地批准与 Stripe 获批仍需同步。 |
| 8 · VIP 防重与 409 查单 | P1-5、P2-4 | 当前代码已有处理，纳入回归。 |
| 9 · 税费报表与争议原币 | P2-2、P2-4 | 报表自动化、列配置和原币种展示待改。 |
