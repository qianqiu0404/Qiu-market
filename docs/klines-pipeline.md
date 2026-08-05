# K 线管线：四家 CEX 版本化选集、原生 1m 与确定性汇总

## 这个功能是什么

K 线管线保存 Binance、Coinbase、Bybit、OKX 各自版本化 50 资产选择对应的一个 USD-family Spot 市场。四家都从 provider 原生接口读取 1m，写入后只在 1m 连续完整时确定性汇总 15m / 1h / 1d。crawler 每 30 秒刷新并续传最近 24 小时；worker 检测闭合窗口缺口并生成持久化 repair task，crawler 按 provider 领取后回拉。即使有自动修复，也必须通过任务状态和对账说明完整性，不能口头保证“永不缺口”。

K 线产品范围不再由 ticker 临时结果决定。`provider_kline_selection` 把“该 provider 的 selection version 选择哪一个具体 market”冻结下来，`exchange_symbol.kline_enabled` 只是这个版本化结果在 market 上的运行标记。一次行情失败、市值排名抖动或目录顺序变化都不能静默换 K 线市场。

## 曾经的问题与根因

旧实现有三个叠加的问题：

1. **只抓最近 20 根 1m**：crawler 每 5 秒只拉 `interval=1m&limit=20`，没有历史回填——进程没运行的时段永久缺失。
2. **大周期靠 1m 实时聚合**：15m/1h/1d 是 API 从 1m 记录现场聚合的，而查询又是"全表取最新 N 条再按 symbol 内存过滤"，长周期覆盖的时间窗口只有几分钟 → 图表又稀又断。
3. **前端 category 轴**：把缺失区间直接挤压跳过，看不出真实的时间空洞。
4. **ticker 与 K 线目录混用**：Top 50 Binance canary 首次启用 19 个 ticker 市场后，worker 曾把这 19 个市场全部当成 K 线市场，生成 repair task 并回填了 13 个范围外市场。`2026080800010.sql` 用显式 `kline_enabled` 修复边界，并把停机后的 K 线、任务和 DW 水位精确清理数量写入 `kline_scope_cleanup_audit`；迁移前由启动器生成私有 PG 备份。已经 Stream Load 的影子/旧 Doris 行按同一组精确 market/symbol identity 做一次性删除并现场复核。

## 设计决策

### 为什么四家统一采原生 1m，再确定性汇总

- 四家对大周期的历史边界、分页方向和闭合语义并不完全一致；统一使用各家最细的 1m 作为 provider 真值，再由同一段代码汇总，能让 15m/1h/1d 口径一致。
- 汇总不是前端临时拼接：它仍落入 `symbol_kline`，并使用 `(market_id, interval, open_time)` 唯一键、真实变化触发 `sync_seq` 和 Doris 幂等覆盖。
- 只有 bucket 内每一分钟都存在且时间严格连续才写大周期；缺一根就不生成该 bucket，避免“看起来完整”的假 K 线。

### 为什么当前回看 24 小时，而不是假装已经有长期历史

- 本阶段先证明四家选集、provider 适配和汇总正确。每轮从最新 1m 向前重叠两分钟，首次最多回看 24 小时，重复数据由业务唯一键幂等覆盖。
- Coinbase 等来源可能返回稀疏分钟；系统如实少写，并由 worker 生成缺口任务，不插值、不拿另一家交易所替代。
- 更长历史只能在请求预算、缺口率和 DW 对账稳定后扩大；当前文档不能把“能采 24 小时”写成“拥有长期历史”。

### 为什么 provider 要隔离并限速

四家各有独立 supervisor，30 秒一轮；同一 provider 的 50 个市场之间固定间隔 120ms，分页之间也复用该间隔。单家 429、超时或 payload 异常只让该 provider 的 K 线状态降级，不停止实时 ticker、其他 CEX 或 DEX。

### Slice 1 取舍：采用什么、拒绝什么

