# Qiu Market 虚拟现货交易系统

本文是 Qiu Market BTC/USDT 虚拟现货纵切片的 canonical 工程文档。它说明已经落地的代码、运行边界、恢复模型和验收方法；业务课程的系统化学习仍以 Obsidian 的《S78交易系统与量化策略开发实战讲义》为主。

面向用户的交易终端产品化范围由
[`PRD-QM-TRADE-001`](prd-qm-trade-001.md) 和其
[`V1 API Schema`](contracts/qm-trade-v1-api.md) 冻结。V1 只覆盖单市场 BTC/USDT
虚拟现货主流程；Stop/OCO、多市场、PnL、真实资金与策略实验室不属于该目标。

## 问题与可见结果

行情系统回答“外部市场现在多少钱”，交易系统回答“谁以什么价格提交了什么意图、资金是否足够、订单如何排队成交、失败后能否恢复到同一个状态”。两者共享可信参考数据和浏览器外壳，但故障域与写入所有权独立。

当前实现提供：

- 单一 `BTC-USDT` 虚拟市场，BTC 精度 `1e8`、USDT 精度 `1e6`；
- Limit、Market、GTC、IOC、FOK、Post Only、部分成交、撤单和 Cancel Taker 自成交保护；
- 价格时间优先订单簿、available/held 余额、不可变双重记账和 Maker/Taker 手续费；
- 每市场单 goroutine、有界队列、严格 sequence、背压和未知提交恢复；
- PostgreSQL 事件流、快照、outbox、订单/成交/余额/账本投影；
- submitted/unknown、成交撤单竞态、断线补发、崩溃恢复和有界随机命令的可复现证明；
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

### 可靠性术语映射

| 术语 | 准确含义 | 直白类比 | 本项目位置 |
|---|---|---|---|
| submitted / unknown | 服务端可能已经提交，但调用方没有拿到确定响应 | 转账按钮超时，不能再换单号转一次，要先查原流水 | `trading/runtime/runner.go`、`trading/reliability/submitted_unknown_test.go` |
| linearization | 并发命令被归入一个确定的先后顺序 | 同一个柜台一次只盖一张章 | `MarketRunner` 的单 goroutine queue |
| reconcile | 用权威持久化事实把本地 checkpoint 追平，缺口未补齐前不可写 | 对账没对平就不能继续记新账 | `trading/reliability/reconcile.go`、`trading/rpc/server/server.go` |
| double-entry | 每笔 transaction 内，每个 asset 的正负 entry 之和必须为零 | BTC 账和 USDT 账分别都要借贷相抵 | `trading/ledger/ledger.go`、`trading/reliability/audit.go` |
| snapshot + tail | 先载入已校验快照，再重放其 sequence 之后的事件 | 从存档点继续播放剩余录像 | `trading/exchange/exchange.go`、`trading/store/postgres/store.go` |

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

### B. Partial fill / cancel race

撤单和成交不会同时修改订单；它们都进入同一个 `MarketRunner` 并被线性化为一个
确定顺序。可靠性证明覆盖三条路径：

1. 部分成交先发生：只清算已成交数量和双边费用，订单保留精确 remainder/held，
   随后的 cancel 只解冻 remainder；
2. 完全成交先发生：maker 已为 filled 且 held 为零，迟到的 cancel 只生成
   `cancel_rejected` 事实，不生成 ledger 分录；
3. fill 与 cancel 并发入队：合法结果只能是“cancel 先、零成交”或“partial fill
   先、再 cancel”，两者最终都必须终态 held 为零且资产/费用一致。

`trading/reliability/partial_fill_cancel_test.go` 对每条路径检查
`filled + remaining = original`、available/held、maker/taker fee、逐笔逐资产
ledger 平衡，并运行 32 次并发起跑。这里拒绝的是让撮合与撤单直接并发修改
订单簿；单 runner 的代价是单市场吞吐受串行执行器限制。测试使用内存 event
store，属于 `build-verified`，不代表 PostgreSQL 锁竞争或浏览器并发联调。

2026-07-30 已执行：

```bash
go test ./trading/reliability \
  -run '^(TestPartialFillThenCancelAccounting|TestFullFillThenLateCancelAccounting|TestConcurrentFillCancelLinearizesWithoutDoubleUnlock)$' \
  -count=1
```

### C. Reconnect / reconcile

只记 `market_sequence` 不足以发现同一命令批次中间丢了一条事件。现在 cursor
checkpoint 同时保存 `(market_sequence,event_index)` 和该 sequence 的权威
`batch_event_count`：

