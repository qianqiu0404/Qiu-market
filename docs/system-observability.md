# Qiu Market System 可观测性与可信状态

本文是 System 页只读状态契约的 canonical 工程文档。它只解释行情、虚拟交易、
数据库、磁盘与保留任务的当前证据，不启动、停止、重启或修改任何服务，也不改变
LaunchDaemon、Guardian、Vercel、OAuth 或 Production 配置。

## 问题与可观察结果

旧 `/api/v1/get_system_overview` 已经提供进程心跳、provider 状态和部分存储统计，
但旧字段以普通数字和空字符串表示。调用方无法区分“真实为 0”与“后端没有返回”，
还会出现这些误报：

- retention 从未成功且 `last_error=""` 时被显示成 healthy；
- 旧后端没有 `storage` 时，前端类型兜底把数据库大小、磁盘和删除行数变成 0；
- 交易 matching、双边 liquidity、transport 和 outbox 没有进入 System；
- 进程心跳时间被行情 `updated_at` 冒充；
- route price 与 reference display price 没有在状态页说明各自来源和边界。

新设计的可观察结果是：

1. 每个运行组件都有 `state / last_success_at / age_seconds / reason / source`；
2. 每个数字使用 `{available,value,reason}`，缺失就是 unavailable，不是 0；
3. System 同时显示数据库/K 线大小、K 线估算行数、四周期最早/最新时间、磁盘余量、
   retention 开始/成功/错误与各周期删除行数；
4. Route price 和 Reference display price 分栏，不允许互相补值；
5. 总状态统一为 `LIVE / CACHED / DEMO SNAPSHOT / DEGRADED / OFFLINE`；
6. 旧后端只有在新路由明确返回 404/405/501 时才走兼容适配；网络错误或新路由 500
   不会被旧接口掩盖。

## 端到端只读控制流

```text
Browser /system（15 秒轮询）
          |
          v
POST /api/v1/get_system_status
          |
          +--> legacy system overview
          |      ├─ Redis role heartbeat existence
          |      ├─ PostgreSQL catalog/provider reads
          |      ├─ pg_database_size / pg_*relation_size
          |      ├─ K-line min/max(open_time)
          |      ├─ kline_retention_status
          |      └─ statfs("/")
          |
          +--> market overview
          |      ├─ all: CEX Spot asset_price_index freshness
          |      ├─ uniswap: route coverage/freshness
          |      └─ pancakeswap: route coverage/freshness
          |
          +--> mounted trading HTTP handler（GET only）
                 ├─ BTC-USDT status -> matching + outbox
                 └─ BTC-USDT orderbook -> two-sided liquidity
                         |
                         v
                 loopback trading gRPC
```

聚合器不直接读浏览器 Cookie、私有账户、订单、余额、数据库凭据或客户端状态。两个
trading 请求都是已有匿名公共只读接口。System handler 返回 `Cache-Control:
no-store`，探针局部失败仍返回 200 和部分状态；这表示“状态读取成功地解释了故障”，
不是把依赖故障当健康。

## 状态字段契约

顶层契约版本是 `system-status.v1`，公式版本是 `system-display.v1`：

```json
{
  "code": 2000,
  "result": {
    "schema_version": "system-status.v1",
    "formula_version": "system-display.v1",
    "generated_at": 0,
    "overall": {
      "state": "degraded",
      "last_success_at": null,
      "age_seconds": null,
      "reason": "one or more required probes are stale, failed, or missing explicit evidence",
      "source": "system-display.v1"
    },
    "components": {},
    "processes": [],
    "storage": {},
    "price_sources": [],
    "provider_statuses": []
  }
}
```

### 八个必需组件

