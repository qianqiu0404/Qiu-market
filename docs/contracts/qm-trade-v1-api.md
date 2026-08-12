# Qiu Market Trade Product V1 API Schema

本文件是 `PRD-QM-TRADE-001` 的冻结浏览器/API 契约。它定义字段和语义，不表示对应实现
已经存在。状态必须按具体 endpoint 分别标记为 `existing`、`planned-P0` 或 `planned-P1`。

## 1. 共同约定

- Base path：`/api/v1/trading`；
- 唯一市场：`BTC-USDT`；普通私有接口不接受客户端 `account_id`；
- JSON 中 price、quantity、amount、fee、quote budget 和 64-bit sequence 均为十进制字符串；
  `event_index`、`timeline_index`、page `limit`、batch count 等有明确小上限的 32-bit 控制值
  才使用 JSON number；
- 私有响应设置 `Cache-Control: no-store`；
- 所有 cursor 是 opaque base64url，最大 512 字节；
- cursor 使用服务端持久化私有 HMAC key 和 `qiu-market/trading-cursor/v1` domain separation
  签名；token 不保存明文账户 ID，只保存账户绑定摘要；
- token 固定包含 `schema_version`、`key_id`、`issued_at`、`expires_at`、market、账户绑定摘要、
  filters、sort direction 和最后排序键；有效期固定 24 小时；
- 当前 key 由仅存在于服务端私有运行环境的 `MARKET_TRADING_CURSOR_HMAC_CURRENT` 提供，
  格式为 `key_id:base64url-secret`；启用 P0 分页接口时缺失或无效必须启动失败，禁止运行时随机生成；
- 可选 `MARKET_TRADING_CURSOR_HMAC_PREVIOUS` 只用于验证轮换前 cursor；current 用于签发，
  current+previous 用于验证；previous 至少保留 24 小时后才能移除；
- page `limit` 默认 50，最小 1，最大 100；
- 响应中 `next_cursor=""` 表示没有下一页；
- cursor 绑定 schema version、market、account、filters、sort direction 和最后排序键；
- filter 变化必须从空 cursor 重新查询；
- cursor 校验失败或过期返回 HTTP 400 `invalid_cursor`，账户不匹配同样返回该错误以避免泄露；
- 时间使用 UTC RFC3339Nano；
- `request_id`、`client_order_id`、`operation_id` 最大 128 字节且只允许可打印 ASCII；
- 写请求沿用现有 CSRF、Origin、session、rate limit 和 submitted/unknown 规则。

### 1.1 通用分页响应

```json
{
  "items": [],
  "next_cursor": "opaque-or-empty"
}
```

实际 endpoint 使用业务数组名 `orders`、`trades`、`events` 或 `entries`，不使用 `items`。

### 1.2 通用错误

```json
{
  "code": "invalid_cursor",
  "message": "bounded diagnostic message"
}
```

稳定错误 code：

| HTTP | code | 含义 |
|---:|---|---|
| 400 | `validation_failed` | 字段或市场规则无效 |
| 400 | `invalid_cursor` | cursor 损坏、过期、filter/账户不匹配 |
| 401 | `authentication_required` | 无有效 session |
| 403 | `csrf_failed` / `origin_rejected` | 写请求安全校验失败 |
| 404 | `order_not_found` / `operation_not_found` | 当前账户下不存在 |
| 409 | `idempotency_conflict` | 同身份配不同 payload/filter |
| 409 | `reconcile_pending` | 客户端应先完成原操作核对 |
| 429 | `rate_limited` | 写接口限流 |
| 503 | `recovery_in_progress` | 权威恢复门禁关闭写入 |
| 503 | `trading_write_paused` | 磁盘或运行时门禁关闭写入 |
| 503 | `liquidity_paused` | 本机虚拟流动性尚未形成安全双边报价；只阻断新 Submit |
| 502/503/504 | `backend_unavailable` / `backend_timeout` | 结果可能 unknown，按接口语义核对 |

## 2. 现有写入、能力与状态契约（existing，V1 保持兼容）

### 2.1 `POST /orders`

