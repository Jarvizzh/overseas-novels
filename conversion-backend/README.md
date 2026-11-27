# Star Novel 转化服务

转化服务消费 Reader 事务 Outbox，创建统一事件与各 Pixel 投递任务。迁移由 CMS 的 `-migrate` 入口管理，启动时要求 schema `2026111937`。应用架构、实施顺序与验收标准见 [统一转化回传架构升级与开发文档](../docs/conversion-reporting-architecture.md)。

从本目录运行 `go run ./cmd/conversion`。配置如下。

| 环境变量 | 说明 |
| --- | --- |
| `DATABASE_URL` | 共享 PostgreSQL 连接。 |
| `CONVERSION_ENVIRONMENT` | Worker 消费的环境，需要与 Reader、接入账户和目标一致。Reader 未配置时使用 `production`。 |
| `CONVERSION_META_ENABLED` | 为 `true` 时领取并发送 Meta 任务。默认关闭发送，仍归一化 Outbox、更新心跳并扫描到期和失效租约。 |
| `META_GRAPH_VERSION` | 启用时指定 Meta Graph API 版本。 |
| `CONVERSION_CREDENTIAL_KEYS` | 启用时提供 JSON 对象，键为密钥版本，值为 Base64 编码的 32 字节 AES 密钥。凭据使用 `cipher.NewGCMWithRandomNonce` 封装，密钥版本作为关联数据。 |

归一化使用同环境下 `source=starnovel`、`account_key=reader` 的已启用接入账户，并通过该账户的授权绑定匹配 Pixel。账户未启用时，Reader 持续入队。测试和生产使用不同数据库及 Pixel。

## CMS 逐单对账

使用 `go run ./cmd/conversion --mode=api` 启动私网 API。沿用 `DATABASE_URL` 与 `CONVERSION_ENVIRONMENT`。使用 `CONVERSION_PRIVATE_ADDR` 配置监听地址，默认 `127.0.0.1:8092`。CMS 与 API 配置相同的 `CONVERSION_PRIVATE_TOKEN`，CMS 另外设置 `CONVERSION_PRIVATE_URL` 指向私网地址，例如同机部署时使用 `http://127.0.0.1:8092`。

CMS 在「广告投放 → 回传中心 → 逐单对账」按订单号查询。默认使用本地订单 ID，分销查询另填写接入 ID。API 路径为 `GET /api/v1/admin/conversion/orders/{order_id}`，分销查询附带 `integration_id`。接口实时复核管理员账号和角色，返回资格、事件、各 Pixel 投递和分销回告摘要。

## 历史队列接管

按架构文档，在维护窗口停住旧写入和 CAPI 消费，等在途请求收尾，再执行 CMS 迁移。接管期间关闭新 Worker 的 Meta 发送。

以下命令从 `conversion-backend` 执行。沿用上表的数据库、环境和密钥配置。每次提交一个批次，默认 100 行，可用 `--limit` 调整。首次运行省略游标，后续把输出的 `after` 传给对应命令，直到返回零条。

| 命令 | 后续批次游标 |
| --- | --- |
| `go run ./cmd/conversion-ops import-legacy-pixels --key-version <version>` | `--after-id <after>` |
| `go run ./cmd/conversion-ops backfill-legacy-subscriptions` | `--after-id <after>` |
| `go run ./cmd/conversion-ops import-legacy-events --evidence <file>` | `--after-event <after>` |

环境证据以旧事件 ID 为键，例如 `{"payment:123:Purchase":{"environment":"production","reference":"审核记录编号"}}`。引用需对应可核验的旧投递环境记录。可省略证据文件，未确认环境的未完成投递进入阻塞。已尝试且未确认接收的事件保留不确定证据。旧成功记录保留确认时间与 Meta ID，源数据缺目标时可在导入账本里对账。

Pixel 导入复用已有凭据，旧明文凭据的清理在切换验收后执行。新建的 Reader 接入账户默认暂停，在核对目标和导入结果后启用。完整切换步骤仍以架构文档为准。

## 运行与运维

