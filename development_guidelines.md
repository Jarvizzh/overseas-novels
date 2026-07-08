# Star Novel Go 后台开发分层与高并发高可用开发规范

为了保障后续业务扩展时代码的健壮性、可测试性，以及系统的**高并发**、**高可用**能力，特制定本开发规范。全体开发人员在后续的代码编写中必须严格遵守。

---

## 1. 三层架构开发规范

系统采用经典的 `Handler -> Service -> Repository` 三层架构模型，面向接口编程。

```
├── cmd/
│   └── server/main.go        # 依赖注入与服务启动入口
├── internal/
│   ├── auth/                 # 业务模块包名
│   │   ├── handler.go        # Handler 层
│   │   ├── service.go        # Service 层
│   │   ├── repository.go     # Repository 层
│   │   └── model.go          # 模块级实体对象（或在全局 model 包中）
```

### 1.1 Handler 层 (HTTP / 演示层)
- **职责**：
  1. 路由注册与请求中间件拦截（如 Auth，CORS）。
  2. 获取并校验 HTTP 报文：使用 Gin 的 `ShouldBindJSON`、`ShouldBindQuery` 等，结合 tag（如 `binding:"required,email"`）进行强类型校验。
  3. 获取 HTTP 请求的上下文元数据（如 `user_id`、IP、User-Agent、Headers），并作为参数传递给 Service 方法。
  4. 调用 Service 层的接口方法，根据返回结果及 Error 映射为 HTTP 状态码并返回标准的 JSON 结构。
- **禁忌**：
  - **严禁**在此层包含任何 SQL 语句、数据库连接 (`db.DB`) 或 Redis 直接调用。
  - **严禁**在此层执行数据库事务操作。
  - **严禁**将数据库内部错误直接返回给前端（例如 `pgx.ErrNoRows` 等敏感信息）。

### 1.2 Service 层 (业务逻辑层)
- **职责**：
  1. 实现具体业务逻辑的核心流程控制。
  2. 控制数据库事务（Transaction） of the DB connection. 开启（`Begin`）、提交（`Commit`）、回滚（`Rollback`）均需在此层操作。
  3. 聚合及调度多个 Repository、缓存机制和第三方客户端组件（如 Stripe, PayPal, Facebook API）。
- **禁忌**：
  - **严禁**在此层引入 HTTP 传输协议依赖（禁止导入 `github.com/gin-gonic/gin` 或直接接收 `*gin.Context`）。
  - 所有方法必须接收 `context.Context` 作为第一个参数，保证超时与链路追踪信息得以流转。
  - **严禁**在事务包裹的代码块中加入阻塞的网络 IO 操作（如调用第三方支付或推送 API），防止数据库连接被长期占用导致连接池耗尽。

### 1.3 Repository 层 (数据访问层)
- **职责**：
  1. 封装对关系数据库（PostgreSQL/pgx）和缓存（Redis）的直接交互逻辑。
  2. 提供纯粹的 CRUD 存取，将数据库行记录 Scan 进 Go 结构体。
  3. Repository 方法不主动管理事务，而是通过接收 Service 层传入的事务对象 `tx pgx.Tx` 参与到全局事务中（方法命名通常以 `Tx` 结尾）。
- **禁忌**：
  - **严禁**在此层处理核心业务逻辑的正确性（如余额是否足够等，这些校验应由 Service 处理）。

---

## 2. 高并发开发规范

为了支撑高并发流量，系统应遵循以下缓存、加锁和异步化准则：

### 2.1 缓存设计规范 (Cache Aside)
- **防缓存穿透**：当查询 DB 返回“数据不存在”时，依然要在 Redis 中缓存一个空值（如 `nil` 或占位字符串，并设置极短的过期时间，例如 30 秒 - 5 分钟），防止恶意的无效请求穿透到 DB。
- **防缓存击穿（热点 Key 失效）**：对于高频访问的 Key（如热门小说首页、排行版配置），当缓存失效时，必须使用**互斥锁（SingleFlight）**或 **Redis 分布式锁**控制只有一个协程去穿透查 DB 并回填缓存，其余协程等待并重新读取缓存。
- **防缓存雪崩**：缓存的过期时间（TTL）严禁设置成固定的值，必须加上一个随机偏差（如 10% - 20% 的随机时间抖动），分散缓存失效点，避免数据库在某一时刻瞬时压力暴增。