```json
{
  "client_order_id": "web-uuid",
  "side": "buy",
  "type": "limit",
  "time_in_force": "gtc",
  "post_only": false,
  "price": "64000.00",
  "quantity": "0.01000000",
  "quote_budget": ""
}
```

- Limit 使用 price + quantity；
- Market Buy 使用 quote_budget；Market Sell 使用 quantity；
- Market 强制 IOC 且不能 Post Only；
- `client_order_id` 同时是 submit 的 request identity。

### 2.2 `POST /orders/{order_id}/cancel`

```json
{ "request_id": "cancel-uuid" }
```

### 2.3 `POST /admin/fund`

```json
{
  "request_id": "fund-uuid",
  "account_id": "optional-admin-target",
  "asset": "USDT",
  "amount": "10000.00"
}
```

该接口只允许 admin，V1 UI 移至 System/Trading Admin。

### 2.4 `GET /auth/capabilities`（existing，T1 扩展）

既有能力字段保持兼容，并增加：

```json
{
  "practice_mode_enabled": true,
  "starter_funds_enabled": true,
  "virtual_liquidity_enabled": true
}
```

- 三个字段都是服务端运行边界的事实，不由前端配置猜测；
- `practice_mode_enabled` 只有在 loopback、local auth、OAuth 关闭、非 Secure Cookie、
  loopback Origin 和独立 Practice PostgreSQL 全部成立时才可为 `true`；
- `starter_funds_enabled` 只表示本机会话可以走固定 Starter funding 流程；
- `virtual_liquidity_enabled` 只表示该能力已配置，不表示当前六档报价已经安全可用。

### 2.5 `GET /markets/BTC-USDT/status`（existing，T1 扩展）

状态响应增加：

```json
{
  "virtual_liquidity": {
    "provider": "Qiu Virtual Liquidity",
    "state": "active",
    "reason": "two-sided virtual liquidity is active",
    "bid_levels": 3,
    "ask_levels": 3,
    "reference_observed_at": "2026-08-13T10:00:00Z",
    "last_refresh_at": "2026-08-13T10:00:05Z"
  }
}
```

`state` 只允许 `disabled | recovering | active | paused`。新 Submit 还必须同时通过原有
runner/recovery/disk/session/CSRF/Origin 门；`active` 不能绕过这些门。虚拟流动性不是
`active` 时，普通账户 Submit 返回 503 `liquidity_paused`，查询和本人撤单仍保留；maker
自己的 Post Only 维护命令不通过这个外部门再次拦截。

### 2.6 `GET /account/funding/{request_id}`（existing，T1 新增）

该私有只读接口只使用当前 HttpOnly session 的账户；请求不能携带 `account_id`：

```json
{
  "market_id": "BTC-USDT",
  "request_id": "starter-v1-usdt",
  "funding_event_id": "12:0",
  "sequence": "12",
  "asset": "USDT",
  "amount": "10000",
  "projection_result": "applied",
  "ledger_balanced": true,
  "occurred_at": "2026-08-13T10:00:00Z"
}
```

不存在或属于其他账户都返回 404 `funding_request_not_found`，避免账户枚举。响应从 funding
event、payload identity 和对平账本重验得到，不能根据余额变化合成。Starter 资金固定为两步：

| 顺序 | request ID | 固定 payload |
|---:|---|---|
| 1 | `starter-v1-usdt` | 当前 session 账户、`USDT`、`10000` |
| 2 | `starter-v1-btc` | 当前 session 账户、`BTC`、`0.1` |

每一步都执行 `GET -> found 且 payload/ledger 一致则完成；404 且所有写门开放才 POST 原 ID`。
超时后再次 GET；只有权威事实仍不存在时，才允许使用同一个 ID 重放。两步不是一个数据库事务，
也不能用随机 ID、余额或 UI 本地状态冒充恢复进度。手动 admin 虚拟入金继续接受其它合法
request ID；仅上述两个保留 ID 被固定为当前 session 和固定 payload。

## 3. 订单分页（planned-P0）

### `GET /orders`

Query：

