# DEX 行情管线：Hyperliquid Perp 与 Uniswap/PancakeSwap V2+V3 智能路线

## 功能与当前作用

`dex` 仍是一个本地进程标签，内部有三个互不拖累的 supervisor：

- Hyperliquid 每 5 秒读取公开 `metaAndAssetCtxs`，产生 `perp_mark`；
- Uniswap 固定 Ethereum Mainnet V2 + V3；
- PancakeSwap 固定 BNB Chain V2 + V3；
- 两家 AMM 有两条发现路径：正式环境使用私有 Subgraph/RPC；本地 Local Preview 在没有私有端点时可用 DEX Screener 发现候选池和限流的公共只读 RPC。无论候选来自哪里，都必须在链上复核 token0/token1、对应版本 Factory；V3 还核验 fee。路线允许稳定币直连或最多两跳，且可组合 V2→V2、V3→V3、V2→V3、V3→V2。每一跳分别调用 V2 Router `getAmountsOut` 或 V3 QuoterV2，按 `$10K → $1K → $100` 做双向只读询价。

- 资产首页仍是一项资产一行；
- Hyperliquid 在 Perpetual Markets；AMM 在 DEX Routes；
- 三家 DEX 都使用带版本的稳定 50 资产选择；Hyperliquid 选择身份确认的 Perp，AMM 选择 chain+contract+pool 身份确认的 listed assets；
- AMM 的“列入 50”与“当前能否给出指示性报价”分开；优先展示能通过 `$10K` 门槛的路线，大额冲击或 Quoter 失败时可降级到 `$1K/$100` 并标成 Low，仍不合格时保留行并显示 `Not covered`；
- 三家 DEX 都进入 All 的 canonical 资产成员并集；
- `asset_price_index` 永久排除 Perp 和 DEX；
- 没有真实 Hyperliquid K 线时不显示 venue chart 入口。

## 两套身份模型

Hyperliquid 是 provider market，仍使用 `provider_market_candidate -> exchange_symbol`。旧实现按全局 symbol 自动创建资产，现改为 reviewed alias + Top 200 门。

1. 每个 universe item 写入 `provider_market_candidate`；
2. base 和 USD quote 都必须存在 `provider=hyperliquid` 的 approved `asset_alias`；
3. base asset 必须在 CoinGecko Top 200；
4. delisted、未审核、ambiguous 或 Top 200 外合约只留审计原因，不创建新资产；
5. manifest 可修复仅由 `migration-existing-catalog` 盖章的旧 provider-local identity；人工审核冲突会停止。

AMM 不创建 CEX market。它使用 `blockchain_network -> asset_representation -> dex_pool_candidate -> dex_route_current`：同 symbol 不足以证明同一 token，chain + contract 才是链上身份。代码评审 manifest 是第一条可信来源；为补足更宽的 Top 200 覆盖，crawler 还读取 CoinGecko 的 platform contract 映射，将已知 CoinGecko external ID 锚回既有 canonical asset，再核验 ERC-20 decimals。无论 representation 来自哪条路径，pool 仍必须在链上核验 token0/token1/factory/fee；contract 冲突会跳过，不能按 symbol 自动合并。

## 数据与时间映射

| 上游字段 | 规范字段 |
|---|---|
| `midPx`，缺失回退 `markPx` | `symbol_market.price` |
| `markPx` / `prevDayPx` | `change_24h_pct = (markPx-prevDayPx)/prevDayPx*100` |
| `prevDayPx` | `open_24h` |
| `dayNtlVlm` | quote turnover |
| 无明确事件时间 | `source_time/source_time_kind = NULL` |
| 本服务解析完成时间 | `observed_at` |

AMM 固定规则：

