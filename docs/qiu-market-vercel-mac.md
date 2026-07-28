# Qiu Market：Vercel + Mac mini 上线手册

## 运行边界

Vercel 只托管 `frontend` 的 Vue/Vite 静态产物与 `/api/**` 轻量 BFF。Mac mini
运行 API、trading、crawler、worker、dex、PostgreSQL 与 Redis。PostgreSQL、Redis、
HTTP 和 gRPC 都只监听本机；Tailscale Funnel 只把 HTTP `127.0.0.1:9092`
映射为 HTTPS/WSS。

行情 REST 必须经过 Vercel BFF。BFF 对时间戳、方法、路径、查询串和 body SHA-256
做 HMAC；Mac mini 配置 `MARKET_PUBLIC_PROXY_HMAC_SECRET` 后，除 `/healthz` 和持有
一次性 ticket 的交易 WebSocket 外，所有未签名 `/api/**` 请求均返回 401。

## 1. 私有生产配置

```bash
make mac-production-build
```

第一次执行会创建：

```text
~/Library/Application Support/Qiu Market/production.env
```

将其中所有 placeholder 替换掉并保持权限 `0600`。GitHub OAuth App 只允许
`qianqiu0404`，回调使用：

```text
https://qiu-market.vercel.app/api/v1/trading/auth/github/callback
```

如果 Vercel 最终分配了不同 production domain，先同时更新 OAuth callback、
`MARKET_TRADING_ALLOWED_ORIGINS` 与 `MARKET_TRADING_GITHUB_REDIRECT_URL`。

### Preview OAuth 验收

