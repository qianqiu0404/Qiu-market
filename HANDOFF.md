# HANDOFF：Qiu Market 七源行情、K 线/DEX 与虚拟现货交易纵切片

> 动态快照：2026-07-26。本文件只记录本次本地交付与剩余正式验收；长期行为以 README 和 canonical docs 为准。

## 2026-07-27 单机生产稳定与极省空间收口

- `symbol_kline` 的长期策略固定为 `1m=7天`、`15m=90天`、`1h=365天`、`1d=永久`。Binance 回填水位已从 symbol identity 改为 `market_id + interval`；不同交易所同名资产不会互相截断。
- 保留任务使用专用 PostgreSQL connection 持有 session advisory lock，按 10,000 行小批量删除并设置短 statement timeout；状态、每周期边界和存储体积进入 System API/UI。
- 生产库已在备份和临时库恢复演练通过后完成首次清理。K 线总关系由约 7.57GB 降为约 2.36GB，其中索引由约 5.6GB 降为约 391MB；过期 1m 行为 0。未执行 `VACUUM FULL`。
- Vercel BFF 完整截止时间为 8 秒，只有只读接口允许一次有界重试；所有写请求不重试并透传 request ID。Trade 面板独立降级、保留 last-good，状态超过 10 秒或 polling/WS 尚未 reconcile 时禁用写操作。
- 订单写超时进入 `submitted/unknown`，沿用原 client order ID 查询权威订单视图，不生成新 ID、不盲目重下。Demo maker 在不安全参考价或低磁盘时撤单暂停，连续三个新鲜样本后恢复。
- 低于 25GiB 告警，低于 15GiB 暂停 crawler/worker/DEX；交易读路径继续可用，而下单、撤单、虚拟入金和 demo maker fail-closed。Guardian 不会盲目重启共享 PostgreSQL。
- 全库每日备份保留 2 份、`trading_*` 每小时备份保留 24 份；每周恢复临时库并校验事件、快照和分资产账本平衡。当前备份仍在同一块物理磁盘，灾难恢复风险已接受但未消除。
- 交易快照 schema v5 紧凑化已终结的 `system:demo-maker` 订单和该系统账户内存幂等缓存，并把运行时 journal 折叠为分资产平衡 checkpoint；事件批次、订单投影和账本投影完整保留，用户状态和开放盘口不裁剪。该升级用于消除 demo maker 长期运行造成的恢复时间和内存无界增长。
- Vercel WebSocket beta 尚无账号环境证据，生产默认降级为同源 cursor polling；WebSocket 验收保持 `environment-pending`。
- 当前仍需机器所有者一次管理员授权，才能把用户 LaunchAgent 提升为无需桌面登录的 LaunchDaemon，并执行 `pmset autorestart 1`。GitHub OAuth 凭据、Preview/Production 同产物晋级和连续 7 天外部 SLO 也必须以真实证据单独收口。

## 可见结果

- Binance、Coinbase、Bybit、OKX 各自从已审核、可交易的 CoinGecko Top 200 候选中冻结 50 个唯一资产，四家不是被迫共用同一张榜。
- All 把七张 selection 按 canonical `asset_id` 去重合并，再按 CoinGecko 市值 rank 排序。2026-07-25 动态并集为 109 个资产，BTC 等交集资产只出现一行。
- Provider 页固定保留自己的 50 个成员；一次 ticker 失败不会删行或清空最后成功值。30 秒内 Fresh，30 秒至 5 分钟 Stale，超过 5 分钟 Unavailable。
- 只有 Fresh CEX Spot 进入 All 综合价和涨跌榜；Perp、DEX、Stale、Unavailable、零价及 3% 离群报价均不参与。
- 四家 CEX 实时行情均为 WebSocket 主链路、REST 周期对账；高频事件按 source symbol 只保留最新一条，每约 5 秒合并写 PostgreSQL。
- Hyperliquid、Uniswap、PancakeSwap 也使用独立、带版本的精确 50 资产选择。
- Hyperliquid 页面展示 Perp Mark；Uniswap/Pancake 页面固定展示 50 个链上身份已复核的 listed assets。路线支持 V2/V3 直连和最多两跳 mixed path，每跳调用对应 Router/Quoter；先问 `$10K`，失败再问 `$1K/$100`。页面显示 protocol path、实际金额和 `CEX corroborated/On-chain only`，全部失败才显示 `Not covered`。
- 全站已经从暗色终端改成 Apple 风格蓝白视觉：浅灰页面、白色内容卡、蓝色交互、独立红绿涨跌语义，并通过 1180/1280/1440 三档页面级无溢出验收。
- 价格可用但 24h 参考缺失时显示明确原因；真实 `0%` 与 Unknown 分开。
- Binance、Coinbase、Bybit、OKX 各把当前 selection version 的 50 个资产冻结到具体 USD-family Spot market；采原生 1m，并只在分钟连续完整时确定性汇总 15m/1h/1d。具体 market 可进入 K 线，综合资产不生成假图。
- `make dev` 在一个 iTerm2 窗口中启动 API、Trading、RPC、Crawler、Worker、Hyperliquid DEX、DW、Frontend 八个标签，S78 固定使用 5174，不影响 5173 的 `xiuqiu-site`。