| 字段 | 直接来源 | LIVE 条件 | 缺失/失败行为 |
|---|---|---|---|
| `matching` | trading `GetStatus.state` | 明确为 `ready` | 无字段为 unknown；读失败为 offline；其它状态为 degraded |
| `liquidity` | BTC-USDT 公共 order book | bids 与 asks 都至少一档 | 缺少数组为 unknown；读失败为 offline；单边/空簿为 degraded |
| `transport` | trading status + order book 两次只读 | 两次都成功 | 只成功一次为 degraded；两次都失败为 offline |
| `market_data` | All 的 `asset_price_index` summary | 至少一个 priced asset 且 index age ≤30s | 30s–5m 为 cached；更旧/零覆盖为 degraded；无时间为 unknown |
| `outbox` | trading status 的 `outbox_*` | `outbox_state=ready` 且无当前错误 | 旧后端无字段为 unknown；读失败为 offline；其它状态为 degraded |
| `database` | legacy overview 的真实 PostgreSQL read status | 明确 Connected/Healthy/Ready/Running | 失败为 offline；缺字段为 unknown |
| `disk` | API 进程 `statfs("/")` | 有 free bytes 且 state=healthy | `<25GiB` warning、`<15GiB` critical 都为 degraded；无测量为 unknown |
| `retention` | `kline_retention_status` | 无当前错误且成功时间 ≤36h | 从未成功为 unknown；当前错误或成功超过 36h 为 degraded |

`last_success_at` 必须按来源解释：

- market-data、route 和 retention 使用各自持久化的最后成功时间；
- outbox 优先使用最后 publish 时间；
- matching、liquidity、transport 当前只有“本次只读探针成功时间”，不能冒充引擎
  内部最后一次业务成功；
- legacy 心跳只暴露 key 是否存在，没有 heartbeat timestamp，因此 System 明确显示
  last success unavailable，不能借行情时间填充。

### 可选数字

所有存储数字都是：

```json
{
  "available": false,
  "value": null,
  "reason": "storage metrics were not reported"
}
```

只有 `available=true` 时 `value=0` 才表示真实零。当前覆盖：

- `database_bytes`
- `kline_table_bytes`
- `kline_heap_bytes`
- `kline_index_bytes`
- `kline_estimated_rows`
- `disk_free_bytes`
- `retention_last_started_at`
- `retention_last_success_at`
- `retention_deleted_rows["1m"|"15m"|"1h"]`
- `kline_intervals[].oldest_at/newest_at`

`1m=7天 / 15m=90天 / 1h=365天 / 1d=永久` 是策略，不是数据存在证明。某周期没有
oldest/newest 时显示 unavailable；页面可以同时写明策略，但不能把 policy badge
冒充 observed data。

## 总状态公式

公式只使用八个必需组件，Route price 是独立可选事实，不拖停 CEX-only 产品：

| 总状态 | 精确规则 |
|---|---|
| `LIVE` | 八个组件全部明确 `live` |
| `CACHED` | 只有 `market_data=cached`，其余七个组件全部 `live` |
| `DEMO SNAPSHOT` | 响应显式声明 `source_mode=demo_snapshot`；不能从缺字段推断 |
| `OFFLINE` | trading transport offline，数据库 offline，market-data 也没有 live/cached 证据 |
| `DEGRADED` | 其它所有部分失败、陈旧、unknown、阈值告警或缺字段组合 |

组件允许 `UNKNOWN`，但总状态不会以 UNKNOWN 隐藏风险；任何必需组件 unknown 都把总
状态收口为 DEGRADED。前端会重新应用同一个公式，防止一个字段残缺却声称 LIVE 的
响应通过。

## Route price 与 Reference display price

| 列 | 来源 | 含义 | 明确边界 |
|---|---|---|---|
| Route price | Uniswap/PancakeSwap venue route summary | 带实际名义金额的 venue-specific 指示性路线报价 | 不是 CEX composite，不作为 reference display price |
| Reference display price | 新鲜 CEX Spot contributor 形成的 `asset_price_index` | 页面与虚拟 demo-maker 使用的只读综合参考 | 不是可执行路线价，不用 DEX、CoinGecko 当前价或 mock 补值 |

System 当前展示的是两条价格链路的状态和来源说明，不把某个资产的具体价格复制到
状态页。具体值仍由 Markets/Trade 的相应价格契约负责。

## 旧后端兼容

前端先请求新路由。只有 404、405、501 表示“后端版本尚无此路由”，此时并发读取：

1. legacy system overview；
2. All/Uniswap/PancakeSwap market overview；
3. trading status；
4. BTC-USDT order book。

兼容结果标记 `source_mode=legacy` 和
`schema_version=system-status.legacy-adapter.v1`。兼容层仍执行同一总状态公式：

- 无 `storage` -> 所有存储值 unavailable；
- 无 `outbox_state` -> outbox unknown；
- 无 `index_updated_at` 或 `priced_asset_count` -> market-data unknown；
- 无 bids/asks 字段 -> liquidity unknown；
- 明确数组为空 -> liquidity degraded；
- 新路由 500 或网络失败 -> 直接报错，不回退旧路由遮蔽问题。

