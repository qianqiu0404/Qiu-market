# Unknown Outcome：90 秒闭卷口述

## 口述稿

交易系统里最危险的误判之一，是把请求超时理解成“服务端没有执行”。例如下单、
撤单或入金已经提交数据库，但响应在返回途中丢失。此时正确状态不是失败，而是
`submitted/unknown`。

我的处理分三层。第一，写操作在发送前就固定幂等身份，范围是 market、subject
account、operation 和 request ID；虚拟入金还要区分登录管理员 actor 和目标账户
subject。第二，网络错误或 502、503、504 时不自动重试，也不生成新 ID，而是把
operation、actor、request ID 和 payload 持久保存。第三，使用原 ID 查询或重放
来收敛：下单当前依赖同 ID 幂等响应或订单视图中的 client order ID；撤单先看
订单是否已终态，仍 open 或 partially filled 才用原 ID 重放；入金依赖同 ID
幂等重放后刷新余额投影。trade、held、ledger 和 request record 是完整目标事实，
但当前客户端没有这些按 request ID 的直接核对接口，不能把投影刷新写成 ledger
证明。

这和钱包的 `broadcast_unknown` 原理相同：RPC 超时不代表交易没广播，都必须
保留原身份并查询权威事实。但权威来源不同。CEX 看 venue order、trade 和 ledger；
钱包看 tx hash、mempool、receipt 和 canonical chain。钱包还要处理 confirmations、
finality、replacement 和 reorg，所以不能直接复制 CEX 状态机。

最终不变量是：不因超时产生第二个副作用；恢复后 sequence 和 state hash 一致；
资金的 available、held 与 ledger 按资产对平。

## 闭卷自测

不看上文回答：

1. 为什么 504 不能直接标记下单失败？
2. submit、cancel、fund 各自查询什么权威事实？
3. 为什么 cancel 必须考虑 partial fill？
4. 为什么 fund 的重复执行比普通读请求更危险？
5. CEX unknown 与钱包 broadcast_unknown 的两个共同点、三个差异是什么？
6. 哪三个不变量能证明恢复没有重复副作用？

掌握标准：90 秒内完整回答，能指出一个代码入口和一个测试入口，并能解释
“已提交、响应丢失、重启恢复”这条失败链。材料生成或测试通过本身不算掌握。
