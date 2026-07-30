# Qiu Market 虚拟现货交易系统

本文是 Qiu Market BTC/USDT 虚拟现货纵切片的 canonical 工程文档。它说明已经落地的代码、运行边界、恢复模型和验收方法；业务课程的系统化学习仍以 Obsidian 的《S78交易系统与量化策略开发实战讲义》为主。

## 问题与可见结果

行情系统回答“外部市场现在多少钱”，交易系统回答“谁以什么价格提交了什么意图、资金是否足够、订单如何排队成交、失败后能否恢复到同一个状态”。两者共享可信参考数据和浏览器外壳，但故障域与写入所有权独立。

当前实现提供：

- 单一 `BTC-USDT` 虚拟市场，BTC 精度 `1e8`、USDT 精度 `1e6`；
- Limit、Market、GTC、IOC、FOK、Post Only、部分成交、撤单和 Cancel Taker 自成交保护；
- 价格时间优先订单簿、available/held 余额、不可变双重记账和 Maker/Taker 手续费；
- 每市场单 goroutine、有界队列、严格 sequence、背压和未知提交恢复；
- PostgreSQL 事件流、快照、outbox、订单/成交/余额/账本投影；
- loopback gRPC、共享 HTTP API 下的 REST/WebSocket gateway；
- 单用户会话、CSRF、Origin、限流、一次性 WebSocket ticket；
- 共享 Vue 的 `/trade/BTC-USDT`：可信参考、真实 K 线、订单簿、下单、撤单、余额、订单和成交；
- `system:demo-maker` 根据新鲜 S78 BTC 综合现货参考提供三档虚拟流动性。

明确不包含真实充值、提现、私钥、真实交易所下单或实盘资金。

## 进程与故障域

```text
Browser /trade/BTC-USDT
  ├─ S78 market-data REST：可信 BTC 参考与具体 venue K 线
  └─ /api/v1/trading/**
            │ session / CSRF / Origin / rate limit / WS ticket
            ▼
market-services api :9092
            │ loopback gRPC
            ▼
market-services trading :9094
            │
            ├─ MarketRunner：BTC-USDT 唯一写入口
            ├─ Exchange：撮合、资金冻结、清算、幂等
            ├─ Ledger：不可变借贷分录
            └─ PostgreSQL：event batch + ledger + outbox + projections
```

`api` 不拥有撮合状态，`trading` 不托管浏览器 HTTP。交易进程不可用时，`/api/v1/trading/**` 局部返回 503，原 Markets 页面继续工作；行情来源异常时，撮合与恢复仍可用，只有 demo-maker 撤单停机。

## 定点数与市场规则

内部不使用 `float64` 表示金额：

| 概念 | 整数语义 |
|---|---|
| BTC quantity | `1 BTC = 100,000,000 base atoms` |
| USDT amount | `1 USDT = 1,000,000 quote atoms` |
| Price | 一枚完整 BTC 对应的 USDT atoms |
| Notional | `floor(price × base atoms ÷ 1e8)` |
| Buy hold | 同一公式向上取整 |

乘除使用 `math/bits` 构造 128 位中间值并检查 `int64` 结果。买单按最坏价格向上冻结，实际成交按成交价向下结算；价格改善、舍入余量、取消和 IOC 未成交余量都会释放。

默认规则：

| 规则 | 值 |
|---|---|
| price tick | `0.01 USDT` |
| quantity step | `0.000001 BTC` |
| minimum quantity | `0.00001 BTC` |
| minimum notional | `5 USDT` |
| Maker / Taker fee | `10 / 20 bps` |

## 命令、撮合与账本调用链

1. REST 从服务端 session 取得账户，忽略客户端伪造的 `account_id`。
2. gRPC adapter 将十进制字符串严格转换为整数 atom。
3. `MarketRunner` 把命令放入有界队列；一个 goroutine 串行分配市场 sequence。
4. `Exchange` 先检查 `(market_id, account_id, operation, request_id)` 幂等键和业务规则。
5. 在克隆状态中冻结余额、按价格时间优先撮合、计算费用并生成账本增量。
6. PostgreSQL 在一个显式事务中 CAS stream sequence，并提交 event batch、ledger、outbox 和投影。
7. 事务成功后才把试算状态应用到正式内存；结果不确定时 runner 立即拒绝新命令并从事件流恢复。
8. REST 返回十进制字符串；WebSocket 以 `(market_sequence, event_index)` 作为持久化 cursor。

