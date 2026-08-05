# PRD-QM-TRADE-001：Qiu Market BTC/USDT 虚拟现货交易终端 V1

| 字段 | 值 |
|---|---|
| 状态 | `frozen-for-implementation-on-merge` |
| 产品 | Qiu Market |
| 版本 | Trade Product V1 |
| 基线 | `e9e6e06e9e03b2e9f809f8ad6ab5fef42af6a661` |
| 市场 | `BTC-USDT` |
| 资金边界 | 仅虚拟资金 |
| API 契约 | [`docs/contracts/qm-trade-v1-api.md`](contracts/qm-trade-v1-api.md) |

本 PRD 只定义单市场 BTC/USDT 虚拟现货终端 V1。合并后，范围、接口字段、事件真值、
unknown 语义和非目标即被冻结；任何改变都必须通过独立 PR 更新本文件和 API 契约，
不能由并行实现任务自行扩展。

## 1. 背景与问题

Qiu Market 已经拥有可恢复的虚拟撮合、available/held 双重记账、Maker/Taker 手续费、
PostgreSQL 事件流、快照、Outbox、OAuth、WebSocket cursor 和 submitted/unknown 处理。
当前问题不是交易核心缺失，而是这些能力集中在后端和一个 2000 多行的 `Trade.vue` 中：

- 页面把恢复 epoch、state hash、Outbox、deployment 等运维证据放在交易主流程之前；
- 下单输入没有价格、数量、总额、余额和手续费的完整联动；
- 订单与成交只有平铺列表，无法解释一笔订单为什么冻结、部分成交、收费和解冻；
- 账本投影已经存在，但普通用户看不到自己的资金变化原因；
- 订单与成交只取固定上限，没有稳定 cursor 分页；
- 中英文只覆盖部分区域；
- Admin 虚拟入金和普通交易操作混在同一信息层级。

### V1 可见结果

用户能够在一个 BTC/USDT 虚拟市场中完成并解释以下闭环：

```text
登录
  -> 管理员发放虚拟资金
  -> Limit Maker 挂单
  -> Market Taker 部分成交
  -> 查看成交与 Maker/Taker 手续费
  -> 查看 available / held 与账本变化原因
  -> 撤销剩余订单
  -> 查看来自事件真值的完整订单时间线
  -> 请求超时后沿用原 ID reconcile
  -> 交易服务重启后恢复余额、订单、cursor 与 state hash
```

完成这条纵向链路即完成 V1。功能数量、市场数量和订单类型数量不是 V1 完成标准。

为使“同一 Maker 订单部分成交后撤销 remainder”可确定性验收，E2E harness 使用测试专属
`system:e2e-taker`：它只在显式 `MARKET_TRADING_E2E_FIXTURE=true`、loopback HTTP/gRPC
和一次性测试数据库同时成立时可创建，由测试代码虚拟入金并提交精确数量的 Market 单。
生产配置、共享 Preview 和普通浏览器都不能启用或控制该账户。现有 `system:demo-maker`
继续只提供 Post Only 报价，不承担主动吃单职责。

## 2. 用户角色

### 2.1 访客

- 查看 BTC/USDT 参考价格、真实 venue K 线、虚拟订单簿和公开成交；
- 查看紧凑的 `LIVE / DEGRADED / OFFLINE` 状态；
- 不能查看私有余额、订单、账本或提交写操作。

### 2.2 已登录交易用户

- 当前只允许 GitHub 用户 `qianqiu0404`；
- 查看本人余额、委托、成交、手续费和账本流水；
- 提交 Limit/Market，选择 GTC/IOC/FOK/Post Only，撤销本人订单；
- unknown 时只能使用原 request/client order ID 查询或重放。

### 2.3 虚拟资金管理员

- 为本人或指定虚拟账户发放 BTC/USDT；
- 入口位于 System 的 Trading Admin 区域，不出现在普通下单面板；
- 不具备真实充值、提现、私钥或实盘交易能力。

### 2.4 运维/学习者

