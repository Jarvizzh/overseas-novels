# 支付域实现 Review · 2026-11-23

## 18–22 最新复验 · 2026-11-26 23:29

基线为 `4dd28a4` 加当前工作区修复。**20、21、22 通过；18 保留 P1，19 重新打开为 P2。** 上轮的唯一合法首款已能正常发放 VIP，本轮另复现以下两项。

- **18 · 旧意向从 PayPal 切到 Stripe 再切回 PayPal，首款仍可能重复履约。** 在第一次切换后还原升级前无替换标记的记录，再切回 PayPal，得到新协议。模拟旧 PayPal 首款先到、新 PayPal 首款随后到账，两个协议覆盖同一期，数据库有两笔已履约订单。旧款入账时没有已履约的重叠订单，父意向当前渠道也已变回 PayPal；随后新协议按正常首期路径履约。建议在新协议首期入账时也检查同次购买的已履约账期，并保留能够追溯旧协议的替换记录。验收应覆盖两种到账顺序，保留两笔实收且只履约一次。[存量首款判断](../reader-backend/internal/payment/application/subscription_repository.go#L239)
- **19 · 本地先取消、远端随后过期时，同渠道重试仍无链接。** 模拟 PayPal 拒绝取消使本地状态成为 CANCELLED，随后远端变为 EXPIRED 且无付款。再次点击 PayPal，返回 `retryable`、原协议 ID、空审批链接。刷新逻辑尝试将本地状态改为 EXPIRED，但状态更新条件使其影响零行，后续未能解绑。建议保持本地取消语义，同时持久化已核实的远端终止证据，检查解绑结果并在同次点击创建新链接。[状态更新条件](../reader-backend/internal/payment/application/subscription_repository.go#L139) · [重试时解绑](../reader-backend/internal/payment/application/vip_checkout.go#L757)

**验证。** 完整六流程和七项前端选包测试通过，生产构建成功。Chrome 实测 20 的 409 只发起一次原单查询并显示成功弹窗；21 的首次 503 保持未选，手选后可继续支付。22 在本地取消、远端仍可审批或核验超时时均保留旧意向，远端确认过期后可换购。使用本机隔离 PostgreSQL 16、临时 Go overlay 与模拟渠道，未操作真实收银台。

## 18–22 上轮复验 · 2026-11-26

基线为 `4dd28a4` 加本轮工作区修改。**19、20 已验收；18、21、22 各有一项已复现的问题。** 隔离 PostgreSQL 16 的完整六流程、前端生产构建和五项选包测试全部通过。以下边界另用临时 Go overlay 和 Chrome 生产构建测试，渠道使用模拟接口。

| 项目 | 本轮复验与修复建议 |
| --- | --- |
| 18 · P1 · 合法首款被误判重复 | 已验证旧版切换记录在两种到账顺序下均只履约一次。但普通退订也可能删除旧子意向。模拟 PayPal 已收首款、本地尚未导入，用户取消续费后重新打开 PayPal 收银台，新协议未付款。此时补录取消前的唯一首款，订单标为重复款，`fulfilled_at` 为空。建议将缺少子意向作为核查线索，结合扣款时间、取消时间、渠道账期及实际重叠收款判断，让有依据的合法首款获得已付权益。[重复款判断](../reader-backend/internal/payment/application/subscription_repository.go#L205) |
| 19 · 已验收 | 渠道已过期时同次点击取得新链接；已审批或已扣款时不返回旧审批链接。已付款但流水未可见时返回确认中，流水可见后完成原单。 |
| 20 · 已验收 | Chrome 从无回跳参数的充值页点击支付，启动返回 409 `checkout_pending`，观察到一次预下单、一次启动、一次原单查询，随后成功弹窗可见。 |
| 21 · P2 · 首次查单失败会静默选中其他套餐 | 正常异步顺序、手选保护和旧卡位变更测试均通过。模拟旧日费意向回跳、默认高亮周费、首次查单返回 503，用户没有选择套餐，仅点击支付，浏览器实际发出卡位 27 的预下单。建议在原套餐未知时维持未选择，直到查证成功或用户明确手选；取消回跳不自动重试时也应保留手选能力。[查单异常分支](../reader-front/src/pages/Recharge.tsx#L318) |
| 22 · P1 · 本地取消可能早于远端审批链接关闭 | 正常关闭、拒绝、超时及重复请求回归通过。通过取消服务和取消确认流程模拟 PayPal 拒绝关闭，远端仍为 `APPROVAL_PENDING`，本地已记 `CANCELLED`。随后换套餐，实际取得了新的 Stripe 审批链接。建议保留终止证据来源，对仅本地取消的协议按原商户再次查证，旧链接仍开放或结果未知时继续保护原单。[本地终态提前返回](../reader-backend/internal/payment/application/vip_plan_switch.go#L148) |

## 18–20 修复复审 · 2026-11-26

基线为 `4dd28a4` 加本轮工作区修改。**18、19 保留待修复，20 已验收。**

- **18 · P1 · 升级前已切换的协议仍可能重复履约。** 新代码在切换时写入 `superseded_vip_intent_id`，新增协议的回归得到两笔实收、一次履约。用相同流程还原旧版本切换后的数据形态（无此标记及切换原因），先完成 Stripe，再处理旧 PayPal 首款，结果为两笔订单、两笔实收、两次履约。建议补上存量协议的识别；能通过历史证据可靠关联的记录可回填，对归属不明且与已有首期重叠的迟到收款先记实收并转复核。须保留正常取消后补录合法账期的能力。[标记读取](../reader-backend/internal/payment/application/subscription_repository.go#L156)
- **19 · P2 · 已获知渠道收款后仍可返回旧审批链接。** 远端 `EXPIRED`、本地 PENDING 的原场景通过，同次点击取得新链接。另仅将模拟渠道设为 `ACTIVE` 并返回最近付款时间，本地仍 PENDING，重试实际返回 `awaiting_approval` 和原链接。建议仅在核实仍待审批时复用链接；已审批或已有付款则按原协议进入确认并核验流水，查询失败或证据不一致时保留意向并返回待确认状态。[远端状态处理](../reader-backend/internal/payment/application/vip_checkout.go#L752)
- **20 · 已验收。** 从无回跳参数的充值页实际点击 PayPal，模拟启动返回 HTTP 409 `checkout_pending`，随后原单查单返回 `completed`。Chrome 显示成功弹窗，观察到一次预下单、一次启动、一次对原意向的查询。[前端异常分支](../reader-front/src/pages/Recharge.tsx#L407)

**验证范围。** Reader 前端生产构建通过。隔离 PostgreSQL 16 中，18、19 原始专项通过；上述边界用临时 Go overlay 验证。整套六流程本次未全绿，另见 22 换套餐用例的订阅冲突及旧 Stripe 报价用例的模板外键错误。渠道均为模拟。

## VIP 回跳后续付与换套餐 · 待验收修复项

**定位依据。** 线上 Reader `4dd28a4` 的沙箱记录显示，旧意向对应卡位 26、商品 5（日费 $0.99），状态为 Pending，当前渠道为 PayPal，切换目标为 Stripe。PayPal 本地协议为 PENDING，无付款记录。新加坡时间 2026-11-26 22:26:16，卡位 27、商品 6（周费 $4.99）在预下单阶段被拒；22:26:23，卡位 26 获准复用旧意向。审计能确认两次请求的套餐不同，无法判断用户是否主动选择了周费。

### 21 · Reader H5 · 带 purchase 回跳时恢复原套餐

**修复要求。** 根据当前用户的服务端意向恢复待付款套餐。原卡位与商品仍匹配当前模板时，选中该卡位，再次点击支付沿用原意向。模板与查单异步返回期间，应确保点击提交的套餐与页面展示一致。用户手动改选后，后续查单响应不得把选择改回。若原卡位已撤下或商品已替换，提示用户重新选择，避免静默提交默认套餐。

**验收。** 从未付款的日费收银台返回，即使模板默认高亮周费，再次点击仍提交卡位 26、商品 5，并复用原意向。分别覆盖模板先返回和查单先返回。手动改选周费后，提交卡位 27、商品 6，进入 22 的换套餐流程。

### 22 · Reader 支付后端与 H5 · 一次点击安全替换未付款 VIP 套餐

**修复要求。** 用户主动改选周费时，在创建新意向前核实旧意向关联的 PayPal 协议、Stripe Checkout 和在途渠道切换。确认旧尝试已关闭、无收款且无在途激活或扣款后，释放日费占位，并在同一次支付操作中取得周费收银台链接。若旧链接仍可审批、关闭结果不明，或发现款项正在确认，保留原单，返回可区分的原因和原套餐信息。H5 使用现有轻提示，例如“日费 VIP 支付仍在确认，暂时无法切换到周费”。已确认付款则展示实际订阅状态，沿用已有订阅规则。

**验收。**

- 按本例构造日费 PayPal 待审批且已发起向 Stripe 切换的旧意向。确认所有旧尝试安全终止后，点击周费一次即可进入周费收银台；重复点击或重放同一请求只产生一个有效周费意向。
- 模拟 PayPal 拒绝关闭且旧链接仍可审批、渠道查证超时、旧付款并发到账。逐项核对原意向可追踪、新渠道创建次数为零，页面提示与查证结果一致。后续确认安全终止后可重试换购。
- 覆盖旧渠道为 Stripe 的对称场景。已有有效付费订阅继续受互斥保护；若已被替换的协议后来收到款项，按 18 验证实收留痕和重复账期处置。

## 离开收银台后再次支付 · 2026-11-26

复核至 `4dd28a4`。在隔离 PostgreSQL 16 和模拟渠道上，金币、VIP 分别以 PayPal、Stripe 发起支付，使用原请求键重新预下单并再次启动原渠道。四种组合均复用原意向，返回相同的非空审批链接。仓库六流程与切换回归通过。以下两项已用临时 Go overlay 复现，一项来自前端控制流审查。本次没有操作真实渠道或浏览器收银台。

### 18 · P1 · 切换到 Stripe 付款后，旧 PayPal 协议的首款仍会再次履约

模拟 PayPal 拒绝取消尚待审批的协议，系统再次查到待审批状态后允许切至 Stripe。先结清 Stripe 月费，再向已被替换的 PayPal 协议送入同一账期的已验证首款。Webhook 将旧协议重新排入取消队列，随后仍走正常续费履约。数据库最终有两笔已履约月费订单。补取消只能处理后续续订，已收的第二笔款仍需处置。[切换放行](../reader-backend/internal/payment/application/vip_checkout_switch.go#L293) · [迟到扣款](../reader-backend/internal/payment/application/webhook_service.go#L236)

建议持久保存被替换协议与原购买意向的关系。旧协议迟到收款应保留实收记录，并与原意向已结算的账期比对；若重复则进入退款或人工复核。验收覆盖两渠道到账顺序互换、Webhook 重投和对账补录，断言同一期只正常履约一次，另一笔实收可追踪。

### 19 · P2 · PayPal 已过期而本地仍待审批时，重复点击返回失效链接

仅将模拟 PayPal 协议改为 `EXPIRED`，保留本地待审批状态。查单后按原请求键重试，仍返回 `awaiting_approval` 和原链接。现有回归先将本地数据库改为过期，跳过了渠道先过期、通知尚未同步的窗口。[同渠道重启](../reader-backend/internal/payment/application/vip_checkout.go#L650) · [已有回归](../reader-backend/internal/payment/application/vip_checkout_review_test.go#L216)

建议重试时按原商户确认旧协议。仍待审批则复用；已确认终止且无付款、无激活在途时，同次点击换取新链接；状态不明时保留原意向。回归应只改变模拟渠道状态，验证本地无需预先同步。

### 20 · P2 · 启动返回 checkout_pending 时可能没有查单请求

前端在启动调用成功返回后才设置当前查单意向。从没有回跳参数的充值页进入，若启动返回 409 `checkout_pending`，异常分支只刷新套餐并提示错误。已保存的草稿供下次点击使用，当前页不会据此自动查询该订单。[启动顺序](../reader-front/src/pages/Recharge.tsx#L363) · [异常分支](../reader-front/src/pages/Recharge.tsx#L396)

建议在已知意向的启动请求返回 `checkout_pending` 时启动原单查询，保留成功分支现有的跳转顺序。验收时让启动返回 409，随后原单变为已完成，断言 H5 自动取得结果且没有再次创建支付。无需新增重查按钮。

## PayPal 回跳与 Reader 充值页复审 · 2026-11-26

评审基线为 `285569b` 加本轮工作区修复。成功弹窗已可独立于套餐加载显示，临时查单失败会自动重试，PayPal 单次购买的两种可恢复确认码也已重新进入主动查证。15 已通过浏览器复验，本次回跳专项无待修复项。16 按产品决定忽略，17 调整为可选的体验优化。

### 15 · 已验收（原 P2）· 已登录买家回跳后，首次查单仍等待钱包刷新

**修复前。** H5 恢复已有会话时，用户身份已经由 profile 确认，随后仍等待余额、交易历史和订阅状态全部返回，才设置 `authReady`。充值页以此标志启动查单。本机 Chrome 使用当前生产构建和模拟 API，将钱包历史挂起 10 秒，即使查单接口随时可返回 `completed`，挂起期间仍收到 0 次查单。自回跳导航开始，首次查单在 11.04 秒，成功弹窗在 11.10 秒可见。这条路径能产生用户反馈的等待，原沙箱付款当时的具体耗时来源尚未核实。[会话初始化](../reader-front/src/App.tsx#L322) · [钱包刷新](../reader-front/src/App.tsx#L193) · [回跳查单](../reader-front/src/pages/Recharge.tsx#L222)

**修复验收。** 已有会话通过身份校验后，钱包在后台刷新。当前 Reader 生产构建通过。本机 Chrome 使用模拟 API，分别挂起余额、交易历史、订阅状态请求，三次均在释放慢请求前完成首次查单并展示成功弹窗。从回跳导航到弹窗可见分别为 1057、96、90 毫秒，每次只查单一次。本轮未发起真实 PayPal 沙箱付款。[会话恢复](../reader-front/src/App.tsx#L350)

### 16 · 本次忽略 · H5 移除了订阅用户的自助退订入口

本轮删除充值页原有的协议列表和操作按钮。当前 Reader 中 `cancelSubscription`、`openStripeVIPPortal` 只剩 API 定义。已有 PayPal 或 Stripe 订阅的用户无法在 H5 按协议请求取消续订，支持 Stripe Portal 的订阅也失去了更新付款方式的入口。[充值页](../reader-front/src/pages/Recharge.tsx#L515) · [Stripe 管理接口](../reader-front/src/utils/api.ts#L501) · [退订接口](../reader-front/src/utils/api.ts#L574)

取消订阅后续迁移至其他入口，本次按产品决定忽略 16。

### 17 · 可选体验优化 · 保留 75 秒自动查单窗口

若查单持续返回 `confirming` 或 `awaiting_approval`，75 秒窗口结束后停止安排下一次自动查询是合理的。停止的是当前页的查询，服务端仍可通过支付通知和对账处理订单。页面再次可见时会重新查单；若用户一直停留在前台，截止后才完成的订单不会自动弹出成功提示。[轮询截止及页面可见性处理](../reader-front/src/pages/Recharge.tsx#L276)

支付页保持简洁，无需增加重查按钮。若希望解释等待结束，可仅在超时且订单仍待确认时显示一次轻提示：“订单仍待确认，无需重复付款。”此项不作为发布阻断。

**验证记录。** Reader 前端生产构建通过，Go 回跳、可恢复确认码、限频、调用方取消及 worker 唤醒共六项定向测试通过。浏览器模拟另测得：套餐请求延迟 4 秒时弹窗约 72 毫秒可见；首次查单返回 503 后约 1.5 秒自动重查并弹窗；首次返回 404 后在随后 3.5 秒内仅查一次。上述为本机模拟 API 耗时，本轮未执行 PayPal 沙箱付款。

## 14 修复验收 · 2026-11-23

**14 已关闭。** 复核修复提交 `bc9600c`，在包含该提交的 `3742ecc` 上执行回归，本次复核范围内未发现新的缺陷。03、13 按此前决定忽略。

- **旧报价可退出。** 月费卡位失效后，查单、重放原请求键、直接改选年费均能释放从未发送的 PayPal 占位。旧意向返回 `closed`，没有审批链接，也没有发起月费创建请求；新的年费意向取得对应 Plan 的审批链接。[释放逻辑](../reader-backend/internal/payment/application/vip_checkout.go#L275)
- **在途请求受保护。** 子记录受另一事务锁定、创建租约有效或正在切换渠道时，改购仍受互斥保护。PayPal 已创建协议但响应丢失时，重试复用原请求键并找回同一协议。[六个回归场景](../reader-backend/internal/payment/application/vip_checkout_rereview_test.go#L266)
- **验证范围。** 在独立 PostgreSQL 16 测试库禁用 Go 测试缓存执行 `TestSixH5PurchaseFlowsEndToEnd`，整套通过，含 14 的六个专项子用例。渠道使用模拟适配器，未发起真实支付。

## 10–12 再次复审 · 2026-11-23

评审基线为 `78d2ff1` 加本次工作区的未提交修复，数据库版本 `2026111929`。**10–12 的原始复现均已通过，当轮遗留的 14 已在后续复验关闭。** 03、13 按业务决定忽略。

| 原编号 | 本轮验证结果 |
| --- | --- |
| 10 · 首发前停用绑定 | PayPal、Stripe 的绑定被停用、验证失效或 Plan / Price 被替换时，首次创建调用均为零，可改走另一可售渠道。已发送但响应丢失的请求仍能按原对象恢复。国家范围收紧见 13（已忽略）。 |
| 11 · 切换中途停用目标 | 两个切换方向均通过。旧对象状态未确认时保持互斥；确认终止后释放旧占位，停用目标的创建次数为零，另一可售套餐可取得审批链接。 |
| 12 · Stripe 旧卡位占位 | 查单和重选套餐均能释放从未发送的 Stripe 子意向。旧请求键重放返回 closed。按“先释放”和“先领取”两种顺序验证，已领取的原尝试保留互斥，并可完成原请求。PayPal 对应场景见 14。 |

### 13 · 已忽略 · Stripe 首次发送前收紧销售国家后，已撤出的国家仍可获得 VIP

**复现。** 无历史账单国家的读者预留月费 VIP，当时绑定只允许美国。在首次发送前将允许国家改为仅加拿大，保持商户、商品和 Price 有效。刷新购买状态得到 `awaiting_approval`。再以美国账单地址的模拟已付 Invoice 调用正式结算逻辑，Reader 返回 `access=true`。首发检查在预期国家为空时放行，随后账期准入使用预留时保存的美国名单。[首发绑定检查](../reader-backend/internal/payment/application/stripe_vip_repository.go#L182) · [账期市场准入](../reader-backend/internal/payment/application/stripe_vip_settlement.go#L267)

**历史建议（已忽略）。** 首次领取时在同一事务校验快照允许的国家仍全部落在当前绑定范围内。若有国家被移除，让未发意向进入可重新报价的状态。对已领取请求继续按原快照查证。回归应覆盖无历史国家、已有历史国家、仅调整名单顺序以及仅扩展市场，断言收紧市场后的旧意向首次创建次数为零，重新报价后的美国账单不能按旧名单自动获权。

### 14 · 已验收（原 P2）· PayPal 从未发送的 VIP 子意向在卡位失效后仍占住购买资格

**复现。** 预留 PayPal 月费子意向后移走模板卡位，确认数据库中 `submitted_at` 和 `subscription_id` 均为空。继续 PayPal 或切换 Stripe 都提示重新选套餐；实际改选有效年费又返回订阅冲突。此时没有可供读者取消的远端协议。释放旧报价的函数仅处理 Stripe，随后套餐替换逻辑因父意向仍关联 PayPal 而拒绝新购。[旧意向释放条件](../reader-backend/internal/payment/application/vip_checkout.go#L269) · [套餐替换](../reader-backend/internal/payment/application/vip_checkout.go#L345)

**修复建议与验收。** 为 PayPal 补上同一事务内的退出路径：锁定父子记录并核对报价已失效、未首次提交、无外部协议、无有效创建租约或在途切换，然后释放 PayPal 的用户级唯一占位并将父意向置为 Expired。覆盖查单、旧请求键重放及直接重选；分别验证释放先完成时能取得新套餐审批链接，首次发送先完成时只恢复原尝试。[PayPal 首发领取](../reader-backend/internal/payment/application/vip_checkout_switch.go#L123) · [用户占位索引](../cms-backend/db/migrations/2026111927.sql#L39)

**验证范围。** 本机隔离 PostgreSQL 上运行 `TestSixH5PurchaseFlowsEndToEnd` 及全部子用例，通过。另以临时 Go overlay 运行上述两组边界场景，核对原始返回值、发送标记和阅读权益。首发与释放的两种顺序由仓库用例分别构造。本轮使用模拟渠道，未发起真实订阅或扣款。[本轮回归用例](../reader-backend/internal/payment/application/vip_checkout_rereview_test.go)

## 修复复审 · 78d2ff1

复审范围为 01、02、04–09，数据库版本为 `2026111929`。03 按业务决定忽略。**8 项原始触发场景均通过回归。扩大到首次发送前及切换过程中变更配置后，发现 3 项仍需修复的 P2。** 原 01 的双协议风险和原 02 的低额付款授予整期 VIP，在本次对应测试中均已阻断。

| 原编号 | 本次验收结果 |
| --- | --- |
| 01 渠道切换 | 取消失败保留原协议；创建结果不明时查原对象；买家审批抢先完成时阻止切换。目标渠道中途停用的恢复见 11。 |
| 02 低额续费 | 99.99 美元年费只实收 0.01 美元时保留实收和复核原因，未发 VIP 或支付转化；另笔足额迟到付款可以恢复权益。 |
| 04 模板报价 | 旧卡位移出有效模板后不能再启动新协议。已预留但尚未发给渠道的旧意向如何退出，见 12。 |
| 05 PayPal 创建重试 | 请求未到达、创建成功但响应丢失两种情况均按原账户和请求键恢复。超过本地重试窗口后进入复核。 |
| 06 过期意向 | 两渠道启动都校验父意向，换套餐与启动并发仍只保留一个待付意向。 |
| 07 退款与权益 | PayPal、Stripe 首期全退后历史显示 refunded。Stripe 仅退续期时首购仍显示 completed；另有已付账期覆盖当前时间时仍可阅读。 |
| 08 Stripe 首次发送 | 账户暂停新增订阅后，普通查单不能发起首次创建；商品级绑定停用的缺口见 10。 |
| 09 冲突审计 | 已有 VIP 再开套餐时，HTTP 错误和持久化决策均归入订阅冲突。 |

### 10 · P2 · 商品渠道绑定停用后，尚未发送的 VIP 尝试仍能创建审批链接

**触发与实测。** 保持商户和商品发布有效，先预留 VIP 子意向，在首次调用渠道前停用该商品对应的渠道绑定。刷新 Stripe 购买状态，创建了 1 个新 Session；在 PayPal 上再次启动，同样发出 1 次创建请求。两侧都返回 `awaiting_approval`。运营已经停卖该商品与渠道组合，读者仍可进入审批。

**修复建议。** 在 `claimUnifiedPayPalVIP` 和 `claimStripeVIP` 领取首次创建任务的事务里，按子意向保存的商品、商户、环境和渠道读取绑定。要求绑定已启用且验证通过，PayPal Plan 或 Stripe Product / Price 与预留快照一致。校验与写入 `submitted_at` 或 `first_sent_at` 在同一事务完成，并与 CMS 对绑定的更新互斥。停用先提交时，返回渠道不可售，保留读者选择其他有效渠道的入口。已有发送标记的尝试继续按原账户、请求键和外部对象恢复。[PayPal 首发](../reader-backend/internal/payment/application/vip_checkout_switch.go#L139) · [Stripe 首发](../reader-backend/internal/payment/application/stripe_vip_repository.go#L188)

**验收。** 双渠道分别覆盖绑定停用、验证失效和外部 Plan / Price 被替换。先撤销再领取时，刷新、主动启动和后台补偿均应产生零次首次创建；先领取且渠道响应丢失时，恢复应定位原请求，最终至多得到一个可审批对象。

### 11 · P2 · 切换途中停用目标商品绑定，旧协议已取消仍无法解锁

**触发与实测。** PayPal 切 Stripe 时注入取消响应超时，随后模拟 PayPal 已确认 CANCELLED，再仅停用月费商品的 Stripe 绑定。重试 Stripe 返回渠道停用，重试 PayPal 返回确认中；改买仍可售的年费套餐也报订阅冲突。父意向仍记录 `switch_to_provider=stripe`。

**修复建议。** 两个启动入口先读取父意向的 `switch_to_provider`。首次申请切换时检查目标可售性，恢复同一次切换时先按原商户核验旧对象。确认旧对象已终止且无实收后，在同一事务校验父意向仍指向原子意向，完成解绑并清除切换标记。随后单独判断目标是否可售；若已停用，返回可重选状态。查证超时保留切换进度；若审批或实收已成立，沿原协议继续处理。[渠道切换](../reader-backend/internal/payment/application/vip_checkout_switch.go#L31) · [条件解绑](../reader-backend/internal/payment/application/vip_checkout_switch.go#L82)

**验收。** 对两个切换方向分别模拟取消响应丢失、目标绑定停用、旧对象随后确认终止。断言停用渠道创建次数为零，切换标记已清除，并从重选另一可售套餐走到取得审批链接。同时验证旧对象查证超时或审批抢先完成时仍只有一个可能收款的协议。

### 12 · P2 · 旧卡位失效时，未发送的 Stripe 子意向可长期占住 VIP 购买资格

**触发与实测。** 在预留 Stripe 子意向后、首次发送前把月费卡位移出读者模板。此时刷新仍为 `confirming`；切 PayPal 提示报价已变、应重选套餐；实际选择有效年费套餐又返回订阅冲突。该场景没有远端 Session，读者没有可取消的协议。

**修复建议。** 在查单发现报价失效、以及 `reserveVIPCheckoutIntentTx` 处理不同套餐的待付意向时，共用同一段事务逻辑。锁定父意向和关联的 Stripe 子意向，核实父意向仍为 Pending、子意向仍为 creating、`first_sent_at` / `checkout_ref` / `subscription_ref` 均为空。满足这些条件后，将子意向标为 closed、父意向标为 Expired，保留原请求键与历史关联。重选套餐可以在该事务内继续预留新意向。若 worker 已取得首次发送资格，转入原对象查证，避免重复开通。[Stripe 领取](../reader-backend/internal/payment/application/stripe_vip_repository.go#L181) · [套餐替换](../reader-backend/internal/payment/application/vip_checkout.go#L251)

**验收。** 月费卡位移出模板后，旧请求键重放返回已关闭，新请求键选择有效年费能取得审批链接，旧月费创建次数为零。并发执行释放旧意向和 worker 领取，分别验证释放先完成与领取先完成两种顺序，最终至多保留一个可能收款的尝试。

**本次验证。** 在本机隔离 PostgreSQL 上禁用测试缓存，运行 `TestSixH5PurchaseFlowsEndToEnd` 及其全部子用例，通过。另以临时 Go overlay 增加四个故障场景，分别观察到 10 的两渠道结果以及 11、12 的阻断结果。验证使用模拟渠道，未产生真实订阅或扣款。[仓库回归用例](../reader-backend/internal/payment/application/vip_checkout_review_test.go)

---

## 首轮评审记录 · ee4dabc

评审基线为 `codex/payment-domain@ee4dabc`、数据库迁移至 `2026111928`。沿 Reader H5 统一下单入口，核对 PayPal / Stripe 实收、退款、订阅账期、取消、钱包和章节授权，再追查 CMS 命令与后台补偿。金额按当前 USD 商品口径。以下编号针对这次评审，旧报告的待办没有直接计入。

**优先修复 VIP 渠道切换和 PayPal 账期准入。** 隔离测试中，PayPal 取消报错后 Stripe 仍给出了审批链接；标价 99.99 美元的年费协议，在有对应账期快照时，0.01 美元的实收也能开通 VIP。前者有双协议风险，后者已实际读到有效权益。另有一项有部署前提的 P1：同库同用户的沙箱金币进入正式钱包。以下共列 3 项 P1、6 项 P2；“复现”指本机 PostgreSQL 加模拟渠道，“静态”指已追踪调用链，尚未执行该故障时序。

## 1. 模块与流程

| 模块 | PayPal | Stripe | 评审结果 |
| --- | --- | --- | --- |
| 商户、商品和报价 | 选择环境、主账户、商品绑定；VIP 使用绑定的 Plan | 绑定 Product、Price、销售模式及市场证据 | 金币与 VIP 都有双渠道入口。VIP 卡位对读者模板的约束有缺口，见 04。 |
| 金币购买 | 建意向、Orders 审批、核验 capture，事务写实收、订单与钱包 | Checkout Session 关联尝试，核验 Charge、商品及金额，事务写入付费币、赠币 | 两渠道均已有发币路径；环境边界见 03。买家回跳本身不触发无凭证发币。[PayPal 履约](../reader-backend/internal/payment/application/checkout.go#L374) · [Stripe 履约](../reader-backend/internal/payment/application/stripe_settlement.go#L261) |
| 金币退款 | CMS 按原款预留可退金额，capture 和订阅 sale 使用不同退款接口；按退款 ID 累加 | 按 Charge 和退款 ID 归集，核对实际履约资金，累计全退后扣币 | 正常部分退款保留权益；全退按付费币、赠币分别回收可用余额，记录已消费缺口。请求未确认时保留预留，后续按原请求查证。[退款命令](../cms-backend/internal/billing/refund_command.go#L114) · [PayPal 退款](../reader-backend/internal/payment/paypal/refund.go#L34) · [Stripe 回收](../reader-backend/internal/payment/aftercare/loss.go#L123) |
| VIP 开通、换渠道、失败重试 | PayPal 子意向保存审批链接和协议，核验扣费后履约 | Stripe 子意向绑定 Session 和协议，已付 Invoice 对应独立订单 | 统一入口与子意向状态不总是一致。切换、恢复和终态问题见 01、05、06、08。 |
| 续费成功 | 验证协议、Plan、交易及周期后写入订单，更新协议截止时间 | 对 Invoice Payment 分配、实收、账期、重复区间及复核状态进行准入，逐期履约 | PayPal 账期快照分支可漏掉金额下限，见 02。Stripe 重复账单按已准入账期处理。[PayPal 续费](../reader-backend/internal/payment/application/subscription_service.go#L187) · [Stripe 账期](../reader-backend/internal/payment/application/stripe_vip_settlement.go#L20) |
| 续费失败、补扣 | 失败通知复查协议，记录失败时间、安排补查；晚于失败的有效付款清除失败标记 | 失败及需买家操作的 Invoice 触发查证；past_due / unpaid 映射为 SUSPENDED，未付账单不能延长权益 | 已付未到期的账期可继续阅读。无后续付款时，到期由阅读谓词自然收权；补扣须经过同一账期准入。[PayPal 失败分派](../reader-backend/internal/payment/application/webhook_service.go#L213) · [Stripe 状态](../reader-backend/internal/payment/application/stripe_vip_repository.go#L301) · [阅读授权](../reader-backend/internal/novel/repository.go#L38) |
| 退订、当期退款 | 正常退订进入 CANCEL_PENDING，由 worker 发送并查证；当期退款关联原 sale | 排队设置期末取消；当期全退按原 Invoice 订单重算剩余已付截止时间 | 正常取消保留已付当期；全额退回该期后重新判定当前覆盖。换渠道走另一条取消路径，见 01。购买历史的退款状态见 07。[取消入口](../reader-backend/internal/payment/application/subscription_service.go#L169) · [Stripe 取消](../reader-backend/internal/payment/application/stripe_vip_service.go#L316) |
| 对账、风控、争议及转化 | Inbox、交易补拉、原流水、退款和争议关联原款 | 事件回补、资金分配、风险暂缓及本金损失关联原款 | 支付结算通过履约记录连接钱包、VIP 与转化；对账和风控另有 worker。09 影响运营看到的阻断原因。真实商户结算单、银行流水和外部转化投递不在本次本机验证内。[事件恢复](../reader-backend/internal/payment/application/event_recovery.go) · [风险暂缓](../reader-backend/internal/payment/aftercare/fulfillment.go) · [结算](../reader-backend/internal/payment/settlement) |

## 2. VIP 开通与渠道切换

### 01 · P1 · 原渠道关闭结果不确定时仍开放另一渠道（PayPal 方向已复现）

PayPal → Stripe：原协议仍待审批，切换代码先查状态，再调用取消，但丢弃取消错误；随后把本地协议改为 CANCELLED，解除统一意向关联并删除 PayPal 子意向。将模拟取消改成返回错误后，Stripe 仍返回 `awaiting_approval`。如果旧审批链接仍可完成，读者可能建立两份续费协议。此次验证到第二个审批入口开放，未发生真实双扣。[切换实现](../reader-backend/internal/payment/application/vip_checkout.go#L532)

两个方向还存在“创建请求已发出，本地尚无外部 ID”的窗口。PayPal 无协议号时直接删除子意向；Stripe 无 Session ID 时跳过远端关闭，直接标关闭并解除关联。两个字段都可能因写入结果未返回而为空。修复应持久化切换进度，按原商户及幂等键核验旧对象，以条件更新完成解绑；在途或查证失败时保留可恢复记录。验收应覆盖取消报错、创建响应丢失以及另一标签页同时完成审批。[Stripe → PayPal](../reader-backend/internal/payment/application/vip_checkout.go#L473) · [PayPal → Stripe](../reader-backend/internal/payment/application/vip_checkout.go#L517)

### 05 · P2 · PayPal 创建失败后，同渠道重试无法恢复（已复现）

原请求先提交并关联 PayPal 子意向；若创建失败且未保存审批链接，再次发起只因已有子意向便直接返回。模拟创建超时后，同一购买再次发起得到 `confirming`、审批链接为空。若渠道根本没建单，查询也没有可用协议号；若渠道已建单，则需要找回原对象。建议按原意向和已发送时间恢复，在安全重试期内复用原请求键，超期转入可处理的复核状态。验收分别模拟远端未创建和远端已创建但响应丢失。[重复发起及提交](../reader-backend/internal/payment/application/vip_checkout.go#L354) · [购买状态](../reader-backend/internal/payment/application/vip_checkout.go#L90)

### 06 · P2 · 已过期的 VIP 意向还能创建渠道协议（已复现）

读者先选年费，再选月费，未提交的年费意向被标记 Expired。从旧标签页发起年费 PayPal，数据库中同一意向仍为 Expired，却新增了渠道协议号。两个渠道的启动代码只提前处理 Completed，预留子意向时也未限定父意向为 Pending。旧报价可继续占用订阅资格，后续履约更新又只匹配 Pending。应在持锁的预留事务中检查意向终态并校验更新行数，旧页面显示报价已变更。验收应覆盖旧标签页、同请求键重放与并发换套餐。[意向过期](../reader-backend/internal/payment/application/vip_checkout.go#L183) · [PayPal 启动](../reader-backend/internal/payment/application/vip_checkout.go#L341) · [Stripe 启动](../reader-backend/internal/payment/application/vip_checkout.go#L422)

### 08 · P2 · Stripe VIP 查单能绕过 worker 的首次发送门禁（静态）

统一购买查询对 Pending Stripe 意向直接调用同步。若子意向处于 creating，尚无 Session 且未置 lookup-only，同步会创建远端 Checkout。正常领取任务会检查允许新增、写入首次发送时间与重试截止时间，查询分支直接读取子意向，跳过这些步骤。可触发时序是预留事务提交后进程中断、商户暂停新订阅、用户再刷新购买状态。修复应让所有可能首次发出创建请求的入口共用同一领取及门禁；已知 ID 仍可查证。验收断言上述时序下创建调用次数为零。[查询入口](../reader-backend/internal/payment/application/vip_checkout.go#L294) · [同步创建](../reader-backend/internal/payment/application/stripe_vip_service.go#L151) · [正常领取](../reader-backend/internal/payment/application/stripe_vip_repository.go#L180)

## 3. 续费账期及 VIP 阅读权益

### 02 · P1 · PayPal 通过账期快照恢复时，低于套餐价格的交易可发放完整 VIP（已复现）

直接从协议详情取周期时会检查实收至少达到协议价格；失败后按协议、付款时间、币种和**实际支付金额**找快照，却不再比较套餐价格。取消任务可以保存正金额快照。复现使用年费 9999 美分、实收 1 美分、相同时间和币种的账期快照，经交易处理后，Reader 返回有效 VIP。触发前提是存在低额真实交易及匹配快照，现有证据不能说明线上发生过。[快照写入](../reader-backend/internal/payment/application/reconcile.go#L155) · [快照条件](../reader-backend/internal/payment/application/billing_period.go#L14) · [续费准入](../reader-backend/internal/payment/application/subscription_service.go#L199)

应统一各证据分支的金额、币种和协议条款判断，快照只证明周期。低额交易应保存实收并留待复核，保持原权益截止时间。验收同时检查新账期、阅读权限及履约转化，并保留足额迟到交易的补录能力。

## 4. 金币钱包、退款及环境

### 03 · P1（同库同用户时）· 沙箱充值和退款会改变正式可用金币（已复现）

订单、VIP 和整本授权区分环境，钱包以 user_id 为唯一主键，发币、扣币和查余额使用同一行。测试中用 PayPal 沙箱充值 100 付费币和 20 赠币，将同一 Reader 用户切到 production，读到的余额仍为 100 + 20。Stripe 发币复用同一钱包；沙箱退款也可能扣掉随后在正式环境获得的同类余额。若生产与沙箱严格物理分库且没有数据混用，线上触发条件不成立。[钱包结构](../cms-backend/db/schema.sql#L128) · [共同发币](../reader-backend/internal/wallet/repository.go#L252) · [按钱包扣回](../reader-backend/internal/payment/aftercare/loss.go#L80)

需要明确并验证部署边界。支持混库时，余额、流水、章节扣费和解锁都应有一致的环境归属；依赖物理分库时，应在服务启动或部署时验证互斥。验收同一用户在两环境分别充值、消费和退款，对照两侧余额、流水、解锁与 shortfall。

## 5. 商品目录与售卖模板

### 04 · P2 · 已移出读者模板的 VIP 卡位仍可下单（已复现）

把月费卡位从读者默认模板 600 移至模板 799，保留商品发布与双渠道绑定。用旧卡位调用统一预下单和 PayPal 启动，仍取得 `awaiting_approval`。按卡位解析只查发布和绑定，VIP 预留直接保存该模板；单次购买落单另有当前模板检查。结果是读者可能继续购买运营已从其模板撤下的报价。应在 VIP 预留事务中核对读者有效模板、卡位和商品，并测试模板切换前后的新请求与已提交协议。[商品解析](../reader-backend/internal/payment/application/product_catalog.go#L98) · [VIP 预留](../reader-backend/internal/payment/application/vip_checkout.go#L235) · [单次购买校验](../reader-backend/internal/payment/application/checkout.go#L117)

## 6. Reader 售后展示与运营审计

### 07 · P2 · VIP 首期全退后，购买历史仍显示已完成（Stripe 已复现）

对唯一已付 Stripe 账期完成全额退款，阅读权限正确变为 false，最近购买记录仍为 `completed`。统一投影遇到 Completed 直接返回，覆盖了底层按原订单给出的 refunded；PayPal 共用同一投影。应关联首付订单和退款状态，另行展示协议续费状态；后续账期单独记录。验收覆盖首期全退、仅退后续一期、有其他有效账期三种情况，分别核对历史与权限。[统一投影](../reader-backend/internal/payment/application/vip_checkout.go#L83) · [Stripe 原订单投影](../reader-backend/internal/payment/application/stripe_vip_repository.go#L315) · [H5 历史](../reader-front/src/pages/Recharge.tsx#L635)

### 09 · P2 · VIP 冲突被记录成整本重复购买（静态）

统一 VIP 互斥遇到已存在的协议或在途意向时返回 `ErrCheckoutConflict`；该错误的读者文案指向“this book”，决策审计映射到 `whole_book_already_reserved_or_owned`。用户和运营都可能查错订单，并且 VIP 冲突统计少计。使用已有的订阅冲突错误和对应决策原因，并在统一购买 HTTP 错误映射中补上该分支；以已有有效 VIP 再开另一套餐为例，断言 API 文案及持久化决策均指向订阅。[互斥返回](../reader-backend/internal/payment/application/vip_checkout.go#L174) · [错误文案](../reader-backend/internal/payment/application/checkout_intent.go#L18) · [审计映射](../reader-backend/internal/payment/application/checkout_decision.go#L80)

## 7. 验证口径

- 原仓库回归使用本机隔离测试库、禁用 Go 测试缓存。Reader 执行 `go test -p 2 -count=1 ./internal/payment/... ./internal/wallet ./internal/novel`，11 个包通过，包含六流程、退款、续费失败、重放、VIP 生命周期及阅读授权。CMS 在本次专用空库执行 `go test -count=1 -run '^TestPaymentCenterRefundAuthorizationIntegration$' ./internal/billing`，通过。其中 CMS 只运行了指定的退款集成用例。
- 专项故障验证在独立 PostgreSQL schema 中运行，基于仓库六流程夹具，用临时 Go overlay 注入渠道故障与边界数据。01 至 07 的观测值已写入各项；08、09 为静态结论。未向真实 PayPal / Stripe 发起付款、退款或退订，也未使用线上数据。
- 六流程测试通过应用服务驱动模拟渠道，VIP 部分直接调用账期结算；它验证本地订单和权益，不覆盖真实浏览器、网络回跳、商户审批、账单重试计划或真实资金结算。[六流程夹具](../reader-backend/internal/payment/application/six_flows_integration_test.go#L171) · [续费失败及迟到事件](../reader-backend/internal/payment/application/payment_recovery_integration_test.go#L243) · [Stripe VIP 生命周期](../reader-backend/internal/payment/application/stripe_vip_test.go#L13)
