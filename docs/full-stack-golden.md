# Q-M7A Full-stack PostgreSQL golden

## 问题与结果

Q-M1/Q-M2 分别证明了完全成交和部分成交/撤单，Q-M2P 证明了 PostgreSQL 跨进程恢复，Q-M5/Q-M6 又分别证明研究流和质量门；但这些证据此前不在同一个浏览器故事、同一个数据库权威和同一组进程边界中。Q-M7A 的目标是补上这条集成证据，而不是增加交易功能或启用外部 provider。

仓库根目录的唯一入口是：

```bash
make verify-full-stack-golden
```

成功运行必须同时输出：

```text
backend_a_pid=<pid-a> backend_b_pid=<pid-b> pg_head=7 snapshot=4 facts=7 trades=2 ledger_tx=9 ledger_entries=26 orders=4
PASS playwright=2 independent_qa=normal+race
cleanup_complete=true pids_stopped=true ports_closed=true temp_removed=true
```

最后一次验收使用 PostgreSQL 16.14；两个真实 Chrome Playwright 测试通过，独立 Go 证据测试的普通与 race 版本通过。所有价格、数量、费用和账本金额仍使用项目既有定点整数/规范十进制格式，没有引入资金 `float64`。

## 进程与数据流

```text
Chrome -> production Vue preview -> stable loopback coordinator/API
                                      |  auth + CSRF + REST/WebSocket
                                      v
                          gateway -> loopback gRPC -> backend A
                                                    -> backend B after restart
                                      |                    |
                                      +---- PostgreSQL 16.14+

Chrome -> research/data-quality GET -> real handlers/adapters
                                      -> verified-TLS loopback fixture
                                         (official response shapes only)
```

PostgreSQL、TLS fixture、coordinator、Vue、backend A 和 backend B 都有独立 PID；端口由脚本动态分配，只绑定 `127.0.0.1`。coordinator 是浏览器的稳定边界。A→B 切换期间，短生命周期交易 REST GET 在有界、context-aware 的 gate 上等待 B ready；WebSocket 不持切换锁，可以断线后重连。control 只有在 B 的 gRPC sequence/state hash 与 PostgreSQL durable state 一致后才返回。

TLS fixture 使用每次运行新生成的本地证书和显式 CA；客户端禁用环境代理、拒绝 redirect，不允许任意 origin。fixture、provider、研究和质量请求不携带真实 key，也不访问公网。

## 固定交易故事与 PostgreSQL 权威

一个 buyer 从 `3000 USDT` 开始，seller 从 `0.03 BTC` 开始：

1. buyer 提交 `60000 × 0.01 BTC` 限价买单并完全成交。buyer 为 maker，BTC 手续费为 `0.00001`；seller 为 taker，USDT 手续费为 `1.2`。
2. buyer 再提交 `60000 × 0.02 BTC` 限价买单，先成交 `0.01 BTC`。此时 buyer 为 `1200 USDT available + 600 held + 0.01998 BTC`。
3. backend A 已在 sequence 4 保存标准 snapshot；部分成交把 durable head 推到 sequence 6，但不覆盖 snapshot。脚本强制终止 A，确保恢复证据不是优雅退出时的新快照。
4. 全新 PID 的 backend B 从 snapshot 4 加 event tail 5..6 恢复到相同 sequence、state hash、余额、订单、成交和账本。
5. Vue 撤销余下 `0.01 BTC`，释放 `600 USDT`；相同 cancel request ID 重放不增加 sequence、facts、trade 或 ledger。

最终 buyer 为 `1800 USDT available / 0 held / 0.01998 BTC`；seller 为 `0.01 BTC / 1197.6 USDT`；平台费用为 `0.00002 BTC + 2.4 USDT`。BTC 与 USDT 分别在 buyer、seller、held 和 platform fee 之间守恒。PostgreSQL 最终权威数字为 head 7、snapshot 4、facts 7、trades 2、ledger transactions 9、ledger entries 26、orders 4。证据返回排序后的 order/trade/transaction/entry 引用，独立 QA 重新计算守恒、exact-once、snapshot+tail 和 replay digest，而不是只相信汇总布尔值。

## 研究、质量与故障恢复

研究流真实穿过 `researchsignal` adapter 和 HTTP handler，覆盖 `fresh / legacy / empty / degraded / stale`；只有 verified empty 能显示空态，degraded 不得伪装为空。fixture summary 返回 production contract 要求的五个 allowlisted source，场景切换推进 deterministic clock 并强制 adapter revalidation。