- `conversion --mode=worker` 消费 Reader Outbox 和 Meta 投递队列，默认关闭 Meta 发送。
- `conversion --mode=api` 提供 CMS 私网查询与运营操作。写入凭据前，用 `CONVERSION_CREDENTIAL_KEY_VERSION` 选择 `CONVERSION_CREDENTIAL_KEYS` 中的版本。Worker 保留读取仍在使用的密钥版本。
- `conversion --mode=collector` 每 30 秒独立采样，写入告警并记录 `opened` 和 `resolved`。CMS 汇总展示同环境的活动告警。

```bash
conversion-ops review-legacy-environment --legacy-event-id "$LEGACY_EVENT_ID" --evidence-reference "$EVIDENCE" --operator "$ADMIN_ID" --reason "$REASON"
conversion-ops retry-delivery --delivery-id "$DELIVERY_ID" --expected-attempts "$ATTEMPTS" --operator "$ADMIN_ID" --reason "$REASON"
conversion-ops repair-missing --order-id "$ORDER_ID" --event-name Purchase
conversion-ops repair-missing --order-id "$ORDER_ID" --event-name Purchase --apply --operator "$ADMIN_ID" --reason "$REASON" --evidence-reference "$EVIDENCE"
conversion-ops retention --keep-days 30 --limit 100 --apply --operator "$ADMIN_ID" --reason "$REASON"
```

历史环境复核只更新既有导入记录。已证实从未发送的任务可恢复原调度，存在发送迹象的任务进入结果未知并继续适用重复风险确认。缺行补建使用履约事务独立保存的资格事实，旧数据缺乏可信证据时需继续调查。

保留期命令按表分批清理已消费或终结记录的敏感字段，保留稳定事件键、资格、已发送标记、结构化尝试结论、回告业务字段与审计。活动任务及未接管旧队列保留原数据。每批输出为零时结束。

部署顺序、受限角色和回滚步骤见[部署 SOP](../DEPLOYMENT_SOP.md)。

## 测试

```bash
: "${STAR_NOVEL_TEST_DATABASE_URL:?请配置可创建临时数据库的专用 PostgreSQL 测试连接}"
go test -race -count=1 ./...
go vet ./...
```

## 司南阶段 C 模拟联调

使用独立测试数据库与 `CONVERSION_ENVIRONMENT=test`。先升级 schema 并执行基础角色授权。使用迁移所有者向两种独立服务账号执行模拟链路授权。

```bash
psql -X -f cms-backend/db/conversion_sinan_mock_grants.sql \
  -v callback_role=sn_conversion_callback -v partner_worker_role=sn_conversion_partner_worker
```

1. 在 CMS 接入配置中新增 `source=sinan`、`contract_version=sinan-mock-v1` 的测试账户，填写账户键、允许的应用和事件类型。事件类型使用 `attribution-succeeded`、`order-created`、`recharge-paid`。绑定允许的 Pixel，勾选「附加目标」的 Pixel 还须出现在对应应用的 `app_pixels` 中。
2. 在独立测试 API 上，由具备 `ads.pixels:write` 权限的启用管理员调用 `PUT /internal/conversion/integrations/{id}/sinan-mock`。凭据由私网 API 加密保存，审计保留配置和凭据是否轮换。首次配置提供测试回调 Bearer Token 和 Base64 编码的 32 字节 Ed25519 seed。后续配置可省略任一凭据以沿用现有值。请求体从受控文件或部署密钥注入。
3. 启动 `conversion --mode=callback`。设置 `CONVERSION_SINAN_MOCK_ENABLED=true` 和密钥配置。入口默认 `127.0.0.1:8093`，可通过 `CONVERSION_CALLBACK_ADDR` 换成其他回环地址。启动 `--mode=worker` 归一化 Inbox。模拟验收保持 `CONVERSION_META_ENABLED=false`，使用自动测试中的 Meta 接收证据。
4. 独立启动 `conversion --mode=partner-worker`。沿用测试环境及密钥，设置 `CONVERSION_PARTNER_REPORT_ENABLED=true` 开启回告发送。回告目标使用同一网络命名空间内的回环 IP。队列和租约独立于 Meta。Collector 监测 `normalize_inbox`、`partner_report` 心跳、回告阻塞和结果未知。

