# 统一转化回传架构升级 Review 报告

审查日期为 2026-11-19。审核对象是 `codex/conversion-reporting` 上以 `d35cac2` 为基线的未提交阶段 A/B 实现，包含新增的 `conversion-backend` 与数据库迁移 `2026111901` 至 `2026111905`。验收依据为 [统一转化回传架构升级与开发文档](conversion-reporting-architecture.md)。阶段 C 的正式司南接入和生产联调不计入本次 A/B 验收。

## 结论

**阶段 A/B 已接通主要链路，验收未通过。** 两项 P1 缺陷分别会让已收到 Meta 确认的事件定稿失败，以及让已导入的历史任务无法在补齐环境证据后恢复。另有 CMS 汇总、告警与补建、部署运维和权限的交付差距。共复现三个问题，复验只用一次性 PostgreSQL 和模拟数据。

| 阶段 | 已核对实现 | 验收判断 |
| --- | --- | --- |
| A 通用核心 | 新 schema、统一事件、每 Pixel 投递任务、独立尝试记录、资格校验、租约、退避及定稿。原目标定稿会在同一事务生成分销回告任务。 | 主路径已实现。已保存的接收证据无法参与崩溃恢复，失败定稿不可靠。 |
| B StarNovel | 注册、结账绑定、订阅审批和已履约支付接入事务 Outbox。CMS 已有订单、事件、汇总、配置、重排和追加目标入口。独立 Collector 和旧数据导入 CLI 已实现。 | 尚缺历史任务恢复闭环，汇总与缺路由告警覆盖有缺口，旧运行手册和权限迁移未收口。 |
| C 司南 | 本次核对到最终回告记录及其状态规则。 | 司南真实接收、鉴权、无 trace 失败格式及重放仍按阶段 C 验收。 |

## 缺陷与实施差距

### R01 · P1 · 已持久化的 Meta 接收证据仍会被定稿为失败

**已复现。** 构造原 Pixel 投递已进入 `sending`、Meta 返回 HTTP 200 和 `events_received=1`、主结果事务未提交、但独立诊断成功保存该响应的记录。运行过期租约扫描后，投递变成 `failed:meta_no_confirmation`，同时生成 `report_status=-1` 的分销回告。此时数据库已有该次真实接收证据。如果在定稿前误进入人工重排，还存在重复计数风险。

Worker 遇到结果提交错误时会将响应写入诊断表，恢复扫描却只检查尝试是否进入 `sending`。建议先在同一尝试和租约上下文中重试本地结果提交。扫描时读取并校验已经保存的有效接收证据，并在期限及任期条件成立时原子补全尝试、投递和回告。已定稿后的晚到证据继续仅用于排查。验收用例应断言原 Pixel 的回告为 `1`，修复期间不会再调用 Meta。

