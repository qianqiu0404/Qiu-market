# Qiu Market 口述与面试证据训练

- 基线：`882951845c1c0f247b9c80bdf3b6173fb6b13d22`
- 材料状态：`implemented`
- 个人状态：`learning`

## 使用方法

1. 第一遍可看“证据锚点”口述；
2. 第二遍只看标题；
3. 第三遍录音，检查是否在时间内讲清业务、调用链、不变量、故障和证据边界；
4. 随机回答追问，不能用“代码里有”代替具体入口或测试；
5. 连续两次通过后，个人状态才可改为 `mastered`。

以下时长按正常中文面试语速设计。不要为了卡秒机械背诵；目标是 60/90 秒内保留事实完整性。

## 90 秒总口述

> Qiu Market 是一个单用户、虚拟资金的 BTC/USDT 交易基础设施纵切片，不连接真实充值、私钥或实盘。行情侧先把 provider symbol 通过审核 alias 解析成 canonical asset；四家 CEX 用 WebSocket 收实时 ticker、REST 对账漏项，PostgreSQL 先接收，Redis 后派生，只有 30 秒内的新鲜 Spot 才进入综合价。交易写请求经 session、CSRF、Origin 和限流后，通过 loopback gRPC 进入唯一的 MarketRunner。runner 串行分配 sequence，所以同价 FIFO、部分成交和撤单顺序可确定。每次 submit、cancel、fund 都用 market、subject account、operation、request ID 做幂等；超时只进入 submitted/unknown，必须保留原 ID 查订单、成交和账本，不能盲目重试。撮合先冻结 available 到 held，成交后按资产写对平分录并计 Maker/Taker 费用；cancel 只释放未成交余量。event batch、ledger、outbox 和投影在一个 PostgreSQL 事务提交，成功后才更新内存。启动时校验 snapshot hash，再重放 event stream；结果、账本、投影或 state hash 不一致就 fail closed。当前代码与本地测试是 implemented / build-verified，真实 PostgreSQL 重启和精确 Preview unknown-write Gate 有 integration-verified 证据；Production promotion、浏览器断线重连闭环和长期可用仍不能冒充完成。

### 90 秒追问

1. 为什么 `submitted/unknown` 不能叫“失败”？
2. partial fill 后 cancel 的资金不变量是什么？
3. event stream、snapshot、projection、outbox 各自是什么角色？
4. 为什么 exact Preview Gate 2C 不等于 Production？

## 60 秒一：行情身份与质量

> 行情系统首先解决“这个 symbol 到底是谁”，再解决“价格是否可信”。provider 目录进入 `resolveDiscoveredMarkets` 后，只有已审核 alias 才能映射到 canonical asset；symbol 唯一只能生成 pending 建议，不能自动批准。四家 CEX 用 WebSocket 主链路、REST reconcile 漏项，所有快照走 `SnapshotWriter`：PostgreSQL 先判断乱序、丢弃或修正，接受后 Redis 才更新。`BuildComposite` 只用 30 秒内的新鲜 Spot，稳定币率必须有效，偏离中位数 3% 的报价排除，venue 权重按场所限额，Perp 和 DEX 不进综合现货价。接口继续暴露 provider、price kind、source time 和 freshness，失败时显示 Unknown/Stale，不用 CoinGecko 或假数据兜底。现有身份、snapshot 和 composite 单测是 build-verified；长期 provider canary 和生产 SLA 仍是 environment-pending。

证据锚点：

- `crawler/catalog_supervisor.go::resolveDiscoveredMarkets`
- `marketdata/snapshot_writer.go::SnapshotWriter.Write`
- `marketdata/composite.go::BuildComposite`
- `TestResolveDiscoveredMarketsRequiresApprovedProviderAliases`
- `TestBuildCompositeRejectsPerpStaleAndOutlier`

常见误区：crawler 进程活着不等于某 provider ticker 新鲜；selection 成员也不等于当前有可用价格。

## 60 秒二：订单幂等与 Unknown