兼容层只是版本窗口，不是长期双契约。新后端挂载并上线后，生产应以 native
`system-status.v1` 为准。

## 设计决策、被拒绝方案与代价

1. **新增版本化状态契约，不继续往普通数字 overview 上堆字段。** 被拒绝的是继续用
   `0/""` 同时表示零和缺失。代价是多一个 endpoint 和兼容适配，但缺失语义可审计。
2. **后端聚合只读事实。** 被拒绝的是浏览器直连 PostgreSQL、Redis 或 trading
   gRPC；当前方案保持 BFF/HTTP 安全边界，代价是 API 每 15 秒多做有限只读查询。
3. **局部失败返回部分状态。** 被拒绝的是任一依赖失败就让整个状态接口 500；那会
   丢掉仍可用于定位的事实。代价是客户端必须逐组件解释，不可只看 HTTP 200。
4. **流动性只证明双边可见，不声称 demo-maker 健康。** 公共订单簿可能包含用户单；
   被拒绝的是从 bids/asks 反推具体账户。若未来要证明 maker，必须增加去身份化专用
   状态字段。
5. **Transport 只证明 System 探针的 REST→gRPC 路径。** 它不等于浏览器 Trade 页
   WebSocket/polling transport；两者是不同观察点。
6. **磁盘阈值复用现有生产边界。** 25GiB warning、15GiB critical 与当前运维规则
   一致；但 `statfs("/")` 是否就是 PostgreSQL 数据卷必须在实际 Mac mini 验证。

## 关键代码入口与阅读顺序

1. `services/http/systemstatus/model.go`：状态、可选数字、storage 和 price source
   的 JSON 契约。
2. `services/http/systemstatus/service.go`：八组件派生、30s/5m/36h 阈值和总公式。
3. `services/http/systemstatus/trading_probe.go`：通过已挂载 public trading handler
   并行读取 status/orderbook，不跨越鉴权边界。
4. `frontend/src/api/system.ts`：native 校验、统一公式和旧后端兼容。
5. `frontend/src/views/System.vue`：组件证据、storage/retention 和价格来源分栏。

专项回归分别位于：

- `services/http/systemstatus/service_test.go`
- `frontend/src/api/system.test.ts`
- `frontend/e2e/system.spec.ts`

## 术语

| 术语 | 准确含义 | 大白话 | 项目位置 |
|---|---|---|---|
| Evidence state | 由明确来源、时间和规则派生的组件状态 | 不只亮灯，还写谁、何时、为什么 | `StatusEvidence` / `Evidence` |
| Optional metric | 数字值与“是否真的拿到”分开 | 没称重就写没称，不写 0 公斤 | `OptionalMetric` / `OptionalInt64` |
| Last success | 最近一次成功事实，不是最近一次尝试 | 上次真正送达是什么时候 | component `last_success_at` |
| Cached | 仅行情最后成功值在 30s–5m 内 | 电话刚断，牌子保留旧价并标年龄 | `market_data=cached` |
| Outbox | 与交易命令同事务写入、随后发布到 feed 的事件队列 | 已盖章待派送的回执 | `trading_outbox` / publisher status |
| Two-sided liquidity | 公共订单簿同时有 bid 和 ask | 买卖两边都有人挂牌 | BTC-USDT order book |
| Route price | 指定 DEX 路线和名义金额的指示价 | 问这条路现在大概能换多少 | DEX route summary |
| Reference display price | 新鲜 CEX Spot 组成的综合参考 | 多家现货共同支撑的展示牌 | `asset_price_index` |

## 故障、降级、重试与恢复

