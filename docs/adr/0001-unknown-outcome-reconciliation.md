# ADR 0001：未知执行结果必须沿用原请求身份核对

- 状态：Accepted
- 日期：2026-07-28
- 范围：Qiu Market 虚拟资金 submit / cancel / fund

## 背景

客户端发出写请求后，可能在“服务端已经提交、响应尚未到达”之间断网或超时。
此时客户端只知道结果未知，不能据此判断命令失败。如果生成新 request ID
再次执行，虚拟入金可能重复记账，下单可能重复挂单，撤单则可能与 partial fill
并发产生错误解释。

同一个问题也存在于钱包广播：RPC 超时不代表交易没有进入节点、mempool 或链。
两者都需要把传输结果与权威业务事实分开。

## 决策

1. 每次写操作在发送前生成并保存稳定身份。服务端幂等范围是
   `(market_id, subject_account_id, operation, request_id)`；submit/cancel 的
   subject 是当前交易账户，管理员虚拟入金的 subject 是目标入金账户。
2. submit 使用 `client_order_id` 作为 request ID；cancel 和 fund 也在调用前生成
   request ID，不能在超时后丢失。
3. 网络错误、502、503、504、`backend_timeout` 或 `backend_unavailable` 只把
   请求置为 `unknown`，不会自动重试，也不会生成新 ID。
4. 浏览器 pending 记录中的 `account_id` 是发起操作的登录 actor，用于阻止
   另一个登录账户代为核对；fund payload 中的 `account_id` 才是目标 subject。
   两个身份不能混用。
5. 用户发起 reconcile 时：
   - submit：以同一 client order ID 重放；服务端幂等约束返回原事实；
   - cancel：先读取权威订单；仍为 open/partially-filled 才用原 request ID
     重放撤单；
   - fund：只允许用原 request ID 重放，数据库唯一约束防止重复记账。
6. 当前客户端在同 ID 重放成功，或订单视图确认 submit/cancel 已收敛后清除
   pending，并随后刷新订单、成交与余额投影。fund 尚无按 request ID 查询
   fund/ledger 的只读接口，因此当前只能依赖同 ID 重放的幂等成功再刷新投影；
   这是一项明确的后续验证缺口，不能宣称客户端已直接核对 ledger。
   再次超时仍保持 `unknown`。
7. BFF 对任何写请求以及 OAuth start/callback 都不自动重试。只允许显式白名单
   中的纯读 GET 有界重试。

代码入口：

- `frontend/src/api/trading.ts`：错误是否 uncertain，以及写接口不重试；
- `frontend/src/trading/pending-write.ts`：账户绑定的 pending schema；
- `frontend/src/views/Trade.vue`：submit/cancel/fund 保存与 reconcile；
- `trading/runtime/runner.go`：提交结果不确定后的恢复；
- `trading/store/postgres/store.go`：`ErrCommitOutcomeUnknown`；
- `frontend/src/api/trading.test.ts`、`frontend/src/trading/pending-write.test.ts`：
  当前客户端契约测试。

## 当前收敛条件与完整目标

下表刻意区分当前客户端已经实现的收敛条件与完整的权威事实目标。当前 UI
并没有 submit/cancel/fund 的 ledger 或 request-record 直查能力。

| 场景 | 不能作为结论 | 当前客户端收敛条件 | 完整目标权威事实 |
|---|---|---|---|
| CEX submit unknown | HTTP 超时、按钮状态 | 同 ID 幂等响应，或订单视图出现同一 client order ID | order、trades、ledger、request ID 唯一记录 |
| CEX cancel unknown | 撤单响应丢失 | GetOrder 已为终态，或同 ID cancel 返回成功 | order status、partial fills、held/available、ledger、cancel request |
| 虚拟 fund unknown | 余额页面未刷新 | 同 ID fund 返回幂等成功，再刷新余额投影 | fund request 唯一记录、ledger entries、balance projection |
| 钱包 broadcast_unknown | RPC 超时或单个节点 not found | 不属于 Qiu Market 当前实现 | tx hash、mempool、receipt、canonical block、confirmations |

## 后果

优点：

- 避免重复订单、重复入金和盲目撤单；
- 服务重启、BFF 超时与浏览器刷新后仍能继续核对；
- 可以把“传输不确定”与“业务拒绝”分别监控和解释。

代价：

- UI 必须保留 pending 状态和原请求身份；
- 服务端必须长期保证幂等键唯一性；
- reconcile 不是简单重试，需要按 operation 查询不同的权威事实；
- fund 的直接 request/ledger 查询接口尚未实现，当前证据只能证明幂等重放和
  投影刷新，不能把它写成完整的客户端 ledger reconciliation；
- 钱包比 CEX 多出 finality、canonical chain 与 reorg 处理，不能直接复制状态机。

## 不采用的方案

- 超时后生成新 ID 自动重试：会产生重复副作用；
- 把所有超时都当失败：会掩盖已经提交的事实；
- 只依赖前端 sessionStorage：服务端幂等与权威查询缺失时仍不安全；
- 对 OAuth callback 做 GET 重试：一次性 state/code 已消费时会破坏登录。
