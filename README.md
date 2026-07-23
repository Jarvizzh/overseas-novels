# STAR NOVEL (星芒小说) - 海外小说平台与运营管理系统

<p client="center">
  <img src="cms-front/public/assets/logo_light.png" alt="STAR NOVEL Logo" width="120" />
</p>

**STAR NOVEL** 是一套专门面向海外小说市场的现代化高并发网络文学阅读平台与全功能运营管理系统（CMS）。

系统基于 **Go (Gin + pgxpool + Redis)** 开发高性能后端 RESTful API，基于 **React 19 + Vite 8 + TypeScript** 构建移动端 H5 读者前端与运营后台。涵盖海外小说内容上线、推广域名管理、多套充值代币划扣、Facebook CAPI 广告追踪、多像素绑定、行锁金币钱包及全链路用户指标数据分析。

---

## 🌟 核心特性 (Key Features)

### 📖 1. H5 读者端与云同步阅读器 (`front`)
* **即开即读与游客免密鉴权**：支持基于设备指纹的游客自动注册，发放迎新赠送金币，无缝过渡至完整账户。
* **高安全防爬收费墙**：小说 1~2 章免费阅读，第 3 章起自动开启收费墙，未解锁用户接口严格防截获。
* **云端阅读进度同步**：采用防冲突时间戳对账算法与客户端 2 秒防抖队列，确保多设备间同步不覆盖历史阅读进度。
* **多渠道支付**：集成 Stripe 绑卡交易与 PayPal 快捷结算。

### 📊 2. 全功能运营管理后台 (`cms-front` & `cms-backend`)
* **子域名管理 (Subdomain Management)**：支持主域名（Main）与引流子域名（Sub）的统一接入与状态调控，可一键切换默认 H5 入口。
* **深色/浅色模式无缝切换**：全系统变量式 UI 设计，支持右上角主题随时切换，配有专属同构 **双星 (Twin Stars)** 极简 LOGO。
* **按字数动态计费体系**：上传章节自动剔除空白字符计算 Unicode Runes 字数，按配置公式（`price = RunesCount * cost_per_thousand / 1000`）自动计算定价。
* **6 卡位充值模板**：灵活配置单次充值、VIP 订阅与全本解锁模板，支持首页推荐标签与默认选中设置。
* **广告像素与 Facebook CAPI**：支持 FB 像素绑定与多维度 Conversion API 异步事件分发，自动记录广告点击 `fbclid`, `utm_source` 效果。

### ⚡ 3. 高并发与高可用 Go 架构 (`backend` & `cms-backend`)
* **面向接口三层分层模式**：Handler - Service - Repository 彻底解耦，依赖注入在 `main.go` 统一装配。
* **PostgreSQL 行级排他锁 (`FOR UPDATE`)**：扣减金币、充值结算全程在数据库事务中施加行锁，彻底杜绝高并发并发解锁导致的余额穿透与重复扣费。
* **Cache-Aside Redis 多级缓存**：小说详情（缓存 6h）、章节正文（缓存 2h）、已购章节 Set 微秒级 `SISMEMBER` 极速鉴权。

---

## 🛠️ 技术栈 (Tech Stack)

| 领域 | 核心技术选型 | 说明 |
| :--- | :--- | :--- |
| **读者端前端** | React 19, Vite 8, React Router v6 | 移动端 H5 适配，微秒级动效 |
| **CMS 前端** | React 19, Vite 8, Lucide React, Custom Select | 响应式仪表盘、黑白双模式切换 |
| **读者端后端** | Go 1.25, Gin Framework, pgxpool, go-redis/v9 | 高并发 REST API (端口 `8080`) |
| **CMS 后端** | Go 1.25, Gin Framework, pgxpool, go-redis/v9 | 管理端 API (端口 `8081`) |
| **数据存储** | PostgreSQL 15, Redis 7 | Docker 容器化部署 |
| **支付与追踪** | Stripe SDK, PayPal REST API, Facebook CAPI | 海外合规支付网关与广告追踪 |

---

## 📁 目录结构 (Directory Structure)

```text
star-novel/
├── backend/                   # 读者端 Go 后端服务 (Port 8080)
│   ├── cmd/server/main.go     # 服务实例化与依赖注入
│   ├── db/
│   │   ├── schema.sql         # 全量数据库初始化脚本
│   │   └── seed.sql           # 高保真小说种子数据
│   └── internal/              # 读者端分层架构 (auth, novel, shelf, wallet, payment, tracking)
├── cms-backend/               # 运营后台 Go 后端服务 (Port 8081)
│   ├── main.go                # CMS 入口与路由配置
│   └── internal/              # CMS 业务分层 (auth, billing, domain, novel, user, tracking)
├── front/                     # 读者端 H5 移动前端 (Port 5173)
├── cms-front/                 # 运营后台 SPA 前端 (Port 5174)
│   └── public/assets/         # 品牌双星 LOGO 资源 (logo_dark.png, logo_light.png)
├── development_guidelines.md  # 后端开发分层与并发控制规范
└── DEVELOPMENT.md             # 详细开发历史与集成日志
```

---

## 🚀 快速启动指南 (Quick Start)

### 1. 环境准备 (Prerequisites)
* Go 1.25 或更高版本
* Node.js 18+ 与 npm
* Docker 与 Docker Compose

### 2. 启动基础设施 (Database & Redis)
进入 `backend/` 目录并使用 Docker 启动 PostgreSQL 和 Redis 服务：
```bash
cd backend
docker-compose up -d
```
* PostgreSQL 运行于 `localhost:5432`（数据库名: `star_novel`，用户名: `postgres`，密码: `postgres123`）
* Redis 运行于 `localhost:6379`

### 3. 启动后端 API 服务 (Backend Services)

#### 启动读者端后端服务 (Port 8080)：
```bash
cd backend
go run cmd/server/main.go
```

#### 启动 CMS 运营后台后端服务 (Port 8081)：
```bash
cd cms-backend
go run main.go
```

### 4. 启动前端 SPA 服务 (Frontend Apps)

#### 启动读者端 H5 (Port 5173)：
```bash
cd front
npm install
npm run dev -- --port 5173
```
浏览器访问: `http://localhost:5173/`

#### 启动 CMS 运营管理面板 (Port 5174)：
```bash
cd cms-front
npm install
npm run dev -- --port 5174
```
浏览器访问: `http://localhost:5174/`

---

## 🔐 默认管理员账号 (Default Credentials)

初始部署后，可在 CMS 登录界面点击**一键初始化**，或直接使用默认账号登录：
* **后台登录地址**：`http://localhost:5174/`
* **用户名**：`admin`
* **初始密码**：`admin123`

---

## 📖 补充文档 (Documentation)

* 详细的 Go 架构三层分层、防刷高并发与事物锁规范，请阅读 [development_guidelines.md](file:///Users/jarvizzhang/IdeaProjects/star-novel/development_guidelines.md)。
* 完整项目迭代日志与功能变更记录，请阅读 [DEVELOPMENT.md](file:///Users/jarvizzhang/IdeaProjects/star-novel/DEVELOPMENT.md)。

---

## 📄 开源协议 (License)

Copyright © 2026 STAR NOVEL Team. All rights reserved.
