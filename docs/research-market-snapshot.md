# 研究行情快照：M2 原子数据边界

## 结果与数据流

M2-A 新增一个默认不运行、一次只执行一轮的 `market-snapshot` 命令。它从 Binance 官方 market-data-only 域读取 BTC、ETH、SOL，随后无论上游是否成功，都构造恰好 21 项覆盖状态。其余十八项在 M2-B/C 前固定为 `unavailable`，不会以缓存或 mock 补齐。

```text
Binance HTTPS -> Capture -> Validate -> deterministic snapshotId/checksum
                                      -> stdout（默认）
                                      -> signed xiuqiu-site ingest（显式 --publish）
```

Qiu Market 是唯一生产者；xiuqiu-site 只验证签名与合同并保存相同载荷。现有 crawler、`SnapshotWriter`、综合价、Redis、虚拟交易与账本均不调用这条链路。

## 决策、替代方案与代价

- **一次性命令，不接 crawler。** 被拒绝的是先把它塞进现有正式 supervisor；M2 尚未获 Production 调度授权。代价是 Preview 阶段需要人工执行，M2-C 才验证临时小时 runner。
- **整包失败也发布覆盖状态。** Binance 失败时三项报价为空、状态为 `unavailable`，仍保留完整 21 项合同；不能用上一次成功值冒充当前值。
- **确定性身份。** `snapshotId` 是去掉 `snapshotId/checksum` 后的规范 JSON SHA-256 前缀；相同事实得到相同 ID，任何字段变化得到新 ID。
- **签名推送，不给生产者数据库凭据。** 发布使用 HMAC、时间戳、nonce 和 body hash；xiuqiu-site 拥有 Neon 写入事务。代价是双方必须共享一个只用于 Preview 的高熵密钥。

## 故障、降级与恢复

| 故障 | 输出 | 恢复 |
|---|---|---|
| Binance timeout/429/5xx/坏 JSON | 三个加密资产 `unavailable`，无报价 | 下一次命令重新采集 |
| 未来时间或非法十进制 | 对应资产 `unavailable` | 上游恢复后重新采集 |
| 合同/checksum 失败 | 命令失败，不发布 | 修正生产者代码 |
| HMAC/网络/接收端失败 | 发布失败，不改变 Neon current pointer | 使用同一事实安全重试 |

## 关键入口与顺序

1. `marketdata/researchsnapshot/binance.go`：受限 HTTPS 获取、字段与来源时间校验。
2. `marketdata/researchsnapshot/contract.go`：21 资产合同、规范 JSON、checksum、snapshotId。
3. `marketdata/researchsnapshot/publisher.go`：签名精确请求体并发送到唯一 ingest 路由。
4. `cmd/market-services/research_snapshot.go`：一次性 CLI；默认只向 stdout 输出，显式 `--publish` 才发送。

## 验证与证据等级

```bash
go build ./...
go vet ./...
go test ./...
go test -race ./marketdata/researchsnapshot
go run ./cmd/market-services market-snapshot
git diff --check
```

- **代码实现**：21 资产、确定性身份、官方 Binance 固定来源、固定 Preview 接收端、HMAC 和默认不发布已经进入本分支。
- **本地验证**：构建、vet、全量测试、race、真实 Binance 一次采集和 diff 检查可以在隔离 worktree 复现。
- **environment-pending**：固定 Vercel Preview alias、GitHub OAuth App、Preview-only secrets、Neon Preview migration 与端到端 publish 尚未获单独授权，因此不能描述为已联调。
- **Production**：没有实现或验证任何 Production 调度、Production 数据库写入或正式域名发布。

## M2-B 前置合同升级

M2-A 的五分钟新鲜度规则只覆盖每个 Binance 资产的一条实时行情。进入 M2-B 前，生产者与消费者必须共同升级为按市场、provider 和 role 判断新鲜度：同一美股资产需要允许 `analysis/internal_non_display` 小时价与 `display/private` EOD 价共存，休市 EOD 不得套用实时五分钟阈值；顶层 partial 状态必须从 coverage 推导，不能从 quote 数量推导。完成该合同演进前，不得声称 M2-A 已支持双角色或 EOD 行情。

## 术语

| 术语 | 准确含义 | 大白话 | 位置 |
|---|---|---|---|
| 原子快照 | current pointer 只能指向完整的一批 21 项事实 | 不是一张张照片拼到一半就给人看 | snapshot contract / Neon transaction |
| deterministic ID | 相同规范载荷的哈希身份相同 | 内容指纹 | `Finalize` |
| fail closed | 不满足来源或合同就显式不可用 | 宁可空着也不猜 | `Capture` / `Validate` |
| HMAC | 共享密钥对精确请求体的完整性证明 | 接收端能确认是谁发的且没被改 | `Publisher` |

## Owner 60 秒解释

> `market-snapshot` 是独立的一次性研究采集器。它只从 Binance 官方只读域拿 BTC、ETH、SOL，生成固定 21 项覆盖；没接入的十八项或失败的来源都写 unavailable，不做缓存兜底。合同用规范 JSON 算 checksum 和 snapshotId，显式 publish 时再用 HMAC 保护时间戳、nonce 和 body hash，把同一 JSON 发给 xiuqiu-site。它不碰现有 crawler、Redis、交易或账本，也没有 Production 调度。

## 闭卷自检

1. 为什么 Binance 整体失败仍然要输出 21 项 coverage？
2. 为什么 `snapshotId` 不使用随机 UUID？
3. 为什么生产者通过签名 API 写 Neon，而不是持有数据库管理员连接串？
4. `observedAt`、`asOf` 和 `generatedAt` 分别是什么？
5. 为什么 M2-A 不把命令接进现有 crawler supervisor？