### 订单语义

- Limit GTC 的剩余量可入簿；IOC 剩余立即取消；FOK 先预扫描，不能全成则不发生部分成交。
- Post Only 若会立即成交则整单拒绝。
- Market Buy 的输入是 quote budget，Market Sell 的输入是 base quantity；市价单从不入簿。
- 同价订单严格 FIFO；跨价位按买价从高到低、卖价从低到高。
- 自成交保护固定为 Cancel Taker，不让虚拟账户与自己的 resting order 成交。
- 买方费用从获得的 BTC 扣除，卖方费用从获得的 USDT 扣除；成交事件固化实际角色和费率。

### 必须一直成立的不变量

- 任何账户的 `available` 和 `held` 都不能为负；
- 每个资产的每笔 ledger transaction 借贷和为零；
- 系统资金只可通过显式虚拟入金进入 treasury 对手账户；
- open order 的 held 金额必须覆盖其剩余最坏义务；
- 一个订单只能在一个价格档出现一次，同价 FIFO 顺序确定；
- 市场 sequence 严格单调，持久化 sequence 与状态 hash 一致；
- 同一幂等键和同一 payload 返回原结果，不生成第二批事件；
- 同一幂等键配不同 payload 必须拒绝。

## 交易可靠性闭环

### A. Submitted / unknown

问题不是“请求失败”，而是服务端事件已经提交、调用方却没有收到响应。此时
fund、submit 和 cancel 都必须保留原请求身份，先读取权威状态或以同一身份重放：

| 操作 | 稳定身份 | 响应丢失后的权威核对 | 必须证明 |
|---|---|---|---|
| fund | `request_id` | 读取余额后以同一 request ID 重放 | 余额只增加一次、event batch 只有一个 |
| submit | `client_order_id` | 按账户读取订单后以同一 client order ID 重放 | 订单/hold 只有一份、返回原 sequence/order ID |
| cancel | `request_id` + 原 `order_id` | 先读取订单终态，再以同一 request ID 重放 | 余量只解冻一次、返回原 cancel 结果 |

`trading/reliability/submitted_unknown_test.go` 在现有 `MarketRunner` 成功提交后
主动丢弃响应，再完成上述查询、同 ID 重放、事件数量、held/available 和冲突
payload 断言。这里拒绝的方案是生成新 ID 自动重试；代价是调用方必须持久保存
原 payload 和身份。该测试复用真实内存 event store，但不经过 PostgreSQL、
HTTP、浏览器或网络代理，因此证据是 `build-verified`，不是环境联调。

2026-07-30 已执行：

```bash
go test ./trading/reliability -run '^TestSubmittedUnknownReusesOriginalIdentity$' -count=1
```

## PostgreSQL 真值与恢复

核心交易表由 `migrations/2026082100023.sql` 创建，发布游标分离由
`migrations/2026082300025.sql` 创建：

| 表 | 作用 |
|---|---|
| `trading_market` | 市场规则与当前 stream sequence |
| `trading_event_batch` | 命令、结果、journal、投影增量和状态 hash；最终真值 |
| `trading_snapshot` | versioned 完整状态与 hash |
| `trading_outbox` | 与命令事务一起写入的待发布事件；不是浏览器长期 replay 表 |
| `trading_event_feed` | WebSocket/polling 使用的持久化 cursor feed |
| `trading_outbox_checkpoint` | publisher 已提交到 feed 的最后 `(sequence,event_index)` |
| `trading_order` / `trading_trade` | 可重建读模型 |
| `trading_balance` / `trading_ledger_entry` | 余额投影与审计分录 |
| `trading_projection_checkpoint` | 投影消费位置 |
| `trading_user_session` | token hash、CSRF hash 与会话过期时间 |

