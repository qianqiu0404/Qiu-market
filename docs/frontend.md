# Qiu Market 前端：多源资产首页与蓝白视觉系统

## 问题与可观察结果

旧 Markets 会在“全部”里混合资产粒度和市场粒度，BTC 可重复出现，venue 筛选还会改变一行代表什么。新首页固定为资产粒度：

- `/markets?venue=binance|coinbase|bybit|okx` 分别展示该交易所自己的版本化 50 资产选择；一项 canonical asset 一行。
- 顶部是 Global Market Cap、Covered Spot Volume、BTC Dominance、Market Breadth 四项轻量概览。
- 顶部有 All 与 Binance/Coinbase/Bybit/OKX/Hyperliquid/Uniswap/PancakeSwap；venue 切换只换报价语义，不改变资产行粒度。七家各固定展示 50 个身份确认的资产。
- All 的二级筛选是 Assets、Gainers、Losers，搜索、排序和分页在七家选择的去重并集上执行；未覆盖项保留并显示明确原因。
- “N Markets/Routes” 打开 `?asset=<asset_id>` 抽屉，分为 CEX Markets、Perpetual Markets、DEX Routes。
- DEX 明确显示链、V2/V3 protocol path、实际 `$10K/$1K/$100` 指示性报价金额、On-chain only/CEX corroborated、同区块 quote-side impact、spread/TVL；不得称为套利信号。
- 综合资产 K 线未完成前，7D Chart 显示 `—`；四家 CEX 当前版本化 selection 中已有真实 K 线的具体 venue 可从抽屉进入。

主导航为 Markets、Trade、Insights、System。`/trade/BTC-USDT` 进入虚拟现货终端；`/dashboard` 回到 `/markets`，`/analytics` 回到 `/insights`，Assets/Exchanges/Symbols/Catalog Audit 位于 System。

## 页面数据流与契约

| 路由 | 页面 | 主要数据 | 轮询 |
|---|---|---|---|
| `/` `/dashboard` | 重定向 `/markets` | — | — |
| `/markets` | 七家 selection 并集首页与七源切换 | venue-aware v2 overview + asset dashboard + lightweight price ticks | 30s / 15s / 3s |
| `/markets?venue=...&asset=...` | 同页 venue 抽屉 | v2 asset venues，按需 | 打开时 |
| `/markets/:marketId` | 现有 venue K 线详情 | v1 market + klines | 随周期 |
| `/trade/BTC-USDT` | 虚拟现货交易终端 | 可信 BTC 参考 + venue K 线 + trading REST/WebSocket | 5s / WS |
| `/insights` | 宽度、跨 venue、历史动量 | v1 insights + Doris momentum | 模块独立 |
| `/system?tab=audit` | provider 目录审计 | v2 catalog audit | 手动/分页 |
| `/system` | 进程、依赖、provider 状态 | v1 system overview | 15s |

Insights 的 Provider Selection Coverage 必须分别请求 All `provider_union` 与七家 provider 的 `provider_top50` 稳定选择。七家 `provider_top50` 均要求正好 50 个 canonical asset；AMM 的 50 指 listed assets，不能理解为 50 个实时可询价路线。页面中的 `24h Market Breadth · Full Catalog` 是更宽的活跃市场目录，明确独立于首页 selection 并集，避免把动态 All 行数和完整目录资产数误认为同一指标。

v2 接口：

| 接口 | 语义 |
|---|---|
| `get_market_overview` | venue 覆盖数、覆盖率、宽度及 CoinGecko global |
| `get_asset_dashboard` | `provider_top50` 或 `provider_union` 资产级分页；不嵌套 markets |
| `get_asset_markets` | 单资产的 Spot/Perp 具体市场 |
| `get_asset_venues` | 单资产的 CEX/Perp/DEX 统一抽屉契约 |
| `get_provider_catalog_audit` | discovered/resolved/enabled/ambiguous/rejected |

请求显式携带 `universe=provider_top50|provider_union`；All 接受七源 selection union，单 provider 读取自己的稳定 selection，不能静默回退。响应携带 `selection_version/selection_rank/freshness_status/freshness_age_seconds/last_attempt_at/last_success_at/last_error_class`。数值仍以十进制字符串跨网络，Unknown 使用 `{value:null, available:false}`，不得把缺值变成 `0`。

价格不再靠这些平铺字段临时拼装。Dashboard 与 3 秒 tick 都提供三个同形事实：

