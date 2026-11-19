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
