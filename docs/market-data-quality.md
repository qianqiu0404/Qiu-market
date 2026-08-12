# 行情数据质量：多 provider 隔离、综合价降级与修复

## 核心原则

行情数据就是产品。S78 不用 mock、CoinGecko 当前价或假 `0%` 掩盖数据源异常；它保存来源时间、排除原因、confidence 和恢复路径，让调用方知道“有没有值、值来自哪里、还新不新”。

## R2I 覆盖计数与查询三态

R2I 修复的是 read model 的真实性，不增加 provider，也不改变 provider 的地域、许可或
rollout 边界。`QueryAssetIndexSummary` 对同一个 provider-selection union 做一次聚合：
`displayed_asset_count` 是 display predicate 为真的行数，`unpriced_asset_count` 固定等于
`asset_count - displayed_asset_count`。不能写成 `COUNT ... WHERE NOT(predicate)`，因为
没有 snapshot 的资产会让 SQL predicate 变成 `NULL`，而 `NOT NULL` 仍是 `NULL`；这些行
会从 priced 和 unpriced 两边同时消失。真实 PostgreSQL fixture 固定构造 106 个已选择资产，
其中 61 个有新鲜 composite snapshot、45 个没有 snapshot，并同时对账 overview 与两页
dashboard。这个 `106 / 61 / 45` 是历史回归切片，不是生产 cardinality 上限；七源 selection
加入后 All canonical union 可以增长或收缩，但快照只接受 Top-200 范围内的 1 至 200 行，
并始终逐行重算三态守恒。验收继续单列 Top20/50/106，防止扩展 universe 稀释原有覆盖问题。

Catalog 与 rollout reconcile 继续分层。`ReconcileResolvedSpotMarkets` 从最新一条成功
catalog row 读取 `last_seen_at`；零行意味着该 provider 当前部署没有可用 catalog，返回
零 enabled、保留 provider 的 HTTP 451/403 状态，不再把 PostgreSQL aggregate 的 `NULL`
扫描成内部错误。这里拒绝的替代方案是代理、换地区出口或用另一家 provider 填数据：它们
都会越过许可/身份边界，并制造不存在的覆盖。

Markets 搜索按当前 query key 表达三个互斥状态：当前请求尚未结算时显示 loading；成功且
结果确实为空时显示 search empty；所选 provider 的 `published_asset_count=0` 时显示
`unavailable in this deployment`。旧查询的 data/loading/error 不得代替新查询状态，慢请求
期间也不能先显示“无结果”。每个价格仍必须携带 provider、时间和 freshness；这些 UI 状态
只解释查询进度与来源可用性，不制造价格。

关键入口按顺序是：

1. `database/market_aggregation.go`：从同一 union 计算 displayed/unpriced 守恒计数；
2. `database/venue_aggregation.go`：把零 catalog 解释为 provider unavailable，而非 SQL 错误；
3. `services/http/service/market_index.go`：原样输出 overview/dashboard 的只读事实；
4. `frontend/src/views/Markets.vue`：按 query key 渲染 loading、empty、provider unavailable；
5. `database/market_aggregation_integration_test.go` 与 `frontend/e2e/markets.spec.ts`：分别锁定
   106/61/45 和慢搜索三态。

失败与恢复：dashboard 请求失败显示当前 query 的 error，不复用旧查询的空态；catalog
之后真实恢复时，下一次成功 discovery 写入候选行，30 秒 reconcile 才会重新 enable 合法
selection；没有 provider 成功前，45 个 unavailable 仍保持 unavailable。Owner 60 秒说明：
overview 的 unpriced 是总数减 displayed，因此没有 snapshot 的 SQL NULL 也会被计入；零
catalog 是已知 provider 降级，不是数据库故障；页面只有当前查询返回后才能宣称 empty，
restricted provider 继续明确不可用，所有价格来源和 freshness 边界不变。

闭卷检查：

