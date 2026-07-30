# Qiu Market 交易可靠性测试矩阵

- 代码基线：`882951845c1c0f247b9c80bdf3b6173fb6b13d22`
- 审计日期：2026-07-30
- 产品边界：单用户、虚拟资金、`BTC-USDT`
- 矩阵状态：`implemented`

## 怎么读这张矩阵

“测试粒度”只说明测试在哪里运行：

- unit：单一 Go / TypeScript 模块；
- contract：gRPC、HTTP 或受控 mock 浏览器契约；
- integration harness：真实 PostgreSQL / TCP / HTTP / restart；
- live gate：真实 OAuth、Vercel BFF 和精确 Preview deployment。

它不是完成等级。完成等级只使用：

- `implemented`
- `build-verified`
- `integration-verified`
- `environment-pending`
- `production-recommendation`

## 本次复跑快照

| 命令 | 结果 | 可接受结论 |
|---|---|---|
| `go test ./trading/...` | 通过 | 无外部依赖的 Go 测试为 `build-verified` |
| `go test -v ./trading/e2e ./trading/integration ./trading/store/postgres ./trading/auth` | 4 个需要 `S78_TEST_POSTGRES_DSN` 的测试明确 `SKIP`；其他 auth 单测通过 | 本次没有新增 PostgreSQL integration 证据 |
| `cd frontend && npm test -- --run` | 7 files / 41 tests 通过 | 前端 unit/contract 为 `build-verified` |
| `cd frontend && npx playwright test e2e/trading.spec.ts` | 10 tests 通过 | 受控 mock 浏览器契约为 `build-verified`，不是真实后端 |

本次首次运行 Vitest 时因新 worktree 尚无 `node_modules` 而报
`vitest: command not found`；执行锁文件约束的 `npm ci` 后复跑通过。依赖准备失败不被隐藏，也不计作产品缺陷。

仓库此前已记录真实 PostgreSQL integration 通过；2026-07-30 的 Accord 事件还记录了精确 `8829518` Preview Gate 2C 通过。矩阵保留这些既有 `integration-verified` 证据，同时明确本次没有重跑私有 DSN 或 live OAuth Gate。Preview 证据不等于 Production。

## 1. Submit / 下单

| ID | 场景与故障注入 | 必须成立的不变量 | 真实测试入口 | 粒度 | 当前证据 | 本次复跑与缺口 |
|---|---|---|---|---|---|---|
| S1 | 非法精度、tick、step、最小名义金额 | 非法请求不占用 sequence、不冻结资金 | `trading/domain/types_test.go::TestNewOrderValidation`；`TestDefaultBTCUSDTFixedPointAndRounding` | unit | `build-verified` | `go test ./trading/...` 通过 |
| S2 | 同 account/operation/client ID 并发重试 | 只生成一条 submit event batch；同 ID 异 payload 冲突 | `trading/exchange/exchange_test.go::TestIdempotencyAndConcurrentRetry` | unit | `build-verified` | 16 个 goroutine 只得到同一 sequence；本次通过 |
| S3 | 不同 account 复用相同 client ID | 幂等范围不能跨账户碰撞 | `trading/exchange/exchange_test.go::TestScopedIdempotencyAndQueryViews` | unit | `build-verified` | 本次全量 Go 通过 |
| S4 | fetch 在写请求中断开 | 客户端标记 unknown，初次调用不自动重试 | `frontend/src/api/trading.test.ts`：`marks a write transport failure as submitted unknown without retrying` | unit | `build-verified` | Vitest 41/41 通过；mock fetch 不是 BFF |
| S5 | submit 已提交、浏览器只收到 504 | 同 client ID replay 返回原订单；订单视图只出现一条 open order | `frontend/scripts/run-preview-oauth-gate.mjs::committedResponseLostOnce/runUnknownWriteChecks` | live gate | `integration-verified` | Accord 记录 exact `8829518` Preview Gate 2C 通过；本次未重跑私有证据，Production 仍不能由此推出 |
| S6 | 明确 4xx 业务拒绝 | 不进入 unknown，不生成 pending | `frontend/src/api/trading.ts::request` | code path | `implemented` | 没有 submit/cancel/fund 三种 UI 负向组合测试；补测仍为 `production-recommendation` |

## 2. Cancel / 撤单