私网配置请求体示例。应用和 Pixel ID 用字符串。原目标取回调中的 Pixel。`promotion_pixels` 可按应用配置司南推广链 ID 对应的授权 Pixel。缺少显式映射时沿用已授权的原 Pixel。`source_hosts` 按小写主机名精确匹配来源 URL。

```json
{
  "reason": "配置隔离模拟账户",
  "config": {
    "app_pixels": {"1": ["123", "456"]},
    "promotion_pixels": {"1": {"987": "123"}},
    "source_hosts": ["reader.example"],
    "endpoint": "http://127.0.0.1:8094/final",
    "max_age_seconds": 86400,
    "no_trace_encoding": "omit",
    "missing_promotion_encoding": "null",
    "replay_supported": false
  },
  "inbound_token": "<单账户测试 Token>",
  "signing_seed": "<Base64 编码的测试 Ed25519 seed>"
}
```

- 回调使用 `Authorization: Bearer <测试 Token>` 和 `Content-Type: application/json`。模拟契约使用字符串 `event_id`、Unix 秒 `event_time`、原始 `app_id` 和 `pixel_id`。订单入口同时提供 `order_id`，支付提供 `recharge_type`（1 或 2）、十进制 `amount` 和 ISO 4217 `currency`。回调中的 `order_id`、`device_id` 用字符串保存原值。浏览器数据从 `client_ip`、`user_agent` 读取。Inbox 按已存摘要核对重试。迁移前未标记版本的记录先核对完整摘要，再核对旧字段口径。版本 2 始终核对包含这两个浏览器字段的完整摘要。旧记录的摘要和快照原样保留。
- 模拟回告按 `message` 和字段表的 `ad_set_id` 编码。应用、投手和推广链 ID 用 int64，订单、设备、广告及 Pixel ID 保持字符串。模拟结果 `1`、`-1` 使用原投递定稿时间的 Unix 秒作为 `report_time`，`0` 省略时间、消息和 trace。签名使用 Ed25519，签名原文依次为 `POST`、转义路径、Unix 秒、十六进制 nonce、业务请求体 SHA-256 十六进制摘要，使用换行连接。头字段为 `X-Sinan-Timestamp`、`X-Sinan-Nonce` 和 `X-Sinan-Signature`，签名值用标准 Base64。模拟成功条件为 HTTP 200 与 JSON `code=0`。
- `recharge-paid` 按环境、接入、应用和订单号使用付款规则。缺推广 ID 时走应用的 `default`。入口仍按原始回调事件 ID 幂等。同一付款的其他回调 ID 在 Inbox 记录重复或付款事实冲突，附带首条归一化事件 ID，逐单查询可带 `app_id`。
- 缺推广 ID 的新付款最终回告进入契约阻塞。隔离 Mock 可配置 `missing_promotion_encoding=null`，再重排同一回告任务，出站 `promotion_id` 为 JSON `null`。正式编码和成功响应以司南联调为准。
- 无真实 trace 的失败默认阻塞。模拟接收方确认省略或空字符串后，设置 `no_trace_encoding=omit` 或 `empty`，通过 CMS 或 `conversion-ops retry-partner-report` 审计重排。明确拒收的限流按 `Retry-After` 和退避重试。只有模拟账户明确配置 `replay_supported=true` 时才自动重放结果未知的回告。业务请求体从首次进入发送起冻结。司南返回 HTTP 200 且业务码为 0 后，如果首次定稿失败，Worker 会按原发送尝试及其租约令牌保存确认。扫描已收回租约时，若后续尝试尚未发送，可据此确认回告并结束已领取但未发送的重试。后续尝试已发送时，原确认保留为历史证据。CMS 事件详情显示最近 20 次回告的 HTTP 状态、业务码、尝试时间和重试计划。
- 正式上线依赖架构文档 §7.2 的真实样本和联调结果，包括入站验证、签名及字段、无 trace 失败回告和结果未知后的重放语义。模拟模式只接受测试环境和回环地址。


