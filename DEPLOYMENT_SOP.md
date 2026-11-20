# STAR NOVEL 生产上线部署标准化操作指南 (SOP)

本文档面向 **STAR NOVEL（海外小说平台与运营管理系统）** 运维与研发团队，详细定义了 **「宝塔 Nginx 统一网关 (H5 + CMS 静态托管) + 读者 API 多 ECS 负载均衡集群 + CMS 运营单实例 + 独立统一转化回传服务 (`conversion-backend`: api / worker / collector) + 阿里云 RDS PostgreSQL + Redis」** 生产部署架构的全流程落地规范与维护手册。

---

## 🏗️ 一、 生产部署全景架构图 (Architecture Topology)

```mermaid
flowchart TD
    %% 样式定义
    classDef clientStyle fill:#EBF5FB,stroke:#2980B9,stroke-width:2px,color:#1B4F72;
    classDef cdnStyle fill:#FEF9E7,stroke:#F39C12,stroke-width:2px,color:#7D6608;
    classDef gatewayStyle fill:#E8F8F5,stroke:#1ABC9C,stroke-width:2px,color:#0E6251;
    classDef backendStyle fill:#F4ECF7,stroke:#8E44AD,stroke-width:2px,color:#512E5F;
    classDef dataStyle fill:#FDEDEC,stroke:#E74C3C,stroke-width:2px,color:#78281F;
    classDef extStyle fill:#EAEDED,stroke:#7F8C8D,stroke-width:1px,color:#34495E;

    %% 1. 用户与接入层
    subgraph Layer1 ["【接入与边缘加速层】"]
        ReaderUser["📱 海外移动端读者 (H5)"]:::clientStyle
        AdminUser["💻 运营管理员 (CMS)"]:::clientStyle
        PartnerSrc["🤝 外部分销平台<br/>(如番茄司南，按阶段启用)"]:::extStyle
        CDN["⚡ Cloudflare 全球 CDN<br/>(静态资源 Edge 缓存 / SSL 卸载 / DDoS 防护)"]:::cdnStyle
        
        ReaderUser -->|"h5.star-novel.com<br/>*.star-novel.com"| CDN
        AdminUser -->|"admin.star-novel.com"| CDN
        PartnerSrc -.->|"Webhook /callbacks/... (仅授权入站)"| CDN
    end

    %% 2. 宝塔 Nginx 统一网关与前端机
    subgraph Layer2 ["【网关与运营控制层 (ECS-Gateway)】"]
        BTNginx["🌐 宝塔 Nginx 统一网关 (:80 / :443)<br/>• 证书管理 & 强制 HTTPS<br/>• SPA 路由回退 (/pixels, /conversions)<br/>• 真实客户端 IP 透传 (X-Real-IP)"]:::gatewayStyle
        
        subgraph StaticFiles ["静态资源托管"]
            H5Dist["📁 reader-front 静态产物<br/>(/www/wwwroot/h5.../dist)"]:::gatewayStyle
            CMSDist["📁 cms-front 静态产物<br/>(/www/wwwroot/admin.../dist)"]:::gatewayStyle
        end
        
        CMSNode["⚙️ cms-backend 单实例<br/>(Docker :8081)"]:::backendStyle
    end

    CDN -->|"HTTPS 统一回源"| BTNginx
    BTNginx -->|"GET /* (H5页面)"| H5Dist
    BTNginx -->|"GET /* (CMS页面)"| CMSDist
    BTNginx -->|"/api/v1/admin/* (反代)"| CMSNode

    %% 3. VPC 内网读者后端集群
    subgraph Layer3 ["【阿里云 VPC 专有网络 - 读者端后端集群】"]
        UpstreamRoute{"🔀 Nginx Upstream 调度<br/>(least_conn 最少连接均衡)"}:::gatewayStyle
        
        ReaderNode1["🚀 ECS-Reader-1<br/>reader-backend (:8080)<br/>事务写 Outbox，不直连 Meta"]:::backendStyle
        ReaderNode2["🚀 ECS-Reader-2<br/>reader-backend (:8080)"]:::backendStyle
        ReaderNodeN["🚀 ECS-Reader-N (弹性扩容)<br/>reader-backend (:8080)"]:::backendStyle
    end

    BTNginx -->|"/api/v1/* (读者API)"| UpstreamRoute
    UpstreamRoute -->|"内网分发"| ReaderNode1
    UpstreamRoute -->|"内网分发"| ReaderNode2
    UpstreamRoute -->|"内网分发"| ReaderNodeN

    %% 3.5 独立统一转化回传服务
    subgraph Layer3Conv ["【阿里云 VPC / 私网 - 统一转化回传服务 (conversion-backend)】"]
        ConvAPI["🔐 conversion --mode=api<br/>私网接口 (127.0.0.1 / 内网 :8092)<br/>Bearer Token + 操作人角色校验"]:::backendStyle
        ConvWorker["⚡ conversion --mode=worker<br/>消费 Outbox & Inbox / Meta 异步投递<br/>租约隔离 + 审计重排 + 定稿"]:::backendStyle
        ConvCollector["📡 conversion --mode=collector<br/>30s 独立心跳/积压采集与告警"]:::backendStyle
        ConvCallback["🪝 conversion --mode=callback / partner-worker<br/>(仅分销阶段启用，隔离测试/授权放行)"]:::backendStyle
    end

    CMSNode -->|"/internal/conversion/*<br/>(CONVERSION_PRIVATE_URL + Token)"| ConvAPI
    BTNginx -.->|"/callbacks/* (仅分销正式接入放行)"| ConvCallback

    %% 4. 集中数据与持久化层
    subgraph Layer4 ["【阿里云 VPC 专有网络 - 集中数据层】"]
        RDS[("🗄️ 阿里云 RDS PostgreSQL 18<br/>(schema: public + conversion)<br/>• reader_conversion_outbox 事务出站<br/>• conversion.* 统一事件/任务/审计/密文<br/>• 当前统一使用 RDS 账号 star_novel")]:::dataStyle
        
        Redis[("⚡ Redis 7 缓存<br/>(独立 Compose 内网或 VPC ECS :6379)<br/>• 0~20% Jitter 缓存防雪崩<br/>• SingleFlight / 分布式锁 / AOF")]:::dataStyle
    end

    ReaderNode1 --> RDS & Redis
    ReaderNode2 --> RDS & Redis
    ReaderNodeN --> RDS & Redis
    CMSNode --> RDS & Redis
    ConvAPI --> RDS
    ConvWorker --> RDS
    ConvCollector --> RDS
    ConvCallback -.-> RDS

    %% 5. 海外第三方对接
    subgraph Layer5 ["【海外第三方生态】"]
        StripePay["💳 PayPal / 支付网关 (Stripe 当前停用)"]:::extStyle
        FBCAPI["📊 Meta Conversions API (异步可靠回传)"]:::extStyle
        PartnerReport["📤 分销平台回告端点 (阶段 C)"]:::extStyle
    end

    ReaderNode1 -.-> StripePay
    ConvWorker -.-> FBCAPI
    ConvCallback -.-> PartnerReport
```