| 规则 | 值 |
|---|---|
| 路线 | 稳定币直连，或最多两跳；中间资产只允许审核过的 WETH/WBNB/稳定币 |
| 每 token pool discovery | token0/token1 各最多 12 条 |
| 池门槛 | TVL ≥ $1M；24h volume ≥ $100K |
| 询价 | 按 `$10,000 → $1,000 → $100` 双向逐跳调用 V2 Router 或 V3 QuoterV2；首个通过质量门的金额成为实际 `quote_notional_usd` |
| 可用门槛 | block delay ≤60s；impact ≤1%；round-trip spread ≤2%；存在新鲜 CEX 综合价时偏差还必须 ≤3% |
| CEX 参考缺失 | 不再拒绝已通过双向链上门槛的报价，标记 `onchain_only`；质量最高只能 Medium |
| 质量 | `$10K` 为 Medium；有 CEX 交叉验证且满足更严流动性/冲击条件才可 High；`$1K/$100` 一律 Low |
| quote-side impact | 同一区块双向 buy/sell 各自偏离中点的最大百分比；不使用 6 小时目录发现价冒充当前基准 |
| 24h 涨跌 | 必须是同一 route、同一 `quote_notional_usd` 的内部观察；24h 窗口开始后 30 分钟内建立，历史采样最大缺口不超过 10 分钟。本次通过全部链上质量门的当前报价作为窗口终点；公开 route price 仍独立要求 30 秒 freshness |
| 观察保留 | `dex_quote_observation` 保留 8 天，足以独立核验 24/48/72 小时窗口，并为后续七天观察留出余量 |

`dex_pool_candidate.quote_eligible` 把两层资格显式分开：链上身份成立的 pool 足以证明该资产“在这个 provider 有 listed pool”，可用于固定 50 资产目录；只有 TVL/成交额门槛也通过的 pool 才能进入 route 构造，最终还要通过区块、impact、spread 和偏差门槛才有公开报价。它解决的是“页面成员不能被一次 Quoter 失败删掉”，不是降低行情质量。

数值进入 SnapshotWriter 前放大 1e8。Hyperliquid 单 adapter 串行提交，无 source time 时按项目的 source-free 规则处理；PG 接受后才派生 Redis。

## 数据流

```text
POST /info metaAndAssetCtxs
        |
        v
auditCatalog
  -> approved alias + Top 200 ?
  -> yes: existing/canonical market
  -> no: Catalog Audit only
        |
        v
processPerp
  -> normalize price/open/change/turnover
        |
        v
SnapshotWriter -> PostgreSQL -> Redis
        |
        +-> asset market drawer (Perp)
        +-> v1 Insights cross-venue comparison
        X-> asset composite index

CoinGecko platform contract / reviewed manifest
        -> canonical asset + onchain decimals
        |
Subgraph / DEX Screener pool discovery
        -> onchain V2/V3 pool identity
        |
        v
listed pool -> exact 50 asset selection
        |
        +-> All canonical asset union
        |
quote-eligible pool -> direct / max-two-hop route
        -> per-hop V2 Router / V3 QuoterV2 $10K buy + sell
        -> quality fail: retry $1K, then $100
        |
        v
dex_route_current -> rollout/preview gate
        -> asset_venue_snapshot
        |
        +-> Uniswap/Pancake tab + DEX Routes drawer
        X-> All composite index
```

`dex_route_current` 是技术审计表，不是公开发布事实。公开 route count 和抽屉再次经过当前 provider selection 与 rollout/preview 过滤；shadow/paused 的技术报价即使存在，也不会泄露成用户可见路线。Hyperliquid 也先把 alias 解析成 canonical asset，再检查同一套稳定选择。选择成员不会因一次上游故障跳动：旧行保留，报价从 Fresh 降为 Stale/Unavailable。

## 设计决策、替代方案与代价

