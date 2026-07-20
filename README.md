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
- HTTP API 错误统一返回 JSON，前端不用兼容纯文本错误。
- Dashboard / Markets 展示行情更新时间和数据延迟。

## 本地验证

### 后端本地启动

项目的启动入口是 `cmd/market-services/main.go`，运行模式在 `cmd/market-services/cli.go` 里，包括 `migrate`、`api`、`crawler`、`worker` 和 `rpc`。

第一次本地启动建议按这个顺序：

```bash
# 1. 启动本地 PostgreSQL 和 Redis
make dev-deps

# 2. 执行数据库迁移
make migrate

# 3. 写入 Dashboard 演示数据
make seed

# 4. 启动 HTTP API，默认监听 127.0.0.1:9092
make api
```

这里的“本地 Docker”可以简单理解成：不用手动安装和配置 PostgreSQL / Redis，而是让 Docker 在你电脑上启动两个隔离的小服务容器。这个项目的 `docker-compose.yml` 会启动：

- PostgreSQL：保存资产、交易对、行情、K 线等数据。
- Redis：保存 crawler 写入、worker 读取的热点价格。

如果你电脑上已经手动装了 PostgreSQL 和 Redis，也可以不用 Docker。确认它们是否可用：

```bash
source .env
pg_isready -h "$MARKET_MASTER_DB_HOST" -p "$MARKET_MASTER_DB_PORT" -U "$MARKET_MASTER_DB_USER" -d "$MARKET_MASTER_DB_NAME"
redis-cli -h 127.0.0.1 -p 6379 ping
```

常用验证接口：

```bash
curl -X POST http://127.0.0.1:9092/api/v1/get_market_dashboard \
  -H 'Content-Type: application/json' \
  -d '{"page":1,"page_size":10}'

curl -X GET http://127.0.0.1:9092/healthz
```

如果要启动采集和后台处理，可以另外开终端执行：

```bash
make crawler
make worker
```

本地演示链路可以这样理解：

```text
make crawler
  -> BinanceTickerCrawler 拉取 Binance ticker / kline
  -> 写入 Redis 热点价格 key，同时更新 PostgreSQL symbol_market / symbol_kline

make worker
  -> MarketPriceHandle 从 Redis 读取 Binance 价格 key
  -> 按 symbol_guid 更新 PostgreSQL symbol_market 最新行情，不存在时再创建

make api
  -> HTTP API 从 PostgreSQL 查询数据
  -> Vue Dashboard 通过 /api/v1/get_market_dashboard 展示
```

### 前端本地启动

前端在 `frontend` 目录，Vite 会把 `/api` 请求代理到 `http://localhost:9092`：

```bash
cd frontend
npm install
npm run dev
```

也可以在项目根目录执行：

```bash
make frontend-dev
make frontend-build
```

### 构建与测试

后端：

```bash
go test ./...
go build ./cmd/market-services
```

本地链路检查：

```bash
make verify-local
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
- 当前已用 `symbol_market.updated_at` 表达数据新鲜度，生产环境可进一步抽象成采集状态表。
- Klines 页面已做图表 chunk 拆分，生产环境还可继续做更细粒度懒加载。

## 和钱包项目的组合讲法

> 钱包项目解决资金流：充值、提现、签名、广播、通知。行情服务解决交易所后台的数据流：价格、K 线、市值、交易所和交易对。两个项目组合起来，可以说明我不只是会链上钱包，也理解交易所后端里“资金服务 + 行情数据服务”这两条核心链路。

## 后续面试追问回答

> 如果面试官问“行情数据不新鲜怎么办”，我会回答：我在接口里增加了 `last_updated` 和 `data_delay_seconds`，前端能区分 Live、Delayed 和 Stale。生产环境会继续接多数据源、K 线缺口补偿、价格突变检测和延迟告警。