| 参数 | 类型 | 默认 | 约束 |
|---|---|---|---|
| `cursor` | string | 空 | opaque |
| `limit` | integer | 50 | 1..100 |
| `scope` | string | `all` | P0：`all,open,history` |
| `status` | string | 空 | P1：`open,partially_filled,filled,canceled,rejected` |
| `side` | string | 空 | P1：`buy,sell` |
| `type` | string | 空 | P1：`limit,market` |

P0 排序固定为不可变的 `(accepted_sequence DESC, order_id DESC)`，cursor 使用两者作为
keyset，且绑定完整 filter。新订单不会移动已开始的翻页窗口，既有订单的状态变化也不会改变
排序键。`scope/status` 是 live projection 过滤，不承诺 point-in-time 历史快照；若状态在翻页
期间变化，订单可以合法进入或离开该过滤集合，但同一稳定键不得重复返回。`scope=all`
要求新订单并发写入时不跳项、不重复；`open/history/status` 只保证稳定键不重复，完整集合
通过刷新首屏获得。响应：

P0 scope 集合固定为：`open={open,partially_filled}`，
`history={filled,canceled,rejected}`，`all=open union history`。领域内部瞬时 `received` 不进入
订单查询投影；它只可能作为订单时间线中 `order_accepted` 节点的 lifecycle status 出现。

兼容与存储要求：

- migration 为 `trading_order` 增加实体列 `accepted_sequence`、`created_at`、`updated_at`；
  `accepted_sequence` 从订单 accepted event/payload 回填，时间从对应 event batch 的权威
  `created_at` 回填，全部校验非空后再建立约束；
- projection 在事件批次同一事务内写这些列；重建投影必须得到相同值；
- 账户分页索引固定以
  `(market_id, account_id, accepted_sequence DESC, order_id DESC)` 为前缀；
- 旧 `open_only` query 保留一个发布周期：单独出现时 `true -> scope=open`、
  `false -> scope=all`；与 `scope` 同时出现返回 400 `validation_failed`，不猜优先级；
- 新分页实现禁止继续使用可变的 `updated_sequence` 作为 cursor 排序键。

```json
{
  "orders": [
    {
      "id": "order-id",
      "client_order_id": "client-id",
      "market_id": "BTC-USDT",
      "side": "buy",
      "type": "limit",
      "time_in_force": "gtc",
      "post_only": false,
      "price": "64000.00",
      "original_quantity": "0.01000000",
      "remaining_quantity": "0.00400000",
      "filled_quantity": "0.00600000",
      "average_fill_price": "63990.00",
      "original_quote_budget": "",
      "remaining_quote_budget": "",
      "spent_quote": "383.940000",
      "held_asset": "USDT",
      "held_amount": "256.000000",
      "status": "partially_filled",
      "accepted_sequence": "120001",
      "last_sequence": "120004",
      "reject_reason": "",
      "created_at": "2026-08-05T00:00:00Z",
      "updated_at": "2026-08-05T00:00:03Z"
    }
  ],
  "next_cursor": "opaque-or-empty"
}
```

`average_fill_price` 的 atom 值固定为
`CheckedMulDivFloor(spent_quote_atoms, base_scale, filled_quantity_atoms)`；BTC/USDT 以 quote
atom 精度（6 位小数）格式化，不再次对齐 `0.01 USDT` tick。零成交返回空字符串。它只是
成交加权均价，不是 PnL 成本。
`created_at/updated_at` 来自 event batch 投影，不从浏览器时钟生成。

## 4. 成交分页（planned-P0）

### `GET /account/trades`

P0 Query：`cursor`、`limit`；P1 增加 `side=buy|sell`。排序固定为
`(sequence DESC,event_index DESC,trade_id DESC)`。

内部接口必须拆分：

- 现有 `ListTrades` 继续作为公开 market trade feed，只返回匿名化市场成交事实；
  Transport/API P0 实现合并后其 `account_id` 必须为空，非空请求返回 invalid argument，
  防止继续走私有旧路径；纯结构 contract scaffold 不提前改变现有 endpoint 行为，该隐私修复
  在 Transport/API P0 合并前保持 `pending`；
- 新增 account-scoped `ListAccountTrades` RPC 和独立 DTO，`account_id` 只由 HTTP session
  注入，不接受浏览器传值；
