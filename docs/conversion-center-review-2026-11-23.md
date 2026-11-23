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

本节记录原 Review 基线。后续实施采用同 Pixel 每请求最多 100 条，并分别验收发送端与 Reader 入队到确认的延迟。详见[回传链路吞吐优化方案](conversion-throughput-plan.md)。

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

## 逐项修复建议与验收

补充时复核至 `5ec1513`，CMS、Reader 与 Conversion 的版本门槛仍分别为 1930、1930、1929。以下为建议实施方案；文末的测试记录对应原始缺陷复核。

### 发布、授权与历史归因

| 编号 | 建议改动 | 修复验收 |
| --- | --- | --- |
| R01 | 在本次 1930 发布中同步更新 Conversion 的启动门槛和 README。把跨服务兼容测试接到 CMS 正式迁移入口，消除手工列举迁移文件导致的漏测。今后若新增回传表或字段，同一发布同步更新三个服务和受限角色授权。 | 分别从空库、1929 库运行正式迁移至 1930，使用实际运行角色启动 API、Worker、Collector；在测试环境也启动两个分销模式。检查 API 可读、Worker 有心跳、Collector 成功采样；旧库仍能报告预期的版本错误。 |
| R02 | 在 Reader 领取 Outbox 的事务中读入账户的 `allowed_events`，在分配付款策略计数、创建投递前判定准入。被排除的事件按业务键记录“配置跳过”并消费 Outbox，使逐单对账、汇总和缺行补建识别同一原因。既有账户先按审计区分历史默认空值与显式清空；确认沿用全量的账户写入四种事件，无法判断的账户列入上线核对。建议新配置显式选择事件，空集合表示全部关闭。 | 经 CMS 仅勾选 Purchase，送入注册、结账、购买和首笔订阅样例；只有 Purchase 可发送，Subscribe 有可查的跳过原因，同笔付款只计算一次。重复消费、人工追加或补建均不能绕过此次准入结论。 |
| R03 | 让归一化、付款判定与人工恢复复用同一原目标判定。用订单、订阅或结账时的冻结归因及其来源时间证明退役前已绑定；续费继承该证据。目标仍须启用，且账户、环境及路由授权仍有效。退役标记继续限制新推广和新附加目标。对已有阻塞任务按原因、截止时间、既有发送证据筛选，沿用原任务和 Meta 事件 ID 恢复。 | 同一 Pixel 同时准备旧待付款单、既有订阅和新推广。退役后前两者能按原路由投递，新绑定被拒绝；再主动停用或撤销授权，历史任务也应停止。轮换 Token 后重排同一任务，已确认投递不得再发。 |

### Meta 调度与限流

建议先完成 R06 的共享退避和 R09 的观测，再合入 R04 的加速。R04 与 R05 一起压测，避免吞吐提高后仍有某一类事件长期得不到发送机会。

| 编号 | 建议改动 | 修复验收 |
| --- | --- | --- |
| R04 | 将归一化、到期扫描和发送分开调度。发送槽释放即触发下一次有界领取；暂时无任务时，按最早可领时间与空闲轮询间隔休眠。数据库继续裁定全局租约数、同 Pixel 互斥及间隔。首轮可沿用单事件请求，先量化调度改动收益，再决定是否引入微批。 | 复用原 32 秒热点样例，在即时成功、持续积压时检查 3 秒间隔到期后能继续领取；再加一个慢响应 Pixel，其他目标应继续推进。双实例验证全局并发和单 Pixel 间隔，空队列验证无忙轮询。 |
| R05 | 把支付与非支付的服务顺序跨轮次保存，按实际成功领取的类别推进。可按 Pixel 记录两类均有任务时连续领取的支付次数，到达约定上限后优先领取最老的就绪非支付任务。跨实例通过同一调度状态作决定；只有一类积压时允许占满可用能力。 | 持续向一个 Pixel 补充 Purchase，同时预置注册和结账。设连续支付上限为 N，两类一直就绪时，最老非支付任务应在最多 N 次支付领取后获得机会。双实例、进程重启与仅支付流量分别验证。 |
| R06 | 在 Meta 客户端将“明确限流”与一般瞬态错误分别返回。在确认限流的完成事务中，用数据库原子操作将目标 `next_claim_at` 延长至当前值与有效退避截止时间的较晚者；同一事务同时保存本条任务的重试计划。无有效 Retry-After 时使用带抖动的有界退避。对账户级限流，仅在能识别实际共享额度范围时合并冷却。 | 注入一次有明确拒绝证据且带 1 小时 Retry-After 的响应。冷却期间两个 Worker 都不能领取同 Pixel 的另一笔任务，独立 Pixel 仍能发送；重启保留截止时间，较短的后续退避不能缩短它。另测普通瞬态错误和传输结果未知各走原有处理路径。 |

### 对账、告警与 CMS 操作

