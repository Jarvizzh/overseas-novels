# Reader Review 第 6 节优化与验收

日期：2026-11-17。范围：`reader-backend-deep-code-review-34ff7cf-2026-11-17.md` 第 6 节的七条建议，不新增或重新计算原 17 项缺陷。以下为本地工作区实现与隔离验证，尚未提交、部署或验证生产状态。

## 1. 完成情况

| 建议 | 实现 | 验证 |
| --- | --- | --- |
| O01 超时 | HTTP、请求上下文、连接池等待、SQL、锁等待及 OSS 均有预算；异步营销查询补充截止时间 | 请求预算测试、连接池耗尽/慢 SQL 测试、OSS 本地 HTTP 取消测试 |
| O02 规模 | 请求体、分页和书架批量限制；H5 对大批量自动拆分 | 未知长度请求体边界、整数溢出、批量预校验、201 条 H5 请求分拆测试 |
| O03 查询错误 | Reader 全部手工 `rows.Next()` 循环检查 `rows.Err()`；解锁索引 Scan 失败直接返回错误 | 逐处代码核对；异常索引行不再静默丢失；全量 PostgreSQL 回归 |
| O04 schema | 版本登记、CMS 原子迁移和失败退出、两服务 ready 版本门槛 | schema/seed 故障回滚、重复迁移时间不变、缺失/过旧/超前版本拒绝 |
| O05 持久任务 | 错误日志、失败结果持久化、重试/游标覆盖保护、积压汇总和恢复 CLI | Inbox/订单/订阅结果覆盖保护、取消错误持久化、租约和恢复约束、状态汇总 |
| O06 会话与错误 | HS256/exp/正数 ID/issuer/audience；固定旧令牌兼容窗口；领域错误和内部错误分离 | JWT 正反例、账户查询故障与账号不存在区分、响应不泄露 SQL 错误 |
| O07 商业规则 | 产品和客服说明、回归用例 | 部分退款保留权益、累计全退与重放、金币 shortfall、取消后的已付周期、未知周期人工复核 |

## 2. 请求和数据库预算

Reader HTTP Server：header 5 秒、整个请求读取 10 秒、写入 70 秒、idle 60 秒，header 最大 32 KiB。中间件提供共用截止时间：普通接口 10 秒，付款/订阅创建确认及取消 60 秒，Webhook 30 秒，`/healthz` 2 秒。上游已取消或截止更早时沿用较早截止时间。

Reader pgx：连接建立 5 秒、连接池等待 3 秒、每次查询 10 秒；服务器端 `statement_timeout=10000`、`lock_timeout=3000`、`idle_in_transaction_session_timeout=15000`。pgx 官方 tracing hook 返回的上下文用于对应 acquire/query 全过程，结束时清理定时器；请求上下文继续约束整个业务流程。

OSS 的上传、下载、删除、列表和批量删除都传递 `oss.WithContext(ctx)`，客户端另有连接 5 秒、读写 10 秒预算。停机继续沿用既有 25 秒总预算；后台循环最长 8 分钟，子操作保留较短预算，claim 最长 5 秒，取消总上下文时停止。

这些是初始保护值，尚无生产 P99/压力测试结论；后续根据慢查询、连接池占用和渠道延迟调整，保持付款重试的幂等约束。

## 3. 请求规模和客户端兼容

| 对象 | 限制 | 超限响应 |
| --- | --- | --- |
| 普通 Reader 请求体 | 64 KiB，包含未知 Content-Length/分块请求 | 413 `body_too_large` |
| 支付 Webhook 请求体 | 1 MiB，保留原始字节用于验证 | 413 `body_too_large` |
| 小说列表、搜索、钱包流水 | limit 1–100；offset ≤ 100000；先校验后乘法 | 400 `invalid_pagination` |
| 移除书架、同步进度 | 每批 1–100；小说 ID 正数；进度原有值域/时间校验保留 | 400 |
| 注册/绑定/登录 | 邮箱最长 255；密码至少 6 字符且最多 72 字节（与 bcrypt 一致）；昵称最长 100 | 400 `invalid_request` |

分页不合法参数不再静默回退或截断。H5 API 将超过 100 条的操作分批，常规请求依次执行，失败立即抛出；页面退出的 keepalive 请求立即排入发送，避免等待上一批响应后才创建下一批。空数组在客户端直接完成。移除和进度同步保留幂等/时间戳约束，部分批次成功后可安全重试。

退出发送仍受浏览器整体 keepalive 配额、进程退出和网络条件影响；本次不宣称页面关闭后必达，也没有浏览器实机验收。分批测试使用本地 fetch stub，不发真实网络请求。

## 4. schema 发布门槛

当前统一 schema 发布版本为 **2026111701**，登记在 `reader_schema_migrations(version, applied_at)`。这是现有统一 schema 的首个版本基线；保留全量幂等 DDL，后续版本需追加版本登记并同步两服务的版本要求。

CMS 使用数据库事务和事务级 advisory lock 串行执行 schema、seed/默认值、序列修复；任何一步失败均回滚表数据/DDL 并使启动失败；PostgreSQL 序列推进可能留下编号空洞，不代表业务数据提交。版本时间只在首次插入时生成。较旧 CMS 发现数据库登记了更高版本时拒绝执行迁移。Reader 启动检查版本；两服务 `/healthz` 每次检查精确版本及依赖，版本不匹配返回 503；`/health` 保留进程存活语义。

