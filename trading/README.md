# S78 虚拟现货交易纵切片

`trading` 是 S78 的独立 BTC/USDT 虚拟现货系统。它不连接充值、提现、私钥或真实交易 API，目标是把撮合、账本、持久化、接口、鉴权和浏览器终端放进一条可运行、可审计、可恢复的教学链路。

```text
Vue 交易终端
  ├─ REST：登录、查询、下单、撤单、虚拟入金
  └─ WebSocket：按持久化 cursor 续传事件
             ↓
本机 HTTP API（session / CSRF / Origin / 限流）
             ↓
本机 gRPC TradingService
             ↓
单市场 MarketRunner（有界队列、严格顺序、背压）
             ↓
撮合引擎 + 双重记账
             ↓
PostgreSQL（事件流 / 账本 / outbox / 快照 / 可重建投影）
```

当前状态：`standalone-vertical-slice-verified`（2026-07-25）。独立 `trading/**` 已具备网络接口、持久化和浏览器终端；这不等于已经接入 MacBook Air 上尚未交付的 S78 行情与共享前端，也不代表完成生产部署。

## 安全边界

- 仅支持虚拟余额和单个 BTC/USDT 市场。
- gRPC 强制绑定 IP loopback；本地免 OAuth 模式也只允许 loopback。
- 正常登录只允许 GitHub 用户 `qianqiu0404`，并使用 OAuth state、PKCE、HttpOnly/SameSite session cookie、CSRF cookie/header 双提交和 Origin 白名单。
- WebSocket 先换取 30 秒有效的一次性 ticket，再按 `(market_sequence, event_index)` 续传。
- HTTP 层从服务端 session 确定账户；客户端提交的 `account_id` 不参与普通用户授权。
- 公开订单簿和成交可匿名读取，公开成交会清除账户标识。
- 不记录 token、cookie、OAuth secret 或 `.env` 内容。
- PostgreSQL DDL 暂时内嵌于 `trading/store/postgres`；等最终 S78 基线确定后才进入正式迁移序列。

## 模块

| 路径 | 职责 |
| --- | --- |
| `domain` | 市场、订单、命令、事件、Trade 和整数安全计算 |
| `decimal` | API 十进制字符串与整数 atom 的严格互转 |
| `orderbook` | 有序价格档、同价 FIFO、撮合、FOK 预扫描和撤单 |
| `ledger` | available/held、平台费用账户、系统 treasury 和不可变分录 |
| `exchange` | 幂等、sequence、冻结、撮合清算、快照与确定性恢复 |
| `store` | EventStore/SnapshotStore 接口和内存实现 |
| `store/postgres` | pgx 事务、stream CAS、事件/快照/outbox 和读模型 |
| `runtime` | 每市场单 goroutine、有界队列、背压和未知提交恢复 |
| `rpc` | TradingService protobuf、gRPC server 和事件订阅 |
| `auth` | GitHub OAuth、PostgreSQL session、CSRF 和一次性 WS ticket |
| `httpapi` | REST/WebSocket adapter、账户隔离、Origin 校验和写限流 |
| `marketmaker` | `system:demo-maker` 三档 Post Only 报价与陈旧/跳变停机 |
| `cmd/demo` | 内存交易、手续费、撤单和重启恢复演示 |
| `cmd/server` | PostgreSQL + MarketRunner + gRPC + HTTP 独立进程 |
| `web` | Vue 3 虚拟交易终端 |

## 固定业务语义

### 整数单位与市场规则

撮合和账本不使用 `float64`：

- BTC：`1 BTC = 100,000,000 atoms`；
- USDT：`1 USDT = 1,000,000 atoms`；
- 价格：一枚完整 BTC 对应的 USDT atoms，例如 `60,000 USDT = 60,000,000,000`；
- 成交金额：`floor(price × base atoms ÷ 100,000,000)`；
- 买单冻结：同一公式向上取整，成交、价格改善和撤单后释放余量；
- 乘除使用 `math/bits` 的 128 位中间值，并检查最终 `int64` 溢出。

| 规则 | 值 |
| --- | --- |
| 价格 tick | `0.01 USDT` |
| 数量 step | `0.000001 BTC` |
| 最小数量 | `0.00001 BTC` |
| 最小名义金额 | `5 USDT` |
| Maker / Taker | `10 / 20 bps` |

### 订单、费用与幂等

- Limit 支持 GTC、IOC、FOK 和 Post Only；Market 固定 IOC，不能进入订单簿。
- Market Buy 使用 quote budget，Market Sell 使用 base quantity。
- 自成交保护固定为 Cancel Taker。
- 买方手续费从获得的 base 扣除，卖方手续费从获得的 quote 扣除。
- 幂等键为 `(market_id, account_id, operation, request_id)`，数据库唯一约束跨重启兜底。
- 浏览器 API 的价格、数量、余额和 sequence 都是十进制字符串，不把大整数直接交给 JavaScript。