质量故事使用真实 Binance/CoinGlass fixture mapping、`qualityadapters` 和 `quality.Monitor`，依次验证：

- healthy；
- Binance 429 和精确 `Retry-After: 1`；
- CoinGlass 502；
- provider timeout；
- stale、future、conflict hard fault；
- cache hit 与 no-data 不推进恢复；
- 一窗 healthy 后再 fault 会清零；
- 三个新的健康窗口依次进入 recovering 1/3、2/3、healthy。

每个故障保留 HTTP observation、normalizer typed error、capability、hard-fault reason 和窗口分母。研究、质量和 provider 读取不能修改 reference、订单、资金或账本；runtime spy 与静态依赖门同时断言这些写入为零。

## 许可与非生产边界

此门证明技术链路，不授予数据再展示权：

- Binance production adapter 的许可仍是 `unknown`。只有 full-stack fixture 复制出的 evidence 带 `golden_only_license_approved_not_production` 假设，用于演示技术 quarantine/recovery；UI 和最终 evidence 必须常显该假设。
- CoinGlass 始终 `restricted / not_live`，不读取 key、不请求线上、不归档或公开展示原始响应。
- xiuqiu research 始终 `unknown`，事件固定 `executable=false`。
- 所有来源的 trade/reference/matcher/ledger eligibility 固定为 false；确定性交易参考价来自独立 market-data fixture，不来自 research 或 quality。

因此本命令的 PASS 不能解释为真实 provider 已启用、许可已批准、生产恢复已演练或真实资金可用。

## 一键运行、安全与清理

脚本不会读取项目 `.env`。PostgreSQL 二进制发现顺序固定为：显式 `QIU_TEST_POSTGRES_BIN_DIR`、当前 `PATH`、工作区内唯一已验证缓存；版本必须精确为 16.14。找不到时 fail closed，并且不会下载、Homebrew 安装或连接共享数据库。

每次运行会创建一次性 data/socket/temp/cache 目录、动态端口和 `0600` manifest。子进程使用最小环境，浏览器 HOME 指向临时目录；PostgreSQL DSN 只注入需要它的进程，不输出到日志。退出、失败、INT 或 TERM 都进入同一 trap：按精确 PID 有界 TERM，超时后 KILL，再验证全部 PID 已停止、五个端口已关闭、临时根已删除。旧 backend PID 只有在仍属当前 coordinator 时才会终止，避免 PID 重用误杀。

若命令失败，先读它打印的 fixture/coordinator/preview/PostgreSQL 日志尾部；不要复用临时数据库或把 `.env` DSN 传给 harness。成功也必须看到四个 cleanup 布尔值全为 true，只有测试 PASS 没有清理证据不算完成。

## Owner 60 秒说明

> 这条门把 Vue、认证/CSRF、HTTP、gRPC、真实 PostgreSQL、runner、journal、研究 adapter 和质量 Monitor 放进同一条 loopback 故事。买方先完全成交一单，再让第二单部分成交；数据库只留 sequence 4 的快照，backend A 在 head 6 被强杀，新的 PID B 必须用 snapshot 加 tail 恢复相同 hash，随后 Vue 撤掉余量并用同一个 request ID 重放。最终是 head 7、7 facts、2 trades、9 笔账本交易、26 entries、4 orders，BTC/USDT 含费用守恒。研究和质量又跑完 empty/degraded、429/5xx/timeout/stale/future/conflict、cache/no-data 和三窗恢复，但 runtime spy 证明它们没写交易或参考价。所有进程、端口和临时 PG 都由 trap 清掉。它证明本地 production-like 集成，不代表外部数据许可或生产上线。

## Q-M8 建议

Q-M8 应聚焦“可持续门禁”，不再扩展交易状态机：

1. 在受控 CI runner 固定提供已校验的 PostgreSQL 16.14 artifact/cache，运行本门并保留脱敏的 JUnit、Playwright trace 和 evidence summary；不得在测试中临时下载未知二进制。
2. 增加重复运行/失败注入 soak，证明 transition gate、WebSocket reconnect、trap 和 PID/端口清理在连续运行中稳定，仍不连接生产数据库。
3. 把 external provider activation 单独保留为 owner 输入门：Binance/CoinGlass/xiuqiu 的许可、套餐、再展示与存档批准完成前，不把 golden-only license assumption 带入生产配置。
4. 生产恢复演练另开里程碑，使用脱敏备份、版本化 release candidate、回滚和观察窗；本地 full-stack PASS 不能替代 Mac mini/Vercel/OAuth/备份恢复验收。
