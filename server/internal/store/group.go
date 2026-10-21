package store

import (
	"context"
)

// 群成员管理（Sprint 5）。
//
// 方法定义在 ConversationStore 上（Go 允许同一包内把方法分散到多个文件），
// 单独放一个文件只是为了让「群管理」这块逻辑更容易被找到。

// AddMembers 向群聊添加成员，返回真正新增的人数。
//
// 用 INSERT IGNORE 而不是先查后插：
//   - 重复拉人是**幂等**的正常操作（群主可能重复点、或两个人同时拉同一个人）
//   - 让数据库的唯一键（PK conversation_id+user_id）来做去重，代码里不写竞态
//
// 注意：调用方必须先校验「操作者是群成员」「会话是群聊」——
// store 层只负责数据，不做业务判断（架构纪律）。
func (s *ConversationStore) AddMembers(ctx context.Context, convID uint64, userIDs []uint64) (int64, error) {
	if len(userIDs) == 0 {
		return 0, nil
	}

	// 一次 INSERT 多行：一次网络往返 + 一次事务写入。
	// 逐个单插在成员多时会明显变慢。
	q := `INSERT IGNORE INTO conversation_members (conversation_id, user_id) VALUES `
	args := make([]any, 0, len(userIDs)*2)
	for i, uid := range userIDs {
		if i > 0 {
			q += ","
		}
		q += "(?, ?)"
		args = append(args, convID, uid)
	}

	res, err := s.db.ExecContext(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// RemoveMember 从群聊移除成员。
//
// 返回是否真的删掉了一行 —— 用于区分「退群成功」与「本来就不在群里」。
// 不报错是刻意的：退群是幂等操作，重复退群不该失败。
func (s *ConversationStore) RemoveMember(ctx context.Context, convID, userID uint64) (bool, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM conversation_members WHERE conversation_id = ? AND user_id = ?`,
		convID, userID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// CountMembers 返回会话成员数。
func (s *ConversationStore) CountMembers(ctx context.Context, convID uint64) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM conversation_members WHERE conversation_id = ?`, convID).Scan(&n)
	if err != nil {
		return 0, err
	}
	return n, nil
}
