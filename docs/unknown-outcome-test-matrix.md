# Unknown Outcome 测试矩阵

目标：证明“请求超时不等于未执行”，并证明 CEX 与钱包都沿用原请求身份查询
权威事实，但不会混淆各自的最终性模型。

## Qiu Market / CEX

状态含义：`verified-unit` 是单层单元测试，`verified-contract` 是真实浏览器
配合受控 mock 后端的交互契约，`verified-integration` 是真实
PostgreSQL/传输栈测试，`partial` 表示仅有部分分层证据，
`planned/environment-pending` 表示仍缺指定故障注入或真实环境。

| 编号 | 故障注入与核对动作 | 必须成立的不变量 | 当前状态 | 精确证据 / 缺口 |
|---|---|---|---|---|
| C1 | 请求发出前断网；保留同 client order ID | 至多一个订单 | `partial` | `frontend/src/api/trading.test.ts` 的 `marks a write transport failure as submitted unknown without retrying` 只证明 submit 单次调用；尚无浏览器到 PostgreSQL 的断网 E2E |
| C2 | 事件提交后响应丢失；同 ID submit + ListOrders | 返回原订单，不生成第二条事件批次 | `partial` | runner test 只用 fund 注入 unknown commit；submit 幂等和查询视图分别由 `trading/exchange/exchange_test.go::TestScopedIdempotencyAndQueryViews`、`trading/rpc/server/server_test.go::TestTradingGRPCDecimalContractAndOwnership` 覆盖；缺组合故障测试与 OAuth 浏览器链路 |
| C3 | commit 结果不确定；从 event log 恢复 | hash、sequence 与恢复事实一致 | `partial` | `trading/runtime/runner_test.go::TestMarketRunnerRecoversUnknownCommitAndRetryIsIdempotent` 覆盖 unknown commit、sequence 与同 ID 恢复；`trading/exchange/exchange_test.go::TestSnapshotReplayAndCorruptionDetection` 独立覆盖 replay hash；缺把两者合并的单一故障测试 |
| C4 | cancel 响应丢失且订单仍 open；GetOrder 后同 ID cancel | 只解冻一次 | `partial` | `frontend/e2e/trading.spec.ts` 的 `open cancel unknown replays the original request ID exactly once` 证明浏览器在 504 后先查 open 订单并只沿用原 ID；`TestCancelOrderIdempotencyPrecedesClosedStateCheck` 覆盖服务端幂等。仍缺真实 PostgreSQL 响应丢失下的 held/ledger 证明 |
| C5 | cancel 与 partial fill 并发；查询 order/trades/held | filled 部分清算，余量至多解冻一次 | `partial` | `trading/exchange/exchange_test.go::TestPriceTimeSettlementFeesAndCancel`、`trading/orderbook/orderbook_test.go::TestPriceTimePriorityAndPartialFill`；缺 cancel/partial-fill 竞态故障注入 |
| C6 | cancel 响应丢失但订单已终态；读取 GetOrder | 不再发第二次撤单 | `verified-contract` | `frontend/e2e/trading.spec.ts` 的 `terminal cancel fact clears unknown without issuing a second cancel` 在浏览器中注入 commit 后 504，随后以订单终态清 pending，并断言 cancel 调用总数仍为 1 |
| C7 | fund 已记账但响应丢失；同 ID fund | ledger 只有一组分录，余额只增加一次 | `verified-integration` | `trading/e2e/vertical_slice_integration_test.go::TestVirtualSpotTransportTradeFeesCancelAndRestart` 在真实 PostgreSQL/HTTP 栈提交 fund 后丢弃响应，同 ID 重放并跨重启再重放；断言 event batch 仅 1 条、ledger 仅 2 条且净额为 0、用户只增加一次。客户端仍没有 fund request/ledger 直查接口 |
| C8 | 另一个 actor 账户打开 pending；点击 reconcile | 必须拒绝跨 actor 账户核对，fund target 不与 actor 混淆 | `partial` | `frontend/e2e/trading.spec.ts` 的 fund unknown 测试证明 sessionStorage 中 actor 与 payload target 分离且重放 target 不变；仍缺另一个 actor 登录后点击 reconcile 的拒绝测试 |
| C9 | 浏览器刷新；原 actor 重新登录并 reconcile | operation/request/payload 不丢 | `verified-contract` | `frontend/e2e/trading.spec.ts` 的 `fund unknown survives reload and replays the same actor-bound request` 执行真实 browser reload，断言原 actor、target、operation/request payload 与 request ID 保留并在成功后删除；真实 OAuth re-login 仍属 Gate 2C |
| C10 | 502/503/504；保持 unknown，不自动写第二次 | 初次调用不盲重试，只有显式 reconcile 才沿用原 ID | `partial` | `frontend/src/api/trading.test.ts` 覆盖 submit fetch failure；两个 Playwright unknown 测试覆盖 cancel/fund 504 后等待用户显式核对。仍没有经过真实 Vercel BFF 的三种写故障链 |
| C11 | 明确 4xx 业务拒绝；不进入 unknown | 不产生 pending，不盲重试 | `planned/environment-pending` | API 能区分 uncertain 错误，但尚无三种写操作的 UI pending 负向测试 |
| C12 | 恢复后发布 outbox；从 cursor 续读 | checkpoint 前进且 `(sequence,event_index)` 可用于去重 | `partial` | `trading/store/postgres/store_integration_test.go::TestPostgresEventSnapshotOutboxAndRecovery` 与 `trading/rpc/server/server_test.go::TestTradingGRPCSubscribeEventsFromCursor`；尚无浏览器断线重连去重 E2E |

