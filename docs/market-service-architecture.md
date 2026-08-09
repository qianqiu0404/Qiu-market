# 多交易所聚合行情：资产目录、综合现货价与交易域边界

## 问题与可观察结果

旧首页把一个 venue 的市场价格当作资产价格，因此 BTC 会因 Binance Spot、Hyperliquid Perp 等市场重复出现，也无法回答“这个 BTC 价格由哪些交易所共同支持”。新读模型固定为资产粒度：

- CoinGecko 市值 Top 200 是候选池；Binance、Coinbase、Bybit、OKX、Hyperliquid、Uniswap、PancakeSwap 各自冻结一套 50 资产选择，All 展示七套选择按 canonical `asset_id` 去重后的并集并按市值排序。
- Binance、Coinbase、Bybit、OKX 提供 Spot；Hyperliquid Perp、Uniswap/PancakeSwap V2+V3 路线会扩展 All 的资产成员，但永不参与 All 综合现货价。
- 首页价格由 5 秒一轮的 `asset_price_index` 提供，Unknown 是明确状态，绝不借 CoinGecko 当前价冒充交易所综合价。
- 四家 CEX 各把当前版本化 50 资产选择映射到一个具体 USD-family Spot，采原生 1m 并确定性汇总 15m/1h/1d；综合资产 K 线和 7D 走势仍属于后续独立阶段。

## 系统边界

S78 的行情 bounded context 负责市场目录、行情快照、综合参考价、K 线和只读洞察，不拥有订单、订单簿、成交撮合、账户或账本。仓库现已新增独立 `trading` bounded context：它可以只读消费新鲜综合参考价和 K 线，但交易状态只能由 `market-services trading` 写；共享 HTTP API 只是 loopback gRPC gateway。详细实现见 [trading-system.md](trading-system.md)。

```text
行情域：asset / provider market / snapshot / composite index / kline
交易域：order / order book / match / account / ledger（当前只做虚拟现货，不做 position）
```

这就是 bounded context：不是看代码是否放在同一仓库，而是看哪组业务术语对哪份状态拥有最终解释权。

## 身份与目录模型

| 模型 | 准确含义 | 关键约束 |
|---|---|---|
| `asset` | 跨 venue 的 canonical 资产，如 BTC | 不能只按 symbol 猜身份 |
| `asset_external_mapping` | CoinGecko 等 provider 的外部 ID 到 canonical asset | `(provider, external_id)` 唯一 |
| `asset_alias` | 审核过的 provider 资产代码别名 | provider 级别；pending 不可启用 |
| `provider_market_candidate` | 交易所目录里发现的市场及解析状态 | discovery 不等于 enabled |
| `exchange_symbol.guid` | 不透明 `market_id` | 一个可报价 venue market |
| `exchange_symbol.market_code` | 可读审计编码 | 全库唯一 |
| `asset_metric_current` | CoinGecko 排名、市值、供应量、图标和 provider 时间 | 不作为综合交易所价格 |
| `asset_price_index` | 交易所综合现货价、成交额、参与者和 confidence | 5 秒重算、版本化 |
| `provider_rollout_state` | 每个 provider 独立的 shadow/canary/enabled/paused 状态，以及正交的本地预览开关 | 正式 rank/canary/soak 与本地产品开发互不污染 |
| `provider_asset_selection_state` / `provider_asset_selection` | 每家 provider 当前激活的版本化 50 资产选择及历史版本 | 短暂行情失败和市值排名抖动不能换成员 |
| `asset_venue_snapshot` | All/CEX/Perp/DEX 统一资产级当前读模型 | 保存 last attempt/success；30s Fresh、5m 内 Stale、再后 Unavailable |
| `asset_representation` | canonical asset 在指定链上的已审核合约表示 | chain + contract 唯一；上线前核验 decimals |
| `dex_pool_candidate` / `dex_route_current` | DEX 池审计与固定名义路线报价 | 不复用 CEX `exchange_symbol` |

