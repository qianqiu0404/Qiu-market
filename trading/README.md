# S78 Trading Core V2 Lab

`trading` 是 S78 的第一阶段虚拟交易核心。它在不连接真实资金、真实交易所和现有行情服务的前提下，提供一条可以测试和恢复的现货交易闭环：

```text
虚拟入金
→ 交易前校验
→ available / held 冻结
→ 价格时间优先撮合
→ Maker / Taker 手续费
→ 双重记账清算
→ Versioned Command + Event + Journal Batch
→ 快照与确定性重放
→ MarketRunner 顺序执行与故障恢复
```

当前状态：`persistence-v1-verified`（2026-07-25，独立交易模块范围）。这证明 `trading/**` 的内存与 PostgreSQL 存储、顺序执行、恢复、投影重建和业务语义；尚不证明网络层、S78 主进程集成、真实交易所或生产性能。

## 安全边界

- 仅支持虚拟余额和单个 BTC/USDT 市场。
- 核心和 demo 不读取 `.env`，不需要 API Key，不发送网络请求；PostgreSQL 集成测试只接受显式测试 DSN。
- 不连接真实交易端点，不包含充值、提现或签名。
- 不修改 S78 现有 `crawler`、`database`、`services`、`protobuf`、`frontend`、迁移或根 README。
- PostgreSQL adapter 已通过一次性本地数据库测试，但当前 DDL 仍内嵌在 `trading`，等待 canonical S78 基线确定后再进入正式迁移序列。

## 模块

| 路径 | 职责 |
| --- | --- |
| `domain` | 市场、订单、命令、事件、Trade、整数安全计算 |
| `orderbook` | 有序价格档、同价 FIFO、撮合、FOK 预扫描、撤单 |
| `ledger` | available/held、平台费用账户、系统 treasury、不可变分录 |
| `exchange` | 幂等、严格 sequence、资金冻结、撮合清算、快照恢复 |
| `store` | EventStore/SnapshotStore 接口和并发安全内存实现 |
| `store/postgres` | pgx 事务、stream CAS、事件/快照/outbox、可重建读模型 |
| `runtime` | 每市场单 goroutine、有界队列、背压、未知提交结果恢复、关闭快照 |
| `cmd/demo` | 可运行的虚拟交易与恢复演示 |

## 固定业务语义

### 整数单位

撮合和账本不使用 `float64`。V2 的单位定义为：

- BTC 数量和余额：base atoms，`1 BTC = 100,000,000 atoms`；
- USDT 余额和成交额：quote atoms，`1 USDT = 1,000,000 atoms`；
- 价格：买卖一枚完整 BTC 所需的 USDT atoms，例如 `60,000 USDT = 60,000,000,000`；
- 成交金额：`floor(price × base atoms ÷ 100,000,000)`；
- 买单冻结：同一公式向上取整，成交和撤单后释放舍入余量；
- 乘除使用 `math/bits` 的 128 位中间值，结果仍必须安全落入非负 `int64`。

默认 BTC/USDT 规则：

| 规则 | 值 |
| --- | --- |
| 价格 tick | `0.01 USDT` |
| 数量 step | `0.000001 BTC` |
| 最小数量 | `0.00001 BTC` |
| 最小名义金额 | `5 USDT` |
| Maker / Taker | `10 / 20 bps` |

### 订单类型

- Limit + GTC：可成交部分先成交，剩余进入订单簿。
- Limit + IOC：立即成交，剩余取消。
- Limit + FOK：提交前预扫描；不能全部成交则零成交、零冻结。
- Limit + Post Only：会穿价时直接拒绝。
- Market Buy：使用 quote budget，固定 IOC，不进入订单簿。
- Market Sell：使用 base quantity，固定 IOC，不进入订单簿。

### 自成交保护

第一阶段固定为 `Cancel Taker`：incoming order 碰到同账户 maker 时不生成 Trade，取消 taker 剩余量并释放对应 held。FOK 预扫描也不会把自己的挂单计为可成交流动性。

### 手续费

- resting order 是 Maker，incoming order 是 Taker；
- 买方手续费从收到的 base 扣除；
- 卖方手续费从收到的 quote 扣除；
- 整数除法向下取整；
- 实际费率、费用资产和金额固化在 Trade 事件中。

### 原子执行与恢复

每条新业务请求：

1. 以 `(market, account, operation, request ID)` 检查幂等；
2. 从正式状态构建试算副本；
3. 在副本完成订单簿和账本变更；
4. 校验订单簿、每张开放订单的 held、余额非负和账本平衡；
5. 追加带 schema version 的 Command、Result/Event、Journal Delta、Projection Delta 和 state hash；
6. Event Store 追加成功后才替换正式状态。

