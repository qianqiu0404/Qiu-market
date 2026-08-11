# Qiu Market：Vercel + Mac mini 上线手册

## 运行边界

Vercel 只托管 `frontend` 的 Vue/Vite 静态产物与 `/api/**` 轻量 BFF。Mac mini
运行 API、trading、crawler、worker、dex、PostgreSQL 与 Redis。PostgreSQL、Redis、
HTTP 和 gRPC 都只监听本机；Tailscale Funnel 只把 HTTP `127.0.0.1:9092`
映射为 HTTPS/WSS。

行情 REST 必须经过 Vercel BFF。BFF 对时间戳、方法、路径、查询串和 body SHA-256
做 HMAC；Mac mini 配置 `MARKET_PUBLIC_PROXY_HMAC_SECRET` 后，除 `/healthz` 和持有
一次性 ticket 的交易 WebSocket 外，所有未签名 `/api/**` 请求均返回 401。

### BFF HMAC 与重放边界

每次 BFF → Mac mini 请求使用以下逐行 canonical 字符串；字段顺序是稳定契约：

```text
unix_timestamp_seconds
32_lowercase_hex_nonce
UPPERCASE_METHOD
exact_path_and_query
lowercase_body_sha256
```

nonce 是每次尝试重新生成的 16-byte 随机值。同一个只读请求如果因 502/503/504
重试，第二次必须使用新 nonce、重新计算签名；交易写请求仍不重试。Mac mini 先验证
时间窗、nonce 格式、1 MiB body、digest 和签名，最后才把 nonce 原子写入有界内存
replay cache。记录保留到该签名时间戳越过 ±30 秒验收窗；同 nonce 的并发或顺序重放
都返回 401。cache 上限为 16,384，满载时 fail closed，不逐出尚有效 nonce。认证失败、
上游失败和 5xx 都保持 `Cache-Control: no-store`。

路径也在签名前 fail closed：Vercel route capture 必须是单个、最多 1,024 bytes 的
相对路径；控制字符、反斜线、内嵌 query/hash、原始或 percent-decoded `.` / `..`
segment 都被拒绝。BFF 只在受管的精确 backend origin 下重建 `/api/**`，不接受请求
提供 host。Go router 继续只挂载显式产品 route。BFF 不制造宽泛 CORS：浏览器使用
同源 `/api`，写请求的 `Origin` 原样转发；Trading gateway 只接受
`MARKET_TRADING_ALLOWED_ORIGINS` 中规范化后完全相等的 origin，并同时要求 session
与 CSRF。没有 `*` 回退。

控制流按以下顺序执行：

1. `frontend/api/proxy.ts` 验证 exact origin/path，计算 body digest，并为每次 attempt
   生成 nonce 和 HMAC；
2. Tailscale Funnel 只转发 HTTPS/WSS 到 `127.0.0.1:9092`，不终止应用鉴权；
3. `services/http/public_proxy_auth.go` 验证 canonical 内容并原子占用 nonce；
4. `services/http/api.go` 的显式 route 才进入市场、研究、质量或模拟交易 handler；
5. `ops/macos/production-lib.sh`、`guardian.sh` 与 `script/verify-local.sh` 用同一
   canonical 契约做受管探针。

这里的 **nonce** 是“一次性票号”：时间戳只说明票何时有效，nonce 让同一张票不能
在有效期内用第二次。**replay cache** 是“已验票清单”，只存有界 nonce 与过期时间，
不存请求 body、Cookie 或 secret。选择 bounded in-process cache，是因为当前 authority
只有一个 API 进程且 Funnel 单入口；相比引入 Redis，它没有新的共享故障域。代价是 API
重启会清空清单，所以 timestamp 窗仍必须很短；未来多 API replica 前必须改为共享的
原子 nonce store，不能把当前实现横向复制后宣称仍防重放。

失败与恢复：无效、重复或容量耗尽都统一返回无细节 401，避免形成签名 oracle；BFF
生成新 nonce 后可重试明确安全的 GET。API 重启后 replay cache 为空，但超过 30 秒的
旧签名仍因时间窗被拒绝。运维 signer 缺 nonce 会立即 401，Guardian 不得继续沿用旧
四字段 canonical。