目录启动时刷新，此后 provider 市场目录每 6 小时刷新，CoinGecko Top 200 与稳定币美元参考率每 5 分钟刷新。新市场只有同时满足“可交易 Spot、USD-family quote、base/quote 唯一映射、base 在 Top 200、无 identity 冲突”才可解析。按 symbol 唯一得到的映射只生成 pending 建议，不能自动批准。

正式切流由 `provider_rollout_state` 控制，产品成员由独立的 `provider_asset_selection` 控制：前者回答“能否正式发布”，后者回答“provider 当前稳定展示哪些资产”。本地 `make dev` 默认开启正交的 Local Preview，七源读取各自激活选择，但只累计 preview source，readiness 固定 blocked。四家 CEX 从审核过的 Top 200 现货候选按市值冻结 50；Hyperliquid 从身份确认的 Perp 冻结 50；Uniswap/PancakeSwap 从 chain+contract+pool 身份复核通过的 listed assets 冻结 50。AMM listing membership 与分级 route quote eligibility 分开，询价暂时失败不会删掉成员。以后只有 owner 显式刷新，或已选成员不再有效时才产生新版本。正式 Shadow/Canary/Enabled 和 24/48/72 小时门保持不变。

## 端到端控制流

```text
CoinGecko Top 200 + global
          |
          v
asset_metric_current / external mapping
          |
          +--> provider-local selection vN (50 each)
          |             |
          |             +--> All canonical union
          +-----------------------------+
                                        |
Binance / Coinbase / Bybit / OKX        |
          |                             |
          v                             |
provider_market_candidate --> alias audit
          | reviewed + enabled          |
          v                             |
WebSocket primary + REST reconcile       |
          | 5s coalesced latest          |
          | normalized snapshot         |
          v                             |
SnapshotWriter --> PostgreSQL ----------+
      |               |
      v               v
Redis cache       asset composite index (5s)
                       |
                       v
              asset_venue_snapshot
              (last success retained)
                       |
                       v
              v2 HTTP / gRPC read model
                       |
                       v
                 CMC-style Markets

Hyperliquid Perp --> stable selection -> perp_mark snapshot ----------+
Uniswap / Pancake --> V2/V3 pool audit -> max-two-hop route quote ----------+-> venue tabs only
provider_kline_selection --> native 1m --> deterministic 15m/1h/1d --> dw --> Doris
```

PostgreSQL 是当前状态所有者；Redis 是可重建缓存和排行；Doris 是历史分析旁路。把 Redis 放到 PostgreSQL 前面会让缓存成为不可审计主写链路，当前规模也不需要用 Kafka 换取额外运维复杂度，因此两者都被拒绝。

Provider selection 与 K 线 market selection 是两层边界：前者冻结“该 provider 页面有哪些资产”，后者冻结“每个资产从哪一个具体 USD-family Spot 取历史”。`provider_kline_selection` 保存 provider selection version、asset、market、source symbol 与 rank，reconcile 后才投影为 `exchange_symbol.kline_enabled=true`。四家各自读取原生 1m，只有分钟连续完整才确定性汇总 15m/1h/1d；不能拿另一家交易所补缺口，也不能用目录顺序静默换 market。

四家 Spot adapter 不把“我们收到响应的时间”冒充上游事件时间：

| Provider | `source_time` | `source_time_kind` |
|---|---|---|
| Binance 24h batch | `closeTime` | `ticker_window_close`；百分比直接取 `priceChangePercent` |
| Binance WebSocket ticker | `C`，缺失回退 `E` | `ticker_window_close`；结构体同时声明 `o` 开盘价和 `O` 窗口开始时间，防止大小写不敏感覆盖 |
| Coinbase ticker batch | 消息/事件 timestamp | `ticker_event` |
| Bybit Spot tickers | 响应顶层 server time | `provider_response_time` |
| OKX Spot tickers | 每条 ticker `ts` | `ticker_event` |