- 在 System 查看 recovery epoch、state hash、Outbox checkpoint、deployment、source digest
  和 transport proof；
- Trade 只显示结论和写入被禁止的原因，不复制完整证明。

## 3. 用户主流程

### 3.1 公开浏览

1. 打开 `/trade/BTC-USDT`；
2. 页面独立加载参考价、K 线、订单簿、公开成交和撮合状态；
3. 某个面板失败时保留明确标记的 last-good，其他面板继续工作；
4. 状态条显示最后成功时间和 `LIVE / DEGRADED / OFFLINE`。

### 3.2 登录与虚拟入金

1. 用户通过 GitHub OAuth 登录；
2. 管理员从 System 的 Trading Admin 发起虚拟入金；
3. 请求在发送前保存 operation/request ID；
4. 返回成功后 Trade 刷新余额与账本；
5. 超时则进入 unknown，禁止生成新 ID，先查询再精确重放。

### 3.3 下单、部分成交和撤单

1. 用户选择 Buy/Sell、Limit/Market、TIF/Post Only；
2. 页面根据市场规则校验 tick、step、min quantity 和 min notional；
3. 页面展示预计冻结、预计手续费和预计收到的资产；
4. 命令经权威写门禁进入单市场 runner；
5. 部分成交后，订单详情同时展示成交、费用、余额影响和剩余 held；
6. 用户撤销剩余订单，时间线展示取消和解冻；
7. 所有写超时都使用原身份 reconcile。

### 3.4 重启恢复

1. 交易服务停止时 Trade 切换为 DEGRADED/OFFLINE 并禁用写入；
2. 服务从 snapshot + event tail 恢复并核对 hash、账本、投影和 Outbox；
3. WebSocket 使用最后 cursor 补发，缺口未 reconcile 前不开放写入；
4. 恢复后订单详情、余额、账本流水和时间线与重启前一致。

## 4. 页面信息架构

```text
Trade / BTC-USDT
  ├─ Market header
  │    pair / reference / venue / freshness / 24h context
  ├─ Compact status strip
  │    LIVE|DEGRADED|OFFLINE / last success / write block reason / System link
  ├─ Trading workspace
  │    Kline + virtual fill markers | Order book | Order entry
  ├─ Account summary
  │    available / held
  │    [P1 only, absent in P0] reference valuation / accumulated fees
  └─ Activity tabs
       Open Orders | Order History | Trades | Ledger
            └─ Order detail drawer + authoritative lifecycle timeline

System / Trading
  ├─ Recovery proof and deployment provenance
  ├─ Runner / Outbox / cursor / state hash
  └─ Trading Admin virtual funding
```

### Trade 必须保留

- `LIVE / DEGRADED / OFFLINE`；
- 最后成功时间和数据年龄；
- 写操作为什么被禁止；
- “查看系统详情”入口。

### 必须迁移至 System

- recovery epoch；
- state hash；
- Outbox checkpoint；
- deployment ID / immutable deployment URL；
- release commit / source digest；
- transport proof；
- Admin 虚拟入金。

## 5. 功能需求与优先级

### 5.1 P0：V1 发布门

P0 全部完成才可称为 Trade Product V1：

1. **组件化**：`Trade.vue` 只负责页面编排；行情、订单簿、下单、账户、活动、时间线、
   传输和 pending write 各自进入 feature component/composable。
2. **专业交易布局**：桌面端以 K 线、订单簿、下单为主；账户和活动位于下方；窄屏按
   市场、下单、活动顺序折叠。
3. **中英文完整覆盖**：所有用户可见文本都有 en/zh-CN；不得继续新增硬编码中英文。
4. **下单联动**：价格、数量、Quote Budget、Total、可用余额、预计 held、预计费用和预计
   到账随输入变化。
5. **余额比例**：支持 25%/50%/75%/100%，始终向下对齐 quantity step 或 quote precision，
   不因前端计算产生超额使用。
6. **市场规则校验**：展示并校验 price tick、quantity step、minimum quantity、minimum
   notional、余额和 Post Only/FOK 组合；服务端仍是权威校验者。