事件批次、账本增量、outbox 和投影在同一 pgx 事务提交。独立 publisher
把 outbox 批次写入 event feed、推进 checkpoint、再在同一 PostgreSQL
事务中标记源行已发布；已发布源行保留 24 小时后小批量清理。数据库唯一约束
兜底跨进程重启幂等，stream sequence 使用行锁和 CAS 阻止双写。

每 100 条命令和优雅退出时保存快照。启动流程是：

1. 读取快照并校验 schema version 与 state hash；
2. 重放快照之后的 event batch；
3. 逐批重新计算命令结果、账本、投影和 hash；
4. 任一损坏或不一致即 fail closed，不带病提供下单；
5. 恢复成功后才把 runner 标为 ready。

订单、成交、余额与 checkpoint 是查询加速投影，可以从事件流重建；不能把投影表当成最终真值。

## 接口与鉴权

内部 `TradingService`：

- `SubmitOrder`
- `CancelOrder`
- `GetOrder`
- `ListOrders`
- `ListTrades`
- `GetBalances`
- `GetOrderBook`
- `GetStatus`
- `AdminFundVirtual`
- `SubscribeEvents`

浏览器接口位于 `/api/v1/trading/**`。订单簿、公共成交、状态和登录能力可匿名读取；账户余额、个人订单/成交、下单、撤单、虚拟入金和事件订阅要求会话。

安全边界：

- gRPC 地址必须是显式 IP loopback，默认 `127.0.0.1:9094`；
- GitHub OAuth 只接受 `qianqiu0404`，包含 state 和 PKCE；
- 本地登录必须显式设置 `MARKET_TRADING_LOCAL_AUTH=true`，并且 HTTP 绑定 loopback；
- session 使用 HttpOnly/SameSite cookie，CSRF 使用 cookie/header 双提交；
- 写请求校验 Origin 并限流；
- WebSocket 先领取 30 秒一次性 ticket，再带 cursor 建连；
- 所有金额、价格、数量和 sequence 都以十进制字符串跨网络。

## 可信参考、K 线与 demo-maker

交易页不生成假行情：

- 参考价读取 S78 `asset_price_index` 中 CoinGecko identity 为 `bitcoin` 的可用综合现货指数；
- K 线从当前 S78 真实 spot market 中选择可用 venue，再调用已有 K 线 API；
- 任一来源缺失时显示明确 unavailable，不回退随机数、硬编码价格或旧缓存。

`system:demo-maker` 只使用虚拟资金，围绕参考价在 `±10/25/50 bps` 各挂一档 Post Only 订单。参考时间超过 30 秒，或相邻参考跳变超过 5%，maker 会先撤销自己的挂单再停止；撮合服务仍保持 ready。每次进程启动使用随机 request prefix，启动资金命令自身仍幂等。

## 运行

先迁移：

```bash
source .env
./market-services migrate
```

日常由启动器运行八个角色：

```bash
make dev
make dev-status
```

手动运行交易后端：

```bash
source .env
./market-services trading
```

另一个终端运行共享 API 和前端：

```bash
source .env
./market-services api
cd frontend && npm run dev
```

浏览器入口：<http://127.0.0.1:5174/trade/BTC-USDT>。

本地不需要 maker 时：

```bash
MARKET_TRADING_DEMO_MAKER_ENABLED=false ./market-services trading
```

### Mac mini 版本化发布

生产发布不能再用 `manage-services.sh reload` 覆盖唯一二进制。正式流程固定为：

```bash
# 只构建到 releases/<git-sha>，不切换线上服务
ops/macos/release-production.sh prepare

# 对同一个 binary SHA 执行 Go、race、vet、前端、SLO fixture 和临时库真实恢复
ops/macos/release-production.sh verify

# 只读查看当前 binary、迁移、feed、checkpoint 与 trading/outbox 状态
ops/macos/release-production.sh status

# 新建 full + trading 备份、用本次 binary 恢复临时库、应用迁移，
# 原子切换 symlink，并且只重启 trading/API
ops/macos/release-production.sh deploy

# 任一上线后门禁失败会自动恢复旧 binary；也可显式执行
ops/macos/release-production.sh rollback
```