任何来源没有明确时间时保持 NULL；`observed_at` 始终表示本服务成功解析的时间。

## 读接口的三类价格事实

V2 dashboard 与轻量 tick 不再要求浏览器把 `price_usd`、`price_source` 和
若干时间字段自行拼成来源结论。它们返回同形的 `MarketPriceFact`：

| 字段 | 唯一语义 | 当前来源 |
|---|---|---|
| `venue_price` | 所选 CEX Spot 或 Hyperliquid mark 自己的价格；绝不拿综合价补位 | `binance` / `coinbase` / `bybit` / `okx` / `hyperliquid` |
| `dex_route_price` | 仍在 60 秒 route 窗口内的链上指示性价格 | `uniswap` / `pancakeswap` |
| `display_price` | 与 route 分开的参考栏；优先 Fresh CEX composite，DEX 无 composite 时才可显示明确标注的 CoinGecko market reference | `cex_composite` / `coingecko` |

每个事实都同时携带
`price_usd/change_24h_pct/turnover_24h_usd`、`available`、`kind`、
`source`、`source_time`、`observed_at`、`last_success_at`、
`freshness_status/freshness_age_seconds`、`quality`、
`contributor_count/contributors` 和 `version`。Unavailable 使用空价格、空
source、空 contributors 和明确的 `unavailable` 状态，不能把旧来源标签留给
另一个值。Composite 没有单一上游 source time，因此它只声明本服务的
`observed_at`；CoinGecko reference 的 provider time 与本服务观察时间分开。

被拒绝的是继续提供一个“当前最方便展示的数字”并让客户端根据 tab 猜它究竟是
venue、route 还是 reference。那样字段少，但缓存、延迟或局部故障时会静默换
口径。当前方案多传三个小对象，并暂时保留旧平铺字段作为兼容层；新代码只消费
价格事实。HTTP 与 TypeScript 类型边界已经固化，Markets 的 3 秒 CEX tick 已按
query generation 与 version/observed time 消费；DEX Price、24h 与 Quality 已
永久拆成 Route / Reference 两栏。SQL 兼容层对 DEX 的 route 也只保留 60 秒：
过期后 price/change/turnover/source/quality/observed time 一起不可见，而
`display_price` 只剩 composite 或 CoinGecko reference。这个契约不改变综合价
贡献规则，也不把 CoinGecko reference 变成 All 指数。

```text
DEX snapshot <= 60s -> row.Price -> dex_route_price -> Route lane
asset_price_index <= 30s ---------------------------> display_price
CoinGecko reference <= 15m -------------------------> display_price
                                                        -> Reference lane
```

## 综合现货价规则

每个资产每 5 秒执行一次：

1. 只保留活跃 Spot、正价格且 `observed_at` 不超过 30 秒的市场；Perp 永久排除。
2. USD 汇率为 1；USDT/USDC 必须有不超过 10 分钟的 CoinGecko 美元参考率。
3. 计算候选价格中位数，偏离超过 3% 的市场记录 exclusion 并移除。
4. 以 24h 美元成交额计算 venue 原始权重；同一 venue 即使有多个 quote，也先合并。三个及以上 venue 用 water-filling 限权，使最终归一化权重仍不超过 40%；一或两个 venue 数学上无法同时满足 40% 与总和 100%，按低/中 confidence 保留归一化原始权重。
5. 1 个 venue 返回 `low/single_venue`，2 个为 `medium`，3 个及以上为 `high`；0 个返回 Unknown。
6. 规范 ticker 同时有独立可空的 `open_24h` 与 `change_24h_pct`。Binance 保留官方百分比，Coinbase 优先保留 provider open/百分比，Bybit 用 `prevPrice24h`，OKX 用 `open24h`；缺一项可确定性推导另一项，真实 `0%` 不会被当成 Unknown。All 仍使用同一 contributor 和权重聚合 current/open 后计算，绝不平均各家百分比。