1. PostgreSQL feed 读取会关联对应 `trading_event_batch.result_payload`，带回该批
   总事件数；
2. gRPC reconnect 先核对客户端 cursor 所在批次的 event count；
3. `EventReconciler` 忽略小于等于 checkpoint 的重复 cursor，只把严格后继交给
   原 consumer；
4. 同批 index 跳号或跨过完整 sequence 会返回 `DataLoss`，不静默继续；
5. 断线后仍以最后成功 cursor 分页补发；从 snapshot checkpoint 启动时只重放
   checkpoint 之后的事件；
6. unknown commit 的 event replay 期间，`MarketRunner` 立即返回 unavailable，
   不再把新写排队；恢复成功并追平后才重新接受写入。

这里拒绝的是“只按 sequence 去重”以及“reconcile 时先收下写请求以后再执行”：
前者看不见批内缺口，后者把 unavailable 伪装成可写。代价是 reconnect 时多一次
batch metadata 查询，feed 查询也必须关联不可变 event batch。cursor gate 不保存
订单、余额或账本，因此没有形成第二套交易状态。

`trading/reliability/reconcile_test.go` 覆盖重复、批内/跨批缺口、多次断线分页和
snapshot 后重放；`trading/rpc/server/reconcile_stream_test.go` 覆盖 gRPC 去重与
gap fail-closed；`trading/runtime/reconcile_gate_test.go` 覆盖恢复未完成不可写。
这些是 `build-verified`。PostgreSQL 集成断言已加入，但本次没有
`S78_TEST_POSTGRES_DSN`，因此真实 feed join/reconnect 仍是 `environment-pending`。

2026-07-30 已执行：

```bash
go test ./trading/reliability -run '^TestEventReconciler' -count=1
go test ./trading/runtime -run '^TestMarketRunnerRejectsWritesUntilReconcileCompletes$' -count=1
go test ./trading/rpc/server -run '^TestTradingGRPCSubscribeEvents' -count=1
```

### D. 双重记账与崩溃恢复

`trading/reliability.AuditRecords` 只读完整 immutable event history，不保存订单、
余额或撮合状态。它逐 sequence 检查：

1. command/result sequence 与 event `(sequence,index)` 连续；
2. transaction ID 在全历史唯一；
3. 每个 transaction 的每个 asset 借贷和为零；
4. 把全部 journal entry 再累加后，BTC、USDT 等每个 asset 的净变化仍为零；
5. 每批 state hash 都是 32-byte SHA-256。

`ProveRecovery` 先生成上述账本证明，再调用现有 `exchange.Restore`，要求恢复后的
sequence/hash 与最后一个持久化 record 完全相同。它没有实现第二本账或第二套
reducer；被拒绝的是只检查最终余额总数，因为那会漏掉某一笔内部不平的分录。
代价是完整证明必须扫描 immutable journal，适合启动验收、恢复演练和离线审计，
不放在每个低延迟请求的热路径上。

故障注入覆盖“event append 已成功、内存 trial 尚未应用时进程崩溃”：旧进程内存
仍为 sequence 0，而持久化已为 sequence 1；新进程从 event 恢复后，同一 request
ID 只返回原结果且不会再记账。损坏 snapshot hash、损坏 event journal 都会
fail closed。相同命令分别走 live、snapshot+tail、event-only 和独立重跑，最终
hash 必须一致。

2026-07-30 专项命令：

```bash
go test ./trading/reliability \
  -run '^(TestLedgerAndRecoveryProof|TestCommitThenCrashBeforeApplyRecoversExactlyOnce|TestCorruptSnapshotAndEventAreRejected|TestFinalStateHashDeterministicAcrossRecoveryPaths)$' \
  -count=1
```

### E. 有界随机命令、并发与复现

`TestBoundedRandomCommandSequence` 默认用 seed `20260730` 执行 192 步，硬上限
2048 步。命令只经过现有 `exchange.Exchange`：虚拟 fund、limit/market submit、
cancel、同 ID replay、snapshot 和 restore。每 8 步审计完整 journal，每 17 步
强制恢复并核对 hash，每 31 步保存 snapshot；失败信息总是带 seed、step 和
command。指定 seed 可闭环复现：

```bash
go test ./trading/reliability \
  -run '^TestBoundedRandomCommandSequence$' -count=1 \
  -args -reliability.seed=20260730 -reliability.steps=192
```