这个五字段协议与旧四字段协议不兼容：新 BFF 请求旧 API，或旧 BFF 请求新 API，都会
按预期返回 401。D1 Preview 必须让 BFF 与独立 local API stack 来自同一个精确 Git SHA；
本轮禁止 promote，也禁止单边升级当前 Mac authority 或 Production BFF。未来生产切换
必须先并行启动新版本 API endpoint，再构建并验收匹配 SHA 的 BFF，最后在同一受控窗口
切换 endpoint 与 Vercel deployment；如果平台无法提供这个并行窗口，只能安排明确的
原子维护切换。失败回滚必须同时恢复同版本的 API+BFF pair，不能把旧 BFF 指回新 API
或把新 BFF 指回旧 API。当前旧 Production deployment 仅能作为前端壳回滚点，已知
backend offline 时不能宣称业务恢复。

Owner 60 秒说明：浏览器永远只访问 Vercel 同源 `/api`；BFF 把 exact RequestURI、body
digest、时间戳和一次性 nonce 一起签名；Mac mini 在执行业务 handler 前验签并原子登记
nonce；只读重试换 nonce，写请求不重试；Origin/CSRF 仍由 Trading gateway 独立校验；
cache 满或验证不确定时拒绝，不降级放行。

闭卷检查：

- 为什么仅有 ±30 秒 timestamp 仍不能阻止窗口内重放？
- 为什么一次只读 retry 必须换 nonce，而不能只换签名？
- replay cache 满时为什么不能逐出一个仍有效的旧 nonce？
- 哪个入口负责 exact CORS，哪个入口负责 HMAC，它们为何不能互相替代？
- API 横向扩为两个 replica 前，nonce store 必须发生什么变化？

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

### D1 live 行情日志轮转与受控恢复

D1 live 行情 lane 与上面的通用 production runtime 分开管理。它的固定根目录是
`~/Library/Application Support/Qiu Market/d1-candidate`，现役角色是
`com.qiu-market.live.crawler`、`dex`、`worker` 和 `api-tunnel`。安装轮转器使用：

```bash
bash ops/macos/manage-live-log-rotation.sh install
bash ops/macos/manage-live-log-rotation.sh status
```

`com.qiu-market.live.log-rotation` 每 60 秒只检查 D1 `logs/live-*.out.log` 与
`logs/live-*.err.log`。单文件超过 50 MiB 时做 copy-truncate：归档保留最近 50 MiB，
原 inode 留最近 10 MiB，并最多保留两代归档。保留原 inode 是因为 launchd 子进程已打开
stdout/stderr；直接 rename 会让进程继续写旧文件。被拒绝的方案是递归扫描整个
`d1-candidate`，因为 evidence、Vercel 构建日志和私有 runtime 文件不属于 live role 日志。
plist、状态目录、当前日志和归档均收紧为 owner-only；脚本不 source worktree `.env`，也不
读取 `config/database.env` 或 secrets。`uninstall` 只卸载轮转 LaunchAgent，保留日志，因而
可以回滚。

受控 restart 前先记录四个 live label、D1 stack/API PID、`/healthz`、overview 的
asset/priced/unpriced 和最新 freshness。切换 exact-SHA binary 后只用 launchd 的
`kickstart -k`/现有 D1 restart gate 重启对应只读角色与 API stack；tunnel 必须等 loopback
health 恢复后再连接。恢复验收要求新的 PID、`/healthz=200`、overview/dashboard 对账、
freshness 在有界时间内回到 fresh，并跨至少两个 30 秒 reconcile 周期确认零 catalog 不再
产生 `unsupported Scan ... <nil> into *time.Time`。Binance 451、Bybit 403 仍是
`unavailable in this deployment`，不能为了消除日志而绕过限制。

Owner 60 秒说明：轮转器只看 D1 live 的八类 stdout/stderr 文件，每分钟有界
copy-truncate，保持 launchd 已打开的 inode；配置和 secret 不进入 argv、日志或工作树。
重启前保存 PID/health/freshness，exact-SHA 切换失败就恢复旧 selector/LaunchAgent；恢复后
既要健康 200，也要等两个 reconcile 周期验证没有重复内部错误。

闭卷检查：

- 为什么 D1 live 不能接到旧的 `com.qiumarket.*` runtime label？
- 为什么轮转必须保留 inode，且不能递归扫描整个 candidate？
- uninstall 会删除哪些东西、明确保留哪些东西？
- 为什么 `healthz=200` 仍不能替代 freshness 与 30 秒 reconcile 观察？
- restart 失败时 exact selector、PID 和 tunnel 应如何回滚？