> Qiu Market 的幂等身份是 market、subject account、operation 和 request ID。submit 用 client order ID，cancel 和 fund 在调用前生成 request ID；fund 还要区分登录管理员 actor 与目标入金 subject。浏览器遇到网络错误或 502、503、504 时只保存 unknown pending，不自动重试。核对时 submit 用原 payload/ID 重放或查同 client ID 订单，cancel 先查订单终态，仍 open 或 partially-filled 才用原 ID 重放，fund 只能原 ID 重放后刷新投影。服务端 `runLocked` 先返回同键历史结果，同键异 payload 冲突；PostgreSQL commit 结果未知时 runner 从 event stream 恢复，再让原 ID 收敛。单层测试是 build-verified，真实 PostgreSQL fund 响应丢失与 exact Preview 三种写 504 已有 integration-verified；fund request/ledger 直查仍缺失。

证据锚点：

- `frontend/src/views/Trade.vue::reconcilePendingWrite`
- `trading/exchange/exchange.go::runLocked`
- `trading/runtime/runner.go::recoverAfterPersistenceError`
- `TestIdempotencyAndConcurrentRetry`
- `TestMarketRunnerRecoversUnknownCommitAndRetryIsIdempotent`

常见误区：同 ID 重试不是“随便再发一次”，必须保持同 operation、同 subject 和同 payload。

## 60 秒三：部分成交与撤单竞态

> 部分成交和撤单看起来并发，但同一市场只有一个 MarketRunner writer，所以最终是两条命令的 sequence 顺序。fill 先到时，`Book.Match` 按价格时间优先成交，`applySubmit` 更新 maker 的 filled、remaining 和 held，`settleFill` 固化费用与账本；随后 cancel 只能删除剩余挂单并释放剩余 held，已成交部分不能回滚。cancel 先到时，订单先从 book 删除，后续 taker 不应再匹配它。相同 cancel ID 必须先走幂等结果，即使投影已经 closed 也返回原 canceled；新 cancel ID 对 closed order 才拒绝。现有 FIFO、fill-before-cancel 和 cancel 幂等是 build-verified，但“两种顺序 + 响应丢失 + restart + ledger/hash”的单一 PostgreSQL E2E 还没有，所以完整 race 闭环仍是 environment-pending。

证据锚点：

- `trading/orderbook/orderbook.go::Book.Match`
- `trading/exchange/orders.go::applySubmit/applyCancel/settleFill`
- `TestPriceTimePriorityAndPartialFill`
- `TestPriceTimeSettlementFeesAndCancel`
- `TestCancelOrderIdempotencyPrecedesClosedStateCheck`

常见误区：`go test -race` 只能发现数据竞争，不能替代 fill/cancel 两种业务顺序的断言。

## 60 秒四：双重记账

> 交易账本不直接改一个余额数字，而是每个资产都写对平分录。虚拟入金把 system treasury 记负、用户 available 记正；下单把 available 移到 held；撤单把未成交 held 释放回 available。成交时，买方 held USDT 减少、卖方 available USDT 增加并提取卖方费用；卖方 held BTC 减少、买方 available BTC 增加并提取买方费用。`Ledger.Post` 先在 pending map 试算，逐资产分录和不为零、用户余额为负或 transaction ID 重复都会整笔拒绝。Exchange 再把 journal delta、余额、订单和成交投影交给 PostgreSQL，与 event batch、outbox、sequence CAS 同事务提交。单元测试证明对平、负余额拒绝和费用；真实 PostgreSQL E2E 还核过 fund 只有一批事件、两条分录、净额为零和跨重启余额一次变化。

证据锚点：

- `trading/ledger/ledger.go::Ledger.Post`
- `trading/exchange/orders.go::settleFill`
- `trading/store/postgres/store.go::Append/applyProjection`
- `TestLedgerRejectsUnbalancedAndNegativeTransactions`
- `TestVirtualSpotTransportTradeFeesCancelAndRestart`

常见误区：balance projection 是快速查询结果，event stream 与不可变 ledger 才提供重建和审计依据。

## 60 秒五：快照、事件与恢复

> Qiu Market 把 event stream 当最终交易真值，snapshot 只是加速恢复，projection 是可重建读模型，outbox/event feed 负责可靠事件交付。每条命令先克隆状态并完成撮合、账本和不变量校验，再把 command/result、journal、projection、outbox 与 state hash 同事务提交；成功后内存才前进。启动时 `Restore` 先校验 snapshot schema、sequence 和 hash，再顺序重放后续 event batch，并逐批比较结果、journal、projection 和 hash，任何差异都 fail closed。commit outcome unknown 时 runner 进入 recovering，从日志恢复后才回 ready。publisher 把 outbox 幂等写进 event feed并推进 checkpoint，客户端按 sequence/event index 续读。快照损坏、PG restart 和 cursor 层已有分层或真实 integration 证据；浏览器真断线、换 ticket 后不丢不重仍是 environment-pending。

