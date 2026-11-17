# STAR NOVEL 生产上线部署标准化操作指南 (SOP)

本文档面向 **STAR NOVEL（海外小说平台与运营管理系统）** 运维与研发团队，详细定义了 **「宝塔 Nginx 统一网关 (H5 + CMS 静态托管) + 读者 API 多 ECS 负载均衡集群 + CMS 运营单实例 + 阿里云 RDS PostgreSQL + 自建 Redis」** 生产部署架构的全流程落地规范与维护手册。

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
        CDN["⚡ Cloudflare 全球 CDN<br/>(静态资源 Edge 缓存 / SSL 卸载 / DDoS 防护)"]:::cdnStyle
        
        ReaderUser -->|"h5.star-novel.com<br/>*.star-novel.com"| CDN
        AdminUser -->|"admin.star-novel.com"| CDN
    end

    %% 2. 宝塔 Nginx 统一网关与前端机
    subgraph Layer2 ["【网关机 ECS-Gateway (宝塔面板)】"]
        BTNginx["🌐 宝塔 Nginx 统一网关 (:80 / :443)<br/>• 证书管理 & 强制 HTTPS<br/>• SPA 路由回退 (防刷新 404)<br/>• 真实客户端 IP 透传 (X-Real-IP)"]:::gatewayStyle
        
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
        
        ReaderNode1["🚀 ECS-Reader-1<br/>reader-backend (172.16.0.11:8080)"]:::backendStyle
        ReaderNode2["🚀 ECS-Reader-2<br/>reader-backend (172.16.0.12:8080)"]:::backendStyle
        ReaderNodeN["🚀 ECS-Reader-N (弹性扩容)<br/>reader-backend (172.16.0.1X:8080)"]:::backendStyle
    end

    BTNginx -->|"/api/v1/* (读者API)"| UpstreamRoute
    UpstreamRoute -->|"内网分发"| ReaderNode1
    UpstreamRoute -->|"内网分发"| ReaderNode2
    UpstreamRoute -->|"内网分发"| ReaderNodeN

    %% 4. 集中数据与持久化层
    subgraph Layer4 ["【阿里云 VPC 专有网络 - 集中数据层】"]
        RDS[("🗄️ 阿里云 RDS PostgreSQL 18<br/>(pgm-xxxx.pgsql.rds.aliyuncs.com)<br/>• 钱包行锁 (FOR UPDATE)<br/>• 模糊搜索 (pg_trgm GIN 索引)<br/>• 签到防刷 (偏唯一索引)<br/>• 广告归因 (JSONB)")]:::dataStyle
        
        Redis[("⚡ 自建 Redis 7 缓存<br/>(172.16.0.15:6379)<br/>• 0~20% Jitter 缓存防雪崩<br/>• SingleFlight 互斥回填<br/>• 签到分布式锁 & AOF 持久化")]:::dataStyle
    end

    ReaderNode1 --> RDS & Redis
    ReaderNode2 --> RDS & Redis
    ReaderNodeN --> RDS & Redis
    CMSNode --> RDS & Redis

    %% 5. 海外第三方对接
    subgraph Layer5 ["【海外第三方生态】"]
        StripePay["💳 Stripe / PayPal 支付网关"]:::extStyle
        FBCAPI["📊 Facebook CAPI (转化事件异步回传)"]:::extStyle
        MetaAPI["📈 Meta Marketing API (广告消耗/ROAS)"]:::extStyle
    end

    ReaderNode1 -.-> StripePay & FBCAPI
    CMSNode -.-> MetaAPI
