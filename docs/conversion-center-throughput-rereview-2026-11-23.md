# 回传中心修复与整链路吞吐复审

## 最新复测 · 2026-11-23 · schema 2026111934

**上轮“迟到的凭据错误暂停健康 Pixel”P2 已关闭。本次修复范围内未发现新的阻塞问题。** 审查对象为 `75ae7b2` 加当前工作区的 1934 改动。目标现在记录决定当前凭据健康状态的批次 ID，收到完整成功或明确凭据错误时，按同一凭据的批次先后更新暂停状态。旧批次仍保留自己的回执及成员结果。[回执事务](../conversion-backend/internal/delivery/batch_receipt.go#L95) · [轮换凭据](../conversion-backend/internal/delivery/configuration.go#L88)

| 隔离 PostgreSQL 16 实测 | 结果 |
| --- | --- |
| 同一凭据，A 失租，较新批次 B 已经发出 | A 的旧错误先落库、B 的成功先落库两种顺序均通过。第 3 条投递成功，A 保持未知且仅尝试一次，迟到的 190/463 错误可查。[原场景回归](../conversion-backend/internal/delivery/batch_test.go#L145) |
| B 的最新回执确认为凭据错误，A 的旧成功随后落库 | Pixel 保持暂停，第 3 条无法领取。凭据轮换后旧版本错误不影响新凭据的用例也通过。[反向与轮换用例](../conversion-backend/internal/delivery/batch_test.go#L185) |
| 两个事务同时写同一 Pixel 的 A 错误与 B 成功 | 通过临时 Go test overlay 运行 12 组并发写入。每组第 3 条均可领取并按模拟成功回执定稿，最终均为 1 条未知、2 条成功。 |
| 历史库升级到 1934 | CMS 正式迁移入口恢复了 1933 错停且已有更新成功批次的 Pixel，最新证据仍为凭据错误的 Pixel 继续暂停。[回填 SQL](../cms-backend/db/migrations/2026111934.sql#L6) · [升级用例](../cms-backend/internal/db/migration_test.go#L562) |

本轮 Conversion 全模块 `go test -race -count=1 -p 1 ./...` 与 CMS 数据库升级用例均通过，未调用真实 Meta。下文的 Reader 耗时来自 1933 复测。双实例 Reader 与 Partner 混合峰值、30 分钟稳态、1.5 倍峰值与重启容量仍待按实际流量分布验证。

## 上轮复测 · 2026-11-23 · `75ae7b2`

**上轮四项问题的原始场景均通过复测。单 Pixel、批量 100 的 Reader 吞吐也达到方案目标。** 故障恢复仍有一项 P2：同一凭据已有更新的成功回执时，旧批次迟到的错误可以暂停该 Pixel。建议修复这个边界后再扩大生产批量。Linux 配置及发布说明已统一为从批量 1 起步。

### P2 · 旧批次的迟到错误会覆盖同一凭据的新成功

**已用真实 PostgreSQL 复现。** 在一个 Pixel 上预置 3 条就绪投递。批次 A 开始发送并取得 HTTP 400、Graph 190，延迟写入该错误。A 租约到期并由 Sweep 定稿为未知后，批次 B 使用同一凭据发送第 2 条，取得完整的 1/1 确认并落库。此时写入 A 的迟到回执，目标的 `blocked_credential_id` 又被设为当前凭据。第 3 条仍然就绪，`ClaimBatch` 却返回无任务。该问题需要失租和回执乱序同时发生，影响该 Pixel 后续全部待发投递。

回执事务检查了当前凭据 ID，但没有比较同一凭据的批次先后。1933 迁移回填时却已经比较最后一次失败和成功的批次 ID，运行时与迁移对同一段历史可能给出不同结论。[运行时更新](../conversion-backend/internal/delivery/batch_receipt.go#L95) · [迁移回填](../cms-backend/db/migrations/2026111933.sql#L7) · [领取过滤](../conversion-backend/internal/delivery/batch_claim.go#L45)

**修复与验收。** 在目标行锁保护下，按目标、凭据和批次顺序更新健康状态。晚到的旧错误仍写入原批次账本；较新批次已经证实同一凭据有效时，保持后续领取畅通。还需覆盖 A 的错误先落库、已经发出的 B 随后成功的顺序，由较新的成功解除旧错误造成的暂停。两种顺序都应允许第 3 条发送，A 的成员继续保留未知。保留已有的“换新凭据后收到旧错误”及“最新批次确实失效就暂停”用例。

### 上轮四项修复的验收结果

| 原发现 | 本轮结果 |
| --- | --- |
| P1 凭据失效后继续发 | 原场景通过。批量 1 和 100 下，缺少接收数的 190 错误保留成员未知，同时拦住同 Pixel 后续领取；独立 Pixel 可发送。轮换凭据后，旧版本迟到错误不会暂停新版本。相同版本的乱序边界见上。[批次用例](../conversion-backend/internal/delivery/batch_test.go#L145) |
| P2 超龄注册挤占支付 | 通过。104 条超龄注册与 1 条 Purchase 同时就绪，批量 100 的首批已包含 Purchase；批量 1 时切换到另一个 Service 后，第二次领取取得 Purchase。原有连续支付时让出非支付名额的用例也通过。[调度与跨批计数](../conversion-backend/internal/delivery/batch_claim.go#L135) · [双向用例](../conversion-backend/internal/delivery/batch_test.go#L200) |
| P2 混合批次成员计数 | 通过。1 条 Reader 和 99 条 Partner 共用同一 Pixel，分别构造成功批次和未知批次；吞吐及监控查询按来源归属 1/99，真实平均批大小保留 100。监控的事件及账户筛选也通过。[数据库断言](../conversion-backend/internal/operations/throughput_rereview_test.go#L60) |
| P2 Partner 完成时间 | 通过。已消费和已隔离的 Inbox 在状态转换时记录自己的处理时间。跨窗口付款别名计入当前窗口归并量，隔离消息按处理时间计数，旧数据缺失时间单列。司南消费集成用例通过。[时间戳触发器](../cms-backend/db/migrations/2026111933.sql#L27) · [指标用例](../conversion-backend/internal/operations/throughput_rereview_test.go#L82) |

### 容量与测试边界

使用一次性 PostgreSQL 16、单 Worker、2 个 Reader 消费者、连接池上限 16、单 Pixel、批量上限 100、请求间隔 3 秒。积压场景的本机 HTTP 服务模拟 2 秒响应。

| 场景 | 原目标 | 本轮实测 |
| --- | --- | --- |
| 1,000 条预就绪投递至全部确认 | ≤35 秒、10 个请求 | **29.903 秒**，10 个满批，最短发起间隔 3.064 秒 |
| 1,000 条 Reader Outbox 至全部确认 | ≤60 秒 | **34.208 秒**，11 个请求，首批 22 条、末批 78 条，最短间隔 3.092 秒 |
| 空队列单笔付款至模拟 HTTP 收到请求 | P95 ≤1 秒 | **P95 479 毫秒**，20 笔、20 个请求，最大 483 毫秒 |

Conversion 全模块在隔离库执行 `go test -race -count=1 -p 1 ./...` 通过。以上计时取自另行运行的无竞态插桩容量用例。迟到回执由临时 Go test overlay 调用正式领取、持久化、扫描及定稿路径复现。本轮未向 Meta 发送真实事件。Reader 与 Partner 双实例混合峰值、30 分钟稳态、1.5 倍峰值和单实例重启仍待按实际流量分布验收。[容量用例](../conversion-backend/internal/operations/throughput_test.go) · [双源验收条件](conversion-throughput-plan.md)

---

## 上轮复审记录 · 1932

2026-11-23，审查基线为 `codex/payment-domain` 的 `5ec1513` 加当前工作区改动，包含 1931、1932 迁移及未提交的批量发送实现。审查依据为[原 Review 的 R01–R12](conversion-center-review-2026-11-23.md)和[吞吐优化方案](conversion-throughput-plan.md)。

**建议暂缓将生产批量扩大到 50 或 100。** 同 Pixel 最多 100 条、槽位释放后续领、跨实例共享冷却和批次回执持久化均已进入正式 Worker 路径。仍有一个凭据故障可连续影响多个批次，一个调度条件可让已就绪的 Purchase 长时间得不到发送机会。现有采样也不足以验收 Reader 与 Partner 同时上线时的容量。

## 需要修复的发现

| 优先级 | 发现 | 业务影响 |
| --- | --- | --- |
| P1 | 凭据失效信号未阻止后续批次领取 | 同一失效凭据可连续产生结果未知的投递，批量调到 1 也会触发 |
| P2 | 超龄非支付任务可占满每个批次 | 注册或结账积压期间，热点 Pixel 的 Purchase、Subscribe 排队甚至到期 |
| P2 | 来源筛选后仍累计整批成员 | Reader 与 Partner 共用 Pixel 时，按来源查询的发送量及未知影响数被放大 |
| P2 | Partner 消费完成时间取自统一事件 | 重复付款回调和跨窗口隔离消息导致消费吞吐、耗时和拒绝量失真 |

### 1. P1 · 凭据失效后，后续批次继续使用同一凭据

离线注入 HTTP 400、Graph code 190、subcode 463、无 `events_received`，同一笔事件走原单发入口得到 `permanent_rejection`、`Block=true`；走新版单成员批次得到 `outcome_unknown`、`Block=false`。当前 Worker 只要媒体实现支持批量就调用 `PrepareBatch`。客户端在识别凭据错误前因缺少接收数提前返回，持久化逻辑也会把缺少明确零接收的拒绝改为未知。Graph 错误码虽被保存，领取路径却没有按本次凭据版本暂停新请求。管理员轮换 Token 以后，已产生的未知投递仍需要逐笔遵循重复风险恢复流程。[Worker 分派](../conversion-backend/internal/delivery/worker.go#L289) · [响应分类](../conversion-backend/internal/media/meta/client.go#L145) · [回执校验](../conversion-backend/internal/delivery/batch_receipt.go#L102) · [领取条件](../conversion-backend/internal/delivery/batch_claim.go#L41)

**修复建议。** 分别持久化成员接收结论和凭据健康状态。对于已确认的凭据配置错误，在回执事务内阻止该目标使用同一凭据版本继续领取并发出可定位的告警。错误来自旧版本时避免阻塞刚轮换的新版本。缺少接收证据的已发成员继续保留未知；有明确零接收的成员可以沿用配置阻塞恢复路径。凭据恢复后优先处理尚未发出的任务。

**验收。** 用两个 Worker，分别在批量 1 和 100 下注入上述响应。核对首批成员状态、后续旧凭据 HTTP 请求数为零、其他独立 Pixel 继续推进。轮换凭据后验证新任务能发送，迟到的旧错误不会封锁新凭据，既有未知成员不会自动重发。

### 2. P2 · 超龄提升会反向饿死支付事件

领取逻辑把等待至少一分钟的非支付队首无条件置于支付队首之前。选中非支付后，连续支付计数归零；下一条非支付只要也已超龄，就再次胜出。同一 Pixel 有 1 笔已就绪 Purchase 和 1,000 笔已超龄注册时，即使 Purchase 等得更久，前 10 个满批也可以全部是注册。超龄积压持续补充时，支付等待缺少上界。[候选排序与成员分配](../conversion-backend/internal/delivery/batch_claim.go#L96)

**修复建议与验收。** 两类都就绪时按成员分配可跨批延续的服务份额，两边都设连续占用上限；一方为空时允许另一方填满。超龄提升可以提前获得一个名额，领取后应交回另一类的机会。用持续超龄非支付积压加 Purchase，以及持续支付积压加注册做双向测试，在批量 1、100、双实例和重启后断言两类最大连续等待的领取次数。

### 3. P2 · 筛选到一名成员会计入整个批次

吞吐接口用 `EXISTS` 判断一个批次是否含所选来源，随后对整批 `sent_count` 和接受总数求和。监控汇总对来源、账户或事件筛选也使用同类逻辑。若一批有 1 条 Reader、99 条 Partner，筛选 Reader 可显示发送 100 条；整批未知时还可显示影响 Reader 100 条。这会直接干扰两个来源的容量对比。[吞吐查询](../conversion-backend/internal/operations/throughput.go#L110) · [监控汇总](../conversion-backend/internal/operations/monitor.go#L188)

**修复建议与验收。** 请求数、HTTP 429 和真实批大小以批次去重；发送、确认及未知成员数按已冻结的 `batch_position` 关联并应用当前筛选。明确标记请求数跨来源不可相加。用 1+99 的同 Pixel 混合批分别模拟完整确认及结果未知，两来源应各显示 1 和 99 名受影响成员，全局仅 1 个请求、100 名成员。

### 4. P2 · Partner 已处理消息缺少自己的完成时间

Partner 付款别名命中同一订单时，消费事务返回已存在的事件 ID，并将新 Inbox 标记为已消费。吞吐查询用旧事件的 `created_at` 推断消费时间，只有它不早于新消息才计入归一化。此时新回调计入入队，消费完成数和延迟样本却均缺失。另一例是上一个窗口入队、本窗口转入隔离的消息，由于查询使用接收时间，也会漏掉本窗口的拒绝。[付款去重](../conversion-backend/internal/delivery/partner_payment.go#L62) · [Inbox 定稿](../conversion-backend/internal/partner/inbox.go#L129) · [分段指标](../conversion-backend/internal/operations/throughput.go#L45)

**修复建议与验收。** 在将 Inbox 改为 consumed 或 quarantined 的同一事务写入处理时间，用它计算窗口内处理数和等待耗时，另列新建统一事件数、重复归并数和隔离数。对历史未知完成时间标明覆盖范围。用跨窗口的原付款、别名回调和隔离回调验证：两条付款消息共用一个统一事件，新回调和隔离回调各在其处理窗口计一次。

## 两份方案的逐项结论

| 原项 | 当前工作区证据与复审结论 |
| --- | --- |
| R01 迁移与启动 | CMS、Reader、Conversion 的门槛已对齐到 2026111932，CMS 已嵌入迁移。受限角色从旧库升级后的完整启动仍待本轮实测。[CMS 迁移](../cms-backend/db/embed.go) · [Reader 门槛](../reader-backend/internal/db/postgres.go#L109) · [Conversion 门槛](../conversion-backend/internal/delivery/store.go#L71) |
| R02 白名单、R03 退役归因 | 已有 Reader 准入、配置跳过、冻结归因和人工恢复的实现与回归用例。R03 的新旧凭据故障恢复还应覆盖发现 1。[Reader 消费](../conversion-backend/internal/delivery/reader.go) · [Reader 修复用例](../conversion-backend/internal/delivery/conversion_review_test.go) · [订阅恢复](../conversion-backend/internal/delivery/reader_subscription_recovery_test.go) |
| R04 连续调度、R05 公平、R06 限流 | Reader 与 Inbox 独立消费，发送槽释放立即补位；限流信号可推动数据库中的 Pixel 冷却，部分接收继续保持未知。R05 尚有反向饥饿。已有批次边界、共享槽、冷却与恢复用例。[调度](../conversion-backend/internal/delivery/worker.go) · [回执与冷却](../conversion-backend/internal/delivery/batch_receipt.go) · [批次用例](../conversion-backend/internal/delivery/batch_test.go) |
| R07 订单环境、R08 采样新鲜度、R09 告警 | 已有按证据归属环境、记录最近成功采样、查询终态失败和限流的实现。凭据错误告警与筛选后批次影响数仍有上述缺口。[迁移](../cms-backend/db/migrations/2026111931.sql) · [逐单查询](../conversion-backend/internal/operations/orders.go) · [监控](../conversion-backend/internal/operations/monitor.go) |
| R10 页面故障隔离、R11 授权配置、R12 多目标查询 | 已见分离的摘要加载状态、启用账户的授权校验、按事件和 Pixel 的游标分页，以及第 51 个目标的 API 用例。本轮未做浏览器故障注入或人工重排端到端验证。[回传中心](../cms-front/src/pages/ConversionCenterTab.tsx) · [配置](../conversion-backend/internal/operations/configuration.go) · [分页及监控用例](../conversion-backend/internal/operations/conversion_review_test.go) |
| 批量发送与完成恢复 | 1–100 条按同一目标组批；发送前冻结实际成员，先持久化回执再定稿。测试代码覆盖部分响应、迟到回执及完整回执后的本地恢复。以上结论来自实现和测试用例核对。[发送边界](../conversion-backend/internal/delivery/batch_send.go) · [回执恢复](../conversion-backend/internal/delivery/batch_receipt.go) |

## 吞吐与上线验收

- **1,000 条的两个时钟。** 单 Pixel、100 条满批、3 秒发起间隔、每次响应 2 秒时，10 次请求的理想末批确认时间约 29 秒。方案对预就绪任务要求 ≤35 秒，对已提交 Reader Outbox 至末条原目标确认要求 ≤60 秒。当前测试包含两个场景，本轮没有取得新耗时。冷流量测试会打印 P95，建议把方案的 ≤1 秒写为断言，并以实际起点到最后确认的耗时断言两个容量门槛。[容量目标](conversion-throughput-plan.md) · [隔离样例](../conversion-backend/internal/operations/throughput_test.go)
- **Reader 与 Partner 同时承载流量。** 两实例、每实例 Reader 2 + Partner 2、连接池 16，对应各 4 个消费者、Worker 合计最多 32 个数据库连接。现有吞吐样例只启动一个 Worker，负载为 Reader Purchase，且未注册 `ConsumeInbox`。正式入口仅在 test 环境启用司南模拟适配器，生产仅调整消费者数量不会启动 Partner 消费。该项依赖正式契约、鉴权和消费入口；测试环境的双源压测可先进行。[采样拓扑](../conversion-backend/internal/operations/throughput_test.go#L87) · [消费者注册](../conversion-backend/cmd/conversion/main.go#L125) · [环境限制](../conversion-backend/cmd/conversion/main.go#L149)
- **明确放量依据。** 方案要求按 1、10、50、100 验证；当前 Linux 配置及服务 README 写的是首发 50。发布前统一首发档位及其对应测试记录。修复上述问题后，使用同一脱敏流量分布，先回放双源峰值 30 分钟，再回放 1.5 倍峰值并重启一个实例。记录分来源入队、消费及确认速率、两段最老等待、批大小、429、未知成员数、数据库连接获取与热点锁等待。只有 Partner 持续积压且数据库有余量时，才有依据将其从每实例 2 提高到 3。[发布说明](../conversion-backend/README.md#L158) · [双源验收要求](conversion-throughput-plan.md)

## 本轮实际验证

| 检查 | 结果 |
| --- | --- |
| Meta 凭据错误分支 | 用离线 HTTP Transport 对正式 Meta 客户端注入固定响应，复现发现 1 的单发与单成员批次差异。后续批次行为按数据库领取和回执代码核对。 |
| Go 检查 | Conversion 模块 `go vet ./...` 通过；全包 `go test -run '^$' ./...` 编译通过；`go test -race -count=1 ./cmd/conversion ./internal/paymentpolicy` 通过。 |
| 数据库与容量 | 隔离 PostgreSQL 吞吐复测命令因自动审批额度耗尽未执行。35 秒、60 秒、双源峰值和故障切换均未在本轮实测。临时容器 `sn-conv-rereview-1123` 使用自动删除参数启动；后续 Docker daemon 不可达，未能确认容器清理结果。 |
| 范围 | 本轮仅新增复审文档。未访问生产数据库，未向 Meta 或分销平台发送真实事件。 |