这里的“综合价”是可审计读模型，不是可执行成交价。参与市场、权重和排除原因写入 JSONB，市场抽屉按需读取；`provider_union` 响应不嵌套全部 venue 明细。

## Provider contract 与确定性路由（Q-M3）

`marketdata/providercontract` 是现有 crawler adapter 与未来信息源之间的只读契约层，不替代 `SnapshotWriter`，也不拥有数据库、Redis、订单、撮合或账本。Q-M3 的 provider-neutral contract/router 继续用 deterministic fake provider 做门禁；Q-M4A 在独立 `binancepublic` 子包增加一个默认关闭的真实 HTTP adapter。它只读 Binance 官方 market-data-only 域的 BTC/USDT Spot ticker 与 1m OHLCV，仍未注册到 crawler、公开 API/UI 或交易参考价，因此其离线证据是 `contract-verified`，opt-in smoke 成功时才是该时刻的 `external-read-verified`，不是 production rollout。

四种 capability 使用同一信封：`spot_ticker`、`ohlcv`、`derivatives`、`signals`。每条事实都必须携带 schema version、实际 provider、可审计 source ID 或 URL、canonical identity、显式单位/精度、`observed_at`、本地 `received_at`、TTL 与 quality flags；事件类另有 `event_time` 和稳定 event ID。所有价格、数量、比率、费用或置信度均为规范十进制字符串，不能穿过该边界变成 `float64`。当前 TTL 是 Go 进程内 `time.Duration`（JSON 数值单位为纳秒），不是对外 wire 格式；M4 的 HTTP adapter 必须把持续时间映射为字段名或 schema 明示单位的值。

身份不能由裸 symbol 猜测。Asset 同时包含稳定 ID 与规范 symbol；Market 同时包含稳定 market ID、venue、base/quote asset 和 `spot|perp`，canonical code 固定为 `<venue>:<BASE>/<QUOTE>:<type>`。因此 BTC、Binance BTC/USDT Spot 与同 venue BTC/USDT Perp 是三个不同层级的身份，不能静默互换。

时间与质量规则是确定性的：

| 输入 | 结果 |
|---|---|
| `observed_at` 在允许 future skew 之外 | typed future/bad-payload，fail closed |
| 当前时间超过 `observed_at + TTL` | 标记 stale；不能作为 fresh success 或缓存命中 |
| source、schema、TTL、身份或字段单位缺失/冲突 | typed bad-payload/identity/unit，fail closed |
| 同 source 的相同 event ID、K 线 open time 且内容完全相同 | 去重一次并加 duplicate quality flag |
| 相同键但内容冲突 | typed conflict，fail closed |
| 可安全排序的乱序 K 线 | 按 open time 稳定排序并加 out-of-order quality flag；不能改变事实内容 |

Provider 先通过 capability discovery 声明能力；缺失能力返回 typed `unsupported`，不能返回空数组冒充成功。Router 保留每次 attempt 的 provider、capability、cache hit、error kind 与 retry-after，并把最终实际 source 原样返回。只有 `unsupported` 或显式 retryable 的 rate-limit、timeout、network、upstream 5xx/unavailable 才能尝试下一 provider；auth、permission、配置、bad request、bad payload、stale、future、identity、单位或冲突错误立即停止。429 的 `retry_after` 只阻断该 provider 到 fake clock 指定时刻，不改变其他 provider；调用方 context 取消也不会被 fallback 掩盖。缓存有界且只保存验证通过的 fresh success，过期值不会 stale-on-error 回放。

### M4 真实 adapter 接入门

