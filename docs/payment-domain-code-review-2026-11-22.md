# StarNovel 支付域代码 Review

日期 2026-11-22。最新复审基于 `codex/payment-domain` 的 `ac1c6e1` 与当前工作区，数据库版本为 `2026111927`。按 USD 收款、退款、结算与银行对账范围，核对支付架构第 3 至 7 节和全项目开发规范。 [支付域架构与开发实施方案](payment-domain-architecture-and-implementation.md) · [全项目开发规范](../development_guidelines.md)

**当前结论。** 22 个工作包均已有实现入口。19 项未发现新增功能缺口，3 项 (`PAY-01`、`PAY-02`、`FE-02`) 涉及同一处已复现的历史订单恢复缺陷 R15。当前代码仍有必做事项，实际渠道及生产切换验收继续单独跟踪。 [全流程复评](#15-usd-范围全流程复评)

**首轮评审基线（Schema `2026111926`）。** 22 个工作包均有实现，6 项核心代码已到位，16 项有缺口。首版 USD 范围内发现 9 项缺陷，其中 4 项在隔离库复现。以下保留首轮清单，供复审对照。R02 属于非 USD 范围。

## 1. 逐模块完成度

本节及第 1.1 节保留首轮评审记录。当前 22 项的完成度见第 15 节。

「已实现」表示本次核对的核心代码及入口已具备。「部分完成」表示已有主路径，仍需补齐子功能或修复缺陷。第 1.1 节逐项列出 16 个工作包的剩余开发内容，第 1.2 节列出待联调和验收的事项。

| 模块 | 工作包 | 状态 | 实现证据与待完成事项 |
| --- | --- | --- | --- |
| 共用底座 | `PAY-01` | 部分完成 | 账户化 Inbox、主动补拉、失败事件重试和版本门禁已落地。PayPal 部分事件消费后丢弃业务结果，见 R09。存量副本协调切换仍待演练。 [事件回补](../reader-backend/internal/payment/application/event_recovery.go) · [迁移入口](../cms-backend/internal/db/postgres.go) |
| 收款与支付订单 | `PAY-02` | 部分完成 | 购买意向、业务占位、支付尝试、同事务履约及实际收款关联已实现。切换时的查证失败可卡住原 PayPal 尝试，见 R05。 [购买意向](../reader-backend/internal/payment/application/checkout_intent.go) · [切换](../reader-backend/internal/payment/application/checkout_switch.go) |
| 订阅与计费 | `PAY-03` | 部分完成 | PayPal 套餐版本、协议补偿、已付周期、取消及首付转化有入口。VIP 互斥、管理列表及权益对环境的约束缺失，扣费失败的通知也未进入业务处理，见 R01、R09。 [订阅服务](../reader-backend/internal/payment/application/subscription_service.go) · [公共决策](../reader-backend/internal/payment/application/checkout_decision_repository.go) |
| 退款 | `PAY-04` | 部分完成 | 按收款和请求 ID 提交持久命令，金额预留、旧命令接管和 Stripe 原请求查询已实现。PayPal 未取得退款编号的在途命令缺少结案入口，见 R04、R09。 [CMS 退款命令](../cms-backend/internal/billing/refund_command.go) · [Reader 执行](../reader-backend/internal/payment/application/refund_commands.go) |
| 资金账务 | `FIN-01` | 部分完成 | 权威流水去重、凭证、账户归属和金额分项已实现。部分退款和剩余本金拒付仍需统一计算权益资金，见 R08。 [资金流水写入](../reader-backend/internal/payment/ledger/repository.go) |
| 结算对账 | `FIN-02` | 已实现 | 具备 PayPal 分卷导入、Stripe 划拨和报表导入、银行流水匹配、定时拉取与命令审计。本机使用测试数据验证，仍需商户结算报告和银行凭证。 [结算实现](../reader-backend/internal/payment/settlement) |
| CMS 资金核对 | `FIN-03` | 部分完成 | 有流水、差异、划拨及批次关联列表，退款与拒付划扣分别统计。订单列表发出的沙箱联查链接丢失环境，见 R10。 [资金查询](../cms-backend/internal/billing/reconciliation_repository.go) · [页面](../cms-front/src/pages/PaymentReconciliation.tsx) |
| 商户账户 | `CFG-01` | 部分完成 | 按账户、渠道和环境注册适配器，历史账户可接收事件。部分 VIP 读写范围没有沿用账户环境，见 R01。 [注册](../reader-backend/cmd/server/payment_accounts.go) · [账户路由](../reader-backend/internal/payment/application/account_routing.go) |
| 商品映射 | `CFG-02` | 已实现 | PayPal Plan 绑定版本化，Stripe Product/Price 校验及下单快照已接入。实际商户产品发布与重新改价仍需验收。 [PayPal 计划发布](../reader-backend/internal/payment/application/vip_offers.go) · [Stripe 商品校验](../cms-backend/internal/billing/stripe_catalog.go) |
| 渠道启停与市场 | `CFG-03` | 部分完成 | 账户启停、商品和销售模式、市场证据、税务差异复核有入口。仍需补齐已存在尝试恢复的缺口，并按拟开放账户、销售模式和市场验收 USD 范围，见 R05。 [渠道配置](../cms-backend/internal/billing/payment_queries.go) · [市场复核](../reader-backend/internal/payment/application/stripe_market_review.go) |
| 风控决策 | `RSK-01` | 部分完成 | 决策与占位同事务，审核日志记录阶段、原因与版本。跨环境协议会参与冲突判断，见 R01。 [决策实现](../reader-backend/internal/payment/application/checkout_decision.go) |
| 渠道信号 | `RSK-02` | 已实现 | PayPal 拒绝及卡验证、Stripe Radar 复核与预警按现有渠道入口归集。实际启用能力以商户权限和事件样本验收为准。 [PayPal 事件解析](../reader-backend/internal/payment/paypal/webhook.go) · [Stripe 风控适配](../reader-backend/internal/payment/stripe/aftercare.go) |
| 风控处置 | `RSK-03` | 部分完成 | 账户策略、审批、升级、退款或争议待办和重新查证均有实现。多笔付款的次要收款可能只有风险状态，没有可处置的暂缓记录，见 R03。 [履约暂缓](../reader-backend/internal/payment/aftercare/fulfillment.go) · [CMS 决策](../cms-backend/internal/billing/aftercare_service.go) |
| 争议同步 | `DSP-01` | 已实现 | 两渠道案件进入事件处理，可查渠道详情。PayPal 独立冲回通过同账户权威流水归并。待验证实际账户的案件权限、争议与报表样本。 [PayPal 事件](../reader-backend/internal/payment/application/webhook_service.go) · [Stripe 案件采集](../reader-backend/internal/payment/stripe/aftercare.go) · [独立冲回](../reader-backend/internal/payment/aftercare/repository.go#L91) |
| 争议工作台 | `DSP-02` | 部分完成 | 有负责人、期限、去重提醒、证据和渠道后台入口。MoR 处置责任显示错误，PayPal 入口为案件列表，财务需凭已展示的案件号手动定位。见 R07。 [CMS 案件](../cms-front/src/pages/PaymentCases.tsx) |
| 争议资金与权益 | `DSP-03` | 部分完成 | 案件划扣、返还、权益冲正及恢复已接入审批和资金证据。部分退款加剩余本金拒付的合计损失未覆盖，多笔资金复核还有暂缓记录缺口。见 R03、R08。 [本金损失视图](../cms-backend/db/migrations/2026111926.sql#L34) · [权益调整](../reader-backend/internal/payment/aftercare/loss.go) |
| CMS 入口 | `FE-01` | 已实现 | 新路由、角色导航和旧地址的权限跳转已接入。原 PaymentTab 已退役。 [路由](../cms-front/src/App.tsx#L313) |
| Reader H5 | `FE-02` | 部分完成 | 有渠道选择、回跳定位、购买历史、查单及订阅管理。禁用存储时 PayPal VIP 无法发起，切换也可能卡住。见 R05、R06、R09。 [购买页](../reader-front/src/pages/Recharge.tsx) |
| CMS 交易、退款和配置 | `FE-03` | 已实现 | 有订单／账期、原款详情、退款命令恢复、配置和旧 settings 白名单。支付渠道的命令结案缺陷见 R04。 [支付路由](../cms-backend/internal/billing/handler.go#L34) · [退款页](../cms-front/src/pages/PaymentRefunds.tsx) · [设置白名单](../cms-backend/internal/novel/service.go#L1327) |
| CMS 风控、争议和对账 | `FE-04` | 部分完成 | 三个工作台及关联跳转已接入。MoR 标签及部分沙箱入口丢失上下文，见 R07、R10。 [案件页](../cms-front/src/pages/PaymentCases.tsx) · [对账页](../cms-front/src/pages/PaymentReconciliation.tsx) |
| Stripe 单次 | `ST-01` | 部分完成 | Checkout、验签、实收、退款和余额流水主路径已实现。跨渠道尝试恢复仍有缺口，见 R05。直收与 MoR 分别完成 USD 实付及售后验收。 [收款处理](../reader-backend/internal/payment/application/stripe_settlement.go) |
| Stripe VIP | `ST-02` | 部分完成 | 有开通、Invoice Payment 分配、账期、取消、Portal/Link 和替代账单识别。VIP 环境隔离、多笔收款复核和本金损失计算仍有缺口，见 R01、R03、R08。 [账单适配](../reader-backend/internal/payment/stripe/vip_invoice.go) · [账期结算](../reader-backend/internal/payment/application/stripe_vip_settlement.go) |

### 1.1 尚需开发或修复的事项

16 是受影响的工作包数量。本轮共 9 个缺陷编号。相同编号涉及多个模块时，在负责该业务规则的模块修复一次，再逐个验收受影响的入口。

| 工作包 | 尚需交付的代码 | 完成判定 | 缺陷 |
| --- | --- | --- | --- |
| `PAY-01` 共用底座 | 在 PayPal Inbox 消费端补上扣费失败、退款在途及退款失败通知的分派和原对象查证。 | 相关通知触发业务处理，重复消费只更新一次，查证失败按既有机制重试。 | R09 |
| `PAY-02` 收款与支付订单 | 补齐从 PayPal 切 Stripe 时旧尝试进入只查证后的恢复路径。 | 查证超时后可继续处理原意向。在任一时点只有一个可能继续收款的尝试。 | R05 |
| `PAY-03` 订阅与计费 | 将 VIP 协议、已付权益和互斥限制按业务环境隔离。补齐 PayPal 扣费失败后的协议核验和恢复处理。 | 沙箱协议既不会发放正式权益，也不会拦截正式开通。一次续费失败保留当前有效的已付权益，后续扣费结果可收敛。 | R01、R09 |
| `PAY-04` 退款 | 补齐 PayPal 在途及失败状态处理，以及退款响应丢失后通过可靠凭证结清原命令的路径。 | 已退款金额与原命令预留只扣一次。同一原款有两笔同额退款时，凭证不足的命令保留待核对。 | R04、R09 |
| `FIN-01` 资金账务 | 提供按原收款去重并汇总的净本金损失，纳入独立核实的部分退款、后续划扣及返还。 | 原本金 1000 分、已退 400 分、另确认划扣 600 分时，计算出 1000 分损失。同一资金动作从多个来源到达时只计算一次。 | R08 |
| `FIN-03` CMS 资金核对 | 从订单入口跳转对账时，携带实际收款环境。 | 从订单列表和详情点开同一沙箱订单，能查到对应沙箱流水。 | R10 |
| `CFG-01` 商户账户 | 补齐 VIP 读取与占位中的环境及账户归属约束，核对历史协议的归属。 | 同一用户的沙箱与正式数据互相隔离，来源待核实的旧协议有明确状态。 | R01 |
| `CFG-03` 渠道启停与市场 | 让既有尝试在切渠道查证失败后继续进入可查证、可恢复的路径，并沿用原账户归属。 | 暂停新增购买或切换失败时，既有尝试仍能查询和恢复。 | R05 |
| `RSK-01` 风控决策 | 将 VIP 防重复决策限定在当前环境。 | 当前环境的 PayPal、Stripe 仍共用互斥规则，其他环境的协议不参与判定。 | R01 |
| `RSK-03` 风控处置 | 为同一账单的多笔收款保存各自可处理的履约暂缓记录。 | 第一笔获批后，第二笔仍可独立批准或拒绝。全部通过后只履约一次，拒绝则生成关联售后待办。 | R03 |
| `DSP-02` 争议工作台 | 使用统一销售模式值展示 MoR 案件的处理责任和响应提示。 | PayPal、Stripe 直收、MoR 与模式未知的案件均显示对应处理方和操作提示。 | R07 |
| `DSP-03` 争议资金与权益 | 用经核实的累计净本金损失计算冲正和恢复，并让多笔资金复核保留各自的处置依据。 | 部分退款加独立划扣达到全部本金时可审批冲正。发生返还后按原订单或账期重算，多收款的处置不会影响正常权益。 | R03、R08 |
| `FE-02` Reader H5 | 补齐无浏览器存储时的 PayPal VIP 发起和继续阅读，接通切换失败后的原意向恢复及续费状态展示。 | 禁用存储也能发起 VIP、从回跳恢复和继续阅读。切换失败提供可执行的下一步，订阅展示已核实状态。 | R05、R06、R09 |
| `FE-04` CMS 工作台 | 修正 MoR 标签及响应提示，保留沙箱订单跳转至争议、对账的环境。 | 财务能辨认案件处理方，从列表、详情、退款及账务入口定位同一环境的记录。 | R07、R10 |
| `ST-01` Stripe 单次 | 将单次 Checkout 接到可恢复的 PayPal 转 Stripe 路径。 | 原尝试结束或恢复均可核实，查证超时不会卡住原意向，也不会并发创建第二笔可扣款尝试。 | R05 |
| `ST-02` Stripe VIP | 在 Stripe VIP 链路接入环境隔离、多笔收款复核及累计净本金损失。 | 跨环境占位互不影响，多笔实收逐笔复核，退款与争议只处置原收款关联的账期。 | R01、R03、R08 |

### 1.2 尚待联调和放行验收

这些项需要实测或业务证据。前述 16 个工作包的代码缺口仍按第 1.1 节跟踪。

| 待验收事项 | 需要的证据 |
| --- | --- |
| 渠道账户、商品及市场 | 用拟开放的 PayPal、Stripe Sandbox 和获授权的商户账户核实权限、商品映射、事件覆盖及 USD 实付、退款与结算。Stripe 直收与 MoR 独立放行。 |
| Link 售后与财务凭证 | 按授权验证 MoR 用户端订单和售后，核对 Reader 同步结果。用渠道报表和独立银行凭证核实结算。 |
| 存量迁移与维护窗口 | 用存量副本演练版本切换、在途退款接管及事件补拉，确认旧交易仍按原渠道和账户处理。 |
| Reader H5 与 CMS 浏览器验证 | 实测跨标签、无存储、重新登录、旧路由、角色权限、跨页联查及界面交互。 |

## 2. 缺陷与修复验收

### R01 · P1 · VIP 权益、协议管理及互斥缺少环境隔离（已复现）

**场景。** 同一数据库含同一用户的沙箱和正式数据。正式服务读取该用户的沙箱 PayPal 已付账期，返回 VIP 有效，订阅列表返回沙箱协议，新建正式 VIP 又被该协议拦截。即使当前部署隔离沙箱库，数据混存、恢复快照或切换环境时也会触发。

**证据与验收。** 临时集成测试将 `CONVERSION_ENVIRONMENT` 设置为 `production`，只种入沙箱协议及已付期，观察到 `access=true, managed=1, checkout=subscription conflict`。协议查询与公共互斥都缺环境条件，Stripe 在途意向索引也只按用户。统一按业务环境、已验证账户与协议来源取数，覆盖授权、管理列表、占位和续费。加入双环境同用户及无法推定环境的旧协议测试。 [权益查询](../reader-backend/internal/novel/repository.go#L270) · [协议列表](../reader-backend/internal/payment/application/subscription_repository.go#L71) · [互斥](../reader-backend/internal/payment/application/checkout_decision_repository.go#L11) · [Stripe 占位索引](../cms-backend/db/migrations/2026111924.sql#L66)

### R02 · 范围外观察 · Stripe 非 USD Charge 的业务收款记录

**场景。** Checkout 已确认成功 Charge，实际 Charge 币种为非 USD。当前单次购买路径仅保存说明和尝试状态后提交，订单仍为 `Pending`，原收款明细为空。VIP 适配器在 Invoice Payment 层直接忽略非 USD 分配，也无法形成相应收款关联。买家确实被扣款，财务却无法在原订单找到可追溯的原收款。仅买家呈现币种非 USD 而 Charge 为 USD 时另有保存收款的分支。

**复现记录与范围决策。** 单次集成探针使用 CAD 1299 的已确认 Charge，查到订单仍为 Pending、原收款数为 0。首版统一 USD，这个场景留作后续扩币时的复查依据，不计入本轮缺陷和验收。拟开放的账户、模式及市场仍须验证实际 USD 扣款和结算。 [单次提前返回](../reader-backend/internal/payment/application/stripe_settlement.go#L76) · [VIP 分配过滤](../reader-backend/internal/payment/stripe/vip_invoice.go#L185)

### R03 · P2 · 多笔收款的 VIP 风控只能持久化一笔暂缓（已复现）

同一账单由两笔付款补足时，第一笔暂缓记录按订单唯一。第一笔批准后，第二笔若有未决复核，`EvaluateFulfillment` 仍返回 Deferred，但 UPSERT 因收款 ID 不一致而不写。CMS 拒绝第二个案件要求它有独立的未结束暂缓记录，因此不能创建退款或争议待办。隔离库复现第二笔返回已暂缓、该案件与订单却无暂缓记录。按订单与收款或复核项保存独立处置，并证明两笔逐一批准可发放一次、其中一笔拒绝可进入售后。 [暂缓写入](../reader-backend/internal/payment/aftercare/fulfillment.go#L16) · [多笔账期核验](../reader-backend/internal/payment/application/stripe_vip_settlement.go#L274) · [拒绝条件](../cms-backend/internal/billing/aftercare_service.go#L98)

### R04 · P2 · PayPal 已退事实无法结清丢失响应的原退款命令（已复现）

部分退款已在 PayPal 成功，POST 响应丢失。Worker 缺退款 ID 时转只查证，最后将命令停在 `review`。随后已验证的完成事件写入同一原款的退款明细，命令仍未关联退款 ID。CMS 可退额查询会同时扣掉已退款和仍被预留的金额，页面也因存在未决命令禁止新退款。隔离库复现 500 分退款已入明细、原命令继续 review 且无退款引用。应提供按可信关联自动结案或授权人工核对结案的路径，保障相同收款两笔同额退款不会被误配，结案后只扣除一次金额。 [退款 Worker](../reader-backend/internal/payment/application/refund_commands.go#L74) · [完成事件](../reader-backend/internal/payment/application/refund_repository.go#L34) · [可退余额](../cms-backend/internal/billing/refund_command.go#L120) · [前端限制](../cms-front/src/pages/PaymentRefunds.tsx#L176)

### R05 · P2 · 切 Stripe 查证失败后，原 PayPal 尝试无法正常继续（代码确认）

切渠道先把 PayPal 尝试标成永久只查证。若下一步查单发生超时或返回无法关闭，函数返回但不记录独立的切换任务或恢复入口。原 Capture 的全部入口都会被标识挡住，H5 也从原批准链接切到确认中。账号仍可通过再次切换触发查证，但想继续用原 PayPal 的用户没有可用路径。应持久化待切换状态并自动查证。允许恢复原 Capture 前，必须在同一事务证明仍由原尝试占位且目标渠道没有开单。覆盖查证超时、购买人返回原渠道及迟到扣款。 [切换写入](../reader-backend/internal/payment/application/checkout_switch.go#L71) · [Capture 保护](../reader-backend/internal/payment/application/checkout_intent.go#L355) · [H5 状态投影](../reader-backend/internal/payment/application/checkout_intent.go#L49)

### R06 · P2 · 存储不可用时 PayPal VIP 结账会在前端退出（代码确认）

PayPal VIP 在调用后端前直接写 `sessionStorage`。浏览器禁止存储或存储满时，此写入会抛错并进入总异常处理，连正式下单请求也未发送。「继续阅读」缺服务器返回路径时直接读 `localStorage` 也可能抛错。按单次购买的既有方式把本地存储作为可选缓存，保留服务端意向、回跳引用和站内路径校验。覆盖拒绝存储、新标签回跳、登录恢复与继续阅读。 [发起 PayPal VIP](../reader-front/src/pages/Recharge.tsx#L401) · [继续阅读](../reader-front/src/pages/Recharge.tsx#L463)

### R07 · P2 · MoR 争议责任显示错误（代码确认）

案件接口返回数据库的 `merchant_of_record`，页面只比较 `mor`，所以能准确归属的 MoR 案件仍显示「销售模式待核实」。需要商户响应的提示也不区分 MoR，可能将渠道处置误判为本方举证。复用同一个销售模式契约，覆盖直收、MoR 及未知模式。手工检查两渠道后台入口和可到达案件。 [业务枚举](../cms-backend/db/migrations/2026111924.sql#L3) · [CMS 字段](../cms-backend/internal/billing/aftercare_repository.go#L21) · [页面责任提示](../cms-front/src/pages/PaymentCases.tsx#L130)

### R08 · P2 · 独立退款与后续冲回未按最终净本金损失合计（已复现）

`payment_principal_loss_by_receipt` 只纳入争议借贷和独立冲回，权益视图拿该损失与整笔原履约本金比较。隔离库构造无税的 PayPal 单次订单，原履约本金 1000 分，已退款 400 分，随后有独立权威流水确认冲回 600 分。损失视图只计入 600 分，返回 `target_blocked=false`，因此无法发起权益冲正。相同的计算缺口也适用于独立核实的部分退款加已结案的剩余本金拒付。应按实际原收款及原发放分摊汇总不同资金动作，并基于渠道引用去重，覆盖同一动作多来源、部分损失、全部损失及返还。 [流水范围](../cms-backend/db/migrations/2026111922.sql#L104) · [冲正规则](../cms-backend/db/migrations/2026111926.sql#L34) · [架构的权益口径](payment-domain-architecture-and-implementation.md#退款全退和权益比较口径)

### R09 · P2 · PayPal 扣费失败及退款失败／在途通知被消费但无业务更新（代码确认）

适配层已产出 `subscription_payment_failed` 和 `refund_status`，Worker 的事件分派没有对应分支，默认返回成功并写入 `processed_at`。独立补查可能获得部分状态，但通知当前不触发对应协议或退款的查证。补齐两类事件到原协议、原退款的有界查证和状态更新。验证暂时失败、后续恢复、已有已付 VIP 不被提前撤销，以及退款失败后释放已核实的预留款。 [PayPal 事件映射](../reader-backend/internal/payment/paypal/webhook.go#L51) · [Worker 分派](../reader-backend/internal/payment/application/webhook_service.go#L88) · [事件完成](../reader-backend/internal/payment/application/reconciliation_repository.go#L181)

### R10 · P2 · CMS 订单列表的沙箱联查默认查询正式环境（代码确认）

订单列表快捷入口只传订单号。对账和争议页缺环境时默认查询 `live`，同一笔沙箱订单从详情凭证区访问则可以保留环境。统一用已确认的订单／收款环境生成链接。若历史环境未知，先显示需要核对的范围。补测同一沙箱订单从列表、详情、退款和结算入口跳转。 [订单列表链接](../cms-front/src/pages/OrdersTab.tsx#L586) · [对账默认环境](../cms-front/src/pages/PaymentReconciliation.tsx#L229) · [案件默认环境](../cms-front/src/pages/PaymentCases.tsx#L235)

## 3. 验证记录与放行边界

- 在本机临时 PostgreSQL 16 使用 `star_novel` 为 Reader、CMS、转化服务分别创建空测试库。三个 Go 模块全量 `go test -p 2 -count=1 ./...` 和 `go vet -p 2 ./...` 均通过。迁移和业务集成测试实际连接数据库。
- 测试夹具可维护性问题。CMS 的退款授权集成测试直接在传入数据库执行基础结构和迁移。重用已跑过的测试库时，执行旧迁移会因 `bookshelves.progress_version` 已存在而失败。空库复跑通过。建议由测试为自己申请临时库，沿用现有迁移测试的隔离方式。 [退款测试准备](../cms-backend/internal/billing/refund_test.go#L26) · [迁移测试隔离](../cms-backend/internal/db/migration_test.go#L14)
- 四个首版范围内的问题 R01、R03、R04、R08 已在隔离库复现。R02 的复现结果作为范围外记录保留。探针仅用于本轮验证，通过临时文件运行。
- Reader H5 和 CMS 各自的 `npm run lint`、`npm run build` 均通过。UI 缺陷来自代码检查。浏览器交互、无存储回跳及浅深色验收待执行。
- 本次使用渠道 Mock、测试数据和本地数据库。实际 PayPal／Stripe Sandbox、正式商户权限、Link 用户端、真实报表与银行凭证、存量副本切换及维护窗口回补仍待验证。
- 代码组织已拆出 adapter、application、aftercare、ledger、settlement。仍需减少机器判断依赖展示文案。例如，Stripe 市场审批使用 `reconciliation_note LIKE`，收款退款使用整句文案识别额外收款。将这些判断迁移为明确的原因码或状态，并让展示文案独立。 [市场复核](../cms-backend/internal/billing/stripe_market_review.go#L54) · [退款识别](../reader-backend/internal/payment/application/stripe_refunds.go#L85)

## 4. 文档随修复更新

主文档第 2 节明确是主线基线。第 6 节后半已经记录到 Schema `2026111926`，但部分源码指针仍指向已移动的 `wallet/service.go` 旧结算逻辑、已删除的 `billing/refund.go` 和 `PaymentTab.tsx`。实现修复完成后，分阶段更新第 6 节的实装证据和验收结果。历史链接可指向对应提交或现行代码位置。 [架构现状](payment-domain-architecture-and-implementation.md#2-现状和迁移范围) · [迁移与切换](payment-domain-architecture-and-implementation.md#6-实施顺序与切换)

## 5. 开发修复记录

第 1.1 节的 16 个工作包按共同业务规则落地。原评审结果保留供回归对照。Schema 更新至 `2026111927`。 [实施记录](payment-domain-architecture-and-implementation.md#6-实施顺序与切换) · [部署手册](../DEPLOYMENT_SOP.md)

| 缺陷 | 修复入口 |
| --- | --- |
| R01 | VIP 权益、管理、开通占位及续费按环境核对，CMS 显示来源待核对的旧协议。 [共享迁移](../cms-backend/db/migrations/2026111927.sql) |
| R03 | 同账单按收款分别持久化风控暂缓，全部通过后才发放。 [账期处理](../reader-backend/internal/payment/application/stripe_vip_settlement.go) |
| R04 | 财务核对已经入账的 PayPal 退款并关联原命令。按已核实退款编号结清预留。 [退款命令](../cms-backend/internal/billing/refund_command.go) |
| R05 | 切换标记持久化并由 Worker 查证。买家恢复原 PayPal 时，在原订单锁内撤销切换，失效的查证结果不能关闭原尝试。 [切换流程](../reader-backend/internal/payment/application/checkout_switch.go) |
| R06 | H5 存储降级、服务器回跳及站内继续阅读。 [购买页](../reader-front/src/pages/Recharge.tsx) |
| R07 | 按销售模式呈现 Stripe、商户或待确认的争议处置责任。 [案件页](../cms-front/src/pages/PaymentCases.tsx) |
| R08 | 以原收款为单位累计已核实退款、划扣和返还，按渠道退款编号排除重复。 [损失计算](../cms-backend/db/migrations/2026111927.sql) |
| R09 | PayPal 失败续费及退款状态通知触发原协议或原退款查证。后续已核实的扣款更新续费结果。 [Inbox 分派](../reader-backend/internal/payment/application/webhook_service.go) |
| R10 | 订单列表、详情、收款退款和结算入口保留对应的渠道环境，未知环境显示待核对。 [订单页](../cms-front/src/pages/OrdersTab.tsx) |

本机隔离 PostgreSQL 16 的三个后端全量测试和 `go vet` 通过。通过 `2026111926` 到 `2026111927` 的带数据升级测试，以及 VIP 跨环境、退款结案、多笔暂缓、累计损失和切换恢复回归。支付专项并发检测、Reader H5 与 CMS 的 lint 和构建通过。存储异常通过模拟拒绝访问和空间耗尽验证。

第 1.2 节继续记录实际渠道、存量副本与浏览器验收。

## 6. 复审结果（Schema `2026111927`）

本轮复审工作区对应的 `2026111927` 迁移及 Reader、CMS、转化服务版本门禁。第 1 节和第 2 节为首轮评审，第 5 节为开发修复说明。下表列的是本轮独立复查结果。

| 原缺陷 | 本轮结论 | 受影响工作包和验证依据 |
| --- | --- | --- |
| R01 · VIP 环境隔离 | 原 VIP 场景通过，整本另见 R11 | `PAY-03`、`CFG-01`、`RSK-01`、`ST-02`。重跑原沙箱 VIP 读入正式环境的探针，通过。仓库测试同时核验了购买互斥、订阅管理和已付权益。 [VIP 环境与续费回归](../reader-backend/internal/payment/application/payment_recovery_integration_test.go#L236) |
| R03 · 多笔付款各自复核 | 通过 | `RSK-03`、`DSP-03`、`ST-02`。重跑第二笔收款暂缓记录的探针，通过。复核记录按订单及原收款保存，Stripe 对每笔足额账单分配逐一执行门禁。 [复核持久化](../reader-backend/internal/payment/aftercare/fulfillment.go#L16) · [账期核验](../reader-backend/internal/payment/application/stripe_vip_settlement.go#L274) |
| R04 · PayPal 退款响应丢失 | 部分通过，旧退款仍有缺口 | `PAY-04`。新命令可由 Finance 按已核实退款编号结案。同额退款、权限和幂等审计的现有测试通过。缺首次发送时间的迁移命令仍无法结案，见第 6.1 节。 [CMS 退款回归](../cms-backend/internal/billing/refund_test.go#L474) |
| R05 · 切换查证失败的恢复 | 原场景通过 | `PAY-02`、`CFG-03`、`FE-02`、`ST-01`。PayPal 切换查证超时后能恢复原订单；在选择继续 PayPal 之后，旧查证结果被拒绝，原 Capture 可继续认领。 [切换恢复回归](../reader-backend/internal/payment/application/payment_recovery_integration_test.go#L86) |
| R06 · 浏览器存储失败 | 代码与存储模拟通过 | `FE-02`。本地模拟存储访问被拒绝及写入空间不足，内存降级读写和删除通过。PayPal VIP 发起、站内回跳及继续阅读均检查了存储不可用分支。浏览器完整回跳仍待验收。 [H5 发起](../reader-front/src/pages/Recharge.tsx#L407) · [存储降级](../reader-front/src/utils/readerStorage.ts) |
| R07 · MoR 案件处理责任 | 代码复查通过 | `DSP-02`、`FE-04`。页面按后端契约 `merchant_of_record` 匹配，按渠道及销售模式显示处理责任和响应提示，CMS lint 与构建通过。 [案件提示](../cms-front/src/pages/PaymentCases.tsx#L130) |
| R08 · 退款加独立冲回的净本金 | 原场景通过 | `FIN-01`、`DSP-03`、`ST-02`。重跑已退 400 分、独立冲回 600 分的探针，通过。现有账务测试还覆盖同一退款动作按渠道退款编号排除重复及权益恢复。 [本金金额汇总](../cms-backend/db/migrations/2026111927.sql#L92) · [冲回回归](../reader-backend/internal/payment/aftercare/repository_test.go#L134) |
| R09 · PayPal 续费及退款状态通知 | 已知渠道对象的原场景通过 | `PAY-01`、`PAY-03`、`PAY-04`、`FE-02`。扣费失败保留有效已付期，确认后续实收清除警示，旧失败重放不会回退结果。已绑定退款编号的通知触发原单查证，再按渠道结果写回状态。 [事件分派](../reader-backend/internal/payment/application/webhook_service.go#L89) · [退款状态回归](../reader-backend/internal/payment/application/refund_commands_test.go#L99) |
| R10 · CMS 沙箱订单联查 | 数据及代码复查通过 | `FIN-03`、`FE-04`。CMS 集成测试验证订单列表、原收款、结账凭证返回沙箱环境。前端链接携带该环境，经检查未知环境展示待核对。 [环境联查回归](../cms-backend/internal/billing/refund_test.go#L457) · [CMS 订单链接](../cms-front/src/pages/OrdersTab.tsx#L586) |

16 个工作包中，`PAY-04` 仍有原缺口，`PAY-02` 和 `CFG-01` 需关闭本轮新增的整本环境缺口，其余 13 个工作包的首轮代码缺口通过本轮复查。联调与放行按第 1.2 节继续验收。

### 6.1 仍需修复的缺口

#### R11 · P1 · 沙箱整本购买可授予正式阅读权益（已复现）

VIP 的环境过滤已进入阅读分支，但整本权益查询仅检查用户、小说、履约状态和购买凭证条件，缺少当前业务环境。阅读快照与钱包章节解锁也使用同一不带环境的整本谓词。同库有沙箱及正式交易时，正式服务可能因此开放付费章节。

在隔离库设置业务环境为 `production`，为同一账号及小说写入一笔已履约的沙箱整本订单，调用实际 `HasWholeBook` 返回 `owned=true`。在整本权益的公共读取契约统一核对当前环境，并明确历史订单的可信归属。回归覆盖正式、沙箱、历史来源待核实三种情形，核对阅读、章节解锁和结账占位。涉及 `PAY-02` 和 `CFG-01`。 [整本权益查询](../reader-backend/internal/novel/repository.go#L52) · [阅读快照](../reader-backend/internal/novel/read_snapshot.go#L29) · [钱包章节解锁](../reader-backend/internal/wallet/repository.go#L153) · [结账占位](../reader-backend/internal/payment/application/checkout_intent.go#L174)

#### R04 · P2 · 缺首次发送时间的旧 PayPal 退款无法人工结案（已复现）

旧退款迁移刻意将首次发送时间留空。若原退款编号未保存，Worker 查证后保留 `review`。即使后来确认退款明细已入账，CMS 按钮资格和服务端核对条件都要求存在 `first_sent_at`，旧命令仍占用原款的可退额度。

在 CMS 集成测试中构造迁移后的命令状态，保留已核实的原收款和成功退款明细，令 `legacy_source=true`、`first_sent_at=NULL`。Finance 使用相符退款编号结案时返回 `ErrRefundAmountUnavailable`。需要给来源与金额可核实的旧命令补上授权查证结案，保留对同额退款错配的保护。无法核实的旧命令维持待核对。验收同时验证原操作不再次发送、退款及预留只扣一次，重复结案不重复写审计。涉及 `PAY-04`。 [迁移规则](../cms-backend/db/migrations/2026111917.sql#L130) · [按钮资格](../cms-backend/internal/billing/refund_command.go#L33) · [服务端结案条件](../cms-backend/internal/billing/refund_command.go#L204)

### 6.2 验证与放行

- 在本机临时 PostgreSQL 16 给 Reader、CMS、转化服务各分配空库，三个模块全量 `go test -p 2 -count=1 ./...` 和 `go vet -p 2 ./...` 均通过。迁移回归包含带数据的 `2026111926` 至 `2026111927` 升级。
- Reader 对 PayPal 切换、VIP 环境及续费失败、Stripe VIP 账期、退款恢复和售后本金处置的 5 个专项用例运行 `go test -race`，均通过。
- Reader H5 与 CMS 的 `npm run lint` 和 `npm run build` 均通过。额外探针重跑了 R01、R03 和 R08 的原场景，并复现上述两个缺口。探针通过临时文件运行。
- `refund_commands.go` 仍有一处格式差异，由 `gofmt -l` 检出。 [退款 Worker](../reader-backend/internal/payment/application/refund_commands.go#L154)
- 正式商户资格、实际渠道 Sandbox、浏览器完整回跳、Link 售后、银行凭证及生产存量副本演练仍以第 1.2 节作为放行验收项。首版范围为 USD。

## 7. R11 与旧退款修复记录

| 缺陷 | 修复与回归 |
| --- | --- |
| R11 | 整本状态查询、阅读快照、钱包解锁和结账占位共用环境判断。订单快照与渠道收款证据必须一致。无环境快照的历史订单以已验证的原渠道收款确认环境，整本授权仍要求购买对象证据。回归覆盖同库沙箱和正式订单、来源未知的历史订单、及已知归属的历史订单。 [环境判断](../reader-backend/internal/novel/repository.go) · [隔离回归](../reader-backend/internal/payment/application/whole_book_environment_test.go) |
| R04 | 缺首次发送时间的迁移命令可由财务凭同一原收款、环境、金额、币种及未关联的退款编号结案。已入账退款保留一笔，原命令预留释放。回归覆盖同额编号防串单、重复结案、审计及可退金额。 [财务结案](../cms-backend/internal/billing/refund_command.go) · [CMS 回归](../cms-backend/internal/billing/refund_test.go) |

本轮复用 Schema `2026111927`，迁移版本门禁沿用现有配置。真实渠道和放行验收继续按第 1.2 节跟踪。

## 8. ac1c6e1 复审结果

| 缺陷 | 本轮结果 | 验证依据 |
| --- | --- | --- |
| R11 · 沙箱整本权益串用 | 原缺陷通过 | 重跑原探针，正式环境读取同库沙箱整本订单返回未购。仓库测试覆盖正式、沙箱、历史有凭证与历史来源未知，核对拥有状态、结账占位和钱包解锁。阅读快照已接入同一环境谓词。仍可扣款的无环境旧单另见 R12。 [原路径](../reader-backend/internal/novel/repository.go#L57) · [阅读快照](../reader-backend/internal/novel/read_snapshot.go#L29) · [回归](../reader-backend/internal/payment/application/whole_book_environment_test.go#L13) |
| R04 · 旧 PayPal 退款结案 | 通过 | 重跑缺首次发送时间的原探针通过。迁移命令在有同一原款及渠道退款证据时可由 Finance 结案。仓库回归验证已被占用的同额退款编号无法再次关联，可退额仅释放一次原预留，两次结案仅写一条审计，且首次发送时间仍为空。 [结案代码](../cms-backend/internal/billing/refund_command.go#L179) · [迁移退款回归](../cms-backend/internal/billing/refund_test.go#L503) |

### R12 · P1 · 等待扣款的旧整本订单失去购买互斥（已复现）

具有 PayPal 订单引用及整本购买凭证的旧 `Pending` 订单可能没有环境快照，也没有已核实收款。新结账用整本权益的环境谓词筛选旧订单，这类订单因此无法占位。与此同时，Reader 补查仍会认领该旧 PayPal 订单，且无独立尝试记录的旧订单仍符合既有 Capture 认领条件。若买家继续完成新意向的结账，旧订单后续获批或被补查时仍可能扣款，产生同一小说的重复收款。

**隔离库验证。** 设置 Reader 为 `production`，为同一用户和小说创建一笔无 `checkout_id`、无环境快照、已有 PayPal 引用及整本凭证的在途订单。实际调用 `preparePurchase` 返回新意向 `ready`。随后 `claimPayPalOrders` 认领原旧订单，`leaseCheckoutCapture` 对它返回 `true`。本轮验证了应用内可同时进入两个收款路径的状态，使用临时数据库。 [新意向占位](../reader-backend/internal/payment/application/checkout_intent.go#L174) · [旧订单补查](../reader-backend/internal/payment/application/reconciliation_repository.go#L23) · [Capture 认领](../reader-backend/internal/payment/application/checkout_intent.go#L364) · [实际扣款条件](../reader-backend/internal/payment/application/checkout_service.go#L353)

**修复验收。** 整本权益授权继续要求可信环境。对仍可能收款的旧订单，在确认其环境和渠道状态前维持购买互斥，或由统一状态机阻止旧 Capture 并查清资金结果后再释放。分别验证同环境在途、跨环境已核实、环境未知、旧渠道已关闭和收款晚到。断言任一时点只有一个能继续收款的尝试。涉及 `PAY-02`、`CFG-01`。 [架构的购买互斥](payment-domain-architecture-and-implementation.md#跨渠道购买意向和互斥范围)

### 本轮验证范围

- 在本机临时 PostgreSQL 16 中复跑上轮两个原场景，并单独验证 R12。复验使用临时测试代码，未向任何真实渠道发起扣款或退款。
- Reader 的支付、小说阅读、钱包和支付运维包，以及 CMS 的财务和迁移包，使用隔离库运行 `go test -p 2 -count=1` 均通过。变更涉及的 Reader 和 CMS 包通过 `go vet`。
- 真实渠道、Link 售后、银行凭证及生产存量副本切换仍按第 1.2 节验收，首版币种范围为 USD。

## 9. R12 修复记录

- 缺环境的 PayPal 旧整本单仍可能收款时，保留该用户和小说的购买占位。已有意向在创建下一笔渠道尝试前也检查占位。已核实属于其他环境的旧单不参与当前环境的互斥，整本权益仍要求可信环境。 [占位规则](../reader-backend/internal/payment/application/whole_book_reservation.go) · [意向和扣款认领](../reader-backend/internal/payment/application/checkout_intent.go)
- Worker 和买家回跳均可查询旧单。发现同环境的其他可收款尝试时，旧单停止提交 Capture。只有原 PayPal 订单的终态和购买凭证吻合，且没有收款记录，才释放旧单。已关闭旧单的晚到实收登记在原单供核对。两渠道在发放前按买家串行检查同一本书，额外款项保留收据，其退款不扣减已授予的金币或权益。 [完成与终态处理](../reader-backend/internal/payment/application/checkout_service.go) · [构造回归](../reader-backend/internal/payment/application/whole_book_environment_test.go)
- 沿用 Schema `2026111927`。真实渠道和生产切换继续按第 1.2 节验收。

## 10. R12 修复后复审

| 模块 | 结果 | 验证证据 |
| --- | --- | --- |
| 购买意向和订单恢复 `PAY-02` | R12 原场景通过 | 无环境快照、带整本购买凭证的 `Pending` 旧单仍可被 Worker 认领，同时会拦截新意向。回归还覆盖终态关闭后放行，以及关闭后的晚到收据重复投递只保留一笔。 [占位代码](../reader-backend/internal/payment/application/checkout_intent.go#L174) · [回归](../reader-backend/internal/payment/application/whole_book_environment_test.go#L96) |
| PayPal 和 Stripe 渠道执行 `PAY-02`、`ST-01` | R13 · P1 | 切换前已落库的尝试可以在旧单成功履约后继续走到渠道写入。PayPal 模拟适配器收到一次 Capture 调用，Stripe Worker 返回 `MayCreate=true`。 |
| 旧单补查 `PAY-02`、`CFG-01` | R14 · P2 | 缺商品凭证的旧单占位后，Worker 在读渠道之前返回，已关闭的旧单仍挡住新购买。 |
| 环境与售后 `CFG-01`、`PAY-04` | R11、R04 回归通过 | 正式环境无法读取沙箱整本权益。缺首次发送时间的历史 PayPal 退款命令可按凭证结案。额外旧单收款全退后，原有金币余额和已购权益保留。 [环境和退款回归](../reader-backend/internal/payment/application/whole_book_environment_test.go) · [CMS 退款回归](../cms-backend/internal/billing/refund_test.go#L503) |

### R13 · P1 · 旧单已收款并履约，存量尝试仍可发起收款

上轮缺陷可能留下同一用户、同一小说的旧 PayPal 订单和新渠道尝试。旧单此前已发出的扣款请求可能晚到。旧款凭证入账并完成履约后，应继续查询未决渠道结果，并阻止已被这笔购买替代的尝试发起新的渠道写入。当前 PayPal 的 Capture 认领只检查另一笔旧单是否仍为 `Pending`。旧单变为 `Success` 后，现有的 `buyer_action` 尝试重新具备 Capture 资格。Stripe 新尝试的预留会检查旧单和已购权益，Worker 恢复已落库的 `creating` 尝试时会跳过这一步。已有的重复履约检查只能在实收后保护权益，买家仍可能再付一次。 [PayPal Capture 认领](../reader-backend/internal/payment/application/checkout_intent.go#L370) · [提交 Capture](../reader-backend/internal/payment/application/checkout_service.go#L368) · [Stripe Worker 认领](../reader-backend/internal/payment/application/stripe_checkout_repository.go#L125) · [创建 Stripe Checkout](../reader-backend/internal/payment/application/stripe_checkout_service.go#L113)

**复现。** 在正式环境的隔离库中分别种入旧 PayPal 订单及同一本书的存量尝试，用已验证的 USD Capture 完成旧单履约，并通过整本拥有查询确认权益。PayPal 尝试在旧单 `Pending` 时无法认领 Capture，旧单履约后，买家完成入口向模拟适配器提交了一次 Capture。Stripe 尝试处于 `creating` 且尚未发送，旧单履约后，后台认领仍返回 `MayCreate=true`。 [购买互斥验收](payment-domain-architecture-and-implementation.md#跨渠道购买意向和互斥范围)

**修复验收。** 在渠道写入的认领处校验同一环境内的整本占位、已支付待履约和已购状态，覆盖 Reader 回跳、Webhook 与 Worker。复验旧单已支付、待确认、确认无收款三种状态。旧单已支付时，存量 PayPal 尝试不得再提交 Capture，Stripe 未发送的尝试不得再建 Checkout。已向渠道提交过的请求继续按原账户查证，对已打开的 Checkout 按渠道能力验明终态。晚到实收应保留原始收据，退款应只作用于对应的额外收款。

### R14 · P2 · 缺整本购买凭证的旧单无法释放占位

占位规则接受没有 `checkout_id` 和独立整本凭证、但保留 PayPal 订单号的旧 `Pending` 订单。补查可以认领这类订单，但 `completePayPalOrder` 在查 PayPal 前就以缺整本凭证退出，渠道关闭分支也复用同一凭证校验。即使原渠道已关闭且没有收款，Worker 再次扫到该订单也会退出，用户仍无法为同一本书发起新购买。 [占位条件](../reader-backend/internal/payment/application/whole_book_reservation.go#L13) · [补查入口](../reader-backend/internal/payment/application/reconciliation_repository.go#L23) · [完成入口](../reader-backend/internal/payment/application/checkout_service.go#L295) · [渠道关闭处理](../reader-backend/internal/payment/application/checkout_service.go#L347) · [凭证校验](../reader-backend/internal/payment/application/checkout.go#L430)

**复现。** 种入一笔无 `checkout_id`、无整本凭证且有 PayPal 订单号的 USD 旧单，模拟原渠道查询返回同订单、同金额和同环境的 `VOIDED`，无 Capture。Worker 认领成功，完成入口返回 `ErrCheckoutUnavailable`，数据库仍为 `Pending`，新意向返回 `ErrCheckoutConflict`。

**修复验收。** 独立核验旧单终态和履约资格。原账户、订单号、收款状态能够核实为已关闭且无收款时，可关闭订单并释放占位。账户不明、仍可能收款或查证失败时保留占位并提供可审计的核对入口。授权整本权益时仍要求独立商品凭证。补测确认无款、实收但缺凭证、查证未知三个分支。

### 本轮验证范围

- 使用本机临时 PostgreSQL 16、Schema `2026111927` 和模拟适配器，重跑 R12、R11、R04 原场景，并独立复现 R13 的 PayPal 与 Stripe 分支及 R14。
- Reader 的支付域、小说、钱包、支付运维包通过 `go test -p 2 -count=1` 和 `go vet -p 2`。CMS 财务包通过 `go test -p 2 -count=1`。三项独立复现均观察到上述缺陷。
- 本轮范围为 USD。真实渠道、银行凭证及生产存量切换仍按第 1.2 节验收。

## 11. R13 与 R14 修复记录

| 缺陷 | 修复与回归 |
| --- | --- |
| R13 | PayPal 建单及 Capture、Stripe Checkout 创建在认领与提交前核对同环境的旧单占位、已支付待发放及已购状态。Stripe 已发送的尝试继续查证，仍可支付且已被同一本书的实收替代时，走已有的关闭并查证流程。回归检查旧单待确认、已履约、已支付待发放及占位释放。 [写入保护](../reader-backend/internal/payment/application/whole_book_reservation.go) · [Worker 查证](../reader-backend/internal/payment/application/stripe_checkout_service.go) |
| R14 | 旧单缺整本商品凭证时，仍可使用原 PayPal 账户按本地原订单号及金额查询。确认终态且无收款后释放占位。可信实收进入原订单收据并待核对，财务可据此追踪。查证失败保留原占位，发放权益仍需独立商品凭证。 [旧订单查询](../reader-backend/internal/payment/application/legacy_book_recovery.go) · [回归](../reader-backend/internal/payment/application/whole_book_environment_test.go) |

沿用 Schema `2026111927`。真实 Stripe 与 PayPal Sandbox 及生产存量验收继续按第 1.2 节跟踪。

## 12. Stripe Sandbox 联调（2026-11-22）

使用 Stripe Sandbox `acct_1UdjRfBacqEepNtm`、API `2026-11-18.endive`、Stripe CLI `v1.55.14` 和隔离 PostgreSQL 16（Schema `2026111927`）。Stripe Balance 确认 `livemode=false`。

| 验收项 | 实测结果 |
| --- | --- |
| Reader Checkout 适配器 | 直收单次和 VIP Checkout 均通过创建、USD Price 明细查询、相同幂等键返回同一会话及到期。 |
| Stripe 测试卡收款与退款 | USD 1.99 扣款成功，分两次退回 USD 0.80 和 USD 1.19，额外 USD 0.01 被 Stripe 拒绝。拒绝测试卡返回 `card_declined`。 |
| VIP 账单 | 测试时钟产出首付和续费账单，各实付 USD 3.99。另验证期末取消设置，以及账单和 InvoicePayment 查询。 |
| Radar 与争议 | 测试卡生成 Early Fraud Warning，预警列表和详情可查。另一笔测试扣款生成争议，详情为 `needs_response`，存在资金划扣流水和划扣事件。 |
| Webhook 转发 | Stripe CLI 将 `checkout.session.expired` 事件 `evt_1UeSLWBacqEepNtmAknJWg95` 实时转发至隔离 Reader 的 `18080` 端口，HTTP 200，Inbox 写入一条。原有本地服务的 `8080` Webhook 路径返回 404，原进程保留。 |
| Webhook 重投 | 11 种真实 Sandbox 事件使用本地签名重放，正确签名与重复投递返回 200，每个事件只产生一条 Sandbox Inbox 记录，错误签名被拒绝。覆盖 Checkout 到期、订阅更新、两类账单已付、退款创建与更新、Charge 成功与失败、PaymentIntent 失败、争议创建和划扣。 |
| USD 结算 | **未通过。** 买家扣款与两笔退款均为 USD，对应 Stripe Balance Transaction 的币种均为 HKD。USD 1.99 收款对应 HKD 15.60，USD 0.80 和 USD 1.19 退款分别对应 HKD −6.27 和 HKD −9.33。账号返回 `payouts_enabled=false`。需使用能够提供 USD 余额和划拨凭证的账户配置复验。 [首版币种范围](payment-domain-architecture-and-implementation.md#starnovel-支付域架构与开发实施方案) |

本地 Reader 的 Stripe 适配器测试和隔离库中的 Checkout、VIP、结算及整本回归通过。浏览器支付回跳至 Reader 履约、Radar 人工审核、USD 划拨及银行匹配、Managed Payments 与 Link 仍待专项验收。

## 13. Stripe 沙箱默认结算币种切换复验

同一 Stripe Sandbox 改为默认 USD 后，用新的一笔 USD 1.99 扣款复验 `GET /v1/balance_transactions/{id}`。新收款流水 `txn_3UeSjiBacqEepNtm08CPSr4o` 为 USD 1.99，总手续费 USD 0.38，净额 USD 1.61。全额测试退款成功，流水 `txn_3UeSjiBacqEepNtm02JfobLH` 为 USD −1.99。两条新流水的 `exchange_rate` 均为空。原交易流水 `txn_3UeSJFBacqEepNtm1GHveCzt` 仍为 HKD 15.60。该沙箱 `payouts_enabled=false`，划拨与实际银行入账仍待验证。

## 14. R13 与 R14 修复后复审

| 模块 | 结果 | 验证证据 |
| --- | --- | --- |
| 整本收款与渠道恢复 `PAY-02`、`ST-01` | R13 原场景通过 | 旧 PayPal 在途、已购和实收待发放时，存量 PayPal Capture 的模拟调用次数为零。Stripe 尚未发送的尝试保持待命，已发送的尝试可以查证并关闭被替代的会话。旧单全退后，原有 PayPal 尝试可继续。 [渠道写入回归](../reader-backend/internal/payment/application/whole_book_environment_test.go#L235) |
| 旧单补查与退款 `PAY-02`、`PAY-04`、`CFG-01` | R14 原场景通过 | 用原 Worker 认领入口复验缺商品凭证的 `VOIDED` 旧单，收款为空时关闭并放行新购买。真实收款的模拟凭证入原单并保留占位，超时维持原状态。额外探针重复投递这类待核对收款的全额退款，订单结案且已有金币未扣。 [旧单补查回归](../reader-backend/internal/payment/application/whole_book_environment_test.go#L316) · [退款分支](../reader-backend/internal/payment/application/refund_repository.go#L67) |
| 结算对账 `FIN-02`、`FIN-03` | USD 本地回归通过 | USD 划拨与银行凭证匹配、重复匹配、划拨撤回后重新进入核对。CMS 财务查询通过。 [结算回归](../reader-backend/internal/payment/settlement/reconciliation_test.go#L18) · [CMS 财务回归](../cms-backend/internal/billing/reconciliation_test.go) |

### 本轮验证范围

- 收款、退款、结算和银行对账统一使用 USD。第 13 节记录 Stripe 默认结算币种调整后的复验结果。
- 使用隔离 PostgreSQL 16 和模拟渠道复验。Reader 支付、小说、钱包及支付运维包，CMS 财务与数据库迁移测试包，以及转化服务本次受影响的包均通过测试。Reader 与 CMS 相关包通过 `go vet`，CMS 前端通过 lint 与构建。R11、R12 原独立探针也已重跑通过。
- 撤回 HKD 结算改动后，以 Schema `2026111927` 在隔离 PostgreSQL 16 复跑三个后端的全量测试及 `go vet`，CMS 前端 lint 和构建通过。确认划拨和银行流水表均限制为 USD。
- 本轮使用模拟渠道。真实 USD 划拨、银行凭证及生产存量切换继续按第 1.2 节验收。

## 15. USD 范围全流程复评

覆盖架构第 4 节的 20 个通用及前端工作包，以及第 5 节 `ST-01`、`ST-02` 两项 Stripe 工作包。Stripe 首版商品范围为获批的整本和 VIP，PayPal 保留原金币、整本与 VIP。直收与 MoR 按各自渠道证据验收。下表「已实现」表示本轮核对到业务入口、持久化或处置路径及现有验证依据。

| 工作包 | 当前代码 | 实现与验证依据 |
| --- | --- | --- |
| `PAY-01` 公共底座 | 部分完成 | 账户化 Inbox、主动补拉、定向重跑和版本门禁已接入。历史订单主动查证与定向恢复仍有 R15。 [事件恢复](../reader-backend/internal/payment/application/event_recovery.go) · [迁移回归](../cms-backend/internal/db/migration_test.go#L484) |
| `PAY-02` 一次性收款与履约 | 部分完成 | 购买意向、整本防重、切渠道、钱包和权益、原收款关联及事务 Outbox 已接入。R13、R14 原场景通过，超龄旧单仍可能锁住购买，见 R15。 [意向及防重回归](../reader-backend/internal/payment/application/checkout_intent_test.go) · [旧单回归](../reader-backend/internal/payment/application/whole_book_environment_test.go) |
| `PAY-03` 订阅与计费 | 已实现 | PayPal 协议、版本、首付和续费、失败观察、取消补偿及多协议管理已接入，同类 VIP 共享 PayPal 和 Stripe 互斥规则。 [权益、续费及账户回归](../reader-backend/internal/payment/application/payment_recovery_integration_test.go#L236) · [跨渠道互斥](../reader-backend/internal/payment/application/checkout_intent_test.go#L224) |
| `PAY-04` 退款 | 已实现 | CMS 按原收款提交持久命令，Reader 按渠道证据与重试窗口处理并核销预留。旧命令可由财务凭已入账退款结案。集成测试覆盖相同请求重放及同额独立退款。 [CMS 退款回归](../cms-backend/internal/billing/refund_test.go#L205) · [旧命令](../cms-backend/internal/billing/refund_test.go#L503) · [执行恢复](../reader-backend/internal/payment/application/refund_commands_test.go) |
| `FIN-01` 资金账务 | 已实现 | 权威 USD 流水按账户及渠道标识去重，金额分项及与原收款的关联可重建。凭证不足时保留净额未知。 [证据合并回归](../reader-backend/internal/payment/ledger/repository_test.go#L16) · [PayPal 订阅资金证据](../cms-backend/internal/billing/refund_test.go#L173) |
| `FIN-02` USD 结算 | 已实现 | PayPal 报告、Stripe 报表和划拨、银行 CSV、匹配及按账户调度均有入口。本地覆盖批次分配、重复匹配和撤回后重新核对。 [调度](../reader-backend/internal/payment/settlement/automatic.go) · [银行匹配回归](../reader-backend/internal/payment/settlement/reconciliation_test.go) |
| `FIN-03` CMS 资金视图 | 已实现 | 订单、权威流水、批次、银行证据与差异可联查，税费分列，仪表盘分别统计退款和拒付划扣。 [查询](../cms-backend/internal/billing/reconciliation_repository.go) · [财务回归](../cms-backend/internal/billing/reconciliation_test.go) |
| `CFG-01` 账户、凭证与环境 | 已实现 | 启动核对已注册账户和凭证，历史适配器按作用域继续处理。回归覆盖错环境和轮换后原账户事件。 [账户注册](../reader-backend/cmd/server/payment_accounts.go) · [原账户事件测试](../reader-backend/internal/payment/application/event_recovery_test.go#L40) |
| `CFG-02` 渠道商品映射 | 已实现 | PayPal Plan 绑定套餐版本，Stripe Product 和 Price 按账户、商品、USD 价格及销售模式校验，历史订单及协议用原快照。 [PayPal 版本发布回归](../reader-backend/internal/payment/application/vip_offers_test.go#L52) · [Stripe 目录校验](../cms-backend/internal/billing/stripe_catalog.go) |
| `CFG-03` 渠道启停与市场 | 已实现 | 按账户、商品、单次或订阅及销售模式控制入口。Stripe 保存市场、实际地区及税务依据，金额和地区差异进入复核。 [账户开关](../cms-backend/internal/billing/payment_queries.go#L58) · [市场复核](../reader-backend/internal/payment/application/stripe_market_review.go) |
| `RSK-01` 公共结账决策 | 已实现 | 购买意向、尝试及 VIP 复用事务内决策，记录规则版本、原因与结果。CMS 可按订单查询。 [决策层](../reader-backend/internal/payment/application/checkout_decision.go) · [CMS 查询](../cms-backend/internal/billing/aftercare_repository.go#L92) |
| `RSK-02` 渠道风险信号 | 已实现 | PayPal 拒绝或验证及 Stripe Radar 复核、预警等进入统一信号记录。保留渠道原因及来源。 [PayPal 信号](../reader-backend/internal/payment/paypal/aftercare.go) · [Stripe 信号回归](../reader-backend/internal/payment/stripe/aftercare_test.go) |
| `RSK-03` 暂缓履约与复核 | 已实现 | 账户策略、按收款暂缓、复核决定、拒绝转退款或争议、超期升级及审计均有入口。 [暂缓回归](../reader-backend/internal/payment/aftercare/repository_test.go) · [CMS 处置](../cms-backend/internal/billing/aftercare_service.go) |
| `DSP-01` 同步和补查 | 已实现 | PayPal 和 Stripe 案件、PayPal 独立冲回、时窗及定向补查已接入。补查后的资金影响以权威流水为准。 [补查入口](../reader-backend/cmd/payment-ops/main.go) · [PayPal 事件处理](../reader-backend/internal/payment/paypal/aftercare.go) |
| `DSP-02` 案件与证据工作台 | 已实现 | 负责人、截止提醒、处置责任、原交易、账期和使用证据可查。PayPal 通过渠道案件列表和已展示的案件编号定位。 [案件工作台](../cms-front/src/pages/PaymentCases.tsx) · [权限与案件操作回归](../cms-backend/internal/billing/refund_test.go#L455) |
| `DSP-03` 拒付与权益 | 已实现 | 按实际履约资金计入退款、拒付和返还。授权审核后可冲正、恢复，额外收款与履约资金独立处置。 [本金损失视图](../cms-backend/db/migrations/2026111927.sql#L52) · [权益生命周期回归](../reader-backend/internal/payment/aftercare/repository_test.go#L14) |
| `FE-01` 支付中心及退役旧入口 | 已实现 | CMS 新路由、角色访问、旧地址跳转已接入，原 `PaymentTab` 已退役。 [新旧路由](../cms-front/src/App.tsx#L313) · [API 角色及字段回归](../cms-backend/internal/billing/refund_test.go#L290) |
| `FE-02` H5 支付、回跳与订阅管理 | 部分完成 | 方式选择、服务端回跳、存储降级、购买历史及 VIP 管理已接入。R15 中旧单不可从原用户的购买历史找回。 [回跳及进度](../reader-front/src/pages/Recharge.tsx#L205) · [历史查询](../reader-backend/internal/payment/application/checkout_intent.go#L208) |
| `FE-03` CMS 交易及配置 | 已实现 | 交易、订阅、退款命令恢复和渠道配置可查。旧 `/settings` 支付字段已收口。 [CMS 路由](../cms-backend/internal/billing/handler.go#L26) · [退款页面](../cms-front/src/pages/PaymentRefunds.tsx) · [旧配置入口](../cms-backend/internal/novel/handler.go#L897) |
| `FE-04` CMS 售后与资金联查 | 已实现 | 风控、争议及对账页面与交易和退款联查，复用 CMS 布局、表格、状态及确认弹窗。 [案件联查](../cms-front/src/pages/PaymentCases.tsx#L161) · [对账页](../cms-front/src/pages/PaymentReconciliation.tsx) |
| `ST-01` Stripe 单次收款 | 已实现 | USD 整本的 Checkout、签名 Inbox、查单核验、退款、争议及市场适配均有入口。模拟结算与先前直收 Sandbox 证据已记录。 [单次回归](../reader-backend/internal/payment/application/stripe_checkout_test.go) · [Sandbox 记录](#12-stripe-sandbox-联调2026-11-22) |
| `ST-02` Stripe VIP | 已实现 | 核验 Invoice、Invoice Payment 及实际收款后履约。覆盖部分收款、零额或外部已付复核、同周期重新开票和取消。直收 Portal、MoR Link 管理入口已提供。 [VIP 账期回归](../reader-backend/internal/payment/application/stripe_vip_test.go) · [管理入口](../reader-front/src/pages/Recharge.tsx#L593) |

### R15 · P2 · 超过补查窗口的 PayPal 旧整本订单持续占位

旧订单已有 `checkout_id` 和 PayPal 订单号，但无环境快照及渠道尝试。购买意向将这种旧 `Pending` 单视为占位，与账龄无关。Worker 对有 `checkout_id` 的待付款订单只扫描最近 30 天，`payment-ops -kind order` 也拒绝对此类超龄订单安排补查。Reader 购买记录又要求当前环境快照，失去原回跳引用后的用户无法从历史继续处理。这时即使 PayPal 已关闭且无收款，也缺少能把本地占位释放的常规路径。 [占位规则](../reader-backend/internal/payment/application/whole_book_reservation.go#L16) · [Worker 范围](../reader-backend/internal/payment/application/reconciliation_repository.go#L23) · [运维恢复](../reader-backend/internal/payment/application/operations_repository.go#L109) · [购买历史](../reader-backend/internal/payment/application/checkout_intent.go#L208)

**复现。** 在隔离库写入 31 天前的 USD 旧整本单。新购买得到 `ErrCheckoutConflict`，Worker 认领 0 条，定向重试得到 `ErrTaskNotRetryable`，购买历史返回 0 条。直接调用旧单处理并提供同账户、同订单号、同金额、无 Capture 的 `VOIDED` 模拟证据后，同一个新购买请求返回 `ready`。

**修复验收。** 将仍占位的旧订单纳入可认领的只查证或可审计的定向恢复路径。无法确定归属或渠道结果时保留占位。核实关闭且无收款后允许新购买。已收款时按 R14 保留证据及复核，禁止因超时直接释放或发起扣款。验证 30 天边界、已知和未知环境、回跳丢失、查询超时、晚到实收及原账户隔离。向用户提供可执行的查进度或客服路径。

### 真实渠道及上线验收

| 待验收 | 关闭条件 |
| --- | --- |
| PayPal 与 Stripe 直收全流程 | 按拟放行账户、SKU 和市场，从实际 H5 结账和回跳跟踪到权益、续费、退款、风控与争议。第 12、13 节记录了部分 Stripe Sandbox 结果。 |
| Stripe MoR 与 Link | 用获批账户和拟开启市场验证 USD 实收、税务责任、账单地区变化以及 Link 售后与 Reader 权益同步，单独批准开放。 |
| USD 结算与银行 | 使用有划拨能力的商户、真实脱敏报告和独立 USD 银行入账凭证，从订单追到实际入账。 |
| 应用交互与存量切换 | 浏览器验证移动回跳、无 Web Storage、重登换号、CMS 角色及跨页联查。用脱敏存量副本完成 Schema 升级、在途退款交接、维护期事件补拉和暂停新交易演练。 |

### 本轮验证

- 在同一临时 PostgreSQL 16 实例使用 `star_novel` 给三个后端分别建库。Reader、CMS、转化服务仓库全量 `go test -p 2 -count=1 ./...` 和 `go vet -p 2 ./...` 通过。通过带历史数据的 `2026111926` 到 `2026111927` 迁移回归，以及整本、订阅、支付命令、账务、风控与争议测试。
- Reader H5 和 CMS 的 `npm run lint` 与 `npm run build` 通过。R15 使用独立探针复现并验证渠道终态解除占位的对照场景。
- 本轮粒度为代码盘点、隔离库和模拟渠道，未发起真实支付写入、浏览器端到端联调或生产操作。

## 16. R15 修复

超过 30 天、已有 `checkout_id` 和 PayPal 订单号、无渠道尝试且仍为 `Pending` 的整本旧单进入 Reader 补查队列，也可通过 `payment-ops -kind order` 按本地订单 ID 定向安排重试。恢复按原 PayPal 账户读取渠道结果。原账户不明、环境不符、查询超时或结果仍为已批准时保留占位。确认原单已关闭且无收款后释放占位。迟到实收保留原订单凭证并进入复核，此类款项全退时可按原凭证结案。 [恢复代码](../reader-backend/internal/payment/application/legacy_book_recovery.go) · [Worker](../reader-backend/internal/payment/application/reconciliation_repository.go)

H5 购买历史优先展示仍占位的旧单。买家可在原账户登录后从历史打开状态，使用现有「Check status」查看进展。状态页提供客服提示。已核实的迟到实收仅在所属环境展示。 [历史查询](../reader-backend/internal/payment/application/checkout_intent.go) · [专项回归](../reader-backend/internal/payment/application/aged_legacy_book_test.go)

隔离 PostgreSQL 16 和模拟 PayPal 的专项测试通过，覆盖 30 天边界、未知和已知环境、跨用户访问、原账户与替换账户、查询超时、已批准但未收款、关闭后重新购买，以及迟到收款与退款。Reader 全量测试、`go vet` 和专项竞态检测通过。真实渠道与浏览器端到端联调仍按第 15 节验收。
