# Qiu Market 后续目标 Context

更新日期：2026-07-28
产品边界：单用户、虚拟资金、单一 BTC/USDT 现货纵切片。

## 后续开发方向

Qiu Market 的下一阶段不是继续堆交易品种，而是把已有系统做成一份可解释、
可恢复、可外部验收的交易基础设施作品。它服务于交易所钱包、CEX 后端和
数字资产基础设施的求职叙事，重点证明：

1. 行情身份、route price、reference/display price、freshness、coverage 和
   quality 的可信语义；
2. 订单幂等、`submitted/unknown`、partial fill/cancel race 和权威事实查询；
3. available/held、不可变分录、双重记账、手续费与 reconciliation；
4. event stream、snapshot、outbox、cursor reconnect 和确定性恢复；
5. 单机生产发布、回滚、故障隔离和证据门禁。

以下内容冻结，除非建立新的明确目标：量化策略实验室、多市场、杠杆、永续、
期权、真实交易所下单、充值、提现、私钥和真实资金。

## 执行顺序

任何 Gate 都不能用后一个 Gate 的演示替代，也不能因为代码存在就升级为外部
验收完成。

### Gate 0：Review 基线

状态：`integration-verified`

- 基线提交 `7df90013b1c7114d759c16e804e124e62f6b07e8`；
- OAuth callback 不自动重试；
- submit/cancel/fund 都持久保存 operation/request ID，并支持 unknown reconcile；
- Guardian 检查交易与 outbox 状态；
- restore drill 启动真实交易进程并验证恢复 hash 与账本；
- outbox publisher、event feed、checkpoint 和清理语义已实现；
- Go、race、vet、前端测试/构建、PostgreSQL 恢复演练已通过。

这只证明本地代码与集成，不证明当前生产正在运行该提交。

### Gate 1：Mac mini 可回滚发布

状态：`integration-verified (Mac mini backend)`

完成证据：

- 后端精确基线为
  `96a81ff0bce981b50c1280a9f737561dbfbdbf24`；
- fresh backup、临时库真实恢复、迁移校验和、版本化 binary 与受管切换均通过；
- 恢复到 sequence `101861` 后状态 hash 一致，ledger imbalance 为零；
- `trading_event_feed`、`trading_outbox_checkpoint` 和 publisher 已进入 ready；
- 2026-07-28 Preview 验收快照中，交易服务为 ready、sequence `101933`，
  outbox checkpoint 同步到 `101933`；
- 数据盘验收时可用空间约 35.2 GB。

这证明当前 Mac mini 后端发布与恢复链路，不证明多机高可用，也不替代后续
完整时间窗。

### Gate 2：同产物 Preview → Production

状态：

- `integration-verified (protected Preview)`
- `environment-pending (OAuth + Production)`

已完成：

- 前端/BFF 精确提交为
  `19928325f9a1104d1dd3505a004dffb9fe52a714`；
- Preview deployment 为
  `dpl_C5k5JWv2mEXz7ko75rQS3nTbdrSG`，状态 READY，Function 区域 `sfo1`；
- metadata 中 Git SHA 与 `qiuMarketReleaseCommit` 都等于 `1992832...`；
- 36 个前端单测、生产构建、18 个本地 mocked browser contract tests 与
  diff check 通过；
- Preview 验证了 SPA 深链接、安全响应头、HMAC BFF、未登录 session/write
  返回 401、直连 Funnel 返回 401；
- Runtime Cache 实测 `MISS → STALE(1ms) → FRESH(0ms)`，后台刷新后 age
  重新变小；
- Insights 八个 provider 能局部完成；DEX coverage 明确标记 `displayed`，
  Route Monitor 只接受 60 秒内 `dex_route_available`；
- Doris 停用被显示为显式 capability error，没有再发送必然失败的 momentum
  请求；
- 一次快速跨页验收曾记录 `/get_market_overview` 504；随后三个强制冷
  overview 均为 200（约 1.8–2.7 秒），复验 Insights 无 4xx/5xx，最近
  5 分钟 Preview Function 日志未显示 5xx。该证据只算 smoke，不算 soak。

为使后续 7 天证据绑定不可变产物，BFF 已增加服务端生成的 release provenance
响应头。旧 `dpl_C5k5...` 没有这些响应头，因此保留为历史 Preview 证据，但不再是
最终可 promote 产物。Gate 2C 必须从包含 provenance 的新精确提交构建一次新的
protected Preview，完成同样验收后只 promote 该 deployment。

当前阻塞：

- `github_oauth_enabled=false`、`local_login_enabled=false`；
- 因而尚未真实验证 OAuth callback、Secure session、CSRF，以及登录后的
  submit/cancel/fund unknown reconciliation；
- 在这些证据完成前，禁止 promote `qiu-market.vercel.app`，更不能重新构建
  一个不同 Production 产物。
- 当前 Production alias 仍指向旧 deployment
  `dpl_Hy7YyJJG6bA4SU1D8RGE6TS2NYmk`（Git SHA `17c48f6...`）；本轮没有
  改动 Production。

剩余完成标准：

- 创建 GitHub OAuth App，callback 固定为
  `https://qiu-market.vercel.app/api/v1/trading/auth/github/callback`，仅允许
  `qianqiu0404`；
