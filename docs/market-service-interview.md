# 行情服务面试讲解

## 一句话定位

S78 Market Services 是一个交易所行情数据服务，负责从外部交易所和行情平台采集价格、K 线、市值和法币汇率，并通过 API 和前端看板展示。

## 为什么不能用假数据兜底

行情系统和普通内容系统不一样，数据本身就是产品价值。API 挂了以后如果展示 mock 行情，会让用户误以为价格仍然实时，风险比空状态更高。

因此当前策略是：

- 成功就展示真实数据。
- 失败就展示错误态。
- 不用 mock BTC/ETH 数据伪装实时行情。

## 核心链路

```text
CoinGecko Top 200 + Binance/Coinbase/Bybit/OKX + Hyperliquid
        |
        v
Catalog Audit + independent adapters
        |
        v
SnapshotWriter
   |          |
   v          v
PostgreSQL  Redis（派生）
        |
        +-- worker 扫缺口 -> repair task -> crawler 回补
        |
        +-- 5 秒 composite index
        v
HTTP API / gRPC
        |
        v
Qiu Market（Markets / Insights / System）
```

## 面试官为什么问

行情服务会考察后端工程里的几个关键能力：

- 外部 API 调用和超时控制。
- 定时任务和优雅退出。
- 精度处理，避免 float 直接存金额。
- Redis / DB 协作。
- 前端错误态和数据可信。
- 数据源失败时的降级策略。

## 标准回答

> 我这个行情服务先用 CoinGecko Top 200 建资产宇宙，再审计 Binance、Coinbase、Bybit、OKX 的 Spot 目录。provider alias 未审核时只进待审表，不按 symbol 猜身份。四家行情独立采集并统一写 SnapshotWriter；综合价只取 30 秒内报价，稳定币率 10 分钟内有效，剔除偏离中位数 3% 的报价，再按 venue 成交额限权。Hyperliquid Perp 只比较，不参加现货指数。

## 高频追问

### 为什么要统一错误响应？

如果有些接口返回 JSON、有些接口返回纯文本，前端就很难稳定处理异常。当前项目把错误统一为 `{ code, message, result }`，前端只需要判断 `code === 2000` 是否成功。

### 行情数据不新鲜怎么办？

不能只看有没有价格，还要看 `provider_updated_at`、`freshness_status` 和 System 的 provider 最近成功时间。如果价格是几小时前的数据，应该展示 Stale，而不是把页面刚刷新当成行情实时。

### 数据源失败怎么办？

单 provider 超时或 429 只让该 adapter 退避，其他来源继续。综合价按 contributor 数降 confidence；全部 Spot 失败时返回 Unknown，不用 CoinGecko 当前价补洞。长期仍需要告警和 SLA。

### 为什么不能按 symbol 自动合并资产？

同一个代码在不同 provider 可能指向不同资产。项目把 provider alias 当成待审核主数据；symbol 唯一只能给出 pending 建议，只有 canonical identity 明确后市场才能 enabled。

### 为什么价格不用 float 存？

float 有精度误差。项目里价格会放大为整数或使用 numeric/decimal 存储，展示时再还原。

### Redis 的作用是什么？

Redis 保存热点行情、ZSET 排名和进程心跳；PostgreSQL 是快照与任务真值。worker 不再从 Redis 聚合价格写回 DB。

### K 线缺口怎么办？

项目已按 market_id + interval + open_time 扫描缺口：worker 写持久化 repair task，crawler 用 SKIP LOCKED 领取并回补，逐根验证后才完成。

### 价格突然异常跳变怎么办？

生产环境需要数据质量规则，例如同一交易对短时间涨跌超过阈值时进入异常队列，不直接覆盖展示。

### 和钱包项目有什么关系？

钱包项目负责充值提现，行情服务可以作为交易所后台的市场数据模块。组合起来能体现我不仅会链上钱包，也理解交易所后端的数据服务。

### 面试时放在什么位置讲？

不要把行情服务放在第一主项目。第一主项目仍然是钱包三件套；行情服务作为附加项目，用来证明你理解交易所后端除了资金流之外，还有价格、K 线、交易对、法币汇率和数据源稳定性。

## 可背诵版本

> 行情服务最重要的是身份和价格都可信。Top 200 是资产宇宙，四家 Spot 目录先过 provider alias 审核。每家 adapter 独立运行，但都通过 SnapshotWriter 先写 PG。综合价只用新鲜、非离群、汇率有效的 Spot，Perp 永远排除；只剩一家就标 low，全部失败就是 Unknown。Redis 是缓存，worker 只开 K 线 repair task，Doris 只做历史旁路。

## 升级后可背诵版本

> 我做了四层收口：目录层不按 symbol 猜身份，采集层每家 provider 独立，写入层只有 SnapshotWriter，读模型层用可审计 contributor 形成综合 Spot。前端固定资产粒度，具体 Spot/Perp 放右侧抽屉；Phase 0 安全门未通过前只发现目录不切新 venue。错误与未知值如实展示，不用 mock 或 0% 冒充真值。

## 闭卷自检

1. 为什么 Redis 不能先于 PostgreSQL 更新？
2. 为什么 worker 不能直接访问 Binance 做回补？
3. Running 与 Healthy 分别由什么证据支持？
4. change_24h_pct 缺失时为什么不能返回 0？
5. 行情域与交易域各自拥有什么状态？
6. 为什么 Perp 能进抽屉却不能参加 composite？
7. Phase 0 安全门关闭时哪些代码仍然运行？