## 2026-07-26 虚拟现货交易纵切片

- 新增独立 `market-services trading` 进程，只监听 `127.0.0.1:9094`。单市场 `MarketRunner` 独占 BTC/USDT 撮合状态；现有 API 只负责会话、REST/WebSocket 与 loopback gRPC 适配，任一进程故障不拖垮另一个 bounded context。
- 正式迁移 `2026082100023.sql` 创建 event batch、snapshot、outbox、order、trade、balance、ledger、checkpoint 与 session 等十张交易表。事件流是最终真值，投影可重建；运行时只校验迁移，不自行建表。
- 浏览器入口为 `http://127.0.0.1:5174/trade/BTC-USDT`。页面只读取真实 S78 综合参考价和 reviewed venue K 线，不生成假行情；所有交易价格、数量、余额和序列都以十进制字符串跨 JavaScript 边界。
- 本地免 OAuth 必须显式开启且 HTTP 绑定 IP loopback；共享环境只允许 GitHub `qianqiu0404`。写接口使用 HttpOnly/SameSite session、CSRF、Origin 白名单和限流；WebSocket 使用 30 秒一次性 ticket 与 `(sequence,event_index)` cursor。
- `system:demo-maker` 只使用虚拟余额，在新鲜参考价的 ±10/25/50 bps 各挂三档。参考价超过 30 秒或跳变超过 5% 时，先撤单再停止做市。
- 2026-07-26 真实本地浏览器完成：登录 → USDT/BTC 虚拟入金 → 限价挂单 → 撤单 → Market Buy → Maker/Taker 手续费 → WebSocket 增量。优雅退出时 event 与 snapshot 同停在 sequence `619`，状态哈希均为 `a2dfc64679475847568215c2276c3124bc2c5c08861fa9fd37201ddd52b6641b`；重启后余额、2 条历史委托、1 条成交、六档盘口与真实 K 线恢复。
- 同一真实本地流中，BTC 与 USDT 的 `trading_ledger_entry.amount` 分资产求和都为 `0`。这是虚拟交易业务库的验收证据，不是生产资金证明。
- 交易域的 canonical 说明、调用链、不变量、故障矩阵和闭卷题见 [`docs/trading-system.md`](docs/trading-system.md)；实现入口见 [`trading/README.md`](trading/README.md)。
- 边界仍然固定：不接充值、提现、私钥、真实交易所下单或实盘资金；不自动推送、合并主分支或部署。杠杆、永续、期权和策略实验室属于后续目标。

## 设计边界

选币成员与正式发布状态是两件事：`provider_asset_selection` 回答“页面有哪些资产”，`provider_rollout_state` 回答“是否完成正式 canary/enabled 验收”。本地 `make dev` 默认为七源启用 Local Preview，让真实行情立即可见，但不会推进正式 readiness；关闭预览后仍需人工按既定顺序晋级。

被拒绝的替代方案是四家都硬套 CoinGecko rank 1–50。那会让未上市资产占满 `Not covered`，也无法表达交易所真实覆盖。当前方案的代价是 All 可能多于 50 行，但 version、rank、selected_at 和 canonical identity 都可审计。

