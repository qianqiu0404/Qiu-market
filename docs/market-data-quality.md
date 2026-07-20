# 行情数据质量与异常补偿

## 核心原则

行情服务的数据本身就是产品价值。系统不能用 mock 价格掩盖真实数据源异常，应该明确暴露错误态、延迟状态和数据质量问题。

## 必须关注的指标

| 指标 | 含义 | 项目落点 |
|---|---|---|
| last_updated | 最近一条行情更新时间 | `symbol_market.updated_at` |
| data_delay_seconds | 当前时间与最近行情时间差 | Dashboard / Markets 展示 |
| source_status | 数据源是否可用 | 生产可接监控告警 |
| kline_gap | K 线是否缺失 | 生产用补偿 worker 扫描 |
| price_jump | 价格是否异常跳变 | 生产用规则或风控阈值 |

## 异常场景

### 外部数据源超时

当前项目会让 API 返回明确错误态，前端展示 Error。生产环境应增加 timeout、retry、熔断和备用数据源。

面试回答：

> 行情服务不能无限等待外部交易所。请求必须有 timeout，失败后记录错误并暴露数据源状态。生产环境可以接多个 source，比如 Binance、OKX、CoinGecko，按优先级切换。

### Binance / CoinGecko 限流

限流时不能伪造数据。可以降低抓取频率，短时间保留旧值，但必须展示 `last_updated` 和 `data_delay_seconds`。

面试回答：

> 旧数据可以作为 stale data 展示，但不能当作 live data。页面必须显示数据延迟，否则用户会误以为价格仍然实时。

### Redis 不可用

Redis 是缓存和高频中间层，不应该成为唯一真相。生产环境可降级读 DB，同时标记缓存状态异常。

### DB 写入失败

Crawler 或 Worker 写 DB 失败时，应记录失败任务，下轮重试。生产环境可以增加 outbox 或 failed_jobs 表。

### K 线缺口

K 线应按 `symbol + interval + open_time` 检查连续性。如果发现缺口，补偿 worker 根据缺失时间段回拉数据。

面试回答：

> K 线不是简单查最近 100 条。生产里要校验 open_time 是否连续，缺哪段补哪段，避免图表断层或聚合周期错误。

### 价格突变

价格突然偏离上一条过大时，不应该直接覆盖展示。生产可按交易对配置阈值，例如 5 分钟内涨跌超过 20% 进入异常队列。

## 当前项目边界

已实现：

- API 失败展示 Error，不返回假行情。
- HTTP 错误响应统一为 JSON。
- Dashboard / Markets 展示 `last_updated` 和 `data_delay_seconds`。
- Klines 页面拆分图表 chunk，降低首屏负担。

未生产化但可讲清：

- 多数据源自动切换。
- K 线缺口补偿 worker。
- 价格突变风控规则。
- 行情延迟告警。
- 数据源级别 SLA 统计。

## 2500U 面试表达

> 我做行情服务时重点关注数据可信。价格接口失败不会返回 mock 数据，而是统一 JSON 错误，前端展示 Error。同时我增加了 last_updated 和 data_delay_seconds，因为行情不是“有数据就行”，还要知道数据是否新鲜。生产化方向是多数据源容灾、K 线缺口补偿、价格突变检测和延迟告警。
