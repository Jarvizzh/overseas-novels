# 回传中心 Code Review

评审日期为 2026-11-23。基线为 `codex/payment-domain` 的 `78d2ff1` 及评审时的本地工作区。检查范围包括 CMS 授权接入和 Pixel 管理、Reader 事件生产、司南测试接入、Meta CAPI、回传事件查询、逐单对账、独立采集器与监控页。

## 结论

**当前工作区有 3 项 P1。** 随 CMS 最新迁移发布会导致所有新启动的回传进程退出。即使使用兼容的数据库版本，Reader 事件白名单仍不会生效，退役 Pixel 也会挡住已冻结归因的付款。发布前应优先处理这三项。另有 8 项 P2 和 1 项 P3。

**Meta CAPI 存在可复现的本地吞吐和限流处理问题。** 默认单 Worker 在多个可领取 Pixel 间合计约能发起 20 次投递/分钟；流量集中到单 Pixel、数据库和响应较快时，通常只有 4 次/分钟。明确拒绝的任务会退避，同 Pixel 后续任务仍继续请求。生产入队峰值、实际 429 比例及账户额度未实测，因此无法判断线上已经丢失多少转化。

| 优先级 | 编号 | 影响 |
| --- | --- | --- |
| P1 | R01–R03 | 升级后服务无法启动、Reader 白名单失效、既有付款被退役目标阻塞 |
| P2 | R04–R06 | 峰值积压、注册与结账被支付挤占、同 Pixel 在限流期间继续请求 |
| P2 | R07–R11 | 逐单环境误判、告警失去时效或遗漏失败、汇总故障阻断对账、司南白名单提示错误 |
| P3 | R12 | 单事件超过 50 个目标时，后续投递无法从 CMS 下钻 |

## 授权接入与事件生产