### R2E 行情读取合同与原子切换

R2E 开始前的 2026-08-12 只读基线如下。表内不含 secret、共享 token 或私有配置值；它用于解释为什么旧页面虽然可用，却不能证明前后端来自同一个行情发布环境。

| 环境 | 前端 / source | backend 路径 | 数据模式与合同 | 只读结果 |
| --- | --- | --- | --- | --- |
| 旧 Production | `dpl_xUvW5eVJd7FLM4pr8D77e4jtR7na` / `65420c1c8be474ccc02ae4740c540cf9bc5e5cf6` | live tunnel 当时直连 `127.0.0.1:18080` | 旧 BFF，不要求 R2E backend/edge contract | `106` assets，`61` priced，`45` unavailable |
| 当前 live runtime | `market-services.a7adc11b` / `a7adc11b142ec0c08d615616ec6d31204d699d83` | API `18080` | `qiu.d1.active-release.v1`，缺 `data_mode/provider_policy/snapshot_schema` | 真 provider，只读行情；不能被新 BFF 视为合格 R2E authority |
| 旧 replay | source `4b2a278d77b0f6aaae5c5ea103db1e65ed1d2441` | 旧 `18084` body-rewrite proxy | `d1_deterministic_replay`，`live_providers=false` | 只可作旧恢复证据，禁止进入 Preview/Production 覆盖率 |
| PR #15 旧 Preview | `dpl_FdC1A59j1dqNyqcBEMTNUhgMWNWJ` / `a7adc11b142ec0c08d615616ec6d31204d699d83` | 与当时 live API 相连 | R2I envelope，无共享 snapshot/edge contract | 同为 `106 / 61 / 45`，但 overview/dashboard 可跨查询时刻 |

R2E 的公开 market read 需要两层身份同时成立：backend 返回 exact
`X-Qiu-Market-Backend-Release-Commit`、`X-Qiu-Market-Data-Mode: live`、
`X-Qiu-Market-Provider-Policy: restricted-no-bypass.v1`、contract/snapshot schema
和本次 request nonce；pure frontdoor 再返回同 SHA 的 edge release、`live` mode 与
`qiu.market-edge-contract.v1`。BFF 的期望 SHA 来自已验证的 immutable Vercel
deployment provenance。任一字段缺失或漂移、旧 `X-Qiu-Data-Mode` replay、direct
`18080`、nonce 不一致时统一 `502 backend_contract_mismatch`、`no-store`，且禁止读取
last-good stale cache。ticks 也走这项校验，但不进入公共 body cache。

overview 新建 snapshot 时，API 在一次 read-only `REPEATABLE READ` PostgreSQL
事务中用同一 `CURRENT_TIMESTAMP` 读取 overview summary、CoinGecko global metric 和
完整 dashboard 行。Redis 只保存一个完整 JSON 值：所有 API instance 对同一 15 秒
bucket 推导相同 ID，以 Lua `SET NX` 竞争，输家丢弃自己的数据库读取并返回 winner；
没有可与 payload 分离漂移的 current pointer。snapshot 绑定 release/data mode/provider
policy/schema，TTL 为 5 分钟、namespace 最多 64 项、当前 All 上限 106 行。读取时重新
核对唯一非空 asset ID、逐行 freshness 与总数，All 必须满足：

```text
106 = fresh + stale + unavailable
priced = displayed = fresh + stale
unpriced = unavailable
single-venue priced + multi-venue priced = priced
```

overview 可以由 BFF fresh-cache 15 秒，并在回源普通故障时最多 stale 240 秒；该窗口
小于 Redis 300 秒 authority TTL。带显式 `snapshot_id` 的 dashboard 不缓存，所有分页、
搜索和排序都从同一冻结值读取；unknown/expired/wrong-venue 返回 409，Redis 缺失或损坏
返回 503。snapshot body 不再按 transport `Age` 改写 freshness，`Age/Warning` 只描述
传输缓存。Markets 只呈现与 dashboard 同 ID 的 overview；慢搜索显示 loading，成功的
零行才显示 empty，`published_asset_count=0` 仍显示当前部署不可用。