7. **订单详情抽屉**：展示原始参数、状态、filled/remaining、held、平均成交价、成交明细、
   Maker/Taker 费率和费用资产。
8. **订单生命周期时间线**：只消费事件真值生成的 order lifecycle projection；展示 accepted、
   rested、partial fill、filled、canceled、rejected、自成交保护、费用和关联余额影响。
9. **账本流水**：只展示当前账户的 available/held 变化、资产、带符号金额、原因、reference、事件
   cursor 和时间；不能泄露其他账户标识或把单账户流水冒充整笔双重记账证明。
10. **cursor 分页**：订单、成交、时间线和账本使用稳定 opaque cursor；不得使用 offset。
11. **Admin 迁移**：虚拟入金从 Trade 移到 System/Trading Admin，保留原 unknown 语义。
12. **Recovery 分离**：Trade 只保留紧凑状态条，完整证明迁入 System。

P0 Account 区只消费现有 balance truth，展示 available/held；P0 的手续费可见性来自每笔
private trade 和 order timeline。P1 未实现前不得渲染账户估值或累计手续费占位模块。

### 5.2 P1：不阻塞 V1 发布的同范围增强

- Cancel All；
- 订单和成交筛选；
- 按资产累计手续费；
- 使用可信参考价的资产参考估值；
- CSV 导出订单与成交；
- 订单簿深度可视化；
- K 线标记本账户虚拟成交。

P1 不能改变 P0 API 或延迟 P0 验收。P1 未完成时必须继续标记为 backlog，不能把 V1
描述成包含这些能力。

## 6. API 数据契约

字段、路径、cursor、错误和 Cancel All 语义以
[`docs/contracts/qm-trade-v1-api.md`](contracts/qm-trade-v1-api.md) 为冻结契约。

V1 新增或升级的主要查询为：

| 能力 | 契约 |
|---|---|
| 订单分页 | `GET /api/v1/trading/orders?cursor=&limit=&scope=` |
| 成交分页 | `GET /api/v1/trading/account/trades?cursor=&limit=` |
| 订单时间线 | `GET /api/v1/trading/orders/{order_id}/events?cursor=&limit=` |
| 账本流水 | `GET /api/v1/trading/ledger/entries?cursor=&limit=&asset=&reason=` |
| 账户摘要 | `GET /api/v1/trading/account/summary`（P1） |
| Cancel All | `POST /api/v1/trading/orders/cancel-all` + 权威批次查询 |

共同约束：

- 所有价格、金额、数量、费率基数和 sequence 仍传十进制字符串；
- cursor 是服务端签发的 opaque base64url 字符串，使用 domain-separated HMAC 校验并绑定
  market/account/filter/sort；
- 修改 filter 后必须丢弃旧 cursor；无效或错账户 cursor 返回 `invalid_cursor`；
- 账户只从服务端 session 获取；客户端 `account_id` 不参与普通查询或写入授权；
- 私有响应 `Cache-Control: no-store`；
- 服务端返回明确 `next_cursor`，空字符串表示没有下一页；
- 事件和账本投影可重建，`trading_event_batch` 仍是最终交易真值。

## 7. 交易与账本不变量

V1 页面和查询能力不能削弱现有核心不变量：

1. available/held 永不为负；
2. 每笔 ledger transaction 的每个资产借贷和为零；
3. open order 的 held 覆盖剩余最坏义务；
4. 同价 FIFO、市场 sequence 和 event index 确定；
5. 同一幂等键和同 payload 返回原结果，不生成第二批事件；
6. 事件先持久化、随后应用内存；投影不是最终真值；
7. 订单时间线的每个权威节点都能回指 `(market_sequence,event_index)`；
8. 账本流水的每个节点都能回指 sequence/transaction/reference；
9. 参考估值只用于展示，不参与撮合、冻结、费用或结算；
10. 页面计算是预览，服务端市场规则和账本结果始终权威。

## 8. Unknown、失败与恢复语义

