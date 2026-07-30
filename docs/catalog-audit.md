# Catalog Audit 与 provider 灰度发布

## 解决什么问题

交易所说“BTC”，只能证明它给了一个代码，不能自动证明它就是系统里的 canonical BTC。S78 把“发现市场”“审核身份”“允许写正式行情”拆成三步，避免同 symbol 不同资产、下架重上架或错误链上合约造成静默串价。

可观察结果：

- CoinGecko Top 200 作为候选资产池；每家 CEX 从自己真实可交易、身份已审核的候选中维护独立 50 资产选择；
- `catalog/provider-asset-mappings.yaml` 显式记录候选池所需的 provider alias 与 chain contract，不能靠 symbol 临时猜身份；
- 对既有 canonical 身份可以显式写 `canonical_asset_id`；当前 USDT 锁定旧系统的 `a3`，避免 CoinGecko 导入再造一条同名资产；
- Coinbase/Bybit/OKX 即使发现数百个市场，未审核或 rollout=shadow 时正式启用数仍为 0；
- System → Catalog Audit 显示 provider、source market、rank、alias review、解析原因和 rollout；
- Provider 页固定读取自己的版本化 50 资产选择；All 读取七家选择的 canonical `asset_id` 去重并集并按市值排序，没有可信报价时保留行并解释原因。

## 身份与发布术语

| 模型 | 准确含义 | 大白话 |
|---|---|---|
| `provider_market_candidate` | CEX/Hyperliquid 发现的原始市场和解析结果 | 新商家登记表 |
| `asset_alias` | provider 代码到 canonical asset 的审核映射 | 确认昵称属于谁 |
| `asset_external_mapping` | provider 外部 ID 到 canonical asset | 外部身份证号对照表 |
| `asset_representation` | canonical asset 在某条链上的审核合约 | 链上的合法分身 |
| `provider_rollout_state` | 每家 provider 的 shadow/canary/enabled/paused | 每家独立的灰度闸门 |
| `provider_asset_selection_state` | provider 当前激活的选币版本、目标数和候选数 | 交易所当前采用哪一版菜单 |
| `provider_asset_selection` | 某一版本内的准确 `asset_id`、选择序和替换时间；七家都要求正好 50 | 带版本号的菜品清单 |

`shadow` 会发现、校验并每 30 秒探测一次已审核资产的批量 ticker；探测只把 received/matched/priced/24h-complete 等有界计数写入 `market_provider_status.details`，绝不写 `symbol_market`、`asset_venue_snapshot` 或综合价。`canary` 固定 10 个审核资产，CLI 会按当时的 market-cap rank 生成建议并把人工确认后的准确 `asset_id` 写进状态，之后排名、目录或 DEX 路线新鲜度变化都不能静默换人。`2026081100013.sql` 会先审计旧的空 Canary：只有当前 enabled 市场恰好归属 10 个唯一、已审核的 Top 50 资产时才原子固化，否则迁移停止并输出 provider；数据库 CHECK 约束随后禁止再次写入空或非十项 Canary。rollout 切换后 CLI 立即用最后一次成功目录 reconcile，crawler 每 30 秒再做一次低成本兜底，不再等待六小时目录刷新。`enabled` 允许 rank limit 内的审核资产；`paused` 停止正式发布。Hyperliquid 和 AMM 也读取同一闸门：技术目录可以继续审计，但 Perp/route 不得绕过 rollout 出现在公开抽屉和市场计数。旧全局 `MARKET_MULTI_VENUE_ENABLED` 只作一版兼容，不再决定正式启用。

`local_preview_enabled` 是与上述状态机正交的本地开发开关。开启时四家 CEX 分别读取自己的激活选币版本，状态源固定为 `spot-tickers-preview`；Hyperliquid 使用 `metaAndAssetCtxs-preview`，两家 AMM 使用 `route-quotes-preview`。正式 mode、Canary 清单、transition/soak 时间和正式 source 计数完全不变。readiness evaluator 会增加硬 blocker，所以预览永远不能晋级。关闭时清空预览成功时间、撤回预览发布范围，再按正式 rollout reconcile，并把正式 feed 观察窗口归零。