1. **Hyperliquid 复用 market；AMM 不复用。** Perp 是明确的 venue market；pool/route 是链上合约关系，塞进 `exchange_symbol` 会丢失 chain、contract、fee 和路径。
2. **审核 alias，不再自动建资产。** 被拒绝的是按 symbol 复用或新建 `h-a-*`；它省人工但会污染 canonical identity。代价是新合约先进入审计。
3. **Perp 不进综合现货价。** mark/mid price 和 Spot 可比较，但不是现货可执行报价。Hyperliquid 独有资产可以进入 All 成员并集，但在没有新鲜 CEX Spot 时 Price 显示 Unknown。
4. **DEX 只发布指示性路线价。** 被拒绝的是叫 basis、套利机会或交易信号；没有 gas、MEV、交易签名和成交保证。
5. **公开发现只提供候选，链上复核才建立信任。** 被拒绝的是直接把 DEX Screener 的价格当成项目报价；它无法证明本项目选中的 Factory、fee、路径和分级 Quoter 结果。代价是公共 RPC 更慢、覆盖更少，但本地无需保存密钥。
6. **DEX 固定 50 个 listed assets，但报价资格独立。** 被拒绝的是按 symbol 猜身份、把 DEX Screener price 当项目报价，或为了填价格放宽 Quoter 门槛。代价是 50 行中会有 `Not covered`，但每个成员都有 canonical asset、chain、contract 与链上 pool 证据。
7. **endpoint 缺失只降级对应 DEX。** 私有 Graph/RPC URL 仅来自环境变量，代码、文档和日志都不保存真实 endpoint/key。已配置 endpoint 的首次连接/chain-id 校验失败会 30 秒到 10 分钟自动重试；本地公共回退也保持 provider 隔离和限流。
8. **暂不接 K 线、funding、open interest。** 本切片只做当前行情。
9. **CEX 参考是交叉验证，不是 DEX 报价的存在条件。** 被拒绝的是“没有 CEX price 就把真实双向链上报价清空”；这会让 DEX 独有资产永远无价。没有 CEX 时仍必须通过区块、池门槛、双向 Quoter、impact 和 spread，且 `quote_reference_kind=onchain_only`，不能获得 High。
10. **不同名义金额的历史严格分开。** 被拒绝的是把 `$10K/$1K/$100` 观察混成一条 24h 曲线；`dex_quote_observation.quote_notional_usd` 参与窗口查询，降级金额变化后要重新积累该金额的 24h 覆盖。
11. **impact 使用同一区块双向中点。** 被拒绝的是拿 6 小时一次的目录发现价和当前 Quoter 价格比较；正常市场波动会被误报为滑点。现在 impact 衡量 buy/sell 两侧相对本次中点的最大偏差，CEX 3% 偏差仍是独立外部交叉验证。它仍是指示性质量指标，不是成交滑点保证。

## 关键代码入口与顺序

1. `catalog/provider-asset-mappings.yaml`：reviewed provider alias 与 chain contract。
2. `crawler/hyperliquid.go`：Perp 目录、规范化和 `perp_mark`。
3. `crawler/amm_dex.go`：Subgraph/公共候选发现、V2/V3 Factory 校验、混合 route 构造，以及逐跳 Router/Quoter 询价。
4. `migrations/2026081600018.sql` 至 `2026082000022.sql` + `database/venue_aggregation.go`：把三家 DEX 纳入版本化稳定选择，分离 listed pool 与 quote eligibility，保存 `protocol_versions`，按 route + 实际名义金额隔离 24h 观察，并用当前 cycle 权威替换公开 venue snapshot。
5. `services/http/service/market_index.go`：按当前选择读取 Perp/DEX venue 页面和按需抽屉；All 合并七家资产身份，但综合 Price 仍只读四家 CEX。

## 术语

