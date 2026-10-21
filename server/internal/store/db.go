package store

import (
	"context"
	"database/sql"
	"fmt"
	"runtime"
	"time"

	// MySQL 驱动：下划线导入是为了执行它的 init() 注册 "mysql" driver
	_ "github.com/go-sql-driver/mysql"
)

// 默认连接池参数。
//
// 这几个值不是拍脑袋定的：
const (
	// DefaultMaxOpenConns 最大打开连接数。
	//
	// 25 是"本地单机 + 单人开发"够用的值。**但它是一个已知的瓶颈**：
	// 压测实测 5000 个并发发送者抢这 25 个连接，排队时间超过请求的
	// context deadline，产生了 16835 次 `context deadline exceeded`。
	// 所以现在它可以通过环境变量 DB_MAX_OPEN 调整（默认仍为 25，行为不变）。
	DefaultMaxOpenConns = 25

	// DefaultMaxIdleConns 最大空闲连接：太少会导致频繁建连。
	DefaultMaxIdleConns = 10

	// DefaultConnMaxLifetime 连接最大寿命。
	//
	// ⚠️ 这一项是**踩过坑才加的**：MySQL 服务端默认 wait_timeout 是 8 小时，
	// 会主动断开长时间空闲的连接，而 Go 的连接池并不知道，
	// 于是第一次用到这条"死连接"时就报 `driver: bad connection` / `invalid connection`。
	// 把寿命限制在 3 分钟，强制在服务端断开之前主动换新连接。
	DefaultConnMaxLifetime = 3 * time.Minute

	pingTimeout = 5 * time.Second
)

// PoolOptions 连接池参数。零值字段会被填成默认值。
type PoolOptions struct {
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
}

// withDefaults 把零值补成默认值，并做基本的安全夹取。
//
// 为什么要夹取：如果环境变量配错（比如 DB_MAX_OPEN=0 或负数），
// database/sql 里 0 表示"无限制"，反而会把 MySQL 打到 max_connections 上限。
// 这类配置错误应该在启动时就被纠正，而不是等到线上才发现。
func (o PoolOptions) withDefaults() PoolOptions {
	if o.MaxOpenConns <= 0 {
		o.MaxOpenConns = DefaultMaxOpenConns
	}
	if o.MaxOpenConns > 1000 {
		o.MaxOpenConns = 1000
	}
	if o.MaxIdleConns <= 0 {
		o.MaxIdleConns = DefaultMaxIdleConns
	}
	// 空闲连接不能超过总连接数，否则 database/sql 会自动把它压低但不报错，
	// 配置看着生效了其实没有
	if o.MaxIdleConns > o.MaxOpenConns {
		o.MaxIdleConns = o.MaxOpenConns
	}
	if o.ConnMaxLifetime <= 0 {
		o.ConnMaxLifetime = DefaultConnMaxLifetime
	}
	return o
}

// Open 用默认连接池参数建立连接。测试与工具代码用它。
func Open(dsn string) (*sql.DB, error) {
	return OpenWithPool(dsn, PoolOptions{})
}

// OpenWithPool 用指定连接池参数建立 MySQL 连接池，并做一次连通性检查。
func OpenWithPool(dsn string, opts PoolOptions) (*sql.DB, error) {
	opts = opts.withDefaults()

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}

	db.SetMaxOpenConns(opts.MaxOpenConns)
	db.SetMaxIdleConns(opts.MaxIdleConns)
	db.SetConnMaxLifetime(opts.ConnMaxLifetime)

	ctx, cancel := context.WithTimeout(context.Background(), pingTimeout)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf(
			"连接 MySQL 失败: %w\n"+
				"请检查：① MySQL 服务是否运行 ② .env 里的 DB_* 是否正确 "+
				"③ 是否已执行 server/migrations/000_bootstrap.sql 建库建账号", err)
	}

	return db, nil
}

// PoolStatus 当前连接池的观测值。
//
// 用途：`/healthz` 里暴露出去，压测和排障时能直接看到
// "连接池是不是被打满了" —— 这比看日志猜快得多。
type PoolStatus struct {
	MaxOpenConns   int   `json:"maxOpenConns"`
	OpenConns      int   `json:"openConns"`
	InUse          int   `json:"inUse"`
	Idle           int   `json:"idle"`
	WaitCount      int64 `json:"waitCount"`      // 累计等待过连接池的次数
	WaitDurationMS int64 `json:"waitDurationMs"` // 累计等待时长
}

// Stat 读取连接池当前状态。
func Stat(db *sql.DB) PoolStatus {
	if db == nil {
		return PoolStatus{}
	}
	s := db.Stats()
	return PoolStatus{
		MaxOpenConns:   s.MaxOpenConnections,
		OpenConns:      s.OpenConnections,
		InUse:          s.InUse,
		Idle:           s.Idle,
		WaitCount:      s.WaitCount,
		WaitDurationMS: s.WaitDuration.Milliseconds(),
	}
}

// SuggestMaxOpenConns 按 CPU 核数给出一个经验上的起始值。
//
// 注意：这只是"起始值"，真正的值必须靠压测定。
// 公式取 4×核数，原因是数据库操作大部分时间在等 IO，
// 让连接数略多于核数能提高吞吐；但太多会让 MySQL 侧排队更严重。
func SuggestMaxOpenConns() int {
	n := runtime.NumCPU() * 4
	if n < 25 {
		n = 25
	}
	if n > 200 {
		n = 200
	}
	return n
}
