# S78 Trading Core Lab

`trading` 是 S78 的第一阶段虚拟交易核心。它在不连接真实资金、真实交易所和现有行情服务的前提下，提供一条可以测试和恢复的现货交易闭环：

```text
虚拟入金
→ 交易前校验
→ available / held 冻结
→ 价格时间优先撮合
→ Maker / Taker 手续费
→ 双重记账清算
→ Command + Event Batch
→ 快照与确定性重放
```

当前状态：`verified`（2026-07-25，本地教学核心范围）。这只证明 `trading/**` 的专项行为，不证明 PostgreSQL、网络层、真实交易所或生产性能。

## 安全边界

- 仅支持虚拟余额和单个 BTC/USDT 市场。
- 不读取 `.env`，不需要 API Key，不发送网络请求。
- 不连接真实交易端点，不包含充值、提现或签名。
- 不修改 S78 现有 `crawler`、`database`、`services`、`protobuf`、`frontend`、迁移或根 README。
- 当前 Event Store 和 Snapshot Store 仅有内存实现，不宣称具备生产持久性。

## 模块

| 路径 | 职责 |
| --- | --- |
| `domain` | 市场、订单、命令、事件、Trade、整数安全计算 |
| `orderbook` | 有序价格档、同价 FIFO、撮合、FOK 预扫描、撤单 |
| `ledger` | available/held、平台费用账户、系统 treasury、不可变分录 |
| `exchange` | 幂等、严格 sequence、资金冻结、撮合清算、快照恢复 |
| `store` | EventStore/SnapshotStore 接口和并发安全内存实现 |
| `cmd/demo` | 可运行的虚拟交易与恢复演示 |

## 固定业务语义

### 整数单位

撮合和账本不使用 `float64`：

- 价格：一个 base lot 值多少 quote atom；
- 数量：base lots；
- 余额和成交额：asset atoms；
- 所有乘法、加法和手续费计算都检查 `int64` 溢出。

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

1. 检查 request/client order ID；
2. 从正式状态构建试算副本；
3. 在副本完成订单簿和账本变更；
4. 校验订单簿、余额非负和账本平衡；
5. 追加 Command、Result/Event Batch 和 state hash；
6. Event Store 追加成功后才替换正式状态。

恢复时加载最新快照，再重放后续 command。每一步重新生成的 Result 和 state hash 都必须等于持久化记录，否则 fail closed。

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
go test ./trading/exchange -run '^$' -fuzz FuzzExchange -fuzztime 10s
go test ./trading/orderbook -bench BenchmarkMatch -benchmem
git diff --check
```

2026-07-25 已实际通过：

- `go test ./trading/...`；
- `go test -race ./trading/...`；
- `go vet ./trading/...`；
- 10 秒 fuzz 通过，本次未发现失败；
- Apple M4 教学基准样本：`10,570 ns/op`、`13,694 B/op`、`108 allocs/op`。

基准只用于保存当前实现的对照样本；每条命令克隆完整状态，因此不能把该数字当作生产撮合性能。

测试范围：

- 价格时间优先、同价 FIFO、多档与部分成交；
- Limit、Market、IOC、FOK、Post Only；
- 价格改善退款；
- 余额不足、整数溢出、越权撤单；
- client order ID 幂等和并发重试；
- Cancel Taker 自成交保护；
- Maker/Taker 双边手续费；
- 每个 Journal Transaction 按资产平衡；
- 快照、日志重放、状态哈希与篡改检测；
- 随机命令序列后的订单簿和账本不变量。

## 后续接入 S78

MacBook Air 的新版 S78 提交并推送后：

1. 获取新的 canonical commit；
2. 将本分支 rebase 到新版 S78；
3. 先保留 `trading` 纯核心不依赖现有服务；
4. 第二阶段实现 PostgreSQL Event/Snapshot Store；
5. 再增加 gRPC、HTTP/WebSocket 和前端 adapter；
6. 行情服务只通过明确端口提供市场配置和参考价格，不反向侵入撮合核心。

若新版 S78 已创建同名 `trading` 模块，应逐提交移植并重新跑全仓验证，不做整目录覆盖。

## 尚未实现

- PostgreSQL 持久化、outbox 和灾难恢复演练；
- 多市场分片和独立市场 goroutine；
- HTTP、gRPC、WebSocket、鉴权和限流；
- 真实行情、K 线和交易所测试网 adapter；
- 止损触发单、杠杆、永续、期权和统一账户；
- 生产级延迟、吞吐、内存和长时间 soak 验收。
