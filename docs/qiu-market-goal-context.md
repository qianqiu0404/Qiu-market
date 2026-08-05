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
最终可 promote 产物。

新的 provenance-enabled Preview 已建立：

- 源提交 `2aa8bda39d2298e1d57886e472f9a090d728f56e`；
- deployment `dpl_7usLvktVPRCgt8PhoNDSUtd9Zo7e`，状态 READY，Function 区域
  `sfo1`；
- immutable URL 为
  `https://qiu-market-qnzz1s6a0-qianqiu0404s-projects.vercel.app`；
- Vercel metadata 中 Git SHA 与 `qiuMarketReleaseCommit` 均为 `2aa8bda...`；
- BFF 运行时响应头在线返回同一 deployment ID、immutable URL 和完整 commit，
  provenance 为 `VERIFIED`；
- 未携带 Vercel Authentication 的静态页与 API 均返回 302；受保护访问下
  `/markets`、`/trade/BTC-USDT`、`/system` 深链接为 200，session 为预期 401；
- 公共读取实测 `MISS → FRESH`，两个响应都携带同一 provenance；
- 40 个前端单测、生产构建、21 个本地 browser contract tests、SLO 夹具与
  diff check 通过；新 Preview 最近一次日志检查只有 200/401，没有 5xx。
- `ops/macos/verify-preview-gate.sh` 已把非登录 Gate 2C 收敛为可重复只读验证：
  deployment READY/Preview、当前 frontend 与 release commit 一致、普通访问被
  Vercel Authentication 拦截、三个 SPA 深链接 200、provenance 三元组一致、
  trading/outbox ready、匿名 session 401、直连 Funnel 401、本地登录关闭；
- 2026-07-28 对 `dpl_7us...` 的真实验证中，上述非 OAuth 检查全部通过，报告按
  证据门禁返回 `environment-pending / github_oauth_private_configuration_missing`，
  没有把 preflight 误标为 Preview Gate 完成。
- `ops/macos/manage-preview-oauth-window.sh` 已把 Gate 2C 的配置切换做成受管状态机：
  `preflight` 只读校验 immutable Preview 与安全基线；`open` 用私有 SHA-256 备份、
  随机 `window_id`、原子环境更新和只重启 API 打开维护窗；打开失败自动恢复；
  `close` 要求绑定本次 window 的真实 pre-close/logout evidence；`abort` 只恢复
  Production 且永不升级验收状态。夹具已覆盖无变更预检、成功打开、重复打开拒绝、
  无 evidence 关闭拒绝、正常关闭、打开后故障回滚和紧急中止。它减少手工配置风险，
  但不替代尚未执行的真实 GitHub OAuth 浏览器验收。
- 最终 Preview gate 只接受 schema v2 浏览器 evidence，并要求它与受管 close 报告
  的 deployment、commit、随机 `window_id`、打开时间和关闭时间完全一致；close
  报告还必须证明 Production 配置已恢复且 OAuth runtime 已复验。旧 schema v1、
  `abort`、错 window 或只完成 logout 未验证 stale session 的证据都保持 pending。
- `frontend/scripts/run-preview-oauth-gate.mjs` 已把真实浏览器验收串成一次受管流程：
  强制清除 Qiu Market Cookie 后重新走 Vercel/GitHub OAuth；callback 首次 302、
  重放 400；校验 `qianqiu0404`、Secure/HttpOnly/SameSite Cookie、CSRF/Origin；
  对 fund/submit/cancel 使用 `route.fetch()` 真实提交后只向页面丢 504，并用原 ID
  重放、余额与订单权威事实证明没有重复执行；logout 后调用 close，再验证旧 session
  和写请求均为 401，最终自动运行 Preview verifier。close 前任一步失败都会 abort。
  纯函数测试已覆盖定点数、规范响应比较、远端挂单价、窗口身份和 0600 原子 evidence；
  真实浏览器流程仍必须等 OAuth 凭据配置后执行，当前不能用这些测试替代外部证据。
- 2026-07-28 对真实私有环境运行受管 `preflight` 返回退出码 2：
  `GitHub OAuth credentials are still missing`；前后环境文件 SHA-256 一致，窗口状态
  仍为 `closed / never_opened`。因此没有打开维护窗、没有重启 API、没有改变
  Production。
- `ops/macos/promote-vercel-release.sh` 已实现但尚未执行同一产物发布门：
  只接受最近 15 分钟内通过的精确 Preview Gate 2C；拒绝活动 OAuth 窗口或活动
  acceptance epoch；记录旧 Production deployment 后只调用 `vercel promote`，
  不 build/deploy；结构检查失败自动回滚并核对 alias；成功后仍停在
  `awaiting-production-auth`，必须提交绑定随机 `promotion_id` 的真实 Production
  登录、CSRF/Origin、同 request ID 最小虚拟写入/重放、账本、状态哈希、logout 和
  stale session 证据才能 confirm；confirm 失败也自动回滚。夹具已覆盖无
  `--execute` 拒绝、成功 promote、缺证据自动回滚、确认、结构失败自动回滚和
  “alias 已切但 CLI 返回失败”的不确定结果回滚，以及显式回滚。
  这只是发布安全能力，当前 OAuth Gate pending 时仍禁止真实 promote。

