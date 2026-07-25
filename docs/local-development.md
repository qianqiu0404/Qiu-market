# S78 本地启动与停止手册

这份文档是 S78 Market Services 的本地运行 canonical runbook。日常开发优先使用一条命令启动；启动器会自动创建七个服务终端，不要求你逐个启动。

## 功能与最短答案

进入项目目录：

```bash
cd /Users/xiuqiu/WorkSpace/s78-market-services
```

先看当前状态：

```bash
make dev-status
```

- 如果七个角色都是 `running`，不要重复启动，直接打开 <http://127.0.0.1:5174/markets>。
- 如果全部是 `stopped` 或 `stale-pid`，执行：

```bash
make dev
```

`make dev` 会自动启动七个终端角色。本机安装 iTerm2 时，默认使用一个 iTerm2 窗口七个标签；没有 iTerm2 时才降级到 Terminal.app 标签或独立窗口：

| 标签 | 角色 | 做什么 |
|---|---|---|
| API | `api` | HTTP API，前端通过 9092 读取行情 |
| RPC | `rpc` | gRPC 行情服务，监听 9091 |
| Crawler | `crawler` | Top 200 候选池、四家独立 50 资产选择、WebSocket/REST Spot、综合价、法币与四家版本化 K 线 |
| Worker | `worker` | 扫描 K 线缺口并生成 repair task |
| DEX | `dex` | 隔离运行 Hyperliquid Perp、Uniswap/PancakeSwap V2+V3 supervisor |
| DW | `dw` | PostgreSQL → Doris 增量同步 |
| Frontend | `frontend` | Vite 前端，固定监听 5174 |

5173 保留给 `xiuqiu-site`，S78 不会占用或停止它。

`make dev` 默认还会幂等开启七源 **Local Preview**。四家 CEX 立即读取真实 ticker，并分别读取自己当前激活的 50 资产选择；All 按 canonical `asset_id` 展示四张 CEX 选择的去重并集。Hyperliquid、Uniswap、PancakeSwap 读取自己的稳定 50 资产选择；一行表示 listed/identity-confirmed 成员，当前 Perp/路线不合格时行仍保留并显示 `Not covered`、`Stale` 或 `Unavailable`，不能用一次请求成败改变 selection。这只服务本地产品开发，不代表正式 rollout 已通过。

正式验收必须关闭预览：

```bash
S78_CEX_PREVIEW=0 S78_DEX_PREVIEW=0 make dev
```

关闭时会撤回预览发布范围、把预览 venue snapshot 置为 unavailable、删除 `spot-tickers-preview` 状态，并重新开始正式观察窗口。正式 mode 和 Canary 清单从未被预览修改。

## Provider 独立 rollout 安全门

新来源不再由一个全局布尔值一起切流。`provider_rollout_state` 为每个 provider 独立保存：

- `shadow`：发现目录并约每 30 秒探测已审核 ticker；只记录覆盖证据，不发布正式报价；
- `canary`：只允许显式 canary 资产；未指定时取 rank 最靠前的十项；
- `enabled`：允许 rank limit 内通过身份门的资产；
- `paused`：停止正式发布，保留最后一次可信目录和快照供审计。

全新空库把 CEX/AMM 放在 shadow，已有部署的 rollout 行不会被初始化迁移覆盖；当前业务库 Binance 已通过迁移审计固化为十资产 canary。目录仍会刷新；shadow 只是不进入正式首页报价。

安全 CLI：

```bash
source .env
./market-services catalog audit --provider coinbase --rank-limit 50
./market-services catalog apply-mappings --file catalog/provider-asset-mappings.yaml --dry-run
./market-services catalog select-assets --provider coinbase --limit 50 --dry-run
./market-services catalog select-assets --provider coinbase --limit 50 \
  --reason owner-approved-selection-refresh
./market-services catalog rollout-status --provider coinbase --rank-limit 50 --json
./market-services catalog rollout --provider coinbase --mode canary --rank-limit 50 --soak-hours 24
```

`apply-mappings --dry-run` 只校验仓库内的审核清单，不连接数据库。`select-assets --dry-run` 只展示当前 Top 200 中身份审核通过、可交易的候选；显式执行时才原子创建新 selection version。目录刷新不会因为 CoinGecko 排名轻微变化静默换人；只有目标数量变化、现有成员失效或 owner 主动刷新才产生新版本。`rollout-status` 是只读证据命令；它和真正的 rollout 共用判定器。改变 rollout 前，CLI 会检查上一 provider、最短观察时间、当前窗口至少 100 次尝试、成功率（CEX/Perp 99%，AMM 98%）、连续失败和逐资产新鲜快照；Binance canary 还要求 `dw_acceptance_state` 连续 72 小时对账成功。每次切换都会清零该 provider 的观察计数；`--soak-hours` 只能延长，不能缩短 canary 24h / enabled 48h。任何状态都不会自动晋级。

