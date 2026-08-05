# Top Movers：Redis ZSET 24h 涨跌幅榜

本文档讲清楚"涨幅榜 / 跌幅榜"这个功能的设计与实现，读完可以独立向别人解释每一行关键代码为什么在那里。改榜单相关代码前先读这里。

## 这个功能是什么、解决什么问题

`get_top_movers` 和 Markets 的涨跌排序需要共享的实时 24h 百分比。如果每次都在数据库排序，会把热榜请求压到 OLTP；只在进程内排序又无法让 crawler/dex 与 API 共享结果。当前由唯一 `SnapshotWriter` 在 PostgreSQL 接受行情后派生写 Redis ZSET，API 直接取序或批量补分数。Dashboard 已退出主导航，但兼容接口仍保留给调用方。

## 设计决策

### 为什么用 ZSET（有序集合）

- **写入 O(log N)**：`ZADD market:rank:change24h <score> <symbol_guid>`，同 member 直接覆盖分数，不需要"先查再删再插"，crawler 的 5 秒热循环里只多一条命令。
- **天然有序**：ZSET 内部按 score 排序维护，`ZREVRANGE key 0 4`（涨幅榜）和 `ZRANGE key 0 4`（跌幅榜）是 O(log N + M) 的范围读，**读榜零排序成本**——排序在写入时已经摊销掉了。
- **跨进程共享**：crawler 写、API 读，ZSET 是现成的共享结构，不需要引入消息队列或共享内存。

### 为什么 score 用涨跌幅百分比而不是绝对涨跌额

BTC 涨 1% 是约 1000 美元，DOGE 涨 1% 不到 0.001 美元。用绝对涨跌额排序，榜单永远被高价币霸榜，失去"涨得猛"的语义。Binance ticker 直接返回 `priceChangePercent` 字段（代码里 `binanceTicker.PriceChangePercent`），百分比让不同价位的币可比，这才是"Top Movers"的直觉含义。ZSET score 是 float64，百分比浮点直接当 score 写入即可，无需缩放。

### 为什么热点价格 key 要加 TTL 抖动

所有热点价格 key（`e1%Binance%s1%BTC/USDT` 等）由同一个 5 秒定时循环写入，TTL 原本固定 10 分钟。假设服务重启或某轮写入集中发生，这批 key 会在未来**同一秒集中过期**，下一瞬间所有读请求全部击穿到数据库——这就是缓存雪崩。改动是在 10 分钟基础上叠加 **0~60 秒随机抖动**（`jitteredHotTTL()`，`math/rand/v2`），过期时刻被打散到一分钟的窗口里，单点过期变成均匀过期。代价是每个 key 的 TTL 不再整齐，但对业务无感。

### 为什么 API 侧要 SQL 回退

ZSET 是易失的：Redis flush、重启或 crawler 尚未写完第一轮时可能为空。此时 API 按可空的规范字段 `symbol_market.change_24h_pct` 回退排序，缺值直接排除；不再读取旧 `radio` 猜口径。响应 `source=redis/sql` 暴露路径。两边都没有数据时返回空列表，不制造 `0%`。

## 数据流

```text
Binance /api/v3/ticker/24hr
        |  每 5s 拉取（crawler 进程）
        v
Binance crawler / Hyperliquid dex
        |
        v
marketdata.SnapshotWriter
        |-- 乱序/修正判断 + 规范涨跌幅 --> PostgreSQL symbol_market
        |-- 同值兼容双写 ----------------> radio（只作迁移兼容）
        |-- 热点价格 key ----------------> Redis string（TTL 10min + 0~60s 抖动）
        |-- ZADD 涨跌幅% ----------------> Redis ZSET market:rank:change24h
                                          |
        +---------------------------------+--------------------------------+
        | ZSET 非空（快路径）              | ZSET 为空 / Redis 不可用（回退） |
        v                                 v
        ZREVRANGE / ZRANGE WITHSCORES      ORDER BY change_24h_pct
取 guid 列表                       DESC / ASC LIMIT N
        |                                 |
        +---------------+-----------------+
                        v
        ZSET score 负责展示涨跌幅；回表只补价格、名称、市场身份和 logo
                        |
                        v
        POST /api/v1/get_top_movers  {code:2000, result:[...], source}
                        |
                        v
        兼容 Top Movers API / Markets 全局涨跌排序
```

