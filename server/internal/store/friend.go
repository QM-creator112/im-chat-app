package store

import (
	"context"
	"database/sql"

	"github.com/QM-creator112/im-chat-app/server/internal/model"
)

// FriendStore 好友关系数据访问。
//
// 表结构：friendships(user_id, friend_id, status)，主键 (user_id, friend_id)。
// 关系**双向存两条**（A→B 与 B→A），这样查询"我的好友"只需一次索引扫描，
// 不用写 OR 条件、也不用 UNION。
type FriendStore struct {
	db *sql.DB
}

// NewFriendStore 构造 FriendStore。
func NewFriendStore(db *sql.DB) *FriendStore {
	return &FriendStore{db: db}
}

// AddBidirectional 建立双向好友关系。
//
// 用 ON DUPLICATE KEY UPDATE 而不是先查后插：
// 重复加好友是**幂等**的正常操作（用户可能连点两次，或两边同时加），
// 不该报错。
func (s *FriendStore) AddBidirectional(ctx context.Context, userID, friendID uint64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// 两条边必须在**同一个事务**里写：
	// 否则只写成功一条时会出现"我看到他是好友，他看不到我"的不一致状态。
	const q = `INSERT INTO friendships (user_id, friend_id, status)
	           VALUES (?, ?, 1)
	           ON DUPLICATE KEY UPDATE status = 1`
	for _, p := range [][2]uint64{{userID, friendID}, {friendID, userID}} {
		if _, err := tx.ExecContext(ctx, q, p[0], p[1]); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ListFriendIDs 返回好友 ID 列表（只取 ID，给在线状态广播用）。
//
// 单独一个只取 ID 的方法，是因为在线状态广播在主链路上，
// 不需要把好友的昵称、头像都查出来 —— 少查字段、少建对象。
func (s *FriendStore) ListFriendIDs(ctx context.Context, userID uint64) ([]uint64, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT friend_id FROM friendships WHERE user_id = ? AND status = 1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	ids := make([]uint64, 0, 16)
	for rows.Next() {
		var id uint64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ListFriends 返回好友的完整资料。
func (s *FriendStore) ListFriends(ctx context.Context, userID uint64) ([]model.User, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT u.id, u.username, u.nickname, u.avatar_url, u.created_at, u.updated_at
		   FROM friendships f
		   JOIN users u ON u.id = f.friend_id
		  WHERE f.user_id = ? AND f.status = 1
		  ORDER BY u.id ASC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]model.User, 0, 16)
	for rows.Next() {
		var u model.User
		if err := rows.Scan(&u.ID, &u.Username, &u.Nickname,
			&u.AvatarURL, &u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// IsFriend 判断两人是否为好友。
func (s *FriendStore) IsFriend(ctx context.Context, userID, friendID uint64) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM friendships
		  WHERE user_id = ? AND friend_id = ? AND status = 1`,
		userID, friendID).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}