- `venue_price`：所选 Binance/Coinbase/Bybit/OKX Spot 或 Hyperliquid mark；
- `dex_route_price`：60 秒内仍成立的 Uniswap/PancakeSwap route；
- `display_price`：独立的 CEX composite / CoinGecko reference 栏。

三者均携带 `source`、source/observed/last-success time、freshness、quality、
contributor count/list 和 version。DEX 页面可以同时展示 route 与 reference，但
不能把 reference 写回 route，也不能让过期 route 的 change/source 标签附着到
reference。旧 `price_usd/display_price_usd` 暂时只为旧调用方保留；新前端状态以
price fact 为准。API 类型化与解析、Markets tick 代次/乱序保护已经完成。DEX
表格的 Price、24h 与 Quality 永久拆成 Route / Reference 两行；Venue Volume
在 DEX tab 明确改为 Route Volume，不拿 composite turnover 冒充链上成交额。

DEX 读取链路是：

```text
asset_venue_snapshot (route, <=60s)
  -> dex_route_price -> identity + wall-age validation -> Route lane

asset_price_index / asset_metric_current
  -> display_price -> reference-kind + wall-age validation -> Reference lane
```

服务端兼容字段也执行同一边界：route 超过 60 秒后，旧 `price_usd`、
`change_24h_pct`、turnover、source、quality 与 observed time 均不可见；旧
`display_price_usd` 对 DEX 只表达 composite/market reference。前端兼容解析器
只接受显式 `dex_route_available + dex_route + 同 venue + <=60s` 的旧 route，
也只接受带独立 observed time 的 composite/market reference。

### 3 秒 tick、请求代次与 last-good

`Markets.vue` 的快照查询仍负责成员、分页和慢变指标；轻量 tick 只请求当前页
`asset_id`。一次 tick 的完整身份是 `venue + ordered asset_ids + generation`：

```text
venue / page / search changes
  -> query key changes
  -> generation increments synchronously
  -> old in-flight response cannot match the new generation
  -> current response checks provider + fact.source + kind
  -> version and observed_at must not move backwards
  -> accepted fact becomes venue+asset last-good
```

只比较 query key 不够：A→B→A 会让 key 回到 A，较早的 A 响应可能刚好最后返回。
Generation 是每次查询身份变化都递增的“批次号”，像给每次点单盖新日期章；旧
批次即使菜名相同也不能上桌。Version 防缓存回放，`observed_at` 防同 version
时间倒退；同价、同 version 但观察时间更新是合法的 heartbeat，不会被当成异常。

请求整体失败、单资产缺项、source mismatch、stale 或旧缓存响应都不会借
composite 填 CEX。页面在 `venue+asset` 边界保留最后接受的事实，并与慢速
dashboard 的同 venue fact 选较新者；仅五分钟内可读，且 caption 与 Quality
明确显示 `last-good` 及 `tick request failed / asset missing / wrong source
rejected / older tick rejected`。超过五分钟就 Unavailable。

### 虚拟交易页契约

`Trade.vue` 同时读取两个边界：

- 现有 market-data API 提供 S78 BTC 综合参考和当前可用 spot venue 的真实 K 线；
- `/api/v1/trading/**` 提供虚拟订单簿、公共成交、状态、登录、余额、订单、个人成交、下单、撤单和管理虚拟入金。

价格、数量、余额、费用和 sequence 在 TypeScript 中都保持十进制字符串，不先转成 JavaScript `number`。写请求由 `trading.ts` 自动读取 CSRF cookie 并发送 `X-CSRF-Token`，账户身份只来自 HttpOnly session。页面启动先读取 `/auth/capabilities`：只有后端显式开启本地模式才展示 Local login，只有 OAuth 配置完整才展示 GitHub login。

WebSocket 连接前先领取一次性 ticket，并携带最后 cursor 重连。连接状态、撮合恢复状态和重试原因必须可见。可信参考或 K 线缺失时显示 unavailable；不得用静态 BTC、随机蜡烛或过期响应填图。

## 交互与信息层级

资产行固定为 Rank、Asset、Price、24h、Market Cap、Venue Volume、Markets/Routes、Quality。All 的 Price 是 CEX 综合 Spot；CEX tab 是 venue Spot。All 标题展示 `N/并集数 fresh`；CEX 标题展示 `N/50 fresh · selection vX`。24h 缺失不再只有无解释横杠，而是显示 `24h reference missing`、Stale 或 Source unavailable。抽屉请求携带当前 venue：All 展示所有来源，CEX tab 只展示该交易所。1180/1280/1440 不改变核心字段。