| 候选来源 | 最小字段映射 | 密钥与许可边界 | 速率预算门 |
|---|---|---|---|
| CEX Spot / OHLCV | provider instrument → 已审核 Market；成交价、bid/ask、source time；interval/open/close/OHLCV | 优先官方 API；公开展示和再分发条款必须逐 provider 审核；凭据只在服务端 secret store | 按 capability 分开预算；stream、REST reconcile 与 repair 不共享隐式全局桶 |
| CoinGlass 类 derivatives | Perp Market、mark/index、funding interval/rate、OI，以及带明确统计窗口的可选 liquidation | 先确认套餐、展示/再分发授权；key 不进入浏览器、日志、fixture 或仓库 | 以已购买套餐为硬上限；429 尊重 Retry-After；未确认额度前 adapter 保持 disabled |
| CoinMarketCap 类资产/市场信息 | external asset ID → canonical Asset；provider timestamp、价格/市值或获许可的 signal | 先确认具体 endpoint、缓存与再分发许可；key 仅服务端注入 | 每 endpoint 建独立 token bucket 与成本记录；不能假设免费额度或抓网页 |
| xiuqiu-site 内容/消息 | stream、稳定 event ID、event/publish time、asset refs、source URLs、review/license 状态 | server-to-server；保留 attribution 与内容许可，reviewed snapshot 和 shadow event 不静默合并 | 条件请求、last-good 与明确 stale；degraded+empty 不能解释为“没有事件” |

M4 adapter 必须提交官方契约 fixture、许可/费率记录、字段映射表、超时/429/坏 payload 测试和明确成本预算后才可注册；真实密钥、收费订阅或私有接口缺失时只完成 adapter/config seam，不能用 fake provider 冒充外部集成。Q-M4A 的 Binance 选择、精确字段、HTTP/SSRF 边界、429 预算和许可门记录在 [`docs/binance-public-provider.md`](binance-public-provider.md)；即使 online smoke 通过，未完成 owner 法务确认也不能自动启用或向用户展示。

## 设计决策、代价和边界

1. **CoinGecko 只管资产主数据。** 被拒绝的是用 CoinGecko 当前价填补交易所失败；那会把来源不同的价格伪装成一个口径。代价是全部 venue 失败时首页必须显示 Unknown。
2. **provider alias 必须审核。** 被拒绝的是全局按 `BTC` 字符串自动合并；同 symbol 不同资产会造成静默串价。代价是接入初期会有 pending/ambiguous 审计工作。
3. **独立 adapter supervisor，WebSocket 优先、REST 对账。** Binance/Bybit/OKX 订阅公开 ticker stream，Coinbase 订阅 `ticker_batch`；内存只保留每个 source symbol 最新事件，约 5 秒合并写一次。REST 每 30 秒对账安静/漏消息资产并在 stream 断线时保底。单 provider 的 panic、429 或断线只重启/退避该 adapter。代价是状态必须区分 stream primary 与 reconcile，测试也要覆盖乱序和断线。
4. **先综合价，后综合 K 线。** 被拒绝的是用某一家 K 线冒充资产指数历史。代价是 168 根闭合综合 1h 蜡烛形成前，7D Chart 诚实显示 `—`。
5. **AMM 使用独立身份模型。** Uniswap/Pancake 的 chain、token contract、V2/V3 pool 和 route 不塞进 CEX `exchange_symbol`。正式环境使用私有 Subgraph/RPC；本地可用 DEX Screener 发现候选和公共只读 RPC，但仍必须链上复核对应 Factory。路线允许直连或最多两跳混合协议，每一跳分别通过 V2 Router 或 V3 QuoterV2 询价。代价是覆盖更少、请求更慢和更严格的故障边界。
6. **DEX 进入 All 的资产成员并集，但不进入 All 综合价。** `$10K → $1K → $100` 双向 QuoterV2 只是带明确名义金额的指示性路线价格，不是订单或可执行套利价。All 可以因此出现 DEX 独有资产，但其 Price 仍是 Unknown，除非有新鲜 CEX Spot contributor；至少共同稳定 72 小时并另行批准后，才讨论是否改变价格贡献规则。
7. **选币与行情状态分离。** 被拒绝的是“当前有价格才出现在列表”：它会让 API 抖动改变产品成员。代价是必须持久化 selection version，但页面、审计和恢复都稳定。
8. **价格、来源、时间和质量作为一个事实传输。** 被拒绝的是客户端把多个平铺字段重新拼装；代价是响应稍大，但 route/reference、last-good 和缓存乱序都能按同一边界校验。
9. **DEX route 与 reference 永久分栏。** 被拒绝的是 route 优先的单一 display 值和 route 过期后的 reference 改名；代价是读模型保留两套小对象和更高的表格行，但故障时不会继承错误来源或涨跌语义。