DEX 同样拒绝按 symbol 自动收录 token。AMM listed asset 可以来自 reviewed manifest 或 CoinGecko platform contract，但必须锚回既有 canonical asset，并经过 ERC-20 decimals 与链上 V2/V3 Factory/token/fee 复核。Listing 资格负责固定 50 行，TVL/成交额/区块/impact/spread/逐跳 Router/Quoter 负责当前能否报价。当前 cycle 失败会权威撤回公开 snapshot 的 available，而不是永久保留旧成功。缺少新鲜 CEX 参考不再抹掉合格双向链上报价，但这种 `onchain_only` 最多为 Medium；不同询价金额的 24h 历史严格分开。DEX 会进入 All 成员并集，但永远不进入 All 综合现货价。

## 关键调用链

1. `catalog/provider-asset-mappings.yaml` + `crawler/catalog_supervisor.go`：加载审核别名、发现真实市场并生成候选。
2. `database/venue_aggregation.go`：原子冻结七源 selection version；七家各 50，并用七家 canonical identity 形成 All。
3. `crawler/spot_ticker_streams.go` + `crawler/spot_ticker_supervisor.go`：四家隔离的 WebSocket primary、REST reconcile 和 5 秒 latest coalescing；规范化 price/open/change 后进入唯一 writer。
4. `marketdata/composite.go`：只用 Fresh 四家 Spot 生成资产综合价。
5. `crawler/cex_kline_supervisor.go` + `crawler/cex_kline_adapters.go`：四家各 50 market 的原生 1m、确定性大周期与按 provider repair。
6. `crawler/hyperliquid.go` + `crawler/amm_dex.go`：分别产生 Perp Mark 与 V2/V3 最多两跳、`$10K → $1K → $100` route quote。
7. `services/http/service/market_index.go` + `frontend/src/views/Markets.vue`：返回七个固定 50 provider 页面、All 七源并集、新鲜度和按需市场/路线抽屉。

## 故障、降级与恢复

| 故障 | 可见行为 | 恢复 |
|---|---|---|
| 单家 ticker 失败 | 保留成员和最后成功值，Fresh → Stale → Unavailable；该来源退出综合价 | 下一次成功原地恢复 Fresh |
| 24h open/percent 缺失 | 价格仍可展示，24h 为 Unknown 并给出 `missing_24h_reference` | provider 补齐后恢复 |
| selection 成员排名变化 | 不静默换币 | owner 显式刷新才生成新版本 |
| Redis 故障 | PostgreSQL 可信状态仍可重建缓存 | Redis 恢复后重建 |
| Doris 故障 | 实时 Markets 继续工作，历史模块独立降级 | DW 恢复后继续对账 |
| 私有 DEX 端点未配置 | 正式路径显示 Unconfigured；本地可显式启用公共只读回退，不拖垮 CEX | 配置私有端点或修复公共来源后单独验证 |
| DEX 一次报价失败 | 稳定选择成员保留，Fresh → Stale → Unavailable | provider 独立退避后原地恢复 |
| `$10K` 报价因冲击或 Quoter 失败 | 自动尝试 `$1K/$100`；成功时标 Low 并显示实际金额 | 流动性恢复后下一轮重新优先 `$10K` |
| 缺少新鲜 CEX 参考 | 双向链上门槛通过则 `On-chain only`，最高 Medium | CEX 恢复后自动追加偏差校验 |
| AMM 尚未观察满 24h | 价格可展示，24h 明确 Unknown | 同一路线、同询价金额连续覆盖满窗口后自动恢复 |
| Binance stream 同时含 `o` 与 `O` | 分别解析开盘价和窗口开始时间；不能让时间戳覆盖开盘价 | 协议回归测试锁定大小写组合 |
| 单家 CEX K 线稀疏/失败 | 只少写该家 bucket 并生成 repair，不用另一家补 | 原 provider 恢复后重叠续传 |
| 本轮 DEX route 失败 | 当前公开 snapshot 变 unavailable，技术审计行保留 | 下一轮同 selection 路线合格后原地恢复 |

## 证据边界

