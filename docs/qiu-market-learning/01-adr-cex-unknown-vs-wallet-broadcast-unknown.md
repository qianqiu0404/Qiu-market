# Learning ADR L-001：CEX `submitted/unknown` 与钱包 `broadcast_unknown`

- 状态：Accepted
- 基线：`882951845c1c0f247b9c80bdf3b6173fb6b13d22`
- 范围：Qiu Market 虚拟 submit / cancel / fund 与钱包广播未知结果的恢复原则
- 工程真值：[ADR 0001：未知执行结果必须沿用原请求身份核对](../adr/0001-unknown-outcome-reconciliation.md)
- 课程状态：`implemented`

本文是学习对照 ADR，不替代 Qiu Market 的 canonical 工程 ADR，也不声明本仓库实现了钱包广播。钱包部分是跨系统设计对照；本仓库内可运行的实现与测试只覆盖 Qiu Market 虚拟交易。

## 问题与可见结果

写请求可能已经越过副作用边界，但客户端没有收到响应：

```text
client 发送稳定身份
  -> 服务端 / 节点接受
  -> 事务提交 / 交易传播
  -X-> 响应在返回途中丢失
```

此时传输层只知道“结果未知”，不知道“未执行”。如果把超时直接标成失败并生成新身份重试，CEX 可能重复下单、重复入金或错误撤单；钱包可能重复签名、制造替代交易，甚至改变 nonce / fee 后让两笔不同交易同时处于观察范围。

可见的正确结果是：

1. UI 或调用方显示 `submitted/unknown` / `broadcast_unknown`，不伪装成失败；
2. 保留原 idempotency identity；
3. 查询对应系统的权威事实；
4. 在事实收敛前不释放保护状态、不生成第二个副作用。

## 决策

### 共同原则

1. **发送前固定身份。** 不能等到超时后再补 request ID、tx hash 或 nonce。
2. **超时只改变认知状态。** 网络错误、502/503/504、RPC timeout 说明响应不可见，不说明副作用未发生。
3. **保留原身份。** CEX 保留原 request ID；钱包保留原签名交易、tx hash、chain identity 与 sender/nonce。
4. **先查权威事实。** 不拿按钮状态、单个 RPC 的 `not found`、页面余额未刷新或本地缓存当最终结论。
5. **只有同一身份可做安全重放。** CEX 重放同 operation/request ID；钱包在策略确认允许时只重播完全相同的 signed raw transaction，不重新签名或静默改 nonce / fee。
6. **恢复过程可审计。** 记录查询来源、观察时间、权威状态与最终收敛依据。

### 不同权威事实

| 维度 | CEX `submitted/unknown` | 钱包 `broadcast_unknown` |
|---|---|---|
| 稳定身份 | `(market_id, subject_account_id, operation, request_id)`；submit 使用 `client_order_id` | `chain_id + signed raw tx + tx hash + sender + nonce`；tx hash 来自签名后字节 |
| 权威系统 | 当前 Qiu Market event stream / order / trade / ledger；外部 CEX 则是 venue order/trade/ledger API | 节点与 canonical chain：mempool、transaction、receipt、block、confirmations |
| Submit 核对 | 同 client order ID 的 order、trades、request record、ledger | 同 tx hash 是否已知、是否进入 mempool / block |
| Cancel 核对 | order terminal state、partial fills、剩余 held/available、cancel request | 不直接等价；链上“取消”通常是同 nonce replacement，需要同时跟踪原/替代 hash |
| Fund 核对 | fund request 唯一记录、ledger entries、balance projection | 资产转账本身仍按 tx hash / receipt / canonical block 核对 |
| 最终性 | venue 接受、成交和账本事实；仍需处理 venue 自己的纠正语义 | receipt 出现后仍要 confirmations / finality，并处理 reorg |
| 安全重放 | 同 operation、同 subject、同 request ID、同 payload | 完全相同的 signed raw transaction；是否重播要先查 canonical facts |
| 额外竞态 | partial fill 与 cancel、订单状态与账本投影 | replacement、dropped transaction、reorg、不同节点视图不一致 |

### Qiu Market 当前收敛条件与完整目标

| 操作 | 当前客户端已实现 | 完整权威目标 | 当前缺口 |
|---|---|---|---|
| submit | 同 ID 重放返回原结果，或订单列表出现同 `client_order_id` | order + trades + ledger + 唯一 request record | UI 未按 request ID 直接核对 trade / ledger |
| cancel | 先 `GetOrder`；已终态则不再撤，仍 open/partially-filled 才用原 ID 重放 | order terminal state + partial fills + available/held + ledger + cancel record | 尚缺真实 partial-fill/cancel 响应丢失组合 E2E |
| fund | 原 ID 幂等重放成功，再刷新余额 | fund request 唯一记录 + 两条对平 ledger entries + balance projection | 客户端没有 fund request / ledger 只读查询 |

