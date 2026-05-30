# S78 Market Services

S78 Market Services 是一个面向交易所/钱包后台的行情数据服务学习项目。项目包含行情采集、价格聚合、K 线写入、法币汇率、PostgreSQL 存储、Redis 缓存、HTTP API、gRPC 接口和 Vue 管理前端。

## 项目定位

这个项目不是钱包主链路，但可以作为 Web3 后端面试的加分项目：

- 证明我理解交易所数据服务如何采集和展示行情。
- 证明我能处理外部数据源、缓存、数据库、API 和前端展示之间的链路。
- 证明我不会用 mock 假数据伪装实时行情，数据源异常时会明确展示错误态。

和钱包项目组合时，它的位置是“交易所后端数据服务”：钱包三件套负责充值、提现、签名和链上状态；行情服务负责交易所后台需要展示的资产价格、K 线、交易所、交易对和法币汇率。

## 架构

```text
External Data Sources
  - Binance ticker / klines
  - CoinGecko market cap
  - Open ER API fiat rates
        |
        v
Crawler / Worker
  - 定时抓取
  - 精度转换
  - 写入 PostgreSQL
  - Redis 缓存读取
        |
        v
Service Layer
  - Asset
  - Exchange
  - Symbol
  - Market Dashboard
  - Klines
  - Fiat Rates
        |
        v
HTTP API / gRPC
        |
        v
Vue Dashboard
```

## 核心能力

| 模块 | 作用 |
|---|---|
| `crawler` | 抓取 Binance / CoinGecko / 法币汇率数据 |
| `worker` | 从 Redis / DB 聚合行情并写入市场表 |
| `database` | GORM + PostgreSQL 表模型和查询 |
| `services/http` | 行情、资产、交易所、K 线、总览 API |
| `services/grpc` | gRPC 服务接口 |
| `frontend` | Vue3 + Vite 行情后台 |

## 不使用 Mock 行情兜底

行情系统的核心是数据可信。如果 API、数据库或外部数据源异常，前端应该展示错误态，而不是展示 BTC/ETH 假数据。

当前前端策略：

- API 成功：展示真实行情，状态为 `Connected`。
- API 失败：展示错误态，状态为 `Error`。
- 不返回 mock 行情数据。
- 不使用假数据兜底。

## 本地验证

后端：

```bash
go test ./...
go build ./...
```

前端：

```bash
cd frontend
npm run build
```

## 面试讲法

> 这个行情服务负责交易所后台的市场数据能力。Crawler 定时从 Binance、CoinGecko 和法币汇率 API 获取数据，经过精度转换后写入 PostgreSQL；HTTP API 给前端提供行情看板、K 线、资产、交易所和系统总览。这里我特意去掉了 mock fallback，因为行情服务不应该用假数据掩盖数据源异常，API 失败时应该展示明确错误态。

## 生产化边界

- 还需要完整的多数据源容灾和权重策略。
- 还需要行情延迟指标和告警。
- 还需要更完整的 Redis 缓存策略。
- 还需要数据质量校验，例如价格突变、成交量异常、K 线缺口。
- 还需要 API 鉴权、限流和监控。

## 和钱包项目的组合讲法

> 钱包项目解决资金流：充值、提现、签名、广播、通知。行情服务解决交易所后台的数据流：价格、K 线、市值、交易所和交易对。两个项目组合起来，可以说明我不只是会链上钱包，也理解交易所后端里“资金服务 + 行情数据服务”这两条核心链路。