- `implemented`：迁移到 `2026082100023.sql`、七源独立 50 selection、All canonical union、四家 WS-first/REST-reconcile/5 秒写入、四家版本化 K 线 selection、原生 1m 与确定性大周期、V2+V3 mixed AMM、权威 DEX snapshot、公共只读本地回退、preview/正式隔离、蓝白 UI、独立交易进程、PostgreSQL 事件流、鉴权网关和虚拟交易终端均已落地。
- `build-verified`：2026-07-26 运行 `go vet ./...`、`go test ./...`、`go test -race ./trading/...`、10 秒 fuzz、撮合 benchmark、Vitest 8/8、Vue production build、Playwright 16/16、npm audit、`make verify-local`、隔离 PostgreSQL 集成脚本、shell syntax 与 `git diff --check` 均通过。
- `integration-verified`：真实本地 PostgreSQL 已按 canonical migration 初始化；七个 provider 各 50、All 为 109 个 canonical asset。Crawler、DEX、API、Trading 与 Frontend 的真实链路完成浏览器交易和重启恢复验收；交易状态哈希与分资产账本平衡证据如上。真实公共 RPC、V2/V3 合约和 mixed protocol path 已交换数据；动态行情数量不是永久覆盖承诺。
- `environment-pending`：Uniswap/Pancake route 同 route/名义金额尚未连续观察满 24h；正式 CEX/DEX 24/48/72 小时 rollout、私有 DEX 端点、四家 K 线长期缺口率、DW 长时安全门、GitHub OAuth 生产凭据、HTTPS Cookie 与最终七天仍未完成。任何状态不会自动晋级。
- `production-recommendation`：真实资金、充值提现、公开 API 配额/SLA、衍生品和策略实验室都必须作为后续独立切片。

## Owner 60 秒解释

> 七个 provider 各自冻结 50 个身份审核通过的资产；All 按 asset_id 去重，所以 BTC 只有一行。四家 CEX 用 WebSocket 收实时 ticker，REST 对账漏项，5 秒合并写入；只有 Fresh Spot 进入综合价。K 线另把四家 selection 固定到具体 market，采 1m 后确定性汇总。AMM 允许 V2/V3 最多两跳混合路线，按 `$10K/$1K/$100` 分级询价，本轮失败就撤回公开可用态。Local Preview 不改变正式 rollout。

## 闭卷自检

1. 为什么四家 selection 可以不同，而 All 不会出现两个 BTC？
2. 为什么 selection 不能等同于当前 Fresh 报价集合？
3. Stale 为什么可以展示，却不能进入综合价和涨跌榜？
4. 本地 Local Preview 为什么不等于正式 Enabled？
5. 为什么真实 `0%` 不能与缺失 24h 参考共用一个值？
6. 为什么 Top 200 是候选池，而不是首页最终行数？
7. 为什么 ticker 扩展不能自动扩大 K 线范围？
8. Doris 故障时为什么 Markets 仍应可用？
9. 为什么 DEX 可以固定 50 个资产，却不能为了填满价格而放宽身份或路线质量？
10. 为什么 DEX Screener 发现的价格不能直接成为 Qiu Market 的 route quote？
11. 为什么 AMM 有当前价却仍可能没有 24h 涨跌？
12. 为什么蓝白视觉改造不能顺手改变 All 的 contributor 规则？
13. 为什么缺少 CEX 参考时可以展示 On-chain only，却不能标 High？
14. 为什么 `$10K` 与 `$1K` 的观察不能混成一条 24h 曲线？
15. Binance `o` 与 `O` 为什么必须是两个结构体字段？
16. WebSocket 已经实时，为什么还需要 REST reconcile？
17. 四家 K 线为什么采同一粒度，但不能互相补洞？
18. mixed route 如何决定每一跳走 V2 Router 还是 V3 QuoterV2？

## 启动与继续验收

```bash
make dev
make dev-status
```

行情入口是 `http://127.0.0.1:5174/markets`，虚拟交易入口是 `http://127.0.0.1:5174/trade/BTC-USDT`。正式验收时使用 `S78_CEX_PREVIEW=0 S78_DEX_PREVIEW=0 S78_DEX_PUBLIC_FALLBACK=0 make dev`，再按 CLI readiness 证据人工晋级；不要把本地完成版的证据冒充 24/48/72 小时生产级 soak。