| 术语 | 准确含义 | 大白话 | 当前项目位置 |
|---|---|---|---|
| Perpetual | 没有固定到期日的衍生品合约 | 长期滚动的合约报价 | Hyperliquid markets |
| mark price | 用于估值/风控的参考价 | 不让一笔异常成交带偏的价格 | `markPx` |
| settlement asset | 盈亏结算/保证金资产 | 最后用什么币结账 | 未来 USDC 字段 |
| provider alias | 某 provider 代码到 canonical asset 的审核映射 | 确认 HYPE 在这里到底是谁 | `asset_alias` |
| source-free snapshot | 上游无明确事件时间的快照 | 只知道我们什么时候收到 | `source_time=NULL` |
| Asset representation | canonical asset 在一条链上的审核 token contract | 这枚币在这条链上的合法分身 | `asset_representation` |
| Route quote | 固定路径、明确名义金额的双向只读询价 | 先问 `$10K`，不行再问 `$1K/$100` | `dex_route_current` |
| On-chain only | 没有新鲜 CEX 参考、但链上双向质量门已通过 | 有链上证据，但少了一次场外对表 | `quote_reference_kind=onchain_only` |
| V2 Router | 常数乘积池的只读路径报价入口 | 老式柜台的询价器 | `getAmountsOut` |
| QuoterV2 | V3 集中流动性池的链上模拟报价合约 | 新式柜台的询价员，不替你下单 | `quoteExactInput` |
| Mixed route | 一条路线的不同 hop 使用不同 AMM 版本 | 中途换了一种柜台，但身份和路径仍可审计 | `protocol_versions` |

## 启动与验证

```bash
make dex
```

日常本地开发直接执行 `make dev`。启动器会显式打开 DEX Local Preview；若没有私有端点，`dev-role.sh` 只在本地为 `dex` 角色启用公共只读回退。直接运行二进制或正式验收时，公共回退默认关闭。

Mac live runtime 也保持默认关闭。若 owner 明确选择内置公共只读 RPC/DEX Screener
fallback，只能在 runtime 私有文件
`config/provider-readonly.env` 写一行：

```text
MARKET_DEX_PUBLIC_FALLBACK=true
```

`ops/macos/live-role.sh` 只为 `dex` 读取该文件，且要求它是当前用户拥有的普通文件、
非软链、权限精确为 `0600`。解析器只接受上述键以及 `true/false`，不 `source` 文件、
不读取 worktree `.env`、不输出值；文件缺失时仍为 `false`。crawler/worker 不消费该
配置。被拒绝的是从 world-readable worktree 环境或任意 shell 内容注入 runtime；代价
是配置格式刻意很窄，但公开 fallback 的权限与来源可审计且不会扩张 secret 边界。

正式环境建议私下配置以下变量；空值且未显式打开公共回退时只显示 Unavailable，Hyperliquid 与 CEX 继续：

```text
MARKET_ETHEREUM_RPC_URL
MARKET_BSC_RPC_URL
MARKET_UNISWAP_V3_SUBGRAPH_URL
MARKET_PANCAKE_V3_SUBGRAPH_URL
```

正式 Canary 前先运行只读自检：

```bash
source .env
./market-services catalog endpoint-check --provider uniswap
./market-services catalog endpoint-check --provider pancakeswap
```

本地公共回退可显式验证：

```bash
MARKET_DEX_PUBLIC_FALLBACK=1 \
  ./market-services catalog endpoint-check --provider uniswap
MARKET_DEX_PUBLIC_FALLBACK=1 \
  ./market-services catalog endpoint-check --provider pancakeswap
```

它验证 chain ID、最新区块不超过 60 秒、V2 Factory/Router 与 V3 Factory/QuoterV2 都存在合约代码，并验证 Subgraph `_meta` 或 DEX Screener 候选池能通过链上版本化身份复核。错误经过脱敏，命令输出不会回显 endpoint 或 API key；该检查不需要私钥、助记词、钱包或签名权限。

进程启动和 endpoint-check 在任何 RPC 调用前都会校验两家 AMM 的 V2 Factory、V2
Router、V3 Factory、QuoterV2、Stable 与 Bridge 共 12 个地址：必须是 `0x` 开头的
40 位 hex 且非零。任一地址拼写错误会 fail closed，Hyperliquid/AMM supervisor 都不
启动；不会再由 `HexToAddress` 静默截断。Uniswap V2 Router02 固定为官方 40 位
`0x7a250d5630b4cf539739df2c5dacb4c659f2488d`。

观察 System：