---

## 📋 二、 服务器规划与网络矩阵

| 机器/服务角色 | 部署组件与职责 | 监听端口 / 访问模式 | 数据库连接账号 |
| :--- | :--- | :--- | :--- |
| **网关与管理机 (`ECS-Gateway`)** | 宝塔面板 + Nginx (统一网关) + `reader-front` 静态 + `cms-front` 静态 + `cms-backend` 容器 | `80`, `443` (公网开放)<br/>`127.0.0.1:8081` (仅本机反代) | `star_novel`（当前统一 RDS 账号） |
| **转化私网 API (`conversion-api`)** | `conversion --mode=api`：Pixel/接入配置、凭据加密写入、只读变更审计与订单全链路对账、审计重排 | `8092`（**仅回环 `127.0.0.1` 或 Docker/VPC 私网互通，严禁公网暴露**） | `star_novel`（当前统一 RDS 账号） |
| **转化异步 Worker (`conversion-worker`)** | `conversion --mode=worker`：消费 `reader_conversion_outbox` / `conversion.inbox`、投递 Meta CAPI、定稿 | 无入站监听端口（仅主动出站访问 RDS 与 Meta Graph API） | `star_novel`（当前统一 RDS 账号） |
| **转化采集告警 (`conversion-collector`)** | `conversion --mode=collector`：独立每 30 秒采样积压、缺路由与 Worker 心跳写入 `conversion.alerts` | 无入站监听端口（仅主动查询 RDS） | `star_novel`（当前统一 RDS 账号） |
| **分销回调与回告（阶段 C 启用）** | `conversion --mode=callback` + `partner-worker`：第三方分销入站 Webhook 与原目标最终结果回告 | `:8093`（仅限已验证接入的特定 `/callbacks/...` 路径反代放行） | `star_novel`（当前统一 RDS 账号；阶段 C 按需拆分） |
| **读者端集群节点 (`ECS-Reader-1..N`)** | `reader-backend` Go API 容器 | `8080` (仅 VPC 内网 / 本机回环) | `star_novel`（当前统一 RDS 账号） |
| **Redis 7 缓存** | Docker Compose 内置 `redis:7`（推荐，见 [deploy/linux/README.md](deploy/linux/README.md)）或 VPC 内网 ECS | `6379` (仅内网，严禁公网开放) | — |
| **阿里云 RDS PostgreSQL** | 主业务数据库（含 `pg_trgm` 与 `conversion` schema） | `3433` 或 `5432` (VPC 专网连接) | 统一使用 `star_novel`（含迁移与全部业务表权限） |

> [!IMPORTANT]
> **网络与安全边界原则**：
> 1. 所有 ECS、Redis 与 RDS 必须处于 **同一个阿里云专有网络（VPC）**；推荐容器仅绑定宿主机 `127.0.0.1`（或隔离容器网段如 `172.30.87.0/24`）配合宝塔 Nginx 统一反代，禁止裸露 `0.0.0.0` 业务端口。
> 2. **Reader、CMS 与 8092 私网转化 API 之间严禁对公网暴露**：`conversion-api (:8092)` 仅接受持有 `CONVERSION_PRIVATE_TOKEN` 的 `cms-backend` 内网请求；读者端节点与 Redis 节点同样严禁在安全组对公网开放。
> 3. **部署工具链与当前状态说明**：仓库内 `deploy/linux/`（见 [deploy/linux/README.md](deploy/linux/README.md)）目前编排了 `redis`、`cms`、`reader` 三个容器；`conversion-backend` 需使用独立镜像（[conversion-backend/Dockerfile](conversion-backend/Dockerfile)）并接入同 VPC / Docker 私网后同步运行，**严禁仅升级 Reader/CMS 而未部署 `conversion-worker` 与 `conversion-api`**，否则新订单会堆积在 Outbox 且 CMS 对账页将报 `503` 服务不可用。

---

## 🗄️ 三、 第一阶段：数据基础设施初始化 (RDS & Redis)

