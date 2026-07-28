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

状态：`in-progress`

完成标准：

- 干净 Git SHA 构建为版本化、带 SHA-256 manifest 的 release；
- 同一 binary 通过全量测试和 fresh backup 临时库恢复；
- 数据盘部署前至少有 `35,000,000,000` 可用字节；
- 应用 `2026082300025.sql` 后，迁移账本 checksum 正确；
- `trading_event_feed` 与 `trading_outbox_checkpoint` 存在；
- publisher 为 ready，unpublished backlog 收敛，checkpoint 追上事件流；
- 只切换受管 symlink，只重启 trading/API；
- 失败自动恢复旧 binary，显式 rollback 命令可再次验证；
- 状态 hash 一致，每个资产 ledger 净额为零。

### Gate 2：同产物 Preview → Production

状态：`environment-pending`

完成标准：

- Vercel Preview 明确绑定 Gate 1 的 Git SHA 与 deployment ID；
- Preview 验证 SPA、BFF、HMAC、CSRF、Origin、Cookie、OAuth 单次 callback；
- 真实浏览器验证 submit/cancel/fund 的 unknown reconciliation；
- sfo1 Runtime Cache 跨冷实例语义有证据，私有交易接口始终 `no-store`；
- WebSocket 若环境不支持，则明确使用 cursor polling，不能伪称 WS 已验收；
- 只 promote 已验收的同一 Preview deployment，不重新构建 Production。

### Gate 3：观测契约与长期证据

状态：`environment-pending`

先修 observer，再重新开始 acceptance epoch：

- 每条样本记录 `acceptance_epoch_id`、`deployment_commit`、`scheduled_at`、
  start/end 和 duration；
- 使用绝对墙钟分钟调度，不用会随执行时间漂移的相对间隔；
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

状态：`pending`

只产出三份材料，不扩产品：

1. `unknown outcome reconciliation` ADR；
2. CEX order unknown 与钱包 `broadcast_unknown` 对照测试矩阵；
3. 90 秒闭卷口述。

共同点：请求超时不等于未执行，必须保留原 idempotency key 并查询权威事实。
区别：CEX 查询 venue order/trade/ledger；钱包查询 mempool、receipt、canonical
chain，并额外处理 finality 与 reorg。

## 证据等级

- `implemented`：代码或迁移存在；
- `build-verified`：构建与相应测试真实运行通过；
- `integration-verified`：真实依赖下完成端到端与恢复验证；
- `environment-pending`：需要 OAuth、Vercel、Funnel、时间窗口或外部环境；
- `production-recommendation`：完整生产门禁与观察期均通过。

没有命令输出、deployment ID、数据库 proof 或完整时间窗，就不升级状态。