- 为什么 `NOT(nullable_predicate)` 会漏掉没有 snapshot 的资产？
- overview 与 dashboard 必须共享哪个 universe 才能对账？
- 零 catalog 为什么返回零 enabled，而 451/403 仍必须保留？
- 为什么慢搜索不能使用轮询 composable 的全局 `loading` 直接决定 empty？
- provider unavailable 与真实 search empty 的判据分别是什么？

## 质量事实与位置

| 事实 | 含义 | 项目落点 |
|---|---|---|
| `observed_at` | 本服务成功接收并解析上游响应的时间 | `symbol_market` |
| `source_time` | 上游明确提供的时间；没有就 NULL | `source_time/source_time_kind` |
| process status | 角色进程是否仍刷新心跳 | Redis heartbeat / System |
| provider status | 最近尝试、成功、失败串和错误分类 | `market_provider_status` |
| identity status | 市场是否可唯一解析到 canonical asset | `provider_market_candidate` |
| contributor/exclusion | 市场是否参加综合价及原因 | `asset_price_index` JSONB |
| confidence | 合格 Spot venue 数 | low / medium / high |
| kline gap | 业务 `open_time` 是否连续 | worker + repair task |

## Q-M6A 统一质量门：计划、指标字典与 SLO

Q-M6A 只增加只读质量判断，不改变任何行情事实、研究事件或交易状态。实施顺序固定为：先把 provider/research 的已验证响应转换为不可变 evidence；再按数据类别独立评分；随后由 quarantine 状态机决定只读 read model 是否可消费；最后由独立 API/UI 展示状态与原因。质量层不保存或修改上游原始事实，也没有进入 `trading/reference`、matcher、orders、balances 或 ledger 的依赖方向。

三个类别不能合成一个平均分：

1. `binance_spot`：BTC/USDT ticker 与 1m OHLCV，各 capability 保留自己的样本和 freshness。
2. `coinglass_derivatives`：BTCUSD_PERP OI 与 liquidation；funding 仍是 typed unsupported，不放进成功率分母，但必须显示 capability gap。
3. `xiuqiu_research`：动态 Market Radar 事件；priority 只表示研究编辑优先级，固定不可执行。

### 指标字典

所有窗口都是 UTC 半开区间 `[start, end)`；计数是整数，比例以 basis points 表达，延迟用整数毫秒。每个维度同时返回 numerator、denominator 和 value，不能只返回一个失去分母的百分比。`denominator=0` 或样本少于 `min_samples` 时该窗口为 `insufficient`，不得用 100% 填充。

| 维度 | 精确定义 | 失败/空数据语义 |
|---|---|---|
| availability | 成功 fetch 数 / 总 fetch attempt 数 | 无 attempt 为 insufficient；429、timeout、5xx 均进入错误分布和失败分母 |
| freshness | 在 capability freshness budget 内的有效样本 / 有效样本 | 无有效样本为 insufficient；future、stale serve 是 hard fault |
| fetch latency | 成功 fetch 的 total/max latency 与 SLO 内样本数 | cache hit 单列，不能冒充一次上游成功 |
| completeness | 真实存在的必需字段 / 按 capability 定义的必需字段 | 0 条事件没有字段分母，不能得到 100% |
| schema validity | schema、时间、decimal、单位与精度均有效的样本 / 收到样本 | bad payload、单位或精度冲突 fail closed |
| consistency | 去除 duplicate 后且无 conflict/out-of-order/future 的样本 / 收到样本 | duplicate 可审计；conflict/future 是 hard fault |
| coverage | observed capability/exchange / policy expected set | unsupported 不伪装成功；CoinGlass funding 作为显式 gap |
| source/provenance | provider、source、market identity 与时间语义完整的样本 / 收到样本 | venue/provider/spot/perp 不能靠 symbol 猜测 |
| research context | source、watchFor、invalidation、priority、content hash 的逐字段计数 | legacy 分开计数；同 ID 不同 content hash 为 conflict |
| license eligibility | `approved|unknown|restricted|prohibited` | 独立硬门，不进入加权平均；unknown 不得标成可公开展示 |

