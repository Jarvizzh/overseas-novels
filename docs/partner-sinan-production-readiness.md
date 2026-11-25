# Partner 分销（番茄司南）正式接入清单

2026-11-25。本文配合[统一转化回传架构](conversion-reporting-architecture.md)使用，记录生产接入的开发缺口、配置与联调验收。

司南的三个回调入口、Inbox、Meta 投递和原 Pixel 最终回告已有模拟实现。当前公网 Callback、Inbox 消费和回告发送仍依赖测试环境及模拟契约。正式接入需要确认真实协议，接通生产配置和服务，并取得司南与 Meta 的端到端联调证据。[模拟联调说明](../conversion-backend/README.md#L67) · [服务入口](../conversion-backend/cmd/conversion/partner.go)

## 一、分模块开发与配置

下表中的配置为生产接入需提供或核对的内容。字段名沿用仓库已有名称或业务协议名称，正式入口和配置格式随联调确认。

| 模块 | 待完成工作 | 配置与输入 |
| --- | --- | --- |
| 公网 Callback、入站鉴权 | 接通三个 HTTPS 路由，按正式契约实现鉴权、重放处理和凭据轮换。沿用 Inbox 写入提交后应答、重复接收幂等、冲突隔离的流程。 | 回调域名与路径、`integration_key`、入站鉴权方式与凭据、签名字段及重放窗口、环境与启用开关。 |
| CMS 接入账户与授权 | 增加生产契约的账户配置和轮换入口。启用时校验 App、事件和 Pixel 授权，凭据加密保存，配置和轮换写入审计。当前 `/sinan-mock` 配置入口仅在测试环境注册。 | 正式 `contract_version`、账户键、允许的 App 和事件类型、`app_pixels`、`promotion_pixels`、附加目标、`source_hosts`。 |
| Inbox 消费与归一化 | 在生产 Worker 注册正式消费者，将归一化路由接入实际环境。用真实报文核对事件 ID、原始时间、金额、币种、订单及匹配字段。如果 ID 仅在 App 内唯一，将 App 纳入接入身份或去重范围。保留付款去重和原目标路由。 | 环境映射、账户与 App 作用域、事件开关、`inbox_normalizers`。 |
| Meta 路由与司南回告 | 核实原 Pixel 与附加目标授权，配置 CAPI 凭据。接通生产回告 Worker，实现正式 HTTPS 请求、签名、响应判定、幂等和重试。仅回告原 Pixel 的业务终态。 | Meta Pixel Token、回告 endpoint、签名密钥和版本、回告时限、无 trace 及缺推广 ID 的编码、幂等重放能力。 |
| 部署、对账和监控 | 接通公网 Callback、生产 Inbox 消费和独立回告 Worker 的部署入口。检查数据库权限和密钥注入。CMS 能从来源回调查到统一事件、原 Pixel 投递及司南回告。Collector 覆盖心跳、积压、阻塞和结果未知。 | Callback 与回告服务的数据库连接、环境和密钥、`worker_instances`、`reader_normalizers`、`inbox_normalizers`、Worker 连接池、回告延迟告警阈值。 |

[生产与测试入口](../conversion-backend/cmd/conversion/main.go#L125) · [队列与字段契约](conversion-reporting-architecture.md#4-司南接入实现) · [当前部署配置生成](../deploy/linux/prepare-config.py)

### 事件与路径映射

| 计划登记的回调路径 | 司南事件 | Meta 事件 |
| --- | --- | --- |
| `POST /callbacks/sinan/{integration_key}/attribution-succeeded` | 染色成功 | `CompleteRegistration` |
| `POST /callbacks/sinan/{integration_key}/order-created` | 订单创建 | `InitiateCheckout` |
| `POST /callbacks/sinan/{integration_key}/recharge-paid` | 代币或 VIP 充值完成 | `Purchase` |

## 二、需要司南确认的交付项

- **入站协议与账户开通。** 确认测试和生产账户、App 范围、回调登记方式、鉴权和重放规则，以及签名规范和验证样例。架构文档中的 Ed25519 用于我方发给司南的回告，司南来向的鉴权仍待确认。
- **三类原始报文与字段字典。** 充值提供代币与 VIP 样例。确认 `event_id` 的唯一范围和重试稳定性、`event_time` 格式与单位、订单号、金额币种、原 Pixel、推广与广告字段，以及可提供的匹配字段。
- **入站响应与补发。** 确认成功应答、HTTP／业务错误码、重试条件及间隔、停止重试条件和人工补发方式。
- **回告协议。** 提供测试与生产 HTTPS endpoint、签名示例、密钥轮换方式、响应确认规则、回告时限和幂等键。明确 `report_status` 为 `1`、`-1`、`0` 的业务语义，以及无真实 `fbtrace_id` 或缺推广 ID 时的合法请求体。确认响应丢失后能否查询或安全重放。

[架构中的联调待确认项](conversion-reporting-architecture.md#72-联调待确认项)

## 三、联调验收矩阵

| 场景 | 通过标准 |
| --- | --- |
| 三类真实回调 | 指定 App 下分别接收染色、建单、代币支付和 VIP 支付，核对来源字段、原始时间、事件映射、原 Pixel 和 Meta 接收证据。 |
| 鉴权、重放与越权 | 有效请求可入库。篡改、过期、重放和越权按正式契约处理。模拟入库临时失败，核对司南实际重试与补发行为。 |
| 去重与订单关联 | 相同业务回调重送只生成一个事件。同键不同内容可查。建单与付款按订单关联且各自落库。同笔付款换一个事件 ID 重送时，购买与付款计数保持一致。 |
| 原目标与附加目标 | 给同一事件增加一个附加 Pixel，分别验证成功和失败。司南只收到原 Pixel 的业务终态，Meta 重试期间没有中间回告。 |
| 三类终态与无 trace | 司南确认接收成功 `1`、失败 `-1` 和业务跳过 `0`。Meta 超时或始终缺少明确接收确认时，在定稿期限后验证无真实 trace 的 `-1` 能被接受。 |
| 回告响应丢失与对账 | 模拟司南收到回告但响应丢失，按约定查询或重试并维持一个业务终态。回告重试只推进回告队列。双方能用来源事件 ID、订单号及原 Pixel 核对。 |
| Reader 与 Partner 混合负载 | 以 2 个 Worker、每实例 Reader 2 + Partner 2、连接池 16 为压测起点。回放预计峰值至少 30 分钟，再验 1.5 倍峰值与单实例重启。分别记录 Reader Outbox、Partner Inbox、Meta 投递和司南回告的速率、最老等待与清空时间。 |

[完整架构验收矩阵](conversion-reporting-architecture.md#8-验收矩阵) · [双源容量方案](conversion-throughput-plan.md#分销正式上线时的消费者配置)

## 四、推进顺序与首发判据

1. **双方先锁定协议。** 优先取得真实回调样例、入站鉴权规范、正式回告规范，以及无 trace 和重复回告的处理约定。
2. **我方并行准备运行条件。** 完成生产配置入口、独立启停开关、服务部署及对账监控。按确认的协议接通正式回调、消费者和回告适配器。
3. **隔离联调后进行指定 App 验收。** 用一个授权 App 与指定原 Pixel 验证「司南回调 → Inbox → Meta 明确确认 → 司南确认收到最终回告」，再跑通一次无 trace 的失败回告。保留脱敏的来源事件 ID、内部事件 ID、原 Pixel、Meta 接收证据、司南应答及 CMS 对账记录。
4. **按预计生产负载验收后放量。** 核对混合压测与故障切换结果，并验证生产开关和告警。消费实例数与连接池起始值用实测校准。