## 端到端流程

```text
CoinGecko Top 200
  -> canonical asset + external mapping + metrics
  -> apply code-reviewed manifest
  -> approved alias / chain representation

CEX directory
  -> provider_market_candidate
  -> tradable + USD family + approved base/quote + Top 200?
  -> resolved
  -> provider-local versioned selection (target 50)
  -> provider rollout allows asset?
  -> exchange_symbol enabled
  -> ticker adapter -> SnapshotWriter

DEX Subgraph / reviewed public discovery
  -> reviewed chain contracts only
  -> V2/V3 pool candidate + versioned onchain identity check
  -> direct / max-two-hop route quote
  -> identity-reviewed listed assets
  -> provider-local versioned selection (exactly 50)
  -> quote eligibility is evaluated separately
  -> provider rollout/preview allows asset?
  -> asset_venue_snapshot
```

目录首次失败按 provider 独立从 30 秒退避到 10 分钟；成功后仍每 6 小时全量刷新。一次响应缺项不能自动下架；只有上游明确返回不可交易/解析失败的当前候选才会停用对应市场。

## 安全 CLI

命令不会暴露数据库审批 HTTP 接口：

```bash
# 读取并检查代码评审 manifest，不连接数据库
./market-services catalog apply-mappings \
  --file catalog/provider-asset-mappings.yaml \
  --dry-run

# 检查或显式生成各 provider 的独立选币版本
./market-services catalog select-assets \
  --provider binance,coinbase,bybit,okx --limit 50 --dry-run
./market-services catalog select-assets \
  --provider binance,coinbase,bybit,okx --limit 50 \
  --reason owner-approved-refresh
./market-services catalog select-assets \
  --provider hyperliquid,uniswap,pancakeswap --limit 50 --dry-run

# 读取真实数据库审计
./market-services catalog audit --provider coinbase --rank-limit 50
./market-services catalog rollout-status --provider coinbase --rank-limit 50 --json

# 串行进入 canary；默认最短观察 24h
./market-services catalog rollout \
  --provider coinbase \
  --mode canary \
  --rank-limit 50

# 在 DEX 进入 Canary 前验证只读 RPC、chain ID、最新区块、
# V2 Factory/Router、V3 Factory/QuoterV2 合约代码和 Subgraph _meta
./market-services catalog endpoint-check --provider uniswap
./market-services catalog endpoint-check --provider pancakeswap

# 仅本机 PostgreSQL + 显式环境保护下可用
S78_LOCAL_PREVIEW=1 ./market-services catalog preview \
  --provider binance,coinbase,bybit,okx,hyperliquid,uniswap,pancakeswap \
  --enable
```

### 源码可重建与 fail-closed 边界

`cmd/market-services/catalog.go` 是上述命令的受版本控制入口。仓库只忽略根目录构建出的 `/market-services` 二进制，不再用无锚点规则忽略同名源码目录；因此 `NewCli → catalogCommand → 具体 action → database/crawler` 能从任意干净 checkout 重建。被拒绝的替代方案是删掉 `catalogCommand()` 调用、提交一个只返回成功的 stub，或继续依赖某台机器上被忽略的源码/旧二进制：这些做法虽能让编译变绿，却会让 operator 以为审核与灰度闸门仍然存在。

Catalog 子命令只注册自己使用的配置。`apply-mappings --dry-run` 只解码并校验 manifest，不要求 HTTP、Redis 或数据库配置，也不建立数据库连接；数据库读写命令缺少 host/name 时立即拒绝；`endpoint-check` 只读取对应链的只读 endpoint 配置。代价是 Catalog 维护一组从公共 flag 定义复制出的可选 CLI flag 元数据，但避免了无关服务依赖阻断离线审核。正式 rollout 写入仍必须通过同一个 `EvaluateProviderRolloutReadiness`，Local Preview 仍要求 `S78_LOCAL_PREVIEW=1`、loopback PostgreSQL 且显式二选一 enable/disable。

