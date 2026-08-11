package redis

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

type Config struct {
	Address  string
	Password string
	DB       int
}

type Client struct {
	rdb *redis.Client
}

func New(cfg Config) (*Client, error) {
	rdb := redis.NewClient(&redis.Options{
		Addr:         cfg.Address,
		Password:     cfg.Password,
		DB:           cfg.DB,
		PoolSize:     50,
		MinIdleConns: 10,
		DialTimeout:  5 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
	})

	//  真正启动前先确认 Redis 可连通。
	//如果连不上，整个服务就不会继续。
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)

	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, err
	}
	return &Client{rdb: rdb}, nil
}

func (c *Client) Set(ctx context.Context, key string, value any, expiration time.Duration) error {
	return c.rdb.Set(ctx, key, value, expiration).Err()
}

func (c *Client) Get(ctx context.Context, key string) (string, error) {
	return c.rdb.Get(ctx, key).Result()
}

func (c *Client) Del(ctx context.Context, keys ...string) error {
	return c.rdb.Del(ctx, keys...).Err()
}

func (c *Client) Exists(ctx context.Context, key string) (bool, error) {
	n, err := c.rdb.Exists(ctx, key).Result()
	return n > 0, err
}

func (c *Client) Incr(ctx context.Context, key string) (int64, error) {
	return c.rdb.Incr(ctx, key).Result()
}

func (c *Client) SMembers(ctx context.Context, key string) ([]string, error) {
	return c.rdb.SMembers(ctx, key).Result()
}

func (c *Client) HSet(ctx context.Context, key string, values ...interface{}) error {
	return c.rdb.HSet(ctx, key, values...).Err()
}

func (c *Client) HGetAll(ctx context.Context, key string) (map[string]string, error) {
	return c.rdb.HGetAll(ctx, key).Result()
}

func (c *Client) ZAdd(ctx context.Context, key string, score float64, member any) error {
	return c.rdb.ZAdd(ctx, key, redis.Z{
		Score:  score,
		Member: member,
	}).Err()
}

func (c *Client) ZRem(ctx context.Context, key string, members ...any) error {
	return c.rdb.ZRem(ctx, key, members...).Err()
}

func (c *Client) ZRevRange(ctx context.Context, key string, start, stop int64) ([]string, error) {
	return c.rdb.ZRevRange(ctx, key, start, stop).Result()
}

// ZScorePair 是 ZSET 读榜结果：member + 其 score（本项目里是 24h 涨跌幅百分比）。
type ZScorePair struct {
	Member string
	Score  float64
}

func (c *Client) ZCard(ctx context.Context, key string) (int64, error) {
	return c.rdb.ZCard(ctx, key).Result()
}

// ZRangeWithScores 按 score 升序取 [start, stop] 区间（跌幅榜用）。
func (c *Client) ZRangeWithScores(ctx context.Context, key string, start, stop int64) ([]ZScorePair, error) {
	zs, err := c.rdb.ZRangeWithScores(ctx, key, start, stop).Result()
	return zPairs(zs), err
}

// ZRevRangeWithScores 按 score 降序取 [start, stop] 区间（涨幅榜用）。
func (c *Client) ZRevRangeWithScores(ctx context.Context, key string, start, stop int64) ([]ZScorePair, error) {
	zs, err := c.rdb.ZRevRangeWithScores(ctx, key, start, stop).Result()
	return zPairs(zs), err
}

// ZScores returns only members that currently exist in the ZSET. A genuine
// score of zero is therefore distinguishable from a missing rank value.
func (c *Client) ZScores(ctx context.Context, key string, members []string) (map[string]float64, error) {
	result := make(map[string]float64, len(members))
	if len(members) == 0 {
		return result, nil
	}
	pipe := c.rdb.Pipeline()
	cmds := make(map[string]*redis.FloatCmd, len(members))
	for _, member := range members {
		cmds[member] = pipe.ZScore(ctx, key, member)
	}
	_, err := pipe.Exec(ctx)
	if err != nil && err != redis.Nil {
		return nil, err
	}
	for member, cmd := range cmds {
		score, cmdErr := cmd.Result()
		if cmdErr == nil {
			result[member] = score
		} else if cmdErr != redis.Nil {
			return nil, cmdErr
		}
	}
	return result, nil
}

func zPairs(zs []redis.Z) []ZScorePair {
	pairs := make([]ZScorePair, 0, len(zs))
	for _, z := range zs {
		member, _ := z.Member.(string)
		pairs = append(pairs, ZScorePair{Member: member, Score: z.Score})
	}
	return pairs
}

func (c *Client) Close() error {
	return c.rdb.Close()
}

func (c *Client) TryLock(ctx context.Context, key string, value string, expiration time.Duration) (bool, error) {
	return c.rdb.SetNX(ctx, key, value, expiration).Result()
}

// UnlockIfValue releases a lease only while the caller still owns it. The
// compare-and-delete must be one Redis operation: a GET followed by DEL could
// delete a new owner's lock after the previous lease expired.
func (c *Client) UnlockIfValue(ctx context.Context, key, value string) (bool, error) {
	result, err := c.rdb.Eval(ctx, `
if redis.call("GET", KEYS[1]) == ARGV[1] then
  return redis.call("DEL", KEYS[1])
end
return 0
`, []string{key}, value).Int64()
	return result == 1, err
}

func (c *Client) Unlock(ctx context.Context, key string) error {
	return c.rdb.Del(ctx, key).Err()
}

// Eval exposes a bounded atomic primitive to packages that need to commit
// related Redis keys without exposing the underlying client.
func (c *Client) Eval(ctx context.Context, script string, keys []string, args ...any) (any, error) {
	return c.rdb.Eval(ctx, script, keys, args...).Result()
}

func IsNotFound(err error) bool {
	return errors.Is(err, redis.Nil)
}

func (c *Client) Pipeline() redis.Pipeliner {
	return c.rdb.Pipeline()
}
func (c *Client) Publish(ctx context.Context, channel string, message interface{}) error {
	return c.rdb.Publish(ctx, channel, message).Err()
}

func (c *Client) Subscribe(ctx context.Context, channels ...string) *redis.PubSub {
	return c.rdb.Subscribe(ctx, channels...)
}
