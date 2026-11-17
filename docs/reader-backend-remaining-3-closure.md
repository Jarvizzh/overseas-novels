# 三项遗漏修复及最终复验

日期：2026-11-17。对象为当前工作区中三批修复及本轮补充代码，尚未提交、部署。HEAD 仍为 `c8b87fe`，不能仅用该提交号代表修复内容。

**F01/R04、F02/R09、F03/R14 已修复并通过本地代码检查与隔离回归。原 17 项在此前约定的代码及隔离验证范围内全部关闭。** 历史身份恢复、真实渠道、浏览器、多节点和生产迁移等验收边界保留，不改称为已完成线上验收。

## F01 / R04：全部本人可管理协议

- Repository 新增 `ListManagedSubscriptions`，不再用一条摘要记录代替协议管理集合。返回本人待审批、ACTIVE、暂停、取消中及仍有付费权益的已取消记录，排除其他账号和 mock 协议。
- `/wallet/subscription/status` 新增 `subscriptions` 数组，空结果为 `[]`；保留 `subscription` 摘要兼容已有调用方。`is_vip` 仍由独立权益谓词计算。
- 充值页按协议逐项展示，每项有独立的取消入口、请求中状态和失败提示，不因较新的 CANCELLED 摘要隐藏较旧 ACTIVE。取消中的协议不可重复点击。
- 取消只能操作本人 PayPal 协议；他人或不存在的协议返回 404，不泄漏内部错误。

**正式回归：`TestAllOwnedAgreementsRemainManageable`。** 用真实 Gin 路由及数据库验证 ACTIVE+CANCELLED、ACTIVE+PENDING、ACTIVE+ACTIVE、SUSPENDED+CANCEL_PENDING 四组；旧协议付费期已过、新已取消协议仍有付费期的原复现场景已覆盖。逐项核对列表、旧协议可取消、另一协议不被误改、他人协议不可取消及空数组响应。

## F02 / R09：套餐继承不丢推广身份

- 创建单次订单始终取得用户绑定的推广记录，不再用 `recharge_template_id IS NOT NULL` 过滤整个推广身份。
- 仅对有效套餐 ID 做 COALESCE 默认继承；订单继续保存原推广 ID 和服务端选择的像素，不接受客户端替换像素。
- 整本适用范围使用同一推广解析结果；继承默认套餐时仍受原推广目标小说约束，不因此变成任意小说的整本报价。
- 原本未配置像素的订单保持 unattributed；原本配置了像素的推广订单正确进入 pending 投递队列。

**正式回归：`TestInheritedAndExplicitOffersKeepPromotionIdentity`。** 覆盖继承/显式套餐 × 有/无像素四组，调用真实订单创建、渠道回执结算和 outbox 触发器，核对推广 ID、像素、事件状态、原小说整本可买以及其他小说被拒绝；不请求真实 PayPal 或 Meta。

## F03 / R14：历史更新不是收款事件入口

- 新增 `recharge_orders.payment_events_enabled`，默认 false。应用迁移不会自动把存量订单纳入事件投递，也不会因重复迁移改变已有标记。
- 新代码验证并结算单次收款时显式设置 true；新导入的已验证订阅交易显式设置 true，周期尚待确认时仍不投递。
- 事件触发器要求已纳入、成功、已履约且非已退款报告；对 UPDATE 另要求原记录尚未履约。旧已履约订单的退款失败恢复、备注更新、普通重放不生成新 Purchase。
- 已纳入的新订阅先待周期确认、后首次履约，仍生成一次 Purchase 和 Subscribe。退款先于发货时不残留未履约收款事件，原有事务回滚、租约恢复和投递重试规则保留。
- 历史未纳入的订单不由普通状态更新自动补发。若有独立证据需要历史补发，仍需显式审核，而不是批量把所有旧订单标记为 true。

**正式回归：**

- `TestLegacyRestorationNeverEnrollsPaymentEvents`：默认未纳入的历史履约单在退款失败恢复及备注更新后没有事件；重复执行完整 schema 后仍未纳入；即使后续手工打开标记，旧履约记录普通 UPDATE 也不被当作首次履约。
- `TestDeferredNewSubscriptionEmitsOnlyAtFirstFulfillment`：新交易待周期时无事件，首次履约后产生 Purchase/Subscribe，重复回执及退款失败恢复不增加事件数量。
- 原有 `TestTransactionalPaymentOutbox`、`TestRefundBeforeFulfillmentLeavesNoPaymentEvent`、`TestOutboxLeaseFencesCrashedWorkers` 和支付集成继续通过。人工 SQL 构造新事件管线的测试数据显式设置纳入标记，不再依赖历史订单的默认值。

## 本轮验证结果

| 检查 | 结果 |
| --- | --- |
| Reader 全量 `go test -race -count=1 ./...` | 通过，使用新的空临时数据库；原支付集成及本轮正式回归实际执行 |
| 最终补充的原始旧协议到期条件、重复迁移边界 race 回归 | 通过 |
| Reader `go vet ./...` | 通过 |
| CMS `go test -count=1 ./...` | 通过 |
| Reader 前端 `npm run build`、`npm run lint` | 通过 |
| `git diff --check` | 通过 |

临时 PostgreSQL 16 无业务挂载，验证后删除。未修改生产数据，没有真实广告/支付调用。前端执行了格式化并保留无关代码排版；列表操作保留原生按钮和现有键盘焦点样式。没有宣称做过真实浏览器逐项点击或生产端到端验收。

## 发布注意

先应用共享 schema，再更新 Reader 后端和 Reader 前端。旧客户端只读取 `subscription` 摘要，必须更新前端才能使用完整协议列表。支付结算流量应切换到新版本；不要把旧服务仍使用内存投递的行为视为新 outbox 保证。存量已履约订单保持未纳入默认值，不批量打开标记来“补齐历史”。

此前的 [17 项首轮复验](reader-backend-17-fixes-acceptance-2026-11-17.md) 保留缺陷复现证据，以本文的后续修复结果作为当前关闭状态。
