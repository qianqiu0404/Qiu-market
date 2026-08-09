# 行情数据质量：多 provider 隔离、综合价降级与修复

## 核心原则

行情数据就是产品。S78 不用 mock、CoinGecko 当前价或假 `0%` 掩盖数据源异常；它保存来源时间、排除原因、confidence 和恢复路径，让调用方知道“有没有值、值来自哪里、还新不新”。

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
