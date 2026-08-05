package redis

import (
	"context"
	"strconv"
	"time"

	"github.com/ethereum/go-ethereum/log"
)

// HeartbeatKey 是长驻进程（rpc / crawler / worker / dex / dw）证明自己存活的 Redis key。
// System 页的 overview 接口只读这个 key 判定进程状态——进程无需暴露任何端口，
// 也不需要额外组件（Redis 本就是项目依赖）。
func HeartbeatKey(role string) string {
	return "market:heartbeat:" + role
}

// StartHeartbeat 每 interval 刷新一次 market:heartbeat:<role>（值为当前毫秒时间戳，
// 进程死亡后 key 在 TTL 内自然过期，overview 据此判定 Stopped）。
// 写失败只告警：心跳是观测手段，不能反过来拖垮业务进程。
// 调用方传入进程级 ctx，进程退出时 goroutine 随之结束。
func (c *Client) StartHeartbeat(ctx context.Context, role string, interval, ttl time.Duration) {
	beat := func() {
		bctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		if err := c.Set(bctx, HeartbeatKey(role), strconv.FormatInt(time.Now().UnixMilli(), 10), ttl); err != nil {
			log.Warn("heartbeat write failed", "role", role, "err", err)
		}
	}
	beat()
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				beat()
			}
		}
	}()
}