每个 release 保存 Git commit、构建时间、binary SHA-256、目标迁移 SHA-256
和完整 migration set SHA-256；
`verify` 的证据也绑定同一个 binary SHA。`deploy` 要求干净工作树、精确 HEAD、
PostgreSQL ready、HMAC 已配置和数据盘至少 `35,000,000,000` 可用字节。
迁移前必须证明所有已应用 checksum 与仓库一致，且 pending set 只能为空或
恰好为 `2026082300025.sql`。
迁移前的 fresh backup 必须通过临时数据库恢复、快照重放、最终 hash 和账本平衡
检查。二进制回滚不逆向删除数据库表；迁移必须保持向后兼容。

第一次切换前，旧 binary 会被捕获为 legacy release。它读取 outbox 作为 cursor
feed，因此只允许在 24 小时兼容窗口内回滚，并且要求 event feed 的每一行仍能在
outbox 找到；源行清理开始后自动拒绝 legacy rollback。新 release 之间都使用
`trading_event_feed`，不受该限制。

`manage-services.sh prepare` 现在只暂存 release，`reload` 只重载 plist 并继续
运行当前 binary，避免无来源覆盖。

## 失败、降级与恢复

| 故障 | 行为 | 恢复 |
|---|---|---|
| gRPC trading 未启动 | trading 路由返回受限 503；行情 API 正常 | 启动 trading，gateway 下次请求恢复 |
| API 停止 | 浏览器 trading/Markets 都显示离线 | 重启 API；撮合进程和事件流不丢 |
| 命令队列满 | 立即返回背压，不偷偷排无限任务 | 客户端复用同一 request ID 重试 |
| 提交结果不确定 | runner 停止接单 | 从 event stream 恢复后重新 ready |
| 快照损坏 | 启动 fail closed | 修复/移除损坏介质后从可信事件恢复，不能忽略 hash |
| 事件损坏 | 启动 fail closed | 从备份恢复事件真值 |
| 参考过期或跳变 | maker 撤单停机 | 新进程在可信参考恢复后再启动 maker |
| PostgreSQL 不可达 | trading 无法启动；市场 API 局部降级 | 恢复数据库并重启 |
| WebSocket 断开 | UI 显示重连，保留 cursor | 领取新 ticket 并从 cursor 补发 |

## 关键代码入口

建议按以下顺序阅读：

1. `trading/domain/market.go` 与 `trading/domain/math.go`：整数单位、市场规则和 checked mulDiv。
2. `trading/orderbook/orderbook.go` 与 `trading/exchange/exchange.go`：FIFO 撮合、冻结、费用、幂等和状态转换。
3. `trading/runtime/runner.go`：单市场串行执行、背压和未知提交恢复。
4. `trading/store/postgres/store.go`：事务 CAS、event/outbox/ledger/投影和快照。
5. `trading/service/backend.go`、`trading/gateway/gateway.go`、`services/http/api.go`：独立后端、loopback gRPC 和共享 API 故障隔离。
6. `trading/httpapi/api.go` 与 `trading/auth`：REST/WebSocket、会话、CSRF、Origin 和 ticket。
7. `frontend/src/views/Trade.vue` 与 `frontend/src/api/trading.ts`：共享交易终端及十进制字符串契约。

## 设计决策、被拒绝方案与代价

1. **事件流是真值。** 被拒绝的是“先改内存/投影，再补事件”；它在进程崩溃时无法判断哪一步生效。代价是每个命令都承担事务和重放数据成本。
2. **一个市场一个串行 runner。** 被拒绝的是多个 goroutine 直接修改订单簿；串行化换来确定性 sequence、FIFO 和简单恢复，代价是单市场吞吐受一个执行器约束。
3. **API 与撮合分进程。** 被拒绝的是把撮合状态塞进现有行情 HTTP 进程；当前方案多一次本机 gRPC hop，但重启与故障域清晰。
4. **整数 atom。** 被拒绝的是跨账本使用浮点；代价是所有边界都必须明确 scale、floor/ceil 和溢出处理。
5. **服务端会话决定账户。** 被拒绝的是信任请求里的 `account_id`；代价是开发环境也必须显式登录。
6. **参考价只驱动 maker，不驱动强制成交。** 被拒绝的是把外部指数当可执行价格；这样行情异常不会凭空改用户订单或资金。
7. **教学正确性优先。** 当前命令会克隆完整状态，快照保留完整 journal，便于审计和恢复验证；生产低延迟版本需要分片、增量结构和容量压测，不能把本切片冒充 HFT 引擎。