### 投递调度

同一环境的 Worker 共用数据库租约和 Pixel 冷却时间。明确限流按 Pixel 退避，未确认回执仍按原有审计流程处理。同一 Pixel 的两类任务都就绪时，任一类连续领取最多 4 条，计数跨批次、进程和重启保留。就绪超过 1 分钟的非支付任务可提前获得一个名额，随后将机会交回支付任务。只有一类就绪时可以填满批次。

| 环境变量 | 默认值 | 用途 |
| --- | --- | --- |
| `CONVERSION_META_GLOBAL_LIMIT` | `5` | 同环境全局租约上限，所有实例需使用同一值 |
| `CONVERSION_META_CONCURRENCY` | `5` | 单实例发送并发，受全局上限约束 |
| `CONVERSION_META_PIXEL_INTERVAL` | `3s` | 同一 Pixel 的最小领取间隔 |
| `CONVERSION_WORKER_POLL_INTERVAL` | `15s` | 归一化心跳、到期扫描和发送数据库故障后的轮询间隔 |
| `CONVERSION_WORKER_QUEUE_PROBE_INTERVAL` | `250ms` | 归一化和发送队列的空闲探测上限 |
| `CONVERSION_META_REQUEST_TIMEOUT` | `5s` | 每次 Meta HTTP 请求的超时 |
| `CONVERSION_NORMALIZE_BATCH` | `20` | 每个归一化消费者单轮最多处理的消息数 |
| `CONVERSION_READER_NORMALIZERS` | `2` | 每进程 Reader 归一化消费者数，允许 1–3 |
| `CONVERSION_INBOX_NORMALIZERS` | `2` | 已注册的分销 Inbox 每进程消费者数，允许 1–3 |
| `CONVERSION_NORMALIZER_LIMIT` | `4` | 每进程已启用的消费者合计上限，最多使用 Worker 连接池的一半 |
| `CONVERSION_WORKER_DB_MAX_CONNS` | `16` | 每个 Worker 的独立数据库连接池上限 |
| `CONVERSION_DB_MAX_CONNS` | `8` | API、Collector、Callback 和分销回告各自的连接池上限 |
| `CONVERSION_META_BATCH_SIZE` | `1` | 每次 Meta 请求的成员上限，允许 1–100 |

使用独立数据库角色的环境升级至 1934 后，按部署流程重新应用 `cms-backend/db/conversion_role_grants.sql`。Reader 接入的空事件白名单会跳过所有事件。迁移保留已有审计记录的空白名单，对无配置审计的旧 Reader 接入保留历史四事件默认行为。上线前核对有审计记录但白名单仍为空的旧账户。

Collector 每 30 秒采样，首次采样成功前标为等待，最后一次成功采样达到 90 秒时标为过期。明确限流和定稿失败按 Pixel 统计最近 15 分钟。

Reader Outbox 与分销 Inbox 各自连续消费。满批或在 3 秒单轮预算内取得进展后耗尽预算，会马上启动下一轮。空队列从 50 ms 退避到 250 ms，空闲发送器最多每 250 ms 探测一次。归一化数据库错误按 100 ms 至 5 秒退避。每条归一化消息仍独立提交。

吞吐诊断可通过私网 `GET /internal/conversion/throughput?minutes=15&source=starnovel&pixel_id=123456` 或 CMS 同名代理读取。时间窗为 1–60 分钟，来源和 Pixel 均可省略。入口按来源和源事件类型聚合。投递及尝试按来源、统一事件名和 Pixel 聚合。Pixel 筛选仅影响投递和尝试，入口计数按源消息计算。时间数组依次为 P50、P95、P99，单位为毫秒。空样本返回 null。已生成的目标包含进入阻塞等状态的目标，就绪数单独计算。尝试统计使用数据库发送边界；HTTP 开始时间还可用隔离模拟服务器采集。使用此诊断前，按部署流程在事务外应用 `cms-backend/db/conversion_query_indexes.sql`。