read model 还必须保留 `last_attempt_at`、`last_success_at`、age、attempt/success 数、duplicate/conflict/out-of-order/future/stale 数、429/timeout/5xx 分布、cache hit/stale serve、research legacy 与 P0/P1/P2 分布，以及每个 reason 对应的原始计数。质量 evidence 可以保留，quarantine 只阻止进入可消费 read model，不删除事实。

### SLO 与权重矩阵

默认权重总和严格为 100；所有阈值由经过校验的 policy 提供，零值使用下表安全默认。分数只描述技术质量，license 仍可在高分时禁止公开展示。

| 类别 | evidence window / 最小样本 | freshness / latency 预算 | 权重（fresh / availability / completeness / schema / consistency / coverage） | 公开展示门 |
|---|---|---|---|---|
| Binance Spot | 5 分钟；ticker 5 + OHLCV 2 = source 7 | ticker 5s、闭合 1m candle 65s；fetch 2s | 25 / 20 / 20 / 15 / 10 / 10 | 分数有效、无 hard fault、license=approved |
| CoinGlass derivatives | 5 小时；OI 1 + liquidation 1 = source 2 | source row 5h；fetch 5s | 20 / 20 / 20 / 15 / 15 / 10 | 仅 fixture 时 `not_live`；license=approved 前不可公开 |
| xiuqiu research | 168 小时；summary 1 + events 1 = source 2 | 事件不超过 7d；fetch 5s | 15 / 15 / 20 / 15 / 10 / 25（coverage 含 provenance/watch/invalidation） | verified empty 仍是 no_data；license=approved 且事件合格才可展示 |

grade 阈值为 A=90–100、B=80–89、C=70–79、D=50–69、F<50；样本不足时 grade 为 `insufficient` 而不是 F 或 A。状态固定为 `insufficient|healthy|degraded|quarantined|recovering`。future timestamp、schema/identity/unit/precision 冲突、content hash conflict、stale serve 与 policy 定义的超龄是 hard fault，分数不能覆盖它们。所有类别的 `trade_eligible` 永远为 false。

### Quarantine 状态机

```text
insufficient --enough healthy evidence--> healthy
healthy --soft SLO miss---------------> degraded
healthy/degraded/recovering --hard fault--> quarantined
quarantined --one healthy window------> recovering (1/3)
recovering --next healthy window------> recovering (2/3)
recovering --third healthy window-----> healthy
recovering --fault or empty window----> quarantined
```

默认恢复门是连续 3 个健康 evidence window；空窗口不会增加健康 streak，任一 hard fault 清零。窗口状态和 evidence 继续保留，来源关闭只停止新消费，不删除审计事实。告警必须携带 source、capability、窗口、分母、last success、age 与 reasons，不能只写“质量差”。

### 最小运行手册

1. **告警。** 先看 data class、source/capability、窗口和分母；再看 last success/age、429/timeout/5xx、schema/identity/unit 与 conflict 计数。不能拿进程心跳或 cache hit 代替上游成功。
2. **降级。** soft SLO miss 标 `degraded`；hard fault 或许可门失败标 `quarantined`。页面继续显示原因和最后证据，但不把 quarantined item 放入可消费列表，也不以旧值伪装 fresh。
3. **恢复。** 修复来源后从下一完整 evidence window 开始计数；连续 3 个健康窗口依次为 recovering 1/3、2/3、healthy。中途空窗或故障回到 quarantined。
4. **关闭来源。** 关闭对应 provider/research enabled gate，不删除 evidence，不切换到身份或单位不同的来源；Binance BTC/USDT、CoinGlass BTC/USD Perp 与 xiuqiu research 永不互相 fallback。
5. **保留证据。** 保存聚合计数、窗口、reason、hash 与 source reference；不要把受许可限制的原始 CoinGlass payload、provider key、header 或 xiuqiu 长文本写进质量日志。
6. **只读边界。** 质量 API/UI 只解释来源可用性。即便 technical grade 为 A，`trade_eligible` 仍为 false；任何质量状态都不能直接改变 reference price、订单、撮合、余额或账本。

