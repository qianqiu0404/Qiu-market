# Doris 数仓与 Insights 历史动量

本文档是 PostgreSQL → `dw` → Doris 同步和 `/insights` 历史动量的 canonical 文档。Doris 是行情旁路分析库，不替代 PostgreSQL，也不参与实时 Markets、Market Breadth 或 Cross-Venue Monitor。

## 当前可观察结果

- `dw` 每 60 秒把 PG K 线同步到旧公开表，并并行维护 Slice 1 的 v2 影子表。
- `POST /api/v1/get_asset_momentum` 从 Doris 计算 24H / 7D / 30D 历史动量。
- 查询固定使用已闭合的 1h K 线，窗口结束为 UTC 整点。
- 响应包含窗口收益率、1h 收益率标准差、High-Low 百分比区间、蜡烛数和覆盖率。
- 覆盖率低于 90% 标记 Low Coverage；前端将其排除出散点图和排名。
- 旧 `get_kline_analytics` 暂时保留兼容，但已限制明确起止时间并排除未闭合蜡烛；旧 `/analytics` 页面地址重定向到 `/insights`。

## 与多交易所综合价的边界

`asset_price_index` 是 PostgreSQL 当前状态，每 5 秒重算，不进入现有 Doris venue K 线表。四家 CEX 已各自把当前版本化 50 资产 selection 映射到具体 USD-family Spot market：采 provider 原生 1m，再在分钟连续完整时确定性汇总 15m/1h/1d，统一写 `symbol_kline`。这仍是 **venue history**，不是综合资产历史。

综合资产历史必须以后新建 `asset_index_kline`：从综合价生成 1m，再确定性汇总 15m/1h/1d。被拒绝的是把 Coinbase/Bybit/OKX 任一家 K 线或 CoinGecko 历史价当作综合指数历史。形成 168 根闭合综合 1h 蜡烛前，CMC 首页 7D Chart 显示 `—`。

因此多交易所上线不改变 Slice 1 的 72 小时安全门，也不能因为 v2 首页已经可构建就宣称 Doris 新历史链路已完成。

## 设计决策

### 分开实时洞察与历史计算

实时宽度和跨 venue 监控使用 PostgreSQL + Redis；历史动量使用 Doris。被拒绝的替代方案是让整个 Insights 页面依赖 Doris：这样数仓故障会把实时行情一起拖成不可用。当前设计的成本是前端需要维护两个独立加载、错误和重试状态，但故障边界真实且可解释。

### 固定窗口，不做全历史泄漏

窗口定义：

| 选择 | 期望闭合 1h 蜡烛 |
|---|---:|
| 24H | 24 |
| 7D | 168 |
| 30D | 720 |

查询使用 `[window_start, window_end)`，`window_end` 为当前 UTC 整点，因此当前未闭合蜡烛不参与。旧 Analytics 的“全历史基础币成交量排行”和裸 High/Low 被拒绝：不同基础币成交量单位不可比，裸价格区间也容易被误读为当前价。High-Low 现在只以窗口百分比区间呈现。

### 旧表与 v2 影子表

```sql
dwd_symbol_kline           DUPLICATE KEY(symbol_guid, interval, open_time)
dwd_market_kline_v2        UNIQUE KEY(market_id, interval, open_time)
dws_symbol_market_snapshot DUPLICATE KEY(symbol_guid, captured_at)
dws_market_snapshot_v2     UNIQUE KEY(market_id, captured_at)
```

真实业务库 Slice 1 的 72 小时安全门通过前，历史动量仍读 `dwd_symbol_kline`。旧表可能包含同一 symbol/open_time 的修正行，所以查询先按业务时间分组再聚合；这是兼容成本，不宣称等价于 v2 的最终内容语义。安全门通过后只替换历史查询的数据源和身份列，HTTP 响应契约与前端保持不变。

不得为了新页面绕过迁移冲突阻断，也不得提前读取尚未验收的影子表。

### Stream Load 与同步隔离

选择 Stream Load，是因为项目没有 Kafka，Routine Load 会引入不必要的基础设施；逐行 JDBC 写入又不适合分钟级批量同步。`dw` 独立于 worker，避免 Doris 延迟拖慢实时行情。

Stream Load 的关键协议行为：

