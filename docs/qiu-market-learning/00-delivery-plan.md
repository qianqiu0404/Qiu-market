# Qiu Market 学习资产交付计划

- 基线提交：`882951845c1c0f247b9c80bdf3b6173fb6b13d22`
- 交付范围：`docs/qiu-market-learning/**`
- 产品边界：单用户、虚拟资金、单一 `BTC-USDT` 现货纵切片
- 当前状态：`implemented`（计划已记录；课程材料与个人掌握状态按下表分别验收）

## 问题与可见结果

现有代码已经覆盖行情身份、撮合、幂等、账本和恢复，仓库也保存了工程文档与分层测试，但学习者仍需要在多个目录之间跳转，容易把“代码存在”“测试通过”“真实依赖联调”和“生产可用”混成一个结论。

本交付把同一批事实整理成可学习、可复述、可审计的课程资产。完成后，Owner 应能：

1. 90 秒讲清 `submitted/unknown` 与钱包 `broadcast_unknown` 的共同原则和不同权威事实；
2. 从可靠性矩阵定位 submit、cancel、fund、部分成交、账本、重连和恢复的真实测试入口；
3. 对五个主题各做一次 60 秒闭卷复述；
4. 不看文件树，按三至五个入口解释每条业务调用链、关键不变量和故障恢复；
5. 明确说出哪些结论只是 `implemented` / `build-verified`，哪些已有 `integration-verified`，哪些仍是 `environment-pending` 或 `production-recommendation`。

## Canonical 来源与课程边界

本目录是学习编排层，不另造实现真值。工程事实仍以下列文档和代码为准：

- [交易系统 canonical 工程文档](../trading-system.md)
- [Unknown Outcome 工程 ADR](../adr/0001-unknown-outcome-reconciliation.md)
- [现有 Unknown Outcome 测试矩阵](../unknown-outcome-test-matrix.md)
- [行情架构 canonical 工程文档](../market-service-architecture.md)
- [行情质量 canonical 工程文档](../market-data-quality.md)

被拒绝的方案是复制一套“更好读但脱离代码演进”的第二份系统说明。课程材料会引用上述真值、补充学习顺序和闭卷训练；代价是阅读时需要沿链接回到 canonical 文档，收益是避免两套实现口径静默漂移。

`README.md` 索引本应在材料完成后更新，但当前 Accord 中存在重叠 claim；在该 claim 释放前不写 README。此项属于协调边界，不影响独立课程目录交付。

## 材料目录与逐目标提交

| 顺序 | 目标文件 | 交付内容 | 独立提交标准 |
|---|---|---|---|
| 0 | `00-delivery-plan.md` | 目录、入口、实验、证据等级、自测标准 | 计划本身先提交 |
| A | `01-adr-cex-unknown-vs-wallet-broadcast-unknown.md` | CEX `submitted/unknown` 与钱包 `broadcast_unknown` 对照 ADR | 决策、替代、成本、边界和 60 秒复述齐全 |
| B | `02-trading-reliability-test-matrix.md` | submit/cancel/fund、partial fill/cancel race、reconnect/reconcile、账本、快照/事件恢复矩阵 | 每项有真实测试入口、测试粒度、当前证据等级和缺口 |
| C | `03-interview-rehearsal.md` | 一份 90 秒总口述与五份 60 秒章节复述 | 每段能闭卷完成并带追问 |
| D | `04-code-learning-map.md` | 五主题代码学习地图 | 每章含业务流程、调用链、结构、不变量、失败边界、实验、自测与掌握状态 |
| E | Obsidian 目标讲义 | 增量整合 A–D，并关联两篇已有讲义 | 仅在 Accord 网络同步成功且目标 note claim 可用时写入 |

每个仓库目标使用一个本地提交并执行一次 Accord capture；禁止 push、merge、deploy。Obsidian 是仓库外资产，不用仓库提交伪装成已落库。

## 五个主题的真实代码入口