`MARKET_MULTI_VENUE_ENABLED` 暂时保留一版仅为旧部署兼容，当前正式启用决策不再读取它。

本地预览命令被两层保护：必须显式设置 `S78_LOCAL_PREVIEW=1`，且 PostgreSQL host 必须是 `localhost`、`127.0.0.1` 或 `::1`。它没有 HTTP 写入口：

```bash
source .env
S78_LOCAL_PREVIEW=1 ./market-services catalog preview \
  --provider binance,coinbase,bybit,okx,hyperliquid,uniswap,pancakeswap \
  --enable
```

正式 DEX 连接只通过环境变量注入；仓库文档和日志只允许出现变量名：

```text
MARKET_ETHEREUM_RPC_URL
MARKET_BSC_RPC_URL
MARKET_UNISWAP_V3_SUBGRAPH_URL
MARKET_PANCAKE_V3_SUBGRAPH_URL
```

配置完成后先做不会发交易、不会签名的端点检查：

```bash
source .env
./market-services catalog endpoint-check --provider uniswap
./market-services catalog endpoint-check --provider pancakeswap
```

输出只包含 provider、chain ID、区块高度/年龄、Subgraph 高度和样本池数量，不包含 endpoint 或 key。

本地 `dev-role.sh dex` 在没有私有 endpoint 时默认设置
`MARKET_DEX_PUBLIC_FALLBACK=1`：Ethereum/BSC 使用限流的 PublicNode 只读 RPC，
DEX Screener 只负责候选池发现，代码仍通过链上 `token0/token1/factory`
验证 V2/V3 pool，V3 额外核验 fee；每一跳再交给 V2 Router 或 V3 QuoterV2 做
`$10K → $1K → $100` 双向询价。该模式明确标为 Local Preview，
不积累正式 rollout 证据。需要复现正式边界时执行：

```bash
S78_DEX_PUBLIC_FALLBACK=0 S78_DEX_PREVIEW=0 make dev
```

也可直接验证公共回退，而不打印 endpoint：

```bash
source .env
MARKET_DEX_PUBLIC_FALLBACK=1 ./market-services catalog endpoint-check --provider uniswap
MARKET_DEX_PUBLIC_FALLBACK=1 ./market-services catalog endpoint-check --provider pancakeswap
```

## 第一次运行前

需要提前准备：

- 项目根目录已有 `.env`；
- `.env` 指向的 PostgreSQL 和 Redis 正在运行；
- Go、Node.js、npm、PostgreSQL 客户端、Redis 客户端已安装；
- 使用 DW 时 Docker/Colima 与 Doris 可用；
- 正式启用 Uniswap/PancakeSwap shadow 或 canary 前，建议私下配置 Ethereum/BSC RPC 与能覆盖所需 V2/V3 池的索引端点；公共回退只用于本地产品预览；
- 推荐安装 iTerm2；它能通过原生 AppleScript 接口创建一个窗口和多个标签，不依赖 System Events 模拟快捷键。
- 只有强制使用 Terminal.app 标签模式时，macOS 才需要允许发起命令的应用控制 Terminal。

辅助功能权限不是启动服务的硬前提。默认优先使用 iTerm2；iTerm2 不可用时，Terminal.app 没有权限就自动使用独立窗口。

可先检查启动器将做什么，不启动任何服务：

```bash
S78_DEV_DRY_RUN=1 make dev
```

可以明确指定布局：

```bash
S78_DEV_TERMINAL_MODE=iterm make dev    # iTerm2 一个窗口七个标签
S78_DEV_TERMINAL_MODE=tabs make dev     # Terminal.app 一个窗口七个标签，需要辅助功能权限
S78_DEV_TERMINAL_MODE=windows make dev  # Terminal.app 七个独立窗口，不需要辅助功能权限
```

## `make dev` 控制流程

启动顺序固定如下：