### 2.2 并发冲突与防刷控制
- **悲观锁 / 行级锁**：涉及敏感资产扣减（如章节解锁扣金币、钱包扣费），必须使用关系数据库的排他锁 `SELECT ... FOR UPDATE`。先加锁查询最新余额，在事务内计算并更新扣减，保证绝对的原子性。
- **分布式锁**：高并发的签到发放（`DailyCheckIn`）等可能导致并发重复刷奖励的接口，应使用 Redis 分布式锁（基于 `SetNX` 实现具有租约期限的锁机制），并在数据库设计上对 `(user_id, biz_type, date)` 加唯一联合索引作为最终一致性防线。

### 2.3 异步解耦与 Goroutines 控制
- **禁止无节制开启裸 Goroutine**：在高并发下，直接执行 `go func()` 会导致短时间内产生海量协程，可能耗尽系统内存甚至使服务 OOM 崩溃。
- **协程池机制**：非实时交互的长耗时任务（如 Facebook 埋点事件上报，大批量数据汇总），应将其发布至带缓冲区（Buffered Channel）的异步队列，由固定数量 of Workers 协程并发消费，并在发送时配置合理的 Timeout（如 3 - 5 秒）。

---

## 3. 高可用开发规范

高可用旨在保证系统面对故障、网络超时时依然能正常提供核心服务：

### 3.1 长/短事务规范
- **短事务原则**：数据库事务持有的连接资源极度昂贵，事务代码块必须尽可能精炼和快速。
- **事务内无网络 IO**：
  ```go
  // ❌ 错误示范：事务内进行 HTTP 请求，如果 Stripe 响应慢，连接会被一直挂起
  tx, _ := repo.BeginTx(ctx)
  _ = repo.CreateOrderTx(tx, order)
  _ = stripeClient.Verify(...) // 网络 IO 耗时 5 秒！
  _ = repo.UpdateWalletTx(tx, user)
  tx.Commit()

  //  正确示范：事务内外分离
  result, err := stripeClient.Verify(...) // 在事务外处理网络交互
  if err != nil { return err }

  tx, _ := repo.BeginTx(ctx) // 只在 DB 更新时开启事务
  _ = repo.CreateOrderTx(tx, order)
  _ = repo.UpdateWalletTx(tx, user)
  tx.Commit()
  ```

### 3.2 错误处理与防御性设计
- **屏蔽数据库报错**：Repository 层的报错（如包含 SQL 关键字的数据库错误）应只记录在日志系统中，严禁将其直接通过 HTTP 响应抛给用户。
- **定义领域错误（Domain Errors）**：Service 层与 Repository 层应将错误转化为业务定义好的强类型常数错误，例如 `ErrInsufficientBalance`（余额不足）、`ErrAlreadyUnlocked`（章节已解锁）。Handler 捕获到这些强类型错误后，再翻译为标准的 4xx 或 5xx HTTP 响应。
- **外部 API 的超时与熔断**：调用 PayPal, Stripe, Facebook 等海外接口必须配置严格的上下文超时限制（`context.WithTimeout`）。在网络抖动或服务故障时，通过降级（如 mock 默认值、稍后重试等）保证系统主体路由的可达性。

### 3.3 优雅停机 (Graceful Shutdown)
- 系统主入口中必须通过监听信号量（`syscall.SIGINT`, `syscall.SIGTERM`）来控制服务的平滑退出：
  1. 接收到退出信号时，首先关闭 HTTP Listener 不再接收新的流量。
  2. 预留一段缓冲时间（如 5 - 10 秒），等待内存中正在处理的请求和数据库事务正常处理完毕。
  3. 优雅释放数据库连接池 (`db.CloseDB()`) 和缓存客户端连接，避免数据库事务断连回滚。
