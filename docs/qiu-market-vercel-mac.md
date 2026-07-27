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

## 2. 安装常驻服务

先停止开发启动器，避免占用 9092/9094：

```bash
make dev-stop
make mac-production-install
make mac-production-status
```

五个 LaunchAgent 为 `com.qiumarket.trading|api|crawler|worker|dex`。它们使用固定构建
的私有 binary、失败自动重启和 10 秒退避；日志位于
`~/Library/Application Support/Qiu Market/logs`。卸载只停止 LaunchAgent，不删除
私有配置、binary 或日志：

```bash
bash ops/macos/manage-services.sh uninstall
```

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

项目名固定为 `qiu-market`，Root Directory 为 `frontend`，Node 为 24.x。
Preview 与 Production 使用同一组三个变量。先从本地精确 commit 构建 Preview，
完成页面、API、WebSocket、鉴权和恢复验收后，再将同一 deployment promote 到
Production，不重新构建。

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

## 6. 24/48/72 小时生产观察

生产观察器是只读验收定时器，不是第六个业务服务。它每 5 分钟检查：

- Production 页面和 Vercel BFF；
- Funnel `/healthz` 以及未签名 REST 必须返回 401；
- 虚拟交易状态必须为 `ready`、队列无错误；
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
```

证据保存在私有运行目录，不进入仓库：

```text
~/Library/Application Support/Qiu Market/observations/latest.json
~/Library/Application Support/Qiu Market/observations/production-soak.jsonl
```

`latest.json` 是当前状态，JSONL 是追加式审计历史。顶层 `status = observing`
表示当前检查通过但长窗口仍在积累；只有六个 provider/window 组合全部通过才成为
`passed`，当前检查失败则为 `failed`。停止观察器不会删除历史：

```bash
bash ops/macos/manage-observer.sh uninstall
```