- 拒绝继续让 `created_at` 同时代表开盘时间和入库时间：一个字段回答两个问题，迟到回填、延迟统计和 DW 水位都会互相污染；代价是新增列和一次严格回填审计。
- 拒绝只靠 guid 后缀做业务唯一：guid 保留兼容，但数据库约束必须直接表达 `(market_id, interval, open_time)`，否则不同 venue 或 guid 格式演进会再次制造孪生行。
- 拒绝存冗余 `close_time`：当前周期连续且固定，统一推导能避免两列不一致；代价是查询时做一次极小的 duration 加法。非连续交易日历不套用这个决定。
- 拒绝用“市场已启用”或“已有一行 K 线”推断 K 线资格：ticker、K 线是两个独立产品范围；代价是每个 provider selection version 都要显式解析到一个具体 USD-family Spot，并写入 `provider_kline_selection`。

### 前端如何表达"实时"

K 线的"实时"和行情快照不同：一根进行中的蜡烛要到周期结束才定型，刷新节奏也必须跟着周期走，否则 1d 周期每 10 秒刷一次只是浪费请求，1m 周期 30 秒刷一次又会漏掉 2 根新蜡烛。

- **从 Markets 进入详情**：侧栏不再暴露孤立 Klines 页面；只有 `has_kline=true` 的具体 CEX market 可进入 `/markets/:marketId`。四个周期都按 60s 刷新，页面隐藏时暂停；当前不做综合资产 K 线或分时线。
- **右侧价格语义**：主价格轴、最新收盘参考线和 `LIVE` 彩色价签都在图表右侧，`LIVE` 只表示最后一根蜡烛仍在形成，不代表 WebSocket 推送。
- **进行中蜡烛标识**：最后一根若 `openTime + interval > now`，说明是未定型蜡烛（crawler 每轮 upsert 同一业务键覆盖它），用虚线描边 + `LIVE` 前缀的最新价线标出，避免把未定型收盘价当成定盘价。
- **freshness 按周期归一**：`utils/format.ts` 的 `klineFreshness` 用"最新一根开盘时间滞后几个周期"判定 Live（≤2 个周期）/ Delayed（≤3）/ Stale，不能套用行情快照的 15s/60s 扁平阈值——1d 周期开盘后滞后 23 小时是正常的。

- `exchange_symbol.guid` 是内部不透明 `market_id`；`exchange_symbol.market_code` 是全库唯一、可读的审计编码，例如 `binance:BTC/USDT:spot`。
- K 线业务唯一键是 `(market_id, interval, open_time)`；guid 继续保留作兼容主键，但不再承担业务时间和市场身份解析。
- `open_time` 是上游蜡烛开盘时间；`ingested_at` 是首次入 PG 时间；`updated_at` 只在 OHLCV 等真实内容变化时更新；`sync_seq` 同样只在真实内容变化时取新号，专供 Doris 增量同步。
- `close_time` 不落库。当前四个连续周期由 `common/markettime.CloseTime` 统一用 `open_time + interval` 推导；未来非连续交易日历必须单独设计。
- 历史事实：crawler 曾显式写 `CreatedAt: time.UnixMilli(openTime)`。即使 Go 模型带 GORM `autoCreateTime`，非零显式值也不会被覆盖，所以旧 `created_at` 是业务开盘时间，不是落库时间。迁移从 guid 的 13 位毫秒后缀回填 `open_time`，并与按 `Asia/Shanghai` 解释的旧列逐行对账；存量 `ingested_at` 只能以旧 `updated_at/created_at` 近似，不能伪称精确真值。
- 价格/成交量沿用全局 1e8 放大整数字符串约定，API 输出时还原。
- 2026-07 迁移（`migrations/2026072100002.sql`）为 `symbol_kline` 增加 `interval` 列，存量 5605 行默认归为 `1m`。
- 2026-07-22 清理了 24 行"新旧 guid 格式指向同一根蜡烛"的重复行：迁移前旧格式 guid 为 `<symbol_guid>-<openTimeMs>`（无 interval 段），迁移后新格式行与少量旧格式行构成孪生。脚本 `script/cleanup-kline-duplicates.sql` 先把待删行备份到 `symbol_kline_dedup_backup_20260722` 再删除；没有新格式孪生的旧格式行是独家历史数据，未动。

## 数据流