## 关键代码入口与顺序

1. `catalog/provider-asset-mappings.yaml` + `catalog/manifest.go`：代码评审过的 Top 200 候选 alias 和链上表示。
2. `database/venue_aggregation.go`：确保七源独立 50 资产 selection、原子版本切换并保留最后成功快照；All 合并七家 canonical identity。
3. `crawler/catalog_supervisor.go` + `crawler/spot_ticker_supervisor.go` + `crawler/spot_ticker_streams.go`：刷新目录、确保选择，并隔离四家 WS primary / REST reconcile。
4. `marketdata/snapshot_writer.go` + `marketdata/composite.go`：PG-first 快照、30 秒 Fresh 参与者、异常值和 water-filling 限权。
5. `database/market_aggregation.go` + `services/http/service/market_index.go`：查询三类来源列并组装 `MarketPriceFact`；HTTP dashboard/tick 共享同一事实契约。
6. `marketdata/providercontract/`：Q-M3 的 provider-neutral 类型、canonical identity、规范化、typed error、确定性 router/cache/fake provider 和纯读 consumer seam；`binancepublic/` 是 Q-M4A 默认关闭的 BTC/USDT Spot ticker/OHLCV read adapter，不接写链或交易链。

K 线与 DEX 的后续入口是：

- `crawler/cex_kline_supervisor.go`：四家 1m 采集、确定性汇总和按 provider repair；
- `crawler/amm_dex.go`：V2/V3 pool identity、mixed route、分级询价和当前 cycle 权威 snapshot。

## 术语

| 术语 | 准确含义 | 大白话 | 当前项目位置 |
|---|---|---|---|
| Canonical asset | 系统认可的跨 provider 资产身份 | BTC 的身份证 | `asset.guid` |
| Candidate catalog | 已发现但还未必可启用的市场清单 | 新商家先进入待审区 | `provider_market_candidate` |
| Alias review | provider 代码到资产身份的人工确认 | 确认这个昵称真指向谁 | `asset_alias` |
| Snapshot writer | 所有 adapter 共用的当前行情提交入口 | 所有送货车走同一个收货台 | `marketdata.SnapshotWriter` |
| Composite index | 多个合格 Spot 报价形成的资产读模型 | 多家报价做一张可追溯的参考牌 | `asset_price_index` |
| Confidence | 按合格 venue 数表达的覆盖等级 | 有几家店一起支撑这个价 | low / medium / high |
| Provider rollout | provider 独立的 shadow/canary/enabled/paused 正式状态机 | 每家店有自己的正式灰度闸门 | `provider_rollout_state.mode` |
| Local Preview | 只允许本机使用真实行情做产品预览，不能晋级 | 商场内部试营业，不算正式开业记录 | `provider_rollout_state.local_preview_enabled` + `spot-tickers-preview` |
| Provider selection | 某 provider 当前稳定展示的版本化 50 资产集合 | 每家店自己的带版本菜单 | `provider_asset_selection*` |
| Asset representation | 资产在某条链上的审核合约身份 | 同一身份证在不同链上的合法分身 | `asset_representation` |
| Indicative route quote | 按 $10K/$1K/$100 分级双向询价形成的只读参考 | 先问大额，不行再问小额，不真正下单 | `dex_route_current` |
| Price fact | 数值、来源、时间、新鲜度、质量和贡献者不可拆分的 API 对象 | 每张价签连同店名、打印时间和质检章一起交付 | `model.MarketPriceFact` |
| Coalesced writer | 高频流只保留每个 symbol 最新事件，再按固定节奏提交 | 快递不断到，收货台每 5 秒合并签一次 | `spot_ticker_streams.go` |
| Provider K-line selection | 一个 provider selection version 对应的具体历史 market | 每道菜固定去哪个柜台查旧账 | `provider_kline_selection` |