| 场景 | System 行为 | 重试/恢复 |
|---|---|---|
| matching 非 ready | matching degraded，总状态 degraded | 15 秒后重读；恢复 ready 后自动回 live |
| status 成功、order book 失败 | transport degraded、liquidity offline | 不重试写操作；下轮只读轮询 |
| 无 bid 或 ask | liquidity degraded | 新双边订单出现后下一轮恢复 |
| outbox 旧后端无字段 | outbox unknown，不默认 ready | 后端升级新契约后恢复明确状态 |
| 行情 30s–5m | market-data cached；其它全 live 时总状态 cached | 新 index 写入后恢复 live |
| 行情超过 5m | market-data degraded | crawler/provider 恢复写入后自动恢复 |
| PostgreSQL 读失败 | database offline；能返回的其它事实保留 | 恢复数据库；API 下轮只读重查 |
| storage query 失败 | 数字 unavailable，retention degraded/unknown | 不显示 0；查询恢复后重读 |
| 磁盘低于阈值 | disk degraded；System 本身不执行停机 | 由既有运维/写入守卫处理，System 只解释 |
| retention 当前错误 | retention degraded，保留上次成功时间 | Worker 下一次成功会清除 error |
| retention 从未成功 | retention unknown，总状态 degraded | 首次完成后才可变 live |
| 新状态路由 404 | 显式 legacy compatibility | 后端升级后自动切 native |
| 新状态路由 500/网络失败 | 页面 ErrorState/Offline，不回退 | 服务恢复后手动或 15 秒轮询重试 |

System 不含 retry 写按钮、rollout 按钮或服务控制按钮。页面上的 retry 只重新执行
HTTP read。

## 验证与证据边界

建议专项命令：

```bash
go test ./services/http/systemstatus
go test -race ./services/http/systemstatus
go vet ./services/http/systemstatus
cd frontend && npm test
cd frontend && npx playwright test e2e/system.spec.ts
cd frontend && npm run build
git diff --check
```

证据分级：

- `implemented`：版本化 model/service/handler、前端 native/legacy adapter、System
  展示和专项测试存在于当前提交；
- `build-verified`：以本次交付实际记录的 Go/Vitest/Playwright/Vue 命令为准；
- `integration-verified`：必须由真实 API route、loopback trading、PostgreSQL 和
  当前机器 filesystem 完成请求并核对响应；mock Playwright 不属于这一层；
- `environment-pending`：Mac mini 实际 PostgreSQL 数据卷是否对应 `statfs("/")`、
  Production Funnel/BFF 的新路由、真实磁盘阈值、真实 retention 最新成功/失败、
  provider/index freshness 与 outbox publish 时间；
- `production-recommendation`：未来可让 storage probe 使用显式 PostgreSQL data
  volume path，并增加去身份化 demo-maker 状态；它们不是当前行为。

## Owner 60 秒解释

> System 不再把 HTTP 200、进程心跳或缺失数字当成健康。后端每 15 秒只读聚合八个
> 必需组件：matching、双边 liquidity、REST 到 trading gRPC 的 transport、CEX
> 行情 freshness、outbox、PostgreSQL、磁盘和 retention。每个组件都带状态、最后
> 成功、年龄、原因和来源；每个数字都带 available，所以没拿到数据库大小不会显示
> 0。八项全部明确当前成功才是 LIVE；只有行情单独在 30 秒到 5 分钟内才是 CACHED；
> 任一缺字段都 DEGRADED。DEX route price 与 CEX reference display price 分栏，
> 永不互相补值。旧后端只有明确缺新路由时进入兼容层，500 或网络错误不会被隐藏。
> 整个页面没有服务控制和 rollout 写操作，Production 磁盘、数据卷和真实新路由仍要
> 在线验证后才能称 integration-verified。

## 闭卷自检

1. 为什么 retention 没有 error 仍不能自动判 healthy？
2. `{available:false,value:null}` 与 `{available:true,value:0}` 分别表示什么？
3. 为什么进程 Running 不能证明 Binance 或 outbox Healthy？
4. matching、liquidity、transport 三项各自观察什么？
5. 双边订单簿为什么不能直接证明 `system:demo-maker` 正常？
6. 为什么只有 market-data 可以让总状态进入 CACHED？
7. market-data 30 秒和 5 分钟两个边界分别改变什么？
8. 哪些条件才允许 DEMO SNAPSHOT？
9. 为什么组件有 UNKNOWN，而总状态用 DEGRADED 收口？
10. 新路由 500 时为什么禁止回退旧 overview？
11. Route price 与 Reference display price 的来源和用途分别是什么？
12. outbox ready 为什么不能从 matching ready 推断？
13. `statfs("/")` 当前证明了什么，又尚未证明什么？
14. mock Playwright 通过为什么不是 Production integration evidence？
15. System 页面为什么不能提供 restart、rollout 或 retention 手动执行按钮？