## 验证与证据等级

专项命令：

```bash
go test ./...
go test -race ./trading/...
go vet ./...
go test ./trading/exchange -run='^$' -fuzz=FuzzExchange -fuzztime=10s
go test ./trading/orderbook -run='^$' -bench=BenchmarkMatch -benchmem
./trading/scripts/verify-local.sh postgres
ops/macos/release-production.sh verify
ops/macos/release-production.sh status
cd frontend && npm test -- --run && npm run build
cd frontend && npm audit --audit-level=high
cd frontend && npx playwright test
make verify-local
git diff --check
```

证据必须分开描述：

- `implemented`：代码、迁移、接口、共享页面和启动器已经存在；
- `build-verified`：全仓 Go、race/vet/fuzz/benchmark、前端单测/构建/E2E 与 audit 已运行；
- `integration-verified`：一次性 PostgreSQL 中真实 migration + gRPC + REST + session + restart hash E2E 已通过；
- `local-runtime-verified`：2026-07-26 在本机 S78 数据库完成真实参考、reviewed venue K 线、六档 maker、虚拟入金、挂单、成交、费用、撤单和浏览器 WebSocket；优雅退出的 event/snapshot 同为 sequence `619` 且 hash 完全一致，重启后余额与交易记录恢复，BTC/USDT 分资产账本净额均为零；
- `production-pending`：HTTPS/OAuth 实际回调、容量/延迟、备份恢复演练、监控告警和长期 soak 不属于本地完成证据。

## Owner 60 秒解释

> 这个切片只做一个虚拟 BTC/USDT 市场。浏览器请求先经过共享 API 的登录、CSRF、Origin 和限流，再通过本机 9094 gRPC 进入唯一的 MarketRunner。runner 串行执行，所以 sequence、同价 FIFO 和重放结果确定。每条命令先在试算状态里完成撮合、冻结、双重记账和费用，再把事件、账本、outbox 与投影放进同一个 PostgreSQL 事务，提交成功后才更新内存。启动时校验快照并重放事件，hash 不一致就拒绝服务。行情只给页面和虚拟 maker 作参考，过期或跳变会撤单停 maker，不会伪造成交，更不会连接真实资金。

## 闭卷自检

1. 为什么价格是“一枚 BTC 对应的 USDT atoms”，而不是普通 `60000.0`？
2. 为什么买单冻结向上取整、成交金额向下取整？
3. Market Buy 和 Market Sell 的输入为什么不同？
4. FOK 为什么必须在修改状态前预扫描？
5. Post Only 与普通 GTC 的失败边界有什么不同？
6. Cancel Taker 如何避免自成交，又为什么不能取消 maker？
7. 为什么 `(market_id, account_id, operation, request_id)` 四元组才是正确幂等范围？
8. HTTP 超时后为什么必须复用同一 request ID？
9. 为什么 event batch、ledger、outbox 和 projection 必须同事务？
10. 为什么投影表可重建，而 event stream 不能随意重建？
11. 提交结果不确定时为什么 runner 必须停止，而不能继续接单？
12. gRPC 为什么只允许 loopback？
13. 为什么浏览器传来的 `account_id` 必须被忽略？
14. WebSocket ticket 为什么一次性且只有 30 秒？
15. 参考价过期时为什么只停 maker，不停撮合？
16. 为什么真实 K 线缺失时页面必须显示 unavailable？
17. 共享 API 与 trading 分进程带来了什么收益和成本？
18. 当前实现为什么是教学正确性系统，而不是生产 HFT 引擎？