恢复时加载最新快照，再重放后续 command。每一步重新生成的 Result、Journal Delta、Projection Delta 和 state hash 都必须等于持久化记录，否则 fail closed。

PostgreSQL adapter 用 `SELECT ... FOR UPDATE` 与 `current_sequence` CAS 保证一个市场流严格递增。事件批次、账本增量、outbox、订单/成交/余额投影和投影 checkpoint 在同一事务提交；`(market_id, account_id, operation, request_id)` 由数据库唯一约束兜底。事件流是最终真值，全部投影都能在一个事务中清空并由事件流重建。

`MarketRunner` 是单市场唯一写入口。队列满时明确返回背压错误；若持久化层返回“提交结果未知”，runner 立即进入 recovering，通过日志恢复后才继续接单。已经进入队列的命令在关闭时会先执行完，再保存最终快照。命令进入队列后即使调用方超时也可能完成，因此客户端必须使用原幂等键重试。

当前“每命令克隆完整状态”和“快照包含完整 journal”是为了让教学版原子性容易审计，不是低延迟生产优化方案。

## 运行

从仓库根目录执行：

```bash
go run ./trading/cmd/demo
```

演示包括：

1. maker 虚拟入金 BTC；
2. taker 虚拟入金 USDT；
3. maker 挂出 0.1 BTC；
4. taker 以更高限价买入 0.07 BTC，按 maker 价格成交；
5. 双边手续费进入平台费用账户；
6. maker 撤销剩余 0.03 BTC；
7. 保存快照并在新 Exchange 实例恢复；
8. 比较恢复前后的确定性状态哈希。

## 验证

```bash
go test ./trading/...
go test -race ./trading/...
go vet ./trading/...
S78_TEST_POSTGRES_DSN='postgres:///disposable_test_database?host=/tmp' \
  go test -v ./trading/store/postgres
go test ./trading/exchange -run '^$' -fuzz FuzzExchange -fuzztime 10s
go test ./trading/orderbook -bench BenchmarkMatch -benchmem
git diff --check
```

2026-07-25 已实际通过：

- `go test ./trading/...`；
- `go test -race ./trading/...`；
- `go vet ./trading/...`；
- 10 秒 fuzz 通过（本机以 `GOMAXPROCS=2` 运行，避免测试运行时过度并行）；
- Apple M4 教学基准三次样本：`5,853–5,865 ns/op`、`14,126 B/op`、`108 allocs/op`。

基准只用于保存当前实现的对照样本；每条命令克隆完整状态，因此不能把该数字当作生产撮合性能。

测试范围：

- 价格时间优先、同价 FIFO、多档与部分成交；
- Limit、Market、IOC、FOK、Post Only；
- 价格改善退款；
- 余额不足、整数溢出、越权撤单；
- client order ID 幂等和并发重试；
- 跨账户、跨操作的作用域幂等；
- Cancel Taker 自成交保护；
- Maker/Taker 双边手续费；
- base/quote 不同精度、向上冻结、向下成交和舍入余量释放；
- 每个 Journal Transaction 按资产平衡；
- 快照、日志重放、状态哈希与篡改检测；
- MarketRunner 队列背压、未知提交结果恢复、排空关闭和最终快照；
- PostgreSQL CAS 冲突、事务内 outbox/投影、跨重启幂等、快照重放与投影重建；
- 随机命令序列后的订单簿和账本不变量。

## 后续接入 S78

MacBook Air 的新版 S78 提交并推送后：

1. 获取新的 canonical commit；
2. 将本分支 rebase 到新版 S78；
3. 先保留 `trading` 纯核心不依赖现有服务；
4. 把已验证的内嵌交易 DDL 移入新的正式迁移；
5. 增加 gRPC、HTTP/WebSocket 和前端 adapter；
6. 行情服务只通过明确端口提供市场配置和参考价格，不反向侵入撮合核心。

若新版 S78 已创建同名 `trading` 模块，应逐提交移植并重新跑全仓验证，不做整目录覆盖。

## 尚未实现

- 多市场 registry 与跨市场资金账户；
- HTTP、gRPC、WebSocket、鉴权和限流；
- 真实行情、K 线和交易所测试网 adapter；
- 止损触发单、杠杆、永续、期权和统一账户；
- 生产级延迟、吞吐、内存和长时间 soak 验收。