| ID | 场景与故障注入 | 必须成立的不变量 | 真实测试入口 | 粒度 | 当前证据 | 本次复跑与缺口 |
|---|---|---|---|---|---|---|
| C1 | 部分成交后正常撤单 | 已成交部分保留；只释放剩余 held 一次 | `trading/exchange/exchange_test.go::TestPriceTimeSettlementFeesAndCancel` | unit | `build-verified` | 本次精确复跑通过 |
| C2 | 撤单已提交，closed projection 后用相同 cancel ID 重试 | 返回原 canceled 结果，不被 closed-state 预检查挡住 | `trading/rpc/server/server_test.go::TestCancelOrderIdempotencyPrecedesClosedStateCheck` | gRPC contract | `build-verified` | 全量 Go 通过 |
| C3 | cancel 已提交但浏览器收到 504 | 权威终态清 pending，不发送第二次 cancel | `frontend/e2e/trading.spec.ts`：`terminal cancel fact clears unknown without issuing a second cancel` | browser contract | `build-verified` | Playwright 10/10 通过；后端是受控 mock |
| C4 | cancel 尚未提交且浏览器收到 504 | 先查 open order，只用原 cancel ID 重放一次 | `frontend/e2e/trading.spec.ts`：`open cancel unknown replays the original request ID exactly once` | browser contract | `build-verified` | Playwright 通过；没有真实 PostgreSQL response-loss 组合 |
| C5 | 另一个账户伪造撤单 | gRPC owner guard 不占 sequence；直接 Exchange 拒绝也不能改变原订单 | `trading/exchange/exchange_test.go::TestInsufficientBalanceOverflowAndCancelAuthorization`；`trading/rpc/server/server_test.go::TestTradingGRPCDecimalContractAndOwnership` | unit / gRPC contract | `build-verified` | 全量 Go 通过；直接 Exchange 的 rejected command 会被审计为一条 sequence，不能与边界拒绝混写 |
| C6 | exact Preview cancel commit 后丢响应 | 同 ID replay；`GetOrder` 最终为 canceled | `frontend/scripts/run-preview-oauth-gate.mjs::runUnknownWriteChecks` | live gate | `integration-verified` | 只证明 exact Preview；不证明 Production、长期运行或 partial-fill race |

## 3. Fund / 虚拟入金

| ID | 场景与故障注入 | 必须成立的不变量 | 真实测试入口 | 粒度 | 当前证据 | 本次复跑与缺口 |
|---|---|---|---|---|---|---|
| F1 | 同 fund ID 重复或并发调用 | 一个 event batch；两条 ledger entries；余额只增加一次 | `trading/exchange/exchange_test.go::TestIdempotencyAndConcurrentRetry` | unit | `build-verified` | 本次通过 |
| F2 | fund 已提交但 HTTP 响应被主动丢弃；同 ID 重放并重启 | event batch = 1、ledger entries = 2、净额 = 0、用户余额只增加一次 | `trading/e2e/vertical_slice_integration_test.go::TestVirtualSpotTransportTradeFeesCancelAndRestart` | integration harness | `integration-verified` | 仓库已有真实 PostgreSQL 通过记录；本次因 DSN 未设置明确 `SKIP` |
| F3 | fund unknown 保存在浏览器，刷新后再核对 | actor、target、request ID、asset、amount 不丢；成功后删除 pending | `frontend/e2e/trading.spec.ts`：`fund unknown survives reload and replays the same actor-bound request` | browser contract | `build-verified` | Playwright 通过；mock 后端，不是 OAuth re-login |
| F4 | actor 与 fund target 不同 | actor 负责 pending 所有权，subject 决定服务端幂等账户 | 同上 Playwright 测试；`trading/httpapi/api.go::fundVirtual` | contract / code path | `build-verified` | 已验证 target 不变；尚无“另一个 actor 登录后点击核对”的负向浏览器测试 |
| F5 | 余额投影变化但无法直接查询 fund request / ledger | 客户端不能从余额猜测 fund 已收敛 | `frontend/src/trading/pending-write.test.ts`：`never infers virtual funding from a balance snapshot` | unit negative guard | `build-verified` | 当前客户端仍没有 fund request / ledger 只读 API，完整 reconciliation 为 `environment-pending` |
| F6 | exact Preview fund commit 后丢响应 | 同 ID 两次 replay 后余额只增加 `1 USDT` | `frontend/scripts/run-preview-oauth-gate.mjs::runUnknownWriteChecks` | live gate | `integration-verified` | exact Preview Gate 2C 既有证据；本次未重跑，非 Production |

## 4. Partial Fill / Cancel Race

