package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/QM-creator112/im-chat-app/server/internal/model"
)

// ConversationStore 会话与成员数据访问。
type ConversationStore struct {
	db *sql.DB
}

// NewConversationStore 构造 ConversationStore。
func NewConversationStore(db *sql.DB) *ConversationStore {
	return &ConversationStore{db: db}
}

// GetOrCreateDM 取得或创建单聊会话，返回 conversation_id。
//
// 这是经典的「先检查后执行」（check-then-act）竞态的标准解法：
//
//	① 先查（快路径，绝大多数情况走到这里就返回了）
//	② 不存在则插 —— 唯一索引 uk_dm_key 会挡住并发的第二个请求
//	③ 撞了唯一索引说明别人刚插进去 → 再查一次，用别人创建的那条
//
// ⚠️ 关键在于：**正确性由数据库唯一索引保证，而不是靠应用层的"查过了没有"**。
// 两个请求可能同时通过 ① 的检查，只有一个能在 ② 成功。
func (s *ConversationStore) GetOrCreateDM(ctx context.Context, userA, userB uint64) (uint64, error) {
	key := model.BuildDMKey(userA, userB)

	// ① 快路径
	var id uint64
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM conversations WHERE dm_key = ?`, key).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}

	// ② 创建（会话 + 两条成员记录必须在一起，所以用事务）
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx,
		`INSERT INTO conversations (type, dm_key, next_seq) VALUES (?, ?, 0)`,
		model.ConversationTypeDirect, key)
	if err != nil {
		if IsDuplicateKey(err) {
			// ③ 并发下别人先插了 → 回滚本次，改查已有记录
			_ = tx.Rollback()
			if err2 := s.db.QueryRowContext(ctx,
				`SELECT id FROM conversations WHERE dm_key = ?`, key).Scan(&id); err2 != nil {
				return 0, err2
			}
			return id, nil
		}
		return 0, err
	}

	lastID, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	id = uint64(lastID)

	if err := insertMembers(ctx, tx, id, []uint64{userA, userB}); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

// CreateGroup 创建群聊，返回 conversation_id。
func (s *ConversationStore) CreateGroup(ctx context.Context, ownerID uint64, title string, memberIDs []uint64) (uint64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	// 群聊的 dm_key 必须是 NULL（不能是空串）：
	// MySQL 唯一索引允许多个 NULL，但只允许一个空串 ——
	// 用空串会让「第二个群」直接插不进去。这个坑在 001_init.sql 里也注释了。
	res, err := tx.ExecContext(ctx,
		`INSERT INTO conversations (type, title, owner_id, dm_key, next_seq)
		 VALUES (?, ?, ?, NULL, 0)`,
		model.ConversationTypeGroup, title, ownerID)
	if err != nil {
		return 0, err
	}
	lastID, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	id := uint64(lastID)

	// 群主自动成为成员
	all := make([]uint64, 0, len(memberIDs)+1)
	all = append(all, ownerID)
	for _, m := range memberIDs {
		if m != ownerID {
			all = append(all, m)
		}
	}
	if err := insertMembers(ctx, tx, id, all); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

// insertMembers 批量插入成员记录。
//
// 用单条 INSERT ... VALUES (...),(...) 而不是循环单插：
// 一次网络往返、一次事务写入，成员多时差别明显。
func insertMembers(ctx context.Context, tx *sql.Tx, convID uint64, userIDs []uint64) error {
	if len(userIDs) == 0 {
		return nil
	}
	q := `INSERT INTO conversation_members (conversation_id, user_id) VALUES `
	args := make([]any, 0, len(userIDs)*2)
	for i, uid := range userIDs {
		if i > 0 {
			q += ","
		}
		q += "(?, ?)"
		args = append(args, convID, uid)
	}
	_, err := tx.ExecContext(ctx, q, args...)
	return err
}

// IsMember 判断用户是否为会话成员。
//
// 所有「读写某会话的消息」的接口都必须先过这一关 —— 否则任何人猜到
// conversation_id 就能读别人的聊天记录。这是本项目最重要的一处鉴权。
func (s *ConversationStore) IsMember(ctx context.Context, convID, userID uint64) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM conversation_members
		  WHERE conversation_id = ? AND user_id = ?`,
		convID, userID).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// ListMemberIDs 返回会话的所有成员 ID。
func (s *ConversationStore) ListMemberIDs(ctx context.Context, convID uint64) ([]uint64, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT user_id FROM conversation_members WHERE conversation_id = ?`, convID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	ids := make([]uint64, 0, 8)
	for rows.Next() {
		var id uint64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// GetByID 按 ID 读会话。
func (s *ConversationStore) GetByID(ctx context.Context, convID uint64) (*model.Conversation, error) {
	var c model.Conversation
	var dmKey sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT id, type, title, owner_id, dm_key, next_seq, created_at, updated_at
		   FROM conversations WHERE id = ?`, convID).
		Scan(&c.ID, &c.Type, &c.Title, &c.OwnerID, &dmKey, &c.NextSeq, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if dmKey.Valid {
		c.DMKey = &dmKey.String
	}
	return &c, nil
}

