package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	// MySQL 驱动：下划线导入是为了执行它的 init() 注册 "mysql" driver
	_ "github.com/go-sql-driver/mysql"
)

// 连接池参数。
//
// 这几个值不是拍脑袋定的：
const (
	maxOpenConns    = 25              // 最大打开连接数：本地单机 + 单人用，25 足够
	maxIdleConns    = 10              // 最大空闲连接：太少会导致频繁建连
	connMaxLifetime = 3 * time.Minute // ⚠️ 关键，见下方注释
	pingTimeout     = 5 * time.Second
)

// Open 建立 MySQL 连接池并做一次连通性检查。
func Open(dsn string) (*sql.DB, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}

	db.SetMaxOpenConns(maxOpenConns)
	db.SetMaxIdleConns(maxIdleConns)

	// ⚠️ 这一行是**踩过坑才加的**：
	// MySQL 服务端默认 wait_timeout 是 8 小时，会主动断开长时间空闲的连接。
	// 而 Go 的连接池并不知道，于是第一次用到这条"死连接"时就报：
	//     driver: bad connection  /  invalid connection
	// 把连接寿命限制在 3 分钟，强制在服务端断开之前主动换新连接。
	db.SetConnMaxLifetime(connMaxLifetime)

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