`TestConcurrentSameIDAndUniqueCommands` 让 32 个 goroutine 同时重放一个 fund ID，
另 32 个提交不同 ID。前者只能形成一个 sequence/ledger transaction，后者必须
各有唯一 sequence；最终余额、record count 和恢复证明完全相等。race detector
用于证明实现没有数据竞争，不把某次 goroutine 调度顺序写成业务保证。

fuzz 的每个输入最多 64 步，benchmark 固定 96 步历史，因此两者都有明确边界：

```bash
GOMAXPROCS=2 go test ./trading/reliability \
  -run '^$' -fuzz '^FuzzBoundedCommandRecovery$' -fuzztime=10s
go test ./trading/reliability \
  -run '^$' -bench '^BenchmarkAuditAndRestore$' -benchtime=100x -benchmem
```

这里拒绝无上限随机跑和只打印随机失败、不记录 seed 的做法。代价是 bounded
property test 只能证明覆盖到的状态空间；长期 soak、跨进程 PostgreSQL 并发和
浏览器断网仍需单独环境验收。

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

## Recovery Coordinator 与写入开放

`MarketRunner=ready` 只证明撮合内存完成 snapshot + tail 恢复，不能单独证明
projection、outbox、指定 HTTPS endpoint 或浏览器 cursor 都已追平。为此新增持久化 Recovery
Coordinator。启用后可见结果是：Fund、Submit、Cancel 在 runner 唯一命令入口
统一 fail closed；HTTP gateway 返回 503 `recovery_in_progress`；订单簿、状态、
订单、成交和余额等只读查询继续可用。

```text
bootstrap -> dependencies_ready -> trading_replay -> reconciling
          -> read_only -> transport_warmup -> writable
                         \-> offline / manual_review
```

`trading_recovery_epoch` 保存 phase、proof 摘要、连续 transport 样本摘要、错误和 CAS version，
`trading_recovery_current` 原子选择当前 epoch。它们只是写入准入控制平面，不保存
订单、余额、账本或撮合状态；`trading_event_batch` 仍是最终真值。启动新 epoch 会
插入新的历史行并以事务切换 current，旧 version 不能覆盖新 epoch。

本地调用链依次为：`trading/recovery/coordinator.go` 定义 phase 与 proof；
`postgres_store.go` 持久化 epoch；`trading/service/backend.go` 启动 epoch 并复用
现有 restore/audit；`trading/runtime/runner.go` 在 Fund/Submit/Cancel 前执行权威
门禁；`trading/operator/transport.go` 连续核对运维者指定的 HTTPS REST、权威 recovery gRPC、runner
sequence/hash 和 outbox；`cmd/market-services/trading_recovery.go` 提供 loopback-only
status/promote；`trading/gateway/gateway.go` 提供友好 503 和只读 recovery status。

选择 runner 门禁而不是只禁用 Vue 按钮，是因为 loopback gRPC 和 demo-maker 也
必须服从同一边界。拒绝用固定 sleep 自动开放，因为时间经过不是账本、cursor 或
传输健康证明。每条命令入队时记录 recovery market/epoch/version/phase 准入票据，
到达单写者时重新读取权威 recovery row；票据陈旧就返回 `recovery_in_progress`，不得
进入 Exchange。已经通过二次校验并开始提交 event batch 的命令按 existing
submitted/unknown 语义完成或恢复，phase 回退不会假装撤销一个结果未知的提交；其后
仍在队列中的旧票据命令全部失败关闭。代价是启用时增加一次完整 immutable history
审计；隔离本地 PostgreSQL 的 CAS/fault 已验证，Mac mini production PostgreSQL 上的
故障注入仍需验证 recovery row 与 event transaction 的跨事务边界。