- Preview 与 Production 使用 host-only OAuth state/session cookie，不能把
  Production callback 的登录结果当成 Preview 验收。Gate 2C 必须开一个短维护窗：
  暂时把后端 redirect 切到精确 Preview callback，并把该 Preview origin 加入
  allowlist；完成 Preview 的 OAuth、CSRF、Origin、Secure Cookie 与 callback
  单次执行后，先从 Preview 调用 logout 删除 PostgreSQL session，再移除 Preview
  origin、恢复 Production redirect 并重启 API；旧 Preview session 必须返回 401，
  写请求必须被拒绝；
- 当前 GitHub OAuth App 只登记一个 Production callback。GitHub 允许显式
  `redirect_uri` 使用同一基础主机和端口下的子域与匹配路径，因此上述 Preview
  callback 必须由后端固定生成，不能接受客户端任意传入；
- 真实浏览器验证 submit/cancel/fund 的 unknown reconciliation；
- WebSocket 若环境不支持，则明确使用 cursor polling，不能伪称 WS 已验收；
- 只 promote 已验收的同一 Preview deployment，不重新构建 Production；
- Preview session 不跨域继承，也不能只靠 API 重启使其失效。promote 后必须在
  Production 重新完成一次 OAuth 登录与最小写验收；失败立即回滚 alias，不能沿用
  Preview 登录证据。

### Gate 3：观测契约与长期证据

状态：`environment-pending`

Production 尚未 promote，所以正式 acceptance epoch 尚未开始。下一步必须：

- 每条样本记录 `acceptance_epoch_id`、`deployment_commit`、`scheduled_at`、
  start/end 和 duration；
- BFF 必须从受管 `QIU_MARKET_RELEASE_COMMIT` 与 Vercel 自动提供的
  `VERCEL_DEPLOYMENT_ID`、`VERCEL_URL` 输出 release provenance；启动 epoch 前，
  管理脚本会从 Production 在线核对响应头与精确 deployment ID、URL、commit，
  不匹配则拒绝开始；
- 使用绝对墙钟分钟调度，不用会随执行时间漂移的相对间隔；
- 只统计 schema v4、`acceptance_eligible=true` 且 epoch、Production origin、
  deployment ID、immutable URL 与 commit 全部一致的样本。旧 713 条 schema v3
  样本继续用于诊断，但永不计入正式 7 天窗口；缺失分钟按失败处理，同一分钟的
  任一失败样本不能被后一个成功样本覆盖；
- 每家 DEX 固定同 route、同名义金额 canary，route/notional 改变即重新计时；
- 依次完成 30 分钟、24 小时、DEX 24/48/72 小时、连续 7 天；
- 7 天覆盖率和可用率均不低于 99.5%，REST 5xx 低于 0.5%，p95 低于
  5 秒，无超过 5 分钟的单次中断，磁盘始终不低于 25 GiB；
- route price 与 composite/reference display price 永久分栏，50 个资产可显示
  不等于 50 条链上 route 可执行。

只有完整证据期通过，才标记
`production-recommendation (availability)`。同盘备份仍只能标记
`risk-accepted / environment-pending`，不能宣称多机高可用或灾备完成。

### Gate 4：学习资产化

状态：`implemented / mastery-pending`

已产出三份材料，不扩产品：

1. [`unknown outcome reconciliation` ADR](adr/0001-unknown-outcome-reconciliation.md)；
2. [CEX order unknown 与钱包 `broadcast_unknown` 对照测试矩阵](unknown-outcome-test-matrix.md)；
3. [90 秒闭卷口述](unknown-outcome-90s.md)。

共同点：请求超时不等于未执行，必须保留原 idempotency key 并查询权威事实。
区别：CEX 查询 venue order/trade/ledger；钱包查询 mempool、receipt、canonical
chain，并额外处理 finality 与 reorg。

材料存在不等于已经掌握。只有不看稿完成口述、解释一个失败恢复，并能从测试
入口指出证据，才把个人学习状态升级为 `mastered`。

## 下一步开发顺序

1. 用户提供 GitHub OAuth Client ID/Secret 后，只完成 Gate 2C；
2. OAuth 与虚拟资金写 E2E 通过后，promote 新的同一 provenance-enabled
   deployment，不重新 build；
3. 从 Production promotion 时刻创建新的 acceptance epoch，依次完成
   30 分钟、24 小时、DEX 24/48/72 小时和连续 7 天；
4. 若时间窗暴露问题，只修可信语义、恢复、对账、传输与观测；仍冻结策略、
   多市场、衍生品和真实资金；
5. 全部门禁完成后，把当前分支作为可冻结稳定基线，再另开明确目标。

## 证据等级

- `implemented`：代码或迁移存在；
- `build-verified`：构建与相应测试真实运行通过；
- `integration-verified`：真实依赖下完成端到端与恢复验证；
- `environment-pending`：需要 OAuth、Vercel、Funnel、时间窗口或外部环境；
- `production-recommendation`：完整生产门禁与观察期均通过。

没有命令输出、deployment ID、数据库 proof 或完整时间窗，就不升级状态。