`MarketRunner` 让同一市场只有一个命令 goroutine，因此“竞态”不是两个 goroutine 同时改订单簿，而是 fill 与 cancel 谁先拿到下一条 sequence。测试必须覆盖两种顺序和响应丢失，而不能只跑 Go race detector。

| ID | 场景与故障注入 | 必须成立的不变量 | 真实测试入口 | 粒度 | 当前证据 | 本次复跑与缺口 |
|---|---|---|---|---|---|---|
| P1 | 同价 FIFO、多档部分成交 | fill 先按价格，再按 accepted sequence；maker 剩余量正确 | `trading/orderbook/orderbook_test.go::TestPriceTimePriorityAndPartialFill` | unit | `build-verified` | 全量 Go 通过 |
| P2 | fill 先于 cancel | 成交及费用保留；partially-filled order 的剩余 held 只释放一次 | `trading/exchange/exchange_test.go::TestPriceTimeSettlementFeesAndCancel` | unit | `build-verified` | 本次精确复跑通过 |
| P3 | cancel 先移除 resting order | canceled order 从 book 删除，剩余同价订单 FIFO 不变 | `trading/orderbook/orderbook_test.go::TestCancelPreservesFIFO` | unit | `build-verified` | 只证明 book removal/FIFO；没有后续 taker、Exchange 资金、trade、ledger 组合断言 |
| P4 | fill/cancel 两种 sequence 顺序 + cancel response loss + restart | filled quantity、remaining、status、held、available、ledger、hash 全部收敛 | 最近入口为 P2、C2、F2；当前没有单一组合测试 | missing integration scenario | `environment-pending` | 这是矩阵最重要缺口之一；分层测试不能拼成真实组合 E2E |
| P5 | 多 goroutine 直接修改同一市场 | 不允许绕过 runner；队列满时显式背压 | `trading/runtime/runner_test.go::TestMarketRunnerBackpressureAndGracefulSnapshot` | unit concurrency | `build-verified` | 全量 Go 通过；它证明串行与背压，不证明业务 race 两种顺序 |

## 5. Reconnect / Reconcile

| ID | 场景与故障注入 | 必须成立的不变量 | 真实测试入口 | 粒度 | 当前证据 | 本次复跑与缺口 |
|---|---|---|---|---|---|---|
| R1 | event append 已写入，但 store 返回 commit unknown | runner 从日志恢复到 sequence 1；同 fund ID 重试不重复入账 | `trading/runtime/runner_test.go::TestMarketRunnerRecoversUnknownCommitAndRetryIsIdempotent` | unit fault injection | `build-verified` | 本次精确复跑通过 |
| R2 | submit/cancel pending 从权威订单视图收敛 | submit 按 client ID；cancel 只接受 terminal status；fund 不猜余额 | `frontend/src/trading/pending-write.test.ts` 全部 3 tests | unit | `build-verified` | Vitest 通过 |
| R3 | 浏览器刷新后恢复 pending | request identity 和 payload 不丢 | `frontend/e2e/trading.spec.ts`：fund reload test | browser contract | `build-verified` | Playwright 通过 |
| R4 | gRPC 从 cursor 订阅 | 只返回 `(sequence,event_index)` 之后的事件 | `trading/rpc/server/server_test.go::TestTradingGRPCSubscribeEventsFromCursor` | gRPC contract | `build-verified` | 本次精确复跑通过 |
| R5 | PostgreSQL event feed 从中间 cursor 续读 | 顺序严格递增，不重发 cursor 本身 | `trading/store/postgres/store_integration_test.go::TestPostgresEventSnapshotOutboxAndRecovery` | integration harness | `integration-verified` | 既有真实 PG 记录；本次 DSN 缺失 `SKIP` |
| R6 | outbox publisher 失败后恢复 | checkpoint 前进；current error 可清除，cleanup error 独立保留 | `trading/outbox/publisher_test.go::TestPublisherDrainsBatches`、`TestPublisherClearsCurrentErrorAfterRecovery`、`TestPublisherKeepsCleanupErrorUntilCleanupRecovers` | unit | `build-verified` | 全量 Go 通过 |
| R7 | 浏览器 WebSocket 真断线、换 ticket、沿原 cursor 重连 | 不丢事件、不重复应用 `(sequence,event_index)` | 最近入口为 `TestTradingGRPCSubscribeEventsFromCursor` 和 `frontend/src/views/Trade.vue::connectEvents` | missing browser integration | `environment-pending` | 当前没有断线重连 + 去重的浏览器 E2E |