```text
Binance /api/v3/klines?symbol=..&interval=..&startTime=..&limit=1000
        |
        v
crawler（启动时 backfillAllKlines 回填 + 运行中 refreshKlines 错峰刷新）
        |
        v
symbol_kline（UNIQUE market_id + interval + open_time）
        ^
        | worker 只扫描当前 provider_kline_selection -> kline_repair_task
        | crawler 按 provider FOR UPDATE SKIP LOCKED -> 原 provider 回拉 -> 逐根复核
        |
        v
POST /api/v1/get_klines {symbol_guid, interval, limit}
  -> QueryMarketKlines 以 market_id 直查，升序返回，不聚合
        |
        v
Markets 行情详情（time 连续时间轴，真实空洞如实显示）
```

## 关键代码位置

Owner 讲 Slice 1 时只需按这五个入口走，不要背仓库树：

1. `migrations/2026081900021.sql` + `database/provider_catalog.go`：冻结四家 K 线 market selection，并把版本映射到 `kline_enabled`。
2. `crawler/cex_kline_adapters.go`：四家原生 1m 请求、分页方向和字段布局归一。
3. `crawler/cex_kline_supervisor.go`：30 秒调度、1m 幂等写入、连续 bucket 汇总与按 provider repair。
4. `database/symbol_kline.go`：无变化不发 UPDATE，数据库触发器再兜底。
5. `worker/worker.go` + `dw/dw.go`：前者只产 repair task；后者固定 ceiling、回看、单调水位与水位内对账。

| 文件 | 函数 / 结构 | 作用 |
|---|---|---|
| `crawler/cex_kline_adapters.go` | 四个 `Fetch1m` | Binance/Coinbase/Bybit/OKX 原生 1m 请求与字段归一 |
| `crawler/cex_kline_supervisor.go` | `syncProvider` / `syncMarket` | 四家隔离的 30 秒周期、24 小时冷启动和重叠续传 |
| `crawler/cex_kline_supervisor.go` | `aggregateAffectedBuckets` | 只有连续完整的 1m bucket 才确定性落 15m/1h/1d |
| `worker/worker.go` | `scanKlineGaps` / `FindKlineGaps` | 按配置目录扫描闭合窗口，合并连续缺口 |
| `database/provider_catalog.go` | `QueryProviderKlineMarkets` | 只返回显式 `kline_enabled=true` 市场；ticker rollout 不会隐式进入 K 线 |
| `database/kline_repair.go` | claim / retry / complete | 确定性 task key、`SKIP LOCKED`、有界重试与 stale lock 回收 |
| `crawler/cex_kline_supervisor.go` | `processRepairTasks` / `repairRangeComplete` | 按任务 provider 回原交易所；缺任一预期 open_time 都不能 completed |
| `migrations/2026081900021.sql` | 全文 | 版本化 provider/asset/market/source symbol 选择与唯一约束 |
| `migrations/2026080200004.sql` | 全文 | market identity 回填审计、显式时间列、业务唯一键、sync_seq 触发器 |
| `common/marketidentity/code.go` | `GenerateMarketCode` | 生成并校验全库可读 market_code |
| `common/markettime/interval.go` | `CloseTime` | 统一推导当前四个周期的 close time |
| `database/symbol_kline.go` | `QueryLatestSymbolKline` | 按显式 open_time 查某 symbol+interval 最新一根，决定回填起点 |
| `database/symbol_kline.go` | `QueryMarketKlines` / `QueryMarketSparklines` | 按 market_id+interval 取详情蜡烛；窗口函数批量取当前页每市场最近 N 个点 |
| `services/http/service/klines.go` | `GetKlines` / `GetMarketSparklines` | market_id 优先、legacy symbol_guid 唯一映射兼容；升序并还原精度 |
| `frontend/src/views/Klines.vue` | `scheduleAutoRefresh` / `buildOption` | Markets 详情图；右侧价格轴、LIVE 价签、MA(7)/MA(25)，60s 静默刷新 |
| `frontend/src/utils/format.ts` | `klineFreshness` / `isInProgressCandle` / `KLINE_INTERVAL_MS` | K 线新鲜度按周期归一判定 + 进行中蜡烛判定 |

## 本地验证