不传 `--file` 时兼容使用编译进二进制的同一份清单；不带 `--dry-run` 才写入审核过的 alias 与 representation，并把清单路径保存在 review source 中供审计。

`rollout` 会拒绝：

- 非法 mode/rank limit；
- 前序 provider 尚未 enabled；
- 前序或当前最短 soak 尚未结束；
- 把 canary 的 24 小时或 enabled 的 48 小时最短观察期改短；
- canary 不是恰好 10 个审核且可覆盖的资产；
- canary → enabled 没有至少 100 次当前观察窗口内的行情尝试；
- 从首个真实 feed attempt 起没有跑满当前阶段完整的 24/48 小时；rollout 命令执行时间不能冒充观察开始时间；
- CEX/Hyperliquid 成功率低于 99%，或 AMM route quote 成功率低于 98%；
- 最近成功已过期、仍有连续失败，或任一 rollout 资产缺少新鲜正式 venue snapshot；
- 身份映射冲突。

每次 rollout 变更都会重置该 provider 的 `attempt_count/success_count/observation_started_at`，所以旧阶段的成功不能冒充新阶段 soak。`observation_started_at` 在首个真实 feed attempt 才写入；CEX 还必须先有 rollout 允许的正式 market，不能用“目录还没激活但批量接口请求成功”提前启动计时。`market_provider_status.next_retry_at` 记录独立 supervisor 的下一次重试。System 的 provider 总状态只跟当前模式对应的 primary source：CEX shadow 看 `spot-tickers-shadow`，canary/enabled 看 `spot-tickers`；Binance K 线、目录或遗留 source 只留在能力矩阵里，不能把正常 ticker 降级。`rollout-status` 与写命令复用同一个 readiness evaluator，`ready=false` 时写命令必然拒绝；System 不提供无鉴权写入口。

任何 conflict 都输出审计错误，不自动改名或覆盖人工审核。manifest 只允许修复 `migration-existing-catalog` 产生的旧 Hyperliquid provider-local identity。

## 设计决策、替代方案与代价

1. **manifest 进代码评审。** 被拒绝的是“symbol 唯一就自动 approved”；代价是需要维护映射，但身份变化有审计证据。
2. **每 provider 独立 rollout。** 被拒绝的是一个全局 bool 同时切四家 CEX；代价是状态和操作更多，但 Coinbase 失败不会阻止 Binance，也不会把 Bybit 一起切流。
3. **每家独立选 50，All 做 canonical 并集。** Provider 页不再被 CoinGecko 全局 Top 50 限死；All 也不把七家市场行直接堆成重复 BTC，而是按 `asset_id` 去重。代价是 All 行数动态大于 50，且必须维护 selection version，但这正是聚合服务的真实覆盖。
4. **选择成员与瞬时行情解耦。** 市值排名变化或一次 ticker 失败不会换币；只有显式刷新，或已选资产不再是审核过的 Top 200 可交易候选，才生成新版本并给旧版本写 `replaced_at`。
5. **保留 legacy market_id。** Binance 原六个市场在扩展目录时按 exchange + source_symbol 复用，避免唯一 market_code 冲突和历史审计断裂。
6. **链上身份单独建模。** DEX token/pool/route 不复用 CEX market，代价是模型更多，但 chain/contract/fee/path 不会丢失。
7. **显式合并重复 canonical asset。** `2026080700009.sql` 只在 CoinGecko `tether` 指向非 `a3` 时执行，先写 `asset_identity_merge_audit`，再迁移引用并删除重复行；被拒绝的是放松唯一约束或让运行时静默任选一个 USDT。
8. **灰度证据从真实观察开始。** 被拒绝的是把 rollout 命令时间当成 feed 已经运行；代价是目录或 adapter 晚启动时 promotion 也会相应顺延，但不会把未运行的几小时算进 canary。
9. **公开市场读模型再次检查 rollout。** 被拒绝的是只依赖 `exchange_symbol.is_active`；后者会让刚 paused 的旧市场在下次目录刷新前继续出现在抽屉和计数。现在数据库读模型按 enabled/fixed-canary 二次过滤。
10. **DEX listing 与 quote eligibility 分离。** DEX 必须先用 reviewed chain contract、ERC-20 decimals 和 V2/V3 pool identity 建立至少 50 个可审计 listed assets，再固定选择 50；TVL、成交量、区块新鲜度、impact、spread 和 `$10K → $1K → $100` 逐跳 Router/Quoter 只决定当前能否报价。被拒绝的是按 symbol 猜身份，以及为了填价格而放宽询价门槛。
11. **K 线 market selection 版本化。** provider 的 50 资产菜单不能直接回答“从哪个具体交易对取历史”。`provider_kline_selection` 对每个 selection version 固定一个 USD-family Spot market；目录顺序变化不允许静默换来源。