### 8.1 Submit / Cancel / Fund

- 写请求发送前先持久保存 operation、request ID、账户和完整 payload；
- 网络超时或 502/503/504 且结果可能已提交时进入 `submitted/unknown`；
- UI 锁定新的写操作，先查询权威订单/余额/操作事实；
- 需要重放时使用原 ID 和原 payload；不同 payload 必须冲突；
- 重启浏览器后仍能恢复 pending operation journal；换账户时不得替原账户 reconcile。

### 8.2 Order timeline 与客户端 unknown

权威时间线只来自 event stream projection。浏览器可以在时间线旁展示“客户端在某时刻观察到
超时/已核对”的本地注释，但必须标为 `client_observation`，不能伪装成交易所事件，也不能
写回 authoritative order lifecycle projection。

### 8.3 Cancel All

Cancel All 是 P1，语义固定为：

- 一个 `operation_id` 表示一个批次意图；首次请求在 batch control transaction 中固定当时
  投影可见且符合 filter 的订单 ID 清单；固定清单不锁订单，也不阻止随后成交；
- 每个订单使用由 market/account/operation/order 确定性派生的子 request ID；
- 批次不是“全成或全败”事务，允许部分完成；每个子结果单独记录；
- 订单在取消前全部成交时记录 `already_terminal/filled`；部分成交后取消只释放 remainder；
- 取消过程中发生新成交时，由 MarketRunner 的线性顺序决定最终结果；
- 请求超时后使用原 `operation_id` 查询批次权威状态，不创建新批次；
- 同 operation ID 配不同 filter 返回 `idempotency_conflict`；
- 服务重启后未完成子项可恢复执行，已经终态的子项不重复取消或解冻；
- 完成状态为 `complete`、`partial` 或 `failed`，响应包含逐订单结果；不宣称原子取消。

### 8.4 失败矩阵

| 故障 | Trade 行为 | 恢复 |
|---|---|---|
| 单个只读面板失败 | 保留明确陈旧的 last-good，其他面板继续 | 独立重试该面板 |
| status 超过 10 秒 | 禁止写入，显示原因 | 读取新鲜 status 后开放 |
| WebSocket 断线 | 保留 cursor，进入 polling/reconcile | 新 ticket + cursor 补发 |
| cursor 缺口 | 禁止写入，不跳过 | 权威快照/事件补齐 |
| 写响应未知 | 保存原 ID，锁定写入 | 查询后同 ID 重放 |
| PostgreSQL 不可用 | 私有查询和写入 fail closed | 恢复 DB，重启并校验 |
| snapshot/event/hash 损坏 | OFFLINE/manual review | 从可信备份/事件恢复 |
| 参考价陈旧 | 估值 unavailable，maker 暂停 | 连续新鲜样本后恢复 |

## 9. 中英文要求

- 支持 `en` 与 `zh-CN`，同一功能必须同时提交两种语言；
- 翻译键按 `trade.*`、`system.trading.*` 命名，不在组件中散落 `t(en, zh)` 字面量；
- Limit、Market、GTC、IOC、FOK、Post Only 保留标准英文缩写，并提供本地化解释；
- 金额、日期和相对时间使用 locale formatter；ID、cursor、sequence 不本地化；
- API 错误 code 稳定，前端按 code 翻译，不直接把后端英文 message 当唯一用户文案；
- 单测检查 en/zh-CN key 集完全相同且页面不存在新增硬编码业务文案。

## 10. 非目标

本轮明确不做：

- Stop Limit、Stop Market、OCO 或 Trigger Order 状态机；
- 多市场、Market Registry、ETH/USDT、SOL/USDT；
- PnL、持仓成本、FIFO/移动平均会计；
- 真实充值、提现、私钥、链上结算或真实交易所下单；
- 杠杆、永续、期权、跟单；
- Python 策略、回测、模拟策略框架；
- HFT 延迟目标、分片撮合或商业级高可用声明。

## 11. 验收标准

### 11.1 自动化