## 故障、退化与恢复

| 场景 | 行为 | 恢复 |
|---|---|---|
| 单 provider 超时/429 | 该 adapter 退避，其他 adapter 继续；记录 provider failure | 下次成功重置到 5 秒 |
| 任一 CEX WebSocket 断线或安静产品无事件 | WS 独立指数退避；REST reconcile 补安静/漏消息资产，不影响其他 adapter | 重连后仍以 WS 为主，内存 latest map 继续 5 秒合并 |
| quote 汇率超过 10 分钟 | 该市场排除，不能按 1 假定 | CoinGecko 指标刷新后自动恢复 |
| 报价偏离中位数 >3% | exclusion 记录原因，不参与价格/open | 报价回到范围后下一轮恢复 |
| 只剩一个 venue | 仍返回价格，confidence 降为 low | 新鲜 contributor 恢复后自动升级 |
| 全部 Spot 失效 | `available=false`，API/页面显示 `—` | 下一轮有合格报价后恢复 |
| Redis 故障 | PG 仍是状态真值；既有读路径按接口规则回退 | Redis 重建缓存 |
| Doris 故障 | 首页、快照和综合价不受影响 | 历史模块独立重试 |
| identity ambiguous | 不创建正式市场，出现在 Catalog Audit | 审核 alias 后下一次目录刷新解析 |
| 上游明确下架 | provider-managed market 置为 inactive；单次目录缺项不会直接下架 | 上游恢复 tradable 后重新启用 |
| DEX 私有端点缺失或公共来源 429 | 仅对应 DEX 显示 Unavailable，CEX/Hyperliquid 继续；稳定选择不换成员 | 独立 30 秒到 10 分钟退避，或切换已审核私有端点 |
| provider source cadence 不同 | System 分别按 catalog/ticker/route 的真实周期判断 | 成功后清空 next retry；不拿目录成功冒充行情成功 |
| 旧 K 线或目录 source 失败 | 仍在 source matrix 显示，但不覆盖主 ticker 的 operational status | 单独修复对应能力，不误停现货报价 |
| DW 对账中断 | `dw_acceptance_state` 重置连续成功起点 | 恢复后重新累计 72 小时，绝不沿用失败前时长 |
| 合约 decimals / pool factory 不符 | 停止该 DEX 的路线发布，不用错误精度计算 | 修正审核清单后重新发现 |
| 当前 DEX cycle 的旧路线消失或失败 | 技术审计行保留，但公开 venue snapshot 权威置为 unavailable | 下一轮同一 selection 出现合格路线后恢复 |
| route 事实不可用但 reference 仍可用 | `dex_route_price` 为空；兼容 route price/change/turnover/source/quality 也清空；`display_price` 保持 `cex_composite` 或 `coingecko` 来源 | 新 route 到达后只恢复 route 分栏 |
| DEX route 30–60 秒 | Route 栏明确 Stale，Reference 栏不变；route 不参与 reference breadth | 60 秒内新询价恢复 Fresh，否则转 Unavailable |
| 单家 CEX K 线 API 失败 | 只降级该 provider 的 `klines` capability；ticker/其他 provider 继续 | 独立退避、下一轮重叠续传和 repair |
| 快照超过 30 秒但不超过 5 分钟 | 保留最后成功价格并标 Stale，不进综合价、涨跌榜或按价格排序 | 新快照写入后自动 Fresh |
| 快照超过 5 分钟 | 行仍属于 selection，但价格为 Unavailable；保留 last attempt/success 审计 | 新成功值原地恢复 |

## 验证与证据边界