### 3.1 阿里云 RDS PostgreSQL 配置
1. **白名单配置**：在 RDS 控制台 【白名单与安全组】 中，将 VPC 内网网段（如 `172.16.0.0/24`）加入白名单；
2. **账号与数据库创建**：统一使用高权限账号 `star_novel` 与业务数据库 `star_novel`（或既有线上库 `star_novel_db`），字符集选择 `UTF8`；
3. **初始化或升级 Schema**。使用本次发布的 CMS 镜像，通过同一份 CMS 环境配置运行迁移命令。
   ```bash
   docker run --rm --env-file cms-backend/.env star-cms-backend:latest -migrate
   ```
   `schema.sql` 是版本 `2026111701` 的基线。迁移命令根据数据库版本依次执行尚未应用的 SQL，新库同时写入初始化数据。同版本的 CMS 和 Reader 启动与健康检查只读取版本账本。迁移失败会回滚并返回非零退出码。使用 `CREATE INDEX CONCURRENTLY` 的查询索引脚本仍单独在事务外执行。

   **章节发布升级 `2026111702`**。安排一次短维护窗口，先停止旧 CMS 的发布写入和旧 Reader 的扣费流量。运行迁移后配套启动新版 CMS、Reader API 和 H5，确认各服务通过健康检查再恢复流量。迁移等待业务表锁最多 3 秒，单条 SQL 和迁移进程分别有 60 秒和 90 秒上限。出现锁超时先解除业务锁再重试。新版开始发布后，旧程序无法读取不可变正文引用。回退须协调应用与数据库共同恢复。

   **阅读性能升级 `2026111703`**。继续使用同一个 CMS `-migrate` 命令，已在 `2026111702` 的数据库只执行新增迁移。启用 OSS 正文的环境，在迁移成功后用新版 CMS 镜像和原配置回填历史预览，回填可分批重跑，已生成的预览会跳过。
   ```bash
   docker run --rm --env-file cms-backend/.env star-cms-backend:latest -backfill-chapter-previews
   ```
   检查回填结果后切换 Reader 流量。仍待回填的 OSS 章节会显示付费墙，已授权的读者仍能读取全文。PostgreSQL 正文可直接生成历史预览。

   **订阅对账、监控和书架升级 `2026111704`**。先用新版 CMS 镜像运行 `-migrate`。迁移会回填订阅状态变更时间和无归因事件计数，安排维护窗口并停止支付写入和后台任务，按存量规模预演迁移耗时。迁移成功后，用同一个业务库单独执行并发索引脚本，然后发布 CMS、Reader API，最后发布 H5。

   ```bash
   psql -X -v ON_ERROR_STOP=1 -f cms-backend/db/reader_query_indexes.sql
   ```

   脚本需要自动提交。核对新索引的定义和有效性后，对 `user_subscriptions`、`payment_event_outbox`、`bookshelves` 更新统计信息。旧版的 `idx_paypal_subscription_claim`、`idx_reader_shelf_feed` 如已存在，可在升级完成并确认没有依赖后单独并发删除。新版本的任务汇总在同一数据库内每分钟最多采集一次，保留 `PAYMENT_TASK_METRICS_ENABLED` 实例开关和手工查询。

   已在 `2026111704` 的环境升级订单领取时，先重跑上述索引脚本，确认 `idx_paypal_order_recent_pending` 和 `idx_paypal_order_retained_claim` 均有效，再发布 Reader API。可对 `recharge_orders` 执行 `ANALYZE` 更新统计信息。旧索引 `idx_paypal_order_claim` 可在旧版本全部退出且确认没有依赖后单独并发删除。

   Reader 单实例数据库连接默认前台 21、支付后台 4。可用 `READER_DB_FOREGROUND_MAX_CONNS` 和 `READER_DB_BACKGROUND_MAX_CONNS` 调整，按 Reader 实例数、CMS 和运维进程预留 RDS 总连接数。阅读请求和不同缓存回源的并发分别由 `READER_MAX_IN_FLIGHT_READS` 和 `READER_MAX_CONTENT_LOADS` 控制，默认 48 和 12。Reader 的 `/healthz` 提供两组池的累计等待、取消和使用量，数据库正常而 Redis 不可用时仍返回 200，由有界回源提供服务。CMS 的就绪检查使用现有依赖策略。

   历史单章权益通过 `unlock_records.chapter_id` 迁移。仅在 PostgreSQL 能证明当前章节行已在购买前提交时自动关联。未启用或已缺失提交时间记录的数据库，需要从原始证据复核。恢复扣费流量前导出并处理下列记录。对每条记录，应根据购买时间的正文版本、备份或其他原始购买证据确认章节身份。确认后在短事务中锁住对应的 `novels` 行，填写同一小说的稳定 `chapter_id`、包含证据位置和审核人的 `identity_evidence`，以及 `identity_resolved_at`。同一用户已有该章节的有效记录时，先核对重复扣款再处置。待复核期间，读者仍可使用已确认权益，新的单章购买会提示联系支持。

   ```sql
   SELECT id, user_id, novel_id, chapter_index, created_at
   FROM unlock_records
   WHERE chapter_id IS NULL
   ORDER BY novel_id, id;
   ```

   OSS 切换沿用原有 bucket 和 base path。CMS 需要正文的写、读和删除权限。旧版正文保持可读，内容被替换后，未引用对象进入延迟清理。可按 `chapter_content_objects.state`、`attempts`、`last_error` 检查清理状态。

   部署新版支付前，先执行 CMS 迁移命令，再配套更新 CMS、Reader API 与 Reader H5。为 Reader 设置当前 PayPal 应用对应的 `PAYPAL_WEBHOOK_ID`（若用 Linux 配置生成脚本，则设置 `payment.paypal_webhook_id`）。注册订单批准、扣款完成、支付和订阅状态、退款完成的通知。Stripe 接口当前返回停用。CMS 必须配置独立的 `CMS_JWT_SECRET`（Linux 配置生成脚本读取 `cms.jwt_secret`），切换后 CMS 管理员需重新登录。 完成迁移后，可从 CMS 订单接口的 `reconciliation_note` 定位待核对的历史订单或账期。核实存量 PayPal 交易和退款凭证后再补发权益。


   支付修复升级还需确认以下事项。

   - CMS 的生产示例使用 `CMS_JWT_SECRET`。复制示例后，用 `openssl rand -hex 32` 生成独立密钥并填写实际配置。生产启动会拒绝占位值和开发默认值。
   - `paypal_refund_items` 按真实退款 ID 保存已验证的退款明细，累计退满原扣款后冲销金币与权益。已有 inbox 中的退款记录会迁入明细表，退款差额继续保存在 `payment_refunds`。
   - 新订阅会先持久化账号级创建意图。相同请求恢复原审批链接，存在有效渠道订阅时返回冲突。更换套餐需先确认旧订阅已在渠道取消或到期。渠道已收到请求但本地未取得结果且超过 24 小时的意图，需要按渠道账单核对后处理。
   - 取消前保存渠道确认的账期快照。外部取消后，金额、币种和周期锚点均匹配的普通扣款可恢复已付周期，无法确认的补扣继续显示 `reconciliation_note`。
   - 历史整本订单必须在 `legacy_whole_book_proofs` 中有独立的原商品类型与小说证据。审核人需记录 `order_id`、`novel_id`、原始证据定位 `evidence_reference` 和审核管理员 `verified_by`。证据中的小说必须与订单保存的原目标一致，目标缺失或错误时须依据原始商品凭证修正后再登记。核实渠道收款后仅补齐权益，已入账金币保留原账。此前已回填的历史整本权益也会检查此证据，缺少证据的订单需人工核对。

   上线前执行以下只读查询，核对历史重复订阅与缺少整本证据的订单，并保存核对结果。重复订阅需分别核查渠道状态和用户权益后处理。

   ```sql
   SELECT user_id, count(*) AS subscription_count,
          array_agg(subscription_id ORDER BY id) AS subscription_ids
   FROM user_subscriptions
   WHERE payment_method='paypal'
     AND status NOT IN ('CANCELLED','EXPIRED')
     AND subscription_id NOT LIKE 'I-MOCK%'
   GROUP BY user_id HAVING count(*)>1;

   SELECT o.id, o.user_id, o.external_ref_id, o.novel_id, o.status, o.reconciliation_note
   FROM recharge_orders o
   WHERE o.payment_method='paypal' AND o.order_type='whole_book'
     AND o.checkout_id IS NULL AND o.status IN ('Pending','Success','Paid')
     AND NOT EXISTS (SELECT 1 FROM legacy_whole_book_proofs p
                     WHERE p.order_id=o.id AND p.novel_id=o.novel_id)
   ORDER BY o.id;

   SELECT user_id, intent_id, slot_id, plan_id, subscription_id, submitted_at
   FROM paypal_subscription_intents
   WHERE subscription_id IS NULL AND submitted_at < now()-interval '24 hours';
   ```