1. FE 可能 307 重定向到 BE，客户端手工跟随并保留认证头。
2. 请求使用 `Expect: 100-continue`，避免 body 先发给 FE。
3. label 由流、最小/最大 seq 与 payload hash 生成，重复提交可识别。
4. FE/BE 请求禁用连接复用；未进入 Doris 事务、返回裸 HTML 的瞬态 400 用同 label 最多重试三次。可解析的 Doris `Fail` 400 不重试，避免掩盖 schema/数据错误。

### v2 水位与提交序竞争

PG `sync_seq` 只在 K 线真实物化内容变化时取新号。每轮：

1. 记录 sequence ceiling。
2. 从 `max(0, last_sync_seq - 1000)` 回看。
3. 仅处理不大于 ceiling 的已提交行。
4. 成功后水位推进为旧水位与本批最大 seq 的较大值。
5. Doris 以 `(market_id, interval, open_time)` UNIQUE KEY 幂等覆盖。
6. 每日 key/content 对账只比较已成功水位；若某业务键在 PG 已有 `sync_seq > watermark` 的新版本，Doris 水位内旧版本属于“待同步”，不算多余 key。
7. 缺 key/内容差异按精确业务键重放；真实多余 key 阻止切换。记录最近尝试和最近成功时间，失败按 5 分钟到 1 小时有界退避，成功后隔 24 小时再查。

只用 `sync_seq > last` 会漏掉低序号晚提交事务；用 `open_time` 当水位会漏迟到回填；原地改旧表会失去低成本回滚。影子双写增加存储和对账成本，但把切换变成可观察、可回退的过程。

## 端到端数据流

```text
Binance / Coinbase / Bybit / OKX native 1m
    -> provider_kline_selection
    -> crawler deterministic 15m/1h/1d
    -> PostgreSQL symbol_kline
    -> dw（旧时间水位 + v2 sync_seq 回看）
    -> Doris Stream Load
       ├── dwd_symbol_kline（当前历史读模型）
       └── dwd_market_kline_v2（未切换影子）
    -> get_asset_momentum
       ├── 闭合 1h 固定窗口
       ├── 收益率 / 波动率 / 百分比区间
       └── coverage / low_coverage
    -> Insights Historical Momentum
```

实时模块是另一条链：

```text
PostgreSQL + Redis
    -> get_market_insights
    -> Market Breadth + Cross-Venue Monitor
```

两条链路不能画成一条串行请求。

## 关键代码入口

1. `dw/dw.go`：旧流与 v2 影子流的同步周期。
2. `dw/streamload.go`：Stream Load、307、100-continue 和成功判定。
3. `dw/reconcile.go`：v2 每日 key/content 对账与缺 key 重放。
4. `services/http/service/analytics.go`：固定窗口、闭合蜡烛、覆盖率和 Doris 查询。
5. `frontend/src/views/Insights.vue`：历史模块独立加载、窗口切换、Low Coverage 排除。

相关定义在 `script/doris-init.sql`、`database/dw_sync.go`、`services/http/model/analytics.go`。

## 术语

| 术语 | 准确定义 | 大白话 | 项目位置 |
|---|---|---|---|
| OLTP | 高频单行事务读写 | 日常收银 | PostgreSQL |
| OLAP | 大范围扫描聚合 | 月底经营分析 | Doris |
| Stream Load | Doris HTTP 批量导入协议 | 整车卸货 | `dw/streamload.go` |
| Watermark | 已成功同步的位置 | 书签 | `dw_sync_state` |
| sync_seq | 内容真实变化时取得的 PG 序号 | 变更流水号 | v2 增量扫描 |
| Lookback | 从水位前重复扫描一段 | 书签往前翻防漏 | 固定 1000 |
| Coverage | 实际闭合蜡烛 / 期望蜡烛 | 数据完整度 | momentum 响应 |
| Closed candle | 时间区间已经结束的蜡烛 | 已封账的一小时 | `open_time < UTC hour` |

## 失败、降级、重试与恢复

| 故障 | 当前行为 | 恢复 |
|---|---|---|
| Stream Load 失败 | 不推进水位，下轮重试 | Doris 恢复后自动继续 |
| FE/BE 裸 HTML 400 | 同幂等 label 有界重试，日志只含状态、label 与摘要 | 三次仍失败则本轮不推进 |
| 低序号晚提交 | 1000 回看重新发现 | 下一周期自动补入 |
| Doris 缺 key | 每日对账按精确业务 key 重放 | 自动修复 |
| Doris 内容冲突/多余 key | 输出差异并阻止切换 | 人工确认，不自动删除 |
| Doris 不可达 | `get_asset_momentum` 返回 503 | Insights 实时模块仍工作，历史模块可重试 |
| 覆盖率不足 | 返回真实数值并标 Low Coverage | 前端不纳入散点/排名 |
| 精度转换失败 | 本批失败且水位不推进 | 修复数据或转换后重试 |

