# Reader 第三批修复说明

> **最终复验：此前发现的遗漏已追加修复并验证关闭。** 见[最终补充修复记录](reader-backend-remaining-3-closure.md)；下文为原批次实现记录。

对应原审查报告 R13–R17；保留第一、第二批未提交修改。本次没有部署、修改生产数据或请求真实 PayPal/Meta 接口。

## 实现

| 问题 | 修复方式 |
| --- | --- |
| R13 缓存回填竞态 | 小说增加数据库发布版本。小说更新、章节增删改由触发器在内容事务内推进版本。Reader 先读取权威发布状态和版本，详情、目录、正文缓存及 singleflight 均按版本隔离。旧请求可以填充旧版本键，但新请求不再访问该键；不依赖 CMS 删除 Redis 键成功。 |
| R14 支付事件丢失 | `recharge_orders` 的已完成履约状态在同一事务内产生 `payment_event_outbox`。已履约收款产生 Purchase，订阅首次履约另有 Subscribe；订单事件、订阅首购事件分别有稳定唯一 ID。结算后不再把 Purchase/Subscribe 交给内存 worker。 |
| R15 停机顺序 | HTTP 停止并等待处理 → 停止及等待对账/outbox 生产者 → 排空内存 worker → 关闭日志 → 关闭连接池。总预算 25 秒，HTTP 阶段至多 8 秒。超时退出时持久化任务留待恢复。worker 拒绝停机后的提交，日志关闭具备幂等及并发保护。 |
| R16 环境/商户混用 | 采用原报告允许的保守方案：一个安装实例绑定一个 PayPal Client ID 和 sandbox/live 环境，首次渠道配置使用时持久化绑定，之后禁止直接切换。Reader 和 CMS 退款均校验绑定；恢复审批链接前也校验。新计划保存身份标记，只有当前绑定身份下的 ACTIVE 计划可复用；历史无标记计划不直接复用。 |
| R17 订阅归因丢失 | 创建意图保存 fbc/fbp、IP、UA、来源 URL、国家、推广/小说身份、UTM 和服务端选定像素；随后复制到用户订阅、付款订单和不可变 outbox payload。后台发货不再从当前浏览器或修改后的推广配置猜测原始上下文，付款方邮箱保留为匹配补充。 |

## Outbox 行为

- 事务回滚时，订单更新和事件一起回滚；退款在发货前已经确认时，不保留该笔未履约收款事件。
- 每轮认领最多 5 项，使用 `FOR UPDATE SKIP LOCKED`、2 分钟租约及随机 lease token；崩溃后租约到期可认领，旧 worker 不能确认新租约。
- 每次投递使用原始 event ID、业务时间和归因快照；失败保留错误并退避重试，最长退避 1 小时。
- 只有渠道返回有效的单事件接收确认且投递日志成功落库后，才标记 delivered。网络异常、确认丢失或数据库暂时故障会重试。
- 没有配置像素的订单保留为 `unattributed`，不占用自动重试队列，也不会自动改投当前其他像素。历史丢失的点击信息不会伪造或自动补齐。
- 这是至少一次投递模型，稳定 ID 用于渠道去重；没有声称任意故障时长下都能实现端到端 exactly-once。实际渠道的去重、事件时效和账号权限仍需集成验收。
- 已确认金额但付费周期尚待复核的订阅，继续沿用原有保守履约规则；完成履约后生成事件。不自动为上线前所有历史订单批量补发，避免重复归因。
- InitiateCheckout、CompleteRegistration 等非结算事件仍使用原有受限内存队列；本次持久化保证针对 Purchase/Subscribe。

CAPI 请求将访问令牌放在 Authorization Header；默认 Graph API 更新为 v25.0，可通过 `FB_GRAPH_API_VERSION` 配置。选用该版本的依据是 [Meta 官方版本列表](https://developers.facebook.com/docs/graph-api/changelog/versions/) 所列的支持期限；它不是对“最新版”的声明。

## 上线顺序及兼容性

1. 先应用完整共享 `cms-backend/db/schema.sql`，验证新增字段、版本触发器、身份保护函数和 outbox 触发器；若 Reader 使用独立数据库角色，确认其具备新增表的必要读写及函数执行权限。
2. 更新 CMS 和 Reader。首次切换需冻结支付配置变更，核对当前 PayPal Client ID、环境和存量交易所属账号，避免旧服务的配置缓存与新绑定不一致。未给历史数据猜测或回填商户身份。
3. 当前方案不支持在同一运行实例中直接切换商户/环境，也没有引入历史凭据仓库。需要切换时，必须停机进行独立审计迁移，或使用独立数据库/安装实例；不能删除绑定后让后台任务带着旧交易继续运行。
4. 相同 Client ID 的 secret 轮换允许进行。Reader 配置缓存仍有 5 分钟有效期，轮换应考虑旧密钥有效窗口或重启服务刷新缓存。
5. Redis 旧缓存和旧版本键通过原 TTL 自然回收。无需全库清空 Redis；下架检查和当前版本由 PostgreSQL 决定。
6. 原有可疑交易、历史整本证据和未证明付费周期的人工审核要求保留。

## 运行检查

检查投递积压和不可归因记录：

```sql
SELECT state, count(*), min(created_at) AS oldest
FROM payment_event_outbox
GROUP BY state;

SELECT event_id, order_id, attempts, next_attempt_at, lease_until, last_error
FROM payment_event_outbox
WHERE state = 'pending' AND delivered_at IS NULL
ORDER BY created_at
LIMIT 100;
```

修复渠道权限或网络后可以等待自动重试。需要提前重试指定事件时，只调整调度时间，保留原始 payload、event ID 和业务时间；不要重新生成事件来绕过失败。`unattributed` 记录需要独立核验原始归因，不能直接使用最新推广链接补发。

## 验证

- Reader 全量 `go test -race -count=1 ./...` 通过，包含原支付结算、退款乱序和订阅状态等集成场景。
- 最后新增的历史计划隔离、退款前置及租约 fencing 场景，以及修改后的发送/停机路径，另行重跑 race 测试通过。
- Reader `go vet ./...`、CMS `go test ./...`、`git diff --check` 通过。
- 第三批新增 10 个测试函数：旧回填与新版本隔离、提交/下架版本、事务回滚、失败投递重试、不可变事件身份及归因、订阅快照、历史计划身份、退款先于发货、崩溃租约恢复、商户/环境切换拒绝及安全密钥轮换、worker 超时及拒绝新任务、日志并发关闭等场景在这些函数内组合覆盖。
- 测试数据库为无业务挂载的临时 PostgreSQL 16。Meta/PayPal HTTP 均由内存 transport 模拟；没有做真实渠道、真实 OSS、生产多节点或进程级 SIGTERM 的端到端验收。

原审查 MD 保留为固定提交的历史审查结果；本文记录对应问题的实现与验证，不改写原始结论。