隔离采样命令在 `conversion-backend` 下运行。它测量 1,000 笔已提交付款的归一化，以及 20 次单笔冷流量从付款提交到本机模拟 Meta 收到 HTTP 的延迟。

```sh
CONVERSION_THROUGHPUT_SAMPLE=1 CONVERSION_THROUGHPUT_COLD_SAMPLES=20 \
  go test -count=1 -v ./internal/operations -run '^TestThroughputSample$'
```

测试需要设置 `STAR_NOVEL_TEST_DATABASE_URL` 指向可创建一次性数据库的隔离 PostgreSQL。设 `CONVERSION_THROUGHPUT_NORMALIZERS=3` 可测量三个 Reader 消费者。


### 配置与批量切换

线上配置入口为 `deploy/linux/config.yaml` 的 `conversion` 段。运行 `deploy/linux/prepare-config.py` 后，Compose 按 `worker_instances` 启动 Worker，默认两个副本，各自使用 16 个连接。Reader 与 Partner Inbox 各配置 2 个消费者，合计预算上限 4。生产的分销 Inbox 要在正式契约和鉴权接通后注册消费者。测试环境使用 `config.test.yaml` 中的隔离司南适配器，生成的 Worker 实例文件带各自的心跳标识：

```sh
python3 deploy/linux/prepare-config.py --config conversion-backend/config.test.yaml \
  --output-dir /tmp/star-novel-conversion-test-env
```

生成目录含 API、Collector、Callback、分销回告和运维 CLI 的独立环境文件，也含 CMS 代理和 Reader 入队的配置片段。测试启动前将 YAML 中的 DSN 指向已初始化的一次性测试库。单独设置各角色连接时，可在 `conversion` 中使用 `api_database_url`、`worker_database_url`、`collector_database_url`、`callback_database_url`、`partner_worker_database_url`、`ops_database_url`，缺省沿用 `common.database_url`。

升级前先停旧 Worker 的新领取，等待旧租约定稿。由 CMS 执行 `-migrate` 时保持旧发送器停止。更新独立数据库角色授权后，线上首发以 `meta_batch_size: 1` 启动新版并验证确认和恢复，后续按压测和灰度结果调整。缩小批次只影响下一次领取，当前或待恢复的请求按已冻结成员定稿。回退二进制前先清空在途请求，并使用支持当前 schema 和批次账本的版本。

Meta 凭据故障会暂停目标使用当前凭据领取新任务。凭据状态按批次顺序更新。较新的完整回执会恢复当前凭据的领取，较早批次的迟到错误保留在原批次。凭据轮换后可继续领取，已发且接收证据不足的任务仍进入原有的人工核对流程。只在接收总数明确为零时按拒绝重排或阻塞。

吞吐接口的发送、确认及未知成员数随来源筛选变化。请求数、429 和真实批大小使用整批口径，同一请求可能出现在多个来源筛选结果里。Partner Inbox 以自己的 `processed_at` 计数，分别显示新事件、重复归并、隔离及冲突。处理时间从 schema 1933 开始记录，响应通过 `partner_completion_recording_since` 和 `unknown_completion_time` 标明覆盖范围。

批量隔离验收：

```sh
CONVERSION_THROUGHPUT_SAMPLE=1 CONVERSION_THROUGHPUT_BATCH_RUN=1 \
  CONVERSION_THROUGHPUT_BATCH_SIZE=100 CONVERSION_THROUGHPUT_NORMALIZERS=3 \
  go test -count=1 -v ./internal/operations -run '^TestThroughputSample$'
```

这个测试用本机 HTTP 服务模拟 2 秒响应，验证预就绪及 Outbox 两种负载、稳定事件 ID、逐条成功以及 3 秒 Pixel 间隔。用 `CONVERSION_THROUGHPUT_VOLUME`、`CONVERSION_THROUGHPUT_PIXEL_INTERVAL`、`CONVERSION_THROUGHPUT_REQUEST_DELAY` 可设置固定负载并对比 1、10、50、100 批量。生产峰值及 1.5 倍回放需要实际的脱敏流量分布。
