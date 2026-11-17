# Reader SQL、索引与查询性能专项 Review

日期：2026-11-17。对应 [全流程审查报告](reader-full-flow-review-2026-11-17.md)。原始审查和数据保留在第 1–5 节。代码改动、验证结果及发布脚本见[第 6 节](#6-实施与验收)。

S08 历史无归因汇总和 S09 书架分页通过。S07 的旧 Pending 订单扫描已修复，存量 EXPIRED 迁移边界暂不处理。S05 租约队头的性能限制仍存在。[全流程与 SQL 修复复核](reader-full-flow-and-sql-fixes-review-2026-11-17.md)

## 1. 结论及实验范围

目前 SQL 多数有参数绑定，钱包锁和唯一约束使用合理；主要问题是 **权限子查询缺少索引、部分索引谓词不匹配、筛选索引不支持排序、队列的 COALESCE/OR 妨碍现有索引、监控扫描成本随全量历史增长**。不能因为 schema 中存在一个相近名称的索引，就断言当前查询会有效使用它。

在独立 PostgreSQL 16 中应用当前统一 schema，生成：10,000 本小说（8,000 本符合可读条件）、2,000 用户、200,000 笔流水（示例用户 10,000 笔）、100,000 个订阅订单、100,000 条 Outbox（99,995 条无归因，5 条 pending）、100,000 个会话。另构造一个有有效期但没有订单周期证据的历史协议，验证现有兼容分支成本。所有记录均为合成数据。

原有索引 → EXPLAIN ANALYZE BUFFERS → 在同一临时库创建候选索引并 ANALYZE → 重跑同组查询。多数数据命中缓存；下面是单次样本，不是生产基准，不承诺相同延迟或倍数提升。`buffers` 是顶层 shared hit+read 访问计数，不是唯一物理页数。

## 2. 执行计划证据

| 查询 | 当前计划及访问量 | 候选后计划及访问量 | 单次耗时 ms（前 → 后） |
| --- | --- | --- | --- |
| 钱包流水首页，完整投影，20 条 | user 索引 Bitmap + 10,000 行 Heap + Sort；5,421 buffers | 复合索引 Index Scan；17 buffers | 11.756 → 0.099 |
| 历史协议 VIP 周期证据 | recharge_orders Seq Scan，过滤 100,000 行；2,223 buffers | 按 subscription_id 的周期索引；3 buffers | 26.957 → 0.122 |
| 整本权限（无命中） | user 索引后过滤 100 行；102 buffers，现有整本部分索引不可用于完整谓词 | 匹配状态集合的部分索引；2 buffers | 0.485 → 0.017 |
| 热门小说首页，轻量投影 | 8,000 行 Seq Scan + Sort；176 buffers | 可读小说排序部分索引；22 buffers | 5.973 → 0.082 |
| Outbox 领取候选 | 扫描并过滤 99,995 条无归因行 + Sort；1,436 buffers | pending 专用有序部分索引；12 buffers | 18.599 → 0.083 |
| 按 user_id 找会话 | Seq Scan；1,429 buffers | user_id 索引 Bitmap；55 buffers | 9.711 → 0.147 |
| 流水 offset=8000，完整 API 投影 | Bitmap + Sort；5,418 buffers | **仍选 Bitmap + Sort**；5,415 buffers | 11.811 → 10.010 |

重要限制：热门小说实验只投影 id/title/author/views，用来验证过滤和排序；正文、封面签名、网络和 JSON 成本未包含。会话实验为撤销逻辑同过滤条件的 SELECT，不包含 DELETE/WAL 成本。Outbox 实验保留 FOR UPDATE SKIP LOCKED，但未执行完整 UPDATE/发送。深分页另用 API 完整九字段投影复核；最初仅 id/created_at 的覆盖查询不能代表真实 API，所以没有用它的较好结果宣传优化。

最后一行说明：**新增排序索引不能替代深分页改写**。完整行需要大量回表时，优化器仍可能认为 Bitmap + Sort 更划算；不能用 SET enable_seqscan=off 强行做出“命中索引”的结果。

## 3. 具体 SQL 审查

### S01 · 优先级高 · VIP 权限订单子查询缺少协议/周期索引

**位置**：`reader-backend/internal/novel/repository.go:27-32,253-267`；同一谓词也用于钱包解锁事务、协议列表。

PaidSubscriptionPredicate 对 recharge_orders 做 NOT EXISTS/EXISTS，条件核心是 subscription_id 和 paid_period_end。当前订单索引以 user_id/status/整本/单次外部 ID 为主，没有 subscription_id 索引。缺少周期证据的历史协议需要证明“不存在”，尤其容易全表扫描；已存在交易的分支也随匹配行位置放大。它位于每次付费阅读的鉴权链路，优先级高于把 VIP 判定缓存数分钟。

**建议**：先添加 `(subscription_id, paid_period_end)`、`WHERE paid_period_end IS NOT NULL`，同时支持两个子查询；依据真实退款/周期分布决定是否需要 INCLUDE 或有效收据专用索引，不要初始堆叠多个近似索引。保留当前退款间隙与未证明周期的判断，不能为了索引把 NOT EXISTS 兼容语义直接删除。

**验收**：无交易历史协议、很多旧周期、当前有效/未来/已退款周期，多协议用户；EXPLAIN 验证不再按每次阅读全扫订单。需要同时关注准备语句的 generic/custom plan。

### S02 · 优先级高 · 整本权益部分索引遗漏 Paid 状态

**位置**：`cms-backend/db/schema.sql:514-516`；`novel/repository.go:35-41`。

现有索引 WHERE status IN ('Success','RefundPending')，查询允许 ('Success','Paid','RefundPending')。查询谓词不蕴含更窄的索引谓词，不能只依赖该部分索引得到全部结果。实验确实使用宽泛 user 索引后过滤。

**建议**：用与查询一致的状态集合替换索引定义，保留 `(user_id, novel_id)` 及 order_type/fulfilled_at 条件；确认应用是否仍支持 Paid，统一领域状态后再考虑收窄。不要只是 CREATE INDEX IF NOT EXISTS 同名索引，它不会更新旧谓词。可以先并发创建不同名字，验证有效后切换/移除旧索引。

### S03 · 优先级高 · 流水排序与分页

**位置**：`wallet/repository.go:81-105`；schema 当前仅 `idx_transactions_user(user_id)`。

WHERE user_id 后 ORDER BY created_at DESC，原索引只能缩小用户集合，仍需获取并排序其全部流水。候选 `(user_id, created_at DESC, id DESC)` 对首页效果明确；同步在 SQL 中加 id 作为稳定排序键。

**深分页方案**：使用 `(created_at,id)` 游标而非 OFFSET，例如：

```sql
SELECT id,user_id,type,biz_type,amount,charged_amount,bonus_amount,description,created_at
FROM transactions
WHERE user_id=$1
  AND (created_at,id) < ($2::timestamptz,$3::varchar)
ORDER BY created_at DESC,id DESC
LIMIT $4;
```

首屏不带游标条件；服务端校验 cursor 和最大 limit。新接口/客户端一起演进，保留必要的旧分页兼容。不要把 description 等大字段无差别 INCLUDE 进索引；20 条回表通常比扩大每笔交易的索引写入更合理。

### S04 · 中优先级 · 小说列表索引的首列和排序不匹配

**位置**：`novel/repository.go:86-105,125-132`；schema:448-453。

现有 `(status,publish_status,view_count DESC,created_at DESC)` 与 `status <> 'archived'` 的不等条件、`publish_status='published'` 组合，不能有效提供跨多个 status 的全局有序前 20 条。实验出现 Seq Scan + top-N Sort。

**建议**：可读小说的排序部分索引，按热门/最新真实访问占比分别评估；ORDER BY 补 id 稳定排序。genres GIN 以及 title/author/synopsis trigram 已存在，能服务分类/模糊搜索；但短字符串、前置 `%` 和多个 OR 仍可能使优化器选扫描。对 1/2/3 字符、常见词、稀有词和中文分布分别 EXPLAIN，不能断言有 trigram 就永远快速。限定搜索长度与范围，必要时区分前缀/全文检索。

### S05 · 优先级高 · Outbox 部分索引包含永久无归因任务

**位置**：`wallet/outbox_repository.go:17-21`；schema:805-810。

当前索引 WHERE delivered_at IS NULL，把不再自动发送的 unattributed 也全部纳入。查询还要求 state='pending'，排序是 next_attempt_at,event_id，而索引只有 next_attempt_at。无归因行大量累积会过滤大量历史任务，实验过滤 99,995 行才得到 5 条任务。

**建议**：专用 `(next_attempt_at,event_id)`、`WHERE state='pending' AND delivered_at IS NULL`。lease_until 仍需检查，且要验证大量有效租约占据队头时的成本；不能为性能删除租约或 SKIP LOCKED。无归因任务和已完成任务另做保留/归档策略，不伪造像素补发。

### S06 · 中优先级 · 会话撤销缺少 user_id 索引

**位置**：`auth/repository.go:147-154`；schema:757-759。

绑定时 DELETE reader_sessions WHERE user_id=...，当前仅 token_hash 主键和 expires_at 索引。按 hash 验证/刷新会话的索引使用良好，不能推导出“按用户撤销”也有索引。

**建议**：增加 reader_sessions(user_id)。过期清理已有 expires_at 索引，但登录时清理应避免多个请求争抢同一批过期行，可用 SKIP LOCKED 或独立清理任务；别把全表清理放进认证热路径。

### S07 · 中优先级 · 对账队列 COALESCE/OR 与现有索引不一致

**位置**：`wallet/reconciliation_repository.go:23-36,48-56,73-84`；schema:645-647,691-694。

订单按 COALESCE(reconciliation_at,created_at) 过滤排序，**已有索引确实采用相同表达式**；问题在于该部分索引只覆盖 Pending 且 checkout_id IS NOT NULL，实际查询还包含 checkout_id IS NULL 的历史路径及已成功未履约整本订单，不能依赖它覆盖完整 OR 条件，且缺少 id 排序尾列。取消和订阅则按 COALESCE(next_attempt/check_at,updated/created_at) 排序，现有索引只按原列建，不能直接支撑相同表达式；订阅条件又混合 active、最近关闭及显式恢复。

**建议顺序**：

1. 把 next_attempt_at/next_check_at 建模为非 NULL、创建时初始化，回填历史值后直接按该列排序，减少表达式和 fallback。
2. 如暂不改变模型，评估与 SQL 完全一致的表达式索引。partial WHERE 不得使用 now() 等非 immutable 表达式。
3. 将新订单自动对账和历史证据复核分开查询/队列，分别限制年龄与资格；active/recent-closed/recovery 可分支领取，避免一个大 OR 拖垮索引。
4. 大规模时把调度状态放到独立持久任务表，避免不断更新/扫描宽订单表；交易数据仍是唯一账务事实来源。

本项为谓词/索引静态核对，**未对上述三类队列构造完整分布做计划收益测量**。不能把 S05 的 Outbox 数字套用到它们。

### S08 · 中优先级 · 汇总监控重复扫描宽历史表

**位置**：`wallet/operations_repository.go:24-35`、`reconcile.go` 的每分钟汇总。

多个 UNION ALL 分支分别扫描 recharge_orders 得到待确认、已付未发货、人工复核计数；实例扩容后每实例都跑一次。COUNT/MIN/MAX 精确聚合的成本随候选集增长，超时保护只会终止查询，不会让它变便宜。

**建议**：小积压可用与候选谓词匹配的部分索引；合并可共享的扫描，或用任务状态表/增量计数与定时校对。监控使用单实例聚合/单独资源预算，避免争抢阅读连接池。最老年龄仍要从真实待处理时间计算，不能为了快只记录最近尝试时间。此项没有单独压测数字。

### S09 · 中低优先级 · 书架和进度查询

**位置**：`shelf/repository.go:25-36,119-128`；`shelf/service.go:77-101`。

按 user_id 过滤已有索引，进度精确查/UPSERT 的 `(user_id,novel_id)` 唯一约束也能用，不能笼统归为“书架无索引”。但收藏列表还需 in_shelf=true、updated_at DESC，长期大量历史进度会增加过滤/排序成本。

**建议**：在确认用户收藏数量和执行计划后，评估 `(user_id,updated_at DESC,novel_id) WHERE in_shelf=true`，并分页。进度同步删除冗余逐条 SELECT，改为预校验/去重后的批量 UPSERT，保持数据库比较时间。配合主报告 N08 新增独立进度恢复接口。

### S10 · 其他已核对的 SQL 使用

| 链路 | 当前评价/建议 |
| --- | --- |
| 小说/章节点查 | novels PK、chapters 唯一 `(novel_id,chapter_index)` 可用；显式 idx_chapters_novel_index 与唯一索引有重复列，需看实际依赖后再清理，别继续重复加索引。 |
| 解锁存在性 | 唯一 `(user_id,novel_id,chapter_index)` 支持点查及用户书籍前缀；全量取索引的放大主要由调用方式造成，见主报告 P04。 |
| 钱包余额与扣费 | user_id 主键/唯一定位 + FOR UPDATE 合理，扣费/流水/权益同事务；不能拆事务换吞吐。 |
| 推广解析 | 已绑定推广按用户/链接主键查询；UTM 唯一回退的 count/min 过滤没有 `(utm_source,utm_campaign)` 专用索引，大量链接时需评估；不能用“取最新”替代唯一性判定。 |
| 支付收据/退款 | capture 唯一部分索引、订单/外部 capture 查询索引和退款 capture/currency 索引存在；保留幂等及锁顺序，按真实 SQL 检查状态是否满足部分谓词。 |
| 日签到 | user+日唯一索引保护幂等，按用户时间窗口排序的历史查询仍随签到天数增长；可维护最近签到状态并保留流水对账。 |
| SQL 参数 | 本次重点链路使用 pgx 参数，动态排序有受控分支；未发现这些路径的直接 SQL 注入。不是全项目安全审计结论。 |
| 结果完整性 | Reader rows.Err 已修；CMS 同类遗留仍在，见主报告 N09。返回部分结果不能当成查询优化。 |

## 4. 候选索引 DDL（需发布设计后执行）

以下名字是示例，不是已落地 migration。实验用非并发 CREATE INDEX 建在空闲临时库；生产如需在线创建，可用下列事务外 CONCURRENTLY 形式。**现有 CMS autoMigrate 把全 schema 包在事务里，不能直接把这些语句贴进去。**

```sql
CREATE INDEX CONCURRENTLY idx_orders_subscription_period
ON recharge_orders(subscription_id,paid_period_end)
WHERE paid_period_end IS NOT NULL;

-- 使用新名字替换旧的、不含 Paid 的部分索引。
CREATE INDEX CONCURRENTLY idx_whole_book_entitlement_v2
ON recharge_orders(user_id,novel_id)
WHERE order_type='whole_book'
  AND status IN ('Success','Paid','RefundPending')
  AND fulfilled_at IS NOT NULL;

CREATE INDEX CONCURRENTLY idx_transactions_user_feed
ON transactions(user_id,created_at DESC,id DESC);

CREATE INDEX CONCURRENTLY idx_novels_reader_popular
ON novels(view_count DESC,created_at DESC,id DESC)
WHERE status<>'archived' AND publish_status='published';

CREATE INDEX CONCURRENTLY idx_payment_outbox_pending
ON payment_event_outbox(next_attempt_at,event_id)
WHERE state='pending' AND delivered_at IS NULL;

CREATE INDEX CONCURRENTLY idx_reader_sessions_user
ON reader_sessions(user_id);
```

不要同批盲建所有候选索引。先用真实基数/分布、参数化 SQL 和计划验证；评估索引体积、WAL、更新成本、autovacuum、连接/锁预算。检查 concurrent build 失败留下的 invalid index；完成后 ANALYZE、核对 pg_index.indisvalid 和实际索引使用，再决定移除旧索引。新旧代码/索引共存的发布顺序要写入正式迁移方案。

## 5. 可复核方法与后续验收

取主报告记录的当前工作区 schema，装载合成数据并 ANALYZE，执行与 Repository 相同谓词的 `EXPLAIN (ANALYZE,BUFFERS,FORMAT JSON)`；对写语句只能在隔离库或受控可回滚实验中用 ANALYZE，因为它会实际执行。不要在生产直接运行本报告的造数、DROP 或测试命令。

本轮原始计划、造数及候选索引脚本保存在主报告列明的临时副本：`sql-setup.sql`、`sql-plans.sql`、`sql-indexes.sql`、`sql-before.jsonlog`、`sql-after.jsonlog`、`history-deep-corrected.sql`、`history-deep.jsonlog`、`session-plan.sql`、`session-plan.jsonlog`。这些临时材料不能替代正式版本化迁移或长期性能基线。

上线前至少覆盖：热/冷缓存、真实重度用户、无结果/少量/大量结果、NULL 与历史状态、付费周期/退款间隙、99% 已完成或无归因任务、租约占据队头、generic/custom plan、并发写入与 autovacuum。观察总 SQL 次数、执行/等待时间、buffers、heap fetch、排序溢出和锁等待，再比较用户请求 P95/P99。本文没有生产执行计划、实际 QPS 或全站容量承诺。

## 6. 实施与验收

| 项目 | 实施结果 |
| --- | --- |
| S01 / S02 | 增加协议周期索引及覆盖 `Paid` 的整本权益索引。权益、退款和历史证明谓词保持原样。 |
| S03 | 新增 `GET /api/v1/wallet/history/cursor`，按日期和 ID 稳定倒序，限量多取一条判断下一页。历史空日期归到最早，保留旧数组接口。H5 使用服务端游标并取消过期分页请求。 |
| S04 | 热门及最新列表使用可读小说部分索引。热门、搜索和最新排序均有 ID 作为最终排序键。搜索关键词限制 1–128 字符，保留现有模糊搜索语义。 |
| S05 / S06 | Outbox 只索引待发送行，保留有效租约检查、去重和 SKIP LOCKED。会话支持按用户撤销索引，过期清理按到期顺序跳过已锁记录。 |
| S07 | 订单、取消、订阅使用匹配现有 COALESCE 及排序的表达式索引。保留历史任务、有效期和重试/恢复条件。 |
| S08 | 订单三项指标合并为一次候选集扫描和条件聚合。同一订单仍可计入多个类别。多实例可将 `PAYMENT_TASK_METRICS_ENABLED=false` 配在非采集实例，默认继续采集。队列执行和手工诊断不受此开关影响。 |
| S09 | 增加收藏排序部分索引及稳定排序。进度按小说 ID 保留批内最新值，用一条 SQL 原子 UPSERT，保留已有收藏状态及数据库时间戳比较。 |
| S10 | 章节缓存未命中时按章节精确查询购买记录，保留成功购买的正缓存。UTM 唯一匹配增加复合索引。签到沿现有 UTC 日期唯一索引向前查到断点。CMS 小说相关迭代错误向上返回，导入遇到配置读取失败或非法值即中止。 |

### 6.1 隔离库结果

在 PostgreSQL 16 对同一组 SQL 比较加索引前后的计划。使用第 1 节合成规模，并加入 20,000 条订阅以及少量待确认订单和未履约订单。以下为 `EXPLAIN (ANALYZE, BUFFERS)` 单次采样的顶层 shared buffers 访问数。

| 查询 | 访问数前后 | 新计划使用的索引 |
| --- | ---: | --- |
| 流水首页 | 5,424 → 18 | `idx_transactions_user_feed` |
| 流水约第 8,000 行处继续读取 | 5,419 → 19 | `idx_transactions_user_feed` |
| 历史 VIP 周期证据 | 2,225 → 5 | `idx_orders_subscription_period` |
| 整本权益 | 102 → 2 | `idx_whole_book_entitlement_v2` |
| 热门 / 最新目录 | 179 → 22 / 173 → 3 | 两个读者目录部分索引 |
| Outbox 领取 | 1,433 → 7 | `idx_payment_outbox_pending` |
| 订单对账领取 | 2,225 → 5 | `idx_paypal_order_claim` |
| 订阅对账领取 | 333 → 8 | `idx_paypal_subscription_claim` |
| 按用户查会话 | 1,429 → 52 | `idx_reader_sessions_user` |
| 任务指标汇总 | 5,409 → 1,438 | 订单关注项、待发送 Outbox、到期订阅索引 |

深页样本在两边都使用相同游标 SQL，包含测试用的一次边界行查询。早先 OFFSET=8000 的基线见第 2 节。取消队列仅 10 条候选时仍使用既有状态索引。改为 20,000 条待取消、20 条到期的分布后，使用 `idx_paypal_cancel_claim`，访问 9 块。签到样本含近万条历史记录和 2 天当前连续签到，使用现有唯一日期索引，访问 11 块。书架样本通过部分索引把 10,000 条进度中的 100 条收藏选出，最终仍有 JOIN 和排序。合成结果用于验证计划形态，线上查询延迟和写入成本需通过上线观测采集。

Reader、CMS 的全量 `go test -race -count=1 ./...` 及 `go vet ./...` 通过。新增回归覆盖同时间流水、插入新流水后的分页、空日期历史行、用户隔离、过期签到间断、进度批内去重和整批回滚。H5 构建、源码 lint、既有书架分批测试通过。索引脚本在隔离库执行两次，第二次全部跳过已建索引。

### 6.2 发布顺序

索引脚本依赖现有统一 schema 已安装。由具备建索引权限的连接在目标 schema 中单独执行，每个数据库一次。提供 `PGSERVICE` 或 PostgreSQL 环境配置后运行下列命令。

```sh
psql -X -v ON_ERROR_STOP=1 -f cms-backend/db/reader_query_indexes.sql
```

索引采用 `CREATE INDEX CONCURRENTLY`，执行器须保持自动提交，不能用 `--single-transaction`。并发建索引失败可能留下无效索引。脚本再次运行会对目标索引的无效状态报错。确认失败索引尚未被采用后，由运维针对该索引执行 `DROP INDEX CONCURRENTLY` 并重跑脚本。可用 `pg_index.indisvalid` 和 `pg_get_indexdef` 核对有效性和定义，再对相关表执行 `ANALYZE`。

部署时先执行索引脚本，再启动含新接口的 Reader，最后发布 H5。新旧服务可使用同一组索引。上线观察写入成本和使用情况后，再决定是否移除已被覆盖的旧索引。当前改动尚在本地。

书架结果分页留待阅读历史接口拆分后再切换 H5，避免只加载首屏就丢失完整收藏状态。队列状态独立建表、精确监控增量化和历史归档需基于生产分布再决策。本次通过匹配索引和合并扫描降低现有实现开销，保留任务调度和会计事实来源。

## 7. 全量联合复核补充（修复前）

基于 `fcc42ad` 加当前工作区的独立核验已完成，详见 [联合复核报告](reader-full-flow-and-sql-fixes-review-2026-11-17.md)。正式并发索引脚本、重复运行、invalid-index 保护和恢复后 14 个目标索引有效性均通过隔离验证。

但订阅领取对 100,000 条无资格旧协议仍逐行过滤，取 5 条任务访问 2,509 buffers；同一终态的状态同步还会刷新 `updated_at`，使“最近 30 天”资格不断延长。S07 因而保持部分完成，需按 C01/C02 修复。S08 的无归因历史精确统计、S09 的书架分页及实际排序表达式仍保留优化空间。上述结论不否定订单/取消队列、权限、流水游标等已经验证的改进。

## 8. S07–S09 追加优化

订阅领取按稳定状态时间限定观察期，并用两个索引分开检索持续跟踪与近期协议。历史无归因计数改为事务内增量维护，定时采集按数据库限为每分钟一次。书架使用与活动排序表达式匹配的游标索引，分页结果及收藏 ID 分开获取。

最新执行计划、回归记录和发布顺序见 [联合复核第 8 节](reader-full-flow-and-sql-fixes-review-2026-11-17.md#8-s07s09-修复与验收)。
