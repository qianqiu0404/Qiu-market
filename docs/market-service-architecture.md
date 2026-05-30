# 行情服务架构与面试表达

## 架构定位

S78 Market Services 是交易所后台的数据服务，不是钱包资金服务。它和钱包三件套互补：

```text
交易所后端
  ├─ 钱包服务：充值 / 提现 / 签名 / 广播 / 通知
  └─ 行情服务：价格 / K线 / 市值 / 交易所 / 交易对 / 法币汇率
```

## 数据流

```text
Binance / CoinGecko / Fiat API
        |
        v
Crawler
  - ticker
  - klines
  - market cap
  - fiat rates
        |
        v
PostgreSQL / Redis
        |
        v
HTTP API / gRPC
        |
        v
Vue Dashboard
```

## 核心模块

| 模块 | 价值 | 面试表达 |
|---|---|---|
| Crawler | 外部行情采集 | “外部 API 要有 timeout、错误处理和数据质量校验。” |
| Worker | 聚合和落库 | “高频数据适合先缓存，再异步写入或聚合。” |
| Database | 持久化查询 | “价格不能直接用 float，要用 numeric 或放大整数。” |
| HTTP API | 前端接口 | “错误态要明确，不拿 mock 假数据兜底。” |
| Frontend | 数据展示 | “API 失败时展示 Error，避免伪装实时行情。” |

## 异常场景

| 场景 | 处理方式 |
|---|---|
| Binance 超时 | 记录错误，保留错误态，生产可切换备用源 |
| CoinGecko 限流 | 降低频率，使用上次成功值并标注更新时间 |
| Redis 不可用 | worker 降级读 DB 或暂停聚合 |
| DB 写入失败 | 记录错误，worker 下轮重试 |
| K 线缺口 | 按 symbol + interval + open_time 补偿 |
| 前端 API 失败 | 展示 Error，不展示假行情 |

## 2500U 加分点

> 我主项目是钱包服务，行情服务是交易所后端附加项目。它体现我能把外部数据源、定时任务、缓存、数据库、API 和前端错误态串起来。特别是我不会用 mock 价格伪装实时行情，数据异常就明确展示错误，这符合金融系统的数据可信原则。