### 实现与复现入口

- `marketdata/quality` 保存策略、exact counters、独立评分、许可门、Monitor 和三窗口恢复；单来源窗口最多保留 4096 条 immutable evidence，窗口外 evidence 与已评估 ID 一起清理。
- `marketdata/qualityadapters` 只把已规范化的 Binance/CoinGlass provider 结果和 xiuqiu research 结果转成 evidence。HTTP observation 与 dispatch trace 必须同 provider/capability/source；cache hit 使用独立路径，计入 cache 但不增加上游 attempt/success 或恢复 streak。
- `GET /api/v1/data-quality/summary` 固定返回三个 source 与六个 capability。生产没有显式 collector 时返回 `unconfigured`，不会把 fixture 或“进程已启动”冒充真实质量。
- Insights 客户端在渲染前复核 schema、canonical source、全部分子/分母、grade、license、eligibility 与 overall 最坏状态；任何低报 quarantine 或 nullable collection 漂移都 fail closed。
- `make verify-quality-golden` 启动 loopback quality monitor 与真实 Vue/HTTP 链，验证三类状态、desktop/mobile 布局和零 provider/DB/trading 写入。普通 CI 不发网。
- opt-in 只读采样需要同时满足 Go build tag 与显式 flag：`go test -tags=quality_online ./marketdata/qualityadapters -run '^TestOnlineReadOnlyQualitySmoke$' -args -quality-smoke`。它最多执行 Binance ticker/OHLCV 两个逻辑读与 xiuqiu summary/events 两个逻辑读；CoinGlass 只解析官方 fixture 并固定 `not_live`，不读取 key。

## 多 provider 失败隔离

Binance、Coinbase、Bybit、OKX 都使用独立的 WebSocket supervisor：前三者分别订阅官方 ticker stream，Coinbase 订阅 `ticker_batch`。事件只更新每个 source symbol 的 latest map，约 5 秒合并写一次，避免把流式频率直接放大成 PostgreSQL 写放大。每家另有 REST reconcile：周期性对账安静、漏消息或断线资产，不能用 REST 成功冒充 WebSocket 已恢复。单 adapter panic 由本 supervisor 捕获并重启，不会终止 crawler；超时、429、畸形响应和断线都写独立 provider/source failure。

Binance ticker 协议存在一个容易漏测的大小写陷阱：`o` 是 24h 开盘价，`O` 是统计窗口开始毫秒。Go `encoding/json` 会做大小写不敏感候选匹配，因此结构体必须同时显式声明两个 tag；否则后出现的 `O` 可能覆盖 `o`，把时间戳当价格并生成接近 `-100%` 的假涨跌。回归测试必须使用同时包含 `o/O/C/E` 的真实形状。

Catalog 和 ticker 是两条状态：目录成功只能证明市场存在，不能证明报价新鲜；ticker 成功也不能越过 alias 审核创建市场。System 因此必须同时表达 process 与 provider source，不能把一个绿色心跳当成所有交易所可用。

## 综合价质量门

每轮综合价按固定顺序执行：

1. 非 Spot、零价、未来时间、超过 30 秒、缺稳定币率的市场排除。
2. USD 为 1；USDT/USDC 参考率必须在 10 分钟内。
3. 报价偏离候选中位数超过 3% 时排除并记录 `median_deviation_gt_3pct`。
4. 24h quote turnover 换算为 USD，按 venue 合并后应用 40% 原始限权，再归一化。
5. 没有 contributor 返回 Unknown；只有一个 contributor 返回 low，不能伪装高可信。
6. 当前价和 24h open 必须使用同一 contributor/weight；缺 open 时只让 change Unknown，不丢当前价。