```bash
# 重启 crawler，观察四家选择与周期日志
make crawler
# 预期日志：CEX K-line cycle complete provider=... markets=50

# 查看各周期数据量
psql ... -c "SELECT interval, count(*) FROM symbol_kline GROUP BY interval;"

# 业务键与 sync_seq 假变化检查
psql ... -c "SELECT market_id, interval, open_time, count(*) FROM symbol_kline GROUP BY 1,2,3 HAVING count(*) > 1;"
psql ... -c "SELECT stream_name,last_sync_seq FROM dw_sync_state WHERE stream_name LIKE 'kline-v2:%';"

# 接口验证：15m 应返回连续序列，相邻 timestamp 间隔恒为 900000ms
curl -X POST http://127.0.0.1:9092/api/v1/get_klines \
  -H 'Content-Type: application/json' \
  -d '{"consumer_token":"frontend-dashboard","market_id":"s1","interval":"15m","limit":100}'
```

## Owner 先讲这 60 秒

> 四家 CEX 各自把当前 50 资产 selection 冻结到一个具体 USD-family Spot market。crawler 只向原 provider 拉 1m，再在分钟连续时确定性生成 15m/1h/1d；market_id、interval、open_time 唯一定位一根蜡烛。open_time 是业务时间，ingested_at 才是进入 PG，sync_seq 只在真实内容变化时推进。worker 不访问交易所，只写 repair task；crawler 按 provider 领取并逐根核对。页面 LIVE 只表示当前 bucket 尚未闭合，不代表综合价或 WebSocket。

## 大白话术语表

| 术语 | 准确含义 | 大白话 | 在本项目中 |
|---|---|---|---|
| K 线 | 一个固定时间窗口内价格和成交量的摘要 | 把一段价格过程压成一根蜡烛 | 按 1m、15m、1h、1d 查询 |
| OHLCV | 开盘、最高、最低、收盘、成交量 | 一段时间的起点、顶点、底点、终点和活跃度 | `symbol_kline` 的核心字段 |
| interval | 每根 K 线覆盖的时间长度 | 每张照片间隔多久拍一次 | 是查询和唯一定位的一部分 |
| 原生 1m | 直接保存每家交易所返回的分钟 K 线 | 先保留原始分钟底片 | 四家 adapter 的 `Fetch1m` |
| 确定性汇总 | 只有分钟连续完整才生成大周期 | 底片少一张就不合成长片 | `aggregateAffectedBuckets` |
| 回填 | 补齐启动前或历史缺失的数据 | 把以前漏记的账补回来 | crawler 启动时按 symbol + interval 拉历史 |
| repair task | 持久化的确定性缺口修复任务 | 把漏账写成可领取的工单 | `kline_repair_task` |
| 增量刷新 | 从已有最新位置继续抓新数据 | 从书签处继续读 | 运行期更新未完成和新产生的蜡烛 |
| 错峰 | 把不同任务分散到不同时间执行 | 让送货车不要一起堵门 | 各周期刷新不在同一秒集中请求 Binance |
| market_id | 不带业务含义的稳定内部市场主键 | 机器使用的档案号 | `exchange_symbol.guid`，K 线业务键的一部分 |
| market_code | 全局唯一且人可读的市场编码 | 档案封面上的清晰标签 | `binance:BTC/USDT:spot` |
| open_time | 一根蜡烛的业务开盘时刻 | 这张照片代表几点 | `symbol_kline.open_time` |
| ingested_at | 数据第一次进入 PG 的时间 | 照片几点送到仓库 | 新写入由数据库记录；存量只能近似回填 |
| sync_seq | 真实内容变化时分配的序号 | 仓库变更流水号 | Doris v2 增量水位，不代表提交顺序 |
| provider K 线 selection | provider 某 selection version 对应的具体 Spot market | 每家菜单里的币固定去哪一个柜台取历史 | `provider_kline_selection` |
| kline_enabled | 当前激活 K 线 selection 投影到 market 的运行标记 | 这个柜台当前是否领到历史相册资格 | `exchange_symbol.kline_enabled` |

## 从入口到结果的代码调用链