“当前实现”与“完整目标”必须同时写出。余额增加一次是重要投影事实，但不能单独替代 fund request 和 ledger 证明。

### 钱包收敛顺序

钱包侧使用以下查询顺序；它是跨系统设计规则，不是本仓库实现状态：

1. 本地从完全相同的 signed raw transaction 计算 tx hash；
2. 用多个可信 RPC 查询该 hash 的 transaction / mempool 可见性；
3. 查询 receipt，并核对 receipt 所属 block 是否仍在 canonical chain；
4. 未达到链特定 confirmations / finality 前保持 pending；
5. 如果 sender/nonce 出现 replacement，同时跟踪原 hash 与候选 hash，确认 canonical winner；
6. reorg 移除 receipt 时回退为 pending/unknown，不把旧 receipt 当完成；
7. 只有在查询后仍满足安全重播策略时，才重播原始 signed bytes；不得自动重签、改 nonce 或改 fee。

Bitcoin、Solana 等链必须替换为各自的 mempool、确认与 canonical 事实，不能把上述 EVM 术语原样套用。

## Qiu Market 端到端控制流

1. `frontend/src/views/Trade.vue::submitOrder/cancelOrder/fundVirtual` 在发送前生成或固定 request ID。
2. `frontend/src/api/trading.ts::request` 把网络错误及 502/503/504 标为 uncertain，写请求不自动 retry。
3. `frontend/src/views/Trade.vue::storePendingWrite` 保存 operation、actor、request ID 与 payload。
4. `frontend/src/views/Trade.vue::reconcilePendingWrite` 校验当前登录 actor；submit/fund 用原 ID 重放，cancel 先查订单。
5. `trading/httpapi/api.go` 从服务端 session 确定 submit/cancel 账户；fund 明确区分管理员 actor 与目标 subject。
6. `trading/exchange/exchange.go::runLocked` 先按四元组查历史结果；同 ID 异 payload返回 conflict。
7. `trading/store/postgres/store.go::Append` 把 event、ledger、outbox、projection 和 sequence CAS 放进同一事务。
8. commit 结果不确定时，`trading/runtime/runner.go::recoverAfterPersistenceError` 从 event stream 恢复，之后同 ID 返回原结果。

## 必须一直成立的不变量

- 同一幂等键、同一 payload 至多生成一个 event batch；
- 同一幂等键、不同 payload 必须拒绝；
- actor 不能核对另一个 actor 留下的 pending；fund target subject 不能与 actor 混淆；
- partial fill 已成交部分不可回滚，cancel 只释放剩余 held；
- 每个资产的每笔 ledger transaction 分录和为零；
- unknown 恢复后 sequence 和 state hash 与持久化真值一致；
- 钱包不得因为 RPC timeout 自动产生一笔新签名交易。

## 故障、降级、重试与恢复

| 故障 | 禁止行为 | 安全行为 | 收敛条件 |
|---|---|---|---|
| CEX submit 响应丢失 | 新 client order ID 自动重下 | 保持 pending；查订单或同 ID 重放 | 原 order/result 唯一 |
| CEX cancel 与 partial fill 交错 | 假设原数量全部可解冻 | 先查 order/trades；对剩余量用原 cancel ID | 终态、已成交清算、剩余 held 只释放一次 |
| CEX fund 响应丢失 | 从余额页面猜测并用新 ID 再入金 | 原 fund ID 重放；目标是核对 request/ledger/projection | 唯一 event batch、对平分录、余额一次变化 |
| PostgreSQL commit 返回未知 | runner 继续接新命令 | runner 进入 recovering，从事件恢复后再 ready | sequence/hash 与日志一致 |
| 钱包 RPC timeout | 重签或 fee bump | 多 RPC 查原 hash；必要时重播相同 raw tx | mempool/receipt/canonical 事实 |
| 钱包 receipt 后 reorg | 仍标 final | 回退 pending，重查 canonical block | 达到链特定 finality |
| 钱包 replacement | 只盯原 hash并误判 dropped | 按 sender+nonce 跟踪候选 hash | 确认 canonical winner |

## 设计理由、替代方案与成本

### 选择：原身份 + 权威查询

这个决策把“请求是否送达”与“业务是否生效”分开。它允许网络、BFF、RPC 或响应链路失败时仍从业务真值恢复。

### 被拒绝的方案

- **超时即失败。** 已提交命令会被错误地再次执行。
- **超时后生成新 ID。** 服务端幂等约束无法识别同一意图。
- **所有写操作统一 GET retry。** OAuth callback、submit、cancel、fund 都可能有一次性或副作用语义。
- **只保存 sessionStorage。** 浏览器状态不是服务端唯一性，也不是 ledger/chain 事实。
- **把 CEX request ID 当钱包最终身份。** request ID 不能替代签名后 tx hash、sender/nonce 和 canonical chain。
- **钱包 unknown 自动重签或提费。** 它会产生新的交易身份，必须作为显式 replacement 决策处理。