## 关键代码位置

| 文件 | 函数 / 位置 | 作用 |
|---|---|---|
| `common/marketkey/key.go` | `RankChange24hKey` 常量 | 榜单 ZSET 的 key：`market:rank:change24h` |
| `redis/redis.go` | `ZAdd` / `ZScores` / `ZRangeWithScores` / `ZRevRangeWithScores` | ZSET 读写；`ZScores` 批量给 Markets 当前页补可信涨跌幅，并区分真实 0 与缺成员 |
| `marketdata/snapshot_writer.go` | `Write` | 唯一 adapter 写入口；PG 接受后刷新热点缓存和 ZSET |
| `database/symbol_market.go` | `ApplyMarketSnapshot` | 乱序、no-op、correction 与 `change_24h_pct/radio` 双写 |
| `database/symbol_market.go` | `QuerySymbolMarketsByGuids` | 按 ZSET 读出的 guid 批量回表 |
| `database/symbol_market.go` | `QuerySymbolMarketsByChange` | SQL 回退：只按非空 `change_24h_pct` 排序 |
| `services/http/model/mover.go` | `TopMoversRequest` / `TopMoverItem` / `TopMoversResponse` | 接口模型；条目字段与 dashboard 一致并加 `rank` |
| `services/http/service/mover.go` | `GetTopMovers` | 收敛 direction/limit（默认 5、上限 20），编排 Redis 快路径与 SQL 回退 |
| `services/http/service/mover.go` | `rankMarketsFromRedis` | ZCard 判空 → ZRANGE/ZREVRANGE → 回表并保持榜单顺序 |
| `services/http/routes/mover.go` | `GetTopMovers` | HTTP 处理器：解 JSON、调 service、信封返回 |
| `services/http/api.go` | `initRedis` / 路由注册 | API 进程接入 Redis（失败仅告警，榜单自动降级 SQL） |
| `frontend/src/api/market.ts` | `getTopMovers(direction, limit)` | 前端类型化请求函数（数字字符串兜底转 number） |
| `services/http/service/asset.go` | `rankScores` / `changeRankOrder` | Markets 当前页补分数与服务端全局涨跌排序 |

## 本地验证

前提：`make dev-deps`（PostgreSQL + Redis）、`make migrate && make seed`、`make api` 已启动。

### 1. 直接看 ZSET

crawler 运行后（或手动造数据）：

```bash
# 手动写入几条测试分数
redis-cli ZADD market:rank:change24h 3.21 s1
redis-cli ZADD market:rank:change24h -1.85 s2
redis-cli ZADD market:rank:change24h 5.67 s3

# 涨幅榜（高分在前）
redis-cli ZREVRANGE market:rank:change24h 0 -1 WITHSCORES

# 跌幅榜（低分在前）
redis-cli ZRANGE market:rank:change24h 0 -1 WITHSCORES
```

### 2. 调接口

```bash
# 涨幅榜，默认 limit=5
curl -X POST http://127.0.0.1:9092/api/v1/get_top_movers \
  -H 'Content-Type: application/json' \
  -d '{"consumer_token":"frontend-dashboard","direction":"gainers","limit":5}'

# 跌幅榜
curl -X POST http://127.0.0.1:9092/api/v1/get_top_movers \
  -H 'Content-Type: application/json' \
  -d '{"consumer_token":"frontend-dashboard","direction":"losers","limit":5}'
```

预期返回 `code: 2000`，`result` 按涨跌幅排序、带 `rank`，且 `source` 为 `"redis"`。然后验证回退：

```bash
redis-cli DEL market:rank:change24h
# 再发同样请求，仍返回 code 2000，source 变为 "sql"；change_available=false，
# SQL 只读取规范字段；规范字段也为空时返回空榜，不把旧 radio 当真值
```

### 3. 看前端

`npm run dev` 后打开 Markets。涨跌幅排序必须是服务端全局顺序；单个市场在 Redis 与规范字段都无值时显示 `—`，不能显示假 `0%`。主导航不再挂载旧 Dashboard。

## Owner 先讲这 60 秒