## 关键代码入口

1. `migrations/2026081500017.sql` 至 `2026082000022.sql`：增加版本化 provider/DEX/K 线 selection、快照 last attempt/success、pool listing/quote eligibility 与 V2/V3 protocol path。
2. `cmd/market-services/catalog.go` + `catalog/provider-asset-mappings.yaml` + `catalog/manifest.go`：可从干净 checkout 重建的安全 CLI，以及代码评审过的 Top 200 候选身份授权。
3. `database/venue_aggregation.go`：原子生成/切换选币版本，保留历史版本和最后成功报价。
4. `crawler/catalog_supervisor.go` + `crawler/spot_ticker_supervisor.go` + `crawler/spot_ticker_streams.go`：先确保 provider 选择，再启用目录并隔离 WebSocket primary/REST reconcile。
5. `database/market_aggregation.go` + `frontend/src/views/Markets.vue`：`provider_top50` 与 `provider_union` 两种读模型及可解释新鲜度。

## 故障、降级与恢复

| 场景 | 行为 | 恢复 |
|---|---|---|
| CoinGecko 暂时失败 | 保留最后一次成功 metrics/目录 | 5 分钟后重试 |
| CEX 首次 EOF/429 | 仅该 provider 30s→10min 退避 | 成功后回到 6h 目录周期 |
| alias ambiguous | candidate 留审计，不创建正式 market | 更新 manifest 并复审 |
| market 明确下架 | 对应市场 inactive | 上游恢复 tradable 后再启用 |
| rollout shadow/paused | 正式 venue snapshot 为 unavailable | 通过 CLI 串行恢复 |
| Local Preview provider 失败 | 只移除该 provider contributor；其余 provider 继续形成综合价 | 独立退避后自动恢复 |
| ticker 单轮失败 | 更新 `last_attempt_at/last_error_class`，保留最后成功值；30s 内仍 Fresh，30s–5m 为 Stale，超过 5m Unavailable | 下一次成功原地恢复 Fresh |
| 已选资产排名变化 | 不换选币版本 | 显式 CLI 刷新才按新顺序选币 |
| 已选资产明确失效 | 生成新版本，旧版本写 `replaced_at` | 新版本原子切换，不出现半套列表 |
| Local Preview 关闭 | 预览快照 unavailable，正式观察重新从零累计 | 正式 adapter 按 rollout 恢复 |
| rollout 已切换、目录尚未激活 market | CLI 立即基于最后成功目录 reconcile；失败时不启动 observation clock | 修复目录后 30 秒兜底 reconcile |
| DEX endpoint 错链、区块过期或合约无代码 | `endpoint-check` 失败且不打印 URL/key | 修复只读端点后重试 |
| fixed canary 资产身份失效 | 整个 canary 失败关闭并报告资产 ID，不自动补另一个币 | 修复 manifest/目录后重新开始 canary |
| ticker/route 连续失败 | 保留最后可信状态并写 `next_retry_at` | 独立退避成功后清零失败与 retry |
| legacy market_code 冲突 | 复用原 market_id/symbol_id | 保留历史与 K 线关系 |
| CoinGecko 重复创建 USDT | 迁移因 `a3` 缺失/非 USDT 而停止 | 核对审计后再迁移，绝不按 symbol 静默合并 |

