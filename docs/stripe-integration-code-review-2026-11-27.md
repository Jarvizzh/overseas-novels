# Stripe 接入优化实现 Code Review

2026-11-27 · 审查方案：`stripe-integration-audit-and-optimization-plan-2026-11-27.md`

**最新复审结论（第 3 轮，`09903c6`）：R1、R2、R3、R5 复验通过。R4 在原用例中通过，但首次报表生成前的起点仍依赖 Payout 的可变窗口，交叉恢复用例已复现漏掉 12 天。当前剩余 1 项 P2；新增 `STRIPE_REPORT_START_DATE` 可以显式补回缺口，默认路径尚未完全闭环。详见第 10 节。**

第 1～9 节保留前两轮审查记录，状态以第 10 节复审结论为准。

## 1. 首轮结论与范围

**首轮确认 4 项缺陷：3 项 P1、1 项 P2，均已使用一次性 PostgreSQL 16 和临时 Go overlay 复现。首轮建议修复后再启用相应能力，当时未按完整方案验收通过。**

Stripe 改造审查范围为 `f171ce4..daf06cb` 中的相关代码及迁移；审查结束时工作区 HEAD 为 `9482c4e`，新增的章节字数计费提交不属于本次范围。未修改业务代码、原方案或既有测试。复现新增用例只存在于临时 overlay，未写入业务测试目录。没有操作 Stripe 或生产数据库。

沿用已经确定的业务规则：金币、整本 MoR 税码为 `txcd_10000000`，VIP 为 `txcd_10302003`；废弃固定 `tax_liability_account` 的配置和履约硬门槛；USD 商品标价、业务账本和结算；取消续费保留已付周期。本轮不重新讨论这些决策。

| 编号 | 优先级 | 问题 | 方案对应项 | 验证 |
| --- | --- | --- | --- | --- |
| R1 | P1 | 被拒绝更新的旧争议观测仍触发自动动作，胜诉订单可再次被冻结 | P1-1、P2-3 | 数据库复现 |
| R2 | P1 | 关闭冻结策略会阻断既有冻结的胜诉解冻及败诉终结 | P1-1 | 数据库复现 |
| R3 | P1 | 新事件分组不继承旧补查进度，升级时遗漏历史积压 | P0-4、P2-3 | 数据库复现 |
| R4 | P2 | 每日报表调度不补漏日，跨日停机造成永久窗口缺口 | P2-2 | 数据库复现 |

## 2. R1 · 旧争议观测绕过状态新旧判定，重复冻结胜诉订单