operator 必须先从 `trading-recovery status` 抄下精确 market、epoch、version、runtime
sequence 和 state hash。`promote` 至少采集 7 个连续健康样本、覆盖 30 秒，任意间隔
不得超过 8 秒；每个样本同时校验指定 HTTPS endpoint 的 JSON 正文、交易进程中的权威 recovery 状态、
runner ready/hash/sequence/空队列和 outbox checkpoint，而不是只看 HTTP 200 或固定
sleep。样本摘要和 SHA-256 随 CAS 写入；最终 promote 通过交易进程自身的 loopback
gRPC 执行，CLI 禁止直接 SQL。任一绑定变化、样本失败或 CAS 冲突都不自动重试。
operator 将 status URL 锁定到 epoch 的 Production origin，并逐样本要求 Vercel BFF
返回 VERIFIED、immutable deployment ID/URL 和 exact release commit；公开 JSON、权威
gRPC 与最终 CAS 还必须匹配 backend source digest。它仍不证明浏览器 cursor 已追平；Trade 页面
仍须在 cursor reconcile 完成前独立 fail closed。
该 operator gRPC 只绑定显式 IP loopback，信任边界是本机 `xiuqiu` 用户、受管 release
目录和 LaunchDaemon 配置；浏览器与公网无法直接调用，因此当前不另设一套共享 secret。
若未来监听非 loopback、引入多用户运维或远程控制，必须先增加 mTLS 或等价的强认证。

失败语义固定为：recovery row 缺失、数据库失败或 proof 不完整时关闭写入；
snapshot/event/hash/ledger/projection 不一致进入 `manual_review`；outbox 未追平停在
`reconciling`；本地证明完成只到 `transport_warmup`。Coordinator 一旦观察到 recovery
store 读写连续性不确定，会对同一 epoch 粘性关闭写入；数据库恢复也不会重新放行，
只能由新进程 `Begin` 新 epoch 后重新证明。Demo Maker 的旧挂单在恢复期通过 runner
的专用 safety-cancel 逐笔撤销，只有 writable 后才启动；phase 进入 offline/manual
review 或 runner/outbox 退化时先停 maker，再沿同一顺序写入口撤单。它没有第二条撮合
写路径。旧挂单必须在进入 `transport_warmup` 前撤完；该 phase 内普通写、bootstrap
和 safety-cancel 全部被拒绝，从而保证连续样本绑定的 sequence/hash 不会被内部改写。

受管命令已经 `implemented / build-verified`；隔离本地 PostgreSQL + loopback gRPC 的
30 秒 TransportProbe 已达到 `integration-verified`。但开关仍默认 false，Mac mini
production PostgreSQL/epoch、实际外部 HTTPS provenance、断电
注入和生产启用仍是 `environment-pending`，所以不得
把本切片称为 production-verified。

```bash
market-services trading-recovery status --grpc-address 127.0.0.1:9094
market-services trading-recovery promote \
  --grpc-address 127.0.0.1:9094 \
  --status-url https://qiu-market.vercel.app/api/v1/trading/recovery/status \
  --production-origin https://qiu-market.vercel.app \
  --deployment-id 'dpl_<exact promoted deployment id>' \
  --deployment-url 'https://<immutable-deployment>.vercel.app' \
  --release-commit '<exact 40-char release commit>' \
  --source-digest '<source_digest copied from current recovery status>' \
  --epoch '<status epoch>' --version '<status version>' \
  --runtime-sequence '<status sequence>' --state-hash '<status hash>'
```

术语：admission gate 是命令进入唯一 writer 前的准入判断；recovery epoch 是一次
启动恢复的持久化验收身份；control plane 是决定能否写的红绿灯，不是交易事实；
CAS 表示只有持有预期旧 version 的 actor 才能更新。

60 秒复述：runner ready 只证明撮合恢复，不代表指定 HTTPS endpoint、Production 来源或
浏览器 cursor 可用。Coordinator 从
关闭写入开始，审计事件、账本、hash、projection 和 outbox 后停在 transport warmup。
operator 绑定精确 epoch/version/sequence/hash，连续 30 秒同时检查指定 HTTPS endpoint
正文、权威 gRPC、runner 和 outbox，再让交易进程自身做一次 CAS promote；CLI 不碰 SQL。
这不代替浏览器 cursor reconcile。恢复库一旦断过，
同一 epoch 永久熔断，必须新 epoch 重证。Demo Maker 只在 writable 后启动，退化时经
runner safety-cancel 撤单。当前默认仍关闭，等待 Mac mini production PostgreSQL/epoch、
实际外部 HTTPS、Production 来源绑定和断电验收。

闭卷自检：为什么 ready 不能直接开放？为什么门禁不能只放在 Vue？recovery epoch
为什么不是第二套交易状态？为什么本地证明后只到 transport_warmup？新 epoch 如何
阻止旧 version 覆盖 current？为什么 promote 必须由 trading 进程执行而不是 CLI 直写
数据库？为什么 store 恢复后旧 writable epoch 仍不能自动恢复？safety-cancel 为什么
不是绕过 runner？

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
- `GetRecoveryStatus`（loopback operator）
- `PromoteRecovery`（loopback operator，精确 binding + transport evidence）
- `AdminFundVirtual`
- `SubscribeEvents`