```

---

## 📋 二、 服务器规划与网络矩阵

| 机器/服务角色 | 部署组件与职责 | 监听端口 / 访问模式 | 推荐硬件配置 |
| :--- | :--- | :--- | :--- |
| **网关与管理机 (`ECS-Gateway`)** | 宝塔面板 + Nginx (统一网关) + `reader-front` 静态 + `cms-front` 静态 + `cms-backend` Docker 容器 | `80`, `443` (公网开放)<br/>`8081` (本机内网) | 2核 4G / 40G 高效云盘 |
| **读者端集群节点 1 (`ECS-Reader-1`)** | `reader-backend` Go API Docker 容器 | `8080` (仅 VPC 内网 `172.16.0.11`) | 2核 4G 或 4核 8G |
| **读者端集群节点 2 (`ECS-Reader-2`)** | `reader-backend` Go API Docker 容器 | `8080` (仅 VPC 内网 `172.16.0.12`) | 2核 4G 或 4核 8G |
| **自建 Redis 节点 (`ECS-Redis`)** | Redis 7 服务（强密码认证 + AOF 持久化） | `6379` (仅 VPC 内网 `172.16.0.15`) | 2核 4G / 内存型 |
| **阿里云 RDS PostgreSQL** | 主业务数据库（自带 `pg_trgm` 扩展） | `3433` 或 `5432` (VPC 专网连接) | 2核 4G 基础版 / 高可用版 |

> [!IMPORTANT]
> **网络规划原则**：
> 1. 所有 ECS、Redis 与 RDS 必须处于 **同一个阿里云专有网络（VPC）**；
> 2. 读者端 Backend 节点与 Redis 节点 **严禁在安全组中对公网开放 8080 与 6379 端口**，仅允许网关机与集群内网 IP 互通。

---

## 🗄️ 三、 第一阶段：数据基础设施初始化 (RDS & Redis)

### 3.1 阿里云 RDS PostgreSQL 配置
1. **白名单配置**：在 RDS 控制台 【白名单与安全组】 中，将 VPC 内网网段（如 `172.16.0.0/24`）加入白名单；
2. **账号与数据库创建**：创建高权限账号 `star_admin`，创建数据库 `star_novel`，字符集选择 `UTF8`；
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

# 数据库与缓存内网连接
DATABASE_URL=postgres://star_admin:YOUR_RDS_PASSWORD@pgm-xxxxxx.pgsql.rds.aliyuncs.com:3433/star_novel?sslmode=disable
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

## 🛠️ 五、 第三阶段：CMS 运营后端单实例部署

在网关/管理机（`ECS-Gateway`）上操作：

### 5.1 准备 `cms-backend/Dockerfile` (基于预编译 Linux 二进制)
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
DATABASE_URL=postgres://star_admin:YOUR_RDS_PASSWORD@pgm-xxxxxx.pgsql.rds.aliyuncs.com:3433/star_novel?sslmode=disable
REDIS_URL=redis://:YOUR_STRONG_REDIS_PASSWORD_2026@172.16.0.15:6379/0
CMS_JWT_SECRET=<独立于 Reader 的至少 32 字符随机密钥>
DEFAULT_DOMAIN=https://h5.starsfic.com

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

# 3. 启动新容器
docker run -d \
  --name star_cms_backend \
  --restart always \
  -p 127.0.0.1:8081:8081 \
  --env-file .env \
  star-cms-backend:latest

# 4. 验证健康检查
curl http://127.0.0.1:8081/healthz
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
| **5. 广告 CAPI 归因** | 查看 `facebook_capi_logs` 数据库表 | 成功记录回传事件，`payload` 中的 `client_ip_address` 为真实用户 IP 而非 127.0.0.1 |
| **6. CMS 管理端操作** | 登录 `https://admin.star-novel.com` (`admin` / `admin123`) | 仪表盘加载正常，小说章节拆分导入、6卡位充值模板修改即时生效 |

---

## 🔄 八、 第六阶段：日常运维与平滑滚动更新指南

### 8.1 前端热更新（零停机）
```bash
# 1. 本地执行编译
npm run build

# 2. 将 dist 目录内容上传覆盖网关机的 /www/wwwroot/h5.star-novel.com/
# 因为 index.html 配置了 no-cache，线上读者刷新浏览器即可加载最新版本
```

### 8.2 读者端 Go 后端滚动升级（Zero Downtime）

#### 步骤 0：在本地 Mac 执行一键交叉编译并推送到 Git
```bash
# 在本地 Mac 项目根目录执行
./scripts/build-linux.sh

# 提交并推送到 GitHub 远端仓库
git add . && git commit -m "feat: 发布新版本二进制" && git push origin main
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
*在更新节点 1 时，网关 Nginx 会自动将所有流量平滑导向节点 2，整个发布过程读者完全无感知。*

### 8.3 数据库每日自动备份
在网关机或任意 ECS 的 Crontab 中添加自动备份任务：
```bash
0 3 * * * pg_dump -h <RDS内网地址> -p 3433 -U star_admin star_novel | gzip > /data/backup/star_novel_$(date +\%Y\%m\%d).sql.gz
find /data/backup/ -name "star_novel_*.sql.gz" -mtime +7 -exec rm -f {} \;
```

---
*文档版本：v1.2.0 | 维护团队：STAR NOVEL 架构组*