| 编号 | 建议改动 | 修复验收 |
| --- | --- | --- |
| R07 | 在订单查询中独立解析订单所属环境。优先读取已冻结的资格快照，缺失时核对结账环境及可验证的收款账户证据，统一转换支付域的 live 与回传域的 production。证据冲突或缺失时返回明确的待核实状态；具有确定归属的订单只在对应环境可查。逐单和汇总采用一致口径，按实际 API 角色补齐最小查询权限。 | 共库分别建立 sandbox、production 待支付单、无环境旧单及证据冲突单。用两个环境的受限 API 角色查询并核对响应标签与汇总分组；查错环境不能返回已知归属订单。 |
| R08 | Collector 每次成功采样时，在写入告警变化的同一事务内按环境更新最近采样时间，即使本轮零条告警也更新。API 以数据库时间计算数据年龄；首次未采样、过期、正常分别展示。建议以连续缺失三个采样周期作为初始过期阈值，并在 CMS 展示最后成功时间。 | 保持 Worker 在线，分别模拟首次启动、零告警成功采样、采样事务失败和 Collector 停止。失败事务不能刷新时间；超过阈值后刷新页面应显示采集过期，下次成功采样后恢复。 |
| R09 | Collector 从投递终态按定稿时间统计参数拒绝、本地校验失败和到期失败，从尝试记录按响应时间统计明确限流。按环境、Pixel、原因合并告警并附样例事件；失败率以同窗口已尝试的相应事件为分母，并设置最小样本量。仅向 Collector 授予新增统计使用的列权限。 | 模拟连续参数拒绝直至队列变空，仍应保留该窗口的失败告警；重复采样只更新同一条。连续 429 应能查到受影响 Pixel；业务规则跳过不触发故障告警。停止注入后按窗口验证恢复。 |
| R10 | 在回传中心父组件拆分基础列表、角标和当前标签的请求状态。角标请求失败时显示“告警数未知”，订单与事件标签仍发起各自请求。每个标签独立处理超时和重试；保留输入订单号、筛选条件及已加载结果的时间。 | 仅让 summary 返回 503，直接打开逐单深链并查询订单，再切换事件列表，两者仍可用。反向模拟订单接口失败，监控页应可读；恢复后只需重试出错的查询。 |
| R11 | 在司南配置保存事务中对最终配置做校验。允许停用账户保存空白草稿；启用账户必须有明确的应用和事件授权。对已启用账户的 PATCH 合并原值再校验，避免清空白名单后仍显示已启用。CMS 按来源展示留空语义，将具体字段错误显示在输入框旁。 | 覆盖创建草稿、空白启用、启用后清空、仅修改无关字段四条路径。模拟回调验证已授权应用与 Pixel 可进入 Inbox，未授权组合被拒绝。 |
| R12 | 增加按事件分页读取投递目标的只读接口，以原目标优先及投递 ID 作稳定游标，支持按 Pixel 定位。事件详情和逐单对账共用目标列表；从失败 Pixel 筛选结果打开详情时带入该 Pixel，重排仍传递准确的投递 ID 和预期尝试数。 | 建立含 51 个目标的事件，将第 51 个置为可恢复失败。确认能从筛选结果下钻至该目标、查看尝试记录，并仅重排这一条；跨页无重复、无遗漏，原目标的确认结果保持一致。 |

## 验证记录与上线验收

| 检查 | 结果与证据边界 |
| --- | --- |
| Conversion 全量回归 | Go 1.26.10、一次性 PostgreSQL 16，设置测试 DSN 后执行 `go test -race -p 2 -count=1 ./...`，全部通过。覆盖投递生命周期、受限角色、操作接口、逐单查询、告警、付款规则及司南模拟链路 |
| CMS 回传代理 | 执行 `go test -race -count=1 -run TestConversion ./internal/tracking` 通过，覆盖角色复核、身份转发、上游错误映射以及规则与审计代理 |
| 工作区最新迁移 | 在真实 1929 测试库应用 1930 迁移，再调用正式启动检查，复现 R01 |
| 冻结付款归因 | 真实付款入队 SQL、退役、归一化和审计重排路径复现 R03 |
| 单热点 Worker | 真实数据库、正式 Worker 和默认节奏，替换媒体传输以注入一次可重试拒绝和一小时退避；32 秒调用顺序为 p1、p2、p3，最终 p1 待重试、p2/p3 成功、注册尝试数为零。该实验验证本地调度行为 |
| 告警 | 使用正式 Collector 查询和汇总 SQL，失败投递出现在目标统计，活动告警为零。Collector 离线的显示结果按查询及界面分支核对 |
| 外部边界 | 未读取生产队列或 Meta 使用量，未发送真实 CAPI 事件；本次未做浏览器端到端操作。司南结果仅覆盖测试契约。R07 的部署前提需要结合实际数据库拓扑确认 |

上线容量应按**最忙 Pixel** 和**全环境**分别验收。建议用脱敏流量分布在隔离环境回放单热点、多 Pixel 扇出和限流恢复三组负载，分别统计每分钟就绪投递数、首次发送数、明确拒绝数、P95 入队等待、最老待发年龄以及到期数，并单列 Purchase、Subscribe 和非支付事件。先要求实测峰值下积压能在投递窗口内清空、限流期间其它独立目标继续推进，再决定采用多大并发及批量。

已有的事务 Outbox、稳定事件键、发送前落库及结果未知后的审计重排应保留。调整吞吐时，回归应同时检查同一目标的并发领取、明确确认后的本地提交失败、重复入队，以及策略跳过的任务不会发起外部请求。 [事务入队](../conversion-backend/internal/delivery/enqueue.go#L66) · [发送边界](../conversion-backend/internal/delivery/store.go#L137) · [回执恢复测试](../conversion-backend/internal/delivery/receipt_recovery_test.go)