live selector 升级为 `qiu.d1.active-release.v2`，除 binary、attestation 与 gate digest
外，还固定 exact commit、`live` data mode、restricted provider policy、contract/
snapshot/edge schema、generation/Redis owner token，以及唯一 tunnel target
`http://127.0.0.1:18084`。`live-api-tunnel.sh` 明确拒绝 direct `18080`；`18084` 只能运行
repo-tracked pure passthrough frontdoor，禁止旧 replay body rewrite。

受控切换入口是：

```bash
bash ops/macos/live-cutover.sh preflight /private/path/to/candidate-active-release.json
bash ops/macos/live-cutover.sh cutover /private/path/to/candidate-active-release.json --execute
```

执行顺序固定为：暂停 tunnel；备份旧 selector/generation/wrappers；安装候选 selector；
写 `ready=false` 保持 frontdoor drain；重启 stack 与 crawler/dex/worker；用 exact-SHA
`market-services contract-probe --secret-file <0600 path>` 验证 direct backend 200 与 edge
drain 503/no-store；claim 新 Redis generation owner；原子提交无 tunnel 的 PID set 与
`ready=true`；验证 edge 200；恢复 tunnel；最后补写包含 tunnel 的完整 PID set。probe
只把 secret 文件路径放入 argv，secret 本身由 Go 进程读取，不进入 argv、日志或工作树。

旧 Redis generation 只有同时满足以下三项才删除自己的 owner/state/lock key：旧
generation 中记录的全部 PID 已停止；6389 listener 不再是旧 Redis PID；Redis owner key
仍精确等于旧 owner token。脚本不执行 `FLUSH*`，旧 cleanup 也不能删除新 PID file。
任何阶段失败都会先暂停 tunnel，再尝试恢复旧 selector、committed generation、四个
wrapper、只读 roles 与 tunnel；只有整条恢复链通过后才记录 `rolled-back`，并按 owner
token 清掉失败候选的 generation key。如果恢复链自身失败，脚本保持 tunnel 暂停、记录
`rollback-failed` 并保留候选 Redis owner/state 作为人工恢复证据，不会虚报已回滚。
`pidfile-cleanup` 旁路只在 `/tmp/qiu-market-live-cutover.*` 隔离 test mode 可用。

主要入口：

- `services/http/market_read_contract.go`：backend release/data/provider/schema/nonce 合同；
- `database/market_aggregation.go` 与 `services/http/service/market_snapshot.go`：PG 冻结读取、Redis authority 与分页；
- `frontend/api/proxy.ts`：Vercel BFF backend+edge fail-closed、cache 与 snapshot envelope 验证；
- `cmd/market-frontdoor/main.go`：固定 `18084 -> 18080` pure passthrough 与 drain；
- `ops/macos/live-release-selector.sh`、`live-cutover.sh`：私有 selector、PID/Redis owner、原子切换与回滚。

Owner 60 秒说明：浏览器先取 overview snapshot ID，再用它读取 dashboard；PostgreSQL
一次事务冻结 106 行，Redis 只接受同 bucket 的一个完整 winner，BFF 同时验证 Vercel
期望 SHA、backend 和 edge 合同。live tunnel 永远只到 pure frontdoor 18084；切换时先
drain，再验候选 backend，提交 ready 后才恢复 tunnel。restricted provider 仍保持 451/
403 unavailable，不加入覆盖率；失败时只允许完整恢复旧 release pair，不拼接新旧组件；
恢复自身失败则保持 tunnel 暂停并留存 `rollback-failed` 证据。

闭卷检查：

- 为什么只验证 backend 自报 SHA，仍不能排除旧 replay 或 direct `18080`？
- 为什么 snapshot 必须保存完整值，而不能分开写 payload 与 current pointer？
- 为什么带显式 snapshot ID 的 dashboard 不进入 BFF stale cache？
- Redis cleanup 的 PID、listener、owner token 三门分别防哪一种竞态？
- cutover 为什么必须先看到 edge drain 503，再提交 `ready=true` 和恢复 tunnel？

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

### 受保护 Preview 的 edge generation 与 drain

