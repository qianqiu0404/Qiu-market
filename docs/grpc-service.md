# gRPC 行情服务：MarketService

MarketService 监听 `127.0.0.1:9091`，使用 protobuf 提供 16 个强类型只读 RPC。HTTP 和 gRPC 共用 `services/http/service.HandleSvc`，避免 provider selection、All 去重并集、综合价、资产排序和目录审计发展成两套规则。

## 当前 RPC

| RPC | HTTP 对应接口 | 语义 |
|---|---|---|
| GetMarketDashboard | `get_market_dashboard` | venue 级市场分页 |
| GetAssetDashboard | `get_asset_dashboard` | base asset 聚合、嵌套 markets |
| GetMarketInsights | `get_market_insights` | 实时宽度与跨 venue 比较 |
| GetKlines | `get_klines` | market_id 优先的 K 线 |
| GetSystemOverview | `get_system_overview` | 进程/依赖状态与独立 provider 状态 |
| GetSupportAssets | `get_support_assets` | 资产目录 |
| GetSymbols | `get_symbols` | 交易对目录 |
| GetExchanges | `get_exchanges` | 交易所目录 |
| GetFiatRates | `get_fiat_rates` | 法币汇率 |
| GetTopMovers | `get_top_movers` | Redis ZSET 涨跌榜 |
| GetMarketSparklines | `get_market_sparklines` | 批量 7D 迷你走势 |
| GetMarketOverview | `v2/get_market_overview` | CoinGecko global 与内部综合价宽度 |
| GetAssetDashboardV2 | `v2/get_asset_dashboard` | CMC 首页资产级综合价分页，不嵌套 markets |
| GetAssetMarkets | `v2/get_asset_markets` | 单资产 Spot/Perp 市场按需读取 |
| GetProviderCatalogAudit | `v2/get_provider_catalog_audit` | provider 候选目录身份审计 |

`get_asset_momentum` 当前只提供 HTTP：gRPC 进程没有 Doris 连接，历史分析不应偷偷走另一套依赖和错误语义。若未来增加对应 RPC，应先把 Doris 连接作为显式可选依赖注入，而不是在 handler 内自行连接。

## 设计决策

每个 handler 只做三件事：校验 protobuf 请求、调用共用业务层、转换 protobuf 响应。被拒绝的替代方案是复制一套 gRPC 业务逻辑；它会让市场排序、未知涨跌幅、资产聚合和跨 venue 降级规则逐渐漂移。

代价是 `services/grpc/handle.go` 必须逐字段转换 model 与 protobuf。这个机械成本是刻意保留的传输边界，也要求测试新字段映射。数值继续使用 string，避免 float64 破坏价格和大额市值精度。

protobuf 兼容规则：已有字段号不修改、不复用；新字段和新消息只追加。v2 请求可显式声明 `universe`，响应返回 selection version/rank、last attempt/success 与 freshness；System provider 状态返回 selection version/target/count。`market_id` 是市场身份，`asset_id` 是基础资产身份，不能用一个替代另一个。

v2 的 `AvailableDecimal` 同时携带 `value` 和 `available`：没有综合价、涨跌幅或汇率时使用 unavailable，而不是字符串 `"0"`。旧 v1 RPC 保留到最终七天验收后再生成清理清单。

## 调用链

```text
gRPC client
  -> protobuf/market-services.proto
  -> services/grpc/handle.go
  -> services/http/service.HandleSvc
  -> PostgreSQL / Redis
  -> proto response
```

综合资产首页示例：

```text
GetAssetDashboardV2
  -> QueryAssetIndexDashboard
  -> provider 读取自己的激活 selection；All 读取四家 canonical 去重并集
  -> last success 30 秒内 Fresh，5 分钟内 Stale，否则 Unavailable
  -> 服务端搜索/过滤/排序/分页
  -> 返回 asset row，不嵌套 markets

GetAssetMarkets
  -> 按 asset_id 查询具体 venue
  -> 稳定币率只用于相对综合价偏差
  -> 返回 Spot/Perp、freshness、confidence、has_kline
```

## 错误语义