## 6. Ledger / 双重记账与对账

| ID | 场景与故障注入 | 必须成立的不变量 | 真实测试入口 | 粒度 | 当前证据 | 本次复跑与缺口 |
|---|---|---|---|---|---|---|
| L1 | 正常 fund/hold/release | available + held 保持正确；快照恢复一致 | `trading/ledger/ledger_test.go::TestLedgerPostingAndSnapshot` | unit | `build-verified` | 本次精确复跑通过 |
| L2 | 不平衡分录或用户负余额 | 整笔 transaction 拒绝，失败不改变余额 | `trading/ledger/ledger_test.go::TestLedgerRejectsUnbalancedAndNegativeTransactions` | unit | `build-verified` | 全量 Go 通过 |
| L3 | 重复 transaction ID | 第二笔拒绝 | `trading/ledger/ledger_test.go::TestDuplicateTransactionIsRejected` | unit | `build-verified` | 全量 Go 通过 |
| L4 | 多 maker 部分成交、Maker/Taker 费率、撤剩余 | BTC/USDT 各自分录和为零；平台费用准确 | `trading/exchange/exchange_test.go::TestPriceTimeSettlementFeesAndCancel` | unit | `build-verified` | 本次通过 |
| L5 | PostgreSQL ledger / balance 投影损坏后重建 | event stream 重建后的 order/trade/balance/checkpoint 与 runtime 一致 | `trading/store/postgres/store_integration_test.go::TestPostgresEventSnapshotOutboxAndRecovery` | integration harness | `integration-verified` | 既有真实 PG 证据；本次 `SKIP` |
| L6 | fund response loss 与跨重启 | fund event 唯一、2 条分录、净额 0、用户只入账一次 | `trading/e2e/vertical_slice_integration_test.go::TestVirtualSpotTransportTradeFeesCancelAndRestart` | integration harness | `integration-verified` | 既有真实 PG 证据；本次 `SKIP` |
| L7 | 在线逐 request 对账 | request record、ledger transaction、balance projection 可按原 ID 联查 | 当前没有公开只读入口 | missing product slice | `environment-pending` | 建议新增内部审计查询，但它是 `production-recommendation`，不是当前行为 |

## 7. Snapshot / Event Recovery

| ID | 场景与故障注入 | 必须成立的不变量 | 真实测试入口 | 粒度 | 当前证据 | 本次复跑与缺口 |
|---|---|---|---|---|---|---|
| E1 | 快照后追加 event，再恢复 | replay 后 sequence/hash/订单/账本与原状态一致 | `trading/exchange/exchange_test.go::TestSnapshotReplayAndCorruptionDetection` | unit | `build-verified` | 全量 Go 通过 |
| E2 | 篡改 snapshot / event / projection | 恢复 fail closed，返回 `ErrRecoveryDiverged` | 同一测试 | unit corruption | `build-verified` | 全量 Go 通过 |
| E3 | PostgreSQL event、snapshot、outbox、projection | 恢复 hash 相同；投影可重建；stale writer CAS 失败 | `trading/store/postgres/store_integration_test.go::TestPostgresEventSnapshotOutboxAndRecovery` | integration harness | `integration-verified` | 既有真实 PG 证据；本次 `SKIP` |
| E4 | canonical migration + backend/gateway + session + restart | session 延续；cancel 同 ID 跨重启返回原结果；snapshot/event hash 相同 | `trading/integration/integrated_stack_test.go::TestCanonicalMigrationIntegratedGatewayAndRestartRecovery` | integration harness | `integration-verified` | 既有真实 PG 证据；本次因 DSN 未设置 `SKIP` |
| E5 | 旧 schema 快照跨版本升级 | 先验证旧 replay，再压缩；不删除可审计用户状态 | `trading/exchange/state_compaction_test.go::TestRestoreVerifiesLegacyReplayAtUpgradeBoundary`；`TestRestoreUpgradesLegacySnapshotWithoutDeletingAuditableUserState` | unit upgrade | `build-verified` | 全量 Go 通过 |
| E6 | 优雅退出 | drain queue 后保存最终 snapshot；关闭后拒绝新命令 | `trading/runtime/runner_test.go::TestMarketRunnerBackpressureAndGracefulSnapshot` | unit lifecycle | `build-verified` | 全量 Go 通过 |
| E7 | 当前 binary 恢复真实备份并核对 state hash / ledger | restored sequence、hash、ledger imbalance 都通过 | `ops/macos/release-production.sh verify` 的受管恢复流程 | environment harness | `integration-verified` | 这是既有 Mac mini 真实恢复证据；本任务未运行 ops，也不把它写成多机灾备 |
| E8 | 介质丢失、跨机恢复、长期 RPO/RTO | 在独立故障域恢复并满足明确目标 | 当前没有对应测试 | missing production evidence | `environment-pending` | 多机 HA / DR 只能作为 `production-recommendation` |