#### 统一转化回传切换（schema `2026111909`）

本次架构升级将广告转化追踪彻底解耦为「**Reader 事务写 Outbox + `conversion-backend` 异步消费 / 私网控制 / 独立监控告警 + CMS 可视化回传中心与像素管理**」，同时更新 Reader、CMS、转化 Worker、私网 API 和独立 Collector。**测试与生产必须使用独立数据库、独立 `CONVERSION_ENVIRONMENT` 及独立测试 Pixel**，私网 API 只接受 CMS 私网调用。

1. **停写与窗口迁移**：在维护窗口记录 `reader_schema_migrations` 版本账本、`payment_event_outbox` 各状态数量和已确认的 Meta 事件 ID（当前部署策略暂不额外执行数据库备份）。暂停旧业务写入和旧 CAPI 消费；如测试环境曾启动司南模拟服务，同步暂停回调入口和回告 Worker。等待在途请求及租约结束，使用新版 CMS 镜像与 `star_novel` 连接执行 `-migrate`，推进 schema 至 **`2026111909`**（含 `conversion` schema 15 张表、`reader_conversion_outbox`、`reader_conversion_repair_facts` 及订单资格扩展字段）。
2. **创建并发保留期与查询索引**：使用 `star_novel` 在**事务外（保持自动提交）**单独执行：
   ```bash
   psql -X -v ON_ERROR_STOP=1 -f cms-backend/db/conversion_query_indexes.sql
   ```
   核对新增索引（`recharge_orders_conversion_fulfilled_at`、`conversion_outbox_retention`、`conversion_repair_fact_retention`、`conversion_event_retention`、`conversion_inbox_retention`）的 `pg_index.indisvalid=true`，并在接近生产规模的副本上检查执行耗时和查询计划。
