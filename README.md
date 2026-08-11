# Qiu Market

Qiu Market 是一个行情与虚拟交易学习产品：从外部交易所与行情平台采集加密货币价格、K 线、市值和法币汇率，经过精度转换与聚合后存入 PostgreSQL / Redis，并通过 HTTP API、gRPC 和 Vue 前端提供多场所行情；独立 `trading` 进程还提供仅使用虚拟资金的 BTC/USDT 撮合、账本和恢复教学纵切片。

仓库中的 Go module、可执行文件 `market-services`、数据库表和 `MARKET_*` 环境变量暂时保留原技术标识，避免一次品牌更名破坏迁移与现有运行环境；用户可见产品、Vercel 项目和运维服务统一使用 **Qiu Market**。

## 仓库身份与工作区约定

- 唯一 GitHub 代码仓库是 [`qianqiu0404/zxq-s78-market-services`](https://github.com/qianqiu0404/zxq-s78-market-services)。`Qiu Market` 是产品名，`market-services` / `S78 Market Services` 是仍在使用的技术标识。
- `WorkSpace` 根目录中的 `qiu-market-*` 目录是这个仓库的 Git worktree，不是独立项目或独立 GitHub 仓库。一个 worktree 只承载一个明确的分支任务。
- `release-*` 用于固定验证快照，`trade-v1-*` 用于产品纵切片，`research-*` 及 contract/publication/accounting 类目录用于研究与契约切片，`trading-lab-*` 用于实验；分类名不代表已经合入、部署或验收。
- 新改动从已核对的干净 `origin/main` 建独立 worktree。脏工作区只作调查线索；完成的 worktree 在确认无独有改动并由 Owner 审阅清理报告后移除。
- 开始跨模块改动或整理工作区前运行 `make repo-audit`。输出中的 `ancestor` 只证明提交可达，不证明脏文件、squash patch、PR、部署或清理安全。
- 本地 `.env`、私钥、wallet/TSS material 和数据库状态不得进入 Git。提交前运行 `make security-paths-test security-paths security-env-templates-test security-env-templates`；规则、模板语法、轮换与事故响应见 [Sensitive local files](docs/sensitive-files.md)。

## 架构

```text
External Data Sources
  - Binance / Coinbase / Bybit / OKX Spot
  - Hyperliquid metaAndAssetCtxs（Perp）
  - Ethereum Uniswap V2+V3 / BNB Chain PancakeSwap V2+V3（指示性多池路线）
  - CoinGecko Top 200 / global
  - Open ER API fiat rates
        |
        v
Catalog + Crawler / Dex
  - Top 200 候选池 + provider 代码评审身份清单
  - 七家独立、版本化的 50 资产产品选择
  - provider 候选目录 + alias / chain representation 审核
  - provider 独立 shadow / canary / enabled / paused
  - 独立 adapter supervisor + 退避
  - shared snapshot writer（唯一行情写入口）
  - PostgreSQL 真值 + Redis 派生缓存 / ZSET
  - 5 秒多 venue 综合现货价
Worker
  - K 线缺口扫描
  - 只生成 repair task，不访问交易所
        |
        v
Service Layer                dw 进程（每 60s）
  - Asset                      PostgreSQL --Stream Load--> Apache Doris（OLAP）
  - Exchange                                                |
  - Symbol                                                  v
  - CMC-style Asset Dashboard                   闭合 1h 固定窗口历史动量
  - Composite Price / Catalog Audit
  - Realtime Insights                                       |
  - Klines（分周期原生）                                       |
  - Fiat Rates / Top Movers                                 v
  - Asset Momentum <----------------------------------------+
        |
        v
HTTP API / gRPC（共用业务层，数据一致）
        |
        v
Vue Dashboard（Qiu Market，蓝白金融产品风）
```

虚拟交易是独立故障域：共享 HTTP API 通过本机 `127.0.0.1:9094` gRPC 调用 `market-services trading`，撮合事件与账本写 PostgreSQL。交易进程异常只降级 `/api/v1/trading/**`，行情页面继续工作；行情参考异常只会让虚拟 demo-maker 撤单停机，不阻断撮合恢复。

Mac mini 单机生产、极省空间 K 线保留、备份恢复、Guardian 与 Vercel 验收见
[`docs/qiu-market-vercel-mac.md`](docs/qiu-market-vercel-mac.md)。滚动生产证据使用：

受保护 Preview 的临时 tunnel 不等于生产入口。它使用独立 front door、原子 readiness
generation、503/no-store drain 与外侧 BFF 探针；完整停启顺序和失败边界也记录在同一
上线手册中。

公网 BFF 的 HMAC 信封除时间戳、方法、精确 RequestURI 和 body digest 外，还绑定
一次性 128-bit nonce；Mac mini 在有界时窗内原子拒绝 nonce 重放。Vercel 的只读
重试每次都重新签发 nonce，运维健康探针遵守同一契约。完整边界与故障恢复见上述
上线手册的“BFF HMAC 与重放边界”。

```bash
bash ops/macos/manage-observer.sh status
bash ops/macos/summarize-production-slo.sh
```

## 核心能力

| 模块 | 作用 |
|---|---|
| `crawler` | 刷新 CoinGecko Top 200 候选池、维护四家独立 selection、以 WebSocket 主链路 + REST 对账采集 Spot、计算综合现货价，并维护四家版本化 K 线 |
| `dex` | 在同一进程内隔离运行 Hyperliquid Perp、Ethereum Uniswap V2+V3、BNB Chain PancakeSwap V2+V3；都不参与 All 综合现货价（详见 [docs/dex-hyperliquid.md](docs/dex-hyperliquid.md)） |
| `worker` | 扫描 K 线缺口并生成持久化 `kline_repair_task`；每日执行 `1m=7天 / 15m=90天 / 1h=1年 / 1d=永久` 的有界保留，不访问交易所、不写价格 |
| `dw` | PostgreSQL → Apache Doris 数仓同步进程；旧公开流旁边已增加 `sync_seq` 固定回看 + UNIQUE KEY 的 v2 影子流（详见 [docs/doris-analytics.md](docs/doris-analytics.md)） |
| `database` | GORM + PostgreSQL 表模型和查询 |
| `services/http` | v1 市场/Insights/K 线与 v2 综合资产首页、按需市场抽屉、Catalog Audit |
| `services/grpc` | gRPC MarketService：16 个只读 RPC 与 HTTP 共用业务层（历史动量当前仅 HTTP，详见 [docs/grpc-service.md](docs/grpc-service.md)） |
| `trading` | BTC/USDT 虚拟撮合、available/held 双重记账、PostgreSQL 事件流/快照/outbox、loopback gRPC、鉴权 REST/WebSocket 和 demo-maker（详见 [docs/trading-system.md](docs/trading-system.md)） |
| `redis` | 热点价格缓存（TTL 抖动）+ ZSET 24h 涨跌幅榜（详见 [docs/redis-top-movers.md](docs/redis-top-movers.md)）+ 进程心跳（`market:heartbeat:<role>`，5s 刷新 / TTL 15s，System 页真实状态来源） |
| `frontend` | Vue3 + Vite + TypeScript 行情与虚拟交易终端（详见 [docs/frontend.md](docs/frontend.md)） |

## 工程设计与实现要点

### 数据流水线

Crawler 启动时载入 CoinGecko Top 200 候选池、provider 代码评审清单与四家 CEX 目录，此后市场目录每 6 小时、资产指标每 5 分钟刷新。新市场先进入 `provider_market_candidate`，只有 provider 级 `asset_alias` 已审核且 rollout 允许才可启用；按 symbol 猜身份被禁止。四家 CEX 从审核通过、可交易的 Top 200 Spot 候选中各自冻结 50 个资产；Hyperliquid 从身份确认的 Perp 中冻结 50 个；Uniswap/PancakeSwap 从链上身份复核通过的 listed assets 中各自冻结 50 个。All 读取七张选择的 canonical `asset_id` 去重并集，并按全局市值顺序稳定分页。

Overview 与 dashboard 必须从同一 selection union 对账：display predicate 为真的资产计为 priced/displayed，其余（包括 LEFT JOIN 后没有 snapshot、SQL 值为 NULL 的资产）统一计为 unpriced；守恒条件是 `displayed_asset_count + unpriced_asset_count = asset_count`。零 catalog 是 provider 当前部署不可用，不是内部 SQL 错误；Binance 451、Bybit 403 保留原状态且不得经代理或其它 provider 绕过。Markets 搜索只有当前 query key 成功返回空集合后才显示 empty，慢请求显示 loading，`published_asset_count=0` 显示 deployment unavailable。计数、降级和复现入口见 [Market data quality](docs/market-data-quality.md)。

R2E 把公开 overview/dashboard 固定到同一个不可变行情快照：PostgreSQL 在一次 read-only `REPEATABLE READ` 事务中使用同一 `CURRENT_TIMESTAMP` 读取 summary、global metric 与完整资产行，Redis 再以 15 秒 bucket、5 分钟 TTL、最多 64 个完整值做跨 API 实例的单一 authority。响应必须携带 `snapshot_id`、`snapshot_as_of`、`qiu.market-snapshot.v1`，当前 All universe 还必须满足 `106 = fresh + stale + unavailable` 与 `priced = fresh + stale`；显式 snapshot dashboard 不进入 BFF cache，ticks 继续实时且不缓存。BFF 同时核对 backend/edge exact release SHA、`data_mode=live`、`restricted-no-bypass.v1`、contract/snapshot schema 与本次 nonce；wrong SHA、旧 deterministic replay、direct `18080`、损坏或过期 snapshot 都 fail closed，不能回退 stale cache。Mac live lane 固定经独立 `com.qiu-market.d1r1.frontdoor` 的 pure frontdoor `18084 -> 18080`，business stack 仍由 `com.qiu-market.d1r1.stack` 管理；selector、tunnel、Redis generation owner 与失败回滚流程见 [Vercel + Mac mini 上线手册](docs/qiu-market-vercel-mac.md#r2e-行情读取合同与原子切换)。

四家 CEX 实时 feed 都是 **WebSocket primary + REST reconcile**：Binance/Bybit/OKX 订阅 ticker stream，Coinbase 订阅 `ticker_batch`；高频事件只更新内存 latest map，每约 5 秒合并提交一次。REST 每 30 秒对账安静、漏消息或断线资产。每家由独立 supervisor 隔离失败。正式环境中，CEX 在 shadow/paused 时只探测审核资产，不发布快照；canary/enabled 才进入正式 writer。本地 `make dev` 默认开启 Local Preview，但使用 preview source，不改变正式 mode、Canary 清单或 readiness。所有行情经 `marketdata.SnapshotWriter` 先提交 PostgreSQL，再派生 Redis。writer 保留最后成功值：30 秒内 Fresh，30 秒到 5 分钟 Stale（可展示但不参与综合价和涨跌排名），超过 5 分钟 Unavailable。规范 ticker 分开保存 `open_24h` 与可空 `change_24h_pct`；Binance 协议同时声明小写 `o` 开盘价和大写 `O` 窗口开始时间，防止 Go JSON 大小写不敏感把时间戳覆盖价格。综合价每 5 秒只使用 30 秒内的新鲜 CEX Spot，要求 10 分钟内 USD-family 汇率、剔除 3% 中位数离群报价，并在三个以上 contributor 时限制单 venue 权重不超过 40%。Perp/DEX 只扩展 All 成员，永不贡献综合现货价。

K 线另有独立的 `provider_kline_selection`：四家各把当前 50 资产 selection version 固定到一个具体 USD-family Spot market，只采 provider 原生 1m，再在分钟严格连续时确定性汇总 15m/1h/1d。worker 只产缺口任务，crawler 必须回原 provider 修复；不能用另一家交易所填洞，也不能用 ticker 目录顺序静默换 K 线来源。

Q-M3 新增只读的 `marketdata/providercontract` 边界，为后续行情、衍生品和事件来源统一定义 spot ticker、OHLCV、derivatives 与 signals 四种 capability。契约把 canonical asset、venue、Spot/Perp market、十进制单位、schema version、source、`event_time`、`observed_at`、`received_at`、TTL、quality flags 和 fallback trace 绑定在同一个事实里；unsupported、auth、rate limit、timeout、upstream 5xx、bad payload 与 stale 都是 typed error。路由只在明确可重试错误或 capability 不支持时按稳定顺序切换，auth / bad payload / identity 或单位冲突会 fail closed。

Q-M4A 在该边界下新增默认关闭的 `marketdata/providercontract/binancepublic` 只读 adapter，只允许 Binance 官方 market-data-only 域的 `BTCUSDT` Spot ticker 与 1m OHLCV。离线 fixture、受限 HTTP client、bounded cache 和 opt-in online smoke 都不注册到现有 crawler/UI，不写 `SnapshotWriter`，也不进入交易、撮合或账本。字段映射、限流、安全和许可门见 [Binance public spot provider](docs/binance-public-provider.md)；在 owner 完成地域与再分发许可确认前不得开启公开展示。

Q-M4B 在同一只读边界下增加默认关闭的 CoinGlass 衍生品 adapter：只对官方明确单位的 Binance `BTCUSD_PERP` 4h OI 与清算历史建模；funding 相关端点未明确 ratio/percent 单位，因此在发网前返回 typed `unsupported`，不猜值。凭据只允许由服务端 secret provider 注入，adapter 不读 `.env`、不注册到 UI/写链/交易链，也不持久化原始响应。端点、套餐、许可、字段和 Q-M4C 激活门见 [CoinGlass derivatives provider](docs/coinglass-derivatives-provider.md)。

Q-M5A 增加默认关闭的 xiuqiu-site 动态 Market Radar 只读研究流。后端只允许官方 HTTPS origin 的 `summary`、`events` 与 `events/:id` 三个 GET，固定查询 `market=crypto&asset=BTC&window=168`，将事件转换为 `researchsignal/v1`，并在 `/insights` 独立显示来源、事件/发布/接收时间、观察与失效条件。研究优先级不是交易建议，所有事件固定 `executable=false`；该包没有数据库、Redis、行情快照、订单、撮合、余额或账本依赖。默认 `MARKET_RESEARCH_SIGNALS_ENABLED=false`，一键隔离验收为 `make verify-research-golden`。

Q-M6A 在这些只读事实之上增加独立的数据质量门：Binance Spot、CoinGlass derivatives fixture 与 xiuqiu research 各自拥有 evidence window、capability 最小样本、SLO、技术分、许可 eligibility 和 quarantine/recovery 状态，绝不跨类别求平均。比例始终携带整数分子/分母；空数据和样本不足不会得到 100%，cache hit 也不会冒充上游成功。许可未知或受限的数据即使技术分为 A 仍不可公开消费，所有来源的 `trade_eligible`、reference、matcher、orders、balances 和 ledger eligibility 固定为 false。只读状态由 `/api/v1/data-quality/summary` 和 Insights 的 Data Quality 面板解释；阈值、告警、恢复与证据保留规则见 [Market data quality](docs/market-data-quality.md)。

Q-M7A 把此前分开的交易、研究和质量 golden path 合成一个 production-like 隔离门：一次性 PostgreSQL 16.14、独立 TLS fixture、稳定 HTTP coordinator、两个不同 PID 的真实 trading backend 和 production Vue build 在动态 loopback 端口运行。浏览器先完成一笔完全成交，再完成部分成交、强制终止 backend A、从 snapshot 4 + event tail 恢复到 backend B、撤单与同 request ID 重放；同一故事还验证研究状态、六类 provider fault、cache/no-data 不推进恢复和连续三窗恢复。完整边界、固定账本数字与清理证据见 [Full-stack PostgreSQL golden](docs/full-stack-golden.md)。

### 可信行情底座与多交易所实施状态

- `implemented`：七家独立版本化 50 资产选择、All canonical 去重并集、本地预览与正式 rollout 隔离、四家 WebSocket/REST feed、四家版本化 50 market K 线、V2+V3 最多两跳 AMM、权威 DEX snapshot、最后成功值与 Fresh/Stale/Unavailable、手动 rollout 门和统一 venue 快照已落地。
- `build-verified`：2026-07-26 的 `go vet ./...`、`go test ./...`、交易 race/fuzz/benchmark、Vitest 8/8、Vue production build、Playwright 16/16、npm audit 0、`make verify-local`、shell syntax 与 `git diff --check` 通过；编译通过不等于外部来源已完成 canary。
- `integration-verified`：当前本地业务库已顺序执行到 `2026082100023.sql`。HTTP/gRPC/PostgreSQL/Redis、四家真实 CEX、Hyperliquid、公共只读 EVM RPC、V2 Router 与 V3 QuoterV2 已交换数据。七家各有 50 个 active selection，All 为 109 个 canonical 并集成员；四家 K 线各 reconcile 50 个 market。真实浏览器还完成虚拟入金、挂单、成交、费用、撤单、WebSocket 与交易重启恢复；动态数量只代表现场快照。
- `environment-pending`：DW 新连续对账窗 72 小时、Binance 当前阶段门、Coinbase → Bybit → OKX 各 24h canary/48h enabled、四家联合 72 小时、交易 HTTPS/OAuth/容量与最终七天；任何阶段都不会自动晋级。
- 旧 Doris 表、水位、`get_kline_analytics` 和旧组件都保留；`/analytics` 只做地址重定向。`exchange_symbol_kline`、`symbol_market_currey` 已解除运行时注册但未删除；删除仍需最终七天验收后单独批准。

### 虚拟现货交易实施状态

- `implemented`：`BTC-USDT` 定点数撮合、全部首版订单语义、双重记账、每市场串行 runner、PostgreSQL 事件/快照/outbox/投影、正式迁移已覆盖至 `2026082800030.sql`、loopback gRPC、共享 HTTP gateway、单用户鉴权、WebSocket cursor、可信参考 demo-maker 和共享 `/trade/BTC-USDT` 已落地。
- `implemented`（Trade Product V1）：订单/个人成交/账本/事件真值时间线已提供账户绑定的 cursor 分页；Trade 展示专业单市场终端，管理员虚拟入金迁至 System；submit、cancel、fund 三种写入都持久化原 request ID 并按权威事实核对。cursor 使用私有持久化 HMAC 轮换键，订单 lifecycle checkpoint 同时记录 sequence 和 row count，缺行、多行或孤儿行会 fail closed。
- `build-verified`：2026-08-05 的 `go build ./...`、`go vet ./...`、`go test ./...`、`go test -race ./trading/...`、真实临时 PostgreSQL 串行专项、前端 125 个 Vitest、production build、49 个 Playwright、production dependency audit 0、不可变候选 fixture 与 `git diff --check` 通过。
- `integration-verified`：一次性真实 PostgreSQL 上执行正式 migration，启动真实 gRPC + REST，完成虚拟入金、挂单、撤单、优雅快照、整套重启、session 延续、跨重启幂等，并确认 snapshot/event state hash 完全一致。
- `production-pending`：真实资金、充值提现、私钥、实盘下单不在目标内；生产 HTTPS/OAuth 回调、容量压测、备份恢复演练、监控告警和长期 soak 仍未验收。
- `implemented / build-verified / integration-verified (isolated local PostgreSQL + loopback gRPC) / activation-pending`：持久化 Recovery Coordinator、runner 权威写门禁、30 秒 TransportProbe、loopback gRPC status/promote、store 连续性粘性熔断和 demo-maker 受控恢复已落地，且随机隔离数据库中的 migration/CAS/fault 与真实 loopback gRPC 集成已通过。兼容开关默认关闭；Mac mini production PostgreSQL/epoch、实际外部 HTTPS、Production origin/deployment provenance、浏览器 cursor 和断电故障注入完成前不得在生产启用。
- `implemented / build-verified / activation-pending`：Mac mini 候选发布由 [交易系统文档](docs/trading-system.md#mac-mini-版本化发布) 中的 `manage-release-candidate.sh` 统一绑定同一精确 Git SHA 的 binary、完整 migration set 与 runtime bundle；默认命令不切换服务，激活和回滚必须显式 `--execute`。本轮只有 fixture 证明，尚未读取生产配置、恢复生产备份或切换 Mac mini。
- `implemented / build-verified / environment-pending`：正式 observer 只有在 runner/outbox ready、Recovery Coordinator 已到 `writable` 且 continuity 确定、recovery JSON 完整 provenance 与 Vercel headers/epoch 对齐、Mac mini binary/runtime commit 及两份产物 SHA-256 全部匹配时才累计分钟；observer sample 使用 schema v7、7 天 epoch 使用 schema v4，旧样本不会升级证据。GitHub CI 只提供 build/vet/unit/race、前端 unit/build 和 shell syntax，不能替代 PostgreSQL、OAuth、Vercel、Mac mini 或公网 soak。

### 价格精度

价格、成交量、市值不使用 float 存储。写入前统一放大 1e8 转为整数字符串，落库为 `numeric(65,18)` / `uint256`，API 输出时再按比例还原。跨模块传递的都是放大后的整数字符串，避免二进制浮点误差在聚合、比较和存储中累积。

### 统一响应信封与错误处理

现有 market-data HTTP 接口（包括读接口）统一为 POST + JSON，响应信封 `{ code, message, result }`：`code = 2000` 成功，`4000/5000` 为业务 / 内部错误且伴随正确的 HTTP 状态码。`/api/v1/trading/**` 是独立 REST/WebSocket 契约，使用标准 HTTP 方法和精确十进制字符串；两个边界在前端 API 层分开处理。

### 数据新鲜度模型

行情快照保存 `observed_at` 与可空 `source_time/source_time_kind`。接口返回 `provider_updated_at` 和 `freshness_status`；System 把“进程活着”和“上游数据源健康”分开，不能再用心跳冒充 Binance 可用。页面顶部只写 `Page refreshed`，表示页面何时请求成功，不代表数据源健康。K 线新鲜度按周期归一判定，进行中的蜡烛用虚线 + `LIVE` 价签与已闭合蜡烛区分；这里的 LIVE 也不代表 WebSocket。

V2 资产首页与 3 秒轻量 tick 还返回结构化的 `venue_price`、
`dex_route_price`、`display_price`。每个价格事实把 value/change、`source`、
`source_time`、`observed_at`、freshness、quality、contributors 和 version
绑在一起；调用方不再从一个裸数值猜来源。`display_price` 是 CEX
composite 或明确的 market reference 分栏，DEX route 只进入
`dex_route_price`。原平铺字段暂时保留为兼容层；当前 HTTP/TypeScript
契约已固化，Markets 的 3 秒 tick 已只消费匹配 venue 的价格事实。DEX 页把
Route 与 Reference 的价格、24h 和质量永久分栏；route 超过 60 秒后，兼容字段
也会清空链上价格、涨跌、成交额、来源和质量，reference 只能留在自身栏位。

### 前端工程

- **设计令牌**：色彩 / 圆角 / 字体 / 间距全部集中在 `src/style.css` 的 CSS 变量，组件不硬编码颜色；Apple 风格蓝白金融产品主题，数字统一 tabular-nums。
- **组件化**：DataTable（排序 / 搜索 / 分页）、StatusBadge、AssetLogo、StatCard、骨架屏、ErrorState、EmptyState 等共享组件，页面只组合不复制样式。
- **数据层**：`usePolling` 组合式函数统一慢速快照轮询（Markets 15s、System 15s，其余通常 30s），页面隐藏自动暂停，卸载自动清理；Markets 另有 3 秒轻量 tick，按 query generation、venue identity、version 与 observed time 拒绝旧响应。失败时只显示五分钟内并明确标记的同 venue last-good，不回退综合价。API 层对后端"数字序列化为字符串"的情况统一做类型兜底。
- **类型与构建**：全部页面 `<script setup lang="ts">`，TS strict + noUnusedLocals，`npm run build` 先过 vue-tsc 再构建；ECharts 按需引入且仅行情详情 / Insights 加载（独立 chunk）。

### 不使用 Mock 行情兜底

行情系统的核心是数据可信。如果 API、数据库或外部数据源异常，前端展示错误态（ErrorState + 重试），而不是展示 BTC/ETH 假数据：

- API 成功：展示真实行情，按延迟分级为 `Live` / `Delayed` / `Stale`。
- API 失败：展示错误态，页面状态为 `Offline`。
- 不返回 mock 行情数据，不使用假数据兜底。

## 前端概览

Qiu Market 前端在 2026-07 完成整体重设计，当前形态：

- **四项主导航 + 行情详情**：Markets、Trade、Insights、System。`/trade/BTC-USDT` 是虚拟交易终端；`/` 与 `/dashboard` 回到 Markets，旧 `/analytics` 到 Insights，旧 `/klines` 到 Markets。Assets / Exchanges / Symbols 作为 System 内的 Catalog 标签，旧 URL 重定向并保留 tab。
- **Markets 聚合首页**：七个 provider 各展示自己带版本号的 50 资产选择；All 展示七张选择按 canonical identity 去重后的并集并按市值排序。单 venue 与 All 都保持一项资产一行，短暂断线或 DEX 询价失败不会删行，而会保留成员并显示 Stale/Unavailable/Not covered。
- **按需市场/路线抽屉**：点击数量后才请求 CEX Spot、Hyperliquid Perp 和 DEX routes；Perp/DEX 明确排除综合价。只有 `has_kline=true` 的具体市场显示图表入口。
- **七源稳定选择**：四家 CEX 各冻结 50 个审核现货资产；Hyperliquid 冻结 50 个身份确认的 Perp 资产；Uniswap/PancakeSwap 各冻结 50 个链上身份已复核的 listed assets。是否入选和是否已有合格路线报价是两个状态；AMM 使用 V2/V3 直连或最多两跳 mixed route，按 `$10K → $1K → $100` 询价并显示实际金额和 protocol path，全部失败才显示 `Not covered`。
- **蓝白视觉系统**：页面、卡片、图表和抽屉统一为 `#f5f5f7 / #ffffff / #0071e3`，沿用 Apple 平台字体、清晰层级、44px 主要点击目标与 reduced-motion；涨跌仍保留独立红绿语义。
- **真实状态驱动**：System 同时展示 Redis 心跳形成的 process status 与 `market_provider_status` 形成的 source status。两者是独立事实。
- 详细设计与开发规范见 [docs/frontend.md](docs/frontend.md)。

## 从零启动（新设备验证指南）

日常启动、停止、八终端说明和故障处理以 [docs/local-development.md](docs/local-development.md) 为 canonical runbook；本节保留新设备从零准备依赖的完整步骤。

### 已配置开发机：一条命令启动

macOS 上已经准备好 `.env`、PostgreSQL、Redis 和 Docker 时，直接执行：

```bash
make dev
```

默认启动会为七个 provider 开启本地真实行情预览；七个 provider 分别读取自己的 50 资产 selection，All 读取七张选择的去重并集。进程层面会启动八个角色。正式验收时关闭预览：

```bash
S78_CEX_PREVIEW=0 make dev
```

启动器会先探测 `.env` 指向的真实 PostgreSQL/Redis、编译，在检测到待执行迁移时把私有备份写到 `~/Library/Application Support/S78 Market Services/backups`，幂等执行迁移并启动 Doris，然后启动 API / Trading / RPC / crawler / worker / dex / dw / frontend 八个终端角色。它不会启动 compose 的空 PostgreSQL/Redis，也不会杀死端口上的非托管进程。本机安装 iTerm2 时默认打开一个 iTerm2 窗口八个标签；否则降级到 Terminal.app 标签或独立窗口。

新来源由 `provider_rollout_state` 独立控制。全新空库中的 CEX 与 AMM 从 shadow 开始，Hyperliquid 保持初始化策略；已有部署的 rollout 行绝不被初始化迁移覆盖。当前业务库的 Binance 是已经审计并固化十资产清单的 canary。旧 `MARKET_MULTI_VENUE_ENABLED` 只保留兼容，不再决定正式切流。审核与 rollout 命令见 [docs/catalog-audit.md](docs/catalog-audit.md)。

查看只读晋级证据，不改变 rollout：

```bash
./market-services catalog rollout-status --provider binance --rank-limit 50 --json
```

```bash
make dev-status
make dev-logs
make dev-stop
S78_DEV_DRY_RUN=1 make dev
S78_SKIP_DORIS=1 make dev
```

PID 与日志放在 `/tmp/s78-market-services-$UID`；角色日志运行中达到 20 MB 会轮转并保留五份归档。5173 始终留给 `xiuqiu-site`，S78 固定 5174。

以下步骤在一台全新的机器上验证通过即可运行，全程只需要 Docker、Go 和 Node.js。

### 0. 环境准备

| 依赖 | 版本要求 | 用途 |
|---|---|---|
| Docker + Docker Compose | 任意近期版本 | 启动 PostgreSQL 16 和 Redis 7 |
| Go | ≥ 1.24 | 编译后端（`go version` 确认） |
| Node.js | ≥ 18（推荐 20/22/24） | 前端 Vite 开发 / 构建 |
| psql 客户端 | 与 PG 16 兼容即可 | `make seed` 写入演示数据（macOS: `brew install libpq`） |

> 如果机器上已手动安装 PostgreSQL 16 和 Redis 7，可以不装 Docker，跳过第 2 步，自行把 `.env` 指向已有实例。

### 1. 获取代码并配置环境变量

```bash
git clone <repo-url> s78-market-services
cd s78-market-services

# 复制环境变量模板；默认值与 docker-compose 完全匹配，本地开发无需修改
cp .env.example .env
```

`.env` 关键项说明：HTTP API 监听 `127.0.0.1:9092`，gRPC 在 `9091`，metrics 在 `9093`；主库指向 compose 里的 `postgres`（用户 `xiuqiu`、库 `s78_market`、trust 认证所以密码随意）；Redis 无密码。从库（`MARKET_SLAVE_DB_*`）本地留空即可。

### 2. 启动 PostgreSQL 和 Redis

```bash
make dev-deps        # = docker compose up -d postgres redis
docker compose ps    # 确认两个容器 healthy
```

这里的“本地 Docker”可以简单理解成：不用手动安装和配置 PostgreSQL / Redis，而是让 Docker 在你电脑上启动两个隔离的小服务容器。PostgreSQL 保存资产、市场、行情、K 线和修复任务；Redis 保存 crawler/dex 派生、API 读取的热点价格、排名和进程心跳。

### 3. 编译、迁移、写入演示数据

```bash
make migrate   # 编译二进制并执行 migrations/ 下的建表 SQL
make seed      # 通过 psql 写入 7 个资产 / 交易所 / 交易对演示数据
```

### 4. 启动后端进程

```bash
# 终端 1：HTTP API（前端的数据来源）
make api

# 终端 2：虚拟 BTC/USDT 撮合、账本、事件与 9094 gRPC
make trading

# 终端 3：crawler，目录审计、四家 Spot adapter、综合价与四家 CEX K 线
make crawler

# 终端 4：worker，只扫描 K 线缺口并生成 repair task
make worker

# 终端 5（可选）：dex，隔离运行 Hyperliquid、Uniswap/PancakeSwap V2+V3
make dex

# 终端 6（可选）：gRPC 行情服务，与 HTTP API 共用业务层、返回相同数据
make rpc
```

> crawler 需要能访问 Binance、Coinbase、Bybit、OKX、CoinGecko 和法币汇率 API。单 provider 不通时只降级该来源，前端显示 Stale / Unavailable，这是预期行为（项目刻意不做 mock 兜底）。
>
> Hyperliquid 使用公开 API。本地 `make dev` 默认让 Uniswap/PancakeSwap 使用限流的公开 RPC 与 DEX Screener 发现池，并在链上复核 V2/V3 Factory、token 和 V3 fee；每跳分别使用 V2 Router 或 V3 QuoterV2。正式环境仍建议私下配置 Ethereum/BSC RPC 与索引端点，并默认不启用公共回退。任何 AMM 失败都只降级对应 supervisor。Perp/DEX 只出现在独立 venue 与资产抽屉中，不参加综合现货价。

### 4.5 （可选）启动 Doris 数仓与分析链路

Historical Momentum 的固定窗口收益率、波动率、百分比区间和覆盖率由 Apache Doris 提供。Doris 完全可选：不启动时 Markets、Market Breadth 和 Cross-Venue Monitor 不受影响，只有 Insights 的历史模块与历史接口显示明确不可用。

```bash
# Linux 宿主机先执行一次（Doris BE 要求；colima 用 colima ssh -- sudo sysctl ...；Docker Desktop 一般已满足）
sudo sysctl -w vm.max_map_count=2000000

docker compose up -d doris                              # 启动 all-in-one Doris（首启约 1~2 分钟）
curl http://127.0.0.1:8030/api/health                   # 等 FE 就绪
# 宿主机 mysql 9.x 客户端连 Doris 会报 ERROR 2059（缺 mysql_native_password 插件），用容器内客户端：
docker exec -i s78-market-doris mysql -h127.0.0.1 -P9030 -uroot < script/doris-init.sql
make dw                                                 # 终端 6：PG -> Doris 同步，每 60s 一轮
```

之后 `make api` 重启一次（让 API 进程连上 Doris），前端 **Insights** 的 Historical Momentum 模块即可查询。macOS 无 Docker 时的运行与验证边界见 [docs/doris-analytics.md](docs/doris-analytics.md)。

### 5. 启动前端

```bash
cd frontend
npm install
npm run dev        # 固定 http://127.0.0.1:5174（5173 留给 xiuqiu-site）
```

Vite 已配置把 `/api` 代理到 `http://localhost:9092`，所以前端必须在 `npm run dev` 下访问（后端不托管静态文件、也未开 CORS）。也可以在项目根目录用 `make frontend-dev`。

### 6. 验证

```bash
# 后端健康检查
curl -X GET http://127.0.0.1:9092/healthz

# 系统总览（前端 System 页的数据源）
curl -X POST http://127.0.0.1:9092/api/v1/get_system_overview \
  -H 'Content-Type: application/json' \
  -d '{"consumer_token":"frontend-dashboard"}'

# 综合资产首页
curl -X POST http://127.0.0.1:9092/api/v2/get_asset_dashboard \
  -H 'Content-Type: application/json' \
  -d '{"consumer_token":"frontend-dashboard","page":1,"page_size":10,"filter":"assets","sort_by":"rank"}'

# 可选：如果启动了 make rpc，用 grpcurl 验证 gRPC 接口（数据与 HTTP 一致）
grpcurl -plaintext -d '{}' 127.0.0.1:9091 dapplink.xyz.MarketService/GetSystemOverview
grpcurl -plaintext -d '{"page":1,"page_size":3}' 127.0.0.1:9091 dapplink.xyz.MarketService/GetAssetDashboardV2
```

更多 gRPC 验证方式与 proto 重新生成步骤见 [docs/grpc-service.md](docs/grpc-service.md)。

浏览器打开 `http://127.0.0.1:5174`，预期看到：

- 默认进入 Markets，按 rank 展示七家 selection 的 canonical 去重并集；没有新鲜报价的资产仍保留并显示明确原因；
- `/trade/BTC-USDT` 展示可信参考、真实 venue K 线、虚拟订单簿和交易操作；缺失行情时明确 unavailable，不生成假图；
- All 只展示 CEX Spot 综合价，切换七个 venue 后仍保持一项资产一行，多市场和路线从右侧抽屉按需查看；
- System 的 Processes 与 Data sources 分开；crawler 运行不等于 Binance 必然 Healthy；
- 停掉 `make api` 后，前端各页面进入 Offline 错误态（可重试），这是设计行为。

也可以执行一键链路检查：

```bash
make verify-local
```

该检查会复用已经监听 9092/9094 的服务；缺少服务时才创建带精确 PID 和临时日志的短生命周期 API/Trading，并在退出时只回收自己启动的进程。除行情接口和七源 selection 外，它还检查交易 schema、公开状态、精确字符串 sequence 和订单簿数组契约。

### 端口一览

| 端口 | 进程 | 说明 |
|---|---|---|
| 5174 | S78 Vite dev server | 前端入口（固定 127.0.0.1，代理 /api → 9092；5173 由 xiuqiu-site 使用） |
| 9092 | market-services api | HTTP API |
| 9091 | market-services rpc | gRPC |
| 9094 | market-services trading | loopback-only TradingService gRPC |
| 9093 | market-services | metrics |
| 5432 | Docker postgres | PostgreSQL 16 |
| 6379 | Docker redis | Redis 7 |
| 8030 | Docker doris | Doris FE HTTP（Web UI / 健康检查，可选） |
| 9030 | Docker doris | Doris FE MySQL 协议（分析查询，可选） |
| 8040 | Docker doris | Doris BE HTTP（Stream Load 重定向落点，dw 必需，可选） |

### 常见问题

- **5432 / 6379 端口被占用**：机器上已有本地 PostgreSQL / Redis 在跑。停掉本地服务，或修改 `docker-compose.yml` 的端口映射并同步修改 `.env`。
- **`make seed` 报 psql 不存在**：安装 PostgreSQL 客户端（macOS `brew install libpq && brew link --force libpq`，Ubuntu `sudo apt install postgresql-client`）。
- **前端全部显示 Offline**：确认 `make api` 正在运行且监听 9092；确认是通过 `npm run dev` 的地址访问，而不是直接打开 `dist/index.html`。
- **页面数据一直 Stale**：crawler 无法访问外部数据源（网络 / 代理问题），检查后重启 `make crawler`。
- **生产构建**：`cd frontend && npm run build` 产出 `frontend/dist`。部署时需要任意静态服务器托管 dist，并把 `/api` 反向代理到后端 9092（后端自身不托管静态文件）。

## 构建与测试

开始跨模块改动或判断 worktree 是否可清理前，先运行只读仓库审计。它只比较本地已有的 `origin/main`，不会 fetch、切分支或删除文件：

```bash
make repo-audit
make security-paths-test
make security-paths
make security-env-templates-test
make security-env-templates
```

后端：

```bash
go test ./...
go build ./cmd/market-services
go test -race ./trading/...
./trading/scripts/verify-local.sh postgres
```

前端：

```bash
cd frontend
npm run test     # Vitest
npm run build    # vue-tsc 类型检查 + Vite 构建
npm run test:e2e # Playwright，默认使用隔离端口 4175
```

单笔 BTC 限价买单的隔离 golden path 可从仓库根目录一条命令复现：

```bash
make verify-trading-golden
```

该命令只启动 loopback 内存 harness 和本地 Vue/Vite，不读取项目 `.env`、不连接共享
PostgreSQL、真实交易所或真实资金。浏览器通过现有认证、CSRF 和
`/api/v1/trading/**` 接口提交 `60000 USDT × 0.01 BTC` 买单，再由隔离控制端触发
确定性对手单；测试核对 open → filled、冻结/释放、费用、余额、成交、账本和同一
`client_order_id` 重放。需要 Node.js 24、Go 1.24 和本机 Chrome；如 Go 不在
`PATH`，可用 `QIU_GOLDEN_HARNESS_COMMAND` 指向等价的 `go run
./trading/cmd/golden --bind 127.0.0.1:19092` 命令。

部分成交、标准 store 恢复和撤销余量的独立竖切使用：

```bash
make verify-trading-partial-golden
```

该命令在独立端口启动 Q-M2 harness，提交 `60000 USDT × 0.02 BTC` 买单，先成交
`0.01 BTC`，关闭旧 `MarketRunner` 并从同一 snapshot + event tail 创建新 runner，
最后由真实 Vue 取消余量并重放相同 cancel request。它同样不读取根 `.env`，不连接
共享数据库、外部行情或真实资金。

质量 read model 与 Insights 面板的隔离浏览器竖切使用：

```bash
make verify-quality-golden
```

该命令只启动 loopback `quality-golden`、真实 data-quality HTTP handler 和本地 Vue，
用确定性 evidence 同时展示 Binance healthy、CoinGlass restricted/not-live 与 xiuqiu
license-unknown quarantine。它不读取 `.env`、不访问 provider 网络或数据库，且浏览器
验收会核对三来源、六 capability、精确分母、许可/恢复原因、移动端布局以及零 trading
mutation。真实 online sampling 另有显式 build tag/flag 双门，普通 CI 不发网。

把上述交易、真实 PostgreSQL 恢复、研究与质量门合并成一个 production-like 浏览器故事：

```bash
make verify-full-stack-golden
```

命令不读取项目 `.env`，不连接共享数据库、真实 provider 或真实资金。它只接受 PostgreSQL
16.14：先看显式 `QIU_TEST_POSTGRES_BIN_DIR`，再看 `PATH`，最后复用工作区中唯一已验证的
本地缓存；找不到就 fail closed，不下载或安装系统软件。脚本动态分配端口和临时目录，构建
race harness 与 production Vue，运行两个真实 Chrome Playwright 测试和独立 Go QA
（普通 + race），随后用有界 TERM/KILL 清理所有子进程并验证 PID、端口和临时目录均已
消失。固定数据流、许可假设、故障注入与 PASS 数字见
[docs/full-stack-golden.md](docs/full-stack-golden.md)。

## 文档索引

每个工程专题都按“功能是什么 → 设计决策 → 数据流 → 关键代码位置 → 验证步骤 → 边界 → 大白话术语 → Owner 60 秒口述 → 闭卷自检”组织。推荐阅读顺序：

```text
README 全局架构
  -> 对应专题文档
  -> 关键代码入口
  -> 验证命令
  -> 60 秒口述
  -> 闭卷自检
```

读完专题不等于真正掌握；必须能脱离文档画出数据流、指出三至五个关键入口，并区分代码实现、编译验证、真实联调和生产化边界。

| 文档 | 内容 |
|---|---|
| [docs/local-development.md](docs/local-development.md) | 日常一键启动、八终端角色、停止、日志与常见故障 |
| [docs/sensitive-files.md](docs/sensitive-files.md) | dotenv、私钥、wallet/TSS 与数据库状态的本地边界、CI 路径门和事故响应 |
| [docs/frontend.md](docs/frontend.md) | 资产首页与虚拟交易页、三类价格事实、DEX 双栏、六类行情竞态回归和响应式验收 |
| [docs/trading-system.md](docs/trading-system.md) | BTC/USDT 撮合、账本、submitted/unknown、fill/cancel 竞态、cursor reconcile、崩溃恢复、鉴权和验收边界 |
| [docs/prd-qm-trade-001.md](docs/prd-qm-trade-001.md) | Trade Product V1 用户主流程、页面范围、P0/P1、非目标、验收与并行所有权 |
| [docs/contracts/qm-trade-v1-api.md](docs/contracts/qm-trade-v1-api.md) | Trade Product V1 cursor、订单时间线、账本、账户摘要和 Cancel All 冻结 API Schema |
| [docs/qm-trade-v1-goal-context.md](docs/qm-trade-v1-goal-context.md) | Trade Product V1 的持续目标、冻结范围、并行所有权、证据状态和终止条件 |
| [docs/klines-pipeline.md](docs/klines-pipeline.md) | K 线 market identity、显式时间、业务唯一键、分周期续传与刷新 |
| [docs/redis-top-movers.md](docs/redis-top-movers.md) | Redis ZSET 涨跌榜、TTL 抖动防雪崩、SQL 回退 |
| [docs/catalog-audit.md](docs/catalog-audit.md) | provider 审核清单、版本化资产选择、候选市场、rollout、安全 CLI 与源码可重建边界 |
| [docs/dex-hyperliquid.md](docs/dex-hyperliquid.md) | Hyperliquid Perp、Uniswap/Pancake V2+V3 mixed route、链上校验与综合价排除 |
| [docs/grpc-service.md](docs/grpc-service.md) | gRPC MarketService、与 HTTP 共用业务层、proto 重新生成 |
| [docs/doris-analytics.md](docs/doris-analytics.md) | Doris 旧流 + v2 影子流、固定窗口历史动量、Mac mini 不可变 DW 运行与故障隔离 |
| [docs/market-service-architecture.md](docs/market-service-architecture.md) | 七源独立 selection、三类价格事实、DEX 60 秒 route 边界、All canonical 并集、CEX-only 综合现货价与 rollout |
| [docs/market-data-quality.md](docs/market-data-quality.md) | provider/research 独立评分、许可门、overview/dashboard 守恒计数、搜索三态、quarantine/recovery 与综合价排除 |
| [docs/full-stack-golden.md](docs/full-stack-golden.md) | Q-M7A 真实 PostgreSQL、双 backend 恢复、Vue/研究/质量完整故事、一键运行与清理证据 |
| [docs/binance-public-provider.md](docs/binance-public-provider.md) | Q-M4A 默认关闭的 Binance BTC/USDT Spot ticker/OHLCV adapter、HTTP 边界与许可门 |
| [docs/coinglass-derivatives-provider.md](docs/coinglass-derivatives-provider.md) | Q-M4B 默认关闭的 CoinGlass BTCUSD_PERP OI/清算 adapter、secret/套餐/单位与许可门 |
| [docs/market-service-interview.md](docs/market-service-interview.md) | 围绕当前项目的面试讲解与追问扩展 |
| [docs/project-go-interview-bagua.md](docs/project-go-interview-bagua.md) | Go 工程知识与当前项目代码映射 |

## 工程边界与后续优化方向

- 完成 Binance → Coinbase → Bybit → OKX 串行 canary、Uniswap/PancakeSwap 私有端点环境验证、四家 K 线缺口率验收、七源共同 72 小时和最终七天验收。
- 综合价稳定后新建 `asset_index_kline`；不使用某一家 venue K 线冒充综合历史。
- 行情延迟指标与告警体系。
- 更完整的 Redis 缓存策略（持久化、雪崩防护已做 TTL 抖动，其余待补）。
- 数据质量校验待补价格突变与成交量异常；持续缺口扫描和 repair task 已实现，但连续 48/72 小时验收仍未完成。
- 公共行情 API 的鉴权、配额和监控仍待补；虚拟交易写接口已有单用户 session、CSRF、Origin 和限流。
- provider 状态已落表；后续补监控指标、告警和长期 SLA 统计。
- Doris 链路可继续加分区 / 分桶调优、Routine Load、物化视图。
- 前端已做 ECharts 按需引入 + 路由级懒加载，可继续做更细粒度拆分与 CDN 化。