```text
检查工具、权限和端口
        ↓
探测 .env 指向的 PostgreSQL / Redis
        ↓
编译一次 Go 二进制
        ↓
必要时备份 PostgreSQL，再幂等执行迁移
        ↓
按 S78_CEX_PREVIEW 开启或关闭四家 CEX 本地预览
        ↓
启动并初始化 Doris
        ↓
优先打开一个 iTerm2 窗口和七个标签
        ↓
浏览器访问 127.0.0.1:5174/markets
```

几个重要边界：

- 启动器只连接 `.env` 指向的 PostgreSQL/Redis，不会擅自切到 Compose 空库。
- 发现 9091、9092 或 5174 被非托管进程占用时会拒绝启动并列出 PID，不会自动杀进程。
- 待执行迁移存在时，备份写到用户应用数据目录，不放进 Git 仓库。
- Doris 未就绪时会停止启动流程，不会假装历史分析可用。
- DEX endpoint 未配置或不可用时，只把对应 DEX 标为 Unavailable；Hyperliquid、四家 CEX 与首页其他模块继续运行。
- 这是本地开发启动器，不是生产进程管理器。

## 日常管理命令

查看七个角色：

```bash
make dev-status
```

查看所有角色日志：

```bash
make dev-logs
```

修改某个角色后可以只重启它，启动器只停止 PID 文件中记录的目标进程：

```bash
make dev-restart ROLE=crawler
make dev-restart ROLE=dex
make dev-restart ROLE=frontend
```

单角色重启会重新编译所需端（前端除外），但不会重复执行迁移或启动 Doris。完整结构性迁移仍使用 `make dev`。

`make dev-logs` 中按 `Ctrl+C` 只会退出日志跟随，不会停止服务。

停止全部 S78 角色：

```bash
make dev-stop
```

停止逻辑只读取 `/tmp/s78-market-services-$UID` 中记录的精确 PID，不使用宽泛 `pkill`，也不会触碰 `xiuqiu-site`。

日志和 PID 位于：

```text
/tmp/s78-market-services-$UID
```

单角色日志由 `script/dev-log-writer.sh` 在进程持续输出期间检查；达到 20 MB 后立即重开活动文件，最多保留五份归档，不再只在下一次启动时轮转。可用 `S78_LOG_MAX_BYTES` 为本地测试调整阈值。

## 不启动 Doris

只验收实时 Markets、Cross-Venue 和系统状态时，可以跳过 Doris：

```bash
S78_SKIP_DORIS=1 make dev
```

这时不会打开 DW 标签。Markets 和实时 Insights 仍可用；Historical Momentum 会明确显示不可用，不会返回 mock 数据。

## 手动七终端兜底

只有 Terminal 标签自动化权限暂时无法开启时，才使用这一节。

先在项目根目录编译一次：

```bash
go build -o market-services ./cmd/market-services
```

第一次手动启动还需要执行迁移；如果要运行 DW，也要先准备 Doris：

```bash
source .env
./market-services migrate
docker compose up -d doris
docker exec -i s78-market-doris mysql -h127.0.0.1 -P9030 -uroot < script/doris-init.sql
```

迁移器使用 `s78_schema_migrations` 保存文件名、SHA-256 和首次应用时间。已经登记的迁移不会在服务重启或再次执行 `make migrate` 时重放；已应用文件的内容若被修改，迁移会拒绝继续。对于首次引入迁移台账的既有数据库，第一次运行会顺序执行并登记现有 SQL，因此必须先按启动器提示备份；此后才具备稳定的幂等行为。这样可以避免初始化迁移意外重置 canary 观察窗口。

然后分别打开七个终端，每个终端都先进入项目目录，再运行一个角色：

```bash
cd /Users/xiuqiu/WorkSpace/s78-market-services
bash script/dev-role.sh api
```

```bash
cd /Users/xiuqiu/WorkSpace/s78-market-services
bash script/dev-role.sh rpc
```

```bash
cd /Users/xiuqiu/WorkSpace/s78-market-services
bash script/dev-role.sh crawler
```

```bash
cd /Users/xiuqiu/WorkSpace/s78-market-services
bash script/dev-role.sh worker
```

```bash
cd /Users/xiuqiu/WorkSpace/s78-market-services
bash script/dev-role.sh dex
```

```bash
cd /Users/xiuqiu/WorkSpace/s78-market-services
bash script/dev-role.sh dw
```

```bash
cd /Users/xiuqiu/WorkSpace/s78-market-services
bash script/dev-role.sh frontend
```

使用 `dev-role.sh` 而不是直接运行裸二进制，是因为它会统一记录 PID、命名标签、写入角色日志并执行日志轮转。

## 启动后的验证

```bash
make dev-status
curl http://127.0.0.1:9092/healthz
```