**R01 · P1　CMS 迁移到 2026111930 后，回传进程无法启动。** CMS 和 Reader 均要求 `2026111930`，Conversion 对迁移表最大版本作严格相等检查，仍要求 `2026111929`。该检查在模式分发前执行，影响 API、Worker、Collector 和两个分销模式。隔离库先通过旧版检查，再执行工作区的真实迁移，得到 `schema version 2026111930, required 2026111929`。这项缺陷来自尚未提交的跨服务组合。已运行进程不会因该启动检查立即退出，重启或滚动发布会触发。修复时统一三个服务的兼容版本，并让跨服务启动测试使用 CMS 正式迁移清单。现有回传集成测试自行枚举迁移到 1929，整套通过仍会漏检。 [CMS 迁移](../cms-backend/db/migrations/2026111930.sql) · [Reader 门槛](../reader-backend/internal/db/postgres.go#L109) · [Conversion 门槛](../conversion-backend/internal/delivery/store.go#L67) · [启动顺序](../conversion-backend/cmd/conversion/main.go#L58) · [测试迁移清单](../conversion-backend/internal/delivery/service_test.go#L42)

**R02 · P1　Reader 不执行 CMS 保存的事件白名单。** CMS 对 StarNovel 提供四种事件勾选，保存到 `allowed_events` 并写配置审计。Reader 领取只检查账户、环境和 `enabled`，归一化用写死的四种事件名判断。运营即使只选 `Purchase`，满足归因和上下文条件的注册、结账仍可创建待发任务。建议为 Reader 明确空列表和既有账户的迁移语义，让各入口使用相同的事件准入判断，并给被排除的事件留存可对账的原因。验收应经 CMS 保存“只允许购买”，检查新注册、结账均无可投递任务，购买仍按付款规则处理。 [勾选和保存](../cms-front/src/components/conversion/IntegrationConfigDrawer.tsx#L34) · [服务端保存](../conversion-backend/internal/delivery/configuration.go#L214) · [Reader 领取](../conversion-backend/internal/delivery/reader.go#L219) · [事件判定](../conversion-backend/internal/delivery/reader.go#L101)

**R03 · P1　Pixel 退役使已冻结归因的付款无法投递或恢复。** 设计要求退役 Pixel 继续服务历史快照、待付款订单和活跃订阅；退役检查只看当前推广链接。移走推广绑定后，旧订单仍带原 Pixel。若退役发生在归一化之前，Reader 将原目标标记为 `destination_disabled`，付款规则拿不到有效原目标，人工重排又要求 `retired_at IS NULL`。隔离测试通过真实付款 SQL 入队、调用退役操作、消费并重排，得到 `pending / blocked / destination_disabled` 和 `state_conflict`。修复时用已冻结的原始归因及有效授权判定历史原目标，继续禁止给退役目标建立新推广或附加路由；区分退役与主动停发。验收覆盖退役前后归一化、退役后的既有订阅续费及凭据轮换。 [路由设计](conversion-reporting-architecture.md#L115) · [退役检查](../conversion-backend/internal/delivery/configuration.go#L65) · [Reader 原目标](../conversion-backend/internal/delivery/reader.go#L231) · [付款重排](../conversion-backend/internal/delivery/payment_delivery.go#L225) · [恢复条件](../conversion-backend/internal/delivery/reader_payment.go#L193)

## Meta CAPI 吞吐、调度与限流

Meta 说明 CAPI 调用计入 Marketing API，没有独立固定的 CAPI 请求速率；一次可提交最多 1,000 个事件。官方也建议事件发生后尽快发送。以下容量按代码和隔离运行估算，计量单位为**目标投递尝试**。一个业务事件扇出三个 Pixel 就有三次投递，首笔订阅的 Purchase 和 Subscribe 以及后续重试都会继续占用容量。 [Meta CAPI 使用说明、批量上报与 API Limits](https://developers.facebook.com/documentation/ads-commerce/conversions-api/using-the-api)

| 默认调度条件 | 可从代码推导的容量 | 对业务的含义 |
| --- | --- | --- |
| 单 Worker，多 Pixel 同时可领 | 每 15 秒最多领取 5 条，稳态约 20 次/分钟、1,200 次/小时 | 这是理想上限。每个请求只有一个事件，数据库、网络、重试会消耗余量 |
| 单 Worker，一个热点 Pixel，快速完成一轮 | 通常每 15 秒领取 1 条，约 4 次/分钟、240 次/小时 | 1,000 条已就绪投递在无新流量、无重试时约需 4 小时 10 分钟 |
| 同环境多 Worker | 全部实例共享最多 5 个有效租约；同 Pixel 最多 1 个，领取间隔至少 3 秒 | 即使实例轮询完全错开，单 Pixel 理想上限仍是 20 次/分钟；实际还受单次在途耗时限制 |

**R04 · P2　Worker 刚释放的发送能力要等下一轮才使用。** 发送路径每轮最多领取五次，遇到无可领任务立即结束领取，等已启动任务全部完成后回到 15 秒定时器。单热点的 3 秒节流到期时没有新的领取动作。Reader 归一化也与发送同轮，每轮至多消费 20 条，且共享 3 秒归一化时限。本地初始任务通常只有 24 小时寿命，持续超过消费能力可从积压演变为到期失败。建议把归一化和发送的消费节奏解耦，发送侧按空闲槽及下一次可领取时间补位；把并发、间隔和批量变成可配置项。采用同 Pixel 微批前要覆盖部分接收、超窗事件、未知响应与逐目标回执，避免仅凭批次状态把全部事件写成成功。 [运行参数](../conversion-backend/cmd/conversion/main.go#L103) · [Worker 循环](../conversion-backend/internal/delivery/worker.go#L38) · [共享租约和间隔](../conversion-backend/internal/delivery/store.go#L82) · [单事件请求](../conversion-backend/internal/media/meta/client.go#L49) · [截止时间](../conversion-backend/internal/delivery/enqueue.go#L65)

**R05 · P2　支付持续积压时，注册和结账可以一直领不到任务。** 非支付优先权固定在当轮第五次领取。同一热点 Pixel 被第一笔支付占用后，第二次领取可能直接返回无行，循环提前结束。下一轮又从支付优先开始。隔离样例中注册比三笔支付更早就绪，32 秒内三轮依次尝试三个 Purchase，注册仍为 `pending`、尝试数为 0。若支付输入持续补满，这种情况可延续至注册事件到期。建议在轮次之间保留调度进度或按任务等待时长提升优先级；验收应在持续支付流量下检查同一 Pixel 的非支付任务最长等待时间。 [领取顺序与提前退出](../conversion-backend/internal/delivery/worker.go#L76) · [类别回退条件](../conversion-backend/internal/delivery/service.go#L108) · [按类别与目标挑选](../conversion-backend/internal/delivery/store.go#L87)

**R06 · P2　明确的限流退避只作用于当前任务。** Meta 客户端识别结构化 429、若干 Graph 错误码和瞬态错误，解析 `Retry-After`，并与指数退避加抖动取较晚时间。完成事务只更新本条投递；同 Pixel 下一次可领取时间仍是之前设置的 3 秒。隔离 Worker 模拟一小时退避，随后两轮继续对同一 Pixel 发起两笔新尝试。共享额度受限时会放大拒绝数和积压。建议区分限流与其他瞬态错误，至少对明确限流的目标持久化冷却时间；有实际额度归属证据时再扩展到共享凭据或账户。验收要求重启或多实例也遵守退避，独立目标仍能投递，传输结果未知仍按原有审计流程处理。 [响应分类](../conversion-backend/internal/media/meta/client.go#L111) · [退避](../conversion-backend/internal/delivery/service.go#L46) · [任务完成事务](../conversion-backend/internal/delivery/service.go#L87) · [目标领取时间](../conversion-backend/internal/delivery/store.go#L100)

## 对账、监控与运营操作

**R07 · P2　逐单对账可能把已有环境证据的订单标成查询服务的环境。** 本地订单 SQL 以 `COALESCE(capi_expectation->>'environment',$2)=$2` 判断归属，然后把 `$2` 写入响应。待支付订单通常还没有资格快照，但已有 `checkout_environment`。如果两个环境共库，一笔明确为 sandbox 的待支付订单可在 production 查询中被标为 production。独立数据库部署不会出现这例串查。修复时优先使用资格快照，缺失时使用已有结账或收款证据；证据不足的历史记录显示环境未知。验收覆盖共库的两笔不同环境待支付单及无环境证据旧单。 [逐单查询](../conversion-backend/internal/operations/orders.go#L101) · [返回环境](../conversion-backend/internal/operations/orders.go#L166) · [订单环境字段](../cms-backend/db/migrations/2026111917.sql#L1) · [CMS 环境标签](../cms-front/src/components/conversion/ConversionOrdersTab.tsx#L270)

**R08 · P2　Collector 停止或持续采样失败时，监控页仍可显示健康。** Collector 每 30 秒运行一次；成功采样不会留下可供汇总 API 判断新鲜度的采样时间。页面查询的是现存活动告警。若最后一次采样没有告警，Collector 停止后即使还有新异常，页面刷新也能显示“投递与回告链路健康”。建议在采集事务中按环境记录最近成功采样时间，由 API 独立判断过期，页面显示采集停滞。验收时保持 API、Worker 正常，仅停止 Collector，刷新后应在约定时限内显示数据过期，恢复后自动解除。 [采集循环](../conversion-backend/cmd/conversion/collector.go#L13) · [告警与心跳读取](../conversion-backend/internal/operations/monitor.go#L233) · [健康提示](../cms-front/src/components/conversion/ConversionSummaryTab.tsx#L352)

**R09 · P2　已明确失败的投递可以从活动告警中消失。** 采集器覆盖超时待发、部分阻塞原因和结果未知，没有按近期终态失败或持续明确限流建立告警。隔离样例中一条原目标 Purchase 因 `meta_invalid_parameter` 定稿为失败，目标统计正确显示失败一条，Collector 返回零次状态变化、活动告警为零。若一批请求均遭明确拒绝，队列还会随定稿变空。建议基于每 Pixel 的近期失败数或失败率、限流拒绝次数及到期数聚合告警，排除策略主动跳过，区分媒体拒绝与本地校验失败；验收应同时验证触发、去重和清除。 [永久拒绝分类](../conversion-backend/internal/media/meta/client.go#L126) · [告警条件](../conversion-backend/internal/operations/monitor.go#L282) · [定稿状态](../conversion-backend/internal/delivery/service.go#L42)

**R10 · P2　监控汇总超时会使正常的事件和逐单对账页面一起不可用。** 进入回传中心任一标签都会请求 24 小时汇总，以显示告警角标。三项基础请求只要一项返回 503，父组件便设置全局不可用；两个查询标签也随之进入服务不可用视图。即使订单按主键查询正常，数据量变大造成的汇总超时也足以阻断对账。建议角标单独降级，各标签按自身查询结果展示状态；验收模拟仅汇总接口超时，事件查询和逐单查询应仍可使用。 [并发加载及全局标志](../cms-front/src/pages/ConversionCenterTab.tsx#L91) · [对账降级分支](../cms-front/src/components/conversion/ConversionOrdersTab.tsx#L186) · [私网请求时限](../conversion-backend/internal/operations/http.go#L68)

**R11 · P2　司南应用白名单留空的实际行为与配置界面相反。** 表单注明“留空代表不限应用”，允许保存空数组。司南回调入口要求应用 ID 确实存在于数组中，空数组将拒绝每一个应用，即使该应用与 Pixel 的映射正确。建议保留显式授权语义，在司南启用前校验应用配置，并修正表单提示。验收覆盖空数组、已允许应用和未列出的应用。影响范围为仓库已实现的司南模拟接入，正式协议仍需联调。 [表单文案](../cms-front/src/components/conversion/IntegrationConfigDrawer.tsx#L668) · [回调准入](../conversion-backend/internal/partner/sinan/inbox.go#L130) · [运行环境限制](../conversion-backend/cmd/conversion/partner.go#L17)

**R12 · P3　事件超过 50 个目标时，后续目标无法从 CMS 逐项排查。** 目标投影只返回原目标优先的前 50 行。事件列表虽能按某个 Pixel 和失败状态找到该事件，打开详情仍沿用相同投影，无法取得排在第 51 位之后的目标任务 ID、回执和重排入口。页面会提示截断，但没有目标分页。建议为目标列表增加游标，或支持按已选 Pixel 获取对应投递；用至少 51 个目标、最后一个失败的事件验收从筛选到操作的完整路径。 [目标聚合](../conversion-backend/internal/operations/orders.go#L75) · [事件筛选与投影](../conversion-backend/internal/operations/events.go) · [详情截断提示](../cms-front/src/components/conversion/ConversionEventDrawer.tsx#L754)

## 验证记录与上线验收

| 检查 | 结果与证据边界 |
| --- | --- |
| Conversion 全量回归 | Go 1.26.10、一次性 PostgreSQL 16，设置测试 DSN 后执行 `go test -race -p 2 -count=1 ./...`，全部通过。覆盖投递生命周期、受限角色、操作接口、逐单查询、告警、付款规则及司南模拟链路 |
| 工作区最新迁移 | 在真实 1929 测试库应用 1930 迁移，再调用正式启动检查，复现 R01 |
| 冻结付款归因 | 真实付款入队 SQL、退役、归一化和审计重排路径复现 R03 |
| 单热点 Worker | 真实数据库、正式 Worker 和默认节奏，替换媒体传输以注入一次可重试拒绝和一小时退避；32 秒调用顺序为 p1、p2、p3，最终 p1 待重试、p2/p3 成功、注册尝试数为零。该实验验证本地调度行为 |
| 告警 | 使用正式 Collector 查询和汇总 SQL，失败投递出现在目标统计，活动告警为零。Collector 离线的显示结果按查询及界面分支核对 |
| 外部边界 | 未读取生产队列或 Meta 使用量，未发送真实 CAPI 事件；本次未做浏览器端到端操作。司南结果仅覆盖测试契约。R07 的部署前提需要结合实际数据库拓扑确认 |

上线容量应按**最忙 Pixel** 和**全环境**分别验收。建议用脱敏流量分布在隔离环境回放单热点、多 Pixel 扇出和限流恢复三组负载，分别统计每分钟就绪投递数、首次发送数、明确拒绝数、P95 入队等待、最老待发年龄以及到期数，并单列 Purchase、Subscribe 和非支付事件。先要求实测峰值下积压能在投递窗口内清空、限流期间其它独立目标继续推进，再决定采用多大并发及批量。

已有的事务 Outbox、稳定事件键、发送前落库及结果未知后的审计重排应保留。调整吞吐时，回归应同时检查同一目标的并发领取、明确确认后的本地提交失败、重复入队，以及策略跳过不产生投递。 [事务入队](../conversion-backend/internal/delivery/enqueue.go#L66) · [发送边界](../conversion-backend/internal/delivery/store.go#L137) · [回执恢复测试](../conversion-backend/internal/delivery/receipt_recovery_test.go)