- process status 证明 `dex` 在运行；
- provider status 证明 `metaAndAssetCtxs` 最近是否成功；
- Catalog Audit 解释 rejected/ambiguous；
- Markets 的三个 DEX 页各读取 50 个稳定成员；抽屉显示已审核的 Perp 或 route，没有可用 route 的 AMM 成员显示 `Not covered`；
- Local Preview 必须明确显示为预览，不能冒充正式 Enabled。

数据库核验：

```sql
SELECT provider, resolution_status, count(*)
FROM provider_market_candidate
WHERE provider = 'hyperliquid'
GROUP BY provider, resolution_status;

SELECT es.market_code, sm.price, sm.open_24h, sm.change_24h_pct, sm.observed_at
FROM exchange_symbol es
JOIN symbol_market sm ON sm.market_id = es.guid
WHERE es.market_code LIKE 'hyperliquid:%';
```

动态市场数量只是现场快照，不能写成永久规模结论。

## 故障与恢复

| 场景 | 行为 | 恢复 |
|---|---|---|
| HTTP 超时/非 200 | 本轮失败，旧快照自然变 stale | 5 秒后下一轮尝试 |
| universe/ctx 长度不同 | 只处理两侧都有的下标并告警 | 上游恢复完整响应 |
| 单合约字段缺失 | 跳过该合约，其他继续 | 下一轮字段完整后恢复 |
| alias 未审核 | 不写正式 market，Catalog Audit 展示 | 审核后重启/下一轮 |
| asset 不在 Top 200 | rejected，不进入正式首页 | 排名变化后目录刷新重评 |
| Redis 写失败 | PG 状态仍可保留；缓存/榜单降级 | Redis 恢复后派生重建 |
| 无 K 线 | 不显示 chart 入口 | 独立 K 线设计实施后再启用 |
| Graph 429 / malformed response | 对应 AMM 独立退避 30s→10min | 下一次 discovery 成功 |
| DEX Screener 或公共 RPC 限流 | 只影响对应本地 AMM，保留稳定选择并让报价自然降级 | 限流退避后重新发现/询价，或配置私有端点 |
| RPC 首次连接失败或 chain id 不符 | 不打印 endpoint，记录下一次重试并按 30s→10min 退避 | endpoint 恢复/修正后自动建立 session |
| 任一 Factory/Router/Quoter/Stable/Bridge 地址非 40 位 hex 或为零地址 | dex 启动前失败关闭，不启动任何 provider loop | 修正代码评审配置并重新构建候选 |
| `provider-readonly.env` 缺失 | live DEX 公共 fallback 保持 false，私有端点也为空时 AMM 明确 unavailable | owner 创建合规 0600 文件并受控重启 dex |
| `provider-readonly.env` 是软链、非 0600、含未知/重复键 | dex wrapper 拒绝启动，错误不回显内容 | 修正 owner/mode/唯一布尔键后受控重启 |
| token decimals、protocol version 或 pool factory 不符 | 不发布该 provider 路线 | 修正 reviewed manifest/上游 |
| block 超过 60 秒 | 本轮 route `available=false` | 新区块和下一轮 quote |
| `$10K` Quoter/impact/spread 未通过 | 依次尝试 `$1K`、`$100`；成功则 Low 并展示实际名义 | 流动性恢复后下一轮自动优先回到 `$10K` |
| impact/spread 超限 | 50 资产成员保留，route 审计保留但公开价格 Unknown | 路线质量恢复后自动发布 |
| 无新鲜 CEX 综合价 | 合格双向链上报价以 `onchain_only` 发布，最高 Medium | CEX 恢复后下一轮自动增加偏差校验 |
| CEX 偏差超限 | 尝试更小名义；仍超 3% 则公开价格 Unknown | 两侧价格恢复一致后自动发布 |
| 24h 观察跨 route、跨名义金额或历史采样中断超过 10 分钟 | 当前 route 的 24h change 为 Unknown | 同一路线、同金额连续覆盖满窗口后自动恢复；当前 route price 仍按 30 秒 freshness 单独判断 |

## 证据边界