- 新私有 `GET /account/trades` 只能调用 `ListAccountTrades`，禁止把包含
  maker/taker/buyer/seller
  两侧账户与两侧费用的现有 `Trade` Proto 直接序列化给浏览器；
- 发布采用两阶段兼容：第一阶段后端先增加新 endpoint，同时旧私有 `GET /trades` 保留一个
  发布周期，但只能返回 safe legacy DTO：所有账户字段为空、对手方 order ID 为空、只保留
  当前账户自己的 order ID 与 buyer/seller fee（另一侧 fee 为空）；第二阶段 Vercel 前端切到
  `/account/trades`；确认生产前端不再调用旧路径后，下一发布才删除旧私有 endpoint；
- 公开 `GET /markets/BTC-USDT/trades` 的匿名 DTO 和语义始终保持兼容。

```json
{
  "trades": [
    {
      "id": "trade-id",
      "market_id": "BTC-USDT",
      "order_id": "my-order-id",
      "side": "buy",
      "liquidity_role": "taker",
      "price": "63990.00",
      "quantity": "0.00600000",
      "quote_amount": "383.940000",
      "fee_asset": "BTC",
      "fee_amount": "0.00001200",
      "fee_rate_bps": "20",
      "sequence": "120004",
      "event_index": 3,
      "occurred_at": "2026-08-05T00:00:03Z"
    }
  ],
  "next_cursor": "opaque-or-empty"
}
```

私有成交 DTO 以当前账户视角返回单一 `order_id/side/liquidity_role/fee_*`，不向用户暴露其他
账户 ID。公开 market trades 继续使用匿名化现有 DTO。

## 5. 订单生命周期（planned-P0）

### `GET /orders/{order_id}/events`

Query：`cursor`、`limit`。排序固定为
`(sequence ASC,event_index ASC,timeline_index ASC)`，从最早事件向后读取。

数据流固定为：

```text
trading_event_batch + immutable result/journal
  -> rebuildable trading_order_event projection
  -> GetOrderEvents
  -> browser timeline
```

禁止在请求时仅用 `trading_order` + `trading_trade` 猜历史。

```json
{
  "events": [
    {
      "event_id": "120004:3:1",
      "market_id": "BTC-USDT",
      "order_id": "order-id",
      "sequence": "120004",
      "event_index": 3,
      "timeline_index": 1,
      "source_kind": "event",
      "type": "trade_executed",
      "status": "partially_filled",
      "quantity": "0.00600000",
      "price": "63990.00",
      "remaining_quantity": "0.00400000",
      "remaining_quote_budget": "",
      "trade_id": "trade-id",
      "fee": {
        "asset": "BTC",
        "amount": "0.00001200",
        "rate_bps": "20",
        "role": "taker"
      },
      "balance_effects": [
        {
          "asset": "USDT",
          "bucket": "held",
          "amount": "-383.940000",
          "reason": "trade_settlement",
          "transaction_id": "settle-id"
        },
        {
          "asset": "BTC",
          "bucket": "available",
          "amount": "0.00598800",
          "reason": "trade_settlement",
          "transaction_id": "settle-id"
        }
      ],
      "reason": "",
      "occurred_at": "2026-08-05T00:00:03Z"
    }
  ],
  "next_cursor": "opaque-or-empty"
}
```

允许的顶层 `type`：`order_accepted`、`order_rejected`、`order_rested`、
`trade_executed`、`order_filled`、`order_canceled`、`cancel_rejected`、
`self_trade_prevented`。hold、release 和 fee 不是伪造的新领域事件：它们作为权威 journal
和 Trade fee 对应的 `balance_effects/fee` 嵌入生命周期节点。浏览器超时等本地事实只能以
`client_observation` 单独显示，不得进入服务端权威事件数组。

确定性投影规则固定为：

1. 按 `(sequence,event_index)` 重放 event batch；每个订单从 accepted command/event 中取得
   原始 quantity、quote budget、side 和 type；
2. 单订单事件只产生该订单一行，`timeline_index=0`；
3. `trade_executed` 同时产生 taker 与 maker 的账户视角行：taker 固定
   `timeline_index=0`，maker 固定 `timeline_index=1`；两行都引用同一 trade ID，但只返回
   当前 session account 自己的 order、side、role 和 fee；