预期：

- `api/rpc/crawler/worker/dex/dw/frontend` 全部为 `running`；
- healthz 返回成功；
- <http://127.0.0.1:5174/markets> 可以打开；
- System 页面分别显示进程状态和数据源状态；
- Binance/Coinbase/Bybit/OKX/CoinGecko/Hyperliquid/Uniswap/PancakeSwap 的 provider 状态根据真实请求显示 Healthy、Stale 或 Unavailable；
- System 同时显示各 provider 的 `shadow/canary/enabled/paused`、rank limit 和观察截止时间；
- Binance/Coinbase/Bybit/OKX 各读取自己的激活 selection version，目标均为 50；All 展示四张选择的 canonical 去重并集，因此行数可以大于 50，但同一 BTC 只能出现一次。
- 四家 CEX 的 ticker 状态显示 `WebSocket primary + REST reconcile`；四家 K 线 capability 各自显示当前 selection version 与 `50/50` market 映射，不拿某家 K 线失败覆盖该家的 ticker 健康。
- 没有可信报价仍保留成员并显示明确原因；最后成功值在 30 秒后降为 Stale（最多保留 5 分钟且退出综合价/排名），超过 5 分钟变为 Unavailable。

也可以运行完整本地链路检查：

```bash
make verify-local
```

验证器先检查 PostgreSQL、Redis 和后端构建。9092 已有健康 API 时直接复用；只有端口尚未提供服务时才启动临时 API，并用自身记录的 PID 在退出时精确回收。随后它验证旧接口 smoke test、四家各自 50 个唯一 canonical asset 以及 All selection 并集的去重约束。这样验收不会误启第二个 API，也不会用宽泛进程匹配影响其他项目。

## 常见故障与恢复

### 为什么没有打开到 iTerm2

确认 `/Applications/iTerm.app` 已安装。可以强制指定并得到明确错误：

```bash
make dev-stop
S78_DEV_TERMINAL_MODE=iterm make dev
```

### 为什么 Terminal.app 没有打开成一个窗口七个标签

Terminal.app 的 AppleScript 接口可以无权限创建窗口，但创建标签需要 System Events 辅助功能权限。没有 iTerm2 时，默认 `make dev` 会自动使用七个 Terminal.app 窗口，不再失败。

需要强制 Terminal.app 标签模式时，先开启权限，再执行：

```bash
make dev-stop
S78_DEV_TERMINAL_MODE=tabs make dev
```

### 提示角色已经运行

不要重复启动。先检查：

```bash
make dev-status
```

确实需要全部重启时：

```bash
make dev-stop
make dev
```

### 提示端口被 unmanaged process 占用

启动器会输出占用进程的 PID。先确认它属于哪个项目；不能确认时不要杀。5173 的 `xiuqiu-site` 不构成冲突，S78 使用的是 5174。

### 前端打开但行情 Offline

依次检查：

```bash
make dev-status
curl http://127.0.0.1:9092/healthz
```

API 停止时前端进入 Offline 是预期行为。crawler 存活但上游不可用时，System 应显示 `Running + Source Stale/Unavailable`，不能用进程心跳冒充行情健康。

### DW 或 Historical Momentum 不可用

先看：

```bash
docker compose ps
make dev-logs
```

Doris 故障只应影响 DW 和历史动量，不应拖垮实时 Markets。恢复 Doris 后重启 DW；是否允许切换 v2 仍以 72 小时对账安全门为准。

## 运行术语

| 术语 | 准确含义 | 大白话 | 当前项目位置 |
|---|---|---|---|
| role | 一个以前台方式运行、可独立停止的服务职责 | 七个标签中的一个工位 | `script/dev-role.sh` |
| managed PID | 启动器精确记录且只由 `dev-stop` 管理的进程号 | 有登记的本项目进程 | `/tmp/s78-market-services-$UID` |
| source status | 上游目录、ticker、route 等独立成功/失败事实 | 人活着不代表电话线路通 | System source matrix |
| rollout window | 一次状态切换后重新累计的尝试、成功和 soak 证据 | 换挡后重新计时验车 | `market_provider_status` |
| provider selection | 某家交易所独立、带版本号的 50 资产产品集合 | 四家各有自己的 50 道菜单 | `provider_asset_selection_state` |
| provider K-line selection | provider selection version 对应的具体 USD-family Spot market | 每道菜固定从哪个柜台读取历史 | `provider_kline_selection` |
| canonical union | 四家 selection 按 `asset_id` 去重后的 All 集合 | 合并四张菜单，同一道菜只留一份 | `/markets?venue=all` |
| last-success freshness | 按最后一次成功采集而非最后一次尝试计算的新鲜度 | 电话打不通也保留上次听清的报价，并标过期 | `asset_venue_snapshot.last_success_at` |