短期 D1 Preview 可以用 loopback front door 和 ephemeral Quick Tunnel 做非生产验收，
但它不是 Tailscale Funnel 的生产替代品。Tunnel 只有在 PostgreSQL、隔离 Redis、
trading、gRPC、API 和 front door 六个组件同时通过后才可启动。`preview-edge-gate.sh`
把这一批 PID、端口、启动时间、binary SHA、source commit、预期 Quick Tunnel hostname
和验证时间写成一个权限 `0600` 的 **committed generation**；只有整份 JSON 通过校验后
才原子替换旧文件。Stack 每 5 秒用同一个 generation ID 续租，front door 在租约超过
15 秒、文件缺失或 drain 存在时直接返回 typed 503、`Retry-After: 1` 和
`Cache-Control: no-store`。

Gate 不信任 manifest 的自报身份：每次 check 都从 `ps` 重算启动时间、从进程 text
executable 重算 SHA，并要求六个互异 PID 分别持有固定角色端口且只监听
`127.0.0.1`。Source commit 必须是预期的完整 40 位 commit。外侧 probe 还要求一个权限
`0600` 的 deployment attestation；其 Qiu project ID、deployment ID、immutable URL、
READY state、`target:null` 和 git/source/release commit 必须与 gate 配置逐字段相同。
Production target、空 commit 或只靠响应自报的 deployment 都会 fail closed。

这里的 **generation** 是“一次完整开机批次的盖章清单”：六个组件不是各自说“我好
了”，而是等全部一致后一次盖章。**drain** 是“门口先挂暂停营业牌”：重启前先原子
写 drain 并撤销 generation，外部流量继续到独立 front door，但只得到明确 503，不能
落到正在退出的 API。Front door 与业务 stack 分属不同 launchd 生命周期；它只在
generation/drain/transport-connect failure 时生成 503。已经到达 API 的合法 HTTP
401、403、业务 4xx 和业务 5xx 保持原状态与 body，不会被门面改写为“暂不可用”。

受控重启顺序固定：

1. 原子写 drain、撤销 committed generation；
2. 从 loopback 和受保护的 immutable Preview 各验证 503/no-store；
3. 只重启 Redis/trading/gRPC/API stack，front door 与 tunnel 保持运行；
4. 六组件重新通过后提交新 generation；
5. 删除 drain，验证同一 Preview 的关键 BFF API 恢复 200/no-store。

初次安装顺序是 front door → stack → tunnel → guardian。Tunnel 进程启动前必须消费
同一份新鲜 generation；运行后固定检查受管 hostname。Quick Tunnel 即使使用同一份
受限 credential 在实测中复用了 hostname，也仍标记 `ephemeral=true`；hostname 漂移
必须 fail closed，禁止静默改向。正式 Production 仍应使用受管稳定域名与明确 SLA。

外侧 guardian 不只请求 tunnel `/healthz`。它使用单独的权限 `0600` Vercel automation
bypass file，请求精确 immutable deployment hostname 下固定的
`/api/v1/trading/auth/capabilities` 与 `/api/v1/data-quality/summary`，并校验 HTTP 200、
JSON schema、D1 安全 flags 和 no-store。Secret 通过 curl config file descriptor 注入，
不进入 argv、日志或 evidence；Production alias、任意 path、宽泛 host 和权限错误都
fail closed。该 secret 只属于 Qiu project，必须有独立 rotate/revoke 流程；不得复用
其它站点或项目凭据。

选择独立 front door 而不是“重启时直接停 tunnel”，是为了让计划内恢复呈现可解释的
503，而不是 Cloudflare 502/530。成本是多一个常驻 loopback 进程和 generation 续租；
收益是 tunnel 与业务进程不再同时消失。选择 committed single-file JSON 而不是六个
松散 ready 文件，是为了避免 guardian 读到半套新旧 PID。若 front door 自身退出，
tunnel supervisor 必须立即摘除 tunnel，避免继续把 connection refused 暴露成 502。

失败与恢复矩阵：

| 事件 | 外部行为 | 自动恢复边界 |
|---|---|---|
| generation 缺失、过期或 PID/listener 漂移 | typed 503 + no-store | stack 重新整体验真并提交新 generation |
| planned restart | drain 期间持续 typed 503 | 新 generation 提交后显式 resume |
| API transport connect failure | front door typed 503，不生成 502 | stack 恢复；不得改写合法上游响应 |
| front door 退出 | tunnel supervisor 摘除 edge | launchd 恢复 front door 后重新满足启动门 |
| Quick Tunnel hostname 漂移 | fail closed，标记 ephemeral | operator 重新绑定 Preview backend 后再验收 |
| Preview BFF/HMAC/schema 异常 | 外侧 guardian 失败 | 不以 tunnel `/healthz` 绿替代 BFF 修复 |