### 原子提交与恢复

每条新命令先在试算状态完成撮合、账本和不变量校验，再将 versioned command、事件、账本增量、outbox、投影增量与 state hash 放进同一 PostgreSQL 事务。事件批次提交成功后才应用正式内存状态。

PostgreSQL 使用 `SELECT ... FOR UPDATE` 和 stream sequence CAS。订单、成交、余额、账本与 checkpoint 是可重建投影，事件流是最终真值。每 100 条命令以及优雅退出时保存快照；启动先校验快照哈希，再重放后续事件。重放生成的结果、账本、投影和哈希任一不一致都会 fail closed。

`MarketRunner` 是单市场唯一写入口。队列满时返回背压错误；存储提交结果不确定时立即停止接单，从事件日志恢复后才重新 ready。请求超时不代表命令未执行，调用方必须使用同一幂等键重试。

当前“每命令克隆完整状态”和“快照包含完整 journal”服务于教学可审计性，不是低延迟生产优化方案。

## 运行

### 纯内存演示

从仓库根目录执行：

```bash
go run ./trading/cmd/demo
```

它会演示虚拟入金、部分成交、双边手续费、撤单、快照、重启恢复和状态哈希一致。

### PostgreSQL、gRPC、HTTP 与 Vue

先准备一个仅用于本地实验的空数据库，然后启动后端：

```bash
createdb s78_trading_lab

S78_TRADING_POSTGRES_DSN='postgresql:///s78_trading_lab' \
S78_TRADING_GRPC_ADDR='127.0.0.1:9094' \
S78_TRADING_HTTP_ADDR='127.0.0.1:18084' \
S78_TRADING_ALLOWED_ORIGINS='http://127.0.0.1:5175' \
S78_TRADING_LOCAL_AUTH='true' \
S78_TRADING_SECURE_COOKIES='false' \
go run ./trading/cmd/server
```

另一个终端启动 Vue：

```bash
cd trading/web
npm ci
S78_TRADING_WEB_PORT='5175' \
S78_TRADING_HTTP_TARGET='http://127.0.0.1:18084' \
npm run dev
```

浏览器访问 `http://127.0.0.1:5175/trade/BTC-USDT`。本地模式必须显式开启且只能绑定 `127.0.0.1`；共享环境应关闭本地模式，改用 GitHub OAuth 环境变量并启用 HTTPS secure cookie。

当前 K 线与参考价区域会明确显示“等待集成”，不会用随机数、静态价格或过期行情兜底。`marketmaker` 已完成逻辑和单测，但要等最终 S78 行情接口确定后才能连接可信参考价并随服务启动。

## 接口

内部 gRPC：

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

浏览器使用 `/api/v1/trading/**` 的 REST 和 WebSocket adapter。公共行情接口匿名可读；余额、订单、写请求、虚拟入金和事件订阅必须登录。

## 验证

完整独立模块门禁：

```bash
make -C trading verify-local
```

该命令会：

1. 检查 Go 格式；
2. 执行 `go test ./...`、`go test -race ./trading/...` 和 `go vet ./trading/...`；
3. 创建并销毁精确命名的一次性 PostgreSQL 数据库，执行 store 与 session 集成测试；
4. 执行 10 秒 exchange fuzz 和 orderbook benchmark；
5. 执行 Vue 单测、类型检查、生产构建和 npm audit；
6. 执行 `git diff --check`。

也可只执行某一组：

```bash
make -C trading verify-go
make -C trading verify-postgres
make -C trading verify-fuzz
make -C trading verify-web
```

测试覆盖定点数舍入与溢出、FIFO、多档与部分成交、全部订单类型、自成交保护、冻结/清算/费用/解冻、幂等、并发背压、故障恢复、CAS、事务回滚、outbox cursor、session/CSRF/Origin/越权、一次性 WS ticket 和浏览器交易流程。

## 等待最终 S78 基线后再做

必须同时确认 MacBook Air claim 已释放、handoff 已记录、最终代码已推送，然后：

1. fetch 最终 `origin/main`，将交易分支 rebase 到 canonical commit；
2. 若新版没有 `trading/**`，保留完整提交；若存在同名模块，逐提交移植；
3. 把交易 DDL 接入正式 migration，并将 HTTP adapter 接入现有服务；
4. 把 Vue 页面移入共享前端路由；
5. 连接 S78 的新鲜参考价与 K 线，启动 `system:demo-maker`；
6. 执行全仓、浏览器、重启恢复和账本对账后，才标记 `integration-verified`。

## 明确不在本目标内

- 真实资金、充值、提现、私钥和真实交易所下单；
- 杠杆、永续、期权、跟单和 Python 策略实验室；
- 多市场 registry、统一账户和生产级低延迟优化；
- 未经最终共享基线验证的远端推送、主分支合并或部署。