浏览器接口位于 `/api/v1/trading/**`。订单簿、公共成交、状态和登录能力可匿名读取；账户余额、个人订单/成交、下单、撤单、虚拟入金和事件订阅要求会话。

Trade Product V1 的私有查询包括订单、账户成交、账本流水和事件真值订单时间线。
分页 cursor 绑定 market、session account、查询类型、筛选条件和不可变排序键；HTTP 层不会把
客户端 `account_id` 当成授权输入，也不会把私有订单的账户标识回传给浏览器。cursor 使用
`MARKET_TRADING_CURSOR_HMAC_CURRENT=key_id:base64url-secret` 签发，可在有界轮换窗口内用
`MARKET_TRADING_CURSOR_HMAC_PREVIOUS` 验证旧 cursor。current 缺失、格式错误、解码后少于
32 字节，或 current/previous key ID 相同，交易进程都会在启动时 fail closed；不会生成
重启即失效的临时 key。

订单 lifecycle 是从 event stream 重建的读模型。checkpoint 除 stream sequence 外还记录
`row_count`；每个在线 append 和每批最多 500 条的历史回填，都在同一 PostgreSQL 事务中
原子 CAS 两者。启动时即使 checkpoint 已等于 stream head，也会核对实际行数与最大 sequence；
缺行、多行、孤儿行或 checkpoint 越界都会拒绝启动。安全修复是同时清空该可重建 lifecycle
投影和 checkpoint，再由事件真值重放；只改 checkpoint 会掩盖损坏，因此被拒绝。row count
只能发现基数变化，不能认证“相同行数但 payload 被原位篡改”；内容 digest 是独立后续迁移，
当前不能宣称已实现。

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

生产发布不能再用 `manage-services.sh reload` 覆盖唯一二进制，也不能分别手工切换
binary 与 runtime bundle。正式流程由同 SHA 协调器固定为：

```bash
# 必须在干净、且 HEAD 等于目标 SHA 的工作树执行；只生成不可变产物
bash ops/macos/manage-release-candidate.sh prepare <40-character-git-sha>

# 对同一 SHA 的 binary/runtime 做测试，并用真实备份创建临时恢复库
bash ops/macos/manage-release-candidate.sh verify <40-character-git-sha>

# 只读检查生产依赖、迁移 ledger、候选与旧回退点；不改数据库和服务
bash ops/macos/manage-release-candidate.sh preflight <40-character-git-sha>

# 唯一有生产副作用的激活入口；必须由操作者再次写出 --execute
bash ops/macos/manage-release-candidate.sh activate <40-character-git-sha> --execute

# 使用激活前记录的精确 binary/runtime 回退点
bash ops/macos/manage-release-candidate.sh rollback --execute
```

`prepare` 不读取 `production.env`，从目标 Git object 的 archive 编译，不从可能变化的
工作目录取源码。每个 release 保存 Git commit、构建时间、binary SHA-256、完整
migration set SHA-256、迁移数量与最后一个文件；完整 migrations 目录随 release 冻结。
runtime bundle 另有全包 SHA-256，协调器要求两份 manifest 的 Git commit 完全相同。
`verify` 的证据同时绑定 binary 与 migration-set SHA。激活要求干净工作树、精确 HEAD、
PostgreSQL ready、HMAC 已配置和数据盘至少 `35,000,000,000` 可用字节。
迁移 ledger 中已应用文件必须是候选 migration set 的 checksum 完全一致前缀；允许
零个或多个连续尾部迁移待执行，但拒绝未知文件、checksum 漂移与中间缺口。这样新增
后续迁移不需要再把脚本硬编码到旧 `0025`，又不会把任意迁移集合带入生产。
迁移前的 fresh backup 必须通过临时数据库恢复、快照重放、最终 hash 和账本平衡
检查。二进制回滚不逆向删除数据库表；迁移必须保持向后兼容。

这里的“原子”是两个 symlink 各自原子替换，加上跨步骤失败补偿，不是假装整个 Mac
上所有 launchd 进程能在同一个 CPU 指令内切换。协调器先记录可验证的旧 binary 和旧
runtime；binary 发布失败由原脚本自动恢复，runtime 激活失败则恢复完整旧 plist 集，
随后把 binary 补偿回旧目标。若补偿本身失败，状态明确写为
`compensation-failed` 并停止，不继续尝试数据库逆迁移。