验证入口：

```bash
bash ops/macos/test-preview-edge-gate.sh
```

该 fixture 锁定 partial/stale generation、PID/listener drift、drain 503/no-store、精确
Preview host/path/schema、bypass ACL 和 secret 不进入 argv/log。它是 build-verified；
真实 stop → drain → restart → resume 必须由独立测试者在受保护 Preview 上执行后才能
标记 integration-verified。

Owner 60 秒说明：先让六个本机组件共同提交一个持续续租的 generation，初次通过后才
启动 tunnel；front door 独立常驻，generation 不可信时返回 503/no-store。重启先 drain，
所以 tunnel 不会把 API connection refused 暴露成 502；恢复后提交新 generation 再
resume。Guardian 同时检查本机、Quick Tunnel hostname 和受保护 Preview 的两个真实
BFF API。Quick Tunnel 始终是 ephemeral，hostname 变化会停门而不是静默漂移。

闭卷检查：

- 为什么六个松散的 ready 文件会允许半新半旧进程组合？
- 为什么 transport connect failure 可改为 503，但合法上游 401/403 不能改？
- 为什么业务 stack 重启时 front door 和 tunnel 必须保留？
- 为什么 `/healthz` 200 不能替代受保护 Preview BFF schema 探针？
- 同一 Quick Tunnel credential 曾复用 hostname，为什么仍必须标记 ephemeral？

2026-07-28 的 protected Preview、deployment `dpl_7usLvktVPRCgt8PhoNDSUtd9Zo7e`
与 commit `2aa8bda39d2298e1d57886e472f9a090d728f56e` 仅是历史归档证据，禁止直接
复用或 promote；更早的 `dpl_C5k5...` 同样只保留为历史证据。每次发布都必须从当前
不可变 Preview/release 状态重新取得 deployment ID、immutable URL 和精确 40 字符
commit，并在以下命令中替换占位符，绝不能复制历史值。

非登录 Gate 2C 可以重复执行：

```bash
bash ops/macos/verify-preview-gate.sh \
  --deployment-id <candidate-deployment-id> \
  --deployment-url https://<candidate-immutable-url> \
  --commit <candidate-40-character-commit>
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
  --deployment-id <candidate-deployment-id> \
  --deployment-url https://<candidate-immutable-url> \
  --commit <candidate-40-character-commit>
```

预检会复用上面的 immutable Preview gate，并要求：私有文件权限为 `0600/0400`、
OAuth 凭据非 placeholder、本地登录关闭、Secure Cookie 开启、Production callback
和 origin 处于基线。通过后才打开短维护窗：

```bash
bash ops/macos/manage-preview-oauth-window.sh open \
  --deployment-id <candidate-deployment-id> \
  --deployment-url https://<candidate-immutable-url> \
  --commit <candidate-40-character-commit>
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
`verify-preview-gate.sh` 使用的 schema v2 `preview-oauth-evidence.json`。最终证据
必须逐项匹配私有 `preview-oauth-window/last.json` 中的 deployment、commit、
`window_id`、`window_opened_at` 与 `completed_at`，而且 last 状态必须是
`closed_after_verified_logout`；`abort` 报告或旧窗口 evidence 都不能通过。因此
pre-close 证据和最终 Gate 证据是两个阶段，不能提前把 stale-session 写成通过。

如果浏览器验收失败、用户中止或无法安全生成 logout 证据，执行：

```bash
bash ops/macos/manage-preview-oauth-window.sh abort
```

`abort` 恢复精确备份并标记 `aborted_without_acceptance`，不会生成或升级任何
Preview acceptance。备份/状态位于私有 Application Support，不含于仓库；凭据和
Secret 不得粘贴到聊天、日志或 evidence JSON。

### 真实浏览器 Gate 2C 采集器

凭据就绪且 `open` 成功后，不手写 pre-close/final evidence，运行：

```bash
cd frontend
npm run gate:preview-oauth -- \
  --deployment-id <candidate-deployment-id> \
  --deployment-url https://<candidate-immutable-url> \
  --commit <candidate-40-character-commit>
