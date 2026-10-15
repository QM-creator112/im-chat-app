// Package store 是数据访问层。
//
// 三条边界纪律（架构文档 docs/02-设计.md）：
//  1. 只有 service 层可以访问 store 层；handler 和 ws 不许直接碰数据库
//  2. store 层只做数据存取，不含业务判断
//  3. handler / ws 只做协议转换
package store

import (
	"database/sql"
	"errors"

	"github.com/go-sql-driver/mysql"
)

// ErrNotFound 表示记录不存在。
//
// 为什么不让上层直接判 sql.ErrNoRows：
// 那样会把「数据库驱动」这个实现细节泄露到 service 层。
// 包一层之后，将来换驱动或换存储，service 层不用改。
var ErrNotFound = errors.New("记录不存在")

// IsDuplicateKey 判断是否为唯一索引冲突（MySQL 错误码 1062）。
//
// 这是**幂等设计的关键判据**：
//
//	插入 messages 时撞了 uk_sender_client，说明这条消息已经存在 ——
//	这不是错误，而是「幂等成功」，应当返回已有记录而不是报错给用户。
func IsDuplicateKey(err error) bool {
	var me *mysql.MySQLError
	return errors.As(err, &me) && me.Number == 1062
}

// IsNoRows 判断是否为「查询无结果」。
func IsNoRows(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}
