# StarNovel 支付中心 CMS 前端 Review 与重构方案

评审日期为 2026-11-22。以 `codex/payment-domain` 的 `ac1c6e1` 及当前工作区为基线，包含工作区中注册的渠道账户、商品目录和充值模板新契约。优先解决财务选错处理对象、待办难以定位、结算操作串渠道，以及商品和模板无法按新契约维护的问题。

建议保留「交易与订单、订阅与账期、退款、风控、争议与冲回、资金对账、渠道与商户」的任务导航，增加「商品与定价」。各操作性工作台把 **PayPal、Stripe、正式／沙箱、商户账户** 放在结果上方，列表、详情和操作共用查询范围。Stripe 直收与 MoR 在相关记录和处置步骤中单独标明。方案沿用当前 CMS 公共控件、表格和抽屉，并把后台权限、可执行动作和真实渠道状态接到界面。[项目开发规范](../development_guidelines.md) · [支付域架构](payment-domain-architecture-and-implementation.md#35-reader-h5-与-cms-前端设计)

## 1. Review 发现

以下保留首轮 Review 发现。2026-11-22 后续工作区已出现商品页面、渠道配置面板和按 `product_id` 提交的模板表单。对应缺口以新实现的接口及浏览器回归结果为准。 [商品页面](../cms-front/src/pages/ProductsPage.tsx) · [渠道配置面板](../cms-front/src/pages/PaymentChannelsPanel.tsx) · [新版模板表单](../cms-front/src/pages/TemplatesTab.tsx#L321)

| 优先级 | 发现与影响 | 依据 |
| --- | --- | --- |
| P1 | **退款深链接会静默换掉指定的原收款。** 指定收款查不到时，页面选履约收款或首笔。模拟同单有 Stripe 与 PayPal 收款时，从 Stripe 失效链接进入会选中 PayPal，输入金额后可以打开该笔 PayPal 的退款确认。确认文案仅显示订单、收款内部编号和金额，难以发现渠道已经变更。 | [收款选择](../cms-front/src/pages/PaymentRefunds.tsx#L65) · [退款确认](../cms-front/src/pages/PaymentRefunds.tsx#L294) |
| P1 | **首轮的充值模板表单无法按新契约保存。** 新接口要求每个卡位提交 `product_id`，并拒绝卡位覆盖价格、币数和周期。首轮 CMS 仍提交旧商业字段，且缺少商品管理入口。新增和编辑均触发契约冲突，已有模板仍可读。 | [新模板请求](../cms-backend/internal/billing/handler.go#L304) · [后续模板表单](../cms-front/src/pages/TemplatesTab.tsx#L321) |
| P2 | **结算查询和操作范围脱节。** Stripe 沙箱流水视图中，结算操作仍默认「导入 PayPal 结算报表」，甚至可选择同环境 PayPal 账户。后端能校验操作类型和账户对应关系，但页面当前选择的 Stripe 范围没有传给操作表单。切换「结算批次」也会清掉已定位的批次或订单上下文。 | [对账视图](../cms-front/src/pages/PaymentReconciliation.tsx#L224) · [结算操作](../cms-front/src/pages/SettlementCommands.tsx#L17) · [服务端校验](../cms-backend/internal/billing/settlement_commands.go#L104) |
| P2 | **风控复核、渠道信号混排，案件详情离开操作视线。** 风控界面提供「渠道信号与复核」和「公共结账决策」，但没有 PayPal／Stripe 可见筛选，也没有履约复核队列。模拟 20 行时，点击首条案件后详情出现在 800 px 视口以下，焦点仍在行按钮。决策视图沿用 `environment`、`provider`，其 API 实际识别 `checkout_environment` 且不支持渠道过滤。 | [案件页](../cms-front/src/pages/PaymentCases.tsx#L192) · [案件列表和决策 SQL](../cms-backend/internal/billing/aftercare_repository.go#L34) · [决策查询参数](../cms-backend/internal/billing/aftercare_handler.go#L90) |
| P2 | **争议详情可查、可处置，操作范围和进度表达不足。** 当前已有 Stripe MoR 责任提示、PayPal 后台入口、负责人、举证期限、原收款、退款和使用摘要。页面把分派、案件操作及权益冲回／恢复放在表格下的内联表单，确认时缺少此次操作的原收款、金额、证据与影响摘要。按钮由页面状态分支推断，API 最终以当前权限、案件状态和证据决定是否接受。 | [案件详情与操作表单](../cms-front/src/pages/PaymentCases.tsx#L69) · [服务端处置](../cms-backend/internal/billing/aftercare_service.go#L25) |
| P2 | **退款页只能按订单查命令，无法按渠道跟进积压。** 页面已有原请求键恢复、可退金额和 PayPal 已确认退款凭证关联。当前明细表只消费命令状态和结果说明，收款 DTO 把已退和预留合并进可退余额，财务看不到两者拆分。按收款查询命令最多 50 条，且看不到无原收款引用的旧命令。渠道退款事实需要从其他证据补查。 | [退款页](../cms-front/src/pages/PaymentRefunds.tsx#L111) · [收款余额查询](../cms-backend/internal/billing/payment_queries.go#L123) · [退款命令查询](../cms-backend/internal/billing/refund_command.go#L162) |
| P2 | **对账页面展示数据丰富，但查询和处置流程分离。** 流水表默认 14 列，操作集中在顶部折叠表单，财务从异常批次进入匹配时还需重填商户和渠道划拨号。命令列表只显示少量字段，遗漏后端返回的命令编号可见入口、操作依据、结果、操作者和时间，账户未展示已有的自动拉取完成时间。列表拉取失败后，自动轮询等待人工刷新。 | [流水和批次](../cms-front/src/pages/PaymentReconciliation.tsx#L129) · [命令表单与轮询](../cms-front/src/pages/SettlementCommands.tsx#L39) · [命令与账户 DTO](../cms-backend/internal/billing/settlement_commands.go#L155) |
| P2 | **首轮渠道与商户走旧配置表单。** 原页面能看存量账户和旧 Stripe 卡位配置，编辑调用旧 `PUT /channels/:id`。已注册的建档、版本编辑、验证、发布、停用入口需要接入。账户风控策略已有写接口，但缺当前策略的独立读契约和操作界面。 | [后续渠道配置面板](../cms-front/src/pages/PaymentChannelsPanel.tsx) · [渠道生命周期接口](../cms-backend/internal/billing/handler.go#L48) · [风控策略](../cms-backend/internal/billing/aftercare_policy.go#L15) |
| P2 | **跨页联查和列表状态尚未统一。** 订单列表已携带已知环境跳到各工作台，但订单查询入口只消费深链的订单号。订阅只按用户查，服务端最多 100 条。以案件为例，前端用「返回第一页」和「返回 20 条」推测下一页，详情失败无独立重试。现有订单列表请求失败仅记控制台。 | [订单筛选及错误](../cms-front/src/pages/OrdersTab.tsx#L94) · [订单联查](../cms-front/src/pages/OrdersTab.tsx#L581) · [订阅查询](../cms-backend/internal/billing/payment_queries.go#L90) · [案件分页](../cms-front/src/pages/PaymentCases.tsx#L257) |
| P2 | **商品管理的角色与返回字段需要收口。** 商品列表和详情对 Admin、Support 开放，当前按同一 DTO 返回渠道绑定、校验结果和放行依据，详情的 `all_bindings` 还包含其他环境记录。架构建议渠道绑定与正式发布由 SuperAdmin 执行，当前 Finance 同样可调用这两类写接口。商品页上线前需要明确角色及字段边界，由服务端执行。 | [商品路由授权](../cms-backend/internal/billing/handler.go#L68) · [商品读接口](../cms-backend/internal/billing/products.go#L208) · [绑定字段和环境](../cms-backend/internal/billing/products.go#L363) · [商品角色建议](payment-domain-architecture-and-implementation.md#cms-支付中心与旧入口退役) |

退款、案件、对账已复用部分 `TableRegion`、骨架屏和平台样式。退款确认已使用 `CmsDialog`，模拟验证也确认默认焦点落在「取消」。筛选、行操作、案件详情、反馈和状态恢复仍缺统一模式，财务要在同一模块内适应原生选择框、表格下内联详情和折叠表单。现有订单页可作筛选和表格结构的参考，回传中心的记录抽屉可作详情结构的参考。[开发规范](../development_guidelines.md) · [架构文档的组件复用约定](payment-domain-architecture-and-implementation.md#ui-一致性与公共组件复用) · [现有订单页](../cms-front/src/pages/OrdersTab.tsx#L465) · [回传事件抽屉](../cms-front/src/components/conversion/ConversionEventDrawer.tsx)

## 2. 页面结构与渠道范围

| 入口 | 页面内导航 | 首要任务 |
| --- | --- | --- |
| `/payments/orders`、`/payments/subscriptions` | 交易列表／交易详情，协议列表／原账期详情 | 从购买快照、尝试、实收和履约追到同一原收款的售后与资金。 |
| `/payments/refunds` | 退款待办、退款记录 | 恢复进行中命令，核对退款事实。从指定订单和原收款发起退款。 |
| `/payments/risk` | 履约复核、渠道信号、结账决策 | 财务先处理待复核或已升级事项，再查 Stripe Radar、PayPal 渠道信号及平台决策原因。 |
| `/payments/disputes` | 争议案件、独立冲回 | 按责任和截止时间分派、查看证据、进入渠道后台，跟进资金结果与权益处置。 |
| `/payments/reconciliation` | 资金流水、结算与银行入账、报表差异、操作记录 | 从渠道报表或划拨追到银行实收，从差异行定位凭证和原交易。 |
| `/payments/channels`、新增 `/payments/products` | 商户账户详情、商品详情 | 管理渠道账户、版本和发布，维护同一商品的双渠道绑定。充值模板从已发布商品选择卡位。 |

以风控为例，桌面内容区沿用 CMS 面包屑与工作台布局。行操作打开详情抽屉，当前渠道和筛选继续留在背景列表。

```text
支付中心 / 风控
履约复核    渠道信号    结账决策
全部渠道    PayPal    [Stripe]      [沙箱 ▾]  [商户账户 ▾]
[查订单、收款或案件号]                [筛选 ▾]  [视图 ▾]
待复核 ×    我负责的 ×
状态       原收款       负责人       处置期限       操作
待复核     ch_…         财务账号     11-22 18:00   查看 ›

详情抽屉  Stripe · 沙箱 · 商户名称
         收款与履约  /  风险证据  /  操作记录
         [批准后重新核验]  [拒绝并转交财务]
```

- 渠道使用可直接选择的「全部渠道、PayPal、Stripe」文字项。全部渠道只供浏览对比。涉及渠道写操作时，由明确选中的渠道、环境、账户及记录锁定目标。银行流水导入属于选定环境的财务操作，匹配时才锁定具体渠道划拨和银行交易。商品详情同屏展示两个渠道的绑定结果，发布按环境核验两端就绪。平台结账决策保留尚未选渠道的记录，使用独立的结账环境查询参数。
- 查询范围、筛选、当前页及详情 ID 用 URL 恢复。跨业务入口传已核实的渠道、环境和稳定记录 ID，能取到原账户时一并传递。账户显示使用脱敏名称或内部编号。链接以原订单、原收款、争议和结算批次的关联为准，禁止用当前默认范围替换已指定目标。深链丢失、目标不存在或范围冲突时显示原因并停止敏感操作。手动切换范围时关闭当前详情，清理不兼容筛选，返回列表或上一页时恢复原上下文。
- 渠道标签贯穿列表、详情标题和确认弹窗。Stripe 销售模式是对应交易属性，展示直收、MoR 或待核实。正式和沙箱维持现有路由值 `live`、`sandbox`，结账决策按 `checkout_environment` 映射已知值。岗位导航沿用 CMS 侧栏，旧 `/orders`、`/payment` 保留授权跳转。具有关联意义的历史查询参数随兼容跳转保留。

## 3. 工作台流程与现有接口

### 3.1 退款

财务从待办或交易详情打开同一原收款。详情依次展示收款与履约摘要、原收款金额、已退、预留、当前可退、退款事实及操作记录。发起时确认 **渠道、环境、原账户、订单、原收款、金额币种**，说明权益状态与退款处理进度。多笔收款逐笔选择，明确哪笔支持了履约，已绑定的原收款不可由页面自动更换。

沿用原命令请求键、按键或编号查证、同键同参数重试。刷新或原操作者重新登录后可恢复。若本地键丢失，先查原收款的命令与已确认退款，再允许发起另一笔退款。PayPal 仅在服务端返回 `can_reconcile` 时提供「关联已确认退款」，确认说明该操作只关联已核实凭证。Stripe 展示原 Charge 对应退款及确认结果，继续走原命令查询。部分退款、全退和累计净本金损失接服务端口径。前端依据服务端可用动作和金额上限控制按钮，暂停提交的具体原因一并显示。[退款契约](../cms-backend/internal/billing/handler.go#L61) · [命令去重与金额预留](../cms-backend/internal/billing/refund_command.go#L89)

可先接原收款详情和已知命令恢复。退款全局待办需要新增分页查询，至少支持渠道、环境、账户、命令状态、编号和订单，并兼容未绑定收款的旧命令。收款详情需返回分别确认的已退、预留、可退、退款事实和可用动作。按服务端证据提供退款来源，缺失时显示待核实。命令历史保留操作时间、链路编号和已授权审计字段。

### 3.2 风控

「履约复核」优先展示待复核、已升级、退款待办和争议待办，列表提供负责人、处置期限、订单及原收款。抽屉展示收款、履约状态、证据和审计。审批后标示「待重新核验收款」，直到实际履约结果返回。拒绝后给出关联退款或争议入口。Stripe Radar Review、预警和拒绝信息进入「渠道信号」，PayPal 展示实际取得的拒绝、验证及其他已归集信号。无数据时区分账户能力未开通、尚未采集与筛选无结果。渠道侧是否支持更多处置以账户能力为准。

现有 `/payments/cases`、`/decisions`、案件详情、负责人和动作接口可复用。正式交付需补 `kind`、负责人等服务端队列筛选和游标元数据，避免只过滤当前页。案件详情需返回账户标识和当前可用动作。结账决策使用后端已支持的环境参数，渠道过滤需要后端明确支持并保留尚未选渠道的决策。账户策略设置放在「渠道与商户」的账户详情，先补策略读取，再接已有写接口。[用例路由](../cms-backend/internal/billing/aftercare_handler.go#L14) · [风控工作包](payment-domain-architecture-and-implementation.md#44-支付风控) · [账户策略写接口](../cms-backend/internal/billing/aftercare_policy.go#L26)

### 3.3 争议与冲回

主表优先展示处理责任、是否待本方响应、举证截止、负责人和金额。详情提供渠道案件阶段和裁决、关联原收款及账期、使用及退款证据、已确认划扣或返还、权益处置及审计。PayPal 争议提供对应环境的渠道后台和可复制案件编号，独立冲回从原 Capture 或 Sale 联查。Stripe 直收显示商户响应任务和对应后台。MoR 显示 Stripe 的响应责任及本方协查信息。销售模式待核实时保留待确认提示。

在 CMS 内分派和处理有权执行的权益任务。冲回／恢复确认时展示作用对象、确认损失金额、证据版本和预期影响，服务端以当前资金和状态校验。提交后继续跟踪已受理、执行中和最终状态。渠道举证或接受案件沿用架构约定的渠道后台。后端已有案件详情、原收款、退款、损失与证据键，正式交付需补渠道责任和响应标识、可用动作、可追踪的处置结果，以及争议／冲回与到期状态的分页筛选。Stripe 案件直达在多商户及沙箱环境用授权样本核验。[争议详情 SQL](../cms-backend/internal/billing/aftercare_repository.go#L52) · [案件动作](../cms-backend/internal/billing/aftercare_service.go#L25) · [争议工作包](payment-domain-architecture-and-implementation.md#45-争议)

### 3.4 资金对账

流水默认显示时间、渠道账户、动作、总额、手续费、净额和关联入口。销售税、代扣税、处理费、买家实付、换汇费以及渠道引用进详情或可选列。批次显示渠道划拨、银行实收、差额及核对状态，可打开批次成员、银行凭证和原交易。报告差异定位文件、行号、核对原因和关联流水。按筛选范围展示后端给出的 USD 已确认汇总，注明时间口径。缺权威凭证的历史金额标记为待核对并保留空值。

PayPal 提供分卷导入和已授权的自动拉取。Stripe 提供拉取划拨和导入已生成报表，直收与 MoR 在详情展示可用税费证据。银行流水导入和划拨匹配进入各自的确认弹窗，批次入口带入商户、环境和划拨号。操作记录接现有命令编号、状态、依据、结果、操作者和时间。账户详情接自动拉取状态、上次完成时间与错误。待确认写操作保留原请求及文件摘要，使用同键同参数重试。上传文件需重选时明确提示，凭证内容留在授权表单会话内。

已有流水、批次、报告差异、近期命令、账户自动拉取及提交接口可接通。按账户、时间、流水类型查询和按编号或原请求键查结算命令需补服务端契约。当前命令查询按环境最多 30 条，不能充当完整操作历史。把「银行匹配」「报表补证」接回对应差异，关闭差异或变更资金状态由服务端验证凭证后完成。[对账 API](../cms-backend/internal/billing/reconciliation.go#L12) · [结算命令](../cms-backend/internal/billing/settlement_commands.go#L104) · [资金对账工作包](payment-domain-architecture-and-implementation.md#42-资金账务与对账)

### 3.5 交易、商户、商品与模板

交易详情整合购买快照、支付尝试、原收款、履约和关联售后，订阅详情沿用 PayPal 与 Stripe 账期 API，显示渠道协议状态、已付期截止、账期实付和对应原收款。订单及订阅需补渠道、环境和账户查询，订阅要支持服务端分页。保留既有订单筛选和客服按号查询。首页继续使用已有的正式环境退款和拒付划扣分别统计，只调整下钻目标与查询范围。[订单与账期路由](../cms-backend/internal/billing/handler.go#L35) · [首页统计](../cms-front/src/pages/DashboardTab.tsx#L15)

商户页按渠道展示账户、主路由、验证结果、已发布及待发布版本、两个新交易开关。接现有新建、编辑、验证、发布和停用路由，凭证变更走待发布版本。确认影响时分别写明新交易和历史交易范围。新增待验证账户、替换凭证和切主账户时，使用 API 返回的状态和版本。字段分 PayPal 和 Stripe 表单，列表用脱敏视图，密钥只写。验证结果按后端已提供的证据显示，最近有效回调等字段缺失时补 DTO 后接入。[渠道 API](../cms-backend/internal/billing/handler.go#L48) · [账户响应](../cms-backend/internal/billing/payment_channels.go#L77)

商品页接 `/products` 的列表、详情、创建、复制、双渠道绑定、发布、暂停和退役。商品详情显示 USD 定价、交付条款、两个渠道的就绪情况、失败原因、环境与模板引用。改价、改赠币或改销售条款通过复制生成新商品。模板编辑仅调整商品引用和展示字段，保留已有卡位顺序、预览、默认模板及推广链接，提交 `product_id` 等新字段。以商品页接管旧 Stripe 卡位配置入口，切换前核实存量绑定、模板引用和历史查询。财务绑定时使用授权返回的账户候选和校验结果，避免扩大商户配置读取权限。[商品与模板 API](../cms-backend/internal/billing/handler.go#L68) · [商品详情字段](../cms-backend/internal/billing/products.go#L51)

## 4. 开发规范与实现边界

### CMS 样式一致性验收要求

退款、风控、争议、资金对账及其关联页面按同一套 CMS 样式验收。支付域可调整业务字段、顺序和布局组合。字体、字号、字重、颜色、边距、密度、状态和交互反馈按下面的现有 CMS 页面及组件逐项对照。已有公共组件满足需求时直接复用。缺少公共组件时，以已有页面为样式和交互基准，在具备多处用法时从平台模式抽取共享实现，保留现有调用方行为。

| 界面元素 | 平台基准与通过条件 |
| --- | --- |
| 布局、字体与字号 | 复用 `App`、`CmsSidebar` 和 `index.css` 的字体栈、主题变量、面包屑与内容区。标题、正文、次要信息、数字与编号、表头及单元格按对应 CMS 元素的字号、字重和行高验收。浅色与深色均沿用平台主题。 [应用布局](../cms-front/src/App.tsx) · [CMS 样式](../cms-front/src/index.css#L5) |
| 页面内 Tab | 以「回传中心」及「书城配置」顶部横向 Tab 为基准。沿用相同的字号、字重、间距、分隔线、选中、悬停和焦点状态，并保留键盘切换及窄宽度滚动。渠道属于筛选范围，渠道控件采用平台的筛选或已有同类选择控件。 [回传中心 Tab](../cms-front/src/pages/ConversionCenterTab.tsx#L241) · [书城配置 Tab](../cms-front/src/pages/OperationsTab.tsx#L638) |
| 列表与筛选 | 以「交易与订单」为基准，实际使用 `TableRegion`、`.cms-table`、`SearchInput`、`CustomSelect`、`ToolbarPopover`、`FilterChipsBar`、`StatusDot` 和 `SkeletonRows`。列表边框、行高、密度、对齐、行操作、无结果和错误状态在两个页面中用同一模式。 [订单筛选](../cms-front/src/pages/OrdersTab.tsx#L331) · [订单列表](../cms-front/src/pages/OrdersTab.tsx#L465) |
| 分页 | 有总数的列表直接渲染 `TableRegion` 页脚中的 `Pagination`，保持记录范围、每页条数、页码、按钮及禁用状态的既有规则。游标列表沿用回传事件的页脚布局和前后页体验，按钮可用性以服务端游标为准。 [页码分页](../cms-front/src/components/ui/Pagination.tsx#L41) · [游标分页](../cms-front/src/components/conversion/ConversionEventsTab.tsx#L775) |
| 抽屉与确认弹窗 | 实际使用 `CmsDialog` 的 `drawer` 和 `modal` 变体，根据内容复用已有尺寸档位。标题、副标题、关闭按钮、分隔线、内边距、滚动区域、页脚按钮和关闭方式均沿用组件。以回传事件详情为抽屉对照，验证打开、确认、取消与关闭后的焦点。 [公共弹窗与抽屉](../cms-front/src/components/ui/CmsDialog.tsx#L21) · [回传事件详情](../cms-front/src/components/conversion/ConversionEventDrawer.tsx) |
| 支付专属内容 | 退款金额摘要、争议证据、对账批次与商户版本等内容以 CMS 的标签值布局、状态点、标准按钮及反馈模式组成。缺少对应组件时，评审明确对照页面及沿用的主题、字号、间距和交互。 |

验收时在同一主题和桌面宽度下，把支付页面与对应参考页面并排核对。至少覆盖默认、选中、悬停、键盘焦点、加载、空数据、错误、禁用和在途状态。组件复用清单记录实际组件名和引用位置，新增控件记录对应的 CMS 样式和交互来源。

### 页面实现边界

| 规范 | 开发要求 |
| --- | --- |
| 主题、布局和公共组件 | 继续使用 `App`、`CmsSidebar`、面包屑、主题与 `index.css` 的 `--cms-*`、`.cms-table`、标准按钮和控件。筛选复用 `SearchInput`、`CustomSelect`、`ToolbarPopover`、`FilterChipsBar`。常规列表复用 `TableRegion`、`ViewOptionsPopover`、`RowActionMenu`、`StatusDot`、`SkeletonRows` 和 `Pagination`。游标列表沿用平台的前后页交互，由服务端返回下一页游标。 |
| 操作和反馈 | 详情使用 `CmsDialog` 抽屉，确认用同组件的模态版本。复用现有详情布局及复制交互。键盘焦点进入详情，确认默认焦点放在安全操作，关闭后回到触发按钮。表格保留可识别的表名和列标题，宽表在 `TableRegion` 内可滚动。列表和抽屉各自处理加载、无数据、无匹配、无权限、失败和重试。即时反馈用 `ToastProvider`，在途操作保留可持续查询的页面状态。 |
| 页面和契约 | 按交易、退款、案件、对账、渠道和商品职责拆页面组件。在现有 `apiRequest` 之上集中支付请求、类型、状态文案、金额单位及查询范围转换，复用 CMS 已有错误处理。金额按契约处理最小单位或精确定点，编号保留精度。明确 UTC 报表时间与页面展示的时区。状态、资格、损失和余额由后台提供。 |
| 权限、数据与恢复 | Finance 和 SuperAdmin 拥有售后和结算权限，商户配置限 SuperAdmin。Admin 和 Support 在交易和订阅页只看授权摘要，Support 保留按订单／用户定位。商品页按已确认的角色分工授权，建档与模板配置可供 Finance，渠道绑定和正式发布收口至 SuperAdmin。商品摘要和授权配置详情由服务端按角色返回。动作入口按权限及服务端返回资格呈现。超时按原请求查证，版本或证据冲突刷新同一目标。 |
| 评审和验证 | 每个页面工作包附组件复用清单、浅深色截图、服务端契约对照、权限与旧链接回归。改动公共组件时回归现有调用方。对变更的前端格式化、运行 lint 与 build，变更的 Go 模块验证 test、vet，涉及并发命令或迁移时在隔离环境补跑相应验证。 |

有实际共用场景时提取支付范围选择、关联入口、命令状态与证据展示。页面专属字段和处置步骤保留在各自功能组件中。公共组件需要扩展时沿用现有接口的默认行为。[开发规范](../development_guidelines.md) · [CMS 公共组件](../cms-front/src/components/ui/Primitives.tsx) · [CMS 弹窗](../cms-front/src/components/ui/CmsDialog.tsx) · [CMS Toast](../cms-front/src/context/ToastContext.tsx)

## 5. 开发分期与验收

| 顺序 | 可交付内容 | 验收标准 |
| --- | --- | --- |
| 1 目标正确与模板可用 | 修复失效退款深链和确认摘要，对账表单继承当前范围，统一联查规则。并行接商品列表与选择器，使现有模板可按新契约保存。 | 同一订单含两笔不同渠道收款，指定哪笔就定位哪笔，缺失时显示错误且无可提交按钮。Stripe 沙箱命令表单只显示相符账户和操作。旧模板读取、复制、新建、替换商品及重排可用，空缺或未发布商品有清楚反馈。 |
| 2 四个财务工作台 | 统一渠道范围、筛选、表格、抽屉及权限反馈。落地退款待办、风控复核、争议与冲回、批次和报告差异，配套补齐分页、动作资格、详情和命令查询契约。 | PayPal 原收款、Stripe 原 Charge 均可完成退款命令恢复。Stripe 待复核已收款进入队列，审批后看到重新核验结果。PayPal 独立冲回、Stripe 直收和 MoR 处理责任清晰。流水到批次到银行凭证可双向联查。 |
| 3 账户和商品全流程 | 接商户新增、凭证轮换、验证、发布、停用和账户策略，接商品双渠道绑定、发布、暂停及退役。按存量验证结果切换旧卡位入口。 | 两渠道沙箱分别通过管理员流程。旧凭证及历史售后在新版本未生效时可继续查证。模板关联保持，商品改价产生新 ID。Finance、Admin、Support 直接调用各类敏感接口的结果与授权矩阵一致。 |

重点回归使用 PayPal、Stripe 直收、Stripe MoR、未知销售模式、正式及沙箱、同环境多账户和存量缺凭证数据。财务操作覆盖请求响应丢失、重登、重复点击、并发版本冲突及两笔同额部分退款。以 1280 px 和 1440 px 桌面视口验证滚动与键盘路径，检查浅深色、操作后的焦点、复制编号、清除筛选与前进后退。报表导入覆盖 PayPal 分卷、Stripe 报表、银行参考号不符、重复导入和状态轮询失败恢复。上线前用授权 Sandbox 和脱敏历史数据验收，正式资金口径与银行入账用财务认可的凭证核对。[项目验收矩阵](payment-domain-architecture-and-implementation.md#7-验收矩阵)

本轮对 CMS 运行了 `npm run lint` 和 `npm run build`，均通过。使用 Chrome 1280×800 和模拟响应验证了 Stripe 对账范围与结算操作不一致、风控详情及焦点、结账决策请求参数，以及退款深链选中正确收款和目标缺失时的回落。模板保存冲突通过前后端请求契约核对。真实商户、真实资金写入、数据库迁移及完整角色端到端仍属于后续实施验收。

## 6. 首批重构实现

- 退款接入服务端队列、状态分页、已退与预留金额和渠道退款凭证。旧链接可定位原收款。指定目标缺失或与渠道、环境、商户条件冲突时阻断提交。确认窗口展示原收款和金额，既有原请求键恢复流程继续使用。 [退款工作台](../cms-front/src/pages/PaymentRefunds.tsx) · [退款查询](../cms-backend/internal/billing/refund_command.go)
- 风控与争议按履约复核、渠道信号、结账决策、争议案件、独立冲回分开查询。后台按商户、案件类型及负责人筛选并返回下一页游标，详情、操作确认和证据留在平台抽屉流程内。 [案件工作台](../cms-front/src/pages/PaymentCases.tsx) · [案件查询](../cms-backend/internal/billing/aftercare_repository.go)
- 对账继承渠道与商户范围，默认流水列保留总额、费用和净额，详情可查完整资金分项。银行匹配从批次带入固定原账户和划拨号。报表、划拨、定期拉取和操作历史都已接入现有服务。 [对账工作台](../cms-front/src/pages/PaymentReconciliation.tsx) · [结算操作](../cms-front/src/pages/SettlementCommands.tsx)
- 四个工作台共用回传中心样式的 Tab 和 CMS 渠道选择，复用平台表格、搜索、选择、状态、分页和抽屉组件。 [公共页面组合](../cms-front/src/components/payments/PaymentWorkspace.tsx)

首批改动聚焦退款、风控、争议与资金对账。交易和账期的全面通用筛选、商品发布角色收口、账户风控策略读表单、跨刷新恢复结算表单和真实渠道验收列入后续批次。

## 7. 第二批重构实现

- 交易与账期共享支付渠道、正式或沙箱、商户筛选。订单商户按已核实的原收款归属，订阅商户按原协议账户或唯一可核实的 PayPal 凭证归属。客服继续按已知编号查询，订单与账期摘要保留现有脱敏范围。 [交易](../cms-front/src/pages/OrdersTab.tsx) · [订阅](../cms-front/src/pages/PaymentCenter.tsx)
- 「商品与定价」归入支付中心，旧地址转入新入口。Finance 可创建、复制、发布沙箱商品，并沿用原有暂停和下线权限。渠道绑定和正式发布由 SuperAdmin 执行，正式发布须填写依据。只有 SuperAdmin 读取所选环境的绑定细节，其余角色按权限查商品及就绪摘要。 [商品页面](../cms-front/src/pages/ProductsPage.tsx) · [商品权限与读写](../cms-backend/internal/billing/products.go)
- 渠道与商户可在 CMS 抽屉读取并编辑账户风控策略。服务端按 SuperAdmin 角色校验，操作留审计，版本冲突时要求刷新读取。 [账户策略](../cms-front/src/components/payments/AftercarePolicyDrawer.tsx) · [策略接口](../cms-backend/internal/billing/aftercare_policy.go)
- 财务命令提交前在当前浏览器标签页按操作者保存原请求键、关联范围和请求摘要。刷新后先查原命令，服务端已受理时直接恢复状态。尚未收到时可重新填入原字段和文件，字段或文件内容变化会阻止提交。浏览器会话不保存报表原文、银行表单值或操作依据。 [结算恢复](../cms-front/src/utils/settlementRecovery.ts) · [原命令查询](../cms-backend/internal/billing/settlement_commands.go)

后端角色、策略并发和原命令归属使用隔离 PostgreSQL 验证。真实 PayPal、Stripe 商户及银行凭证仍按架构文档的渠道验收矩阵执行。

## 8. 第三批重构实现

- 退款可按完整操作号查询历史。队列显示内部操作号、操作人及已取得的渠道退款编号，详情沿用按同渠道、环境和商户锁定原收款的抽屉。 [退款工作台](../cms-front/src/pages/PaymentRefunds.tsx) · [退款队列](../cms-backend/internal/billing/refund_command.go)
- 风控和争议由服务端按处理状态及期限筛选。详情由后端返回当前可用动作，提交时在事务内重新验证。权益冲回和恢复的确认窗口显示订单、原收款、损失金额及证据版本。履约复核批准后和权益处置等待执行时，抽屉自动刷新约一分钟，并支持手动刷新。争议可复制渠道案件编号。 [案件工作台](../cms-front/src/pages/PaymentCases.tsx) · [案件查询](../cms-backend/internal/billing/aftercare_repository.go) · [动作校验](../cms-backend/internal/billing/aftercare_service.go)
- 对账流水支持编号、订单号、类型和 UTC 发生日期筛选，截止日包含该日全部流水。列表、分页和汇总由同一份服务端筛选计算。批次支持按渠道批次号及内部批次号定位。流水与批次详情可通过链接刷新恢复，按原渠道、环境、商户查询。 [对账工作台](../cms-front/src/pages/PaymentReconciliation.tsx) · [对账查询](../cms-backend/internal/billing/reconciliation_repository.go)
- 筛选、表格、分页、弹层使用 CMS 组件。案件游标分页复用平台的导航按钮与当前页样式。报告差异详情沿用列表内抽屉，真实渠道与银行凭证仍需按验收矩阵联调。

## 9. 页面功能补全

- 退款待办与退款记录支持按订单号进行服务端筛选，列表分页沿用当前订单和渠道范围。「查原收款」继续从同一查询栏打开原收款抽屉。
- 风控渠道信号为空时，按渠道、环境和商户范围区分账户未配置、已配置但尚未采集信号、当前筛选无结果。
- 报告差异抽屉可发起补证。Stripe 可填写报表运行号，PayPal 可上传分卷。表单沿用原渠道、环境和商户，服务端验证原导入、源文件和行号，并将来源显示在操作记录。提交超时可恢复同一请求，资金和差异状态仍由报表处理结果决定。

## 10. 默认列表与用户档案联查

- 在授权范围内，交易、订阅、退款、风控、争议和资金对账进入页面即请求列表。编号用于精确定位。退款记录使用顶部 Tab 切换，正式和沙箱记录使用环境选择查询。
- 用户档案提供订单和订阅入口。链接带入所选用户 ID，支付页打开后立即查询，可继续分页。客服沿用按已知订单或用户查询的权限。正式和沙箱对账仍独立查询和汇总。