GitHub OAuth App 只登记上面的 Production callback。OAuth App 只支持一个登记
callback；GitHub 的 `redirect_uri` 规则允许使用相同基础主机和端口下的子域，
且路径必须等于或位于登记 callback 路径之下。规则见
[GitHub Authorizing OAuth apps](https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/authorizing-oauth-apps#redirect-urls)。

Preview 与 Production 是不同浏览器 origin，OAuth state 和 session 都是
host-only cookie。因此不能从 Preview 发起登录、却让 GitHub 回到 Production，
也不能把 Production 登录结果当成 Preview 验收。

Gate 2C 使用短维护窗，顺序固定：

1. 将精确 Preview origin 加入 `MARKET_TRADING_ALLOWED_ORIGINS`；
2. 临时把 `MARKET_TRADING_GITHUB_REDIRECT_URL` 改为精确 Preview deployment 的
   `/api/v1/trading/auth/github/callback`，然后只重启 API；
3. 在同一受保护 Preview 完成 GitHub 登录、callback 单次消费、Secure Cookie、
   CSRF/Origin，以及 submit/cancel/fund unknown reconciliation；
4. 保持 Preview origin 仍在 allowlist 时，从该 Preview 调用 logout；只有返回
   204 才认为 PostgreSQL session 已删除；
5. 从 `MARKET_TRADING_ALLOWED_ORIGINS` 移除精确 Preview origin，将 redirect
   恢复为 Production callback，再次重启 API；
6. 使用原 Preview 浏览器上下文验证 session 返回 401，写请求被拒绝。API 重启
   本身不会删除 PostgreSQL session，不能替代第 4 步；
7. promote 已验收的同一 Preview deployment，不重新 build；随后在 Production
   重新登录并做最小写验收。失败时立即回滚 Vercel alias。

Preview redirect 只能来自受管私有配置，不提供客户端可控的 `redirect_uri`。
维护窗内当前旧 Production 的登录不作为可用能力；公共只读行情仍需保持可用。

## 2. 安装常驻服务

先停止开发启动器，避免占用 9092/9094：

```bash
make dev-stop
make mac-production-install
make mac-production-status
```

五个业务角色先以 LaunchAgent 运行；正式的“无人登录也能恢复”形态使用
LaunchDaemon。Guardian、备份和恢复演练可以先安装为当前用户的安全回退，不需要
管理员权限：

```bash
bash ops/macos/manage-user-resilience.sh install
bash ops/macos/manage-user-resilience.sh status
```

完成验收后，由机器所有者输入一次管理员密码，把五个业务角色、专用 tailscaled、
guardian、备份与恢复演练整体提升为 `xiuqiu` 用户身份运行的 LaunchDaemon，并开启
断电自动开机：

```bash
sudo bash ops/macos/manage-system-daemons.sh install
sudo bash ops/macos/manage-system-daemons.sh status
```

脚本不会删除私有配置、备份或日志。日志位于
`~/Library/Application Support/Qiu Market/logs`，Guardian 每 60 秒检查 API、
trading、Funnel、PostgreSQL 与磁盘；数据库异常不会触发共享 PostgreSQL 的盲目重启。

## 3. Tailscale Funnel

本机使用 Homebrew 的开源 Tailscale 1.52+，为 Qiu Market 建立独立的
userspace daemon、state 和 socket；不会复用或停止其它项目的 Tailscale：

```bash
bash ops/macos/manage-funnel.sh install-daemon
bash ops/macos/manage-funnel.sh login
```

`login` 会给出一次性浏览器授权地址。完成后执行：

```bash
bash ops/macos/manage-funnel.sh start
```

脚本按当前 CLI 使用 `tailscale funnel --bg --yes --https=443
http://127.0.0.1:9092`。后台模式会在 Tailscale 重启后恢复。记录实际
`https://<node>.<tailnet>.ts.net`，用它设置 Vercel：

```text
S78_BACKEND_ORIGIN=https://<node>.<tailnet>.ts.net
S78_PROXY_HMAC_SECRET=<与 Mac mini 完全相同>
VITE_TRADING_WS_ORIGIN=https://<node>.<tailnet>.ts.net
```

## 4. Vercel

项目名固定为 `qiu-market`，Root Directory 为 `frontend`，Node 为 24.x，Function
固定运行在 `sfo1`。Preview 与 Production 使用同一组三个后端变量，并设置：

```text
VITE_TRADING_EVENT_MODE=polling
QIU_MARKET_RELEASE_COMMIT=<当前精确 40 字符 Git commit>
```

当前账号环境先使用同源 cursor polling；Vercel WebSocket beta 未完成实测前不宣称
WebSocket 已验收。先从本地精确 commit 构建 Preview，完成页面、API、鉴权和恢复
验收后，再将同一 deployment promote 到 Production，不重新构建。BFF 的完整
upstream 截止时间为 8 秒；仅只读请求可重试一次，交易写请求从不自动重试。
项目必须启用 Vercel 的 System Environment Variables。`VERCEL_DEPLOYMENT_ID` 和
`VERCEL_URL` 由 Vercel 自动提供，BFF 将两者与受管 release commit 作为不可变
provenance 响应头。不要手工把 Production alias 当成 immutable deployment URL。

当前待 OAuth 验收的 protected Preview 为
`dpl_7usLvktVPRCgt8PhoNDSUtd9Zo7e`，immutable URL 是
`https://qiu-market-qnzz1s6a0-qianqiu0404s-projects.vercel.app`，源提交与
provenance commit 都是 `2aa8bda39d2298e1d57886e472f9a090d728f56e`。旧
`dpl_C5k5...` 没有 runtime provenance，只保留为历史证据，不再 promote。

非登录 Gate 2C 可以重复执行：

```bash
bash ops/macos/verify-preview-gate.sh \
  --deployment-id dpl_7usLvktVPRCgt8PhoNDSUtd9Zo7e \
  --deployment-url \
    https://qiu-market-qnzz1s6a0-qianqiu0404s-projects.vercel.app \
  --commit 2aa8bda39d2298e1d57886e472f9a090d728f56e
```

报告保存在私有运行目录
`~/Library/Application Support/Qiu Market/observations/preview-gate-latest.json`。
OAuth 凭据或真实浏览器证据缺失时命令以退出码 2 和
`status=environment-pending` 收口；非 OAuth 安全检查失败时退出码 1；只有全部
证据绑定同一 deployment/commit 时才退出 0。浏览器 OAuth evidence 必须由实际
callback、Cookie、CSRF/Origin、unknown reconcile、logout 与旧 session 401 验收
流程生成，不得手写为通过。

### 受管 OAuth 维护窗口

不要手工编辑 allowlist 后忘记恢复。凭据写入私有 `production.env` 后，先执行只读
预检：

```bash
bash ops/macos/manage-preview-oauth-window.sh preflight \
  --deployment-id dpl_7usLvktVPRCgt8PhoNDSUtd9Zo7e \
  --deployment-url \
    https://qiu-market-qnzz1s6a0-qianqiu0404s-projects.vercel.app \
  --commit 2aa8bda39d2298e1d57886e472f9a090d728f56e
```

预检会复用上面的 immutable Preview gate，并要求：私有文件权限为 `0600/0400`、
OAuth 凭据非 placeholder、本地登录关闭、Secure Cookie 开启、Production callback
和 origin 处于基线。通过后才打开短维护窗：

```bash
bash ops/macos/manage-preview-oauth-window.sh open \
  --deployment-id dpl_7usLvktVPRCgt8PhoNDSUtd9Zo7e \
  --deployment-url \
    https://qiu-market-qnzz1s6a0-qianqiu0404s-projects.vercel.app \
  --commit 2aa8bda39d2298e1d57886e472f9a090d728f56e
```

`open` 在私有目录保存带 SHA-256 的原始环境备份和随机 `window_id`，原子切换两个
配置项，只重启 API，并在线核对 GitHub authorize 的 `redirect_uri` 与 OAuth state
Cookie。任何打开阶段错误或普通信号都会尝试恢复原文件、重启 API 并验证
Production callback。状态可随时只读查看：

```bash
bash ops/macos/manage-preview-oauth-window.sh status
```

浏览器验收必须先生成私有
`observations/preview-oauth-preclose-evidence.json`。它要绑定本次 state 中精确的
`deployment_id`、`deployment_commit`、随机 `window_id` 和 `opened_at`，并证明
callback 单次消费、Secure Cookie、CSRF/Origin 拒绝、submit/cancel/fund unknown
reconcile 及 Preview logout 204。文件必须为 `0600/0400`。随后执行：

```bash
bash ops/macos/manage-preview-oauth-window.sh close
```

`close` 只有在 pre-close evidence 完整时才恢复 Production；恢复后还会再次在线
核对 OAuth capability、固定 Production callback 和 state Cookie。浏览器保留原
Preview context，再验证 session 为 401、写操作被拒绝，最后才生成供
`verify-preview-gate.sh` 使用的完整 `preview-oauth-evidence.json`。因此 pre-close
证据和最终 Gate 证据是两个阶段，不能提前把 stale-session 写成通过。

如果浏览器验收失败、用户中止或无法安全生成 logout 证据，执行：

```bash
bash ops/macos/manage-preview-oauth-window.sh abort
```

`abort` 恢复精确备份并标记 `aborted_without_acceptance`，不会生成或升级任何
Preview acceptance。备份/状态位于私有 Application Support，不含于仓库；凭据和
Secret 不得粘贴到聊天、日志或 evidence JSON。

## 5. 最小安全验收

```bash
curl -i https://<node>.<tailnet>.ts.net/healthz
curl -i -X POST https://<node>.<tailnet>.ts.net/api/v2/get_market_overview \
  -H 'content-type: application/json' \
  --data '{"venue":"all","universe":"provider_union"}'
```

第一条应为 200；第二条绕过 BFF，应为 401。随后检查 Vercel `/api/**` 为 200，
Cookie 带 Secure，错误 Origin、过期 WebSocket ticket 与伪造 CSRF 均被拒绝。
交易仍只使用虚拟资金，不接充值、提现、私钥或实盘。

## 6. 极省空间 K 线治理

保留策略固定为 `1m=7天`、`15m=90天`、`1h=365天`、`1d=永久`。Worker 启动时及
之后每 24 小时执行一次；使用同一 PostgreSQL 专用连接持有 advisory lock，每批最多
删除 10,000 行并设置 5 秒 statement timeout。生产维护命令为：

```bash
bash ops/macos/backup-production.sh full
bash ops/macos/restore-drill.sh
bash ops/macos/optimize-kline-indexes.sh
bash ops/macos/compact-kline-indexes.sh
```

索引优化保留主键、business unique、`sync_seq` 和紧凑
`(interval, open_time)` 索引。只执行普通 `VACUUM (ANALYZE)` 与逐索引
`REINDEX CONCURRENTLY`，不执行 `VACUUM FULL`。

系统页公开数据库大小、K 线 heap/index 大小、各周期最早/最新时间、磁盘余量和保留
任务结果。磁盘低于 25GiB 告警；低于 15GiB 时暂停 crawler/worker/DEX，交易服务仍
保留只读查询，但下单、撤单、虚拟入金与 demo maker fail-closed。

## 7. 24/48/72 小时与 7 天生产观察

生产观察器是只读验收定时器，不是第六个业务服务。LaunchAgent 使用绝对墙钟分钟
调度，每个 UTC 分钟触发一次，而不是从上次结束后相对等待 60 秒。它检查：

- Production 页面和 Vercel BFF；
- Funnel `/healthz` 以及未签名 REST 必须返回 401；
- 虚拟交易状态必须为 `ready`、队列无错误；
- 系统存储状态、磁盘至少 25GiB、保留任务无错误；
- Uniswap/PancakeSwap 的动态 route 数与 reference-only 数；
- PostgreSQL 中同 provider、asset、route、`quote_notional_usd` 的 24/48/72
  小时历史窗口。

历史窗口要求起点位于窗口开始后 30 分钟内、最新观察不超过 10 分钟、最大历史
采样间隔不超过 10 分钟。这个历史采样 SLA 不会放宽公开 route price 的 30 秒
freshness。任一窗口只有在至少一条同 route、同名义金额曲线通过时才标记 `passed`。

安装并立即查看：

```bash
bash ops/macos/manage-observer.sh install
bash ops/macos/manage-observer.sh status
bash ops/macos/summarize-production-slo.sh
```

未 promote 时不要创建正式 epoch。promote 同一 Preview deployment、完成
Production OAuth 和最小写验收后，使用 Vercel 返回的真实 deployment ID、
immutable deployment URL 与完整提交创建 epoch：

```bash
bash ops/macos/manage-acceptance-epoch.sh start \
  --deployment-id dpl_... \
  --deployment-url https://<immutable-deployment>.vercel.app \
  --commit <40-character-release-commit>
```

脚本先请求 Production BFF，只有 provenance 响应头与三个参数完全相符才写入
私有 epoch 文件。同时它会从 PostgreSQL 为 Uniswap 与 PancakeSwap 各选择一条
过去 6 小时最大间隔不超过 600 秒、最近 10 分钟仍新鲜的固定 canary；缺少任意一条
都会拒绝开始。窗口从下一个 UTC 整分钟开始；已有 active epoch 不会被覆盖，需要
放弃时必须显式 `stop`，旧文件会在下一次 start 前归档。

canary 身份由 `asset_guid + route_key + quote_notional_usd + selected_at` 构成，
写入 epoch 后不可静默改变。24/48/72 小时只查询这两个固定身份；窗口未满为
`observing`，窗口已满但起点、最新观察或最大间隔不合格则为 `failed`。更换 route
或名义金额必须停止并新建 epoch，所有正式时间窗从零开始。

证据保存在私有运行目录，不进入仓库：

```text
~/Library/Application Support/Qiu Market/observations/latest.json
~/Library/Application Support/Qiu Market/observations/production-soak.jsonl
~/Library/Application Support/Qiu Market/observations/acceptance-epoch.json
```

`latest.json` 是当前状态，JSONL 是追加式审计历史。正式汇总不是混合部署的滚动
窗口：它只接受 schema v4、epoch ID、Production origin、deployment ID、immutable
URL 和 release commit 全部匹配且 provenance 在线校验通过的样本。旧 schema、
其它 epoch 和其它 release 永远不计入；漏掉的墙钟分钟按失败处理，同一分钟的重复
样本采用“任一失败即失败”。

只有两个固定 DEX canary 的 24/48/72 小时窗口全部通过，并且完整 10,080 个预定
分钟、监控覆盖率和可用率均不低于 99.5%、REST 5xx 低于 0.5%、REST p95 低于
5 秒、单次中断不超过 5 分钟且磁盘始终不少于 25GiB 时，才输出
`production-recommendation`。顶层 `status = observing`
表示当前检查通过但长窗口仍在积累；只有六个 provider/window 组合全部通过才成为
`passed`，当前检查失败则为 `failed`。停止观察器不会删除历史：

```bash
bash ops/macos/manage-observer.sh uninstall
```

同盘备份只能覆盖短期误操作和逻辑恢复，不能覆盖整盘损坏；灾难恢复保持
`risk-accepted / environment-pending`。

交易快照 schema v5 会在启动时把旧 v3/v4 快照升级为紧凑表示：淘汰已终结的
`system:demo-maker` 订单和该系统账户的内存幂等缓存，并把运行时 ledger journal
折叠为每资产双重记账余额 checkpoint。`trading_event_batch`、`trading_order` 和
`trading_ledger_entry` 投影不删除，用户订单、用户幂等、成交、余额、完整账本历史
和六笔开放做市订单全部保留。这个边界用于防止长期 demo maker 把恢复快照无界放大。