- Go build/vet/test/race 通过；
- PostgreSQL 集成测试覆盖 cursor、timeline projection、ledger query、unknown 和恢复；
- 前端 unit/typecheck/build 通过；
- en/zh-CN key parity 与关键页面无硬编码回归通过；
- Playwright 覆盖桌面和窄屏主流程；
- `git diff --check` 通过。

### 11.2 端到端主流程

```text
登录
-> 虚拟入金
-> Limit Maker 挂单
-> Market Taker 部分成交
-> 查看手续费和账本变化
-> 撤销剩余订单
-> 查看完整订单时间线
-> 模拟超时并使用原 ID reconcile
-> 重启交易服务
-> 余额、订单、事件 cursor 和状态哈希一致
```

必须同时断言：

- 订单时间线 cursor 连续且来自 event truth；
- available + held 与权威投影一致；
- 每资产完整账本审计借贷平衡；
- 页面显示的预计值与权威结果差异只来自已说明的 floor/ceil 和实际 Maker/Taker 角色；
- 重启后 pending write、订单详情、账本流水和 cursor 收敛；
- Trade 不再显示完整 recovery/deployment 证明；System 能查看这些证明和 Admin 入金。
- 测试专属 `system:e2e-taker` 只在隔离 loopback E2E harness 出现，生产/Preview capability
  明确为 false。

## 12. 发布与证据等级

1. PRD/API 契约 PR 合并，状态成为 `frozen`；
2. 主 Agent 合并无业务行为的共享契约脚手架；
3. 前端、transport/query、timeline/ledger 从该脚手架的精确 SHA 按互斥所有权并行；
4. 主 Agent 集成共享文件并执行全量 Review；
5. 形成新的 immutable release candidate；
6. 从同一 commit 构建 Protected Preview；
7. 完成 OAuth、V1 E2E、unknown、restart 和浏览器视觉验收；
8. promote 同一个 Vercel deployment，不重新构建；
9. Production 完成 smoke 和外部观察。

证据标签：

- `implemented`：代码存在；
- `build-verified`：自动构建和测试通过；
- `integration-verified`：真实 PostgreSQL/gRPC/HTTP/浏览器交换数据并核对；
- `environment-pending`：Preview、OAuth、Production 或外部网络仍未验证；
- `production-recommendation`：建议，不是当前能力。

V1 仍是虚拟资金学习产品。Production 公开可用不等于真实资金安全或商业级交易所。

## 13. 并行所有权与集成顺序

PRD 合并前不启动实现分支。合并后固定为：

| 角色 | 独占范围 | 禁止修改 |
|---|---|---|
| Frontend | `frontend/src/features/trade/**`、最终由其重写 `Trade.vue`、Trade 专属 copy/tests | Proto、Go、migration、System 共享页面 |
| Transport/API | gRPC/HTTP adapter、session-scoped mapping、分页 transport tests | Proto/generated、共享 DTO/interface、`Trade.vue`、PostgreSQL 查询实现、translation |
| Timeline/Ledger | 新 readmodel/query 文件、order lifecycle projection、ledger query、migration、可靠性测试 | Domain/Proto、HTTP handler、`Trade.vue` |
| Main Agent | PRD/API 契约、Domain event schema、Proto/generated、共享 Go interface、前端共享 DTO、System 集成、最终 wiring、Review、发布 | 不重复实现子任务已交付模块 |

集成顺序：

1. 冻结并合并 PRD/API Schema；
2. 主 Agent 独占创建并合并只含共享 Domain event schema、Go interface、Proto/generated、
   HTTP 字段边界和前端 DTO 的
   contract scaffold；该提交不得包含查询实现、页面行为或 migration；
3. 记录 contract scaffold 的精确 SHA，三个实现任务都从该 SHA 创建分支；
4. Timeline/Ledger 先合入 query implementation；
5. Transport/API 再接入共享 interface；
6. Frontend 最后接真实 API；
7. 主 Agent 处理 System、E2E、文档和发布。

## 14. 关键代码入口

按以下顺序理解 V1：