## 关键代码入口

按实际执行顺序阅读：

1. `Makefile`：`dev/dev-status/dev-logs/dev-stop` 的公开命令入口。
2. `script/dev.sh`：依赖检查、数据库探测、迁移备份、Doris 初始化和 Terminal 标签编排。
3. `script/dev-role.sh`：单角色 PID、日志、标签名与进程启动。
4. `cmd/market-services/cli.go`：`api/rpc/crawler/worker/dex/dw` 的 Go 角色接线。
5. `frontend/vite.config.ts`：前端固定 5174，以及 `/api` 到 9092 的代理。

## 设计决策

选择“每个角色一个前台终端”，因为开发时需要分别观察和中断 API、采集、修复与数仓。默认用 iTerm2 原生接口把它们收进一个窗口的七个标签；iTerm2 不可用时才使用 Terminal.app。被拒绝的替代方案是把所有角色塞进一个后台命令：它看起来更短，但日志混在一起、无法单独停止，也更容易误杀其他项目。

iTerm2 AppleScript 本身不需要 System Events 辅助功能权限；Terminal.app 标签布局才需要。因此保留 Terminal 独立窗口降级、dry-run 和手动七终端兜底。PID 文件只是本机开发管理状态，不是分布式锁，也不是生产级 supervisor。

启动器提交 iTerm2/Terminal 命令后会等待每个 `dev-role.sh` 写入 PID 且进程真实存活，最长 10 秒；因此紧接着执行 `make dev-status` 不会再因为 AppleScript 与 PID 文件的竞态把刚启动的角色误报为 stopped。任何角色未进入 running 都会让启动命令失败并指向 `make dev-logs`，不会静默宣称全部启动成功。

产品集合与进程编排相互独立：启动器只保证角色和本地预览运行，`provider_asset_selection` 才回答每家页面有哪些资产。被拒绝的是让四家共用 CoinGecko rank 1–50；那会让未上市资产占满横杠，也会遮蔽交易所各自真实覆盖。代价是 All 行数不再固定为 50，但 canonical identity 能保证不重复 BTC，并且每次成员变化都有 version 可审计。

## Owner 60 秒解释

> 平时执行 `make dev` 会启动七个标签并为七源打开 Local Preview。四家 CEX 页面各读取自己的 50 资产选择；ticker 以 WebSocket 为主、REST 对账，K 线再把同一 selection version 固定到具体 market，采 1m 并汇总大周期。All 合并七张稳定选择并按 asset_id 去重，所以可能超过 50 行但 BTC 只出现一次。Uniswap/Pancake 展示链上核验的 V2/V3 最多两跳 route quote；不能报价的成员仍显示 `Not covered`。预览不改变正式 rollout。5173 留给 xiuqiu-site，S78 固定 5174。

## 闭卷自检

1. 日常启动为什么只需要 `make dev`，七个终端分别对应什么？
2. 为什么启动器不能自动杀死占用 9091、9092 或 5174 的进程？
3. `make dev-stop` 如何避免误停 `xiuqiu-site`？
4. 跳过 Doris 后哪些页面仍能工作，哪个模块会降级？
5. 为什么进程 Running 不能证明任一 provider 数据源 Healthy？
6. 为什么 `provider_rollout_state` 比全局 `MARKET_MULTI_VENUE_ENABLED` 更适合串行发布？
7. 为什么 Local Preview 能写真实行情，却绝不能让 `rollout_ready=true`？
8. 关闭 Local Preview 时为什么必须撤回快照并重置正式观察窗口？
9. 为什么四家可以选择不同的 50 个资产，而 All 仍不会出现两个 BTC？
10. 为什么一次 ticker 失败不应该立刻删除最后成功值？
11. Uniswap 或 BSC RPC 未配置时，为什么不能让整个 dex 进程退出？
12. 公共 DEX 发现为什么仍要用链上 Factory/token/fee 做二次核验？
13. 为什么 DEX selection 固定 50 行仍不代表必须有 50 条当前报价？
14. 为什么 CEX ticker 使用 WebSocket 主链路仍然保留 REST reconcile？
15. 为什么 provider K 线 selection 不能直接等同于 provider 资产 selection？