被拒绝的替代方案有两个：直接从脏工作树构建会让 commit 与真实代码不一致；只记录
“最后一个 migration”会漏掉前面被修改或插入的 SQL。当前方案的代价是 release 多保存
一份 migrations，完整 `verify` 会创建和删除临时数据库并执行真实恢复，所以它不是
纯 dry-run。只有 `prepare`、`artifact/verify bundle` 和 `preflight` 不切换生产服务；
`activate/rollback --execute` 才拥有服务、备份和生产迁移副作用。
`--execute` 本身仍不足以授权子脚本：协调器持锁后为每一个 deploy、runtime activate
或 rollback 生成 120 秒的一次性随机上下文，绑定操作、精确 commit/target、协调器 PID
与私有 `0600` 状态文件。子脚本要求调用者正是该父进程并在任何生产修改前消费 nonce；
缺文件、旧布尔变量、过期 token、错操作或错目标都会 fail closed。这个边界防止脚本被
误绕过，信任模型仍是 Mac mini 上的同一受管本地用户，不把它冒充跨用户密码学隔离。

控制流按以下入口阅读：

1. `ops/macos/manage-release-candidate.sh`：绑定同 SHA 候选、检查回退点并编排补偿。
2. `ops/macos/release-production.sh`：从 Git archive 构建 binary、冻结 migration set、备份恢复、迁移与 binary symlink。
3. `ops/macos/manage-runtime-release.sh`：归档运行脚本、全包验 hash、精确恢复旧 plist 集。
4. `ops/macos/backup-production.sh` 与 `restore-drill.sh`：生成 SHA-256 备份，并在临时库真实启动交易恢复、核对 hash/ledger/outbox。

术语说明：immutable release 是“放进带 SHA 编号且校验后不再修改的封箱包裹”，位于
`Application Support/Qiu Market/releases/<commit>`；migration ledger 是数据库的“已盖章
施工记录”，表为 `s78_schema_migrations`；compensating rollback 是“后一步失败后按已记录
旧目标执行反向业务步骤”，它恢复程序与脚本，但不删除向后兼容的数据库结构。

第一次切换前，旧 binary 会被捕获为 legacy release。它读取 outbox 作为 cursor
feed，因此只允许在 24 小时兼容窗口内回滚，并且要求 event feed 的每一行仍能在
outbox 找到；源行清理开始后自动拒绝 legacy rollback。新 release 之间都使用
`trading_event_feed`，不受该限制。

`manage-services.sh prepare` 只暂存单一 binary；正式候选必须走协调器同时准备 runtime。
`reload` 只重载 plist 并继续
运行当前 binary，避免无来源覆盖。

## 失败、降级与恢复

| 故障 | 行为 | 恢复 |
|---|---|---|
| gRPC trading 未启动 | trading 路由返回受限 503；行情 API 正常 | 启动 trading，gateway 下次请求恢复 |
| API 停止 | 浏览器 trading/Markets 都显示离线 | 重启 API；撮合进程和事件流不丢 |
| 命令队列满 | 立即返回背压，不偷偷排无限任务 | 客户端复用同一 request ID 重试 |
| 提交结果不确定 | runner 停止接单 | 从 event stream 恢复后重新 ready |
| reconcile 尚未追平 | 新写立即返回 unavailable，不进入待执行队列 | 补齐权威事件并验证 checkpoint/head 后开放 |
| 事件重复 | 小于等于 checkpoint 的 cursor 被去重 | 从最后成功 cursor 继续，不重复应用 |
| 事件缺口 | gRPC 返回 `DataLoss`，不跳过缺口 | 重新读取权威 batch metadata/feed 或从可信 checkpoint 重建 |
| event 已提交、内存未应用即崩溃 | 旧内存被进程退出丢弃 | 新进程从 event 恢复；原 ID 重放只返回原结果 |
| 快照损坏 | 启动 fail closed | 修复/移除损坏介质后从可信事件恢复，不能忽略 hash |
| 事件损坏 | 启动 fail closed | 从备份恢复事件真值 |
| 参考过期或跳变 | maker 撤单停机 | 新进程在可信参考恢复后再启动 maker |
| PostgreSQL 不可达 | trading 无法启动；市场 API 局部降级 | 恢复数据库并重启 |
| WebSocket 断开 | UI 显示重连，保留 cursor | 领取新 ticket 并从 cursor 补发 |

## 关键代码入口