## 验证与证据边界

- `implemented`：provider 级版本化 50 资产选择、七源去重并集、AMM listing/quote eligibility 分离、manifest、固定 canary、七源 shadow/preview 隔离、人工 promotion gate、能力级 System 状态和最后成功值保留已存在。
- `build-verified`：以本次交付记录的状态机、公开读模型、source cadence、DEX 覆盖、Catalog CLI 干净源码重建、RPC 首次失败恢复、日志流式轮转及完整工程门禁为准。
- `integration-verified`：当前业务库已执行到 `2026082000022.sql`。2026-07-25 本地动态快照中七家 selection 均为 50，All 七源并集为 109 个 canonical 资产且分页无重复；四家 K 线各映射 50 个 market，真实 CEX feed、Perp、公共发现、V2/V3 合约与 Router/Quoter 数据已经交换。这些数量不是永久覆盖承诺。
- `environment-pending`：真实 provider 的 100+ 次正式观察与 24/48/72 小时 promotion、私有只读 DEX endpoint 正式路径、AMM 24h 路线观察和最终七天。

## Owner 60 秒解释

> 七个 provider 都先建立身份审核通过的候选，再冻结成一个带版本号的 50 资产选择。All 取七家选择的 `asset_id` 并集，所以 BTC 仍只有一行，再按市值统一排序。Ticker/Quoter 失败不会删成员，而是保留行并从 Fresh 降为 Stale、Unavailable 或 Not covered；只有 Fresh CEX Spot 报价进入综合价和涨跌榜。Catalog CLI 的 dry-run 不连库，正式 rollout 必须通过同一个 readiness evaluator；本地预览只让这套产品立即可看，不会冒充正式灰度验收。

## 闭卷自检

1. discovered、resolved、enabled 分别证明了什么？
2. 为什么 symbol 唯一仍不能自动 approved？
3. `provider_rollout_state` 比全局 bool 解决了什么故障边界？
4. 为什么 Top 50 查询必须从 `asset_metric_current` 出发？
5. Binance 原六个市场为什么要复用 market_id？
6. DEX contract 为什么不能放进 `exchange_symbol`？
7. 为什么 USDT 重复身份要做带审计的显式合并，而不是删除唯一索引？
8. 为什么达到 soak 时间仍不等于可以 promotion？
9. 为什么 `catalog` 和 `spot-tickers` 必须使用不同 freshness 阈值？
10. 为什么 canary 资产必须落库固定，而不能每轮重新取当前排名前十？
11. 为什么 `exchange_symbol.is_active=true` 仍不足以证明市场可以出现在公开抽屉？
12. 为什么 provider 页按“已发布集合”过滤，却不能按“当前有新鲜价格”过滤？
13. 为什么 shadow ticker 探测不是正式行情发布？
14. 为什么 System 中旧 K 线失败不能把正常 Spot ticker 标成 Stale？
15. 为什么 Local Preview 不等于 Enabled？
16. 为什么四家 CEX 的 50 可以不同，而 All 仍不会出现重复 BTC？
17. `selection_version/selected_at/replaced_at` 分别用于回答什么审计问题？
18. 为什么市值排名变化不能自动替换一个健康的 provider selection？
19. 为什么 DEX selection 必须固定 50，但实时可询价数量可以少于 50？
20. 为什么 provider 资产 selection 之后还需要独立的 K 线 market selection？
21. V2/V3 protocol path 为什么必须落入 route 审计，而不能只显示一个最终价格？
22. 为什么只忽略根目录 `/market-services` 二进制，而不能忽略任意名为 `market-services` 的路径？