**定位**：[observation.go:27](../reader-backend/internal/payment/aftercare/observation.go#L27)。关联：[案件更新判定](../reader-backend/internal/payment/aftercare/repository.go#L62)、[冻结分支](../reader-backend/internal/payment/aftercare/mitigation.go#L134)、[观测时间取值](../reader-backend/internal/payment/stripe/aftercare.go#L148)。

`RecordTx` 使用 `observed_at` 防止旧状态覆盖新状态。拒绝更新时，它读取现有案件 ID 并返回成功。随后 `RecordObservation` 无条件把原输入 `evidence.Fact` 传给 `ApplyStripeMitigationTx`，后者按输入状态执行冻结、解冻或停订，没有使用已接受的案件状态。

因此案件表的状态虽然受到保护，权益仍可被旧状态更改。资源回查也不能排除此时序：`AftercareDispute` 在发起 GET 前记录观测时间，较早的查询可以晚于新的胜诉查询完成事务。

### 复现与结果

1. 复用现有 `TestStripeEFWAndDisputeAutoMitigation` 的已履约金币订单：钱包本金 100、赠币 20，开启 `dispute_freeze`。
2. 写入 `needs_response@T1`，钱包归零、订单冻结。
3. 写入同一争议的 `won@T2`，其中 `T2>T1`，正常恢复 100/20。
4. 再提交同一争议的旧 `needs_response@T1`，使用不同 `source_ref` 模拟较早回查晚到。

临时用例 `TestReviewStaleOpenObservationDoesNotRefreezeWonDispute` 的输出：

```text
persisted case status after stale observation: won
unfreeze state mismatch: blocked=true status="frozen" charged=0 bonus=0
```

影响是已胜诉用户再次失去余额或阅读权限，且案件已经为终态，未必还会有新通知帮助恢复。

### 修复与验收

在同一事务中明确区分“观测证据已存档”和“案件当前状态已接受”。自动动作必须基于案件已接受的当前状态；不能因为旧观测只存了审计证据就再次执行旧动作。也可在锁定当前案件后读取其状态及对应证据，执行幂等收敛。处理重复观测时仍应保留原有补偿能力。

增加 `open→won→旧 open`、`open→lost→旧 won` 及相同时间的终态优先测试。断言案件、钱包、权益与冻结标记一致；旧状态不得重新冻结、错误解冻或触发停订，交易流水不得多扣或多加。

## 3. R2 · 关闭策略后，已产生的冻结没有终结路径

**定位**：[mitigation.go:134](../reader-backend/internal/payment/aftercare/mitigation.go#L134)。关联：[全部关闭时提前返回](../reader-backend/internal/payment/aftercare/mitigation.go#L80)、[解冻](../reader-backend/internal/payment/aftercare/loss.go#L169)、[败诉终结](../reader-backend/internal/payment/aftercare/loss.go#L222)。

开启新冻结、胜诉解冻和败诉转销账均位于当前 `policy.disputeFreeze` 条件内。运营在争议处理中关闭该策略后，后续 `won` 不再调用 `ReleaseDisputeFreezeTx`，`lost` 也不再执行 `WriteOffDisputeFreezeTx`。如果三个开关全部关闭，还会在读取原冻结状态之前直接返回。

策略关闭应停止产生新冻结，但既有冻结仍需依据最终争议结果收敛。当前代码会把关停功能的操作变成持续扣留既有权益。

### 复现与结果

1. 在相同现有集成用例中开启冻结，写入正式争议，确认钱包从 100/20 变为 0/0。
2. 更新该账户 `dispute_freeze=false`，其余自动动作开关保持开启，以验证问题不只来自全部关闭的提前返回。
3. 写入同一争议的 `won`。

临时用例 `TestReviewDisablingPolicyStillReleasesExistingFreeze` 输出：

```text
unfreeze state mismatch: blocked=true status="frozen" charged=0 bonus=0
```

### 修复与验收

把“是否允许新增冻结”和“如何清理本系统已经产生的冻结”分开。开启正式争议时读取策略；胜诉、无损终结及败诉时按原冻结记录收敛，不能受当前开关阻断。提前返回也应允许处理待终结的旧冻结。

验收开关关闭前后分别发生 `won`、`lost`、全额退款，覆盖三个开关全部关闭及重投。胜诉只返还实际冻结金额一次；败诉只转换冻结状态，不重复扣减；退款后的终结不得返还已经回收的余额。

## 4. R3 · 分组事件补查升级丢失旧窗口

**定位**：[event_recovery.go:187](../reader-backend/internal/payment/application/event_recovery.go#L187)。关联：[迁移 1940](../cms-backend/db/migrations/2026111940.sql#L32)、[新 worker 仅遍历分组](../reader-backend/internal/payment/application/event_recovery.go#L272)。

旧补查进度存于 `payment_event_recovery`。迁移 1940 创建 `payment_event_recovery_groups`，未初始化已有 Stripe 账户的旧窗口。新 worker 第一次领取每个分组时统一从 `now()-15 minutes` 初始化，不读取旧表的 `window_start`、`window_end` 或未完成页。之后 Stripe 路径只扫描分组。

若升级时旧账户还有积压，或停止 worker 到新版本恢复超过 15 分钟，旧窗口中尚未发现的事件会被跳过。保留旧表并不等于继续处理它。付款可能被独立查单恢复，但 EFW、争议等事件未必有对应补偿，不能依赖它们恰好已通过 Webhook 入队。

### 复现与结果

1. 创建 Stripe sandbox 账户，在旧 `payment_event_recovery` 写入起点为 48 小时前、终点为 47 小时前且带未完成 `page_cursor` 的进度。
2. 不创建新分组记录，模拟旧数据升级完成后首次运行。
3. 对 `RecoveryEventGroups()` 返回的每个分组调用 `claimStripeEventRecoveryGroup`。

临时用例 `TestReviewStripeRecoveryKeepsLegacyBacklog` 输出：

```text
group=group_0 starts only 15m0s ago; legacy backlog starts 48h ago
group=group_1 starts only 15m0s ago; legacy backlog starts 48h ago
```

### 修复与验收

对已有账户从旧表的最早未完成窗口初始化所有新分组，并保留安全重叠。筛选事件集合已改变，旧 `page_cursor` 不能直接复制，应从该窗口起点重新分页，依靠 Inbox 幂等去重。新分组初始化与旧 worker 切换须协调；已有分组记录不得被初始化逻辑重置。超过 Stripe 事件保留期的积压应显式告警并进入资源恢复流程。

验收无旧记录、已追平、未完成页、积压两天、升级停机超过 15 分钟、重复初始化及多个分组独立完成。断言旧窗口内的所有目标事件最终被发现，同一账户事件只入队一次，后续重启不倒退或跳跃。

## 5. R4 · Report Run 调度只看昨天，停机漏日不补

**定位**：[report_schedule.go:214](../reader-backend/internal/payment/settlement/report_schedule.go#L214)。关联：[创建窗口](../reader-backend/internal/payment/settlement/report_schedule.go#L245)。

`ScheduleDueStripeReportRuns` 每次只按当前时间计算 `[昨天 00:00 UTC, 今天 00:00 UTC)`。已有持久化记录能恢复已创建任务，但没有按账户和报表类型保存调度进度，也不枚举历史缺口。跨日停机、权限临时缺失或某天调度失败后，恢复运行只会补昨天；更早没有创建出来的任务永久缺失。

### 复现与结果

同一账户分别以 2026-11-24 和 2026-11-27 作为调度时间调用调度器，模拟中间两天未运行。查询 `payment_stripe_report_runs` 中不同的 `parameters.interval_start`。

临时用例 `TestReviewReportScheduleCatchesUpMissingDays` 输出：

```text
scheduled day windows=2, want 4 (Nov 23,24,25,26)
```

实际仅生成 11 月 23 日及 26 日两天的两类报表，24、25 日的税费及结算报表未被调度。

### 修复与验收

按账户、报表类型保存下一待调度日期，或从明确的启用日期和已有窗口推导缺口，逐日补齐至最近完成的 UTC 日。创建任务与推进调度进度应具备事务保证，继续复用已有参数摘要唯一键。单轮设置补齐上限，后续轮次继续推进。

验收连续运行、停机三天、部分账户失败、权限恢复及重复调度。每日报表窗口完整、每个窗口只调度一次，已运行和已导入任务不重复导入。

## 6. 最终方案逐项核对

| 方案项 | 本轮结论 |
| --- | --- |
| P0-1 税码 | 已实现按三类商品映射；保留本轮确定的税码。具体商品准入及真实交易验收仍需对应渠道证据。 |
| P0-2 固定税务账户 | 已移除新绑定硬门槛，迁移放宽旧表约束；实际返回值继续入证。未发现需要恢复固定账户配置的理由。 |
| P0-3 本地币及 USD 结算 | 已接入原币证据、参数冻结和待结算重试；单次、VIP 多付款分配及 Customer 聚焦数据库用例通过。真实 Stripe 金额关系仍需实单确认。 |
| P0-4 Webhook | 已有统一信封、资源回查和实时/补查分派；相关集成回归通过。升级到分组补查仍受 R3 阻断。 |
| P0-5 凭据 | 已有用途凭据解析及目录接入；Reader 相关聚焦用例通过。未验证真实账户 Restricted Key 权限。 |
| P0-6 实单样本 | 本轮未执行 Stripe Sandbox 实单、Live 或银行到账验证；模拟测试不能替代此项。 |
| P1-2 → P1-1 退款及自动动作 | 退款并发守卫、结果确认及正常自动处置用例通过；R1、R2 尚未满足乱序和关停边界。 |
| P1-3 Radar | 超时回查、明确拒绝、Dashboard 与本地同时批准及通知重投等聚焦用例通过。 |
| P1-4 订阅管理 | H5 入口、能力区分、取消归属及幂等用例通过；真实 Direct Portal 和 MoR Link 未打开验证。 |
| P1-5 VIP 防重 | 六支付流程通过，含升级前缺替换标记、PayPal→Stripe→PayPal 两种到账顺序及迟到首款用例。 |
| P2-1 Customer | 并发幂等与地址精简的数据库用例通过；付款方式实际保存和授权体验未做 Stripe 验证。 |
| P2-2 报表 | 已有创建、查询、入队及税费列；相关恢复和去重用例通过，但 R4 导致漏日。MoR 列保持显式启用。 |
| P2-3 回补及举证 | 已有分组、重试和证据导出；本轮确认 R3。Visa CE 3.0 保持 Stripe 确认门槛。 |
| P2-4 原币展示及 409 | 原币金额格式化、未知历史值及 409 查询原意向的前端回归通过。 |
| 共享 schema | CMS、Reader、Conversion 当前均要求 `2026111941`；迁移及发布仍需按方案的服务依赖顺序执行。 |

审查中还核对了 Customer 地址更新参数：MoR 会自动同步 Customer 姓名和账单地址，且禁止传入 `customer_update[name/address]`；没有把“缺少此参数”列为缺陷。[Stripe 官方 MoR 参数说明](https://docs.stripe.com/payments/managed-payments/update-checkout#remove-unsupported-parameters)

## 7. 验证记录及限制

### 7.1 现有回归

| 检查 | 结果 |
| --- | --- |
| Reader `stripe`、`application`、`aftercare`、`settlement`、`directory` 五个支付包初次测试 | 通过；初次未提供数据库，数据库用例会跳过，后续单独补跑下列聚焦用例。 |
| PostgreSQL：`TestStripeEFWAndDisputeAutoMitigation` 原用例 | 通过。 |
| PostgreSQL：单次本地币待结算、VIP 多分配、Customer 并发、退款守卫、Radar、分组回补、统一信封、取消归属及幂等 | 通过，`application` 聚焦执行 27.567 秒。 |
| PostgreSQL：Report Run 重启与重复导入、缺权限及手工导入兼容、动态凭据 | 通过，`settlement` 聚焦执行 16.618 秒。 |
| PostgreSQL：CMS `internal/billing`、`internal/db` | 通过，分别 50.122 秒、46.450 秒。 |
| PostgreSQL：`TestSixH5PurchaseFlowsEndToEnd` | 通过，10.982 秒；含 `UnifiedVIPReviewRegressions` 和 `UnifiedVIPRereviewRegressions`。 |
| Conversion `internal/delivery` | 通过；未提供数据库，不作为实际部署和共享库连接验证。 |
| Reader H5 与 CMS 前端 `npm run build` | 均通过。CMS 有既有构建 chunk 大小提示。 |
| Reader 409 与订阅管理 Node 测试 | 7 项通过。 |
| CMS 商品目录 Node 测试 | 6 项通过。 |
| CMS 争议原币展示 Node 测试 | 使用 Node 22 的 `--experimental-strip-types`，2 项通过。 |

聚焦数据库命令：

```bash
# 使用一次性数据库的 DSN，禁止指向业务数据库。
STAR_NOVEL_TEST_DATABASE_URL='<disposable-postgres-dsn>' go test -count=1 -p 1 \
  ./internal/payment/application ./internal/payment/settlement \
  -run '^(TestStripe(SingleLocalCurrency|VIPMultiAllocation|CustomerConcurrent|RefundGuard|Radar|GroupedEventRecovery|UniversalEnvelope|ReportRunRestart|ReportScheduleMissing|AutomaticPayout)|TestCancelSubscriptionIdempotency)'
```

### 7.2 本轮新增边界复现

临时 overlay 复用现有集成夹具并加入第 2～5 节的最小事件/时间变化。4 个用例均失败，输出已逐项记录；这些失败是当前实现缺陷的复现结果，不计入“原有测试通过”。修复时应把相应断言加入正式测试。

数据库为本轮独立启动的 `postgres:16-alpine` 容器，测试使用独立 schema；未使用既有业务库。测试结束后删除本轮容器及临时 overlay。

### 7.3 证据边界

本轮完成代码、迁移和模拟状态机核对，没有提交或推送代码，也未执行部署、Stripe 真实 API 写入、真实付款、真实 Restricted Key 权限测试或浏览器人工支付。上线前仍需按方案 P0-6 记录原始渠道对象、Webhook 签名和投递、USD 余额流水、首扣/续费/退款及管理入口证据。历史线上快照未作为本轮当前生产状态的结论。

## 8. 修复顺序

1. 先修 R1，统一案件已接受状态与自动处置依据，补乱序终态回归。
2. 修 R2，确保关停开关不阻断原冻结终结；联合回归退款及本金损失，核对无重复扣返。
3. 上线新分组回补前修 R3，并对已初始化过的新分组核查是否已有遗漏窗口，按原账户和环境补查。
4. 修 R4，补齐未调度日期后再把报表覆盖范围作为对账验收依据。
5. 四项复验通过后完成方案要求的渠道实单验收，按账户和模式开放相应能力。

## 9. 第 2 轮：R1–R4 修复复审

2026-11-27 · 修复提交 `d4f4cc0` · 对比基线 `9482c4e` · 本轮仅更新本报告，未修改业务代码、迁移或正式测试。

### 9.1 原问题逐项结论

| 项目 | 状态 | 本轮验证与边界 |
| --- | --- | --- |
| R1 · 旧争议观测重复冻结 | 复验通过 | 改为锁定并依据已接受的案件状态处置。`open→won→旧 open`、`open→lost→旧 won`、相同时间终态优先均通过，余额与冻结状态未被旧状态改回。 |
| R2 · 策略关闭阻断终结 | 复验通过 | 胜诉解冻和败诉终结已脱离当前冻结开关。仅关闭冻结、全部开关关闭、终态重投及全额退款后胜诉不返币均通过。 |
| R3 · 旧事件窗口丢失 | 代码修复复验通过 | 新分组继承旧窗口起点并清空页游标；积压 48 小时、分组推进后重领不重置、新账户 15 分钟起点、超过 30 天显式记录错误均通过。原有多分组分页用例也通过。 |
| R4 · 停机漏日报表 | 部分修复，仍为 P2 | 有至少一条历史日报任务后，停机补日、重复调度和每轮补齐上限均通过；完全没有成功生成过任务时仍从昨天开始，见 9.2。 |
| R5 · 报表覆盖去重忽略参数 | 新增 P2，待修 | 打开 MoR 税费列后，旧的普通日报被当作新参数日报的覆盖证明，未生成所需税费报表，见 9.3。 |

R3 修复使用 `ON CONFLICT DO NOTHING` 保护已有分组进度。它不会追溯修正旧版本已经创建且跳过窗口的分组；如果旧版本实际运行过，应按原账户、环境核对缺口，并用现有 `SchedulePaymentEventRecovery` 从遗漏起点补查。本轮没有访问线上，不能据此宣称历史积压已清空。

### 9.2 R4 残留 · 首次成功调度前的漏日没有恢复起点（P2）

**定位**：[report_schedule.go:268](../reader-backend/internal/payment/settlement/report_schedule.go#L268)。关联：[真实 worker 的配置构造](../reader-backend/internal/payment/application/settlement_worker.go#L63)。

新实现从已有日报的最早日期开始枚举缺口；没有任何日报记录且 `StartDate` 为空时，仍把 `firstStart` 设为昨天。真实 worker 只设置报表类型和 MoR 列开关，没有设置 `StartDate`，仓库中也未找到对应的运行配置入口。

因此，账户已经启用，但在首次成功生成任务之前遇到操作者失效、缺权限或连续调度失败，恢复时仍丢失前几天。新增正式回归先在 11 月 24 日成功生成两条日报，再模拟停机，未覆盖这个起始阶段。

**数据库复现**：

1. 创建已启用的 Stripe 对账账户，保存 11 月 23～24 日窗口。
2. 临时停用配置操作者，在 11 月 24 日调用真实 `ScheduleDueStripeReportRuns`。因为权限检查不通过，返回 0 条任务。
3. 恢复操作者，在 11 月 27 日再次调用，配置与真实 worker 一致，`StartDate` 为空。
4. 预期从已请求调度的 11 月 23 日补至 26 日，实际只有 26 日。

临时用例 `TestReReviewScheduleBeforeFirstSuccess`：

```text
restored actor: created=2, daily windows=1; want 4 days Nov 23..26
```

**修复建议**：为每个账户及报表类型保留明确且稳定的启用/计划起点，首次调度受阻时也不能丢失这一起点；创建任务和推进计划保持一致。将 `StartDate` 接入可用的恢复入口也可作为补偿手段，但当前仅增加结构字段不足以解决真实 worker 的恢复问题。不要直接使用会随 Payout 同步推进的 `window_start` 作为长期报表游标。

**验收**：首次调度前操作者失效、权限恢复、首次某一报表类型写入失败、进程跨日恢复，且始终使用实际 worker 的配置路径。断言从原启用/计划日期补齐，而非从首次成功创建任务的前一天重新开始；已覆盖日期不重复。

### 9.3 R5 新增 · 日报去重只按日期，忽略所需税费列（P2）

**定位**：[report_schedule.go:280](../reader-backend/internal/payment/settlement/report_schedule.go#L280)。关联：[覆盖查询](../reader-backend/internal/payment/settlement/report_schedule.go#L307)、[日期归一化](../reader-backend/internal/payment/settlement/report_schedule.go#L330)、[完整参数生成摘要](../reader-backend/internal/payment/settlement/report_schedule.go#L111)。

新 `loadScheduledDailyReportStarts` 仅按账户、环境、报表类型及 24 小时时长收集日期。调度器看到日期存在就跳过，尚未进入按完整参数生成 `schedule_key` 的路径。因此无税费列的旧日报会挡住后来显式启用 MoR 税费列的日报。旧任务虽然仍可正常导入，但无法提供新配置要求的 `withheld_tax` 等证据。

这也改变了修复前的参数幂等语义：此前新参数会生成不同摘要和独立任务；当前新增的日期级去重提前吞掉了该任务。问题不要求覆盖或修改旧任务，应为不同的有效参数保留独立任务。

**数据库复现**：

1. 在 11 月 27 日按默认配置调度两类日报，正常生成两条 `include_mor_columns=false` 任务。
2. 同一天按真实 worker 可达配置切换为 `IncludeMoRColumns=true` 并再次调度。
3. 预期为当前所需的列参数生成两条新任务，实际返回 0，库中仍无 MoR 列任务。

临时用例 `TestReReviewScheduleMoRColumnsChange`：

```text
MoR columns enabled: created=0, MoR schedules=0; want 2
```

同一覆盖函数还有一个相关边界：经公开的 `EnsureReportRunSchedule` 创建 11 月 24 日中午至 25 日中午的 24 小时报表后，它会被截断并当作 24 日完整 UTC 自然日报。临时用例 `TestReReviewScheduleManualOffsetWindow` 输出：

```text
manual offset report masks daily coverage: created=1, exact daily schedules=1; want 2
```

当前后台 worker 自己只生成 UTC 自然日报，因此非自然日例子属于该存储接口的边界复现，不另计一项生产缺陷；MoR 列开关变化可以直接通过现有配置触发。

**修复建议**：按所需完整规范参数/摘要判断覆盖，至少匹配精确的自然日起止、币种/筛选、列集合与 MoR 列要求；不要把任意 24 小时记录截断成自然日后当作完整覆盖。复用已有 `BuildReportScheduleKey` 与唯一键即可保持相同参数幂等，旧参数任务和导入记录应继续保留。

**验收**：普通列→MoR 列切换、配置不变重复调度、已有其他列集合/筛选任务、非自然日窗口。断言新需求能创建对应任务，同参数不重复，旧报表不被误计为完整覆盖，既有导入不会被重复执行。

### 9.4 本轮验证记录

本轮新建独立 `postgres:16-alpine`，所有数据库测试使用独立 schema。没有连接原有业务数据库或 Stripe。

| 检查 | 结果 |
| --- | --- |
| 正式 R1–R4 用例：三包 `-run '^TestReview'` | 4 个顶层用例全部通过；分别执行 6.538、3.327、3.172 秒，未跳过数据库测试。 |
| 原有自动处置、退款守卫、失败事件重试、分组分页、Report Run 重启及手工导入兼容 | 三包通过，分别执行 3.917、19.802、8.530 秒。 |
| 共享售后本金损失、权益生命周期及自动指派权限 | 通过，6.484 秒。 |
| 新增临时边界用例 | 首次恢复起点、MoR 列切换、非自然日覆盖共 3 个用例均失败；输出见上文，对应 2 项未解决问题。 |
| 修改范围 | 仅修改本 Review 文档；没有修改业务代码和正式测试，没有提交、推送或部署。 |

正式用例复跑命令（在 `reader-backend` 执行）：

```bash
STAR_NOVEL_TEST_DATABASE_URL='<disposable-postgres-dsn>' go test -count=1 -p 1 -v \
  ./internal/payment/aftercare ./internal/payment/application ./internal/payment/settlement \
  -run '^TestReview'
```

新增边界用例使用临时 Go overlay，本轮结束后清理 overlay 和专用数据库容器。修复时应把对应输入及断言补入正式用例。

本轮变动集中在后端案件与调度，未重复执行无变更的前端构建或扩大为全仓回归。Stripe Sandbox 实单、Live、银行到账和历史分组线上补查仍不在本轮验证范围。报表能力仍有上述两项 P2，不能仅凭四个原用例通过就宣称完整方案全部验收完成。

## 10. 第 3 轮：R4–R5 修复复审

2026-11-27 · 修复提交 `09903c6` · 对比基线 `58ea95c` · 本轮仅更新本报告，未修改业务代码、正式测试或迁移。

### 10.1 当前状态

| 项目 | 结论 | 本轮证据 |
| --- | --- | --- |
| R1、R2、R3 | 复验继续通过 | 三条对应正式测试通过，包含旧状态重放、策略关停终结和旧补查窗口继承。 |
| R4 · 补日报表 | 部分修复，剩余 P2 | 停机补日和操作者恢复原用例通过；首次日报生成前，由正常 Payout 同步推进的起点仍会覆盖原计划日期，见 10.2。 |
| R5 · 参数覆盖去重 | 复验通过 | 改为生成完整 `schedule_key` 后去重；MoR 列切换生成独立任务、再次运行不重复，非自然日窗口不再掩盖自然日报。 |
| 显式起始日期 | 代码接入已核对，补偿对照通过 | 环境变量 `STRIPE_REPORT_START_DATE` 由配置层读取，worker 按 `YYYY-MM-DD` 解析并传入调度器。同一数据库复现中，指定原起点后补回全部 19 天。 |

本轮未新增问题编号，R4 继续保留；没有在修复范围内发现新增 P0/P1。

### 10.2 R4 残留 · 首次报表生成前，Payout 仍可覆盖原始起点（P2）

**定位**：[report_schedule.go:237](../reader-backend/internal/payment/settlement/report_schedule.go#L237)、[首次调度回退](../reader-backend/internal/payment/settlement/report_schedule.go#L290)。关联：[Payout 成功后滚动窗口](../reader-backend/internal/payment/settlement/automatic.go#L45)、[两个 worker 独立调度](../reader-backend/internal/payment/application/reconcile.go#L43)、[显式日期接入](../reader-backend/internal/payment/application/settlement_worker.go#L68)。

代码把 `payment_ledger_sync_settings.window_start` 读取到 `initialSyncStart`。但此字段不是不可变的初始起点：`SyncNextLedgerPage` 每次成功完成 Payout 分页都会将其改为 `now()-7 days`。在任何报表类型都还没有自然日报记录、且未配置 `StartDate` 时，调度器仍直接使用这个已经移动的窗口。

例如报表类型配置暂时无效或首次计划写入持续失败，报表 worker 不能创建起点记录，但 Payout worker 可以正常推进。待报表恢复时，早期日期已经不可推导。第一条较晚日报创建后，后续又以这条记录为稳定起点，遗漏日期不会被默认路径自动补回。

本次正式用例验证的是“先成功创建日报，再修改 Payout 窗口不影响起点”，这一路径已通过；缺少的是“首次日报生成之前，窗口已经被其他 worker 修改”的时序。

**数据库及真实 worker 复现**：

1. 设置账户原同步窗口为 11 月 8～15 日，使其可被 Payout worker 领取，库中尚无日报。
2. 模拟 11 月 15 日报表类型配置错误，调用真实调度器并确认返回配置错误、未产生日报。
3. 在 11 月 27 日调用真实 `SyncNextLedgerPage`。仅 Stripe HTTP 使用内存模拟，返回归属正确的账户与空 Payout 列表；真实数据库更新将共享窗口推进至 11 月 20 日。
4. 恢复有效报表配置，不指定 `StartDate`，调用调度器三轮，允许默认每轮 7 天上限充分完成应有的 19 天补齐。
5. 查询不同自然日窗口：实际只创建 11 月 20～26 日共 7 天，11 月 8～19 日共 12 天遗漏。

临时用例 `TestReReviewFirstReportAnchorSurvivesPayoutBeforeFirstRun` 输出：

```text
original start=2026-11-08T00:00:00Z; payout worker changed window_start to 2026-11-20T10:39:58Z
first report start=2026-11-20T00:00:00Z, scheduled days=7; want original start=2026-11-08T00:00:00Z and 19 days
explicit StartDate control: restored all 19 days from 2026-11-08T00:00:00Z
```

最后一行是正向对照：向同一调度器显式传入 `StartDate=2026-11-08`，再跑三轮，确实恢复全部 19 天。新增环境配置提供了可用的补偿入口，本项不是“无法恢复”，而是默认自动恢复仍可能产生未告警的覆盖缺口。

**修复建议**：在账户启用报表或建立初始计划时持久化独立、不可随 Payout 推进的报表起点，并在创建第一条日报前就保存；报表类型暂时无效、操作者失效和首次写入失败都不能丢失这一计划。若选择依赖显式日期，应在缺少可靠起点时要求配置该日期并明确报错，避免默默把滚动窗口当作完整历史起点。已有历史缺口可使用新增日期配置补查，无须删除或重建原任务。

**验收**：复用上面的两个真实 worker，覆盖先 Payout 后首个日报、报表失败期间 Payout 多次推进、仅一个报表类型先成功、显式日期补偿及补完后的重跑。断言保留原始计划日期、所有自然日最终覆盖、每轮仍受上限控制、相同参数幂等。

### 10.3 本轮验证记录

本轮独立启动 `postgres:16-alpine`，使用测试隔离 schema；Stripe HTTP 为内存模拟，没有访问 Stripe 或生产数据库。

| 检查 | 结果 |
| --- | --- |
| 正式 R1–R5 回归 `TestReview*` | 7 个顶层用例全部通过；覆盖 R4 原停机及操作者恢复、R5 列切换及非自然日窗口。 |
| Report Run 重启及重复导入、缺权限及手工导入兼容、动态凭据和自动 Payout | 3 个顶层用例全部通过。 |
| 以上两组实际执行 | `aftercare` 10.776 秒、`application` 4.795 秒、`settlement` 23.972 秒；均提供一次性数据库，未因缺数据库跳过。 |
| 新增 Payout→首日报交叉用例 | 默认起点断言失败，确认 R4 残留；同一用例的显式 `StartDate` 补偿对照通过。最终复现执行 2.982 秒。 |
| 修改与清理 | 仅修改本报告；临时 overlay 与本轮数据库容器在结束后删除，未提交、推送或部署。 |

正式用例命令（在 `reader-backend` 执行）：

```bash
STAR_NOVEL_TEST_DATABASE_URL='<disposable-postgres-dsn>' go test -count=1 -p 1 -v \
  ./internal/payment/aftercare ./internal/payment/application ./internal/payment/settlement \
  -run '^(TestReview|TestStripeReportRunRestart|TestStripeReportScheduleMissing|TestStripeAutomaticPayout)'
```

本轮未重复执行无改动的前端构建和全仓回归。结论限于所审查提交及上述数据库/模拟用例，不代表 Stripe Sandbox 实单、Live 及银行到账验收完成。