```

脚本打开真实 Chrome，并给 Vercel/GitHub 登录各最多 5 分钟。专用浏览器 profile
位于私有 `Application Support/Qiu Market/browser`，只用于复用 Vercel/GitHub
登录状态；每次运行会先清除全部 `s78_trading_*` Cookie，强制 Qiu Market 重新走
OAuth callback。浏览器中如果出现 Vercel Authentication 或 GitHub 授权页面，由
用户本人完成，不把密码、2FA、OAuth code 或 Cookie 写入 evidence/日志。

采集器必须在线证明：

- callback 首次为 302，保存的同一 callback URL 再放一次为 400；
- Trade 页面有真实 `BTC / USDT` 内容、没有 Vite/框架错误层和 console error，并把
  截图以 `0600` 保存到私有 observations；
- principal 精确为 `github:qianqiu0404`，session/CSRF Cookie 为 Secure、
  SameSite=Strict，session 还是 HttpOnly；
- 缺 CSRF 和恶意 Origin 都返回 403；
- 先使用虚拟资金创建一笔远离市场且 Post Only 的卖单；
- 对 fund、submit、cancel 分别让 Playwright `route.fetch()` 把请求真实发送到
  Vercel BFF，确认上游 2xx 后只向页面返回人工 504，制造“服务端已提交但浏览器
  不知道”的 unknown；
- 三种写操作都使用原 request/client order ID 重放；响应必须与已提交结果一致，
  fund 余额只增加一次，订单权威列表为 open，cancel 后权威订单为 canceled；
- Preview logout 为 204。

以上通过后脚本原子写入权限 `0600` 的 pre-close evidence，直接调用受管 `close`
恢复 Production，再用原浏览器上下文验证 session 401 和写请求 401，生成 schema
v2 final evidence，最后调用 `verify-preview-gate.sh`。任一步在 close 前失败、浏览器
被关闭或登录超时，脚本都会调用 `abort` 恢复 Production；不会把失败流程写成通过。
所有资金仍是 Qiu Market 虚拟账本，不涉及链上或交易所真实资金。

采集器的纯函数边界可独立复验：

```bash
cd frontend
npm run test:gate-lib
```

### 同一构建产物的 Promotion Gate

只有 `verify-preview-gate.sh` 在最近 15 分钟内对精确 deployment/commit 返回
`preview-gate-passed`，而且 OAuth 维护窗口已关闭、正式 acceptance epoch 尚未开始，
才允许进入发布预检：

```bash
bash ops/macos/promote-vercel-release.sh preflight \
  --deployment-id <candidate-deployment-id> \
  --deployment-url https://<candidate-immutable-url> \
  --commit <candidate-40-character-commit>