依据为 [Worker 结果提交](../conversion-backend/internal/delivery/worker.go#L101-L116)、[租约恢复](../conversion-backend/internal/delivery/service.go#L125-L148) 和 [独立诊断记录](../conversion-backend/internal/delivery/recovery.go#L77-L88)。

### R02 · P1 · 历史任务补齐环境证据后仍不能解阻

**已复现。** 以缺少环境证据的方式导入一条未发送、目标可查、仍在有效期内的旧任务，得到 `blocked:legacy_environment_unverified`。补齐该事件的环境证据后重跑导入，导入数为零，投递仍阻塞。正常重排也会拒绝这个原因。

导入器排除已存在于导入账本的行，重排白名单没有证据复核路径。建议增加按原事件 ID 审计复核的操作，在锁行事务里补足环境证据并重新判定旧尝试。可证实未发送且未过期的任务恢复待发，已有发送迹象的任务保留结果未知并遵守现有重排限制。验证新 CLI 能恢复已导入记录，且重做无重复任务。

依据为 [旧事件选择](../conversion-backend/internal/delivery/legacy.go#L82-L90)、[历史状态判定](../conversion-backend/internal/delivery/legacy.go#L196-L217) 和 [审计重排条件](../conversion-backend/internal/delivery/operations.go#L135-L155)。

### R03 · P2 · 已迁移的历史成功订单被 CMS 归入历史未知

**已复现。** 一个已履约订单没有新 `capi_expectation`，其迁移账本及原 Pixel 投递均明确成功。在同一份汇总中，目标统计显示一条 `succeeded`，订单群组却将它记为 `historical_unknown` 且确认数为零。

支付分母仅由新资格快照展开，其他事件聚合又排除了 StarNovel 的 `Purchase` 和 `Subscribe`。建议当快照缺失时，用已核验的历史订单、原 Pixel 投递与导入账本补充资格和确认，按同一稳定业务键去重。对确实缺证据的订单保留未知组，并用旧款、首笔订阅、缺目标三类数据核对逐单和汇总一致性。

依据为 [支付与其他事件汇总](../conversion-backend/internal/operations/monitor.go#L98-L134) 和 [逐单历史证据查询](../conversion-backend/internal/operations/orders.go#L107-L135)。

### R04 · P2 · 已归一化但缺原 Pixel 的订单没有独立告警

当订单有归因标识，但目标绑定不存在时，归一化会消化 Outbox 并保存缺 Pixel 的事件。它已不满足「未入队」条件，也没有原目标任务可进入 Meta 队列的阻塞或积压告警。CMS 汇总能查到缺目标，独立 Collector 不会通知运营。

建议按环境和接入给 `missing_pixel`、`original_target_unavailable` 增加告警分组，提供数量、最早时间及定位入口。自然流量和历史未知单列。验收模拟归一化已成功但缺目标，检查告警触发以及补建原目标后解除。

依据为 [缺原目标分支](../conversion-backend/internal/delivery/reader.go#L113-L126) 和 [Collector 告警条件](../conversion-backend/internal/operations/monitor.go#L174-L216)。

### R05 · P2 · 补建支持「入过队后丢行」，未覆盖「有资格但从未入队」

诊断依据是 `reader_conversion_repair_facts`，该记录仅在 Outbox 插入后的触发器里产生。如果订单有合格资格快照，但生产路径漏写 Outbox，就没有对应的修复事实。CLI 会在读取证据时直接拒绝，即使可核验原结算快照。保守拒绝能防重，但文档中的未入队排查仍缺对应恢复出口。

建议把可用于补建的最小快照和发送证据留在可独立核查的位置。为存在可信资格、时间、原 Pixel 且可证明从未进入发送的订单提供审计补建。证据不足的订单保留待调查。验收分别覆盖从未入队、入队后丢行以及此前已经发送三种情况。

依据为 [修复事实触发器](../cms-backend/db/migrations/2026111905.sql#L17-L41) 和 [补建证据校验](../conversion-backend/internal/delivery/repair.go#L92-L119)。

### R06 · P2 · 切换后旧运维命令仍可操作无人消费的队列

Reader 已取消旧的 CAPI 消费者，原 `payment-ops -kind outbox` 仍会更新 `payment_event_outbox`。旧任务统计同时仍报告该表的待发。运营可以看到旧命令执行成功，却没有任何新 Worker 重试。新服务 README 也仍写 schema `2026111904`，实际检查值为 `2026111905`。正式部署 SOP 未纳入新 Worker、私网 API、独立 Collector 及新索引脚本。按现有发布入口单独升级 Reader/CMS，业务可入队但没有新投递消费。

建议切换旧出站统计和重试入口至统一运维路径，保留旧表的只读历史查询。更新部署 SOP 与服务 README，补齐对应版本、运行模式、索引、凭据写入密钥版本、启停顺序和 Collector 自检。至少用一笔订单验证新队列、回执和告警均在部署后可查。

依据为 [旧重试 SQL](../reader-backend/internal/wallet/operations_repository.go#L87-L95)、[旧统计](../reader-backend/internal/wallet/operations_repository.go#L55-L59)、[版本检查](../conversion-backend/internal/delivery/store.go#L66-L75)、[服务 README](../conversion-backend/README.md#L1-L18) 和 [部署 SOP](../DEPLOYMENT_SOP.md)。

### R07 · P2 · 迁移和上线流程缺少数据库角色隔离

架构要求 Reader 仅获转化 Outbox 及相关资格写入能力，凭据和投递任务由转化服务处理。当前迁移创建了表、索引和触发器，但未提供对应的数据库角色与授予脚本，也未验证最小权限角色能运行这些正常链路。用共享的表所有者 DSN 可以跑通测试，却不能证明部署后的隔离。

建议在发布步骤中落实各服务数据库账号、必要权限和凭据访问边界。用受限账号验证 Reader 可结算并写 Outbox、Worker 可领取结果、CMS 可做授权后的运营操作。另验证 Reader 无法读凭据表或直接改写投递终态。

依据为 [目标服务边界](conversion-reporting-architecture.md#2-服务边界与数据流)、[凭据与队列表迁移](../cms-backend/db/migrations/2026111901.sql) 和 [各运行模式连接初始化](../conversion-backend/cmd/conversion/main.go#L36-L70)。

### R08 · P2 · 归因快照与诊断明细缺少可执行的清理流程

支付 Outbox 的快照可包含邮箱、IP、UA 和广告标识。修复事实表会再保存一份。诊断表已建时间索引，代码与运维入口尚无按期限批量清理或脱敏的流程。长期运行会同时累积敏感归因字段和诊断数据。

建议实现可控的分批保留策略，清理允许过期的原文、诊断及匹配字段，保留稳定键、资格与已发送证据。回归时核对清理前后的成功率、历史对账和防重结果，并验证有效期内仍可恢复的任务不受影响。

依据为 [支付快照构造](../cms-backend/db/migrations/2026111902.sql#L65-L89)、[补建快照存储](../cms-backend/db/migrations/2026111905.sql#L17-L41) 和 [结构化诊断存储](../conversion-backend/internal/delivery/recovery.go#L77-L88)。

## 验证记录

| 验证 | 结果 |
| --- | --- |
| `conversion-backend`、`cms-backend` 各运行 `go test -race -count=1 ./...` | 通过。使用一次性 PostgreSQL 16，覆盖新 schema、旧数据导入、队列领取、定稿、对账和独立采集。 |
| `reader-backend` 运行 `go test -race -count=1 ./...` | Wallet、Auth 等业务包通过。全量并跑时 `internal/db` 的 `TestConnectionWaitAndQueryBudgets` 在 50 ms 获取连接预算处超时，初次单独复测也超时。随后对同一个隔离数据库按「当前工作树、Git 基线、当前工作树」顺序各执行 3 次，全部通过。连接预算逻辑与基线相同，目前未将这次不稳定结果归因为 A/B 回归。 |
| 三个 Go 模块 `go vet ./...` | 通过。 |
| CMS 前端 `npm run lint`、`npm run build` | 通过。 |
| 补充复验 | 两个临时模块测试和一个旧数据导入用例扩展复现 R01、R02、R03。测试通过表示实际观察到了缺陷。临时代码未加入仓库。 |

本次复验使用本地隔离数据库与模拟投递证据。生产队列、真实 Meta 接收、司南实际回告、生产容量和部署演练待后续验收。

## 修复与复审顺序

1. 先修 R01、R02，使用可重复的故障注入和历史证据复核测试锁住两条 P1。
2. 补齐 R03 至 R05 的对账、告警和安全恢复，核对 CMS 逐单和汇总口径。
3. 收口 R06 至 R08，使用受限账号完成切换演练并验证 Worker 与 Collector 独立运行。A/B 验收通过后再安排阶段 C 联调。

## 阶段 C 司南接入复验

复验对象为 `codex/conversion-reporting` 上以 `ca56c94` 为基线的阶段 C 修复，包含迁移 `2026111908`。首轮审查发现 3 项 P1、3 项 P2。复验通过其中 5 项，原有 1 项 P2 仍存并发边界，另发现 1 项跨版本幂等 P2。

| 首轮发现 | 复验结果 |
| --- | --- |
| P1 · 设备和订单 ID 被当作纯数字解析 | 通过。三个入口接收司南样例中的字符串 ID，订单号与设备号在最终回告中保留原值。 |
| P1 · 司南 IP 和 User-Agent 丢失 | 通过。`client_ip` 和 `user_agent` 被转换为 Meta 匹配字段，并通过发送前校验。 |
| P1 · 司南推广链与 StarNovel 主键撞号 | 通过。同号的 StarNovel 推广链及其他应用的映射不影响原 Pixel。当前应用有显式绑定且 Pixel 不符时仍阻塞。 |
| P2 · 整数 ID 被编码成字符串 | 通过。回告中的 `app_id`、`distributor_id` 和 `promotion_id` 以无损 JSON 整数发送。模拟回告按字段表使用 `ad_set_id`。 |
| P2 · 司南已接收的回告被记为结果未知 | 部分通过。确认先落库时，扫描能恢复为 `confirmed`，且只发送一次。扫描抢先完成时仍会丢失确认，见 C01。 |
| P2 · CMS 缺少回告尝试证据 | 通过。最近 20 次尝试可查 HTTP 状态、业务码、时间和重试计划，接口不返回原始载荷。 |

### C01 · P2 · 恢复扫描可能抢先丢弃已收到的确认

已复现。Worker 在租约有效期内收到 HTTP 200 和 `code=0`。租约到期后 `Finish` 拒绝提交。如果另一 Worker 在 `RecordReceipt` 前完成过期扫描并清除令牌，原尝试的确认无法写入，回告被记为 `outcome_unknown`。

修复时按原尝试保存确认，在回告行锁内检查后续尝试是否已发送，再决定是否恢复为 `confirmed`。回归应覆盖扫描先完成和确认先落库两种顺序，并验证旧尝试不会覆盖后续已发送尝试。[回告 Worker](../conversion-backend/internal/partner/report_worker.go#L78-L99) · [回告队列](../conversion-backend/internal/partner/report_queue.go#L127-L155)

### C02 · P2 · 相同回调跨版本重试返回 409

已复现。旧版摘要忽略司南的 `client_ip` 和 `user_agent`，新版摘要包含这两个字段。按旧版规则保存 Inbox 记录后，逐字节重发同一请求，返回 `409 source_event_conflict`。

给摘要增加版本标识，旧记录按旧规则核对，新记录继续检查实际变更。回归应验证同一请求跨版本重试返回 200，且仅产生一条业务事件，同时保留对同键业务内容冲突的拦截。[入站摘要](../conversion-backend/internal/partner/sinan/inbox.go#L156-L178) · [Inbox 去重](../conversion-backend/internal/partner/inbox.go#L34-L60)

### 验证范围

- 转换服务、CMS 和 Reader 的 `go test -race -p 2 -count=1 ./...` 与 `go vet ./...` 均通过。CMS 前端 lint 和构建通过。
- 在隔离 PostgreSQL 16 和模拟 HTTP 环境复验。两个补充用例复现 C01 和 C02，临时测试代码未加入仓库。
- 真实入站鉴权、回告端点与签名以及无 trace 失败格式仍待司南联调。[联调验收项](conversion-reporting-architecture.md#72-联调待确认项)

### C01、C02 修复验证

迁移 `2026111909` 保留回告尝试令牌并给 Inbox 摘要标记版本。隔离 PostgreSQL 回归通过以下场景。

- C01 覆盖确认先落库、扫描先收回租约、后续尝试已领取但尚未发送，以及后续尝试已进入发送的顺序。前两种均可完成确认。迟到的确认会结束尚未发送的重试。后续尝试进入发送后，迟到确认只保存在原尝试，后续尝试仍负责定稿。
- C02 使用按旧规则构造的 Inbox 摘要。相同回调跨版本重试返回 200，只归一化一次。旧记录的金额变更和新记录的 IP、User-Agent 变更仍返回 409。

**上轮独立复验结论。C01 通过，C02 部分通过，当时剩 1 项 P2。**

- C01 追加实际模拟 HTTP 回归。Worker 在租约有效期内读到 HTTP 200 和 `code=0`。扫描先回收租约并将回告标记为 `outcome_unknown`，随后 Worker 保存原尝试的确认并恢复为 `confirmed`，HTTP 只发送一次。
- C02 从 `2026111907` 的摘要规则升级后，相同请求可安全重试。以 `2026111908` 接收并保留了 `client_ip` 和 `user_agent` 的摘要构造升级记录后，重发同一请求仍返回 `409 source_event_conflict`，并增加一条隔离记录。`2026111909` 把已有行统一标为版本 1，冲突检查只使用忽略这两个字段的旧摘要。[迁移默认值](../cms-backend/db/migrations/2026111909.sql#L1-L3) · [摘要核对](../conversion-backend/internal/partner/inbox.go#L46-L66)

迁移前未标记版本的记录可先核对完整摘要，再核对旧摘要。版本 2 继续按完整摘要核对。补齐从 `2026111907` 和 `2026111908` 升级的重复回调测试，两者都应返回 200 且只生成一条业务事件。`2026111908` 已入库的金额、IP 或 User-Agent 发生变更时，仍应返回 409。

本轮在隔离 PostgreSQL 16 上重跑转换服务、CMS 和 Reader 的全量 `go test -race` 与 `go vet`，均通过。CMS 前端 lint 和构建通过。上述两个独立补充用例使用临时测试代码。

### C02 剩余问题修复验证

迁移前的未标记摘要先按完整字段核对，未匹配时再按旧字段核对。版本 2 只核对完整摘要。使用隔离 PostgreSQL 分别在 `2026111907` 和 `2026111908` 写入旧记录，并实际执行后续迁移。两个来源的相同回调重试均返回 200，且只归一化一次。金额变更，以及 `2026111908` 已记录的 IP 或 User-Agent 变更仍返回 409。版本 2 也拒绝在重试中新增或修改浏览器字段。转化服务全量竞态测试和 `go vet` 通过。

**本轮独立复验通过，阶段 C 上轮遗留问题已关闭。** 在隔离 PostgreSQL 16 对两条迁移路径、冲突校验和 C01 回告租约恢复连续执行两轮竞态回归，全部通过。真实司南鉴权和回告协议仍按架构文档的联调项验收。[升级回归用例](../conversion-backend/internal/partner/sinan/integration_test.go#L645-L702) · [联调验收项](conversion-reporting-architecture.md#72-联调待确认项)

## 购买与订阅事件部分回传 Review

2026-11-20 审查 `codex/conversion-reporting` 上以 `22be987` 为基线的工作区实现，包含迁移 `2026111910` 至 `2026111912`。评审范围包括公共付款规则、Reader 和司南接入、CMS 建链配置、逐单对账及监控。验收依据为 [购买与订阅事件部分回传开发方案](conversion-partial-reporting-implementation.md)。

**审查发现 1 项 P1、2 项 P2。** 两项付款事实缺陷会使符合规则的 StarNovel 订单阻塞并可能到期，监控缺陷会使正常的部分回传表现为投递确认率下降。修复与验收记录见本节末尾。

### R09 · P1 · PayPal 续费回调乱序时丢失付款类型

**复现。** 使用同一订阅依次处理首付、第 3 笔付款和晚到的第 2 笔付款。第 3 笔快照标记为 `subscription_renewal`，第 2 笔的 `payment_kind` 为空，`action_source` 为 `website`。当前代码以 `paidAt.After(last_payment_time)` 为续费依据，乱序付款无法通过这个条件。若链接配置了具体付款类型规则，缺少类型的订单进入 `rule_fact_missing`，可能在 Meta 投递截止前一直阻塞。[Reader 履约](../reader-backend/internal/wallet/repository.go#L598) · [转化快照](../cms-backend/db/migrations/2026111911.sql#L115) · [规则匹配](../conversion-backend/internal/paymentpolicy/rules.go#L166)

**修复建议。** 在锁定订阅行的履约事务内，根据 `capi_subscribe_state`、`capi_subscribe_order_id` 和已核实的渠道付款记录明确传入 `payment_kind`。已认领首付后的其他已核实续费按 `subscription_renewal` 处理，包括渠道时间早于或等于最近付款时间的续费。`last_payment_time` 继续单调更新。用独立字段决定 `action_source`，并为自动续费正确保留其来源。历史订阅确实缺乏首付或续费依据时保留未知诊断。通过后续迁移更新数据库函数，覆盖 Reader 旧调用入口的升级兼容。[事务出站入口](../reader-backend/internal/conversion/outbox.go#L45)

**验收。** 在隔离 PostgreSQL 使用顺序、乱序和同时间三组已核实付款。首付只生成一组 `Purchase` 与 `Subscribe`，两条事件共用 `subscription_initial` 判定。其余每笔续费只生成一个 `Purchase`，按 `subscription_renewal` 命中相应规则。重复处理同一渠道付款时，事件、判定和序号各只产生一次。

### R10 · P2 · 老链接核验后，已入队首单仍无法判定

**复现。** 现有 `TestDurableDeliveryLifecycle/reader_payment_policy` 先在历史覆盖尚未核实的链接履约并记录首单订单 ID，此时原 Pixel 暂不可用。随后补齐覆盖核验、发布「仅首单」规则并恢复原 Pixel。重试仍从旧快照读取空的 `first_paid_on_promotion`，首单持续阻塞为 `rule_fact_missing`，第二笔付款判定为复购并跳过。若订单在核验前入队、发布后才首次消费，Worker 也会读到同样的空快照。[首单事实写入](../cms-backend/db/migrations/2026111911.sql#L103) · [首次判定与重试证据](../conversion-backend/internal/delivery/reader_payment.go#L95) · [复现用例](../conversion-backend/internal/delivery/reader_payment_test.go#L209)

**修复建议。** 将补齐逻辑放在 Reader 公用的付款判定路径，供首次消费、重试和补建复用。对未完成判定且首单快照为空的订单，用已校验的源订单、用户、推广链接和当前环境查询首单事实。仅在历史覆盖已核实且存在可验证的首单记录时补齐布尔值。用窄范围数据库函数提供查询能力，并为 Worker 与运营 API 补齐所需权限和受限角色测试。保留原始快照，并在首次成功判定事务中固化结果。事实仍不充分时保留 `rule_fact_missing`。[数据库角色授权](../cms-backend/db/conversion_role_grants.sql#L119)

**验收。** 分别覆盖核验前已入队但尚未消费，以及缺原 Pixel 后恢复并重试。发布「仅首单」且首单最终动作配置为全传时，两条路径均应选中真实首单并放行待投递任务，同链接的第二笔有效付款应按 `rule_not_first_payment` 跳过。覆盖尚未核实、事实不一致和已完成判定三种场景，分别验证核验门禁、未知诊断和原判定复用。

### R11 · P2 · 按规则跳过的付款进入原目标确认率分母

**复现。** 在相同来源、接入、事件和时间窗口内构造两笔司南 `Purchase`，其中一笔选中且 Meta 确认，另一笔按规则跳过。`GET /internal/conversion/summary` 返回 `selected_confirmation` 为 `1/1`，同时原表的 `expected` 分组为 `1/2`。CMS 分别呈现 100% 和 50% 的确认率。Reader 的 `payment_facts` 同样把这类付款归入 `expected`。[运营查询](../conversion-backend/internal/operations/monitor.go#L112) · [CMS 确认率](../cms-front/src/components/conversion/ConversionSummaryTab.tsx#L431)

**修复建议。** Reader 和分销汇总用已落库的付款判定识别 `skipped`，将其在资格分组中展示为「按规则跳过」，确认率显示 `—`。应投递的原目标事件继续纳入确认率和缺入队、缺目标、缺时间诊断。覆盖率仍按付款计算，确认率仍按原目标事件计算；追加 Pixel 的成功只进入其自身投递统计。CMS 分别标明规则指标使用判定时间、原目标资格统计使用事件业务时间。[现有规则统计区](../cms-front/src/components/conversion/ConversionSummaryTab.tsx#L409)

**验收。** 对 Reader 和司南各构造一笔已确认和一笔规则跳过的同类事件。覆盖率应为 50%，两处对应的应投递确认率应为 100%，界面可见一笔规则跳过。另测全跳过时确认率为 `—`、选中但确有缺目标时保留诊断、追加 Pixel 确认时原目标分子不变。同一付款的 `Purchase` 和 `Subscribe` 在覆盖率分母只计一次，确认率按事件分别统计。

### 验证记录与复审顺序

- 隔离 PostgreSQL 16 上，Reader、CMS、转化服务的 `go test -race -p 2 -count=1 ./...` 和 `go vet ./...` 均通过。CMS 前端 `npm run lint` 和 `npm run build` 通过。现有回归覆盖同单共用判定、按链接独立计数、规则变更续号及司南 Mock 最终回告。[规则并发回归](../conversion-backend/internal/operations/payment_policy_test.go#L23) · [司南最终回告回归](../conversion-backend/internal/partner/sinan/payment_policy_test.go#L20)
- 使用两条临时用例在本地隔离数据库复现 R09 和 R11，使用现有 Reader 投递集成用例复现 R10。上述修复建议待实施及专项复验。浏览器交互、真实 Meta 投递和司南正式回告协议仍待验收。
- 优先修复 R09 和 R10，合入对应回归后再开启按付款类型或仅首单配置。上线前一并完成 R11，并在 CMS 核对覆盖率、确认率和逐单对账。

### R09–R11 修复与复验

2026-11-20，R09、R10 和 R11 已在本地修复。验证使用隔离 PostgreSQL 16 和 CMS 浏览器模拟数据。

- R09 根据认领的首付订单和已核实的渠道付款识别续费。顺序、乱序、同时间及重复付款回归通过，首付的 `Purchase` 与 `Subscribe` 共用判定。续费保留 `system_generated` 来源，每笔仅占用一个判定序号。历史订阅缺证据时保留未知诊断，已核实的先前付款可支持后续续费分类。旧六参数写入入口通过兼容性验证。 [Reader 履约用例](../reader-backend/internal/wallet/batch3_test.go) · [规则投递用例](../conversion-backend/internal/delivery/reader_payment_test.go)
- R10 从源订单和已核实的首单记录补齐事实。首次消费、原目标恢复后重试和补建均可选中首单，同链接复购按规则跳过。覆盖未核实或订单事实不一致时仍停在 `rule_fact_missing`，已完成判定直接复用。Worker 和运营 API 使用受限数据库函数，校验环境、用户和推广链接。 [判定回归](../conversion-backend/internal/delivery/reader_payment_test.go) · [角色权限回归](../conversion-backend/internal/delivery/permissions_test.go)
- R11 将 Reader 和司南的规则跳过归入独立资格分组。覆盖率 50% 时，示例中应投递事件的原目标确认率均为 100%，全跳过场景显示 `—`。选中事件缺原目标时保留诊断，追加 Pixel 确认只进入自身投递统计。 [汇总接口回归](../conversion-backend/internal/operations/payment_policy_read_test.go)
- Reader、CMS 和转化服务的完整 Go 竞态测试与 `go vet` 通过。CMS 前端 lint、构建和桌面浏览器统计核对通过。数据库已验证从 `2026111912` 升级到 `2026111913`。生产发布需要应用新迁移并刷新独立数据库角色授权。 [迁移回归](../cms-backend/internal/db/migration_test.go) · [发布步骤](../DEPLOYMENT_SOP.md#付款部分回传schema-2026111915)

### 2026-11-20 独立复审

**R09–R11 的原复现均通过，新发现两项 P2。** R09 的同时间及乱序续费能命中续费规则，旧六参数入口也能写入正确类型。R10 的待消费快照、原目标恢复后重试和补建能补齐已核实的首单事实，环境或归属不符时仍阻塞。R11 的 Reader 和司南汇总都将规则跳过从应投递分母移出。[Reader 履约回归](../reader-backend/internal/wallet/batch3_test.go#L139) · [首单恢复回归](../conversion-backend/internal/delivery/reader_payment_test.go#L245) · [监控回归](../conversion-backend/internal/operations/payment_policy_read_test.go#L186)

#### R12 · P2 · 历史续费补齐渠道证据后仍无法重试

**复现。** 在历史订阅上先履约并归一化一笔没有可验证早期付款证据的订单。配置续费类型规则时，该订单停在 `rule_fact_missing`。随后录入一笔渠道时间更早、已核实且已履约的同订阅付款。数据库此时能把原订单识别为 `subscription_renewal`，但 `RetryDelivery` 仍读取原始快照里的空 `payment_kind`，返回成功且没有安排下次投递，任务继续阻塞。使用隔离 PostgreSQL 的临时集成用例复现。[订阅类型查询](../cms-backend/db/migrations/2026111913.sql#L17) · [Reader 重试证据](../conversion-backend/internal/delivery/reader_payment.go#L124) · [重试结果](../conversion-backend/internal/delivery/payment_delivery.go#L240)

**修复建议。** 对未完成判定且类型缺失的 Reader 订阅付款，在首次消费、重试和补建的共同路径中，按环境、订单、用户及订阅核对渠道付款事实。已可证实为自动续费时，按纠正后的 `action_source` 校验载荷，并在投递解阻的同一事务内更新待发事件的派生字段。复用本单原始归因、发生时间和投递截止时间，并让判定照常取得一次规则结果和序号。保持原始快照可供追溯。若重试后仍缺证据，响应显式标注待核查状态，便于 CMS 显示真实结果。

**验收。** 测试早期付款在原订单首次消费前、阻塞后才补齐两种顺序。阻塞后补齐时，运营重试应按同一笔订单命中续费规则，并按规则恢复投递或跳过。重复重试和并发消费只产生一个判定。证据缺失、身份不一致、已过期或已经定稿的订单继续遵循原状态和约束。

#### R13 · P2 · 选中后确认率漏算尚未归一化的同单事件

**复现。** Reader 的订阅首付款已经选中，`Purchase` 已确认，订单资格里也有应投递的 `Subscribe`，但 `Subscribe` 还没有归一化记录。按 `Subscribe` 筛选汇总，原资格统计给出 1 条应投递、1 条缺入队，规则区给出的付款覆盖率分子和分母均为 0，选中后确认率也没有记录。规则区只连接已有的 `conversion.events`，遗漏了这笔已选中的付款和应投递事件。同窗口存在其他已确认的 `Subscribe` 时，确认率会偏高。使用隔离 PostgreSQL 的汇总接口用例复现。[确认率与事件过滤查询](../conversion-backend/internal/operations/monitor.go#L186) · [Reader 资格查询](../conversion-backend/internal/operations/monitor.go#L112)

**修复建议。** 对 Reader 在已判定付款集合中，以 `capi_expectation.events` 和首付归属确定应投递事件，再左连归一化事件和原目标投递记录。应用事件类型过滤时按该资格集合选择付款并对付款键去重。保留判定时间窗口、Pixel 作用域和逐单诊断。实际原目标确认记录构成分子。

**验收。** 对已选中的订阅首付，仅归一化并确认 `Purchase`，过滤 `Subscribe` 时付款覆盖率为 `1/1`，选中后原目标确认率为 `0/1`。补齐 `Subscribe` 并确认后，确认率变为 `1/1`。追加 Pixel 确认不增加原目标确认数，同时验证一笔含两种事件的付款在未过滤的覆盖率中只计一次。

本轮独立重跑 Reader、CMS、转化服务的全量 `go test -race -p 2 -count=1 ./...` 和 `go vet ./...`，均通过。CMS 前端 lint 和构建通过。补充的两条临时数据库集成用例均复现故障。浏览器交互、真实 Meta 和司南端到端投递沿用单独的联调验收。

### R12、R13 修复与复验

- R12 使用已核实的同订阅付款补齐待判定订单的类型。首次消费、运营重排和补建共用该路径。确认为自动续费后，投递使用 `system_generated`，原始快照、业务发生时间和截止时间保留作审计。隔离 PostgreSQL 验证了并发重排、消费重放、证据归属与环境不符、截止时间、既有判定复用及 Worker 和运营 API 的受限角色权限。CMS 支持重排 `rule_fact_missing`，并通过浏览器验证了「付款事实仍待核查」和「按规则跳过」两种反馈。 [续费恢复回归](../conversion-backend/internal/delivery/reader_subscription_recovery_test.go)
- R13 规则统计从 Reader 订单资格快照和订阅首付认领取得应投递事件。按 `Subscribe` 筛选时，缺入队的首付计入付款覆盖率 `1/1` 和原目标确认率 `0/1`。追加 Pixel 确认后原目标确认率仍为 `0/1`，原目标确认后变为 `1/1`。同笔付款仍只计一次，付款判定时间和原目标 Pixel 筛选均已验证。 [汇总接口回归](../conversion-backend/internal/operations/payment_policy_read_test.go)
- Reader、CMS 和转化服务的完整 Go 竞态测试与 `go vet` 通过。并行执行三个模块时，Reader 的连接预算用例在建立测试连接时超时，单模块串行全量复跑通过。CMS 前端 lint、构建和键盘提交流程验证通过。
- 本地已验证从 `2026111912` 和 `2026111913` 升级到 `2026111914`。上线前按部署步骤应用迁移。独立数据库角色需刷新授权。 [迁移回归](../cms-backend/internal/db/migration_test.go) · [发布步骤](../DEPLOYMENT_SOP.md#付款部分回传schema-2026111915)

### R12、R13 独立复审（2026-11-20）

**两项原复现均通过，续费路径另发现两项 P2。** R12 在原网站上下文完整时可等待渠道证据，经并发重排只产生一个判定和一个循环序号，并写入 `system_generated`。R13 过滤 `Subscribe` 时，缺入队的订单已正确显示付款覆盖率 `1/1` 和原目标确认率 `0/1`。确认追加 Pixel 时分子不变，原 Pixel 确认后变为 `1/1`。[续费恢复回归](../conversion-backend/internal/delivery/reader_subscription_recovery_test.go#L92) · [Subscribe 汇总回归](../conversion-backend/internal/operations/payment_policy_read_test.go#L174)

#### R14 · P2 · 历史续费缺网站上下文时提前定稿失败

**复现。** 归一化一笔已履约且有原 Pixel 的历史订阅订单。快照没有有效网站 URL，当时也缺少可确认续费的早期付款证据。流水线按快照中的 `website` 校验，直接把投递定稿为 `failed/invalid_website_context`。随后补入同订阅的早期已核实付款，数据库已可识别该单为续费，但运营重试返回 `state_conflict`。若证据在首次归一化前就已存在，同样缺 URL 的订单可使用 `system_generated` 进入投递。[载荷校验与定稿前判断](../conversion-backend/internal/delivery/payment_delivery.go#L53) · [待判定重试门禁](../conversion-backend/internal/delivery/payment_delivery.go#L204)

**修复建议与验收。** 将未知续费类型导致的 `invalid_website_context` 作为可等待的付款事实缺失，仍校验金额、时间和匹配数据。证据到齐后，在原期限内按 `system_generated` 重新校验并走原规则。补测缺 URL 且证据在归一化前、归一化后到达的两种顺序，验证选中时 Worker 读到有效载荷、跳过时无发送、证据一直不足时按原截止时间结束。确认必须使用网站上下文的真实首付仍按现有规则校验。

#### R15 · P2 · 首次履约时跨环境付款证据可进入续费快照

**复现。** 同一数据库中，历史订阅在 `test` 有一笔已标记环境的已核实早期付款。将该订阅的新订单通过兼容六参数入口写入另一环境。由于判定函数执行时新订单的 `capi_expectation` 尚为空，环境比较直接通过，快照落入 `subscription_renewal:system_generated`。随后在新环境调用受限补证函数会返回未知。当前 Reader 履约入口也在写快照前调用同一分类函数，因此同库多环境时，首次履约与恢复使用了不同的证据边界。[订阅证据条件](../cms-backend/db/migrations/2026111914.sql#L14) · [Reader 履约调用](../reader-backend/internal/wallet/repository.go#L647) · [兼容写入入口](../cms-backend/db/migrations/2026111913.sql#L153)

**修复建议与验收。** 首次履约、兼容入口和恢复入口均显式传入本次环境。核对已认领首付和历史早期付款两条分支。测试同一个数据库中证据处于同环境、其他环境及未标记环境三种情况，确认已知其他环境的订单无法影响付款类型、回传规则和事件来源。未标记的历史付款按既有历史证据口径处理。

本轮在隔离 PostgreSQL 16 使用临时用例复现 R14、R15。另验证已有有效网站上下文的续费在补证后可由受限 Worker 领取，载荷校验通过，并在模拟接收确认后定稿为 `succeeded`。Reader、CMS、转化服务的全量竞态测试和 `go vet` 均通过，覆盖从 `2026111912` 与 `2026111913` 升级到 `2026111914`。CMS 前端 lint 和构建通过。浏览器交互及真实 Meta、司南投递继续按联调验收。

### R14、R15 修复与复验

- R14 缺网站上下文且续费类型待确认的订阅付款，会在原期限内等待补证。金额、匹配数据或业务时间无效的订单仍按对应原因处理。受限 Worker 和运营 API 回归覆盖证据先到及晚到、选中后模拟确认、按规则跳过、到期定稿和真实网站首付校验。迁移仅恢复仍在期限内、从未尝试投递且待判定的符合条件的旧失败任务。 [续费恢复回归](../conversion-backend/internal/delivery/reader_subscription_recovery_test.go) · [迁移回归](../cms-backend/internal/db/migration_test.go)
- R15 首次履约、六参数兼容写入和补证统一显式传入环境。对已认领首付和历史早期付款分别验证了同环境、其他环境、历史环境未标记三种情况。其他环境的证据无法决定本环境的付款类型或事件来源。 [环境隔离回归](../reader-backend/internal/wallet/subscription_conversion_environment_test.go)
- CMS 新增金额区间的币种固定为 `USD`。桌面和窄屏浏览器验证了键盘无法修改币种，预览请求以 USD 提交并按美分精确转换。 [金额区间表单](../cms-front/src/components/conversion/PaymentPolicyBandDialog.tsx)
- 首次创建和后续变更均可直接发布，预览供发布前核对。服务端为首个专属版本写入「新建回传规则」，为后续版本和恢复继承写入「变更回传规则」。浏览器验证了直接发布、可选预览，以及发布响应超时后通过审计核对结果。 [发布交互](conversion-partial-reporting-implementation.md#53-草稿权限与异常状态)
- 隔离 PostgreSQL 16 验证了从 `2026111912`、`2026111913` 和 `2026111914` 升级到 `2026111915`。CMS 和转化服务的全量 Go 竞态测试通过。Reader 并跑时的连接预算用例在建立测试连接时超时，单独重复测试通过，其他 Reader 包通过。三个模块的 `go vet` 和 CMS 前端 lint、构建均通过。 [发布步骤](../DEPLOYMENT_SOP.md#付款部分回传schema-2026111915)

### R14、R15 独立复审（2026-11-20）

**R14、R15 的原复现均通过。规则发布的超时核对另有一项 P2。** R14 验证了缺 URL 的未知续费保留在 `blocked/rule_fact_missing`。补证后可按规则选中并由 Worker 收到模拟确认，也可直接跳过。到期仍缺证据按原期限定稿，真实网站首付和缺金额订单继续按各自校验处理。R15 在实际 Reader 履约、六参数兼容入口和续费补证入口分别验证了同环境、其他环境和未标记环境的付款证据。[续费恢复回归](../conversion-backend/internal/delivery/reader_subscription_recovery_test.go#L96) · [环境隔离回归](../reader-backend/internal/wallet/subscription_conversion_environment_test.go#L41)

#### R16 · P2 · 规则发布已提交，超时核对仍提示重新发布

**复现。** 编辑已有推广链接专属规则时，另一位运营先更新接入默认规则。隔离 PostgreSQL 用例确认服务端接受仍带旧继承版本的请求，并用本次 `publication_id` 写入审计。若响应超时，CMS 的「查看最新配置」还要求审计中的发布前继承版本等于旧草稿版本，因此忽略了已有的成功审计。若本次发布后又有更新，审计版本与当前生效版本不同，也会进入同一误判。用本机 Chrome 和模拟网关 `504` 响应分别复现两种情况。两次都显示「未找到本次发布记录」，保留旧草稿并重新启用发布按钮。再次发布可能生成重复版本，或覆盖超时后已生效的配置。[服务端版本校验](../conversion-backend/internal/paymentpolicy/settings.go#L228) · [CMS 核对逻辑](../cms-front/src/components/conversion/PaymentPolicyPanel.tsx#L311)

**修复建议与验收。** 按 `publication_id`、操作人和作用域精确查询已提交的审计记录。先确认本次是否成功，再用当前生效版本判断本次版本是否仍有效。若已被替代，显示已提交的版本和当前生效版本，并结束本次待确认流程。覆盖响应超时且默认规则变更、同链接随后发布了下一版本、确实未提交三种情况。对高频接入使用精确查询，覆盖成功记录被后续审计挤出最近 20 条的情况。

本轮在隔离 PostgreSQL 16 串行执行了三个 Go 模块的全量竞态测试及 `go vet`，均通过，包含 `2026111912`、`2026111913`、`2026111914` 升级到 `2026111915` 的迁移验证。CMS 前端 lint 和构建通过。浏览器测试使用模拟 API，真实 Meta 和司南投递仍按联调验收。

### R16 修复与复验

- 回传规则超时核对按 `publication_id`、验证过的操作人、环境、接入、应用及推广链接精确查询。审计和当前配置在同一接入锁下读取，核对会等待正在提交的发布事务。隔离 PostgreSQL 覆盖了接入默认规则先更新、同链接后续版本生效、记录移出最近 20 条、确实无提交记录及作用域隔离。 [审计回归](../conversion-backend/internal/operations/publication_audit_test.go)
- CMS 确认成功后清除本次已提交的草稿，并区分本次发布版本和当前生效版本。浏览器验证了已被替代时的状态、无提交记录时保留草稿，以及旧响应格式无法核对时仍暂停发布。 [发布核对](../cms-front/src/components/conversion/PaymentPolicyPanel.tsx)
- 发布前端无需填写或提交 `reason`。CMS 将发布请求错误以回传规则专用文案展示。隔离数据库验证了超过旧 8 KiB 限制的 100 条区间草稿可以直接发布，CMS 代理验证了大请求的完整转发。 [CMS 代理测试](../cms-backend/internal/tracking/conversion_orders_test.go)