4. taker order 来自 event `order_id`，maker order 来自 `event.trade.maker_order_id`；maker 与
   taker 的 remaining quantity 都由“accepted 原量减该订单累计 fill”重建，禁止把只属于
   taker 的 event `remaining` 复制给 maker；
5. Market Buy 的 `remaining_quantity` 为空字符串，`remaining_quote_budget` 等于原始 quote
   budget 减累计 quote amount；其他订单的 `remaining_quote_budget` 为空字符串；新事件 schema
   同步固化该字段，旧事件由上述重放算法回算；
6. journal 只按以下不可变 ID/reference 关联并嵌入，不按时间或金额猜测：
   `hold:<sequence>` + `order-hold:<order_id>` 关联 accepted；
   `trade:<trade_id>` + `matched-trade:<trade_id>` 关联双方 trade；
   `maker-release:<sequence>:<fill_ordinal>` + `maker-rounding-release:<maker_order_id>`
   关联该 maker trade；`release:<sequence>` + `order-release:<taker_order_id>` 关联 taker
   的最终 rested/filled/canceled 节点；`cancel-release:<sequence>` +
   `order-cancel:<order_id>` 关联 user cancel；
7. fee 只取 Trade 中当前账户的 buyer/seller fee 和 Maker/Taker role；当前账本的用户 credit
   已是扣费净额，不再推导第二笔 user fee entry；
8. 找不到唯一 order/trade/reference、一个 journal 重复绑定或重放后 remaining 为负时，
   projection 构建失败并停止推进 checkpoint，禁止跳过或生成近似时间线。

`source_kind` 在 P0 权威行固定为 `event`；journal 仅作为该 event row 的可审计 enrichment。
`sequence/event_index/timeline_index` 就是源 cursor，必须原样保留。

## 6. 账本流水（planned-P0）

### `GET /ledger/entries`

Query：

| 参数 | 约束 |
|---|---|
| `cursor` | opaque |
| `limit` | 1..100 |
| `asset` | `all,BTC,USDT` |
| `reason` | `all,virtual_fund,order_hold,order_release,trade_settlement,other` |

排序固定为 `(sequence DESC,transaction_id DESC,entry_index DESC)`。

```json
{
  "entries": [
    {
      "entry_id": "120004:settle-id:2",
      "market_id": "BTC-USDT",
      "sequence": "120004",
      "transaction_id": "settle-id",
      "entry_index": 2,
      "asset": "USDT",
      "bucket": "held",
      "amount": "-383.940000",
      "reason": "trade_settlement",
      "reference": "matched-trade:trade-id",
      "order_id": "order-id",
      "trade_id": "trade-id",
      "occurred_at": "2026-08-05T00:00:03Z"
    }
  ],
  "next_cursor": "opaque-or-empty"
}
```

- 只返回属于 session account 的 user available/held entry；
- 不暴露 treasury、platform 或其他用户账户名称；
- 这是账户流水，不声称单个响应页自身借贷平衡；完整双重记账证明仍在 System/审计工具；
- `amount` 为带符号十进制字符串：正数表示该用户 bucket 增加，负数表示减少；不使用
  `debit/credit`，避免把展示方向伪装成会计借贷方向；
- 当前结算把买/卖手续费直接体现在用户获得资产的净 credit，并把费用 credit 到平台账户；
  因此账户流水没有独立 `reason=fee` 行。权威费用从私有 trade DTO 和订单时间线展示，禁止
  从净额重复扣减或虚构 user fee entry；
- `reason` 只能由已冻结的 transaction ID/reference 前缀映射；未知前缀返回 `other`，不得按
  金额或相邻时间推断。

## 7. 账户摘要（planned-P1）

### `GET /account/summary`

```json
{
  "market_id": "BTC-USDT",
  "quote_asset": "USDT",
  "assets": [
    {
      "asset": "BTC",
      "available": "0.09400000",
      "held": "0.00400000",
      "total": "0.09800000",
      "reference_valuation": {
        "available": true,
        "value_quote": "6271.020000",
        "kind": "composite_reference",
        "source": "cex_composite",
        "observed_at": "2026-08-05T00:00:05Z",
        "freshness": "fresh"
      }
    }
  ],
  "accumulated_fees": [
    { "asset": "BTC", "amount": "0.00001200" },
    { "asset": "USDT", "amount": "0.064000" }
  ],
  "updated_sequence": "120004"
}
```