该 Preview 已通过非登录 smoke，但仍须完成 Gate 2C 的 OAuth 与真实浏览器写操作
验收后，才可 promote。

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
- Preview Gate 验证器只有在真实浏览器流程生成并绑定同一 deployment/commit 的
  私有 OAuth evidence 后才会返回 `preview-gate-passed`；手工合同测试或手写 JSON
  不能替代这份外部验收；
- WebSocket 若环境不支持，则明确使用 cursor polling，不能伪称 WS 已验收；
- 只 promote 已验收的同一 Preview deployment，不重新构建 Production；
- Preview session 不跨域继承，也不能只靠 API 重启使其失效。promote 后必须在
  Production 重新完成一次 OAuth 登录与最小写验收；失败立即回滚 alias，不能沿用
  Preview 登录证据。
- Production 最小写证据会把公开 runtime `state_hash` 与 PostgreSQL 最新 event hash
  直接比较，不再用 sequence 相等后写死 `state_hash_consistent=true`。当前 Production
  Gate 仍只验证正常 fund 与同 ID replay；真实 504 `submitted/unknown` 故障注入和
  浏览器断线换 ticket 后的 cursor reconcile 明确保持 `environment-pending`。

### Gate 3：观测契约与长期证据

状态：`environment-pending`

Production 尚未 promote，所以正式 acceptance epoch 尚未开始。下一步必须：

- 每条样本记录 `acceptance_epoch_id`、Vercel `deployment_commit`、Mac mini
  `runtime_release_commit`、`binary_sha256`、`runtime_bundle_sha256`、`scheduled_at`、
  start/end 和 duration；observer 每分钟重新计算 binary 与 bundle 摘要，并同时核对
  manifest、commit 和 epoch 固定摘要，任一文件损坏或被替换都使该分钟失败；
- BFF 必须从受管 `QIU_MARKET_RELEASE_COMMIT` 与 Vercel 自动提供的
  `VERCEL_DEPLOYMENT_ID`、`VERCEL_URL` 输出 release provenance；启动 epoch 前，
  管理脚本会从 Production 在线核对响应头与精确 deployment ID、URL、commit，
  不匹配则拒绝开始；
- 使用绝对墙钟分钟调度，不用会随执行时间漂移的相对间隔；
- 只统计 schema v7、`acceptance_eligible=true` 且 epoch、Production origin、
  deployment ID、immutable URL、前端 commit、Mac mini runtime commit、binary SHA-256
  与 runtime bundle SHA-256 全部一致的样本；acceptance epoch schema v4 与 transport
  smoke schema v2 在开始时固定同一组摘要。旧 schema 样本继续用于诊断，但永不计入新的正式 7 天窗口；缺失分钟按失败
  处理，同一分钟的任一失败样本不能被后一个成功样本覆盖；
- recovery status JSON 自身的 `production_origin / deployment_id / deployment_url /
  release_commit / source_digest` 必须同时匹配 acceptance epoch、Vercel provenance
  headers 和 Mac mini 当前 managed binary SHA-256；缺失或漂移都会使当分钟失败且
  不具备 `acceptance_eligible`；
- 交易可用分钟必须同时证明 runner/outbox ready，以及公开 recovery endpoint 为
  `writable`、`writes_enabled=true`、`continuity_uncertain=false`。进程重启后停在
  `transport_warmup` 会记为失败；Guardian 只告警并等待 operator proof，不用重复
  重启制造新的 recovery epoch；
- 每家 DEX 固定同 route、同名义金额 canary，route/notional 改变即重新计时；
- 依次完成 30 分钟、24 小时、DEX 24/48/72 小时、连续 7 天；
- 7 天覆盖率和可用率均不低于 99.5%，REST 5xx 低于 0.5%，p95 低于
  5 秒，无超过 5 分钟的单次中断，磁盘始终不低于 25 GiB；
- route price 与 composite/reference display price 永久分栏，50 个资产可显示
  不等于 50 条链上 route 可执行。

固定 canary 的实现口径：

- epoch start 会从 PostgreSQL 各选一条过去 6 小时连续、最近 10 分钟仍新鲜且
  最大间隔不超过 600 秒的 Uniswap/PancakeSwap route；任一 provider 没有候选时
  拒绝启动；
- `asset_guid + route_key + quote_notional_usd + selected_at` 写入私有 epoch
  文件，24/48/72 小时都只查询这两个身份，不再“每次任取一个当前最优 route”；
- 窗口未走完标记 `observing`；走完后起点、freshness 或 gap 任一不满足即
  `failed`，不能继续显示 pending；
- canary 不会在 active epoch 中静默替换。需要换 route/notional 时必须 stop 并
  创建新 epoch，因此 DEX 窗口与完整 7 天计时都会从零开始。

当前未启动 epoch 的动态诊断仍是 `pending`：最近快照中 Uniswap 最佳完整 24 小时
group 的最大间隔约 695 秒，超过 600 秒；PancakeSwap 虽有最大间隔约 254 秒的旧
group，但 freshness 与连续性没有在同一 group 上同时满足。这些只说明旧动态历史
尚不能过门，不会被算入正式 canary 证据。

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