固定 1000 是有界保护，不是无限事务等待的数学保证；每日对账是第二层恢复线。

## Mac mini 不可变运行与恢复

Mac mini 把 `dw` 作为与 `api`、`crawler` 同级的长期 launchd 角色管理，而不是
从源码目录手工运行。发布顺序是：

```text
manage-runtime-release.sh
  -> 归档 migrations 与 ops 脚本为带 commit 的只读运行包
  -> manage-services.sh 生成 com.qiumarket.dw.plist
  -> run-role.sh 从私有 database.env / production.env 启动同一受管二进制
  -> launchd KeepAlive 在进程异常退出后重启
```

运行包激活只有在包括 `dw` 在内的全部受管 label 已加载、且 plist 和 launchd
都指向同一个不可变目录时才成功。激活中途失败会移除候选 DW plist 并恢复激活前
的完整 plist 备份，不留下只有一部分角色切换成功的状态。这里拒绝的替代方案是
重新加载指向源码工作区的旧 DW plist：它绕过 commit 绑定，后续发布还会再次将其
移除，无法证明实际运行的是哪一版脚本。

恢复时先确认 Doris 健康，再检查 `dw_sync_state` 的最新更新时间和 v2
`last_sync_seq`。进程存活只说明搬运工还在，水位持续前进才说明数据仍在更新。

## 验证

```bash
docker compose up -d doris
docker exec -i s78-market-doris mysql -h127.0.0.1 -P9030 -uroot < script/doris-init.sql
make dw

curl -X POST http://127.0.0.1:9092/api/v1/get_asset_momentum \
  -H 'Content-Type: application/json' \
  -d '{"consumer_token":"frontend-dashboard","window":"7d"}'
```

检查：

- `window_end` 是 UTC 整点，当前蜡烛不在结果里。
- 24H / 7D / 30D 的 `expected_candles` 分别是 24 / 168 / 720。
- `coverage_pct = candle_count / expected_candles * 100`。
- Doris 停止后只有历史请求 503，`get_market_insights` 仍成功。
- Mac mini 的 `com.qiumarket.dw` 已加载，ProgramArguments 指向
  `runtime-releases/<commit>`，而不是源码工作区。
- `dw_sync_state` 在一个以上 60 秒周期内继续更新，且 v2 最大水位逐步接近
  PostgreSQL 当前 `sync_seq` 上界。

## 证据边界

- `implemented`：固定窗口动量接口、独立错误边界、旧查询时间约束、四家版本化 venue K 线与 v2 影子同步；综合资产 K 线仅有边界设计，尚未实现。
- `build-verified`：以本次交付记录的完整 Go/前端门禁为准。
- `integration-verified`：2026-07-24 当前开发环境真实 PG/Redis/Doris 已交换数据；修复后一个同步周期内 24 条 v2 K 线流、旧/v2 快照和全部逐流 key/content 对账通过。该证据不是连续 72 小时 soak。
- `environment-pending`：真实业务数据连续 72 小时 Slice 1 soak 与新页面连续 48 小时观察。
- `production-recommendation`：安全门通过后切换到 `dwd_market_kline_v2`；本次未执行。

## Owner 60 秒解释

> Doris 是实时行情旁边的历史分析库。四家 CEX venue K 线都进入同一套 PG/DW 模型，但不能冒充综合资产历史。DW 用真实变化才推进的 sync_seq，固定回看 1000，再用 market_id、interval、open_time 的 UNIQUE KEY 覆盖。Stream Load 用确定性 label 幂等提交；每日对账只比较成功水位，缺 key 精确重放。Insights 历史动量仍等 72 小时安全门后才切 v2。

## 闭卷自检

1. 为什么实时 Insights 不应依赖 Doris？
2. 为什么必须排除当前未闭合 1h 蜡烛？
3. 为什么旧表查询要限定明确起止时间？
4. 回看窗口、UNIQUE KEY、每日对账分别解决什么问题？
5. 为什么当前不能直接切到 v2 影子表？
6. Low Coverage 为什么必须从散点和排名排除？