3. **数据库权限说明（当前暂不拆分多账号）**：当前所有后端服务统一使用 RDS 账号 `star_novel` 连接，**暂无需单独创建多个 RDS 子账号，也暂不运行 `cms-backend/db/conversion_role_grants.sql`（以及阶段 C 的 `conversion_sinan_mock_grants.sql`）**；待后续需收紧生产最小权限隔离时，再按该脚本拆分为 `reader_role` / `cms_role` / `worker_role` / `api_role` / `collector_role` / `retention_role`（其中 `api_role` 需保留对 `conversion.operation_audit` 的 `SELECT, INSERT` 权限以支撑只读审计时间线）。
4. **历史队列与 Pixel 凭据接管**：使用 `star_novel` 账号运行[历史队列接管](conversion-backend/README.md#历史队列接管)（`conversion-ops import-legacy-pixels` / `backfill-legacy-subscriptions` / `import-legacy-events`）。补齐旧投递的环境证据后可用 `conversion-ops review-legacy-environment` 审计复核，确认切换完成后再清理旧表中的明文凭据。
5. **配置密钥与首启转化服务**：按第五章 §5.3 部署并启动 `conversion-api`（`:8092`）、`conversion-worker` 和 `conversion-collector`。**初次启动务必将 Worker 的 `CONVERSION_META_ENABLED` 设为 `false`**，确认 `worker` 已正常归一化 Outbox 并更新心跳后，核对测试订单入队、逐单资格与各 Pixel 任务无误，再将 `CONVERSION_META_ENABLED` 改为 `true` 重启 `worker` 放量。
6. **启动新版 Reader、CMS 与前端并核对告警**：按第四章 §4.3、第五章 §5.2 确保 `reader-backend` 设置一致的 `CONVERSION_ENVIRONMENT`，`cms-backend` 正确配置 `CONVERSION_PRIVATE_URL` 与共享 `CONVERSION_PRIVATE_TOKEN`。保持 Collector 运行，停用 Worker 验证心跳告警触发（`alerts`），恢复 Worker 后确认日志记录 `opened` 与 `resolved`。
7. **运维工具切换与回滚边界**：PayPal 对账继续使用 `payment-ops`；转化重排、旧环境复核、缺行补建和保留期清理统一改由 CMS「回传中心」或[转化运维命令](conversion-backend/README.md#运行与运维)执行（旧 `payment-ops -kind outbox` 不再对应新转化队列）。迁移提交后先暂停故障链路再用匹配当前 schema 的版本修复或前滚；回退旧代码前须核对切换后新增业务、未发任务及已确认的 `meta_event_id` 证据。
8. **司南阶段 C 约定**：使用独立测试库（`CONVERSION_ENVIRONMENT=test`）、测试凭据与回环模拟端点，按[模拟联调步骤](conversion-backend/README.md#司南阶段-c-模拟联调)完成入站鉴权、三个回调与无 trace 失败回告确认后再开启生产接入（生产严禁启用 `CONVERSION_SINAN_MOCK_ENABLED=true`）。

### 3.2 自建 Redis 生产配置
修改 Redis 所在机器的 `/etc/redis/redis.conf`：
```ini
bind 172.16.0.15 127.0.0.1
protected-mode yes
port 6379

# 必须设置强密码
requirepass YOUR_STRONG_REDIS_PASSWORD_2026

# 数据持久化
appendonly yes
appendfsync everysec

# 内存上限与淘汰策略
maxmemory 3gb
maxmemory-policy volatile-lru
```
重启并验证连接：
```bash
systemctl restart redis
redis-cli -a YOUR_STRONG_REDIS_PASSWORD_2026 ping  # 预期返回 PONG
```

---

## 🚀 四、 第二阶段：读者端 Go 后端多节点集群部署

在 **每一台** 读者端业务机（`ECS-Reader-1`, `ECS-Reader-2`...）上执行：

### 4.1 安装 Docker 环境
```bash
curl -fsSL https://get.docker.com | sh
sudo systemctl enable --now docker
```

### 4.2 准备 `reader-backend/Dockerfile` (基于预编译 Linux 二进制)
```dockerfile
# =========================================================================
# STAR NOVEL - reader-backend 极速轻量运行镜像 (基于预编译 Linux 二进制)
# =========================================================================
FROM alpine:3.19

WORKDIR /app

# 安装基础运行时依赖 (CA 证书、时区、健康检查 curl)
RUN apk add --no-cache ca-certificates tzdata curl
ENV TZ=UTC

# 复制在本地预编译好的 Linux 静态二进制文件
COPY reader-server-linux-amd64 /app/server
RUN chmod +x /app/server

# 暴露读者端 API 端口
EXPOSE 8080

# 容器启动入口
ENTRYPOINT ["/app/server"]
```

### 4.3 配置生产环境变量 `reader-backend/.env`
> [!WARNING]
> 所有 `reader-backend` 节点的 `JWT_SECRET` 与基础配置必须保持 **100% 绝对一致**。

```ini
GIN_MODE=release
PORT=8080

# 数据库与缓存内网连接（统一使用 RDS 账号 star_novel）
DATABASE_URL=postgres://star_novel:YOUR_RDS_PASSWORD@pgm-xxxxxx.pgsql.rds.aliyuncs.com:3433/star_novel?sslmode=disable
REDIS_URL=redis://:YOUR_STRONG_REDIS_PASSWORD_2026@172.16.0.15:6379/0

# 全集群必须一致的 JWT 密钥
JWT_SECRET=STAR_NOVEL_SUPER_JWT_KEY_PROD_2026_RANDOM_STRING

# 海外支付 Live 秘钥
STRIPE_SECRET_KEY=sk_live_xxxxxx
PAYPAL_CLIENT_ID=xxxxxx
PAYPAL_CLIENT_SECRET=xxxxxx
PAYPAL_MODE=live

# 默认主站落地页域名
DEFAULT_DOMAIN=https://h5.starsfic.com

# 统一转化回传所属环境（需与 conversion-backend 一致；缺省时默认为 production）
CONVERSION_ENVIRONMENT=production

# 章节正文存储引擎配置 ("postgres" 或 "oss")
STORAGE_TYPE=postgres
# 若启用 OSS 存储，配置以下凭证:
OSS_ENDPOINT=oss-cn-hangzhou.aliyuncs.com
OSS_ACCESS_KEY_ID=your_aliyun_oss_access_key_id
OSS_ACCESS_KEY_SECRET=your_aliyun_oss_access_key_secret
OSS_BUCKET=star-novel-content
OSS_BASE_PATH=novels
```

### 4.4 构建并启动容器 (含容器清理与多重验证)
```bash
cd reader-backend

# 1. 构建镜像 (基于预编译二进制，仅耗时 1 秒，0 CPU 开销)
docker build -t star-reader-backend:latest .

# 2. 优雅停止并清理旧容器 (防端口与名称冲突)
docker stop star_reader_backend 2>/dev/null || true
docker rm star_reader_backend 2>/dev/null || true

# 3. 启动新容器
docker run -d \
  --name star_reader_backend \
  --restart always \
  -p 8080:8080 \
  --env-file .env \
  star-reader-backend:latest
```

#### 4.5 三步确认服务是否 100% 启动成功
* **第一步：查看容器运行状态**
  ```bash
  docker ps | grep star_reader_backend
  # 预期输出: STATUS 列显示 "Up X seconds" (持续存活未退出)
  ```
* **第二步：查看容器核心初始化日志**
  ```bash
  docker logs --tail 20 star_reader_backend
  # 预期包含:
  # Database connection pool initialized successfully
  # Redis client initialized successfully
  # Reader Content Storage initialized with driver: postgres (或 oss)
  # Star Novel backend starting on port 8080
  ```
* **第三步：健康检查接口自测**
  ```bash
  curl http://127.0.0.1:8080/healthz
  # 预期返回: {"status":{"database":"UP","redis":"UP"},"timestamp":"..."}
  ```

> [!TIP]
> **启动异常常见排查字典**：
> 1. 若 `docker ps` 显示 `Exited (1)`，立即执行 `docker logs star_reader_backend` 查看末尾报错：
>    * `Database connection failed`：检查 RDS 白名单是否已添加该 ECS 的内网 IP，并核对 `.env` 中的数据库密码；
>    * `Redis connection failed`：检查 Redis 容器或服务是否已启动，核对内网 IP/密码；
>    * `[CRITICAL SECURITY ERROR] JWT_SECRET...`：`GIN_MODE=release` 模式下严禁使用默认密钥，请修改 `.env` 中的 `JWT_SECRET` 为自定义复杂字符串。

---

## 🛠️ 五、 第三阶段：CMS 运营后端与统一转化服务部署

在网关/管理机（`ECS-Gateway` 或独立内网节点）上操作，确保 **`conversion-backend`** 与 **`cms-backend`** 处于同一可控私网内（例如共享 Docker 自定义网络或同宿主机 `127.0.0.1` 回环通信）。

### 5.1 准备 `cms-backend/Dockerfile` (基于预编译 Linux 二进制)
仓库根目录下的 `cms-backend/Dockerfile` 结构如下：
```dockerfile
# =========================================================================
# STAR NOVEL - cms-backend 极速轻量运行镜像 (基于预编译 Linux 二进制)
# =========================================================================
FROM alpine:3.19

WORKDIR /app

# 安装基础运行时依赖 (CA 证书、时区、健康检查 curl)
RUN apk add --no-cache ca-certificates tzdata curl
ENV TZ=UTC

# 复制在本地预编译好的 Linux 静态二进制文件
COPY cms-server-linux-amd64 /app/cms-server
RUN chmod +x /app/cms-server

# 暴露 CMS API 端口
EXPOSE 8081

# 容器启动入口
ENTRYPOINT ["/app/cms-server"]
```

### 5.2 配置 `cms-backend/.env` 并启动
```ini
GIN_MODE=release
PORT=8081
# 统一使用 RDS 账号 star_novel
DATABASE_URL=postgres://star_novel:YOUR_RDS_PASSWORD@pgm-xxxxxx.pgsql.rds.aliyuncs.com:3433/star_novel?sslmode=disable
REDIS_URL=redis://:YOUR_STRONG_REDIS_PASSWORD_2026@172.16.0.15:6379/0
CMS_JWT_SECRET=<独立于 Reader 的至少 32 字符随机密钥，可用 openssl rand -hex 32 生成>
DEFAULT_DOMAIN=https://h5.starsfic.com

# =========================================================
# 统一转化回传私网对接 (必须与 conversion-api 一致)
# =========================================================
# • 若使用同一 Docker 私网通信，指向容器名 http://conversion-api:8092
# • 若与 conversion-api 同宿主机非容器网络运行，使用 http://127.0.0.1:8092
# 注意：未配置或不可达时，CMS「像素管理」与「回传中心」在线视图将返回 503 降级提示
CONVERSION_PRIVATE_URL=http://conversion-api:8092
CONVERSION_PRIVATE_TOKEN=<与 conversion-api 一致的强随机 Bearer Token>

# 章节正文存储引擎配置 ("postgres" 或 "oss")
STORAGE_TYPE=postgres
# 若启用 OSS 存储，配置以下凭证:
OSS_ENDPOINT=oss-cn-hangzhou.aliyuncs.com
OSS_ACCESS_KEY_ID=your_aliyun_oss_access_key_id
OSS_ACCESS_KEY_SECRET=your_aliyun_oss_access_key_secret
OSS_BUCKET=star-novel-content
OSS_BASE_PATH=novels
```

启动容器（含容器清理防冲突）：
```bash
cd cms-backend

# 1. 构建镜像 (基于预编译二进制，耗时不到 1 秒)
docker build -t star-cms-backend:latest .

# 2. 优雅停止并清理旧容器 (防端口与名称冲突)
docker stop star_cms_backend 2>/dev/null || true
docker rm star_cms_backend 2>/dev/null || true

# 3. 启动新容器（接入私网供调用 conversion-api）
docker run -d \
  --name star_cms_backend \
  --restart always \
  --network "$STAR_NOVEL_PRIVATE_NETWORK" \
  -p 127.0.0.1:8081:8081 \
  --env-file .env \
  star-cms-backend:latest

# 4. 验证健康检查
curl -fsS http://127.0.0.1:8081/healthz
```

> [!NOTE]
> **关于 `deploy/linux/` 自动化部署包**：若生产使用 `deploy/scripts/build-linux.sh` 与 `deploy/linux/compose.yaml`（参见 [deploy/linux/README.md](deploy/linux/README.md)），当前 `compose.yaml` 仅内置 `redis`（`172.30.87.0/24`）、`cms` 与 `reader`。部署新版前，须确认 `conversion-api` 已接入同一容器网络（或宿主机回环可访路径），并在生成的 `cms.env` 中配置 `CONVERSION_PRIVATE_URL` 与 `CONVERSION_PRIVATE_TOKEN`。

### 5.3 部署独立统一转化回传服务 (`conversion-backend`)

`conversion-backend/Dockerfile` 采用多阶段构建，打包出 `/app/bin/conversion`（常驻服务入口，支持 `--mode=api|worker|collector|callback|partner-worker`）与 `/app/bin/conversion-ops`（运维/接管 CLI）。三个核心生产实例按职责拆分进程运行（当前统一使用 `star_novel` RDS 账号）：

1. **准备三份独立环境配置文件**：
   - 生成共享 Token 与凭据加密主密钥：
     ```bash
     # 1. 生成 CMS 与 conversion-api 共享 Bearer Token
     openssl rand -hex 32

     # 2. 生成 32 字节 Base64 AES-256-GCM 主密钥 (用于加密 Pixel Access Token)
     openssl rand -base64 32
     ```
   - `conversion-api.env`（私网控制面：配置增改、密钥写入、只读审计、逐单对账与审计重排）：
     ```ini
     DATABASE_URL=postgres://star_novel:YOUR_RDS_PASSWORD@pgm-xxxxxx.pgsql.rds.aliyuncs.com:3433/star_novel?sslmode=disable
     CONVERSION_ENVIRONMENT=production
     CONVERSION_PRIVATE_ADDR=:8092
     CONVERSION_PRIVATE_TOKEN=<与 cms-backend 一致的共享 Token>
     # 新增或轮换 Pixel Token 时必填；版本号须存在于 CONVERSION_CREDENTIAL_KEYS 中
     CONVERSION_CREDENTIAL_KEY_VERSION=v1
     CONVERSION_CREDENTIAL_KEYS={"v1":"<Base64 32-byte AES Key>"}
     ```
   - `conversion-worker.env`（异步投递面：Outbox 归一化 + Meta Graph API 发送）：
     ```ini
     DATABASE_URL=postgres://star_novel:YOUR_RDS_PASSWORD@pgm-xxxxxx.pgsql.rds.aliyuncs.com:3433/star_novel?sslmode=disable
     CONVERSION_ENVIRONMENT=production
     # 初次切换上线先设 false 验证入队与心跳，确认无误后再改为 true 放量
     CONVERSION_META_ENABLED=false
     META_GRAPH_VERSION=v21.0
     # 须保留所有仍在使用的历史及当前密钥版本，确保平滑轮换期间可解密旧目标凭据
     CONVERSION_CREDENTIAL_KEYS={"v1":"<Base64 32-byte AES Key>"}
     ```
   - `conversion-collector.env`（独立监控面：每 30s 采集心跳、积压、缺目标与告警）：
     ```ini
     DATABASE_URL=postgres://star_novel:YOUR_RDS_PASSWORD@pgm-xxxxxx.pgsql.rds.aliyuncs.com:3433/star_novel?sslmode=disable
     CONVERSION_ENVIRONMENT=production
     ```

2. **构建镜像并启动三模式容器**（**严禁对公网映射 `8092` 端口**）：
   ```bash
   docker network create "$STAR_NOVEL_PRIVATE_NETWORK" 2>/dev/null || true
   docker build -t star-conversion:release conversion-backend

   for name in conversion-api conversion-worker conversion-collector; do
     docker stop "$name" 2>/dev/null || true
     docker rm "$name" 2>/dev/null || true
   done

   docker run -d --restart unless-stopped --name conversion-api \
     --network "$STAR_NOVEL_PRIVATE_NETWORK" \
     --env-file conversion-api.env star-conversion:release --mode=api

   docker run -d --restart unless-stopped --name conversion-worker \
     --network "$STAR_NOVEL_PRIVATE_NETWORK" \
     --env-file conversion-worker.env star-conversion:release --mode=worker

   docker run -d --restart unless-stopped --name conversion-collector \
     --network "$STAR_NOVEL_PRIVATE_NETWORK" \
     --env-file conversion-collector.env star-conversion:release --mode=collector
   ```
   > 如 `cms-backend` 运行于宿主机网络而非 Docker 自定义网络，可为 `conversion-api` 额外添加 `-p 127.0.0.1:8092:8092`，并将 `cms-backend/.env` 的 `CONVERSION_PRIVATE_URL` 指向 `http://127.0.0.1:8092`。

3. **使用 `conversion-ops` 运维 CLI**：
   ```bash
   # 示例：缺行订单对账补建试运行（使用独立维护/retention 凭据环境文件）
   docker run --rm --entrypoint /app/bin/conversion-ops --env-file conversion-ops.env \
     star-conversion:release repair-missing --order-id 12345 --event-name Purchase
   ```

---

## 🌐 六、 第四阶段：前端构建与宝塔 Nginx 统一网关配置

在网关机（`ECS-Gateway`）上操作：

### 6.1 前端静态产物编译
```bash
# 1. 编译读者端 H5 前端
cd reader-front && npm install && npm run build

# 2. 编译 CMS 运营后台前端
cd ../cms-front && npm install && npm run build
```

### 6.2 宝塔面板站点创建
1. **读者端 H5 站点**：
   - 宝塔【网站】 $\rightarrow$ 【添加站点】 $\rightarrow$ 域名：`h5.star-novel.com` 与 `*.star-novel.com`；
   - 根目录：`/www/wwwroot/h5.star-novel.com`，将 `reader-front/dist/` 下的文件上传到该目录。
2. **CMS 运营后台站点**：
   - 宝塔【网站】 $\rightarrow$ 【添加站点】 $\rightarrow$ 域名：`admin.star-novel.com`；
   - 根目录：`/www/wwwroot/admin.star-novel.com`，将 `cms-front/dist/` 下的文件上传到该目录。
   - **架构升级路由说明**：CMS 前端「广告投放」菜单已升级为 `/pixels`（像素管理）和 `/conversions`（回传中心），同时旧链接 `/ad-config/logs` 在前端保留携带 Query 参数自动重定向至 `/conversions`，依赖 Nginx `try_files $uri $uri/ /index.html;` 防止刷新 404。所有转化查询与操作请求均通过 `/api/v1/admin/conversion/*` 经由 `cms-backend` 私网转发，**严禁在 Nginx 中直接暴露 `conversion-api (:8092)`**；仅未来正式上线外部分销 Webhook 时，才另行将对应 `/callbacks/<partner>/...` 白名单路径反代至 `conversion --mode=callback`。

### 6.3 宝塔 Nginx 网关完整配置文件

#### 1. 读者端 H5 站点配置 (`h5.star-novel.com`)
```nginx
# 1. 定义后端多实例负载均衡集群
upstream reader_backend_cluster {
    least_conn; # 最少连接数均衡算法

    # 填写所有 reader-backend 节点的 VPC 内网 IP
    server 172.16.0.11:8080 max_fails=3 fail_timeout=10s weight=1;
    server 172.16.0.12:8080 max_fails=3 fail_timeout=10s weight=1;

    keepalive 64; # HTTP 长连接复用
}

server {
    listen 80;
    listen 443 ssl http2;
    server_name h5.star-novel.com *.star-novel.com;

    root /www/wwwroot/h5.star-novel.com;
    index index.html;

    # 1. SPA 单页路由回退
    location / {
        try_files $uri $uri/ /index.html;
    }

    # 2. index.html 禁用强缓存（保证新发布即时生效）
    location = /index.html {
        add_header Cache-Control "no-cache, no-store, must-revalidate";
    }

    # 3. 静态资源长期强缓存加速
    location ~* \.(js|css|png|jpg|jpeg|gif|ico|svg|woff2)$ {
        expires 30d;
        add_header Cache-Control "public, no-transform";
    }

    # 4. 动态 API 路由反代至集群
    location /api/v1/ {
        proxy_pass http://reader_backend_cluster/api/v1/;
        proxy_http_version 1.1;

        proxy_set_header Connection "";
        proxy_set_header Host $host;
        
        # 客户端真实 IP 透传 (CAPI 归因与游客注册核心字段)
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;

        # 故障自动转移（单实例宕机自动重试下一节点）
        proxy_next_upstream error timeout invalid_header http_500 http_502 http_503 http_504;
        proxy_next_upstream_tries 3;
        proxy_next_upstream_timeout 5s;

        proxy_connect_timeout 3s;
        proxy_read_timeout 60s;
        proxy_send_timeout 60s;
    }
}
```

#### 2. CMS 运营后台配置 (`admin.star-novel.com`)
```nginx
server {
    listen 80;
    listen 443 ssl http2;
    server_name admin.star-novel.com;

    root /www/wwwroot/admin.star-novel.com;
    index index.html;

    location / {
        try_files $uri $uri/ /index.html;
    }

    location = /index.html {
        add_header Cache-Control "no-cache, no-store, must-revalidate";
    }

    # CMS API 单实例反代
    location /api/v1/admin/ {
        proxy_pass http://127.0.0.1:8081/api/v1/admin/;
        proxy_http_version 1.1;

        proxy_set_header Connection "";
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

### 6.4 SSL 证书与强制 HTTPS
- 在宝塔面板对应站点进入 【SSL】 标签页；
- 选择 Let's Encrypt 申请证书；
- 勾选右上角 **【强制 HTTPS】** 保存生效。

---

## ✅ 七、 第五阶段：全链路联调与上线验收清单

| 验收维度 | 测试操作 | 预期结果 |
| :--- | :--- | :--- |
| **1. 读者端游客免密鉴权** | 访问 H5 首页 `https://h5.starsfic.com` | 自动发起 `/auth/guest` 请求，成功生成游客 User，下发 JWT 与迎新赠币 |
| **2. 负载均衡与健康探测** | 手动停止其中一台 `reader-backend` 节点 | 刷新读者端页面无任何报错，网关自动将请求无缝转发到存活节点 |
| **3. 行锁扣费与章节解锁** | 连续快速点击“解锁付费章节” | 数据库 `wallets` 余额正常扣减，无负余额，无并发双扣，流水记录完整 |
| **4. 每日签到防刷** | 模拟并发请求 `/wallet/checkin` 接口 | 首次签到成功，后续并发请求被 Redis 分布式锁与偏唯一索引拦截并提示已签到 |
| **5. 像素管理与配置审计 (`/pixels`)** | 打开 `/pixels` 新增/编辑测试 Pixel（带 Access Token）、尝试退役绑定了推广链的 Pixel、在接入配置编辑三态路由授权矩阵并查看抽屉「变更审计记录」 | 凭据加密保存（返回无明文）；占用中的 Pixel 退役按钮置灰阻断；矩阵仅提交变更 Diff；变更审计时间线正常加载且无密文泄漏 |
| **6. 回传中心与监控降级 (`/conversions`)** | 打开 `/conversions`，测试事件列表筛选、详情抽屉时间线与审计重排；使用「逐单对账」输入测试订单；暂停/恢复 `conversion-worker` 检查「监控与告警」；用 `SuperAdmin` 检查「回传日志」详情抽屉；短暂停掉 `conversion-api` 测试降级与切换查看历史日志 | Outbox → 统一事件 → Meta 投递全链路贯通且真实 `fbtrace_id` 可查；停 Worker 触发告警、恢复后自动解除；`MediaBuyer` 无「回传日志」Tab 与写权限；`conversion-api` 离线时 UI 给出 503 降级提示且 H5 下单写入 Outbox 不受阻 |
| **7. CMS 基础运营与权限** | 使用初始化/部署 `SuperAdmin` 账号登录 `https://admin.star-novel.com` | 仪表盘加载正常，小说章节拆分导入、6卡位充值模板修改即时生效 |

---

## 🔄 八、 第六阶段：日常运维与平滑滚动更新指南

### 8.1 前端热更新（零停机）
```bash
# 1. 本地执行编译
npm run build

# 2. 将 dist 目录内容上传覆盖网关机的 /www/wwwroot/h5.star-novel.com/
# 因为 index.html 配置了 no-cache，线上读者刷新浏览器即可加载最新版本
```

### 8.2 后端服务滚动升级与发版顺序（Zero Downtime）

> [!IMPORTANT]
> **升级发版顺序**：涉及数据结构与转化架构变更时，严格遵循 **`Schema 迁移 + 索引授权` $\rightarrow$ `conversion-api` / `conversion-worker` / `conversion-collector` $\rightarrow$ `cms-backend` & `reader-backend` $\rightarrow$ `cms-front` / `reader-front`** 顺序滚动发布。

#### 步骤 0：在本地 Mac 执行交叉编译打包并推送 Git
```bash
# 1. 在本地 Mac 项目根目录验证 Config 与生成后端 Linux 发布包（对应 cms + reader）
./deploy/scripts/build-linux.sh amd64

# 2. 若本次同时更新了 conversion-backend，请同步在构建机/本地打包 star-conversion 镜像
docker build -t star-conversion:release conversion-backend

# 3. 推送已提交代码到远端仓库（注：deploy/ 目录含生产配置，已 .gitignore 忽略，仅走私有安全传输）
git push origin <your-release-branch>
```

#### 步骤 1：登录服务器逐台滚动更新
```bash
# 1. 登录 ECS-Reader-1
cd /path/to/star-novel/reader-backend && git pull origin main

# 2. 构建镜像 (基于预编译二进制，仅耗时 1 秒，0 CPU 开销)
docker build -t star-reader-backend:latest .

# 3. 优雅重启容器
docker stop star_reader_backend 2>/dev/null || true
docker rm star_reader_backend 2>/dev/null || true
docker run -d \
  --name star_reader_backend \
  --restart always \
  -p 8080:8080 \
  --env-file .env \
  star-reader-backend:latest

# 4. 验证健康检查通过 (curl 127.0.0.1:8080/healthz)

# 5. 登录 ECS-Reader-2 重复上述步骤
```
*在更新节点 1 时，网关 Nginx 会自动将所有流量平滑导向节点 2，整个发布过程读者完全无感知。若采用 `deploy/linux/compose.yaml` 整套发布，请按 [deploy/linux/README.md §3](deploy/linux/README.md) 执行 `prepare-config.py` 与 `docker compose up -d --wait`，并单独更新 `conversion-api` / `conversion-worker` / `conversion-collector`。*

### 8.3 转化加密密钥平滑轮换与应急命令
1. **Pixel 凭据密钥平滑轮换（零中断）**：
   - **Step 1**：生成新密钥 `v2`（`openssl rand -base64 32`），在 `conversion-worker.env` 中同时保留旧新密钥：`CONVERSION_CREDENTIAL_KEYS={"v1":"...","v2":"..."}`，滚动重启 `conversion-worker`（确保其能读取新旧密文）。
   - **Step 2**：更新 `conversion-api.env` 中的 `CONVERSION_CREDENTIAL_KEYS={"v1":"...","v2":"..."}` 并将 `CONVERSION_CREDENTIAL_KEY_VERSION=v2`，重启 `conversion-api`。
   - **Step 3**：在 CMS「像素管理」逐个轮换 Pixel Access Token 后写入 `v2` 密文；确认数据库 `conversion.credentials` 中已无 `key_version='v1'` 的活动/待投递引用后，再将 `v1` 从配置移除。
2. **数据保留期脱敏清理**：可通过 `conversion-ops retention --keep-days 30 --limit 100 --apply --operator <admin_id> --reason "<reason>"` 分批清理超保留期的敏感快照字段（保留逻辑键、投递终态与审计记录），直至每批处理返回 `0`。

---
*文档版本：v2.1.0（2026-11-20 更新：统一使用 `star_novel` RDS 账号，精简数据库备份步骤） | 维护团队：STAR NOVEL 架构组*