// MaxSeq 返回会话当前分配到的最大 seq（没有消息时为 0）。
//
// 直接读 conversations.next_seq 而不是 MAX(messages.seq)：
// 前者是分配器的当前值，一次主键查询；后者要扫索引。语义上二者一致
// （因为 next_seq 与最后一次成功提交的 seq 相等）。
func (s *ConversationStore) MaxSeq(ctx context.Context, convID uint64) (uint64, error) {
	var seq uint64
	err := s.db.QueryRowContext(ctx,
		`SELECT next_seq FROM conversations WHERE id = ?`, convID).Scan(&seq)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, err
	}
	return seq, nil
}

// ListForUser 返回某用户的会话列表（含最后一条消息与未读数），按最后消息时间倒序。
//
// 几个实现细节值得说明：
//
//  1. 单聊的 title 在库里是空串，展示时要用对方的昵称 → 用 COALESCE + 子查询补上。
//  2. 最后一条消息用 `m.seq = c.next_seq` 取：next_seq 是分配器当前值，
//     恰好等于最后一条已提交消息的 seq，走 uk_conv_seq 索引，是精确查找。
//  3. 未读数 = next_seq - last_read_seq，用 GREATEST(...,0) 兜住负数
//     （防御 last_read_seq 因异常超过 next_seq 时显示"-3 条未读"）。
func (s *ConversationStore) ListForUser(ctx context.Context, userID uint64, limit int) ([]model.ConversationListItem, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}

	const q = `
SELECT
    c.id,
    c.type,
    COALESCE(NULLIF(c.title, ''), (
        SELECT u.nickname
          FROM conversation_members cm2
          JOIN users u ON u.id = cm2.user_id
         WHERE cm2.conversation_id = c.id AND cm2.user_id <> ?
         LIMIT 1
    ), '')                                        AS title,
    c.owner_id                                    AS owner_id,
    COALESCE(m.content, '')                       AS last_msg,
    COALESCE(m.created_at, c.created_at)          AS last_msg_at,
    c.next_seq                                    AS latest_seq,
    cm.last_read_seq                              AS my_last_read_seq,
    GREATEST(c.next_seq - cm.last_read_seq, 0)    AS unread_count
  FROM conversation_members cm
  JOIN conversations c ON c.id = cm.conversation_id
  LEFT JOIN messages m ON m.conversation_id = c.id AND m.seq = c.next_seq
 WHERE cm.user_id = ?
 ORDER BY c.updated_at DESC
 LIMIT ?`

	rows, err := s.db.QueryContext(ctx, q, userID, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]model.ConversationListItem, 0, 16)
	for rows.Next() {
		var it model.ConversationListItem
		var lastAt sql.NullTime
		if err := rows.Scan(&it.ID, &it.Type, &it.Title, &it.OwnerID, &it.LastMsg, &lastAt,
			&it.LatestSeq, &it.MyLastReadSeq, &it.UnreadCount); err != nil {
			return nil, err
		}
		if lastAt.Valid {
			it.LastMsgAt = lastAt.Time.UnixMilli()
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// ListMembers 返回会话成员（含已读位置）。
func (s *ConversationStore) ListMembers(ctx context.Context, convID uint64) ([]model.Member, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT cm.user_id, u.username, u.nickname, u.avatar_url, cm.last_read_seq, cm.joined_at
		   FROM conversation_members cm
		   JOIN users u ON u.id = cm.user_id
		  WHERE cm.conversation_id = ?
		  ORDER BY cm.joined_at ASC`, convID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]model.Member, 0, 8)
	for rows.Next() {
		var m model.Member
		if err := rows.Scan(&m.UserID, &m.Username, &m.Nickname,
			&m.AvatarURL, &m.LastReadSeq, &m.JoinedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// UpdateLastRead 推进已读位置。
//
// ⚠️ 必须用 GREATEST(last_read_seq, ?) —— **只前进，不回退**。
//
// 原因：网络乱序时，"读到 5" 的上报可能比 "读到 10" 后到。
// 如果直接赋值，已读位置会从 10 退回到 5，界面上就会出现
// 「已读又变回未读」的诡异现象。
func (s *ConversationStore) UpdateLastRead(ctx context.Context, convID, userID, readSeq uint64) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE conversation_members
		    SET last_read_seq = GREATEST(last_read_seq, ?)
		  WHERE conversation_id = ? AND user_id = ?`,
		readSeq, convID, userID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		// 没有更新到行不代表失败：可能是值没变化（GREATEST 后相同）。
		// 但也有可能是「不是成员」—— 用一次存在性查询区分开。
		ok, err := s.IsMember(ctx, convID, userID)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNotMember
		}
	}
	return nil
}

// ErrNotMember 表示用户不在该会话中。
var ErrNotMember = errors.New("不是该会话成员")