## 关键缺口排序

### P0 学习闭环

1. **Partial fill/cancel 双顺序组合测试**：同一真实 PostgreSQL harness 覆盖 fill-before-cancel、cancel-before-fill、cancel response loss、restart 和 ledger/hash。
2. **浏览器 WebSocket 断线重连**：换一次性 ticket，沿原 cursor 续读，断言不丢、不重复应用。
3. **Fund request/ledger 只读核对**：让客户端从原 request ID 查询 request record 与 ledger，而不是只靠同 ID 重放。

这里的 P0 表示学习资产的下一优先级，不代表线上事故等级。

### 仍不能宣称

- Go package `ok` 不能掩盖内部 PostgreSQL test `SKIP`；
- Playwright mock contract 不是真实 OAuth / BFF / PostgreSQL E2E；
- exact Preview Gate 2C 不是 Production promotion；
- 单机 restore drill 不是多机高可用或灾备；
- test file 存在不等于个人已经能解释其不变量。

## 设计决策、替代与成本

选择“场景 × 不变量 × 真实入口 × 粒度 × 证据 × 缺口”六列，是为了防止把多个分层测试拼成一个不存在的端到端证明。

被拒绝的替代：

- 只列测试文件，不写断言；
- 用 `verified-unit`、`verified-contract` 替代工程五级证据；
- 把 `go test ./...` 的 package `ok` 当成所有 integration test 实际执行；
- 把 Preview、Production 和长期可用折叠成一个“E2E 已完成”。

成本是矩阵更长，且每次代码或外部 Gate 变化都要重新核对；收益是面试和审计时能准确回答“哪一层真的验证过”。

## 术语

| 术语 | 准确含义 | 大白话 | 当前项目位置 |
|---|---|---|---|
| Test harness | 为真实依赖搭建、运行并清理的受控测试环境 | 临时搭一个小交易所再拆掉 | `trading/e2e`、`trading/integration` |
| Fault injection | 主动制造响应丢失、commit unknown 或断线 | 故意拔一次回执线 | runner store stub、Playwright route |
| Race ordering | 两个业务命令在单 writer 上的先后 sequence | 谁先拿到柜台号码 | `MarketRunner` |
| Reconciliation | 用原身份把 unknown 收敛到权威事实 | 拿原小票去查总账 | pending write / order / ledger |
| Projection | 从 event stream 派生的查询表 | 为查得快抄出的索引账 | `trading_order` 等 |
| Cursor | 消费者已确认的最后事件位置 | 书签停在第几页第几行 | `(sequence,event_index)` |

## Owner 60 秒解释

> 这张矩阵不把“测试类型”当证据等级。纯 Go、Vitest 和 mock Playwright 本次都通过，所以对应不变量是 build-verified；真实 PostgreSQL suites 本次因为没有 DSN 明确 skip，但仓库保存过真实运行证据，所以只在对应行保留既有 integration-verified，并标出本次未复跑。submit、cancel、fund 的同 ID 语义都有分层测试，exact Preview 还做过真实 504 注入；最大的组合缺口是 partial fill 与 cancel 两种顺序加响应丢失、重启、held 和 ledger 的单一 PostgreSQL E2E。cursor 在 RPC 和 PostgreSQL 层有证据，但浏览器真断线换 ticket 后不丢不重还没测。Preview 通过不等于 Production，更不等于长期 SLA。

## 闭卷自检

1. 为什么 `go test ./trading/...` 显示 package `ok`，仍不能说 PostgreSQL E2E 本次通过？
2. C1 与 P4 的证据差别是什么？
3. 哪个测试证明同 cancel ID 在订单已 closed 后仍返回原结果？
4. 哪个测试真实检查 fund event batch = 1、ledger entries = 2、净额 = 0？
5. RPC cursor、PostgreSQL feed 和浏览器 reconnect 三层分别有什么证据？
6. 哪三项是当前优先补的可靠性闭环？