资产行与 market identity 不互换：

- `asset_id` 驱动资产抽屉；
- `market_id` 只驱动已有真实 K 线的具体 venue；
- `venue` 驱动资产级报价来源，不把 Perp/DEX 混入 All；
- 内部 `a1/a3` 之类 ID 不显示为资产名称；
- 抽屉价格显示原始 quote 单位，例如 `65,700 USDT`，只有相对综合价偏差会使用新鲜美元参考率换算。

页面离开/刷新时，venue、filter、page、page size、search、sort 和 asset 都留在 query；页面隐藏时 `usePolling` 暂停，恢复可见后立即刷新。

## 视觉语言

`src/style.css` 使用 Qiu 蓝白金融产品令牌：

| 令牌 | 用途 |
|---|---|
| `--bg-base / --bg-panel / --bg-panel-2` | 页面、卡片、输入层级 |
| `--text-1 / --text-2 / --text-3` | 主、次、弱文本 |
| `--accent` | 品牌与交互 |
| `--up / --down / --warn` | 上涨、下跌、警告 |
| `--border` | 发丝分隔 |

固定主色为 `--bg-base:#f5f5f7`、`--bg-panel:#ffffff`、
`--text-1:#1d1d1f`、`--accent:#0071e3`。侧栏和右侧抽屉可以使用克制的
半透明 blur，数据表和图表内容层必须保持不透明白底，避免可读性下降。字体使用
Apple system stack，Regular/Medium 为主，不用极细字重；颜色不是状态的唯一载体，
必须同时显示文字。1440/1280/1180 三种宽度均禁止页面级横向溢出。

数字使用 tabular-nums，图标统一用 `AppIcon`，不使用 emoji。confidence 用低饱和 badge，不和涨跌色竞争。空状态、局部接口错误、页面级错误分别使用已有 EmptyState/ErrorState，不生成 mock 行情。

侧栏页脚只汇总 API/数据库/Redis/crawler/worker 等 core process，因此文案是 `Core processes running`；provider 的 Healthy/Stale/Unavailable 只能在 System 显示，不能再写成模糊的 `All systems normal`。

## 设计决策、替代方案与代价

1. **所有 tab 固定资产粒度。** venue 只切换 `price_kind/price_source`，BTC 始终只有一行。
2. **抽屉按需加载。** 被拒绝的是并集每行嵌套所有 markets/routes；代价是第一次打开抽屉需要一次请求。
3. **7D 不造假。** 被拒绝的是使用某一家 venue K 线冒充综合资产历史。代价是综合 1h 闭合蜡烛达到 168 根前显示 `—`。
4. **Top KPI 只放四项。** Insights 仍负责更深的市场宽度、跨 venue 和动量；首页不复制分析页面。
5. **页面成员来自 selection，不来自当前报价。** 被拒绝的是从 `available=true` 起查，那会让临时断线造成资产突然消失。Fresh/Stale/Unavailable 只改变值和参与资格，不改变成员。
6. **DEX selection 固定 50，但不伪造 50 条报价。** listed membership 来自 reviewed chain contract 和链上 V2/V3 pool identity；询价按 `$10K → $1K → $100` 逐级尝试，路线最多两跳且可混合协议，仍使用 TVL、成交量、新鲜度、冲击和 spread 门槛。小金额成功必须显示实际金额并降为 Low。代价是 DEX 页仍会同时出现可询价行和 `Not covered` 行，但产品覆盖与价格质量不再互相污染。
7. **价格事实不在组件内重新拼装。** 被拒绝的是让 `Markets.vue` 从 price/source/time 多个字段猜当前语义；响应稍大，但旧缓存、乱序响应和 route/reference 切换都有同一校验单位。
8. **generation + 单调事实门，而不是只看 query key。** 被拒绝的是 A→B→A 时复用同 key，也拒绝“最后返回者获胜”；代价是保存一个小型 venue+asset last-good map，但请求竞态不会改写来源。
9. **DEX 永久双栏，而不是选一个“最好看的价格”。** 被拒绝的是 route 新鲜时覆盖 reference、route 过期时再把 reference 改名为 route。代价是 DEX 行更高、字段更多，但链上指示价与市场参考永远不会静默换口径。

## 关键代码入口与顺序