### 成本

- UI 要保存并展示 pending；
- 服务端要维护长期唯一约束和幂等结果；
- reconcile 必须按 operation 分支，不能写成一个通用“再试一次”按钮；
- 钱包需要多节点、canonical chain、replacement 和 reorg 观察；
- fund 的 request/ledger 查询接口仍需后续实现。

## 证据等级

| 结论 | 等级 | 证据与边界 |
|---|---|---|
| Qiu Market 四元组幂等、pending 保存、同 ID reconcile、unknown commit 恢复 | `implemented` | 上述代码入口存在于 `8829518` |
| 单层幂等、unknown commit、partial fill、ledger、cursor 契约 | `build-verified` | `TestIdempotencyAndConcurrentRetry`、`TestMarketRunnerRecoversUnknownCommitAndRetryIsIdempotent`、`TestPriceTimeSettlementFeesAndCancel`、`TestLedgerPostingAndSnapshot`、`TestTradingGRPCSubscribeEventsFromCursor` |
| 真实 PostgreSQL 下 fund 响应丢失、同 ID 重放、跨重启、event/ledger/hash 核对 | `integration-verified` | `TestVirtualSpotTransportTradeFeesCancelAndRestart`；只有设置真实 `S78_TEST_POSTGRES_DSN` 并实际运行时成立 |
| 精确 `8829518` Preview 的 OAuth、BFF 与 fund/submit/cancel 504 后同 ID 核对 | `integration-verified` | Accord 在 2026-07-30 记录 exact Preview Gate 2C 已通过；实现入口为 `frontend/scripts/run-preview-oauth-gate.mjs`。本交付未重跑私有 Gate，且该证据不等于 Production |
| Production promotion、长期可用与生产 SLA | `environment-pending` | 当前其他任务仍在执行 promotion / Production auth；本 ADR 不升级其状态 |
| 钱包 `broadcast_unknown` 运行时与链上 E2E | `environment-pending` | 本仓库没有钱包广播实现或链上测试；这里只接受设计对照 |
| 多 RPC、链特定 finality、replacement/reorg 监控 | `production-recommendation` | 钱包系统后续应实现并按链验收 |

## 术语

| 术语 | 准确含义 | 大白话 | 本项目位置 |
|---|---|---|---|
| Idempotency identity | 唯一标识一次业务意图及其作用域的稳定键 | 同一张业务小票 | `domain.IdempotencyKey` |
| `submitted/unknown` | 请求可能已提交，但客户端尚无权威结果 | 快递交出去了，回执丢了 | `PendingTradingWrite.state` |
| `broadcast_unknown` | 签名交易可能已传播，但 RPC 未给出确定结果 | 广播发出去了，不知道哪些节点听见了 | 钱包对照；本仓库未实现 |
| Authoritative fact | 能决定业务状态的系统真值 | 不看按钮颜色，去查总账 | event/order/trade/ledger 或 canonical chain |
| Partial fill | 订单只有一部分数量成交 | 一单买十个，先成交四个 | `OrderStatusPartiallyFilled` |
| Finality | 链上事实达到约定后不再轻易回滚的置信边界 | 回执还要等足够多次盖章 | 钱包链特定规则 |
| Reorg | canonical chain 替换已观察区块 | 原来盖章的那页被换掉 | 钱包链上恢复边界 |

## Owner 60 秒解释

> CEX 和钱包最危险的共同误判，是把超时当成未执行。Qiu Market 在发送前固定 market、subject account、operation 和 request ID；网络或 502/503/504 只进入 submitted/unknown，不生成新 ID。submit 查同 client order ID 的订单，cancel 先查终态与部分成交，仍 open 才用原 cancel ID，fund 用原 ID 幂等重放，完整目标还要核对 ledger。服务端 commit 结果未知时 runner 从 event stream 恢复，再让同 ID 返回原结果。钱包原则相同，但身份和权威事实不同：保留完全相同的 signed raw transaction 和 tx hash，查询 mempool、receipt、canonical block 与 confirmations；还要处理 replacement 和 reorg，所以不能复制 CEX 状态机，更不能自动重签、改 nonce 或改 fee。

## 闭卷自检

1. 为什么 504 只能说明响应未知，不能说明订单没提交？
2. Qiu Market 的幂等四元组是什么？fund 的 actor 与 subject 为什么不同？
3. submit、cancel、fund 当前分别如何收敛，完整目标还缺什么？
4. partial fill 后 cancel 为什么只能释放剩余 held？
5. 钱包为什么必须保留 signed raw transaction，而不只保留业务 request ID？
6. receipt 已出现后为什么仍不能立刻声称 final？
7. replacement 要用哪两个身份维度关联候选交易？
8. 指出一个 `build-verified` 测试和一个 `integration-verified` 测试，并说明它们各自不能证明什么。