发布顺序：先运行新版 CMS 完成迁移，再启动新版 Reader，检查 `/healthz`。手动执行完整 schema 时须由发布系统包裹事务，并配置错误即停止。不可手工降低版本号来绕过检查；回滚需使用与当前 schema 兼容的二进制。没有执行生产迁移。

## 5. 会话及错误契约

新 JWT 使用 `HS256`、`iss=star-novel-reader`、`aud=star-novel-reader-api`、正数 user_id、72 小时 exp。验证拒绝其他算法、缺失 exp、错误 issuer/audience 和未来 iat。用户状态与 session hash 一并经 Repository 校验。

升级前没有 issuer/audience 的令牌只在固定窗口兼容：必须同时缺失这两个字段、具备 iat 和 exp、iat 不晚于首次 schema 发布时间，exp 不晚于该时间 +72 小时，且当前仍在窗口内。重启和重跑迁移不会延长窗口。原令牌自身 exp 始终有效；可用原会话 cookie 刷新为新令牌。

响应继续保留 `{ "error": "可展示消息" }`，新增保护/领域错误另含稳定 `code`。参数失败为 400、资源不可用为 404、价格/订阅冲突为 409、体积超限为 413；认证失败为 401、禁用账号为 403；依赖故障使用通用 5xx 消息。内部 SQL/渠道/绑定诊断不直接返回客户端，服务器日志关联 RequestID 和 route。已知领域错误使用固定消息，不返回可能包裹诊断的 `err.Error()`。

账户查询的数据库故障不再误报成用户不存在或密码错误；可选登录接口在有效 token 的账户查询失败时返回 503，避免将已登录读者静默降为匿名。

## 6. 持久任务监控与恢复

每分钟输出一次 `payment_tasks=<JSON>`：各类别 `count`、`oldest_at`、`max_attempts`，无渠道凭证或客户 payload。类别包括 Inbox、Outbox、无归因事件、取消待处理、订阅到期检查、已付款未发货、待确认订单、人工复核、退款 shortfall、结果未明的订阅创建意图。Inbox 旧行缺失历史入队时间时，首次迁移以迁移时间填补；旧取消请求以原 updated_at 估算，之后用独立 cancel_requested_at 保留年龄。

订单、订阅检查、Inbox 和取消的 claim、迭代、执行和结果写入失败均记录日志；订单及订阅另保存尝试次数和最近错误。订单/Inbox 使用尝试次数，订阅使用 claim 时间，Outbox 使用 lease token，防止旧 worker 覆盖新一轮结果。业务应用继续依赖既有 capture/event 去重和事务约束。

在 Reader 后端目录，通过环境配置连接到目标数据库：

```sh
# 只查看摘要
go run ./cmd/payment-ops

# 默认只打印拟恢复任务
go run ./cmd/payment-ops -kind outbox -id 'payment:123:Purchase'

# 核对工单和任务后，调整指定任务调度
go run ./cmd/payment-ops -kind outbox -id 'payment:123:Purchase' -apply -reason '工单 T123：网络恢复后重试'

# 从经过确认的 UTC 时间回退订阅对账游标
go run ./cmd/payment-ops -kind subscription -id 'I-EXAMPLE' -from '2026-11-01T00:00:00Z' -apply -reason '工单 T124：补查该时间起的扣款'
```

支持 `inbox`（event ID）、`outbox`（event ID）、`order`（本地正数订单 ID）、`cancel`（协议 ID）、`subscription`（协议 ID）。实际修改必须提供 `-apply` 和 `-reason`，日志记录操作人、任务和原因；应保留到工单审计记录。无有效数据库配置不会执行。

恢复仅调整调度/回退游标，不改金额、收据、权益或事件 payload，不创建退款，不立即调用任何外部渠道。完成任务、无归因 Outbox 和有效租约中的任务拒绝恢复。部分队列以 next_attempt/check 时间兼作租约，须等待该时间到期后再操作。订阅游标只能回退，显式恢复标记使超过常规 30 天窗口的已关闭协议仍可补查；追平至最近一天后清除标记。订单仍遵守已有 30 天自动捕获边界。

建议告警：积压年龄超过各自租约与退避上限、最大尝试次数持续增加、已付未发货/人工复核非零、取消超过工单 SLA、shortfall/无归因长期未处理。日志与 CLI 已提供数据；没有配置或宣称存在生产告警平台、值班 SLA。

## 7. 验证记录

- Reader：隔离 PostgreSQL 16，`go test -race -count=1 ./...` 通过；`go vet ./...` 通过。
- CMS：`go test -race -count=1 ./...` 通过，包含真实 PostgreSQL 迁移回滚；`go vet ./...` 通过。
- H5：`npm run build`、`npm run lint`、`npm run test:shelf-batches`（3 项）通过。
- `payment-ops` 已在临时数据库完成只读启动与摘要输出 smoke 验证。
- 新增测试覆盖请求预算/大小、分页溢出、非法批量、OSS 取消、schema 门槛/回滚、JWT、错误隔离、任务恢复及商业规则。手工迭代查询逐项核对。
- 首次跨模块并行回归共用数据库时，CMS 默认模板测试数据干扰 Reader 固定模板预期；分离测试数据库后全量通过。测试数据库由本次临时容器提供，不连接生产。
- 未执行真实 PayPal/Meta/OSS 请求、生产迁移、生产负载或浏览器退出实机验收。商业规则与客服路径见 [产品与客服说明](reader-payments-business-rules-and-support.md)。