这里没有“自动切换到 CoinGecko 当前价”。CoinGecko 只提供资产身份、市值、供应量、图标与稳定币参考率。

## 身份异常

同一个 symbol 可能代表不同资产，因此 `resolveDiscoveredMarkets` 只接受已审核的 provider alias。Top 200 中 symbol 唯一时可以生成 pending 建议，但不能自动批准。ambiguous、非 USD-family、非 Spot、不可交易和 Top 200 之外的市场只进入 Catalog Audit。

上游明确把 provider-managed 市场标为不可交易时，市场置 inactive；一次目录响应缺项不直接下架，避免 provider 返回不完整页面造成大面积误删。重新变为 tradable 并通过身份门后可以重新启用。

## 写入、缓存与修复

SnapshotWriter 先让 PostgreSQL 做乱序/no-op/correction 决策；只有 PG 接受后才刷新 Redis，因此缓存不能比真值更“新”。Redis 故障允许按既有接口规则回退 PG，不能反过来把 Redis 当主库。

K 线使用 `(market_id, interval, open_time)` 业务唯一键。四家 CEX 各用 `provider_kline_selection` 固定 50 个具体 USD-family market，采原生 1m；只有分钟连续完整才生成 15m/1h/1d。worker 只扫描缺口并生成确定性 repair task；crawler 按任务 provider 用 `FOR UPDATE SKIP LOCKED` 领取并回原交易所幂等回补。Doris 是历史旁路，失败不阻断当前行情或综合价。

## 设计决策、被拒绝方案与代价

- **独立 supervisor 而不是一个大循环。** 避免 Coinbase 断线拖停 OKX；代价是每家有独立状态与退避。
- **四家 WS 主链路 + REST reconcile。** 被拒绝的是把每家 50 个产品都用 5 秒 REST 轮询：请求放大会制造限流，也丢失流式 source 语义。REST 只做周期对账和断线保底；代价是每家多一个 reconcile source，但 System 能准确说明实时链路与对账链路。
- **stale 可读但不入综合价。** market 抽屉可展示旧价格和时间，综合价严格排除；代价是两处“有值/可参与”语义不同，接口必须明确。
- **不在 writer 做主观价格突变阈值。** 当前只执行确定性 3% 跨 venue 中位数门；更复杂的时间序列风控属于后续质量 worker。
- **没有快照 outbox。** PG 成功后 Redis 派生失败可通过重建恢复，但不是事务级原子提交，这是当前明确成本。

## 故障与恢复表

| 故障 | 对正式价格的影响 | 可观察状态 | 恢复 |
|---|---|---|---|
| 某 provider 429 | 仅该 provider 停止贡献 | provider failure + backoff | 下一次成功重置 |
| 任一 CEX WebSocket 断线 | REST reconcile 可维持部分已选快照；WS source 独立降级 | disconnected/failure | 自动重连，REST 不冒充 WS 已恢复 |
| Binance `O` 被误当 `o` | 正常实现中由精确字段阻断；回归测试失败会阻止交付 | 协议 fixture test | 同时声明大小写字段，绝不靠事后阈值修正 |
| USDT/USDC 率过期 | 对应 quote 市场排除 | missing quote rate | 5 分钟指标刷新 |
| 报价离群 >3% | 该 market 排除 | exclusion JSON | 下一轮重新判断 |
| 只剩一个 venue | 价格可用、confidence low | single contributor | 新 venue 恢复升级 |
| 全部 venue 失败 | asset index unavailable | API available=false | 任一合格 Spot 恢复 |
| Redis 不可用 | PG 真值保留 | cache degraded | 重建派生缓存 |
| DB 写入失败 | 不刷新 Redis | write failure | adapter 下一轮重试 |
| K 线回补不完整 | repair task 不完成 | attempts/last_error | 有界退避再领 |
| Doris 不可达 | 仅历史模块失败 | DW/Insights error | 独立重试 |

## 关键入口与顺序