```text
crawler 启动
  -> 四家各自 reconcile provider_kline_selection
  -> 按 market 请求 provider 原生 1m
  -> 精度转换与时间归一
  -> database/symbol_kline.go upsert
  -> 连续完整时汇总 15m/1h/1d

运行中 ticker
  -> 各 interval 错峰刷新
  -> POST /api/v1/get_klines
  -> frontend/src/views/Klines.vue
```

讲代码时先定位调度、数据库 upsert 和查询三段；不要把 ECharts 绘图逻辑误说成 K 线生成逻辑。

## 失败、降级与恢复边界

| 故障 | 当前行为 | Owner 应怎样解释 |
|---|---|---|
| crawler 重启或历史不足 | 启动回填 + worker 周期扫描 | 两层都不靠前端插值 |
| 外部 API 临时失败 | 当前轮失败，后续刷新重试 | 不能用前端插值伪造真实蜡烛 |
| 某周期缺数据 | 该 interval 返回少量或空数据 | 不用 1m 临时拼接掩盖采集缺口 |
| Hyperliquid / AMM | 当前无 K 线 | 这是未接入范围，页面应不显示 venue chart 入口 |
| 重复抓取同一根且内容相同 | 触发器保留原 sync_seq/updated_at | 不制造假增量 |
| 同一根当前蜡烛 OHLCV 修正 | 原业务键覆盖并取得新 sync_seq | Doris v2 用 UNIQUE KEY 覆盖旧内容 |
| repair fetch 返回空/不全 | 逐根校验失败，按上限退避重试；第 8 次进入 failed | 不能以 HTTP 200 冒充修复完成 |
| provider selection 更新 | 先生成新 selection version，再原子 reconcile market 标记 | 不能靠目录顺序偷偷换历史来源 |
| market_code 冲突或 symbol 多 venue | 迁移审计报错并停止收紧约束 | 不猜 market_id，也不自动改名 |

## 闭卷自检

1. 为什么四家统一保存原生 1m，而不是各自直接保存不同大周期？
2. 回填、增量刷新和持续缺口检测有什么区别？
3. 为什么刷新要错峰？
4. 一根 K 线用哪些字段唯一定位？为什么不再用 guid/created_at 推断？
5. 为什么 DEX 交易对空 K 线不是前端 bug？
6. open_time、ingested_at 和 sync_seq 分别回答什么问题？
7. LIVE 价签表示什么，为什么它不等于 WebSocket 实时推送？
8. 为什么不能用“市场已启用”或“已经存在一行 K 线”推断 K 线资格？
9. `provider_kline_selection` 与 `exchange_symbol.kline_enabled` 分别回答什么？
10. 为什么 Coinbase 分钟稀疏时必须少写，而不能从 Binance 补进来？

## 边界与后续方向

- 回填依赖对应 CEX 可达；单家网络不通时记录该 provider 错误并保持既有数据，不拿另一家市场替代。
- DEX（Hyperliquid/AMM）目前没有 K 线；未来必须做各自 venue 历史设计，不能复用 CEX K 线冒充。
- 缺口扫描与持久化回补已实现；永久失败任务保留最后错误，不自动伪装成功。
- K 线已同步进 Doris 数仓（见 [doris-analytics.md](doris-analytics.md)），分析查询不占用 PG。
- `implemented`：四家版本化 K 线 market selection、四个原生 1m adapter、确定性 15m/1h/1d 汇总、provider repair 和 Slice 1 sync_seq/Doris v2 影子链路已落地。
- `build-verified`：以本次交付记录的 Go build/vet/test 和前端门禁结果为准；编译不等于四家长期历史完整。
- `integration-verified`：2026-07-25 本地业务库中四家各 reconcile 50 个市场并完成真实 K 线周期；Coinbase 稀疏分钟按真实响应保留，没有插值。
- `integration-verified`：2026-07-22 使用隔离 PostgreSQL 16 容器与临时 Doris 数据库验证了迁移、无变化不取号、T1/T2 提交乱序回看、Stream Load、key/content 对账和缺 key 自动重放。
- `environment-pending`：四家连续缺口率、repair 结果、PG/Doris 72 小时 soak 和最终七天尚未完成。