- `implemented`：三家 DEX 的版本化精确 50 资产选择、Local Preview/正式 rollout 隔离、Hyperliquid Perp、CoinGecko platform contract 锚定、AMM 双发现路径、V2+V3 多池最多两跳、链上身份复核、listing/quote eligibility 分离、`$10K → $1K → $100` 分级询价、on-chain-only 降级、route+notional 24h 覆盖、当前 cycle 权威快照与 CEX-only 综合价已经存在。
- `build-verified`：以本次交付记录的 Go、前端和端到端门禁为准；单测包含 V2/V3 编码、混合路线、当前 route 权威替换与失败隔离。
- `integration-verified`（2026-07-25 本机动态快照）：真实公共只读 RPC、V2/V3 Factory、V2 Router、V3 QuoterV2 已交换数据；数据库和浏览器看到 direct、V2→V3、V3→V2 等实际 protocol path。公开 snapshot 只保留当前 selection/current cycle 的路线，失败路线会变 unavailable，不能靠旧 `available=true` 长期冒充新鲜报价。动态数量不是永久覆盖承诺。
- `environment-pending`：新 AMM route 尚未连续观察满 24 小时，因此 24h 涨跌仍为 Unknown；私有 Subgraph/RPC 正式路径、24/48/72 小时 rollout soak 和最终七天验收仍未完成。
- `production-recommendation`：funding、open interest、settlement asset 和 Hyperliquid K 线。

## Owner 60 秒解释

> `dex` 进程里有三个隔离 supervisor。Hyperliquid 是 Perp；Uniswap/Pancake 用 canonical asset、chain、contract、protocol version 和 Factory 认池，允许 V2/V3 直连或最多两跳混合路线。每一跳只读询价，先问 `$10K`，不合格再降到 `$1K/$100`；没有 CEX 参考但链上双向门槛通过时标 On-chain only，不能给 High。当前 cycle 会权威撤回失败路线。Perp/AMM 永不贡献 All 综合现货价，也不声称可执行套利。

> live DEX 公共 fallback 默认关闭；唯一开关来自 runtime 内 owner-only 0600 普通文件，wrapper 只解析一个布尔键，不执行文件，也不读 checkout `.env`。二进制在启动 provider loop 前校验两家 AMM 共 12 个合约/token 地址，任一不是 40 位非零 hex 就整体失败关闭。

## 闭卷自检

1. 为什么 Hyperliquid quote 是 USD，而 USDC 不能冒充 quote？
2. 为什么内存 registry 不能再决定 canonical asset identity？
3. Perp 为什么能计算相对综合价偏差，却不能成为 contributor？
4. source time 为什么必须为 NULL？
5. 新 Hyperliquid 合约从发现到可展示要经过哪些门？
6. 为什么 AMM pool 不能存成 `exchange_symbol`？
7. 为什么 `$10K/$1K/$100` 双向询价仍不是可执行成交价？
8. Graph 正常但 RPC chain id 错误时，为什么必须整家 provider 停止发布？
9. 为什么 `dex_route_current` 有数据仍不代表用户应该在抽屉看到它？
10. 为什么 DEX Screener 可以发现候选，却不能直接成为 Qiu Market 的路线报价？
11. 为什么 DEX 页面固定 50 行，但 `priced_asset_count` 可以只有 9 或 6？
12. DEX 独有资产为什么能进入 All 行成员，却不能贡献 All 的综合 Price？
13. 为什么缺少 CEX 参考时可以发布 On-chain only，却不能标成 High？
14. 为什么切换询价金额后必须重新累计该金额的 24h 覆盖？
15. 为什么目录发现价不能作为 15 秒 Quoter 的当前 impact 基准？
16. 一条 V2→V3 mixed route 如何决定每一跳调用哪个只读合约？
17. 为什么本轮 route 失败后要把公开 snapshot 置为 unavailable，而不能永久保留旧 available？
18. 为什么 `provider-readonly.env` 不能被 shell source，也不能放在 worktree？
19. 为什么一个错误 AMM 地址要在启动任何 provider loop 前失败，而不是等到 RPC 调用时再报错？
