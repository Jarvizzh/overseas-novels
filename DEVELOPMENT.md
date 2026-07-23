# Star Novel - 前后端开发与集成记录 (Project Development Log)

本项目包含一个基于 **Go** 语言开发的高性能 Web 读者端后端服务与 **CMS 管理端后端**，以及基于 **React + Vite** 的移动端小说阅读 H5 前端与 **CMS 运营管理面板**。前后端已全量对接，支持完整的推广引流、像素追踪、多套模板代币划扣及完备的用户指标统计体系。

---

## 🛠️ 项目架构与技术栈 (Architecture & Stack)

系统采用面向接口设计、低耦合的**三层分层架构模式**，为高并发与高可用而优化：
- **Handler 层 (HTTP / Presentation Layer)**：只负责参数解析、请求数据校验（使用 Gin binding tags）和包装 HTTP JSON 响应，阻断了数据库与 HTTP 状态的强耦合，没有包含任何业务逻辑与 SQL。
- **Service 层 (Business Logic Layer)**：承载核心业务规则（包含事务生命周期的开启、提交与回滚）、Facebook CAPI 转化事件异步分发，不依赖 HTTP 框架，参数为标准的 Go 原生结构体与 `context.Context`，保证了极高的可测试性。
- **Repository 层 (Data Access Layer)**：负责 pgx/PostgreSQL 底层 CRUD 的执行与 Redis 缓存的维护。