- 估值使用带来源、时间和 freshness 的可信展示参考；不可用时
  `available=false,value_quote=""`；
- `value_quote` 是该资产 `total = available + held` 的估值，不是仅 available；加法必须检查
  int64 溢出；BTC 值固定为
  `CheckedMulDivFloor(total_base_atoms, reference_price_quote_atoms, base_scale)` 并按 USDT
  6 位精度格式化；溢出返回该资产 `available=false`，不得截断；
- BTC `kind=composite_reference`、`source=cex_composite`，与现有可信综合参考术语一致；参考样本
  `observed_at` 超过 30 秒即 `freshness=stale,available=false,value_quote=""`，不得显示为 live；
- 以 USDT 为 quote 的 `value_quote` 对 USDT 本身使用 `quote-unit-identity`，含义是
  `1 USDT balance unit = 1 USDT quote unit`，不代表 USDT 永久等于 1 USD；BTC 等非 quote
  资产才使用带来源、时间和 freshness 的市场参考；
- USDT 的 `source=quote_unit_identity`，`observed_at` 使用该 balance projection 的
  `updated_at`，`freshness=not_applicable`；它不是价格源新鲜度证明；
- `accumulated_fees` 只按当前账户在 immutable Trade 中的 buyer/seller fee、按原始 fee asset
  分别求和；不折算 USDT、不从净到账重复推导；
- balances、fees、reference 和 `updated_sequence` 在同一个 PostgreSQL repeatable-read
  snapshot 中读取；`updated_sequence` 是该 snapshot 可见的最大 trading event sequence；
- 不返回 cost basis、realized/unrealized PnL。

## 8. Cancel All（planned-P1）

### 8.1 `POST /orders/cancel-all`

```json
{
  "operation_id": "cancel-all-uuid",
  "side": "all"
}
```

- filter 仅支持 `side=all|buy|sell`；
- 首次接受时在 batch control transaction 中固定当时投影可见的 open/partially_filled
  order ID 清单；“固定清单”不锁订单，也不阻止随后成交；
- 子 request ID 使用 `qiu-market/cancel-all-child/v1` domain，依次对 market ID、account ID、
  operation ID、order ID 做 `uint32 big-endian length || UTF-8 bytes` 编码后计算 SHA-256；
  输出固定为 `ca1_` 加 digest 前 24 bytes 的无 padding base64url（192-bit）；
- 同 operation ID + 同 filter 返回原批次；不同 filter 返回 409；
- 返回 202 表示仍在处理，200 表示批次已经终态；
- 批次不保证全局原子性。

```json
{
  "operation_id": "cancel-all-uuid",
  "market_id": "BTC-USDT",
  "state": "partial",
  "requested_count": 3,
  "terminal_count": 3,
  "canceled_count": 2,
  "already_terminal_count": 1,
  "failed_count": 0,
  "items": [
    {
      "order_id": "order-1",
      "child_request_id": "ca-derived-id",
      "state": "canceled",
      "final_order_status": "canceled",
      "last_error_code": ""
    },
    {
      "order_id": "order-2",
      "child_request_id": "ca-derived-id-2",
      "state": "already_terminal",
      "final_order_status": "filled",
      "last_error_code": ""
    }
  ],
  "created_at": "2026-08-05T00:00:00Z",
  "updated_at": "2026-08-05T00:00:05Z"
}
```

批次 `state`：`pending|running|complete|partial|failed`：

- `complete`：所有子项终态，且每项为 `canceled` 或 `already_terminal`；
- `partial`：所有子项终态，至少一个成功关闭且至少一个 `rejected`；
- `failed`：所有子项都得到权威永久 `rejected`，且没有 `canceled/already_terminal`；
- 任一子项仍为 `pending|unknown` 时批次保持 `running`，不得提前宣称终态。