以下入口均存在于基线提交；学习地图会解释调用顺序，不罗列整个仓库树。

### 1. 行情身份与质量

1. `crawler/catalog_supervisor.go::resolveDiscoveredMarkets`：provider symbol 必须通过已审核 alias 才能解析为 canonical asset。
2. `marketdata/snapshot_writer.go::SnapshotWriter.Write`：PostgreSQL 先决定接收、丢弃或修正，Redis 只做后派生。
3. `marketdata/composite.go::BuildComposite`：只使用新鲜 Spot，执行稳定币率、3% 中位数离群和 venue 权重门。
4. `database/market_aggregation.go::QueryMarketPriceTicks`：按 `all/composite_spot`、CEX `venue_spot`、DEX `dex_route` 分离读模型。
5. `services/http/service/market_index.go::GetMarketPriceTicks`：把可用性、来源时间和 freshness 明确输出到 HTTP 契约。

### 2. 订单幂等与未知结果

1. `frontend/src/views/Trade.vue::submitOrder/cancelOrder/fundVirtual`：发送前固定 request identity，传输不确定时保存 pending。
2. `frontend/src/views/Trade.vue::reconcilePendingWrite`：同一 actor 使用原 ID 查询或重放。
3. `trading/exchange/exchange.go::Fund/Submit/Cancel/runLocked`：构造四元组幂等键，先返回历史结果，再试算新命令。
4. `trading/store/postgres/store.go::Append`：串行事务、stream CAS；commit 返回不确定时抛出 `ErrCommitOutcomeUnknown`。
5. `trading/runtime/runner.go::recoverAfterPersistenceError`：停止正常写入、从事件真值恢复，再回到 ready。

### 3. 部分成交与撤单竞态

1. `trading/orderbook/orderbook.go::Book.Match`：价格时间优先、同价 FIFO 和部分成交。
2. `trading/exchange/orders.go::applySubmit`：冻结、撮合、maker/taker 状态更新和剩余挂单。
3. `trading/exchange/orders.go::settleFill`：按成交粒度结算两种资产与 Maker/Taker 费用。
4. `trading/exchange/orders.go::applyCancel`：只对仍 open/partially-filled 的剩余义务解冻一次。
5. `trading/rpc/server/server.go::CancelOrder`：先做 owner 校验，把“同 ID 已提交撤单”的幂等判断留给 exchange。

### 4. 双重记账

1. `trading/ledger/ledger.go::Ledger.Post`：每资产分录和必须为零，失败事务不改变余额。
2. `trading/ledger/ledger.go::FundVirtual/Hold/Release`：treasury、available、held 之间的显式资金移动。
3. `trading/exchange/orders.go::settleFill`：买方 BTC、卖方 USDT、两种手续费账户同时入账。
4. `trading/exchange/exchange.go::buildProjection`：从试算状态生成余额、订单、成交投影。
5. `trading/store/postgres/store.go::applyProjection`：事件批次、ledger 和投影同一 PostgreSQL 事务提交。

### 5. 快照、事件与恢复

1. `trading/exchange/exchange.go::Restore`：校验快照 hash，顺序重放事件并逐批核对结果、账本、投影和 state hash。
2. `trading/runtime/runner.go::NewMarketRunner/handle`：恢复成功后才 ready；每市场单 goroutine 串行命令。
3. `trading/store/postgres/store.go::RecordsAfter/Save/Load`：事件和快照持久化边界。
4. `trading/store/postgres/store.go::PublishOutboxBatch/FeedAfter`：outbox 到持久 event feed 与 cursor checkpoint。
5. `trading/rpc/server/server.go::SubscribeEvents`：客户端从 `(sequence,event_index)` 之后续读。

## 实验计划与证据升级规则