建议按以下顺序阅读：

1. `trading/domain/types.go`：先看整数 atom、市场规则、命令身份、订单/事件类型和 checked mulDiv。
2. `trading/exchange/exchange.go`：再看同 ID 判定、trial state、持久化先于内存应用和 `Restore`；具体撮合/结算进入同目录 `orders.go`。
3. `trading/runtime/runner.go`：然后看单市场串行化、背压、unknown commit 后关写和恢复门禁。
4. `trading/store/postgres/store.go`：接着看 sequence CAS 以及 event、journal、outbox、投影和快照的 PostgreSQL 边界。
5. `trading/reliability/reconcile.go` 与 `audit.go`：最后看 cursor 连续性和账本/恢复证明；它们只审计前四步的真值，不复制撮合状态。

## 设计决策、被拒绝方案与代价

1. **事件流是真值。** 被拒绝的是“先改内存/投影，再补事件”；它在进程崩溃时无法判断哪一步生效。代价是每个命令都承担事务和重放数据成本。
2. **一个市场一个串行 runner。** 被拒绝的是多个 goroutine 直接修改订单簿；串行化换来确定性 sequence、FIFO 和简单恢复，代价是单市场吞吐受一个执行器约束。
3. **API 与撮合分进程。** 被拒绝的是把撮合状态塞进现有行情 HTTP 进程；当前方案多一次本机 gRPC hop，但重启与故障域清晰。
4. **整数 atom。** 被拒绝的是跨账本使用浮点；代价是所有边界都必须明确 scale、floor/ceil 和溢出处理。
5. **服务端会话决定账户。** 被拒绝的是信任请求里的 `account_id`；代价是开发环境也必须显式登录。
6. **参考价只驱动 maker，不驱动强制成交。** 被拒绝的是把外部指数当可执行价格；这样行情异常不会凭空改用户订单或资金。
7. **教学正确性优先。** 当前命令会克隆完整状态；schema 5 快照只保存压缩后的逐资产余额 checkpoint，完整 journal 仍保留在 immutable event history 并由 `AuditRecords` 扫描。生产低延迟版本需要分片、增量结构和容量压测，不能把本切片冒充 HFT 引擎。

## 验证与证据等级

### 单笔限价买单 golden path

`make verify-trading-golden` 是不依赖 `.env` 或共享数据库的最小真实浏览器竖切。
它启动 loopback-only 内存事件存储、真实 `MarketRunner`/撮合/账本、loopback gRPC、
production trading HTTP handler 和 Vue/Vite。只有确定性市场读数据与成交触发属于
测试控制面；买方登录、CSRF、下单及订单/成交/余额/账本查询均走现有
`/api/v1/trading/**`。

固定状态机与金额如下：

| 阶段 | 订单与资金证据 |
| --- | --- |
| 初始 | buyer `1000 USDT available / 0 held`；seller `0.01 BTC` |
| open | limit buy `60000 × 0.01 BTC`；buyer `400 USDT available / 600 held` |
| replay | 相同 `client_order_id` 和 payload 返回相同结果；sequence、事实、成交和 ledger 数量不变 |
| filled | buyer maker fee `0.00001 BTC`，最终 `0.00999 BTC + 400 USDT`；seller taker fee `1.2 USDT`，最终 `598.8 USDT`；held 全部归零 |

BTC scale 固定 `1e8`、USDT scale 固定 `1e6`；API 只接收精确十进制字符串。成交额与
手续费使用整数 floor，买单 quote hold 使用整数 ceil，禁止用浮点数表示资金。最终
immutable journal 必须逐资产和为零，订单、成交和 ledger 引用必须一致。该门禁只证明
单笔完全成交的隔离 golden path，不覆盖 PostgreSQL 恢复、部分成交、撤单、并发或真实
行情保护。

专项命令：

```bash
go test ./...
go test -race ./trading/...
go vet ./...
go test ./trading/exchange -run='^$' -fuzz=FuzzExchange -fuzztime=10s
go test ./trading/orderbook -run='^$' -bench=BenchmarkMatch -benchmem
GOMAXPROCS=2 go test ./trading/reliability -run='^$' -fuzz=FuzzBoundedCommandRecovery -fuzztime=10s
go test ./trading/reliability -run='^$' -bench=BenchmarkAuditAndRestore -benchtime=100x -benchmem
./trading/scripts/verify-local.sh postgres
ops/macos/manage-release-candidate.sh verify <40-character-git-sha>
ops/macos/manage-release-candidate.sh status
cd frontend && npm test -- --run && npm run build
cd frontend && npm audit --audit-level=high
cd frontend && npx playwright test
make verify-local
git diff --check
```