```

预检只读执行以下约束：

- Preview gate 的 deployment、immutable URL、commit、managed close 和 browser
  evidence 全部一致；
- 当前 `frontend/**` 仍与 release commit 一致；
- candidate 仍是 READY Preview，且运行时 provenance、trading/outbox 为 ready；
- 记录当前 READY Production deployment 作为唯一回滚目标；
- 当前 Production 页面、trading/outbox、GitHub OAuth capability、固定 Production
  callback 与 Secure OAuth state Cookie 都是健康基线。

真正切换必须显式加入 `--execute`：

```bash
bash ops/macos/promote-vercel-release.sh promote --execute \
  --deployment-id <candidate-deployment-id> \
  --deployment-url https://<candidate-immutable-url> \
  --commit <candidate-40-character-commit>
```

脚本只调用 `vercel promote`，不调用 build/deploy。它先写入随机 `promotion_id` 和
旧 Production deployment，再验证 Production alias 确实指向 candidate、四个 SPA
深链接、provenance、trading/outbox、OAuth capability、匿名 session 401 和 Funnel
未签名 REST 401。上述结构检查任一失败都会立即执行精确旧 deployment rollback
并复核 alias；回滚无法确认时保留私有状态，不会声称成功。

结构检查通过只表示 `awaiting-production-auth`，还不是 Production 验收。必须使用
新的 Production 浏览器上下文完成 GitHub 登录、Secure Cookie、CSRF/Origin 拒绝、
一次带 request ID 的最小虚拟资金写入及同 ID 重放、账本平衡、状态哈希一致、
logout 204 和旧 session 401，并把真实结果绑定 state 中随机 `promotion_id` 写入
权限为 `0600/0400` 的
`observations/production-auth-evidence.json`。证据必须在 promote 后且不超过
15 分钟，再执行：

```bash
bash ops/macos/promote-vercel-release.sh confirm
```

只有 confirm 成功才产生 `production-gate-passed`。调用 confirm 后如果 Production
smoke、账号 `qianqiu0404`、证据身份/时间或最小写对账任一失败，脚本会立即回滚并
核对旧 alias。尚未调用 confirm、浏览器验收失败或决定停止时：

```bash
bash ops/macos/promote-vercel-release.sh rollback
```

它只回滚到本次 promote 前记录的精确 deployment。脚本不会自动开始 acceptance
epoch；必须在 Production gate 通过后再单独启动，避免把未验收分钟计入 7 天窗口。

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

### 断网/断电后的交易写入补偿

启用 Recovery Gate 后，LaunchDaemon 重启 trading 只会恢复到
`transport_warmup`。operator 先用 loopback `trading-recovery status` 固定本次
market/epoch/version/sequence/hash 和 epoch 内受信任的 Production origin、immutable
Vercel deployment ID/URL、release commit、backend source digest，再用 `promote` 连续
30 秒核对 Production HTTPS recovery JSON、Vercel provenance 响应头、权威 gRPC、
runner state hash 和 outbox checkpoint。命令由
trading 进程内部 CAS，禁止手工 SQL。任一样本失败、版本变化、数据库读写不确定或
CAS 冲突都保持只读；同一 epoch 不会因网络恢复自动开放，必须重启 trading 创建新
epoch 并重新证明。Demo Maker 在 writable 前不启动，旧订单与回退后的订单通过
runner safety-cancel 撤销。

`transport_warmup` 是不可变观察窗：旧 demo-maker 订单必须在此前经 runner 撤销，
进入该 phase 后普通写、bootstrap 和 safety-cancel 都会失败。最终 promote 还会在
交易进程内立即复核 runner ready/sequence/hash/queue 与 outbox checkpoint，再执行
Coordinator CAS。operator gRPC 仅绑定显式 IP loopback，其信任边界是本机用户、受管
release 目录与 LaunchDaemon；目前无需额外共享 secret，未来若开放远程监听则必须改用
mTLS 或等价强认证。

正常写入也使用两次 fail-closed 门禁：入队时签发精确 epoch/version/phase 准入票据，
单写者执行前再读取 recovery row。回退发生后，尚在队列中的旧票据命令不会执行；已经
完成二次校验且正在提交的单个命令允许先解决提交结果，再按原 request ID 做权威查询和
恢复，不能把超时等同于未执行。

这部分目前是 `implemented / build-verified`；随机隔离数据库中的 migration/CAS/fault
与真实 loopback gRPC TransportProbe 30 秒集成已达到
`integration-verified (isolated local PostgreSQL + loopback gRPC)`。默认 flag 仍为 false，
Mac mini production PostgreSQL/epoch、实际外部 HTTPS provenance
绑定和断电故障注入仍为 `environment-pending`，完成前不得修改生产配置。

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
~/Library/Application Support/Qiu Market/observations/archive/observer-locks/
```

`latest.json` 是当前状态，JSONL 是追加式审计历史。正式汇总不是混合部署的滚动
窗口：它只接受 schema v4、epoch ID、Production origin、deployment ID、immutable
URL 和 release commit 全部匹配且 provenance 在线校验通过的样本。旧 schema、
其它 epoch 和其它 release 永远不计入；漏掉的墙钟分钟按失败处理，同一分钟的重复
样本采用“任一失败即失败”。

Observer 使用原子目录锁，并把 PID、进程启动时间和精确脚本入口共同作为 owner
身份。下一分钟触发看到真实活 owner 时只退出，不发送信号、不覆盖锁；PID 已退出、
PID 被复用或初始化锁超过 30 秒仍不完整时，会先把整个旧锁移动到上述 archive，再由
唯一的竞争者获得新锁。正常退出只清理 token 仍属于自己的锁；HUP/INT/TERM 产生
独立 evidence，SIGKILL/断电留下的锁会在下次启动时形成 `stale-lock-recovered`
证据。因此观察器可以从异常退出自愈，同时保留为什么失去一个墙钟样本的证据；归档
不能补算漏掉的分钟，正式 SLO 仍把缺失分钟计为失败。

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