| 情况 | gRPC status |
|---|---|
| 非法分页、排序、interval 或方向 | `InvalidArgument` |
| PostgreSQL / Redis / 内部查询失败 | `Internal` |
| 合法但无数据 | OK + 空 result |

缺 Redis score 是业务未知态，不是 RPC 错误；响应保留 `change_available=false`。这与 HTTP 的 `—` 展示语义一致。

## 关键入口

1. `protobuf/market-services.proto`：16 个 RPC 与兼容字段号。
2. `services/grpc/proto/*`：`make proto` 生成的 Go stub，不手改。
3. `services/grpc/handle.go`：参数校验与 model/proto 转换。
4. `services/http/service/market_index.go`：HTTP/gRPC 共用的 v2 综合资产与目录规则。
5. `cmd/market-services/cli.go`：`rpc` 模式连接 PG/Redis 并注册服务。

## 术语

| 术语 | 准确定义 | 大白话 | 项目位置 |
|---|---|---|---|
| RPC | 跨进程调用远端方法 | 像调函数一样请另一个服务做事 | MarketService |
| protobuf | 有字段号的二进制接口定义 | 双方共同遵守的结构化表格 | `.proto` |
| unary RPC | 一次请求对应一次响应 | 一问一答 | 当前 16 个方法 |
| reflection | 服务运行时暴露接口描述 | grpcurl 可现场查看菜单 | `service.go` |
| shared service layer | 多协议复用同一业务规则 | 两个窗口共用同一个后厨 | `HandleSvc` |

## 生成、启动与验证

```bash
make proto
make rpc

grpcurl -plaintext 127.0.0.1:9091 list
grpcurl -plaintext 127.0.0.1:9091 describe market.MarketService

grpcurl -plaintext \
  -d '{"page":1,"page_size":20,"sort_by":"rank","sort_direction":"desc"}' \
  127.0.0.1:9091 dapplink.xyz.MarketService/GetAssetDashboardV2

grpcurl -plaintext -d '{}' \
  127.0.0.1:9091 dapplink.xyz.MarketService/GetMarketOverview

# 仓库内可重复的真实 RPC 验收；默认测试套件未设置地址时会跳过
S78_TEST_GRPC_ADDR=127.0.0.1:9091 \
  go test ./services/grpc -run TestLiveAssetDashboardV2 -count=1 -v
```

验证 BTC Spot + Perp 时，GetAssetDashboardV2 只能返回一个 BTC 资产项；具体市场必须再调 GetAssetMarkets，Perp 的 confidence 为 excluded_perp。

## 失败与恢复

- Redis 不可用：服务按既有容错启动，依赖 Redis 的涨跌分数保持未知或走已有回退，不能伪造为 0。
- PostgreSQL 不可用：RPC 进程无法提供核心读模型，调用返回 Internal；修复连接后重启。
- 转换层漏字段：构建不会总能发现语义遗漏，因此新增 proto 字段必须做响应验证。
- 客户端仍用旧 proto：新增字段会被忽略；已有字段号未变，保持向后兼容。
- Doris 不可用：16 个当前 RPC 不依赖 Doris，不受影响。

## Owner 60 秒解释

> MarketService 有 16 个一元 RPC。v2 RPC 提供首页概览、资产聚合分页、按需市场/venue 抽屉和 Catalog Audit。provider 页读自己的 50 资产 selection，All 读四家 canonical 去重并集；gRPC handler 不复制这些 SQL 或综合价规则，只调用 HTTP 共用 HandleSvc。Unknown 用 AvailableDecimal，旧字段号没有修改。历史动量只走 HTTP，因为 gRPC 进程没有 Doris 依赖。

## 闭卷自检

1. 为什么 gRPC handler 不能复制一套资产聚合 SQL？
2. `market_id` 与 `asset_id` 分别是什么？
3. 为什么数值字段使用 string？
4. 缺 Redis score 为什么不是 Internal 错误？
5. 为什么当前不提供 GetAssetMomentum RPC？
6. 如何用 grpcurl 或仓库 live integration test 证明 16 个方法已运行？
7. 为什么 v2 请求和响应要显式携带 `universe` 与 selection version？