- `implemented`：七源独立版本化 50 资产 selection、All 七源 canonical 并集、四家 CEX WS-first/REST-reconcile/5 秒合并写入、四家版本化 50 market K 线选择、原生 1m 与确定性大周期、V2+V3 最多两跳 AMM、权威 DEX snapshot、DEX Route/Reference 双栏、Fresh/Stale/Unavailable、手动 rollout 门和向后兼容 HTTP/gRPC 字段已存在。
- `build-verified`：以本次交付末尾记录的完整 Go、Vitest、Vue、Playwright 和 `git diff --check` 为准。
- `integration-verified`：2026-07-25 本地七角色与真实 PostgreSQL/Redis/HTTP/gRPC/四家 CEX/Hyperliquid/公共只读 EVM RPC 已交换数据。All 页面现场显示 109 个 canonical 并集成员、四家 CEX contributor，四家 K 线各 reconcile 50 个 market；浏览器与数据库确认 Binance `o` 开盘价未再被 `O` 时间戳覆盖。AMM 现场出现 V2/V3 直连和 mixed route。动态覆盖不是永久承诺。
- `environment-pending`：AMM 同 route 24h 观察、私有 DEX endpoints 正式路径、各 24h/48h/72h rollout 与最终七天。
- `production-recommendation`：公开行情 API 鉴权、配额、SLA 与综合资产 K 线仍是后续切片；虚拟交易已有独立单用户鉴权，但不等于生产或真实资金能力。

## Owner 60 秒解释

> CoinGecko Top 200 是候选池。七个 provider 各自冻结 50 个身份确认的资产，All 按 canonical asset 合并，所以 BTC 只出现一次。四家 CEX 用 WebSocket 收实时 ticker，REST 对账漏项，5 秒合并写 PG；只有 Fresh Spot 进综合价。API 把 venue、DEX route、composite/reference 分成三个 price fact，每个事实自带来源、时间、新鲜度、质量和贡献者。DEX route 只读 60 秒并永久和 reference 分栏，过期不会把 reference 改名。K 线另有版本化 market selection，只采原生 1m 再确定性汇总。Perp/AMM 只扩展成员；一次故障只降级所属 capability。

## 闭卷自检

1. 为什么 CoinGecko 当前价不能在交易所失败时补首页价格？
2. 为什么同 symbol 唯一只能生成 pending alias，不能自动启用？
3. 两个 Binance quote 为什么必须先合并成一个 venue 权重？
4. 一个 contributor 缺 24h open 时，价格和涨跌幅分别怎么降级？
5. 为什么正式切流必须看 `provider_rollout_state`，不能再只看全局环境变量？
6. Hyperliquid Perp 为什么能出现在抽屉却不能进入综合现货价？
7. 四家 adapter 代码通过测试后，为什么仍不能声称多交易所已 integration-verified？
8. DEX route 为什么不能复用 `exchange_symbol`，也不能直接进入 All 综合价格？
9. Shadow feed 写了什么，又明确不写什么？
10. 为什么 provider selection 不能等同于当前新鲜报价集合？
11. 为什么 Stale 值可以展示，却不能进入综合价、涨跌榜或价格排序？
12. All 的动态行数为什么可能大于 50，但仍没有重复资产？
13. 为什么发布就绪必须由 CLI 人工确认而不是自动切流？
14. 为什么 DEX selection 固定 50，但其中只有一部分行能显示分级指示性询价？
15. 为什么缺少 CEX 参考时可以显示 On-chain only，但不能标 High？
16. Binance WebSocket 为什么必须同时声明小写 `o` 和大写 `O`？
17. 为什么 WebSocket 高频事件不逐条写 PostgreSQL，而是 5 秒合并？
18. 为什么四家 K 线只共享汇总代码，不能互相补市场缺口？
19. 为什么 `display_price` 可继续显示 reference，却不能因此把 `dex_route_price` 标成可用？
20. 为什么 DEX route 过期时，兼容平铺字段也必须清空 source、quality 和 change？