> Binance 与 Hyperliquid 都把快照交给同一个 SnapshotWriter。PG 先决定这条数据是更新、修正、no-op 还是乱序丢弃；接受后才派生写热点 key 和以 24h 百分比为 score 的 ZSET。Redis 快路径直接用 score，失效时 SQL 只读可空的 change_24h_pct，旧 radio 不再参与读口径。worker 只扫 K 线缺口，不会覆盖价格或涨跌幅。缺值是 Unknown，不是 0。

## 大白话术语表

| 术语 | 准确含义 | 大白话 | 在本项目中 |
|---|---|---|---|
| Redis | 以内存数据结构为核心的高性能数据存储 | 把常用商品放到收银台手边 | 保存热点行情和涨跌榜，不是完整行情老底 |
| ZSET | member 带 score、由 Redis 自动排序的集合 | 会自动排位的选手榜 | member 是 symbol GUID，score 是 24h 涨跌百分比 |
| TTL 抖动 | 在基础过期时间上加入随机量 | 让商品不要同一秒全部过期下架 | 价格、买一、卖一、成交量 String key 使用 10min + 0～60s；ZSET 本身不靠它过期 |
| PG 回表 | 用缓存返回的标识去 PostgreSQL 查完整记录 | 拿排行榜号码回总账查选手档案 | 用 ZSET 的 GUID 补齐名称、价格和交易所信息 |
| SQL 回退 | 缓存路径不可用时由数据库规范字段提供顺序 | 收银台关门后直接翻仓库总账 | Redis 空或报错时按非空 `change_24h_pct` 排序 |
| 缓存雪崩 | 大量缓存同时失效，流量集中冲击后端 | 商场商品同时下架，顾客一起冲后仓 | 热点 key 用随机 TTL 错开过期时间 |

## 从入口到结果的代码调用链

```text
Binance / Hyperliquid adapter
  -> marketdata.SnapshotWriter.Write
  -> database.ApplyMarketSnapshot
  -> redis.Client.ZAdd
  -> services/http/routes/mover.go GetTopMovers
  -> services/http/service/mover.go GetTopMovers
     -> getMoversFromRedis
     -> database 查询完整行情
     -> Redis 无结果时走 SQL 排序
  -> Markets / 兼容调用方
```

讲代码时只抓三处：写榜入口、服务层快路径/回退、页面双榜。Redis client 的 `ZAdd/ZRange` 是基础设施封装，不要把它讲成业务规则所在地。

## 失败、降级与恢复边界

| 故障 | 当前行为 | Owner 应怎样解释 |
|---|---|---|
| Redis 不可达或 ZSET 为空 | SQL 按规范字段回退，响应 `source=sql` | 仍不读取旧 radio；规范字段为空就没有该榜单项 |
| ZSET 有 GUID、PG 缺详情 | 无法拼出完整条目 | Redis 不是事实源，需要监控缓存与 PG 的数据一致性 |
| PostgreSQL 不可达 | 无法完成回表或 SQL 回退 | 不能只拿 Redis 排名伪造完整行情 |
| 大量热点 key 同时过期 | 当前用 TTL 抖动分散 | 只降低集中失效概率，不替代容量规划和限流 |

## 闭卷自检

1. 为什么 ZSET 不保存完整行情对象？
2. PG 回表和 SQL 回退分别在什么情况下发生？
3. TTL 抖动到底作用在哪类 key，为什么不是 ZSET？
4. Redis 仍有排名但 PostgreSQL 故障时，为什么不能正常返回完整榜单？
5. 从 adapter 到兼容 Top Movers API 的三个业务入口分别在哪里？
6. 为什么 Redis score 为 0 和 Redis 没有这个 member 必须区分？

## 边界与后续方向

- **榜单不设 TTL**：ZSET 只存 guid+score；规范涨跌幅为空时 SnapshotWriter 会 `ZREM`。未来市场下线仍需目录停用流程清理成员。
- **兼容双写**：`radio` 仍由 SnapshotWriter 同值写入，只为迁移期对账；所有新读路径使用 Redis score 或 `change_24h_pct`。删除旧列仍需最终七天验收后单独批准。
- **limit 上限 20**：榜单是"速览"语义，完整排序列表属于 Markets 页的服务端分页范畴，不在此接口扩展。
- **可做未做**：ZSET 快照持久化（重启后秒级恢复榜单）、涨幅榜按成交额加权过滤低流动性币、榜单变化推送（WebSocket 替代 30s 轮询）。