1. `trading/domain/types.go`：订单、事件、费用和定点数市场规则；
2. `trading/exchange/orders.go`：冻结、撮合、费用、解冻和订单事件；
3. `trading/store/postgres/store.go`：事件真值与现有订单/成交/余额/账本投影；
4. `trading/rpc/proto/trading.proto` 与 `trading/httpapi/api.go`：当前浏览器查询边界；
5. `frontend/src/views/Trade.vue`：当前页面编排、unknown journal 和 cursor reconcile。

## 15. 设计决策、被拒绝方案与成本

1. **先完成单市场产品化。** 拒绝同时做高级订单、多市场和策略；成本是功能数量较少，
   收益是一个可解释、可恢复的完整纵切片。
2. **不做 PnL。** 拒绝在成本法、费用折算和参考时点未定义时展示伪精确收益；成本是账户
   摘要较简单，但避免错误会计语义。
3. **事件时间线来自真值。** 拒绝用当前订单行和成交行猜生命周期；成本是增加可重建投影
   和查询索引，收益是每个节点可审计。
4. **opaque keyset cursor。** 拒绝 offset；成本是 cursor schema 与过滤条件必须绑定，收益是
   新事件持续写入时翻页不跳项、不重复。
5. **Trade/System 分离。** 拒绝把运维证明继续放在交易主流程；成本是 System 增加交易专区，
   收益是用户任务与运维任务各自清晰。
6. **Cancel All 非原子批次。** 拒绝伪装全局原子性；成本是需要批次状态和逐单结果，收益是
   能诚实处理成交/撤单竞态和 unknown。

## 16. 术语

| 术语 | 准确含义 | 大白话 | 项目位置 |
|---|---|---|---|
| order lifecycle projection | 从不可变事件重建的一笔订单时间线 | 把录像按订单号剪成一段，不是凭当前照片猜历史 | 新 readmodel + event batch |
| opaque cursor | 客户端不能修改内部排序键的分页令牌 | 下一页票据，不是第几页数字 | V1 query API |
| reference valuation | 用带来源和时间的参考价估算资产展示价值 | 看盘估值，不是成交或会计成本 | Account summary |
| client observation | 浏览器观察到的超时/重连事实 | “我没收到回执”，不等于交易所没执行 | pending write journal |
| cancel batch | 一组可逐单完成和查询的撤单意图 | 一张任务清单，不是假装所有单同一瞬间取消 | P1 Cancel All |

## 17. Owner 60 秒解释

> Trade Product V1 不重写撮合，而是把已有 BTC/USDT 虚拟交易核心变成一条用户可操作、
> 可解释的纵向链路。用户能登录、虚拟入金、挂单、部分成交、查看费用和 available/held，
> 再撤销余量并从事件真值查看订单时间线和账本影响。所有列表用稳定 cursor；写请求超时
> 继续复用原 ID 查询或重放。参考估值只用于展示，不做 PnL，也不参与结算。Trade 只显示
> LIVE/DEGRADED/OFFLINE 和写禁用原因，state hash、Outbox、deployment 等证明进入 System。
> 本轮不做 Stop/OCO、多市场、真实资金或策略。P0 主流程在真实 PostgreSQL、HTTP、浏览器
> 和重启恢复中通过后，V1 才完成。

## 18. 闭卷自检

1. V1 的唯一完成主流程是什么？
2. 为什么 PnL 不进入 V1？
3. 为什么订单时间线不能由 `trading_order` 和 `trading_trade` 临时拼接？
4. 为什么 cursor 不能使用 offset？
5. 参考估值为什么不能参与撮合和结算？
6. Trade 与 System 分别保留哪些状态？
7. 客户端 unknown 与权威事件有什么区别？
8. Cancel All 为什么不是原子操作？
9. Cancel All 超时后为什么不能换 operation ID？
10. 哪些能力是 P0，哪些只是 P1 backlog？
11. 三个并行实现任务如何避免修改同一共享文件？
12. 什么证据才能把 V1 从 build-verified 升级为 integration-verified？