1. `frontend/src/router.ts`：资产首页、market 详情、旧地址兼容和 System Catalog。
2. `frontend/src/api/market.ts`：v1/v2 信封、nullable decimal、三类 `MarketPriceFact`、DEX identity/时间窗校验与类型归一。
3. `frontend/src/views/Markets.vue`：概览条、Route/Reference 双栏、tick generation、last-good 降级、URL 抽屉和 venue K 线入口。
4. `frontend/src/api/trading.ts`：十进制字符串 REST、CSRF 和 WebSocket cursor。
5. `frontend/src/views/Trade.vue`：参考/K 线、订单簿、下单、余额、订单与成交。
6. `frontend/src/views/CatalogAudit.vue`：provider/status 审计筛选与分页。
7. `frontend/src/composables/usePolling.ts`：可见性暂停、恢复刷新和卸载清理。

## 术语

| 术语 | 准确含义 | 大白话 | 当前项目位置 |
|---|---|---|---|
| Asset row | 一个 canonical 资产的综合读模型 | 首页的一枚币 | v2 asset dashboard |
| Venue market | 某 CEX 的一条可报价交易对 | 某家店的具体报价牌 | CEX drawer |
| DEX route | 某链上池组合、带实际名义金额的指示性询价 | 先问 $10K，不行再问 $1K/$100 | DEX drawer |
| Reference lane | 不改变 route 状态的 CEX composite 或 CoinGecko 市场参考 | 旁边单独挂一张参考价牌，不能改写链上价牌 | `display_price` |
| Price fact | 把价格、来源、时间、新鲜度和质量绑在一起的状态对象 | 一张不能撕掉店名和打印时间的价签 | dashboard/tick |
| Request generation | 每次查询身份变化都递增的本地批次号 | 同一道菜重新点单，也要看新单号 | `Markets.vue` tick snapshot |
| Last-good | 最近一次通过身份和单调性检查、仍在可读窗口内的事实 | 明说是上一张有效价签，不假装刚打印 | venue+asset tick cache |
| Protocol path | route 每一跳使用的 AMM 版本 | 这条路线先走 V2 还是 V3 | 抽屉 `V2 → V3` |
| Composite price | 合格 Spot venue 的加权美元参考价 | 多家现货一起支撑的参考价 | main table |
| Confidence | 当前贡献 venue 数形成的覆盖等级 | 这个价有几家店背书 | low/medium/high |
| Market breadth | 指定 universe 中涨/跌/平/未知的横截面；Insights 完整目录与首页 selection 并集会明确分开 | 今天这张名单里大多数币在涨还是跌 | overview/Insights |
| Catalog Audit | 发现市场的身份解析与启用状态 | 新市场待审清单 | System tab |
| Provider union | 七家当前 selection 按 canonical identity 去重后的集合 | 合并七张菜单，同一道菜只留一行 | All |

## 失败、降级与恢复

| 故障 | 页面行为 | 恢复 |
|---|---|---|
| 所选 venue 暂时不可用 | selection 行仍存在；30s–5m 显示最后值 + Stale，之后 Unavailable | 新鲜快照到达 |
| All 中没有 Fresh Spot | provider union 行保留，明确显示 Not covered/Unavailable | 任一家 CEX 恢复 Fresh |
| 资产未上市/身份待审/rollout pending | 不占 provider 主表；Catalog Audit 显示原因 | 身份审核与串行 rollout |
| 24h open 和 provider 百分比都缺失 | 价格可显示，24h 为 `—`，reason=`missing_24h_reference` | adapter 提供任一可信参考后恢复 |
| 单 market 无稳定币率 | 仍显示原始 quote 价，relative deviation 为 `—` | 参考率刷新 |
| market 过期 | freshness 显示 Stale/Unavailable，不作为 contributor | 新快照到达 |
| DEX route 过期、reference 仍新鲜 | route 分栏显示 Unavailable；reference 分栏保持自身来源，不显示链上 change/quality | 新 route 到达后独立恢复 |
| tick 请求超时或局部缺项 | 同 venue 五分钟内 last-good 明确降级；无同 venue 事实则 Unavailable | 当前 generation 成功后恢复 Live |
| 缓存返回较低 version / 较早 observed_at | 响应被拒绝，页面标 `older tick rejected` 并保留 last-good | 新单调事实到达 |
| A→B→A 旧 A 响应最后返回 | generation 不匹配，旧 A 不进入当前状态 | 当前 A generation 完成 |
| Catalog ambiguous | 只在 Audit 展示原因，不进入首页 | 审核 alias 后刷新 |
| Doris 不可达 | Insights 历史模块报错；Markets 不受影响 | Doris 模块独立重试 |
| API 整体失败 | 对应模块 ErrorState + retry，不显示 mock | 服务恢复后轮询/手动重试 |
| Trading gRPC 不可用 | Trade 显示服务 unavailable；Markets/Insights 正常 | trading 恢复后刷新/WS 重连 |
| 可信 BTC 参考或 K 线缺失 | 对应卡片显示 unavailable，不生成假图 | 行情源产生新鲜可信数据 |
| WebSocket 断开 | 显示重连状态并保留 cursor | 新 ticket 建连后补发 |
| session/CSRF 失效 | 清除私有视图并要求重新登录 | 重新建立合法会话 |
| provider 为 shadow/paused | 目录可见，正式 venue snapshot 为 unavailable | CLI 串行切入 canary/enabled |
| venue 参数未知 | HTTP 400 / gRPC InvalidArgument，不静默回退 All | 调用方改用八个受支持值之一 |

