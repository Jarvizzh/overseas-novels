# Star Novel 转化服务

转化服务消费 Reader 事务 Outbox，创建统一事件与各 Pixel 投递任务。迁移由 CMS 的 `-migrate` 入口管理，Worker 启动时要求 schema `2026111909`。应用架构、实施顺序与验收标准见 [统一转化回传架构升级与开发文档](../docs/conversion-reporting-architecture.md)。

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

CMS 在「广告投放 → 回传日志」按订单号查询。默认使用本地订单 ID，分销查询另填写接入 ID。API 路径为 `GET /api/v1/admin/conversion/orders/{order_id}`，分销查询附带 `integration_id`。接口实时复核管理员账号和角色，返回资格、事件、各 Pixel 投递和分销回告摘要。历史日志由 SuperAdmin 查看脱敏明细。

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
2. 在独立测试 API 上，由 Admin 或 SuperAdmin 调用 `PUT /internal/conversion/integrations/{id}/sinan-mock`。凭据由私网 API 加密保存，审计保留配置和凭据是否轮换。首次配置提供测试回调 Bearer Token 和 Base64 编码的 32 字节 Ed25519 seed。后续配置可省略任一凭据以沿用现有值。请求体从受控文件或部署密钥注入。
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
    "replay_supported": false
  },
  "inbound_token": "<单账户测试 Token>",
  "signing_seed": "<Base64 编码的测试 Ed25519 seed>"
}
```

- 回调使用 `Authorization: Bearer <测试 Token>` 和 `Content-Type: application/json`。模拟契约使用字符串 `event_id`、Unix 秒 `event_time`、原始 `app_id` 和 `pixel_id`。订单入口同时提供 `order_id`，支付提供 `recharge_type`（1 或 2）、十进制 `amount` 和 ISO 4217 `currency`。回调中的 `order_id`、`device_id` 用字符串保存原值。浏览器数据从 `client_ip`、`user_agent` 读取。Inbox 按已存摘要核对重试。迁移前未标记版本的记录先核对完整摘要，再核对旧字段口径。版本 2 始终核对包含这两个浏览器字段的完整摘要。旧记录的摘要和快照原样保留。
- 模拟回告按 `message` 和字段表的 `ad_set_id` 编码。应用、投手和推广链 ID 用 int64，订单、设备、广告及 Pixel ID 保持字符串。模拟结果 `1`、`-1` 使用原投递定稿时间的 Unix 秒作为 `report_time`，`0` 省略时间、消息和 trace。签名使用 Ed25519，签名原文依次为 `POST`、转义路径、Unix 秒、十六进制 nonce、业务请求体 SHA-256 十六进制摘要，使用换行连接。头字段为 `X-Sinan-Timestamp`、`X-Sinan-Nonce` 和 `X-Sinan-Signature`，签名值用标准 Base64。模拟成功条件为 HTTP 200 与 JSON `code=0`。
- 无真实 trace 的失败默认阻塞。模拟接收方确认省略或空字符串后，设置 `no_trace_encoding=omit` 或 `empty`，通过 CMS 或 `conversion-ops retry-partner-report` 审计重排。明确拒收的限流按 `Retry-After` 和退避重试。只有模拟账户明确配置 `replay_supported=true` 时才自动重放结果未知的回告。业务请求体从首次进入发送起冻结。司南返回 HTTP 200 且业务码为 0 后，如果首次定稿失败，Worker 会按原发送尝试及其租约令牌保存确认。扫描已收回租约时，若后续尝试尚未发送，可据此确认回告并结束已领取但未发送的重试。后续尝试已发送时，原确认保留为历史证据。CMS 事件详情显示最近 20 次回告的 HTTP 状态、业务码、尝试时间和重试计划。
- 正式上线依赖架构文档 §7.2 的真实样本和联调结果，包括入站验证、签名及字段、无 trace 失败回告和结果未知后的重放语义。模拟模式只接受测试环境和回环地址。