## 钱包 `broadcast_unknown`（EVM 示例）

| 编号 | 故障注入 | 核对顺序 | 完成条件 | 与 CEX 的关键区别 |
|---|---|---|---|---|
| W1 | `sendRawTransaction` 超时 | 本地 tx hash → 多 RPC `getTransaction` → mempool | 找到相同 hash 或明确可安全重播同一 raw tx | 身份是签名后 tx hash，不是 venue order ID |
| W2 | 节点返回 already known | 查 tx/hash | 视为已传播，不重新构造 nonce | 相同 raw tx 可重播，新签名可能变成另一交易 |
| W3 | receipt 暂无 | mempool → receipt → block | 继续 pending，不能释放资金 | CEX 通常以 venue order/trade 为权威 |
| W4 | receipt success | canonical block + confirmations | 达到链特定 finality 后完成 | receipt 一次出现仍可能被 reorg |
| W5 | receipt reverted | canonical receipt | 业务失败，但 gas 已消耗 | CEX reject 通常没有链上 gas |
| W6 | reorg 移除 receipt | canonical hash/height 重查 | 回退为 pending/unknown，再等 canonical 事实 | CEX 主要处理 venue 事件纠正，不复制链 finality |
| W7 | replacement transaction | sender+nonce、原/新 hash | 识别 replaced/dropped/canonical winner | request ID 不能替代 nonce 与多个 tx hash |
| W8 | 多链差异 | 使用链专属查询与确认规则 | 分链证明最终性 | 上述 W1–W7 主要是 EVM 术语；Bitcoin、Solana 必须换成各自的广播、确认与 canonical 事实，不能共享一套语义 |

## 验收顺序

1. 先证明相同 ID 不产生第二个副作用；
2. 再证明不同账户/不同 operation 不会错误碰撞；
3. 再注入响应丢失、commit unknown、重启和 partial fill/cancel race；
4. 最后对账 sequence、state hash、订单/成交、available/held 与 ledger；
5. 钱包场景还必须补 canonical chain、finality 与 reorg 证据。

当前状态：矩阵已设计并逐项标明证据等级；现有分层测试不等于 OAuth 后的真实
submit/cancel/fund 网络故障 E2E，后者仍为 `environment-pending`。