### 技术栈详情
* **前端 (H5 & CMS)**：React 19, Vite 8, TypeScript 6, Vanilla CSS (微秒级动效与极简毛玻璃卡片风格)。
* **后端 (Go REST API)**：Go 1.25, Gin, pgxpool (高性能 PostgreSQL 驱动), go-redis/v9 (缓存与高并发 ACL)。
* **基础设施**：PostgreSQL 15, Redis 7, Docker Compose。
* **开发分层规范**：详细的分层职责定义、高并发防刷、缓存设计和高可用事务标准请参见工程根目录下的 [development_guidelines.md](file:///Users/jarvizzhang/IdeaProjects/star-novel/development_guidelines.md)。

---

## 📁 目录结构 (Project Directory Structure)

```text
star-novel/
├── backend/                   # Go 读者端后端工程 (端口 8080)
│   ├── cmd/server/main.go     # 主入口与服务实例化、依赖注入装配
│   ├── db/
│   │   ├── schema.sql         # 数据库全量表定义与重置 SQL (包含核心表)
│   │   └── seed.sql           # 高保真小说内容与内置配置数据种子
│   └── internal/              # 读者端业务层 (全部实现规范三层分层)
│       ├── auth/              # 用户鉴权 (handler.go, service.go, repository.go)
│       ├── config/            # 配置中心
│       ├── db/                # pgx 数据库连接池
│       ├── model/             # 读者端领域结构体 models.go
│       ├── novel/             # 小说与阅读器接口 (handler.go, service.go, repository.go, cache.go)
│       ├── payment/           # Stripe / PayPal 支付网关客户端
│       ├── redis/             # Redis 连接实例
│       ├── shelf/             # 书架与阅读进度 (handler.go, service.go, repository.go)
│       ├── tracking/          # Facebook Conversions API 转化追踪器
│       └── wallet/            # 钱包与充值结算 (handler.go, service.go, repository.go)
├── cms-backend/               # Go CMS 后端服务 (端口 8081)
│   ├── main.go                # CMS 入口，路由分发与服务实例化装配
│   └── internal/              # 运营后台逻辑层 (全部实现规范三层分层)
│       ├── auth/              # 管理员账户与权限控制 (handler.go, service.go, repository.go)
│       ├── billing/           # 财务订单、退款及充值模板配置 (model.go, handler.go, service.go, repository.go)
│       ├── config/            # 配置读取
│       ├── db/                # 数据库连接
│       ├── redis/             # Redis 连接
│       ├── novel/             # 小说导入、上下架、系统配置与推广像素 (model.go, handler.go, service.go, repository.go)
│       ├── user/              # 用户指标明细与钱包手动调整 (model.go, handler.go, service.go, repository.go)
│       └── tracking/          # 事件追踪辅助
├── front/                     # React 读者端 H5 前端 (端口 5173)
│   └── src/                   # 读者页面、设置、API 网络层 (自适应拦截 UTM 及 FB 追踪)
├── cms-front/                 # React 管理端前端 SPA (端口 5174)
│   └── src/                   # 运营仪表盘、广告像素配置、用户指标列表、Portal 磨砂提示窗
├── development_guidelines.md  # 本项目 Go 分层与高并发高可用开发规范
└── DEVELOPMENT.md             # 本开发记录文档
```

---

## 🌟 已实现的核心阶段功能 (Implemented Features)

### 阶段 1：项目分层重构与依赖注入 (Dependency Injection)
* **标准三层骨架重构**：重构了前台后台与 CMS 后台的全部业务代码，业务流程、底层 SQL 与 HTTP 控制层彻底解耦，在服务启动时在 `main.go` 进行构造注入装配。
- **高并发与强事务把控**：
  * 将 `BeginTx` 控制权上移至 Service 层，通过事务保障余额退款、游客注册等多表交互原子性；在 Repository 中支持无状态事务对象 `pgx.Tx` 传入。
  * 将长耗时网络请求（如 Stripe/PayPal SDK 支付校验，Facebook CAPI 推送）从事务锁代码块中拆离，彻底消除长事务挂死数据库连接池的隐患。

### 阶段 2：用户与 JWT 双模鉴权 (Authentication)
* **免注册游客免密登录 (`POST /auth/guest`)**：为设备分发自增 ID 账号，并发放 200Coins 迎新赠送代币，提供“即开即读”的极速转化体验。
* **注册与登录 (`POST /auth/register`, `/auth/login`)**：使用 `bcrypt` 算法对密码进行安全加盐哈希存储。
* **双模拦截器**：
  * `AuthMiddleware` 保护书架、充值等隐私数据，要求持有合法 JWT Token。
  * `OptionalAuthMiddleware` 可选鉴权小说阅读页，登录时自动对账以查询章节解锁，游客自动放行免费章节。

### 阶段 3：高并发多级缓存与防爬收费墙 (Caching & Security)
* **Cache-Aside 缓存模型**：小说详情（缓存 6h）、章节目录（缓存 6h）及正文（缓存 2h）全部在 Redis 进行多级缓存，防止瞬时并发涌入直接击垮 DB。
* **微秒级购买鉴权 (Redis Set)**：已购章节保存在 Redis 内存 Set 中（Key 为 `user:unlocks:{user_id}:{novel_id}`），查询时通过 `SISMEMBER` 进行微秒级极速校验，规避频繁的 DB IO 操作。
* **安全收费墙拦截**：小说 1、2 章免费，第 3 章起收费。若未解锁，API 强行拦截仅返回首部 2 个段落并在 JSON 响应中标记 `locked: true`，防爬虫整本防盗。

### 阶段 4：云同步书架与多端进度冲突解决 (Cloud Sync)
* **书架逻辑软删除**：用户移出书架时，仅标记 `in_shelf = false`，永久保留该书的历史阅读进度，重新加回时自动恢复进度。
* **防冲突时间戳对账算法 (`POST /shelf/sync`)**：客户端定时或退出阅读器时批量上报进度。后端通过比对时间戳，**仅在客户端进度新于数据库时执行更新**，规避多设备同步时的进度覆盖冲突。
* **前台 2 秒防抖队列**：前端采用防抖队列同步进度频度，减少高密度的网络开销。

### 阶段 5：行锁钱包账户与充值渠道集成 (Wallet & Payments)
* **行级排他锁并发控制**：在进行金币扣减（如章节解锁）时，使用 Postgres `FOR UPDATE` 行锁开启事务，避免多设备并发解锁造成的余额穿透与重复扣费。
* **双币优先抵扣**：系统优先扣减有时效性的赠送金币（`bonus_coins`），不足部分再从充值币（`charged_coins`）扣减。
* **幂等签到领币**：每日签到接口在 Service 事务中，通过底层 `HasCheckedInToday` 方法原子性查询本日重复流水，严格防止高并发多发刷币。
* **双渠道集成**：Stripe 准备 Intent 绑卡交易；PayPal 执行 Order Capture 完成防篡改充值。

### 阶段 6：模版充值与动态计费体系 (Template & Dynamic Billing)
* **6 卡位模版充值管理**：支持单次充值、VIP 订阅、全本购买三种模式，后台支持模板新增、修改、设为默认配置。
* **动态小说计费机制**：废除章节金币手动录入，后台上传或导入章节时通过 Unicode Runes 自动计算去空白的有效字数，按配置公式（`price = ( RunesCount * cost_per_thousand ) / 1000`）实时动态计费。
* **降级兜底查询**：当小说没有配置定价属性时，级联降级为全局配置（`system_configs` 默认 5 金币/千字），并在改动发生时主动刷新并失效 Redis 缓存，保障一致性。

### 阶段 7：多广告像素绑定与 Facebook CAPI 集成 (Marketing Tracking)
* **引流链路捕获**：H5 读者端检测 URL 中的广告点击 `fbclid`, `utm_source`, `utm_campaign`, `pixel_id` 等并存储在客户端，使用 API 网络标头自动回传给后端。
* **Conversions API 异步派发**：后端收到请求头后，调用 Service 方法，并开启异步 Goroutines 完成与 Facebook 端的 `CompleteRegistration` 和 `Purchase` 转化事件对接。

### 阶段 8：财务指标看板与审计日志 (Analytics & Audit)
* **自增 BIGINT 用户 ID**：将数据库 `users` 表的主键全面重构为自增的 `BIGINT` 类型（`BIGSERIAL PRIMARY KEY`），在各关联业务表中统一格式，提升多表关联查询性能。
* **自增 BIGINT 书籍 ID 重构**：将小说唯一标识 `novels.id` 全面重构为自增整数类型（由自增序列控制，从 `10000001` 开始），并重构了所有的外键关联字段。移除了管理后台新建小说时手动输入自定义 ID 的表单框，实现全自动分配自增主键，保障高并发下的性能与唯一性。
* **财务及阅读指标自动对账**：利用子查询实时关联订单和流水，在用户列表中展示用户的**累计充值（金币/美金）、金币当前总余额、累计金币消费，以及最近一次同步进度所阅读保持的章节小说标题**，运营数据一目了然。
- **操作审计日志**：在涉及用户封禁、财务退款（`RefundOrder`）、系统强行调账时，在 Service 的事务块内同步记录 `admin_audit_logs` 日志，为运营安全保驾护航。

---

## 🏃 本地运行与调试命令 (Running locally)

### 1. 启动数据库与 Redis 容器
打开 Docker Desktop 并在后端工程目录下执行：
```bash
cd backend
docker-compose up -d
```

### 2. 数据库迁移与初始种子注入
如果需要干净的数据库数据重置，运行以下命令（直接通过管道输入 `psql` Stdin 避免 volume 缓存不一致）：
```bash
docker exec -i novel_postgres psql -U postgres -d novel_db < db/schema.sql
docker exec -i novel_postgres psql -U postgres -d novel_db < db/seed.sql
```

### 3. 运行 Go 后端服务

#### 运行读者端服务 (端口 8080)
```bash
cd backend
go run cmd/server/main.go
```

#### 运行 CMS 管理端服务 (端口 8081)
```bash
cd cms-backend
go run main.go
```

### 4. 运行前端前端服务

#### 运行读者端 H5 界面 (端口 5173)
```bash
cd front
npm run dev
```
* **访问链接**：`http://localhost:5173/`

#### 运行管理后台界面 (端口 5174)
```bash
cd cms-front
npm run dev
```
* **访问链接**：`http://localhost:5174/`
* **默认测试管理员**：在登录界面点击 "Seed Default Admin (admin/admin123)" 一键注册管理员，或自行在后台注册后，以用户名 `admin` / 密码 `password` (或 `admin123`) 登录系统管理用户档案。