System 的 provider 行显示 `Observing / Healthy / Stale / Unavailable / Unconfigured / Paused`、primary source、`WebSocket primary + REST reconcile` feed mode、shadow/正式 feed 的 received/matched/priced/24h 计数、四家各自 K 线 selection/匹配数、最早可晋级时间和 blockers。逐 source capability matrix 默认折叠，避免 K 线 source 淹没主要状态；某家 `klines` 失败不会把正常 `spot-tickers` 误报为不可用。System 只读，不提供 rollout 按钮。

## 开发与验证

```bash
cd frontend
npm install
npm run dev       # 127.0.0.1:5174；5173 保留给 xiuqiu-site
npm run test
npm run build
npm run test:e2e
```

Playwright 覆盖七家各 50 资产 selection、All 去重并集、DEX `Not covered`、
Route/Reference 双栏与 route 过期语义清除、资产抽屉、Unknown 不变 0、显式
24h 原因、旧路由重定向和页面级无横向溢出。2026-07-30 Markets 专项为
16/16；交易页的登录、虚拟入金、挂单、撤单、市价成交与费用证据仍由其独立
spec 验收。

## Owner 60 秒解释

> 首页一行永远代表 canonical asset，七家各有稳定的 50 资产 selection，All 展示去重并集。Markets 把 venue、DEX route 和 composite/reference 作为三个 price fact。DEX 的 Route 和 Reference 永远分栏，route 最多读 60 秒，过期会同时失去链上价格、涨跌、成交额、来源和质量；reference 只保留自己的标签。3 秒 CEX tick 绑定 query generation，再检查 venue identity、version 和 observed time；失败只保留五分钟内、明确标为 last-good 的同 venue 事实，绝不拿综合价补 CEX。

## 闭卷自检

1. 为什么首页不能继续用 venue tab 改变行粒度？
2. `asset_id` 和 `market_id` 分别驱动什么交互？
3. 为什么 market/route 抽屉不嵌进 provider union 首页响应？
4. 稳定币率缺失时，原始 venue 价格和 relative deviation 如何表现？
5. 7D Chart 为什么宁愿显示 `—` 也不用某一家 venue K 线冒充综合资产历史？
6. Catalog Audit 为什么属于 System 而不是 Markets？
7. 为什么单 provider 页不能用 `available=true` 作为行过滤条件？
8. `?venue=uniswap` 为什么仍是一行一个资产，而不是一行一个 pool？
9. 为什么 System 的 `rollout_ready=true` 不能直接触发页面切流？
10. 为什么 Local Preview 必须显示在页面上，而不能伪装成 Enabled？
11. 为什么 All 行数可能大于 50，仍然是一项资产一行？
12. 为什么 Stale 价格能展示但不能参与 Gainers/Losers？
13. 为什么 DEX 的 `selected_count` 固定为 50，而 `priced_asset_count` 可以明显更少？
14. 蓝白界面为什么只允许导航层使用 blur，而表格和图表保持白色实底？
15. 为什么 Trade 的金额和 sequence 不能先转换成 JavaScript `number`？
16. 为什么本地登录按钮必须由 `/auth/capabilities` 决定是否显示？
17. 为什么 trading WebSocket 重连必须携带 cursor？
18. 为什么行情参考不可用时不能影响用户撤单和撮合恢复？
19. 为什么 DEX route 过期后，reference 即使仍可用也不能继承 route 的涨跌、质量或来源？
19. 为什么旧缓存里的 `display_price_usd` 不能覆盖一个更新的 `dex_route_price.version`？
20. A→B→A 时 query key 最终相同，为什么 generation 仍必须不同？
21. 为什么同价、同 version、但更新的 observed time 应当被接受？