证据锚点：

- `trading/exchange/exchange.go::Restore`
- `trading/runtime/runner.go::NewMarketRunner/recoverAfterPersistenceError`
- `trading/store/postgres/store.go::PublishOutboxBatch/FeedAfter`
- `TestSnapshotReplayAndCorruptionDetection`
- `TestPostgresEventSnapshotOutboxAndRecovery`

常见误区：snapshot 不是最终真值；删掉 event stream 后只留 snapshot，无法重新证明每条命令与账本历史。

## 失败链快速演练

面试官说：“下单接口 504，服务重启，用户又点了一次。”回答必须按顺序：

1. 第一次发送前已有稳定 client order ID；
2. 504 只进入 `submitted/unknown`，UI 锁定新写并保留原 payload；
3. PostgreSQL commit 若结果未知，runner 从 event stream 恢复；
4. 用户核对时使用原 ID；服务端返回原 result，不产生第二个 event batch；
5. 查询 order/trades/available/held/ledger，核对 sequence 与 state hash；
6. 缺少权威事实时继续 unknown，不能为了体验把状态改成失败或成功。

## 证据边界答法

| 面试官问法 | 合格回答 |
|---|---|
| “做完了吗？” | “代码是 `implemented`；本次 Go、Vitest、Playwright contract 是 `build-verified`。” |
| “有真实联调吗？” | “真实 PostgreSQL restart 和 exact Preview unknown-write Gate 有 `integration-verified` 记录；本工作树复跑时 PostgreSQL suites 因无 DSN skip。” |
| “上线稳定吗？” | “不能这样说。Preview 不等于 Production；promotion、浏览器 reconnect、长期观察仍是 `environment-pending`。” |
| “你建议怎么补？” | “补 partial-fill/cancel 组合 E2E、fund request/ledger 查询、浏览器 cursor reconnect；这是 `production-recommendation`。” |

## 评分卡

每项 0–2 分，总分 10 分；单次至少 8 分且“证据边界”不能为 0：

| 维度 | 0 分 | 1 分 | 2 分 |
|---|---|---|---|
| 业务结果 | 只讲技术名词 | 能说用户行为 | 能说结果与风险 |
| 调用链 | 无入口 | 入口不成顺序 | 三至五个入口顺序准确 |
| 不变量 | 说“保证一致” | 一个具体不变量 | 两个以上且能解释失败 |
| 恢复 | 只说重试 | 能说恢复方向 | 原身份、权威事实、fail closed 完整 |
| 证据边界 | 把本地当生产 | 能区分部分层级 | 五级证据与缺口准确 |

## 术语速答

| 术语 | 准确含义 | 大白话 | 项目位置 |
|---|---|---|---|
| Canonical asset | 跨 provider 统一、已审核的资产身份 | BTC 的身份证 | `asset.guid` / alias 审核 |
| Idempotency key | 同一业务意图的稳定作用域身份 | 同一张小票 | `domain.IdempotencyKey` |
| Held | 已为 open order 冻结、不能再次花费的余额 | 已占座的钱 | `ledger.UserHeld` |
| Event stream | 按 sequence 保存的版本化命令与结果真值 | 不可跳页的流水账 | `trading_event_batch` |
| State hash | 对确定性交易状态计算的摘要 | 状态指纹 | event/snapshot `state_hash` |
| Cursor | 消费者最后确认的事件位置 | 读到哪一页哪一行 | `(sequence,event_index)` |

## 闭卷自检

1. 不看稿完成 90 秒总口述，是否主动说出产品边界和证据边界？
2. 五个主题能否各指出三至五个按顺序的入口？
3. 哪个主题目前最大的 `environment-pending` 缺口是什么？
4. 能否用“已提交、响应丢失、重启恢复、原 ID 核对”讲完一条失败链？
5. 为什么材料 `implemented` 不等于个人 `mastered`？