证据必须按能力分开描述，不能把核心交易证据外溢到 Recovery Admission：

Core virtual trading：

- `implemented`：撮合、账本、事件/快照、接口、共享页面和启动器已经存在；
- `build-verified`：全仓 Go、race/vet/fuzz/benchmark、前端单测/构建/E2E 与 audit 已运行；
- `integration-verified`：历史的一次性 PostgreSQL 中真实 migration + gRPC + REST + session + restart hash E2E 已通过；
- `local-runtime-verified`：2026-07-26 在本机 S78 数据库完成真实参考、reviewed venue K 线、六档 maker、虚拟入金、挂单、成交、费用、撤单和浏览器 WebSocket；优雅退出的 event/snapshot 同为 sequence `619` 且 hash 完全一致，重启后余额与交易记录恢复，BTC/USDT 分资产账本净额均为零；
- `production-pending`：HTTPS/OAuth 实际回调、容量/延迟、备份恢复演练、监控告警和长期 soak 不属于上述核心证据。

Recovery admission：

- `implemented / build-verified`：Coordinator、runner/gateway 权威门禁、operator、连续性熔断、前端 fail-closed 与 mock 回归已落地并通过构建级验证；
- `integration-verified (isolated local PostgreSQL + loopback gRPC)`：随机隔离数据库中的 migration/CAS/fault 测试与真实 loopback gRPC TransportProbe 30 秒集成已通过；
- `activation-pending / environment-pending`：Mac mini production PostgreSQL/epoch、实际外部 HTTPS、Production origin/provenance 绑定、断电故障注入和生产启用尚未完成；
- mock 前端、单元测试和既有核心交易 PostgreSQL 证据都不能升级为 Recovery 的 `integration-verified` 或 Production 结论。

## Owner 60 秒解释

> 这个切片只做一个虚拟 BTC/USDT 市场。浏览器写请求经登录、CSRF、Origin 和限流后，通过 loopback gRPC 进入唯一的 MarketRunner；它把 submit、fill 和 cancel 线性化，所以 sequence 与同价 FIFO 确定。Exchange 在 trial state 中冻结、撮合和双重记账，PostgreSQL 先原子提交 event、journal、outbox 与投影，随后才替换内存。若响应丢失，fund、submit、cancel 都保留原 ID 查询或重放，绝不换 ID 再做一遍。断线事件以 `(market_sequence,event_index)` 去重补发，batch count 暴露缺口；未 reconcile 完成就拒绝新写。启动时校验 snapshot，再重放 tail，逐批重算结果和 hash；损坏或不一致就 fail closed。完整 event history 还能证明每笔每资产借贷平衡和总资产守恒。这里没有真实资金或真实交易所下单，PostgreSQL/浏览器故障联调要和本地内存证明分开标注。

发布部分的 60 秒解释是：目标 SHA 必须等于干净工作树 HEAD，但 binary 真正从该 Git
object 的 archive 编译；完整 migrations 和 runtime scripts 分别封箱算 hash，再由协调器
确认同一 commit。`verify` 会把新备份恢复到临时库并用候选 binary 重放；`preflight`
只读检查生产依赖和连续 migration ledger。只有写出 `activate SHA --execute` 才会备份、
迁移和切换。先切 binary，再切 runtime；后者失败就恢复旧 plist 并补偿回旧 binary。
数据库迁移不做危险逆迁移，所以所有候选 SQL 都必须向后兼容。

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
19. 为什么只有 `market_sequence` 不能发现同一批次中间丢失的事件？
20. event append 已成功但内存 apply 前崩溃时，为什么重启后不会重复入账？
21. `AuditRecords` 为什么不是第二套账本或第二套 reducer？
22. 随机测试必须记录哪些信息，才能让失败闭环复现？
23. 哪些结果只是 `build-verified`，还需要 PostgreSQL、HTTP 或浏览器环境证明？
24. 为什么“干净 HEAD”之外，binary 还必须从该 commit 的 Git archive 编译？
25. 为什么 migration ledger 只能是候选 migration set 的连续 checksum 前缀？
26. `prepare`、`verify`、`preflight` 和 `activate --execute` 的副作用边界分别是什么？
27. runtime 激活失败后，为什么回滚 binary 而不逆向删除数据库迁移？
