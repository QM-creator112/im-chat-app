// Package cache 封装 Redis 客户端与幂等原语。
//
// 为什么单独一个包而不是塞进 store：
// Redis 与 MySQL 的失败模式完全不同 —— MySQL 挂了就彻底不可用，
// 而 Redis 挂了应当**降级**（幂等靠数据库唯一索引兜底）。
// 把这个区别体现在包结构上，代码意图更清楚。
package cache

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const pingTimeout = 5 * time.Second

// NewClient 建立 Redis 客户端并做一次连通性检查。
func NewClient(addr, password string, db int) (*redis.Client, error) {
	cli := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       db,
	})

	ctx, cancel := context.WithTimeout(context.Background(), pingTimeout)
	defer cancel()

	if err := cli.Ping(ctx).Err(); err != nil {
		_ = cli.Close()
		return nil, fmt.Errorf(
			"连接 Redis 失败: %w\n"+
				"请检查：① Redis 是否运行（Test-NetConnection 127.0.0.1 -Port 6379）"+
				" ② .env 里的 REDIS_ADDR 是否正确", err)
	}
	return cli, nil
}

// IdempotencyStore 幂等键的读写封装。
//
// 键格式：idem:{userID}:{clientMsgID}
// 值   ：处理中时是占位符；处理完是分配到的 seq
type IdempotencyStore struct {
	cli *redis.Client
}

// NewIdempotencyStore 构造幂等存储。
func NewIdempotencyStore(cli *redis.Client) *IdempotencyStore {
	return &IdempotencyStore{cli: cli}
}

// Placeholder 表示"正在处理中"的占位值。
//
// 用 -1 而不是 0：因为 seq 从 1 开始，0 是非法值，
// 这样语义更清晰（读到 -1 = 处理中；读到 >0 = 已完成且 seq 已知）。
const Placeholder int64 = -1

// Key 拼出幂等键。
func Key(userID uint64, clientMsgID string) string {
	return fmt.Sprintf("idem:%d:%s", userID, clientMsgID)
}

// TryLock 尝试占位。
//
//   - 返回 (true, 0, nil)  → 占位成功，调用者可以继续处理
//   - 返回 (false, seq, nil) → 已存在：seq > 0 表示已完成可直接返回；
//     seq == Placeholder 表示另一个请求正在处理，调用者应让客户端稍后重试
//   - 返回 error → Redis 故障。**调用者应当降级继续**（靠数据库唯一索引兜底），
//     而不是让整个发消息流程失败
func (s *IdempotencyStore) TryLock(ctx context.Context, userID uint64, clientMsgID string, ttl time.Duration) (bool, int64, error) {
	key := Key(userID, clientMsgID)

	ok, err := s.cli.SetNX(ctx, key, Placeholder, ttl).Result()
	if err != nil {
		return false, 0, err
	}
	if ok {
		return true, 0, nil
	}

	// 占位失败 → 已经有值，读出来看看是"处理中"还是"已完成"
	val, err := s.cli.Get(ctx, key).Int64()
	if err != nil {
		// 极少数情况：SETNX 失败但 GET 读不到（键刚好过期）
		// 当作"处理中"处理，让客户端稍后重试，比误判成成功安全
		return false, Placeholder, nil
	}
	return false, val, nil
}

// SetResult 写入处理结果（seq）。
func (s *IdempotencyStore) SetResult(ctx context.Context, userID uint64, clientMsgID string, seq uint64, ttl time.Duration) error {
	return s.cli.Set(ctx, Key(userID, clientMsgID), seq, ttl).Err()
}

// Release 释放占位。
//
// ⚠️ **这一步绝对不能漏**：
// 如果落库失败却不释放占位，这个键会在 Redis 里活到 TTL 到期，
// 客户端在这段时间内每次重试都读到 Placeholder → 一直返回"处理中"，
// 用户看到的现象是**消息永远发不出去，且不报错**。
//
// 这个坑我在本地真实踩过，复盘见 docs/postmortem.md。
func (s *IdempotencyStore) Release(ctx context.Context, userID uint64, clientMsgID string) error {
	return s.cli.Del(ctx, Key(userID, clientMsgID)).Err()
}