逐单 `state`：`pending|canceled|already_terminal|rejected|unknown`。

状态转换与恢复固定为：

- 空 eligible list 直接 `complete`，所有 count 为 0；
- `pending -> canceled|already_terminal|rejected|unknown`；`unknown` 只能用原 child request ID
  查询权威订单/结果，确认后转为前三种终态之一；
- `backend_timeout/backend_unavailable/recovery_in_progress` 属于 unknown，不是 rejected；后台
  采用持久化 attempt counter 最多自动核对 3 次，达到上限仍保持 unknown/running 并告警，
  不生成新 ID；后续人工或定时恢复仍只能沿用同一 ID；
- 只有权威返回 `validation_failed/idempotency_conflict/permission_denied` 才是永久 rejected；
  eligible order 后来 filled/canceled/closed 一律为 `already_terminal`；投影中凭空消失属于
  integrity error，保持 unknown 并阻止批次宣称完成；
- 批次和 item 的每次转换、attempt、last error 与 next reconcile time 都在同一 batch control
  事务持久化，重启不重置计数。
- batch control record 缺失、损坏或状态转换不合法时返回 5xx integrity error，进入 manual
  review；此时不得构造 DTO 或伪造 `failed` 终态。

### 8.2 `GET /operations/cancel-all/{operation_id}`

返回相同批次 DTO，是超时、重启和浏览器刷新后的唯一权威查询入口。若仍有 `unknown`，
服务端只使用原 child request ID 查询/重放；客户端不得创建新 operation ID。

实现需要持久化、可恢复的 batch control record；它只保存批次意图和子项进度，不保存或
替代订单、余额、账本和撮合状态。每个订单的最终状态仍由交易事件真值决定。

## 9. 前端计算契约（planned-P0）

下单预览必须使用 decimal/integer-safe 工具，不使用 JS `number` 直接计算资金：

- Limit notional：`floor(price * quantity)`，显示 quote precision；
- Buy hold：服务端规则的 ceil；
- Market Buy：输入 quote budget，不承诺确定 BTC 数量；
- Market Sell：输入 BTC quantity；
- Maker/Taker 费用显示为区间或按用户明确选择的最坏角色预估，不能保证 Post Only 之外
  的最终角色；
- 25/50/75/100% 向下对齐 step 且不得超过可用余额；买方费从获得的 BTC 扣除、卖方费
  从获得的 USDT 扣除，因此不得错误地从下单资产再重复预留费用；买单 held 仍按服务端
  ceil 规则预览并保留必要舍入余量；
- 服务端响应是权威结果，预览差异必须能解释为价格、流动性、角色或 floor/ceil。

## 10. 契约测试

P0 实现 PR 必须新增：

1. Proto/HTTP JSON 字段和 decimal string contract；
2. cursor encode/decode、tamper、wrong account/filter、重复/缺口；
3. PostgreSQL keyset pagination：`scope=all` 在并发新订单写入时不跳项、不重复；
   动态 `open/history/status` 集合只保证不重复，并要求刷新首屏取得最新完整集合；
4. order lifecycle projection 从 event+journal 重建后与 live projection 相同；
5. 账本 API 不泄露其他账户和系统 bucket；
6. 浏览器对每个 P0 稳定 error code 的 en/zh-CN 显示；
7. `system:e2e-taker` 只能在显式测试开关、loopback 和一次性测试数据库同时成立时使用。

仅在对应 P1 endpoint 实现时才成为门禁：

1. account summary 在 reference stale/unavailable 时不输出伪 live 估值；
2. Cancel All 同 ID、冲突 payload、部分成交竞态、unknown、重启恢复；
3. P1 筛选和导出不能改变 P0 cursor/DTO 语义。

## 11. 变更控制

合并 `PRD-QM-TRADE-001` 后，本文件即为 V1 实现边界。实现中发现字段不足时：

1. 先停止修改共享 Proto/DTO；
2. 提交独立契约变更 PR，说明问题、替代方案、兼容成本和迁移；
3. 主 Agent Review 并更新 PRD；
4. 合并后所有实现分支 rebase；
5. 禁止某个 Agent 在自己的实现分支私自改变已冻结字段或语义。