1. `crawler/spot_catalog_adapters.go`：provider 目录 HTTP 边界、状态与 JSON 校验。
2. `crawler/spot_ticker_streams.go` + `crawler/spot_ticker_supervisor.go`：WebSocket/REST、5 秒合并、退避、断线隔离和规范化。
3. `marketdata/snapshot_writer.go`：PG 先提交、Redis 后派生。
4. `marketdata/composite.go`：新鲜度、汇率、中位数、venue 权重和 confidence。
5. `crawler/cex_kline_supervisor.go` + `worker/worker.go`：四家 K 线采集/汇总与只产任务的缺口扫描。

## 术语

| 术语 | 准确含义 | 大白话 | 当前项目位置 |
|---|---|---|---|
| Freshness | 数据距 `observed_at` 的年龄 | 这口报价还热不热 | 30 秒综合门 |
| Exclusion | 某 market 本轮不参与综合价的确定性原因 | 这张报价牌为何没被采用 | `asset_price_index.exclusions` |
| Degradation | 依赖减少时仍提供诚实的较弱能力 | 少一家报价就降级，不装没事 | confidence/Unknown |
| Backoff | 连续失败后逐步延长重试间隔 | 别每 5 秒撞一次限流墙 | adapter supervisor |
| Correction | 同 source time、较晚 observed time 的真实修正 | 同一张报价的新版 | SnapshotWriter |

## 证据边界

- `implemented`：四家 WS/REST 隔离、5 秒合并、HTTP 429/退避、nullable change、综合价排除、四家 K 线 selection 与数据生命周期已落地。
- `build-verified`：以本次交付的完整门禁为准；Binance `o/O` 真实协议 fixture 已加入。
- `integration-verified`：本地真实 PostgreSQL 与浏览器验证了四家快照、综合价和四家 K 线；修复后 BTC 24h open 约为现价而不是毫秒时间戳。
- `environment-pending`：外部四家 canary、断网/429 现场注入、72 小时共同运行和最终七天。
- `production-recommendation`：延迟告警阈值、provider SLA、快照 outbox 与更复杂价格突变风控。

## Owner 60 秒解释

> 四家交易所都用 WebSocket 收实时 ticker、REST 对账漏项，5 秒合并后统一走 SnapshotWriter 写 PG，Redis 只是后派生。Binance 的 `o` 开盘价和 `O` 窗口时间必须分别解析。综合价只用 30 秒内的新鲜 Spot，稳定币率过期或偏离中位数 3% 就排除；只剩一家降成 low，全部失败就是 Unknown。四家 K 线各固定自己的 market selection，缺口由 worker 开工单、crawler 回原 provider 修复；Doris 挂了不影响首页。Q-M3 另建只读 provider contract：显式 identity、source、三种时间、十进制 unit/scale、TTL 与 quality，只有 retryable/unsupported 能 fallback；它现在只有 fake provider，不写 SnapshotWriter、更不接交易账本。

## 闭卷自检

1. 目录成功为什么不能证明 ticker Healthy？
2. 为什么 stale market 可以在抽屉展示却不能参加综合价？
3. 429 为什么要退避而不是继续 5 秒固定重试？
4. 为什么 CoinGecko 当前价不能作为综合价 fallback？
5. 一次目录缺项为什么不能立刻下架市场？
6. PG 成功、Redis 失败时系统如何恢复，当前代价是什么？
7. 为什么 Binance ticker 结构体必须同时声明 `json:"o"` 和 `json:"O"`？
8. 为什么四家 WebSocket 事件要 5 秒合并，而不是逐条写 PG？
9. 为什么 Coinbase 的 K 线缺口不能拿 Binance BTC/USDT 补？
10. 为什么 BTC、某 venue 的 BTC/USDT Spot 和 BTC/USDT Perp 不能只靠 symbol 合并？
11. 为什么 auth、bad payload 或 stale 不能由下一 provider 的成功静默掩盖？
12. Q-M3 fake provider 通过为什么不等于 CoinGlass、CMC 或交易所 adapter 已经集成？