| 实验 | 命令或入口 | 能证明什么 | 不能证明什么 |
|---|---|---|---|
| 基线与路径审计 | `git rev-parse HEAD`；`rg --files`；`rg -n '^func Test' trading` | 基线与引用路径真实存在，属于 `implemented` 审计 | 不证明行为运行正确 |
| Go 分层测试 | `go test ./trading/...` | 本次机器上的纯 Go 契约达到 `build-verified`；输出中被 skip 的 PostgreSQL 测试不升级 | 不证明真实 PostgreSQL、HTTP 或浏览器链路 |
| 关键竞态/恢复测试 | 精确运行 runner、exchange、ledger、orderbook、RPC 测试名 | 相应单层不变量达到 `build-verified` | 不能把组合缺口写成端到端证据 |
| 前端单测 | `cd frontend && npm test -- --run` | pending、传输不确定和 API 契约达到 `build-verified` | mock fetch 不等于真实 BFF |
| 浏览器契约 | `cd frontend && npx playwright test e2e/trading.spec.ts` | 受控 mock 后端下的刷新、同 ID 核对和 UI 行为达到 `build-verified` | 不等于 OAuth、Vercel 或真实 PostgreSQL |
| PostgreSQL E2E | `S78_TEST_POSTGRES_DSN=... go test ./trading/e2e ./trading/store/postgres` | 只有真实临时 PostgreSQL 实际运行且断言通过才是 `integration-verified` | 未设置 DSN 而 skip 不能算通过；本任务不读取私有 `.env` |
| 真实 Preview Gate | `frontend/scripts/run-preview-oauth-gate.mjs` + 对应受管 verifier 证据 | 精确 deployment/commit 下真实 OAuth、BFF 与三种 unknown 写可达到 `integration-verified` | Preview 通过不等于 Production promotion 或长期可用 |
| 文档验收 | 链接/测试名定位检查、`git diff --check` | 文档结构和仓库引用自洽 | 不升级任何运行时证据 |

统一只使用以下读者可见等级：

- `implemented`：行为、测试或材料存在于基线代码；
- `build-verified`：本地构建、静态检查或自动测试真实运行通过；
- `integration-verified`：真实依赖交换数据且结果被核对；
- `environment-pending`：仍需要 OAuth、Preview/Production、数据库、部署或时间窗口；
- `production-recommendation`：建议或目标，不是当前行为。

测试粒度会另列为 unit、contract、integration harness 或 live gate，不能用自造等级替代上述证据等级。

## 自测与掌握标准

### 单章 60 秒

每个主题必须在 60 秒内、不看文档完成四件事：

1. 说出业务问题和用户可见结果；
2. 按顺序说出三至五个代码入口；
3. 说出至少两个不变量和一个失败恢复；
4. 指出一个真实测试名，并准确描述它的证据等级与缺口。

### 总体 90 秒

必须讲清：

- 超时为何是 unknown 而不是失败；
- CEX 与钱包分别查询什么权威事实；
- partial fill 后 cancel 只能释放剩余 held；
- 双重记账怎样保证每资产对平；
- 快照、事件、state hash 和 cursor 怎样共同恢复。

### 闭卷通过条件

- 五章各完成两次 60 秒复述；
- 90 秒总口述连续完成两次；
- 随机抽三道题，能从业务语义落到代码入口和测试入口；
- 不能把 `go test`、mock Playwright、Preview Gate 或设计建议说成 Production 证明。

材料写完只标记 `implemented`；只有上述闭卷标准通过，个人掌握状态才从 `not-started` / `learning` 升为 `mastered`。

## 写入、失败与恢复边界

- docs claim 冲突：停止对应文件写入，不使用 `--force`。
- README claim 未释放：保持 README 不变，在最终 handoff 记录待补索引。
- Obsidian 同步或 claim 未确认：不写目标讲义，只报告 `environment-pending`。
- 测试需要私有 DSN、OAuth 或部署：不读取 `.env`，保留 skip/blocked 的原始事实。
- 任一引用无法在 `8829518` 定位：修正文档，不用近似文件名补齐。

